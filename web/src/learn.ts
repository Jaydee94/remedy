import type { Host, HostLearning, Skill } from './api.ts'
import { timeAgo } from './incidents.ts'

/** Most proposals of one host that may wait when a new learning run starts: the server refuses more (internal/store/learner.go). */
export const LEARN_ROOM = 11

/** What the button says it costs and what it does, shown next to it. */
export const LEARN_COST =
  'This costs one run of your subscription. I only read the host with a fixed list of commands and change nothing; whatever I find is a proposal that no run uses before you confirm it.'

export const LEARN_POLL_MS = 3000

/** A learning run is going: queued or running. */
export const learnActive = (l: HostLearning | undefined): boolean => l?.status === 'queued' || l?.status === 'running'

/** How many proposals of the host wait for a review, from the skills of that host. */
export const proposalsWaiting = (skills: readonly Skill[] | null): number => (skills ?? []).filter((s) => s.state === 'proposed').length

/**
 * Why "Get to know this host" cannot be pressed, or '' when it can. The server checks the same things; this says why before the click.
 * `skills` are the skills of the host (null: not loaded yet).
 */
export function learnBlocked(host: Host, skills: readonly Skill[] | null): string {
  if (learnActive(host.learning)) return 'A learning run for this host is going.'
  if (!host.enabled) return 'This host is turned off.'
  if (!host.credential) return "I don't have a login for this host yet."
  if (!host.pinnedFingerprint) return "I don't trust the host's key yet: pin it first."
  if (host.status !== 'ok') return 'Test the connection first: I learn only about a host that shows Connected.'
  if (proposalsWaiting(skills) > LEARN_ROOM) return `${proposalsWaiting(skills)} proposals of this host wait for your review. Review them first, so that a new run has room.`
  return ''
}

/** What the last learning run came to, in words; '' when there was none or it is still going. */
export function learnOutcome(l: HostLearning | undefined, now: number = Date.now()): string {
  if (!l || learnActive(l)) return ''
  const when = l.finishedAt ? timeAgo(l.finishedAt, now) : timeAgo(l.createdAt, now)
  if (l.status === 'succeeded') return `The last learning run finished ${when}.`
  switch (l.failureReason) {
    case 'invalid_output':
      return `The last learning run (${when}) failed: its answer was not valid, so I stored nothing.`
    case 'timeout':
      return `The last learning run (${when}) took too long, so I stopped it and stored nothing.`
    case 'cancelled':
      return `The last learning run (${when}) was cancelled. I stored nothing.`
    case 'runner_lost':
      return `I lost the runner during the last learning run (${when}). I stored nothing.`
  }
  return `The last learning run (${when}) failed. I stored nothing.`
}

/** The sentence while a learning run is going; '' otherwise. */
export function learnProgress(l: HostLearning | undefined): string {
  if (l?.status === 'queued') return 'Waiting for the runner…'
  if (l?.status === 'running') return 'Getting to know the host…'
  return ''
}

/** "3 proposals wait for your review", "1 proposal waits for your review", or '' for none. */
export function proposalsLine(n: number): string {
  if (n <= 0) return ''
  return n === 1 ? '1 proposal waits for your review' : `${n} proposals wait for your review`
}

/** What the live region says after a click. */
export const learnStartedNotice = (name: string): string => `Started. I'm getting to know ${name}.`

/** Where the run is: its page. */
export const learnRunPath = (l: HostLearning | undefined): string | null => (l ? `/runs/${encodeURIComponent(l.runId)}` : null)

/** The next state of the host after a poll: the new host when the poll worked, the old one when it failed. */
export function nextHost(prev: Host, result: PromiseSettledResult<Host>): Host {
  return result.status === 'fulfilled' ? result.value : prev
}
