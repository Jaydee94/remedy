import { useCallback, useEffect, useRef, useState } from 'react'
import { api, ApiError } from './api.ts'
import type { RunnerLogin } from './api.ts'
import { answerOrder, requestProblem } from './loginwizard.ts'
import type { LoginRoute } from './loginwizard.ts'
import { hideOnError } from './runnerstatus.ts'

export interface LoginRelay {
  /** What `GET /api/runner/login` answered last; null before the first answer, after an error and when the route is absent. */
  offered: RunnerLogin | null
  /** The control plane has no such route (404): the page shows the terminal command only, as before the relay. */
  absent: boolean
  /** A fixed sentence when the last read failed. */
  error: string
  /** Reads the view again now. */
  refresh: () => Promise<void>
  /**
   * Runs one request of the sign-in and reads the view again afterwards, whatever its answer. Resolves to the empty text when it
   * worked, otherwise to the sentence of `requestProblem`; it never rejects and never passes the server's message on.
   */
  run: (route: LoginRoute, request: () => Promise<unknown>) => Promise<string>
}

/**
 * The login from the Setup page as the server sees it, read every `pollMs` (2 s while a session is open, 10 s otherwise: the wizard's
 * view says which). The hook holds no code and no address beyond the last answer of the view: the code only passes through `run`'s
 * closure, which the caller builds at the click.
 */
export function useLoginRelay(pollMs: number): LoginRelay {
  const [offered, setOffered] = useState<RunnerLogin | null>(null)
  const [absent, setAbsent] = useState(false)
  const [error, setError] = useState('')
  const alive = useRef(true)
  // A poll sent before a request of the sign-in can answer after the read that follows the request: its older state is dropped.
  const order = useRef(answerOrder())

  const refresh = useCallback(() => {
    const n = order.current.next()
    return api.getRunnerLogin().then(
      (v) => {
        if (!alive.current || !order.current.accept(n)) return
        setOffered(v)
        setAbsent(false)
        setError('')
      },
      (e: unknown) => {
        if (!alive.current || !order.current.accept(n)) return
        setOffered(null)
          if (e instanceof ApiError && hideOnError(e.status)) {
            setAbsent(true)
            setError('')
            return
          }
        setError('I could not load the sign-in from this page.')
      },
    )
  }, [])

  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
    }
  }, [])

  // Once the route answered 404 the polling stops: an older control plane without the routes answers 404, so
  // the page stops asking (an upgrade restarts the control plane, which ends the admin session and so reloads the page).
  useEffect(() => {
    if (absent) return
    void refresh()
    const timer = setInterval(() => void refresh(), pollMs)
    return () => clearInterval(timer)
  }, [refresh, pollMs, absent])

  const run = useCallback(
    async (route: LoginRoute, request: () => Promise<unknown>) => {
      let problem = ''
      try {
        await request()
      } catch (e: unknown) {
        problem = e instanceof ApiError ? requestProblem(route, e.status, e.reason) : requestProblem(route, 0)
      }
      await refresh()
      return problem
    },
    [refresh],
  )

  return { offered, absent, error, refresh, run }
}
