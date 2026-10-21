import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import type { RunnerLogin, RunnerLoginSession } from './api.ts'
import { reasonSentence, refusalSentence } from './loginrelay.ts'
import { stepOf, wizardView } from './loginwizard.ts'
import type { WizardView } from './loginwizard.ts'

const validUrl =
  'https://claude.com/cai/oauth/authorize?code=true&client_id=c-1&response_type=code&redirect_uri=r-1&scope=s-1&code_challenge=ch-1&code_challenge_method=S256&state=st-1'

const now = Date.parse('2026-10-21T12:00:00Z')
const inMs = (ms: number) => new Date(now + ms).toISOString()

const session = (over: Partial<RunnerLoginSession> = {}): RunnerLoginSession => ({
  id: 'sess-1',
  state: 'starting',
  attempt: 1,
  expiresAt: inMs(10 * 60 * 1000),
  waitingRuns: 0,
  ...over,
})
const offered = (s: RunnerLoginSession | null = null, over: Partial<RunnerLogin> = {}): RunnerLogin => ({ available: true, session: s, ...over })

const view = (s: RunnerLoginSession | null, over: { open?: boolean; typing?: boolean; loggedIn?: boolean } = {}): WizardView =>
  wizardView({ offered: offered(s), open: over.open ?? true, typing: over.typing ?? false, now, loggedIn: over.loggedIn ?? false })

const action = (v: WizardView, id: string) => v.actions.find((a) => a.id === id)
const ids = (v: WizardView) => v.actions.map((a) => a.id)

describe('stepOf', () => {
  const table: [string, boolean, number | null][] = [
    ['idle', false, 1],
    ['idle', true, 1],
    ['starting', false, 1],
    ['starting', true, 1],
    ['awaiting_code', false, 2],
    ['awaiting_code', true, 3],
    ['submitting', false, 3],
    ['submitting', true, 3],
    ['succeeded', false, 4],
    ['failed', true, 4],
    ['cancelled', false, 4],
    ['expired', false, 4],
    ['something_new', false, null],
    ['', false, null],
  ]
  for (const [state, typing, step] of table) {
    it(`${state} with typing ${typing} is step ${step}`, () => {
      assert.equal(stepOf(state, typing), step)
    })
  }

  it('lands a reload on step 3 on step 2: typing is client state and false after a reload', () => {
    assert.equal(stepOf('awaiting_code', true), 3)
    assert.equal(stepOf('awaiting_code', false), 2)
    const reloaded = view(session({ state: 'awaiting_code', url: validUrl }), { typing: false })
    assert.equal(reloaded.step, 2)
    assert.equal(reloaded.indicator, 'Step 2 of 4')
  })
})

describe('the view while the relay is not offered', () => {
  it('shows only the terminal when the relay is off', () => {
    const v = wizardView({ offered: offered(null, { available: false, unavailable: 'api_key_mode' }), open: true, typing: false, now, loggedIn: false })
    assert.equal(v.step, null)
    assert.equal(v.indicator, '')
    assert.deepEqual(v.actions, [])
    assert.equal(v.showTerminal, true)
    assert.ok(v.paragraphs.includes(refusalSentence('api_key_mode')))
    assert.equal(v.link, null)
    assert.equal(v.pollMs, 10000)
  })

  it('shows only the terminal when the page has no answer (an older control plane)', () => {
    const v = wizardView({ offered: null, open: true, typing: true, now, loggedIn: false })
    assert.equal(v.step, null)
    assert.deepEqual(v.actions, [])
    assert.equal(v.showTerminal, true)
    assert.ok(v.paragraphs.length > 0)
  })

  it('does not show a session of a relay that is not available', () => {
    const v = wizardView({
      offered: { available: false, unavailable: 'disabled', session: session({ state: 'awaiting_code', url: validUrl }) },
      open: true,
      typing: false,
      now,
      loggedIn: false,
    })
    assert.equal(v.link, null)
    assert.deepEqual(v.actions, [])
  })
})

describe('the closed wizard', () => {
  it('offers to log in, and to log in again when the agent is logged in', () => {
    const a = view(null, { open: false })
    assert.equal(a.step, null)
    assert.deepEqual(a.actions, [{ id: 'open', label: 'Log in from here', enabled: true }])
    assert.equal(a.showTerminal, true)
    assert.equal(a.pollMs, 10000)
    const b = view(null, { open: false, loggedIn: true })
    assert.deepEqual(b.actions, [{ id: 'open', label: 'Log in again', enabled: true }])
  })

  it('opens by itself for a session that is still open: a reload or a second tab shows the same step', () => {
    const v = view(session({ state: 'awaiting_code', url: validUrl }), { open: false })
    assert.equal(v.step, 2)
    assert.ok(v.link)
  })

  it('does not bring a finished session back when the wizard was closed', () => {
    const v = view(session({ state: 'succeeded' }), { open: false })
    assert.equal(v.step, null)
    assert.equal(action(v, 'open')?.label, 'Log in from here')
  })
})

describe('step 1', () => {
  it('explains what will happen before the start, in two sentences, and offers the start', () => {
    const v = view(null)
    assert.equal(v.step, 1)
    assert.equal(v.indicator, 'Step 1 of 4')
    assert.equal(v.stepName, 'Start')
    assert.ok(v.heading.length > 0)
    assert.match(v.paragraphs.join(' '), /code/)
    assert.match(v.paragraphs.join(' '), /not store/)
    assert.deepEqual(v.actions, [
      { id: 'start', label: 'Start sign-in', enabled: true },
      { id: 'cancel', label: 'Cancel', enabled: true },
    ])
    assert.equal(v.countdown, null)
    assert.equal(v.link, null)
    assert.equal(v.showTerminal, true)
    assert.equal(v.pollMs, 10000)
  })

  it('says that the login is replaced when the agent is logged in already', () => {
    assert.match(view(null, { loggedIn: true }).paragraphs.join(' '), /replace/)
    assert.doesNotMatch(view(null).paragraphs.join(' '), /replace/)
  })

  it('waits while the sign-in starts, with the start disabled and the cancel on', () => {
    const v = view(session({ state: 'starting' }))
    assert.equal(v.step, 1)
    assert.equal(action(v, 'start')?.enabled, false)
    assert.equal(action(v, 'start')?.label, 'Start sign-in')
    assert.equal(action(v, 'cancel')?.enabled, true)
    assert.equal(v.pollMs, 2000)
    assert.equal(v.showTerminal, false)
  })
})

describe('step 2', () => {
  const awaiting = (over: Partial<RunnerLoginSession> = {}) => session({ state: 'awaiting_code', url: validUrl, ...over })

  it('shows the host of the link as text, and the link for the page', () => {
    const v = view(awaiting())
    assert.equal(v.step, 2)
    assert.equal(v.stepName, 'Open the sign-in page')
    assert.deepEqual(v.link, { host: 'claude.com', href: validUrl })
    assert.deepEqual(v.actions, [
      { id: 'open', label: 'Open the sign-in page', enabled: true },
      { id: 'cancel', label: 'Cancel', enabled: true },
    ])
    assert.equal(v.countdown, '10 min 0 s left')
    assert.equal(v.pollMs, 2000)
    assert.equal(v.showTerminal, false)
  })

  it('does not show an address it does not recognise, and offers the terminal', () => {
    for (const url of ['https://claude.com.evil.test/cai/oauth/authorize?state=1', 'http://claude.com/x', '', undefined]) {
      const v = view(awaiting({ url }))
      assert.equal(v.link, null, String(url))
      assert.ok(v.paragraphs.includes('I got a sign-in address I do not recognise, so I did not show it'), String(url))
      assert.equal(v.showTerminal, true)
      assert.equal(action(v, 'open')?.enabled, false)
      assert.equal(action(v, 'cancel')?.enabled, true)
      assert.ok(!JSON.stringify(v).includes('evil.test'))
    }
  })

  it('never has the link outside awaiting_code', () => {
    for (const state of ['starting', 'submitting', 'succeeded', 'failed', 'cancelled', 'expired']) {
      const v = view(session({ state, url: validUrl }))
      assert.equal(v.link, null, state)
      assert.ok(!JSON.stringify(v).includes('code_challenge'), state)
    }
    assert.equal(view(session({ state: 'awaiting_code', url: validUrl }), { typing: true }).link, null)
  })

  it('says how many runs wait for the sign-in', () => {
    assert.ok(view(awaiting({ waitingRuns: 2 })).paragraphs.includes('2 runs wait for this'))
    assert.ok(view(awaiting({ waitingRuns: 1 })).paragraphs.includes('1 run waits for this'))
    assert.ok(!view(awaiting({ waitingRuns: 0 })).paragraphs.join(' ').includes('wait for this'))
    assert.ok(view(session({ state: 'starting', waitingRuns: 3 })).paragraphs.includes('3 runs wait for this'))
  })

  it('counts down from the expiry and clamps at zero', () => {
    assert.equal(view(awaiting({ expiresAt: inMs(9 * 60 * 1000 + 30 * 1000) })).countdown, '9 min 30 s left')
    assert.equal(view(awaiting({ expiresAt: inMs(45 * 1000) })).countdown, '45 s left')
    assert.equal(view(awaiting({ expiresAt: inMs(0) })).countdown, '0 s left')
    assert.equal(view(awaiting({ expiresAt: inMs(-5000) })).countdown, '0 s left')
    assert.equal(view(awaiting({ expiresAt: 'not a time' })).countdown, null)
  })
})

describe('step 3', () => {
  it('offers to send the code, to go back to the link and to cancel while the code is typed', () => {
    const v = view(session({ state: 'awaiting_code', url: validUrl }), { typing: true })
    assert.equal(v.step, 3)
    assert.equal(v.indicator, 'Step 3 of 4')
    assert.equal(v.stepName, 'Paste the code')
    assert.deepEqual(v.actions, [
      { id: 'send', label: 'Send code', enabled: true },
      { id: 'back', label: 'Back to the link', enabled: true },
      { id: 'cancel', label: 'Cancel', enabled: true },
    ])
    assert.equal(v.countdown, '10 min 0 s left')
    assert.match(v.paragraphs.join(' '), /not store/)
  })

  it('disables the send while the code is being checked, and the way back', () => {
    const v = view(session({ state: 'submitting' }))
    assert.equal(v.step, 3)
    assert.equal(action(v, 'send')?.enabled, false)
    assert.equal(action(v, 'send')?.label, 'Send code')
    assert.equal(action(v, 'back')?.enabled, false)
    assert.equal(action(v, 'cancel')?.enabled, true)
    assert.equal(v.pollMs, 2000)
    assert.match(v.paragraphs.join(' '), /wait/)
  })
})

describe('step 4', () => {
  it('is glad when the agent is logged in', () => {
    const v = view(session({ state: 'succeeded' }))
    assert.equal(v.step, 4)
    assert.equal(v.indicator, 'Step 4 of 4')
    assert.equal(v.stepName, 'Result')
    assert.equal(v.heading, 'The agent is logged in')
    assert.deepEqual(ids(v), ['done'])
    assert.equal(action(v, 'done')?.label, 'Done')
    assert.equal(v.countdown, null)
    assert.equal(v.pollMs, 10000)
    assert.equal(v.showTerminal, false)
  })

  it('says one fixed sentence per reason of a failure, and offers a new try', () => {
    for (const reason of ['no_url', 'bad_url', 'process_exited', 'not_confirmed', 'timeout', 'runner_lost', 'runner_restarted', 'run_active']) {
      const v = view(session({ state: 'failed', reason }))
      assert.equal(v.step, 4, reason)
      assert.ok(v.paragraphs.includes(reasonSentence(reason)), reason)
      assert.deepEqual(v.actions, [
        { id: 'done', label: 'Done', enabled: true },
        { id: 'again', label: 'Try again', enabled: true },
      ])
      assert.equal(v.showTerminal, true)
      assert.equal(v.pollMs, 10000)
    }
  })

  it('shows the generic sentence for a reason it does not know, and not the word', () => {
    const v = view(session({ state: 'failed', reason: '<script>alert(1)</script>' }))
    assert.ok(v.paragraphs.includes(reasonSentence(undefined)))
    assert.ok(!JSON.stringify(v).includes('script'))
    assert.ok(view(session({ state: 'failed' })).paragraphs.includes(reasonSentence(undefined)))
  })

  it('says that the sign-in was cancelled or ran out of time', () => {
    assert.ok(view(session({ state: 'cancelled' })).paragraphs.includes(reasonSentence('cancelled')))
    assert.ok(view(session({ state: 'expired' })).paragraphs.includes(reasonSentence('expired')))
    assert.deepEqual(ids(view(session({ state: 'cancelled' }))), ['done', 'again'])
    assert.deepEqual(ids(view(session({ state: 'expired' }))), ['done', 'again'])
  })
})

describe('a code that was not accepted', () => {
  it('says so on the fresh sign-in the server started, and is on step 1 or 2 as the state says', () => {
    const starting = view(session({ state: 'starting', attempt: 2 }))
    assert.equal(starting.step, 1)
    assert.match(starting.paragraphs.join(' '), /not accepted/)
    assert.match(starting.paragraphs.join(' '), /2 of 3/)
    const awaiting = view(session({ state: 'awaiting_code', attempt: 3, url: validUrl }))
    assert.equal(awaiting.step, 2)
    assert.match(awaiting.paragraphs.join(' '), /not accepted/)
    assert.ok(awaiting.link)
    assert.doesNotMatch(view(session({ state: 'awaiting_code', attempt: 1, url: validUrl })).paragraphs.join(' '), /not accepted/)
  })

  it('offers "I started a fresh sign-in" on step 2 for a failed invalid code below the last attempt, and back is enabled', () => {
    const v = view(session({ state: 'failed', reason: 'invalid_code', attempt: 2 }))
    assert.equal(v.step, 2)
    assert.equal(v.link, null)
    assert.deepEqual(action(v, 'back'), { id: 'back', label: 'I started a fresh sign-in', enabled: true })
    assert.match(v.paragraphs.join(' '), /not accepted/)
  })

  it('ends on step 4 after the last attempt', () => {
    const v = view(session({ state: 'failed', reason: 'invalid_code', attempt: 3 }))
    assert.equal(v.step, 4)
    assert.ok(v.paragraphs.includes(reasonSentence('invalid_code')))
    assert.deepEqual(ids(v), ['done', 'again'])
  })
})

describe('the announcement', () => {
  it('is the same for the same input and changes with the state and the attempt', () => {
    const a = view(session({ state: 'awaiting_code', url: validUrl }))
    const b = view(session({ state: 'awaiting_code', url: validUrl }))
    assert.deepEqual(a.announcement, b.announcement)
    assert.ok(a.announcement && a.announcement.key !== '' && a.announcement.text !== '')
    const keys = new Set([
      a.announcement.key,
      view(session({ state: 'starting' })).announcement?.key,
      view(session({ state: 'starting', attempt: 2 })).announcement?.key,
      view(session({ state: 'submitting' })).announcement?.key,
      view(session({ state: 'succeeded' })).announcement?.key,
      view(session({ state: 'failed', reason: 'timeout' })).announcement?.key,
      view(session({ state: 'failed', reason: 'no_url' })).announcement?.key,
      view(session({ state: 'cancelled' })).announcement?.key,
    ])
    assert.equal(keys.size, 8)
  })

  it('does not change with the clock', () => {
    const s = session({ state: 'awaiting_code', url: validUrl })
    const later = wizardView({ offered: offered(s), open: true, typing: false, now: now + 60000, loggedIn: false })
    assert.deepEqual(later.announcement, view(s).announcement)
    assert.notEqual(later.countdown, view(s).countdown)
  })

  it('does not carry the link, a reason word or an id', () => {
    const v = view(session({ id: 'secret-id-123', state: 'failed', reason: 'timeout' }))
    assert.ok(!JSON.stringify(v.announcement).includes('secret-id-123'))
    const w = view(session({ state: 'awaiting_code', url: validUrl }))
    assert.ok(!JSON.stringify(w.announcement).includes('claude.com'))
  })
})

describe('the poll interval', () => {
  it('is 2 s with a session that is not finished, whether the wizard is open or not', () => {
    for (const state of ['starting', 'awaiting_code', 'submitting']) {
      assert.equal(view(session({ state, url: validUrl }), { open: false }).pollMs, 2000, state)
      assert.equal(view(session({ state, url: validUrl })).pollMs, 2000, state)
    }
  })

  it('is 10 s otherwise', () => {
    assert.equal(view(null).pollMs, 10000)
    for (const state of ['succeeded', 'failed', 'cancelled', 'expired']) assert.equal(view(session({ state })).pollMs, 10000, state)
  })
})

describe('the code', () => {
  it('has no way into the view: the function never receives it', () => {
    assert.equal(wizardView.length, 1)
    const v = view(session({ state: 'submitting' }), { typing: true })
    assert.ok(!/code=|[A-Za-z0-9]{20,}/.test(JSON.stringify(v)))
  })
})
