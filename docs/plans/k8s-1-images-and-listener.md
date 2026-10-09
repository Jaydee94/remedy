# K-1: Images, the Internal Listener, the Token Refresher and the CLI Installer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remedy can be built as two container images (the control plane and the runner); the control plane can serve the runner and gatekeeper routes on a second, internal port; a small binary mints the short-lived write token; and the runner image can install the pinned `claude` CLI by itself. Nothing is deployed yet: this plan makes the parts the chart of plan K-2 stands on.

**Architecture:** `internal/server` gets `NewSplit`, which returns two handlers (public: UI, `/api`, `/healthz`; internal: `/runner/v1`, `/mcp`, `/healthz`), driven by one new variable `REMEDY_INTERNAL_ADDR`. A new package `internal/tokenrefresh` and its command `cmd/remedy-tokenrefresh` call the TokenRequest API for one service account and patch one Secret. A new package `internal/cliinstall` and the subcommand `remedy-runner install-cli` download a pinned artifact over HTTPS and verify its SHA-256. Two Dockerfiles build the images; `make images` builds both.

**Tech Stack:** Go 1.27 stdlib only (no new dependency), Docker.

**Spec:** [`docs/specs/2026-10-06-kubernetes-deployment-design.md`](../specs/2026-10-06-kubernetes-deployment-design.md), sections 2 (D9, D12), 4, 6 and 8. The record of spike S4 ([`docs/research/k8s-s4-cli-install.md`](../research/k8s-s4-cli-install.md), plan K-0 task 1) fixes the base image of `Dockerfile.runner` and the form of the artifact `install-cli` downloads.

**Scope note:** No Kubernetes object is written here and no cluster is touched. `remedy-tokenrefresh` is tested against a fake API server only; plan K-3 runs it for real. The image workflow and the push to GHCR are plan K-5; here the images are built locally.

## Decisions made while planning

| Topic | Spec said | This plan |
|---|---|---|
| The platform flag of `install-cli` | `runner.cli.sha256` per architecture | One repeated flag `--platform GOARCH=NAME@SHA256`, because the vendor's name for a platform (`linux-x64`) is not Go's (`amd64`) and the installer must pick by `runtime.GOARCH` on its own. K-2 puts the same three facts in `values.yaml`. |
| Archive support | "native binary or the npm package" | The installer takes a raw binary (`--archive none`) or a `tar.gz` with one named member (`--member`). It does not run npm: the runner image has no Node. S4's record says which one applies; the code supports both because both are a few lines. |
| `internal` handler in a single-port setup | not mentioned | `NewSplit` is used only when `REMEDY_INTERNAL_ADDR` is set. Without it `server.New` serves everything on one port, as today, and no existing test or documented command changes. |
| `/mcp` on the public port | "stops serving" | The SPA fallback answers 404 for `/mcp` as it does for `/api/` and `/runner/`, so a request for it never returns the HTML shell. |
| Build context | not mentioned | A `.dockerignore` is added: there is none, and `COPY . .` would send `web/node_modules` and `.git` into every build. |
| Cross-compilation | images for amd64 and arm64 | The Dockerfiles compile on the build platform (`--platform=$BUILDPLATFORM`, `GOOS`/`GOARCH` from `TARGETOS`/`TARGETARCH`), so the arm64 build under QEMU does not run the Go compiler emulated. |

## Global Constraints

- Everything committed is English: docs, code, identifiers, comments, commit messages.
- No new Go dependencies. `remedy-tokenrefresh` and `install-cli` use the standard library only; no `client-go`, no `kubectl`.
- No token (the refresher's own, the minted one) and no credential appears in a log line, an error message or a test's output. Secrets are `secret.Value` while held.
- The refresher's transport sends exactly two requests: `POST` to the token path of the configured account and `PATCH` to the path of the configured Secret. It follows no redirect and uses no proxy.
- The installer accepts only `https` URLs (also after a redirect), writes nothing to its destination unless the SHA-256 matches, and never executes what it downloaded.
- `REMEDY_INTERNAL_ADDR` unset means the behaviour of today, byte for byte.
- A Docker image runs as uid 65532 and the runner image does not contain the `claude` CLI.
- Go tests: `go test ./... -race -count=1` and `make check` pass at the end of every task. Shell in tests runs on macOS and on Linux.
- Every commit message ends with the trailer `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`.

## Review Focus

- A request to the public port for a runner or gatekeeper route, with any method: must be 404 JSON, never the SPA's index and never a handler (task 3 pins `POST /runner/v1/claim`, `POST /mcp` and `GET /runner/v1/runs/x/snapshot`).
- `REMEDY_INTERNAL_ADDR` equal to the main port, without a port, or with a host and no port: refused at start with a message that names the variable (task 2).
- A refresher answer that carries the token inside an error or a log line, or an API server that redirects the refresher to another host: neither may leak the token or follow (task 4).
- A download whose checksum differs, whose URL is `http`, whose version contains `../` or whose archive lacks the member: the destination stays empty and the exit code is non-zero (task 5).
- An image that runs as root or that contains the proprietary binary (task 6 checks both on the built images).

## How to read the code blocks

A line `Create `path`:` or `Overwrite `path`:` is followed by the complete file. A line `In `path`, replace:` is followed by a block with the exact old text, a line `with:` and a block with the new text; the old text occurs exactly once in the file. Go code uses tabs.

## File Structure

| Path | Responsibility |
|---|---|
| `docs/design.md`, `README.md`, `CLAUDE.md` | §2.7 amended before any code; the container section; current state and commands |
| `internal/config/config.go` | `InternalAddr` (`REMEDY_INTERNAL_ADDR`) |
| `internal/server/server.go`, `internal/server/spa.go` | `New`, `NewSplit`, the route registration for two muxes; the SPA's 404 for `/mcp` |
| `internal/app/app.go`, `cmd/remedy-server/main.go` | `App.InternalHandler`; the second `http.Server` |
| `internal/tokenrefresh/tokenrefresh.go`, `cmd/remedy-tokenrefresh/main.go` | Mint a token for a service account, store it in a Secret |
| `internal/cliinstall/cliinstall.go`, `cmd/remedy-runner/installcli.go`, `cmd/remedy-runner/main.go` | Download, verify and install the pinned CLI |
| `Dockerfile`, `Dockerfile.runner`, `.dockerignore`, `Makefile` | The images and `make images` |

---

### Task 1: Amend the design document first

**Files:**
- Modify: `docs/design.md` (§2.7)
- Modify: `README.md` (the "Container" section)

The repository rule is that the design document changes before a decision does. This task is documentation only.

- [ ] **Step 1: Replace §2.7 of the design document**

In `docs/design.md`, replace:

```markdown
- GitHub Actions builds images to GHCR. Delivered as a Helm chart or Kustomize base in
  this repo.
```

with:

```markdown
- GitHub Actions builds the control plane image and the runner image to GHCR. Delivered as
  a **Helm chart** in `deploy/chart` (2026-10-06,
  [spec](specs/2026-10-06-kubernetes-deployment-design.md)).
- **Two identities in one pod.** The control plane pod runs as the read service account.
  A CronJob mints a short-lived token of the write service account into a Secret that the
  pod mounts as a file; if the job stops, the token expires and actions fail closed.
- **Two listeners.** The control plane serves the UI, `/api` and `/healthz` on the public
  port and `/runner/v1` and `/mcp` on an internal one (`REMEDY_INTERNAL_ADDR`), so an
  Ingress cannot expose the runner token or the run tokens.
- **The `claude` binary is in no image.** An init container of the runner pod installs the
  pinned, unmodified CLI from the official source and verifies its checksum, because the
  images are public and the binary is proprietary.
- A throwaway **dummy setup** in kind (`make dummy-up`, `make dummy-down`) runs the real
  chain with the real agent; the login lives in a host directory that survives it.
```

- [ ] **Step 2: Replace the last sentences of the README's container section**

In `README.md`, replace:

```markdown
or a directory writable by uid 65532. Only the control plane is containerised so far; the
runner needs the CLI login and runs on the host in this phase.
```

with:

```markdown
or a directory writable by uid 65532. The runner has its own image (`Dockerfile.runner`;
`make images` builds both). It does not contain the `claude` CLI: the Helm chart's init
container installs the pinned CLI, and the login is done once with `kubectl exec`. Until
the chart exists (plan K-2) the runner runs on the host.
```

- [ ] **Step 3: Commit**

```bash
git add docs/design.md README.md
git commit -m "docs: design 2.7 amended for the Kubernetes deployment (Helm, two identities, two listeners, the CLI installed at start)

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: `REMEDY_INTERNAL_ADDR`

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `config.Server.InternalAddr string`: empty means one port; otherwise a `host:port` or `:port` that differs from `Addr` in its port.

- [ ] **Step 1: Write the failing tests**

Append to `internal/config/config_test.go`:

```go
func TestServerFromEnvInternalAddr(t *testing.T) {
	c, err := config.ServerFromEnv(serverEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.InternalAddr != "" {
		t.Fatalf("the default must be one port, got InternalAddr %q", c.InternalAddr)
	}

	c, err = config.ServerFromEnv(serverEnv(map[string]string{"REMEDY_INTERNAL_ADDR": ":8081"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.InternalAddr != ":8081" {
		t.Fatalf("InternalAddr = %q", c.InternalAddr)
	}
}

func TestServerFromEnvRefusesABadInternalAddr(t *testing.T) {
	cases := map[string]map[string]string{
		"no port":                  {"REMEDY_INTERNAL_ADDR": "8081"},
		"a host without a port":    {"REMEDY_INTERNAL_ADDR": "localhost"},
		"an empty port":            {"REMEDY_INTERNAL_ADDR": "localhost:"},
		"the main port":            {"REMEDY_INTERNAL_ADDR": ":8080"},
		"the main port, other host": {"REMEDY_INTERNAL_ADDR": "127.0.0.1:9090", "REMEDY_ADDR": "0.0.0.0:9090"},
	}
	for name, override := range cases {
		_, err := config.ServerFromEnv(serverEnv(override))
		if err == nil {
			t.Errorf("%s: expected an error", name)
			continue
		}
		if !strings.Contains(err.Error(), "REMEDY_INTERNAL_ADDR") {
			t.Errorf("%s: the error must name the variable, got %q", name, err)
		}
	}
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./internal/config -run InternalAddr -v`
Expected: FAIL to compile with `c.InternalAddr undefined`.

- [ ] **Step 3: Implement**

In `internal/config/config.go`, add `"net"` to the imports (between `"fmt"` and `"net/url"`), and replace:

```go
	Addr                   string        // REMEDY_ADDR, default ":8080"
```

with:

```go
	Addr                   string        // REMEDY_ADDR, default ":8080"
	InternalAddr           string        // REMEDY_INTERNAL_ADDR, default empty: one port. Set, it serves /runner/v1 and /mcp and the main port does not
```

Then replace:

```go
	c.GitHubAPIURL = strings.TrimRight(c.GitHubAPIURL, "/")
```

with:

```go
	c.GitHubAPIURL = strings.TrimRight(c.GitHubAPIURL, "/")

	c.InternalAddr = get("REMEDY_INTERNAL_ADDR")
	if c.InternalAddr != "" {
		_, port, err := net.SplitHostPort(c.InternalAddr)
		if err != nil || port == "" {
			return Server{}, errors.New("REMEDY_INTERNAL_ADDR must be host:port or :port, for example :8081")
		}
		if _, mainPort, err := net.SplitHostPort(c.Addr); err == nil && mainPort == port {
			return Server{}, errors.New("REMEDY_INTERNAL_ADDR must use another port than REMEDY_ADDR")
		}
	}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/config -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): REMEDY_INTERNAL_ADDR, a second port for the runner and gatekeeper routes

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Two handlers, two listeners

**Files:**
- Overwrite: `internal/server/server.go`
- Modify: `internal/server/spa.go`
- Create: `internal/server/split_test.go`
- Modify: `internal/app/app.go`, `cmd/remedy-server/main.go`
- Test: `internal/app/split_test.go`

**Interfaces:**
- Consumes: `config.Server.InternalAddr` (task 2); `server.Deps`, unchanged.
- Produces: `server.NewSplit(d Deps) (public, internal http.Handler)`; `server.New(d Deps) http.Handler` is unchanged for callers; `app.App.InternalHandler http.Handler` (nil unless `InternalAddr` is set).

- [ ] **Step 1: Write the failing server tests**

Create `internal/server/split_test.go`:

```go
package server_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/store"
)

// splitServers starts the two handlers of NewSplit on two test servers: the public one with a UI, the internal one.
func splitServers(t *testing.T) (public, internal *httptest.Server) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	gate := gatekeeper.New(gatekeeper.Config{
		Store: st,
		Tools: gatekeeper.IncidentTools(st),
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	pub, in := server.NewSplit(server.Deps{
		Store: st, Auth: auth.New(password), RunnerToken: runnerToken, Gatekeeper: gate,
		Web: fstest.MapFS{"index.html": {Data: []byte("<html>shell</html>")}},
	})
	public, internal = httptest.NewServer(pub), httptest.NewServer(in)
	t.Cleanup(public.Close)
	t.Cleanup(internal.Close)
	return public, internal
}

func send(t *testing.T, method, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestThePublicHandlerServesTheUIAndTheAdminAPI(t *testing.T) {
	public, _ := splitServers(t)
	if code, _ := send(t, http.MethodGet, public.URL+"/healthz"); code != http.StatusOK {
		t.Fatalf("/healthz = %d", code)
	}
	if code, body := send(t, http.MethodGet, public.URL+"/"); code != http.StatusOK || !strings.Contains(body, "shell") {
		t.Fatalf("/ = %d %q", code, body)
	}
	if code, _ := send(t, http.MethodGet, public.URL+"/api/me"); code != http.StatusUnauthorized {
		t.Fatalf("/api/me without a session = %d, want 401", code)
	}
}

func TestThePublicHandlerDoesNotServeTheRunnerOrGatekeeperRoutes(t *testing.T) {
	public, _ := splitServers(t)
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/runner/v1/claim"},
		{http.MethodPost, "/runner/v1/runs/x/events"},
		{http.MethodGet, "/runner/v1/runs/x/snapshot"},
		{http.MethodPost, "/mcp"},
		{http.MethodGet, "/mcp"},
		{http.MethodPost, "/mcp/anything"},
	} {
		code, body := send(t, c.method, public.URL+c.path)
		if code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", c.method, c.path, code)
		}
		if strings.Contains(body, "shell") {
			t.Errorf("%s %s returned the UI's index", c.method, c.path)
		}
	}
}

func TestTheInternalHandlerServesOnlyTheRunnerAndGatekeeperRoutes(t *testing.T) {
	_, internal := splitServers(t)
	if code, _ := send(t, http.MethodGet, internal.URL+"/healthz"); code != http.StatusOK {
		t.Fatalf("/healthz = %d", code)
	}
	// The routes are there: they answer for a missing credential, not for a missing route.
	if code, _ := send(t, http.MethodPost, internal.URL+"/runner/v1/claim"); code != http.StatusUnauthorized {
		t.Fatalf("/runner/v1/claim without the token = %d, want 401", code)
	}
	if code, _ := send(t, http.MethodPost, internal.URL+"/mcp"); code != http.StatusUnauthorized {
		t.Fatalf("/mcp without a run token = %d, want 401", code)
	}
	for _, path := range []string{"/api/me", "/api/runs", "/api/approvals", "/"} {
		if code, _ := send(t, http.MethodGet, internal.URL+path); code != http.StatusNotFound {
			t.Errorf("GET %s on the internal port = %d, want 404", path, code)
		}
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/server -run 'Public|Internal' -v`
Expected: FAIL to compile with `undefined: server.NewSplit`.

- [ ] **Step 3: Rewrite `server.New` into a shared registration**

Overwrite `internal/server/server.go`:

```go
// Package server hosts the Remedy control plane HTTP surface: the admin API used by the UI
// and the runner API used by remedy-runner.
package server

import (
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"time"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/incident"
	"github.com/Jaydee94/remedy/internal/responder"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/store"
)

type Deps struct {
	Store       *store.Store
	Auth        *auth.Auth
	RunnerToken string
	Web         fs.FS // optional: the built UI, served for every non-API path

	// Key seals the GitHub token. NewGitHub builds a client for a token; when it is nil the GitHub
	// routes are not registered.
	Key       secret.Key
	NewGitHub func(token secret.Value) GitHub

	// Incidents changes incidents on behalf of the UI. When it is nil the incident routes are not
	// registered.
	Incidents *incident.Engine

	// Responder diagnoses incidents. When it is nil there are no diagnose, snapshot and limits routes.
	Responder    *responder.Responder
	PollInterval time.Duration // only shown by the limits endpoint

	// ActivityInterval is how often the activity stream looks for new entries. Zero means one second.
	ActivityInterval time.Duration

	// Gatekeeper serves the MCP tools of agents at /mcp and decides approvals. When it is nil there are neither, and
	// a run cannot be created with tools.
	Gatekeeper *gatekeeper.Gatekeeper

	// Cluster says what can be done in a cluster. The zero value is no cluster.
	Cluster Cluster
}

type srv struct {
	d   Deps
	hub *hub
}

// New returns one handler that serves everything: the UI, the admin API, the runner API and /mcp.
func New(d Deps) http.Handler {
	mux := http.NewServeMux()
	(&srv{d: d, hub: newHub()}).routes(mux, mux)
	return mux
}

// NewSplit returns two handlers for two ports. public serves the UI, /api and /healthz. internal serves /runner/v1 and
// /mcp, which only the runner and the agent CLI next to it call, and /healthz. An Ingress that points at the public
// port cannot reach the runner token's routes or the run tokens' route.
func NewSplit(d Deps) (public, internal http.Handler) {
	pub, in := http.NewServeMux(), http.NewServeMux()
	(&srv{d: d, hub: newHub()}).routes(pub, in)
	return pub, in
}

// routes registers every route. public gets the UI and the admin API, internal the runner API and /mcp. They may be
// the same mux: that is the single-port server.
func (s *srv) routes(public, internal *http.ServeMux) {
	d := s.d
	health := func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
	public.HandleFunc("GET /healthz", health)
	if internal != public {
		internal.HandleFunc("GET /healthz", health)
	}

	public.HandleFunc("POST /api/login", s.login)
	public.HandleFunc("POST /api/logout", s.session(s.logout))
	public.HandleFunc("GET /api/me", s.session(s.me))
	public.HandleFunc("POST /api/runs", s.session(s.createRun))
	public.HandleFunc("GET /api/capabilities", s.session(s.capabilities))
	public.HandleFunc("GET /api/runs", s.session(s.listRuns))
	public.HandleFunc("GET /api/runs/{id}", s.session(s.getRun))
	public.HandleFunc("GET /api/runs/{id}/events", s.session(s.streamEvents))
	public.HandleFunc("GET /api/runs/{id}/tool-calls", s.session(s.listToolCalls))
	public.HandleFunc("POST /api/runs/{id}/cancel", s.session(s.cancelRun))
	if d.Gatekeeper != nil {
		// The gatekeeper authenticates by the run token itself: this is neither an admin nor a runner route.
		internal.Handle("/mcp", d.Gatekeeper)
		public.HandleFunc("GET /api/approvals", s.session(s.listApprovals))
		public.HandleFunc("POST /api/approvals/{id}/approve", s.session(s.decide(true)))
		public.HandleFunc("POST /api/approvals/{id}/deny", s.session(s.decide(false)))
	}
	public.HandleFunc("GET /api/activity", s.session(s.listActivity))
	public.HandleFunc("GET /api/activity/stream", s.session(s.streamActivity))

	internal.HandleFunc("POST /runner/v1/claim", s.runner(s.claim))
	internal.HandleFunc("POST /runner/v1/runs/{id}/events", s.runner(s.postEvent))
	internal.HandleFunc("POST /runner/v1/runs/{id}/finish", s.runner(s.finish))
	internal.HandleFunc("POST /runner/v1/runs/{id}/heartbeat", s.runner(s.heartbeat))

	if d.NewGitHub != nil {
		public.HandleFunc("GET /api/github/connection", s.session(s.getConnection))
		public.HandleFunc("PUT /api/github/connection", s.session(s.putConnection))
		public.HandleFunc("POST /api/github/connection/check", s.session(s.checkConnection))
		public.HandleFunc("DELETE /api/github/connection", s.session(s.deleteConnection))
		public.HandleFunc("GET /api/repos", s.session(s.listRepos))
		public.HandleFunc("POST /api/repos", s.session(s.addRepo))
		public.HandleFunc("PATCH /api/repos/{id}", s.session(s.patchRepo))
		public.HandleFunc("DELETE /api/repos/{id}", s.session(s.deleteRepo))
	}

	if d.Incidents != nil {
		public.HandleFunc("GET /api/incidents", s.session(s.listIncidents))
		public.HandleFunc("GET /api/incidents/{id}", s.session(s.getIncident))
		public.HandleFunc("POST /api/incidents/{id}/ignore", s.session(s.ignoreIncident))
		public.HandleFunc("POST /api/incidents/{id}/unignore", s.session(s.unignoreIncident))
	}

	if d.Responder != nil {
		public.HandleFunc("POST /api/incidents/{id}/diagnose", s.session(s.diagnoseIncident))
		public.HandleFunc("GET /api/limits", s.session(s.getLimits))
		internal.HandleFunc("GET /runner/v1/runs/{id}/snapshot", s.runner(s.snapshot))
	}

	if d.Web != nil {
		public.Handle("/", spa(d.Web))
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// clientIP is the TCP peer address. Proxy headers are deliberately not trusted.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
```

In `internal/server/spa.go`, replace:

```go
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/runner/") {
```

with:

```go
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/runner/") ||
			r.URL.Path == "/mcp" || strings.HasPrefix(r.URL.Path, "/mcp/") {
```

- [ ] **Step 4: Run all server tests**

Run: `go test ./internal/server -count=1 -race`
Expected: PASS, including the three new tests and every existing one (the single-port `New` is unchanged in behaviour).

- [ ] **Step 5: Write the failing app test**

Create `internal/app/split_test.go`:

```go
package app_test

import (
	"bytes"
	"encoding/base64"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/Jaydee94/remedy/internal/app"
	"github.com/Jaydee94/remedy/internal/config"
	"github.com/Jaydee94/remedy/internal/store"
)

func appFor(t *testing.T, extra map[string]string) *app.App {
	t.Helper()
	env := map[string]string{
		"REMEDY_ADMIN_PASSWORD": password,
		"REMEDY_RUNNER_TOKEN":   runnerToken,
		"REMEDY_MASTER_KEY":     secret32(),
	}
	for k, v := range extra {
		env[k] = v
	}
	cfg, err := config.ServerFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return app.New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
}

func TestWithoutAnInternalAddrThereIsOneHandler(t *testing.T) {
	if a := appFor(t, nil); a.InternalHandler != nil {
		t.Fatal("InternalHandler must be nil when REMEDY_INTERNAL_ADDR is not set")
	}
}

func TestWithAnInternalAddrThereAreTwoHandlers(t *testing.T) {
	if a := appFor(t, map[string]string{"REMEDY_INTERNAL_ADDR": ":8081"}); a.InternalHandler == nil {
		t.Fatal("InternalHandler must be set when REMEDY_INTERNAL_ADDR is set")
	}
}
```

The test needs `secret32()`; add it to the same file:

```go
// secret32 is a valid REMEDY_MASTER_KEY: 32 bytes in Base64.
func secret32() string { return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)) }
```

- [ ] **Step 6: Run it to see it fail**

Run: `go test ./internal/app -run 'InternalAddr|OneHandler|TwoHandlers' -v`
Expected: FAIL to compile with `a.InternalHandler undefined`.

- [ ] **Step 7: Wire the app and the binary**

In `internal/app/app.go`, replace:

```go
type App struct {
	Handler    http.Handler
```

with:

```go
type App struct {
	// Handler serves the UI and the admin API; when no internal address is configured it serves everything.
	Handler http.Handler
	// InternalHandler serves /runner/v1 and /mcp and is non-nil only when REMEDY_INTERNAL_ADDR is set.
	InternalHandler http.Handler

```

and replace the whole `return &App{ ... }` block's `Handler: server.New(server.Deps{` part. First replace:

```go
	return &App{
		Responder:  diagnoser,
```

with:

```go
	deps := server.Deps{
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
	}
	var handler, internalHandler http.Handler
	if cfg.InternalAddr != "" {
		handler, internalHandler = server.NewSplit(deps)
	} else {
		handler = server.New(deps)
	}

	return &App{
		Handler:         handler,
		InternalHandler: internalHandler,
		Responder:       diagnoser,
```

and replace the old handler block at the end:

```go
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
```

with:

```go
	}
}
```

Run `gofmt -w internal/app/app.go` afterwards (the struct literal's alignment changes).

In `cmd/remedy-server/main.go`, replace:

```go
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
```

with:

```go
	var internal *http.Server
	if a.InternalHandler != nil {
		internal = &http.Server{
			Addr:              cfg.InternalAddr,
			Handler:           a.InternalHandler,
			ReadHeaderTimeout: 10 * time.Second,
		}
		go func() {
			log.Info("internal listener", "addr", cfg.InternalAddr)
			if err := internal.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("internal listener failed", "err", err)
				os.Exit(1)
			}
		}()
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		if internal != nil {
			_ = internal.Shutdown(shutdownCtx)
		}
	}()
```

- [ ] **Step 8: Run everything, then try the real binary**

Run: `gofmt -l . ; go vet ./... && go test ./... -race -count=1`
Expected: no output from `gofmt -l`, all tests PASS.

Then:

```sh
go build -o /tmp/remedy-server-split ./cmd/remedy-server
cd "$(mktemp -d)" && REMEDY_ADMIN_PASSWORD=twelve-chars-pw REMEDY_RUNNER_TOKEN=a-runner-token-of-24-chars-or-more REMEDY_MASTER_KEY=$(openssl rand -base64 32) REMEDY_ADDR=127.0.0.1:18080 REMEDY_INTERNAL_ADDR=127.0.0.1:18081 /tmp/remedy-server-split &
sleep 1
curl -s -o /dev/null -w 'public /healthz: %{http_code}\n' http://127.0.0.1:18080/healthz
curl -s -o /dev/null -w 'public runner route: %{http_code}\n' -X POST http://127.0.0.1:18080/runner/v1/claim
curl -s -o /dev/null -w 'public mcp: %{http_code}\n' -X POST http://127.0.0.1:18080/mcp
curl -s -o /dev/null -w 'internal runner route (no token): %{http_code}\n' -X POST http://127.0.0.1:18081/runner/v1/claim
curl -s -o /dev/null -w 'internal admin route: %{http_code}\n' http://127.0.0.1:18081/api/me
kill %1
```

Expected: `200`, `404`, `404`, `401`, `404`.

- [ ] **Step 9: Commit**

```bash
git add internal/server internal/app cmd/remedy-server
git commit -m "feat(server): a second, internal listener for the runner and gatekeeper routes

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 4: `remedy-tokenrefresh`

**Files:**
- Create: `internal/tokenrefresh/tokenrefresh.go`
- Create: `cmd/remedy-tokenrefresh/main.go`
- Test: `internal/tokenrefresh/tokenrefresh_test.go`, `internal/tokenrefresh/guard_test.go`

**Interfaces:**
- Consumes: `kube.DefaultAPI`, `kube.DefaultCAFile`, `kube.ValidNamespace` (`internal/kube/config.go`); `secret.NewValue`, `Value.Reveal`.
- Produces: `tokenrefresh.Config{API, CAFile, TokenFile, Namespace, Account, Secret, Key string; Lifetime time.Duration}`, `tokenrefresh.Run(ctx context.Context, c Config, log *slog.Logger) error`, `tokenrefresh.DefaultTokenFile`. The command's flags are the fields in lower-case kebab form.

- [ ] **Step 1: Write the failing tests**

Create `internal/tokenrefresh/tokenrefresh_test.go`:

```go
package tokenrefresh_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/tokenrefresh"
)

const (
	ownToken    = "refresher-own-token-0123456789"
	mintedToken = "minted-write-token-abcdefghijkl"
)

type recorded struct {
	Method, Path, Auth, ContentType string
	Body                            []byte
}

// fakeAPI answers the two requests of the refresher and records them.
type fakeAPI struct {
	mu                      sync.Mutex
	reqs                    []recorded
	mintStatus, patchStatus int // 0 means success
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.reqs = append(f.reqs, recorded{r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Content-Type"), body})
	f.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/namespaces/remedy-system/serviceaccounts/remedy-write/token":
		if f.mintStatus != 0 {
			w.WriteHeader(f.mintStatus)
			fmt.Fprint(w, `{"kind":"Status","message":"serviceaccounts \"remedy-write\" is forbidden"}`)
			return
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"status":{"token":%q,"expirationTimestamp":"2026-10-07T12:00:00Z"}}`, mintedToken)
	case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/namespaces/remedy-system/secrets/remedy-write-token":
		if f.patchStatus != 0 {
			w.WriteHeader(f.patchStatus)
			fmt.Fprint(w, `{"kind":"Status","message":"secrets \"remedy-write-token\" not found"}`)
			return
		}
		fmt.Fprint(w, `{}`)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeAPI) requests() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.reqs...)
}

// setup starts the fake API and returns a valid configuration for it and the buffer the logs go to.
func setup(t *testing.T, f *fakeAPI) (tokenrefresh.Config, *bytes.Buffer) {
	t.Helper()
	ts := httptest.NewServer(f)
	t.Cleanup(ts.Close)
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte(ownToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return tokenrefresh.Config{
		API: ts.URL, TokenFile: tokenFile,
		Namespace: "remedy-system", Account: "remedy-write", Secret: "remedy-write-token", Key: "token",
		Lifetime: 2 * time.Hour,
	}, &bytes.Buffer{}
}

func logTo(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestRunMintsATokenAndStoresIt(t *testing.T) {
	f := &fakeAPI{}
	cfg, logs := setup(t, f)
	if err := tokenrefresh.Run(context.Background(), cfg, logTo(logs)); err != nil {
		t.Fatal(err)
	}
	reqs := f.requests()
	if len(reqs) != 2 {
		t.Fatalf("%d requests, want 2: %+v", len(reqs), reqs)
	}
	mint, patch := reqs[0], reqs[1]
	if mint.Auth != "Bearer "+ownToken || patch.Auth != "Bearer "+ownToken {
		t.Fatalf("both requests are made with the refresher's own token, got %q and %q", mint.Auth, patch.Auth)
	}
	var tr struct {
		Kind string `json:"kind"`
		Spec struct {
			ExpirationSeconds int64 `json:"expirationSeconds"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(mint.Body, &tr); err != nil || tr.Kind != "TokenRequest" || tr.Spec.ExpirationSeconds != 7200 {
		t.Fatalf("token request = %s (%v)", mint.Body, err)
	}
	if patch.ContentType != "application/merge-patch+json" {
		t.Fatalf("patch content type = %q", patch.ContentType)
	}
	var p struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(patch.Body, &p); err != nil {
		t.Fatal(err)
	}
	if got, _ := base64.StdEncoding.DecodeString(p.Data["token"]); string(got) != mintedToken || len(p.Data) != 1 {
		t.Fatalf("patch data = %v, want only token=%q", p.Data, mintedToken)
	}
}

func TestRunNeverLogsOrReturnsATokenInText(t *testing.T) {
	f := &fakeAPI{patchStatus: http.StatusNotFound}
	cfg, logs := setup(t, f)
	err := tokenrefresh.Run(context.Background(), cfg, logTo(logs))
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, text := range []string{logs.String(), err.Error()} {
		if strings.Contains(text, mintedToken) || strings.Contains(text, ownToken) {
			t.Fatalf("a token is in %q", text)
		}
	}
}

func TestRunDoesNotStoreAnythingWhenMintingFails(t *testing.T) {
	f := &fakeAPI{mintStatus: http.StatusForbidden}
	cfg, _ := setup(t, f)
	err := tokenrefresh.Run(context.Background(), cfg, logTo(&bytes.Buffer{}))
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v, want one that says 403", err)
	}
	if reqs := f.requests(); len(reqs) != 1 {
		t.Fatalf("%d requests, want only the token request", len(reqs))
	}
}

func TestRunSaysWhenTheSecretIsMissing(t *testing.T) {
	f := &fakeAPI{patchStatus: http.StatusNotFound}
	cfg, _ := setup(t, f)
	err := tokenrefresh.Run(context.Background(), cfg, logTo(&bytes.Buffer{}))
	if err == nil || !strings.Contains(err.Error(), `"remedy-write-token"`) || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("err = %v, want one that names the missing Secret", err)
	}
}

func TestRunRefusesAConfigurationItCannotUse(t *testing.T) {
	cases := map[string]func(*tokenrefresh.Config){
		"a lifetime under ten minutes":  func(c *tokenrefresh.Config) { c.Lifetime = 5 * time.Minute },
		"a lifetime over a day":         func(c *tokenrefresh.Config) { c.Lifetime = 48 * time.Hour },
		"no namespace":                  func(c *tokenrefresh.Config) { c.Namespace = "" },
		"a namespace with a slash":      func(c *tokenrefresh.Config) { c.Namespace = "a/b" },
		"an account with a path":        func(c *tokenrefresh.Config) { c.Account = "../kube-system" },
		"a Secret with upper case":      func(c *tokenrefresh.Config) { c.Secret = "Remedy" },
		"a key with a slash":            func(c *tokenrefresh.Config) { c.Key = "a/b" },
		"an API that is not an URL":     func(c *tokenrefresh.Config) { c.API = "not a url" },
		"a CA file that does not exist": func(c *tokenrefresh.Config) { c.CAFile = "/no/such/ca.crt" },
	}
	for name, change := range cases {
		f := &fakeAPI{}
		cfg, _ := setup(t, f)
		change(&cfg)
		if err := tokenrefresh.Run(context.Background(), cfg, logTo(&bytes.Buffer{})); err == nil {
			t.Errorf("%s: expected an error", name)
		}
		if n := len(f.requests()); n != 0 {
			t.Errorf("%s: %d requests were sent, want none", name, n)
		}
	}
}

func TestRunNeedsItsOwnTokenFile(t *testing.T) {
	f := &fakeAPI{}
	cfg, _ := setup(t, f)
	for name, content := range map[string]*string{"missing": nil, "empty": ptr(""), "two lines": ptr("a\nb\n")} {
		if content == nil {
			cfg.TokenFile = filepath.Join(t.TempDir(), "nothing")
		} else if err := os.WriteFile(cfg.TokenFile, []byte(*content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := tokenrefresh.Run(context.Background(), cfg, logTo(&bytes.Buffer{})); err == nil {
			t.Errorf("%s token file: expected an error", name)
		}
	}
	if n := len(f.requests()); n != 0 {
		t.Fatalf("%d requests were sent without a token", n)
	}
}

func ptr(s string) *string { return &s }

func TestRunDoesNotFollowARedirect(t *testing.T) {
	elsewhere := &fakeAPI{}
	other := httptest.NewServer(elsewhere)
	t.Cleanup(other.Close)
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirecting.Close)

	cfg, _ := setup(t, &fakeAPI{})
	cfg.API = redirecting.URL
	if err := tokenrefresh.Run(context.Background(), cfg, logTo(&bytes.Buffer{})); err == nil {
		t.Fatal("expected an error for a redirect")
	}
	if n := len(elsewhere.requests()); n != 0 {
		t.Fatalf("the refresher followed a redirect: %d requests reached the other host (its token went with them)", n)
	}
}
```

Create `internal/tokenrefresh/guard_test.go`:

```go
package tokenrefresh

import (
	"net/http"
	"testing"
)

func TestTheGuardAllowsExactlyTwoRequests(t *testing.T) {
	g := &guard{mintPath: "/api/v1/namespaces/n/serviceaccounts/a/token", secretPath: "/api/v1/namespaces/n/secrets/s"}
	allowed := map[string]bool{
		"POST /api/v1/namespaces/n/serviceaccounts/a/token": true,
		"PATCH /api/v1/namespaces/n/secrets/s":              true,
	}
	for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"} {
		for _, path := range []string{
			"/api/v1/namespaces/n/serviceaccounts/a/token",
			"/api/v1/namespaces/n/secrets/s",
			"/api/v1/namespaces/n/secrets/other",
			"/api/v1/namespaces/other/secrets/s",
			"/api/v1/namespaces/n/serviceaccounts/a",
			"/api/v1/namespaces/n/secrets",
			"/api/v1/secrets",
		} {
			if got, want := g.allows(method, path), allowed[method+" "+path]; got != want {
				t.Errorf("%s %s: allows = %v, want %v", method, path, got, want)
			}
		}
	}
	// And the transport itself refuses before anything is sent.
	req, _ := http.NewRequest(http.MethodDelete, "http://127.0.0.1:1/api/v1/namespaces/n/secrets/s", nil)
	if _, err := g.RoundTrip(req); err == nil {
		t.Fatal("RoundTrip must refuse a request that is not allowed")
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/tokenrefresh -v`
Expected: FAIL to compile (`package ... is not in std` or `undefined: tokenrefresh.Run`).

- [ ] **Step 3: Implement**

Create `internal/tokenrefresh/tokenrefresh.go`:

```go
// Package tokenrefresh mints a short-lived token for one service account and stores it in one Secret. The control
// plane's write identity is such a token: a pod gets only the token of its own account, so a CronJob that runs this
// keeps a second account's token fresh in a Secret the pod mounts as a file. If the job stops, the token expires and
// the actions fail closed.
//
// The client is as small as it can be: two requests, to two fixed paths, with the refresher's own token, which is read
// from its file for every request. It follows no redirect and uses no proxy: a token must not be sent anywhere the
// configuration did not name. Neither token is ever written to a log line or an error.
package tokenrefresh

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/kube"
	"github.com/Jaydee94/remedy/internal/secret"
)

const (
	// DefaultTokenFile is the refresher's own service account token inside its pod.
	DefaultTokenFile = "/var/run/secrets/kubernetes.io/serviceaccount/token"

	// MinLifetime is the least the API server grants a token request. MaxLifetime keeps a stored token short.
	MinLifetime = 10 * time.Minute
	MaxLifetime = 24 * time.Hour

	maxBody        = 1 << 20
	maxMessage     = 300
	requestTimeout = 15 * time.Second
)

var (
	nameRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?$`)
	keyRE  = regexp.MustCompile(`^[-._a-zA-Z0-9]{1,253}$`)
)

// Config says what to mint and where to store it.
type Config struct {
	API       string        // the API server, default kube.DefaultAPI
	CAFile    string        // its CA; empty means the pod's own CA when that file exists, else the system's roots
	TokenFile string        // the refresher's own token, default DefaultTokenFile
	Namespace string        // the namespace of the account and of the Secret
	Account   string        // the service account to mint a token for
	Secret    string        // the Secret to store it in; it must exist
	Key       string        // the key in the Secret's data
	Lifetime  time.Duration // MinLifetime to MaxLifetime
}

func (c Config) api() string {
	if c.API == "" {
		return kube.DefaultAPI
	}
	return strings.TrimRight(c.API, "/")
}

func (c Config) tokenFile() string {
	if c.TokenFile == "" {
		return DefaultTokenFile
	}
	return c.TokenFile
}

func (c Config) validate() error {
	if !kube.ValidNamespace(c.Namespace) {
		return fmt.Errorf("the namespace %q is not a namespace name", c.Namespace)
	}
	if !nameRE.MatchString(c.Account) {
		return fmt.Errorf("the service account %q is not a name", c.Account)
	}
	if !nameRE.MatchString(c.Secret) {
		return fmt.Errorf("the Secret %q is not a name", c.Secret)
	}
	if !keyRE.MatchString(c.Key) {
		return fmt.Errorf("the key %q is not a Secret key", c.Key)
	}
	if c.Lifetime < MinLifetime || c.Lifetime > MaxLifetime {
		return fmt.Errorf("the lifetime must be between %s and %s", MinLifetime, MaxLifetime)
	}
	if u, err := url.Parse(c.api()); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("the API must be an http or https URL")
	}
	return nil
}

// guard is the client's transport: whatever calls it, only the two requests of the refresher leave the process.
type guard struct {
	base                   http.RoundTripper
	mintPath, secretPath   string
}

func (g *guard) allows(method, path string) bool {
	return (method == http.MethodPost && path == g.mintPath) || (method == http.MethodPatch && path == g.secretPath)
}

func (g *guard) RoundTrip(req *http.Request) (*http.Response, error) {
	if !g.allows(req.Method, req.URL.Path) {
		return nil, fmt.Errorf("tokenrefresh: refusing to send %s %s", req.Method, req.URL.Path)
	}
	return g.base.RoundTrip(req)
}

type client struct {
	base       string
	tokenFile  string
	mintPath   string
	secretPath string
	http       *http.Client
}

func newClient(c Config) (*client, error) {
	pool, err := loadPool(c.CAFile)
	if err != nil {
		return nil, err
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil // straight to the API server, whatever the environment says
	tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	mint := "/api/v1/namespaces/" + c.Namespace + "/serviceaccounts/" + c.Account + "/token"
	store := "/api/v1/namespaces/" + c.Namespace + "/secrets/" + c.Secret
	return &client{
		base: c.api(), tokenFile: c.tokenFile(), mintPath: mint, secretPath: store,
		http: &http.Client{
			Timeout:   requestTimeout,
			Transport: &guard{base: tr, mintPath: mint, secretPath: store},
			// An answer that redirects is an answer, not an instruction to send the token elsewhere.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// loadPool reads the CA. With no path it uses the pod's service account CA when that file exists, otherwise nil,
// which means the system's roots.
func loadPool(path string) (*x509.CertPool, error) {
	if path == "" {
		if _, err := os.Stat(kube.DefaultCAFile); err != nil {
			return nil, nil
		}
		path = kube.DefaultCAFile
	}
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("the CA file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("the CA file %s holds no certificate", path)
	}
	return pool, nil
}

// readToken reads the refresher's own token. It is called for every request.
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

// do sends one request as the refresher and returns the body of a 2xx answer. A failure becomes an error whose text
// carries the status and the API server's own message, never a token.
func (cl *client) do(ctx context.Context, method, path, contentType string, body []byte) ([]byte, error) {
	own, err := readToken(cl.tokenFile)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, cl.base+path, bytes.NewReader(body))
	if err != nil {
		return nil, scrubbed(err, own)
	}
	req.Header.Set("Authorization", "Bearer "+own.Reveal())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", contentType)
	resp, err := cl.http.Do(req)
	if err != nil {
		return nil, scrubbed(err, own)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, scrubbed(err, own)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, apiMessage(raw))
	}
	return raw, nil
}

// scrubbed returns an error with the token taken out of its text.
func scrubbed(err error, tokens ...secret.Value) error {
	msg := err.Error()
	for _, t := range tokens {
		if v := t.Reveal(); v != "" {
			msg = strings.ReplaceAll(msg, v, "***")
		}
	}
	return errors.New(msg)
}

// apiMessage is the message of an API server's Status answer, shortened, or a fixed text when there is none.
func apiMessage(raw []byte) string {
	var s struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &s) != nil || s.Message == "" {
		return "no message"
	}
	if len(s.Message) > maxMessage {
		return s.Message[:maxMessage] + "..."
	}
	return s.Message
}

// Run mints a token for the account and stores it in the Secret. It returns an error for anything that went wrong; the
// token is never in it.
func Run(ctx context.Context, c Config, log *slog.Logger) error {
	if err := c.validate(); err != nil {
		return err
	}
	cl, err := newClient(c)
	if err != nil {
		return err
	}

	request, err := json.Marshal(map[string]any{
		"apiVersion": "authentication.k8s.io/v1",
		"kind":       "TokenRequest",
		"spec":       map[string]any{"expirationSeconds": int64(c.Lifetime.Seconds())},
	})
	if err != nil {
		return err
	}
	raw, err := cl.do(ctx, http.MethodPost, cl.mintPath, "application/json", request)
	if err != nil {
		return fmt.Errorf("cannot mint a token for %s: %w", c.Account, err)
	}
	var answer struct {
		Status struct {
			Token               string `json:"token"`
			ExpirationTimestamp string `json:"expirationTimestamp"`
		} `json:"status"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil || answer.Status.Token == "" {
		return fmt.Errorf("the token request for %s was answered without a token", c.Account)
	}
	minted := secret.NewValue(answer.Status.Token)

	patch, err := json.Marshal(map[string]any{
		"data": map[string]string{c.Key: base64.StdEncoding.EncodeToString([]byte(minted.Reveal()))},
	})
	if err != nil {
		return scrubbed(err, minted)
	}
	if _, err := cl.do(ctx, http.MethodPatch, cl.secretPath, "application/merge-patch+json", patch); err != nil {
		if strings.Contains(err.Error(), "HTTP 404") {
			return fmt.Errorf("the Secret %q does not exist in %s: the chart creates it (%w)", c.Secret, c.Namespace, scrubbed(err, minted))
		}
		return fmt.Errorf("cannot store the token in the Secret %q: %w", c.Secret, scrubbed(err, minted))
	}
	log.Info("stored a new token", "account", c.Account, "secret", c.Secret, "key", c.Key, "expires", answer.Status.ExpirationTimestamp)
	return nil
}
```

Run `gofmt -w internal/tokenrefresh` (the `guard` struct's field alignment).

Create `cmd/remedy-tokenrefresh/main.go`:

```go
// Command remedy-tokenrefresh mints a short-lived token for one service account and stores it in one Secret. The Helm
// chart runs it as a CronJob for the control plane's write identity. See internal/tokenrefresh.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/Jaydee94/remedy/internal/kube"
	"github.com/Jaydee94/remedy/internal/tokenrefresh"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	var c tokenrefresh.Config
	flag.StringVar(&c.API, "api", envOr("REMEDY_K8S_API", kube.DefaultAPI), "the Kubernetes API server")
	flag.StringVar(&c.CAFile, "ca-file", os.Getenv("REMEDY_K8S_CA_FILE"), "its CA (default: the pod's own)")
	flag.StringVar(&c.TokenFile, "token-file", tokenrefresh.DefaultTokenFile, "the refresher's own service account token")
	flag.StringVar(&c.Namespace, "namespace", "", "the namespace of the account and the Secret (required)")
	flag.StringVar(&c.Account, "account", "remedy-write", "the service account to mint a token for")
	flag.StringVar(&c.Secret, "secret", "remedy-write-token", "the Secret to store the token in; it must exist")
	flag.StringVar(&c.Key, "key", "token", "the key in the Secret's data")
	flag.DurationVar(&c.Lifetime, "lifetime", 2*time.Hour, "how long the token is valid (10m to 24h)")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := tokenrefresh.Run(ctx, c, log); err != nil {
		log.Error("the token was not refreshed", "err", err)
		os.Exit(1)
	}
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}
```

- [ ] **Step 4: Run the tests**

Run: `gofmt -l . ; go vet ./... && go test ./internal/tokenrefresh -race -count=1 -v`
Expected: no `gofmt` output; all tests PASS, including `TestRunDoesNotFollowARedirect` and `TestTheGuardAllowsExactlyTwoRequests`.

- [ ] **Step 5: Check the binary's usage**

Run: `go run ./cmd/remedy-tokenrefresh -h`
Expected: usage with the eight flags; exit status 0. Run `go run ./cmd/remedy-tokenrefresh; echo "exit=$?"` and expect the error `the namespace "" is not a namespace name` and `exit=1`.

- [ ] **Step 6: Commit**

```bash
git add internal/tokenrefresh cmd/remedy-tokenrefresh
git commit -m "feat: remedy-tokenrefresh, a short-lived write token for the control plane

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 5: `remedy-runner install-cli`

**Files:**
- Create: `internal/cliinstall/cliinstall.go`
- Create: `cmd/remedy-runner/installcli.go`
- Modify: `cmd/remedy-runner/main.go`
- Test: `internal/cliinstall/cliinstall_test.go`, `cmd/remedy-runner/installcli_test.go`

**Interfaces:**
- Produces: `cliinstall.Platform{Name, SHA256 string}`, `cliinstall.Options{URLTemplate, Version string; Platforms map[string]Platform; GOARCH, Dest, Name, Archive, Member string; HTTP *http.Client}`, `cliinstall.Install(ctx context.Context, o Options) (string, error)` (the path of the installed file). The command: `remedy-runner install-cli --version V --url-template T --platform GOARCH=NAME@SHA256 [--platform ...] [--dest /opt/claude] [--name claude] [--archive none|tar.gz] [--member FILE]`.

S4's record decides `--archive`, `--member` and the template; the code supports both forms and does not depend on the answer.

- [ ] **Step 1: Write the failing tests for the package**

Create `internal/cliinstall/cliinstall_test.go`:

```go
package cliinstall_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/cliinstall"
)

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func tarGz(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, data := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// serve starts an https server with the files and returns options that point at it.
func serve(t *testing.T, files map[string][]byte) cliinstall.Options {
	t.Helper()
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(ts.Close)
	return cliinstall.Options{
		URLTemplate: ts.URL + "/{version}/{platform}/claude",
		Version:     "1.2.3",
		GOARCH:      "arm64",
		Dest:        t.TempDir(),
		Name:        "claude",
		Archive:     "none",
		HTTP:        ts.Client(),
	}
}

func installed(t *testing.T, dest string) []string {
	t.Helper()
	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestInstallDownloadsVerifiesAndInstallsARawBinary(t *testing.T) {
	binary := []byte("#!/bin/sh\necho claude 1.2.3\n")
	o := serve(t, map[string][]byte{"/1.2.3/linux-arm64/claude": binary})
	o.Platforms = map[string]cliinstall.Platform{
		"amd64": {Name: "linux-x64", SHA256: strings.Repeat("0", 64)},
		"arm64": {Name: "linux-arm64", SHA256: sum(binary)},
	}
	path, err := cliinstall.Install(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(o.Dest, "claude") {
		t.Fatalf("path = %q", path)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, binary) {
		t.Fatalf("installed content = %q (%v)", got, err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v, want 0755", info.Mode().Perm())
	}
	if names := installed(t, o.Dest); len(names) != 1 {
		t.Fatalf("the directory holds %v, want only claude", names)
	}
}

func TestInstallPicksThePlatformOfTheArchitecture(t *testing.T) {
	x64, arm := []byte("x64 binary"), []byte("arm binary")
	o := serve(t, map[string][]byte{"/1.2.3/linux-x64/claude": x64, "/1.2.3/linux-arm64/claude": arm})
	o.Platforms = map[string]cliinstall.Platform{
		"amd64": {Name: "linux-x64", SHA256: sum(x64)},
		"arm64": {Name: "linux-arm64", SHA256: sum(arm)},
	}
	o.GOARCH = "amd64"
	path, err := cliinstall.Install(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, x64) {
		t.Fatalf("installed %q on amd64", got)
	}
}

func TestInstallLeavesNothingWhenTheChecksumDiffers(t *testing.T) {
	o := serve(t, map[string][]byte{"/1.2.3/linux-arm64/claude": []byte("tampered")})
	o.Platforms = map[string]cliinstall.Platform{"arm64": {Name: "linux-arm64", SHA256: sum([]byte("the real one"))}}
	_, err := cliinstall.Install(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("err = %v, want a checksum error", err)
	}
	if names := installed(t, o.Dest); len(names) != 0 {
		t.Fatalf("the directory holds %v after a failed install, want nothing", names)
	}
}

func TestInstallTakesTheMemberOutOfATarGz(t *testing.T) {
	binary := []byte("the cli")
	archive := tarGz(t, map[string][]byte{"package/README": []byte("read me"), "package/bin/claude": binary})
	o := serve(t, map[string][]byte{"/1.2.3/linux-arm64/claude": archive})
	o.Archive, o.Member = "tar.gz", "claude"
	o.Platforms = map[string]cliinstall.Platform{"arm64": {Name: "linux-arm64", SHA256: sum(archive)}}
	path, err := cliinstall.Install(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, binary) {
		t.Fatalf("installed %q", got)
	}
	if names := installed(t, o.Dest); len(names) != 1 {
		t.Fatalf("the directory holds %v, want only claude", names)
	}
}

func TestInstallFailsWhenTheArchiveHasNoSuchMember(t *testing.T) {
	archive := tarGz(t, map[string][]byte{"package/other": []byte("x")})
	o := serve(t, map[string][]byte{"/1.2.3/linux-arm64/claude": archive})
	o.Archive, o.Member = "tar.gz", "claude"
	o.Platforms = map[string]cliinstall.Platform{"arm64": {Name: "linux-arm64", SHA256: sum(archive)}}
	if _, err := cliinstall.Install(context.Background(), o); err == nil {
		t.Fatal("expected an error")
	}
	if names := installed(t, o.Dest); len(names) != 0 {
		t.Fatalf("the directory holds %v, want nothing", names)
	}
}

func TestInstallRefusesWhatItCannotTrust(t *testing.T) {
	binary := []byte("x")
	good := func() cliinstall.Options {
		o := serve(t, map[string][]byte{"/1.2.3/linux-arm64/claude": binary})
		o.Platforms = map[string]cliinstall.Platform{"arm64": {Name: "linux-arm64", SHA256: sum(binary)}}
		return o
	}
	plain := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(plain.Close)

	cases := map[string]func(*cliinstall.Options){
		"an http URL":             func(o *cliinstall.Options) { o.URLTemplate = plain.URL + "/{version}/{platform}" },
		"a version with a path":   func(o *cliinstall.Options) { o.Version = "../1.2.3" },
		"an empty version":        func(o *cliinstall.Options) { o.Version = "" },
		"an architecture unknown": func(o *cliinstall.Options) { o.GOARCH = "riscv64" },
		"a checksum too short":    func(o *cliinstall.Options) { o.Platforms["arm64"] = cliinstall.Platform{Name: "linux-arm64", SHA256: "abc"} },
		"a name with a slash":     func(o *cliinstall.Options) { o.Name = "../claude" },
		"an unknown archive":      func(o *cliinstall.Options) { o.Archive = "zip" },
		"a tar.gz without member": func(o *cliinstall.Options) { o.Archive, o.Member = "tar.gz", "" },
		"an unknown file":         func(o *cliinstall.Options) { o.Version = "9.9.9" },
	}
	for name, change := range cases {
		o := good()
		change(&o)
		if _, err := cliinstall.Install(context.Background(), o); err == nil {
			t.Errorf("%s: expected an error", name)
		}
		if names := installed(t, o.Dest); len(names) != 0 {
			t.Errorf("%s: the directory holds %v, want nothing", name, names)
		}
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/cliinstall -v`
Expected: FAIL to compile (`no non-test Go files` or `undefined: cliinstall.Install`).

- [ ] **Step 3: Implement the package**

Create `internal/cliinstall/cliinstall.go`:

```go
// Package cliinstall installs the pinned agent CLI into a directory: it downloads one artifact over HTTPS, checks its
// SHA-256 and only then writes the executable. The runner image does not contain the CLI (the images are public and the
// binary is proprietary), so an init container of the runner pod runs this before the runner starts.
//
// The package never executes what it downloaded and never touches the CLI's login: it writes one file, and it writes
// it only when the checksum matches.
package cliinstall

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// maxDownload bounds the artifact. The CLI is a few hundred megabytes at most.
const maxDownload = 512 << 20

var (
	versionRE = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._-]{0,63}$`)
	platformRE = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._-]{0,63}$`)
	sha256RE  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	nameRE    = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._-]{0,63}$`)
)

// Platform is the vendor's name for an architecture and the SHA-256 of the artifact for it.
type Platform struct {
	Name   string
	SHA256 string
}

// Options says what to install.
type Options struct {
	URLTemplate string              // an https URL with {version} and {platform}
	Version     string              // the pinned version
	Platforms   map[string]Platform // by GOARCH: "amd64", "arm64"
	GOARCH      string              // the architecture to install for; the command passes runtime.GOARCH
	Dest        string              // the directory to install into; it must exist
	Name        string              // the file name of the installed executable
	Archive     string              // "none": the artifact is the executable; "tar.gz": it holds it as Member
	Member      string              // for "tar.gz": the base name of the file to take
	HTTP        *http.Client        // nil: a client with a timeout that follows only https redirects
}

func (o Options) validate() (Platform, error) {
	if !versionRE.MatchString(o.Version) {
		return Platform{}, fmt.Errorf("the version %q is not a version string", o.Version)
	}
	if !nameRE.MatchString(o.Name) {
		return Platform{}, fmt.Errorf("the file name %q is not a plain name", o.Name)
	}
	switch o.Archive {
	case "none":
	case "tar.gz":
		if !nameRE.MatchString(o.Member) {
			return Platform{}, fmt.Errorf("the archive member %q is not a plain name", o.Member)
		}
	default:
		return Platform{}, fmt.Errorf("the archive type %q is not none or tar.gz", o.Archive)
	}
	p, ok := o.Platforms[o.GOARCH]
	if !ok {
		return Platform{}, fmt.Errorf("no platform is configured for the architecture %q", o.GOARCH)
	}
	if !platformRE.MatchString(p.Name) {
		return Platform{}, fmt.Errorf("the platform name %q is not a plain name", p.Name)
	}
	if !sha256RE.MatchString(p.SHA256) {
		return Platform{}, fmt.Errorf("the checksum for %s is not 64 lower-case hex digits", o.GOARCH)
	}
	if info, err := os.Stat(o.Dest); err != nil || !info.IsDir() {
		return Platform{}, fmt.Errorf("the destination %q is not a directory", o.Dest)
	}
	return p, nil
}

// downloadURL fills the template and refuses anything but https.
func (o Options) downloadURL(p Platform) (string, error) {
	raw := strings.NewReplacer("{version}", o.Version, "{platform}", p.Name).Replace(o.URLTemplate)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", errors.New("the download URL must be an https URL")
	}
	return raw, nil
}

func (o Options) client() *http.Client {
	if o.HTTP != nil {
		return o.HTTP
	}
	return &http.Client{
		Timeout: 10 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" {
				return errors.New("a redirect to a URL that is not https")
			}
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
}

// Install downloads, verifies and installs the CLI and returns the path of the executable. On any failure nothing is
// left in the destination.
func Install(ctx context.Context, o Options) (string, error) {
	p, err := o.validate()
	if err != nil {
		return "", err
	}
	target, err := o.downloadURL(p)
	if err != nil {
		return "", err
	}

	download, err := os.CreateTemp(o.Dest, ".download-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(download.Name())
	defer download.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	resp, err := o.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(download, hash), io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	if n > maxDownload {
		return "", errors.New("download: the artifact is larger than the installer reads")
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != p.SHA256 {
		return "", fmt.Errorf("the checksum of the download is %s, pinned is %s: refusing to install it", got, p.SHA256)
	}

	staged, err := os.CreateTemp(o.Dest, ".install-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(staged.Name())
	defer staged.Close()
	if _, err := download.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	switch o.Archive {
	case "none":
		_, err = io.Copy(staged, download)
	case "tar.gz":
		err = extract(download, o.Member, staged)
	}
	if err != nil {
		return "", err
	}
	if err := staged.Chmod(0o755); err != nil {
		return "", err
	}
	if err := staged.Close(); err != nil {
		return "", err
	}
	final := filepath.Join(o.Dest, o.Name)
	if err := os.Rename(staged.Name(), final); err != nil {
		return "", err
	}
	return final, nil
}

// extract copies the one regular file whose base name is member out of a tar.gz. Every other entry is skipped, and no
// path in the archive is ever joined to a destination, so an entry cannot write anywhere.
func extract(r io.Reader, member string, w io.Writer) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("the archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("the archive has no file named %q", member)
		}
		if err != nil {
			return fmt.Errorf("the archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || path.Base(hdr.Name) != member {
			continue
		}
		n, err := io.Copy(w, io.LimitReader(tr, maxDownload+1))
		if err != nil {
			return fmt.Errorf("the archive: %w", err)
		}
		if n > maxDownload {
			return errors.New("the archive member is larger than the installer reads")
		}
		return nil
	}
}
```

Run `gofmt -w internal/cliinstall`.

- [ ] **Step 4: Run the package tests**

Run: `go test ./internal/cliinstall -race -count=1 -v`
Expected: all PASS. If `TestInstallRefusesWhatItCannotTrust` reports a case that did not fail, the validation for that case is missing: add it in `validate` or `downloadURL`, not in the test.

- [ ] **Step 5: Write the failing test for the command's flag**

Create `cmd/remedy-runner/installcli_test.go`:

```go
package main

import (
	"strings"
	"testing"
)

func TestPlatformFlagParsesGoarchNameAndChecksum(t *testing.T) {
	var p platformFlag
	sha := strings.Repeat("a", 64)
	if err := p.Set("amd64=linux-x64@" + sha); err != nil {
		t.Fatal(err)
	}
	if err := p.Set("arm64=linux-arm64@" + sha); err != nil {
		t.Fatal(err)
	}
	got := p.platforms()
	if got["amd64"].Name != "linux-x64" || got["arm64"].Name != "linux-arm64" || got["arm64"].SHA256 != sha {
		t.Fatalf("platforms = %+v", got)
	}
}

func TestPlatformFlagRefusesWhatIsNotThreeParts(t *testing.T) {
	var p platformFlag
	for _, bad := range []string{"", "amd64", "amd64=linux-x64", "=linux-x64@abc", "amd64=@abc", "amd64=linux-x64@", "amd64=a=b@c"} {
		if err := p.Set(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
	var dup platformFlag
	if err := dup.Set("amd64=a@b"); err != nil {
		t.Fatal(err)
	}
	if err := dup.Set("amd64=c@d"); err == nil {
		t.Error("a second value for the same architecture must be refused")
	}
}

func TestInstallCLIFailsWithoutItsRequiredFlags(t *testing.T) {
	if code := installCLI(nil); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if code := installCLI([]string{"--version", "1.0.0"}); code != 2 {
		t.Fatalf("exit code without a template and a platform = %d, want 2", code)
	}
}
```

- [ ] **Step 6: Run it to see it fail**

Run: `go test ./cmd/remedy-runner -run 'Platform|InstallCLI' -v`
Expected: FAIL to compile with `undefined: platformFlag`.

- [ ] **Step 7: Implement the subcommand and wire it**

Create `cmd/remedy-runner/installcli.go`:

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/cliinstall"
)

// platformFlag collects `--platform GOARCH=NAME@SHA256` flags: Go's name for an architecture, the vendor's name for the
// platform, and the SHA-256 of the artifact.
type platformFlag struct{ m map[string]cliinstall.Platform }

func (p *platformFlag) String() string { return fmt.Sprint(p.m) }

func (p *platformFlag) Set(v string) error {
	arch, rest, ok := strings.Cut(v, "=")
	name, sum, ok2 := strings.Cut(rest, "@")
	if !ok || !ok2 || arch == "" || name == "" || sum == "" || strings.ContainsAny(name, "=@") || strings.Contains(sum, "@") {
		return fmt.Errorf("%q is not GOARCH=NAME@SHA256", v)
	}
	if p.m == nil {
		p.m = map[string]cliinstall.Platform{}
	}
	if _, dup := p.m[arch]; dup {
		return fmt.Errorf("the architecture %s is given twice", arch)
	}
	p.m[arch] = cliinstall.Platform{Name: name, SHA256: sum}
	return nil
}

func (p *platformFlag) platforms() map[string]cliinstall.Platform { return p.m }

// installCLI is `remedy-runner install-cli`, the command of the init container of the runner pod. It returns the exit
// code: 0 on success, 1 when the install failed, 2 for a wrong command line.
func installCLI(args []string) int {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	fs := flag.NewFlagSet("install-cli", flag.ContinueOnError)
	var platforms platformFlag
	version := fs.String("version", "", "the pinned CLI version (required)")
	template := fs.String("url-template", "", "an https URL with {version} and {platform} (required)")
	dest := fs.String("dest", "/opt/claude", "the directory to install into")
	name := fs.String("name", "claude", "the file name of the installed executable")
	archive := fs.String("archive", "none", "none: the download is the executable; tar.gz: it holds it as --member")
	member := fs.String("member", "", "for tar.gz: the base name of the file to take")
	fs.Var(&platforms, "platform", "GOARCH=NAME@SHA256, once per architecture (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *version == "" || *template == "" || len(platforms.platforms()) == 0 {
		fmt.Fprintln(os.Stderr, "install-cli needs --version, --url-template and at least one --platform")
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	path, err := cliinstall.Install(ctx, cliinstall.Options{
		URLTemplate: *template, Version: *version, Platforms: platforms.platforms(), GOARCH: runtime.GOARCH,
		Dest: *dest, Name: *name, Archive: *archive, Member: *member,
	})
	if err != nil {
		log.Error("the CLI was not installed", "err", err)
		return 1
	}
	log.Info("installed the CLI", "path", path, "version", *version, "arch", runtime.GOARCH)
	return 0
}
```

In `cmd/remedy-runner/main.go`, replace:

```go
func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
```

with:

```go
func main() {
	if len(os.Args) > 1 && os.Args[1] == "install-cli" {
		os.Exit(installCLI(os.Args[2:]))
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
```

- [ ] **Step 8: Run everything**

Run: `gofmt -l . ; go vet ./... && go test ./... -race -count=1`
Expected: no `gofmt` output; all tests PASS.

Then `go run ./cmd/remedy-runner install-cli; echo "exit=$?"`: expect the message `install-cli needs --version, --url-template and at least one --platform` and `exit=2`.

- [ ] **Step 9: Commit**

```bash
git add internal/cliinstall cmd/remedy-runner
git commit -m "feat(runner): install-cli, a pinned CLI download that verifies its checksum

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 6: The images

**Files:**
- Overwrite: `Dockerfile`
- Create: `Dockerfile.runner`, `.dockerignore`
- Modify: `Makefile`, `CLAUDE.md`

**Interfaces:**
- Consumes: S4's record for the base image of `Dockerfile.runner` (the default below, `gcr.io/distroless/base-debian12:nonroot`, is used when the record says the CLI runs on it; otherwise set `BASE` to the record's image, and add `--build-arg BASE=...` nowhere: change the default).
- Produces: images `remedy-server:$(IMAGE_TAG)` (with `/remedy-server` and `/remedy-tokenrefresh`) and `remedy-runner:$(IMAGE_TAG)` (with `/remedy-runner`, which has the `install-cli` subcommand), `make images`.

- [ ] **Step 1: The build context**

Create `.dockerignore`:

```text
.git
.github
.claude
.playwright-mcp
bin
docs
dev
scripts
web/node_modules
web/dist
**/*.db
**/*.db-wal
**/*.db-shm
**/*.png
```

The server image needs `web/` sources and `go.mod`/`go.sum`; nothing excluded here is used by either build.

- [ ] **Step 2: The control plane image**

Overwrite `Dockerfile`:

```dockerfile
FROM --platform=$BUILDPLATFORM node:lts-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -tags webui -o /out/remedy-server ./cmd/remedy-server
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /out/remedy-tokenrefresh ./cmd/remedy-tokenrefresh
RUN mkdir /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/remedy-server /remedy-server
# The Helm chart runs this as the CronJob that keeps the write identity's token fresh.
COPY --from=build /out/remedy-tokenrefresh /remedy-tokenrefresh
# The image runs as nonroot (65532). /data must exist and be writable for SQLite; named volumes inherit this.
COPY --from=build --chown=65532:65532 /out/data /data
ENV REMEDY_DB=/data/remedy.db
VOLUME /data
EXPOSE 8080 8081
ENTRYPOINT ["/remedy-server"]
```

- [ ] **Step 3: The runner image**

Create `Dockerfile.runner`:

```dockerfile
# The runner image. It does NOT contain the claude CLI: the images are public and the binary is proprietary. The
# runner pod's init container runs `/remedy-runner install-cli` to install the pinned CLI into a volume.
# BASE is the smallest image the CLI runs on (the record of spike S4, docs/research/k8s-s4-cli-install.md).
ARG BASE=gcr.io/distroless/base-debian12:nonroot

FROM --platform=$BUILDPLATFORM golang:1.27 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /out/remedy-runner ./cmd/remedy-runner

FROM ${BASE}
COPY --from=build /out/remedy-runner /remedy-runner
USER 65532:65532
ENTRYPOINT ["/remedy-runner"]
```

- [ ] **Step 4: `make images`**

In `Makefile`, replace:

```make
.PHONY: help build build-go test vet fmt web-install web-build web-lint web-test dev-server dev-web check
```

with:

```make
IMAGE_TAG ?= dev

.PHONY: help build build-go images test vet fmt web-install web-build web-lint web-test dev-server dev-web check
```

and replace:

```make
test: ## Run Go tests
```

with:

```make
images: ## Build the control plane and runner images for this machine (tag $(IMAGE_TAG), default dev)
	docker build -t remedy-server:$(IMAGE_TAG) .
	docker build -f Dockerfile.runner -t remedy-runner:$(IMAGE_TAG) .

test: ## Run Go tests
```

- [ ] **Step 5: Build the images and check what is in them**

Run: `make images`
Expected: both builds succeed.

Then:

```sh
docker image inspect remedy-server:dev remedy-runner:dev --format '{{.RepoTags}} user={{.Config.User}}'
docker run --rm remedy-server:dev; echo "exit=$?"
docker run --rm --entrypoint /remedy-tokenrefresh remedy-server:dev -h 2>&1 | head -3
docker run --rm remedy-runner:dev; echo "exit=$?"
docker run --rm remedy-runner:dev install-cli; echo "exit=$?"
docker create --name remedy-inspect remedy-runner:dev > /dev/null && docker export remedy-inspect | tar -t | grep -c -i claude; docker rm remedy-inspect > /dev/null
```

Expected: the server image's user is `nonroot` or `65532` (distroless `:nonroot` reports `65532`), the runner image's is `65532:65532`; the server prints `invalid configuration` with the password message and `exit=2`; the refresher prints its usage; the runner without a token prints `invalid configuration` and `exit=2`; `install-cli` without flags prints its message and `exit=2`; the last command prints `0` (no file of the runner image has "claude" in its name).

- [ ] **Step 6: Document the commands and the state**

In `CLAUDE.md`, in the "Current state" list, add after the phase 2 part D line:

```markdown
- Kubernetes deployment (`docs/specs/2026-10-06-kubernetes-deployment-design.md`, plans `k8s-0` to `k8s-6` in `docs/plans/`): the control plane and the runner are built as two images (`make images`, `Dockerfile`, `Dockerfile.runner`); the control plane can serve `/runner/v1` and `/mcp` on a second port (`REMEDY_INTERNAL_ADDR`, `server.NewSplit`); `cmd/remedy-tokenrefresh` mints the write identity's short-lived token; `remedy-runner install-cli` installs the pinned `claude` CLI, which no image contains. Check `git log` for how far the plans are.
```

and in the Commands block, after the `make build-go` line add:

```markdown
make images                                      # the control plane and runner images for this machine, tag dev (IMAGE_TAG=...)
```

Add to the variables paragraph, after `REMEDY_ADDR` (default `:8080`):

```markdown
`REMEDY_INTERNAL_ADDR` (server, default empty: one port) moves `/runner/v1` and `/mcp` to a second port, and the main port then answers 404 for them; the runner's `REMEDY_SERVER_URL` must point at the internal one.
```

- [ ] **Step 7: Run the full check and commit**

Run: `make check`
Expected: PASS.

```bash
git add Dockerfile Dockerfile.runner .dockerignore Makefile CLAUDE.md
git commit -m "build: a control plane image with the token refresher and a runner image without the CLI, make images

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```
