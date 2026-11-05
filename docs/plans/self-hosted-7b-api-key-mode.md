# Cycle 7b: API-key mode, implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An operator can run their installation on their own Anthropic API key instead of a subscription login, chosen once at install time, shown everywhere, guarded against billing the wrong account, and limited in cost.

**Architecture:** The runner reads `REMEDY_AUTH_MODE` (`subscription` or `api_key`). One function, `provider.EnvFor(mode, env)`, builds the CLI's environment for both modes. The runner reads `apiKeySource` from each run's `init` event and cancels a run whose billing contradicts its mode, scrubs the literal key from everything it posts, reports its mode to the control plane, and passes a per-run budget to the CLI. The control plane records `runs.auth_source`, shows the mode and the cost of key-billed runs only, and refuses an automatic start over a daily budget.

**Tech Stack:** Go (`internal/provider`, `internal/runner`, `internal/store`, `internal/server`, `internal/settings`, `internal/redact`, `cmd/remedy-runner`), React/TypeScript (`web/src`), Helm (`deploy/chart`), Markdown.

**Spec:** `docs/specs/2026-10-21-self-hosted-for-others-design.md`, sections 4, 5, 8, 9, 11 and 13 (points 1 and 3 to 4). 7a is merged (release 0.19.x). 7c (the webhook) is not part of this plan.

## Global constraints

- Everything in the repo is English. Pull request titles are conventional commits; the pull request of this cycle is `feat:` (a new mode, nothing existing changes), the plan's own is `docs:`.
- The key lives in the runner's environment only. It never reaches the control plane, a database column, a log, an event, an API answer, an error, argv or a file Remedy writes. `ANTHROPIC_AUTH_TOKEN` and `ANTHROPIC_BASE_URL` stay banned in both modes; `--bare` is not used (spec 5.5).
- In `subscription` mode a key in the runner's environment is dropped as today, and now reported once at start (a warning in the log).
- Money is shown only for key-billed runs, and always as the CLI's estimate (`total_cost_usd` is not a charge; for a subscription it means nothing).
- The migration number is 003 (the spec's "017" is the SQLite numbering; `002_incidents_hot_updates.sql` is the last). Existing tests list the applied migrations in one constant, `appliedMigrations` in `internal/store/schema_test.go`; update it.
- Money settings are cents (integers), a call that departs from the spec's USD decimals: `settings.Value` has `Num int` and no decimal kind, and a new kind would touch the registry, the API and the Setup page for two values. The UI shows dollars.
- Do not run the real CLI with a key in a test or in CI. The only run with a key is Task 12, started by the maintainer, who pays for it.
- Tests: Go tests need PostgreSQL (`make test` starts one); chart tests need `helm`; `make check` is the authority before the pull request.

## Review focus

1. **A subscription runner with `ANTHROPIC_API_KEY` in its environment** (a leftover from a shell, a mis-set Secret) must still bill the subscription: the key is dropped, a warning is logged, and a run whose `init` says a key was used is cancelled. (Tasks 1, 2, 4.)
2. **The key inside what the CLI prints:** in a stdout JSON string (escaped context), in a stderr line, as a substring of a longer token, and in `Result`. The scrub must keep every posted line valid JSON. (Task 3.)
3. **An `init` event without `apiKeySource`** (an older CLI, a changed format). Key mode fails closed (the run is cancelled, billing cannot be shown to be the key's); subscription mode runs and records `auth_source = ''`. (Task 4.)
4. **A runner that restarts in the other mode, or has not reported yet.** The daily budget is enforced only while the runner reports key mode; the claim carries a per-run budget only then; a runner in key mode with no value in the claim uses its own built-in 1.00. (Tasks 6 to 8.)
5. **Rollout order:** the status body is decoded strictly, so a new runner posting `authMode` to an old control plane is answered 400 until the control plane is upgraded (it retries every 15 s); an old runner posting no `authMode` to a new control plane stays "subscription". The chart upgrades both; say so in the runbook. (Task 6.)

---

## File structure

| File | Responsibility |
|---|---|
| `internal/provider/env.go` (new), `env_test.go` | `Mode`, `EnvFor`: the CLI's environment per mode. |
| `internal/provider/claude.go`, `login.go`, `provider.go` | Use `EnvFor`; parse `apiKeySource`; pass `--max-budget-usd`. |
| `internal/runner/scrub.go` (new), `execute.go`, `loop.go`, `status.go` | The scrub, the billing guard, the budget, reporting the mode. |
| `cmd/remedy-runner/main.go`, `internal/config/config.go` | Validate the mode and the key at start. |
| `internal/run/run.go` | `ReasonBillingMismatch`, `Outcome.AuthSource`, `Run.AuthSource`, `Claim.MaxBudgetCents`. |
| `internal/store/migrations/003_runs_auth_source.sql` (new), `store.go` | Column `runs.auth_source`; recorded at finish. |
| `internal/store/diagnosis.go`, `internal/settings/registry.go` | The two budget settings; the daily budget in `StartDiagnosis`. |
| `internal/server/runner_status.go`, `responder.go`, `runnerapi.go` (the claim) | `authMode` in the status; `apiSpendLast24hCents` in the limits; the claim's budget. |
| `internal/redact/redact.go` | A pattern for the vendor's key prefix. |
| `web/src/components/setup/RunnerSection.tsx`, `runnerstatus.ts`, `api.ts`, `thread.ts`, `runview.ts` | The mode chip, the spend line, "billed to". |
| `deploy/chart/values.yaml`, `templates/runner-statefulset.yaml`, `templates/validate.yaml`, `deploy/runner_auth_test.go` (new) | `runner.auth`. |
| `docs/runbook/api-key-mode.md` (new), `README.md`, `CLAUDE.md`, `docs/design.md`, `docs/research/api-key-mode-real-run.md` (new, Task 12) | Documents. |

---

### Task 1: The environment of the CLI per mode

**Files:**
- Create: `internal/provider/env.go`, `internal/provider/env_test.go`
- Modify: `internal/provider/claude.go:70`, `internal/provider/login.go:66,110,134`, `internal/provider/provider.go` (the doc comment of the allowlist)

**Interfaces:**
- Produces: `type Mode string` with `ModeSubscription Mode = "subscription"` and `ModeAPIKey Mode = "api_key"`; `func EnvFor(mode Mode, parent []string) []string`; `func (m Mode) Valid() bool`; `Claude{Binary string, Mode Mode}` (the zero `Mode` means subscription); `LoginCheck`, `Version` and the relay's command builder take a `Mode`.

- [ ] **Step 1: Write the failing tests** in `env_test.go`, a table over both modes with the parent environment `PATH`, `HOME`, `ANTHROPIC_API_KEY=sk-ant-x`, `CLAUDE_CODE_OAUTH_TOKEN=tok`, `ANTHROPIC_AUTH_TOKEN=a`, `ANTHROPIC_BASE_URL=http://evil`, `AWS_ACCESS_KEY_ID=k`, `CLAUDE_CODE_USE_BEDROCK=1`: `subscription` keeps `PATH`, `HOME`, `CLAUDE_CODE_OAUTH_TOKEN` and nothing of the others; `api_key` keeps `PATH`, `HOME`, `ANTHROPIC_API_KEY` and drops `CLAUDE_CODE_OAUTH_TOKEN`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_BASE_URL`, `AWS_*`, `CLAUDE_CODE_USE_*`. Order is preserved. A zero `Mode` equals subscription. `Mode("x").Valid()` is false. The existing `TestFilterEnvDropsAPIKeysAndKeepsAllowlist` and `TestLoginCheckDoesNotPassTheAPIKeyOn` / `TestVersionDoesNotPassTheAPIKeyOn` keep passing for the zero mode.
- [ ] **Step 2: Run them.** Run: `go test ./internal/provider -run 'EnvFor|Mode' -v`. Expected: FAIL, undefined `EnvFor`.
- [ ] **Step 3: Implement** `EnvFor` on top of `envAllowlist`: the base allowlist is shared; `CLAUDE_CODE_OAUTH_TOKEN` is added only for subscription, `ANTHROPIC_API_KEY` only for `api_key`. `FilterEnv` becomes `EnvFor(ModeSubscription, env)` (kept as a wrapper if other code calls it). Replace the four call sites; the doc comment of the allowlist says the two modes.
- [ ] **Step 4: Run** `go test ./internal/provider -race`. Expected: PASS.
- [ ] **Step 5: Commit.** `feat: one function builds the CLI's environment for the subscription and the API-key mode`

### Task 2: The runner's mode at start

**Files:**
- Modify: `internal/config/config.go:200-210`, `cmd/remedy-runner/main.go:95-140`, `internal/runner/loop.go` (a `Mode provider.Mode` field), `internal/config/config_test.go`

**Interfaces:**
- Consumes: Task 1's `Mode`.
- Produces: `config.Runner.AuthMode` validated to `subscription` or `api_key` (anything else is an error naming the two); `runner.Loop.Mode`; the process exits 2 with the fixed message `REMEDY_AUTH_MODE=api_key needs ANTHROPIC_API_KEY in the runner's environment` when the key is empty; in `subscription` mode a non-empty `ANTHROPIC_API_KEY` logs `ANTHROPIC_API_KEY is set but ignored: this runner uses the subscription login` once, without the value.

- [ ] **Step 1: Write the failing tests** in `config_test.go`: `api_key` is accepted, `Api_Key` and `apikey` are refused, the default stays `subscription`. In `cmd/remedy-runner` (a small `validateAuth(mode, env) error` function, tested in `main_test.go`): `api_key` with the key empty or absent is an error with exactly the message above; `subscription` with a key returns a warning string that does not contain the key.
- [ ] **Step 2: Run them.** Run: `go test ./internal/config ./cmd/remedy-runner -run 'Auth' -v`. Expected: FAIL.
- [ ] **Step 3: Implement** `validateAuth`, call it before the loop starts, set `Loop.Mode`, `Claude.Mode` and the login check's mode from `cfg.AuthMode`. The login relay stays off outside subscription mode (existing).
- [ ] **Step 4: Run** `go test ./internal/config ./cmd/remedy-runner ./internal/runner -race`. Expected: PASS.
- [ ] **Step 5: Commit.** `feat: the runner knows its billing mode and refuses to start in key mode without a key`

### Task 3: The scrub

**Files:**
- Create: `internal/runner/scrub.go`, `internal/runner/scrub_test.go`
- Modify: `internal/runner/execute.go` (emit and outcome), `internal/runner/loop.go` (build the scrubber from the key), `internal/runner/execute_test.go`

**Interfaces:**
- Produces: `func NewScrubber(key string) func(string) string` and `func NewScrubberBytes(key string) func([]byte) []byte`; both replace every literal occurrence of the key, and of its first 6 and its last 6 characters when they stand alone (a run of 6 characters bounded by non-token characters), by `[REDACTED:api-key]`. An empty key gives the identity function.

- [ ] **Step 1: Write the failing tests:** the key in the middle of a line, twice in one line, at the start and the end, inside a JSON object (`{"error":"bad key sk-ant-abc..."}` stays valid JSON after the scrub: `json.Valid`), a stdout line and a stderr line of the fake CLI that print it, the first 6 and the last 6 characters alone (replaced) and inside a longer word (kept: `xsk-ant-ab` is not the first 6 of the key standing alone), an empty key (identity), a key that is a prefix of another token. In `execute_test.go`: a run whose fake CLI prints the key on stdout, on stderr and in its `result` event posts no event and no `Result` that contains it.
- [ ] **Step 2: Run them.** Run: `go test ./internal/runner -run 'Scrub' -v`. Expected: FAIL.
- [ ] **Step 3: Implement** the scrubbers (`strings.NewReplacer` over the key and the two fragments with the boundary check for the fragments) and apply them in `Execute` to every `emit` payload before `sink.Event` and to `Outcome.Result` before it is returned. The scrubber is a field of `Loop` and a parameter of `Execute`; with no key it is the identity.
- [ ] **Step 4: Run** `go test ./internal/runner -race`. Expected: PASS.
- [ ] **Step 5: Commit.** `feat: the runner scrubs its API key from everything it posts`

### Task 4: The billing guard

**Files:**
- Modify: `internal/provider/provider.go` (`Line.APIKeySource`, `Spec.OnInit`), `internal/provider/claude.go` (`ParseLine`), `internal/runner/execute.go`, `internal/runner/loop.go`, `internal/run/run.go` (`ReasonBillingMismatch = "billing_mismatch"`, `Outcome.AuthSource`), `internal/testutil` (the fake `claude` can print `apiKeySource`)
- Test: `internal/provider/claude_test.go`, `internal/runner/execute_test.go`, `internal/runner/loop_test.go`

**Interfaces:**
- Consumes: Task 1's `Mode`, Task 2's `Loop.Mode`, Task 3's scrubber.
- Produces: `provider.Line.APIKeySource string` and `Line.IsInit bool` (set for `{"type":"system","subtype":"init"}`); `func (m Mode) Accepts(source string) bool` (subscription accepts `none` and the empty string; key mode accepts any non-empty value other than `none`); `Outcome.AuthSource` is `subscription` for `none`, `api_key` for any other non-empty value, empty when the event had no field; `Spec.OnInit func(source string) bool`: `Execute` calls it once, on the first init line, and if it returns false stops the run through an internal cancel.

- [ ] **Step 1: Write the failing tests:** `ParseLine` of an init line with `"apiKeySource":"none"`, `"ANTHROPIC_API_KEY"` and with no field; `Accepts` as a table (both modes, `none`, `ANTHROPIC_API_KEY`, `oauth`, empty); a fake CLI that prints init with `apiKeySource` `ANTHROPIC_API_KEY` and then sleeps: in subscription mode the run ends `failed` with `FailureReason = billing_mismatch` well before the sleep ends, with the fixed `Result` `The run was stopped: it would have been billed differently than configured.`; the same with `none` in key mode; an init line without the field runs in subscription mode and is cancelled in key mode; `Outcome.AuthSource` is recorded in all three cases.
- [ ] **Step 2: Run them.** Run: `go test ./internal/provider ./internal/runner -run 'Init|Billing|Accepts' -v`. Expected: FAIL.
- [ ] **Step 3: Implement** the parsing (an `init` line carries `apiKeySource`; keep the event posted as it is, the source is not a secret), the `OnInit` hook in `Execute` (its own `context.WithCancel` around the command), and in `handle` the mapping to `out.FailureReason` and the fixed result text. The value is checked against the mode of the loop, never against the control plane.
- [ ] **Step 4: Run** `go test ./internal/provider ./internal/runner -race`. Expected: PASS.
- [ ] **Step 5: Commit.** `feat: a run that would be billed against the configured mode is stopped`

### Task 5: The run records how it was billed

**Files:**
- Create: `internal/store/migrations/003_runs_auth_source.sql`
- Modify: `internal/store/store.go` (`runCols`, the finish `UPDATE` at about line 327, the scan), `internal/run/run.go` (`Run.AuthSource`), `internal/server/runnerapi.go` (finish decodes `Outcome`; check whether it is strict), `internal/store/schema_test.go` (`appliedMigrations`, the column list)
- Test: `internal/store/runs_test.go`, `internal/server` finish test

**Interfaces:**
- Consumes: Task 4's `Outcome.AuthSource`.
- Produces: `runs.auth_source TEXT NOT NULL DEFAULT '' CHECK (auth_source IN ('', 'subscription', 'api_key'))`, set by `FinishRun`; `Run.AuthSource` as JSON `authSource`; the finish endpoint refuses any other value with 400.

- [ ] **Step 1: Write the failing tests:** finishing a run with `AuthSource: "api_key"` returns it from `GetRun` and `ListRuns`; `"banana"` is refused by the endpoint (400) and by the CHECK; an old run has `''`; the schema test sees the column; `appliedMigrations` lists `003_runs_auth_source.sql`.
- [ ] **Step 2: Run them.** Run: `go test ./internal/store ./internal/server -run 'AuthSource|Schema' -v`. Expected: FAIL.
- [ ] **Step 3: Implement** the migration (`ALTER TABLE runs ADD COLUMN auth_source TEXT NOT NULL DEFAULT ''` with the CHECK; no table is rebuilt), the store and server changes. A column with a default changes no data, so no data-migration model test is needed; the schema test is the model.
- [ ] **Step 4: Run** `go test ./internal/store ./internal/server -race`. Expected: PASS.
- [ ] **Step 5: Commit.** `feat: a run records whether it was billed to a subscription or to a key`

### Task 6: The runner reports its mode

**Files:**
- Modify: `internal/runner/status.go` (`Report.AuthMode`), `internal/server/runner_status.go` (the strict body, `ReportMode`, `View.AuthMode`, `AuthMode()` returns the reported mode), `internal/server/loginrelay.go` (unchanged logic, now fed by a real value), `web/src/api.ts`
- Test: `internal/runner/status_test.go`, `internal/server/runner_status_test.go`, `internal/server/loginrelay_test.go`

**Interfaces:**
- Consumes: Task 2's `Loop.Mode`.
- Produces: the status body gains `"authMode": "subscription" | "api_key"`; the control plane refuses any other value (400); `GET /api/runner` gains `authMode` (`""` while unreported, which the UI shows as unknown); the login routes answer `api_key_mode` / `runner_offline` as before, now correct for a runner in key mode.

- [ ] **Step 1: Write the failing tests:** a report with `api_key` shows in `GET /api/runner` and makes `POST /api/runner/login` answer `api_key_mode`; a report with `"authMode":"x"` is 400; a body without the field (an older runner) leaves the mode as it was reported before, or subscription when never reported; the runner's `Report()` carries the loop's mode.
- [ ] **Step 2: Run them.** Run: `go test ./internal/runner ./internal/server -run 'AuthMode|RunnerStatus|LoginRelay' -v`. Expected: FAIL.
- [ ] **Step 3: Implement.** Keep `DisallowUnknownFields`: the field is added on both sides in this task.
- [ ] **Step 4: Run** `go test ./internal/runner ./internal/server -race`. Expected: PASS.
- [ ] **Step 5: Commit.** `feat: the runner reports its billing mode to the control plane`

### Task 7: The per-run budget

**Files:**
- Modify: `internal/settings/registry.go` (two settings), `internal/run/run.go` (`Claim.MaxBudgetCents`), `internal/server/runnerapi.go:84` (where the claim's `RunTimeoutSeconds` is filled), `internal/provider/provider.go` (`Spec.MaxBudgetCents`), `internal/provider/claude.go` (`--max-budget-usd`), `internal/runner/loop.go`
- Test: `internal/settings`, `internal/provider/claude_test.go`, `internal/server` claim test, `internal/runner/loop_test.go`

**Interfaces:**
- Consumes: Task 6 (the control plane knows the mode).
- Produces: settings `api_budget_per_run_cents` (5 to 2000, default 100) and `api_budget_per_day_cents` (0 to 100000, default 500; 0 means automatic runs off in key mode), group "API key", env `REMEDY_API_BUDGET_PER_RUN_CENTS`, `REMEDY_API_BUDGET_PER_DAY_CENTS`; the claim carries `max_budget_cents` only while the runner reports `api_key`; in key mode the runner adds `--max-budget-usd <cents/100 with two decimals>`, using 100 cents when the claim has none; in subscription mode the flag is never added.

- [ ] **Step 1: Write the failing tests:** both settings validate their ranges and default; the claim of a run carries the value in key mode and omits it in subscription mode; `Command` has `--max-budget-usd 1.00` for 100 and `0.05` for 5, and no such flag in subscription mode or with `MaxBudgetCents == 0` in subscription mode; a key-mode `Spec` with 0 uses 100.
- [ ] **Step 2: Run them.** Run: `go test ./internal/settings ./internal/provider ./internal/server ./internal/runner -run 'Budget' -v`. Expected: FAIL.
- [ ] **Step 3: Implement.** What the CLI does when the budget is hit (exit code, result subtype) is unverified (spec 5.1): record whatever happens as it is, and let Task 12 settle it.
- [ ] **Step 4: Run** `go test ./internal/settings ./internal/provider ./internal/server ./internal/runner -race`. Expected: PASS.
- [ ] **Step 5: Commit.** `feat: a run in key mode carries a per-run budget`

### Task 8: The daily budget

**Files:**
- Modify: `internal/store/diagnosis.go` (`StartParams`, `checkAutomatic`, a new `ErrOverBudget`), `internal/responder` (the caller fills the new `StartParams` fields), `internal/server/responder.go` (`getLimits`), and the wording of the refusal in the thread, next to the other refusals of automatic diagnosis
- Test: `internal/store/diagnosis_test.go`, `internal/store/concurrency_test.go`, `internal/server/responder_test.go`

**Interfaces:**
- Consumes: Task 5 (`runs.auth_source`, `cost_usd`), Task 7 (the settings).
- Produces: `StartParams.EnforceBudget bool` and `StartParams.DayBudgetCents int`; `checkAutomatic` sums `cost_usd` of runs with `auth_source = 'api_key'` and `finished_at` in the last 24 h inside the transaction (after `lockDiagnosis`) and returns `ErrOverBudget` at or over the budget (0 refuses every automatic start); a manual start is never refused; `GET /api/limits` gains `apiSpendLast24hCents` and `apiBudgetPerDayCents` (both omitted when the runner does not report key mode).

- [ ] **Step 1: Write the failing tests:** below, at and over the budget (automatic refused, manual allowed); a budget of 0; runs older than 24 h and subscription runs do not count; `EnforceBudget` false ignores the sum; two automatic starts racing with the budget one cent above the first run's cost: exactly one succeeds (the same pattern as the daily diagnosis limit's test); the limits view omits the fields in subscription mode. The thread sentence for the refusal says the day's spend and the budget in dollars.
- [ ] **Step 2: Run them.** Run: `go test ./internal/store ./internal/server -run 'Budget|Limits' -v`. Expected: FAIL.
- [ ] **Step 3: Implement.** The responder passes `EnforceBudget` from the runner's reported mode at the moment of the start.
- [ ] **Step 4: Run** `go test ./internal/store ./internal/server ./internal/responder -race`. Expected: PASS (the concurrency tests are slow; do not run `-race ./internal/server` more than once).
- [ ] **Step 5: Commit.** `feat: automatic runs stop at a daily budget while the runner bills a key`

### Task 9: Showing it

**Files:**
- Modify: `web/src/components/setup/RunnerSection.tsx`, `web/src/runnerstatus.ts` (+ test), `web/src/api.ts`, `web/src/thread.ts`, `web/src/runview.ts` (+ tests), the limits display in `web/src/components/setup/SettingsSection.tsx` / `setup.ts`

**Interfaces:**
- Consumes: Tasks 5, 6, 8 (`authMode`, `authSource`, the limits fields).
- Produces: pure functions tested with `node --test`: `modeChip(authMode)` returns "Billed to your subscription", "Billed to your API key (Console)" or "Billing mode unknown"; in key mode the login chip reads "Key present" and the "Log in from here" button is not offered; `billedTo(run)` returns "your subscription", "your API key" or nothing; `costLine(run)` returns "about $0.12 (the CLI's estimate)" only for `authSource === 'api_key'`; `spendLine(limits)` returns "about $1.20 of $5.00 today (the CLI's estimate; your invoice decides)" when the fields are present.

- [ ] **Step 1: Write the failing tests** for the four functions, including: a subscription run with `costUsd: 0.4` shows no money anywhere; an unknown mode shows the login button as today.
- [ ] **Step 2: Run them.** Run: `cd web && npm test`. Expected: FAIL.
- [ ] **Step 3: Implement** the functions and wire them into the runner card, the run page and the thread.
- [ ] **Step 4: Run** `cd web && npm test && npm run lint && npm run build`; then look at Setup in a browser against a local control plane with a fake runner status in both modes (CLAUDE.md, "a throwaway server for a browser check"), and note it in the pull request.
- [ ] **Step 5: Commit.** `feat: the UI shows how the runner is billed and what a key-billed run cost`

### Task 10: The vendor's key prefix in `redact`

**Files:**
- Modify: `internal/redact/redact.go`, `internal/redact/redact_test.go`

**Interfaces:**
- Produces: `redact.Redact` replaces `sk-ant-` followed by 20 or more token characters (`[A-Za-z0-9_-]`) with `[REDACTED:anthropic-key]`; this covers text that reaches a prompt (the runner's scrub covers what it posts).

- [ ] **Step 1: Write the failing tests:** a key in a log line, in JSON, two keys in one text, `sk-ant-` alone (kept), a short `sk-ant-abc` (kept), the existing patterns unchanged.
- [ ] **Step 2: Run them.** Run: `go test ./internal/redact -v`. Expected: FAIL.
- [ ] **Step 3: Implement.** The prefix and the length are the ones in the spec's open point 3 ("the current key format"): confirm them against the vendor's documentation when you write the test, and record the source in a comment.
- [ ] **Step 4: Run** `go test ./internal/redact ./internal/prompt -race`. Expected: PASS.
- [ ] **Step 5: Commit.** `feat: redact the vendor's API key format from text that reaches a prompt`

### Task 11: The chart

**Files:**
- Modify: `deploy/chart/values.yaml` (`runner.auth`), `deploy/chart/templates/runner-statefulset.yaml`, `deploy/chart/templates/validate.yaml`, `deploy/chart/README.md`
- Create: `deploy/runner_auth_test.go`

**Interfaces:**
- Consumes: Task 2 (`REMEDY_AUTH_MODE`).
- Produces: `runner.auth.mode: subscription` (default) or `apiKey`; with `apiKey`, `runner.auth.apiKeySecret: {name, key}` (both required): the runner container gets `REMEDY_AUTH_MODE=api_key` and `ANTHROPIC_API_KEY` from `secretKeyRef`; no other container and no other template references the Secret; any other `mode` or an `apiKey` without the Secret fails the render naming the value.

- [ ] **Step 1: Write the failing tests:** the default renders no `REMEDY_AUTH_MODE` change and no `ANTHROPIC_*`; `apiKey` renders the two variables on the `runner` container only (not on `install-cli`, not on the server, not on the hook or the CronJobs: walk every container of every workload); the Secret appears exactly once in the whole render; `mode: apikey`, a missing `name` and a missing `key` fail; the Secret value is never a chart value (a `value` next to `name` is refused like the other secret-like values); `runner.enabled=false` with `mode: apiKey` renders without error and without the variable.
- [ ] **Step 2: Run them.** Run: `REMEDY_REQUIRE_HELM=1 go test ./deploy -run 'RunnerAuth' -v`. Expected: FAIL.
- [ ] **Step 3: Implement.** Add `runner.auth` to `lint-values.yaml` is not done (the lint values stay the stock ones); a second values file `ci/apikey-values.yaml` is rendered by `chart-check`.
- [ ] **Step 4: Run** `make chart-check`. Expected: PASS.
- [ ] **Step 5: Commit.** `feat: the chart can give the runner an API key from a Secret`

### Task 12: Documents, and the real run (gate: the maintainer)

**Files:**
- Create: `docs/runbook/api-key-mode.md`, `docs/research/api-key-mode-real-run.md`
- Modify: `README.md` (the two modes, in the agents sentence and in "Incidents and diagnoses"), `CLAUDE.md` ("Current state", the `--bare` rule reworded to "never in subscription mode", `REMEDY_AUTH_MODE` and the two budget variables, the trust-boundary paragraph), `docs/design.md` (2.1: "in key mode the key never leaves the runner"; section 4: the rows of spec section 11 for the key), `docs/runbook/install.md` (a short pointer)

**Interfaces:**
- Consumes: everything above.

- [ ] **Step 1: The runbook** `api-key-mode.md`: when to choose key mode (unsure about the terms, 24/7, the README's recommendation), creating the Secret (`kubectl create secret generic` from `read -rs`, never on a command line), the values, switching modes (a chart value and a restart, never from the UI), what Setup shows, the budgets and what they do, the rollout order of Review focus 5, and what a leaked key means (revoke it in the Console; it is readable by anyone with `kubectl exec` on the runner pod, as is the subscription login).
- [ ] **Step 2: The other documents,** worded so that nothing claims more than the code does; the sentence about the terms of use in the README waits for the maintainer's reading of the primary text (spec open point 1).
- [ ] **Step 3: Gate: ask the maintainer** whether to run the real CLI with a key now (it costs money on their key). The run settles spec section 5.1: which value `apiKeySource` has with a key; whether a stored subscription login or the key wins without `--bare`; what a run looks like when `--max-budget-usd` is hit; what `claude auth status` says with only a key; the current key format; whether the scrub catches what the CLI prints on a rejected key. Use a key with a low spending limit, one trivial run, one run with a budget of 0.05 that is made to exceed it, one run with a deliberately wrong key.
- [ ] **Step 4: The record** `docs/research/api-key-mode-real-run.md` with the date, the CLI version, each result as a shape (never a key, never a full event), what differs from the spec, and the changes to Tasks 4, 7 and 10 that follow. If a result contradicts a guard (for example `apiKeySource` is `none` with a key), fix the guard in a follow-up pull request before key mode is called done; until then the README says key mode is implemented and its real run is pending.
- [ ] **Step 5: Run** `make check`; open the pull request(s); CLAUDE.md's "Current state" says what was run and what was not.
- [ ] **Step 6: Commit.** `docs: the runbook for API-key mode and the record of its first real run`

---

## Self-review

- **Spec coverage (section 5):** 5.2 (Tasks 1, 2, 3, 10, 11); 5.3 (Tasks 4, 5, 6, 9); 5.4 (Tasks 7, 8, 9); 5.5 `--bare` (Global constraints, Task 12 reword); 5.6 (`EnvFor` takes a mode; nothing offered). Section 8's failure rows: no key (Task 2), invalid key and `apiKeySource` mismatch (Tasks 4, 12), budget reached (Tasks 7, 8, 12). Section 9's tests: the `EnvFor` table, the fake CLI's `apiKeySource` values, the scrub inside JSON, the render test that only the runner references the Secret, the budget under concurrency, "no money in the view of a subscription run". Section 12: Task 12. Section 13 points 3 and 4: Task 12 (verified items) and Task 9 (the "billed to" line sits next to the run and in the thread; Today's digest and the activity feed are not changed).
- **Calls made here, open to change:** cents instead of USD decimals; `ReasonBillingMismatch` as a new failure reason; an `init` without the field is accepted in subscription mode and refused in key mode; the budget is read at the moment of the start from the runner's reported mode.
- **Not planned on purpose:** a UI switch for the mode (spec decision), the quota display, other providers, `--bare`.
- **Names across tasks:** `Mode`/`ModeSubscription`/`ModeAPIKey`, `EnvFor`, `Loop.Mode`, `Outcome.AuthSource`, `Run.AuthSource`, `ReasonBillingMismatch`, `Claim.MaxBudgetCents`, `Spec.MaxBudgetCents`, `Spec.OnInit`, `StartParams.EnforceBudget`, `StartParams.DayBudgetCents`, `ErrOverBudget`, `appliedMigrations` are used with these spellings in every task that names them.
