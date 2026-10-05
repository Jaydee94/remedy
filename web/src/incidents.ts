import type { Incident, IncidentSource, IncidentState } from './api.ts'

export const incidentStateColor: Record<IncidentState, string> = {
  open: 'bg-destructive',
  diagnosing: 'bg-primary',
  diagnosed: 'bg-info',
  resolved: 'bg-success',
  ignored: 'bg-neutral',
}

/** The soft background and the text colour of an incident's state: the marker of a card and the state chip. */
export const incidentStateSoft: Record<IncidentState, string> = {
  open: 'bg-soft-open',
  diagnosing: 'bg-soft-diagnosing',
  diagnosed: 'bg-soft-diagnosed',
  resolved: 'bg-soft-resolved',
  ignored: 'bg-soft-ignored',
}

export const incidentStateText: Record<IncidentState, string> = {
  open: 'text-destructive',
  diagnosing: 'text-primary',
  diagnosed: 'text-info',
  resolved: 'text-success',
  ignored: 'text-neutral',
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
  firing: 'firing',
  degraded: 'degraded',
  missing: 'missing',
  sync_failed: 'sync failed',
}

export const sourceLabels: Record<IncidentSource, string> = {
  github: 'GitHub',
  alertmanager: 'Alertmanager',
  argocd: 'Argo CD',
}

export function sourceLabel(source: IncidentSource): string {
  return sourceLabels[source] ?? source
}

/** The text of a severity, or nothing for the severity of an incident that has none. */
export function severityText(severity: Incident['severity']): string {
  return severity === 'none' ? '' : severity
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
    case 'cleared':
      return 'the signal is no longer reported'
    default:
      return reason ?? ''
  }
}

const categoryLabels: Record<string, string> = {
  dependency_update: 'Dependency update',
  test_failure: 'Test failure',
  build_error: 'Build error',
  configuration: 'Configuration',
  infrastructure_or_flaky: 'Infrastructure or flaky',
  unknown: 'Unknown cause',
}

export function categoryText(category: string): string {
  return categoryLabels[category] ?? category
}

export function shortSha(sha: string): string {
  return sha.slice(0, 7)
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
