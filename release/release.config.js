// The semantic-release configuration of Remedy, one file with three modes chosen by RELEASE_MODE:
//   plan  analyzer, notes generator and exec: says the next version (writes it to .release-version), changes nothing;
//         dryRun is set here, so plan can not publish with or without the --dry-run flag
//   local plan + changelog, the chart version (exec prepare) and git: a real release into a local remote; used by
//         release/test/dry-run.sh, which has no GitHub
//   full  local + the GitHub release with the chart attached; used by .github/workflows/release.yml (publish)
// The rules are those of 0.x (docs/specs/2026-10-09-release-automation-design.md R3): feat is minor, fix and perf are
// patch, a breaking change is minor too (1.0.0 is the maintainer's decision), revert is patch, nothing else releases.
// There is no default: a forgotten RELEASE_MODE must not turn into a publishing run.
const mode = process.env.RELEASE_MODE
if (!['plan', 'local', 'full'].includes(mode)) {
  throw new Error(`RELEASE_MODE must be plan, local or full (got "${mode}")`)
}

const preset = 'conventionalcommits'

const analyzer = [
  '@semantic-release/commit-analyzer',
  {
    preset,
    releaseRules: [
      { breaking: true, release: 'minor' },
      { revert: true, release: 'patch' },
      { type: 'feat', release: 'minor' },
      { type: 'fix', release: 'patch' },
      { type: 'perf', release: 'patch' },
    ],
  },
]

const hiddenTypes = ['docs', 'chore', 'ci', 'test', 'refactor', 'style', 'build']

const notes = [
  '@semantic-release/release-notes-generator',
  {
    preset,
    presetConfig: {
      types: [
        { type: 'feat', section: 'Features' },
        { type: 'fix', section: 'Bug Fixes' },
        { type: 'perf', section: 'Performance' },
        { type: 'revert', section: 'Reverts' },
        ...hiddenTypes.map((type) => ({ type, hidden: true })),
      ],
    },
  },
]

const changelog = ['@semantic-release/changelog', { changelogFile: 'CHANGELOG.md', changelogTitle: '# Changelog' }]

// ${nextRelease.version} is filled in by the exec plugin (lodash template), not by the shell.
const execPlan = ['@semantic-release/exec', { verifyReleaseCmd: 'printf %s "${nextRelease.version}" > .release-version' }]
const execPrepare = ['@semantic-release/exec', { prepareCmd: 'sh scripts/set-chart-version.sh ${nextRelease.version}' }]

const git = [
  '@semantic-release/git',
  {
    assets: ['CHANGELOG.md', 'deploy/chart/Chart.yaml'],
    message: 'chore(release): ${nextRelease.version} [skip ci]\n\n${nextRelease.notes}',
  },
]

// No comments on pull requests or issues, no labels, no failure issue: the job then needs only contents: write.
// The *Condition options are the forward-compatible form of `successComment: false` (deprecated, it logs a warning).
// With them off, lib/success.js and lib/fail.js make no write call besides the release itself (only a GET of the repository).
const github = [
  '@semantic-release/github',
  {
    assets: [{ path: 'dist/remedy-*.tgz', label: 'Helm chart (also oci://ghcr.io/jaydee94/charts/remedy)' }],
    successCommentCondition: false,
    failCommentCondition: false,
    releasedLabels: false,
  },
]

const plugins =
  mode === 'plan' ? [analyzer, notes, execPlan] : [analyzer, notes, changelog, execPrepare, git, ...(mode === 'full' ? [github] : [])]

module.exports = { branches: ['main'], tagFormat: 'v${version}', plugins, ...(mode === 'plan' ? { dryRun: true } : {}) }
