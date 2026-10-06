import { useSyncExternalStore } from 'react'

const TICK_MS = 30_000
const listeners = new Set<() => void>()
let now = Date.now()
let timer: ReturnType<typeof setInterval> | undefined

function subscribe(listener: () => void) {
  listeners.add(listener)
  if (listeners.size === 1) {
    now = Date.now()
    timer = setInterval(() => {
      now = Date.now()
      listeners.forEach((l) => l())
    }, TICK_MS)
  }
  return () => {
    listeners.delete(listener)
    if (listeners.size === 0) clearInterval(timer)
  }
}

/** The current time in milliseconds, refreshed every 30 seconds for every component that asks: for "N min ago", never for logic. */
export function useClock(): number {
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot)
}

/** Without a subscriber nothing ticks, so a stale value is renewed here; the stored value keeps two calls in a row equal. */
function getSnapshot(): number {
  if (listeners.size === 0 && Date.now() - now > TICK_MS) now = Date.now()
  return now
}
