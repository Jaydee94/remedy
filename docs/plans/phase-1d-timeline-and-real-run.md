# Phase 1d: Timeline and the Real Run Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Everything Remedy does shows up in a live timeline (the new home page), the GitHub client is provably read-only and auditable, and the whole of phase 1 is run once against the real GitHub with the Remedy repository itself.

**Architecture:** The `activity` table already records every state change in the transaction that causes it. This plan reads it: a paginated list endpoint and an SSE stream that follows the table by id, then a Timeline page that loads the newest entries, subscribes to the stream from the newest id it has, and merges what arrives. The GitHub client gets a request audit and a hard read-only guard at the HTTP transport level, switched on by `REMEDY_LOG_LEVEL=debug`. The last task is a runbook and the maintainer-assisted run that checks the success criteria of the spec.

**Tech Stack:** Go 1.27 stdlib only (no new Go dependencies), SQLite, React 19 and the shadcn components that are already installed (no new web dependencies).

**Spec:** [`docs/specs/2026-10-02-phase-1-detect-and-diagnose-design.md`](../specs/2026-10-02-phase-1-detect-and-diagnose-design.md), sections 8 (API: activity), 9 (Timeline), 11 steps 8 and 9, and 12 (success criteria).

**Scope note:** Plans 1a to 1c are merged. This plan adds no capability that changes anything on GitHub; it only makes what exists visible and checks it against the real service. The fixer, Home Assistant notifications and webhooks stay out (spec, non-goals).

## Decisions made while planning

These refine the spec after looking at the code of plans 1a to 1c. Task 5 records them in the spec.

| Topic | Spec said | This plan |
|---|---|---|
| How the stream learns about new entries | "like the run stream" (a hub wakes the stream) | **the stream looks at the table once a second** (`Deps.ActivityInterval`). Entries are written from the engine, the poller, the responder, the server and the store; a hub would need a hook in every one of them, and one second is fast enough for a single-user timeline. |
| Where the stream starts | resumable with `Last-Event-ID` | `Last-Event-ID` when the browser reconnects; otherwise `?after=<id>`; otherwise the end of the log. The page loads the list first and then names the newest id it has in `after`, so nothing that happens in between is lost. `after=0` replays everything. |
| Pagination | `before` and `limit` | the same, with `limit` 1 to 200 (default 50) and a `hasMore` flag in the answer. The server reads one more row than asked to know whether there is more. |
| "Remedy never makes a write call to GitHub" | a client without write methods | additionally **enforced at the transport**: `github.New` wraps the HTTP transport so that any method except `GET` and `HEAD` is refused before it leaves the process. With `REMEDY_LOG_LEVEL=debug` every request is logged (method, host, path without query, status, duration), so the real run can show the proof. |
| Real run | the Remedy repository itself | two cases: the open Renovate pull request (a real red check that already exists) and one deliberately red pull request that is then fixed, which exercises detection, diagnosis and resolution. The maintainer creates the token; the deliberate pull request is only opened after the maintainer confirms. |

## Global Constraints

- Everything committed is English: docs, code, identifiers, comments, UI copy, commit messages.
- No new Go dependencies and no new web dependencies.
- The GitHub client is **read-only**: every exported method name starts with `Get` or `List`, and it only sends `GET`. The existing reflection test keeps enforcing this; Task 4 adds the transport guard.
- The GitHub token never appears in an API response, a log line, an error message, an activity entry, a prompt or the database in plaintext.
- Text that comes from GitHub (summaries contain check names and branch names) is **untrusted**: the UI shows it as escaped text and only links `http` and `https` addresses.
- `web/tsconfig.app.json` keeps `erasableSyntaxOnly` and `verbatimModuleSyntax`: no enums, no constructor parameter properties, `import type` for types.
- Every UI change is checked in a real browser (Playwright) before it is called done.
- Every commit message ends with the trailer `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`.
- `make check` and `go test ./... -race -count=1` must pass at the end of every task.

## How to read the code blocks

A line `Create `path`:` or `Overwrite `path`:` is followed by the complete file. A line `In `path`, replace:` is followed by a block with the exact old text, a line `with:` and a block with the new text; the old text occurs exactly once in the file. Go code uses tabs.

## File Structure

| Path | Responsibility |
|---|---|
| `internal/store/incidents.go`, `internal/store/activity_test.go` | Activity entries with the repository name, a `Before` cursor, `ListActivitySince`, `LastActivityID` |
| `internal/server/timeline.go`, `internal/server/timeline_test.go`, `internal/server/server.go` | `GET /api/activity` and `GET /api/activity/stream`, `Deps.ActivityInterval` |
| `internal/app/app.go`, `cmd/remedy-server/main.go` | The log level, the audited GitHub client |
| `web/src/api.ts`, `web/src/timeline.ts`, `web/src/TimelinePage.tsx`, `web/src/App.tsx`, `web/src/components/AppLayout.tsx` | The Timeline page, the home route and the sidebar |
| `internal/github/audit.go`, `internal/github/audit_test.go`, `internal/github/client.go` | The read-only transport guard and the request audit |
| `internal/config/loglevel.go`, `internal/config/loglevel_test.go` | `REMEDY_LOG_LEVEL` |
| `docs/runbook/first-real-run.md`, `docs/research/phase-1-real-run.md`, `README.md`, `CLAUDE.md`, `docs/specs/...`, `docs/design.md` | The runbook, the record of the real run, the status of the documents |

---

### Task 1: Activity entries with the repository name, a cursor and a follow-up read

**Files:**
- Modify: `internal/store/incidents.go`
- Create: `internal/store/activity_test.go`

**Interfaces:**
- Consumes: the `activity` table (migration 003), `store.Store`, `store.NewActivity`, `store.Activity`, `store.ActivityQuery`.
- Produces:
  - `store.Activity.RepoName string`: the full name of the entry's repository, empty when the entry has no repository or the repository was removed.
  - `store.ActivityQuery.Before int64`: only entries with an id below it (0 means no cursor). `ListActivity` keeps returning newest first.
  - `(*Store).ListActivitySince(ctx, afterID int64, limit int) ([]Activity, error)`: entries with an id above `afterID`, **oldest first**; `limit` defaults to 100.
  - `(*Store).LastActivityID(ctx) (int64, error)`: the highest id, 0 for an empty log.

- [ ] **Step 1: Write the failing tests**

Create `internal/store/activity_test.go`:

```go
package store_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/Jaydee94/remedy/internal/store"
)

func addEntries(t *testing.T, s *store.Store, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		err := s.AddActivity(context.Background(), store.NewActivity{Kind: store.KindPollRecovered, Summary: "entry " + strconv.Itoa(i)})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestListActivityNamesTheRepository(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	repo := seedRepo(t, s)
	if err := s.AddActivity(ctx, entry(store.KindRepoAdded, repo.ID)); err != nil {
		t.Fatal(err)
	}
	if err := s.AddActivity(ctx, store.NewActivity{Kind: store.KindConnectionChanged, Summary: "connected"}); err != nil {
		t.Fatal(err)
	}

	log, err := s.ListActivity(ctx, store.ActivityQuery{})
	if err != nil || len(log) != 2 {
		t.Fatalf("activity = %+v, err = %v", log, err)
	}
	if log[0].RepoName != "" || log[0].RepoID != 0 || log[1].RepoName != "octo/hello" || log[1].RepoID != repo.ID {
		t.Fatalf("entries = %+v", log)
	}

	// Removing the repository keeps the entry; its summary still says what happened.
	if err := s.DeleteRepo(ctx, repo.ID); err != nil {
		t.Fatal(err)
	}
	log, err = s.ListActivity(ctx, store.ActivityQuery{})
	if err != nil || len(log) != 2 {
		t.Fatalf("activity after the removal = %+v, err = %v", log, err)
	}
	if log[1].RepoID != 0 || log[1].RepoName != "" || log[1].Summary != "repo_added summary" {
		t.Fatalf("entry of the removed repository = %+v", log[1])
	}
}

func TestListActivityBeforeIsACursor(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	addEntries(t, s, 5)
	all, err := s.ListActivity(ctx, store.ActivityQuery{})
	if err != nil || len(all) != 5 {
		t.Fatalf("activity = %+v, err = %v", all, err)
	}

	page, err := s.ListActivity(ctx, store.ActivityQuery{Before: all[1].ID, Limit: 2})
	if err != nil || len(page) != 2 || page[0].ID != all[2].ID || page[1].ID != all[3].ID {
		t.Fatalf("page = %+v, err = %v, want the entries after the second one", page, err)
	}
	if rest, err := s.ListActivity(ctx, store.ActivityQuery{Before: all[4].ID}); err != nil || len(rest) != 0 {
		t.Fatalf("before the oldest entry = %+v, err = %v, want nothing", rest, err)
	}
}

func TestListActivitySinceReturnsTheNewerEntriesOldestFirst(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	if last, err := s.LastActivityID(ctx); err != nil || last != 0 {
		t.Fatalf("LastActivityID of an empty log = %d, %v, want 0", last, err)
	}
	addEntries(t, s, 5)
	all, _ := s.ListActivity(ctx, store.ActivityQuery{}) // newest first

	if last, err := s.LastActivityID(ctx); err != nil || last != all[0].ID {
		t.Fatalf("LastActivityID = %d, %v, want %d", last, err, all[0].ID)
	}

	got, err := s.ListActivitySince(ctx, all[3].ID, 10)
	if err != nil || len(got) != 3 || got[0].ID != all[2].ID || got[1].ID != all[1].ID || got[2].ID != all[0].ID {
		t.Fatalf("since the second oldest = %+v, err = %v, want the three newer ones, oldest first", got, err)
	}
	got, err = s.ListActivitySince(ctx, 0, 2)
	if err != nil || len(got) != 2 || got[0].ID != all[4].ID || got[1].ID != all[3].ID {
		t.Fatalf("since 0 with a limit of 2 = %+v, err = %v, want the two oldest", got, err)
	}
	if got, err := s.ListActivitySince(ctx, all[0].ID, 10); err != nil || len(got) != 0 {
		t.Fatalf("since the newest = %+v, err = %v, want nothing", got, err)
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/store -run 'TestListActivity' -v`
Expected: the package does not compile (`unknown field Before`, `log[0].RepoName undefined`, `s.ListActivitySince undefined`, `s.LastActivityID undefined`).

- [ ] **Step 3: Implement it**

In `internal/store/incidents.go`, replace:

```go
type Activity struct {
	ID         int64
	At         time.Time
	Kind       string
	RepoID     int64
	IncidentID int64
	RunID      string
	Summary    string
	Data       json.RawMessage
}
```

with:

```go
type Activity struct {
	ID         int64
	At         time.Time
	Kind       string
	RepoID     int64
	RepoName   string // empty when the entry has no repository, or the repository was removed
	IncidentID int64
	RunID      string
	Summary    string
	Data       json.RawMessage
}
```

In `internal/store/incidents.go`, replace:

```go
// ActivityQuery selects activity entries. IncidentID 0 means all of them; Limit defaults to 100.
type ActivityQuery struct {
	IncidentID int64
	Limit      int
}
```

with:

```go
// ActivityQuery selects activity entries. IncidentID 0 means all of them. Before is a cursor: only entries
// with a smaller id, 0 means no cursor. Limit defaults to 100.
type ActivityQuery struct {
	IncidentID int64
	Before     int64
	Limit      int
}
```

In `internal/store/incidents.go`, replace:

```go
// ListActivity returns activity entries, newest first.
func (s *Store) ListActivity(ctx context.Context, q ActivityQuery) ([]Activity, error) {
	query := `SELECT id, at, kind, repo_id, incident_id, run_id, summary, data FROM activity`
	var args []any
	if q.IncidentID != 0 {
		query += ` WHERE incident_id = ?`
		args = append(args, q.IncidentID)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Activity{}
	for rows.Next() {
		var (
			a         Activity
			at, data  string
			repo, inc sql.NullInt64
			runID     sql.NullString
		)
		if err := rows.Scan(&a.ID, &at, &a.Kind, &repo, &inc, &runID, &a.Summary, &data); err != nil {
			return nil, err
		}
		if a.At, err = parseTS(at); err != nil {
			return nil, err
		}
		a.RepoID, a.IncidentID, a.RunID = repo.Int64, inc.Int64, runID.String
		a.Data = json.RawMessage(data)
		list = append(list, a)
	}
	return list, rows.Err()
}
```

with:

```go
const activitySelect = `SELECT a.id, a.at, a.kind, a.repo_id, a.incident_id, a.run_id, a.summary, a.data, r.full_name
	FROM activity a LEFT JOIN repos r ON r.id = a.repo_id`

func activityLimit(n int) int {
	if n <= 0 {
		return 100
	}
	return n
}

// ListActivity returns activity entries, newest first.
func (s *Store) ListActivity(ctx context.Context, q ActivityQuery) ([]Activity, error) {
	var where []string
	var args []any
	if q.IncidentID != 0 {
		where = append(where, `a.incident_id = ?`)
		args = append(args, q.IncidentID)
	}
	if q.Before != 0 {
		where = append(where, `a.id < ?`)
		args = append(args, q.Before)
	}
	query := activitySelect
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, ` AND `)
	}
	query += ` ORDER BY a.id DESC LIMIT ?`
	args = append(args, activityLimit(q.Limit))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return collectActivity(rows)
}

// ListActivitySince returns the entries with an id above afterID, oldest first. It is how a stream follows
// the log.
func (s *Store) ListActivitySince(ctx context.Context, afterID int64, limit int) ([]Activity, error) {
	rows, err := s.db.QueryContext(ctx, activitySelect+` WHERE a.id > ? ORDER BY a.id LIMIT ?`, afterID, activityLimit(limit))
	if err != nil {
		return nil, err
	}
	return collectActivity(rows)
}

// LastActivityID returns the highest activity id, or 0 when the log is empty.
func (s *Store) LastActivityID(ctx context.Context) (int64, error) {
	var id sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(id) FROM activity`).Scan(&id); err != nil {
		return 0, err
	}
	return id.Int64, nil
}

func collectActivity(rows *sql.Rows) ([]Activity, error) {
	defer rows.Close()
	list := []Activity{}
	for rows.Next() {
		var (
			a               Activity
			at, data        string
			repo, inc       sql.NullInt64
			runID, repoName sql.NullString
		)
		if err := rows.Scan(&a.ID, &at, &a.Kind, &repo, &inc, &runID, &a.Summary, &data, &repoName); err != nil {
			return nil, err
		}
		var err error
		if a.At, err = parseTS(at); err != nil {
			return nil, err
		}
		a.RepoID, a.RepoName, a.IncidentID, a.RunID = repo.Int64, repoName.String, inc.Int64, runID.String
		a.Data = json.RawMessage(data)
		list = append(list, a)
	}
	return list, rows.Err()
}
```

- [ ] **Step 4: Run the tests and watch them pass**

Run: `gofmt -l internal/store; go vet ./internal/store && go test ./internal/store -race -count=1`
Expected: no output from `gofmt -l`, then `ok`.

- [ ] **Step 5: Mutation checks**

Make each change, run `go test ./internal/store -run TestListActivity -count=1`, expect the named test to fail, and revert it.

1. In `ListActivity`, change `a.id < ?` to `a.id <= ?`: `TestListActivityBeforeIsACursor` fails.
2. Change `LEFT JOIN repos` to `JOIN repos`: `TestListActivityNamesTheRepository` fails (the entry without a repository disappears).
3. In `ListActivitySince`, change `ORDER BY a.id LIMIT ?` to `ORDER BY a.id DESC LIMIT ?`: `TestListActivitySinceReturnsTheNewerEntriesOldestFirst` fails.

- [ ] **Step 6: Run the whole suite and commit**

Run: `go test ./... -race -count=1`
Expected: all packages `ok`.

```bash
git add internal/store/incidents.go internal/store/activity_test.go
git commit -m "feat(store): list activity with the repository name, a cursor and a follow-up read" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: The activity API and its live stream

**Files:**
- Create: `internal/server/timeline.go`, `internal/server/timeline_test.go`
- Modify: `internal/server/server.go`

**Interfaces:**
- Consumes: `store.ListActivity` with `Before`, `store.ListActivitySince`, `store.LastActivityID` (Task 1); `writeSSE`, `keepAlive`, `writeJSON`, `writeErr`, `s.session` (existing).
- Produces:
  - `GET /api/activity?before=&limit=` answers `{"entries":[...],"hasMore":bool}`, newest first. An entry is `{id, at, kind, summary, repo?, incidentId?, runId?}`; the optional fields are left out when empty.
  - `GET /api/activity/stream?after=` (SSE): one `activity` event per entry, oldest first, with the entry id as the event id. Starts after `Last-Event-ID`, else after `after`, else at the end of the log.
  - `server.Deps.ActivityInterval time.Duration` (default 1 second).

- [ ] **Step 1: Write the failing tests**

Create `internal/server/timeline_test.go`:

```go
package server_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/store"
)

// activityEnv is a server whose activity stream looks for news every 10 milliseconds.
type activityEnv struct{ *ghEnv }

func newActivityEnv(t *testing.T) *activityEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ts := httptest.NewServer(server.New(server.Deps{
		Store: st, Auth: auth.New(password), RunnerToken: runnerToken, ActivityInterval: 10 * time.Millisecond,
	}))
	t.Cleanup(ts.Close)

	jar, _ := cookiejar.New(nil)
	e := &ghEnv{ts: ts, store: st, client: &http.Client{Jar: jar}}
	if code, _ := e.call(t, http.MethodPost, "/api/login", `{"password":"`+password+`"}`); code != http.StatusNoContent {
		t.Fatalf("login status = %d", code)
	}
	return &activityEnv{e}
}

// addActivity appends an entry and returns its id.
func addActivity(t *testing.T, st *store.Store, summary string) int64 {
	t.Helper()
	ctx := context.Background()
	if err := st.AddActivity(ctx, store.NewActivity{Kind: store.KindPollRecovered, Summary: summary}); err != nil {
		t.Fatal(err)
	}
	id, err := st.LastActivityID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

type timelinePage struct {
	Entries []map[string]any `json:"entries"`
	HasMore bool             `json:"hasMore"`
}

func (e *activityEnv) page(t *testing.T, query string) timelinePage {
	t.Helper()
	code, body := e.call(t, http.MethodGet, "/api/activity"+query, "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/activity%s = %d %s", query, code, body)
	}
	var p timelinePage
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("body %q: %v", body, err)
	}
	return p
}

func idOf(e map[string]any) string { return strconv.FormatInt(int64(e["id"].(float64)), 10) }

func TestActivityRoutesNeedASession(t *testing.T) {
	e := newActivityEnv(t)
	for _, path := range []string{"/api/activity", "/api/activity/stream"} {
		resp, err := http.Get(e.ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s without a session = %d, want 401", path, resp.StatusCode)
		}
	}
}

func TestListActivityIsNewestFirstAndPaged(t *testing.T) {
	e := newActivityEnv(t)
	ctx := context.Background()
	if err := e.store.SaveConnection(ctx, store.Connection{TokenCiphertext: []byte("sealed"), TokenHint: "wxyz", Login: "octo", Status: store.ConnOK, CheckedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	repo, err := e.store.AddRepo(ctx, store.ConnectionID, "octo/hello", "main")
	if err != nil {
		t.Fatal(err)
	}
	in, err := e.store.OpenIncident(ctx, store.NewIncident{
		RepoID: repo.ID, Ref: "pr:7", RefURL: "https://github.com/octo/hello/pull/7", CheckName: "go",
		Conclusion: "failure", HeadSHA: "abc1234", CheckURL: "https://github.com/octo/hello/runs/1",
	}, store.NewActivity{Kind: store.KindIncidentOpened, RepoID: repo.ID, Summary: "go failed on pr:7"})
	if err != nil {
		t.Fatal(err)
	}
	code, body := e.call(t, http.MethodPost, "/api/runs", `{"prompt":"hello"}`)
	if code != http.StatusCreated {
		t.Fatalf("POST /api/runs = %d %s", code, body)
	}
	runID := field(t, body, "id").(string)
	for _, a := range []store.NewActivity{
		{Kind: store.KindConnectionChanged, Summary: "connected"},
		{Kind: store.KindDiagnosisStarted, RepoID: repo.ID, IncidentID: in.ID, RunID: runID, Summary: "diagnosing"},
		{Kind: store.KindPollFailed, RepoID: repo.ID, Summary: "poll failed"},
		{Kind: store.KindPollRecovered, RepoID: repo.ID, Summary: "recovered"},
	} {
		if err := e.store.AddActivity(ctx, a); err != nil {
			t.Fatal(err)
		}
	}

	all := e.page(t, "?limit=200")
	if len(all.Entries) != 5 || all.HasMore {
		t.Fatalf("all = %+v", all)
	}
	for i, want := range []string{"recovered", "poll failed", "diagnosing", "connected", "go failed on pr:7"} {
		if all.Entries[i]["summary"] != want {
			t.Fatalf("entry %d = %v, want %q (newest first)", i, all.Entries[i], want)
		}
	}
	opened, diagnosing, connected := all.Entries[4], all.Entries[2], all.Entries[3]
	if opened["repo"] != "octo/hello" || int64(opened["incidentId"].(float64)) != in.ID || opened["kind"] != store.KindIncidentOpened || opened["at"] == "" {
		t.Errorf("opened = %v", opened)
	}
	if diagnosing["runId"] != runID {
		t.Errorf("diagnosing = %v, want the run id", diagnosing)
	}
	for _, key := range []string{"repo", "incidentId", "runId"} {
		if _, has := connected[key]; has {
			t.Errorf("an entry without a %s still has the key: %v", key, connected)
		}
	}

	first := e.page(t, "?limit=2")
	if len(first.Entries) != 2 || !first.HasMore || first.Entries[0]["summary"] != "recovered" {
		t.Fatalf("first page = %+v", first)
	}
	second := e.page(t, "?limit=2&before="+idOf(first.Entries[1]))
	if len(second.Entries) != 2 || !second.HasMore || second.Entries[0]["summary"] != "diagnosing" {
		t.Fatalf("second page = %+v", second)
	}
	third := e.page(t, "?limit=2&before="+idOf(second.Entries[1]))
	if len(third.Entries) != 1 || third.HasMore || third.Entries[0]["summary"] != "go failed on pr:7" {
		t.Fatalf("third page = %+v", third)
	}
	if exact := e.page(t, "?limit=5"); len(exact.Entries) != 5 || exact.HasMore {
		t.Fatalf("a page that holds everything = %d entries, hasMore %v, want 5 and false", len(exact.Entries), exact.HasMore)
	}
}

func TestListActivityOfAnEmptyLogIsAnEmptyList(t *testing.T) {
	e := newActivityEnv(t)
	code, body := e.call(t, http.MethodGet, "/api/activity", "")
	if code != http.StatusOK || strings.ReplaceAll(strings.TrimSpace(body), " ", "") != `{"entries":[],"hasMore":false}` {
		t.Fatalf("GET /api/activity = %d %s", code, body)
	}
}

func TestListActivityRejectsBadParameters(t *testing.T) {
	e := newActivityEnv(t)
	for _, query := range []string{"?limit=0", "?limit=201", "?limit=abc", "?limit=-1", "?before=0", "?before=abc", "?before=-5"} {
		if code, _ := e.call(t, http.MethodGet, "/api/activity"+query, ""); code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", query, code)
		}
	}
}

type sseEvent struct{ id, event, data string }

// readEvent returns the next event of a stream and skips comment lines. ok is false when the stream ends.
func readEvent(br *bufio.Reader) (ev sseEvent, ok bool) {
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return sseEvent{}, false
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			if ev.event != "" {
				return ev, true
			}
		case strings.HasPrefix(line, "id: "):
			ev.id = line[len("id: "):]
		case strings.HasPrefix(line, "event: "):
			ev.event = line[len("event: "):]
		case strings.HasPrefix(line, "data: "):
			ev.data = line[len("data: "):]
		}
	}
}

func nextEvent(t *testing.T, br *bufio.Reader) sseEvent {
	t.Helper()
	ev, ok := readEvent(br)
	if !ok {
		t.Fatal("the stream ended before the next event")
	}
	if ev.event != "activity" {
		t.Fatalf("event = %+v, want an activity event", ev)
	}
	return ev
}

func summaryOf(t *testing.T, ev sseEvent) string {
	t.Helper()
	var entry map[string]any
	if err := json.Unmarshal([]byte(ev.data), &entry); err != nil {
		t.Fatalf("data %q: %v", ev.data, err)
	}
	s, _ := entry["summary"].(string)
	return s
}

// openStream connects to the stream. The deadline keeps a broken stream from hanging the test; the returned
// function closes the connection and must run before the test ends.
func (e *activityEnv) openStream(t *testing.T, query string, header map[string]string) (*bufio.Reader, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.ts.URL+"/api/activity/stream"+query, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		b, _ := io.ReadAll(resp.Body)
		cancel()
		t.Fatalf("stream%s = %d %q %s", query, resp.StatusCode, resp.Header.Get("Content-Type"), b)
	}
	return bufio.NewReader(resp.Body), func() { cancel(); _ = resp.Body.Close() }
}

func TestActivityStreamSendsOnlyWhatComesAfterTheResumePoint(t *testing.T) {
	e := newActivityEnv(t)
	a := addActivity(t, e.store, "first")
	b := addActivity(t, e.store, "second")

	br, closeStream := e.openStream(t, "?after="+strconv.FormatInt(a, 10), nil)
	defer closeStream()
	ev := nextEvent(t, br)
	if ev.id != strconv.FormatInt(b, 10) || summaryOf(t, ev) != "second" {
		t.Fatalf("first event = %+v, want the entry after the resume point", ev)
	}

	c := addActivity(t, e.store, "third")
	ev = nextEvent(t, br)
	if ev.id != strconv.FormatInt(c, 10) || summaryOf(t, ev) != "third" {
		t.Fatalf("live event = %+v", ev)
	}
}

func TestActivityStreamWithoutAResumePointStartsAtTheEnd(t *testing.T) {
	e := newActivityEnv(t)
	addActivity(t, e.store, "old")

	br, closeStream := e.openStream(t, "", nil)
	defer closeStream()
	addActivity(t, e.store, "new")
	if ev := nextEvent(t, br); summaryOf(t, ev) != "new" {
		t.Fatalf("first event = %+v, want only what happens after the connection", ev)
	}
}

func TestActivityStreamAfterZeroReplaysTheLog(t *testing.T) {
	e := newActivityEnv(t)
	addActivity(t, e.store, "first")
	addActivity(t, e.store, "second")

	br, closeStream := e.openStream(t, "?after=0", nil)
	defer closeStream()
	if got := summaryOf(t, nextEvent(t, br)); got != "first" {
		t.Fatalf("first event = %q, want the oldest entry", got)
	}
	if got := summaryOf(t, nextEvent(t, br)); got != "second" {
		t.Fatalf("second event = %q", got)
	}
}

func TestLastEventIDWinsOverAfter(t *testing.T) {
	e := newActivityEnv(t)
	addActivity(t, e.store, "first")
	b := addActivity(t, e.store, "second")
	addActivity(t, e.store, "third")

	// A browser that reconnects keeps the address it first used (after=0) and adds Last-Event-ID.
	br, closeStream := e.openStream(t, "?after=0", map[string]string{"Last-Event-ID": strconv.FormatInt(b, 10)})
	defer closeStream()
	if got := summaryOf(t, nextEvent(t, br)); got != "third" {
		t.Fatalf("first event = %q, want the entry after Last-Event-ID", got)
	}
}

func TestActivityStreamRejectsABadResumePoint(t *testing.T) {
	e := newActivityEnv(t)
	for _, query := range []string{"?after=abc", "?after=-1"} {
		if code, _ := e.call(t, http.MethodGet, "/api/activity/stream"+query, ""); code != http.StatusBadRequest {
			t.Errorf("stream%s = %d, want 400", query, code)
		}
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/server -run 'Activity|LastEventID' -v`
Expected: the package does not compile (`unknown field ActivityInterval` in `server.Deps`).

- [ ] **Step 3: Implement the server side**

Create `internal/server/timeline.go`:

```go
package server

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Jaydee94/remedy/internal/store"
)

const (
	defaultActivityLimit = 50
	maxActivityLimit     = 200
	activityBatch        = 100
	defaultActivityPoll  = time.Second
)

var errBadAfter = errors.New("after must be a whole number of 0 or more")

// timelineEntry is one line of the timeline. The optional fields are left out when they are empty.
type timelineEntry struct {
	ID         int64     `json:"id"`
	At         time.Time `json:"at"`
	Kind       string    `json:"kind"`
	Summary    string    `json:"summary"`
	Repo       string    `json:"repo,omitempty"`
	IncidentID int64     `json:"incidentId,omitempty"`
	RunID      string    `json:"runId,omitempty"`
}

func timelineEntryOf(a store.Activity) timelineEntry {
	return timelineEntry{ID: a.ID, At: a.At, Kind: a.Kind, Summary: a.Summary, Repo: a.RepoName, IncidentID: a.IncidentID, RunID: a.RunID}
}

func (s *srv) listActivity(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit := defaultActivityLimit
	if v := query.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxActivityLimit {
			writeErr(w, http.StatusBadRequest, "limit must be a whole number from 1 to "+strconv.Itoa(maxActivityLimit))
			return
		}
		limit = n
	}
	var before int64
	if v := query.Get("before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			writeErr(w, http.StatusBadRequest, "before must be a positive whole number")
			return
		}
		before = n
	}

	// One row more than asked for tells whether there is another page.
	log, err := s.d.Store.ListActivity(r.Context(), store.ActivityQuery{Before: before, Limit: limit + 1})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load the timeline")
		return
	}
	hasMore := len(log) > limit
	if hasMore {
		log = log[:limit]
	}
	entries := make([]timelineEntry, 0, len(log))
	for _, a := range log {
		entries = append(entries, timelineEntryOf(a))
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "hasMore": hasMore})
}

// resumePoint is the id after which the stream starts. A browser that reconnects sends Last-Event-ID, which
// wins. A first connection names the newest entry it already has in ?after=. Without either, the stream
// starts at the end of the log.
func (s *srv) resumePoint(r *http.Request) (int64, error) {
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			return n, nil
		}
	}
	if v := r.URL.Query().Get("after"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return 0, errBadAfter
		}
		return n, nil
	}
	return s.d.Store.LastActivityID(r.Context())
}

// streamActivity follows the activity log: every entry above the resume point, then new ones as they are
// written. The log is only ever appended to and ids grow, so "above the last id sent" misses nothing.
func (s *srv) streamActivity(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	last, err := s.resumePoint(r)
	switch {
	case errors.Is(err, errBadAfter):
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "could not read the activity")
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	// The resume point is fixed before the headers go out, so whatever happens after a client sees them is sent.
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	interval := s.d.ActivityInterval
	if interval <= 0 {
		interval = defaultActivityPoll
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	lastWrite := time.Now()

	for {
		entries, err := s.d.Store.ListActivitySince(r.Context(), last, activityBatch)
		if err != nil {
			return
		}
		for _, a := range entries {
			if writeSSE(w, strconv.FormatInt(a.ID, 10), "activity", timelineEntryOf(a)) != nil {
				return
			}
			last = a.ID
		}
		switch {
		case len(entries) > 0:
			flusher.Flush()
			lastWrite = time.Now()
		case time.Since(lastWrite) >= keepAlive:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
			lastWrite = time.Now()
		}
		if len(entries) == activityBatch {
			continue // a full batch: there may be more waiting
		}

		select {
		case <-ticker.C:
		case <-r.Context().Done():
			return
		}
	}
}
```

In `internal/server/server.go`, replace:

```go
	// Responder diagnoses incidents. When it is nil there are no diagnose, snapshot and limits routes.
	Responder    *responder.Responder
	PollInterval time.Duration // only shown by the limits endpoint
}
```

with:

```go
	// Responder diagnoses incidents. When it is nil there are no diagnose, snapshot and limits routes.
	Responder    *responder.Responder
	PollInterval time.Duration // only shown by the limits endpoint

	// ActivityInterval is how often the activity stream looks for new entries. Zero means one second.
	ActivityInterval time.Duration
}
```

In `internal/server/server.go`, replace:

```go
	mux.HandleFunc("GET /api/runs/{id}/events", s.session(s.streamEvents))
```

with:

```go
	mux.HandleFunc("GET /api/runs/{id}/events", s.session(s.streamEvents))
	mux.HandleFunc("GET /api/activity", s.session(s.listActivity))
	mux.HandleFunc("GET /api/activity/stream", s.session(s.streamActivity))
```

- [ ] **Step 4: Run the tests and watch them pass**

Run: `gofmt -l internal cmd; go vet ./... && go test ./internal/server -race -count=1`
Expected: no output from `gofmt -l`, then `ok`. If `gofmt -l` lists a file, run `gofmt -w` on it and look at the diff: it is only alignment.

- [ ] **Step 5: Mutation checks**

Make each change, run `go test ./internal/server -run 'Activity|LastEventID' -count=1`, expect the named test to fail, and revert it.

1. `hasMore := len(log) > limit` to `hasMore := len(log) >= limit`: `TestListActivityIsNewestFirstAndPaged` fails (the page that holds everything).
2. In `resumePoint`, delete the whole `if v := r.Header.Get("Last-Event-ID"); v != "" { ... }` block: `TestLastEventIDWinsOverAfter` fails.
3. In `resumePoint`, replace the last line `return s.d.Store.LastActivityID(r.Context())` with `return 0, nil`: `TestActivityStreamWithoutAResumePointStartsAtTheEnd` fails (it replays "old").
4. Delete `Repo: a.RepoName, ` from `timelineEntryOf`: `TestListActivityIsNewestFirstAndPaged` fails (the repository name is missing).

- [ ] **Step 6: Smoke test with the real binary**

Run the server on a fresh database, open the stream with `curl`, and write an entry with `sqlite3` while the stream is open. The stream must print the entry within a second or two.

```bash
go build -o bin/remedy-server ./cmd/remedy-server
export REMEDY_ADMIN_PASSWORD='smoke-test-password' REMEDY_RUNNER_TOKEN="$(openssl rand -hex 24)" \
  REMEDY_MASTER_KEY="$(openssl rand -base64 32)" REMEDY_DB="$(mktemp -d)/remedy.db" REMEDY_ADDR=127.0.0.1:8085
./bin/remedy-server > /dev/null 2>&1 &
SERVER=$!
until curl -sf http://127.0.0.1:8085/healthz > /dev/null; do sleep 0.2; done
curl -s -c cookies.txt -H 'X-Remedy-CSRF: 1' -d "{\"password\":\"$REMEDY_ADMIN_PASSWORD\"}" http://127.0.0.1:8085/api/login
curl -sN -b cookies.txt http://127.0.0.1:8085/api/activity/stream > stream.txt &
sleep 1
sqlite3 -cmd '.timeout 5000' "$REMEDY_DB" "INSERT INTO activity (at, kind, summary) VALUES (strftime('%Y-%m-%dT%H:%M:%S','now') || '.000000000Z', 'poll_recovered', 'smoke entry')"
sleep 2
kill -TERM $SERVER; wait $SERVER
cat stream.txt
rm -f cookies.txt stream.txt
```

Expected: `stream.txt` holds `: connected` and an `event: activity` block whose data contains `"summary":"smoke entry"`. (The store reads timestamps with nine fractional digits, as in the insert; any other format makes `GET /api/activity` answer 500.)

- [ ] **Step 7: Run the whole suite and commit**

Run: `go test ./... -race -count=1`
Expected: all packages `ok`.

```bash
git add internal/server
git commit -m "feat(server): serve the activity log as a list and as a live stream" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---
### Task 3: The Timeline page

**Files:**
- Modify: `web/src/api.ts`, `web/src/App.tsx`, `web/src/components/AppLayout.tsx`
- Create: `web/src/timeline.ts`, `web/src/TimelinePage.tsx`

**Interfaces:**
- Consumes: `GET /api/activity` and `GET /api/activity/stream` (Task 2); `request<T>`, `ApiError`, `Link`, `Alert`, `Button`, `Card`, `Skeleton` (existing).
- Produces:
  - `api.listActivity(before?: number): Promise<TimelinePage>` and `streamActivity(after, onEntry, onState): () => void`.
  - `web/src/timeline.ts`: `dayKey`, `dayLabel`, `groupByDay`, `mergeEntries`, `kindDotClass`, `timeOfDay`.
  - The route `/` shows the Timeline (it used to redirect to `/incidents`), and the sidebar lists Timeline first.

There is no web test runner in this repository, so this task is checked by the type checker, the linter and a real browser (Step 6), as the earlier UI tasks were. The logic that could be wrong (grouping, merging, the day labels) is kept in `timeline.ts`, away from the components, and Step 6 exercises each of its cases with seeded data.

- [ ] **Step 1: The API client**

In `web/src/api.ts`, replace:

```ts
export class ApiError extends Error {
```

with:

```ts
/** One line of the timeline. The summary may contain text from GitHub: show it as text, never as HTML. */
export interface TimelineEntry {
  id: number
  at: string
  kind: string
  summary: string
  repo?: string
  incidentId?: number
  runId?: string
}

export interface TimelinePage {
  /** Newest first. */
  entries: TimelineEntry[]
  hasMore: boolean
}

export class ApiError extends Error {
```

In `web/src/api.ts`, replace:

```ts
  getLimits: () => request<Limits>('GET', '/api/limits'),
}
```

with:

```ts
  getLimits: () => request<Limits>('GET', '/api/limits'),

  /** The newest entries, or the ones before the entry with the id `before`. */
  listActivity: (before?: number) =>
    request<TimelinePage>('GET', before === undefined ? '/api/activity' : `/api/activity?before=${before}`),
}

/** 'closed' means the browser gave up, for example because the session ended and the server answered 401. */
export type StreamState = 'live' | 'reconnecting' | 'closed'

/**
 * Follows the activity log from the entry with the id `after` on (0 means from the start). The browser
 * reconnects on its own and then sends Last-Event-ID, which the server prefers to `after`.
 */
export function streamActivity(
  after: number,
  onEntry: (e: TimelineEntry) => void,
  onState: (state: StreamState) => void,
): () => void {
  const es = new EventSource(`/api/activity/stream?after=${after}`)
  es.onopen = () => onState('live')
  es.onerror = () => onState(es.readyState === EventSource.CLOSED ? 'closed' : 'reconnecting')
  es.addEventListener('activity', (m) => onEntry(JSON.parse((m as MessageEvent<string>).data) as TimelineEntry))
  return () => es.close()
}
```

- [ ] **Step 2: The helpers**

Create `web/src/timeline.ts`:

```ts
import type { TimelineEntry } from './api.ts'

export interface DayGroup {
  /** The local calendar day, YYYY-MM-DD. */
  key: string
  entries: TimelineEntry[]
}

function pad(n: number): string {
  return String(n).padStart(2, '0')
}

/** The local calendar day of a point in time, as YYYY-MM-DD. */
export function dayKey(iso: string): string {
  const d = new Date(iso)
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

/** "Today", "Yesterday", or the date written out. */
export function dayLabel(key: string, now: Date = new Date()): string {
  const yesterday = new Date(now)
  yesterday.setDate(now.getDate() - 1)
  if (key === dayKey(now.toISOString())) return 'Today'
  if (key === dayKey(yesterday.toISOString())) return 'Yesterday'
  const [year, month, day] = key.split('-').map(Number)
  return new Date(year, month - 1, day).toLocaleDateString(undefined, {
    weekday: 'long',
    day: 'numeric',
    month: 'long',
    year: 'numeric',
  })
}

/** Groups entries that are sorted newest first into days, keeping the order. */
export function groupByDay(entries: TimelineEntry[]): DayGroup[] {
  const groups: DayGroup[] = []
  for (const entry of entries) {
    const key = dayKey(entry.at)
    const last = groups[groups.length - 1]
    if (last && last.key === key) last.entries.push(entry)
    else groups.push({ key, entries: [entry] })
  }
  return groups
}

/** Adds entries to a list, newest first, and drops duplicates: a live entry may also be in a loaded page. */
export function mergeEntries(current: TimelineEntry[], incoming: TimelineEntry[]): TimelineEntry[] {
  const byId = new Map<number, TimelineEntry>()
  for (const e of current) byId.set(e.id, e)
  for (const e of incoming) byId.set(e.id, e)
  return [...byId.values()].sort((a, b) => b.id - a.id)
}

const kindDots: Record<string, string> = {
  incident_opened: 'bg-rose-500',
  incident_recurred: 'bg-rose-500',
  incident_resolved: 'bg-emerald-500',
  incident_ignored: 'bg-slate-500',
  poll_failed: 'bg-amber-500',
  poll_recovered: 'bg-emerald-500',
  diagnosis_started: 'bg-amber-500',
  diagnosis_finished: 'bg-sky-500',
  diagnosis_failed: 'bg-rose-500',
}

export function kindDotClass(kind: string): string {
  return kindDots[kind] ?? 'bg-slate-500'
}

export function timeOfDay(iso: string): string {
  return new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}
```

- [ ] **Step 3: The page**

Create `web/src/TimelinePage.tsx`:

```tsx
import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api, ApiError, streamActivity } from './api.ts'
import type { StreamState, TimelineEntry } from './api.ts'
import { dayLabel, groupByDay, kindDotClass, mergeEntries, timeOfDay } from './timeline.ts'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'

type Live = 'connecting' | StreamState

const liveText: Record<Live, string> = {
  connecting: 'Connecting…',
  live: 'Live',
  reconnecting: 'Reconnecting…',
  closed: 'Disconnected',
}
const liveDot: Record<Live, string> = {
  connecting: 'bg-amber-500',
  live: 'bg-emerald-500',
  reconnecting: 'bg-amber-500',
  closed: 'bg-rose-500',
}

const linkClass = 'underline decoration-muted-foreground/50 underline-offset-4 hover:text-foreground hover:decoration-foreground'

function TimelineRow({ entry }: { entry: TimelineEntry }) {
  return (
    <li className="flex gap-3 py-3 first:pt-0 last:pb-0">
      <span aria-hidden className={`mt-2 h-2 w-2 shrink-0 rounded-full ${kindDotClass(entry.kind)}`} />
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="break-words whitespace-pre-wrap">{entry.summary}</span>
        <span className="flex flex-wrap gap-x-3 text-xs text-muted-foreground">
          <time dateTime={entry.at} title={new Date(entry.at).toLocaleString()}>
            {timeOfDay(entry.at)}
          </time>
          {entry.repo && <span>{entry.repo}</span>}
          {entry.incidentId !== undefined && (
            <Link to={`/incidents/${entry.incidentId}`} className={linkClass}>
              Incident #{entry.incidentId}
            </Link>
          )}
          {entry.runId !== undefined && (
            <Link to={`/runs/${encodeURIComponent(entry.runId)}`} className={linkClass}>
              Run
            </Link>
          )}
        </span>
      </div>
    </li>
  )
}

export default function TimelinePage() {
  const [entries, setEntries] = useState<TimelineEntry[] | null>(null)
  const [hasMore, setHasMore] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState('')
  const [live, setLive] = useState<Live>('connecting')
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    let active = true
    let stop: (() => void) | undefined
    api
      .listActivity()
      .then((page) => {
        if (!active) return
        setEntries(page.entries)
        setHasMore(page.hasMore)
        setError('')
        // Follow from the newest entry this page has, so that nothing between the list and the stream is lost.
        const newest = page.entries[0]?.id ?? 0
        stop = streamActivity(
          newest,
          (entry) => setEntries((current) => mergeEntries(current ?? [], [entry])),
          setLive,
        )
      })
      .catch((e: unknown) => {
        if (active) setError(e instanceof ApiError ? e.message : 'Could not load the timeline')
      })
    return () => {
      active = false
      stop?.()
    }
  }, [attempt])

  async function loadMore() {
    const oldest = entries?.[entries.length - 1]
    if (!oldest) return
    setLoadingMore(true)
    try {
      const page = await api.listActivity(oldest.id)
      setEntries((current) => mergeEntries(current ?? [], page.entries))
      setHasMore(page.hasMore)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Could not load older entries')
    } finally {
      setLoadingMore(false)
    }
  }

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-2xl font-semibold tracking-tight">Timeline</h1>
        {entries !== null && (
          <span role="status" className="flex items-center gap-2 text-sm text-muted-foreground">
            <span className={`h-2 w-2 rounded-full ${liveDot[live]}`} />
            {liveText[live]}
            {live === 'closed' && (
              <Button variant="outline" size="sm" onClick={() => window.location.reload()}>
                Reload
              </Button>
            )}
          </span>
        )}
      </div>

      {error && (
        <Alert variant="destructive">
          <AlertDescription className="flex flex-wrap items-center justify-between gap-3">
            {error}
            {entries === null && (
              <Button variant="outline" size="sm" onClick={() => setAttempt((n) => n + 1)}>
                Try again
              </Button>
            )}
          </AlertDescription>
        </Alert>
      )}

      {entries === null ? (
        !error && <Skeleton className="h-40 w-full" />
      ) : entries.length === 0 ? (
        <p className="text-muted-foreground">
          Nothing has happened yet. Connect GitHub and add a repository under{' '}
          <Link to="/settings" className={linkClass}>
            Settings
          </Link>
          .
        </p>
      ) : (
        <>
          {groupByDay(entries).map((group) => (
            <section key={group.key} className="flex flex-col gap-2">
              <h2 className="text-sm font-medium text-muted-foreground">{dayLabel(group.key)}</h2>
              <Card>
                <CardContent>
                  <ol className="flex flex-col divide-y divide-border">
                    {group.entries.map((entry) => (
                      <TimelineRow key={entry.id} entry={entry} />
                    ))}
                  </ol>
                </CardContent>
              </Card>
            </section>
          ))}
          {hasMore && (
            <Button variant="outline" className="self-center" disabled={loadingMore} onClick={() => void loadMore()}>
              {loadingMore ? 'Loading…' : 'Load older entries'}
            </Button>
          )}
        </>
      )}
    </div>
  )
}
```

- [ ] **Step 4: The route and the sidebar**

In `web/src/App.tsx`, replace:

```tsx
import SettingsPage from './SettingsPage.tsx'
```

with:

```tsx
import SettingsPage from './SettingsPage.tsx'
import TimelinePage from './TimelinePage.tsx'
```

In `web/src/App.tsx`, replace:

```tsx
        <Route index element={<Navigate to="/incidents" replace />} />
```

with:

```tsx
        <Route index element={<TimelinePage />} />
```

Overwrite `web/src/components/AppLayout.tsx`:

```tsx
import { History, LogOut, Play, Settings, TriangleAlert } from 'lucide-react'
import { NavLink, Outlet } from 'react-router'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

const nav = [
  { to: '/', label: 'Timeline', icon: History, end: true },
  { to: '/incidents', label: 'Incidents', icon: TriangleAlert, end: false },
  { to: '/runs', label: 'Runs', icon: Play, end: false },
  { to: '/settings', label: 'Settings', icon: Settings, end: false },
]

export default function AppLayout({ onSignOut }: { onSignOut: () => void }) {
  return (
    <div className="flex min-h-screen">
      <aside className="flex w-56 shrink-0 flex-col gap-6 border-r border-border bg-card/40 p-4">
        <NavLink to="/" className="px-2 text-xl font-semibold tracking-tight">
          Remedy
        </NavLink>
        <nav className="flex flex-1 flex-col gap-1">
          {nav.map(({ to, label, icon: Icon, end }) => (
            <NavLink
              key={to}
              to={to}
              end={end}
              className={({ isActive }) =>
                cn(
                  'flex items-center gap-2 rounded-lg px-2 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground',
                  isActive && 'bg-accent text-accent-foreground',
                )
              }
            >
              <Icon className="size-4" />
              {label}
            </NavLink>
          ))}
        </nav>
        <Button variant="ghost" size="sm" className="justify-start" onClick={onSignOut}>
          <LogOut /> Sign out
        </Button>
      </aside>
      <main className="min-w-0 flex-1 p-8">
        <div className="mx-auto max-w-5xl">
          <Outlet />
        </div>
      </main>
    </div>
  )
}
```

- [ ] **Step 5: Lint and build**

Run: `make web-install` (a fresh worktree has no `web/node_modules`), then `cd web && npm run lint && npm run build`
Expected: oxlint reports no warnings or errors; `tsc -b` and `vite build` succeed. If `tsc` complains that `year`, `month` or `day` may be `undefined` (`noUncheckedIndexedAccess`), write `const [year = 0, month = 1, day = 1] = key.split('-').map(Number)`.

- [ ] **Step 6: Check it in a real browser**

Build the server with the UI, start it on a fresh database, and seed the database. The seed has one connection, one repository, one incident and one run (so that the links have targets), 60 older entries, 2 from yesterday and 3 from today, which makes 65 and so a second page.

```bash
make build
export REMEDY_ADMIN_PASSWORD='ui-test-password' REMEDY_RUNNER_TOKEN="$(openssl rand -hex 24)" \
  REMEDY_MASTER_KEY="$(openssl rand -base64 32)" REMEDY_DB="$(mktemp -d)/remedy.db" REMEDY_ADDR=127.0.0.1:8080
./bin/remedy-server > server.log 2>&1 &
until curl -sf http://127.0.0.1:8080/healthz > /dev/null; do sleep 0.2; done
sqlite3 -cmd '.timeout 5000' "$REMEDY_DB" <<'SQL'
INSERT INTO github_connections (id, token_ciphertext, token_hint, login, status, status_detail, checked_at)
VALUES (1, x'00', 'seed', 'octo', 'error', 'Seeded for the UI check; polling is off.', '2026-10-02T12:00:00.000000000Z');
INSERT INTO repos (id, connection_id, full_name, default_branch, enabled, created_at)
VALUES (1, 1, 'octo/hello', 'main', 1, '2026-10-02T12:00:00.000000000Z');
INSERT INTO incidents (id, repo_id, ref, ref_url, check_name, state, conclusion, head_sha, check_url, occurrences, diagnoses, first_seen, last_seen, last_diagnosis_at, resolved_at, resolved_reason, diagnosis, diagnosed_sha, run_id)
VALUES (1, 1, 'pr:7', 'https://github.com/octo/hello/pull/7', 'web', 'open', 'failure', 'abc1234def5678', '', 1, 0, '2026-10-02T10:00:00.000000000Z', '2026-10-02T10:00:00.000000000Z', NULL, NULL, '', NULL, '', NULL);
INSERT INTO runs (id, provider, prompt, status, exit_code, result, session_id, cost_usd, created_at, started_at, finished_at, role, incident_id, output, failure_reason, head_sha, automatic)
VALUES ('resp-run-1', 'claude', 'You are Remedy''s responder.', 'succeeded', 0, 'ok', '', 0.06, '2026-10-02T10:00:30.000000000Z', '2026-10-02T10:01:00.000000000Z', '2026-10-02T10:02:00.000000000Z', 'responder', 1, NULL, '', 'abc1234def5678', 1);
-- The timeline is ordered by id, which is the order of writing, so the seed is written oldest first:
-- 60 older entries without a repository (Old entry 60 is the oldest), then yesterday, then today.
WITH RECURSIVE n(i) AS (SELECT 60 UNION ALL SELECT i - 1 FROM n WHERE i > 1)
INSERT INTO activity (at, kind, summary)
SELECT strftime('%Y-%m-%dT%H:%M:%S', 'now', '-4 days', '-' || i || ' minutes') || '.000000000Z', 'poll_recovered', 'Old entry ' || i FROM n;
INSERT INTO activity (at, kind, repo_id, summary) VALUES
  (strftime('%Y-%m-%dT%H:%M:%S', 'now', '-1 day', '-5 minutes') || '.000000000Z', 'repo_added', 1, 'Added repository octo/hello'),
  (strftime('%Y-%m-%dT%H:%M:%S', 'now', '-1 day') || '.000000000Z', 'poll_failed', 1, 'Polling octo/hello failed: rate limited');
-- Today (a few minutes back, so that the day is today in any time zone except right after midnight).
INSERT INTO activity (at, kind, repo_id, incident_id, run_id, summary) VALUES
  (strftime('%Y-%m-%dT%H:%M:%S', 'now', '-30 minutes') || '.000000000Z', 'incident_opened', 1, 1, NULL, 'web failed on PR #7'),
  (strftime('%Y-%m-%dT%H:%M:%S', 'now', '-20 minutes') || '.000000000Z', 'diagnosis_started', 1, 1, 'resp-run-1', 'Diagnosing web on PR #7'),
  (strftime('%Y-%m-%dT%H:%M:%S', 'now', '-10 minutes') || '.000000000Z', 'diagnosis_finished', 1, 1, 'resp-run-1', '<img src=x onerror=alert(1)> npm ci fails: the lock file is out of date');
SQL
```

With the Playwright MCP tools, sign in at `http://127.0.0.1:8080` (password `ui-test-password`) and check, taking a screenshot of each numbered point into the workspace root (and deleting the files afterwards):

1. `/` is the Timeline and the sidebar marks **Timeline** (and only it) as active. Clicking **Incidents**, then **Timeline** again, moves the mark.
2. The page has the heading **Timeline**, a status that says **Live** within a second or two, and three sections: **Today** with 3 entries (newest first: the `<img ...>` entry, then "Diagnosing web on PR #7", then "web failed on PR #7"), **Yesterday** with 2 entries and then a section with a written-out date for the older ones.
3. The `<img src=x ...>` summary is shown as literal text and no dialog opens. The console has no errors.
4. The first entry shows `octo/hello`, a time, and a **Run** link; the first two entries show **Incident #1**. **Incident #1** opens `/incidents/1`; browser back returns to the timeline, which is live again. **Run** opens `/runs/resp-run-1`.
5. The page shows 50 entries and a **Load older entries** button. Clicking it adds the remaining 15 and the button disappears. Scrolling to the end shows `Old entry 60` last.
6. Live update: with the page open (scrolled to the top), run
   `sqlite3 -cmd '.timeout 5000' "$REMEDY_DB" "INSERT INTO activity (at, kind, repo_id, summary) VALUES (strftime('%Y-%m-%dT%H:%M:%S','now') || '.000000000Z', 'poll_recovered', 1, 'Live entry')"`.
   **Live entry** appears at the top of **Today** within about two seconds, without a reload, and is not shown twice.
7. The server goes away: stop it (`kill` the process; `lsof -ti tcp:8080 | xargs kill` finds it). Within a second or two the status becomes **Disconnected** with a **Reload** button (Chromium does not retry a refused connection, and no automatic retry could succeed after a restart anyway: sessions live in memory, so the stream would be answered with 401). Insert an entry into the database while the server is down, start the server again with the same environment and database, and click **Reload**: the sign-in form appears, and after signing in the Timeline is **Live** and shows the entry that was written while the server was down.
8. The `Reconnecting…` state (the browser is retrying after a dropped connection) could not be provoked with the browser tools: stopping the server ends in **Disconnected**, and an offline emulation does not cut a stream that is already open. The resume itself is covered by the server test `TestLastEventIDWinsOverAfter`. Do not claim it was checked in the browser.
9. Narrow the window to 400 px: the sidebar and the rows do not overflow horizontally, and the long summary wraps.
10. A fresh, empty database (a new `REMEDY_DB`): the Timeline shows "Nothing has happened yet. Connect GitHub and add a repository under Settings." and the **Settings** link works. Once the list has loaded the status shows **Live**; while it loads there is a skeleton and no status.

Stop the server afterwards (`lsof -ti tcp:8080 | xargs kill`), and delete `server.log` and any screenshots.

- [ ] **Step 7: Commit**

Run: `make check`
Expected: everything passes.

```bash
git add web
git commit -m "feat(web): show the activity as a live timeline on the home page" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---
### Task 4: A read-only guard and an audit for the GitHub client, and the log level

**Files:**
- Create: `internal/github/audit.go`, `internal/github/audit_test.go`, `internal/github/audit_internal_test.go`, `internal/config/loglevel.go`, `internal/config/loglevel_test.go`
- Modify: `internal/github/client.go`, `internal/app/app.go`, `cmd/remedy-server/main.go`

**Interfaces:**
- Consumes: `github.New`, `secret.Value` (existing).
- Produces:
  - `github.Option` and `github.WithLog(*slog.Logger) Option`; `github.New(baseURL, token, httpClient, opts ...Option)`. Existing callers keep working.
  - Every request of a `Client` goes through a transport that returns an error for any method except `GET` and `HEAD` without sending anything.
  - With a logger at debug level, each request is logged as `github request method=GET host=api.github.com path=/repos/o/r/pulls status=200 took=12ms`. The path is logged for the API host only; the query, the headers and the path of any other host (the signed log download) are never logged.
  - `config.LogLevelFromEnv(get func(string) string) (slog.Level, error)` for `REMEDY_LOG_LEVEL` (`debug`, `info`, `warn`, `error`; default `info`).

- [ ] **Step 1: Write the failing tests**

Create `internal/github/audit_test.go`:

```go
package github_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/github"
	"github.com/Jaydee94/remedy/internal/secret"
)

func loggedClient(t *testing.T, level slog.Level, h http.HandlerFunc) (*github.Client, *bytes.Buffer) {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: level}))
	return github.New(ts.URL, secret.NewValue(token), ts.Client(), github.WithLog(log)), &buf
}

func TestDebugLogShowsEveryRequestWithoutQueryOrToken(t *testing.T) {
	c, buf := loggedClient(t, slog.LevelDebug, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	if _, err := c.ListOpenPRs(context.Background(), "o/r"); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	for _, want := range []string{"github request", "method=GET", "path=/repos/o/r/pulls", "status=200", "took="} {
		if !strings.Contains(out, want) {
			t.Errorf("the log lacks %q:\n%s", want, out)
		}
	}
	for _, bad := range []string{"per_page", "state=open", token, "Bearer", "Authorization"} {
		if strings.Contains(out, bad) {
			t.Errorf("the log contains %q:\n%s", bad, out)
		}
	}
}

func TestDebugLogNamesTheHostOfARedirectButNotItsPathOrSignature(t *testing.T) {
	blob := blobServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("##[error]boom\n"))
	})
	c, buf := loggedClient(t, slog.LevelDebug, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, blob+"/logs/abc?sig=SIGNEDSECRET", http.StatusFound)
	})
	if _, _, err := c.GetJobLogs(context.Background(), "o/r", 1); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if !strings.Contains(out, "path=/repos/o/r/actions/jobs/1/logs") || !strings.Contains(out, "status=302") {
		t.Errorf("the API request is missing:\n%s", out)
	}
	if !strings.Contains(out, "host=localhost:") || !strings.Contains(out, "status=200") {
		t.Errorf("the request to the other host is missing:\n%s", out)
	}
	for _, bad := range []string{"SIGNEDSECRET", "/logs/abc", "sig="} {
		if strings.Contains(out, bad) {
			t.Errorf("the log contains %q:\n%s", bad, out)
		}
	}
}

func TestInfoLevelLogsNoRequests(t *testing.T) {
	c, buf := loggedClient(t, slog.LevelInfo, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	if _, err := c.ListOpenPRs(context.Background(), "o/r"); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("the log at info level is not empty:\n%s", buf.String())
	}
}
```

Create `internal/github/audit_internal_test.go`:

```go
package github

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Jaydee94/remedy/internal/secret"
)

func TestTheTransportRefusesEverythingButGetAndHead(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(ts.Close)
	var buf bytes.Buffer
	c := audited(ts.Client(), "", slog.New(slog.NewTextHandler(&buf, nil)))

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
		req, _ := http.NewRequest(method, ts.URL+"/repos/o/r/issues", strings.NewReader("{}"))
		if resp, err := c.Do(req); err == nil {
			_ = resp.Body.Close()
			t.Errorf("%s was sent", method)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("the server saw %d requests, want none", hits.Load())
	}
	if !strings.Contains(buf.String(), "refused") {
		t.Errorf("the refusals were not logged:\n%s", buf.String())
	}

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		req, _ := http.NewRequest(method, ts.URL+"/repos/o/r", nil)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		_ = resp.Body.Close()
	}
	if hits.Load() != 2 {
		t.Fatalf("the server saw %d requests, want the GET and the HEAD", hits.Load())
	}
}

func TestNewGuardsTheAPIClientAndTheDownloadClient(t *testing.T) {
	clients := map[string]*Client{
		"default clients": New("https://api.github.com", secret.NewValue("t"), nil),
		"a given client":  New("https://api.github.com", secret.NewValue("t"), &http.Client{}),
	}
	for name, c := range clients {
		if _, ok := c.http.Transport.(*auditTransport); !ok {
			t.Errorf("%s: the API client is not guarded", name)
		}
		if _, ok := c.stream.Transport.(*auditTransport); !ok {
			t.Errorf("%s: the download client is not guarded", name)
		}
	}
}
```

Create `internal/config/loglevel_test.go`:

```go
package config_test

import (
	"log/slog"
	"testing"

	"github.com/Jaydee94/remedy/internal/config"
)

func TestLogLevelFromEnv(t *testing.T) {
	for value, want := range map[string]slog.Level{
		"":        slog.LevelInfo,
		"info":    slog.LevelInfo,
		"debug":   slog.LevelDebug,
		"DEBUG":   slog.LevelDebug,
		" warn ":  slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
	} {
		got, err := config.LogLevelFromEnv(env(map[string]string{"REMEDY_LOG_LEVEL": value}))
		if err != nil || got != want {
			t.Errorf("REMEDY_LOG_LEVEL=%q gives %v, %v, want %v", value, got, err, want)
		}
	}
	for _, bad := range []string{"verbose", "trace", "1"} {
		if _, err := config.LogLevelFromEnv(env(map[string]string{"REMEDY_LOG_LEVEL": bad})); err == nil {
			t.Errorf("REMEDY_LOG_LEVEL=%q was accepted", bad)
		}
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/github ./internal/config 2>&1 | head -20`
Expected: neither package compiles (`undefined: github.WithLog`, `undefined: audited`, `undefined: auditTransport`, `undefined: config.LogLevelFromEnv`).

- [ ] **Step 3: Implement the transport and the log level**

Create `internal/github/audit.go`:

```go
package github

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// Option changes how New builds a Client.
type Option func(*options)

type options struct{ log *slog.Logger }

// WithLog makes the client log every request at debug level: the method, the host, the status and the
// duration, and the path for the API host. It never logs a query (a redirect to a download carries a signed
// address in it), a header, or the path of another host.
func WithLog(log *slog.Logger) Option { return func(o *options) { o.log = log } }

// auditTransport is what every request of a Client goes through. It is the second line of defence behind
// the read-only methods of Client: whatever calls it, nothing but GET and HEAD leaves the process.
type auditTransport struct {
	base    http.RoundTripper
	apiHost string
	log     *slog.Logger // nil when nothing is logged
}

func (t *auditTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		if t.log != nil {
			t.log.Error("github request refused: the client is read-only", "method", req.Method, "host", req.URL.Host)
		}
		return nil, fmt.Errorf("github: refusing to send %s: the client is read-only", req.Method)
	}

	started := time.Now()
	resp, err := t.base.RoundTrip(req)
	if t.log != nil {
		attrs := []any{"method", req.Method, "host", req.URL.Host}
		if req.URL.Host == t.apiHost {
			attrs = append(attrs, "path", req.URL.Path)
		}
		if err != nil {
			attrs = append(attrs, "failed", true) // the error text may contain the address
		} else {
			attrs = append(attrs, "status", resp.StatusCode)
		}
		attrs = append(attrs, "took", time.Since(started).Round(time.Millisecond))
		t.log.Debug("github request", attrs...)
	}
	return resp, err
}

// audited returns a copy of c whose requests go through an auditTransport.
func audited(c *http.Client, apiHost string, log *slog.Logger) *http.Client {
	guarded := *c
	base := guarded.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	guarded.Transport = &auditTransport{base: base, apiHost: apiHost, log: log}
	return &guarded
}
```

In `internal/github/client.go`, replace:

```go
	"net/http"
	"regexp"
```

with:

```go
	"net/http"
	"net/url"
	"regexp"
```

In `internal/github/client.go`, replace:

```go
// New returns a client for baseURL (for example https://api.github.com). A nil httpClient gets a
// 20 second timeout.
func New(baseURL string, token secret.Value, httpClient *http.Client) *Client {
	stream := httpClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
		stream = &http.Client{} // a download can take longer than 20 seconds; the caller's context limits it
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    httpClient,
		stream:  stream,
		cache:   map[string]cacheEntry{},
	}
}
```

with:

```go
// New returns a client for baseURL (for example https://api.github.com). A nil httpClient gets a
// 20 second timeout. Every request goes through a transport that refuses anything but GET and HEAD and,
// with WithLog, logs it.
func New(baseURL string, token secret.Value, httpClient *http.Client, opts ...Option) *Client {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	baseURL = strings.TrimRight(baseURL, "/")
	apiHost := ""
	if u, err := url.Parse(baseURL); err == nil {
		apiHost = u.Host
	}

	stream := httpClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
		stream = &http.Client{} // a download can take longer than 20 seconds; the caller's context limits it
	}
	shared := stream == httpClient
	httpClient = audited(httpClient, apiHost, o.log)
	if shared {
		stream = httpClient
	} else {
		stream = audited(stream, apiHost, o.log)
	}
	return &Client{
		baseURL: baseURL,
		token:   token,
		http:    httpClient,
		stream:  stream,
		cache:   map[string]cacheEntry{},
	}
}
```

Create `internal/config/loglevel.go`:

```go
package config

import (
	"errors"
	"log/slog"
	"strings"
)

// LogLevelFromEnv reads REMEDY_LOG_LEVEL: debug, info, warn or error, in any case. The default is info.
// At debug level the server logs every request to GitHub.
func LogLevelFromEnv(get func(string) string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(get("REMEDY_LOG_LEVEL"))) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, errors.New("REMEDY_LOG_LEVEL must be debug, info, warn or error")
}
```

- [ ] **Step 4: Use them in the server**

In `internal/app/app.go`, replace:

```go
	reader := func(token secret.Value) *github.Client { return github.New(cfg.GitHubAPIURL, token, nil) }
```

with:

```go
	reader := func(token secret.Value) *github.Client {
		return github.New(cfg.GitHubAPIURL, token, nil, github.WithLog(log))
	}
```

In `cmd/remedy-server/main.go`, replace:

```go
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfg, err := config.ServerFromEnv(os.Getenv)
```

with:

```go
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	level, err := config.LogLevelFromEnv(os.Getenv)
	if err != nil {
		log.Error("invalid configuration", "err", err)
		os.Exit(2)
	}
	log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	cfg, err := config.ServerFromEnv(os.Getenv)
```

- [ ] **Step 5: Run the tests and watch them pass**

Run: `gofmt -l internal cmd; go vet ./... && go test ./internal/github ./internal/config ./internal/app -race -count=1`
Expected: no output from `gofmt -l`, then `ok` for all three. The existing `TestOnlyReadMethodsAreExported` still passes: `Option` and `WithLog` are not methods of `Client`.

- [ ] **Step 6: Mutation checks**

Make each change, run `go test ./internal/github -count=1`, expect the named test to fail, and revert it.

1. In `RoundTrip`, change the guard to `if req.Method != http.MethodGet && req.Method != http.MethodHead && req.Method != http.MethodPost {`: `TestTheTransportRefusesEverythingButGetAndHead` fails (a POST was sent).
2. Delete the `if req.URL.Host == t.apiHost {` line and its closing brace so that the path is always logged: `TestDebugLogNamesTheHostOfARedirectButNotItsPathOrSignature` fails.
3. Append `"query", req.URL.RawQuery` to the first `attrs` list: `TestDebugLogShowsEveryRequestWithoutQueryOrToken` fails.
4. In `New`, delete the line `httpClient = audited(httpClient, apiHost, o.log)`: `TestNewGuardsTheAPIClientAndTheDownloadClient` fails for both cases (the API client is no longer guarded).

- [ ] **Step 7: Smoke test with the real binary**

Start the server at debug level against an address that refuses connections, so that no request leaves the machine, and let it make one GitHub request by saving a token. Then check an invalid level.

```bash
go build -o bin/remedy-server ./cmd/remedy-server
export REMEDY_ADMIN_PASSWORD='smoke-test-password' REMEDY_RUNNER_TOKEN="$(openssl rand -hex 24)" \
  REMEDY_MASTER_KEY="$(openssl rand -base64 32)" REMEDY_DB="$(mktemp -d)/remedy.db" REMEDY_ADDR=127.0.0.1:8085 \
  REMEDY_GITHUB_API_URL=http://127.0.0.1:9
REMEDY_LOG_LEVEL=debug ./bin/remedy-server > smoke.log 2>&1 &
SERVER=$!
until curl -sf http://127.0.0.1:8085/healthz > /dev/null; do sleep 0.2; done
curl -s -c cookies.txt -H 'X-Remedy-CSRF: 1' -d "{\"password\":\"$REMEDY_ADMIN_PASSWORD\"}" http://127.0.0.1:8085/api/login
curl -s -b cookies.txt -X PUT -H 'X-Remedy-CSRF: 1' -d '{"token":"ghp_SMOKETESTTOKEN0123456789abcdefghijkl"}' http://127.0.0.1:8085/api/github/connection
kill -TERM $SERVER; wait $SERVER
grep 'github request' smoke.log
grep -c 'ghp_SMOKETESTTOKEN' smoke.log
REMEDY_LOG_LEVEL=verbose ./bin/remedy-server; echo "exit with a bad level: $?"
rm -f smoke.log cookies.txt
```

Expected: the `grep` shows one line like `level=DEBUG msg="github request" method=GET host=127.0.0.1:9 path=/user failed=true took=0s`; the count of the token is `0`; the bad level prints `invalid configuration` with the message about `REMEDY_LOG_LEVEL` and exits with `2`.

- [ ] **Step 8: Run the whole suite and commit**

Run: `go test ./... -race -count=1`
Expected: all packages `ok`.

```bash
git add internal cmd
git commit -m "feat(github): refuse every request but GET and HEAD, and log requests at debug level" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 5: The runbook, the leak check, and the real run

**Files:**
- Create: `scripts/check-no-token-leak.sh`, `docs/runbook/first-real-run.md`, `docs/research/phase-1-real-run.md`
- Modify: `README.md`, `CLAUDE.md`, `docs/specs/2026-10-02-phase-1-detect-and-diagnose-design.md`, `docs/design.md`

**Interfaces:**
- Consumes: everything of tasks 1 to 4 and plans 1a to 1c.
- Produces: a script that searches logs, the database file and every admin API response for a token; the runbook; the record of the real run; the status of the documents.

This task has steps that only the maintainer can do (creating the token, entering it, confirming the deliberate failure). Steps marked **(maintainer)** are theirs; the assistant never sees the token, never reads it from `gh auth token` or the keychain, and never enters it. Steps marked **(assistant)** are done by the one who executes this plan.

- [ ] **Step 1: (assistant) Write the leak check**

The success criteria say that the token appears in no API response and no log. A check that cannot fail proves nothing, so the script tests itself: it plants the token in a probe file and refuses to continue if its own search does not find it.

Create `scripts/check-no-token-leak.sh`:

```sh
#!/bin/sh
# Checks that a GitHub token appears nowhere Remedy writes or serves: in the files you name (the logs, the
# database file) and in every admin API response, including the run streams.
#
#   REMEDY_ADMIN_PASSWORD=... scripts/check-no-token-leak.sh <base-url> <file>...
#
# The token is read from standard input, with echo turned off on a terminal, so it never lands in the shell
# history or in the argument list of another process. Needs curl and jq. Exits 0 when nothing was found, 1 when
# the token was found, 2 when the check itself could not run.
set -u

base=${1:?usage: check-no-token-leak.sh <base-url> <file>...}
shift
[ "$#" -gt 0 ] || { echo "name at least one file to search" >&2; exit 2; }
[ -n "${REMEDY_ADMIN_PASSWORD:-}" ] || { echo "REMEDY_ADMIN_PASSWORD must be set" >&2; exit 2; }
command -v jq > /dev/null || { echo "jq is needed" >&2; exit 2; }

printf 'GitHub token to look for (input is hidden): ' >&2
[ -t 0 ] && stty -echo
IFS= read -r token
[ -t 0 ] && stty echo
echo >&2
[ "${#token}" -ge 8 ] || { echo "that is too short to search for" >&2; exit 2; }

for f in "$@"; do
  [ -r "$f" ] || { echo "cannot read $f" >&2; exit 2; }
done

work=$(mktemp -d)
chmod 700 "$work"
trap 'rm -rf "$work"' EXIT
printf '%s\n' "$token" > "$work/pattern"
unset token

# count prints how many lines of the file contain the token.
count() { grep -a -c -F -f "$work/pattern" "$1"; }

# The search must be able to find something.
{ echo "noise"; cat "$work/pattern"; echo "noise"; } > "$work/probe"
[ "$(count "$work/probe")" = "1" ] || { echo "the search does not find a planted token: not trusting it" >&2; exit 2; }

leaks=0
report() { # report <name> <count>
  if [ "$2" != "0" ]; then
    echo "LEAK  $1 ($2 lines)"
    leaks=$((leaks + 1))
  else
    echo "ok    $1"
  fi
}

for f in "$@"; do
  report "$f" "$(count "$f")"
done

printf '{"password":"%s"}' "$REMEDY_ADMIN_PASSWORD" |
  curl -sf -c "$work/cookies" -H 'X-Remedy-CSRF: 1' --data-binary @- "$base/api/login" > /dev/null ||
  { echo "could not sign in at $base" >&2; exit 2; }

# fetch <path> <file> saves an answer. A stream that never ends is cut off after 10 seconds (curl exit 28);
# what arrived until then is still searched.
fetch() {
  curl -sfN --max-time 10 -b "$work/cookies" "$base$1" > "$2"
  rc=$?
  [ "$rc" -eq 0 ] || [ "$rc" -eq 28 ] || { echo "GET $1 failed (curl exit $rc)" >&2; exit 2; }
}

check_endpoint() {
  fetch "$1" "$work/answer"
  report "GET $1" "$(count "$work/answer")"
}

for path in /api/github/connection /api/repos "/api/incidents?state=all" "/api/activity?limit=200" /api/runs /api/limits; do
  check_endpoint "$path"
done

fetch "/api/incidents?state=all" "$work/incidents.json"
for id in $(jq -r '.[].id' "$work/incidents.json"); do
  check_endpoint "/api/incidents/$id"
done
fetch "/api/runs" "$work/runs.json"
for id in $(jq -r '.[].id' "$work/runs.json"); do
  check_endpoint "/api/runs/$id"
  check_endpoint "/api/runs/$id/events"
done

if [ "$leaks" -gt 0 ]; then
  echo "the token was found in $leaks places" >&2
  exit 1
fi
echo "the token appears nowhere that was searched"
```

Make it executable and check it. Start a fresh server and seed it as in Task 3, Step 6 (the server on port 8080, its log in `server.log`, `REMEDY_DB` and `REMEDY_ADMIN_PASSWORD` exported there), then:

```bash
chmod +x scripts/check-no-token-leak.sh
FAKE='ghp_FAKELEAKCHECKTOKEN0123456789abcdef'

# 1. A clean system: exit 0.
printf '%s\n' "$FAKE" | scripts/check-no-token-leak.sh http://127.0.0.1:8080 server.log "$REMEDY_DB"; echo "exit: $?"

# 2. The token in a log file: exit 1, and the log is named.
cp server.log leaked.log; echo "token=$FAKE" >> leaked.log
printf '%s\n' "$FAKE" | scripts/check-no-token-leak.sh http://127.0.0.1:8080 leaked.log; echo "exit: $?"

# 3. The token in something an API serves: exit 1, and the endpoint is named.
sqlite3 -cmd '.timeout 5000' "$REMEDY_DB" "INSERT INTO activity (at, kind, summary) VALUES (strftime('%Y-%m-%dT%H:%M:%S','now') || '.000000000Z', 'poll_failed', 'oops $FAKE')"
printf '%s\n' "$FAKE" | scripts/check-no-token-leak.sh http://127.0.0.1:8080 server.log; echo "exit: $?"

# 4. A token that is too short, and a file that is missing: exit 2.
printf 'abc\n' | scripts/check-no-token-leak.sh http://127.0.0.1:8080 server.log; echo "exit: $?"
printf '%s\n' "$FAKE" | scripts/check-no-token-leak.sh http://127.0.0.1:8080 /nonexistent; echo "exit: $?"
rm -f leaked.log
```

Expected: 1 prints `ok` lines for `server.log`, the database file and every endpoint, ends with `the token appears nowhere that was searched` and exit `0`; 2 prints `LEAK  leaked.log (1 lines)` and exits `1`; 3 prints `LEAK  GET /api/activity?limit=200 (1 lines)` and exits `1`; both lines of 4 exit `2`. Stop the server and delete `server.log`, `leaked.log` and the temporary database afterwards. If 3 exits `0`, the loop's count is lost: fix the script before going on.

Run `sh -n scripts/check-no-token-leak.sh` (syntax) and, if `shellcheck` is installed, `shellcheck scripts/check-no-token-leak.sh`.

- [ ] **Step 2: (assistant) Write the runbook**

Create `docs/runbook/first-real-run.md`:

````markdown
# Runbook: the first run against the real GitHub

This runs everything of phase 1 once against a real repository, and checks the success criteria of the
[spec](../specs/2026-10-02-phase-1-detect-and-diagnose-design.md#12-success-criteria). It uses the Remedy
repository itself. Nothing in it writes to GitHub from Remedy; the only writes are the ones you make with your
own `git` and `gh` to create and fix a deliberately red pull request.

Plan on about an hour, most of it waiting for CI. The run uses subscription quota: a diagnosis is one agent
run, and the limits of the quick start (at most 3 per incident, 20 per day) apply.

## 1. The token

Create a **fine-grained personal access token** at <https://github.com/settings/personal-access-tokens/new>:

- Resource owner: your account. Repository access: **only** `Jaydee94/remedy`.
- Repository permissions, all **read-only**: Metadata, Contents, Pull requests, Actions, Checks.
- Nothing else, no account permissions. A short expiry (7 days) is enough.

Do not paste the token into a terminal, a chat or a file. You enter it once, in the Remedy UI.

## 2. Build and start

```sh
make web-install && make build
mkdir -p ~/remedy-real-run && cd ~/remedy-real-run
export REMEDY_ADMIN_PASSWORD='choose-a-long-password'
export REMEDY_RUNNER_TOKEN="$(openssl rand -hex 24)"
export REMEDY_MASTER_KEY="$(openssl rand -base64 32)"
export REMEDY_DB="$PWD/remedy.db" REMEDY_LOG_LEVEL=debug REMEDY_POLL_INTERVAL=30s

<path-to-remedy>/bin/remedy-server > server.log 2>&1 &
<path-to-remedy>/bin/remedy-runner > runner.log 2>&1 &
```

The `claude` CLI must be installed and logged in (`claude`, then `/login`). The shell that starts the runner must
not have `ANTHROPIC_API_KEY` set to anything you want billed; the runner removes it from the CLI's environment
anyway.

## 3. Connect and register

Open <http://localhost:8080>, sign in, go to **Settings**, paste the token into the GitHub connection field and
save, then add the repository `Jaydee94/remedy`. The **Timeline** (home) shows "connected" and "added" entries.

## 4. A real red check: the Renovate pull request

Pull request 20 (the TypeScript 7 update) fails `npm ci` while it is open; if it has been closed or fixed since, skip to section 5. Within about a minute of adding the
repository the Timeline shows an **incident opened** entry for it, then **diagnosing**, and a few minutes later
**diagnosis finished**. Open the incident: the diagnosis should name the lock file mismatch in `web/`.

## 5. A deliberately red pull request, fixed again

This part pushes to GitHub with your own credentials. From a clean checkout of Remedy:

```sh
git switch -c real-run/red-check origin/main
cat > internal/store/zz_real_run_test.go <<'EOF'
package store_test

import "testing"

func TestDeliberatelyRedForTheRealRun(t *testing.T) { t.Fatal("deliberately red: remove this file") }
EOF
git add internal/store/zz_real_run_test.go
git commit -m "test: deliberately red check for the Remedy real run (do not merge)"
git push -u origin real-run/red-check
gh pr create --title "do not merge: deliberately red check for the Remedy real run" \
  --body "Opened on purpose to see Remedy detect, diagnose and resolve a red check. Closed without merging."
```

When CI has failed, the Timeline shows an incident for the new pull request and, shortly after, its diagnosis;
the diagnosis should name `internal/store/zz_real_run_test.go`. Then fix it:

```sh
git rm internal/store/zz_real_run_test.go
git commit -m "test: remove the deliberately red check"
git push
```

When CI is green, the incident resolves with the reason "the check turned green", and the Timeline shows it.
Finally close the pull request without merging and delete the branch:

```sh
gh pr close --delete-branch
git switch main && git branch -D real-run/red-check
```

## 6. The audit

- Writes: `grep 'github request' server.log | grep -vc 'method=GET'` must print `0`, and
  `grep -c 'refused' server.log` must print `0`.
- Requests: `grep -c 'github request' server.log` and, per status, `grep -o 'status=[0-9]*' server.log | sort | uniq -c`.
- The token: `scripts/check-no-token-leak.sh http://localhost:8080 server.log runner.log "$REMEDY_DB"` (from the
  Remedy checkout, with `REMEDY_ADMIN_PASSWORD` set). It asks for the token with the input hidden, and searches the
  logs, the database file and every admin API response. It must end with "the token appears nowhere that was searched".

## 7. Clean up

Stop the server and the runner. Revoke the token at <https://github.com/settings/personal-access-tokens> unless
you keep using it. `~/remedy-real-run` holds the database (with the sealed token) and the logs; delete it when
you no longer need it.
````

- [ ] **Step 3: (maintainer) Create the token and start the processes**

Follow sections 1 to 3 of the runbook. The assistant may run the build and start the two processes (section 2) with a generated runner token and master key, because those are not the maintainer's credentials; **the maintainer** enters the GitHub token in the UI in their own browser. Wait until the maintainer says the repository is added.

- [ ] **Step 4: (assistant) Watch the real incident**

Watch `~/remedy-real-run/server.log` and, with the Playwright tools, the Timeline and the incident page. Check section 4 of the runbook: an incident for pull request 20, an automatic diagnosis, the diagnosis card (cause, confidence, affected files, proposed fix), the live status on the Timeline, and that the Timeline order matches the log. Take screenshots into the workspace root, keep the ones worth keeping out of the repository (they may show repository data), and delete the files afterwards.

Look at the facts, not at the happy path: how long it took from the check going red to the incident, from the incident to the diagnosis, the cost the run reports, whether the diagnosis is right. Write them down for Step 6.

- [ ] **Step 5: (maintainer) Confirm the deliberate failure, then (assistant) run section 5**

Ask the maintainer: "May I push a branch `real-run/red-check` with one deliberately failing test to `Jaydee94/remedy`, open a pull request that I close without merging, and delete the branch afterwards?" Only on a yes, run section 5 of the runbook. Never merge that pull request. Record the times: pushed, CI red, incident opened, diagnosis finished, fix pushed, CI green, incident resolved.

If CI does not fail on the test file (for example because the workflow only runs on a path filter), say so and look at `.github/workflows` before trying anything else; do not weaken the test or edit the workflow to make it fail.

- [ ] **Step 6: (assistant) Run the audit and record the result**

Run section 6 of the runbook. Ask the maintainer to run the leak check themselves, with the `!` prefix so that the token stays in their terminal: `! REMEDY_ADMIN_PASSWORD=... scripts/check-no-token-leak.sh http://localhost:8080 ~/remedy-real-run/server.log ~/remedy-real-run/runner.log ~/remedy-real-run/remedy.db`. Then create `docs/research/phase-1-real-run.md` with these headings, filled with what was observed (numbers, times and the diagnoses as they were, nothing rounded up):

```markdown
# Phase 1 run against the real GitHub

Date, Remedy commit, `claude --version`, the model, the poll interval.

## What was run
The token permissions, the repository, the two cases (pull request 20, the deliberately red pull request).

## Timeline of the deliberately red pull request
| Time | Event |
A table with the seven times of Step 5 and the Timeline entry that shows each.

## Diagnoses
For each case: the summary, cause, confidence and affected files as stored, and whether they were right.

## Cost and duration
The cost the CLI reported per run, the duration of each run, the number of automatic runs.

## Audit
Request counts by method and status, the count of writes (expected 0), the count of refusals (expected 0),
the output of the leak check.

## Success criteria
One line per criterion of section 12 of the spec: met or not met, with the evidence above.

## Problems found
Each thing that went wrong or surprised, what caused it, and where it was fixed or filed.
```

If a criterion is **not met**, say so in the record and do not write "done" anywhere else in this task. Fix the cause in a separate pull request with a test first (the normal cycle), repeat the part that failed, and update the record.

- [ ] **Step 7: (assistant) Update the documents**

Do this only for what the record shows; where a criterion is not met, say what is open instead.

In `README.md`, replace:

```markdown
> within limits or by a click. The timeline, the approval gatekeeper and the learning graph come next (see
```

with:

```markdown
> within limits or by a click. Everything Remedy does shows up in a live **timeline**, and the GitHub
> client is read-only down to its HTTP transport. The fixer, the approval gatekeeper and the learning graph come next (see
```

In `README.md`, replace:

```markdown
- [`docs/plans/`](docs/plans/): implementation plans per phase
```

with:

```markdown
- [`docs/plans/`](docs/plans/): implementation plans per phase
- [`docs/runbook/first-real-run.md`](docs/runbook/first-real-run.md): a first run against a real repository, step by step, and
  [`docs/research/phase-1-real-run.md`](docs/research/phase-1-real-run.md): what it showed
```

In `README.md`, replace:

```markdown
Open <http://localhost:8080>, sign in, start a run. The agent is read-only in this phase.
```

with:

```markdown
Open <http://localhost:8080> and sign in. The home page is the **Timeline**, a live feed of everything Remedy
does: incidents, diagnoses, polling problems, changes to the settings. Under **Runs** you can start a read-only
agent run by hand. `REMEDY_LOG_LEVEL=debug` (default `info`) logs every request to GitHub with its method, host,
path and status, never a query or the token; the GitHub client refuses to send anything but `GET` and `HEAD`.
```

In `CLAUDE.md`, replace:

```markdown
`REMEDY_DIAGNOSE_MAX_PER_DAY` (20, `0` turns it off). See the README quick start.
```

with:

```markdown
`REMEDY_DIAGNOSE_MAX_PER_DAY` (20, `0` turns it off). `REMEDY_LOG_LEVEL` (server, default `info`; `debug` logs every GitHub request). See the README quick start.
```

In `CLAUDE.md`, replace:

```markdown
The `github` client is read-only: every exported method is a `Get*` or `List*`, enforced by a test.
```

with:

```markdown
The `github` client is read-only: every exported method is a `Get*` or `List*`, enforced by a test, and `github.New` wraps the HTTP transport so that any method except `GET` and `HEAD` is refused before it is sent.
```

In `CLAUDE.md`, replace:

```markdown
that ordering is what guarantees a client sees every event before `done`.
```

with:

```markdown
that ordering is what guarantees a client sees every event before `done`. The Timeline reads the append-only `activity` table, and its SSE stream follows that table by id (it polls, there is no hub): write activity in the same transaction as the state change it describes, and never reuse or backfill ids.
```

In `CLAUDE.md`, replace:

```markdown
 and 1c (the responder: diagnosis of an incident by a read-only agent) are implemented; the timeline with the real run against GitHub (1d) is next.
```

with:

```markdown
, 1c (the responder: diagnosis of an incident by a read-only agent) and 1d (the timeline, and the run against the real GitHub that `docs/research/phase-1-real-run.md` records) are implemented; the fixer (part C of the spec) is next.
```

In `docs/specs/2026-10-02-phase-1-detect-and-diagnose-design.md`, replace:

```markdown
(step 7), all implemented; the plan for the remaining steps follows.
```

with:

```markdown
(step 7) and [`phase-1d`](../plans/phase-1d-timeline-and-real-run.md) (steps 8 and 9), all implemented. The run against the real GitHub is recorded in [`phase-1-real-run.md`](../research/phase-1-real-run.md).
```

In `docs/specs/2026-10-02-phase-1-detect-and-diagnose-design.md`, replace:

```markdown
- `GET /api/activity?before=&limit=` and a live stream `GET /api/activity/stream` (SSE, resumable with
  `Last-Event-ID`, like the run stream)
```

with:

```markdown
- `GET /api/activity?before=&limit=` answers `{entries, hasMore}`, newest first (`limit` 1 to 200, default 50).
  `GET /api/activity/stream?after=` is an SSE stream of the entries above `after`, oldest first. A reconnecting
  browser resumes with `Last-Event-ID`, which wins over `after`; without either the stream starts at the end of
  the log. The stream follows the table by id once a second instead of waking on writes, because the entries are
  written from many places.
```

In `docs/specs/2026-10-02-phase-1-detect-and-diagnose-design.md`, replace:

```markdown
- GitHub's API shapes and rate limits are only exercised against fixtures (taken from real answers) until step 9.
```

with:

```markdown
- GitHub's API shapes and rate limits were only exercised against fixtures (taken from real answers) until step 9;
  [`phase-1-real-run.md`](../research/phase-1-real-run.md) records what the real service showed. The client's
  transport also refuses every method but `GET` and `HEAD`, and `REMEDY_LOG_LEVEL=debug` logs each request.
```

In `docs/design.md`, replace:

```markdown
Phase plans live in [`plans/`](plans/).
```

with:

```markdown
Phase plans live in [`plans/`](plans/). Phase 1 is delivered in parts: the first part (detect and diagnose, with the
timeline) is implemented; the fixer that changes a workspace and lets the control plane open a pull request, and
the Home Assistant notification, are separate later cycles.
```

- [ ] **Step 8: Check and commit**

Run: `make check`
Expected: everything passes (the documents and the script are not part of it; `sh -n` on the script was run in Step 1).

Check that none of the new or changed files contains a token, a password or a real runner token: `git diff --cached | grep -E 'ghp_|github_pat_'` must print nothing (the fake `ghp_FAKELEAKCHECKTOKEN...` appears only in this plan, not in the committed files).

```bash
git add scripts docs README.md CLAUDE.md
git commit -m "docs: add the first-real-run runbook, a token leak check and the record of the real run" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```
