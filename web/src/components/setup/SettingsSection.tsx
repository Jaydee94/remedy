import { useCallback, useEffect, useRef, useState } from 'react'
import type { FormEvent } from 'react'
import { api, ApiError } from '@/api.ts'
import type { Fact, Limits, Setting, SignalSource } from '@/api.ts'
import { groupSettings, inputText, isSecret, parseInput, replaceSetting, resetLabel, sourceBadge, unchanged, valueText } from '@/settings.ts'
import { diagnosisBar, limitsText } from '@/setup.ts'
import { signalLine } from '@/signals.ts'
import { useClock } from '@/useClock.ts'
import ConfirmButton from '@/components/ConfirmButton'
import SecretRow from '@/components/setup/SecretRow'
import { ariaDisabled } from '@/components/setup/ariaDisabled.ts'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

/** What a request at a row came to: `error` is the message for that row, empty on success (and when another request was running). */
interface Outcome {
  ok: boolean
  error: string
}

/** `action` returns the setting the server answered with, or nothing (a reset). `unrefreshed` is announced when the write worked and the refresh did not. */
type RunFn = (
  key: string,
  action: () => Promise<Setting | undefined>,
  said: (s: Setting) => string,
  unrefreshed: string,
) => Promise<Outcome>

/** The groups of settings that have a signal source, by title; a change in one refreshes the line about what I last heard from that source. */
const signalOfGroup: Record<string, string> = { Alertmanager: 'alertmanager', Metrics: 'metrics' }

/** What Remedy does on its own, how much of it, and the values behind it. The environment gives the defaults; a value set here wins. */
export default function SettingsSection() {
  const [settings, setSettings] = useState<Setting[] | null>(null)
  const [facts, setFacts] = useState<Fact[]>([])
  const [limits, setLimits] = useState<Limits | null>(null)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState<{ n: number; text: string } | null>(null)
  const [busy, setBusy] = useState(false)
  const [signals, setSignals] = useState<SignalSource[]>([])
  const busyRef = useRef(false)
  const now = useClock()

  const loadLimits = useCallback(() => {
    api.getLimits().then(setLimits).catch(() => undefined)
  }, [])

  /** A failure to load shows nothing: the line is an extra, and the rows work without it. */
  const loadSignals = useCallback(() => {
    api
      .getSignals()
      .then((p) => setSignals(p.sources))
      .catch(() => undefined)
  }, [])

  useEffect(() => {
    loadSignals()
    api
      .getSettings()
      .then((p) => {
        setSettings(p.settings)
        setFacts(p.facts)
      })
      .catch((e: unknown) => setError(e instanceof ApiError ? e.message : 'Could not load the settings'))
    loadLimits()
  }, [loadLimits, loadSignals])

  /** One request at a time. A failure goes back to the row that asked, which shows it under its field, not for the whole page. */
  const run: RunFn = async (key, action, said, unrefreshed) => {
    if (busyRef.current) return { ok: false, error: '' }
    busyRef.current = true
    setBusy(true)
    try {
      let written: Setting | undefined
      try {
        written = await action()
      } catch (e) {
        return { ok: false, error: e instanceof ApiError ? e.message : 'Request failed' }
      }
      // The write worked. What the server answered is applied at once, so the page never keeps an older (possibly wider) value; the
      // refresh that follows is best effort, and when it fails that is said in the live region, not at the row.
      if (written) {
        const next = written
        setSettings((prev) => (prev ? replaceSetting(prev, next) : prev))
      }
      let text = unrefreshed
      try {
        const fresh = await api.getSettings()
        setSettings(fresh.settings)
        setFacts(fresh.facts)
        const s = fresh.settings.find((x) => x.key === key)
        text = s ? said(s) : 'Done.'
        loadLimits()
      } catch {
        // keep `unrefreshed`
      }
      // What I know about a source follows its settings (the address and the token).
      if (signalOfGroup[settings?.find((x) => x.key === key)?.group ?? '']) loadSignals()
      setNotice((prev) => ({ n: (prev?.n ?? 0) + 1, text }))
      return { ok: true, error: '' }
    } finally {
      busyRef.current = false
      setBusy(false)
    }
  }

  const bar = limits ? diagnosisBar(limits) : null

  /** What I last heard from the source that belongs to a group of settings, if it has one and it has been listed. */
  const signalNote = (title: string) => {
    const signal = signals.find((x) => x.name === signalOfGroup[title])
    return signal ? <p className="border-t border-border pt-3 text-[13px] text-muted-foreground">{signalLine(signal, now)}</p> : null
  }

  return (
    <section className="flex flex-col gap-3.5">
      <h2 className="text-xs font-semibold text-muted-foreground">My limits</h2>
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      {!settings && !error && <Skeleton className="h-24" />}
      {limits && (
        <div className="flex flex-col gap-4 rounded-3xl border border-border bg-card p-5">
          <p className="font-serif text-[17px] leading-relaxed text-pretty">{limitsText(limits)}</p>
          {bar && (
            <div className="flex flex-col gap-2">
              <span className="text-[13px] text-muted-foreground">
                Automatic diagnoses in the last 24 hours: {bar.used} of {bar.max}
              </span>
              <div
                role="progressbar"
                aria-label="Automatic diagnoses in the last 24 hours"
                aria-valuemin={0}
                aria-valuemax={bar.max}
                aria-valuenow={Math.min(bar.used, bar.max)}
                aria-valuetext={`${bar.used} of ${bar.max}`}
                className="h-2 overflow-hidden rounded-full bg-input"
              >
                <div className={cn('h-full rounded-full', bar.full ? 'bg-destructive' : 'bg-primary')} style={{ width: `${Math.round(bar.ratio * 100)}%` }} />
              </div>
            </div>
          )}
        </div>
      )}

      <div role="status" aria-live="polite" className="text-[13px] text-muted-foreground empty:hidden">
        {notice && <span key={notice.n}>{notice.text}</span>}
      </div>

      {settings &&
        groupSettings(settings).map((g) => (
          <div key={g.title} className="flex flex-col gap-3 rounded-3xl border border-border bg-card p-5">
            <h3 className="text-[13px] font-semibold text-muted-foreground">{g.title}</h3>
            {g.settings.map((s) =>
              isSecret(s) ? (
                <SecretRow
                  key={s.key}
                  setting={s}
                  busy={busy}
                  onSave={async (value) =>
                    (
                      await run(
                        s.key,
                        () => api.putSetting(s.key, value),
                        (n) => `${n.label}: saved.`,
                        `${s.label}: saved. I could not refresh the page; reload it to be sure.`,
                      )
                    ).error
                  }
                  onRemove={async () =>
                    (
                      await run(
                        s.key,
                        () => api.deleteSetting(s.key).then(() => undefined),
                        (n) => `${n.label}: removed.`,
                        `${s.label}: removed. I could not refresh the page; reload it to be sure.`,
                      )
                    ).error
                  }
                />
              ) : (
                <Row key={s.key} setting={s} busy={busy} run={run} />
              ),
            )}
            {signalNote(g.title)}
          </div>
        ))}

      {facts.length > 0 && (
        <div className="flex flex-col gap-2 rounded-3xl border border-border bg-card p-5">
          <h3 className="text-[13px] font-semibold text-muted-foreground">Set outside this page</h3>
          <dl className="flex flex-col gap-1 text-[14px]">
            {facts.map((f) => (
              <div key={f.label} className="flex flex-wrap gap-x-2">
                <dt className="text-muted-foreground">{f.label}:</dt>
                <dd className="break-all">{f.value}</dd>
              </div>
            ))}
          </dl>
          <span className="text-[13px] text-subtle">These come from the deployment (Helm values or the environment). They cannot be changed here.</span>
        </div>
      )}
    </section>
  )
}

function Row({ setting: s, busy, run }: { setting: Setting; busy: boolean; run: RunFn }) {
  // The row is never remounted after a save, so the Save button keeps the focus. The field follows the server's value instead: when the
  // value the setting shows changes, the text becomes it (derived during the render, like useAnnouncement does).
  const current = inputText(s)
  const [shown, setShown] = useState(current)
  const [text, setText] = useState(current)
  if (shown !== current) {
    setShown(current)
    setText(current)
  }
  const [problem, setProblem] = useState('')
  const inputRef = useRef<HTMLInputElement>(null)
  const id = `setting-${s.key}`
  const isList = s.kind === 'list'

  async function save() {
    if (busy || unchanged(s, text)) return
    const parsed = parseInput(s, text)
    if (!parsed.ok) {
      setProblem(parsed.message)
      return
    }
    const out = await run(
      s.key,
      () => api.putSetting(s.key, parsed.value),
      (n) => `${n.label}: now ${valueText(n)}.`,
      `${s.label}: saved. I could not refresh the page; reload it to be sure.`,
    )
    setProblem(out.error)
  }

  async function reset() {
    if (busy) return
    const out = await run(
      s.key,
      () => api.deleteSetting(s.key).then(() => undefined),
      (n) => `${n.label}: back to ${valueText(n)} (${n.source}).`,
      `${s.label}: reset. I could not refresh the page; reload it to be sure.`,
    )
    setProblem(out.error)
    // The reset button is gone once the value is no longer set here: the focus goes to the field, not to the page.
    if (out.ok) inputRef.current?.focus()
  }

  function onSubmit(e: FormEvent) {
    e.preventDefault()
    if (!isList) void save()
  }

  const resetText = resetLabel(s)
  const same = unchanged(s, text)
  return (
    <form onSubmit={onSubmit} className="flex flex-col gap-1.5 border-t border-border pt-3 first:border-t-0 first:pt-0" autoComplete="off">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <label htmlFor={id} className="text-[15px] font-semibold">
          {s.label}
        </label>
        <span className="rounded-full bg-muted px-2.5 py-0.5 text-[12px] text-muted-foreground">{sourceBadge(s.source)}</span>
      </div>
      <span className="text-[13px] text-muted-foreground">
        {s.rule} {s.effect}
      </span>
      {s.problem && <span className="text-[13px] text-destructive">{s.problem}</span>}
      <div className="flex flex-wrap items-center gap-2">
        <Input
          ref={inputRef}
          id={id}
          value={text}
          onChange={(e) => {
            setText(e.target.value)
            setProblem('')
          }}
          inputMode={s.kind === 'number' ? 'numeric' : undefined}
          spellCheck={false}
          aria-describedby={problem ? `${id}-problem` : undefined}
          aria-invalid={problem ? true : undefined}
          className="h-11 max-w-64 text-base md:text-sm"
        />
        {isList ? (
          // Never `disabled`: a disabled button drops the keyboard focus. save() already does nothing while busy or unchanged.
          <ConfirmButton label="Save" confirmLabel="Confirm: change where I may act" className="h-10" onConfirm={() => void save()} />
        ) : (
          <Button type="submit" aria-disabled={busy || same} className={cn('h-10', ariaDisabled)}>
            Save
          </Button>
        )}
        {resetText && (
          <Button type="button" variant="ghost" aria-disabled={busy} className={cn('h-10', ariaDisabled)} onClick={() => void reset()}>
            {resetText}
          </Button>
        )}
      </div>
      {problem && (
        <span id={`${id}-problem`} role="alert" className="text-[13px] text-destructive">
          {problem}
        </span>
      )}
    </form>
  )
}
