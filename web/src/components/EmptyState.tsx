import type { ReactNode } from 'react'
import RemedyMark from '@/components/RemedyMark'

/** A dashed card for a list with nothing in it. */
export default function EmptyState({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="flex flex-col items-center gap-3 rounded-3xl border border-dashed border-input px-5 py-16 text-center">
      <RemedyMark size={36} fill="var(--input)" />
      <span className="font-serif text-[19px]">{title}</span>
      {children && <span className="max-w-90 text-muted-foreground">{children}</span>}
    </div>
  )
}
