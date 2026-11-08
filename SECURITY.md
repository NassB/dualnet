# Security

dualnet runs as administrator and routes all of a PC's network traffic, so security problems matter. Thank you for reporting them responsibly.

## Reporting a vulnerability

**Please do not publish the details in a public issue.**

- If the repository's **Security** tab shows a **Report a vulnerability** button, use it: the report stays private.
- Otherwise open a public issue titled `Security: please contact me`, **without any technical detail**, and ask for a private channel. The maintainer will answer there.

Useful in a report: the dualnet version (`dualnet version`), what you did, what happened, and what you expected.

## What is already in place

- The interface talks to a local API on `127.0.0.1:8090`. It only answers loopback `Host` names, and every request that changes something needs the `X-Dualnet` header (a web page cannot send it without a CORS preflight, which dualnet never grants), must not be cross-site and must not carry a foreign `Origin`. See [README](README.md#security).
- Releases are built by GitHub Actions with the newest Go, from actions pinned to commits, with `govulncheck` on every push. Each release publishes `dualnet.exe.sha256` and a build provenance attestation.
- Text shown in notifications reaches PowerShell through environment variables, never inside a script.

## Verifying a download

Compare the checksum with the one in the release:

```powershell
certutil -hashfile dualnet.exe SHA256
```

Check where and how the file was built (needs the [GitHub CLI](https://cli.github.com)):

```powershell
gh attestation verify dualnet.exe --repo NassB/dualnet
```

## Good practice

dualnet is not code-signed, so Windows SmartScreen warns on first run. Because it runs as administrator, keep `dualnet.exe` in a folder only administrators can modify (for example `C:\Program Files\dualnet`); the installation check (`dualnet doctor`) points it out when it is elsewhere.
