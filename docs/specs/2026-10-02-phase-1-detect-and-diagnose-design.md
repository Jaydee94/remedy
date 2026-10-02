# Phase 1 (part 1): detect and diagnose

Status: approved in the planning session of 2026-10-02, pending written-spec review.
Parent documents: [`../design.md`](../design.md) (sections 2.2 to 2.4, 2.9 and the roadmap) and
[`../research/spike-claude-billing.md`](../research/spike-claude-billing.md) (CLI isolation, real event shapes).

## 1. Goal and scope

Remedy watches GitHub repositories, turns failed CI checks into **incidents**, and automatically produces a
**read-only diagnosis** (cause, confidence, affected files, fix proposal) for real failures. A **timeline**
shows what happened. Remedy changes nothing on GitHub in this part.

Phase 1 of the roadmap was split into four parts. This spec covers parts A (GitHub connection and CI
signals) and B (responder), plus the timeline from part D. The remaining parts are separate cycles:

| Part | Content | Status |
|---|---|---|
| A | GitHub connection, repos, CI signals, incidents | this spec |
| B | Responder: diagnose a failure, read-only | this spec |
| D (partly) | Timeline (activity feed) | this spec |
| C | Fixer: change the workspace, the control plane pushes a branch and opens a PR | later cycle |
| D (rest) | Home Assistant notifications with a deep link | later cycle |

### Non-goals

- Any write call to GitHub (no comments, branches, PRs, labels, merges). The GitHub client offers no write
  methods in this part.
- The fixer, automatic fixes, Home Assistant notifications.
- Webhooks, or any inbound connection from GitHub.
- Several GitHub connections in the UI, and master-key rotation.
- Other signal sources (Alertmanager, Loki, Argo CD) and the learning graph.

## 2. Decisions

| Topic | Decision |
|---|---|
| Signal source | **Polling** the GitHub API. No inbound port; the signal layer gets an interface so webhooks can be added later without rework. |
| What is a failure | Everything GitHub reports as not green: `failure`, `timed_out`, `startup_failure`, `cancelled`, `action_required`, on open PRs **and** on the default branch. |
| Automatic diagnosis | Only for `failure`, `timed_out`, `startup_failure`. The other results become visible incidents; the maintainer starts a diagnosis by click. |
| Configuration | Everything in the UI. The GitHub token is stored **encrypted in the database**; the key comes from `REMEDY_MASTER_KEY`. |
| Connections | One GitHub connection for all repos. The data model separates connection and repo, so more connections need no migration. |
| Responder context | Failed job logs, PR metadata and diff, and a **repo snapshot** (tarball at the head commit) that the control plane fetches and the runner unpacks into the workspace. |
| Diagnosis format | **Structured JSON** validated against a schema. |
| Incident identity | Key `(repo, ref, check name)`, where `ref` is `pr:<number>` or `branch:<default branch>`. New commits that stay red are recurrences of the same incident; it resolves automatically when the check is green or the PR closes. |
| UI scope | Settings (connection, repos), incident list, incident detail with diagnosis, timeline. |
| Architecture | New modules in the existing control plane plus an append-only `activity` table. No new process. |

## 3. Components

New packages under `internal/`:

| Package | Responsibility |
|---|---|
| `secret` | Seals and opens values with AES-256-GCM using the master key. Provides a value type whose text form is `***`. |
| `github` | Read-only API client with an ETag cache: open PRs, check runs of a commit, job logs, repo tarball. Knows the token, nothing about incidents. |
| `poller` | Polls each enabled repo (default every 60 s) and emits observations `(repo, ref, check name, conclusion, head SHA, link)`. |
| `incident` | Correlates observations into incidents, runs the state machine, resolves automatically, applies the limits. |
| `activity` | Append-only log that feeds the timeline. |
| `diagnosis` | The JSON schema of a diagnosis and validation of the agent's output. |
| `reaper` (inside the server) | Fails runs that stay `running` beyond the timeout. |

Existing packages that change: `store` (migration and queries), `server` (new API, runner API additions),
`runner` (snapshot download and unpacking, run timeout), `provider` (structured output), `config`.

## 4. Data model (migration 002)

- `github_connections`: `id`, `token_ciphertext`, `token_hint` (last four characters), `login`, `status`
  (`ok`, `error`, `undecryptable`), `status_detail`, `checked_at`. One row for now.
- `repos`: `id`, `connection_id`, `full_name`, `default_branch`, `enabled`, `last_polled_at`, `last_error`.
- `incidents`: `id`, `repo_id`, `ref`, `check_name`, `state` (`open`, `diagnosing`, `diagnosed`, `resolved`,
  `ignored`), `conclusion`, `head_sha`, `check_url`, `occurrences`, `diagnoses` (count of automatic diagnoses), `first_seen`,
  `last_seen`, `last_diagnosis_at`, `resolved_at`, `resolved_reason`, `diagnosis` (JSON), `run_id`. A unique
  index on `(repo_id, ref, check_name)` for rows whose state is not `resolved`.
- `activity`: `id`, `at`, `kind`, `repo_id`, `incident_id`, `run_id`, `summary`, `data` (JSON).
- `runs` gains `incident_id`, `role` (`adhoc` or `responder`), `output` (structured JSON) and `failure_reason`.

Activity kinds: `incident_opened`, `incident_recurred`, `incident_resolved`, `incident_ignored`,
`diagnosis_started`, `diagnosis_finished`, `diagnosis_failed`, `poll_failed`, `poll_recovered`,
`connection_changed`, `repo_added`, `repo_removed`.

## 5. Polling and incident correlation

One cycle per enabled repo, default every 60 seconds:

1. Fetch open PRs (conditional request with ETag; a `304` does not count against the rate limit) and, per PR,
   the check runs of its head commit.
2. Fetch the check runs of the default branch's head commit.
3. Turn each check run into an observation.

| Observation | Effect |
|---|---|
| `queued` or `in_progress` | ignored; an open incident stays unchanged |
| bad result, no open incident for the key | open an incident, log `incident_opened` |
| bad result, open incident, new head SHA | `occurrences` + 1, update head SHA and conclusion, log `incident_recurred` |
| bad result, open incident, same head SHA | nothing (polling is idempotent) |
| `success`, `neutral`, `skipped` | resolve an open incident, reason `green` |
| PR closed or merged | resolve, reason `pr_closed` |

Bad results are `failure`, `timed_out`, `startup_failure`, `cancelled`, `action_required`.

**Automatic diagnosis** starts only for `failure`, `timed_out` and `startup_failure`, and only if all limits hold:

- cooldown of 15 minutes per incident,
- at most 3 automatic diagnoses per incident,
- at most 20 automatic responder runs per rolling 24 hours,
- one run at a time (the runner is sequential).

The numbers are configuration defaults. A manual diagnosis from the UI ignores the cooldown, the cap of three
and the 24-hour limit (the maintainer asked for it explicitly), but still respects "one run at a time".
`incidents.diagnoses` counts automatic diagnoses only, which is what the cap uses.

**Errors:** on `403` or `429` the poller waits for `Retry-After`. A repo's failure is stored in
`repos.last_error` and logged as `poll_failed`; the next success logs `poll_recovered`. The reaper fails runs
that stay `running` for more than 15 minutes (`failure_reason = timeout`); this closes the phase 0 limitation
that a dead runner leaves a run `running` forever.

## 6. The responder run

1. The incident code creates a run (`role = responder`, `incident_id`). The runner claims it as before.
2. At claim time the control plane builds the **context package**:
   - the **prompt**: instructions plus the untrusted data (PR title and description, logs of the failed jobs,
     diff summary), truncated with priority on the end of the log,
   - a **snapshot endpoint** `GET /runner/v1/runs/{id}/snapshot` that streams the repo tarball at the head
     commit, fetched with the PAT,
   - the **JSON schema** of the diagnosis.
3. The runner unpacks the snapshot into the workspace (rejecting path traversal, symlinks pointing outside,
   and anything beyond the size limit) and starts the CLI as today: tools `Read`, `Grep`, `Glob` only,
   confined to the workspace, no credentials.
4. The diagnosis comes back as structured JSON. The control plane **validates it against the schema**. Invalid
   output fails the run with `failure_reason = invalid_output`; for an automatic diagnosis it counts against the
cap of three.

Diagnosis schema (fields): `summary`, `cause`, `confidence` (`high`, `medium`, `low`), `category`
(`dependency_update`, `test_failure`, `build_error`, `configuration`, `infrastructure_or_flaky`, `unknown`),
`affected_files` (list of paths), `proposed_fix`, `fix_looks_automatable` (boolean).

### Prompt injection and secrets

- Untrusted text is placed in blocks delimited by a **random token per run**, with an instruction that it is
  data and not instructions.
- The agent **can only read**. The worst outcome of a crafted log is a wrong diagnosis, not an action.
- The UI shows the diagnosis as escaped text, never as HTML.
- **Redaction** before anything is sent to the CLI: GitHub tokens (`ghp_`, `github_pat_`, `ghs_`), AWS keys,
  private key blocks, `Bearer ...` and `password=...`. GitHub already masks secrets in logs with `***`.
- The **snapshot omits typical secret files** (`.env*`, `*.pem`, `*.key`, `id_rsa*`). Encrypted files (SOPS)
  stay ciphertext, and agents never receive decryption keys.

Limits: run timeout 10 minutes (in the runner), snapshot at most 50 MB, log excerpt at most 200 KB.

### Open verification (first task of the plan)

The CLI documents structured output (`--json-schema`) for `--output-format json`. Whether the `stream-json`
mode delivers it in the `result` event is **not yet verified**. It is tested against the real CLI before
anything builds on it. If it does not work, the fallback is to ask for a JSON object in the final `text` block
and extract and validate it in the control plane.

## 7. The GitHub connection and its encryption

- `REMEDY_MASTER_KEY` is 32 random bytes, Base64 (`openssl rand -base64 32`). A missing or malformed key
  makes the server exit with code 2 and a clear message.
- Values are sealed with AES-256-GCM and a random nonce. The row's ID is bound in as additional authenticated
  data, so a ciphertext copied into another row does not open. Tampering or a wrong key is detected on open.
- The token is **write-only**:
  - `PUT /api/github/connection {token}` checks it against GitHub (`GET /user`), stores only the ciphertext,
    the last four characters and the login, and never returns the token.
  - `GET /api/github/connection` returns login, hint, status and check time. `DELETE` removes the connection
    and stops polling.
  - In memory the token has a type whose text form is `***`; it never appears in logs or error messages.
- `POST /api/repos {full_name}` checks access with the token and refuses without it. The UI lists the
  required **read-only** PAT permissions: Metadata, Contents, Pull requests, Actions, Checks.
- With a wrong master key and an existing connection the status becomes `undecryptable`, polling is off, the
  server keeps running, and the UI asks for the token again.
- Master-key rotation is out of scope: changing the key means entering the token again. This is documented.
- Threat model: a stolen database backup alone is worthless. Whoever has the database and the key gets only as
  much as the PAT allows.

## 8. API

Admin API (session cookie and `X-Remedy-CSRF`, as today):

- `GET|PUT|DELETE /api/github/connection`
- `GET|POST /api/repos`, `PATCH|DELETE /api/repos/{id}` (enable or disable, remove)
- `GET /api/incidents?state=&repo=`, `GET /api/incidents/{id}`,
  `POST /api/incidents/{id}/diagnose`, `POST /api/incidents/{id}/ignore`
- `GET /api/activity?before=&limit=` and a live stream `GET /api/activity/stream` (SSE, resumable with
  `Last-Event-ID`, like the run stream)

Runner API additions (bearer token, as today): `GET /runner/v1/runs/{id}/snapshot`, and the claim response
carries `role`, `incident_id` and the schema; `POST /runner/v1/runs/{id}/finish` accepts an optional
`output`.

## 9. User interface

Sidebar: Timeline (home), Incidents, Runs (the existing manual runs), Settings.

| View | Content |
|---|---|
| Timeline | Reverse-chronological feed from `activity`, grouped by day, with repo and links, paginated and live. |
| Incidents | Table with a state filter (default: open) and a repo filter. Columns: check, repo, ref (link to the PR), state, conclusion, occurrences, last seen. |
| Incident detail | Header with links to the PR and the check run. A diagnosis card (summary, cause, confidence badge, category, affected files, fix proposal, "automatable"). The live responder run while it runs. This incident's history. Actions: start or repeat the diagnosis, ignore. |
| Settings | GitHub connection card (write-only token field, status, check connection). Repo table (add by `owner/name`, enable or disable, last poll, last error, remove). The limits are shown read-only. |

The visual design pass that phase 0 deferred belongs to this cycle: a consistent dark theme, sidebar, tables,
badges, cards and forms with shadcn/ui, and a real router instead of hash routing (five or more pages). The
implementation uses the `frontend-design` skill and is checked in a real browser with screenshots.

## 10. Testing

- **Unit:** sealing and opening, the diagnosis schema, the incident state machine (table-driven), redaction,
  safe unpacking (traversal, symlinks, size), mapping of GitHub responses to observations.
- **GitHub client** against an `httptest` server with fixed example responses, including ETag and rate-limit
  behaviour. No network in tests.
- **Integration:** a fake GitHub server, the real server and the real runner with the fake `claude`. Covers the
  whole chain: a failure appears, an incident opens, a diagnosis is stored, green resolves it, the reaper
  clears a stuck run.
- **Real checks:** the structured-output test against the real CLI, and a run against a real repository.
- **Browser:** every new view with Playwright and screenshots. Everything under `-race`.
- The token never appears in an API response or a log (a test searches both).

## 11. Order of work

Each step is mergeable with green CI.

0. Spike: structured output in `stream-json` mode (real CLI).
1. `secret`, master key at startup, configuration.
2. Migration 002 and the store (connection, repos, incidents, activity).
3. The `github` client (read-only) with example responses.
4. Router, layout, shadcn/ui and the settings page (connection, repos).
5. `poller`, incident state machine, activity, incident list.
6. Run extension (role, incident link, output, timeout) and the reaper.
7. Responder: context package (redaction, truncation), snapshot endpoint, unpacking in the runner, schema
   validation, auto-start rules, diagnosis card.
8. Timeline with live updates.
9. Real run against GitHub (the Remedy repository itself) and documentation.

## 12. Success criteria

With a read-only PAT and the Remedy repository registered:

- a deliberately red PR check appears as an incident within one to two minutes,
- Remedy produces a plausible diagnosis automatically (cause and affected files),
- the timeline shows the sequence,
- the incident resolves when the check turns green,
- Remedy never makes a write call to GitHub, and the token appears in no API response and no log,
- `make check` and CI are green.

## 13. Risks

- The structured-output assumption (section 6), hence step 0.
- The maintainer must create a suitable fine-grained PAT.
- Automatic runs consume subscription quota; the limits in section 5 bound it. A later change can derive the
  budget from the CLI's `rate_limit_event`.
- GitHub's API shapes and rate limits are only exercised against fixtures until step 9.
