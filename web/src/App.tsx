import { useEffect, useState } from 'react'
import { api } from './api.ts'
import Login from './Login.tsx'
import RunsPage from './RunsPage.tsx'
import RunView from './RunView.tsx'

function useHash(): string {
  const [hash, setHash] = useState(location.hash)
  useEffect(() => {
    const onChange = () => setHash(location.hash)
    window.addEventListener('hashchange', onChange)
    return () => window.removeEventListener('hashchange', onChange)
  }, [])
  return hash
}

export default function App() {
  const [auth, setAuth] = useState<'loading' | 'in' | 'out'>('loading')
  const hash = useHash()

  useEffect(() => {
    api.me().then(() => setAuth('in')).catch(() => setAuth('out'))
  }, [])

  if (auth === 'loading') return null
  if (auth === 'out') return <Login onLoggedIn={() => setAuth('in')} />

  const runId = /^#\/runs\/([\w-]+)$/.exec(hash)?.[1]

  async function logout() {
    await api.logout()
    setAuth('out')
  }

  return (
    <main className="mx-auto flex min-h-screen max-w-3xl flex-col gap-8 px-6 py-10">
      <div className="flex items-center justify-between">
        <a href="#/" className="text-2xl font-semibold tracking-tight">
          Remedy
        </a>
        <button onClick={logout} className="text-sm text-slate-400 hover:text-slate-200">
          Sign out
        </button>
      </div>
      {runId ? <RunView key={runId} id={runId} /> : <RunsPage />}
    </main>
  )
}
