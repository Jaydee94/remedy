# Remedy

AI operator for a homelab GitOps setup. Remedy reacts to alerts, logs, failed pipelines
and GitOps status, analyses root causes and delivers fixes as pull requests.
**A human always does the merge.** Agents run on the subscription logins of the official
CLIs (Claude Code, Antigravity CLI), without API tokens.

> Status: phase 0 (foundation). You can start a read-only agent run from the web UI and
> watch its output stream in live. Incident handling, GitHub integration, the approval
> gatekeeper and the learning graph come in later phases (see [`docs/design.md`](docs/design.md)
> and [`docs/plans/`](docs/plans/)).

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

## Quick start (local)

Prerequisite: the `claude` CLI is installed and logged in with your subscription
(run `claude`, then `/login`).

```sh
make build       # builds the UI, then both binaries (the server embeds the UI)

export REMEDY_ADMIN_PASSWORD='choose-a-long-password'   # min. 12 characters
export REMEDY_RUNNER_TOKEN="$(openssl rand -hex 24)"    # min. 24 characters

./bin/remedy-server &    # control plane on :8080, database in ./remedy.db
./bin/remedy-runner      # needs the same REMEDY_RUNNER_TOKEN
```

Open <http://localhost:8080>, sign in, start a run. The agent is read-only in this phase.
The runner removes `ANTHROPIC_API_KEY` from the CLI's environment on purpose, so runs are
always billed to the subscription login.

**Warning:** the runner does not isolate the CLI's configuration yet. The agent loads your global
Claude setup: skills, plugins, hooks and **MCP servers** (for example Home Assistant), and a trivial
prompt costs about 23k input tokens. Until this is fixed (see
[`docs/research/spike-claude-billing.md`](docs/research/spike-claude-billing.md)), run the runner only
with a `CLAUDE_CONFIG_DIR` that has its own login and no MCP servers, and only with prompts you trust.

### Container

```sh
docker build -t remedy-server .
docker run -p 8080:8080 -v remedy-data:/data \
  -e REMEDY_ADMIN_PASSWORD -e REMEDY_RUNNER_TOKEN remedy-server
```

The image runs as a non-root user; `/data` holds the SQLite database. Use a named volume
or a directory writable by uid 65532. Only the control plane is containerised so far; the
runner needs the CLI login and runs on the host in this phase.
