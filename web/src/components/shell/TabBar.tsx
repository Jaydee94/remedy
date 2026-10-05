import { Link } from 'react-router'
import { navItems } from '@/shell.ts'
import type { Section } from '@/shell.ts'
import { navIcons } from '@/components/shell/navIcons.ts'
import PendingBadge from '@/components/shell/PendingBadge'
import { cn } from '@/lib/utils'

/** The bottom tab bar of a phone. 44 px or more per target, and the safe-area inset under it. Hidden from the md breakpoint up. */
export default function TabBar({ section, pathname, pending }: { section: Section | null; pathname: string; pending: number }) {
  return (
    <nav
      aria-label="Main"
      className="flex shrink-0 border-t border-border bg-sidebar px-1.5 pt-1.5 pb-[max(0.75rem,env(safe-area-inset-bottom))] md:hidden"
    >
      {navItems.map((item) => {
        const Icon = navIcons[item.section]
        const active = item.section === section
        return (
          <Link
            key={item.to}
            to={item.to}
            aria-current={pathname === item.to ? 'page' : active ? 'true' : undefined}
            className={cn(
              'relative flex h-13.5 flex-1 flex-col items-center justify-center gap-1 rounded-2xl text-[11px] outline-none focus-visible:ring-3 focus-visible:ring-ring/50',
              active ? 'text-primary' : 'text-muted-foreground',
            )}
          >
            <Icon className="size-5.5" aria-hidden />
            {item.short}
            {item.section === 'needs' && pending > 0 && (
              <PendingBadge count={pending} className="absolute top-1 left-[calc(50%+6px)] h-4.5 min-w-4.5 px-1 text-[11px]" />
            )}
          </Link>
        )
      })}
    </nav>
  )
}
