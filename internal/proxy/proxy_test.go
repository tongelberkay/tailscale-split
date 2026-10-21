package proxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
)

type dialLog struct {
	mu    sync.Mutex
	addrs []string
}

func start(t *testing.T, target string, allow bool) (string, *dialLog) {
	log := &dialLog{}
	s := &Server{
		// Every upstream dial lands on target, standing in for the tailnet.
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			log.mu.Lock()
			log.addrs = append(log.addrs, address)
			log.mu.Unlock()
			var d net.Dialer
			return d.DialContext(ctx, network, target)
		},
		Allow: func(remote, local netip.AddrPort) bool { return allow },
		Logf:  t.Logf,
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve(ln)
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String(), log
}

func TestPlainHTTP(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "hello %s", r.URL.Path)
	}))
	defer origin.Close()
	addr, log := start(t, strings.TrimPrefix(origin.URL, "http://"), true)
	pu, _ := url.Parse("http://" + addr)
	c := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}
	for i := range 2 { // second request reuses the proxy connection
		resp, err := c.Get("http://example.test/x")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(b) != "hello /x" {
			t.Fatalf("request %d: got %q", i, b)
		}
	}
	if log.addrs[0] != "example.test:80" {
		t.Fatalf("dialed %q", log.addrs)
	}
}

func TestConnect(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			io.Copy(c, c)
			c.Close()
		}
	}()
	addr, log := start(t, ln.Addr().String(), true)
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// Send the CONNECT and the first tunnel bytes together, as TLS clients may.
	fmt.Fprintf(c, "CONNECT api.openai.com:443 HTTP/1.1\r\nHost: api.openai.com:443\r\n\r\nearly-bytes")
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("CONNECT: %v %v", resp, err)
	}
	got := make([]byte, len("early-bytes"))
	if _, err := io.ReadFull(br, got); err != nil || string(got) != "early-bytes" {
		t.Fatalf("tunnel echo %q %v", got, err)
	}
	if log.addrs[0] != "api.openai.com:443" {
		t.Fatalf("dialed %q", log.addrs)
	}
}

func TestRefusesUnselected(t *testing.T) {
	addr, log := start(t, "127.0.0.1:1", false)
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintf(c, "CONNECT x:443 HTTP/1.1\r\nHost: x:443\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil || resp.StatusCode != 403 || len(log.addrs) != 0 {
		t.Fatalf("got %v %v, dials %q", resp, err, log.addrs)
	}
}
