# dualnet

Combine several internet connections on one Windows PC — for example your home Wi‑Fi and your phone's USB tethering — **without any remote server**.

The interface is in English, or in French when your browser prefers French.

## Download

Get `dualnet.exe` from the [latest release](https://github.com/NassB/dualnet/releases/latest). It is a single file, nothing to install.

> The executable is not code-signed, so Windows SmartScreen may warn the first time: click **More info → Run anyway**. You can also build it yourself (see [Development](#development)).

## Quick start

Double-click `dualnet.exe`:

1. Windows asks for administrator rights (needed by transparent mode);
2. an icon appears in the notification area and the interface opens in your browser (`http://127.0.0.1:8090`);
3. **transparent mode** is on: all the PC's IPv4 traffic (Steam, games, browser…) is spread over your links.

Tray menu: open the interface, toggle transparent mode, **start with Windows**, quit (networking goes back to normal).

## What dualnet does

| Mode | For | Gain |
|---|---|---|
| **Transparent** (Wintun virtual adapter) | The whole PC, no settings | Connections are spread over the links |
| **Proxy** SOCKS5/HTTP `127.0.0.1:1080` | Apps configured to use the proxy | Same, app by app |
| **Download** (`get` or the interface) | One large file | Link speeds **add up** for that file |

- **Spreads by measured speed**: each new connection goes to the link with the most free capacity (measured capacity ÷ busy connections; idle sockets do not count).
- **Link monitoring**: every 2 s (plugged / unplugged / IP change) plus an internet check every 5 s; a link that stops answering is skipped until it comes back. Ping is shown.
- **Per-link speed cap** (Mb/s), remembered.
- **History**: data per link and per day (30 days shown, one year kept).
- **Windows settings in one click** (administrator): keep Wi‑Fi on with USB tethering, link preferred by Windows, restart Steam.

Data and settings: `%APPDATA%\dualnet` (`history.json`, `settings.json`, `dualnet.log`).

## Command line

```powershell
.\dualnet.exe ui -sticky 0 -transparent     # interface + transparent mode (administrator PowerShell)
.\dualnet.exe ui                            # interface + proxy, no admin rights needed
.\dualnet.exe get https://example.com/big-file.iso
.\dualnet.exe proxy -listen 127.0.0.1:1080 -system-proxy
.\dualnet.exe interfaces
```

`-sticky 10m` (default) keeps a given site on the same link for 10 minutes (useful for sites that watch your IP); `-sticky 0` spreads every connection (best for Steam).

Add `?lang=en` or `?lang=fr` to the interface address to force a language.

## Wi‑Fi drops when the phone is plugged in?

By default Windows disconnects Wi‑Fi as soon as a wired connection (the USB tethering) is present. Use the “Keep Wi‑Fi with USB tethering” button in the interface, or in an administrator PowerShell:

```powershell
New-Item -Path 'HKLM:\SOFTWARE\Policies\Microsoft\Windows\WcmSvc\GroupPolicy' -Force | Out-Null
Set-ItemProperty -Path 'HKLM:\SOFTWARE\Policies\Microsoft\Windows\WcmSvc\GroupPolicy' -Name fMinimizeConnections -Value 0 -Type DWord
```

## Transparent mode: details

- A `dualnet` virtual adapter (Wintun) plus routes `0.0.0.0/1` and `128.0.0.0/1`; your normal routes are left untouched.
- Connections are terminated by tun2socks (gVisor stack) and reopened over a link (TCP and UDP, so DNS and games too).
- **IPv6 is refused**: applications fall back to IPv4. Restart Steam after enabling it (button in the interface): its already-open connections keep their old path.
- When dualnet exits, even if it crashes, the adapter and its routes disappear.
- `wintun.dll` 0.14.1 (signed by WireGuard LLC, license: `internal/transparent/WINTUN-LICENSE.txt`) is embedded in the executable and extracted next to it on first run.

## Limits

- Without a server in between, **a single connection** (one video stream, one online game) cannot use two links at once: the gain comes from apps that open several connections (Steam, downloads, browsing).
- IPv6 is not used in transparent mode.

## Development

```powershell
go test ./...
go build -o dualnet.exe .
go run ./tools/mkicon     # regenerates the icon (assets/) and the .exe resource
```

GitHub Actions builds and tests every push. To publish a release, push a version tag:

```powershell
git tag v0.2.0
git push origin v0.2.0
```

The workflow builds `dualnet.exe` with that version and attaches it to a GitHub release with generated notes.

## License

[MIT](LICENSE). `wintun.dll` keeps its own license (WireGuard LLC).
