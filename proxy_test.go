package tunneld_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/maxmcd/tunneld"
)

func TestProxy(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("got test server request")
		fmt.Fprintln(w, "hello")
	}))

	defer ts.Close()
	ctx := context.Background()
	proxy := tunneld.NewProxyServer()
	server, err := proxy.ListenAddr(ctx, "127.0.0.1:0", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	port := uint16(0)
	if parts := strings.Split(ts.URL, ":"); len(parts) > 2 {
		if p, err := fmt.Sscanf(parts[2], "%d", &port); err != nil || p != 1 {
			t.Fatal("failed to parse port from test server URL")
		}
	} else {
		t.Fatal("test server URL missing port")
	}
	proxy.AppAddr = netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), port)
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
	proxyClient := tunneld.NewProxyClient()
	go func() {
		if err := proxyClient.Dial(context.Background(), clientAddr); err != nil {
			panic(err)
		}
	}()

	time.Sleep(time.Second)
	fmt.Println("GET")
	client := http.Client{
		Timeout: time.Millisecond * 100,
	}
	resp, err := client.Get("http://" + publicAddr)
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
