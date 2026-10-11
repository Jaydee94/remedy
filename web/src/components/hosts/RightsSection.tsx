import { api } from '@/api.ts'
import type { Host, PresentedKey } from '@/api.ts'
import { statusView, sudoText } from '@/hosts.ts'
import type { Run } from '@/components/hosts/run'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

interface Props {
  host: Host
  busy: boolean
  run: Run
  onTested: (h: Host, presented: PresentedKey | null) => void
}

/** The test, and what it learned about the account: who it is, its groups, what sudo lets it do. Everything in the report is text from the host. */
export default function RightsSection({ host, busy, run, onTested }: Props) {
  const view = statusView(host.status)
  const report = host.report

  async function test() {
    if (busy) return
    const res = await run(() => api.testHost(host.id))
    if (res) {
      const { presentedKey, ...tested } = res
      onTested(tested, presentedKey ?? null)
    }
  }

  return (
    <div className="flex flex-col gap-4 rounded-3xl border border-border bg-card p-5">
      <div className="flex flex-wrap items-center gap-3">
        <span className={cn('flex items-center gap-2 rounded-2xl px-3 py-1.5 text-[13px] font-semibold', view.soft, view.text)}>
          <span aria-hidden className={cn('size-2 rounded-full', view.dot)} />
          {view.label}
        </span>
        <Button variant="outline" size="sm" className="h-10 aria-disabled:pointer-events-none aria-disabled:opacity-50" aria-disabled={busy} onClick={() => void test()}>
          {busy ? 'Testing…' : 'Test connection'}
        </Button>
      </div>

      {report && (
        <>
          <p className="font-serif text-[17px] leading-relaxed text-pretty break-words">
            I'm <strong className="font-semibold">{report.account}</strong> on {report.system || 'this host'}, in the groups {report.groups.join(', ') || 'none'}.{' '}
            {sudoText(report.sudo)}
          </p>
          {report.warnings.map((w) => (
            <Alert key={w} variant="destructive" role="note">
              <AlertDescription>{w}</AlertDescription>
            </Alert>
          ))}
          {report.sudoRules.length > 0 && (
            <pre className="overflow-x-auto rounded-xl bg-muted px-3 py-2 text-[13px] whitespace-pre-wrap break-words">{report.sudoRules.join('\n')}</pre>
          )}
        </>
      )}
    </div>
  )
}
