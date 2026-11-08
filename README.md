# dualnet

Combine several internet connections on one Windows PC — for example your home Wi‑Fi and your phone's USB tethering — **without any remote server**.

The interface follows your language: English, French, Spanish or German (English otherwise). The Spanish and German texts were written without a native-speaker review: corrections are welcome.

## Download

Get `dualnet.exe` from the [latest release](https://github.com/NassB/dualnet/releases/latest). It is a single file, nothing to install.

> The executable is not code-signed, so Windows SmartScreen may warn the first time: click **More info → Run anyway**. You can also build it yourself (see [Development](#development)).

## Quick start

Double-click `dualnet.exe`:

1. Windows asks for administrator rights (needed by transparent mode);
2. **dualnet's window opens** (its own app window, no browser) and an icon appears in the notification area;
3. press the big **power button** to turn dualnet on or off. When on, all the PC's IPv4 traffic (Steam, games, browser…) is spread over your links; when off, Windows uses its normal connection. Your choice is remembered.

Closing the window keeps dualnet running in the notification area; open it again from the icon. **Quit** in the icon's menu stops dualnet and restores normal networking.

The window uses Microsoft Edge WebView2, built into Windows 11 and most up-to-date Windows 10 PCs. Without it, the interface opens in your browser instead.

The interface has four pages:

- **Dashboard**: on/off switch, combined speed, one card per link (speed, ping, share, this month's data, speed cap, monthly budget), the **applications** using the network, live chart;
- **Downloads**: download one file over all links at once;
- **History**: data per link and per day;
- **Settings**: transparent mode, Windows proxy, keep Wi‑Fi with USB tethering, link preferred by Windows, restart Steam, application rules, start with Windows, notifications, update check, diagnostics.

## What dualnet does

| Mode | For | Gain |
|---|---|---|
| **Transparent** (Wintun virtual adapter) | The whole PC, no settings | Connections are spread over the links |
| **Proxy** SOCKS5/HTTP `127.0.0.1:1080` | Apps configured to use the proxy | Same, app by app |
| **Download** (`get` or the interface) | One large file | Link speeds **add up** for that file |

- **Spreads by measured speed**: each new connection goes to the link with the most free capacity (measured capacity ÷ busy connections; idle sockets do not count).
- **Link monitoring**: every 2 s (plugged / unplugged / IP change) plus an internet check every 5 s; a link that stops answering is skipped until it comes back. Ping is shown.
- **Smarter choice**: links whose score is within 10 % are a tie, broken by the lowest ping; DNS lookups (port 53/853) always use the lowest-ping link.
- **Per-link speed cap** (Mb/s), remembered.
- **Monthly data budget per link** (GB): when a link has used its budget, it gets no new connections until next month (warning at 80 %). Only data that goes through dualnet counts. The last usable link is never excluded, so the internet keeps working.
- **Applications and rules** (transparent mode): the dashboard lists which programs use the network and through which links. A rule restricts a program to one link (for example an online game on Wi‑Fi for a steady ping while Steam uses every link); if that link is down, the program uses the others.
- **Notifications**: a Windows notification when a link drops, comes back or appears (and for budget events), even if the window is closed. The toast appears under Windows PowerShell's name. The Settings page has a "send a test" button.
- **Update check**: once a day dualnet asks GitHub for the latest release and shows a banner. Nothing is downloaded or installed automatically; it can be turned off in Settings.
- **Diagnostics**: Settings can show the log live and copy a bug-report text (version, links, last log lines). Check it before sharing: it lists your network interface names and addresses.
- **History**: data per link and per day (30 days shown, one year kept).
- **Windows settings in one click** (administrator): keep Wi‑Fi on with USB tethering, link preferred by Windows, restart Steam.

Data and settings: `%APPDATA%\dualnet` (`history.json`, `settings.json`, `dualnet.log`). Set the `DUALNET_DATA` environment variable to use another folder.

## Command line

```powershell
.\dualnet.exe ui -sticky 0 -transparent     # interface + transparent mode (administrator PowerShell)
.\dualnet.exe ui                            # interface + proxy, no admin rights needed
.\dualnet.exe get https://example.com/big-file.iso
.\dualnet.exe proxy -listen 127.0.0.1:1080 -system-proxy
.\dualnet.exe window                        # dualnet's window for an instance started with "ui"
.\dualnet.exe interfaces
```

`-sticky 10m` (default) keeps a given site on the same link for 10 minutes (useful for sites that watch your IP); `-sticky 0` spreads every connection (best for Steam).

Add `?lang=en`, `?lang=fr`, `?lang=es` or `?lang=de` to the interface address to force a language. The notification-area menu and the Windows notifications are in English or French (Windows' language).

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
- Per-application statistics and rules work in transparent mode only: in proxy mode dualnet cannot tell which program a connection comes from.
- An already open connection cannot move to another link: if its link drops it ends, and the application reconnects over the remaining links.

## Development

```powershell
go test ./...
go build -o dualnet.exe .
go run ./tools/mkicon     # regenerates the icon (assets/) and the .exe resource
```

GitHub Actions builds and tests every push. To publish a release, push a version tag:

```powershell
git tag v0.3.0
git push origin v0.3.0
```

The workflow builds `dualnet.exe` with that version and attaches it to a GitHub release with generated notes.

## Security

dualnet runs as administrator, and its interface talks to a local API on `127.0.0.1:8090`. That API:

- only answers to loopback `Host` names (no DNS rebinding);
- requires the custom header `X-Dualnet: 1` on every request that changes something, and refuses requests the browser marks as cross-site or that carry a foreign `Origin`: a web page you visit cannot drive dualnet (a custom header forces a CORS preflight, which dualnet never grants);
- only accepts `http(s)` URLs for downloads.

If you script the local API yourself, send `X-Dualnet: 1` with your POST requests.

Found a problem? Please open an issue.

## License

[MIT](LICENSE). `wintun.dll` keeps its own license (WireGuard LLC).
