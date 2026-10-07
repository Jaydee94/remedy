# K-0: Four Spikes for the Kubernetes Deployment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Four questions that the Kubernetes deployment depends on are answered by measurement, each in a record under `docs/research/`, and the spec and the plans K-1, K-2, K-4 and K-6 are corrected where the answer differs from what they assume.

**Architecture:** No product code. Each spike is a short script or a list of commands run against a throwaway container, a throwaway kind cluster or a pod, and one markdown record with the command, the output and the decision. Throwaway files live in `scripts/spike/k8s/` and are labelled as such.

**Tech Stack:** Docker Desktop, kind v0.33, kubectl, curl, the `claude` CLI. No new dependencies.

**Spec:** [`docs/specs/2026-10-06-kubernetes-deployment-design.md`](../specs/2026-10-06-kubernetes-deployment-design.md), section 9. The earlier spikes are the model: [`docs/research/spike-claude-billing.md`](../research/spike-claude-billing.md).

**Scope note:** A spike's output is an answer, not code that stays. The scripts here are committed so the answer can be reproduced; nothing in `internal/` or `cmd/` changes. S2 needs the maintainer's interactive login and S3 is meant to be run once more against the homelab by the maintainer: those two steps are marked **Maintainer**.

## Decisions made while planning

| Topic | Spec said | This plan |
|---|---|---|
| Order | S1 to S4 | S4, then S1 and S3 (independent), then S2: S2 needs the install route S4 finds, and S4 blocks K-1, the first plan. |
| Where S3 runs | kind and k3s | The script takes a kubectl context. The agent runs it against kind; the maintainer runs it against the homelab (it creates and removes one namespace and nothing else). |
| What S1 may touch | the host directory | `~/remedy-kind-spike/` only, never `~/remedy-kind/`: the real login directory must not be touched by a spike. |
| Result files | `docs/research/<name>.md` | `k8s-s4-cli-install.md`, `k8s-s1-hostpath-login.md`, `k8s-s3-networkpolicy.md`, `k8s-s2-login-check.md`. |

## Global Constraints

- Everything committed is English: docs, scripts, comments, commit messages.
- A spike never reads, copies, logs or stores the CLI's credentials. It starts the unmodified binary and looks at its exit code and its output. The login directory is never opened by a script; the record says "the login exists" only because the CLI says so.
- No credential, token or password appears in a record. A record that shows CLI output is read once for that before commit.
- A spike changes no existing cluster except the namespace and objects it names; S1 and S3 run on a kind cluster of their own (`remedy-spike`), which they delete at the end.
- Shell in scripts runs on macOS and on Linux: no BSD-only flags (`sed -i ''`, `stat -f`, `date -j`).
- Every commit message ends with the trailer `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`.

## Review Focus

- A spike that cannot fail: every script step prints what it saw and the record states the decision in a sentence that a failing result would have changed.
- An arm64 answer given for amd64: S4 checks both architectures' checksums and artifact names, even if only one runs here.
- A kind result generalised to k3s: S3's record keeps the two apart and says which was measured.
- Quota burnt by a spike: S2 limits itself to the commands listed and records the cost of each.
- A leftover cluster or directory: each spike ends with a cleanup step and a check that it worked.

## File Structure

| Path | Responsibility |
|---|---|
| `scripts/spike/k8s/s1-kind.yaml`, `s1-storage.yaml`, `s1-pod.yaml`, `s1.sh` | S1: a kind cluster with a host mount, a hostPath PV and a non-root pod that writes into it |
| `scripts/spike/k8s/s3-netpol.sh` | S3: NetworkPolicy behaviour against any kubectl context |
| `scripts/spike/k8s/s2-pod.yaml` | S2: a throwaway pod to run the login check in |
| `docs/research/k8s-s4-cli-install.md`, `k8s-s1-hostpath-login.md`, `k8s-s3-networkpolicy.md`, `k8s-s2-login-check.md` | The four records |

---

### Task 1: S4, how the CLI gets into the runner pod

**Files:**
- Create: `docs/research/k8s-s4-cli-install.md`

**Interfaces:**
- Produces (for K-1 and K-2): the `urlTemplate` with `{version}` and `{platform}`, the vendor's platform names for `amd64` and `arm64`, whether the artifact is a raw binary (`archive: none`) or a `tar.gz` (and its `member`), where the SHA-256 of each artifact comes from, the base image the CLI runs on, and whether a read-only install directory works.

- [ ] **Step 1: Read the official sources and write down the install route**

Read, with `WebFetch` or in a browser, `https://code.claude.com/docs/en/setup` and `https://code.claude.com/docs/en/legal-and-compliance`. Start the record with a table of these facts, each with its source URL: the officially supported install routes for Linux; for the native route the download host, the URL pattern with a version in it, the artifact names for Linux `x64` and `arm64`, and where a checksum is published (a manifest next to the artifact is the usual form); whether a version can be pinned; the wording about redistributing the binary (quote one sentence). If a fact is not on the page, write "not found" and do not guess.

- [ ] **Step 2: Download both architectures and check the checksums**

```sh
mkdir -p ~/remedy-spike/s4 && cd ~/remedy-spike/s4
# Fill VERSION and the URLs from step 1. Download for both platforms, whatever this machine is.
VERSION='<the pinned version from step 1>'
for p in '<amd64 platform name>' '<arm64 platform name>'; do
  curl -fsSL -o "claude-$p" "<url template with $VERSION and $p>"
  shasum -a 256 "claude-$p"
done
```

Expected: both files download. Compare each `shasum` with the published checksum from step 1. Write both checksums and "match" or "differ" into the record. If the vendor publishes no checksum, write that, and write the SHA-256 you computed with the sentence "pinned by us, not published by the vendor": K-2 then pins it in `values.yaml` itself.

- [ ] **Step 3: Run it on the candidate base images**

```sh
cd ~/remedy-spike/s4
chmod +x claude-*
HOSTARCH=$(uname -m); [ "$HOSTARCH" = arm64 ] && P='<arm64 platform name>' || P='<amd64 platform name>'
mkdir -p ro && cp "claude-$P" ro/claude
for img in gcr.io/distroless/base-debian12:nonroot gcr.io/distroless/static-debian12:nonroot debian:bookworm-slim; do
  echo "== $img"
  docker run --rm --entrypoint /opt/claude/claude -v "$PWD/ro:/opt/claude:ro" "$img" --version 2>&1 | head -5
done
```

Expected: at least `debian:bookworm-slim` prints a version. Note for each image whether it works. If a distroless image fails with a missing library, write the library's name (`docker run --rm --entrypoint /usr/bin/ldd` needs a shell image, so use `debian:bookworm-slim` with `ldd /opt/claude/claude`). The base of `Dockerfile.runner` is the smallest image that works; if only `debian:bookworm-slim` does, that is the decision, and the record lists what the CLI needs at run time (a shell? `git`? `ripgrep`?) from the docs of step 1.

- [ ] **Step 4: Does it try to update itself, and does a read-only directory break it?**

```sh
docker run --rm --entrypoint /opt/claude/claude -v "$PWD/ro:/opt/claude:ro" --read-only --tmpfs /tmp --tmpfs /home/nonroot:uid=65532 -u 65532 -e HOME=/home/nonroot debian:bookworm-slim --version
docker run --rm --entrypoint /opt/claude/claude -v "$PWD/ro:/opt/claude:ro" --read-only --tmpfs /tmp --tmpfs /home/nonroot:uid=65532 -u 65532 -e HOME=/home/nonroot debian:bookworm-slim --help | grep -i -E 'update|install|doctor' | head
```

Expected: `--version` works with a read-only root and a read-only install directory. Write down which update-related subcommands exist and whether the docs name a way to turn the automatic update off (an environment variable or a setting). Remedy's `provider.FilterEnv` would drop an unknown variable, so write the exact name if there is one: K-1 does not add it by itself, the record says whether the read-only mount is enough.

- [ ] **Step 5: Write the decision and commit**

The record ends with a "Decision" block of exactly these lines (fill them):

```text
urlTemplate:    <the template, {version} and {platform}>
archive:        none | tar.gz   member: <name or empty>
platforms:      amd64=<name>  arm64=<name>
sha256 source:  published at <url> | pinned by us
runner base:    <image>
runtime needs:  <list>
read-only dir:  works | breaks (<what>)
pinned version: <version>
```

```bash
git add docs/research/k8s-s4-cli-install.md
git commit -m "docs(research): S4, how the claude CLI gets into the runner pod

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: S1, a login directory on the host

**Files:**
- Create: `scripts/spike/k8s/s1-kind.yaml`, `scripts/spike/k8s/s1-storage.yaml`, `scripts/spike/k8s/s1-pod.yaml`, `scripts/spike/k8s/s1.sh`, `docs/research/k8s-s1-hostpath-login.md`

**Interfaces:**
- Produces (for K-4): the directory mode and ownership that work, whether `extraMounts` needs the directory to exist first, whether a file a pod writes as uid 65532 can be read by the host user and survives deleting and creating the cluster, and the exact `PersistentVolume` spec that worked.

- [ ] **Step 1: The throwaway cluster definition**

Create `scripts/spike/k8s/s1-kind.yaml` (throwaway; the real definition is made in K-4):

```yaml
# THROWAWAY (spike S1). A kind cluster whose node mounts a host directory.
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
name: remedy-spike
nodes:
  - role: control-plane
    extraMounts:
      - hostPath: __HOSTDIR__
        containerPath: /mnt/remedy-claude
```

- [ ] **Step 2: The storage and the pod**

Create `scripts/spike/k8s/s1-storage.yaml`:

```yaml
# THROWAWAY (spike S1).
apiVersion: v1
kind: Namespace
metadata:
  name: spike
---
apiVersion: v1
kind: PersistentVolume
metadata:
  name: remedy-claude
spec:
  capacity: {storage: 1Gi}
  accessModes: [ReadWriteOnce]
  persistentVolumeReclaimPolicy: Retain
  storageClassName: remedy-host
  hostPath: {path: /mnt/remedy-claude, type: Directory}
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: claude-state
  namespace: spike
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: remedy-host
  volumeName: remedy-claude
  resources: {requests: {storage: 1Gi}}
```

Create `scripts/spike/k8s/s1-pod.yaml`:

```yaml
# THROWAWAY (spike S1). Runs as the uid the runner will use and writes into the mounted state volume.
apiVersion: v1
kind: Pod
metadata:
  name: writer
  namespace: spike
spec:
  restartPolicy: Never
  securityContext:
    runAsNonRoot: true
    runAsUser: 65532
    runAsGroup: 65532
    fsGroup: 65532
    seccompProfile: {type: RuntimeDefault}
  containers:
    - name: writer
      image: busybox:1.37
      command:
        - sh
        - -c
        - |
          id
          mkdir -p /state/claude && echo "written by $(id -u) at $(date)" > /state/claude/probe.txt
          ls -ln /state /state/claude
          cat /state/claude/probe.txt
      securityContext:
        allowPrivilegeEscalation: false
        readOnlyRootFilesystem: true
        capabilities: {drop: [ALL]}
      volumeMounts:
        - {name: state, mountPath: /state}
  volumes:
    - name: state
      persistentVolumeClaim: {claimName: claude-state}
```

- [ ] **Step 3: The script that runs the whole spike**

Create `scripts/spike/k8s/s1.sh`:

```sh
#!/bin/sh
# THROWAWAY (spike S1). Shows whether a non-root pod can write into a host directory that kind mounts, whether the
# host can read what it wrote, and whether it survives a new cluster. It only touches ~/remedy-kind-spike and the
# kind cluster remedy-spike. Usage: scripts/spike/k8s/s1.sh [mode]   (mode is the host directory's mode, default 700)
set -eu
MODE=${1:-700}
DIR=$HOME/remedy-kind-spike
HERE=$(cd "$(dirname "$0")" && pwd)
CTX=kind-remedy-spike
k() { kubectl --context "$CTX" "$@"; }

rm -rf "$DIR"; mkdir -p "$DIR"; chmod "$MODE" "$DIR"
echo "host dir: $(ls -ld "$DIR")"

make_cluster() {
  sed "s|__HOSTDIR__|$DIR|" "$HERE/s1-kind.yaml" > "$DIR/../remedy-kind-spike.yaml"
  kind create cluster --config "$DIR/../remedy-kind-spike.yaml"
  k apply -f "$HERE/s1-storage.yaml"
}

echo "== first cluster"
make_cluster
k apply -f "$HERE/s1-pod.yaml"
k -n spike wait --for=jsonpath='{.status.phase}'=Succeeded pod/writer --timeout=120s || true
k -n spike logs writer
echo "== on the host after the pod"
ls -lan "$DIR" "$DIR/claude" 2>&1 || true
cat "$DIR/claude/probe.txt" 2>&1 || echo "the host cannot read the file"

echo "== delete and create the cluster again"
kind delete cluster --name remedy-spike
ls -lan "$DIR/claude" 2>&1 || echo "the files are gone"
make_cluster
k apply -f "$HERE/s1-pod.yaml"
k -n spike wait --for=jsonpath='{.status.phase}'=Succeeded pod/writer --timeout=120s || true
k -n spike logs writer
echo "== cleanup"
kind delete cluster --name remedy-spike
rm -f "$DIR/../remedy-kind-spike.yaml"
echo "left: $(ls -A "$DIR" | tr '\n' ' ')   (the directory $DIR stays so that you can inspect it; remove it by hand)"
```

- [ ] **Step 4: Run it with the default mode and with the mode that fails first**

```sh
chmod +x scripts/spike/k8s/s1.sh
scripts/spike/k8s/s1.sh 700
scripts/spike/k8s/s1.sh 755
```

Expected for each run: the `writer` pod's log shows `uid=65532`, the `ls` of `/state/claude`, and the line `written by 65532 …`. Note per run: whether the pod could write (if it fails with `Permission denied`, that is the answer for that mode, write the message); who owns the files on the host (`ls -lan`); whether `cat` on the host works; whether the file is still there after the cluster was deleted and created again, and whether the second pod could write over it. If the first run fails because `extraMounts` needs the directory first, the script already created it; if Docker Desktop refuses the path, write the message.

- [ ] **Step 5: Write the record and commit**

The record `docs/research/k8s-s1-hostpath-login.md` has the commands, the outputs (the `ls` lines, no more) and a "Decision" block:

```text
works with mode:      <700 | 755 | only 777 | neither>
file owner on host:   <user | root | uid 65532>
host can read:        yes | no
survives new cluster: yes | no
needs dir first:      yes | no
PV spec that worked:  the s1-storage.yaml as committed | <the change>
K-4 must:             <chmod/chown step, if any>
```

```bash
git add scripts/spike/k8s/s1-kind.yaml scripts/spike/k8s/s1-storage.yaml scripts/spike/k8s/s1-pod.yaml scripts/spike/k8s/s1.sh docs/research/k8s-s1-hostpath-login.md
git commit -m "docs(research): S1, a login directory on the host for the kind runner

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: S3, what NetworkPolicies do

**Files:**
- Create: `scripts/spike/k8s/s3-netpol.sh`, `docs/research/k8s-s3-networkpolicy.md`

**Interfaces:**
- Produces (for K-2): whether the CNI of kind enforces `NetworkPolicy` at all; the behaviour of an egress rule `0.0.0.0/0 except private ranges` for a pod that must reach the internet and a server pod but not the API server; whether an egress rule for the API server works as `ipBlock <node ip>/32 port 6443` (traffic to `kubernetes.default.svc` is rewritten to that address) or needs something else; the same three answers for the homelab's k3s.

- [ ] **Step 1: Write the script**

Create `scripts/spike/k8s/s3-netpol.sh`:

```sh
#!/bin/sh
# THROWAWAY (spike S3). Measures what NetworkPolicies do in the cluster of a kubectl context. It creates the
# namespace netpol-spike, three pods and some policies in it, and removes the namespace at the end. Nothing else.
# Usage: scripts/spike/k8s/s3-netpol.sh <kubectl context>
set -eu
CTX=${1:?usage: s3-netpol.sh <kubectl context>}
NS=netpol-spike
k() { kubectl --context "$CTX" -n "$NS" "$@"; }
trap 'kubectl --context "$CTX" delete namespace "$NS" --wait=false > /dev/null 2>&1 || true' EXIT

echo "context: $CTX"
kubectl --context "$CTX" get nodes -o wide | head -3
kubectl --context "$CTX" create namespace "$NS"

# The API server as pods see it: the service address and the endpoint behind it (what a rule has to name).
SVC_IP=$(kubectl --context "$CTX" -n default get svc kubernetes -o jsonpath='{.spec.clusterIP}')
EP_IP=$(kubectl --context "$CTX" -n default get endpoints kubernetes -o jsonpath='{.subsets[0].addresses[0].ip}')
EP_PORT=$(kubectl --context "$CTX" -n default get endpoints kubernetes -o jsonpath='{.subsets[0].ports[0].port}')
echo "api server: service $SVC_IP:443, endpoint $EP_IP:$EP_PORT"

cat <<EOF | k apply -f -
apiVersion: v1
kind: Pod
metadata: {name: web, labels: {app: web}}
spec:
  containers:
    - name: web
      image: busybox:1.37
      command: [sh, -c, "mkdir -p /www && echo ok > /www/index.html && httpd -f -p 8081 -h /www"]
---
apiVersion: v1
kind: Pod
metadata: {name: client, labels: {app: client}}
spec:
  containers:
    - name: client
      image: curlimages/curl:8.11.1
      command: [sleep, "3600"]
---
apiVersion: v1
kind: Pod
metadata: {name: other, labels: {app: other}}
spec:
  containers:
    - name: other
      image: curlimages/curl:8.11.1
      command: [sleep, "3600"]
EOF
k wait --for=condition=Ready pod/web pod/client pod/other --timeout=180s
WEB_IP=$(k get pod web -o jsonpath='{.status.podIP}')

# probe <what> <url>: prints the HTTP status the client got, or "blocked" when curl could not connect in 4 s.
probe() {
  code=$(k exec client -- curl -sk -m 4 -o /dev/null -w '%{http_code}' "$2" 2> /dev/null || true)
  case "$code" in 000|"") code=blocked ;; esac
  printf '  %-34s %s\n' "$1" "$code"
}
probe_all() {
  probe "web pod (private, 8081)" "http://$WEB_IP:8081/"
  probe "internet 443 (example.com)" "https://example.com/"
  probe "api service (kubernetes.default.svc)" "https://kubernetes.default.svc/version"
  probe "api endpoint ($EP_IP:$EP_PORT)" "https://$EP_IP:$EP_PORT/version"
  printf '  %-34s ' "dns (getent via nslookup)"; k exec client -- nslookup -timeout=3 example.com > /dev/null 2>&1 && echo ok || echo blocked
}

echo "== 1. no policy (the baseline: everything should be reachable; 401 or 403 from the api means reachable)"
probe_all

echo "== 2. default-deny egress for app=client (if these are still reachable, the CNI does not enforce policies)"
cat <<EOF | k apply -f -
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: deny-egress}
spec:
  podSelector: {matchLabels: {app: client}}
  policyTypes: [Egress]
EOF
sleep 5
probe_all

echo "== 3. the runner's shape: dns, the web pod, and 443 to the internet except private ranges"
k delete networkpolicy deny-egress
cat <<EOF | k apply -f -
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: runner-shape}
spec:
  podSelector: {matchLabels: {app: client}}
  policyTypes: [Egress]
  egress:
    - to:
        - namespaceSelector: {}
          podSelector: {matchLabels: {k8s-app: kube-dns}}
      ports: [{protocol: UDP, port: 53}, {protocol: TCP, port: 53}]
    - to:
        - podSelector: {matchLabels: {app: web}}
      ports: [{protocol: TCP, port: 8081}]
    - to:
        - ipBlock:
            cidr: 0.0.0.0/0
            except: [10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16]
      ports: [{protocol: TCP, port: 443}]
EOF
sleep 5
probe_all

echo "== 4. the server's shape: as 3, plus the api endpoint by address (does a rule on the endpoint let the service address through?)"
k delete networkpolicy runner-shape
cat <<EOF | k apply -f -
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: server-shape}
spec:
  podSelector: {matchLabels: {app: client}}
  policyTypes: [Egress]
  egress:
    - to:
        - namespaceSelector: {}
          podSelector: {matchLabels: {k8s-app: kube-dns}}
      ports: [{protocol: UDP, port: 53}, {protocol: TCP, port: 53}]
    - to: [{ipBlock: {cidr: $EP_IP/32}}]
      ports: [{protocol: TCP, port: $EP_PORT}]
EOF
sleep 5
probe_all

echo "== 5. ingress: only app=client may reach the web pod on 8081"
k delete networkpolicy server-shape
cat <<EOF | k apply -f -
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: web-ingress}
spec:
  podSelector: {matchLabels: {app: web}}
  policyTypes: [Ingress]
  ingress:
    - from: [{podSelector: {matchLabels: {app: client}}}]
      ports: [{protocol: TCP, port: 8081}]
EOF
sleep 5
printf '  %-34s ' "client -> web"; k exec client -- curl -s -m 4 -o /dev/null -w '%{http_code}\n' "http://$WEB_IP:8081/" || echo blocked
printf '  %-34s ' "other -> web (must be blocked)"; k exec other -- curl -s -m 4 -o /dev/null -w '%{http_code}\n' "http://$WEB_IP:8081/" 2> /dev/null || echo blocked
echo "== done; the namespace $NS is removed"
```

- [ ] **Step 2: Run it against kind (agent) and read the output**

```sh
chmod +x scripts/spike/k8s/s3-netpol.sh
kind create cluster --name remedy-spike
scripts/spike/k8s/s3-netpol.sh kind-remedy-spike 2>&1 | tee ~/remedy-spike/s3-kind.txt
kind delete cluster --name remedy-spike
```

Expected: section 1 shows every line reachable (`200`, or `401` and `403` for the API). Section 2 shows `blocked` everywhere **if** the CNI enforces policies. If section 2 still shows reachable lines, kind's default CNI does not enforce them: write that, and the dummy then runs with `networkPolicy.enabled: false` (the chart's test of the policies uses `helm template` only). Sections 3 and 4 are the answers for the two shapes: write which lines changed and, in section 4, whether the `api service` line works when only the endpoint address is allowed.

- [ ] **Step 3 (Maintainer): run it against the homelab and keep the output**

The agent asks the maintainer to run this once against the k3s cluster, naming the context:

```sh
scripts/spike/k8s/s3-netpol.sh <homelab context> 2>&1 | tee ~/remedy-spike/s3-k3s.txt
```

It creates the namespace `netpol-spike` with three tiny pods, applies and deletes five policies in it, and deletes the namespace. The maintainer pastes the output into the conversation. The agent does not run this step.

- [ ] **Step 4: Write the record and commit**

`docs/research/k8s-s3-networkpolicy.md` holds both outputs (kind and k3s, kept apart and labelled) and a "Decision" block:

```text
kind enforces policies:           yes | no
k3s enforces policies:            yes | no
runner shape (3) works on:        kind | k3s | both   (internet yes, web pod yes, api blocked)
api egress by endpoint address:   works on kind | works on k3s | neither (<what works instead>)
chart default networkPolicy:      enabled: true      dummy values: enabled: true | false
apiServer.cidrs for k3s:          <the endpoint address and port, as the maintainer's cluster showed>
ingress rule (5):                 enforced on kind | k3s
```

```bash
git add scripts/spike/k8s/s3-netpol.sh docs/research/k8s-s3-networkpolicy.md
git commit -m "docs(research): S3, what NetworkPolicies do on kind and on k3s

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 4: S2, how to tell whether the runner is logged in

**Files:**
- Create: `scripts/spike/k8s/s2-pod.yaml`, `docs/research/k8s-s2-login-check.md`

**Interfaces:**
- Consumes: the install route from S4's record.
- Produces (for K-6): the one command that tells "logged in" from "not logged in" without reading credentials, its exit codes and output in both states, its time and its cost, and whether it needs the network.

- [ ] **Step 1: The throwaway pod**

Create `scripts/spike/k8s/s2-pod.yaml`:

```yaml
# THROWAWAY (spike S2). A pod to install the CLI into and log in to, to try login checks. Not the runner.
apiVersion: v1
kind: Pod
metadata:
  name: s2
  namespace: default
spec:
  restartPolicy: Never
  securityContext: {runAsNonRoot: true, runAsUser: 65532, runAsGroup: 65532, fsGroup: 65532, seccompProfile: {type: RuntimeDefault}}
  containers:
    - name: s2
      image: debian:bookworm-slim   # replace with S4's runner base if that is a different image
      command: [sleep, "7200"]
      env:
        - {name: HOME, value: /tmp/home}
        - {name: CLAUDE_CONFIG_DIR, value: /tmp/home/claude}
      volumeMounts:
        - {name: tmp, mountPath: /tmp}
  volumes:
    - name: tmp
      emptyDir: {}
```

- [ ] **Step 2: Start the pod and install the CLI as S4 decided**

```sh
kind create cluster --name remedy-spike
kubectl --context kind-remedy-spike apply -f scripts/spike/k8s/s2-pod.yaml
kubectl --context kind-remedy-spike wait --for=condition=Ready pod/s2 --timeout=120s
```

Install the CLI into `/tmp/bin/claude` with the download, checksum and URL that S4's record gives (a `kubectl exec` with `curl` needs `curl`: if the base lacks it, `kubectl cp` the file downloaded in S4 to `/tmp/bin/claude` and `chmod +x` it through `kubectl exec`). Check: `kubectl exec s2 -- /tmp/bin/claude --version` prints the pinned version.

- [ ] **Step 3: The logged-out state**

```sh
kubectl --context kind-remedy-spike exec s2 -- sh -c 'mkdir -p /tmp/home/claude; for c in "auth status" "auth --help" "--help"; do echo "== claude $c"; /tmp/bin/claude $c; echo "exit=$?"; done' 2>&1 | head -80
```

Expected: you see which login-related subcommands the installed version has (`auth`, `login`, `status`, `doctor`, …) and what each says without a login. Write each candidate's exit code and its first line. If `auth status` does not exist, the candidates are the ones this `--help` shows; write them.

- [ ] **Step 4 (Maintainer): log in once**

The agent asks the maintainer to run, in their terminal (the `!` prefix runs it in the session):

```sh
kubectl --context kind-remedy-spike exec -it s2 -- /tmp/bin/claude
```

then `/login`, follow the URL in a browser, paste the code, and leave the CLI with `/exit`. The agent does not see the code and does not open any file the login created.

- [ ] **Step 5: The logged-in state and the cost of each candidate**

For each candidate from step 3 that looked like a status check, run it again now, with `time`:

```sh
kubectl --context kind-remedy-spike exec s2 -- sh -c 'time /tmp/bin/claude auth status; echo "exit=$?"' 2>&1 | head -20
```

Then the logged-out check on the same pod, to make the contrast exact: an empty config directory.

```sh
kubectl --context kind-remedy-spike exec s2 -- sh -c 'mkdir -p /tmp/empty; time CLAUDE_CONFIG_DIR=/tmp/empty /tmp/bin/claude auth status; echo "exit=$?"' 2>&1 | head -20
```

Expected: an exit code or a text that differs between the two. If the CLI has no status command, use the cheapest model call as the last resort and record what it cost:

```sh
kubectl --context kind-remedy-spike exec s2 -- sh -c 'time /tmp/bin/claude -p --output-format json --model <the pinned model> "reply with ok"; echo "exit=$?"' 2>&1 | head -30
```

Write whether each candidate calls the network, how long it took and (for the model call) the `total_cost_usd` or the usage it printed. Do not paste a token or a URL that carries a code.

- [ ] **Step 6: Clean up, write the record, commit**

```sh
kind delete cluster --name remedy-spike
kind get clusters
```

Expected: `remedy-spike` is gone. `docs/research/k8s-s2-login-check.md` holds the commands, the exit codes and the first lines of the outputs, and a "Decision" block:

```text
check command:         claude <args>     (K-6 sets loginCheckArgs to this)
logged in:             exit <n>, output matches <what>
not logged in:         exit <n>, output matches <what>
no config at all:      exit <n>
calls the network:     yes | no           takes: <seconds>
costs quota:           no | yes (<amount>)
K-6 parses:            <exit code | a phrase of the output>
```

```bash
git add scripts/spike/k8s/s2-pod.yaml docs/research/k8s-s2-login-check.md
git commit -m "docs(research): S2, how to tell whether the runner is logged in

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Feed the findings back into the spec and the plans

**Files:**
- Modify: `docs/specs/2026-10-06-kubernetes-deployment-design.md` (sections 3.2, 4, 5, 7 and 9)
- Modify: `docs/plans/k8s-1-images-and-listener.md`, `k8s-2-chart-core.md`, `k8s-4-dummy-setup.md`, `k8s-6-runner-status.md` where a record's decision block differs from what they assume (each plan names the record it consumes)

- [ ] **Step 1: Compare each decision block with the assumption that consumes it**

| Record | Consumed by | Assumed |
|---|---|---|
| S4 | K-1 task 5 (`Dockerfile.runner` base), K-2 task 3 (init container), `values.yaml` | `BASE` is `gcr.io/distroless/base-debian12:nonroot`; the artifact is a raw binary named by `platforms.<arch>.name`; the SHA-256 is pinned in values |
| S1 | K-4 task 1 (mode of the host directory, the PV) | mode `700`, the PV of `s1-storage.yaml` |
| S3 | K-2 task 5 (policy defaults), K-4 task 1 (dummy values) | kind enforces policies; the API rule is `ipBlock <endpoint>/32 :6443` |
| S2 | K-6 task 1 (`loginCheckArgs` and the parse rule) | the command is `auth status`, exit code 0 means logged in |

For every row where the record differs, edit the named plan's step (the code block and the sentence that names the assumption) and the spec's section; where it matches, write nothing.

- [ ] **Step 2: Record what changed**

Add to the top of this plan, under "Decisions made while planning", one row per correction: the record, the assumption, the result, the files changed. If nothing changed, add the row "No correction needed".

- [ ] **Step 3: Verify and commit**

Run: `grep -n 'TBD\|TODO' docs/specs/2026-10-06-kubernetes-deployment-design.md docs/plans/k8s-*.md`
Expected: no output.

```bash
git add docs/specs docs/plans
git commit -m "docs: the findings of the four Kubernetes spikes, fed back into the spec and the plans

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```
