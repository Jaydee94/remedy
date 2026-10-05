import { useEffect, useState } from 'react'
import { Navigate, Route, Routes, useParams } from 'react-router'
import { api } from './api.ts'
import AppLayout from './components/AppLayout.tsx'
import LegacyPage from './components/LegacyPage.tsx'
import ConversationsPage from './ConversationsPage.tsx'
import IncidentThreadPage from './IncidentThreadPage.tsx'
import Login from './Login.tsx'
import NeedsYouPage from './NeedsYouPage.tsx'
import NotFound from './NotFound.tsx'
import RunPage from './RunPage.tsx'
import RunsPage from './RunsPage.tsx'
import SettingsPage from './SettingsPage.tsx'
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
  return (
    <div className="mx-auto max-w-190 px-4 py-10 md:px-10">
      <NotFound />
    </div>
  )
}

export default function App() {
  const [auth, setAuth] = useState<'loading' | 'in' | 'out'>('loading')

  useEffect(() => {
    api.me().then(() => setAuth('in')).catch(() => setAuth('out'))
  }, [])

  if (auth === 'loading') return null
  if (auth === 'out') return <Login onLoggedIn={() => setAuth('in')} />

  async function signOut() {
    await api.logout()
    setAuth('out')
  }

  return (
    <Routes>
      <Route element={<AppLayout onSignOut={signOut} />}>
        <Route index element={<TodayPage />} />
        <Route path="incidents" element={<ConversationsPage />} />
        <Route path="incidents/:id" element={<IncidentRoute />} />
        <Route path="approvals" element={<NeedsYouPage />} />
        <Route path="runs/:id" element={<RunRoute />} />
        <Route element={<LegacyPage />}>
          <Route path="runs" element={<RunsPage />} />
          <Route path="settings" element={<SettingsPage />} />
          <Route path="*" element={<NotFound />} />
        </Route>
      </Route>
    </Routes>
  )
}
