import assert from 'node:assert/strict'
import { afterEach, beforeEach, describe, it } from 'node:test'
import { ApiError, api, runBody, setUnauthorizedHandler } from './api.ts'

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

describe('runBody', () => {
  it('keeps the old bodies byte for byte and adds hosts only when there are some', () => {
    assert.equal(JSON.stringify(runBody('q', true, false)), '{"prompt":"q","tools":true,"cluster":false}')
    assert.equal(JSON.stringify(runBody('q', true, true, 7)), '{"prompt":"q","tools":true,"cluster":true,"incidentId":7}')
    assert.equal(JSON.stringify(runBody('q', true, false, undefined, [2, 3])), '{"prompt":"q","tools":true,"cluster":false,"hosts":[2,3]}')
    assert.equal(JSON.stringify(runBody('q', true, false, undefined, [])), '{"prompt":"q","tools":true,"cluster":false}')
  })
})

describe('the skill calls', () => {
  afterEach(() => {
    globalThis.fetch = realFetch
  })

  it('builds the query of a list from the filter', async () => {
    const paths: string[] = []
    stubFetch((path) => {
      paths.push(path)
      return respond(200, [])
    })
    await api.listSkills()
    await api.listSkills({ host: 3 })
    await api.listSkills({ state: 'proposed' })
    await api.listSkills({ host: 3, state: 'active' })
    assert.deepEqual(paths, ['/api/skills', '/api/skills?host=3', '/api/skills?state=proposed', '/api/skills?host=3&state=active'])
  })

  it('confirms without a body and with an edit', async () => {
    const bodies: (string | undefined)[] = []
    globalThis.fetch = ((_: RequestInfo | URL, init?: RequestInit) => {
      bodies.push(init?.body as string | undefined)
      return Promise.resolve(respond(200, {}))
    }) as typeof fetch
    await api.confirmSkill(4)
    await api.confirmSkill(4, { description: 'd', body: 'b' })
    assert.deepEqual(bodies, [undefined, JSON.stringify({ description: 'd', body: 'b' })])
  })
})

describe('the runner login calls', () => {
  afterEach(() => {
    globalThis.fetch = realFetch
  })

  interface Seen {
    method?: string
    path: string
    body?: string
    csrf?: string
  }

  function record(res: () => Response): Seen[] {
    const seen: Seen[] = []
    globalThis.fetch = ((input: RequestInfo | URL, init?: RequestInit) => {
      seen.push({
        method: init?.method,
        path: String(input),
        body: init?.body as string | undefined,
        csrf: (init?.headers as Record<string, string> | undefined)?.['X-Remedy-CSRF'],
      })
      return Promise.resolve(res())
    }) as typeof fetch
    return seen
  }

  it('reads the offer', async () => {
    const seen = record(() => respond(200, { available: true, unavailable: '', session: null }))
    const login = await api.getRunnerLogin()
    assert.deepEqual(login, { available: true, unavailable: '', session: null })
    assert.deepEqual(seen, [{ method: 'GET', path: '/api/runner/login', body: undefined, csrf: '1' }])
  })

  it('starts with the replace flag in the body', async () => {
    const seen = record(() => respond(201, { id: 's', state: 'starting', attempt: 1, expiresAt: '2026-10-21T12:00:00Z', waitingRuns: 0 }))
    const s = await api.startRunnerLogin(true)
    assert.equal(s.state, 'starting')
    assert.deepEqual(
      seen.map((x) => [x.method, x.path, x.body]),
      [['POST', '/api/runner/login', '{"replace":true}']],
    )
  })

  it('sends the code to the code route and accepts an answer without a body', async () => {
    const seen = record(() => new Response(null, { status: 202 }))
    await api.sendRunnerLoginCode('sess-1', 'abcdefgh')
    assert.deepEqual(
      seen.map((x) => [x.method, x.path, x.body]),
      [['POST', '/api/runner/login/code', '{"session":"sess-1","code":"abcdefgh"}']],
    )
  })

  it('cancels with the session id in the body and not in the path', async () => {
    const seen = record(() => respond(204))
    await api.cancelRunnerLogin('sess-1')
    assert.deepEqual(
      seen.map((x) => [x.method, x.path, x.body]),
      [['POST', '/api/runner/login/cancel', '{"session":"sess-1"}']],
    )
  })

  it('exposes the status and the reason word of a refusal, and keeps the status of a 429', async () => {
    record(() => respond(409, { error: 'A run is running.', reason: 'run_active' }))
    await assert.rejects(api.startRunnerLogin(false), (err: unknown) => err instanceof ApiError && err.status === 409 && err.reason === 'run_active')
    record(() => respond(429, { error: 'too many attempts, try again later' }))
    await assert.rejects(api.startRunnerLogin(false), (err: unknown) => err instanceof ApiError && err.status === 429 && err.reason === undefined)
  })

  it('does not take a reason that is not a plain word', async () => {
    record(() => respond(409, { error: 'x', reason: { a: 1 } }))
    await assert.rejects(api.startRunnerLogin(false), (err: unknown) => err instanceof ApiError && err.reason === undefined)
  })
})
