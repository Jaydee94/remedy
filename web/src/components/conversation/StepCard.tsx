import type { Step } from '@/runview.ts'
import { cn } from '@/lib/utils'

/** One thing the agent did: in words, and the raw line under it. The raw line is the agent's text and is only shown. */
export default function StepCard({ step }: { step: Step }) {
  return (
    <div className="flex flex-col gap-1.5 rounded-2xl border border-border bg-sidebar px-3.5 py-2.5">
      <span className="flex items-center gap-2 text-[13px] text-muted-foreground">
        <span
          aria-hidden
          className={cn('size-1.5 shrink-0 rounded-full', step.failed ? 'bg-destructive' : step.done ? 'bg-violet' : 'bg-primary')}
        />
        <span className="min-w-0 break-words">{step.label}</span>
      </span>
      <span className="line-clamp-3 font-mono text-xs break-all text-subtle">{step.raw}</span>
    </div>
  )
}
