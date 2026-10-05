import { useEffect, useState } from 'react'
import { api } from './api.ts'

/** The number of calls that wait for a decision, refreshed every two seconds. */
export function usePendingApprovals(): number {
  const [count, setCount] = useState(0)

  useEffect(() => {
    let active = true
    const load = () =>
      api
        .listApprovals('pending')
        .then((list) => {
          if (active) setCount(list.length)
        })
        .catch(() => undefined) // a failed refresh keeps the last number
    void load()
    const timer = setInterval(load, 2000)
    return () => {
      active = false
      clearInterval(timer)
    }
  }, [])

  return count
}
