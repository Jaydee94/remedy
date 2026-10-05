# Phase 2 (parts A and B) run against the real CLI

Run on 2026-10-04, between 19:58 and 20:18 UTC (21:58 to 22:18 local time), following
[`docs/runbook/gatekeeper-real-run.md`](../runbook/gatekeeper-real-run.md).

- Remedy: `main` at `deac2d4` (plans 2a and 2b, tasks 1 to 5 merged), binaries built with `make build`.
- CLI: `claude` 2.1.288, logged in with the maintainer's subscription. The model is pinned as `sonnet`; the CLI's init event reports `claude-sonnet-5-5`.
- Runner: default time limit of 10 minutes (`REMEDY_RUN_TIMEOUT` not set), heartbeat every 10 seconds. Reaper: heartbeat limit of 2 minutes.
- Cost as reported by the CLI: 0.0297 USD for scenario A, 0.0159 USD for scenario B, 0.055 USD for all seven runs together (subscription quota, nothing was billed to an API).

## What was run

A fresh database with one seeded incident (`octo/hello`, PR 7, check `web`; GitHub is not connected, so no job log can be read), the server and the runner on `127.0.0.1:8080`, both started by hand. The assistant ran all five scenarios itself: scenario A through the UI (sign in, **Allow gatekeeper tools**, the prompt of the runbook, **Approve**) and scenario C through the UI's **Cancel run** button; scenarios B, D and E started their runs and decided through the admin API with `curl`, because the UI paths are the same endpoints and were already checked in a browser for tasks 4 and 5. The prompt was always: "Use your tools to look at incident 1, then add a short note to it that says what you found. Reply with "finished" when the note is added."

The agent's tool list (from the CLI's init event) was `Glob`, `Grep`, `Read` and the five gatekeeper tools `activity_list`, `incident_add_note`, `incident_get`, `incident_job_log`, `incident_list`.

## Scenario A: a wait longer than the runner's time limit

| Time (UTC) | What |
| --- | --- |
| 19:58:16.8 | run started (claimed by the runner) |
| 19:58:19.6 | `incident_get` and `incident_job_log` (read calls, succeeded; the log says GitHub is not connected) |
| 19:58:22.5 | `incident_add_note` requested, the run waits (5.7 s after the start) |
| 19:59 to 20:10 | checked every minute: status `running`, no "run timed out", "heartbeat failed" or "no longer has this run" in `runner.log`, one CLI process |
| 20:08:16 | the 10 minute limit of the runner would have fired here without the clock stopping |
| 20:11:26.8 | **Approve**, reason "accurate and harmless", 13 min 4 s after the request |
| 20:11:29.4 | run `succeeded`, exit 0, answer "finished" |

The note the agent wrote says that the incident is open with one occurrence and that the root cause is unknown because the job log cannot be read. The Timeline shows `approval_requested`, `approval_decided` ("Approved incident_add_note on incident #1: accurate and harmless") and `note_added`. After the run the workspace root was empty: the workspace and the config directory (`<run>-mcp-*`, mode 0700, with `mcp.json` mode 0600, a sibling of the workspace) were both gone. While the run waited, `ls -l` showed exactly those modes.

## Scenario B: a denial

Request at 20:12:15.7, **Deny** with "not now" at 20:12:19.0. The agent was told `denied: not now` and finished at 20:12:21.7 (`succeeded`) with: "The note was not added. The maintainer denied the approval request with "not now", so I'm not retrying it." followed by what it had found. The activity log has `approval_requested` and `approval_decided` ("Denied ... not now") and no `note_added`.

## Scenario C: cancelling a waiting run

Run started 20:12:41, request 20:12:46. **Cancel run** (first click, then Enter on the armed button) at 20:12:57.8; the page showed "Cancelling: the runner is stopping the agent." The runner logged "the run was cancelled: stopping the agent" at 20:13:01.22 (3.4 s after the click, the next heartbeat) and the run ended `failed` with reason `cancelled`, exit -1, result "The run was cancelled." The approval became `abandoned` ("approval_abandoned", then "run_cancelled" in the activity log), it left the waiting list, no CLI process and no workspace was left. A plain run created at 20:13:24 was claimed 1.2 s later and succeeded ("ok").

## Scenario D: SIGTERM

Run started 20:13:33, request 20:13:37.5, `SIGTERM` to the CLI at about 20:13:38. The CLI exited with 143 within a second and the run ended `failed` (exit 143) at 20:13:38.98. The run has **one** `incident_add_note` call (status `abandoned`) and there was one approval for it, never a second. What cannot be said from this run: whether the CLI tried to replay its in-flight call while shutting down (the spike saw it do that). The server does not log requests to `/mcp`, and the runner finishes the run the moment the CLI exits, which revokes the run token and abandons the waiting call, so a replay after that would have been refused anyway. The behaviour while the run is still alive (a replay with the same tool use ID is one call, never a second approval) is covered by the gatekeeper tests of plan 2a, not by this run.

## Scenario E: the runner dies

Run started 20:14:25.5, request 20:14:30.7. `kill -9` of the runner at 20:14:33; the orphaned CLI was stopped with `pkill` on its config directory pattern (the spike run that was going on in the background was not touched, see "Problems found"). The approval became `abandoned` about 30 s later (the gatekeeper's grace period; the status showed it at the 20:15:09 poll). The reaper failed the run at 20:17:03.0 with reason `runner_lost` and the result "The runner stopped reporting for more than 2m0s and the run was failed by the control plane.", 2 min 30 s after the kill; `server.log` has the matching warning ("failed a run whose runner was lost", "silent for"=2m0s). The runner was started again at 20:17:18, claimed a plain run a second later and finished it.

## Audit

- `scripts/check-no-token-leak.sh` with the run token of scenario A (taken from `mcp.json` while the run waited, kept in a 0600 file): `server.log`, `runner.log`, `remedy.db`, every admin endpoint of the script (connection, repos, incidents, activity, runs, limits, every run and its event stream): all `ok`, "the token appears nowhere that was searched".
- The same token searched in `GET /api/runs/{id}`, `/api/runs/{id}/tool-calls` and `/api/runs/{id}/events` of all seven runs, in `/api/approvals?status=all` and `/api/activity`: 0 lines.
- `run_tokens` has the columns `run_id`, `token_hash`, `created_at`, `revoked_at`; five rows (one per run with tools), each hash 32 bytes, all revoked.
- 15 tool calls in `tool_calls` (3 per run with tools: two reads and the note), each with its arguments, outcome and decision.
- `server.log`: the start line and the one expected warning of scenario E. `runner.log`: no warnings or errors.

## Success criteria

Section 12 of the [spec](../specs/2026-10-04-phase-2ab-gatekeeper-and-approvals-design.md):

1. **An ad-hoc run with tools lists the five tools and reads an incident through them:** met (init event, `incident_get` in every run).
2. **A call to `incident_add_note` appears in the inbox within two seconds and the run waits for longer than ten minutes without being stopped:** the wait is met (13 min 4 s, the limit of 10 minutes passed with the run still `running`). The two seconds are met for the approvals endpoint (every run's request was seen at the first one-second poll) and **not measured in the browser**: by design the Approvals page refreshes every 3 seconds and the sidebar count every 5, so the UI can take longer than two seconds. Open, see below.
3. **Approving adds the note and the agent receives the result; denying leaves no note and the agent receives the denial:** met (scenarios A and B).
4. **Cancelling a waiting run ends it and frees the runner:** met (scenario C).
5. **A `SIGTERM` to the CLI during a wait does not create a second approval:** met for what this run can show (one approval); the replay itself was not observed, see scenario D.
6. **Every call is in the audit log, and the run token appears in no API response, no log and no database column except as a hash:** met (audit above).
7. **`make check` and CI are green:** met for every pull request of plan 2b (#75 to #79) and for this one.

## Problems found

- **A loose `pkill -f 'mcp-config'` would have stopped the 4-hour spike run** (another `claude` with `--mcp-config` was running on the same machine). The runbook now uses the pattern of the run's own config directory, `--mcp-config .*-mcp-[0-9]+/mcp.json`, and `CLAUDE.md` says never to use a looser one. Fixed in this pull request.
- **A `kill -9` of the runner leaves the run's workspace and its config directory on disk** (scenario E). The config file holds the run token, which is revoked as soon as the reaper fails the run, so the file is worthless afterwards, but it stays until the workspace root is cleaned by hand. Done afterwards: the runner removes the directories of dead runs from its workspace root when it starts (`runner.SweepWorkspaces`: real directories named like a run ID, a dash and more, nothing else).
- **The two-second criterion is a statement about the UI that the UI does not guarantee** (3 s and 5 s polling). Done afterwards: the Approvals page refreshes every second and the count in the sidebar every two seconds and the spec says so. Checked in a real browser (2026-10-05, Playwright, an empty database, a waiting approval inserted by hand while the Approvals page was open): the card was on the page 617 ms and 691 ms after the insert, the count in the sidebar 1617 ms and 691 ms after it. Two trials, so a bound by the intervals (about one second plus a request for the page, about two for the count), not a distribution.
- **The server logs no `/mcp` requests**, so the replay on `SIGTERM` could not be seen from the server's side. Done afterwards: at level `debug` the gatekeeper logs one line per tool call (run, tool, tool use ID, call ID, kind, replay, refused; never the arguments), so a replay shows as `replay=true`.
