# Hosts, cycle 2: host tools in the gatekeeper

Status: implemented 2026-10-13 (pull request pending). Implementation plan: docs/plans/hosts-2-host-tools.md.
Parent documents: [`2026-10-09-hosts-and-skills-design.md`](2026-10-09-hosts-and-skills-design.md) (the umbrella: sections 2, 3 and 11 are binding here; this spec changes `host_exec` into two tools, see 2),
[`2026-10-09-hosts-1-inventory-design.md`](2026-10-09-hosts-1-inventory-design.md) (hosts, sealed logins, pinned keys, the connection test and its report, built),
[`2026-10-04-phase-2ab-gatekeeper-and-approvals-design.md`](2026-10-04-phase-2ab-gatekeeper-and-approvals-design.md) and
[`2026-10-04-phase-2c-cluster-design.md`](2026-10-04-phase-2c-cluster-design.md) (the gatekeeper, the approvals, the tool groups and the run switch this copies).

## 1. Goal and scope

An agent can look at a host and, after the maintainer's approval, change something on it. The control plane runs the commands over SSH with the login of cycle 1; the agent, the
runner and the UI never see a credential. **Reading is free and limited to a small built-in table; every change waits for an approval in "Needs you", shown word for word.**

The cycle is proven on a throwaway SSH testbed (`dev/ssh/`: a real `sshd`, a real `sudo` and a fake `docker`) and by a real run against it. Pointing Remedy at the NAS is cycle 6.

| Cycle | Content | Status |
|---|---|---|
| 1 | Hosts and access | implemented |
| 2 | Host tools in the gatekeeper | this spec |
| 3 to 6 | Settings, Alertmanager and the responder, skills, operation on the home server | see the umbrella |

### Non-goals

- The automatic responder. It gets host tools in cycle 4; here only **ad-hoc runs** (a question to Remedy) can have hosts.
- Skills, learning, notes about a host for the agent (cycle 5). `host_list` shows the maintainer's notes as text, nothing more.
- Per-host additions to the table of reads in the UI (the umbrella allows them; not built here).
- A shell, a pty, standard input, file transfer, port forwarding, an interactive command, a command that runs longer than the limit.
- General file readers (`cat`, `head`, `tail`, `grep`, `ls`, `find`). Decided: not in the table. Logs come from `docker logs` and `journalctl`.
- Connection reuse between calls, and any parallelism on one host.

## 2. Decisions

| Topic | Decision |
|---|---|
| Tools | **Three tools in a new group `host`:** `host_list` (read), `host_read` (read) and `host_change` (mutating, an approval each). This replaces the umbrella's single `host_exec`: the gatekeeper decides read or mutating **per tool, statically** (`Tool.Mutating`), and that stays as it is. The classification is still the barrier: `host_read` runs nothing that the table does not describe exactly. |
| Which runs | An ad-hoc run with gatekeeper access that was started with **hosts** (`run_hosts`, fixed at creation). A run without hosts is not offered the group. A run sees only its hosts, by name; a host that is disabled, has no login or has no pinned key is not usable. |
| `host_read` | `{host, argv}`. Runs only an argv that matches a **form of the table** (section 4). Anything else is refused at once with the list of forms and "use `host_change`, which waits for approval". |
| `host_change` | `{host, argv}`. Any argv that passes the argument rules and is **not on the never-list** (section 5). It always waits for an approval and runs as `sudo -n -- <argv>`. The OS decides after that: the `sudoers` file of cycle 1 is the second barrier. |
| No shell on our side | The argv travels as **single-quoted words** in one command line (OpenSSH hands the line to the account's shell). Control characters are refused, so are NUL and an argv over 64 words or 4096 bytes. For `host_change` every character must also be printable and visible (`visible()`): a zero-width or right-to-left mark, a no-break space, a line separator or a private-use character is refused, because the approval card shows the words as text and must show exactly what runs. A container name made only of hex digits is never a container (docker reads it as an ID prefix), so it matches no pattern. |
| Shell canary | Before every command, in the same connection, Remedy runs `printf %s <canary>` with the same quoting and compares the bytes it gets back. A login shell that does not treat quoted words like a POSIX shell (fish, csh, a restricted shell) fails the canary, and the host's tools answer "the account's shell is not supported". |
| Sudo for reads | Docker forms run as `sudo -n -- docker ...`, because the account is not in the `docker` group (cycle 1 warns about that). The container name must match the host's container pattern, **checked here and again by `sudoers`**. All other forms run as the account. |
| Output | Opens with a line that says it is data from the host. stdout and stderr are kept apart, each cut at 24 KB; the exit code is stated; the whole result goes through `sanitize` (redaction, 32 KB) like every gatekeeper result. |
| `docker inspect` | Its JSON is parsed and the **values of `Config.Env` and `ContainerConfig.Env` are removed** (names stay), as the cluster tools do for environment variables. Output that cannot be parsed, has another shape or was cut is not shown: the answer holds a fixed text instead. The same holds for every inspect that has a template (`--format`, `-f`, a flag cluster with `f`): a template can print the environment under any key, so its output is never shown. The filter applies to `host_read` and to an approved `host_change` alike, and it recognises an inspect by the word `inspect` after a `docker` program anywhere in the argv (also behind `sudo`, `env` or global options). |
| Time | The gatekeeper's `executeTimeout` is 75 seconds overall for an approved call; the command itself gets 30 seconds, and the dial and the shell canary 10 seconds each. A new connection for every call, serialised per host by the `Service` of cycle 1. A command that does not end in time, or whose connection is lost after it started, has an **unknown outcome**: the answer says that the host did not answer in time and that the change may or may not have happened, and it does not suggest to run it again. |
| Approval card | The question is a fixed sentence: "May I run `docker restart remedy-drill` on nas?"; the argv is shown in full next to it. What is shown is what runs. |
| Stale approval | If the host row or its login changed after the question was asked (`hosts.updated_at` is later than `Call.RequestedAt`, including pinning, unpinning and a new login), the approved call is **not run**: "the host changed after the approval was asked for; ask again". |
| Audit | Every call is a `tool_calls` row (cycle 2a). An executed change writes the activity entry `Ran docker restart remedy-drill on nas` (kind `host_action`). No credential is ever in a row, an entry or a log. |

## 3. Components

| Part | Where | Does |
|---|---|---|
| Classification | `internal/hostexec` (new, pure) | `Classify(argv, rules) Verdict` (read form, change, never, invalid), the table, the never-list, `Quote`, `CommandLine`, `Canary`, the `docker inspect` filter. No I/O. |
| Tools | `internal/gatekeeper/tools_host.go` | `HostTools(HostAccess)`: `host_list`, `host_read`, `host_change`, built on the usual `Tool` (`Decode`, `Check`, `Run`, `Activity`). The group `host` is new; `offeredTo` offers it to a run with hosts. |
| Access | `internal/gatekeeper` (interface), `internal/app` (implementation) | `HostAccess` is what the tools need: the hosts of a run, their rules and last report, and `Exec`. The implementation opens the sealed login, holds the pinned key and calls `sshhost`. **The gatekeeper package holds no credential and no crypto.** |
| SSH | `internal/sshhost` | `Run` returns stdout and stderr apart with a limit that the caller sets; a new `Service.Exec` dials against the pinned key, runs the canary, runs the command line, closes. New error `ErrShell`. |
| Store | `internal/store` | Migration 011 (`run_hosts`), `hosts.updated_at` moves on every change of the row, its pin and its login, `KindHostAction`, a single creator of runs with options. |
| Server | `internal/server` | `hosts` on `POST /api/runs`; the run answers with its hosts. |
| UI | `web/` | A chip per usable host on the Ask page, the host names on the run, the approval sentence for `host_change`. |
| Testbed | `dev/ssh/` | A container with `sshd`, `sudo`, a fake `docker`, a `sudoers` file made from the cycle 1 snippet, and a second account with a non-POSIX shell. |

## 4. The table of reads

A form is a fixed sequence of words with typed slots. A word is literal; a slot is `NAME` (a container name that matches the host's pattern), `UNIT` (a systemd unit,
`.service`, `.timer` or `.socket` as in cycle 1) or `N` (a whole number, 1 to 2000). The argv must match a form **exactly**: no other word, no other order, no extra flag.
Anything not described is not a read.

| Form | Runs as | Notes |
|---|---|---|
| `docker ps -a` | sudo | Needs a container pattern on the host. Since cycle 6 it runs as `docker ps -a --filter name=^PREFIX` (`^NAME$` for an exact name), so only the host's containers are listed; the agent still asks for the plain words (section 5 of [`2026-10-21-operation-6-design.md`](2026-10-21-operation-6-design.md)). |
| `docker logs --tail N NAME` | sudo | |
| `docker inspect NAME` | sudo | The output is filtered (section 2). |
| `systemctl status UNIT --no-pager` | the account | |
| `systemctl is-active UNIT` | the account | |
| `systemctl list-units --failed --no-pager` | the account | |
| `journalctl -u UNIT -n N --no-pager` | the account | Needs the group `systemd-journal` (the snippet says so). |
| `df -h` | the account | |
| `free -m` | the account | |
| `uptime` | the account | |
| `uname -a` | the account | |
| `ps -eo pid,user,pcpu,pmem,etime,comm` | the account | |
| `ss -tln` | the account | |

The program word is matched as written (`docker`, not `/usr/bin/docker`). The table is code, versioned and tested; **no skill, no agent proposal and no host field can add to it.**

For a change, `Classify` also knows two shapes that it can check before an approval is asked for: `docker start|stop|restart NAME` (the name must match the host's container pattern)
and `systemctl start|stop|restart UNIT` (the unit must be in the host's list). The program word of these two may be an absolute path (`/usr/bin/docker`): the same rules apply. A `docker` or `systemctl` `start|stop|restart` with more than one container or unit (or with a flag) is refused, because a sudoers `*` would let the extra words through: one container or unit at a time. A change of any other shape is allowed and goes to the approval as it is.

## 5. The arguments, the never-list and the approval

**Arguments** (`Decode`, strict, for both tools): `host` is a name (the pattern of cycle 1); `argv` is 1 to 64 strings; each word has 1 to 1024 bytes, no control character (below `0x20`, `0x7f`), no NUL.
For `host_change` the first word is also a **program name**: letters, digits and `._+-`, not starting with `-`, or an absolute path of such names. A word with `=` first would be an environment assignment to `sudo`; a leading `-` would be an option of `sudo`.

**The never-list** is in code and is checked for `host_change` before anything else, **also when the maintainer would approve**. It matches on the program (the last path element, and the program behind `sudo`, `env`, `nohup`, `time`, `timeout`, `nice`, `ionice`, `command`, `exec`, `xargs`, `watch`; every word is also looked at as a possible program, so a wrapper is seen through) and on its words. `su` and `doas` are always refused (they are not wrappers to look through: `sudo -n` is the only privilege path), and so are `nsenter` and `chroot`, `sudo` with `-s`, `-i` or `-e` (or their long forms) and `env -S`/`--split-string`:

- Filesystem and disks: `mkfs*`, `mke2fs`, `wipefs`, `fdisk`, `parted`, `sgdisk`, `shred`, `dd` with `of=/dev/...`, `rm` with `-r`/`-R`/`--recursive` on `/` or a system path (`/etc /usr /bin /sbin /lib /boot /var /home /root /opt /srv /dev /proc /sys`), `chmod`/`chown` with `-R` on those.
- Access: any command that names `authorized_keys`, `sudoers`, `/etc/sudoers.d`, `/etc/passwd`, `/etc/shadow`, `/etc/ssh/sshd_config` among its words, `passwd`, `chpasswd`, `usermod`, `useradd`, `userdel`, `visudo`.
- Code from a string: `sh`, `bash`, `dash`, `zsh`, `ksh`, `fish`, `csh`, `tcsh` (with any arguments), `python*`/`perl`/`ruby`/`node`/`php`/`lua` with `-c`/`-e`/`-r`, `awk`/`gawk`/`mawk`, `eval`, `curl`/`wget` with `-o`/`-O`/`--output`/`--post-file`/`-T` (the agent downloads nothing and uploads nothing).
- Power: `shutdown`, `reboot`, `poweroff`, `halt`, `init 0`, `systemctl poweroff|reboot|halt|kexec`.
- Reconfiguring Remedy's own door: `iptables`, `nft`, `ufw` (they could lock the maintainer out or open the host), `systemctl ... ssh|sshd` with `stop|disable|mask`.

Each refusal states the rule in a sentence. The list may be **extended** with a test; shrinking it needs a decision recorded in this spec.

**`Check`** (before an approval is asked for, so that no attention is wasted): the host is one of the run's, enabled, has a login and a pinned key; for `host_change` the last report of the host does not say `sudo: none` ("sudo cannot run without a password for this account: fix `sudoers`, then test the connection"); for the two known shapes the name or unit is in the host's rules.

**`Run`** of `host_change` checks again, on the stored arguments: the never-list (a call that reached the database some other way is still not run) and the host: it is re-read and the call is refused when `hosts.updated_at` is later than `Call.RequestedAt` (second resolution, like `cluster_delete_pod`).

## 6. Data model (migration 011)

```sql
CREATE TABLE run_hosts (
  run_id  TEXT    NOT NULL REFERENCES runs (id)  ON DELETE CASCADE,
  host_id INTEGER NOT NULL REFERENCES hosts (id) ON DELETE CASCADE,
  PRIMARY KEY (run_id, host_id)
);
CREATE INDEX run_hosts_host ON run_hosts (host_id);
ALTER TABLE runs ADD COLUMN host_count INTEGER NOT NULL DEFAULT 0;
```

- A run gets its hosts when it is created and never changes them. Deleting a host removes the binding; the `tool_calls` rows keep the host **name** in their arguments, so the audit stays readable.
- `run.Run` gets `HostCount`, from a new column `runs.host_count` (a copy of the number of bindings at creation, used by `offeredTo`; a column and not a subquery of the run columns, because `ClaimNext` reads the run back with `UPDATE ... RETURNING`; deleting a host removes its binding and not the count, and the host tools then find no usable host); `GET /api/runs/{id}` also answers `hosts: [{id, name}]`. The lists answer the count only.
- The store has four creators of runs (`CreateRun`, `CreateToolRun`, `CreateClusterRun`, `CreateQuestionRun`), each a variant of the other. A fifth would be one too many: they are replaced by **one** `CreateRunWith(ctx, RunOptions{Provider, Prompt, Tools, Cluster, IncidentID, HostIDs})` in the same transaction as the `run_hosts` rows, and the old four become thin wrappers or go. Their tests move with them.
- `PinHostKey`, `UnpinHostKey`, `SaveHostCredential` and `DeleteHostCredential` also set `hosts.updated_at` (they do not today). A migration test (in the manner of `migrate010_test.go`) runs on a database built from 001 to 010.
- `KindHostAction = "host_action"`; the generalised `FinishToolCallWithActivity` takes the kind from the tool (`Tool.ActivityKind`, empty means the cluster kind, so the cluster tools do not change).

## 7. API and UI

- `POST /api/runs` takes `hosts: [id]`. It needs `tools: true` (`400` otherwise), the gatekeeper and the host service (`409` "no hosts are configured" otherwise), and every id must be a usable host (`409` "this host is not ready: <reason>", where the reason is one of: disabled, no login, key not pinned). At most 8 hosts.
- **Ask page:** under the existing chips, one chip per usable host ("Look at nas"). Choosing a host switches the tools on, as the cluster chip does. The hint sentence says what is possible: reading is free, every change waits for the maintainer under "Needs you" and is shown word for word.
- **Run page:** the host names as text next to the existing badges.
- **Approval card:** `ask.ts` gets `host_change`: `May I run <command> on <host>?` with `Yes, run it`. `<command>` is the argv joined with the quoting a person would type (a word with a space gets single quotes); it is text, never markup. The arguments are shown in full as for every tool.
- The tool-call list of a run (`ToolCallsCard`) shows `host_read` and `host_change` like the other tools: arguments as text, the result as text.
- Words stay in pure modules (`ask.ts`, `askpage.ts`) with `node --test`.

## 8. Untrusted text

Everything a host prints is untrusted data: it opens with the data line, is redacted and cut, and is shown to the maintainer as text. It cannot extend the table or the never-list. What the agent derives from it and proposes as a change is judged by the maintainer, who sees the exact command. A container name, a unit name or a log line that contains an instruction is text.

## 9. Testing

Test first for the logic, in this order:

1. **`hostexec`, pure.** A table-driven classification test with every attack the planning session named: `; rm`, `$(...)`, backticks, redirects, `|`, `&&`, a flag that looks like a word, `docker run -v /:/h`, `docker exec`, `find -exec`, `journalctl --output`, a program given with a path, `sudo` inside the argv, `env`, `xargs`, `sh -c`, a word with a newline or a NUL, a leading `-`, `FOO=bar cmd`, a container name outside the pattern, `docker ps` without `-a`, a flag reordered. Each form of the table has a positive and a negative case; a test removes nothing silently (the table is listed and counted).
2. **The never-list**, one case per rule and wrapper (`sudo rm -rf /`, `env sh -c`, `nohup dd of=/dev/sda`), and that `host_read` can never match a never-listed argv.
3. **Quoting against a real shell.** `Quote`/`CommandLine` are run through `/bin/sh` (and `bash`/`dash` if present) with a helper that prints its arguments NUL-separated; the bytes that come back must equal the argv, for a fixed corpus (quotes, spaces, `$`, backticks, `\`, `*`, unicode) and for a Go fuzz test `FuzzQuote`.
4. **The canary** against the in-process server (`sshtest`) and against the testbed's non-POSIX account.
5. **The tools** with a fake `HostAccess`: groups (a run without hosts does not see them), a host not in the run, `Check` before approval, the replay rule, the stale approval, the output (data line, stdout/stderr, cut, redaction, the `docker inspect` filter incl. unparsable output).
6. **The `sshtest` chain:** the exact command line that arrives (`sudo -n -- ...`, quoting), no login without a pin, the limits (time, output), the lock per host.
7. **Migration 011** and `CreateRunWith` (every old creator's behaviour is pinned first).
8. **The testbed** (`go test -tags sshlive`, like `-tags kind`): real `sudo` with the cycle 1 snippet's lines, quoting through a real `sshd` and shell, the canary refusing a non-POSIX shell, `sudo -n` failing without a rule.
9. **UI** with `node --test`, then a look in a browser (the chip, the run badge, the approval card with a long and an odd command).
10. **A real run** against the testbed with the real CLI, recorded in `docs/research/hosts-2-real-run.md`. It costs subscription quota and is the maintainer's decision, like the earlier real runs.

## 10. Order of work

1. `hostexec` with its tests (nothing else depends on I/O yet).
2. `sshhost`: stdout and stderr apart, the limit parameter, `Service.Exec`, the canary, `ErrShell`.
3. Store: migration 011, `updated_at` on every host change, `CreateRunWith`, `KindHostAction`, `ActivityKind`.
4. The gatekeeper group, `HostTools`, `HostAccess` and its implementation in `internal/app`.
5. Server and UI.
6. The testbed `dev/ssh/` and the live tests.
7. The real run and its record.

## 11. Success criteria

1. A question run started with a host calls `host_list` and `host_read` with a form of the table and gets the output without any approval.
2. `host_read` with anything outside the table, or with a container outside the host's pattern, is refused at once and runs nothing (a test asserts that the SSH test server received no command).
3. `host_change` waits under "Needs you" with the exact command, runs only after the approval as `sudo -n -- ...`, and writes the activity entry; a denied call runs nothing.
4. A never-listed command is refused before an approval is asked for; and a test that puts such a call in the database as approved still does not run it (`Run` checks the list again).
5. A changed host (address, pin, login) after the question was asked makes the approved call refuse to run.
6. No credential appears in a result, a row, an entry, a log or an error; every failure of a connection is a fixed sentence.
7. The quoting survives the real shell test and the fuzz test; the canary refuses a non-POSIX shell on the testbed.
8. A run without hosts is not offered the group, and a call of a host tool by such a run is answered like a tool that does not exist.
9. `make check` passes; the live tests pass on the testbed.

## 12. Risks

| Risk | Decision | Retrofit |
|---|---|---|
| **The table and the quoting are the only barrier Remedy owns** (the umbrella's accepted risk). A wrong form lets `host_read` run something that is not a read, without an approval. | A form is exact words and typed slots, never a pattern over a program. No generic file reader. The heaviest tests of the series. `sudoers` limits the docker forms at the OS level. | Two identities per host: the read account is a login that cannot change anything. |
| **The remote shell parses the line.** Our quoting is correct for POSIX shells only. | The canary runs in every connection and refuses a shell that does not round-trip a hostile string. The real-shell and fuzz tests. | Run through `ssh ... -s sh` style channels, or a fixed `/bin/sh -c` wrapper on the host. |
| **Prompt injection through output.** A log line tells the agent to run `host_change`. | Output is data and fenced; a change needs an approval that shows the exact command; the never-list holds even then. | A second look at a change whose argv contains text copied from output. |
| **Approval fatigue.** A click on a command the maintainer does not read. | One command per approval, word for word, a fixed sentence, a short never-list for the worst cases. | Per-host policies for what may be approved at all. |
| **`docker inspect` and `ps` show secrets** (environment, arguments). | `Config.Env` values are removed; redaction on everything; `ps` has a fixed column list without the command line. | Remove `docker inspect` from the table. |
| **sudoers wildcards** (`remedy-*` also matches spaces and more operands, cycle 1). | The container name is checked here against the pattern as a whole word before it is sent. | Exact lines per container. |
| **The host changes between the question and the run.** | `updated_at` after `RequestedAt` makes the call refuse. | |
| **A command that hangs or prints a lot.** | 30 seconds, 24 KB per stream, one connection per host at a time, the existing per-run call limit. | |

## 13. Changes to other documents

- The umbrella: `host_exec` becomes `host_read` and `host_change` (cycle table, non-goals, section 3, section 5 table, risks), with a pointer here for the decision.
- [`../design.md`](../design.md) 2.10: the same sentence. `CLAUDE.md`: the host credentials bullet names the tools; "Current state" says cycle 2 is specified.

## 14. Open points for the plan

- The exact `HostAccess` interface and where its implementation lives (`internal/app` or a small package).
- The wording of the refusal sentences and of the data line.
- Whether `CreateRunWith` keeps the four old creators as wrappers or replaces their callers in the same cycle (the plan counts the callers).
- The testbed's image (Debian slim with `openssh-server` and `sudo`) and how its host key and logins are generated, in the manner of `dev/kind/up.sh`.
