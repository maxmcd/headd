package main

import (
	"log"

	"github.com/maxmcd/shard0/pkg/tunnel"
)

const (
	SERVER_ADDR = "localhost:7835" // Address of the bore server
	LOCAL_ADDR  = "localhost:3000" // Local service to expose
)

func main() {
	client := tunnel.NewClient(SERVER_ADDR, LOCAL_ADDR)
	if err := client.Connect(); err != nil {
		log.Fatalf("Client error: %v", err)
	}
}
