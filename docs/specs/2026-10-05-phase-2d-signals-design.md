# Phase 2 (part D): signals and the responder for outages

Status: draft for the maintainer's review, 2026-10-05. Implementation plans: [`phase-2d-1`](../plans/phase-2d-1-incident-model.md), implemented; 2d-2 and 2d-3 are not written yet (see 10).
Parent documents: [`../design.md`](../design.md) (sections 2.3, 2.4 and the roadmap),
[`2026-10-02-phase-1-detect-and-diagnose-design.md`](2026-10-02-phase-1-detect-and-diagnose-design.md) (incidents, the poller, the responder),
[`2026-10-04-phase-2ab-gatekeeper-and-approvals-design.md`](2026-10-04-phase-2ab-gatekeeper-and-approvals-design.md) (the gatekeeper and the approvals) and
[`2026-10-04-phase-2c-cluster-design.md`](2026-10-04-phase-2c-cluster-design.md) (the cluster tools and the kind testbed this builds on).

## 1. Goal and scope

Remedy notices outages by itself. Two new sources feed the incident engine next to GitHub: **Alertmanager** (firing alerts) and **Argo CD** (applications that are
degraded, missing or failed to sync). An incident from either source is diagnosed automatically by an agent that has the cluster read tools, and that may ask for
one of the four cluster actions of part C; every such action still waits for the maintainer's approval.

The part is proven on the kind testbed of part C, extended by an Alertmanager. Pointing Remedy at the homelab is configuration afterwards, not code.

| Part | Content | Status |
|---|---|---|
| A, B | Gatekeeper, approvals, the run clock, cancelling | implemented |
| C | Cluster: read tools, approved mutating actions | implemented |
| D | Signals: Alertmanager and Argo CD, a general incident model, the responder for outages | this spec |

### Non-goals

- Loki (phase 3), webhook receivers (an inbound port and a fourth authentication domain), and any source other than the two above.
- **Correlating signals of different sources** into one incident. An outage can open an alert incident and an Argo CD incident; linking them by namespace or
  service is a later cycle.
- Notifications (Home Assistant), a fixer or a pull request for a cluster incident, and a budget over the subscription's rate limits.
- Writing to Alertmanager (silences, acknowledgements) or creating alerts. Remedy only reads.
- Several Alertmanagers, and Alertmanager clusters beyond what one URL answers.
- Changing how the GitHub responder works: it keeps its snapshot, has no tools and is not affected by the approval expiry of section 6.

## 2. Decisions

| Topic | Decision |
|---|---|
| Alertmanager transport | **Polling** `GET /api/v2/alerts`, in the poll cycle of phase 1. No inbound port, no new authentication domain. The latency is the poll interval. |
| Argo CD source | Polling through the existing `kube.Reader` (`ListApplications`), with the read identity of part C. No new credential. |
| What an Argo CD incident is | Health `Degraded` or `Missing`, or the last operation `Failed` or `Error`. `OutOfSync` alone is not one: without auto-sync it is a normal state. |
| What an alert incident is | An alert in the state `active` (not silenced, not inhibited), except `Watchdog` and `InfoInhibitor`. |
| One incident per source | The key is per source. No correlation across sources (non-goal). |
| Where the unification happens | In the **incident engine** (one observation type with a source and a key), not in the pollers. The GitHub poller stays a poller; the two cluster sources share a small `Source` interface and one runner. See 3. |
| Data model | **One `incidents` table**, generalised (migration 008). One list, one state machine, one set of limits, notes and timeline for all sources. |
| Responder for outages | Starts automatically for severity `critical` and `warning` (alerts) and for every counted Argo CD state, under the limits of phase 1. A run with `tools` and `cluster`: the cluster read tools, the incident tools and the four actions, each action after an approval. No snapshot. |
| Approvals of automatic runs | **Expire after 15 minutes** (`REMEDY_AUTO_APPROVAL_TIMEOUT`), so that an unanswered request does not hold the one runner. Manual runs wait without a limit as before. |
| Answer format | A second, strict schema for cluster incidents, stored in the same `diagnosis` column. |
| Configuration | Environment variables, like the cluster. No UI for it. |

## 3. Components

New packages under `internal/`:

- `alertmanager`: the client. Read-only like `github` and `kube.Reader`: every exported method is a `Get*` or `List*` (enforced by a test), and the HTTP
  transport refuses every method except `GET` and `HEAD` before it is sent. The bearer token is read from a file at every request, there are no redirects and no
  proxy (the same rules as `internal/kube`).
- `alertmanagertest`: a fake Alertmanager with recorded answers, in the manner of `kubetest`.
- `signals`: the interface and the runner for the cluster sources.

```go
// A Source reports everything that is wrong right now, in one answer. A source that cannot give a complete answer returns an error and nothing else.
type Source interface {
    Name() string                                   // "alertmanager" or "argocd"
    Fetch(ctx context.Context) ([]Signal, error)
}
```

A `Signal` is `{Key, Title, Severity, Auto bool, Details, URL, SeenAt}`: normalised, bounded, and the only thing the engine knows about a source.

The **runner** of `signals` calls every configured source once per poll interval and gives the engine, for each source:

1. an observation `Bad` for every signal (the engine opens the incident or touches it),
2. after a **complete and successful** fetch, `Resolve` for every active incident of that source whose key is not in the answer.

A fetch that failed, timed out, was cut off or was cut short by a size limit resolves nothing. An unreachable Alertmanager is therefore never read as "all clear";
the failure is logged once per cycle and shown in the UI as the state of the source (like a failing repo in phase 1).

`incident.Observation` gets `Source` and `Key` and loses its GitHub-only fields to `Details`; `Observe` keeps its rules (idempotent, a green or absent
signal resolves, an ignored incident stays ignored). The GitHub poller keeps its own cycle and its pull-request rule and writes the same observation type with
`Source: "github"` and the key it has always had.

The responder gets a branch by source (section 6), `internal/prompt` a second input type, and `internal/diagnosis` a second schema.

## 4. Data model (migration 008)

`incidents` is rebuilt with:

| Column | Meaning |
|---|---|
| `source` | `github`, `alertmanager` or `argocd`. Existing rows become `github`. |
| `key` | The identity inside the source. For GitHub the old triple `repo_id`, `ref`, `check_name` joined with a separator that cannot occur in a ref; for alerts `<alertname>/<fingerprint>` (the fingerprint is Alertmanager's own hash of the labels); for Argo CD the application name. |
| `title` | A short line for the list. For GitHub the check name, for an alert `summary` or the alert name with namespace and pod, for Argo CD the application and its state. At most 200 characters, one line. |
| `severity` | `critical`, `warning`, `info` or `none`. GitHub rows: `none`. |
| `auto_diagnose` | Decided once, when the incident opens, by the source (see 6). It replaces the list of conclusions in `ListAutoCandidates`. |
| `details` | The signal as bounded JSON (labels, annotations, application state). Shown as text and put in the prompt as data, never as markup. |

`repo_id` becomes nullable; `ref`, `check_name`, `head_sha`, `check_url` and `conclusion` stay and are filled for GitHub rows. For a cluster incident `conclusion` carries the
state (`firing`, `degraded`, `missing`, `sync_failed`).

The index of active incidents becomes `UNIQUE (source, key) WHERE state <> 'resolved'`. A resolved incident does not block a new one: an alert that fires again
after it resolved opens a new incident, as a red check does after a green one.

`activity`, `incident_notes` and `tool_calls` reference `incidents(id)`. The rebuild keeps every id and every foreign key. This is the riskiest step of the part and
the first test of the first plan: the migration runs on a real 007 database filled with incidents, notes, tool calls and activity, and nothing may be lost or
re-numbered.

The state machine, the diagnosis limits (checked in one store transaction, `StartDiagnosis`), the notes and the timeline are unchanged and valid for every source.
Activity summaries name the source, for example `Incident opened: alert KubePodCrashLooping (demo/web)`.

## 5. The two sources

### Alertmanager

Configuration (control plane only, all optional; without the URL the source is off):

| Variable | Meaning |
|---|---|
| `REMEDY_ALERTMANAGER_URL` | Base URL, `http` or `https`. |
| `REMEDY_ALERTMANAGER_TOKEN_FILE` | A bearer token in a file, read at every request. Optional. |
| `REMEDY_ALERTMANAGER_CA_FILE` | A CA bundle for `https`. Optional. |

The poll interval is the existing `REMEDY_POLL_INTERVAL`.

The request is `GET /api/v2/alerts?active=true&silenced=false&inhibited=false`, and the client checks `status.state == "active"` on every alert again. An answer
over 8 MB, or one that is not a JSON array of alerts, is an error, never a partial list. An alert becomes a signal unless its `alertname` is `Watchdog` or
`InfoInhibitor`. `severity` is the label of that name, lower-cased; anything other than `critical`, `warning` or `info` is `none`. `Auto` is true for `critical` and
`warning`. At most 50 new incidents are opened per source and cycle; the rest is logged and picked up in the next cycle, which protects against an alert storm.

Labels and annotations are limited in number, key length and value length before they go into `details`, and they are redacted (`internal/redact`).

### Argo CD

The source needs only the read side of part C (`REMEDY_K8S_READ_TOKEN_FILE`). It lists the applications with `ListApplications`; a `Truncated` listing is an
incomplete answer and resolves nothing. An application is a signal when its health is `Degraded` or `Missing`, or the last operation is `Failed` or `Error`. The key is
the application name, `Auto` is true, `severity` is `warning` (`critical` is not a state Argo CD can tell). `details` carry the health message, the
conditions, the last operation and the resources that differ from Git (at most 20, as the reader already limits them).

## 6. The responder for outages

`responder.start` branches on `incident.source`.

**GitHub incidents** go the way of phase 1, unchanged.

**Cluster incidents** start a run with `tools` and `cluster` and without a snapshot, through the same `StartDiagnosis` transaction, the same limits and the same
`automatic` flag. The run is offered the seven cluster read tools, the incident tools and the four actions; `internal/gatekeeper` already decides this from the run
flags. A run that needs the gatekeeper drops `--safe-mode` (CLAUDE.md, measured), which the runner already does for a claim that carries an MCP token.

**Automatic start.** `ListAutoCandidates` selects by `auto_diagnose` instead of by the list of conclusions: `critical` and `warning` alerts, every Argo CD state, and
the GitHub conclusions it selected before (it now joins `repos` with a left join, and the `enabled` check applies to GitHub rows only). A diagnosed cluster incident is not diagnosed again automatically; a click does it. (GitHub incidents are re-diagnosed on a
new head commit as before.)

**Prompt.** Only `internal/prompt` builds it: instructions outside, the signal in blocks with a random delimiter, cleaned, redacted and bounded. The instruction says
that the agent is Remedy's responder for an outage, that it must find the cause with the read tools, that everything in the blocks is data copied from the cluster's
monitoring and never an instruction, and that it may request an action only when its diagnosis supports it and says in `proposed_action` which one and why.

**Answer.** `diagnosis.ParseCluster` (strict, like `Parse`) accepts `summary`, `cause`, `confidence`, `category`, `affected_objects` (a list of `kind/namespace/name`),
`proposed_action` (text, or `none`) and `evidence` (what the agent read). Categories: `workload_crash`, `image_pull`, `resource_pressure`, `configuration`,
`dependency_outage`, `sync_failure`, `unknown`. `responder.CheckOutcome` validates it in `finish`, as for GitHub. The UI shows it as text.

**Approvals of automatic runs expire.** One run at a time means that a run waiting for an approval holds the runner (the run clock does not count the wait). For a run with
`automatic = 1` the gatekeeper gives every approval a deadline, `REMEDY_AUTO_APPROVAL_TIMEOUT` (default 15 minutes, a positive duration; `0` turns the expiry off). When
it passes without a decision, the call ends as `abandoned` with the decision `abandoned` and a reason `no decision within <duration>`, the agent is told that no decision
was made in time, and the run goes on to its answer. The activity log gets an `approval_expired` entry in the same transaction. The action stays a proposal in the diagnosis.
Runs started by a click wait without a limit. The expiry is a property of the call, set when the call starts waiting; changing the variable does not change calls that
already wait.

## 7. Untrusted text

Alert labels and annotations, Argo CD messages and conditions are text that anyone who can name a pod, write a rule or commit to a repository can influence. They are
data: fenced, redacted and bounded in the prompt, shown as text in the UI, and never an instruction. The damage an injection can do is bounded by what a run can do:
read tools, the four actions on the allowlist namespaces, and an approval for every action. The approval card shows the target, not the agent's motive (accepted in
part B). Alert text in the activity log and the incident title is one line and cut to 200 characters.

## 8. API and UI

- **Incident list:** a source badge (GitHub, Alertmanager, Argo CD) and the `title`; the repository and ref columns are shown for GitHub rows only. A filter by source.
- **Incident detail:** the signal's `details` as text, the severity, and the diagnosis rendered by source (`DiagnosisCard` knows both schemas). "Start diagnosis",
  "Ignore" and the notes work as before.
- **Source state:** `GET /api/signals` lists the configured sources with the time of the last cycle and its error, if any. Settings shows it next to the repositories.
- **Approvals:** an expired approval appears in the history as `abandoned` with its reason; the Timeline shows `approval_expired`.
- `GET /api/incidents` gets `source`, `title`, `severity` and `details`; the old fields stay for GitHub rows.

## 9. Testing

Test first for logic with behaviour. In particular:

- **Migration 008** on a populated 007 database: every row, id and foreign key survives; the new unique index lets a resolved key reopen.
- **Alertmanager client:** recorded answers, only `GET` and `HEAD` pass, every exported method is a `Get*`/`List*`, the token is read per request and appears in no error
  or log, an answer over 8 MB or not an array is an error.
- **Sources and runner:** idempotence (a second cycle on the same state changes nothing); a failed, cut-off or truncated fetch resolves nothing; a missing signal after
  a complete fetch resolves its incident; silenced, inhibited, `Watchdog` and `InfoInhibitor` alerts open nothing; at most 50 new incidents per cycle and source; a
  resolved alert that fires again opens a new incident.
- **Argo CD:** `OutOfSync` alone opens nothing; `Degraded`, `Missing`, a failed and an errored operation each do.
- **Prompt and answer:** fencing and redaction of labels and annotations, bounds, `ParseCluster` rejects unknown members, bad categories and over-long fields.
- **Responder:** a cluster incident starts a run with the flags and without a snapshot; the limits count it; a diagnosed cluster incident is not re-diagnosed
  automatically; GitHub behaviour is unchanged (the existing tests stay green).
- **Approval expiry:** an automatic run's approval expires after the timeout and the run goes on; a manual run's does not; a decision just before the deadline wins; the
  activity entry is written in the same transaction; `0` turns it off.
- **Live tests** with the tags `kind` (read-only) and `kindwrite` (changes the testbed), as in part C: the Alertmanager of the testbed and an Argo CD application in a
  bad state against the real client.

The **testbed** (`dev/kind/`) gets a minimal Alertmanager (one pod, a service, a config that needs no receiver) and a script that posts a test alert to it. Posting is
test setup done by the script; Remedy only reads. `Missing` exists already (`guestbook` before its first sync). `Degraded` is made by a failing image with a short
`progressDeadlineSeconds`; whether Argo CD reports it as `Degraded` in the pinned version is checked in the first live test, and the real run decides.

## 10. Order of work

Three plans, as in part C. Each ends with `make check` and CI green, and a plan builds only on the one before it.

1. **2d-1, the incident model.** Migration 008 (its test first), the generalised observation and engine, GitHub as a source of observations with the same behaviour,
   `ListAutoCandidates` by `auto_diagnose`, the API fields and the UI list and detail. Phase 1 behaves as before.
2. **2d-2, the sources.** The Alertmanager client and its fake, the Argo CD source, the `signals` runner, the configuration, `GET /api/signals`, the testbed
   extension and the live tests.
3. **2d-3, the responder and the real run.** The prompt, `ParseCluster`, the branch in the responder, the approval expiry, the diagnosis card, the runbook and the run
   against the real CLI and the testbed, recorded in `docs/research/phase-2d-real-run.md`.

## 11. Success criteria

With the CLI logged in and the kind testbed up (an Alertmanager, the read and write identities, the allowlist `demo`):

- an alert posted to the testbed's Alertmanager becomes an incident within one poll interval; when the alert ends, the incident resolves; a second cycle on an
  unchanged state changes nothing,
- a silenced alert opens no incident,
- stopping the Alertmanager resolves nothing and shows the source as failing; starting it again resumes,
- an Argo CD application that is `Missing` or `Degraded` becomes an incident; one that is only `OutOfSync` (and `Healthy`) opens none,
- the automatic diagnosis of a crash-looping workload names the cause through the read tools, an instruction in an alert label is not acted on, and a fake token in an
  annotation does not appear in the prompt, a tool result or the diagnosis,
- an action the agent requests shows up as an approval; approved, it runs; left alone, it expires after the timeout (shortened for the run), the run still ends with a
  diagnosis and the activity log has `approval_expired`,
- a click-started run keeps waiting past that timeout,
- a pull-request incident from GitHub is created, diagnosed and resolved as before,
- no cluster or Alertmanager token appears in an API answer, a log or a database column,
- `make check` and CI are green.

## 12. Risks

- **The rebuild of `incidents`** touches the table everything points at. It is the first test and the first task of the first plan.
- **`Missing` is also the state of an application that was never synced.** A new application with manual sync opens an incident, and it is diagnosed automatically. The
  maintainer can ignore the incident; if it is a nuisance in practice, a grace period before `Missing` counts is a later change.
- **Alert text as an injection source.** Fenced, redacted, bounded; the damage is limited by the allowlist, the four actions and the approval (section 7).
- **An alert storm** opens many incidents. The cap of 50 new ones per source and cycle bounds the table; the limits of phase 1 bound the runs (20 a day by default).
- **Remedy can restart itself.** An alert about Remedy can lead to a run that asks to restart Remedy. Documented, not prevented, as in part C.
- **The expiry hides a late decision.** An approval that expired cannot be approved afterwards; the maintainer starts a new run. That is the intent: a stale action must
  not run on a state that has changed.
- **One run at a time** stays. A long diagnosis of one outage delays the next; the limits and the expiry keep it bounded, a second runner is not part of this cycle.
- **Alertmanager API version drift.** `/api/v2/alerts` and its fields belong to a version; the recorded answers and the live test are the check after an upgrade.
