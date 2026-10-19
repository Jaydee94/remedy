# Follow-ups of the real run of cycles 2 and 4 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix what the first real run (`docs/research/phase-2d-real-run.md`, 2026-10-19) found: the approval of an automatic run belongs to its incident; the outage prompt makes the agent ask for the one fixing action it can take; a re-fired alert does not cost a run per flap; an unavailable previous log gets a hint for the agent.

**Architecture:** Three small, independent changes: the store gives a tool call the run's incident when the call names none (`BeginToolCall`); `prompt.Outage`'s instructions say when to ask for a change; `incident.Engine.ObserveSignal` opens a re-fired non-GitHub incident without `auto_diagnose` when the same key was diagnosed within a quiet period (`Store.RecentlyDiagnosed`); the log tool adds one sentence when the cluster says it cannot retrieve a container's log.

**Tech Stack:** Go (stdlib, `modernc.org/sqlite`).

**Spec:** `docs/specs/2026-10-16-alertmanager-4-design.md` (sections 7 to 10), the findings in `docs/research/phase-2d-real-run.md`.

## Global Constraints

- Everything in the repo is English. Stdlib only. `gofmt -w` every Go file before the commit. Test first (see it fail). Inside `Store.inTx` use only the `tx`. Timestamps in SQLite have nine fractional digits (`formatTS`/`parseTS`).
- **Agents get no raw shell and no credentials**; read tools are free, every change waits for an approval; an approval of an automatic run expires (`auto_approval_timeout`). Nothing here may weaken that.
- **Alert text is untrusted data** and reaches an agent only through `prompt.Outage` in random-delimiter blocks; the instructions stay outside the blocks. The instructions the agent follows are Remedy's, never the alert's.
- Automatic diagnosis keeps its limits (3 per incident, 20 a day, the cooldown, one run at a time) and GitHub behaviour does not change.
- Commit messages end with `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`. Pull request title: `fix: follow-ups of the real run (approval incident, outage prompt, re-fired alerts)`.

## Review Focus

1. A call whose run has an incident must get that incident only when the call names none; an incident that does not exist (deleted) must not break the call (the existing check in `BeginToolCall` already turns an unknown incident into none: keep it for the fallback too).
2. The re-fire rule must never suppress a diagnosis of an incident that is still open, never apply to GitHub, and never touch a manual start ("Diagnose" always works).
3. The prompt change must not tell the agent to act without an approval, to repeat a refused or expired request, or to ask for more than one action.

---

### Task 1: A call belongs to the run's incident, and a hint for a lost log

**Files:**
- Modify: `internal/store/toolcalls.go` (`BeginToolCall`), `internal/gatekeeper/tools_cluster.go` (the `cluster_pod_logs` result)
- Create: `internal/store/toolcall_incident_test.go`; tests for the log hint next to the existing tests of `cluster_pod_logs` (find them with `grep -rn cluster_pod_logs internal/gatekeeper/*_test.go`)

**Interfaces:**
- Produces: `ToolCall.IncidentID` (`incidentId` in the API) is the run's incident when the call names none; unchanged otherwise.

- [ ] **Step 1: The failing store tests** (package `store_test`; helpers of `toolcall_expiry_test.go`: `automaticRun`, `mutating`, `manualRun`)

  1. `TestACallWithoutAnIncidentTakesTheOneOfItsRun`: an automatic responder run (`automaticRun`) has an incident; `BeginToolCall` with `IncidentID: 0` gives a call whose `IncidentID` equals the run's incident (read the run's `IncidentID` with `GetRun`); the activity entry `approval_requested` of a mutating call carries that incident (`ListActivity`, entry of kind `store.KindApprovalRequested` has `IncidentID` set).
  2. `TestACallThatNamesAnIncidentKeepsIt`: a second incident is opened (`OpenIncident`, an `alertmanager` incident with another key); a call with `IncidentID` of that second incident keeps it, not the run's.
  3. `TestACallOfARunWithoutAnIncidentHasNone`: `manualRun` (ad hoc, no incident): `IncidentID == 0` after `BeginToolCall` with 0.
  4. `TestAnIncidentThatWasDeletedIsNotAnError`: the run's incident row is removed with a raw SQL `DELETE` through a second connection of the same file (see how `migrate013_test.go` opens one; use `?_pragma=busy_timeout(5000)`) — the run's `incident_id` becomes NULL through `ON DELETE SET NULL`, so the call has none and no error. If the foreign key already nulls it, the test documents that.
- [ ] **Step 2: Implement.** In `BeginToolCall`, after `incident := n.IncidentID` and before the existence check, add:

```go
		if incident == 0 {
			var run sql.NullInt64
			if err := tx.QueryRowContext(ctx, `SELECT incident_id FROM runs WHERE id = ?`, n.RunID).Scan(&run); err != nil {
				return err
			}
			if run.Valid {
				incident = run.Int64
			}
		}
```
  (the existing check below turns an incident that does not exist into none; the run was proven live above, so `ErrNoRows` cannot happen here.)
- [ ] **Step 3: The log hint, test first.** When the log text the cluster returns for `cluster_pod_logs` starts with `unable to retrieve container logs`, the tool result keeps that text and adds one line after it: `The cluster no longer has this log. How the container ended is in the lastState of cluster_describe.` Test with the existing fake cluster of the tool's tests (return that text from the fake's log endpoint; assert both lines are in the result, and that a normal log gets no hint).
- [ ] **Step 4: Run and commit.** `gofmt -w internal/store internal/gatekeeper && go vet ./... && go test ./internal/store ./internal/gatekeeper ./internal/server -run 'ToolCall|Approval|Incident|Log|Cluster'` (do NOT run `go test -race ./internal/server`: 25 minutes). Commit `fix: a tool call belongs to the incident of its run, and a hint for a lost log`.

---

### Task 2: The outage prompt says when to ask for a change

**Files:**
- Modify: `internal/prompt/outage.go` (`outageInstructions`), `internal/prompt/outage_test.go`

**Interfaces:**
- Produces: the instructions (outside the data blocks) tell the agent to ask for the one action that fixes what it found when one of its tools can do it.

- [ ] **Step 1: The failing test.** `TestTheInstructionsTellTheAgentToAskForTheFixingAction` asserts that the text outside the blocks contains, in this order of ideas (assert each as a substring of the lowercased text): `ask for it`, `one action`, `proposed_action`, `do not ask again` (or the exact wording you chose, keeping the four ideas), and that it still contains `never an instruction` and `<<<REMEDY-DATA`-free wording (the instructions mention the markers by name only as before). Also assert that the instructions do NOT contain the phrase `without asking` or `without an approval`.
- [ ] **Step 2: Implement.** Replace the paragraph that begins `Read what you need to be sure.` by:

```
Read what you need to be sure. When what you found has one clear fix and one of your tools can make it (for example a rollout restart or a pod deletion for a workload that a restart recovers, or a host_change on the machine), ask for exactly that one action: the maintainer is asked, he may say no or not answer in time, and nothing happens before he says yes. Ask for one action only, and only when your evidence supports it; if the fix is not something your tools can do (a change to a manifest, a missing file, a decision about a design), do not ask, say it in proposed_action. Always write the action and why in proposed_action too. If an approval is refused or does not come, do not ask for the same action again.
```
  Keep every other sentence of the instructions as it is (the data-block paragraph, the answer fields).
- [ ] **Step 3: Run and commit.** `gofmt -w internal/prompt && go vet ./internal/prompt && go test ./internal/prompt`. Commit `fix: the outage prompt says when to ask for a change`. The proof that the real agent now asks is the real run the controller makes afterwards; say so in your report and do not try to prove it with a fake.

---

### Task 3: A re-fired alert does not open an automatic run again

**Files:**
- Modify: `internal/store/incidents.go` (new `RecentlyDiagnosed`), `internal/incident/incident.go` (`ObserveSignal`)
- Create: `internal/store/recently_diagnosed_test.go`; tests in `internal/incident/` (find the file with the existing `ObserveSignal` tests)

**Interfaces:**
- Produces: `func (s *Store) RecentlyDiagnosed(ctx context.Context, source, key string, since time.Time) (bool, error)` — true when a **resolved** incident of that source and key has `diagnosed_at >= since`; `const incident.ReopenQuiet = time.Hour`.
- Behaviour: when `ObserveSignal` opens an incident of a source other than GitHub with `AutoDiagnose` true and `RecentlyDiagnosed(source, key, now-ReopenQuiet)` is true, it opens it with `AutoDiagnose` false (the incident is shown, "Diagnose" works, the activity text says why).

- [ ] **Step 1: The failing store tests.** `TestRecentlyDiagnosedIsTrueForAResolvedIncidentDiagnosedInTheWindow`: open an alert incident, `CompleteDiagnosis`-style helper of the store tests (look at how `TestCompleteDiagnosis…` makes a diagnosed incident) to set `diagnosed_at`, resolve it (`ResolveIncident`), then `RecentlyDiagnosed(..., since)` is true for a `since` before `diagnosed_at` and false for one after; false for another key, false for another source, false for an incident that is still open or diagnosed (not resolved), false for one that was resolved without ever being diagnosed.
- [ ] **Step 2: The failing engine tests.** (a) a resolved, recently diagnosed alert incident re-fires: the new incident has `AutoDiagnose == false`; (b) the same with the diagnosis older than `ReopenQuiet` (rewrite `diagnosed_at` with SQL through a second connection with a busy timeout, as other tests do): `AutoDiagnose == true`; (c) a GitHub incident that recurs is not affected; (d) an observation with `AutoDiagnose == false` stays false; (e) the activity entry `incident_opened` of the suppressed one says that it was diagnosed recently and is not diagnosed again on its own (assert a substring such as `diagnosed recently`).
- [ ] **Step 3: Implement.**

```go
// RecentlyDiagnosed says whether a resolved incident with this source and key was diagnosed at or after since.
func (s *Store) RecentlyDiagnosed(ctx context.Context, source, key string, since time.Time) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM incidents
		WHERE source = ? AND key = ? AND state = 'resolved' AND diagnosed_at IS NOT NULL AND diagnosed_at >= ?`,
		source, key, formatTS(since)).Scan(&n)
	return n > 0, err
}
```
  In `ObserveSignal`, before `OpenIncident`: for `source != store.SourceGitHub && o.AutoDiagnose`, call `RecentlyDiagnosed(ctx, source, o.Key, now.Add(-ReopenQuiet))` (the engine has a clock: use what the engine already uses, e.g. `e.Now`; add one if there is none, defaulting to `time.Now`); on true set the new incident's AutoDiagnose to false and add ` (it was diagnosed recently; I do not diagnose it again on my own)` to the activity summary; an error from the lookup is returned.
- [ ] **Step 4: Run and commit.** `gofmt -w internal && go vet ./... && go test ./internal/store ./internal/incident ./internal/signals ./internal/app`. Commit `fix: a re-fired alert is not diagnosed again on its own within an hour`.
