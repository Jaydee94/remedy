import { ArrowUp } from 'lucide-react'
import { useEffect, useState } from 'react'
import type { FormEvent, KeyboardEvent } from 'react'
import { useNavigate } from 'react-router'
import { api, ApiError } from './api.ts'
import type { Capabilities, Run } from './api.ts'
import { askHint, recentRun, toggleCluster, toggleTools } from './askpage.ts'
import type { AskSwitches } from './askpage.ts'
import { useShellState } from './shellContext.ts'
import RecentRunRow from '@/components/ask/RecentRunRow'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

const POLL_MS = 3000

const chipClass = (on: boolean) =>
  cn(
    'h-9 rounded-full border px-4 text-[13px] outline-none transition-colors focus-visible:ring-3 focus-visible:ring-ring/50',
    on ? 'border-primary bg-soft-diagnosing font-semibold text-primary' : 'border-input text-muted-foreground hover:text-foreground',
  )

/** Where the maintainer starts a run by hand: the question, what the agent may use, and the runs of the past. */
export default function AskPage() {
  const navigate = useNavigate()
  const { asks } = useShellState()
  const [runs, setRuns] = useState<Run[] | null>(null)
  const [prompt, setPrompt] = useState('')
  const [switches, setSwitches] = useState<AskSwitches>({ tools: false, cluster: false })
  const [capabilities, setCapabilities] = useState<Capabilities | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [listError, setListError] = useState('')

  useEffect(() => {
    api.getCapabilities().then(setCapabilities).catch(() => setCapabilities(null))
  }, [])

  useEffect(() => {
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const tick = async () => {
      try {
        const list = await api.listRuns()
        if (!cancelled) {
          setRuns(list)
          setListError('')
        }
      } catch (e) {
        // the list keeps what it had; the page still works. Without a list there is something to say; the poll retries quietly.
        if (!cancelled) setListError(e instanceof ApiError ? e.message : 'Could not load the earlier conversations')
      }
      if (!cancelled) timer = setTimeout(() => void tick(), POLL_MS)
    }
    void tick()
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [])

  const ready = prompt.trim() !== '' && !busy

  async function send() {
    if (!ready) return
    setBusy(true)
    setError('')
    try {
      const created = await api.createRun(prompt.trim(), switches.tools, switches.cluster)
      void navigate(`/runs/${created.id}`)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Could not start the run')
      setBusy(false)
    }
  }

  function submit(e: FormEvent) {
    e.preventDefault()
    void send()
  }

  function onKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      e.preventDefault()
      void send()
    }
  }

  return (
    <div className="mx-auto flex max-w-205 flex-col gap-6 px-4 py-5 md:px-10 md:py-12">
      <h1 className="font-serif text-[clamp(26px,3vw,34px)] font-normal">What should I look into?</h1>

      <form
        onSubmit={submit}
        className="flex flex-col gap-3 rounded-3xl border border-field bg-card p-4 focus-within:border-ring focus-within:ring-3 focus-within:ring-ring/50"
      >
        <textarea
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
          onKeyDown={onKeyDown}
          rows={4}
          aria-label="Your question"
          placeholder="Ask me about an incident, a workload, a log…"
          className="w-full resize-y border-0 bg-transparent font-serif text-[17px] leading-relaxed text-foreground outline-none placeholder:text-subtle"
        />
        <div className="flex flex-wrap items-center gap-2">
          <button type="button" aria-pressed={switches.tools} onClick={() => setSwitches(toggleTools)} className={chipClass(switches.tools)}>
            Use gatekeeper tools
          </button>
          {capabilities?.cluster.read && (
            <button type="button" aria-pressed={switches.cluster} onClick={() => setSwitches(toggleCluster)} className={chipClass(switches.cluster)}>
              Read the cluster
            </button>
          )}
          <button
            type="submit"
            disabled={!ready}
            aria-label="Send"
            className={cn(
              'ml-auto flex size-11 shrink-0 items-center justify-center rounded-full text-primary-foreground outline-none focus-visible:ring-3 focus-visible:ring-ring/50',
              ready ? 'bg-primary' : 'bg-input',
            )}
          >
            <ArrowUp className="size-5" aria-hidden />
          </button>
        </div>
      </form>
      <p className="-mt-3 pl-1 text-[13px] text-muted-foreground">{askHint(switches, capabilities)}<span className="hidden sm:inline"> Cmd or Ctrl and Enter send.</span></p>
      {error && (
        <span role="alert" className="-mt-3 pl-1 text-[13px] text-destructive">
          {error}
        </span>
      )}

      <section className="flex flex-col gap-3">
        <h2 className="text-xs font-semibold text-muted-foreground">Earlier conversations</h2>
        {runs === null ? (
          listError ? (
            <Alert variant="destructive">
              <AlertDescription>{listError}</AlertDescription>
            </Alert>
          ) : (
            <Skeleton className="h-14" />
          )
        ) : runs.length === 0 ? (
          <p className="text-[13px] text-muted-foreground">Nothing yet. Ask me something above.</p>
        ) : (
          runs.map((r) => <RecentRunRow key={r.id} run={recentRun(r, asks)} />)
        )}
      </section>
    </div>
  )
}
