# TailSplit

Send only the apps you choose through a Tailscale exit node. Everything
else stays on your normal internet connection.

This is a Go rewrite of the earlier Python and sing-box prototype. It
ships as one `tailsplit.exe` (about 20 MB) plus the signed `wintun.dll`
(about 0.4 MB). The old build needed `sing-box.exe` (82 MB) and
`libcronet.dll` (9.5 MB), plus a bundled Python runtime.

## Build

```powershell
.\build.ps1          # runs the tests, builds dist\tailsplit.exe, fetches wintun.dll
```

You need Go 1.27.3 or newer (Tailscale v1.104 requires it). An older Go fetches the right toolchain
by itself. The build uses no cgo, no Python and no installer.

## Use

1. Run `dist\tailsplit.exe` and accept the administrator prompt. Creating
   the network adapter needs it. A tray icon appears.
2. The first time, your browser opens the Tailscale sign-in page. TailSplit
   is its own device in your tailnet (`tailsplit-windows`) and does not
   touch the regular Tailscale client.
3. Set the exit node with tray › **Edit settings...**, or with
   `tailsplit exit-node <tailscale-ip-or-name>`.
4. Add apps with tray › **Add apps...** (multi-select works), or with
   `tailsplit add "C:\Path\App.exe"`. To remove one, click it under
   **Apps using the exit node**.
5. Click **Start routing**. Restart the apps you chose so that they open
   new connections.

Tray icon colours: grey means not routing, amber means starting or waiting
for sign-in, green means routing, red means routing but the exit node is offline.

Settings, the log and the node's keys live in `%LOCALAPPDATA%\TailSplit`.
Treat that folder like a password. Editing `settings.json` by hand is
picked up within about 2 seconds, and app list changes apply to new
connections without a restart.

## How it works

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

* Unselected apps' traffic still passes through the userspace stack before
  going out directly. A later version can replace this steering layer with
  a WinDivert-based one that captures only the selected apps.
* DNS stays with Windows. Selected apps' lookups use your normal resolver,
  not the exit node's.
* ICMP is not forwarded, so `ping` to internet hosts fails while routing is
  on (LAN pings still work).
* There is no kill switch when routing is **stopped**: apps then use the
  normal network.
* An app is matched by its exact `.exe` path. Helper processes such as
  `codex.exe` next to `ChatGPT.exe` need adding separately, and Store app
  updates can change paths.
* The tray lists the first 24 apps. `settings.json` can hold more.
* Uploads of Tailscale client logs to Tailscale are turned off. Local logs go
  to `tailsplit.log`, with sign-in links and keys redacted.

## Layout

| Path | What |
|---|---|
| `internal/engine` | packet pump, gVisor stack, TCP/UDP forwarding (tested end to end over a fake TUN) |
| `internal/policy` | per-flow decision: tailnet or direct |
| `internal/proc` | socket → PID → .exe lookup via iphlpapi |
| `internal/tailnet` | tsnet node, sign-in, exit node selection, leak guard |
| `internal/winnet` | Wintun adapter, addresses/routes, NIC-pinned direct dialer |
| `internal/app` | start/stop orchestration |
| `internal/ui` | tray, file picker, elevation, icon |

Licences: Tailscale is BSD-3-Clause, wireguard-go is MIT, and gVisor and
fyne systray are Apache-2.0, so there are no GPL obligations, unlike sing-box.
`wintun.dll` comes under the Wintun prebuilt-binaries licence. Read it in
the downloaded zip before redistributing.
