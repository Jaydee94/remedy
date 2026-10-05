import { cn } from '@/lib/utils'

/** The number of calls that wait for a decision. */
export default function PendingBadge({ count, className }: { count: number; className?: string }) {
  return (
    <span
      aria-label={`${count} waiting for a decision`}
      className={cn(
        'flex h-5.5 min-w-5.5 items-center justify-center rounded-full bg-primary px-1.5 text-xs font-bold text-primary-foreground',
        className,
      )}
    >
      {count}
    </span>
  )
}
