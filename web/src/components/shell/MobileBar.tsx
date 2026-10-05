import { ChevronLeft } from 'lucide-react'
import { Link } from 'react-router'
import type { Header } from '@/shell.ts'
import RemedyMark from '@/components/RemedyMark'
import { cn } from '@/lib/utils'

/** The top bar of a phone: back, the mark, the title, and whether Remedy is awake. Hidden from the md breakpoint up. */
export default function MobileBar({ header, online }: { header: Header; online: boolean }) {
  return (
    <div className="flex h-14 shrink-0 items-center gap-2.5 border-b border-border px-4 md:hidden">
      {header.back && (
        <Link
          to={header.back}
          aria-label="Back"
          className="-ml-3 flex size-11 items-center justify-center rounded-full text-foreground outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
        >
          <ChevronLeft className="size-5.5" aria-hidden />
        </Link>
      )}
      <RemedyMark size={24} />
      <p className="min-w-0 flex-1 truncate font-serif text-[17px] font-medium">{header.title}</p>
      <span aria-hidden className={cn('size-2 rounded-full', online ? 'bg-success' : 'bg-primary')} />
      <span className="sr-only">{online ? 'Remedy is awake' : 'Remedy is dozing'}</span>
    </div>
  )
}
