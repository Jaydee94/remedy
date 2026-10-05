import { useCallback, useEffect, useRef, useState } from 'react'
import { api, ApiError } from './api.ts'
import type { IncidentDetail, Run } from './api.ts'

const SLOW_MS = 5000
const FAST_MS = 2000

export interface ThreadData {
  detail: IncidentDetail | null
  /** The runs of the incident (its responder runs and the questions), newest first. */
  runs: Run[]
  /** The message of the last failed load; what was loaded before is kept. */
  error: string
  /** The server does not know this incident. */
  missing: boolean
  /** Loads again now, for example after the maintainer did something. */
  reload: () => Promise<void>
}

/** An incident with its history and its runs. It refreshes every five seconds, every two while a diagnosis or a question runs. */
export function useIncidentThread(id: number): ThreadData {
  const [detail, setDetail] = useState<IncidentDetail | null>(null)
  const [runs, setRuns] = useState<Run[]>([])
  const [error, setError] = useState('')
  const [missing, setMissing] = useState(false)
  const fast = useRef(false)
  const alive = useRef(true)

  const load = useCallback(async () => {
    try {
      const [d, r] = await Promise.all([api.getIncident(id), api.listIncidentRuns(id)])
      if (!alive.current) return
      setDetail(d)
      setRuns(r)
      setError('')
      fast.current = d.incident.state === 'diagnosing' || r.some((x) => x.status === 'queued' || x.status === 'running')
    } catch (e) {
      if (!alive.current) return
      if (e instanceof ApiError && e.status === 404) setMissing(true)
      else setError(e instanceof ApiError ? e.message : 'Could not load the incident')
    }
  }, [id])

  useEffect(() => {
    alive.current = true
    let timer: ReturnType<typeof setTimeout> | undefined
    const tick = async () => {
      await load()
      if (alive.current) timer = setTimeout(() => void tick(), fast.current ? FAST_MS : SLOW_MS)
    }
    void tick()
    return () => {
      alive.current = false
      clearTimeout(timer)
    }
  }, [load])

  return { detail, runs, error, missing, reload: load }
}
