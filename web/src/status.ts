import type { RunStatus } from './api.ts'

/** The dot of a run's status in a list. */
export const statusColor: Record<RunStatus, string> = {
  queued: 'bg-neutral',
  running: 'bg-primary',
  succeeded: 'bg-success',
  failed: 'bg-destructive',
}

/** What Remedy says about a run: a run that waits for an approval is "waiting for you", not "working". */
export type RunPhase = 'queued' | 'working' | 'waiting' | 'done' | 'failed'

export function runPhase(status: RunStatus, waiting: boolean): RunPhase {
  switch (status) {
    case 'queued':
      return 'queued'
    case 'running':
      return waiting ? 'waiting' : 'working'
    case 'succeeded':
      return 'done'
    case 'failed':
      return 'failed'
  }
}

export const phaseView: Record<RunPhase, { label: string; dot: string; soft: string; text: string }> = {
  queued: { label: 'Queued', dot: 'bg-neutral', soft: 'bg-soft-ignored', text: 'text-muted-foreground' },
  working: { label: 'Working', dot: 'bg-primary', soft: 'bg-soft-diagnosing', text: 'text-primary' },
  waiting: { label: 'Waiting for you', dot: 'bg-primary', soft: 'bg-soft-diagnosing', text: 'text-primary' },
  done: { label: 'Done', dot: 'bg-success', soft: 'bg-soft-resolved', text: 'text-success' },
  failed: { label: 'Failed', dot: 'bg-destructive', soft: 'bg-soft-open', text: 'text-destructive' },
}
