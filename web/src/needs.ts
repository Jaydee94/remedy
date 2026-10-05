import type { ToolCall } from './api.ts'

const asked = (c: ToolCall) => Date.parse(c.requestedAt) || 0

/**
 * The approvals of the Needs you page: what waits, the oldest asked first (the call that was asked first is decided first), and
 * what was answered, the latest answer first (not the latest question). The API answers newest first, but the order is not left to it.
 */
export function splitApprovals(calls: readonly ToolCall[]): { pending: ToolCall[]; answered: ToolCall[] } {
  const pending = calls.filter((c) => c.decision === 'pending').sort((a, b) => asked(a) - asked(b) || a.id - b.id)
  const answered = calls
    .filter((c) => c.decision !== 'pending')
    .sort((a, b) => (Date.parse(answeredAt(b)) || 0) - (Date.parse(answeredAt(a)) || 0) || b.id - a.id)
  return { pending, answered }
}

/** The decision of a call in words, and the token colour of the words. An approved action that failed says so. */
export function decisionView(call: ToolCall): { label: string; text: string } {
  switch (call.decision) {
    case 'approved':
      if (call.status === 'failed') return { label: 'approved, failed', text: 'text-destructive' }
      if (call.status === 'succeeded') return { label: 'approved', text: 'text-success' }
      if (call.status === 'abandoned' || call.status === 'denied') return { label: 'approved, did not run', text: 'text-muted-foreground' }
      if (call.status === 'waiting') return { label: 'approved, not run yet', text: 'text-primary' }
      return { label: 'approved, running', text: 'text-primary' }
    case 'denied':
      return { label: 'denied', text: 'text-muted-foreground' }
    case 'abandoned':
      return { label: 'abandoned', text: 'text-muted-foreground' }
    case 'pending':
      return { label: 'waiting for you', text: 'text-primary' }
    case '':
      return { label: 'read', text: 'text-muted-foreground' }
  }
}

/** When a call was answered: the decision, else the end, else the request. */
export function answeredAt(call: ToolCall): string {
  return call.decidedAt ?? call.finishedAt ?? call.requestedAt
}

/**
 * The calls of the Needs you page from the two polls: the newest list of what waits, and the answered calls of the last full list.
 * A call the pending list no longer has is answered; a stale `pending` entry of the full list is left out until the next full list.
 */
export function mergeApprovals(pending: readonly ToolCall[], all: readonly ToolCall[]): ToolCall[] {
  const waiting = new Set(pending.map((c) => c.id))
  return [...pending, ...all.filter((c) => c.decision !== 'pending' && !waiting.has(c.id))]
}

/**
 * When the asks lock after the pending list changed: a call that was pending and is gone (decided here or elsewhere, abandoned)
 * moves the next call into its place under the pointer, so a second click must not reach it. An append does not lock.
 * Returns the time (ms) until which the asks stay locked, or null for no lock.
 */
export function lockAfterChange(prevPendingIds: readonly number[], nextPendingIds: readonly number[], now: number, ms = 700): number | null {
  const next = new Set(nextPendingIds)
  return prevPendingIds.some((id) => !next.has(id)) ? now + ms : null
}
