import type { RunStatus } from './api.ts'

export const statusColor: Record<RunStatus, string> = {
  queued: 'bg-slate-600',
  running: 'bg-amber-500',
  succeeded: 'bg-emerald-500',
  failed: 'bg-rose-500',
}
