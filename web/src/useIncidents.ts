import { useEffect, useState } from 'react'
import { api, ApiError } from './api.ts'
import type { Incident } from './api.ts'

const POLL_MS = 5000

export interface IncidentsState {
  /** Every incident of every state (the API answers at most 200), newest first. Null until the first answer. */
  incidents: Incident[] | null
  /** The message of the last failed load; the incidents of an earlier load are kept. */
  error: string
}

/** All incidents, refreshed every five seconds. */
export function useIncidents(): IncidentsState {
  const [state, setState] = useState<IncidentsState>({ incidents: null, error: '' })

  useEffect(() => {
    let active = true
    let busy = false
    const load = async () => {
      if (busy) return
      busy = true
      try {
        const list = await api.listIncidents('all')
        if (active) setState({ incidents: list, error: '' })
      } catch (e) {
        const error = e instanceof ApiError ? e.message : 'Could not load the incidents'
        if (active) setState((s) => ({ incidents: s.incidents, error }))
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
