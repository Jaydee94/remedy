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

export const callStatusColor: Record<ToolCall['status'], string> = {
  running: 'bg-primary',
  waiting: 'bg-primary',
  succeeded: 'bg-success',
  failed: 'bg-destructive',
  denied: 'bg-neutral',
  abandoned: 'bg-neutral',
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

/** `text` cut to at most `max` UTF-8 bytes, on a character boundary (the server limits some fields in bytes, not characters). */
export function limitBytes(text: string, max: number): string {
  const encoder = new TextEncoder()
  if (encoder.encode(text).length <= max) return text
  let out = ''
  let bytes = 0
  for (const ch of text) {
    const n = encoder.encode(ch).length
    if (bytes + n > max) break
    out += ch
    bytes += n
  }
  return out
}
