import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { byteLength, limitBytes } from './approvals.ts'

const size = (text: string) => new TextEncoder().encode(text).length

describe('limitBytes', () => {
  it('leaves a text within the limit unchanged', () => {
    assert.equal(limitBytes('abc', 500), 'abc')
  })

  it('cuts ASCII at exactly the limit', () => {
    assert.equal(limitBytes('a'.repeat(600), 500), 'a'.repeat(500))
  })

  it('cuts two-byte characters at 250 characters', () => {
    const out = limitBytes('ä'.repeat(300), 500)
    assert.equal(out, 'ä'.repeat(250))
    assert.equal(size(out), 500)
  })

  it('never ends in half a character', () => {
    const out = limitBytes('ä'.repeat(300), 499)
    assert.equal(out, 'ä'.repeat(249))
    assert.ok(!out.includes('\uFFFD'))
  })

  it('cuts four-byte characters at 125 emoji', () => {
    const out = limitBytes('😀'.repeat(200), 500)
    assert.equal(out, '😀'.repeat(125))
    assert.equal(size(out), 500)
  })

  it('handles an empty text and a limit of 0', () => {
    assert.equal(limitBytes('', 10), '')
    assert.equal(limitBytes('abc', 0), '')
    assert.equal(limitBytes('', 0), '')
  })

  it('always fits the limit', () => {
    const text = 'aä€😀'.repeat(50)
    for (let max = 0; max <= 120; max++) assert.ok(size(limitBytes(text, max)) <= max)
  })
})

describe('byteLength', () => {
  it('counts UTF-8 bytes, not characters', () => {
    assert.equal(byteLength(''), 0)
    assert.equal(byteLength('abc'), 3)
    assert.equal(byteLength('ä'), 2)
    assert.equal(byteLength('😀'), 4)
    assert.equal(byteLength('aä😀'), 7)
  })
})
