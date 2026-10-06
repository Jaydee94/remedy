import { useRef, useState } from 'react'
import type { FormEvent } from 'react'
import { api, ApiError } from '@/api.ts'
import type { GitHubConnection } from '@/api.ts'
import { timeAgo } from '@/incidents.ts'
import { connectionView, readingIntro } from '@/setup.ts'
import { useNow } from '@/useNow.ts'
import ConfirmButton from '@/components/ConfirmButton'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'

interface Props {
  connection: GitHubConnection
  onChange: (c: GitHubConnection) => void
}

/**
 * How Remedy sees GitHub. The token is write-only: it is never shown, stored or logged, and only its hint comes back. The field is cleared
 * after a successful request and on Cancel; after a failed one it keeps the text so that a typo can be corrected.
 */
export default function GitHubSection({ connection, onChange }: Props) {
  const [token, setToken] = useState('')
  const [editing, setEditing] = useState(false)
  const [busy, setBusy] = useState(false)
  const [checking, setChecking] = useState(false)
  const [error, setError] = useState('')
  const now = useNow(60_000).getTime()
  // Set when the form closes; the next render of "Replace token" takes the focus, so a keyboard user does not lose their place.
  const returnFocus = useRef(false)

  const showForm = !connection.connected || editing
  const view = connection.connected && connection.status ? connectionView(connection.status) : null

  /** Another tab disconnected GitHub: the connection this page shows is gone, so show that instead of an error. */
  function gone(e: unknown): boolean {
    if (!(e instanceof ApiError) || e.status !== 404) return false
    onChange({ connected: false })
    setError('')
    return true
  }

  async function run(action: () => Promise<GitHubConnection>, notFoundMeansGone = false) {
    setBusy(true)
    setError('')
    try {
      onChange(await action())
      setToken('')
      if (editing) returnFocus.current = true
      setEditing(false)
    } catch (e) {
      if (!(notFoundMeansGone && gone(e))) setError(e instanceof ApiError ? e.message : 'Request failed')
    } finally {
      setBusy(false)
    }
  }

  function save(e: FormEvent) {
    e.preventDefault()
    if (busy || token.trim() === '') return
    void run(() => api.putConnection(token.trim()))
  }

  async function check() {
    setChecking(true)
    try {
      await run(api.checkConnection, true)
    } finally {
      setChecking(false)
    }
  }

  async function disconnect() {
    setBusy(true)
    setError('')
    try {
      await api.deleteConnection()
      onChange({ connected: false })
    } catch (e) {
      if (!gone(e)) setError(e instanceof ApiError ? e.message : 'Request failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex flex-col gap-4 rounded-3xl border border-border bg-card p-5">
      {connection.connected ? (
        <>
          <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
            {view && (
              <span className={cn('flex items-center gap-2 rounded-2xl px-3 py-1.5 text-[13px] font-semibold', view.soft, view.text)}>
                <span aria-hidden className={cn('size-2 rounded-full', view.dot)} />
                {view.label}
              </span>
            )}
            {connection.checkedAt && <span className="text-[13px] text-muted-foreground">checked {timeAgo(connection.checkedAt, now)}</span>}
          </div>
          <p className="font-serif text-[17px] leading-relaxed text-pretty break-words">
            {readingIntro(connection.status)} <strong className="font-semibold">@{connection.login}</strong> with token {connection.tokenHint}. It's stored
            encrypted and never shown again.
          </p>
          {connection.statusDetail && (
            <Alert variant="destructive" role="note">
              <AlertDescription>{connection.statusDetail}</AlertDescription>
            </Alert>
          )}
        </>
      ) : (
        <p className="font-serif text-[17px] leading-relaxed text-pretty">I can't see GitHub yet. Paste a fine-grained token and I'll start watching.</p>
      )}

      {showForm && (
        <form onSubmit={save} className="flex flex-col gap-3">
          <Input
            type="password"
            autoComplete="new-password"
            data-1p-ignore
            data-lpignore="true"
            data-bwignore
            autoFocus={connection.connected}
            value={token}
            onChange={(e) => setToken(e.target.value)}
            placeholder="github_pat_…"
            aria-label="GitHub token"
            className="h-11 text-base md:text-sm"
          />
          <p className="text-[13px] text-muted-foreground">
            Use a fine-grained personal access token with read-only access to the repositories: Metadata, Contents, Pull requests, Actions and Checks.
          </p>
          <div className="flex gap-2">
            <Button type="submit" disabled={busy || token.trim() === ''} className="h-11">
              {connection.connected ? 'Replace token' : 'Connect'}
            </Button>
            {editing && (
              <Button
                type="button"
                variant="ghost"
                className="h-11"
                onClick={() => {
                  returnFocus.current = true
                  setEditing(false)
                  setToken('')
                  setError('')
                }}
              >
                Cancel
              </Button>
            )}
          </div>
        </form>
      )}

      {connection.connected && !editing && (
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" className="h-10" disabled={busy} onClick={() => void check()}>
            {checking ? 'Checking…' : 'Check connection'}
          </Button>
          <Button
            ref={(el) => {
              if (el && returnFocus.current) {
                returnFocus.current = false
                el.focus()
              }
            }}
            variant="outline"
            size="sm"
            className="h-10"
            disabled={busy}
            onClick={() => {
              setError('')
              setEditing(true)
            }}
          >
            Replace token
          </Button>
          <ConfirmButton
            label="Disconnect"
            confirmLabel="Confirm: also removes the repositories"
            className="h-10"
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
    </div>
  )
}
