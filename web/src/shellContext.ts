import { createContext, useContext, useEffect } from 'react'
import type { Header } from './shell.ts'

export interface ShellApi {
  setHeader: (header: Header | null) => void
}

export const ShellContext = createContext<ShellApi>({ setHeader: () => {} })

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
