import { useMemo, useState } from 'react'
import { Link } from 'react-router'
import { api, ApiError } from './api.ts'
import type { Incident, Run } from './api.ts'
import {
  conclusionText,
  externalLinkClass,
  incidentStateColor,
  incidentStateText,
  reasonText,
  refLabel,
  safeUrl,
  shortSha,
  sourceLabel,
  timeAgo,
} from './incidents.ts'
import { runFailure } from './runview.ts'
import { useShellHeader, useShellState } from './shellContext.ts'
import { allAnnouncements, buildThread } from './thread.ts'
import type { ThreadItem } from './thread.ts'
import { dayTimeLabel } from './timeline.ts'
import { useAnnouncement } from './useAnnouncement.ts'
import { useClock } from './useClock.ts'
import { useFocusRestore } from './useFocusRestore.ts'
import { useIncidentThread } from './useIncidentThread.ts'
import { useToast } from './toast.ts'
import RefLink from './RefLink.tsx'
import ApprovalAsk from '@/components/conversation/ApprovalAsk'
import Composer from '@/components/conversation/Composer'
import DiagnosisMessage from '@/components/conversation/DiagnosisMessage'
import EventPill from '@/components/conversation/EventPill'
import FailCard from '@/components/conversation/FailCard'
import RemedyMessage from '@/components/conversation/RemedyMessage'
import UserBubble from '@/components/conversation/UserBubble'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button, buttonVariants } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

const when = (iso: string) => new Date(iso).toLocaleString()

const undiagnosedText = {
  fresh: "I haven't looked at this yet. I diagnose real failures on my own, within my limits. You can also start it by hand.",
  manual: "Cancelled and action-required results aren't diagnosed automatically. You can start it by hand.",
  failed: "My last diagnosis didn't finish.",
} as const

/** The incident as a conversation: its history, Remedy's diagnosis, what the maintainer asked, what waits for a decision. */
export default function IncidentThreadPage({ id }: { id: number }) {
  const { detail, runs, error, missing, reload } = useIncidentThread(id)
  const { asks, loaded } = useShellState()
  const toast = useToast()
  const now = useClock()
  const incident = detail?.incident
  useShellHeader(incident ? { title: incident.title, back: '/incidents' } : null)
  const [diagnoseBusy, setDiagnoseBusy] = useState(false)
  // The state the incident was in when starting the diagnosis failed: the message is only shown while it is still in that state.
  const [diagnoseFailure, setDiagnoseFailure] = useState<{ state: string; message: string } | null>(null)
  const diagnoseError = diagnoseFailure && diagnoseFailure.state === incident?.state ? diagnoseFailure.message : ''
  const [panelError, setPanelError] = useState('')
  const [panelBusy, setPanelBusy] = useState(false)

  const items = useMemo(
    () =>
      incident && detail
        ? buildThread({ incident, activity: detail.activity, questionRuns: runs.filter((r) => r.role === 'adhoc'),
            asks,
            // The runs are newest first.
            lastResponder: runs.find((r) => r.role === 'responder'),
            diagnosisRun: runs.find((r) => r.role === 'responder' && r.status === 'succeeded'),
          })
        : [],
    [incident, detail, runs, asks],
  )
  // The asks come from the shell: until it has answered once, an ask that is already pending would look like news.
  const history = useMemo(() => allAnnouncements(items), [items])
  const announcement = useAnnouncement(history.at(-1) ?? null, history, incident !== undefined && loaded)
  const { target: heading, restore } = useFocusRestore<HTMLHeadingElement>()

  if (missing) {
    return (
      <div className="mx-auto flex max-w-190 flex-col gap-3 px-4 py-10 md:px-10">
        <h1 className="font-serif text-3xl font-normal">I can't find that incident.</h1>
        <Link to="/incidents" className="text-primary hover:text-primary-hover">
          Back to the conversations
        </Link>
      </div>
    )
  }

  async function diagnose() {
    if (diagnoseBusy) return
    setDiagnoseBusy(true)
    setDiagnoseFailure(null)
    try {
      await api.diagnoseIncident(id)
      await reload()
      void restore()
    } catch (e) {
      setDiagnoseFailure({ state: incident?.state ?? '', message: e instanceof ApiError ? e.message : 'Could not start the diagnosis' })
    } finally {
      setDiagnoseBusy(false)
    }
  }

  async function ignore() {
    if (panelBusy) return
    setPanelBusy(true)
    setPanelError('')
    try {
      await api.ignoreIncident(id)
      await reload()
      toast.show(`Ignored #${id}.`, () => {
        if (panelBusy) return
        setPanelBusy(true)
        void api
          .unignoreIncident(id)
          .then(reload)
          .catch((e: unknown) => setPanelError(e instanceof ApiError ? e.message : 'Could not stop ignoring the incident'))
          .finally(() => setPanelBusy(false))
      })
    } catch (e) {
      setPanelError(e instanceof ApiError ? e.message : 'Could not ignore the incident')
    } finally {
      setPanelBusy(false)
    }
  }

  async function unignore() {
    if (panelBusy) return
    setPanelBusy(true)
    setPanelError('')
    try {
      await api.unignoreIncident(id)
      await reload()
      toast.show(`Stopped ignoring #${id}.`)
    } catch (e) {
      setPanelError(e instanceof ApiError ? e.message : 'Could not stop ignoring the incident')
    } finally {
      setPanelBusy(false)
    }
  }

  async function ask(text: string) {
    await api.createRun(text, true, false, id)
    await reload()
  }

  const diagnosing = incident?.state === 'diagnosing'

  function renderItem(item: ThreadItem, inc: Incident) {
    switch (item.type) {
      case 'event':
        return (
          <EventPill key={item.key} dot={item.dot} title={new Date(item.at).toLocaleString()}>
            {item.text}
          </EventPill>
        )
      case 'working':
        return (
          <RemedyMessage key={item.key} kind="working" meta={`Remedy · ${dayTimeLabel(item.at, now)}`}>
            <span className="text-[13px] text-muted-foreground">
              Reading the failure log and the repository. I can only read; I can't change anything.
            </span>
            {item.runId && (
              <div>
                <Link to={`/runs/${encodeURIComponent(item.runId)}`} className={buttonVariants({ variant: 'outline', size: 'sm' })}>
                  Follow along
                </Link>
              </div>
            )}
          </RemedyMessage>
        )
      case 'diagnosis':
        return (
          <RemedyMessage key={item.key} meta={`Remedy · ${dayTimeLabel(item.at, now)} · read the failure log and the repository at ${shortSha(item.sha)}`}>
            <DiagnosisMessage
              diagnosis={item.diagnosis}
              outdatedSha={item.outdated ? item.sha : undefined}
              headSha={inc.headSha}
              runId={item.runId}
              busy={diagnoseBusy || diagnosing}
              onDiagnoseAgain={inc.state === 'open' || inc.state === 'diagnosed' ? () => void diagnose() : undefined}
            />
            {diagnoseError && (
              <span role="alert" className="text-[13px] text-destructive">
                {diagnoseError}
              </span>
            )}
          </RemedyMessage>
        )
      case 'undiagnosed':
        return (
          <RemedyMessage key={item.key} meta={`Remedy · ${dayTimeLabel(item.at, now)}`}>
            {item.reason === 'source' ? (
              <p className="font-serif text-[17px] leading-normal text-pretty">I can't diagnose incidents from {item.sourceLabel} yet.</p>
            ) : (
              <>
                <p className="font-serif text-[17px] leading-normal text-pretty">{undiagnosedText[item.reason]}</p>
                {diagnoseError && (
                  <span role="alert" className="text-[13px] text-destructive">
                    {diagnoseError}
                  </span>
                )}
                <div>
                  <Button aria-disabled={diagnoseBusy} className="aria-disabled:pointer-events-none aria-disabled:opacity-50" onClick={() => void diagnose()}>
                    {item.reason === 'manual' ? 'Diagnose' : item.reason === 'failed' ? 'Diagnose again' : 'Diagnose now'}
                  </Button>
                  {item.reason === 'failed' && item.runId && (
                    <Link to={`/runs/${encodeURIComponent(item.runId)}`} className={cn(buttonVariants({ variant: 'outline' }), 'ml-2')}>
                      See my work
                    </Link>
                  )}
                </div>
              </>
            )}
          </RemedyMessage>
        )
      case 'ask':
        return (
          <RemedyMessage key={item.key} kind="ask" meta={`Remedy · asked ${timeAgo(item.call.requestedAt, now)}`}>
            <ApprovalAsk call={item.call} onChanged={() => void reload().then(restore)} />
          </RemedyMessage>
        )
      case 'question':
        return <UserBubble key={item.key}>{item.run.prompt}</UserBubble>
      case 'answer':
        return <AnswerMessage key={item.key} run={item.run} />
    }
  }

  return (
    <div className="flex min-h-full flex-wrap items-start">
      <div className="flex min-h-full max-w-220 min-w-0 flex-[1_1_520px] flex-col gap-5 px-4 py-5 md:px-11 md:py-10">
        {error && !detail && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        {!incident ? (
          !error && (
            <div className="flex flex-col gap-4">
              <Skeleton className="h-8 w-3/5" />
              <Skeleton className="h-24" />
              <Skeleton className="h-40" />
            </div>
          )
        ) : (
          <>
            <div className="flex flex-col gap-1">
              <span className="hidden text-[13px] text-muted-foreground md:block">
                {incident.source === 'github' ? `${incident.repo} · ${refLabel(incident.ref)}` : sourceLabel(incident.source)} · Incident #{incident.id}
              </span>
              <h1 ref={heading} tabIndex={-1} className="font-serif text-3xl font-normal break-words outline-none max-md:sr-only">
                {incident.title}
              </h1>
            </div>
            <p className="sr-only" aria-live="polite">
              {announcement}
            </p>
            {items.map((item) => renderItem(item, incident))}
            <Composer placeholder="Ask Remedy about this incident…" onSend={ask} />
          </>
        )}
      </div>

      {incident && (
        // Beside the thread from 1180 px on, below it before: the sidebar is 300 px (w-75), the thread needs its basis of 520 px, the panel is
        // 280 px and has margins of 28 px (md:m-7) on both sides, which is 1156 px, rounded up. Recompute 1180 if one of them changes.
        <aside className="m-4 flex min-w-65 flex-[1_1_100%] flex-col min-[1180px]:flex-[0_1_280px] gap-3.5 rounded-[20px] border border-border bg-sidebar p-5 md:m-7">
          <span className="text-xs font-semibold text-muted-foreground">About this incident</span>
          <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-2 text-[13px]">
            <dt className="text-subtle">State</dt>
            <dd className={cn('flex items-center gap-2', incidentStateText[incident.state])}>
              <span aria-hidden className={cn('size-2 rounded-full', incidentStateColor[incident.state])} />
              {incident.state}
            </dd>
            <dt className="text-subtle">Conclusion</dt>
            <dd>{conclusionText(incident.conclusion)}</dd>
            {incident.source === 'github' && (
              <>
                <dt className="text-subtle">Commit</dt>
                <dd className="font-mono text-[12.5px]">{shortSha(incident.headSha)}</dd>
              </>
            )}
            {incident.source !== 'github' && incident.severity && incident.severity !== 'none' && (
              <>
                <dt className="text-subtle">Severity</dt>
                <dd>{incident.severity}</dd>
              </>
            )}
            <dt className="text-subtle">Occurrences</dt>
            <dd>{incident.occurrences}</dd>
            <dt className="text-subtle">First seen</dt>
            <dd>{when(incident.firstSeen)}</dd>
            <dt className="text-subtle">Last seen</dt>
            <dd>{when(incident.lastSeen)}</dd>
            {incident.resolvedAt && (
              <>
                <dt className="text-subtle">Resolved</dt>
                <dd>
                  {when(incident.resolvedAt)} ({reasonText(incident.resolvedReason)})
                </dd>
              </>
            )}
          </dl>
          <div className="flex flex-col gap-1.5 text-[13px]">
            {incident.source === 'github' && <RefLink refName={incident.ref} url={incident.refUrl} />}
            {safeUrl(incident.checkUrl) && (
              <a href={safeUrl(incident.checkUrl)} target="_blank" rel="noreferrer" className={cn(externalLinkClass, 'text-primary')}>
                {incident.source === 'github' ? 'Open the check run ↗' : 'Open the source ↗'}
              </a>
            )}
          </div>
          {incident.details && Object.keys(incident.details).length > 0 && (
            <details className="text-[13px]">
              <summary className="cursor-pointer rounded-sm text-muted-foreground outline-none focus-visible:ring-3 focus-visible:ring-ring/50">Signal</summary>
              <pre className="mt-2 max-h-80 overflow-auto font-mono text-xs break-words whitespace-pre-wrap text-subtle">
                {JSON.stringify(incident.details, null, 2)}
              </pre>
            </details>
          )}
          {panelError && (
            <span role="alert" className="text-[13px] text-destructive">
              {panelError}
            </span>
          )}
          {incident.state === 'ignored' ? (
            <Button variant="outline" size="sm" className="self-start aria-disabled:pointer-events-none aria-disabled:opacity-50" aria-disabled={panelBusy} onClick={() => void unignore()}>
              Stop ignoring
            </Button>
          ) : (
            (incident.state === 'open' || incident.state === 'diagnosing' || incident.state === 'diagnosed') && (
              <Button variant="outline" size="sm" className="self-start aria-disabled:pointer-events-none aria-disabled:opacity-50" aria-disabled={panelBusy} onClick={() => void ignore()}>
                Ignore
              </Button>
            )
          )}
        </aside>
      )}
    </div>
  )
}

/** Remedy's answer to a question: working, the text, or why it failed. The steps are on the run page. */
function AnswerMessage({ run }: { run: Run }) {
  const now = useClock()
  const failure = runFailure(run)
  const phase = run.status
  return (
    <RemedyMessage kind={phase === 'queued' || phase === 'running' ? 'working' : 'idle'} meta={`Remedy · ${dayTimeLabel(run.finishedAt ?? run.startedAt ?? run.createdAt, now)}`}>
      {phase === 'queued' && <span className="text-[13px] text-muted-foreground">Waiting for the runner…</span>}
      {phase === 'running' && <span className="text-[13px] text-muted-foreground">Working…</span>}
      {phase === 'succeeded' && (
        <p className="font-serif text-[clamp(16.5px,1.9vw,19px)] leading-relaxed text-pretty break-words whitespace-pre-wrap">
          {run.result.trim() === '' ? 'Done. There is nothing more to say.' : run.result}
        </p>
      )}
      {failure && <FailCard title={failure.title}>{failure.text}</FailCard>}
      <div>
        <Link to={`/runs/${encodeURIComponent(run.id)}`} className={buttonVariants({ variant: 'outline', size: 'sm' })}>
          {phase === 'running' || phase === 'queued' ? 'Follow along' : 'See my work'}
        </Link>
      </div>
    </RemedyMessage>
  )
}
