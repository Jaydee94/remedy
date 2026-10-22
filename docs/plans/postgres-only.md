# PostgreSQL as the only database: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The control plane, its tests, the images, the chart and the docs use PostgreSQL only, and the existing SQLite data is carried over by a one-time importer without changing an id.

**Architecture:** A thin wrapper around `database/sql` (pgx stdlib driver) rewrites `?` to `$n`, so the 366 existing placeholders stay; dialect differences (identity columns, `LastInsertId`, case-insensitive names, unique-violation detection, foreign-key-off migrations) are fixed where the tests show them. One Postgres baseline migration replaces the 18 SQLite migrations. Every transaction is audited for the guarantees SQLite's single connection gave for free, and each class gets a lock or a locking read. A separate command imports a SQLite file.

**Tech Stack:** Go 1.27 (`database/sql`, `github.com/jackc/pgx/v5/stdlib`), PostgreSQL 16 or newer, Helm, GitHub Actions, shell for `dev/pg`.

**Spec:** `docs/specs/2026-10-22-postgres-only-design.md` (read it first; the plan only decides what it leaves open and orders the work). The repository's `CLAUDE.md` binds every task.

## Global Constraints

- Everything in the repo is English. Go stdlib-first; the only new dependency is `github.com/jackc/pgx/v5` (spec decision 1); `modernc.org/sqlite` leaves the server's imports in task 8 and `go.mod` one release later.
- `internal/store` is the only package that issues SQL (the importer is the one exception: it reads SQLite and writes through the store's schema; it lives in `cmd/remedy-import-sqlite`).
- Column types do not change (spec decision 3): fixed-width `TEXT` timestamps (`tsLayout`), `INTEGER` 0/1 booleans, `TEXT` JSON.
- Sealed values are never touched; ids are never changed, reused or backfilled; `activity` order is kept.
- Activity is written in the same transaction as the state change; inside `Store.inTx` only the `tx` is used.
- The database URL is a secret: `secret.Value`, never in a log, an error, an answer, `values.yaml` or a database column.
- Tests use `REMEDY_TEST_DATABASE_URL` and a fresh schema per test; no test silently skips for lack of a database.
- A migration that changes data gets a test on a database built from the earlier schema.
- Conventional commits; the release is triggered by `feat`/`fix` on merge: the final merge title is `feat!: use PostgreSQL as the only database` (minor bump at 0.x; BREAKING CHANGE paragraph names the importer).
- Nothing outside the worktree changes without the maintainer's go: no push, no PR, no change to the home-server repository, no deletion of the old PVC.

## Review Focus

1. Two callers start an automatic diagnosis at once: the daily limit and "one run at a time" still hold (Task 6).
2. Two runners claim at once: one run is claimed once (Task 6).
3. The importer is run twice, on a non-empty database, on a SQLite file of another schema version, or on a file a live server still writes: it refuses, changes nothing, says why (Task 7).
4. After the import every sealed credential still opens and the next inserted row gets a fresh id above the old high-water mark, even for a deleted last row (Task 7).
5. The server starts before Postgres is ready (retry then clear error), loses the connection mid-run (requests fail, no process crash, recovery without restart) and has a password with URL-special characters (Tasks 2, 8).

---

### Task 0: Decide in the design document first

**Files:** Modify `docs/design.md` (the backend line "SQLite", the roadmap rows that say SQLite, the persistence bullet), `docs/specs/2026-10-22-postgres-only-design.md` (status line after the maintainer's go).

- [ ] **Step 1:** Replace the decision "Go backend, SQLite" by PostgreSQL with one paragraph of reasons (the spec's "Why (assumed)": mark the reasons as assumed until the maintainer states his own) and the explicit statement that this is not an HA change. Search `docs/design.md` for every "SQLite" and fix each (`grep -n -i sqlite docs/design.md`); leave `docs/plans/*` and `docs/research/*` alone (history).
- [ ] **Step 2:** Commit `docs: record PostgreSQL as the database decision`. Stop for the maintainer's confirmation of the spec's ten decisions before Task 1 starts coding (he may overrule any of them; this is the only checkpoint).

---

### Task 1: A Postgres for the tests

**Files:** Create `dev/pg/up.sh`, `dev/pg/down.sh`, `dev/pg/README.md`, `internal/testutil/pgtest/pgtest.go`, `internal/testutil/pgtest/pgtest_test.go`. Modify `Makefile` (`test`, new `pg-up`, `pg-down`), `.github/workflows/ci.yml` (job `go`: `services.postgres` image `postgres:16`, `REMEDY_TEST_DATABASE_URL` env, health options), `.gitignore` if the script writes an env file.

**Interfaces:**
- Produces: `pgtest.New(t testing.TB) string` returns a DSN whose connections have `search_path` set to a schema created for this test and dropped by `t.Cleanup`; it calls `t.Fatal` with instructions (`make pg-up`, then `. dev/pg/env.sh`) when `REMEDY_TEST_DATABASE_URL` is unset. `pgtest.Admin(t) *sql.DB` is the unscoped connection for tests that create their own databases.

- [ ] **Step 1: Write the failing test** `pgtest_test.go`: two `New` calls give different schemas that cannot see each other's tables; cleanup removes the schema; unset variable fails with the instruction text (use a subprocess or a stub `TB`).
- [ ] **Step 2:** Run `go test ./internal/testutil/pgtest` and see it fail (no package).
- [ ] **Step 3:** Implement `pgtest` (pgx stdlib; schema name `t_<random hex>`; the schema is created with the admin connection and the DSN returned carries `options=-c search_path=<schema>` or the driver's `search_path` runtime parameter). `dev/pg/up.sh`: start `postgres:16` on a random free port with a throwaway password, wait for `pg_isready`, write `dev/pg/env.sh` (git-ignored) exporting `REMEDY_TEST_DATABASE_URL`; `down.sh` removes the container. `make test` runs `up.sh` when the variable is unset and `docker` exists, and prints the instruction otherwise.
- [ ] **Step 4:** Run `make pg-up && . dev/pg/env.sh && go test ./internal/testutil/pgtest`. Expected: PASS. Add the CI service and push nothing.
- [ ] **Step 5: Commit** `test: add a Postgres harness for the Go tests`.

---

### Task 2: The database wrapper and connection handling

**Files:** Create `internal/store/db.go`, `internal/store/db_test.go`. Modify `go.mod`/`go.sum` (`go get github.com/jackc/pgx/v5`), `internal/store/store.go` (`Open`, `Close`, `inTx`).

**Interfaces:**
- Produces: `store.Open(ctx context.Context, url secret.Value, opts ...Option) (*Store, error)` with `WithMaxConns(n int)`; internal `type db interface { ExecContext; QueryContext; QueryRowContext }` implemented by `*pool` (wrapping `*sql.DB`) and `*txn` (wrapping `*sql.Tx`), both rewriting `?` to `$n` (cached per query string); `rebind(q string) string`; `isUnique(err error) bool` (SQLSTATE 23505, also reports the constraint name via `uniqueConstraint(err) string`), `isSerialization(err error) bool` (40001); `(*Store).inTx(ctx, fn func(tx *txn) error) error` with an `inTxIso(ctx, level sql.IsolationLevel, fn)` variant that retries on 40001 up to 5 times.

- [ ] **Step 1: Write the failing tests:** `rebind` turns `a = ? AND b IN (?, ?)` into `$1, $2, $3`; refuses (panics in tests / returns an error at first use) a statement with a `?` inside a single-quoted literal; `isUnique` on a real violation and `uniqueConstraint` returns the constraint name; `Open` with a wrong password returns an error that does not contain the password; `Open` against a closed port retries for the bounded time (use a short test option) then errors; a URL with `@`, `/`, `%` and `?` in the password (percent-encoded) connects; `WithMaxConns` sets the pool size.
- [ ] **Step 2:** Run `go test ./internal/store -run 'Rebind|Unique|Open'`. Expected: FAIL (undefined).
- [ ] **Step 3: Implement** as in Interfaces. `Open`: `pgx` config parsed from the URL (errors scrubbed of the URL), `sql.OpenDB(stdlib.GetConnector(...))`, `SetMaxOpenConns`, `SetConnMaxLifetime(30 * time.Minute)`, ping with 30 s bounded backoff (1 s to 5 s), then the migration runner of Task 3. `db.go` caches rebound strings in a `sync.Map`. Add a source scan test that fails if a Go SQL string in `internal/store` contains a quoted question mark.
- [ ] **Step 4:** Run the tests with the harness env. Expected: PASS for the new tests (the rest of the package may still fail to build until Task 4; keep the package compiling by leaving the old `Store.db` users on the wrapper type: the wrapper has the same method names as `*sql.DB`).
- [ ] **Step 5: Commit** `feat: open PostgreSQL through a wrapper that rewrites placeholders`.

---

### Task 3: The baseline schema and the migration runner

**Files:** Create `internal/store/migrations/001_baseline.sql`, `internal/store/schema_test.go`. Delete `internal/store/migrations/001_init.sql` to `018_incident_reopen.sql` (git history keeps them; Task 7 copies them into the importer's testdata first, so do Task 7's step 1 copy before this deletion or copy from git). Modify `internal/store/store.go` (`migrate`, `apply`; delete `foreignKeysOff`).

**Interfaces:**
- Produces: `(*Store).migrate(ctx)` takes `pg_advisory_lock(7265_6d65_6479)` (any fixed int64, named constant `migrateLockKey`) on a pinned connection, applies each file not in `schema_migrations` in its own transaction, releases the lock.

- [ ] **Step 1: Write the failing tests** `schema_test.go` against a fresh schema: every table of SQLite schema 018 exists (list them from the SQLite migration files: `runs`, `run_events`, `repos`, `incidents`, `activity`, `incident_notes`, `tool_calls`, `run_tokens`, `hosts`, `host_credentials`, `run_hosts`, `settings`, `skills`, `skill_evidence` or whatever the files define; derive the list by reading the 18 files, do not guess); the partial unique index `incidents_active_key` rejects a second unresolved row of one key and allows a resolved twin; `lower(full_name)` uniqueness for repos and `lower(name)` for hosts; each `CHECK` of the old files (state sets, source/repository rule, role sets) rejects a bad row; foreign keys cascade or restrict exactly as before (delete a host removes its credentials and skills; delete an incident removes notes: copy each `ON DELETE` from the old files); identity columns accept explicit ids and then continue above them after `setval`; running the runner twice applies nothing the second time; two goroutines starting at once apply each migration once (advisory lock).
- [ ] **Step 2:** Run `go test ./internal/store -run Schema`. Expected: FAIL.
- [ ] **Step 3: Implement** `001_baseline.sql` by translating the end state of the 18 files (not by replaying them): `BIGINT GENERATED BY DEFAULT AS IDENTITY`, `lower()` unique indexes instead of `COLLATE NOCASE`, same `CHECK`s, same partial indexes (`WHERE state <> 'resolved'` etc.), same defaults (`'{}'` for JSON text), all later `ALTER`ed columns in place, `resolved_while_ignored` included, no `sqlite_sequence`. Keep column order of the SQLite end state so the importer can copy by position-independent names anyway (it will select by name).
- [ ] **Step 4:** Run `go test ./internal/store -run Schema` Expected: PASS.
- [ ] **Step 5: Commit** `feat: describe the schema as one PostgreSQL baseline migration`.

---

### Task 4: Port the store

**Files:** Modify every non-test file in `internal/store/*.go` as the tests show; the known sites: `store.go` (`RETURNING`, `LastInsertId`), `incidents.go` (`OpenIncident` `LastInsertId`, unique detection by string, `UnignoreIncident`/`ReopenIncident` `CASE`, `incidentCols()` cutoff), `github.go` (`ON CONFLICT`, `COLLATE NOCASE` lookups), `hosts.go`, `skills*.go`, `toolcalls.go`, `settings.go`, `reap.go`, `runs_with.go`, `runtokens.go`, `learner.go`, `heartbeat.go`, `diagnosis.go`, `incident_runs.go`, `runs_counts.go`.

**Interfaces:** No exported signature changes except `Open` (Task 2). Behaviour is identical.

- [ ] **Step 1:** Make the existing store tests use the harness: change `openStore` in `internal/store/store_test.go` (and the helpers in `internal/reaper`, `internal/settings` tests) to `store.Open(ctx, secret.NewValue(pgtest.New(t)))`. Do not touch the other 70 callers yet (Task 5).
- [ ] **Step 2:** Run `go test ./internal/store` and list the failures by cause. Expected: FAIL in groups: `LastInsertId`, unique-violation strings, case-insensitive lookups, integer/text type strictness (a text parameter compared with an integer column), `RowsAffected` semantics on `UPDATE` without change, ordering that SQLite gave by rowid.
- [ ] **Step 3: Fix each group** where it occurs: (a) `LastInsertId` → `QueryRowContext(... RETURNING id).Scan(&id)` at the six sites; (b) `strings.Contains(err.Error(), "UNIQUE constraint failed")` at the five sites → `isUnique(err)` (and `uniqueConstraint(err)` when the code distinguishes which constraint); (c) NOCASE lookups → `lower(col) = lower(?)`; (d) any `ORDER BY` that relied on implicit rowid order gets an explicit `, id`; (e) SQLite's dynamic typing: pass `int`/`int64`/`bool→0/1` with the Go type the column has (`nullInt`, `boolInt` helpers already exist or are added); (f) `PRAGMA` and the foreign-keys-off branch are gone with Task 3. Fix only what a failing test shows or what the audit in Step 4 names; no style changes.
- [ ] **Step 4:** Grep for the dialect constructs left: `grep -n "strftime\|datetime(\|INSERT OR\|COLLATE NOCASE\|AUTOINCREMENT\|LastInsertId\|sqlite" internal/store/*.go` (non-test) must be empty.
- [ ] **Step 5:** Run `go test ./internal/store` and `go vet ./...`. Expected: PASS for the store package. Commit `feat: port the store to PostgreSQL`.

---

### Task 5: Move all tests to Postgres and retire the SQLite-era migration tests

**Files:** Modify the roughly 76 call sites of `store.Open(...)` in tests (`grep -rn "store.Open(" --include=_test.go`): packages `app`, `gatekeeper`, `incident`, `responder`, `server`, `settings`, `reaper`, `store`, `learner`, `signals`, `skill`, `poller`, `hostexec`, `sshhost` as they occur. Delete `internal/store/migrate00*_test.go`, `migrate01*_test.go` (SQLite-era data migration tests; their logic is covered by Task 7's import tests). Create `internal/testutil/store.go` (`testutil.Store(t) *store.Store`) if it removes the repeated three lines.

- [ ] **Step 1:** Replace each `store.Open(filepath.Join(t.TempDir(), "x.db"))` with the helper; tests that reopen the same file to prove persistence (`secret_test.go:35,115`, `outage_start_test.go openStoreAt`) get a variant `testutil.StoreAt(t, dsn)` that opens a second `Store` on the same schema.
- [ ] **Step 2:** Run `go test ./... 2>&1 | tail -50` with the harness env. Expected: compile errors first, then failures grouped by cause; fix test-only issues (time sources, ordering, hard-coded `?`-style SQL in tests that seed with `db.Exec`: those tests use `database/sql` directly on SQLite; give them the DSN through the helper and write `$1` placeholders or use the wrapper's exported test seam `store.RawDB()` limited to `_test` via an `export_test.go`).
- [ ] **Step 3:** Run `go test ./... -race -cpu 1` over `./internal/store ./internal/gatekeeper ./internal/incident ./internal/responder ./internal/app`. Expected: PASS. Runtime of `make test` must stay under twice the SQLite time; if not, add `t.Parallel()` safely where schemas are isolated and note the numbers in the report.
- [ ] **Step 4:** `grep -rln "modernc.org/sqlite\|sqlite3\|\.db\"" --include='*_test.go' .` shows nothing outside the importer's directory. Commit `test: run every Go test against PostgreSQL`.

---

### Task 6: Concurrency audit and hardening

**Files:** Create `internal/store/concurrency_test.go` and `docs/research/postgres-concurrency-audit.md`. Modify the store files named by the audit (candidates: `store.go ClaimNext`, `diagnosis.go StartDiagnosis`, `toolcalls.go BeginExecution/ExpireCall/Decide`, `incidents.go ReopenIncident/OpenIncident/ResolveIncident`, `hosts.go`, `skills*.go`, `runtokens.go MintRunToken`, `reap.go`).

**Interfaces:** Produces `(*Store).lockDiagnosis(tx *txn) error` (`pg_advisory_xact_lock(diagnosisLockKey)`); `ClaimNext` selects with `FOR UPDATE SKIP LOCKED`.

- [ ] **Step 1: Audit.** For each of the 43 `inTx` sites and each multi-statement function without a transaction, write one row in the report: function, statements, what it assumes (single writer, row unchanged between read and write), class (single-statement CAS / claim / limit / row read-modify-write / append-only), mechanism chosen, test name. The report is the deliverable of this step; commit it before changing code.
- [ ] **Step 2: Write the failing tests** (they must fail against the unhardened port, so run them on Task 4's tree first and record the output; if one cannot fail, say why in the report): 20 goroutines call `StartDiagnosis` for different incidents with a daily limit of 5: exactly 5 start; 20 call it for one incident with a cap of 3: exactly 3; 10 runners call `ClaimNext` with 3 queued runs: exactly 3 distinct runs claimed, none twice; 10 goroutines call `BeginExecution` on one approved call: exactly one wins; `ExpireCall` and `Decide` race 200 times on fresh calls: exactly one outcome each time and the decided call never expires; two `ReopenIncident` of one resolved incident: one wins, the other `ErrNotFound`; two `OpenIncident` for one key: one `ErrExists`; concurrent `ResolveIncident` and `IgnoreIncident`: a consistent final state.
- [ ] **Step 3:** Run `go test ./internal/store -run Concurrency -race -count=5`. Expected: FAIL on the limit and claim tests before the fix.
- [ ] **Step 4: Implement** the mechanisms from the report (limits: advisory lock first statement of the transaction; claim: `FOR UPDATE SKIP LOCKED`; row read-modify-write: `SELECT ... FOR UPDATE`; the rest are single guarded statements and need no change). Keep the "only the tx inside inTx" rule; add a lint-style test that greps for `s.db.` inside functions that call `inTx` is optional.
- [ ] **Step 5:** Run the same command, `-count=20`, then `make check`. Expected: PASS. Commit `fix: keep the store's guarantees under a pool of connections`.

---

### Task 7: The SQLite importer

**Files:** Create `cmd/remedy-import-sqlite/main.go`, `internal/importsqlite/import.go`, `internal/importsqlite/import_test.go`, `internal/importsqlite/testdata/sqlite-migrations/*.sql` (copy of the 18 old files, taken from git before Task 3 deletes them), `docs/runbook/postgres-cutover.md` (Task 10 finishes it).

**Interfaces:**
- Produces: `importsqlite.Run(ctx, src string, dst *store.Store, opts Options) (Report, error)`; `Report{Tables []TableReport{Name string; Rows int}}`; the command `remedy-import-sqlite --from <file> [--dry-run]` reading `REMEDY_DATABASE_URL`, printing the report. Errors are sentinel: `ErrDestinationNotEmpty`, `ErrSchemaVersion{Have, Want string}`, `ErrSourceBusy`.

- [ ] **Step 1: Write the failing tests.** A helper builds a SQLite file by running the testdata migrations and then inserts a row in every table (including a host with a sealed credential produced with `secret.Seal` and a master key, a deleted incident to create a gap in ids, an `ignored`-resolved incident, `activity` rows with nine-digit timestamps). Tests: import into an empty store succeeds and every table's row count and id set match; the sealed host credential opens with the same key after the import; the identity sequences continue above the old maximum **and above the high-water mark of a deleted last row** (`sqlite_sequence`); a second import into the now non-empty store returns `ErrDestinationNotEmpty` and changes nothing; a source at another `schema_migrations` set returns `ErrSchemaVersion` naming both; a source with a WAL file whose writer is alive (open a writing connection) returns `ErrSourceBusy` or reads a consistent snapshot (decide and pin: read-only with `immutable=0` and `PRAGMA query_only`, and refuse when `-wal` has unchecked content older than a threshold is NOT required; pin the behaviour you implement); a failure in the middle (inject a bad row via a CHECK violation in the destination) rolls everything back; `--dry-run` writes nothing.
- [ ] **Step 2:** Run `go test ./internal/importsqlite`. Expected: FAIL.
- [ ] **Step 3: Implement.** Open the source with `modernc.org/sqlite` read-only (`mode=ro`); verify `schema_migrations` equals the 18 names exactly; in one destination transaction (`SET CONSTRAINTS ALL DEFERRED` is not needed if tables are copied in dependency order: parents first; derive the order from the foreign keys), insert rows by column name with `INSERT ... OVERRIDING SYSTEM VALUE`, then `setval` each identity sequence to `max(max(id), sqlite_sequence.seq)`; compare counts; commit. Copy ciphertext columns as bytes/text exactly (no decode). Add the command and wire `make build`.
- [ ] **Step 4:** Run `go test ./internal/importsqlite -race`. Expected: PASS. Run once against a copy of the maintainer's real file only with his go (Task 11).
- [ ] **Step 5: Commit** `feat: add a one-time importer from SQLite to PostgreSQL`.

---

### Task 8: The server, config and image

**Files:** Modify `internal/config/config.go` and tests (`DatabaseURL secret.Value` from `REMEDY_DATABASE_URL`, required; `DatabaseMaxConns` 2 to 100 default 10; delete `DBPath`/`REMEDY_DB`), `cmd/remedy-server/main.go` (open with the context, log the host and database name only, never the URL), `Dockerfile` (no `/data`, no `VOLUME`, no `REMEDY_DB`), `go.mod` (the server no longer imports `modernc.org/sqlite`; it stays for the importer), `README.md` quick start, `Makefile` (`dev-server` needs the variable; add `make dev-pg` helper), `.dockerignore` if needed.

- [ ] **Step 1: Failing tests** in `config`: missing URL → error naming `REMEDY_DATABASE_URL` and containing no secret; a malformed URL → error without the URL text; `REMEDY_DATABASE_MAX_CONNS=1` and `101` refused; the URL is never in `fmt.Sprintf("%v", cfg)` (`secret.Value` form); `REMEDY_DB` set → a clear error "REMEDY_DB is gone; see docs/runbook/postgres-cutover.md" (fail loudly rather than ignore).
- [ ] **Step 2:** Run `go test ./internal/config ./cmd/...`. Expected: FAIL.
- [ ] **Step 3:** Implement. `app.New` and the tests that build `config.Server` get the DSN from the harness. A startup log line says `database` = `host:port/dbname` only.
- [ ] **Step 4:** Run `make check` (with `REMEDY_TEST_DATABASE_URL`) and `docker build -t remedy-server:pgtest .`; run the image against the harness database and curl `/healthz`. Expected: PASS and 200.
- [ ] **Step 5: Commit** `feat!: configure the database by REMEDY_DATABASE_URL and drop SQLite from the server`.

---

### Task 9: Chart, kind testbed and CI

**Files:** Modify `deploy/chart/values.yaml`, `templates/server-deployment.yaml` (env `REMEDY_DATABASE_URL` from `secretKeyRef`, remove `REMEDY_DB`, the `data` volume and the PVC mount), delete `templates/server-pvc.yaml` and `server.persistence.*` values, `templates/validate.yaml` (refuse `server.env.REMEDY_DATABASE_URL`/`REMEDY_DB`, require `database.existingSecret` and `database.secretKey` default `url`; refuse a `database.url` value), `templates/networkpolicy.yaml` (`networkPolicy.database`: either `namespace`+`podSelector`+`port` like Alertmanager, or `cidr`+`port` for an external server; one of the two required when policies are on; port default 5432), `deploy/chart_test.go` and `deploy/*_test.go` (the cases that assert `REMEDY_DB`, `/data`, the PVC, and new ones for the refusals above), `deploy/chart/README.md`, `dev/kind/up.sh`/`dummy-up.sh`/`dummy-values.yaml`/`down.sh`/`dummy-storage.yaml` (a small single-pod Postgres manifest `dev/kind/postgres.yaml` plus its Secret, the chart's `database.existingSecret` pointing at it), `.github/workflows/ci.yml` (the `go` job service from Task 1; the chart job unchanged), `.github/workflows/release.yml` if the image smoke needs a database.

- [ ] **Step 1: Failing render tests** (model: the Alertmanager egress tests): the deployment has the env from the secret and no `REMEDY_DB` and no PVC; `database.existingSecret` missing fails with a message naming it; `networkPolicy.database` renders one rule per the chosen form and fails for neither/both, an empty selector, a bad CIDR (`0.0.0.0/0` refused: it would open everything), a bad port; a secret value in `server.env` is refused.
- [ ] **Step 2:** Run `make chart-check`. Expected: FAIL.
- [ ] **Step 3:** Implement chart and testbed changes. `make dummy-up` must work with the in-cluster Postgres (the real agent, so it costs quota; run `make dummy-smoke` only with the maintainer's go and record it in `docs/research/`; without it say "not run").
- [ ] **Step 4:** Run `make chart-check` and `make check`. Expected: PASS.
- [ ] **Step 5: Commit** `feat!: give the chart a database connection instead of a volume`.

---

### Task 10: Docs, runbooks and the repository's own instructions

**Files:** Create `docs/runbook/postgres-cutover.md` (prerequisites, backup of the old PVC, scale to zero, importer run with the real report, deploy, checks on the Setup page and the activity feed, rollback and its limit, how to take a `pg_dump` backup and restore, TLS via `sslmode`/`sslrootcert`, pool sizing, what a Postgres restart looks like in the logs). Modify `README.md`, `CLAUDE.md` (Commands: `REMEDY_DATABASE_URL`, the `sqlite3` seeding paragraph becomes `psql` with the same timestamp rule, the "single connection" statements in Architecture ("Persistence"), the `inTx` deadlock note, the Playwright seeding hints, the test requirement `REMEDY_TEST_DATABASE_URL`/`make pg-up`), `docs/runbook/*.md` that mention `sqlite3` or `/data/remedy.db` (`grep -rn "sqlite\|remedy.db" docs/runbook README.md CLAUDE.md dev`), the spec's status line ("implemented; cutover on the home server not made").

- [ ] **Step 1:** `grep -rIn -i "sqlite\|remedy\.db\|REMEDY_DB\b" --exclude-dir=docs/plans --exclude-dir=docs/research --exclude=CHANGELOG.md --exclude-dir=node_modules --exclude-dir=.git .` lists every remaining mention; each is fixed or deliberately kept (the importer, history) and listed in the report.
- [ ] **Step 2:** Walk the runbook against the kind testbed: import a fixture SQLite file with the real command into the kind Postgres, start the chart, open the Setup page. Record deviations and fix the runbook.
- [ ] **Step 3:** Run `make check`. Commit `docs: describe PostgreSQL, the cutover and the new test setup`.

---

### Task 11: Cutover on the home server (maintainer's go, separate steps)

Not an implementation task for an agent without the maintainer in the loop.

- [ ] **Step 1:** The maintainer picks the Postgres (spec "Rollout" step 2); a pull request in the home-server repository adds it and, after the release, the wrapper chart values (`database.existingSecret`, `networkPolicy.database`). Ask before touching that repository.
- [ ] **Step 2:** With his go: scale the control plane to zero, copy `/data/remedy.db`, run the importer on the copy, read the report with him, deploy, check Setup, the activity feed, an approval and a diagnosis (a real run costs quota: his decision), record the result in `docs/research/postgres-cutover-real-run.md`.
- [ ] **Step 3:** One release later: remove `cmd/remedy-import-sqlite`, `internal/importsqlite`, the `modernc.org/sqlite` dependency and the cutover runbook's importer section (`chore: remove the SQLite importer`).

---

## Self-review

- **Spec coverage:** decisions 1 to 10 map to tasks 2 (1, 2, 7), 3 (4, 5, 6), 4 (5), 5 (8), 6 (concurrency), 7 (9, 10), 8 (7), 9 (7, chart), 10 (docs), 11 (rollout); non-goals hold (no HA, no ORM, no type change).
- **Consistency:** `store.Open(ctx, secret.Value, ...Option)` (Task 2) is what Tasks 5, 7 and 8 call; `isUnique` and `inTxIso` (Task 2) are used in Tasks 4 and 6; the importer's testdata copy (Task 7 step 1) precedes the deletion in Task 3: **order the commits so the copy exists first** (do Task 7's copy as part of Task 3's step 3, in the same commit that deletes the files).
- **Order and parallelism:** 0, then 1, 2, 3 in sequence; 4 needs 1 to 3; 5 and 6 need 4; 7 needs 3 (and its own copy); 9 needs only the config names of 8 (can start after Task 2); 10 last before 11. After Task 3, Tasks 7 and 9 can run beside Task 4 in separate worktrees.
- **Calls made in this plan (not in the spec):** PostgreSQL 16 or newer as the tested floor; the advisory lock key as a named constant; five retries on 40001 where `SERIALIZABLE` is used; `FOR UPDATE SKIP LOCKED` for the claim; a source-scan test for quoted question marks; the importer refuses to run against a file at another schema version rather than converting; `REMEDY_DB` set is a hard error, not ignored; the final merge title is `feat!:`.
- **Size warning:** this is the largest change since phase 0 (366 placeholders ride unchanged on the wrapper, but 43 transactions need judgment and about 76 test call sites change). Task 4 and Task 6 carry the risk; do not merge before Task 6's tests pass with `-race -count=20`.
