# Local API

The interface talks to dualnet through an HTTP API on `127.0.0.1:8090` (the `-listen` address of `dualnet ui`). You can script it, but it is **not a stable interface**: endpoints may change between releases.

Rules of the API (see [Security](../README.md#security)):

- only loopback `Host` names are answered;
- every request that changes something is a `POST` and must carry the header `X-Dualnet: 1`; cross-site requests and foreign `Origin`s are refused;
- a success without data is `204 No Content`; a bad value is `400`, an unknown link or object `404`.

```powershell
curl.exe -X POST -H "X-Dualnet: 1" "http://127.0.0.1:8090/api/transparent?on=1"
```

## Read

| Request | Returns |
|---|---|
| `GET /api/events` | live state, one JSON snapshot every 500 ms (server-sent events) |
| `GET /api/history?days=30&format=json\|csv` | traffic per day and link (`days` 1-366; `csv` is a download) |
| `GET /api/journal` | the last 200 link events since dualnet started (set aside, back, unplugged, over budget), most recent first |
| `GET /api/domainrules` | the domain rules |
| `GET /api/profiles` | the profile names |
| `GET /api/wifi` | the Wi-Fi network the PC is on (`current`, "" = none) and the rules that apply a profile by network |
| `GET /api/doctor` | the installation checks |
| `GET /api/diagnostics`, `GET /api/log` | the bug-report text, the log |

## Change

| Request | Effect |
|---|---|
| `POST /api/transparent?on=1\|0` | transparent mode on/off |
| `POST /api/power`, `/api/sysproxy`, `/api/autostart` | main switch, Windows proxy, start with Windows (`on=1\|0`) |
| `POST /api/limit?link=<name>&mbps=<n>` | speed cap of a link (0 = none) |
| `POST /api/budget?link=<name>&gb=<n>` | monthly budget of a link (0 = none) |
| `POST /api/budgetwarn?percent=<50-99>` | share of a budget at which dualnet warns |
| `POST /api/role?link=<name>&mode=normal\|overflow` | how a link is used |
| `POST /api/rule?app=<exe>&link=<name>` | restrict a program to a link (`link=*ping`: the link with the lowest ping; empty `link` removes the restriction, `all=1` removes the whole rule) |
| `POST /api/applimit?app=<exe>&mbps=<n>[&dir=up]` | download cap of a program, or its upload cap with `dir=up` (0 = none) |
| `POST /api/domainrule?domain=<name>&link=<name>` | send a domain (and subdomains) over a link (empty `link` removes the rule) |
| `POST /api/profile?name=<name>&action=save\|apply\|delete` | save, apply or delete a profile |
| `POST /api/wifiprofile?ssid=<network>&profile=<name>` | apply a profile by itself when the PC joins a Wi-Fi network (empty `profile` removes the rule) |
| `POST /api/speedtest?link=<name>` | measure one link |
| `POST /api/update/install` | download the newer release found by the daily check, verify its checksum, replace the program and restart (app mode on Windows only; `501` otherwise) |
| `POST /api/download` (JSON body `{"url": "...", "workers": 4, "sha256": "<64 hex, optional>"}`), `/api/cancel?id=<n>`, `/api/pause?id=<n>` (keeps the partial file), `/api/resume?id=<n>` (back to the queue), `/api/retry?id=<n>` (failed download back to the queue; `409` if not failed or the address is already downloading), `/api/move?id=<n>&to=up|down|top` (change the place of a queued download in the start order; `409` if not queued), `/api/startnow?id=<n>` (run a queued or paused download at once, even above the limit; `409` otherwise), `/api/jobcap?id=<n>&mbps=<0-10000>` (speed cap of one download, 0 = none; live on a running one, kept in the list; `400` if invalid), `/api/jobsha?id=<n>&sha256=<64 hex>` (expected SHA-256 of a download, empty = remove; a finished one is checked at once; the job shows `sha256`, `shaState` = `checking`/`ok`/`mismatch` and, on a mismatch, `shaGot`; a mismatch keeps the file; `400` if not 64 hex), `/api/jobname?id=<n>&name=<file name>` and `POST /api/jobdir?id=<n>` (JSON body `{"dir": "C:\folder"}`, absolute, created if needed) (rename / move a download: before it starts the wish is used when it runs; paused, failed or done: the file and its `.part`/`.part.json` are renamed on the same drive; `409` if running, if the file exists or on another drive; `400` for a bad name or a relative folder), `/api/clear` (drop finished/failed/cancelled downloads from the list), `/api/remove?id=<n>` (drop one; `409` if still active; files are never deleted), `/api/reveal?id=<n>`, `/api/openfile?id=<n>` (open a finished download with its default app; `409` if not done, `404` if the file is gone, `403` for programs like `.exe`) | multi-link downloads |
| `POST /api/maxdownloads?n=<0-8>` | downloads running at once, the others wait in a queue (0 = no limit, default 2) |
| `POST /api/downloaddir` (JSON body `{"dir": "D:\\Downloads"}`, `""` = default), `/api/downloaddir/browse` (Windows folder picker, returns `{"dir": "..."}`, saves nothing) | folder of the Downloads page; the folder is created if needed and must be writable |
| `POST /api/system?do=simultaneous\|prefer&...` | Windows network settings (administrator) |

The authoritative list is the route table in `internal/ui/ui.go`.
