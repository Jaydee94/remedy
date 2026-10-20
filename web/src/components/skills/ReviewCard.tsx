import { useState } from 'react'
import { evidenceSummary, skillProblem } from '@/skills.ts'
import type { ReviewItem } from '@/skills.ts'
import ConfirmButton from '@/components/ConfirmButton'
import { ariaDisabled } from '@/components/setup/ariaDisabled.ts'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'

interface Props {
  item: ReviewItem
  /** Why Confirm cannot work now (the scope is full, the name is taken), or ''. */
  blocked: string
  busy: boolean
  onConfirm: (edit?: { description: string; body: string }) => void
  onReject: () => void
}

const text = 'rounded-xl border border-border bg-background px-3 py-2 font-mono text-[13px] whitespace-pre-wrap break-words'

/** One proposal that waits for the maintainer: what it says as text, the commands behind it, what it replaces, and the three answers. */
export default function ReviewCard({ item, blocked, busy, onConfirm, onReject }: Props) {
  const { skill } = item
  const [editing, setEditing] = useState(false)
  const [description, setDescription] = useState(skill.description)
  const [body, setBody] = useState(skill.body)
  const problem = editing ? skillProblem({ name: skill.name, description, body }) : ''
  const summary = evidenceSummary(item)

  return (
    <li className="flex flex-col gap-3 rounded-3xl border border-border bg-card p-5">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <strong className="font-mono text-[15px] break-all">{skill.name}</strong>
        <span className="text-[13px] text-muted-foreground">{item.scope}</span>
        {item.replaces && <span className="rounded-2xl bg-muted px-2.5 py-0.5 text-xs font-semibold text-muted-foreground">Replaces a skill</span>}
      </div>

      {editing ? (
        <div className="flex flex-col gap-3">
          <label className="flex flex-col gap-1.5">
            <span className="text-[13px] font-semibold text-muted-foreground">Description</span>
            <Input value={description} onChange={(e) => setDescription(e.target.value)} className="h-11 text-base md:text-sm" autoComplete="off" />
          </label>
          <label className="flex flex-col gap-1.5">
            <span className="text-[13px] font-semibold text-muted-foreground">Note</span>
            <textarea
              value={body}
              onChange={(e) => setBody(e.target.value)}
              rows={8}
              className="min-h-40 rounded-xl border border-input bg-transparent px-3 py-2 font-mono text-base outline-none focus-visible:ring-3 focus-visible:ring-ring/50 md:text-sm"
            />
          </label>
          {problem && (
            <p role="alert" className="text-[13px] text-destructive">
              {problem}
            </p>
          )}
        </div>
      ) : item.replaces ? (
        <div className="grid gap-3 md:grid-cols-2">
          <div className="flex min-w-0 flex-col gap-1.5">
            <h3 className="text-xs font-semibold text-muted-foreground">Now</h3>
            <p className="break-words">{item.replaces.description}</p>
            <pre className={text}>{item.replaces.body}</pre>
          </div>
          <div className="flex min-w-0 flex-col gap-1.5">
            <h3 className="text-xs font-semibold text-muted-foreground">Proposed</h3>
            <p className="break-words">{skill.description}</p>
            <pre className={text}>{skill.body}</pre>
          </div>
        </div>
      ) : (
        <div className="flex flex-col gap-1.5">
          <p className="break-words">{skill.description}</p>
          <pre className={text}>{skill.body}</pre>
        </div>
      )}

      <div className="flex flex-col gap-1.5">
        <h3 className="text-xs font-semibold text-muted-foreground">The commands behind these facts</h3>
        {item.evidence.length > 0 && (
          <ul aria-label="Commands behind these facts" className="flex flex-col gap-1">
            {item.evidence.map((line, i) => (
              <li key={`${i}:${line.text}`} className="flex flex-wrap items-center gap-2">
                <code className="font-mono text-[13px] break-all">{line.text}</code>
                {!line.ran && <span className="rounded-2xl bg-soft-open px-2.5 py-0.5 text-xs font-semibold text-destructive">not run</span>}
              </li>
            ))}
          </ul>
        )}
        {summary && <p className="text-[13px] text-muted-foreground">{summary}</p>}
      </div>

      {blocked && <p className="text-[13px] text-destructive">{blocked}</p>}

      <div className="flex flex-wrap gap-2">
        {!editing && (
          <Button
            type="button"
            aria-disabled={busy || blocked !== ''}
            className={cn('h-10', ariaDisabled)}
            onClick={() => {
              if (!busy && blocked === '') onConfirm()
            }}
          >
            Confirm
          </Button>
        )}
        {editing ? (
          <>
            <Button
              type="button"
              aria-disabled={busy || problem !== '' || blocked !== ''}
              className={cn('h-10', ariaDisabled)}
              onClick={() => {
                if (!busy && problem === '' && blocked === '') onConfirm({ description: description.trim(), body })
              }}
            >
              Save and confirm
            </Button>
            <Button type="button" variant="ghost" className="h-10" onClick={() => setEditing(false)}>
              Cancel
            </Button>
          </>
        ) : (
          <Button type="button" variant="outline" className="h-10" onClick={() => setEditing(true)}>
            Edit and confirm
          </Button>
        )}
        <ConfirmButton label="Reject" confirmLabel="Confirm: reject" className="h-10" disabled={busy} onConfirm={onReject} />
      </div>
    </li>
  )
}
