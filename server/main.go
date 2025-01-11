package main

import (
	"log"
	"os"

	"github.com/maxmcd/tund"
)

func main() {
	server := tund.NewServer(":4442")
	if err := server.ListenAndServeTLS(os.Args[1], os.Args[2]); err != nil {
		log.Fatal(err)
	}
}
