import { useCallback, useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { api, ApiError } from './api.ts'
import type { Repo } from './api.ts'
import ConfirmButton from '@/components/ConfirmButton'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'

export default function ReposCard({ connected }: { connected: boolean }) {
  const [repos, setRepos] = useState<Repo[]>([])
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const reload = useCallback(() => {
    api.listRepos().then(setRepos).catch((e: unknown) => setError(e instanceof ApiError ? e.message : String(e)))
  }, [])

  useEffect(() => {
    if (connected) reload()
  }, [connected, reload])

  async function act(action: () => Promise<unknown>) {
    setBusy(true)
    setError('')
    try {
      await action()
      reload()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Request failed')
    } finally {
      setBusy(false)
    }
  }

  function add(e: FormEvent) {
    e.preventDefault()
    void act(async () => {
      await api.addRepo(name.trim())
      setName('')
    })
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Repositories</CardTitle>
        <CardDescription>Remedy watches the enabled repositories for failed checks.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {!connected ? (
          <p className="text-muted-foreground">Connect GitHub first.</p>
        ) : (
          <>
            <form onSubmit={add} className="flex gap-2">
              <Input
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="owner/name"
                aria-label="Repository"
              />
              <Button type="submit" disabled={busy || name.trim() === ''}>
                Add
              </Button>
            </form>

            {repos.length === 0 ? (
              <p className="text-muted-foreground">No repositories yet.</p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Repository</TableHead>
                    <TableHead>Default branch</TableHead>
                    <TableHead>Last poll</TableHead>
                    <TableHead>Enabled</TableHead>
                    <TableHead />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {repos.map((repo) => (
                    <TableRow key={repo.id}>
                      <TableCell>
                        <div className="font-medium">{repo.fullName}</div>
                        {repo.lastError && <div className="text-xs text-destructive">{repo.lastError}</div>}
                      </TableCell>
                      <TableCell>{repo.defaultBranch}</TableCell>
                      <TableCell className="text-muted-foreground">
                        {repo.lastPolledAt ? new Date(repo.lastPolledAt).toLocaleString() : 'never'}
                      </TableCell>
                      <TableCell>
                        <Switch
                          checked={repo.enabled}
                          disabled={busy}
                          aria-label={`Watch ${repo.fullName}`}
                          onCheckedChange={(enabled) => void act(() => api.setRepoEnabled(repo.id, enabled))}
                        />
                      </TableCell>
                      <TableCell className="text-right">
                        <ConfirmButton
                          label="Remove"
                          confirmLabel="Confirm remove"
                          disabled={busy}
                          onConfirm={() => void act(() => api.deleteRepo(repo.id))}
                        />
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </>
        )}

        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
      </CardContent>
    </Card>
  )
}
