import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api, streamRun } from './api.ts'
import type { Run, RunEvent } from './api.ts'
import { statusColor } from './status.ts'
import { Badge } from '@/components/ui/badge'

function summarize(e: RunEvent): string {
  if (e.kind === 'result' && typeof e.payload === 'object' && e.payload !== null) {
    const result = (e.payload as { result?: unknown }).result
    if (typeof result === 'string') return result
  }
  if (typeof e.payload === 'string') return e.payload
  const text = JSON.stringify(e.payload)
  return text.length > 600 ? `${text.slice(0, 600)}...` : text
}

/** Mount with `key={id}` so that switching runs resets the state instead of resetting it in the effect. */
export default function RunView({ id }: { id: string }) {
  const [run, setRun] = useState<Run | null>(null)
  const [events, setEvents] = useState<RunEvent[]>([])

  useEffect(() => {
    api.getRun(id).then(setRun).catch(() => setRun(null))
    return streamRun(
      id,
      (e) => setEvents((prev) => (prev.some((p) => p.seq === e.seq) ? prev : [...prev, e])),
      setRun,
    )
  }, [id])

  // The run is only fetched once and replaced on "done". Events exist only after the runner has
  // started the run, so a queued run that already has events is in fact running.
  const status = run && run.status === 'queued' && events.length > 0 ? 'running' : run?.status

  return (
    <div className="flex flex-col gap-6">
      <Link to="/runs" className="text-sm text-muted-foreground hover:text-foreground">
        ← All runs
      </Link>

      {run && status && (
        <header className="flex flex-col gap-2">
          <div className="flex items-center gap-3">
            <Badge variant="outline" className="gap-2">
              <span className={`h-2 w-2 rounded-full ${statusColor[status]}`} />
              {status}
            </Badge>
            {run.exitCode !== undefined && <span className="text-sm text-muted-foreground">exit {run.exitCode}</span>}
            {run.role === 'responder' && <Badge variant="secondary">responder</Badge>}
            {run.failureReason === 'timeout' && <Badge variant="destructive">timed out</Badge>}
            {run.failureReason === 'invalid_output' && <Badge variant="destructive">invalid answer</Badge>}
            {run.incidentId !== undefined && (
              <Link to={`/incidents/${run.incidentId}`} className="text-sm text-muted-foreground hover:text-foreground">
                Incident #{run.incidentId}
              </Link>
            )}
          </div>
          {run.role === 'responder' ? (
            <details className="rounded-lg bg-card p-3">
              <summary className="cursor-pointer text-sm text-muted-foreground">
                Prompt ({run.prompt.length.toLocaleString()} characters, it contains data from GitHub)
              </summary>
              <p className="mt-2 font-mono text-xs break-words whitespace-pre-wrap">{run.prompt}</p>
            </details>
          ) : (
            <p className="whitespace-pre-wrap rounded-lg bg-card p-3">{run.prompt}</p>
          )}
          {run.failureReason && run.result && <p className="text-sm text-destructive">{run.result}</p>}
        </header>
      )}

      <ol className="flex flex-col gap-2 font-mono text-sm">
        {events.map((e) => (
          <li key={e.seq} className="rounded-lg border border-border bg-card/50 p-2">
            <Badge variant="secondary" className="mr-2">
              {e.kind}
            </Badge>
            <span className="whitespace-pre-wrap break-words text-muted-foreground">{summarize(e)}</span>
          </li>
        ))}
      </ol>

      {status === 'running' && <p className="text-sm text-amber-400">Waiting for more output...</p>}
    </div>
  )
}
