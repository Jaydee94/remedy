import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api, ApiError } from './api.ts'
import type { IncidentDetail } from './api.ts'
import DiagnosisCard from './DiagnosisCard.tsx'
import { conclusionText, externalLinkClass, reasonText, safeUrl, severityText } from './incidents.ts'
import RefLink from './RefLink.tsx'
import SourceBadge from './SourceBadge.tsx'
import StateBadge from './StateBadge.tsx'
import ConfirmButton from '@/components/ConfirmButton'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'

const when = (iso: string) => new Date(iso).toLocaleString()

/** Mount with `key={id}` so that switching incidents resets the state. */
export default function IncidentView({ id }: { id: number }) {
  const [detail, setDetail] = useState<IncidentDetail | null>(null)
  const [missing, setMissing] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    let active = true
    const load = () =>
      api
        .getIncident(id)
        .then((d) => {
          if (!active) return
          setDetail(d)
          setError('')
        })
        .catch((e: unknown) => {
          if (!active) return
          if (e instanceof ApiError && e.status === 404) setMissing(true)
          else setError(e instanceof ApiError ? e.message : 'Could not load the incident')
        })
    void load()
    const timer = setInterval(() => void load(), 5000)
    return () => {
      active = false
      clearInterval(timer)
    }
  }, [id])

  async function reload() {
    try {
      setDetail(await api.getIncident(id))
    } catch {
      // The next poll tries again.
    }
  }

  async function ignore() {
    setBusy(true)
    setError('')
    try {
      await api.ignoreIncident(id)
      setDetail(await api.getIncident(id))
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not ignore the incident')
    } finally {
      setBusy(false)
    }
  }

  if (missing) {
    return (
      <div className="flex flex-col gap-2">
        <h1 className="text-2xl font-semibold tracking-tight">Incident not found</h1>
        <Link to="/incidents" className="text-sm text-muted-foreground hover:text-foreground">
          Back to incidents
        </Link>
      </div>
    )
  }

  const incident = detail?.incident
  const checkHref = safeUrl(incident?.checkUrl)
  const canIgnore = incident && ['open', 'diagnosing', 'diagnosed'].includes(incident.state)
  const fromGitHub = incident?.source === 'github'
  const signal = incident?.details && Object.keys(incident.details).length > 0 ? JSON.stringify(incident.details, null, 2) : ''

  return (
    <div className="flex flex-col gap-6">
      <Link to="/incidents" className="text-sm text-muted-foreground hover:text-foreground">
        ← All incidents
      </Link>

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {!detail || !incident ? (
        !error && <Skeleton className="h-48 w-full" />
      ) : (
        <>
          <header className="flex flex-col gap-2">
            <div className="flex flex-wrap items-center gap-3">
              <h1 className="text-2xl font-semibold tracking-tight break-all">{incident.title}</h1>
              <StateBadge state={incident.state} />
            </div>
            <p className="flex flex-wrap items-center gap-x-2 text-muted-foreground">
              {fromGitHub ? (
                <>
                  <span>{incident.repo}</span>
                  <span aria-hidden>·</span>
                  <RefLink refName={incident.ref} url={incident.refUrl} />
                </>
              ) : (
                <>
                  <SourceBadge source={incident.source} />
                  {severityText(incident.severity) && <span>{severityText(incident.severity)}</span>}
                </>
              )}
              {checkHref && (
                <>
                  <span aria-hidden>·</span>
                  <a href={checkHref} target="_blank" rel="noreferrer" className={externalLinkClass}>
                    {fromGitHub ? 'Open the check run' : 'Open the source'}
                  </a>
                </>
              )}
            </p>
          </header>

          <DiagnosisCard incident={incident} onChanged={reload} />

          <Card>
            <CardHeader>
              <CardTitle>Details</CardTitle>
            </CardHeader>
            <CardContent className="flex flex-col gap-4">
              <dl className="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-2 text-sm">
                <dt className="text-muted-foreground">Conclusion</dt>
                <dd>{conclusionText(incident.conclusion)}</dd>
                {fromGitHub && (
                  <>
                    <dt className="text-muted-foreground">Commit</dt>
                    <dd className="font-mono">{incident.headSha.slice(0, 7)}</dd>
                  </>
                )}
                <dt className="text-muted-foreground">Occurrences</dt>
                <dd>{incident.occurrences}</dd>
                <dt className="text-muted-foreground">First seen</dt>
                <dd>{when(incident.firstSeen)}</dd>
                <dt className="text-muted-foreground">Last seen</dt>
                <dd>{when(incident.lastSeen)}</dd>
                {incident.resolvedAt && (
                  <>
                    <dt className="text-muted-foreground">Resolved</dt>
                    <dd>
                      {when(incident.resolvedAt)} ({reasonText(incident.resolvedReason)})
                    </dd>
                  </>
                )}
              </dl>
              {canIgnore && (
                <div>
                  <ConfirmButton
                    label="Ignore"
                    confirmLabel="Confirm ignore"
                    disabled={busy}
                    onConfirm={() => void ignore()}
                  />
                </div>
              )}
            </CardContent>
          </Card>

          {signal && (
            <Card>
              <CardHeader>
                <CardTitle>Signal</CardTitle>
              </CardHeader>
              <CardContent>
                <pre className="max-h-96 overflow-auto font-mono text-xs break-words whitespace-pre-wrap">{signal}</pre>
              </CardContent>
            </Card>
          )}

          <Card>
            <CardHeader>
              <CardTitle>History</CardTitle>
            </CardHeader>
            <CardContent>
              {detail.activity.length === 0 && <p className="text-sm text-muted-foreground">No history yet.</p>}
              <ol className="flex flex-col gap-3">
                {detail.activity.map((a) => (
                  <li key={a.id} className="flex flex-col gap-0.5">
                    <span className="break-words whitespace-pre-wrap">{a.summary}</span>
                    <span className="text-xs text-muted-foreground">{when(a.at)}</span>
                  </li>
                ))}
              </ol>
            </CardContent>
          </Card>
        </>
      )}
    </div>
  )
}
