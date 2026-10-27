# TailSplit

Send only the apps you choose through a Tailscale exit node. Everything
else stays on your normal internet connection.

> **Status: v0.1, early.** Proxy mode and the one-shot command have been
> tried on one Windows 11 PC. TailSplit is not affiliated with Tailscale Inc.

It ships as one `tailsplit.exe` (about 20 MB). It needs no network adapter,
no route changes and no administrator rights.

## Build

Builds run in GitHub Actions ([build.yml](.github/workflows/build.yml)) on every push to `main`
and every pull request; the exe for each architecture is attached to the run. To build locally
you need Go 1.27.3 or newer (Tailscale v1.104 requires it); an older Go fetches the right
toolchain by itself. The build uses no cgo.

```powershell
go test ./...
$env:CGO_ENABLED = '0'
go build -trimpath -tags (Get-Content .githubuild-tags.txt -Raw).Trim() -ldflags '-s -w -H windowsgui' -o dist	ailsplit.exe .
```

## Release

Push a version tag and GitHub Actions ([release.yml](.github/workflows/release.yml)) tests, builds
amd64 and arm64, and publishes a release with one zip per architecture (`tailsplit.exe` and
this README) plus `SHA256SUMS.txt`. A tag with a suffix such as
`v0.2.0-rc.1` becomes a pre-release.

```powershell
git tag v0.2.0
git push origin v0.2.0
```

`tailsplit version` prints the version a build was stamped with.

## Use

TailSplit runs a proxy on `127.0.0.1:47123` that exits through the tailnet.
Only the apps you list may use it: each connection to the proxy is checked
against the process that owns it, or that process's parent. There is no
network adapter, no route change and no administrator prompt, so Edge,
Teams and everything else keep their normal network.

1. Run `tailsplit.exe`. A tray icon appears, and the first time your
   browser opens the Tailscale sign-in page. TailSplit is its own device
   in your tailnet (`tailsplit-windows`), separate from the regular
   Tailscale client.
2. Pick the exit node from tray › **Exit node**, which lists every exit
   node this device may use and whether it's online. From a terminal,
   `tailsplit exit-nodes` lists them and `tailsplit exit-node <name>` sets one.
3. Add apps with tray › **Add running app...** (search by name or path,
   multi-select) or **Add app from file...**.
4. **Quit the app completely**, then use tray › *app* › **Launch through
   the tailnet**, or `tailsplit launch codex` from a shortcut. The app
   starts with `HTTPS_PROXY`/`HTTP_PROXY`/`ALL_PROXY` set, plus
   `--proxy-server` for Chromium and Electron apps. Its child processes
   inherit the settings.

**One command, no tray:**

```powershell
tailsplit.exe codex.exe --exit-node home-server
tailsplit.exe "C:\Path\App.exe" -e 100.64.0.7 -- --app-arg
```

This signs in if needed, lets only that app (and the processes it starts)
use the proxy, launches it, and exits once the app and its children have
closed. The app can be a full path, a program on `PATH`, a name from your
app list, or the name of a running program. Without `--exit-node`, the
saved exit node is used. It doesn't save anything. It can't run while the
tray app is running; use the tray's Launch item then.

This only works for apps that respect proxy settings, which covers most
command-line tools and Electron apps. Microsoft Store apps can only be
started this way if they have an app execution alias.

**Sign-in expiry.** Like any Tailscale device, TailSplit's sign-in expires
(180 days by default). The tray shows the date. When it passes, routing
stops sending traffic (it doesn't fall back to your normal connection), the
icon turns amber and your browser opens the sign-in page. After you sign
in, TailSplit selects the exit node again by itself. To skip this, disable
key expiry for `tailsplit-windows` in the Tailscale admin console.

Tray icon colours: grey means off, amber means starting or waiting for
sign-in, the full-colour icon means on, red means on but the exit node is offline.

Settings, the log and the node's keys live in `%LOCALAPPDATA%\TailSplit`.
Treat that folder like a password. Editing `settings.json` by hand is
picked up within about 2 seconds.

## Known limits

* Apps that ignore proxy settings can't be routed.
* DNS stays with Windows. Selected apps' lookups use your normal resolver,
  not the exit node's.
* There is no kill switch when routing is **stopped**: apps then use the
  normal network.
* An app is matched by its exact `.exe` path, or by any ancestor's path
  (up to 8 levels), so helpers it starts, such as `msedgewebview2.exe`
  under Teams or `git` run from an app's terminal, follow it. Store apps
  are matched by package, so they keep working after Store updates.
* The tray lists the first 24 apps. `settings.json` can hold more.
* Uploads of Tailscale client logs to Tailscale are turned off. Local logs go
  to `tailsplit.log`, with sign-in links and keys redacted.

## Layout

| Path | What |
|---|---|
| `internal/policy` | which processes may use the proxy |
| `internal/proc` | socket → PID → .exe (+ parents) lookup via iphlpapi, running-process list |
| `internal/proxy` | loopback HTTP CONNECT proxy that only selected processes may use |
| `internal/tailnet` | tsnet node, sign-in, exit node selection, leak guard |
| `internal/app` | start/stop orchestration |
| `internal/ui` | tray, file picker, running-app picker, launcher, icon |

TailSplit itself is MIT-licensed (see `LICENSE`). Tailscale is BSD-3-Clause, wireguard-go is MIT, and gVisor and
fyne systray are Apache-2.0, so there are no GPL obligations.
