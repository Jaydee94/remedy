import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { cleanText, MAX_INPUT, parseAgentInline, parseAgentText, plainText } from './agenttext.ts'
import type { Block, Inline } from './agenttext.ts'

// A compact form of the tree, so a case reads like its expected output. Test only: the app never makes markup from it.
function showInline(nodes: readonly Inline[]): string {
  return nodes
    .map((n) => {
      switch (n.type) {
        case 'text':
          return n.text
        case 'code':
          return `<code>${n.text}</code>`
        case 'break':
          return '<br>'
        case 'strong':
          return `<b>${showInline(n.children)}</b>`
        case 'em':
          return `<i>${showInline(n.children)}</i>`
        case 'del':
          return `<s>${showInline(n.children)}</s>`
      }
    })
    .join('')
}

function showBlocks(blocks: readonly Block[]): string {
  return blocks
    .map((b) => {
      switch (b.type) {
        case 'paragraph':
          return `<p>${showInline(b.children)}</p>`
        case 'heading':
          return `<h>${showInline(b.children)}</h>`
        case 'codeblock':
          return `<pre>${b.text}</pre>`
        case 'quote':
          return `<q>${showBlocks(b.children)}</q>`
        case 'rule':
          return '<hr>'
        case 'list':
          return `${b.ordered ? `<ol ${b.start}>` : '<ul>'}${b.items.map((item) => `<li>${showBlocks(item)}</li>`).join('')}${b.ordered ? '</ol>' : '</ul>'}`
      }
    })
    .join('')
}

const show = (md: string) => showBlocks(parseAgentText(md).blocks)

/** Every node of a tree, blocks and inlines. */
function allNodes(blocks: readonly Block[]): Array<Block | Inline> {
  const out: Array<Block | Inline> = []
  const inl = (nodes: readonly Inline[]) => {
    for (const n of nodes) {
      out.push(n)
      if ('children' in n) inl(n.children)
    }
  }
  const blk = (bs: readonly Block[]) => {
    for (const b of bs) {
      out.push(b)
      if (b.type === 'paragraph' || b.type === 'heading') inl(b.children)
      else if (b.type === 'quote') blk(b.children)
      else if (b.type === 'list') b.items.forEach(blk)
    }
  }
  blk(blocks)
  return out
}

const allText = (md: string) =>
  allNodes(parseAgentText(md).blocks)
    .map((n) => ('text' in n ? n.text : ''))
    .join('')

function listDepth(blocks: readonly Block[]): number {
  let max = 0
  for (const b of blocks) {
    if (b.type === 'list') max = Math.max(max, 1 + Math.max(0, ...b.items.map(listDepth)))
    else if (b.type === 'quote') max = Math.max(max, listDepth(b.children))
  }
  return max
}

function inlineDepth(nodes: readonly Inline[]): number {
  return Math.max(0, ...nodes.map((n) => ('children' in n ? 1 + inlineDepth(n.children) : 0)))
}

const timed = (f: () => void) => {
  const start = performance.now()
  f()
  return performance.now() - start
}

// The answer of incident #39 to "was war hier los?", word for word from the screenshot: a bold lead with a code chip, a bold label
// line and right under it (no blank line) items that start with a bold label and carry code chips and German quotation marks.
const kurz = '**Kurz:** Am 22.10.2026 gab es einen kritischen Alarm `KubeAPIDown`. Er hat sich nach etwa 15 Minuten von selbst erledigt.'
const items = [
  '**Was ich weiß:**',
  '- **Alarm:** Der Alarm kam von Alertmanager (`alertgroup: kubernetes-system-apiserver`, `job: apiserver`, Schweregrad critical). Die Beschreibung lautet: „KubeAPI has disappeared from Prometheus target discovery.“ Die Abfrage dahinter ist `absent(up{job="apiserver"})`. Es kamen also keine Metriken des Kubernetes-API-Servers mehr an.',
  '- **Beginn:** Der Alarm feuerte ab 05:41:30 UTC. Remedy hat den Incident um 05:42:12 geöffnet.',
].join('\n')
const screenshot = `${kurz}\n\n${items}`

const kurzHtml = '<b>Kurz:</b> Am 22.10.2026 gab es einen kritischen Alarm <code>KubeAPIDown</code>. Er hat sich nach etwa 15 Minuten von selbst erledigt.'
const listHtml =
  '<ul>' +
  '<li><p><b>Alarm:</b> Der Alarm kam von Alertmanager (<code>alertgroup: kubernetes-system-apiserver</code>, <code>job: apiserver</code>, Schweregrad critical). Die Beschreibung lautet: „KubeAPI has disappeared from Prometheus target discovery.“ Die Abfrage dahinter ist <code>absent(up{job="apiserver"})</code>. Es kamen also keine Metriken des Kubernetes-API-Servers mehr an.</p></li>' +
  '<li><p><b>Beginn:</b> Der Alarm feuerte ab 05:41:30 UTC. Remedy hat den Incident um 05:42:12 geöffnet.</p></li>' +
  '</ul>'

describe('parseAgentText: the answer of incident #39', () => {
  it('makes a bold lead, a bold label line and the two-item list right under it', () => {
    assert.equal(show(screenshot), `<p>${kurzHtml}</p><p><b>Was ich weiß:</b></p>${listHtml}`)
  })

  it('does the same without the blank line after the lead: the label line is a line break of the lead', () => {
    assert.equal(show(`${kurz}\n${items}`), `<p>${kurzHtml}<br><b>Was ich weiß:</b></p>${listHtml}`)
  })

  it('keeps a code span that wraps onto an unindented line in its item', () => {
    assert.equal(
      show('- **Ursache:** `kube-apiserver` wurde neu gestartet (`kubernetes-\nsystem-apiserver` meldete nichts).\n- **Jetzt:** gut'),
      '<ul><li><p><b>Ursache:</b> <code>kube-apiserver</code> wurde neu gestartet (<code>kubernetes- system-apiserver</code> meldete nichts).</p></li><li><p><b>Jetzt:</b> gut</p></li></ul>',
    )
  })

  it('makes numbered steps right after a label line', () => {
    assert.equal(
      show('**Was ich empfehle:**\n1. Prüfe `kubectl -n kube-system logs kube-apiserver`.\n2. Beobachte den Alarm.'),
      '<p><b>Was ich empfehle:</b></p><ol 1><li><p>Prüfe <code>kubectl -n kube-system logs kube-apiserver</code>.</p></li><li><p>Beobachte den Alarm.</p></li></ol>',
    )
  })
})

describe('parseAgentText: blocks', () => {
  it('returns nothing for empty and whitespace-only input', () => {
    assert.deepEqual(parseAgentText(''), { blocks: [], truncated: false })
    assert.deepEqual(parseAgentText('   \n\t\n  \n'), { blocks: [], truncated: false })
  })

  it('splits paragraphs at blank lines and keeps a single newline as a line break', () => {
    assert.equal(show('one\ntwo\n\nthree'), '<p>one<br>two</p><p>three</p>')
  })

  it('normalises CRLF and CR', () => {
    assert.equal(show('a\r\nb\r\n\r\nc\rd'), '<p>a<br>b</p><p>c<br>d</p>')
  })

  it('makes ordered steps with either delimiter and keeps the first number', () => {
    assert.equal(show('1. one\n2. two\n3. three'), '<ol 1><li><p>one</p></li><li><p>two</p></li><li><p>three</p></li></ol>')
    assert.equal(show('3) three\n4) four'), '<ol 3><li><p>three</p></li><li><p>four</p></li></ol>')
  })

  it('lets an ordered list interrupt a paragraph only when it starts at 1, as CommonMark does', () => {
    assert.equal(show('Steps:\n1. one\n2. two'), '<p>Steps:</p><ol 1><li><p>one</p></li><li><p>two</p></li></ol>')
    assert.equal(show('In the year\n2019. it broke'), '<p>In the year<br>2019. it broke</p>')
  })

  it('takes all three bullet markers', () => {
    assert.equal(show('- a\n* b'), '<ul><li><p>a</p></li></ul><ul><li><p>b</p></li></ul>')
    assert.equal(show('+ a\n+ b'), '<ul><li><p>a</p></li><li><p>b</p></li></ul>')
  })

  it('continues an item on indented and on lazy lines', () => {
    assert.equal(show('- first line\n  second line\nthird line\n- next'), '<ul><li><p>first line<br>second line<br>third line</p></li><li><p>next</p></li></ul>')
  })

  it('nests one level', () => {
    assert.equal(
      show('- a\n  - b\n  - c\n- d'),
      '<ul><li><p>a</p><ul><li><p>b</p></li><li><p>c</p></li></ul></li><li><p>d</p></li></ul>',
    )
    assert.equal(show('1. step\n   - detail\n2. next'), '<ol 1><li><p>step</p><ul><li><p>detail</p></li></ul></li><li><p>next</p></li></ol>')
  })

  it('keeps a list item with a blank line inside together', () => {
    assert.equal(show('1. one\n\n   more\n2. two'), '<ol 1><li><p>one</p><p>more</p></li><li><p>two</p></li></ol>')
  })

  it('makes headings small labels and strips a closing sequence', () => {
    assert.equal(show('# Title\n## Sub ##\ntext'), '<h>Title</h><h>Sub</h><p>text</p>')
    assert.equal(show('###### six\n####### seven'), '<h>six</h><p>####### seven</p>')
    assert.equal(show('#nospace'), '<p>#nospace</p>')
  })

  it('keeps a fenced block exactly, markers and all, and ignores the language', () => {
    assert.equal(show('```bash\nkubectl get pods\n  **not bold** <b>\n```\nafter'), '<pre>kubectl get pods\n  **not bold** <b></pre><p>after</p>')
    assert.equal(show('~~~\na\n~~~'), '<pre>a</pre>')
  })

  it('runs an unclosed fence to the end', () => {
    assert.equal(show('text\n```\ncode\nmore'), '<p>text</p><pre>code\nmore</pre>')
  })

  it('makes a quote, one level deep', () => {
    assert.equal(show('> **Hinweis:** a\n> b'), '<q><p><b>Hinweis:</b> a<br>b</p></q>')
    assert.equal(show('> > nested'), '<q><p>> nested</p></q>')
  })

  it('makes a divider from a thematic break', () => {
    assert.equal(show('a\n\n---\n\nb'), '<p>a</p><hr><p>b</p>')
    assert.equal(show('* * *'), '<hr>')
    assert.equal(show('___'), '<hr>')
  })

  it('leaves tables, HTML blocks and footnotes as paragraph text', () => {
    assert.equal(show('| a | b |\n|---|---|\n| 1 | 2 |'), '<p>| a | b |<br>|---|---|<br>| 1 | 2 |</p>')
    assert.equal(show('text[^1]\n\n[^1]: note'), '<p>text[^1]</p><p>[^1]: note</p>')
  })

  it('caps list nesting at two levels and keeps the text of the deeper ones', () => {
    const md = Array.from({ length: 50 }, (_, i) => `${'  '.repeat(i)}- level ${i}`).join('\n')
    const doc = parseAgentText(md)
    assert.ok(listDepth(doc.blocks) <= 2, `depth ${listDepth(doc.blocks)}`)
    const text = allText(md)
    for (let i = 0; i < 50; i++) assert.ok(text.includes(`level ${i}`), `level ${i} is lost`)
  })
})

describe('parseAgentText: inline', () => {
  it('makes bold, italic, strike and code', () => {
    assert.equal(show('**b** __b__ *i* _i_ ~~s~~ `c`'), '<p><b>b</b> <b>b</b> <i>i</i> <i>i</i> <s>s</s> <code>c</code></p>')
  })

  it('takes a double-backtick span with a backtick inside', () => {
    assert.equal(show('``a ` b``'), '<p><code>a ` b</code></p>')
    assert.equal(show('`` `x` ``'), '<p><code>`x`</code></p>')
  })

  it('never makes italics of underscores inside words', () => {
    assert.equal(show('kube_state_metrics and snake_case_name'), '<p>kube_state_metrics and snake_case_name</p>')
    assert.equal(show('up{job="api_server"} is_up_'), '<p>up{job="api_server"} is_up_</p>')
  })

  it('nests bold and italic', () => {
    assert.equal(show('***both***'), '<p><i><b>both</b></i></p>')
    assert.equal(show('**bold *and italic***'), '<p><b>bold <i>and italic</i></b></p>')
  })

  it('caps inline nesting at two and shows the deeper markers as text', () => {
    const doc = parseAgentText('**a *b ~~c~~ d* e**')
    const p = doc.blocks[0]
    assert.ok(p.type === 'paragraph')
    assert.ok(inlineDepth(p.children) <= 2)
    assert.equal(show('**a *b ~~c~~ d* e**'), '<p><b>a <i>b ~~c~~ d</i> e</b></p>')
  })

  it('leaves unbalanced markers literal', () => {
    assert.equal(show('**bold'), '<p>**bold</p>')
    assert.equal(show('a ** b'), '<p>a ** b</p>')
    assert.equal(show('*a **b*'), '<p>*a *<i>b</i></p>') // the nearest opener wins, as in CommonMark
    assert.equal(show('`open'), '<p>`open</p>')
    assert.equal(show('~single~'), '<p>~single~</p>')
  })

  it('takes backslash escapes of punctuation only', () => {
    assert.equal(show('\\*not italic\\* \\_x\\_ \\\\ \\a'), '<p>*not italic* _x_ \\ \\a</p>')
    assert.equal(show('\\# not a heading'), '<p># not a heading</p>')
  })

  it('keeps markers inside code literal', () => {
    assert.equal(show('`**x** _y_`'), '<p><code>**x** _y_</code></p>')
  })
})

describe('parseAgentText: hostile input', () => {
  const asText = (md: string, text: string) =>
    assert.deepEqual(parseAgentText(md).blocks, [{ type: 'paragraph', children: [{ type: 'text', text }] }])

  it('shows HTML as text', () => {
    asText('<script>alert(1)</script>', '<script>alert(1)</script>')
    asText('<img src=x onerror=alert(1)>', '<img src=x onerror=alert(1)>')
    asText('<a href="http://evil.test">click</a>', '<a href="http://evil.test">click</a>')
  })

  it('never makes a link: the label and the target are text', () => {
    asText('[x](javascript:alert(1))', 'x (javascript:alert(1))')
    asText('see [the docs](https://evil.test/a_b) now', 'see the docs (https://evil.test/a_b) now')
    asText('[x](data:text/html,<script>alert(1)</script>)', 'x (data:text/html,<script>alert(1)</script>)')
  })

  it('never makes an image: only the alt text is left', () => {
    asText('![a](http://evil.test/p.png)', 'a')
  })

  it('leaves bare URLs, autolinks and data URLs as text', () => {
    asText('open https://evil.test/x_y_z now', 'open https://evil.test/x_y_z now')
    asText('<https://evil.test>', '<https://evil.test>')
    asText('data:text/html;base64,PHNjcmlwdD4=', 'data:text/html;base64,PHNjcmlwdD4=')
  })

  it('removes bidirectional and invisible controls', () => {
    asText('a\u202Eb\u200Bc\uFEFFd\u2066e\u0007f\u200Fg\u061Ch\u2060i\u009Bj', 'abcdefghij')
    assert.equal(show('`a\u202Eb`'), '<p><code>ab</code></p>')
  })

  it('cuts input over 64 KiB, quickly, and says so', () => {
    const big = 'a *b* `c` **d** _e_ - [x](y)\n'.repeat(Math.ceil(200_000 / 30))
    let doc = parseAgentText('')
    const ms = timed(() => {
      doc = parseAgentText(big)
    })
    assert.ok(ms < 2000, `took ${ms} ms`)
    assert.equal(doc.truncated, true)
    assert.ok(allText(big).length <= MAX_INPUT)
    assert.equal(parseAgentText('short').truncated, false)
  })

  it('never cuts in the middle of a surrogate pair', () => {
    const doc = parseAgentText('a' + '😀'.repeat(MAX_INPUT)) // the plain cut would fall between the halves of a pair
    const p = doc.blocks[0]
    assert.ok(p.type === 'paragraph' && p.children[0].type === 'text')
    const t = p.children[0].type === 'text' ? p.children[0].text : ''
    assert.ok(!/[\uD800-\uDBFF]$/.test(t))
  })

  // Near MAX_INPUT, where a quadratic search shows. Each case also runs at a quarter of the size: a linear parser takes about four times
  // as long for four times the text, a quadratic one about sixteen times. The quarter's time has a floor of 1 ms so that timer noise
  // on a tiny number cannot fail a linear parser, and a factor only counts above 30 ms; the cross-kind cases (many openers of one marker, then many closers of another that
  // find none) are the ones a missing lower bound in the emphasis search makes quadratic.
  const halves = (a: string, b: string) => (size: number) => a.repeat(Math.floor(size / 2 / a.length)) + b.repeat(Math.floor(size / 2 / b.length))
  const scaled: Array<[string, (size: number) => string]> = [
    ['_ openers, * closers', halves('_a ', 'b* ')],
    ['* openers, _ closers', halves('*a ', 'b_ ')],
    ['~~ openers, * closers', halves('~~a ', 'b* ')],
    ['* openers, ~~ closers', halves('*a ', 'b~~ ')],
    ['_ openers, ~~ closers', halves('_a ', 'b~~ ')],
    ['* openers, * closers', halves('*a ', 'b** ')],
    ['unmatched runs', (size) => '*'.repeat(size / 2) + 'a' + '*'.repeat(size / 2)],
    ['nested runs', (size) => '**_~~'.repeat(size / 5)],
    ['backtick runs', (size) => { let out = ''; for (let i = 1; out.length < size; i++) out += '`'.repeat(i % 400 || 1) + 'x'; return out }],
    ['brackets', (size) => '['.repeat(size / 3) + ']('.repeat(size / 3)],
    ['quote markers', (size) => '> '.repeat(size / 2) + 'x'],
    ['list markers', (size) => '- '.repeat(size / 2) + 'x'],
    ['lazy items', (size) => '- a\nb *c _d\n'.repeat(size / 12)],
    ['link labels', (size) => '[*a](b) '.repeat(size / 8)],
  ]
  const best = (md: string) => {
    let min = Infinity
    for (let r = 0; r < 4; r++) min = Math.min(min, timed(() => parseAgentText(md)))
    return min
  }

  for (const [name, make] of scaled) {
    it(`stays linear on ${name}`, () => {
      const large = make(60_000)
      assert.ok(large.length > 50_000 && large.length <= MAX_INPUT, `size ${large.length}`)
      best(make(15_000)) // warm up
      const small = best(make(15_000))
      const big = best(large)
      assert.ok(big < 1000, `${name}: ${big.toFixed(1)} ms at 60 KB`)
      // Quadratic means both: a large factor and a time that is not small. A linear parser on a busy machine (make check runs the test
      // files in parallel) can show a factor near 8 at a few milliseconds; the quadratic one shows about 14 at 100 ms and more.
      const ratio = big / Math.max(small, 1)
      assert.ok(ratio < 8 || big < 30, `${name}: ${small.toFixed(2)} ms at 15 KB, ${big.toFixed(2)} ms at 60 KB`)
    })
  }

  it('never throws', () => {
    for (const md of ['\u0000', '\uD800', '``', '-', '1.', '>', '#', '```', '\\', '[', '](', '![', '***', '~~~~', '- - -', '\\\n', '[`](x)', '[**](x)']) {
      assert.doesNotThrow(() => parseAgentText(md))
      assert.doesNotThrow(() => plainText(md, 10))
      assert.doesNotThrow(() => parseAgentInline(md))
    }
  })

  const allowed: Record<string, readonly string[]> = {
    paragraph: ['type', 'children'],
    heading: ['type', 'children'],
    quote: ['type', 'children'],
    list: ['type', 'ordered', 'start', 'items'],
    codeblock: ['type', 'text'],
    rule: ['type'],
    text: ['type', 'text'],
    code: ['type', 'text'],
    break: ['type'],
    strong: ['type', 'children'],
    em: ['type', 'children'],
    del: ['type', 'children'],
  }
  const checkNodes = (md: string) => {
    for (const node of allNodes(parseAgentText(md).blocks)) {
      const keys = allowed[node.type]
      assert.ok(keys, `unexpected node type ${node.type} for ${JSON.stringify(md)}`)
      for (const k of Object.keys(node)) assert.ok(keys.includes(k), `node ${node.type} has ${k} for ${JSON.stringify(md)}`)
    }
  }

  it('has no node that could carry a link, a source or an attribute from the text', () => {
    for (const md of [
      screenshot,
      '[x](javascript:alert(1)) ![a](http://evil.test/p.png) <a href=x>y</a> <img src=x onerror=alert(1)>',
      '# h\n> q\n```js\ncode\n```\n---\n1. a\n   - b\n~~s~~ `c` *e* **s**',
      '<style>body{}</style> {style} class="x"',
      '[**x** `y`](javascript:alert(1)) ![*a*](data:image/png;base64,AAAA)',
    ])
      checkNodes(md)
  })

  it('has no such node for fuzzed input either (a seeded, deterministic generator)', () => {
    const pieces = ['*', '**', '_', '__', '~~', '`', '``', '[', ']', '(', ')', '![', '](', '\\', '\n', '\n\n', '> ', '- ', '1. ', '# ', '```', '---',
      '  ', 'a', 'b', ' ', '<a href="x">', '<img src=x>', 'javascript:', 'http://e.test/', '\u202E', '\u200B', '\u{E0041}', 'ä', '„', '\t']
    // xorshift32 with a fixed seed: every bit of the state is mixed, so `% n` uses all the pieces. (An LCG's low bits are degenerate:
    // with n divisible by 4 it drew almost only every fourth piece.) The state stays a 32-bit unsigned integer, exact in a double.
    let state = 20261022
    const next = (n: number) => {
      state ^= state << 13
      state >>>= 0
      state ^= state >>> 17
      state ^= state << 5
      state >>>= 0
      return state % n
    }
    const runs = 3000
    const used = new Set<number>()
    const documents = new Set<string>()
    for (let k = 0; k < runs; k++) {
      let md = ''
      const len = 1 + next(40)
      for (let i = 0; i < len; i++) {
        const p = next(pieces.length)
        used.add(p)
        md += pieces[p]
      }
      documents.add(md)
      assert.doesNotThrow(() => checkNodes(md), JSON.stringify(md))
      assert.doesNotThrow(() => parseAgentInline(md), JSON.stringify(md))
    }
    // The generator is worth something only when it draws every piece and rarely repeats a document.
    assert.equal(used.size, pieces.length, `drew ${used.size} of ${pieces.length} pieces`)
    assert.ok(documents.size >= runs * 0.95, `${documents.size} distinct documents of ${runs}`)
  })
})

describe('parseAgentText: after the review', () => {
  const asText = (md: string, text: string) =>
    assert.deepEqual(parseAgentText(md).blocks, [{ type: 'paragraph', children: [{ type: 'text', text }] }])

  it('removes every format character, variation selectors, the grapheme joiner and the Hangul filler', () => {
    asText('a\u{E0041}\u{E0042}b', 'ab') // tag characters
    asText('soft\u00ADhyphen', 'softhyphen')
    asText('a\u034Fb\u3164c\u180Ed\uFFF9e\uFE0Ff\u{E0100}g\uE000h', 'abcdefgh')
    assert.equal(cleanText('a\u{E007F}\u{1F600}\u200Db').text, 'a\u{1F600}b') // an emoji stays, the joiner goes
  })

  it('makes a lone U+2028 and U+2029 a line break', () => {
    assert.equal(show('a\u2028b'), '<p>a<br>b</p>')
    assert.equal(show('a\u2029b'), '<p>a<br>b</p>')
  })

  it('makes a backslash at the end of a line a line break', () => {
    assert.equal(show('a\\\nb'), '<p>a<br>b</p>')
    assert.equal(show('- a\\\n  b'), '<ul><li><p>a<br>b</p></li></ul>')
    assert.equal(show('a\\'), '<p>a\\</p>') // at the very end it is a backslash
  })

  it('formats a link label and keeps the target as text after it', () => {
    assert.equal(show('[`x`](y)'), '<p><code>x</code> (y)</p>')
    assert.equal(show('see [**the docs**](https://e.test) now'), '<p>see <b>the docs</b> (https://e.test) now</p>')
    assert.equal(show('![*alt*](http://evil.test/p.png)'), '<p><i>alt</i></p>')
    assert.equal(show('**a *[~~x~~](u)* b**'), '<p><b>a <i>~~x~~ (u)</i> b</b></p>') // the label counts towards the depth
  })

  it('takes a heading and a quote inside a list item', () => {
    assert.equal(show('- # Label\n  text'), '<ul><li><h>Label</h><p>text</p></li></ul>')
    assert.equal(show('- > quoted\n- plain'), '<ul><li><q><p>quoted</p></q></li><li><p>plain</p></li></ul>')
  })

  it('takes 20000 bare quote markers quickly', () => {
    let doc = parseAgentText('')
    const ms = timed(() => {
      doc = parseAgentText('>'.repeat(20000))
    })
    assert.ok(ms < 1000, `took ${ms} ms`)
    assert.equal(show('>'.repeat(5)), '<q><p>>>>></p></q>')
    assert.equal(doc.blocks.length, 1)
  })

  it('strips controls in plainText too', () => {
    assert.equal(plainText('a\u202Eb\u2066c\u200Bd\u{E0041}e', 10), 'abcde')
  })
})

describe('parseAgentInline', () => {
  it('says when it cut the input', () => {
    const doc = parseAgentInline('x'.repeat(MAX_INPUT + 10))
    assert.equal(doc.truncated, true)
    assert.equal(parseAgentInline('x').truncated, false)
  })

  it('takes the inline subset only: block markers stay text', () => {
    assert.deepEqual(parseAgentInline('**a** and `b`\n- c\n# d').children, [
      { type: 'strong', children: [{ type: 'text', text: 'a' }] },
      { type: 'text', text: ' and ' },
      { type: 'code', text: 'b' },
      { type: 'break' },
      { type: 'text', text: '- c' },
      { type: 'break' },
      { type: 'text', text: '# d' },
    ])
  })

  it('makes no link either', () => {
    assert.deepEqual(parseAgentInline('[x](javascript:alert(1))').children, [{ type: 'text', text: 'x (javascript:alert(1))' }])
  })
})

describe('plainText', () => {
  it('strips the markers and makes one line', () => {
    assert.equal(plainText('**Kurz:** Der `api` ist _weg_.\n\n- a\n- b', 100), 'Kurz: Der api ist weg. a b')
    assert.equal(plainText('# H\n\n```\ncode\n```\n> q\n\n---\n\n1. s', 100), 'H code q s')
  })

  it('cuts by code points with an ellipsis', () => {
    const line = plainText(screenshot, 40)
    assert.ok(line.startsWith('Kurz: Am 22.10.2026'))
    assert.ok(!line.includes('**'))
    assert.ok(line.endsWith('…'))
    assert.ok(Array.from(line).length <= 41)
  })

  it('keeps a link as text and returns empty for empty input', () => {
    assert.equal(plainText('[x](javascript:1)', 50), 'x (javascript:1)')
    assert.equal(plainText('', 10), '')
    assert.equal(plainText('  \n ', 10), '')
  })
})
