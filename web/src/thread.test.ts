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

  it('shows the failure of the last responder run instead of a fresh incident', () => {
    const failed = run({ id: 'rf', role: 'responder', status: 'failed' })
    const mk = (over: Partial<Incident> = {}, lastResponder: Run | undefined = failed) =>
      buildThread({ incident: inc(over), activity: [], questionRuns: [], asks: [], lastResponder }).find((i) => i.type === 'undiagnosed')
    for (const autoDiagnose of [true, false]) {
      const u = mk({ autoDiagnose })
      assert.ok(u && u.type === 'undiagnosed')
      assert.equal(u.reason, 'failed')
      assert.equal(u.runId, 'rf')
    }
  })

  it('keeps fresh and manual when the last responder run did not fail or there is none', () => {
    const mk = (over: Partial<Incident>, lastResponder?: Run) =>
      buildThread({ incident: inc(over), activity: [], questionRuns: [], asks: [], lastResponder }).find((i) => i.type === 'undiagnosed')
    const ok = run({ role: 'responder', status: 'succeeded' })
    for (const last of [ok, undefined]) {
      const fresh = mk({}, last)
      assert.ok(fresh && fresh.type === 'undiagnosed' && fresh.reason === 'fresh' && fresh.runId === undefined)
      const manual = mk({ autoDiagnose: false }, last)
      assert.ok(manual && manual.type === 'undiagnosed' && manual.reason === 'manual')
    }
  })

  it('lets the source win over a failed responder run', () => {
    const items = buildThread({
      incident: inc({ source: 'alertmanager', repoId: 0, repo: '', ref: '' }),
      activity: [],
      questionRuns: [],
      asks: [],
      lastResponder: run({ role: 'responder', status: 'failed' }),
    })
    const u = items.find((i) => i.type === 'undiagnosed')
    assert.ok(u && u.type === 'undiagnosed' && u.reason === 'source')
  })

  it('shows a failure when a diagnosis was started and nothing came of it, even without a failed run in the list', () => {
    const at = '2026-10-05T07:50:00Z'
    const mk = (over: Partial<Incident>, lastResponder?: Run) =>
      buildThread({ incident: inc(over), activity: [], questionRuns: [], asks: [], lastResponder }).find((i) => i.type === 'undiagnosed')
    for (const last of [undefined, run({ role: 'responder', status: 'succeeded' })]) {
      const u = mk({ lastDiagnosisAt: at }, last)
      assert.ok(u && u.type === 'undiagnosed')
      assert.equal(u.reason, 'failed')
      assert.equal(u.runId, undefined)
    }
    const withRun = mk({ lastDiagnosisAt: at }, run({ id: 'rf', role: 'responder', status: 'failed' }))
    assert.ok(withRun && withRun.type === 'undiagnosed' && withRun.reason === 'failed' && withRun.runId === 'rf')
    const none = mk({})
    assert.ok(none && none.type === 'undiagnosed' && none.reason === 'fresh' && none.runId === undefined)
    const manual = mk({ autoDiagnose: false })
    assert.ok(manual && manual.type === 'undiagnosed' && manual.reason === 'manual')
    const other = mk({ source: 'alertmanager', repoId: 0, repo: '', ref: '', lastDiagnosisAt: at })
    assert.ok(other && other.type === 'undiagnosed' && other.reason === 'source')
  })

  it('does not change a stored diagnosis because a diagnosis was started', () => {
    const items = buildThread({ incident: inc({ state: 'diagnosed', diagnosis, lastDiagnosisAt: '2026-10-05T07:50:00Z' }), activity: [], questionRuns: [], asks: [] })
    assert.ok(!types(items).includes('undiagnosed'))
    assert.ok(types(items).includes('diagnosis'))
  })

  it('does not change a stored diagnosis because the last responder run failed', () => {
    const items = buildThread({ incident: inc({ state: 'diagnosed', diagnosis }), activity: [], questionRuns: [], asks: [], lastResponder: run({ role: 'responder', status: 'failed' }) })
    assert.ok(!types(items).includes('undiagnosed'))
    assert.ok(types(items).includes('diagnosis'))
  })

  it('points the diagnosis at the succeeded responder run, and falls back to the latest run of the incident', () => {
    const mk = (diagnosisRun?: Run) =>
      buildThread({ incident: inc({ state: 'diagnosed', diagnosis, runId: 'latest' }), activity: [], questionRuns: [], asks: [], diagnosisRun }).find((i) => i.type === 'diagnosis')
    const d = mk(run({ id: 'rok', role: 'responder' }))
    assert.ok(d && d.type === 'diagnosis' && d.runId === 'rok')
    const fallback = mk()
    assert.ok(fallback && fallback.type === 'diagnosis' && fallback.runId === 'latest')
  })

  it('shows both the diagnosis and that it is working when the incident is diagnosed again', () => {
    const items = buildThread({ incident: inc({ state: 'diagnosing', diagnosis, runId: 'rd', lastDiagnosisAt: '2026-10-05T08:30:00Z' }), activity: [], questionRuns: [], asks: [] })
    assert.equal(items.filter((i) => i.type === 'diagnosis').length, 1)
    const w = items.filter((i) => i.type === 'working')
    assert.equal(w.length, 1)
    assert.equal(w[0].at, '2026-10-05T08:30:00Z')
  })

  it('times the diagnosis by the finished entry, then the last diagnosis, then the last sighting', () => {
    const at = (over: Partial<Incident>, activity: ActivityEntry[]) =>
      buildThread({ incident: inc({ state: 'diagnosed', diagnosis, ...over }), activity, questionRuns: [], asks: [] }).find((i) => i.type === 'diagnosis')?.at
    assert.equal(at({ lastDiagnosisAt: '2026-10-05T07:50:00Z' }, [act(1, 'diagnosis_finished', '2026-10-05T08:00:00Z')]), '2026-10-05T08:00:00Z')
    assert.equal(at({ lastDiagnosisAt: '2026-10-05T07:50:00Z' }, []), '2026-10-05T07:50:00Z')
    assert.equal(at({}, []), '2026-10-05T07:00:00Z')
  })

  it('names the head commit when the diagnosed commit is empty', () => {
    const d = buildThread({ incident: inc({ state: 'diagnosed', diagnosis, diagnosedSha: '' }), activity: [], questionRuns: [], asks: [] }).find((i) => i.type === 'diagnosis')
    assert.ok(d && d.type === 'diagnosis')
    assert.equal(d.sha, '3f9c2ab')
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

  it('does not count the approvals of a responder run among the question runs', () => {
    const items = buildThread({ incident: inc(), activity: [], questionRuns: [run({ id: 'rr', role: 'responder' })], asks: [call({ id: 11, runId: 'rr', incidentId: undefined })] })
    assert.ok(!types(items).includes('ask'))
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
