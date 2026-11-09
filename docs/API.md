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
| `GET /api/domainrules` | the domain rules |
| `GET /api/profiles` | the profile names |
| `GET /api/doctor` | the installation checks |
| `GET /api/diagnostics`, `GET /api/log` | the bug-report text, the log |

## Change

| Request | Effect |
|---|---|
| `POST /api/transparent?on=1\|0` | transparent mode on/off |
| `POST /api/power`, `/api/sysproxy`, `/api/autostart` | main switch, Windows proxy, start with Windows (`on=1|0`)\|0`) |
| `POST /api/limit?link=<name>&mbps=<n>` | speed cap of a link (0 = none) |
| `POST /api/budget?link=<name>&gb=<n>` | monthly budget of a link (0 = none) |
| `POST /api/budgetwarn?percent=<50-99>` | share of a budget at which dualnet warns |
| `POST /api/role?link=<name>&mode=normal\|overflow` | how a link is used |
| `POST /api/rule?app=<exe>&link=<name>` | restrict a program to a link (empty `link` removes the restriction, `all=1` removes the whole rule) |
| `POST /api/applimit?app=<exe>&mbps=<n>` | download cap of a program (0 = none) |
| `POST /api/domainrule?domain=<name>&link=<name>` | send a domain (and subdomains) over a link (empty `link` removes the rule) |
| `POST /api/profile?name=<name>&action=save\|apply\|delete` | save, apply or delete a profile |
| `POST /api/speedtest?link=<name>` | measure one link |
| `POST /api/download` (JSON body `{"url": "...", "workers": 4}`), `/api/cancel?id=<n>`, `/api/reveal?id=<n>` | multi-link downloads |
| `POST /api/system?do=simultaneous\|prefer&...` | Windows network settings (administrator) |

The authoritative list is the route table in `internal/ui/ui.go`.
