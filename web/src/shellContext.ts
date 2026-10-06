import { createContext, useCallback, useContext, useEffect, useRef } from 'react'
import { emptyShell } from './shell.ts'
import type { Header, ShellState } from './shell.ts'
import { useToast } from './toast.ts'

export interface ShellApi {
  setHeader: (header: Header | null) => void
  /** Signs the maintainer out (the sidebar has a button; a phone has it at the end of Setup). It may reject; `useSignOut` handles that. */
  signOut: () => void | Promise<void>
}

export const ShellContext = createContext<ShellApi>({ setHeader: () => {}, signOut: () => {} })

/** What the shell polls: the waiting approvals, the active incidents, whether the server answers. Pages read it instead of polling again. */
export const ShellStateContext = createContext<ShellState>(emptyShell)

export function useShellState(): ShellState {
  return useContext(ShellStateContext)
}

/** The way to sign out, for a page or the sidebar. A failed request keeps the session and says so; it never rejects. A second call while one runs is ignored. */
export function useSignOut(): () => void {
  const { signOut } = useContext(ShellContext)
  const toast = useToast()
  const running = useRef(false)
  return useCallback(() => {
    if (running.current) return
    running.current = true
    void Promise.resolve()
      .then(signOut)
      .catch(() => toast.show('Could not sign out. Check the connection and try again.'))
      .finally(() => {
        running.current = false
      })
  }, [signOut, toast])
}

/** A page sets the title and back target of the phone's top bar for as long as it is mounted. Null keeps the default of the route. */
export function useShellHeader(header: Header | null) {
  const { setHeader } = useContext(ShellContext)
  const title = header?.title
  const back = header?.back
  useEffect(() => {
    setHeader(title === undefined ? null : { title, back })
    return () => setHeader(null)
  }, [setHeader, title, back])
}
