package tailnet

import (
	"net/netip"
	"strings"
	"testing"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

func peer(id, host, dns, ip string, exit bool) *ipnstate.PeerStatus {
	return &ipnstate.PeerStatus{ID: tailcfgID(id), HostName: host, DNSName: dns,
		TailscaleIPs: []netip.Addr{netip.MustParseAddr(ip)}, ExitNodeOption: exit}
}

func TestFindExitNode(t *testing.T) {
	st := &ipnstate.Status{Peer: map[key.NodePublic]*ipnstate.PeerStatus{
		key.NewNode().Public(): peer("n1", "Home-Server", "home-server.tail1.ts.net.", "100.64.0.1", true),
		key.NewNode().Public(): peer("n2", "laptop", "laptop.tail1.ts.net.", "100.64.0.2", false),
	}}
	for _, q := range []string{"home-server", "HOME-SERVER", "home-server.tail1.ts.net", "home-server.tail1.ts.net.", "100.64.0.1"} {
		p, err := FindExitNode(st, q)
		if err != nil || p.ID != "n1" {
			t.Errorf("%q: %v, %v", q, p, err)
		}
	}
	if _, err := FindExitNode(st, "laptop"); err == nil || !strings.Contains(err.Error(), "not an exit node") {
		t.Errorf("laptop: %v", err)
	}
	if _, err := FindExitNode(st, "nope"); err == nil {
		t.Error("nope: expected error")
	}
}

func tailcfgID(s string) tailcfg.StableNodeID { return tailcfg.StableNodeID(s) }

func TestListExitNodes(t *testing.T) {
	a := peer("n1", "zeta", "zeta.tail1.ts.net.", "100.64.0.9", true)
	a.Online, a.ExitNode = true, true
	st := &ipnstate.Status{Peer: map[key.NodePublic]*ipnstate.PeerStatus{
		key.NewNode().Public(): a,
		key.NewNode().Public(): peer("n2", "Alpha", "alpha.tail1.ts.net.", "100.64.0.2", true),
		key.NewNode().Public(): peer("n3", "laptop", "laptop.tail1.ts.net.", "100.64.0.3", false),
	}}
	got := ListExitNodes(st)
	if len(got) != 2 || got[0].Name != "alpha" || got[1].Name != "zeta" {
		t.Fatalf("got %+v", got)
	}
	if !got[1].Online || !got[1].Active || got[1].IP != "100.64.0.9" || got[0].Active {
		t.Errorf("flags: %+v", got)
	}
}
