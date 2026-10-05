import type { TimelineEntry } from './api.ts'

export interface DayGroup {
  /** The local calendar day, YYYY-MM-DD. */
  key: string
  entries: TimelineEntry[]
}

function pad(n: number): string {
  return String(n).padStart(2, '0')
}

/** The local calendar day of a point in time, as YYYY-MM-DD. */
export function dayKey(iso: string): string {
  const d = new Date(iso)
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

/** "Today", "Yesterday", or the date written out. */
export function dayLabel(key: string, now: Date = new Date()): string {
  const yesterday = new Date(now)
  yesterday.setDate(now.getDate() - 1)
  if (key === dayKey(now.toISOString())) return 'Today'
  if (key === dayKey(yesterday.toISOString())) return 'Yesterday'
  const [year, month, day] = key.split('-').map(Number)
  return new Date(year, month - 1, day).toLocaleDateString(undefined, {
    weekday: 'long',
    day: 'numeric',
    month: 'long',
    year: 'numeric',
  })
}

/** Groups entries that are sorted newest first into days, keeping the order. */
export function groupByDay(entries: TimelineEntry[]): DayGroup[] {
  const groups: DayGroup[] = []
  for (const entry of entries) {
    const key = dayKey(entry.at)
    const last = groups[groups.length - 1]
    if (last && last.key === key) last.entries.push(entry)
    else groups.push({ key, entries: [entry] })
  }
  return groups
}

/** Adds entries to a list, newest first, and drops duplicates: a live entry may also be in a loaded page. */
export function mergeEntries(current: TimelineEntry[], incoming: TimelineEntry[]): TimelineEntry[] {
  const byId = new Map<number, TimelineEntry>()
  for (const e of current) byId.set(e.id, e)
  for (const e of incoming) byId.set(e.id, e)
  return [...byId.values()].sort((a, b) => b.id - a.id)
}

const kindDots: Record<string, string> = {
  incident_opened: 'bg-destructive',
  incident_recurred: 'bg-destructive',
  incident_resolved: 'bg-success',
  incident_ignored: 'bg-neutral',
  incident_unignored: 'bg-primary',
  poll_failed: 'bg-primary',
  poll_recovered: 'bg-success',
  diagnosis_started: 'bg-primary',
  diagnosis_finished: 'bg-info',
  diagnosis_failed: 'bg-destructive',
  approval_requested: 'bg-primary',
  approval_decided: 'bg-neutral',
  approval_abandoned: 'bg-neutral',
  note_added: 'bg-primary',
  run_cancelled: 'bg-neutral',
  cluster_action: 'bg-violet',
}

export function kindDotClass(kind: string): string {
  return kindDots[kind] ?? 'bg-neutral'
}

export function timeOfDay(iso: string): string {
  return new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}
