import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import type { ToolCall } from './api.ts'
import { answeredAt, decisionView, lockAfterChange, mergeApprovals, splitApprovals } from './needs.ts'

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

  it('lists the answered ones by the time of the answer, newest first, a tie by id', () => {
    const calls = [
      call({ id: 1, decision: 'approved', status: 'succeeded', requestedAt: '2026-10-05T09:00:00Z', decidedAt: '2026-10-05T11:00:00Z' }),
      call({ id: 2, decision: 'denied', status: 'denied', requestedAt: '2026-10-05T10:00:00Z', decidedAt: '2026-10-05T10:30:00Z' }),
      call({ id: 3, decision: 'denied', status: 'denied', requestedAt: '2026-10-05T10:00:00Z', decidedAt: '2026-10-05T10:30:00Z' }),
    ]
    assert.deepEqual(splitApprovals(calls).answered.map((c) => c.id), [1, 3, 2])
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
  })
  it('says approved, not run yet for an approved action that waits for its turn', () => {
    const v = decisionView(call({ decision: 'approved', status: 'waiting' }))
    assert.equal(v.label, 'approved, not run yet')
    assert.equal(v.text, 'text-primary')
  })
  it('says approved, did not run for an approved action that was abandoned or denied afterwards', () => {
    for (const status of ['abandoned', 'denied'] as const) {
      const v = decisionView(call({ decision: 'approved', status }))
      assert.equal(v.label, 'approved, did not run')
      assert.equal(v.text, 'text-muted-foreground')
    }
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

describe('lockAfterChange', () => {
  it('locks when a call that was pending is gone', () => {
    assert.equal(lockAfterChange([1, 2], [2], 1000), 1700)
  })
  it('does not lock for an append', () => {
    assert.equal(lockAfterChange([1, 2], [1, 2, 3], 1000), null)
  })
  it('does not lock when nothing was pending', () => {
    assert.equal(lockAfterChange([], [4], 1000), null)
    assert.equal(lockAfterChange([], [], 1000), null)
  })
  it('does not lock for a reorder without a removal', () => {
    assert.equal(lockAfterChange([1, 2, 3], [3, 1, 2], 1000), null)
  })
  it('locks for a removal together with an append, and takes the time from the caller', () => {
    assert.equal(lockAfterChange([1, 2], [2, 3], 1000), 1700)
    assert.equal(lockAfterChange([1], [], 1000, 250), 1250)
  })
})

describe('mergeApprovals', () => {
  it('takes the pending list as it is and adds the answered calls of the full list that are not pending', () => {
    const pending = [call({ id: 5 }), call({ id: 6 })]
    const all = [
      call({ id: 6 }),
      call({ id: 4, decision: 'approved', status: 'succeeded' }),
      call({ id: 3, decision: 'denied', status: 'denied' }),
    ]
    assert.deepEqual(mergeApprovals(pending, all).map((c) => c.id), [5, 6, 4, 3])
  })
  it('leaves out a call the full list still has as pending but the pending list no longer has', () => {
    assert.deepEqual(mergeApprovals([], [call({ id: 7 })]), [])
  })
  it('takes the pending version of a call that both lists have', () => {
    const merged = mergeApprovals([call({ id: 5, reason: 'new' })], [call({ id: 5, decision: 'approved', status: 'succeeded' })])
    assert.equal(merged.length, 1)
    assert.equal(merged[0].reason, 'new')
  })
  it('does not change its input', () => {
    const pending = [call({ id: 1 })]
    mergeApprovals(pending, [call({ id: 2, decision: 'denied', status: 'denied' })])
    assert.equal(pending.length, 1)
  })
})
