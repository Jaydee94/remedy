# Agent text as restricted Markdown

Status: decided by the maintainer on 2026-10-22. It amends section 9 of
[`2026-10-05-ui-conversation-redesign-design.md`](2026-10-05-ui-conversation-redesign-design.md), which said until that day that agent-written text
is rendered "as React text, never as HTML or Markdown".

## 1. Goal

Agents write Markdown: a bold label (`**Kurz:**`), backticks around a job name, `- ` lists, numbered steps. Shown as plain text, all of it appears raw, and a
numbered list of steps runs together as one paragraph (incident #39, an answer to "was war hier los?", is the case the maintainer pointed at). The goal is that
the agent's descriptive prose reads like a formatted message in the app's own design, with a uniform look for the cause and the steps, and that the trust
boundary of section 9 stays as strong as it was.

## 2. Decision and why

Markdown in agent prose **is** rendered, but only by our own restricted renderer: `web/src/agenttext.ts` parses a small CommonMark subset into a tree of plain
nodes, and `web/src/components/AgentText.tsx` turns that tree into React elements. No HTML is ever produced or parsed, and no Markdown library is added: a
library that can emit HTML, follow links or be configured into either would be a second place to audit, and the subset needed is small enough to own and test.

Until 2026-10-22 the rule was "never Markdown". It is changed because the raw markers made long answers hard to read, not because the data became more
trusted: agent text is still data, and every rule below is there so that rendering it cannot give it more power than a string had.

## 3. Security rules

- **React elements only.** No `dangerouslySetInnerHTML`, no HTML parsing. Raw HTML in the text (`<script>`, `<img onerror=...>`, `<a href>`) is literal text.
- **No link and no image is ever made from agent text.** `[label](url)` becomes the text `label (url)`; `![alt](url)` becomes `alt`; a bare URL or an
  autolink stays text. No element made from agent text carries an `href`, `src`, `style`, `class` or any other attribute derived from the text; the tree has
  no node type that could carry one (a test checks this as a property).
- **The text formats only inside its own block.** A heading becomes a small label inside the card or message (a `<p>` styled like the card's section titles),
  never an `h1` to `h6`: the page's heading outline is Remedy's. Agent text never decides the structure of one of Remedy's sentences.
- **Invisible and bidirectional controls are removed** before parsing (U+200B to U+200F, U+202A to U+202E, U+2060 to U+2064, U+2066 to U+2069, U+061C,
  U+FEFF, the C0 controls except newline and tab, DEL and the C1 controls), so text cannot hide or reorder content. U+2028 and U+2029 become newlines.
- **Bounded and total.** Input over 64 KiB (65536 UTF-16 units) is cut, and a note under the text says so. Lists nest at most two levels and inline
  formatting at most two; deeper markers are shown as text. Parsing is one pass in linear time: no regular expression with a nested quantifier runs on
  untrusted text, delimiter matching keeps a lower bound per marker so a failed search is never repeated, and backtick runs are matched through an index.
  The renderer never throws: on any internal error it falls back to the plain text.

## 4. Grammar

Blocks: paragraphs (separated by a blank line; a single newline inside one is a line break); unordered lists (`-`, `*`, `+`) and ordered lists (`1.` or
`1)`), which may start directly after a paragraph line (a bullet list always, an ordered list when it starts at 1, as in CommonMark); item text may continue
on indented lines and on unindented lazy lines; one nested level; headings `#` to `######` as labels; fenced code blocks (three or more backticks or tildes,
the info string ignored) as preformatted text; blockquotes (`>`, one level); a thematic break (`---`, `***`, `___`) as a divider. Everything else (tables,
HTML blocks, footnotes, setext headings, indented code) stays paragraph text.

Inline: `**bold**` and `__bold__`, `*italic*` and `_italic_` with the CommonMark flanking rules, so an underscore inside a word (`kube_state_metrics`,
`up{job="api_server"}`) is never emphasis; `` `code` `` with any backtick run length (a newline inside becomes a space); `~~strike~~`; backslash escapes of
ASCII punctuation. An unbalanced marker stays literal. CRLF and CR are normalised to LF.

## 5. Rendering

`AgentText` (`variant: 'body' | 'compact'`) uses the app's tokens only: serif 15.5 px, `leading-relaxed`, `text-foreground/90`; bold `font-semibold`; inline
code as the chip the file names use; unordered items with a small accent dot; **ordered lists as steps**, each with a round `bg-secondary` badge whose number
is in the accent colour, the confidence chip's family; headings as `text-xs font-semibold text-muted-foreground` labels; code blocks in a rounded bordered
block (`bg-background`, mono 12.5 px, no wrapping, horizontal scroll inside the block); blockquotes with a muted left border. Every container has `min-w-0`
and breaks long words, so a page at 390 px never scrolls sideways. `compact` tightens the spacing for the diagnosis card's sections. `AgentInline` renders the
inline subset only, for a one-line summary. `plainText(md, max)` strips the markers and returns one line, for previews.

## 6. Where it applies, and where it does not

Applies to descriptive agent prose: the diagnosis fields `cause` and `proposed_fix` (`DiagnosisMessage`), `cause`, `evidence` and the proposed action
(`OutageMessage`; "Nothing to do for now." stays Remedy's sentence), the summary of both (inline subset), the answer to a question in an incident thread and
the answer of an ad-hoc run on the run page. The previews in the sidebar and on Conversations, and the screen reader's announcement of a diagnosis, use
`plainText`.

Does not apply to: the arguments of a tool call and of an approval, a note the agent asks to add (it was approved word for word and is shown as it was
approved), commands, raw run output and steps, the prompt, host, file and resource names, a failed run's own text, skills (shown as text, see the skills
spec), and anything Remedy itself says. "A decision shows what runs" (section 9 of the redesign) is unchanged: those texts are exact.

## 7. Tests

`web/src/agenttext.test.ts`, pure and run by `node --test`, test first: the text of incident #39's answer (bold label line, then `- **Alarm:**` items with
inline code such as `absent(up{job="apiserver"})` and a code span that wraps onto the next line), steps, nested lists, a fence, headings, a quote, underscores
inside words, unbalanced markers, escapes, CRLF, empty input. Hostile input: `<script>`, `<img onerror>`, `[x](javascript:...)`, an image, bare and `data:`
URLs, bidirectional and zero-width controls, 200 KB of input (cut, and within a time bound), 10000 nested `*` and `_` markers, 50 nested list levels, and the
property that no node can carry a link or a source. The components are checked in a real browser at desktop width and at 390 px.

## 8. Risks

- **A parser bug can misformat text** (wrong emphasis, a list that swallows a line). It cannot create a link, an image, an attribute or a page heading, because
  the tree has no node for them; the worst case is text that looks wrong, and the plain text is one fallback away.
- **Formatting can make an agent's claim look more authoritative** (a bold "Fix:" line). The card still says that the maintainer must check it, and the
  confidence chip is Remedy's, not the agent's.
- **Divergence from CommonMark** on edge cases (rule of three for emphasis, link reference definitions, HTML entities shown literally) is accepted: the subset
  is for reading, not for round-tripping documents.
