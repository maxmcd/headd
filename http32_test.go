package tunneld

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"testing"

	"github.com/quic-go/quic-go"
)

type quicListener struct {
	conn quic.Connection
}

func (l *quicListener) Accept() (net.Conn, error) {
	stream, err := l.conn.AcceptStream(context.Background())
	if err != nil {
		return nil, fmt.Errorf("quicListener accept: %w", err)
	}

	if _, err := parseAddrFromStream(stream); err != nil {
		return nil, fmt.Errorf("quicListener parseAddrFromStream: %w", err)
	}
	_ = binary.Write(stream, binary.BigEndian, uint16(0))

	return &streamConn{stream: stream, ReadWriteCloser: stream}, nil
}

func (l *quicListener) Close() error {
	fmt.Println("closing quic listener")
	return l.conn.CloseWithError(quic.ApplicationErrorCode(quic.NoError), "")
}

func (l *quicListener) Addr() net.Addr {
	return l.conn.LocalAddr()
}
func TestHTTP32(t *testing.T) {
	tlsCert, err := tls.LoadX509KeyPair("server.crt", "server.key")
	if err != nil {
		log.Panicln(fmt.Errorf("loading certificates: %w", err))
	}
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
		Certificates:       []tls.Certificate{tlsCert},
	}
	// Start the server.
	listener, err := quic.ListenAddr(":8321", tlsConfig, &quic.Config{})
	if err != nil {
		log.Panicln(err)
	}
	defer listener.Close()

	// Connect a client.
	conn, err := quic.DialAddr(context.Background(), "localhost:8321", tlsConfig, &quic.Config{})
	if err != nil {
		log.Panicln(err)
	}
	defer func() { _ = conn.CloseWithError(quic.ApplicationErrorCode(quic.NoError), "") }()

	// Accept a client connection.
	serverConn, err := listener.Accept(context.Background())
	if err != nil {
		log.Panicln(err)
	}
	defer func() { _ = serverConn.CloseWithError(quic.ApplicationErrorCode(quic.NoError), "") }()

	// // Open a stream to the server.
	// stream, err := conn.OpenStream()
	// if err != nil {
	// 	log.Panicln(err)
	// }

	// _, _ = stream.Write([]byte("hello"))
	// serverStream, err := serverConn.AcceptStream(context.Background())
	// if err != nil {
	// 	log.Panicln(err)
	// }
	// defer func() { _ = serverStream.Close() }()

	// buf := make([]byte, 5)
	// if _, err := io.ReadFull(serverStream, buf); err != nil {
	// 	log.Panicln(err)
	// }

	// fmt.Println(string(buf)) // => "hello"

	rpcServer, err := NewRPC2Server(&quicListener{conn: serverConn})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = rpcServer.ListenAndServe()
	}()

	client := NewRPC2Client(quicConnDial(conn))

	if err := client.Hello(); err != nil {
		t.Fatal(err)
	}

	fmt.Println(client.HealthCheckApp("foo"))
}
