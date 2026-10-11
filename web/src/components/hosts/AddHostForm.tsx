import { useState } from 'react'
import type { FormEvent } from 'react'
import { useNavigate } from 'react-router'
import { api, ApiError } from '@/api.ts'
import { validAddress, validHostName } from '@/hosts.ts'
import Field from '@/components/hosts/Field'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

/** The two things a host needs to exist. Everything else is on its own page. */
export default function AddHostForm() {
  const navigate = useNavigate()
  const [name, setName] = useState('')
  const [address, setAddress] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const ready = validHostName(name) && validAddress(address)

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (busy || !ready) return
    setBusy(true)
    setError('')
    try {
      const host = await api.createHost({ name: name.trim(), address: address.trim() })
      navigate(`/hosts/${host.id}`)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Request failed')
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-4 rounded-3xl border border-border bg-card p-5">
      <p className="font-serif text-[17px] leading-relaxed text-pretty">Tell me where a machine is. I'll ask for the login on the next page.</p>
      <Field label="Name" hint="Letters, digits, dot, dash and underscore.">
        <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="nas" className="h-11 text-base md:text-sm" autoComplete="off" />
      </Field>
      <Field label="Address" hint="A host name or an IP address.">
        <Input value={address} onChange={(e) => setAddress(e.target.value)} placeholder="192.168.178.118" className="h-11 text-base md:text-sm" autoComplete="off" />
      </Field>
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      <div>
        <Button type="submit" disabled={!ready} aria-disabled={busy} className="h-11 aria-disabled:pointer-events-none aria-disabled:opacity-50">
          Add host
        </Button>
      </div>
    </form>
  )
}
