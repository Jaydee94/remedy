import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import type { Limits, Repo } from './api.ts'
import { connectionView, diagnosisBar, duration, everyText, limitsText, readingIntro, repoSubline, validRepoName } from './setup.ts'

const limits = (over: Partial<Limits> = {}): Limits => ({
  pollIntervalSeconds: 60,
  diagnoseCooldownSeconds: 900,
  diagnoseMaxPerIncident: 3,
  diagnoseMaxPerDay: 20,
  staleRunMinutes: 15,
  diagnosesLast24h: 4,
  ...over,
})
const repo = (over: Partial<Repo> = {}): Repo => ({ id: 1, fullName: 'octo/homelab', defaultBranch: 'main', enabled: true, lastError: '', createdAt: '2026-10-05T07:00:00Z', ...over })

describe('duration and everyText', () => {
  it('names the largest whole unit', () => {
    assert.equal(duration(30), '30 seconds')
    assert.equal(duration(60), '1 minute')
    assert.equal(duration(900), '15 minutes')
    assert.equal(duration(90), '90 seconds')
    assert.equal(duration(3600), '1 hour')
    assert.equal(duration(7200), '2 hours')
    assert.equal(duration(5400), '90 minutes')
  })
  it('says "every minute", not "every 1 minute"', () => {
    assert.equal(everyText(60), 'minute')
    assert.equal(everyText(300), '5 minutes')
    assert.equal(everyText(3600), 'hour')
    assert.equal(everyText(10), '10 seconds')
  })
})

describe('limitsText', () => {
  it('builds the paragraph of the spec from the limits', () => {
    assert.equal(
      limitsText(limits()),
      'I check every minute. I diagnose up to 3 times per incident and 20 times per 24 hours, at least 15 minutes apart, one run at a time. A run that stays running for 15 minutes is failed.',
    )
  })
  it('says that it does not diagnose on its own when the daily limit is 0', () => {
    const text = limitsText(limits({ diagnoseMaxPerDay: 0 }))
    assert.ok(text.includes("I don't diagnose on my own; you can start it by hand."))
    assert.ok(!text.includes('per incident'))
  })
  it('reads a negative daily limit like 0, as the bar does', () => {
    const text = limitsText(limits({ diagnoseMaxPerDay: -1 }))
    assert.ok(text.includes("I don't diagnose on my own; you can start it by hand."))
    assert.ok(!text.includes('per incident'))
  })
  it('uses the singular for a limit of 1', () => {
    const text = limitsText(limits({ diagnoseMaxPerIncident: 1, diagnoseMaxPerDay: 1, staleRunMinutes: 1 }))
    assert.ok(text.includes('up to 1 time per incident and 1 time per 24 hours'))
    assert.ok(text.endsWith('A run that stays running for 1 minute is failed.'))
  })
})

describe('diagnosisBar', () => {
  it('is absent when automatic diagnosis is off', () => {
    assert.equal(diagnosisBar(limits({ diagnoseMaxPerDay: 0 })), null)
  })
  it('gives the count, the limit and the share', () => {
    assert.deepEqual(diagnosisBar(limits({ diagnosesLast24h: 5, diagnoseMaxPerDay: 20 })), { used: 5, max: 20, ratio: 0.25, full: false })
  })
  it('caps the share at 100 percent and marks a full bar', () => {
    assert.deepEqual(diagnosisBar(limits({ diagnosesLast24h: 25, diagnoseMaxPerDay: 20 })), { used: 25, max: 20, ratio: 1, full: true })
    assert.equal(diagnosisBar(limits({ diagnosesLast24h: 20, diagnoseMaxPerDay: 20 }))?.full, true)
  })
  it('treats a missing count as 0', () => {
    const l = { ...limits(), diagnosesLast24h: undefined } as unknown as Limits
    assert.deepEqual(diagnosisBar(l), { used: 0, max: 20, ratio: 0, full: false })
  })
})

describe('validRepoName', () => {
  it('accepts owner/name', () => {
    for (const ok of ['jaydee94/homelab', 'octo/remedy.git', 'a/b', ' octo/homelab ', 'my-org/my_repo.v2']) assert.equal(validRepoName(ok), true, ok)
  })
  it('refuses everything else', () => {
    for (const bad of ['', '   ', 'homelab', 'a/b/c', 'a/', '/b', 'a b/c', 'octo/home lab', 'octo//homelab', 'octo/<b>x</b>', 'https://github.com/octo/homelab']) {
      assert.equal(validRepoName(bad), false, bad)
    }
  })
  it('refuses a part that is . or .., as the server does', () => {
    for (const bad of ['./..', '../x', 'a/.', 'a/..', './x', '../..']) assert.equal(validRepoName(bad), false, bad)
  })
  it('accepts dots inside a part', () => {
    for (const ok of ['a/.github', 'a/b..c', '.github/x', 'a/..b']) assert.equal(validRepoName(ok), true, ok)
  })
})

describe('repoSubline', () => {
  const now = Date.parse('2026-10-05T10:00:00Z')
  it('names the branch and the last poll', () => {
    assert.equal(repoSubline(repo({ lastPolledAt: '2026-10-05T09:57:00Z' }), now), 'main, polled 3 min ago')
  })
  it('says never polled', () => {
    assert.equal(repoSubline(repo(), now), 'main, never polled')
  })
  it('says paused when the repository is off', () => {
    assert.equal(repoSubline(repo({ enabled: false, lastPolledAt: '2026-10-05T09:57:00Z' }), now), 'main, polled 3 min ago · paused')
    assert.equal(repoSubline(repo({ enabled: false }), now), 'main, never polled · paused')
  })
  it('says never polled when the time cannot be read', () => {
    assert.equal(repoSubline(repo({ lastPolledAt: 'yesterday' }), now), 'main, never polled')
  })
})

describe('connectionView', () => {
  it('has a label and token colours for every status', () => {
    assert.equal(connectionView('ok').label, 'Connected')
    assert.equal(connectionView('error').label, 'Error')
    assert.equal(connectionView('undecryptable').label, 'Cannot decrypt')
  })
  it('colours a working connection as success', () => {
    assert.deepEqual(connectionView('ok'), { label: 'Connected', dot: 'bg-success', soft: 'bg-soft-resolved', text: 'text-success' })
  })
  it('colours error and undecryptable as destructive', () => {
    for (const s of ['error', 'undecryptable'] as const) {
      const v = connectionView(s)
      assert.equal(v.dot, 'bg-destructive')
      assert.equal(v.soft, 'bg-soft-open')
      assert.equal(v.text, 'text-destructive')
    }
    assert.notEqual(connectionView('error').label, connectionView('undecryptable').label)
  })
})

describe('readingIntro', () => {
  it('says what Remedy does when the connection works', () => {
    assert.equal(readingIntro('ok'), 'I read pull requests and check runs as')
    assert.equal(readingIntro(undefined), 'I read pull requests and check runs as')
  })
  it('says what Remedy is set up to do when it cannot', () => {
    assert.equal(readingIntro('error'), "I'm set up to read pull requests and check runs as")
    assert.equal(readingIntro('undecryptable'), "I'm set up to read pull requests and check runs as")
  })
})
