import { useRef, useState } from 'react'
import type { FormEvent } from 'react'
import type { Setting } from '@/api.ts'
import { secretLine, sourceBadge } from '@/settings.ts'
import ConfirmButton from '@/components/ConfirmButton'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ariaDisabled } from '@/components/setup/ariaDisabled.ts'
import { cn } from '@/lib/utils'

interface Props {
  setting: Setting
  busy: boolean
  /** Each callback answers with the message to show under the field, or an empty text when it worked (or did not run). */
  onSave: (value: string) => Promise<string>
  onRemove: () => Promise<string>
}

/**
 * The row of a secret setting. The token is write-only: the field is uncontrolled, so React never mirrors it (not into state, not into the
 * DOM attribute), it is read from the ref when the form is sent and emptied as soon as the request starts, also when it fails. Nothing of it
 * goes into storage or a URL. The row is never remounted after a change, so Save keeps the focus; Remove unmounts with the stored token,
 * and the focus goes to the field then.
 */
export default function SecretRow({ setting: s, busy, onSave, onRemove }: Props) {
  const inputRef = useRef<HTMLInputElement>(null)
  const [problem, setProblem] = useState('')
  const id = `setting-${s.key}`

  async function save(e: FormEvent) {
    e.preventDefault()
    const token = inputRef.current?.value.trim() ?? ''
    if (busy || token === '') return
    if (inputRef.current) inputRef.current.value = ''
    setProblem('')
    setProblem(await onSave(token))
  }

  async function remove() {
    if (busy) return
    setProblem('')
    const message = await onRemove()
    setProblem(message)
    // The Remove button is gone once no token is stored: the focus goes to the field, not to the page.
    if (message === '') inputRef.current?.focus()
  }

  return (
    <form onSubmit={(e) => void save(e)} className="flex flex-col gap-1.5 border-t border-border pt-3 first:border-t-0 first:pt-0" autoComplete="off">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <label htmlFor={id} className="text-[15px] font-semibold">
          {s.label}
        </label>
        <span className="rounded-full bg-muted px-2.5 py-0.5 text-[12px] text-muted-foreground">{sourceBadge(s.source)}</span>
      </div>
      <span className="text-[13px] text-muted-foreground">
        {s.rule} {s.effect}
      </span>
      <p className={cn('text-[13px]', s.problem ? 'text-destructive' : 'text-muted-foreground')}>{secretLine(s)}</p>
      <div className="flex flex-wrap items-center gap-2">
        <Input
          ref={inputRef}
          id={id}
          type="password"
          autoComplete="new-password"
          data-1p-ignore
          data-lpignore="true"
          data-bwignore
          spellCheck={false}
          aria-describedby={problem ? `${id}-problem` : undefined}
          aria-invalid={problem ? true : undefined}
          className="h-11 max-w-64 text-base md:text-sm"
        />
        <Button type="submit" aria-disabled={busy} className={cn('h-10', ariaDisabled)}>
          Save
        </Button>
        {s.source === 'set here' && (
          // Never `disabled`: a disabled button drops the keyboard focus. remove() already does nothing while busy.
          <ConfirmButton label="Remove token" confirmLabel="Confirm: remove the token" className="h-10" onConfirm={() => void remove()} />
        )}
      </div>
      {problem && (
        <span id={`${id}-problem`} role="alert" className="text-[13px] text-destructive">
          {problem}
        </span>
      )}
    </form>
  )
}
