import { createContext, useContext, useEffect } from 'react'
import { emptyShell } from './shell.ts'
import type { Header, ShellState } from './shell.ts'

export interface ShellApi {
  setHeader: (header: Header | null) => void
  /** Signs the maintainer out (the sidebar has a button; a phone has it at the end of Setup). */
  signOut: () => void
}

export const ShellContext = createContext<ShellApi>({ setHeader: () => {}, signOut: () => {} })

/** What the shell polls: the waiting approvals, the active incidents, whether the server answers. Pages read it instead of polling again. */
export const ShellStateContext = createContext<ShellState>(emptyShell)

export function useShellState(): ShellState {
  return useContext(ShellStateContext)
}

/** The way to sign out, for a page that offers it. */
export function useSignOut(): () => void {
  return useContext(ShellContext).signOut
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
