import { LogOut, Play, Settings, TriangleAlert } from 'lucide-react'
import { NavLink, Outlet } from 'react-router'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

const nav = [
  { to: '/incidents', label: 'Incidents', icon: TriangleAlert },
  { to: '/runs', label: 'Runs', icon: Play },
  { to: '/settings', label: 'Settings', icon: Settings },
]

export default function AppLayout({ onSignOut }: { onSignOut: () => void }) {
  return (
    <div className="flex min-h-screen">
      <aside className="flex w-56 shrink-0 flex-col gap-6 border-r border-border bg-card/40 p-4">
        <NavLink to="/" className="px-2 text-xl font-semibold tracking-tight">
          Remedy
        </NavLink>
        <nav className="flex flex-1 flex-col gap-1">
          {nav.map(({ to, label, icon: Icon }) => (
            <NavLink
              key={to}
              to={to}
              className={({ isActive }) =>
                cn(
                  'flex items-center gap-2 rounded-lg px-2 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground',
                  isActive && 'bg-accent text-accent-foreground',
                )
              }
            >
              <Icon className="size-4" />
              {label}
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
