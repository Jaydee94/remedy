<p align="center">
  <img src="docs/assets/logo.svg" alt="Remedy" width="96" height="96">
</p>

# Remedy

AI operator for a homelab GitOps setup. Remedy reacts to alerts, logs, failed pipelines
and GitOps status, analyses root causes and delivers fixes as pull requests.
**A human always does the merge.** Agents run on the subscription logins of the official
CLIs (Claude Code, Antigravity CLI), without API tokens.

> Status: phase 1 (detect and diagnose) and the gatekeeper and cluster parts of phase 2 are implemented.
> Remedy polls GitHub (read-only token, stored encrypted) and turns failed checks of pull requests and
> default branches into **incidents**. For a real failure a read-only agent reads the failure log and a copy
> of the repository and stores a **diagnosis** on the incident (cause, confidence, affected files, proposed
> fix), on its own within limits or by a click. Agents get tools through an MCP gatekeeper: reading is free,
> and every action (a note on an incident, a restart, a deleted pod, an Argo CD sync) waits for your approval.
> The web UI is a conversation: Remedy speaks in the first person and one incident is one thread you can ask
> questions in. The fixer (a branch and a pull request), the Alertmanager and Argo CD sources and the learning
> graph come next (see [`docs/design.md`](docs/design.md), [`docs/specs/`](docs/specs/) and
> [`docs/plans/`](docs/plans/)).

## The UI

Five places, in the sidebar on a desktop and in a tab bar on a phone:

| Page | What it is |
|---|---|
| **Today** (`/`) | A sentence about the last 24 hours (incidents opened and diagnosed, questions that wait) and a live feed of everything Remedy does: incidents, diagnoses, polling problems, changes to the settings. |
| **Conversations** (`/incidents`) | One card per incident, filtered by Active, Resolved and Ignored. Open one to get its **thread**. |
| **Needs you** (`/approvals`) | Every action an agent asks to take, oldest first, with exactly what would run. Yes or No, with an optional reason. A badge in the navigation counts what waits. |
| **Ask Remedy** (`/runs`) | Start an agent run by hand, with or without the gatekeeper tools and, if the cluster is configured, the cluster read tools. The earlier runs are listed below. |
| **Setup** (`/settings`) | Connect GitHub, choose the repositories to watch and see the limits of automatic diagnosis. |

**A thread** shows the history of an incident, Remedy's diagnosis (or that it is looking into it), your questions
with the answers, and the actions that wait for a decision, in place. **Diagnose now** or **Diagnose again**
starts a diagnosis by hand, **Ignore** puts the incident away (Undo, or Ctrl+Z / Cmd+Z, brings it back), and the
field at the bottom asks Remedy about this incident: the agent reads the incident through a tool, and anything
it wants to change waits for your approval. **See my work** opens the run: the steps the agent took, what it
answered, and **Cancel run**.

What Remedy says is a sentence filled with values from the data. Text an agent or GitHub wrote is shown as text
and never decides what a sentence says. The UI works with the keyboard and a screen reader (a live region
announces new messages in a thread), and on a phone. A session lasts 24 hours and is kept in memory, so a server
restart ends it too; the next request then shows the login page again.

## Documentation

- [`docs/design.md`](docs/design.md): decisions, architecture, roadmap, risks
- [`docs/specs/`](docs/specs/): design specs per phase part (what and why), next to the plans (how); the UI is in
  [`2026-10-05-ui-conversation-redesign-design.md`](docs/specs/2026-10-05-ui-conversation-redesign-design.md)
- [`docs/plans/`](docs/plans/): implementation plans per phase and per UI part
- [`docs/research/subscription-cli-usage.md`](docs/research/subscription-cli-usage.md): research on using the CLIs with a subscription
- [`docs/runbook/`](docs/runbook/): real runs step by step: [a first run against a repository](docs/runbook/first-real-run.md)
  ([what it showed](docs/research/phase-1-real-run.md)), [the gatekeeper](docs/runbook/gatekeeper-real-run.md) and
  [the cluster tools](docs/runbook/cluster-real-run.md)

## Layout

| Path | Content |
|---|---|
| `cmd/remedy-server` | Control plane (admin and runner API, poller, responder, gatekeeper, DB) |
| `cmd/remedy-runner` | Runner, starts the CLI subprocesses |
| `internal/` | Go packages (not importable from outside) |
| `web/` | UI (React, Vite, TypeScript, Tailwind, shadcn/ui); the server embeds the build |
| `dev/kind/` | A throwaway kind cluster with demo workloads and Argo CD for the cluster tools |
| `docs/` | Design, specs, plans, research, runbooks |

## Development

Requirements: Go (see `go.mod`), Node.js with npm.

```sh
make help        # all targets
make web-install # once, and again after a dependency changed: installs the UI dependencies
make test        # Go tests
make dev-server  # control plane on :8080
make dev-web     # Vite dev server, proxies /api and /healthz to :8080
make check       # everything CI checks: fmt, vet, Go tests, UI lint, UI tests, UI build
```

`make dev-server` needs the three variables of the quick start. The UI tests (`cd web && npm test`) cover the
pure logic only (what a thread or the digest says, how a run is read); a UI change is also looked at in a real browser.

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

Open <http://localhost:8080> and sign in with the admin password.

`REMEDY_MASTER_KEY` is required: Remedy refuses to start without it. Keep it outside the database and its
backups; whoever has both can read the stored GitHub token. If you lose or change the key, enter the token
again under **Setup**. Under **Setup** you also connect GitHub (a fine-grained, read-only personal access
token, shown only as its last four characters afterwards) and add repositories. `REMEDY_GITHUB_API_URL`
(default `https://api.github.com`) exists for tests.

### Incidents and diagnoses

Remedy polls the enabled repositories with that token every 60 seconds (`REMEDY_POLL_INTERVAL`, a Go
duration of at least `10s`) and never writes to GitHub. A failed check becomes an incident under
**Conversations**, and it resolves when the check is green again or the pull request is closed. The runner stops a
run that takes longer than 10 minutes (`REMEDY_RUN_TIMEOUT`, set on the runner); the server fails runs that
stay `running` for more than 15 minutes, for example because the runner died (a run with gatekeeper tools is
failed after 2 minutes without a heartbeat from the runner, and its time budget stops while it waits for an approval).

For a failed check (`failure`, `timed_out`, `startup_failure`) Remedy starts a read-only diagnosis on its own,
within limits: at most 3 per incident (`REMEDY_DIAGNOSE_MAX_PER_INCIDENT`), 15 minutes apart
(`REMEDY_DIAGNOSE_COOLDOWN`), 20 per 24 hours (`REMEDY_DIAGNOSE_MAX_PER_DAY`, `0` turns it off), one run at a time.
**Setup** shows these limits and how much of the daily one is used. **Diagnose now** or **Diagnose again** in an
incident's thread starts one by hand and ignores the limits. The agent runs on your subscription login
through the runner. It gets the job log, the pull request and a copy of the repository at the failing commit, can
only read, and its answer is checked before it is stored. Secret-looking text is removed from what it is sent, and
files named `.env*`, `*.pem`, `*.key` and `id_rsa*` are not part of the copy; a secret in any other file would be
readable by the agent. The runner never receives a GitHub token: the control plane fetches the copy.

### Asking Remedy, and approvals

Under **Ask Remedy** you start an agent run by hand, and in a thread you ask about one incident. A run with tools
can read incidents and ask to add a note to one; every such action waits for your decision under **Needs you** (or
in the thread, in place), and the run waits with it as long as it takes. A first run against the real CLI is
described in [`docs/runbook/gatekeeper-real-run.md`](docs/runbook/gatekeeper-real-run.md) and
[`docs/research/phase-2ab-real-run.md`](docs/research/phase-2ab-real-run.md).

`REMEDY_LOG_LEVEL=debug` (default `info`) logs every request to GitHub with its method, host,
path and status, never a query or the token; the GitHub client refuses to send anything but `GET` and `HEAD`.
The runner removes `ANTHROPIC_API_KEY` from the CLI's environment on purpose, so runs are
always billed to the subscription login.

The runner starts the CLI in an isolated configuration (`--restricted`, and `--safe-mode` for a run without
gatekeeper tools): your global skills, plugins, hooks and MCP servers are **not** loaded, the agent only gets the
read-only tools `Read`, `Grep` and `Glob` (and the gatekeeper's tools when the run has them), and file access is
confined to a temporary workspace. This needs a recent `claude` CLI (the flags exist in 2.1.287). The model is
`sonnet` unless you set `REMEDY_CLAUDE_MODEL` (see [`docs/research/spike-claude-billing.md`](docs/research/spike-claude-billing.md)).

### Cluster access (optional)

The control plane can reach a Kubernetes cluster with two separate identities: a read-only
one (`REMEDY_K8S_READ_TOKEN_FILE`, a file with a service account token) and one for approved actions (`REMEDY_K8S_WRITE_TOKEN_FILE`, usable only in
the namespaces of `REMEDY_K8S_WRITE_NAMESPACES`). `REMEDY_K8S_API` (default: the in-cluster address), `REMEDY_K8S_CA_FILE` and `REMEDY_K8S_ARGO_NAMESPACE`
(default `argocd`) say where and how. With the read token set, the **Ask Remedy** page offers the chip "Read the cluster". A run started with it gets seven
read tools (workloads, pods, describe, events, pod logs, nodes, Argo CD applications); what a cluster returns is shown to the agent as data, never as
an instruction. Four actions are possible in the namespaces of
`REMEDY_K8S_WRITE_NAMESPACES`, each one only after you approve it under **Needs you**: restart a workload, delete a pod, refresh and sync an Argo CD
application ([`docs/specs/2026-10-04-phase-2c-cluster-design.md`](docs/specs/2026-10-04-phase-2c-cluster-design.md)). A run with the real CLI against the
testbed is described in [`docs/runbook/cluster-real-run.md`](docs/runbook/cluster-real-run.md) and recorded in
[`docs/research/phase-2c-real-run.md`](docs/research/phase-2c-real-run.md).
[`dev/kind/`](dev/kind/README.md) has a throwaway kind cluster with demo workloads and Argo CD to try it on.

### Container

```sh
docker build -t remedy-server .
docker run -p 8080:8080 -v remedy-data:/data \
  -e REMEDY_ADMIN_PASSWORD -e REMEDY_RUNNER_TOKEN -e REMEDY_MASTER_KEY remedy-server
```

The image runs as a non-root user; `/data` holds the SQLite database. Use a named volume
or a directory writable by uid 65532. The runner has its own image (`Dockerfile.runner`;
`make images` builds both; a tag `vX.Y.Z` publishes them to GHCR). It does not contain the
`claude` CLI: the Helm chart's init container installs the pinned CLI, and the login is done
once with `kubectl exec`. The chart is in [`deploy/chart`](deploy/chart/README.md); how to
install it on k3s under Argo CD is in [`docs/runbook/homelab-deploy.md`](docs/runbook/homelab-deploy.md).
