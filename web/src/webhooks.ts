import type { WebhookEndpoint } from './api.ts'
import { timeAgo } from './incidents.ts'
import { duration } from './setup.ts'

/** What the block says while the listener does not exist: no route accepts a webhook, whatever endpoints are listed. */
export function listeningNote(listening: boolean): string {
  return listening
    ? ''
    : 'No route exists for webhooks yet: the control plane has no webhook listener. Set REMEDY_WEBHOOK_ADDR (the chart: webhook.enabled) and restart it. Endpoints you create now work once it is on.'
}

/** One line under the name: how it diagnoses and when it resolves. */
export function endpointLine(e: WebhookEndpoint): string {
  const diagnose = e.autoDiagnose ? 'Diagnoses on its own' : 'Diagnosis by a click'
  const resolve = e.resolveAfterSeconds === 0 ? 'resolves only when the sender says so' : `resolves after ${duration(e.resolveAfterSeconds)} of silence`
  return `${e.enabled ? '' : 'Switched off · '}${diagnose} · ${resolve}`
}

/** When the endpoint last received something, and what it counted since the control plane started. */
export function lastUsedText(e: WebhookEndpoint, now: number): string {
  if (!e.lastUsedAt && e.accepted === 0 && e.rejected === 0) return 'not used yet'
  const parts = [e.lastUsedAt ? `last used ${timeAgo(e.lastUsedAt, now)}` : 'not used yet']
  if (e.accepted > 0 || e.rejected > 0) parts.push(`${e.accepted} accepted, ${e.rejected} rejected since the last start`)
  if (e.lastError) parts.push(`last rejection: ${e.lastError}`)
  return parts.join(' · ')
}

/** The same rule as the server: 1 to 40 lower-case letters, digits and dashes, not starting with a dash. */
export function validWebhookName(name: string): boolean {
  return /^[a-z0-9][a-z0-9-]{0,39}$/.test(name)
}

/** A request the sender could make. The token comes from the environment: the page never writes one into a command. */
export function curlExample(name: string, base: string): string {
  const host = base.trim().replace(/\/+$/, '') || 'https://YOUR-HOST'
  return [
    `curl -sS -X POST ${host}/webhook/v1/${name} \\`,
    `  -H "Authorization: Bearer $REMEDY_WEBHOOK_TOKEN" \\`,
    `  -H "Content-Type: application/json" \\`,
    `  -d '{"version":1,"key":"disk-full/nas","status":"firing","title":"Disk almost full on nas","severity":"warning"}'`,
  ].join('\n')
}

export const tokenNotice = 'Copy the token now. It is shown only once and cannot be shown again; if you lose it, rotate it.'
