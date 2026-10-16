import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import type { Incident } from './api.ts'
import { alsoLine, hostLine } from './incidents.ts'

const inc = (over: Partial<Incident> = {}): Incident => ({ id: 1, source: 'alertmanager', title: 'HighLoad', ...over }) as Incident

describe('hostLine', () => {
  it('is null without a host', () => {
    assert.equal(hostLine(inc()), null)
    assert.equal(hostLine(inc({ hostId: 3 })), null)
    assert.equal(hostLine(inc({ hostName: 'nas' })), null)
  })
  it('is the name and the id with one', () => {
    assert.deepEqual(hostLine(inc({ hostId: 3, hostName: 'nas' })), { name: 'nas', id: 3 })
  })
})

describe('alsoLine', () => {
  it('is empty when no other host matched', () => {
    assert.equal(alsoLine(inc()), '')
    assert.equal(alsoLine(inc({ alsoMatches: [] })), '')
  })
  it('lists the other hosts', () => {
    assert.equal(alsoLine(inc({ alsoMatches: ['a'] })), 'also matches: a')
    assert.equal(alsoLine(inc({ alsoMatches: ['a', 'b'] })), 'also matches: a, b')
  })
})
