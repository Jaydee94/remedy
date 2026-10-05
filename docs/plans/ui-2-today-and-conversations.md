# UI redesign, part 2: Today and Conversations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Timeline page by Today (Remedy's digest and the feed) and the Incidents table by Conversations (one card per incident, state filters with counts), and give the sidebar's open conversations a preview line. All with the existing API.

**Architecture:** The sentences Remedy says (the digest, the preview of a conversation, the question of a waiting approval) are built by pure functions in `ask.ts` and `conversation.ts` from the data, tested with `node --test`. `ShellState` also carries the waiting approvals (`asks`) so that the sidebar, Today and Conversations share one poll. A small `useIncidents` hook loads all incidents for the two pages. The pages are new components; the old `TimelinePage` and `IncidentsPage` are deleted and their routes leave the `LegacyPage` wrapper.

**Tech Stack:** React 19, React Router 8, TypeScript 7, Tailwind 4, `node --test`.

**Spec:** [`docs/specs/2026-10-05-ui-conversation-redesign-design.md`](../specs/2026-10-05-ui-conversation-redesign-design.md), section 5 (and 2, 9, 10). Plan part 1 is [`ui-1-foundation.md`](ui-1-foundation.md), implemented.

## Global Constraints

- Everything in the repo is English: code, comments, UI copy, docs, commit messages.
- `web/tsconfig.app.json` sets `erasableSyntaxOnly` and `verbatimModuleSyntax`: no enums or constructor parameter properties, and `import type` for types.
- Pure logic modules (`ask.ts`, `conversation.ts`, `timeline.ts`, `incidents.ts`, `shell.ts`) import only with relative paths and `.ts` extensions and contain only erasable TypeScript, so `node --test` can run them. Components may use the `@/` alias.
- Agent-written and GitHub-written text (incident titles, diagnosis summaries, activity summaries, approval arguments) is rendered as React text only, never as HTML. A sentence's structure never comes from such text: the sentences are templates filled with values.
- Palette and tokens of part 1 (`bg-card`, `text-subtle`, `bg-soft-*`, `animate-rm-in`, ...) are used; no new hard-coded palette colours (`bg-rose-500` and the like) in new code.
- The activity stream and the incident list keep their data sources and polling: `listActivity`, `streamActivity`, `mergeEntries`, `groupByDay`; `listIncidents('all')` every 5 s; the shell's 2 s poll.
- The routes do not change. `/incidents/:id` (the thread) stays a legacy page until part 3.
- Commits end with the line `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>` (exactly this model name).
- In a worktree-isolated session the Bash tool refuses commands that name git in a compound or variable form: write such logic into a script file with the Write tool (literal paths) and run it as its own command (see `CLAUDE.md`). A fresh worktree has no `web/node_modules`: run `npm ci` in `web/` first. Use `command ls`.
- Work on a branch `feat/ui-2-today-conversations`, never on `main`.

## Review Focus

1. A waiting approval for an incident that is already resolved or ignored: the preview says it is ignored or resolved (those rules come first), and the "asks you" chip still shows only while the incident is active. (Task 1 tests the preview order; Task 3.)
2. An incident of another source (no repository, no ref): the card shows the source's label instead of "repository, ref"; a GitHub incident shows "repository · ref". (Task 3, Task 5.)
3. The digest never states what the data does not show: a clause with a count of 0 is left out; the quiet sentence appears only when nothing opened and nothing was diagnosed in the last 24 hours; "1 incident" is singular. (Task 1.)
4. An approval whose arguments are missing or have another type must not print "undefined" or a hole: `askText` falls back to the general question. (Task 1.)
5. The filter pills' counts follow the source and repository selects; the selects appear only when there is more than one source or repository; an empty filter shows its own empty state. (Task 3.)
6. Only entries that arrive live fade in; the first page does not animate. The ended stream shows "Live updates stopped." with Reload. (Task 4.)
7. The list has at most 200 incidents (the API limit): the counts are of what was loaded. (Documented, nothing to build.)
8. 390 px: cards and the feed wrap, long titles break, nothing scrolls sideways; the sidebar preview line truncates. (Task 3, 4, 5.)

---

## File Structure

| File | Change | Responsibility |
|---|---|---|
| `web/src/ask.ts` + `ask.test.ts` | create | `askText(call)`: the question and the Yes label of a waiting approval |
| `web/src/conversation.ts` + `conversation.test.ts` | create | filters, counts, `incidentPreview`, `digestText`, `lastDiagnosed`, `askFor` |
| `web/src/incidents.ts` | modify | `incidentStateSoft`, `incidentStateText` |
| `web/src/timeline.ts` + `timeline.test.ts` | modify / create | `kindDotClass` on tokens and the new activity kinds |
| `web/src/shell.ts`, `web/src/shell.test.ts` | modify | `ShellState.asks` |
| `web/src/useShell.ts` | modify | passes the approvals through |
| `web/src/shellContext.ts` | modify | `ShellStateContext`, `useShellState` |
| `web/src/useIncidents.ts`, `web/src/useNow.ts` | create | all incidents, polled; the current time, refreshed |
| `web/src/components/RemedyMark.tsx` | modify | `fill` prop |
| `web/src/components/EmptyState.tsx` | create | the dashed empty state |
| `web/src/components/AppLayout.tsx`, `web/src/components/shell/Sidebar.tsx` | modify | provide the state; the preview line |
| `web/src/components/conversations/IncidentCard.tsx` | create | one card of the list |
| `web/src/ConversationsPage.tsx` | create | the list (replaces `IncidentsPage.tsx`, deleted) |
| `web/src/components/today/Digest.tsx` | create | Remedy's digest |
| `web/src/TodayPage.tsx` | create | digest and feed (replaces `TimelinePage.tsx`, deleted) |
| `web/src/App.tsx` | modify | the two routes leave `LegacyPage` |

---

### Task 1: The sentences Remedy says (pure logic)

**Files:**
- Create: `web/src/ask.ts`, `web/src/ask.test.ts`, `web/src/conversation.ts`, `web/src/conversation.test.ts`, `web/src/timeline.test.ts`
- Modify: `web/src/incidents.ts`, `web/src/timeline.ts`, `web/src/shell.ts`, `web/src/shell.test.ts`, `web/src/useShell.ts`

**Interfaces:**
- Consumes: `Incident`, `ToolCall`, `Diagnosis` (types of `web/src/api.ts`); `reasonText`, `conclusionText` of `incidents.ts`.
- Produces: `Ask`, `askText(call)`; `StateFilter`, `ACTIVE_STATES`, `matchesFilter`, `filterIncidents`, `filterCounts`, `askFor`, `incidentPreview`, `digestText`, `lastDiagnosed`; `incidentStateSoft`, `incidentStateText`; `kindDotClass` on tokens; `ShellState.asks: ToolCall[]`.

- [ ] **Step 1: Write the failing tests**

Create `web/src/ask.test.ts`:

```ts
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { askText } from './ask.ts'

const call = (tool: string, args: unknown) => ({ tool, arguments: args })

describe('askText', () => {
  it('asks to restart a workload', () => {
    assert.deepEqual(askText(call('cluster_rollout_restart', { kind: 'deployment', namespace: 'guestbook', name: 'guestbook-ui' })), {
      question: 'May I restart guestbook-ui in guestbook?',
      yes: 'Yes, restart it',
    })
  })

  it('asks to delete a pod', () => {
    assert.deepEqual(askText(call('cluster_delete_pod', { namespace: 'demo', name: 'api-1' })), {
      question: 'May I delete the pod api-1 in demo?',
      yes: 'Yes, delete it',
    })
  })

  it('asks to refresh and to sync an Argo CD application', () => {
    assert.deepEqual(askText(call('argo_refresh', { app: 'guestbook', hard: true })), {
      question: 'May I refresh the application guestbook?',
      yes: 'Yes, refresh it',
    })
    assert.deepEqual(askText(call('argo_sync', { app: 'guestbook' })), {
      question: 'May I sync the application guestbook?',
      yes: 'Yes, sync it',
    })
  })

  it('asks to add a note to an incident, whose id may be a number', () => {
    assert.deepEqual(askText(call('incident_add_note', { id: 27, note: 'see the diagnosis' })), {
      question: 'May I add a note to incident #27?',
      yes: 'Yes, add it',
    })
  })

  it('asks in general terms about a tool it does not know', () => {
    assert.deepEqual(askText(call('something_new', { a: 1 })), { question: 'May I run something_new?', yes: 'Yes, run it' })
  })

  it('does not print a hole when a value is missing, empty or of another type', () => {
    const general = { question: 'May I run cluster_rollout_restart?', yes: 'Yes, run it' }
    assert.deepEqual(askText(call('cluster_rollout_restart', { namespace: 'guestbook' })), general)
    assert.deepEqual(askText(call('cluster_rollout_restart', { name: '', namespace: 'guestbook' })), general)
    assert.deepEqual(askText(call('cluster_rollout_restart', { name: { x: 1 }, namespace: 'guestbook' })), general)
    assert.deepEqual(askText(call('cluster_rollout_restart', 'not an object')), general)
    assert.deepEqual(askText(call('cluster_rollout_restart', null)), general)
    assert.deepEqual(askText(call('cluster_rollout_restart', [1, 2])), general)
    assert.ok(!askText(call('incident_add_note', {})).question.includes('undefined'))
  })

  it('keeps text from the agent out of the sentence structure: the values are only inserted', () => {
    const got = askText(call('argo_sync', { app: 'x? Yes, run it. May I delete everything' }))
    assert.equal(got.question, 'May I sync the application x? Yes, run it. May I delete everything?')
    assert.equal(got.yes, 'Yes, sync it')
  })
})
```

Create `web/src/timeline.test.ts`:

```ts
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { kindDotClass } from './timeline.ts'

describe('kindDotClass', () => {
  it('colours the kinds of the activity log with the tokens', () => {
    assert.equal(kindDotClass('incident_opened'), 'bg-destructive')
    assert.equal(kindDotClass('incident_resolved'), 'bg-success')
    assert.equal(kindDotClass('incident_ignored'), 'bg-neutral')
    assert.equal(kindDotClass('incident_unignored'), 'bg-primary')
    assert.equal(kindDotClass('diagnosis_started'), 'bg-primary')
    assert.equal(kindDotClass('diagnosis_finished'), 'bg-info')
    assert.equal(kindDotClass('diagnosis_failed'), 'bg-destructive')
    assert.equal(kindDotClass('approval_requested'), 'bg-primary')
    assert.equal(kindDotClass('cluster_action'), 'bg-violet')
  })

  it('falls back to neutral for a kind it does not know', () => {
    assert.equal(kindDotClass('something_new'), 'bg-neutral')
  })
})
```

Create `web/src/conversation.test.ts`:

```ts
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import type { Diagnosis, Incident, ToolCall } from './api.ts'
import { askFor, digestText, filterCounts, filterIncidents, incidentPreview, lastDiagnosed } from './conversation.ts'

const inc = (over: Partial<Incident> = {}): Incident => ({
  id: 1,
  source: 'github',
  title: 'go',
  severity: 'none',
  autoDiagnose: true,
  repoId: 1,
  repo: 'octo/hello',
  ref: 'pr:7',
  checkName: 'go',
  state: 'open',
  conclusion: 'failure',
  headSha: 'abc1234',
  occurrences: 1,
  firstSeen: '2026-10-05T08:00:00Z',
  lastSeen: '2026-10-05T08:00:00Z',
  diagnoses: 0,
  ...over,
})

const call = (over: Partial<ToolCall> = {}): ToolCall => ({
  id: 1,
  runId: 'r1',
  incidentId: 1,
  tool: 'cluster_rollout_restart',
  kind: 'mutating',
  arguments: { kind: 'deployment', namespace: 'guestbook', name: 'guestbook-ui' },
  status: 'waiting',
  decision: 'pending',
  requestedAt: '2026-10-05T08:00:00Z',
  waiting: true,
  ...over,
})

const diagnosis: Diagnosis = {
  summary: 'The Renovate bump renamed a key.',
  cause: 'c',
  confidence: 'high',
  category: 'dependency_update',
  affected_files: [],
  proposed_fix: 'f',
  fix_looks_automatable: true,
}

describe('filterIncidents and filterCounts', () => {
  const list = [
    inc({ id: 1, state: 'open' }),
    inc({ id: 2, state: 'diagnosing' }),
    inc({ id: 3, state: 'diagnosed' }),
    inc({ id: 4, state: 'resolved' }),
    inc({ id: 5, state: 'ignored' }),
    inc({ id: 6, state: 'ignored' }),
  ]

  it('active is open, diagnosing and diagnosed, newest first', () => {
    assert.deepEqual(filterIncidents(list, 'active').map((i) => i.id), [3, 2, 1])
  })

  it('resolved and ignored are their own states', () => {
    assert.deepEqual(filterIncidents(list, 'resolved').map((i) => i.id), [4])
    assert.deepEqual(filterIncidents(list, 'ignored').map((i) => i.id), [6, 5])
  })

  it('counts every filter', () => {
    assert.deepEqual(filterCounts(list), { active: 3, resolved: 1, ignored: 2 })
    assert.deepEqual(filterCounts([]), { active: 0, resolved: 0, ignored: 0 })
  })

  it('does not change the list it is given', () => {
    const before = list.map((i) => i.id)
    filterIncidents(list, 'active')
    assert.deepEqual(list.map((i) => i.id), before)
  })
})

describe('askFor', () => {
  it('finds the call that was asked first for an incident', () => {
    const asks = [call({ id: 9, incidentId: 1 }), call({ id: 4, incidentId: 1 }), call({ id: 5, incidentId: 2 })]
    assert.equal(askFor(asks, 1)?.id, 4)
    assert.equal(askFor(asks, 2)?.id, 5)
    assert.equal(askFor(asks, 3), undefined)
  })
})

describe('incidentPreview', () => {
  it('says what the maintainer did to an ignored incident', () => {
    assert.equal(incidentPreview(inc({ state: 'ignored' })), 'You ignored this incident.')
  })

  it('says why a resolved incident is resolved', () => {
    assert.equal(incidentPreview(inc({ state: 'resolved', resolvedReason: 'green' })), 'Resolved: the check turned green.')
    assert.equal(incidentPreview(inc({ state: 'resolved' })), 'Resolved.')
  })

  it('shows the question of a waiting approval', () => {
    assert.equal(incidentPreview(inc({ state: 'diagnosed', diagnosis }), call()), 'May I restart guestbook-ui in guestbook?')
  })

  it('says ignored or resolved even when an approval is still waiting', () => {
    assert.equal(incidentPreview(inc({ state: 'ignored' }), call()), 'You ignored this incident.')
    assert.equal(incidentPreview(inc({ state: 'resolved', resolvedReason: 'cleared' }), call()), 'Resolved: the signal is no longer reported.')
  })

  it('says that it is looking into an incident it diagnoses', () => {
    assert.equal(incidentPreview(inc({ state: 'diagnosing' })), "I'm looking into it…")
  })

  it('shows the summary of the diagnosis', () => {
    assert.equal(incidentPreview(inc({ state: 'diagnosed', diagnosis })), 'The Renovate bump renamed a key.')
  })

  it('says it has not looked yet, or that it does not diagnose this kind of result on its own', () => {
    assert.equal(incidentPreview(inc({ state: 'open' })), "I haven't looked yet.")
    assert.equal(
      incidentPreview(inc({ state: 'open', autoDiagnose: false })),
      "I don't diagnose this kind of result automatically. Ask me if you want.",
    )
  })
})

describe('digestText', () => {
  const now = new Date('2026-10-05T12:00:00Z')
  const hoursAgo = (h: number) => new Date(now.getTime() - h * 3600_000).toISOString()

  it('counts what opened and what was diagnosed in the last 24 hours', () => {
    const list = [
      inc({ id: 1, firstSeen: hoursAgo(3) }),
      inc({ id: 2, firstSeen: hoursAgo(5), state: 'diagnosed', diagnosis, lastDiagnosisAt: hoursAgo(4) }),
      inc({ id: 3, firstSeen: hoursAgo(30) }),
    ]
    assert.equal(digestText(now, list, 0), 'In the last 24 hours I opened 2 incidents and diagnosed 1.')
  })

  it('uses the singular for one incident', () => {
    assert.equal(digestText(now, [inc({ firstSeen: hoursAgo(1) })], 0), 'In the last 24 hours I opened 1 incident.')
  })

  it('leaves out a clause with a count of 0 and names the incidents when only diagnoses are counted', () => {
    const list = [inc({ firstSeen: hoursAgo(40), state: 'diagnosed', diagnosis, lastDiagnosisAt: hoursAgo(2) })]
    assert.equal(digestText(now, list, 0), 'In the last 24 hours I diagnosed 1 incident.')
  })

  it('does not count a diagnosis that is older than 24 hours or that has no stored answer', () => {
    const list = [
      inc({ id: 1, firstSeen: hoursAgo(40), state: 'diagnosed', diagnosis, lastDiagnosisAt: hoursAgo(30) }),
      inc({ id: 2, firstSeen: hoursAgo(40), state: 'diagnosing', lastDiagnosisAt: hoursAgo(1) }),
    ]
    assert.equal(digestText(now, list, 0), "All quiet. Nothing opened in the last 24 hours, and I'm still watching.")
  })

  it('is quiet when nothing happened', () => {
    assert.equal(digestText(now, [], 0), "All quiet. Nothing opened in the last 24 hours, and I'm still watching.")
  })

  it('says how many questions wait', () => {
    assert.equal(digestText(now, [inc({ firstSeen: hoursAgo(1) })], 1), 'In the last 24 hours I opened 1 incident. One question waits for you.')
    assert.equal(digestText(now, [], 3), "All quiet. Nothing opened in the last 24 hours, and I'm still watching. 3 questions wait for you.")
  })
})

describe('lastDiagnosed', () => {
  it('is the incident with the newest stored diagnosis', () => {
    const list = [
      inc({ id: 1, diagnosis, lastDiagnosisAt: '2026-10-05T08:00:00Z' }),
      inc({ id: 2, diagnosis, lastDiagnosisAt: '2026-10-05T09:00:00Z' }),
      inc({ id: 3, lastDiagnosisAt: '2026-10-05T10:00:00Z' }),
    ]
    assert.equal(lastDiagnosed(list)?.id, 2)
  })

  it('is undefined when nothing was diagnosed', () => {
    assert.equal(lastDiagnosed([inc()]), undefined)
    assert.equal(lastDiagnosed([]), undefined)
  })
})
```

In `web/src/shell.test.ts`: change the import line `import type { Incident } from './api.ts'` to `import type { Incident, ToolCall } from './api.ts'`; add after the existing `const incident = ...` helper line `const call = (id: number): ToolCall => ({ id }) as ToolCall`; and replace the whole `describe('nextShell', ...)` block with:

```ts
describe('nextShell', () => {
  it('starts not loaded, online, with nothing', () => {
    assert.deepEqual(emptyShell, { pending: 0, asks: [], incidents: [], online: true, loaded: false })
  })

  it('takes both answers and is loaded', () => {
    const got = nextShell(emptyShell, ok([call(1), call(2)]), ok([incident(7)]), isHttpError)
    assert.deepEqual(got, { pending: 2, asks: [call(1), call(2)], incidents: [incident(7)], online: true, loaded: true })
  })

  it('stays online when a route answers with an HTTP error (the route may not exist)', () => {
    const got = nextShell(emptyShell, failed(new HttpError('404')), ok([incident(7)]), isHttpError)
    assert.equal(got.online, true)
    assert.equal(got.pending, 0)
    assert.deepEqual(got.asks, [])
    assert.equal(got.loaded, true)
  })

  it('goes offline when the server cannot be reached, and keeps the last data', () => {
    const before = nextShell(emptyShell, ok([call(1), call(2), call(3)]), ok([incident(1), incident(2)]), isHttpError)
    const got = nextShell(before, failed(new TypeError('fetch failed')), failed(new TypeError('fetch failed')), isHttpError)
    assert.equal(got.online, false)
    assert.equal(got.pending, 3)
    assert.deepEqual(got.asks, [call(1), call(2), call(3)])
    assert.deepEqual(got.incidents, [incident(1), incident(2)])
    assert.equal(got.loaded, true)
  })

  it('is not loaded while the incidents have never answered', () => {
    const got = nextShell(emptyShell, ok([]), failed(new TypeError('fetch failed')), isHttpError)
    assert.equal(got.loaded, false)
    assert.equal(got.online, false)
  })

  it('comes back online when the server answers again', () => {
    const down = nextShell(emptyShell, failed(new TypeError('x')), failed(new TypeError('x')), isHttpError)
    const up = nextShell(down, ok([call(1)]), ok([]), isHttpError)
    assert.equal(up.online, true)
    assert.equal(up.pending, 1)
  })

  it('keeps the previous asks when a route answers with a transient HTTP error', () => {
    const before = nextShell(emptyShell, ok([call(1), call(2)]), ok([]), isHttpError)
    const got = nextShell(before, failed(new HttpError('500')), ok([]), isHttpError)
    assert.equal(got.pending, 2)
    assert.deepEqual(got.asks, [call(1), call(2)])
    assert.equal(got.online, true)
  })

  it('keeps the incidents and the loaded flag when the incidents route answers with an HTTP error', () => {
    const before = nextShell(emptyShell, ok([call(1)]), ok([incident(7)]), isHttpError)
    const got = nextShell(before, ok([call(1)]), failed(new HttpError('500')), isHttpError)
    assert.deepEqual(got.incidents, [incident(7)])
    assert.equal(got.loaded, true)
    assert.equal(got.online, true)
  })
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web && npm test`
Expected: FAIL: `Cannot find module './ask.ts'`, `'./conversation.ts'`; `kindDotClass` colours are still the old palette; `emptyShell` has no `asks`.

- [ ] **Step 3: Implement**

Create `web/src/ask.ts`:

```ts
import type { ToolCall } from './api.ts'

/** What Remedy says when a call waits for a decision: the question, and the label of the Yes button. */
export interface Ask {
  question: string
  yes: string
}

/** A text or number argument of a call, or undefined when it is missing, empty or of another type. */
function arg(args: unknown, name: string): string | undefined {
  if (typeof args !== 'object' || args === null || Array.isArray(args)) return undefined
  const value = (args as Record<string, unknown>)[name]
  if (typeof value === 'string' && value !== '') return value
  if (typeof value === 'number') return String(value)
  return undefined
}

/**
 * The question and the Yes label of a call, from a fixed table. The sentence is ours; the values in it come from the agent's
 * arguments and are only inserted. A call with a value missing falls back to the general question instead of printing a hole.
 * The arguments are shown next to the question in full: what is shown is what runs.
 */
export function askText(call: Pick<ToolCall, 'tool' | 'arguments'>): Ask {
  const a = (name: string) => arg(call.arguments, name)
  switch (call.tool) {
    case 'cluster_rollout_restart': {
      const name = a('name')
      const namespace = a('namespace')
      if (name && namespace) return { question: `May I restart ${name} in ${namespace}?`, yes: 'Yes, restart it' }
      break
    }
    case 'cluster_delete_pod': {
      const name = a('name')
      const namespace = a('namespace')
      if (name && namespace) return { question: `May I delete the pod ${name} in ${namespace}?`, yes: 'Yes, delete it' }
      break
    }
    case 'argo_refresh': {
      const app = a('app')
      if (app) return { question: `May I refresh the application ${app}?`, yes: 'Yes, refresh it' }
      break
    }
    case 'argo_sync': {
      const app = a('app')
      if (app) return { question: `May I sync the application ${app}?`, yes: 'Yes, sync it' }
      break
    }
    case 'incident_add_note': {
      const id = a('id')
      if (id) return { question: `May I add a note to incident #${id}?`, yes: 'Yes, add it' }
      break
    }
  }
  return { question: `May I run ${call.tool}?`, yes: 'Yes, run it' }
}
```

Create `web/src/conversation.ts`:

```ts
import type { Incident, ToolCall } from './api.ts'
import { askText } from './ask.ts'
import { reasonText } from './incidents.ts'

/** The pills of the conversations list. */
export type StateFilter = 'active' | 'resolved' | 'ignored'

export const ACTIVE_STATES: readonly Incident['state'][] = ['open', 'diagnosing', 'diagnosed']

export function matchesFilter(incident: Incident, filter: StateFilter): boolean {
  return filter === 'active' ? ACTIVE_STATES.includes(incident.state) : incident.state === filter
}

/** The incidents of a filter, newest first. The list it is given is not changed. */
export function filterIncidents(list: readonly Incident[], filter: StateFilter): Incident[] {
  return list.filter((i) => matchesFilter(i, filter)).sort((a, b) => b.id - a.id)
}

export function filterCounts(list: readonly Incident[]): Record<StateFilter, number> {
  return {
    active: list.filter((i) => matchesFilter(i, 'active')).length,
    resolved: list.filter((i) => matchesFilter(i, 'resolved')).length,
    ignored: list.filter((i) => matchesFilter(i, 'ignored')).length,
  }
}

/** The waiting call that was asked first for an incident. */
export function askFor(asks: readonly ToolCall[], incidentId: number): ToolCall | undefined {
  return asks.filter((c) => c.incidentId === incidentId).sort((a, b) => a.id - b.id)[0]
}

/**
 * The line under an incident in the list and in the sidebar. Ignored and resolved come first: the maintainer's decision and the
 * end of the incident outrank a call that still waits. The sentences are ours; the diagnosis summary is the agent's text and is
 * only shown.
 */
export function incidentPreview(incident: Incident, ask?: ToolCall): string {
  if (incident.state === 'ignored') return 'You ignored this incident.'
  if (incident.state === 'resolved') {
    const why = reasonText(incident.resolvedReason)
    return why ? `Resolved: ${why}.` : 'Resolved.'
  }
  if (ask) return askText(ask).question
  if (incident.state === 'diagnosing') return "I'm looking into it…"
  if (incident.diagnosis) return incident.diagnosis.summary
  if (!incident.autoDiagnose) return "I don't diagnose this kind of result automatically. Ask me if you want."
  return "I haven't looked yet."
}

const DAY_MS = 24 * 60 * 60 * 1000

const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? '' : 's'}`

/**
 * The digest of Today, from counts over the last 24 hours: incidents opened (by first seen), incidents diagnosed (a stored
 * diagnosis, by the time it was started) and the questions that wait. A clause with a count of 0 is left out, and the quiet
 * sentence appears only when nothing was counted.
 */
export function digestText(now: Date, incidents: readonly Incident[], pending: number): string {
  const since = now.getTime() - DAY_MS
  const opened = incidents.filter((i) => Date.parse(i.firstSeen) >= since).length
  const diagnosed = incidents.filter(
    (i) => i.diagnosis !== undefined && i.lastDiagnosisAt !== undefined && Date.parse(i.lastDiagnosisAt) >= since,
  ).length

  let text: string
  if (opened === 0 && diagnosed === 0) {
    text = "All quiet. Nothing opened in the last 24 hours, and I'm still watching."
  } else {
    const parts: string[] = []
    if (opened > 0) parts.push(`opened ${plural(opened, 'incident')}`)
    if (diagnosed > 0) parts.push(`diagnosed ${opened > 0 ? diagnosed : plural(diagnosed, 'incident')}`)
    text = `In the last 24 hours I ${parts.join(' and ')}.`
  }
  if (pending === 1) text += ' One question waits for you.'
  else if (pending > 1) text += ` ${pending} questions wait for you.`
  return text
}

/** The incident whose diagnosis was started last, among those that have one. */
export function lastDiagnosed(incidents: readonly Incident[]): Incident | undefined {
  let best: Incident | undefined
  for (const i of incidents) {
    if (i.diagnosis === undefined || i.lastDiagnosisAt === undefined) continue
    if (best === undefined || Date.parse(i.lastDiagnosisAt) > Date.parse(best.lastDiagnosisAt!)) best = i
  }
  return best
}
```

`web/src/incidents.ts`: add after `incidentStateColor`:

```ts
/** The soft background and the text colour of an incident's state: the marker of a card and the state chip. */
export const incidentStateSoft: Record<IncidentState, string> = {
  open: 'bg-soft-open',
  diagnosing: 'bg-soft-diagnosing',
  diagnosed: 'bg-soft-diagnosed',
  resolved: 'bg-soft-resolved',
  ignored: 'bg-soft-ignored',
}

export const incidentStateText: Record<IncidentState, string> = {
  open: 'text-destructive',
  diagnosing: 'text-primary',
  diagnosed: 'text-info',
  resolved: 'text-success',
  ignored: 'text-neutral',
}
```

`web/src/timeline.ts`: replace the `kindDots` table with:

```ts
const kindDots: Record<string, string> = {
  incident_opened: 'bg-destructive',
  incident_recurred: 'bg-destructive',
  incident_resolved: 'bg-success',
  incident_ignored: 'bg-neutral',
  incident_unignored: 'bg-primary',
  poll_failed: 'bg-primary',
  poll_recovered: 'bg-success',
  diagnosis_started: 'bg-primary',
  diagnosis_finished: 'bg-info',
  diagnosis_failed: 'bg-destructive',
  approval_requested: 'bg-primary',
  approval_decided: 'bg-neutral',
  approval_abandoned: 'bg-neutral',
  note_added: 'bg-primary',
  run_cancelled: 'bg-neutral',
  cluster_action: 'bg-violet',
}
```

and in `kindDotClass` change the fallback `'bg-slate-500'` to `'bg-neutral'`.

`web/src/shell.ts`: change the import to `import type { Incident, ToolCall } from './api.ts'`; give `ShellState` the field `asks: ToolCall[]` (comment: the calls that wait for a decision, oldest asked first is not guaranteed: the API answers newest first) next to `pending`; `emptyShell` becomes `{ pending: 0, asks: [], incidents: [], online: true, loaded: false }`; `nextShell`'s `approvals` parameter type becomes `PromiseSettledResult<readonly ToolCall[]>` and its body becomes:

```ts
  const unreachable = [approvals, incidents].some((r) => r.status === 'rejected' && !isHttpError(r.reason))
  const asks = approvals.status === 'fulfilled' ? [...approvals.value] : prev.asks
  return {
    pending: asks.length,
    asks,
    incidents: incidents.status === 'fulfilled' ? incidents.value : prev.incidents,
    online: !unreachable,
    loaded: prev.loaded || incidents.status === 'fulfilled',
  }
```

(`web/src/useShell.ts` needs no change: `api.listApprovals('pending')` returns `ToolCall[]`.)

- [ ] **Step 4: Run the tests, lint and build**

Run: `cd web && npm test && npm run lint && npm run build`
Expected: all pass; `npm test` reports the new tests (about 50 in total), pristine output, lint prints nothing.

- [ ] **Step 5: Commit**

```sh
git add web/src
git commit -m "feat(web): the sentences Remedy says about incidents, approvals and the day" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Shared data, atoms and the sidebar preview

**Files:**
- Create: `web/src/useIncidents.ts`, `web/src/useNow.ts`, `web/src/components/EmptyState.tsx`
- Modify: `web/src/shellContext.ts`, `web/src/components/AppLayout.tsx`, `web/src/components/RemedyMark.tsx`, `web/src/components/shell/Sidebar.tsx`

**Interfaces:**
- Consumes: Task 1's `ShellState.asks`, `incidentPreview`, `askFor`; part 1's `useShell`, `ShellContext`, `RemedyMark`, `Sidebar`.
- Produces: `useShellState(): ShellState` (context); `useIncidents(): { incidents: Incident[] | null; error: string }`; `useNow(intervalMs): Date`; `EmptyState({ title, children })`; `RemedyMark` with a `fill` prop; the sidebar's preview line.

- [ ] **Step 1: The state context**

In `web/src/shellContext.ts` add to the imports `import { emptyShell } from './shell.ts'` and `import type { ShellState } from './shell.ts'` (merge with the existing `Header` import), and append:

```ts
/** What the shell polls: the waiting approvals, the active incidents, whether the server answers. Pages read it instead of polling again. */
export const ShellStateContext = createContext<ShellState>(emptyShell)

export function useShellState(): ShellState {
  return useContext(ShellStateContext)
}
```

In `web/src/components/AppLayout.tsx` import `ShellStateContext` together with `ShellContext` and wrap the existing provider content: `<ShellContext.Provider value={context}><ShellStateContext.Provider value={shell}>...</ShellStateContext.Provider></ShellContext.Provider>` (the `ToastProvider` stays inside).

- [ ] **Step 2: The hooks**

`web/src/useIncidents.ts`:

```ts
import { useEffect, useState } from 'react'
import { api, ApiError } from './api.ts'
import type { Incident } from './api.ts'

const POLL_MS = 5000

export interface IncidentsState {
  /** Every incident of every state (the API answers at most 200), newest first. Null until the first answer. */
  incidents: Incident[] | null
  /** The message of the last failed load; the incidents of an earlier load are kept. */
  error: string
}

/** All incidents, refreshed every five seconds. */
export function useIncidents(): IncidentsState {
  const [state, setState] = useState<IncidentsState>({ incidents: null, error: '' })

  useEffect(() => {
    let active = true
    let busy = false
    const load = async () => {
      if (busy) return
      busy = true
      try {
        const list = await api.listIncidents('all')
        if (active) setState({ incidents: list, error: '' })
      } catch (e) {
        const error = e instanceof ApiError ? e.message : 'Could not load the incidents'
        if (active) setState((s) => ({ incidents: s.incidents, error }))
      } finally {
        busy = false
      }
    }
    void load()
    const timer = setInterval(() => void load(), POLL_MS)
    return () => {
      active = false
      clearInterval(timer)
    }
  }, [])

  return state
}
```

`web/src/useNow.ts`:

```ts
import { useEffect, useState } from 'react'

/** The current time, refreshed every `intervalMs`: a render never reads the clock itself. */
export function useNow(intervalMs: number): Date {
  const [now, setNow] = useState(() => new Date())
  useEffect(() => {
    const timer = setInterval(() => setNow(new Date()), intervalMs)
    return () => clearInterval(timer)
  }, [intervalMs])
  return now
}
```

- [ ] **Step 2b: The empty state and the mark's fill**

In `web/src/components/RemedyMark.tsx` add a prop `fill?: string` (comment: the colour of the drop) with the default `'var(--primary)'` and use it as the first path's `fill` instead of the literal `var(--primary)`.

`web/src/components/EmptyState.tsx`:

```tsx
import type { ReactNode } from 'react'
import RemedyMark from '@/components/RemedyMark'

/** A dashed card for a list with nothing in it. */
export default function EmptyState({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="flex flex-col items-center gap-3 rounded-3xl border border-dashed border-input px-5 py-16 text-center">
      <RemedyMark size={36} fill="var(--input)" />
      <span className="font-serif text-[19px]">{title}</span>
      {children && <span className="max-w-90 text-muted-foreground">{children}</span>}
    </div>
  )
}
```

- [ ] **Step 3: The sidebar preview line**

In `web/src/components/shell/Sidebar.tsx` import `askFor`, `incidentPreview` from `@/conversation.ts`, and replace the `shell.incidents.map(...)` block of the open conversations with:

```tsx
        {shell.incidents.map((incident) => {
          const here = pathname === `/incidents/${incident.id}`
          return (
            <Link
              key={incident.id}
              to={`/incidents/${incident.id}`}
              aria-current={here ? 'page' : undefined}
              className={cn('flex gap-2.5 rounded-2xl px-3 py-2.5 transition-colors hover:bg-card', here && 'bg-card', focus)}
            >
              <span aria-hidden className={cn('mt-1.5 size-2 shrink-0 rounded-full', incidentStateColor[incident.state])} />
              <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                <span className="flex gap-2">
                  <span className="flex-1 truncate font-semibold">
                    <span className="sr-only">{incident.state}: </span>
                    {incident.title}
                  </span>
                  <span className="shrink-0 text-xs text-subtle">{timeAgo(incident.lastSeen)}</span>
                </span>
                <span className="truncate text-[13px] text-muted-foreground">
                  {incidentPreview(incident, askFor(shell.asks, incident.id))}
                </span>
              </span>
            </Link>
          )
        })}
```

(Keep whatever `aria-current` expression the file has for the item if it differs from the one shown here: only the inner content changes.)

- [ ] **Step 4: Verify and commit**

Run: `cd web && npm test && npm run lint && npm run build`
Expected: all pass.

```sh
git add web/src
git commit -m "feat(web): share the waiting approvals with the pages and show a preview in the sidebar" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Conversations

**Files:**
- Create: `web/src/components/conversations/IncidentCard.tsx`, `web/src/ConversationsPage.tsx`
- Modify: `web/src/App.tsx`
- Delete: `web/src/IncidentsPage.tsx`

**Interfaces:**
- Consumes: `useIncidents`, `useShellState`, `filterIncidents`, `filterCounts`, `askFor`, `incidentPreview`, `StateFilter`, `incidentStateColor`, `incidentStateSoft`, `incidentStateText`, `conclusionText`, `refLabel`, `sourceLabel`, `timeAgo`, `EmptyState`, `Skeleton`.
- Produces: `IncidentCard({ incident, ask? })`, the default export `ConversationsPage`, the route `incidents` outside `LegacyPage`.

- [ ] **Step 1: The card**

`web/src/components/conversations/IncidentCard.tsx`:

```tsx
import { Link } from 'react-router'
import type { Incident, ToolCall } from '@/api.ts'
import { incidentPreview } from '@/conversation.ts'
import { conclusionText, incidentStateColor, incidentStateSoft, incidentStateText, refLabel, sourceLabel, timeAgo } from '@/incidents.ts'
import { cn } from '@/lib/utils'

interface Props {
  incident: Incident
  /** The call that waits for a decision for this incident, if there is one. */
  ask?: ToolCall
}

/** One incident of the conversations list. Everything from GitHub and from the agent is shown as text. */
export default function IncidentCard({ incident, ask }: Props) {
  const github = incident.source === 'github'
  const active = incident.state === 'open' || incident.state === 'diagnosing' || incident.state === 'diagnosed'
  return (
    <Link
      to={`/incidents/${incident.id}`}
      className="flex gap-3.5 rounded-[20px] border border-border bg-card p-4.5 text-foreground outline-none transition-colors hover:border-input hover:bg-secondary/40 focus-visible:ring-3 focus-visible:ring-ring/50"
    >
      <span
        aria-hidden
        className={cn('flex size-10 shrink-0 items-center justify-center rounded-full', incidentStateSoft[incident.state])}
      >
        <span className={cn('size-2.5 rounded-full', incidentStateColor[incident.state])} />
      </span>
      <span className="flex min-w-0 flex-1 flex-col gap-1.5">
        <span className="flex flex-wrap items-baseline gap-x-2.5 gap-y-1">
          <span className="min-w-0 text-[15px] font-semibold break-words">{incident.title}</span>
          <span className="text-[13px] text-muted-foreground">
            {github ? `${incident.repo} · ${refLabel(incident.ref)}` : sourceLabel(incident.source)}
          </span>
          <span className="ml-auto text-xs text-subtle">{timeAgo(incident.lastSeen)}</span>
        </span>
        <span className="font-serif text-[15px] leading-normal text-pretty break-words text-foreground/85">
          {incidentPreview(incident, ask)}
        </span>
        <span className="flex flex-wrap gap-1.5 text-xs">
          <span className={cn('rounded-full px-2.5 py-0.5', incidentStateSoft[incident.state], incidentStateText[incident.state])}>
            {incident.state}
          </span>
          <span className="rounded-full bg-secondary px-2.5 py-0.5 text-muted-foreground">
            {conclusionText(incident.conclusion)} · {incident.occurrences}×
          </span>
          {ask && active && (
            <span className="rounded-full bg-primary px-2.5 py-0.5 font-semibold text-primary-foreground">asks you</span>
          )}
        </span>
      </span>
    </Link>
  )
}
```

- [ ] **Step 2: The page**

`web/src/ConversationsPage.tsx`:

```tsx
import { useState } from 'react'
import type { IncidentSource } from './api.ts'
import { askFor, filterCounts, filterIncidents } from './conversation.ts'
import type { StateFilter } from './conversation.ts'
import { sourceLabel } from './incidents.ts'
import { useShellState } from './shellContext.ts'
import { useIncidents } from './useIncidents.ts'
import EmptyState from '@/components/EmptyState'
import IncidentCard from '@/components/conversations/IncidentCard'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

const filterLabels: Record<StateFilter, string> = { active: 'Active', resolved: 'Resolved', ignored: 'Ignored' }

const empty: Record<StateFilter, { title: string; text: string }> = {
  active: { title: 'All quiet.', text: 'No active incidents. I check the enabled repositories every minute.' },
  resolved: { title: 'Nothing resolved yet.', text: 'Incidents land here when the check turns green or the pull request closes.' },
  ignored: { title: 'Nothing ignored.', text: 'Ignored incidents land here. You can stop ignoring them at any time.' },
}

const selectClass =
  'h-8.5 rounded-full border border-input bg-transparent px-3.5 text-[13px] text-muted-foreground outline-none transition-colors hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50'

/** The conversations: one card per incident, with the state filters of the prototype. */
export default function ConversationsPage() {
  const { incidents, error } = useIncidents()
  const { asks } = useShellState()
  const [filter, setFilter] = useState<StateFilter>('active')
  const [source, setSource] = useState('')
  const [repoId, setRepoId] = useState('')

  const sources = [...new Set((incidents ?? []).map((i) => i.source))]
  const repos = new Map<number, string>()
  for (const i of incidents ?? []) if (i.source === 'github') repos.set(i.repoId, i.repo)

  const scoped = (incidents ?? []).filter(
    (i) => (source === '' || i.source === source) && (repoId === '' || String(i.repoId) === repoId),
  )
  const counts = filterCounts(scoped)
  const shown = filterIncidents(scoped, filter)

  return (
    <div className="mx-auto flex max-w-205 flex-col gap-5 px-4 py-5 md:px-10 md:py-12">
      <div className="flex flex-wrap items-end gap-3">
        <div className="flex flex-col gap-1">
          <h1 className="font-serif text-[clamp(26px,3vw,34px)] font-normal">Conversations</h1>
          <span className="text-muted-foreground">One per incident. I open a new one when a check fails.</span>
        </div>
        <span className="flex flex-wrap gap-1.5 sm:ml-auto">
          {(Object.keys(filterLabels) as StateFilter[]).map((key) => {
            const on = filter === key
            return (
              <button
                key={key}
                type="button"
                aria-pressed={on}
                onClick={() => setFilter(key)}
                className={cn(
                  'h-8.5 rounded-full border px-3.5 text-[13px] outline-none transition-colors focus-visible:ring-3 focus-visible:ring-ring/50',
                  on
                    ? 'border-foreground bg-foreground font-semibold text-background'
                    : 'border-input text-muted-foreground hover:text-foreground',
                )}
              >
                {filterLabels[key]} · {counts[key]}
              </button>
            )
          })}
        </span>
      </div>

      {(sources.length > 1 || repos.size > 1) && (
        <div className="flex flex-wrap gap-2">
          {sources.length > 1 && (
            <select aria-label="Source" className={selectClass} value={source} onChange={(e) => setSource(e.target.value)}>
              <option value="">All sources</option>
              {sources.map((s: IncidentSource) => (
                <option key={s} value={s}>
                  {sourceLabel(s)}
                </option>
              ))}
            </select>
          )}
          {repos.size > 1 && (
            <select aria-label="Repository" className={selectClass} value={repoId} onChange={(e) => setRepoId(e.target.value)}>
              <option value="">All repositories</option>
              {[...repos].map(([id, name]) => (
                <option key={id} value={id}>
                  {name}
                </option>
              ))}
            </select>
          )}
        </div>
      )}

      {error && incidents === null && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {incidents === null ? (
        !error && (
          <div className="flex flex-col gap-5">
            <Skeleton className="h-32" />
            <Skeleton className="h-32" />
          </div>
        )
      ) : shown.length === 0 ? (
        <EmptyState title={empty[filter].title}>{empty[filter].text}</EmptyState>
      ) : (
        shown.map((incident) => <IncidentCard key={incident.id} incident={incident} ask={askFor(asks, incident.id)} />)
      )}
    </div>
  )
}
```

- [ ] **Step 3: The route**

In `web/src/App.tsx`: remove `import IncidentsPage from './IncidentsPage.tsx'`, add `import ConversationsPage from './ConversationsPage.tsx'`, and move the route out of the `LegacyPage` group so that the routes read (keep every other route exactly as it is):

```tsx
      <Route element={<AppLayout onSignOut={signOut} />}>
        <Route path="incidents" element={<ConversationsPage />} />
        <Route element={<LegacyPage />}>
          <Route index element={<TimelinePage />} />
          <Route path="incidents/:id" element={<IncidentRoute />} />
          ...the remaining routes unchanged...
        </Route>
      </Route>
```

Delete `web/src/IncidentsPage.tsx` (`git rm`). Check with `grep -rn "IncidentsPage" web/src` that nothing else imports it.

- [ ] **Step 4: Verify and commit**

Run: `cd web && npm test && npm run lint && npm run build`
Expected: all pass, no lint warnings.

```sh
git add web/src
git commit -m "feat(web): the conversations list with state filters and previews" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Today

**Files:**
- Create: `web/src/components/today/Digest.tsx`, `web/src/TodayPage.tsx`
- Modify: `web/src/App.tsx`
- Delete: `web/src/TimelinePage.tsx`

**Interfaces:**
- Consumes: `useIncidents`, `useNow`, `useShellState`, `digestText`, `lastDiagnosed`, `groupByDay`, `dayLabel`, `kindDotClass`, `mergeEntries`, `timeOfDay`, `api.listActivity`, `streamActivity`, `StreamState`, `TimelineEntry`, `RemedyAvatar`, `EmptyState`, `Skeleton`, `Alert`, `Button`, `buttonVariants`.
- Produces: `Digest({ text, pending, diagnosedId, time })`, the default export `TodayPage`, the index route outside `LegacyPage`.

- [ ] **Step 1: The digest**

`web/src/components/today/Digest.tsx`:

```tsx
import { Link } from 'react-router'
import RemedyAvatar from '@/components/RemedyAvatar'
import { buttonVariants } from '@/components/ui/button'

interface Props {
  /** The sentence of Remedy, built by digestText. */
  text: string
  /** How many questions wait for a decision. */
  pending: number
  /** The incident diagnosed last, if there is one. */
  diagnosedId?: number
  /** The time shown in the line above the sentence. */
  time: string
}

/** Remedy's morning words: a sentence, and what to do about it. */
export default function Digest({ text, pending, diagnosedId, time }: Props) {
  return (
    <div className="flex gap-3.5">
      <RemedyAvatar />
      <div className="flex min-w-0 flex-col gap-3">
        <span className="text-xs text-subtle">Remedy · {time}</span>
        <p className="font-serif text-[clamp(19px,2.2vw,24px)] leading-[1.45] text-pretty break-words">{text}</p>
        {(pending > 0 || diagnosedId !== undefined) && (
          <div className="flex flex-wrap gap-2">
            {pending > 0 && (
              <Link to="/approvals" className={buttonVariants({ variant: 'default' })}>
                {pending === 1 ? 'Answer 1 question' : `Answer ${pending} questions`}
              </Link>
            )}
            {diagnosedId !== undefined && (
              <Link to={`/incidents/${diagnosedId}`} className={buttonVariants({ variant: 'outline' })}>
                Read the diagnosis
              </Link>
            )}
          </div>
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 2: The page**

`web/src/TodayPage.tsx`:

```tsx
import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api, ApiError, streamActivity } from './api.ts'
import type { StreamState, TimelineEntry } from './api.ts'
import { digestText, lastDiagnosed } from './conversation.ts'
import { useShellState } from './shellContext.ts'
import { dayLabel, groupByDay, kindDotClass, mergeEntries, timeOfDay } from './timeline.ts'
import { useIncidents } from './useIncidents.ts'
import { useNow } from './useNow.ts'
import EmptyState from '@/components/EmptyState'
import Digest from '@/components/today/Digest'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

type Live = 'connecting' | StreamState

const linkClass = 'whitespace-nowrap text-primary outline-none hover:text-primary-hover focus-visible:ring-3 focus-visible:ring-ring/50 rounded-sm'

/** Today: Remedy's digest of the last 24 hours and the feed of what happened, newest first. */
export default function TodayPage() {
  const { incidents } = useIncidents()
  const { pending } = useShellState()
  const now = useNow(60_000)
  const [entries, setEntries] = useState<TimelineEntry[] | null>(null)
  const [fresh, setFresh] = useState<ReadonlySet<number>>(new Set())
  const [hasMore, setHasMore] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState('')
  const [live, setLive] = useState<Live>('connecting')
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    let active = true
    let stop: (() => void) | undefined
    api
      .listActivity()
      .then((page) => {
        if (!active) return
        setEntries(page.entries)
        setHasMore(page.hasMore)
        setError('')
        // Follow from the newest entry this page has, so that nothing between the list and the stream is lost.
        const newest = page.entries[0]?.id ?? 0
        stop = streamActivity(
          newest,
          (entry) => {
            setFresh((current) => new Set(current).add(entry.id))
            setEntries((current) => mergeEntries(current ?? [], [entry]))
          },
          setLive,
        )
      })
      .catch((e: unknown) => {
        if (active) setError(e instanceof ApiError ? e.message : 'Could not load the timeline')
      })
    return () => {
      active = false
      stop?.()
    }
  }, [attempt])

  async function loadMore() {
    const oldest = entries?.[entries.length - 1]
    if (!oldest) return
    setLoadingMore(true)
    try {
      const page = await api.listActivity(oldest.id)
      setEntries((current) => mergeEntries(current ?? [], page.entries))
      setHasMore(page.hasMore)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Could not load older entries')
    } finally {
      setLoadingMore(false)
    }
  }

  const time = timeOfDay(now.toISOString())

  return (
    <div className="mx-auto flex max-w-190 flex-col gap-6.5 px-4 py-5 md:px-10 md:py-12">
      {live === 'closed' && (
        <div role="status" className="flex flex-wrap items-center gap-3 rounded-2xl bg-soft-diagnosing px-4 py-2.5 text-[13px] text-primary">
          Live updates stopped.
          <Button variant="outline" size="sm" onClick={() => window.location.reload()}>
            Reload
          </Button>
        </div>
      )}

      {incidents === null ? (
        <div className="flex flex-col gap-3">
          <Skeleton className="size-9 rounded-full" />
          <Skeleton className="h-6 w-4/5" />
          <Skeleton className="h-6 w-3/5" />
        </div>
      ) : (
        <Digest text={digestText(now, incidents, pending)} pending={pending} diagnosedId={lastDiagnosed(incidents)?.id} time={time} />
      )}

      {error && (
        <Alert variant="destructive">
          <AlertDescription className="flex flex-wrap items-center justify-between gap-3">
            {error}
            {entries === null && (
              <Button variant="outline" size="sm" onClick={() => setAttempt((n) => n + 1)}>
                Try again
              </Button>
            )}
          </AlertDescription>
        </Alert>
      )}

      {entries === null ? (
        !error && <Skeleton className="h-40" />
      ) : entries.length === 0 ? (
        <EmptyState title="Nothing has happened yet.">
          Connect GitHub and add a repository under{' '}
          <Link to="/settings" className="text-primary hover:text-primary-hover">
            Setup
          </Link>
          .
        </EmptyState>
      ) : (
        <>
          {groupByDay(entries).map((group) => (
            <section key={group.key} className="flex flex-col gap-0.5">
              <div className="mt-1.5 mb-2.5 flex items-center gap-3">
                <span className="h-px flex-1 bg-border" />
                <span className="text-xs text-subtle">{dayLabel(group.key)}</span>
                <span className="h-px flex-1 bg-border" />
              </div>
              {group.entries.map((entry) => (
                <div key={entry.id} className={cn('relative flex gap-3.5 py-2 pl-12.5', fresh.has(entry.id) && 'animate-rm-in')}>
                  <span aria-hidden className={cn('absolute top-3.5 left-3.5 size-2 rounded-full', kindDotClass(entry.kind))} />
                  <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                    <span className="break-words whitespace-pre-wrap text-foreground/90">{entry.summary}</span>
                    <span className="flex flex-wrap gap-x-3 text-xs text-subtle">
                      <time dateTime={entry.at} title={new Date(entry.at).toLocaleString()}>
                        {timeOfDay(entry.at)}
                      </time>
                      {entry.incidentId !== undefined && (
                        <Link to={`/incidents/${entry.incidentId}`} className={linkClass}>
                          Incident #{entry.incidentId}
                        </Link>
                      )}
                      {entry.runId !== undefined && (
                        <Link to={`/runs/${encodeURIComponent(entry.runId)}`} className={linkClass}>
                          See my work
                        </Link>
                      )}
                    </span>
                  </div>
                </div>
              ))}
            </section>
          ))}
          {hasMore && (
            <Button variant="outline" className="self-center" disabled={loadingMore} onClick={() => void loadMore()}>
              {loadingMore ? 'Loading…' : 'Load older entries'}
            </Button>
          )}
        </>
      )}
    </div>
  )
}
```

- [ ] **Step 3: The route**

In `web/src/App.tsx`: remove `import TimelinePage from './TimelinePage.tsx'`, add `import TodayPage from './TodayPage.tsx'`, and make the index route a sibling of the conversations route, outside `LegacyPage`:

```tsx
      <Route element={<AppLayout onSignOut={signOut} />}>
        <Route index element={<TodayPage />} />
        <Route path="incidents" element={<ConversationsPage />} />
        <Route element={<LegacyPage />}>
          <Route path="incidents/:id" element={<IncidentRoute />} />
          <Route path="approvals" element={<ApprovalsPage />} />
          <Route path="runs" element={<RunsPage />} />
          <Route path="runs/:id" element={<RunRoute />} />
          <Route path="settings" element={<SettingsPage />} />
          <Route path="*" element={<NotFound />} />
        </Route>
      </Route>
```

Delete `web/src/TimelinePage.tsx` (`git rm`); `grep -rn "TimelinePage" web/src` must find nothing. `timeline.ts` stays (`TodayPage` uses its helpers).

- [ ] **Step 4: Verify and commit**

Run: `cd web && npm test && npm run lint && npm run build`
Expected: all pass; lint prints nothing (in particular no `react(purity)` warning: the clock is read in `useNow`, not in render).

```sh
git add web/src
git commit -m "feat(web): Today with Remedy's digest and the feed" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Verification (run by the controller, in a real browser)

No code changes unless a defect is found (a defect goes to a fix dispatch). Nothing here is committed.

- [ ] **Step 1: Full checks.** `cd web && npm ci` if `node_modules` is missing; from the worktree root `make check` (fmt, vet, test, web lint, web test, web build) and `go test -tags webui ./web/` after `make web-build`. Expected: PASS.

- [ ] **Step 2: Run the app** with a throwaway database in the scratchpad (server with `REMEDY_ADMIN_PASSWORD`, `REMEDY_RUNNER_TOKEN`, `REMEDY_MASTER_KEY`, `REMEDY_DB`; `npm run dev -- --host 127.0.0.1` in `web/`). Seed (nine fractional digits in timestamps): incidents in every state (open, diagnosing, diagnosed with a stored `diagnosis` JSON and `last_diagnosis_at`, resolved with `resolved_reason` `green`, ignored), one of them 80+ characters long, sources alertmanager and argocd (and a GitHub one with a connection and repository row if the schema makes that easy), a pending `tool_calls` row (`cluster_rollout_restart`, `waiting`, `pending`) for the diagnosed incident and another for the ignored one, and 15 `activity` rows over two days with and without `incident_id`/`run_id`.

- [ ] **Step 3: Check Conversations** at 1280x800 and 390x844: the cards (marker, title, source or repository and ref, age, preview in Literata, chips; "asks you" on the active incident with the waiting call and not on the ignored one); the preview texts for every state (ignored, resolved: "Resolved: the check turned green.", the waiting question, "I'm looking into it…", the diagnosis summary, "I haven't looked yet."); the filter pills with counts that change with the source select; the selects appear (two sources); each filter's empty state (delete rows or use a filter that is empty); long titles break without sideways scrolling; keyboard focus ring on cards and pills; a click opens `/incidents/:id`.

- [ ] **Step 4: Check Today** at both widths: the digest sentence matches the seeded data (counts, singular/plural, the waiting questions), "Answer N questions" goes to Needs you and "Read the diagnosis" to the diagnosed incident; the feed in day sections ("Today", "Yesterday"), dots in the token colours, "Incident #N" and "See my work" links; "Load older entries" with more than 50 rows; an entry inserted live fades in (insert a row with `sqlite3` while the page is open: it appears within about a second and only that one animates); with the server stopped the shell banner shows and, after a restart and a new login, "Live updates stopped." with Reload appears when the stream has ended; with an empty database the quiet digest and the empty feed with the link to Setup.

- [ ] **Step 5: Check the sidebar** at 1280 px: each open conversation shows title, age and the preview line truncated; the waiting question shows for the diagnosed incident.

- [ ] **Step 6: Clean up.** Stop the processes you started (by PID), delete the throwaway database, `.playwright-mcp/` and any `*.png`.

---

## Self-Review

**Spec coverage (spec 5):** 5.1 Today: digest in the first person with avatar and time, deterministic sentence over 24 hours with the clause rules (Task 1 `digestText`, tested), actions only with pending approvals and a diagnosed incident (Task 4 `Digest`), feed in day sections with tokens, live fade-in only for live entries, "Load older entries", stream-closed line with Reload, empty state with the link to Setup. 5.2 Conversations: card with marker, title, repository/ref or source label, age, preview, chips including "asks you" via `ToolCall.incidentId`; pills with counts; source and repository selects only when there is more than one; preview rules in the specified order; the sidebar uses the same function; empty states per filter; `incidentStateColor`/`StateBadge` tokens (`incidentStateColor` was moved in part 1; `StateBadge` is used by the legacy thread until part 3); the pure functions live in `conversation.ts`. The spec put `askText` in part 3 (6.3); it moves into this part because the preview needs it, and part 3 reuses it.

**Placeholder scan:** none. Task 2 Step 3 and Task 3 Step 3 name the surrounding code to keep when the file differs slightly; the new code is given in full.

**Type consistency:** `ShellState.asks: ToolCall[]` (Task 1) is read by `useShellState()` (Task 2) and by `askFor(asks, id)` (Tasks 2, 3). `incidentPreview(incident, ask?)` takes a `ToolCall`, as `askFor` returns. `StateFilter` is exported by `conversation.ts` and imported by `ConversationsPage`. `Digest` props `text`, `pending`, `diagnosedId`, `time` match the call in `TodayPage`. `RemedyMark`'s new `fill` prop is used by `EmptyState`.

**Review Focus coverage:** 1 and 3 and 4 (Task 1 tests), 2 and 5 (Task 3 code; Task 5 steps), 6 (Task 4 code and Task 5), 7 (documented), 8 (Tasks 3 to 5).
