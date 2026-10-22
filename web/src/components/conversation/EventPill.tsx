import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

/**
 * Something that happened to the incident, as a pill in the middle of the thread. `dot` is a token class such as bg-destructive; `title` is
 * the hover text, for example the full date. `muted` makes it a quiet line without the pill, for a recurrence under the opening.
 */
export default function EventPill({ dot, title, muted, children }: { dot: string; title?: string; muted?: boolean; children: ReactNode }) {
  return (
    <span
      title={title}
      className={cn(
        'flex max-w-full items-center gap-2.5 self-center text-center',
        muted ? 'px-4 py-0 text-xs text-subtle' : 'rounded-full bg-card px-4 py-2 text-[13px] text-muted-foreground',
      )}
    >
      <span aria-hidden className={cn('shrink-0 rounded-full', muted ? 'size-1.5' : 'size-2', dot)} />
      <span className="min-w-0 break-words">{children}</span>
    </span>
  )
}
