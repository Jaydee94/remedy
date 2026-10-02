# Phase 0: Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A run can be started from the web UI, executed by the runner through the `claude` CLI on a subscription login, and its output streams live back into the UI.

**Architecture:** The control plane (`remedy-server`) owns SQLite, the admin session, the admin API and a runner API. The runner (`remedy-runner`) long-polls the control plane for queued runs, starts the CLI as a subprocess with a filtered environment, and posts the CLI's stream-json lines back as events. The UI reads events over Server-Sent Events. No gatekeeper, no git, no incidents yet (phases 1 to 3).

**Tech Stack:** Go 1.27 (stdlib `net/http`, `log/slog`, `os/exec`), `modernc.org/sqlite`, `golang.org/x/crypto/argon2`, React 19 + Vite + TypeScript + Tailwind (already scaffolded in `web/`).

**Spec:** [`docs/design.md`](../design.md), sections 2.1, 2.6, 2.8 and roadmap phase 0. Research constraints: [`docs/research/subscription-cli-usage.md`](../research/subscription-cli-usage.md).

## Global Constraints

- Everything committed to the repo is English: docs, code, identifiers, comments, commit messages, UI copy.
- Go version is whatever `go.mod` says (`go 1.27.1`). New Go dependencies are limited to `modernc.org/sqlite` and `golang.org/x/crypto`. No new web dependencies in this phase (no router, no component library).
- The runner must never read, copy, log or store CLI credentials. It starts the unmodified `claude` binary only.
- The runner passes the subprocess an allowlisted environment. `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN` are never passed, so a stray API key cannot silently switch billing away from the subscription.
- Do not use `claude --bare` (it ignores the subscription login).
- Agents get no write permissions in this phase: `--permission-mode dontAsk` with no `--allowedTools`.
- Admin API state-changing requests (anything but `GET`/`HEAD`) require the header `X-Remedy-CSRF: 1`. The session cookie is `HttpOnly`, `SameSite=Strict`.
- Runner API requests require `Authorization: Bearer <REMEDY_RUNNER_TOKEN>`, compared in constant time.
- Every commit message ends with the trailer `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`.
- `make check` must pass at the end of every task.

## File Structure

| Path | Responsibility |
|---|---|
| `scripts/spike/claude-billing.sh` | Spike helper: one real `claude -p` call, prints cost and session ID |
| `docs/research/spike-claude-billing.md` | Spike findings and the go/no-go decision |
| `internal/run/run.go` | Shared domain types (`Run`, `Event`, `Outcome`, `Status`), `NewID`, `JSONString` |
| `internal/store/store.go`, `migrations/001_init.sql` | SQLite access: runs, events, atomic claim |
| `internal/provider/provider.go` | `Provider` interface, `Spec`, `Line`, `Final`, `FilterEnv` |
| `internal/provider/claude.go` | Claude CLI adapter: command line and stream-json parsing |
| `internal/testutil/fakeclaude.go` | Fake `claude` shell script for tests |
| `internal/runner/execute.go` | Run one provider subprocess, forward lines to a `Sink` |
| `internal/runner/client.go`, `loop.go` | HTTP client for the runner API and the claim/execute/finish loop |
| `internal/auth/auth.go` | Admin password verification, sessions, login rate limit |
| `internal/config/config.go` | Environment-based config for server and runner |
| `internal/server/*.go` | HTTP routing, session middleware, admin API, runner API, SSE hub, SPA handler |
| `web/embed.go`, `web/embed_stub.go` | Embed `web/dist` behind the `webui` build tag |
| `web/src/*` | Login, runs list, new run form, live run view |
| `Dockerfile` | Control plane image |

---

### Task 1: Spike: subscription billing and login behaviour of `claude -p`

Everything in this plan assumes `claude -p` on a subscription login is billed against the subscription. Research found a closed issue (#43333) that reported the opposite and could not confirm the fix. Settle this with evidence before building on it. Tasks 2 to 5 do not depend on the outcome and may proceed in parallel; **Tasks 6 and later must not start while this task has a "stop" result.**

**Files:**
- Create: `scripts/spike/claude-billing.sh`
- Create: `docs/research/spike-claude-billing.md`
- Modify: `docs/design.md` only if the result is "stop" (section 2.1 and risks)

**Interfaces:**
- Consumes: a working `claude` binary logged in with the Claude Pro/Max subscription (`claude` then `/login`), `jq`.
- Produces: a written decision in `docs/research/spike-claude-billing.md`: `proceed` or `stop`.

- [ ] **Step 1: Write the helper script**

```sh
#!/bin/sh
# Spike helper: one real `claude -p` call on the subscription login.
# Strips API-key variables so the subscription is the only possible credential.
set -eu

out=$(env -u ANTHROPIC_API_KEY -u ANTHROPIC_AUTH_TOKEN \
  claude -p "Reply with the single word: pong" \
    --output-format json \
    --permission-mode dontAsk)

echo "$out" | jq '{result, session_id, total_cost_usd, usage}'
```

Run: `chmod +x scripts/spike/claude-billing.sh`

- [ ] **Step 2: Capture the baseline**

In a browser, open the subscription usage page (claude.ai, Settings, Usage) and the Console usage/billing page (platform.claude.com). Write down the current numbers for both.

- [ ] **Step 3: Run the helper three times, then compare**

Run: `./scripts/spike/claude-billing.sh` (three times)
Expected: JSON with `result` containing `pong`. Then reload both pages. The subscription page shows more usage; the Console page shows **no** new usage or charge.

- [ ] **Step 4: Repeat with a long-lived token**

Run `claude setup-token`, copy the token, then:

Run: `CLAUDE_CODE_OAUTH_TOKEN=<token> ./scripts/spike/claude-billing.sh`
Expected: same result as step 3 (subscription usage grows, Console unchanged).

- [ ] **Step 5: Record the findings**

Create `docs/research/spike-claude-billing.md` and fill in every answer from what you observed:

```markdown
# Spike: billing and login behaviour of `claude -p`

Date: <YYYY-MM-DD>. Claude Code version (`claude --version`): <version>.

## Questions and observations

1. Does `claude -p` with the interactive `/login` credential count against the subscription? <yes/no, with the usage numbers before and after>
2. Does it appear in Console billing? <yes/no, with numbers>
3. Same two questions for `CLAUDE_CODE_OAUTH_TOKEN` from `claude setup-token`: <answers>
4. What does `total_cost_usd` show on a subscription run? <value, and whether it is only an estimate>
5. Login lifetime: <date of login, expiry shown in `/status`, result of re-checking after 7 days>

## Decision

<proceed | stop>. <One paragraph of reasoning.>

## Deferred

Behaviour of an MCP tool that blocks for minutes (needed for approvals) is not tested
here. It is tested in phase 2 together with the gatekeeper.
```

- [ ] **Step 6: Apply the decision**

If `proceed`: continue with the plan. If `stop`: edit `docs/design.md` section 2.1 and the risks table to say that Remedy requires an API key, mark Tasks 6+ as blocked in this plan, and ask the maintainer how to continue.

- [ ] **Step 7: Commit**

```bash
git add scripts/spike/claude-billing.sh docs/research/spike-claude-billing.md docs/design.md
git commit -m "docs: record claude -p subscription billing spike" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Shared run types and the SQLite store

**Files:**
- Create: `internal/run/run.go`
- Create: `internal/store/migrations/001_init.sql`
- Create: `internal/store/store.go`
- Test: `internal/store/store_test.go`
- Modify: `go.mod`, `go.sum` (dependency)

**Interfaces:**
- Consumes: nothing.
- Produces (package `run`):
  - `type Status string` with constants `Queued`, `Running`, `Succeeded`, `Failed`
  - `type Run struct{ ID, Provider, Prompt string; Status Status; ExitCode *int; Result, SessionID string; CostUSD float64; CreatedAt time.Time; StartedAt, FinishedAt *time.Time }` (JSON keys camelCase: `id`, `provider`, `prompt`, `status`, `exitCode`, `result`, `sessionId`, `costUsd`, `createdAt`, `startedAt`, `finishedAt`)
  - `type Event struct{ Seq int; Kind string; Payload json.RawMessage; CreatedAt time.Time }` (JSON: `seq`, `kind`, `payload`, `createdAt`)
  - `type Outcome struct{ ExitCode int; Result, SessionID string; CostUSD float64 }` (JSON: `exitCode`, `result`, `sessionId`, `costUsd`)
  - `func NewID() string` (32 hex chars), `func JSONString(s string) json.RawMessage`
- Produces (package `store`):
  - `var ErrNotFound error`
  - `func Open(path string) (*Store, error)`, `func (*Store) Close() error`
  - `func (*Store) CreateRun(ctx, provider, prompt string) (run.Run, error)`
  - `func (*Store) GetRun(ctx, id string) (run.Run, error)` (returns `ErrNotFound`)
  - `func (*Store) ListRuns(ctx, limit int) ([]run.Run, error)` (newest first)
  - `func (*Store) ClaimNext(ctx) (*run.Run, error)` (nil when nothing queued)
  - `func (*Store) AppendEvent(ctx, runID, kind string, payload json.RawMessage) (run.Event, error)`
  - `func (*Store) Events(ctx, runID string, afterSeq int) ([]run.Event, error)`
  - `func (*Store) FinishRun(ctx, id string, o run.Outcome) error` (`ErrNotFound` if the run is not `running`)

- [ ] **Step 1: Add the dependency**

Run: `go get modernc.org/sqlite@latest`
Expected: `go.mod` gains a `modernc.org/sqlite` requirement.

- [ ] **Step 2: Write the shared types**

Create `internal/run/run.go`:

```go
// Package run holds the domain types shared by the control plane and the runner.
package run

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"
)

type Status string

const (
	Queued    Status = "queued"
	Running   Status = "running"
	Succeeded Status = "succeeded"
	Failed    Status = "failed"
)

// Terminal reports whether no further state change is expected.
func (s Status) Terminal() bool { return s == Succeeded || s == Failed }

type Run struct {
	ID         string     `json:"id"`
	Provider   string     `json:"provider"`
	Prompt     string     `json:"prompt"`
	Status     Status     `json:"status"`
	ExitCode   *int       `json:"exitCode,omitempty"`
	Result     string     `json:"result"`
	SessionID  string     `json:"sessionId"`
	CostUSD    float64    `json:"costUsd"`
	CreatedAt  time.Time  `json:"createdAt"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

type Event struct {
	Seq       int             `json:"seq"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"createdAt"`
}

// Outcome is what the runner reports when a run has ended.
type Outcome struct {
	ExitCode  int     `json:"exitCode"`
	Result    string  `json:"result"`
	SessionID string  `json:"sessionId"`
	CostUSD   float64 `json:"costUsd"`
}

// NewID returns a random 128-bit identifier as 32 hex characters.
func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failing is unrecoverable
	}
	return hex.EncodeToString(b)
}

// JSONString encodes s as a JSON string value.
func JSONString(s string) json.RawMessage {
	b, _ := json.Marshal(s) // marshalling a string cannot fail
	return b
}
```

- [ ] **Step 3: Write the migration**

Create `internal/store/migrations/001_init.sql`:

```sql
CREATE TABLE runs (
  id          TEXT PRIMARY KEY,
  provider    TEXT NOT NULL,
  prompt      TEXT NOT NULL,
  status      TEXT NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed')),
  exit_code   INTEGER,
  result      TEXT NOT NULL DEFAULT '',
  session_id  TEXT NOT NULL DEFAULT '',
  cost_usd    REAL NOT NULL DEFAULT 0,
  created_at  TEXT NOT NULL,
  started_at  TEXT,
  finished_at TEXT
);

CREATE INDEX runs_status_created ON runs (status, created_at);

CREATE TABLE run_events (
  run_id     TEXT NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
  seq        INTEGER NOT NULL,
  kind       TEXT NOT NULL,
  payload    TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (run_id, seq)
);
```

- [ ] **Step 4: Write the failing tests**

Create `internal/store/store_test.go`:

```go
package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestCreateAndGetRun(t *testing.T) {
	s, ctx := openStore(t), context.Background()

	created, err := s.CreateRun(ctx, "claude", "say pong")
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if created.Status != run.Queued || created.ID == "" {
		t.Fatalf("unexpected run: %+v", created)
	}

	got, err := s.GetRun(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got.Prompt != "say pong" || got.Provider != "claude" {
		t.Fatalf("unexpected run: %+v", got)
	}

	if _, err := s.GetRun(ctx, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetRun(missing) error = %v, want ErrNotFound", err)
	}
}

func TestListRunsNewestFirst(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	first, _ := s.CreateRun(ctx, "claude", "one")
	second, _ := s.CreateRun(ctx, "claude", "two")

	runs, err := s.ListRuns(ctx, 10)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 2 || runs[0].ID != second.ID || runs[1].ID != first.ID {
		t.Fatalf("unexpected order: %+v", runs)
	}
}

func TestClaimNextIsExclusiveAndOldestFirst(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	first, _ := s.CreateRun(ctx, "claude", "one")
	_, _ = s.CreateRun(ctx, "claude", "two")

	claimed, err := s.ClaimNext(ctx)
	if err != nil || claimed == nil {
		t.Fatalf("ClaimNext = %v, %v", claimed, err)
	}
	if claimed.ID != first.ID || claimed.Status != run.Running || claimed.StartedAt == nil {
		t.Fatalf("unexpected claim: %+v", claimed)
	}

	second, err := s.ClaimNext(ctx)
	if err != nil || second == nil || second.ID == first.ID {
		t.Fatalf("second claim = %v, %v", second, err)
	}

	none, err := s.ClaimNext(ctx)
	if err != nil || none != nil {
		t.Fatalf("third claim = %v, %v, want nil", none, err)
	}
}

func TestEventsAreSequencedPerRun(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	r, _ := s.CreateRun(ctx, "claude", "x")

	for i, kind := range []string{"system", "assistant", "result"} {
		ev, err := s.AppendEvent(ctx, r.ID, kind, json.RawMessage(`{"n":1}`))
		if err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
		if ev.Seq != i+1 {
			t.Fatalf("seq = %d, want %d", ev.Seq, i+1)
		}
	}

	after, err := s.Events(ctx, r.ID, 1)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(after) != 2 || after[0].Seq != 2 || after[0].Kind != "assistant" {
		t.Fatalf("unexpected events: %+v", after)
	}

	if _, err := s.AppendEvent(ctx, r.ID, "bad", json.RawMessage(`not json`)); err == nil {
		t.Fatal("AppendEvent accepted invalid JSON")
	}
}

func TestFinishRun(t *testing.T) {
	s, ctx := openStore(t), context.Background()
	r, _ := s.CreateRun(ctx, "claude", "x")

	if err := s.FinishRun(ctx, r.ID, run.Outcome{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("FinishRun on queued run error = %v, want ErrNotFound", err)
	}

	if _, err := s.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	}
	out := run.Outcome{ExitCode: 0, Result: "pong", SessionID: "s-1", CostUSD: 0.01}
	if err := s.FinishRun(ctx, r.ID, out); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}

	got, _ := s.GetRun(ctx, r.ID)
	if got.Status != run.Succeeded || got.Result != "pong" || got.SessionID != "s-1" ||
		got.ExitCode == nil || *got.ExitCode != 0 || got.FinishedAt == nil {
		t.Fatalf("unexpected run: %+v", got)
	}

	r2, _ := s.CreateRun(ctx, "claude", "y")
	_, _ = s.ClaimNext(ctx)
	_ = s.FinishRun(ctx, r2.ID, run.Outcome{ExitCode: 3})
	got2, _ := s.GetRun(ctx, r2.ID)
	if got2.Status != run.Failed {
		t.Fatalf("status = %s, want failed", got2.Status)
	}
}
```

- [ ] **Step 5: Run the tests to verify they fail**

Run: `go test ./internal/store/ -v`
Expected: FAIL (build error: package `store` has no `Open`).

- [ ] **Step 6: Write the store**

Create `internal/store/store.go`:

```go
// Package store persists runs and run events in SQLite.
package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"github.com/Jaydee94/remedy/internal/run"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// ErrNotFound is returned when a run does not exist or is not in the expected state.
var ErrNotFound = errors.New("not found")

// tsLayout is fixed-width so that string order equals time order.
const tsLayout = "2006-01-02T15:04:05.000000000Z"

const runCols = `id, provider, prompt, status, exit_code, result, session_id, cost_usd, created_at, started_at, finished_at`

type Store struct{ db *sql.DB }

// Open opens (and migrates) the SQLite database at path.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection keeps SQLite's single-writer model simple and race-free.
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY)`); err != nil {
		return err
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	for _, e := range entries { // ReadDir returns entries sorted by filename
		var applied int
		err := s.db.QueryRow(`SELECT 1 FROM schema_migrations WHERE version = ?`, e.Name()).Scan(&applied)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		body, err := migrationsFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return err
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("%s: %w", e.Name(), err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, e.Name()); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func formatTS(t time.Time) string { return t.UTC().Format(tsLayout) }

func parseTS(s string) (time.Time, error) { return time.Parse(tsLayout, s) }

type scanner interface{ Scan(dest ...any) error }

func scanRun(sc scanner) (run.Run, error) {
	var (
		r                 run.Run
		status, created   string
		exit              sql.NullInt64
		started, finished sql.NullString
	)
	if err := sc.Scan(&r.ID, &r.Provider, &r.Prompt, &status, &exit, &r.Result, &r.SessionID,
		&r.CostUSD, &created, &started, &finished); err != nil {
		return run.Run{}, err
	}
	r.Status = run.Status(status)
	if exit.Valid {
		code := int(exit.Int64)
		r.ExitCode = &code
	}
	var err error
	if r.CreatedAt, err = parseTS(created); err != nil {
		return run.Run{}, err
	}
	if started.Valid {
		t, err := parseTS(started.String)
		if err != nil {
			return run.Run{}, err
		}
		r.StartedAt = &t
	}
	if finished.Valid {
		t, err := parseTS(finished.String)
		if err != nil {
			return run.Run{}, err
		}
		r.FinishedAt = &t
	}
	return r, nil
}

func (s *Store) CreateRun(ctx context.Context, provider, prompt string) (run.Run, error) {
	id := run.NewID()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO runs (id, provider, prompt, status, created_at) VALUES (?, ?, ?, 'queued', ?)`,
		id, provider, prompt, formatTS(time.Now()))
	if err != nil {
		return run.Run{}, err
	}
	return s.GetRun(ctx, id)
}

func (s *Store) GetRun(ctx context.Context, id string) (run.Run, error) {
	r, err := scanRun(s.db.QueryRowContext(ctx, `SELECT `+runCols+` FROM runs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return run.Run{}, ErrNotFound
	}
	return r, err
}

func (s *Store) ListRuns(ctx context.Context, limit int) ([]run.Run, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+runCols+` FROM runs ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	runs := []run.Run{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

// ClaimNext atomically moves the oldest queued run to running. It returns nil when none is queued.
func (s *Store) ClaimNext(ctx context.Context) (*run.Run, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE runs SET status = 'running', started_at = ?
		WHERE id = (SELECT id FROM runs WHERE status = 'queued' ORDER BY created_at, id LIMIT 1)
		RETURNING `+runCols, formatTS(time.Now()))
	r, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// AppendEvent stores the next event of a run. payload must be valid JSON.
func (s *Store) AppendEvent(ctx context.Context, runID, kind string, payload json.RawMessage) (run.Event, error) {
	if !json.Valid(payload) {
		return run.Event{}, errors.New("payload is not valid JSON")
	}
	now := time.Now()
	var seq int
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO run_events (run_id, seq, kind, payload, created_at)
		SELECT ?, COALESCE(MAX(seq), 0) + 1, ?, ?, ? FROM run_events WHERE run_id = ?
		RETURNING seq`, runID, kind, string(payload), formatTS(now), runID).Scan(&seq)
	if err != nil {
		return run.Event{}, err
	}
	return run.Event{Seq: seq, Kind: kind, Payload: payload, CreatedAt: now.UTC()}, nil
}

// Events returns the events of a run with seq greater than afterSeq, in order.
func (s *Store) Events(ctx context.Context, runID string, afterSeq int) ([]run.Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT seq, kind, payload, created_at FROM run_events WHERE run_id = ? AND seq > ? ORDER BY seq`,
		runID, afterSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []run.Event{}
	for rows.Next() {
		var (
			e              run.Event
			payload, ctime string
		)
		if err := rows.Scan(&e.Seq, &e.Kind, &payload, &ctime); err != nil {
			return nil, err
		}
		e.Payload = json.RawMessage(payload)
		if e.CreatedAt, err = parseTS(ctime); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// FinishRun records the outcome of a running run. It returns ErrNotFound if the run is not running.
func (s *Store) FinishRun(ctx context.Context, id string, o run.Outcome) error {
	status := run.Succeeded
	if o.ExitCode != 0 {
		status = run.Failed
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE runs SET status = ?, exit_code = ?, result = ?, session_id = ?, cost_usd = ?, finished_at = ?
		WHERE id = ? AND status = 'running'`,
		string(status), o.ExitCode, o.Result, o.SessionID, o.CostUSD, formatTS(time.Now()), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("run %s is not running: %w", id, ErrNotFound)
	}
	return nil
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go mod tidy && go test ./internal/store/ -v`
Expected: PASS for all five tests.

- [ ] **Step 8: Commit**

```bash
make check
git add go.mod go.sum internal/run internal/store
git commit -m "feat(store): add SQLite store for runs and events" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Provider interface and the Claude adapter

**Files:**
- Create: `internal/provider/provider.go`
- Create: `internal/provider/claude.go`
- Test: `internal/provider/claude_test.go`

**Interfaces:**
- Consumes: `run.JSONString` from Task 2.
- Produces (package `provider`):
  - `type Spec struct{ Prompt, Workdir string }`
  - `type Final struct{ Result, SessionID string; CostUSD float64 }`
  - `type Line struct{ Kind string; Payload json.RawMessage; Final *Final }`
  - `type Provider interface { Name() string; Command(ctx context.Context, spec Spec, parentEnv []string) *exec.Cmd; ParseLine(line []byte) Line }`. `ParseLine` must not retain `line` (callers reuse the buffer).
  - `func FilterEnv(env []string) []string`
  - `type Claude struct{ Binary string }` implementing `Provider`

- [ ] **Step 1: Write the failing tests**

Create `internal/provider/claude_test.go`:

```go
package provider_test

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/provider"
)

func TestFilterEnvDropsAPIKeysAndKeepsAllowlist(t *testing.T) {
	in := []string{
		"PATH=/usr/bin",
		"HOME=/home/r",
		"CLAUDE_CONFIG_DIR=/data/claude",
		"CLAUDE_CODE_OAUTH_TOKEN=tok",
		"ANTHROPIC_API_KEY=sk-secret",
		"ANTHROPIC_AUTH_TOKEN=bearer-secret",
		"GITHUB_TOKEN=ghp_secret",
		"REMEDY_RUNNER_TOKEN=runner-secret",
		"malformed-entry",
	}
	got := provider.FilterEnv(in)

	want := []string{"PATH=/usr/bin", "HOME=/home/r", "CLAUDE_CONFIG_DIR=/data/claude", "CLAUDE_CODE_OAUTH_TOKEN=tok"}
	if !slices.Equal(got, want) {
		t.Fatalf("FilterEnv = %v, want %v", got, want)
	}
}

func TestClaudeCommand(t *testing.T) {
	c := provider.Claude{Binary: "/opt/claude"}
	cmd := c.Command(context.Background(), provider.Spec{Prompt: "fix it", Workdir: "/work"},
		[]string{"PATH=/bin", "ANTHROPIC_API_KEY=x"})

	wantArgs := []string{"/opt/claude", "-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "dontAsk"}
	if !slices.Equal(cmd.Args, wantArgs) {
		t.Fatalf("Args = %v, want %v", cmd.Args, wantArgs)
	}
	if cmd.Dir != "/work" {
		t.Fatalf("Dir = %q", cmd.Dir)
	}
	if !slices.Equal(cmd.Env, []string{"PATH=/bin"}) {
		t.Fatalf("Env = %v", cmd.Env)
	}
	stdin, _ := io.ReadAll(cmd.Stdin)
	if string(stdin) != "fix it" {
		t.Fatalf("stdin = %q, want the prompt (never argv)", stdin)
	}
	if slices.Contains(cmd.Args, "--bare") {
		t.Fatal("--bare ignores the subscription login and must not be used")
	}
}

func TestClaudeCommandDefaultBinary(t *testing.T) {
	cmd := provider.Claude{}.Command(context.Background(), provider.Spec{}, nil)
	if !strings.HasSuffix(cmd.Args[0], "claude") {
		t.Fatalf("Args[0] = %q", cmd.Args[0])
	}
}

func TestClaudeParseLine(t *testing.T) {
	c := provider.Claude{}

	init := c.ParseLine([]byte(`{"type":"system","subtype":"init","session_id":"s-1"}`))
	if init.Kind != "system" || init.Final != nil {
		t.Fatalf("init = %+v", init)
	}

	res := c.ParseLine([]byte(`{"type":"result","subtype":"success","result":"pong","session_id":"s-1","total_cost_usd":0.0123}`))
	if res.Kind != "result" || res.Final == nil {
		t.Fatalf("result = %+v", res)
	}
	if res.Final.Result != "pong" || res.Final.SessionID != "s-1" || res.Final.CostUSD != 0.0123 {
		t.Fatalf("final = %+v", res.Final)
	}

	raw := c.ParseLine([]byte("not json at all"))
	if raw.Kind != "raw" || string(raw.Payload) != `"not json at all"` {
		t.Fatalf("raw = %+v", raw)
	}

	untyped := c.ParseLine([]byte(`{"foo":1}`))
	if untyped.Kind != "raw" {
		t.Fatalf("untyped JSON kind = %q, want raw", untyped.Kind)
	}
}

func TestClaudeParseLineDoesNotRetainBuffer(t *testing.T) {
	buf := []byte(`{"type":"assistant","n":1}`)
	l := provider.Claude{}.ParseLine(buf)
	copy(buf, `{"type":"XXXXXXXXX","n":2}`)
	if string(l.Payload) != `{"type":"assistant","n":1}` {
		t.Fatalf("payload aliases the caller's buffer: %s", l.Payload)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/provider/ -v`
Expected: FAIL (build error: undefined `provider.FilterEnv`, `provider.Claude`).

- [ ] **Step 3: Write the interface and `FilterEnv`**

Create `internal/provider/provider.go`:

```go
// Package provider adapts the official agent CLIs. An adapter only builds the command
// line and understands the CLI's output. It never touches credentials.
package provider

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
)

// Spec describes one agent invocation.
type Spec struct {
	Prompt  string
	Workdir string
}

// Final carries the end-of-run summary some CLIs emit as their last line.
type Final struct {
	Result    string
	SessionID string
	CostUSD   float64
}

// Line is one parsed line of CLI output.
type Line struct {
	Kind    string          // event type, or "raw" for anything unparseable
	Payload json.RawMessage // always valid JSON
	Final   *Final          // set only on the end-of-run summary line
}

type Provider interface {
	Name() string
	// Command builds the subprocess. The prompt goes to stdin, never to argv.
	// parentEnv is the runner's own environment and is filtered by the adapter.
	Command(ctx context.Context, spec Spec, parentEnv []string) *exec.Cmd
	// ParseLine must not retain line; callers reuse the buffer.
	ParseLine(line []byte) Line
}

// envAllowlist names the only variables a CLI subprocess may inherit.
// ANTHROPIC_API_KEY and ANTHROPIC_AUTH_TOKEN are deliberately absent: with them set the CLI
// would bill the API instead of the subscription login.
var envAllowlist = map[string]bool{
	"PATH": true, "HOME": true, "LANG": true, "LC_ALL": true, "TMPDIR": true,
	"XDG_CONFIG_HOME": true, "HTTPS_PROXY": true, "HTTP_PROXY": true, "NO_PROXY": true,
	"CLAUDE_CONFIG_DIR": true, "CLAUDE_CODE_OAUTH_TOKEN": true,
}

// FilterEnv keeps only allowlisted KEY=value entries, preserving order.
func FilterEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key, _, ok := strings.Cut(kv, "=")
		if ok && envAllowlist[key] {
			out = append(out, kv)
		}
	}
	return out
}
```

- [ ] **Step 4: Write the Claude adapter**

Create `internal/provider/claude.go`:

```go
package provider

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
)

// Claude runs the unmodified `claude` binary in headless mode on the subscription login.
type Claude struct {
	Binary string // defaults to "claude" on PATH
}

func (Claude) Name() string { return "claude" }

func (c Claude) Command(ctx context.Context, spec Spec, parentEnv []string) *exec.Cmd {
	bin := c.Binary
	if bin == "" {
		bin = "claude"
	}
	// dontAsk denies everything that would prompt, so without --allowedTools the agent is read-only.
	// --bare is intentionally not used: it ignores the subscription login.
	cmd := exec.CommandContext(ctx, bin,
		"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "dontAsk")
	cmd.Dir = spec.Workdir
	cmd.Stdin = strings.NewReader(spec.Prompt)
	cmd.Env = FilterEnv(parentEnv)
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

func (Claude) ParseLine(line []byte) Line {
	var head struct {
		Type      string  `json:"type"`
		Result    string  `json:"result"`
		SessionID string  `json:"session_id"`
		CostUSD   float64 `json:"total_cost_usd"`
	}
	if err := json.Unmarshal(line, &head); err != nil || head.Type == "" {
		return Line{Kind: "raw", Payload: run.JSONString(string(line))}
	}
	l := Line{Kind: head.Type, Payload: append(json.RawMessage(nil), line...)}
	if head.Type == "result" {
		l.Final = &Final{Result: head.Result, SessionID: head.SessionID, CostUSD: head.CostUSD}
	}
	return l
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/provider/ -v`
Expected: PASS for all tests.

- [ ] **Step 6: Commit**

```bash
make check
git add internal/provider
git commit -m "feat(provider): add Claude CLI adapter with filtered environment" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Execute a provider subprocess and forward its lines

**Files:**
- Create: `internal/testutil/fakeclaude.go`
- Create: `internal/runner/execute.go`
- Test: `internal/runner/execute_test.go`

**Interfaces:**
- Consumes: `provider.Provider`, `provider.Spec`, `provider.Line`, `provider.Claude` (Task 3); `run.Outcome`, `run.JSONString` (Task 2).
- Produces:
  - `testutil.FakeClaude(t testing.TB, exitCode int) string` returns the path of a shell script that behaves like `claude -p` (reads the prompt from stdin, prints stream-json lines, exits with `exitCode`).
  - `type Sink interface{ Event(ctx context.Context, kind string, payload json.RawMessage) error }`
  - `func Execute(ctx context.Context, p provider.Provider, spec provider.Spec, env []string, sink Sink) (run.Outcome, error)`. It always returns a usable `Outcome`. A non-nil error means the subprocess could not start (`Outcome.ExitCode` is 127) or that the first sink error occurred (events may be missing); the run still ended with the returned `Outcome`.

- [ ] **Step 1: Write the fake CLI helper**

Create `internal/testutil/fakeclaude.go`:

```go
// Package testutil holds helpers shared by tests in several packages.
package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// FakeClaude writes an executable script that mimics `claude -p --output-format stream-json`
// and returns its path. It echoes the prompt it read from stdin and whether ANTHROPIC_API_KEY
// reached the process, writes one line to stderr, and exits with exitCode.
func FakeClaude(t testing.TB, exitCode int) string {
	t.Helper()
	script := fmt.Sprintf(`#!/bin/sh
prompt=$(cat)
echo '{"type":"system","subtype":"init","session_id":"s-1"}'
echo "{\"type\":\"probe\",\"prompt\":\"$prompt\",\"api_key\":\"${ANTHROPIC_API_KEY:-unset}\"}"
echo 'not json'
echo 'warn: something' >&2
echo '{"type":"result","subtype":"success","result":"done","session_id":"s-1","total_cost_usd":0.0123}'
exit %d
`, exitCode)
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	return path
}
```

- [ ] **Step 2: Write the failing tests**

Create `internal/runner/execute_test.go`:

```go
package runner_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/runner"
	"github.com/Jaydee94/remedy/internal/testutil"
)

type recordedEvent struct {
	Kind    string
	Payload string
}

type recordingSink struct {
	mu     sync.Mutex
	events []recordedEvent
}

func (s *recordingSink) Event(_ context.Context, kind string, payload json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, recordedEvent{kind, string(payload)})
	return nil
}

func (s *recordingSink) stdoutKinds() []string {
	var kinds []string
	for _, e := range s.events {
		if e.Kind != "stderr" {
			kinds = append(kinds, e.Kind)
		}
	}
	return kinds
}

func TestExecuteForwardsLinesAndReportsOutcome(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-must-not-leak")
	sink := &recordingSink{}
	p := provider.Claude{Binary: testutil.FakeClaude(t, 3)}

	out, err := runner.Execute(context.Background(), p,
		provider.Spec{Prompt: "hello", Workdir: t.TempDir()}, os.Environ(), sink)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if out.ExitCode != 3 || out.Result != "done" || out.SessionID != "s-1" || out.CostUSD != 0.0123 {
		t.Fatalf("outcome = %+v", out)
	}

	want := []string{"system", "probe", "raw", "result"}
	got := sink.stdoutKinds()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("stdout kinds = %v, want %v", got, want)
	}

	var probe, stderr string
	var stderrCount int
	for _, e := range sink.events {
		switch e.Kind {
		case "probe":
			probe = e.Payload
		case "stderr":
			stderr = e.Payload
			stderrCount++
		}
	}
	if !strings.Contains(probe, `"prompt":"hello"`) {
		t.Errorf("prompt did not arrive on stdin: %s", probe)
	}
	if !strings.Contains(probe, `"api_key":"unset"`) {
		t.Errorf("ANTHROPIC_API_KEY leaked into the subprocess: %s", probe)
	}
	if stderrCount != 1 || stderr != `"warn: something"` {
		t.Errorf("stderr events = %d (%s)", stderrCount, stderr)
	}
}

func TestExecuteSuccessExitCode(t *testing.T) {
	out, err := runner.Execute(context.Background(), provider.Claude{Binary: testutil.FakeClaude(t, 0)},
		provider.Spec{Prompt: "x", Workdir: t.TempDir()}, os.Environ(), &recordingSink{})
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
}

func TestExecuteStartFailure(t *testing.T) {
	out, err := runner.Execute(context.Background(), provider.Claude{Binary: "/nonexistent/claude"},
		provider.Spec{Prompt: "x", Workdir: t.TempDir()}, os.Environ(), &recordingSink{})
	if err == nil {
		t.Fatal("expected a start error")
	}
	if out.ExitCode != 127 || !strings.Contains(out.Result, "failed to start") {
		t.Fatalf("outcome = %+v", out)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/runner/ -v`
Expected: FAIL (build error: undefined `runner.Execute`).

- [ ] **Step 4: Write `Execute`**

Create `internal/runner/execute.go`:

```go
// Package runner executes agent CLI subprocesses and reports them to the control plane.
package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"

	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/run"
)

// maxLine bounds one line of CLI output (stream-json lines can carry whole files).
const maxLine = 4 << 20

// Sink receives the events of one run.
type Sink interface {
	Event(ctx context.Context, kind string, payload json.RawMessage) error
}

// Execute runs the provider subprocess to completion, forwarding every stdout line (parsed)
// and every stderr line to sink. It always returns a usable Outcome. A non-nil error means
// the subprocess could not be started (ExitCode 127) or the first sink error (events may be
// missing); in both cases the run still ended with the returned Outcome.
func Execute(ctx context.Context, p provider.Provider, spec provider.Spec, env []string, sink Sink) (run.Outcome, error) {
	cmd := p.Command(ctx, spec, env)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return failedToStart(err), err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return failedToStart(err), err
	}
	if err := cmd.Start(); err != nil {
		return failedToStart(err), fmt.Errorf("start %s: %w", p.Name(), err)
	}

	var (
		mu      sync.Mutex
		sinkErr error
	)
	emit := func(kind string, payload json.RawMessage) {
		mu.Lock()
		defer mu.Unlock()
		if err := sink.Event(ctx, kind, payload); err != nil && sinkErr == nil {
			sinkErr = err
		}
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 0, 64*1024), maxLine)
		for sc.Scan() {
			emit("stderr", run.JSONString(sc.Text()))
		}
		_, _ = io.Copy(io.Discard, stderr) // keep draining if a line was too long
	}()

	var final *provider.Final
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), maxLine)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		l := p.ParseLine(line)
		if l.Final != nil {
			final = l.Final
		}
		emit(l.Kind, l.Payload)
	}
	if err := sc.Err(); err != nil {
		emit("raw", run.JSONString(fmt.Sprintf("stdout read error: %v", err)))
		_, _ = io.Copy(io.Discard, stdout)
	}
	wg.Wait()

	exit := 0
	if err := cmd.Wait(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exit = ee.ExitCode() // -1 if the process was killed by a signal
		} else {
			exit = -1
		}
	}

	out := run.Outcome{ExitCode: exit}
	if final != nil {
		out.Result, out.SessionID, out.CostUSD = final.Result, final.SessionID, final.CostUSD
	}
	return out, sinkErr
}

func failedToStart(err error) run.Outcome {
	return run.Outcome{ExitCode: 127, Result: fmt.Sprintf("failed to start: %v", err)}
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/runner/ -v -race`
Expected: PASS for all three tests, no race reports.

- [ ] **Step 6: Commit**

```bash
make check
git add internal/testutil internal/runner
git commit -m "feat(runner): execute provider subprocess and forward output lines" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Admin authentication and configuration

**Files:**
- Create: `internal/auth/auth.go`
- Test: `internal/auth/auth_test.go`
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`
- Modify: `go.mod`, `go.sum` (dependency)

**Interfaces:**
- Consumes: nothing.
- Produces (package `auth`):
  - `const SessionTTL = 24 * time.Hour`
  - `func New(password string) *Auth`
  - `func (*Auth) Verify(password string) bool`
  - `func (*Auth) NewSession() string`, `ValidSession(token string) bool`, `EndSession(token string)`
  - `func (*Auth) Locked(key string) bool`, `RecordFailure(key string)`, `ResetFailures(key string)` (5 failures within 10 minutes lock the key until the oldest failure ages out)
  - `func (*Auth) SetClock(now func() time.Time)` (tests)
- Produces (package `config`):
  - `type Server struct{ Addr, DBPath, AdminPassword, RunnerToken string }`, `func ServerFromEnv(get func(string) string) (Server, error)`
  - `type Runner struct{ ServerURL, Token, WorkspaceRoot, ClaudeBin string }`, `func RunnerFromEnv(get func(string) string) (Runner, error)`

- [ ] **Step 1: Add the dependency**

Run: `go get golang.org/x/crypto@latest`
Expected: `go.mod` gains `golang.org/x/crypto`.

- [ ] **Step 2: Write the failing auth tests**

Create `internal/auth/auth_test.go`:

```go
package auth_test

import (
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/auth"
)

func TestVerify(t *testing.T) {
	a := auth.New("correct horse battery")
	if !a.Verify("correct horse battery") {
		t.Fatal("correct password rejected")
	}
	if a.Verify("wrong") || a.Verify("") {
		t.Fatal("wrong password accepted")
	}
}

func TestSessions(t *testing.T) {
	a := auth.New("correct horse battery")
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a.SetClock(func() time.Time { return now })

	tok := a.NewSession()
	if len(tok) < 40 {
		t.Fatalf("token too short: %q", tok)
	}
	if !a.ValidSession(tok) {
		t.Fatal("fresh session invalid")
	}
	if a.ValidSession("nope") || a.ValidSession("") {
		t.Fatal("unknown token accepted")
	}

	now = now.Add(auth.SessionTTL + time.Second)
	if a.ValidSession(tok) {
		t.Fatal("expired session accepted")
	}

	now = time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	tok2 := a.NewSession()
	a.EndSession(tok2)
	if a.ValidSession(tok2) {
		t.Fatal("ended session accepted")
	}
}

func TestLoginRateLimit(t *testing.T) {
	a := auth.New("correct horse battery")
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a.SetClock(func() time.Time { return now })

	for i := 0; i < 5; i++ {
		if a.Locked("1.2.3.4") {
			t.Fatalf("locked after only %d failures", i)
		}
		a.RecordFailure("1.2.3.4")
	}
	if !a.Locked("1.2.3.4") {
		t.Fatal("not locked after 5 failures")
	}
	if a.Locked("5.6.7.8") {
		t.Fatal("lock leaked to another key")
	}

	now = now.Add(11 * time.Minute)
	if a.Locked("1.2.3.4") {
		t.Fatal("still locked after the window passed")
	}

	a.RecordFailure("9.9.9.9")
	a.ResetFailures("9.9.9.9")
	for i := 0; i < 4; i++ {
		a.RecordFailure("9.9.9.9")
	}
	if a.Locked("9.9.9.9") {
		t.Fatal("ResetFailures did not clear the counter")
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/auth/ -v`
Expected: FAIL (build error: undefined `auth.New`).

- [ ] **Step 4: Write the auth package**

Create `internal/auth/auth.go`:

```go
// Package auth implements the single-admin login: password check, in-memory sessions and a
// login rate limit. Sessions do not survive a restart, which is acceptable for one admin.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

const (
	SessionTTL     = 24 * time.Hour
	maxFailures    = 5
	failureWindow  = 10 * time.Minute
	argonTime      = 1
	argonMemoryKiB = 64 * 1024
	argonThreads   = 4
	argonKeyLen    = 32
)

type Auth struct {
	salt []byte
	hash []byte

	mu       sync.Mutex
	now      func() time.Time
	sessions map[string]time.Time // token -> expiry
	failures map[string][]time.Time
}

// New derives an Argon2id hash of password. The plaintext is not kept.
func New(password string) *Auth {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		panic(err)
	}
	return &Auth{
		salt:     salt,
		hash:     derive(password, salt),
		now:      time.Now,
		sessions: map[string]time.Time{},
		failures: map[string][]time.Time{},
	}
}

func derive(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLen)
}

// SetClock replaces the time source (tests only).
func (a *Auth) SetClock(now func() time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.now = now
}

func (a *Auth) Verify(password string) bool {
	return subtle.ConstantTimeCompare(derive(password, a.salt), a.hash) == 1
}

func (a *Auth) NewSession() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	tok := base64.RawURLEncoding.EncodeToString(b)

	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	for t, exp := range a.sessions { // opportunistic cleanup
		if now.After(exp) {
			delete(a.sessions, t)
		}
	}
	a.sessions[tok] = now.Add(SessionTTL)
	return tok
}

func (a *Auth) ValidSession(token string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	exp, ok := a.sessions[token]
	if !ok {
		return false
	}
	if a.now().After(exp) {
		delete(a.sessions, token)
		return false
	}
	return true
}

func (a *Auth) EndSession(token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, token)
}

// Locked reports whether key has hit the failure limit within the window.
func (a *Auth) Locked(key string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.recent(key)) >= maxFailures
}

func (a *Auth) RecordFailure(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failures[key] = append(a.recent(key), a.now())
}

func (a *Auth) ResetFailures(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.failures, key)
}

// recent drops failures outside the window and returns the rest. Callers hold a.mu.
func (a *Auth) recent(key string) []time.Time {
	cutoff := a.now().Add(-failureWindow)
	kept := a.failures[key][:0]
	for _, t := range a.failures[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	a.failures[key] = kept
	return kept
}
```

- [ ] **Step 5: Run the auth tests to verify they pass**

Run: `go mod tidy && go test ./internal/auth/ -v -race`
Expected: PASS.

- [ ] **Step 6: Write the failing config tests**

Create `internal/config/config_test.go`:

```go
package config_test

import (
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/config"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestServerFromEnvDefaults(t *testing.T) {
	c, err := config.ServerFromEnv(env(map[string]string{
		"REMEDY_ADMIN_PASSWORD": "a-long-enough-password",
		"REMEDY_RUNNER_TOKEN":   "a-runner-token-of-24-chars-or-more",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != ":8080" || c.DBPath != "remedy.db" {
		t.Fatalf("defaults = %+v", c)
	}
}

func TestServerFromEnvRejectsWeakSecrets(t *testing.T) {
	cases := map[string]map[string]string{
		"missing password":     {"REMEDY_RUNNER_TOKEN": "a-runner-token-of-24-chars-or-more"},
		"short password":       {"REMEDY_ADMIN_PASSWORD": "short", "REMEDY_RUNNER_TOKEN": "a-runner-token-of-24-chars-or-more"},
		"missing runner token": {"REMEDY_ADMIN_PASSWORD": "a-long-enough-password"},
		"short runner token":   {"REMEDY_ADMIN_PASSWORD": "a-long-enough-password", "REMEDY_RUNNER_TOKEN": "short"},
	}
	for name, m := range cases {
		if _, err := config.ServerFromEnv(env(m)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestRunnerFromEnv(t *testing.T) {
	c, err := config.RunnerFromEnv(env(map[string]string{
		"REMEDY_RUNNER_TOKEN": "a-runner-token-of-24-chars-or-more",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.ServerURL != "http://localhost:8080" || c.ClaudeBin != "claude" ||
		!strings.HasSuffix(c.WorkspaceRoot, "remedy-workspaces") {
		t.Fatalf("defaults = %+v", c)
	}

	if _, err := config.RunnerFromEnv(env(nil)); err == nil {
		t.Fatal("expected an error without a runner token")
	}
}
```

- [ ] **Step 7: Run the config tests to verify they fail**

Run: `go test ./internal/config/ -v`
Expected: FAIL (build error: undefined `config.ServerFromEnv`).

- [ ] **Step 8: Write the config package**

Create `internal/config/config.go`:

```go
// Package config reads process configuration from environment variables.
package config

import (
	"errors"
	"os"
	"path/filepath"
)

const (
	minPasswordLen = 12
	minTokenLen    = 24
)

type Server struct {
	Addr          string // REMEDY_ADDR, default ":8080"
	DBPath        string // REMEDY_DB, default "remedy.db"
	AdminPassword string // REMEDY_ADMIN_PASSWORD, required, min 12 chars
	RunnerToken   string // REMEDY_RUNNER_TOKEN, required, min 24 chars
}

func ServerFromEnv(get func(string) string) (Server, error) {
	c := Server{
		Addr:          orDefault(get("REMEDY_ADDR"), ":8080"),
		DBPath:        orDefault(get("REMEDY_DB"), "remedy.db"),
		AdminPassword: get("REMEDY_ADMIN_PASSWORD"),
		RunnerToken:   get("REMEDY_RUNNER_TOKEN"),
	}
	if len(c.AdminPassword) < minPasswordLen {
		return Server{}, errors.New("REMEDY_ADMIN_PASSWORD must be set and at least 12 characters")
	}
	if len(c.RunnerToken) < minTokenLen {
		return Server{}, errors.New("REMEDY_RUNNER_TOKEN must be set and at least 24 characters")
	}
	return c, nil
}

type Runner struct {
	ServerURL     string // REMEDY_SERVER_URL, default "http://localhost:8080"
	Token         string // REMEDY_RUNNER_TOKEN, required, min 24 chars
	WorkspaceRoot string // REMEDY_WORKSPACES, default $TMPDIR/remedy-workspaces
	ClaudeBin     string // REMEDY_CLAUDE_BIN, default "claude"
}

func RunnerFromEnv(get func(string) string) (Runner, error) {
	c := Runner{
		ServerURL:     orDefault(get("REMEDY_SERVER_URL"), "http://localhost:8080"),
		Token:         get("REMEDY_RUNNER_TOKEN"),
		WorkspaceRoot: orDefault(get("REMEDY_WORKSPACES"), filepath.Join(os.TempDir(), "remedy-workspaces")),
		ClaudeBin:     orDefault(get("REMEDY_CLAUDE_BIN"), "claude"),
	}
	if len(c.Token) < minTokenLen {
		return Runner{}, errors.New("REMEDY_RUNNER_TOKEN must be set and at least 24 characters")
	}
	return c, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
```

- [ ] **Step 9: Run both packages' tests**

Run: `gofmt -w internal/config && go test ./internal/auth/ ./internal/config/ -race`
Expected: PASS.

- [ ] **Step 10: Commit**

```bash
make check
git add go.mod go.sum internal/auth internal/config
git commit -m "feat(auth): add admin login, sessions, rate limit and env config" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Control plane HTTP API (admin API, runner API, live stream)

**Files:**
- Create: `internal/server/server.go` (replaces the Task-0 stub, `New` signature changes)
- Create: `internal/server/session.go`
- Create: `internal/server/runs.go`
- Create: `internal/server/runnerapi.go`
- Create: `internal/server/stream.go`
- Create: `internal/server/spa.go`
- Modify: `internal/server/server_test.go`

**Interfaces:**
- Consumes: `store.Store` methods, `auth.Auth` methods (Tasks 2 and 5), `run` types.
- Produces:
  - `type Deps struct{ Store *store.Store; Auth *auth.Auth; RunnerToken string; Web fs.FS }` (`Web` may be nil)
  - `func New(d Deps) http.Handler`
  - Routes: `GET /healthz`; `POST /api/login` (`{"password"}` returns 204 and sets cookie `remedy_session`); `POST /api/logout`; `GET /api/me`; `POST /api/runs` (`{"provider","prompt"}` returns 201 and a `run.Run`); `GET /api/runs`; `GET /api/runs/{id}`; `GET /api/runs/{id}/events` (SSE: `event: run_event` with `id: <seq>` and a `run.Event` JSON; final `event: done` with the `run.Run`); `POST /runner/v1/claim` (200 with `run.Run` after up to 25 s, else 204); `POST /runner/v1/runs/{id}/events` (`{"kind","payload"}` returns 204); `POST /runner/v1/runs/{id}/finish` (a `run.Outcome` returns 204)

- [ ] **Step 1: Replace the test file with failing tests**

Overwrite `internal/server/server_test.go`:

```go
package server_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/store"
)

const (
	password    = "correct horse battery"
	runnerToken = "runner-token-with-at-least-24-chars"
)

type env struct {
	ts    *httptest.Server
	store *store.Store
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ts := httptest.NewServer(server.New(server.Deps{Store: st, Auth: auth.New(password), RunnerToken: runnerToken}))
	t.Cleanup(ts.Close)
	return &env{ts: ts, store: st}
}

// adminClient returns a client with a cookie jar, logged in if login is true.
func (e *env) adminClient(t *testing.T, login bool) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if login {
		resp := e.do(t, c, http.MethodPost, "/api/login", `{"password":"`+password+`"}`, true)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("login status = %d", resp.StatusCode)
		}
	}
	return c
}

func (e *env) do(t *testing.T, c *http.Client, method, path, body string, csrf bool) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, e.ts.URL+path, strings.NewReader(body))
	if csrf {
		req.Header.Set("X-Remedy-CSRF", "1")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func (e *env) runnerDo(t *testing.T, method, path, body string, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, e.ts.URL+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func decode[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return v
}

func TestHealthz(t *testing.T) {
	e := newEnv(t)
	resp := e.do(t, http.DefaultClient, http.MethodGet, "/healthz", "", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestAdminAPIRequiresSession(t *testing.T) {
	e := newEnv(t)
	c := e.adminClient(t, false)
	for _, path := range []string{"/api/me", "/api/runs"} {
		if got := e.do(t, c, http.MethodGet, path, "", false).StatusCode; got != http.StatusUnauthorized {
			t.Errorf("GET %s without session = %d, want 401", path, got)
		}
	}
}

func TestLoginRejectsWrongPasswordAndRateLimits(t *testing.T) {
	e := newEnv(t)
	c := e.adminClient(t, false)
	for i := 0; i < 5; i++ {
		resp := e.do(t, c, http.MethodPost, "/api/login", `{"password":"wrong"}`, true)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401", i, resp.StatusCode)
		}
	}
	// Even the right password is refused while locked.
	resp := e.do(t, c, http.MethodPost, "/api/login", `{"password":"`+password+`"}`, true)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("locked login status = %d, want 429", resp.StatusCode)
	}
}

func TestStateChangingRequestsNeedCSRFHeader(t *testing.T) {
	e := newEnv(t)
	c := e.adminClient(t, true)
	resp := e.do(t, c, http.MethodPost, "/api/runs", `{"prompt":"hi"}`, false)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if e.do(t, http.DefaultClient, http.MethodPost, "/api/login", `{"password":"`+password+`"}`, false).StatusCode != http.StatusForbidden {
		t.Fatal("login without CSRF header should be 403")
	}
}

func TestSessionCookieFlags(t *testing.T) {
	e := newEnv(t)
	resp := e.do(t, http.DefaultClient, http.MethodPost, "/api/login", `{"password":"`+password+`"}`, true)
	var found bool
	for _, ck := range resp.Cookies() {
		if ck.Name == "remedy_session" {
			found = true
			if !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode {
				t.Fatalf("cookie flags = %+v", ck)
			}
		}
	}
	if !found {
		t.Fatal("no session cookie set")
	}
}

func TestCreateAndListRuns(t *testing.T) {
	e := newEnv(t)
	c := e.adminClient(t, true)

	if e.do(t, c, http.MethodPost, "/api/runs", `{"prompt":""}`, true).StatusCode != http.StatusBadRequest {
		t.Fatal("empty prompt should be 400")
	}
	if e.do(t, c, http.MethodPost, "/api/runs", `{"provider":"gemini","prompt":"x"}`, true).StatusCode != http.StatusBadRequest {
		t.Fatal("unknown provider should be 400")
	}

	resp := e.do(t, c, http.MethodPost, "/api/runs", `{"prompt":"say pong"}`, true)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", resp.StatusCode)
	}
	created := decode[run.Run](t, resp)
	if created.Provider != "claude" || created.Status != run.Queued {
		t.Fatalf("created = %+v", created)
	}

	list := decode[[]run.Run](t, e.do(t, c, http.MethodGet, "/api/runs", "", false))
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("list = %+v", list)
	}

	if e.do(t, c, http.MethodGet, "/api/runs/"+created.ID, "", false).StatusCode != http.StatusOK {
		t.Fatal("get run failed")
	}
	if e.do(t, c, http.MethodGet, "/api/runs/missing", "", false).StatusCode != http.StatusNotFound {
		t.Fatal("missing run should be 404")
	}
}

func TestRunnerAPIRequiresToken(t *testing.T) {
	e := newEnv(t)
	for _, token := range []string{"", "wrong-token-wrong-token-wrong"} {
		resp := e.runnerDo(t, http.MethodPost, "/runner/v1/claim", "", token)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("token %q: status = %d, want 401", token, resp.StatusCode)
		}
	}
}

func TestRunnerLifecycle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	queued, _ := e.store.CreateRun(ctx, "claude", "say pong")

	claim := e.runnerDo(t, http.MethodPost, "/runner/v1/claim", "", runnerToken)
	if claim.StatusCode != http.StatusOK {
		t.Fatalf("claim status = %d", claim.StatusCode)
	}
	claimed := decode[run.Run](t, claim)
	if claimed.ID != queued.ID || claimed.Status != run.Running {
		t.Fatalf("claimed = %+v", claimed)
	}

	post := e.runnerDo(t, http.MethodPost, "/runner/v1/runs/"+claimed.ID+"/events",
		`{"kind":"system","payload":{"a":1}}`, runnerToken)
	if post.StatusCode != http.StatusNoContent {
		t.Fatalf("event status = %d", post.StatusCode)
	}
	if e.runnerDo(t, http.MethodPost, "/runner/v1/runs/"+claimed.ID+"/events", `{"kind":"","payload":1}`, runnerToken).StatusCode != http.StatusBadRequest {
		t.Fatal("empty kind should be 400")
	}
	if e.runnerDo(t, http.MethodPost, "/runner/v1/runs/missing/events", `{"kind":"x","payload":1}`, runnerToken).StatusCode != http.StatusNotFound {
		t.Fatal("event for a missing run should be 404")
	}

	fin := e.runnerDo(t, http.MethodPost, "/runner/v1/runs/"+claimed.ID+"/finish",
		`{"exitCode":0,"result":"pong","sessionId":"s-1","costUsd":0.01}`, runnerToken)
	if fin.StatusCode != http.StatusNoContent {
		t.Fatalf("finish status = %d", fin.StatusCode)
	}
	got, _ := e.store.GetRun(ctx, claimed.ID)
	if got.Status != run.Succeeded || got.Result != "pong" {
		t.Fatalf("run = %+v", got)
	}
	if e.runnerDo(t, http.MethodPost, "/runner/v1/runs/"+claimed.ID+"/finish", `{"exitCode":0}`, runnerToken).StatusCode != http.StatusNotFound {
		t.Fatal("finishing twice should be 404")
	}
}

func TestStreamEventsDeliversBacklogLiveEventsAndDone(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.adminClient(t, true)

	r, _ := e.store.CreateRun(ctx, "claude", "x")
	_, _ = e.store.ClaimNext(ctx)
	_, _ = e.store.AppendEvent(ctx, r.ID, "system", json.RawMessage(`{"n":1}`))

	resp := e.do(t, c, http.MethodGet, "/api/runs/"+r.ID+"/events", "", false)
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("content type = %q", got)
	}

	go func() {
		time.Sleep(100 * time.Millisecond)
		e.runnerDo(t, http.MethodPost, "/runner/v1/runs/"+r.ID+"/events", `{"kind":"assistant","payload":{"n":2}}`, runnerToken)
		e.runnerDo(t, http.MethodPost, "/runner/v1/runs/"+r.ID+"/finish", `{"exitCode":0,"result":"ok"}`, runnerToken)
	}()

	var names []string
	sc := bufio.NewScanner(resp.Body)
	deadline := time.AfterFunc(5*time.Second, func() { _ = resp.Body.Close() })
	defer deadline.Stop()
	for sc.Scan() {
		if name, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
			names = append(names, name)
			if name == "done" {
				break
			}
		}
	}
	if strings.Join(names, ",") != "run_event,run_event,done" {
		t.Fatalf("events = %v", names)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/server/ -v`
Expected: FAIL (build error: `server.Deps` undefined / `server.New` takes no arguments).

- [ ] **Step 3: Write routing and shared helpers**

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

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/store"
)

type Deps struct {
	Store       *store.Store
	Auth        *auth.Auth
	RunnerToken string
	Web         fs.FS // optional: the built UI, served for every non-API path
}

type srv struct {
	d   Deps
	hub *hub
}

func New(d Deps) http.Handler {
	s := &srv{d: d, hub: newHub()}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.session(s.logout))
	mux.HandleFunc("GET /api/me", s.session(s.me))
	mux.HandleFunc("POST /api/runs", s.session(s.createRun))
	mux.HandleFunc("GET /api/runs", s.session(s.listRuns))
	mux.HandleFunc("GET /api/runs/{id}", s.session(s.getRun))
	mux.HandleFunc("GET /api/runs/{id}/events", s.session(s.streamEvents))

	mux.HandleFunc("POST /runner/v1/claim", s.runner(s.claim))
	mux.HandleFunc("POST /runner/v1/runs/{id}/events", s.runner(s.postEvent))
	mux.HandleFunc("POST /runner/v1/runs/{id}/finish", s.runner(s.finish))

	if d.Web != nil {
		mux.Handle("/", spa(d.Web))
	}
	return mux
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

- [ ] **Step 4: Write the session middleware and login**

Create `internal/server/session.go`:

```go
package server

import (
	"encoding/json"
	"net/http"

	"github.com/Jaydee94/remedy/internal/auth"
)

const (
	cookieName = "remedy_session"
	csrfHeader = "X-Remedy-CSRF"
)

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

func needsCSRF(r *http.Request) bool {
	return r.Method != http.MethodGet && r.Method != http.MethodHead
}

// session guards an admin handler: valid session cookie, plus the CSRF header on state changes.
func (s *srv) session(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil || !s.d.Auth.ValidSession(c.Value) {
			writeErr(w, http.StatusUnauthorized, "not logged in")
			return
		}
		if needsCSRF(r) && r.Header.Get(csrfHeader) != "1" {
			writeErr(w, http.StatusForbidden, "missing "+csrfHeader+" header")
			return
		}
		next(w, r)
	}
}

func (s *srv) login(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get(csrfHeader) != "1" {
		writeErr(w, http.StatusForbidden, "missing "+csrfHeader+" header")
		return
	}
	ip := clientIP(r)
	if s.d.Auth.Locked(ip) {
		writeErr(w, http.StatusTooManyRequests, "too many failed attempts, try again later")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !s.d.Auth.Verify(req.Password) {
		s.d.Auth.RecordFailure(ip)
		writeErr(w, http.StatusUnauthorized, "wrong password")
		return
	}
	s.d.Auth.ResetFailures(ip)
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    s.d.Auth.NewSession(),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   isHTTPS(r),
		MaxAge:   int(auth.SessionTTL.Seconds()),
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		s.d.Auth.EndSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) me(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"user": "admin"})
}
```

- [ ] **Step 5: Write the admin run handlers**

Create `internal/server/runs.go`:

```go
package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Jaydee94/remedy/internal/store"
)

const maxPromptBytes = 20_000

func (s *srv) createRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider string `json:"provider"`
		Prompt   string `json:"prompt"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Provider == "" {
		req.Provider = "claude"
	}
	// Phase 4 replaces this with a provider registry.
	if req.Provider != "claude" {
		writeErr(w, http.StatusBadRequest, "unknown provider")
		return
	}
	if req.Prompt == "" || len(req.Prompt) > maxPromptBytes {
		writeErr(w, http.StatusBadRequest, "prompt must be 1 to 20000 bytes")
		return
	}
	created, err := s.d.Store.CreateRun(r.Context(), req.Provider, req.Prompt)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not create run")
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *srv) listRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.d.Store.ListRuns(r.Context(), 50)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not list runs")
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (s *srv) getRun(w http.ResponseWriter, r *http.Request) {
	got, err := s.d.Store.GetRun(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load run")
		return
	}
	writeJSON(w, http.StatusOK, got)
}
```

- [ ] **Step 6: Write the runner API**

Create `internal/server/runnerapi.go`:

```go
package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/store"
)

const (
	claimWait     = 25 * time.Second
	claimInterval = 500 * time.Millisecond
)

// runner guards a runner-API handler with the shared bearer token.
func (s *srv) runner(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(s.d.RunnerToken)) != 1 {
			writeErr(w, http.StatusUnauthorized, "invalid runner token")
			return
		}
		next(w, r)
	}
}

// claim long-polls for the next queued run and returns 204 if none arrives in time.
func (s *srv) claim(w http.ResponseWriter, r *http.Request) {
	deadline := time.NewTimer(claimWait)
	defer deadline.Stop()
	tick := time.NewTicker(claimInterval)
	defer tick.Stop()
	for {
		claimed, err := s.d.Store.ClaimNext(r.Context())
		if err != nil {
			if r.Context().Err() == nil {
				writeErr(w, http.StatusInternalServerError, "claim failed")
			}
			return
		}
		if claimed != nil {
			writeJSON(w, http.StatusOK, claimed)
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			w.WriteHeader(http.StatusNoContent)
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (s *srv) postEvent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Kind    string          `json:"kind"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&req); err != nil || req.Kind == "" || len(req.Payload) == 0 {
		writeErr(w, http.StatusBadRequest, "kind and payload are required")
		return
	}
	if _, err := s.d.Store.GetRun(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load run")
		return
	}
	if _, err := s.d.Store.AppendEvent(r.Context(), id, req.Kind, req.Payload); err != nil {
		writeErr(w, http.StatusBadRequest, "could not store event")
		return
	}
	s.hub.notify(id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) finish(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var out run.Outcome
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&out); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	err := s.d.Store.FinishRun(r.Context(), id, out)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "run not found or not running")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not finish run")
		return
	}
	s.hub.notify(id)
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 7: Write the hub and the SSE stream**

Create `internal/server/stream.go`:

```go
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Jaydee94/remedy/internal/store"
)

const keepAlive = 15 * time.Second

// hub wakes SSE streams when a run gets a new event or finishes.
type hub struct {
	mu   sync.Mutex
	subs map[string]map[chan struct{}]struct{}
}

func newHub() *hub { return &hub{subs: map[string]map[chan struct{}]struct{}{}} }

func (h *hub) subscribe(runID string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	if h.subs[runID] == nil {
		h.subs[runID] = map[chan struct{}]struct{}{}
	}
	h.subs[runID][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.subs[runID], ch)
		if len(h.subs[runID]) == 0 {
			delete(h.subs, runID)
		}
	}
}

func (h *hub) notify(runID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[runID] {
		select {
		case ch <- struct{}{}:
		default: // a wake-up is already pending
		}
	}
}

func writeSSE(w http.ResponseWriter, id, event string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if id != "" {
		fmt.Fprintf(w, "id: %s\n", id)
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	return err
}

// streamEvents sends the backlog, then live events, then a final "done" event with the run.
// Reconnecting clients resume from Last-Event-ID.
func (s *srv) streamEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.d.Store.GetRun(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load run")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")

	last, _ := strconv.Atoi(r.Header.Get("Last-Event-ID"))
	wake, cancel := s.hub.subscribe(id)
	defer cancel()

	for {
		// Read the status before the events: the runner posts every event before it finishes
		// the run, so a terminal status guarantees the following read sees all events.
		current, err := s.d.Store.GetRun(r.Context(), id)
		if err != nil {
			return
		}
		events, err := s.d.Store.Events(r.Context(), id, last)
		if err != nil {
			return
		}
		for _, e := range events {
			if writeSSE(w, strconv.Itoa(e.Seq), "run_event", e) != nil {
				return
			}
			last = e.Seq
		}
		if current.Status.Terminal() {
			_ = writeSSE(w, "", "done", current)
			flusher.Flush()
			return
		}
		flusher.Flush()

		select {
		case <-wake:
		case <-time.After(keepAlive):
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
```

- [ ] **Step 8: Write the SPA handler**

Create `internal/server/spa.go`:

```go
package server

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// spa serves a built single-page app: existing files as-is, everything else as index.html.
// Unknown /api/ and /runner/ paths stay 404 instead of returning the HTML shell.
func spa(fsys fs.FS) http.Handler {
	files := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/runner/") {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if f, err := fsys.Open(name); err == nil {
			_ = f.Close()
			files.ServeHTTP(w, r)
			return
		}
		r.URL.Path = "/"
		files.ServeHTTP(w, r)
	})
}
```

- [ ] **Step 9: Run the server tests**

Run: `gofmt -l internal/ ; go vet ./internal/server/ && go test ./internal/server/ -v -race`
Expected: PASS for all tests (the login lock test takes about five Argon2 derivations, so it needs a second or two). If `gofmt -l` lists files, run `gofmt -w` on them.

- [ ] **Step 10: Adapt the old binary and commit**

`cmd/remedy-server/main.go` still calls `server.New()` without arguments and no longer compiles. It is rewritten in Task 8; until then, only run the package tests, not `make check`.

```bash
git add internal/server
git commit -m "feat(server): add admin API, runner API and SSE run stream" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Runner client and claim/execute/finish loop

**Files:**
- Create: `internal/runner/client.go`
- Create: `internal/runner/loop.go`
- Test: `internal/runner/loop_test.go`

**Interfaces:**
- Consumes: `Execute`, `Sink` (Task 4); `provider.Provider` (Task 3); `run` types; the runner API of Task 6.
- Produces:
  - `type Client struct{ BaseURL, Token string; HTTP *http.Client }` with `Claim(ctx) (*run.Run, error)` (nil on 204), `Event(ctx, runID, kind string, payload json.RawMessage) error`, `Finish(ctx, runID string, o run.Outcome) error`
  - `type Loop struct{ Client *Client; Providers map[string]provider.Provider; WorkspaceRoot string; Env []string; Log *slog.Logger; Backoff time.Duration }` with `Run(ctx context.Context)` (returns when ctx is cancelled)

- [ ] **Step 1: Write the failing end-to-end test**

Create `internal/runner/loop_test.go`:

```go
package runner_test

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/runner"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/store"
	"github.com/Jaydee94/remedy/internal/testutil"
)

const token = "runner-token-with-at-least-24-chars"

func startLoop(t *testing.T, providers map[string]provider.Provider) (*store.Store, *httptest.Server) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ts := httptest.NewServer(server.New(server.Deps{Store: st, Auth: auth.New("correct horse battery"), RunnerToken: token}))
	t.Cleanup(ts.Close)

	loop := &runner.Loop{
		Client:        &runner.Client{BaseURL: ts.URL, Token: token, HTTP: ts.Client()},
		Providers:     providers,
		WorkspaceRoot: t.TempDir(),
		Env:           os.Environ(),
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Backoff:       50 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { loop.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done }) // runs before ts.Close (LIFO)
	return st, ts
}

func waitTerminal(t *testing.T, st *store.Store, id string) run.Run {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		r, err := st.GetRun(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if r.Status.Terminal() {
			return r
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("run did not finish in time")
	return run.Run{}
}

func TestLoopRunsQueuedRunEndToEnd(t *testing.T) {
	st, _ := startLoop(t, map[string]provider.Provider{
		"claude": provider.Claude{Binary: testutil.FakeClaude(t, 0)},
	})
	queued, _ := st.CreateRun(context.Background(), "claude", "hello")

	got := waitTerminal(t, st, queued.ID)
	if got.Status != run.Succeeded || got.Result != "done" || got.SessionID != "s-1" {
		t.Fatalf("run = %+v", got)
	}

	events, _ := st.Events(context.Background(), queued.ID, 0)
	if len(events) < 4 {
		t.Fatalf("expected the CLI's lines as events, got %d", len(events))
	}
	if events[0].Kind != "system" {
		t.Fatalf("first event kind = %q", events[0].Kind)
	}
}

func TestLoopReportsFailedExit(t *testing.T) {
	st, _ := startLoop(t, map[string]provider.Provider{
		"claude": provider.Claude{Binary: testutil.FakeClaude(t, 3)},
	})
	queued, _ := st.CreateRun(context.Background(), "claude", "hello")

	got := waitTerminal(t, st, queued.ID)
	if got.Status != run.Failed || got.ExitCode == nil || *got.ExitCode != 3 {
		t.Fatalf("run = %+v", got)
	}
}

func TestLoopFailsRunWithUnknownProvider(t *testing.T) {
	st, _ := startLoop(t, map[string]provider.Provider{})
	queued, _ := st.CreateRun(context.Background(), "claude", "hello")

	got := waitTerminal(t, st, queued.ID)
	if got.Status != run.Failed || got.ExitCode == nil || *got.ExitCode != 127 {
		t.Fatalf("run = %+v", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/runner/ -run Loop -v`
Expected: FAIL (build error: undefined `runner.Loop`, `runner.Client`).

- [ ] **Step 3: Write the HTTP client**

Create `internal/runner/client.go`:

```go
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Jaydee94/remedy/internal/run"
)

const (
	claimTimeout   = 40 * time.Second // server long-polls for 25 s
	requestTimeout = 15 * time.Second
)

// Client talks to the control plane's runner API. The runner only ever dials out.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func (c *Client) do(ctx context.Context, timeout time.Duration, path string, body any) (*http.Response, error) {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, &buf)
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

func expect(resp *http.Response, want int) error {
	if resp.StatusCode == want {
		return nil
	}
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, bytes.TrimSpace(msg))
}

// Claim blocks until a run is available or the server's long-poll times out (nil, nil).
func (c *Client) Claim(ctx context.Context) (*run.Run, error) {
	resp, err := c.do(ctx, claimTimeout, "/runner/v1/claim", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if err := expect(resp, http.StatusOK); err != nil {
		return nil, err
	}
	var r run.Run
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) Event(ctx context.Context, runID, kind string, payload json.RawMessage) error {
	resp, err := c.do(ctx, requestTimeout, "/runner/v1/runs/"+runID+"/events",
		map[string]any{"kind": kind, "payload": payload})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return expect(resp, http.StatusNoContent)
}

func (c *Client) Finish(ctx context.Context, runID string, o run.Outcome) error {
	resp, err := c.do(ctx, requestTimeout, "/runner/v1/runs/"+runID+"/finish", o)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return expect(resp, http.StatusNoContent)
}
```

- [ ] **Step 4: Write the loop**

Create `internal/runner/loop.go`:

```go
package runner

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"time"

	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/run"
)

// Loop claims runs one at a time, executes them and reports the outcome.
// Known limitation (phase 0): if the runner dies mid-run the run stays "running";
// a reaper for stale runs arrives with phase 1.
type Loop struct {
	Client        *Client
	Providers     map[string]provider.Provider
	WorkspaceRoot string
	Env           []string
	Log           *slog.Logger
	Backoff       time.Duration // wait after a failed claim, default 3s
}

func (l *Loop) Run(ctx context.Context) {
	backoff := l.Backoff
	if backoff == 0 {
		backoff = 3 * time.Second
	}
	for ctx.Err() == nil {
		r, err := l.Client.Claim(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			l.Log.Warn("claim failed", "err", err)
			sleep(ctx, backoff)
			continue
		}
		if r == nil {
			continue // long-poll timed out, ask again
		}
		l.handle(ctx, *r)
	}
}

func (l *Loop) handle(ctx context.Context, r run.Run) {
	log := l.Log.With("run", r.ID, "provider", r.Provider)
	log.Info("run claimed")

	p, ok := l.Providers[r.Provider]
	if !ok {
		l.finish(log, r.ID, run.Outcome{ExitCode: 127, Result: "unknown provider " + r.Provider})
		return
	}

	dir, err := os.MkdirTemp(l.WorkspaceRoot, r.ID+"-")
	if err != nil {
		l.finish(log, r.ID, run.Outcome{ExitCode: 127, Result: "cannot create workspace: " + err.Error()})
		return
	}
	defer os.RemoveAll(dir)

	out, err := Execute(ctx, p, provider.Spec{Prompt: r.Prompt, Workdir: dir}, l.Env,
		clientSink{client: l.Client, runID: r.ID})
	if err != nil {
		log.Error("execution problem", "err", err)
	}
	l.finish(log, r.ID, out)
}

// finish reports the outcome even when the runner is shutting down.
func (l *Loop) finish(log *slog.Logger, runID string, out run.Outcome) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := l.Client.Finish(ctx, runID, out); err != nil {
		log.Error("finish failed", "err", err)
		return
	}
	log.Info("run finished", "exit", out.ExitCode)
}

type clientSink struct {
	client *Client
	runID  string
}

func (s clientSink) Event(ctx context.Context, kind string, payload json.RawMessage) error {
	return s.client.Event(ctx, s.runID, kind, payload)
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}
```

- [ ] **Step 5: Run the runner tests**

Run: `go vet ./internal/runner/ && go test ./internal/runner/ -v -race`
Expected: PASS for the three loop tests and the three execute tests.

- [ ] **Step 6: Commit**

```bash
git add internal/runner
git commit -m "feat(runner): add API client and claim/execute/finish loop" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Wire up the two binaries

**Files:**
- Modify: `cmd/remedy-server/main.go`
- Modify: `cmd/remedy-runner/main.go`
- Modify: `Makefile`, `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: everything from Tasks 2 to 7.
- Produces: runnable `remedy-server` and `remedy-runner`; Makefile targets `build-go` (Go only) and `build` (UI plus Go with the `webui` tag, completed in Task 10).

- [ ] **Step 1: Rewrite the server main**

Overwrite `cmd/remedy-server/main.go`:

```go
// Command remedy-server runs the Remedy control plane.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Jaydee94/remedy/internal/auth"
	"github.com/Jaydee94/remedy/internal/config"
	"github.com/Jaydee94/remedy/internal/server"
	"github.com/Jaydee94/remedy/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfg, err := config.ServerFromEnv(os.Getenv)
	if err != nil {
		log.Error("invalid configuration", "err", err)
		os.Exit(2)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Error("cannot open database", "path", cfg.DBPath, "err", err)
		os.Exit(1)
	}
	defer st.Close()

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: server.New(server.Deps{
			Store:       st,
			Auth:        auth.New(cfg.AdminPassword),
			RunnerToken: cfg.RunnerToken,
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Info("control plane listening", "addr", cfg.Addr, "db", cfg.DBPath)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server failed", "err", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 2: Rewrite the runner main**

Overwrite `cmd/remedy-runner/main.go`:

```go
// Command remedy-runner executes agent CLI subprocesses on behalf of the control plane.
//
// The runner holds the CLI logins and nothing else. It dials the control plane
// (outbound only), pulls jobs and never reads or copies CLI credentials.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/Jaydee94/remedy/internal/config"
	"github.com/Jaydee94/remedy/internal/provider"
	"github.com/Jaydee94/remedy/internal/runner"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfg, err := config.RunnerFromEnv(os.Getenv)
	if err != nil {
		log.Error("invalid configuration", "err", err)
		os.Exit(2)
	}
	if err := os.MkdirAll(cfg.WorkspaceRoot, 0o700); err != nil {
		log.Error("cannot create workspace root", "path", cfg.WorkspaceRoot, "err", err)
		os.Exit(1)
	}

	loop := &runner.Loop{
		Client:        &runner.Client{BaseURL: cfg.ServerURL, Token: cfg.Token, HTTP: &http.Client{}},
		Providers:     map[string]provider.Provider{"claude": provider.Claude{Binary: cfg.ClaudeBin}},
		WorkspaceRoot: cfg.WorkspaceRoot,
		Env:           os.Environ(),
		Log:           log,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("runner started", "server", cfg.ServerURL, "workspaces", cfg.WorkspaceRoot)
	loop.Run(ctx)
	log.Info("runner stopped")
}
```

- [ ] **Step 3: Update the Makefile and CI**

In `Makefile`, replace the `.PHONY` line and the `build` target with:

```make
.PHONY: help build build-go test vet fmt web-install web-build web-lint dev-server dev-web check

build-go: ## Build both Go binaries into ./bin (no UI embedded)
	go build -o bin/remedy-server ./cmd/remedy-server
	go build -o bin/remedy-runner ./cmd/remedy-runner

build: web-build ## Build the UI, then both Go binaries with the UI embedded
	go build -tags webui -o bin/remedy-server ./cmd/remedy-server
	go build -o bin/remedy-runner ./cmd/remedy-runner
```

In `.github/workflows/ci.yml`, change the Go job's run line to `- run: make fmt vet test build-go`.

- [ ] **Step 4: Verify everything**

Run: `make check && make build-go`
Expected: all green; `bin/remedy-server` and `bin/remedy-runner` exist.

Run: `REMEDY_ADMIN_PASSWORD=short ./bin/remedy-server; echo "exit=$?"`
Expected: log line `invalid configuration` and `exit=2`.

- [ ] **Step 5: Commit**

```bash
git add cmd Makefile .github
git commit -m "feat: wire control plane and runner binaries" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 9: UI: login, runs list, new run, live run view

Phase 0 delivers a functional, restrained UI. The "super modern" visual design pass happens in phase 1 using the `frontend-design` skill, once the real data shapes from the spike are known. shadcn/ui is introduced then too. `tsconfig` has `erasableSyntaxOnly` and `verbatimModuleSyntax`, so: no parameter properties, no enums, and `import type` for types.

**Files:**
- Create: `web/src/api.ts`
- Create: `web/src/Login.tsx`
- Create: `web/src/RunsPage.tsx`
- Create: `web/src/RunView.tsx`
- Modify: `web/src/App.tsx`

**Interfaces:**
- Consumes: the HTTP routes of Task 6.
- Produces: the UI. Hash routes: `#/` (runs list and form), `#/runs/<id>` (live view). Verified by the type checker, the linter and a manual check in Task 10.

- [ ] **Step 1: Write the API client**

Create `web/src/api.ts`:

```ts
export type RunStatus = 'queued' | 'running' | 'succeeded' | 'failed'

export interface Run {
  id: string
  provider: string
  prompt: string
  status: RunStatus
  exitCode?: number
  result: string
  sessionId: string
  costUsd: number
  createdAt: string
  startedAt?: string
  finishedAt?: string
}

export interface RunEvent {
  seq: number
  kind: string
  payload: unknown
  createdAt: string
}

export class ApiError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: { 'Content-Type': 'application/json', 'X-Remedy-CSRF': '1' },
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: 'same-origin',
  })
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string }
    throw new ApiError(res.status, data.error ?? res.statusText)
  }
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

export const api = {
  login: (password: string) => request<void>('POST', '/api/login', { password }),
  logout: () => request<void>('POST', '/api/logout'),
  me: () => request<{ user: string }>('GET', '/api/me'),
  listRuns: () => request<Run[]>('GET', '/api/runs'),
  createRun: (prompt: string) => request<Run>('POST', '/api/runs', { prompt }),
  getRun: (id: string) => request<Run>('GET', `/api/runs/${id}`),
}

/** Opens the live stream of a run. The browser reconnects with Last-Event-ID on its own. */
export function streamRun(
  id: string,
  onEvent: (e: RunEvent) => void,
  onDone: (r: Run) => void,
): () => void {
  const es = new EventSource(`/api/runs/${id}/events`)
  es.addEventListener('run_event', (m) => onEvent(JSON.parse((m as MessageEvent<string>).data) as RunEvent))
  es.addEventListener('done', (m) => {
    onDone(JSON.parse((m as MessageEvent<string>).data) as Run)
    es.close()
  })
  return () => es.close()
}
```

- [ ] **Step 2: Write the login view**

Create `web/src/Login.tsx`:

```tsx
import { useState } from 'react'
import type { FormEvent } from 'react'
import { api, ApiError } from './api.ts'

export default function Login({ onLoggedIn }: { onLoggedIn: () => void }) {
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await api.login(password)
      onLoggedIn()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Login failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <main className="mx-auto flex min-h-screen max-w-sm flex-col justify-center gap-4 px-6">
      <h1 className="text-3xl font-semibold tracking-tight">Remedy</h1>
      <form onSubmit={submit} className="flex flex-col gap-3">
        <input
          type="password"
          autoFocus
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          placeholder="Admin password"
          className="rounded-lg border border-slate-700 bg-slate-900 px-3 py-2 outline-none focus:border-indigo-400"
        />
        <button
          disabled={busy || password === ''}
          className="rounded-lg bg-indigo-500 px-3 py-2 font-medium disabled:opacity-50"
        >
          Sign in
        </button>
        {error && <p className="text-sm text-rose-400">{error}</p>}
      </form>
    </main>
  )
}
```

- [ ] **Step 3: Write the runs page**

Create `web/src/RunsPage.tsx`:

```tsx
import { useCallback, useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { api, ApiError } from './api.ts'
import type { Run } from './api.ts'

export const statusColor: Record<Run['status'], string> = {
  queued: 'bg-slate-600',
  running: 'bg-amber-500',
  succeeded: 'bg-emerald-500',
  failed: 'bg-rose-500',
}

export default function RunsPage() {
  const [runs, setRuns] = useState<Run[]>([])
  const [prompt, setPrompt] = useState('')
  const [error, setError] = useState('')

  const refresh = useCallback(() => {
    api.listRuns().then(setRuns).catch((e: unknown) => setError(String(e)))
  }, [])

  useEffect(() => {
    refresh()
    const t = setInterval(refresh, 3000)
    return () => clearInterval(t)
  }, [refresh])

  async function submit(e: FormEvent) {
    e.preventDefault()
    setError('')
    try {
      const created = await api.createRun(prompt)
      setPrompt('')
      location.hash = `#/runs/${created.id}`
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not start the run')
    }
  }

  return (
    <div className="flex flex-col gap-8">
      <form onSubmit={submit} className="flex flex-col gap-3">
        <textarea
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
          rows={4}
          placeholder="What should the agent do? (read-only in phase 0)"
          className="rounded-lg border border-slate-700 bg-slate-900 px-3 py-2 outline-none focus:border-indigo-400"
        />
        <button
          disabled={prompt.trim() === ''}
          className="self-start rounded-lg bg-indigo-500 px-4 py-2 font-medium disabled:opacity-50"
        >
          Start run
        </button>
        {error && <p className="text-sm text-rose-400">{error}</p>}
      </form>

      <section className="flex flex-col gap-2">
        <h2 className="text-sm uppercase tracking-wide text-slate-400">Recent runs</h2>
        {runs.length === 0 && <p className="text-slate-500">No runs yet.</p>}
        {runs.map((r) => (
          <a
            key={r.id}
            href={`#/runs/${r.id}`}
            className="flex items-center gap-3 rounded-lg border border-slate-800 bg-slate-900/50 px-3 py-2 hover:border-slate-600"
          >
            <span className={`h-2.5 w-2.5 rounded-full ${statusColor[r.status]}`} />
            <span className="flex-1 truncate">{r.prompt}</span>
            <span className="text-xs text-slate-500">{new Date(r.createdAt).toLocaleString()}</span>
          </a>
        ))}
      </section>
    </div>
  )
}
```

- [ ] **Step 4: Write the live run view**

Create `web/src/RunView.tsx`:

```tsx
import { useEffect, useState } from 'react'
import { api, streamRun } from './api.ts'
import type { Run, RunEvent } from './api.ts'
import { statusColor } from './RunsPage.tsx'

function summarize(e: RunEvent): string {
  if (e.kind === 'result' && typeof e.payload === 'object' && e.payload !== null) {
    const result = (e.payload as { result?: unknown }).result
    if (typeof result === 'string') return result
  }
  if (typeof e.payload === 'string') return e.payload
  const text = JSON.stringify(e.payload)
  return text.length > 600 ? `${text.slice(0, 600)}...` : text
}

export default function RunView({ id }: { id: string }) {
  const [run, setRun] = useState<Run | null>(null)
  const [events, setEvents] = useState<RunEvent[]>([])

  useEffect(() => {
    setEvents([])
    api.getRun(id).then(setRun).catch(() => setRun(null))
    return streamRun(
      id,
      (e) => setEvents((prev) => (prev.some((p) => p.seq === e.seq) ? prev : [...prev, e])),
      setRun,
    )
  }, [id])

  return (
    <div className="flex flex-col gap-6">
      <a href="#/" className="text-sm text-slate-400 hover:text-slate-200">
        ← All runs
      </a>

      {run && (
        <header className="flex flex-col gap-2">
          <div className="flex items-center gap-3">
            <span className={`h-2.5 w-2.5 rounded-full ${statusColor[run.status]}`} />
            <span className="font-medium">{run.status}</span>
            {run.exitCode !== undefined && <span className="text-sm text-slate-400">exit {run.exitCode}</span>}
          </div>
          <p className="whitespace-pre-wrap rounded-lg bg-slate-900 p-3">{run.prompt}</p>
        </header>
      )}

      <ol className="flex flex-col gap-2 font-mono text-sm">
        {events.map((e) => (
          <li key={e.seq} className="rounded-lg border border-slate-800 bg-slate-900/50 p-2">
            <span className="mr-2 rounded bg-slate-700 px-1.5 py-0.5 text-xs">{e.kind}</span>
            <span className="whitespace-pre-wrap break-words text-slate-300">{summarize(e)}</span>
          </li>
        ))}
      </ol>

      {run?.status === 'running' && <p className="text-sm text-amber-400">Waiting for more output...</p>}
    </div>
  )
}
```

- [ ] **Step 5: Write the app shell with auth gate and hash routing**

Overwrite `web/src/App.tsx`:

```tsx
import { useEffect, useState } from 'react'
import { api } from './api.ts'
import Login from './Login.tsx'
import RunsPage from './RunsPage.tsx'
import RunView from './RunView.tsx'

function useHash(): string {
  const [hash, setHash] = useState(location.hash)
  useEffect(() => {
    const onChange = () => setHash(location.hash)
    window.addEventListener('hashchange', onChange)
    return () => window.removeEventListener('hashchange', onChange)
  }, [])
  return hash
}

export default function App() {
  const [auth, setAuth] = useState<'loading' | 'in' | 'out'>('loading')
  const hash = useHash()

  useEffect(() => {
    api.me().then(() => setAuth('in')).catch(() => setAuth('out'))
  }, [])

  if (auth === 'loading') return null
  if (auth === 'out') return <Login onLoggedIn={() => setAuth('in')} />

  const runId = /^#\/runs\/([\w-]+)$/.exec(hash)?.[1]

  async function logout() {
    await api.logout()
    setAuth('out')
  }

  return (
    <main className="mx-auto flex min-h-screen max-w-3xl flex-col gap-8 px-6 py-10">
      <div className="flex items-center justify-between">
        <a href="#/" className="text-2xl font-semibold tracking-tight">
          Remedy
        </a>
        <button onClick={logout} className="text-sm text-slate-400 hover:text-slate-200">
          Sign out
        </button>
      </div>
      {runId ? <RunView id={runId} /> : <RunsPage />}
    </main>
  )
}
```

- [ ] **Step 6: Typecheck, lint, build**

Run: `cd web && npm run lint && npm run build`
Expected: no lint findings, `tsc -b` succeeds, Vite build succeeds.

- [ ] **Step 7: Commit**

```bash
git add web
git commit -m "feat(web): add login, runs list and live run view" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Embed the UI, package the control plane, verify end to end

**Files:**
- Create: `web/embed.go`
- Create: `web/embed_stub.go`
- Modify: `cmd/remedy-server/main.go`
- Create: `Dockerfile`
- Modify: `README.md` (quick start)

**Interfaces:**
- Consumes: Tasks 6, 8 and 9.
- Produces: `web.FS() fs.FS` (nil without the `webui` tag); a single `remedy-server` binary that serves the UI; a control plane `Dockerfile`.

- [ ] **Step 1: Write the embed files**

Create `web/embed.go`:

```go
//go:build webui

// Package web exposes the built UI (web/dist) to the control plane binary.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the built UI rooted at index.html.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist is embedded at build time, so Sub cannot fail
	}
	return sub
}
```

Create `web/embed_stub.go`:

```go
//go:build !webui

// Package web exposes the built UI (web/dist) to the control plane binary.
package web

import "io/fs"

// FS returns nil unless the binary is built with -tags webui.
func FS() fs.FS { return nil }
```

- [ ] **Step 2: Serve the UI from the server main**

In `cmd/remedy-server/main.go`, add the import `"github.com/Jaydee94/remedy/web"` and add the field to the `server.Deps` literal:

```go
		Handler: server.New(server.Deps{
			Store:       st,
			Auth:        auth.New(cfg.AdminPassword),
			RunnerToken: cfg.RunnerToken,
			Web:         web.FS(),
		}),
```

- [ ] **Step 3: Write the Dockerfile**

Create `Dockerfile`:

```dockerfile
FROM node:lts-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -tags webui -o /out/remedy-server ./cmd/remedy-server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/remedy-server /remedy-server
ENV REMEDY_DB=/data/remedy.db
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/remedy-server"]
```

- [ ] **Step 4: Add the quick start to the README**

Append to `README.md`:

````markdown
## Quick start (phase 0, local)

Prerequisite: the `claude` CLI is installed and logged in with your subscription
(`claude`, then `/login`). Run `claude auth status` or `/status` to confirm.

```sh
make build
export REMEDY_ADMIN_PASSWORD='choose-a-long-password'
export REMEDY_RUNNER_TOKEN="$(openssl rand -hex 24)"

./bin/remedy-server &          # control plane on :8080, database in ./remedy.db
./bin/remedy-runner            # in a second terminal, with the same REMEDY_RUNNER_TOKEN
```

Open <http://localhost:8080>, sign in, start a run. The agent is read-only in this phase.
The runner strips `ANTHROPIC_API_KEY` from the CLI's environment on purpose, so runs are
always billed to the subscription login.
````

- [ ] **Step 5: Build and run the checks**

Run: `make check && make build && go vet -tags webui ./...`
Expected: all green; `bin/remedy-server` now has the UI embedded.

Optional, if Docker is available: `docker build -t remedy-server:dev .`
Expected: image builds.

- [ ] **Step 6: Manual end-to-end check with the real CLI**

Run (terminal 1):

```bash
export REMEDY_ADMIN_PASSWORD='choose-a-long-password'
export REMEDY_RUNNER_TOKEN="$(openssl rand -hex 24)"
./bin/remedy-server
```

Run (terminal 2, same `REMEDY_RUNNER_TOKEN`): `./bin/remedy-runner`

Then in a browser at <http://localhost:8080>:
1. A wrong password shows an error; after five wrong attempts further attempts are refused.
2. The correct password opens the runs page.
3. Start a run with the prompt `Reply with the single word: pong`.
4. The run view shows events appearing live (`system`, `assistant`, `result`), the status turns green, and the last event contains `pong`.
5. Reload the page mid-run in a second run: the backlog reappears and streaming continues.

Expected: all five pass. Record any deviation (especially unexpected event shapes) in
`docs/research/spike-claude-billing.md` under a new heading "Observed stream-json events",
because the UI renderer in phase 1 depends on the real shapes.

- [ ] **Step 7: Commit**

```bash
git add web/embed.go web/embed_stub.go cmd Dockerfile README.md
git commit -m "feat: embed UI in control plane and add Dockerfile" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

## Exit criteria for phase 0

- `make check` passes; CI is green on `main`.
- The spike has a recorded decision (`proceed`) in `docs/research/spike-claude-billing.md`.
- The manual end-to-end check in Task 10 passes against the real `claude` CLI.
- Known limitations are documented and carried into phase 1: no reaper for stale `running` runs, single run at a time, read-only agent, plain-styled UI, no Kubernetes manifests or runner image.

## Self-review notes

- **Spec coverage (design section 3, phase 0):** monorepo (done at bootstrap), SQLite (T2), control plane + runner (T6 to T8), auth (T5, T6), UI shell (T9), Claude adapter (T3), billing spike (T1), live streaming (T6, T9). Deviations from the design, both deliberate: shadcn/ui is deferred to phase 1; no Kubernetes manifests or runner image yet (phase 1 or a dedicated deployment task).
- **Types used across tasks:** `run.Outcome` (T2) is consumed by `provider.Final` mapping in `Execute` (T4), `store.FinishRun` (T2), the `finish` handler (T6) and `Client.Finish` (T7). `Sink` (T4) is implemented by `clientSink` (T7). `server.Deps` (T6) is constructed in T7 tests and T8/T10 mains.
- **Verified when written:** every Go and TypeScript block of Tasks 2 to 9 was extracted into a scratch copy of the repo. The Go code passes `gofmt`, `go vet` and `go test -race ./...`; the TypeScript passes `tsc -b`, `vite build` and `oxlint`. Not verified: Task 1 (needs the real subscription login), the `Dockerfile`, `go vet -tags webui` (needs a built `web/dist`), and the manual end-to-end check in Task 10. Still, treat the TDD "fails/passes" checkpoints as authoritative if anything drifted since.
