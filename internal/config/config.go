// Package config loads and saves TailSplit's settings file.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Settings is what the user chooses. Everything else is derived at runtime.
type Settings struct {
	// ExitNode is a Tailscale IP, or a peer's host name or MagicDNS name.
	ExitNode string `json:"exit_node"`
	// Apps are absolute paths of executables whose traffic uses the exit node.
	Apps []string `json:"apps"`
	// Hostname is the name this device gets in the tailnet.
	Hostname string `json:"hostname,omitempty"`
	// ProxyPort is the loopback port of the proxy that only the selected apps
	// may use.
	ProxyPort int `json:"proxy_port,omitempty"`
}

const (
	DefaultHostname  = "tailsplit-windows"
	DefaultProxyPort = 47123
)

// Normalized fills in defaults and rejects out-of-range values.
func (s Settings) Normalized() (Settings, error) {
	if s.Hostname == "" {
		s.Hostname = DefaultHostname
	}
	if s.ProxyPort == 0 {
		s.ProxyPort = DefaultProxyPort
	}
	if s.ProxyPort < 1024 || s.ProxyPort > 65535 {
		return s, fmt.Errorf("proxy_port must be between 1024 and 65535, not %d", s.ProxyPort)
	}
	return s, nil
}

// Dir is where settings, logs and the tailnet node state live. Treat it as
// credentials: tailscaled.state holds the node's private keys.
func Dir() (string, error) {
	if d := os.Getenv("TAILSPLIT_DIR"); d != "" {
		return d, nil
	}
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		var err error
		if base, err = os.UserConfigDir(); err != nil {
			return "", err
		}
	}
	return filepath.Join(base, "TailSplit"), nil
}

func Path(dir string) string { return filepath.Join(dir, "settings.json") }

// Load reads settings. A missing file gives empty settings, not an error.
func Load(dir string) (Settings, error) {
	var s Settings
	data, err := os.ReadFile(Path(dir))
	if errors.Is(err, os.ErrNotExist) {
		return s.Normalized()
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("%s: %w", Path(dir), err)
	}
	apps, err := NormalizeApps(s.Apps)
	if err != nil {
		return s, err
	}
	s.Apps = apps
	return s.Normalized()
}

// Save writes settings atomically.
func Save(dir string, s Settings) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := Path(dir) + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, Path(dir))
}

// NormalizeApps checks that every entry is an absolute Windows .exe path and
// drops case-insensitive duplicates, keeping the first spelling.
func NormalizeApps(paths []string) ([]string, error) {
	out := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, p := range paths {
		p = strings.TrimSpace(strings.Trim(strings.TrimSpace(p), `"`))
		p = strings.ReplaceAll(p, "/", `\`)
		if !isAbsWindows(p) || !strings.EqualFold(windowsExt(p), ".exe") {
			return nil, fmt.Errorf("not an absolute path to an .exe: %q", p)
		}
		key := strings.ToLower(p)
		if !seen[key] {
			seen[key] = true
			out = append(out, p)
		}
	}
	return out, nil
}

// isAbsWindows is done by hand so that tests behave the same on any OS.
func isAbsWindows(p string) bool {
	if len(p) >= 3 && p[1] == ':' && p[2] == '\\' {
		c := p[0] | 0x20
		return c >= 'a' && c <= 'z'
	}
	return strings.HasPrefix(p, `\\`) // UNC
}

func windowsExt(p string) string {
	i := strings.LastIndexAny(p, `.\`)
	if i < 0 || p[i] != '.' {
		return ""
	}
	return p[i:]
}

var hostnameRE = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

// ValidateExitNode accepts a Tailscale IP or a host/MagicDNS name, not a URL.
func ValidateExitNode(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", errors.New("no exit node set; run: tailsplit exit-node <tailscale-ip-or-name>")
	}
	if _, err := netip.ParseAddr(v); err == nil {
		return v, nil
	}
	if !hostnameRE.MatchString(strings.TrimSuffix(v, ".")) {
		return "", fmt.Errorf("exit node must be a Tailscale IP or name, without a URL or port: %q", v)
	}
	return strings.TrimSuffix(v, "."), nil
}
