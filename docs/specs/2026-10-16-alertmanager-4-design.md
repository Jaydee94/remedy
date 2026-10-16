# Alertmanager, cycle 4: the source and the responder for outages

Status: proposed 2026-10-16. Two implementation plans: **plan 4a (the source) implemented 2026-10-16 (see the pull request of cycle 4a); plan 4b (the responder) not written**. Until 4b, an Alertmanager incident is shown and mapped to a host but never diagnosed (the thread says "I can't diagnose incidents from Alertmanager yet").
Parent documents: [`2026-10-09-hosts-and-skills-design.md`](2026-10-09-hosts-and-skills-design.md) (the umbrella: sections 2, 7 and 11 are binding here),
[`2026-10-05-phase-2d-signals-design.md`](2026-10-05-phase-2d-signals-design.md) (the signals design this narrows: its decisions stand unless this spec changes them),
[`2026-10-14-settings-3-design.md`](2026-10-14-settings-3-design.md) (the settings this builds on: registry, snapshot reader, the page),
[`2026-10-11-hosts-2-tools-design.md`](2026-10-11-hosts-2-tools-design.md) (the host tools the outage run uses),
[`2026-10-04-phase-2ab-gatekeeper-and-approvals-design.md`](2026-10-04-phase-2ab-gatekeeper-and-approvals-design.md) and
[`2026-10-04-phase-2c-cluster-design.md`](2026-10-04-phase-2c-cluster-design.md) (the gatekeeper, the approvals and the cluster tools).

## 1. Goal and scope

An alert that fires in Alertmanager becomes an incident in Remedy within one poll interval. It is mapped to a host when the alert's labels match that host's rules. A young `critical` or
`warning` alert is diagnosed on its own: the run reads the cluster and, for the mapped host, the host, and answers with a strict diagnosis that the maintainer reads. Anything that
would change something still waits for an approval, and an approval that nobody gives for 15 minutes in an automatic run expires, so that one unanswered question never holds the
one runner.

The cycle is proven on recorded Alertmanager answers (including the real `instance="ugreen-nas"`), on the kind and SSH testbeds, and by a real run with the real CLI against them.
Pointing Remedy at the real Alertmanager of the home server is cycle 6.

### Non-goals

- **The Argo CD source.** The `Source` interface stays so that it can be built later.
- **Writing to Alertmanager** (silences, acknowledgements), notifications, Loki, receiving webhooks, several Alertmanagers, correlation across sources.
- **A connection-test button** for Alertmanager. The state line of the source (section 9) shows the result of the last real request.
- **Other signals for the host mapping** than the alert's labels, and a global rule list (the rules live at the host).
- **A fixer** for outages: the run diagnoses and may request an approved action; it changes no repository.

## 2. Decisions

| Topic | Decision |
|---|---|
| Transport | Polling `GET /api/v2/alerts?active=true&silenced=false&inhibited=false`. No inbound port, no new authentication domain. The latency is the poll interval of the settings. |
| Where it runs | A **new runner in `internal/signals` with its own loop** that reads `poll_interval` each round. It does not hang off the GitHub poller: that one skips its cycles without a GitHub connection, and Remedy must work with Alertmanager alone. |
| What is an incident | An alert in the state `active` whose `alertname` is not `Watchdog` or `InfoInhibitor`. The key is `<alertname>/<fingerprint>`. All severities open an incident; `severity` is the label of that name, lower-cased, anything but `critical`, `warning` or `info` is `none`. |
| Complete answers only | After a **complete and successful** fetch the runner resolves the active incidents of the source whose key is not in the answer. A failed, timed-out, cut-off or size-limited fetch resolves nothing: an unreachable Alertmanager is never "all clear". |
| Alert storm | At most **50 new incidents** per cycle; the rest is picked up in the next one. |
| Automatic diagnosis | Decided **once, when the incident opens**: severity `critical` or `warning` **and** `startsAt` at most `alertmanager_auto_max_age` ago (default 1 hour). A long-firing alert stays visible and a click diagnoses it, but it costs no quota on its own. The limits of phase 1 apply (3 per incident, 20 per day, the cooldown). A diagnosed alert is not diagnosed again automatically. |
| Settings | The source is configured in the Setup page (cycle 3): `alertmanager_url`, `alertmanager_token` (**the first secret setting**, sealed, write-only), `alertmanager_auto_max_age`, `auto_approval_timeout`. The environment stays the default for the non-secret ones. The CA bundle is a file path of the pod: `REMEDY_ALERTMANAGER_CA_FILE`, bootstrap, shown as a fact. |
| Host mapping | The rules are **a field of the host**: Alertmanager matchers, one per line, **all must hold**, a host without rules never matches. The first matching host (lowest id) wins; further matches are shown, not guessed. Recomputed at every observation. |
| What the mapping does | Only this: the outage run gets the host tools of that one host. The host tools of cycle 2 keep deciding at every call whether the host is usable. |
| Outage run | A responder run with the gatekeeper's incident tools, the cluster tools when the cluster read side is set up, and the host tools of the mapped host. No snapshot, no GitHub connection needed. Every change waits for an approval as before. |
| Answer | A strict schema of its own (`diagnosis.ParseOutage`), stored in the same `diagnosis` column; the UI picks the card by the incident's source. |
| Approval expiry | A call of a run with `automatic = 1` gets a deadline, `auto_approval_timeout` (default 15 minutes, `0` turns it off). Calls of runs started by a click wait without a limit. |
| Untrusted text | Alert labels, annotations and the host's name are data: cleaned, redacted and bounded by `internal/prompt`, in blocks with a random delimiter, never instructions. |

## 3. Components

| Part | Where | Does |
|---|---|---|
| Client | `internal/alertmanager` (new) | Read-only like `internal/kube` and `internal/github`: every exported method is a `Get*` or `List*` (a test enforces it), the transport refuses every method but `GET` and `HEAD` before it is sent, no redirects, no proxy. `ListAlerts` checks `status.state == "active"` on every alert again. The token is opened per request. |
| Fake | `internal/alertmanager/alertmanagertest` (new) | A fake Alertmanager with recorded answers in the manner of `kubetest`, including the answer of the home server. |
| Signals | `internal/signals` (new) | `Source` (`Name`, `Fetch`), `Signal` (`Key`, `Title`, `Severity`, `Auto`, `Details`, `URL`, `SeenAt`), the `Runner` (own loop, one source today), the source state. |
| Matchers | `internal/alertmatch` (new, pure) | `Parse(text) ([]Matcher, error)` and `Matches(rules, labels) bool` for the Alertmanager syntax. No I/O. |
| Settings | `internal/settings` | Two new kinds, **text** and **secret**, four new keys, a `Secret(key)` accessor, sealing with the master key (section 4). |
| Store | `internal/store` | Migration 013, `hosts.matchers`, `incidents.host_id`, `tool_calls.expires_at`, `StartParams` with `Cluster` and `HostIDs`, `ListAutoCandidates` for every source, expiry of a call, `KindApprovalExpired`. |
| Engine | `internal/incident` | The observation carries the `host_id`; every touch updates it. |
| Responder | `internal/responder`, `internal/prompt`, `internal/diagnosis` | The branch by source, `prompt.Outage`, `diagnosis.ParseOutage`, `CheckOutcome` by source. |
| Gatekeeper | `internal/gatekeeper` | The deadline of a call in an automatic run and its end as `abandoned`. |
| Claim | `internal/server` | `claimFor` picks the schema and the snapshot flag by the incident's source. |
| Server | `internal/server` | `GET /api/signals`, `matchers` on the host routes, `hostId`, `hostName` and `alsoMatches` on the incident routes, the expiry time on an approval. |
| UI | `web/` | The Alertmanager block in Setup, the matcher field on the host page, the host on incidents, the outage card, the expiry on an approval. |
| Testbed and real run | `dev/`, `docs/research/phase-2d-real-run.md` | A fake Alertmanager for the testbeds and the recorded real run. |

## 4. Settings

| Key | Kind | Environment (default) | Bounds | Takes effect |
|---|---|---|---|---|
| `alertmanager_url` | text | `REMEDY_ALERTMANAGER_URL` (empty: the source is off) | `http` or `https`, a host, **no userinfo, no query, no fragment**, at most 2048 characters, a trailing slash is removed | next cycle |
| `alertmanager_token` | secret | none | 1 to 4096 **bytes**, no control character | next request |
| `alertmanager_auto_max_age` | duration | none (1 h) | at least 1 m | when an incident opens |
| `auto_approval_timeout` | duration | `REMEDY_AUTO_APPROVAL_TIMEOUT` (15 m) | `0` (off) or at least 1 m | when a call starts waiting (registered in plan 4b, with its consumer) |

**The secret kind.** The stored value is JSON `{"sealed": "<base64>", "hint": "…abcd"}`; the sealing is `secret.Key.Seal` with the additional data `settings/<key>`, the same AES-256-GCM as the host
credentials. The reader needs the master key and keeps the sealed bytes and the hint in the snapshot, **never the plaintext**; `Secret(key)` opens it on demand and returns a `secret.Value`
(its text form is `***`, so it cannot reach a log or an error). `GET /api/settings` shows a secret with `kind: "secret"`, `value: null` and a `hint`. `PUT` takes `{"value": "<text>"}` once;
`DELETE` removes it. The **activity entry of a secret change names the key only**, never a value or a hint. A sealed value that cannot be opened (a changed master key) is a `problem` on the row,
as in Setup for the GitHub token, and the source reports `error`.

The URL carries no credential by design: the token is the only secret, so the Setup page can show the URL.

## 5. Alerts become incidents

**The request** has a size limit of 8 MB; an answer that is not a JSON array of alerts is an error, never a partial list. The client sends `Authorization: Bearer` when a token is set.

**The signal.** `title` is the annotation `summary`, else the alert name with namespace and pod when they exist, one line of at most 200 characters. `details` holds `labels`, `annotations`, `startsAt`,
`generatorURL` and the fingerprint, bounded before they are stored: at most 30 labels (key at most 64 characters, value at most 300) and 20 annotations (value at most 1000), redacted with `internal/redact`.
`generatorURL` is kept only as an `http` or `https` address (anything else becomes empty), redacted and then cut to 2000 characters. An alert without a name or a fingerprint, with a name over 200 or a fingerprint over 64 characters is skipped
(the key goes into the database as it is). A label or annotation key over 64 characters, or one that redaction would change, is dropped. `details.truncated` says that something was left out.
They are shown as text and go into the prompt as data, never as markup.

**The cycle**, once per `poll_interval`, with the URL and token of that moment:

1. fetch; on failure log once per cycle, set the source state to `error` with a bounded message, resolve nothing;
2. for every signal: map it to a host (section 6), then `Observe` (the engine opens, touches or re-opens; an ignored incident stays ignored; the whole cycle is idempotent);
3. after a complete fetch resolve the active incidents of the source whose key is not in the answer.

**The state of the source** (`ok`, `error`, `off`, the time of the last attempt, the number of active alerts, the error) is kept in memory like the runner status and is answered by `GET /api/signals`.
An empty URL means `off` and nothing runs.

## 6. Alert to host

**Data** (migration 013): `hosts.matchers TEXT NOT NULL DEFAULT ''` and `incidents.host_id INTEGER REFERENCES hosts (id) ON DELETE SET NULL` (a deleted host leaves the incident without a host).

**The rules.** One matcher per line in the Alertmanager syntax: `label="value"`, `label!="value"`, `label=~"regex"`, `label!~"regex"`; the value is quoted, a regex is RE2 and anchored to the whole value, a missing label counts as the empty
string (as in Alertmanager). **All lines of a host must hold**; a host with no lines never matches. The server checks on `PATCH /api/hosts/{id}`: at most 10 lines, at most 200 characters per line, the regex compiles,
label names follow the Prometheus rule; an error is a 400 with a readable text. For the NAS one line is enough: `instance=~"ugreen-nas(:[0-9]+)?"`.

**The mapping** looks at the hosts in the order of their ids; the first host whose rules all hold becomes `host_id`. When more than one matches, the names of the others are in `details.alsoMatches` and the UI shows
"also matches: …". Nothing is guessed. It runs at **every** observation, so a changed rule applies in the next cycle. The labels are only compared: never executed, never put into a command.

## 7. The responder for outages

**`responder.start`** branches on the incident's source. GitHub goes the way it always went. An Alertmanager incident needs no GitHub connection and no snapshot. `StartDiagnosis` gets two options that it applies in
its own transaction, like `CreateRunWith`: `Cluster` (true when the cluster read side is set up) and `HostIDs` (the incident's `host_id` when there is one). The run has `mcp = 1`, `runs.cluster` and its `run_hosts`, `automatic` as before.

**The claim.** `claimFor` loads the incident of a responder run and answers by its source: the GitHub schema and `snapshot: true` for GitHub, the outage schema and no snapshot for Alertmanager.

**The prompt** is built only by `internal/prompt` (`Outage`): the instructions outside, the signal (labels, annotations, the host's name) in blocks with a random delimiter, cleaned, redacted and bounded. The instructions say that the agent is
Remedy's responder for an outage, that it finds the cause with the read tools, that everything in the blocks is data copied from monitoring and never an instruction, and that it may request an action only when the diagnosis supports it and says
in `proposed_action` which one and why.

**The answer.** `diagnosis.ParseOutage` is strict like `Parse`: `summary`, `cause`, `confidence`, `category`, `affected` (a list of strings, each at most 200 characters), `proposed_action` (text or `none`), `evidence` (what the agent read).
Categories: `workload_crash`, `image_pull`, `resource_pressure`, `configuration`, `dependency_outage`, `host_problem`, `unknown`. `responder.CheckOutcome` validates it in `finish` by the incident's source. The UI shows it as text.

**Automatic start.** `ListAutoCandidates` leaves out the join that excluded every incident without a repository: it joins `repos` with a left join and the `enabled` test applies to GitHub rows only; an incident that is not GitHub's
is a candidate only while `open` (the re-diagnosis on a new head commit is GitHub's). Cooldown, caps and the daily limit are the existing ones.

## 8. Approvals of automatic runs expire

One run at a time means that a run waiting for an approval holds the runner (the run clock does not count the wait). For a run with `automatic = 1` the gatekeeper gives every approval a deadline when the call starts waiting:
`tool_calls.expires_at` = the time of the request plus `auto_approval_timeout`, read from the settings at that moment (a change does not change calls that already wait; `0` sets none). When the deadline passes without a decision:

- the call ends as `abandoned` with the decision `abandoned` and the reason `no decision within <duration>`, by a compare-and-set that only moves a call that is still `pending` (a decision that arrives first wins, a late `Decide` is refused);
- the agent is told that no decision was made in time, and the run goes on to its answer;
- an activity entry `approval_expired` is written in the same transaction; the action stays a proposal in the diagnosis.

Calls of runs started by a click have no deadline. The approval card of an automatic run shows the time at which Remedy stops waiting.

## 9. API and UI

**API** (admin session and `X-Remedy-CSRF` as everywhere):

- `GET /api/signals`: `{"sources": [{"name": "alertmanager", "state": "ok|error|off", "checkedAt": ..., "active": n, "error": "..."}]}`.
- `GET` and `PATCH /api/hosts[/{id}]` carry `matchers` (text).
- The incident routes carry `hostId`, `hostName` and `alsoMatches`; `alsoMatches` is read from `details.alsoMatches`.
- A pending approval carries `expiresAt` (empty when it has none).
- The settings routes carry the new keys and the secret kind (section 4).

**UI** (conventions of the pages: `aria-disabled` plus a guard, focus, live region):

- *Setup*: a block "Alertmanager" with the URL, the token (write-only: an empty field with the hint of the stored one and "Replace" and "Remove", like the GitHub token), the maximum age and the state line ("I asked 12 seconds ago and
  it listed 7 active alerts" or the error). `auto_approval_timeout` is in the group "Runs". The CA path is a fact.
- *Host page*: the field "Alerts of this host" with the server's message for an invalid rule.
- *Incidents*: the host as a chip, "also matches: …", the outage diagnosis as a card of text. Expired approvals appear in "Needs you" history as "no decision in time".

## 10. Failure

- A fetch error, a size limit, a non-array answer, an invalid URL, a token that cannot be opened or a host list that cannot be read (the mapping needs it): state `error`, nothing resolves, one log line per cycle.
- A rule that stops compiling (it cannot, the server checks it) or a host row with an unreadable rule text is skipped for the mapping and the host's name goes to the log once.
- A host that is deleted or disabled between the start of a run and a tool call: the host tool answers as in cycle 2.
- A run whose cluster read side is off still runs with the incident tools (and the host's, if mapped).
- The control plane restarting abandons every waiting call (`AbandonAllWaiting`, as before); a deadline is not re-armed.

## 11. Tests

Written first, in this order of weight:

1. **The expiry**: the deadline exists only for automatic runs and only with a non-zero setting; a call ends as `abandoned` with the reason; the activity entry is in the same transaction; the agent is told; the run goes on to its answer; a click-started run never
   expires; a changed setting does not touch a waiting call; a decision racing the expiry has exactly one winner.
2. **The matcher** as a table: every operator, quoting and escapes, the anchored regex, a missing label, all-lines-must-hold, an empty rule set never matches, every invalid input.
3. **The client** against `alertmanagertest`: the transport refuses every other method, the size limit, a non-array answer, `status.state` checked again, the token opened per request and never in a log or an error, no redirect followed.
4. **The runner**: opens, touches and resolves; a failed or cut-off fetch resolves nothing; the cap of 50; `Watchdog` and `InfoInhibitor` skipped; the automatic decision for a young and an old alert and for the severities; a second cycle changes nothing;
   an empty URL runs nothing.
5. **The host mapping**: first match by id, the tie shown, recomputed at each observation, a deleted host clears it.
6. **The secret**: sealed, the plaintext never in a response, an activity entry, a log, an error or the database (a walk over every answer as the host tests do), a wrong master key is a problem.
7. **Migration 013** on a database built from the earlier migration files, with a row in every table that points at `hosts` or `incidents`.
8. **The responder**: the branch, no snapshot, the run options (`cluster`, `run_hosts`, `mcp`), the claim's schema by source, `ParseOutage` strict, `CheckOutcome` by source, an alert annotation that tries to instruct stays inside its block, `ListAutoCandidates` for every source.
9. **A chain test** in `internal/app`: a recorded alert becomes an incident, is mapped to a host, an automatic run starts with the right tools, an approval expires.
10. Pure web modules with `node --test`, a browser check of Setup, the host page and an incident, and **the real run** with the real CLI against the testbeds (costs quota, not in CI), recorded in `docs/research/phase-2d-real-run.md`.

## 12. Order of work

Two plans, each ending with `make check` and a green CI; the second builds on the first.

1. **4a, the source.** The secret kind and the text kind in the registry, the three keys `alertmanager_url`, `alertmanager_token` and `alertmanager_auto_max_age`, `internal/alertmanager` and its fake, `internal/alertmatch`, `internal/signals` and its runner, migration 013 (`hosts.matchers`, `incidents.host_id`),
   the host routes and page, `GET /api/signals`, the Setup block. At the end alerts are incidents, mapped to hosts, and nothing diagnoses them yet.
2. **4b, the responder.** `ListAutoCandidates` for every source, `StartDiagnosis` options, the claim by source, `prompt.Outage`, `diagnosis.ParseOutage`, the expiry of approvals (`tool_calls.expires_at`) with its setting `auto_approval_timeout`, the outage card, the testbed extension and the real run.

## 13. Risks

| Risk | Handling | Retrofit |
|---|---|---|
| **Alert text is untrusted** (anyone who can name a pod or write a rule influences it) and goes to an agent. | Data only: cleaned, redacted, bounded, in blocks with a random delimiter; the strict answer; every change waits for an approval. | none needed |
| **An alert storm** fills the list and burns quota. | 50 new incidents per cycle, the daily limit, one run at a time, and automatic diagnosis only for young `critical` and `warning` alerts. | A per-label rate limit |
| **An unreachable Alertmanager** is read as all clear. | Only a complete fetch resolves. | none needed |
| **A wrong host mapping** gives an agent the tools of the wrong host. | The first match wins and ties are shown; the host tools allow reads from a small table and every change only after an approval. | A required confirmation of the mapping |
| **The token is a secret in the database.** | Sealed with the master key, write-only, shown as a hint, never in an activity entry. | none needed |
| **An unanswered approval holds the runner.** | The expiry of automatic runs (15 minutes). | A per-tool expiry |
| **A matcher regex costs time.** | RE2 (linear), at most 10 lines of 200 characters per host. | none needed |
| **The URL setting makes the control plane connect to any address** (the same oracle as the host test). | Admin only; the client sends a read-only `GET` of a fixed path; no redirects. | An allowlist of networks |

## 14. Documents to change with the implementation

`docs/design.md` (sections 2.3, 2.7 and 2.10 and the roadmap paragraph for the signals), the 2D spec (status table and the "Configuration" row: the environment names are defaults; the Argo CD source stays postponed), the umbrella spec (section 7 built, section 13 open points answered),
`CLAUDE.md` (Current state, the Commands line for the testbed, the environment variables), the README, and a runbook `docs/runbook/alertmanager.md` for connecting a real Alertmanager.

## 15. Open points for the plans

- 4a (answered when it was built): the wire form of the secret setting is `kind: "secret"`, `value: null` and a `hint` (section 4); the state of the source lives in memory in `internal/signals` (`Runner.States`, which `GET /api/signals` answers; a restart forgets it and the next cycle fills it in; before the first attempt the state is `pending`); the runner's loop (`Runner.Run`) is started by `cmd/remedy-server` with the process context and ends with it.
- 4b: how `StartDiagnosis` applies `HostIDs` for a host that was deleted in between; the wording of the outage prompt; the testbed's fake Alertmanager (a container or a Go test server).
- 4b, found in the final review of 4a:
  - (a) Incidents of a source that is switched off, or whose address changed, stay open and keep `auto_diagnose`. The automatic candidates of 4b must be limited to recently seen incidents, or such incidents must be resolved when the source is switched off.
  - (b) A touch refreshes the host and the details only, not the title, the severity or the URL of an incident. Decide in 4b whether it should.
  - (c) A host whose rules are only negative or can match the empty value maps every alert to the lowest host id (a missing label counts as empty).
