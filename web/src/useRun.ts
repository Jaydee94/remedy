import { useCallback, useEffect, useState } from 'react'
import { api, streamRun } from './api.ts'
import type { Run, RunEvent, ToolCall } from './api.ts'
import { effectiveStatus } from './runview.ts'

/**
 * A run with its events (live), its calls and its status. The run and its calls are fetched again every three seconds while the run
 * can still change (it may start waiting for an approval, or be cancelled), and once more when it has ended.
 */
export function useRun(id: string) {
  const [run, setRun] = useState<Run | null>(null)
  const [events, setEvents] = useState<RunEvent[]>([])
  const [calls, setCalls] = useState<ToolCall[]>([])

  const refresh = useCallback(() => {
    api.getRun(id).then(setRun).catch(() => undefined)
    api.listToolCalls(id).then(setCalls).catch(() => undefined)
  }, [id])

  const ended = run?.status === 'succeeded' || run?.status === 'failed'

  useEffect(() => {
    refresh()
    if (ended) return
    const timer = setInterval(refresh, 3000)
    return () => clearInterval(timer)
  }, [refresh, ended])

  useEffect(
    () => streamRun(id, (e) => setEvents((prev) => (prev.some((p) => p.seq === e.seq) ? prev : [...prev, e])), setRun),
    [id],
  )

  const status = run ? effectiveStatus(run.status, events.length) : undefined
  return { run, events, calls, status, ended, refresh }
}
