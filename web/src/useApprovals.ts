import { useCallback, useEffect, useRef, useState } from 'react'
import { api, ApiError } from './api.ts'
import type { ToolCall } from './api.ts'

const POLL_MS = 1000 // a call must show within two seconds (phase 2 spec, success criteria)

export interface Approvals {
  /** Every approval, newest first as the API answers; null until the first answer. */
  calls: ToolCall[] | null
  /** The message of the last failed load; what was loaded before is kept. */
  error: string
  /** Loads again now, after a decision. */
  reload: () => Promise<void>
}

/** All approvals, waiting and answered, polled every second. A response that is not the newest request's is dropped. */
export function useApprovals(): Approvals {
  const [calls, setCalls] = useState<ToolCall[] | null>(null)
  const [error, setError] = useState('')
  const latest = useRef(0)

  const load = useCallback(async () => {
    const mine = ++latest.current
    try {
      const list = await api.listApprovals('all')
      if (mine !== latest.current) return
      setCalls(list)
      setError('')
    } catch (e) {
      if (mine !== latest.current) return
      setError(e instanceof ApiError ? e.message : 'Could not load the approvals')
    }
  }, [])

  useEffect(() => {
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const tick = async () => {
      await load()
      if (!cancelled) timer = setTimeout(() => void tick(), POLL_MS)
    }
    void tick()
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [load])

  return { calls, error, reload: load }
}
