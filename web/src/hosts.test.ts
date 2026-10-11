import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import type { Host } from './api.ts'
import { timeAgo } from './incidents.ts'
import { credentialText, hostLine, parseUnits, statusSentence, statusView, sudoText, validAddress, validDockerPattern, validHostName } from './hosts.ts'

function host(over: Partial<Host> = {}): Host {
  return {
    id: 1, name: 'nas', address: '192.168.178.118', port: 22, account: 'remedy', notes: '', dockerPattern: '', systemdUnits: [], enabled: true,
    status: 'new', statusDetail: '', createdAt: '2026-10-09T10:00:00Z', updatedAt: '2026-10-09T10:00:00Z', ...over,
  }
}

describe('statusView', () => {
  it('names every status and colours the failures', () => {
    assert.equal(statusView('ok').label, 'Connected')
    assert.equal(statusView('new').label, 'Not tested')
    assert.equal(statusView('untrusted').label, 'Key not pinned')
    assert.equal(statusView('key_changed').label, 'Key changed')
    assert.equal(statusView('undecryptable').label, 'Cannot decrypt')
    for (const s of ['error', 'key_changed', 'undecryptable'] as const) assert.match(statusView(s).text, /destructive/)
    assert.match(statusView('ok').text, /success/)
  })
})

describe('hostLine', () => {
  const now = Date.parse('2026-10-09T12:00:00Z')

  it('names the address, shows a port only when it is not 22, and says when it was tested', () => {
    assert.equal(hostLine(host(), now), '192.168.178.118 · never tested')
    assert.equal(
      hostLine(host({ port: 2222, checkedAt: '2026-10-09T11:55:00Z' }), now),
      `192.168.178.118:2222 · tested ${timeAgo('2026-10-09T11:55:00Z', now)}`,
    )
  })

  it('puts an IPv6 address in brackets when it has a port', () => {
    assert.equal(hostLine(host({ address: 'fe80::1', port: 2222 }), now), '[fe80::1]:2222 · never tested')
    assert.equal(hostLine(host({ address: 'fe80::1' }), now), 'fe80::1 · never tested')
  })
})

describe('statusSentence', () => {
  it('says what to do next, in order', () => {
    assert.match(statusSentence(host()), /don't have a login/)
    assert.match(statusSentence(host({ credential: { kind: 'password', hint: 'set', updatedAt: 'x' } })), /test the connection/i)
    assert.match(statusSentence(host({ credential: { kind: 'password', hint: 'set', updatedAt: 'x' }, status: 'untrusted' })), /not pinned yet\. Test the connection/)
    assert.match(statusSentence(host({ credential: { kind: 'password', hint: 'set', updatedAt: 'x' }, pinnedFingerprint: 'SHA256:a' })), /haven't tested/)
    assert.match(statusSentence(host({ status: 'ok', credential: { kind: 'password', hint: 'set', updatedAt: 'x' }, pinnedFingerprint: 'SHA256:a' })), /can log in/)
    assert.match(statusSentence(host({ status: 'ok' })), /don't have a login/)
  })

  it('uses the server\'s detail for a failure', () => {
    assert.equal(statusSentence(host({ status: 'error', statusDetail: 'The host rejected the login.' })), 'The host rejected the login.')
    assert.equal(statusSentence(host({ status: 'error', statusDetail: '' })), "I couldn't log in.")
  })
})

describe('sudoText and credentialText', () => {
  it('says what each sudo level means for Remedy', () => {
    assert.match(sudoText('none'), /changes cannot run/)
    assert.match(sudoText('limited'), /rules below/)
    assert.match(sudoText('unrestricted'), /everything/)
  })

  it('names a login without showing it', () => {
    assert.equal(credentialText({ kind: 'key', hint: 'SHA256:abc', updatedAt: 'x' }), 'Key SHA256:abc')
    assert.equal(credentialText({ kind: 'password', hint: 'set', updatedAt: 'x' }), 'A password')
  })
})

describe('input checks', () => {
  it('parses units from lines and commas, without blanks or duplicates', () => {
    assert.deepEqual(parseUnits('node_exporter, backup.timer\n\n node_exporter \n'), ['node_exporter', 'backup.timer'])
    assert.deepEqual(parseUnits(''), [])
  })

  it('checks a name, an address and a container pattern like the server does', () => {
    assert.equal(validHostName('nas-1'), true)
    assert.equal(validHostName('my nas'), false)
    assert.equal(validHostName(''), false)
    assert.equal(validAddress('192.168.178.118'), true)
    assert.equal(validAddress('nas.homeserver'), true)
    assert.equal(validAddress('fe80::1'), true)
    assert.equal(validAddress('a b'), false)
    assert.equal(validAddress('-oProxyCommand=x'), false)
    assert.equal(validDockerPattern(''), true)
    assert.equal(validDockerPattern('remedy-*'), true)
    assert.equal(validDockerPattern('a*b'), false)
    assert.equal(validDockerPattern('a b'), false)
  })
})
