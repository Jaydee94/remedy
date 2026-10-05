import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import type { Incident } from './api.ts'
import { defaultHeader, emptyShell, isGatewayStatus, navItems, nextShell, sectionOf } from './shell.ts'

describe('sectionOf', () => {
  it('maps every navigation path to its section', () => {
    assert.equal(sectionOf('/'), 'today')
    assert.equal(sectionOf('/incidents'), 'conversations')
    assert.equal(sectionOf('/approvals'), 'needs')
    assert.equal(sectionOf('/runs'), 'ask')
    assert.equal(sectionOf('/settings'), 'setup')
  })

  it('keeps a thread under Conversations and a run under Ask Remedy', () => {
    assert.equal(sectionOf('/incidents/27'), 'conversations')
    assert.equal(sectionOf('/runs/3f9c2ab'), 'ask')
  })

  it('does not match a path that only starts with the same letters', () => {
    assert.equal(sectionOf('/incidentsX'), null)
    assert.equal(sectionOf('/runsheet'), null)
    assert.equal(sectionOf('/nope'), null)
  })

  it('has five items, in the order of the navigation', () => {
    assert.deepEqual(navItems.map((n) => n.section), ['today', 'conversations', 'needs', 'ask', 'setup'])
    assert.deepEqual(navItems.map((n) => n.short), ['Today', 'Chats', 'Needs you', 'Ask', 'Setup'])
  })
})

describe('defaultHeader', () => {
  it('uses the label of the section', () => {
    assert.deepEqual(defaultHeader('/'), { title: 'Today' })
    assert.deepEqual(defaultHeader('/approvals'), { title: 'Needs you' })
    assert.deepEqual(defaultHeader('/settings'), { title: 'Setup' })
  })

  it('gives a thread and a run a back target', () => {
    assert.deepEqual(defaultHeader('/incidents/27'), { title: 'Conversation', back: '/incidents' })
    assert.deepEqual(defaultHeader('/runs/abc'), { title: 'Run', back: '/runs' })
  })

  it('falls back to the name of the app for an unknown path', () => {
    assert.deepEqual(defaultHeader('/nope'), { title: 'Remedy' })
  })
})

const incident = (id: number): Incident => ({ id }) as Incident
const ok = <T>(value: T): PromiseFulfilledResult<T> => ({ status: 'fulfilled', value })
const failed = (reason: unknown): PromiseRejectedResult => ({ status: 'rejected', reason })
class HttpError extends Error {}
const isHttpError = (e: unknown) => e instanceof HttpError

describe('nextShell', () => {
  it('starts not loaded, online, with nothing', () => {
    assert.deepEqual(emptyShell, { pending: 0, incidents: [], online: true, loaded: false })
  })

  it('takes both answers and is loaded', () => {
    const got = nextShell(emptyShell, ok([1, 2]), ok([incident(7)]), isHttpError)
    assert.deepEqual(got, { pending: 2, incidents: [incident(7)], online: true, loaded: true })
  })

  it('stays online when a route answers with an HTTP error (the route may not exist)', () => {
    const got = nextShell(emptyShell, failed(new HttpError('404')), ok([incident(7)]), isHttpError)
    assert.equal(got.online, true)
    assert.equal(got.pending, 0)
    assert.equal(got.loaded, true)
  })

  it('goes offline when the server cannot be reached, and keeps the last data', () => {
    const before = nextShell(emptyShell, ok([1, 2, 3]), ok([incident(1), incident(2)]), isHttpError)
    const got = nextShell(before, failed(new TypeError('fetch failed')), failed(new TypeError('fetch failed')), isHttpError)
    assert.equal(got.online, false)
    assert.equal(got.pending, 3)
    assert.deepEqual(got.incidents, [incident(1), incident(2)])
    assert.equal(got.loaded, true)
  })

  it('is not loaded while the incidents have never answered', () => {
    const got = nextShell(emptyShell, ok([]), failed(new TypeError('fetch failed')), isHttpError)
    assert.equal(got.loaded, false)
    assert.equal(got.online, false)
  })

  it('comes back online when the server answers again', () => {
    const down = nextShell(emptyShell, failed(new TypeError('x')), failed(new TypeError('x')), isHttpError)
    const up = nextShell(down, ok([1]), ok([]), isHttpError)
    assert.equal(up.online, true)
    assert.equal(up.pending, 1)
  })

  it('keeps the previous count when a route answers with a transient HTTP error', () => {
    const before = nextShell(emptyShell, ok([1, 2]), ok([]), isHttpError)
    const got = nextShell(before, failed(new HttpError('500')), ok([]), isHttpError)
    assert.equal(got.pending, 2)
    assert.equal(got.online, true)
  })

  it('keeps the incidents and the loaded flag when the incidents route answers with an HTTP error', () => {
    const before = nextShell(emptyShell, ok([1]), ok([incident(7)]), isHttpError)
    const got = nextShell(before, ok([1]), failed(new HttpError('500')), isHttpError)
    assert.deepEqual(got.incidents, [incident(7)])
    assert.equal(got.loaded, true)
    assert.equal(got.online, true)
  })
})

describe('isGatewayStatus', () => {
  it('is true for the statuses a reverse proxy answers when the server is down', () => {
    for (const status of [502, 503, 504]) assert.equal(isGatewayStatus(status), true)
  })

  it('is false for every other status', () => {
    for (const status of [200, 401, 404, 500]) assert.equal(isGatewayStatus(status), false)
  })
})
