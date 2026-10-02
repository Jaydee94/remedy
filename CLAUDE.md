# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Remedy is an AI operator for a homelab GitOps setup: it reacts to alerts, logs and failed pipelines, analyses root causes and opens pull requests. A human always merges. Authoritative documents: `docs/design.md` (decisions, roadmap, accepted risks) and `docs/research/subscription-cli-usage.md` (terms-of-use constraints). On conflict the design document wins; change the document before changing a decision.

## Current state

Phase 0 is done: control plane (SQLite, admin and runner APIs, SSE), runner, UI, Docker image. Phase 1 is built in small plans: the spec is `docs/specs/2026-10-02-phase-1-detect-and-diagnose-design.md`, the plans are in `docs/plans/`. Plans 1a (GitHub connection and repos) and 1b (poller, incidents, activity log, run extension, reaper) are implemented; the responder (1c) and the timeline with the real run against GitHub (1d) are next. Check `git log` and the plan before assuming a package from a later task exists, and build only within the current plan.

Docs: `docs/design.md` (decisions), `docs/specs/` (what and why), `docs/plans/` (how), `docs/research/` (spikes and measurements).

## Commands

```sh
make check                                       # fmt, vet, test, web lint, web build (same as CI)
make test                                        # all Go tests
go test ./internal/server -run TestHealthz -v    # single Go test
go test ./internal/secret -run TestSealOpenRoundTrip -v   # another single Go test
go test ./... -race                              # CI-relevant: some ordering bugs only show with -race -cpu 1
make build                                       # UI first, then both binaries (the server embeds the UI via -tags webui)
make build-go                                    # Go only, no UI
go test -tags webui ./web/                       # needs a built UI (make web-build)
make dev-server                                  # control plane on :8080
make dev-web                                     # Vite dev server, proxies /api and /healthz to :8080
make web-install                                 # npm ci in web/
cd web && npm run lint                           # oxlint (there is no web test runner yet)
```

The server needs `REMEDY_ADMIN_PASSWORD` (12+ chars), `REMEDY_RUNNER_TOKEN` (24+ chars) and `REMEDY_MASTER_KEY` (`openssl rand -base64 32`). The runner needs the same runner token. Optional: `REMEDY_POLL_INTERVAL` (server, default 60s, at least 10s) and `REMEDY_RUN_TIMEOUT` (runner, default 10m). See the README quick start.

A fresh git worktree has no `web/node_modules`: run `make web-install` first, otherwise `make check` fails with `oxlint: command not found`, which looks like a code error.

A run's stdout and stderr lines are read concurrently, so their order relative to each other is not guaranteed. Tests may only assert the order within stdout.

The maintainer's shell aliases `ls` to a tool that rejects plain paths; use `command ls` in commands.

## Architecture

Two Go processes (module `github.com/Jaydee94/remedy`, `go 1.27.1`) plus a UI in `web/`:

- **Control plane** (`cmd/remedy-server`): admin API for the UI, runner API, SSE stream, SQLite, a GitHub poller that feeds the incident engine (`internal/poller`, `internal/incident`), a reaper for runs that stay `running` (`internal/reaper`), and later the MCP gatekeeper.
- **Runner** (`cmd/remedy-runner`): the only place that holds agent-CLI logins. It dials out to the control plane, long-polls `POST /runner/v1/claim`, runs one CLI subprocess per run in a temp workspace, and posts each output line back as an event (`/runner/v1/runs/{id}/events`, then `/finish`). It has no GitHub, cluster or DB credentials.
- **UI** (`web/`): React 19, Vite, TypeScript, Tailwind, React Router and shadcn/ui (components live in `src/components/ui`, imports use the `@/` alias, no `baseUrl`). Reads run events over SSE (`/api/runs/{id}/events`, resumable via `Last-Event-ID`). The Go server embeds `web/dist` only behind the `webui` build tag, so plain `go build`/`go vet` work without a built UI.

Trust boundaries that span several files and are easy to break:

- **Agents get no raw shell and no credentials.** All their actions go through MCP tools of the control plane's gatekeeper (phase 2). Read tools are free; mutating tools block until a human approves in the UI. The agent edits only its local workspace; the control plane pushes branches and opens PRs.
- **Subprocess environment is allowlisted** (`provider.FilterEnv`). `ANTHROPIC_API_KEY` / `ANTHROPIC_AUTH_TOKEN` must never reach the CLI, otherwise it silently bills the API instead of the subscription. `USER` must stay on the allowlist: on macOS the CLI finds its login in the keychain by user name and otherwise reports "Not logged in". The prompt goes to the CLI via stdin, never argv.
- **Claude CLI invocation** is `claude -p --output-format stream-json --verbose --permission-mode dontAsk --safe-mode --restricted --strict-mcp-config --tools <allowlist> --model <pinned>`. The isolation flags keep the maintainer's global MCP servers (Home Assistant), hooks and plugins away from the agent; `--restricted` ignores the user's settings, so the model must always be pinned. Never add `--bare`: it ignores the subscription login.
- **The GitHub token is write-only.** It is sealed with AES-256-GCM (`internal/secret`, key from `REMEDY_MASTER_KEY`, the row ID bound in as additional data), shown only as `…` plus its last four characters, and has a `***` text form (`secret.Value`) so it cannot reach logs or errors. The `github` client is read-only: every exported method is a `Get*` or `List*`, enforced by a test. Do not add a write method without a decision recorded in the spec.
- **Admin API:** session cookie (`HttpOnly`, `SameSite=Strict`) plus a required `X-Remedy-CSRF: 1` header on every non-GET. **Runner API:** shared bearer token, constant-time compare. The two are separate middleware and must stay separate.
- **Persistence:** one SQLite file, opened with a single connection (`SetMaxOpenConns(1)`), embedded SQL migrations. The graph (phase 3) lives in the same DB behind an interface. A run's events are always posted before `finish`, and the SSE handler reads run status before events; that ordering is what guarantees a client sees every event before `done`.
- **Incidents** are keyed by `(repo, ref, check name)`, and every state change writes its `activity` entry in the same transaction. The poller must stay idempotent (a second cycle on the same GitHub state changes nothing) and must not resolve PR incidents from a full page of 100 PRs, which may be truncated. Inside `Store.inTx` use only the `tx`: the store has one connection, so a call on `s.db` there deadlocks.

## Hard rules

- Never read, copy, log or store the agent CLIs' credentials; only start the unmodified binary (terms of use).
- No auto-merge, no force-push (additional commits only; abort on new foreign commits).
- Logs, alert text, PR text and commit messages are untrusted input and never go to an agent as instructions.
- Secrets are redacted before anything is handed to a CLI.
- No multi-user operation and no third-party access through the subscription logins.

## Conventions

- Everything in the repo is English: docs, plans, code, comments, commit messages, UI copy. Conversation with the maintainer may be in German.
- Go: stdlib-first (`net/http` method patterns, `log/slog`). A new dependency needs a reason; today there are only `modernc.org/sqlite` (pure Go, no cgo) and `golang.org/x/crypto`.
- `web/tsconfig.app.json` sets `erasableSyntaxOnly` and `verbatimModuleSyntax`: no enums or constructor parameter properties, and `import type` for types.
- Test first for logic with behaviour (incident correlation, redaction, gatekeeper). Runner tests use a fake `claude` shell script from `internal/testutil` instead of the real CLI.
