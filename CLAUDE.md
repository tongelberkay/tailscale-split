# CLAUDE.md

Notes for working on TailSplit with Claude Code. Read `README.md` first for what the app does.

## What this is

A single Windows exe (Go, no cgo) that sends only chosen apps through a Tailscale exit node. It
embeds its own Tailscale node with `tsnet`, so it is a separate device (`tailsplit-windows`) and
never touches the regular Tailscale client.

- **Proxy mode (default):** a loopback HTTP CONNECT/HTTP proxy on `127.0.0.1:47123`. Each client
  connection is allowed only if the process that owns the socket, or one of its ancestors, is in
  the app list. Apps are launched with `*_PROXY` env vars, plus `--proxy-server=` for
  Chromium/Electron. No admin rights, no adapter.
- **TUN mode (`mode: tun`):** Wintun adapter with `0.0.0.0/1` + `128.0.0.0/1` routes, gVisor
  netstack, per-flow owner lookup. Needs admin and `wintun.dll`. It is known to break unselected
  apps (Edge/Teams) on managed PCs, which is why proxy mode is the default. Don't make it the default.

## Commands

```sh
go test ./...                       # all tests run on Linux too
GOOS=windows go vet ./...           # most code is *_windows.go; vet it for Windows
gofmt -l .                          # must print nothing
```

Release build on Windows: `.\build.ps1` (set `TAILSPLIT_VERSION=v0.x.y` to stamp the version).
Cross-build from Linux/macOS with the same tags:

```sh
TAGS=$(grep -o "ts_omit_[a-z0-9]*" build.ps1 | paste -sd, -)
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -tags "$TAGS" \
  -ldflags "-s -w -H windowsgui -X main.version=dev" -o tailsplit.exe .
```

After changing the icon drawing in `internal/ui/icon.go`, run `go generate` to rewrite
`assets/tailsplit.ico`, `assets/tailsplit.png` and `rsrc_windows_amd64.syso` (the exe icon).

## Layout

| Path | What |
|---|---|
| `main_windows.go` | CLI dispatch, one-shot mode (`tailsplit app.exe --exit-node x`), tray start |
| `internal/app` | start/stop orchestration, proxy server wiring, settings apply |
| `internal/tailnet` | tsnet node: sign-in, exit node list/select, leak guard in `Dial` |
| `internal/proxy` | the loopback proxy (tested) |
| `internal/policy` | per-flow decision: tailnet, direct, or refuse |
| `internal/proc` | socket → PID → exe (+ ancestors) via iphlpapi; running-process list |
| `internal/engine`, `internal/winnet` | TUN mode only: gVisor stack, Wintun, NIC-pinned direct dialer |
| `internal/ui` | tray (fyne systray), Win32 dialogs and running-app picker, launcher, icon |
| `internal/config`, `internal/oneshot`, `internal/logfile` | settings, one-shot arg parsing, log with redaction |

## Rules that matter

- **Fail closed.** A selected app's traffic must never fall back to the normal connection.
  `tailnet.Node.Dial` refuses unless tsnet's `UserDialPlan` says the dial goes via Tailscale.
  Keep that guard.
- **Never route or touch unselected apps** in proxy mode. The proxy must keep checking the socket
  owner (and ancestors) on every connection.
- Tailscale log uploads stay off (`logtail.Disable()`). The local log redacts login URLs and keys
  (`internal/logfile`). Don't log secrets.
- The state folder `%LOCALAPPDATA%\TailSplit` holds the node key. Never commit it, and never put
  real tailnet names, IPs or auth keys in tests or docs.
- Keep the binary small: no cgo, keep the `ts_omit_*` tags in `build.ps1` in sync with any new
  Tailscale feature you need.
- Windows-only code lives in `*_windows.go` with a stub in `*_other.go` where needed, so
  `go test ./...` keeps working on Linux. Put pure logic (parsing, labels, policy) in
  platform-neutral files with tests.
- The exe is built with `-H windowsgui`. CLI output works because `ui.AttachParentConsole`
  attaches to the parent console; the prompt returns immediately.

## Testing on Windows

Nothing in CI runs on Windows. Before a release, check by hand on Windows 10/11: tray start and
sign-in, Exit node menu, Add running app, Launch through the tailnet for an Electron app,
`tailsplit app.exe --exit-node x`, and that an unselected browser still works normally.
