# UI redesign, part 3: the thread and the run Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the incident detail page by a conversation (the thread: events, Remedy's diagnosis, questions and answers, waiting approvals, a composer, a side panel with Ignore and Undo) and the run page by a conversation of one run (status, steps, answer, waiting approval, failure), with the shared approval ask.

**Architecture:** What the thread and the run show is built by pure, tested functions (`thread.ts`, `runview.ts`) from the API data. The pages are components over two data hooks (`useIncidentThread`, `useRun`). The conversation building blocks (`UserBubble`, `EventPill`, `RemedyMessage`, `StepCard`, `FailCard`, `DiagnosisMessage`, `ApprovalAsk`, `Composer`) are small components. The toast is hardened (live region, pause on hover and focus) because Ignore now uses it with Undo.

**Tech Stack:** React 19, React Router 8, TypeScript 7, Tailwind 4, `node --test`.

**Spec:** [`docs/specs/2026-10-05-ui-conversation-redesign-design.md`](../specs/2026-10-05-ui-conversation-redesign-design.md), section 6 (and 2, 3.1, 3.2, 9, 10). The backend it needs (un-ignore, the question run, `?incident=`) is merged (part 0). Earlier plans: `ui-1-foundation.md`, `ui-2-today-and-conversations.md`.

## Global Constraints

- Everything in the repo is English: code, comments, UI copy, docs, commit messages.
- `web/tsconfig.app.json` sets `erasableSyntaxOnly` and `verbatimModuleSyntax`: no enums or constructor parameter properties, `import type` for types.
- Pure logic modules (`thread.ts`, `runview.ts`, `status.ts`, `approvals.ts`, `ask.ts`, `incidents.ts`, `timeline.ts`) import only with relative paths and `.ts` extensions and contain only erasable TypeScript. Components may use the `@/` alias.
- **Untrusted text** (incident titles, diagnosis fields, agent answers, step arguments and results, tool arguments, notes, run prompts of responder runs) is rendered as React text only: never as HTML or Markdown. A sentence's structure never comes from such text: Remedy's sentences are templates filled with values. A responder run's prompt contains data from GitHub and stays collapsed.
- **A decision shows what runs:** the approval ask shows the arguments of the call exactly as stored.
- **The question to an incident** is sent as `{prompt, tools: true, incidentId}`; the user stays in the thread.
- Palette and tokens of part 1; no new hard-coded palette colours in new code (`bg-rose-500` and the like).
- Routes do not change. The thread and the run page leave the `LegacyPage` wrapper.
- Reversible actions use the toast with Undo (Ignore); irreversible ones keep `ConfirmButton` (Cancel run).
- Commits end with the line `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>` (exactly this model name).
- In a worktree-isolated session the Bash tool refuses commands that name git in a compound or variable form: use plain separate commands or a script file with literal paths (see `CLAUDE.md`); run npm with `--prefix web`. A fresh worktree has no `web/node_modules`: run `npm ci --prefix web` first. Use `command ls`. `npm run lint` must print no warnings.
- Work on a branch `feat/ui-3-thread-and-run`, never on `main`.

## Review Focus

1. A thread of an incident from another source (no repository, no ref, no diagnosis support): the header, the panel and the message "I can't diagnose incidents from <source> yet." must work; no "Diagnose" button then. (Task 1 tests, Task 4.)
2. A waiting approval of a **question run** carries no incident id (the tool call's `incident_id` comes from the tool's arguments): the thread must still show it, matched by the run id of the incident's question runs. (Task 1 test.)
3. A diagnosis about another commit than the failing one is marked as such; a diagnosis in the state `diagnosed` with `diagnosedSha` empty is not. (Task 1 test.)
4. `runSteps` on odd events: a `tool_use` without a result yet, a `tool_result` whose content is a string or a list of text blocks, an error result, the synthetic `StructuredOutput` call (hidden), payloads that are not objects. It never throws and never prints "undefined". (Task 1 tests.)
5. Ignore with Undo: Ignore acts at once, the toast offers Undo, Undo calls un-ignore, both reload the thread; an error shows inline and never leaves the page in a half state. (Task 4.)
6. Diagnose while another run is queued or running answers `409` (a question run is a run: the runner is sequential): the message of the API shows in the thread; the button is disabled while a diagnosis runs. (Task 4.)
7. The toast: a persistent live region so that screen readers announce it; the timer pauses while the pointer or the focus is on it; a long unbroken token wraps. (Task 2, Task 6.)
8. 390 px: nothing scrolls sideways; long titles, long file paths and step lines break; the composer stays at the bottom of the content; the top bar shows one title and each page has one `h1`. (Tasks 4, 5, 6.)
9. Keyboard: every button, link and `details` of the thread and the run shows a focus ring; the waiting approval can be answered with the keyboard. (Task 6.)

---

## File Structure

| File | Change | Responsibility |
|---|---|---|
| `web/src/api.ts` | modify | `createRun(..., incidentId?)`, `listIncidentRuns`, `unignoreIncident` |
| `web/src/status.ts` + `status.test.ts` | modify / create | token colours, `runPhase`, `phaseView` |
| `web/src/approvals.ts` | modify | `callStatusColor` on tokens |
| `web/src/runview.ts` + `runview.test.ts` | create | steps, labels, failure text, status helpers |
| `web/src/thread.ts` + `thread.test.ts` | create | `buildThread` |
| `web/src/toast.ts`, `web/src/components/ToastProvider.tsx` | modify | live region, pause, wrapping |
| `web/src/components/RemedyAvatar.tsx`, `web/src/components/shell/MobileBar.tsx` | modify | reduced-motion ring; title is not a heading |
| `web/src/components/conversation/{UserBubble,EventPill,RemedyMessage,StepCard,FailCard,DiagnosisMessage,ApprovalAsk,Composer}.tsx` | create | the building blocks |
| `web/src/useIncidentThread.ts`, `web/src/IncidentThreadPage.tsx` | create | the thread (replace `IncidentView.tsx`, `DiagnosisCard.tsx`, deleted) |
| `web/src/useRun.ts`, `web/src/RunPage.tsx` | create | the run (replaces `RunView.tsx`, deleted) |
| `web/src/ToolCallsCard.tsx` | modify | token classes; stays as the audit |
| `web/src/App.tsx` | modify | the two routes leave `LegacyPage` |
| `web/src/StateBadge.tsx`, `SourceBadge.tsx` | delete if unused | their only user was the old thread |
| `docs/specs/2026-10-05-ui-conversation-redesign-design.md` | modify | 6.1: steps of a question run live on the run page |

---

### Task 1: API, status and the logic of the thread and the run (pure)

**Files:**
- Modify: `web/src/api.ts`, `web/src/status.ts`, `web/src/approvals.ts`
- Create: `web/src/status.test.ts`, `web/src/runview.ts`, `web/src/runview.test.ts`, `web/src/thread.ts`, `web/src/thread.test.ts`

**Interfaces:**
- Consumes: `Run`, `RunEvent`, `ToolCall`, `Incident`, `ActivityEntry`, `Diagnosis`, `RunStatus` (types of `api.ts`); `kindDotClass` (`timeline.ts`); `sourceLabel` (`incidents.ts`).
- Produces: `api.createRun(prompt, tools?, cluster?, incidentId?)`, `api.listIncidentRuns(incidentId)`, `api.unignoreIncident(id)`; `RunPhase`, `runPhase`, `phaseView`, `statusColor` (tokens); `Step`, `runSteps`, `stepLabel`, `shortTool`, `effectiveStatus`, `canCancel`, `runFailure`, `runMeta`, `summarizeEvent`; `ThreadItem`, `ThreadInput`, `buildThread`.

- [ ] **Step 1: The API calls**

In `web/src/api.ts` replace the `createRun` entry and add two entries after `ignoreIncident`:

```ts
  createRun: (prompt: string, tools = false, cluster = false, incidentId?: number) =>
    request<Run>('POST', '/api/runs', incidentId === undefined ? { prompt, tools, cluster } : { prompt, tools, cluster, incidentId }),
```

```ts
  unignoreIncident: (id: number) => request<Incident>('POST', `/api/incidents/${id}/unignore`),
  /** The runs of one incident, newest first: its responder runs and the questions asked about it. */
  listIncidentRuns: (incidentId: number) => request<Run[]>('GET', `/api/runs?incident=${incidentId}`),
```

- [ ] **Step 2: Write the failing tests**

`web/src/status.test.ts`:

```ts
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { phaseView, runPhase, statusColor } from './status.ts'

describe('runPhase', () => {
  it('maps the statuses of a run to what Remedy says about it', () => {
    assert.equal(runPhase('queued', false), 'queued')
    assert.equal(runPhase('running', false), 'working')
    assert.equal(runPhase('succeeded', false), 'done')
    assert.equal(runPhase('failed', false), 'failed')
  })

  it('says waiting for a run that waits for an approval, and only for a running one', () => {
    assert.equal(runPhase('running', true), 'waiting')
    assert.equal(runPhase('succeeded', true), 'done')
    assert.equal(runPhase('failed', true), 'failed')
  })
})

describe('phaseView', () => {
  it('has a label and token classes for every phase', () => {
    assert.equal(phaseView.queued.label, 'Queued')
    assert.equal(phaseView.working.label, 'Working')
    assert.equal(phaseView.waiting.label, 'Waiting for you')
    assert.equal(phaseView.done.label, 'Done')
    assert.equal(phaseView.failed.label, 'Failed')
    for (const view of Object.values(phaseView)) {
      assert.match(view.dot, /^bg-/)
      assert.match(view.soft, /^bg-soft-/)
      assert.match(view.text, /^text-/)
    }
  })
})

describe('statusColor', () => {
  it('uses tokens, not palette colours', () => {
    for (const cls of Object.values(statusColor)) assert.match(cls, /^bg-(neutral|primary|success|destructive)$/)
  })
})
```

`web/src/runview.test.ts`:

```ts
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import type { Run, RunEvent } from './api.ts'
import { canCancel, effectiveStatus, runFailure, runMeta, runSteps, shortTool, stepLabel, summarizeEvent } from './runview.ts'

const ev = (seq: number, kind: string, payload: unknown): RunEvent => ({ seq, kind, payload, createdAt: '2026-10-05T08:00:00Z' })
const use = (id: string, name: string, input: unknown) => ({ type: 'tool_use', id, name, input })
const assistant = (seq: number, ...blocks: unknown[]) => ev(seq, 'assistant', { type: 'assistant', message: { content: blocks } })
const result = (seq: number, id: string, content: unknown, isError?: boolean) =>
  ev(seq, 'user', { type: 'user', message: { content: [{ type: 'tool_result', tool_use_id: id, content, is_error: isError }] } })

describe('shortTool', () => {
  it('drops the prefix of the gatekeeper tools', () => {
    assert.equal(shortTool('mcp__remedy__cluster_pods'), 'cluster_pods')
    assert.equal(shortTool('Read'), 'Read')
  })
})

describe('stepLabel', () => {
  it('names the tools of the gatekeeper in Remedy words', () => {
    assert.equal(stepLabel('mcp__remedy__incident_get', { id: 27 }), 'Read incident #27')
    assert.equal(stepLabel('mcp__remedy__incident_list', {}), 'Looked at the incidents')
    assert.equal(stepLabel('mcp__remedy__cluster_pods', { namespace: 'guestbook' }), 'Looked at the pods in guestbook')
    assert.equal(stepLabel('mcp__remedy__cluster_workloads', { namespace: 'demo' }), 'Looked at the workloads in demo')
    assert.equal(stepLabel('mcp__remedy__cluster_describe', { kind: 'deployment', namespace: 'demo', name: 'api' }), 'Looked at deployment api in demo')
    assert.equal(stepLabel('mcp__remedy__cluster_events', { namespace: 'demo' }), 'Read the events in demo')
    assert.equal(stepLabel('mcp__remedy__cluster_pod_logs', { namespace: 'demo', pod: 'api-1' }), 'Read the logs of api-1')
    assert.equal(stepLabel('mcp__remedy__cluster_nodes', {}), 'Looked at the nodes')
    assert.equal(stepLabel('mcp__remedy__argo_apps', { name: 'guestbook' }), 'Looked at the Argo CD application guestbook')
    assert.equal(stepLabel('mcp__remedy__argo_apps', {}), 'Looked at the Argo CD applications')
  })

  it('names the actions it asked for', () => {
    assert.equal(stepLabel('mcp__remedy__cluster_rollout_restart', { name: 'guestbook-ui', namespace: 'guestbook' }), 'Asked to restart guestbook-ui in guestbook')
    assert.equal(stepLabel('mcp__remedy__cluster_delete_pod', { name: 'api-1', namespace: 'demo' }), 'Asked to delete the pod api-1 in demo')
    assert.equal(stepLabel('mcp__remedy__argo_sync', { app: 'guestbook' }), 'Asked to sync the application guestbook')
    assert.equal(stepLabel('mcp__remedy__incident_add_note', { id: 27 }), 'Asked to add a note to incident #27')
  })

  it('names the tools of the workspace', () => {
    assert.equal(stepLabel('Read', { file_path: 'apps/ingress/values.yaml' }), 'Read apps/ingress/values.yaml')
    assert.equal(stepLabel('Grep', { pattern: 'REDIS_HOST' }), 'Searched for "REDIS_HOST"')
    assert.equal(stepLabel('Glob', { pattern: '**/*.yaml' }), 'Listed the files matching **/*.yaml')
  })

  it('falls back to a general phrase when a value is missing or the tool is unknown', () => {
    assert.equal(stepLabel('mcp__remedy__cluster_pods', {}), 'Looked at the pods')
    assert.equal(stepLabel('mcp__remedy__cluster_pods', null), 'Looked at the pods')
    assert.equal(stepLabel('mcp__remedy__incident_get', { id: { x: 1 } }), 'Read an incident')
    assert.equal(stepLabel('SomethingNew', { a: 1 }), 'Used SomethingNew')
  })
})

describe('runSteps', () => {
  it('pairs a tool call with its result', () => {
    const steps = runSteps([
      ev(1, 'system', { type: 'system', subtype: 'init' }),
      assistant(2, { type: 'text', text: 'Let me look.' }, use('t1', 'mcp__remedy__cluster_pods', { namespace: 'guestbook' })),
      result(3, 't1', '3 pods, 1 restarting'),
    ])
    assert.equal(steps.length, 1)
    assert.equal(steps[0].label, 'Looked at the pods in guestbook')
    assert.equal(steps[0].raw, 'cluster_pods {"namespace":"guestbook"} → 3 pods, 1 restarting')
    assert.equal(steps[0].done, true)
    assert.equal(steps[0].failed, false)
  })

  it('keeps the order of the calls', () => {
    const steps = runSteps([
      assistant(1, use('a', 'Read', { file_path: 'a.yaml' })),
      assistant(2, use('b', 'Read', { file_path: 'b.yaml' })),
      result(3, 'b', 'B'),
      result(4, 'a', 'A'),
    ])
    assert.deepEqual(steps.map((s) => s.label), ['Read a.yaml', 'Read b.yaml'])
  })

  it('shows a call that has no result yet', () => {
    const steps = runSteps([assistant(1, use('t1', 'Grep', { pattern: 'x' }))])
    assert.equal(steps[0].done, false)
    assert.equal(steps[0].raw, 'Grep {"pattern":"x"}')
  })

  it('reads a result that is a list of text blocks', () => {
    const steps = runSteps([
      assistant(1, use('t1', 'Read', { file_path: 'a' })),
      result(2, 't1', [{ type: 'text', text: 'line one' }, { type: 'text', text: 'line two' }]),
    ])
    assert.match(steps[0].raw, /line one line two$/)
  })

  it('marks an error result', () => {
    const steps = runSteps([assistant(1, use('t1', 'Read', { file_path: 'a' })), result(2, 't1', 'no such file', true)])
    assert.equal(steps[0].failed, true)
    assert.match(steps[0].raw, /error: no such file$/)
  })

  it('hides the synthetic structured-output call', () => {
    const steps = runSteps([assistant(1, use('t1', 'StructuredOutput', { summary: 's' })), result(2, 't1', 'ok')])
    assert.deepEqual(steps, [])
  })

  it('shortens a long result and a long argument list to one tidy line', () => {
    const long = 'x'.repeat(1000)
    const steps = runSteps([assistant(1, use('t1', 'Read', { file_path: 'a' })), result(2, 't1', `${long}\n\n  tail`)])
    assert.ok(steps[0].raw.length < 400)
    assert.ok(!steps[0].raw.includes('\n'))
  })

  it('never throws on payloads that are not what it expects', () => {
    const odd: RunEvent[] = [
      ev(1, 'assistant', null),
      ev(2, 'assistant', 'text'),
      ev(3, 'assistant', { message: null }),
      ev(4, 'assistant', { message: { content: 'not a list' } }),
      ev(5, 'user', { message: { content: [null, 3, { type: 'tool_result' }] } }),
      ev(6, 'raw', 'garbage'),
      assistant(7, { type: 'tool_use' }),
    ]
    assert.doesNotThrow(() => runSteps(odd))
    for (const s of runSteps(odd)) assert.ok(!s.label.includes('undefined') && !s.raw.includes('undefined'))
  })
})

describe('effectiveStatus', () => {
  it('treats a queued run that already has events as running', () => {
    assert.equal(effectiveStatus('queued', 3), 'running')
    assert.equal(effectiveStatus('queued', 0), 'queued')
    assert.equal(effectiveStatus('succeeded', 3), 'succeeded')
  })
})

describe('canCancel', () => {
  const run = (over: Partial<Run>): Run => ({ id: 'r', status: 'running', mcp: true, ...over }) as Run
  it('allows cancelling a queued run, and a running run that has the gatekeeper', () => {
    assert.equal(canCancel(run({}), 'queued'), true)
    assert.equal(canCancel(run({}), 'running'), true)
    assert.equal(canCancel(run({ mcp: false }), 'running'), false)
  })
  it('does not allow it after the end or after a cancel was asked for', () => {
    assert.equal(canCancel(run({}), 'succeeded'), false)
    assert.equal(canCancel(run({}), 'failed'), false)
    assert.equal(canCancel(run({ cancelRequested: true }), 'running'), false)
  })
})

describe('runFailure', () => {
  const run = (over: Partial<Run>): Run => ({ id: 'r', status: 'failed', result: '', ...over }) as Run
  it('names each reason in Remedy words and keeps the text of the run', () => {
    assert.equal(runFailure(run({ failureReason: 'runner_lost' }))?.title, 'The runner was lost')
    assert.equal(runFailure(run({ failureReason: 'timeout' }))?.title, 'It took too long')
    assert.equal(runFailure(run({ failureReason: 'invalid_output' }))?.title, "The answer wasn't valid")
    assert.equal(runFailure(run({ failureReason: 'cancelled' }))?.title, 'Cancelled')
    assert.equal(runFailure(run({ failureReason: 'cancelled', result: 'You cancelled this run.' }))?.text, 'You cancelled this run.')
  })
  it('has a general answer for a failure without a reason, and none for a run that did not fail', () => {
    assert.equal(runFailure(run({}))?.title, 'The run failed')
    assert.equal(runFailure(run({ status: 'succeeded' })), undefined)
    assert.equal(runFailure(run({ status: 'running' })), undefined)
  })
})

describe('runMeta', () => {
  it('lists the kind of the run and its tools', () => {
    assert.equal(runMeta({ role: 'responder', mcp: true, cluster: true } as Run, '08:38'), 'Responder · tools · cluster · 08:38')
    assert.equal(runMeta({ role: 'adhoc' } as Run, '08:38'), 'Ad-hoc · 08:38')
  })
})

describe('summarizeEvent', () => {
  it('shows the text of a result and shortens anything else', () => {
    assert.equal(summarizeEvent(ev(1, 'result', { result: 'Done.' })), 'Done.')
    assert.equal(summarizeEvent(ev(2, 'raw', 'plain')), 'plain')
    assert.ok(summarizeEvent(ev(3, 'assistant', { text: 'y'.repeat(900) })).length <= 603)
  })
})
```

`web/src/thread.test.ts`:

```ts
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import type { ActivityEntry, Diagnosis, Incident, Run, ToolCall } from './api.ts'
import { buildThread } from './thread.ts'

const inc = (over: Partial<Incident> = {}): Incident => ({
  id: 27,
  source: 'github',
  title: 'ci / helm-lint',
  severity: 'none',
  autoDiagnose: true,
  repoId: 1,
  repo: 'octo/homelab',
  ref: 'pr:214',
  checkName: 'ci / helm-lint',
  state: 'open',
  conclusion: 'failure',
  headSha: '3f9c2ab',
  occurrences: 1,
  firstSeen: '2026-10-05T07:00:00Z',
  lastSeen: '2026-10-05T07:00:00Z',
  diagnoses: 0,
  ...over,
})
const act = (id: number, kind: string, at: string, summary = kind): ActivityEntry => ({ id, at, kind, summary })
const diagnosis: Diagnosis = { summary: 's', cause: 'c', confidence: 'high', category: 'dependency_update', affected_files: ['a'], proposed_fix: 'f', fix_looks_automatable: true }
const run = (over: Partial<Run> = {}): Run => ({ id: 'r1', provider: 'claude', prompt: 'why?', status: 'succeeded', result: 'Because.', sessionId: '', costUsd: 0, createdAt: '2026-10-05T09:00:00Z', role: 'adhoc', incidentId: 27, ...over }) as Run
const call = (over: Partial<ToolCall> = {}): ToolCall => ({ id: 1, runId: 'x', tool: 'cluster_rollout_restart', kind: 'mutating', arguments: {}, status: 'waiting', decision: 'pending', requestedAt: '2026-10-05T10:00:00Z', waiting: true, ...over }) as ToolCall
const types = (items: ReturnType<typeof buildThread>) => items.map((i) => i.type)

describe('buildThread: the events', () => {
  it('turns the activity into pills, oldest first, and leaves out what a message replaces', () => {
    const activity = [
      act(5, 'incident_recurred', '2026-10-05T08:31:00Z', 'ci / helm-lint failed again'),
      act(4, 'diagnosis_finished', '2026-10-05T08:00:00Z'),
      act(3, 'diagnosis_started', '2026-10-05T07:50:00Z'),
      act(2, 'approval_requested', '2026-10-05T07:45:00Z'),
      act(1, 'incident_opened', '2026-10-05T07:00:00Z', 'ci / helm-lint failed on PR #214'),
    ]
    const items = buildThread({ incident: inc({ state: 'diagnosed', diagnosis }), activity, questionRuns: [], asks: [] })
    assert.deepEqual(items.filter((i) => i.type === 'event').map((i) => (i.type === 'event' ? i.text : '')), ['ci / helm-lint failed on PR #214', 'ci / helm-lint failed again'])
    assert.equal(items[0].type, 'event')
  })

  it('colours a pill by the kind of the entry', () => {
    const [pill] = buildThread({ incident: inc({ state: 'ignored' }), activity: [act(1, 'incident_opened', '2026-10-05T07:00:00Z')], questionRuns: [], asks: [] })
    assert.equal(pill.type === 'event' && pill.dot, 'bg-destructive')
  })
})

describe('buildThread: the diagnosis', () => {
  it('shows the stored diagnosis at the time it finished', () => {
    const items = buildThread({
      incident: inc({ state: 'diagnosed', diagnosis, diagnosedSha: '3f9c2ab', runId: 'rd', lastDiagnosisAt: '2026-10-05T07:50:00Z' }),
      activity: [act(1, 'incident_opened', '2026-10-05T07:00:00Z'), act(2, 'diagnosis_finished', '2026-10-05T08:00:00Z')],
      questionRuns: [],
      asks: [],
    })
    const d = items.find((i) => i.type === 'diagnosis')
    assert.ok(d && d.type === 'diagnosis')
    assert.equal(d.at, '2026-10-05T08:00:00Z')
    assert.equal(d.outdated, false)
    assert.equal(d.runId, 'rd')
  })

  it('marks a diagnosis about another commit, and not one with an empty diagnosed commit', () => {
    const mk = (diagnosedSha: string) =>
      buildThread({ incident: inc({ state: 'diagnosed', diagnosis, diagnosedSha }), activity: [], questionRuns: [], asks: [] }).find((i) => i.type === 'diagnosis')
    const other = mk('aaaaaaa')
    assert.ok(other && other.type === 'diagnosis' && other.outdated === true)
    const none = mk('')
    assert.ok(none && none.type === 'diagnosis' && none.outdated === false)
  })

  it('shows that it is working while the incident is being diagnosed', () => {
    const items = buildThread({ incident: inc({ state: 'diagnosing', runId: 'rd', lastDiagnosisAt: '2026-10-05T07:50:00Z' }), activity: [], questionRuns: [], asks: [] })
    const w = items.find((i) => i.type === 'working')
    assert.ok(w && w.type === 'working')
    assert.equal(w.runId, 'rd')
    assert.ok(!types(items).includes('undiagnosed'))
  })

  it('offers to diagnose an open incident that has no diagnosis', () => {
    const open = buildThread({ incident: inc(), activity: [], questionRuns: [], asks: [] }).find((i) => i.type === 'undiagnosed')
    assert.ok(open && open.type === 'undiagnosed' && open.reason === 'fresh')
    const manual = buildThread({ incident: inc({ autoDiagnose: false }), activity: [], questionRuns: [], asks: [] }).find((i) => i.type === 'undiagnosed')
    assert.ok(manual && manual.type === 'undiagnosed' && manual.reason === 'manual')
  })

  it('says that it cannot diagnose an incident of another source yet, with the source named', () => {
    const items = buildThread({ incident: inc({ source: 'alertmanager', repoId: 0, repo: '', ref: '' }), activity: [], questionRuns: [], asks: [] })
    const u = items.find((i) => i.type === 'undiagnosed')
    assert.ok(u && u.type === 'undiagnosed')
    assert.equal(u.reason, 'source')
    assert.equal(u.sourceLabel, 'Alertmanager')
  })

  it('does not offer a diagnosis for a resolved or ignored incident', () => {
    for (const state of ['resolved', 'ignored'] as const) {
      assert.ok(!types(buildThread({ incident: inc({ state }), activity: [], questionRuns: [], asks: [] })).includes('undiagnosed'))
    }
  })
})

describe('buildThread: questions, answers and approvals', () => {
  it('shows a question as the maintainer, followed by the answer', () => {
    const items = buildThread({ incident: inc({ state: 'diagnosed', diagnosis }), activity: [], questionRuns: [run({ startedAt: '2026-10-05T09:00:05Z', finishedAt: '2026-10-05T09:00:20Z' })], asks: [] })
    const q = types(items).indexOf('question')
    assert.ok(q >= 0)
    assert.equal(items[q + 1].type, 'answer')
  })

  it('keeps the question before its answer when the run has not started', () => {
    const items = buildThread({ incident: inc({ state: 'diagnosed', diagnosis }), activity: [], questionRuns: [run({ status: 'queued' })], asks: [] })
    const t = types(items)
    assert.ok(t.indexOf('question') < t.indexOf('answer'))
  })

  it('does not show the responder runs of the incident as questions', () => {
    const items = buildThread({ incident: inc(), activity: [], questionRuns: [run({ role: 'responder' })], asks: [] })
    assert.ok(!types(items).includes('question'))
  })

  it('shows the approvals that wait for this incident', () => {
    const items = buildThread({ incident: inc(), activity: [], questionRuns: [], asks: [call({ id: 7, incidentId: 27 }), call({ id: 8, incidentId: 99 })] })
    assert.deepEqual(items.filter((i) => i.type === 'ask').map((i) => (i.type === 'ask' ? i.call.id : 0)), [7])
  })

  it('also shows an approval of one of its question runs, which carries no incident id', () => {
    const items = buildThread({ incident: inc(), activity: [], questionRuns: [run({ id: 'rq' })], asks: [call({ id: 9, runId: 'rq', incidentId: undefined }), call({ id: 10, runId: 'other' })] })
    assert.deepEqual(items.filter((i) => i.type === 'ask').map((i) => (i.type === 'ask' ? i.call.id : 0)), [9])
  })

  it('puts everything in the order of time', () => {
    const items = buildThread({
      incident: inc({ state: 'diagnosed', diagnosis }),
      activity: [act(1, 'incident_opened', '2026-10-05T07:00:00Z')],
      questionRuns: [run({ createdAt: '2026-10-05T09:00:00Z' })],
      asks: [call({ incidentId: 27, requestedAt: '2026-10-05T10:00:00Z' })],
    })
    const at = items.map((i) => Date.parse(i.at))
    assert.deepEqual([...at].sort((a, b) => a - b), at)
    assert.equal(items[items.length - 1].type, 'ask')
  })
})
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `npm test --prefix web`
Expected: FAIL: `Cannot find module './runview.ts'`, `'./thread.ts'`; `runPhase`, `phaseView` not exported by `status.ts`.

- [ ] **Step 4: Implement `status.ts` and `approvals.ts`**

Replace `web/src/status.ts` with:

```ts
import type { RunStatus } from './api.ts'

/** The dot of a run's status in a list. */
export const statusColor: Record<RunStatus, string> = {
  queued: 'bg-neutral',
  running: 'bg-primary',
  succeeded: 'bg-success',
  failed: 'bg-destructive',
}

/** What Remedy says about a run: a run that waits for an approval is "waiting for you", not "working". */
export type RunPhase = 'queued' | 'working' | 'waiting' | 'done' | 'failed'

export function runPhase(status: RunStatus, waiting: boolean): RunPhase {
  switch (status) {
    case 'queued':
      return 'queued'
    case 'running':
      return waiting ? 'waiting' : 'working'
    case 'succeeded':
      return 'done'
    case 'failed':
      return 'failed'
  }
}

export const phaseView: Record<RunPhase, { label: string; dot: string; soft: string; text: string }> = {
  queued: { label: 'Queued', dot: 'bg-neutral', soft: 'bg-soft-ignored', text: 'text-neutral' },
  working: { label: 'Working', dot: 'bg-primary', soft: 'bg-soft-diagnosing', text: 'text-primary' },
  waiting: { label: 'Waiting for you', dot: 'bg-primary', soft: 'bg-soft-diagnosing', text: 'text-primary' },
  done: { label: 'Done', dot: 'bg-success', soft: 'bg-soft-resolved', text: 'text-success' },
  failed: { label: 'Failed', dot: 'bg-destructive', soft: 'bg-soft-open', text: 'text-destructive' },
}
```

In `web/src/approvals.ts` replace `callStatusColor` by:

```ts
export const callStatusColor: Record<ToolCall['status'], string> = {
  running: 'bg-primary',
  waiting: 'bg-primary',
  succeeded: 'bg-success',
  failed: 'bg-destructive',
  denied: 'bg-neutral',
  abandoned: 'bg-neutral',
}
```

- [ ] **Step 5: Implement `runview.ts`**

```ts
import type { Run, RunEvent, RunStatus } from './api.ts'

/** One thing the agent did: what it did in words, the raw line, and whether it has an answer yet. */
export interface Step {
  id: string
  label: string
  raw: string
  /** The result has arrived. */
  done: boolean
  /** The result is an error. */
  failed: boolean
}

const PREFIX = 'mcp__remedy__'

/** The name of a tool without the prefix of the gatekeeper's server. */
export function shortTool(name: string): string {
  return name.startsWith(PREFIX) ? name.slice(PREFIX.length) : name
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

/** A text or number value of an input, or undefined when it is missing, empty or of another type. */
function value(input: unknown, name: string): string | undefined {
  if (!isRecord(input)) return undefined
  const v = input[name]
  if (typeof v === 'string' && v !== '') return v
  if (typeof v === 'number') return String(v)
  return undefined
}

/**
 * What a tool call means, in Remedy's words, from a fixed table. The sentence is ours; the values in it come from the agent's
 * arguments and are only inserted. A missing value falls back to the general phrase.
 */
export function stepLabel(tool: string, input: unknown): string {
  const name = shortTool(tool)
  const v = (n: string) => value(input, n)
  switch (name) {
    case 'incident_get':
      return v('id') ? `Read incident #${v('id')}` : 'Read an incident'
    case 'incident_list':
      return 'Looked at the incidents'
    case 'incident_job_log':
      return v('id') ? `Read the failure log of incident #${v('id')}` : 'Read a failure log'
    case 'activity_list':
      return 'Read the activity log'
    case 'incident_add_note':
      return v('id') ? `Asked to add a note to incident #${v('id')}` : 'Asked to add a note'
    case 'cluster_workloads':
      return v('namespace') ? `Looked at the workloads in ${v('namespace')}` : 'Looked at the workloads'
    case 'cluster_pods':
      return v('namespace') ? `Looked at the pods in ${v('namespace')}` : 'Looked at the pods'
    case 'cluster_describe':
      return v('kind') && v('name') && v('namespace')
        ? `Looked at ${v('kind')} ${v('name')} in ${v('namespace')}`
        : v('kind') && v('name')
          ? `Looked at ${v('kind')} ${v('name')}`
          : 'Looked at a resource'
    case 'cluster_events':
      return v('namespace') ? `Read the events in ${v('namespace')}` : 'Read the events'
    case 'cluster_pod_logs':
      return v('pod') ? `Read the logs of ${v('pod')}` : 'Read the logs of a pod'
    case 'cluster_nodes':
      return 'Looked at the nodes'
    case 'argo_apps':
      return v('name') ? `Looked at the Argo CD application ${v('name')}` : 'Looked at the Argo CD applications'
    case 'cluster_rollout_restart':
      return v('name') && v('namespace') ? `Asked to restart ${v('name')} in ${v('namespace')}` : 'Asked to restart a workload'
    case 'cluster_delete_pod':
      return v('name') && v('namespace') ? `Asked to delete the pod ${v('name')} in ${v('namespace')}` : 'Asked to delete a pod'
    case 'argo_refresh':
      return v('app') ? `Asked to refresh the application ${v('app')}` : 'Asked to refresh an application'
    case 'argo_sync':
      return v('app') ? `Asked to sync the application ${v('app')}` : 'Asked to sync an application'
    case 'Read':
      return v('file_path') ? `Read ${v('file_path')}` : 'Read a file'
    case 'Grep':
      return v('pattern') ? `Searched for "${v('pattern')}"` : 'Searched the files'
    case 'Glob':
      return v('pattern') ? `Listed the files matching ${v('pattern')}` : 'Listed files'
  }
  return `Used ${name}`
}

const MAX_ARGS = 240
const MAX_RESULT = 160

/** One tidy line of text: whitespace collapsed, cut at `max` characters. */
function line(text: string, max: number): string {
  const flat = text.replace(/\s+/g, ' ').trim()
  return flat.length > max ? `${flat.slice(0, max)}…` : flat
}

function resultText(content: unknown): string {
  if (typeof content === 'string') return content
  if (Array.isArray(content)) {
    return content
      .map((b) => (isRecord(b) && typeof b.text === 'string' ? b.text : ''))
      .filter((t) => t !== '')
      .join(' ')
  }
  return ''
}

function blocks(payload: unknown): unknown[] {
  if (!isRecord(payload) || !isRecord(payload.message)) return []
  const content = payload.message.content
  return Array.isArray(content) ? content : []
}

/**
 * The steps of a run, from its events: each `tool_use` of an assistant event is paired with the `tool_result` of a later user event
 * by the id of the call. The order is the order of the calls. The synthetic structured-output call is not a step. Everything the agent
 * wrote is text: it is cut and shown, never interpreted. Events that are not what is expected are skipped.
 */
export function runSteps(events: readonly RunEvent[]): Step[] {
  const steps: Step[] = []
  const byId = new Map<string, Step>()
  for (const e of events) {
    if (e.kind === 'assistant') {
      for (const b of blocks(e.payload)) {
        if (!isRecord(b) || b.type !== 'tool_use' || typeof b.name !== 'string') continue
        if (b.name === 'StructuredOutput') continue
        const id = typeof b.id === 'string' ? b.id : `seq${e.seq}-${steps.length}`
        const args = b.input === undefined ? '' : (JSON.stringify(b.input) ?? '')
        const step: Step = {
          id,
          label: stepLabel(b.name, b.input),
          raw: `${shortTool(b.name)} ${line(args, MAX_ARGS)}`.trim(),
          done: false,
          failed: false,
        }
        steps.push(step)
        byId.set(id, step)
      }
    } else if (e.kind === 'user') {
      for (const b of blocks(e.payload)) {
        if (!isRecord(b) || b.type !== 'tool_result' || typeof b.tool_use_id !== 'string') continue
        const step = byId.get(b.tool_use_id)
        if (!step) continue
        const failed = b.is_error === true
        step.done = true
        step.failed = failed
        step.raw = `${step.raw} → ${failed ? 'error: ' : ''}${line(resultText(b.content), MAX_RESULT)}`.trimEnd()
      }
    }
  }
  return steps
}

/** A queued run that already has events is in fact running: events exist only after the runner has started it. */
export function effectiveStatus(status: RunStatus, eventCount: number): RunStatus {
  return status === 'queued' && eventCount > 0 ? 'running' : status
}

/** Whether "Cancel run" is offered: a queued run, or a running run that has the gatekeeper (the runner can then be told). */
export function canCancel(run: Run, status: RunStatus): boolean {
  const ended = status === 'succeeded' || status === 'failed'
  return !ended && !run.cancelRequested && (status === 'queued' || (status === 'running' && run.mcp === true))
}

/** What went wrong with a failed run, in Remedy's words. The run's own text, when there is one, is shown under the title. */
export function runFailure(run: Run): { title: string; text: string } | undefined {
  if (run.status !== 'failed') return undefined
  const own = run.result?.trim() ?? ''
  switch (run.failureReason) {
    case 'runner_lost':
      return { title: 'The runner was lost', text: own || 'The runner stopped sending heartbeats, so the run was failed. Nothing was changed.' }
    case 'timeout':
      return { title: 'It took too long', text: own || 'The run stayed running longer than its time budget, so it was stopped.' }
    case 'invalid_output':
      return { title: "The answer wasn't valid", text: own || 'The agent finished, but its answer did not match what I asked for.' }
    case 'cancelled':
      return { title: 'Cancelled', text: own || 'The run was cancelled. The runner stopped the agent.' }
  }
  return { title: 'The run failed', text: own || 'The agent stopped with an error.' }
}

/** The line under the status of a run: its kind, its tools and its time. */
export function runMeta(run: Run, time: string): string {
  return [run.role === 'responder' ? 'Responder' : 'Ad-hoc', run.mcp && 'tools', run.cluster && 'cluster', time].filter(Boolean).join(' · ')
}

/** One line of text for the raw output: the text of a result, a plain string, or the start of the JSON. */
export function summarizeEvent(e: RunEvent): string {
  if (e.kind === 'result' && isRecord(e.payload) && typeof e.payload.result === 'string') return e.payload.result
  if (typeof e.payload === 'string') return e.payload
  const text = JSON.stringify(e.payload) ?? ''
  return text.length > 600 ? `${text.slice(0, 600)}...` : text
}
```

- [ ] **Step 6: Implement `thread.ts`**

```ts
import type { ActivityEntry, Diagnosis, Incident, Run, ToolCall } from './api.ts'
import { sourceLabel } from './incidents.ts'
import { kindDotClass } from './timeline.ts'

export type ThreadItem =
  | { type: 'event'; key: string; at: string; dot: string; text: string }
  | { type: 'working'; key: string; at: string; runId?: string }
  | { type: 'diagnosis'; key: string; at: string; diagnosis: Diagnosis; outdated: boolean; sha: string; runId?: string }
  | { type: 'undiagnosed'; key: string; at: string; reason: 'fresh' | 'manual' | 'source'; sourceLabel: string }
  | { type: 'ask'; key: string; at: string; call: ToolCall }
  | { type: 'question'; key: string; at: string; run: Run }
  | { type: 'answer'; key: string; at: string; run: Run }

export interface ThreadInput {
  incident: Incident
  /** The history of the incident, newest first as the API answers. */
  activity: readonly ActivityEntry[]
  /** The ad-hoc runs that were started about this incident. */
  questionRuns: readonly Run[]
  /** The calls that wait for a decision (any incident). */
  asks: readonly ToolCall[]
}

/** The kinds an activity entry has that a message of the thread replaces. */
const REPLACED = new Set(['diagnosis_started', 'diagnosis_finished', 'approval_requested'])

const time = (at: string) => Date.parse(at) || 0

/**
 * The messages of an incident's thread, oldest first: its history as pills, Remedy's diagnosis (or that it is working, or that it can be
 * asked), the questions of the maintainer with their answers, and the calls that wait for a decision. The approval of a question run
 * carries no incident id (the id comes from the tool's arguments), so it is matched by the run.
 */
export function buildThread({ incident, activity, questionRuns, asks }: ThreadInput): ThreadItem[] {
  const items: ThreadItem[] = []

  for (const a of [...activity].sort((x, y) => x.id - y.id)) {
    if (REPLACED.has(a.kind)) continue
    items.push({ type: 'event', key: `a${a.id}`, at: a.at, dot: kindDotClass(a.kind), text: a.summary })
  }

  if (incident.diagnosis) {
    const finished = activity.filter((a) => a.kind === 'diagnosis_finished').sort((x, y) => y.id - x.id)[0]
    items.push({
      type: 'diagnosis',
      key: 'diagnosis',
      at: finished?.at ?? incident.lastDiagnosisAt ?? incident.lastSeen,
      diagnosis: incident.diagnosis,
      outdated: !!incident.diagnosedSha && incident.diagnosedSha !== incident.headSha,
      sha: incident.diagnosedSha || incident.headSha,
      runId: incident.runId,
    })
  } else if (incident.state === 'diagnosing') {
    items.push({ type: 'working', key: 'working', at: incident.lastDiagnosisAt ?? incident.lastSeen, runId: incident.runId })
  } else if (incident.state === 'open') {
    const reason = incident.source !== 'github' ? 'source' : !incident.autoDiagnose ? 'manual' : 'fresh'
    items.push({ type: 'undiagnosed', key: 'undiagnosed', at: incident.lastSeen, reason, sourceLabel: sourceLabel(incident.source) })
  }
  if (incident.diagnosis && incident.state === 'diagnosing') {
    items.push({ type: 'working', key: 'working', at: incident.lastDiagnosisAt ?? incident.lastSeen, runId: incident.runId })
  }

  const questionIds = new Set(questionRuns.map((r) => r.id))
  for (const r of questionRuns) {
    if (r.role !== 'adhoc') continue
    items.push({ type: 'question', key: `q${r.id}`, at: r.createdAt, run: r })
    items.push({ type: 'answer', key: `r${r.id}`, at: r.finishedAt ?? r.startedAt ?? r.createdAt, run: r })
  }

  for (const c of asks) {
    if (c.incidentId === incident.id || questionIds.has(c.runId)) items.push({ type: 'ask', key: `c${c.id}`, at: c.requestedAt, call: c })
  }

  // Array.prototype.sort is stable: a question stays before its answer when both carry the same time.
  return items.sort((x, y) => time(x.at) - time(y.at))
}
```

- [ ] **Step 7: Run the tests, lint and build**

Run: `npm test --prefix web` then `npm run lint --prefix web` then `npm run build --prefix web`
Expected: all pass; lint prints nothing. If a `thread.test.ts` expectation fails because of the order of two items with the same time, fix `thread.ts` (not the test) unless the test contradicts the spec.

- [ ] **Step 8: Commit**

```sh
git add web/src
git commit -m "feat(web): the logic of the thread and the run" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: The toast, the avatar, the top bar and the small building blocks

**Files:**
- Modify: `web/src/components/ToastProvider.tsx`, `web/src/components/RemedyAvatar.tsx`, `web/src/components/shell/MobileBar.tsx`
- Create: `web/src/components/conversation/UserBubble.tsx`, `EventPill.tsx`, `RemedyMessage.tsx`, `StepCard.tsx`, `FailCard.tsx`

**Interfaces:**
- Consumes: `RemedyAvatar`, `Step` (`runview.ts`), `cn`.
- Produces: `UserBubble({ children })`, `EventPill({ dot, children })`, `RemedyMessage({ kind?, meta?, children })`, `StepCard({ step })`, `FailCard({ title, children })`; a toast with a persistent live region and pause on hover/focus.

No unit test fits (DOM); the verification is lint, build and Task 6.

- [ ] **Step 1: The toast**

Replace `web/src/components/ToastProvider.tsx` with:

```tsx
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { ToastContext } from '@/toast.ts'

const SHOWN_MS = 4200

interface Toast {
  id: number
  text: string
  undo?: () => void
}

/**
 * Hosts the toast of the shell. It sits above the tab bar on a phone. The live region is always in the page, so that a screen reader
 * announces a toast that is put into it; the timer stops while the pointer or the focus is on the toast (WCAG 2.2.1).
 */
export default function ToastProvider({ children }: { children: ReactNode }) {
  const [toast, setToast] = useState<Toast | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const lastId = useRef(0)

  const arm = useCallback(() => {
    clearTimeout(timer.current)
    timer.current = setTimeout(() => setToast(null), SHOWN_MS)
  }, [])

  const hold = useCallback(() => clearTimeout(timer.current), [])

  const show = useCallback(
    (text: string, undo?: () => void) => {
      lastId.current += 1
      setToast({ id: lastId.current, text, undo })
      arm()
    },
    [arm],
  )

  const dismiss = useCallback(() => {
    clearTimeout(timer.current)
    setToast(null)
  }, [])

  useEffect(() => () => clearTimeout(timer.current), [])

  const value = useMemo(() => ({ show }), [show])

  return (
    <ToastContext.Provider value={value}>
      {children}
      <div
        role="status"
        aria-live="polite"
        className="pointer-events-none fixed bottom-24 left-1/2 z-50 flex w-max max-w-[calc(100%-2rem)] -translate-x-1/2 justify-center md:bottom-7"
      >
        {toast && (
          <div
            key={toast.id}
            onMouseEnter={hold}
            onMouseLeave={arm}
            onFocus={hold}
            onBlur={arm}
            className="pointer-events-auto flex w-max max-w-full animate-rm-in items-center gap-3.5 rounded-full bg-foreground py-2.5 pr-2.5 pl-4.5 text-background shadow-2xl"
          >
            <span className="min-w-0 font-semibold break-words">{toast.text}</span>
            {toast.undo && (
              <button
                type="button"
                onClick={() => {
                  toast.undo?.()
                  dismiss()
                }}
                className="flex h-8 shrink-0 items-center rounded-full bg-background px-3.5 font-semibold text-primary outline-none focus-visible:ring-3 focus-visible:ring-primary/50"
              >
                Undo
              </button>
            )}
          </div>
        )}
      </div>
    </ToastContext.Provider>
  )
}
```

- [ ] **Step 2: The avatar and the top bar**

In `web/src/components/RemedyAvatar.tsx` change the class list of the outer `span` so that the working state keeps a static ring when the user asks for reduced motion: replace `kind === 'working' && 'animate-rm-breath',` by

```tsx
        kind === 'working' &&
          'animate-rm-breath motion-reduce:shadow-[0_0_0_2px_var(--background),0_0_0_5px_rgb(242_193_78/0.5)]',
```

In `web/src/components/shell/MobileBar.tsx` change the title element from `<h1 ...>{header.title}</h1>` to `<p className="min-w-0 flex-1 truncate font-serif text-[17px] font-medium">{header.title}</p>` (keep its classes; only the tag changes): each page owns its `h1`, the bar's title is not a heading.

- [ ] **Step 3: The building blocks**

`web/src/components/conversation/UserBubble.tsx`:

```tsx
import type { ReactNode } from 'react'

/** What the maintainer said, on the right. */
export default function UserBubble({ children }: { children: ReactNode }) {
  return (
    <span className="max-w-[85%] self-end rounded-[20px_20px_6px_20px] bg-secondary px-4 py-3 break-words whitespace-pre-wrap text-foreground/90">
      {children}
    </span>
  )
}
```

`web/src/components/conversation/EventPill.tsx`:

```tsx
import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

/** Something that happened to the incident, as a pill in the middle of the thread. `dot` is a token class such as bg-destructive. */
export default function EventPill({ dot, children }: { dot: string; children: ReactNode }) {
  return (
    <span className="flex max-w-full items-center gap-2.5 self-center rounded-full bg-card px-4 py-2 text-center text-[13px] text-muted-foreground">
      <span aria-hidden className={cn('size-2 shrink-0 rounded-full', dot)} />
      <span className="min-w-0 break-words">{children}</span>
    </span>
  )
}
```

`web/src/components/conversation/RemedyMessage.tsx`:

```tsx
import type { ReactNode } from 'react'
import RemedyAvatar from '@/components/RemedyAvatar'

interface Props {
  kind?: 'idle' | 'working' | 'ask'
  /** The small line above the message, such as "Remedy · 08:40". */
  meta?: string
  children: ReactNode
}

/** A message of Remedy: the avatar on the left, a small line, and the content. */
export default function RemedyMessage({ kind = 'idle', meta, children }: Props) {
  return (
    <div className="flex gap-3.5">
      <RemedyAvatar kind={kind} />
      <div className="flex min-w-0 flex-1 flex-col gap-3">
        {meta && <span className="text-xs text-subtle">{meta}</span>}
        {children}
      </div>
    </div>
  )
}
```

`web/src/components/conversation/StepCard.tsx`:

```tsx
import type { Step } from '@/runview.ts'
import { cn } from '@/lib/utils'

/** One thing the agent did: in words, and the raw line under it. The raw line is the agent's text and is only shown. */
export default function StepCard({ step }: { step: Step }) {
  return (
    <div className="flex flex-col gap-1.5 rounded-2xl border border-border bg-sidebar px-3.5 py-2.5">
      <span className="flex items-center gap-2 text-[13px] text-muted-foreground">
        <span
          aria-hidden
          className={cn('size-1.5 shrink-0 rounded-full', step.failed ? 'bg-destructive' : step.done ? 'bg-violet' : 'bg-primary')}
        />
        <span className="min-w-0 break-words">{step.label}</span>
      </span>
      <span className="line-clamp-3 font-mono text-xs break-all text-subtle">{step.raw}</span>
    </div>
  )
}
```

`web/src/components/conversation/FailCard.tsx`:

```tsx
import type { ReactNode } from 'react'

/** Why a run failed, in a red card. */
export default function FailCard({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div role="alert" className="flex flex-col gap-2 rounded-[18px] border border-destructive/40 bg-soft-open p-4">
      <span className="font-semibold text-destructive">{title}</span>
      <span className="break-words whitespace-pre-wrap text-foreground/90">{children}</span>
    </div>
  )
}
```

- [ ] **Step 4: Verify and commit**

Run: `npm test --prefix web`, `npm run lint --prefix web`, `npm run build --prefix web`. Expected: all pass, lint prints nothing.

```sh
git add web/src
git commit -m "feat(web): harden the toast and add the building blocks of a conversation" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: The diagnosis, the approval ask and the composer

**Files:**
- Create: `web/src/components/conversation/DiagnosisMessage.tsx`, `ApprovalAsk.tsx`, `Composer.tsx`

**Interfaces:**
- Consumes: `Diagnosis`, `ToolCall`, `api.decideApproval`, `ApiError`, `argumentList`, `askText`, `categoryText`, `shortSha`, `useToast`, `Button`, `Input`, `buttonVariants`.
- Produces: `DiagnosisMessage({ diagnosis, outdatedSha?, headSha, runId?, busy?, onDiagnoseAgain? })`; `ApprovalAsk({ call, variant?, onChanged })` with `variant: 'compact' | 'large'` (the large one is used by part 4); `Composer({ placeholder, onSend })`.

- [ ] **Step 1: The diagnosis**

`web/src/components/conversation/DiagnosisMessage.tsx`:

```tsx
import { Link } from 'react-router'
import type { Diagnosis } from '@/api.ts'
import { categoryText, shortSha } from '@/incidents.ts'
import { Button, buttonVariants } from '@/components/ui/button'
import { cn } from '@/lib/utils'

const confidence = {
  high: { label: "I'm fairly sure", bars: 3, chip: 'bg-soft-diagnosing text-primary', bar: 'bg-primary' },
  medium: { label: 'I think so', bars: 2, chip: 'bg-secondary text-foreground/90', bar: 'bg-foreground/90' },
  low: { label: "I'm not sure", bars: 1, chip: 'bg-secondary text-muted-foreground', bar: 'bg-muted-foreground' },
} as const

interface Props {
  diagnosis: Diagnosis
  /** The commit the diagnosis is about, when it is not the failing one. */
  outdatedSha?: string
  /** The failing commit. */
  headSha: string
  /** The responder run that wrote it, for "See my work". */
  runId?: string
  busy?: boolean
  /** Starts a new diagnosis. Left out when none can be started. */
  onDiagnoseAgain?: () => void
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <span className="text-xs font-semibold text-muted-foreground">{title}</span>
      {children}
    </div>
  )
}

/** Remedy's diagnosis of an incident. Every field is the agent's text and is only shown. */
export default function DiagnosisMessage({ diagnosis: d, outdatedSha, headSha, runId, busy, onDiagnoseAgain }: Props) {
  const c = confidence[d.confidence] ?? confidence.low
  return (
    <>
      <p className="font-serif text-[clamp(18px,2vw,21px)] leading-normal text-pretty break-words">{d.summary}</p>
      <div className="flex flex-wrap items-center gap-2">
        <span className={cn('flex items-center gap-2 rounded-2xl py-1.5 pr-3 pl-1.5 text-[13px] font-semibold', c.chip)}>
          <span aria-hidden className="flex gap-0.5">
            {[0, 1, 2].map((i) => (
              <span key={i} className={cn('h-3.5 w-1.5 rounded-sm', i < c.bars ? c.bar : 'bg-input')} />
            ))}
          </span>
          {c.label}
        </span>
        <span className="rounded-2xl bg-secondary px-3 py-1.5 text-[13px] text-foreground/90">{categoryText(d.category)}</span>
        {d.fix_looks_automatable && (
          <span className="rounded-2xl bg-secondary px-3 py-1.5 text-[13px] text-foreground/90">Small mechanical fix</span>
        )}
      </div>
      <div className="flex flex-col gap-4 rounded-[20px] border border-border bg-card p-5">
        <Section title="What went wrong">
          <p className="font-serif text-[15.5px] leading-relaxed text-pretty break-words whitespace-pre-wrap text-foreground/90">{d.cause}</p>
        </Section>
        {d.affected_files.length > 0 && (
          <Section title="Where">
            <span className="flex flex-wrap gap-1.5">
              {d.affected_files.map((f) => (
                <span key={f} className="rounded-lg bg-background px-2.5 py-1 font-mono text-[12.5px] break-all">
                  {f}
                </span>
              ))}
            </span>
          </Section>
        )}
        <Section title="What I'd do">
          <p className="font-serif text-[15.5px] leading-relaxed text-pretty break-words whitespace-pre-wrap text-foreground/90">{d.proposed_fix}</p>
        </Section>
      </div>
      {outdatedSha && (
        <span className="text-[13px] text-primary">
          This is about commit {shortSha(outdatedSha)}. The failing commit is now {shortSha(headSha)}.
        </span>
      )}
      <span className="text-[13px] text-muted-foreground">
        Please check this before you act on it. I could only read; I didn't change anything.
      </span>
      {(runId || onDiagnoseAgain) && (
        <div className="flex flex-wrap gap-2">
          {runId && (
            <Link to={`/runs/${encodeURIComponent(runId)}`} className={buttonVariants({ variant: 'outline', size: 'sm' })}>
              See my work
            </Link>
          )}
          {onDiagnoseAgain && (
            <Button variant="outline" size="sm" disabled={busy} onClick={onDiagnoseAgain}>
              Diagnose again
            </Button>
          )}
        </div>
      )}
    </>
  )
}
```

(`React.ReactNode` needs `import type { ReactNode } from 'react'` instead of the `React.` namespace: use the import.)

- [ ] **Step 2: The approval ask**

`web/src/components/conversation/ApprovalAsk.tsx`:

```tsx
import { useState } from 'react'
import { api, ApiError } from '@/api.ts'
import type { ToolCall } from '@/api.ts'
import { argumentList } from '@/approvals.ts'
import { askText } from '@/ask.ts'
import { useToast } from '@/toast.ts'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'

interface Props {
  call: ToolCall
  /** compact: in a thread or a run, the reason opens on demand. large: on Needs you, the reason field is always there. */
  variant?: 'compact' | 'large'
  /** Called after every decision, whatever happened: the state may have changed under the page. */
  onChanged: () => void
}

/**
 * A call that waits for a decision. The question is Remedy's sentence (a fixed table); under it the arguments of the call exactly as
 * stored: what is shown is what runs when it is approved. The values are the agent's text and are only shown.
 */
export default function ApprovalAsk({ call, variant = 'compact', onChanged }: Props) {
  const toast = useToast()
  const [reason, setReason] = useState('')
  const [reasonOpen, setReasonOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const ask = askText(call)
  const large = variant === 'large'

  async function decide(approve: boolean) {
    setBusy(true)
    setError('')
    try {
      await api.decideApproval(call.id, approve, reason.trim() || undefined)
      toast.show(approve ? 'Approved. Running…' : 'Denied. I told the agent.')
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Could not record the decision')
    } finally {
      setBusy(false)
      onChanged()
    }
  }

  return (
    <div className="flex flex-col gap-3">
      <p className="font-serif text-[clamp(18px,2vw,21px)] leading-normal text-pretty break-words">{ask.question}</p>
      <div className={cn('flex flex-col gap-3.5 rounded-[20px] border bg-card', call.waiting ? 'border-primary' : 'border-border', large ? 'p-4.5' : 'p-4')}>
        <span className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
          <span className="font-mono text-[13px] break-all text-foreground">{call.tool}</span>
          {call.waiting && 'exactly this runs if you approve'}
        </span>
        <dl className="grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-[13px]">
          {argumentList(call.arguments).map(({ name, value }) => (
            <div key={name} className="contents">
              <dt className="text-subtle">{name}</dt>
              <dd className="break-words whitespace-pre-wrap">{value}</dd>
            </div>
          ))}
        </dl>
        {call.waiting ? (
          <>
            {(large || reasonOpen) && (
              <Input
                aria-label="Reason (optional)"
                placeholder="Add a reason (optional)"
                maxLength={500}
                value={reason}
                onChange={(e) => setReason(e.target.value)}
                className="h-11 bg-background text-sm"
              />
            )}
            {error && (
              <span role="alert" className="text-[13px] text-destructive">
                {error}
              </span>
            )}
            <div className="flex flex-wrap items-center gap-2">
              <Button disabled={busy} onClick={() => void decide(true)} className={large ? 'h-11 flex-1' : undefined}>
                {ask.yes}
              </Button>
              <Button variant="outline" disabled={busy} onClick={() => void decide(false)} className={large ? 'h-11 flex-1' : undefined}>
                No
              </Button>
              {!large && !reasonOpen && (
                <button
                  type="button"
                  onClick={() => setReasonOpen(true)}
                  className="h-10 rounded-full px-2.5 text-[13px] text-muted-foreground outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
                >
                  Add a reason…
                </button>
              )}
            </div>
          </>
        ) : (
          <p className="text-[13px] text-muted-foreground">
            The agent is no longer waiting for this call, so it can no longer be decided. It is marked as abandoned shortly.
          </p>
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 3: The composer**

`web/src/components/conversation/Composer.tsx`:

```tsx
import { ArrowUp } from 'lucide-react'
import { useState } from 'react'
import type { FormEvent } from 'react'
import { ApiError } from '@/api.ts'
import { cn } from '@/lib/utils'

interface Props {
  placeholder: string
  /** Sends the text. It rejects with an ApiError when the server refuses; the message shows under the field. */
  onSend: (text: string) => Promise<void>
}

/** The field at the bottom of a conversation. */
export default function Composer({ placeholder, onSend }: Props) {
  const [text, setText] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const ready = text.trim() !== '' && !busy

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (!ready) return
    setBusy(true)
    setError('')
    try {
      await onSend(text.trim())
      setText('')
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not send that')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="mt-auto flex flex-col gap-2">
      <form onSubmit={(e) => void submit(e)} className="flex items-center gap-2.5 rounded-[28px] border border-input bg-card py-1.5 pr-1.5 pl-5">
        <input
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={placeholder}
          aria-label={placeholder}
          maxLength={4000}
          className="h-10 min-w-0 flex-1 border-0 bg-transparent text-sm text-foreground outline-none placeholder:text-subtle"
        />
        <button
          type="submit"
          disabled={!ready}
          aria-label="Send"
          className={cn(
            'flex size-10 shrink-0 items-center justify-center rounded-full text-primary-foreground outline-none focus-visible:ring-3 focus-visible:ring-ring/50',
            ready ? 'bg-primary' : 'bg-input',
          )}
        >
          <ArrowUp className="size-4.5" aria-hidden />
        </button>
      </form>
      {error && (
        <span role="alert" className="pl-5 text-[13px] text-destructive">
          {error}
        </span>
      )}
    </div>
  )
}
```

- [ ] **Step 4: Verify and commit**

Run: `npm test --prefix web`, `npm run lint --prefix web`, `npm run build --prefix web`. Expected: pass, lint prints nothing.

```sh
git add web/src
git commit -m "feat(web): the diagnosis message, the approval ask and the composer" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 4: The thread

**Files:**
- Create: `web/src/useIncidentThread.ts`, `web/src/IncidentThreadPage.tsx`
- Modify: `web/src/App.tsx`
- Delete: `web/src/IncidentView.tsx`, `web/src/DiagnosisCard.tsx`, and `web/src/StateBadge.tsx`, `web/src/SourceBadge.tsx` if nothing imports them any more

**Interfaces:**
- Consumes: Tasks 1 to 3; `useShellState`, `useShellHeader`, `useToast`, `api.getIncident`, `api.listIncidentRuns`, `api.ignoreIncident`, `api.unignoreIncident`, `api.diagnoseIncident`, `api.createRun`, `RefLink`, `safeUrl`, `externalLinkClass`, `timeAgo`, `timeOfDay`, `Skeleton`, `Alert`.
- Produces: `useIncidentThread(id)`, the default export `IncidentThreadPage({ id })`, the route `incidents/:id` outside `LegacyPage`.

- [ ] **Step 1: The hook**

`web/src/useIncidentThread.ts`:

```ts
import { useCallback, useEffect, useRef, useState } from 'react'
import { api, ApiError } from './api.ts'
import type { IncidentDetail, Run } from './api.ts'

const SLOW_MS = 5000
const FAST_MS = 2000

export interface ThreadData {
  detail: IncidentDetail | null
  /** The runs of the incident (its responder runs and the questions), newest first. */
  runs: Run[]
  /** The message of the last failed load; what was loaded before is kept. */
  error: string
  /** The server does not know this incident. */
  missing: boolean
  /** Loads again now, for example after the maintainer did something. */
  reload: () => Promise<void>
}

/** An incident with its history and its runs. It refreshes every five seconds, every two while a diagnosis or a question runs. */
export function useIncidentThread(id: number): ThreadData {
  const [detail, setDetail] = useState<IncidentDetail | null>(null)
  const [runs, setRuns] = useState<Run[]>([])
  const [error, setError] = useState('')
  const [missing, setMissing] = useState(false)
  const fast = useRef(false)
  const alive = useRef(true)

  const load = useCallback(async () => {
    try {
      const [d, r] = await Promise.all([api.getIncident(id), api.listIncidentRuns(id)])
      if (!alive.current) return
      setDetail(d)
      setRuns(r)
      setError('')
      fast.current = d.incident.state === 'diagnosing' || r.some((x) => x.status === 'queued' || x.status === 'running')
    } catch (e) {
      if (!alive.current) return
      if (e instanceof ApiError && e.status === 404) setMissing(true)
      else setError(e instanceof ApiError ? e.message : 'Could not load the incident')
    }
  }, [id])

  useEffect(() => {
    alive.current = true
    let timer: ReturnType<typeof setTimeout> | undefined
    const tick = async () => {
      await load()
      if (alive.current) timer = setTimeout(() => void tick(), fast.current ? FAST_MS : SLOW_MS)
    }
    void tick()
    return () => {
      alive.current = false
      clearTimeout(timer)
    }
  }, [load])

  return { detail, runs, error, missing, reload: load }
}
```

- [ ] **Step 2: The page**

`web/src/IncidentThreadPage.tsx`:

```tsx
import { useMemo, useState } from 'react'
import { Link } from 'react-router'
import { api, ApiError } from './api.ts'
import type { Incident, Run } from './api.ts'
import { conclusionText, externalLinkClass, reasonText, refLabel, safeUrl, shortSha, sourceLabel, timeAgo } from './incidents.ts'
import { useShellHeader } from './shellContext.ts'
import { useShellState } from './shellContext.ts'
import { buildThread } from './thread.ts'
import type { ThreadItem } from './thread.ts'
import { timeOfDay } from './timeline.ts'
import { useIncidentThread } from './useIncidentThread.ts'
import { useToast } from './toast.ts'
import RefLink from './RefLink.tsx'
import ApprovalAsk from '@/components/conversation/ApprovalAsk'
import Composer from '@/components/conversation/Composer'
import DiagnosisMessage from '@/components/conversation/DiagnosisMessage'
import EventPill from '@/components/conversation/EventPill'
import FailCard from '@/components/conversation/FailCard'
import RemedyMessage from '@/components/conversation/RemedyMessage'
import UserBubble from '@/components/conversation/UserBubble'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button, buttonVariants } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { runFailure } from './runview.ts'
import { incidentStateColor, incidentStateText } from './incidents.ts'
import { cn } from '@/lib/utils'

const when = (iso: string) => new Date(iso).toLocaleString()

const undiagnosedText = {
  fresh: "I haven't looked at this yet. I diagnose real failures on my own, within my limits. You can also start it by hand.",
  manual: "Cancelled and action-required results aren't diagnosed automatically. You can start it by hand.",
} as const

/** The incident as a conversation: its history, Remedy's diagnosis, what the maintainer asked, what waits for a decision. */
export default function IncidentThreadPage({ id }: { id: number }) {
  const { detail, runs, error, missing, reload } = useIncidentThread(id)
  const { asks } = useShellState()
  const toast = useToast()
  const incident = detail?.incident
  useShellHeader(incident ? { title: incident.title, back: '/incidents' } : null)
  const [diagnoseBusy, setDiagnoseBusy] = useState(false)
  const [diagnoseError, setDiagnoseError] = useState('')
  const [panelError, setPanelError] = useState('')

  const items = useMemo(
    () =>
      incident && detail
        ? buildThread({ incident, activity: detail.activity, questionRuns: runs.filter((r) => r.role === 'adhoc'), asks })
        : [],
    [incident, detail, runs, asks],
  )

  if (missing) {
    return (
      <div className="mx-auto flex max-w-190 flex-col gap-3 px-4 py-10 md:px-10">
        <h1 className="font-serif text-3xl font-normal">I can't find that incident.</h1>
        <Link to="/incidents" className="text-primary hover:text-primary-hover">
          Back to the conversations
        </Link>
      </div>
    )
  }

  async function diagnose() {
    setDiagnoseBusy(true)
    setDiagnoseError('')
    try {
      await api.diagnoseIncident(id)
      await reload()
    } catch (e) {
      setDiagnoseError(e instanceof ApiError ? e.message : 'Could not start the diagnosis')
    } finally {
      setDiagnoseBusy(false)
    }
  }

  async function ignore() {
    setPanelError('')
    try {
      await api.ignoreIncident(id)
      await reload()
      toast.show(`Ignored #${id}.`, () => {
        void api
          .unignoreIncident(id)
          .then(reload)
          .catch((e: unknown) => setPanelError(e instanceof ApiError ? e.message : 'Could not stop ignoring the incident'))
      })
    } catch (e) {
      setPanelError(e instanceof ApiError ? e.message : 'Could not ignore the incident')
    }
  }

  async function unignore() {
    setPanelError('')
    try {
      await api.unignoreIncident(id)
      await reload()
      toast.show(`Stopped ignoring #${id}.`)
    } catch (e) {
      setPanelError(e instanceof ApiError ? e.message : 'Could not stop ignoring the incident')
    }
  }

  async function ask(text: string) {
    await api.createRun(text, true, false, id)
    await reload()
  }

  const diagnosing = incident?.state === 'diagnosing'

  function renderItem(item: ThreadItem, inc: Incident) {
    switch (item.type) {
      case 'event':
        return (
          <EventPill key={item.key} dot={item.dot}>
            {item.text}
          </EventPill>
        )
      case 'working':
        return (
          <RemedyMessage key={item.key} kind="working" meta={`Remedy · ${timeOfDay(item.at)}`}>
            <span className="text-[13px] text-muted-foreground">
              Reading the failure log and the repository. I can only read; I can't change anything.
            </span>
            {item.runId && (
              <div>
                <Link to={`/runs/${encodeURIComponent(item.runId)}`} className={buttonVariants({ variant: 'outline', size: 'sm' })}>
                  Follow along
                </Link>
              </div>
            )}
          </RemedyMessage>
        )
      case 'diagnosis':
        return (
          <RemedyMessage key={item.key} meta={`Remedy · ${timeOfDay(item.at)} · read the failure log and the repository at ${shortSha(item.sha)}`}>
            <DiagnosisMessage
              diagnosis={item.diagnosis}
              outdatedSha={item.outdated ? item.sha : undefined}
              headSha={inc.headSha}
              runId={item.runId}
              busy={diagnoseBusy || diagnosing}
              onDiagnoseAgain={inc.state === 'open' || inc.state === 'diagnosed' ? () => void diagnose() : undefined}
            />
            {diagnoseError && (
              <span role="alert" className="text-[13px] text-destructive">
                {diagnoseError}
              </span>
            )}
          </RemedyMessage>
        )
      case 'undiagnosed':
        return (
          <RemedyMessage key={item.key} meta={`Remedy · ${timeOfDay(item.at)}`}>
            {item.reason === 'source' ? (
              <p className="font-serif text-[17px] leading-normal text-pretty">I can't diagnose incidents from {item.sourceLabel} yet.</p>
            ) : (
              <>
                <p className="font-serif text-[17px] leading-normal text-pretty">{undiagnosedText[item.reason]}</p>
                {diagnoseError && (
                  <span role="alert" className="text-[13px] text-destructive">
                    {diagnoseError}
                  </span>
                )}
                <div>
                  <Button disabled={diagnoseBusy} onClick={() => void diagnose()}>
                    {item.reason === 'manual' ? 'Diagnose' : 'Diagnose now'}
                  </Button>
                </div>
              </>
            )}
          </RemedyMessage>
        )
      case 'ask':
        return (
          <RemedyMessage key={item.key} kind="ask" meta={`Remedy · asked ${timeAgo(item.call.requestedAt)}`}>
            <ApprovalAsk call={item.call} onChanged={() => void reload()} />
          </RemedyMessage>
        )
      case 'question':
        return <UserBubble key={item.key}>{item.run.prompt}</UserBubble>
      case 'answer':
        return <AnswerMessage key={item.key} run={item.run} />
    }
  }

  return (
    <div className="flex min-h-full flex-wrap items-start">
      <div className="flex min-h-full max-w-220 min-w-0 flex-[1_1_520px] flex-col gap-5 px-4 py-5 md:px-11 md:py-10">
        {error && !detail && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        {!incident ? (
          !error && (
            <div className="flex flex-col gap-4">
              <Skeleton className="h-8 w-3/5" />
              <Skeleton className="h-24" />
              <Skeleton className="h-40" />
            </div>
          )
        ) : (
          <>
            <div className="flex flex-col gap-1">
              <span className="hidden text-[13px] text-muted-foreground md:block">
                {incident.source === 'github' ? `${incident.repo} · ${refLabel(incident.ref)}` : sourceLabel(incident.source)} · Incident #{incident.id}
              </span>
              <h1 className="font-serif text-3xl font-normal break-words max-md:sr-only">{incident.title}</h1>
            </div>
            {items.map((item) => renderItem(item, incident))}
            <Composer placeholder="Ask Remedy about this incident…" onSend={ask} />
          </>
        )}
      </div>

      {incident && (
        <aside className="m-4 flex min-w-65 flex-[0_1_280px] flex-col gap-3.5 rounded-[20px] border border-border bg-sidebar p-5 md:m-7">
          <span className="text-xs font-semibold text-muted-foreground">About this incident</span>
          <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-2 text-[13px]">
            <dt className="text-subtle">State</dt>
            <dd className={cn('flex items-center gap-2', incidentStateText[incident.state])}>
              <span aria-hidden className={cn('size-2 rounded-full', incidentStateColor[incident.state])} />
              {incident.state}
            </dd>
            <dt className="text-subtle">Conclusion</dt>
            <dd>{conclusionText(incident.conclusion)}</dd>
            {incident.source === 'github' && (
              <>
                <dt className="text-subtle">Commit</dt>
                <dd className="font-mono text-[12.5px]">{shortSha(incident.headSha)}</dd>
              </>
            )}
            <dt className="text-subtle">Occurrences</dt>
            <dd>{incident.occurrences}</dd>
            <dt className="text-subtle">First seen</dt>
            <dd>{when(incident.firstSeen)}</dd>
            <dt className="text-subtle">Last seen</dt>
            <dd>{when(incident.lastSeen)}</dd>
            {incident.resolvedAt && (
              <>
                <dt className="text-subtle">Resolved</dt>
                <dd>
                  {when(incident.resolvedAt)} ({reasonText(incident.resolvedReason)})
                </dd>
              </>
            )}
          </dl>
          <div className="flex flex-col gap-1.5 text-[13px]">
            {incident.source === 'github' && <RefLink refName={incident.ref} url={incident.refUrl} />}
            {safeUrl(incident.checkUrl) && (
              <a href={safeUrl(incident.checkUrl)} target="_blank" rel="noreferrer" className={cn(externalLinkClass, 'text-primary')}>
                {incident.source === 'github' ? 'Open the check run ↗' : 'Open the source ↗'}
              </a>
            )}
          </div>
          {incident.details && Object.keys(incident.details).length > 0 && (
            <details className="text-[13px]">
              <summary className="cursor-pointer rounded-sm text-muted-foreground outline-none focus-visible:ring-3 focus-visible:ring-ring/50">Signal</summary>
              <pre className="mt-2 max-h-80 overflow-auto font-mono text-xs break-words whitespace-pre-wrap text-subtle">
                {JSON.stringify(incident.details, null, 2)}
              </pre>
            </details>
          )}
          {panelError && (
            <span role="alert" className="text-[13px] text-destructive">
              {panelError}
            </span>
          )}
          {incident.state === 'ignored' ? (
            <Button variant="outline" size="sm" className="self-start" onClick={() => void unignore()}>
              Stop ignoring
            </Button>
          ) : (
            (incident.state === 'open' || incident.state === 'diagnosing' || incident.state === 'diagnosed') && (
              <Button variant="outline" size="sm" className="self-start" onClick={() => void ignore()}>
                Ignore
              </Button>
            )
          )}
        </aside>
      )}
    </div>
  )
}

/** Remedy's answer to a question: working, the text, or why it failed. The steps are on the run page. */
function AnswerMessage({ run }: { run: Run }) {
  const failure = runFailure(run)
  const phase = run.status
  return (
    <RemedyMessage kind={phase === 'queued' || phase === 'running' ? 'working' : 'idle'} meta={`Remedy · ${timeOfDay(run.finishedAt ?? run.startedAt ?? run.createdAt)}`}>
      {phase === 'queued' && <span className="text-[13px] text-muted-foreground">Waiting for the runner…</span>}
      {phase === 'running' && <span className="text-[13px] text-muted-foreground">Working…</span>}
      {phase === 'succeeded' && (
        <p className="font-serif text-[clamp(16.5px,1.9vw,19px)] leading-relaxed text-pretty break-words whitespace-pre-wrap">{run.result}</p>
      )}
      {failure && <FailCard title={failure.title}>{failure.text}</FailCard>}
      <div>
        <Link to={`/runs/${encodeURIComponent(run.id)}`} className={buttonVariants({ variant: 'outline', size: 'sm' })}>
          {phase === 'running' || phase === 'queued' ? 'Follow along' : 'See my work'}
        </Link>
      </div>
    </RemedyMessage>
  )
}
```

(Tidy the imports: merge the two imports of `shellContext.ts` and the two of `incidents.ts` into one each, and make sure no import is unused; the compiler and lint decide.)

- [ ] **Step 3: The route and the deletions**

In `web/src/App.tsx` keep `IncidentRoute` (it validates the id) but let it render `<IncidentThreadPage key={id} id={id} />` instead of `IncidentView`; import `IncidentThreadPage from './IncidentThreadPage.tsx'` and remove the `IncidentView` import. Move the `incidents/:id` route out of the `LegacyPage` group so it sits next to `incidents`:

```tsx
        <Route path="incidents" element={<ConversationsPage />} />
        <Route path="incidents/:id" element={<IncidentRoute />} />
```

`git rm web/src/IncidentView.tsx web/src/DiagnosisCard.tsx`. Then `grep -rn "StateBadge\|SourceBadge" web/src`: delete `StateBadge.tsx` and `SourceBadge.tsx` with `git rm` when nothing imports them any more. `RefLink.tsx` stays (the panel uses it).

- [ ] **Step 4: Verify and commit**

Run: `npm test --prefix web`, `npm run lint --prefix web`, `npm run build --prefix web`. Expected: all pass, lint prints nothing.

```sh
git add web/src
git commit -m "feat(web): the incident as a conversation" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 5: The run

**Files:**
- Create: `web/src/useRun.ts`, `web/src/RunPage.tsx`
- Modify: `web/src/App.tsx`, `web/src/ToolCallsCard.tsx`
- Delete: `web/src/RunView.tsx`

**Interfaces:**
- Consumes: Tasks 1 to 3; `api.getRun`, `api.listToolCalls`, `api.cancelRun`, `api.createRun`, `streamRun`, `ConfirmButton`, `ToolCallsCard`.
- Produces: `useRun(id)`, the default export `RunPage({ id })`, the route `runs/:id` outside `LegacyPage`.

- [ ] **Step 1: The hook**

`web/src/useRun.ts`:

```ts
import { useCallback, useEffect, useState } from 'react'
import { api, streamRun } from './api.ts'
import type { Run, RunEvent, ToolCall } from './api.ts'
import { effectiveStatus } from './runview.ts'

/**
 * A run with its events (live), its calls and its status. The run and its calls are fetched again every three seconds while the run
 * can still change (it may start waiting for an approval, or be cancelled), and once more when it has ended.
 */
export function useRun(id: string) {
  const [run, setRun] = useState<Run | null>(null)
  const [events, setEvents] = useState<RunEvent[]>([])
  const [calls, setCalls] = useState<ToolCall[]>([])

  const refresh = useCallback(() => {
    api.getRun(id).then(setRun).catch(() => undefined)
    api.listToolCalls(id).then(setCalls).catch(() => undefined)
  }, [id])

  const ended = run?.status === 'succeeded' || run?.status === 'failed'

  useEffect(() => {
    refresh()
    if (ended) return
    const timer = setInterval(refresh, 3000)
    return () => clearInterval(timer)
  }, [refresh, ended])

  useEffect(
    () => streamRun(id, (e) => setEvents((prev) => (prev.some((p) => p.seq === e.seq) ? prev : [...prev, e])), setRun),
    [id],
  )

  const status = run ? effectiveStatus(run.status, events.length) : undefined
  return { run, events, calls, status, ended, refresh }
}
```

- [ ] **Step 2: The page**

`web/src/RunPage.tsx`:

```tsx
import { useMemo, useState } from 'react'
import { Link, useNavigate } from 'react-router'
import { api, ApiError } from './api.ts'
import { canCancel, runFailure, runMeta, runSteps, summarizeEvent } from './runview.ts'
import { useShellHeader } from './shellContext.ts'
import { phaseView, runPhase } from './status.ts'
import { timeOfDay } from './timeline.ts'
import { useRun } from './useRun.ts'
import ToolCallsCard from './ToolCallsCard.tsx'
import ApprovalAsk from '@/components/conversation/ApprovalAsk'
import FailCard from '@/components/conversation/FailCard'
import RemedyMessage from '@/components/conversation/RemedyMessage'
import StepCard from '@/components/conversation/StepCard'
import UserBubble from '@/components/conversation/UserBubble'
import ConfirmButton from '@/components/ConfirmButton'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button, buttonVariants } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

const summaryClass = 'cursor-pointer rounded-sm text-[13px] text-muted-foreground outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50'

/** One run as a conversation: what was asked, what the agent did, what it answered, and what waits for a decision. */
export default function RunPage({ id }: { id: string }) {
  const navigate = useNavigate()
  const { run, events, calls, status, ended, refresh } = useRun(id)
  useShellHeader(run ? { title: 'Run', back: run.incidentId !== undefined ? `/incidents/${run.incidentId}` : '/runs' } : null)
  const [error, setError] = useState('')
  const steps = useMemo(() => runSteps(events), [events])
  const waitingCall = calls.find((c) => c.decision === 'pending')

  async function cancel() {
    setError('')
    try {
      await api.cancelRun(id)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Could not cancel the run')
    }
    refresh()
  }

  async function again() {
    if (!run) return
    setError('')
    try {
      const created = await api.createRun(run.prompt, run.mcp === true, run.cluster === true, run.incidentId)
      void navigate(`/runs/${created.id}`)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Could not start the run')
    }
  }

  if (!run || !status) {
    return (
      <div className="mx-auto flex max-w-190 flex-col gap-4 px-4 py-5 md:px-10 md:py-12">
        <h1 className="sr-only">Run</h1>
        <Skeleton className="h-8 w-2/5" />
        <Skeleton className="h-24" />
      </div>
    )
  }

  const phase = runPhase(status, waitingCall !== undefined && !ended)
  const view = phaseView[phase]
  const failure = runFailure(run)
  const responder = run.role === 'responder'
  const time = timeOfDay(run.startedAt ?? run.createdAt)

  return (
    <div className="mx-auto flex max-w-190 flex-col gap-5 px-4 py-5 md:px-10 md:py-12">
      <h1 className="sr-only">Run</h1>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <span className={cn('flex items-center gap-2 rounded-2xl px-3 py-1.5 text-[13px] font-semibold', view.soft, view.text)}>
          <span aria-hidden className={cn('size-2 rounded-full', view.dot)} />
          {view.label}
        </span>
        <span className="text-[13px] text-muted-foreground">{runMeta(run, time)}</span>
        {run.incidentId !== undefined && (
          <Link to={`/incidents/${run.incidentId}`} className="text-[13px] text-primary hover:text-primary-hover">
            Incident #{run.incidentId}
          </Link>
        )}
        {canCancel(run, status) && (
          <span className="sm:ml-auto">
            <ConfirmButton label="Cancel run" confirmLabel="Confirm cancel" onConfirm={() => void cancel()} />
          </span>
        )}
      </div>

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {responder ? (
        <details className="rounded-2xl border border-border bg-card p-3.5">
          <summary className={summaryClass}>Prompt ({run.prompt.length.toLocaleString()} characters, it contains data from GitHub)</summary>
          <p className="mt-2 font-mono text-xs break-words whitespace-pre-wrap text-subtle">{run.prompt}</p>
        </details>
      ) : (
        <UserBubble>{run.prompt}</UserBubble>
      )}

      <RemedyMessage kind={phase === 'waiting' ? 'ask' : phase === 'working' || phase === 'queued' ? 'working' : 'idle'} meta={`Remedy · ${time}`}>
        {steps.map((s) => (
          <StepCard key={s.id} step={s} />
        ))}
        {run.status === 'succeeded' &&
          (responder ? (
            <p className="font-serif text-[clamp(16.5px,1.9vw,19px)] leading-relaxed">
              Done. The diagnosis is on the incident.
              {run.incidentId !== undefined && (
                <>
                  {' '}
                  <Link to={`/incidents/${run.incidentId}`} className="text-primary hover:text-primary-hover">
                    Read the diagnosis
                  </Link>
                </>
              )}
            </p>
          ) : (
            <p className="font-serif text-[clamp(16.5px,1.9vw,19px)] leading-relaxed text-pretty break-words whitespace-pre-wrap">{run.result}</p>
          ))}
        {waitingCall && !ended && <ApprovalAsk call={waitingCall} onChanged={refresh} />}
        {failure && <FailCard title={failure.title}>{failure.text}</FailCard>}
        {phase === 'queued' && <span className="text-[13px] text-muted-foreground">Waiting for the runner…</span>}
        {phase === 'working' && <span className="text-[13px] text-muted-foreground">Working…</span>}
        {run.cancelRequested && !ended && <span className="text-[13px] text-primary">Cancelling: the runner is stopping the agent.</span>}
        {failure &&
          (responder ? (
            run.incidentId !== undefined && (
              <div>
                <Link to={`/incidents/${run.incidentId}`} className={buttonVariants({ variant: 'outline', size: 'sm' })}>
                  Back to the incident
                </Link>
              </div>
            )
          ) : (
            <div>
              <Button size="sm" onClick={() => void again()}>
                Start it again
              </Button>
            </div>
          ))}
      </RemedyMessage>

      <details>
        <summary className={summaryClass}>Show raw output ({events.length})</summary>
        <ol className="mt-2 flex flex-col gap-1.5 font-mono text-xs">
          {events.map((e) => (
            <li key={e.seq} className="rounded-xl border border-border bg-card/50 p-2">
              <span className="mr-2 rounded-full bg-secondary px-2 py-0.5 text-subtle">{e.kind}</span>
              <span className="break-words whitespace-pre-wrap text-muted-foreground">{summarizeEvent(e)}</span>
            </li>
          ))}
        </ol>
      </details>
      {calls.length > 0 && (
        <details>
          <summary className={summaryClass}>Tool calls ({calls.length})</summary>
          <div className="mt-2">
            <ToolCallsCard calls={calls} />
          </div>
        </details>
      )}
    </div>
  )
}
```

(Merge duplicate imports and drop unused ones as the compiler and lint demand.)

- [ ] **Step 3: The route, the audit card and the deletion**

In `web/src/App.tsx`: `RunRoute` renders `<RunPage key={id} id={id} />` (import `RunPage from './RunPage.tsx'`, remove the `RunView` import) and the `runs/:id` route moves out of the `LegacyPage` group, next to `incidents/:id`. `git rm web/src/RunView.tsx`; `grep -rn "RunView" web/src` must find nothing.

In `docs/specs/2026-10-05-ui-conversation-redesign-design.md` section 6.1 change the bullet "**A question and its answer:** the question as the user's bubble; the answer as a Remedy message with steps and text, or a working indicator." to "**A question and its answer:** the question as the user's bubble; the answer as a Remedy message with its text, a working indicator or the failure, and a link to the run (the steps of the run are on the run page: showing them here would need one event stream per question)." (read the file first; change only that bullet).

In `web/src/ToolCallsCard.tsx` change the hard-coded classes to tokens: `divide-border` stays; any `text-amber-*`, `bg-slate-*`, `bg-rose-*`, `bg-emerald-*` becomes the matching token (`text-primary`, `bg-neutral`, `bg-destructive`, `bg-success`); the dots already use `callStatusColor` (tokens since Task 1).

- [ ] **Step 4: Verify and commit**

Run: `npm test --prefix web`, `npm run lint --prefix web`, `npm run build --prefix web`. Expected: all pass, lint prints nothing.

```sh
git add web/src
git commit -m "feat(web): the run as a conversation" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Verification (run by the controller, in a real browser)

No code changes unless a defect is found (a defect goes to a fix dispatch). Nothing here is committed.

- [ ] **Step 1: Full checks.** `npm ci --prefix web` if needed; from the worktree root `make check` and `go test -tags webui ./web/` after `make web-build`. Expected: PASS.

- [ ] **Step 2: Run the app** with a throwaway database in the scratchpad (server with `REMEDY_ADMIN_PASSWORD`, `REMEDY_RUNNER_TOKEN`, `REMEDY_MASTER_KEY`, `REMEDY_DB`; `npm run dev --prefix web -- --host 127.0.0.1`). Seed (nine fractional digits in timestamps; repos disabled so the poller stays quiet; see the part-2 seed for the shape): incidents of every state and three sources; for a GitHub incident a stored diagnosis with `diagnosed_sha` equal to and different from `head_sha` (two incidents), an `activity` history (opened, recurred, `diagnosis_finished`, `note_added`, `incident_ignored`), a waiting `tool_calls` row for it, a responder run (`role = 'responder'`) and an ad-hoc question run with `incident_id` (succeeded with a `result`; one failed with `failure_reason = 'runner_lost'`; one queued), a waiting call whose `incident_id` is NULL but whose `run_id` is the question run, run events (`run_events`: an assistant `tool_use` and a user `tool_result` with a string and with a text-block result, an error result and a `StructuredOutput` call).

- [ ] **Step 3: Check the thread** at 1280x900 and 390x844 for: a diagnosed GitHub incident (the diagnosis message with confidence bars, category, "Small mechanical fix", the card, the files, the notes, "See my work", "Diagnose again", the outdated note for the other commit), an open fresh one ("Diagnose now"), a manual one ("Diagnose"), an alert incident ("I can't diagnose incidents from Alertmanager yet." and no button), a diagnosing one ("Follow along"), a resolved and an ignored one; the pills in time order; the question bubble and its answer (text, working, queued, failure card); both waiting approvals show in the thread (the one with the incident id and the one that only has the run); Yes with the reason opened and No; the composer sends a question (with the fake `claude` or a refused one: "the gatekeeper tools are not enabled" shows under the field when the server has none); Ignore shows the toast with Undo, Undo restores the state, "Stop ignoring" works; the panel rows and links; the signal block for an alert; a missing incident id shows "I can't find that incident."; one `h1` per page (phone: the top bar title is not a heading); no horizontal overflow at 390 px with a 150-character title and a long file path; keyboard focus rings on every button, link and `summary`.

- [ ] **Step 4: Check the run** at both widths: a succeeded ad-hoc run (user bubble, steps with labels and raw lines, the answer), a responder run (collapsed prompt, "Done. The diagnosis is on the incident." with the link), a run that waits for an approval (status "Waiting for you", the ask inside the message, the keyboard answers it), a failed run per reason with the right title and "Start it again" (ad-hoc) or "Back to the incident" (responder), a queued run, Cancel run (two-step), the raw output and tool calls blocks, an event list with odd payloads does not break the page.

- [ ] **Step 5: Check the toast** (Ignore with Undo): it is announced (the live region is in the page before the toast), the timer stops while the pointer is over it or the focus is on Undo and restarts after, a 120-character text wraps inside the pill at 390 px, and with `prefers-reduced-motion: reduce` the working avatar keeps a static ring.

- [ ] **Step 6: Clean up.** Stop the processes you started (by PID), delete the throwaway database, `.playwright-mcp/` and any `*.png`.

---

## Self-Review

**Spec coverage (spec 6):** 6.1 the thread: header and panel (Task 4), `buildThread` with pills from the activity and without `diagnosis_started`/`diagnosis_finished` (Task 1), working message with "Follow along", the diagnosis message with confidence bars, category, "Small mechanical fix", the card, the closing sentence, "See my work" and "Diagnose again", the soft note for another commit, "Diagnose now" and the source sentence, the waiting approval inline, the question and the answer, the composer with `{prompt, tools: true, incidentId}` staying in the thread, polling every 5 s and 2 s, the panel with Ignore and Undo and "Stop ignoring" and the signal. **Deviation, recorded:** the answer of a question run in the thread shows the text, the working state or the failure, and "Follow along" / "See my work"; its steps are on the run page (loading the events of each question run into the thread would need one stream per run). The spec's 6.1 line is updated accordingly in Task 5. 6.2 the run: status chip, meta, incident link, Cancel with `ConfirmButton`, collapsed responder prompt, steps from the events, the answer or the fixed sentence, the ask, the failure card per reason, "Start it again", "Show raw output" and "Tool calls". 6.3 the ask with both variants and the table `askText` (built in part 2). The carry-forward items of parts 0 to 2 for this part: persistent live region, pause on hover and focus, wrapping in the toast; the static ring of the working avatar; one `h1` per page; the 409 on diagnose shown in the thread; approvals of question runs matched by run; `StateBadge`/`SourceBadge` removed.

**Placeholder scan:** none. Task 4 and 5 tell the implementer to merge duplicate imports and drop unused ones: those are mechanical.

**Type consistency:** `buildThread` (Task 1) returns `ThreadItem` with the `type` values handled by `renderItem` (Task 4). `runSteps` returns `Step` used by `StepCard` (Task 2) and `RunPage` (Task 5). `phaseView`, `runPhase` (Task 1) are used by `RunPage`. `ApprovalAsk` takes `call: ToolCall`, `onChanged`; both pages pass them. `Composer.onSend(text) => Promise<void>` matches `ask` in the thread. `api.createRun` has the new fourth parameter used by the thread (`id`) and the run page (`run.incidentId`).

**Review Focus coverage:** 1 (Task 1 test of the source reason; Task 4 code: no button for `source`), 2 (Task 1 test), 3 (Task 1 tests), 4 (Task 1 tests), 5 (Task 4 code; Task 6), 6 (Task 4 code shows the API's message; Task 6), 7 (Task 2; Task 6), 8 and 9 (Tasks 2 to 5; Task 6).
