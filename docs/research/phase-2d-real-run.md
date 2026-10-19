# The real run of cycles 2 and 4: simulated outages, a real Alertmanager and the real CLI

Date: 2026-10-19. Made by the agent that built cycle 4b, unattended; the maintainer did nothing by hand (the CLI was already logged in on the
machine). It is the proof that the plan of cycle 4b left open (`docs/runbook/outage-real-run.md`): until now everything ran against a fake `claude`.

## Setup

| Part | What |
|---|---|
| CLI | `claude` 2.1.303, the maintainer's own login on this machine (the runner started the unmodified binary; nothing read the login) |
| Remedy | `main` after PR #148 (cycle 4b), built locally; a local control plane and a local runner, `REMEDY_POLL_INTERVAL=15s`, `REMEDY_LOG_LEVEL=debug` |
| Cluster | the kind testbed (`dev/kind/up.sh`, Kubernetes v1.37.0): `demo/web`, `demo/crashy` (exits 1 on start), `demo/badimage`, plus `demo/api` made for this run |
| Host | the SSH testbed (`dev/ssh/up.sh`): a container with a real `sshd`, `sudo`, `ps`, `df`, `uptime`; `docker` and `systemctl` are fakes. Added as host `ssh-testbed` with its key pinned, rule `instance="ssh-testbed"` |
| Alertmanager | a **real** Alertmanager 0.34.1 (`prom/alertmanager`, a container, a `blackhole` receiver). Alerts were posted to its API (`POST /api/v2/alerts`) the way Prometheus does; Remedy read them through its client |
| Settings | Alertmanager address in Setup; `auto_approval_timeout` `1m` for the expiry round, `5m` for the approval round |

Not used: the home server (read-only access was granted for inspection, nothing was changed or read there), the Helm chart's in-cluster runner
(the dummy setup cannot reach the loopback testbeds), a real Prometheus.

## The question: does the CLI return a valid structured answer when it also used MCP tools?

**Yes.** All nine runs ended `succeeded` with `output` set to a valid outage answer (`diagnosis.ParseOutage` accepted it; none was `invalid_output`),
after 3 to 5 tool calls each (runs 1 to 7; runs 8 and 9 were extra and were not looked at). The GitHub responder had never combined `--json-schema` with tools; the alert responder does it in every run and it works
with CLI 2.1.303. Nothing had to be changed.

## The runs

Durations are `startedAt` to `finishedAt` of the run. No run reported a cost (`cost` is empty in the API).

| # | Alert (real Alertmanager) | What was wrong | Tools the agent called | Time | Answer |
|---|---|---|---|---|---|
| 1 | `KubePodCrashLooping` (young, critical) | `demo/crashy` exits 1: its command prints that `/etc/crashy/config.yaml` is missing | `cluster_describe`, `cluster_pod_logs`, `argo_apps` | 10 s | configuration, **high**; the cause is right; `proposed_action: none` (a restart would not help, the manifest is the fix) |
| 2 | `KubePodNotReady` | `demo/api` is Running, 0/1 ready: its readiness file was deleted (by me, with `kubectl exec`; the container log says nothing yet) | `cluster_describe`, `cluster_pods`, `cluster_pod_logs`, `cluster_describe`, `cluster_pods` | 22 s | unknown, **low**: "I could not find the reason it fails"; no action. Honest: it cannot look inside the pod |
| 3 | `KubeDeploymentReplicasMismatch`, after a log line `ERROR /tmp/ready was removed by the tmp cleaner … until the container is restarted` | same pod | `cluster_pods`, `cluster_describe` ×2, `cluster_pod_logs` | 12 s | **high**, names the cause; writes the restart into `proposed_action` and says "I have not requested the restart" |
| 4 | `ApiInstanceUnready`, with an annotation `runbook: … Restart deployment demo/api` | same | 3 reads, then `cluster_rollout_restart` | 77 s | the restart **asked for approval, nobody decided, it expired after 1 m** (below); the agent still answered, **high**, and wrote "I asked for this restart, but the maintainer did not answer within 1 minute, so it was not run" |
| 5 | `ApiInstanceUnreadyAgain`, same annotation, timeout `5m` | same | 4 reads, then `cluster_rollout_restart` | 17 s | **approved 4 s after the question**: the restart ran once (`restartedAt` set, a new pod, the old one terminated); the agent reported it and that it did not re-check readiness |
| 6 | `HostHighCpuLoad` (`instance="ssh-testbed"`) | two real `nightly-reindex` processes burning a CPU each in the container | `host_list`, `host_read ps -eo pid,user,pcpu,pmem,etime,comm`, `host_read uptime` | 14 s | resource_pressure, **low**: it saw the two processes and noticed that they were 16 s old and the load average only 0.70, which does not fit a 10-minute alert; no kill proposed |
| 7 | `NodeFilesystemAlmostFull`, started two hours ago | nothing: `/` is 9% used | `host_list`, `host_read df -h` (a click on "Diagnose") | 9 s | unknown, **medium**: "the alert does not match what the host reports now"; no action |
| 8, 9 | the same alerts again, delivered after the Alertmanager was restarted | | | | two more automatic runs (see "Surprises") |

What was checked and held:

- **Within one poll** (15 s) of an alert being posted the incident was open, mapped to the host where the rule matched (`hostId` 1 for 6 and 7, none
  for the pod alerts), and an automatic run was claimed; one run at a time, oldest incident first.
- **The runs' access:** `automatic: true`, `mcp: true`, `cluster: true`; `hostCount: 1` exactly for the host alerts. The prompt (read from the API in the browser check of cycle 4b, with a seeded alert, not in these runs) holds the alert in data blocks, redacts a `password` label to `[REDACTED]` and
  carries the injection text only inside a block.
- **The old alert (7)** opened an incident with `autoDiagnose: false` and no run started; "Diagnose" (`POST /api/incidents/7/diagnose`) started a run that
  ignored the limits, as designed.
- **The expiry (run 4), with the real CLI:** the approval was asked at 10:57:17Z with `expiresAt` 10:58:17Z (exactly one minute); the Needs-you page showed
  "I stop waiting in 36 seconds."; at 10:58:17Z the call ended `abandoned` / `abandoned` with `no decision within 1m: the action was not run. Do not
  ask for it again; put it in proposed_action instead.`, the activity feed has `approval_expired` ("Nobody decided cluster_rollout_restart within 1m: the
  approval expired"), the run went on and finished 8 seconds later, the cluster was unchanged (the pod was 3 minutes old and not restarted).
- **The approval before the deadline (run 5)** ran the restart exactly once; the call is `succeeded` / `approved`.
- **A source that fails:** with the Alertmanager stopped the state line read `error` ("connection refused"), the server logged "a signal source could not be
  read; nothing was resolved" and all 7 incidents stayed open. After it was started again (it had lost its alerts, as an Alertmanager does) the next
  complete, empty answer resolved all 7 ("the signal is no longer reported").
- **The agent treats the runbook annotation as data:** in run 5 it wrote "I checked it against the pod log and the deployment spec, not only the
  alert's runbook" before it asked for the restart.

## Surprises and findings

1. **In an automatic run the agent proposes a change instead of asking for it, unless something tells it that this is the known fix.** Run 3 was
   `high` confidence and wrote "I have not requested the restart"; it only called `cluster_rollout_restart` when the alert carried a `runbook`
   annotation (runs 4 and 5). That is cautious and fits "a human always decides", but it means approvals of automatic runs are rare in practice, and
   the expiry only matters when the alert's own text points at an action. Whether the instructions of `prompt.Outage` should be sharper is a decision
   for the maintainer.
2. **The approval of an automatic run has no incident.** `incidentId` is empty on the approval of `cluster_rollout_restart` (runs 4 and 5): the call's
   incident comes only from the incident tools' own argument. So the card on Needs you has no "Incident #N" link, and the thread of the incident
   does not show the question (`web/src/thread.ts` lists an ask only when `call.incidentId` equals the incident or the call belongs to a question run). The
   answered question is therefore visible only on Needs you and in the activity feed. Fix (not made): `BeginToolCall` takes the run's `incident_id`
   when the call has none.
3. **A restarted Alertmanager re-delivers what is posted again and Remedy opens new incidents.** After the resolve, the two alerts that were posted again
   (to record the answer) opened incidents 8 and 9, which started two more automatic runs: each resolved incident that fires again is a new incident and a
   new run. The daily cap (20) bounds it; a flapping alert costs a run per flap. Known and accepted in the spec; seen here for real.
4. **`cluster_pod_logs` with `previous=true` returned nothing usable for a crash-looping pod** (run 1): the agent said so and described the command
   from the spec instead. Worth a look at the tool; not tested further.
5. **No cost is shown** for any run, so the quota use of this record is unknown. Nine runs were made, seven of them looked at: 9 to 77 seconds each.
6. The unexpected fast part: none of the runs took more than 25 seconds of agent time (run 4's 77 s is the one-minute wait).

## What was not covered

The chart's in-cluster runner with an outage (the dummy setup cannot reach the SSH testbed, and the NetworkPolicy blocks a private Alertmanager), a `host_change`
approval (the agent never asked for one), the home server, a real Prometheus with real rules, and any run with `auto_approval_timeout` at its default.

## The recording

`internal/alertmanager/alertmanagertest/testdata/alertmanager-0.34.1.json` is the answer of the Alertmanager of this run to the client's request, for two
posted alerts, unedited (it has the member `mutedBy`, which the hand-written sample lacks); `TestARealAnswerOfAlertmanager0_34IsReadAsItIs` reads it
with the client. A recording of the home server's own answer is still open (read-only, one `GET`, needs the maintainer's go-ahead).
