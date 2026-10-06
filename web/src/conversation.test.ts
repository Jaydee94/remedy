import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import type { Diagnosis, Incident, ToolCall } from './api.ts'
import {
  askFor,
  digestText,
  filterCounts,
  filterIncidents,
  incidentPreview,
  lastDiagnosed,
  mergeIncidents,
  scopeIncidents,
} from './conversation.ts'

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

describe('scopeIncidents', () => {
  const list = [
    inc({ id: 1, repoId: 1, repo: 'octo/hello' }),
    inc({ id: 2, repoId: 2, repo: 'octo/world' }),
    inc({ id: 3, source: 'alertmanager', repoId: 0, repo: '' }),
    inc({ id: 4, repoId: 2, repo: 'octo/world' }),
  ]
  const ids = (s: { incidents: Incident[] }) => s.incidents.map((i) => i.id)

  it('without a selection returns everything, the sources once and only the github repositories', () => {
    const s = scopeIncidents(list, '', '')
    assert.deepEqual(ids(s), [1, 2, 3, 4])
    assert.deepEqual(s.sources, ['github', 'alertmanager'])
    assert.deepEqual([...s.repos], [
      [1, 'octo/hello'],
      [2, 'octo/world'],
    ])
    assert.equal(s.source, '')
    assert.equal(s.repoId, '')
  })

  it('narrows by a source', () => {
    const s = scopeIncidents(list, 'alertmanager', '')
    assert.deepEqual(ids(s), [3])
    assert.equal(s.source, 'alertmanager')
  })

  it('narrows by a repository given as text', () => {
    const s = scopeIncidents(list, '', '2')
    assert.deepEqual(ids(s), [2, 4])
    assert.equal(s.repoId, '2')
  })

  it('treats a source that is not in the list as no selection', () => {
    const s = scopeIncidents(list, 'argocd', '')
    assert.equal(s.source, '')
    assert.deepEqual(ids(s), [1, 2, 3, 4])
  })

  it('treats a repository that is not in the list as no selection', () => {
    const s = scopeIncidents(list, '', '99')
    assert.equal(s.repoId, '')
    assert.deepEqual(ids(s), [1, 2, 3, 4])
  })

  it('treats an empty or non-numeric repository as no selection', () => {
    assert.deepEqual(ids(scopeIncidents(list, '', '')), [1, 2, 3, 4])
    const s = scopeIncidents(list, '', 'abc')
    assert.equal(s.repoId, '')
    assert.deepEqual(ids(s), [1, 2, 3, 4])
  })

  it('combines both selections', () => {
    const s = scopeIncidents(list, 'github', '1')
    assert.deepEqual(ids(s), [1])
    assert.equal(scopeIncidents(list, 'alertmanager', '1').incidents.length, 0)
  })

  it('recovers when the selected repository drops out of the list', () => {
    const s = scopeIncidents([inc({ id: 1, repoId: 1 })], '', '2')
    assert.equal(s.repoId, '')
    assert.deepEqual(ids(s), [1])
  })
})

describe('scopeIncidents selections only count while their select is visible', () => {
  const ids = (s: { incidents: Incident[] }) => s.incidents.map((i) => i.id)

  it('ignores a source selection when the list has a single source', () => {
    const single = [inc({ id: 1 }), inc({ id: 2, repoId: 2, repo: 'octo/world' })]
    const s = scopeIncidents(single, 'github', '')
    assert.equal(s.source, '')
    assert.deepEqual(ids(s), [1, 2])
  })

  it('ignores a repository selection when the list has a single repository', () => {
    const single = [inc({ id: 1 }), inc({ id: 2, source: 'alertmanager', repoId: 0, repo: '' })]
    const s = scopeIncidents(single, '', '1')
    assert.equal(s.repoId, '')
    assert.deepEqual(ids(s), [1, 2])
  })

  it('keeps the sources and repositories complete after narrowing', () => {
    const list = [
      inc({ id: 1, repoId: 1, repo: 'octo/hello' }),
      inc({ id: 2, repoId: 2, repo: 'octo/world' }),
      inc({ id: 3, source: 'alertmanager', repoId: 0, repo: '' }),
    ]
    const s = scopeIncidents(list, 'github', '1')
    assert.deepEqual(ids(s), [1])
    assert.deepEqual(s.sources, ['github', 'alertmanager'])
    assert.deepEqual([...s.repos.keys()], [1, 2])
  })
})

describe('mergeIncidents', () => {
  const ids = (l: Incident[]) => l.map((i) => i.id)

  it('appends the ignored incidents that all does not have', () => {
    assert.deepEqual(ids(mergeIncidents([inc({ id: 3 }), inc({ id: 1 })], [inc({ id: 7, state: 'ignored' })])), [3, 1, 7])
  })

  it('drops duplicates by id and the entry of all wins', () => {
    const merged = mergeIncidents([inc({ id: 2, title: 'from all' })], [inc({ id: 2, title: 'from ignored' }), inc({ id: 5 })])
    assert.deepEqual(ids(merged), [2, 5])
    assert.equal(merged[0]?.title, 'from all')
  })

  it('handles empty inputs', () => {
    assert.deepEqual(mergeIncidents([], []), [])
    assert.deepEqual(ids(mergeIncidents([], [inc({ id: 4 })])), [4])
    assert.deepEqual(ids(mergeIncidents([inc({ id: 4 })], [])), [4])
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

  it('shows the question of a waiting call even while the incident is diagnosing', () => {
    assert.equal(incidentPreview(inc({ state: 'diagnosing' }), call()), 'May I restart guestbook-ui in guestbook?')
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

  it('says that the last diagnosis did not finish, whether or not it runs automatically', () => {
    const at = '2026-10-05T12:00:00.000000000Z'
    assert.equal(incidentPreview(inc({ state: 'open', lastDiagnosisAt: at })), "My last diagnosis didn't finish.")
    assert.equal(incidentPreview(inc({ state: 'open', autoDiagnose: false, lastDiagnosisAt: at })), "My last diagnosis didn't finish.")
    assert.equal(incidentPreview(inc({ state: 'open' })), "I haven't looked yet.")
  })

  it('keeps the stored diagnosis, a waiting ask, diagnosing, ignored and resolved ahead of a failed diagnosis', () => {
    const at = '2026-10-05T12:00:00.000000000Z'
    assert.equal(incidentPreview(inc({ state: 'open', diagnosis, lastDiagnosisAt: at })), 'The Renovate bump renamed a key.')
    assert.equal(incidentPreview(inc({ state: 'open', lastDiagnosisAt: at }), call()), 'May I restart guestbook-ui in guestbook?')
    assert.equal(incidentPreview(inc({ state: 'diagnosing', lastDiagnosisAt: at })), "I'm looking into it…")
    assert.equal(incidentPreview(inc({ state: 'ignored', lastDiagnosisAt: at })), 'You ignored this incident.')
    assert.equal(incidentPreview(inc({ state: 'resolved', lastDiagnosisAt: at })), 'Resolved.')
  })
})

describe('digestText', () => {
  const now = new Date('2026-10-05T12:00:00Z')
  const hoursAgo = (h: number) => new Date(now.getTime() - h * 3600_000).toISOString()

  it('counts what opened and what was diagnosed in the last 24 hours', () => {
    const list = [
      inc({ id: 1, firstSeen: hoursAgo(3) }),
      inc({ id: 2, firstSeen: hoursAgo(5), state: 'diagnosed', diagnosis, lastDiagnosisAt: hoursAgo(4), diagnosedAt: hoursAgo(4) }),
      inc({ id: 3, firstSeen: hoursAgo(30) }),
    ]
    assert.equal(digestText(now, list, 0), 'In the last 24 hours I opened 2 incidents and diagnosed 1.')
  })

  it('uses the singular for one incident', () => {
    assert.equal(digestText(now, [inc({ firstSeen: hoursAgo(1) })], 0), 'In the last 24 hours I opened 1 incident.')
  })

  it('leaves out a clause with a count of 0 and names the incidents when only diagnoses are counted', () => {
    const list = [inc({ firstSeen: hoursAgo(40), state: 'diagnosed', diagnosis, lastDiagnosisAt: hoursAgo(2), diagnosedAt: hoursAgo(2) })]
    assert.equal(digestText(now, list, 0), 'In the last 24 hours I diagnosed 1 incident.')
  })

  it('does not count a diagnosis that is older than 24 hours or that has no stored answer', () => {
    const list = [
      inc({ id: 1, firstSeen: hoursAgo(40), state: 'diagnosed', diagnosis, lastDiagnosisAt: hoursAgo(30), diagnosedAt: hoursAgo(30) }),
      inc({ id: 2, firstSeen: hoursAgo(40), state: 'diagnosing', lastDiagnosisAt: hoursAgo(1) }),
    ]
    assert.equal(digestText(now, list, 0), "All quiet. Nothing opened in the last 24 hours, and I'm still watching.")
  })

  it('counts an incident that is diagnosing again by when its stored answer was written', () => {
    const list = [inc({ firstSeen: hoursAgo(40), state: 'diagnosing', diagnosis, lastDiagnosisAt: hoursAgo(1), diagnosedAt: hoursAgo(3) })]
    assert.equal(digestText(now, list, 0), 'In the last 24 hours I diagnosed 1 incident.')
  })

  it('counts a diagnosis by diagnosedAt whatever lastDiagnosisAt says', () => {
    const list = [inc({ firstSeen: hoursAgo(40), state: 'diagnosed', diagnosis, lastDiagnosisAt: hoursAgo(30), diagnosedAt: hoursAgo(2) })]
    assert.equal(digestText(now, list, 0), 'In the last 24 hours I diagnosed 1 incident.')
  })

  it('does not count an old diagnosis whose re-diagnosis failed recently', () => {
    const list = [inc({ firstSeen: hoursAgo(40), state: 'open', diagnosis, lastDiagnosisAt: hoursAgo(1), diagnosedAt: hoursAgo(30) })]
    assert.equal(digestText(now, list, 0), "All quiet. Nothing opened in the last 24 hours, and I'm still watching.")
  })

  it('does not count a diagnosis without diagnosedAt, as from a server that does not send it', () => {
    const list = [inc({ firstSeen: hoursAgo(40), state: 'diagnosed', diagnosis, lastDiagnosisAt: hoursAgo(1) })]
    assert.equal(digestText(now, list, 0), "All quiet. Nothing opened in the last 24 hours, and I'm still watching.")
  })

  it('counts an incident first seen exactly 24 hours ago and not one a millisecond older', () => {
    const edge = new Date(now.getTime() - 24 * 3600_000)
    assert.equal(digestText(now, [inc({ firstSeen: edge.toISOString() })], 0), 'In the last 24 hours I opened 1 incident.')
    const older = new Date(edge.getTime() - 1).toISOString()
    assert.equal(digestText(now, [inc({ firstSeen: older })], 0), "All quiet. Nothing opened in the last 24 hours, and I'm still watching.")
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
      inc({ id: 1, diagnosis, diagnosedAt: '2026-10-05T08:00:00Z' }),
      inc({ id: 2, diagnosis, diagnosedAt: '2026-10-05T09:00:00Z' }),
      inc({ id: 3, diagnosedAt: '2026-10-05T10:00:00Z' }),
    ]
    assert.equal(lastDiagnosed(list)?.id, 2)
  })

  it('goes by when the diagnosis was written, not when the last one was started', () => {
    const list = [
      inc({ id: 1, diagnosis, diagnosedAt: '2026-10-05T08:00:00Z', lastDiagnosisAt: '2026-10-05T11:00:00Z' }),
      inc({ id: 2, diagnosis, diagnosedAt: '2026-10-05T09:00:00Z', lastDiagnosisAt: '2026-10-05T09:00:00Z' }),
    ]
    assert.equal(lastDiagnosed(list)?.id, 2)
  })

  it('skips an incident without diagnosedAt or whose time cannot be parsed', () => {
    const bad = inc({ id: 1, diagnosis, diagnosedAt: 'not a date' })
    const none = inc({ id: 3, diagnosis, lastDiagnosisAt: '2026-10-05T10:00:00Z' })
    const good = inc({ id: 2, diagnosis, diagnosedAt: '2026-10-05T08:00:00Z' })
    assert.equal(lastDiagnosed([bad, good])?.id, 2)
    assert.equal(lastDiagnosed([good, bad])?.id, 2)
    assert.equal(lastDiagnosed([none, good])?.id, 2)
    assert.equal(lastDiagnosed([bad]), undefined)
    assert.equal(lastDiagnosed([none]), undefined)
  })

  it('is undefined when nothing was diagnosed', () => {
    assert.equal(lastDiagnosed([inc()]), undefined)
    assert.equal(lastDiagnosed([]), undefined)
  })
})
