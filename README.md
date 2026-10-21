# TailSplit

Send only the apps you choose through a Tailscale exit node. Everything
else stays on your normal internet connection.

This is a Go rewrite of the earlier Python and sing-box prototype. It
ships as one `tailsplit.exe` (about 20 MB). TUN mode also needs the signed
`wintun.dll` (about 0.4 MB), which proxy mode doesn't. The old build needed `sing-box.exe` (82 MB) and
`libcronet.dll` (9.5 MB), plus a bundled Python runtime.

## Build

```powershell
.\build.ps1          # runs the tests, builds dist\tailsplit.exe, fetches wintun.dll (TUN mode only)
```

You need Go 1.27.3 or newer (Tailscale v1.104 requires it). An older Go fetches the right toolchain
by itself. The build uses no cgo, no Python and no installer.

## Use

There are two modes. **Proxy mode** is the default and the recommended one.

### Proxy mode (default): nothing else is touched

TailSplit runs a proxy on `127.0.0.1:47123` that exits through the tailnet.
Only the apps you list may use it: each connection to the proxy is checked
against the process that owns it, or that process's parent. There is no
network adapter, no route change and no administrator prompt, so Edge,
Teams and everything else keep their normal network.

1. Run `tailsplit.exe`. A tray icon appears, and the first time your
   browser opens the Tailscale sign-in page. TailSplit is its own device
   in your tailnet (`tailsplit-windows`), separate from the regular
   Tailscale client.
2. Set the exit node: `tailsplit exit-node <tailscale-ip-or-name>`, or use
   tray › **Edit settings...**.
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

### TUN mode: for apps that ignore proxies

`tailsplit mode tun` uses a virtual adapter that every app's traffic passes
through, and matches flows to processes. It needs administrator rights.
**It can break other apps:** on managed PCs, Edge and Teams showed
`ERR_NETWORK_ACCESS_DENIED` and failed to send messages while the old
prototype's adapter was up, even though they weren't selected.

### Both modes

Tray icon colours: grey means off, amber means starting or waiting for
sign-in, green means on, red means on but the exit node is offline.

Settings, the log and the node's keys live in `%LOCALAPPDATA%\TailSplit`.
Treat that folder like a password. Editing `settings.json` by hand is
picked up within about 2 seconds.

## How TUN mode works

```
app ──► Windows routing ──► TailSplit adapter (0.0.0.0/1 + 128.0.0.0/1)
                                   │
                         userspace TCP/IP stack (gVisor)
                                   │  owner of this socket? (GetExtendedTcpTable/UdpTable)
                 ┌─────────────────┴──────────────────┐
         selected .exe                           anything else
   embedded Tailscale node (tsnet)       socket pinned to the physical NIC
        → exit node                          (IP_UNICAST_IF) → internet
```

* The two `/1` routes beat the physical default route because they are
  more specific, and the physical `0.0.0.0/0` stays in place. Tailscale's
  own sockets and TailSplit's direct sockets bind to the interface that
  owns that real default route, so nothing loops back into the adapter.
* LAN, loopback, link-local and multicast destinations always go direct.
  Tailnet addresses (`100.64.0.0/10`, `fd7a:115c:a1e0::/48`) from selected
  apps go through TailSplit's own node, so selected apps can reach tailnet
  devices even when the regular Tailscale client is off.
* **Fails closed for selected apps.** If the exit node is not active, a
  selected app's connection is refused rather than sent out directly.
* IPv6 is routed only when Windows already has native IPv6.
* Wintun removes the adapter, and with it the routes, when the process exits.

## Known limits

* TUN mode: unselected apps' traffic still passes through the userspace
  stack, which is why proxy mode is the default.
* DNS stays with Windows. Selected apps' lookups use your normal resolver,
  not the exit node's.
* TUN mode: ICMP is not forwarded, so `ping` to internet hosts fails while routing is
  on (LAN pings still work).
* There is no kill switch when routing is **stopped**: apps then use the
  normal network.
* An app is matched by its exact `.exe` path, or by its parent's or
  grandparent's path (up to 4 levels), so helpers it starts, such as
  `msedgewebview2.exe` under Teams, follow it. Store app updates can change paths.
* The tray lists the first 24 apps. `settings.json` can hold more.
* Uploads of Tailscale client logs to Tailscale are turned off. Local logs go
  to `tailsplit.log`, with sign-in links and keys redacted.

## Layout

| Path | What |
|---|---|
| `internal/engine` | packet pump, gVisor stack, TCP/UDP forwarding (tested end to end over a fake TUN) |
| `internal/policy` | per-flow decision: tailnet or direct |
| `internal/proc` | socket → PID → .exe (+ parents) lookup via iphlpapi, running-process list |
| `internal/proxy` | loopback HTTP CONNECT proxy that only selected processes may use |
| `internal/tailnet` | tsnet node, sign-in, exit node selection, leak guard |
| `internal/winnet` | Wintun adapter, addresses/routes, NIC-pinned direct dialer |
| `internal/app` | start/stop orchestration |
| `internal/ui` | tray, file picker, running-app picker, launcher, elevation, icon |

Licences: Tailscale is BSD-3-Clause, wireguard-go is MIT, and gVisor and
fyne systray are Apache-2.0, so there are no GPL obligations, unlike sing-box.
`wintun.dll` comes under the Wintun prebuilt-binaries licence. Read it in
the downloaded zip before redistributing.
