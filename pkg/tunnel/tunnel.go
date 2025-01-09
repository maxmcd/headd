package tunnel

import (
	"io"
	"log"
	"net"
)

// Server represents a TCP tunnel server
type Server struct {
	listenPort  string
	forwardPort string
}

// NewServer creates a new TCP tunnel server
func NewServer(listenPort, forwardPort string) *Server {
	return &Server{
		listenPort:  listenPort,
		forwardPort: forwardPort,
	}
}

// Listen starts the server and handles incoming connections
func (s *Server) Listen() error {
	listener, err := net.Listen("tcp", ":"+s.listenPort)
	if err != nil {
		return err
	}
	defer listener.Close()

	log.Printf("Server started on port %s", s.listenPort)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %v", err)
			continue
		}

		go s.handleConnection(conn)
	}
}

func (s *Server) handleConnection(clientConn net.Conn) {
	defer clientConn.Close()

	listener, err := net.Listen("tcp", ":"+s.forwardPort)
	if err != nil {
		log.Printf("Failed to start listener: %v", err)
		return
	}
	defer listener.Close()

	log.Printf("Listening for incoming connections on port %s", s.forwardPort)

	for {
		incomingConn, err := listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %v", err)
			continue
		}

		go proxyConnection(incomingConn, clientConn)
	}
}

// Client represents a TCP tunnel client
type Client struct {
	serverAddr string
	localAddr  string
}

// NewClient creates a new TCP tunnel client
func NewClient(serverAddr, localAddr string) *Client {
	return &Client{
		serverAddr: serverAddr,
		localAddr:  localAddr,
	}
}

// Connect establishes the tunnel connection
func (c *Client) Connect() error {
	serverConn, err := net.Dial("tcp", c.serverAddr)
	if err != nil {
		return err
	}
	defer serverConn.Close()

	log.Printf("Connected to server at %s", c.serverAddr)

	localConn, err := net.Dial("tcp", c.localAddr)
	if err != nil {
		return err
	}
	defer localConn.Close()

	go io.Copy(serverConn, localConn)
	io.Copy(localConn, serverConn)

	return nil
}

// Helper function used by both client and server
func proxyConnection(conn1, conn2 net.Conn) {
	defer conn1.Close()

	go io.Copy(conn1, conn2)
	io.Copy(conn2, conn1)
}
