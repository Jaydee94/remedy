import { Link } from 'react-router'
import { timeAgo } from '@/incidents.ts'
import { phaseView } from '@/status.ts'
import type { RecentRun } from '@/askpage.ts'
import { cn } from '@/lib/utils'

/** A run in "Earlier conversations": the dot of its phase, what was asked, the phase in words and the age. */
export default function RecentRunRow({ run }: { run: RecentRun }) {
  const view = phaseView[run.phase]
  return (
    <Link
      to={`/runs/${encodeURIComponent(run.id)}`}
      title={run.title}
      className="flex items-center gap-3 rounded-2xl border border-border bg-card px-4 py-3 outline-none transition-colors hover:border-input focus-visible:ring-3 focus-visible:ring-ring/50"
    >
      <span aria-hidden className={cn('size-2.5 shrink-0 rounded-full', view.dot)} />
      <span className="min-w-0 flex-1 truncate">{run.title}</span>
      {run.tools && <span className="hidden text-xs text-subtle sm:inline">{run.cluster ? 'cluster' : 'tools'}</span>}
      <span className={cn('shrink-0 text-xs font-semibold', view.text)}>{view.label}</span>
      <span className="shrink-0 text-xs text-subtle" title={new Date(run.at).toLocaleString()}>
        {timeAgo(run.at)}
      </span>
    </Link>
  )
}
