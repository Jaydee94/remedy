import { Link } from 'react-router'
import type { ToolCall } from '@/api.ts'
import { argumentList, callStatusColor, outcomeText } from '@/approvals.ts'
import { timeAgo } from '@/incidents.ts'
import { answeredAt, decisionView } from '@/needs.ts'
import { cn } from '@/lib/utils'

/** A call that was answered: the tool, the decision in colour, the age, what was asked, the reason and what came of it. All text. */
export default function AnsweredRow({ call }: { call: ToolCall }) {
  const at = answeredAt(call)
  const view = decisionView(call)
  const outcome = outcomeText(call)
  return (
    <li className="flex gap-3 py-3.5 first:pt-0 last:pb-0">
      <span aria-hidden className={cn('mt-2 size-2 shrink-0 rounded-full', callStatusColor[call.status])} />
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <span className="flex flex-wrap items-baseline gap-x-2.5">
          <span className="font-mono text-[13px] break-all">{call.tool}</span>
          <span className={cn('text-[13px] font-semibold', view.text)}>{view.label}</span>
          <span className="text-xs text-subtle" title={new Date(at).toLocaleString()}>
            {timeAgo(at)}
          </span>
        </span>
        <dl className="grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-1 text-[13px]">
          {argumentList(call.arguments).map(({ name, value }) => (
            <div key={name} className="contents">
              <dt className="break-all text-muted-foreground">{name}</dt>
              <dd className="font-mono break-words whitespace-pre-wrap">{value}</dd>
            </div>
          ))}
        </dl>
        {call.reason && <span className="text-[13px] break-words">Reason: {call.reason}</span>}
        {outcome && <span className="text-[13px] break-words whitespace-pre-wrap text-muted-foreground">{outcome}</span>}
        <span className="flex gap-3 text-xs">
          <Link to={`/runs/${encodeURIComponent(call.runId)}`} className="rounded-sm text-muted-foreground outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50">
            The run
          </Link>
          {call.incidentId !== undefined && (
            <Link to={`/incidents/${call.incidentId}`} className="rounded-sm text-muted-foreground outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50">
              Incident #{call.incidentId}
            </Link>
          )}
        </span>
      </div>
    </li>
  )
}
