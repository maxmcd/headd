package tunneld

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/rpc"
	"os"
	"os/exec"

	"github.com/maxmcd/tunneld/pkg/synctyped"
)

type RPCClient struct {
	rpcClient *rpc.Client
}

func (c *RPCClient) Hello() error {
	return c.rpcClient.Call("RPCServer.Hello", 1, nil)
}

func (c *RPCClient) RegisterApp(app App) (appPort AppPort, err error) {
	var resp RegisterAppResp
	if err := c.rpcClient.Call("RPCServer.RegisterApp", RegisterAppReq{App: app}, &resp); err != nil {
		return AppPort{}, err
	}

	return AppPort{
		App:  app,
		Port: resp.Port,
	}, nil
}

func NewRPCClient(stream io.ReadWriteCloser) *RPCClient {
	return &RPCClient{
		rpcClient: rpc.NewClient(stream),
	}
}

func NewRPCServer() *RPCServer {
	return &RPCServer{
		apps: synctyped.Map[*RPCApp]{},
	}
}

type Connection struct {
	StreamID int64
	Addr     string
}

type RPCServer struct {
	apps synctyped.Map[*RPCApp]
}

type RPCApp struct {
	// TODO: locking? serial connection but there be multiple connections?
	app  App
	port int
	cmd  *exec.Cmd
}

func (s *RPCServer) Listen(stream io.ReadWriteCloser) error {
	if err := rpc.Register(s); err != nil {
		return fmt.Errorf("registering RPCServer: %w", err)
	}
	rpc.ServeConn(stream)
	return nil
}

func (s *RPCServer) Hello(req int, resp *int) error {
	*resp = 1
	return nil
}

type RegisterAppReq struct {
	App App
}

type RegisterAppResp struct {
	Port int
}

func (s *RPCServer) RegisterApp(req RegisterAppReq, resp *RegisterAppResp) error {
	slog.Debug("RegisterApp", "req", req)
	cmd := exec.Command(req.App.Command, req.App.Args...)
	if cmd.Err != nil {
		return cmd.Err
	}
	port, err := freePort()
	if err != nil {
		return fmt.Errorf("getting free port for app %q: %w", req.App.Name, err)
	}
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	cmd.Env = append(os.Environ(), fmt.Sprintf("PORT=%d", port))
	go func() {
		if err := cmd.Run(); err != nil {
			// TODO: safe?
			app, ok := s.apps.Load(req.App.Name)
			if ok {
				app.cmd.Err = err
				s.apps.Store(req.App.Name, app)
			}
		}
	}()
	s.apps.Store(req.App.Name, &RPCApp{
		cmd:  cmd,
		port: port,
		app:  req.App,
	})
	*resp = RegisterAppResp{Port: port}
	return nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
