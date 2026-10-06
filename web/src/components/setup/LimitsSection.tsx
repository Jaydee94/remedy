import { useEffect, useState } from 'react'
import { api, ApiError } from '@/api.ts'
import type { Limits } from '@/api.ts'
import { diagnosisBar, limitsText } from '@/setup.ts'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

/** What Remedy does on its own, and how much of it. The server's environment sets the limits. */
export default function LimitsSection() {
  const [limits, setLimits] = useState<Limits | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    api
      .getLimits()
      .then(setLimits)
      .catch((e: unknown) => setError(e instanceof ApiError ? e.message : 'Could not load the limits'))
  }, [])

  const bar = limits ? diagnosisBar(limits) : null

  return (
    <section className="flex flex-col gap-3.5">
      <h2 className="text-xs font-semibold text-muted-foreground">My limits</h2>
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      {!limits && !error && <Skeleton className="h-24" />}
      {limits && (
        <div className="flex flex-col gap-4 rounded-3xl border border-border bg-card p-5">
          <p className="font-serif text-[17px] leading-relaxed text-pretty">{limitsText(limits)}</p>
          {bar && (
            <div className="flex flex-col gap-2">
              <span className="text-[13px] text-muted-foreground">
                Automatic diagnoses in the last 24 hours: {bar.used} of {bar.max}
              </span>
              <div
                role="progressbar"
                aria-label="Automatic diagnoses in the last 24 hours"
                aria-valuemin={0}
                aria-valuemax={bar.max}
                aria-valuenow={Math.min(bar.used, bar.max)}
                aria-valuetext={`${bar.used} of ${bar.max}`}
                className="h-2 overflow-hidden rounded-full bg-input"
              >
                <div className={cn('h-full rounded-full', bar.full ? 'bg-destructive' : 'bg-primary')} style={{ width: `${Math.round(bar.ratio * 100)}%` }} />
              </div>
            </div>
          )}
          <span className="text-[13px] text-subtle">The server's environment sets these limits. They cannot be changed here.</span>
        </div>
      )}
    </section>
  )
}
