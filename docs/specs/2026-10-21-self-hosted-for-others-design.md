# Remedy for other operators, cycle 7 (self-hosted, API-key mode, generic webhook)

Status: approved by the maintainer for merge on 2026-10-21; the defaults marked **(call)** are the author's and stay open to change in the plans, and the terms of use must be re-read in their primary text before 7b (open point 1). Three plans follow it: 7a (a stranger can install it), 7b (API-key mode), 7c (the webhook source).
Parent documents: [`../design.md`](../design.md) (1 Purpose, 2.1, 2.3, 2.7, 4 Accepted risks),
[`../research/subscription-cli-usage.md`](../research/subscription-cli-usage.md) (the terms-of-use constraints; on conflict the research wins until it is updated),
[`2026-10-09-hosts-and-skills-design.md`](2026-10-09-hosts-and-skills-design.md) (the series this follows; cycle 6, the home-server drill, continues separately and is not blocked by this),
[`2026-10-16-alertmanager-4-design.md`](2026-10-16-alertmanager-4-design.md) (the signal runner and the outage responder the webhook reuses),
[`2026-10-06-kubernetes-deployment-design.md`](2026-10-06-kubernetes-deployment-design.md) (the two listeners and the chart).
**Not legal advice.** Sections 4 and 11 quote the research file; the vendor's legal page was **not** read again for this draft (no network was used), so the quotes are only as current as the research of 2026-10-02.

## 1. Goal and scope

Until now Remedy is one maintainer's tool for one lab. The maintainer decided that **other people may run their own installation for their own hosts**. Each operator has their own login or key, their own cluster, their own data; nothing is shared and the maintainer runs nothing for anyone. Three things stand between today's state and that:
(1) the repository, the chart and the docs assume one lab, and a stranger cannot legally or practically install it (section 7); (2) the only way to pay for runs is a subscription login, which suits some operators and not others (section 5); (3) Remedy hears about trouble only from GitHub and from an Alertmanager it polls (section 6).

### Non-goals of this series

- **Teams, companies, several users, roles, OIDC.** Reason, from the research: OAuth auth "is designed to support ordinary use", the limits "assume ordinary, individual usage", and it is forbidden to "route requests through subscription credentials on behalf of others". One subscription login must not serve several people, so Remedy has no users. Any later team mode would have to run on API keys (section 5 is its precondition, not its design).
- **Running Remedy as a service for others** (design 1, stays). **Notifications** (the Home Assistant notify of design 2.9 is not built and is not generalised here). **Docker Compose or other install paths**: the chart is the one supported install. **The Argo CD source** (postponed since 2D). **Other Git hosts** than GitHub.
- **Bedrock, Vertex and other third-party model providers**, and a gateway or base URL for the API: not offered (section 5.6).
- A mapping language or a plugin system for webhooks (section 6.5), a Prometheus pull source, the display of the subscription's quota (`rate_limit_event` is still not parsed).

**What keeps a later team mode possible at no cost (kept, not built).** The admin API passes one `session` middleware (`internal/server/session.go`), so a per-user check would replace one function; approvals and activity entries have no decider column, so adding an actor later is an additive migration; no table gets an owner column now, because one installation is one operator's. Webhook tokens and the key mode are per installation, never per user. Nothing else is done for it.

## 2. Decisions

| Topic | Decision |
|---|---|
| Audience | Individual operators, each with their own login or key, each running their own installation. A fork or a second installation is the way to "more than one". |
| Login modes | Per installation, chosen by the operator at install time: **`subscription`** (today, the default) or **`api_key`** (new). Not switchable from the UI (**call**: a click must not change who is billed; the chart value and the runner's environment are the one place). |
| The key | Lives only in the **runner's environment** (a Secret mounted into the runner pod). The control plane never receives it, stores it, or has a column for it. Never in a log, an event, an API answer or an error; the runner scrubs it from every line it posts (section 5.2). |
| The guarantee | The allowlist of `provider.FilterEnv` stays. The mode adds one explicit, tested exception: in `api_key` mode `ANTHROPIC_API_KEY` (only that variable) passes and `CLAUDE_CODE_OAUTH_TOKEN` does not; in `subscription` mode a key in the environment is dropped as today, **and now reported** (a warning in the log and on Setup). `ANTHROPIC_AUTH_TOKEN` and `ANTHROPIC_BASE_URL` stay banned in both. |
| Visibility | Setup shows the mode and a run shows how it was billed. The run's own record comes from the CLI's report (`system/init` `apiKeySource`), not from Remedy's belief (section 5.3). |
| Mismatch | A run whose `apiKeySource` contradicts the mode is **stopped** and fails with a fixed reason (**call**: fail closed on billing, like the write token on expiry). |
| Cost control | In `api_key` mode: a per-run cap (`--max-budget-usd`) and a daily budget for automatic runs; both are settings with defaults (**call**: 1.00 and 5.00 USD). The daily diagnosis counts stay. |
| `--bare` | Not used in either mode (section 5.5). |
| Webhook shape | A **new inbound source**, not a polling one: `POST /webhook/v1/{endpoint}` with a per-endpoint bearer token, a strict normalised JSON v1, on a **third listener** that is off unless `REMEDY_WEBHOOK_ADDR` is set (section 6). |
| Webhook identity | New incident source `webhook`; key is `{endpoint}/{sender's key}`. It needs the `source` CHECK of `incidents` widened: a rebuild in the manner of migration 008. It cannot reuse `alertmanager`: the poller's `ResolveAbsent` would resolve every webhook incident. |
| Webhook resolution | An explicit `resolved` status, **and** a per-endpoint silence limit (`resolve_after`, default 24 h, `0` = explicit only). |
| Webhook diagnosis | Off by default per endpoint (**call**): a leaked token must not be able to spend the operator's quota or money. A click diagnoses; the existing daily limits apply when it is on. |
| Generic pull | Rejected for this series (6.8). |
| Lab values | After 7a, nothing in code, UI copy, chart defaults or the README is a value of the maintainer's lab. Those live in the cycle 6 runbook and in fixtures, and a script checks it (section 7). |
| Order | 7a stranger-ready, 7b API-key mode, 7c webhook (section 10). |

## 3. Components

| Piece | Where | What |
|---|---|---|
| Auth mode | `internal/provider` (`env.go`), `internal/config` (runner) | `Mode` (`subscription`, `api_key`), `EnvFor(mode, parentEnv)`; `REMEDY_AUTH_MODE`; refusal to start in `api_key` without a key. |
| Runner guard and scrub | `internal/runner` | Reads `apiKeySource` from the `init` event, stops a contradicting run, scrubs the key from every line and from the outcome, adds `--max-budget-usd` from the claim, reports the mode. |
| Run record | `internal/store` (migration 017) | `runs.auth_source`, set at `finish`. A column added in place; no table is rebuilt. |
| Budget | `internal/settings`, `internal/store` (`StartDiagnosis`) | Two settings, the sum of `cost_usd` of key-billed runs in 24 h, checked inside the transaction of the automatic start. |
| Webhook endpoints | `internal/webhook` (new), `internal/store` (migration 018) | The handler, the strict decode, rate limits, the sweeper; table `webhook_endpoints`; `incidents.source` widened. |
| The listener | `internal/server`, `internal/app`, `cmd/remedy-server`, `internal/config` | `NewWebhook(d)` returns a third handler; a third `http.Server` with real timeouts. |
| Host mapping and bounds | `internal/signals` | `mapHost` and the bounding helpers of `alertmanager.go` become exported pieces both sources use; no behaviour change for Alertmanager. |
| Responder | `internal/responder`, `internal/prompt`, `internal/server/responder.go` | Every place that switches on `SourceAlertmanager` also takes `webhook` (list in 6.7); `Outage` states the real source. |
| Chart | `deploy/chart` | `runner.auth`, `webhook.*`, defaults without a lab (7). |
| Docs | section 12 | |

## 4. Terms of use, in this series

The research (2026-10-02) says: OAuth auth is "designed to support ordinary use of Claude Code and other native Anthropic applications"; "Developers building products or services (also with the Agent SDK) should use API key auth"; it is forbidden to "collect, store or intermediate Claude.ai credentials or session tokens, or to route requests through subscription credentials on behalf of others"; still allowed is "an end user signing in to the **unmodified** Claude Code binary with their own subscription"; "Advertised usage limits for Pro and Max plans assume ordinary, individual usage". Consequences, none of which is a legal opinion:

1. Each operator logs in on **their own** runner, with their own account. No installation may ship, collect or relay a login of anyone but its own operator, and the relay of the Setup page ([`2026-10-21-ui-login-relay-design.md`](2026-10-21-ui-login-relay-design.md)) is part of the product: since 2026-10-22 it has no switch and is available whenever the runner is in subscription mode (in another mode the runner starts no relay driver; the server learns the runner's mode, and the page stops offering the button, only with 7b). The primary text of the terms decides whether the project may be offered to others as it is (open point 1). The login-directory guard (`scripts/check-login-dir-untouched.sh`) rule holds for every installation, not just the maintainer's.
2. Remedy stays "a tool that starts the unmodified binary". Whether a public tool that drives the CLI for many strangers is "building a product" in the vendor's sense is **not settled by the research**. This is why `api_key` mode exists and why the README recommends it to anyone who is unsure or who runs 24/7 (the research already lists "API-key fallback as a documented option" as the mitigation of the 24/7 risk).
3. One runner serves one operator. The docs say so; there is no sharing feature (section 1).
4. The maintainer reads the primary text again before the README goes public (section 13, open point 1).

## 5. API-key mode

### 5.1 What I verified, and what not

From the code: `provider.FilterEnv` is an allowlist applied to `os.Environ()` of the runner (`cmd/remedy-runner/main.go`, `internal/runner/loop.go`); `CLAUDE_CODE_OAUTH_TOKEN` is already allowed through (a `setup-token` login works today); `total_cost_usd` is parsed (`provider/claude.go`) and stored (`runs.cost_usd`) but shown nowhere in the UI; the control plane stores run events **unredacted** (`server/runnerapi.go` `postEvent`) and `internal/redact` has no rule for an Anthropic key; the login check is `claude auth status --text` (`provider/login.go`); `rate_limit_event` is not parsed. From the spike record: `system/init` carries `apiKeySource` (observed `"none"` on the subscription login) and `total_cost_usd` is "a client-side estimate, not a charge".
From `claude --help` (version **2.1.306** on this machine, newer than the pinned 2.1.292; I ran `--help` only): `--bare` says "Anthropic auth is strictly ANTHROPIC_API_KEY or apiKeyHelper via --settings (OAuth and keychain are never read)", skips hooks, plugin sync, auto-memory, keychain reads and CLAUDE.md discovery, and takes MCP only from `--mcp-config`; `--max-budget-usd <amount>` exists and "only works with --print"; Bedrock/Vertex/Foundry "use their own credentials".
**Not verified** (a real run settles them, 7b): which value `apiKeySource` has with a key; whether `claude -p` without `--bare` prefers `ANTHROPIC_API_KEY` over a stored subscription login; what a run looks like when `--max-budget-usd` is hit (exit code, result subtype); whether `claude auth status` says anything useful with only a key; the current key format; whether `--bare` combines with `--restricted`, `--tools` and `--json-schema`.

### 5.2 How the key reaches the runner

Chart: `runner.auth.mode: subscription | apiKey` and, for `apiKey`, `runner.auth.apiKeySecret: {name, key}`; the Secret is the operator's (`kubectl create secret`, SealedSecret, whatever they use), mounted as `ANTHROPIC_API_KEY` in the **runner container only**; a render test asserts that no server, hook or CronJob template references it. The runner reads `REMEDY_AUTH_MODE`; `api_key` without a non-empty key exits 2 with a fixed message. `provider.EnvFor(mode, env)` replaces the bare `FilterEnv` call in `Command` and `LoginCheck`/`Version` (**call**: one function, both modes, one test table). The key is never in argv or in a file Remedy writes (the MCP config file is the precedent for how little is written; the key needs none).
Scrubbing: the runner knows the exact value, so before it posts any stdout or stderr line, or the outcome's `Result`, it replaces the literal key (and its first and last 6 characters if they appear alone) with `[REDACTED:api-key]`. This is the **only** copy of the logic that can work, because the control plane must not learn the key; `internal/redact` also gets a pattern for the vendor's key prefix (**call**, to verify against the current format) for text that reaches a prompt. Run events stay unredacted otherwise, as today: the agent has `Read`, `Grep`, `Glob` and gatekeeper tools only and no way to print the environment, so the realistic leak is an error line of the CLI, which the scrub covers.

### 5.3 Showing the mode, and the guard

The runner reports `authMode` in `POST /runner/v1/status` (the body is decoded strictly: both sides change together, and an older runner shows "mode unknown"). Setup's runner section gets a chip, "Billed to your subscription" or "Billed to your API key (Console)", read-only with a sentence on how to change it. In `api_key` mode the login chip becomes "key present" (the runner checks that the variable is non-empty, never that it is valid; a bad key fails the first run with a fixed sentence).
Every run records `auth_source` from its own `init` event: `none` is `subscription`, anything else is `api_key`. The runner compares it with its mode as soon as the `init` line is parsed: **subscription mode requires `apiKeySource == "none"`** (observed); **key mode requires it to be something else** (value unverified, so the test is "not none"). A mismatch cancels the run (the context cancel the heartbeat already uses) and finishes it with a fixed reason; this also catches a stored subscription login that wins over the key, and a settings file with an `apiKeyHelper` (`--restricted` ignores user settings, so none is expected).
The thread and the run page say "billed to: your subscription / your API key" next to the run; nothing else on a page changes.

### 5.4 Limits and cost

Today's limits count diagnoses (`diagnose_*` settings, checked in `StartDiagnosis`); they say nothing about money, and ad-hoc and learning runs are not counted at all. In `api_key` mode:

- **Per run:** the claim carries `max_budget_usd` (next to `run_timeout_seconds`); the runner passes `--max-budget-usd` only in key mode. A runner in key mode with no value in the claim uses its own built-in 1.00 (fail closed on cost). Setting `api_budget_per_run_usd`, 0.05 to 20, default 1.00 (**call**).
- **Per day, automatic runs:** setting `api_budget_per_day_usd`, default 5.00, `0` = automatic runs off (**call**). `StartDiagnosis` sums `cost_usd` of runs with `auth_source = 'api_key'` finished in the last 24 h and refuses an **automatic** start at or over the budget, in the same transaction as the other limits, with a reason the thread shows. A run the operator starts by hand is never refused (as with the diagnosis limits) but the UI shows the day's spend next to the button. The budget is enforced only while the runner reports key mode.
- **UI:** `GET /api/limits` gains `apiSpendLast24hUsd`; Setup shows "about $X of $Y today (the CLI's estimate; your invoice decides)"; a run shows its cost the same way, **only** when `auth_source` is `api_key`. For a subscription run no money is shown anywhere: the figure is not a charge, and showing it would mislead.

### 5.5 `--bare`

The repo forbids `--bare` because it "does not read OAuth credentials" (research) and the help text of 2.1.306 agrees. In key mode it is technically allowed. **Decision (call): not used.** The command line is the one measured in the spike (about 1.4k to 4.5k tokens per trivial run); `--bare` would add a second shape to maintain and test, and whether it combines with `--restricted`, `--tools`, `--mcp-config` and `--json-schema` is unverified. Revisit with a measurement, as an optimisation of key mode only. CLAUDE.md's rule is reworded from "never add `--bare`" to "never in subscription mode".

### 5.6 Other providers

Bedrock, Vertex and Foundry would be a third mode with its own variables (`CLAUDE_CODE_USE_BEDROCK`, `AWS_*`, which are not on the allowlist, so they cannot be used by accident). Not offered; `EnvFor` is shaped so that a mode is one more case. `agy` stays phase 4.

## 6. The generic webhook source

### 6.1 A new trust boundary

Every source so far is **polled**: Remedy dials out, an answer is a complete snapshot, and an absent alert means resolved (`Source.Fetch`, `ResolveAbsent`). A webhook reverses both: a stranger's request arrives, authenticated only by a token, and carries one event. So it does **not** implement `signals.Source` (a snapshot interface); it calls `incident.Engine.ObserveSignal` directly (`Bad` is firing, `Green` is resolved), the same entry the poller ends in. Everything the sender writes is untrusted text and reaches an agent only through `internal/prompt`, bounded and in a data block, as an alert does.

### 6.2 Listener: a third one, off by default

The public port serves the login page and the admin API behind a password; the internal port is open only to the runner pod (the chart's NetworkPolicy). A webhook fits neither: an Ingress to the public port would put the login page on the internet next to the hook, and the internal port's policy admits runner pods only. **Decision (call):** `REMEDY_WEBHOOK_ADDR` (default empty: **no route exists anywhere**, the same fail-closed shape as `REMEDY_INTERNAL_ADDR`; it must differ from both other addresses, validated like the internal one). The listener serves only `POST /webhook/v1/{endpoint}` and `GET /healthz`; the other two muxes never register it. Its `http.Server` has `ReadHeaderTimeout` 10 s, `ReadTimeout` 15 s, `WriteTimeout` 15 s, `IdleTimeout` 60 s and `MaxHeaderBytes` 16 KiB (the two existing servers set only `ReadHeaderTimeout`; a body arrives here from the internet, so a slow body must not hold a connection).
Chart: `webhook.enabled` (false), a `ClusterIP` Service `remedy-server-webhook`, a NetworkPolicy ingress rule from `webhook.from` (**required and non-empty** when enabled, the same `fail` as `networkPolicy.public.from`), and an optional separate `Ingress` (own host, class and TLS) so that an operator can expose `/webhook/v1/` without exposing the UI. A sender inside the cluster (Alertmanager, Grafana) needs no Ingress at all.
**Behind a reverse proxy** the operator needs: TLS terminated at the proxy (the token is a bearer secret; without TLS it crosses the network in clear, acceptable only inside a cluster), the path restricted to `/webhook/v1/`, a body limit as a second line, and source-IP filtering at the proxy if the sender's addresses are known. Remedy deliberately does not trust `X-Forwarded-For` (`clientIP` is the TCP peer, as for the login), so **no limit here is per IP**: behind a proxy every request has the proxy's address, and a per-IP lockout would let an attacker lock out every real sender (the login's five-failures lockout has that property today and is left alone). Limits are per endpoint.

### 6.3 Authentication

Table `webhook_endpoints`: `id`, `name` (`[a-z0-9][a-z0-9-]{0,39}`, unique), `token_hash` (SHA-256 of 256 random bits, the way `run_tokens` does it), `previous_token_hash` and `previous_valid_until`, `enabled`, `auto_diagnose`, `resolve_after`, limits, `created_at`, `last_used_at`. The token is **generated by Remedy**, shown once at creation or rotation, and sent as `Authorization: Bearer ...`. It is never accepted in the URL (proxies log URLs; a tool that cannot set a header cannot use this endpoint, section 13). Hashing, not sealing: Remedy never needs the plaintext back, so it keeps none (unlike the GitHub token, which it must replay). The check is a constant-time compare of hashes. An unknown endpoint, a disabled one and a wrong token all answer the same `401` with the same body, after the same work (a dummy compare for an unknown name). Rotation keeps the previous token valid for 24 h (**call**) so a sender can be changed without a gap. A failed request writes nothing to the database or to `activity`; the endpoint row keeps in-memory counters and the last error, shown on Setup. The admin API for endpoints is `/api/webhooks` (session and CSRF, like hosts), the Setup page gets a "Webhooks" block.

### 6.4 The schema (version 1)

`Content-Type: application/json`, body at most 64 KiB, decoded strictly (`DisallowUnknownFields`, nothing after the object, as the status endpoint does):

```json
{ "version": 1, "key": "disk-full/nas", "status": "firing", "title": "Disk almost full on nas",
  "severity": "warning", "labels": {"instance": "nas"}, "annotations": {"summary": "..."},
  "url": "https://monitor.example/alerts/42", "startsAt": "2026-10-21T08:00:00Z" }
```

`version` must be 1; `key` 1 to 200 printable characters, no control characters; `status` is `firing` or `resolved`; `severity` `critical`, `warning`, `info` or `none` (default `none`); `title` at most 200 after cleaning; `labels` at most 30 (key 64, value 300), `annotations` at most 20 (value 1000); `url` http(s) only, 2000; `startsAt` optional. The bounds and the cleaning (`oneLine`, `redact.Redact` before the cut, keys that redaction would change dropped) are the Alertmanager source's, extracted and shared (section 3), so there is one definition of "bounded alert text". The stored key is `{endpoint}/{key}`; the stored details are the bounded copy.
Why one fixed schema and not adapters or a mapping language: real tools differ (Grafana, Uptime Kuma, Prometheus-based senders), but a free-form mapping (JSONPath, templates) is an injection and complexity surface that decides what becomes an incident. Senders can template their own body (Grafana contact points, Uptime Kuma custom body, `curl`), which the runbook shows with recipes. A fixed, strictly validated `?format=` decoder for a specific tool can come later as its own cycle (section 13).

### 6.5 Mapping onto the engine

`firing` becomes `incident.Observation{Source: "webhook", Key, Title, Severity, Details, HostID, Class: Bad, Conclusion: "firing", URL, AutoDiagnose}`; `resolved` the same with `Class: Green`. The host comes from the same matcher rules as alerts, over the sender's `labels` (so a sender chooses a host only as an alert label does, never by name). `AutoDiagnose` is `endpoint.auto_diagnose` and severity `critical` or `warning` and a `startsAt` (or now) within `alertmanager_auto_max_age`; the engine's `RecentlyDiagnosed` hour still applies. Host rules are read from the store per request through a cache of a few seconds (the poller reads them per cycle).

### 6.6 Idempotency, limits, resolution

- **Idempotency by key:** a repeat of the same body is a touch (`TouchIncidentSignal`); a `resolved` for an unknown or already resolved key is a `200` no-op; a `firing` after a resolve opens a new incident, as for any source. Answer `200 {"result": "opened|updated|resolved|ignored"}`. A sender's retry after a timeout therefore never doubles anything.
- **Limits per endpoint (defaults are a call):** 60 requests per minute with a burst of 120 (`429` with `Retry-After`); at most 30 **new** incidents per hour (`429` for a new key only: updates and resolves are always accepted, so a flood cannot stop a resolve); at most 8 requests in flight on the listener (`503` beyond), because the store has one connection and a queue of webhook writes must not starve the admin UI.
- **Resolution:** explicit `resolved`, or silence: a sweeper (in the poll loop's cadence) resolves unresolved `webhook` incidents whose `last_seen` is older than the endpoint's `resolve_after` (default 24 h, minimum 5 min, `0` = never), reason `expired`, and the thread says "not reported for 24 h", not "fixed". A sender that repeats while firing (Alertmanager's `repeat_interval`, Grafana) keeps the incident alive; a fire-once sender needs a long limit or an explicit resolve. There is no resolution by absence in push.
- **Activity:** an entry only where the engine writes one (open, resolve); a touch or a rejection writes none, so a sender cannot grow the append-only table by repeating itself.

### 6.7 Where the responder must learn the source

`internal/responder/responder.go` `start` (the `switch in.Source`) and `parseAnswer`; `internal/server/responder.go` `responderClaim` (schema, no snapshot); `internal/incident/incident.go` `noun`; `internal/server/incidents.go` `validSourceFilter`; `internal/store/incidents.go` `sources`; the CHECK of migration 008 (new migration, with a test on a database built from `001` to `017` and a row in every table that points at `incidents`, the model of `migrate008_test.go`); `prompt.Outage` (`"Source: Alertmanager"` becomes a fact the control plane writes, `webhook` plus the endpoint name inside a data block); the web source labels. A webhook incident is diagnosed by the outage responder: the same schema, the same tools, the same approvals.

### 6.8 The polling alternative

A generic `Fetch` of a URL that returns JSON would fit `signals.Source` and give resolution by absence for free, with no inbound port. Rejected as the first step (**call**): it needs the mapping language of 6.4 (what is a key, a title, a severity in an arbitrary document), it covers only tools that expose a list of current problems, and it puts another credential per tool into the control plane (the webhook puts one token per endpoint into the sender instead). Two cheap facts to check instead (not verified here): Grafana's embedded Alertmanager and VictoriaMetrics' vmalert may already be readable by the existing Alertmanager source. If so, the runbook says so and 7c needs no pull source.

## 7. A stranger's installation: what the code assumes today

Found by reading, with the fix 7a makes. "Lab value" means something only the maintainer's setup has.

| Where | Assumes | Fix |
|---|---|---|
| Repository root | **No `SECURITY.md`, no `CONTRIBUTING.md`.** (The licence is decided: MIT, 2026-10-21, `LICENSE` merged in #161.) | `SECURITY.md` with a private reporting address; neither file is drafted here. |
| `docs/runbook/homelab-deploy.md:6` and the release record | Images and chart were "expected to be private" and the OCI install under Argo CD is "not proven". Update of 2026-10-21: releases exist (up to `0.8.0`) and the three packages are public (an anonymous `helm show chart` and an anonymous image manifest request worked), so a stranger can pull; the OCI install under Argo CD is still unproven. | A generic `docs/runbook/install.md` (plain `helm install`, a `kubectl create secret`, the one-time login); the runbook's status line updated. |
| `deploy/chart/values.yaml:82-87` | `networkPolicy.public.from` defaults to `kube-system` ("where k3s's Traefik runs"). On ingress-nginx or others the UI is unreachable. | No default (the template already fails on an empty list); the README lists presets (k3s, ingress-nginx, a node CIDR). |
| `deploy/chart/values.yaml` `runner.cli.*`, `deploy/cli-pin.yaml:1` | The CLI pin is required and lives in a values file named for "the homelab". | Ship the pin with the chart as its default (the cli-pin workflow already bumps one file); reword the comment. |
| `deploy/chart/Chart.yaml:3`, `README.md` intro | "an AI operator for a homelab GitOps setup"; "Antigravity CLI" named though only Claude is implemented. | Section 12. |
| `README.md` Alertmanager block | The example is the maintainer's NAS (`instance="ugreen-nas"`). | `instance="myhost"`. |
| `web/src/HostsPage.tsx:68` | "such as your NAS". | "such as a NAS, a VM or a server". |
| `web/src/components/setup/ReposSection.tsx:61` | The error example is `jaydee94/homelab`. | `owner/name`, for example `acme/infra`. |
| `web/src/components/hosts/HostForm.tsx:84`, `AddHostForm.tsx:38` | Examples `nas\\.local`, placeholder `nas`. Harmless. | `myhost` (low). |
| `internal/alertmanager/alertmanagertest/testdata/home-server.json` | Real label values (`ugreen-nas`, `paperless-webserver`) in a public fixture. | Rename in the fixture and the tests that pin them (low). |
| `internal/prompt/*.go`, `internal/gatekeeper/tools_*.go` | "the maintainer" means the operator. Agent-facing text. | **No change (call)**: a prompt edit needs a real run to re-verify; the docs say "operator". |
| `docs/specs/2026-10-09-*` section 4, 10 | `/volume1`, Paperless, `remedy-*` containers of the NAS. | Stays: history. The drill is cycle 6's. |
| Login lock (`internal/auth/auth.go`), `session.go` | UI "reachable only on the LAN or a VPN" with a password only; five failures per TCP peer lock out everyone behind a proxy. | Said plainly in the install runbook; not changed (accepted risk 3). |

**How a stranger avoids relying on lab values.** (1) No default in code, chart or UI copy names a lab thing. (2) The cycle 6 runbook is the only place for the maintainer's values and says it is an example. (3) `scripts/check-no-lab-values.sh` (**call**) greps tracked files outside `docs/specs`, `docs/research`, the cycle 6 runbook and the script itself for a short deny-list (`ugreen`, `paperless`, `jaydee94/homelab`, the maintainer's domains), wired into `make shell-test`. (4) The chart renders and passes `make chart-check` with only the values of a stock cluster, which a render test pins.

## 8. Failure

| Situation | Result |
|---|---|
| `api_key` mode, no key in the runner's environment | The runner exits 2 at start; Setup shows the runner as not connected. |
| Key mode, the key is invalid or the account is out of credit | The first run fails with a fixed sentence (no CLI text with a key in it); Setup stays "key present". |
| `apiKeySource` contradicts the mode | The run is cancelled, fails with a fixed reason, the thread says "billed differently than configured". |
| `--max-budget-usd` reached | Whatever the CLI does is recorded; the run fails with the budget as the visible reason (shape unverified). |
| Daily budget reached | Automatic runs are refused with a reason in the thread; a click still works. |
| Webhook: wrong token, unknown endpoint, disabled | `401`, same body, nothing written. |
| Webhook: body too large, wrong type, unknown field, bad value | `413`, `415`, `400` with the field named, nothing stored. |
| Webhook: over a rate limit | `429` with `Retry-After`; resolves and updates are still accepted. |
| Webhook: the database or the engine fails | `500`; the sender retries; idempotency by key makes the retry safe. |
| The webhook listener cannot bind | The server exits (as the internal listener does). |
| Control plane restarts | Endpoints persist (table); counters reset; the runner reports its mode again within seconds. |

## 9. Tests

- **Env and guard (7b):** a table of `EnvFor` over both modes (the key passes only in `api_key`, `CLAUDE_CODE_OAUTH_TOKEN` only in `subscription`, `ANTHROPIC_AUTH_TOKEN` and `ANTHROPIC_BASE_URL` never); the fake `claude` of `internal/testutil` emits `apiKeySource` values and the runner stops the mismatching run; the scrub removes the key from stdout, stderr and the outcome, including inside a JSON string; a render test that only the runner template references the Secret; `redact` pattern test.
- **Budget (7b):** the sum in `StartDiagnosis` inside the transaction with concurrent starts; automatic refused over budget, manual allowed; no money in the view of a subscription run.
- **Webhook (7c):** auth (all three 401 cases byte-identical, constant-time path, rotation overlap and expiry); the schema table (every bound, every extra and missing member, control characters, a key that redaction changes); idempotency (twice, resolve before fire, fire after resolve); each limit; the sweeper; prompt injection in a title and in a label is data in a block and cannot close it (`prompt` test); the host mapping from labels; the three-listener split (routes absent from the other two muxes, 404 when the address is unset); the migration on a database from files `001` to `017`.
- **The chain (`internal/app`):** a webhook firing opens an incident, the fake CLI diagnoses it, a `resolved` closes it.
- **UI:** pure modules with `node --test` (mode chip, spend line, endpoint form); a real browser look at Setup in both modes and at the endpoint's one-time token.
- **Real runs:** key mode with the real CLI settles the unverified list of 5.1 (costs money on the operator's key: the maintainer decides); recorded in `docs/research/`.
- **7a:** `scripts/check-no-lab-values.sh` and its test; `make chart-check` on stock values.

## 10. Order of work

1. **7a, a stranger can install it.** `SECURITY.md` (the licence is MIT and merged), the inventory fixes of section 7, chart defaults without a lab, the generic install runbook, the positioning edits of section 12, the lab-values script. Small code, mostly copy and docs; no migration.
2. **7b, API-key mode.** Migration 017, `EnvFor`, the runner guard and scrub, the claim field, the two settings, the status field, Setup and run UI, chart `runner.auth`, the real run with a key, the record.
3. **7c, the webhook source.** Migration 018 (`webhook_endpoints`, `incidents` rebuild), `internal/webhook`, the third listener and chart pieces, the sweeper, the responder pieces of 6.7, the Setup block, the runbook with sender recipes (Grafana, Uptime Kuma, `curl`), the chain test.
Cycle 6 is independent of all three. 7b before 7c because a webhook can start paid runs and the budget must exist first.

## 11. Risks

| Risk | Decision | Retrofit |
|---|---|---|
| **The terms of use**: a public tool that drives the CLI for strangers may be read as "building a product". Not settled by the research; not legal advice. | Each operator uses their own login; `api_key` mode exists and is recommended when unsure or for 24/7; the maintainer re-reads the primary text before publishing. | API key as the documented default; ask the vendor. |
| **A key in the runner's environment** is readable by anyone with `kubectl exec` on that pod. | Accepted: that person already holds the login in subscription mode. The key never reaches the control plane or the database. | A key file with a mounted Secret and `apiKeyHelper`. |
| **A leaked webhook token** lets a stranger open incidents and, with a host rule, aim a read-only run at a host. | Per-endpoint rate and new-incident limits; diagnosis off by default; the daily limits and budget; rotation; hashed storage. Changes still need an approval. | An endpoint-to-hosts allowlist; HMAC signing. |
| **Denial of wallet** through automatic runs. | Daily diagnosis limits (exist), the dollar budget, `--max-budget-usd`. | Lower defaults. |
| **An internet-facing listener** in a project whose other ports are LAN-only. | Off by default, own port, own Service and policy, real timeouts, tiny strict body, no state written for a rejection. | A mandatory allowlist of source ranges. |
| **The `incidents` rebuild** (the table others point at; 008 once deleted notes). | The `-- remedy:foreign-keys-off` pattern and the model test with a row in every referencing table. | None. |
| **Cost figures mislead.** `total_cost_usd` is an estimate; for a subscription it is not a charge. | Money is shown only for key-billed runs and always as an estimate. | Read usage from the Console. |
| **Unverified CLI behaviour** (5.1). | The guard fails closed; a real run settles it before 7b is called done. | Pin a CLI version that behaves. |
| **The series is long.** | Each plan is built within its own part; this document changes first when a decision does. | None. |

## 12. Documents to change

- [`../design.md`](../design.md) **section 1**: replace the non-goals line with "**Non-goals:** multi-user operation, third-party access to the subscriptions, auto-merge, running Remedy as a service for other people. **Goal (2026-10-21):** anyone may run their own installation for their own hosts, with their own login or API key." Purpose, line 8: "an autonomous AI operator for a homelab GitOps setup" becomes "...for a self-hosted GitOps and infrastructure setup (a homelab, a small team's servers run by one operator)"; line 10-11: "Agents run on the subscription logins of the official CLIs, without API tokens" becomes "...on the operator's own subscription login of the official CLI, or on their own API key; the mode is chosen once per installation and shown everywhere". Section 2.1: "Remedy never touches login credentials" gains "and in key mode the key never leaves the runner". Section 2.3 gains the webhook source; section 4 gains the rows of section 11.
- **`README.md` intro** (lines 8-11 and the status block): "AI operator for a homelab GitOps setup" becomes "Self-hosted AI operator for your infrastructure"; "Agents run on the subscription logins of the official CLIs (Claude Code, Antigravity CLI), without API tokens" becomes "Agents run the unmodified Claude Code CLI on your own subscription login or on your own API key (Antigravity CLI is planned). Each installation belongs to one operator; Remedy is not a service and has no user accounts."; in "Incidents and diagnoses", the sentence "The runner removes `ANTHROPIC_API_KEY` ... always billed to the subscription login" becomes a description of the two modes; the Alertmanager example loses the NAS label; add a short "Who is this for" block (individuals, one installation each, why no teams) and links to the install runbook.
- [`../research/subscription-cli-usage.md`](../research/subscription-cli-usage.md): a dated paragraph on installations by strangers once the maintainer has read the primary text; consequence 3 ("single-person tool for the owner's own homelab") is reworded to "for the operator's own hosts".
- [`2026-10-09-hosts-and-skills-design.md`](2026-10-09-hosts-and-skills-design.md): a line that cycle 7 follows the series and cycle 6 is unchanged.
- `CLAUDE.md`: "Current state" (cycle 7), the `--bare` rule, the trust-boundary paragraphs (key mode, the webhook listener), the new variables (`REMEDY_AUTH_MODE`, `REMEDY_WEBHOOK_ADDR`), the lab-values script under Commands.
- `deploy/chart/README.md`, `docs/runbook/install.md` (new), `docs/runbook/webhooks.md` (new), `docs/runbook/api-key-mode.md` (new), `docs/runbook/homelab-deploy.md` (becomes the maintainer's example), `SECURITY.md`.

## 13. Open points for the plans

1. **Primary text of the terms** (the maintainer): re-read the vendor's legal page; decide how the README words key mode versus subscription, and whether the project may be offered to other operators as it is, with the login relay of the Setup page (`2026-10-21-ui-login-relay-design.md`, section 4), which since 2026-10-22 ships enabled in every installation in subscription mode (no switch): the primary text decides that, and if the reading is unfavourable the relay is removed (for others or altogether) and `kubectl exec -it` remains. Apart from that, nothing here depends on the answer except wording.
2. ~~The licence and whether the packages are public~~ Settled on 2026-10-21: the licence is MIT (`LICENSE`, #161, holder `Jaydee94`), and the packages are public (measured anonymously). Remaining: `SECURITY.md` with a private reporting address, and whether a `CONTRIBUTING.md` is wanted.
3. 7b: the `apiKeySource` value with a key; precedence of the key over a stored login; the behaviour at `--max-budget-usd`; the login check in key mode; the key format for `redact`; whether to combine `--bare` later.
4. 7b: where the "billed to" line sits on the run page and in Today's digest; whether `auth_source` shows in the activity feed.
5. 7c: the endpoint limits' defaults; whether `Authorization` is enough or a sender-side `?token=` is needed for tools that cannot set headers (rejected for now); the exact `webhook` wording in thread sentences ("not reported for 24 h").
6. 7c: whether Grafana's and vmalert's Alertmanager-compatible endpoints already work with the existing source (not verified); if so, the recipes cover most users and the webhook is for the rest.
7. 7c: `?format=` decoders for named tools as a later cycle; an endpoint-to-hosts allowlist.
8. 7a: how the CLI pin ships with the chart; the exact deny-list of `check-no-lab-values.sh`; the `install.md` path for a cluster without an Ingress controller.
9. Whether subscription operators need the quota display (`rate_limit_event`) before 24/7 use is sensible; not in this series.
