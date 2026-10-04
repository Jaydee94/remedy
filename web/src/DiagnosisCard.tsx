import { useState } from 'react'
import type { ReactNode } from 'react'
import { Link } from 'react-router'
import { api, ApiError } from './api.ts'
import type { Incident } from './api.ts'
import { categoryText, shortSha } from './incidents.ts'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

const confidenceVariant = { high: 'default', medium: 'secondary', low: 'outline' } as const

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-1">
      <h3 className="text-xs tracking-wide text-muted-foreground uppercase">{title}</h3>
      {children}
    </section>
  )
}

interface Props {
  incident: Incident
  /** Reloads the incident after a diagnosis was started. */
  onChanged: () => Promise<void>
}

export default function DiagnosisCard({ incident, onChanged }: Props) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const d = incident.diagnosis
  const diagnosing = incident.state === 'diagnosing'
  const canDiagnose = incident.state === 'open' || incident.state === 'diagnosed'
  const outdated = d !== undefined && incident.diagnosedSha !== undefined && incident.diagnosedSha !== incident.headSha
  const automatic = ['failure', 'timed_out', 'startup_failure'].includes(incident.conclusion)

  async function diagnose() {
    setBusy(true)
    setError('')
    try {
      await api.diagnoseIncident(incident.id)
      await onChanged()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not start the diagnosis')
    } finally {
      setBusy(false)
    }
  }

  let subtitle = 'Written by an agent that read the failure log and the repository at the failing commit. Check it before you act on it.'
  if (diagnosing) subtitle = 'An agent is reading the failure log and the repository. It can only read; it cannot change anything.'
  else if (!d) {
    subtitle = automatic
      ? 'Remedy diagnoses real failures on its own, within its limits. You can also start it by hand.'
      : 'Cancelled and action-required results are not diagnosed automatically. You can start it by hand.'
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{diagnosing ? 'Diagnosing…' : 'Diagnosis'}</CardTitle>
        <CardDescription>{subtitle}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {outdated && incident.diagnosedSha && (
          <Alert>
            <AlertDescription>
              This diagnosis is about commit {shortSha(incident.diagnosedSha)}. The failing commit is now{' '}
              {shortSha(incident.headSha)}.
            </AlertDescription>
          </Alert>
        )}

        {d && (
          <>
            <div className="flex flex-wrap items-center gap-2">
              <Badge variant={confidenceVariant[d.confidence] ?? 'outline'}>{d.confidence} confidence</Badge>
              <Badge variant="outline">{categoryText(d.category)}</Badge>
              {d.fix_looks_automatable && <Badge variant="secondary">small mechanical fix</Badge>}
            </div>
            <p className="font-medium break-words">{d.summary}</p>
            <Section title="Cause">
              <p className="break-words whitespace-pre-wrap">{d.cause}</p>
            </Section>
            {d.affected_files.length > 0 && (
              <Section title="Affected files">
                <ul className="flex flex-col gap-0.5 font-mono text-sm">
                  {d.affected_files.map((f) => (
                    <li key={f} className="break-all">
                      {f}
                    </li>
                  ))}
                </ul>
              </Section>
            )}
            <Section title="Proposed fix">
              <p className="break-words whitespace-pre-wrap">{d.proposed_fix}</p>
            </Section>
          </>
        )}

        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}

        <div className="flex flex-wrap items-center gap-4">
          {canDiagnose && (
            <Button variant={d ? 'outline' : 'default'} disabled={busy} onClick={() => void diagnose()}>
              {d ? 'Diagnose again' : 'Diagnose'}
            </Button>
          )}
          {incident.runId && (
            <Link to={`/runs/${incident.runId}`} className="text-sm text-muted-foreground hover:text-foreground">
              {diagnosing ? 'Watch the run' : 'Show the run'}
            </Link>
          )}
        </div>
      </CardContent>
    </Card>
  )
}
