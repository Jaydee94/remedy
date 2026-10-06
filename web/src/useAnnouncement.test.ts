import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { announcedAtStart, nextAnnounced } from './useAnnouncement.ts'
import type { Announced } from './useAnnouncement.ts'

const a = (key: string, text = `text ${key}`) => ({ key, text })

/** One render: the whole history list (oldest first) and whether the page is ready. */
type Step = { all: { key: string; text: string }[]; ready?: boolean }

const run = (steps: Step[]): Announced => steps.reduce((state, s) => nextAnnounced(state, s.all, s.ready ?? true), announcedAtStart)

describe('nextAnnounced', () => {
  it('takes nothing before the page is ready', () => {
    assert.equal(nextAnnounced(announcedAtStart, [a('k1')], false), announcedAtStart)
  })

  it('does not announce the history that is there when the page is ready', () => {
    const s = run([{ all: [a('k1')] }])
    assert.equal(s.announcement, null)
    assert.ok(s.started)
    assert.ok(s.keys.has('k1'))
  })

  it('takes the history as seen when it is empty, so the first message after it is news', () => {
    assert.deepEqual(run([{ all: [] }, { all: [a('k1')] }]).announcement, a('k1'))
  })

  it('announces a key it has not seen', () => {
    assert.deepEqual(run([{ all: [a('k1')] }, { all: [a('k1'), a('k2')] }]).announcement, a('k2'))
  })

  it('does not announce a key again', () => {
    const s = run([{ all: [a('k1')] }, { all: [a('k1'), a('k2')] }, { all: [a('k1'), a('k2')] }])
    assert.deepEqual(s.announcement, a('k2'))
    assert.equal(nextAnnounced(s, [a('k1'), a('k2')], true), s)
  })

  it('does not change the announcement when the thread falls back to an older key', () => {
    const s = run([{ all: [a('k1')] }, { all: [a('k1'), a('k2')] }, { all: [a('k1')] }])
    assert.deepEqual(s.announcement, a('k2'))
  })

  it('changes nothing for an empty history, and keeps the announcement', () => {
    const s = run([{ all: [a('k1')] }, { all: [a('k1'), a('k2')] }])
    assert.equal(nextAnnounced(s, [], true), s)
  })

  it('announces a new key after a fallback', () => {
    const s = run([{ all: [a('k1')] }, { all: [a('k1'), a('k2')] }, { all: [a('k1')] }, { all: [a('k1'), a('k3')] }])
    assert.deepEqual(s.announcement, a('k3'))
  })

  it('announces the newest of two unseen keys in one step and marks both as seen', () => {
    const s = run([{ all: [a('k1')] }, { all: [a('k1'), a('k2'), a('k3')] }])
    assert.deepEqual(s.announcement, a('k3'))
    assert.ok(s.keys.has('k2') && s.keys.has('k3'))
    // k2 is never announced afterwards, not even when k3 is dealt with.
    assert.equal(nextAnnounced(s, [a('k1'), a('k2')], true), s)
  })

  it('announces a new key that is not the last of the history (an older message arriving late)', () => {
    const s = run([{ all: [a('k1'), a('k3')] }, { all: [a('k1'), a('k2'), a('k3')] }])
    assert.deepEqual(s.announcement, a('k2'))
  })

  it('treats every message of the history as seen: the old one does not come back when the newer one is decided', () => {
    const dia = a('diagnosis')
    const ask = a('ask')
    const loaded = run([{ all: [dia, ask] }])
    assert.equal(loaded.announcement, null)
    // The ask is decided and leaves: the diagnosis is the newest again, but it was in the history.
    assert.equal(nextAnnounced(loaded, [dia], true), loaded)
  })

  it('still announces a genuinely new message after the history fell back', () => {
    const dia = a('diagnosis')
    const ask = a('ask')
    const s = run([{ all: [dia, ask] }, { all: [dia] }, { all: [dia, a('answer')] }])
    assert.deepEqual(s.announcement, a('answer'))
  })

  it('announces a message with the same text but another key', () => {
    const s = run([{ all: [a('c1|x', 'Remedy asks you: May I?')] }, { all: [a('c2|x', 'Remedy asks you: May I?')] }])
    assert.deepEqual(s.announcement, a('c2|x', 'Remedy asks you: May I?'))
  })
})
