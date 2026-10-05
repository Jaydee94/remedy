import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { phaseView, runPhase } from './status.ts'

describe('runPhase', () => {
  it('maps the statuses of a run to what Remedy says about it', () => {
    assert.equal(runPhase('queued', false), 'queued')
    assert.equal(runPhase('running', false), 'working')
    assert.equal(runPhase('succeeded', false), 'done')
    assert.equal(runPhase('failed', false), 'failed')
  })

  it('says waiting for a run that waits for an approval, and only for a running one', () => {
    assert.equal(runPhase('running', true), 'waiting')
    assert.equal(runPhase('succeeded', true), 'done')
    assert.equal(runPhase('failed', true), 'failed')
  })
})

describe('phaseView', () => {
  it('has a label and token classes for every phase', () => {
    assert.equal(phaseView.queued.label, 'Queued')
    assert.equal(phaseView.working.label, 'Working')
    assert.equal(phaseView.waiting.label, 'Waiting for you')
    assert.equal(phaseView.done.label, 'Done')
    assert.equal(phaseView.failed.label, 'Failed')
    for (const view of Object.values(phaseView)) {
      assert.match(view.dot, /^bg-/)
      assert.match(view.soft, /^bg-soft-/)
      assert.match(view.text, /^text-/)
    }
  })
})

describe('phaseView.queued', () => {
  it('uses the muted foreground for its text, which reads on the soft background', () => {
    assert.equal(phaseView.queued.text, 'text-muted-foreground')
  })
})
