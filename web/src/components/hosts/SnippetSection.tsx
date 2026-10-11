import { useState } from 'react'
import type { Host } from '@/api.ts'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'

function Block({ title, text }: { title: string; text: string }) {
  const [copied, setCopied] = useState(false)
  async function copy() {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 2000)
    } catch {
      setCopied(false) // no clipboard here: the text is selectable
    }
  }
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between gap-3">
        <h3 className="text-[13px] font-semibold text-muted-foreground">{title}</h3>
        <Button variant="outline" size="sm" className="h-9" onClick={() => void copy()}>
          {copied ? 'Copied' : 'Copy'}
        </Button>
      </div>
      <pre className="overflow-x-auto rounded-xl bg-muted px-3 py-2 text-[13px] whitespace-pre-wrap break-all">{text}</pre>
    </div>
  )
}

/** What to run on the host. I only show it; I never run it. */
export default function SnippetSection({ host }: { host: Host }) {
  const o = host.onboarding
  if (!o) return null
  return (
    <div className="flex flex-col gap-4 rounded-3xl border border-border bg-card p-5">
      <p className="font-serif text-[17px] leading-relaxed text-pretty">
        To set the account up, read this and run it on the host as root. I don't run it for you. It lets me do exactly what these lines say, and nothing else.
      </p>
      {o.error ? (
        <Alert variant="destructive">
          <AlertDescription>{o.error}</AlertDescription>
        </Alert>
      ) : (
        <>
          {o.authorizedKeys && <Block title="authorized_keys" text={o.authorizedKeys} />}
          <Block title="sudoers" text={o.sudoers} />
        </>
      )}
    </div>
  )
}
