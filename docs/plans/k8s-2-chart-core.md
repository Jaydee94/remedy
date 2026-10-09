# K-2: The Helm Chart's Core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `helm install` of `deploy/chart` runs the control plane (Deployment, Services, PVC) and the runner (StatefulSet with the init container that installs the pinned CLI) in a namespace, with an Ingress and NetworkPolicies as options, from a Secret that already exists. The chart has no cluster identities yet (plan K-3), and every invariant of the spec's section 5 that concerns what this plan renders is pinned by a test that fails the build.

**Architecture:** A plain Helm chart (`deploy/chart`, `apiVersion: v2`) with fixed resource names (`remedy-server`, `remedy-runner`; one release per namespace). Render tests are a Go test (`deploy/chart_test.go`) that runs `helm template`, decodes the YAML and asserts on the objects; `make chart-check` adds `helm lint` and `kubeconform`, and a CI job runs it.

**Tech Stack:** Helm 3, Go 1.27 (one new test-only dependency: `gopkg.in/yaml.v3`), kubeconform.

**Spec:** [`docs/specs/2026-10-06-kubernetes-deployment-design.md`](../specs/2026-10-06-kubernetes-deployment-design.md), sections 3, 4, 5 and 8 (the chart parts). Images and the listener are [plan K-1](k8s-1-images-and-listener.md); the S3 record ([`docs/research/k8s-s3-networkpolicy.md`](../research/k8s-s3-networkpolicy.md)) fixes the network policy defaults and the S4 record ([`docs/research/k8s-s4-cli-install.md`](../research/k8s-s4-cli-install.md)) the init container's inputs.

**Scope note:** No ServiceAccount token, no RBAC, no refresher and no `REMEDY_K8S_*` variable are in the chart yet; the server pod runs as the account `remedy-server`, which has no rights, until plan K-3 switches it to `remedy-read`. The runner has no probes (plan K-6). The chart is installed for real by plan K-4's dummy setup; here it is only rendered and linted.

## Decisions made while planning

| Topic | Spec said | This plan |
|---|---|---|
| Ingress rule of the server policy | `networkPolicy.ingressControllerNamespace` | `networkPolicy.public.from`, a list of NetworkPolicy peers. A namespace name cannot say "the node" (the kind testbed's NodePort comes from outside the cluster) or a pod label (Traefik in k3s); a list of peers can. The default is the `kube-system` namespace, which is where k3s's Traefik runs. |
| DNS peer | not mentioned | `networkPolicy.dnsNamespace` (default `kube-system`), the pods labelled `k8s-app: kube-dns`. |
| API server rule | `apiServer.cidrs` | Required (the render fails with a message) when `cluster.enabled` and the policy are both on: an egress rule that is silently missing would show as a server that cannot reach the cluster. |
| Names | not mentioned | Fixed: `remedy-server`, `remedy-server-internal`, `remedy-runner`, `remedy-data`, ServiceAccounts `remedy-server` and `remedy-runner`. The chart supports one release per namespace and says so; it keeps the names the spec, the runbooks and the dummy scripts use. |
| Secret-named env | "a render assertion fails on a key named like one" | `server.env` entries whose name contains `PASSWORD`, `TOKEN` or `KEY` make the render fail, and a test also checks that every such env var of the rendered pods comes from a Secret. |
| Runner `TERM` | not mentioned | `TERM=xterm-256color` in the runner container, so that `kubectl exec -it … claude` for the one-time login has a usable terminal. |
| Skipping when `helm` is missing | CI has helm | The Go test skips without `helm`, and fails instead when `REMEDY_REQUIRE_HELM` is set; `make chart-check` and the CI job set it. |
| `kubeconform` | in `make check` | `make chart-check` runs it when it is installed and says it skipped it otherwise; CI installs it. |
| What the API server rule can and cannot protect (spike S3, kind half) | `apiServer.cidrs` and `port: 6443` | Kept as planned: kind enforces policies, and a rule by endpoint address and port (`172.18.0.2/32:6443` in the record) lets the server reach the API, also through the service address `kubernetes.default.svc`. The record adds two consequences that the values comment and the chart's README now state. (1) The runner's rule is by address **and port**: with the API on 6443 (k3s and kind), a runner rule that allows only 443 to the public range blocks the API by port whatever the address; an API on 443 with a public address is **not** protected by the `except` list (inferred from the record's control run, not measured), while the usual homelab node ranges (`192.168.0.0/16`, `10.0.0.0/8`) are in the excepted ranges and are blocked by address. (2) Cluster pods have addresses in `10.0.0.0/8`, which the runner rule excepts, so traffic to a pod needs a pod or namespace selector rule, never an address. The k3s half of S3 is a maintainer step; nothing here assumes its result. |

## Global Constraints

- Everything committed is English: docs, templates, comments, commit messages.
- The chart never creates the admin password, the runner token or the master key: they come from `existingSecret`, and the render fails without `existingSecret.name`. No default value of any kind carries a secret.
- Pod hardening on every pod and container this chart renders: non-root (65532), `readOnlyRootFilesystem: true`, `allowPrivilegeEscalation: false`, all capabilities dropped, seccomp `RuntimeDefault`.
- The runner pod has no ServiceAccount token (`automountServiceAccountToken: false` on its ServiceAccount and on the pod).
- Only the public port of the control plane (8080) is reachable through the Ingress and the public Service; the internal port (8081, `/runner/v1`, `/mcp`) is reachable only from runner pods (NetworkPolicy) and in-cluster.
- Server: one replica, strategy `Recreate`, one RWO PVC. Runner: a StatefulSet with one replica.
- The image tag defaults to the chart's `appVersion`; no `latest`.
- `go test ./... -race -count=1` and `make check` pass at the end of every task. Every commit message ends with the trailer `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`.

## Review Focus

- A render without `existingSecret.name`, without `runner.cli.version`, with an Ingress and no host, or with the cluster on and no API server address: each must fail with a message that names the value (the tests below).
- A `server.env` entry that is a secret in disguise (`MY_API_TOKEN`): the render fails.
- A runner pod that could talk to the Kubernetes API: no token volume, no `serviceAccountToken` projection, `automountServiceAccountToken: false` twice (the tests inspect the rendered pod, not the template).
- A policy that lets the Ingress's peer reach the internal port: the public rule lists only 8080 and the internal rule lists only runner pods and 8081.
- A chart upgrade that deletes the database: the data PVC carries `helm.sh/resource-policy: keep`, and an existing claim is used as is.

## How to read the code blocks

A line `Create `path`:` or `Overwrite `path`:` is followed by the complete file. A line `In `path`, replace:` is followed by a block with the exact old text, a line `with:` and a block with the new text; the old text occurs exactly once in the file. Go code uses tabs; YAML uses two spaces.

## File Structure

| Path | Responsibility |
|---|---|
| `deploy/chart/Chart.yaml`, `values.yaml`, `templates/_helpers.tpl`, `templates/NOTES.txt` | Metadata, defaults, shared names and labels, what the installer reads |
| `deploy/chart/templates/server-*.yaml`, `serviceaccounts.yaml` | The control plane: ServiceAccount, Deployment, Services, PVC |
| `deploy/chart/templates/runner-*.yaml` | The runner: StatefulSet, headless Service |
| `deploy/chart/templates/ingress.yaml`, `networkpolicy.yaml` | The options: Ingress, policies |
| `deploy/chart/ci/lint-values.yaml` | The values `helm lint` and `kubeconform` need |
| `deploy/chart_test.go`, `deploy/helpers_test.go` | Render tests |
| `Makefile`, `.github/workflows/ci.yml` | `make chart-check`, the CI job |

---

### Task 1: The chart skeleton and the render test harness

**Files:**
- Create: `deploy/chart/Chart.yaml`, `deploy/chart/values.yaml`, `deploy/chart/templates/_helpers.tpl`, `deploy/chart/templates/validate.yaml`, `deploy/chart/ci/lint-values.yaml`, `deploy/chart/.helmignore`
- Create: `deploy/helpers_test.go`, `deploy/chart_test.go`
- Modify: `Makefile`, `.github/workflows/ci.yml`, `go.mod`, `go.sum`

**Interfaces:**
- Produces (Go test helpers, used by every later task): `baseValues() map[string]any`, `set(m map[string]any, path string, v any)`, `render(t, values) (docs, string, error)`, `mustRender(t, values) docs`, `docs.find(kind, name string) map[string]any`, `docs.all(kind string) []map[string]any`, `dig(t, v any, path ...any) any`, `container(t, pod map[string]any, name string) map[string]any`, `env(c map[string]any) map[string]map[string]any`.
- Produces (templates): the helpers `remedy.labels`, `remedy.selector` (called with `(dict "ctx" . "component" "server")`), `remedy.serverImage`, `remedy.runnerImage`, `remedy.secretName`, `remedy.privateRanges`.

- [ ] **Step 1: Add the YAML dependency**

Run: `go get gopkg.in/yaml.v3@v3.0.1 && go mod tidy`
Expected: `go.mod` gains `gopkg.in/yaml.v3 v3.0.1` (used by the tests only; `go mod tidy` keeps it because `deploy` imports it from a test file).

- [ ] **Step 2: Write the harness and the first failing test**

Create `deploy/helpers_test.go`:

```go
// Package deploy_test renders the Helm chart in deploy/chart and checks the objects. The tests run `helm template`, so
// they need helm: without it they skip, unless REMEDY_REQUIRE_HELM is set (make chart-check and CI set it).
package deploy_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const chartDir = "chart"

// baseValues are the values a chart needs to render at all, and nothing else.
func baseValues() map[string]any {
	sha := strings.Repeat("a", 64)
	return map[string]any{
		"existingSecret": map[string]any{"name": "remedy-secrets"},
		"runner": map[string]any{"cli": map[string]any{
			"version":     "2.1.288",
			"urlTemplate": "https://downloads.example.invalid/{version}/{platform}/claude",
			"archive":     "none",
			"platforms": map[string]any{
				"amd64": map[string]any{"name": "linux-x64", "sha256": sha},
				"arm64": map[string]any{"name": "linux-arm64", "sha256": sha},
			},
		}},
	}
}

// set puts v at a dotted path in m, creating the maps on the way.
func set(m map[string]any, path string, v any) {
	keys := strings.Split(path, ".")
	for _, k := range keys[:len(keys)-1] {
		next, ok := m[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[k] = next
		}
		m = next
	}
	m[keys[len(keys)-1]] = v
}

type docs []map[string]any

func helmBinary(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("helm")
	if err != nil {
		if os.Getenv("REMEDY_REQUIRE_HELM") != "" {
			t.Fatal("helm is required (REMEDY_REQUIRE_HELM is set) but is not installed")
		}
		t.Skip("helm is not installed")
	}
	return path
}

// render runs `helm template` for the values and returns the objects, the error output and the error of helm.
func render(t *testing.T, values map[string]any) (docs, string, error) {
	t.Helper()
	bin := helmBinary(t)
	file := filepath.Join(t.TempDir(), "values.yaml")
	raw, err := yaml.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "template", "remedy", chartDir, "--namespace", "remedy-system", "-f", file)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, stderr.String(), err
	}
	dec := yaml.NewDecoder(&stdout)
	var out docs
	for {
		var d map[string]any
		err := dec.Decode(&d)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("helm's output is not YAML: %v", err)
		}
		if d != nil {
			out = append(out, d)
		}
	}
	return out, "", nil
}

func mustRender(t *testing.T, values map[string]any) docs {
	t.Helper()
	d, stderr, err := render(t, values)
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, stderr)
	}
	return d
}

// mustFail renders and requires an error whose output contains want.
func mustFail(t *testing.T, values map[string]any, want string) {
	t.Helper()
	_, stderr, err := render(t, values)
	if err == nil {
		t.Fatalf("the render must fail with %q, but it succeeded", want)
	}
	if !strings.Contains(stderr, want) {
		t.Fatalf("the render failed, but its message does not contain %q:\n%s", want, stderr)
	}
}

func (d docs) all(kind string) []map[string]any {
	var out []map[string]any
	for _, o := range d {
		if o["kind"] == kind {
			out = append(out, o)
		}
	}
	return out
}

// find returns the object of the kind and name, or nil.
func (d docs) find(kind, name string) map[string]any {
	for _, o := range d.all(kind) {
		if dig(nil, o, "metadata", "name") == name {
			return o
		}
	}
	return nil
}

// dig walks maps by string keys and lists by int indexes and returns nil where the path ends. t may be nil.
func dig(t *testing.T, v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				return nil
			}
			v = m[k]
		case int:
			l, ok := v.([]any)
			if !ok || k >= len(l) {
				return nil
			}
			v = l[k]
		}
	}
	return v
}

// container returns the container or init container of a pod spec by name, or fails.
func container(t *testing.T, pod map[string]any, name string) map[string]any {
	t.Helper()
	for _, list := range []string{"containers", "initContainers"} {
		cs, _ := pod[list].([]any)
		for _, c := range cs {
			if m, ok := c.(map[string]any); ok && m["name"] == name {
				return m
			}
		}
	}
	t.Fatalf("no container %q in the pod", name)
	return nil
}

// env returns a container's environment by variable name.
func env(c map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	list, _ := c["env"].([]any)
	for _, e := range list {
		if m, ok := e.(map[string]any); ok {
			out[m["name"].(string)] = m
		}
	}
	return out
}

// toString is a value as YAML text, for substring checks.
func toString(v any) string {
	raw, _ := yaml.Marshal(v)
	return string(raw)
}

// podSpec returns the pod spec of a Deployment or StatefulSet.
func podSpec(t *testing.T, workload map[string]any) map[string]any {
	t.Helper()
	spec, ok := dig(t, workload, "spec", "template", "spec").(map[string]any)
	if !ok {
		t.Fatalf("%v has no pod spec", dig(t, workload, "metadata", "name"))
	}
	return spec
}
```

Create `deploy/chart_test.go`:

```go
package deploy_test

import "testing"

func TestTheRenderNeedsAnExistingSecret(t *testing.T) {
	values := baseValues()
	set(values, "existingSecret.name", "")
	mustFail(t, values, "existingSecret.name")
}
```

- [ ] **Step 3: Run it to see it fail**

Run: `go test ./deploy -run ExistingSecret -v`
Expected: FAIL: `helm template failed` is not reached; the test reports `Error: path "chart" not found` or `Chart.yaml file is missing` from helm, because the chart does not exist, and `mustFail` says the message does not contain `existingSecret.name`.

- [ ] **Step 4: Create the chart's metadata, defaults and helpers**

Create `deploy/chart/Chart.yaml`:

```yaml
apiVersion: v2
name: remedy
description: Remedy, an AI operator for a homelab GitOps setup. The control plane and the runner.
type: application
version: 0.1.0
appVersion: "0.1.0"
```

Create `deploy/chart/.helmignore`:

```text
ci/
*.orig
.git
```

Create `deploy/chart/values.yaml`:

```yaml
# Remedy. The chart runs the control plane (a Deployment) and the runner (a StatefulSet) in the release's namespace.
# It supports one release per namespace: the resource names are fixed (remedy-server, remedy-runner, ...).
#
# Nothing here is a secret. The admin password, the runner token and the master key come from a Secret that exists
# already (existingSecret), for example one a SealedSecret creates.

image:
  pullPolicy: IfNotPresent
  server:
    repository: ghcr.io/jaydee94/remedy-server
    tag: ""            # empty: the chart's appVersion
  runner:
    repository: ghcr.io/jaydee94/remedy-runner
    tag: ""

existingSecret:
  name: ""             # required
  keys:
    adminPassword: admin-password   # 12 or more characters
    runnerToken: runner-token       # 24 or more characters
    masterKey: master-key           # openssl rand -base64 32; losing it loses the sealed GitHub token

server:
  resources:
    requests: {cpu: 50m, memory: 128Mi}
    limits: {memory: 512Mi}
  persistence:
    size: 1Gi
    storageClass: ""   # empty: the cluster's default
    existingClaim: ""  # set: use this claim, create none
  service:
    type: ClusterIP
    nodePort: null     # only for type NodePort
  # Extra environment variables for the control plane, for example REMEDY_POLL_INTERVAL or REMEDY_LOG_LEVEL. A name that
  # contains PASSWORD, TOKEN or KEY is refused: secrets do not belong in values.
  env: {}

ingress:
  enabled: false
  className: ""
  host: ""             # required when enabled
  annotations: {}      # nginx: nginx.ingress.kubernetes.io/proxy-buffering: "off" keeps the live streams live
  tls:
    secretName: ""     # empty: no TLS block (then the cookie is not Secure, see the runbook)

runner:
  enabled: true
  model: ""            # REMEDY_CLAUDE_MODEL; the CLI runs with --restricted, which ignores user settings: pin it
  runTimeout: ""       # REMEDY_RUN_TIMEOUT, for example 10m
  resources:
    requests: {cpu: 100m, memory: 256Mi}
    limits: {memory: 1Gi}
  persistence:
    size: 1Gi
    storageClass: ""
    existingClaim: ""  # set: use this claim for the login and the CLI's state, create none
  # The init container installs this CLI before the runner starts. No image contains it.
  cli:
    version: ""        # required, pinned
    urlTemplate: ""    # required: an https URL with {version} and {platform}
    archive: none      # none, or tar.gz with member the file to take out of it
    member: ""
    platforms:         # the vendor's name for the platform and the SHA-256 of its artifact, per architecture
      amd64: {name: "", sha256: ""}
      arm64: {name: "", sha256: ""}

cluster:
  enabled: false       # the control plane reads the cluster (plan K-3 adds what this needs)

networkPolicy:
  enabled: true
  dnsNamespace: kube-system
  # Who may reach the public port (8080). The default is where k3s's Traefik runs.
  public:
    from:
      - namespaceSelector:
          matchLabels:
            kubernetes.io/metadata.name: kube-system
  # The address and port behind kubernetes.default.svc, as the cluster shows them (kubectl get endpoints kubernetes).
  # Required when cluster.enabled is true. The rule is by endpoint address and port (6443 on k3s and kind), not by the
  # service address. The runner's egress to the public range is 443 only, which keeps it from an API server on 6443 by
  # port; an API server on 443 with a public address is not protected by the except list. To let a pod reach a pod in
  # the cluster use a pod or namespace selector: pod addresses are in 10.0.0.0/8, which the runner's rule excepts.
  apiServer:
    cidrs: []
    port: 6443
```

Create `deploy/chart/templates/_helpers.tpl`:

```gotemplate
{{/* The labels every object carries. */}}
{{- define "remedy.labels" -}}
app.kubernetes.io/name: remedy
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | quote }}
{{- end }}

{{/* The labels that select the pods of a component. Call with (dict "ctx" . "component" "server"). */}}
{{- define "remedy.selector" -}}
app.kubernetes.io/name: remedy
app.kubernetes.io/instance: {{ .ctx.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end }}

{{- define "remedy.serverImage" -}}
{{ .Values.image.server.repository }}:{{ .Values.image.server.tag | default .Chart.AppVersion }}
{{- end }}

{{- define "remedy.runnerImage" -}}
{{ .Values.image.runner.repository }}:{{ .Values.image.runner.tag | default .Chart.AppVersion }}
{{- end }}

{{/* The Secret with the three application secrets. The chart never creates it. */}}
{{- define "remedy.secretName" -}}
{{ required "existingSecret.name is required: the chart never creates the admin password, the runner token or the master key" .Values.existingSecret.name }}
{{- end }}

{{/* The private ranges: an egress rule for the internet leaves them out. */}}
{{- define "remedy.privateRanges" -}}
- 10.0.0.0/8
- 172.16.0.0/12
- 192.168.0.0/16
- 169.254.0.0/16
{{- end }}
```

Create `deploy/chart/ci/lint-values.yaml`:

```yaml
# The values helm lint and kubeconform need to render the chart. Not used for an install.
existingSecret:
  name: remedy-secrets
runner:
  cli:
    version: 2.1.288
    urlTemplate: https://downloads.example.invalid/{version}/{platform}/claude
    archive: none
    platforms:
      amd64: {name: linux-x64, sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}
      arm64: {name: linux-arm64, sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}
```

Nothing uses `remedy.secretName` yet, so the first test would not fail on a missing name. Create `deploy/chart/templates/validate.yaml`, which renders nothing and fails the render for a value the chart cannot work without:

```yaml
{{- /* Fails the render for a value the chart cannot work without. It renders nothing. */ -}}
{{- $_ := include "remedy.secretName" . -}}
```

- [ ] **Step 5: Run the test**

Run: `go test ./deploy -run ExistingSecret -v`
Expected: PASS (the message `existingSecret.name is required: ...` contains `existingSecret.name`).

Also check the two behaviours without helm. Make a directory that holds only `go`, and use it as `PATH`:

```sh
only=$(mktemp -d) && ln -s "$(command -v go)" "$only/go"
PATH="$only" go test ./deploy -count=1 -v 2>&1 | grep -E 'SKIP|helm'
REMEDY_REQUIRE_HELM=1 PATH="$only" go test ./deploy -count=1 2>&1 | grep -E 'FAIL|helm'
```

Expected: the first prints `helm is not installed` in a `SKIP` line; the second fails with `helm is required (REMEDY_REQUIRE_HELM is set) but is not installed`.

- [ ] **Step 6: `make chart-check` and the CI job**

In `Makefile`, replace:

```make
.PHONY: help build build-go images test vet fmt web-install web-build web-lint web-test dev-server dev-web check
```

with:

```make
.PHONY: help build build-go images test vet fmt web-install web-build web-lint web-test dev-server dev-web chart-check check
```

replace:

```make
check: fmt vet test web-lint web-test web-build ## Everything CI checks
```

with:

```make
chart-check: ## Lint and render the Helm chart (needs helm; kubeconform when installed), then run its render tests
	@command -v helm > /dev/null || { echo "helm is needed" >&2; exit 1; }
	helm lint deploy/chart -f deploy/chart/ci/lint-values.yaml
	@if command -v kubeconform > /dev/null; then \
		helm template remedy deploy/chart --namespace remedy-system -f deploy/chart/ci/lint-values.yaml | kubeconform -strict -summary; \
	else echo "kubeconform is not installed: skipping the schema check (CI runs it)"; fi
	REMEDY_REQUIRE_HELM=1 go test ./deploy -count=1

check: fmt vet test chart-check web-lint web-test web-build ## Everything CI checks
```

In `.github/workflows/ci.yml`, append at the end of the `jobs:` map (two-space indentation, after the `web` job):

```yaml

  chart:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v7
        with:
          go-version-file: go.mod
      - uses: azure/setup-helm@v4
      - run: go install github.com/yannh/kubeconform/cmd/kubeconform@v0.6.7
      - run: make chart-check
```

- [ ] **Step 7: Verify and commit**

Run: `make chart-check`
Expected: `helm lint` prints `1 chart(s) linted, 0 chart(s) failed`, kubeconform runs or says it is skipped, the Go test passes.

```bash
git add deploy Makefile .github/workflows/ci.yml go.mod go.sum
git commit -m "feat(chart): the Helm chart's skeleton, a render test harness and make chart-check

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: The control plane: Deployment, Services, PVC

**Files:**
- Create: `deploy/chart/templates/serviceaccounts.yaml`, `server-deployment.yaml`, `server-service.yaml`, `server-pvc.yaml`
- Test: `deploy/chart_test.go`

**Interfaces:**
- Consumes: the helpers and test helpers of task 1.
- Produces: a Deployment `remedy-server` with container `server` (ports `http` 8080 and `internal` 8081), Services `remedy-server` (public, 8080) and `remedy-server-internal` (8081), the PVC `remedy-data`. Later plans add environment variables to the container and volumes to the pod.

- [ ] **Step 1: Write the failing tests**

Append to `deploy/chart_test.go` (add `"strings"` to the imports):

```go
func TestTheServerRunsOnceWithRecreateAndHardening(t *testing.T) {
	d := mustRender(t, baseValues())
	dep := d.find("Deployment", "remedy-server")
	if dep == nil {
		t.Fatal("no Deployment remedy-server")
	}
	if got := dig(t, dep, "spec", "replicas"); got != 1 {
		t.Fatalf("replicas = %v, want 1 (SQLite has one connection)", got)
	}
	if got := dig(t, dep, "spec", "strategy", "type"); got != "Recreate" {
		t.Fatalf("strategy = %v, want Recreate (a second pod must not open the same file)", got)
	}
	pod := podSpec(t, dep)
	if pod["automountServiceAccountToken"] != false {
		t.Fatalf("automountServiceAccountToken = %v, want false", pod["automountServiceAccountToken"])
	}
	if dig(t, pod, "securityContext", "runAsNonRoot") != true || dig(t, pod, "securityContext", "runAsUser") != 65532 ||
		dig(t, pod, "securityContext", "seccompProfile", "type") != "RuntimeDefault" {
		t.Fatalf("pod securityContext = %v", pod["securityContext"])
	}
	c := container(t, pod, "server")
	sc := c["securityContext"]
	if dig(t, sc, "readOnlyRootFilesystem") != true || dig(t, sc, "allowPrivilegeEscalation") != false ||
		dig(t, sc, "capabilities", "drop", 0) != "ALL" {
		t.Fatalf("container securityContext = %v", sc)
	}
	if dig(t, c, "readinessProbe", "httpGet", "path") != "/healthz" || dig(t, c, "livenessProbe", "httpGet", "path") != "/healthz" {
		t.Fatalf("probes = %v / %v", c["readinessProbe"], c["livenessProbe"])
	}
	if got := c["image"]; got != "ghcr.io/jaydee94/remedy-server:0.1.0" {
		t.Fatalf("image = %v, want the default repository at the chart's appVersion", got)
	}
}

func TestTheServerListensOnTwoPortsAndTakesItsSecretsFromTheSecret(t *testing.T) {
	d := mustRender(t, baseValues())
	c := container(t, podSpec(t, d.find("Deployment", "remedy-server")), "server")
	e := env(c)
	for name, want := range map[string]string{"REMEDY_ADDR": ":8080", "REMEDY_INTERNAL_ADDR": ":8081", "REMEDY_DB": "/data/remedy.db"} {
		if e[name]["value"] != want {
			t.Errorf("%s = %v, want %q", name, e[name]["value"], want)
		}
	}
	for name, key := range map[string]string{
		"REMEDY_ADMIN_PASSWORD": "admin-password", "REMEDY_RUNNER_TOKEN": "runner-token", "REMEDY_MASTER_KEY": "master-key",
	} {
		ref := dig(t, e[name], "valueFrom", "secretKeyRef")
		if dig(t, ref, "name") != "remedy-secrets" || dig(t, ref, "key") != key {
			t.Errorf("%s comes from %v, want the Secret remedy-secrets key %s", name, ref, key)
		}
		if _, literal := e[name]["value"]; literal {
			t.Errorf("%s has a literal value", name)
		}
	}
}

func TestNoSecretIsEverALiteralInARenderedEnvironment(t *testing.T) {
	values := baseValues()
	set(values, "server.env", map[string]any{"REMEDY_LOG_LEVEL": "debug"})
	d := mustRender(t, values)
	for _, o := range append(d.all("Deployment"), d.all("StatefulSet")...) {
		for _, list := range []string{"containers", "initContainers"} {
			cs, _ := podSpec(t, o)[list].([]any)
			for _, c := range cs {
				for name, e := range env(c.(map[string]any)) {
					secretLike := strings.Contains(name, "PASSWORD") || strings.Contains(name, "TOKEN") || strings.Contains(name, "KEY")
					if _, literal := e["value"]; secretLike && literal {
						t.Errorf("%s of %v has a literal value; a secret must come from a Secret", name, dig(t, o, "metadata", "name"))
					}
				}
			}
		}
	}
}

func TestServerEnvRefusesAnythingNamedLikeASecret(t *testing.T) {
	for _, name := range []string{"MY_API_TOKEN", "DB_PASSWORD", "SIGNING_KEY"} {
		values := baseValues()
		set(values, "server.env", map[string]any{name: "x"})
		mustFail(t, values, name)
	}
	values := baseValues()
	set(values, "server.env", map[string]any{"REMEDY_POLL_INTERVAL": "2m", "REMEDY_LOG_LEVEL": "debug"})
	e := env(container(t, podSpec(t, mustRender(t, values).find("Deployment", "remedy-server")), "server"))
	if e["REMEDY_POLL_INTERVAL"]["value"] != "2m" || e["REMEDY_LOG_LEVEL"]["value"] != "debug" {
		t.Fatalf("server.env did not reach the container: %v", e)
	}
}

func TestTheServicesSeparateThePublicAndTheInternalPort(t *testing.T) {
	d := mustRender(t, baseValues())
	public, internal := d.find("Service", "remedy-server"), d.find("Service", "remedy-server-internal")
	if public == nil || internal == nil {
		t.Fatal("both Services must exist")
	}
	if ports, _ := dig(t, public, "spec", "ports").([]any); len(ports) != 1 || dig(t, ports[0], "port") != 8080 {
		t.Fatalf("the public Service ports = %v, want only 8080", dig(t, public, "spec", "ports"))
	}
	if ports, _ := dig(t, internal, "spec", "ports").([]any); len(ports) != 1 || dig(t, ports[0], "port") != 8081 {
		t.Fatalf("the internal Service ports = %v, want only 8081", dig(t, internal, "spec", "ports"))
	}
	if dig(t, internal, "spec", "type") != "ClusterIP" {
		t.Fatalf("the internal Service type = %v, want ClusterIP", dig(t, internal, "spec", "type"))
	}
}

func TestThePublicServiceCanBeANodePort(t *testing.T) {
	values := baseValues()
	set(values, "server.service.type", "NodePort")
	set(values, "server.service.nodePort", 30080)
	svc := mustRender(t, values).find("Service", "remedy-server")
	if dig(t, svc, "spec", "type") != "NodePort" || dig(t, svc, "spec", "ports", 0, "nodePort") != 30080 {
		t.Fatalf("service = %v", svc["spec"])
	}
}

func TestTheDataVolumeIsKeptAndAnExistingClaimIsUsedAsIs(t *testing.T) {
	d := mustRender(t, baseValues())
	pvc := d.find("PersistentVolumeClaim", "remedy-data")
	if pvc == nil {
		t.Fatal("no PersistentVolumeClaim remedy-data")
	}
	if dig(t, pvc, "metadata", "annotations", "helm.sh/resource-policy") != "keep" {
		t.Fatalf("the data claim must survive helm uninstall: annotations = %v", dig(t, pvc, "metadata", "annotations"))
	}
	if dig(t, pvc, "spec", "accessModes", 0) != "ReadWriteOnce" {
		t.Fatalf("accessModes = %v", dig(t, pvc, "spec", "accessModes"))
	}

	values := baseValues()
	set(values, "server.persistence.existingClaim", "my-claim")
	d = mustRender(t, values)
	if d.find("PersistentVolumeClaim", "remedy-data") != nil {
		t.Fatal("no claim may be created when an existing one is named")
	}
	vols, _ := podSpec(t, d.find("Deployment", "remedy-server"))["volumes"].([]any)
	found := false
	for _, v := range vols {
		if dig(t, v, "persistentVolumeClaim", "claimName") == "my-claim" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the pod does not use my-claim: %v", vols)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./deploy -count=1 2>&1 | head -30`
Expected: FAIL: `no Deployment remedy-server` and the others.

- [ ] **Step 3: The control plane's account and the Deployment**

Create `deploy/chart/templates/serviceaccounts.yaml`:

```yaml
# The control plane's account. It has no rights and no token (the pod does not mount one); plan K-3 gives the
# control plane the read identity when the cluster is on.
apiVersion: v1
kind: ServiceAccount
metadata:
  name: remedy-server
  labels:
    {{- include "remedy.labels" . | nindent 4 }}
    app.kubernetes.io/component: server
automountServiceAccountToken: false
```

Create `deploy/chart/templates/server-deployment.yaml`:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: remedy-server
  labels:
    {{- include "remedy.labels" . | nindent 4 }}
    app.kubernetes.io/component: server
spec:
  replicas: 1
  # SQLite has one connection and one file: the old pod must be gone before the new one opens it.
  strategy:
    type: Recreate
  selector:
    matchLabels:
      {{- include "remedy.selector" (dict "ctx" . "component" "server") | nindent 6 }}
  template:
    metadata:
      labels:
        {{- include "remedy.labels" . | nindent 8 }}
        app.kubernetes.io/component: server
    spec:
      serviceAccountName: remedy-server
      automountServiceAccountToken: false
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        runAsGroup: 65532
        fsGroup: 65532
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: server
          image: {{ include "remedy.serverImage" . | quote }}
          imagePullPolicy: {{ .Values.image.pullPolicy }}
          ports:
            - {name: http, containerPort: 8080}
            - {name: internal, containerPort: 8081}
          env:
            - {name: REMEDY_ADDR, value: ":8080"}
            - {name: REMEDY_INTERNAL_ADDR, value: ":8081"}
            - {name: REMEDY_DB, value: /data/remedy.db}
            - name: REMEDY_ADMIN_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: {{ include "remedy.secretName" . }}
                  key: {{ .Values.existingSecret.keys.adminPassword }}
            - name: REMEDY_RUNNER_TOKEN
              valueFrom:
                secretKeyRef:
                  name: {{ include "remedy.secretName" . }}
                  key: {{ .Values.existingSecret.keys.runnerToken }}
            - name: REMEDY_MASTER_KEY
              valueFrom:
                secretKeyRef:
                  name: {{ include "remedy.secretName" . }}
                  key: {{ .Values.existingSecret.keys.masterKey }}
            {{- range $name, $value := .Values.server.env }}
            {{- if regexMatch "PASSWORD|TOKEN|KEY" $name }}
            {{- fail (printf "server.env.%s: a name that contains PASSWORD, TOKEN or KEY is refused; secrets belong in the existing Secret, not in values" $name) }}
            {{- end }}
            - name: {{ $name }}
              value: {{ $value | quote }}
            {{- end }}
          readinessProbe:
            httpGet: {path: /healthz, port: http}
            periodSeconds: 10
          livenessProbe:
            httpGet: {path: /healthz, port: http}
            initialDelaySeconds: 10
            periodSeconds: 20
          resources:
            {{- toYaml .Values.server.resources | nindent 12 }}
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: [ALL]
          volumeMounts:
            - {name: data, mountPath: /data}
            - {name: tmp, mountPath: /tmp}
      volumes:
        - name: data
          persistentVolumeClaim:
            claimName: {{ .Values.server.persistence.existingClaim | default "remedy-data" }}
        - name: tmp
          emptyDir: {}
```

Create `deploy/chart/templates/server-service.yaml`:

```yaml
# The public Service: the UI, /api and /healthz. This is the only one an Ingress or a NodePort may point at.
apiVersion: v1
kind: Service
metadata:
  name: remedy-server
  labels:
    {{- include "remedy.labels" . | nindent 4 }}
    app.kubernetes.io/component: server
spec:
  type: {{ .Values.server.service.type }}
  selector:
    {{- include "remedy.selector" (dict "ctx" . "component" "server") | nindent 4 }}
  ports:
    - name: http
      port: 8080
      targetPort: http
      {{- if and (eq .Values.server.service.type "NodePort") .Values.server.service.nodePort }}
      nodePort: {{ .Values.server.service.nodePort }}
      {{- end }}
---
# The internal Service: /runner/v1 and /mcp. Always ClusterIP; the NetworkPolicy lets only runner pods reach it.
apiVersion: v1
kind: Service
metadata:
  name: remedy-server-internal
  labels:
    {{- include "remedy.labels" . | nindent 4 }}
    app.kubernetes.io/component: server
spec:
  type: ClusterIP
  selector:
    {{- include "remedy.selector" (dict "ctx" . "component" "server") | nindent 4 }}
  ports:
    - name: internal
      port: 8081
      targetPort: internal
```

Create `deploy/chart/templates/server-pvc.yaml`:

```yaml
{{- if not .Values.server.persistence.existingClaim }}
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: remedy-data
  labels:
    {{- include "remedy.labels" . | nindent 4 }}
    app.kubernetes.io/component: server
  annotations:
    # The database is the only state. helm uninstall must not delete it.
    helm.sh/resource-policy: keep
spec:
  accessModes: [ReadWriteOnce]
  {{- with .Values.server.persistence.storageClass }}
  storageClassName: {{ . | quote }}
  {{- end }}
  resources:
    requests:
      storage: {{ .Values.server.persistence.size }}
{{- end }}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./deploy -count=1 -v 2>&1 | tail -30`
Expected: all tests of this task PASS. If `TestTheServicesSeparate...` reports `dig(... "port") != 8080` although the value prints as 8080, YAML decoded the number as `int`; the comparison with the untyped constant `8080` compares `any(int)`, which is equal, so a failure means the template is wrong.

- [ ] **Step 5: Commit**

```bash
git add deploy
git commit -m "feat(chart): the control plane's Deployment, Services and data volume

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: The runner: StatefulSet with the init container

**Files:**
- Create: `deploy/chart/templates/runner-statefulset.yaml`, `runner-service.yaml`
- Modify: `deploy/chart/templates/serviceaccounts.yaml` (append the runner's account)
- Test: `deploy/chart_test.go`

**Interfaces:**
- Consumes: task 1 and 2.
- Produces: a StatefulSet `remedy-runner` with the init container `install-cli` and the container `runner`; volumes `state`, `workspaces`, `claude-bin`, `tmp`; the ServiceAccount `remedy-runner` (no token). The container's environment is `REMEDY_SERVER_URL`, `REMEDY_RUNNER_TOKEN` (from the Secret), `REMEDY_WORKSPACES`, `REMEDY_CLAUDE_BIN`, `HOME`, `CLAUDE_CONFIG_DIR`, `TMPDIR`, `TERM` and optionally `REMEDY_CLAUDE_MODEL`, `REMEDY_RUN_TIMEOUT`.

- [ ] **Step 1: Write the failing tests**

Append to `deploy/chart_test.go`:

```go
func TestTheRunnerHasNoServiceAccountToken(t *testing.T) {
	d := mustRender(t, baseValues())
	sts := d.find("StatefulSet", "remedy-runner")
	if sts == nil {
		t.Fatal("no StatefulSet remedy-runner")
	}
	pod := podSpec(t, sts)
	if pod["automountServiceAccountToken"] != false {
		t.Fatalf("pod automountServiceAccountToken = %v, want false", pod["automountServiceAccountToken"])
	}
	sa := d.find("ServiceAccount", "remedy-runner")
	if sa == nil || sa["automountServiceAccountToken"] != false {
		t.Fatalf("the runner's ServiceAccount must exist and not mount a token: %v", sa)
	}
	if pod["serviceAccountName"] != "remedy-runner" {
		t.Fatalf("serviceAccountName = %v", pod["serviceAccountName"])
	}
	vols, _ := pod["volumes"].([]any)
	for _, v := range vols {
		if dig(t, v, "projected") != nil && strings.Contains(toString(v), "serviceAccountToken") {
			t.Fatalf("the runner pod has a projected service account token: %v", v)
		}
	}
}

func TestTheRunnerIsHardenedAndHasOneReplica(t *testing.T) {
	sts := mustRender(t, baseValues()).find("StatefulSet", "remedy-runner")
	if dig(t, sts, "spec", "replicas") != 1 {
		t.Fatalf("replicas = %v, want 1 (the runner is sequential)", dig(t, sts, "spec", "replicas"))
	}
	pod := podSpec(t, sts)
	if dig(t, pod, "securityContext", "runAsNonRoot") != true || dig(t, pod, "securityContext", "runAsUser") != 65532 ||
		dig(t, pod, "securityContext", "seccompProfile", "type") != "RuntimeDefault" {
		t.Fatalf("pod securityContext = %v", pod["securityContext"])
	}
	for _, name := range []string{"runner", "install-cli"} {
		sc := container(t, pod, name)["securityContext"]
		if dig(t, sc, "readOnlyRootFilesystem") != true || dig(t, sc, "allowPrivilegeEscalation") != false ||
			dig(t, sc, "capabilities", "drop", 0) != "ALL" {
			t.Errorf("%s securityContext = %v", name, sc)
		}
	}
}

func TestTheRunnerReachesTheInternalServiceWithItsTokenFromTheSecret(t *testing.T) {
	values := baseValues()
	set(values, "runner.model", "sonnet")
	set(values, "runner.runTimeout", "15m")
	e := env(container(t, podSpec(t, mustRender(t, values).find("StatefulSet", "remedy-runner")), "runner"))
	if e["REMEDY_SERVER_URL"]["value"] != "http://remedy-server-internal.remedy-system.svc:8081" {
		t.Errorf("REMEDY_SERVER_URL = %v", e["REMEDY_SERVER_URL"]["value"])
	}
	ref := dig(t, e["REMEDY_RUNNER_TOKEN"], "valueFrom", "secretKeyRef")
	if dig(t, ref, "name") != "remedy-secrets" || dig(t, ref, "key") != "runner-token" {
		t.Errorf("REMEDY_RUNNER_TOKEN comes from %v", ref)
	}
	for name, want := range map[string]string{
		"REMEDY_WORKSPACES": "/workspaces", "REMEDY_CLAUDE_BIN": "/opt/claude/claude", "HOME": "/state",
		"CLAUDE_CONFIG_DIR": "/state/claude", "TMPDIR": "/tmp", "TERM": "xterm-256color",
		"REMEDY_CLAUDE_MODEL": "sonnet", "REMEDY_RUN_TIMEOUT": "15m",
	} {
		if e[name]["value"] != want {
			t.Errorf("%s = %v, want %q", name, e[name]["value"], want)
		}
	}
	for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN"} {
		if _, ok := e[name]; ok {
			t.Errorf("the runner must not have %s: it would bill the API or hold a credential", name)
		}
	}
}

func TestTheInitContainerInstallsThePinnedCLIAndTheRunnerMountsItReadOnly(t *testing.T) {
	pod := podSpec(t, mustRender(t, baseValues()).find("StatefulSet", "remedy-runner"))
	init := container(t, pod, "install-cli")
	if init["image"] != container(t, pod, "runner")["image"] {
		t.Fatalf("the init container must use the runner image (the image has no shell): %v", init["image"])
	}
	args := toString(init["args"])
	sha := strings.Repeat("a", 64)
	for _, want := range []string{
		"install-cli", "--version=2.1.288",
		"--url-template=https://downloads.example.invalid/{version}/{platform}/claude",
		"--archive=none", "--dest=/opt/claude",
		"--platform=amd64=linux-x64@" + sha, "--platform=arm64=linux-arm64@" + sha,
	} {
		if !strings.Contains(args, want) {
			t.Errorf("the init container's args lack %q:\n%s", want, args)
		}
	}
	mounts, _ := container(t, pod, "runner")["volumeMounts"].([]any)
	readOnly := false
	for _, m := range mounts {
		if dig(t, m, "mountPath") == "/opt/claude" && dig(t, m, "readOnly") == true {
			readOnly = true
		}
	}
	if !readOnly {
		t.Fatalf("the runner must mount /opt/claude read-only: %v", mounts)
	}
}

func TestATarGzArtifactPassesItsMember(t *testing.T) {
	values := baseValues()
	set(values, "runner.cli.archive", "tar.gz")
	set(values, "runner.cli.member", "claude")
	args := toString(container(t, podSpec(t, mustRender(t, values).find("StatefulSet", "remedy-runner")), "install-cli")["args"])
	if !strings.Contains(args, "--archive=tar.gz") || !strings.Contains(args, "--member=claude") {
		t.Fatalf("args = %s", args)
	}
}

func TestTheRunnerNeedsThePinnedCLI(t *testing.T) {
	for path, want := range map[string]string{
		"runner.cli.version":     "runner.cli.version",
		"runner.cli.urlTemplate": "runner.cli.urlTemplate",
	} {
		values := baseValues()
		set(values, path, "")
		mustFail(t, values, want)
	}
	values := baseValues()
	set(values, "runner.cli.platforms.amd64.sha256", "")
	mustFail(t, values, "runner.cli.platforms.amd64.sha256")
}

func TestTheRunnerKeepsItsStateOnAClaimThatSurvivesARestart(t *testing.T) {
	sts := mustRender(t, baseValues()).find("StatefulSet", "remedy-runner")
	tpls, _ := dig(t, sts, "spec", "volumeClaimTemplates").([]any)
	if len(tpls) != 1 || dig(t, tpls[0], "metadata", "name") != "state" {
		t.Fatalf("volumeClaimTemplates = %v, want one named state", tpls)
	}

	values := baseValues()
	set(values, "runner.persistence.existingClaim", "claude-state")
	sts = mustRender(t, values).find("StatefulSet", "remedy-runner")
	if dig(t, sts, "spec", "volumeClaimTemplates") != nil {
		t.Fatal("no claim template may be rendered when an existing claim is named")
	}
	vols, _ := podSpec(t, sts)["volumes"].([]any)
	found := false
	for _, v := range vols {
		if dig(t, v, "name") == "state" && dig(t, v, "persistentVolumeClaim", "claimName") == "claude-state" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the state volume does not use claude-state: %v", vols)
	}
}

func TestTheRunnerCanBeLeftOut(t *testing.T) {
	values := baseValues()
	set(values, "runner.enabled", false)
	set(values, "runner.cli", map[string]any{})
	d := mustRender(t, values)
	if d.find("StatefulSet", "remedy-runner") != nil || d.find("ServiceAccount", "remedy-runner") != nil {
		t.Fatal("nothing of the runner may be rendered when runner.enabled is false")
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./deploy -count=1 2>&1 | head -30`
Expected: FAIL: `no StatefulSet remedy-runner`.

- [ ] **Step 3: The runner's account, Service and StatefulSet**

In `deploy/chart/templates/serviceaccounts.yaml`, append:

```yaml
{{- if .Values.runner.enabled }}
---
# The runner's account. The runner holds the CLI login and nothing else: no token for the Kubernetes API.
apiVersion: v1
kind: ServiceAccount
metadata:
  name: remedy-runner
  labels:
    {{- include "remedy.labels" . | nindent 4 }}
    app.kubernetes.io/component: runner
automountServiceAccountToken: false
{{- end }}
```

Create `deploy/chart/templates/runner-service.yaml`:

```yaml
{{- if .Values.runner.enabled }}
# Headless, for the StatefulSet's identity only. Nothing connects to the runner: it dials the control plane.
apiVersion: v1
kind: Service
metadata:
  name: remedy-runner
  labels:
    {{- include "remedy.labels" . | nindent 4 }}
    app.kubernetes.io/component: runner
spec:
  clusterIP: None
  selector:
    {{- include "remedy.selector" (dict "ctx" . "component" "runner") | nindent 4 }}
{{- end }}
```

Create `deploy/chart/templates/runner-statefulset.yaml`:

```yaml
{{- if .Values.runner.enabled }}
{{- $cli := .Values.runner.cli }}
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: remedy-runner
  labels:
    {{- include "remedy.labels" . | nindent 4 }}
    app.kubernetes.io/component: runner
spec:
  serviceName: remedy-runner
  replicas: 1
  selector:
    matchLabels:
      {{- include "remedy.selector" (dict "ctx" . "component" "runner") | nindent 6 }}
  template:
    metadata:
      labels:
        {{- include "remedy.labels" . | nindent 8 }}
        app.kubernetes.io/component: runner
    spec:
      serviceAccountName: remedy-runner
      automountServiceAccountToken: false
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        runAsGroup: 65532
        fsGroup: 65532
        seccompProfile:
          type: RuntimeDefault
      initContainers:
        # No image contains the claude CLI (the images are public, the binary is proprietary). The runner image installs
        # the pinned one into a volume, after checking its SHA-256.
        - name: install-cli
          image: {{ include "remedy.runnerImage" . | quote }}
          imagePullPolicy: {{ .Values.image.pullPolicy }}
          args:
            - install-cli
            - --version={{ required "runner.cli.version is required: the CLI is installed at this pinned version" $cli.version }}
            - --url-template={{ required "runner.cli.urlTemplate is required: an https URL with {version} and {platform}" $cli.urlTemplate }}
            - --archive={{ $cli.archive }}
            {{- if $cli.member }}
            - --member={{ $cli.member }}
            {{- end }}
            {{- range $arch, $p := $cli.platforms }}
            - --platform={{ $arch }}={{ required (printf "runner.cli.platforms.%s.name is required" $arch) $p.name }}@{{ required (printf "runner.cli.platforms.%s.sha256 is required" $arch) $p.sha256 }}
            {{- end }}
            - --dest=/opt/claude
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: [ALL]
          volumeMounts:
            - {name: claude-bin, mountPath: /opt/claude}
      containers:
        - name: runner
          image: {{ include "remedy.runnerImage" . | quote }}
          imagePullPolicy: {{ .Values.image.pullPolicy }}
          env:
            - {name: REMEDY_SERVER_URL, value: "http://remedy-server-internal.{{ .Release.Namespace }}.svc:8081"}
            - name: REMEDY_RUNNER_TOKEN
              valueFrom:
                secretKeyRef:
                  name: {{ include "remedy.secretName" . }}
                  key: {{ .Values.existingSecret.keys.runnerToken }}
            - {name: REMEDY_WORKSPACES, value: /workspaces}
            - {name: REMEDY_CLAUDE_BIN, value: /opt/claude/claude}
            # The login is in CLAUDE_CONFIG_DIR, on the state volume. `kubectl exec -it ... /opt/claude/claude` for the
            # one-time login inherits these, and TERM gives that session a usable terminal.
            - {name: HOME, value: /state}
            - {name: CLAUDE_CONFIG_DIR, value: /state/claude}
            - {name: TMPDIR, value: /tmp}
            - {name: TERM, value: xterm-256color}
            {{- with .Values.runner.model }}
            - {name: REMEDY_CLAUDE_MODEL, value: {{ . | quote }}}
            {{- end }}
            {{- with .Values.runner.runTimeout }}
            - {name: REMEDY_RUN_TIMEOUT, value: {{ . | quote }}}
            {{- end }}
          resources:
            {{- toYaml .Values.runner.resources | nindent 12 }}
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: [ALL]
          volumeMounts:
            - {name: state, mountPath: /state}
            - {name: workspaces, mountPath: /workspaces}
            - {name: claude-bin, mountPath: /opt/claude, readOnly: true}
            - {name: tmp, mountPath: /tmp}
      volumes:
        - name: workspaces
          emptyDir: {}
        - name: claude-bin
          emptyDir: {}
        - name: tmp
          emptyDir: {}
        {{- if .Values.runner.persistence.existingClaim }}
        - name: state
          persistentVolumeClaim:
            claimName: {{ .Values.runner.persistence.existingClaim }}
        {{- end }}
  {{- if not .Values.runner.persistence.existingClaim }}
  volumeClaimTemplates:
    - metadata:
        name: state
      spec:
        accessModes: [ReadWriteOnce]
        {{- with .Values.runner.persistence.storageClass }}
        storageClassName: {{ . | quote }}
        {{- end }}
        resources:
          requests:
            storage: {{ .Values.runner.persistence.size }}
  {{- end }}
{{- end }}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./deploy -count=1 -v 2>&1 | tail -40`
Expected: all PASS. A failure of `TestTheRunnerNeedsThePinnedCLI` for the `sha256` case means the `required` call inside the `range` did not fire: check the `printf` message contains the exact path `runner.cli.platforms.amd64.sha256`.

- [ ] **Step 5: Commit**

```bash
git add deploy
git commit -m "feat(chart): the runner, a StatefulSet whose init container installs the pinned CLI

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 4: The Ingress

**Files:**
- Create: `deploy/chart/templates/ingress.yaml`
- Test: `deploy/chart_test.go`

**Interfaces:**
- Consumes: the Service `remedy-server` port 8080.
- Produces: an optional Ingress `remedy`.

- [ ] **Step 1: Write the failing tests**

Append to `deploy/chart_test.go`:

```go
func TestThereIsNoIngressByDefault(t *testing.T) {
	if got := mustRender(t, baseValues()).all("Ingress"); len(got) != 0 {
		t.Fatalf("%d Ingress objects rendered, want none", len(got))
	}
}

func TestTheIngressPointsAtThePublicServiceOnly(t *testing.T) {
	values := baseValues()
	set(values, "ingress.enabled", true)
	set(values, "ingress.host", "remedy.example.invalid")
	set(values, "ingress.className", "traefik")
	set(values, "ingress.tls.secretName", "remedy-tls")
	set(values, "ingress.annotations", map[string]any{"cert-manager.io/cluster-issuer": "letsencrypt"})
	ing := mustRender(t, values).find("Ingress", "remedy")
	if ing == nil {
		t.Fatal("no Ingress remedy")
	}
	if dig(t, ing, "spec", "ingressClassName") != "traefik" {
		t.Errorf("ingressClassName = %v", dig(t, ing, "spec", "ingressClassName"))
	}
	rules, _ := dig(t, ing, "spec", "rules").([]any)
	if len(rules) != 1 || dig(t, rules[0], "host") != "remedy.example.invalid" {
		t.Fatalf("rules = %v", rules)
	}
	paths, _ := dig(t, rules[0], "http", "paths").([]any)
	if len(paths) != 1 || dig(t, paths[0], "path") != "/" || dig(t, paths[0], "backend", "service", "name") != "remedy-server" ||
		dig(t, paths[0], "backend", "service", "port", "number") != 8080 {
		t.Fatalf("paths = %v, want one path / to remedy-server:8080 (never the internal Service)", paths)
	}
	if dig(t, ing, "spec", "tls", 0, "secretName") != "remedy-tls" || dig(t, ing, "spec", "tls", 0, "hosts", 0) != "remedy.example.invalid" {
		t.Errorf("tls = %v", dig(t, ing, "spec", "tls"))
	}
	if dig(t, ing, "metadata", "annotations", "cert-manager.io/cluster-issuer") != "letsencrypt" {
		t.Errorf("annotations = %v", dig(t, ing, "metadata", "annotations"))
	}
}

func TestTheIngressNeedsAHostAndHasNoTLSBlockWithoutASecret(t *testing.T) {
	values := baseValues()
	set(values, "ingress.enabled", true)
	mustFail(t, values, "ingress.host")

	set(values, "ingress.host", "remedy.example.invalid")
	ing := mustRender(t, values).find("Ingress", "remedy")
	if dig(t, ing, "spec", "tls") != nil {
		t.Fatalf("tls = %v, want none without ingress.tls.secretName", dig(t, ing, "spec", "tls"))
	}
	if dig(t, ing, "spec", "ingressClassName") != nil {
		t.Fatalf("ingressClassName = %v, want none when className is empty (the cluster's default applies)", dig(t, ing, "spec", "ingressClassName"))
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./deploy -run Ingress -count=1 2>&1 | head -20`
Expected: `TestThereIsNoIngressByDefault` passes; the other two FAIL (`no Ingress remedy` and the render succeeding without a host).

- [ ] **Step 3: The template**

Create `deploy/chart/templates/ingress.yaml`:

```yaml
{{- if .Values.ingress.enabled }}
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: remedy
  labels:
    {{- include "remedy.labels" . | nindent 4 }}
  {{- with .Values.ingress.annotations }}
  annotations:
    {{- toYaml . | nindent 4 }}
  {{- end }}
spec:
  {{- with .Values.ingress.className }}
  ingressClassName: {{ . | quote }}
  {{- end }}
  {{- if .Values.ingress.tls.secretName }}
  tls:
    - hosts:
        - {{ required "ingress.host is required when the Ingress is enabled" .Values.ingress.host | quote }}
      secretName: {{ .Values.ingress.tls.secretName }}
  {{- end }}
  rules:
    - host: {{ required "ingress.host is required when the Ingress is enabled" .Values.ingress.host | quote }}
      http:
        paths:
          # Only the public Service. /runner/v1 and /mcp live on the internal port and are not reachable from here.
          - path: /
            pathType: Prefix
            backend:
              service:
                name: remedy-server
                port:
                  number: 8080
{{- end }}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./deploy -count=1 -v 2>&1 | tail -20`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add deploy
git commit -m "feat(chart): an optional Ingress for the public port

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 5: NetworkPolicies

**Files:**
- Create: `deploy/chart/templates/networkpolicy.yaml`
- Test: `deploy/chart_test.go`

**Interfaces:**
- Consumes: the pod labels of the server and the runner; `networkPolicy.*` values.
- Produces: NetworkPolicies `remedy-server` and `remedy-runner` (plan K-3 adds one for the refresher).

The defaults follow the S3 record. Before this task, read `docs/research/k8s-s3-networkpolicy.md` and, if its decision block says that the API rule must look different on k3s or kind (for example an extra peer), change the server's API-server egress rule below and the test that pins it to match. For kind the record says it does not: the rule below (`ipBlock <endpoint>/32` on the endpoint port) is the one that worked, and policies are on by default. The k3s lines of the record are not measured yet; when the maintainer fills them in, re-read this paragraph.

- [ ] **Step 1: Write the failing tests**

Append to `deploy/chart_test.go`:

```go
func policy(t *testing.T, d docs, name string) map[string]any {
	t.Helper()
	p := d.find("NetworkPolicy", name)
	if p == nil {
		t.Fatalf("no NetworkPolicy %s", name)
	}
	return p
}

// rulesText is a policy's ingress or egress rules as YAML text, for substring checks.
func rulesText(p map[string]any, which string) string { return toString(dig(nil, p, "spec", which)) }

func TestTheServerPolicyLetsOnlyRunnersReachTheInternalPort(t *testing.T) {
	p := policy(t, mustRender(t, baseValues()), "remedy-server")
	ingress, _ := dig(t, p, "spec", "ingress").([]any)
	if len(ingress) != 2 {
		t.Fatalf("%d ingress rules, want 2 (public, internal): %v", len(ingress), ingress)
	}
	public, internal := toString(ingress[0]), toString(ingress[1])
	if !strings.Contains(public, "port: 8080") || strings.Contains(public, "8081") {
		t.Errorf("the public rule must list only 8080:\n%s", public)
	}
	if !strings.Contains(public, "kubernetes.io/metadata.name: kube-system") {
		t.Errorf("the public rule must name the configured peers:\n%s", public)
	}
	if !strings.Contains(internal, "port: 8081") || strings.Contains(internal, "8080") ||
		!strings.Contains(internal, "app.kubernetes.io/component: runner") || strings.Contains(internal, "namespaceSelector") {
		t.Errorf("the internal rule must list only runner pods and 8081:\n%s", internal)
	}
}

func TestThePublicPeersAreConfigurable(t *testing.T) {
	values := baseValues()
	set(values, "networkPolicy.public.from", []any{map[string]any{"ipBlock": map[string]any{"cidr": "0.0.0.0/0"}}})
	p := policy(t, mustRender(t, values), "remedy-server")
	if got := rulesText(p, "ingress"); !strings.Contains(got, "cidr: 0.0.0.0/0") || strings.Contains(got, "kube-system") {
		t.Fatalf("ingress = %s", got)
	}
}

func TestTheRunnerPolicyAllowsNoIngressAndOnlyServerDNSAndTheInternet(t *testing.T) {
	p := policy(t, mustRender(t, baseValues()), "remedy-runner")
	types := toString(dig(t, p, "spec", "policyTypes"))
	if !strings.Contains(types, "Ingress") || !strings.Contains(types, "Egress") {
		t.Fatalf("policyTypes = %s, want both: the runner takes no connection", types)
	}
	if dig(t, p, "spec", "ingress") != nil {
		t.Fatalf("ingress = %v, want no rule at all", dig(t, p, "spec", "ingress"))
	}
	egress := rulesText(p, "egress")
	for _, want := range []string{
		"k8s-app: kube-dns", "app.kubernetes.io/component: server", "port: 8081", "port: 443",
		"cidr: 0.0.0.0/0", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16",
	} {
		if !strings.Contains(egress, want) {
			t.Errorf("the runner's egress lacks %q:\n%s", want, egress)
		}
	}
	if strings.Contains(egress, "6443") {
		t.Errorf("the runner must not be allowed to reach the API server:\n%s", egress)
	}
}

func TestTheServerPolicyNamesTheAPIServerOnlyWhenTheClusterIsOn(t *testing.T) {
	p := policy(t, mustRender(t, baseValues()), "remedy-server")
	if strings.Contains(rulesText(p, "egress"), "6443") {
		t.Fatalf("the cluster is off: no API server rule expected:\n%s", rulesText(p, "egress"))
	}

	values := baseValues()
	set(values, "cluster.enabled", true)
	mustFail(t, values, "networkPolicy.apiServer.cidrs")

	set(values, "networkPolicy.apiServer.cidrs", []any{"172.18.0.2/32"})
	p = policy(t, mustRender(t, values), "remedy-server")
	egress := rulesText(p, "egress")
	if !strings.Contains(egress, "cidr: 172.18.0.2/32") || !strings.Contains(egress, "port: 6443") {
		t.Fatalf("the API server rule is missing:\n%s", egress)
	}
}

func TestPoliciesCanBeTurnedOff(t *testing.T) {
	values := baseValues()
	set(values, "networkPolicy.enabled", false)
	if got := mustRender(t, values).all("NetworkPolicy"); len(got) != 0 {
		t.Fatalf("%d policies rendered, want none", len(got))
	}
}

func TestClusterOnWithoutPoliciesNeedsNoAPIServerAddress(t *testing.T) {
	values := baseValues()
	set(values, "networkPolicy.enabled", false)
	set(values, "cluster.enabled", true)
	mustRender(t, values)
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./deploy -run 'Policy|Policies|ClusterOn' -count=1 2>&1 | head -20`
Expected: FAIL (`no NetworkPolicy remedy-server`); `TestPoliciesCanBeTurnedOff` and `TestCluserOn...` pass already.

- [ ] **Step 3: The template**

Create `deploy/chart/templates/networkpolicy.yaml`:

```yaml
{{- if .Values.networkPolicy.enabled }}
# The control plane. Ingress: the public port from the configured peers (the Ingress controller, the node), the
# internal port from runner pods only. Egress: DNS, the Kubernetes API when the cluster is on, and HTTPS to the
# internet (GitHub), never to a private range.
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: remedy-server
  labels:
    {{- include "remedy.labels" . | nindent 4 }}
    app.kubernetes.io/component: server
spec:
  podSelector:
    matchLabels:
      {{- include "remedy.selector" (dict "ctx" . "component" "server") | nindent 6 }}
  policyTypes: [Ingress, Egress]
  ingress:
    - from:
        {{- toYaml .Values.networkPolicy.public.from | nindent 8 }}
      ports:
        - {protocol: TCP, port: 8080}
    - from:
        - podSelector:
            matchLabels:
              {{- include "remedy.selector" (dict "ctx" . "component" "runner") | nindent 14 }}
      ports:
        - {protocol: TCP, port: 8081}
  egress:
    - to:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: {{ .Values.networkPolicy.dnsNamespace }}
          podSelector:
            matchLabels:
              k8s-app: kube-dns
      ports:
        - {protocol: UDP, port: 53}
        - {protocol: TCP, port: 53}
    {{- if .Values.cluster.enabled }}
    {{- if not .Values.networkPolicy.apiServer.cidrs }}
    {{- fail "networkPolicy.apiServer.cidrs is required when cluster.enabled is true: the address behind kubernetes.default.svc (kubectl get endpoints kubernetes)" }}
    {{- end }}
    - to:
        {{- range .Values.networkPolicy.apiServer.cidrs }}
        - ipBlock:
            cidr: {{ . }}
        {{- end }}
      ports:
        - {protocol: TCP, port: {{ .Values.networkPolicy.apiServer.port }}}
    {{- end }}
    - to:
        - ipBlock:
            cidr: 0.0.0.0/0
            except:
              {{- include "remedy.privateRanges" . | nindent 14 }}
      ports:
        - {protocol: TCP, port: 443}
{{- if .Values.runner.enabled }}
---
# The runner. It takes no connection. It dials the control plane's internal port, resolves names, and reaches the
# internet on 443 (the CLI talks to its vendor); it cannot reach the Kubernetes API or any private range.
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: remedy-runner
  labels:
    {{- include "remedy.labels" . | nindent 4 }}
    app.kubernetes.io/component: runner
spec:
  podSelector:
    matchLabels:
      {{- include "remedy.selector" (dict "ctx" . "component" "runner") | nindent 6 }}
  policyTypes: [Ingress, Egress]
  egress:
    - to:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: {{ .Values.networkPolicy.dnsNamespace }}
          podSelector:
            matchLabels:
              k8s-app: kube-dns
      ports:
        - {protocol: UDP, port: 53}
        - {protocol: TCP, port: 53}
    - to:
        - podSelector:
            matchLabels:
              {{- include "remedy.selector" (dict "ctx" . "component" "server") | nindent 14 }}
      ports:
        - {protocol: TCP, port: 8081}
    - to:
        - ipBlock:
            cidr: 0.0.0.0/0
            except:
              {{- include "remedy.privateRanges" . | nindent 14 }}
      ports:
        - {protocol: TCP, port: 443}
{{- end }}
{{- end }}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./deploy -count=1 -v 2>&1 | tail -30`
Expected: all PASS. If `rulesText` finds `cidr: 0.0.0.0/0` in the "public peers" test of a configured peer, it is the configured peer, as intended; the check for `kube-system` must not match, because the DNS rule names `kube-system` as well: if `TestThePublicPeersAreConfigurable` fails on that, narrow its check to the ingress text only (it already uses `rulesText(p, "ingress")`, which contains no DNS rule).

- [ ] **Step 5: Commit**

```bash
git add deploy
git commit -m "feat(chart): NetworkPolicies for the control plane and the runner

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 6: What the installer reads, and the state of the project

**Files:**
- Create: `deploy/chart/templates/NOTES.txt`, `deploy/chart/README.md`
- Modify: `CLAUDE.md`

- [ ] **Step 1: NOTES.txt**

Create `deploy/chart/templates/NOTES.txt`:

```text
Remedy is installed in the namespace {{ .Release.Namespace }}.

1. The control plane: kubectl -n {{ .Release.Namespace }} rollout status deployment/remedy-server
{{- if .Values.ingress.enabled }}
   Open https://{{ .Values.ingress.host }} and sign in as the admin with the password in the Secret "{{ .Values.existingSecret.name }}".
{{- else }}
   There is no Ingress. Reach the public Service with: kubectl -n {{ .Release.Namespace }} port-forward service/remedy-server 8080:8080
{{- end }}
{{- if .Values.runner.enabled }}

2. The runner logs in once. It holds the only copy of the CLI login, on its state volume:
     kubectl -n {{ .Release.Namespace }} exec -it remedy-runner-0 -c runner -- /opt/claude/claude
   Type /login, open the URL it shows in a browser, paste the code, then /exit. The login survives restarts of the pod.
   Until then every run ends with "Not logged in".
{{- end }}
```

- [ ] **Step 2: The chart's README**

Create `deploy/chart/README.md`:

```markdown
# The Remedy chart

Runs the control plane and the runner in one namespace. One release per namespace: the resource names are fixed.
Spec: [`docs/specs/2026-10-06-kubernetes-deployment-design.md`](../../docs/specs/2026-10-06-kubernetes-deployment-design.md).

## What you need first

- A Secret with three keys (`admin-password`, `runner-token`, `master-key`; the names are values). The chart never creates
  it. `openssl rand -hex 12` (password), `openssl rand -hex 24` (runner token), `openssl rand -base64 32` (master key).
- The pinned CLI: `runner.cli.version`, `runner.cli.urlTemplate`, and the platform names and SHA-256 per architecture
  (`docs/research/k8s-s4-cli-install.md` says where they come from). No image contains the CLI.

## What it renders

| Object | Name | Note |
|---|---|---|
| Deployment | `remedy-server` | one replica, `Recreate`, ports 8080 (public) and 8081 (internal) |
| Services | `remedy-server`, `remedy-server-internal` | only the first may be exposed |
| PVC | `remedy-data` | kept on uninstall; `server.persistence.existingClaim` uses your own |
| StatefulSet | `remedy-runner` | init container `install-cli`; no Kubernetes token |
| Ingress | `remedy` | optional |
| NetworkPolicies | `remedy-server`, `remedy-runner` | on by default; `networkPolicy.*` |

The cluster tools (`cluster.*`) and their identities are added by plan K-3.

## NetworkPolicies: what the address rules mean

The rules are by address **and port**. The control plane reaches the API server through `networkPolicy.apiServer.cidrs`
and `port` (6443 on k3s and kind; fill them from `kubectl get endpoints kubernetes`, not from the service address). The
runner may reach TCP 443 on public addresses only, so an API server on 6443 is out of its reach by port. An API server on
443 with a public address would not be protected by the excepted private ranges; the usual homelab node addresses
(`192.168.0.0/16`, `10.0.0.0/8`) are inside them. Pods in the cluster have addresses in `10.0.0.0/8` too: allow traffic to a
pod with a pod or namespace selector, never with an address.

## Checking it

`make chart-check` lints, renders, validates (kubeconform) and runs the render tests in `deploy/`.
```

- [ ] **Step 3: Update CLAUDE.md**

In `CLAUDE.md`, in the "Current state" Kubernetes bullet added by plan K-1, append: ` The Helm chart's core (`deploy/chart`: control plane, runner, Ingress, NetworkPolicies) is in; `make chart-check` lints, renders and runs the render tests in `deploy/` (they need helm and skip without it, unless `REMEDY_REQUIRE_HELM` is set).` In the Commands block add:

```markdown
make chart-check                                 # helm lint, render, kubeconform (if installed) and the render tests of deploy/chart
```

- [ ] **Step 4: Verify and commit**

Run: `helm template remedy deploy/chart --namespace remedy-system -f deploy/chart/ci/lint-values.yaml --show-only templates/NOTES.txt 2>&1 | head -3; helm install --dry-run --generate-name deploy/chart -f deploy/chart/ci/lint-values.yaml 2>&1 | tail -12; make check`
Expected: the NOTES text shows in the dry run (`NOTES:` section), and `make check` passes.

```bash
git add deploy CLAUDE.md
git commit -m "docs(chart): install notes, the chart's README and the project's state

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```
