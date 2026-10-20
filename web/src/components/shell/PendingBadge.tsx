import { cn } from '@/lib/utils'

/** A number in a round badge: the calls that wait for a decision, or the proposals that wait for a review. */
export default function PendingBadge({ count, label, className }: { count: number; label?: string; className?: string }) {
  return (
    <span
      aria-label={label ?? `${count} waiting for a decision`}
      className={cn(
        'flex h-5.5 min-w-5.5 items-center justify-center rounded-full bg-primary px-1.5 text-xs font-bold text-primary-foreground',
        className,
      )}
    >
      {count}
    </span>
  )
}
