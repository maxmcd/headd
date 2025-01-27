package headd

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
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"golang.org/x/sync/errgroup"
)

type Server struct {
	clientsMu  sync.RWMutex
	clients    map[string]*ConnectedClient
	appHostsMu sync.RWMutex
	appHosts   map[string]*serverApp
	tlsConfig  *tls.Config
	cfg        *ServerConfig
}

type serverApp struct {
	appPort AppPort
	healthy bool
	client  string
	cancel  func()
}

type App struct {
	Name string

	Command string
	Args    []string

	Count int
}

type AppPort struct {
	App  App
	Port int
}

type ServerConfig struct {
	HealthCheckPeriod time.Duration
}

func NewServer(cfg *ServerConfig) *Server {
	if cfg == nil {
		cfg = &ServerConfig{
			HealthCheckPeriod: time.Second,
		}
	}
	if cfg.HealthCheckPeriod == 0 {
		cfg.HealthCheckPeriod = time.Second
	}

	tlsCert, err := tls.LoadX509KeyPair("server.crt", "server.key")
	if err != nil {
		log.Panicln(fmt.Errorf("loading certificates: %w", err))
	}

	return &Server{
		cfg:      cfg,
		clients:  make(map[string]*ConnectedClient),
		appHosts: make(map[string]*serverApp),
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

func (p *Server) ListenAndServe(ctx context.Context, clientAddr string, publicAddr string) error {
	cAddr, err := net.ResolveUDPAddr("udp", clientAddr)
	if err != nil {
		return fmt.Errorf("resolving clientAddr: %w", err)
	}
	cConn, err := net.ListenUDP("udp", cAddr)
	if err != nil {
		return fmt.Errorf("listening on clientAddr: %w", err)
	}
	pListener, err := net.Listen("tcp4", publicAddr)
	if err != nil {
		return fmt.Errorf("listening on publicAddr: %w", err)
	}
	return p.Serve(ctx, cConn, pListener)
}

func (p *Server) Serve(ctx context.Context, clientConn *net.UDPConn, publicListener net.Listener) error {
	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error { return p.ClientListen(ctx, clientConn) })
	eg.Go(func() error { return p.PublicListen(ctx, publicListener) })
	return eg.Wait()
}

func (p *Server) ClientListen(ctx context.Context, conn net.PacketConn) error {
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
		connName := conn.RemoteAddr().String()
		connClient := &ConnectedClient{
			conn:   conn,
			client: NewRPC2Client(quicConnDial(conn)),
		}

		p.clientsMu.Lock()
		if _, exists := p.clients[connName]; exists {
			p.clientsMu.Unlock()
			return fmt.Errorf("conflicting client name %q", connName)
		}
		p.clients[connName] = connClient
		p.clientsMu.Unlock()

		if err := connClient.client.Hello(); err != nil {
			return fmt.Errorf("hello failed: %w", err)
		}
	}
}

func (p *Server) PublicListen(ctx context.Context, listener net.Listener) error {
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

func (p *Server) RegisterApp(app App) (*AppPort, error) {
	name, c := p.firstClient()
	if c == nil {
		return nil, fmt.Errorf("no connected clients")
	}

	appPort, err := c.client.RegisterApp(app)
	if err != nil {
		return nil, fmt.Errorf("client.RegisterApp: %w", err)
	}
	slog.Debug("registered new app",
		"conn", c.conn.RemoteAddr().String(),
		"name", app.Name, "port", appPort.Port)
	ctx, cancel := context.WithCancel(context.Background())
	p.appHostsMu.Lock()
	p.appHosts[app.Name] = &serverApp{
		appPort: *appPort,
		client:  name,
		cancel:  cancel,
	}

	p.appHostsMu.Unlock()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(p.cfg.HealthCheckPeriod):
				if resp, err := c.client.HealthCheckApp(app.Name); err != nil {
					slog.Error("health check failed", "err", err)
				} else if !resp.Healthy {
					slog.Error("app unhealthy", "name", app.Name)
				}
			}
		}
	}()

	return appPort, nil
}

func (p *Server) firstClient() (string, *ConnectedClient) {
	p.clientsMu.RLock()
	defer p.clientsMu.RUnlock()
	for name, client := range p.clients {
		return name, client // Return first client found
	}
	return "", nil
}

func (p *Server) handleConn(ctx context.Context, publicConn net.Conn) error {
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

	p.appHostsMu.RLock()
	app, ok := p.appHosts[sni]
	p.appHostsMu.RUnlock()

	if !ok {
		return fmt.Errorf("no matching app found for %q", sni)
	}

	_, connClient := p.firstClient()
	stream, err := connClient.conn.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("opening stream failed: %w", err)
	}
	defer stream.Close()

	appAddr := netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), uint16(app.appPort.Port))
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
	if err = binary.Read(stream, binary.BigEndian, &errLen); err != nil {
		return fmt.Errorf("reading error length: %w", err)
	}
	if errLen > 0 {
		errB := make([]byte, errLen)
		if _, err := io.ReadFull(stream, errB); err != nil {
			return fmt.Errorf("error reading error: %w: %q", err, string(errB))
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
	rpcServer, err := NewRPC2Server(&quicChanListener{streamChan: rpcStreams})
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

func parseAddrFromStream(stream quic.Stream) (*netip.AddrPort, error) {
	lenB := make([]byte, 1)
	if _, err := io.ReadFull(stream, lenB); err != nil {
		return nil, fmt.Errorf("reading len byte: %w", err)
	}
	addrB := make([]byte, lenB[0])
	if _, err := io.ReadFull(stream, addrB); err != nil {
		return nil, fmt.Errorf("reading addr bytes: %w", err)
	}
	ap := &netip.AddrPort{}
	if err := ap.UnmarshalBinary(addrB); err != nil {
		return nil, fmt.Errorf("unmarshaling addr: %w", err)
	}
	return ap, nil
}

func (p *ProxyClient) handleStreamProxy(ctx context.Context, stream quic.Stream) error {
	fmt.Println("parse and dial addr")
	addr, err := parseAddrFromStream(stream)
	if err != nil {
		_ = stream.Close()
		return err
	}
	if addr.Port() == 0 && addr.Addr() == netip.AddrFrom4([4]byte{0, 0, 0, 0}) {
		_ = binary.Write(stream, binary.BigEndian, uint16(0))
		p.rpcStreams <- stream
		// Return early, this is an RPC channel.
		return nil
	}
	defer stream.Close()
	slog.Debug("dialing local app", "addr", addr)
	conn, err := net.Dial("tcp", addr.String())
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
