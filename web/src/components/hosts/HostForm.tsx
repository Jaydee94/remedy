import { useState } from 'react'
import type { FormEvent } from 'react'
import { api } from '@/api.ts'
import type { Host, HostPatched } from '@/api.ts'
import { parseUnits, validAddress, validDockerPattern, validHostName } from '@/hosts.ts'
import Field from '@/components/hosts/Field'
import type { Run } from '@/components/hosts/run'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'

interface Props {
  host: Host
  busy: boolean
  run: Run
  onSaved: (h: Host, pinCleared: boolean) => void
}

/** The fields of a host that the maintainer edits. Nothing here is a secret, so the inputs are ordinary controlled ones. */
export default function HostForm({ host, busy, run, onSaved }: Props) {
  const [f, setF] = useState({
    name: host.name,
    address: host.address,
    port: String(host.port),
    account: host.account,
    notes: host.notes,
    dockerPattern: host.dockerPattern,
    units: host.systemdUnits.join('\n'),
    enabled: host.enabled,
  })
  const set = (k: keyof typeof f) => (v: string | boolean) => setF((prev) => ({ ...prev, [k]: v }))
  const ready = validHostName(f.name) && validAddress(f.address) && validDockerPattern(f.dockerPattern) && Number(f.port) >= 1 && Number(f.port) <= 65535

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (busy || !ready) return
    const res: HostPatched | undefined = await run(() =>
      api.patchHost(host.id, {
        name: f.name.trim(),
        address: f.address.trim(),
        port: Number(f.port),
        account: f.account.trim(),
        notes: f.notes,
        dockerPattern: f.dockerPattern.trim(),
        systemdUnits: parseUnits(f.units),
        enabled: f.enabled,
      }),
    )
    if (res) {
      const { pinCleared, ...saved } = res
      onSaved(saved, pinCleared)
    }
  }

  const input = 'h-11 text-base md:text-sm'
  return (
    <form onSubmit={submit} className="flex flex-col gap-4 rounded-3xl border border-border bg-card p-5">
      <Field label="Name">
        <Input value={f.name} onChange={(e) => set('name')(e.target.value)} className={input} autoComplete="off" />
      </Field>
      <div className="grid grid-cols-[1fr_6.5rem] gap-3">
        <Field label="Address">
          <Input value={f.address} onChange={(e) => set('address')(e.target.value)} className={input} autoComplete="off" />
        </Field>
        <Field label="Port">
          <Input value={f.port} onChange={(e) => set('port')(e.target.value)} inputMode="numeric" className={input} autoComplete="off" />
        </Field>
      </div>
      <Field label="Account" hint="The user I log in as. A dedicated account such as remedy is safer than yours.">
        <Input value={f.account} onChange={(e) => set('account')(e.target.value)} className={input} autoComplete="off" />
      </Field>
      <Field label="Notes" hint="For you. I show them as text.">
        <textarea
          value={f.notes}
          onChange={(e) => set('notes')(e.target.value)}
          rows={3}
          className="min-h-20 rounded-xl border border-input bg-transparent px-3 py-2 text-base outline-none focus-visible:ring-3 focus-visible:ring-ring/50 md:text-sm"
        />
      </Field>
      <Field label="Containers I may start and stop" hint="A name with one star at the end, for example remedy-*. Leave it empty for none. It only shapes the snippet below. In sudoers a star also matches spaces: it limits less than it looks.">
        <Input value={f.dockerPattern} onChange={(e) => set('dockerPattern')(e.target.value)} placeholder="remedy-*" className={input} autoComplete="off" />
      </Field>
      <Field label="systemd units I may start and stop" hint="One per line. It only shapes the snippet below.">
        <textarea
          value={f.units}
          onChange={(e) => set('units')(e.target.value)}
          rows={2}
          className="min-h-16 rounded-xl border border-input bg-transparent px-3 py-2 font-mono text-base outline-none focus-visible:ring-3 focus-visible:ring-ring/50 md:text-sm"
        />
      </Field>
      <label className="flex items-center gap-3">
        <Switch checked={f.enabled} onCheckedChange={(v: boolean) => set('enabled')(v)} />
        <span className="text-[15px]">Remedy may use this host</span>
      </label>
      <div>
        <Button type="submit" disabled={!ready} aria-disabled={busy} className="h-11 aria-disabled:pointer-events-none aria-disabled:opacity-50">
          Save
        </Button>
      </div>
    </form>
  )
}
