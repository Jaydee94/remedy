import { History, LogOut, Play, Settings, ShieldCheck, TriangleAlert } from 'lucide-react'
import { NavLink, Outlet } from 'react-router'
import { usePendingApprovals } from '@/usePendingApprovals'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

const nav = [
  { to: '/', label: 'Timeline', icon: History, end: true },
  { to: '/incidents', label: 'Incidents', icon: TriangleAlert, end: false },
  { to: '/approvals', label: 'Approvals', icon: ShieldCheck, end: false },
  { to: '/runs', label: 'Runs', icon: Play, end: false },
  { to: '/settings', label: 'Settings', icon: Settings, end: false },
]

export default function AppLayout({ onSignOut }: { onSignOut: () => void }) {
  const pending = usePendingApprovals()

  return (
    <div className="flex min-h-screen">
      <aside className="flex w-56 shrink-0 flex-col gap-6 border-r border-border bg-card/40 p-4">
        <NavLink to="/" className="px-2 text-xl font-semibold tracking-tight">
          Remedy
        </NavLink>
        <nav className="flex flex-1 flex-col gap-1">
          {nav.map(({ to, label, icon: Icon, end }) => (
            <NavLink
              key={to}
              to={to}
              end={end}
              className={({ isActive }) =>
                cn(
                  'flex items-center gap-2 rounded-lg px-2 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground',
                  isActive && 'bg-accent text-accent-foreground',
                )
              }
            >
              <Icon className="size-4" />
              {label}
              {to === '/approvals' && pending > 0 && (
                <span
                  aria-label={`${pending} waiting for a decision`}
                  className="ml-auto rounded-full bg-amber-500 px-1.5 text-xs font-medium text-black"
                >
                  {pending}
                </span>
              )}
            </NavLink>
          ))}
        </nav>
        <Button variant="ghost" size="sm" className="justify-start" onClick={onSignOut}>
          <LogOut /> Sign out
        </Button>
      </aside>
      <main className="min-w-0 flex-1 p-8">
        <div className="mx-auto max-w-5xl">
          <Outlet />
        </div>
      </main>
    </div>
  )
}
