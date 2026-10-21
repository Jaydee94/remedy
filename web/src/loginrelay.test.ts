import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { describe, it } from 'node:test'
import { LOGIN_HOST, conflictSentence, knownReason, reasonSentence, refusalSentence, validateCode, validateUrl } from './loginrelay.ts'

const validUrl =
  'https://claude.com/cai/oauth/authorize?code=true&client_id=c-1&response_type=code&redirect_uri=r-1&scope=s-1&code_challenge=ch-1&code_challenge_method=S256&state=st-1'

interface Case {
  name: string
  input: string
  valid: boolean
  trimmed?: string
  padTo?: number
}

/** The tokens keep invisible and non-ASCII characters out of the source and the shared file, as the Go test does. */
function expand(s: string, padTo = 0): string {
  const out = s
    .replaceAll('<VALID>', validUrl)
    .replaceAll('<NUL>', String.fromCharCode(0))
    .replaceAll('<TAB>', String.fromCharCode(9))
    .replaceAll('<LF>', String.fromCharCode(10))
    .replaceAll('<CR>', String.fromCharCode(13))
    .replaceAll('<BS>', String.fromCharCode(8))
    .replaceAll('<SP>', String.fromCharCode(32))
    .replaceAll('<CYR>', String.fromCharCode(0x430))
  return padTo > out.length ? out + 'a'.repeat(padTo - out.length) : out
}

const cases = JSON.parse(readFileSync(new URL('../../internal/loginrelay/testdata/cases.json', import.meta.url), 'utf8')) as {
  urls: Case[]
  codes: Case[]
}

describe('the shared cases of the Go validators', () => {
  it('has cases', () => {
    assert.ok(cases.urls.length > 20)
    assert.ok(cases.codes.length > 10)
  })

  for (const c of cases.urls) {
    it(`validateUrl: ${c.name}`, () => {
      assert.equal(validateUrl(expand(c.input, c.padTo)), c.valid)
    })
  }

  for (const c of cases.codes) {
    it(`validateCode: ${c.name}`, () => {
      const got = validateCode(expand(c.input, c.padTo))
      assert.equal(got.ok, c.valid)
      if (got.ok && c.trimmed !== undefined) assert.equal(got.code, c.trimmed)
    })
  }
})

describe('validateCode', () => {
  it('returns the trimmed code and nothing else when it is not valid', () => {
    assert.deepEqual(validateCode('  AbC123-_xyz\n'), { ok: true, code: 'AbC123-_xyz' })
    assert.deepEqual(validateCode('short'), { ok: false })
  })

  it('trims only the white space the server trims', () => {
    // U+00A0 is white space for String.prototype.trim and is not for the server.
    assert.deepEqual(validateCode(String.fromCharCode(0xa0) + 'abcdefgh'), { ok: false })
  })
})

describe('validateUrl', () => {
  it('names the host it shows', () => {
    assert.equal(LOGIN_HOST, 'claude.com')
    assert.ok(validateUrl(validUrl))
  })
})

const reasons = [
  'no_url',
  'bad_url',
  'invalid_code',
  'process_exited',
  'not_confirmed',
  'timeout',
  'runner_lost',
  'runner_restarted',
  'expired',
  'cancelled',
  'run_active',
]
const refusals = ['disabled', 'runner_offline', 'run_active', 'already_active', 'api_key_mode', 'replace_required']

describe('reasonSentence', () => {
  const generic = reasonSentence(undefined)

  it('has a distinct non-empty sentence for each of the eleven reasons', () => {
    const seen = new Set<string>()
    for (const r of reasons) {
      const s = reasonSentence(r)
      assert.notEqual(s, '', r)
      assert.notEqual(s, generic, r)
      seen.add(s)
    }
    assert.equal(seen.size, reasons.length)
  })

  it('shows one generic sentence for a word it does not know', () => {
    assert.notEqual(generic, '')
    for (const w of ['', 'drop table', 'constructor', '__proto__', 'toString', '<b>x</b>']) assert.equal(reasonSentence(w), generic, w)
  })

  it('offers the terminal when the CLI ended on its own', () => {
    assert.match(reasonSentence('process_exited'), /terminal/)
    assert.match(reasonSentence('not_confirmed'), /terminal/)
  })

  it('knownReason keeps a word it knows and hides one it does not', () => {
    assert.equal(knownReason('timeout'), 'timeout')
    assert.equal(knownReason(undefined), '')
    assert.equal(knownReason(''), '')
    assert.equal(knownReason('<script>'), 'other')
    assert.equal(knownReason('constructor'), 'other')
  })

  it('never repeats the word it was given', () => {
    assert.ok(!reasonSentence('drop table').includes('drop'))
  })
})

describe('refusalSentence', () => {
  const generic = refusalSentence(undefined)

  it('has a distinct non-empty sentence for each of the six refusals', () => {
    const seen = new Set<string>()
    for (const w of refusals) {
      const s = refusalSentence(w)
      assert.notEqual(s, '', w)
      assert.notEqual(s, generic, w)
      seen.add(s)
    }
    assert.equal(seen.size, refusals.length)
  })

  it('shows one generic sentence for a word it does not know', () => {
    assert.notEqual(generic, '')
    for (const w of ['', 'drop table', 'constructor', '__proto__']) assert.equal(refusalSentence(w), generic, w)
  })
})

describe('conflictSentence', () => {
  it('tells a stale tab that the sign-in moved on, and a hasty one to wait', () => {
    const moved = conflictSentence(409)
    const wait = conflictSentence(429)
    assert.ok(moved && wait)
    assert.notEqual(moved, wait)
    assert.match(moved, /another tab|another window/)
    assert.match(wait, /wait/)
  })

  it('has nothing to say about other statuses', () => {
    assert.equal(conflictSentence(500), null)
    assert.equal(conflictSentence(400), null)
  })
})
