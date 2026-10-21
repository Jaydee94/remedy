import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import type { RunnerStatus } from './api.ts'
import { buildLabel, runnerVersionLine, sidebarVersion, versionNotice } from './version.ts'

const runner = (over: Partial<RunnerStatus> = {}): RunnerStatus => ({ connected: true, login: 'ok', ...over })

describe('buildLabel', () => {
  it('is the version of a release, without the commit', () => {
    assert.equal(buildLabel({ version: '0.9.0', commit: 'abc1234' }), '0.9.0')
  })

  it('adds the commit to a dev or edge build, which has no number of its own', () => {
    assert.equal(buildLabel({ version: 'edge', commit: 'abc1234' }), 'edge (abc1234)')
    assert.equal(buildLabel({ version: 'dev', commit: 'abc1234' }), 'dev (abc1234)')
    assert.equal(buildLabel({ version: 'dev' }), 'dev')
  })

  it('is null when nothing is known', () => {
    assert.equal(buildLabel(null), null)
    assert.equal(buildLabel(undefined), null)
    assert.equal(buildLabel({}), null)
    assert.equal(buildLabel({ version: '' }), null)
    assert.equal(buildLabel({ version: '  ' }), null)
  })

  it('treats the control plane\'s "unknown" (a value it refused) as not known', () => {
    assert.equal(buildLabel({ version: 'unknown', commit: 'abc1234' }), null)
  })
})

describe('sidebarVersion', () => {
  it('shows the version, and the commit as the tooltip', () => {
    assert.deepEqual(sidebarVersion({ version: '0.9.0', commit: 'abc1234' }), { text: 'Remedy 0.9.0', title: 'Commit abc1234' })
  })

  it('has no tooltip without a commit', () => {
    assert.deepEqual(sidebarVersion({ version: 'dev', commit: '' }), { text: 'Remedy dev', title: undefined })
  })

  it('says nothing while the version is unknown, as an older control plane or a failed request leaves it', () => {
    assert.equal(sidebarVersion(null), null)
    assert.equal(sidebarVersion({ version: '' }), null)
  })
})

describe('runnerVersionLine', () => {
  it('shows the runner\'s version while it is connected', () => {
    assert.deepEqual(runnerVersionLine(runner({ version: '0.9.0', commit: 'abc1234' })), { text: 'Runner 0.9.0', title: 'Commit abc1234' })
  })

  it('says nothing for a runner that does not send a version (an older one)', () => {
    assert.equal(runnerVersionLine(runner()), null)
    assert.equal(runnerVersionLine(runner({ version: '' })), null)
  })

  it('says nothing for a runner that is gone: its version is a fact about the past', () => {
    assert.equal(runnerVersionLine(runner({ connected: false, version: '0.9.0' })), null)
  })

  it('says nothing without a status', () => {
    assert.equal(runnerVersionLine(null), null)
  })
})

describe('versionNotice', () => {
  const same = '0.9.0'

  it('is silent when both run the same release', () => {
    assert.equal(versionNotice({ version: same, commit: 'abc1234' }, runner({ version: same, commit: 'abc1234' })), null)
  })

  it('is silent when the release is the same and only the short commit differs in spelling', () => {
    assert.equal(versionNotice({ version: same, commit: 'abc1234' }, runner({ version: same })), null)
  })

  it('tells the maintainer to upgrade both when the versions differ', () => {
    assert.equal(
      versionNotice({ version: '0.9.0' }, runner({ version: '0.8.0' })),
      'The runner runs 0.8.0, the control plane 0.9.0. Upgrade both together.',
    )
  })

  it('is silent while the runner\'s version is unknown: an older runner, empty or not connected', () => {
    assert.equal(versionNotice({ version: '0.9.0' }, runner()), null)
    assert.equal(versionNotice({ version: '0.9.0' }, runner({ version: '' })), null)
    assert.equal(versionNotice({ version: '0.9.0' }, runner({ connected: false, version: '0.8.0' })), null)
    assert.equal(versionNotice({ version: '0.9.0' }, null), null)
  })

  it('is silent while the control plane\'s version is unknown', () => {
    assert.equal(versionNotice(null, runner({ version: '0.8.0' })), null)
    assert.equal(versionNotice({}, runner({ version: '0.8.0' })), null)
    assert.equal(versionNotice({ version: 'unknown' }, runner({ version: '0.8.0' })), null)
  })

  it('is silent when the runner\'s version is one the control plane refused', () => {
    assert.equal(versionNotice({ version: '0.9.0' }, runner({ version: 'unknown' })), null)
  })

  it('is silent for two dev builds of the same commit and speaks for two of different commits', () => {
    assert.equal(versionNotice({ version: 'dev', commit: 'abc1234' }, runner({ version: 'dev', commit: 'abc1234' })), null)
    assert.equal(versionNotice({ version: 'dev' }, runner({ version: 'dev' })), null)
    assert.equal(
      versionNotice({ version: 'edge', commit: 'abc1234' }, runner({ version: 'edge', commit: 'def5678' })),
      'The runner runs edge (def5678), the control plane edge (abc1234). Upgrade both together.',
    )
  })

  it('does not guess when only one side of an edge build knows its commit', () => {
    assert.equal(versionNotice({ version: 'edge', commit: 'abc1234' }, runner({ version: 'edge' })), null)
  })

  it('speaks when a dev build meets a release', () => {
    assert.equal(
      versionNotice({ version: '0.9.0' }, runner({ version: 'dev', commit: 'abc1234' })),
      'The runner runs dev (abc1234), the control plane 0.9.0. Upgrade both together.',
    )
  })
})
