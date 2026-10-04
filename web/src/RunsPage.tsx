import { useCallback, useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { Link, useNavigate } from 'react-router'
import { api, ApiError } from './api.ts'
import type { Run } from './api.ts'
import { statusColor } from './status.ts'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'

export default function RunsPage() {
  const navigate = useNavigate()
  const [runs, setRuns] = useState<Run[]>([])
  const [prompt, setPrompt] = useState('')
  const [tools, setTools] = useState(false)
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
      const created = await api.createRun(prompt, tools)
      setPrompt('')
      navigate(`/runs/${created.id}`)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not start the run')
    }
  }

  return (
    <div className="flex flex-col gap-8">
      <h1 className="text-2xl font-semibold tracking-tight">Runs</h1>

      <Card>
        <CardHeader>
          <CardTitle>Start a run</CardTitle>
        </CardHeader>
        <CardContent>
          <form onSubmit={submit} className="flex flex-col gap-3">
            <Textarea
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
              rows={4}
              placeholder="What should the agent do?"
              aria-label="Prompt"
            />
            <div className="flex items-center gap-3">
              <Switch id="tools" checked={tools} onCheckedChange={setTools} />
              <Label htmlFor="tools">Allow gatekeeper tools</Label>
            </div>
            <p className="text-sm text-muted-foreground">
              {tools
                ? 'The agent can read incidents and ask to add a note to one. Every note waits for your decision under Approvals, and the run waits with it.'
                : 'The agent can only read the files of its workspace.'}
            </p>
            <Button type="submit" className="self-start" disabled={prompt.trim() === ''}>
              Start run
            </Button>
            {error && (
              <Alert variant="destructive">
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            )}
          </form>
        </CardContent>
      </Card>

      <section className="flex flex-col gap-2">
        <h2 className="text-sm uppercase tracking-wide text-muted-foreground">Recent runs</h2>
        {runs.length === 0 && <p className="text-muted-foreground">No runs yet.</p>}
        {runs.map((r) => (
          <Link
            key={r.id}
            to={`/runs/${r.id}`}
            className="flex items-center gap-3 rounded-lg border border-border bg-card/50 px-3 py-2 transition-colors hover:border-ring"
          >
            <span className={`h-2.5 w-2.5 rounded-full ${statusColor[r.status]}`} />
            <span className="flex-1 truncate">{r.prompt}</span>
            {r.mcp && <span className="text-xs text-muted-foreground">tools</span>}
            <span className="text-xs text-muted-foreground">{new Date(r.createdAt).toLocaleString()}</span>
          </Link>
        ))}
      </section>
    </div>
  )
}
