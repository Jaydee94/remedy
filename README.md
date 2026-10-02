# Remedy

AI operator for a homelab GitOps setup. Remedy reacts to alerts, logs, failed pipelines
and GitOps status, analyses root causes and delivers fixes as pull requests.
**A human always does the merge.** Agents run on the subscription logins of the official
CLIs (Claude Code, Antigravity CLI), without API tokens.

> Status: bootstrap. Design, roadmap and a runnable skeleton exist, but no functionality
> yet. The next step is phase 0 (see [`docs/plans/`](docs/plans/)).

## Documentation

- [`docs/design.md`](docs/design.md): decisions, architecture, roadmap, risks
- [`docs/research/subscription-cli-usage.md`](docs/research/subscription-cli-usage.md): research on using the CLIs with a subscription
- [`docs/plans/`](docs/plans/): implementation plans per phase

## Layout

| Path | Content |
|---|---|
| `cmd/remedy-server` | Control plane (API, pipeline, gatekeeper, DB) |
| `cmd/remedy-runner` | Runner, starts the CLI subprocesses |
| `internal/` | Go packages (not importable from outside) |
| `web/` | UI (React, Vite, TypeScript, Tailwind) |

## Development

Requirements: Go (see `go.mod`), Node.js with npm.

```sh
make help        # all targets
make test        # Go tests
make dev-server  # control plane on :8080
make dev-web     # Vite dev server, proxies /api and /healthz to :8080
make check       # everything CI checks
```
