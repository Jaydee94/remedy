import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import type { WebhookEndpoint } from './api.ts'
import { curlExample, endpointLine, lastUsedText, listeningNote, previousTokenText, tokenNotice, validWebhookName } from './webhooks.ts'

const ep = (over: Partial<WebhookEndpoint> = {}): WebhookEndpoint => ({
  id: 1, name: 'grafana', enabled: true, autoDiagnose: false, resolveAfterSeconds: 86400, ratePerMinute: 60, newPerHour: 30,
  createdAt: '2026-11-01T10:00:00Z', accepted: 0, rejected: 0, ...over,
})

describe('listeningNote', () => {
  it('says that no route exists until the operator sets the address', () => {
    assert.match(listeningNote(false), /REMEDY_WEBHOOK_ADDR|webhook\.enabled/)
    assert.match(listeningNote(false), /no route/i)
    assert.equal(listeningNote(true), '')
  })
})

describe('endpointLine', () => {
  it('says how the endpoint diagnoses and when it resolves', () => {
    assert.equal(endpointLine(ep()), 'Diagnosis by a click · resolves after 24 hours of silence')
    assert.equal(endpointLine(ep({ autoDiagnose: true, resolveAfterSeconds: 3600 })), 'Diagnoses on its own · resolves after 1 hour of silence')
    assert.equal(endpointLine(ep({ resolveAfterSeconds: 0 })), 'Diagnosis by a click · resolves only when the sender says so')
    assert.match(endpointLine(ep({ enabled: false })), /^Switched off · /)
  })
})

describe('lastUsedText', () => {
  const now = Date.parse('2026-11-04T12:00:00Z')
  it('says "not used yet" for an endpoint that never received anything', () => {
    assert.equal(lastUsedText(ep(), now), 'not used yet')
  })
  it('says how long ago, and the counters of the last start', () => {
    assert.equal(lastUsedText(ep({ lastUsedAt: '2026-11-04T11:00:00Z' }), now), 'last used 1 h ago')
    assert.match(lastUsedText(ep({ lastUsedAt: '2026-11-04T11:00:00Z', accepted: 4, rejected: 1 }), now), /4 accepted, 1 rejected since the last start/)
  })
  it('shows the last error as given', () => {
    assert.match(lastUsedText(ep({ lastUsedAt: '2026-11-04T11:00:00Z', rejected: 1, lastError: 'severity: must be critical' }), now), /severity: must be critical/)
  })
})

describe('validWebhookName', () => {
  it('follows the rule of the server', () => {
    for (const ok of ['grafana', 'a', '0x', 'uptime-kuma', 'a'.repeat(40)]) assert.equal(validWebhookName(ok), true, ok)
    for (const bad of ['', '-a', 'A', 'a b', 'a_b', 'a'.repeat(41), 'ünï']) assert.equal(validWebhookName(bad), false, bad)
  })
})

describe('curlExample', () => {
  it('never contains a token and takes it from the environment', () => {
    const text = curlExample('grafana', 'https://hooks.example.com')
    assert.match(text, /\$REMEDY_WEBHOOK_TOKEN/)
    assert.match(text, /https:\/\/hooks\.example\.com\/webhook\/v1\/grafana/)
    assert.match(text, /Authorization: Bearer/)
    assert.doesNotMatch(text, /Bearer [A-Za-z0-9_-]{20,}/)
  })
  it('uses a placeholder host when none is known', () => {
    assert.match(curlExample('x', ''), /https:\/\/YOUR-HOST\/webhook\/v1\/x/)
  })
})

describe('tokenNotice', () => {
  it('warns that the token is shown once', () => {
    assert.match(tokenNotice, /only once|cannot be shown again/i)
  })
})

describe('previousTokenText', () => {
  const now = Date.parse('2026-11-04T12:00:00Z')
  it('says nothing without an overlap or after it ended', () => {
    assert.equal(previousTokenText(ep(), now), '')
    assert.equal(previousTokenText(ep({ previousValidUntil: '2026-11-04T11:00:00Z' }), now), '')
  })
  it('says how long the old token still works and how to end it', () => {
    assert.equal(
      previousTokenText(ep({ previousValidUntil: '2026-11-05T11:30:00Z' }), now),
      'The previous token still works for about 24 more hours. Switching the endpoint off ends that.',
    )
    assert.match(previousTokenText(ep({ previousValidUntil: '2026-11-04T12:20:00Z' }), now), /about 1 more hour\./)
  })
})
