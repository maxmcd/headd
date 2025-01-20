package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	addr := fmt.Sprintf(":%s", os.Getenv("PORT"))
	fmt.Println("Running on addr: ", addr)
	start := time.Now()
	log.Panicln(http.ListenAndServe(addr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hostname := os.Hostname()
		_, _ = fmt.Fprintf(w, "%s - %s", hostname, time.Since(start))
	})))
}
