# Settings, cycle 3: settings in the database

Status: proposed 2026-10-14. Implementation plan: not written yet.
Parent documents: [`2026-10-09-hosts-and-skills-design.md`](2026-10-09-hosts-and-skills-design.md) (the umbrella: sections 2, 6 and 11 are binding here),
[`2026-10-04-phase-2c-cluster-design.md`](2026-10-04-phase-2c-cluster-design.md) (the namespace allowlist and the write identity),
[`2026-10-02-phase-1-detect-and-diagnose-design.md`](2026-10-02-phase-1-detect-and-diagnose-design.md) (the limits of automatic diagnosis).

## 1. Goal and scope

The maintainer changes the limits and rules Remedy works with in the Setup page, and the change takes effect without a restart. The environment stays what it is today, the
**default**; the page says for every value where it comes from.

Six values move: the poll interval, the three limits of automatic diagnosis, the namespace allowlist of the cluster actions and the run timeout. Everything that is a secret or a
listener stays bootstrap (Helm and the environment) and is shown, if at all, as a read-only fact.

### Non-goals

- **Secret settings.** There is none in this cycle, so there is no sealing code. The Alertmanager token of cycle 4 brings it, with the sealing the host credentials use.
- **The Alertmanager keys** (URL, token, severities, matcher rules). Nothing reads them yet; cycle 4 adds them to the registry when it adds the code that uses them.
- **An approval expiry.** There is none in the code (the gatekeeper has only a grace period for a call nobody waits for); inventing one is not part of moving settings.
- **`REMEDY_GITHUB_API_URL`.** It is a test and enterprise switch; it stays in the environment and is shown as a fact.
- **The other runner values** (`REMEDY_CLAUDE_BIN`, `REMEDY_CLAUDE_MODEL`, `REMEDY_WORKSPACES`, the status address). They describe the runner's machine, not a policy.
- **The cluster token files and the cluster address.** The write identity is minted by a CronJob and expires by design (umbrella, section 6); a token in the database would give that up.
- **A history of changes** beyond the activity entry, and export or import of settings.

## 2. Decisions

| Topic | Decision |
|---|---|
| Resolution | **Database, then environment, then default.** The source of the winning value is `set here`, `environment` or `default`. `DELETE` removes the row and the value falls back to the environment or the default. |
| Where values live | A new package `internal/settings` with a **registry** (a fixed list, in code) and a **reader**. The registry is the only place that knows a key's type, bounds, environment name and default. |
| One set of rules | The bounds that `internal/config` checks today (at least 10 s, 0 to 20, and so on) move into the registry. The environment at start-up and `PUT` use the same check and the same error text. The existing config tests stay and must pass unchanged. |
| Reading | The reader holds an **atomic snapshot** of the resolved values. It is built at start-up and rebuilt after every committed `PUT` or `DELETE`, under a mutex. Consumers never touch the database to read a setting, so the single connection is not loaded and no `inTx` can deadlock on it. The control plane is one process, so the snapshot is never stale. |
| Live | **All six values take effect without a restart**, each at its own point (section 4). A running run keeps the timeout it was claimed with. |
| Allowlist | The namespace allowlist is live, too. The action tools are registered whenever a write token file is set, and an **empty list refuses every action**; the start-up warning "token file set but no namespaces" moves from the cluster configuration to the app, which logs it only when the list is empty at start-up. The cluster RBAC stays the hard limit: the UI can only release namespaces in which the write identity has a role. The check stays twice, before the approval is asked for and when the action runs, both against the current list. |
| Reaper | The reaper's threshold for a run without gatekeeper access becomes `max(15 min, run_timeout + 5 min)`, read per sweep, so a long run timeout cannot be overtaken by the reaper. |
| Audit | Every `PUT` and `DELETE` writes an `activity` entry (kind `setting_changed`) **in the same transaction**: the key, the old and the new value and their sources. |
| Input | Durations are entered as Go durations (`15m`, `90s`), exactly as in the environment; numbers as numbers; the allowlist as a comma-separated list. The server checks; the UI shows its message. |
| Confirmation | Saving the namespace list goes through `ConfirmButton` (it is the safety boundary of the actions). The other values save with one click. |
| Bootstrap facts | `GET /api/settings` lists the listener addresses, the GitHub API URL and whether the cluster read and write sides are set up, as **read-only facts**. The admin password, the runner token and the master key never appear, not even as "set". |

## 3. Components

| Part | Where | Does |
|---|---|---|
| Registry | `internal/settings` (new) | `Key`, `Spec` (type, bounds, environment name, default, label, effect text), `Parse(spec, text) (Value, error)`, the six entries. Pure. |
| Reader | `internal/settings` | `Settings` with `Get`, `Duration(key)`, `Int(key)`, `List(key)`, `All()` (value, source, problem). `Reload` rebuilds the snapshot from the environment map and the rows. |
| Store | `internal/store` | Migration 012 (`settings`), `ListSettings`, `SetSetting` and `DeleteSetting` (each with its activity entry in the transaction), `KindSettingChanged`. |
| Config | `internal/config` | Reads the environment through the registry; keeps its public fields as the *environment layer*. The warning "write token file set but no namespaces" is dropped here (the allowlist may come from the database). |
| Consumers | `internal/app`, `internal/poller`, `internal/responder`, `internal/gatekeeper`, `internal/kube`, `internal/reaper` | Take a function instead of a value (section 4). |
| Runner path | `internal/run`, `internal/server`, `internal/runner`, `cmd/remedy-runner` | `Claim.RunTimeoutSeconds`; the runner uses it, `REMEDY_RUN_TIMEOUT` is the fallback. |
| Chart | `deploy/chart` | `runner.runTimeout` also sets `REMEDY_RUN_TIMEOUT` on the control plane pod (section 5); a render test. |
| Server | `internal/server` | `GET /api/settings`, `PUT /api/settings/{key}`, `DELETE /api/settings/{key}`; `GET /api/limits` reads from the reader. |
| UI | `web/` | `SettingsSection` replaces `LimitsSection`; the pure module `web/src/settings.ts`. |

## 4. The consumers

| Value | Consumer | Reads it |
|---|---|---|
| `poll_interval` | `poller.Poller` | `Interval` becomes `func() time.Duration`; the loop reads it before each wait and resets the ticker when it changed. |
| `diagnose_cooldown`, `diagnose_max_per_incident`, `diagnose_max_per_day` | `responder.Responder` | `Limits` becomes `func() store.DiagnosisLimits`, read at every automatic start. `StartDiagnosis` still receives the limits as a parameter and checks them inside its transaction; it does not read the reader. |
| `k8s_write_namespaces` | `kube.Writer`, `gatekeeper` cluster action tools, `server.Cluster` | A `func() []string`; `Writer` refuses a namespace outside the current list, the tools check before the approval and again at execution. |
| `run_timeout` | the claim handler, `runner.Loop` | The control plane puts `run_timeout_seconds` into the claim; the runner uses it for that run. |
| reaper threshold | `reaper.Reaper` | A `func() time.Duration`, derived as in section 2. |

`GET /api/limits` keeps its shape (it also carries the use of the last 24 hours); its values now come from the reader.

## 5. The registry

| Key | Type | Environment | Default | Bounds |
|---|---|---|---|---|
| `poll_interval` | duration | `REMEDY_POLL_INTERVAL` | 60 s | at least 10 s |
| `diagnose_cooldown` | duration | `REMEDY_DIAGNOSE_COOLDOWN` | 15 m | at least 1 m |
| `diagnose_max_per_incident` | whole number | `REMEDY_DIAGNOSE_MAX_PER_INCIDENT` | 3 | 0 to 20 |
| `diagnose_max_per_day` | whole number | `REMEDY_DIAGNOSE_MAX_PER_DAY` | 20 | 0 to 200; 0 turns automatic diagnosis off |
| `k8s_write_namespaces` | list | `REMEDY_K8S_WRITE_NAMESPACES` | empty | `kube.ParseNamespaces` |
| `run_timeout` | duration | `REMEDY_RUN_TIMEOUT` (runner) | 10 m | at least 10 s |

`REMEDY_RUN_TIMEOUT` is read by the runner today, and the chart sets it only on the runner pod (`runner.runTimeout`). The control plane now reads it too, **only as the
environment layer of this key**, and the chart passes `runner.runTimeout` to the control plane pod as well (a change of the chart with a render test), so the Setup page shows
what the runner would use. Where the two differ, the control plane's claim wins and the runner's own value is the fallback for a claim without the field.

## 6. Data model (migration 012)

```sql
CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,   -- JSON
    updated_at TEXT NOT NULL    -- nine fractional digits, like every timestamp
);
```

The migration changes no existing table, so it needs no migration test on an older database; a store test covers the new table.

## 7. API and UI

**API** (admin session and `X-Remedy-CSRF`, like every admin route):

- `GET /api/settings`: for each key its label, type, current value, source, environment name, default, bounds, the effect text and an optional `problem`; plus the bootstrap facts.
- `PUT /api/settings/{key}` with `{"value": ...}`: 404 for an unknown key, 400 with a readable message for a value outside the bounds, 500 when the row could not be written (the
  snapshot is then unchanged).
- `DELETE /api/settings/{key}`: removes the override.

**UI.** `SettingsSection` replaces `LimitsSection` on the Setup page, in four groups: *Polling*, *Diagnosis limits*, *Cluster actions* and *Runs*. Each row shows the label, the
value, the source as a badge and a field to change it; "Reset to environment" (or "Reset to default") shows while the value is set here. The use of the last 24 hours stays as the
bar under the diagnosis limits. The bootstrap facts follow as a calm list without fields. The page follows the conventions of the UI: a busy button keeps its focus
(`aria-disabled`), the focus returns to the heading when a control unmounts, the live region announces by key. The logic (the text of a row, the badge, the pre-check of an input)
is in `web/src/settings.ts` and is tested with `node --test`.

## 8. Failure

- An invalid environment value stops the start, as today, now through the registry.
- A row with an unknown key or a value that no longer passes (after a downgrade, say) is **ignored at start-up** with a warning in the log. `GET /api/settings` gives that key a
  `problem`, the Setup page shows it on the row, and the value falls back to the environment or the default.
- A failed write leaves the snapshot as it was; it is rebuilt only after the commit.
- A runner that gets a claim without `run_timeout_seconds` (an older control plane) uses `REMEDY_RUN_TIMEOUT`.

## 9. Tests

Written first, in this order of weight:

1. **Registry**: a table of valid and invalid values per key. The existing tests in `internal/config` (`poll_test`, `diagnose_test`, `runtimeout_test`, `cluster_test`) pass unchanged.
2. **Reader**: the order default, environment, database and the source named for each; the snapshot is rebuilt after `PUT` and `DELETE`; an invalid row is ignored and reported;
   a failed write changes nothing.
3. **Consumers**: the poller takes a new interval in the next cycle; the next automatic start uses new limits; a changed allowlist is used before the approval **and** at execution
   (a namespace removed between the two is refused); an empty list refuses every action; the claim carries the timeout; the reaper threshold follows `max(15 min, run_timeout + 5 min)`.
4. **API**: CSRF, 404, the 400 texts, the activity entry with the old and the new value in the same transaction, and that no bootstrap secret is in any answer.
5. **Web**: `settings.ts` with `node --test`.
6. **Chain** (`internal/app`): change a setting through the API and see the consumer act on it.
7. **Browser**: the Setup page with real clicks (the confirm button of the allowlist arms on the first click and disarms on blur).
8. **Live** (`kindwrite`): change the allowlist through the API and see an action in a removed namespace refused.

## 10. Risks

| Risk | Handling | Retrofit |
|---|---|---|
| A click changes a safety limit (the diagnosis limits, the allowlist). | The UI is behind the admin password only, as everything else (design, section 4); every change is an activity entry; the allowlist asks for a confirmation; the cluster RBAC bounds the allowlist. | TOTP or OIDC, as for every admin action. |
| The environment as a default hides where a value comes from. | Every row shows its source. | none needed |
| A long `run_timeout` lets a stuck run hold the runner for long. | The reaper threshold follows it; a run without gatekeeper access has no heartbeat, so the timeout is the only limit. The Setup page states this next to the field. | A ceiling, if it happens. |
| A snapshot that differs from the table after a failed rebuild. | The rebuild reads the table after the commit; if it fails the handler answers 500 and logs it, and the next successful `PUT`, `DELETE` or restart builds it again. | none needed |

## 11. Documents to change with the implementation

`docs/design.md` (section 2.7), the sentence "Configuration: environment variables. No UI for it." in the 2D spec, sections 6 and 13 of the umbrella spec (cycle 3 built; the open
points answered), `CLAUDE.md` (Current state, and the line on the environment variables: they are defaults), the README (the paragraphs that name `REMEDY_POLL_INTERVAL`,
`REMEDY_RUN_TIMEOUT` and the three `REMEDY_DIAGNOSE_*` variables) and the chart (`runner.runTimeout` also reaches the control plane; its comment in `values.yaml` says that these
variables are defaults which the Setup page can override).

## 12. Open points for the plan

- The exact `Reload` signature and how `internal/app` hands the environment map to it.
- Whether the poller resets its ticker or re-arms a timer per cycle.
- The wording of the effect texts and of the problem messages.
