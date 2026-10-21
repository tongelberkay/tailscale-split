package engine

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"

	"tailsplit/internal/policy"
	"tailsplit/internal/proc"
)

// fakeTUN connects the engine to a second gVisor stack that plays the role
// of Windows and its applications.
type fakeTUN struct {
	toEngine chan []byte
	app      *channel.Endpoint
	done     chan struct{}
	once     sync.Once
}

func (f *fakeTUN) ReadPacket(buf []byte) (int, error) {
	select {
	case p := <-f.toEngine:
		return copy(buf, p), nil
	case <-f.done:
		return 0, os.ErrClosed
	}
}

func (f *fakeTUN) WritePacket(p []byte) error {
	pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(append([]byte(nil), p...))})
	f.app.InjectInbound(ipv4.ProtocolNumber, pkt)
	pkt.DecRef()
	return nil
}

func (f *fakeTUN) Close() error { f.once.Do(func() { close(f.done) }); return nil }

const appAddr = "172.31.255.1"

func newAppStack(t *testing.T) (*stack.Stack, *fakeTUN) {
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})
	ep := channel.New(512, 1500, "")
	if err := s.CreateNIC(1, ep); err != nil {
		t.Fatal(err)
	}
	s.AddProtocolAddress(1, tcpip.ProtocolAddress{
		Protocol:          ipv4.ProtocolNumber,
		AddressWithPrefix: tcpip.AddrFrom4([4]byte{172, 31, 255, 1}).WithPrefix(),
	}, stack.AddressProperties{})
	s.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: 1}})
	f := &fakeTUN{toEngine: make(chan []byte, 512), app: ep, done: make(chan struct{})}
	go func() {
		for {
			pkt := ep.ReadContext(context.Background())
			if pkt == nil {
				return
			}
			v := pkt.ToView()
			b := append([]byte(nil), v.AsSlice()...)
			v.Release()
			pkt.DecRef()
			select {
			case f.toEngine <- b:
			case <-f.done:
				return
			}
		}
	}()
	t.Cleanup(func() { f.Close(); ep.Close(); s.Close() })
	return s, f
}

// recorder dials a local echo server whatever address it is given, and
// remembers which addresses it was asked for.
type recorder struct {
	mu    sync.Mutex
	addrs []string
	tcp   string
	udp   string
}

func (r *recorder) dial(ctx context.Context, network, address string) (net.Conn, error) {
	r.mu.Lock()
	r.addrs = append(r.addrs, network+" "+address)
	r.mu.Unlock()
	var d net.Dialer
	if network == "udp" {
		return d.DialContext(ctx, "udp", r.udp)
	}
	return d.DialContext(ctx, "tcp", r.tcp)
}

func (r *recorder) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.addrs...)
}

func echoServers(t *testing.T) (tcpAddr, udpAddr string) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		buf := make([]byte, 2048)
		for {
			n, a, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			pc.WriteTo(buf[:n], a)
		}
	}()
	t.Cleanup(func() { ln.Close(); pc.Close() })
	return ln.Addr().String(), pc.LocalAddr().String()
}

const selectedExe = `C:\Apps\browser.exe`

func TestEngineRoutesByProcess(t *testing.T) {
	tcpEcho, udpEcho := echoServers(t)
	tailnet := &recorder{tcp: tcpEcho, udp: udpEcho}
	direct := &recorder{tcp: tcpEcho, udp: udpEcho}
	appStack, dev := newAppStack(t)

	// Port 443 belongs to the "selected" app, everything else to another app.
	lookup := func(f proc.Flow) string {
		if f.Local.Addr().String() != appAddr {
			t.Errorf("lookup got local %v, want the app's address", f.Local)
		}
		if f.Remote.Port() == 443 {
			return selectedExe
		}
		return `C:\Other\app.exe`
	}
	e, err := New(Config{Device: dev, MTU: 1500, Tailnet: tailnet.dial, Direct: direct.dial, Lookup: lookup, Logf: t.Logf},
		policy.New([]string{selectedExe}))
	if err != nil {
		t.Fatal(err)
	}
	go e.Run()
	t.Cleanup(e.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	roundTrip := func(c net.Conn, msg []byte) {
		t.Helper()
		c.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := c.Write(msg); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(msg))
		if _, err := io.ReadFull(c, got); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, msg) {
			t.Fatalf("echo mismatch")
		}
	}

	dialTCP := func(ip [4]byte, port uint16) net.Conn {
		t.Helper()
		c, err := gonet.DialContextTCP(ctx, appStack, tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4(ip), Port: port}, ipv4.ProtocolNumber)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	// A large payload exercises windowing in both stacks.
	big := bytes.Repeat([]byte("tailsplit"), 200_000)
	c := dialTCP([4]byte{1, 1, 1, 1}, 443)
	roundTrip(c, big)
	c.Close()

	c = dialTCP([4]byte{93, 184, 216, 34}, 80)
	roundTrip(c, []byte("hello direct"))
	c.Close()

	// Selected app, but a LAN destination: stays direct.
	c = dialTCP([4]byte{192, 168, 1, 20}, 443)
	roundTrip(c, []byte("hello lan"))
	c.Close()

	u, err := gonet.DialUDP(appStack, nil, &tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4([4]byte{1, 0, 0, 1}), Port: 443}, ipv4.ProtocolNumber)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip(u, []byte("quic-ish datagram"))
	u.Close()

	wantTailnet := []string{"tcp 1.1.1.1:443", "udp 1.0.0.1:443"}
	wantDirect := []string{"tcp 93.184.216.34:80", "tcp 192.168.1.20:443"}
	if got := tailnet.seen(); !equal(got, wantTailnet) {
		t.Errorf("tailnet dials %q, want %q", got, wantTailnet)
	}
	if got := direct.seen(); !equal(got, wantDirect) {
		t.Errorf("direct dials %q, want %q", got, wantDirect)
	}
}

// A failed upstream dial must refuse the app's connection, not accept it.
func TestEngineRefusesWhenUpstreamFails(t *testing.T) {
	appStack, dev := newAppStack(t)
	fail := func(context.Context, string, string) (net.Conn, error) { return nil, io.ErrUnexpectedEOF }
	e, err := New(Config{Device: dev, Tailnet: fail, Direct: fail, Lookup: func(proc.Flow) string { return selectedExe }},
		policy.New([]string{selectedExe}))
	if err != nil {
		t.Fatal(err)
	}
	go e.Run()
	t.Cleanup(e.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = gonet.DialContextTCP(ctx, appStack, tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4([4]byte{1, 1, 1, 1}), Port: 443}, ipv4.ProtocolNumber)
	if err == nil {
		t.Fatal("connection was accepted although the upstream dial failed")
	}
	if e.DialErrors.Load() != 1 {
		t.Fatalf("DialErrors = %d", e.DialErrors.Load())
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
