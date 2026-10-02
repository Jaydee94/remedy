import { useState } from 'react'
import type { FormEvent } from 'react'
import { api, ApiError } from './api.ts'

export default function Login({ onLoggedIn }: { onLoggedIn: () => void }) {
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await api.login(password)
      onLoggedIn()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Login failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <main className="mx-auto flex min-h-screen max-w-sm flex-col justify-center gap-4 px-6">
      <h1 className="text-3xl font-semibold tracking-tight">Remedy</h1>
      <form onSubmit={submit} className="flex flex-col gap-3">
        <input
          type="password"
          autoFocus
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          placeholder="Admin password"
          className="rounded-lg border border-slate-700 bg-slate-900 px-3 py-2 outline-none focus:border-indigo-400"
        />
        <button
          disabled={busy || password === ''}
          className="rounded-lg bg-indigo-500 px-3 py-2 font-medium disabled:opacity-50"
        >
          Sign in
        </button>
        {error && <p className="text-sm text-rose-400">{error}</p>}
      </form>
    </main>
  )
}
