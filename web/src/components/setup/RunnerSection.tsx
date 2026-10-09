import { useEffect, useId, useState } from 'react'
import { api, ApiError } from '@/api.ts'
import type { RunnerStatus } from '@/api.ts'
import { hideOnError, runnerView, type Chip } from '@/runnerstatus.ts'
import { useClock } from '@/useClock.ts'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

const POLL_MS = 10_000

function ChipView({ chip }: { chip: Chip }) {
  return (
    <span className={cn('flex items-center gap-2 rounded-2xl px-3 py-1.5 text-[13px] font-semibold', chip.soft, chip.text)}>
      <span aria-hidden className={cn('size-2 rounded-full', chip.dot)} />
      {chip.label}
    </span>
  )
}

/** Whether my runner is connected and whether its agent is logged in. In a cluster nobody sees the runner's terminal. */
export default function RunnerSection() {
  const [status, setStatus] = useState<RunnerStatus | null>(null)
  const [error, setError] = useState('')
  // An older control plane has no route for this: the section then says nothing.
  const [absent, setAbsent] = useState(false)
  const now = useClock()
  const headingId = useId()

  useEffect(() => {
    let alive = true
    const load = () =>
      api
        .getRunner()
        .then((s) => {
          if (!alive) return
          setStatus(s)
          setError('')
          setAbsent(false)
        })
        .catch((e: unknown) => {
          if (!alive) return
          if (e instanceof ApiError && hideOnError(e.status)) {
            setAbsent(true)
            setError('')
            return
          }
          // The last good answer is a fact about the past: only the error is shown until a poll succeeds.
          setStatus(null)
          setError(e instanceof ApiError ? e.message : 'Could not load the runner')
        })
    load()
    const timer = setInterval(load, POLL_MS)
    return () => {
      alive = false
      clearInterval(timer)
    }
  }, [])

  if (absent) return null

  const view = status ? runnerView(status, now) : null

  return (
    <section aria-labelledby={headingId} className="flex flex-col gap-3.5">
      <h2 id={headingId} className="text-xs font-semibold text-muted-foreground">
        My runner
      </h2>
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      {!status && !error && <Skeleton className="h-24" />}
      {status && view && (
        <div className="flex flex-col gap-4 rounded-3xl border border-border bg-card p-5">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
            <ChipView chip={view.connection} />
            <ChipView chip={view.login} />
          </div>
          <p aria-live="polite" className="font-serif text-[17px] leading-relaxed text-pretty">
            {view.sentence}
          </p>
          {view.detail && <p className="text-[13px] leading-relaxed text-muted-foreground">{view.detail}</p>}
          {view.hint && <p className="text-[13px] leading-relaxed text-muted-foreground">{view.hint}</p>}
          {view.command && (
            <code className="rounded-xl bg-muted px-3 py-2 font-mono text-xs break-words whitespace-pre-wrap text-foreground select-all">{view.command}</code>
          )}
          {view.version && <span className="text-[13px] break-words text-subtle">Agent CLI: {view.version}</span>}
        </div>
      )}
    </section>
  )
}
