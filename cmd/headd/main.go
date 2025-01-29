package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/maxmcd/headd"
)

func main() {
	help := func() {
		fmt.Println("Headd")
		fmt.Println("  $ headd server")
		fmt.Println("  $ headd client")
		os.Exit(1)
	}
	if len(os.Args) == 1 {
		help()
	}
	cmd := os.Args[1]
	if cmd == "client" {
		client, err := headd.NewClient()
		if err != nil {
			log.Panicln(err)
		}
		conn, err := client.Dial(context.Background(), "127.0.0.1:7400")
		if err != nil {
			fmt.Println("Failed to connect to server: %w", err)
			os.Exit(1)
		}
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			signals := make(chan os.Signal, 1)
			signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
			for signal := range signals {
				slog.Info("Got signal", "signal", signal)
				cancel()
				_ = client.Shutdown()
			}
		}()
		if err := client.Listen(ctx, conn); err != nil && !errors.Is(err, context.Canceled) {
			log.Panicln(err)
		}
		return
	}
	if cmd == "server" {
		server := headd.NewServer(&headd.ServerConfig{
			HealthCheckPeriod: time.Second,
		})
		go func() {
			panic(http.ListenAndServe(":7402", headd.WebHandler(server)))
		}()
		panic(server.ListenAndServe(context.Background(), "127.0.0.1:7400", "127.0.0.1:7401"))
	}
	help()
}
