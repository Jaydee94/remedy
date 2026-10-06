import { Link } from 'react-router'
import type { Incident, ToolCall } from '@/api.ts'
import { incidentPreview, matchesFilter } from '@/conversation.ts'
import { conclusionText, incidentStateColor, incidentStateSoft, incidentStateText, refLabel, sourceLabel, timeAgo } from '@/incidents.ts'
import { useClock } from '@/useClock.ts'
import { cn } from '@/lib/utils'

interface Props {
  incident: Incident
  /** The call that waits for a decision for this incident, if there is one. */
  ask?: ToolCall
}

/** One incident of the conversations list. Everything from GitHub and from the agent is shown as text. */
export default function IncidentCard({ incident, ask }: Props) {
  const now = useClock()
  const github = incident.source === 'github'
  const active = matchesFilter(incident, 'active')
  return (
    <Link
      to={`/incidents/${incident.id}`}
      className="flex gap-3.5 rounded-[20px] border border-border bg-card p-4.5 text-foreground outline-none transition-colors hover:border-input hover:bg-secondary/40 focus-visible:ring-3 focus-visible:ring-ring/50"
    >
      <span
        aria-hidden
        className={cn('flex size-10 shrink-0 items-center justify-center rounded-full', incidentStateSoft[incident.state])}
      >
        <span className={cn('size-2.5 rounded-full', incidentStateColor[incident.state])} />
      </span>
      <span className="flex min-w-0 flex-1 flex-col gap-1.5">
        <span className="flex flex-wrap items-baseline gap-x-2.5 gap-y-1">
          <span className="min-w-0 text-[15px] font-semibold break-words">{incident.title}</span>
          <span className="min-w-0 text-[13px] break-words text-muted-foreground">
            {github ? `${incident.repo} · ${refLabel(incident.ref)}` : sourceLabel(incident.source)}
          </span>
          <span className="ml-auto text-xs text-subtle">{timeAgo(incident.lastSeen, now)}</span>
        </span>
        <span className="font-serif text-[15px] leading-normal text-pretty break-words text-foreground/85">
          {incidentPreview(incident, ask)}
        </span>
        <span className="flex flex-wrap gap-1.5 text-xs">
          <span className={cn('rounded-full px-2.5 py-0.5', incidentStateSoft[incident.state], incidentStateText[incident.state])}>
            {incident.state}
          </span>
          <span className="rounded-full bg-secondary px-2.5 py-0.5 text-muted-foreground">
            {conclusionText(incident.conclusion)} · {incident.occurrences}×
          </span>
          {ask && active && (
            <span className="rounded-full bg-primary px-2.5 py-0.5 font-semibold text-primary-foreground">asks you</span>
          )}
        </span>
      </span>
    </Link>
  )
}
