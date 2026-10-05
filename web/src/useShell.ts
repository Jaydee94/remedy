import { useEffect, useState } from 'react'
import { api, ApiError } from './api.ts'
import { emptyShell, isGatewayStatus, nextShell } from './shell.ts'
import type { ShellState } from './shell.ts'

const POLL_MS = 2000 // a call that waits for a decision must show within two seconds (phase 2 spec, success criteria)

/** What the shell shows, from one poll every two seconds. A failed poll keeps the last data; see nextShell for what counts as offline. */
export function useShell(): ShellState {
  const [state, setState] = useState<ShellState>(emptyShell)

  useEffect(() => {
    let active = true
    let busy = false // a slow poll must not overlap the next one
    const load = async () => {
      if (busy) return
      busy = true
      try {
        const [approvals, incidents] = await Promise.allSettled([api.listApprovals('pending'), api.listIncidents('active')])
        if (active) setState((prev) => nextShell(prev, approvals, incidents, (e) => e instanceof ApiError && !isGatewayStatus(e.status)))
      } finally {
        busy = false
      }
    }
    void load()
    const timer = setInterval(() => void load(), POLL_MS)
    return () => {
      active = false
      clearInterval(timer)
    }
  }, [])

  return state
}
