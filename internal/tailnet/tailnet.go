// Package tailnet runs TailSplit's own embedded Tailscale node. It is a
// separate device in the tailnet and never touches the regular Tailscale
// client's settings or state.
package tailnet

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/logtail"
	"tailscale.com/tsnet"
)

// Status is a snapshot for the UI.
type Status struct {
	State    string // ipn backend state: NeedsLogin, NeedsMachineAuth, Running...
	AuthURL  string
	Self     string // our MagicDNS name or IP
	ExitNode string // the selected exit node's name, empty if none active
	ExitOK   bool   // exit node selected and online
}

type Node struct {
	srv  *tsnet.Server
	lc   *local.Client
	logf func(string, ...any)

	mu     sync.Mutex
	status Status
	notify func(Status)
}

// New creates the node; Start brings it up. Logs go to logf only: uploads to
// Tailscale's log service are turned off for this process.
func New(stateDir, hostname string, logf func(string, ...any), notify func(Status)) *Node {
	logtail.Disable()
	n := &Node{logf: logf, notify: notify}
	n.srv = &tsnet.Server{
		Dir:      filepath.Join(stateDir, "tailscale"),
		Hostname: hostname,
		Logf:     func(f string, a ...any) { logf("tsnet: "+f, a...) },
		UserLogf: func(f string, a ...any) { logf(f, a...) },
	}
	return n
}

func (n *Node) Start(ctx context.Context) error {
	if err := n.srv.Start(); err != nil {
		return err
	}
	lc, err := n.srv.LocalClient()
	if err != nil {
		return err
	}
	n.lc = lc
	go n.watch(ctx)
	return nil
}

func (n *Node) Close() error { return n.srv.Close() }

func (n *Node) Status() Status {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.status
}

func (n *Node) update(f func(*Status)) {
	n.mu.Lock()
	f(&n.status)
	s := n.status
	n.mu.Unlock()
	if n.notify != nil {
		n.notify(s)
	}
}

// watch follows the node's state and sign-in URL.
func (n *Node) watch(ctx context.Context) {
	for ctx.Err() == nil {
		w, err := n.lc.WatchIPNBus(ctx, ipn.NotifyInitialState|ipn.NotifyNoPrivateKeys)
		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		for {
			msg, err := w.Next()
			if err != nil {
				break
			}
			if msg.State != nil {
				st := msg.State.String()
				n.update(func(s *Status) {
					s.State = st
					if st == "Running" {
						s.AuthURL = ""
					}
				})
				if st == "NeedsLogin" {
					// Ask control for a sign-in URL; it arrives as BrowseToURL.
					go n.lc.StartLoginInteractive(ctx)
				}
			}
			if msg.BrowseToURL != nil && *msg.BrowseToURL != "" {
				u := *msg.BrowseToURL
				n.update(func(s *Status) { s.AuthURL = u })
			}
		}
		w.Close()
	}
}

// WaitRunning blocks until the node is signed in and connected.
func (n *Node) WaitRunning(ctx context.Context) error {
	for {
		if n.Status().State == "Running" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// SetExitNode selects the exit node by Tailscale IP, host name or MagicDNS
// name. It fails, rather than silently routing direct, when no matching
// peer offers itself as an exit node to us.
func (n *Node) SetExitNode(ctx context.Context, want string) error {
	st, err := n.lc.Status(ctx)
	if err != nil {
		return err
	}
	peer, err := FindExitNode(st, want)
	if err != nil {
		return err
	}
	_, err = n.lc.EditPrefs(ctx, &ipn.MaskedPrefs{
		Prefs: ipn.Prefs{
			ExitNodeID:             peer.ID,
			ExitNodeAllowLANAccess: true,
		},
		ExitNodeIDSet:             true,
		ExitNodeAllowLANAccessSet: true,
	})
	if err != nil {
		return fmt.Errorf("select exit node: %w", err)
	}
	name := strings.TrimSuffix(peer.DNSName, ".")
	n.update(func(s *Status) { s.ExitNode = name; s.ExitOK = peer.Online })
	return nil
}

// Refresh updates the exit node's online state and our own name.
func (n *Node) Refresh(ctx context.Context) {
	st, err := n.lc.Status(ctx)
	if err != nil {
		return
	}
	n.update(func(s *Status) {
		if st.Self != nil {
			s.Self = strings.TrimSuffix(st.Self.DNSName, ".")
		}
		s.ExitOK = false
		for _, p := range st.Peer {
			if p.ExitNode {
				s.ExitNode = strings.TrimSuffix(p.DNSName, ".")
				s.ExitOK = p.Online
			}
		}
	})
}

// FindExitNode matches want against peers' IPs, host names and MagicDNS names
// (full or first label), case-insensitively.
func FindExitNode(st *ipnstate.Status, want string) (*ipnstate.PeerStatus, error) {
	want = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(want), "."))
	ip, ipErr := netip.ParseAddr(want)
	var match *ipnstate.PeerStatus
	for _, p := range st.Peer {
		dns := strings.ToLower(strings.TrimSuffix(p.DNSName, "."))
		label, _, _ := strings.Cut(dns, ".")
		hit := strings.EqualFold(p.HostName, want) || dns == want || label == want
		if ipErr == nil {
			for _, a := range p.TailscaleIPs {
				if a == ip {
					hit = true
				}
			}
		}
		if !hit {
			continue
		}
		if !p.ExitNodeOption {
			return nil, fmt.Errorf("%s is in the tailnet but is not an exit node available to this device (it must advertise an exit route, be approved, and be allowed by your ACLs)", label)
		}
		if match != nil && match.ID != p.ID {
			return nil, fmt.Errorf("%q matches more than one device; use its Tailscale IP", want)
		}
		match = p
	}
	if match == nil {
		return nil, fmt.Errorf("no device named or addressed %q is visible in the tailnet", want)
	}
	return match, nil
}

// Dial dials through the embedded node: tailnet peers directly, everything
// else through the selected exit node.
func (n *Node) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	// Until the exit node's routes are installed, tsnet would dial public
	// addresses from this process directly, quietly bypassing the exit node.
	// Refuse unless the dial is planned to go through Tailscale.
	d, ok := n.srv.Sys().Dialer.GetOK()
	if !ok {
		return nil, errors.New("tailnet not started")
	}
	_, viaTailscale, err := d.UserDialPlan(ctx, network, address)
	if err != nil {
		return nil, err
	}
	if !viaTailscale {
		return nil, errors.New("exit node not active; refusing to send selected app traffic directly")
	}
	return n.srv.Dial(ctx, network, address)
}

// Logout removes this device's sign-in.
func (n *Node) Logout(ctx context.Context) error { return n.lc.Logout(ctx) }
