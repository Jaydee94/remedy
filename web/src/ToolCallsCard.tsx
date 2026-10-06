import type { ToolCall } from './api.ts'
import { argumentList, callStatusColor, outcomeText } from './approvals.ts'
import { timeAgo } from './incidents.ts'
import { decisionView } from './needs.ts'
import { useClock } from './useClock.ts'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent } from '@/components/ui/card'

/** The audit of a run: every call the agent made to a gatekeeper tool. Nothing here is interpreted: it is all text. */
export default function ToolCallsCard({ calls }: { calls: ToolCall[] }) {
  const now = useClock()
  if (calls.length === 0) return null
  return (
    <Card>
      <CardContent>
        <ol className="flex flex-col divide-y divide-border">
          {calls.map((c) => (
            <li key={c.id} className="py-2 first:pt-0 last:pb-0">
              <details>
                <summary className="flex cursor-pointer flex-wrap items-center gap-2">
                  <span aria-hidden className={`h-2 w-2 shrink-0 rounded-full ${callStatusColor[c.status]}`} />
                  <span className="font-mono break-all">{c.tool}</span>
                  <Badge variant="secondary">{c.kind}</Badge>
                  <span className="text-sm text-muted-foreground">
                    {c.kind === 'mutating' ? decisionView(c).label : c.status}
                  </span>
                  <span className="text-xs text-muted-foreground" title={new Date(c.requestedAt).toLocaleString()}>
                    {timeAgo(c.requestedAt, now)}
                  </span>
                </summary>
                <div className="mt-2 flex flex-col gap-2 pl-4 text-sm">
                  <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1">
                    {argumentList(c.arguments).map(({ name, value }) => (
                      <div key={name} className="contents">
                        <dt className="text-muted-foreground">{name}</dt>
                        <dd className="break-words whitespace-pre-wrap">{value}</dd>
                      </div>
                    ))}
                  </dl>
                  {c.reason && <p className="break-words">Reason: {c.reason}</p>}
                  <p className="break-words whitespace-pre-wrap text-muted-foreground">{outcomeText(c)}</p>
                </div>
              </details>
            </li>
          ))}
        </ol>
      </CardContent>
    </Card>
  )
}
