# Concurrency audit of the store on PostgreSQL

Plan task 6 of `docs/plans/postgres-only.md`, spec `docs/specs/2026-10-22-postgres-only-design.md` section "Concurrency".
Date: 2026-10-22. Tree audited: the port of task 4 (`9b9e254`).

SQLite with one connection ran every transaction of the store alone. PostgreSQL runs them side by side at `READ COMMITTED`:
every statement sees the rows committed when it starts, an `UPDATE` or `DELETE` that meets a row another transaction is
changing waits for it and then re-checks its `WHERE` on the newest version of that row (and recomputes its `SET` from it), and
an `INSERT` into a unique index waits for a concurrent insert of the same key and then fails with `23505`. This audit lists
every transaction of `internal/store` and every function that issues more than one statement, says what each one relied on,
and names the mechanism that keeps it true under a pool. The tests are in `internal/store/concurrency_test.go`; all their
names start with `TestConcurrency`.

## Classes and mechanisms

| Class | Meaning | Mechanism |
|---|---|---|
| CAS | one guarded statement (`UPDATE ... WHERE state = ...`), or a read whose result the guarded write re-checks | nothing: PostgreSQL re-checks the guard on the newest row version; a test with parallel callers proves it |
| claim | take the next queued row | `FOR NO KEY UPDATE SKIP LOCKED` in the subquery, plus the guard on the outer `UPDATE` |
| limit | count, compare, write (a daily limit, a cap, one run at a time, a scope or pending limit) | a transaction-level advisory lock on a fixed key, taken as the first statement of the transaction |
| RMW | read a row, decide in Go, write it | `SELECT ... FOR NO KEY UPDATE` of the row first (`FOR UPDATE` when the row is then deleted, `FOR SHARE` for a row only read, `FOR KEY SHARE` for a row that must only keep existing), or a guard on the write that repeats the read's condition |
| unique | check that a key is free, then insert | the unique index decides; `23505` becomes the function's answer (`ErrExists`, or the existing row) |
| ordered feed | rows a reader follows by an increasing id or seq | ids handed out in commit order (see "The ordered feeds") |
| cascade | a delete whose foreign key actions lock child rows after the parent | bounded lock waits and a retry (`inDeleteTx`, see "Deletes that cascade") |
| append-only | an insert that depends on no read | nothing |
| benign | a race whose worst outcome is harmless, explained in the row | nothing |

`FOR NO KEY UPDATE` is used rather than `FOR UPDATE` wherever a row lock is taken for an update: it conflicts with every other
writer of the row, but not with the `FOR KEY SHARE` lock a foreign key check takes, so inserts of child rows (events, activity,
tool calls) do not wait for it.

Advisory keys (one-key `bigint` form, `pg_advisory_xact_lock`, released at commit or rollback; the two-key form with class
`0x726d6479` stays the migration runner's): `activityLockKey`, `runStartLockKey` (taken by `lockDiagnosis(ctx, tx)`) and
`skillsLockKey` (`lockSkills`), named constants in `internal/store/locks.go`, each `0x726d6479 << 32 | n`. Order: the run-start
lock, then the skills lock, each before any row lock of its transaction; the activity lock last. Advisory locks belong to the
database, not the schema, so tests on separate schemas of the shared test server wait for each other on these keys. That only
serialises them, and each lock is held for one short transaction.

## The ordered feeds

The activity stream (`internal/server/timeline.go`, `streamActivity`) remembers the last id it sent and asks
`ListActivitySince(last)` again every poll. SQLite gave out ids in commit order. PostgreSQL gives out an identity value when
the row is inserted, and transactions commit in any order: a transaction can take id N, a second one take N+1 and commit
first, a poll return N+1, and N, committed a moment later, is never sent. The entry is lost for every open browser. The test
`TestConcurrencyTheActivityFeedMissesNothing` showed it on the unhardened port: 1121 entries, the reader saw 1089.

Chosen: (a), ids handed out in commit order. `insertActivity` now only checks an entry and queues it on the transaction
(`txn.acts`); `runTx` writes the queued entries of a transaction in one flush (`txn.flushActivity`) right before the commit:

1. it takes `FOR KEY SHARE` on the repository, incident and run rows the entries point at (the locks the inserts' foreign key
   checks need),
2. it takes `pg_advisory_xact_lock(activityLockKey)`,
3. it inserts the entries in the order they were queued, each with the time of its insert,

and the transaction commits right after. The lock is released after the commit has made the rows visible, so the next
transaction draws its ids only after the previous one's are visible: id order is commit order, and a reader that sees id M
sees every smaller id that will ever exist (gaps of rolled-back transactions are never filled). Activity is still written in
the transaction of the state change it describes. The cost: activity-writing transactions serialise for their last few
statements before the commit. `AddActivity`, the one writer outside a transaction, now runs in one. `LastActivityID`
(`MAX(id)`, the resume point of a fresh stream) is correct for the same reason.

Deadlocks: a transaction that holds the activity lock waits for nothing but its commit. Step 1 takes, before the lock, the only
row locks the inserts could still have to wait for (a cascading delete of a repository holds them), and since every entry is
written at the end, no statement of the transaction runs after the lock. So the activity lock is never part of a wait cycle.

(b), readers that skip ids still in flight, needs to know which transactions are in flight for which ids (an xid column, a
schema change); (c), a lag window, only makes the loss less likely. Both rejected.

Run events (`AppendEvent`, followed by `Events(runID, afterSeq)` in `internal/server/stream.go`) computed `MAX(seq) + 1` in
the insert: two appends to one run took the same seq and one failed on the primary key, and seq order need not be commit order.
`AppendEvent` now runs in a transaction that locks the run's row (`FOR NO KEY UPDATE`) first: appends of one run run one after
the other, so seq is gapless and in commit order. No other table is followed by id: `ListActivity` (paging back with `Before`),
`ListApprovals`, `ListNotes` and `ListToolCalls` re-read whole lists.

## Deletes that cascade

Found by `TestConcurrencyDeleteRepoAndActivityWriters`, and present on the unhardened port too (it failed there with
`deadlock detected (SQLSTATE 40P01)`): `DeleteRepo` locks the repository and then, in its cascade, the repository's incidents
(and their notes, and the rows that link to them). `ResolveIncident` locks an incident and then needs `FOR KEY SHARE` on the
repository for the foreign key of its activity entry. The two orders close a cycle, and PostgreSQL aborts one of them after
`deadlock_timeout` (one second). SQLite never had it. The same shape exists for `DeleteConnection` (cascades into the repos),
`DeleteHost` (cascades into skills, bindings and the login, and sets the host of incidents to NULL, while `TouchIncidentSignal`
locks an incident and then the host) and `DeleteSkill` (unlinks the proposals that replace the skill, while `CreateProposals`
deletes such a proposal and then locks the skill).

Mechanism: these four run in `inDeleteTx`, a transaction with `SET LOCAL lock_timeout = '50ms'` that is run again after a short
pause (up to 40 attempts) when a wait runs out (`55P03`). A delete that never waits long is in practice not part of a lasting cycle: it
steps back before the other transaction's deadlock check (after one second by default) can fire, the other transaction
finishes, and the delete tries again. The closures are idempotent. Assumed: the server's `deadlock_timeout` stays well above
50 ms (the default is 1 s). Locking every child before the parent instead would have to follow every foreign key by hand and
break with the next one added.

## Transactions (`inTx`)

| # | Function | Statements | Assumed (single writer) | Class | Mechanism | Test |
|---|---|---|---|---|---|---|
| 1 | `FinishRun` (store.go) | guarded `UPDATE runs ... status = 'running'`; `closeRunTx` | nothing beyond the guard | CAS | guard; activity flush | `TestConcurrencyBeginToolCallAndTheEndOfItsRun`, `TestConcurrencyDecideOrTheEndOfTheRun` |
| 2 | `StartDiagnosis` (diagnosis.go) | read incident; count active runs; count today's automatic runs; per-incident cap and cooldown from the row; count hosts; insert run, run_hosts; update incident; activity | no run starts between the counts and the insert; the incident row is unchanged until the update (an ignore or resolve in between was overwritten with `diagnosing`); a counted host still exists at the insert | limit + RMW | `lockDiagnosis` as the first statement; incident read `FOR NO KEY UPDATE OF i`; hosts counted `FOR KEY SHARE` | `TestConcurrencyTheDailyLimitHolds`, `TestConcurrencyThePerIncidentCapHolds`, `TestConcurrencyOneRunAtATime`, `TestConcurrencyStartDiagnosisAndIgnore` |
| 3 | `CompleteDiagnosis` | read run; guarded `UPDATE incidents ... WHERE run_id = ?` with `CASE` on state; read repo; activity | the incident's state when the `CASE` is computed | CAS | none (the guard and `CASE` are re-evaluated on the newest row) | none of its own (one guarded statement) |
| 4 | `FailDiagnosis` | as 3 | as 3 | CAS | none | as 3; used under load by `TestConcurrencyThePerIncidentCapHolds` |
| 5 | `OpenIncident` | insert (unique `incidents_active_key`); activity | no second active incident of the key | unique | `23505` on `incidents_active_key` is `ErrExists` | `TestConcurrencyOpenIncidentOnce` |
| 6 | `RecordRecurrence` | guarded update; activity | none | CAS | none | none of its own (one guarded statement) |
| 7 | `ResolveIncident` | guarded update, `resolved_while_ignored` from the row's state; activity | the state read by the `CASE` | CAS | none | `TestConcurrencyResolveAndIgnore` |
| 8 | `ReopenIncident` | guarded update (`state = 'resolved'`), state from `EXISTS` on runs; activity | one reopen; no other active incident of the key; the `EXISTS` sees a run that ended while the update waited for the row (a waiting `UPDATE` is re-checked on the new row, but its subquery keeps the statement's first snapshot: the incident stayed `diagnosing`) | CAS + unique | the incident's row `FOR NO KEY UPDATE` as a statement of its own first (`lockIncidentForState`), so the update starts with a fresh snapshot; guard; `23505` is `ErrExists` | `TestConcurrencyReopenOnce`, `TestReopenThatWaitsForTheIncidentSeesTheRunThatFinishedMeanwhile` |
| 9 | `IgnoreIncident` | guarded update; activity | none | CAS | none | `TestConcurrencyResolveAndIgnore`, `TestConcurrencyStartDiagnosisAndIgnore` |
| 10 | `UnignoreIncident` | guarded update, state from `EXISTS` on runs; activity | no responder run starts meanwhile; the `EXISTS` sees a run that ended while the update waited for the row (as 8) | RMW | `StartDiagnosis` refuses an ignored incident and locks the row, so the two are ordered; the incident's row `FOR NO KEY UPDATE` first (`lockIncidentForState`), as 8 | `TestUnignoreThatWaitsForTheIncidentSeesTheRunThatFinishedMeanwhile` |
| 11 | `BeginToolCall` (toolcalls.go) | read repeat; read run liveness; count calls; read run incident; read incident; read automatic; insert; read back; activity | a repeat is found by the read (a concurrent repeat failed on `UNIQUE (run_id, tool_use_id)` with a raw error); the counts hold until the insert; the run stays live until the commit (a call could be left waiting on a cancelled or finished run) | RMW + limit + unique | the run's row `FOR NO KEY UPDATE` first (one statement now gives status, cancel, automatic and incident): calls of one run, `CancelRun`, `closeRunTx`, `DecideApproval` and `Heartbeat` are ordered on it; the repeat read and the counts come after the lock; the incident read is `FOR KEY SHARE`. The exported contract is unchanged; a concurrent repeat now answers `existed` with the first call, never a unique violation | `TestConcurrencyARepeatedToolCallIsRecordedOnce`, `TestConcurrencyThePendingLimitOfARunHolds`, `TestConcurrencyBeginToolCallAndCancelRun`, `TestConcurrencyBeginToolCallAndTheEndOfItsRun` |
| 12 | `FinishToolCallWithActivityKind` | guarded update; read back; activity | none | CAS | none | none of its own (one guarded statement) |
| 13 | `DecideApproval` | read call; check pending; read run liveness; unguarded `UPDATE ... WHERE id = ?`; read back; activity | the call is still pending and its run live at the update (an expiry, an abandon, a cancel or the end of the run in between was overwritten: an abandoned call came out approved or denied, with a second activity entry) | RMW | the run's row `FOR SHARE`, then the update guarded by `status = 'waiting' AND decision = 'pending'`; no row is `ErrNotPending` | `TestConcurrencyExpireOrDecide`, `TestConcurrencyDecideOrAbandon`, `TestConcurrencyDecideOrTheEndOfTheRun` |
| 14 | `ExpireCall` | read call; guarded update; activity | none: the update repeats the guard | CAS | none | `TestConcurrencyExpireOrDecide` |
| 15 | `AbandonCall` | `abandonTx` (one guarded `UPDATE ... RETURNING`); activity per call | none | CAS | none | `TestConcurrencyDecideOrAbandon` |
| 16 | `AbandonAllWaiting` | as 15 | none (start-up) | CAS | none | used as the long writer of `TestConcurrencyTheActivityFeedMissesNothing` |
| 17 | `AddNote` | read incident; insert note; activity | the incident exists (the foreign key checks it anyway) | append-only | none | none |
| 18 | `CancelRun` | read run; queued: guarded update (rows affected ignored); running: unguarded `UPDATE runs SET cancel_requested = 1`; `abandonTx`; activity | the run's status is unchanged until the update (a claim in between left a running run with a "cancelled before it started" entry; a finish in between got `cancel_requested` and a "Run cancelled" entry) | RMW | the run's row read `FOR NO KEY UPDATE` (a claim skips it, `SKIP LOCKED`) | `TestConcurrencyCancelOrClaim`, `TestConcurrencyBeginToolCallAndCancelRun` |
| 19 | `MintRunToken` (runtokens.go) | read run; insert token (unique run_id) | the run stays running | benign | none: a token minted for a run that ends at the same moment is not revoked by that end, but `RunForToken` requires a running, uncancelled run, so it opens nothing | none |
| 20 | `Heartbeat` (heartbeat.go) | read run; update `last_heartbeat_at`; count pending | none that matters | benign | none: the answer can be one beat old; the update is a timestamp | none |
| 21 | `failRuns` (reap.go) | `UPDATE ... RETURNING` (guarded on running); `closeRunTx` per run | none | CAS | guard; all entries of all runs in the one flush at the end | none of its own |
| 22 | `CreateRunWith` (runs_with.go) | count incident; count each host; insert run; insert run_hosts | the host still exists at the insert (a delete in between failed the insert with a raw foreign key error `23503`) | RMW | incident and hosts counted `FOR KEY SHARE`, which holds off their deletion until the commit; a delete that came first is `ErrNoSuchHost` | `TestConcurrencyCreateRunWithAndDeleteHost` |
| 23 | `CreateHost` (hosts.go) | insert (unique `lower(name)`); activity | name free | unique | `23505` is `ErrExists` | none |
| 24 | `UpdateHost` | read address, port, pin; decide whether the pin goes; update; activity | the pin read is the pin at the update (a pin in between survived an address change: a key pinned for the old endpoint stayed on the new one) | RMW | the row read `FOR NO KEY UPDATE` | `TestConcurrencyUpdateHostAndPinHostKey` |
| 25 | `DeleteHost` | read name; count skills; delete; activity | the count is the number deleted; the cascade does not deadlock | RMW + cascade | `inDeleteTx`; the row read `FOR UPDATE` (no skill of the host can be added after it: an insert's key check waits), so the count is exact | `TestConcurrencyDeleteHostAndItsWriters`, `TestConcurrencyPinHostKeyAndDeleteHost`, `TestConcurrencyCreateRunWithAndDeleteHost` |
| 26 | `SaveHostCredential` | read name; upsert; reset status; touch; activity | the host exists | RMW | `hostName` reads `FOR KEY SHARE` (a delete waits for the commit) | `TestConcurrencyDeleteHostAndItsWriters` |
| 27 | `DeleteHostCredential` | as 26 with a delete | as 26 | RMW | as 26 | none |
| 28 | `PinHostKey` | read name; update; touch; activity | the host exists (on a deleted host the update changed nothing and the entry was written anyway) | RMW | as 26 | `TestConcurrencyUpdateHostAndPinHostKey`, `TestConcurrencyPinHostKeyAndDeleteHost` |
| 29 | `UnpinHostKey` | as 28 | as 28 | RMW | as 26 | none |
| 30 | `SaveHostTest` | guarded update (`pinned_key = ?`); existence read | none | CAS | none | none |
| 31 | `StartLearning` (learner.go) | read host; count learner runs of the host; count active runs; count proposals; insert run, run_hosts; activity | nothing starts and no proposal is stored between the counts and the insert | limit | `lockDiagnosis` (the same "one run at a time" as 2), then `lockSkills` (the pending count) | `TestConcurrencyStartLearningOnce` |
| 32 | `CreateSkill` (skills.go) | read host; count scope; insert (unique scope and name); activity | the scope count holds until the insert | limit | `lockSkills` first | `TestConcurrencyTheScopeLimitHolds` |
| 33 | `UpdateSkill` | read skill; check not proposed; unguarded update; activity | the row read is the row updated (a delete in between still logged "changed") | RMW | the row read `FOR NO KEY UPDATE OF s` | `TestConcurrencyUpdateAndDeleteSkill` |
| 34 | `DeleteSkill` | read skill; check; delete; activity | as 33 (two deletes both logged); the cascade does not deadlock | RMW + cascade | `inDeleteTx`; the row read `FOR UPDATE OF s` | `TestConcurrencyUpdateAndDeleteSkill`, `TestConcurrencyDeleteSkillAndAProposalThatReplacesIt` |
| 35 | `CreateProposals` (skills_proposals.go) | read host; count pending; per item: delete, read replaced, insert; activity | the pending count holds until the inserts; the replaced skill still exists at the insert | limit | `lockSkills` first; the replaced skill read `FOR KEY SHARE` | `TestConcurrencyThePendingProposalLimitHolds`, `TestConcurrencyDeleteSkillAndAProposalThatReplacesIt` |
| 36 | `ProposalWithReplaced` | two reads | none (read-only, one view for the review) | benign | none: each read has its own snapshot; the confirm re-reads everything under its own locks | none |
| 37 | `ConfirmSkill` | read proposal; read replaced; count scope; update or update + delete; activity | the proposal is still a proposal at the write (two confirms both wrote and both logged); the replaced skill is still what was read; the scope count holds | limit + RMW | `lockSkills` first, the proposal read `FOR UPDATE OF s`, the replaced skill `FOR NO KEY UPDATE OF s` | `TestConcurrencyConfirmOnce`, `TestConcurrencyTheScopeLimitHolds` |
| 38 | `RejectSkill` | read proposal; delete; activity | the proposal still exists (a confirm and a reject both went through) | RMW | the row read `FOR UPDATE OF s` | `TestConcurrencyConfirmOnce` |
| 39 | `SetSetting` (settings.go) | upsert; activity | none | append-only | none (`ON CONFLICT` is atomic) | none |
| 40 | `DeleteSetting` | delete; activity if a row went | none | CAS | none | none |

`migrateFS` (store.go) runs on a connection of its own under the migration lock (task 3) and is not an `inTx` site.

## Functions with more than one statement and no transaction, and single statements that read and write

| Function | Statements | Assumed | Class | Mechanism | Test |
|---|---|---|---|---|---|
| `ClaimNext` (store.go) | one `UPDATE ... WHERE id = (SELECT ... LIMIT 1)` | the subquery's row is still queued: two claims picked the same row, the second waited for the first and then updated it too (its `WHERE id = X` still matched), and both returned it | claim | subquery `FOR NO KEY UPDATE SKIP LOCKED`, outer `AND status = 'queued'` | `TestConcurrencyClaimNextHandsOutEachRunOnce`, `TestConcurrencyCancelOrClaim` |
| `AppendEvent` | one `INSERT ... SELECT MAX(seq) + 1` | no other append of the run at the same time | ordered feed | a transaction that locks the run's row first | `TestConcurrencyAppendEventNumbersWithoutGaps` |
| `AddActivity` (incidents.go) | one insert | ids in commit order | ordered feed | runs in a transaction, flushed like every other entry | `TestConcurrencyTheActivityFeedMissesNothing` |
| `DeleteRepo`, `DeleteConnection` (github.go) | one delete that cascades | no other transaction holds a child row and waits for the parent | cascade | `inDeleteTx` | `TestConcurrencyDeleteRepoAndActivityWriters` |
| `BeginExecution` (toolcalls.go) | one guarded `UPDATE ... RETURNING` | none | CAS | none | `TestConcurrencyOneExecutionPerApproval` |
| `FinishToolCall` | one guarded update | none | CAS | none | none |
| `TouchIncident`, `TouchIncidentSignal` (incidents.go) | one guarded update | none | CAS | none | `TouchIncidentSignal` in `TestConcurrencyDeleteHostAndItsWriters` |
| `CreateIncidentRun` (incident_runs.go) | one insert | none | append-only | none | none |
| `SaveConnection`, `UpdateConnectionStatus`, `AddRepo`, `SetRepoEnabled` (github.go), `MarkRepoPolled` (incidents.go) | one statement each | none | append-only / CAS | none | none |
| `CreateRun`, `CreateToolRun`, `CreateClusterRun`, `CreateQuestionRun` | `CreateRunWith` | as 22 | as 22 | as 22 | as 22 |
| Every `X` that ends with `return s.GetX(ctx, id)` after its transaction (`StartDiagnosis`, `OpenIncident`, `CreateRunWith`, `CancelRun`, `CreateHost`, `UpdateHost`, `CreateSkill`, `UpdateSkill`, `ConfirmSkill`, `CreateProposals`, `StartLearning`) | the transaction, then a read | the row read back is the row written | benign | none: the read shows the row as it is after the commit, as an API read a moment later would; no decision is taken on it | none |
| Pure reads (`GetRun`, `ListRuns`, `Events`, `ListActivity`, `ListActivitySince`, `LastActivityID`, `ListIncidents`, the skill and host reads, `RunCounts`, `HasActiveRun`, `AutoRunsSince`, `ListAutoCandidates`, `PendingApprovalForRun`, `RunForToken`, `SucceededReads`, `LearnOutcomeRecorded`, `CountProposals`) | one statement | none | benign | none | none |

## Results

All 30 tests, run against the unhardened store of `9b9e254` (`go test ./internal/store -run Concurrency -race -cpu 1`):
23 fail, among them every limit, claim, read-modify-write and feed test and the deadlock of `DeleteRepo`. Seven pass there, as
expected, because the single statement was already safe or because they probe for deadlocks that this change's own locks could
have added; they pin the guarantee, so that a later rewrite is caught:

| Passes on the unhardened tree | Why |
|---|---|
| `TestConcurrencyOneExecutionPerApproval` | `BeginExecution` is one guarded `UPDATE` |
| `TestConcurrencyReopenOnce` | `ReopenIncident` is one guarded `UPDATE` |
| `TestConcurrencyOpenIncidentOnce` | the partial unique index decides |
| `TestConcurrencyResolveAndIgnore` | two guarded `UPDATE`s, `SET` recomputed on the newest row |
| `TestConcurrencyPinHostKeyAndDeleteHost`, `TestConcurrencyDeleteHostAndItsWriters`, `TestConcurrencyDeleteSkillAndAProposalThatReplacesIt` | deadlock probes for the `FOR KEY SHARE` and `FOR UPDATE` reads this change adds (`hostName`, `DeleteHost`, the replaced skill), which make a cascading delete wait where it did not before; they pass on both trees |

After the change: `go test ./internal/store -run Concurrency -race -cpu 1 -count=20` and the whole package pass. The outputs are in
the task report (`.superpowers/sdd/postgres-only/task-6-report.md`).

## Operational notes

- **Advisory locks are database-wide.** `pg_advisory_xact_lock` keys belong to the database, not to a schema. Two Remedy
  installations that share one database on different schemas would take the same keys (`activityLockKey`, `runStartLockKey`,
  `skillsLockKey`) and serialise each other's activity writes, diagnosis starts and skill changes. That is not supported: give
  each installation a database of its own. (The migration runner's two-key lock is scoped to the schema; the schemas of the
  shared test server share the one-key locks, which only serialises the tests.)
- **Activity-writing transactions serialise through their commit.** `flushActivity` takes `activityLockKey` right before the
  commit and holds it until the commit has finished, so that ids are handed out in commit order. Every transaction that writes
  an activity entry therefore commits one after the other, and their throughput is bounded by one commit (one WAL flush) at a
  time, some hundreds per second on ordinary disks. Remedy writes a few entries a minute: fine at this scale, and the first
  thing to revisit for a much busier installation.
- **A delete that stays blocked says so.** When all 40 attempts of `inDeleteTx` run out (other transactions held the rows for
  about four seconds), the error is `store.ErrDatabaseBusy`, "the database is busy; try again", with the last `55P03` still in
  its chain (`errors.Is`, `errors.As`). Nothing was deleted; the same request a moment later goes through. The admin API
  answers such a delete (`DELETE /api/repos/{id}`, `/api/github/connection`, `/api/hosts/{id}`, `/api/skills/{id}`) with
  `503`, the body `{"error":"the database is busy; try again"}` and `Retry-After: 2` (`internal/server/busy.go`), not a `500`.
- **Text with a NUL byte or invalid UTF-8 is stored with U+FFFD in its place.** PostgreSQL refuses either in a text value
  (`22021`); SQLite stored them. Untrusted text reaches the store with them (a pod log, host output, an agent's result or
  note), and a refused write left a tool call `running` for ever, an approved action without its audit result and activity
  entry, or a run whose result was lost. The store's wrapper (`db.go`, `cleanArgs` in `textargs.go`) therefore replaces each
  NUL and each invalid UTF-8 sequence of each text argument it covers (`string`, `*string`, a valid `sql.NullString`, any type
  of kind string) with U+FFFD before the statement is sent (`internal/pgtext`). This is a deliberate change from SQLite's behaviour, and it
  touches only text: byte slices, among them every sealed value and token hash on its way to a `BYTEA` column, are sent as
  they are. Arrays and other slice types, such as the `[]string` of an `= ANY(?)`, are not covered either: a caller that
  passes a slice of text must clean its elements.
  Different dirty values can collapse to one after cleaning: a run of adjacent invalid bytes becomes one U+FFFD and a NUL
  becomes U+FFFD. Two different `tool_use_id`s such as `"a\x00"` and `"a\uFFFD"` would therefore become one key of
  `UNIQUE (run_id, tool_use_id)`, and the second call would be treated as a replay of the first. That is the safe direction:
  no second approval and no second execution. Practically only a NUL against a literal U+FFFD can collide, because the JSON
  decoding of the MCP request already replaces invalid bytes (`encoding/json` replaces each invalid byte separately while the
  store collapses a run into one, so the text of an audit entry can differ cosmetically from what the agent sent).
- **`deadlock_timeout` must stay well above 50 ms.** `inDeleteTx` relies on stepping back before the deadlock check runs.
  `store.Open` reads `deadlock_timeout` after the migrations and logs one warning when it is below 200 ms; the store works
  either way, but a rare wait cycle of a delete may then end in a deadlock error (`40P01`) instead of a retry.
