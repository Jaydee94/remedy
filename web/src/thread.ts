import { plainText } from './agenttext.ts'
import { askText } from './ask.ts'
import type { ActivityEntry, AnyDiagnosis, Incident, Run, ToolCall } from './api.ts'
import { sourceLabel } from './incidents.ts'
import { kindDotClass, timeOfDay } from './timeline.ts'

export type ThreadItem =
  | { type: 'event'; key: string; at: string; dot: string; text: string; /** What a screen reader hears when the entry appears, if anything. */ say?: string; /** A quiet line under the opening rather than a pill: a recurrence. */ muted?: boolean }
  | { type: 'working'; key: string; at: string; runId?: string }
  | { type: 'diagnosis'; key: string; at: string; diagnosis: AnyDiagnosis; outdated: boolean; sha: string; runId?: string }
  | { type: 'undiagnosed'; key: string; at: string; reason: 'fresh' | 'manual' | 'source' | 'failed'; sourceLabel: string; runId?: string }
  | { type: 'ask'; key: string; at: string; call: ToolCall }
  | { type: 'question'; key: string; at: string; run: Run }
  | { type: 'answer'; key: string; at: string; run: Run }

export interface ThreadInput {
  incident: Incident
  /** The history of the incident, newest first as the API answers. */
  activity: readonly ActivityEntry[]
  /** The ad-hoc runs that were started about this incident. */
  questionRuns: readonly Run[]
  /** The calls that wait for a decision (any incident). */
  asks: readonly ToolCall[]
  /** The newest responder run of the incident, whatever its status. */
  lastResponder?: Run
  /** The newest succeeded responder run: the one that wrote the stored diagnosis. */
  diagnosisRun?: Run
}

/** The kinds an activity entry has that a message of the thread replaces. */
const REPLACED = new Set(['diagnosis_started', 'diagnosis_finished', 'approval_requested'])

const time = (at: string) => Date.parse(at) || 0

/** How many recurrences are listed one by one; the rest are counted in one line. */
const MAX_RECURRENCE_LINES = 5

/**
 * The messages of an incident's thread, oldest first: its history as pills, Remedy's diagnosis (or that it is working, or that it can be
 * asked), the questions of the maintainer with their answers, and the calls that wait for a decision. The approval of a question run
 * carries no incident id (the id comes from the tool's arguments), so it is matched by the run.
 */
export function buildThread({ incident, activity, questionRuns, asks, lastResponder, diagnosisRun }: ThreadInput): ThreadItem[] {
  const items: ThreadItem[] = []

  const sorted = [...activity].sort((x, y) => x.id - y.id)

  // A re-fired alert reopens the incident (never a GitHub one): each time is a muted line, the latest few in full and the earlier ones as
  // a count. A flapping alert writes a clearing and a reopening every time, so the clearing that comes before a reopening is part of its
  // line, and the count comes from the incident's occurrences (the loaded history is a window and may not reach back to the first one).
  const folded = new Set<number>()
  if (incident.source !== 'github') {
    const lines: { entry: ActivityEntry; clearedAt?: string }[] = []
    let cleared: ActivityEntry | undefined
    for (const a of sorted) {
      if (a.kind === 'incident_resolved') cleared = a
      else if (a.kind === 'incident_reopened') {
        lines.push({ entry: a, clearedAt: cleared?.at })
        if (cleared) folded.add(cleared.id)
        cleared = undefined
      }
    }
    const shown = lines.slice(-MAX_RECURRENCE_LINES)
    const hidden = Math.max(0, incident.occurrences - 1 - shown.length)
    if (hidden > 0 && shown.length > 0) {
      items.push({ type: 'event', key: 'reopened-more', at: shown[0].entry.at, dot: kindDotClass('incident_reopened'), text: `and ${hidden} more`, muted: true })
    }
    for (const { entry, clearedAt } of shown) {
      const text = `Fired again ${timeOfDay(entry.at)}${clearedAt ? `, it had cleared ${timeOfDay(clearedAt)}` : ''}`
      items.push({ type: 'event', key: `a${entry.id}`, at: entry.at, dot: kindDotClass(entry.kind), text, say: 'This alert fired again.', muted: true })
    }
  } else {
    const reopened = sorted.filter((a) => a.kind === 'incident_reopened')
    const hidden = Math.max(0, reopened.length - MAX_RECURRENCE_LINES)
    reopened.forEach((a, n) => {
      if (n < hidden) return
      items.push({ type: 'event', key: `a${a.id}`, at: a.at, dot: kindDotClass(a.kind), text: `Fired again ${timeOfDay(a.at)}`, say: 'This alert fired again.', muted: true })
    })
    if (hidden > 0) {
      items.push({ type: 'event', key: 'reopened-more', at: reopened[0].at, dot: kindDotClass('incident_reopened'), text: `and ${hidden} more`, muted: true })
    }
  }

  for (const a of sorted) {
    if (REPLACED.has(a.kind) || a.kind === 'incident_reopened' || folded.has(a.id)) continue
    // A failed diagnosis of an incident with no diagnosis is said by the undiagnosed message; with one, nothing else changes in the thread.
    const say = a.kind === 'diagnosis_failed' && incident.diagnosis ? 'My latest diagnosis did not finish: this is the earlier one.' : undefined
    items.push({ type: 'event', key: `a${a.id}`, at: a.at, dot: kindDotClass(a.kind), text: a.summary, say })
  }

  if (incident.diagnosis) {
    const finished = activity.filter((a) => a.kind === 'diagnosis_finished').sort((x, y) => y.id - x.id)[0]
    items.push({
      type: 'diagnosis',
      key: 'diagnosis',
      at: finished?.at ?? incident.diagnosedAt ?? incident.lastDiagnosisAt ?? incident.lastSeen,
      diagnosis: incident.diagnosis,
      outdated: !!incident.diagnosedSha && incident.diagnosedSha !== incident.headSha,
      sha: incident.diagnosedSha || incident.headSha,
      runId: diagnosisRun?.id ?? incident.runId,
    })
  } else if (incident.state === 'open') {
    // A diagnosis that was started (lastDiagnosisAt) and left no diagnosis failed, even when its run is not in the list.
    const failed = lastResponder?.status === 'failed' || incident.lastDiagnosisAt !== undefined
    const reason = incident.source === 'argocd' ? 'source' : failed ? 'failed' : !incident.autoDiagnose ? 'manual' : 'fresh'
    items.push({
      type: 'undiagnosed',
      key: 'undiagnosed',
      at: incident.lastSeen,
      reason,
      sourceLabel: sourceLabel(incident.source),
      runId: reason === 'failed' && lastResponder?.status === 'failed' ? lastResponder.id : undefined,
    })
  }
  if (incident.state === 'diagnosing') {
    items.push({ type: 'working', key: 'working', at: incident.lastDiagnosisAt ?? incident.lastSeen, runId: incident.runId })
  }

  const questionIds = new Set(questionRuns.filter((r) => r.role === 'adhoc').map((r) => r.id))
  for (const r of questionRuns) {
    if (r.role !== 'adhoc') continue
    items.push({ type: 'question', key: `q${r.id}`, at: r.createdAt, run: r })
    items.push({ type: 'answer', key: `r${r.id}`, at: r.finishedAt ?? r.startedAt ?? r.createdAt, run: r })
  }

  for (const c of asks) {
    if (c.incidentId === incident.id || questionIds.has(c.runId)) items.push({ type: 'ask', key: `c${c.id}`, at: c.requestedAt, call: c })
  }

  // Array.prototype.sort is stable: a question stays before its answer when both carry the same time.
  return items.sort((x, y) => time(x.at) - time(y.at))
}

const oneLine = (text: string, max: number) => {
  const flat = text.replace(/\s+/g, ' ').trim()
  // Cut by code points: a UTF-16 cut could leave half of an emoji.
  const points = Array.from(flat)
  return points.length > max ? `${points.slice(0, max).join('').trimEnd()}…` : flat
}

/** An announcement of a thread: the sentence, and the identity of the message it describes. */
export interface Announcement {
  key: string
  text: string
}

/** The announcement of one item, or null when it is not worth announcing (or has nothing to say yet). A value from an agent is cut and inserted. */
function announcementOf(item: ThreadItem): Announcement | null {
  let text = ''
  switch (item.type) {
    case 'ask':
      text = `Remedy asks you: ${oneLine(askText(item.call).question, 120)}`
      break
    case 'diagnosis':
      // The key is the item and its text: a re-diagnosis with an identical summary is not announced twice (the `working` message
      // before it was).
      text = `Remedy diagnosed this incident: ${oneLine(plainText(item.diagnosis.summary, 200), 100)}`
      break
    case 'answer':
      if (item.run.status === 'succeeded') text = `Remedy answered your question: ${oneLine(item.run.prompt, 60)}`
      else if (item.run.status === 'failed') text = `Remedy could not answer your question: ${oneLine(item.run.prompt, 60)}`
      break
    case 'working':
      text = 'Remedy is looking into this incident.'
      break
    case 'event':
      text = item.say ?? ''
      break
    case 'undiagnosed':
      // The other reasons are states the incident starts in, not something that happened while the page was open.
      if (item.reason === 'failed') text = 'Remedy could not diagnose this incident.'
      break
  }
  if (!text) return null
  // A second diagnosis of the same incident says the same words: its run tells it from the first.
  const run = item.type === 'working' || item.type === 'undiagnosed' ? `${item.runId ?? ''}|` : ''
  return { key: `${item.key}|${run}${text}`, text }
}

/** Every message of a thread worth announcing, oldest first: what a page has already shown when it has loaded. */
export function allAnnouncements(items: readonly ThreadItem[]): Announcement[] {
  return items.flatMap((item) => announcementOf(item) ?? [])
}
