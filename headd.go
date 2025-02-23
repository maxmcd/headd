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
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"golang.org/x/net/http2"
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

type AppInfo struct {
	Name    string
	Healthy bool
	Client  string
}

type ClientInfo struct {
	Name string
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
	eg.Go(func() error { return p.PublicListenHTTP(ctx, publicListener) })
	return eg.Wait()
}

func (p *Server) handleNewConnection(conn quic.Connection) error {
	connName := conn.RemoteAddr().String()
	go func() {
		<-conn.Context().Done()
		slog.Info("connection closed", "name", connName)
	}()
	slog.Info("New client connection", "name", connName)
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

	start := time.Now()
	slog.Info("hello", "name", connName)
	if err := connClient.client.Hello(); err != nil {
		return fmt.Errorf("hello failed: %w", err)
	}
	slog.Info("hello done", "name", connName, "duration", time.Since(start))
	return nil
}

func (p *Server) ClientListen(ctx context.Context, conn net.PacketConn) error {
	listener, err := quic.Listen(conn, p.tlsConfig, &quic.Config{
		MaxIdleTimeout:  20 * time.Second,
		KeepAlivePeriod: 10 * time.Second,
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
		go func() {
			if err := p.handleNewConnection(conn); err != nil {
				slog.Error("error handling new connection", "err", err)
			}
		}()
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

var ErrAppNotFound = errors.New("app not found")

func (p *Server) PublicListenHTTP(ctx context.Context, listener net.Listener) error {
	proxy := httputil.ReverseProxy{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				app, c := p.appClient(strings.TrimSuffix(addr, ":80"))
				if c == nil {
					return nil, ErrAppNotFound
				}
				stream, err := c.conn.OpenStreamSync(ctx)
				if err != nil {
					return nil, fmt.Errorf("conn.OpenStreamSync: %w", err)
				}
				appAddr := netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), uint16(app.appPort.Port))
				if err := writeAddrToStream(stream, appAddr); err != nil {
					return nil, fmt.Errorf("writing addr to stream: %w", err)
				}
				return &streamConn{stream: stream, ReadWriteCloser: stream}, nil
			},
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(err, ErrAppNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			panic(err)
		},
		Director: func(r *http.Request) {},
	}

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// TODO: make urls work as expected
			r.URL, _ = url.Parse(fmt.Sprintf("http://%s", r.Host))
			proxy.ServeHTTP(w, r)
		}),
	}
	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()
	slog.Info("Running public http listener", "addr", listener.Addr())
	if err := server.Serve(listener); err != nil {
		return err
	}
	return nil
}

func (p *Server) appClient(name string) (*serverApp, *ConnectedClient) {
	p.appHostsMu.RLock()
	app := p.appHosts[name]
	p.appHostsMu.RUnlock()
	if app == nil {
		return nil, nil
	}
	p.clientsMu.RLock()
	c := p.clients[app.client]
	p.clientsMu.RUnlock()
	return app, c
}

func (p *Server) Apps() (apps []AppInfo) {
	p.appHostsMu.RLock()
	for name, app := range p.appHosts {
		apps = append(apps, AppInfo{
			Client:  app.client,
			Healthy: app.healthy,
			Name:    name,
		})
	}
	p.appHostsMu.RUnlock()
	return apps
}

func (p *Server) Clients() (clients []ClientInfo) {
	p.clientsMu.RLock()
	for name := range p.clients {
		clients = append(clients, ClientInfo{
			Name: name,
		})
	}
	p.clientsMu.RUnlock()
	return clients
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
					slog.Debug("health check failed", "err", err)
				} else if !resp.Healthy {
					slog.Debug("app unhealthy", "name", app.Name)
				} else {
					p.appHostsMu.Lock()
					a := p.appHosts[app.Name]
					a.healthy = true
					p.appHostsMu.Unlock()
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

func WebHandler(server *Server) http.Handler {
	mux := http.NewServeMux()

	// Add logging middleware
	loggingMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w}

			next.ServeHTTP(sw, r)

			slog.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.status,
				"duration", time.Since(start),
				"remote_addr", r.RemoteAddr,
				"user_agent", r.UserAgent(),
			)
		})
	}

	html := func(body string) string {
		return fmt.Sprintf(`<html>
		<style>
		body {
			background: black; color: white; font-family: monospace;
			max-width: 400px; margin: 30px auto 0px auto;
		}
		a { color: lightblue; }
		.error { color: red; padding: 10px; border: 1px dashed red; }
		</style>
		<body>%s</body>
		</html>`, body)
	}

	httpError := func(w http.ResponseWriter, msg string, code int) {
		w.WriteHeader(code)
		w.Header().Add("Content-Type", "text/html")
		fmt.Fprint(w, html(fmt.Sprintf(`
		<h3>Headd.</h3>
		<p><i>uh oh</i></p>
		<div class=error>%s</div>
		`, msg)))
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			httpError(w, "404 - Not found", http.StatusNotFound)
			return
		}
		w.Header().Add("Content-Type", "text/html")
		fmt.Fprint(w, html(fmt.Sprintf(`
		<h3>Headd.</h3>
		<p>Current apps: %v</p>
		<p><a href="/">refresh</a></p>
		<p><form method=post action="/add-app"><button type=submit>Add app</button></form></p>

		`, server.Apps())))
	})
	mux.HandleFunc("/add-app", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			httpError(w, "404 - Not found", http.StatusNotFound)
			return
		}

		_, err := server.RegisterApp(App{
			Name:    fmt.Sprintf("app-%d", len(server.Apps())),
			Command: "./sample-app/sample-app",
		})
		if err != nil {
			httpError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/", 302)
	})

	// Wrap the final mux with the logging middleware
	return loggingMiddleware(mux)
}

// statusWriter wraps http.ResponseWriter to capture the status code
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

type Client struct {
	rpcServer  *RPC2Server
	rpcStreams chan quic.Stream
}

func NewClient() (*Client, error) {
	rpcStreams := make(chan quic.Stream, 2)
	rpcServer, err := NewRPC2Server(&quicChanListener{streamChan: rpcStreams})
	if err != nil {
		return nil, err
	}
	return &Client{
		rpcServer:  rpcServer,
		rpcStreams: rpcStreams,
	}, nil
}

func (p *Client) Shutdown() error {
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

func (p *Client) handleStreamProxy(ctx context.Context, stream quic.Stream) error {
	addr, err := parseAddrFromStream(stream)
	if err != nil {
		_ = stream.Close()
		return err
	}
	if addr.Port() == 0 && addr.Addr() == netip.AddrFrom4([4]byte{0, 0, 0, 0}) {
		_ = binary.Write(stream, binary.BigEndian, uint16(0))
		slog.Info("using stream as rpc channel", "stream", stream.StreamID())

		(&http2.Server{}).ServeConn(&streamConn{stream: stream, ReadWriteCloser: stream}, &http2.ServeConnOpts{
			Handler: p.rpcServer.server.Handler,
		})
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
func (p *Client) Dial(ctx context.Context, addr string) (quic.Connection, error) {
	tlsCert, err := tls.LoadX509KeyPair("server.crt", "server.key")
	if err != nil {
		return nil, fmt.Errorf("loading tls certs: %w", err)
	}
	slog.Info("dialing", "addr", addr)
	conn, err := quic.DialAddr(ctx,
		addr,
		&tls.Config{
			InsecureSkipVerify: true,
			Certificates:       []tls.Certificate{tlsCert},
		},
		&quic.Config{
			MaxIdleTimeout:  20 * time.Second,
			KeepAlivePeriod: 10 * time.Second,
		},
	)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func (p *Client) Listen(ctx context.Context, conn quic.Connection) error {
	ctx, cancel := context.WithCancel(ctx)
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
			if errors.Is(err, context.Canceled) {
				_ = conn.CloseWithError(quic.ApplicationErrorCode(quic.ConnectionRefused), "connection closed")
			} else {
				_ = conn.CloseWithError(quic.ApplicationErrorCode(quic.InternalError), err.Error())
			}
			return fmt.Errorf("accepting stream failed: %w", err)
		}
		slog.Info("accepting stream", "id", stream.StreamID())
		go func() {
			if err := p.handleStreamProxy(ctx, stream); err != nil {
				slog.Error("handleStreamProxy", "err", err)
			}
			slog.Info("stream ended", "id", stream.StreamID())
		}()
	}

}
func copy(closer chan error, dst io.Writer, src io.Reader) {
	_, err := io.Copy(dst, src)
	closer <- err // connection is closed, send signal to stop proxy
}
