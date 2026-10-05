import { useEffect, useState } from 'react'
import { api, ApiError } from './api.ts'
import type { Incident } from './api.ts'
import { mergeIncidents } from './conversation.ts'

const POLL_MS = 5000

export interface IncidentsState {
  /** Every incident the API answers for all states plus every ignored one, in the order the API answers (the pages sort). Null until the first answer. */
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
        // The API answers the 200 incidents seen most recently. An ignored incident is not seen any more (the engine skips it),
        // so it would drop out of 'all' and could not be un-ignored: the ignored ones are loaded by a request of their own.
        const [all, ignored] = await Promise.all([api.listIncidents('all'), api.listIncidents('ignored')])
        if (active) setState({ incidents: mergeIncidents(all, ignored), error: '' })
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
