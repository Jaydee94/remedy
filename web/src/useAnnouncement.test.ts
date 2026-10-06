import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { announcedAtStart, nextAnnounced } from './useAnnouncement.ts'
import type { Announced } from './useAnnouncement.ts'

const a = (key: string, text = `text ${key}`) => ({ key, text })

/** One render: the newest announcement, the whole history list, and whether the page is ready. */
type Step = { latest: { key: string; text: string } | null; all?: { key: string; text: string }[]; ready?: boolean }

const run = (steps: Step[]): Announced =>
  steps.reduce((state, s) => nextAnnounced(state, s.latest, s.all ?? (s.latest ? [s.latest] : []), s.ready ?? true), announcedAtStart)

describe('nextAnnounced', () => {
  it('takes nothing before the page is ready', () => {
    assert.equal(nextAnnounced(announcedAtStart, a('k1'), [a('k1')], false), announcedAtStart)
  })

  it('does not announce the history that is there when the page is ready', () => {
    const s = run([{ latest: a('k1') }])
    assert.equal(s.text, '')
    assert.ok(s.started)
    assert.ok(s.keys.has('k1'))
  })

  it('takes the history as seen when there is nothing to announce yet, so the first message after it is news', () => {
    assert.equal(run([{ latest: null }, { latest: a('k1') }]).text, 'text k1')
  })

  it('announces a key it has not seen', () => {
    assert.equal(run([{ latest: a('k1') }, { latest: a('k2') }]).text, 'text k2')
  })

  it('does not announce a key again', () => {
    const s = run([{ latest: a('k1') }, { latest: a('k2') }, { latest: a('k2') }])
    assert.equal(s.text, 'text k2')
    assert.equal(nextAnnounced(s, a('k2'), [a('k2')], true), s)
  })

  it('does not change the text when the thread falls back to an older key', () => {
    assert.equal(run([{ latest: a('k1') }, { latest: a('k2') }, { latest: a('k1') }]).text, 'text k2')
  })

  it('changes nothing for null, and keeps the text', () => {
    const s = run([{ latest: a('k1') }, { latest: a('k2') }, { latest: null }])
    assert.equal(s.text, 'text k2')
    assert.equal(nextAnnounced(s, null, [], true), s)
  })

  it('announces a new key after a fallback', () => {
    assert.equal(run([{ latest: a('k1') }, { latest: a('k2') }, { latest: a('k1') }, { latest: a('k3') }]).text, 'text k3')
  })

  it('treats every message of the history as seen: the old one does not come back when the newer one is decided', () => {
    const dia = a('diagnosis')
    const ask = a('ask')
    const loaded = run([{ latest: ask, all: [dia, ask] }])
    assert.equal(loaded.text, '')
    // The ask is decided and leaves: the diagnosis is the newest again, but it was in the history.
    const decided = nextAnnounced(loaded, dia, [dia], true)
    assert.equal(decided.text, '')
    assert.equal(decided, loaded)
  })

  it('still announces a genuinely new message after the history fell back', () => {
    const dia = a('diagnosis')
    const ask = a('ask')
    const s = run([
      { latest: ask, all: [dia, ask] },
      { latest: dia, all: [dia] },
      { latest: a('answer'), all: [dia, a('answer')] },
    ])
    assert.equal(s.text, 'text answer')
  })
})
