import { useEffect, useId, useRef, useState } from 'react'
import type { FormEvent } from 'react'
import { api } from '@/api.ts'
import { validateCode } from '@/loginrelay.ts'
import {
  codeProblem,
  labels,
  opensByItself,
  sessionPhase,
  shownLogin,
  stepNames,
  terminalHeading,
  wizardView,
} from '@/loginwizard.ts'
import type { ActionId, Step, WizardAction } from '@/loginwizard.ts'
import { useAnnouncement } from '@/useAnnouncement.ts'
import { useFocusRestore } from '@/useFocusRestore.ts'
import { useLoginRelay } from '@/useLoginRelay.ts'
import ConfirmButton from '@/components/ConfirmButton'
import { Button, buttonVariants } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ariaDisabled } from '@/components/setup/ariaDisabled.ts'
import { cn } from '@/lib/utils'

interface Props {
  /** The terminal way to log in (the hint and the `kubectl exec` command), when the runner needs a login; null otherwise. */
  terminal: { hint: string | null; command: string } | null
  /** The runner is connected and logged in: a start then replaces the login, after a confirmation. */
  loggedIn: boolean
  /** Called with the session's state when it changes, so the runner card reads the login again (it turns green after a success). */
  onSessionChange: (state: string) => void
}

const steps: Step[] = [1, 2, 3, 4]
const idlePollMs = 10_000

function TerminalBlock({ terminal }: { terminal: NonNullable<Props['terminal']> }) {
  return (
    <>
      {terminal.hint && <p className="text-[13px] leading-relaxed text-muted-foreground">{terminal.hint}</p>}
      <code className="rounded-xl bg-muted px-3 py-2 font-mono text-xs break-words whitespace-pre-wrap text-foreground select-all">{terminal.command}</code>
    </>
  )
}

const nowhere = (el: Element | null) => el === null || el === document.body

/**
 * The login from the Setup page as a wizard of four steps inside the runner section (a step panel, not a modal). The step comes from the
 * server's state (`wizardView`) plus the client flag `typing`, so a reload lands on step 2 with an empty field. The code lives only in this
 * component's state until it is sent: cleared on send, on cancel, when the step leaves the code and with the component; never in storage,
 * a ref, an attribute other than the field's value, a message or a log. The address is only the link's `href` on step 2.
 */
export default function LoginWizard({ terminal, loggedIn, onSessionChange }: Props) {
  const [pollMs, setPollMs] = useState(idlePollMs)
  const relay = useLoginRelay(pollMs)
  const [opened, setOpened] = useState(false)
  const [typing, setTyping] = useState(false)
  const [code, setCode] = useState('')
  const [epoch, setEpoch] = useState(0)
  const [dismissed, setDismissed] = useState<ReadonlySet<string>>(() => new Set())
  const [message, setMessage] = useState('')
  const [copied, setCopied] = useState('')
  const [busy, setBusy] = useState(false)
  const busyRef = useRef(false)
  const [now, setNow] = useState(() => Date.now())
  const { target: headingRef, restore } = useFocusRestore<HTMLHeadingElement>()
  const openerRef = useRef<HTMLDivElement>(null)
  const codeId = useId()
  const headingId = useId()

  const shown = shownLogin(relay.offered, dismissed)
  const session = shown?.session ?? null
  const view = wizardView({ offered: shown, open: opened || opensByItself(relay.offered, dismissed), typing, now, loggedIn, epoch })
  // The view says how often to read the session again; the hook takes it on the next render.
  if (view.pollMs !== pollMs) setPollMs(view.pollMs)

  const history = view.announcement ? [view.announcement] : []
  const announcement = useAnnouncement(history, relay.offered !== null)

  // The countdown runs while a sign-in waits for its code.
  const counting = view.countdown !== null
  useEffect(() => {
    if (!counting) return
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [counting])

  // A new session or a fresh start after a rejected code begins without a code and on the link; a step other than 3 has no code. Both
  // are derived during the render, so no render shows a code on a step that has none.
  const phase = sessionPhase(session)
  const [seenPhase, setSeenPhase] = useState(phase)
  if (phase !== seenPhase) {
    setSeenPhase(phase)
    setTyping(false)
    setCode('')
  }
  if (view.step !== 3 && code !== '') setCode('')
  if (view.step !== 2 && copied !== '') setCopied('')

  const state = session?.state ?? ''
  useEffect(() => {
    onSessionChange(state)
  }, [state, onSessionChange])

  // On a step change the focus goes to the step heading when it was in the wizard or has fallen to the page (the panel is keyed by the
  // step, so its buttons are gone). Two exceptions: the field keeps it from step 2 to 3 (typing is what moves the wizard there), and a
  // maintainer who has moved elsewhere on the page keeps it. The first step after the load takes no focus.
  const lastStep = useRef<Step | null | undefined>(undefined)
  const panelRef = useRef<HTMLElement>(null)
  const step = view.step
  useEffect(() => {
    if (relay.offered === null) return
    const before = lastStep.current
    lastStep.current = step
    if (before === undefined || before === step) return
    const active = document.activeElement
    if (step === null) {
      if (nowhere(active)) openerRef.current?.querySelector('button')?.focus({ preventScroll: true })
      return
    }
    if (before === 2 && step === 3) return
    if (nowhere(active) || panelRef.current?.contains(active)) headingRef.current?.focus({ preventScroll: true })
  }, [step, relay.offered, headingRef])

  if (relay.absent || relay.offered === null || !relay.offered.available) {
    // The relay is off, not there, or not read yet: the section is what it was before it.
    return (
      <>
        {relay.error && <p className="text-[13px] text-muted-foreground">{relay.error}</p>}
        {terminal && <TerminalBlock terminal={terminal} />}
      </>
    )
  }

  async function request(fn: () => Promise<string>) {
    if (busyRef.current) return
    busyRef.current = true
    setBusy(true)
    setMessage('')
    setCopied('')
    void restore()
    try {
      setMessage(await fn())
    } finally {
      busyRef.current = false
      setBusy(false)
    }
  }

  const start = () =>
    request(async () => {
      if (session !== null) setDismissed((d) => new Set([...d, session.id]))
      setEpoch((e) => e + 1)
      setOpened(true)
      return relay.run('start', () => api.startRunnerLogin(loggedIn))
    })

  function cancel() {
    setCode('')
    setTyping(false)
    if (session === null || view.step === 4) {
      setOpened(false)
      setMessage('')
      return
    }
    const id = session.id
    void request(() => relay.run('cancel', () => api.cancelRunnerLogin(id)))
  }

  function send(e: FormEvent) {
    e.preventDefault()
    if (busyRef.current || session === null || view.step !== 3 || !view.actions.some((a) => a.id === 'send' && a.enabled)) return
    const checked = validateCode(code)
    if (!checked.ok) {
      setMessage(codeProblem)
      return
    }
    setCode('')
    const id = session.id
    void request(() => relay.run('code', () => api.sendRunnerLoginCode(id, checked.code)))
  }

  function back() {
    setCode('')
    setTyping(false)
    setMessage('')
    void relay.refresh()
  }

  function done() {
    if (session !== null) setDismissed((d) => new Set([...d, session.id]))
    setOpened(false)
    setMessage('')
    setCopied('')
  }

  function copy(href: string) {
    setCopied('')
    navigator.clipboard
      .writeText(href)
      .then(() => setCopied('I copied the link.'))
      .catch(() => setCopied('I could not copy the link. Open it with the button instead.'))
  }

  const handlers: Partial<Record<ActionId, () => void>> = {
    start: () => void start(),
    again: () => void start(),
    cancel,
    back,
    done,
  }

  function actionButton(a: WizardAction) {
    const off = !a.enabled || (busy && a.id !== 'done')
    const handler = handlers[a.id]
    return (
      <Button
        key={a.id}
        type="button"
        variant={a.id === 'start' || a.id === 'again' ? 'default' : 'outline'}
        aria-disabled={off || undefined}
        className={cn('h-10', ariaDisabled)}
        onClick={() => {
          if (off) return
          handler?.()
        }}
      >
        {a.label}
      </Button>
    )
  }

  const announcer = (
    // The span is keyed by the message: a new message with the same words still replaces the node and is announced.
    <p className="sr-only" aria-live="polite" aria-atomic="true">
      {announcement && <span key={announcement.key}>{announcement.text}</span>}
    </p>
  )

  if (view.step === null) {
    return (
      <>
        {announcer}
        <div ref={openerRef} className="flex flex-wrap items-center gap-2">
          {loggedIn ? (
            <ConfirmButton label={labels.openAgain} confirmLabel="Confirm: log in again" className="h-10" onConfirm={() => setOpened(true)} />
          ) : (
            <Button type="button" className="h-10" onClick={() => setOpened(true)}>
              {labels.open}
            </Button>
          )}
        </div>
        {message && (
          <p role="alert" className="text-[13px] text-destructive">
            {message}
          </p>
        )}
        {terminal && (
          <div className="flex flex-col gap-2">
            <h3 className="text-[13px] font-semibold text-muted-foreground">{terminalHeading}</h3>
            <TerminalBlock terminal={terminal} />
          </div>
        )}
      </>
    )
  }

  const current = view.step
  const showField = current === 3 || (current === 2 && view.link !== null)
  const sendAction = view.actions.find((a) => a.id === 'send')
  const sendOff = !sendAction?.enabled || busy
  const otherActions = view.actions.filter((a) => a.id !== 'send' && a.id !== 'open')

  return (
    <section ref={panelRef} aria-labelledby={headingId} className="flex flex-col gap-3 rounded-2xl border border-border bg-background p-4">
      {announcer}
      <div key={`top-${current}`} className="flex flex-col gap-3">
        <div className="flex flex-col gap-1.5">
          <p className="text-[12px] font-semibold text-muted-foreground">
            {view.indicator}: {view.stepName}
          </p>
          <ol aria-hidden className="grid grid-cols-4 gap-1">
            {steps.map((n) => (
              <li key={n} title={stepNames[n]} className={cn('h-1.5 rounded-full', n <= current ? 'bg-primary' : 'bg-muted')} />
            ))}
          </ol>
        </div>
        <h3 id={headingId} ref={headingRef} tabIndex={-1} className="font-serif text-[19px] leading-snug outline-none">
          {view.heading}
        </h3>
        {view.paragraphs.map((p, i) => (
          <p key={i} className="text-[14px] leading-relaxed text-pretty">
            {p}
          </p>
        ))}
        {view.link && (
          <div className="flex flex-wrap items-center gap-2">
            <a href={view.link.href} target="_blank" rel="noopener noreferrer" className={cn(buttonVariants({ variant: 'default' }), 'h-10')}>
              {labels.openLink}
            </a>
            <span className="text-[13px] text-muted-foreground">on {view.link.host}</span>
            <Button type="button" variant="outline" className="h-10" onClick={() => view.link && copy(view.link.href)}>
              {labels.copy}
            </Button>
          </div>
        )}
        {copied && (
          <p aria-live="polite" className="text-[13px] text-muted-foreground">
            {copied}
          </p>
        )}
      </div>
      {showField && (
        // Kept outside the keyed part: the field keeps the focus when typing moves the wizard from step 2 to step 3.
        <form key="code" onSubmit={send} autoComplete="off" className="flex flex-col gap-1.5">
          <label htmlFor={codeId} className="text-[14px] font-semibold">
            One-time code
          </label>
          <div className="flex flex-wrap items-center gap-2">
            <Input
              id={codeId}
              type="text"
              autoComplete="off"
              autoCorrect="off"
              autoCapitalize="off"
              spellCheck={false}
              data-1p-ignore
              data-lpignore="true"
              data-bwignore
              value={code}
              onChange={(e) => {
                setCode(e.target.value)
                if (e.target.value !== '') setTyping(true)
              }}
              className="h-11 max-w-80 min-w-0 flex-1 font-mono text-base md:text-sm"
            />
            {current === 3 && sendAction && (
              <Button type="submit" aria-disabled={sendOff || undefined} className={cn('h-10', ariaDisabled)}>
                {sendAction.label}
              </Button>
            )}
          </div>
        </form>
      )}
      <div key={`bottom-${current}`} className="flex flex-col gap-3">
        {view.countdown && <p className="text-[13px] text-muted-foreground">{view.countdown}</p>}
        {message && (
          <p role="alert" className="text-[13px] text-destructive">
            {message}
          </p>
        )}
        {otherActions.length > 0 && <div className="flex flex-wrap items-center gap-2">{otherActions.map(actionButton)}</div>}
        {view.showTerminal && terminal && (
          <div className="flex flex-col gap-2 border-t border-border pt-3">
            <h4 className="text-[13px] font-semibold text-muted-foreground">{terminalHeading}</h4>
            <TerminalBlock terminal={terminal} />
          </div>
        )}
      </div>
    </section>
  )
}
