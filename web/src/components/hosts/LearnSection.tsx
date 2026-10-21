import { useEffect } from 'react'
import { Link } from 'react-router'
import { api } from '@/api.ts'
import type { Host, Skill } from '@/api.ts'
import type { Run } from '@/components/hosts/run'
import { LEARN_COST, LEARN_POLL_MS, learnActive, learnBlocked, learnOutcome, learnProgress, learnRunPath, learnStartedNotice, proposalsLine, proposalsWaiting } from '@/learn.ts'
import { useClock } from '@/useClock.ts'
import { Button } from '@/components/ui/button'

interface Props {
  host: Host
  /** The skills of this host, proposals included; null while they are not loaded. */
  skills: Skill[] | null
  busy: boolean
  run: Run
  /** Reads the host and its skills again. */
  onRefresh: () => Promise<void>
  /** One quiet poll; the page skips it in a hidden tab and while one of its requests runs. */
  onPoll: () => void
  onNotice: (text: string) => void
}

/**
 * "Get to know this host": starts the learning run, says what it costs and what it does, links to the run and to the review list, and shows
 * how the last run ended. While a run is going it asks for the host again every few seconds.
 */
export default function LearnSection({ host, skills, busy, run, onRefresh, onPoll, onNotice }: Props) {
  const now = useClock()
  const active = learnActive(host.learning)
  const blocked = learnBlocked(host, skills)
  const waiting = proposalsWaiting(skills)
  const path = learnRunPath(host.learning)

  useEffect(() => {
    if (!active) return
    const timer = setInterval(onPoll, LEARN_POLL_MS)
    return () => clearInterval(timer)
  }, [active, onPoll])

  // The page is read again after a failed start too: a run that another window started is then shown (the error stays, the focus stays).
  async function start() {
    if (busy || blocked !== '') return
    const started = await run(() => api.learnHost(host.id))
    if (started) onNotice(learnStartedNotice(host.name))
    await onRefresh()
  }

  const progress = learnProgress(host.learning)
  const outcome = learnOutcome(host.learning, now)
  const review = proposalsLine(waiting)

  return (
    <div className="flex flex-col gap-3 rounded-3xl border border-border bg-card p-5">
      <p className="text-[13px] text-muted-foreground">{LEARN_COST}</p>
      <div className="flex flex-wrap items-center gap-3">
        <Button
          variant="outline"
          size="sm"
          className="h-10 aria-disabled:pointer-events-none aria-disabled:opacity-50"
          aria-disabled={busy || blocked !== ''}
          aria-describedby={blocked !== '' ? 'learn-why' : undefined}
          onClick={() => void start()}
        >
          Get to know this host
        </Button>
        {progress && <span className="text-[13px] text-primary">{progress}</span>}
        {path && (
          <Link to={path} className="text-[13px] text-primary hover:text-primary-hover">
            {active ? 'Watch the run' : 'Open the last run'}
          </Link>
        )}
      </div>
      {blocked !== '' && (
        <p id="learn-why" className="text-[13px] text-muted-foreground">
          {blocked}
        </p>
      )}
      {outcome && <p className="text-[13px] text-muted-foreground">{outcome}</p>}
      {review && (
        <Link to="/skills" className="text-[13px] font-semibold text-primary hover:text-primary-hover">
          {review}
        </Link>
      )}
    </div>
  )
}
