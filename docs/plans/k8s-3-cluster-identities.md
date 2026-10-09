# K-3: The Cluster Identities Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** With `cluster.enabled` and `cluster.write.enabled` the chart gives the control plane its two identities as the spec's D4 says: the pod runs as the read service account with a projected, rotating token; a CronJob and a post-install hook keep a short-lived token of the write account in a Secret that the pod mounts as a file. RBAC is exactly what the kind testbed's `rbac.yaml` and `argocd-rbac.yaml` have, nothing can read a Secret, and the release namespace can never be in the write allowlist. The server starts when the write token is not there yet and every action fails closed until it is.

**Architecture:** One server change (a write token file that is missing or empty is a start-up warning, not an error), then chart templates: ServiceAccounts, a read ClusterRole, write Roles per allowed namespace and for Argo CD, a Role for the refresher that names its two objects, an empty Secret, the CronJob and an identical hook Job that run `/remedy-tokenrefresh` from plan K-1, and the server pod's projected volume and environment. A real kind cluster proves it.

**Tech Stack:** Go 1.27 stdlib, Helm 3, kind, kubectl.

**Spec:** [`docs/specs/2026-10-06-kubernetes-deployment-design.md`](../specs/2026-10-06-kubernetes-deployment-design.md), sections 2 (D4), 3, 5 and 6 (change 3). The RBAC mirrors `dev/kind/rbac.yaml` and `dev/kind/argocd-rbac.yaml` and the rules of [`docs/specs/2026-10-04-phase-2c-cluster-design.md`](../specs/2026-10-04-phase-2c-cluster-design.md). Plans [K-1](k8s-1-images-and-listener.md) (the refresher binary) and [K-2](k8s-2-chart-core.md) (the chart and its test harness) are done.

**Scope note:** The runner is switched off in the real proof (`runner.enabled: false`): its CLI install needs S4's inputs and the runner itself is proved in plan K-4. The kind testbed of `dev/kind/up.sh` is not used here, because its `rbac.yaml` creates the same account names; the proof uses a plain kind cluster of its own.

## Decisions made while planning

| Topic | Spec said | This plan |
|---|---|---|
| Refresher's verb on the Secret | `get` and `update` (the spec's first text; section 5 of the amended spec says `patch`) | `patch` only, with `resourceNames: [remedy-write-token]`. The refresher sends a merge patch and never reads the Secret. |
| Read account's name | `remedy-read` | The server pod runs as `remedy-read` when `cluster.enabled`, otherwise as `remedy-server` (K-2's no-rights account). Only one of the two accounts is rendered. |
| ClusterRole name | `remedy-read` | `remedy-read-<release namespace>`: a ClusterRole is cluster-wide, and two releases in two namespaces must not collide. |
| Where the write Roles live | in the allowlisted namespaces | Helm installs a Role and a RoleBinding into each namespace of `cluster.write.namespaces` and one Role into `cluster.argoNamespace`. Those namespaces must exist when the chart is installed (a missing one fails the install with Helm's own message); the runbooks say so. |
| The server pod's CA | "the in-cluster file" | The pod does not mount the default token volume (`automountServiceAccountToken: false`). A projected volume carries the read token (`expirationSeconds: 3600`, the kubelet renews it) and the cluster's `kube-root-ca.crt` ConfigMap as `ca.crt`; `REMEDY_K8S_CA_FILE` points at it. |
| The write token Secret | an empty Secret the chart creates | A Secret without a `data` key. Helm's three-way merge leaves a field that neither the old nor the new manifest has, so an upgrade does not empty it; task 4 proves that with `helm upgrade --no-hooks`. |
| Refresher account's token | its own | The refresher pods keep the default token (`automountServiceAccountToken: true` on those pods only): it is the credential they authenticate with. |
| Hook mapping under Argo CD | PostSync | The chart's hook annotations are `post-install,post-upgrade`; Argo CD maps those to `PostSync`. The runbook of plan K-5 says so. |
| Fail-closed proof | "actions fail closed" | A unit test of the `Writer` (task 1) pins it. A live check would need a whole agent run; the real proof does not repeat it. |

## Global Constraints

- Everything committed is English: docs, templates, scripts, comments, commit messages.
- The write identity never reads and the read identity never changes: the read ClusterRole has only `get` and `list`, the write Roles only `patch` (workloads, Argo CD applications) and `delete` (pods), and no rule of any role names `secrets` or `configmaps`, except the refresher's, which names one Secret.
- The release namespace is never in `cluster.write.namespaces`; the render fails with a message that says so. Write needs the cluster to be on, and at least one namespace, each a valid namespace name.
- The refresher's role has `resourceNames` on every rule. It cannot create a Secret, read a Secret, or mint a token for any account but `remedy-write`.
- A token never appears in a log line, a template, a test's output or a record. The real proof prints hashes or lengths, never a token.
- No new Go or web dependencies. `go test ./... -race -count=1` and `make check` pass at the end of every task. Every commit message ends with the trailer `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`.

## Review Focus

- A start with a write token file that is missing, empty or filled later: starts, warns, fails each action closed, and works when the file fills, without a restart (task 1).
- A write token file that holds two lines or nothing readable at all (a directory): still refused at start; only "missing" and "empty" are tolerated (task 1).
- `cluster.write.namespaces: [remedy-system]`, `[]`, `["Demo"]`, or write on while the cluster is off: each fails the render with a message that names the value (task 3).
- A role that grew a verb: the tests compare each role's rules to the exact expected set, so an added `get` on a write role or a `secrets` rule fails (task 3).
- The refresher failing: a failed hook fails `helm install` visibly, and the CronJob keeps its last three failed jobs for `kubectl logs` (task 3, task 4).

## How to read the code blocks

A line `Create `path`:` or `Overwrite `path`:` is followed by the complete file. A line `In `path`, replace:` is followed by a block with the exact old text, a line `with:` and a block with the new text; the old text occurs exactly once in the file. Go code uses tabs; YAML uses two spaces.

## File Structure

| Path | Responsibility |
|---|---|
| `internal/kube/config.go`, `internal/kube/client.go` | A write token that is not there yet is a warning |
| `deploy/chart/values.yaml`, `templates/validate.yaml`, `templates/_helpers.tpl` | The `cluster.*` values, the render checks, the server account helper |
| `deploy/chart/templates/serviceaccounts.yaml` | The four accounts |
| `deploy/chart/templates/rbac-read.yaml`, `rbac-write.yaml`, `rbac-refresher.yaml` | The three permission sets |
| `deploy/chart/templates/write-token-secret.yaml`, `cronjob-token-refresh.yaml`, `job-token-refresh-hook.yaml` | The write token's Secret and what fills it |
| `deploy/chart/templates/server-deployment.yaml`, `networkpolicy.yaml` | The server's projected token, CA and write token volume and environment; the refresher's policy |
| `deploy/identities_test.go` | Render tests for all of it |
| `dev/kind/check-chart-identities.sh`, `docs/research/k8s-identities-real-run.md` | The proof on a real cluster |

---

### Task 1: A write token that is not there yet

**Files:**
- Modify: `internal/kube/client.go`, `internal/kube/config.go`
- Test: `internal/kube/config_test.go`, `internal/kube/writer_test.go`

**Interfaces:**
- Produces: `Config.Validate` accepts a `WriteTokenFile` that does not exist or holds only whitespace; `Config.Warnings` says so; the `Writer` reads the file again at every request and fails an action without sending anything until the file holds a token. The read token and a malformed write token file (two lines, unreadable) are still start-up errors.

- [ ] **Step 1: Write the failing tests**

Append to `internal/kube/config_test.go`:

```go
func TestValidateAcceptsAWriteTokenThatIsNotThereYet(t *testing.T) {
	read := writeFile(t, "read", "read-token\n")
	api := "https://k8s.example:6443"
	for name, file := range map[string]string{
		"missing": filepath.Join(t.TempDir(), "not-yet"),
		"empty":   writeFile(t, "empty", "\n"),
	} {
		c := Config{API: api, ReadTokenFile: read, WriteTokenFile: file, WriteNamespaces: []string{"demo"}}
		if err := c.Validate(); err != nil {
			t.Errorf("a %s write token file must be accepted at start-up, got %v", name, err)
		}
	}

	// What is there but wrong stays a mistake: two lines, or a path that is a directory.
	for name, file := range map[string]string{
		"two lines": writeFile(t, "two", "a\nb\n"),
		"directory": t.TempDir(),
	} {
		c := Config{API: api, ReadTokenFile: read, WriteTokenFile: file, WriteNamespaces: []string{"demo"}}
		if err := c.Validate(); err == nil {
			t.Errorf("a write token file that is a %s must still be refused", name)
		}
	}

	// The read token is as strict as before.
	c := Config{API: api, ReadTokenFile: filepath.Join(t.TempDir(), "not-yet")}
	if err := c.Validate(); err == nil {
		t.Error("a missing read token file must still be refused")
	}
}

func TestWarningsSayWhenTheWriteTokenIsNotThereYet(t *testing.T) {
	read := writeFile(t, "read", "r")
	base := Config{ReadTokenFile: read, WriteNamespaces: []string{"demo"}}
	for name, file := range map[string]string{
		"missing": filepath.Join(t.TempDir(), "not-yet"),
		"empty":   writeFile(t, "empty", " \n"),
	} {
		c := base
		c.WriteTokenFile = file
		w := c.Warnings()
		if len(w) != 1 || !strings.Contains(w[0], "REMEDY_K8S_WRITE_TOKEN_FILE") || !strings.Contains(w[0], "fail") {
			t.Errorf("%s: warnings = %v, want one that says actions fail until the file holds a token", name, w)
		}
	}
	c := base
	c.WriteTokenFile = writeFile(t, "write", "write-token\n")
	if w := c.Warnings(); len(w) != 0 {
		t.Errorf("a write token that is there: warnings = %v, want none", w)
	}
}
```

Append to `internal/kube/writer_test.go` (add `"os"` and `"path/filepath"` to its imports):

```go
func TestAnActionFailsClosedUntilTheWriteTokenExists(t *testing.T) {
	api := newFakeAPI(t, okReply())
	file := filepath.Join(t.TempDir(), "token")
	w, err := NewWriter(Config{
		API: api.URL, ReadTokenFile: writeFile(t, "read", "read-token"),
		WriteTokenFile: file, WriteNamespaces: []string{"demo"},
	})
	if err != nil {
		t.Fatalf("a writer must be built while the token file is not there yet: %v", err)
	}

	if err := w.DeletePod(context.Background(), "demo", "web-1"); err == nil {
		t.Fatal("an action must fail while there is no write token")
	}
	if n := len(api.requests()); n != 0 {
		t.Fatalf("%d requests were sent without a write token, want none", n)
	}

	if err := os.WriteFile(file, []byte("write-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := w.DeletePod(context.Background(), "demo", "web-1"); err != nil {
		t.Fatalf("the same writer must work once the file holds a token: %v", err)
	}
	got := api.requests()
	if len(got) != 1 || got[0].Method != "DELETE" || got[0].Auth != "Bearer write-token" {
		t.Fatalf("requests = %+v", got)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/kube -run 'NotThereYet|FailsClosedUntil' -v`
Expected: FAIL: `a missing write token file must be accepted at start-up, got REMEDY_K8S_WRITE_TOKEN_FILE: ...` and the writer test fails at `NewWriter`.

- [ ] **Step 3: Implement**

In `internal/kube/client.go`, replace:

```go
// readToken reads a token file. It is called for every request: a projected service account token is replaced
// by the kubelet before it expires.
func readToken(path string) (secret.Value, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return secret.Value{}, err
	}
	tok := strings.TrimSpace(string(raw))
	switch {
	case tok == "":
		return secret.Value{}, fmt.Errorf("the token file %s is empty", path)
```

with:

```go
// emptyTokenError is the error for a token file that exists and holds no token.
type emptyTokenError struct{ path string }

func (e emptyTokenError) Error() string { return fmt.Sprintf("the token file %s is empty", e.path) }

// readToken reads a token file. It is called for every request: a projected service account token is replaced
// by the kubelet before it expires.
func readToken(path string) (secret.Value, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return secret.Value{}, err
	}
	tok := strings.TrimSpace(string(raw))
	switch {
	case tok == "":
		return secret.Value{}, emptyTokenError{path}
```

In `internal/kube/config.go`, replace:

```go
	if c.WriteTokenFile != "" {
		if _, err := readToken(c.WriteTokenFile); err != nil {
			return fmt.Errorf("REMEDY_K8S_WRITE_TOKEN_FILE: %w", err)
		}
	}
```

with:

```go
	if c.WriteTokenFile != "" {
		// A refresher fills this file after the control plane has started (in a cluster the Secret it comes from is
		// empty at install), so a file that is not there yet or holds no token is not a mistake: Warnings says so
		// and every action fails until the file holds a token. It is read again at every request. A file that holds
		// something wrong is still refused.
		if _, err := readToken(c.WriteTokenFile); err != nil && !tokenNotThereYet(err) {
			return fmt.Errorf("REMEDY_K8S_WRITE_TOKEN_FILE: %w", err)
		}
	}
```

and replace:

```go
// Warnings lists what is configured but cannot be used, for a start-up log. It assumes a valid configuration.
func (c Config) Warnings() []string {
	var w []string
```

with:

```go
// tokenNotThereYet says whether readToken failed because the file does not exist or holds no token.
func tokenNotThereYet(err error) bool {
	var empty emptyTokenError
	return errors.Is(err, os.ErrNotExist) || errors.As(err, &empty)
}

// Warnings lists what is configured but cannot be used, for a start-up log. It assumes a valid configuration.
func (c Config) Warnings() []string {
	var w []string
	if c.WriteTokenFile != "" {
		if _, err := readToken(c.WriteTokenFile); err != nil && tokenNotThereYet(err) {
			w = append(w, "REMEDY_K8S_WRITE_TOKEN_FILE holds no token yet: every cluster action fails until it does (the token refresher fills it)")
		}
	}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/kube ./internal/config ./internal/app -race -count=1`
Expected: PASS, including the existing `TestValidate` (its "bad" cases never involved the write file's absence) and `TestWarnings`.

- [ ] **Step 5: Document the rule**

In `CLAUDE.md`, in the paragraph that lists the `REMEDY_K8S_*` variables ("The cluster (server only, all optional): …"), append: ` A write token file that is missing or empty at start-up is a warning, not an error (a refresher fills it in a cluster): every action fails until it holds a token; a file that holds two lines is still refused.`

- [ ] **Step 6: Commit**

```bash
git add internal/kube CLAUDE.md
git commit -m "feat(kube): a write token that is not there yet is a warning, and actions fail closed until it is

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Accounts and permissions

**Files:**
- Modify: `deploy/chart/values.yaml`, `deploy/chart/templates/validate.yaml`, `deploy/chart/templates/_helpers.tpl`, `deploy/chart/templates/server-deployment.yaml` (the account name only)
- Overwrite: `deploy/chart/templates/serviceaccounts.yaml`
- Create: `deploy/chart/templates/rbac-read.yaml`, `rbac-write.yaml`, `rbac-refresher.yaml`
- Test: `deploy/identities_test.go`

**Interfaces:**
- Consumes: the harness of plan K-2 (`baseValues`, `set`, `mustRender`, `mustFail`, `docs.find`, `docs.all`, `dig`, `toString`).
- Produces: ServiceAccounts `remedy-read` (cluster on), `remedy-write` and `remedy-token-refresher` (write on); ClusterRole and ClusterRoleBinding `remedy-read-<namespace>`; Roles and RoleBindings `remedy-write` in each write namespace and in the Argo namespace; Role and RoleBinding `remedy-token-refresher` in the release namespace; the helper `remedy.serverAccount`; the Go helpers `ruleSet(role) []string`, `wantRules(t, what, role, want...)` and `clusterValues()`.

- [ ] **Step 1: Write the failing tests**

Create `deploy/identities_test.go`:

```go
package deploy_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// clusterValues are the base values with the cluster on, read and write, and one write namespace. The policy needs the
// API server's address.
func clusterValues() map[string]any {
	v := baseValues()
	set(v, "cluster.enabled", true)
	set(v, "cluster.write.enabled", true)
	set(v, "cluster.write.namespaces", []any{"demo"})
	set(v, "networkPolicy.apiServer.cidrs", []any{"172.18.0.2/32"})
	return v
}

func strs(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, e := range list {
		out = append(out, fmt.Sprint(e))
	}
	return out
}

// ruleSet is a role's rules as sorted lines "groups|resources|verbs|resourceNames", each list joined by commas.
func ruleSet(role map[string]any) []string {
	var out []string
	rules, _ := role["rules"].([]any)
	for _, r := range rules {
		m := r.(map[string]any)
		out = append(out, strings.Join(strs(m["apiGroups"]), ",")+"|"+strings.Join(strs(m["resources"]), ",")+"|"+
			strings.Join(strs(m["verbs"]), ",")+"|"+strings.Join(strs(m["resourceNames"]), ","))
	}
	slices.Sort(out)
	return out
}

func wantRules(t *testing.T, what string, role map[string]any, want ...string) {
	t.Helper()
	if role == nil {
		t.Fatalf("%s does not exist", what)
	}
	slices.Sort(want)
	if got := ruleSet(role); !slices.Equal(got, want) {
		t.Fatalf("%s has the rules\n  %s\nwant exactly\n  %s", what, strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func subjectOf(t *testing.T, binding map[string]any) string {
	t.Helper()
	subjects, _ := binding["subjects"].([]any)
	if len(subjects) != 1 {
		t.Fatalf("%v has %d subjects, want 1", dig(t, binding, "metadata", "name"), len(subjects))
	}
	return fmt.Sprintf("%v/%v/%v", dig(t, subjects[0], "kind"), dig(t, subjects[0], "namespace"), dig(t, subjects[0], "name"))
}

func TestWithTheClusterOffThereIsNoClusterIdentity(t *testing.T) {
	d := mustRender(t, baseValues())
	for _, kind := range []string{"ClusterRole", "ClusterRoleBinding", "Role", "RoleBinding", "CronJob", "Job"} {
		if got := d.all(kind); len(got) != 0 {
			t.Errorf("%d %s objects rendered with the cluster off, want none", len(got), kind)
		}
	}
	for _, name := range []string{"remedy-read", "remedy-write", "remedy-token-refresher"} {
		if d.find("ServiceAccount", name) != nil {
			t.Errorf("the ServiceAccount %s must not exist with the cluster off", name)
		}
	}
	if d.find("ServiceAccount", "remedy-server") == nil {
		t.Error("the no-rights account remedy-server must exist with the cluster off")
	}
	if pod := podSpec(t, d.find("Deployment", "remedy-server")); pod["serviceAccountName"] != "remedy-server" {
		t.Errorf("serviceAccountName = %v, want remedy-server", pod["serviceAccountName"])
	}
}

func TestTheReadIdentityCanOnlyGetAndListWhatTheReadToolsShow(t *testing.T) {
	values := baseValues()
	set(values, "cluster.enabled", true)
	set(values, "networkPolicy.apiServer.cidrs", []any{"172.18.0.2/32"})
	d := mustRender(t, values)

	wantRules(t, "the read ClusterRole", d.find("ClusterRole", "remedy-read-remedy-system"),
		"|pods,pods/log,events,nodes|get,list|",
		"apps|deployments,statefulsets,daemonsets,replicasets|get,list|",
		"argoproj.io|applications|get,list|")
	if got := subjectOf(t, d.find("ClusterRoleBinding", "remedy-read-remedy-system")); got != "ServiceAccount/remedy-system/remedy-read" {
		t.Errorf("the read ClusterRoleBinding's subject = %s", got)
	}
	if d.find("ServiceAccount", "remedy-read") == nil || d.find("ServiceAccount", "remedy-server") != nil {
		t.Error("with the cluster on the control plane's account is remedy-read, and remedy-server is not rendered")
	}
	if pod := podSpec(t, d.find("Deployment", "remedy-server")); pod["serviceAccountName"] != "remedy-read" {
		t.Errorf("serviceAccountName = %v, want remedy-read", pod["serviceAccountName"])
	}
	if got := d.all("Role"); len(got) != 0 {
		t.Errorf("read only: %d Roles rendered, want none (no write identity)", len(got))
	}
	if d.find("ServiceAccount", "remedy-write") != nil {
		t.Error("read only: the write account must not exist")
	}
}

func TestTheWriteIdentityCanOnlyRestartDeleteAndSyncAndReadsNothing(t *testing.T) {
	values := clusterValues()
	set(values, "cluster.write.namespaces", []any{"demo", "staging"})
	d := mustRender(t, values)

	for _, ns := range []string{"demo", "staging"} {
		var role map[string]any
		for _, r := range d.all("Role") {
			if dig(t, r, "metadata", "name") == "remedy-write" && dig(t, r, "metadata", "namespace") == ns {
				role = r
			}
		}
		wantRules(t, "the write Role in "+ns, role, "apps|deployments,statefulsets,daemonsets|patch|", "|pods|delete|")
	}
	var argo map[string]any
	for _, r := range d.all("Role") {
		if dig(t, r, "metadata", "name") == "remedy-write" && dig(t, r, "metadata", "namespace") == "argocd" {
			argo = r
		}
	}
	wantRules(t, "the write Role in argocd", argo, "argoproj.io|applications|patch|")

	bindings := 0
	for _, b := range d.all("RoleBinding") {
		if dig(t, b, "metadata", "name") != "remedy-write" {
			continue
		}
		bindings++
		if got := subjectOf(t, b); got != "ServiceAccount/remedy-system/remedy-write" {
			t.Errorf("a write RoleBinding's subject = %s", got)
		}
	}
	if bindings != 3 {
		t.Errorf("%d write RoleBindings, want 3 (demo, staging, argocd)", bindings)
	}
	sa := d.find("ServiceAccount", "remedy-write")
	if sa == nil || sa["automountServiceAccountToken"] != false {
		t.Errorf("the write account must exist and mount no token: %v", sa)
	}
	for _, r := range d.all("Role") {
		for _, line := range ruleSet(r) {
			if strings.Contains(line, "secrets") && dig(t, r, "metadata", "name") == "remedy-write" {
				t.Errorf("a write role names secrets: %s", line)
			}
		}
	}
}

func TestTheArgoNamespaceIsConfigurable(t *testing.T) {
	values := clusterValues()
	set(values, "cluster.argoNamespace", "gitops")
	found := false
	for _, r := range mustRender(t, values).all("Role") {
		if dig(t, r, "metadata", "name") == "remedy-write" && dig(t, r, "metadata", "namespace") == "gitops" {
			found = true
		}
	}
	if !found {
		t.Fatal("no write Role in the configured Argo CD namespace")
	}
}

func TestTheRefresherCanMintOneAccountsTokenAndPatchOneSecretAndNothingElse(t *testing.T) {
	d := mustRender(t, clusterValues())
	var role map[string]any
	for _, r := range d.all("Role") {
		if dig(t, r, "metadata", "name") == "remedy-token-refresher" {
			role = r
			if dig(t, r, "metadata", "namespace") != "remedy-system" {
				t.Errorf("the refresher's Role is in %v, want the release namespace", dig(t, r, "metadata", "namespace"))
			}
		}
	}
	wantRules(t, "the refresher's Role", role,
		"|serviceaccounts/token|create|remedy-write",
		"|secrets|patch|remedy-write-token")
	for _, b := range d.all("RoleBinding") {
		if dig(t, b, "metadata", "name") == "remedy-token-refresher" {
			if got := subjectOf(t, b); got != "ServiceAccount/remedy-system/remedy-token-refresher" {
				t.Errorf("the refresher's binding subject = %s", got)
			}
		}
	}
	if d.find("ServiceAccount", "remedy-token-refresher") == nil {
		t.Error("no ServiceAccount remedy-token-refresher")
	}
}

func TestTheReleaseNamespaceCanNeverBeWritten(t *testing.T) {
	values := clusterValues()
	set(values, "cluster.write.namespaces", []any{"demo", "remedy-system"})
	mustFail(t, values, "remedy-system")
	_, stderr, _ := render(t, values)
	if !strings.Contains(stderr, "never change itself") {
		t.Errorf("the message must say why:\n%s", stderr)
	}
}

func TestWriteNeedsTheClusterAndANamespaceAndGoodNames(t *testing.T) {
	cases := map[string]func(map[string]any){
		"write without the cluster":  func(v map[string]any) { set(v, "cluster.enabled", false) },
		"no namespace":               func(v map[string]any) { set(v, "cluster.write.namespaces", []any{}) },
		"an upper case namespace":    func(v map[string]any) { set(v, "cluster.write.namespaces", []any{"Demo"}) },
		"a wildcard":                 func(v map[string]any) { set(v, "cluster.write.namespaces", []any{"*"}) },
		"a namespace with a comma":   func(v map[string]any) { set(v, "cluster.write.namespaces", []any{"a,b"}) },
	}
	for name, change := range cases {
		values := clusterValues()
		change(values)
		if _, _, err := render(t, values); err == nil {
			t.Errorf("%s: the render must fail", name)
		}
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./deploy -run 'Identity|ClusterOff|ReadIdentity|WriteIdentity|Refresher|ReleaseNamespace|WriteNeeds|ArgoNamespace' -count=1 2>&1 | head -30`
Expected: FAIL (`does not exist`, and the render succeeding where it must fail).

- [ ] **Step 3: Values, checks and the account helper**

In `deploy/chart/values.yaml`, replace:

```yaml
cluster:
  enabled: false       # the control plane reads the cluster (plan K-3 adds what this needs)
```

with:

```yaml
cluster:
  enabled: false       # the control plane reads the cluster; it runs as the read account (needs networkPolicy.apiServer.cidrs)
  api: https://kubernetes.default.svc
  argoNamespace: argocd   # where Argo CD keeps its Application objects
  write:
    enabled: false     # approved actions: restart a workload, delete a pod, refresh and sync an Argo CD application
    namespaces: []     # where actions may be used. Never the release namespace: Remedy may read itself, never change itself
    tokenRefresh:
      schedule: "*/30 * * * *"   # the write token is minted again this often ...
      lifetime: 2h                # ... and is valid this long, so a stopped job leaves at most this much
```

Replace the contents of `deploy/chart/templates/validate.yaml` with:

```yaml
{{- /* Fails the render for a value the chart cannot work without or must never accept. It renders nothing. */ -}}
{{- $_ := include "remedy.secretName" . -}}
{{- if .Values.cluster.write.enabled }}
{{- if not .Values.cluster.enabled }}
{{- fail "cluster.write.enabled needs cluster.enabled: the actions check their target with the read side" }}
{{- end }}
{{- if not .Values.cluster.write.namespaces }}
{{- fail "cluster.write.namespaces must name at least one namespace when cluster.write.enabled is true" }}
{{- end }}
{{- range .Values.cluster.write.namespaces }}
{{- if eq . $.Release.Namespace }}
{{- fail (printf "cluster.write.namespaces contains the release namespace %q: Remedy may read itself, never change itself" .) }}
{{- end }}
{{- if not (regexMatch "^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$" .) }}
{{- fail (printf "cluster.write.namespaces: %q is not a namespace name (lower case letters, digits and dashes)" .) }}
{{- end }}
{{- end }}
{{- end }}
```

In `deploy/chart/templates/_helpers.tpl`, append:

```gotemplate

{{/* The control plane's account: the read identity when the cluster is on, otherwise one with no rights. */}}
{{- define "remedy.serverAccount" -}}
{{- if .Values.cluster.enabled -}}remedy-read{{- else -}}remedy-server{{- end -}}
{{- end }}
```

In `deploy/chart/templates/server-deployment.yaml`, replace:

```yaml
      serviceAccountName: remedy-server
```

with:

```yaml
      serviceAccountName: {{ include "remedy.serverAccount" . }}
```

- [ ] **Step 4: The accounts and the three permission sets**

Overwrite `deploy/chart/templates/serviceaccounts.yaml`:

```yaml
{{- if not .Values.cluster.enabled }}
# The control plane's account without a cluster: no rights and no token (the pod does not mount one).
apiVersion: v1
kind: ServiceAccount
metadata:
  name: remedy-server
  labels:
    {{- include "remedy.labels" . | nindent 4 }}
    app.kubernetes.io/component: server
automountServiceAccountToken: false
{{- end }}
{{- if .Values.cluster.enabled }}
---
# The read identity, and the control plane's own account. The pod mounts its token through a projected volume of its
# own (server-deployment.yaml), never the default one.
apiVersion: v1
kind: ServiceAccount
metadata:
  name: remedy-read
  labels:
    {{- include "remedy.labels" . | nindent 4 }}
    app.kubernetes.io/component: server
automountServiceAccountToken: false
{{- end }}
{{- if .Values.cluster.write.enabled }}
---
# The write identity. No pod runs as it: the refresher mints tokens for it.
apiVersion: v1
kind: ServiceAccount
metadata:
  name: remedy-write
  labels:
    {{- include "remedy.labels" . | nindent 4 }}
    app.kubernetes.io/component: server
automountServiceAccountToken: false
---
# The refresher's account. It needs its own token to call the API, so the refresher's pods mount it (see the CronJob).
apiVersion: v1
kind: ServiceAccount
metadata:
  name: remedy-token-refresher
  labels:
    {{- include "remedy.labels" . | nindent 4 }}
    app.kubernetes.io/component: token-refresh
{{- end }}
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

Create `deploy/chart/templates/rbac-read.yaml`:

```yaml
{{- if .Values.cluster.enabled }}
# The read identity: get and list, and nothing else, on what the read tools show. Nothing here can read a Secret or a
# ConfigMap. The name carries the namespace because a ClusterRole is cluster-wide.
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: remedy-read-{{ .Release.Namespace }}
  labels:
    {{- include "remedy.labels" . | nindent 4 }}
rules:
  - apiGroups: [""]
    resources: [pods, pods/log, events, nodes]
    verbs: [get, list]
  - apiGroups: [apps]
    resources: [deployments, statefulsets, daemonsets, replicasets]
    verbs: [get, list]
  - apiGroups: [argoproj.io]
    resources: [applications]
    verbs: [get, list]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: remedy-read-{{ .Release.Namespace }}
  labels:
    {{- include "remedy.labels" . | nindent 4 }}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: remedy-read-{{ .Release.Namespace }}
subjects:
  - kind: ServiceAccount
    name: remedy-read
    namespace: {{ .Release.Namespace }}
{{- end }}
```

Create `deploy/chart/templates/rbac-write.yaml`:

```yaml
{{- if .Values.cluster.write.enabled }}
{{- range .Values.cluster.write.namespaces }}
---
# The write identity in a namespace the actions are allowed in: restart a workload, delete a pod. It reads nothing.
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: remedy-write
  namespace: {{ . }}
  labels:
    {{- include "remedy.labels" $ | nindent 4 }}
rules:
  - apiGroups: [apps]
    resources: [deployments, statefulsets, daemonsets]
    verbs: [patch]
  - apiGroups: [""]
    resources: [pods]
    verbs: [delete]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: remedy-write
  namespace: {{ . }}
  labels:
    {{- include "remedy.labels" $ | nindent 4 }}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: remedy-write
subjects:
  - kind: ServiceAccount
    name: remedy-write
    namespace: {{ $.Release.Namespace }}
{{- end }}
---
# The write identity may patch Argo CD applications, in the namespace they live in. RBAC cannot say "only applications
# that deploy to an allowed namespace": that limit is in Remedy's code.
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: remedy-write
  namespace: {{ .Values.cluster.argoNamespace }}
  labels:
    {{- include "remedy.labels" . | nindent 4 }}
rules:
  - apiGroups: [argoproj.io]
    resources: [applications]
    verbs: [patch]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: remedy-write
  namespace: {{ .Values.cluster.argoNamespace }}
  labels:
    {{- include "remedy.labels" . | nindent 4 }}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: remedy-write
subjects:
  - kind: ServiceAccount
    name: remedy-write
    namespace: {{ .Release.Namespace }}
{{- end }}
```

Create `deploy/chart/templates/rbac-refresher.yaml`:

```yaml
{{- if .Values.cluster.write.enabled }}
# The refresher mints a token for one account and patches one Secret. Every rule names its object: it cannot mint a token
# for another account, read a Secret, or create one (the chart creates the empty Secret for it).
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: remedy-token-refresher
  labels:
    {{- include "remedy.labels" . | nindent 4 }}
rules:
  - apiGroups: [""]
    resources: [serviceaccounts/token]
    resourceNames: [remedy-write]
    verbs: [create]
  - apiGroups: [""]
    resources: [secrets]
    resourceNames: [remedy-write-token]
    verbs: [patch]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: remedy-token-refresher
  labels:
    {{- include "remedy.labels" . | nindent 4 }}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: remedy-token-refresher
subjects:
  - kind: ServiceAccount
    name: remedy-token-refresher
    namespace: {{ .Release.Namespace }}
{{- end }}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./deploy -count=1 -v 2>&1 | tail -40`
Expected: all PASS, including every test of plans K-2 (the Deployment tests there do not depend on the account's name except `serviceAccountName`, which they do not assert). If `TestTheRefresherCanMint...` reports an `nindent`-induced YAML error, run `helm template remedy deploy/chart -n remedy-system -f deploy/chart/ci/lint-values.yaml --set cluster.enabled=true --set cluster.write.enabled=true --set 'cluster.write.namespaces={demo}' --set 'networkPolicy.apiServer.cidrs={1.2.3.4/32}'` and read the output near the error.

- [ ] **Step 6: Commit**

```bash
git add deploy
git commit -m "feat(chart): the read and write identities, the refresher's account, and exactly their permissions

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: The write token's Secret, the refresher, and the server's volumes

**Files:**
- Create: `deploy/chart/templates/write-token-secret.yaml`, `cronjob-token-refresh.yaml`, `job-token-refresh-hook.yaml`
- Modify: `deploy/chart/templates/_helpers.tpl`, `server-deployment.yaml`, `networkpolicy.yaml`, `deploy/chart/ci/lint-values.yaml`
- Test: `deploy/identities_test.go`

**Interfaces:**
- Consumes: task 2; the image of plan K-1 (`/remedy-tokenrefresh`, flags `--namespace --account --secret --lifetime --api`).
- Produces: Secret `remedy-write-token` (no data), CronJob `remedy-token-refresh`, hook Job `remedy-token-refresh-hook`, the server container's `REMEDY_K8S_*` environment and volumes `cluster-read` (projected) and `cluster-write` (Secret, optional), a NetworkPolicy `remedy-token-refresher`; the helper `remedy.refreshPod`.

- [ ] **Step 1: Write the failing tests**

Append to `deploy/identities_test.go`:

```go
func serverContainer(t *testing.T, d docs) (map[string]any, map[string]any) {
	t.Helper()
	pod := podSpec(t, d.find("Deployment", "remedy-server"))
	return pod, container(t, pod, "server")
}

func volume(pod map[string]any, name string) map[string]any {
	vols, _ := pod["volumes"].([]any)
	for _, v := range vols {
		if m := v.(map[string]any); m["name"] == name {
			return m
		}
	}
	return nil
}

func mounted(c map[string]any, path string) map[string]any {
	mounts, _ := c["volumeMounts"].([]any)
	for _, m := range mounts {
		if mm := m.(map[string]any); mm["mountPath"] == path {
			return mm
		}
	}
	return nil
}

func TestWithTheClusterOffTheServerHasNoClusterSettings(t *testing.T) {
	pod, c := serverContainer(t, mustRender(t, baseValues()))
	for name := range env(c) {
		if strings.HasPrefix(name, "REMEDY_K8S_") {
			t.Errorf("%s is set with the cluster off", name)
		}
	}
	if volume(pod, "cluster-read") != nil || volume(pod, "cluster-write") != nil {
		t.Error("no cluster volume expected with the cluster off")
	}
}

func TestTheServerReadsTheClusterWithAProjectedTokenAndTheClusterCA(t *testing.T) {
	values := baseValues()
	set(values, "cluster.enabled", true)
	set(values, "networkPolicy.apiServer.cidrs", []any{"172.18.0.2/32"})
	pod, c := serverContainer(t, mustRender(t, values))
	e := env(c)
	for name, want := range map[string]string{
		"REMEDY_K8S_API": "https://kubernetes.default.svc", "REMEDY_K8S_READ_TOKEN_FILE": "/var/run/remedy/read/token",
		"REMEDY_K8S_CA_FILE": "/var/run/remedy/read/ca.crt", "REMEDY_K8S_ARGO_NAMESPACE": "argocd",
	} {
		if e[name]["value"] != want {
			t.Errorf("%s = %v, want %q", name, e[name]["value"], want)
		}
	}
	for _, name := range []string{"REMEDY_K8S_WRITE_TOKEN_FILE", "REMEDY_K8S_WRITE_NAMESPACES"} {
		if _, ok := e[name]; ok {
			t.Errorf("%s is set although write is off", name)
		}
	}
	if pod["automountServiceAccountToken"] != false {
		t.Errorf("the pod must not mount the default token volume: %v", pod["automountServiceAccountToken"])
	}
	v := volume(pod, "cluster-read")
	if v == nil {
		t.Fatal("no cluster-read volume")
	}
	text := toString(v)
	for _, want := range []string{"serviceAccountToken:", "path: token", "expirationSeconds: 3600", "name: kube-root-ca.crt", "key: ca.crt", "path: ca.crt"} {
		if !strings.Contains(text, want) {
			t.Errorf("the cluster-read volume lacks %q:\n%s", want, text)
		}
	}
	if m := mounted(c, "/var/run/remedy/read"); m == nil || m["readOnly"] != true {
		t.Errorf("cluster-read must be mounted read-only at /var/run/remedy/read: %v", m)
	}
}

func TestTheServerTakesTheWriteTokenFromAnOptionalSecretAndTheAllowlistFromValues(t *testing.T) {
	values := clusterValues()
	set(values, "cluster.write.namespaces", []any{"demo", "staging"})
	pod, c := serverContainer(t, mustRender(t, values))
	e := env(c)
	if e["REMEDY_K8S_WRITE_TOKEN_FILE"]["value"] != "/var/run/remedy/write/token" {
		t.Errorf("REMEDY_K8S_WRITE_TOKEN_FILE = %v", e["REMEDY_K8S_WRITE_TOKEN_FILE"]["value"])
	}
	if e["REMEDY_K8S_WRITE_NAMESPACES"]["value"] != "demo,staging" {
		t.Errorf("REMEDY_K8S_WRITE_NAMESPACES = %v", e["REMEDY_K8S_WRITE_NAMESPACES"]["value"])
	}
	v := volume(pod, "cluster-write")
	if v == nil || dig(t, v, "secret", "secretName") != "remedy-write-token" || dig(t, v, "secret", "optional") != true {
		t.Fatalf("cluster-write = %v, want the optional Secret remedy-write-token (the server must start before it is filled)", v)
	}
	if m := mounted(c, "/var/run/remedy/write"); m == nil || m["readOnly"] != true {
		t.Errorf("cluster-write must be mounted read-only at /var/run/remedy/write: %v", m)
	}
	if m := mounted(c, "/var/run/remedy/write"); m != nil && m["subPath"] != nil {
		t.Error("a subPath mount is never updated by the kubelet: the refreshed token would not arrive")
	}
}

func TestTheWriteTokenSecretIsEmptyAndTheRefresherFillsItEveryHalfHour(t *testing.T) {
	d := mustRender(t, clusterValues())
	sec := d.find("Secret", "remedy-write-token")
	if sec == nil {
		t.Fatal("no Secret remedy-write-token")
	}
	if sec["data"] != nil || sec["stringData"] != nil {
		t.Fatalf("the Secret must carry no data (a value would be reset by an upgrade and live in git): %v", sec)
	}

	cj := d.find("CronJob", "remedy-token-refresh")
	if cj == nil {
		t.Fatal("no CronJob remedy-token-refresh")
	}
	if dig(t, cj, "spec", "schedule") != "*/30 * * * *" || dig(t, cj, "spec", "concurrencyPolicy") != "Forbid" {
		t.Errorf("schedule/concurrency = %v / %v", dig(t, cj, "spec", "schedule"), dig(t, cj, "spec", "concurrencyPolicy"))
	}
	pod := dig(t, cj, "spec", "jobTemplate", "spec", "template", "spec").(map[string]any)
	if pod["serviceAccountName"] != "remedy-token-refresher" || pod["automountServiceAccountToken"] != true || pod["restartPolicy"] != "Never" {
		t.Errorf("refresher pod = %v", pod)
	}
	c := container(t, pod, "refresh")
	if c["image"] != "ghcr.io/jaydee94/remedy-server:0.1.0" {
		t.Errorf("image = %v, want the control plane's image (it carries /remedy-tokenrefresh)", c["image"])
	}
	if got := toString(c["command"]); !strings.Contains(got, "/remedy-tokenrefresh") {
		t.Errorf("command = %s", got)
	}
	args := toString(c["args"])
	for _, want := range []string{"--namespace=remedy-system", "--account=remedy-write", "--secret=remedy-write-token", "--lifetime=2h", "--api=https://kubernetes.default.svc"} {
		if !strings.Contains(args, want) {
			t.Errorf("the refresher's args lack %q:\n%s", want, args)
		}
	}
	sc := c["securityContext"]
	if dig(t, sc, "readOnlyRootFilesystem") != true || dig(t, sc, "allowPrivilegeEscalation") != false || dig(t, sc, "capabilities", "drop", 0) != "ALL" {
		t.Errorf("the refresher's securityContext = %v", sc)
	}
	if dig(t, pod, "securityContext", "runAsNonRoot") != true || dig(t, pod, "securityContext", "seccompProfile", "type") != "RuntimeDefault" {
		t.Errorf("the refresher pod's securityContext = %v", pod["securityContext"])
	}
}

func TestAHookFillsTheTokenRightAfterAnInstallOrUpgrade(t *testing.T) {
	job := mustRender(t, clusterValues()).find("Job", "remedy-token-refresh-hook")
	if job == nil {
		t.Fatal("no hook Job remedy-token-refresh-hook")
	}
	ann := dig(t, job, "metadata", "annotations")
	if dig(t, ann, "helm.sh/hook") != "post-install,post-upgrade" {
		t.Errorf("hook = %v, want post-install,post-upgrade (Argo CD runs those as PostSync)", dig(t, ann, "helm.sh/hook"))
	}
	if got := fmt.Sprint(dig(t, ann, "helm.sh/hook-delete-policy")); !strings.Contains(got, "before-hook-creation") || !strings.Contains(got, "hook-succeeded") {
		t.Errorf("hook-delete-policy = %v", got)
	}
	pod := dig(t, job, "spec", "template", "spec").(map[string]any)
	if pod["serviceAccountName"] != "remedy-token-refresher" || pod["restartPolicy"] != "Never" {
		t.Errorf("hook pod = %v", pod)
	}
	// The same pod as the CronJob's: one definition.
	cj := mustRender(t, clusterValues()).find("CronJob", "remedy-token-refresh")
	cronPod := dig(t, cj, "spec", "jobTemplate", "spec", "template", "spec").(map[string]any)
	if toString(container(t, pod, "refresh")) != toString(container(t, cronPod, "refresh")) {
		t.Error("the hook and the CronJob must run the same container")
	}
}

func TestWithoutWriteThereIsNoRefresher(t *testing.T) {
	values := baseValues()
	set(values, "cluster.enabled", true)
	set(values, "networkPolicy.apiServer.cidrs", []any{"172.18.0.2/32"})
	d := mustRender(t, values)
	if d.find("Secret", "remedy-write-token") != nil || len(d.all("CronJob")) != 0 || len(d.all("Job")) != 0 {
		t.Fatal("no Secret, CronJob or Job expected without write")
	}
}

func TestTheRefresherMayOnlyReachDNSAndTheAPIServer(t *testing.T) {
	p := policy(t, mustRender(t, clusterValues()), "remedy-token-refresher")
	egress := rulesText(p, "egress")
	for _, want := range []string{"k8s-app: kube-dns", "cidr: 172.18.0.2/32", "port: 6443"} {
		if !strings.Contains(egress, want) {
			t.Errorf("the refresher's egress lacks %q:\n%s", want, egress)
		}
	}
	if strings.Contains(egress, "port: 443\n") || strings.Contains(egress, "port: 443}") || strings.Contains(egress, "0.0.0.0/0") {
		t.Errorf("the refresher must not reach the internet:\n%s", egress)
	}
	if dig(t, p, "spec", "ingress") != nil {
		t.Errorf("the refresher takes no connection: %v", dig(t, p, "spec", "ingress"))
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./deploy -count=1 2>&1 | head -30`
Expected: FAIL (`no cluster-read volume`, `no Secret remedy-write-token`, ...).

- [ ] **Step 3: The refresher's pod, once**

In `deploy/chart/templates/_helpers.tpl`, append:

```gotemplate

{{/* The pod of the token refresher, for the CronJob and for the hook Job. */}}
{{- define "remedy.refreshPod" -}}
restartPolicy: Never
serviceAccountName: remedy-token-refresher
automountServiceAccountToken: true
securityContext:
  runAsNonRoot: true
  runAsUser: 65532
  runAsGroup: 65532
  seccompProfile:
    type: RuntimeDefault
containers:
  - name: refresh
    image: {{ include "remedy.serverImage" . | quote }}
    imagePullPolicy: {{ .Values.image.pullPolicy }}
    command: ["/remedy-tokenrefresh"]
    args:
      - --namespace={{ .Release.Namespace }}
      - --account=remedy-write
      - --secret=remedy-write-token
      - --lifetime={{ .Values.cluster.write.tokenRefresh.lifetime }}
      - --api={{ .Values.cluster.api }}
    securityContext:
      allowPrivilegeEscalation: false
      readOnlyRootFilesystem: true
      capabilities:
        drop: [ALL]
{{- end }}
```

- [ ] **Step 4: The Secret, the CronJob and the hook**

Create `deploy/chart/templates/write-token-secret.yaml`:

```yaml
{{- if .Values.cluster.write.enabled }}
# The write identity's token. It has no data on purpose: the refresher fills it, the control plane mounts it as a file
# (optional, so that the server starts before the first token exists), and a chart upgrade leaves what the refresher
# wrote alone because neither manifest has a data field. Under Argo CD add an ignoreDifferences for /data.
apiVersion: v1
kind: Secret
metadata:
  name: remedy-write-token
  labels:
    {{- include "remedy.labels" . | nindent 4 }}
    app.kubernetes.io/component: token-refresh
type: Opaque
{{- end }}
```

Create `deploy/chart/templates/cronjob-token-refresh.yaml`:

```yaml
{{- if .Values.cluster.write.enabled }}
# Keeps the write token fresh. If this stops, the token expires after cluster.write.tokenRefresh.lifetime and the
# actions fail closed. The last three failed jobs stay for `kubectl logs`.
apiVersion: batch/v1
kind: CronJob
metadata:
  name: remedy-token-refresh
  labels:
    {{- include "remedy.labels" . | nindent 4 }}
    app.kubernetes.io/component: token-refresh
spec:
  schedule: {{ .Values.cluster.write.tokenRefresh.schedule | quote }}
  concurrencyPolicy: Forbid
  startingDeadlineSeconds: 300
  successfulJobsHistoryLimit: 1
  failedJobsHistoryLimit: 3
  jobTemplate:
    spec:
      backoffLimit: 2
      activeDeadlineSeconds: 120
      template:
        metadata:
          labels:
            {{- include "remedy.labels" . | nindent 12 }}
            app.kubernetes.io/component: token-refresh
        spec:
          {{- include "remedy.refreshPod" . | nindent 10 }}
{{- end }}
```

Create `deploy/chart/templates/job-token-refresh-hook.yaml`:

```yaml
{{- if .Values.cluster.write.enabled }}
# Fills the write token right after an install or an upgrade, so that the first action does not wait up to half an hour
# for the CronJob. A failed hook fails the install, visibly. Argo CD runs post-install and post-upgrade hooks as PostSync.
apiVersion: batch/v1
kind: Job
metadata:
  name: remedy-token-refresh-hook
  labels:
    {{- include "remedy.labels" . | nindent 4 }}
    app.kubernetes.io/component: token-refresh
  annotations:
    helm.sh/hook: post-install,post-upgrade
    helm.sh/hook-weight: "5"
    helm.sh/hook-delete-policy: before-hook-creation,hook-succeeded
spec:
  backoffLimit: 2
  activeDeadlineSeconds: 120
  template:
    metadata:
      labels:
        {{- include "remedy.labels" . | nindent 8 }}
        app.kubernetes.io/component: token-refresh
    spec:
      {{- include "remedy.refreshPod" . | nindent 6 }}
{{- end }}
```

- [ ] **Step 5: The server's environment and volumes**

In `deploy/chart/templates/server-deployment.yaml`, replace:

```yaml
            {{- range $name, $value := .Values.server.env }}
```

with:

```yaml
            {{- if .Values.cluster.enabled }}
            - {name: REMEDY_K8S_API, value: {{ .Values.cluster.api | quote }}}
            - {name: REMEDY_K8S_READ_TOKEN_FILE, value: /var/run/remedy/read/token}
            - {name: REMEDY_K8S_CA_FILE, value: /var/run/remedy/read/ca.crt}
            - {name: REMEDY_K8S_ARGO_NAMESPACE, value: {{ .Values.cluster.argoNamespace | quote }}}
            {{- end }}
            {{- if .Values.cluster.write.enabled }}
            - {name: REMEDY_K8S_WRITE_TOKEN_FILE, value: /var/run/remedy/write/token}
            - {name: REMEDY_K8S_WRITE_NAMESPACES, value: {{ join "," .Values.cluster.write.namespaces | quote }}}
            {{- end }}
            {{- range $name, $value := .Values.server.env }}
```

Replace:

```yaml
            - {name: tmp, mountPath: /tmp}
      volumes:
```

with:

```yaml
            - {name: tmp, mountPath: /tmp}
            {{- if .Values.cluster.enabled }}
            - {name: cluster-read, mountPath: /var/run/remedy/read, readOnly: true}
            {{- end }}
            {{- if .Values.cluster.write.enabled }}
            # A directory mount, never subPath: the kubelet does not update a subPath, and the token is replaced.
            - {name: cluster-write, mountPath: /var/run/remedy/write, readOnly: true}
            {{- end }}
      volumes:
```

and replace the last lines of the file:

```yaml
        - name: tmp
          emptyDir: {}
```

with:

```yaml
        - name: tmp
          emptyDir: {}
        {{- if .Values.cluster.enabled }}
        # The read identity: the pod's own account token, renewed by the kubelet, and the cluster's CA.
        - name: cluster-read
          projected:
            sources:
              - serviceAccountToken:
                  path: token
                  expirationSeconds: 3600
              - configMap:
                  name: kube-root-ca.crt
                  items:
                    - {key: ca.crt, path: ca.crt}
        {{- end }}
        {{- if .Values.cluster.write.enabled }}
        # The write identity: filled by the refresher after the first start, so the Secret is optional.
        - name: cluster-write
          secret:
            secretName: remedy-write-token
            optional: true
        {{- end }}
```

- [ ] **Step 6: The refresher's NetworkPolicy**

In `deploy/chart/templates/networkpolicy.yaml`, replace:

```yaml
        - {protocol: TCP, port: 443}
{{- end }}
{{- end }}
```

(the last three lines of the file) with:

```yaml
        - {protocol: TCP, port: 443}
{{- end }}
{{- if .Values.cluster.write.enabled }}
---
# The token refresher (the CronJob and the hook): DNS and the Kubernetes API, nothing else.
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: remedy-token-refresher
  labels:
    {{- include "remedy.labels" . | nindent 4 }}
    app.kubernetes.io/component: token-refresh
spec:
  podSelector:
    matchLabels:
      {{- include "remedy.selector" (dict "ctx" . "component" "token-refresh") | nindent 6 }}
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
        {{- range .Values.networkPolicy.apiServer.cidrs }}
        - ipBlock:
            cidr: {{ . }}
        {{- end }}
      ports:
        - {protocol: TCP, port: {{ .Values.networkPolicy.apiServer.port }}}
{{- end }}
{{- end }}
```

The labels of the refresher's pods are `app.kubernetes.io/component: token-refresh`; `remedy.selector` yields the same set with `app.kubernetes.io/instance`, which the pods' `remedy.labels` carry (`app.kubernetes.io/instance: {{ .Release.Name }}`).

- [ ] **Step 7: Make the lint values exercise it**

In `deploy/chart/ci/lint-values.yaml`, append:

```yaml
cluster:
  enabled: true
  write:
    enabled: true
    namespaces: [demo]
networkPolicy:
  apiServer:
    cidrs: [172.18.0.2/32]
```

- [ ] **Step 8: Run everything**

Run: `go test ./deploy -count=1 -v 2>&1 | tail -50 && make chart-check`
Expected: all PASS; `helm lint` passes; kubeconform (if installed) validates the new RBAC, CronJob and Job objects without errors. A failing `TestTheRefresherMayOnlyReachDNSAndTheAPIServer` on the `443` condition means the DNS or API rule contains a `443`; the condition is meant to reject a rule for port 443 or `0.0.0.0/0`, not the string inside `6443`.

- [ ] **Step 9: Commit**

```bash
git add deploy
git commit -m "feat(chart): the write token's Secret, the refresher CronJob and hook, and the server's cluster volumes

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Proof on a real cluster

**Files:**
- Create: `dev/kind/check-chart-identities.sh`, `docs/research/k8s-identities-real-run.md`
- Modify: `deploy/chart/README.md`, `CLAUDE.md`

**Interfaces:**
- Consumes: the images of plan K-1 (`make images`), the chart.
- Produces: a record that the chart's identities work on a real cluster, and a script that shows what each identity may and may not do with `kubectl auth can-i`.

- [ ] **Step 1: The permission check**

Create `dev/kind/check-chart-identities.sh`:

```sh
#!/bin/sh
# Shows what the three accounts the chart creates may and may not do, with `kubectl auth can-i --as`. Needs a cluster the
# chart is installed in, and cluster-admin rights to impersonate. It changes nothing.
# Usage: dev/kind/check-chart-identities.sh <kubectl context> <release namespace> <write namespace> [argocd namespace]
set -eu
CTX=${1:?usage: check-chart-identities.sh <context> <release namespace> <write namespace> [argocd namespace]}
NS=${2:?}
WNS=${3:?}
ARGO=${4:-argocd}
fail=0

# can <expected yes|no> <as> <what>... : runs `kubectl auth can-i` and compares.
can() {
  want=$1; as=$2; shift 2
  got=$(kubectl --context "$CTX" auth can-i "$@" --as="system:serviceaccount:$NS:$as" 2> /dev/null || true)
  if [ "$got" = "$want" ]; then
    printf 'ok    %-22s %-3s %s\n' "$as" "$got" "$*"
  else
    printf 'WRONG %-22s %-3s (wanted %s) %s\n' "$as" "$got" "$want" "$*"
    fail=1
  fi
}

echo "-- remedy-read: get and list what the read tools show, nothing else"
can yes remedy-read list pods --all-namespaces
can yes remedy-read get pods --subresource=log -n "$WNS"
can yes remedy-read list deployments.apps -n "$WNS"
can yes remedy-read list applications.argoproj.io -n "$ARGO"
can no  remedy-read get secrets -n "$WNS"
can no  remedy-read get secrets -n "$NS"
can no  remedy-read list configmaps -n "$WNS"
can no  remedy-read delete pods -n "$WNS"
can no  remedy-read patch deployments.apps -n "$WNS"
can no  remedy-read patch applications.argoproj.io -n "$ARGO"

echo "-- remedy-write: restart, delete a pod, patch an application, and read nothing"
can yes remedy-write patch deployments.apps -n "$WNS"
can yes remedy-write delete pods -n "$WNS"
can yes remedy-write patch applications.argoproj.io -n "$ARGO"
can no  remedy-write get pods -n "$WNS"
can no  remedy-write get secrets -n "$WNS"
can no  remedy-write delete pods -n kube-system
can no  remedy-write patch deployments.apps -n kube-system
can no  remedy-write patch deployments.apps -n "$NS"
can no  remedy-write delete pods -n "$NS"

echo "-- remedy-token-refresher: mint one account's token, patch one Secret, nothing else"
can yes remedy-token-refresher create serviceaccounts --subresource=token --resource-name=remedy-write -n "$NS"
can yes remedy-token-refresher patch secrets --resource-name=remedy-write-token -n "$NS"
can no  remedy-token-refresher create serviceaccounts --subresource=token --resource-name=remedy-read -n "$NS"
can no  remedy-token-refresher patch secrets --resource-name=remedy-secrets -n "$NS"
can no  remedy-token-refresher get secrets --resource-name=remedy-write-token -n "$NS"
can no  remedy-token-refresher create secrets -n "$NS"
can no  remedy-token-refresher list secrets -n "$NS"

echo "-- remedy-server (no cluster) or remedy-runner: no rights at all"
for who in remedy-runner; do
  can no "$who" list pods --all-namespaces
  can no "$who" get secrets -n "$NS"
done

[ "$fail" -eq 0 ] && echo "all as expected" || { echo "some answers are not as expected" >&2; exit 1; }
```

Run: `chmod +x dev/kind/check-chart-identities.sh && sh -n dev/kind/check-chart-identities.sh && echo syntax ok`
Expected: `syntax ok`.

- [ ] **Step 2: Build the images and a cluster of its own**

```sh
make images
kind create cluster --name remedy-k3
for n in remedy-system demo argocd; do kubectl --context kind-remedy-k3 create namespace $n; done
kind load docker-image remedy-server:dev remedy-runner:dev --name remedy-k3
kubectl --context kind-remedy-k3 -n remedy-system create secret generic remedy-secrets \
  --from-literal=admin-password="$(openssl rand -hex 12)" \
  --from-literal=runner-token="$(openssl rand -hex 24)" \
  --from-literal=master-key="$(openssl rand -base64 32)"
kubectl --context kind-remedy-k3 get endpoints kubernetes -o jsonpath='{.subsets[0].addresses[0].ip}:{.subsets[0].ports[0].port}{"\n"}'
```

Expected: the cluster exists; the last command prints the API server's address, for example `172.18.0.2:6443`. Use that address in the next step.

- [ ] **Step 3: Install the chart with the cluster identities and no runner**

Write the values to the scratchpad directory (not into the repository), replacing `172.18.0.2/32` with the address just printed:

```sh
cat > "$TMPDIR/k3-values.yaml" <<'EOF'
image: {pullPolicy: IfNotPresent, server: {repository: remedy-server, tag: dev}, runner: {repository: remedy-runner, tag: dev}}
existingSecret: {name: remedy-secrets}
runner: {enabled: false}
cluster: {enabled: true, write: {enabled: true, namespaces: [demo]}}
networkPolicy: {enabled: true, apiServer: {cidrs: ["172.18.0.2/32"]}}
EOF
helm --kube-context kind-remedy-k3 install remedy deploy/chart -n remedy-system -f "$TMPDIR/k3-values.yaml" --wait --timeout 5m
```

Expected: `STATUS: deployed`. The `--wait` completes only if the server pod becomes ready (so it started with an empty write token) and the post-install hook Job completes. If the install hangs, run `kubectl --context kind-remedy-k3 -n remedy-system get pods,jobs` and `logs` of the failing one.

- [ ] **Step 4: Check the token, the log, the permissions**

```sh
C="kubectl --context kind-remedy-k3 -n remedy-system"
echo "token length: $($C get secret remedy-write-token -o jsonpath='{.data.token}' | wc -c)"
$C logs deploy/remedy-server | grep -E 'cluster|listening|internal'
$C get cronjob,job
dev/kind/check-chart-identities.sh kind-remedy-k3 remedy-system demo argocd
```

Expected: a token length well above 100 (a Base64 JWT; do not print it); the log has `the cluster answers` with `actions=true` and `internal listener`; no `holds no token yet` warning appeared before it only if the hook ran first, and either order is fine (a warning at the first start is the designed behaviour: note whether it appeared); the CronJob exists; `check-chart-identities.sh` prints `ok` for every line and `all as expected`. The `argocd` namespace has no Argo CD here, so `list applications.argoproj.io` answers by RBAC alone: `kubectl auth can-i` evaluates the rules, not the CRD. If a line says `WRONG`, the role in the chart differs from the test's expectation: fix the chart, not the script.

- [ ] **Step 5: Refresh once by hand, and show that an upgrade leaves the token alone**

```sh
hash() { $C get secret remedy-write-token -o jsonpath='{.data.token}' | shasum -a 256 | cut -c1-12; }
before=$(hash); echo "before: $before"
$C create job manual-refresh --from=cronjob/remedy-token-refresh
$C wait --for=condition=complete job/manual-refresh --timeout=120s
after=$(hash); echo "after the manual job: $after"
helm --kube-context kind-remedy-k3 upgrade remedy deploy/chart -n remedy-system -f "$TMPDIR/k3-values.yaml" --no-hooks --wait
echo "after an upgrade without hooks: $(hash)"
$C logs job/manual-refresh
```

Expected: `before` and `after the manual job` differ (a new token was minted and stored); `after an upgrade without hooks` equals `after the manual job` (Helm did not reset the Secret's data); the job's log has `stored a new token` with `account=remedy-write secret=remedy-write-token` and an expiry time about two hours ahead, and no token text.

- [ ] **Step 6: The refresher cannot do what its role does not allow**

```sh
kubectl --context kind-remedy-k3 -n remedy-system auth can-i create serviceaccounts --subresource=token --resource-name=default --as=system:serviceaccount:remedy-system:remedy-token-refresher
```

Expected: the last command prints `no`. (The script of step 4 already covers the other denials; this one is the case that matters most: the refresher cannot mint a token for any account but `remedy-write`.)

- [ ] **Step 7: Write the record and clean up**

Create `docs/research/k8s-identities-real-run.md` with: the date, the Kubernetes version (`kubectl version` server line) and kind version, the commands of steps 3 to 6 and their outputs (the table of `check-chart-identities.sh` in full; the lengths and the 12-character hashes; the log lines; no token), whether a "holds no token yet" warning showed at the first start, and a "Result" paragraph that says which success criteria of the spec's section 1 this proves (identities work, the hook fills the token, an upgrade keeps it) and what it does not (an action's failing closed live, Argo CD's handling of the Secret).

Then:

```sh
kind delete cluster --name remedy-k3
kind get clusters
rm -f "$TMPDIR/k3-values.yaml"
```

Expected: `remedy-k3` is gone.

- [ ] **Step 8: Update the docs and commit**

In `deploy/chart/README.md`, replace the sentence `The cluster tools (`cluster.*`) and their identities are added by plan K-3.` with:

```markdown
With `cluster.enabled` the control plane runs as the read account (a projected token) and the chart creates a read-only ClusterRole.
With `cluster.write.enabled` a CronJob (and a post-install hook) mints a two-hour token of the write account into the Secret
`remedy-write-token`, which the pod mounts; Roles are created in each of `cluster.write.namespaces` (which must exist) and in
`cluster.argoNamespace`. `cluster.write.namespaces` can never contain the release namespace. If the refresher stops, actions
fail closed after the token expires. Under Argo CD add an `ignoreDifferences` for the Secret's `/data`.
```

In `CLAUDE.md`, in the Kubernetes bullet of "Current state", append: ` The chart's cluster identities (read account with a projected token, a write token refreshed by a CronJob and a post-install hook, exact RBAC, `dev/kind/check-chart-identities.sh`) are in; the record is `docs/research/k8s-identities-real-run.md`.`

```bash
git add dev/kind/check-chart-identities.sh docs/research/k8s-identities-real-run.md deploy/chart/README.md CLAUDE.md
git commit -m "test(chart): the cluster identities on a real cluster, and a script that shows what each may do

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```
