import { ArrowUp } from 'lucide-react'
import { useState } from 'react'
import type { FormEvent } from 'react'
import { ApiError } from '@/api.ts'
import { cn } from '@/lib/utils'

interface Props {
  placeholder: string
  /** Sends the text. It rejects with an ApiError when the server refuses; the message shows under the field. */
  onSend: (text: string) => Promise<void>
}

/** The field at the bottom of a conversation. */
export default function Composer({ placeholder, onSend }: Props) {
  const [text, setText] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const ready = text.trim() !== '' && !busy

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (!ready) return
    setBusy(true)
    setError('')
    try {
      await onSend(text.trim())
      setText('')
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not send that')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="mt-auto flex flex-col gap-2">
      <form onSubmit={(e) => void submit(e)} className="flex items-center gap-2.5 rounded-[28px] border border-field bg-card py-1.5 pr-1.5 pl-5 focus-within:border-ring focus-within:ring-3 focus-within:ring-ring/50">
        <input
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={placeholder}
          aria-label={placeholder}
          maxLength={4000}
          readOnly={busy}
          className="h-10 min-w-0 flex-1 border-0 bg-transparent text-base text-foreground md:text-sm outline-none placeholder:text-subtle"
        />
        <button
          type="submit"
          aria-disabled={!ready}
          aria-label="Send"
          className={cn(
            'flex size-10 shrink-0 items-center justify-center rounded-full text-primary-foreground outline-none focus-visible:ring-3 focus-visible:ring-ring/50',
            ready ? 'bg-primary' : 'bg-input',
          )}
        >
          <ArrowUp className="size-4.5" aria-hidden />
        </button>
      </form>
      {error && (
        <span role="alert" className="pl-5 text-[13px] text-destructive">
          {error}
        </span>
      )}
    </div>
  )
}
