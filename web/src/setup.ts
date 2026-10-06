import type { GitHubConnection, Limits, Repo } from './api.ts'
import { timeAgo } from './incidents.ts'

const unit = (n: number, name: string) => `${n} ${name}${n === 1 ? '' : 's'}`

/** The largest unit that divides `seconds` evenly: 90 seconds stay 90 seconds, 5400 seconds are 90 minutes. */
function largest(seconds: number): { n: number; name: string } {
  if (seconds < 60 || seconds % 60 !== 0) return { n: seconds, name: 'second' }
  if (seconds < 3600 || seconds % 3600 !== 0) return { n: seconds / 60, name: 'minute' }
  return { n: seconds / 3600, name: 'hour' }
}

/** "15 minutes". */
export function duration(seconds: number): string {
  const { n, name } = largest(seconds)
  return unit(n, name)
}

/** What follows "every": "minute", "5 minutes". */
export function everyText(seconds: number): string {
  const { n, name } = largest(seconds)
  return n === 1 ? name : unit(n, name)
}

const times = (n: number) => (n === 1 ? '1 time' : `${n} times`)

/** What Remedy does on its own, as a paragraph in Remedy's voice, from the limits the server answers. */
export function limitsText(l: Limits): string {
  const poll = `I check every ${everyText(l.pollIntervalSeconds)}.`
  const diagnose =
    l.diagnoseMaxPerDay <= 0
      ? "I don't diagnose on my own; you can start it by hand."
      : `I diagnose up to ${times(l.diagnoseMaxPerIncident)} per incident and ${times(l.diagnoseMaxPerDay)} per 24 hours, at least ${duration(l.diagnoseCooldownSeconds)} apart, one run at a time.`
  const stale = `A run that stays running for ${unit(l.staleRunMinutes, 'minute')} is failed.`
  return [poll, diagnose, stale].join(' ')
}

/** The bar of the automatic diagnoses of the last 24 hours, or null when automatic diagnosis is off. */
export function diagnosisBar(l: Limits): { used: number; max: number; ratio: number; full: boolean } | null {
  if (l.diagnoseMaxPerDay <= 0) return null
  const used = l.diagnosesLast24h ?? 0
  return { used, max: l.diagnoseMaxPerDay, ratio: Math.min(1, used / l.diagnoseMaxPerDay), full: used >= l.diagnoseMaxPerDay }
}

/** "owner/name": one slash, two parts of letters, digits, dot, dash and underscore, neither of them "." or "..". The API still has the last word. */
export function validRepoName(name: string): boolean {
  const trimmed = name.trim()
  if (!/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(trimmed)) return false
  return trimmed.split('/').every((part) => part !== '.' && part !== '..')
}

/** The line under a repository's name: its branch, the last poll, and "paused" when it is off. */
export function repoSubline(repo: Repo, now: number): string {
  const polled = repo.lastPolledAt && !Number.isNaN(Date.parse(repo.lastPolledAt)) ? `polled ${timeAgo(repo.lastPolledAt, now)}` : 'never polled'
  return `${repo.defaultBranch}, ${polled}${repo.enabled ? '' : ' · paused'}`
}

/** The chip of the connection's status, in tokens. */
export function connectionView(status: NonNullable<GitHubConnection['status']>): { label: string; dot: string; soft: string; text: string } {
  switch (status) {
    case 'ok':
      return { label: 'Connected', dot: 'bg-success', soft: 'bg-soft-resolved', text: 'text-success' }
    case 'error':
      return { label: 'Error', dot: 'bg-destructive', soft: 'bg-soft-open', text: 'text-destructive' }
    case 'undecryptable':
      return { label: 'Cannot decrypt', dot: 'bg-destructive', soft: 'bg-soft-open', text: 'text-destructive' }
  }
}

/** The opening of the sentence about the token: what Remedy does, or what it is set up to do when the connection does not work. */
export function readingIntro(status: GitHubConnection['status']): string {
  return status === 'error' || status === 'undecryptable' ? "I'm set up to read pull requests and check runs as" : 'I read pull requests and check runs as'
}
