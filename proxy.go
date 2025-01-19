package tunneld

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/maxmcd/tunneld/rpc"
	"github.com/quic-go/quic-go"
)

type ProxyServer struct {
	publicAddr string
	clientAddr string

	conn *ConnectedClient
}

func NewProxyServer(publicAddr, clientAddr string) *ProxyServer {
	return &ProxyServer{
		publicAddr: publicAddr,
		clientAddr: clientAddr,
	}
}

type ConnectedClient struct {
	conn   quic.EarlyConnection
	client *rpc.Client
}

func (p *ProxyServer) ClientListen() error {
	tlsCert, err := tls.LoadX509KeyPair("server.crt", "server.key")
	if err != nil {
		panic(err)
	}
	listener, err := quic.ListenAddrEarly(p.clientAddr, &tls.Config{
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
	fmt.Println("Running client listener on ", p.clientAddr)
	for {
		conn, err := listener.Accept(context.Background())
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

func (p *ProxyServer) PublicListen() error {
	fmt.Println("Running public listener on ", p.publicAddr)
	listener, err := net.Listen("tcp", p.publicAddr)
	if err != nil {
		return fmt.Errorf("listening tcp failed: %w", err)
	}

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
			if err := p.handleConn(p.conn, conn); err != nil {
				fmt.Println("error handling connection:", err)
			}
		}()
	}
}

func (p *ProxyServer) handleConn(conn *ConnectedClient, publicConn net.Conn) error {
	stream, err := conn.conn.OpenStreamSync(context.Background())
	if err != nil {
		return fmt.Errorf("opening stream failed: %w", err)
	}
	if err := conn.client.NewConnection(rpc.Connection{
		StreamID: int64(stream.StreamID()),
		Addr:     "localhost:4443",
	}); err != nil {
		return fmt.Errorf("new connection failed: %w", err)
	}
	defer stream.Close()
	closer := make(chan error, 2)
	go copy(closer, publicConn, stream)
	go copy(closer, stream, publicConn)
	<-closer
	return nil
}

type ProxyClient struct {
	localAddr string

	streamErrorsLock sync.Mutex
	streamErrors     map[quic.StreamID]error

	connectionsLock sync.Mutex
	connections     map[quic.StreamID]string
}

func NewProxyClient(localAddr string) *ProxyClient {
	return &ProxyClient{
		localAddr:    localAddr,
		streamErrors: make(map[quic.StreamID]error),
		connections:  make(map[quic.StreamID]string),
	}
}

func (p *ProxyClient) handleStreamProxy(ctx context.Context, stream quic.Stream) error {
	defer stream.Close()
	conn, err := net.Dial("tcp", p.localAddr)
	if err != nil {
		return fmt.Errorf("dialing tcp failed: %w", err)
	}
	defer conn.Close()
	closer := make(chan error, 2)
	go copy(closer, conn, stream)
	go copy(closer, stream, conn)
	select {
	case err := <-closer:
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
		return fmt.Errorf("accepting stream failed: %w", err)
	}
	go func() {
		defer controlStream.Close()
		rpc.StartServer(controlStream, rpc.NewInlineServer(
			func(conn rpc.Connection, resp *int) error {
				fmt.Println("new connection", conn.StreamID, conn.Addr)
				p.connectionsLock.Lock()
				p.connections[quic.StreamID(conn.StreamID)] = conn.Addr
				p.connectionsLock.Unlock()
				return nil
			},
			func(streamID int64, err *error) error {
				fmt.Println("stream error", streamID, *err)
				p.streamErrorsLock.Lock()
				p.streamErrors[quic.StreamID(streamID)] = *err
				p.streamErrorsLock.Unlock()
				return nil
			},
			func(req int, resp *int) error {
				return nil
			},
		))
		cancel()
	}()

	for {
		stream, err := conn.AcceptStream(ctx)
		if err != nil {
			return fmt.Errorf("accepting stream failed: %w", err)
		}
		fmt.Println("accepting stream", stream.StreamID())
		go func() {
			if err := p.handleStreamProxy(ctx, stream); err != nil {
				p.streamErrorsLock.Lock()
				p.streamErrors[stream.StreamID()] = err
				p.streamErrorsLock.Unlock()
			}
		}()
	}
}
