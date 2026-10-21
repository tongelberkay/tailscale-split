package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Launch starts exe with its traffic pointed at the proxy. Command-line
// tools and most runtimes read the *_PROXY variables; Chromium and Electron
// apps ignore them on Windows and need --proxy-server instead. Child
// processes inherit both.
func Launch(exe, proxyURL string) error {
	var args []string
	if isChromium(exe) {
		args = append(args, "--proxy-server="+proxyURL)
	}
	env := append(os.Environ(),
		"HTTPS_PROXY="+proxyURL, "HTTP_PROXY="+proxyURL, "ALL_PROXY="+proxyURL,
		"NO_PROXY=localhost,127.0.0.1,::1")
	start := func(path string) error {
		cmd := exec.Command(path, args...)
		cmd.Env = env
		cmd.Dir = filepath.Dir(exe)
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	}
	err := start(exe)
	if err == nil {
		return nil
	}
	// Store apps under WindowsApps cannot be started by their real path, but
	// many register an execution alias that can, and that keeps our
	// environment and arguments.
	if strings.Contains(strings.ToLower(exe), `\windowsapps\`) {
		alias := filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "WindowsApps", filepath.Base(exe))
		if _, serr := os.Stat(alias); serr == nil {
			if aerr := start(alias); aerr == nil {
				return nil
			}
		}
		return fmt.Errorf("%s is a Microsoft Store app and Windows does not allow starting it with proxy settings (no app alias found): %w", filepath.Base(exe), err)
	}
	return err
}

// isChromium spots Chromium/Electron apps by the files they ship with.
func isChromium(exe string) bool {
	dir := filepath.Dir(exe)
	for _, f := range []string{"resources.pak", "chrome_100_percent.pak", `resources\app.asar`, "v8_context_snapshot.bin"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return true
		}
	}
	return false
}
