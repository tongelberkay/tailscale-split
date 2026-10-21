// Package app ties the tailnet node, the TUN and the engine together and
// exposes start/stop to the UI.
package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"tailsplit/internal/config"
	"tailsplit/internal/engine"
	"tailsplit/internal/policy"
	"tailsplit/internal/proc"
	"tailsplit/internal/tailnet"
	"tailsplit/internal/winnet"
)

type State int

const (
	Stopped State = iota
	Starting
	Routing
)

func (s State) String() string {
	return [...]string{"Stopped", "Starting", "Routing"}[s]
}

// Snapshot is what the UI renders.
type Snapshot struct {
	State        State
	Tailnet      tailnet.Status
	Settings     config.Settings
	Err          string
	FlowsTailnet int64 // flows sent through the tailnet since start
	FlowsDirect  int64
}

type App struct {
	Dir  string
	Logf func(string, ...any)
	// OnChange is called (from any goroutine) when the snapshot changes.
	OnChange func()

	ctx    context.Context
	cancel context.CancelFunc
	node   *tailnet.Node

	mu       sync.Mutex
	state    State
	err      string
	settings config.Settings
	eng      *engine.Engine
	tun      *winnet.Tun
}

func New(dir string, logf func(string, ...any)) (*App, error) {
	s, err := config.Load(dir)
	if err != nil {
		return nil, err
	}
	if s.Hostname == "" {
		s.Hostname = config.DefaultHostname
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &App{Dir: dir, Logf: logf, ctx: ctx, cancel: cancel, settings: s}
	a.node = tailnet.New(dir, s.Hostname, logf, func(tailnet.Status) { a.changed() })
	return a, nil
}

// StartNode brings the tailnet node up (no TUN, no routes). Signing in only
// needs this.
func (a *App) StartNode() error {
	if err := a.node.Start(a.ctx); err != nil {
		return fmt.Errorf("start tailnet node: %w", err)
	}
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-a.ctx.Done():
				return
			case <-t.C:
				a.node.Refresh(a.ctx)
			}
		}
	}()
	return nil
}

func (a *App) changed() {
	if a.OnChange != nil {
		a.OnChange()
	}
}

func (a *App) Snapshot() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := Snapshot{State: a.state, Tailnet: a.node.Status(), Settings: a.settings, Err: a.err}
	if a.eng != nil {
		s.FlowsTailnet = a.eng.FlowsTailnet.Load()
		s.FlowsDirect = a.eng.FlowsDirect.Load()
	}
	return s
}

func (a *App) setState(s State, err error) {
	a.mu.Lock()
	a.state = s
	a.err = ""
	if err != nil {
		a.err = err.Error()
	}
	a.mu.Unlock()
	if err != nil {
		a.Logf("error: %v", err)
	}
	a.changed()
}

// Start signs in if needed, selects the exit node and starts routing.
// It returns once routing is up or has failed.
func (a *App) Start() error {
	a.mu.Lock()
	if a.state != Stopped {
		a.mu.Unlock()
		return nil
	}
	a.state = Starting
	s := a.settings
	a.mu.Unlock()
	a.changed()

	err := a.start(s)
	if err != nil {
		a.setState(Stopped, err)
		return err
	}
	a.setState(Routing, nil)
	return nil
}

func (a *App) start(s config.Settings) error {
	exit, err := config.ValidateExitNode(s.ExitNode)
	if err != nil {
		return err
	}
	if len(s.Apps) == 0 {
		return errors.New("no apps selected; add at least one")
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Minute)
	defer cancel()
	a.Logf("waiting for tailnet sign-in...")
	if err := a.node.WaitRunning(ctx); err != nil {
		return fmt.Errorf("tailnet did not come up (signed in? device approved?): %w", err)
	}
	if err := a.node.SetExitNode(ctx, exit); err != nil {
		return err
	}
	v6 := winnet.NativeIPv6()
	tun, err := winnet.Open(v6)
	if err != nil {
		return err
	}
	eng, err := engine.New(engine.Config{
		Device:  tun,
		MTU:     winnet.MTU,
		Tailnet: a.node.Dial,
		Direct:  winnet.DirectDial,
		Lookup:  proc.Lookup,
		Logf:    a.Logf,
	}, policy.New(s.Apps))
	if err != nil {
		tun.Close()
		return err
	}
	a.mu.Lock()
	a.tun, a.eng = tun, eng
	a.mu.Unlock()
	go func() {
		if err := eng.Run(); err != nil {
			a.Logf("engine stopped: %v", err)
			a.Stop()
			a.setState(Stopped, err)
		}
	}()
	a.Logf("routing %d app(s) via %s (IPv6 routed: %v)", len(s.Apps), exit, v6)
	return nil
}

// Stop removes the TUN and its routes. The tailnet node stays signed in.
func (a *App) Stop() {
	a.mu.Lock()
	eng := a.eng
	a.eng, a.tun = nil, nil
	was := a.state
	a.mu.Unlock()
	if eng != nil {
		eng.Close() // also closes the TUN
	}
	if was != Stopped {
		a.Logf("routing stopped")
		a.setState(Stopped, nil)
	}
}

// UpdateSettings saves new settings and applies the app list to running
// flows at once. A changed exit node needs a restart of routing.
func (a *App) UpdateSettings(f func(*config.Settings) error) error {
	a.mu.Lock()
	s := a.settings
	s.Apps = append([]string(nil), s.Apps...)
	a.mu.Unlock()
	if err := f(&s); err != nil {
		return err
	}
	apps, err := config.NormalizeApps(s.Apps)
	if err != nil {
		return err
	}
	s.Apps = apps
	if err := config.Save(a.Dir, s); err != nil {
		return err
	}
	return a.apply(s)
}

// Reload re-reads settings.json after the user edited it by hand.
func (a *App) Reload() error {
	s, err := config.Load(a.Dir)
	if err != nil {
		return err
	}
	if s.Hostname == "" {
		s.Hostname = config.DefaultHostname
	}
	return a.apply(s)
}

func (a *App) apply(s config.Settings) error {
	a.mu.Lock()
	exitChanged := s.ExitNode != a.settings.ExitNode
	a.settings = s
	eng := a.eng
	a.mu.Unlock()
	if eng != nil {
		eng.SetPolicy(policy.New(s.Apps))
		if exitChanged {
			if err := a.node.SetExitNode(a.ctx, s.ExitNode); err != nil {
				a.changed()
				return err
			}
		}
	}
	a.changed()
	return nil
}

func (a *App) Logout() error {
	a.Stop()
	return a.node.Logout(a.ctx)
}

func (a *App) Close() {
	a.Stop()
	a.cancel()
	a.node.Close()
}
