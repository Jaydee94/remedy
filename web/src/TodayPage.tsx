import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api, ApiError, streamActivity } from './api.ts'
import type { StreamState, TimelineEntry } from './api.ts'
import { digestText, lastDiagnosed } from './conversation.ts'
import { useShellState } from './shellContext.ts'
import { dayLabel, groupByDay, kindDotClass, mergeEntries, timeOfDay } from './timeline.ts'
import { useIncidents } from './useIncidents.ts'
import { useClock } from './useClock.ts'
import EmptyState from '@/components/EmptyState'
import Digest from '@/components/today/Digest'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

type Live = 'connecting' | StreamState

const linkClass = 'whitespace-nowrap text-primary outline-none hover:text-primary-hover focus-visible:ring-3 focus-visible:ring-ring/50 rounded-sm'

/** Today: Remedy's digest of the last 24 hours and the feed of what happened, newest first. */
export default function TodayPage() {
  const { incidents, error: incidentsError } = useIncidents()
  const { pending, loaded } = useShellState()
  const clock = useClock()
  const now = new Date(clock)
  const [entries, setEntries] = useState<TimelineEntry[] | null>(null)
  const [fresh, setFresh] = useState<ReadonlySet<number>>(new Set())
  const [hasMore, setHasMore] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState('')
  const [live, setLive] = useState<Live>('connecting')
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    let active = true
    let stop: (() => void) | undefined
    api
      .listActivity()
      .then((page) => {
        if (!active) return
        setEntries(page.entries)
        setHasMore(page.hasMore)
        setError('')
        // Follow from the newest entry this page has, so that nothing between the list and the stream is lost.
        const newest = page.entries[0]?.id ?? 0
        stop = streamActivity(
          newest,
          (entry) => {
            setFresh((current) => new Set(current).add(entry.id))
            setEntries((current) => mergeEntries(current ?? [], [entry]))
          },
          setLive,
        )
      })
      .catch((e: unknown) => {
        if (active) setError(e instanceof ApiError ? e.message : 'Could not load the timeline')
      })
    return () => {
      active = false
      stop?.()
    }
  }, [attempt])

  async function loadMore() {
    const oldest = entries?.[entries.length - 1]
    if (!oldest || loadingMore) return
    setLoadingMore(true)
    try {
      const page = await api.listActivity(oldest.id)
      setEntries((current) => mergeEntries(current ?? [], page.entries))
      setHasMore(page.hasMore)
      setError('')
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Could not load older entries')
    } finally {
      setLoadingMore(false)
    }
  }

  const time = timeOfDay(now.toISOString())

  return (
    <div className="mx-auto flex max-w-190 flex-col gap-6.5 px-4 py-5 md:px-10 md:py-12">
      <h1 className="sr-only">Today</h1>

      {live === 'closed' && (
        <div role="status" className="flex flex-wrap items-center gap-3 rounded-2xl bg-soft-diagnosing px-4 py-2.5 text-[13px] text-primary">
          Live updates stopped.
          <Button variant="outline" size="sm" onClick={() => window.location.reload()}>
            Reload
          </Button>
        </div>
      )}

      {incidents !== null && loaded ? (
        <Digest text={digestText(now, incidents, pending)} pending={pending} diagnosedId={lastDiagnosed(incidents)?.id} time={time} />
      ) : incidents === null && incidentsError ? (
        <p className="text-[13px] text-muted-foreground">I couldn't load the incidents.</p>
      ) : (
        <div className="flex flex-col gap-3">
          <Skeleton className="size-9 rounded-full" />
          <Skeleton className="h-6 w-4/5" />
          <Skeleton className="h-6 w-3/5" />
        </div>
      )}

      {error && (
        <Alert variant="destructive">
          <AlertDescription className="flex flex-wrap items-center justify-between gap-3">
            {error}
            {entries === null && (
              <Button variant="outline" size="sm" onClick={() => setAttempt((n) => n + 1)}>
                Try again
              </Button>
            )}
          </AlertDescription>
        </Alert>
      )}

      {entries === null ? (
        !error && <Skeleton className="h-40" />
      ) : entries.length === 0 ? (
        <EmptyState title="Nothing has happened yet.">
          Connect GitHub and add a repository under{' '}
          <Link to="/settings" className={linkClass}>
            Setup
          </Link>
          .
        </EmptyState>
      ) : (
        <>
          {groupByDay(entries).map((group) => (
            <section key={group.key} className="flex flex-col gap-0.5">
              <div className="mt-1.5 mb-2.5 flex items-center gap-3">
                <span className="h-px flex-1 bg-border" />
                <h2 className="text-xs font-normal text-subtle">{dayLabel(group.key)}</h2>
                <span className="h-px flex-1 bg-border" />
              </div>
              {group.entries.map((entry) => (
                <div key={entry.id} className={cn('relative flex gap-3.5 py-2 pl-12.5', fresh.has(entry.id) && 'animate-rm-in')}>
                  <span aria-hidden className={cn('absolute top-3.5 left-3.5 size-2 rounded-full', kindDotClass(entry.kind))} />
                  <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                    <span className="break-words whitespace-pre-wrap text-foreground/90">{entry.summary}</span>
                    <span className="flex flex-wrap gap-x-3 text-xs text-subtle">
                      <time dateTime={entry.at} title={new Date(entry.at).toLocaleString()}>
                        {timeOfDay(entry.at)}
                      </time>
                      {entry.incidentId !== undefined && (
                        <Link to={`/incidents/${entry.incidentId}`} className={linkClass}>
                          Incident #{entry.incidentId}
                        </Link>
                      )}
                      {entry.runId !== undefined && (
                        <Link to={`/runs/${encodeURIComponent(entry.runId)}`} className={linkClass}>
                          See my work
                        </Link>
                      )}
                    </span>
                  </div>
                </div>
              ))}
            </section>
          ))}
          {hasMore && (
            <Button variant="outline" className="self-center" aria-disabled={loadingMore} onClick={() => void loadMore()}>
              {loadingMore ? 'Loading…' : 'Load older entries'}
            </Button>
          )}
        </>
      )}
    </div>
  )
}
