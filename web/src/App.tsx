import { useEffect, useState } from 'react'
import { Navigate, Route, Routes, useParams } from 'react-router'
import { api } from './api.ts'
import AppLayout from './components/AppLayout.tsx'
import Login from './Login.tsx'
import NotFound from './NotFound.tsx'
import RunsPage from './RunsPage.tsx'
import RunView from './RunView.tsx'

/** Mounts RunView with key={id} so that switching runs resets its state. */
function RunRoute() {
  const { id } = useParams()
  return id ? <RunView key={id} id={id} /> : <Navigate to="/runs" replace />
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
        <Route index element={<Navigate to="/runs" replace />} />
        <Route path="runs" element={<RunsPage />} />
        <Route path="runs/:id" element={<RunRoute />} />
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  )
}
