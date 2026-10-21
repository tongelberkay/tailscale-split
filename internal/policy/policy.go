// Package policy decides, per flow, whether it leaves through the tailnet
// exit node or directly through the physical network.
package policy

import (
	"net/netip"
	"strings"
)

type Route int

const (
	Direct Route = iota
	Tailnet
)

func (r Route) String() string {
	if r == Tailnet {
		return "tailnet"
	}
	return "direct"
}

// Policy is immutable; build a new one when the app list changes.
type Policy struct {
	apps map[string]bool // lower-cased executable paths
}

func New(apps []string) *Policy {
	p := &Policy{apps: make(map[string]bool, len(apps))}
	for _, a := range apps {
		p.apps[strings.ToLower(a)] = true
	}
	return p
}

// Decide picks the route for a flow to dst. owner is the owning process's
// executable path followed by its ancestors' paths; a flow is selected when
// any of them is. An unidentified owner (nil) goes direct.
func (p *Policy) Decide(owner []string, dst netip.Addr) Route {
	if !p.Selected(owner) {
		return Direct
	}
	if IsTailnet(dst) {
		// Peers are dialed through our own node, so selected apps reach the
		// tailnet even when the regular Tailscale client is not running.
		return Tailnet
	}
	if IsLocal(dst) {
		return Direct
	}
	return Tailnet
}

// Selected reports whether the process or one of its ancestors is selected.
func (p *Policy) Selected(owner []string) bool {
	for _, path := range owner {
		if p.apps[strings.ToLower(path)] {
			return true
		}
	}
	return false
}

var (
	cgnat        = netip.MustParsePrefix("100.64.0.0/10")
	tailscaleULA = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

// IsTailnet reports Tailscale's own address ranges.
func IsTailnet(a netip.Addr) bool {
	a = a.Unmap()
	return cgnat.Contains(a) || tailscaleULA.Contains(a)
}

// IsLocal reports destinations that must never go through the exit node:
// LAN, loopback, link-local and multicast.
func IsLocal(a netip.Addr) bool {
	a = a.Unmap()
	return !a.IsValid() || a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() ||
		a.IsLinkLocalMulticast() || a.IsMulticast() || a.IsUnspecified() ||
		a == netip.AddrFrom4([4]byte{255, 255, 255, 255})
}
