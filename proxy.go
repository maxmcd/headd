package tunneld

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/netip"
	"time"

	"github.com/maxmcd/tunneld/pkg/synctyped"
	"github.com/quic-go/quic-go"
	"golang.org/x/sync/errgroup"
)

type ProxyServer struct {
	clients   synctyped.Map[*ConnectedClient]
	appHosts  synctyped.Map[*AppPort]
	tlsConfig *tls.Config
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
	tlsCert, err := tls.LoadX509KeyPair("server.crt", "server.key")
	if err != nil {
		log.Panicln(fmt.Errorf("loading certificates: %w", err))
	}

	return &ProxyServer{
		clients:  synctyped.Map[*ConnectedClient]{},
		appHosts: synctyped.Map[*AppPort]{},
		tlsConfig: &tls.Config{
			InsecureSkipVerify: true,
			Certificates:       []tls.Certificate{tlsCert},
		},
	}
}

type ConnectedClient struct {
	conn   quic.Connection
	client *RPC2Client
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
	listener, err := quic.Listen(conn, p.tlsConfig, &quic.Config{
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
		connName := conn.RemoteAddr().String()
		connClient := &ConnectedClient{
			conn:   conn,
			client: NewRPC2Client(quicConnDial(conn)),
		}
		fmt.Println("new conn", connName)
		_, loaded := p.clients.LoadOrStore(connName, connClient)
		if loaded {
			return fmt.Errorf("conflicting client name %q", connName)
		}
		if err := connClient.client.Hello(); err != nil {
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
		go func() {
			defer conn.Close()
			fmt.Println("New public conn", conn)
			if err := p.handleConn(ctx, conn); err != nil {
				fmt.Println("New public conn", conn)

				fmt.Println("error handling connection:", err)
			}
		}()
	}
}

func (p *ProxyServer) RegisterApp(app App) (*AppPort, error) {
	c := p.firstClient()

	appPort, err := c.client.RegisterApp(app)
	if err != nil {
		return nil, fmt.Errorf("client.RegisterApp: %w", err)
	}
	slog.Debug("registered new app",
		"conn", c.conn.RemoteAddr().String(),
		"name", app.Name, "port", appPort.Port)
	p.appHosts.Store(app.Name, appPort)
	return appPort, nil
}

func (p *ProxyServer) firstClient() *ConnectedClient {
	var out *ConnectedClient
	p.clients.Range(func(key string, value *ConnectedClient) bool {
		out = value
		return false
	})
	return out
}

func (p *ProxyServer) handleConn(ctx context.Context, publicConn net.Conn) error {
	var sni string
	sniConn := tls.Server(publicConn, &tls.Config{
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			slog.Debug("handleConn", "sni", hello.ServerName)
			sni = hello.ServerName
			return p.tlsConfig, nil
		},
	})

	if err := sniConn.Handshake(); err != nil {
		return fmt.Errorf("error handshaking %s %s: %w", publicConn.RemoteAddr(), sni, err)
	}

	app, ok := p.appHosts.Load(sni)
	if !ok {
		return fmt.Errorf("no matching app found for %q", sni)
	}

	connClient := p.firstClient()
	stream, err := connClient.conn.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("opening stream failed: %w", err)
	}
	defer stream.Close()

	appAddr := netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), uint16(app.Port))
	if err := writeAddrToStream(stream, appAddr); err != nil {
		return fmt.Errorf("writing addr to stream: %w", err)
	}

	closer := make(chan error, 2)
	go copy(closer, sniConn, stream)
	go copy(closer, stream, sniConn)
	<-closer
	return nil
}

func writeAddrToStream(stream quic.Stream, addrPort netip.AddrPort) (err error) {
	b, err := addrPort.MarshalBinary()
	if err != nil {
		return fmt.Errorf("marshalling app addr: %w", err)
	}

	_, _ = stream.Write([]byte{uint8(len(b))})
	if n, err := stream.Write(b); err != nil {
		return fmt.Errorf("writing to stream: %w", err)
	} else if n < len(b) {
		return fmt.Errorf("failed to write all addr bytes to stream: %s", addrPort)
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
	return nil
}

type ProxyClient struct {
	rpcServer  *RPC2Server
	rpcStreams chan quic.Stream
}

func NewProxyClient() (*ProxyClient, error) {
	rpcStreams := make(chan quic.Stream, 2)
	rpcServer, err := NewRPC2Server(&streamChanListener{streams: rpcStreams})
	if err != nil {
		return nil, err
	}
	return &ProxyClient{
		rpcServer:  rpcServer,
		rpcStreams: rpcStreams,
	}, nil
}

func (p *ProxyClient) Shutown() error {
	return p.rpcServer.listener.Close()
}

func (p *ProxyClient) parseAddr(ctx context.Context, stream quic.Stream) (*netip.AddrPort, error) {
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
	return ap, nil
}

func (p *ProxyClient) handleStreamProxy(ctx context.Context, stream quic.Stream) error {
	defer stream.Close()
	fmt.Println("parse and dial addr")
	addr, err := p.parseAddr(ctx, stream)
	if err != nil {
		return err
	}
	if addr.Port() == 0 && addr.Addr() == netip.AddrFrom4([4]byte{0, 0, 0, 0}) {
		fmt.Println("sending rpc stream")
		_ = binary.Write(stream, binary.BigEndian, uint16(0))
		p.rpcStreams <- stream
		return nil
	}
	slog.Debug("dialing local app", "addr", addr)
	conn, err := net.Dial("tcp", addr.String())
	if err != nil {
		return fmt.Errorf("dialing tcp failed: %w", err)
	}
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
	go func() {
		if err := p.rpcServer.ListenAndServe(); err != nil {
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
