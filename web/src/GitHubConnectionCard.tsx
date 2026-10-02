import { useState } from 'react'
import type { FormEvent } from 'react'
import { api, ApiError } from './api.ts'
import type { GitHubConnection } from './api.ts'
import ConfirmButton from '@/components/ConfirmButton'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'

interface Props {
  connection: GitHubConnection
  onChange: (c: GitHubConnection) => void
}

const statusLabel = { ok: 'Connected', error: 'Error', undecryptable: 'Cannot decrypt' } as const

export default function GitHubConnectionCard({ connection, onChange }: Props) {
  const [token, setToken] = useState('')
  const [editing, setEditing] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const showForm = !connection.connected || editing

  async function run(action: () => Promise<GitHubConnection>) {
    setBusy(true)
    setError('')
    try {
      onChange(await action())
      setToken('')
      setEditing(false)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Request failed')
    } finally {
      setBusy(false)
    }
  }

  function save(e: FormEvent) {
    e.preventDefault()
    void run(() => api.putConnection(token))
  }

  async function disconnect() {
    setBusy(true)
    setError('')
    try {
      await api.deleteConnection()
      onChange({ connected: false })
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Request failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>GitHub connection</CardTitle>
        <CardDescription>
          Remedy reads pull requests and check runs. The token is stored encrypted and is never shown again.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {connection.connected && connection.status && (
          <div className="flex flex-col gap-2">
            <div className="flex flex-wrap items-center gap-3">
              <Badge variant={connection.status === 'ok' ? 'secondary' : 'destructive'}>
                {statusLabel[connection.status]}
              </Badge>
              <span>
                Account <span className="font-medium">@{connection.login}</span>
              </span>
              <span className="text-muted-foreground">Token {connection.tokenHint}</span>
              {connection.checkedAt && (
                <span className="text-sm text-muted-foreground">
                  checked {new Date(connection.checkedAt).toLocaleString()}
                </span>
              )}
            </div>
            {connection.statusDetail && (
              <Alert variant="destructive">
                <AlertDescription>{connection.statusDetail}</AlertDescription>
              </Alert>
            )}
          </div>
        )}

        {showForm && (
          <form onSubmit={save} className="flex flex-col gap-3">
            <Input
              type="password"
              autoComplete="off"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              placeholder="github_pat_..."
              aria-label="GitHub token"
            />
            <p className="text-sm text-muted-foreground">
              Use a fine-grained personal access token with read-only access to the repositories: Metadata,
              Contents, Pull requests, Actions and Checks.
            </p>
            <div className="flex gap-2">
              <Button type="submit" disabled={busy || token.trim() === ''}>
                {connection.connected ? 'Replace token' : 'Connect'}
              </Button>
              {editing && (
                <Button type="button" variant="ghost" onClick={() => setEditing(false)}>
                  Cancel
                </Button>
              )}
            </div>
          </form>
        )}

        {connection.connected && !editing && (
          <div className="flex flex-wrap gap-2">
            <Button variant="outline" size="sm" disabled={busy} onClick={() => void run(api.checkConnection)}>
              Check connection
            </Button>
            <Button variant="outline" size="sm" disabled={busy} onClick={() => setEditing(true)}>
              Replace token
            </Button>
            <ConfirmButton
              label="Disconnect"
              confirmLabel="Confirm: also removes the repositories"
              disabled={busy}
              onConfirm={() => void disconnect()}
            />
          </div>
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
