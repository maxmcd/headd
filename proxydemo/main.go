package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/maxmcd/tund"
)

func main() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("got test server request")
		fmt.Fprintln(w, "hello")
	}))
	defer ts.Close()

	proxy := tund.NewProxyServer("localhost:4442", "localhost:4443")
	go func() {
		if err := proxy.ClientListen(); err != nil {
			log.Panicln(err)
		}
	}()
	go func() {
		if err := proxy.PublicListen(); err != nil {
			log.Panicln(err)
		}
	}()
	time.Sleep(time.Millisecond * 100)
	fmt.Println("new proxy client")
	proxyClient := tund.NewProxyClient(strings.TrimPrefix(ts.URL, "http://"))
	if err := proxyClient.Dial(context.Background(), "localhost:4443"); err != nil {
		log.Panicln(err)
	}

}
