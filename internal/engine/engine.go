// Package engine terminates every TCP/UDP flow that Windows routes into the
// TUN in a userspace network stack, asks the policy where the flow belongs,
// and re-dials it either through the tailnet or directly.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"

	"tailsplit/internal/policy"
	"tailsplit/internal/proc"
)

// Device is a layer-3 packet device such as a Wintun session.
type Device interface {
	// ReadPacket copies one IP packet into buf and returns its length.
	ReadPacket(buf []byte) (int, error)
	WritePacket(pkt []byte) error
	Close() error
}

type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

type Config struct {
	Device Device
	MTU    int
	// Tailnet dials through the exit node (or to a tailnet peer).
	Tailnet DialFunc
	// Direct dials through the physical network, bypassing the TUN.
	Direct DialFunc
	// Lookup maps a flow to its owning executable path ("" if unknown).
	Lookup func(proc.Flow) string
	Logf   func(format string, args ...any)
}

type Engine struct {
	cfg    Config
	stack  *stack.Stack
	link   *channel.Endpoint
	policy atomic.Pointer[policy.Policy]
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Counters for the status display.
	FlowsTailnet, FlowsDirect, DialErrors atomic.Int64
}

const (
	nicID       = 1
	dialTimeout = 10 * time.Second
	udpIdle     = 60 * time.Second
	dnsIdle     = 10 * time.Second
)

func New(cfg Config, p *policy.Policy) (*Engine, error) {
	if cfg.Device == nil || cfg.Tailnet == nil || cfg.Direct == nil || cfg.Lookup == nil {
		return nil, errors.New("engine: incomplete config")
	}
	if cfg.MTU == 0 {
		cfg.MTU = 1500
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, ipv6.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
		HandleLocal:        false,
	})
	link := channel.New(1024, uint32(cfg.MTU), "")
	if err := s.CreateNIC(nicID, link); err != nil {
		return nil, fmt.Errorf("engine: create NIC: %v", err)
	}
	// Accept packets for any destination and answer from any source: the
	// stack impersonates every remote host the applications talk to.
	s.SetPromiscuousMode(nicID, true)
	s.SetSpoofing(nicID, true)
	s.SetRouteTable([]tcpip.Route{
		{Destination: header.IPv4EmptySubnet, NIC: nicID},
		{Destination: header.IPv6EmptySubnet, NIC: nicID},
	})
	sack := tcpip.TCPSACKEnabled(true)
	s.SetTransportProtocolOption(tcp.ProtocolNumber, &sack)
	// Larger buffers matter for throughput on the direct path.
	rcv := tcpip.TCPReceiveBufferSizeRangeOption{Min: 4 << 10, Default: 1 << 20, Max: 8 << 20}
	s.SetTransportProtocolOption(tcp.ProtocolNumber, &rcv)
	snd := tcpip.TCPSendBufferSizeRangeOption{Min: 4 << 10, Default: 1 << 20, Max: 8 << 20}
	s.SetTransportProtocolOption(tcp.ProtocolNumber, &snd)

	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{cfg: cfg, stack: s, link: link, ctx: ctx, cancel: cancel}
	e.policy.Store(p)
	s.SetTransportProtocolHandler(tcp.ProtocolNumber, tcp.NewForwarder(s, 0, 4096, e.handleTCP).HandlePacket)
	s.SetTransportProtocolHandler(udp.ProtocolNumber, udp.NewForwarder(s, e.handleUDP).HandlePacket)
	return e, nil
}

// SetPolicy swaps the app list without restarting; it affects new flows only.
func (e *Engine) SetPolicy(p *policy.Policy) { e.policy.Store(p) }

// Run pumps packets until the device fails or Close is called.
func (e *Engine) Run() error {
	errc := make(chan error, 2)
	e.wg.Add(2)
	go func() { defer e.wg.Done(); errc <- e.inbound() }()
	go func() { defer e.wg.Done(); errc <- e.outbound() }()
	err := <-errc
	e.cancel()
	return err
}

func (e *Engine) Close() {
	e.cancel()
	e.cfg.Device.Close()
	e.link.Close()
	e.stack.Close()
	e.wg.Wait()
}

// inbound moves packets from the TUN (sent by applications) into the stack.
func (e *Engine) inbound() error {
	buf := make([]byte, e.cfg.MTU+64)
	for {
		n, err := e.cfg.Device.ReadPacket(buf)
		if err != nil {
			if e.ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("engine: read from TUN: %w", err)
		}
		if n == 0 {
			continue
		}
		var proto tcpip.NetworkProtocolNumber
		switch buf[0] >> 4 {
		case 4:
			proto = ipv4.ProtocolNumber
		case 6:
			proto = ipv6.ProtocolNumber
		default:
			continue
		}
		pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
			Payload: buffer.MakeWithData(append([]byte(nil), buf[:n]...)),
		})
		e.link.InjectInbound(proto, pkt)
		pkt.DecRef()
	}
}

// outbound moves packets the stack generates (replies) back into the TUN.
func (e *Engine) outbound() error {
	for {
		pkt := e.link.ReadContext(e.ctx)
		if pkt == nil {
			return nil // closed
		}
		v := pkt.ToView()
		err := e.cfg.Device.WritePacket(v.AsSlice())
		v.Release()
		pkt.DecRef()
		if err != nil && e.ctx.Err() == nil {
			return fmt.Errorf("engine: write to TUN: %w", err)
		}
	}
}

func addrPort(a tcpip.Address, port uint16) netip.AddrPort {
	ip, _ := netip.AddrFromSlice(a.AsSlice())
	return netip.AddrPortFrom(ip.Unmap(), port)
}

// route resolves which way a new flow goes. In the forwarder's view the
// "local" end is the destination the app dialed and the "remote" end is
// the app itself.
func (e *Engine) route(udp bool, id stack.TransportEndpointID) (src, dst netip.AddrPort, r policy.Route, exe string) {
	src = addrPort(id.RemoteAddress, id.RemotePort)
	dst = addrPort(id.LocalAddress, id.LocalPort)
	exe = e.cfg.Lookup(proc.Flow{UDP: udp, Local: src, Remote: dst})
	r = e.policy.Load().Decide(exe, dst.Addr())
	if r == policy.Tailnet {
		e.FlowsTailnet.Add(1)
	} else {
		e.FlowsDirect.Add(1)
	}
	return
}

func (e *Engine) dial(ctx context.Context, r policy.Route, network string, dst netip.AddrPort) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	if r == policy.Tailnet {
		return e.cfg.Tailnet(ctx, network, dst.String())
	}
	return e.cfg.Direct(ctx, network, dst.String())
}

func (e *Engine) handleTCP(req *tcp.ForwarderRequest) {
	src, dst, r, exe := e.route(false, req.ID())
	// Dial first, so a failed upstream shows up to the app as a refused
	// connection instead of an accepted one that immediately dies.
	up, err := e.dial(e.ctx, r, "tcp", dst)
	if err != nil {
		e.DialErrors.Add(1)
		e.cfg.Logf("tcp %v -> %v via %v (%s): %v", src, dst, r, exeName(exe), err)
		req.Complete(true)
		return
	}
	var wq waiter.Queue
	ep, terr := req.CreateEndpoint(&wq)
	if terr != nil {
		up.Close()
		req.Complete(true)
		return
	}
	req.Complete(false)
	ep.SocketOptions().SetKeepAlive(true)
	if r == policy.Tailnet {
		e.cfg.Logf("tcp %v -> %v via tailnet (%s)", src, dst, exeName(exe))
	}
	relayTCP(gonet.NewTCPConn(&wq, ep), up)
}

func (e *Engine) handleUDP(req *udp.ForwarderRequest) bool {
	id := req.ID()
	var wq waiter.Queue
	ep, terr := req.CreateEndpoint(&wq)
	if terr != nil {
		return false
	}
	down := gonet.NewUDPConn(&wq, ep)
	go func() {
		defer down.Close()
		src, dst, r, exe := e.route(true, id)
		up, err := e.dial(e.ctx, r, "udp", dst)
		if err != nil {
			e.DialErrors.Add(1)
			e.cfg.Logf("udp %v -> %v via %v (%s): %v", src, dst, r, exeName(exe), err)
			return
		}
		defer up.Close()
		idle := udpIdle
		if dst.Port() == 53 {
			idle = dnsIdle
		}
		relayUDP(down, up, idle)
	}()
	return true
}

// relayTCP copies both ways, propagating half-closes so protocols that rely
// on them (HTTP/1.0, some RPCs) work.
func relayTCP(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		io.Copy(dst, src)
		if c, ok := dst.(interface{ CloseWrite() error }); ok {
			c.CloseWrite()
		} else {
			dst.Close()
		}
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
	// Give the other direction a moment to drain after a half-close.
	t := time.AfterFunc(30*time.Second, func() { a.Close(); b.Close() })
	<-done
	t.Stop()
	a.Close()
	b.Close()
}

// relayUDP copies datagrams both ways until neither side has sent anything
// for the idle period.
func relayUDP(a, b net.Conn, idle time.Duration) {
	var last atomic.Int64
	touch := func() { last.Store(time.Now().UnixNano()) }
	touch()
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		buf := make([]byte, 64<<10)
		for {
			src.SetReadDeadline(time.Now().Add(idle))
			n, err := src.Read(buf)
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() &&
					time.Since(time.Unix(0, last.Load())) < idle {
					continue // the other direction is still active
				}
				break
			}
			touch()
			if _, err := dst.Write(buf[:n]); err != nil {
				break
			}
		}
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
	a.Close()
	b.Close()
	<-done
}

func exeName(p string) string {
	if p == "" {
		return "unknown process"
	}
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '\\' || p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}
