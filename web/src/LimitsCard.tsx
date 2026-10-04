import { useEffect, useState } from 'react'
import { api, ApiError } from './api.ts'
import type { Limits } from './api.ts'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'

function duration(seconds: number): string {
  const unit = (n: number, name: string) => `${n} ${name}${n === 1 ? '' : 's'}`
  if (seconds < 60 || seconds % 60 !== 0) return unit(seconds, 'second')
  if (seconds < 3600 || seconds % 3600 !== 0) return unit(seconds / 60, 'minute')
  return unit(seconds / 3600, 'hour')
}

export default function LimitsCard() {
  const [limits, setLimits] = useState<Limits | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    api
      .getLimits()
      .then(setLimits)
      .catch((e: unknown) => setError(e instanceof ApiError ? e.message : 'Could not load the limits'))
  }, [])

  return (
    <Card>
      <CardHeader>
        <CardTitle>Limits</CardTitle>
        <CardDescription>
          What Remedy does on its own, and how much of it. The server's environment sets them; they cannot be changed here.
        </CardDescription>
      </CardHeader>
      <CardContent>
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        {!limits && !error && <Skeleton className="h-24 w-full" />}
        {limits && (
          <dl className="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-2 text-sm">
            <dt className="text-muted-foreground">Polling</dt>
            <dd>Every {duration(limits.pollIntervalSeconds)}</dd>
            <dt className="text-muted-foreground">Automatic diagnosis</dt>
            <dd>
              {limits.diagnoseMaxPerDay === 0
                ? 'Off'
                : `Up to ${limits.diagnoseMaxPerIncident} per incident and ${limits.diagnoseMaxPerDay} per 24 hours, at least ${duration(limits.diagnoseCooldownSeconds)} apart per incident, one run at a time`}
            </dd>
            <dt className="text-muted-foreground">Stuck runs</dt>
            <dd>A run that stays running for {limits.staleRunMinutes} minutes is failed</dd>
          </dl>
        )}
      </CardContent>
    </Card>
  )
}
