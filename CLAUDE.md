# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Remedy is an AI operator for a homelab GitOps setup: it reacts to alerts, logs and failed pipelines, analyses root causes and opens pull requests. A human always merges. Authoritative documents: `docs/design.md` (decisions, roadmap, accepted risks) and `docs/research/subscription-cli-usage.md` (terms-of-use constraints). On conflict the design document wins; change the document before changing a decision.

## Current state: mostly design, little code

Only a skeleton exists (`/healthz` handler, a runner stub, a UI placeholder). The architecture below is the **planned** one. `docs/plans/phase-0-foundation.md` lists every file still to be created, with code. Do not assume packages such as `internal/store` or `internal/runner` exist before checking, and build only within the current phase. Task 1 of that plan (a billing spike that needs the maintainer's real subscription login) gates Tasks 6 and later.

## Commands

```sh
make check                                       # fmt, vet, test, web lint, web build (same as CI)
make test                                        # all Go tests
go test ./internal/server -run TestHealthz -v    # single Go test
go test ./... -race                              # the plan's tests are written to pass under -race
make dev-server                                  # control plane on :8080
make dev-web                                     # Vite dev server, proxies /api and /healthz to :8080
make web-install                                 # npm ci in web/
cd web && npm run lint                           # oxlint (there is no web test runner yet)
```

The maintainer's shell aliases `ls` to a tool that rejects plain paths; use `command ls` in commands.

## Architecture

Two Go processes (module `github.com/Jaydee94/remedy`, `go 1.27.1`) plus a UI in `web/`:

- **Control plane** (`cmd/remedy-server`): admin API for the UI, runner API, SSE stream, SQLite, and later the MCP gatekeeper, git/GitHub access and signal ingestion.
- **Runner** (`cmd/remedy-runner`): the only place that holds agent-CLI logins. It dials out to the control plane, long-polls `POST /runner/v1/claim`, runs one CLI subprocess per run in a temp workspace, and posts each output line back as an event (`/runner/v1/runs/{id}/events`, then `/finish`). It has no GitHub, cluster or DB credentials.
- **UI** (`web/`): React 19, Vite, TypeScript, Tailwind. Reads run events over SSE (`/api/runs/{id}/events`, resumable via `Last-Event-ID`). The Go server embeds `web/dist` only behind the `webui` build tag, so plain `go build`/`go vet` work without a built UI.

Trust boundaries that span several files and are easy to break:

- **Agents get no raw shell and no credentials.** All their actions go through MCP tools of the control plane's gatekeeper (phase 2). Read tools are free; mutating tools block until a human approves in the UI. The agent edits only its local workspace; the control plane pushes branches and opens PRs.
- **Subprocess environment is allowlisted** (`provider.FilterEnv`). `ANTHROPIC_API_KEY` / `ANTHROPIC_AUTH_TOKEN` must never reach the CLI, otherwise it silently bills the API instead of the subscription. `USER` must stay on the allowlist: on macOS the CLI finds its login in the keychain by user name and otherwise reports "Not logged in". The prompt goes to the CLI via stdin, never argv.
- **Claude CLI invocation** is `claude -p --output-format stream-json --verbose --permission-mode dontAsk --safe-mode --restricted --strict-mcp-config --tools <allowlist> --model <pinned>`. The isolation flags keep the maintainer's global MCP servers (Home Assistant), hooks and plugins away from the agent; `--restricted` ignores the user's settings, so the model must always be pinned. Never add `--bare`: it ignores the subscription login.
- **Admin API:** session cookie (`HttpOnly`, `SameSite=Strict`) plus a required `X-Remedy-CSRF: 1` header on every non-GET. **Runner API:** shared bearer token, constant-time compare. The two are separate middleware and must stay separate.
- **Persistence:** one SQLite file, opened with a single connection (`SetMaxOpenConns(1)`), embedded SQL migrations. The graph (phase 3) lives in the same DB behind an interface. A run's events are always posted before `finish`, and the SSE handler reads run status before events; that ordering is what guarantees a client sees every event before `done`.

## Hard rules

- Never read, copy, log or store the agent CLIs' credentials; only start the unmodified binary (terms of use).
- No auto-merge, no force-push (additional commits only; abort on new foreign commits).
- Logs, alert text, PR text and commit messages are untrusted input and never go to an agent as instructions.
- Secrets are redacted before anything is handed to a CLI.
- No multi-user operation and no third-party access through the subscription logins.

## Conventions

- Everything in the repo is English: docs, plans, code, comments, commit messages, UI copy. Conversation with the maintainer may be in German.
- Go: stdlib-first (`net/http` method patterns, `log/slog`). Allowed new dependencies in phase 0 are only `modernc.org/sqlite` (pure Go, no cgo) and `golang.org/x/crypto`.
- `web/tsconfig.app.json` sets `erasableSyntaxOnly` and `verbatimModuleSyntax`: no enums or constructor parameter properties, and `import type` for types.
- Test first for logic with behaviour (incident correlation, redaction, gatekeeper). Runner tests (planned in phase 0) use a fake `claude` shell script from `internal/testutil` instead of the real CLI.
