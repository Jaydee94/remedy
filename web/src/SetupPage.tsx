import { useEffect, useState } from 'react'
import { api, ApiError } from './api.ts'
import type { GitHubConnection } from './api.ts'
import { useSignOut } from './shellContext.ts'
import GitHubSection from '@/components/setup/GitHubSection'
import LimitsSection from '@/components/setup/LimitsSection'
import ReposSection from '@/components/setup/ReposSection'
import RunnerSection from '@/components/setup/RunnerSection'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'

/** What Remedy watches and how much it does on its own. One column; on a phone "Sign out" is the last thing on the page. */
export default function SetupPage() {
  const signOut = useSignOut()
  const [connection, setConnection] = useState<GitHubConnection | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    api
      .getConnection()
      .then(setConnection)
      .catch((e: unknown) => setError(e instanceof ApiError ? e.message : 'Could not load the setup'))
  }, [])

  return (
    <div className="mx-auto flex max-w-190 flex-col gap-8 px-4 py-5 md:px-10 md:py-12">
      <div className="flex flex-col gap-1">
        <h1 className="font-serif text-[clamp(26px,3vw,34px)] font-normal">Setup</h1>
        <span className="text-muted-foreground">What I watch, and how much I do on my own.</span>
      </div>

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      <RunnerSection />

      {connection ? (
        <>
          <section className="flex flex-col gap-3.5">
            <h2 className="text-xs font-semibold text-muted-foreground">GitHub</h2>
            <GitHubSection connection={connection} onChange={setConnection} />
          </section>
          <ReposSection key={String(connection.connected)} connected={connection.connected} />
        </>
      ) : (
        !error && <Skeleton className="h-48" />
      )}

      <LimitsSection />

      <div className="border-t border-border pt-4 md:hidden">
        <button
          type="button"
          onClick={signOut}
          className="h-11 rounded-full px-1 text-[15px] text-muted-foreground outline-none transition-colors hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
        >
          Sign out
        </button>
      </div>
    </div>
  )
}
