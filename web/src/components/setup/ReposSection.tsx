import { useCallback, useEffect, useId, useRef, useState } from 'react'
import type { FormEvent } from 'react'
import { api, ApiError } from '@/api.ts'
import type { Repo } from '@/api.ts'
import { repoSubline, validRepoName } from '@/setup.ts'
import ConfirmButton from '@/components/ConfirmButton'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { useClock } from '@/useClock.ts'

/** The repositories Remedy watches for failed checks. Without a GitHub connection there is nothing to list. */
export default function ReposSection({ connected }: { connected: boolean }) {
  const [repos, setRepos] = useState<Repo[] | null>(null)
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [loadError, setLoadError] = useState('')
  const [formError, setFormError] = useState('')
  const now = useClock()
  const formErrorId = useId()
  const nameRef = useRef<HTMLInputElement>(null)

  const reload = useCallback(
    () =>
      api
        .listRepos()
        .then((list) => {
          setRepos(list)
          setLoadError('')
        })
        .catch((e: unknown) => setLoadError(e instanceof ApiError ? e.message : 'Could not load the repositories')),
    [],
  )

  useEffect(() => {
    if (connected) void reload()
  }, [connected, reload])

  async function act(action: () => Promise<unknown>) {
    if (busy) return
    setBusy(true)
    setError('')
    try {
      await action()
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Request failed')
    } finally {
      // Also after a failed action: the page then shows what is really there (a repository another tab removed, a switch that did not flip).
      await reload()
      setBusy(false)
    }
  }

  function add(e: FormEvent) {
    e.preventDefault()
    if (busy) return
    if (!validRepoName(name)) {
      setFormError('Use owner/name, for example jaydee94/homelab.')
      return
    }
    setFormError('')
    void act(async () => {
      await api.addRepo(name.trim())
      setName('')
      // The Add button is disabled again with the empty field and would drop the focus: keep it in the field for the next repository.
      requestAnimationFrame(() => nameRef.current?.focus())
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
                  ref={nameRef}
                  value={name}
                  onChange={(e) => {
                    setName(e.target.value)
                    setFormError('')
                  }}
                  placeholder="owner/name"
                  aria-label="Repository to add"
                  aria-invalid={formError !== ''}
                  aria-describedby={formError ? formErrorId : undefined}
                  autoComplete="off"
                  className="h-11 text-base md:text-sm"
                />
                <Button
                  type="submit"
                  disabled={name.trim() === ''}
                  aria-disabled={busy}
                  className="h-11 aria-disabled:pointer-events-none aria-disabled:opacity-50"
                >
                  Add
                </Button>
              </div>
              {formError && (
                <span id={formErrorId} role="alert" className="text-[13px] text-destructive">
                  {formError}
                </span>
              )}
            </form>

            {repos === null ? (
              loadError ? null : <Skeleton className="h-16" />
            ) : repos.length === 0 ? (
              <p className="text-[13px] text-muted-foreground">No repositories yet. Add one above and I'll start watching it.</p>
            ) : (
              <ul className="flex flex-col divide-y divide-border">
                {repos.map((repo) => (
                  <li key={repo.id} className="flex items-center gap-3 py-3.5 first:pt-0 last:pb-0">
                    <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                      <span className="font-mono text-[13px] break-all">{repo.fullName}</span>
                      <span className="text-xs text-muted-foreground">{repoSubline(repo, now)}</span>
                      {repo.lastError && <span className="text-xs break-words text-destructive">{repo.lastError}</span>}
                    </div>
                    <Switch
                      checked={repo.enabled}
                      aria-disabled={busy}
                      className="aria-disabled:cursor-not-allowed aria-disabled:opacity-50"
                      aria-label={`Watch ${repo.fullName}`}
                      onCheckedChange={(enabled) => {
                        if (busy) return
                        void act(() => api.setRepoEnabled(repo.id, enabled))
                      }}
                    />
                    <ConfirmButton
                      label="Remove"
                      confirmLabel="Confirm remove"
                      className="h-10"
                      disabled={busy}
                      onConfirm={() => void act(() => api.deleteRepo(repo.id))}
                    />
                  </li>
                ))}
              </ul>
            )}
          </>
        )}

        {(error || loadError) && (
          <Alert variant="destructive">
            <AlertDescription>{error || loadError}</AlertDescription>
          </Alert>
        )}
      </div>
    </section>
  )
}
