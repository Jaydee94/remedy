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
  (render assertions live outside the chart, in deploy/chart_test.go, D13)
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
    urlTemplate: ""                            # {version} and {platform}; S4: https://downloads.claude.ai/claude-code-releases/{version}/{platform}/claude
    archive: none                              # none (S4: the download is the executable), or tar.gz with `member` the file to take out of it
    member: ""
    platforms:                                 # the vendor's name for the platform and the SHA-256 of the artifact
      amd64: {name: "", sha256: ""}            # S4: name linux-x64
      arm64: {name: "", sha256: ""}            # S4: name linux-arm64
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
  dnsNamespace: kube-system
  public: {from: [namespaceSelector kube-system]}   # who may reach the public port; k3s: Traefik
  apiServer: {cidrs: [], port: 6443}           # the endpoint address(es) and port behind kubernetes.default.svc (S3: the rule is by endpoint address and port, not by the service address)
```

## 4. The runner pod

Image: `gcr.io/distroless/base-debian12:nonroot` (spike S4: the CLI is a dynamically linked glibc binary, which this base starts
on `amd64` and `arm64` and `static-debian12` cannot), `remedy-runner` as the only Remedy file, user 65532. It does not contain
`claude`, and it has no shell, `git` or `ripgrep` binary (the CLI bundles ripgrep, per its documentation; the first real run
in the pod confirms that a run that uses `Grep` works).

Init container: the runner image itself, running `remedy-runner install-cli` (a subcommand written in Go on the standard
library: the image has no shell and no curl). It downloads the CLI at `runner.cli.version` over HTTPS into an emptyDir at
`/opt/claude`, verifies the SHA-256 for the node's architecture (`runner.cli.platforms`) and fails the pod if it differs. The main container mounts that volume read-only
(a CLI that tries to update itself finds nothing writable at the install path). Spike S4 decided the mechanism: the native
binary, one bare ELF per platform from `https://downloads.claude.ai/claude-code-releases/{version}/{platform}/claude`
(`linux-x64`, `linux-arm64`), no archive, mode `0755`; its SHA-256 is published by the vendor in the release's signed
`manifest.json`. There is no cache on the state volume: the download is 250 MB once per pod start, and a cache would put an
executable on the one writable volume. One question stays open: an update started by the CLI would write under
`$HOME/.local/share/claude`, which is on the state volume (`HOME=/state`). S4 could not measure it without a run; plan K-4's first real
run checks that `/state/.local/share/claude` stays absent, and if it appears, the answer is a spec change (`DISABLE_UPDATES=1`
in the pod and an allowlist entry) before any code.

Decision of 2026-10-09 (K-4's first real run): after an interactive login the maintainer measured one file of 249528 KiB under
`.local` on the host side of the state volume; the headless runs created nothing in the runs observed. The maintainer decided to set
`DISABLE_UPDATES=1` in the runner pod's environment only, with **no** entry in `provider.FilterEnv`'s allowlist (the headless runs
did not stage anything, and the interactive login session started with `kubectl exec` is expected to inherit the pod environment).
That the variable keeps the directory away is not yet verified; the maintainer will check it.

Volumes: `/state` (the PVC; `HOME=/state`, `CLAUDE_CONFIG_DIR=/state/claude`), `/workspaces` (emptyDir, `REMEDY_WORKSPACES`),
`/opt/claude` (emptyDir, read-only in the main container), `/tmp` (emptyDir). `SweepWorkspaces` already cleans the workspace
root at start.

Environment: `REMEDY_SERVER_URL` is the internal Service, `REMEDY_RUNNER_TOKEN` comes from the Secret key, `REMEDY_CLAUDE_BIN`
is `/opt/claude/claude` (S4: the downloaded file runs as it is, there is no `install` step), `REMEDY_CLAUDE_MODEL`, `REMEDY_RUN_TIMEOUT`. `provider.FilterEnv` stays the
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
| Refresher | Own ServiceAccount. RBAC: `create` on `serviceaccounts/token` of `remedy-write` only, `patch` on the one Secret only (the chart creates the empty Secret, because `create` cannot be limited by name). It never reads another Secret. |
| Write token lifetime | `lifetime` 2 h, refreshed every 30 min; at most 2 h of validity remain after the job fails. It sits in a Secret that anyone with `get secrets` in the namespace can read (accepted, R3). |
| Self-protection | The release namespace is never in `cluster.write.namespaces` (render fails). |
| Public surface | Only the public port is exposed: `/api`, `/healthz`, the UI. `/runner/v1` and `/mcp` are on the internal port and reachable only from runner pods (NetworkPolicy) and in-cluster. |
| Secrets | `existingSecret` only. Values never carry a secret; a render assertion fails on a key named like one. |
| Pod hardening | Non-root, read-only root filesystem, no capabilities, seccomp `RuntimeDefault`, on every pod. The dummy labels its namespace `pod-security.kubernetes.io/enforce: restricted`. |
| CLI credentials | Remedy never reads them. A repository check (an extension of `scripts/check-no-token-leak.sh`) fails when a script opens a file in the login directory. The scripts create and `chmod` it only. |
| Binary provenance | Pinned version, verified checksum, official source, unmodified. The checksum is the vendor's own: S4 compared the downloaded `linux-x64` and `linux-arm64` files of the pinned version with the signed `manifest.json` of its release (good signature, key fingerprint equal to the one the documentation publishes). |

NetworkPolicies:

- Server: ingress on the public port from `networkPolicy.public.from` (a list of peers: the Ingress controller, or the node for a NodePort); on the internal port from the runner pods only. Egress:
  DNS, the API server (`apiServer.cidrs:port`, by endpoint address and port), TCP 443 to public addresses (GitHub).
- Runner: no ingress. Egress: DNS, the internal server port, TCP 443 except `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`,
  `169.254.0.0/16`.
- Refresher: egress DNS and the API server.

What the address rules mean (S3, measured on kind; the k3s half is a maintainer step and not measured yet): kind enforces policies.
A rule for the API server names the endpoint's address and port from `endpoints/kubernetes` (`172.18.0.2/32` port 6443 on kind),
not the service address, which is translated before the policy is evaluated. The runner's 443-only rule keeps it from an
API server on 6443 by port; an API server on 443 with a public address would not be protected by the `except` list (inferred, not
measured), while the usual homelab node ranges are inside the excepted ranges. Pod addresses are in `10.0.0.0/8`, so traffic to a pod
is allowed by a pod or namespace selector, never by an address.

## 6. The server changes

Three small changes, all test-first. The first two are in K-1, the third in K-3:

1. **Two listeners.** `REMEDY_INTERNAL_ADDR` (default empty). Empty keeps today's behaviour: one port serves everything (local
   `make dev-server`, the existing Docker command). When set, the main port stops serving `/runner/v1/*` and `/mcp`, and a
   second `http.Server` serves exactly those. The shutdown and the two timeouts follow the existing server.
2. **`remedy-tokenrefresh`**, a second binary in the server image (`cmd/remedy-tokenrefresh`, stdlib only). It reads its own
   service account token, calls the TokenRequest endpoint for `remedy-write` (a fixed name from a flag) and updates the one
   Secret. It has no other behaviour, uses `internal/kube`'s transport rules (HTTPS, CA file, no redirect) and is tested against
   `kubetest`'s fake API server. The image runs it as `command` of the CronJob; no kubectl image and no shell are needed.

3. **A write token that is not there yet is a warning.** `kube.Config.Validate` today refuses to start when the write token
   file is missing or empty. In the cluster the file is filled by the refresher after the first start (the Secret is empty at
   install, and an install that waits for the server to be ready would otherwise never reach the job that fills it). A missing
   or empty write token file is now a warning at start-up, and every action fails closed until the file holds a token (the file
   is read again at every request, as before). The read token stays strict.

None of them touches the gatekeeper, the store or the runner protocol.

## 7. The dummy setup

Everything lives in `dev/kind/` next to the existing testbed and reuses its cluster (`remedy-dev`), its demo workloads, Argo CD
and `check-rbac.sh`.

Cluster: `kind.yaml` gets `extraMounts` (host `~/remedy-kind/claude` to `/mnt/remedy-claude`) and an `extraPortMappings` entry
(host `18080` to the node's NodePort for the public Service; `8080` is taken by `make dev-server`). The scripts create the
host directory (`0700`) before the cluster exists, because a directory Docker creates would belong to root. S1 measured that
mode `0700` is enough on Docker Desktop for Mac: the pod writes as uid 65532, the file belongs to the host user and survives
`kind delete cluster`, and the hostPath PV needs no change. On a Linux Docker host a bind mount keeps the real owner, so the
directory would need a `chown` to 65532 or a world-writable mode; that was not measured and the dummy does not claim to run there.
An existing cluster without the mount is detected (`docker inspect` of the node) and the script says to run `make dummy-down`.

Release: namespace `remedy-system`, `helm upgrade --install` with a values file `dev/kind/dummy-values.yaml`: NodePort service,
a hostPath PV and its claim for `runner.persistence.existingClaim`, `cluster.enabled` and `cluster.write.enabled` with
`namespaces: [demo]`, `networkPolicy.enabled: true` (S3: kind enforces policies; `dummy-up` fills `apiServer.cidrs` from
`endpoints/kubernetes` at install time, because the node's Docker address differs between machines), locally built images (`remedy-server:<tag>`,
`remedy-runner:<tag>`, `kind load docker-image`, `imagePullPolicy: IfNotPresent`). The tag changes with every build so that
`dummy-redeploy` rolls the pods. The script waits for the namespace's default ServiceAccount before it creates anything in it (S1: a pod
made right after the namespace is refused until that account exists).

State: `~/remedy-kind/dummy.env` (mode `0600`) holds the generated admin password, runner token and master key and the URL, so
a person or an agent can read the URL and the password. `dummy-up` keeps the values if the file exists and the Secret in the
cluster matches; `dummy-down` removes the cluster and this file, never `claude/`.

| Target | Does |
|---|---|
| `make dummy-up` | prerequisites check; directory; cluster; demo workloads; Argo CD; images build and load; Secret; `helm upgrade --install`; waits for the server; prints the URL and the path of `dummy.env` (not the password) and whether the runner is logged in (it cannot read the login; it asks the runner: after K-6, before that it says how to run `dummy-login`) On a fresh install (a release that is not `deployed`) it restarts the control plane once, after the hook filled the write token (decision of 2026-10-09: the pod mounts the Secret optional, and an approved action failed once shortly after a cold start). |
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
(`REMEDY_RUN_TIMEOUT` applies) and asserts: status `succeeded`, at least one `cluster_*` read tool call succeeded, no denied call. It
then starts a second run asking for a restart of `demo/web`, waits for the approval, approves **only** a restart of
`demo/web` (any other approval is denied and fails the test), and asserts that the deployment's `restartedAt` annotation is
at or after the approval and that the rollout completed. A run that ends with "Not logged in" prints "run `make dummy-login`" and exits non-zero. The script reads no
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

Results (2026-10-07): S4 (`docs/research/k8s-s4-cli-install.md`) fixed the install route, the pin and the base image of sections 3.2 and 4; S1
(`k8s-s1-hostpath-login.md`) confirmed the host directory and the PV for Docker Desktop on macOS; S3 (`k8s-s3-networkpolicy.md`) measured kind
(enforced; the k3s half is a maintainer step and still open); S2 (`k8s-s2-login-check.md`) chose `claude auth status --text` for K-6; S5
(`k8s-s5-login-relay.md`) is described in section 10.1. The corrections they caused in the plans are listed at the top of `docs/plans/k8s-0-spikes.md`.

## 10. Sub-plans

| Plan | Content | Needs |
|---|---|---|
| spikes S1 to S4 | as above | this spec |
| **K-1** | Amends `design.md` §2.7 and the README; the internal listener; `remedy-tokenrefresh`; `remedy-runner install-cli`; `Dockerfile` and `Dockerfile.runner`; `make images` | S4 |
| **K-2** | The chart's core: server, runner with init container, Services, PVCs, Ingress, NetworkPolicies, `existingSecret`, `make chart-check` with the render tests | K-1, S3 |
| **K-3** | The cluster identities: the tolerant write token (server change 3), ServiceAccounts, RBAC read and write, the refresher CronJob and hook, render tests, a proof on a real kind cluster | K-2 |
| **K-4** | The dummy setup: `kind.yaml`, scripts, values, targets, `smoke.sh`, the login check script, the runbook `docs/runbook/dummy-setup.md`, a real run recorded in `docs/research/` | K-3, S1 |
| **K-5** | Image pipeline, Renovate, release checks, `docs/runbook/homelab-deploy.md` | K-2 |
| **K-6** | Runner status: the status listener and probes, the report to the server, an API field, the Setup page, `dummy-up` reports the login | K-4, S2 |

K-4 and K-5 can run in parallel after K-3 and K-2. K-6 is last on purpose. Each plan is test first where there is logic
(Go code, render assertions, the smoke script's own checks with a fake `curl`), and each UI change follows the UI rules of
`CLAUDE.md` (a real browser check).

### 10.1 Follow-up under discussion: the login from the web UI

The maintainer wished on 2026-10-07 to log the CLI in from the Remedy web UI instead of `kubectl exec -it` (D2, section 4). The way chosen
is A: the runner starts `claude auth login`, the control plane shows the login URL that it prints, the maintainer opens it and pastes the
one-time code into a field of the UI, and the control plane hands the code to the runner, which writes it to the CLI's standard input.
Spike S5 ([`docs/research/k8s-s5-login-relay.md`](../research/k8s-s5-login-relay.md)) found this feasible without a TTY: the URL and the
prompt for the code are printed on stdout, and a line on a piped stdin is read and judged (measured with an invalid code only; a valid code,
a code without a newline and a second attempt in the same process are not measured). This would be the first place where Remedy handles
login material: the URL carries OAuth state and a PKCE challenge, and the code is a one-time secret, which would stay in memory and never be
stored or logged. That conflicts with the hard rule of `CLAUDE.md` (never read, copy, log or store the agent CLIs' credentials) unless
`docs/design.md` records the exception. It therefore needs its own design decision, spec and plan (K-7) before any code, and nothing in K-1 to
K-6 prepares for it. Until then the login stays the `kubectl exec -it` of section 4.

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
