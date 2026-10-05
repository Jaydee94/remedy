import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { kindDotClass } from './timeline.ts'

describe('kindDotClass', () => {
  const expected: Record<string, string> = {
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

  for (const [kind, cls] of Object.entries(expected)) {
    it(`colours ${kind} with ${cls}`, () => {
      assert.equal(kindDotClass(kind), cls)
    })
  }

  it('covers all 16 kinds', () => {
    assert.equal(Object.keys(expected).length, 16)
  })

  it('falls back to neutral for a kind it does not know', () => {
    assert.equal(kindDotClass('something_new'), 'bg-neutral')
  })
})
