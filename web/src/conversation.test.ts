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
