package winnet

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// IP_UNICAST_IF / IPV6_UNICAST_IF
const sockoptUnicastIf = 31

// DirectDial dials through the interface that owns the real default route,
// so the connection leaves through the physical network and not the TUN.
func DirectDial(ctx context.Context, network, address string) (net.Conn, error) {
	d := net.Dialer{KeepAlive: 30 * time.Second, Control: bindControl}
	return d.DialContext(ctx, network, address)
}

func bindControl(network, address string, c syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return err
	}
	v6 := ap.Addr().Is6() && !ap.Addr().Is4In6()
	idx, err := defaultIfIndex(v6)
	if err != nil {
		return err
	}
	var serr error
	err = c.Control(func(fd uintptr) {
		s := windows.Handle(fd)
		if v6 {
			serr = windows.SetsockoptInt(s, windows.IPPROTO_IPV6, sockoptUnicastIf, int(idx))
			return
		}
		// The IPv4 option takes the index in network byte order.
		var be [4]byte
		binary.BigEndian.PutUint32(be[:], idx)
		serr = windows.SetsockoptInt(s, windows.IPPROTO_IP, sockoptUnicastIf, int(binary.NativeEndian.Uint32(be[:])))
	})
	if err != nil {
		return err
	}
	return serr
}

var (
	defMu    sync.Mutex
	defCache [2]struct {
		idx uint32
		at  time.Time
	}
)

// defaultIfIndex finds the lowest-metric interface with a 0.0.0.0/0 (or ::/0)
// route, skipping our own adapter. Cached briefly: it is called per dial.
func defaultIfIndex(v6 bool) (uint32, error) {
	i := 0
	fam := winipcfg.AddressFamily(windows.AF_INET)
	if v6 {
		i, fam = 1, windows.AF_INET6
	}
	defMu.Lock()
	defer defMu.Unlock()
	if c := defCache[i]; c.idx != 0 && time.Since(c.at) < 2*time.Second {
		return c.idx, nil
	}
	idx, err := findDefault(fam)
	if err != nil {
		return 0, err
	}
	defCache[i].idx, defCache[i].at = idx, time.Now()
	return idx, nil
}

func findDefault(fam winipcfg.AddressFamily) (uint32, error) {
	rows, err := winipcfg.GetIPForwardTable2(fam)
	if err != nil {
		return 0, err
	}
	best, bestMetric := uint32(0), ^uint32(0)
	for _, r := range rows {
		if r.DestinationPrefix.PrefixLength != 0 {
			continue
		}
		iface, err := r.InterfaceLUID.IPInterface(fam)
		if err != nil || !iface.Connected {
			continue
		}
		if isOwnAdapter(r.InterfaceLUID) {
			continue
		}
		if m := r.Metric + iface.Metric; m < bestMetric {
			best, bestMetric = r.InterfaceIndex, m
		}
	}
	if best == 0 {
		return 0, fmt.Errorf("no default route on a physical interface")
	}
	return best, nil
}

func isOwnAdapter(luid winipcfg.LUID) bool {
	row, err := luid.Interface()
	return err == nil && row.Alias() == AdapterName
}

// NativeIPv6 reports whether a non-TailSplit interface has an IPv6 default
// route and a global address. Without it, routing IPv6 into the TUN would
// make IPv6 look available on an IPv4-only network.
func NativeIPv6() bool {
	_, err := findDefault(windows.AF_INET6)
	if err != nil {
		return false
	}
	addrs, err := winipcfg.GetUnicastIPAddressTable(windows.AF_INET6)
	if err != nil {
		return false
	}
	for _, a := range addrs {
		ip := a.Address.Addr()
		if isOwnAdapter(a.InterfaceLUID) || !ip.IsGlobalUnicast() || ip.IsPrivate() {
			continue
		}
		if a.DadState == winipcfg.DadStatePreferred {
			return true
		}
	}
	return false
}
