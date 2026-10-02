import { useEffect, useState } from 'react'
import { api, streamRun } from './api.ts'
import type { Run, RunEvent } from './api.ts'
import { statusColor } from './status.ts'

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
      <a href="#/" className="text-sm text-slate-400 hover:text-slate-200">
        ← All runs
      </a>

      {run && status && (
        <header className="flex flex-col gap-2">
          <div className="flex items-center gap-3">
            <span className={`h-2.5 w-2.5 rounded-full ${statusColor[status]}`} />
            <span className="font-medium">{status}</span>
            {run.exitCode !== undefined && <span className="text-sm text-slate-400">exit {run.exitCode}</span>}
          </div>
          <p className="whitespace-pre-wrap rounded-lg bg-slate-900 p-3">{run.prompt}</p>
        </header>
      )}

      <ol className="flex flex-col gap-2 font-mono text-sm">
        {events.map((e) => (
          <li key={e.seq} className="rounded-lg border border-slate-800 bg-slate-900/50 p-2">
            <span className="mr-2 rounded bg-slate-700 px-1.5 py-0.5 text-xs">{e.kind}</span>
            <span className="whitespace-pre-wrap break-words text-slate-300">{summarize(e)}</span>
          </li>
        ))}
      </ol>

      {status === 'running' && <p className="text-sm text-amber-400">Waiting for more output...</p>}
    </div>
  )
}
