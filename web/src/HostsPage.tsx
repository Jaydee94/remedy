import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api, ApiError } from './api.ts'
import type { Host } from './api.ts'
import { hostLine, statusView } from './hosts.ts'
import { useClock } from './useClock.ts'
import AddHostForm from '@/components/hosts/AddHostForm'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

/** The machines I may log in to. */
export default function HostsPage() {
  const [hosts, setHosts] = useState<Host[] | null>(null)
  const [error, setError] = useState('')
  const now = useClock()

  useEffect(() => {
    api
      .listHosts()
      .then(setHosts)
      .catch((e: unknown) => setError(e instanceof ApiError ? e.message : 'Could not load the hosts'))
  }, [])

  return (
    <div className="mx-auto flex max-w-190 flex-col gap-8 px-4 py-5 md:px-10 md:py-12">
      <div className="flex flex-col gap-1">
        <h1 className="font-serif text-[clamp(26px,3vw,34px)] font-normal">Hosts</h1>
        <span className="text-muted-foreground">The machines I may log in to, and how.</span>
      </div>

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {hosts === null && !error && <Skeleton className="h-32" />}

      {hosts !== null && hosts.length > 0 && (
        <ul className="flex flex-col gap-3" aria-label="Hosts">
          {hosts.map((h) => {
            const view = statusView(h.status)
            return (
              <li key={h.id}>
                <Link
                  to={`/hosts/${h.id}`}
                  className="flex flex-col gap-1.5 rounded-3xl border border-border bg-card p-5 outline-none transition-colors hover:bg-muted focus-visible:ring-3 focus-visible:ring-ring/50"
                >
                  <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
                    <strong className="text-[17px] font-semibold break-all">{h.name}</strong>
                    <span className={cn('flex items-center gap-2 rounded-2xl px-3 py-1 text-[13px] font-semibold', view.soft, view.text)}>
                      <span aria-hidden className={cn('size-2 rounded-full', view.dot)} />
                      {view.label}
                    </span>
                  </span>
                  <span className="text-[13px] text-muted-foreground break-all">{hostLine(h, now)}</span>
                </Link>
              </li>
            )
          })}
        </ul>
      )}

      {hosts !== null && hosts.length === 0 && (
        <p className="font-serif text-[17px] leading-relaxed text-pretty">
          I don't know any machine yet. Add the ones outside the cluster that I should be able to look at, such as your NAS.
        </p>
      )}

      {hosts !== null && (
        <section className="flex flex-col gap-3.5">
          <h2 className="text-xs font-semibold text-muted-foreground">Add a host</h2>
          <AddHostForm />
        </section>
      )}
    </div>
  )
}
