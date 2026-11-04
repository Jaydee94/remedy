import type { Limits, Run } from './api.ts'
import type { Chip } from './runnerstatus.ts'

/**
 * How the runner's agent CLI is billed, as words (cycle 7b). Money is shown only for a run that was billed to an API key, and always as the CLI's
 * estimate: for a subscription the figure the CLI prints is not a charge, and showing it would mislead.
 */

const neutral = { dot: 'bg-muted-foreground', soft: 'bg-muted', text: 'text-muted-foreground' }

/** The chip of the runner card: how the runner bills. A mode the page does not know, or none yet, reads as unknown. */
export function modeChip(authMode: string | undefined): Chip {
  switch (authMode) {
    case 'subscription':
      return { label: 'Billed to your subscription', ...neutral }
    case 'api_key':
      return { label: 'Billed to your API key (Console)', ...neutral }
    default:
      return { label: 'Billing mode unknown', ...neutral }
  }
}

/** Who a run was billed to, from its own record; null when the run does not say, or was stopped before it was billed. */
export function billedTo(run: Pick<Run, 'authSource' | 'failureReason'>): string | null {
  if (run.failureReason === 'billing_mismatch') return null // the source is what it would have used
  switch (run.authSource) {
    case 'subscription':
      return 'your subscription'
    case 'api_key':
      return 'your API key'
    default:
      return null
  }
}

function dollars(cents: number): string {
  return `$${(cents / 100).toFixed(2)}`
}

/** The cost of a run billed to a key, as an estimate; null for any other run. */
export function costLine(run: Pick<Run, 'authSource' | 'costUsd'>): string | null {
  if (run.authSource !== 'api_key' || !(run.costUsd > 0)) return null
  const cents = Math.round(run.costUsd * 100)
  return `${cents < 1 ? 'less than $0.01' : `about ${dollars(cents)}`} (the CLI’s estimate)`
}

/** What the key-billed runs of the last day cost against the daily budget; null while the runner does not bill a key. */
export function spendLine(l: Limits): string | null {
  const spent = l.apiSpendLast24hCents
  const budget = l.apiBudgetPerDayCents
  if (spent === undefined || budget === undefined) return null
  if (budget === 0) return 'Automatic runs are off while I bill your API key: the daily budget is $0.00. A run you start yourself still works.'
  return `About ${dollars(spent)} of ${dollars(budget)} today (the CLI’s estimate; your invoice decides).`
}
