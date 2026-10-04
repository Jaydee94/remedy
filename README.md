# Remedy

AI operator for a homelab GitOps setup. Remedy reacts to alerts, logs, failed pipelines
and GitOps status, analyses root causes and delivers fixes as pull requests.
**A human always does the merge.** Agents run on the subscription logins of the official
CLIs (Claude Code, Antigravity CLI), without API tokens.

> Status: phase 1 is in progress. You can start a read-only agent run from the web UI and watch
> its output stream in live, connect GitHub (read-only token, stored encrypted), add repositories
> under **Settings**, and see failed checks of their pull requests and default branches as
> **incidents**. For a real failure a read-only agent reads the failure log and a copy of the repository
> and stores a **diagnosis** on the incident (cause, confidence, affected files, proposed fix), on its own
> within limits or by a click. Everything Remedy does shows up in a live **timeline**, and the GitHub
> client is read-only down to its HTTP transport. The fixer, the approval gatekeeper and the learning graph come next (see
> [`docs/design.md`](docs/design.md), [`docs/specs/`](docs/specs/) and [`docs/plans/`](docs/plans/)).

## Documentation

- [`docs/design.md`](docs/design.md): decisions, architecture, roadmap, risks
- [`docs/specs/`](docs/specs/): design specs per phase part (what and why), next to the plans (how)
- [`docs/research/subscription-cli-usage.md`](docs/research/subscription-cli-usage.md): research on using the CLIs with a subscription
- [`docs/plans/`](docs/plans/): implementation plans per phase
- [`docs/runbook/first-real-run.md`](docs/runbook/first-real-run.md): a first run against a real repository, step by step, and
  [`docs/research/phase-1-real-run.md`](docs/research/phase-1-real-run.md): what it showed

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
make web-install # once: installs the UI dependencies
make build       # builds the UI, then both binaries (the server embeds the UI)

export REMEDY_ADMIN_PASSWORD='choose-a-long-password'   # min. 12 characters
export REMEDY_RUNNER_TOKEN="$(openssl rand -hex 24)"    # min. 24 characters
export REMEDY_MASTER_KEY="$(openssl rand -base64 32)"   # seals the GitHub token in the database

./bin/remedy-server &    # control plane on :8080, database in ./remedy.db
./bin/remedy-runner      # needs the same REMEDY_RUNNER_TOKEN
```

`REMEDY_MASTER_KEY` is required: Remedy refuses to start without it. Keep it outside the database and its
backups; whoever has both can read the stored GitHub token. If you lose or change the key, enter the token
again under **Settings**. Under **Settings** you also connect GitHub (a fine-grained, read-only personal access
token) and add repositories. `REMEDY_GITHUB_API_URL` (default `https://api.github.com`) exists for tests.

Remedy polls the enabled repositories with that token every 60 seconds (`REMEDY_POLL_INTERVAL`, a Go
duration of at least `10s`) and never writes to GitHub. A failed check becomes an incident under
**Incidents**, and it resolves when the check is green again or the pull request is closed. The runner stops a
run that takes longer than 10 minutes (`REMEDY_RUN_TIMEOUT`, set on the runner); the server fails runs that
stay `running` for more than 15 minutes, for example because the runner died.

For a failed check (`failure`, `timed_out`, `startup_failure`) Remedy starts a read-only diagnosis on its own,
within limits: at most 3 per incident (`REMEDY_DIAGNOSE_MAX_PER_INCIDENT`), 15 minutes apart
(`REMEDY_DIAGNOSE_COOLDOWN`), 20 per 24 hours (`REMEDY_DIAGNOSE_MAX_PER_DAY`, `0` turns it off), one run at a time.
**Diagnose** on an incident starts one by hand and ignores the limits. The agent runs on your subscription login
through the runner. It gets the job log, the pull request and a copy of the repository at the failing commit, can
only read, and its answer is checked before it is stored. Secret-looking text is removed from what it is sent, and
files named `.env*`, `*.pem`, `*.key` and `id_rsa*` are not part of the copy; a secret in any other file would be
readable by the agent. The runner never receives a GitHub token: the control plane fetches the copy.

Open <http://localhost:8080> and sign in. The home page is the **Timeline**, a live feed of everything Remedy
does: incidents, diagnoses, polling problems, changes to the settings. Under **Runs** you can start a read-only
agent run by hand. `REMEDY_LOG_LEVEL=debug` (default `info`) logs every request to GitHub with its method, host,
path and status, never a query or the token; the GitHub client refuses to send anything but `GET` and `HEAD`.
The runner removes `ANTHROPIC_API_KEY` from the CLI's environment on purpose, so runs are
always billed to the subscription login.

The runner starts the CLI in an isolated configuration (`--safe-mode --restricted`): your global
skills, plugins, hooks and MCP servers are **not** loaded, the agent only gets the read-only tools
`Read`, `Grep` and `Glob`, and file access is confined to a temporary workspace. This needs a recent
`claude` CLI (the flags exist in 2.1.287). The model is `sonnet` unless you set `REMEDY_CLAUDE_MODEL`
(see [`docs/research/spike-claude-billing.md`](docs/research/spike-claude-billing.md)).

### Container

```sh
docker build -t remedy-server .
docker run -p 8080:8080 -v remedy-data:/data \
  -e REMEDY_ADMIN_PASSWORD -e REMEDY_RUNNER_TOKEN -e REMEDY_MASTER_KEY remedy-server
```

The image runs as a non-root user; `/data` holds the SQLite database. Use a named volume
or a directory writable by uid 65532. Only the control plane is containerised so far; the
runner needs the CLI login and runs on the host in this phase.
