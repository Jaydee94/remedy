import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import type { Limits, Run } from './api.ts'
import { billedTo, costLine, modeChip, spendLine } from './billing.ts'
import { runFailure, runMeta } from './runview.ts'
import { runnerView } from './runnerstatus.ts'

const run = (over: Partial<Run> = {}): Run =>
  ({ id: 'r1', provider: 'claude', prompt: 'why?', status: 'succeeded', result: 'ok', sessionId: 's', costUsd: 0, createdAt: '2026-11-04T10:00:00Z', role: 'adhoc', ...over }) as Run

describe('modeChip', () => {
  it('names how the runner is billed, and says when it does not know', () => {
    assert.equal(modeChip('subscription').label, 'Billed to your subscription')
    assert.equal(modeChip('api_key').label, 'Billed to your API key (Console)')
    assert.equal(modeChip('').label, 'Billing mode unknown')
    assert.equal(modeChip(undefined).label, 'Billing mode unknown')
    assert.equal(modeChip('bedrock').label, 'Billing mode unknown')
  })
})

describe('billedTo and costLine', () => {
  it('names the billing of a run that says how it was billed', () => {
    assert.equal(billedTo(run({ authSource: 'subscription' })), 'your subscription')
    assert.equal(billedTo(run({ authSource: 'api_key' })), 'your API key')
    assert.equal(billedTo(run()), null)
  })

  it('does not call a run billed that was stopped because of its billing', () => {
    assert.equal(billedTo(run({ authSource: 'api_key', failureReason: 'billing_mismatch' })), null)
    assert.ok(!runMeta(run({ authSource: 'api_key', failureReason: 'billing_mismatch' }), 'now').includes('billed to'))
  })

  it('shows money only for a run that was billed to a key, and as the CLI’s estimate', () => {
    assert.equal(costLine(run({ authSource: 'api_key', costUsd: 0.1234 })), 'about $0.12 (the CLI’s estimate)')
    assert.equal(costLine(run({ authSource: 'api_key', costUsd: 1.5 })), 'about $1.50 (the CLI’s estimate)')
    assert.equal(costLine(run({ authSource: 'api_key', costUsd: 0.004 })), 'less than $0.01 (the CLI’s estimate)')
    assert.equal(costLine(run({ authSource: 'api_key', costUsd: 0 })), null)
    assert.equal(costLine(run({ authSource: 'subscription', costUsd: 0.4 })), null, 'a subscription run has no price')
    assert.equal(costLine(run({ costUsd: 0.4 })), null, 'a run that does not say how it was billed has none either')
  })

  it('puts the billing in the line under a run, and the cost only for a key', () => {
    assert.match(runMeta(run({ authSource: 'api_key', costUsd: 0.3 }), '1 min ago'), /billed to your API key, about \$0\.30/)
    const sub = runMeta(run({ authSource: 'subscription', costUsd: 0.3 }), '1 min ago')
    assert.match(sub, /billed to your subscription/)
    assert.doesNotMatch(sub, /\$/)
    assert.doesNotMatch(runMeta(run(), '1 min ago'), /billed/)
  })
})

describe('spendLine', () => {
  const limits = (over: Partial<Limits> = {}): Limits => ({ pollIntervalSeconds: 60, diagnoseCooldownSeconds: 900, diagnoseMaxPerIncident: 3, diagnoseMaxPerDay: 20, staleRunMinutes: 15, diagnosesLast24h: 0, ...over })

  it('says what was spent today, of what budget, and whose estimate it is', () => {
    assert.equal(spendLine(limits({ apiSpendLast24hCents: 120, apiBudgetPerDayCents: 500 })), 'About $1.20 of $5.00 today (the CLI’s estimate; your invoice decides).')
  })
  it('says that automatic runs are off with a budget of zero', () => {
    assert.match(spendLine(limits({ apiSpendLast24hCents: 0, apiBudgetPerDayCents: 0 })) ?? '', /automatic runs are off/i)
  })
  it('says nothing when the runner does not bill a key', () => {
    assert.equal(spendLine(limits()), null)
  })
})

describe('the runner card in key mode', () => {
  const key = { connected: true, login: 'missing' as const, authMode: 'api_key', lastSeenAt: '2026-11-04T10:00:00Z' }
  it('says the key is present instead of asking for a login', () => {
    const v = runnerView(key, Date.parse('2026-11-04T10:00:05Z'))
    assert.equal(v.login.label, 'Key present')
    assert.match(v.sentence, /API key/)
    assert.equal(v.hint, null)
    assert.equal(v.command, null)
  })
  it('does not call a runner that is gone present', () => {
    const v = runnerView({ ...key, connected: false }, Date.parse('2026-11-04T10:30:00Z'))
    assert.equal(v.login.label, 'Login unknown')
  })
})

describe('a run stopped for its billing', () => {
  it('says so in Remedy’s words', () => {
    const f = runFailure(run({ status: 'failed', failureReason: 'billing_mismatch', result: '' }))
    assert.equal(f?.title, 'It would have been billed differently')
    assert.match(f?.text ?? '', /configured/)
  })
})
