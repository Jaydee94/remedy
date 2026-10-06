# Diagnosed-at Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Today's digest honest about diagnoses. It counts "incidents diagnosed in the last 24 hours" from `lastDiagnosisAt`, the time a diagnosis STARTED. A re-diagnosis that fails keeps the old stored diagnosis and gets a recent `last_diagnosis_at`, so the old diagnosis is counted as new for up to 24 hours (a known limitation recorded in PR #115). A new incident field `diagnosedAt` (when the stored diagnosis was written) removes it.

**Architecture:** One migration adds `incidents.diagnosed_at`; `CompleteDiagnosis` sets it in the transaction that stores the diagnosis; the store's `Incident` and the admin API carry it; the digest and "the incident diagnosed last" of Today use it instead of `lastDiagnosisAt`.

**Tech Stack:** Go 1.27 (stdlib, SQLite via `modernc.org/sqlite`), React 19, TypeScript 7, `node --test`.

**Sources:** the "Notes for review" of PR #115 (digest limitation), the memory note `project_ui-conversation-redesign.md` (open items). Spec: `docs/specs/2026-10-05-ui-conversation-redesign-design.md`, section 5.1 (the digest) and `docs/specs/2026-10-02-phase-1-detect-and-diagnose-design.md` (incident fields), `CLAUDE.md` (persistence and incidents).

## Global Constraints

- Everything in the repo is English: code, comments, docs, UI copy, commit messages.
- Go: stdlib-first; the store has ONE connection (`SetMaxOpenConns(1)`): inside `Store.inTx` use only the `tx`. Every state change writes its `activity` entry in the same transaction (unchanged here). Test first for logic with behaviour.
- **A migration that changes existing data gets a test on a database built from the earlier migration files, with a row in every table that points at the changed one** (`internal/store/migrate008_test.go` is the model: it builds an older database from `migrations/*.sql`). This migration adds a column and backfills it, so the test builds a database as migration 008 left it, with incidents (with and without a stored diagnosis) and rows in the tables that point at `incidents` (`activity`, `runs`, `tool_calls`, `incident_notes`: check the real names in the migrations), runs the migrations, and checks the backfill and that nothing was lost.
- Timestamps are stored the way the existing code does (`formatTS`/nine fractional digits); the API sends RFC 3339 like `lastDiagnosisAt` does.
- `web/tsconfig.app.json` sets `erasableSyntaxOnly` and `verbatimModuleSyntax`. Pure logic modules import only relatively with `.ts` extensions. `npm run lint --prefix web` prints no warnings.
- Commits end with the line `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>` (exactly this model name).
- In a worktree-isolated session the Bash tool refuses commands that name git in a compound or variable form: plain separate commands or a script file with literal paths; npm with `--prefix web`. Use `command ls`.
- Work on a branch `feat/ui-8-diagnosed-at`, never on `main`.

## Review Focus

1. The backfill: an incident that has a stored diagnosis gets `diagnosed_at = last_diagnosis_at` (the best value the old data has; for an incident that was diagnosed once it is the start of that run, the same value the digest used before); an incident without a diagnosis stays NULL; running the migration twice is impossible (it is applied once) and a database with no incidents migrates. (Task 1 test.)
2. `CompleteDiagnosis` sets `diagnosed_at` to the time the diagnosis was stored, in the same statement that stores it; `FailDiagnosis` and `StartDiagnosis` never touch it, so a failed re-diagnosis keeps the old `diagnosed_at`. (Task 1 tests.)
3. The API omits `diagnosedAt` for an incident without a diagnosis (like `lastDiagnosisAt` for an incident never diagnosed); the Incident list endpoints and the single-incident endpoint both carry it. (Task 1 test.)
4. The digest counts an incident as diagnosed in the last 24 hours only if its stored diagnosis was written in that window, whether or not a new diagnosis is running; a failed re-diagnosis changes nothing in the digest; "the incident whose diagnosis was written last" uses the same field. (Task 2 tests.)
5. Nothing else that reads `lastDiagnosisAt` changes meaning: the cooldown (`StartDiagnosis`), the preview of a failed diagnosis ("open with `lastDiagnosisAt` and no diagnosis"), the poller and responder. (Tasks 1, 2: grep.)

---

## File Structure

| File | Change | Responsibility |
|---|---|---|
| `internal/store/migrations/009_diagnosed_at.sql` | create | the column and its backfill |
| `internal/store/diagnosis.go`, `incidents.go` | modify | `CompleteDiagnosis` sets it; `Incident.DiagnosedAt` is selected and scanned |
| `internal/store/*_test.go` | modify/create | the tests of Review Focus 1 to 3 |
| `internal/server/incidents.go` + test | modify | `diagnosedAt` in the JSON |
| `web/src/api.ts`, `web/src/conversation.ts` + test | modify | the type; `digestText` and `lastDiagnosed` |
| `docs/specs/2026-10-05-ui-conversation-redesign-design.md`, `docs/specs/2026-10-02-phase-1-detect-and-diagnose-design.md` | modify | the digest definition; the incident field (only where they describe these) |

---

### Task 1: The backend (migration, store, API)

**Files:**
- Create: `internal/store/migrations/009_diagnosed_at.sql`
- Modify: `internal/store/diagnosis.go`, `internal/store/incidents.go`, `internal/server/incidents.go`, and their tests (create `internal/store/migrate009_test.go`)

**Interfaces:**
- Produces: `store.Incident.DiagnosedAt *time.Time`; the JSON field `diagnosedAt` (RFC 3339, omitted when absent).

- [ ] **Step 1: Read first.** `internal/store/migrations/*.sql` (how migrations are written and recorded; the header line `-- remedy:foreign-keys-off` is only for a rebuilt table: this one is `ALTER TABLE ... ADD COLUMN` and needs none), `internal/store/migrate008_test.go` (the model of the migration test and its helper that builds an older database from `migrations/*.sql`), `internal/store/diagnosis.go` (`StartDiagnosis`, `CompleteDiagnosis`, `FailDiagnosis`), `internal/store/incidents.go` (the `Incident` struct, its `SELECT` list and `scan`, `formatTS`/`parseTS` helpers), `internal/server/incidents.go` (the view and its tests).

- [ ] **Step 2: Tests first.**
  1. `internal/store/migrate009_test.go` (modelled on `migrate008_test.go`): build a database as 008 left it (apply the migration files below `009` from disk, record them, insert raw rows), with: an incident with a stored diagnosis and a `last_diagnosis_at`; an incident with a diagnosis written by a diagnosis that was later re-run (just the same row: a diagnosis and a newer `last_diagnosis_at`: the backfill takes `last_diagnosis_at`, document that this is the best the old data has); an incident without a diagnosis; a resolved incident with a diagnosis; and a row in each table that points at `incidents` (as the 008 test does). Open it with the normal store (which migrates), then check: `diagnosed_at` equals `last_diagnosis_at` for the incidents with a diagnosis and is NULL for the others; every pointing row is still there; the `incidents` row count is unchanged.
  2. A store test (next to the existing diagnosis tests): after `CompleteDiagnosis` the incident has `DiagnosedAt` set to about now (use the store's clock the way the existing tests control time, or compare with a window); after a second `StartDiagnosis` the incident is `diagnosing` with the OLD `DiagnosedAt` unchanged and a newer `LastDiagnosisAt`; after `FailDiagnosis` it is `diagnosed` again with the old `DiagnosedAt`; after a second successful `CompleteDiagnosis` `DiagnosedAt` moves forward; an incident that was never diagnosed has `DiagnosedAt == nil`.
  3. A server test: the incident JSON (list and single) has `diagnosedAt` for a diagnosed incident and no such key for an undiagnosed one.
  Run them: `go test ./internal/store ./internal/server` fails.

- [ ] **Step 3: The migration** `internal/store/migrations/009_diagnosed_at.sql`:

```sql
-- When the stored diagnosis was written. A new diagnosis moves it; a diagnosis that was started and failed does not (it moves last_diagnosis_at only).
ALTER TABLE incidents ADD COLUMN diagnosed_at TEXT;
-- The best value the old data has: the start of the last diagnosis, which is what Today's digest used.
UPDATE incidents SET diagnosed_at = last_diagnosis_at WHERE diagnosis IS NOT NULL;
```

(Match the style and naming of the existing migration files, and check how the incident `INSERT`s and `SELECT`s are spelled so that the new column is added where columns are listed explicitly.)

- [ ] **Step 4: The store.** `CompleteDiagnosis` sets `diagnosed_at` in the same `UPDATE` that stores the diagnosis, to the time of the write (use the store's way of getting the current time and `formatTS`; do not add a second statement). `Incident` gets `DiagnosedAt *time.Time`, selected and scanned next to `LastDiagnosisAt` (nullable). Nothing else in the store changes.

- [ ] **Step 5: The API.** `internal/server/incidents.go`: the view gets `DiagnosedAt *time.Time \`json:"diagnosedAt,omitempty"\`` filled from the store incident, next to `LastDiagnosisAt`.

- [ ] **Step 6: Run everything.** `go vet ./...`, `go test ./...` and `go test ./... -race` (CI runs `-race`; some ordering bugs only show with `-race -cpu 1`: run `go test ./internal/store ./internal/server -race -cpu 1` too). Expected: PASS.

- [ ] **Step 7: Commit**

```sh
git add internal
git commit -m "feat(store): record when the stored diagnosis was written" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: The digest and "diagnosed last" (UI)

**Files:**
- Modify: `web/src/api.ts`, `web/src/conversation.ts`, `web/src/conversation.test.ts`, the two spec documents named above

**Interfaces:**
- Consumes: the JSON field `diagnosedAt` (Task 1).
- Produces: `Incident.diagnosedAt?: string`; `digestText` and `lastDiagnosed` that use it.

- [ ] **Step 1: Tests first** (`conversation.test.ts`, with the existing fixtures): an incident with a diagnosis whose `diagnosedAt` is within 24 hours is counted as diagnosed whatever its `lastDiagnosisAt` and whether it is `diagnosing` again (the old guard `state !== 'diagnosing'` goes away: a running re-diagnosis does not make the OLD diagnosis any less recent); an incident whose `diagnosedAt` is older than 24 hours is not counted even if `lastDiagnosisAt` is recent (the failed re-diagnosis case: this is the bug); an incident with a diagnosis but no `diagnosedAt` (a server that does not send it) is not counted; `lastDiagnosed` returns the incident with the newest `diagnosedAt` and ignores incidents without one; the existing digest sentences stay as they are.

- [ ] **Step 2: Implement.** `api.ts`: `Incident.diagnosedAt?: string` with a doc comment ("when the stored diagnosis was written; a new diagnosis moves it, a failed one does not"). `conversation.ts`: `digestText` counts `i.diagnosis !== undefined && i.diagnosedAt !== undefined && Date.parse(i.diagnosedAt) >= since`; `lastDiagnosed` compares `diagnosedAt`; update both doc comments ("by the time it was written") and remove the "Known limitation" comment.

- [ ] **Step 3: The documents.** In `docs/specs/2026-10-05-ui-conversation-redesign-design.md` section 5.1 the digest's "incidents diagnosed (... by the time it was started)" now says "by the time the diagnosis was written (`diagnosedAt`)"; in `docs/specs/2026-10-02-phase-1-detect-and-diagnose-design.md` add `diagnosedAt` where the incident's fields (`lastDiagnosisAt` or the stored diagnosis) are described (read it first; change only that sentence; if the document describes no field list, leave it and say so). Report what you changed.

- [ ] **Step 4: Run** `npm test --prefix web`, `npm run lint --prefix web`, `npm run build --prefix web`, and `make check`. Expected: PASS.

- [ ] **Step 5: Commit**

```sh
git add web/src docs/specs
git commit -m "feat(web): the digest counts diagnoses by when they were written" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Verification (run by the controller)

No code changes unless a defect is found. Nothing here is committed.

- [ ] **Step 1: Full checks.** `make check`, `go test ./... -race`, `go test -tags webui ./web/` after `make web-build`.
- [ ] **Step 2: Run the app** with a throwaway database (the scripts of the earlier parts) that has: a diagnosed incident with `diagnosed_at` 2 hours ago; one whose `diagnosed_at` is 30 hours ago but `last_diagnosis_at` is 10 minutes ago (a failed re-diagnosis); a freshly diagnosed one.
- [ ] **Step 3: Check.** Today's digest counts the first and the third and not the second ("diagnosed 2 ..."); the API answers `diagnosedAt` (curl with a session); the "Read the diagnosis" link on Today goes to the incident with the newest `diagnosedAt`; an old database (one created before the migration, then opened by the new server) migrates and shows the same digest as before for its diagnosed incidents.
- [ ] **Step 4: Clean up** processes, database, `.playwright-mcp/`, stray `*.png`.

---

## Self-Review

**Coverage:** the digest limitation of PR #115 (Tasks 1, 2). Not covered: the payload of `listIncidentRuns` and `runSteps` (no decision/evidence), the a11y leftovers of PR #120 (small, listed there).

**Placeholder scan:** none; the Go steps name the files to read first because the exact scan/select code depends on what is there.

**Type consistency:** `store.Incident.DiagnosedAt` (Task 1) is what the server view reads; the JSON `diagnosedAt` is what `Incident.diagnosedAt` (Task 2) reads.

**Review Focus coverage:** 1, 2, 3 (Task 1 tests), 4 (Task 2 tests; Task 3), 5 (greps in Tasks 1 and 2).
