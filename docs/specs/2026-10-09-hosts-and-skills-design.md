# Hosts, skills and configuration in the UI

Status: draft for the maintainer's review, 2026-10-09. This is the umbrella specification of six cycles. Each cycle gets its own spec and plan before it is built;
this document fixes the decisions they share and the order.
Parent documents: [`../design.md`](../design.md) (2.2, 2.5, 2.7 and the roadmap),
[`2026-10-05-phase-2d-signals-design.md`](2026-10-05-phase-2d-signals-design.md) (the Alertmanager source and the responder for outages, which this narrows and
reorders) and [`2026-10-04-phase-2c-cluster-design.md`](2026-10-04-phase-2c-cluster-design.md) (the two cluster identities this copies the pattern of).
The decisions come from a planning session on 2026-10-09 in which the maintainer's home server (k3s, the UGREEN NAS) was read, read-only, over SSH.

## 1. Goal and scope

Remedy has so far been an operator for one Kubernetes cluster and a set of GitHub repositories. The maintainer wants it to know the whole home lab: machines that run
outside the cluster (the NAS, the host of the cluster itself), the credentials to reach them, and what is known about each of them. Almost everything is to be
configured in the web UI.

| Cycle | Content | Result |
|---|---|---|
| 1 | Hosts and access: inventory, credentials, host key pinning, connection test | A host can be created, trusted and tested in the UI; no agent uses it yet |
| 2 | Host tools in the gatekeeper: `host_exec`, classification, approvals, an SSH testbed | A question run reads a host; an action waits for an approval |
| 3 | Settings in the database: a settings layer with the environment as defaults, the Setup page | Limits, URLs and rules are changed in the UI |
| 4 | The Alertmanager source and the responder for outages, with the host tools | An alert becomes an incident, is mapped to a host and diagnosed |
| 5 | Skills and the learning run: skills per host, `skill_get`, a review list | Remedy gets to know a host and keeps what the maintainer confirms |
| 6 | Operation on the home server: Remedy under Argo CD on k3s, a real run on the NAS | The drill of section 10 runs end to end |

The Argo CD source of part 2D is **not** in this series. It is postponed, not dropped; nothing in the series blocks it.

### Non-goals

- Hosts as a graph (services, dependencies, topology): that is phase 3 and builds on the inventory, it does not replace it.
- An agent that can take a shell. Agents get `host_exec` and nothing else on a host.
- Credentials created or rotated by Remedy on a host, other than showing a snippet. Remedy does not log in as an administrator to set itself up.
- A path filter in Remedy (section 5). Remedy does not know which files are sensitive; the host's file permissions do.
- Notifications, a fixer for hosts, writing to Alertmanager, Loki, the Argo CD source, several users.
- Moving the cluster identities (the read and write token files, the CronJob that mints the write token) into the database (section 8).

## 2. Decisions

| Topic | Decision |
|---|---|
| Inventory | **A flat list of hosts.** An entry is a machine or a system (the home server, the NAS). It has access methods and rules that map alerts to it. Services such as Paperless or k3s are not entries; what is known about them is the content of a host's skills. |
| How a command runs | **One identity per host.** The agent passes an **argv array**, never a shell string. Remedy runs it without a shell. **Default deny:** only programs and flags in a built-in table count as reading; everything else is a change and waits for an approval. A change runs as `sudo -n`. |
| The table | The table of read-only commands is **in the Go code**, versioned and tested. Only a person can add to it for one host, in the UI. A skill, whether written by a person or proposed by an agent, can never extend it. |
| Never | A short list in the code that runs **even with an approval**: `mkfs`, `dd` onto a device, `rm -r` on `/` or a system path, changes to `authorized_keys`, `sudoers` and passwords, and anything that pipes into a shell. |
| Approvals | One per command, shown word for word, with the expiry rules of part 2D (15 minutes for an automatic run). |
| Credentials | The maintainer **enters** a private key or a password. They are sealed like the GitHub token, shown only as their last characters, and never in a log, an error, an API answer or a database column. |
| Host keys | **Pinned.** At the first connection Remedy shows the fingerprint, the maintainer confirms it, and from then on a different key is refused. There is no way to switch the check off. |
| Account rights | The connection test shows what the account can do (`id`, groups, `sudo -n -l`) and **warns clearly** about root and about unrestricted sudo. It does not forbid them. |
| Sensitive data | **Only the host's file permissions.** Remedy has no path filter. The redaction of secrets stays. |
| Where SSH runs | In the **control plane**, in the gatekeeper. The runner never holds a credential and never opens a connection to a host. |
| Which hosts a run sees | Only those assigned to it. The responder gets the host an alert maps to; an ad-hoc run gets the hosts the maintainer selects. A host tool that is not offered answers like a tool that does not exist. |
| Alert to host | Each host has label matchers. The default matches the host's **name and its address** in the `instance` label (on this lab the NAS is `instance="ugreen-nas"`, not its IP). The matchers can be edited. An alert that matches no host still opens an incident, without a host. |
| Skills | A skill is a Markdown document with a name and a description, bound to a host or global. The prompt lists names and descriptions; `skill_get` loads the content on demand. Every skill records its **origin**: written by the maintainer, proposed by an agent and confirmed, or proposed and not confirmed. |
| Learning | A button "get to know this host" starts a read-only run over SSH. The agent proposes a host profile and skills; the maintainer confirms or rejects them in a review list. **Unconfirmed proposals are not used as fact.** Learning after each incident is a later cycle. |
| Configuration | **Bootstrap stays in Helm and the environment:** admin password, runner token, master key, the listener addresses and the cluster token files. **Everything else moves to the database:** hosts, credentials, skills, the Alertmanager URL and token, the limits of diagnosis, the poll interval, the run timeout, the approval expiry, the severities that open an incident, the matcher rules and the namespace allowlist. The environment stays as the **default**; the UI shows where each value comes from. |
| Order | 1 hosts, 2 host tools, 3 settings, 4 Alertmanager and the responder, 5 skills and learning, 6 operation on the home server. |

## 3. How a host command is judged

This is the one place where a mistake means an action without an approval, so it is spelled out.

1. The agent calls `host_exec` with `host` and `argv`. `argv` is an array of strings. There is no field for a shell string and no way to say "through a shell".
2. The tool's strict `Decode` rejects a program name that contains a path separator or a space, an empty argv, an argument with a newline or a NUL byte, and an argv over a fixed length.
3. **Classification.** The built-in table maps a program and its arguments to `read` or nothing. A table entry says which flags and subcommands are allowed, not only which program: `docker ps -a` is a read, `docker run` is not, and `find` is not in the table at all because of `-exec`. Anything that is not exactly described is a **change**. There is no third state.
4. A **read** runs without an approval. A **change** is run by `sudo -n <argv>` and waits for an approval. The "never" list is checked first and for both classes.
5. The command runs through an SSH session without a pty and without a shell: the argv is quoted for the remote side by one function that is tested against the quoting rules of a POSIX shell, because OpenSSH hands the line to the user's shell.
6. Timeout, the number of bytes read and the number of concurrent sessions per host are fixed. The result goes through `sanitize` (redaction, 32 KB) and opens with a note that it is data from a host.
7. Every call is a `tool_calls` row and an activity entry, like every call of the gatekeeper. The text of the command is stored; the credentials never are.

The privileges of the account are the second barrier and the one that does not depend on this code. That is why the connection test reports them (section 5) and
why the onboarding snippet of a host is a `sudoers` file with exact lines (section 4), not a group membership.

A host whose account may use `docker` through `sudo -n` only for exact commands can show a container's state and logs for the containers the `sudoers` file names, and nothing else. For the NAS the drill of section 10 uses `remedy-*`: the Paperless containers are not visible to the account.

## 4. Cycle 1: hosts and access

**Data.** A `hosts` table (name, address, port, user, notes, matchers as JSON, `enabled`) and a `host_credentials` row per host (kind `key` or `password`, the sealed secret with the row id bound in as additional data, the last four characters, a pinned host key fingerprint, the time of the last successful test). The exact schema is for the cycle's spec; the rules are these:

- The sealed value uses `internal/secret`, with the key from `REMEDY_MASTER_KEY`. A value of `secret.Value` has the `***` text form.
- Reading a credential back out of the API is not possible. `PUT` replaces it; `GET` shows kind, last four characters and the fingerprint.
- Deleting a host deletes its credentials and unbinds it from the incidents and runs that name it (the rows stay, the reference is cleared).

**UI.** A page "Hosts" (a section of Setup or a page of its own, decided in the cycle's spec). A host has a form, the **onboarding snippet**, a **connection test** and the host key confirmation.

The onboarding snippet is text for the maintainer to read and run on the host. It names the account to create, the line for `authorized_keys` (the public key derived from the private key the maintainer entered; a password needs no such line), and a `sudoers` file with **exact** lines. For the NAS:

```
remedy ALL=(root) NOPASSWD: /usr/bin/docker ps -a
remedy ALL=(root) NOPASSWD: /usr/bin/docker logs --tail * remedy-*
remedy ALL=(root) NOPASSWD: /usr/bin/docker inspect remedy-*
remedy ALL=(root) NOPASSWD: /usr/bin/docker start remedy-*, /usr/bin/docker restart remedy-*, /usr/bin/docker stop remedy-*
```

The snippet is a suggestion generated from the host's kind; it is never run by Remedy. The cycle's spec says how the lines are chosen and shows the known limits of wildcards in `sudoers`.

**Connection test.** Connects, shows the fingerprint when none is pinned, and after confirmation runs a fixed set of reads (`id`, `groups`, `sudo -n -l`, `uname -a`) and reports: reachable, host key matches, who the account is, its groups, whether `sudo -n` works and for what. It **warns** when the account is root, when it is in the `docker` group, or when `sudo -n -l` shows `(ALL) NOPASSWD: ALL`. The test is not an agent run and does not use `host_exec`.

## 5. Cycle 2: host tools in the gatekeeper

The tool group `host`, offered only to runs that have hosts assigned (`runs.hosts`, like `runs.cluster`):

| Tool | Class | What it does |
|---|---|---|
| `host_list` | read | The hosts assigned to this run: name, notes, kind. No addresses, no credentials. |
| `host_exec` | read or change | Section 3. |

The runner needs no change: the tools are served by the same MCP server, and the claim already carries what a run may use. A **testbed** (`dev/ssh/`): a container with `sshd`, an unprivileged user, a `sudoers` file and a fake `docker`, started by a script like `dev/kind/up.sh`, so that the whole chain is tested without the maintainer's machines. Its login keys are generated on start and removed with it.

The tests of this cycle are the heart of the series and are written first: a table-driven test of the classification with every attack the session named (`; rm`, `$(...)`, backticks, redirects, `find -exec`, `docker run -v /:/h`, `journalctl --output`, an argument that looks like a flag, a program given with a path), the quoting function against a real shell, the "never" list, and the replay rule (a repeated tool call is never a second approval or a second execution).

## 6. Cycle 3: settings in the database

A `settings` table of keys and JSON values, a typed registry in the code (a key, its type, its default from the environment, whether it is secret, whether it needs a restart), and a `Settings` reader that every component asks instead of reading the environment. A change in the UI takes effect without a restart where the component reads the value per cycle or per request; where it cannot, the UI says so. Secret settings (the Alertmanager token) use the same sealing as the host credentials.

The Setup page shows each value with its source (`default`, `environment`, `set here`) and lets the maintainer change what is allowed to change. The bootstrap values are shown as read-only facts, not as fields.

This cycle changes how the pollers, the responder limits and the cluster allowlist read their configuration. The cluster **token files** do not move: the write identity is minted by a CronJob and expires by design (section 2.7 of the design), and a token in the database would give that up. Cycle 3 rewrites the sentence "Configuration: environment variables. No UI for it." of part 2D.

## 7. Cycle 4: the Alertmanager source and the responder

This is the part of 2D that the series needs: `internal/alertmanager` (a read-only client with the transport rules of `internal/kube`), `internal/signals` and the poll runner, the Alertmanager source, and the responder for outages. Its decisions stay as in
[`2026-10-05-phase-2d-signals-design.md`](2026-10-05-phase-2d-signals-design.md), with these changes:

- The Alertmanager URL, token and severities come from the settings of cycle 3, not from environment variables only.
- An incident gets a nullable `host_id`, set when an alert's labels match a host's matchers. The first matching host wins and a tie is shown, not guessed.
- The responder run gets the host's tools (cycle 2) next to the cluster tools, for the host the alert maps to.
- The Argo CD source and its tests are **not** built. The `Source` interface stays so that it can be.

On this lab, Alertmanager has a single receiver `blackhole`. It still holds the firing alerts, and Remedy only reads them, so nothing in the home-server repository has to change for the source. What does have to change is one rule (section 10).

## 8. Cycle 5: skills and the learning run

**Skills.** A table of skills (name, description, body, host or global, origin, state, the time and the run that proposed it). The body is Markdown shown as text; it is never rendered as markup in the UI and never decides the structure of a sentence Remedy "says".

- A skill written by the maintainer is trusted as instructions *for the agent's reasoning*, but it still cannot widen what `host_exec` allows (section 2, "The table").
- A skill proposed by an agent has origin `proposed` and is invisible to runs until confirmed. After a confirmation it carries `proposed, confirmed`, so that a later reader can see where it came from.
- The prompt lists names and descriptions; `skill_get` returns the body, as data, through `sanitize`.

**Learning run.** A run of a new role, the learner for hosts: the host tools, a prompt built in `internal/prompt`, and a strict answer schema (a profile with fixed fields, and a list of proposed skills). The control plane validates the answer; the agent never writes to the database. The review list shows each proposal with the commands that produced its facts. The maintainer confirms, edits or rejects it.

A log line that contains an instruction must not become a skill. The defence is the review list, the origin marking and the table of section 3 that no skill can change; the cycle's spec adds a test with an injected instruction in the output of a host.

## 9. Testing

- **Classification and quoting** (cycle 2): table tests as described, run in the unit tests and against the SSH testbed.
- **Credentials** (cycle 1): a test that no endpoint, log line, error or database column holds a plain secret; `***` text form; the additional-data binding (a sealed value copied to another row does not open).
- **Host keys** (cycle 1, 2): a changed key is refused; a first connection never proceeds without a confirmation.
- **Settings** (cycle 3): a migration test in the manner of `migrate008_test.go` if any existing table changes; a test that the environment default and the database value resolve as the UI says.
- **Alert to host** (cycle 4): matchers against recorded Alertmanager answers, including the real `instance="ugreen-nas"` label.
- **Real runs** (cycle 2 on the testbed, cycle 6 on the NAS), recorded in `docs/research/` like the earlier ones. Real runs cost subscription quota and are run by the maintainer's decision.
- Shell and fake `claude` scripts follow the rules of the repository (GNU first, no BSD-only flags).

## 10. The drill and the success criteria

**The drill (cycle 6).** On the NAS a throwaway container `remedy-drill` runs, with a label. A `VMRule` in the `home-server` repository, merged by the maintainer, fires when it is not running (cAdvisor is already scraped). The account `remedy` exists on the NAS with the `sudoers` lines of section 4, and the NAS is in the inventory with a pinned host key. The maintainer stops the container. Then:

1. the alert reaches Alertmanager; Remedy opens an incident within one poll interval and maps it to the NAS;
2. the responder reads `docker ps -a` and `docker logs` of `remedy-drill` over SSH, without an approval;
3. it asks to run `docker start remedy-drill`; the request appears in "Needs you" with the exact command;
4. after the approval the container runs, the alert resolves, and the incident closes;
5. at no point does the run see a Paperless container, a credential or the NAS's address.

**Success criteria of the series.**

1. A host is created, its key is pinned and its connection tested, entirely in the UI.
2. No secret of a host appears anywhere but the sealed column (a test and a look at the database).
3. The classification test passes with every attack on the list, and a command not in the table never runs without an approval.
4. The drill of this section runs end to end on the maintainer's NAS and is recorded in `docs/research/`.
5. The learning run produces a host profile for the NAS that the maintainer confirms in the review list, and an unconfirmed proposal is shown to no run.
6. The values of section 2 ("Configuration") are changed in the UI and take effect as the UI says.
7. `make check` passes in every cycle.

## 11. Risks

| Risk | Decision | Retrofit |
|---|---|---|
| **The classification is the only barrier** that Remedy owns. A mistake in the table or the quoting lets a change run without an approval, with the rights of the account. | Accepted by the maintainer (one identity per host). Reduced by argv instead of a shell string, default deny, `sudo -n` for every change, a "never" list, and the heaviest tests of the series. | Two identities per host: a read account the system keeps read-only, and a separate account for approved changes. The data model keeps room for it: a host may later have two credentials. |
| **A privileged key entered by the maintainer.** With an administrator's key the account may do everything, and the classification is then the only protection. | The connection test warns. The onboarding snippet recommends an own account `remedy` with exact `sudoers` lines. | Refuse to save a host whose account is root. |
| **No path filter.** What the account can read can reach the model provider in a log or a file. | The host's permissions are the barrier; the snippet and the test show the account's groups. The redaction of secrets stays. | A list of blocked paths, checked on the argv (it does not stop symlinks). |
| **Prompt injection through a host.** A log line, a file name or a container label is text from an untrusted place. | It is data, fenced and redacted. It cannot extend the table of reads. A skill from an agent needs a confirmation. | A second look at an approval request whose command contains text copied from a log. |
| **Confirming a poisoned skill.** The maintainer confirms a proposal whose source was an injection. | The review list shows the commands behind each fact; the origin stays on the skill. | Re-validate confirmed skills after a change to the learner's prompt. |
| **A wildcard in `sudoers`.** `docker logs --tail * remedy-*` allows extra arguments. | The subcommand cannot run a program; the container names limit the reach. The cycle's spec lists each line and its limit. | Exact lines for each container, or a wrapper script owned by root. |
| **Settings in the database change a safety limit** (the diagnosis limits, the approval expiry, the namespace allowlist) by a click. | The UI is behind the admin password only, as everything else (design, section 4). A change is an activity entry. | The existing retrofits: TOTP or OIDC. |
| **The environment as default hides where a value comes from.** | The Setup page shows the source of every value. | None needed. |
| **The series is long.** Six cycles can drift. | Each cycle is built from its own spec and plan, and only within it (the repository's rule); this document changes first if a decision changes. | None. |

## 12. Changes to other documents

- [`../design.md`](../design.md): section 2.2 (hosts as a second kind of access, next to the cluster), section 2.7 (configuration: bootstrap in Helm, the rest in the UI), a new section 2.10 for hosts and skills, and the roadmap (the series between phase 2 and phase 3; the Argo CD source postponed).
- [`2026-10-05-phase-2d-signals-design.md`](2026-10-05-phase-2d-signals-design.md): the row "Configuration" of the decisions table and the status line, which point here.
- `CLAUDE.md`: one line in "Current state". The rest follows with each cycle.

## 13. Open points for the cycle specs

- Cycle 1: whether Hosts is a page of its own; the exact schema; whether a host may have a tag for a group; the kinds of host for the snippet.
- Cycle 2: the first contents of the table of reads (the set is small and grows by a decision recorded with a test); concurrency and timeouts; whether `host_exec` of a host that is unreachable fails the run or only the call.
- Cycle 3: the registry's list of keys and which need a restart; where a failed setting is shown.
- Cycle 4: how a matcher conflict is shown; what an incident without a host gets for tools.
- Cycle 5: the learner's answer schema; how many proposals per run; what happens to a skill when its host is deleted.
- Cycle 6: how the SSH egress rule is written for the chart's NetworkPolicies, and the `VMRule` for the drill in the `home-server` repository.
