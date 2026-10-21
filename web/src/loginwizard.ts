import type { RunnerLogin, RunnerLoginSession } from './api.ts'
import { LOGIN_HOST, badAddressSentence, conflictSentence, knownReason, reasonSentence, refusalSentence, validateUrl } from './loginrelay.ts'

export type Step = 1 | 2 | 3 | 4

export type ActionId = 'open' | 'start' | 'send' | 'back' | 'cancel' | 'done' | 'again'

export interface WizardAction {
  id: ActionId
  label: string
  enabled: boolean
}

export interface WizardView {
  /** Null when the wizard is not on a step: closed, or the relay is not offered. */
  step: Step | null
  /** "Step 2 of 4", or empty without a step. */
  indicator: string
  stepName: string
  heading: string
  paragraphs: string[]
  /** The sign-in link: the host as text, and the address for the anchor. Only on step 2, only for an address that passed `validateUrl`. */
  link: { host: string; href: string } | null
  /** "9 min 30 s left" while a sign-in is open on step 2 or 3. */
  countdown: string | null
  actions: WizardAction[]
  /** The live region's message. Its key changes with the state, the attempt and the reason, never with the clock. */
  announcement: { key: string; text: string } | null
  /** The block "or do it in a terminal" is shown. */
  showTerminal: boolean
  /** How often the page reads the session again. */
  pollMs: number
}

export const stepNames: Record<Step, string> = { 1: 'Start', 2: 'Open the sign-in page', 3: 'Paste the code', 4: 'Result' }

export const labels = {
  open: 'Log in from here',
  openAgain: 'Log in again',
  start: 'Start sign-in',
  cancel: 'Cancel',
  openLink: 'Open the sign-in page',
  copy: 'Copy link',
  send: 'Send code',
  back: 'Back to the link',
  done: 'Done',
  again: 'Try again',
  fresh: 'I started a fresh sign-in',
} as const

export const terminalHeading = 'or do it in a terminal'
export const loggedInHeading = 'The agent is logged in'

const livePollMs = 2000
const idlePollMs = 10000
const maxAttempts = 3

/** The sessions that are not finished: the page reads them often, and they hold the runs back. */
const openStates = new Set(['starting', 'awaiting_code', 'submitting'])
const endedStates = new Set(['succeeded', 'failed', 'cancelled', 'expired'])

/**
 * The step of a session state. The step comes from the server's state, plus one client flag: `typing` says that the maintainer began to
 * write a code. It is false after a reload, so a reload on step 3 lands on step 2 with an empty field. Null for a state it does not know.
 */
export function stepOf(state: string, typing: boolean): Step | null {
  if (state === 'idle' || state === 'starting') return 1
  if (state === 'awaiting_code') return typing ? 3 : 2
  if (state === 'submitting') return 3
  if (endedStates.has(state)) return 4
  return null
}

/** "9 min 30 s left", "45 s left", "0 s left" once it has run out; null for a time that does not parse. */
function countdownText(expiresAt: string, now: number): string | null {
  const at = Date.parse(expiresAt)
  if (Number.isNaN(at)) return null
  const total = Math.max(0, Math.floor((at - now) / 1000))
  const min = Math.floor(total / 60)
  return min > 0 ? `${min} min ${total % 60} s left` : `${total} s left`
}

function waitingLine(runs: number): string | null {
  const n = Math.max(0, Math.floor(runs))
  if (!(n > 0)) return null
  return n === 1 ? '1 run waits for this' : `${n} runs wait for this`
}

const action = (id: ActionId, label: string, enabled = true): WizardAction => ({ id, label, enabled })

const notOffered = 'Logging in from this page is not offered here.'

export interface WizardInput {
  /** What `GET /api/runner/login` answered, or null when it has not (an older control plane, or not yet). */
  offered: RunnerLogin | null
  /** The maintainer opened the wizard on this page. A session that is not finished opens it by itself. */
  open: boolean
  /** The maintainer began to write a code (client state; false after a reload). */
  typing: boolean
  /** Date.now(), for the countdown. */
  now: number
  /** The runner is logged in already. */
  loggedIn: boolean
  /** A client counter that the page bumps on every start of a sign-in; it makes the live-region keys of one sign-in differ from the next. */
  epoch: number
}

function bare(over: Partial<WizardView>): WizardView {
  return {
    step: null,
    indicator: '',
    stepName: '',
    heading: '',
    paragraphs: [],
    link: null,
    countdown: null,
    actions: [],
    announcement: null,
    showTerminal: true,
    pollMs: idlePollMs,
    ...over,
  }
}

/**
 * What the wizard shows. Every sentence is a template; the session's words are lookup keys, and nothing the server sends decides a
 * sentence's structure. The function never receives the code and never copies a session id into the view. The sign-in address is in
 * `link.href` on step 2 and nowhere else. The announcement's key is `announcementKey(session, step, epoch)`.
 */
export function wizardView(i: WizardInput): WizardView {
  const v = buildView(i)
  if (v.step === null || v.announcement === null) return v
  const s = i.offered?.session ?? null
  return { ...v, announcement: { key: announcementKey(s, v.step, i.epoch), text: v.announcement.text } }
}

function buildView(i: WizardInput): WizardView {
  const { offered, now } = i
  if (offered === null || !offered.available) {
    const sentence = offered !== null && offered.unavailable ? refusalSentence(offered.unavailable) : notOffered
    return bare({ heading: 'Log in from a terminal', paragraphs: [sentence] })
  }
  const s = offered.session
  const live = s !== null && openStates.has(s.state)
  const pollMs = live ? livePollMs : idlePollMs
  const known = s === null || stepOf(s.state, false) !== null
  if (!known || (!i.open && !live)) {
    return bare({ heading: 'Log in from this page', actions: [action('open', i.loggedIn ? labels.openAgain : labels.open)], pollMs })
  }
  if (s === null) return step1(i, null, pollMs)
  const step = stepOf(s.state, i.typing) as Step
  switch (step) {
    case 1:
      return step1(i, s, pollMs)
    case 2:
      return step2(s, now, pollMs)
    case 3:
      return step3(s, now, pollMs)
    default:
      return step4(s)
  }
}

function make(step: Step, over: Partial<WizardView> & Pick<WizardView, 'heading' | 'paragraphs' | 'actions'>): WizardView {
  const indicator = `Step ${step} of 4`
  return bare({
    step,
    indicator,
    stepName: stepNames[step],
    // The key is set by wizardView, which knows the epoch.
    announcement: { key: '', text: `${indicator}: ${over.heading}` },
    ...over,
  })
}

/**
 * The live region's key: `${epoch}:${step}:${state}:${attempt}:${reason}`. `useAnnouncement` never announces a key it has already seen,
 * so the key must differ whenever the message should be heard again:
 * - the step is in it, so going from step 2 to step 3 (typing flips) and back is announced;
 * - the epoch is in it, because the key cannot carry the session id: THE COMPONENT KEEPS A COUNTER (`WizardInput.epoch`) and bumps it on
 *   every start of a sign-in ("Start sign-in", "Try again", a new start after Cancel). Without that, a second sign-in on the same page
 *   visit repeats keys the live region has seen and stays silent.
 * The reason goes through `knownReason`; the session's id, address and time, and any word the server sent that the page does not know,
 * are never in the key. A missing session (nothing started) is `idle`, attempt 0.
 */
export function announcementKey(s: RunnerLoginSession | null, step: Step, epoch: number): string {
  const state = s === null ? 'idle' : s.state
  const attempt = s === null ? 0 : s.attempt
  const reason = s === null ? '' : knownReason(s.reason)
  return `${epoch}:${step}:${state}:${attempt}:${reason}`
}

/** A code that was not accepted starts a fresh sign-in on the server: the state goes back to starting with the next attempt. */
function rejected(s: RunnerLoginSession): string[] {
  if (s.attempt < 2) return []
  return [`The last code was not accepted, so I started a fresh sign-in (attempt ${Math.min(s.attempt, maxAttempts)} of ${maxAttempts}). Open the new page and copy a fresh code.`]
}

function step1(i: WizardInput, s: RunnerLoginSession | null, pollMs: number): WizardView {
  if (s === null) {
    const paragraphs = [
      'I ask my runner to start the vendor’s own sign-in. You open a page of the vendor, sign in with your own account and paste the one-time code it shows here.',
      'I send the code to the runner and do not store it.',
    ]
    if (i.loggedIn) paragraphs.push('The runner is logged in already, so this replaces its login.')
    return make(1, {
      heading: 'Start the sign-in',
      paragraphs,
      actions: [action('start', labels.start), action('cancel', labels.cancel)],
      pollMs,
    })
  }
  const paragraphs = [...rejected(s), 'I asked my runner to start the sign-in. This takes a few seconds.']
  const waiting = waitingLine(s.waitingRuns)
  if (waiting) paragraphs.push(waiting)
  return make(1, {
    heading: 'Starting the sign-in',
    paragraphs,
    actions: [action('start', labels.start, false), action('cancel', labels.cancel)],
    showTerminal: false,
    pollMs,
  })
}

function step2(s: RunnerLoginSession, now: number, pollMs: number): WizardView {
  const href = s.url !== undefined && validateUrl(s.url) ? s.url : null
  const paragraphs = [...rejected(s)]
  if (href === null) paragraphs.push(badAddressSentence)
  else paragraphs.push('Open the sign-in page, sign in with your own account, and the page shows a one-time code. Then paste the code here.')
  const waiting = waitingLine(s.waitingRuns)
  if (waiting) paragraphs.push(waiting)
  return make(2, {
    heading: 'Open the sign-in page',
    paragraphs,
    link: href === null ? null : { host: LOGIN_HOST, href },
    countdown: countdownText(s.expiresAt, now),
    actions: [action('open', labels.openLink, href !== null), action('cancel', labels.cancel)],
    showTerminal: href === null,
    pollMs,
  })
}

function step3(s: RunnerLoginSession, now: number, pollMs: number): WizardView {
  const submitting = s.state === 'submitting'
  const paragraphs = submitting
    ? ['I sent the code to the runner. Please wait while it checks the login.']
    : ['Paste the one-time code the vendor’s page shows. I send it to the runner and do not store it.']
  const waiting = waitingLine(s.waitingRuns)
  if (waiting) paragraphs.push(waiting)
  return make(3, {
    heading: 'Paste the code',
    paragraphs,
    countdown: countdownText(s.expiresAt, now),
    actions: [action('send', labels.send, !submitting), action('back', labels.back, !submitting), action('cancel', labels.cancel)],
    showTerminal: false,
    pollMs,
  })
}

function step4(s: RunnerLoginSession): WizardView {
  if (s.state === 'succeeded') {
    return make(4, {
      heading: loggedInHeading,
      paragraphs: ['My runner confirmed the login, so I can run an agent.'],
      actions: [action('done', labels.done)],
      showTerminal: false,
    })
  }
  if (s.state === 'failed' && s.reason === 'invalid_code' && s.attempt < maxAttempts) {
    // The server starts a fresh sign-in on its own in this case; a failure like this would be a session it has not restarted yet.
    return make(2, {
      heading: 'Open the sign-in page',
      paragraphs: ['The last code was not accepted. I am starting a fresh sign-in.'],
      actions: [action('back', labels.fresh), action('cancel', labels.cancel)],
      showTerminal: false,
    })
  }
  const sentence = s.state === 'failed' ? reasonSentence(s.reason) : reasonSentence(s.state)
  const heading = s.state === 'failed' ? 'The sign-in did not work' : s.state === 'cancelled' ? 'The sign-in was cancelled' : 'The sign-in ran out of time'
  return make(4, {
    heading,
    paragraphs: [sentence],
    actions: [action('done', labels.done), action('again', labels.again)],
  })
}

/** The routes of the sign-in that the page calls. */
export type LoginRoute = 'start' | 'code' | 'cancel'

/** What the page says when a code does not have the shape of one (the page's own check, or the server's 400). It never repeats the code. */
export const codeProblem = 'This does not look like a one-time code. Copy the whole code from the vendor’s page and paste it again.'

const unreachable = 'I could not reach the control plane. Please try again.'

/**
 * The sentence for a request of the sign-in that did not work. Only the start's 409 carries a word (a refusal); the 409 of the code and
 * cancel routes means the session moved on, whatever the body says. Every sentence is a template: the server's message is never shown.
 */
export function requestProblem(route: LoginRoute, status: number, reason?: string): string {
  if (status === 409) return route === 'start' ? refusalSentence(reason) : (conflictSentence(409) as string)
  if (status === 429) return conflictSentence(429) as string
  if (status === 400 && route === 'code') return codeProblem
  if (status === 404) return notOffered
  return unreachable
}

const ended = (s: RunnerLoginSession | null): s is RunnerLoginSession => s !== null && endedStates.has(s.state)

/**
 * The view of the sign-in the wizard works on: a finished session that the maintainer dismissed with Done is left out, so the wizard
 * starts over at step 1 instead of showing the old result again. The dismissals are ids the page keeps in memory.
 */
export function shownLogin(offered: RunnerLogin | null, dismissed: ReadonlySet<string>): RunnerLogin | null {
  if (offered === null) return null
  const s = offered.session
  if (ended(s) && dismissed.has(s.id)) return { ...offered, session: null }
  return offered
}

/**
 * Whether the wizard opens without a click for a finished session: the server keeps the result readable for ten minutes, and the page
 * shows it until the maintainer dismisses it (after a reload too). An open session opens the wizard in `wizardView` itself.
 */
export function opensByItself(offered: RunnerLogin | null, dismissed: ReadonlySet<string>): boolean {
  if (offered === null || !offered.available) return false
  const s = offered.session
  return ended(s) && !dismissed.has(s.id)
}

/**
 * How often the runner card reads the runner while a sign-in is about: every 2 s while a session is open, and after a success until the
 * runner reports the login (its report can come a few seconds after the session ended well), so the chip turns green soon. 10 s otherwise.
 */
export function runnerPollMs(sessionState: string, login: string): number {
  if (openStates.has(sessionState)) return livePollMs
  if (sessionState === 'succeeded' && login !== 'ok') return livePollMs
  return idlePollMs
}

/** One sign-in attempt: a new session or a fresh start after a code that was not accepted changes it, a state change within it does not. */
export function sessionPhase(s: RunnerLoginSession | null): string {
  return s === null ? '' : `${s.id}:${s.attempt}`
}
