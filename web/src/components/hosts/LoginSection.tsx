import { useRef, useState } from 'react'
import type { FormEvent } from 'react'
import { api } from '@/api.ts'
import type { Host } from '@/api.ts'
import { credentialText } from '@/hosts.ts'
import ConfirmButton from '@/components/ConfirmButton'
import Field from '@/components/hosts/Field'
import type { Run } from '@/components/hosts/run'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'

interface Props {
  host: Host
  busy: boolean
  run: Run
  onHost: (h: Host) => void
}

/**
 * How I log in. The secrets are write-only: the fields are uncontrolled, so React never mirrors a secret into the DOM, and they
 * are emptied as soon as the request is sent, also when it fails (the page cannot keep what it must not keep). Nothing goes in a
 * URL or in storage.
 */
export default function LoginSection({ host, busy, run, onHost }: Props) {
  const [kind, setKind] = useState<'key' | 'password'>(host.credential?.kind ?? 'key')
  const [editing, setEditing] = useState(!host.credential)
  const keyRef = useRef<HTMLTextAreaElement>(null)
  const passphraseRef = useRef<HTMLInputElement>(null)
  const passwordRef = useRef<HTMLInputElement>(null)

  function clear() {
    for (const r of [keyRef, passphraseRef, passwordRef]) if (r.current) r.current.value = ''
  }

  async function save(e: FormEvent) {
    e.preventDefault()
    if (busy) return
    const body =
      kind === 'key'
        ? ({ kind, key: keyRef.current?.value ?? '', passphrase: passphraseRef.current?.value ?? '' } as const)
        : ({ kind, password: passwordRef.current?.value ?? '' } as const)
    clear()
    const res = await run(() => api.putHostCredential(host.id, body))
    if (res) {
      onHost(res)
      setEditing(false)
    }
  }

  async function remove() {
    const done = await run(async () => {
      await api.deleteHostCredential(host.id)
      return api.getHost(host.id)
    })
    if (done) {
      onHost(done)
      setEditing(true)
    }
  }

  const tab = (k: 'key' | 'password', label: string) => (
    <button
      type="button"
      onClick={() => {
        clear()
        setKind(k)
      }}
      aria-pressed={kind === k}
      className={cn(
        'h-10 rounded-full px-4 text-[14px] outline-none focus-visible:ring-3 focus-visible:ring-ring/50',
        kind === k ? 'bg-primary text-primary-foreground' : 'bg-muted text-muted-foreground',
      )}
    >
      {label}
    </button>
  )

  return (
    <div className="flex flex-col gap-4 rounded-3xl border border-border bg-card p-5">
      {host.credential && (
        <p className="font-serif text-[17px] leading-relaxed text-pretty">
          I log in with <strong className="font-semibold break-all">{credentialText(host.credential)}</strong>. It's stored encrypted and never shown again.
        </p>
      )}

      {editing && (
        <form onSubmit={save} className="flex flex-col gap-3" autoComplete="off">
          <div className="flex gap-2" role="group" aria-label="Kind of login">
            {tab('key', 'Private key')}
            {tab('password', 'Password')}
          </div>
          {kind === 'key' ? (
            <>
              <Field label="Private key" hint="Paste the whole file, including the BEGIN and END lines.">
                <textarea
                  ref={keyRef}
                  rows={6}
                  spellCheck={false}
                  autoComplete="off"
                  data-1p-ignore
                  data-lpignore="true"
                  data-bwignore
                  className="rounded-xl border border-input bg-transparent px-3 py-2 font-mono text-[13px] outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
                />
              </Field>
              <Field label="Passphrase" hint="Only if the key is protected by one.">
                <Input ref={passphraseRef} type="password" autoComplete="new-password" data-1p-ignore data-lpignore="true" data-bwignore className="h-11 text-base md:text-sm" />
              </Field>
            </>
          ) : (
            <Field label="Password">
              <Input ref={passwordRef} type="password" autoComplete="new-password" data-1p-ignore data-lpignore="true" data-bwignore className="h-11 text-base md:text-sm" />
            </Field>
          )}
          <div className="flex gap-2">
            <Button type="submit" aria-disabled={busy} className="h-11 aria-disabled:pointer-events-none aria-disabled:opacity-50">
              {host.credential ? 'Replace login' : 'Save login'}
            </Button>
            {host.credential && (
              <Button
                type="button"
                variant="ghost"
                className="h-11"
                onClick={() => {
                  clear()
                  setEditing(false)
                }}
              >
                Cancel
              </Button>
            )}
          </div>
        </form>
      )}

      {host.credential && !editing && (
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" className="h-10" onClick={() => setEditing(true)}>
            Replace login
          </Button>
          <ConfirmButton label="Remove login" confirmLabel="Confirm: remove the login" className="h-10" disabled={busy} onConfirm={() => void remove()} />
        </div>
      )}
    </div>
  )
}
