# K-4: The Dummy Setup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `make dummy-up` builds a kind cluster with the demo workloads and Argo CD, builds and loads both images, and installs the Helm chart with the real agent; `make dummy-down` removes it again and keeps the CLI login; `make dummy-smoke` runs a real ad-hoc cluster run and an approved restart against it and asserts states. A person or an agent can run all of it from short `make` commands.

**Architecture:** The scripts live in `dev/kind/` next to the existing testbed and share one `lib.sh`. The cluster is the testbed's (`remedy-dev`), with a host directory mounted into its node for the CLI login and one port mapped for the UI. The chart is installed with `deploy/cli-pin.yaml` (the pinned CLI, one source of truth for the dummy and the homelab) and `dev/kind/dummy-values.yaml`. Generated secrets live in `~/remedy-kind/dummy.env`. The smoke test is a shell script whose decisions are pure `jq` functions with their own tests.

**Tech Stack:** POSIX `sh`, Docker, kind, kubectl, helm, jq, curl. No new dependencies.

**Spec:** [`docs/specs/2026-10-06-kubernetes-deployment-design.md`](../specs/2026-10-06-kubernetes-deployment-design.md), sections 2 (D1, D2, D10), 5 (the CLI credentials row) and 7. Plans [K-1](k8s-1-images-and-listener.md), [K-2](k8s-2-chart-core.md) and [K-3](k8s-3-cluster-identities.md) are done. The records of the spikes fix three inputs: S1 ([`docs/research/k8s-s1-hostpath-login.md`](../research/k8s-s1-hostpath-login.md)) the host directory's mode and the PV, S3 ([`docs/research/k8s-s3-networkpolicy.md`](../research/k8s-s3-networkpolicy.md)) whether the dummy enables the policies, S4 ([`docs/research/k8s-s4-cli-install.md`](../research/k8s-s4-cli-install.md)) the CLI pin.

**Scope note:** The login itself is a human step (`make dummy-login`), once. The real-run record of task 6 needs that login, so it is the one task an agent cannot finish alone; it stops and asks. Nothing here changes Go code or the chart.

## Decisions made while planning

| Topic | Spec said | This plan |
|---|---|---|
| `make dummy-down` | its own script | It is `dev/kind/down.sh`, which now keeps `claude/`. The old script removed the whole `~/remedy-kind`, login directory included; that would have defeated D2. |
| `dummy-up` prints the password | "prints URL and password" | It prints the URL and the path of `dummy.env`, not the password: a password in a terminal ends up in scrollback and in an agent's transcript, a file does not. An agent reads the file. |
| Smoke: "no refused call" | no refused call | No `denied` call and no approval but the one for `demo/web`'s restart. A read call that failed once and was retried by the agent is not a failure of the setup, so failed calls are not counted. |
| The CLI pin | in `values.yaml` | `deploy/cli-pin.yaml`, a values file of its own. The dummy and the homelab (as an Argo values file) use the same one, and Renovate (plan K-5) edits one place. The chart's `values.yaml` keeps the keys empty. |
| One cluster, two flows | the dummy reuses the testbed | `up.sh` (host-run server, RBAC applied from `rbac.yaml`) and `dummy-up.sh` (chart-installed, RBAC from the chart) cannot share a running cluster: the account names collide. `dummy-up.sh` detects the testbed's accounts and says to run `down.sh` first. |
| Host port | `18080` | Host `127.0.0.1:18080` to the node's NodePort `30080`; the dummy sets the public Service to that NodePort. |
| `kind.yaml` | gets `extraMounts` | It becomes a template (`__CLAUDE_DIR__`): kind needs an absolute host path and does not expand `~` or variables. `lib.sh` renders it into `~/remedy-kind/kind-rendered.yaml`. |
| Password rotation | not mentioned | `dummy.env` is made once and reused until `down.sh` removes it with the cluster, so the master key always matches the database it sealed. |
| Run-ending assertion for the restart | "pods created after the approval" | The deployment's `restartedAt` annotation is at or after the moment of the approval (minus the one-second resolution) and the rollout completed. Pod creation times of a rolling restart overlap with the old pods' deletion and would make the check flaky. |
| The host directory's mode (spike S1) | "`0700`" | Confirmed by S1's record: mode `700` works with the PV of `s1-storage.yaml` unchanged (no `777`, no `chown`), the pod writes as uid 65532 into a directory owned by the host user, and the file survives `kind delete cluster`. The result holds for **Docker Desktop on macOS only**: on a Linux Docker host a bind mount keeps the real owner, so the directory would need a `chown` to 65532 or a world-writable mode. That was not measured and the dummy setup does not claim to run there. |
| A pod made right after its namespace (spike S1) | not mentioned | The namespace's `default` ServiceAccount appears a moment after the namespace, and a pod created before it is refused (`serviceaccount "default" not found`). `lib.sh` has `wait_default_serviceaccount` (up to 60 s), and `dummy-up.sh` calls it after applying the namespace. |
| Network policies in the dummy (spike S3, kind half) | "as the spike S3 allows" | `networkPolicy.enabled: true`: kind's default CNI enforces policies, and the rule `ipBlock <endpoint>/32` on the endpoint port (6443) lets the control plane reach the API server. `deploy_release` already fills `apiServer.cidrs` from `endpoints/kubernetes` at install time, which S3 requires because the node's Docker address (`172.18.0.2` in the record) differs between machines. The k3s half of S3 is a maintainer step and says nothing about this plan. |
| The CLI pin (spike S4) | values from the record | `deploy/cli-pin.yaml` shows the record's values: version `2.1.292`, `archive: none`, `linux-x64` and `linux-arm64`, and the two SHA-256 values. The vendor publishes them in a signed `manifest.json`, so step 2 of task 2 checks that each of the six values (version, URL template, both platform names, both checksums) is present in the file: the block prints `MISSING: <value>`, skips the render and exits non-zero if one is not, instead of only checking that no marker is left. It compares the file with the record, not with the vendor. |
| Does an update write under `$HOME/.local/share/claude`? (spike S4) | not mentioned | S4 could not measure it: with `HOME=/state` the native installer's directory would be on the writable state volume, so the read-only `/opt/claude` does not by itself keep an update out. Task 6 step 4a checks after the login and the smoke test that `/state/.local/share/claude` is absent. The runner image has no shell, so the check looks at the host side of the volume, by name only. |

## Global Constraints

- Everything committed is English: docs, scripts, comments, commit messages.
- No script opens a file in the login directory. They create it (`mkdir`, `chmod`), delete its contents on `dummy-logout` (`find … -delete`), and substitute its path into the kind configuration. `scripts/check-login-dir-untouched.sh` enforces that and runs in `make check`.
- `dev/kind/down.sh` never removes `~/remedy-kind/claude`. Only `make dummy-logout` does, after a confirmation.
- The admin password, the runner token and the master key are generated by `openssl rand`, live only in `~/remedy-kind/dummy.env` (mode `0600`, directory `0700`, outside the repository) and in the cluster's Secret, and are never printed, logged or committed. They are passed to `kubectl` through a temporary `0600` file, never as arguments.
- The smoke test sends the admin password only to the dummy's own URL, approves exactly one tool call (`cluster_rollout_restart` of `deployment demo/web`) and denies every other.
- Every target except `dummy-login` and `dummy-logout` is non-interactive, exits non-zero on failure and says in plain lines what it did.
- Shell runs on macOS and on Linux: no BSD-only flags (`sed -i ''`, `stat -f`, `date -j`).
- `make check` passes at the end of every task. Every commit message ends with the trailer `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`.

## Review Focus

- `make dummy-down` followed by `make dummy-up`: the login directory and its content survive; `dummy.env` and the cluster are new (task 3 and the real run of task 6).
- A cluster that was created by `up.sh` or before the mount existed: `dummy-up` stops with a message that names the fix instead of failing inside Helm (task 3).
- A smoke run where the agent asks to restart something else, or to delete a pod: denied, and the test says so and fails (task 4's tests pin the decision function).
- A run that ends "Not logged in": the test prints `run make dummy-login` and exits non-zero, not a bare assertion failure (task 4).
- A script that reads the login: the check fails on a `cat "$CLAUDE_DIR/…"` or a `tar` of `$OUT` (task 5).

## How to read the code blocks

A line `Create `path`:` or `Overwrite `path`:` is followed by the complete file. A line `In `path`, replace:` is followed by a block with the exact old text, a line `with:` and a block with the new text; the old text occurs exactly once in the file. Shell uses two-space indentation.

## File Structure

| Path | Responsibility |
|---|---|
| `dev/kind/lib.sh` | Shared variables and functions of the scripts; runs nothing when sourced |
| `dev/kind/kind.yaml` | The cluster definition, as a template, with the mount and the port |
| `dev/kind/up.sh`, `down.sh` | The host-run testbed, on `lib.sh`; `down.sh` keeps the login |
| `dev/kind/dummy-namespace.yaml`, `dummy-storage.yaml`, `dummy-values.yaml` | The namespace, the login volume, the chart's values |
| `deploy/cli-pin.yaml` | The pinned CLI as a values file |
| `dev/kind/dummy-up.sh`, `dummy-redeploy.sh`, `dummy-reset.sh`, `dummy-login.sh`, `dummy-logout.sh`, `dummy-status.sh`, `dummy-logs.sh` | The targets' scripts |
| `dev/kind/smoke-lib.sh`, `smoke-lib_test.sh`, `smoke.sh` | The smoke test and the tests of its decisions |
| `scripts/check-login-dir-untouched.sh`, `scripts/check-login-dir-untouched_test.sh` | The rule "no script reads the login" as a check |
| `Makefile`, `.github/workflows/ci.yml` | `dummy-*` and `shell-test` targets, a CI job |
| `docs/runbook/dummy-setup.md`, `docs/research/k8s-dummy-real-run.md`, `dev/kind/README.md`, `CLAUDE.md` | Usage and the record |

---

### Task 1: One library, a cluster definition with the mount, and a `down.sh` that keeps the login

**Files:**
- Create: `dev/kind/lib.sh`
- Overwrite: `dev/kind/kind.yaml`, `dev/kind/up.sh`, `dev/kind/down.sh`

**Interfaces:**
- Produces (for every later script; names are fixed): variables `CLUSTER` (`remedy-dev`), `CTX` (`kind-remedy-dev`), `ARGOCD_VERSION`, `KIND_DIR`, `REPO_ROOT`, `OUT` (`${REMEDY_KIND_DIR:-$HOME/remedy-kind}`), `CLAUDE_DIR` (`$OUT/claude`), `CLAUDE_DIR_MODE`, `DUMMY_ENV` (`$OUT/dummy.env`), `NS` (`remedy-system`), `RELEASE` (`remedy`); functions `need`, `k`, `ensure_out_dir`, `ensure_claude_dir`, `ensure_cluster`, `cluster_has_claude_mount`, `install_demo`, `install_argocd`.

Before this task read `docs/research/k8s-s1-hostpath-login.md`. Its decision block gives the mode that works for the host directory: `700`, on Docker Desktop for Mac, with nothing to `chown` and the PV spec of its `s1-storage.yaml` unchanged. `CLAUDE_DIR_MODE` below is therefore `700`. If the record is ever re-measured and says an extra step is needed after the pod writes (a `chown`, a different mode), change `CLAUDE_DIR_MODE` and add that step to `dummy-login.sh` in task 3. The result is for Docker Desktop on macOS only; the runbook says so (task 6 step 7).

- [ ] **Step 1: The shared library**

Create `dev/kind/lib.sh`:

```sh
# Shared by the scripts of dev/kind. Source it from a script in this directory: it sets variables and defines
# functions, and runs nothing. The scripts never open a file in $CLAUDE_DIR: it holds the CLI's login, which Remedy
# never reads (scripts/check-login-dir-untouched.sh enforces that).

CLUSTER=remedy-dev
CTX=kind-$CLUSTER
ARGOCD_VERSION=v3.5.3
KIND_DIR=$(cd "$(dirname "$0")" && pwd)
REPO_ROOT=$(cd "$KIND_DIR/../.." && pwd)
# Outside the repository on purpose: it holds credentials.
OUT=${REMEDY_KIND_DIR:-$HOME/remedy-kind}
CLAUDE_DIR=$OUT/claude
CLAUDE_DIR_MODE=700
DUMMY_ENV=$OUT/dummy.env
NS=remedy-system
RELEASE=remedy

# need <tool>...: stops when a tool is missing.
need() {
  for tool in "$@"; do
    command -v "$tool" > /dev/null || { echo "$tool is needed" >&2; exit 1; }
  done
}

# k runs kubectl against the testbed's cluster.
k() { kubectl --context "$CTX" "$@"; }

ensure_out_dir() {
  (umask 077; mkdir -p "$OUT")
  chmod 700 "$OUT"
}

# The login directory must exist before the cluster does: kind mounts it into the node, and a directory that Docker
# creates itself would belong to root.
ensure_claude_dir() {
  mkdir -p "$CLAUDE_DIR"
  chmod "$CLAUDE_DIR_MODE" "$CLAUDE_DIR"
}

# ensure_cluster creates the kind cluster from kind.yaml, with the host directory mounted, unless it exists.
ensure_cluster() {
  ensure_out_dir
  ensure_claude_dir
  if kind get clusters 2> /dev/null | grep -qx "$CLUSTER"; then
    echo "cluster $CLUSTER exists"
    return 0
  fi
  rendered=$OUT/kind-rendered.yaml
  sed "s|__CLAUDE_DIR__|$CLAUDE_DIR|" "$KIND_DIR/kind.yaml" > "$rendered"
  kind create cluster --config "$rendered"
}

# cluster_has_claude_mount says whether the node of the cluster has the login directory mounted. A cluster made
# before the mount existed does not, and only a new cluster can get one.
cluster_has_claude_mount() {
  docker inspect "$CLUSTER-control-plane" --format '{{range .Mounts}}{{.Destination}}{{"\n"}}{{end}}' 2> /dev/null |
    grep -qx /mnt/remedy-claude
}

install_demo() {
  k apply -f "$KIND_DIR/demo.yaml"
}

# install_argocd installs the core installation of Argo CD (no UI, no API server: the controller, the repo server and
# Redis) and the application guestbook. Server-side apply because its CRDs are too large for the annotation a
# client-side apply adds.
install_argocd() {
  k create namespace argocd --dry-run=client -o yaml | k apply -f -
  k apply -n argocd --server-side --force-conflicts \
    -f "https://raw.githubusercontent.com/argoproj/argo-cd/$ARGOCD_VERSION/manifests/core-install.yaml"
  k wait --for=condition=Established crd/applications.argoproj.io --timeout=120s
  k -n argocd rollout status deployment/argocd-repo-server --timeout=300s
  k -n argocd rollout status statefulset/argocd-application-controller --timeout=300s
  k apply -f "$KIND_DIR/guestbook.yaml"
}
```

- [ ] **Step 2: The cluster definition as a template**

Overwrite `dev/kind/kind.yaml`:

```yaml
# A single-node cluster for trying Remedy: the cluster tools of the host-run testbed (up.sh) and the chart-installed
# dummy setup (dummy-up.sh). lib.sh renders it: kind needs an absolute host path and does not expand variables, so
# __CLAUDE_DIR__ is replaced by ~/remedy-kind/claude before the cluster is created.
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
name: remedy-dev
nodes:
  - role: control-plane
    extraMounts:
      # The CLI's login lives here, on the host, so that it survives deleting the cluster. The runner's volume is a
      # hostPath volume on this path (dummy-storage.yaml).
      - hostPath: __CLAUDE_DIR__
        containerPath: /mnt/remedy-claude
    extraPortMappings:
      # The UI of the dummy setup: http://127.0.0.1:18080 -> the node's NodePort 30080 (the chart's public Service).
      - containerPort: 30080
        hostPort: 18080
        listenAddress: 127.0.0.1
```

- [ ] **Step 3: `up.sh` on the library**

Overwrite `dev/kind/up.sh`:

```sh
#!/bin/sh
# Starts the kind cluster for trying Remedy's cluster tools with a server on the host: demo workloads, two service
# accounts with their roles, a pinned Argo CD, and the files the control plane needs.
# Usage: dev/kind/up.sh [output directory]
# The output directory (default ~/remedy-kind) gets the CA, one token file per identity and an env.sh. It is outside
# the repository on purpose: it holds credentials. For the chart-installed setup use dummy-up.sh instead; the two do not
# share a running cluster (the account names collide).
set -eu
[ $# -gt 0 ] && REMEDY_KIND_DIR=$1
. "$(dirname "$0")/lib.sh"
need docker kind kubectl openssl

ensure_cluster
install_demo
k apply -f "$KIND_DIR/rbac.yaml"
install_argocd
k apply -f "$KIND_DIR/argocd-rbac.yaml"

server=$(k config view --raw --minify -o jsonpath='{.clusters[0].cluster.server}')
k config view --raw --minify -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' | openssl base64 -d -A > "$OUT/ca.crt"
(umask 077
 k -n remedy-system create token remedy-read --duration 24h > "$OUT/read.token"
 k -n remedy-system create token remedy-write --duration 24h > "$OUT/write.token"
 cat > "$OUT/env.sh" <<EOF
export REMEDY_K8S_API='$server'
export REMEDY_K8S_CA_FILE='$OUT/ca.crt'
export REMEDY_K8S_READ_TOKEN_FILE='$OUT/read.token'
export REMEDY_K8S_WRITE_TOKEN_FILE='$OUT/write.token'
export REMEDY_K8S_WRITE_NAMESPACES='demo'
EOF
)
echo
echo "Ready. The tokens are good for 24 hours; run this script again for new ones."
echo "  . $OUT/env.sh   # then start remedy-server"
```

- [ ] **Step 4: `down.sh` that keeps the login**

Overwrite `dev/kind/down.sh`:

```sh
#!/bin/sh
# Removes the kind cluster and the files the scripts generated: the tokens, the env files, the rendered cluster
# definition, dummy.env. It KEEPS ~/remedy-kind/claude, the CLI's login: `make dummy-logout` removes that, after a
# confirmation. This is also `make dummy-down`. Usage: dev/kind/down.sh [output directory]
set -eu
[ $# -gt 0 ] && REMEDY_KIND_DIR=$1
. "$(dirname "$0")/lib.sh"
need kind

kind delete cluster --name "$CLUSTER"
if [ -d "$OUT" ]; then
  find "$OUT" -mindepth 1 -maxdepth 1 ! -name claude -exec rm -rf {} +
fi
echo "removed the cluster and what the scripts generated in $OUT; the login in $CLAUDE_DIR stays"
```

- [ ] **Step 5: Check the host-run flow still works, and that `down.sh` keeps the login directory**

```sh
chmod +x dev/kind/up.sh dev/kind/down.sh
sh -n dev/kind/lib.sh dev/kind/up.sh dev/kind/down.sh && echo syntax ok
dev/kind/up.sh
. ~/remedy-kind/env.sh && dev/kind/check-rbac.sh | tail -4
docker inspect remedy-dev-control-plane --format '{{range .Mounts}}{{.Destination}} {{end}}'
touch ~/remedy-kind/claude/keep-me
dev/kind/down.sh
command ls -A ~/remedy-kind
```

Expected: `syntax ok`; `up.sh` ends with `Ready.`; `check-rbac.sh` prints rows ending `(expected 200)` or `(expected 403)` with the matching status (compare with the table in `dev/kind/README.md`); the mount list includes `/mnt/remedy-claude`; after `down.sh` the listing shows only `claude`. Then `rm ~/remedy-kind/claude/keep-me`.

- [ ] **Step 6: Commit**

```bash
git add dev/kind
git commit -m "refactor(kind): one library for the scripts, a cluster definition with the login mount, and a down.sh that keeps the login

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: What the chart needs in kind

**Files:**
- Create: `deploy/cli-pin.yaml`, `dev/kind/dummy-namespace.yaml`, `dev/kind/dummy-storage.yaml`, `dev/kind/dummy-values.yaml`

**Interfaces:**
- Consumes: the S4 record (`docs/research/k8s-s4-cli-install.md`, its Decision block), the S1 record (its PV spec), the S3 record (its `chart default networkPolicy` line).
- Produces: `deploy/cli-pin.yaml` (a values file: `runner.cli.*`), the namespace `remedy-system` labelled `pod-security.kubernetes.io/enforce: restricted`, the PV `remedy-claude` and the PVC `claude-state`, the dummy's values (NodePort 30080, the claim, the cluster tools on for `demo`).

- [ ] **Step 1: The CLI pin**

Create `deploy/cli-pin.yaml`. Every value comes from the Decision block of `docs/research/k8s-s4-cli-install.md` (version `2.1.292`, downloaded for both platforms on 2026-10-07; the checksums equal the ones the vendor publishes in the signed `manifest.json` of that release). Check the file against the record's Decision block before you commit it; if the record has been re-measured with another version, use its values and keep the structure:

```yaml
# The pinned claude CLI, as a values file. The dummy setup (dev/kind/dummy-up.sh) and the homelab (an Argo CD values file)
# both use it, so there is one place to bump. No image contains the CLI: the runner pod's init container installs this
# version and verifies the SHA-256. The vendor publishes the checksums in
# https://downloads.claude.ai/claude-code-releases/<version>/manifest.json (platforms.<platform>.checksum, signed by
# manifest.json.sig). scripts/cli-checksums.sh (plan K-5) computes and compares them for a new version.
runner:
  cli:
    version: "2.1.292"
    urlTemplate: "https://downloads.claude.ai/claude-code-releases/{version}/{platform}/claude"
    archive: none
    member: ""
    platforms:
      amd64: {name: "linux-x64", sha256: "a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3"}
      arm64: {name: "linux-arm64", sha256: "24caa9e6ff13bf227049a2626f1c816fc895023050f0ec3b12dbf14d897367e0"}
```

- [ ] **Step 2: Check that every value of the record is in the file and that the chart accepts it**

```sh
missing=0
for want in '2.1.292' 'https://downloads.claude.ai/claude-code-releases/{version}/{platform}/claude' 'linux-x64' 'linux-arm64' \
            'a967e7b1d8b4e47ee421d5433027880347952b0c0857abf880e2c942a4ec93b3' '24caa9e6ff13bf227049a2626f1c816fc895023050f0ec3b12dbf14d897367e0'; do
  grep -qF -- "$want" deploy/cli-pin.yaml || { echo "MISSING: $want"; missing=1; }
done
[ "$missing" -eq 0 ] && helm template remedy deploy/chart -n remedy-system -f deploy/cli-pin.yaml --set existingSecret.name=x > /dev/null && echo chart accepts the pin
```

Expected: no `MISSING` line and the last line `chart accepts the pin`. If any value is absent the block prints `MISSING: <value>` for it, does not run `helm template`, does not print `chart accepts the pin`, and exits non-zero (the exit status of the last line). A `MISSING` line means a value of the record was mistyped or dropped (compare the 64 hex digits character by character with the record: a wrong checksum fails the pod's init container, not the render). If `helm template` complains about `runner.cli.platforms.<arch>.sha256 is required`, a checksum is missing.

- [ ] **Step 3: The namespace and the login volume**

Create `dev/kind/dummy-namespace.yaml`:

```yaml
# The dummy setup's namespace. The Pod Security Standard "restricted" is enforced: every pod the chart makes must pass it,
# which is what the chart's hardening is for.
apiVersion: v1
kind: Namespace
metadata:
  name: remedy-system
  labels:
    pod-security.kubernetes.io/enforce: restricted
```

Create `dev/kind/dummy-storage.yaml` (the PV spec is the one of S1's record; this is its shape, with the claim pinned so that nothing else can bind it):

```yaml
# The runner's state volume in the dummy setup: the CLI's login. It is a hostPath volume on the directory that kind
# mounts from the host (kind.yaml), so the login survives deleting the cluster. The claim is bound to this volume.
apiVersion: v1
kind: PersistentVolume
metadata:
  name: remedy-claude
spec:
  capacity: {storage: 1Gi}
  accessModes: [ReadWriteOnce]
  persistentVolumeReclaimPolicy: Retain
  storageClassName: remedy-host
  claimRef: {namespace: remedy-system, name: claude-state}
  hostPath: {path: /mnt/remedy-claude, type: Directory}
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: claude-state
  namespace: remedy-system
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: remedy-host
  volumeName: remedy-claude
  resources: {requests: {storage: 1Gi}}
```

If S1's record says a different PV spec worked, use that one instead and keep the `claimRef`.

- [ ] **Step 4: The values**

Create `dev/kind/dummy-values.yaml`. `networkPolicy.enabled` is `true`: the S3 record's `chart default networkPolicy` / `dummy values` line says kind enforces policies and the API rule by endpoint address and port works (measured on kind, 2026-10-07; the file is only wrong if that record is re-measured). The public peer is the whole world because the NodePort's traffic comes from outside the cluster:

```yaml
# The chart's values for the dummy setup. dummy-up.sh adds the image tags and the API server's address
# (networkPolicy.apiServer.cidrs) with --set, and deploy/cli-pin.yaml supplies the pinned CLI.
image:
  pullPolicy: IfNotPresent
  server: {repository: remedy-server}
  runner: {repository: remedy-runner}

existingSecret:
  name: remedy-secrets

server:
  service:
    type: NodePort
    nodePort: 30080     # kind.yaml maps it to http://127.0.0.1:18080
  env:
    REMEDY_LOG_LEVEL: debug   # one line per tool call: run, tool, kind, replay, refused; never the arguments

runner:
  model: sonnet         # the CLI runs with --restricted, which ignores settings: the model is always pinned
  persistence:
    existingClaim: claude-state

cluster:
  enabled: true
  write:
    enabled: true
    namespaces: [demo]

networkPolicy:
  enabled: true         # kind enforces policies (spike S3); dummy-up.sh adds the API server's address and its port 6443
  public:
    from:
      - ipBlock: {cidr: 0.0.0.0/0}
```

- [ ] **Step 5: Check the whole thing renders for the dummy**

Run: `helm template remedy deploy/chart -n remedy-system -f deploy/cli-pin.yaml -f dev/kind/dummy-values.yaml --set image.server.tag=t --set image.runner.tag=t --set 'networkPolicy.apiServer.cidrs={172.18.0.2/32}' | kubeconform -strict -summary 2>&1 | tail -3`
Expected: `Valid: N, Invalid: 0, Errors: 0, Skipped: 0` (skip this check with a note if `kubeconform` is not installed; `make chart-check` covers the chart's own values).

- [ ] **Step 6: Commit**

```bash
git add deploy/cli-pin.yaml dev/kind/dummy-namespace.yaml dev/kind/dummy-storage.yaml dev/kind/dummy-values.yaml
git commit -m "feat(kind): the pinned CLI as a values file, and the dummy setup's namespace, login volume and values

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: The targets

**Files:**
- Modify: `dev/kind/lib.sh` (append functions)
- Create: `dev/kind/dummy-up.sh`, `dummy-redeploy.sh`, `dummy-reset.sh`, `dummy-login.sh`, `dummy-logout.sh`, `dummy-status.sh`, `dummy-logs.sh`
- Modify: `Makefile`

**Interfaces:**
- Consumes: task 1's library, task 2's files, `make images IMAGE_TAG=...` of plan K-1.
- Produces: functions `write_dummy_env`, `load_dummy_env` (sets `REMEDY_URL`, `REMEDY_ADMIN_PASSWORD`, `REMEDY_RUNNER_TOKEN`, `REMEDY_MASTER_KEY`), `new_tag` (sets `TAG`), `build_and_load_images`, `apply_secret`, `deploy_release`, `wait_default_serviceaccount`, `require_dummy`; the targets `dummy-up`, `dummy-down`, `dummy-login`, `dummy-logout`, `dummy-redeploy`, `dummy-reset`, `dummy-status`, `dummy-logs`.

- [ ] **Step 1: The deployment functions**

Append to `dev/kind/lib.sh`:

```sh

# dummy.env holds what the dummy setup generated: the URL and the three application secrets. It is made once and kept until
# down.sh removes it together with the cluster, so the master key always matches the database it sealed.
write_dummy_env() {
  [ -f "$DUMMY_ENV" ] && return 0
  (umask 077
   {
     echo "REMEDY_URL=http://127.0.0.1:18080"
     echo "REMEDY_ADMIN_PASSWORD=$(openssl rand -hex 12)"
     echo "REMEDY_RUNNER_TOKEN=$(openssl rand -hex 24)"
     echo "REMEDY_MASTER_KEY=$(openssl rand -base64 32)"
   } > "$DUMMY_ENV")
}

# load_dummy_env puts the values of dummy.env into the environment of the script.
load_dummy_env() {
  [ -f "$DUMMY_ENV" ] || { echo "there is no $DUMMY_ENV: run make dummy-up" >&2; exit 1; }
  set -a
  . "$DUMMY_ENV"
  set +a
}

# require_dummy stops unless the cluster and the release exist.
require_dummy() {
  kind get clusters 2> /dev/null | grep -qx "$CLUSTER" || { echo "there is no cluster $CLUSTER: run make dummy-up" >&2; exit 1; }
  helm --kube-context "$CTX" -n "$NS" status "$RELEASE" > /dev/null 2>&1 || { echo "Remedy is not installed in $NS: run make dummy-up" >&2; exit 1; }
}

# new_tag sets TAG to a tag that no image has had, so that a rebuilt image always rolls the pods.
new_tag() { TAG=dev-$(date +%Y%m%d%H%M%S); }

# build_and_load_images builds both images for this machine and loads them into the cluster's node.
build_and_load_images() {
  make -C "$REPO_ROOT" images IMAGE_TAG="$TAG"
  kind load docker-image "remedy-server:$TAG" "remedy-runner:$TAG" --name "$CLUSTER"
}

# apply_secret makes the Secret the chart references. The values go to kubectl through a 0600 file, never as arguments.
apply_secret() {
  load_dummy_env
  tmp=$(umask 077; mktemp)
  {
    echo "admin-password=$REMEDY_ADMIN_PASSWORD"
    echo "runner-token=$REMEDY_RUNNER_TOKEN"
    echo "master-key=$REMEDY_MASTER_KEY"
  } > "$tmp"
  k -n "$NS" create secret generic remedy-secrets --from-env-file="$tmp" --dry-run=client -o yaml | k apply -f -
  rm -f "$tmp"
}

# deploy_release installs or upgrades the chart with the pinned CLI, the dummy's values, the image tags and the address of
# the API server (the network policy needs it). It waits for the control plane, the runner and the token hook.
deploy_release() {
  api_ip=$(k get endpoints kubernetes -o jsonpath='{.subsets[0].addresses[0].ip}')
  helm --kube-context "$CTX" upgrade --install "$RELEASE" "$REPO_ROOT/deploy/chart" -n "$NS" \
    -f "$REPO_ROOT/deploy/cli-pin.yaml" -f "$KIND_DIR/dummy-values.yaml" \
    --set "image.server.tag=$TAG" --set "image.runner.tag=$TAG" \
    --set "networkPolicy.apiServer.cidrs={$api_ip/32}" \
    --wait --timeout 10m
}

# wait_default_serviceaccount waits until the namespace's default ServiceAccount exists. A controller makes it a moment
# after the namespace, and a pod created before then is refused ("serviceaccount default not found"; spike S1).
wait_default_serviceaccount() {
  i=0
  until k -n "$NS" get serviceaccount default > /dev/null 2>&1; do
    i=$((i + 1))
    [ "$i" -le 60 ] || { echo "the default service account of $NS did not appear within 60 s" >&2; exit 1; }
    sleep 1
  done
}
```

- [ ] **Step 2: `dummy-up.sh`**

Create `dev/kind/dummy-up.sh`:

```sh
#!/bin/sh
# make dummy-up: a kind cluster with the demo workloads and Argo CD, both images built and loaded, and the Helm chart
# installed with the real agent. Safe to run again: it upgrades what exists. The CLI login is a separate, one-time step
# (make dummy-login).
set -eu
. "$(dirname "$0")/lib.sh"
need docker kind kubectl helm openssl jq curl make

ensure_cluster
if ! cluster_has_claude_mount; then
  echo "the cluster $CLUSTER has no mount for the login directory (it was made before the dummy setup existed)." >&2
  echo "run make dummy-down, then make dummy-up again; the login directory is kept." >&2
  exit 1
fi
# The host-run testbed (up.sh) applies rbac.yaml, which creates the accounts the chart creates too. Helm would refuse them.
if k -n "$NS" get serviceaccount remedy-read > /dev/null 2>&1 &&
   [ "$(k -n "$NS" get serviceaccount remedy-read -o jsonpath='{.metadata.labels.app\.kubernetes\.io/managed-by}')" != Helm ]; then
  echo "the cluster was started with dev/kind/up.sh, whose accounts collide with the chart's." >&2
  echo "run make dummy-down, then make dummy-up again." >&2
  exit 1
fi

install_demo
install_argocd
k apply -f "$KIND_DIR/dummy-namespace.yaml"
wait_default_serviceaccount
k apply -f "$KIND_DIR/dummy-storage.yaml"
write_dummy_env
new_tag
build_and_load_images
apply_secret
deploy_release

echo
echo "Ready."
echo "  url:       http://127.0.0.1:18080"
echo "  password:  REMEDY_ADMIN_PASSWORD in $DUMMY_ENV"
echo "  login:     make dummy-login   (once; the login survives make dummy-down)"
echo "  then:      make dummy-smoke   (two real runs on your subscription)"
echo "  status:    make dummy-status  /  logs: make dummy-logs  /  remove: make dummy-down"
```

- [ ] **Step 3: The other scripts**

Create `dev/kind/dummy-redeploy.sh`:

```sh
#!/bin/sh
# make dummy-redeploy: the fast loop. Builds both images again, loads them and upgrades the release. The cluster, the
# database and the login stay.
set -eu
. "$(dirname "$0")/lib.sh"
need docker kind kubectl helm make
require_dummy
new_tag
build_and_load_images
deploy_release
echo "redeployed with the tag $TAG"
```

Create `dev/kind/dummy-reset.sh`:

```sh
#!/bin/sh
# make dummy-reset: empties the control plane's database and keeps the cluster, the secrets and the login. Incidents, runs
# and sessions are gone; the admin signs in again.
set -eu
. "$(dirname "$0")/lib.sh"
need kind kubectl helm jq
require_dummy
# The release keeps its image tags: only the database is reset.
TAG=$(helm --kube-context "$CTX" -n "$NS" get values "$RELEASE" -o json | jq -r '.image.server.tag')
k -n "$NS" scale deployment/remedy-server --replicas=0
k -n "$NS" wait --for=delete pod -l app.kubernetes.io/component=server --timeout=120s || true
k -n "$NS" delete pvc remedy-data --wait=true
# Helm makes the missing claim again. A scaled-down Deployment stays scaled down on an upgrade, so scale it up by hand.
deploy_release
k -n "$NS" scale deployment/remedy-server --replicas=1
k -n "$NS" rollout status deployment/remedy-server --timeout=300s
echo "the database is empty again"
```

Create `dev/kind/dummy-login.sh`:

```sh
#!/bin/sh
# make dummy-login: the one-time login of the CLI in the runner pod. It runs the unmodified CLI interactively; the login
# is written by the CLI to the state volume, which is the host directory. Nothing here reads it.
set -eu
. "$(dirname "$0")/lib.sh"
need kubectl
k -n "$NS" get pod remedy-runner-0 > /dev/null 2>&1 || { echo "the runner pod does not exist: run make dummy-up" >&2; exit 1; }
echo "Type /login, open the URL in a browser, paste the code, then /exit."
exec kubectl --context "$CTX" -n "$NS" exec -it remedy-runner-0 -c runner -- /opt/claude/claude
```

Create `dev/kind/dummy-logout.sh`:

```sh
#!/bin/sh
# make dummy-logout: removes the CLI's login from the host directory, after a confirmation. This is the only script that
# touches the directory's content, and it only deletes.
set -eu
. "$(dirname "$0")/lib.sh"
[ -d "$CLAUDE_DIR" ] || { echo "there is no login directory at $CLAUDE_DIR"; exit 0; }
printf "Remove the CLI login in %s? [y/N] " "$CLAUDE_DIR"
read -r answer
[ "$answer" = y ] || { echo "kept"; exit 1; }
find "$CLAUDE_DIR" -mindepth 1 -delete
echo "removed the login; make dummy-login makes a new one"
```

Create `dev/kind/dummy-status.sh`:

```sh
#!/bin/sh
# make dummy-status: the state of the dummy setup as plain `key: value` lines, for a person or an agent.
set -eu
. "$(dirname "$0")/lib.sh"
need kubectl helm curl jq
if ! kind get clusters 2> /dev/null | grep -qx "$CLUSTER"; then
  echo "cluster: none"
  exit 1
fi
echo "cluster: $CLUSTER"
if ! helm --kube-context "$CTX" -n "$NS" status "$RELEASE" > /dev/null 2>&1; then
  echo "release: not installed"
  exit 1
fi
echo "release: $(helm --kube-context "$CTX" -n "$NS" status "$RELEASE" -o json | jq -r '.info.status + " (revision " + (.version|tostring) + ")"')"
echo "server: $(k -n "$NS" get deployment remedy-server -o jsonpath='{.status.readyReplicas}/{.spec.replicas} ready, image {.spec.template.spec.containers[0].image}')"
echo "runner: $(k -n "$NS" get pod remedy-runner-0 -o jsonpath='{.status.phase}, restarts {.status.containerStatuses[0].restartCount}' 2>/dev/null || echo 'no pod')"
# The hook Job deletes itself when it succeeds, so the proof of a working refresher is the token and the CronJob's clock.
if [ "$(k -n "$NS" get secret remedy-write-token -o jsonpath='{.data.token}' 2> /dev/null | wc -c)" -gt 0 ]; then
  echo "write token: present"
else
  echo "write token: missing"
fi
echo "token refresher: last scheduled success $(k -n "$NS" get cronjob remedy-token-refresh -o jsonpath='{.status.lastSuccessfulTime}' 2> /dev/null | grep . || echo never)"
echo "database volume: $(k -n "$NS" get pvc remedy-data -o jsonpath='{.status.phase}' 2>/dev/null || echo none)"
if [ -d "$CLAUDE_DIR" ]; then login_dir=present; else login_dir=missing; fi
echo "login directory: $CLAUDE_DIR ($login_dir)"
if [ -f "$DUMMY_ENV" ]; then
  load_dummy_env
  echo "url: $REMEDY_URL ($(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "$REMEDY_URL/healthz" || true) on /healthz)"
  echo "secrets: $DUMMY_ENV"
else
  echo "url: unknown (no $DUMMY_ENV)"
fi
```

Create `dev/kind/dummy-logs.sh`:

```sh
#!/bin/sh
# make dummy-logs: follows the logs of the control plane, the runner and whatever else carries the app label.
set -eu
. "$(dirname "$0")/lib.sh"
need kubectl
exec kubectl --context "$CTX" -n "$NS" logs -f --prefix --tail=50 --max-log-requests=8 \
  -l app.kubernetes.io/name=remedy --all-containers
```

- [ ] **Step 4: The Makefile targets**

In `Makefile`, replace:

```make
.PHONY: help build build-go images test vet fmt web-install web-build web-lint web-test dev-server dev-web chart-check check
```

with:

```make
.PHONY: help build build-go images test vet fmt web-install web-build web-lint web-test dev-server dev-web chart-check check \
	dummy-up dummy-down dummy-login dummy-logout dummy-redeploy dummy-reset dummy-smoke dummy-status dummy-logs
```

and append after the `check:` target:

```make

dummy-up: ## Build up the kind dummy setup: cluster, demo workloads, Argo CD, images, the chart (real agent; login: dummy-login)
	dev/kind/dummy-up.sh

dummy-down: ## Remove the dummy cluster and what was generated; the CLI login in ~/remedy-kind/claude stays
	dev/kind/down.sh

dummy-login: ## The one-time CLI login in the dummy's runner pod (interactive)
	dev/kind/dummy-login.sh

dummy-logout: ## Remove the CLI login from the host directory (asks first)
	dev/kind/dummy-logout.sh

dummy-redeploy: ## Rebuild both images, load them and upgrade the release (the fast loop)
	dev/kind/dummy-redeploy.sh

dummy-reset: ## Empty the dummy's database; keeps the cluster, the secrets and the login
	dev/kind/dummy-reset.sh

dummy-smoke: ## Run two real runs against the dummy (a cluster question and an approved restart); costs subscription quota
	dev/kind/smoke.sh

dummy-status: ## State of the dummy setup as key: value lines
	dev/kind/dummy-status.sh

dummy-logs: ## Follow the dummy's logs
	dev/kind/dummy-logs.sh
```

(The recipe lines start with a tab.)

- [ ] **Step 5: Syntax and help**

```sh
chmod +x dev/kind/dummy-*.sh
for f in dev/kind/dummy-*.sh dev/kind/lib.sh; do sh -n "$f" || echo "SYNTAX: $f"; done
make help | grep dummy
```

Expected: no `SYNTAX` line; `make help` lists the nine `dummy-*` targets with their descriptions. (The `help` pattern `^[a-zA-Z_-]+:.*## ` matches the hyphenated names.)

- [ ] **Step 6: Run `dummy-up` once, as far as it goes without a login**

```sh
make dummy-up
make dummy-status
```

Expected: `make dummy-up` ends with the `Ready.` block (the chart installs: the init container downloads the pinned CLI, the hook fills the write token); `make dummy-status` prints `release: deployed`, `server: 1/1 ready`, `runner: Running, restarts 0`, `write token: present` (the hook filled it; `token refresher: last scheduled success never` is right until the CronJob has run once, up to half an hour), and `/healthz` answers `200`. If the install stops, the first suspects are: a pod rejected by the Pod Security Standard (`kubectl -n remedy-system get events | grep -i forbidden`), the init container failing the checksum or the download (`kubectl -n remedy-system logs remedy-runner-0 -c install-cli`), or the network policy (set `networkPolicy.enabled: false` in `dummy-values.yaml` and say so in the runbook). Fix the cause in the chart or the values, not by weakening a check.

- [ ] **Step 7: Commit**

```bash
git add dev/kind Makefile
git commit -m "feat(kind): make dummy-up, -down, -login, -logout, -redeploy, -reset, -status and -logs

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 4: The smoke test

**Files:**
- Create: `dev/kind/smoke-lib.sh`, `dev/kind/smoke-lib_test.sh`, `dev/kind/smoke.sh`
- Modify: `Makefile`, `.github/workflows/ci.yml`

**Interfaces:**
- Produces (smoke-lib, pure functions over JSON files with `jq`, no network): `smoke_decisions <approvals.json> <run id> <mode>` (prints `approve|deny <call id> <tool>` per pending approval of the run; mode `restart-web` approves exactly the restart of `deployment demo/web`, mode `deny-all` approves nothing), `smoke_reads_ok <calls.json>`, `smoke_restart_done <calls.json>`, `smoke_no_denied <calls.json>`, `smoke_not_logged_in <run.json>`, `smoke_restarted_after <rfc3339 time> <epoch>`.
- Produces: `make shell-test`.

- [ ] **Step 1: Write the failing test of the decisions**

Create `dev/kind/smoke-lib_test.sh`:

```sh
#!/bin/sh
# Tests of the pure decision functions of the smoke test. Run: sh dev/kind/smoke-lib_test.sh (needs jq).
set -u
HERE=$(cd "$(dirname "$0")" && pwd)
. "$HERE/smoke-lib.sh"
command -v jq > /dev/null || { echo "jq is needed" >&2; exit 1; }

fails=0
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# eq <name> <expected> <actual>
eq() {
  if [ "$2" = "$3" ]; then echo "ok    $1"; else echo "FAIL  $1"; echo "  want: $2"; echo "  got:  $3"; fails=$((fails + 1)); fi
}
# yes <name> <command...>: the command must succeed. no <name> <command...>: it must fail.
yes() { n=$1; shift; if "$@" > /dev/null 2>&1; then echo "ok    $n"; else echo "FAIL  $n (should succeed)"; fails=$((fails + 1)); fi; }
no()  { n=$1; shift; if "$@" > /dev/null 2>&1; then echo "FAIL  $n (should fail)"; fails=$((fails + 1)); else echo "ok    $n"; fi; }

cat > "$WORK/approvals.json" <<'EOF'
[
 {"id":11,"runId":"r1","tool":"cluster_rollout_restart","arguments":{"kind":"deployment","namespace":"demo","name":"web"},"decision":"pending","waiting":true},
 {"id":12,"runId":"r1","tool":"cluster_rollout_restart","arguments":{"kind":"deployment","namespace":"other","name":"web"},"decision":"pending","waiting":true},
 {"id":13,"runId":"r1","tool":"cluster_delete_pod","arguments":{"namespace":"demo","name":"web-1"},"decision":"pending","waiting":true},
 {"id":14,"runId":"r2","tool":"cluster_rollout_restart","arguments":{"kind":"deployment","namespace":"demo","name":"web"},"decision":"pending","waiting":true},
 {"id":15,"runId":"r1","tool":"cluster_rollout_restart","arguments":{"kind":"deployment","namespace":"demo","name":"web"},"decision":"approved","waiting":false},
 {"id":16,"runId":"r1","tool":"cluster_rollout_restart","arguments":{"kind":"deployment","namespace":"demo","name":"web-extra"},"decision":"pending","waiting":true},
 {"id":17,"runId":"r1","tool":"cluster_rollout_restart","arguments":"not an object","decision":"pending","waiting":true},
 {"id":18,"runId":"r1","tool":"cluster_rollout_restart","arguments":{"kind":"statefulset","namespace":"demo","name":"web"},"decision":"pending","waiting":true},
 {"id":19,"runId":"r1","tool":"cluster_rollout_restart","arguments":{"kind":"deployment","namespace":"demo","name":"web"},"decision":"pending","waiting":false}
]
EOF

eq "restart-web approves only the restart of demo/web, and only for this run, and only a waiting one" \
"approve 11 cluster_rollout_restart
deny 12 cluster_rollout_restart
deny 13 cluster_delete_pod
deny 16 cluster_rollout_restart
deny 17 cluster_rollout_restart
deny 18 cluster_rollout_restart" "$(smoke_decisions "$WORK/approvals.json" r1 restart-web)"

eq "deny-all approves nothing" \
"deny 11 cluster_rollout_restart
deny 12 cluster_rollout_restart
deny 13 cluster_delete_pod
deny 16 cluster_rollout_restart
deny 17 cluster_rollout_restart
deny 18 cluster_rollout_restart" "$(smoke_decisions "$WORK/approvals.json" r1 deny-all)"

eq "another run's approvals are not decided" "approve 14 cluster_rollout_restart" "$(smoke_decisions "$WORK/approvals.json" r2 restart-web)"
eq "an unknown mode approves nothing" "deny 14 cluster_rollout_restart" "$(smoke_decisions "$WORK/approvals.json" r2 something)"
eq "an empty list gives nothing" "" "$(echo '[]' > "$WORK/none.json"; smoke_decisions "$WORK/none.json" r1 restart-web)"

cat > "$WORK/calls.json" <<'EOF'
[
 {"id":1,"tool":"cluster_pods","kind":"read","status":"succeeded","decision":"approved"},
 {"id":2,"tool":"cluster_pod_logs","kind":"read","status":"failed","decision":"approved"},
 {"id":3,"tool":"cluster_rollout_restart","kind":"mutating","status":"succeeded","decision":"approved"}
]
EOF
yes "a succeeded cluster read call counts" smoke_reads_ok "$WORK/calls.json"
echo '[{"id":1,"tool":"cluster_pods","kind":"read","status":"failed"},{"id":2,"tool":"incident_get","kind":"read","status":"succeeded"}]' > "$WORK/noreads.json"
no "a failed read and a non-cluster read do not count" smoke_reads_ok "$WORK/noreads.json"
yes "one approved and succeeded restart counts" smoke_restart_done "$WORK/calls.json"
echo '[{"tool":"cluster_rollout_restart","kind":"mutating","status":"denied","decision":"denied"}]' > "$WORK/denied.json"
no "a denied restart is not a restart" smoke_restart_done "$WORK/denied.json"
echo '[{"tool":"cluster_rollout_restart","status":"succeeded","decision":"approved"},{"tool":"cluster_rollout_restart","status":"succeeded","decision":"approved"}]' > "$WORK/twice.json"
no "two restarts are not one" smoke_restart_done "$WORK/twice.json"
yes "no denied call" smoke_no_denied "$WORK/calls.json"
no "a denied call is found" smoke_no_denied "$WORK/denied.json"

echo '{"status":"failed","result":"","failureReason":"Not logged in · Please run /login"}' > "$WORK/run-login.json"
yes "not logged in is recognised in the failure reason" smoke_not_logged_in "$WORK/run-login.json"
echo '{"status":"failed","result":"Invalid API key · Please run /login","failureReason":""}' > "$WORK/run-login2.json"
yes "and in the result" smoke_not_logged_in "$WORK/run-login2.json"
echo '{"status":"succeeded","result":"all good","failureReason":""}' > "$WORK/run-ok.json"
no "a good run is not a login problem" smoke_not_logged_in "$WORK/run-ok.json"
echo '{"status":"failed"}' > "$WORK/run-bare.json"
no "a run without result and reason is not a login problem" smoke_not_logged_in "$WORK/run-bare.json"

yes "a restart at the approval's second counts" smoke_restarted_after 2026-10-07T12:00:05Z 1791374405
yes "a restart a second before counts (the resolution is one second)" smoke_restarted_after 2026-10-07T12:00:04Z 1791374405
no  "a restart long before the approval does not" smoke_restarted_after 2026-10-07T11:00:00Z 1791374405
no  "a time that is not a time does not" smoke_restarted_after nonsense 1791374405

[ "$fails" -eq 0 ] && echo "all passed" || { echo "$fails failed" >&2; exit 1; }
```

The epoch `1791374405` is `2026-10-07T12:00:05Z`; check it before relying on it: `date -u -r 1791374405 +%FT%TZ` (macOS) or `date -u -d @1791374405 +%FT%TZ` (Linux). If it prints another time, replace the number in the four lines by the epoch of `2026-10-07T12:00:05Z`.

- [ ] **Step 2: Run it to see it fail**

Run: `sh dev/kind/smoke-lib_test.sh`
Expected: it stops at the first call with `.: ... smoke-lib.sh: cannot open` (the library does not exist yet).

- [ ] **Step 3: The library**

Create `dev/kind/smoke-lib.sh`:

```sh
# The decisions of the smoke test, as pure functions over JSON files. They read files with jq and touch no network, so
# smoke-lib_test.sh can test them. Source this file; it runs nothing.

# smoke_decisions <approvals.json> <run id> <mode>
# Prints "approve|deny <call id> <tool>" for every approval of the run that is pending and still waited for. Mode
# "restart-web" approves exactly one thing: the restart of the deployment demo/web. Every other mode, and every other
# request (another tool, another namespace, another name, another kind, arguments that are not an object), is denied.
smoke_decisions() {
  jq -r --arg run "$2" --arg mode "$3" '
    .[]
    | select(.runId == $run and .decision == "pending" and .waiting == true)
    | (if $mode == "restart-web"
          and .tool == "cluster_rollout_restart"
          and (.arguments | type == "object")
          and .arguments.namespace == "demo"
          and .arguments.name == "web"
          and ((.arguments.kind // "deployment") == "deployment")
       then "approve" else "deny" end) + " \(.id) \(.tool)"' "$1"
}

# smoke_reads_ok <calls.json>: at least one cluster read call succeeded.
smoke_reads_ok() {
  jq -e '[.[] | select((.tool | startswith("cluster_")) and .kind == "read" and .status == "succeeded")] | length > 0' "$1" > /dev/null
}

# smoke_restart_done <calls.json>: exactly one restart was approved and succeeded.
smoke_restart_done() {
  jq -e '[.[] | select(.tool == "cluster_rollout_restart" and .decision == "approved" and .status == "succeeded")] | length == 1' "$1" > /dev/null
}

# smoke_no_denied <calls.json>: no call was denied.
smoke_no_denied() {
  jq -e '[.[] | select(.status == "denied" or .decision == "denied")] | length == 0' "$1" > /dev/null
}

# smoke_not_logged_in <run.json>: the run says the CLI is not logged in.
smoke_not_logged_in() {
  jq -e '((.result // "") + " " + (.failureReason // "")) | test("not logged in|/login"; "i")' "$1" > /dev/null
}

# smoke_restarted_after <rfc3339 time> <epoch seconds>: the time is not before the epoch by more than the one-second
# resolution of a restart's annotation.
smoke_restarted_after() {
  t=$(jq -rn --arg t "$1" '$t | fromdateiso8601' 2> /dev/null) || return 1
  [ "$t" -ge $(($2 - 2)) ]
}
```

- [ ] **Step 4: Run the tests**

Run: `sh dev/kind/smoke-lib_test.sh`
Expected: every line `ok`, then `all passed`. A `FAIL` on the epoch lines means the number is not the epoch of the time named in the test (step 1's note).

- [ ] **Step 5: The smoke test itself**

Create `dev/kind/smoke.sh`:

```sh
#!/bin/sh
# make dummy-smoke: two real runs against the dummy setup, with the real agent on your subscription.
#   1. an ad-hoc run with cluster tools asks why a deployment in demo crashes: the run succeeds and read tools were used;
#   2. an ad-hoc run asks to restart demo/web: the approval is decided here, and ONLY the restart of demo/web is approved;
#      the restart really happened.
# It asserts states, never text: the agent's words differ every time. It sends the admin password only to the dummy's URL.
# A run that ends "Not logged in" prints what to do and exits non-zero.
set -eu
HERE=$(cd "$(dirname "$0")" && pwd)
. "$HERE/lib.sh"
. "$HERE/smoke-lib.sh"
need curl jq kubectl
load_dummy_env

JAR=$(umask 077; mktemp)
WORK=$(mktemp -d)
trap 'rm -rf "$JAR" "$WORK"' EXIT
TIMEOUT=${SMOKE_TIMEOUT:-900}
APPROVED_AT=0
DENIED=0

fail() { echo "smoke: FAILED: $*" >&2; exit 1; }
say() { echo "smoke: $*"; }

# api <method> <path> [json body]: the body on stdout; fails on anything but 2xx.
api() {
  method=$1 path=$2 body=${3:-}
  if [ -n "$body" ]; then
    code=$(curl -sS -o "$WORK/body" -w '%{http_code}' -b "$JAR" -c "$JAR" -X "$method" -H 'X-Remedy-CSRF: 1' \
      -H 'Content-Type: application/json' --data-binary "$body" "$REMEDY_URL$path")
  else
    code=$(curl -sS -o "$WORK/body" -w '%{http_code}' -b "$JAR" -c "$JAR" -X "$method" -H 'X-Remedy-CSRF: 1' "$REMEDY_URL$path")
  fi
  case $code in
    2??) cat "$WORK/body" ;;
    *) echo "smoke: $method $path answered $code: $(cat "$WORK/body")" >&2; return 1 ;;
  esac
}

# start_run <prompt>: prints the id of a new ad-hoc run with the gatekeeper tools and the cluster tools.
start_run() {
  api POST /api/runs "$(jq -cn --arg p "$1" '{prompt:$p,tools:true,cluster:true}')" | jq -r .id
}

# wait_run <run id> <mode>: waits until the run ends, deciding its approvals on the way (see smoke_decisions).
wait_run() {
  id=$1 mode=$2 started=$(date +%s)
  while :; do
    api GET "/api/runs/$id" > "$WORK/run.json" || return 1
    case $(jq -r .status "$WORK/run.json") in succeeded | failed) return 0 ;; esac
    api GET /api/approvals > "$WORK/approvals.json" || return 1
    smoke_decisions "$WORK/approvals.json" "$id" "$mode" > "$WORK/decisions"
    while read -r verdict call tool; do
      [ -n "$verdict" ] || continue
      [ "$verdict" = approve ] && APPROVED_AT=$(date +%s)
      [ "$verdict" = deny ] && DENIED=$((DENIED + 1))
      api POST "/api/approvals/$call/$verdict" '{"reason":"dummy-smoke"}' > /dev/null || true
      say "$verdict $tool (call $call)"
    done < "$WORK/decisions"
    [ $(($(date +%s) - started)) -lt "$TIMEOUT" ] || fail "the run $id did not end within ${TIMEOUT}s"
    sleep 3
  done
}

# finished <run id>: saves the run and its tool calls, and stops with a clear message when the CLI is not logged in.
finished() {
  api GET "/api/runs/$1" > "$WORK/run.json"
  api GET "/api/runs/$1/tool-calls" > "$WORK/calls.json"
  if [ "$(jq -r .status "$WORK/run.json")" = failed ] && smoke_not_logged_in "$WORK/run.json"; then
    fail "the runner is not logged in. Run: make dummy-login"
  fi
}

say "waiting for the control plane"
n=0
until curl -sf -o /dev/null "$REMEDY_URL/healthz"; do
  n=$((n + 1)); [ "$n" -lt 60 ] || fail "$REMEDY_URL/healthz does not answer. Is the dummy up? make dummy-status"
  sleep 2
done

printf '{"password":"%s"}' "$REMEDY_ADMIN_PASSWORD" |
  curl -sf -o /dev/null -c "$JAR" -H 'X-Remedy-CSRF: 1' --data-binary @- "$REMEDY_URL/api/login" || fail "cannot sign in at $REMEDY_URL"
api GET /api/capabilities | jq -e '.cluster.read and .cluster.write and (.cluster.namespaces | index("demo"))' > /dev/null ||
  fail "the cluster tools are not on for the namespace demo (GET /api/capabilities)"

say "run 1: why does something in demo crash?"
RUN1=$(start_run "In the namespace demo of the Kubernetes cluster one deployment keeps crashing. Use your tools to find out which one and why, and tell me the cause in two sentences. Do not change anything.")
wait_run "$RUN1" deny-all
finished "$RUN1"
[ "$(jq -r .status "$WORK/run.json")" = succeeded ] || fail "run 1 ended $(jq -r .status "$WORK/run.json"): $(jq -r '.failureReason // ""' "$WORK/run.json")"
smoke_reads_ok "$WORK/calls.json" || fail "run 1 made no successful cluster read call"
smoke_no_denied "$WORK/calls.json" || fail "run 1 asked for something that was denied: a question must not change anything"
say "run 1 ok: $(jq 'length' "$WORK/calls.json") tool calls"

say "run 2: restart demo/web"
RUN2=$(start_run "Restart the deployment web in the namespace demo of the Kubernetes cluster, then check with your tools that its pods are back up and tell me the result in one sentence.")
wait_run "$RUN2" restart-web
finished "$RUN2"
[ "$(jq -r .status "$WORK/run.json")" = succeeded ] || fail "run 2 ended $(jq -r .status "$WORK/run.json"): $(jq -r '.failureReason // ""' "$WORK/run.json")"
[ "$DENIED" -eq 0 ] || fail "run 2 asked for $DENIED thing(s) other than the restart of demo/web; they were denied"
smoke_restart_done "$WORK/calls.json" || fail "run 2 has no single approved and succeeded restart"
[ "$APPROVED_AT" -gt 0 ] || fail "no approval was decided in run 2"
restarted=$(k -n demo get deployment web -o jsonpath='{.spec.template.metadata.annotations.kubectl\.kubernetes\.io/restartedAt}')
[ -n "$restarted" ] || fail "demo/web has no restartedAt annotation: the restart did not happen"
smoke_restarted_after "$restarted" "$APPROVED_AT" || fail "demo/web was last restarted at $restarted, before the approval"
k -n demo rollout status deployment/web --timeout=120s > /dev/null || fail "the rollout of demo/web did not complete"
say "run 2 ok: demo/web restarted at $restarted"

say "all ok (runs $RUN1 and $RUN2)"
```

- [ ] **Step 6: `make shell-test` and the CI job**

In `Makefile`, replace `dev-web chart-check check \` (the end of the first `.PHONY` line) with `dev-web chart-check shell-test check \`, and replace the `check:` line with:

```make
shell-test: ## Run the shell tests: the dummy's smoke decisions (needs jq)
	@command -v jq > /dev/null || { echo "jq is needed" >&2; exit 1; }
	sh dev/kind/smoke-lib_test.sh

check: fmt vet test chart-check shell-test web-lint web-test web-build ## Everything CI checks
```

In `.github/workflows/ci.yml`, append at the end of the `jobs:` map:

```yaml

  shell:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - run: make shell-test
```

(`jq` is preinstalled on the hosted runner.)

- [ ] **Step 7: Check the script's syntax and run everything**

```sh
chmod +x dev/kind/smoke.sh
sh -n dev/kind/smoke.sh && sh -n dev/kind/smoke-lib.sh && echo syntax ok
make check
```

Expected: `syntax ok`, and `make check` passes including `shell-test`. (`smoke.sh` itself runs in task 6: it needs the login.)

- [ ] **Step 8: Commit**

```bash
git add dev/kind Makefile .github/workflows/ci.yml
git commit -m "feat(kind): make dummy-smoke, a real cluster question and an approved restart, with tested decisions

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 5: The rule "no script reads the login" as a check

**Files:**
- Create: `scripts/check-login-dir-untouched.sh`, `scripts/check-login-dir-untouched_test.sh`
- Modify: `Makefile`

**Interfaces:**
- Produces: `scripts/check-login-dir-untouched.sh [file...]` (default: `dev/kind/*.sh`) exits 1 and names the line for any use of `$CLAUDE_DIR` or the literal path of the login directory other than the allowed ones, and for any command that reads, copies, archives or removes `"$OUT"` itself (it holds the login) unless it spares `claude`. `make shell-test` runs it and its test.

- [ ] **Step 1: Write the failing test**

Create `scripts/check-login-dir-untouched_test.sh`:

```sh
#!/bin/sh
# Tests of check-login-dir-untouched.sh. Run: sh scripts/check-login-dir-untouched_test.sh
set -u
HERE=$(cd "$(dirname "$0")" && pwd)
CHECK=$HERE/check-login-dir-untouched.sh
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
fails=0

# ok <name> <file content>: the check must pass.  bad <name> <file content>: it must fail.
ok()  { printf '%s\n' "$2" > "$WORK/f.sh"; if sh "$CHECK" "$WORK/f.sh" > /dev/null 2>&1; then echo "ok    $1"; else echo "FAIL  $1 (should pass)"; fails=$((fails + 1)); fi; }
bad() { printf '%s\n' "$2" > "$WORK/f.sh"; if sh "$CHECK" "$WORK/f.sh" > /dev/null 2>&1; then echo "FAIL  $1 (should fail)"; fails=$((fails + 1)); else echo "ok    $1"; fi; }

ok  "an assignment"                 'CLAUDE_DIR=$OUT/claude'
ok  "the mode assignment"           'CLAUDE_DIR_MODE=700'
ok  "creating and chmod-ing"        'mkdir -p "$CLAUDE_DIR"; chmod "$CLAUDE_DIR_MODE" "$CLAUDE_DIR"'
ok  "substituting the path"         'sed "s|__CLAUDE_DIR__|$CLAUDE_DIR|" kind.yaml > out.yaml'
ok  "deleting the content"          'find "$CLAUDE_DIR" -mindepth 1 -delete'
ok  "testing for the directory"     '[ -d "$CLAUDE_DIR" ] && echo present'
ok  "a message"                     'echo "the login in $CLAUDE_DIR stays"'
ok  "a message after a test"        '[ -d "$CLAUDE_DIR" ] || { echo "no login directory at $CLAUDE_DIR"; exit 0; }'
ok  "a prompt"                      'printf "Remove the CLI login in %s? [y/N] " "$CLAUDE_DIR"'
ok  "a state in a variable"         'if [ -d "$CLAUDE_DIR" ]; then login_dir=present; else login_dir=missing; fi'
ok  "a comment"                     '# the files in $CLAUDE_DIR are never read'
ok  "removing the rest of OUT"      'find "$OUT" -mindepth 1 -maxdepth 1 ! -name claude -exec rm -rf {} +'
ok  "a file in OUT"                 'rm -f "$OUT/kind-rendered.yaml"'
ok  "nothing about the login"       'echo hello'

bad "cat of a file in it"           'cat "$CLAUDE_DIR/.credentials.json"'
bad "grep in it"                    'grep -r token "$CLAUDE_DIR"'
bad "a hash of it"                  'shasum -a 256 "$CLAUDE_DIR"/*'
bad "copying it"                    'cp -r "$CLAUDE_DIR" /tmp/x'
bad "a command after an allowed one" 'mkdir -p "$CLAUDE_DIR"; cat "$CLAUDE_DIR/x"'
bad "the literal path"              'cat ~/remedy-kind/claude/x'
bad "a command inside a message"    'echo "$(cat "$CLAUDE_DIR/x")"'
bad "a command after a message"     'echo "x" && cat "$CLAUDE_DIR/x"'
bad "backticks inside a message"    'echo "`cat $CLAUDE_DIR/x`"'
bad "removing all of OUT"           'rm -rf "$OUT"'
bad "archiving OUT"                 'tar cf backup.tar "$OUT"'
bad "copying OUT"                   'cp -r "$OUT" /tmp/out'
bad "listing OUT's content"         'cat "$OUT"/*'

[ "$fails" -eq 0 ] && echo "all passed" || { echo "$fails failed" >&2; exit 1; }
```

- [ ] **Step 2: Run it to see it fail**

Run: `sh scripts/check-login-dir-untouched_test.sh 2>&1 | tail -5`
Expected: FAIL lines (`sh: ... No such file`: the check does not exist, so every `ok` case fails and every `bad` case passes by accident; at least the `ok` cases are reported `FAIL`).

- [ ] **Step 3: The check**

Create `scripts/check-login-dir-untouched.sh`:

```sh
#!/bin/sh
# Checks that no script opens a file in the CLI's login directory. Remedy never reads, copies, logs or stores the agent
# CLIs' credentials; the dummy setup keeps the login in $CLAUDE_DIR (~/remedy-kind/claude) and its scripts may only
# create the directory, set its mode, put its path into the kind configuration, test that it exists, mention it in a
# message, and delete its content. Any other line that names it, and any line that reads, copies, archives or removes
# "$OUT" (the directory that holds it) without sparing it, fails.
# Usage: scripts/check-login-dir-untouched.sh [file...]    (default: dev/kind/*.sh)
set -eu
if [ $# -eq 0 ]; then
  set -- dev/kind/*.sh
fi

awk '
  {
    line = $0
    if (line ~ /^[[:space:]]*#/) next

    # Take out what is allowed. What is left must not name the login directory. A message may mention a variable, but never
    # run a command ($( and backticks are not allowed inside the quotes of an allowed echo or printf).
    rest = line
    gsub(/^[[:space:]]*CLAUDE_DIR(_MODE)?=[^ ;]*/, "", rest)
    gsub(/mkdir -p "\$CLAUDE_DIR"/, "", rest)
    gsub(/chmod "\$CLAUDE_DIR_MODE" "\$CLAUDE_DIR"/, "", rest)
    gsub(/sed "s[|]__CLAUDE_DIR__[|]\$CLAUDE_DIR[|]"/, "", rest)
    gsub(/find "\$CLAUDE_DIR" -mindepth 1 -delete/, "", rest)
    gsub(/\[ -d "\$CLAUDE_DIR" \]/, "", rest)
    gsub(/echo "([^"$`]|\$[A-Za-z_][A-Za-z_0-9]*)*"/, "", rest)
    gsub(/printf "([^"$`]|\$[A-Za-z_][A-Za-z_0-9]*)*"( "\$[A-Za-z_][A-Za-z_0-9]*")*/, "", rest)
    if (rest ~ /CLAUDE_DIR|remedy-kind\/claude/) {
      printf "%s:%d: names the login directory in a way that could read it: %s\n", FILENAME, FNR, line
      bad = 1
    }

    # "$OUT" holds the login. A command that works on all of it must spare it.
    if (line ~ /(rm|cp|mv|tar|zip|rsync|cat|grep|shasum|sha256sum|md5|base64|xxd|strings|find)[^|;&]*"\$OUT"/ && line !~ /! -name claude/) {
      printf "%s:%d: works on all of $OUT, which holds the login, without sparing it: %s\n", FILENAME, FNR, line
      bad = 1
    }
  }
  END { exit bad }
' "$@" && echo "no script reads the login directory"
```

- [ ] **Step 4: Run the test and the check on the real scripts**

Run: `sh scripts/check-login-dir-untouched_test.sh && sh scripts/check-login-dir-untouched.sh`
Expected: every test line `ok`, `all passed`, then `no script reads the login directory`. If the real run flags a line of `lib.sh` or one of the `dummy-*.sh`, it is a real finding: either the script does more with the directory than the rule allows (change the script), or the rule is too narrow for a harmless line (add that exact form to the `gsub` list **and** a case to the test, never loosen a pattern).

- [ ] **Step 5: Put it into `make shell-test`**

In `Makefile`, replace:

```make
	sh dev/kind/smoke-lib_test.sh
```

with:

```make
	sh dev/kind/smoke-lib_test.sh
	sh scripts/check-login-dir-untouched_test.sh
	sh scripts/check-login-dir-untouched.sh
```

and change the target's description to `Run the shell tests: the dummy's smoke decisions and the rule that no script reads the CLI login (needs jq)`.

- [ ] **Step 6: Commit**

```bash
chmod +x scripts/check-login-dir-untouched.sh
git add scripts Makefile
git commit -m "test: a check that no script opens a file in the CLI's login directory

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 6: The first real run, the runbook and the state of the project

**Files:**
- Create: `docs/runbook/dummy-setup.md`, `docs/research/k8s-dummy-real-run.md`
- Modify: `dev/kind/README.md`, `CLAUDE.md`, `README.md`

This task needs the maintainer's login (step 3). An agent stops there, asks the maintainer to run `make dummy-login`, and continues when they say it is done. The run spends subscription quota (two short runs).

- [ ] **Step 1: A cold start**

```sh
make dummy-down
make dummy-up
make dummy-status
```

Expected: the cluster is deleted and made again; `make dummy-up` ends with the `Ready.` block; `dummy-status` shows a deployed release, `server: 1/1 ready`, `runner: Running, restarts 0`, `write token: present`, and `/healthz` `200`. Note the time `make dummy-up` took, and the size of `kubectl -n remedy-system get pods` output. This is the cold start; the login directory must still hold what it held (it is empty before the first login).

- [ ] **Step 2: Without a login, the smoke test says so**

Run: `make dummy-smoke; echo "exit=$?"`
Expected: `smoke: FAILED: the runner is not logged in. Run: make dummy-login` and `exit=1` (the first run fails with "Not logged in" within seconds; it costs nothing).

- [ ] **Step 3 (Maintainer): log in once**

Ask the maintainer to run, in their terminal (the `!` prefix runs it in the session):

```sh
make dummy-login
```

Type `/login`, open the URL, paste the code, `/exit`. The agent does not see the code and does not look at the login directory.

- [ ] **Step 4: The smoke test with a login**

Run: `make dummy-smoke; echo "exit=$?"`
Expected: `smoke: run 1 ok: N tool calls`, an `approve cluster_rollout_restart (call N)` line, `smoke: run 2 ok: demo/web restarted at <time>`, `smoke: all ok`, and `exit=0`. Time it. If a run fails, read `make dummy-logs` and the run in the UI (`http://127.0.0.1:18080`, password in `~/remedy-kind/dummy.env`), and decide whether the cause is the setup (fix it) or the agent's behaviour (run again once; two failures in a row are a finding for the record).

- [ ] **Step 4a: The CLI did not write into the state volume's `.local`**

Spike S4 could not tell whether the CLI, started as `/opt/claude/claude` (not installed by its own `install`), writes an update under `$HOME/.local/share/claude`; with `HOME=/state` that is on the writable state volume. The runner image has no shell and `kubectl exec` cannot run `ls` in it, so look at the host side of the volume, by name only (never its contents, which hold the login):

```sh
command ls -d ~/remedy-kind/claude/.local/share/claude
```

Expected: `No such file or directory` (the directory is absent after the login of step 3 and the two runs of step 4). Put the answer in the record of step 8. If the directory exists, that is a finding, not a failure of this task: the options are `DISABLE_UPDATES=1` in the runner pod's environment together with an entry in `provider.FilterEnv`'s allowlist (a spec change: the spec says nothing is added to it), or mounting nothing writable under `$HOME/.local`. Record it, stop, and ask the maintainer.

- [ ] **Step 5: The login survives, and the other targets work**

```sh
make dummy-redeploy
make dummy-reset
make dummy-down
make dummy-up
make dummy-smoke; echo "exit=$?"
```

Expected: `dummy-redeploy` ends `redeployed with the tag dev-…`; `dummy-reset` ends `the database is empty again`; after `dummy-down` and `dummy-up` the smoke test passes **without** another `make dummy-login`: that is the proof of D2. If it asks to log in again, the host directory did not survive: that contradicts the S1 record, so stop and report it.

- [ ] **Step 6: Verify the secrets are nowhere they should not be**

```sh
git status --short
grep -rn "$(grep REMEDY_ADMIN_PASSWORD ~/remedy-kind/dummy.env | cut -d= -f2)" . --include='*' -l 2>/dev/null | grep -v '^./.git/' || echo "the password is in no file of the repository"
kubectl --context kind-remedy-dev -n remedy-system logs deploy/remedy-server | grep -c "$(grep REMEDY_MASTER_KEY ~/remedy-kind/dummy.env | cut -d= -f2)" || true
ls -la ~/remedy-kind
```

Expected: `git status` shows only files you meant to add; the password is in no file of the repository; the server log has `0` lines with the master key; `~/remedy-kind` is `drwx------` and `dummy.env` is `-rw-------`.

- [ ] **Step 7: The runbook**

Create `docs/runbook/dummy-setup.md` with these sections, each short and in this order: **What it is** (a throwaway kind cluster that runs Remedy from the Helm chart with the real agent; what is in it: the demo workloads, Argo CD, the chart; what is not: GitHub, a fake agent); **You need** (`docker`, `kind`, `kubectl`, `helm`, `jq`, `curl`, `openssl`, `make`, and a logged-in-able Claude subscription); **The commands** (the table of the nine targets from the spec, with what each prints or changes); **The first time** (`make dummy-up`, `make dummy-login`, `make dummy-smoke`, the URL `http://127.0.0.1:18080`, where the password is); **Where things are** (`~/remedy-kind/dummy.env`, `~/remedy-kind/claude`, why they are outside the repository, which survive `dummy-down`; that the login directory is `0700` and that this was measured on Docker Desktop for Mac only: on a Linux Docker host the directory would need a `chown` to 65532 or a world-writable mode, which no script does); **For an agent** (every target is non-interactive except `dummy-login` and `dummy-logout`; `dummy-status` output format; `dummy.env` has URL and password; the smoke test's exit codes: 0 all ok, 1 a failed assertion or a missing login; the quota each run spends: two short runs); **Trying it by hand** (sign in, Ask Remedy with "Read the cluster", the prompts of `docs/runbook/cluster-real-run.md`); **Troubleshooting** (one line each, from what happened in steps 1 to 5 of this task and from the failures named in task 3 step 6: the install stuck, a pod rejected by Pod Security, the CLI download failing, the policy blocking something, a cluster from `up.sh`, a cluster without the mount).

- [ ] **Step 8: The record**

Create `docs/research/k8s-dummy-real-run.md` with: the date, the versions (Docker Desktop, kind, kubectl, helm, the CLI version of `deploy/cli-pin.yaml`), the steps above with the commands and the output that matters (the `dummy-status` lines, the smoke test's lines, the exit codes, the durations of the cold start and of the smoke test, the quota cost the CLI printed if any, and whether `~/remedy-kind/claude/.local/share/claude` exists, step 4a), no secret and no login content, and a "Result" paragraph that says which of the spec's success criteria 1, 2 and 3 this proves and what it does not.

- [ ] **Step 9: Update the documents**

In `dev/kind/README.md`, add a section at the end:

```markdown
## The dummy setup: the chart in this cluster, with the real agent

`make dummy-up` builds this cluster up with Remedy itself installed from the Helm chart (`deploy/chart`), and `make dummy-down`
removes it; the CLI login in `~/remedy-kind/claude` survives. See [`docs/runbook/dummy-setup.md`](../../docs/runbook/dummy-setup.md).
It and `up.sh` do not share a running cluster: the account names collide, and `dummy-up.sh` says so. `down.sh` is `make dummy-down`:
it removes the cluster and what the scripts generated, never `claude/`.
```

In `CLAUDE.md`, in the Kubernetes bullet of "Current state", append: ` The dummy setup (`make dummy-up`, `-down`, `-login`, `-logout`, `-redeploy`, `-reset`, `-smoke`, `-status`, `-logs`; `dev/kind/lib.sh`, `docs/runbook/dummy-setup.md`) runs the chart in kind with the real agent; the login is in `~/remedy-kind/claude`, which `dev/kind/down.sh` never removes and no script ever opens (`scripts/check-login-dir-untouched.sh`). `make shell-test` runs the shell tests.` In the Commands block add:

```markdown
make dummy-up                                    # kind + demo workloads + Argo CD + both images + the Helm chart, real agent; then make dummy-login once
make dummy-smoke                                 # two real runs against it (a cluster question, an approved restart of demo/web); costs subscription quota
make dummy-down                                  # removes the cluster and what was generated; the CLI login stays (make dummy-logout removes it)
make shell-test                                  # the shell tests: the smoke decisions, and the rule that no script reads the CLI login
```

In `README.md`, at the end of the line that mentions `dev/kind/` ("... to try it on."), append ` For the whole chain in a cluster, with Remedy installed by its Helm chart, see [`docs/runbook/dummy-setup.md`](docs/runbook/dummy-setup.md) (`make dummy-up`).`

- [ ] **Step 10: Final check and commit**

Run: `make check`
Expected: PASS.

```bash
git add docs dev/kind/README.md CLAUDE.md README.md
git commit -m "docs: the dummy setup's runbook and the record of its first real run

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```
