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
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	server := newTestServer(t)
	clientAddr := server.cConn.LocalAddr().String()
	publicAddr := server.pListener.Addr().String()
	time.Sleep(time.Millisecond * 100)
	fmt.Println("new proxy client")
	proxyClient, err := headd.NewProxyClient()
	if err != nil {
		t.Fatal(err)
	}
	clientConn, err := proxyClient.Dial(context.Background(), clientAddr)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		if err := proxyClient.Listen(clientConn); err != nil {
			panic(err)
		}
	}()
	defer func() { _ = proxyClient.Shutdown() }()

	for i := 0; i < 10; i++ {
		apps := server.Clients()
		if len(apps) > 0 {
			break
		}
		time.Sleep(time.Millisecond * 50)
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
		apps := server.Apps()
		if len(apps) > 0 && apps[0].Healthy {
			break
		}
		time.Sleep(time.Millisecond * 50)
	}

	fmt.Println("preparing request")

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
	fmt.Println("resp", resp)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("body: %q\n", string(body))
	if !strings.Contains(string(body), "uptime") {
		t.Fatal("body does not contain uptime")
	}

}
