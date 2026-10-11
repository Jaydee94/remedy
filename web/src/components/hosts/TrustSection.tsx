import { api } from '@/api.ts'
import type { Host, PresentedKey } from '@/api.ts'
import { FINGERPRINT_HELP } from '@/hosts.ts'
import ConfirmButton from '@/components/ConfirmButton'
import type { Run } from '@/components/hosts/run'
import { Button } from '@/components/ui/button'

interface Props {
  host: Host
  presented: PresentedKey | null
  busy: boolean
  run: Run
  onHost: (h: Host) => void
  onPresented: (k: PresentedKey | null) => void
}

/** Whether I trust the key the host shows. I pin it only after the maintainer names the fingerprint they compared. */
export default function TrustSection({ host, presented, busy, run, onHost, onPresented }: Props) {
  async function pin() {
    if (busy || !presented) return
    const res = await run(() => api.pinHostKey(host.id, presented.fingerprint))
    if (res) {
      onPresented(null)
      onHost(res)
    }
  }

  async function unpin() {
    const done = await run(async () => {
      await api.unpinHostKey(host.id)
      return api.getHost(host.id)
    })
    if (done) onHost(done)
  }

  return (
    <div className="flex flex-col gap-4 rounded-3xl border border-border bg-card p-5">
      {host.pinnedFingerprint ? (
        <>
          <p className="font-serif text-[17px] leading-relaxed text-pretty">I only log in to a host that shows this key:</p>
          <code className="rounded-xl bg-muted px-3 py-2 text-[13px] break-all">{host.pinnedFingerprint}</code>
          <div>
            <ConfirmButton label="Forget this key" confirmLabel="Confirm: forget the key" className="h-10" disabled={busy} onConfirm={() => void unpin()} />
          </div>
        </>
      ) : presented ? (
        <>
          <p className="font-serif text-[17px] leading-relaxed text-pretty">
            The host shows a key of type <strong className="font-semibold">{presented.type}</strong>. Compare its fingerprint with the host's own before you trust it:
          </p>
          <code className="rounded-xl bg-muted px-3 py-2 text-[13px] break-all">{presented.fingerprint}</code>
          <p className="text-[13px] text-muted-foreground">{FINGERPRINT_HELP}</p>
          <div>
            <Button className="h-11 aria-disabled:pointer-events-none aria-disabled:opacity-50" aria-disabled={busy} onClick={() => void pin()}>
              I checked this fingerprint
            </Button>
          </div>
        </>
      ) : (
        <p className="font-serif text-[17px] leading-relaxed text-pretty">
          I haven't pinned a key for this host. Test the connection, and I'll show you the key it presents. I never send a login to a host whose key I don't know.
        </p>
      )}
    </div>
  )
}
