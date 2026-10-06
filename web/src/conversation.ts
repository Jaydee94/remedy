import type { Incident, IncidentSource, ToolCall } from './api.ts'
import { askText } from './ask.ts'
import { reasonText } from './incidents.ts'

/** The pills of the conversations list. */
export type StateFilter = 'active' | 'resolved' | 'ignored'

export const ACTIVE_STATES: readonly Incident['state'][] = ['open', 'diagnosing', 'diagnosed']

export function matchesFilter(incident: Incident, filter: StateFilter): boolean {
  return filter === 'active' ? ACTIVE_STATES.includes(incident.state) : incident.state === filter
}

/** The incidents of a filter, newest first. The list it is given is not changed. */
export function filterIncidents(list: readonly Incident[], filter: StateFilter): Incident[] {
  return list.filter((i) => matchesFilter(i, filter)).sort((a, b) => b.id - a.id)
}

export function filterCounts(list: readonly Incident[]): Record<StateFilter, number> {
  return {
    active: list.filter((i) => matchesFilter(i, 'active')).length,
    resolved: list.filter((i) => matchesFilter(i, 'resolved')).length,
    ignored: list.filter((i) => matchesFilter(i, 'ignored')).length,
  }
}

/** The incidents of the list narrowed by a source and a repository, and the choices the selects can offer. */
export interface Scope {
  sources: IncidentSource[]
  repos: Map<number, string>
  /** The selected source, or '' when none is selected or the selected one is not in the list any more. */
  source: string
  /** The selected repository id as text, or '' when none is selected or the selected one is not in the list any more. */
  repoId: string
  incidents: Incident[]
}

/**
 * Narrows the list by the selected source and repository. A selection whose value is not in the list any more, or whose select
 * is hidden because there is only one choice, counts as no selection: a stale value must not leave an empty list that cannot be
 * left.
 */
export function scopeIncidents(list: readonly Incident[], source: string, repoId: string): Scope {
  const sources = [...new Set(list.map((i) => i.source))]
  const repos = new Map<number, string>()
  for (const i of list) if (i.source === 'github') repos.set(i.repoId, i.repo)
  // A selection counts only while its select is visible: with one choice the select is hidden and cannot be cleared.
  const effectiveSource = sources.length > 1 && sources.some((s) => s === source) ? source : ''
  const effectiveRepo = repos.size > 1 && repoId !== '' && repos.has(Number(repoId)) ? repoId : ''
  const incidents = list.filter(
    (i) => (effectiveSource === '' || i.source === effectiveSource) && (effectiveRepo === '' || String(i.repoId) === effectiveRepo),
  )
  return { sources, repos, source: effectiveSource, repoId: effectiveRepo, incidents }
}

/** The waiting call that was asked first for an incident. */
export function askFor(asks: readonly ToolCall[], incidentId: number): ToolCall | undefined {
  return asks.filter((c) => c.incidentId === incidentId).sort((a, b) => a.id - b.id)[0]
}

/**
 * The line under an incident in the list and in the sidebar. Ignored and resolved come first: the maintainer's decision and the
 * end of the incident outrank a call that still waits. The sentences are ours; the diagnosis summary is the agent's text and is
 * only shown.
 */
export function incidentPreview(incident: Incident, ask?: ToolCall): string {
  if (incident.state === 'ignored') return 'You ignored this incident.'
  if (incident.state === 'resolved') {
    const why = reasonText(incident.resolvedReason)
    return why ? `Resolved: ${why}.` : 'Resolved.'
  }
  if (ask) return askText(ask).question
  if (incident.state === 'diagnosing') return "I'm looking into it…"
  if (incident.diagnosis) return incident.diagnosis.summary
  // lastDiagnosisAt is set when any diagnosis starts; an open incident without a diagnosis that has one failed it.
  if (incident.state === 'open' && incident.lastDiagnosisAt !== undefined) return "My last diagnosis didn't finish."
  if (!incident.autoDiagnose) return "I don't diagnose this kind of result automatically. Ask me if you want."
  return "I haven't looked yet."
}

const DAY_MS = 24 * 60 * 60 * 1000

const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? '' : 's'}`

/**
 * The digest of Today, from counts over the last 24 hours: incidents opened (by first seen), incidents diagnosed (a stored
 * diagnosis, by the time it was started) and the questions that wait. A clause with a count of 0 is left out, and the quiet
 * sentence appears only when nothing was counted.
 */
export function digestText(now: Date, incidents: readonly Incident[], pending: number): string {
  const since = now.getTime() - DAY_MS
  const opened = incidents.filter((i) => Date.parse(i.firstSeen) >= since).length
  // An incident that is diagnosing again keeps its previous answer, and lastDiagnosisAt is the start of the new run: it is not
  // counted. Known limitation: a failed re-diagnosis cannot be told apart from the API fields and may be counted for up to 24 hours.
  const diagnosed = incidents.filter(
    (i) =>
      i.state !== 'diagnosing' &&
      i.diagnosis !== undefined &&
      i.lastDiagnosisAt !== undefined &&
      Date.parse(i.lastDiagnosisAt) >= since,
  ).length

  let text: string
  if (opened === 0 && diagnosed === 0) {
    text = "All quiet. Nothing opened in the last 24 hours, and I'm still watching."
  } else {
    const parts: string[] = []
    if (opened > 0) parts.push(`opened ${plural(opened, 'incident')}`)
    if (diagnosed > 0) parts.push(`diagnosed ${opened > 0 ? diagnosed : plural(diagnosed, 'incident')}`)
    text = `In the last 24 hours I ${parts.join(' and ')}.`
  }
  if (pending === 1) text += ' One question waits for you.'
  else if (pending > 1) text += ` ${pending} questions wait for you.`
  return text
}

/** The incident whose diagnosis was started last, among those that have one. */
export function lastDiagnosed(incidents: readonly Incident[]): Incident | undefined {
  let best: Incident | undefined
  let bestTime = -Infinity
  for (const i of incidents) {
    if (i.diagnosis === undefined || i.lastDiagnosisAt === undefined) continue
    const t = Date.parse(i.lastDiagnosisAt)
    if (Number.isNaN(t)) continue
    if (t > bestTime) {
      best = i
      bestTime = t
    }
  }
  return best
}

/** The incidents of all, followed by those of ignored that all does not have. The entry of all wins, and the order is kept. */
export function mergeIncidents(all: readonly Incident[], ignored: readonly Incident[]): Incident[] {
  const seen = new Set(all.map((i) => i.id))
  return [...all, ...ignored.filter((i) => !seen.has(i.id))]
}
