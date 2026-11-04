# Research: Remedy on the home server, the drill (cycle 6)

**Status: in progress.** This record is written while `docs/plans/operation-6-home-server.md` runs. Task 8 (facts from the server) is done and recorded in section 1. The sections for the install, the NAS, the drill and the learning run follow with their tasks. Nothing on the server or the NAS was changed so far: every command of Task 8 was a read.

## 1. Facts from the server (Task 8, 2026-10-21)

Read over SSH as `jaydee` with `sudo kubectl`, and with `curl` from the server to services inside the cluster. The maintainer said "go" for these reads in the session.

| Question | Answer |
|---|---|
| API server (`networkPolicy.apiServer`) | `192.168.178.127:6443`, the server's own LAN address. Values: `cidrs: ["192.168.178.127/32"]`, `port: 6443`. |
| Alertmanager | Namespace `monitoring`. Service `vmalertmanager-monitoring-victoria-metrics-k8s-stack`, **headless** (`ClusterIP: None`), ports 9093, 9094. One pod, `vmalertmanager-monitoring-victoria-metrics-k8s-stack-0`, label `app.kubernetes.io/name=vmalertmanager`. The plan's guess for the pod label was right. Values: `namespace: monitoring`, `podSelector: {app.kubernetes.io/name: vmalertmanager}`, `port: 9093`. Address for Setup: `http://vmalertmanager-monitoring-victoria-metrics-k8s-stack.monitoring.svc:9093` (a headless service resolves to the pod's address; **not tried from a pod of Remedy**). |
| Node | `amd64`, k3s `v1.36.5+k3s1`, one node. The CLI pin needs the `amd64` platform. |
| The three packages | **Public.** `gh` could not read them (the token lacks `read:packages`), so they were checked the way the cluster pulls: `helm show chart oci://ghcr.io/jaydee94/charts/remedy --version 0.8.0` worked without a login, and the manifest of `remedy-server:0.8.0` answered 200 to an anonymous token. `remedy-runner` and `charts/remedy` answered the anonymous token request with 200 as well; the runner image's manifest was not fetched. |
| DNS for `remedy.homeserver` | Needs no entry. Pi-hole answers the whole domain (`address=/homeserver/192.168.178.127`, `docs/09-dns-architecture.md` of the home-server repository), and `remedy.homeserver` already resolved to `192.168.178.127` from this machine. |
| Namespace `remedy` | Does not exist yet (the namespace list has no `remedy`). |
| Alerts firing now | Two: `Watchdog` (severity none, since 2026-10-15) and **`KubeAPIDown`, severity critical, since 2026-10-21T08:23:30Z**. That is a real critical alert on the cluster, about five and a half hours old when it was read. With `alertmanager_auto_max_age` at the default of one hour, and at the planned ten minutes, it would not be diagnosed on its own; it stays a candidate for a click in the first look (Task 10). Not investigated here. |

## 2. Finding: the NAS's cAdvisor is not being scraped

The plan's step 5 asked whether `container_last_seen{instance="ugreen-nas"}` carries a `name` label. It does not exist in VictoriaMetrics at all.

- `count(last_over_time(container_last_seen[30d]))` is 640 series, none from the NAS. The series `up{instance="ugreen-nas",job="ugreen-nas-cadvisor"}` exists and its last value is **0**; `ugreen-nas-node-exporter` is 1.
- vmagent reports the target `ugreen-nas-cadvisor` (`http://192.168.178.118:18080/metrics`) as `down`: *"the uncompressed response ... exceeds -promscrape.maxScrapeSize or max_scrape_size in the scrape config (16777216 bytes)"*. A direct GET from the server answers 200 with **19,319,955 bytes in 10,633 lines**, about 1,815 bytes per line: cAdvisor on the NAS prints very long label sets. The largest families are `container_blkio_device_usage_total` (1,626 lines), `container_fs_*` (about 800 each), `container_tasks_state` (560). The page does carry a `name` label (11 distinct values), so the drill's rule can match on it once the series arrive.
- The VMStaticScrape CRD of this cluster has no field for the size (`kubectl explain vmstaticscrape.spec` shows `sampleLimit` and no scrape-size field). The limit is vmagent's flag.
- Consequence: **the drill's alert cannot fire as things are**; this must be fixed before Task 12. This differs from the state read on 2026-10-09 (node-exporter and cAdvisor both scraped); the cause was not investigated further (the NAS's cAdvisor output may have grown, the limit may be newer).

Options, for the maintainer to choose (both are changes outside this repository):

1. Raise the limit in the home-server repository: `vmagent.spec.extraArgs` in `argocd/apps/monitoring/values.yaml` already holds `promscrape.streamParse: "true"`; add `promscrape.maxScrapeSize` (for example 33554432 for 32 MiB). Costs memory in vmagent and storage for series nobody uses.
2. Make the NAS's cAdvisor smaller (fewer container labels, fewer metric groups) where it is deployed. Better in the long run; it changes the NAS.

This belongs in home-server pull request 2 together with the drill rule, or in a pull request of its own that comes first.

### Follow-up, 2026-11-02

The limit was raised by the maintainer (`promscrape.maxScrapeSize: 32MB`, home-server repository), and the 19.5 MB answer of the NAS fits. The target was still not healthy: the `vmagent` pod had crash-looped since 2026-10-27 (1378 restarts, one sample per ~22 minutes for every job instead of one per 30 s). The log said `FATAL: cannot send SIGHUP to itself: permission denied`. The node runs kernel 7.0.0-42 (rebooted 2026-10-28), and containerd's default AppArmor profile `cri-containerd.apparmor.d` denies a process the signals it sends within its own container (`dmesg`: `apparmor="DENIED" operation="signal" comm="vmagent-prod" signal=hup`). An image pin back to v1.154.1 (home-server#544) did **not** help; the fix is the annotation `container.apparmor.security.beta.kubernetes.io/vmagent: unconfined` (and the same for `vmalert`, whose rule reload is denied the same way), home-server#546. After it: no restarts, 10 samples per 5 minutes per job, the NAS cAdvisor shows 112 `container_last_seen` series, 10 of them with a `name` label (`day-pilot-*`, `node-exporter-*`, `paperless-*`, `portainer`), none for `remedy-*` yet. The same denials hit PostgreSQL (116 for `comm="postgres"`, `autovacuum worker took too long to start; canceled` every minute), `frr-k8s`, Argo CD's `helm` and `remedy-server` (SIGURG).

## 3. The install, the NAS, the drill, the learning run

### The install and an outage (2026-10-22 to 2026-11-02)

Remedy 0.16.0 was installed on 2026-10-22 (record `alert-recurrence-and-tools-real-run.md`). Chart 0.17.0 (2026-10-23) moved the bundled PostgreSQL from 16.15 to 18.6, and the data directory was a version 16 one: `remedy-postgres-0` and `remedy-server` crash-looped (2837 and 3772 restarts, `database files are incompatible with server`) and every nightly dump failed, until `remedy.postgres.image.tag: "16.15"` was pinned (home-server#547) and the stuck pod deleted by hand (a StatefulSet does not replace a pod that is not Ready). All three pods were Ready at 2026-11-02 13:13 UTC. The nightly dump then failed on two more nights (`Connection refused` from every attempt of a new pod, within 30 seconds); the script now retries for about ten minutes (Remedy#189), the cause is probably kube-router learning a new pod only at its next sync, not proven.

### Task 10: the first look (2026-11-04, 10:40 UTC)

- **Login:** the runner was logged in (`login check login=ok` every ten minutes since at least 2026-11-02).
- **Settings found:** `alertmanager_url` (`http://vmalertmanager-monitoring-victoria-metrics-k8s-stack.monitoring.svc:9093`) and `metrics_url` (`http://vmsingle-monitoring-victoria-metrics-k8s-stack.monitoring.svc:8428`) were already set; `diagnose_max_per_day` was still the default 20, `alertmanager_auto_max_age` 1 h. So the plan's order (limit 0 first, URL after) was already moot: the Alertmanager source had been on and diagnosing automatically. `diagnose_max_per_day` was set to **0** on 2026-11-04 through the admin API (`PUT /api/settings/diagnose_max_per_day`), read back as `0`, source "set here".
- **Sources:** `GET /api/signals`: `alertmanager` ok with 11 active, `metrics` ok. The network path was open (egress `9093` and `8428`, the `monitoring` isolation policy admits `remedy`).
- **Incidents:** 35, all from the Alertmanager (none from GitHub). 11 were marked for automatic diagnosis, 9 of them diagnosed. Open at the time: a crash looping pod, pods not ready, a PDB and a StatefulSet below their replicas; most of these were the remedy namespace's own outage, not all (a non-ready `mosquitto` is not).
- **Quality of two diagnoses** (read, not judged beyond this): incident 25, a diagnosis of Remedy's own crash loop started two minutes after the recovery, said that the server crash-looped because the database was unreachable, that Postgres had been back since 13:10 and the server Ready since 13:13:20, confidence medium, category `dependency_outage`, proposed action none, and to check why Postgres had been down and that the next backup succeeds; it also noticed the repeated autovacuum warnings. Incident 33 (a failed Job) found the `recyclarr` CronJob of namespace `media` exceeding its active deadline daily at 05:00 UTC, with the cause not confirmed because the pods were gone, and proposed manual steps because none of its tools could do them.
- **Not done:** the log lines of the Alertmanager source and of the cluster read side were not read; the NAS, the drill rule, the drill and the learning run are not made. The reading of the database through `kubectl exec` was refused by the permission check, so everything above comes from the admin API.
