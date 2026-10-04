import type { ToolCall } from './api.ts'

/** The arguments of a call as name and value pairs. A value that is not text is shown as JSON. */
export function argumentList(args: unknown): { name: string; value: string }[] {
  if (typeof args !== 'object' || args === null || Array.isArray(args)) {
    return [{ name: 'arguments', value: JSON.stringify(args) ?? '' }]
  }
  return Object.entries(args).map(([name, value]) => ({
    name,
    value: typeof value === 'string' ? value : (JSON.stringify(value) ?? ''),
  }))
}

export const decisionLabel: Record<string, string> = {
  '': 'read',
  pending: 'waiting for you',
  approved: 'approved',
  denied: 'denied',
  abandoned: 'abandoned',
}

export const callStatusColor: Record<ToolCall['status'], string> = {
  running: 'bg-amber-500',
  waiting: 'bg-amber-500',
  succeeded: 'bg-emerald-500',
  failed: 'bg-rose-500',
  denied: 'bg-slate-500',
  abandoned: 'bg-slate-600',
}

/** What came of a call, in a sentence: its result, or why it did not run. */
export function outcomeText(call: ToolCall): string {
  switch (call.status) {
    case 'succeeded':
      return call.result ? `Result: ${call.result}` : 'Done.'
    case 'failed':
    case 'denied':
    case 'abandoned':
      return call.error ?? ''
    case 'running':
      return 'Running.'
    case 'waiting':
      return call.decision === 'approved' ? 'Approved, not run yet.' : 'Waiting for a decision.'
  }
}
