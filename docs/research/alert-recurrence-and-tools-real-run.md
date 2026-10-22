# The real run of the alert source and `alert_list`: the home server, 0.16.0

Date: 2026-10-22. Made by an agent through the maintainer's logged-in browser (Setup and the incident page of the deployed UI) and read-only `kubectl` over SSH; the maintainer typed nothing. It is the first run of this cycle's
code (`docs/specs/2026-10-22-alert-recurrence-and-signal-tools-design.md`) against a real Alertmanager and the real CLI. It is **partial**: see "Not covered".

## Setup

| Part | What |
|---|---|
| Remedy | 0.16.0, installed on the home server by Argo CD from the Helm chart (the first install on PostgreSQL: its own `remedy-postgres`, an empty database) |
| CLI | `claude` 2.1.292 in the runner pod, the maintainer's login there (Setup: "Connected", "Logged in") |
| Alertmanager | the VictoriaMetrics stack's `vmalertmanager` in the namespace `monitoring`, reached by its headless service on port 9093 (`networkPolicy.alertmanager` of the home server's values); address set in Setup, no token |
| Cluster | the read identity only (`cluster.write.enabled: false`); no GitHub token, no host, no metrics address |

## What was done and seen

1. The Alertmanager address was saved in Setup. The page answered "Set here"; the database got the setting `alertmanager_url` and one activity entry `setting_changed`.
2. About a minute later Setup said: "I asked Alertmanager 56 seconds ago and it listed 3 active alerts." Conversations showed three incidents from the source `alertmanager` (CPU throttling, a vmagent with no targets, a failed Job), each `open`, `firing`, 1 occurrence, and the database had three `incident_opened` entries.
3. None of the three was diagnosed on its own: the thread of the failed-Job incident (severity `warning`) said "I don't diagnose this on my own. You can ask me to look at it." Why was **not** looked into (the alert's age against `Oldest alert I diagnose on my own`, 1h by default, is the first thing to check).
4. A click on "Diagnose" started a responder run. It ended `succeeded`, in under a minute, with a valid answer (the incident moved to `diagnosed`; the strict parse accepted it). Its tool calls, all `succeeded`: `alert_list`, `cluster_pods`, `cluster_pod_logs`. The run has 14 events; the activity has `diagnosis_started` and `diagnosis_finished`.
5. The diagnosis named the failed Job of a CronJob, the DNS errors in its pod log, a later run that had succeeded, and proposed one `kubectl` command for the maintainer. It said it could only read. Whether its statements are right about the cluster was **not** checked independently.
6. No warning or error in the server log; no restart of the server, the runner or PostgreSQL.

## Proven by this run

- The Alertmanager source reaches a real Alertmanager through the chart's NetworkPolicy and the setting saved in the UI, and opens incidents in PostgreSQL.
- The tool `alert_list` works with the real CLI in a run that also used cluster tools, and the CLI returns a valid structured answer.
- Setup, the settings write, the incident list and thread, a click-started diagnosis and the run's events work on PostgreSQL.

## Not covered (so not proven)

- `silence_list`, `metrics_query` and `metrics_query_range`: no metrics address was set and the chart value `networkPolicy.metrics` is not set on the home server, so the egress is closed.
- The recurrence: no alert cleared and returned during the run, so the reopening (`occurrences`, `incident_reopened`, an ignored incident staying ignored) was not seen on the real system.
- The interval setting `alertmanager_poll_interval` (left at its default), the freshness limit and the automatic start, and an approval of an automatic run.
- The host tools, the learner and GitHub (no token, no host were entered).

## Findings

1. A `warning` alert opened at 9:15 PM was not diagnosed automatically; the reason is unknown (not investigated).
2. Argo CD showed the application as `Progressing` for the whole run while every pod was ready. A likely cause is the chart's backup claim `remedy-postgres-backup`, which stays `Pending` until its first consumer (the nightly CronJob) exists; not confirmed.
