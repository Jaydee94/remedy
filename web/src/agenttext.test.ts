import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { MAX_INPUT, parseAgentInline, parseAgentText, plainText } from './agenttext.ts'
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

// The answer of incident #39 to "was war hier los?", rebuilt from the screenshot: a bold lead, a bold label line, then items that start
// with a bold label and carry inline code, one of them wrapped in the middle of a code span; then the steps.
const screenshot = [
  '**Kurz:** Der API-Server war etwa vier Minuten lang nicht erreichbar; der Alarm hat sich danach von selbst aufgelöst.',
  '',
  '**Was passiert ist:**',
  '- **Alarm:** Der Alarm kam von Alertmanager (`alertgroup: kubernetes-system-apiserver`, `job: apiserver`, Schweregrad critical). Die Regel prüft `absent(up{job="apiserver"})`.',
  '- **Ursache:** Der Scrape schlug fehl, weil `kube-apiserver` neu gestartet wurde (`kubernetes-',
  'system-apiserver` meldete in der Zeit keine Targets).',
  '- **Jetzt:** Alle Targets sind wieder `up`, und kube_state_metrics meldet keine Fehler.',
  '',
  '**Was ich empfehle:**',
  '1. Prüfe mit `kubectl -n kube-system logs kube-apiserver` die Zeit um den Neustart.',
  '2. Beobachte den Alarm noch einen Tag.',
].join('\n')

describe('parseAgentText: the answer of incident #39', () => {
  it('makes a lead paragraph, label lines, a list right after a label line and numbered steps', () => {
    assert.equal(
      show(screenshot),
      '<p><b>Kurz:</b> Der API-Server war etwa vier Minuten lang nicht erreichbar; der Alarm hat sich danach von selbst aufgelöst.</p>' +
        '<p><b>Was passiert ist:</b></p>' +
        '<ul>' +
        '<li><p><b>Alarm:</b> Der Alarm kam von Alertmanager (<code>alertgroup: kubernetes-system-apiserver</code>, <code>job: apiserver</code>, Schweregrad critical). Die Regel prüft <code>absent(up{job="apiserver"})</code>.</p></li>' +
        '<li><p><b>Ursache:</b> Der Scrape schlug fehl, weil <code>kube-apiserver</code> neu gestartet wurde (<code>kubernetes- system-apiserver</code> meldete in der Zeit keine Targets).</p></li>' +
        '<li><p><b>Jetzt:</b> Alle Targets sind wieder <code>up</code>, und kube_state_metrics meldet keine Fehler.</p></li>' +
        '</ul>' +
        '<p><b>Was ich empfehle:</b></p>' +
        '<ol 1>' +
        '<li><p>Prüfe mit <code>kubectl -n kube-system logs kube-apiserver</code> die Zeit um den Neustart.</p></li>' +
        '<li><p>Beobachte den Alarm noch einen Tag.</p></li>' +
        '</ol>',
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

  it('stays fast on thousands of unmatched or nested markers', () => {
    const cases = [
      '*'.repeat(10000) + 'a' + '*'.repeat(10000),
      '_'.repeat(10000) + 'a' + '_'.repeat(10000),
      '*a '.repeat(10000),
      '**_~~'.repeat(5000),
      Array.from({ length: 300 }, (_, i) => '`'.repeat(i + 1) + 'x').join(''),
      '['.repeat(10000) + ']('.repeat(10000),
      '> '.repeat(10000) + 'x',
      '- '.repeat(10000) + 'x',
      '#'.repeat(10000),
      ('*a _b ' + '\n').repeat(5000),
    ]
    for (const md of cases) {
      const ms = timed(() => parseAgentText(md))
      assert.ok(ms < 1000, `took ${ms} ms for ${md.slice(0, 20)}`)
    }
  })

  it('never throws', () => {
    for (const md of ['\u0000', '\uD800', '``', '-', '1.', '>', '#', '```', '\\', '[', '](', '![', '***', '~~~~', '- - -']) {
      assert.doesNotThrow(() => parseAgentText(md))
      assert.doesNotThrow(() => plainText(md, 10))
    }
  })

  it('has no node that could carry a link, a source or an attribute from the text', () => {
    const inputs = [
      screenshot,
      '[x](javascript:alert(1)) ![a](http://evil.test/p.png) <a href=x>y</a> <img src=x onerror=alert(1)>',
      '# h\n> q\n```js\ncode\n```\n---\n1. a\n   - b\n~~s~~ `c` *e* **s**',
      '<style>body{}</style> {style} class="x"',
    ]
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
    for (const md of inputs) {
      for (const node of allNodes(parseAgentText(md).blocks)) {
        const keys = allowed[node.type]
        assert.ok(keys, `unexpected node type ${node.type}`)
        for (const k of Object.keys(node)) assert.ok(keys.includes(k), `node ${node.type} has ${k}`)
      }
    }
  })
})

describe('parseAgentInline', () => {
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
    assert.ok(line.startsWith('Kurz: Der API-Server'))
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
