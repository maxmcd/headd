package tunneld

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"
)

func TestE2R(t *testing.T) {
	server := NewServer(":4442")

	go func() {
		if err := server.ListenAndServeTLS("server.crt", "server.key"); err != nil {
			log.Panicln(err)
		}
		t.Cleanup(func() {
			server.CloseGracefully(time.Millisecond * 100)
		})
	}()

	var buf bytes.Buffer
	client := NewClient("localhost:4442")
	if err := client.Exec(Command{
		Command: "ls",
	}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "go.mod") {
		t.Fatal("ls did not return go.mod")
	}
}
