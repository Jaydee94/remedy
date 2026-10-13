import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { argvOf, commandText, quoteWord } from './hostcmd.ts'

// The same cases as TestDisplayQuotesOnlyWhatNeedsIt in internal/hostexec/quote_test.go: the card must read what the control plane logs.
describe('commandText', () => {
  it('quotes a word only when it needs it, as the control plane does', () => {
    assert.equal(commandText(['docker', 'restart', 'remedy-drill']), 'docker restart remedy-drill')
    assert.equal(commandText(['systemctl', 'restart', 'node_exporter.service']), 'systemctl restart node_exporter.service')
    assert.equal(commandText(['echo', 'a b', "it's", '']), "echo 'a b' 'it'\\''s' ''")
    assert.equal(commandText(['ps', '-eo', 'pid,user,comm']), 'ps -eo pid,user,comm')
    assert.equal(commandText(['x', '$(id)', 'a;b']), "x '$(id)' 'a;b'")
  })

  it('quotes a single word', () => {
    assert.equal(quoteWord('plain-word_1.2'), 'plain-word_1.2')
    assert.equal(quoteWord('has space'), "'has space'")
    assert.equal(quoteWord(''), "''")
  })
})

describe('argvOf', () => {
  it('takes a list of strings and nothing else', () => {
    assert.deepEqual(argvOf({ host: 'nas', argv: ['df', '-h'] }), ['df', '-h'])
    for (const bad of [null, undefined, 'x', { argv: 'df' }, { argv: [1, 2] }, { argv: [] }, { argv: ['a', 3] }, {}, []]) {
      assert.equal(argvOf(bad), undefined)
    }
  })
})
