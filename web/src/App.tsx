import { useEffect, useState } from 'react'
import { Navigate, Route, Routes, useParams } from 'react-router'
import { api } from './api.ts'
import AppLayout from './components/AppLayout.tsx'
import LegacyPage from './components/LegacyPage.tsx'
import ApprovalsPage from './ApprovalsPage.tsx'
import ConversationsPage from './ConversationsPage.tsx'
import IncidentView from './IncidentView.tsx'
import Login from './Login.tsx'
import NotFound from './NotFound.tsx'
import RunsPage from './RunsPage.tsx'
import RunView from './RunView.tsx'
import SettingsPage from './SettingsPage.tsx'
import TimelinePage from './TimelinePage.tsx'

/** Mounts RunView with key={id} so that switching runs resets its state. */
function RunRoute() {
  const { id } = useParams()
  return id ? <RunView key={id} id={id} /> : <Navigate to="/runs" replace />
}

/** Mounts IncidentView with key={id}; anything that is not a positive whole number is not an incident. */
function IncidentRoute() {
  const id = Number(useParams().id)
  return Number.isInteger(id) && id > 0 ? <IncidentView key={id} id={id} /> : <NotFound />
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
        <Route path="incidents" element={<ConversationsPage />} />
        <Route element={<LegacyPage />}>
          <Route index element={<TimelinePage />} />
          <Route path="incidents/:id" element={<IncidentRoute />} />
          <Route path="approvals" element={<ApprovalsPage />} />
          <Route path="runs" element={<RunsPage />} />
          <Route path="runs/:id" element={<RunRoute />} />
          <Route path="settings" element={<SettingsPage />} />
          <Route path="*" element={<NotFound />} />
        </Route>
      </Route>
    </Routes>
  )
}
