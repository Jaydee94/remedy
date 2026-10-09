import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import type { RunnerStatus } from './api.ts'
import { hideOnError, loginCommand, loginHint, runnerView } from './runnerstatus.ts'

const now = Date.parse('2026-10-07T12:00:00Z')
const status = (over: Partial<RunnerStatus> = {}): RunnerStatus => ({ connected: true, login: 'ok', lastSeenAt: '2026-10-07T11:59:55Z', ...over })

describe('runnerView', () => {
  it('says so when it has never heard from the runner', () => {
    const v = runnerView({ connected: false, login: 'unknown' }, now)
    assert.equal(v.connection.label, 'Never seen')
    assert.equal(v.login.label, 'Login unknown')
    assert.equal(v.sentence, 'I have not heard from my runner yet.')
    assert.match(v.hint ?? '', /remedy-runner-0/)
    assert.equal(v.detail, null)
    assert.equal(v.command, null)
  })

  it('is glad when the runner is connected and logged in', () => {
    const v = runnerView(status(), now)
    assert.equal(v.connection.label, 'Connected')
    assert.equal(v.login.label, 'Logged in')
    assert.equal(v.sentence, 'My runner is connected and logged in, so I can run an agent.')
    assert.equal(v.hint, null)
    assert.equal(v.detail, null)
    assert.equal(v.command, null)
  })

  it('says the agent cannot run when the runner is connected but not logged in, and how to log in', () => {
    const v = runnerView(status({ login: 'missing' }), now)
    assert.equal(v.login.label, 'Not logged in')
    assert.match(v.login.dot, /destructive/)
    assert.equal(v.sentence, 'My runner is connected, but the agent is not logged in, so I cannot run one.')
    assert.equal(v.hint, loginHint)
    assert.match(loginHint, /\/login/)
    assert.equal(v.command, loginCommand)
    assert.equal(loginCommand, 'kubectl exec -it remedy-runner-0 -c runner -- /opt/claude/claude')
  })

  it('does not claim to know the login when the runner has not said', () => {
    const v = runnerView(status({ login: 'unknown' }), now)
    assert.equal(v.login.label, 'Login unknown')
    assert.equal(v.sentence, 'My runner is connected. I do not know yet whether the agent is logged in.')
    assert.equal(v.hint, null)
    assert.equal(v.command, null)
  })

  it('says how long it has been silent when the runner is not connected', () => {
    const v = runnerView(status({ connected: false, login: 'unknown', lastSeenAt: '2026-10-07T11:57:00Z' }), now)
    assert.equal(v.connection.label, 'Not connected')
    assert.match(v.connection.dot, /destructive/)
    assert.equal(v.sentence, 'My runner is not connected.')
    assert.equal(v.detail, 'I last heard from it 3 minutes ago.')
    assert.equal(v.login.label, 'Login unknown')
    assert.equal(v.command, null)
  })

  it('keeps the live sentence free of the elapsed time, so a clock tick changes nothing it says', () => {
    const at = (t: number) => runnerView(status({ connected: false, login: 'unknown', lastSeenAt: '2026-10-07T11:57:00Z' }), t)
    assert.equal(at(now).sentence, at(now + 30_000).sentence)
    assert.notEqual(at(now).detail, at(now + 60_000).detail)
  })

  it('points a runner that was seen and is gone to its pod, like one never seen', () => {
    const v = runnerView(status({ connected: false, login: 'unknown', lastSeenAt: '2026-10-07T11:57:00Z' }), now)
    assert.equal(v.hint, 'Check that the runner is running: in Kubernetes the pod is remedy-runner-0.')
  })

  it('never says "logged in" for a runner that is not connected, even if the status says ok', () => {
    const v = runnerView(status({ connected: false, login: 'ok', lastSeenAt: '2026-10-07T11:00:00Z' }), now)
    assert.equal(v.login.label, 'Login unknown')
    assert.ok(!/logged in/i.test(v.sentence), v.sentence)
    assert.ok(!/logged in/i.test(v.detail ?? ''), v.detail ?? '')
    assert.equal(v.command, null)
  })

  it('does not go negative for a clock that is a little behind', () => {
    const v = runnerView(status({ connected: false, login: 'unknown', lastSeenAt: '2026-10-07T12:00:30Z' }), now)
    assert.equal(v.sentence, 'My runner is not connected.')
    assert.equal(v.detail, 'I last heard from it 0 seconds ago.')
  })

  it('shows the CLI version only while the runner is connected', () => {
    assert.equal(runnerView(status({ cliVersion: '2.1.288 (Claude Code)' }), now).version, '2.1.288 (Claude Code)')
    assert.equal(runnerView(status(), now).version, null)
    const gone = runnerView(status({ connected: false, login: 'ok', cliVersion: '2.1.288 (Claude Code)' }), now)
    assert.equal(gone.version, null)
  })
})

describe('hideOnError', () => {
  it('hides the section for a control plane without the route, and only for it', () => {
    assert.equal(hideOnError(404), true)
    for (const code of [400, 401, 403, 500, 502, 503]) assert.equal(hideOnError(code), false, String(code))
  })
})
