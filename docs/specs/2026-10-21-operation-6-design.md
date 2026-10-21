# Operation on the home server, cycle 6

Status: draft of 2026-10-21, not yet approved by the maintainer. Written from the umbrella without a question round (the maintainer asked for that); every decision that is the author's call, not the maintainer's, is marked **(call)**.
Parent documents: [`2026-10-09-hosts-and-skills-design.md`](2026-10-09-hosts-and-skills-design.md) (the umbrella: sections 3, 4, 10 and 11 are binding here),
[`2026-10-11-hosts-2-tools-design.md`](2026-10-11-hosts-2-tools-design.md) (the table of reads),
[`2026-10-16-alertmanager-4-design.md`](2026-10-16-alertmanager-4-design.md) (the source and the responder for outages),
[`2026-10-19-skills-5-design.md`](2026-10-19-skills-5-design.md) (the learning run),
[`2026-10-06-kubernetes-deployment-design.md`](2026-10-06-kubernetes-deployment-design.md) (the chart), [`../runbook/homelab-deploy.md`](../runbook/homelab-deploy.md) (the install, never run).

## 1. Goal and scope

Remedy runs on the maintainer's home server (single-node k3s, Argo CD, VictoriaMetrics stack), installed from a release, and does for real what the earlier cycles proved on testbeds: it reads the lab's Alertmanager, maps an alert to the NAS, looks at the NAS over SSH,
asks for one change, and waits for the maintainer. The cycle ends with the **drill** of umbrella section 10, run end to end and recorded, and with a confirmed host profile of the NAS.

Three things stand between the code of cycles 1 to 5 and that run, and they are the work of this cycle:

1. **The chart cannot reach the places Remedy needs to reach.** The control plane's NetworkPolicy allows DNS, the API server and public HTTPS only. The NAS (SSH, a private address) and an Alertmanager in the cluster are both blocked (umbrella section 13; `docs/runbook/alertmanager.md`).
2. **`docker ps -a` shows every container of the host.** The sudoers line and the form of the table both say `docker ps -a`, so on the NAS the run would see the Paperless containers, their images and ports. That contradicts umbrella section 3 and the drill's last step. The table needs a filter (section 5).
3. **Nothing has ever been installed on this cluster.** The runbook is unproven; the home-server repository installs apps in a way the runbook's three-source Application does not fit (section 2).

### Non-goals

- **Cluster actions on the home server.** `cluster.write` stays off. The drill needs none, and the namespace allowlist is its own decision after the drill.
- **The Argo CD source**, a second host (the k3s machine itself), the GitHub connection (Setup can add it later; the drill does not use it), and any change to the Alertmanager receiver (`blackhole` stays; Remedy only reads).
- **Permanent alert rules.** The `VMRule` of the drill is removed afterwards. Which real alerts Remedy should be given is a later decision.
- **TLS, a public address, a second user.** The UI stays on the LAN and Tailscale (section 2, "Exposure").
- **Backups beyond what the runbook says**, high availability, Litestream.
- **Changing anything on the NAS but the account, its sudoers file and the drill container.** The Paperless containers, Portainer and the Ansible-managed parts are not touched.

## 2. Decisions

| Topic | Decision |
|---|---|
| Install form **(call)** | A **wrapper chart** in the home-server repository: `argocd/apps/remedy/` with `Chart.yaml` depending on `oci://ghcr.io/jaydee94/charts/remedy` at one pinned release version, a `values.yaml` with everything under the key `remedy:`, and the extra manifests of the next rows. The repository's ApplicationSet turns every directory under `argocd/apps/*` into a one-source Application whose namespace is the directory name, which does not fit the runbook's three-source Application (`ref` next to an OCI chart is unproven, and it would be the only app of this lab that needs its own Application). The namespace is therefore `remedy`. The CLI pin (`deploy/cli-pin.yaml` of the release) is copied into the wrapper's values; a bump of the chart version and of the pin are one pull request. |
| Namespace | `remedy`, made by the ApplicationSet's `CreateNamespace`. The wrapper carries a `Namespace` manifest with the label `pod-security.kubernetes.io/enforce: restricted` **(call)**; the plan checks that Argo CD accepts it next to `CreateNamespace`. |
| Secrets | A `SealedSecret` `remedy-secrets` in the wrapper (admin password, runner token, master key), sealed on the server as the lab's gotchas say (`kubeseal` runs there over SSH). The three values are generated once on the maintainer's machine and never written to disk in the clear. The master key is also copied into the maintainer's password manager: since cycle 1 it seals the **SSH credentials**, not only the GitHub token, so losing it means entering every host's login again. |
| Exposure | Ingress on Traefik, host `remedy.homeserver` like the other services, plain HTTP, reachable on the LAN and over Tailscale. The session cookie then has no `Secure` flag (the runbook says so). **The NAS key is entered only over a path that is encrypted: `kubectl port-forward` to `localhost`, or Tailscale.** Never over the plain LAN address. |
| Cluster | `cluster.enabled: true` with the read identity, `cluster.write.enabled: false`. `networkPolicy.apiServer.cidrs` from `kubectl get endpoints kubernetes` on the server (read-only). |
| Reaching Alertmanager | A new chart value `networkPolicy.alertmanager` (section 4) opens one egress rule from the control plane to the Alertmanager pods. The home-server's `monitoring-namespace-isolation` policy admits ingress from a fixed list of namespaces; the namespace `remedy` is added to it (a pull request in the home-server repository). The address in Setup is the service the cluster gives Alertmanager, **read from the cluster, not guessed**. |
| Reaching the NAS | A new chart value `networkPolicy.hosts` (section 4): a list of single addresses and ports, on the **control plane's** policy only. The runner never opens an SSH connection. For the drill: `192.168.178.118/32` port `22`. |
| The NAS side | Done by the maintainer, by hand, from the onboarding snippet the Hosts page generates: the account `remedy` (not root, not in the `docker` group), the key's public half in its `authorized_keys`, and `/etc/sudoers.d/remedy` with the lines of section 5. Container pattern `remedy-*`, no systemd units. Remedy never runs any of it. |
| The host entry | Name `ugreen-nas` (the default host rule matches the name against the `instance` label, and on this lab the NAS is `instance="ugreen-nas"`). The key is generated for this purpose by the maintainer and entered once (umbrella decision: the maintainer enters the login). The connection test and the host-key comparison are done before anything else. |
| The drill container | `remedy-drill`, started by hand with `docker run`, a label, no restart policy, a loop that logs a line every ten seconds. **Not** in Ansible, Portainer or any stack: nothing may restart it behind Remedy's back, and nothing of the NAS's configuration changes. |
| The drill rule | A `VMRule` in `argocd/apps/monitoring/templates/` (the app the lab's vmalert reads with `selectAllByDefault`), severity `critical`, `for: 1m`, labels `instance="ugreen-nas"` and `name="remedy-drill"` so the host rule matches. It is merged by the maintainer and **removed by a second pull request after the drill**. The expression is measured against the real cAdvisor series before the pull request is opened (section 10). |
| Quota guard **(call)** | The Alertmanager of this lab holds firing alerts now. When Remedy first reads it, every young `critical` or `warning` alert would be diagnosed on its own. So: install and connect with `diagnose_max_per_day` = `0` (automatic diagnosis off), look at the incidents that appear, and only before the drill set `diagnose_max_per_day` to 5 and `alertmanager_auto_max_age` to 10 minutes. After the drill the maintainer decides what stays. |
| Order of the proofs **(call)** | The drill first, with no skills, so that what the run did is its own doing. The learning run for the NAS comes after the drill (umbrella criterion 5). |
| A second pass | After the successful drill, stop the container again and **deny** (or let expire) the approval: the real run of a refused or expired `host_change` that cycles 2 and 4 left open. Optional; it costs one more run. |
| Who touches what | The maintainer merges every pull request, runs everything on the NAS, and says "go" before any command on the server, including a read-only `kubectl` over SSH (the earlier access was for one session). The author prepares the pull requests, the commands and the record. |

## 3. What changes where

| Place | Change |
|---|---|
| **This repository, chart** | `networkPolicy.hosts` and `networkPolicy.alertmanager`, their validation, render tests, the chart's README. |
| **This repository, host tools** | The filtered `docker ps -a` (section 5): `internal/hostexec`, `internal/sshhost` (the snippet), the fake `docker` of `dev/ssh/`, their tests. |
| **This repository, documents** | A runbook `docs/runbook/home-server-drill.md`; `homelab-deploy.md` rewritten for the wrapper chart, with its "not run" status updated by what the drill shows; the umbrella's status and section 3 sentence about Paperless; `CLAUDE.md`; `docs/design.md` (roadmap); the record `docs/research/home-server-drill.md`. |
| **home-server repository** (pull requests, merged by the maintainer) | `argocd/apps/remedy/` (wrapper chart, `SealedSecret`, `Namespace`); the `remedy` namespace in `monitoring-namespace-isolation`; the drill's `VMRule`, and its removal; a doc page like the other apps have. |
| **The server** (read-only, with a "go") | The API server endpoints, the Alertmanager service and its labels, the node's architecture, `kubectl get` on the Application after the first sync. |
| **The NAS** (the maintainer) | The account, the key, the sudoers file, the container, and `docker stop` for the drill. |

## 4. The chart's two values

```yaml
networkPolicy:
  # Where the control plane may open SSH connections: single addresses, never a range. The runner has no such rule.
  hosts: []
  #  - {cidr: 192.168.178.118/32, port: 22}
  # An Alertmanager inside the cluster. Empty: no rule.
  alertmanager: {}
  #  namespace: monitoring
  #  podSelector: {app.kubernetes.io/name: vmalertmanager}
  #  port: 9093
```

- Both land in the `remedy-server` NetworkPolicy and nowhere else (a render test asserts the runner's and the refresher's policies are unchanged).
- Render refuses: a `cidr` that is not a single IPv4 (`/32`) or IPv6 (`/128`) address; a port outside 1 to 65535; `alertmanager` without a `namespace` or with an empty `podSelector` (an empty selector would admit every pod of the namespace); a `hosts` entry that equals the API server's address (that rule is `apiServer`'s).
- **Why not a free `extraEgress` list (call):** one typo (`0.0.0.0/0`, no port) would open all egress of the pod that holds the credentials, and the render could not tell. Two typed values can be checked.
- The default renders exactly what it rendered before (a test pins the old output for the default values).

## 5. `docker ps -a` shows only the host's containers

Today the form `docker ps -a` runs `sudo -n -- docker ps -a`, and the snippet's sudoers line is `docker ps -a`. Both list every container on the machine. The Paperless containers' names, images, ports and states would reach the run and, through it, the model provider.

**Decision (call).** The form is kept for the agent (`["docker","ps","-a"]`), but `hostexec` runs it with the host's container pattern as a filter: for the pattern `remedy-*` the executed line is `sudo -n -- docker ps -a --filter name=^remedy-`, for an exact name `--filter name=^NAME$`. The sudoers line becomes that exact line, with no star, so `sudoers` enforces the same filter and a request for the unfiltered form is refused by sudo itself, not only by Remedy. The result opens with a line saying it is filtered to the pattern. A host without a pattern still has no docker forms.

- This changes the table of cycle 2, which says the table grows by a decision recorded with a test. This section is that decision. The classification test, the snippet test and the fake `docker` of `dev/ssh/` change with it, and a test pins that no argv the agent can send reaches the host as an unfiltered `docker ps`.
- The other sudoers lines stay as the umbrella has them (`docker logs --tail * remedy-*`, `docker inspect remedy-*`, `start|restart|stop remedy-*`). A `*` in sudoers also matches spaces and further operands: `docker inspect remedy-a paperless-db` would pass sudo, and is stopped by Remedy's one-name check, not by sudo. That is the known and accepted limit of umbrella section 11.
- **Unverified and measured first:** that docker's `name` filter with `^remedy-` matches the container names as the NAS's docker stores them (they carry a leading `/` internally). The plan's first task runs it against a real docker; if the regex needs `^/?remedy-` or another form, this section is corrected before code is written.
- Existing hosts: the next `docker ps -a` they run is filtered; a stored sudoers file with the old line makes sudo refuse the new, narrower command until the snippet is applied again. The Hosts page shows the new snippet; the runbook says to re-apply it. Nothing else of a host is migrated.

## 6. The sequence

Each step names who acts. A step that changes the server or the NAS waits for the maintainer.

1. **Remedy pull request: chart values and the `docker ps` filter** (section 4 and 5), `feat:`, so the merge makes a release. The first task of the plan is the measurement of the filter. The released version is the one the install uses.
2. **Facts from the server** (read-only, with a "go"): the API server's address and port, the Alertmanager service name, namespace, pod labels and port, the node's architecture (`amd64` or `arm64`: the CLI pin has both), whether the three `ghcr.io/jaydee94` packages are public (the cluster cannot pull private ones), and the real cAdvisor series for the NAS (does `container_last_seen{instance="ugreen-nas"}` carry a `name` label, and what happens to it after `docker stop`).
3. **home-server pull request 1: Remedy.** The wrapper chart, the `SealedSecret` (sealed by the maintainer on the server), the `Namespace`, the `remedy` entry in the monitoring isolation policy. Merged; Argo CD syncs; the maintainer watches the first sync (the probes under kube-router and Argo CD's handling of a wrapper chart with an OCI dependency are unproven; the runbook's troubleshooting applies). With `cluster.write` off, the write token, its CronJob and the `PostSync` hook are not part of this install (the plan confirms what the chart renders).
4. **The login and the first look.** `kubectl exec` into `remedy-runner-0` for the one-time login (the runbook's section 7). Open the UI through a port-forward or Tailscale, sign in, check Setup: runner connected, logged in, the cluster read side on. Set `diagnose_max_per_day` to 0. Enter the Alertmanager address and look at the incidents that appear: this is the first time Remedy reads this lab's real alerts, and the record notes what it found.
5. **The NAS.** In the UI add the host `ugreen-nas` (address, port 22, account `remedy`, pattern `remedy-*`), copy the generated snippet, and on the NAS: create the account, append the public key, write the sudoers file with `visudo -f`, check `command -v docker` against the path in the lines. Back in the UI: enter the private key, test the connection, compare the fingerprint with the NAS's own (`ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub` there) and pin it. The test must show: not root, not in `docker`, `sudo -n` limited to the lines.
6. **Start the drill container** on the NAS with the maintainer's own `docker run`, and check that cAdvisor sees it with the expected labels.
7. **home-server pull request 2: the `VMRule`** of the drill, measured first (step 2). Merged; vmalert loads it; the alert is *not* firing while the container runs.
8. **The drill.** Set `diagnose_max_per_day` to 5 and `alertmanager_auto_max_age` to 10 minutes. The maintainer runs `docker stop remedy-drill`. Then, as umbrella section 10 says: the alert reaches Alertmanager; an incident opens within one poll interval, mapped to the NAS; the responder starts on its own; it reads `docker ps -a` and `docker logs` of `remedy-drill` without an approval; it asks for `docker start remedy-drill`, and the request shows the exact command in "Needs you"; the maintainer approves; the container runs; the alert resolves; the incident closes. The record states for the run, from the run's own output and the database, that **no Paperless container name, no credential and no address of the NAS appeared** in the prompt, the tool results or the answer.
9. **The second pass** (optional): stop again, deny the approval; once more, let it expire. Both leave the container stopped and the incident open with a clear account.
10. **The learning run** for the NAS: "Get to know this host", review the proposals with their evidence, confirm some and reject some. The profile can only describe what `host_read` can show of `remedy-*` containers and the host's plain reads; the record says how useful that is.
11. **Clean up.** Remove the drill container; home-server pull request 3 removes the `VMRule`; the maintainer decides which of the drill's settings stay and whether to leave the NAS host in the inventory. The runbook gets the final version of what was actually done.

## 7. Failure and abort

- **Stop everything:** scale `remedy-server` to 0, or delete the app directory in a pull request. Nothing else in the lab depends on Remedy.
- **Take the NAS access away:** delete the host in the UI (the sealed credential goes with it), then on the NAS remove `/etc/sudoers.d/remedy`, the key line and the account. Either half alone is enough to stop SSH use; both is clean.
- **The control plane never becomes Ready** (the probes of port 8080 under kube-router, unverified): the runbook's workaround is to add the node range to `networkPolicy.public.from`; as a last resort `networkPolicy.enabled: false` for the time of the check, with the exposure that means said in the record.
- **The alert does not appear in Alertmanager or has other labels than expected:** the drill stops at step 7 and the rule or the host rule is corrected; no run has been spent.
- **The run asks for something other than `docker start remedy-drill`:** deny it. That is a finding for the record, not a failure of the drill's safety; the barriers are the approval and the sudoers file.
- **A quota surprise:** the guard in section 2 keeps automatic diagnosis off until the drill; the limits (3 per incident, `diagnose_max_per_day`) bound the rest.

## 8. Tests

- **Chart:** render tests for both values (present on the server's policy only, validation failures one by one, the default output unchanged); `make chart-check`.
- **Host tools:** the table-driven classification test gains the filtered `docker ps -a`; a test that every way to ask for a `docker ps` reaches the host filtered or not at all; the snippet test for the exact line; the `dev/ssh/` fake `docker` answers the filtered form and the SSH live test runs it.
- **The measurement** of docker's `name` filter against a real docker, recorded in the plan and in the research record.
- **No test of the home server itself.** The drill is that test, and it is a real run by the maintainer's decision (it costs quota), recorded in `docs/research/home-server-drill.md` like the earlier real runs.
- `make check` passes for the Remedy pull request. The home-server repository's own CI (`yamllint`, `helm lint`, `actionlint`) must be green for its pull requests.

## 9. Success criteria

This cycle completes umbrella criteria 1, 4 and 5 and checks 2 on the real system.

1. Remedy runs under Argo CD on the home server from a release, the Setup page shows the runner connected and logged in, and the Alertmanager source reads this lab's alerts.
2. The NAS is in the inventory, its key pinned and its connection tested, entirely in the UI; the test shows an account that is neither root nor in `docker`.
3. The drill of section 6, step 8, runs end to end and is recorded with the timings of each stage and the check for Paperless names, credentials and the NAS address.
4. A look at the database and the control plane's log after the drill finds no plain secret of the host.
5. The learning run produced a profile of the NAS that the maintainer confirmed or rejected in the review list.
6. The runbook `homelab-deploy.md` says what is proven now and what is not.

## 10. Risks

| Risk | Decision | Retrofit |
|---|---|---|
| **The NAS key crosses plain HTTP.** The UI has no TLS on the LAN. | Entered only through a port-forward or Tailscale (section 2). The runbook step says so. | TLS on the Ingress with a certificate from the lab's CA or cert-manager. |
| **The control plane sits on the machine it watches.** If k3s is down, Remedy and Alertmanager are down together. | Accepted: Remedy is a helper, not a monitor. | Run it elsewhere; not planned. |
| **The master key now protects SSH logins.** Losing it loses every sealed value. | A copy in the maintainer's password manager; the loss costs re-entering the logins, nothing else. | None needed. |
| **Existing firing alerts consume quota** on the first connect. | Automatic diagnosis off until the drill; a 10 minute maximum age for it. | None. |
| **The `docker ps` filter does not behave as assumed.** | Measured against a real docker before code is written (section 5). | An output filter on the lines, as defence in depth. |
| **sudoers wildcards** (`remedy-*` also matches spaces and more operands). | Remedy checks one name per call; the pattern keeps Paperless out. The known limit of umbrella section 11. | Exact lines per container. |
| **The NetworkPolicy of the CNI on k3s is unmeasured** (kube-router and the probes, `docs/research/k8s-s3-networkpolicy.md`). | The first sync is the measurement; the runbook's workarounds apply. | The chart values `probeFrom` / a node peer, if needed. |
| **Argo CD with a wrapper chart, an OCI dependency and a hook is unproven.** | The first sync is the proof; the fallback is the git form of the same chart at the release tag as the dependency source. | None. |
| **A real run on real machines.** | Every step that changes something waits for the maintainer; the drill touches one throwaway container; the abort steps of section 7 exist. | None. |

## 11. Documents to change with the implementation

- `docs/specs/2026-10-09-hosts-and-skills-design.md`: the status line, the sentence of section 3 that the Paperless containers are not visible (true after section 5 of this spec), section 4's sudoers lines (the `docker ps -a` line), section 13's cycle 6 point.
- `docs/specs/2026-10-11-hosts-2-tools-design.md`: the row of the table for `docker ps -a`.
- `docs/runbook/homelab-deploy.md`, `docs/runbook/alertmanager.md` (the "wait for a chart value" paragraph), `deploy/chart/README.md`.
- `docs/design.md`: the roadmap line of cycle 6; `CLAUDE.md`: one line in "Current state".
- New: `docs/runbook/home-server-drill.md`, `docs/research/home-server-drill.md`.

## 12. Open points for the plan

- The exact `docker` path on the NAS (`command -v docker`; UGOS may differ from `/usr/bin/docker`) and whether UGOS reads `/etc/sudoers.d`.
- The Alertmanager's service name, namespace, labels and port as the cluster shows them.
- The NAS's cAdvisor series (`name` label, behaviour after the container stops) and with it the `VMRule` expression, for example `absent(container_last_seen{instance="ugreen-nas",name="remedy-drill"})`.
- Whether Argo CD builds the Helm dependency from `ghcr.io` (public packages) in its repo server without a repository entry, and whether Renovate should watch the wrapper's chart version.
- Whether the lab's DNS answers `remedy.homeserver` without a new entry (the other `*.homeserver` names are served by Pi-hole).
- The chart version to pin: the latest release when step 1 is merged.
