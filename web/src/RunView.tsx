import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api, ApiError, streamRun } from './api.ts'
import type { Run, RunEvent, ToolCall } from './api.ts'
import { statusColor } from './status.ts'
import ToolCallsCard from './ToolCallsCard.tsx'
import ConfirmButton from '@/components/ConfirmButton'
import { Alert, AlertDescription } from '@/components/ui/alert'
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
  const [calls, setCalls] = useState<ToolCall[]>([])
  const [error, setError] = useState('')

  const refresh = useCallback(() => {
    api.getRun(id).then(setRun).catch(() => undefined)
    api.listToolCalls(id).then(setCalls).catch(() => undefined)
  }, [id])

  const ended = run?.status === 'succeeded' || run?.status === 'failed'

  // The run is fetched again every few seconds while it can still change: it may start waiting for an approval,
  // or be cancelled. One more fetch happens when it has ended, so that its calls are complete.
  useEffect(() => {
    refresh()
    if (ended) return
    const timer = setInterval(refresh, 3000)
    return () => clearInterval(timer)
  }, [refresh, ended])

  useEffect(
    () =>
      streamRun(
        id,
        (e) => setEvents((prev) => (prev.some((p) => p.seq === e.seq) ? prev : [...prev, e])),
        setRun,
      ),
    [id],
  )

  async function cancel() {
    setError('')
    try {
      await api.cancelRun(id)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Could not cancel the run')
    }
    refresh()
  }

  // Events exist only after the runner has started the run, so a queued run that already has events is in fact running.
  const status = run && run.status === 'queued' && events.length > 0 ? 'running' : run?.status
  const canCancel = run !== null && !ended && !run.cancelRequested && (status === 'queued' || (status === 'running' && run.mcp === true))

  return (
    <div className="flex flex-col gap-6">
      <Link to="/runs" className="text-sm text-muted-foreground hover:text-foreground">
        ← All runs
      </Link>

      {run && status && (
        <header className="flex flex-col gap-2">
          <div className="flex flex-wrap items-center gap-3">
            <Badge variant="outline" className="gap-2">
              <span className={`h-2 w-2 rounded-full ${statusColor[status]}`} />
              {status}
            </Badge>
            {run.exitCode !== undefined && <span className="text-sm text-muted-foreground">exit {run.exitCode}</span>}
            {run.role === 'responder' && <Badge variant="secondary">responder</Badge>}
            {run.mcp && <Badge variant="secondary">tools</Badge>}
            {run.failureReason === 'timeout' && <Badge variant="destructive">timed out</Badge>}
            {run.failureReason === 'invalid_output' && <Badge variant="destructive">invalid answer</Badge>}
            {run.failureReason === 'cancelled' && <Badge variant="destructive">cancelled</Badge>}
            {run.failureReason === 'runner_lost' && <Badge variant="destructive">runner lost</Badge>}
            {run.incidentId !== undefined && (
              <Link to={`/incidents/${run.incidentId}`} className="text-sm text-muted-foreground hover:text-foreground">
                Incident #{run.incidentId}
              </Link>
            )}
            {canCancel && <ConfirmButton label="Cancel run" confirmLabel="Confirm cancel" onConfirm={() => void cancel()} />}
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
          {run.cancelRequested && !ended && (
            <p className="text-sm text-amber-400">Cancelling: the runner is stopping the agent.</p>
          )}
        </header>
      )}

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {run?.waitingApproval !== undefined && !ended && (
        <Alert>
          <AlertDescription>
            This run is waiting for your approval.{' '}
            <Link to="/approvals" className="underline underline-offset-4">
              Open the approvals
            </Link>
          </AlertDescription>
        </Alert>
      )}

      <ToolCallsCard calls={calls} />

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

      {status === 'running' && run?.waitingApproval === undefined && (
        <p className="text-sm text-amber-400">Waiting for more output...</p>
      )}
    </div>
  )
}
