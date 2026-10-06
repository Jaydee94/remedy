import type { ReactNode } from 'react'
import { Link } from 'react-router'
import type { Diagnosis } from '@/api.ts'
import { categoryText, shortSha } from '@/incidents.ts'
import { Button, buttonVariants } from '@/components/ui/button'
import { cn } from '@/lib/utils'

const confidence = {
  high: { label: "I'm fairly sure", bars: 3, chip: 'bg-soft-diagnosing text-primary', bar: 'bg-primary' },
  medium: { label: 'I think so', bars: 2, chip: 'bg-secondary text-foreground/90', bar: 'bg-foreground/90' },
  low: { label: "I'm not sure", bars: 1, chip: 'bg-secondary text-muted-foreground', bar: 'bg-muted-foreground' },
} as const

interface Props {
  diagnosis: Diagnosis
  /** The commit the diagnosis is about, when it is not the failing one. */
  outdatedSha?: string
  /** The failing commit. */
  headSha: string
  /** The responder run that wrote it, for "See my work". */
  runId?: string
  busy?: boolean
  /** Starts a new diagnosis. Left out when none can be started. */
  onDiagnoseAgain?: () => void
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <h2 className="text-xs font-semibold text-muted-foreground">{title}</h2>
      {children}
    </div>
  )
}

/** Remedy's diagnosis of an incident. Every field is the agent's text and is only shown. */
export default function DiagnosisMessage({ diagnosis: d, outdatedSha, headSha, runId, busy, onDiagnoseAgain }: Props) {
  const c = confidence[d.confidence] ?? confidence.low
  return (
    <>
      <p className="font-serif text-[clamp(18px,2vw,21px)] leading-normal text-pretty break-words">{d.summary}</p>
      <div className="flex flex-wrap items-center gap-2">
        <span className={cn('flex items-center gap-2 rounded-2xl py-1.5 pr-3 pl-1.5 text-[13px] font-semibold', c.chip)}>
          <span aria-hidden className="flex gap-0.5">
            {[0, 1, 2].map((i) => (
              <span key={i} className={cn('h-3.5 w-1.5 rounded-sm', i < c.bars ? c.bar : 'bg-input')} />
            ))}
          </span>
          {c.label}
        </span>
        <span className="rounded-2xl bg-secondary px-3 py-1.5 text-[13px] text-foreground/90">{categoryText(d.category)}</span>
        {d.fix_looks_automatable && (
          <span className="rounded-2xl bg-secondary px-3 py-1.5 text-[13px] text-foreground/90">Small mechanical fix</span>
        )}
      </div>
      <div className="flex flex-col gap-4 rounded-[20px] border border-border bg-card p-5">
        <Section title="What went wrong">
          <p className="font-serif text-[15.5px] leading-relaxed text-pretty break-words whitespace-pre-wrap text-foreground/90">{d.cause}</p>
        </Section>
        {d.affected_files.length > 0 && (
          <Section title="Where">
            <ul className="m-0 flex list-none flex-wrap gap-1.5 p-0">
              {d.affected_files.map((f) => (
                <li key={f} className="rounded-lg bg-background px-2.5 py-1 font-mono text-[12.5px] break-all">
                  {f}
                </li>
              ))}
            </ul>
          </Section>
        )}
        <Section title="What I'd do">
          <p className="font-serif text-[15.5px] leading-relaxed text-pretty break-words whitespace-pre-wrap text-foreground/90">{d.proposed_fix}</p>
        </Section>
      </div>
      {outdatedSha && (
        <span className="text-[13px] text-primary">
          This is about commit {shortSha(outdatedSha)}. The failing commit is now {shortSha(headSha)}.
        </span>
      )}
      <span className="text-[13px] text-muted-foreground">
        Please check this before you act on it. I could only read; I didn't change anything.
      </span>
      {(runId || onDiagnoseAgain) && (
        <div className="flex flex-wrap gap-2">
          {runId && (
            <Link to={`/runs/${encodeURIComponent(runId)}`} className={buttonVariants({ variant: 'outline', size: 'sm' })}>
              See my work
            </Link>
          )}
          {onDiagnoseAgain && (
            <Button variant="outline" size="sm" disabled={busy} onClick={onDiagnoseAgain}>
              Diagnose again
            </Button>
          )}
        </div>
      )}
    </>
  )
}
