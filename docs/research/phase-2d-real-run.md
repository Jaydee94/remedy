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

Durations are `startedAt` to `finishedAt` of the run. The cost of each run is in the API as `costUsd` (0.018 to 0.062 USD per run, 0.33 USD for the nine; notional).

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

Findings 1 to 5 were all dealt with the same day (PR "fix: follow-ups of the real run"); the control run after the fixes is below.

1. **In an automatic run the agent proposed a change instead of asking for it, unless the alert's text pointed at the fix.** Run 3 was `high` confidence
   and wrote "I have not requested the restart"; it only called `cluster_rollout_restart` when the alert carried a `runbook` annotation (runs 4 and 5).
   *Fixed:* `prompt.Outage` now tells the agent to ask for exactly one fixing action when one of its tools can make it (a restart, a pod deletion, a
   `host_change`), says that nothing happens before the maintainer approves, and keeps fixes the tools cannot make (a manifest) in `proposed_action`
   only. Control run: it asks without any runbook hint, and does not ask when the evidence shows no clear fix (see below).
2. **The approval of an automatic run had no incident.** `incidentId` was empty on the approval of `cluster_rollout_restart` (runs 4 and 5), so the card on
   Needs you had no "Incident #N" link and the thread did not show the question (`web/src/thread.ts`). *Fixed:* `BeginToolCall` gives a call the run's
   incident when it names none. Control run: every call of the run, reads included, and the approval carry `incidentId`; the thread shows "Approved
   cluster_rollout_restart on incident #1: …" and "Restarted deployment demo/api".
3. **A restarted Alertmanager that re-delivers alerts opened new incidents and new automatic runs.** After the resolve, the two alerts posted again
   opened incidents 8 and 9 and two more runs. *Fixed:* a non-GitHub incident that is opened while a resolved incident of the same key was diagnosed
   within the last hour (`incident.ReopenQuiet`) is opened with `auto_diagnose` false; it is shown, and "Diagnose" works. Control run: the resolved
   `NodeExporterDown` incident fired again, incident 4 opened with `auto=false` ("it was diagnosed recently; I do not diagnose it again on my own") and no
   run started.
4. **`cluster_pod_logs` with `previous=true` returned nothing usable for a crash-looping pod** (run 1). Not a Remedy defect: the cluster itself answered
   "unable to retrieve container logs for containerd://…" (the kubelet no longer had the previous container). *Changed:* the tool adds one line after that
   text, "The cluster no longer has this log. How the container ended is in the lastState of cluster_describe." Covered by a test only; the situation did
   not come up again in the control run.
5. ~~No cost is shown for any run.~~ **That was a mistake of the first version of this record:** the API field is `costUsd` (the run table's `cost_usd`),
   not `cost`. The nine runs of the first round cost 0.33 USD in total (notional, the subscription is not billed per run): 0.018 to 0.062 per run.
6. None of the runs took more than 25 seconds of agent time (run 4's 77 s is the one-minute wait).

## The control run after the fixes

Same setup (a fresh kind cluster, a fresh SSH testbed, a real Alertmanager 0.34.1, the same CLI), built from the branch with the three fixes; this time the
SSH testbed's `systemctl` and `journalctl` were made stateful (a unit stays `failed` until it is restarted) so that a host change has something to fix.

| # | Alert | What was wrong | What happened | Cost |
|---|---|---|---|---|
| A | `KubeDeploymentReplicasMismatch`, **no runbook hint**, the container log says the instance stays unready until restarted | `demo/api` has no ready pod (its readiness file was deleted) | the agent read the pods, the deployment and the log, **asked for `cluster_rollout_restart`** (an approval card with `incidentId` 1 and a 1-minute deadline); approved 0.1 s after it was asked: the restart ran once, the pod was replaced; the answer says "which I requested and the maintainer approved" and names the lasting fix (the manifest) it cannot make | 0.070 USD |
| B | `ContainerHighMemory` for the container `remedy-drill` | nothing visible: the fake docker shows a running container, host memory is free | `host_list`, `docker ps -a`, `docker inspect`, `docker logs`, `free -m`; **did not ask for a restart** (low confidence, "restart only if it starts getting OOM-killed"): correct, there was no clear fix | 0.059 USD |
| C | `NodeExporterDown` | `node_exporter.service` failed (the journal says the port is in use) | `systemctl status`, `journalctl`, `ss -tln`, then **`host_change` `systemctl restart node_exporter.service`**, approved: it ran through `sudo -n` over real SSH, the unit is `active`, and the agent checked with `systemctl is-active` and wrote it down; activity feed: "Ran systemctl restart node_exporter.service on ssh-testbed" | 0.033 USD |
| D | `NodeExporterDown` again, after it was resolved | (re-fired) | incident 4 opened with `auto=false`, no run | 0 |

So `host_change` is now proven with the real CLI, too: the approval showed the argv, the command ran as root through the sudoers rule of the testbed, and the
result reached the agent. The three runs cost 0.16 USD together.

## What was not covered

The chart's in-cluster runner with an outage (the dummy setup cannot reach the SSH testbed, and the NetworkPolicy blocks a private Alertmanager), a `host_change` that is refused or expires (the control run only approved one), the home server, a real Prometheus with real rules, and any run with `auto_approval_timeout` at its default.

## The recording

`internal/alertmanager/alertmanagertest/testdata/alertmanager-0.34.1.json` is the answer of the Alertmanager of this run to the client's request, for two
posted alerts, unedited (it has the member `mutedBy`, which the hand-written sample lacks); `TestARealAnswerOfAlertmanager0_34IsReadAsItIs` reads it
with the client. A recording of the home server's own answer is still open (read-only, one `GET`, needs the maintainer's go-ahead).
