package main

import (
	"log"

	"github.com/maxmcd/shard0/pkg/tunnel"
)

const (
	LISTEN_PORT  = "7835" // Port the server listens on
	FORWARD_PORT = "8080" // Port to forward connections to
)

func main() {
	server := tunnel.NewServer(LISTEN_PORT, FORWARD_PORT)
	if err := server.Listen(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
