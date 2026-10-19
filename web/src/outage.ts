import type { AnyDiagnosis, OutageDiagnosis, ToolCall } from './api.ts'

export function isOutage(d: AnyDiagnosis): d is OutageDiagnosis {
  return 'affected' in d && 'proposed_action' in d
}

const categories: Record<string, string> = {
  workload_crash: 'A workload keeps crashing',
  image_pull: "An image can't be pulled",
  resource_pressure: 'Not enough resources',
  configuration: 'A configuration problem',
  dependency_outage: 'Something it depends on is down',
  host_problem: 'A problem on the machine',
  unknown: "I couldn't tell",
}

export function outageCategoryText(category: string): string {
  return categories[category] ?? category
}

/** What I'd do: the agent's text, or that there is nothing to do when it said "none". */
export function actionText(d: OutageDiagnosis): { none: boolean; text: string } {
  return d.proposed_action.trim().toLowerCase() === 'none' ? { none: true, text: 'Nothing to do for now.' } : { none: false, text: d.proposed_action }
}

/** When I stop waiting for an approval, for a call that still waits and has a deadline; otherwise ''. */
export function expiryText(call: ToolCall, now: number): string {
  if (!call.expiresAt || call.decision !== 'pending') return ''
  const seconds = Math.round((Date.parse(call.expiresAt) - now) / 1000)
  if (!(seconds > 0)) return '' // also an invalid date (NaN)
  // Whole minutes, rounded up, from a minute on: the clock ticks at arbitrary moments, so exact seconds would jump about.
  if (seconds >= 60) {
    const minutes = Math.ceil(seconds / 60)
    return `I stop waiting in ${minutes} ${minutes === 1 ? 'minute' : 'minutes'}.`
  }
  return `I stop waiting in ${seconds} ${seconds === 1 ? 'second' : 'seconds'}.`
}
