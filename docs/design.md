# Remedy: Design

Status as of 2026-10-02. Outcome of the planning session, corrected with the findings in
[`research/subscription-cli-usage.md`](research/subscription-cli-usage.md).

## 1. Purpose

Remedy is an autonomous AI operator for a homelab GitOps setup. It reacts to signals
(alerts, logs, failed pipelines, GitOps status), analyses root causes, drafts fixes and
delivers them as pull requests. **A human always does the merge.** Agents run on the
subscription logins of the official CLIs, without API tokens.

**Non-goals:** multi-user operation, third-party access to the subscriptions, auto-merge,
running as a service for other people's homelabs.

## 2. Decisions

### 2.1 Runtime (branch 1)

- Agents are **subprocesses of the official CLIs**: `claude -p` and `agy -p`
  (Antigravity CLI). Every run gets an isolated workspace.
- There is **one provider adapter per CLI**, a default provider per agent role, and a
  fallback on limit or error.
- The runner **isolates the CLI's configuration**: `--safe-mode --restricted --strict-mcp-config`, an
  explicit `--tools` allowlist (read-only for now) and a pinned `--model`. Without it a real run loaded
  the maintainer's global MCP servers (including Home Assistant), plugins and hooks. With it the agent
  has exactly the allowed tools, no MCP servers and no hooks, file access is confined to the workspace,
  and a trivial run costs about 4.5k instead of 23k tokens. No second login is needed. Measurements:
  [`research/spike-claude-billing.md`](research/spike-claude-billing.md).
- Remedy **never touches login credentials**. It only starts the unmodified binary.
- The Claude Agent SDK is out (subscription auth is not intended for it).
- The Gemini adapter targets `agy`, not the Gemini CLI (its subscription login was shut
  off on 2026-06-18). It is **phase 4, conditional** (see 5).

### 2.2 Autonomy and security (branch 2)

- Agents **only open PRs**. The merge is always done by the human.
- Agents have **full runtime access to the homelab, but every mutating action needs an
  approval** in the UI. Read-only actions run freely.
- This is enforced by an **MCP gatekeeper** in the control plane: agents get no raw shell
  and no cluster or git credentials. Every action is an MCP tool. Mutating tools pause
  until the UI approves. Every call goes to the audit log.
- **Secrets:** agents never receive decryption keys (SOPS, Sealed Secrets, etc.); encrypted
  files are ciphertext to them. Remedy redacts secrets in tool output before it goes to
  the CLI.
- **Untrusted input:** logs, alert text, PR descriptions and commit messages are data,
  never instructions (prompt injection). They are marked and passed fenced as such.

### 2.3 Signals and incidents (branch 3)

- Sources: Alertmanager/Prometheus webhooks, CI/CD events (GitHub), Loki logs (via ruler
  alerts or queries), Argo CD status.
- One **signal layer** with an adapter per source and a normalised event schema.
- Signals are correlated into **incidents** (fingerprint, affected service, time window).
  At most one active run per incident; follow-up signals attach to it. Plus cooldown,
  retry limit and a **budget per time window** (protects the subscription limit). The Claude CLI
  reports the subscription's five-hour and seven-day utilization in a `rate_limit_event`, which is the
  input for this budget and for the quota-based provider fallback.

### 2.4 GitOps integration (branch 4)

- **GitHub** and **Argo CD**.
- Access via a **fine-grained PAT on the user's own account**, without a required approval
  in branch protection. (Deliberate decision, see risks.)
- The agent edits only the **local workspace clone, without git credentials**. The control
  plane pushes the branch and opens the PR.
- Marking: branch prefix `remedy/`, label `remedy`, PR footer with incident ID, run ID
  and provider.
- **Bot PRs** (Renovate/Dependabot) with red CI are handled automatically. **Human PRs**
  only on opt-in (label or UI trigger).
- Never force-push, only additional commits. If foreign commits land on the branch after
  the run started, the run aborts.

### 2.5 Learning phase and graph (branch 5)

- The graph answers four questions: topology/dependencies, repo navigation, conventions,
  incident knowledge.
- **Hybrid:** structure (topology, navigation) is built **deterministically by parsers**
  (manifests, rendered Helm/Kustomize). The LLM summarises conventions and incident
  knowledge. The LLM does not build structure that a parser can provide.
- Staying current: incremental via push webhook (desired-state graph) plus a regular
  **actual-state reconciliation** against Argo/cluster with drift marking.
- LLM knowledge lands as a **proposal with source references** in a review queue. Only
  notes confirmed by the human count as facts; unconfirmed ones are marked uncertain.
- Agents read the graph through MCP tools of the gatekeeper.

### 2.6 Architecture and stack (branch 6)

- **Go backend**, **SQLite** (relational, graph via node/edge tables and recursive CTEs,
  FTS5 for full text), backed up e.g. with Litestream. The graph layer sits behind an
  interface.
- Two processes:
  - **Control plane:** API, event pipeline, incident logic, graph, gatekeeper (MCP),
    git/GitHub access, DB.
  - **Runner:** starts the CLI subprocesses. Holds **only** the CLI logins, no
    GitHub/cluster/DB credentials.
- The runner connects **outbound** to the control plane and pulls jobs.
- Every run gets a **short-lived run token** for the MCP gatekeeper, valid for exactly
  that run and that incident.
- Runs are **pausable** (state `waiting for approval`).

### 2.7 Deployment (branch 7)

- **Everything in the cluster**, managed by Argo. Control plane as a Deployment, runner as
  a StatefulSet with a PVC for CLI logins and workspaces.
- CLI login once via `kubectl exec`. The runner checks login status regularly, shows it in
  the UI (Setup shows the login state; it does not notify on expiry).
- GitHub Actions builds the control plane image and the runner image to GHCR. Delivered as
  a **Helm chart** in `deploy/chart` (2026-10-06,
  [spec](specs/2026-10-06-kubernetes-deployment-design.md)).
- **Two identities in one pod.** The control plane pod runs as the read service account.
  A CronJob mints a short-lived token of the write service account into a Secret that the
  pod mounts as a file; if the job stops, the token expires and actions fail closed.
- **Two listeners.** The control plane serves the UI, `/api` and `/healthz` on the public
  port and `/runner/v1` and `/mcp` on an internal one (`REMEDY_INTERNAL_ADDR`), so an
  Ingress cannot expose the runner token or the run tokens.
- **The `claude` binary is in no image.** An init container of the runner pod installs the
  pinned, unmodified CLI from the official source and verifies its checksum, because the
  images are public and the binary is proprietary.
- A throwaway **dummy setup** in kind (`make dummy-up`, `make dummy-down`) runs the real
  chain with the real agent; the login lives in a host directory that survives it.
- **Deliberately no** self-protection (no heartbeat, no protected paths) in the MVP.

### 2.8 Web UI (branch 8)

- React, Vite, TypeScript, Tailwind, shadcn/ui. The Go binary serves the static files;
  live data via SSE or WebSocket.
- MVP views: **activity timeline**, **incident detail with live run**, **approval inbox**,
  **knowledge review queue**. Plus settings and runner/login status. The graph explorer
  follows later.
- Access via a local admin account with a password. Basics: Argon2, session cookie with
  CSRF protection, login rate limit. Reachable only on the LAN/VPN.
- **Conversation UI (2026-10-05).** The UI speaks as Remedy, in the first person: Today, Conversations (one thread per
  incident), Needs you (the approvals), Ask Remedy (runs) and Setup. Routes, API and trust boundaries do not change. A sentence
  Remedy "says" is a template filled with values from the data; agent-written text is shown as text and never decides a
  sentence's structure. Spec: [`specs/2026-10-05-ui-conversation-redesign-design.md`](specs/2026-10-05-ui-conversation-redesign-design.md).
- **Three additions to the admin API carry it.** (1) `POST /api/incidents/{id}/unignore` moves an ignored incident back: to
  `diagnosing` while a responder run of it is queued or running, else `diagnosed` if it has a diagnosis, else `open`. (2) `POST
  /api/runs` takes `incidentId` with `tools: true`: an ad-hoc run about an incident. The question is the maintainer's own text;
  the frame around it is built in `internal/prompt` at the claim, and the incident's text is not in the prompt: the agent reads
  it with `incident_get` as data. This is a bounded part of the ad-hoc chat that the roadmap puts into phase 4. `GET /api/runs`
  takes `?incident=`. A question run is a run: the runner is sequential, so a diagnosis of any incident waits while a question
  run is queued, running or waiting for an approval, and `POST /api/incidents/{id}/diagnose` answers 409 until it has ended; the
  thread must handle that answer. (3) `GET /api/limits` carries `diagnosesLast24h`, the count the daily limit uses.

### 2.9 Agent roles and triggers (branch 9)

- Roles: **learner**, **incident responder**, **fixer**, **ad-hoc assistant**. Each with
  its own system prompt, tool set and budget. The responder has no write tools; the fixer
  works only in the workspace.
- An incident automatically starts the **responder** (cause, confidence, fix proposal).
  The human starts the **fixer** with a click.
- **Exception:** on red CI for bot PRs, responder and fixer run automatically (can be
  disabled per repo).
- Notification via the Home Assistant notify service with a deep link into the UI.
  Approvals happen only in the UI.

## 3. Roadmap

| Phase | Content | Result |
|---|---|---|
| **0 Foundation** | Monorepo, SQLite, control plane + runner, auth, UI shell, Claude adapter, **billing spike** | A run starts from the UI and streams live |
| **1 Pipeline fixer** | GitHub (PAT), CI events, responder + fixer, bot PRs automatic, timeline, HA notify | Red CI gets repaired, the human merges |
| **2 Gatekeeper + cluster** | MCP gatekeeper, approval inbox, cluster actions, Alertmanager/Argo signals, incident model | Reaction to outages |
| **3 Learning phase + graph** | Parsers, graph in SQLite, learner, review queue, drift, Loki signals | Fewer tokens, better fixes |
| **4 Expansion** | `agy` adapter and fallback, ad-hoc chat, graph explorer, hardening | Comfort, hardening |

Phase plans live in [`plans/`](plans/). Phase 1 is delivered in parts: the first part (detect and diagnose, with the
timeline) is implemented; the fixer that changes a workspace and lets the control plane open a pull request, and
the Home Assistant notification, are separate later cycles. Phase 2 is delivered in parts as well: the gatekeeper and the approvals
(parts A and B) and cluster access (part C: read tools and actions after an approval, tried on a kind testbed) are implemented, the signal adapters (part D: Alertmanager and Argo CD by polling, a general
incident model, an automatic responder for outages whose actions wait for an approval) are specified in
[`specs/2026-10-05-phase-2d-signals-design.md`](specs/2026-10-05-phase-2d-signals-design.md); the incident model
([plan 2d-1](plans/phase-2d-1-incident-model.md)) is built, the sources and the responder are not.

## 4. Accepted risks

| Risk | Decision | Retrofit |
|---|---|---|
| PAT on the user's account, no required approval | Identities blur, a leaked PAT has more reach | GitHub App, bot account |
| No self-protection | Remedy goes down with the cluster; agents can change Remedy's manifests via PR | Heartbeat, protected paths |
| UI protected by password only | Anyone who reaches the UI can approve cluster actions | TOTP, OIDC |
| 24/7 operation on a subscription login | Only partly fits "ordinary, individual usage" (see research) | Budget, low parallelism, API-key fallback |

## 5. Open points

See [`research/subscription-cli-usage.md`](research/subscription-cli-usage.md), the "Open"
sections:

1. Cross-check billing of `claude -p` in the dashboards. The CLI itself reports subscription use without
   overage (spike, phase 0).
2. Login lifetime and refresh inside a pod (spike, phase 0).
3. Behaviour of long-blocking MCP tools used for approvals (spike, phase 0/2).
4. `agy`: primary ToS source, keyring in a container, quota signalling (before phase 4).
