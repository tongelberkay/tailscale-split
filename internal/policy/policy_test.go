package policy

import (
	"net/netip"
	"testing"
)

func TestDecide(t *testing.T) {
	p := New([]string{`C:\Apps\Browser.exe`})
	sel, other := `c:\apps\browser.EXE`, `C:\Other\x.exe`
	cases := []struct {
		exe, dst string
		want     Route
	}{
		{sel, "1.1.1.1", Tailnet},
		{sel, "2606:4700::1111", Tailnet},
		{sel, "100.100.1.2", Tailnet}, // tailnet peer
		{sel, "fd7a:115c:a1e0::5", Tailnet},
		{sel, "192.168.1.10", Direct}, // LAN stays local
		{sel, "10.1.2.3", Direct},
		{sel, "127.0.0.1", Direct},
		{sel, "169.254.1.1", Direct},
		{sel, "224.0.0.251", Direct},
		{sel, "255.255.255.255", Direct},
		{sel, "fe80::1", Direct},
		{other, "1.1.1.1", Direct},
		{other, "100.100.1.2", Direct},
		{"", "1.1.1.1", Direct}, // unidentified process
	}
	for _, c := range cases {
		if got := p.Decide(c.exe, netip.MustParseAddr(c.dst)); got != c.want {
			t.Errorf("Decide(%q, %s) = %v, want %v", c.exe, c.dst, got, c.want)
		}
	}
}
