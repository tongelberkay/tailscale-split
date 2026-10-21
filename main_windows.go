// TailSplit routes chosen Windows apps through a Tailscale exit node and
// leaves every other app on the normal internet connection.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"tailsplit/internal/app"
	"tailsplit/internal/config"
	"tailsplit/internal/logfile"
	"tailsplit/internal/ui"
)

const usage = `TailSplit: route chosen apps through a Tailscale exit node.

Usage:
  tailsplit                    tray app
  tailsplit run                route in this console until Ctrl+C
  tailsplit launch <app>       start a listed app (name or path) through the proxy;
                               the tray app must be running (good for shortcuts)
  tailsplit mode proxy|tun     proxy (default): only listed apps launched through
                               TailSplit use the exit node; tun: virtual adapter
                               for all traffic (needs administrator rights)
  tailsplit exit-node <name>   set the exit node (Tailscale IP, host or MagicDNS name)
  tailsplit add <app.exe>...   send these executables through the exit node
  tailsplit remove <app.exe>   stop sending an executable through the exit node
  tailsplit list               show settings
  tailsplit dir                print the data folder (treat it as a password)

Settings live in %LOCALAPPDATA%\TailSplit\settings.json.
`

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

func prepare(dir string, echo bool) (*app.App, *logfile.Log, func(), error) {
	s, err := config.Load(dir)
	if err != nil {
		return nil, nil, nil, err
	}
	// Only the adapter needs administrator rights. In proxy mode, staying
	// unelevated also means launched apps don't run as administrator.
	if s.Mode == config.ModeTUN && !ui.IsElevated() {
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
	a, log, done, err := prepare(dir, false)
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

func console(dir string) error {
	a, _, done, err := prepare(dir, true)
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
