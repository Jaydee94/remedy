import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api, ApiError } from './api.ts'
import type { ToolCall } from './api.ts'
import ApprovalCard from './ApprovalCard.tsx'
import { argumentList, callStatusColor, decisionLabel, outcomeText } from './approvals.ts'
import { timeAgo } from './incidents.ts'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'

function HistoryRow({ call }: { call: ToolCall }) {
  const when = call.decidedAt ?? call.finishedAt ?? call.requestedAt
  return (
    <li className="flex gap-3 py-3 first:pt-0 last:pb-0">
      <span aria-hidden className={`mt-2 h-2 w-2 shrink-0 rounded-full ${callStatusColor[call.status]}`} />
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <span className="flex flex-wrap items-baseline gap-x-2">
          <span className="font-mono break-all">{call.tool}</span>
          <span className="text-sm text-muted-foreground">{decisionLabel[call.decision] ?? call.decision}</span>
          <span className="text-xs text-muted-foreground" title={new Date(when).toLocaleString()}>
            {timeAgo(when)}
          </span>
        </span>
        <span className="text-sm break-words whitespace-pre-wrap text-muted-foreground">
          {argumentList(call.arguments)
            .map(({ name, value }) => `${name}: ${value}`)
            .join(' · ')}
        </span>
        {call.reason && <span className="text-sm break-words">Reason: {call.reason}</span>}
        <span className="text-sm break-words whitespace-pre-wrap text-muted-foreground">{outcomeText(call)}</span>
        <span className="flex gap-3 text-xs text-muted-foreground">
          <Link to={`/runs/${encodeURIComponent(call.runId)}`} className="hover:text-foreground">
            Run
          </Link>
          {call.incidentId !== undefined && (
            <Link to={`/incidents/${call.incidentId}`} className="hover:text-foreground">
              Incident #{call.incidentId}
            </Link>
          )}
        </span>
      </div>
    </li>
  )
}

export default function ApprovalsPage() {
  const [calls, setCalls] = useState<ToolCall[] | null>(null)
  const [error, setError] = useState('')

  const load = useCallback(() => {
    api
      .listApprovals('all')
      .then((list) => {
        setCalls(list)
        setError('')
      })
      .catch((e: unknown) => setError(e instanceof ApiError ? e.message : 'Could not load the approvals'))
  }, [])

  useEffect(() => {
    load()
    const timer = setInterval(load, 3000)
    return () => clearInterval(timer)
  }, [load])

  // The API answers newest first. What waits is shown oldest first: the call that was asked first is decided first.
  const pending = (calls ?? []).filter((c) => c.decision === 'pending').reverse()
  const history = (calls ?? []).filter((c) => c.decision !== 'pending')

  return (
    <div className="flex flex-col gap-8">
      <h1 className="text-2xl font-semibold tracking-tight">Approvals</h1>

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {calls === null ? (
        !error && <Skeleton className="h-40 w-full" />
      ) : (
        <>
          <section className="flex flex-col gap-3">
            <h2 className="text-sm tracking-wide text-muted-foreground uppercase">Waiting for you</h2>
            {pending.length === 0 && (
              <p className="text-muted-foreground">
                Nothing waits for a decision. A run with tools asks here before it changes anything.
              </p>
            )}
            {pending.map((call) => (
              <ApprovalCard key={call.id} call={call} onChanged={load} />
            ))}
          </section>

          <section className="flex flex-col gap-3">
            <h2 className="text-sm tracking-wide text-muted-foreground uppercase">History</h2>
            {history.length === 0 ? (
              <p className="text-muted-foreground">No decisions yet.</p>
            ) : (
              <Card>
                <CardContent>
                  <ol className="flex flex-col divide-y divide-border">
                    {history.map((call) => (
                      <HistoryRow key={call.id} call={call} />
                    ))}
                  </ol>
                </CardContent>
              </Card>
            )}
          </section>
        </>
      )}
    </div>
  )
}
