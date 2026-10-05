import type { Incident } from './api.ts'

export type Section = 'today' | 'conversations' | 'needs' | 'ask' | 'setup'

export interface NavItem {
  section: Section
  to: string
  label: string
  /** The label on a phone, where the tab bar is narrow. */
  short: string
}

export const navItems: NavItem[] = [
  { section: 'today', to: '/', label: 'Today', short: 'Today' },
  { section: 'conversations', to: '/incidents', label: 'Conversations', short: 'Chats' },
  { section: 'needs', to: '/approvals', label: 'Needs you', short: 'Needs you' },
  { section: 'ask', to: '/runs', label: 'Ask Remedy', short: 'Ask' },
  { section: 'setup', to: '/settings', label: 'Setup', short: 'Setup' },
]

/** The section a path belongs to: a thread belongs to Conversations, a run to Ask Remedy. Null for a path outside the navigation. */
export function sectionOf(pathname: string): Section | null {
  if (pathname === '/') return 'today'
  for (const item of navItems.slice(1)) {
    if (pathname === item.to || pathname.startsWith(`${item.to}/`)) return item.section
  }
  return null
}

export interface Header {
  title: string
  /** Where the back arrow of the phone's top bar goes. */
  back?: string
}

/** The title and back target of the phone's top bar for a path, until a page sets its own. */
export function defaultHeader(pathname: string): Header {
  if (pathname.startsWith('/incidents/')) return { title: 'Conversation', back: '/incidents' }
  if (pathname.startsWith('/runs/')) return { title: 'Run', back: '/runs' }
  const section = sectionOf(pathname)
  const item = navItems.find((n) => n.section === section)
  return { title: item ? item.label : 'Remedy' }
}

/** What the shell shows: calls that wait for a decision, the active incidents, whether the server answers, whether it has answered once. */
export interface ShellState {
  pending: number
  incidents: Incident[]
  online: boolean
  loaded: boolean
}

export const emptyShell: ShellState = { pending: 0, incidents: [], online: true, loaded: false }

/**
 * The state after one poll. A rejection that is an HTTP answer (the route may not exist on this server, or it failed once) leaves
 * the shell online and keeps what it had for that part; any other rejection means the server cannot be reached: the shell goes
 * offline and also keeps what it had. `loaded` turns true when the incidents have answered once.
 */
export function nextShell(
  prev: ShellState,
  approvals: PromiseSettledResult<readonly unknown[]>,
  incidents: PromiseSettledResult<Incident[]>,
  isHttpError: (reason: unknown) => boolean,
): ShellState {
  const unreachable = [approvals, incidents].some((r) => r.status === 'rejected' && !isHttpError(r.reason))
  return {
    pending: approvals.status === 'fulfilled' ? approvals.value.length : prev.pending,
    incidents: incidents.status === 'fulfilled' ? incidents.value : prev.incidents,
    online: !unreachable,
    loaded: prev.loaded || incidents.status === 'fulfilled',
  }
}
