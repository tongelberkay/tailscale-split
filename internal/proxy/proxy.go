// Package proxy is a loopback HTTP proxy (CONNECT and plain HTTP) whose
// upstream connections go through the tailnet. Only connections from
// selected processes are served, so other local programs cannot use it to
// reach the exit node.
package proxy

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

type Server struct {
	// Dial opens upstream connections (through the tailnet).
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
	// Allow decides whether the client at remote (connected to our listener
	// at local) may use the proxy.
	Allow func(remote, local netip.AddrPort) bool
	Logf  func(string, ...any)

	once sync.Once
	tr   *http.Transport
}

func (s *Server) init() {
	s.once.Do(func() {
		if s.Logf == nil {
			s.Logf = func(string, ...any) {}
		}
		s.tr = &http.Transport{
			Proxy:               nil,
			DialContext:         s.Dial,
			ForceAttemptHTTP2:   false,
			MaxIdleConnsPerHost: 4,
			IdleConnTimeout:     90 * time.Second,
		}
	})
}

// Serve accepts connections until ln is closed.
func (s *Server) Serve(ln net.Listener) error {
	s.init()
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go s.handle(c)
	}
}

func addrPort(a net.Addr) netip.AddrPort {
	if t, ok := a.(*net.TCPAddr); ok {
		return t.AddrPort()
	}
	ap, _ := netip.ParseAddrPort(a.String())
	return ap
}

func (s *Server) handle(c net.Conn) {
	defer c.Close()
	remote, local := addrPort(c.RemoteAddr()), addrPort(c.LocalAddr())
	if s.Allow != nil && !s.Allow(remote, local) {
		io.WriteString(c, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		s.Logf("proxy: refused %v (not a selected app)", remote)
		return
	}
	br := bufio.NewReader(c)
	for {
		c.SetReadDeadline(time.Now().Add(2 * time.Minute))
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		c.SetReadDeadline(time.Time{})
		if req.Method == http.MethodConnect {
			s.connect(c, br, req)
			return
		}
		if !s.forward(c, req) {
			return
		}
	}
}

func (s *Server) connect(c net.Conn, br *bufio.Reader, req *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	up, err := s.Dial(ctx, "tcp", req.Host)
	cancel()
	if err != nil {
		s.Logf("proxy: CONNECT %s: %v", req.Host, err)
		io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		return
	}
	defer up.Close()
	if _, err := io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	// The client may already have sent its TLS ClientHello.
	if n := br.Buffered(); n > 0 {
		b, _ := br.Peek(n)
		if _, err := up.Write(b); err != nil {
			return
		}
		br.Discard(n)
	}
	done := make(chan struct{}, 2)
	go func() { io.Copy(up, c); closeWrite(up); done <- struct{}{} }()
	go func() { io.Copy(c, up); closeWrite(c); done <- struct{}{} }()
	<-done
	t := time.AfterFunc(30*time.Second, func() { c.Close(); up.Close() })
	<-done
	t.Stop()
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		cw.CloseWrite()
	} else {
		c.Close()
	}
}

var hopHeaders = []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
	"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade"}

// forward handles an absolute-form plain HTTP request. It reports whether the
// client connection can be reused.
func (s *Server) forward(c net.Conn, req *http.Request) bool {
	if req.URL.Scheme != "http" || req.URL.Host == "" {
		io.WriteString(c, "HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		return false
	}
	keep := !req.Close && !strings.EqualFold(req.Header.Get("Proxy-Connection"), "close")
	for _, h := range hopHeaders {
		req.Header.Del(h)
	}
	req.RequestURI = ""
	resp, err := s.tr.RoundTrip(req)
	if err != nil {
		s.Logf("proxy: %s %s: %v", req.Method, req.URL.Host, err)
		io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		return false
	}
	defer resp.Body.Close()
	for _, h := range hopHeaders {
		resp.Header.Del(h)
	}
	resp.Close = !keep
	if err := resp.Write(c); err != nil {
		return false
	}
	return keep
}
