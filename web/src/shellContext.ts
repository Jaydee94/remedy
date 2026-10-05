import { createContext, useContext, useEffect } from 'react'
import { emptyShell } from './shell.ts'
import type { Header, ShellState } from './shell.ts'

export interface ShellApi {
  setHeader: (header: Header | null) => void
}

export const ShellContext = createContext<ShellApi>({ setHeader: () => {} })

/** What the shell polls: the waiting approvals, the active incidents, whether the server answers. Pages read it instead of polling again. */
export const ShellStateContext = createContext<ShellState>(emptyShell)

export function useShellState(): ShellState {
  return useContext(ShellStateContext)
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
