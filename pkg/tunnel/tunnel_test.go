package tunnel

import (
	"io"
	"net"
	"testing"
	"time"
)

func TestTunnel(t *testing.T) {
	// Start a mock target service
	targetListener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("Failed to start target listener: %v", err)
	}
	targetAddr := targetListener.Addr().String()
	go func() {
		for {
			conn, err := targetListener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				io.Copy(c, c) // Echo server
			}(conn)
		}
	}()

	// Start the tunnel server
	server := NewServer("0", "0") // Use port 0 for testing to get random ports
	go server.Listen()

	// Start the tunnel client
	client := NewClient("localhost:7835", targetAddr)
	go client.Connect()

	// Give some time for connections to establish
	time.Sleep(100 * time.Millisecond)

	// Test the tunnel
	conn, err := net.Dial("tcp", "localhost:8080")
	if err != nil {
		t.Fatalf("Failed to connect to tunnel: %v", err)
	}

	// Send test data
	testData := []byte("hello world")
	_, err = conn.Write(testData)
	if err != nil {
		t.Fatalf("Failed to write test data: %v", err)
	}

	// Read response
	response := make([]byte, len(testData))
	_, err = io.ReadFull(conn, response)
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	// Verify response
	if string(response) != string(testData) {
		t.Errorf("Expected response %q, got %q", testData, response)
	}
}
