# Phase 2c-1: The Cluster Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The control plane can reach a Kubernetes cluster with two separate identities, a run can be started with "cluster tools", and the gatekeeper offers a tool of the group `cluster` only to such a run. No cluster tool exists yet; this plan lays the ground that the tools of plans 2c-2 and 2c-3 stand on.

**Architecture:** A new package `internal/kube` holds two thin clients on the standard library: a `Reader` whose transport refuses everything but GET and a `Writer` with exactly four methods and a namespace allowlist. Tokens are files that are read at every request. The server configuration (`REMEDY_K8S_*`) builds them; `GET /api/capabilities` tells the UI what is possible; `runs.cluster` (migration 007) marks a run with cluster tools; the gatekeeper's tools get a group, and a run that is not offered a tool cannot call it.

**Tech Stack:** Go 1.27 stdlib only, SQLite, React 19 and the shadcn components that are already installed (no new dependencies).

**Spec:** [`docs/specs/2026-10-04-phase-2c-cluster-design.md`](../specs/2026-10-04-phase-2c-cluster-design.md), sections 3 to 5, 8 and the first step of section 10. The gatekeeper this builds on is [plan 2a](phase-2a-gatekeeper-server.md) and [plan 2b](phase-2b-runner-and-approvals-ui.md), implemented.

**Scope note:** This plan adds no cluster tool and talks to a cluster in one place only: a start-up check that logs whether the read token works (`GET /version`). Until plan 2c-2 the switch "Allow cluster tools" only sets the flag of the run. The `Writer` is built and tested here, against a fake API server, but nothing calls it yet; plan 2c-3 does, and checks its requests against a real Argo CD.

## Decisions made while planning

These refine the spec after reading the code.

| Topic | Spec said | This plan |
|---|---|---|
| Default CA | "the in-cluster file" | `REMEDY_K8S_CA_FILE` if set; otherwise the service account's CA when that file exists; otherwise the system's roots. A laptop has no in-cluster file, and an error would only get in the way. |
| A write token without a read token | not mentioned | An error at start-up. The actions check their target with the read side first, so a write side alone cannot work. |
| Namespaces without a write token | not mentioned | An error at start-up: it is a mistake, and silently ignoring an allowlist hides it. A write token without namespaces stays a warning, as the spec says. |
| Start-up check | not mentioned | `GET /version` with the read token, in the background, logged as info (version) or warning (cannot reach it). It never stops the server: a cluster that is down at start may be up a minute later. |
| Redirects and proxies | not mentioned | The clients follow no redirect and use no proxy: a token must not be sent anywhere the configuration did not name. |
| The Writer's own allowlist | "checked in the tool" | The tools check it before an approval is asked, and the `Writer` checks it again before it sends anything. The second check is a guard, not the first line. |
| `cluster` in `POST /api/runs` | 409 when no cluster is configured | 400 when `tools` is not set (the cluster tools are a part of the gatekeeper's tools), 409 when the read side is not configured. |
| UI | a second switch | Turning the cluster switch on turns the tools switch on; turning the tools switch off turns the cluster switch off. |

## Global Constraints

- Everything committed is English: docs, code, identifiers, comments, UI copy, commit messages.
- No new Go dependencies and no new web dependencies. No `client-go`, no kubeconfig parsing, no `kubectl`.
- A cluster token never appears in a log line, an error message, an API answer or a database column. It is read from its file at every request and wrapped in `secret.Value` while it is held.
- The `Reader` can only read: every exported method is a `Get*` or a `List*`, its transport refuses every method except GET and HEAD, and a test enforces both. The `Writer` has exactly `RestartWorkload`, `DeletePod`, `RefreshApplication` and `SyncApplication`, and its transport refuses every method except PATCH and DELETE.
- A sync never prunes, forces or replaces.
- A run that is not offered a tool gets the answer for a tool that does not exist, and the attempt is still in the audit log.
- `web/tsconfig.app.json` keeps `erasableSyntaxOnly` and `verbatimModuleSyntax`: no enums, no constructor parameter properties, `import type` for types.
- Every UI change is checked in a real browser (Playwright) before it is called done.
- Every commit message ends with the trailer `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`.
- Shell in tests runs on macOS and on Linux: no BSD-only flags.
- `go test ./... -race -count=1` and `make check` must pass at the end of every task.

## How to read the code blocks

A line `Create `path`:` or `Overwrite `path`:` is followed by the complete file. A line `In `path`, replace:` is followed by a block with the exact old text, a line `with:` and a block with the new text; the old text occurs exactly once in the file. Go code uses tabs.

## File Structure

| Path | Responsibility |
|---|---|
| `internal/kube/config.go` | `Config`, what is enabled, validation, `ParseNamespaces` |
| `internal/kube/client.go` | The shared request code: token files, CA, timeouts, size limit, the guard transport, errors |
| `internal/kube/reader.go`, `internal/kube/writer.go` | The read-only client and the four actions |
| `internal/config/config.go` | The `REMEDY_K8S_*` variables |
| `internal/server/capabilities.go`, `internal/server/runs.go` | `GET /api/capabilities`; `cluster` on `POST /api/runs` |
| `internal/app/app.go`, `cmd/remedy-server/main.go` | Builds the clients, the start-up check |
| `internal/store/migrations/007_cluster.sql`, `internal/store/`, `internal/run/run.go` | `runs.cluster` |
| `internal/gatekeeper/` | Tool groups: what is offered and callable |
| `web/src/` | The switch "Allow cluster tools", the badge |

---

### Task 1: The cluster clients

**Files:**
- Create: `internal/kube/config.go`, `internal/kube/client.go`, `internal/kube/reader.go`, `internal/kube/writer.go`, `internal/kube/config_test.go`, `internal/kube/client_test.go`, `internal/kube/writer_test.go`, `internal/kube/audit_test.go`

**Interfaces:**
- Consumes: `secret.Value` and `secret.NewValue` (`internal/secret`).
- Produces:
  - `kube.Config{API, CAFile, ReadTokenFile, WriteTokenFile string; WriteNamespaces []string; ArgoNamespace string}` with `ReadEnabled() bool`, `WriteEnabled() bool` (a write token and at least one namespace), `NamespaceAllowed(string) bool`, `Validate() error` and `Warnings() []string`; `kube.ParseNamespaces(list string) ([]string, error)`; the constants `DefaultAPI`, `DefaultCAFile`, `DefaultArgoNamespace`.
  - `kube.NewReader(Config, ...Option) (*Reader, error)` and `(*Reader).GetVersion(ctx) (Version, error)`; `kube.NewWriter(Config, ...Option) (*Writer, error)` with `RestartWorkload(ctx, kind, namespace, name string, now time.Time)`, `DeletePod(ctx, namespace, name)`, `RefreshApplication(ctx, app string, hard bool)` and `SyncApplication(ctx, app)`; `kube.WithLog(*slog.Logger)`.
  - Errors: `*kube.APIError{Status, Reason, Message}` (wraps `ErrNotFound`, `ErrForbidden`, `ErrUnauthorized` or `ErrConflict` by its status), `ErrTooLarge`, `ErrInvalid` (an argument that cannot go into a path; nothing was sent), `ErrNamespaceNotAllowed`.
  - Plan 2c-2 adds `Get*` and `List*` methods to the `Reader` on top of the unexported `do`; plan 2c-3 builds the mutating tools on the `Writer`.

- [ ] **Step 1: Write the failing tests**

The tests live in the package itself (`package kube`), so that they can reach the transport and the timeout.

Create `internal/kube/config_test.go`:

```go
package kube

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseNamespaces(t *testing.T) {
	got, err := ParseNamespaces(" demo, other ,,demo,kube-test ")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"demo", "other", "kube-test"}; !slices.Equal(got, want) {
		t.Fatalf("namespaces = %v, want %v", got, want)
	}
	if got, err := ParseNamespaces(""); err != nil || len(got) != 0 {
		t.Fatalf("empty list = %v, %v", got, err)
	}
	for _, bad := range []string{"Demo", "demo*", "-demo", "de mo", "demo/other", strings.Repeat("a", 64)} {
		if _, err := ParseNamespaces(bad); err == nil {
			t.Errorf("ParseNamespaces(%q) accepted a bad namespace", bad)
		}
	}
}

func TestWhatIsEnabled(t *testing.T) {
	cases := []struct {
		name        string
		c           Config
		read, write bool
	}{
		{"nothing", Config{}, false, false},
		{"read only", Config{ReadTokenFile: "r"}, true, false},
		{"write token without namespaces", Config{ReadTokenFile: "r", WriteTokenFile: "w"}, true, false},
		{"namespaces without a write token", Config{ReadTokenFile: "r", WriteNamespaces: []string{"demo"}}, true, false},
		{"all", Config{ReadTokenFile: "r", WriteTokenFile: "w", WriteNamespaces: []string{"demo"}}, true, true},
	}
	for _, tc := range cases {
		if tc.c.ReadEnabled() != tc.read || tc.c.WriteEnabled() != tc.write {
			t.Errorf("%s: read=%v write=%v, want %v %v", tc.name, tc.c.ReadEnabled(), tc.c.WriteEnabled(), tc.read, tc.write)
		}
	}
	c := Config{WriteNamespaces: []string{"demo"}}
	if !c.NamespaceAllowed("demo") || c.NamespaceAllowed("other") || c.NamespaceAllowed("") {
		t.Fatal("NamespaceAllowed is not exactly the list")
	}
}

func TestValidate(t *testing.T) {
	read := writeFile(t, "read", "read-token\n")
	write := writeFile(t, "write", "write-token\n")
	empty := writeFile(t, "empty", "  \n")
	notPEM := writeFile(t, "ca.crt", "this is not a certificate")

	ok := Config{API: "https://k8s.example:6443", ReadTokenFile: read, WriteTokenFile: write, WriteNamespaces: []string{"demo"}, ArgoNamespace: "argocd"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("a good configuration is refused: %v", err)
	}
	if err := (Config{}).Validate(); err != nil {
		t.Fatalf("no cluster at all must be fine: %v", err)
	}

	bad := map[string]Config{
		"a write token without a read token": {API: ok.API, WriteTokenFile: write, WriteNamespaces: []string{"demo"}},
		"namespaces without a write token":   {API: ok.API, ReadTokenFile: read, WriteNamespaces: []string{"demo"}},
		"an API that is not a URL":           {API: "k8s.example", ReadTokenFile: read},
		"an API with another scheme":         {API: "ftp://k8s.example", ReadTokenFile: read},
		"a token file that is missing":       {API: ok.API, ReadTokenFile: filepath.Join(t.TempDir(), "nope")},
		"a token file without a token":       {API: ok.API, ReadTokenFile: empty},
		"a CA file that is not PEM":          {API: ok.API, ReadTokenFile: read, CAFile: notPEM},
		"a CA file that is missing":          {API: ok.API, ReadTokenFile: read, CAFile: filepath.Join(t.TempDir(), "nope")},
		"a bad namespace in the allowlist":   {API: ok.API, ReadTokenFile: read, WriteTokenFile: write, WriteNamespaces: []string{"De mo"}},
		"a bad Argo CD namespace":            {API: ok.API, ReadTokenFile: read, ArgoNamespace: "Argo CD"},
	}
	for name, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestValidateNeverShowsATokenInItsError(t *testing.T) {
	// A token file whose content is not a token problem: the error names the file, never the content.
	path := writeFile(t, "tok", "s3cr3t-token-value")
	err := Config{API: "ftp://x", ReadTokenFile: path}.Validate()
	if err == nil || strings.Contains(err.Error(), "s3cr3t-token-value") {
		t.Fatalf("error = %v", err)
	}
}

func TestWarnings(t *testing.T) {
	read := writeFile(t, "read", "r")
	write := writeFile(t, "write", "w")
	if w := (Config{ReadTokenFile: read, WriteTokenFile: write}).Warnings(); len(w) != 1 || !strings.Contains(w[0], "REMEDY_K8S_WRITE_NAMESPACES") {
		t.Fatalf("warnings = %v", w)
	}
	if w := (Config{ReadTokenFile: read, WriteTokenFile: write, WriteNamespaces: []string{"demo"}}).Warnings(); len(w) != 0 {
		t.Fatalf("warnings = %v", w)
	}
	if w := (Config{ReadTokenFile: read}).Warnings(); len(w) != 0 {
		t.Fatalf("warnings = %v", w)
	}
}
```

Create `internal/kube/client_test.go`:

```go
package kube

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

const versionJSON = `{"major":"1","minor":"31","gitVersion":"v1.31.0","platform":"linux/amd64"}`

// seen is what a fake API server saw of a request.
type seen struct {
	Method, Path, Query, Auth, Accept, ContentType, Body string
}

// fakeAPI serves handler and records every request.
type fakeAPI struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []seen
}

func newFakeAPI(t *testing.T, handler http.HandlerFunc) *fakeAPI {
	t.Helper()
	f := &fakeAPI{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := new(strings.Builder)
		buf := make([]byte, 4096)
		for {
			n, err := r.Body.Read(buf)
			body.Write(buf[:n])
			if err != nil {
				break
			}
		}
		f.mu.Lock()
		f.reqs = append(f.reqs, seen{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"),
			r.Header.Get("Accept"), r.Header.Get("Content-Type"), body.String()})
		f.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeAPI) requests() []seen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]seen(nil), f.reqs...)
}

func jsonReply(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func newTestReader(t *testing.T, api *fakeAPI, token string) *Reader {
	t.Helper()
	r, err := NewReader(Config{API: api.URL, ReadTokenFile: writeFile(t, "read", token)})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestGetVersionSendsTheTokenAsABearerOnAGetRequest(t *testing.T) {
	api := newFakeAPI(t, jsonReply(200, versionJSON))
	v, err := newTestReader(t, api, "the-read-token\n").GetVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v.GitVersion != "v1.31.0" || v.Platform != "linux/amd64" {
		t.Fatalf("version = %+v", v)
	}
	got := api.requests()
	if len(got) != 1 || got[0].Method != "GET" || got[0].Path != "/version" ||
		got[0].Auth != "Bearer the-read-token" || got[0].Accept != "application/json" {
		t.Fatalf("requests = %+v", got)
	}
}

func TestTheTokenFileIsReadAtEveryRequest(t *testing.T) {
	api := newFakeAPI(t, jsonReply(200, versionJSON))
	path := writeFile(t, "read", "first")
	r, err := NewReader(Config{API: api.URL, ReadTokenFile: path})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.GetVersion(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.GetVersion(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := api.requests()
	if len(got) != 2 || got[0].Auth != "Bearer first" || got[1].Auth != "Bearer second" {
		t.Fatalf("a rotated token is not used: %+v", got)
	}
}

func TestAnEmptyTokenFileIsAnErrorAndNothingIsSent(t *testing.T) {
	api := newFakeAPI(t, jsonReply(200, versionJSON))
	path := writeFile(t, "read", "token")
	r, err := NewReader(Config{API: api.URL, ReadTokenFile: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.GetVersion(context.Background()); err == nil {
		t.Fatal("a request without a token was sent")
	}
	if n := len(api.requests()); n != 0 {
		t.Fatalf("%d requests reached the server", n)
	}
}

func TestStatusesBecomeErrorsThatCanBeTestedFor(t *testing.T) {
	cases := []struct {
		status int
		body   string
		is     error
		text   string
	}{
		{404, `{"kind":"Status","reason":"NotFound","message":"deployments.apps \"web\" not found","code":404}`, ErrNotFound, "not found"},
		{403, `{"kind":"Status","reason":"Forbidden","message":"pods is forbidden: User cannot list","code":403}`, ErrForbidden, "forbidden"},
		{401, `{"kind":"Status","reason":"Unauthorized","message":"Unauthorized","code":401}`, ErrUnauthorized, "Unauthorized"},
		{409, `{"kind":"Status","reason":"Conflict","message":"the object has been modified","code":409}`, ErrConflict, "modified"},
		{500, `<html>internal error</html>`, nil, "500"},
	}
	for _, tc := range cases {
		api := newFakeAPI(t, jsonReply(tc.status, tc.body))
		_, err := newTestReader(t, api, "tok").GetVersion(context.Background())
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Status != tc.status {
			t.Errorf("%d: error = %v", tc.status, err)
			continue
		}
		if tc.is != nil && !errors.Is(err, tc.is) {
			t.Errorf("%d: error %v is not %v", tc.status, err, tc.is)
		}
		if !strings.Contains(err.Error(), tc.text) {
			t.Errorf("%d: error %q does not mention %q", tc.status, err, tc.text)
		}
	}
}

func TestAnErrorNeverContainsTheToken(t *testing.T) {
	const token = "very-secret-bearer-value"
	api := newFakeAPI(t, jsonReply(403, `{"kind":"Status","message":"the token very-secret-bearer-value may not do this","code":403}`))
	_, err := newTestReader(t, api, token).GetVersion(context.Background())
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("error = %v", err)
	}
}

func TestARedirectIsNotFollowed(t *testing.T) {
	other := newFakeAPI(t, jsonReply(200, versionJSON))
	api := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/version", http.StatusFound)
	})
	_, err := newTestReader(t, api, "tok").GetVersion(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusFound {
		t.Fatalf("error = %v", err)
	}
	if n := len(other.requests()); n != 0 {
		t.Fatalf("the redirect was followed (%d requests, the token went along)", n)
	}
}

func TestAnAnswerThatIsTooLargeIsAnError(t *testing.T) {
	api := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"gitVersion":"`))
		_, _ = w.Write([]byte(strings.Repeat("x", maxBody+10)))
		_, _ = w.Write([]byte(`"}`))
	})
	if _, err := newTestReader(t, api, "tok").GetVersion(context.Background()); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("error = %v", err)
	}
}

func TestARequestHasATimeout(t *testing.T) {
	old := requestTimeout
	requestTimeout = 100 * time.Millisecond
	t.Cleanup(func() { requestTimeout = old })
	api := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	started := time.Now()
	if _, err := newTestReader(t, api, "tok").GetVersion(context.Background()); err == nil {
		t.Fatal("no error")
	}
	if time.Since(started) > 5*time.Second {
		t.Fatal("the request did not time out")
	}
}

func TestACancelledContextStopsTheRequest(t *testing.T) {
	api := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	if _, err := newTestReader(t, api, "tok").GetVersion(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestTLSUsesTheConfiguredCA(t *testing.T) {
	srv := httptest.NewTLSServer(jsonReply(200, versionJSON))
	t.Cleanup(srv.Close)
	ca := writeFile(t, "ca.crt", string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})))
	token := writeFile(t, "read", "tok")

	trusted, err := NewReader(Config{API: srv.URL, ReadTokenFile: token, CAFile: ca})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := trusted.GetVersion(context.Background()); err != nil {
		t.Fatalf("with the CA: %v", err)
	}
	untrusted, err := NewReader(Config{API: srv.URL, ReadTokenFile: token})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := untrusted.GetVersion(context.Background()); err == nil {
		t.Fatal("a certificate that no configured CA signed was accepted")
	}
}

func TestTheReaderRefusesEveryMethodButGet(t *testing.T) {
	api := newFakeAPI(t, jsonReply(200, versionJSON))
	r := newTestReader(t, api, "tok")
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		req, _ := http.NewRequest(method, api.URL+"/api/v1/namespaces/demo/pods/x", nil)
		if _, err := r.http.Do(req); err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Errorf("%s: error = %v", method, err)
		}
	}
	if n := len(api.requests()); n != 0 {
		t.Fatalf("%d refused requests reached the server", n)
	}
}
```

Create `internal/kube/writer_test.go`:

```go
package kube

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func newTestWriter(t *testing.T, api *fakeAPI) *Writer {
	t.Helper()
	w, err := NewWriter(Config{
		API: api.URL, ReadTokenFile: writeFile(t, "read", "read-token"),
		WriteTokenFile: writeFile(t, "write", "write-token\n"), WriteNamespaces: []string{"demo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func okReply() http.HandlerFunc { return jsonReply(200, `{}`) }

func TestRestartWorkloadSetsTheRestartedAtAnnotation(t *testing.T) {
	api := newFakeAPI(t, okReply())
	now := time.Date(2026, 10, 4, 12, 30, 0, 0, time.UTC)
	for kind, plural := range map[string]string{"deployment": "deployments", "statefulset": "statefulsets", "daemonset": "daemonsets"} {
		if err := newTestWriter(t, api).RestartWorkload(context.Background(), kind, "demo", "web", now); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		got := api.requests()
		last := got[len(got)-1]
		if last.Method != "PATCH" || last.Path != "/apis/apps/v1/namespaces/demo/"+plural+"/web" ||
			last.ContentType != "application/merge-patch+json" || last.Auth != "Bearer write-token" {
			t.Fatalf("%s: request = %+v", kind, last)
		}
		var body struct {
			Spec struct {
				Template struct {
					Metadata struct {
						Annotations map[string]string `json:"annotations"`
					} `json:"metadata"`
				} `json:"template"`
			} `json:"spec"`
		}
		if err := json.Unmarshal([]byte(last.Body), &body); err != nil {
			t.Fatal(err)
		}
		if got := body.Spec.Template.Metadata.Annotations["kubectl.kubernetes.io/restartedAt"]; got != "2026-10-04T12:30:00Z" {
			t.Fatalf("%s: restartedAt = %q", kind, got)
		}
	}
}

func TestDeletePod(t *testing.T) {
	api := newFakeAPI(t, okReply())
	if err := newTestWriter(t, api).DeletePod(context.Background(), "demo", "crashy-7d9f-abcde"); err != nil {
		t.Fatal(err)
	}
	got := api.requests()
	if len(got) != 1 || got[0].Method != "DELETE" || got[0].Path != "/api/v1/namespaces/demo/pods/crashy-7d9f-abcde" ||
		got[0].Auth != "Bearer write-token" {
		t.Fatalf("requests = %+v", got)
	}
}

func TestRefreshAndSyncAnApplication(t *testing.T) {
	api := newFakeAPI(t, okReply())
	w := newTestWriter(t, api)
	if err := w.RefreshApplication(context.Background(), "guestbook", false); err != nil {
		t.Fatal(err)
	}
	if err := w.RefreshApplication(context.Background(), "guestbook", true); err != nil {
		t.Fatal(err)
	}
	if err := w.SyncApplication(context.Background(), "guestbook"); err != nil {
		t.Fatal(err)
	}
	got := api.requests()
	if len(got) != 3 {
		t.Fatalf("requests = %+v", got)
	}
	for _, r := range got {
		if r.Method != "PATCH" || r.Path != "/apis/argoproj.io/v1alpha1/namespaces/argocd/applications/guestbook" ||
			r.ContentType != "application/merge-patch+json" {
			t.Fatalf("request = %+v", r)
		}
	}
	if got[0].Body != `{"metadata":{"annotations":{"argocd.argoproj.io/refresh":"normal"}}}` ||
		got[1].Body != `{"metadata":{"annotations":{"argocd.argoproj.io/refresh":"hard"}}}` {
		t.Fatalf("refresh bodies = %s, %s", got[0].Body, got[1].Body)
	}
	if got[2].Body != `{"operation":{"initiatedBy":{"username":"remedy"},"sync":{}}}` {
		t.Fatalf("sync body = %s: a sync must not prune, force or replace", got[2].Body)
	}
}

func TestTheArgoNamespaceIsConfigurable(t *testing.T) {
	api := newFakeAPI(t, okReply())
	w, err := NewWriter(Config{API: api.URL, ReadTokenFile: writeFile(t, "r", "r"), WriteTokenFile: writeFile(t, "w", "w"),
		WriteNamespaces: []string{"demo"}, ArgoNamespace: "gitops"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.SyncApplication(context.Background(), "guestbook"); err != nil {
		t.Fatal(err)
	}
	if got := api.requests()[0].Path; got != "/apis/argoproj.io/v1alpha1/namespaces/gitops/applications/guestbook" {
		t.Fatalf("path = %s", got)
	}
}

func TestTheWriterRefusesANamespaceOutsideTheAllowlistWithoutSendingAnything(t *testing.T) {
	api := newFakeAPI(t, okReply())
	w := newTestWriter(t, api)
	ctx := context.Background()
	if err := w.RestartWorkload(ctx, "deployment", "other", "web", time.Now()); !errors.Is(err, ErrNamespaceNotAllowed) {
		t.Fatalf("restart: %v", err)
	}
	if err := w.DeletePod(ctx, "kube-system", "coredns-1"); !errors.Is(err, ErrNamespaceNotAllowed) {
		t.Fatalf("delete: %v", err)
	}
	if n := len(api.requests()); n != 0 {
		t.Fatalf("%d requests were sent", n)
	}
}

func TestTheWriterRefusesNamesThatCouldChangeThePath(t *testing.T) {
	api := newFakeAPI(t, okReply())
	w := newTestWriter(t, api)
	ctx := context.Background()
	for _, name := range []string{"", "..", "../secrets/x", "web/../../x", "Web", "web?x=1", "web%2Fx", strings.Repeat("a", 254)} {
		if err := w.DeletePod(ctx, "demo", name); !errors.Is(err, ErrInvalid) {
			t.Errorf("DeletePod(%q) = %v", name, err)
		}
		if err := w.RestartWorkload(ctx, "deployment", "demo", name, time.Now()); !errors.Is(err, ErrInvalid) {
			t.Errorf("RestartWorkload(%q) = %v", name, err)
		}
		if err := w.SyncApplication(ctx, name); !errors.Is(err, ErrInvalid) {
			t.Errorf("SyncApplication(%q) = %v", name, err)
		}
	}
	if err := w.RestartWorkload(ctx, "secret", "demo", "web", time.Now()); !errors.Is(err, ErrInvalid) {
		t.Errorf("a kind that is not a workload: %v", err)
	}
	if n := len(api.requests()); n != 0 {
		t.Fatalf("%d requests were sent", n)
	}
}

func TestTheWriterRefusesEveryMethodButPatchAndDelete(t *testing.T) {
	api := newFakeAPI(t, okReply())
	w := newTestWriter(t, api)
	for _, method := range []string{"GET", "HEAD", "POST", "PUT"} {
		req, _ := http.NewRequest(method, api.URL+"/api/v1/namespaces/demo/pods", nil)
		if _, err := w.http.Do(req); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("%s: error = %v", method, err)
		}
	}
	if n := len(api.requests()); n != 0 {
		t.Fatalf("%d refused requests reached the server", n)
	}
}

func TestTheWriterReportsWhatTheClusterSays(t *testing.T) {
	api := newFakeAPI(t, jsonReply(404, `{"kind":"Status","reason":"NotFound","message":"pods \"x\" not found","code":404}`))
	if err := newTestWriter(t, api).DeletePod(context.Background(), "demo", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("404: %v", err)
	}
	api = newFakeAPI(t, jsonReply(403, `{"kind":"Status","reason":"Forbidden","message":"forbidden","code":403}`))
	if err := newTestWriter(t, api).DeletePod(context.Background(), "demo", "x"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("403: %v", err)
	}
}

func TestANewWriterNeedsTheWriteSideToBeConfigured(t *testing.T) {
	read := writeFile(t, "r", "r")
	if _, err := NewWriter(Config{API: "http://k8s", ReadTokenFile: read}); err == nil {
		t.Fatal("a writer without a write token")
	}
	if _, err := NewWriter(Config{API: "http://k8s", ReadTokenFile: read, WriteTokenFile: writeFile(t, "w", "w")}); err == nil {
		t.Fatal("a writer without namespaces")
	}
	if _, err := NewReader(Config{API: "http://k8s"}); err == nil {
		t.Fatal("a reader without a read token")
	}
}
```

Create `internal/kube/audit_test.go`:

```go
package kube

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// The read side can only ever read: every exported method of the Reader is a Get or a List. This is the same
// rule as for the GitHub client; the transport is the second line of defence.
func TestEveryExportedMethodOfTheReaderIsAGetOrAList(t *testing.T) {
	typ := reflect.TypeOf(&Reader{})
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		if !strings.HasPrefix(name, "Get") && !strings.HasPrefix(name, "List") {
			t.Errorf("Reader.%s is neither a Get nor a List: the read side must not change anything", name)
		}
	}
}

// The write side can do four things and nothing else. Adding a fifth is a decision that belongs into the spec.
func TestTheWriterHasExactlyTheFourActionsOfTheSpec(t *testing.T) {
	typ := reflect.TypeOf(&Writer{})
	var got []string
	for i := 0; i < typ.NumMethod(); i++ {
		got = append(got, typ.Method(i).Name)
	}
	slices.Sort(got)
	want := []string{"DeletePod", "RefreshApplication", "RestartWorkload", "SyncApplication"}
	if !slices.Equal(got, want) {
		t.Fatalf("Writer methods = %v, want %v", got, want)
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/kube 2>&1 | head -12`
Expected: the package does not compile (`undefined: Reader`, `undefined: NewReader`, `undefined: Config`, `undefined: Writer`).

- [ ] **Step 3: The configuration, the shared client, the Reader and the Writer**

Create `internal/kube/config.go`:

```go
// Package kube is the control plane's access to the Kubernetes API. It has two clients with different credentials:
// a Reader that can only read, and a Writer that can do four things, in the namespaces of an allowlist. Both are
// thin clients on the standard library: a token read from a file at every request, an optional CA, a bounded
// answer, and a transport that refuses what the client is not meant to send.
package kube

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
)

const (
	// DefaultAPI is the API server as seen from inside a cluster.
	DefaultAPI = "https://kubernetes.default.svc"
	// DefaultCAFile is the CA of a pod's service account. It is used when no CA file is set and the file exists;
	// otherwise the system's roots apply.
	DefaultCAFile = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	// DefaultArgoNamespace is where Argo CD keeps its Application objects.
	DefaultArgoNamespace = "argocd"
)

// Config is how the control plane reaches the cluster. The zero value means no cluster.
type Config struct {
	API             string   // REMEDY_K8S_API
	CAFile          string   // REMEDY_K8S_CA_FILE
	ReadTokenFile   string   // REMEDY_K8S_READ_TOKEN_FILE: turns the read side on
	WriteTokenFile  string   // REMEDY_K8S_WRITE_TOKEN_FILE
	WriteNamespaces []string // REMEDY_K8S_WRITE_NAMESPACES: where actions may be used
	ArgoNamespace   string   // REMEDY_K8S_ARGO_NAMESPACE
}

// ReadEnabled says whether the read side is configured.
func (c Config) ReadEnabled() bool { return c.ReadTokenFile != "" }

// WriteEnabled says whether actions are possible: a write token and at least one namespace to use them in.
func (c Config) WriteEnabled() bool { return c.WriteTokenFile != "" && len(c.WriteNamespaces) > 0 }

// NamespaceAllowed says whether an action may be used in the namespace.
func (c Config) NamespaceAllowed(namespace string) bool {
	return namespace != "" && slices.Contains(c.WriteNamespaces, namespace)
}

func (c Config) api() string {
	if c.API == "" {
		return DefaultAPI
	}
	return strings.TrimRight(c.API, "/")
}

func (c Config) argoNamespace() string {
	if c.ArgoNamespace == "" {
		return DefaultArgoNamespace
	}
	return c.ArgoNamespace
}

var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// ParseNamespaces reads a comma separated list of namespaces. It drops empty entries and duplicates and refuses
// anything that is not a namespace name: a wildcard or a path would widen what the list is meant to say.
func ParseNamespaces(list string) ([]string, error) {
	var out []string
	for _, n := range strings.Split(list, ",") {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if !dnsLabel.MatchString(n) {
			return nil, fmt.Errorf("%q is not a namespace name (lower case letters, digits and dashes, at most 63 characters)", n)
		}
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out, nil
}

// Validate checks a configuration that is meant to be used: the files can be read, the CA parses, the namespaces
// are names. It does not talk to the cluster. A configuration without any cluster setting is fine.
func (c Config) Validate() error {
	if !c.ReadEnabled() && c.WriteTokenFile == "" && len(c.WriteNamespaces) == 0 {
		return nil
	}
	if c.WriteTokenFile != "" && !c.ReadEnabled() {
		return errors.New("REMEDY_K8S_WRITE_TOKEN_FILE needs REMEDY_K8S_READ_TOKEN_FILE: the actions check their target with the read side")
	}
	if len(c.WriteNamespaces) > 0 && c.WriteTokenFile == "" {
		return errors.New("REMEDY_K8S_WRITE_NAMESPACES needs REMEDY_K8S_WRITE_TOKEN_FILE")
	}
	u, err := url.Parse(c.api())
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("REMEDY_K8S_API must be an http or https URL")
	}
	if c.ReadEnabled() {
		if _, err := readToken(c.ReadTokenFile); err != nil {
			return fmt.Errorf("REMEDY_K8S_READ_TOKEN_FILE: %w", err)
		}
	}
	if c.WriteTokenFile != "" {
		if _, err := readToken(c.WriteTokenFile); err != nil {
			return fmt.Errorf("REMEDY_K8S_WRITE_TOKEN_FILE: %w", err)
		}
	}
	if _, err := loadPool(c.CAFile); err != nil {
		return fmt.Errorf("REMEDY_K8S_CA_FILE: %w", err)
	}
	for _, n := range c.WriteNamespaces {
		if !dnsLabel.MatchString(n) {
			return fmt.Errorf("REMEDY_K8S_WRITE_NAMESPACES: %q is not a namespace name", n)
		}
	}
	if c.ArgoNamespace != "" && !dnsLabel.MatchString(c.ArgoNamespace) {
		return fmt.Errorf("REMEDY_K8S_ARGO_NAMESPACE: %q is not a namespace name", c.ArgoNamespace)
	}
	return nil
}

// Warnings lists what is configured but cannot be used, for a start-up log. It assumes a valid configuration.
func (c Config) Warnings() []string {
	var w []string
	if c.WriteTokenFile != "" && len(c.WriteNamespaces) == 0 {
		w = append(w, "REMEDY_K8S_WRITE_TOKEN_FILE is set but REMEDY_K8S_WRITE_NAMESPACES is empty: no cluster action can be used")
	}
	return w
}

// loadPool reads the CA the cluster's certificate is checked against. With no path it uses the service account's CA
// when that file exists and otherwise returns nil, which means the system's roots.
func loadPool(path string) (*x509.CertPool, error) {
	if path == "" {
		if _, err := os.Stat(DefaultCAFile); err != nil {
			return nil, nil
		}
		path = DefaultCAFile
	}
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("%s holds no PEM certificate", path)
	}
	return pool, nil
}
```

Create `internal/kube/client.go`:

```go
package kube

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/secret"
)

// maxBody bounds an answer. The tools cut what they show far below it.
const maxBody = 8 << 20

// maxMessage bounds the cluster's own error message that goes into an error.
const maxMessage = 300

// requestTimeout is the time one request may take. It is a variable only so that a test does not wait for it.
var requestTimeout = 15 * time.Second

var (
	// ErrNotFound, ErrForbidden, ErrUnauthorized and ErrConflict are the answers 404, 403, 401 and 409. An *APIError
	// of that status wraps the matching one.
	ErrNotFound     = errors.New("kube: not found")
	ErrForbidden    = errors.New("kube: forbidden")
	ErrUnauthorized = errors.New("kube: unauthorized")
	ErrConflict     = errors.New("kube: conflict")
	// ErrTooLarge means the answer is bigger than the client reads.
	ErrTooLarge = errors.New("kube: the answer is too large")
	// ErrInvalid means an argument cannot be used in a request path; nothing was sent.
	ErrInvalid = errors.New("kube: invalid argument")
	// ErrNamespaceNotAllowed means the namespace is not in the allowlist; nothing was sent.
	ErrNamespaceNotAllowed = errors.New("kube: the namespace is not in the allowlist")
)

// APIError is a non-success answer of the API server.
type APIError struct {
	Status  int
	Reason  string
	Message string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("kube: HTTP %d", e.Status)
	}
	return fmt.Sprintf("kube: HTTP %d: %s", e.Status, e.Message)
}

func (e *APIError) Unwrap() error {
	switch e.Status {
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusForbidden:
		return ErrForbidden
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusConflict:
		return ErrConflict
	}
	return nil
}

// requestError is a failure to talk to the API server, with the token taken out of its text.
type requestError struct {
	msg string
	err error
}

func (e *requestError) Error() string { return e.msg }
func (e *requestError) Unwrap() error { return e.err }

// Option changes how a client is built.
type Option func(*options)

type options struct{ log *slog.Logger }

// WithLog makes a client log every request at debug level: method, path, status and duration. Never a query, a
// header or a body.
func WithLog(log *slog.Logger) Option { return func(o *options) { o.log = log } }

// guard is the transport of a client. It is the second line of defence behind the methods of the client: whatever
// calls it, only the methods the client is meant to send leave the process.
type guard struct {
	base  http.RoundTripper
	allow func(method string) bool
	what  string
}

func (g *guard) RoundTrip(req *http.Request) (*http.Response, error) {
	if !g.allow(req.Method) {
		return nil, fmt.Errorf("kube: refusing to send %s: %s", req.Method, g.what)
	}
	return g.base.RoundTrip(req)
}

type client struct {
	base      string
	tokenFile string
	http      *http.Client
	log       *slog.Logger
}

func newClient(c Config, tokenFile string, allow func(string) bool, what string, opts []Option) (*client, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	pool, err := loadPool(c.CAFile)
	if err != nil {
		return nil, err
	}
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil // cluster traffic goes straight to the API server, whatever the environment says
	tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	return &client{
		base:      c.api(),
		tokenFile: tokenFile,
		log:       o.log,
		http: &http.Client{
			Timeout:   requestTimeout,
			Transport: &guard{base: tr, allow: allow, what: what},
			// An answer that redirects is an answer, not an instruction to send the token elsewhere.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

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
	case strings.ContainsAny(tok, "\r\n"):
		return secret.Value{}, fmt.Errorf("the token file %s holds more than one line", path)
	}
	return secret.NewValue(tok), nil
}

func scrub(s string, token secret.Value) string {
	if t := token.Reveal(); t != "" {
		s = strings.ReplaceAll(s, t, "***")
	}
	return s
}

// do sends one request and returns the body of a 2xx answer.
func (c *client) do(ctx context.Context, method, path string, query url.Values, contentType string, body []byte) ([]byte, error) {
	token, err := readToken(c.tokenFile)
	if err != nil {
		return nil, err
	}
	target := c.base + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var payload io.Reader = http.NoBody
	if body != nil {
		payload = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, payload)
	if err != nil {
		return nil, &requestError{msg: scrub(err.Error(), token), err: err}
	}
	req.Header.Set("Authorization", "Bearer "+token.Reveal())
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	started := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		c.logRequest(method, path, 0, started)
		return nil, &requestError{msg: scrub(err.Error(), token), err: err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	c.logRequest(method, path, resp.StatusCode, started)
	if err != nil {
		return nil, &requestError{msg: scrub(err.Error(), token), err: err}
	}
	if len(raw) > maxBody {
		return nil, ErrTooLarge
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, statusError(resp.StatusCode, raw, token)
	}
	return raw, nil
}

func (c *client) logRequest(method, path string, status int, started time.Time) {
	if c.log == nil {
		return
	}
	c.log.Debug("kubernetes request", "method", method, "path", path, "status", status, "took", time.Since(started).Round(time.Millisecond))
}

// statusError turns a non-success answer into an error. The API server explains itself in a Status object; any
// other body is not shown.
func statusError(status int, body []byte, token secret.Value) error {
	e := &APIError{Status: status}
	var st struct {
		Reason  string `json:"reason"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &st) == nil {
		e.Reason = st.Reason
		e.Message = scrub(st.Message, token)
		if len(e.Message) > maxMessage {
			e.Message = strings.ToValidUTF8(e.Message[:maxMessage], "") + "..."
		}
	}
	return e
}
```

Create `internal/kube/reader.go`:

```go
package kube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// Reader is the read-only client. Every exported method is a Get or a List, enforced by a test, and its transport
// refuses everything but GET and HEAD. It uses the read identity's token.
type Reader struct{ *client }

// NewReader builds a Reader for a configuration whose read side is on.
func NewReader(c Config, opts ...Option) (*Reader, error) {
	if !c.ReadEnabled() {
		return nil, errors.New("kube: the read side is not configured (REMEDY_K8S_READ_TOKEN_FILE)")
	}
	cl, err := newClient(c, c.ReadTokenFile, func(m string) bool { return m == http.MethodGet || m == http.MethodHead },
		"this client is read-only", opts)
	if err != nil {
		return nil, err
	}
	return &Reader{cl}, nil
}

// Version is what the API server says about itself.
type Version struct {
	Major      string `json:"major"`
	Minor      string `json:"minor"`
	GitVersion string `json:"gitVersion"`
	Platform   string `json:"platform"`
}

// GetVersion asks the API server for its version. Remedy uses it at start-up to say whether the cluster can be
// reached with the read token.
func (r *Reader) GetVersion(ctx context.Context) (Version, error) {
	body, err := r.do(ctx, http.MethodGet, "/version", nil, "", nil)
	if err != nil {
		return Version{}, err
	}
	var v Version
	if err := json.Unmarshal(body, &v); err != nil {
		return Version{}, fmt.Errorf("kube: unexpected answer to /version: %w", err)
	}
	return v, nil
}
```

Create `internal/kube/writer.go`:

```go
package kube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"time"
)

const mergePatch = "application/merge-patch+json"

// Writer is the client for the actions the maintainer approved. It has exactly four methods, enforced by a test, and
// its transport refuses everything but PATCH and DELETE. It uses the write identity's token and works only in the
// namespaces of the allowlist; the Argo CD methods work in the Argo CD namespace.
type Writer struct {
	*client
	namespaces []string
	argo       string
}

// NewWriter builds a Writer for a configuration whose write side is on: a write token and at least one namespace.
func NewWriter(c Config, opts ...Option) (*Writer, error) {
	if !c.WriteEnabled() {
		return nil, errors.New("kube: the write side is not configured (REMEDY_K8S_WRITE_TOKEN_FILE and REMEDY_K8S_WRITE_NAMESPACES)")
	}
	cl, err := newClient(c, c.WriteTokenFile, func(m string) bool { return m == http.MethodPatch || m == http.MethodDelete },
		"this client only patches and deletes", opts)
	if err != nil {
		return nil, err
	}
	return &Writer{client: cl, namespaces: slices.Clone(c.WriteNamespaces), argo: c.argoNamespace()}, nil
}

// A name that goes into a request path: a DNS subdomain. Nothing in it can add a path segment or a query.
var objectName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)

func validName(name string) error {
	if len(name) > 253 || !objectName.MatchString(name) {
		return fmt.Errorf("%w: %q is not an object name", ErrInvalid, name)
	}
	return nil
}

// target checks the namespace against the allowlist and the name against the name rules, before anything is sent.
func (w *Writer) target(namespace, name string) error {
	if !slices.Contains(w.namespaces, namespace) {
		return fmt.Errorf("%w: %q", ErrNamespaceNotAllowed, namespace)
	}
	return validName(name)
}

var workloadPlurals = map[string]string{"deployment": "deployments", "statefulset": "statefulsets", "daemonset": "daemonsets"}

func (w *Writer) patch(ctx context.Context, path string, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	_, err = w.do(ctx, http.MethodPatch, path, nil, mergePatch, raw)
	return err
}

// RestartWorkload restarts a Deployment, StatefulSet or DaemonSet the way `kubectl rollout restart` does: it sets
// the restartedAt annotation of the pod template, which makes the controller roll the pods.
func (w *Writer) RestartWorkload(ctx context.Context, kind, namespace, name string, now time.Time) error {
	plural, ok := workloadPlurals[kind]
	if !ok {
		return fmt.Errorf("%w: %q is not deployment, statefulset or daemonset", ErrInvalid, kind)
	}
	if err := w.target(namespace, name); err != nil {
		return err
	}
	return w.patch(ctx, "/apis/apps/v1/namespaces/"+namespace+"/"+plural+"/"+name, map[string]any{
		"spec": map[string]any{"template": map[string]any{"metadata": map[string]any{"annotations": map[string]string{
			"kubectl.kubernetes.io/restartedAt": now.UTC().Format(time.RFC3339),
		}}}},
	})
}

// DeletePod deletes one pod, with the pod's own grace period. The controller that owns it makes a new one.
func (w *Writer) DeletePod(ctx context.Context, namespace, name string) error {
	if err := w.target(namespace, name); err != nil {
		return err
	}
	_, err := w.do(ctx, http.MethodDelete, "/api/v1/namespaces/"+namespace+"/pods/"+name, nil, "", nil)
	return err
}

func (w *Writer) applicationPath(app string) (string, error) {
	if err := validName(app); err != nil {
		return "", err
	}
	return "/apis/argoproj.io/v1alpha1/namespaces/" + w.argo + "/applications/" + app, nil
}

// RefreshApplication asks Argo CD to compare an application with Git again, the way the refresh annotation does.
// A hard refresh also drops Argo CD's cache of the manifests.
func (w *Writer) RefreshApplication(ctx context.Context, app string, hard bool) error {
	path, err := w.applicationPath(app)
	if err != nil {
		return err
	}
	mode := "normal"
	if hard {
		mode = "hard"
	}
	return w.patch(ctx, path, map[string]any{"metadata": map[string]any{"annotations": map[string]string{
		"argocd.argoproj.io/refresh": mode,
	}}})
}

// SyncApplication starts a sync of an application: the operation that makes Argo CD apply what Git says. It sets no
// options: no prune, no force, no replace.
func (w *Writer) SyncApplication(ctx context.Context, app string) error {
	path, err := w.applicationPath(app)
	if err != nil {
		return err
	}
	return w.patch(ctx, path, map[string]any{"operation": map[string]any{
		"initiatedBy": map[string]string{"username": "remedy"},
		"sync":        map[string]any{},
	}})
}
```

- [ ] **Step 4: Run the tests and watch them pass**

Run: `gofmt -l internal/kube; go vet ./internal/kube && go test ./internal/kube -race -count=1`
Expected: no output from `gofmt -l`, then `ok`. If `gofmt -l` lists a file, run `gofmt -d` on it and apply what it shows (the import order of `client.go` is the usual one).

- [ ] **Step 5: Mutation checks**

Make each change, run `go test ./internal/kube -count=1`, expect the named test to fail, and revert it.

1. In `NewReader`, change the function passed to `newClient` to `func(m string) bool { return true }`: `TestTheReaderRefusesEveryMethodButGet` fails.
2. In `NewWriter`, change the function passed to `newClient` to `func(m string) bool { return true }`: `TestTheWriterRefusesEveryMethodButPatchAndDelete` fails.
3. In `target`, change `if !slices.Contains(w.namespaces, namespace) {` to `if false && !slices.Contains(w.namespaces, namespace) {`: `TestTheWriterRefusesANamespaceOutsideTheAllowlistWithoutSendingAnything` fails.
4. In `validName`, change `!objectName.MatchString(name)` to `name == ""`: `TestTheWriterRefusesNamesThatCouldChangeThePath` fails.
5. In `SyncApplication`, change `"sync":        map[string]any{},` to `"sync":        map[string]any{"prune": true},`: `TestRefreshAndSyncAnApplication` fails.
6. In `newClient`, delete the `CheckRedirect` line: `TestARedirectIsNotFollowed` fails.
7. In `scrub`, change `if t := token.Reveal(); t != "" {` to `if t := token.Reveal(); false && t != "" {`: `TestAnErrorNeverContainsTheToken` fails.
8. In `do`, change `if len(raw) > maxBody {` to `if false && len(raw) > maxBody {`: `TestAnAnswerThatIsTooLargeIsAnError` fails.
9. In `WriteEnabled`, delete `&& len(c.WriteNamespaces) > 0`: `TestWhatIsEnabled` fails.
10. Make `client` remember the first token it read (a field `cached *secret.Value` that `do` fills once and then uses instead of calling `readToken`): `TestTheTokenFileIsReadAtEveryRequest` fails.

- [ ] **Step 6: Run the whole suite and commit**

Run: `go test ./... -race -count=1`
Expected: all packages `ok`.

```bash
git add internal
git commit -m "feat(kube): a read-only and a write client for the Kubernetes API, with their configuration" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Configuration, capabilities and the wiring

**Files:**
- Create: `internal/server/capabilities.go`, `internal/config/cluster_test.go`, `internal/server/capabilities_test.go`, `internal/app/cluster_test.go`
- Overwrite: `internal/app/app.go`
- Modify: `internal/config/config.go`, `internal/server/server.go`, `cmd/remedy-server/main.go`

**Interfaces:**
- Consumes: `kube.Config`, `kube.ParseNamespaces`, `kube.NewReader`, `kube.NewWriter`, `kube.WithLog`, `(*Reader).GetVersion` (Task 1); `env`, `password`, `runnerToken` (the server tests), `password` and `runnerToken` (the app tests).
- Produces:
  - `config.Server.Cluster kube.Config`, read from `REMEDY_K8S_API`, `REMEDY_K8S_CA_FILE`, `REMEDY_K8S_READ_TOKEN_FILE`, `REMEDY_K8S_WRITE_TOKEN_FILE`, `REMEDY_K8S_WRITE_NAMESPACES` and `REMEDY_K8S_ARGO_NAMESPACE`, and validated: a wrong setting stops the server at start and the message names the variable.
  - `server.Cluster{Read, Write bool; Namespaces []string}`, `server.Deps.Cluster` and `GET /api/capabilities` (session): `{"cluster":{"read":bool,"write":bool,"namespaces":[...]}}`. No address and no token is in it.
  - `app.App.KubeReader *kube.Reader` (nil without a cluster), `app.App.KubeWriter *kube.Writer` (nil unless actions are configured) and `(*App).CheckCluster(ctx)`, which logs whether the cluster answers to the read token.

- [ ] **Step 1: Write the failing tests**

Create `internal/config/cluster_test.go`:

```go
package config_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/config"
)

func tokenFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNoClusterSettingsMeansNoCluster(t *testing.T) {
	c, err := config.ServerFromEnv(serverEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Cluster.ReadEnabled() || c.Cluster.WriteEnabled() {
		t.Fatalf("cluster = %+v", c.Cluster)
	}
}

func TestTheClusterSettingsAreRead(t *testing.T) {
	read, write := tokenFile(t, "read", "r-token"), tokenFile(t, "write", "w-token")
	c, err := config.ServerFromEnv(serverEnv(map[string]string{
		"REMEDY_K8S_API":              "https://127.0.0.1:6443/",
		"REMEDY_K8S_READ_TOKEN_FILE":  read,
		"REMEDY_K8S_WRITE_TOKEN_FILE": write,
		"REMEDY_K8S_WRITE_NAMESPACES": "demo, staging",
		"REMEDY_K8S_ARGO_NAMESPACE":   "gitops",
	}))
	if err != nil {
		t.Fatal(err)
	}
	k := c.Cluster
	if k.API != "https://127.0.0.1:6443/" || k.ReadTokenFile != read || k.WriteTokenFile != write ||
		!slices.Equal(k.WriteNamespaces, []string{"demo", "staging"}) || k.ArgoNamespace != "gitops" {
		t.Fatalf("cluster = %+v", k)
	}
	if !k.ReadEnabled() || !k.WriteEnabled() {
		t.Fatal("both sides must be on")
	}
}

func TestAWrongClusterSettingStopsTheServerAndNamesTheVariable(t *testing.T) {
	read := tokenFile(t, "read", "r-token")
	cases := map[string]struct {
		env  map[string]string
		name string
	}{
		"a bad namespace": {map[string]string{"REMEDY_K8S_READ_TOKEN_FILE": read, "REMEDY_K8S_WRITE_TOKEN_FILE": read,
			"REMEDY_K8S_WRITE_NAMESPACES": "demo,*"}, "REMEDY_K8S_WRITE_NAMESPACES"},
		"namespaces without a write token": {map[string]string{"REMEDY_K8S_READ_TOKEN_FILE": read,
			"REMEDY_K8S_WRITE_NAMESPACES": "demo"}, "REMEDY_K8S_WRITE_NAMESPACES"},
		"a missing token file": {map[string]string{"REMEDY_K8S_READ_TOKEN_FILE": filepath.Join(t.TempDir(), "nope")},
			"REMEDY_K8S_READ_TOKEN_FILE"},
		"an API that is not a URL": {map[string]string{"REMEDY_K8S_READ_TOKEN_FILE": read, "REMEDY_K8S_API": "cluster"},
			"REMEDY_K8S_API"},
	}
	for name, tc := range cases {
		_, err := config.ServerFromEnv(serverEnv(tc.env))
		if err == nil || !strings.Contains(err.Error(), tc.name) {
			t.Errorf("%s: error = %v, want one that names %s", name, err, tc.name)
		}
	}
}
```

Create `internal/server/capabilities_test.go`:

```go
package server_test

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/store"
)

// capsEnv is a server with the given cluster capabilities and a signed-in admin client.
func capsEnv(t *testing.T, cluster server.Cluster) (*env, *http.Client) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ts := httptest.NewServer(server.New(server.Deps{Store: st, Auth: auth.New(password), RunnerToken: runnerToken, Cluster: cluster}))
	t.Cleanup(ts.Close)
	e := &env{ts: ts, store: st}
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if resp := e.do(t, c, http.MethodPost, "/api/login", `{"password":"`+password+`"}`, true); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("login = %d", resp.StatusCode)
	}
	return e, c
}

func bodyOf(t *testing.T, resp *http.Response) string {
	t.Helper()
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	return string(buf[:n])
}

func TestCapabilitiesSayThereIsNoClusterByDefault(t *testing.T) {
	e, c := capsEnv(t, server.Cluster{})
	resp := e.do(t, c, http.MethodGet, "/api/capabilities", "", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got, want := bodyOf(t, resp), `{"cluster":{"read":false,"write":false,"namespaces":[]}}`+"\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestCapabilitiesSayWhatIsConfigured(t *testing.T) {
	e, c := capsEnv(t, server.Cluster{Read: true, Write: true, Namespaces: []string{"demo", "staging"}})
	resp := e.do(t, c, http.MethodGet, "/api/capabilities", "", false)
	if got, want := bodyOf(t, resp), `{"cluster":{"read":true,"write":true,"namespaces":["demo","staging"]}}`+"\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestCapabilitiesNeedASession(t *testing.T) {
	e, _ := capsEnv(t, server.Cluster{Read: true})
	anonymous := &http.Client{}
	if resp := e.do(t, anonymous, http.MethodGet, "/api/capabilities", "", false); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}
```

Create `internal/app/cluster_test.go`:

```go
package app_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/app"
	"github.com/Jaydee94/remedy/internal/config"
	"github.com/Jaydee94/remedy/internal/kube"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/store"
)

func clusterApp(t *testing.T, cluster kube.Config, log *slog.Logger) *app.App {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	key, err := secret.ParseKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Server{
		AdminPassword: password, RunnerToken: runnerToken, MasterKey: key, GitHubAPIURL: "http://127.0.0.1:1", PollInterval: time.Minute,
		DiagnoseCooldown: 15 * time.Minute, DiagnoseMaxPerIncident: 3, DiagnoseMaxPerDay: 20, Cluster: cluster,
	}
	return app.New(cfg, st, log, nil)
}

func tokenOf(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(name+"-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWithoutClusterSettingsThereAreNoClusterClients(t *testing.T) {
	a := clusterApp(t, kube.Config{}, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	if a.KubeReader != nil || a.KubeWriter != nil {
		t.Fatalf("reader = %v, writer = %v", a.KubeReader, a.KubeWriter)
	}
	a.CheckCluster(context.Background()) // must not do anything, and must not panic
	if body, want := capabilities(t, a), `{"cluster":{"read":false,"write":false,"namespaces":[]}}`+"\n"; body != want {
		t.Fatalf("capabilities = %q, want %q", body, want)
	}
}

func TestTheClientsAreBuiltForWhatIsConfigured(t *testing.T) {
	log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	readOnly := clusterApp(t, kube.Config{API: "http://127.0.0.1:1", ReadTokenFile: tokenOf(t, "read")}, log)
	if readOnly.KubeReader == nil || readOnly.KubeWriter != nil {
		t.Fatalf("read only: reader = %v, writer = %v", readOnly.KubeReader, readOnly.KubeWriter)
	}
	both := clusterApp(t, kube.Config{API: "http://127.0.0.1:1", ReadTokenFile: tokenOf(t, "read"),
		WriteTokenFile: tokenOf(t, "write"), WriteNamespaces: []string{"demo"}}, log)
	if both.KubeReader == nil || both.KubeWriter == nil {
		t.Fatalf("both: reader = %v, writer = %v", both.KubeReader, both.KubeWriter)
	}
}

func TestCheckClusterSaysWhetherTheClusterAnswers(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer read-token" {
			http.Error(w, `{"kind":"Status","message":"Unauthorized","code":401}`, http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"gitVersion":"v1.31.0"}`))
	}))
	t.Cleanup(api.Close)

	var logged bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logged, nil))
	a := clusterApp(t, kube.Config{API: api.URL, ReadTokenFile: tokenOf(t, "read")}, log)
	a.CheckCluster(context.Background())
	if out := logged.String(); !strings.Contains(out, "level=INFO") || !strings.Contains(out, "v1.31.0") {
		t.Fatalf("log = %s", out)
	}

	logged.Reset()
	bad := clusterApp(t, kube.Config{API: api.URL, ReadTokenFile: tokenOf(t, "wrong")}, log)
	bad.CheckCluster(context.Background())
	if out := logged.String(); !strings.Contains(out, "level=WARN") || strings.Contains(out, "wrong-token") {
		t.Fatalf("log = %s", out)
	}
}

// capabilities signs in to the app and returns what /api/capabilities answers.
func capabilities(t *testing.T, a *app.App) string {
	t.Helper()
	ts := httptest.NewServer(a.Handler)
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	login, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/login", strings.NewReader(`{"password":"`+password+`"}`))
	login.Header.Set("X-Remedy-CSRF", "1")
	resp, err := client.Do(login)
	if err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("login: %v %v", resp, err)
	}
	resp, err = client.Get(ts.URL + "/api/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

func TestTheUIIsToldWhatIsConfiguredAndNothingSecret(t *testing.T) {
	log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	read, write := tokenOf(t, "read"), tokenOf(t, "write")
	a := clusterApp(t, kube.Config{API: "http://127.0.0.1:1", ReadTokenFile: read, WriteTokenFile: write,
		WriteNamespaces: []string{"demo", "staging"}}, log)
	body := capabilities(t, a)
	if want := `{"cluster":{"read":true,"write":true,"namespaces":["demo","staging"]}}` + "\n"; body != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
	for _, secretText := range []string{read, write, "127.0.0.1:1", "-token"} {
		if strings.Contains(body, secretText) {
			t.Fatalf("the answer contains %q", secretText)
		}
	}
}

func TestActionsAreNotOfferedWithoutAWriteSide(t *testing.T) {
	log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	a := clusterApp(t, kube.Config{API: "http://127.0.0.1:1", ReadTokenFile: tokenOf(t, "read")}, log)
	if body, want := capabilities(t, a), `{"cluster":{"read":true,"write":false,"namespaces":[]}}`+"\n"; body != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/config ./internal/server ./internal/app 2>&1 | head -14`
Expected: none of the three packages compiles (`c.Cluster undefined`, `undefined: server.Cluster`, `unknown field Cluster in struct literal of type config.Server`).

- [ ] **Step 3: The configuration**

In `internal/config/config.go`, replace:

```go
	"github.com/Jaydee94/remedy/internal/secret"
```

with:

```go
	"github.com/Jaydee94/remedy/internal/kube"
	"github.com/Jaydee94/remedy/internal/secret"
```

In `internal/config/config.go`, replace:

```go
	DiagnoseMaxPerDay      int           // REMEDY_DIAGNOSE_MAX_PER_DAY, automatic runs in 24 hours, default 20, 0 to 200; 0 turns it off
```

with:

```go
	DiagnoseMaxPerDay      int           // REMEDY_DIAGNOSE_MAX_PER_DAY, automatic runs in 24 hours, default 20, 0 to 200; 0 turns it off
	Cluster                kube.Config   // REMEDY_K8S_*: how to reach the cluster; the zero value means no cluster
```

In `internal/config/config.go`, replace:

```go
	if c.DiagnoseMaxPerDay, err = wholeNumber(get, "REMEDY_DIAGNOSE_MAX_PER_DAY", 20, 200); err != nil {
		return Server{}, err
	}
	return c, nil
```

with:

```go
	if c.DiagnoseMaxPerDay, err = wholeNumber(get, "REMEDY_DIAGNOSE_MAX_PER_DAY", 20, 200); err != nil {
		return Server{}, err
	}

	namespaces, err := kube.ParseNamespaces(get("REMEDY_K8S_WRITE_NAMESPACES"))
	if err != nil {
		return Server{}, fmt.Errorf("REMEDY_K8S_WRITE_NAMESPACES: %w", err)
	}
	c.Cluster = kube.Config{
		API:             get("REMEDY_K8S_API"),
		CAFile:          get("REMEDY_K8S_CA_FILE"),
		ReadTokenFile:   get("REMEDY_K8S_READ_TOKEN_FILE"),
		WriteTokenFile:  get("REMEDY_K8S_WRITE_TOKEN_FILE"),
		WriteNamespaces: namespaces,
		ArgoNamespace:   get("REMEDY_K8S_ARGO_NAMESPACE"),
	}
	if err := c.Cluster.Validate(); err != nil {
		return Server{}, err
	}
	return c, nil
```

- [ ] **Step 4: The capabilities**

Create `internal/server/capabilities.go`:

```go
package server

import "net/http"

// Cluster says what the control plane can do in a cluster, for the UI and for creating runs. The zero value is no
// cluster. It carries no address and no token.
type Cluster struct {
	Read       bool     // the cluster can be read: runs can have cluster tools
	Write      bool     // cluster actions are possible, after an approval
	Namespaces []string // where actions may be used
}

type clusterView struct {
	Read       bool     `json:"read"`
	Write      bool     `json:"write"`
	Namespaces []string `json:"namespaces"`
}

// capabilities tells the UI what it can offer. Nothing in it is secret, but it is behind the session like the rest.
func (s *srv) capabilities(w http.ResponseWriter, _ *http.Request) {
	v := clusterView{Read: s.d.Cluster.Read, Write: s.d.Cluster.Write, Namespaces: s.d.Cluster.Namespaces}
	if v.Namespaces == nil {
		v.Namespaces = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"cluster": v})
}
```

In `internal/server/server.go`, replace:

```go
	Gatekeeper *gatekeeper.Gatekeeper
}
```

with:

```go
	Gatekeeper *gatekeeper.Gatekeeper

	// Cluster says what can be done in a cluster. The zero value is no cluster.
	Cluster Cluster
}
```

In `internal/server/server.go`, replace:

```go
	mux.HandleFunc("GET /api/runs", s.session(s.listRuns))
```

with:

```go
	mux.HandleFunc("GET /api/capabilities", s.session(s.capabilities))
	mux.HandleFunc("GET /api/runs", s.session(s.listRuns))
```

- [ ] **Step 5: The wiring**

`internal/app/app.go` changes in several places (the clients, the start-up check, what the UI is told), so it is given as a whole.

Overwrite `internal/app/app.go`:

```go
// Package app wires the control plane together: the HTTP handler and the background workers (the poller and
// the reaper) with the incident engine and the responder they share. The server binary and the tests that run
// the whole chain use this one wiring.
package app

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/config"
	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/github"
	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/kube"
	"github.com/Jaydee94/remedy/internal/poller"
	"github.com/Jaydee94/remedy/internal/reaper"
	"github.com/Jaydee94/remedy/internal/responder"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/store"
)

type App struct {
	Handler    http.Handler
	Poller     *poller.Poller
	Reaper     *reaper.Reaper
	Responder  *responder.Responder
	Gatekeeper *gatekeeper.Gatekeeper

	// KubeReader reads the cluster; nil when no cluster is configured. KubeWriter does the approved actions; nil unless
	// a write token and an allowlist are configured. The cluster tools are built on them.
	KubeReader *kube.Reader
	KubeWriter *kube.Writer

	log *slog.Logger
}

// clusterClients builds the cluster clients a configuration asks for. The configuration was validated when it was
// read, so a failure here is unexpected: it is logged and the cluster stays off, as if it were not configured.
func clusterClients(c kube.Config, log *slog.Logger) (*kube.Reader, *kube.Writer) {
	if !c.ReadEnabled() {
		return nil, nil
	}
	reader, err := kube.NewReader(c, kube.WithLog(log))
	if err != nil {
		log.Error("the cluster is off: cannot set up the read side", "err", err)
		return nil, nil
	}
	if !c.WriteEnabled() {
		return reader, nil
	}
	writer, err := kube.NewWriter(c, kube.WithLog(log))
	if err != nil {
		log.Error("cluster actions are off: cannot set up the write side", "err", err)
		return reader, nil
	}
	return reader, writer
}

// CheckCluster says in the log whether the cluster answers to the read token. It changes nothing: a cluster that is
// down at start-up may be up a minute later.
func (a *App) CheckCluster(ctx context.Context) {
	if a.KubeReader == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	v, err := a.KubeReader.GetVersion(ctx)
	if err != nil {
		a.log.Warn("the cluster cannot be reached with the read token: cluster tools will fail", "err", err)
		return
	}
	a.log.Info("the cluster answers", "version", v.GitVersion, "actions", a.KubeWriter != nil)
}

// New wires everything for a configuration. web is the built UI, or nil.
func New(cfg config.Server, st *store.Store, log *slog.Logger, web fs.FS) *App {
	engine := &incident.Engine{Store: st}
	reader := func(token secret.Value) *github.Client {
		return github.New(cfg.GitHubAPIURL, token, nil, github.WithLog(log))
	}

	diagnoser := &responder.Responder{
		Store:     st,
		Key:       cfg.MasterKey,
		NewSource: func(token secret.Value) responder.Source { return reader(token) },
		Limits: store.DiagnosisLimits{
			Cooldown: cfg.DiagnoseCooldown, MaxPerIncident: cfg.DiagnoseMaxPerIncident, MaxPerDay: cfg.DiagnoseMaxPerDay,
		},
		Log: log,
	}
	kubeReader, kubeWriter := clusterClients(cfg.Cluster, log)
	gate := gatekeeper.New(gatekeeper.Config{
		Store: st,
		Tools: append(append(gatekeeper.IncidentTools(st), gatekeeper.JobLogTool(diagnoser)), gatekeeper.NoteTool(st)),
		Log:   log,
	})
	// The requests that waited for an approval died with the previous process; nobody can receive their results.
	if n, err := st.AbandonAllWaiting(context.Background()); err != nil {
		log.Error("could not abandon the approvals a restart left behind", "err", err)
	} else if n > 0 {
		log.Warn("abandoned the approvals a restart left behind", "count", n)
	}

	return &App{
		Responder:  diagnoser,
		Gatekeeper: gate,
		KubeReader: kubeReader,
		KubeWriter: kubeWriter,
		log:        log,
		Poller: &poller.Poller{
			Store:      st,
			Engine:     engine,
			Key:        cfg.MasterKey,
			NewSource:  func(token secret.Value) poller.Source { return reader(token) },
			Interval:   cfg.PollInterval,
			Log:        log,
			AfterCycle: diagnoser.AutoStart,
		},
		Reaper: &reaper.Reaper{
			Store: st,
			Log:   log,
			OnFailed: func(ctx context.Context, ids []string) {
				for _, id := range ids {
					diagnoser.Complete(ctx, id)
				}
			},
		},
		Handler: server.New(server.Deps{
			Store:        st,
			Auth:         auth.New(cfg.AdminPassword),
			RunnerToken:  cfg.RunnerToken,
			Web:          web,
			Key:          cfg.MasterKey,
			NewGitHub:    func(token secret.Value) server.GitHub { return reader(token) },
			Incidents:    engine,
			Responder:    diagnoser,
			PollInterval: cfg.PollInterval,
			Gatekeeper:   gate,
			Cluster:      clusterCapabilities(cfg.Cluster, kubeReader, kubeWriter),
		}),
	}
}

// clusterCapabilities is what the UI is told: the read side as far as it was set up, and the namespaces of the
// allowlist only when actions are possible at all.
func clusterCapabilities(c kube.Config, reader *kube.Reader, writer *kube.Writer) server.Cluster {
	caps := server.Cluster{Read: reader != nil, Write: writer != nil}
	if writer != nil {
		caps.Namespaces = slices.Clone(c.WriteNamespaces)
	}
	return caps
}
```

In `cmd/remedy-server/main.go`, replace:

```go
	a := app.New(cfg, st, log, web.FS())
```

with:

```go
	for _, w := range cfg.Cluster.Warnings() {
		log.Warn(w)
	}

	a := app.New(cfg, st, log, web.FS())
	background(a.CheckCluster)
```

- [ ] **Step 6: Run the tests and watch them pass**

Run: `gofmt -l internal cmd; go vet ./... && go test ./internal/config ./internal/server ./internal/app ./internal/kube -race -count=1`
Expected: no output from `gofmt -l`, then `ok` for all four. If `gofmt -l` lists `internal/app/cluster_test.go`, run `gofmt -w` on it (the import order).

- [ ] **Step 7: Mutation checks**

Make each change, run `go test` for the package named in brackets with `-count=1`, expect the named test to fail, and revert it.

1. [`./internal/config`] In `ServerFromEnv`, change `if err := c.Cluster.Validate(); err != nil {` to `if err := error(nil); err != nil {`: `TestAWrongClusterSettingStopsTheServerAndNamesTheVariable` fails.
2. [`./internal/server`] In `capabilities`, change `Write: s.d.Cluster.Write,` to `Write: true,`: `TestCapabilitiesSayThereIsNoClusterByDefault` fails.
3. [`./internal/server`] In `server.go`, change `s.session(s.capabilities)` to `s.capabilities`: `TestCapabilitiesNeedASession` fails.
4. [`./internal/app`] In `clusterCapabilities`, change `Read: reader != nil,` to `Read: true,`: `TestWithoutClusterSettingsThereAreNoClusterClients` fails.
5. [`./internal/app`] In `clusterCapabilities`, change `Write: writer != nil}` to `Write: reader != nil}`: `TestActionsAreNotOfferedWithoutAWriteSide` fails.

- [ ] **Step 8: Run the whole suite and commit**

Run: `go test ./... -race -count=1`
Expected: all packages `ok`.

```bash
git add internal cmd
git commit -m "feat: configure the cluster, tell the UI what is possible and check the cluster at start-up" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: A run with cluster tools

**Files:**
- Create: `internal/store/migrations/007_cluster.sql`, `internal/store/clusterrun_test.go`, `internal/server/runs_cluster_test.go`
- Modify: `internal/run/run.go`, `internal/store/store.go`, `internal/store/runtokens.go`, `internal/server/runs.go`

**Interfaces:**
- Consumes: `server.Cluster` and `Deps.Cluster` (Task 2), `store.CreateToolRun`, `openStore` (the store tests), `bodyOf` and `field` (the server tests), `env` and `password` (the server tests).
- Produces:
  - Migration 007: `runs.cluster INTEGER NOT NULL DEFAULT 0`.
  - `run.Run.Cluster` (`json:"cluster,omitempty"`), read by every query that returns runs.
  - `(*Store).CreateClusterRun(ctx, provider, prompt) (run.Run, error)`: a queued ad-hoc run with `mcp = 1` and `cluster = 1`.
  - `POST /api/runs` accepts `cluster` (default false): 400 without `tools`, 409 when `Deps.Cluster.Read` is false. `GET /api/runs` and `GET /api/runs/{id}` carry the flag.

- [ ] **Step 1: Write the failing tests**

Create `internal/store/clusterrun_test.go`:

```go
package store_test

import (
	"context"
	"testing"

	"github.com/Jaydee94/remedy/internal/run"
)

func TestCreateClusterRunMarksTheRunAsHavingClusterTools(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	r, err := s.CreateClusterRun(ctx, "claude", "look at the cluster")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Cluster || !r.MCP || r.Status != run.Queued || r.Role != run.RoleAdhoc {
		t.Fatalf("run = %+v: a cluster run has gatekeeper access and cluster tools", r)
	}
	got, err := s.GetRun(ctx, r.ID)
	if err != nil || !got.Cluster || !got.MCP {
		t.Fatalf("GetRun = %+v, %v", got, err)
	}
}

func TestOtherRunsHaveNoClusterTools(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	plain, _ := s.CreateRun(ctx, "claude", "plain")
	tools, _ := s.CreateToolRun(ctx, "claude", "tools")
	if plain.Cluster || tools.Cluster {
		t.Fatalf("plain = %+v, tools = %+v", plain, tools)
	}
}

func TestAClusterRunKeepsItsFlagThroughTheClaimTheListAndTheToken(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	made, _ := s.CreateClusterRun(ctx, "claude", "cluster")
	claimed, err := s.ClaimNext(ctx)
	if err != nil || claimed == nil || claimed.ID != made.ID || !claimed.Cluster {
		t.Fatalf("claimed = %+v, %v", claimed, err)
	}
	token, err := s.MintRunToken(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("a cluster run gets a run token like any run with tools: %v", err)
	}
	byToken, err := s.RunForToken(ctx, token)
	if err != nil || !byToken.Cluster {
		t.Fatalf("RunForToken = %+v, %v: the gatekeeper decides the tools by this flag", byToken, err)
	}
	list, err := s.ListRuns(ctx, 10)
	if err != nil || len(list) != 1 || !list[0].Cluster {
		t.Fatalf("ListRuns = %+v, %v", list, err)
	}
}
```

Create `internal/server/runs_cluster_test.go`:

```go
package server_test

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/store"
)

// clusterRunEnv is a server with the gatekeeper and the given cluster, and a signed-in admin client.
func clusterRunEnv(t *testing.T, cluster server.Cluster) (*env, *http.Client) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	g := gatekeeper.New(gatekeeper.Config{Store: st, Tools: gatekeeper.IncidentTools(st)})
	ts := httptest.NewServer(server.New(server.Deps{
		Store: st, Auth: auth.New(password), RunnerToken: runnerToken, Gatekeeper: g, Cluster: cluster,
	}))
	t.Cleanup(ts.Close)
	e := &env{ts: ts, store: st}
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if resp := e.do(t, c, http.MethodPost, "/api/login", `{"password":"`+password+`"}`, true); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("login = %d", resp.StatusCode)
	}
	return e, c
}

func TestARunWithClusterToolsIsCreatedWhenTheClusterCanBeRead(t *testing.T) {
	e, c := clusterRunEnv(t, server.Cluster{Read: true})
	resp := e.do(t, c, http.MethodPost, "/api/runs", `{"prompt":"look at the cluster","tools":true,"cluster":true}`, true)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := bodyOf(t, resp)
	if field(t, body, "cluster") != true || field(t, body, "mcp") != true {
		t.Fatalf("run = %s, want a run with gatekeeper access and cluster tools", body)
	}
	id := field(t, body, "id").(string)

	got := bodyOf(t, e.do(t, c, http.MethodGet, "/api/runs/"+id, "", false))
	if field(t, got, "cluster") != true {
		t.Fatalf("GET run = %s", got)
	}
}

func TestARunWithGatekeeperToolsOnlyHasNoClusterFlag(t *testing.T) {
	e, c := clusterRunEnv(t, server.Cluster{Read: true})
	resp := e.do(t, c, http.MethodPost, "/api/runs", `{"prompt":"just the incidents","tools":true}`, true)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if body := bodyOf(t, resp); field(t, body, "cluster") == true || field(t, body, "mcp") != true {
		t.Fatalf("run = %s", body)
	}
}

func TestClusterToolsNeedTheGatekeeperTools(t *testing.T) {
	e, c := clusterRunEnv(t, server.Cluster{Read: true})
	resp := e.do(t, c, http.MethodPost, "/api/runs", `{"prompt":"look at the cluster","cluster":true}`, true)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if runs, _ := e.store.ListRuns(t.Context(), 10); len(runs) != 0 {
		t.Fatalf("a refused request created %d runs", len(runs))
	}
}

func TestClusterToolsNeedAConfiguredCluster(t *testing.T) {
	e, c := clusterRunEnv(t, server.Cluster{})
	resp := e.do(t, c, http.MethodPost, "/api/runs", `{"prompt":"look at the cluster","tools":true,"cluster":true}`, true)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	if runs, _ := e.store.ListRuns(t.Context(), 10); len(runs) != 0 {
		t.Fatalf("a refused request created %d runs", len(runs))
	}
}

func TestTheRunListShowsWhichRunsHaveClusterTools(t *testing.T) {
	e, c := clusterRunEnv(t, server.Cluster{Read: true})
	e.do(t, c, http.MethodPost, "/api/runs", `{"prompt":"plain"}`, true)
	e.do(t, c, http.MethodPost, "/api/runs", `{"prompt":"cluster","tools":true,"cluster":true}`, true)
	runs, err := e.store.ListRuns(t.Context(), 10)
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs = %+v, %v", runs, err)
	}
	for _, r := range runs {
		if r.Cluster != (r.Prompt == "cluster") {
			t.Fatalf("run %q has cluster = %v", r.Prompt, r.Cluster)
		}
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/store ./internal/server 2>&1 | head -12`
Expected: neither package compiles (`s.CreateClusterRun undefined`, `got.Cluster undefined (type run.Run has no field or method Cluster)`).

- [ ] **Step 3: The migration, the store and the API**

Create `internal/store/migrations/007_cluster.sql`:

```sql
-- cluster: the run was started with cluster tools, the Kubernetes tools of the gatekeeper. It is set together with
-- mcp when the run is created and never changes; the gatekeeper offers the cluster tools only to such a run.
ALTER TABLE runs ADD COLUMN cluster INTEGER NOT NULL DEFAULT 0;
```

In `internal/run/run.go`, replace:

```go
	CancelRequested bool            `json:"cancelRequested,omitempty"` // the maintainer cancelled the run
```

with:

```go
	CancelRequested bool            `json:"cancelRequested,omitempty"` // the maintainer cancelled the run
	Cluster         bool            `json:"cluster,omitempty"`         // the run has the cluster tools of the gatekeeper
```

In `internal/store/store.go`, replace:

```go
role, incident_id, output, failure_reason, head_sha, automatic, mcp, cancel_requested`
```

with:

```go
role, incident_id, output, failure_reason, head_sha, automatic, mcp, cancel_requested, cluster`
```

In `internal/store/store.go`, replace:

```go
	var mcp, cancelRequested int
```

with:

```go
	var mcp, cancelRequested, cluster int
```

In `internal/store/store.go`, replace:

```go
&r.HeadSHA, &automatic, &mcp, &cancelRequested); err != nil {
```

with:

```go
&r.HeadSHA, &automatic, &mcp, &cancelRequested, &cluster); err != nil {
```

In `internal/store/store.go`, replace:

```go
	r.MCP, r.CancelRequested = mcp != 0, cancelRequested != 0
```

with:

```go
	r.MCP, r.CancelRequested = mcp != 0, cancelRequested != 0
	r.Cluster = cluster != 0
```

In `internal/store/runtokens.go`, replace:

```go
// hashToken is what is stored of a run token.
```

with:

```go
// CreateClusterRun creates a queued ad-hoc run that has access to the gatekeeper and to its cluster tools.
func (s *Store) CreateClusterRun(ctx context.Context, provider, prompt string) (run.Run, error) {
	id := run.NewID()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO runs (id, provider, prompt, status, mcp, cluster, created_at) VALUES (?, ?, ?, 'queued', 1, 1, ?)`,
		id, provider, prompt, formatTS(time.Now()))
	if err != nil {
		return run.Run{}, err
	}
	return s.GetRun(ctx, id)
}

// hashToken is what is stored of a run token.
```

In `internal/server/runs.go`, replace:

```go
		Tools    bool   `json:"tools"`
```

with:

```go
		Tools    bool   `json:"tools"`
		Cluster  bool   `json:"cluster"`
```

In `internal/server/runs.go`, replace:

```go
	create := s.d.Store.CreateRun
	if req.Tools {
		create = s.d.Store.CreateToolRun
	}
```

with:

```go
	if req.Cluster && !req.Tools {
		writeErr(w, http.StatusBadRequest, "cluster tools need the gatekeeper tools")
		return
	}
	if req.Cluster && !s.d.Cluster.Read {
		writeErr(w, http.StatusConflict, "no cluster is configured")
		return
	}
	create := s.d.Store.CreateRun
	switch {
	case req.Cluster:
		create = s.d.Store.CreateClusterRun
	case req.Tools:
		create = s.d.Store.CreateToolRun
	}
```

- [ ] **Step 4: Run the tests and watch them pass**

Run: `gofmt -l internal; go vet ./... && go test ./internal/store ./internal/server ./internal/run -race -count=1`
Expected: no output from `gofmt -l`, then `ok` for all three. If `gofmt -l` lists a file, run `gofmt -d` on it and apply what it shows.

- [ ] **Step 5: Mutation checks**

Make each change, run `go test` for the package named in brackets with `-count=1`, expect the named test to fail, and revert it.

1. [`./internal/store`] In `CreateClusterRun`, change `VALUES (?, ?, ?, 'queued', 1, 1, ?)` to `VALUES (?, ?, ?, 'queued', 1, 0, ?)`: `TestCreateClusterRunMarksTheRunAsHavingClusterTools` fails.
2. [`./internal/store`] In `scanRun`, change `r.Cluster = cluster != 0` to `r.Cluster = false`: `TestAClusterRunKeepsItsFlagThroughTheClaimTheListAndTheToken` fails.
3. [`./internal/server`] In `createRun`, change `if req.Cluster && !s.d.Cluster.Read {` to `if false && req.Cluster && !s.d.Cluster.Read {`: `TestClusterToolsNeedAConfiguredCluster` fails.
4. [`./internal/server`] In `createRun`, change `if req.Cluster && !req.Tools {` to `if false && req.Cluster && !req.Tools {`: `TestClusterToolsNeedTheGatekeeperTools` fails.
5. [`./internal/server`] In `createRun`, change `case req.Cluster:` to `case false:`: `TestARunWithClusterToolsIsCreatedWhenTheClusterCanBeRead` fails.

- [ ] **Step 6: Run the whole suite and commit**

Run: `go test ./... -race -count=1`
Expected: all packages `ok`.

```bash
git add internal
git commit -m "feat: start a run with cluster tools, and remember that it has them" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Tool groups in the gatekeeper

**Files:**
- Create: `internal/gatekeeper/group_test.go`
- Modify: `internal/gatekeeper/tools.go`, `internal/gatekeeper/gatekeeper.go`, `internal/gatekeeper/call.go`

**Interfaces:**
- Consumes: `run.Run.Cluster` and `(*Store).CreateClusterRun` (Task 3), `newEnv`, `env.post`, `resultText` and `echoTool` (the gatekeeper tests).
- Produces:
  - `gatekeeper.Tool.Group` and `gatekeeper.GroupCluster`: a tool without a group is offered to every run with gatekeeper access; a tool of the group `cluster` only to a run with `Cluster` set. `gatekeeper.New` panics for a group that does not exist.
  - `tools/list` lists only what the run is offered. `tools/call` for a tool the run is not offered is answered `unknown tool <name>`, exactly like a tool that does not exist, the tool does not run, and the call is still an audit row.

- [ ] **Step 1: Write the failing tests**

Create `internal/gatekeeper/group_test.go`:

```go
package gatekeeper_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/Jaydee94/remedy/internal/gatekeeper"
)

// clusterProbe is a read tool of the cluster group that counts how often it ran.
func clusterProbe(runs *atomic.Int32) gatekeeper.Tool {
	return gatekeeper.Tool{
		Name:        "cluster_probe",
		Description: "A tool of the cluster group.",
		Group:       gatekeeper.GroupCluster,
		Schema:      json.RawMessage(`{"type":"object","additionalProperties":false}`),
		Decode: func(raw json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{}`), gatekeeper.DecodeArgs(raw, &struct{}{})
		},
		Run: func(context.Context, gatekeeper.Call) (string, error) {
			runs.Add(1)
			return "the cluster answers", nil
		},
	}
}

// clusterRunToken makes a run with cluster tools, lets a runner claim it and returns its token. The environment's
// own run has gatekeeper access but no cluster tools.
func clusterRunToken(t *testing.T, e *env) string {
	t.Helper()
	ctx := context.Background()
	if _, err := e.st.CreateClusterRun(ctx, "claude", "look at the cluster"); err != nil {
		t.Fatal(err)
	}
	claimed, err := e.st.ClaimNext(ctx)
	if err != nil || claimed == nil || !claimed.Cluster {
		t.Fatalf("ClaimNext = %+v, %v", claimed, err)
	}
	token, err := e.st.MintRunToken(ctx, claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// listedTools returns the names tools/list offers to the token.
func listedTools(t *testing.T, e *env, token string) []string {
	t.Helper()
	status, out := e.post(t, token, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if status != http.StatusOK {
		t.Fatalf("tools/list = %d %v", status, out)
	}
	res, _ := out["result"].(map[string]any)
	var names []string
	for _, tool := range res["tools"].([]any) {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	return names
}

func has(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

func callWith(t *testing.T, e *env, token, useID, tool string) (string, bool) {
	t.Helper()
	status, out := e.post(t, token, fmt.Sprintf(
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":%q,"arguments":{},"_meta":{"claudecode/toolUseId":%q}}}`, tool, useID))
	if status != http.StatusOK {
		t.Fatalf("tools/call = %d %v", status, out)
	}
	return resultText(t, out)
}

func TestARunWithoutClusterToolsDoesNotSeeThem(t *testing.T) {
	var probes atomic.Int32
	e := newEnv(t, clusterProbe(&probes))
	if names := listedTools(t, e, e.token); has(names, "cluster_probe") || !has(names, "echo") {
		t.Fatalf("a run without cluster tools is offered %v", names)
	}
	if names := listedTools(t, e, clusterRunToken(t, e)); !has(names, "cluster_probe") || !has(names, "echo") {
		t.Fatalf("a run with cluster tools is offered %v", names)
	}
}

func TestARunWithoutClusterToolsCannotCallThemAndLearnsNothingFromTheRefusal(t *testing.T) {
	var probes atomic.Int32
	e := newEnv(t, clusterProbe(&probes))

	text, isErr := callWith(t, e, e.token, "toolu_1", "cluster_probe")
	if !isErr || text != "unknown tool cluster_probe" {
		t.Fatalf("answer = %q, isError = %v", text, isErr)
	}
	other, _ := callWith(t, e, e.token, "toolu_2", "no_such_tool")
	if other != "unknown tool no_such_tool" {
		t.Fatalf("an unknown tool is answered with %q: the refusal must look the same", other)
	}
	if n := probes.Load(); n != 0 {
		t.Fatalf("the cluster tool ran %d times for a run without cluster tools", n)
	}
	calls, err := e.st.ListToolCalls(context.Background(), e.run.ID)
	if err != nil || len(calls) != 2 {
		t.Fatalf("calls = %+v, %v: a refused call is still in the audit log", calls, err)
	}
}

func TestARunWithClusterToolsCanCallThem(t *testing.T) {
	var probes atomic.Int32
	e := newEnv(t, clusterProbe(&probes))
	token := clusterRunToken(t, e)
	text, isErr := callWith(t, e, token, "toolu_1", "cluster_probe")
	if isErr || text != "the cluster answers" || probes.Load() != 1 {
		t.Fatalf("answer = %q, isError = %v, runs = %d", text, isErr, probes.Load())
	}
}

func TestAToolOfAnUnknownGroupIsRefusedAtStartUp(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("a tool of a group nobody knows was accepted: it would never be offered")
		}
	}()
	tool := clusterProbe(&atomic.Int32{})
	tool.Group = "clustre"
	gatekeeper.New(gatekeeper.Config{Tools: []gatekeeper.Tool{tool}})
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/gatekeeper 2>&1 | head -6`
Expected: the package does not compile (`unknown field Group in struct literal of type gatekeeper.Tool`, `undefined: gatekeeper.GroupCluster`).

- [ ] **Step 3: The groups**

In `internal/gatekeeper/tools.go`, replace:

```go
// Tool is something an agent can call.
```

with:

```go
// GroupCluster is the group of the Kubernetes tools. A tool of a group is offered only to runs that have it; a tool
// without a group is offered to every run with gatekeeper access.
const GroupCluster = "cluster"

// Tool is something an agent can call.
```

In `internal/gatekeeper/tools.go`, replace:

```go
	Name        string
	Description string
```

with:

```go
	Name        string
	Description string
	// Group says which runs are offered the tool: empty for every run with gatekeeper access, GroupCluster for runs
	// that were started with cluster tools. A run that is not offered a tool cannot call it either, and its attempt is
	// answered like a call of a tool that does not exist.
	Group string
```

In `internal/gatekeeper/tools.go`, replace:

```go
var toolName = regexp.MustCompile
```

with:

```go
// offeredTo says whether a run is offered the tool.
func (t Tool) offeredTo(r run.Run) bool {
	switch t.Group {
	case "":
		return true
	case GroupCluster:
		return r.Cluster
	}
	return false
}

var toolName = regexp.MustCompile
```

In `internal/gatekeeper/tools.go`, replace:

```go
	case t.Decode == nil || t.Run == nil:
		return fmt.Errorf("tool %q needs Decode and Run", t.Name)
```

with:

```go
	case t.Decode == nil || t.Run == nil:
		return fmt.Errorf("tool %q needs Decode and Run", t.Name)
	case t.Group != "" && t.Group != GroupCluster:
		return fmt.Errorf("tool %q is in the group %q, which does not exist", t.Name, t.Group)
```

In `internal/gatekeeper/tools.go`, replace:

```go
	"regexp"
)
```

with:

```go
	"regexp"

	"github.com/Jaydee94/remedy/internal/run"
)
```

In `internal/gatekeeper/gatekeeper.go`, replace:

```go
		for _, t := range g.tools {
			list = append(list,
```

with:

```go
		for _, t := range g.tools {
			if !t.offeredTo(r0) {
				continue
			}
			list = append(list,
```

In `internal/gatekeeper/call.go`, replace:

```go
	tool, known := g.byName[p.Name]
```

with:

```go
	tool, known := g.byName[p.Name]
	if known && !tool.offeredTo(rn) {
		tool, known = Tool{}, false // not offered to this run: the same answer as for a tool that does not exist
	}
```

- [ ] **Step 4: Run the tests and watch them pass**

Run: `gofmt -l internal; go vet ./... && go test ./internal/gatekeeper -race -count=1`
Expected: no output from `gofmt -l`, then `ok`.

- [ ] **Step 5: Mutation checks**

Make each change, run `go test ./internal/gatekeeper -count=1`, expect the named test to fail, and revert it.

1. In `offeredTo`, change `return r.Cluster` to `return true`: `TestARunWithoutClusterToolsDoesNotSeeThem` fails.
2. In `ServeHTTP` (`tools/list`), change `if !t.offeredTo(r0) {` to `if false && !t.offeredTo(r0) {`: `TestARunWithoutClusterToolsDoesNotSeeThem` fails.
3. In `callTool`, change `if known && !tool.offeredTo(rn) {` to `if false && known && !tool.offeredTo(rn) {`: `TestARunWithoutClusterToolsCannotCallThemAndLearnsNothingFromTheRefusal` fails (the tool runs).
4. In `validateTool`, change `case t.Group != "" && t.Group != GroupCluster:` to `case false:`: `TestAToolOfAnUnknownGroupIsRefusedAtStartUp` fails.

- [ ] **Step 6: Run the whole suite and commit**

Run: `go test ./... -race -count=1`
Expected: all packages `ok`.

```bash
git add internal
git commit -m "feat(gatekeeper): tool groups, so that a run is offered only the tools it was started with" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 5: The switch "Allow cluster tools", and the documents

**Files:**
- Modify: `web/src/api.ts`, `web/src/RunView.tsx`, `web/src/RunsPage.tsx`, `CLAUDE.md`, `README.md`

**Interfaces:**
- Consumes: `GET /api/capabilities` and `cluster` on `POST /api/runs` (Tasks 2 and 3), `Switch`, `Label` (shadcn, already installed), `Badge`.
- Produces:
  - `api.ts`: the type `Capabilities`, the field `cluster` on `Run`, `api.getCapabilities()` and `api.createRun(prompt, tools?, cluster?)`.
  - The Runs page shows a second switch "Allow cluster tools" when the read side is configured, with a line that says whether actions are possible and in which namespaces. Turning it on turns "Allow gatekeeper tools" on; turning the tools off turns it off.
  - The run list and the run view mark a run that has cluster tools.

There is no web test runner in this repository, so this task is checked by the type checker, the linter and a real browser (Step 4).

- [ ] **Step 1: The API client**

In `web/src/api.ts`, replace:

```ts
  /** The maintainer cancelled the run; the runner is stopping the agent. */
```

with:

```ts
  /** The run has the cluster tools of the gatekeeper (it was started with "Allow cluster tools"). */
  cluster?: boolean
  /** The maintainer cancelled the run; the runner is stopping the agent. */
```

In `web/src/api.ts`, replace:

```ts
/** A call of an agent to a gatekeeper tool
```

with:

```ts
/** What the control plane can do in a cluster. It carries no address and no token. */
export interface Capabilities {
  cluster: {
    /** The cluster can be read: a run can be started with cluster tools. */
    read: boolean
    /** Actions in the cluster are possible, each one after an approval. */
    write: boolean
    /** The namespaces in which actions may be used. */
    namespaces: string[]
  }
}

/** A call of an agent to a gatekeeper tool
```

In `web/src/api.ts`, replace:

```ts
  createRun: (prompt: string, tools = false) => request<Run>('POST', '/api/runs', { prompt, tools }),
```

with:

```ts
  createRun: (prompt: string, tools = false, cluster = false) =>
    request<Run>('POST', '/api/runs', { prompt, tools, cluster }),
  getCapabilities: () => request<Capabilities>('GET', '/api/capabilities'),
```

- [ ] **Step 2: The run view and the runs page**

In `web/src/RunView.tsx`, replace:

```tsx
            {run.mcp && <Badge variant="secondary">tools</Badge>}
```

with:

```tsx
            {run.mcp && <Badge variant="secondary">tools</Badge>}
            {run.cluster && <Badge variant="secondary">cluster</Badge>}
```

In `web/src/RunsPage.tsx`, replace:

```tsx
import type { Run } from './api.ts'
```

with:

```tsx
import type { Capabilities, Run } from './api.ts'
```

In `web/src/RunsPage.tsx`, replace:

```tsx
  const [tools, setTools] = useState(false)
```

with:

```tsx
  const [tools, setTools] = useState(false)
  const [cluster, setCluster] = useState(false)
  const [capabilities, setCapabilities] = useState<Capabilities | null>(null)
```

In `web/src/RunsPage.tsx`, replace:

```tsx
  useEffect(() => {
    refresh()
```

with:

```tsx
  useEffect(() => {
    api.getCapabilities().then(setCapabilities).catch(() => setCapabilities(null))
  }, [])

  useEffect(() => {
    refresh()
```

In `web/src/RunsPage.tsx`, replace:

```tsx
await api.createRun(prompt, tools)
```

with:

```tsx
await api.createRun(prompt, tools, cluster)
```

In `web/src/RunsPage.tsx`, replace:

```tsx
              <Switch id="tools" checked={tools} onCheckedChange={setTools} />
```

with:

```tsx
              <Switch
                id="tools"
                checked={tools}
                onCheckedChange={(on) => {
                  setTools(on)
                  if (!on) setCluster(false) // the cluster tools are a part of the gatekeeper's tools
                }}
              />
```

In `web/src/RunsPage.tsx`, replace:

```tsx
            <p className="text-sm text-muted-foreground">
              {tools
                ? 'The agent can read incidents and ask to add a note to one. Every note waits for your decision under Approvals, and the run waits with it.'
                : 'The agent can only read the files of its workspace.'}
            </p>
```

with:

```tsx
            <p className="text-sm text-muted-foreground">
              {tools
                ? 'The agent can read incidents and ask to add a note to one. Every note waits for your decision under Approvals, and the run waits with it.'
                : 'The agent can only read the files of its workspace.'}
            </p>
            {capabilities?.cluster.read && (
              <>
                <div className="flex items-center gap-3">
                  <Switch
                    id="cluster"
                    checked={cluster}
                    onCheckedChange={(on) => {
                      setCluster(on)
                      if (on) setTools(true) // cluster tools need the gatekeeper
                    }}
                  />
                  <Label htmlFor="cluster">Allow cluster tools</Label>
                </div>
                <p className="text-sm text-muted-foreground">
                  {capabilities.cluster.write
                    ? `The agent can read the cluster. Actions in the cluster wait for your decision under Approvals; they are possible in: ${capabilities.cluster.namespaces.join(', ')}.`
                    : 'The agent can read the cluster. No action in the cluster is possible: no write access is configured.'}
                </p>
              </>
            )}
```

In `web/src/RunsPage.tsx`, replace:

```tsx
            {r.mcp && <span className="text-xs text-muted-foreground">tools</span>}
```

with:

```tsx
            {r.mcp && <span className="text-xs text-muted-foreground">tools</span>}
            {r.cluster && <span className="text-xs text-muted-foreground">cluster</span>}
```

- [ ] **Step 3: Lint and build**

Run: `make web-install` (a fresh worktree has no `web/node_modules`), then `cd web && npm run lint && npm run build`
Expected: oxlint reports no warnings or errors; `tsc -b` and `vite build` succeed.

- [ ] **Step 4: Check it in a real browser**

The script builds the server with the UI and starts it on a fresh database in the mode you name: `none` (no cluster settings), `readonly` (a read token) or `cluster` (a read token, a write token and the namespaces `demo` and `staging`). The API address is a port nothing listens on on purpose: the start-up check must warn and the server must go on. Run it from the worktree, once per mode, and stop the server (`lsof -ti tcp:8080 | xargs kill`) before the next.

```bash
#!/bin/bash
# Usage: bash ui-check.sh <none|readonly|cluster>
make build || exit 1
D=$(mktemp -d)
printf 'read-token-value\n' > "$D/read.token"; printf 'write-token-value\n' > "$D/write.token"
export REMEDY_ADMIN_PASSWORD='ui-test-password' REMEDY_RUNNER_TOKEN="$(openssl rand -hex 24)" \
  REMEDY_MASTER_KEY="$(openssl rand -base64 32)" REMEDY_DB="$D/remedy.db" REMEDY_ADDR=127.0.0.1:8080 REMEDY_LOG_LEVEL=debug
case "$1" in
  cluster)
    export REMEDY_K8S_API=http://127.0.0.1:9 REMEDY_K8S_READ_TOKEN_FILE="$D/read.token" \
      REMEDY_K8S_WRITE_TOKEN_FILE="$D/write.token" REMEDY_K8S_WRITE_NAMESPACES=demo,staging ;;
  readonly)
    export REMEDY_K8S_API=http://127.0.0.1:9 REMEDY_K8S_READ_TOKEN_FILE="$D/read.token" ;;
esac
nohup ./bin/remedy-server > "$D/server.log" 2>&1 &
until curl -sf http://127.0.0.1:8080/healthz > /dev/null; do sleep 0.2; done
sleep 1
echo "server up ($1), files in $D"
cut -c1-200 "$D/server.log"
```

With the Playwright tools, sign in at `http://127.0.0.1:8080` (password `ui-test-password`), open **Runs** and check, deleting any screenshot files afterwards:

1. Mode `none`: the form has the switch **Allow gatekeeper tools** and no switch **Allow cluster tools**. `server.log` has no line about the cluster.
2. Mode `readonly`: both switches are there, and the line under the cluster switch says "The agent can read the cluster. No action in the cluster is possible: no write access is configured." `server.log` has a warning "the cluster cannot be reached with the read token: cluster tools will fail" and the server runs on.
3. Mode `cluster`: the line under the cluster switch says "The agent can read the cluster. Actions in the cluster wait for your decision under Approvals; they are possible in: demo, staging."
4. Mode `cluster`, interplay: turning **Allow cluster tools** on turns **Allow gatekeeper tools** on too. Turning **Allow gatekeeper tools** off turns the cluster switch off. (Check `aria-checked` of `#tools` and `#cluster`.)
5. Mode `cluster`: turn the cluster switch on, type a prompt, and start the run with a `fetch` spy installed first (`window.fetch` wrapped so that it records the body of each request). The body is `{"prompt":"...","tools":true,"cluster":true}`, the page of the new run shows the badges `tools` and `cluster`, and the run list shows "tools" and "cluster" next to the prompt.
6. `server.log` of the last server contains neither `read-token-value` nor `write-token-value` (`grep -c` prints 0), and the browser console has no errors other than the 401 of the sign-in check.

Stop the server afterwards, and delete the temporary directory (`$D`), `nohup.out` and any screenshots.

- [ ] **Step 5: The documents**

In `CLAUDE.md`, replace:

```markdown
Part C (cluster access) is specified in `docs/specs/2026-10-04-phase-2c-cluster-design.md`, its plans (`phase-2c-1` to `phase-2c-3`) follow; nothing of it exists in the code yet. Part D (signals) is a later cycle.
```

with:

```markdown
Part C (cluster access, `docs/specs/2026-10-04-phase-2c-cluster-design.md`) is built in three plans: 2c-1 (the cluster client, its configuration, the run flag and the tool groups) is implemented; the read tools with the kind testbed (2c-2) and the actions (2c-3) follow, so there is no cluster tool yet. Part D (signals) is a later cycle.
```

In `CLAUDE.md`, replace:

```markdown
`REMEDY_LOG_LEVEL` (server, default `info`; `debug` logs every GitHub request). See the README quick start.
```

with:

```markdown
`REMEDY_LOG_LEVEL` (server, default `info`; `debug` logs every GitHub request). The cluster (server only, all optional): `REMEDY_K8S_READ_TOKEN_FILE` turns the read side on, `REMEDY_K8S_WRITE_TOKEN_FILE` with `REMEDY_K8S_WRITE_NAMESPACES` the actions, and `REMEDY_K8S_API`, `REMEDY_K8S_CA_FILE` and `REMEDY_K8S_ARGO_NAMESPACE` say where and how to reach it. See the README quick start.
```

In `CLAUDE.md`, replace:

```markdown
Agent-written text (arguments, notes) is untrusted.

## Hard rules
```

with:

```markdown
Agent-written text (arguments, notes) is untrusted.
- **Cluster credentials** (`internal/kube`) belong to the control plane alone: two token files (a read identity and a write identity), read again at every request because service account tokens rotate, and never in a log, an error, an API answer or a database column. The `Reader` is GET-only and the `Writer` has exactly four methods (restart a workload, delete a pod, refresh and sync an Argo CD application) and refuses a namespace outside the allowlist; the transports and tests enforce both. A new method needs a decision recorded in `docs/specs/2026-10-04-phase-2c-cluster-design.md`. Tools of the group `cluster` are offered only to runs started with `runs.cluster`, and a run that is not offered a tool gets the answer for a tool that does not exist.

## Hard rules
```

In `README.md`, replace:

```markdown
(see [`docs/research/spike-claude-billing.md`](docs/research/spike-claude-billing.md)).

### Container
```

with:

```markdown
(see [`docs/research/spike-claude-billing.md`](docs/research/spike-claude-billing.md)).

**Cluster access (optional, in progress).** The control plane can reach a Kubernetes cluster with two separate identities: a read-only
one (`REMEDY_K8S_READ_TOKEN_FILE`, a file with a service account token) and one for approved actions (`REMEDY_K8S_WRITE_TOKEN_FILE`, usable only in
the namespaces of `REMEDY_K8S_WRITE_NAMESPACES`). `REMEDY_K8S_API` (default: the in-cluster address), `REMEDY_K8S_CA_FILE` and `REMEDY_K8S_ARGO_NAMESPACE`
(default `argocd`) say where and how. With the read token set, the **Runs** page offers "Allow cluster tools". The tools themselves come with the
next plans ([`docs/specs/2026-10-04-phase-2c-cluster-design.md`](docs/specs/2026-10-04-phase-2c-cluster-design.md)); today only the configuration, the
clients and the switch exist.

### Container
```

- [ ] **Step 6: Check and commit**

Run: `make check`
Expected: everything passes.

```bash
git add web CLAUDE.md README.md
git commit -m "feat(web): a switch for cluster tools, and the documents for the cluster foundation" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```
