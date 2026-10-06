import { useCallback, useEffect, useRef, useState } from 'react'
import { api, ApiError, streamRun } from './api.ts'
import type { Run, RunEvent, ToolCall } from './api.ts'
import { effectiveStatus } from './runview.ts'

/**
 * A run with its events (live), its calls and its status. The run and its calls are fetched again every three seconds while the run
 * can still change (it may start waiting for an approval, or be cancelled), and once more when it has ended. `missing` says that the
 * server does not know the run (nothing is fetched then); `error` is the message of a failed load while there is no run to show yet.
 */
export function useRun(id: string) {
  const [run, setRun] = useState<Run | null>(null)
  const [events, setEvents] = useState<RunEvent[]>([])
  const [calls, setCalls] = useState<ToolCall[]>([])
  const [missing, setMissing] = useState(false)
  const [error, setError] = useState('')
  const loaded = useRef(false)
  const seen = useRef<{ id: string; seqs: Set<number> }>({ id, seqs: new Set() })

  const refresh = useCallback((): Promise<unknown> => {
    const getRun = api
      .getRun(id)
      .then((r) => {
        loaded.current = true
        setRun(r)
        setError('')
      })
      .catch((e: unknown) => {
        if (e instanceof ApiError && e.status === 404) setMissing(true)
        else if (!loaded.current) setError(e instanceof ApiError ? e.message : 'Could not load the run')
      })
    const getCalls = api.listToolCalls(id).then(setCalls).catch(() => undefined)
    // Both requests, so that a caller can wait until the page shows what the server says; a failure was handled above.
    return Promise.all([getRun, getCalls])
  }, [id])

  const ended = run?.status === 'succeeded' || run?.status === 'failed'

  useEffect(() => {
    if (missing) return
    refresh()
    if (ended) return
    const timer = setInterval(refresh, 3000)
    return () => clearInterval(timer)
  }, [refresh, ended, missing])

  useEffect(
    () =>
      streamRun(
        id,
        (e) => {
          if (seen.current.id !== id) seen.current = { id, seqs: new Set() }
          if (seen.current.seqs.has(e.seq)) return
          seen.current.seqs.add(e.seq)
          setEvents((prev) => [...prev, e])
        },
        setRun,
      ),
    [id],
  )

  const status = run ? effectiveStatus(run.status, events.length) : undefined
  return { run, events, calls, status, ended, missing, error, refresh }
}
