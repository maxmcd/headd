package tund

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProxy(t *testing.T) {

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("got test server request")
		fmt.Fprintln(w, "hello")
	}))
	defer ts.Close()

	proxy := NewProxyServer("localhost:4442", "localhost:4443")
	go func() {
		if err := proxy.ClientListen(); err != nil {
			t.Fatal(err)
		}
	}()
	go func() {
		if err := proxy.PublicListen(); err != nil {
			t.Fatal(err)
		}
	}()
	time.Sleep(time.Millisecond * 100)
	fmt.Println("new proxy client")
	proxyClient := NewProxyClient(strings.TrimPrefix(ts.URL, "http://"))
	go func() {
		if err := proxyClient.Dial(context.Background(), "localhost:4443"); err != nil {
			t.Fatal(err)
		}
	}()

	time.Sleep(time.Second)
	fmt.Println("GET")
	client := http.Client{
		Timeout: time.Millisecond * 100,
	}
	resp, err := client.Get("http://localhost:4442")
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
	if string(body) != "hello\n " {
		t.Fatal("body is not hello")
	}

}
