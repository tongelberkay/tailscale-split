// Package winnet owns the Windows side: the Wintun adapter, its addresses
// and routes, and sockets that bypass it.
package winnet

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"sync"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wintun"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

const (
	AdapterName = "TailSplit"
	tunnelType  = "TailSplit"
	MTU         = 1500
	ringSize    = 0x800000 // 8 MiB, the size WireGuard uses
)

// A fixed GUID keeps Windows from creating "TailSplit 2", "TailSplit 3"...
// network profiles on every start.
var adapterGUID = windows.GUID{Data1: 0x7a11_5b17, Data2: 0x7a11, Data3: 0x4e75,
	Data4: [8]byte{0x8c, 0x1b, 0x54, 0x61, 0x69, 0x6c, 0x53, 0x70}}

var (
	tunAddr4 = netip.MustParsePrefix("172.31.255.1/30")
	tunAddr6 = netip.MustParsePrefix("fd55:6170:7073::1/126")
	// Two halves instead of a default route: they win over the physical
	// default route by being more specific, while the physical 0.0.0.0/0
	// stays in place. Tailscale's own sockets bind to the interface that
	// owns a real default route, so they never loop back into the TUN.
	routes4 = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/1"), netip.MustParsePrefix("128.0.0.0/1")}
	routes6 = []netip.Prefix{netip.MustParsePrefix("::/1"), netip.MustParsePrefix("8000::/1")}
)

// Tun is a Wintun adapter with a running session.
type Tun struct {
	adapter *wintun.Adapter
	session wintun.Session
	wait    windows.Handle
	once    sync.Once
	closed  chan struct{}
}

// Open creates the adapter and routes IPv4 (and IPv6 when withIPv6) through
// it. Windows removes the adapter, and with it the routes, when this process
// exits, so a crash cannot leave the machine's traffic black-holed.
func Open(withIPv6 bool) (*Tun, error) {
	// A previous instance that was killed may have left its adapter behind.
	if old, err := wintun.OpenAdapter(AdapterName); err == nil {
		old.Close()
	}
	a, err := wintun.CreateAdapter(AdapterName, tunnelType, &adapterGUID)
	if err != nil {
		if errors.Is(err, windows.ERROR_MOD_NOT_FOUND) || errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("wintun.dll not found next to tailsplit.exe: %w", err)
		}
		return nil, fmt.Errorf("create Wintun adapter (needs administrator): %w", err)
	}
	t := &Tun{adapter: a, closed: make(chan struct{})}
	if err := t.configure(withIPv6); err != nil {
		a.Close()
		return nil, err
	}
	s, err := a.StartSession(ringSize)
	if err != nil {
		a.Close()
		return nil, fmt.Errorf("start Wintun session: %w", err)
	}
	t.session = s
	t.wait = s.ReadWaitEvent()
	return t, nil
}

func (t *Tun) configure(withIPv6 bool) error {
	luid := winipcfg.LUID(t.adapter.LUID())
	addrs := []netip.Prefix{tunAddr4}
	var rs []*winipcfg.RouteData
	for _, p := range routes4 {
		rs = append(rs, &winipcfg.RouteData{Destination: p, NextHop: netip.IPv4Unspecified(), Metric: 0})
	}
	if withIPv6 {
		addrs = append(addrs, tunAddr6)
		for _, p := range routes6 {
			rs = append(rs, &winipcfg.RouteData{Destination: p, NextHop: netip.IPv6Unspecified(), Metric: 0})
		}
	}
	if err := luid.SetIPAddresses(addrs); err != nil {
		return fmt.Errorf("set TUN addresses: %w", err)
	}
	if err := luid.SetRoutes(rs); err != nil {
		return fmt.Errorf("set TUN routes: %w", err)
	}
	for _, fam := range []winipcfg.AddressFamily{windows.AF_INET, windows.AF_INET6} {
		if fam == windows.AF_INET6 && !withIPv6 {
			continue
		}
		iface, err := luid.IPInterface(fam)
		if err != nil {
			return fmt.Errorf("read TUN interface: %w", err)
		}
		iface.NLMTU = MTU
		// Never let Windows pick this adapter for DNS or as a "real" network.
		iface.UseAutomaticMetric = false
		iface.Metric = 0
		iface.ManagedAddressConfigurationSupported = false
		iface.OtherStatefulConfigurationSupported = false
		iface.RouterDiscoveryBehavior = winipcfg.RouterDiscoveryDisabled
		iface.DadTransmits = 0
		if err := iface.Set(); err != nil {
			return fmt.Errorf("configure TUN interface: %w", err)
		}
	}
	return nil
}

// ReadPacket blocks until a packet arrives.
func (t *Tun) ReadPacket(buf []byte) (int, error) {
	for {
		select {
		case <-t.closed:
			return 0, os.ErrClosed
		default:
		}
		p, err := t.session.ReceivePacket()
		switch err {
		case nil:
			n := copy(buf, p)
			t.session.ReleaseReceivePacket(p)
			return n, nil
		case windows.ERROR_NO_MORE_ITEMS:
			windows.WaitForSingleObject(t.wait, 250)
		case windows.ERROR_HANDLE_EOF:
			return 0, os.ErrClosed
		default:
			return 0, err
		}
	}
}

func (t *Tun) WritePacket(pkt []byte) error {
	select {
	case <-t.closed:
		return os.ErrClosed
	default:
	}
	b, err := t.session.AllocateSendPacket(len(pkt))
	if err != nil {
		if err == windows.ERROR_BUFFER_OVERFLOW {
			return nil // ring full: drop, like a congested link would
		}
		return err
	}
	copy(b, pkt)
	t.session.SendPacket(b)
	return nil
}

func (t *Tun) Close() error {
	t.once.Do(func() {
		close(t.closed)
		t.session.End()
		t.adapter.Close()
	})
	return nil
}
