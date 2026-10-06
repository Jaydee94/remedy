import { useCallback, useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { api, ApiError } from '@/api.ts'
import type { Repo } from '@/api.ts'
import { repoSubline, validRepoName } from '@/setup.ts'
import ConfirmButton from '@/components/ConfirmButton'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'

/** The repositories Remedy watches for failed checks. Without a GitHub connection there is nothing to list. */
export default function ReposSection({ connected }: { connected: boolean }) {
  const [repos, setRepos] = useState<Repo[] | null>(null)
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [formError, setFormError] = useState('')

  const reload = useCallback(
    () =>
      api
        .listRepos()
        .then(setRepos)
        .catch((e: unknown) => setError(e instanceof ApiError ? e.message : 'Could not load the repositories')),
    [],
  )

  useEffect(() => {
    if (connected) void reload()
  }, [connected, reload])

  async function act(action: () => Promise<unknown>) {
    setBusy(true)
    setError('')
    try {
      await action()
      await reload()
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Request failed')
    } finally {
      setBusy(false)
    }
  }

  function add(e: FormEvent) {
    e.preventDefault()
    if (!validRepoName(name)) {
      setFormError('Use owner/name, for example jaydee94/homelab.')
      return
    }
    setFormError('')
    void act(async () => {
      await api.addRepo(name.trim())
      setName('')
    })
  }

  return (
    <section className="flex flex-col gap-3.5">
      <h2 className="text-xs font-semibold text-muted-foreground">Repositories I watch</h2>
      <div className="flex flex-col gap-4 rounded-3xl border border-border bg-card p-5">
        {!connected ? (
          <p className="text-muted-foreground">Connect GitHub first.</p>
        ) : (
          <>
            <form onSubmit={add} className="flex flex-col gap-2">
              <div className="flex gap-2">
                <Input
                  value={name}
                  onChange={(e) => {
                    setName(e.target.value)
                    setFormError('')
                  }}
                  placeholder="owner/name"
                  aria-label="Repository to add"
                  aria-invalid={formError !== ''}
                  autoComplete="off"
                  className="h-11 text-base md:text-sm"
                />
                <Button type="submit" disabled={busy || name.trim() === ''} className="h-11">
                  Add
                </Button>
              </div>
              {formError && (
                <span role="alert" className="text-[13px] text-destructive">
                  {formError}
                </span>
              )}
            </form>

            {repos === null ? null : repos.length === 0 ? (
              <p className="text-[13px] text-muted-foreground">No repositories yet. Add one above and I'll start watching it.</p>
            ) : (
              <ul className="flex flex-col divide-y divide-border">
                {repos.map((repo) => (
                  <li key={repo.id} className="flex items-center gap-3 py-3.5 first:pt-0 last:pb-0">
                    <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                      <span className="font-mono text-[13px] break-all">{repo.fullName}</span>
                      <span className="text-xs text-muted-foreground">{repoSubline(repo)}</span>
                      {repo.lastError && <span className="text-xs break-words text-destructive">{repo.lastError}</span>}
                    </div>
                    <Switch
                      checked={repo.enabled}
                      disabled={busy}
                      aria-label={`Watch ${repo.fullName}`}
                      onCheckedChange={(enabled) => void act(() => api.setRepoEnabled(repo.id, enabled))}
                    />
                    <ConfirmButton label="Remove" confirmLabel="Confirm remove" disabled={busy} onConfirm={() => void act(() => api.deleteRepo(repo.id))} />
                  </li>
                ))}
              </ul>
            )}
          </>
        )}

        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
      </div>
    </section>
  )
}
