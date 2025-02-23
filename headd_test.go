package headd_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/maxmcd/headd"
)

type testServer struct {
	*headd.Server
	cConn     *net.UDPConn
	pListener net.Listener
}

func init() {
	var logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)
}

func newTestServer(t *testing.T) *testServer {
	cAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cConn, err := net.ListenUDP("udp", cAddr)
	if err != nil {
		t.Fatal(err)
	}
	pListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := headd.NewServer(&headd.ServerConfig{
		HealthCheckPeriod: time.Millisecond * 50,
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		if err := server.Serve(ctx, cConn, pListener); err != nil {
			if !errors.Is(err, context.Canceled) {
				log.Panicln(err)
			}
		}
	}()
	return &testServer{
		Server:    server,
		cConn:     cConn,
		pListener: pListener,
	}
}

func TestProxy(t *testing.T) {
	ctx := context.Background()
	server := newTestServer(t)
	clientAddr := server.cConn.LocalAddr().String()
	publicAddr := server.pListener.Addr().String()
	proxyClient, err := headd.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	for x := 0; x < 2; x++ {
		clientConn, err := proxyClient.Dial(context.Background(), clientAddr)
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			if err := proxyClient.Listen(ctx, clientConn); err != nil {
				if !errors.Is(err, context.Canceled) {
					panic(err)
				}
			}
		}()
		defer func() { _ = proxyClient.Shutdown() }()

		for i := 0; i < 10; i++ {
			time.Sleep(time.Millisecond * 5)
			clients := server.Clients()
			if len(clients) > x {
				break
			}
			if i == 10-1 {
				t.Fatal("No connected client")
			}
		}

		appPort, err := server.RegisterApp(headd.App{
			Command: "go",
			Args:    []string{"run", "./sample-app/main.go"},
			Name:    "sample-app",
		})
		if err != nil {
			t.Fatal(err)
		}

		for i := 0; i < 10; i++ {
			time.Sleep(time.Millisecond * 50)
			apps := server.Apps()
			if len(apps) > x && apps[0].Healthy {
				break
			}
			if i == 10-1 {
				t.Fatal("App never got healthy")
			}
		}

		req, err := http.NewRequest("GET", fmt.Sprintf("http://%s/", publicAddr), nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = appPort.App.Name
		req.Header.Set("host", appPort.App.Name)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "uptime") {
			t.Fatal("body does not contain uptime")
		}

		_ = proxyClient.Shutdown()
	}

}
