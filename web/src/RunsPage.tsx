import { useCallback, useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { api, ApiError } from './api.ts'
import type { Run } from './api.ts'
import { statusColor } from './status.ts'

export default function RunsPage() {
  const [runs, setRuns] = useState<Run[]>([])
  const [prompt, setPrompt] = useState('')
  const [error, setError] = useState('')

  const refresh = useCallback(() => {
    api.listRuns().then(setRuns).catch((e: unknown) => setError(String(e)))
  }, [])

  useEffect(() => {
    refresh()
    const t = setInterval(refresh, 3000)
    return () => clearInterval(t)
  }, [refresh])

  async function submit(e: FormEvent) {
    e.preventDefault()
    setError('')
    try {
      const created = await api.createRun(prompt)
      setPrompt('')
      location.hash = `#/runs/${created.id}`
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not start the run')
    }
  }

  return (
    <div className="flex flex-col gap-8">
      <form onSubmit={submit} className="flex flex-col gap-3">
        <textarea
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
          rows={4}
          placeholder="What should the agent do? (read-only in phase 0)"
          className="rounded-lg border border-slate-700 bg-slate-900 px-3 py-2 outline-none focus:border-indigo-400"
        />
        <button
          disabled={prompt.trim() === ''}
          className="self-start rounded-lg bg-indigo-500 px-4 py-2 font-medium disabled:opacity-50"
        >
          Start run
        </button>
        {error && <p className="text-sm text-rose-400">{error}</p>}
      </form>

      <section className="flex flex-col gap-2">
        <h2 className="text-sm uppercase tracking-wide text-slate-400">Recent runs</h2>
        {runs.length === 0 && <p className="text-slate-500">No runs yet.</p>}
        {runs.map((r) => (
          <a
            key={r.id}
            href={`#/runs/${r.id}`}
            className="flex items-center gap-3 rounded-lg border border-slate-800 bg-slate-900/50 px-3 py-2 hover:border-slate-600"
          >
            <span className={`h-2.5 w-2.5 rounded-full ${statusColor[r.status]}`} />
            <span className="flex-1 truncate">{r.prompt}</span>
            <span className="text-xs text-slate-500">{new Date(r.createdAt).toLocaleString()}</span>
          </a>
        ))}
      </section>
    </div>
  )
}
