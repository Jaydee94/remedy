import { useMemo, useState } from 'react'
import { Link, useNavigate } from 'react-router'
import { api, ApiError } from './api.ts'
import { canCancel, runFailure, runMeta, runSteps, summarizeEvent } from './runview.ts'
import { useShellHeader } from './shellContext.ts'
import { phaseView, runPhase } from './status.ts'
import { dayTimeLabel } from './timeline.ts'
import { useClock } from './useClock.ts'
import { useFocusRestore } from './useFocusRestore.ts'
import { useRun } from './useRun.ts'
import ToolCallsCard from './ToolCallsCard.tsx'
import ApprovalAsk from '@/components/conversation/ApprovalAsk'
import FailCard from '@/components/conversation/FailCard'
import RemedyMessage from '@/components/conversation/RemedyMessage'
import StepCard from '@/components/conversation/StepCard'
import UserBubble from '@/components/conversation/UserBubble'
import ConfirmButton from '@/components/ConfirmButton'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button, buttonVariants } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

const summaryClass = 'cursor-pointer rounded-sm text-[13px] text-muted-foreground outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50'

/** One run as a conversation: what was asked, what the agent did, what it answered, and what waits for a decision. */
export default function RunPage({ id }: { id: string }) {
  const navigate = useNavigate()
  const now = useClock()
  const { run, events, calls, status, ended, missing, error: loadError, refresh } = useRun(id)
  const { target: heading, restore } = useFocusRestore<HTMLHeadingElement>()
  useShellHeader(run ? { title: 'Run', back: run.incidentId !== undefined ? `/incidents/${run.incidentId}` : '/runs' } : null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const steps = useMemo(() => runSteps(events), [events])
  const waitingCall = calls.find((c) => c.decision === 'pending')

  // After a decision the ask is gone and the focus falls to the page: bring it back once the page shows the new state.
  async function decided() {
    const back = restore() // before the refresh: it remembers the focused control, which the refresh may remove
    await refresh()
    await back
  }

  async function cancel() {
    setError('')
    try {
      await api.cancelRun(id)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Could not cancel the run')
    }
    refresh()
  }

  async function again() {
    if (!run || busy) return
    setError('')
    setBusy(true)
    try {
      const created = await api.createRun(run.prompt, run.mcp === true, run.cluster === true, run.incidentId)
      // The page navigates away: the button stays disabled until then.
      void navigate(`/runs/${created.id}`)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Could not start the run')
      setBusy(false)
    }
  }

  if (missing) {
    return (
      <div className="mx-auto flex max-w-190 flex-col gap-3 px-4 py-10 md:px-10">
        <h1 className="font-serif text-3xl font-normal">I can't find that run.</h1>
        <Link to="/runs" className="text-primary hover:text-primary-hover">
          Back to Ask Remedy
        </Link>
      </div>
    )
  }

  if (!run || !status) {
    return (
      <div className="mx-auto flex max-w-190 flex-col gap-4 px-4 py-5 md:px-10 md:py-12">
        <h1 className="sr-only">Run</h1>
        {loadError ? (
          <Alert variant="destructive">
            <AlertDescription>{loadError}</AlertDescription>
          </Alert>
        ) : (
          <>
            <Skeleton className="h-8 w-2/5" />
            <Skeleton className="h-24" />
          </>
        )}
      </div>
    )
  }

  // The approval the run waits for, from its calls; when they have not loaded, the run itself says that it waits.
  const waiting = !ended && (waitingCall !== undefined ? waitingCall.waiting === true : run.waitingApproval !== undefined)
  const phase = runPhase(status, waiting)
  const view = phaseView[phase]
  const failure = runFailure(run)
  const responder = run.role === 'responder'
  const time = dayTimeLabel(run.startedAt ?? run.createdAt, now)

  return (
    <div className="mx-auto flex max-w-190 flex-col gap-5 px-4 py-5 md:px-10 md:py-12">
      <h1 ref={heading} tabIndex={-1} className="sr-only outline-none">
        Run
      </h1>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <span className={cn('flex items-center gap-2 rounded-2xl px-3 py-1.5 text-[13px] font-semibold', view.soft, view.text)}>
          <span aria-hidden className={cn('size-2 rounded-full', view.dot)} />
          {view.label}
        </span>
        <span className="text-[13px] text-muted-foreground">{runMeta(run, time)}</span>
        {run.incidentId !== undefined && (
          <Link to={`/incidents/${run.incidentId}`} className="text-[13px] text-primary hover:text-primary-hover">
            Incident #{run.incidentId}
          </Link>
        )}
        {canCancel(run, status) && (
          <span className="sm:ml-auto">
            <ConfirmButton label="Cancel run" confirmLabel="Confirm cancel" onConfirm={() => void cancel()} />
          </span>
        )}
      </div>

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {responder ? (
        <details className="rounded-2xl border border-border bg-card p-3.5">
          <summary className={summaryClass}>Prompt ({run.prompt.length.toLocaleString()} characters, it contains data from GitHub)</summary>
          <p className="mt-2 font-mono text-xs break-words whitespace-pre-wrap text-subtle">{run.prompt}</p>
        </details>
      ) : (
        <UserBubble>{run.prompt}</UserBubble>
      )}

      <RemedyMessage kind={phase === 'waiting' ? 'ask' : phase === 'working' || phase === 'queued' ? 'working' : 'idle'} meta={`Remedy · ${time}`}>
        {steps.map((s) => (
          <StepCard key={s.id} step={s} />
        ))}
        {run.status === 'succeeded' &&
          (responder ? (
            <p className="font-serif text-[clamp(16.5px,1.9vw,19px)] leading-relaxed">
              Done. The diagnosis is on the incident.
              {run.incidentId !== undefined && (
                <>
                  {' '}
                  <Link to={`/incidents/${run.incidentId}`} className="text-primary hover:text-primary-hover">
                    Read the diagnosis
                  </Link>
                </>
              )}
            </p>
          ) : (
            <p className="font-serif text-[clamp(16.5px,1.9vw,19px)] leading-relaxed text-pretty break-words whitespace-pre-wrap">
              {run.result.trim() === '' ? 'Done. There is nothing more to say.' : run.result}
            </p>
          ))}
        {waitingCall && !ended && <ApprovalAsk call={waitingCall} onChanged={() => void decided()} />}
        {!waitingCall && !ended && run.waitingApproval !== undefined && (
          <span className="text-[13px] text-primary">
            This run waits for your decision.{' '}
            <Link to="/approvals" className="underline underline-offset-2 hover:text-primary-hover">
              Open Needs you
            </Link>
          </span>
        )}
        {failure && <FailCard title={failure.title}>{failure.text}</FailCard>}
        {phase === 'queued' && <span className="text-[13px] text-muted-foreground">Waiting for the runner…</span>}
        {phase === 'working' && <span className="text-[13px] text-muted-foreground">Working…</span>}
        {run.cancelRequested && !ended && <span className="text-[13px] text-primary">Cancelling: the runner is stopping the agent.</span>}
        {failure &&
          (responder ? (
            run.incidentId !== undefined && (
              <div>
                <Link to={`/incidents/${run.incidentId}`} className={buttonVariants({ variant: 'outline', size: 'sm' })}>
                  Back to the incident
                </Link>
              </div>
            )
          ) : (
            <div>
              <Button size="sm" disabled={busy} onClick={() => void again()}>
                Start it again
              </Button>
            </div>
          ))}
      </RemedyMessage>

      <details>
        <summary className={summaryClass}>Show raw output ({events.length})</summary>
        <ol className="mt-2 flex flex-col gap-1.5 font-mono text-xs">
          {events.map((e) => (
            <li key={e.seq} className="rounded-xl border border-border bg-card/50 p-2">
              <span className="mr-2 rounded-full bg-secondary px-2 py-0.5 text-muted-foreground">{e.kind}</span>
              <span className="break-words whitespace-pre-wrap text-muted-foreground">{summarizeEvent(e)}</span>
            </li>
          ))}
        </ol>
      </details>
      {calls.length > 0 && (
        <details>
          <summary className={summaryClass}>Tool calls ({calls.length})</summary>
          <div className="mt-2">
            <ToolCallsCard calls={calls} />
          </div>
        </details>
      )}
    </div>
  )
}
