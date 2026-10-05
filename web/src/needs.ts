import type { ToolCall } from './api.ts'

const asked = (c: ToolCall) => Date.parse(c.requestedAt) || 0

/**
 * The approvals of the Needs you page: what waits, the oldest asked first (the call that was asked first is decided first), and
 * what was answered, newest first. The API answers newest first, but the order is not left to it.
 */
export function splitApprovals(calls: readonly ToolCall[]): { pending: ToolCall[]; answered: ToolCall[] } {
  const pending = calls.filter((c) => c.decision === 'pending').sort((a, b) => asked(a) - asked(b) || a.id - b.id)
  const answered = calls.filter((c) => c.decision !== 'pending').sort((a, b) => b.id - a.id)
  return { pending, answered }
}

/** The decision of a call in words, and the token colour of the words. An approved action that failed says so. */
export function decisionView(call: ToolCall): { label: string; text: string } {
  switch (call.decision) {
    case 'approved':
      if (call.status === 'failed') return { label: 'approved, failed', text: 'text-destructive' }
      if (call.status === 'succeeded') return { label: 'approved', text: 'text-success' }
      if (call.status === 'abandoned' || call.status === 'denied') return { label: 'approved, did not run', text: 'text-muted-foreground' }
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
