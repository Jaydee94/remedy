import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import type { Setting } from './api.ts'
import { groupSettings, inputText, isSecret, parseInput, secretLine, replaceSetting, resetLabel, sourceBadge, unchanged, valueText } from './settings.ts'

const setting = (over: Partial<Setting> = {}): Setting => ({
  key: 'poll_interval',
  group: 'Polling',
  label: 'How often I check GitHub',
  kind: 'duration',
  value: '1m',
  source: 'default',
  fallback: 'default',
  env: 'REMEDY_POLL_INTERVAL',
  rule: 'At least 10s.',
  effect: 'Takes effect at the next check.',
  ...over,
})

describe('replaceSetting', () => {
  it('replaces the entry with the same key and keeps the order and the other entries', () => {
    const a = setting({ key: 'a', value: '1m' })
    const b = setting({ key: 'b', value: '2m' })
    const list = [a, b]
    const out = replaceSetting(list, setting({ key: 'a', value: '5m', source: 'set here' }))
    assert.deepEqual(out.map((s) => [s.key, s.value]), [['a', '5m'], ['b', '2m']])
    assert.equal(out[1], b)
    assert.equal(list[0], a, 'the old list is not changed')
  })

  it('leaves the list as it is when the key is not in it', () => {
    const list = [setting({ key: 'a' })]
    assert.deepEqual(replaceSetting(list, setting({ key: 'zz' })), list)
  })
})

describe('groupSettings', () => {
  it('keeps the order of the server and puts settings of one group together', () => {
    const groups = groupSettings([
      setting({ key: 'a', group: 'Polling' }),
      setting({ key: 'b', group: 'Diagnosis limits' }),
      setting({ key: 'c', group: 'Diagnosis limits' }),
      setting({ key: 'd', group: 'Runs' }),
    ])
    assert.deepEqual(groups.map((g) => [g.title, g.settings.map((s) => s.key)]), [
      ['Polling', ['a']],
      ['Diagnosis limits', ['b', 'c']],
      ['Runs', ['d']],
    ])
  })
})

describe('valueText and inputText', () => {
  it('shows a duration and a number as they are, and a list with its names or "none"', () => {
    assert.equal(valueText(setting({ value: '15m' })), '15m')
    assert.equal(valueText(setting({ kind: 'number', value: 3 })), '3')
    assert.equal(valueText(setting({ kind: 'list', value: ['demo', 'staging'] })), 'demo, staging')
    assert.equal(valueText(setting({ kind: 'list', value: [] })), 'none')
  })
  it('puts a list into the input field as comma separated names, empty for none', () => {
    assert.equal(inputText(setting({ kind: 'list', value: ['demo', 'staging'] })), 'demo, staging')
    assert.equal(inputText(setting({ kind: 'list', value: [] })), '')
    assert.equal(inputText(setting({ kind: 'number', value: 0 })), '0')
  })
  it('shows a text as it is, "none" when it is empty, and puts it into the input field', () => {
    assert.equal(valueText(setting({ kind: 'text', value: 'http://am:9093' })), 'http://am:9093')
    assert.equal(valueText(setting({ kind: 'text', value: '' })), 'none')
    assert.equal(inputText(setting({ kind: 'text', value: 'http://am:9093' })), 'http://am:9093')
    assert.equal(inputText(setting({ kind: 'text', value: '' })), '')
  })
})

describe('sourceBadge and resetLabel', () => {
  it('names the three sources', () => {
    assert.equal(sourceBadge('default'), 'Default')
    assert.equal(sourceBadge('environment'), 'Environment')
    assert.equal(sourceBadge('set here'), 'Set here')
  })
  it('says what a reset falls back to, and offers none unless the value is set here', () => {
    assert.equal(resetLabel(setting({ source: 'set here', fallback: 'environment' })), 'Reset to environment')
    assert.equal(resetLabel(setting({ source: 'set here', fallback: 'default' })), 'Reset to default')
    assert.equal(resetLabel(setting({ source: 'environment', fallback: 'environment' })), null)
    assert.equal(resetLabel(setting({ source: 'default' })), null)
  })
})

describe('parseInput', () => {
  it('turns a duration into the string the server wants and refuses what is not one', () => {
    assert.deepEqual(parseInput(setting(), ' 30s '), { ok: true, value: '30s' })
    assert.deepEqual(parseInput(setting(), '1h30m'), { ok: true, value: '1h30m' })
    for (const bad of ['', 'soon', '30', '-5s', '1.5']) assert.equal(parseInput(setting(), bad).ok, false, bad)
  })
  it('turns a number into a number and refuses what is not a whole number', () => {
    assert.deepEqual(parseInput(setting({ kind: 'number' }), '12'), { ok: true, value: 12 })
    assert.deepEqual(parseInput(setting({ kind: 'number' }), '0'), { ok: true, value: 0 })
    for (const bad of ['', '-1', '3.5', 'x']) assert.equal(parseInput(setting({ kind: 'number' }), bad).ok, false, bad)
  })
  it('splits a list at commas, trims it and drops empty entries; an empty field is the empty list', () => {
    assert.deepEqual(parseInput(setting({ kind: 'list' }), 'demo, staging ,,'), { ok: true, value: ['demo', 'staging'] })
    assert.deepEqual(parseInput(setting({ kind: 'list' }), ''), { ok: true, value: [] })
  })
  it('takes a text trimmed and lets the server have the last word; an empty field is the empty text', () => {
    assert.deepEqual(parseInput(setting({ kind: 'text' }), '  http://x  '), { ok: true, value: 'http://x' })
    assert.deepEqual(parseInput(setting({ kind: 'text' }), ''), { ok: true, value: '' })
  })
  it('takes whitespace only as the empty list', () => {
    assert.deepEqual(parseInput(setting({ kind: 'list' }), '   '), { ok: true, value: [] })
  })
  it('gives a message that names what is wrong', () => {
    const r = parseInput(setting(), 'soon')
    assert.equal(r.ok, false)
    if (!r.ok) assert.match(r.message, /duration/)
  })
})

describe('unchanged', () => {
  it('compares the field with what the setting shows now, ignoring spaces around list entries', () => {
    assert.equal(unchanged(setting({ value: '1m' }), '1m'), true)
    assert.equal(unchanged(setting({ value: '1m' }), '90s'), false)
    assert.equal(unchanged(setting({ kind: 'list', value: ['a', 'b'] }), 'a,b'), true)
    assert.equal(unchanged(setting({ kind: 'list', value: ['a', 'b'] }), 'a, b, c'), false)
  })
})

describe('a secret setting', () => {
  const secret = (over: Partial<Setting> = {}) => setting({ kind: 'secret', value: null, hint: '…CRET', source: 'set here', ...over })
  it('has no value to show: the page says none and the field starts empty', () => {
    assert.equal(valueText(secret()), 'none')
    assert.equal(inputText(secret()), '')
  })
  it('takes a typed token trimmed and refuses an empty field', () => {
    assert.deepEqual(parseInput(secret(), '  tok-1  '), { ok: true, value: 'tok-1' })
    assert.equal(parseInput(secret(), '   ').ok, false)
  })
  it('counts a typed token as a change and an empty field as none', () => {
    assert.equal(unchanged(secret(), ''), true)
    assert.equal(unchanged(secret(), 'tok-1'), false)
  })
})

describe('secretLine', () => {
  const secret = (over: Partial<Setting> = {}) => setting({ kind: 'secret', value: null, hint: '…CRET', source: 'set here', ...over })
  it('says whether a token is stored, with its last characters', () => {
    assert.equal(secretLine(secret()), 'A token is stored (…CRET).')
    assert.equal(secretLine(secret({ hint: '…' })), 'A token is stored.')
    assert.equal(secretLine(secret({ hint: undefined })), 'A token is stored.')
    assert.equal(secretLine(secret({ source: 'default', hint: undefined })), 'No token is stored.')
    assert.equal(secretLine(secret({ source: 'environment' })), 'No token is stored.')
  })
  it('prefers the problem of the stored value', () => {
    assert.equal(secretLine(secret({ problem: 'It cannot be read.' })), 'It cannot be read.')
  })
  it('is recognised by isSecret', () => {
    assert.equal(isSecret(secret()), true)
    assert.equal(isSecret(setting()), false)
  })
})
