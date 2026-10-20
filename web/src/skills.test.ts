import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import type { Skill } from './api.ts'
import {
  MAX_PER_SCOPE,
  byteLength,
  commandText,
  confirmBlocked,
  deleteHostLabel,
  evidenceSummary,
  hostDeleteWarning,
  nextProposals,
  originText,
  reviewItems,
  scopeGroups,
  skillNotice,
  skillProblem,
  validSkillName,
} from './skills.ts'

const skill = (over: Partial<Skill> = {}): Skill => ({
  id: 1,
  hostId: null,
  name: 'lab-notes',
  description: 'How the lab is laid out.',
  body: 'The NAS is nas.',
  origin: 'written',
  state: 'active',
  evidence: [],
  createdAt: '2026-10-19T10:00:00Z',
  updatedAt: '2026-10-19T10:00:00Z',
  ...over,
})
const onHost = (hostId: number, host: string, over: Partial<Skill> = {}) => skill({ hostId, host, ...over })

describe('validSkillName', () => {
  it('accepts lower case letters, digits and dashes, 1 to 40, not starting with a dash', () => {
    for (const ok of ['a', '0', 'paperless', 'node-exporter', 'a'.repeat(40)]) assert.equal(validSkillName(ok), true, ok)
    for (const bad of ['', '-a', 'A', 'a b', 'a_b', 'é', 'a'.repeat(41), '(global)']) assert.equal(validSkillName(bad), false, bad)
  })
})

describe('skillProblem', () => {
  const ok = { name: 'paperless', description: 'Paperless keeps its data in /volume1/docker.', body: 'Restart is safe.' }

  it('is empty for a skill the server can be asked about', () => {
    assert.equal(skillProblem(ok), '')
    assert.equal(skillProblem({ ...ok, description: 'é'.repeat(200) }), '')
    assert.equal(skillProblem({ ...ok, body: 'x'.repeat(8192) }), '')
  })

  it('names the first rule that is broken, in the words of the API', () => {
    assert.match(skillProblem({ ...ok, name: 'Paperless' }), /^The name may contain/)
    assert.match(skillProblem({ ...ok, description: '  ' }), /^The description must not be empty/)
    assert.match(skillProblem({ ...ok, description: 'x'.repeat(201) }), /at most 200 characters/)
    assert.match(skillProblem({ ...ok, description: 'one\ntwo' }), /one line of plain text/)
    assert.match(skillProblem({ ...ok, body: ' \n ' }), /^The body must not be empty/)
    assert.match(skillProblem({ ...ok, body: 'x'.repeat(8193) }), /at most 8 KB/)
  })

  it('counts bytes for the body and characters for the description', () => {
    assert.equal(byteLength('é'), 2)
    assert.match(skillProblem({ ...ok, body: 'é'.repeat(4097) }), /at most 8 KB/)
    assert.equal(skillProblem({ ...ok, description: '😀'.repeat(200) }), '')
  })
})

describe('scopeGroups', () => {
  it('puts the global skills first, then each host by name, each by skill name, and leaves the proposals out', () => {
    const groups = scopeGroups([
      onHost(2, 'nas', { id: 1, name: 'profile' }),
      skill({ id: 2, name: 'zeta' }),
      onHost(1, 'alpha', { id: 3, name: 'backup' }),
      skill({ id: 4, name: 'alpha-notes' }),
      onHost(2, 'nas', { id: 5, name: 'paperless' }),
      onHost(2, 'nas', { id: 6, name: 'pending', state: 'proposed', origin: 'proposed' }),
    ])
    assert.deepEqual(
      groups.map((g) => [g.key, g.title, g.skills.map((s) => s.name)]),
      [
        ['global', 'Global', ['alpha-notes', 'zeta']],
        ['host:1', 'alpha', ['backup']],
        ['host:2', 'nas', ['paperless', 'profile']],
      ],
    )
  })

  it('is empty when there is nothing to show', () => {
    assert.deepEqual(scopeGroups([]), [])
    assert.deepEqual(scopeGroups([skill({ state: 'proposed', origin: 'proposed' })]), [])
  })

  it('keeps an off skill in its group', () => {
    assert.equal(scopeGroups([skill({ state: 'off' })])[0].skills.length, 1)
  })
})

describe('commandText', () => {
  it('joins the words and quotes what a shell would read differently', () => {
    assert.equal(commandText(['docker', 'ps', '-a']), 'docker ps -a')
    assert.equal(commandText(['docker', 'logs', '--tail', '50', 'remedy-drill']), 'docker logs --tail 50 remedy-drill')
    assert.equal(commandText(['echo', 'a b', '']), "echo 'a b' ''")
    assert.equal(commandText(['echo', "it's"]), "echo 'it'\\''s'")
    assert.equal(commandText(['cat', '$HOME;rm']), "cat '$HOME;rm'")
  })
})

describe('reviewItems', () => {
  const proposal = (over: Partial<Skill> = {}) =>
    onHost(2, 'nas', {
      id: 10,
      name: 'paperless',
      state: 'proposed',
      origin: 'proposed',
      evidence: [
        { argv: ['docker', 'ps', '-a'], ran: true },
        { argv: ['uname', '-a'], ran: false },
      ],
      ...over,
    })

  it('lists the proposals only, with the scope, the commands and how many did not run', () => {
    const items = reviewItems([skill(), proposal()])
    assert.equal(items.length, 1)
    assert.equal(items[0].scope, 'nas')
    assert.deepEqual(items[0].evidence, [
      { text: 'docker ps -a', ran: true },
      { text: 'uname -a', ran: false },
    ])
    assert.equal(items[0].notRun, 1)
    assert.equal(items[0].replaces, null)
  })

  it('shows the current text of a skill the proposal replaces and whether it changed', () => {
    const changed = reviewItems([proposal({ replaces: { id: 3, description: 'Old.', body: 'Old body.' } })])[0].replaces
    assert.deepEqual(changed, { description: 'Old.', body: 'Old body.', changed: true })
    const same = reviewItems([proposal({ replaces: { id: 3, description: 'About paperless.', body: 'b' }, description: 'About paperless.', body: 'b' })])[0].replaces
    assert.equal(same?.changed, false)
  })

  it('names a global scope', () => {
    assert.equal(reviewItems([skill({ state: 'proposed', origin: 'proposed' })])[0].scope, 'Global')
  })
})

describe('evidenceSummary', () => {
  const item = (evidence: Skill['evidence']) => reviewItems([skill({ state: 'proposed', origin: 'proposed', evidence })])[0]
  it('says what is wrong with the evidence, and nothing when all of it ran', () => {
    assert.equal(evidenceSummary(item([])), 'No command is cited for this skill.')
    assert.equal(evidenceSummary(item([{ argv: ['a'], ran: true }])), '')
    assert.equal(evidenceSummary(item([{ argv: ['a'], ran: false }])), 'None of the cited commands ran.')
    assert.equal(
      evidenceSummary(item([{ argv: ['a'], ran: true }, { argv: ['b'], ran: false }, { argv: ['c'], ran: false }])),
      '2 of 3 cited commands did not run.',
    )
  })
})

describe('originText', () => {
  it('says who wrote it, and for a confirmed proposal the day, in a form that does not depend on the locale', () => {
    assert.equal(originText(skill()), 'written by you')
    assert.equal(originText(skill({ origin: 'proposed', confirmedAt: '2026-10-19T23:59:59Z' })), 'proposed by an agent, confirmed on 2026-10-19')
    assert.equal(originText(skill({ origin: 'proposed', state: 'proposed' })), 'proposed by an agent')
  })
})

describe('confirmBlocked', () => {
  const all = [skill({ id: 1, name: 'taken' }), ...Array.from({ length: 29 }, (_, i) => skill({ id: i + 2, name: `s${i}` }))]
  const proposal = (over: Partial<Skill> = {}) => skill({ id: 99, name: 'new-one', state: 'proposed', origin: 'proposed', ...over })

  it('lets a proposal through when nothing is in its way', () => {
    assert.equal(confirmBlocked(all.slice(0, 5), proposal()), '')
  })

  it('says when the name is taken in the scope, but not for another scope', () => {
    assert.match(confirmBlocked(all, proposal({ name: 'taken' })), /already exists/)
    assert.equal(confirmBlocked(all, proposal({ name: 'taken', hostId: 2, host: 'nas' })), '')
  })

  it('says when the scope is full, unless the proposal replaces a skill', () => {
    assert.equal(all.length, MAX_PER_SCOPE)
    assert.match(confirmBlocked(all, proposal()), /already has 30 skills/)
    assert.equal(confirmBlocked(all, proposal({ replaces: { id: 5, description: 'd', body: 'b' } })), '')
  })

  it('does not count the proposals themselves', () => {
    const withProposals = [...all.slice(0, 5), ...Array.from({ length: 40 }, (_, i) => skill({ id: 100 + i, name: `p${i}`, state: 'proposed', origin: 'proposed' }))]
    assert.equal(confirmBlocked(withProposals, proposal()), '')
  })
})

describe('hostDeleteWarning and deleteHostLabel', () => {
  const some = [onHost(2, 'nas', { name: 'a' }), onHost(2, 'nas', { name: 'b' }), onHost(2, 'nas', { name: 'c' }), onHost(2, 'nas', { name: 'p', state: 'proposed', origin: 'proposed' })]

  it('names the skills that go with the host and the proposals that wait', () => {
    assert.equal(
      hostDeleteWarning(some),
      'Deleting this host also deletes what I know about it: 3 skills (a, b, c) and 1 proposal waiting for review.',
    )
    assert.equal(hostDeleteWarning([onHost(2, 'nas', { name: 'a' })]), 'Deleting this host also deletes what I know about it: 1 skill (a).')
    assert.equal(
      hostDeleteWarning([onHost(2, 'nas', { name: 'p', state: 'proposed', origin: 'proposed' }), onHost(2, 'nas', { id: 2, name: 'q', state: 'proposed', origin: 'proposed' })]),
      'Deleting this host also deletes what I know about it: 2 proposals waiting for review.',
    )
  })

  it('cuts a long list of names', () => {
    const many = Array.from({ length: 8 }, (_, i) => onHost(2, 'nas', { id: i, name: `s${i}` }))
    assert.equal(hostDeleteWarning(many), 'Deleting this host also deletes what I know about it: 8 skills (s0, s1, s2, s3, s4 and 3 more).')
  })

  it('says nothing when there is nothing to lose', () => {
    assert.equal(hostDeleteWarning([]), '')
  })

  it('words the confirmation of the delete button by what goes, and by what is not known', () => {
    assert.equal(deleteHostLabel([]), 'Confirm: also removes its login')
    assert.equal(deleteHostLabel(some), 'Confirm: also removes its login and 4 skills')
    assert.equal(deleteHostLabel([some[0]]), 'Confirm: also removes its login and 1 skill')
    assert.equal(deleteHostLabel(null), 'Confirm: also removes its login and its skills')
  })
})

describe('nextProposals', () => {
  it('takes the count of an answer and keeps the last one when the poll failed', () => {
    assert.equal(nextProposals(0, { status: 'fulfilled', value: [skill(), skill()] }), 2)
    assert.equal(nextProposals(3, { status: 'rejected', reason: new Error('x') }), 3)
    assert.equal(nextProposals(3, { status: 'fulfilled', value: [] }), 0)
  })
})

describe('skillNotice', () => {
  it('says what happened to the skill, for the live region', () => {
    assert.equal(skillNotice('created', 'paperless'), 'Saved paperless. I list it for every run it fits.')
    assert.equal(skillNotice('saved', 'paperless'), 'Saved paperless.')
    assert.equal(skillNotice('confirmed', 'paperless'), 'Confirmed paperless. I list it for every run it fits.')
    assert.equal(skillNotice('rejected', 'paperless'), 'Rejected the proposal paperless.')
    assert.equal(skillNotice('deleted', 'paperless'), 'Deleted paperless.')
    assert.equal(skillNotice('on', 'paperless'), 'paperless is on.')
    assert.equal(skillNotice('off', 'paperless'), 'paperless is off. I no longer list it.')
  })
})
