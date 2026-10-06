import assert from 'node:assert/strict'
import { afterEach, beforeEach, describe, it } from 'node:test'
import { ApiError, api, setUnauthorizedHandler } from './api.ts'

const realFetch = globalThis.fetch

function respond(status: number, body: unknown = {}): Response {
  return new Response(status === 204 ? null : JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function stubFetch(fn: (path: string) => Response | Promise<Response>) {
  globalThis.fetch = ((input: RequestInfo | URL) => Promise.resolve(fn(String(input)))) as typeof fetch
}

describe('request and the unauthorized handler', () => {
  let calls = 0
  beforeEach(() => {
    calls = 0
    setUnauthorizedHandler(() => {
      calls++
    })
  })
  afterEach(() => {
    globalThis.fetch = realFetch
    setUnauthorizedHandler(null)
  })

  it('calls the handler once for a 401 and still rejects with the status', async () => {
    stubFetch(() => respond(401, { error: 'unauthorized' }))
    await assert.rejects(api.listRuns(), (err: unknown) => err instanceof ApiError && err.status === 401 && err.message === 'unauthorized')
    assert.equal(calls, 1)
  })

  it('does not call it for a 401 of the login, of /api/me or of the logout', async () => {
    stubFetch(() => respond(401, { error: 'unauthorized' }))
    await assert.rejects(api.login('x'), (err: unknown) => err instanceof ApiError && err.status === 401)
    await assert.rejects(api.me(), (err: unknown) => err instanceof ApiError && err.status === 401)
    await assert.rejects(api.logout(), (err: unknown) => err instanceof ApiError && err.status === 401)
    assert.equal(calls, 0)
  })

  it('does not call it for a 200 or for another error', async () => {
    stubFetch(() => respond(200, []))
    await api.listRuns()
    stubFetch(() => respond(500, { error: 'boom' }))
    await assert.rejects(api.listRuns(), (err: unknown) => err instanceof ApiError && err.status === 500)
    assert.equal(calls, 0)
  })

  it('calls nothing after the handler was removed', async () => {
    setUnauthorizedHandler(null)
    stubFetch(() => respond(401))
    await assert.rejects(api.listRuns(), ApiError)
    assert.equal(calls, 0)
  })

  it('ignores the 401 of a request that began under an earlier handler', async () => {
    let release: (r: Response) => void = () => {}
    globalThis.fetch = (() => new Promise<Response>((resolve) => (release = resolve))) as typeof fetch
    const pending = api.listRuns()
    let newer = 0
    setUnauthorizedHandler(() => {
      newer++
    })
    release(respond(401, { error: 'unauthorized' }))
    await assert.rejects(pending, (err: unknown) => err instanceof ApiError && err.status === 401)
    assert.equal(calls, 0)
    assert.equal(newer, 0)
  })

  it('ignores the 401 of a request that began while signed out', async () => {
    setUnauthorizedHandler(null)
    let release: (r: Response) => void = () => {}
    globalThis.fetch = (() => new Promise<Response>((resolve) => (release = resolve))) as typeof fetch
    const pending = api.listRuns()
    let signedIn = 0
    setUnauthorizedHandler(() => {
      signedIn++
    })
    release(respond(401))
    await assert.rejects(pending, ApiError)
    assert.equal(signedIn, 0)
  })

  it('reports each of several parallel 401s of the same session', async () => {
    stubFetch(() => respond(401))
    await Promise.allSettled([api.listRuns(), api.listApprovals('pending')])
    assert.equal(calls, 2)
  })
})
