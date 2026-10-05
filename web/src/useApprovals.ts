import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, ApiError } from './api.ts'
import type { ToolCall } from './api.ts'
import { lockAfterChange, mergeApprovals } from './needs.ts'

const POLL_MS = 1000 // a call must show within two seconds (phase 2 spec, success criteria)
const ALL_EVERY = 10 // the full list (with the history) is much heavier than the pending one: every 10th tick

export interface Approvals {
  /** What waits, then what was answered; null until the first answers (or until the first failure). */
  calls: ToolCall[] | null
  /** The message of the last failed load; what was loaded before is kept. */
  error: string
  /** Loads both lists again now, after a decision. Resolves when both are done. */
  reload: () => Promise<void>
  /** Until when (epoch ms, 0 for never) the asks must not be decided: a call that waited has gone, and the next one moved up. */
  lockedUntil: number
}

const message = (e: unknown) => (e instanceof ApiError ? e.message : 'Could not load the approvals')

/**
 * The approvals: the pending list every second, the full list on mount, after a decision and every 10th tick. A response that is
 * not the newest request's of its kind is dropped, and a tick is skipped while the page is hidden.
 */
export function useApprovals(): Approvals {
  const [pending, setPending] = useState<ToolCall[] | null>(null)
  const [all, setAll] = useState<ToolCall[] | null>(null)
  const [pendingError, setPendingError] = useState('')
  const [allError, setAllError] = useState('')
  const [lockedUntil, setLockedUntil] = useState(0)
  const latestPending = useRef(0)
  const latestAll = useRef(0)
  const appliedPending = useRef<readonly number[] | null>(null) // the ids of the pending list last applied
  const sinceAll = useRef(ALL_EVERY)

  const loadPending = useCallback(async () => {
    const mine = ++latestPending.current
    try {
      const list = await api.listApprovals('pending')
      if (mine !== latestPending.current) return
      const ids = list.map((c) => c.id)
      const lock = appliedPending.current === null ? null : lockAfterChange(appliedPending.current, ids, Date.now())
      appliedPending.current = ids
      if (lock !== null) setLockedUntil(lock)
      setPending(list)
      setPendingError('')
    } catch (e) {
      if (mine !== latestPending.current) return
      setPendingError(message(e))
    }
  }, [])

  const loadAll = useCallback(async () => {
    const mine = ++latestAll.current
    sinceAll.current = 0
    try {
      const list = await api.listApprovals('all')
      if (mine !== latestAll.current) return
      setAll(list)
      setAllError('')
    } catch (e) {
      if (mine !== latestAll.current) return
      sinceAll.current = ALL_EVERY // try again at the next tick
      setAllError(message(e))
    }
  }, [])

  const reload = useCallback(async () => {
    await Promise.all([loadPending(), loadAll()])
  }, [loadPending, loadAll])

  useEffect(() => {
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    sinceAll.current = ALL_EVERY // the first tick fetches the full list
    const tick = async () => {
      if (!document.hidden) {
        sinceAll.current++
        await Promise.all([loadPending(), sinceAll.current >= ALL_EVERY ? loadAll() : undefined])
      }
      if (!cancelled) timer = setTimeout(() => void tick(), POLL_MS)
    }
    void tick()
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [loadPending, loadAll])

  // Shown as soon as the pending list is there and the full list has either arrived or failed: a failing history must not hide the asks.
  const calls = useMemo(
    () => (pending !== null && (all !== null || allError !== '') ? mergeApprovals(pending, all ?? []) : null),
    [pending, all, allError],
  )

  return { calls, error: pendingError || allError, reload, lockedUntil }
}
