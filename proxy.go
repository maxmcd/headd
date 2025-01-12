package tund

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"github.com/quic-go/quic-go"
)

type ProxyServer struct {
	publicAddr string
	clientAddr string

	conn *quic.EarlyConnection
}

func NewProxyServer(publicAddr, clientAddr string) *ProxyServer {
	return &ProxyServer{
		publicAddr: publicAddr,
		clientAddr: clientAddr,
	}
}

func (p *ProxyServer) ClientListen() error {
	tlsCert, err := tls.LoadX509KeyPair("server.crt", "server.key")
	if err != nil {
		panic(err)
	}
	listener, err := quic.ListenAddrEarly(p.clientAddr, &tls.Config{
		InsecureSkipVerify: true,
		Certificates:       []tls.Certificate{tlsCert},
	}, &quic.Config{
		MaxIdleTimeout:  20 * time.Second,
		KeepAlivePeriod: 10 * time.Second,
		EnableDatagrams: true,
	})
	if err != nil {
		return fmt.Errorf("listening quic failed: %w", err)
	}
	fmt.Println("Running client listener on ", p.clientAddr)
	for {
		conn, err := listener.Accept(context.Background())
		if err != nil {
			return fmt.Errorf("accepting quic connection failed: %w", err)
		}
		fmt.Println("accepted quic connection")
		p.conn = &conn
	}
}

func (p *ProxyServer) PublicListen() error {
	fmt.Println("Running public listener on ", p.publicAddr)
	listener, err := net.Listen("tcp", p.publicAddr)
	if err != nil {
		return fmt.Errorf("listening tcp failed: %w", err)
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			return fmt.Errorf("accepting tcp connection failed: %w", err)
		}
		if p.conn == nil {
			fmt.Println("no connection to proxy")
			conn.Close()
			continue
		}
		go func() {
			if err := p.handleConn(*p.conn, conn); err != nil {
				fmt.Println("error handling connection:", err)
			}
		}()
	}
	return nil
}

func (p *ProxyServer) handleConn(conn quic.EarlyConnection, publicConn net.Conn) error {
	stream, err := conn.OpenStreamSync(context.Background())
	if err != nil {
		return fmt.Errorf("opening stream failed: %w", err)
	}
	defer stream.Close()
	closer := make(chan struct{}, 2)
	go copy(closer, publicConn, stream)
	go copy(closer, stream, publicConn)
	<-closer
	return nil
}

type ProxyClient struct {
	localAddr string
}

func NewProxyClient(localAddr string) *ProxyClient {
	return &ProxyClient{
		localAddr: localAddr,
	}
}

func (p *ProxyClient) Dial(ctx context.Context, addr string) error {
	tlsCert, err := tls.LoadX509KeyPair("server.crt", "server.key")
	if err != nil {
		panic(err)
	}

	fmt.Println("Dialing quic", addr)
	conn, err := quic.DialAddr(ctx,
		addr,
		&tls.Config{
			InsecureSkipVerify: true,
			Certificates:       []tls.Certificate{tlsCert},
		},
		&quic.Config{
			MaxIdleTimeout:  20 * time.Second,
			KeepAlivePeriod: 10 * time.Second,
			EnableDatagrams: true,
		},
	)
	if err != nil {
		return fmt.Errorf("quic dialAddr failed: %w", err)
	}
	for {
		stream, err := conn.AcceptStream(context.Background())
		if err != nil {
			return fmt.Errorf("accepting stream failed: %w", err)
		}
		go func() {
			conn, err := net.Dial("tcp", p.localAddr)
			if err != nil {
				return
			}
			defer conn.Close()
			closer := make(chan struct{}, 2)
			go copy(closer, conn, stream)
			go copy(closer, stream, conn)
			<-closer
		}()
	}

}
