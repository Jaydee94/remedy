import { askText } from './ask.ts'
import type { ActivityEntry, Diagnosis, Incident, Run, ToolCall } from './api.ts'
import { sourceLabel } from './incidents.ts'
import { kindDotClass } from './timeline.ts'

export type ThreadItem =
  | { type: 'event'; key: string; at: string; dot: string; text: string }
  | { type: 'working'; key: string; at: string; runId?: string }
  | { type: 'diagnosis'; key: string; at: string; diagnosis: Diagnosis; outdated: boolean; sha: string; runId?: string }
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

/**
 * The messages of an incident's thread, oldest first: its history as pills, Remedy's diagnosis (or that it is working, or that it can be
 * asked), the questions of the maintainer with their answers, and the calls that wait for a decision. The approval of a question run
 * carries no incident id (the id comes from the tool's arguments), so it is matched by the run.
 */
export function buildThread({ incident, activity, questionRuns, asks, lastResponder, diagnosisRun }: ThreadInput): ThreadItem[] {
  const items: ThreadItem[] = []

  for (const a of [...activity].sort((x, y) => x.id - y.id)) {
    if (REPLACED.has(a.kind)) continue
    items.push({ type: 'event', key: `a${a.id}`, at: a.at, dot: kindDotClass(a.kind), text: a.summary })
  }

  if (incident.diagnosis) {
    const finished = activity.filter((a) => a.kind === 'diagnosis_finished').sort((x, y) => y.id - x.id)[0]
    items.push({
      type: 'diagnosis',
      key: 'diagnosis',
      at: finished?.at ?? incident.lastDiagnosisAt ?? incident.lastSeen,
      diagnosis: incident.diagnosis,
      outdated: !!incident.diagnosedSha && incident.diagnosedSha !== incident.headSha,
      sha: incident.diagnosedSha || incident.headSha,
      runId: diagnosisRun?.id ?? incident.runId,
    })
  } else if (incident.state === 'open') {
    // A diagnosis that was started (lastDiagnosisAt) and left no diagnosis failed, even when its run is not in the list.
    const failed = lastResponder?.status === 'failed' || incident.lastDiagnosisAt !== undefined
    const reason = incident.source !== 'github' ? 'source' : failed ? 'failed' : !incident.autoDiagnose ? 'manual' : 'fresh'
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
  return flat.length > max ? `${flat.slice(0, max).trimEnd()}…` : flat
}

/** An announcement of a thread: the sentence, and the identity of the message it describes. */
export interface Announcement {
  key: string
  text: string
}

/**
 * What a screen reader should be told about a thread: the newest message of Remedy worth announcing, as a sentence, or null when there is
 * none. The key names the message and its text (a new diagnosis keeps the item key but changes the text). A message leaves the thread when
 * it is dealt with (a decided call), and the newest one that remains is then an older message: the caller shows an announcement only when
 * its key has not been shown before. A value from an agent is cut and inserted.
 */
export function latestAnnouncement(items: readonly ThreadItem[]): Announcement | null {
  for (let i = items.length - 1; i >= 0; i--) {
    const item = items[i]
    let text = ''
    switch (item.type) {
      case 'ask':
        text = `Remedy asks you: ${oneLine(askText(item.call).question, 120)}`
        break
      case 'diagnosis':
        text = `Remedy diagnosed this incident: ${oneLine(item.diagnosis.summary, 100)}`
        break
      case 'answer':
        if (item.run.status === 'succeeded') text = `Remedy answered your question: ${oneLine(item.run.prompt, 60)}`
        else if (item.run.status === 'failed') text = `Remedy could not answer your question: ${oneLine(item.run.prompt, 60)}`
        break
      case 'working':
        text = 'Remedy is looking into this incident.'
        break
    }
    if (text) return { key: `${item.key}|${text}`, text }
  }
  return null
}
