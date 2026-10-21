import { useEffect, useState } from 'react'
import { api } from './api.ts'
import type { VersionInfo } from './api.ts'

/** The control plane's build, asked once when the shell loads. No polling: it changes only when the control plane restarts, and a restart signs everyone out. Null until it answers, and for good when it does not (an older control plane has no route). */
export function useVersion(): VersionInfo | null {
  const [version, setVersion] = useState<VersionInfo | null>(null)
  useEffect(() => {
    let alive = true
    api
      .getVersion()
      .then((v) => {
        if (alive) setVersion(v)
      })
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [])
  return version
}
