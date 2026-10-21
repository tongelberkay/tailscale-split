// TailSplit routes chosen Windows apps through a Tailscale exit node and
// leaves every other app on the normal internet connection.
package main

//go:generate go run ./tools/mkicon
//go:generate go run github.com/akavel/rsrc@v0.10.2 -ico assets/tailsplit.ico -arch amd64 -o rsrc_windows_amd64.syso

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"tailsplit/internal/app"
	"tailsplit/internal/config"
	"tailsplit/internal/logfile"
	"tailsplit/internal/oneshot"
	"tailsplit/internal/proc"
	"tailsplit/internal/tailnet"
	"tailsplit/internal/ui"
)

const usage = `TailSplit: route chosen apps through a Tailscale exit node.

Usage:
  tailsplit <app.exe> [--exit-node <name>] [-- app args]
                               run one app through the exit node, no tray;
                               stops when the app and its child processes exit
  tailsplit                    tray app
  tailsplit run                route in this console until Ctrl+C
  tailsplit launch <app>       start a listed app (name or path) through the proxy;
                               the tray app must be running (good for shortcuts)
  tailsplit mode proxy|tun     proxy (default): only listed apps launched through
                               TailSplit use the exit node; tun: virtual adapter
                               for all traffic (needs administrator rights)
  tailsplit exit-nodes         list the exit nodes you can use (signs in if needed)
  tailsplit exit-node <name>   set the exit node (Tailscale IP, host or MagicDNS name)
  tailsplit add <app.exe>...   send these executables through the exit node
  tailsplit remove <app.exe>   stop sending an executable through the exit node
  tailsplit list               show settings
  tailsplit dir                print the data folder (treat it as a password)
  tailsplit version            print the version

Settings live in %LOCALAPPDATA%\TailSplit\settings.json.
`

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) > 1 {
		ui.AttachParentConsole()
	}
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "tailsplit:", err)
		if len(os.Args) == 1 {
			ui.Error("TailSplit", err.Error())
		}
		os.Exit(1)
	}
}

func run(args []string) error {
	dir, err := config.Dir()
	if err != nil {
		return err
	}
	if len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "version", "-v", "--version":
			fmt.Println("tailsplit", version)
			return nil
		case "-h", "--help", "/?":
			fmt.Print(usage)
			return nil
		}
	}
	if len(args) > 0 && (oneshot.LooksLikeApp(args[0]) || strings.HasPrefix(args[0], "-")) {
		a, err := oneshot.Parse(args)
		if err != nil {
			return fmt.Errorf("%v\n\n%s", err, usage)
		}
		return runApp(dir, a)
	}
	cmd := ""
	if len(args) > 0 {
		cmd = strings.ToLower(args[0])
		args = args[1:]
	}
	switch cmd {
	case "":
		return tray(dir)
	case "run":
		return console(dir)
	case "exit-nodes":
		return listExitNodes(dir)
	case "exit-node":
		if len(args) != 1 {
			return fmt.Errorf("usage: tailsplit exit-node <tailscale-ip-or-name>")
		}
		v, err := config.ValidateExitNode(args[0])
		if err != nil {
			return err
		}
		return edit(dir, func(s *config.Settings) { s.ExitNode = v })
	case "add":
		abs := make([]string, len(args))
		for i, a := range args {
			if abs[i], err = filepath.Abs(a); err != nil {
				return err
			}
			if _, err := os.Stat(abs[i]); err != nil {
				return err
			}
		}
		return edit(dir, func(s *config.Settings) { s.Apps = append(s.Apps, abs...) })
	case "remove":
		return edit(dir, func(s *config.Settings) {
			kept := s.Apps[:0]
			for _, a := range s.Apps {
				drop := false
				for _, r := range args {
					drop = drop || strings.EqualFold(a, r) || strings.EqualFold(filepath.Base(a), r)
				}
				if !drop {
					kept = append(kept, a)
				}
			}
			s.Apps = kept
		})
	case "mode":
		if len(args) != 1 {
			return fmt.Errorf("usage: tailsplit mode proxy|tun")
		}
		m := strings.ToLower(args[0])
		if m != config.ModeProxy && m != config.ModeTUN {
			return fmt.Errorf("mode must be proxy or tun")
		}
		return edit(dir, func(s *config.Settings) { s.Mode = m })
	case "launch":
		if len(args) < 1 {
			return fmt.Errorf("usage: tailsplit launch <app name or path>")
		}
		s, err := config.Load(dir)
		if err != nil {
			return err
		}
		for _, a := range s.Apps {
			if strings.EqualFold(a, args[0]) || strings.EqualFold(filepath.Base(a), args[0]) ||
				strings.EqualFold(strings.TrimSuffix(filepath.Base(a), filepath.Ext(a)), args[0]) {
				return ui.Launch(a, fmt.Sprintf("http://127.0.0.1:%d", s.ProxyPort))
			}
		}
		return fmt.Errorf("%q is not in the app list (tailsplit list)", args[0])
	case "list":
		s, err := config.Load(dir)
		if err != nil {
			return err
		}
		fmt.Printf("mode: %s (proxy port %d)\nexit node: %s\napps:\n", s.Mode, s.ProxyPort, or(s.ExitNode, "(not set)"))
		for _, a := range s.Apps {
			fmt.Println("  " + a)
		}
		return nil
	case "dir":
		fmt.Println(dir)
		return nil
	case "help", "-h", "--help", "/?":
		fmt.Print(usage)
		return nil
	}
	return fmt.Errorf("unknown command %q\n\n%s", cmd, usage)
}

// edit changes settings.json; a running tray instance picks it up.
func edit(dir string, f func(*config.Settings)) error {
	s, err := config.Load(dir)
	if err != nil {
		return err
	}
	f(&s)
	if s.Apps, err = config.NormalizeApps(s.Apps); err != nil {
		return err
	}
	return config.Save(dir, s)
}

func or(a, b string) string {
	if a == "" {
		return b
	}
	return a
}

func prepare(dir string, echo, forceProxy bool) (*app.App, *logfile.Log, func(), error) {
	s, err := config.Load(dir)
	if err != nil {
		return nil, nil, nil, err
	}
	// Only the adapter needs administrator rights. In proxy mode, staying
	// unelevated also means launched apps don't run as administrator.
	if s.Mode == config.ModeTUN && !forceProxy && !ui.IsElevated() {
		return nil, nil, nil, errNeedsAdmin
	}
	ok, release := ui.SingleInstance()
	if !ok {
		return nil, nil, nil, fmt.Errorf("TailSplit is already running (check the tray)")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		release()
		return nil, nil, nil, err
	}
	log, err := logfile.Open(filepath.Join(dir, "tailsplit.log"), echo)
	if err != nil {
		release()
		return nil, nil, nil, err
	}
	a, err := app.New(dir, log.Logf)
	if err != nil {
		log.Close()
		release()
		return nil, nil, nil, err
	}
	if err := a.StartNode(); err != nil {
		log.Close()
		release()
		return nil, nil, nil, err
	}
	return a, log, func() { log.Close(); release() }, nil
}

var errNeedsAdmin = fmt.Errorf("tun mode needs administrator rights to create the network adapter")

func tray(dir string) error {
	a, log, done, err := prepare(dir, false, false)
	if err == errNeedsAdmin {
		return ui.RelaunchElevated(nil)
	}
	if err != nil {
		return err
	}
	defer done()
	ui.Run(a, filepath.Join(dir, "tailsplit.log"))
	log.Logf("exited")
	return nil
}

// resolveApp turns what the user typed into an executable path: a path, a
// program on PATH, a name from the app list, or a running program's name.
func resolveApp(name string, listed []string) (string, error) {
	if filepath.IsAbs(name) || strings.ContainsAny(name, `\/`) {
		p, err := filepath.Abs(name)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(p); err != nil {
			return "", err
		}
		return p, nil
	}
	if p, err := exec.LookPath(name); err == nil {
		return filepath.Abs(p)
	}
	match := func(p string) bool {
		b := filepath.Base(p)
		return strings.EqualFold(b, name) || strings.EqualFold(strings.TrimSuffix(b, filepath.Ext(b)), name)
	}
	for _, p := range listed {
		if match(p) {
			return p, nil
		}
	}
	for _, p := range proc.Running() {
		if match(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("can't find %q: give its full path", name)
}

// runApp is the one-shot mode: sign in if needed, serve the proxy for this
// app only, launch it, and exit once it and its child processes are gone.
func runApp(dir string, args oneshot.Args) error {
	s, err := config.Load(dir)
	if err != nil {
		return err
	}
	exe, err := resolveApp(args.App, s.Apps)
	if err != nil {
		return err
	}
	if args.ExitNode != "" {
		if args.ExitNode, err = config.ValidateExitNode(args.ExitNode); err != nil {
			return err
		}
	}
	if ok, release := ui.SingleInstance(); !ok {
		return fmt.Errorf("the TailSplit tray app is running; use the tray or `tailsplit launch %s` instead", filepath.Base(exe))
	} else {
		release() // prepare takes it again
	}
	if proc.AliveUnder(exe) {
		return fmt.Errorf("%s is already running without the proxy; quit it completely first", filepath.Base(exe))
	}

	a, _, done, err := prepare(dir, true, true)
	if err != nil {
		return err
	}
	defer done()
	defer a.Close()
	err = a.Override(func(s *config.Settings) {
		s.Mode = config.ModeProxy
		s.Apps = []string{exe} // only this app (and its children) may use the proxy
		if args.ExitNode != "" {
			s.ExitNode = args.ExitNode
		}
	})
	if err != nil {
		return err
	}
	var (
		mu    sync.Mutex
		shown string
	)
	a.OnChange = func() {
		u := a.Snapshot().Tailnet.AuthURL
		mu.Lock()
		fresh := u != "" && u != shown
		shown = u
		mu.Unlock()
		if fresh {
			fmt.Fprintln(os.Stderr, "Sign in to Tailscale in your browser:", u)
			ui.Open(u)
		}
	}
	if err := a.Start(); err != nil {
		return err
	}
	if err := ui.Launch(exe, a.ProxyURL(), args.AppArgs...); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "%s is running through %s. TailSplit stops when it exits.\n",
		filepath.Base(exe), a.Snapshot().Settings.ExitNode)

	// Launchers often exit at once and leave the real app running, so wait
	// on the whole family of processes rather than the first one.
	time.Sleep(10 * time.Second)
	for proc.AliveUnder(exe) {
		time.Sleep(2 * time.Second)
	}
	fmt.Fprintf(os.Stderr, "%s exited; stopping.\n", filepath.Base(exe))
	return nil
}

// listExitNodes starts the node just long enough to read the exit nodes it
// may use. It can't run alongside the tray app, which shows them itself.
func listExitNodes(dir string) error {
	if ok, release := ui.SingleInstance(); !ok {
		return fmt.Errorf("the TailSplit tray app is running; its Exit node menu lists them")
	} else {
		release()
	}
	a, _, done, err := prepare(dir, true, true)
	if err != nil {
		return err
	}
	defer done()
	defer a.Close()
	var shown sync.Map
	a.OnChange = func() {
		if u := a.Snapshot().Tailnet.AuthURL; u != "" {
			if _, seen := shown.LoadOrStore(u, true); !seen {
				fmt.Fprintln(os.Stderr, "Sign in to Tailscale in your browser:", u)
				ui.Open(u)
			}
		}
	}
	a.OnChange()
	if err := a.WaitSignedIn(3 * time.Minute); err != nil {
		return fmt.Errorf("not signed in: %w", err)
	}
	// The peer list arrives just after the node comes up.
	var nodes []tailnet.ExitNode
	for i := 0; i < 10 && len(nodes) == 0; i++ {
		if nodes, err = a.ExitNodes(); err != nil {
			return err
		}
		if len(nodes) == 0 {
			time.Sleep(500 * time.Millisecond)
		}
	}
	if len(nodes) == 0 {
		fmt.Println("No exit nodes are available to this device. A device must advertise itself as an exit node, be approved in the admin console, and be allowed by your access rules.")
		return nil
	}
	saved := strings.ToLower(a.Snapshot().Settings.ExitNode)
	for _, n := range nodes {
		mark, state := " ", "online"
		if saved != "" && (strings.ToLower(n.Name) == saved || strings.ToLower(n.DNS) == saved || n.IP == saved) {
			mark = "*"
		}
		if !n.Online {
			state = "offline"
		}
		fmt.Printf("%s %-24s %-16s %s\n", mark, n.Name, n.IP, state)
	}
	if exp := a.Snapshot().Tailnet.KeyExpiry; !exp.IsZero() {
		fmt.Printf("\nThis device's sign-in expires %s.\n", exp.Local().Format("2 Jan 2006 15:04"))
	}
	return nil
}

func console(dir string) error {
	a, _, done, err := prepare(dir, true, false)
	if err != nil {
		return err
	}
	defer done()
	defer a.Close()
	a.OnChange = func() {
		if u := a.Snapshot().Tailnet.AuthURL; u != "" {
			fmt.Fprintln(os.Stderr, "Sign in to Tailscale:", u)
		}
	}
	if err := a.Start(); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Routing. Press Enter or Ctrl+C to stop.")
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	// This is a GUI-subsystem exe attached to the parent console, so read
	// the console directly rather than relying on stdin being wired up.
	go func() {
		if in, err := os.Open("CONIN$"); err == nil {
			in.Read(make([]byte, 1))
			c <- os.Interrupt
		}
	}()
	<-c
	return nil
}
