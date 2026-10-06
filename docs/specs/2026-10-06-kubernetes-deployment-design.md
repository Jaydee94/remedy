# Kubernetes deployment and the dummy setup: design

Status: draft for review, 2026-10-06. Implements the decision of `docs/design.md` §2.7 ("everything in the cluster, managed by
Argo; control plane as a Deployment, runner as a StatefulSet with a PVC") and refines it. Where this document and §2.7
differ, the first plan (K-1) changes §2.7 before it changes any code, as the design document requires.

## 1. Purpose, scope, non-goals

Remedy runs completely in Kubernetes: the control plane, the runner and the jobs they need. A maintainer, or an agent working
for the maintainer, can build up a throwaway copy of that setup and tear it down again with short `make` commands, and run the
real chain through it: UI, server, runner, real `claude`, gatekeeper, cluster tools.

In scope: container images, a Helm chart, the kind-based dummy setup with its `make` targets and a smoke test, the image
pipeline, a homelab runbook, and a minimal runner status (connected, logged in).

Not in scope: a backup of the database (Litestream later, own plan), a fake agent or a fake GitHub (the dummy always uses the
real agent), a GitHub test repository, Alertmanager and Argo CD signal sources (phase 2d), multi-replica operation, any
multi-user access.

Success criteria:

1. `make dummy-up` on a machine with Docker, kind, kubectl and helm ends with a reachable UI, a healthy control plane and a
   runner connected to it, without manual steps except the one-time `make dummy-login`.
2. `make dummy-down` removes everything in the cluster; the claude login survives.
3. `make dummy-smoke` runs a real ad-hoc cluster run and an approved action against the demo workloads and fails with a clear
   message otherwise.
4. The chart installs on the maintainer's k3s homelab with a secret that exists already, an Ingress and no code change.
5. No trust boundary of `CLAUDE.md` is weakened (section 5 lists what the cluster adds).

## 2. Decisions

| # | Decision | Why |
|---|---|---|
| D1 | The dummy setup always uses the real `claude`. | The maintainer wants the real chain. Cost: subscription quota, no deterministic text, a valid login is a precondition. |
| D2 | The login persists in a host directory (`~/remedy-kind/claude`) that kind mounts (`extraMounts`) and a hostPath PV exposes to the runner. `make dummy-login` is interactive and runs once. | A cluster deletion would otherwise delete the PVC and the login with it. Remedy's scripts and code never open files in that directory. |
| D3 | Packaging is a Helm chart in `deploy/chart`. | Several optional blocks (cluster read and write, Ingress, network policy) fit values better than overlays. |
| D4 | The server pod runs as ServiceAccount `remedy-read` (a projected, rotating token). A CronJob mints a short-lived token of `remedy-write` into a Secret that the pod mounts as a file. | A pod gets only its own account's token. The RBAC separation of `docs/specs/2026-10-04-phase-2c-cluster-design.md` stays. If the job stops, the token expires and actions fail closed. |
| D5 | The chart never creates the three application secrets. It references an existing Secret (`existingSecret`). The homelab provides it as a SealedSecret; `make dummy-up` generates one. | No plaintext in git or in values. |
| D6 | Access is an optional Ingress with TLS. | Design §2.8: LAN or VPN only. |
| D7 | No database backup in this plan. The chart has a PVC; the runbook has a snapshot chapter. | YAGNI for the first operation; a loss costs history and the GitHub token. |
| D8 | A minimal runner status ships as the last sub-plan, behind a spike on the login check. | Without a terminal, an expired login would only show as a failed run. |
| D9 | The `claude` binary is not in any image. An init container installs the pinned, unmodified CLI from the official source and verifies its checksum. | The repository and its GHCR packages are public; baking in the proprietary binary would redistribute it. The pin also decouples CLI updates from image builds. |
| D10 | `make dummy-smoke` runs an ad-hoc cluster run about `demo/crashy` and an approved restart of `demo/web`, and asserts states, not text. | Nothing but GitHub's poller creates an incident today. |
| D11 | NetworkPolicies are on by default and use only standard policies (the homelab is k3s with kube-router; there is no FQDN filter). | The runner holds only logins and must not reach the Kubernetes API or private networks. |
| D12 | The server listens on two ports: the public one (UI, `/api`, `/healthz`) and an internal one (`/runner/v1`, `/mcp`). The Ingress and the NodePort of the dummy expose only the public port. | The runner token and the run tokens must not be reachable through the Ingress. Path rules in an Ingress cannot exclude paths portably. This is a small server change (K-1). |
| D13 | Helm render assertions are a Go test that runs `helm template` and decodes its output. The test skips with a message when `helm` is missing; CI has it. This adds one test-only dependency, a YAML decoder (`gopkg.in/yaml.v3`), which the stdlib cannot replace. | Invariants like "automount is off on the runner" and "the write namespaces never include the release namespace" must fail a build. |

Defaults that follow from the above and are not up for debate in the plans unless something contradicts them:

- Cluster write is off by default; `cluster.write.namespaces` is empty; the chart fails to render when the release namespace
  is in that list (Remedy may read itself, never change itself).
- Server: one replica, strategy `Recreate`, one RWO PVC, because the store has a single connection.
- Runner: a StatefulSet with one replica (the runner is sequential), no ServiceAccount token (`automountServiceAccountToken:
  false`), non-root (65532), read-only root filesystem, all capabilities dropped, seccomp `RuntimeDefault`.
- Images for `linux/amd64` and `linux/arm64`.

## 3. Architecture

```
                   LAN / VPN
                       |
                 Ingress (TLS)
                       | :8080  (public: UI, /api, /healthz)
  +--------------------v-----------------------------------------------+
  | namespace remedy-system                                            |
  |                                                                    |
  |  Deployment remedy-server   PVC data (SQLite)                      |
  |   :8080 public   :8081 internal (/runner/v1, /mcp)                 |
  |   SA remedy-read -> projected token (read identity)                |
  |   Secret remedy-write-token -> file (write identity, <= 2 h)       |
  |        ^                  ^                                        |
  |        | :8081            | TokenRequest + Secret update           |
  |  StatefulSet remedy-runner      CronJob remedy-token-refresh       |
  |   init: install pinned claude    SA remedy-token-refresher         |
  |   PVC state (CLAUDE_CONFIG_DIR)  (+ one hook run after install)    |
  |   no SA token                                                      |
  +--------------------------------------------------------------------+
        |  443 only                         Kubernetes API, GitHub
        v
   Anthropic (runner egress)
```

Flows: the browser reaches the public port. The runner dials the internal port (claim, events, finish, heartbeat) and the CLI
in the runner pod calls `/mcp` on the internal port with a per-run token. The server reaches the Kubernetes API with the two
token files, GitHub with the sealed token, and nothing else. The runner reaches the internal port, DNS and the internet on
443, nothing in private ranges.

### 3.1 Chart layout

```
deploy/chart/
  Chart.yaml                    version, appVersion (the image tag default)
  values.yaml
  templates/
    server-deployment.yaml      Recreate, probes on /healthz, public and internal ports
    server-service.yaml         public Service (:8080), internal Service (:8081)
    server-pvc.yaml
    runner-statefulset.yaml     init container, state PVC or an existing claim
    runner-service.yaml         headless, for the StatefulSet only
    ingress.yaml                optional, public Service only
    networkpolicy.yaml          server, runner, refresher
    serviceaccounts.yaml        remedy-read, remedy-write, remedy-token-refresher
    rbac-read.yaml              ClusterRole read (as dev/kind/rbac.yaml) + binding
    rbac-write.yaml             Roles in cluster.write.namespaces and in the Argo namespace
    rbac-refresher.yaml         tokens of remedy-write and one Secret, nothing else
    write-token-secret.yaml     empty Secret the refresher fills
    cronjob-token-refresh.yaml  plus a post-install and post-upgrade hook Job
    NOTES.txt
  tests/                        render assertions (D13)
```

### 3.2 Values (the parts that carry decisions)

```yaml
image:
  server: {repository: ghcr.io/jaydee94/remedy-server, tag: ""}   # "" = appVersion
  runner: {repository: ghcr.io/jaydee94/remedy-runner, tag: ""}
existingSecret:                  # required; no default, the chart fails without it
  name: ""
  keys: {adminPassword: admin-password, runnerToken: runner-token, masterKey: master-key}
server:
  persistence: {size: 1Gi, storageClass: "", existingClaim: ""}
  service: {type: ClusterIP, nodePort: null}   # the dummy sets NodePort
  env: {}                                      # REMEDY_POLL_INTERVAL, diagnose limits, log level
ingress: {enabled: false, className: "", host: "", tls: {secretName: ""}, annotations: {}}
runner:
  model: ""                                    # REMEDY_CLAUDE_MODEL, always pinned in practice
  runTimeout: ""
  persistence: {size: 1Gi, storageClass: "", existingClaim: ""}   # the dummy passes its hostPath claim
  cli:
    version: ""                                # required, pinned
    sha256: {amd64: "", arm64: ""}
cluster:
  enabled: false                               # read side; turns REMEDY_K8S_READ_TOKEN_FILE on
  api: https://kubernetes.default.svc
  argoNamespace: argocd
  write:
    enabled: false
    namespaces: []                             # never the release namespace
    tokenRefresh: {schedule: "*/30 * * * *", lifetime: 2h}
networkPolicy:
  enabled: true
  ingressControllerNamespace: kube-system      # k3s: Traefik
  apiServer: {cidrs: [], port: 6443}           # the node address(es) behind kubernetes.default.svc
```

## 4. The runner pod

Image: a small glibc-based base (to be fixed by spike S4), `remedy-runner` as the only Remedy file, user 65532. It does not
contain `claude`.

Init container: installs the CLI at `runner.cli.version` into an emptyDir at `/opt/claude`, verifies the SHA-256 against
`runner.cli.sha256` for the node's architecture and fails the pod if it differs. The main container mounts that volume read-only
(a CLI that tries to update itself finds nothing writable). The install mechanism (native binary or the npm package) and whether
to cache it on the state volume are decided by spike S4.

Volumes: `/state` (the PVC; `HOME=/state`, `CLAUDE_CONFIG_DIR=/state/claude`), `/workspaces` (emptyDir, `REMEDY_WORKSPACES`),
`/opt/claude` (emptyDir, read-only in the main container), `/tmp` (emptyDir). `SweepWorkspaces` already cleans the workspace
root at start.

Environment: `REMEDY_SERVER_URL` is the internal Service, `REMEDY_RUNNER_TOKEN` comes from the Secret key, `REMEDY_CLAUDE_BIN`
is `/opt/claude/claude` (or as the spike finds), `REMEDY_CLAUDE_MODEL`, `REMEDY_RUN_TIMEOUT`. `provider.FilterEnv` stays the
only gate to the CLI; nothing is added to its allowlist by this spec.

Login: once per state volume, by `kubectl exec -it` into the runner container with the runner's environment, `claude`, then
`/login`. The login lives in `CLAUDE_CONFIG_DIR`. Nothing in the chart, the scripts or Remedy's code opens it.

Probes: none until K-6 (the runner has no HTTP surface). K-6 adds a small status listener and the liveness and readiness
probes. Readiness depends on "connected to the server" only; the login state is shown, never gating (a cluster without a login
must still reach `Ready`, otherwise no unattended check is possible).

## 5. Trust boundaries the cluster adds

| Boundary | Rule |
|---|---|
| Runner identity | No ServiceAccount token, no Kubernetes API, no private networks: `automountServiceAccountToken: false` plus the egress policy. A render assertion pins the first. |
| Read and write identity | Two ServiceAccounts, two RBAC sets, as in 2c. The pod runs as the read account. The write token reaches the pod only as a file from the refresher's Secret. |
| Refresher | Own ServiceAccount. RBAC: `create` on `serviceaccounts/token` of `remedy-write` only, `get` and `update` on the one Secret only (the chart creates the empty Secret, because `create` cannot be limited by name). It never reads another Secret. |
| Write token lifetime | `lifetime` 2 h, refreshed every 30 min; at most 2 h of validity remain after the job fails. It sits in a Secret that anyone with `get secrets` in the namespace can read (accepted, R3). |
| Self-protection | The release namespace is never in `cluster.write.namespaces` (render fails). |
| Public surface | Only the public port is exposed: `/api`, `/healthz`, the UI. `/runner/v1` and `/mcp` are on the internal port and reachable only from runner pods (NetworkPolicy) and in-cluster. |
| Secrets | `existingSecret` only. Values never carry a secret; a render assertion fails on a key named like one. |
| Pod hardening | Non-root, read-only root filesystem, no capabilities, seccomp `RuntimeDefault`, on every pod. The dummy labels its namespace `pod-security.kubernetes.io/enforce: restricted`. |
| CLI credentials | Remedy never reads them. A repository check (an extension of `scripts/check-no-token-leak.sh`) fails when a script opens a file in the login directory. The scripts create and `chmod` it only. |
| Binary provenance | Pinned version, verified checksum, official source, unmodified. |

NetworkPolicies:

- Server: ingress on the public port from `ingressControllerNamespace`; on the internal port from the runner pods only. Egress:
  DNS, the API server (`apiServer.cidrs:port`), TCP 443 to public addresses (GitHub).
- Runner: no ingress. Egress: DNS, the internal server port, TCP 443 except `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`,
  `169.254.0.0/16`.
- Refresher: egress DNS and the API server.

## 6. The server changes

Two small changes, both in K-1 and both test-first:

1. **Two listeners.** `REMEDY_INTERNAL_ADDR` (default empty). Empty keeps today's behaviour: one port serves everything (local
   `make dev-server`, the existing Docker command). When set, the main port stops serving `/runner/v1/*` and `/mcp`, and a
   second `http.Server` serves exactly those. The shutdown and the two timeouts follow the existing server.
2. **`remedy-tokenrefresh`**, a second binary in the server image (`cmd/remedy-tokenrefresh`, stdlib only). It reads its own
   service account token, calls the TokenRequest endpoint for `remedy-write` (a fixed name from a flag) and updates the one
   Secret. It has no other behaviour, uses `internal/kube`'s transport rules (HTTPS, CA file, no redirect) and is tested against
   `kubetest`'s fake API server. The image runs it as `command` of the CronJob; no kubectl image and no shell are needed.

Neither touches the gatekeeper, the store or the runner protocol.

## 7. The dummy setup

Everything lives in `dev/kind/` next to the existing testbed and reuses its cluster (`remedy-dev`), its demo workloads, Argo CD
and `check-rbac.sh`.

Cluster: `kind.yaml` gets `extraMounts` (host `~/remedy-kind/claude` to `/mnt/remedy-claude`) and an `extraPortMappings` entry
(host `18080` to the node's NodePort for the public Service; `8080` is taken by `make dev-server`). The scripts create the
host directory (`0700`) before the cluster exists, because a directory Docker creates would belong to root. An existing cluster
without the mount is detected (`docker inspect` of the node) and the script says to run `make dummy-down`.

Release: namespace `remedy-system`, `helm upgrade --install` with a values file `dev/kind/dummy-values.yaml`: NodePort service,
a hostPath PV and its claim for `runner.persistence.existingClaim`, `cluster.enabled` and `cluster.write.enabled` with
`namespaces: [demo]`, `networkPolicy.enabled` as the spike S3 allows, locally built images (`remedy-server:<tag>`,
`remedy-runner:<tag>`, `kind load docker-image`, `imagePullPolicy: IfNotPresent`). The tag changes with every build so that
`dummy-redeploy` rolls the pods.

State: `~/remedy-kind/dummy.env` (mode `0600`) holds the generated admin password, runner token and master key and the URL, so
a person or an agent can read the URL and the password. `dummy-up` keeps the values if the file exists and the Secret in the
cluster matches; `dummy-down` removes the cluster and this file, never `claude/`.

| Target | Does |
|---|---|
| `make dummy-up` | prerequisites check; directory; cluster; demo workloads; Argo CD; images build and load; Secret; `helm upgrade --install`; waits for the server; prints URL and password and whether a login exists (it cannot read it; it asks the runner: after K-6, before that it prints how to run `dummy-login`) |
| `make dummy-down` | deletes the cluster and `dummy.env`; keeps the login |
| `make dummy-login` | `kubectl exec -it` into the runner with its environment, starts `claude` |
| `make dummy-logout` | deletes the host login directory after a confirmation |
| `make dummy-redeploy` | builds and loads new images, `helm upgrade`, waits |
| `make dummy-reset` | scales the server down, empties the database PVC, scales up; the cluster and the login stay |
| `make dummy-smoke` | runs `dev/kind/smoke.sh` |
| `make dummy-status` | pods, release, whether the refresher has run, a short health summary |
| `make dummy-logs` | follows server, runner and the last refresher job |

`dummy-smoke` (`sh`, `curl`, `jq`): signs in with the password of `dummy.env` and the CSRF header; waits until `capabilities`
shows the cluster tools; starts an ad-hoc run with cluster tools asking why `demo/crashy` crashes; waits for the run to end
(`REMEDY_RUN_TIMEOUT` applies) and asserts: status `done`, at least one `cluster_*` read tool call recorded, no refused call. It
then starts a second run asking for a restart of `demo/web`, waits for the approval, approves **only** a restart of
`demo/web` (any other approval is denied and fails the test), and asserts that the pods of `demo/web` were created after the
approval. A run that ends with "Not logged in" prints "run `make dummy-login`" and exits non-zero. The script reads no
credentials and sends the password only to the dummy URL.

Agents: every target is non-interactive except `dummy-login` and `dummy-logout`. All of them exit non-zero on failure and print
what they did in plain lines. `dummy.env` and `dummy-status` give an agent URL, password and state without scraping.

## 8. Images, release and the homelab

- `Dockerfile` (server image) builds both server binaries and the UI, as today, plus `remedy-tokenrefresh`. A second
  `Dockerfile.runner` builds `remedy-runner`. `make images` builds both for the local architecture.
- `.github/workflows/images.yml`: on push to `main` and on tags `v*`, buildx with QEMU for `linux/amd64,linux/arm64`, push to
  `ghcr.io/jaydee94/remedy-server` and `remedy-runner` with `sha-<short>` (and `vX.Y.Z` on a tag). On a tag the workflow checks
  that `Chart.yaml`'s `appVersion` equals the tag. The workflow has `packages: write` and nothing more.
- `make chart-check` (in `make check` and CI): `helm lint`, `helm template` for the default and the dummy values, `kubeconform`,
  and the Go render tests (D13).
- `renovate.json` gets a custom manager for `runner.cli.version` and the Argo CD version in `up.sh`.
- Homelab: an Argo `Application` with two sources, the chart from this repository at a tag and a values file from the
  maintainer's GitOps repository (the repository is public: no credentials). Argo hooks map the chart's Helm hook to a `PostSync`
  job, which fills the write token once. The runbook `docs/runbook/homelab-deploy.md` has the Application, a SealedSecret example
  for the three keys, the Ingress values for k3s's Traefik, the one-time login, the `apiServer.cidrs` for k3s and a snapshot
  chapter. The Secret `remedy-write-token` has no `data` in the chart and the Application gets an `ignoreDifferences` for it
  as a precaution.

## 9. Spikes (before the plans that depend on them)

| Spike | Question | Decides | Blocks |
|---|---|---|---|
| S1 | Does a hostPath PV on a Docker Desktop `extraMounts` directory work for a non-root runner (uid 65532), on files the pod creates and the host reads, and across `dummy-down` and `dummy-up`? | D2, the dummy's volume and the directory mode | K-4 |
| S2 | Which unmodified `claude` command tells "logged in" from "not logged in" in a pod without reading credentials, and what does it cost (time, quota)? | the content of the K-6 check | K-6 |
| S3 | Does kind's default CNI enforce NetworkPolicies, and how must egress to the API server be written for kind and for k3s (the node address behind `kubernetes.default.svc`)? | `networkPolicy` defaults, `apiServer.cidrs`, whether the dummy enables the policy | K-2 |
| S4 | Which install route gives a pinned, checksum-verifiable, unmodified CLI on `amd64` and `arm64`; which base image runs it (glibc, ripgrep, git); does it update itself when its directory is read-only; is a cache on the state volume worth it? | the runner image, the init container, `runner.cli.*` | K-1 |

Each spike writes `docs/research/<name>.md` with the command, the output and the decision, like the earlier spikes.

## 10. Sub-plans

| Plan | Content | Needs |
|---|---|---|
| spikes S1 to S4 | as above | this spec |
| **K-1** | Amends `design.md` §2.7 and the README; the internal listener; `remedy-tokenrefresh`; `Dockerfile` and `Dockerfile.runner`; `make images` | S4 |
| **K-2** | The chart's core: server, runner with init container, Services, PVCs, Ingress, NetworkPolicies, `existingSecret`, `make chart-check` with the render tests | K-1, S3 |
| **K-3** | The cluster identities: ServiceAccounts, RBAC read and write, the refresher CronJob and hook, tests against `kubetest` and render tests | K-2 |
| **K-4** | The dummy setup: `kind.yaml`, scripts, values, targets, `smoke.sh`, the login check script, the runbook `docs/runbook/dummy-setup.md`, a real run recorded in `docs/research/` | K-3, S1 |
| **K-5** | Image pipeline, Renovate, release checks, `docs/runbook/homelab-deploy.md` | K-2 |
| **K-6** | Runner status: the status listener and probes, the report to the server, an API field, the Setup page, `dummy-up` reports the login | K-4, S2 |

K-4 and K-5 can run in parallel after K-3 and K-2. K-6 is last on purpose. Each plan is test first where there is logic
(Go code, render assertions, the smoke script's own checks with a fake `curl`), and each UI change follows the UI rules of
`CLAUDE.md` (a real browser check).

## 11. Accepted risks

| # | Risk | Mitigation |
|---|---|---|
| R1 | On Linux the CLI's login is a plain file; in the dummy it sits in `~/remedy-kind/claude` on the maintainer's disk (on macOS it would be in the keychain). | Directory `0700`, outside the repository, never read by Remedy, `dummy-logout` removes it. The dummy only. |
| R2 | The runner token and run tokens cross the cluster network over HTTP. | NetworkPolicy limits the internal port to runner pods; no mesh in the homelab. |
| R3 | The write token (valid up to 2 h) is in a Secret readable by anyone who can `get secrets` there. | Short lifetime, fail closed, namespace RBAC is the owner's. |
| R4 | A TokenRequest token is bound to the account, not the pod. | Lifetime, and the account has no rights beyond the allowlist. |
| R5 | The CLI is downloaded at pod start. | Pin, checksum, official source, read-only mount. |
| R6 | Behind an Ingress every login attempt shares one rate-limit bucket (`RemoteAddr` is the proxy). | One admin; a lockout of everyone is the safe direction. |
| R7 | `Recreate` and an RWO PVC mean a short outage per rollout; runs in flight are ended by the reaper; sessions (in memory) end. | One maintainer; acceptable. |
| R8 | 24/7 operation on a subscription login stays the risk of `docs/research/subscription-cli-usage.md`. | Limits of automatic diagnosis, as before. |
| R9 | The dummy's real agent spends subscription quota on every smoke run. | The smoke test makes two runs; it is opt-in, never in CI. |

## 12. Documents to change

`docs/design.md` §2.7 (the refresher, the CLI install, the two listeners, the dummy), `README.md` ("Only the control plane is
containerised so far"), `CLAUDE.md` (current state, the new commands, the dummy's directory and the rule that no script opens
the login directory), `dev/kind/README.md` (the dummy and the shared cluster definition).
