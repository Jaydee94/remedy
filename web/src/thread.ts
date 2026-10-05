import type { ActivityEntry, Diagnosis, Incident, Run, ToolCall } from './api.ts'
import { sourceLabel } from './incidents.ts'
import { kindDotClass } from './timeline.ts'

export type ThreadItem =
  | { type: 'event'; key: string; at: string; dot: string; text: string }
  | { type: 'working'; key: string; at: string; runId?: string }
  | { type: 'diagnosis'; key: string; at: string; diagnosis: Diagnosis; outdated: boolean; sha: string; runId?: string }
  | { type: 'undiagnosed'; key: string; at: string; reason: 'fresh' | 'manual' | 'source'; sourceLabel: string }
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
}

/** The kinds an activity entry has that a message of the thread replaces. */
const REPLACED = new Set(['diagnosis_started', 'diagnosis_finished', 'approval_requested'])

const time = (at: string) => Date.parse(at) || 0

/**
 * The messages of an incident's thread, oldest first: its history as pills, Remedy's diagnosis (or that it is working, or that it can be
 * asked), the questions of the maintainer with their answers, and the calls that wait for a decision. The approval of a question run
 * carries no incident id (the id comes from the tool's arguments), so it is matched by the run.
 */
export function buildThread({ incident, activity, questionRuns, asks }: ThreadInput): ThreadItem[] {
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
      runId: incident.runId,
    })
  } else if (incident.state === 'diagnosing') {
    items.push({ type: 'working', key: 'working', at: incident.lastDiagnosisAt ?? incident.lastSeen, runId: incident.runId })
  } else if (incident.state === 'open') {
    const reason = incident.source !== 'github' ? 'source' : !incident.autoDiagnose ? 'manual' : 'fresh'
    items.push({ type: 'undiagnosed', key: 'undiagnosed', at: incident.lastSeen, reason, sourceLabel: sourceLabel(incident.source) })
  }
  if (incident.diagnosis && incident.state === 'diagnosing') {
    items.push({ type: 'working', key: 'working', at: incident.lastDiagnosisAt ?? incident.lastSeen, runId: incident.runId })
  }

  const questionIds = new Set(questionRuns.map((r) => r.id))
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
