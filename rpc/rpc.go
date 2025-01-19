package rpc

import (
	"io"
	"net/rpc"
)

type Client struct {
	rpcClient *rpc.Client
}

func (c *Client) NewConnection(conn Connection) error {
	return c.rpcClient.Call("InlineServer.NewConnection", conn, nil)
}

func (c *Client) ConnectionError(streamID int64) error {
	var err error
	er := c.rpcClient.Call("InlineServer.ConnectionError", streamID, &err)
	if err != nil {
		return err
	}
	return er
}

func (c *Client) Hello() error {
	return c.rpcClient.Call("InlineServer.Hello", 1, nil)
}

func NewClient(stream io.ReadWriteCloser) *Client {
	return &Client{
		rpcClient: rpc.NewClient(stream),
	}
}

func StartServer(stream io.ReadWriteCloser, server *InlineServer) error {
	if err := rpc.Register(server); err != nil {
		return err
	}
	rpc.ServeConn(stream)
	return nil
}

type Connection struct {
	StreamID int64
	Addr     string
}

func NewInlineServer(
	hello func(req int, resp *int) error,
) *InlineServer {
	return &InlineServer{
		hello: hello,
	}
}

type InlineServer struct {
	hello func(req int, resp *int) error
}

func (s *InlineServer) NewConnection(conn Connection, resp *int) error {
	return nil
}

func (s *InlineServer) ConnectionError(streamID int64, err *error) error {
	return nil
}

func (s *InlineServer) Hello(req int, resp *int) error {
	return s.hello(req, resp)
}
