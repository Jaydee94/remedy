# Docker's name filter and sudo's reading of the line

Measured 2026-10-21 for `docs/specs/2026-10-21-operation-6-design.md` section 5 (plan task 1). The script is `scripts/spike/docker-ps-filter.sh` (`sh scripts/spike/docker-ps-filter.sh [docker|sudo]`; both parts without an argument). It removes what it makes.

Question: can `docker ps -a` on a host be limited to the host's container pattern by a filter that Remedy appends and that `sudoers` enforces word for word?

## Setup

- Docker server 29.9.1 (Docker Desktop on macOS). Image for part (a): `alpine:latest`, `docker run -d --pull=never --name <n> alpine:latest sleep 300`.
- Part (a) containers: `remedy-a`, `remedy-drill`, `remedy-drill2`, `xremedy-c`, `paperless-db`. The script refuses to run if one of these names exists and removes only these on exit.
- Part (b) image: built from `dev/ssh/Dockerfile` as `ops6-spike-ssh:test` (Debian bookworm, `Sudo version 1.9.13p3`, real `sudo` and `visudo`, a fake `/usr/bin/docker`). One container `ops6-spike-sudo`; the testbed's own `/etc/sudoers.d/remedy` is deleted inside it so that only the spike's lines count. The sudoers files exist only in that container. `dev/ssh/up.sh`, `~/remedy-ssh` and the testbed's names are not used. Container and image are removed on exit.

## (a) Which names does the filter list

The daemon stores every name with a leading slash (`docker inspect --format '{{.Name}}'` gives `/remedy-a`, `/remedy-drill`, ...), while `docker ps` shows it without. Filter results (`docker ps -a --filter <f> --format '{{.Names}}'`, sorted):

| Filter | Names listed |
|---|---|
| `name=^remedy-` | `remedy-a remedy-drill remedy-drill2` |
| `name=^remedy-drill$` | `remedy-drill` |
| `name=^/remedy-` | `remedy-a remedy-drill remedy-drill2` |
| `name=^/remedy-drill$` | `remedy-drill` |
| `name=remedy-` | `remedy-a remedy-drill remedy-drill2 xremedy-c` |
| `name=^remedy-drill` | `remedy-drill remedy-drill2` |
| `name=remedy-*` | `remedy-a remedy-drill remedy-drill2 xremedy-c` |
| `name=^remedy-.*` | `remedy-a remedy-drill remedy-drill2` |

Findings:

- `^remedy-` lists exactly the containers of the prefix and neither `xremedy-c` nor `paperless-db`. The leading slash of the stored name is no problem: docker's `^` anchors at the name as `ps` shows it, and `^/remedy-` gives the same result, so both spellings work. The shorter one needs no slash.
- `^remedy-drill$` lists only `remedy-drill`, not `remedy-drill2`.
- An unanchored `name=remedy-` also lists `xremedy-c`: the anchor is required.
- The filter is a regular expression, so a `.` in a pattern is looser than the pattern says (not measured here; the spec accepts it).

## (b) sudo's reading of the line

All as the account `remedy`, `sudo -n -l <command>`, exit status 0 means allowed. `/usr/bin/docker` is spelled in full because sudo matches the path.

Line B1, prefix, with the backslash: `remedy ALL=(root) NOPASSWD: /usr/bin/docker ps -a --filter name\=^remedy-`

```
$ visudo -cf /etc/sudoers.d/ops6-prefix
/etc/sudoers.d/ops6-prefix: parsed OK                                   (exit 0)
$ sudo -n -l /usr/bin/docker ps -a --filter name=^remedy-               allowed (exit 0), prints the command
$ sudo -n -l /usr/bin/docker ps -a                                      refused (exit 1)
$ sudo -n -l /usr/bin/docker ps -a --filter name=^other-                refused (exit 1)
$ sudo -n -l /usr/bin/docker ps -a --filter name=^remedy-drill$         refused (exit 1)
$ sudo -n -l /usr/bin/docker ps -a --filter name=remedy-                refused (exit 1)
$ sudo -n -l /usr/bin/docker ps -a --filter name=^remedy- --all         refused (exit 1)
$ sudo -n -l ... --filter name=^remedy- --filter name=paperless         refused (exit 1)
```

Executing for real as `remedy`: `sudo -n /usr/bin/docker ps -a --filter name=^remedy-` runs (the fake docker answers); `sudo -n /usr/bin/docker ps -a` prints `sudo: a password is required` (refused by sudo itself).

Line B2, exact name, with the backslash: `remedy ALL=(root) NOPASSWD: /usr/bin/docker ps -a --filter name\=^remedy-drill$`

```
visudo -cf: parsed OK                                                   (exit 0)
sudo -n -l ... --filter name=^remedy-drill$                             allowed (exit 0)
sudo -n -l ... --filter name=^remedy-drill2$                            refused (exit 1)
sudo -n -l ... --filter name=^remedy-                                   refused (exit 1)
sudo -n -l /usr/bin/docker ps -a                                        refused (exit 1)
```

The `$` needs no escape in sudoers.

Line B3, prefix, WITHOUT the backslash: `... ps -a --filter name=^remedy-`

```
visudo -cf: parsed OK                                                   (exit 0)
sudo -n -l ... --filter name=^remedy-                                   allowed (exit 0)
sudo -n -l /usr/bin/docker ps -a                                        refused (exit 1)
```

Unexpected: with sudo 1.9.13p3 `visudo` accepts the line without the backslash and it behaves identically. The backslash is not needed here. `sudoers(5)` says `,`, `:`, `=` and `\` in command arguments are to be escaped with a backslash (from the manual, not measured on other versions); an older sudo or UGOS's may be stricter, so the line keeps the backslash. It costs nothing: the escaped line matches the same command.

Line B4, a star at the end (`... name\=^remedy-*`), as a contrast: `visudo` accepts it and it allows `--filter name=^remedy-x --all` too (the star matches spaces and further operands). The exact line with no star is therefore the one that pins the command.

## Decision

- Filter strings (appended to `docker ps -a` as the two arguments `--filter` and the value):
  - prefix pattern `remedy-*`: `name=^remedy-`
  - exact name `remedy-drill`: `name=^remedy-drill$`
  - Executed argv: `docker ps -a --filter name=^remedy-` (via `sudo -n --`).
- Sudoers spelling, one line per host, no star, full path of `docker`, the backslash before the `=`:
  - prefix: `remedy ALL=(root) NOPASSWD: /usr/bin/docker ps -a --filter name\=^remedy-`
  - exact: `remedy ALL=(root) NOPASSWD: /usr/bin/docker ps -a --filter name\=^remedy-drill$`
- sudo then refuses the plain `docker ps -a`, a different filter, an extra flag and a second `--filter`, so the filter is enforced by sudo and not only by Remedy.
- Not measured: another sudo version, a pattern with a dot, and the NAS's own `docker` path (`command -v docker`, plan task 8 and the spec's section 12).

Comparison with spec section 5: it agrees. The filter strings, the leading slash worry (no problem, `^remedy-` works and `^/remedy-` works too) and the `\=` line are as the spec has them; the only addition is that the backslash is optional with sudo 1.9.13 and kept for safety.
