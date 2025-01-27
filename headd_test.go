package headd_test

import (
	"context"
	"crypto/tls"
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
			if err != context.Canceled {
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
		Level: slog.LevelDebug,
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
	go func() {
		if err := proxyClient.Dial(context.Background(), clientAddr); err != nil {
			panic(err)
		}
	}()
	defer func() { _ = proxyClient.Shutown() }()
	time.Sleep(time.Second)

	appPort, err := server.RegisterApp(headd.App{
		Command: "go",
		Args:    []string{"run", "./sample-app/main.go"},
		Name:    "sample-app",
	})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(time.Second)
	time.Sleep(time.Second)
	fmt.Println(appPort)

	tlsCert, err := tls.LoadX509KeyPair("server.crt", "server.key")
	if err != nil {
		log.Panicln(fmt.Errorf("loading certificates: %w", err))
	}

	client := http.Client{
		Timeout: time.Millisecond * 100,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				Certificates:       []tls.Certificate{tlsCert},
				ServerName:         appPort.App.Name,
			},
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return net.Dial(network, publicAddr)
			},
		},
	}

	req, err := http.NewRequest("GET", "https://"+appPort.App.Name, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("host", appPort.App.Name)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	fmt.Println("resp", resp)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("body: %q", string(body))
	if !strings.Contains(string(body), "uptime") {
		t.Fatal("body does not contain uptime")
	}

}
