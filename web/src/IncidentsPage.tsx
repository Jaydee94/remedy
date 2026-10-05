import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api, ApiError } from './api.ts'
import type { Incident, IncidentSource, Repo } from './api.ts'
import { conclusionText, sourceLabels, timeAgo } from './incidents.ts'
import RefLink from './RefLink.tsx'
import SourceBadge from './SourceBadge.tsx'
import StateBadge from './StateBadge.tsx'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'

const selectClass =
  'h-8 rounded-lg border border-input bg-transparent px-2.5 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30'

const stateOptions = [
  { value: 'active', label: 'Active' },
  { value: 'ignored', label: 'Ignored' },
  { value: 'resolved', label: 'Resolved' },
  { value: 'all', label: 'All' },
]

export default function IncidentsPage() {
  const [state, setState] = useState('active')
  const [source, setSource] = useState('')
  const [repoId, setRepoId] = useState('')
  const [incidents, setIncidents] = useState<Incident[] | null>(null)
  const [repos, setRepos] = useState<Repo[]>([])
  const [error, setError] = useState('')

  useEffect(() => {
    api.listRepos().then(setRepos).catch(() => setRepos([]))
  }, [])

  useEffect(() => {
    let active = true
    const load = () =>
      api
        .listIncidents(state, repoId ? Number(repoId) : undefined, source ? (source as IncidentSource) : undefined)
        .then((list) => {
          if (!active) return
          setIncidents(list)
          setError('')
        })
        .catch((e: unknown) => {
          if (active) setError(e instanceof ApiError ? e.message : 'Could not load the incidents')
        })
    void load()
    const timer = setInterval(() => void load(), 5000)
    return () => {
      active = false
      clearInterval(timer)
    }
  }, [state, repoId, source])

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-2xl font-semibold tracking-tight">Incidents</h1>
        <div className="flex flex-wrap gap-2">
          <select aria-label="State" className={selectClass} value={state} onChange={(e) => setState(e.target.value)}>
            {stateOptions.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
          <select aria-label="Source" className={selectClass} value={source} onChange={(e) => setSource(e.target.value)}>
            <option value="">All sources</option>
            {Object.entries(sourceLabels).map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </select>
          <select
            aria-label="Repository"
            className={selectClass}
            value={repoId}
            onChange={(e) => setRepoId(e.target.value)}
          >
            <option value="">All repositories</option>
            {repos.map((r) => (
              <option key={r.id} value={r.id}>
                {r.fullName}
              </option>
            ))}
          </select>
        </div>
      </div>

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {incidents === null ? (
        !error && <Skeleton className="h-40 w-full" />
      ) : incidents.length === 0 ? (
        <p className="text-muted-foreground">
          {state === 'active'
            ? 'No active incidents. Remedy checks the enabled repositories and the signal sources it is configured for regularly.'
            : 'No incidents match.'}
        </p>
      ) : (
        <Card>
          <CardContent>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Incident</TableHead>
                  <TableHead>Source</TableHead>
                  <TableHead>Repository</TableHead>
                  <TableHead>Ref</TableHead>
                  <TableHead>State</TableHead>
                  <TableHead>Conclusion</TableHead>
                  <TableHead>Occurrences</TableHead>
                  <TableHead>Last seen</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {incidents.map((i) => (
                  <TableRow key={i.id}>
                    <TableCell className="font-medium">
                      <Link to={`/incidents/${i.id}`} className="underline-offset-4 hover:underline">
                        {i.title}
                      </Link>
                    </TableCell>
                    <TableCell>
                      <SourceBadge source={i.source} />
                    </TableCell>
                    <TableCell>{i.source === 'github' ? i.repo : <span className="text-muted-foreground">-</span>}</TableCell>
                    <TableCell>
                      {i.source === 'github' ? (
                        <RefLink refName={i.ref} url={i.refUrl} />
                      ) : (
                        <span className="text-muted-foreground">-</span>
                      )}
                    </TableCell>
                    <TableCell>
                      <StateBadge state={i.state} />
                    </TableCell>
                    <TableCell>{conclusionText(i.conclusion)}</TableCell>
                    <TableCell>{i.occurrences}</TableCell>
                    <TableCell className="text-muted-foreground" title={new Date(i.lastSeen).toLocaleString()}>
                      {timeAgo(i.lastSeen)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      )}
    </div>
  )
}
