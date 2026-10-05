import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { kindDotClass } from './timeline.ts'

describe('kindDotClass', () => {
  it('colours the kinds of the activity log with the tokens', () => {
    assert.equal(kindDotClass('incident_opened'), 'bg-destructive')
    assert.equal(kindDotClass('incident_resolved'), 'bg-success')
    assert.equal(kindDotClass('incident_ignored'), 'bg-neutral')
    assert.equal(kindDotClass('incident_unignored'), 'bg-primary')
    assert.equal(kindDotClass('diagnosis_started'), 'bg-primary')
    assert.equal(kindDotClass('diagnosis_finished'), 'bg-info')
    assert.equal(kindDotClass('diagnosis_failed'), 'bg-destructive')
    assert.equal(kindDotClass('approval_requested'), 'bg-primary')
    assert.equal(kindDotClass('cluster_action'), 'bg-violet')
  })

  it('falls back to neutral for a kind it does not know', () => {
    assert.equal(kindDotClass('something_new'), 'bg-neutral')
  })
})
