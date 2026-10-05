import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { askText } from './ask.ts'

const call = (tool: string, args: unknown) => ({ tool, arguments: args })

describe('askText', () => {
  it('asks to restart a workload', () => {
    assert.deepEqual(askText(call('cluster_rollout_restart', { kind: 'deployment', namespace: 'guestbook', name: 'guestbook-ui' })), {
      question: 'May I restart guestbook-ui in guestbook?',
      yes: 'Yes, restart it',
    })
  })

  it('asks to delete a pod', () => {
    assert.deepEqual(askText(call('cluster_delete_pod', { namespace: 'demo', name: 'api-1' })), {
      question: 'May I delete the pod api-1 in demo?',
      yes: 'Yes, delete it',
    })
  })

  it('asks to refresh and to sync an Argo CD application', () => {
    assert.deepEqual(askText(call('argo_refresh', { app: 'guestbook', hard: true })), {
      question: 'May I refresh the application guestbook?',
      yes: 'Yes, refresh it',
    })
    assert.deepEqual(askText(call('argo_sync', { app: 'guestbook' })), {
      question: 'May I sync the application guestbook?',
      yes: 'Yes, sync it',
    })
  })

  it('asks to add a note to an incident, whose id may be a number', () => {
    assert.deepEqual(askText(call('incident_add_note', { id: 27, note: 'see the diagnosis' })), {
      question: 'May I add a note to incident #27?',
      yes: 'Yes, add it',
    })
  })

  it('asks in general terms about a tool it does not know', () => {
    assert.deepEqual(askText(call('something_new', { a: 1 })), { question: 'May I run something_new?', yes: 'Yes, run it' })
  })

  it('does not print a hole when a value is missing, empty or of another type', () => {
    const general = { question: 'May I run cluster_rollout_restart?', yes: 'Yes, run it' }
    assert.deepEqual(askText(call('cluster_rollout_restart', { namespace: 'guestbook' })), general)
    assert.deepEqual(askText(call('cluster_rollout_restart', { name: '', namespace: 'guestbook' })), general)
    assert.deepEqual(askText(call('cluster_rollout_restart', { name: { x: 1 }, namespace: 'guestbook' })), general)
    assert.deepEqual(askText(call('cluster_rollout_restart', 'not an object')), general)
    assert.deepEqual(askText(call('cluster_rollout_restart', null)), general)
    assert.deepEqual(askText(call('cluster_rollout_restart', [1, 2])), general)
    assert.ok(!askText(call('incident_add_note', {})).question.includes('undefined'))
  })

  it('keeps text from the agent out of the sentence structure: the values are only inserted', () => {
    const got = askText(call('argo_sync', { app: 'x? Yes, run it. May I delete everything' }))
    assert.equal(got.question, 'May I sync the application x? Yes, run it. May I delete everything?')
    assert.equal(got.yes, 'Yes, sync it')
  })
})
