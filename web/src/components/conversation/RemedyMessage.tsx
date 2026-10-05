import type { ReactNode } from 'react'
import RemedyAvatar from '@/components/RemedyAvatar'

interface Props {
  kind?: 'idle' | 'working' | 'ask'
  /** The small line above the message, such as "Remedy · 08:40". */
  meta?: string
  children: ReactNode
}

/** A message of Remedy: the avatar on the left, a small line, and the content. */
export default function RemedyMessage({ kind = 'idle', meta, children }: Props) {
  return (
    <div className="flex gap-3.5">
      <RemedyAvatar kind={kind} />
      <div className="flex min-w-0 flex-1 flex-col gap-3">
        {meta && <span className="text-xs text-subtle">{meta}</span>}
        {children}
      </div>
    </div>
  )
}
