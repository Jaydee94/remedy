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
