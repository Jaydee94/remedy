# Phase 2 (parts A and B): the gatekeeper and approvals

Status: accepted by the maintainer on 2026-10-04. Implementation plans: [`phase-2a`](../plans/phase-2a-gatekeeper-server.md) (steps 1 to 3, the server side), implemented; [`phase-2b`](../plans/phase-2b-runner-and-approvals-ui.md) (steps 4 to 6: runner, heartbeat, reaper, UI, the real run), implemented. The run against the real CLI is recorded in [`phase-2ab-real-run.md`](../research/phase-2ab-real-run.md).
Parent documents: [`../design.md`](../design.md) (sections 2.2, 2.6, 2.8 and the roadmap),
[`../research/spike-mcp-blocking.md`](../research/spike-mcp-blocking.md) (how the CLI behaves with a blocking MCP tool) and
[`2026-10-02-phase-1-detect-and-diagnose-design.md`](2026-10-02-phase-1-detect-and-diagnose-design.md) (the incidents, the activity log and the
run model this builds on).

## 1. Goal and scope

Agents get their first **tools**. Until now an agent only read a local copy of a repository. In this part the control plane runs an **MCP
gatekeeper**: the agent calls tools, **read tools run at once, mutating tools wait until the maintainer approves in the UI**, and **every call
is audited**. The mechanism is proven with tools on Remedy's own data, so it needs no cluster.

Phase 2 of the roadmap ("Gatekeeper and cluster") is split into four parts. This spec covers parts A and B; the others are separate cycles that
reuse this mechanism:

| Part | Content | Status |
|---|---|---|
| A | Gatekeeper: MCP endpoint, run tokens, tool registry, audit log, read tools | this spec |
| B | Approvals: waiting tool calls, the approval inbox, the run clock, cancelling a run | this spec |
| C | Cluster: Kubernetes read tools and approved mutating actions | later cycle |
| D | Signals: Alertmanager and Argo CD adapters, a general incident model, the responder for outages | later cycle |

### Non-goals

- Cluster access of any kind, Alertmanager, Argo CD, Loki.
- Gatekeeper access for the automatic responder. It keeps its snapshot and has no tools, so it can never wait for an approval and block the
  runner. It gets read tools in a later cycle, if it needs them.
- "Approve this tool for the rest of the run", approval policies, several approvers, TOTP.
- Notifications (Home Assistant). Approvals are decided only in the UI.
- Concurrent runs on the runner. It still runs one CLI at a time, so a run that waits for an approval blocks every other run (see 13).

## 2. Decisions

| Topic | Decision |
|---|---|
| Transport | The CLI talks **MCP over Streamable HTTP** to the control plane (`POST /mcp`). Measured in the spike: a blocking call survives when the answer is an SSE stream with **MCP progress notifications** every 15 seconds. A plain JSON answer is cut off after 60 seconds, SSE comment lines after 300. |
| CLI invocation | The current invocation **without `--safe-mode`**: `--safe-mode` turns MCP off completely, even for an explicit `--mcp-config`. `--restricted --strict-mcp-config` keeps the isolation (builtin plugins and the one server only). Runs without gatekeeper access keep `--safe-mode`. |
| Which runs | Only **ad-hoc runs** (started by hand on the Runs page). The responder is unchanged. |
| First tools | Read tools on Remedy's own data and one mutating tool, "add a note to an incident", which writes only to Remedy's database. |
| Waiting | **Unlimited**, until the maintainer decides or cancels the run. The runner's time limit and the reaper's stale-run rule do not count the wait. |
| Cancelling | A **Cancel run** action, because a waiting run blocks the runner. It reaches the runner through the heartbeat, which stops the CLI with `SIGKILL`. |
| Run tokens | A random token per run, stored only as a hash, valid while the run runs. A third authentication domain next to the admin session and the runner token. |
| Idempotency | A call is identified by `(run, claudecode/toolUseId)`. A repeat (the CLI replays an in-flight call after `SIGTERM`) gets the state of the first call and **never a second approval or execution**. |
| What is approved | The arguments as stored when the approval was requested. They are shown in the UI and they are what runs. |
| Names | MCP server `remedy`; the CLI shows the tools as `mcp__remedy__<tool>`. |

## 3. Components

| Part | Where | Does |
|---|---|---|
| Gatekeeper | `internal/gatekeeper` | The MCP JSON-RPC handler (`initialize`, `tools/list`, `tools/call`, `ping`, notifications), the tool registry, argument validation, redaction of results, the audit rows and the approval wait. Standard library only, no MCP SDK. |
| Tools | `internal/gatekeeper/tools*.go` | One handler per tool, built on the store, the incident engine and the existing read-only GitHub client. |
| Store | `internal/store` | Run tokens, tool calls and approvals, notes; the transactions that decide an approval and close a run. |
| Server | `internal/server` | `POST|GET|DELETE /mcp` with run-token authentication; the approval and cancel routes (admin); the heartbeat route (runner). |
| Runner | `internal/runner`, `internal/provider` | Writes the MCP config, builds the CLI arguments without `--safe-mode` for such runs, sends the heartbeat, stops the CLI on cancel, keeps the time limit still during a wait. |
| Reaper | `internal/reaper` | Fails a gatekeeper run that has no heartbeat for two minutes; leaves a waiting run alone. |
| UI | `web/` | The approvals page, the run view with tool calls and a waiting banner, the cancel button, the sidebar count. |

## 4. Data model (migration 006)

- `runs`: `mcp INTEGER NOT NULL DEFAULT 0` (the run has gatekeeper access), `cancel_requested INTEGER NOT NULL DEFAULT 0`, `last_heartbeat_at TEXT`.
- `run_tokens`: `run_id` (primary key, references `runs`), `token_hash BLOB NOT NULL UNIQUE`, `created_at`, `revoked_at`. The token is minted when the run is claimed (the claim carries it; it cannot be handed out later) and is 32 random bytes
  (Base64url on the wire); only its SHA-256 is stored, and the lookup is by hash.
- `tool_calls` is both the **audit log** and the **approval**:
  - `id`, `run_id`, `tool_use_id` (unique per run), `tool`, `kind` (`read` or `mutating`), `arguments` (JSON, as validated and **not** redacted: they are what the maintainer sees and what runs),
`incident_id` (the incident the call is about, when the tool names one),
    `status` (`running`, `waiting`, `succeeded`, `failed`, `denied`, `abandoned`), `result` (text, redacted, at most 32 KB), `error`,
    `created_at`, `finished_at`;
  - `decision` (empty for read tools; `pending`, `approved`, `denied`, `abandoned`), `decided_at`, `decision_reason`.
  - The inbox is the rows with a non-empty `decision`. The rows are never deleted.
- `incident_notes`: `id`, `incident_id`, `run_id`, `note`, `created_at`.
- New activity kinds, each written in the transaction of the change: `approval_requested`, `approval_decided`, `approval_abandoned`,
  `note_added`, `run_cancelled`.
- A run that is cancelled ends as `failed` with the new failure reason `cancelled`; there is no new run status. "Waiting for approval" is not a status
  either: it is derived from a `tool_calls` row with `decision = 'pending'`, so it cannot get out of step with the approval.

## 5. The MCP endpoint and a tool call

`POST /mcp` accepts a JSON-RPC message with `Authorization: Bearer <run token>`. `GET /mcp` answers 405 and `DELETE /mcp` 200, as the spike showed the
CLI expects. An unknown or revoked token, or a run that is not `running`, gets 401.

Methods: `initialize` (echo the client's `protocolVersion`, capabilities `{"tools":{}}`, server info), `notifications/*` (202), `ping`, `tools/list`,
`tools/call`. Anything else is a JSON-RPC "method not found". `server/discover` is answered like an unknown method.

`tools/call`:

1. Look up the run by the token; the run must be `running`.
2. The `tool_use_id` is `params._meta["claudecode/toolUseId"]`. If a row for `(run, tool_use_id)` exists, **attach to it**: a finished call answers
   with its stored result; a waiting one waits like the first handler (point 6). A missing id is an error result.
3. Unknown tool or arguments that do not match the schema (strictly: no extra members, bounds, types) give an error **result**
   (`isError: true` with a text), not a protocol error, so the agent can read it and correct itself. Such a call is still audited.
4. A run may make at most 100 calls and have at most 5 pending approvals; beyond that the call gets an error result.
5. **Read tool:** run the handler, redact the text, store the row as `succeeded` (or `failed`), answer with a plain JSON result.
6. **Mutating tool:** store the row as `waiting` with `decision = 'pending'` and the activity entry `approval_requested`. Answer with `text/event-stream`: a
   progress notification (`notifications/progress` with the request's `progressToken`) every 15 seconds until the decision, then the result as the last
   event. A handler registers as a **waiter** of the call (in memory) while it waits, and unregisters when its request closes. The wait ends on a decision,
   when the request closes, or when the run ends.
   - **Approved:** in one transaction check that the decision is `approved`, that the call is still `waiting` and that the run is `running`, and set the
     call `running`. Only the handler that wins this compare-and-set executes the tool, **once**, with the stored arguments. The result is stored and sent.
     A handler that attached by replay does not execute; it waits for the stored result.
   - **Denied:** the result is an error text `denied: <reason>`; the call ends `denied`.
   - **A decision needs a waiter.** The approve and deny routes refuse with 409 ("the agent is no longer waiting") when the call has no waiter, so
     that no approved action runs without an agent to receive its result.
   - **Abandoned:** a call whose last waiter is gone becomes `abandoned` after a grace period of 30 seconds (`approval_abandoned`), unless a replay of the same
     call attaches within that time (the `SIGTERM` case: the replay arrives within milliseconds and keeps the approval pending). When the run ends, all
     its pending calls become `abandoned` at once. An abandoned approval cannot be decided any more.
7. Tool results that contain text from GitHub are labelled as untrusted data in the text itself (a leading line and fenced content), like the responder prompt.

## 6. Tools

| Tool | Kind | Arguments | Result |
|---|---|---|---|
| `incident_list` | read | `state` (`active`, `all` or one state, default `active`), `limit` (1 to 50, default 20) | Id, repository, ref, check, state, conclusion, last seen of each incident. |
| `incident_get` | read | `id` | The incident, its stored diagnosis and its history (activity entries). |
| `activity_list` | read | `before` (activity id), `limit` (1 to 50, default 20) | The newest activity entries, like the Timeline. |
| `incident_job_log` | read | `id` | The cleaned and redacted excerpt (at most 20,000 characters) of the failing job's log of that incident, fetched with the stored GitHub token through the existing read-only client, fenced as untrusted. |
| `incident_add_note` | **mutating** | `id`, `note` (1 to 2,000 characters) | Stores the note on the incident and writes `note_added`; the result is "note added". |

All results are redacted (`internal/redact`) before they go into the audit row and to the CLI.

## 7. Run clock, heartbeat and cancel

- The runner sends `POST /runner/v1/runs/{id}/heartbeat` every 10 seconds while a gatekeeper run executes. The answer is
  `{"waiting": bool, "cancel": bool}`: `waiting` is true while the run has a pending approval, `cancel` when it was cancelled.
  The heartbeat sets `last_heartbeat_at`.
- The runner's time limit (`REMEDY_RUN_TIMEOUT`, 10 minutes) is a budget of running time. It **does not advance while `waiting`**. Runs without gatekeeper
  access are unchanged and need no heartbeat.
- On `cancel` the runner stops the CLI with `SIGKILL` (the default of `exec.CommandContext`, which the spike showed does not make the CLI replay a
  call), reports `finish` with the failure reason `cancelled`, and the control plane abandons the pending approvals.
- A connection error on the heartbeat does not stop the run. After two minutes without a heartbeat (counted from `started_at` until the first one) the
  reaper fails a gatekeeper run with `runner lost`. The 15 minute rule does not apply to gatekeeper runs; the heartbeat rule replaces it, so a long
  wait is never mistaken for a stuck run. Runs without gatekeeper access keep the 15 minute rule.
- `finish`, the reaper and the cancel route end a run in one transaction that also revokes the run token and abandons its pending approvals.
- At startup the control plane marks every pending approval `abandoned` (the waiting requests died with the process).

## 8. API

Admin API (session cookie; the non-GET routes need `X-Remedy-CSRF: 1`):

- `GET /api/approvals?status=pending|all` (default `pending`; newest first, at most 100): id, run id, incident id (when the arguments name one),
  tool, arguments, decision, requested and decided times, reason.
- `POST /api/approvals/{id}/approve` and `/deny`, body `{"reason": "..."}` (optional, at most 500 characters). 409 when the approval is not
  pending (decided, abandoned, its run has ended, or the agent is no longer waiting), 404 for an unknown id.
- `GET /api/runs/{id}/tool-calls`: the audit rows of a run (tool, kind, arguments, status, result, decision, times).
- `POST /api/runs/{id}/cancel`: 204; 409 when the run has ended, belongs to a diagnosis, or is running without gatekeeper access (it has no heartbeat to hear a cancel). `POST /api/runs` gets an optional `tools: true` (default false) that creates an
  ad-hoc run with gatekeeper access.
- `GET /api/runs/{id}` additionally says whether the run is waiting for approval and which approval.

Runner API (bearer runner token): the claim carries `mcp_token` for a gatekeeper run; `POST /runner/v1/runs/{id}/heartbeat` as above.

MCP: `POST|GET|DELETE /mcp` (run token). The three domains use three separate middlewares.

## 9. User interface

- **Approvals** (new sidebar item with the pending count; the count refreshes every 5 seconds): a card per pending approval with the tool, the arguments as text
  (escaped, in full), the run and the incident it concerns (links), how long it has been waiting, a reason field, and **Approve** and **Deny** buttons.
  Below, the history: decision, reason and when it was decided. The page refreshes every 3 seconds.
- **Run view:** a banner "Waiting for your approval" with a link while an approval is pending; a "Tool calls" list (tool, status, time, and the arguments and
  result on expansion); **Cancel run** for a queued or running run, with a confirmation inside the page.
- **Runs page:** the form for a new run gets a checkbox "Allow gatekeeper tools".
- **Incident detail:** notes show in the history as `note_added` entries (text, escaped).
- **Timeline:** shows the approval entries through the activity stream, with no change to the Timeline itself.

## 10. Testing

- Test first for the gatekeeper's behaviour. A Go test client speaks MCP to the handler (handshake, `tools/list`, `tools/call` with JSON and SSE answers).
- Gatekeeper and store: authentication (unknown, revoked, ended-run token), schema rejection, limits, redaction; idempotent replay (the same `toolUseId` never
  creates a second approval or a second execution); approve, deny, abandon and run end racing each other, under `-race` and `-race -cpu 1`, with exactly-once
  execution of the mutating tool; the SSE answer sends progress notifications (the interval is injectable); a decision without a waiter is refused with 409;
  a call without a waiter is abandoned after the grace period (injectable) unless a replay attaches in time.
- Runner: the MCP config file is 0600, outside the workspace and removed afterwards; the arguments have no `--safe-mode` for gatekeeper runs and keep it
  for others; the heartbeat stops the time limit during `waiting` and stops the CLI on `cancel`; the fake `claude` script gets a variant that blocks.
- Reaper: the two-minute rule for gatekeeper runs; no change for others.
- `internal/app`: the whole chain with the Go MCP client instead of the CLI (call, approval through the admin API, result, note in the incident history).
- UI: lint and build, and a real browser against a seeded database (approve, deny, history, banner, cancel, count).
- A **real run** at the end with the CLI, recorded in `docs/research/`: an ad-hoc run reads an incident and asks to add a note, the maintainer approves in
  the UI after more than the runner's old 10 minute limit, and the agent finishes; a second run is denied; a third is cancelled while waiting.
- No test and no log may contain a run token.

## 11. Order of work

Each step is mergeable with green CI.

0. The spike on blocking MCP tools (done: `docs/research/spike-mcp-blocking.md`).
1. Migration 006, the store: run tokens, tool calls and approvals, notes, the transactions.
2. `internal/gatekeeper`: the endpoint, the registry, the audit, the read tools.
3. Approvals in the gatekeeper and the admin API: waiting, deciding, abandoning, the mutating tool.
4. Runner and reaper: the MCP config, the invocation, the heartbeat, the run clock, cancel.
5. The UI: approvals page, run view, runs form, sidebar count.
6. The real run with the CLI and the documentation.

## 12. Success criteria

With the CLI logged in and an incident in the database:

- an ad-hoc run with tools lists the five tools and reads an incident through them,
- a call to `incident_add_note` appears in the inbox within two seconds and the run waits for longer than ten minutes without being stopped,
- approving it adds the note to the incident's history and the agent receives the result; denying it leaves no note and the agent receives the denial,
- cancelling a waiting run ends it and frees the runner,
- a `SIGTERM` to the CLI during a wait does not create a second approval,
- every call is in the audit log, and the run token appears in no API response, no log and no database column except as a hash,
- `make check` and CI are green.

## 13. Risks

- **A waiting run blocks the runner** (one run at a time) and the wait is unlimited by decision. Cancelling is the way out. A later cycle can add
  concurrent runs on the runner or a timeout.
- **The CLI's MCP behaviour is not a documented contract.** The spike pins version 2.1.288: progress notifications keeping a call alive, the 60 and 300
  second limits and the `SIGTERM` replay may change. The real run and the spike script are the check after a CLI update. The longest wait measured so
  far is recorded in the spike document.
- **The run token reaches the CLI** through a file the agent cannot read (outside its workspace, mode 0600). A token that leaks lets someone act as that run
  until it ends; it grants only what the run could do.
- **Agent-written text is untrusted.** A note is shown as escaped text, never as HTML or Markdown.
- **A replayed call after a crash of the control plane** cannot execute an old approval: pending approvals are abandoned at startup.
