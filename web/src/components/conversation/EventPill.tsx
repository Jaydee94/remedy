import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

/** Something that happened to the incident, as a pill in the middle of the thread. `dot` is a token class such as bg-destructive; `title` is the hover text, for example the full date. */
export default function EventPill({ dot, title, children }: { dot: string; title?: string; children: ReactNode }) {
  return (
    <span title={title} className="flex max-w-full items-center gap-2.5 self-center rounded-full bg-card px-4 py-2 text-center text-[13px] text-muted-foreground">
      <span aria-hidden className={cn('size-2 shrink-0 rounded-full', dot)} />
      <span className="min-w-0 break-words">{children}</span>
    </span>
  )
}
