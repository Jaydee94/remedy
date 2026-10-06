import { useState } from 'react'
import { api, ApiError } from '@/api.ts'
import type { ToolCall } from '@/api.ts'
import { argumentList, byteLength, limitBytes } from '@/approvals.ts'
import { askText } from '@/ask.ts'
import { useToast } from '@/toast.ts'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'

const BUSY_CLASS = 'aria-disabled:pointer-events-none aria-disabled:opacity-50'

interface Props {
  call: ToolCall
  /** compact: in a thread or a run, the reason opens on demand. large: on Needs you, the reason field is always there. */
  variant?: 'compact' | 'large'
  /** Called after every decision, whatever happened: the state may have changed under the page. */
  onChanged: () => void
  /** Yes and No are locked from outside (the list of questions just changed under the pointer). Default false. */
  disabled?: boolean
}

/**
 * A call that waits for a decision. The question is Remedy's sentence (a fixed table); under it the arguments of the call exactly as
 * stored: what is shown is what runs when it is approved. The values are the agent's text and are only shown.
 */
function AskBody({ call, variant = 'compact', onChanged, disabled = false }: Props) {
  const toast = useToast()
  const [reason, setReason] = useState('')
  const [reasonOpen, setReasonOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const ask = askText(call)
  const large = variant === 'large'

  async function decide(approve: boolean) {
    // The buttons are aria-disabled, not disabled (they keep the focus while the request runs): a click or a key while busy or locked does nothing.
    if (busy || disabled) return
    setBusy(true)
    setError('')
    try {
      await api.decideApproval(call.id, approve, reason.trim() || undefined)
      toast.show(approve ? 'Approved. Running…' : 'Denied. I told the agent.')
      // The buttons stay busy on success until the parent has refreshed the call: a second click would be a 409.
    } catch (e) {
      const message = e instanceof ApiError ? e.message : 'Could not record the decision'
      setError(message)
      toast.show(message) // the ask may leave the list within milliseconds (a 409): the toast outlives it
      setBusy(false)
    } finally {
      onChanged()
    }
  }

  return (
    <div className="flex flex-col gap-3">
      <p className="font-serif text-[clamp(18px,2vw,21px)] leading-normal text-pretty break-words">{ask.question}</p>
      <div className={cn('flex flex-col gap-3.5 rounded-[20px] border bg-card', call.waiting ? 'border-primary' : 'border-border', large ? 'p-4.5' : 'p-4')}>
        <span className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
          <span className="font-mono text-[13px] break-all text-foreground">{call.tool}</span>
          {call.waiting && 'exactly this runs if you approve'}
        </span>
        <dl className="grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-[13px]">
          {argumentList(call.arguments).map(({ name, value }) => (
            <div key={name} className="contents">
              <dt className="break-all text-subtle">{name}</dt>
              <dd className="break-words whitespace-pre-wrap">{value}</dd>
            </div>
          ))}
        </dl>
        {call.waiting ? (
          <>
            {(large || reasonOpen) && (
              <Input
                aria-label="Reason (optional)"
                placeholder="Add a reason (optional)"
                maxLength={500}
                autoFocus={!large}
                value={reason}
                onChange={(e) => {
                  const v = e.target.value
                  // Typing in a full field keeps the old value, so the caret stays; a paste is cut to the limit.
                  if (byteLength(v) > 500 && byteLength(reason) >= 500) return
                  setReason(limitBytes(v, 500))
                }}
                className="h-11 bg-background text-base md:text-sm"
              />
            )}
            {error && (
              <span role="alert" className="text-[13px] text-destructive">
                {error}
              </span>
            )}
            <div className="flex flex-wrap items-center gap-2">
              <Button aria-disabled={busy || disabled} onClick={() => void decide(true)} className={cn(BUSY_CLASS, large && 'h-11 flex-1')}>
                {ask.yes}
              </Button>
              <Button variant="outline" aria-disabled={busy || disabled} onClick={() => void decide(false)} className={cn(BUSY_CLASS, large && 'h-11 flex-1')}>
                No
              </Button>
              {!large && !reasonOpen && (
                <button
                  type="button"
                  onClick={() => setReasonOpen(true)}
                  className="h-10 rounded-full px-2.5 text-[13px] text-muted-foreground outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
                >
                  Add a reason…
                </button>
              )}
            </div>
            {disabled && <p className="text-[13px] text-muted-foreground">Hold on, the list just changed.</p>}
          </>
        ) : (
          <p className="text-[13px] text-muted-foreground">
            The agent is no longer waiting for this call, so it can no longer be decided. It is marked as abandoned shortly.
          </p>
        )}
      </div>
    </div>
  )
}

/** The state (reason, error, busy) belongs to one call: another call gets a fresh body. */
export default function ApprovalAsk(props: Props) {
  return <AskBody key={props.call.id} {...props} />
}
