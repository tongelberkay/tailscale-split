package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"

	"tailsplit/internal/app"
	"tailsplit/internal/config"
)

var (
	iconOff     = Icon(0x8a, 0x93, 0xa0) // grey: not routing
	iconWaiting = Icon(0xf2, 0xa9, 0x3b) // amber: starting or needs sign-in
	iconOn      = Icon(0x2e, 0xb8, 0x72) // green: routing
	iconBroken  = Icon(0xe0, 0x4f, 0x4f) // red: routing but exit node offline
)

const maxAppItems = 24

type tray struct {
	a       *app.App
	logPath string

	status, signIn, toggle, exitNode *systray.MenuItem
	apps                             *systray.MenuItem
	appItems                         []*systray.MenuItem
	addApp, editSettings, openLog    *systray.MenuItem
	logout, quit                     *systray.MenuItem

	mu      sync.Mutex
	lastURL string
}

// Run shows the tray icon and blocks until Quit.
func Run(a *app.App, logPath string) {
	t := &tray{a: a, logPath: logPath}
	systray.Run(t.ready, a.Close)
}

func (t *tray) ready() {
	systray.SetIcon(iconOff)
	systray.SetTitle("TailSplit")
	systray.SetTooltip("TailSplit")

	t.status = systray.AddMenuItem("Starting...", "")
	t.status.Disable()
	t.exitNode = systray.AddMenuItem("", "")
	t.exitNode.Disable()
	t.signIn = systray.AddMenuItem("Sign in to Tailscale...", "Open the sign-in page in your browser")
	t.signIn.Hide()
	systray.AddSeparator()
	t.toggle = systray.AddMenuItem("Start routing", "")
	t.apps = systray.AddMenuItem("Apps using the exit node", "Click an app to remove it")
	for range maxAppItems {
		it := t.apps.AddSubMenuItem("", "Remove this app")
		it.Hide()
		t.appItems = append(t.appItems, it)
	}
	t.addApp = systray.AddMenuItem("Add apps...", "Choose .exe files whose traffic should use the exit node")
	t.editSettings = systray.AddMenuItem("Edit settings...", "Exit node and app list, as JSON")
	t.openLog = systray.AddMenuItem("Open log", "")
	systray.AddSeparator()
	t.logout = systray.AddMenuItem("Sign out of this device", "")
	t.quit = systray.AddMenuItem("Quit", "Stops routing and exits")

	t.a.OnChange = t.render
	t.render()
	go t.loop()
	go t.watchSettings()
	go func() {
		for range time.Tick(3 * time.Second) {
			t.render() // refresh flow counters
		}
	}()
}

func (t *tray) loop() {
	for i, it := range t.appItems {
		go func() {
			for range it.ClickedCh {
				t.removeApp(i)
			}
		}()
	}
	for {
		select {
		case <-t.signIn.ClickedCh:
			if u := t.a.Snapshot().Tailnet.AuthURL; u != "" {
				Open(u)
			}
		case <-t.toggle.ClickedCh:
			if t.a.Snapshot().State == app.Stopped {
				go func() {
					if err := t.a.Start(); err != nil {
						Error("TailSplit could not start routing", err.Error())
					}
				}()
			} else {
				go t.a.Stop()
			}
		case <-t.addApp.ClickedCh:
			go t.pickApps()
		case <-t.editSettings.ClickedCh:
			p := config.Path(t.a.Dir)
			if _, err := os.Stat(p); err != nil {
				t.a.UpdateSettings(func(*config.Settings) error { return nil })
			}
			Open(p)
		case <-t.openLog.ClickedCh:
			Open(t.logPath)
		case <-t.logout.ClickedCh:
			go func() {
				if err := t.a.Logout(); err != nil {
					Error("TailSplit", err.Error())
				}
			}()
		case <-t.quit.ClickedCh:
			systray.Quit()
			return
		}
	}
}

func (t *tray) pickApps() {
	paths := PickExecutables()
	if len(paths) == 0 {
		return
	}
	err := t.a.UpdateSettings(func(s *config.Settings) error {
		s.Apps = append(s.Apps, paths...)
		return nil
	})
	if err != nil {
		Error("TailSplit", err.Error())
	}
}

func (t *tray) removeApp(i int) {
	t.a.UpdateSettings(func(s *config.Settings) error {
		if i < len(s.Apps) {
			s.Apps = append(s.Apps[:i], s.Apps[i+1:]...)
		}
		return nil
	})
}

// watchSettings picks up hand edits of settings.json.
func (t *tray) watchSettings() {
	p := config.Path(t.a.Dir)
	var last time.Time
	if fi, err := os.Stat(p); err == nil {
		last = fi.ModTime()
	}
	for range time.Tick(2 * time.Second) {
		fi, err := os.Stat(p)
		if err != nil || !fi.ModTime().After(last) {
			continue
		}
		last = fi.ModTime()
		if err := t.a.Reload(); err != nil {
			Error("TailSplit: settings.json not applied", err.Error())
		}
	}
}

func (t *tray) render() {
	s := t.a.Snapshot()
	ts := s.Tailnet

	switch {
	case ts.AuthURL != "" && ts.State != "Running":
		t.status.SetTitle("Sign-in needed")
		t.signIn.Show()
		systray.SetIcon(iconWaiting)
		t.mu.Lock()
		first := t.lastURL != ts.AuthURL
		t.lastURL = ts.AuthURL
		t.mu.Unlock()
		if first {
			Open(ts.AuthURL) // open the browser once per new sign-in link
		}
	case ts.State == "NeedsMachineAuth":
		t.status.SetTitle("Waiting for a tailnet admin to approve this device")
		t.signIn.Hide()
		systray.SetIcon(iconWaiting)
	case s.State == app.Starting:
		t.status.SetTitle("Starting...")
		t.signIn.Hide()
		systray.SetIcon(iconWaiting)
	case s.State == app.Routing && !ts.ExitOK:
		t.status.SetTitle("Routing, but the exit node is offline")
		t.signIn.Hide()
		systray.SetIcon(iconBroken)
	case s.State == app.Routing:
		t.status.SetTitle(fmt.Sprintf("Routing: %d flows via exit node, %d direct", s.FlowsTailnet, s.FlowsDirect))
		t.signIn.Hide()
		systray.SetIcon(iconOn)
	default:
		title := "Not routing"
		if s.Err != "" {
			title = "Stopped: " + truncate(s.Err, 70)
		} else if ts.State != "" && ts.State != "Running" {
			title = "Tailnet: " + ts.State
		}
		t.status.SetTitle(title)
		t.signIn.Hide()
		systray.SetIcon(iconOff)
	}

	exit := s.Settings.ExitNode
	if ts.ExitNode != "" {
		exit = ts.ExitNode
	}
	if exit == "" {
		t.exitNode.SetTitle("Exit node: not set (Edit settings)")
	} else {
		t.exitNode.SetTitle("Exit node: " + exit)
	}
	if s.State == app.Stopped {
		t.toggle.SetTitle("Start routing")
	} else {
		t.toggle.SetTitle("Stop routing")
	}
	systray.SetTooltip("TailSplit: " + s.State.String())

	for i, it := range t.appItems {
		if i < len(s.Settings.Apps) {
			it.SetTitle(filepath.Base(s.Settings.Apps[i]) + "   " + s.Settings.Apps[i])
			it.Show()
		} else {
			it.Hide()
		}
	}
	t.apps.SetTitle(fmt.Sprintf("Apps using the exit node (%d)", len(s.Settings.Apps)))
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
