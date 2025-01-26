package tunneld_test

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
	"testing"
	"time"

	"github.com/maxmcd/tunneld"
)

func TestProxy(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)
	ctx := context.Background()
	proxy := tunneld.NewProxyServer()
	server, err := proxy.ListenAddr(ctx, "127.0.0.1:0", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	clientAddr := server.ClientAddr().String()
	publicAddr := server.PublicAddr().String()

	go func() {
		if err := server.Wait(); err != nil {
			panic(err)
		}
	}()

	fmt.Println(clientAddr, publicAddr)
	_, _ = clientAddr, publicAddr
	time.Sleep(time.Millisecond * 100)
	fmt.Println("new proxy client")
	proxyClient, err := tunneld.NewProxyClient()
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

	appPort, err := proxy.RegisterApp(tunneld.App{
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
	if string(body) != "hello\n" {
		t.Fatal("body is not hello")
	}

}
