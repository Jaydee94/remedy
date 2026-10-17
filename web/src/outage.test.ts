import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import type { Diagnosis, OutageDiagnosis, ToolCall } from './api.ts'
import { actionText, expiryText, isOutage, outageCategoryText } from './outage.ts'

const outage = (over: Partial<OutageDiagnosis> = {}): OutageDiagnosis => ({
  summary: 's', cause: 'c', confidence: 'high', category: 'resource_pressure', affected: ['deployment/demo/web'], proposed_action: 'Raise the limit.', evidence: 'e', ...over,
})
const github: Diagnosis = { summary: 's', cause: 'c', confidence: 'high', category: 'build_error', affected_files: [], proposed_fix: 'f', fix_looks_automatable: false }
const call = (over: Partial<ToolCall> = {}): ToolCall => ({
  id: 1, runId: 'r', tool: 'cluster_rollout_restart', kind: 'mutating', arguments: {}, status: 'waiting', decision: 'pending', requestedAt: '2026-10-16T12:00:00Z', waiting: true, ...over,
})

describe('isOutage', () => {
  it('tells the two shapes apart by their members', () => {
    assert.equal(isOutage(outage()), true)
    assert.equal(isOutage(github), false)
  })
})

describe('outageCategoryText', () => {
  it('says each category in words and passes an unknown one through', () => {
    assert.equal(outageCategoryText('workload_crash'), 'A workload keeps crashing')
    assert.equal(outageCategoryText('host_problem'), 'A problem on the machine')
    assert.equal(outageCategoryText('image_pull'), "An image can't be pulled")
    assert.equal(outageCategoryText('something_new'), 'something_new')
  })
})

describe('actionText', () => {
  it('says there is nothing to do for "none", in any case and with spaces', () => {
    for (const none of ['none', 'None', '  none  ', 'NONE']) assert.deepEqual(actionText(outage({ proposed_action: none })), { none: true, text: 'Nothing to do for now.' })
  })
  it('keeps any other text', () => {
    assert.deepEqual(actionText(outage()), { none: false, text: 'Raise the limit.' })
    assert.equal(actionText(outage({ proposed_action: 'none of the pods restart' })).none, false)
  })
})

describe('expiryText', () => {
  const now = Date.parse('2026-10-16T12:00:00Z')
  it('says when I stop waiting for a call that waits and has a deadline', () => {
    assert.equal(expiryText(call({ expiresAt: '2026-10-16T12:14:00Z' }), now), 'I stop waiting in 14 minutes.')
    assert.equal(expiryText(call({ expiresAt: '2026-10-16T12:00:30Z' }), now), 'I stop waiting in 30 seconds.')
  })
  it('says nothing for a call without a deadline, one that is decided or one whose deadline has passed', () => {
    assert.equal(expiryText(call(), now), '')
    assert.equal(expiryText(call({ expiresAt: '2026-10-16T12:14:00Z', decision: 'approved', status: 'succeeded' }), now), '')
    assert.equal(expiryText(call({ expiresAt: '2026-10-16T11:59:00Z' }), now), '')
  })
})
