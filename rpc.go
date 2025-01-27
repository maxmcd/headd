package headd

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/quic-go/quic-go"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

type RPCApp struct {
	// TODO: locking? serial connection but there be multiple connections?
	app  App
	port int
	cmd  *exec.Cmd
	logs *bytes.Buffer
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		return 0, fmt.Errorf("getting free port: %w", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

type streamConn struct {
	stream quic.Stream
	io.ReadWriteCloser
}

type quicChanListener struct {
	streamChan chan quic.Stream
}

func (l *quicChanListener) Accept() (net.Conn, error) {
	stream, ok := <-l.streamChan
	fmt.Println("quicChanListener Accept", stream, ok)
	if !ok {
		return nil, io.EOF
	}
	return &streamConn{stream: stream, ReadWriteCloser: stream}, nil
}

func (l *quicChanListener) Close() error {
	fmt.Println("closing quic listener")
	close(l.streamChan)
	return nil
}

func (l *quicChanListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(0, 0, 0, 0), Port: 0}
}

func (c *streamConn) LocalAddr() net.Addr                { return &net.TCPAddr{IP: net.IPv4(0, 0, 0, 0), Port: 0} }
func (c *streamConn) RemoteAddr() net.Addr               { return &net.TCPAddr{IP: net.IPv4(0, 0, 0, 0), Port: 0} }
func (c *streamConn) SetDeadline(t time.Time) error      { return nil }
func (c *streamConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *streamConn) SetWriteDeadline(t time.Time) error { return nil }

func quicConnDial(conn quic.Connection) func(ctx context.Context, network string, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		stream, err := conn.OpenStreamSync(ctx)
		if err != nil {
			return nil, fmt.Errorf("quicConnDial openStreamSync: %w", err)
		}
		if err := writeAddrToStream(stream, netip.AddrPortFrom(netip.AddrFrom4([4]byte{0, 0, 0, 0}), 0)); err != nil {
			return nil, fmt.Errorf("quicConnDial: writing addr to stream: %w", err)
		}

		return &streamConn{stream: stream, ReadWriteCloser: stream}, nil
	}
}

type RPC2Server struct {
	listener net.Listener
	server   *http.Server

	appsLock sync.RWMutex
	apps     map[string]*RPCApp
}

func handleRPCRequest[Req any, Resp any](mux *http.ServeMux, name string, handler func(Req) (*Resp, error)) {
	mux.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) {
		var req Req
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("decoding %s body: %s", name, err), http.StatusBadRequest)
			return
		}
		resp, err := handler(req)
		if err != nil {
			http.Error(w, fmt.Sprintf("%s: %s", name, err), http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
}

func NewRPC2Server(listener net.Listener) (*RPC2Server, error) {
	s := &RPC2Server{
		apps: map[string]*RPCApp{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/Hello", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(1)
	})
	handleRPCRequest(mux, "RegisterApp", s.RegisterApp)
	handleRPCRequest(mux, "HealthCheckApp", s.HealthCheckApp)
	handleRPCRequest(mux, "StopApp", s.StopApp)

	// Configure HTTP/2
	h2conf := &http2.Server{
		IdleTimeout: 1 * time.Hour,
	}
	s.listener = listener
	handler := h2c.NewHandler(mux, h2conf)
	s.server = &http.Server{
		Handler: handler,
	}
	return s, nil
}

func (s *RPC2Server) StopApp(req StopAppReq) (resp *StopAppResp, err error) {
	app := s.apps[req.Name]
	if app == nil {
		return nil, fmt.Errorf("app with name %q not found", req.Name)
	}
	if app.cmd.ProcessState != nil && app.cmd.ProcessState.Exited() {
		return nil, fmt.Errorf("process exited: %d", app.cmd.ProcessState.ExitCode())
	}
	pgid := app.cmd.Process.Pid
	if err := syscall.Kill(-pgid, syscall.SIGINT); err != nil {
		return nil, fmt.Errorf("error killing process group: %w", err)
	}
	return &StopAppResp{}, nil
}

func (s *RPC2Server) HealthCheckApp(req HealthCheckAppReq) (resp *HealthCheckAppResp, err error) {
	app := s.apps[req.Name]
	if app == nil {
		return nil, fmt.Errorf("app with name %q not found", req.Name)
	}
	if app.cmd.ProcessState != nil && app.cmd.ProcessState.Exited() {
		return &HealthCheckAppResp{
			Healthy:  false,
			Exited:   true,
			ExitCode: app.cmd.ProcessState.ExitCode(),
		}, nil
	}
	u := fmt.Sprintf("http://localhost:%d/healthcheck", app.port)
	r, err := http.Get(u)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", r.StatusCode)
	}
	return &HealthCheckAppResp{Healthy: true}, nil
}

func (s *RPC2Server) RegisterApp(req RegisterAppReq) (resp *RegisterAppResp, err error) {
	slog.Debug("RegisterApp", "req", req)
	cmd := exec.Command(req.App.Command, req.App.Args...)
	if cmd.Err != nil {
		return nil, cmd.Err
	}
	port, err := freePort()
	if err != nil {
		return nil, fmt.Errorf("getting free port for app %q: %w", req.App.Name, err)
	}
	var logs bytes.Buffer
	cmd.Stderr = &logs
	cmd.Stdout = &logs
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(os.Environ(), fmt.Sprintf("PORT=%d", port))
	_ = cmd.Start()
	go func() {
		_ = cmd.Wait()
	}()
	s.appsLock.Lock()
	s.apps[req.App.Name] = &RPCApp{
		cmd:  cmd,
		port: port,
		app:  req.App,
		logs: &logs,
	}
	s.appsLock.Unlock()
	return &RegisterAppResp{Port: port}, nil
}

func (s *RPC2Server) ListenAndServe() error {
	// Start serving
	slog.Info("starting server", "addr", s.listener.Addr().String())
	if err := s.server.Serve(s.listener); err != nil {
		return err
	}
	return nil
}

type RPC2Client struct {
	dialer func(context.Context, string, string) (net.Conn, error)
	client *http.Client
}

func NewRPC2Client(dialer func(context.Context, string, string) (net.Conn, error)) *RPC2Client {
	client := &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true, // Enable h2c support
			DialTLSContext: func(ctx context.Context,
				network, addr string, cfg *tls.Config) (net.Conn, error) {
				fmt.Println("dialing", network, addr)
				return dialer(ctx, network, addr)
			},
		},
	}

	return &RPC2Client{
		client: client,
		dialer: dialer,
	}
}

func clientRequest[RQ any, RS any](c *http.Client, name string, req RQ) (res *RS, err error) {
	var rs RS
	bodyB, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshalling %s req: %w", name, err)
	}
	resp, err := c.Post("http://headd/"+name, "application/json", bytes.NewBuffer(bodyB))
	if err != nil {
		return nil, fmt.Errorf("register app POST: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, errors.New(string(b))
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	if err := json.NewDecoder(io.TeeReader(resp.Body, &buf)).Decode(&rs); err != nil {
		return nil, fmt.Errorf("decoding resp: %q %w", buf.String(), err)
	}
	return &rs, nil
}

func (c *RPC2Client) Hello() error {
	resp, err := c.client.Get("http://headd/Hello")
	if err != nil {
		return fmt.Errorf("getting hello: %w", err)
	}
	defer resp.Body.Close()
	var one int
	if err := json.NewDecoder(resp.Body).Decode(&one); err != nil {
		return fmt.Errorf("decoding hello body")
	}
	if one != 1 {
		return fmt.Errorf("invalid response from hello")
	}
	return nil
}

type RegisterAppReq struct{ App App }
type RegisterAppResp struct{ Port int }

func (c *RPC2Client) RegisterApp(app App) (appPort *AppPort, err error) {
	res, err := clientRequest[RegisterAppReq, RegisterAppResp](
		c.client, "RegisterApp", RegisterAppReq{App: app})
	if err != nil {
		return nil, fmt.Errorf("RegisterApp req: %w", err)
	}
	return &AppPort{
		App:  app,
		Port: res.Port,
	}, nil
}

type StopAppReq struct{ Name string }
type StopAppResp struct{}

func (c *RPC2Client) StopApp(name string) error {
	_, err := clientRequest[StopAppReq, StopAppResp](
		c.client, "StopApp", StopAppReq{Name: name})
	if err != nil {
		return fmt.Errorf("StopApp req: %w", err)
	}
	return nil
}

type HealthCheckAppReq struct{ Name string }
type HealthCheckAppResp struct {
	Healthy  bool
	Exited   bool
	ExitCode int
}

func (c *RPC2Client) HealthCheckApp(name string) (resp *HealthCheckAppResp, err error) {
	return clientRequest[HealthCheckAppReq, HealthCheckAppResp](
		c.client, "HealthCheckApp", HealthCheckAppReq{Name: name})
}
