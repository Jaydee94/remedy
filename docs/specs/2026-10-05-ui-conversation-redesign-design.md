# UI redesign: Remedy speaks

Status: draft for the maintainer's review, 2026-10-05. Implementation plans: [`ui-0-backend`](../plans/ui-0-backend.md), implemented; ui-1 to ui-5 are not written yet (see 11).
Parent documents: [`../design.md`](../design.md) (section 2.8, the roadmap),
[`2026-10-02-phase-1-detect-and-diagnose-design.md`](2026-10-02-phase-1-detect-and-diagnose-design.md) (incidents, the timeline, the diagnosis),
[`2026-10-04-phase-2ab-gatekeeper-and-approvals-design.md`](2026-10-04-phase-2ab-gatekeeper-and-approvals-design.md) (approvals, the run view) and
[`2026-10-05-phase-2d-signals-design.md`](2026-10-05-phase-2d-signals-design.md) (incident sources).
Design source: the project "Remedy Design Direktion", file `Remedy Conversation v2.dc.html` (a prototype with sample data, not code to copy).

## 1. Goal and scope

The web UI changes from an admin console of tables and cards to a conversation: Remedy speaks in the first person, an incident is a thread, and a question
that waits for a decision is a message that asks. The look is the "Conversation v2" direction: a warm dark theme, Literata for Remedy's own voice, an amber
accent, pill-shaped controls, and a bottom tab bar on a phone.

The backend, the routes, the authentication and every trust boundary of the existing UI stay as they are. Three small backend additions carry the features
the prototype shows that the server cannot answer yet.

| Part | Content | Needs |
|---|---|---|
| 0 | Backend: un-ignore, a question about an incident as a run, the usage of the daily limit | |
| 1 | Foundation: theme, fonts, shell (sidebar, tab bar, offline banner, toast), login | |
| 2 | Today (feed and digest) and Conversations (the incident list) | 1 |
| 3 | The incident thread and the run as a conversation, the shared approval ask | 0, 1 |
| 4 | Needs you (approvals) and Ask Remedy (start a run, recent runs) | 1, 3 |
| 5 | Setup (GitHub, repositories, limits) | 0, 1 |

### Non-goals

- A chat that is not about an incident or a task. "Ask about this incident" is one run with the gatekeeper tools; it is not the ad-hoc chat of phase 4.
- A light theme, a theme switch, and any setting for the look.
- New navigation targets for the "Coming later" chips (pull requests, knowledge, graph). They are labels, nothing more.
- A digest written by an agent. The digest is computed from counts (see 5).
- Changing what the gatekeeper, the poller or the responder do, except as listed in 3.
- A web test runner. Pure logic is tested with `node --test` (see 10).

## 2. Decisions

| Topic | Decision |
|---|---|
| Delivery | Six plans (one per part), each its own pull request, in the order of the table in 1. Every pull request leaves a working app. |
| How the UI is rebuilt | **In place.** The routes and files stay; the theme tokens change first, so that pages not yet rebuilt already look right; the pages are rebuilt one part at a time. Pages not yet rebuilt sit in a `LegacyPage` wrapper that the part that rebuilds them removes. |
| Routes | Unchanged: `/`, `/incidents`, `/incidents/:id`, `/approvals`, `/runs`, `/runs/:id`, `/settings`. Only the labels change (Today, Conversations, Needs you, Ask Remedy, Setup). |
| Components | The existing stack (React, Tailwind, shadcn/ui). The shadcn components get the pill and card shapes; no component library is replaced. |
| Fonts | Self-hosted through `@fontsource-variable/*`: Schibsted Grotesk (UI), Literata (Remedy's voice), JetBrains Mono. No request leaves for a font. `@fontsource-variable/geist` is removed. |
| Icons | `lucide-react`, already a dependency. |
| Theme | One dark theme in `:root`. The `.dark` block goes. |
| Toast | A small provider in the shell, about 50 lines, no dependency. |
| Text Remedy "says" | Written by the UI from data, in fixed tables and templates. The only agent-written text is shown as text and is never the source of a sentence's structure (see 9). |
| Reversible actions | Ignoring an incident runs at once and offers Undo. Irreversible actions (cancel a run, disconnect GitHub, remove a repository) keep the two-step `ConfirmButton`. |
| After asking about an incident | The user stays in the thread; the question and the answer appear there. |
| Defaults of Ask Remedy | Both tool chips start off, as today. |
| Digest window | The last 24 hours. |

## 3. Part 0: backend

Three additions. Each is additive, none needs a migration, and each is recorded in `docs/design.md` before it is built.

### 3.1 Un-ignore

`POST /api/incidents/{id}/unignore`, with the CSRF header of every non-GET.

- `incident.Engine.Unignore` and `Store.UnignoreIncident`. The state change and its `incident_unignored` activity entry ("Stopped ignoring the incident for ...")
  happen in one transaction (`inTx`, only the `tx` inside).
- `404` for an unknown incident, `409` for one that is not `ignored`. An incident that turned green while it was ignored is `resolved`: `409`.
- The state before the ignore is not stored. The target is derived: **`diagnosing`** if a responder run of the incident is `queued` or `running`, else
  **`diagnosed`** if a diagnosis is stored, else **`open`**.
- The poller is unchanged: an ignored incident is skipped while it fails and resolved when it turns green. A test pins that a diagnosis that finishes while
  the incident is ignored does not lift it out of `ignored`; if it does today, that is fixed here.
- Undo in the UI is this call.

### 3.2 A question about an incident, as a run

`POST /api/runs` takes an optional `incidentId`.

- It needs `tools: true` (the agent reads the incident with `incident_get`); otherwise `400`. An unknown incident is `404`. `cluster` may be set as before.
- The run is an **ad-hoc** run with `runs.incident_id` set. It is not a responder run: the daily limit does not count it, `CheckOutcome` does not look for a
  diagnosis in its answer, and nothing in the responder reacts to it.
- `runs.prompt` holds only the question. The frame around it is added where the prompt is handed to the runner, for a run that is ad-hoc and has an
  `incident_id`, and is built in `internal/prompt`, the only place that builds prompts: the operator asks about incident N, read it first with
  `incident_get`, then answer. The incident's own text (GitHub's and an alert's) is **not** in the prompt; the agent gets it through the gatekeeper, as data.
- The question is the operator's own text and is trusted like any ad-hoc prompt.
- `GET /api/runs` takes `?incident=ID` and returns the runs of that incident, newest first, with the same limit. The run's JSON already has `incidentId`.
- A question run is a run: the runner is sequential, so a diagnosis of any incident waits while a question run is queued, running or waiting for an approval, and `POST /api/incidents/{id}/diagnose` answers 409 until it has ended; the thread must handle that answer.

### 3.3 The usage of the daily limit

`GET /api/limits` gets `diagnosesLast24h`: the number of automatic diagnoses in the last 24 hours, counted the way the daily limit counts them (the
`role = 'responder' AND automatic = 1` count of `internal/store/diagnosis.go`).

### 3.4 Documents and tests

`docs/design.md` gets the decisions before they are built: section 2.8 for the conversation UI and the three additions, with a note that the question run is a
bounded part of the ad-hoc chat that the roadmap puts into phase 4. Tests: the
store, the engine and the server for 3.1 (including a race of un-ignore against the poller and a resolve), the server and the prompt for 3.2, the server
for 3.3.

## 4. Part 1: foundation

### 4.1 Theme

The semantic tokens of shadcn (`--background`, `--card`, `--primary`, ...) take the palette of the prototype: background `#1a1612`, card `#231e19`, sidebar
`#16130f`, border `#2c2620`, input border `#3a322a`, text `#f1ebe3`, muted text `#a99e91`, subtle text `#74695d`, primary (amber) `#f2c14e` with dark text,
destructive `#ef6f5e`. New tokens: `success` `#7fcf8f`, `info` `#8cc4ef`, `violet` `#c3a5f2`, and a soft background per state (`soft-open`, `soft-diagnosing`,
`soft-diagnosed`, `soft-resolved`, `soft-ignored`). The font tokens are `--font-sans` (Schibsted Grotesk), `--font-serif` (Literata) and `--font-mono`.

Four keyframes (`rmIn`, `rmBlink`, `rmBreath`, `rmPulse`) become utilities and are switched off by `prefers-reduced-motion`. Button, input, badge and card
take the pill and card shapes, so that the pages not yet rebuilt do not clash.

### 4.2 Shell

`AppLayout` fills the viewport (`h-dvh`) and only the content scrolls. From `md` (768 px) on there is a 300 px sidebar. Below it there is a 56 px top bar
(title, back, status dot) and a bottom tab bar with 44 px targets and the safe-area inset.

- **Navigation:** the five items of section 2 with their icons; the badge on Needs you is the number of pending approvals. `/incidents/:id` keeps
  Conversations active, `/runs/:id` keeps Ask Remedy active.
- **Sidebar:** the logo with "awake" or "dozing", the list "Open conversations" (active incidents: dot, title, age; the preview line comes with part 2), the
  "Coming later" chips and "Sign out". On a phone, "Sign out" is at the bottom of Setup.
- **Shell hook:** one hook replaces `usePendingApprovals` and polls the pending approvals and the active incidents together. It returns the count, the incidents
  and `online`. `online` is false when the poll fails. That gives the offline banner ("Reconnecting... live updates are paused. What you see may be a minute old.")
  and "dozing".
- **Page header:** pages set title and back target with `useShellHeader`; the routes have defaults. The thread sets its title when its data is loaded.
- **Container:** the content area has no padding and no width of its own; each page brings its container. `LegacyPage` (padding, `max-w-5xl`) wraps a page that is
  not yet rebuilt.
- **Mark and avatar:** `RemedyMark` (the drop) and `RemedyAvatar` in the states `idle`, `working` (a breathing ring) and `ask` (filled amber).
- **Toast:** `useToast()` with a text and an optional Undo; it closes after 4.2 s and sits above the tab bar on a phone.

### 4.3 Login

The prototype's layout: the mark, a Literata headline, a pill input and a pill button. The greeting follows the time of day (Good morning, Good afternoon,
Good evening); the line below it is neutral: "Sign in with the admin password and I'll walk you through what happened." (the prototype's "overnight" is a claim
Remedy cannot back). A `401` shows "That isn't the admin password."; any other error shows the API's message.

## 5. Part 2: Today and Conversations

### 5.1 Today (replaces `TimelinePage`)

- The digest on top (avatar, a Literata sentence, actions), then the feed in day sections with a rule and a label (Today, Yesterday, the date). An entry has a
  coloured dot, its summary, its time, and the links "Incident #N" and "See my work" (the run).
- The data is unchanged: `listActivity`, `streamActivity`, `mergeEntries`, `groupByDay`. `kindDotClass` moves to the new tokens. An entry that arrives live fades in.
  "Load older entries" stays, as a pill.
- When the activity stream ends (`closed`: for example after a server restart and a new login) a line says "Live updates stopped." with a Reload button. The
  shell's banner covers only a failing poll.
- **The digest sentence** is computed from `listIncidents('all')` (up to 200) and the pending approvals, over the last 24 hours: incidents opened (by
  `firstSeen`), incidents diagnosed (by `lastDiagnosisAt`), questions waiting. "In the last 24 hours I opened 3 incidents and diagnosed 2. One question waits
  for you." and, when there is nothing, "All quiet. Nothing opened in the last 24 hours, and I'm still watching." A clause whose count is 0 is left out; the
  sentence never states what the data does not show.
- **Actions:** "Answer N questions" (to `/approvals`) only with pending approvals; "Read the diagnosis" (to the incident diagnosed last) only if there is one.
- **Empty:** the hint to Setup (connect GitHub, add a repository), in the new voice.

### 5.2 Conversations (replaces `IncidentsPage`)

- One card per incident: a round state marker, the title (`title`, for every source), "repository, ref" for GitHub and the source's label otherwise, the age, a
  preview in Literata, and chips for the state, "result, N times" and "asks you" when an approval waits for this incident (`ToolCall.incidentId`).
- Filter pills Active, Resolved and Ignored with counts (one `all` request and one `ignored` request, merged by id and counted in the browser: the API answers the 200 incidents seen most recently, and an ignored incident is not seen any more, so without the second request it would drop out of the list). Source and repository stay as small pill selects, shown
  only when there is more than one source or repository. The list polls every 5 s.
- **Preview**, from data: ignored: "You ignored this incident."; resolved: "Resolved: <reason>."; an approval waits: its question; diagnosing: "I'm looking into
  it..."; a diagnosis: its `summary`; otherwise "I haven't looked yet." or, when `autoDiagnose` is false, "I don't diagnose this kind of result automatically. Ask
  me if you want." The same function feeds the sidebar.
- Empty states per filter ("All quiet.", "Nothing resolved yet.", "Nothing ignored.").
- `incidentStateColor` and `StateBadge` move to the new tokens (a soft background and a dot).
- The pure functions (`incidentPreview`, `digestText`, the counts) live in `conversation.ts`.

## 6. Part 3: the thread and the run

### 6.1 The thread (replaces `IncidentView`)

Desktop: the conversation (at most 880 px) and a side panel "About this incident" (280 px); on a phone the panel sits under the conversation. A header (desktop)
shows "repository, ref, Incident #N" and the title in Literata.

`buildThread` (a pure function, in `thread.ts`) merges, by time: the incident's activity, the stored diagnosis, the approvals that wait for this incident, and
the question runs (`GET /api/runs?incident=`). Its items:

- **Event pills** from the activity: opened, recurred, resolved, ignored, unignored, `note_added`, `cluster_action`. `diagnosis_finished` and
  `diagnosis_started` have no pill: the diagnosis message and the working message replace them.
- **Working:** while the incident is `diagnosing`, a message with the breathing avatar and "Follow along" (to the run).
- **The diagnosis:** the summary in Literata; a confidence chip with three bars ("I'm fairly sure", "I think so", "I'm not sure"); the category; "Small mechanical
  fix" when `fix_looks_automatable`; a card with "What went wrong", "Where" (the files, in mono) and "What I'd do"; and "Please check this before you act on it. I
  could only read; I didn't change anything." Actions: "See my work" and "Diagnose again". A diagnosis about another commit than the failing one
  (`diagnosedSha` differs from `headSha`) carries a soft note.
- **No diagnosis yet:** "Diagnose now" for a result that is not diagnosed automatically or whose limit is reached; for an incident of another source: "I can't
  diagnose incidents from <source> yet."
- **An approval that waits**, inline (the shared ask, 6.3).
- **A question and its answer:** the question as the user's bubble; the answer as a Remedy message with steps and text, or a working indicator.

The composer ("Ask Remedy about this incident...") calls `POST /api/runs` with `{prompt, tools: true, incidentId}` and stays in the thread. An error such as "the
gatekeeper tools are not enabled" shows under the field. The thread polls every 5 s, every 2 s while a diagnosis or a question runs.

The side panel: state, conclusion, commit, occurrences, first and last seen, resolved; the links to the pull request and the check run (`safeUrl`, external); for an
incident of another source the "Signal" (its `details`) as a collapsed block; and Ignore (with Undo, 2) or Stop ignoring.

### 6.2 The run (replaces `RunView`)

- A row with the status chip (Queued, Working, Waiting for you, Done, Failed), the meta ("Responder" or "Ad-hoc", tools, cluster, the time), the link to the
  incident and "Cancel run" (`ConfirmButton`).
- The prompt: a user bubble for an ad-hoc run. A responder run's prompt holds data from GitHub and is long: it stays collapsed ("Prompt (N characters, it
  contains data from GitHub)").
- **Steps** (`runSteps`, `stepLabel` in `runview.ts`) come from the run's events: each `tool_use` is paired with its `tool_result` by `tool_use_id`. A label from a
  fixed table ("Looked at the pods in guestbook", "Read incident #27", otherwise "Used <tool>") sits over a raw line in mono (`tool {arguments} -> result`,
  shortened). The raw line is text.
- **The answer:** `run.result` for an ad-hoc run; for a responder run the fixed sentence "Done. The diagnosis is on the incident." with "Read the diagnosis".
- A waiting approval inline; a failure card by `failureReason` (the runner was lost, timed out, the answer was invalid, cancelled) with the run's text;
  "Start it again" for a failed ad-hoc run (for a responder run a link to the incident).
- Two collapsed blocks keep what the page shows today: "Show raw output" (every event) and "Tool calls" (`ToolCallsCard`: decision, reason and result of each call;
  it is the audit).

### 6.3 The shared approval ask

`ApprovalAsk` shows the question, the arguments (exactly what runs), Yes and No, and "Add a reason..", which opens a field in place. It decides through
`api.decideApproval` and calls `onChanged` afterwards whatever happened. A call with `waiting === false` has no buttons and says the agent no longer waits.
The question and the Yes label come from one table, `askText`:

| Tool | Question | Yes |
|---|---|---|
| `cluster_rollout_restart` | May I restart {name} in {namespace}? | Yes, restart it |
| `cluster_delete_pod` | May I delete the pod {name} in {namespace}? | Yes, delete it |
| `argo_refresh` | May I refresh the application {name}? | Yes, refresh it |
| `argo_sync` | May I sync the application {name}? | Yes, sync it |
| `incident_add_note` | May I add a note to incident #{id}? | Yes, add it |
| any other | May I run {tool}? | Yes, run it |

The exact argument names are read from the tools when the plan is written. The arguments are shown under the question in every case.

## 7. Part 4: Needs you and Ask Remedy

### 7.1 Needs you (replaces `ApprovalsPage` and `ApprovalCard`)

- Waiting questions, **oldest first** (the call that was asked first is decided first): the "ask" avatar, "Remedy, asked N min ago, Incident #N, the run", the
  question in Literata, then the highlighted card: the tool in mono, "exactly this runs if you approve", the arguments, "Add a reason (optional)" (at most 500
  characters) and Yes and No at full width. It is the large variant of the shared ask.
- A toast confirms ("Approved. Running...", "Denied. I told the agent."). The state comes from the 1 s poll; nothing is shown optimistically.
- Empty: "Nothing waits for you." Under a rule "Already answered": tool (mono), the decision in colour ("approved, failed" for an approved action that failed),
  the age and the outcome (the reason or `outcomeText`), with the links to the run and the incident.

### 7.2 Ask Remedy (replaces `RunsPage`)

- "What should I look into?", a large card with a Literata textarea, the chips "Use gatekeeper tools" and (only with `capabilities.cluster.read`) "Read the
  cluster", and a round send button. Cluster switches tools on; tools off switches cluster off. Cmd or Ctrl and Enter send. After sending the browser goes to
  `/runs/:id`, as today; an error shows in place.
- The hint follows the state and uses real data: only the workspace, tools, or cluster with `capabilities.cluster.namespaces` or "No action in the cluster is
  possible: no write access is configured."
- "Earlier conversations": the recent runs with dot, prompt (shortened), status and age. "Waiting for you" comes from the pending approvals, matched by `runId`
  (the run list does not carry `waitingApproval`). A responder run shows "Diagnosis of incident #N", not its prompt.
- `statusColor` in `status.ts` moves to the new tokens and gains the label "waiting for you"; the run page uses it as well.

## 8. Part 5: Setup

Replaces `SettingsPage`, `GitHubConnectionCard`, `ReposCard` and `LimitsCard`. One column, at most 760 px.

- **GitHub.** Connected: a status chip (Connected, Error, Cannot decrypt) with "checked N min ago", the sentence "I read pull requests and check runs as **@login**
  with token ...hint. It's stored encrypted and never shown again.", `statusDetail` as an error card, and the actions "Check connection" (with a "Checking..." state),
  "Replace token" (opens the token field) and "Disconnect" (`ConfirmButton`, "Confirm: also removes the repositories"). Not connected: "I can't see GitHub yet.
  Paste a fine-grained token and I'll start watching." with the token field, Connect and today's hint on the read-only permissions. The token stays write-only.
- **Repositories I watch.** One row per repository: the name, "<default branch>, polled N min ago" (or "never polled"), `lastError` in red, a `Switch` (the
  sub line says "paused" when off) and "Remove" (`ConfirmButton`, quiet). Add by `owner/name`; the browser checks the form first ("Use owner/name, for example
  jaydee94/homelab."), then the API answers. Without a connection: "Connect GitHub first."
- **My limits.** A Literata paragraph built from `GET /api/limits` ("I check every minute. I diagnose up to 3 times per incident and 20 times per 24 hours, at
  least 15 minutes apart, one run at a time. A run that stays running for 15 minutes is failed.") or, when `diagnoseMaxPerDay` is 0, "I don't diagnose on my own;
  you can start it by hand." With a limit above 0, a bar "Automatic diagnoses today: `diagnosesLast24h` of N" and the note that the server's environment sets the
  limits. The paragraph is `limitsText` in `setup.ts`.
- On a phone "Sign out" is the last element of the page.

## 9. Trust boundaries

The redesign changes no boundary and must not weaken one.

- **Agent-written text is data.** The diagnosis fields, an answer, a note and a step's raw line are rendered as React text, never as HTML or Markdown, in every
  new component. This includes the preview of a conversation and the sidebar.
- **No sentence's structure comes from untrusted text.** Remedy's sentences are templates and tables filled with values (a name, a number, a state). A value from
  GitHub or an alert is shown as such, shortened, and never decides what a sentence says.
- **A decision shows what runs.** The ask shows the arguments of the call as stored; nothing is reworded in a way that hides them.
- **The question run** (3.2) puts nothing from the incident into the prompt. The incident reaches the agent through `incident_get` and `sanitize`.
- **The admin API, the CSRF header, the session cookie and the three authentication domains are unchanged.** The GitHub token stays write-only: the UI shows only
  its hint.
- **Polling and streams are unchanged in what they fetch;** the shell's poll adds `GET /api/incidents` (active) to the existing approvals poll.

## 10. Testing and verification

- **Go (part 0):** test first for the new behaviour: the store, the engine, the server, the prompt frame, the limits field, and the race of 3.1. `go test ./... -race`.
- **Pure TypeScript** (`conversation.ts`, `thread.ts`, `runview.ts`, `askText`, `limitsText`, the status labels): tests with `node --test`, which runs `.ts` files and
  needs no dependency. `web/tsconfig.app.json` already has `erasableSyntaxOnly`, which Node's type stripping needs. The CI Node version has to run `.ts` (see 11).
- **Every part:** `make check` (fmt, vet, test, web lint, web build) and `go test -tags webui ./web/` after `make web-build`.
- **In a real browser,** at desktop width and at 390 px, with a database seeded by hand (timestamps with nine fractional digits, rows oldest first, see
  `CLAUDE.md`), and for runs with the fake `claude` of `internal/testutil`: incidents of all three sources, a pending approval that can be decided and one that can't,
  a failed run, an ignore with Undo, the offline banner (stop the server), a stream that ends (restart the server), and the empty states.
- Stray `*.png` files and `.playwright-mcp/` of the browser checks are deleted before a commit.

## 11. Plans, order and open points

Plans, in `docs/plans/`: `ui-0-backend.md`, `ui-1-foundation.md`, `ui-2-today-and-conversations.md`, `ui-3-thread-and-run.md`, `ui-4-needs-you-and-ask.md` and
`ui-5-setup.md`. Part 0 and part 1 do not depend on each other. Part 3 builds the shared ask that part 4 uses.

To settle when the plans are written, by reading the code, not by deciding here:

1. **Where the prompt is handed to the runner** (the claim path), to place the frame of 3.2 and to check that a run with `incident_id` and the ad-hoc role is
   treated like any ad-hoc run everywhere else.
2. **Whether a finishing diagnosis lifts an ignored incident** (3.1), and the exact transitions.
3. **The names of the font packages and the axes** (`opsz` of Literata) of `@fontsource-variable`.
4. **The Node version of CI,** for `node --test` on `.ts` files. If it is too old, the version is raised in the workflow, which is a decision for the plan.
5. **Which activity kinds exist for an incident's history** and what the `data` of `note_added` and the approval entries holds, for the pills of 6.1.
6. **The argument names of each tool,** for the table of 6.3 and the step labels of 6.2.
7. **What `GET /api/incidents` returns at the size of 200** for the digest and the counts, and whether the sidebar and the page can share one request.
