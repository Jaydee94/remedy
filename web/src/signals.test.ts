import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import type { SignalSource } from './api.ts'
import { signalLine } from './signals.ts'

const at = Date.parse('2026-10-16T12:00:00Z')
const src = (over: Partial<SignalSource> = {}): SignalSource => ({ name: 'alertmanager', state: 'ok', checkedAt: '2026-10-16T11:59:48Z', active: 7, ...over })

describe('signalLine', () => {
  it('says that no address is set when the source is off', () => {
    assert.equal(signalLine(src({ state: 'off', checkedAt: undefined, active: 0 }), at), "I don't ask Alertmanager: no address is set.")
  })
  it('says that it has not asked yet', () => {
    assert.equal(signalLine(src({ state: 'pending', checkedAt: undefined }), at), "I haven't asked Alertmanager yet.")
  })
  it('says how long ago it asked and how many alerts it listed', () => {
    assert.equal(signalLine(src(), at), 'I asked Alertmanager 12 seconds ago and it listed 7 active alerts.')
    assert.equal(signalLine(src({ active: 1 }), at), 'I asked Alertmanager 12 seconds ago and it listed 1 active alert.')
    assert.equal(signalLine(src({ active: 0 }), at), 'I asked Alertmanager 12 seconds ago and it listed no active alerts.')
  })
  it('shows the error, and does not call the answer current', () => {
    assert.equal(
      signalLine(src({ state: 'error', error: 'alertmanager: HTTP 401' }), at),
      "I couldn't ask Alertmanager 12 seconds ago: alertmanager: HTTP 401. I changed nothing because of it.",
    )
  })
  it('words an age of a minute or more in minutes and hours, and a recent one in seconds', () => {
    const line = (secondsAgo: number) => signalLine(src({ checkedAt: new Date(at - secondsAgo * 1000).toISOString() }), at)
    assert.equal(line(2883), 'I asked Alertmanager 48 min ago and it listed 7 active alerts.')
    assert.equal(line(7200), 'I asked Alertmanager 2 h ago and it listed 7 active alerts.')
    assert.equal(line(12), 'I asked Alertmanager 12 seconds ago and it listed 7 active alerts.')
    assert.equal(line(0), 'I asked Alertmanager just now and it listed 7 active alerts.')
    assert.equal(line(59), 'I asked Alertmanager 59 seconds ago and it listed 7 active alerts.')
    assert.equal(line(60), 'I asked Alertmanager 1 min ago and it listed 7 active alerts.')
    assert.equal(line(1), 'I asked Alertmanager 1 second ago and it listed 7 active alerts.')
  })
  it('copes with a missing or future time', () => {
    assert.equal(signalLine(src({ checkedAt: undefined }), at), 'I asked Alertmanager and it listed 7 active alerts.')
    assert.equal(signalLine(src({ checkedAt: '2026-10-16T12:05:00Z' }), at), 'I asked Alertmanager just now and it listed 7 active alerts.')
  })
})
