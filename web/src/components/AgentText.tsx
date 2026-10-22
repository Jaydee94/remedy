import { Fragment, useMemo } from 'react'
import type { ReactNode } from 'react'
import { parseAgentInline, parseAgentText } from '@/agenttext.ts'
import type { Block, Inline } from '@/agenttext.ts'
import { cn } from '@/lib/utils'

// Agent-written prose, formatted by our own restricted renderer (docs/specs/2026-10-22-agent-text-markdown-design.md). Every element
// here is made from a node of agenttext.ts and carries only fixed classes: nothing from the text becomes an attribute, a link, an
// image or a page heading, and the text itself reaches the page only as React text.

function inline(nodes: readonly Inline[], chip: string): ReactNode[] {
  return nodes.map((n, i) => {
    switch (n.type) {
      case 'text':
        return <Fragment key={i}>{n.text}</Fragment>
      case 'code':
        return (
          <code key={i} className={cn('rounded-md px-1.5 py-0.5 font-mono text-[0.86em] break-words', chip)}>
            {n.text}
          </code>
        )
      case 'break':
        return <br key={i} />
      case 'strong':
        return (
          <strong key={i} className="font-semibold text-foreground">
            {inline(n.children, chip)}
          </strong>
        )
      case 'em':
        return <em key={i}>{inline(n.children, chip)}</em>
      case 'del':
        return (
          <del key={i} className="text-muted-foreground">
            {inline(n.children, chip)}
          </del>
        )
    }
    return null
  })
}

function blocks(list: readonly Block[], level: number, chip: string): ReactNode[] {
  return list.map((b, i) => {
    switch (b.type) {
      case 'paragraph':
        return (
          <p key={i} className="text-pretty">
            {inline(b.children, chip)}
          </p>
        )
      case 'heading':
        // A label inside the agent's block, styled like the card's section titles: never a heading of the page.
        return (
          <p key={i} className="mt-1 font-sans text-xs font-semibold text-muted-foreground">
            {inline(b.children, chip)}
          </p>
        )
      case 'codeblock':
        return (
          <pre
            key={i}
            tabIndex={0}
            className="m-0 max-w-full overflow-x-auto rounded-xl border border-border bg-background px-3.5 py-3 font-mono text-[12.5px] leading-relaxed whitespace-pre text-foreground/90 outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
          >
            <code>{b.text}</code>
          </pre>
        )
      case 'quote':
        return (
          <blockquote key={i} className="m-0 flex min-w-0 flex-col gap-2 border-l-2 border-input pl-3.5 text-muted-foreground">
            {blocks(b.children, level, chip)}
          </blockquote>
        )
      case 'rule':
        return <hr key={i} className="my-1 border-border" />
      case 'list':
        return b.ordered ? (
          <ol key={i} role="list" className="m-0 flex min-w-0 list-none flex-col gap-2.5 p-0">
            {b.items.map((item, j) => (
              <li key={j} className="flex min-w-0 gap-3">
                <span className="mt-px flex h-6 min-w-6 shrink-0 items-center justify-center rounded-full bg-secondary px-1.5 font-sans text-xs font-semibold text-primary tabular-nums">
                  {b.start + j}
                </span>
                <div className="flex min-w-0 flex-1 flex-col gap-2">{blocks(item, level + 1, chip)}</div>
              </li>
            ))}
          </ol>
        ) : (
          <ul key={i} role="list" className="m-0 flex min-w-0 list-none flex-col gap-1.5 p-0">
            {b.items.map((item, j) => (
              <li key={j} className="flex min-w-0 gap-2.5">
                <span aria-hidden className={cn('mt-[0.66em] size-1.5 shrink-0 rounded-full', level === 0 ? 'bg-primary/80' : 'bg-muted-foreground')} />
                <div className="flex min-w-0 flex-1 flex-col gap-2">{blocks(item, level + 1, chip)}</div>
              </li>
            ))}
          </ul>
        )
    }
    return null
  })
}

// Inline code: the file-name chip inside a card (bg-background on bg-card); on the page itself that would vanish, so a step lighter.
const cardChip = 'bg-background'
const pageChip = 'bg-secondary'
const inlineChip = 'bg-secondary'

const bodyClass = 'flex min-w-0 flex-col font-serif text-[15.5px] leading-relaxed break-words text-foreground/90'

function docNodes(text: string, chip: string): ReactNode {
  const doc = parseAgentText(text)
  return (
    <>
      {blocks(doc.blocks, 0, chip)}
      {doc.truncated && <p className="font-sans text-[13px] text-muted-foreground">… The rest is cut: the text was longer than 64 KiB.</p>}
    </>
  )
}

function inlineNodes(text: string): ReactNode {
  const doc = parseAgentInline(text)
  return (
    <>
      {inline(doc.children, inlineChip)}
      {doc.truncated && '…'}
    </>
  )
}

/**
 * The nodes, or null when building them failed. Only intrinsic elements are built (no component of ours renders later), so an error
 * happens here, synchronously, and the caller falls back to the plain text.
 */
function attempt(build: () => ReactNode): ReactNode | null {
  try {
    return build()
  } catch {
    return null
  }
}

/** The agent's prose as formatted text. `compact` tightens the spacing, for the sections of a diagnosis card. */
export default function AgentText({ text, variant = 'body' }: { text: string; variant?: 'body' | 'compact' }) {
  const content = useMemo(() => attempt(() => docNodes(text, variant === 'compact' ? cardChip : pageChip)), [text, variant])
  return (
    <div className={cn(bodyClass, variant === 'compact' ? 'gap-2.5' : 'gap-3.5')}>
      {content ?? <p className="whitespace-pre-wrap">{text}</p>}
    </div>
  )
}

/** The inline subset only (bold, italic, code), for a one-line summary inside the caller's own element. */
export function AgentInline({ text }: { text: string }) {
  const content = useMemo(() => attempt(() => inlineNodes(text)), [text])
  return <>{content ?? text}</>
}
