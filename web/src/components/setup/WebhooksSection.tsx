import { useCallback, useEffect, useId, useRef, useState } from 'react'
import type { FormEvent } from 'react'
import { api, ApiError } from '@/api.ts'
import type { WebhookList } from '@/api.ts'
import { curlExample, endpointLine, lastUsedText, listeningNote, tokenNotice, validWebhookName } from '@/webhooks.ts'
import ConfirmButton from '@/components/ConfirmButton'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { useClock } from '@/useClock.ts'

/** A token that was just made. It lives in this component's memory only and is dropped when the person says they copied it. */
function TokenCard({ name, token, onDone }: { name: string; token: string; onDone: () => void }) {
  const [copied, setCopied] = useState(false)
  async function copy() {
    try {
      await navigator.clipboard.writeText(token)
      setCopied(true)
    } catch {
      setCopied(false) // no clipboard here: the text is selectable
    }
  }
  return (
    <div role="group" aria-label={`Token of ${name}`} className="flex flex-col gap-2 rounded-2xl border border-border bg-muted p-4">
      <span className="text-[13px] font-semibold">Token of {name}</span>
      <pre className="overflow-x-auto font-mono text-[13px] break-all whitespace-pre-wrap">{token}</pre>
      <span className="text-[13px] text-muted-foreground">{tokenNotice}</span>
      <div className="flex gap-2">
        <Button variant="outline" size="sm" className="h-9" onClick={() => void copy()}>
          {copied ? 'Copied' : 'Copy'}
        </Button>
        <Button size="sm" className="h-9" onClick={onDone}>
          I have copied it
        </Button>
      </div>
    </div>
  )
}

/** The endpoints other tools report to. Each has its own token, shown once; the listener itself is switched on outside the page. */
export default function WebhooksSection() {
  const [data, setData] = useState<WebhookList | null>(null)
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [loadError, setLoadError] = useState('')
  const [formError, setFormError] = useState('')
  const [fresh, setFresh] = useState<{ name: string; token: string } | null>(null)
  const now = useClock()
  const formErrorId = useId()
  const nameRef = useRef<HTMLInputElement>(null)

  const reload = useCallback(
    () =>
      api
        .listWebhooks()
        .then((d) => {
          setData(d)
          setLoadError('')
        })
        .catch((e: unknown) => setLoadError(e instanceof ApiError ? e.message : 'Could not load the webhook endpoints')),
    [],
  )

  useEffect(() => {
    void reload()
  }, [reload])

  async function act(action: () => Promise<unknown>) {
    if (busy) return
    setBusy(true)
    setError('')
    try {
      await action()
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Request failed')
    } finally {
      // Also after a failed action: the page then shows what is really there.
      await reload()
      setBusy(false)
    }
  }

  function add(e: FormEvent) {
    e.preventDefault()
    if (busy) return
    if (!validWebhookName(name.trim())) {
      setFormError('Use 1 to 40 lower-case letters, digits or dashes, for example grafana.')
      return
    }
    setFormError('')
    void act(async () => {
      const created = await api.createWebhook({ name: name.trim() })
      setFresh({ name: created.endpoint.name, token: created.token })
      setName('')
    })
  }

  const endpoints = data?.endpoints ?? []

  return (
    <section className="flex flex-col gap-3.5">
      <h2 className="text-xs font-semibold text-muted-foreground">Webhooks</h2>
      <div className="flex flex-col gap-4 rounded-3xl border border-border bg-card p-5">
        <p className="text-[13px] text-muted-foreground">
          A tool that can send an HTTP request can report an incident to an endpoint. Each endpoint has a token of its own.
        </p>
        {data && listeningNote(data.listening) && (
          <Alert>
            <AlertDescription>{listeningNote(data.listening)}</AlertDescription>
          </Alert>
        )}

        <form onSubmit={add} className="flex flex-col gap-2">
          <div className="flex gap-2">
            <Input
              ref={nameRef}
              value={name}
              onChange={(e) => {
                setName(e.target.value)
                setFormError('')
              }}
              placeholder="name, for example grafana"
              aria-label="Name of the endpoint to add"
              aria-invalid={formError !== ''}
              aria-describedby={formError ? formErrorId : undefined}
              autoComplete="off"
              className="h-11 text-base md:text-sm"
            />
            <Button type="submit" disabled={name.trim() === ''} aria-disabled={busy} className="h-11 aria-disabled:pointer-events-none aria-disabled:opacity-50">
              Add
            </Button>
          </div>
          {formError && (
            <span id={formErrorId} role="alert" className="text-[13px] text-destructive">
              {formError}
            </span>
          )}
        </form>

        {fresh && <TokenCard name={fresh.name} token={fresh.token} onDone={() => setFresh(null)} />}

        {data === null ? (
          loadError ? null : <Skeleton className="h-16" />
        ) : endpoints.length === 0 ? (
          <p className="text-[13px] text-muted-foreground">No endpoints yet. Add one above to get a token.</p>
        ) : (
          <ul className="flex flex-col divide-y divide-border">
            {endpoints.map((e) => (
              <li key={e.id} className="flex flex-col gap-2 py-3.5 first:pt-0 last:pb-0">
                <div className="flex items-center gap-3">
                  <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                    <span className="font-mono text-[13px] break-all">{e.name}</span>
                    <span className="text-xs text-muted-foreground">{endpointLine(e)}</span>
                    <span className="text-xs text-muted-foreground">{lastUsedText(e, now)}</span>
                    {e.previousValidUntil && new Date(e.previousValidUntil).getTime() > now && (
                      <span className="text-xs text-muted-foreground">The previous token still works for a while after the rotation.</span>
                    )}
                  </div>
                  <Switch
                    checked={e.enabled}
                    aria-disabled={busy}
                    className="aria-disabled:cursor-not-allowed aria-disabled:opacity-50"
                    aria-label={`Accept events at ${e.name}`}
                    onCheckedChange={(enabled) => {
                      if (busy) return
                      void act(() => api.patchWebhook(e.id, { enabled }))
                    }}
                  />
                </div>
                <div className="flex flex-wrap items-center gap-3">
                  <label className="flex items-center gap-2 text-[13px]">
                    <Switch
                      checked={e.autoDiagnose}
                      aria-disabled={busy}
                      className="aria-disabled:cursor-not-allowed aria-disabled:opacity-50"
                      aria-label={`Diagnose events of ${e.name} on my own`}
                      onCheckedChange={(autoDiagnose) => {
                        if (busy) return
                        void act(() => api.patchWebhook(e.id, { autoDiagnose }))
                      }}
                    />
                    Diagnose on my own
                  </label>
                  <ConfirmButton
                    label="Rotate token"
                    confirmLabel="Confirm rotate"
                    className="h-10"
                    disabled={busy}
                    onConfirm={() =>
                      void act(async () => {
                        const r = await api.rotateWebhook(e.id)
                        setFresh({ name: r.endpoint.name, token: r.token })
                      })
                    }
                  />
                  <ConfirmButton
                    label="Remove"
                    confirmLabel="Confirm remove"
                    className="h-10"
                    disabled={busy}
                    onConfirm={() => void act(() => api.deleteWebhook(e.id))}
                  />
                </div>
              </li>
            ))}
          </ul>
        )}

        {endpoints.length > 0 && (
          <div className="flex flex-col gap-1.5">
            <span className="text-[13px] font-semibold text-muted-foreground">A request from the sender</span>
            <pre className="overflow-x-auto rounded-xl bg-muted px-3 py-2 text-[13px] whitespace-pre-wrap break-all">{curlExample(endpoints[0].name, '')}</pre>
          </div>
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
