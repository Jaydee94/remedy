import { useState } from 'react'
import type { IncidentSource } from './api.ts'
import { askFor, filterCounts, filterIncidents } from './conversation.ts'
import type { StateFilter } from './conversation.ts'
import { sourceLabel } from './incidents.ts'
import { useShellState } from './shellContext.ts'
import { useIncidents } from './useIncidents.ts'
import EmptyState from '@/components/EmptyState'
import IncidentCard from '@/components/conversations/IncidentCard'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

const filterLabels: Record<StateFilter, string> = { active: 'Active', resolved: 'Resolved', ignored: 'Ignored' }

const empty: Record<StateFilter, { title: string; text: string }> = {
  active: { title: 'All quiet.', text: 'No active incidents. I check the enabled repositories every minute.' },
  resolved: { title: 'Nothing resolved yet.', text: 'Incidents land here when the check turns green or the pull request closes.' },
  ignored: { title: 'Nothing ignored.', text: 'Ignored incidents land here. You can stop ignoring them at any time.' },
}

const selectClass =
  'h-8.5 rounded-full border border-input bg-transparent px-3.5 text-[13px] text-muted-foreground outline-none transition-colors hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50'

/** The conversations: one card per incident, with the state filters of the prototype. */
export default function ConversationsPage() {
  const { incidents, error } = useIncidents()
  const { asks } = useShellState()
  const [filter, setFilter] = useState<StateFilter>('active')
  const [source, setSource] = useState('')
  const [repoId, setRepoId] = useState('')

  const sources = [...new Set((incidents ?? []).map((i) => i.source))]
  const repos = new Map<number, string>()
  for (const i of incidents ?? []) if (i.source === 'github') repos.set(i.repoId, i.repo)

  const scoped = (incidents ?? []).filter(
    (i) => (source === '' || i.source === source) && (repoId === '' || String(i.repoId) === repoId),
  )
  const counts = filterCounts(scoped)
  const shown = filterIncidents(scoped, filter)

  return (
    <div className="mx-auto flex max-w-205 flex-col gap-5 px-4 py-5 md:px-10 md:py-12">
      <div className="flex flex-wrap items-end gap-3">
        <div className="flex flex-col gap-1">
          <h1 className="font-serif text-[clamp(26px,3vw,34px)] font-normal">Conversations</h1>
          <span className="text-muted-foreground">One per incident. I open a new one when a check fails.</span>
        </div>
        <span className="flex flex-wrap gap-1.5 sm:ml-auto">
          {(Object.keys(filterLabels) as StateFilter[]).map((key) => {
            const on = filter === key
            return (
              <button
                key={key}
                type="button"
                aria-pressed={on}
                onClick={() => setFilter(key)}
                className={cn(
                  'h-8.5 rounded-full border px-3.5 text-[13px] outline-none transition-colors focus-visible:ring-3 focus-visible:ring-ring/50',
                  on
                    ? 'border-foreground bg-foreground font-semibold text-background'
                    : 'border-input text-muted-foreground hover:text-foreground',
                )}
              >
                {filterLabels[key]} · {counts[key]}
              </button>
            )
          })}
        </span>
      </div>

      {(sources.length > 1 || repos.size > 1) && (
        <div className="flex flex-wrap gap-2">
          {sources.length > 1 && (
            <select aria-label="Source" className={selectClass} value={source} onChange={(e) => setSource(e.target.value)}>
              <option value="">All sources</option>
              {sources.map((s: IncidentSource) => (
                <option key={s} value={s}>
                  {sourceLabel(s)}
                </option>
              ))}
            </select>
          )}
          {repos.size > 1 && (
            <select aria-label="Repository" className={selectClass} value={repoId} onChange={(e) => setRepoId(e.target.value)}>
              <option value="">All repositories</option>
              {[...repos].map(([id, name]) => (
                <option key={id} value={id}>
                  {name}
                </option>
              ))}
            </select>
          )}
        </div>
      )}

      {error && incidents === null && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {incidents === null ? (
        !error && (
          <div className="flex flex-col gap-5">
            <Skeleton className="h-32" />
            <Skeleton className="h-32" />
          </div>
        )
      ) : shown.length === 0 ? (
        <EmptyState title={empty[filter].title}>{empty[filter].text}</EmptyState>
      ) : (
        shown.map((incident) => <IncidentCard key={incident.id} incident={incident} ask={askFor(asks, incident.id)} />)
      )}
    </div>
  )
}
