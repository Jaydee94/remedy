import { useEffect, useLayoutEffect, useState } from 'react'
import { Navigate, Route, Routes, useParams } from 'react-router'
import { ApiError, api, setUnauthorizedHandler } from './api.ts'
import AppLayout from './components/AppLayout.tsx'
import AskPage from './AskPage.tsx'
import ConversationsPage from './ConversationsPage.tsx'
import IncidentThreadPage from './IncidentThreadPage.tsx'
import Login from './Login.tsx'
import NeedsYouPage from './NeedsYouPage.tsx'
import NotFound from './NotFound.tsx'
import RunPage from './RunPage.tsx'
import SetupPage from './SetupPage.tsx'
import TodayPage from './TodayPage.tsx'

/** Mounts RunPage with key={id} so that switching runs resets its state. */
function RunRoute() {
  const { id } = useParams()
  return id ? <RunPage key={id} id={id} /> : <Navigate to="/runs" replace />
}

/** Mounts IncidentThreadPage with key={id}; anything that is not a positive whole number is not an incident. */
function IncidentRoute() {
  const id = Number(useParams().id)
  if (Number.isInteger(id) && id > 0) return <IncidentThreadPage key={id} id={id} />
  return <NotFoundPage />
}

/** A page that does not exist, in the same column as the others. */
function NotFoundPage() {
  return (
    <div className="mx-auto max-w-190 px-4 py-10 md:px-10">
      <NotFound />
    </div>
  )
}

export default function App() {
  const [auth, setAuth] = useState<'loading' | 'in' | 'out'>('loading')
  /** The session ended on the server (a 401 while signed in), as opposed to a deliberate sign out. */
  const [expired, setExpired] = useState(false)

  useEffect(() => {
    api.me().then(() => setAuth('in')).catch(() => setAuth('out'))
  }, [])

  // Many polls may answer 401 at once: setting the same state again is harmless. A layout effect, because the passive effects of the pages
  // start their first requests only after it: a request that began before the handler was set (a new epoch) would not report its 401.
  useLayoutEffect(() => {
    if (auth !== 'in') return
    setUnauthorizedHandler(() => {
      setExpired(true)
      setAuth('out')
    })
    return () => setUnauthorizedHandler(null)
  }, [auth])

  if (auth === 'loading') return null
  if (auth === 'out') {
    return (
      <Login
        notice={expired ? 'Your session ended. Please sign in again.' : undefined}
        onLoggedIn={() => {
          setExpired(false)
          setAuth('in')
        }}
      />
    )
  }

  async function signOut() {
    try {
      await api.logout()
    } catch (err) {
      // A 401 means the session was already gone, which is what signing out wants. Anything else keeps the session.
      if (!(err instanceof ApiError && err.status === 401)) throw err
    }
    // No handler from here on: a late 401 of a poll must not turn a deliberate sign out into "Your session ended".
    setUnauthorizedHandler(null)
    setAuth('out')
  }

  return (
    <Routes>
      <Route element={<AppLayout onSignOut={signOut} />}>
        <Route index element={<TodayPage />} />
        <Route path="incidents" element={<ConversationsPage />} />
        <Route path="incidents/:id" element={<IncidentRoute />} />
        <Route path="approvals" element={<NeedsYouPage />} />
        <Route path="runs" element={<AskPage />} />
        <Route path="runs/:id" element={<RunRoute />} />
        <Route path="settings" element={<SetupPage />} />
        <Route path="*" element={<NotFoundPage />} />
      </Route>
    </Routes>
  )
}
