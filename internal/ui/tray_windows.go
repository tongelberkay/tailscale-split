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
	"tailsplit/internal/proc"
	"tailsplit/internal/tailnet"
)

var (
	iconOff     = Icon(0x8a, 0x93, 0xa0)                   // grey: not routing
	iconWaiting = Icon(0xf2, 0xa9, 0x3b)                   // amber: starting or needs sign-in
	iconOn      = Icon(AppColor.R, AppColor.G, AppColor.B) // full colour: routing
	iconBroken  = Icon(0xe0, 0x4f, 0x4f)                   // red: routing but exit node offline
)

const (
	maxAppItems  = 24
	maxExitNodes = 16
)

// appItem is one app in the menu, with its own actions.
type appItem struct {
	top, launch, remove *systray.MenuItem
}

type tray struct {
	a       *app.App
	logPath string

	status, signIn, toggle, exitNode *systray.MenuItem
	expiry                           *systray.MenuItem
	nodeItems                        []*systray.MenuItem
	noNodes                          *systray.MenuItem
	apps                             *systray.MenuItem
	appItems                         []appItem
	addRunning, addApp               *systray.MenuItem
	editSettings, openLog            *systray.MenuItem
	logout, quit                     *systray.MenuItem

	mu      sync.Mutex
	lastURL string
	nodes   []tailnet.ExitNode // what nodeItems show, refreshed every few seconds
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
	t.exitNode = systray.AddMenuItem("", "Exit nodes this device may use")
	for range maxExitNodes {
		it := t.exitNode.AddSubMenuItemCheckbox("", "Send the listed apps through this exit node", false)
		it.Hide()
		t.nodeItems = append(t.nodeItems, it)
	}
	t.noNodes = t.exitNode.AddSubMenuItem("No exit nodes available yet", "")
	t.noNodes.Disable()
	t.expiry = systray.AddMenuItem("", "")
	t.expiry.Disable()
	t.expiry.Hide()
	t.signIn = systray.AddMenuItem("Sign in to Tailscale...", "Open the sign-in page in your browser")
	t.signIn.Hide()
	systray.AddSeparator()
	t.toggle = systray.AddMenuItem("Start routing", "")
	t.apps = systray.AddMenuItem("Apps using the exit node", "")
	for range maxAppItems {
		top := t.apps.AddSubMenuItem("", "")
		it := appItem{
			top:    top,
			launch: top.AddSubMenuItem("Launch through the tailnet", "Start this app with the TailSplit proxy"),
			remove: top.AddSubMenuItem("Remove", "Stop sending this app through the exit node"),
		}
		top.Hide()
		t.appItems = append(t.appItems, it)
	}
	t.addRunning = systray.AddMenuItem("Add running app...", "Pick from running programs, searchable by name and path")
	t.addApp = systray.AddMenuItem("Add app from file...", "Choose .exe files whose traffic should use the exit node")
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
		for {
			t.refreshNodes()
			t.render() // refresh flow counters
			time.Sleep(3 * time.Second)
		}
	}()
}

func (t *tray) loop() {
	for i, it := range t.nodeItems {
		go func() {
			for range it.ClickedCh {
				t.mu.Lock()
				var name string
				if i < len(t.nodes) {
					name = t.nodes[i].Name
				}
				t.mu.Unlock()
				if name != "" {
					go t.chooseExitNode(name)
				}
			}
		}()
	}
	for i, it := range t.appItems {
		go func() {
			for {
				select {
				case <-it.remove.ClickedCh:
					t.removeApp(i)
				case <-it.launch.ClickedCh:
					go t.launch(i)
				}
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
			go t.addApps(PickExecutables())
		case <-t.addRunning.ClickedCh:
			go func() { t.addApps(PickRunning(t.a.Snapshot().Settings.Apps)) }()
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

func (t *tray) addApps(paths []string) {
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

// launch starts app i through the proxy, starting routing first if needed.
func (t *tray) launch(i int) {
	s := t.a.Snapshot()
	if i >= len(s.Settings.Apps) {
		return
	}
	exe := s.Settings.Apps[i]
	for _, r := range proc.Running() {
		if strings.EqualFold(r, exe) {
			// A second start would only hand off to the running copy, which
			// has no proxy settings.
			Info("TailSplit", filepath.Base(exe)+" is already running without the proxy.\n\n"+
				"Quit it completely (including from the tray), then choose Launch again.")
			return
		}
	}
	if s.State == app.Stopped {
		if err := t.a.Start(); err != nil {
			Error("TailSplit could not start", err.Error())
			return
		}
	}
	if err := Launch(exe, t.a.ProxyURL()); err != nil {
		Error("TailSplit could not launch "+filepath.Base(exe), err.Error())
	}
}

// refreshNodes re-reads the exit nodes the tailnet offers this device.
func (t *tray) refreshNodes() {
	nodes, err := t.a.ExitNodes()
	if err != nil {
		return // not signed in yet; keep the last list
	}
	t.mu.Lock()
	t.nodes = nodes
	t.mu.Unlock()
}

func (t *tray) chooseExitNode(name string) {
	err := t.a.UpdateSettings(func(s *config.Settings) error {
		s.ExitNode = name
		return nil
	})
	if err != nil {
		Error("TailSplit could not switch exit node", err.Error())
	}
	t.refreshNodes()
	t.render()
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
	case s.State == app.Routing && s.ProxyAddr != "":
		t.status.SetTitle(fmt.Sprintf("Proxy on %s: %d connections via exit node, %d refused", s.ProxyAddr, s.FlowsTailnet, s.FlowsDirect))
		t.signIn.Hide()
		systray.SetIcon(iconOn)
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
		t.exitNode.SetTitle("Exit node: choose one")
	} else {
		t.exitNode.SetTitle("Exit node: " + exit)
	}
	t.mu.Lock()
	nodes := t.nodes
	t.mu.Unlock()
	want := strings.ToLower(s.Settings.ExitNode)
	for i, it := range t.nodeItems {
		if i >= len(nodes) {
			it.Hide()
			continue
		}
		n := nodes[i]
		title := n.Name + "   " + n.IP
		if !n.Online {
			title += "   (offline)"
		}
		it.SetTitle(title)
		if strings.ToLower(n.Name) == want || strings.ToLower(n.DNS) == want || n.IP == want {
			it.Check()
		} else {
			it.Uncheck()
		}
		it.Show()
	}
	if len(nodes) == 0 {
		t.noNodes.Show()
	} else {
		t.noNodes.Hide()
	}
	if exp := ts.KeyExpiry; !exp.IsZero() && ts.State == "Running" {
		days := int(time.Until(exp).Hours() / 24)
		t.expiry.SetTitle(fmt.Sprintf("Sign-in expires %s (in %d days)", exp.Local().Format("2 Jan 2006"), days))
		t.expiry.SetTooltip("You'll be asked to sign in again then. To avoid that, disable key expiry for this device in the Tailscale admin console.")
		t.expiry.Show()
	} else {
		t.expiry.Hide()
	}
	if s.State == app.Stopped {
		t.toggle.SetTitle("Start routing")
	} else {
		t.toggle.SetTitle("Stop routing")
	}
	systray.SetTooltip("TailSplit: " + s.State.String())

	proxyMode := s.Settings.Mode == config.ModeProxy
	labels := Labels(s.Settings.Apps)
	for i, it := range t.appItems {
		if i < len(s.Settings.Apps) {
			it.top.SetTitle(labels[i])
			it.top.SetTooltip(s.Settings.Apps[i])
			it.top.Show()
			if proxyMode {
				it.launch.Show()
			} else {
				it.launch.Hide()
			}
		} else {
			it.top.Hide()
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
