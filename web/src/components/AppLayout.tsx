import { useMemo, useState } from 'react'
import { Outlet, useLocation } from 'react-router'
import { defaultHeader, sectionOf } from '@/shell.ts'
import type { Header } from '@/shell.ts'
import { ShellContext, ShellStateContext } from '@/shellContext.ts'
import { useShell } from '@/useShell.ts'
import MobileBar from '@/components/shell/MobileBar'
import OfflineBanner from '@/components/shell/OfflineBanner'
import Sidebar from '@/components/shell/Sidebar'
import TabBar from '@/components/shell/TabBar'
import ToastProvider from '@/components/ToastProvider'

/** The shell: sidebar on a desktop, top bar and tab bar on a phone, an offline banner, and the toast. Only the content scrolls. */
export default function AppLayout({ onSignOut }: { onSignOut: () => void | Promise<void> }) {
  const { pathname } = useLocation()
  const shell = useShell()
  const [custom, setCustom] = useState<Header | null>(null)
  const context = useMemo(() => ({ setHeader: setCustom, signOut: onSignOut }), [onSignOut])
  const section = sectionOf(pathname)
  const header = custom ?? defaultHeader(pathname)

  return (
    <ShellContext.Provider value={context}>
      <ShellStateContext.Provider value={shell}>
        <ToastProvider>
          <div className="flex h-dvh flex-col overflow-hidden bg-background text-foreground">
            {!shell.online && <OfflineBanner />}
            <div className="flex min-h-0 flex-1">
              <Sidebar section={section} pathname={pathname} shell={shell} />
              <main className="flex min-w-0 flex-1 flex-col">
                <MobileBar header={header} online={shell.online} />
                <div className="min-h-0 flex-1 overflow-auto">
                  <Outlet />
                </div>
                <TabBar section={section} pathname={pathname} pending={shell.pending} />
              </main>
            </div>
          </div>
        </ToastProvider>
      </ShellStateContext.Provider>
    </ShellContext.Provider>
  )
}
