import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { announcedAtStart, nextAnnounced } from './useAnnouncement.ts'
import type { Announced } from './useAnnouncement.ts'

const a = (key: string, text = `text ${key}`) => ({ key, text })

const run = (steps: ({ key: string; text: string } | null)[]): Announced =>
  steps.reduce((state, s) => nextAnnounced(state, s, true), announcedAtStart)

describe('nextAnnounced', () => {
  it('takes nothing before the page is ready', () => {
    assert.equal(nextAnnounced(announcedAtStart, a('k1'), false), announcedAtStart)
  })

  it('does not announce the history that is there when the page is ready', () => {
    const s = run([a('k1')])
    assert.equal(s.text, '')
    assert.ok(s.started)
    assert.ok(s.keys.has('k1'))
  })

  it('takes the history as seen when there is nothing to announce yet, so the first message after it is news', () => {
    assert.equal(run([null, a('k1')]).text, 'text k1')
  })

  it('announces a key it has not seen', () => {
    assert.equal(run([a('k1'), a('k2')]).text, 'text k2')
  })

  it('does not announce a key again', () => {
    const s = run([a('k1'), a('k2'), a('k2')])
    assert.equal(s.text, 'text k2')
    assert.equal(nextAnnounced(s, a('k2'), true), s)
  })

  it('does not change the text when the thread falls back to an older key', () => {
    assert.equal(run([a('k1'), a('k2'), a('k1')]).text, 'text k2')
  })

  it('changes nothing for null, and keeps the text', () => {
    const s = run([a('k1'), a('k2'), null])
    assert.equal(s.text, 'text k2')
    assert.equal(nextAnnounced(s, null, true), s)
  })

  it('announces a new key after a fallback', () => {
    assert.equal(run([a('k1'), a('k2'), a('k1'), a('k3')]).text, 'text k3')
  })
})
