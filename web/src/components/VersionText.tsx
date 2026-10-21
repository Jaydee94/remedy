import type { VersionLine } from '@/version.ts'
import { cn } from '@/lib/utils'

/** A version in muted text; the commit is the tooltip. Nothing while the version is not known. */
export default function VersionText({ line, className }: { line: VersionLine | null; className?: string }) {
  if (!line) return null
  return (
    <span title={line.title} className={cn('text-xs break-words text-subtle', className)}>
      {line.text}
    </span>
  )
}
