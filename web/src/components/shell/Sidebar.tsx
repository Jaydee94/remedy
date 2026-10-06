import { Link } from 'react-router'
import { askFor, filterIncidents, incidentPreview } from '@/conversation.ts'
import { incidentStateColor, timeAgo } from '@/incidents.ts'
import { navItems } from '@/shell.ts'
import { useSignOut } from '@/shellContext.ts'
import { useClock } from '@/useClock.ts'
import type { Section, ShellState } from '@/shell.ts'
import RemedyMark from '@/components/RemedyMark'
import { navIcons } from '@/components/shell/navIcons.ts'
import PendingBadge from '@/components/shell/PendingBadge'
import { cn } from '@/lib/utils'

interface Props {
  section: Section | null
  pathname: string
  shell: ShellState
}

const focus = 'outline-none focus-visible:ring-3 focus-visible:ring-ring/50'

/** The desktop sidebar: the navigation, the open conversations, what is coming later. Hidden below the md breakpoint. */
export default function Sidebar({ section, pathname, shell }: Props) {
  const signOut = useSignOut()
  const now = useClock()
  return (
    <aside className="hidden w-75 shrink-0 flex-col border-r border-border bg-sidebar md:flex">
      <Link to="/" className={cn('flex items-center gap-2.5 rounded-full px-5.5 pt-5 pb-4 text-foreground', focus)}>
        <RemedyMark size={26} cutout="var(--sidebar)" />
        <span className="font-serif text-[21px] font-medium tracking-tight">remedy</span>
        <span className="ml-auto flex items-center gap-1.5 text-xs text-muted-foreground">
          <span aria-hidden className={cn('size-1.75 rounded-full', shell.online ? 'bg-success' : 'bg-primary')} />
          {shell.online ? 'awake' : 'dozing'}
        </span>
      </Link>

      <nav aria-label="Main" className="flex flex-col gap-0.5 px-2.5">
        {navItems.map((item) => {
          const Icon = navIcons[item.section]
          const active = item.section === section
          return (
            <Link
              key={item.to}
              to={item.to}
              aria-current={pathname === item.to ? 'page' : active ? 'true' : undefined}
              className={cn(
                'flex h-10 items-center gap-3 rounded-full px-3 transition-colors',
                active ? 'bg-secondary text-foreground' : 'text-muted-foreground hover:bg-card hover:text-foreground',
                focus,
              )}
            >
              <Icon className="size-4.25" aria-hidden />
              <span className="flex-1">{item.label}</span>
              {item.section === 'needs' && shell.pending > 0 && <PendingBadge count={shell.pending} />}
            </Link>
          )
        })}
      </nav>

      <div className="mx-5.5 mt-5.5 mb-2 text-xs text-subtle">Open conversations</div>
      <div className="flex min-h-0 flex-1 flex-col gap-0.5 overflow-auto px-2.5">
        {filterIncidents(shell.incidents, 'active').map((incident) => {
          const here = pathname === `/incidents/${incident.id}`
          return (
            <Link
              key={incident.id}
              to={`/incidents/${incident.id}`}
              aria-current={here ? 'page' : undefined}
              className={cn('flex gap-2.5 rounded-2xl px-3 py-2.5 transition-colors hover:bg-card', here && 'bg-card', focus)}
            >
              <span aria-hidden className={cn('mt-1.5 size-2 shrink-0 rounded-full', incidentStateColor[incident.state])} />
              <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                <span className="flex gap-2">
                  <span className="flex-1 truncate font-semibold">
                    <span className="sr-only">{incident.state}: </span>
                    {incident.title}
                  </span>
                  <span className="shrink-0 text-xs text-subtle">{timeAgo(incident.lastSeen, now)}</span>
                </span>
                <span className="truncate text-[13px] text-muted-foreground">
                  {incidentPreview(incident, askFor(shell.asks, incident.id))}
                </span>
              </span>
            </Link>
          )
        })}
        {shell.loaded && shell.incidents.length === 0 && (
          <span className="px-3 py-2.5 text-[13px] text-subtle">No open incidents. I'll start one when a check fails.</span>
        )}
      </div>

      <div className="flex flex-col gap-2 border-t border-border px-5.5 py-3.5">
        <span className="text-xs text-subtle">Coming later</span>
        <span className="flex flex-wrap gap-1.5">
          {['Pull requests', 'Knowledge', 'Graph'].map((name) => (
            <span key={name} className="rounded-full border border-dashed border-input px-2.5 py-0.5 text-xs text-subtle">
              {name}
            </span>
          ))}
        </span>
        <button
          type="button"
          onClick={signOut}
          className={cn('mt-1 self-start rounded-full text-[13px] text-subtle transition-colors hover:text-foreground', focus)}
        >
          Sign out
        </button>
      </div>
    </aside>
  )
}
