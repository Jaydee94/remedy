import { useEffect, useRef, useState } from 'react'
import type { Step } from '@/runview.ts'
import { cn } from '@/lib/utils'

/** One thing the agent did: in words, and the raw line under it. The raw line is the agent's text and is only shown. */
export default function StepCard({ step }: { step: Step }) {
  const [expanded, setExpanded] = useState(false)
  /** The raw line did not fit in three lines at some width. It only turns true, so an expanded line keeps its "Show less". */
  const [showToggle, setShowToggle] = useState(false)
  const rawRef = useRef<HTMLSpanElement>(null)

  // The line is clamped to three lines, so what is cut off is measured, not guessed from the length: it depends on the width. The state is
  // set only in the callback of the observer, which also fires once when it starts observing. An expanded line never overflows.
  useEffect(() => {
    const el = rawRef.current
    if (!el) return
    const observer = new ResizeObserver(() => {
      if (el.scrollHeight > el.clientHeight + 1) setShowToggle(true)
    })
    observer.observe(el)
    return () => observer.disconnect()
  }, [step.raw])

  return (
    <div className="flex flex-col gap-1.5 rounded-2xl border border-border bg-sidebar px-3.5 py-2.5">
      <span className="flex items-center gap-2 text-[13px] text-muted-foreground">
        <span
          aria-hidden
          className={cn('size-1.5 shrink-0 rounded-full', step.failed ? 'bg-destructive' : step.done ? 'bg-violet' : 'bg-primary')}
        />
        <span className="min-w-0 break-words">{step.label}</span>
      </span>
      <span ref={rawRef} className={cn('font-mono text-xs break-all text-subtle', !expanded && 'line-clamp-3')}>
        {step.raw}
      </span>
      {showToggle && (
        <button
          type="button"
          aria-expanded={expanded}
          onClick={() => setExpanded((v) => !v)}
          className="self-start rounded-md py-1 text-xs text-muted-foreground outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
        >
          {expanded ? 'Show less' : 'Show all'}
        </button>
      )}
    </div>
  )
}
