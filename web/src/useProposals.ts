import { useEffect, useState } from 'react'
import { api } from './api.ts'
import { nextProposals } from './skills.ts'

const POLL_MS = 5000

/** How many proposals wait for a review, from one poll every five seconds. A failed poll keeps the last number. */
export function useProposals(): number {
  const [count, setCount] = useState(0)

  useEffect(() => {
    let active = true
    let busy = false // a slow poll must not overlap the next one
    const load = async () => {
      if (busy) return
      busy = true
      try {
        const [result] = await Promise.allSettled([api.listSkills({ state: 'proposed' })])
        if (active) setCount((prev) => nextProposals(prev, result))
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

  return count
}
