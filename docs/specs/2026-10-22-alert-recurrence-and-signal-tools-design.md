# Alert recurrence, a source of its own interval, and read tools for alerts and metrics

Status: implemented; a partial real run was made on the home server (2026-10-22, `docs/research/alert-recurrence-and-tools-real-run.md`): the source and `alert_list` are proven, the recurrence, `silence_list` and the metrics tools are not. Comes from a click-through of the deployed 0.14.0 (home server), findings 1 to 3 and 8.

## Why

The first day on the home server showed three problems with the Alertmanager source:

1. **Flapping makes noise.** One alert (`Pi-hole exporter is down`) opened and resolved about 20 times in 24 hours. Every appearance is a new incident (`firing · 1×`), so the list holds 41 near-identical resolved rows, Today says "I opened 42 incidents and diagnosed 3", and each row is a fresh thread with no memory of the earlier diagnosis.
2. **The agent cannot see the signal it diagnoses.** It has `cluster_*`, `host_*` and `incident_*` tools but nothing that reads Alertmanager or the metrics. Diagnoses say "I could not read live CPU metrics"; a follow-up "is the alert still firing?" is answered "I have no tool for that".
3. **One interval for two sources.** `poll_interval` ("How often I check GitHub") also paces Alertmanager. The maintainer set it to 1 h because GitHub is not connected, and so an alert that cleared stays `firing` in Remedy for up to an hour.

Item 8 of the review (the Setup page says "2883 seconds ago") sits on the same Setup block and is fixed with it.

## Goals

- One incident per alert identity. A re-fired alert reopens the incident it had, with its thread and diagnosis, and counts the recurrence.
- The agent can read the live state of alerts and silences and run PromQL queries, with hard bounds, and treats all of it as data.
- Alertmanager has its own polling interval; GitHub's setting no longer paces it.
- Today and the lists say how many distinct alerts there were and how often they fired.

## Non-goals

- No write access to Alertmanager (no silences set, no alerts muted) and none to the metrics store.
- No change to GitHub incidents: a failed check on a new commit already is a recurrence (`RecordRecurrence`).
- No alert grouping by similarity (two different alerts about one pod stay two incidents).
- No new automatic behaviour: a reopened incident is not diagnosed again on its own (see below), and no limit changes.
- Not touched: findings 4 to 7, 9 to 12 of the review; they are separate work.

## Anti-goals

- A reopened incident must never lose or rewrite its diagnosis and history.
- A tool that reads metrics must not become a way to reach anything else: no redirects, no other host than the configured address, no write method.
- A flapping alert must not cost a run each time it returns.

## 1. Recurrence

**Behaviour.** When the engine observes a bad (firing) alert of an `alertmanager` or `argocd` source whose `(source, key)` has no unresolved incident, it looks for the most recent *resolved* incident with the same source and key. If one exists it is reopened; otherwise a new incident opens as today.

- Reopening has no time window: any resolved incident of that identity comes back (maintainer's decision: "always reopen").
- The incident returns to `diagnosed` if it has a diagnosis, `open` otherwise, and an `ignored` incident stays `ignored`. (My call: an ignore is the maintainer's statement that the alert is noise; resolving it by absence must not undo it. See storage.)
- `occurrences` is raised by one and `last_seen` is set; `resolved_at` and the resolve reason are cleared; the title, severity, host and details are refreshed from the new observation.
- One `activity` entry `incident_reopened` is written in the same transaction ("Incident reopened: alert X fired again (12th time)"). `incident_opened` is not written for a reopen.
- The incident's diagnosis stays as the answer; "Diagnose again" works as before.
- **Automatic diagnosis:** a reopened incident is diagnosed on its own only if it was never diagnosed (maintainer's decision: "no, show the existing diagnosis"). This replaces `ReopenQuiet` (one hour after a diagnosis) for alerts: with a reopen, a diagnosis of any age is a reason not to diagnose again. A never-diagnosed incident still obeys the freshness, severity and daily limits of the responder.
- Resolution is unchanged (`ResolveAbsent` after a complete, successful fetch).
- The partial unique index `incidents_active_key` (unresolved rows only) is untouched; reopening moves the single resolved row back, so it cannot create a second unresolved row. If another writer opened an incident for the key in the meantime the reopen returns `ErrExists`, handled as in `OpenIncident` ("the next cycle sees it").
- Existing duplicates (the 41 resolved rows already stored) are left as they are. Only the most recent resolved row of a key is ever reopened. No migration rewrites old rows.

**Storage.** A migration adds `incidents.resolved_while_ignored INTEGER NOT NULL DEFAULT 0` (set by `ResolveIncident` when the incident was `ignored` at that moment; rows resolved before the migration are 0). A reopen goes to `ignored` when the flag is set; otherwise to the state `UnignoreIncident` derives (`diagnosing` while a responder run is queued or running, `diagnosed` if a diagnosis is stored, else `open`). The migration test builds the earlier schema from the migration files with a row in every table that points at `incidents`, as required by the repository's rule for migrations.

**Identity.** The identity is the incident's existing `(source, key)`; for Alertmanager the key is `alertname/fingerprint` and the fingerprint is a hash of the alert's labels. An alert whose labels change (a pod name in `instance`) is another identity and opens its own incident, as before. Merging those would need a stable label subset in the key and a data migration of the key; that is a possible follow-up, not part of this work. A reopen refreshes title, severity, host and details from the new observation.

**Counters on a reopen.** `diagnoses` and `last_diagnosis_at` are kept (the per-incident cap and the cooldown keep counting). `auto_diagnose` is recomputed from the new observation only when the incident was never diagnosed (`diagnosed_at IS NULL`); otherwise it is set to false. A reopen counts as "new" for the per-cycle cap `MaxNewPerCycle` (`ObserveSignal` reports it like an opened incident), so a burst of returning alerts cannot flood a cycle; a reopen over the cap is deferred to the next cycle like a new alert.

**Fields the lists need.** The incident API carries two derived values, computed from the activity entries `incident_opened` and `incident_reopened`: `firings24h` (how many there are in the last 24 hours) and `lastFiredAt` (the latest one). Today's digest and the resolved card ("fired for 3 min last time", resolved time minus `lastFiredAt`) use them; no column is added for them.

**Surfaces.**
- Thread: the "Incident opened" line is followed by a muted line per recurrence ("Fired again 09:59, 11:20, ..."), built from the `incident_reopened` activity entries; the "About this incident" block already shows `Occurrences`, `First seen`, `Last seen`.
- List (`Conversations`): the badge reads `firing · 12×` (it already shows `occurrences`); resolved cards say when the alert last resolved and how long it had fired the last time.
- **Today:** the headline counts distinct alerts and firings: "I saw 6 different alerts fire 42 times in the last 24 hours and diagnosed 3." (Decision: unique alerts plus repetitions.) A day with no recurrence reads as today ("I opened 3 incidents and diagnosed 2."). Distinct = incidents with an `incident_opened` or `incident_reopened` entry in the window; times = the number of those entries.

## 2. Read tools for alerts, silences and metrics

Four tools in a new group `signals`, offered like the cluster group: only to runs that have gatekeeper access and when the source is configured. A run that is not offered a tool gets the answer for a tool that does not exist.

| Tool | Does |
|---|---|
| `alert_list` | The alerts Alertmanager holds now (`GET /api/v2/alerts`): name, state (`active`, `suppressed`), severity, starts at, labels (a bounded subset, values clipped), the silences and inhibitors that apply. Filter by a label matcher string, `active_only` default true, at most 100 rows. |
| `silence_list` | Silences (`GET /api/v2/silences`): matchers, creator, comment, state, ends at. Default `active` only, at most 50. Read only; no silence is ever created. |
| `metrics_query` | A PromQL instant query (`GET /api/v1/query`) with an optional evaluation time. |
| `metrics_query_range` | A range query (`GET /api/v1/query_range`) with `start`, `end` or a `last` duration and a `step`; the server picks the step if it would exceed the point budget. |

Common rules:

- The clients (`internal/alertmanager` for the first two, a new `internal/metrics` for the others) are read-only like the existing ones: `GET` only, no redirects, no proxy, the bearer token opened per request from its sealed setting, answers cut off at a byte limit, a failure reaches the agent as a fixed sentence and is logged with its details. **One exception (decision 2026-10-22):** for an answer from the metrics store whose JSON body says `status: "error"`, whatever the HTTP status (the store refusing a PromQL query), its one-line reason is shown to the agent, because without it the agent cannot fix a syntax error. The reason can quote label sets of series, which exporters write, so it is treated as monitoring data: redacted, clipped to 200 characters (plus "..." when it was cut) on one line with control and bidirectional characters neutralised, quoted, and framed by the note below. Every other failure stays a fixed sentence.
- Bounds (my calls, constants in code, not settings): query text at most 2 KiB; instant and range timeout 10 s; range at most 24 h and 300 points per series (the step is raised to fit); at most 20 series per answer and 40 labels per series, values rounded to 4 significant digits; the whole result goes through `sanitize` (redaction, 32 KB). A truncated answer says so, and by how much where Remedy can know it (series, labels, points, rows left out); an answer the client cut at its byte limit says that the number of missing series is not known.
- The result opens with a fixed note that it is data ("The data below comes from monitoring..."), like `clusterNote`. Label values and annotations are written by whoever defines alerts and can say anything; they are never instructions. Annotations are shown clipped and only for `alert_list`.
- The agent writes the PromQL freely (maintainer's decision). PromQL is read-only by construction (`/api/v1/query*` have no side effects); the bounds above, the timeout and the series limit are what protect the store.
- Every tool needs a strict `Decode` and a `Check` (the standing rule for tools). `silence_list` and `alert_list` need only the Alertmanager address; the metrics tools need the new metrics address. A tool whose source is not configured is not offered.
- The prompts that list tools (`prompt.Outage`, the question frame, the learner is not affected: its allowlist stays by name) get one sentence each saying the agent can ask whether an alert still fires and what the metrics show. The responder prompt for an outage names `alert_list` first: a diagnosis checks that the alert still fires before it explains it.
- A learner run is not offered these tools (its allowlist is by name and stays `host_list`, `host_read`, `skill_get`).

**New setting, new egress.**
- `metrics_url` (group "Metrics", label "Metrics address", env `REMEDY_METRICS_URL`, validated like `alertmanager_url`) and `metrics_token` (sealed secret, like `alertmanager_token`). A bootstrap `REMEDY_METRICS_CA_FILE` mirrors the Alertmanager one. Empty: the metrics tools are not offered.
- Chart: `networkPolicy.metrics` with the same shape and validation as `networkPolicy.alertmanager` (`namespace`, `podSelector`, `port`), one egress rule on the remedy-server policy. The values and the render tests get the same treatment as in cycle 6. Documented in `docs/runbook/alertmanager.md` (a new section) and the home-server wrapper values (outside this repository; done by the maintainer's own PR).
- The address may carry a base path (`https://host/select/0/prometheus`); the client joins `/api/v1/...` to it. Authentication is a bearer token only; basic auth is not supported.
- `GET /api/signals` gets a second entry for the metrics source (state `off|ok|error|pending`, last check, the error text) from a small probe loop of its own (`/api/v1/status/buildinfo`, which VictoriaMetrics and Prometheus both serve), paced by `alertmanager_poll_interval` but never faster than once a minute. It runs without an Alertmanager. The Setup block shows it like the Alertmanager line.
- A tool is registered always and offered only while its source is configured (`Tool.Available`), because the addresses are settings that change without a restart.

## 3. Interval of its own

- New setting `alertmanager_poll_interval` (group "Polling", label "How often I ask Alertmanager", env `REMEDY_ALERTMANAGER_POLL_INTERVAL`, minimum 10 s, default 1 m).
- `poll_interval` is relabelled "How often I check GitHub" with the sentence "It is also how often I ask Alertmanager" removed. The signals runner and `AlertFresh` (`max(10m, 3 × interval)`) read the new setting.
- **Migration of the stored value (maintainer's decision: inherit).** A migration copies an existing `poll_interval` row into a new `alertmanager_poll_interval` row when the latter is absent, so the home server keeps its 1 h until the maintainer lowers it. Settings in the database that were never set stay unset (the default applies). This is a data-changing migration and gets a test on a database built from the earlier migrations.
- **Environment.** When `REMEDY_ALERTMANAGER_POLL_INTERVAL` is not set, the default of the new setting is the value of `REMEDY_POLL_INTERVAL` (a deployment that paced both by the environment or by the chart keeps its behaviour). The chart passes the new variable only when its own value is set.
- The Setup page: the Alertmanager line reads "I asked Alertmanager 48 min ago and it listed 1 active alert." (item 8): the duration is formatted in minutes and hours from 60 seconds on, never as 2883 seconds. The rule text of the new setting says "Alerts that clear stay open for up to this long." (My call: a static sentence rather than a hint that appears above a threshold.)

## Constraints that carry over

- Everything that reaches the agent from monitoring is untrusted data: the tools' results are framed, redacted, bounded.
- The metrics and Alertmanager credentials belong to the control plane alone: never in a log, an error, an answer or a database column in clear.
- A `signals` tool needs a decision in this document before it gets a write method; there is none.
- The gatekeeper stays the only way an agent acts; the runner holds nothing new.

## Parts of the how left to the builder

- Exact JSON shape of each tool's answer and its text form, within the bounds above.
- The wording of the activity entries, the Today headline and the Setup text (English, in the existing voice).
- Whether the recurrence line in the thread shows all times or "and 8 more" beyond a few.
- Names of the Go packages and files, test layout, the fake servers for tests (`alertmanagertest` already exists; a `metricstest` fake of the two query endpoints is new).

## Testing and verification

- Go tests first for: the engine's reopen (reopened state per prior state, ignored stays ignored, `ErrExists` race, `occurrences`, no `incident_opened`), no automatic diagnosis for a diagnosed reopen and one for a never-diagnosed, the migrations (both, on databases built from the earlier files), the settings (bounds, env layer, the inherit copy), each tool (decode, bounds, sanitising, framing, a result that tries to instruct the agent, not offered when unconfigured, not offered to a learner), the metrics client (no redirect, GET only, token never in an error).
- `internal/app` chain tests: a flapping alert over several cycles gives one incident with the right count; the signals runner uses its own interval.
- UI: the pure modules (`conversation.ts`, `setup.ts` and the Today text) get `node --test` tests; a browser check against the fake Alertmanager (`dev/fakealertmanager`; the in-test `alertmanagertest` serves the silences) and a fake metrics server, walked in Chrome.
- A real run with the real CLI against the home server's Alertmanager and VictoriaMetrics after the release (`docs/research/`), as the previous cycles did. It was made in part on 2026-10-22 (the Alertmanager source and `alert_list`); the rest (a returning alert, `silence_list`, the metrics tools) is still open.

## One pull request

The maintainer chose one PR for the whole. The plan therefore orders the work so that each part is a commit that passes `make check` on its own: interval and Setup text (part 3), recurrence (part 1), clients and tools (part 2), chart and docs.
