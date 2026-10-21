import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import type { Host, HostLearning, Skill } from './api.ts'
import { LEARN_ROOM, learnActive, learnBlocked, learnOutcome, learnProgress, learnRunPath, nextHost, proposalsLine, proposalsWaiting } from './learn.ts'

const ready = (over: Partial<Host> = {}): Host => ({
  id: 1, name: 'nas', address: '10.0.0.5', port: 22, account: 'remedy', notes: '', dockerPattern: 'remedy-*', systemdUnits: [], enabled: true, matchers: '',
  status: 'ok', statusDetail: '', credential: { kind: 'password', hint: 'set', updatedAt: 'x' }, pinnedFingerprint: 'SHA256:abc',
  createdAt: '2026-10-20T10:00:00Z', updatedAt: '2026-10-20T10:00:00Z', ...over,
})
const learning = (over: Partial<HostLearning> = {}): HostLearning => ({ runId: 'r1', status: 'succeeded', createdAt: '2026-10-20T10:00:00Z', finishedAt: '2026-10-20T10:05:00Z', ...over })
const proposals = (n: number): Skill[] => Array.from({ length: n }, (_, i) => ({ id: i + 1, hostId: 1, name: `p${i}`, state: 'proposed' }) as Skill)
const now = Date.parse('2026-10-20T12:05:00Z')

describe('learnBlocked', () => {
  it('lets a host that is connected, with a login and a pinned key, be learned about', () => {
    assert.equal(learnBlocked(ready(), []), '')
    assert.equal(learnBlocked(ready(), null), '')
  })
  it('says why a host cannot, in the order of what to do first', () => {
    assert.match(learnBlocked(ready({ enabled: false }), []), /turned off/)
    assert.match(learnBlocked(ready({ credential: undefined }), []), /login/)
    assert.match(learnBlocked(ready({ pinnedFingerprint: undefined }), []), /pin it first/)
    assert.match(learnBlocked(ready({ status: 'new' }), []), /Test the connection first/)
    assert.match(learnBlocked(ready({ status: 'untrusted' }), []), /Test the connection first/)
    assert.match(learnBlocked(ready({ status: 'error' }), []), /Test the connection first/)
    assert.match(learnBlocked(ready({ status: 'key_changed' }), []), /Test the connection first/)
  })
  it('is blocked while a run is going', () => {
    assert.match(learnBlocked(ready({ learning: learning({ status: 'running' }) }), []), /is going/)
    assert.match(learnBlocked(ready({ learning: learning({ status: 'queued' }) }), []), /is going/)
    assert.equal(learnBlocked(ready({ learning: learning({ status: 'failed' }) }), []), '')
  })
  it('is blocked when too many proposals wait, at the number the server refuses', () => {
    assert.equal(learnBlocked(ready(), proposals(LEARN_ROOM)), '')
    assert.match(learnBlocked(ready(), proposals(LEARN_ROOM + 1)), /12 proposals of this host wait for your review/)
  })
})

describe('proposalsWaiting and proposalsLine', () => {
  it('counts only proposals', () => {
    assert.equal(proposalsWaiting([...proposals(2), { id: 9, state: 'active' } as Skill]), 2)
    assert.equal(proposalsWaiting(null), 0)
  })
  it('says it in words', () => {
    assert.equal(proposalsLine(0), '')
    assert.equal(proposalsLine(1), '1 proposal waits for your review')
    assert.equal(proposalsLine(3), '3 proposals wait for your review')
  })
})

describe('learnOutcome and learnProgress', () => {
  it('says nothing before a run and while one is going', () => {
    assert.equal(learnOutcome(undefined, now), '')
    assert.equal(learnOutcome(learning({ status: 'running', finishedAt: undefined }), now), '')
    assert.equal(learnProgress(learning({ status: 'queued' })), 'Waiting for the runner…')
    assert.equal(learnProgress(learning({ status: 'running' })), 'Getting to know the host…')
    assert.equal(learnProgress(learning()), '')
    assert.equal(learnProgress(undefined), '')
  })
  it('says how the last run ended and that a failure stored nothing', () => {
    assert.equal(learnOutcome(learning(), now), 'The last learning run finished 2 h ago.')
    assert.match(learnOutcome(learning({ status: 'failed', failureReason: 'invalid_output' }), now), /answer was not valid, so I stored nothing/)
    assert.match(learnOutcome(learning({ status: 'failed', failureReason: 'timeout' }), now), /took too long/)
    assert.match(learnOutcome(learning({ status: 'failed', failureReason: 'cancelled' }), now), /was cancelled/)
    assert.match(learnOutcome(learning({ status: 'failed', failureReason: 'runner_lost' }), now), /lost the runner/)
    assert.match(learnOutcome(learning({ status: 'failed' }), now), /failed\. I stored nothing/)
  })
})

describe('the rest', () => {
  it('links to the run, with an id that is encoded', () => {
    assert.equal(learnRunPath(learning({ runId: 'a/b' })), '/runs/a%2Fb')
    assert.equal(learnRunPath(undefined), null)
  })
  it('keeps the old host when a poll failed', () => {
    const old = ready()
    assert.equal(nextHost(old, { status: 'rejected', reason: new Error('x') }), old)
    const fresh = ready({ name: 'fresh' })
    assert.equal(nextHost(old, { status: 'fulfilled', value: fresh }), fresh)
  })
  it('is active while queued or running only', () => {
    assert.equal(learnActive(learning({ status: 'queued' })), true)
    assert.equal(learnActive(learning({ status: 'running' })), true)
    assert.equal(learnActive(learning({ status: 'succeeded' })), false)
    assert.equal(learnActive(learning({ status: 'failed' })), false)
    assert.equal(learnActive(undefined), false)
  })
})
