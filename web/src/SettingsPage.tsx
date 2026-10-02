import { useEffect, useState } from 'react'
import { api, ApiError } from './api.ts'
import type { GitHubConnection } from './api.ts'
import GitHubConnectionCard from './GitHubConnectionCard.tsx'
import ReposCard from './ReposCard.tsx'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'

export default function SettingsPage() {
  const [connection, setConnection] = useState<GitHubConnection | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    api
      .getConnection()
      .then(setConnection)
      .catch((e: unknown) => setError(e instanceof ApiError ? e.message : 'Could not load the settings'))
  }, [])

  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-2xl font-semibold tracking-tight">Settings</h1>
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      {connection ? (
        <>
          <GitHubConnectionCard connection={connection} onChange={setConnection} />
          <ReposCard connected={connection.connected} />
        </>
      ) : (
        !error && <Skeleton className="h-48 w-full" />
      )}
    </div>
  )
}
