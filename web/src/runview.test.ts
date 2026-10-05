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

  it('makes a step of a tool_use without input or id, and takes a result with null content as empty', () => {
    const steps = runSteps([assistant(1, { type: 'tool_use', name: 'mcp__remedy__incident_list' }), assistant(2, use('t2', 'Read', { file_path: 'a' })), ev(3, 'user', { message: { content: [{ type: 'tool_result', tool_use_id: 't2', content: null }] } })])
    assert.equal(steps.length, 2)
    assert.equal(steps[0].id, 'seq1-0')
    assert.equal(steps[0].label, 'Looked at the incidents')
    assert.equal(steps[0].raw, 'incident_list')
    assert.equal(steps[0].done, false)
    assert.equal(steps[1].done, true)
    assert.equal(steps[1].failed, false)
    assert.equal(steps[1].raw, 'Read {"file_path":"a"}')
  })

  it('leaves no dangling arrow for a result without text, and keeps "error" for a failed one', () => {
    const steps = runSteps([
      assistant(1, use('t1', 'Read', { file_path: 'a' })),
      result(2, 't1', ''),
      assistant(3, use('t2', 'Read', { file_path: 'b' })),
      ev(4, 'user', { message: { content: [{ type: 'tool_result', tool_use_id: 't2', is_error: true, content: '  ' }] } }),
      assistant(5, use('t3', 'Read', { file_path: 'c' })),
      ev(6, 'user', { message: { content: [{ type: 'tool_result', tool_use_id: 't3', is_error: true, content: 'denied' }] } }),
    ])
    assert.equal(steps[0].raw, 'Read {"file_path":"a"}')
    assert.equal(steps[0].done, true)
    assert.equal(steps[1].raw, 'Read {"file_path":"b"} → error')
    assert.equal(steps[1].failed, true)
    assert.equal(steps[2].raw, 'Read {"file_path":"c"} → error: denied')
  })

  it('cuts every value that a label inserts', () => {
    const long = 'a'.repeat(500)
    const [read, pods] = runSteps([assistant(1, use('t1', 'Read', { file_path: long })), assistant(2, use('t2', 'mcp__remedy__cluster_pods', { namespace: `${long}\n${long}` }))])
    assert.ok(read.label.length < 120)
    assert.ok(read.label.includes('…'))
    assert.ok(pods.label.length < 120)
    assert.ok(!pods.label.includes('\n'))
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
