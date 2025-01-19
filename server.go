package tunneld

type Server struct {
	Proxy *ProxyServer
}

func NewServer() *Server {
	return &Server{}
}
