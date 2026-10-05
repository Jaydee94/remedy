import { cn } from '@/lib/utils'

interface Props {
  /** idle: Remedy speaks; working: it is busy (a breathing ring); ask: it waits for an answer. */
  kind?: 'idle' | 'working' | 'ask'
}

/** Remedy's avatar in a conversation. */
export default function RemedyAvatar({ kind = 'idle' }: Props) {
  const ask = kind === 'ask'
  return (
    <span
      aria-hidden
      className={cn(
        'flex size-9 shrink-0 items-center justify-center rounded-full',
        ask ? 'bg-primary' : 'bg-soft-diagnosing',
        kind === 'working' &&
          'animate-rm-breath motion-reduce:shadow-[0_0_0_2px_var(--background),0_0_0_5px_rgb(242_193_78/0.5)]',
      )}
    >
      <svg viewBox="0 0 24 24" width={21} height={21}>
        <path
          d="M12 2.6c-.3.4-7 8.3-7 12.9a7 7 0 0 0 14 0c0-4.6-6.7-12.5-7-12.9z"
          fill={ask ? 'var(--primary-foreground)' : 'var(--primary)'}
        />
        <path
          d="M8.7 15.4a3.3 3.3 0 0 0 3.3 3.3"
          stroke={ask ? 'var(--primary)' : 'var(--soft-diagnosing)'}
          strokeWidth="1.6"
          fill="none"
          strokeLinecap="round"
        />
      </svg>
    </span>
  )
}
