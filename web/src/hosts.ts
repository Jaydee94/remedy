import type { Host, HostCredentialView, HostReport, HostStatus } from './api.ts'
import { timeAgo } from './incidents.ts'

/** The chip of a host's status, in tokens. */
export function statusView(status: HostStatus): { label: string; dot: string; soft: string; text: string } {
  switch (status) {
    case 'ok':
      return { label: 'Connected', dot: 'bg-success', soft: 'bg-soft-resolved', text: 'text-success' }
    case 'error':
      return { label: 'Error', dot: 'bg-destructive', soft: 'bg-soft-open', text: 'text-destructive' }
    case 'key_changed':
      return { label: 'Key changed', dot: 'bg-destructive', soft: 'bg-soft-open', text: 'text-destructive' }
    case 'undecryptable':
      return { label: 'Cannot decrypt', dot: 'bg-destructive', soft: 'bg-soft-open', text: 'text-destructive' }
    case 'untrusted':
      return { label: 'Key not pinned', dot: 'bg-muted-foreground', soft: 'bg-muted', text: 'text-muted-foreground' }
    case 'new':
      return { label: 'Not tested', dot: 'bg-muted-foreground', soft: 'bg-muted', text: 'text-muted-foreground' }
  }
}

/** "192.168.178.118 · tested 5 minutes ago". The port shows only when it is not 22; an IPv6 address gets brackets with a port. */
export function hostLine(h: Host, now: number): string {
  let where = h.address
  if (h.port !== 22) where = h.address.includes(':') ? `[${h.address}]:${h.port}` : `${h.address}:${h.port}`
  const tested = h.checkedAt && !Number.isNaN(Date.parse(h.checkedAt)) ? `tested ${timeAgo(h.checkedAt, now)}` : 'never tested'
  return `${where} · ${tested}`
}

/** What Remedy says about the host at the top of its page: the next thing to do, or what went wrong. */
export function statusSentence(h: Host): string {
  if (h.status === 'error' || h.status === 'key_changed' || h.status === 'undecryptable') return h.statusDetail || "I couldn't log in."
  if (!h.credential) return "I don't have a login for this host yet."
  if (h.status === 'ok') return 'I can log in.'
  if (h.status === 'untrusted') return 'I can reach the host, but its key is not pinned yet. Test the connection and compare the fingerprint it shows.'
  if (h.pinnedFingerprint) return "The key is pinned. I haven't tested the login yet."
  return "I have a login, but I don't trust the host's key yet. Test the connection and I'll show you the key."
}

/** What the sudo level of the report means for Remedy. */
export function sudoText(level: HostReport['sudo']): string {
  switch (level) {
    case 'none':
      return 'sudo does not work for this account without a password: I can read, but changes cannot run.'
    case 'limited':
      return 'sudo is limited to the rules below.'
    case 'unrestricted':
      return 'sudo allows everything without a password.'
  }
}

/** A login in words, without the login. */
export function credentialText(c: HostCredentialView): string {
  return c.kind === 'key' ? `Key ${c.hint}` : 'A password'
}

/** Units typed one per line or separated by commas, without blanks and duplicates. The server normalises and checks them. */
export function parseUnits(text: string): string[] {
  const out: string[] = []
  for (const part of text.split(/[\s,]+/)) {
    if (part !== '' && !out.includes(part)) out.push(part)
  }
  return out
}

export const validHostName = (s: string) => /^[A-Za-z0-9][A-Za-z0-9._-]{0,39}$/.test(s.trim())

/** A host name or an IP address. The API still has the last word. */
export function validAddress(s: string): boolean {
  const a = s.trim()
  return /^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$/.test(a) || (/^[0-9A-Fa-f:.]+$/.test(a) && a.includes(':'))
}

export const validDockerPattern = (s: string) => s.trim() === '' || (/^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}\*?$/.test(s.trim()) && s.trim().toUpperCase() !== 'ALL')

/** How to compare the fingerprint the page shows with the host's own. */
export const FINGERPRINT_HELP =
  'On the host, run: ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub (use the file of the key type shown here). The fingerprints must be identical.'

/** A host a run can be given: enabled, with a login and a pinned key. The server checks it again. */
export const usableHost = (h: Host) => h.enabled && h.credential !== undefined && h.pinnedFingerprint !== undefined && h.pinnedFingerprint !== ''

/**
 * What the form of a host is keyed by: its editable fields only. The server moves `updatedAt` for a pin, an unpin and a login as
 * well, and a form that remounted for those would throw away what the maintainer is typing.
 */
export const editableKey = (h: Host) =>
  [h.name, h.address, h.port, h.account, h.notes, h.dockerPattern, h.systemdUnits.join(','), h.enabled].join('\u0000')
