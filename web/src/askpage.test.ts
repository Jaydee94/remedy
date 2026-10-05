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
    assert.ok(r.title.endsWith('…'))
  })
  it('leaves no space before the ellipsis when the cut falls right after a space', () => {
    const r = recentRun(run({ prompt: `${'a'.repeat(139)} ${'b'.repeat(50)}` }), [])
    assert.ok(r.title.endsWith('…'))
    assert.ok(!r.title.endsWith(' …'))
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
