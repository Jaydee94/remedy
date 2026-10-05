# UI redesign, part 4: Needs you and Ask Remedy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the approvals page by "Needs you" (the waiting questions as Remedy's messages with the large approval ask, and "Already answered") and the runs page by "Ask Remedy" (a question field with tool chips and a list of earlier conversations).

**Architecture:** What the two pages show is built by pure, tested functions (`needs.ts`, `askpage.ts`, `status.ts`). The approvals page gets its own data hook (`useApprovals`, polling every second: a call must show within two seconds). The shared `ApprovalAsk` (part 3) is used in its large variant. Both routes leave `LegacyPage`.

**Tech Stack:** React 19, React Router 8, TypeScript 7, Tailwind 4, `node --test`.

**Spec:** [`docs/specs/2026-10-05-ui-conversation-redesign-design.md`](../specs/2026-10-05-ui-conversation-redesign-design.md), section 7 (and 2, 3.1, 3.2, 6.3, 9, 10). Earlier plans: `ui-1-foundation.md` to `ui-3-thread-and-run.md`; the building blocks of part 3 (`RemedyMessage`, `ApprovalAsk`, `EmptyState`) exist.

## Global Constraints

- Everything in the repo is English: code, comments, UI copy, docs, commit messages.
- `web/tsconfig.app.json` sets `erasableSyntaxOnly` and `verbatimModuleSyntax`: no enums or constructor parameter properties, `import type` for types.
- Pure logic modules (`needs.ts`, `askpage.ts`, `status.ts`, `approvals.ts`, `incidents.ts`) import only with relative paths and `.ts` extensions and contain only erasable TypeScript. Components may use the `@/` alias.
- **Untrusted text** (tool arguments, reasons, results and errors of calls, run prompts of ad-hoc runs) is rendered as React text only: never as HTML or Markdown. A sentence's structure never comes from such text: Remedy's sentences are templates filled with values.
- **A decision shows what runs:** the ask shows the arguments of the call exactly as stored (`ApprovalAsk` does that; do not build another card).
- **Nothing is shown optimistically** after a decision: the state comes from the poll.
- Palette and tokens of part 1; no new hard-coded palette colours (`bg-rose-500` and the like).
- Routes do not change. `approvals` and `runs` leave the `LegacyPage` wrapper; `settings` and the catch-all stay in it (part 5).
- Commits end with the line `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>` (exactly this model name).
- In a worktree-isolated session the Bash tool refuses commands that name git in a compound or variable form: use plain separate commands or a script file with literal paths (see `CLAUDE.md`); run npm with `--prefix web`. A fresh worktree has no `web/node_modules`: run `npm ci --prefix web` first. Use `command ls`. `npm run lint` must print no warnings.
- Work on a branch `feat/ui-4-needs-you-and-ask`, never on `main`.

## Review Focus

1. Two calls waiting at once: the older one is shown first (it is decided first); deciding one leaves the other untouched and the page never shows a decided call as waiting. (Task 1 test, Task 2.)
2. A call whose agent no longer waits (`waiting === false`, not yet marked abandoned): no buttons, the ask says so. (`ApprovalAsk`, Task 2.)
3. An approved action that failed is "approved, failed" in the answered list, in colour; a denied one is "denied"; an abandoned one "abandoned". (Task 1 test.)
4. The answered list shows the reason and the outcome text of agent-written calls as text, and a call without an incident or whose run is gone does not break the row. (Task 2.)
5. A run in the list that waits for an approval says "Waiting for you" (matched by `runId` from the shell's pending approvals); a responder run reads "Diagnosis of incident #N" and never shows its prompt (it holds GitHub data). (Task 1 test.)
6. Tool chips: "Read the cluster" switches the gatekeeper tools on; switching the tools off switches the cluster off; the cluster chip exists only when the server can read the cluster; the hint matches the state and uses the real namespaces. (Task 1 tests, Task 3.)
7. Sending: Cmd or Ctrl and Enter send, a plain Enter is a new line, an empty or whitespace question is not sent, a refusal of the server shows under the field and keeps the text, a second click while sending does nothing. (Task 3.)
8. 390 px: nothing scrolls sideways (long tool arguments, a long prompt in the list, a long reason); the large Yes and No buttons are at full width; each page has one `h1`. (Tasks 2, 3, 4.)
9. Keyboard: every control has a focus ring; a waiting question can be answered with the keyboard (reason field, Yes, No). (Task 4.)

---

## File Structure

| File | Change | Responsibility |
|---|---|---|
| `web/src/needs.ts` + `needs.test.ts` | create | `splitApprovals`, `decisionView`, `answeredAt` |
| `web/src/askpage.ts` + `askpage.test.ts` | create | the tool switches, the hint, `recentRun` |
| `web/src/status.ts` + `status.test.ts` | modify | `statusColor` is replaced by the phase view; the list uses `runPhase`/`phaseView` |
| `web/src/useApprovals.ts` | create | all approvals, polled every second |
| `web/src/NeedsYouPage.tsx`, `web/src/components/needs/AnsweredRow.tsx` | create | the page (replaces `ApprovalsPage.tsx` and `ApprovalCard.tsx`, deleted) |
| `web/src/AskPage.tsx`, `web/src/components/ask/RecentRunRow.tsx` | create | the page (replaces `RunsPage.tsx`, deleted) |
| `web/src/approvals.ts` | modify | `decisionLabel` stays (the audit card uses it) |
| `web/src/App.tsx` | modify | `approvals` and `runs` leave `LegacyPage` |

---

### Task 1: The logic (needs.ts and askpage.ts)

**Files:**
- Create: `web/src/needs.ts`, `web/src/needs.test.ts`, `web/src/askpage.ts`, `web/src/askpage.test.ts`

**Interfaces:**
- Consumes: `ToolCall`, `Run`, `Capabilities`, `RunStatus` from `api.ts`; `runPhase`, `phaseView`, `RunPhase` from `status.ts`.
- Produces: `splitApprovals(calls)`, `decisionView(call)`, `answeredAt(call)`, `toggleTools`, `toggleCluster`, `askHint`, `recentRun`, `AskSwitches`, `RecentRun`.

- [ ] **Step 1: Write the failing tests**

`web/src/needs.test.ts`:

```ts
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import type { ToolCall } from './api.ts'
import { answeredAt, decisionView, splitApprovals } from './needs.ts'

const call = (over: Partial<ToolCall> = {}): ToolCall =>
  ({ id: 1, runId: 'r', tool: 'cluster_rollout_restart', kind: 'mutating', arguments: {}, status: 'waiting', decision: 'pending', requestedAt: '2026-10-05T10:00:00Z', waiting: true, ...over }) as ToolCall

describe('splitApprovals', () => {
  it('puts what waits first, the oldest asked first, and the answered ones newest first', () => {
    const calls = [
      call({ id: 5, requestedAt: '2026-10-05T10:05:00Z' }),
      call({ id: 4, decision: 'approved', status: 'succeeded' }),
      call({ id: 3, requestedAt: '2026-10-05T10:01:00Z' }),
      call({ id: 2, decision: 'denied', status: 'denied' }),
    ]
    const { pending, answered } = splitApprovals(calls)
    assert.deepEqual(pending.map((c) => c.id), [3, 5])
    assert.deepEqual(answered.map((c) => c.id), [4, 2])
  })

  it('breaks a tie of the asking time by id and does not change its input', () => {
    const calls = [call({ id: 9 }), call({ id: 8 })]
    const { pending } = splitApprovals(calls)
    assert.deepEqual(pending.map((c) => c.id), [8, 9])
    assert.deepEqual(calls.map((c) => c.id), [9, 8])
  })

  it('handles an empty list', () => {
    assert.deepEqual(splitApprovals([]), { pending: [], answered: [] })
  })
})

describe('decisionView', () => {
  it('says approved, failed for an approved action that failed', () => {
    assert.equal(decisionView(call({ decision: 'approved', status: 'failed' })).label, 'approved, failed')
    assert.equal(decisionView(call({ decision: 'approved', status: 'failed' })).text, 'text-destructive')
  })
  it('says approved for an approved action that ran, and approved, running while it has not ended', () => {
    assert.equal(decisionView(call({ decision: 'approved', status: 'succeeded' })).label, 'approved')
    assert.equal(decisionView(call({ decision: 'approved', status: 'succeeded' })).text, 'text-success')
    assert.equal(decisionView(call({ decision: 'approved', status: 'running' })).label, 'approved, running')
    assert.equal(decisionView(call({ decision: 'approved', status: 'waiting' })).label, 'approved, running')
  })
  it('names the other decisions, each with a token colour', () => {
    assert.equal(decisionView(call({ decision: 'denied', status: 'denied' })).label, 'denied')
    assert.equal(decisionView(call({ decision: 'abandoned', status: 'abandoned' })).label, 'abandoned')
    assert.equal(decisionView(call({ decision: 'pending' })).label, 'waiting for you')
    assert.equal(decisionView(call({ decision: '', status: 'succeeded', kind: 'read' })).label, 'read')
    for (const decision of ['denied', 'abandoned', 'pending', ''] as const) {
      assert.match(decisionView(call({ decision })).text, /^text-(muted-foreground|primary|success|destructive)$/)
    }
  })
})

describe('answeredAt', () => {
  it('takes the time of the decision, then the end, then the request', () => {
    assert.equal(answeredAt(call({ decidedAt: 'd', finishedAt: 'f' })), 'd')
    assert.equal(answeredAt(call({ finishedAt: 'f' })), 'f')
    assert.equal(answeredAt(call({})), '2026-10-05T10:00:00Z')
  })
})
```

`web/src/askpage.test.ts`:

```ts
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import type { Capabilities, Run, ToolCall } from './api.ts'
import { askHint, recentRun, toggleCluster, toggleTools } from './askpage.ts'

const caps = (over: Partial<Capabilities['cluster']> = {}): Capabilities => ({ cluster: { read: true, write: true, namespaces: ['guestbook', 'monitoring'], ...over } })
const run = (over: Partial<Run> = {}): Run => ({ id: 'r1', provider: 'claude', prompt: 'why is it slow?', status: 'succeeded', result: '', sessionId: '', costUsd: 0, createdAt: '2026-10-05T09:00:00Z', role: 'adhoc', ...over }) as Run
const ask = (runId: string): ToolCall => ({ id: 1, runId, tool: 't', kind: 'mutating', arguments: {}, status: 'waiting', decision: 'pending', requestedAt: '2026-10-05T10:00:00Z', waiting: true }) as ToolCall

describe('the tool switches', () => {
  it('switching the cluster on switches the tools on', () => {
    assert.deepEqual(toggleCluster({ tools: false, cluster: false }), { tools: true, cluster: true })
    assert.deepEqual(toggleCluster({ tools: true, cluster: true }), { tools: true, cluster: false })
  })
  it('switching the tools off switches the cluster off', () => {
    assert.deepEqual(toggleTools({ tools: true, cluster: true }), { tools: false, cluster: false })
    assert.deepEqual(toggleTools({ tools: false, cluster: false }), { tools: true, cluster: false })
  })
})

describe('askHint', () => {
  it('follows the state', () => {
    assert.match(askHint({ tools: false, cluster: false }, caps()), /only read the files of my workspace/)
    assert.match(askHint({ tools: true, cluster: false }, caps()), /ask to add a note/)
  })
  it('names the namespaces in which an action is possible', () => {
    const hint = askHint({ tools: true, cluster: true }, caps())
    assert.match(hint, /guestbook, monitoring/)
  })
  it('says that no action is possible without write access, and without capabilities', () => {
    assert.match(askHint({ tools: true, cluster: true }, caps({ write: false, namespaces: [] })), /No action in the cluster is possible: no write access is configured/)
    assert.match(askHint({ tools: true, cluster: true }, null), /No action in the cluster is possible/)
  })
})

describe('recentRun', () => {
  it('shows an ad-hoc run by its question, on one line and cut', () => {
    const r = recentRun(run({ prompt: `line one\n\n  line   two ${'x'.repeat(400)}` }), [])
    assert.ok(r.title.startsWith('line one line two xxx'))
    assert.ok(r.title.length <= 141)
    assert.ok(!r.title.includes('\n'))
  })
  it('never shows the prompt of a responder run', () => {
    const r = recentRun(run({ role: 'responder', incidentId: 27, prompt: 'SECRET GitHub log' }), [])
    assert.equal(r.title, 'Diagnosis of incident #27')
    assert.equal(recentRun(run({ role: 'responder', prompt: 'x' }), []).title, 'Diagnosis of an incident')
  })
  it('names a run without a question', () => {
    assert.equal(recentRun(run({ prompt: '   ' }), []).title, 'A run without a question')
  })
  it('is waiting for you when a pending approval belongs to the running run', () => {
    assert.equal(recentRun(run({ status: 'running' }), [ask('r1')]).phase, 'waiting')
    assert.equal(recentRun(run({ status: 'running' }), [ask('other')]).phase, 'working')
    assert.equal(recentRun(run({ status: 'succeeded' }), [ask('r1')]).phase, 'done')
    assert.equal(recentRun(run({ status: 'queued' }), []).phase, 'queued')
    assert.equal(recentRun(run({ status: 'failed' }), []).phase, 'failed')
  })
  it('carries the flags and the time', () => {
    const r = recentRun(run({ mcp: true, cluster: true }), [])
    assert.deepEqual([r.tools, r.cluster, r.at], [true, true, '2026-10-05T09:00:00Z'])
    assert.deepEqual([recentRun(run(), []).tools, recentRun(run(), []).cluster], [false, false])
  })
})
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `npm test --prefix web`. Expected: FAIL (modules missing).

- [ ] **Step 3: Write the logic**

`web/src/needs.ts`:

```ts
import type { ToolCall } from './api.ts'

const asked = (c: ToolCall) => Date.parse(c.requestedAt) || 0

/**
 * The approvals of the Needs you page: what waits, the oldest asked first (the call that was asked first is decided first), and
 * what was answered, newest first. The API answers newest first, but the order is not left to it.
 */
export function splitApprovals(calls: readonly ToolCall[]): { pending: ToolCall[]; answered: ToolCall[] } {
  const pending = calls.filter((c) => c.decision === 'pending').sort((a, b) => asked(a) - asked(b) || a.id - b.id)
  const answered = calls.filter((c) => c.decision !== 'pending').sort((a, b) => b.id - a.id)
  return { pending, answered }
}

/** The decision of a call in words, and the token colour of the words. An approved action that failed says so. */
export function decisionView(call: ToolCall): { label: string; text: string } {
  switch (call.decision) {
    case 'approved':
      if (call.status === 'failed') return { label: 'approved, failed', text: 'text-destructive' }
      if (call.status === 'succeeded') return { label: 'approved', text: 'text-success' }
      return { label: 'approved, running', text: 'text-primary' }
    case 'denied':
      return { label: 'denied', text: 'text-muted-foreground' }
    case 'abandoned':
      return { label: 'abandoned', text: 'text-muted-foreground' }
    case 'pending':
      return { label: 'waiting for you', text: 'text-primary' }
    case '':
      return { label: 'read', text: 'text-muted-foreground' }
  }
}

/** When a call was answered: the decision, else the end, else the request. */
export function answeredAt(call: ToolCall): string {
  return call.decidedAt ?? call.finishedAt ?? call.requestedAt
}
```

`web/src/askpage.ts`:

```ts
import type { Capabilities, Run, ToolCall } from './api.ts'
import { runPhase } from './status.ts'
import type { RunPhase } from './status.ts'

export interface AskSwitches {
  tools: boolean
  cluster: boolean
}

/** The tools chip. The cluster tools are a part of the gatekeeper's tools: without the tools there is no cluster. */
export function toggleTools(s: AskSwitches): AskSwitches {
  const tools = !s.tools
  return { tools, cluster: tools ? s.cluster : false }
}

/** The cluster chip. The cluster tools need the gatekeeper: switching the cluster on switches the tools on. */
export function toggleCluster(s: AskSwitches): AskSwitches {
  const cluster = !s.cluster
  return { cluster, tools: cluster ? true : s.tools }
}

/** What the agent can do with the switches as they are, from real data. */
export function askHint(s: AskSwitches, caps: Capabilities | null): string {
  if (!s.tools) return 'I can only read the files of my workspace.'
  if (!s.cluster) return 'I can read incidents and ask to add a note to one. Every note waits for your decision under Needs you, and I wait with it.'
  if (caps?.cluster.write) {
    return `I can read the cluster. Actions in the cluster wait for your decision under Needs you; they are possible in: ${caps.cluster.namespaces.join(', ')}.`
  }
  return 'I can read the cluster. No action in the cluster is possible: no write access is configured.'
}

const TITLE_MAX = 140

/** One tidy line of text: whitespace collapsed, cut at `max` characters. */
function oneLine(text: string, max: number): string {
  const flat = text.replace(/\s+/g, ' ').trim()
  return flat.length > max ? `${flat.slice(0, max)}…` : flat
}

export interface RecentRun {
  id: string
  title: string
  phase: RunPhase
  at: string
  tools: boolean
  cluster: boolean
}

/**
 * A row of "Earlier conversations". An ad-hoc run is its question; a responder run is named by its incident and never shows its
 * prompt (it holds data from GitHub). A running run waits for you when a pending approval belongs to it: the run list does not say so.
 */
export function recentRun(run: Run, asks: readonly ToolCall[]): RecentRun {
  const waiting = run.status === 'running' && asks.some((c) => c.runId === run.id)
  const title =
    run.role === 'responder'
      ? run.incidentId !== undefined
        ? `Diagnosis of incident #${run.incidentId}`
        : 'Diagnosis of an incident'
      : oneLine(run.prompt, TITLE_MAX) || 'A run without a question'
  return { id: run.id, title, phase: runPhase(run.status, waiting), at: run.createdAt, tools: run.mcp === true, cluster: run.cluster === true }
}
```

This task does not touch `status.ts` or `status.test.ts`: `statusColor` stays until `RunsPage.tsx` is deleted in Task 3.

- [ ] **Step 4: Run the tests**

Run: `npm test --prefix web`, `npm run lint --prefix web`, `npm run build --prefix web`. Expected: all pass, lint prints nothing.

- [ ] **Step 5: Commit**

```sh
git add web/src/needs.ts web/src/needs.test.ts web/src/askpage.ts web/src/askpage.test.ts
git commit -m "feat(web): the logic of Needs you and Ask Remedy" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Needs you

**Files:**
- Create: `web/src/useApprovals.ts`, `web/src/NeedsYouPage.tsx`, `web/src/components/needs/AnsweredRow.tsx`
- Modify: `web/src/App.tsx`
- Delete: `web/src/ApprovalsPage.tsx`, `web/src/ApprovalCard.tsx`

**Interfaces:**
- Consumes: Task 1; `ApprovalAsk` (large variant), `RemedyMessage`, `EmptyState`, `callStatusColor`, `argumentList`, `outcomeText` from `approvals.ts`, `timeAgo` from `incidents.ts`, `api.listApprovals`, `ApiError`.
- Produces: `useApprovals()`, the default exports `NeedsYouPage`, `AnsweredRow({ call })`, the route `approvals` outside `LegacyPage`.

- [ ] **Step 1: The hook**

`web/src/useApprovals.ts`:

```ts
import { useCallback, useEffect, useRef, useState } from 'react'
import { api, ApiError } from './api.ts'
import type { ToolCall } from './api.ts'

const POLL_MS = 1000 // a call must show within two seconds (phase 2 spec, success criteria)

export interface Approvals {
  /** Every approval, newest first as the API answers; null until the first answer. */
  calls: ToolCall[] | null
  /** The message of the last failed load; what was loaded before is kept. */
  error: string
  /** Loads again now, after a decision. */
  reload: () => Promise<void>
}

/** All approvals, waiting and answered, polled every second. A response that is not the newest request's is dropped. */
export function useApprovals(): Approvals {
  const [calls, setCalls] = useState<ToolCall[] | null>(null)
  const [error, setError] = useState('')
  const latest = useRef(0)

  const load = useCallback(async () => {
    const mine = ++latest.current
    try {
      const list = await api.listApprovals('all')
      if (mine !== latest.current) return
      setCalls(list)
      setError('')
    } catch (e) {
      if (mine !== latest.current) return
      setError(e instanceof ApiError ? e.message : 'Could not load the approvals')
    }
  }, [])

  useEffect(() => {
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const tick = async () => {
      await load()
      if (!cancelled) timer = setTimeout(() => void tick(), POLL_MS)
    }
    void tick()
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [load])

  return { calls, error, reload: load }
}
```

- [ ] **Step 2: The answered row**

`web/src/components/needs/AnsweredRow.tsx`:

```tsx
import { Link } from 'react-router'
import type { ToolCall } from '@/api.ts'
import { argumentList, callStatusColor, outcomeText } from '@/approvals.ts'
import { timeAgo } from '@/incidents.ts'
import { answeredAt, decisionView } from '@/needs.ts'
import { cn } from '@/lib/utils'

/** A call that was answered: the tool, the decision in colour, the age, what was asked, the reason and what came of it. All text. */
export default function AnsweredRow({ call }: { call: ToolCall }) {
  const at = answeredAt(call)
  const view = decisionView(call)
  const outcome = outcomeText(call)
  return (
    <li className="flex gap-3 py-3.5 first:pt-0 last:pb-0">
      <span aria-hidden className={cn('mt-2 size-2 shrink-0 rounded-full', callStatusColor[call.status])} />
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <span className="flex flex-wrap items-baseline gap-x-2.5">
          <span className="font-mono text-[13px] break-all">{call.tool}</span>
          <span className={cn('text-[13px] font-semibold', view.text)}>{view.label}</span>
          <span className="text-xs text-subtle" title={new Date(at).toLocaleString()}>
            {timeAgo(at)}
          </span>
        </span>
        <span className="text-[13px] break-words whitespace-pre-wrap text-muted-foreground">
          {argumentList(call.arguments)
            .map(({ name, value }) => `${name}: ${value}`)
            .join(' · ')}
        </span>
        {call.reason && <span className="text-[13px] break-words">Reason: {call.reason}</span>}
        {outcome && <span className="text-[13px] break-words whitespace-pre-wrap text-muted-foreground">{outcome}</span>}
        <span className="flex gap-3 text-xs">
          <Link to={`/runs/${encodeURIComponent(call.runId)}`} className="rounded-sm text-muted-foreground outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50">
            The run
          </Link>
          {call.incidentId !== undefined && (
            <Link to={`/incidents/${call.incidentId}`} className="rounded-sm text-muted-foreground outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50">
              Incident #{call.incidentId}
            </Link>
          )}
        </span>
      </div>
    </li>
  )
}
```

(Check how the other components import: if the existing components import from `'@/api.ts'` with the extension, or `'@/api'`: copy that convention, and use the relative form with `.ts` where `@/` does not resolve for `.ts` modules; the compiler decides.)

- [ ] **Step 3: The page**

`web/src/NeedsYouPage.tsx`:

```tsx
import { Link } from 'react-router'
import { splitApprovals } from './needs.ts'
import { timeAgo } from './incidents.ts'
import { useApprovals } from './useApprovals.ts'
import AnsweredRow from '@/components/needs/AnsweredRow'
import ApprovalAsk from '@/components/conversation/ApprovalAsk'
import EmptyState from '@/components/EmptyState'
import RemedyMessage from '@/components/conversation/RemedyMessage'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'

const linkClass = 'rounded-sm text-primary outline-none hover:text-primary-hover focus-visible:ring-3 focus-visible:ring-ring/50'

/** What waits for a decision, as Remedy's questions, oldest first; under them what was answered. */
export default function NeedsYouPage() {
  const { calls, error, reload } = useApprovals()
  const { pending, answered } = splitApprovals(calls ?? [])

  return (
    <div className="mx-auto flex max-w-205 flex-col gap-6 px-4 py-5 md:px-10 md:py-12">
      <div className="flex flex-col gap-1">
        <h1 className="font-serif text-[clamp(26px,3vw,34px)] font-normal">Needs you</h1>
        <span className="text-muted-foreground">Questions I can't answer for myself. I wait for you before I change anything.</span>
      </div>

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {calls === null ? (
        !error && (
          <div className="flex flex-col gap-5">
            <Skeleton className="h-40" />
          </div>
        )
      ) : (
        <>
          {pending.length === 0 ? (
            <EmptyState title="Nothing waits for you.">A run with tools asks here before it changes anything.</EmptyState>
          ) : (
            pending.map((call) => (
              <RemedyMessage key={call.id} kind="ask" meta={`Remedy · asked ${timeAgo(call.requestedAt)}`}>
                <span className="flex flex-wrap gap-x-4 gap-y-1 text-[13px]">
                  {call.incidentId !== undefined && (
                    <Link to={`/incidents/${call.incidentId}`} className={linkClass}>
                      Incident #{call.incidentId}
                    </Link>
                  )}
                  <Link to={`/runs/${encodeURIComponent(call.runId)}`} className={linkClass}>
                    The run
                  </Link>
                </span>
                <ApprovalAsk call={call} variant="large" onChanged={() => void reload()} />
              </RemedyMessage>
            ))
          )}

          <section className="flex flex-col gap-3.5 border-t border-border pt-6">
            <h2 className="text-xs font-semibold text-muted-foreground">Already answered</h2>
            {answered.length === 0 ? (
              <p className="text-[13px] text-muted-foreground">No decisions yet.</p>
            ) : (
              <ol className="flex flex-col divide-y divide-border">
                {answered.map((call) => (
                  <AnsweredRow key={call.id} call={call} />
                ))}
              </ol>
            )}
          </section>
        </>
      )}
    </div>
  )
}
```

- [ ] **Step 4: The route and the deletions**

In `web/src/App.tsx` import `NeedsYouPage from './NeedsYouPage.tsx'`, remove the `ApprovalsPage` import, and move the `approvals` route out of the `LegacyPage` group, next to `incidents`:

```tsx
        <Route path="approvals" element={<NeedsYouPage />} />
```

`git rm web/src/ApprovalsPage.tsx web/src/ApprovalCard.tsx`. `grep -rn "ApprovalsPage\|ApprovalCard" web/src` must find nothing. `approvals.ts` keeps `decisionLabel`, `callStatusColor`, `outcomeText` (`ToolCallsCard` and the answered row use them): if `decisionLabel` is now used only by `ToolCallsCard`, that is fine.

- [ ] **Step 5: Verify and commit**

Run: `npm test --prefix web`, `npm run lint --prefix web`, `npm run build --prefix web`. Expected: all pass, lint prints nothing.

```sh
git add web/src
git commit -m "feat(web): Needs you" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Ask Remedy

**Files:**
- Create: `web/src/AskPage.tsx`, `web/src/components/ask/RecentRunRow.tsx`
- Modify: `web/src/App.tsx`, `web/src/status.ts`, `web/src/status.test.ts`
- Delete: `web/src/RunsPage.tsx`

**Interfaces:**
- Consumes: Task 1; `useShellState().asks`, `api.listRuns`, `api.createRun`, `api.getCapabilities`, `ApiError`, `EmptyState`, `phaseView`, `timeAgo`, `Textarea` is not used (a plain `<textarea>` carries the Literata styling).
- Produces: the default exports `AskPage`, `RecentRunRow({ run })`, the route `runs` (index) outside `LegacyPage`.

- [ ] **Step 1: The row**

`web/src/components/ask/RecentRunRow.tsx`:

```tsx
import { Link } from 'react-router'
import { timeAgo } from '@/incidents.ts'
import { phaseView } from '@/status.ts'
import type { RecentRun } from '@/askpage.ts'
import { cn } from '@/lib/utils'

/** A run in "Earlier conversations": the dot of its phase, what was asked, the phase in words and the age. */
export default function RecentRunRow({ run }: { run: RecentRun }) {
  const view = phaseView[run.phase]
  return (
    <Link
      to={`/runs/${encodeURIComponent(run.id)}`}
      className="flex items-center gap-3 rounded-2xl border border-border bg-card px-4 py-3 outline-none transition-colors hover:border-input focus-visible:ring-3 focus-visible:ring-ring/50"
    >
      <span aria-hidden className={cn('size-2.5 shrink-0 rounded-full', view.dot)} />
      <span className="min-w-0 flex-1 truncate">{run.title}</span>
      {run.tools && <span className="hidden text-xs text-subtle sm:inline">{run.cluster ? 'cluster' : 'tools'}</span>}
      <span className={cn('shrink-0 text-xs font-semibold', view.text)}>{view.label}</span>
      <span className="shrink-0 text-xs text-subtle" title={new Date(run.at).toLocaleString()}>
        {timeAgo(run.at)}
      </span>
    </Link>
  )
}
```

(The same note about import conventions applies; follow what the existing components use.)

- [ ] **Step 2: The page**

`web/src/AskPage.tsx`:

```tsx
import { ArrowUp } from 'lucide-react'
import { useEffect, useState } from 'react'
import type { FormEvent, KeyboardEvent } from 'react'
import { useNavigate } from 'react-router'
import { api, ApiError } from './api.ts'
import type { Capabilities, Run } from './api.ts'
import { askHint, recentRun, toggleCluster, toggleTools } from './askpage.ts'
import type { AskSwitches } from './askpage.ts'
import { useShellState } from './shellContext.ts'
import RecentRunRow from '@/components/ask/RecentRunRow'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

const POLL_MS = 3000

const chipClass = (on: boolean) =>
  cn(
    'h-9 rounded-full border px-4 text-[13px] outline-none transition-colors focus-visible:ring-3 focus-visible:ring-ring/50',
    on ? 'border-primary bg-soft-diagnosing font-semibold text-primary' : 'border-input text-muted-foreground hover:text-foreground',
  )

/** Where the maintainer starts a run by hand: the question, what the agent may use, and the runs of the past. */
export default function AskPage() {
  const navigate = useNavigate()
  const { asks } = useShellState()
  const [runs, setRuns] = useState<Run[] | null>(null)
  const [prompt, setPrompt] = useState('')
  const [switches, setSwitches] = useState<AskSwitches>({ tools: false, cluster: false })
  const [capabilities, setCapabilities] = useState<Capabilities | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    api.getCapabilities().then(setCapabilities).catch(() => setCapabilities(null))
  }, [])

  useEffect(() => {
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const tick = async () => {
      try {
        const list = await api.listRuns()
        if (!cancelled) setRuns(list)
      } catch {
        // the list keeps what it had; the page still works
      }
      if (!cancelled) timer = setTimeout(() => void tick(), POLL_MS)
    }
    void tick()
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [])

  const ready = prompt.trim() !== '' && !busy

  async function send() {
    if (!ready) return
    setBusy(true)
    setError('')
    try {
      const created = await api.createRun(prompt.trim(), switches.tools, switches.cluster)
      void navigate(`/runs/${created.id}`)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Could not start the run')
      setBusy(false)
    }
  }

  function submit(e: FormEvent) {
    e.preventDefault()
    void send()
  }

  function onKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      e.preventDefault()
      void send()
    }
  }

  return (
    <div className="mx-auto flex max-w-205 flex-col gap-6 px-4 py-5 md:px-10 md:py-12">
      <h1 className="font-serif text-[clamp(26px,3vw,34px)] font-normal">What should I look into?</h1>

      <form
        onSubmit={submit}
        className="flex flex-col gap-3 rounded-3xl border border-input bg-card p-4 focus-within:border-ring focus-within:ring-3 focus-within:ring-ring/50"
      >
        <textarea
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
          onKeyDown={onKeyDown}
          rows={4}
          aria-label="Your question"
          placeholder="Ask me about an incident, a workload, a log…"
          className="w-full resize-y border-0 bg-transparent font-serif text-[17px] leading-relaxed text-foreground outline-none placeholder:text-subtle"
        />
        <div className="flex flex-wrap items-center gap-2">
          <button type="button" aria-pressed={switches.tools} onClick={() => setSwitches(toggleTools)} className={chipClass(switches.tools)}>
            Use gatekeeper tools
          </button>
          {capabilities?.cluster.read && (
            <button type="button" aria-pressed={switches.cluster} onClick={() => setSwitches(toggleCluster)} className={chipClass(switches.cluster)}>
              Read the cluster
            </button>
          )}
          <button
            type="submit"
            disabled={!ready}
            aria-label="Send"
            className={cn(
              'ml-auto flex size-11 shrink-0 items-center justify-center rounded-full text-primary-foreground outline-none focus-visible:ring-3 focus-visible:ring-ring/50',
              ready ? 'bg-primary' : 'bg-input',
            )}
          >
            <ArrowUp className="size-5" aria-hidden />
          </button>
        </div>
      </form>
      <p className="-mt-3 pl-1 text-[13px] text-muted-foreground">{askHint(switches, capabilities)} Cmd or Ctrl and Enter send.</p>
      {error && (
        <span role="alert" className="-mt-3 pl-1 text-[13px] text-destructive">
          {error}
        </span>
      )}

      <section className="flex flex-col gap-3">
        <h2 className="text-xs font-semibold text-muted-foreground">Earlier conversations</h2>
        {runs === null ? (
          <Skeleton className="h-14" />
        ) : runs.length === 0 ? (
          <p className="text-[13px] text-muted-foreground">Nothing yet. Ask me something above.</p>
        ) : (
          runs.map((r) => <RecentRunRow key={r.id} run={recentRun(r, asks)} />)
        )}
      </section>
    </div>
  )
}
```

`setSwitches(toggleTools)` passes the functions as updaters: they take the previous switches and return the next ones. A plain Enter in the textarea stays a new line.

- [ ] **Step 3: The route, the deletion and the old status colour**

In `web/src/App.tsx` import `AskPage from './AskPage.tsx'`, remove the `RunsPage` import, and move the `runs` route out of the `LegacyPage` group, next to `runs/:id`:

```tsx
        <Route path="runs" element={<AskPage />} />
        <Route path="runs/:id" element={<RunRoute />} />
```

`git rm web/src/RunsPage.tsx`. `grep -rn "RunsPage" web/src` must find nothing. In `web/src/status.ts` delete `statusColor` (and its doc comment); in `web/src/status.test.ts` remove it from the import and delete its `describe('statusColor', ...)` block. `grep -rn "statusColor" web/src` must then find nothing.

- [ ] **Step 4: Verify and commit**

Run: `npm test --prefix web`, `npm run lint --prefix web`, `npm run build --prefix web`. Expected: all pass, lint prints nothing.

```sh
git add web/src
git commit -m "feat(web): Ask Remedy" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Verification (run by the controller, in a real browser)

No code changes unless a defect is found (a defect goes to a fix dispatch). Nothing here is committed.

- [ ] **Step 1: Full checks.** `make check` and `go test -tags webui ./web/` after `make web-build`. Expected: PASS.

- [ ] **Step 2: Run the app** with a throwaway database in the scratchpad (the scripts of part 3: `t7-start.sh` and `seed-ui3.sql` in the scratchpad; extend the seed with older and newer pending calls on different runs, answered calls of every outcome (approved and succeeded, approved and failed, denied, abandoned) with a reason and a long argument, and a run with a long multi-line prompt; for the cluster chip start the server with `REMEDY_K8S_READ_TOKEN_FILE` pointing at a file with any text and `REMEDY_K8S_API` at an unreachable address: the capabilities answer without contacting the cluster, check that this is so, else skip the chip). Real waiting approvals: claim a queued run with the runner token and call `/mcp` (`t3-live.sh`, `t3-mcp.sh` of part 3) so that `waiting` is true.

- [ ] **Step 3: Check Needs you** at 1280x900 and 390x844: the empty state; two waiting questions (the older first); Yes with a reason by keyboard and No (the toast, the call leaves the list within two seconds, the agent receives the answer); a call whose agent no longer waits (no buttons, the explanation); "Already answered" with every outcome in its colour, the reason, the outcome, both links; no horizontal overflow with a long argument; one `h1`; focus rings; the error alert when the server is stopped.

- [ ] **Step 4: Check Ask Remedy** at both widths: the chips (tools on and off, cluster on switches tools on, tools off switches cluster off, no cluster chip without read access), the hint per state with the namespaces, Cmd/Ctrl+Enter sends and goes to the run, plain Enter is a new line, whitespace is not sent, a refusal shows in place and keeps the text; "Earlier conversations": dots, "Waiting for you" for the run with a pending approval, "Diagnosis of incident #N" for a responder run, a long multi-line prompt on one truncated line; keyboard focus on rows.

- [ ] **Step 5: Clean up.** Stop the processes you started (by PID), delete the throwaway database, `.playwright-mcp/` and any `*.png` (also in the main checkout).

---

## Self-Review

**Spec coverage (spec 7):** 7.1 Needs you: oldest first, the ask avatar, "Remedy, asked N min ago, Incident #N, the run", the question in Literata, the card with the tool in mono and "exactly this runs if you approve", the arguments, the reason (500 characters) and Yes/No at full width (the large `ApprovalAsk`), the toast (inside `ApprovalAsk`), nothing optimistic (the poll), "Nothing waits for you.", "Already answered" with the tool, the decision in colour ("approved, failed"), the age, the outcome and both links (Tasks 1, 2). 7.2 Ask Remedy: "What should I look into?", the large card with a Literata textarea, the chips, the round send button, the cluster rules, Cmd/Ctrl+Enter, navigation to `/runs/:id`, the error in place, the hint from real data, "Earlier conversations" with "Waiting for you" matched by `runId` and "Diagnosis of incident #N", `statusColor` replaced by the phase view (Tasks 1, 3). **Deviation, recorded:** the spec's "the state comes from the 1 s poll" is the page's own hook `useApprovals` (the shell polls the pending approvals every 2 s and carries no history). **Placeholder scan:** none. The notes about import conventions are mechanical (the compiler decides).

**Type consistency:** `splitApprovals`, `decisionView`, `answeredAt` (Task 1) are used by `NeedsYouPage` and `AnsweredRow` (Task 2). `AskSwitches`, `toggleTools`, `toggleCluster`, `askHint`, `recentRun`, `RecentRun` (Task 1) are used by `AskPage` and `RecentRunRow` (Task 3). `ApprovalAsk` takes `call`, `variant`, `onChanged` as in part 3; `onChanged` reloads the approvals, as `ApprovalAsk` stays disabled after a decision until the call changes.

**Review Focus coverage:** 1 (Task 1 test; Task 2), 2 (part 3's `ApprovalAsk`; Task 4), 3 (Task 1 tests), 4 (Task 2 code; Task 4), 5 (Task 1 tests), 6 (Task 1 tests; Task 3), 7 (Task 3 code; Task 4), 8 and 9 (Tasks 2 to 4).
