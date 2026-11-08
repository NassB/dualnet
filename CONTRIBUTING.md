# Contributing to dualnet

Thanks for helping! Bug reports, translations and code are all welcome.

## Reporting a bug

Open an issue and paste the output of **Settings → Diagnostics → Copy diagnostics** (or `dualnet doctor` for the installation check). It contains the version, your links and the last log lines. Read it before posting: it lists your network interface names and addresses.

For a security problem, see [SECURITY.md](SECURITY.md) instead.

## Translating

The interface texts are in [`internal/ui/lang/`](internal/ui/lang): one JSON file per language, `en.json` is the reference.

- **Fix a translation**: edit the text in `fr.json`, `es.json` or `de.json`. Keep the `{placeholders}` (`{n}`, `{name}`…) and the `<b>` tags.
- **Add a language**: copy `en.json` to `<code>.json` (two letters, e.g. `it.json`) and translate every value. The page picks it up by itself when the browser uses that language; `?lang=it` forces it.
- `_server` maps English server messages to your language: translate the values, not the keys.

`go test ./internal/ui` fails if a language lacks a key, has an empty text or loses a placeholder, and if the page asks for a text `en.json` does not have.

The notification-area menu and the Windows notifications are in English or French, in the Go code (`tray_windows.go`, `internal/notify`).

## Code

You need Go (the newest release) and Windows 10/11.

```powershell
go vet ./...
go test ./...
go build -o dualnet.exe .
```

- One change per commit, with a message that says *why*.
- Add or update a test with behaviour changes. Pure logic (the picker, the budget, the checks) is tested with fakes; the Windows-specific parts are tested against the real system where it is safe (a throwaway registry key, loopback connections).
- Changes to the local API must keep the guard in `internal/ui/guard.go`; `internal/ui/guard_test.go` replays web-page attacks.
- The release workflow is guarded by `ci_test.go`: actions stay pinned to commits and only the release job may write.
- Set `DUALNET_DATA` to another folder when you run a development build, so it does not touch your real settings and history.

### Layout

| Path | What |
|---|---|
| `main.go`, `tray_*.go` | commands, the notification-area icon and the window |
| `internal/links` | network links: discovery, health, speed cap, budget flag |
| `internal/proxy` | the picker (capacity, overflow, rules), SOCKS5/HTTP proxy, transparent-mode dialer |
| `internal/transparent` | Wintun adapter and routes |
| `internal/ui` | local web server, API and the page (`index.html`, `lang/*.json`) |
| `internal/store` | history and settings (`%APPDATA%\dualnet`) |
| `internal/doctor`, `speedtest`, `update`, `notify`, `procinfo`, `winsys`, `sysproxy` | one job each |

## Pull requests

Describe what changed and how you checked it. Small, focused pull requests are easiest to review.
