# Runbook: a run with gatekeeper tools against the real CLI

This runs the gatekeeper and the approvals once with the real `claude` CLI, and checks the success criteria of the
[spec](../specs/2026-10-04-phase-2ab-gatekeeper-and-approvals-design.md#12-success-criteria). It needs no GitHub token:
the incident the agent works on is put into a fresh database. Plan on about 40 minutes, most of it waiting.

## 1. Build and start

```sh
make web-install && make build
mkdir -p ~/remedy-gate-run && cd ~/remedy-gate-run
cat > env.sh <<EOF
export REMEDY_ADMIN_PASSWORD='$(openssl rand -hex 12)'
export REMEDY_RUNNER_TOKEN='$(openssl rand -hex 24)'
export REMEDY_MASTER_KEY='$(openssl rand -base64 32)'
export REMEDY_DB='$PWD/remedy.db' REMEDY_LOG_LEVEL=debug
EOF
chmod 600 env.sh && . ./env.sh

<path-to-remedy>/bin/remedy-server > server.log 2>&1 &
<path-to-remedy>/bin/remedy-runner > runner.log 2>&1 &
```

The `claude` CLI must be installed and logged in. The runner's time limit stays at its default of 10 minutes: the point of
scenario A is that a run which waits longer than that is not stopped.

Put an incident into the database (the server has created it on start):

```sh
sqlite3 -cmd '.timeout 5000' "$REMEDY_DB" <<'SQL'
INSERT INTO github_connections (id, token_ciphertext, token_hint, login, status, status_detail, checked_at)
VALUES (1, x'00', 'seed', 'octo', 'error', 'Seeded for the real run; polling is off.', '2026-10-02T12:00:00.000000000Z');
INSERT INTO repos (id, connection_id, full_name, default_branch, enabled, created_at)
VALUES (1, 1, 'octo/hello', 'main', 1, '2026-10-02T12:00:00.000000000Z');
INSERT INTO incidents (id, repo_id, ref, ref_url, check_name, state, conclusion, head_sha, check_url, occurrences, diagnoses, first_seen, last_seen, last_diagnosis_at, resolved_at, resolved_reason, diagnosis, diagnosed_sha, run_id)
VALUES (1, 1, 'pr:7', 'https://github.com/octo/hello/pull/7', 'web', 'open', 'failure', 'abc1234def5678', '', 1, 0, '2026-10-02T10:00:00.000000000Z', '2026-10-02T10:00:00.000000000Z', NULL, NULL, '', NULL, '', NULL);
SQL
```

Sign in at <http://localhost:8080> with `REMEDY_ADMIN_PASSWORD` from `env.sh`. For the `curl` checks below, keep a session in a file:

```sh
printf '{"password":"%s"}' "$REMEDY_ADMIN_PASSWORD" | curl -sf -c cookies -H 'X-Remedy-CSRF: 1' --data-binary @- http://localhost:8080/api/login
```

## 2. Scenario A: a wait that outlasts the runner's time limit

On **Ask Remedy**, switch on the chip **Use gatekeeper tools** and start a run with the prompt

> Use your tools to look at incident 1, then add a short note to it that says what you found. Reply with "finished" when the note is added.

Expected: the run page's meta line reads "Ad-hoc · tools · <time>", the **Tool calls** list gets a read call (`incident_get` or
`incident_list`) and then an `incident_add_note` call, the status chip says "Waiting for you" and the run shows the question
"May I add a note to incident #1?", and **Needs you** in the sidebar shows 1. The question shows the note the agent wants to add.

Now wait **more than 11 minutes** without deciding. Every minute check that the run is still `running`:
`curl -s -b cookies http://localhost:8080/api/runs/<id> | jq .status`, and that `runner.log` has no "run timed out" and no "heartbeat failed".

While it waits, take the run token from the file the runner wrote (the leak check needs it later) and keep it only in a file with mode 0600:

```sh
WS=${REMEDY_WORKSPACES:-${TMPDIR:-/tmp}/remedy-workspaces}   # the runner logs it as "workspaces" at start
(umask 077; jq -r '.mcpServers.remedy.headers.Authorization' "$WS"/*-mcp-*/mcp.json | sed 's/^Bearer //' > token.txt)
ls -l "$WS"/*-mcp-*/mcp.json     # -rw------- and a directory of its own, next to the workspace of the run
```

Then **Yes, add it** with a reason. Expected: the agent receives "note added" and finishes, the run ends `succeeded`, the incident's
history (`/incidents/1`) shows "Note added to incident #1 by an agent: ...", Today shows `approval_requested`,
`approval_decided` and `note_added`, and the config file and its directory are gone from the workspace root.

## 3. Scenario B: a denial

Start the same run again and **No** with the reason "not now". Expected: the agent is told `denied: not now`, says so and finishes
(its final answer is not "finished" with a note added), the incident has no second note, and the call shows as denied in the history.

## 4. Scenario C: cancelling a waiting run

Start the run again and, while it waits, click **Cancel run** and confirm. Expected: "Cancelling: the runner is stopping the agent." for
a few seconds, then the run is `failed` with a failure card titled **Cancelled**, the approval is abandoned (it leaves the waiting list), no `claude`
process of that run is left (`pgrep -fl -- '--mcp-config .*-mcp-[0-9]+/mcp.json'` shows nothing), and the runner takes the next run at once: start a short plain
run and see it finish.

## 5. Scenario D: the CLI is stopped with SIGTERM

The pattern `-mcp-<digits>/mcp.json` is the directory the runner makes for a run's config. Do not use a looser pattern such as `mcp-config`: it also matches any other `claude` process on the machine that was started with an MCP config (a spike run, for example) and would stop it.

Start the run again and wait for the approval. Stop the CLI the way a pod stop would:

```sh
pkill -TERM -f -- '--mcp-config .*-mcp-[0-9]+/mcp.json'
```

The CLI replays its in-flight call while it shuts down (see the [spike](../research/spike-mcp-blocking.md)). Expected: **never a second approval**:
`curl -s -b cookies http://localhost:8080/api/runs/<id>/tool-calls | jq '[.[] | select(.kind=="mutating")] | length'` is 1 for that run, whatever else happens. The run ends
(failed, the agent was killed) and its approval is abandoned.

## 6. Scenario E: the runner dies

Start the run again, wait for the approval, and kill the runner hard (`kill -9` its process); then `pkill -f -- '--mcp-config .*-mcp-[0-9]+/mcp.json'` for the orphaned CLI.
Wait about three minutes. Expected: the reaper fails the run with a failure card titled **The runner was lost** and the result text about the runner, and the
approval is abandoned. Start the runner again; it works as before.

## 7. The audit

- Run token: `scripts/check-no-token-leak.sh http://localhost:8080 server.log runner.log "$REMEDY_DB" < token.txt` (from the Remedy
  checkout, with `REMEDY_ADMIN_PASSWORD` exported). It must end with "the token appears nowhere that was searched": the token is in no log, not in
  the database (only its hash is), and in no admin API answer, the run events included.
- `grep -c 'could not' server.log runner.log` and a look at both logs: no warnings that are not explained by a scenario.
- The audit of a run: `curl -s -b cookies http://localhost:8080/api/runs/<id>/tool-calls | jq` lists every call, with its arguments, outcome and decision.

## 8. Clean up

Stop the server and the runner, `pkill -f -- '--mcp-config .*-mcp-[0-9]+/mcp.json'`, and delete `~/remedy-gate-run` (it holds the master key and the database).
