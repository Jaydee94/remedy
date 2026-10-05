import type { ReactNode } from 'react'

/** What the maintainer said, on the right. */
export default function UserBubble({ children }: { children: ReactNode }) {
  return (
    <span className="max-w-[85%] self-end rounded-[20px_20px_6px_20px] bg-secondary px-4 py-3 break-words whitespace-pre-wrap text-foreground/90">
      {children}
    </span>
  )
}
