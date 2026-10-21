# CLAUDE.md

Notes for working on TailSplit with Claude Code. Read `README.md` first for what the app does.

## What this is

A single Windows exe (Go, no cgo) that sends only chosen apps through a Tailscale exit node. It
embeds its own Tailscale node with `tsnet`, so it is a separate device (`tailsplit-windows`) and
never touches the regular Tailscale client.

It is proxy-only: a loopback HTTP CONNECT/HTTP proxy on `127.0.0.1:47123`. Each client
connection is allowed only if the process that owns the socket, or one of its ancestors, is in
the app list. Apps are launched with `*_PROXY` env vars, plus `--proxy-server=` for
Chromium/Electron. No admin rights, no adapter. TUN mode (Wintun + gVisor) was removed because it
broke unselected apps (Edge/Teams) on managed PCs; don't bring it back.

## Commands

```sh
go test ./...                       # all tests run on Linux too
GOOS=windows go vet ./...           # most code is *_windows.go; vet it for Windows
gofmt -l .                          # must print nothing
```

CI (`.github/workflows/ci.yml`) builds amd64 and arm64 on every push to `main` and every PR.
Pushing a `v*` tag runs `.github/workflows/release.yml`, which publishes a GitHub release. Both
use `.github/workflows/build.yml`. Local build with the same tags:

```sh
TAGS=$(cat .github/build-tags.txt)
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
| `internal/policy` | which processes (or their ancestors) may use the proxy |
| `internal/proc` | socket → PID → exe (+ ancestors) via iphlpapi; running-process list |
| `internal/ui` | tray (fyne systray), Win32 dialogs and running-app picker, launcher, icon |
| `internal/config`, `internal/oneshot`, `internal/logfile` | settings, one-shot arg parsing, log with redaction |

## Rules that matter

- **Fail closed.** A selected app's traffic must never fall back to the normal connection.
  `tailnet.Node.Dial` refuses unless tsnet's `UserDialPlan` says the dial goes via Tailscale.
  Keep that guard.
- **Never route or touch unselected apps.** The proxy must keep checking the socket
  owner (and ancestors) on every connection.
- Tailscale log uploads stay off (`logtail.Disable()`). The local log redacts login URLs and keys
  (`internal/logfile`). Don't log secrets.
- The state folder `%LOCALAPPDATA%\TailSplit` holds the node key. Never commit it, and never put
  real tailnet names, IPs or auth keys in tests or docs.
- Keep the binary small: no cgo, keep the `ts_omit_*` tags in `.github/build-tags.txt` in sync with any new
  Tailscale feature you need.
- Windows-only code lives in `*_windows.go` with a stub in `*_other.go` where needed, so
  `go test ./...` keeps working on Linux. Put pure logic (parsing, labels, policy) in
  platform-neutral files with tests.
- The exe is built with `-H windowsgui`. CLI output works because `ui.AttachParentConsole`
  attaches to the parent console; the prompt returns immediately.

## Testing on Windows

CI vets, tests and builds on Windows, but nothing exercises the app itself. Before a release, check by hand on Windows 10/11: tray start and
sign-in, Exit node menu, Add running app, Launch through the tailnet for an Electron app,
`tailsplit app.exe --exit-node x`, and that an unselected browser still works normally.
