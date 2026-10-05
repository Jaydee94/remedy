import { useState } from 'react'
import { api, ApiError } from '@/api.ts'
import type { ToolCall } from '@/api.ts'
import { argumentList } from '@/approvals.ts'
import { askText } from '@/ask.ts'
import { useToast } from '@/toast.ts'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'

interface Props {
  call: ToolCall
  /** compact: in a thread or a run, the reason opens on demand. large: on Needs you, the reason field is always there. */
  variant?: 'compact' | 'large'
  /** Called after every decision, whatever happened: the state may have changed under the page. */
  onChanged: () => void
}

/**
 * A call that waits for a decision. The question is Remedy's sentence (a fixed table); under it the arguments of the call exactly as
 * stored: what is shown is what runs when it is approved. The values are the agent's text and are only shown.
 */
function AskBody({ call, variant = 'compact', onChanged }: Props) {
  const toast = useToast()
  const [reason, setReason] = useState('')
  const [reasonOpen, setReasonOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const ask = askText(call)
  const large = variant === 'large'

  async function decide(approve: boolean) {
    if (busy) return
    setBusy(true)
    setError('')
    try {
      await api.decideApproval(call.id, approve, reason.trim() || undefined)
      toast.show(approve ? 'Approved. Running…' : 'Denied. I told the agent.')
      // The buttons stay disabled on success until the parent has refreshed the call: a second click would be a 409.
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Could not record the decision')
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
                onChange={(e) => setReason(e.target.value)}
                className="h-11 bg-background text-sm"
              />
            )}
            {error && (
              <span role="alert" className="text-[13px] text-destructive">
                {error}
              </span>
            )}
            <div className="flex flex-wrap items-center gap-2">
              <Button disabled={busy} onClick={() => void decide(true)} className={large ? 'h-11 flex-1' : undefined}>
                {ask.yes}
              </Button>
              <Button variant="outline" disabled={busy} onClick={() => void decide(false)} className={large ? 'h-11 flex-1' : undefined}>
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
