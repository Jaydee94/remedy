import { useState } from 'react'
import { Link } from 'react-router'
import { api, ApiError } from './api.ts'
import type { ToolCall } from './api.ts'
import { argumentList } from './approvals.ts'
import { timeAgo } from './incidents.ts'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'

const linkClass = 'underline decoration-muted-foreground/50 underline-offset-4 hover:text-foreground hover:decoration-foreground'

/** A mutating call that waits for a decision. What it shows is exactly what runs when it is approved. */
export default function ApprovalCard({ call, onChanged }: { call: ToolCall; onChanged: () => void }) {
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  async function decide(approve: boolean) {
    setBusy(true)
    setError('')
    try {
      await api.decideApproval(call.id, approve, reason.trim() || undefined)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Could not record the decision')
    } finally {
      setBusy(false)
      onChanged() // the state may have changed under us, so look again whatever happened
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="font-mono text-base break-all">{call.tool}</CardTitle>
        <p className="flex flex-wrap gap-x-3 text-sm text-muted-foreground">
          <span title={new Date(call.requestedAt).toLocaleString()}>asked {timeAgo(call.requestedAt)}</span>
          <Link to={`/runs/${encodeURIComponent(call.runId)}`} className={linkClass}>
            Run
          </Link>
          {call.incidentId !== undefined && (
            <Link to={`/incidents/${call.incidentId}`} className={linkClass}>
              Incident #{call.incidentId}
            </Link>
          )}
        </p>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <dl className="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-2 text-sm">
          {argumentList(call.arguments).map(({ name, value }) => (
            <div key={name} className="contents">
              <dt className="text-muted-foreground">{name}</dt>
              <dd className="break-words whitespace-pre-wrap">{value}</dd>
            </div>
          ))}
        </dl>

        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}

        {call.waiting ? (
          <div className="flex flex-wrap items-center gap-2">
            <Input
              aria-label="Reason (optional)"
              placeholder="Reason (optional)"
              maxLength={500}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              className="max-w-xs"
            />
            <Button disabled={busy} onClick={() => void decide(true)}>
              Approve
            </Button>
            <Button variant="outline" disabled={busy} onClick={() => void decide(false)}>
              Deny
            </Button>
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">
            The agent is no longer waiting for this call, so it can no longer be decided. It is marked as abandoned shortly.
          </p>
        )}
      </CardContent>
    </Card>
  )
}
