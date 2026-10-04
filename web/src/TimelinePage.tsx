import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api, ApiError, streamActivity } from './api.ts'
import type { StreamState, TimelineEntry } from './api.ts'
import { dayLabel, groupByDay, kindDotClass, mergeEntries, timeOfDay } from './timeline.ts'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'

type Live = 'connecting' | StreamState

const liveText: Record<Live, string> = {
  connecting: 'Connecting…',
  live: 'Live',
  reconnecting: 'Reconnecting…',
  closed: 'Disconnected',
}
const liveDot: Record<Live, string> = {
  connecting: 'bg-amber-500',
  live: 'bg-emerald-500',
  reconnecting: 'bg-amber-500',
  closed: 'bg-rose-500',
}

const linkClass = 'underline decoration-muted-foreground/50 underline-offset-4 hover:text-foreground hover:decoration-foreground'

function TimelineRow({ entry }: { entry: TimelineEntry }) {
  return (
    <li className="flex gap-3 py-3 first:pt-0 last:pb-0">
      <span aria-hidden className={`mt-2 h-2 w-2 shrink-0 rounded-full ${kindDotClass(entry.kind)}`} />
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="break-words whitespace-pre-wrap">{entry.summary}</span>
        <span className="flex flex-wrap gap-x-3 text-xs text-muted-foreground">
          <time dateTime={entry.at} title={new Date(entry.at).toLocaleString()}>
            {timeOfDay(entry.at)}
          </time>
          {entry.repo && <span>{entry.repo}</span>}
          {entry.incidentId !== undefined && (
            <Link to={`/incidents/${entry.incidentId}`} className={linkClass}>
              Incident #{entry.incidentId}
            </Link>
          )}
          {entry.runId !== undefined && (
            <Link to={`/runs/${encodeURIComponent(entry.runId)}`} className={linkClass}>
              Run
            </Link>
          )}
        </span>
      </div>
    </li>
  )
}

export default function TimelinePage() {
  const [entries, setEntries] = useState<TimelineEntry[] | null>(null)
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
          (entry) => setEntries((current) => mergeEntries(current ?? [], [entry])),
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
    if (!oldest) return
    setLoadingMore(true)
    try {
      const page = await api.listActivity(oldest.id)
      setEntries((current) => mergeEntries(current ?? [], page.entries))
      setHasMore(page.hasMore)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Could not load older entries')
    } finally {
      setLoadingMore(false)
    }
  }

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-2xl font-semibold tracking-tight">Timeline</h1>
        {entries !== null && (
          <span role="status" className="flex items-center gap-2 text-sm text-muted-foreground">
            <span className={`h-2 w-2 rounded-full ${liveDot[live]}`} />
            {liveText[live]}
            {live === 'closed' && (
              <Button variant="outline" size="sm" onClick={() => window.location.reload()}>
                Reload
              </Button>
            )}
          </span>
        )}
      </div>

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
        !error && <Skeleton className="h-40 w-full" />
      ) : entries.length === 0 ? (
        <p className="text-muted-foreground">
          Nothing has happened yet. Connect GitHub and add a repository under{' '}
          <Link to="/settings" className={linkClass}>
            Settings
          </Link>
          .
        </p>
      ) : (
        <>
          {groupByDay(entries).map((group) => (
            <section key={group.key} className="flex flex-col gap-2">
              <h2 className="text-sm font-medium text-muted-foreground">{dayLabel(group.key)}</h2>
              <Card>
                <CardContent>
                  <ol className="flex flex-col divide-y divide-border">
                    {group.entries.map((entry) => (
                      <TimelineRow key={entry.id} entry={entry} />
                    ))}
                  </ol>
                </CardContent>
              </Card>
            </section>
          ))}
          {hasMore && (
            <Button variant="outline" className="self-center" disabled={loadingMore} onClick={() => void loadMore()}>
              {loadingMore ? 'Loading…' : 'Load older entries'}
            </Button>
          )}
        </>
      )}
    </div>
  )
}
