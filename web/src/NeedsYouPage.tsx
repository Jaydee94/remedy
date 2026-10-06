import { Link } from 'react-router'
import { splitApprovals } from './needs.ts'
import { timeAgo } from './incidents.ts'
import { useApprovals } from './useApprovals.ts'
import { useClock } from './useClock.ts'
import { useFocusRestore } from './useFocusRestore.ts'
import { useNow } from './useNow.ts'
import AnsweredRow from '@/components/needs/AnsweredRow'
import ApprovalAsk from '@/components/conversation/ApprovalAsk'
import EmptyState from '@/components/EmptyState'
import RemedyMessage from '@/components/conversation/RemedyMessage'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'

const LOCK_TICK_MS = 250 // the asks unlock about 700 ms after the list changed: the clock must tick well within that

const linkClass = 'rounded-sm text-primary outline-none hover:text-primary-hover focus-visible:ring-3 focus-visible:ring-ring/50'

/** What waits for a decision, as Remedy's questions, oldest first; under them what was answered. */
export default function NeedsYouPage() {
  const { calls, error, reload, lockedUntil } = useApprovals()
  const { pending, answered } = splitApprovals(calls ?? [])
  const locked = useNow(LOCK_TICK_MS).getTime() < lockedUntil
  const now = useClock()
  const { target: heading, restore } = useFocusRestore<HTMLHeadingElement>()

  // After a decision the control that had the focus is gone and the focus falls to the page: bring it back to the top of the page.
  async function changed() {
    await reload()
    await restore()
  }

  return (
    <div className="mx-auto flex max-w-205 flex-col gap-6 px-4 py-5 md:px-10 md:py-12">
      <div className="flex flex-col gap-1">
        <h1 ref={heading} tabIndex={-1} className="font-serif text-[clamp(26px,3vw,34px)] font-normal outline-none">
          Needs you
        </h1>
        <span className="text-muted-foreground">Questions I can't answer for myself. I wait for you before I change anything.</span>
      </div>

      <p className="sr-only" aria-live="polite">
        {pending.length === 0 ? '' : pending.length === 1 ? '1 question waits for you.' : `${pending.length} questions wait for you.`}
      </p>

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {calls === null ? (
        !error && (
          <div className="flex flex-col gap-5">
            <Skeleton className="h-40" />
          </div>
        )
      ) : (
        <>
          {pending.length === 0 ? (
            <EmptyState title="Nothing waits for you.">A run with tools asks here before it changes anything.</EmptyState>
          ) : (
            pending.map((call) => (
              <RemedyMessage key={call.id} kind="ask" meta={`Remedy · asked ${timeAgo(call.requestedAt, now)}`}>
                <span className="flex flex-wrap gap-x-4 gap-y-1 text-[13px]">
                  {call.incidentId !== undefined && (
                    <Link to={`/incidents/${call.incidentId}`} className={linkClass}>
                      Incident #{call.incidentId}
                    </Link>
                  )}
                  <Link to={`/runs/${encodeURIComponent(call.runId)}`} className={linkClass}>
                    The run
                  </Link>
                </span>
                <ApprovalAsk call={call} variant="large" disabled={locked} onChanged={() => void changed()} />
              </RemedyMessage>
            ))
          )}

          <section className="flex flex-col gap-3.5 border-t border-border pt-6">
            <h2 className="text-xs font-semibold text-muted-foreground">Already answered</h2>
            {answered.length === 0 ? (
              <p className="text-[13px] text-muted-foreground">No decisions yet.</p>
            ) : (
              <ol className="flex flex-col divide-y divide-border">
                {answered.map((call) => (
                  <AnsweredRow key={call.id} call={call} />
                ))}
              </ol>
            )}
          </section>
        </>
      )}
    </div>
  )
}
