import type { ReactNode } from 'react'

/** Why a run failed, in a red card. */
export default function FailCard({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-2 rounded-[18px] border border-destructive/40 bg-soft-open p-4">
      <span className="font-semibold text-destructive">{title}</span>
      <span className="break-words whitespace-pre-wrap text-foreground/90">{children}</span>
    </div>
  )
}
