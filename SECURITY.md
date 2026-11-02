# Security policy

## Reporting a vulnerability

Please do not open a public issue for a vulnerability.

Report it privately with GitHub's private vulnerability reporting: open the repository's **Security** tab and choose
**Report a vulnerability** ([`github.com/Jaydee94/remedy/security/advisories/new`](https://github.com/Jaydee94/remedy/security/advisories/new)).

Say what you found, which version or commit it is in, how to reproduce it and what you think it allows. If it involves a
credential (a GitHub token, a host login, the CLI login, the master key), do not paste a real one; describe where it leaks.

Remedy is maintained by one person in their spare time. The aim is to answer within a week and to fix what is confirmed in
the next release; that is an intent, not a promise.

## What is in scope

Anything that lets someone without the admin password act through the UI or the API, anything that lets an agent run reach a
credential or act without the approval the gatekeeper requires, and any path by which a secret (the sealed GitHub token,
host credentials, the master key, the CLI login, the one-time login code) reaches a log, an error, an API answer or a
database column.

The accepted risks in [`docs/design.md`](docs/design.md), section 4, are known and not vulnerabilities by themselves. Among
them: the UI is protected by a password only, so it belongs on a LAN or behind a VPN, not on the open internet.

## Supported versions

The latest release. Older ones get no fixes; upgrade.
