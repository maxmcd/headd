package tunneld

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"time"

	"github.com/maxmcd/tunneld/rpc"
	"github.com/quic-go/quic-go"
	"golang.org/x/sync/errgroup"
)

type ProxyServer struct {
	conn *ConnectedClient

	AppAddr netip.AddrPort
}

type RunningServer struct {
	eg        *errgroup.Group
	cConn     net.PacketConn
	pListener net.Listener
}

func (r *RunningServer) Wait() error {
	return r.eg.Wait()
}

func (r *RunningServer) ClientAddr() net.Addr {
	return r.cConn.LocalAddr()
}

func (r *RunningServer) PublicAddr() net.Addr {
	return r.pListener.Addr()
}

func NewProxyServer() *ProxyServer {
	return &ProxyServer{}
}

type ConnectedClient struct {
	conn   quic.Connection
	client *rpc.Client
}

func (p *ProxyServer) ListenAddr(ctx context.Context, clientAddr string, publicAddr string) (*RunningServer, error) {
	cAddr, err := net.ResolveUDPAddr("udp", clientAddr)
	if err != nil {
		return nil, fmt.Errorf("resolving clientAddr: %w", err)
	}
	cConn, err := net.ListenUDP("udp", cAddr)
	if err != nil {
		return nil, fmt.Errorf("listening on clientAddr: %w", err)
	}
	pListener, err := net.Listen("tcp4", publicAddr)
	if err != nil {
		return nil, fmt.Errorf("listening on publicAddr: %w", err)
	}

	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error { return p.ClientListen(ctx, cConn) })
	eg.Go(func() error { return p.PublicListen(ctx, pListener) })
	return &RunningServer{
		eg:        eg,
		cConn:     cConn,
		pListener: pListener,
	}, nil
}

func (p *ProxyServer) ClientListen(ctx context.Context, conn net.PacketConn) error {
	tlsCert, err := tls.LoadX509KeyPair("server.crt", "server.key")
	if err != nil {
		return fmt.Errorf("loading certificates: %w", err)
	}
	listener, err := quic.Listen(conn, &tls.Config{
		InsecureSkipVerify: true,
		Certificates:       []tls.Certificate{tlsCert},
	}, &quic.Config{
		MaxIdleTimeout:  20 * time.Second,
		KeepAlivePeriod: 10 * time.Second,
		EnableDatagrams: true,
	})
	if err != nil {
		return fmt.Errorf("listening quic failed: %w", err)
	}
	slog.Info("Running client listener", "addr", conn.LocalAddr().String())
	for {
		conn, err := listener.Accept(ctx)
		if err != nil {
			return fmt.Errorf("accepting quic connection failed: %w", err)
		}
		fmt.Println("accepted quic connection")
		controlStream, err := conn.OpenStreamSync(context.Background())
		if err != nil {
			return fmt.Errorf("accepting control stream failed: %w", err)
		}
		p.conn = &ConnectedClient{
			conn:   conn,
			client: rpc.NewClient(controlStream),
		}
		if err := p.conn.client.Hello(); err != nil {
			return fmt.Errorf("hello failed: %w", err)
		}
	}
}

func (p *ProxyServer) PublicListen(ctx context.Context, listener net.Listener) error {
	slog.Info("Running public listener", "addr", listener.Addr())
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return fmt.Errorf("accepting tcp connection failed: %w", err)
		}
		if p.conn == nil {
			fmt.Println("no connection to proxy")
			conn.Close()
			continue
		}
		go func() {
			fmt.Println("New public conn", conn)
			if err := p.handleConn(ctx, p.conn, conn); err != nil {
				fmt.Println("New public conn", conn)

				fmt.Println("error handling connection:", err)
			}
		}()
	}
}

func (p *ProxyServer) handleConn(ctx context.Context, conn *ConnectedClient, publicConn net.Conn) error {
	stream, err := conn.conn.OpenStreamSync(context.Background())
	if err != nil {
		return fmt.Errorf("opening stream failed: %w", err)
	}
	defer stream.Close()

	if err := conn.client.NewConnection(rpc.Connection{
		StreamID: int64(stream.StreamID()),
		Addr:     "localhost:4443",
	}); err != nil {
		return fmt.Errorf("new connection failed: %w", err)
	}

	b, err := p.AppAddr.MarshalBinary()
	if err != nil {
		return fmt.Errorf("marshalling app addr: %w", err)
	}

	_, _ = stream.Write([]byte{uint8(len(b))})
	if n, err := stream.Write(b); err != nil {
		return fmt.Errorf("writing to stream: %w", err)
	} else if n < len(b) {
		return fmt.Errorf("failed to write all addr bytes to stream: %s", p.AppAddr)
	}

	var errLen uint16
	_ = binary.Read(stream, binary.BigEndian, &errLen)
	if errLen > 0 {
		errB := make([]byte, errLen)
		if n, err := stream.Read(errB); err != nil {
			return fmt.Errorf("error reading error: %w: %q", err, string(errB))
		} else if n < int(errLen) {
			return fmt.Errorf("short read on stream error: %q", string(errB))
		}
		return errors.New(string(errB))
	}

	closer := make(chan error, 2)
	go copy(closer, publicConn, stream)
	go copy(closer, stream, publicConn)
	<-closer
	return nil
}

type ProxyClient struct {
}

func NewProxyClient() *ProxyClient {
	return &ProxyClient{}
}

func (p *ProxyClient) parseAndDialAddr(ctx context.Context, stream quic.Stream) (net.Conn, error) {
	lenB := make([]byte, 1)
	if _, err := stream.Read(lenB); err != nil {
		return nil, fmt.Errorf("reading len byte: %w", err)
	}
	addrB := make([]byte, lenB[0])
	ap := &netip.AddrPort{}
	if _, err := stream.Read(addrB); err != nil {
		return nil, fmt.Errorf("reading addr bytes: %w", err)
	}
	if err := ap.UnmarshalBinary(addrB); err != nil {
		return nil, fmt.Errorf("unmarshaling addr: %w", err)
	}
	conn, err := net.Dial("tcp", ap.String())
	if err != nil {
		return nil, fmt.Errorf("dialing tcp failed: %w", err)
	}
	return conn, nil
}

func (p *ProxyClient) handleStreamProxy(ctx context.Context, stream quic.Stream) error {
	defer stream.Close()
	fmt.Println("parse and dial addr")
	conn, err := p.parseAndDialAddr(ctx, stream)
	if err != nil {
		fmt.Println("Client dial err", err)
		errBytes := []byte(err.Error())
		errLen := uint16(len(errBytes))
		if err := binary.Write(stream, binary.BigEndian, errLen); err != nil {
			slog.Error("writing error to stream", "err", fmt.Errorf("writing error length: %w", err))
		}
		if _, err := stream.Write(errBytes); err != nil {
			slog.Error("writing error to stream", "err", fmt.Errorf("writing error message: %w", err))
		}
		return err
	}
	_ = binary.Write(stream, binary.BigEndian, uint16(0))
	defer conn.Close()
	closer := make(chan error, 2)
	go copy(closer, conn, stream)
	go copy(closer, stream, conn)
	select {
	case err := <-closer:
		fmt.Println("copy err", err)
		return err
	case <-ctx.Done():
		return fmt.Errorf("context cancelled")
	}
}

// Dial phones home to the server and sets up the connection to proxy traffic
// over.
func (p *ProxyClient) Dial(ctx context.Context, addr string) error {
	tlsCert, err := tls.LoadX509KeyPair("server.crt", "server.key")
	if err != nil {
		panic(err)
	}

	fmt.Println("Dialing quic", addr)
	conn, err := quic.DialAddr(ctx,
		addr,
		&tls.Config{
			InsecureSkipVerify: true,
			Certificates:       []tls.Certificate{tlsCert},
		},
		&quic.Config{
			MaxIdleTimeout:  20 * time.Second,
			KeepAlivePeriod: 10 * time.Second,
			EnableDatagrams: true,
		},
	)
	if err != nil {
		return fmt.Errorf("quic dialAddr failed: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	controlStream, err := conn.AcceptStream(ctx)
	if err != nil {
		cancel()
		return fmt.Errorf("accepting stream failed: %w", err)
	}
	go func() {
		defer controlStream.Close()
		if err := rpc.StartServer(controlStream, rpc.NewInlineServer(
			func(req int, resp *int) error {
				return nil
			},
		)); err != nil {
			cancel()
			fmt.Printf("starting server failed: %v\n", err)
		}
	}()

	for {
		stream, err := conn.AcceptStream(ctx)
		if err != nil {
			cancel()
			return fmt.Errorf("accepting stream failed: %w", err)
		}
		fmt.Println("accepting stream", stream.StreamID())
		go func() {
			if err := p.handleStreamProxy(ctx, stream); err != nil {
				slog.Error("handleStreamProxy", "err", err)
			}
		}()
	}
}

func copy(closer chan error, dst io.Writer, src io.Reader) {
	_, err := io.Copy(dst, src)
	closer <- err // connection is closed, send signal to stop proxy
}
