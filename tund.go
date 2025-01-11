package tund

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/kr/pty"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
	"golang.org/x/term"
)

type Server struct {
	server *webtransport.Server
}

type Command struct {
	Command string
	Args    []string
}

func Fprintfln(w io.Writer, format string, a ...any) {
	fmt.Fprintf(w, format+"\n", a...)
}

func NewServer(addr string) *Server {
	s := &webtransport.Server{
		H3: http3.Server{
			Addr: addr,
			TLSConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}
	mux := http.NewServeMux()
	s.H3.Handler = mux
	mux.HandleFunc("/exec", func(w http.ResponseWriter, r *http.Request) {
		resp := io.MultiWriter(w, os.Stdout)
		sess, err := s.Upgrade(w, r)
		if err != nil {
			Fprintfln(resp, "upgrading failed: %s", err)
			return
		}
		stream, err := sess.AcceptStream(r.Context())
		if err != nil {
			w.WriteHeader(500)
			Fprintfln(resp, "accepting stream failed: %s", err)
			return
		}
		var c Command
		if err := json.NewDecoder(stream).Decode(&c); err != nil {
			w.WriteHeader(400)
			Fprintfln(resp, "Decoding command failed: %s", err)
			return
		}
		fmt.Printf("Got new command: %#v\n", c)
		cmd := exec.Command(c.Command, c.Args...)
		cmd.Stdout = stream
		cmd.Stderr = stream
		if err := cmd.Run(); err != nil {
			Fprintfln(resp, "Command failed: %s", err)
		}
		_ = stream.Close()
	})

	mux.HandleFunc("/pty", func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("Got new pty request")
		resp := io.MultiWriter(w, os.Stdout)
		sess, err := s.Upgrade(w, r)
		if err != nil {
			Fprintfln(resp, "upgrading failed: %s", err)
			return
		}
		stream, err := sess.AcceptStream(r.Context())
		if err != nil {
			w.WriteHeader(500)
			Fprintfln(resp, "accepting stream failed: %s", err)
			return
		}
		fmt.Println("Got new stream")
		cmd := exec.Command("bash")
		cmd.Env = append(os.Environ(), "TERM=xterm")
		ptmx, err := pty.Start(cmd)
		if err != nil {
			Fprintfln(resp, "starting pty failed: %s", err)
			return
		}
		defer ptmx.Close()
		rows, err := strconv.Atoi(r.Header.Get("Rows"))
		if err != nil {
			Fprintfln(resp, "getting rows failed: %s", err)
			return
		}
		cols, err := strconv.Atoi(r.Header.Get("Cols"))
		if err != nil {
			Fprintfln(resp, "getting cols failed: %s", err)
			return
		}
		if err := pty.Setsize(ptmx, &pty.Winsize{
			Rows: uint16(rows),
			Cols: uint16(cols),
		}); err != nil {
			Fprintfln(resp, "setting pty size failed: %s", err)
			return
		}
		closer := make(chan struct{}, 2)
		go copy(closer, ptmx, stream)
		go copy(closer, stream, ptmx)
		<-closer
		_ = stream.Close()
	})

	return &Server{server: s}
}

func (s *Server) ListenAndServeTLS(certFile, keyFile string) error {
	return s.server.ListenAndServeTLS(certFile, keyFile)
}

func (s *Server) CloseGracefully(t time.Duration) error {
	return s.server.H3.CloseGracefully(t)
}

func NewClient(addr string) *Client {
	d := &webtransport.Dialer{}
	d.TLSClientConfig = &tls.Config{
		InsecureSkipVerify: true,
	}

	return &Client{addr: addr, d: d}
}

type Client struct {
	addr string
	d    *webtransport.Dialer
}

func (c *Client) Exec(cmd Command, out io.Writer) error {
	// optionally, add custom headers
	resp, sess, err := c.d.Dial(context.Background(), fmt.Sprintf("https://%s/exec", c.addr), nil)
	if err != nil {
		return fmt.Errorf("Response error: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Response error: %d %s", resp.StatusCode, string(b))
	}
	stream, err := sess.OpenStreamSync(context.Background())
	if err != nil {
		log.Panicln(err)
	}
	defer stream.Close()
	if err := json.NewEncoder(stream).Encode(cmd); err != nil {
		return fmt.Errorf("encoding command failed: %w", err)
	}
	if _, err := io.Copy(out, stream); err != nil {
		return fmt.Errorf("copying stream failed: %w", err)
	}
	return nil
}

func (c *Client) Pty() error {
	oldTerminalState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("making terminal raw failed: %w", err)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldTerminalState)

	cols, rows, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("getting terminal size failed: %w", err)
	}
	// optionally, add custom headers
	resp, sess, err := c.d.Dial(context.Background(), fmt.Sprintf("https://%s/pty", c.addr), map[string][]string{
		"Rows": {strconv.Itoa(rows)},
		"Cols": {strconv.Itoa(cols)},
	})
	if err != nil {
		return fmt.Errorf("Response error: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Response error: %d %s", resp.StatusCode, string(b))
	}
	stream, err := sess.OpenStreamSync(context.Background())
	if err != nil {
		log.Panicln(err)
	}
	defer stream.Close()
	closer := make(chan struct{}, 2)
	stream.Write([]byte(nil))
	go copy(closer, os.Stdout, stream)
	go copy(closer, stream, os.Stdin)
	<-closer
	return nil
}

func copy(closer chan struct{}, dst io.Writer, src io.Reader) {
	_, _ = io.Copy(dst, src)
	closer <- struct{}{} // connection is closed, send signal to stop proxy
}
