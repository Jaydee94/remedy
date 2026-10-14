import type { Setting, SettingValue } from './api.ts'

export interface SettingGroup {
  title: string
  settings: Setting[]
}

/** The settings by group, in the order the server sends them. */
export function groupSettings(settings: Setting[]): SettingGroup[] {
  const groups: SettingGroup[] = []
  for (const s of settings) {
    const group = groups.find((g) => g.title === s.group)
    if (group) group.settings.push(s)
    else groups.push({ title: s.group, settings: [s] })
  }
  return groups
}

/** The value as a person reads it. */
export function valueText(s: Setting): string {
  if (Array.isArray(s.value)) return s.value.length === 0 ? 'none' : s.value.join(', ')
  return String(s.value)
}

/** The value as it stands in the input field. */
export function inputText(s: Setting): string {
  if (Array.isArray(s.value)) return s.value.join(', ')
  return String(s.value)
}

export function sourceBadge(source: Setting['source']): string {
  return source === 'set here' ? 'Set here' : source === 'environment' ? 'Environment' : 'Default'
}

/** The label of the reset button, or null when there is nothing to reset. */
export function resetLabel(s: Setting): string | null {
  return s.source === 'set here' ? `Reset to ${s.fallback}` : null
}

export type Parsed = { ok: true; value: SettingValue } | { ok: false; message: string }

const duration = /^(\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+$/

/** A first check of what was typed, so that an obvious slip needs no request. The server has the last word on the bounds. */
export function parseInput(s: Setting, text: string): Parsed {
  const t = text.trim()
  if (s.kind === 'duration') {
    return duration.test(t) ? { ok: true, value: t } : { ok: false, message: 'Enter a duration such as 30s, 15m or 1h.' }
  }
  if (s.kind === 'number') {
    return /^\d+$/.test(t) ? { ok: true, value: Number(t) } : { ok: false, message: 'Enter a whole number.' }
  }
  return {
    ok: true,
    value: t
      .split(',')
      .map((n) => n.trim())
      .filter((n) => n !== ''),
  }
}

/** Whether the field holds what the setting shows now, so that Save has nothing to do. */
export function unchanged(s: Setting, text: string): boolean {
  const parsed = parseInput(s, text)
  if (!parsed.ok) return false
  return JSON.stringify(parsed.value) === JSON.stringify(s.value)
}
