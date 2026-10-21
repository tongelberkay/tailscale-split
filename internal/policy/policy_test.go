package policy

import (
	"net/netip"
	"testing"
)

func TestDecide(t *testing.T) {
	p := New([]string{`C:\Apps\Browser.exe`})
	sel, other := `c:\apps\browser.EXE`, `C:\Other\x.exe`
	cases := []struct {
		exe  []string
		dst  string
		want Route
	}{
		{[]string{sel}, "1.1.1.1", Tailnet},
		{[]string{sel}, "2606:4700::1111", Tailnet},
		{[]string{sel}, "100.100.1.2", Tailnet}, // tailnet peer
		{[]string{sel}, "fd7a:115c:a1e0::5", Tailnet},
		{[]string{sel}, "192.168.1.10", Direct}, // LAN stays local
		{[]string{sel}, "10.1.2.3", Direct},
		{[]string{sel}, "127.0.0.1", Direct},
		{[]string{sel}, "169.254.1.1", Direct},
		{[]string{sel}, "224.0.0.251", Direct},
		{[]string{sel}, "255.255.255.255", Direct},
		{[]string{sel}, "fe80::1", Direct},
		{[]string{other}, "1.1.1.1", Direct},
		{[]string{other}, "100.100.1.2", Direct},
		{nil, "1.1.1.1", Direct},                                               // unidentified process
		{[]string{`C:\\WebView\\msedgewebview2.exe`, sel}, "1.1.1.1", Tailnet}, // child of a selected app
		{[]string{sel, `C:\\Windows\\explorer.exe`}, "1.1.1.1", Tailnet},
		{[]string{other, `C:\\Windows\\explorer.exe`}, "1.1.1.1", Direct},
	}
	for _, c := range cases {
		if got := p.Decide(c.exe, netip.MustParseAddr(c.dst)); got != c.want {
			t.Errorf("Decide(%q, %s) = %v, want %v", c.exe, c.dst, got, c.want)
		}
	}
}
