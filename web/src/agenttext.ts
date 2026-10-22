/**
 * Agent-written prose as a small tree, for AgentText.tsx: a restricted subset of CommonMark, parsed here and nowhere else
 * (docs/specs/2026-10-22-agent-text-markdown-design.md).
 *
 * The tree has no node that could carry a link, an image, a source, a style or any other attribute: `[label](url)` becomes the text
 * "label (url)", `![alt](url)` becomes "alt", and HTML stays text. Every pass is linear: no regular expression with a nested quantifier
 * runs on the text, emphasis is matched with a lower bound per marker so a failed search is never repeated, and backtick runs are found
 * through an index. Nothing here throws to the caller: on an internal error the text comes back as plain paragraphs.
 */

export type Inline =
  | { type: 'text'; text: string }
  | { type: 'code'; text: string }
  | { type: 'break' }
  | { type: 'strong' | 'em' | 'del'; children: Inline[] }

export type Block =
  | { type: 'paragraph' | 'heading'; children: Inline[] }
  | { type: 'list'; ordered: boolean; start: number; items: Block[][] }
  | { type: 'codeblock'; text: string }
  | { type: 'quote'; children: Block[] }
  | { type: 'rule' }

export interface AgentDoc {
  blocks: Block[]
  /** The input was longer than MAX_INPUT and was cut. */
  truncated: boolean
}

/** The longest input that is read, in UTF-16 units (64 KiB); the rest is cut. */
export const MAX_INPUT = 65536
const MAX_LIST_DEPTH = 2
const MAX_INLINE_DEPTH = 2

// ---- Cleaning ----------------------------------------------------------------------------------------------------------------------

/** Controls that could hide or reorder text: removed. Newline and tab stay; CR, U+2028 and U+2029 have become newlines before. */
function dropped(c: number): boolean {
  return (
    (c < 0x20 && c !== 0x0a && c !== 0x09) ||
    (c >= 0x7f && c <= 0x9f) ||
    c === 0x061c ||
    (c >= 0x200b && c <= 0x200f) ||
    (c >= 0x202a && c <= 0x202e) ||
    (c >= 0x2060 && c <= 0x2064) ||
    (c >= 0x2066 && c <= 0x2069) ||
    c === 0xfeff
  )
}

/** Cuts the input to MAX_INPUT (never between the halves of a surrogate pair), normalises line ends and removes the controls. */
function prepare(md: string): { text: string; truncated: boolean } {
  let input = typeof md === 'string' ? md : ''
  let truncated = false
  if (input.length > MAX_INPUT) {
    let end = MAX_INPUT
    const last = input.charCodeAt(end - 1)
    if (last >= 0xd800 && last <= 0xdbff) end--
    input = input.slice(0, end)
    truncated = true
  }
  const parts: string[] = []
  let from = 0
  for (let i = 0; i < input.length; i++) {
    const c = input.charCodeAt(i)
    if (c === 0x0d || c === 0x2028 || c === 0x2029 || dropped(c)) {
      parts.push(input.slice(from, i))
      if (c === 0x0d) {
        parts.push('\n')
        if (input.charCodeAt(i + 1) === 0x0a) i++
      } else if (c === 0x2028 || c === 0x2029) {
        parts.push('\n')
      }
      from = i + 1
    }
  }
  parts.push(input.slice(from))
  return { text: parts.join(''), truncated }
}

// ---- Inline ------------------------------------------------------------------------------------------------------------------------

type Kind = 'strong' | 'em' | 'del'
type Delim = {
  k: 'delim'
  ch: '*' | '_' | '~'
  n: number
  canOpen: boolean
  canClose: boolean
  /** What this run opens, outermost first. */
  opens: Kind[]
  /** What this run closes, innermost first. */
  closes: Kind[]
}
type Tok = { k: 'text'; s: string } | { k: 'code'; s: string } | { k: 'br' } | Delim

const isSpace = (c: string) => c === '' || c === ' ' || c === '\n' || c === '\t' || /\s/u.test(c)
const isPunct = (c: string) => c !== '' && /[\p{P}\p{S}]/u.test(c)
const ASCII_PUNCT = '!"#$%&\'()*+,-./:;<=>?@[\\]^_`{|}~'

/** For each position, the next index at or after it whose character is in `set`, or -1. One pass from the right. */
function nextOf(s: string, test: (c: string) => boolean): Int32Array {
  const out = new Int32Array(s.length + 2).fill(-1)
  for (let i = s.length - 1; i >= 0; i--) out[i] = test(s[i]) ? i : out[i + 1]
  return out
}

interface LinkIndex {
  close: Int32Array
  open: Int32Array
  paren: Int32Array
  space: Int32Array
  newline: Int32Array
}

/** `[label](target)` at p, or `![alt](target)` when p is the `[` after a `!`: the end and the text it becomes. */
function readLink(s: string, p: number, idx: LinkIndex, image: boolean): { end: number; text: string } | null {
  const j = idx.close[p + 1]
  if (j < 0) return null
  const open = idx.open[p + 1]
  if (open >= 0 && open < j) return null
  const nl = idx.newline[p + 1]
  if (nl >= 0 && nl < j) return null
  if (s[j + 1] !== '(') return null
  const k = idx.paren[j + 2]
  if (k < 0) return null
  const space = idx.space[j + 2]
  if (space >= 0 && space < k) return null
  const label = s.slice(p + 1, j)
  const target = s.slice(j + 2, k)
  return { end: k + 1, text: image || target === '' ? label : `${label} (${target})` }
}

function tokenize(s: string): Tok[] {
  const toks: Tok[] = []
  const n = s.length
  let parts: string[] = []
  let from = 0
  const flush = (to: number) => {
    if (to > from) parts.push(s.slice(from, to))
    if (parts.length) {
      toks.push({ k: 'text', s: parts.join('') })
      parts = []
    }
  }

  // Backtick runs by length, with a cursor per length that only moves forward.
  const runs = new Map<number, number[]>()
  for (let i = 0; i < n; ) {
    if (s[i] !== '`') {
      i++
      continue
    }
    let j = i
    while (j < n && s[j] === '`') j++
    const list = runs.get(j - i)
    if (list) list.push(i)
    else runs.set(j - i, [i])
    i = j
  }
  const cursor = new Map<number, number>()

  let links: LinkIndex | null = null
  const linkIndex = (): LinkIndex =>
    (links ??= {
      close: nextOf(s, (c) => c === ']'),
      open: nextOf(s, (c) => c === '['),
      paren: nextOf(s, (c) => c === ')'),
      space: nextOf(s, (c) => isSpace(c)),
      newline: nextOf(s, (c) => c === '\n'),
    })

  let i = 0
  while (i < n) {
    const c = s[i]
    if (c === '\\' && i + 1 < n && ASCII_PUNCT.includes(s[i + 1])) {
      if (i > from) parts.push(s.slice(from, i))
      parts.push(s[i + 1])
      i += 2
      from = i
      continue
    }
    if (c === '\n') {
      flush(i)
      toks.push({ k: 'br' })
      i++
      from = i
      continue
    }
    if (c === '`') {
      let j = i
      while (j < n && s[j] === '`') j++
      const len = j - i
      const list = runs.get(len) ?? []
      let at = cursor.get(len) ?? 0
      while (at < list.length && list[at] < j) at++
      cursor.set(len, at)
      if (at < list.length) {
        const close = list[at]
        flush(i)
        let code = s.slice(j, close).split('\n').join(' ')
        if (code.length >= 2 && code.startsWith(' ') && code.endsWith(' ') && code.trim() !== '') code = code.slice(1, -1)
        toks.push({ k: 'code', s: code })
        i = close + len
        from = i
      } else {
        i = j // the run is text
      }
      continue
    }
    if (c === '[' || (c === '!' && s[i + 1] === '[')) {
      const image = c === '!'
      const link = readLink(s, image ? i + 1 : i, linkIndex(), image)
      if (link) {
        if (i > from) parts.push(s.slice(from, i))
        parts.push(link.text)
        i = link.end
        from = i
      } else {
        i += image ? 2 : 1
      }
      continue
    }
    if (c === '*' || c === '_' || c === '~') {
      let j = i
      while (j < n && s[j] === c) j++
      const len = j - i
      if (c === '~' && len !== 2) {
        i = j
        continue
      }
      const before = i > 0 ? s[i - 1] : ''
      const after = j < n ? s[j] : ''
      const left = !isSpace(after) && (!isPunct(after) || isSpace(before) || isPunct(before))
      const right = !isSpace(before) && (!isPunct(before) || isSpace(after) || isPunct(after))
      const canOpen = c === '_' ? left && (!right || isPunct(before)) : left
      const canClose = c === '_' ? right && (!left || isPunct(after)) : right
      if (!canOpen && !canClose) {
        i = j
        continue
      }
      flush(i)
      toks.push({ k: 'delim', ch: c, n: len, canOpen, canClose, opens: [], closes: [] })
      i = j
      from = i
      continue
    }
    i++
  }
  flush(n)
  return toks
}

/** Pairs the delimiter runs (CommonMark's process emphasis, without the rule of three), recording what each run opens and closes. */
function matchEmphasis(toks: Tok[]): void {
  const stack: number[] = []
  const bottom = { '*': 0, _: 0, '~': 0 }
  for (let t = 0; t < toks.length; t++) {
    const tok = toks[t]
    if (tok.k !== 'delim') continue
    if (tok.canClose) {
      while (tok.n > 0) {
        let found = -1
        for (let k = stack.length - 1; k >= bottom[tok.ch]; k--) {
          const o = toks[stack[k]] as Delim
          if (o.ch === tok.ch) {
            found = k
            break
          }
        }
        if (found < 0) {
          bottom[tok.ch] = stack.length
          break
        }
        const opener = toks[stack[found]] as Delim
        const use = tok.ch === '~' ? 2 : opener.n >= 2 && tok.n >= 2 ? 2 : 1
        const kind: Kind = tok.ch === '~' ? 'del' : use === 2 ? 'strong' : 'em'
        opener.n -= use
        tok.n -= use
        opener.opens.unshift(kind)
        tok.closes.push(kind)
        // The openers between the pair become text; an exhausted opener leaves the stack.
        stack.length = found + (opener.n > 0 ? 1 : 0)
        bottom['*'] = Math.min(bottom['*'], stack.length)
        bottom._ = Math.min(bottom._, stack.length)
        bottom['~'] = Math.min(bottom['~'], stack.length)
      }
    }
    if (tok.n > 0 && tok.canOpen) stack.push(t)
  }
}

const marker = (kind: Kind, ch: string) => (kind === 'strong' ? ch + ch : kind === 'del' ? '~~' : ch)

function pushText(into: Inline[], text: string) {
  if (text === '') return
  const last = into[into.length - 1]
  if (last && last.type === 'text') last.text += text
  else into.push({ type: 'text', text })
}

/** The matched runs as a tree; a pair deeper than MAX_INLINE_DEPTH is shown as its markers. */
function buildInline(toks: Tok[]): Inline[] {
  const root: Inline[] = []
  const frames: Array<{ kind: Kind; ch: string; into: Inline[]; node: Inline[] | null }> = []
  let cur = root
  let depth = 0
  for (const tok of toks) {
    if (tok.k === 'text') pushText(cur, tok.s)
    else if (tok.k === 'code') cur.push({ type: 'code', text: tok.s })
    else if (tok.k === 'br') cur.push({ type: 'break' })
    else {
      for (const kind of tok.closes) {
        const f = frames.pop()
        if (!f || f.kind !== kind) throw new Error('unbalanced emphasis')
        if (f.node) depth--
        cur = f.into
        if (!f.node) pushText(cur, marker(kind, f.ch))
      }
      pushText(cur, tok.ch.repeat(tok.n))
      for (const kind of tok.opens) {
        if (depth < MAX_INLINE_DEPTH) {
          const children: Inline[] = []
          cur.push({ type: kind, children })
          frames.push({ kind, ch: tok.ch, into: cur, node: children })
          cur = children
          depth++
        } else {
          pushText(cur, marker(kind, tok.ch))
          frames.push({ kind, ch: tok.ch, into: cur, node: null })
        }
      }
    }
  }
  if (frames.length) throw new Error('unclosed emphasis')
  return root
}

function parseInline(s: string): Inline[] {
  const toks = tokenize(s)
  matchEmphasis(toks)
  return buildInline(toks)
}

// ---- Blocks ------------------------------------------------------------------------------------------------------------------------

function indentOf(line: string): number {
  let i = 0
  while (i < line.length && line[i] === ' ') i++
  return i
}

const blank = (line: string) => line.trim() === ''

function fenceOpen(line: string): { ch: string; len: number; indent: number } | null {
  const indent = indentOf(line)
  if (indent > 3) return null
  const ch = line[indent]
  if (ch !== '`' && ch !== '~') return null
  let j = indent
  while (j < line.length && line[j] === ch) j++
  const len = j - indent
  if (len < 3) return null
  if (ch === '`' && line.indexOf('`', j) >= 0) return null
  return { ch, len, indent }
}

function fenceClose(line: string, ch: string, len: number): boolean {
  const indent = indentOf(line)
  if (indent > 3) return false
  let j = indent
  while (j < line.length && line[j] === ch) j++
  return j - indent >= len && line.slice(j).trim() === ''
}

function isRule(line: string): boolean {
  const indent = indentOf(line)
  if (indent > 3) return false
  const ch = line[indent]
  if (ch !== '-' && ch !== '*' && ch !== '_') return false
  let count = 0
  for (let i = indent; i < line.length; i++) {
    if (line[i] === ch) count++
    else if (line[i] !== ' ' && line[i] !== '\t') return false
  }
  return count >= 3
}

function heading(line: string): string | null {
  const indent = indentOf(line)
  if (indent > 3) return null
  let j = indent
  while (j < line.length && j - indent < 7 && line[j] === '#') j++
  const level = j - indent
  if (level < 1 || level > 6) return null
  if (j < line.length && line[j] !== ' ' && line[j] !== '\t') return null
  let text = line.slice(j).trim()
  let e = text.length
  while (e > 0 && text[e - 1] === '#') e--
  if (e === 0) text = ''
  else if (e < text.length && (text[e - 1] === ' ' || text[e - 1] === '\t')) text = text.slice(0, e).trimEnd()
  return text
}

function quoteContent(line: string): string | null {
  const indent = indentOf(line)
  if (indent > 3 || line[indent] !== '>') return null
  return line.slice(line[indent + 1] === ' ' ? indent + 2 : indent + 1)
}

interface Marker {
  ordered: boolean
  /** The bullet character, or the delimiter after the number. */
  ch: string
  start: number
  /** Where the item's content begins: lines indented this far belong to the item. */
  width: number
  content: string
}

function listMarker(line: string): Marker | null {
  const indent = indentOf(line)
  if (indent > 3) return null
  let j = indent
  let ordered = false
  let start = 1
  let ch = line[j]
  if (ch === '-' || ch === '*' || ch === '+') {
    j++
  } else {
    while (j < line.length && j - indent < 10 && line[j] >= '0' && line[j] <= '9') j++
    const digits = j - indent
    if (digits < 1 || digits > 9 || (line[j] !== '.' && line[j] !== ')')) return null
    ch = line[j]
    start = Number(line.slice(indent, j))
    ordered = true
    j++
  }
  if (j < line.length && line[j] !== ' ') return null
  let spaces = 0
  while (j + spaces < line.length && line[j + spaces] === ' ') spaces++
  if (j + spaces === line.length) return { ordered, ch, start, width: j + 1, content: '' }
  // More than four spaces: the content starts after one, as in CommonMark (the rest is indentation of the content).
  if (spaces > 4) return { ordered, ch, start, width: j + 1, content: line.slice(j + 1) }
  return { ordered, ch, start, width: j + spaces, content: line.slice(j + spaces) }
}

const sameList = (a: Marker, b: Marker) => a.ordered === b.ordered && a.ch === b.ch

interface Ctx {
  listDepth: number
  inQuote: boolean
}

/** Whether a line starts a block; with `interrupting`, only the blocks that may cut a paragraph short. */
function startsBlock(line: string, ctx: Ctx, interrupting: boolean): boolean {
  if (fenceOpen(line) || isRule(line) || heading(line) !== null) return true
  if (!ctx.inQuote && quoteContent(line) !== null) return true
  if (ctx.listDepth < MAX_LIST_DEPTH) {
    const m = listMarker(line)
    if (m && (!interrupting || (m.content.trim() !== '' && (!m.ordered || m.start === 1)))) return true
  }
  return false
}

function parseBlocks(lines: string[], ctx: Ctx): Block[] {
  const out: Block[] = []
  let para: string[] | null = null
  const flush = () => {
    if (para) out.push({ type: 'paragraph', children: parseInline(para.join('\n')) })
    para = null
  }
  let i = 0
  while (i < lines.length) {
    const line = lines[i]
    if (blank(line)) {
      flush()
      i++
      continue
    }
    if (para && !startsBlock(line, ctx, true)) {
      ;(para as string[]).push(line.trim())
      i++
      continue
    }
    flush()
    const fence = fenceOpen(line)
    if (fence) {
      const code: string[] = []
      i++
      while (i < lines.length && !fenceClose(lines[i], fence.ch, fence.len)) {
        const l = lines[i]
        code.push(l.slice(Math.min(indentOf(l), fence.indent)))
        i++
      }
      i++ // the closing fence, or past the end
      out.push({ type: 'codeblock', text: code.join('\n') })
      continue
    }
    if (isRule(line)) {
      out.push({ type: 'rule' })
      i++
      continue
    }
    const h = heading(line)
    if (h !== null) {
      out.push({ type: 'heading', children: parseInline(h) })
      i++
      continue
    }
    if (!ctx.inQuote && quoteContent(line) !== null) {
      const inner: string[] = []
      while (i < lines.length) {
        const q = quoteContent(lines[i])
        if (q !== null) inner.push(q)
        else if (!blank(lines[i]) && inner.length && !blank(inner[inner.length - 1]) && !startsBlock(lines[i], ctx, false)) inner.push(lines[i])
        else break
        i++
      }
      out.push({ type: 'quote', children: parseBlocks(inner, { ...ctx, inQuote: true }) })
      continue
    }
    const m = ctx.listDepth < MAX_LIST_DEPTH ? listMarker(line) : null
    if (m) {
      i = parseList(lines, i, m, ctx, out)
      continue
    }
    para = [line.trim()]
    i++
  }
  flush()
  return out
}

/** One list from line i on, appended to `out`; returns the line after it. */
function parseList(lines: string[], i: number, first: Marker, ctx: Ctx, out: Block[]): number {
  const items: Block[][] = []
  let m: Marker | null = first
  while (m) {
    const item: string[] = [m.content]
    let fence: { ch: string; len: number } | null = fenceOpen(m.content)
    i++
    while (i < lines.length) {
      const l = lines[i]
      if (blank(l)) {
        item.push('')
        i++
        continue
      }
      if (indentOf(l) >= m.width) {
        const inner = l.slice(m.width)
        if (fence) {
          if (fenceClose(inner, fence.ch, fence.len)) fence = null
        } else {
          fence = fenceOpen(inner)
        }
        item.push(inner)
        i++
        continue
      }
      const prev = item[item.length - 1]
      // A lazy line: the item's paragraph goes on without indentation.
      if (!fence && !blank(prev) && !startsBlock(l, ctx, false) && listMarker(l) === null) {
        item.push(l.trim())
        i++
        continue
      }
      break
    }
    while (item.length && blank(item[item.length - 1])) item.pop()
    items.push(parseBlocks(item, { ...ctx, listDepth: ctx.listDepth + 1 }))
    const next: Marker | null = i < lines.length ? listMarker(lines[i]) : null
    m = next && sameList(first, next) ? next : null
  }
  out.push({ type: 'list', ordered: first.ordered, start: first.start, items })
  return i
}

function expandIndent(line: string): string {
  let j = 0
  let width = 0
  while (j < line.length && (line[j] === ' ' || line[j] === '\t')) {
    width = line[j] === '\t' ? width + 4 - (width % 4) : width + 1
    j++
  }
  return j === 0 ? line : ' '.repeat(width) + line.slice(j)
}

function plainBlocks(text: string): Block[] {
  if (text.trim() === '') return []
  const children: Inline[] = []
  text.split('\n').forEach((line, i) => {
    if (i > 0) children.push({ type: 'break' })
    if (line !== '') children.push({ type: 'text', text: line })
  })
  return [{ type: 'paragraph', children }]
}

/** The agent's text as blocks. Never throws: on an internal error the text comes back as one plain paragraph. */
export function parseAgentText(md: string): AgentDoc {
  const { text, truncated } = prepare(md)
  try {
    return { blocks: parseBlocks(text.split('\n').map(expandIndent), { listDepth: 0, inQuote: false }), truncated }
  } catch {
    return { blocks: plainBlocks(text), truncated }
  }
}

/** The inline subset only (bold, italic, strike, code, line breaks), for a one-line summary: block markers stay text. */
export function parseAgentInline(md: string): { children: Inline[]; truncated: boolean } {
  const { text, truncated } = prepare(md)
  const lines = text.split('\n').map((l) => l.trim())
  try {
    return { children: parseInline(lines.join('\n').trim()), truncated }
  } catch {
    const p = plainBlocks(text)[0]
    return { children: p && p.type === 'paragraph' ? p.children : [], truncated }
  }
}

function inlineText(nodes: readonly Inline[], out: string[]) {
  for (const n of nodes) {
    if (n.type === 'text' || n.type === 'code') out.push(n.text)
    else if (n.type === 'break') out.push(' ')
    else inlineText(n.children, out)
  }
}

function blockText(blocks: readonly Block[], out: string[]) {
  for (const b of blocks) {
    if (b.type === 'paragraph' || b.type === 'heading') inlineText(b.children, out)
    else if (b.type === 'codeblock') out.push(b.text)
    else if (b.type === 'quote') blockText(b.children, out)
    else if (b.type === 'list') b.items.forEach((item) => blockText(item, out))
    out.push(' ')
  }
}

/** The text without its markers, on one line and at most `max` code points (then cut with "…"), for a preview. */
export function plainText(md: string, max: number): string {
  let flat: string
  try {
    const out: string[] = []
    blockText(parseAgentText(md).blocks, out)
    flat = out.join('')
  } catch {
    flat = prepare(md).text
  }
  flat = flat.replace(/\s+/g, ' ').trim()
  const points = Array.from(flat)
  return points.length > max ? `${points.slice(0, max).join('').trimEnd()}…` : flat
}
