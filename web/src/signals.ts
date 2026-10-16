import type { SignalSource } from './api.ts'
import { duration } from './setup.ts'

function ago(checkedAt: string | undefined, now: number): string {
  if (!checkedAt) return ''
  const seconds = Math.round((now - Date.parse(checkedAt)) / 1000)
  if (!Number.isFinite(seconds)) return ''
  return seconds < 1 ? ' just now' : ` ${duration(seconds)} ago`
}

/** The state of a signal source as one sentence in Remedy's voice. `duration` words are those of the Setup page ("12 seconds"). */
export function signalLine(s: SignalSource, now: number): string {
  const name = s.name === 'alertmanager' ? 'Alertmanager' : s.name
  switch (s.state) {
    case 'off':
      return `I don't ask ${name}: no address is set.`
    case 'pending':
      return `I haven't asked ${name} yet.`
    case 'error':
      return `I couldn't ask ${name}${ago(s.checkedAt, now)}: ${s.error ?? 'it did not answer'}. I changed nothing because of it.`
  }
  const alerts = s.active === 0 ? 'no active alerts' : s.active === 1 ? '1 active alert' : `${s.active} active alerts`
  return `I asked ${name}${ago(s.checkedAt, now)} and it listed ${alerts}.`
}
