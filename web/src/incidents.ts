import type { IncidentState } from './api.ts'

export const incidentStateColor: Record<IncidentState, string> = {
  open: 'bg-rose-500',
  diagnosing: 'bg-amber-500',
  diagnosed: 'bg-sky-500',
  resolved: 'bg-emerald-500',
  ignored: 'bg-slate-600',
}

/** Links that leave Remedy are underlined, so that they do not read as plain text. */
export const externalLinkClass = 'underline decoration-muted-foreground/50 underline-offset-4 hover:decoration-foreground'

/** "pr:7" becomes "PR #7" and "branch:main" becomes "main". */
export function refLabel(ref: string): string {
  if (ref.startsWith('pr:')) return `PR #${ref.slice(3)}`
  if (ref.startsWith('branch:')) return ref.slice(7)
  return ref
}

const conclusionLabels: Record<string, string> = {
  failure: 'failed',
  timed_out: 'timed out',
  startup_failure: 'failed to start',
  cancelled: 'cancelled',
  action_required: 'needs action',
}

export function conclusionText(conclusion: string): string {
  return conclusionLabels[conclusion] ?? conclusion
}

export function reasonText(reason?: string): string {
  switch (reason) {
    case 'green':
      return 'the check turned green'
    case 'pr_closed':
      return 'the pull request was closed or merged'
    default:
      return reason ?? ''
  }
}

/** Only http(s) links become links. Anything else that comes from GitHub's data is shown as text. */
export function safeUrl(url?: string): string | undefined {
  return url && /^https?:\/\//i.test(url) ? url : undefined
}

export function timeAgo(iso: string, now: number = Date.now()): string {
  const seconds = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000))
  if (seconds < 60) return 'just now'
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes} min ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 48) return `${hours} h ago`
  return `${Math.floor(hours / 24)} d ago`
}
