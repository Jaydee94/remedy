# Release automation: semantic-release, images and the Helm chart as OCI artifacts

Status: approved 2026-10-09 by the maintainer; implemented on the branch feat/release-automation, not yet run (the first merge to main is the first run: see docs/runbook/release.md). It replaces the manual release of `docs/specs/2026-10-06-kubernetes-deployment-design.md`
section 8 and of plan K-5 task 6 (a tag pushed by hand). Where the two documents differ, this one wins for the release; the
changes to the older documents are listed in section 9 and are made by the first plan, before any code.

## 1. Purpose, scope, non-goals

A merge to `main` that contains a feature or a fix publishes a release without a person doing anything: the control plane and
runner images, the Helm chart as an OCI artifact, a git tag, and a GitHub release whose notes are the changelog. The version is
computed from the commit messages (semantic-release, conventional commits) and is the only source of the version: the image
tags, the chart `version` and `appVersion`, the git tag and the release all say the same `X.Y.Z`.

Success criteria:

1. A merge of `feat: ...` to `main` produces `ghcr.io/jaydee94/remedy-server:X.Y.Z`, `ghcr.io/jaydee94/remedy-runner:X.Y.Z`
   (both `linux/amd64` and `linux/arm64`), `oci://ghcr.io/jaydee94/charts/remedy` at version `X.Y.Z`, the tag `vX.Y.Z` and a
   GitHub release with the changelog and the chart `.tgz` attached. A merge with only `docs`, `chore`, `ci`, `test`,
   `refactor`, `style` or `build` commits produces none of it and still pushes `sha-<short>` and `edge`.
2. `Chart.yaml` at the tag says `version: X.Y.Z` and `appVersion: "X.Y.Z"`, so an install from git at the tag and an install from
   the OCI chart agree. `CHANGELOG.md` at the tag has the section for `X.Y.Z`.
3. No release exists without its images and its chart: the artifacts are pushed before the tag and the release are made.
4. The workflow uses `contents: write` and `packages: write` and nothing more, no personal access token, and no action outside
   `actions/*` and `docker/*`.
5. A pull request whose title is not a conventional commit fails a check, because the squash title is the commit that decides.

Non-goals: signing images or the chart, SBOM publication beyond what `docker/build-push-action` attaches, release candidates
or other channels, publishing the chart to a classic Helm repository (GitHub Pages), notifying anyone, a release from a branch
other than `main`, and moving past `0.x` (1.0.0 is a decision for the maintainer, section 3).

## 2. Decisions

| # | Topic | Decision |
|---|---|---|
| R1 | Tool | `semantic-release` with the plugins `commit-analyzer`, `release-notes-generator` (both with the `conventionalcommits` preset), `changelog`, `exec`, `git` and `github`. Versions are pinned by a lockfile in `release/` (not a `package.json` in the repository root; `web/` has its own). |
| R2 | Version source | semantic-release alone. Its `prepare` step runs `scripts/set-chart-version.sh X.Y.Z`, which writes `version` and `appVersion` of `deploy/chart/Chart.yaml`; the `git` plugin commits `CHANGELOG.md` and `Chart.yaml` as `chore(release): X.Y.Z [skip ci]` to `main` with the workflow's `GITHUB_TOKEN`, authored and committed by `github-actions[bot]` (`publish` sets `GIT_AUTHOR_*` and `GIT_COMMITTER_*`; the default would be `semantic-release-bot`). This needs `main` to stay unprotected (it is today); a later branch protection needs a GitHub App or a token with a bypass, which is a decision for then. |
| R3 | Start and 0.x | The tag `v0.0.0` is created once by hand on the first commit of the repository, so the first release is `0.1.0` and its changelog holds the whole history of `feat` and `fix` commits. `releaseRules`: `feat` is minor, `fix` and `perf` are patch, a `BREAKING CHANGE` is **minor** while the major version is 0, `revert` is patch, everything else releases nothing (a type marked with `!` is a breaking change, so it releases a minor). The `revert` rule fires only on a commit whose body has the line `This reverts commit <sha>.` that `git revert` writes; with the squash setting of 8a point 9 the body is blank, so under it a revert releases nothing by itself (ship it with a `fix: revert ...` title). 1.0.0 is made by the maintainer, never by accident: the `plan` job refuses any planned version whose major is not 0 (without the tag `v0.0.0` semantic-release plans `1.0.0` for a first release). Going to 1.0.0 means, in one pull request: the `breaking` rule in `release/release.config.js` becomes `release: 'major'` (as written it can never produce a major); a breaking commit (a `feat!:` title) then has to reach `main`; the guard `''\|0.*` of the `plan` job in `release.yml` is removed or relaxed (it also blocks 1.1.0 and every later major); and `release/test/dry-run.sh`, which pins the 0.x behaviour, is updated, otherwise `make release-test` and the CI `release` job fail. |
| R4 | Trigger | Every push to `main`. The workflow runs `semantic-release --dry-run` first; with no releasable commit nothing but `sha-<short>` and `edge` happens. |
| R5 | One workflow | A tag created by `GITHUB_TOKEN` does not start other workflows, so the tag cannot start the image build. `.github/workflows/release.yml` has the whole chain. `images.yml` keeps only what a pull request needs (build `linux/amd64`, push nothing) and `workflow_dispatch`; it loses the `push` and `tags` triggers. |
| R6 | Order | `plan` (dry run: next version) then `images` and `chart` (build and push the artifacts with the planned version) then `publish` (semantic-release for real: bump commit, tag, GitHub release). Artifacts first: a failure in between leaves unused `X.Y.Z` artifacts, never a release without artifacts. A re-run of the same push recomputes the same version and overwrites the same tags, but only while `main` has not moved (section 8a). Before anything is committed or tagged, the `exec` plugin's `verifyReleaseCmd` (`scripts/check-expected-version.sh`) compares the version semantic-release computes with the planned one (`EXPECTED_VERSION`, set by `publish`; required in `full` mode, checked only when set in `local` mode): a difference fails the run with no commit and no tag (tested in `release/test/dry-run.sh`). |
| R7 | Serialisation | `concurrency: {group: release, cancel-in-progress: false}`. If `main` moved between `plan` and `publish`, semantic-release does not get a refused push: its `git push --dry-run` is rejected, it checks whether the branch is behind the remote, logs "behind the remote one ... won't be published" and exits 0 without a release. `plan` can print `none` for that reason too. `publish` then fails at its final check (no tag) with an explanatory message, the artifacts of that version stay unused, and the run of the newer push computes the version. |
| R8 | Chart as OCI | `helm package deploy/chart --version X --app-version X` and `helm push` to `oci://ghcr.io/jaydee94/charts` (the chart is `charts/remedy`). The chart `.tgz` is also attached to the GitHub release. `helm lint` runs before the push. The chart does not contain `cli-pin.yaml` (it stays a values file of the repository, as in the runbook). `Chart.yaml` carries `home` and `sources`, so that the pushed manifest has the annotation `org.opencontainers.image.source` (measured against a local registry, Helm 4.3.0 and 3.19.0); that GHCR links the package to the repository because of it is expected from GitHub's documentation, not yet seen. |
| R9 | Image tags | Unchanged for `main`: `sha-<short>` and `edge`. With a version: `X.Y.Z` as well. No `latest`. |
| R10 | Release notes | Generated from the conventional commits by the `conventionalcommits` preset: sections Features, Bug Fixes, Performance, Reverts; BREAKING CHANGE notes; `docs`, `chore`, `ci`, `test`, `refactor`, `style`, `build` hidden. The same text goes to `CHANGELOG.md` and to the GitHub release. No comments on pull requests or issues (`successComment`, `failComment` and `releasedLabels` off), so the job does not need `issues: write` or `pull-requests: write`. |
| R11 | Pull request titles | `scripts/check-pr-title.sh` (with a shell test in `make shell-test`) checks that a title is `type(scope)?!?: subject` with a known type; `.github/workflows/pr-title.yml` runs it on `pull_request` (`opened`, `edited`, `synchronize`, `reopened`) with the title passed through the environment, never interpolated into a script. The check also refuses the five skip keywords (`[skip ci]`, `[ci skip]`, `[no ci]`, `[skip actions]`, `[actions skip]`, case-insensitively), because such a title would skip the whole release run of its merge. Squash merges use the pull request title as the commit subject, so the title decides the release (GitHub's Revert button titles a pull request `Revert "feat: x"`, which the check refuses). |
| R12 | Verification after publish | `publish` ends with three checks (section 4): the tag exists on the remote, `scripts/check-release.sh vX.Y.Z` on the `Chart.yaml` of that tag, and the GitHub release is published with the chart attached. |
| R13 | Dependencies | The plugins and their dependencies are pinned by `release/package-lock.json`; Renovate's npm manager updates them (a pull request, not an automatic merge). `npm ci` runs with `--ignore-scripts`. |

## 3. Why 0.x with the breaking rule on minor

semantic-release's default makes a breaking change `1.0.0` and every later one a new major. Remedy has had no release; the
chart, the runbook and the spec say `0.1.0`. The `releaseRules` above keep the project on `0.x` until the maintainer decides,
and the tag `v0.0.0` on the first commit makes the first `feat` release `0.1.0`. Nothing about the major version is automatic.

## 4. The workflow

`.github/workflows/release.yml`, `on: push: branches: [main]`, top-level `permissions: {contents: read}`.

1. **`plan`** (`contents: write`): `actions/checkout` with `fetch-depth: 0` and `persist-credentials: false`, Node (the version in
   `release/.nvmrc`), `npm ci --ignore-scripts` in `release/`, then `semantic-release --dry-run --extends ./release/release.config.js`
   with `RELEASE_MODE=plan`; the `exec` plugin's `verifyReleaseCmd` writes `${nextRelease.version}` to a file. Output: `version`
   (empty when nothing is to be released). The step refuses any version that is not `X.Y.Z` and any major other than 0
   (R3: without the tag `v0.0.0` semantic-release plans `1.0.0` for a first release; a major version is the maintainer's
   decision, who changes that line on purpose). The plan mode loads only the analyzer, the notes generator and
   `exec`. It still needs `contents: write` because semantic-release's core checks push access with `git push --dry-run` even in a
   dry run; the job runs only the pinned release tool and never builds or runs code of the repository.
2. **`images`** (matrix `remedy-server`, `remedy-runner`; `contents: read`, `packages: write`): as today's push job of
   `images.yml` (QEMU, buildx, login with `GITHUB_TOKEN`, `docker/metadata-action` with `type=sha`, `type=edge,branch=main`
   and, when `version` is set, `type=raw,value=X.Y.Z`, `docker/build-push-action` for `linux/amd64,linux/arm64`).
3. **`chart`** (needs `plan`; only when `version` is set; `contents: read`, `packages: write`): `azure/setup-helm` is **not**
   used (a third-party action); Helm is the version preinstalled on the runner, printed in the log (the chart is also tested with
   it in `ci.yml`). `helm lint`, `helm package`, `helm registry login ghcr.io` with `GITHUB_TOKEN` through stdin, `helm push`.
   The `.tgz` is uploaded as a workflow artifact for `publish`.
4. **`publish`** (needs `images` and `chart`; only when `version` is set; `contents: write`): checkout with
   `fetch-depth: 0` and `persist-credentials: false` (no token is stored: semantic-release builds the authenticated URL from
   `GITHUB_TOKEN` itself), download the `.tgz` into `dist` and check that `dist/remedy-$VERSION.tgz` exists (the `github` plugin's
   asset glob would otherwise make a release without the chart), then `semantic-release` for real with the `.tgz` as an asset. The
   commit identity is `github-actions[bot]` (`GIT_AUTHOR_*` and `GIT_COMMITTER_*` in the environment; the default would be
   `semantic-release-bot`). Final checks, each failing the run: the tag `v$VERSION` exists on the remote (`git fetch --tags`, then
   `ls-remote`; if not, `main` moved and the run of the newer push releases), `Chart.yaml` at the tag matches it
   (`scripts/check-release.sh` on the file read from the tag), and the GitHub release exists, is not a draft and has the asset
   `remedy-$VERSION.tgz` (`gh` and `jq`, preinstalled; `contents: write` covers reading releases).

`images.yml` (pull requests): the existing PR build of both images for `linux/amd64`, no push, no tag trigger, no
`check-release.sh` step. The `cli-pin.yml` and `ci.yml` workflows are unchanged except that `ci.yml`'s chart job also asserts that
`Chart.yaml`'s `version` equals its `appVersion` (the invariant the release keeps) and that a `release` job runs `make release-test`.

## 5. The configuration

`release/package.json` (private, only the dependencies), `release/package-lock.json`, `release/.nvmrc`, and
`release/release.config.js` (one file with three modes chosen by `RELEASE_MODE`: `plan` = analyzer, notes generator and `exec`; `local` = plan
plus changelog, `exec` prepare and `git`, no `github` plugin, used by the scratch-repository test; `full` = everything; `branches: ["main"]`,
`tagFormat: "v${version}"`, the options of R3, R10 and R12). One file keeps the modes from drifting apart.
`scripts/set-chart-version.sh X.Y.Z` rewrites exactly the `version:` and `appVersion:` lines of `deploy/chart/Chart.yaml`
and refuses anything that is not `X.Y.Z` or a file with other `version` lines.

## 6. Trust boundaries and permissions

- No secret other than `GITHUB_TOKEN`. The pull-request workflows have `contents: read` only. `plan` and `publish` have
  `contents: write` (`plan` only because semantic-release checks push access in a dry run; `publish` for the bump commit, the tag and
  the release) and only `images` and `chart` have `packages: write`.
- Untrusted text: commit messages feed the version, the notes and the changelog; they are never interpolated into a shell
  line (the version is `X.Y.Z` validated by `set-chart-version.sh`). A pull request title goes to the check through the
  environment. The release notes are commit subjects: a hostile subject can only appear as text in the changelog.
- The bump commit carries `[skip ci]`, so it starts no workflow loop; a `GITHUB_TOKEN` push does not start workflows anyway.
- `npm ci --ignore-scripts` and a lockfile bound the supply chain of the release tool; the plugins run only in the release job.
- The packages are expected to be created private by the first push (not yet seen); making `remedy-server`, `remedy-runner` and `charts/remedy` public is a
  manual step in GitHub, once (as before).

## 7. Tests and what is proven where

- Shell tests (in `make shell-test`): `scripts/set-chart-version_test.sh` (writes both lines, refuses a bad version, a missing
  file, a second `version:` line; leaves other lines and comments alone) and `scripts/check-pr-title_test.sh`.
- A semantic-release dry run against a **scratch git repository** with a local bare remote and crafted commits (a script
  `release/test/dry-run.sh` run by `make release-test`): `feat` gives `0.1.0` from the tag `v0.0.0`, `fix` gives a patch, a
  breaking change stays on 0.x, `docs` alone gives no release, the notes contain the sections of R10, `CHANGELOG.md` and
  `Chart.yaml` are rewritten by the exec step in a real (non-dry) run against the scratch repository with no GitHub plugin. The
  `full` mode (the `github` plugin, the release with the chart attached) is only loaded there, never run.
- The chart push is tried against a local registry (`registry:2` in Docker) with `helm push` and `helm pull`, and the OCI
  manifest annotations are read (`org.opencontainers.image.source` ties a package to the repository on GHCR).
- `actionlint` is not required; the workflow YAML is parsed and read against the rules of section 6.
- Not provable before the first real merge: that the `github` plugin makes the release with the chart attached, that the first push creates the `charts/remedy` package linked to the repository,
  that `GITHUB_TOKEN` may push the bump commit to `main`, and the behaviour when `main` moves during a run. The first release
  is the proof; a record `docs/research/release-first-run.md` is written from it.

## 8. Accepted risks

| # | Risk | Status |
|---|---|---|
| A1 | The tag points at the bump commit while the images were built from its parent. The two differ only in `Chart.yaml` and `CHANGELOG.md`; the image label `org.opencontainers.image.revision` names the parent. | Accepted. |
| A2 | A failed run between the artifacts and the tag leaves unused `X.Y.Z` image and chart artifacts; the next run of the same version overwrites them, a different version orphans them. | Accepted: unused artifacts are harmless. |
| A3 | A bad conventional title (for example `feat:` for a fix) releases the wrong bump. | Mitigated by the title check; a wrong release is corrected by the next one, tags and releases are not rewritten. |
| A4 | The workflow writes to an unprotected `main` with the workflow token. | Accepted while `main` is unprotected; a branch protection needs a decision (R2). |
| A5 | Merging a pull request is publishing: every merge with a `feat` or `fix` makes a public GitHub release. | The intended behaviour (R4); the manual alternative was declined. |
| A6 | Re-running an older run of `main` moves `edge` backwards. GitHub keeps one pending run per concurrency group, so the `sha-<short>` image of an intermediate push may never be built. | Accepted: re-run only the latest run of `main`. No release is lost, because the analysis covers all commits since the last tag. |

## 8a. Failure points and recovery

| # | State | Recovery |
|---|---|---|
| 1 | `plan` fails | Nothing is pushed, no images. Re-run if it is the latest run of `main`, else the next push does it. |
| 2 | `plan` says `none` for a merge with a `feat` or `fix` | Look for "behind the remote one" in the log: `main` moved and the newer run releases. |
| 3 | `images` or `chart` fails with a version | X.Y.Z artifacts may be pushed; no commit, tag or release. "Re-run failed jobs" while `main` has not moved. |
| 4 | `publish`: the chart package is missing | As 3. |
| 4a | `publish`: semantic-release fails before the bump commit (the push is refused, `EGITNOPERMISSION`, or the computed version differs from the planned one: R6) | As 3: unused `X.Y.Z` artifacts, no commit, tag or release. Fix the permission or the settings and re-run the failed jobs while `main` has not moved. |
| 5 | `publish`: `main` moved | semantic-release exits 0 as "behind", the final check fails with "no tag". Nothing to do, the newer run releases. |
| 6 | The bump commit is pushed but the tag push failed | `main` has `chore(release)` without a tag, and no GitHub release (order expected from the plugin sources, `semantic-release/index.js`, `@semantic-release/git` prepare and `@semantic-release/github` publish, read, not run: bump commit pushed in `prepare`, tag created and pushed, GitHub release last). Tag the bump commit by hand (`git tag vX.Y.Z <sha>`, push), then do row 7 (create the release by hand) and the three checks of `publish` by hand (tag on the remote, `Chart.yaml` at the tag, `gh release view vX.Y.Z --json isDraft,assets`; commands in `docs/runbook/release.md`), or let the next run plan the same version (a duplicate changelog section is fixed by a `docs:` pull request). |
| 7 | The tag is pushed but the GitHub release failed or is a draft | A re-run of `publish` does not recover (semantic-release exits 0 as "behind" and the final check fails on the missing release). Create or finish the release by hand: `gh release create vX.Y.Z dist/remedy-X.Y.Z.tgz --notes-file <the CHANGELOG section>`, or upload the asset and `gh release edit vX.Y.Z --draft=false`. Never move or delete the tag. |
| 8 | The final check fails although the tag exists | Tag and `Chart.yaml` disagree: fix forward with a `fix:` pull request, never rewrite a tag. |
| 9 | Before the first merge | Push `v0.0.0` on the first commit. Set the squash merge title to the pull request title: the repository has `squash_merge_commit_title=COMMIT_OR_PR_TITLE` and the message `COMMIT_MESSAGES`, which makes the squash subject of a single-commit pull request the commit's subject, not the title, and puts every commit message in the body, so a `[skip ci]` in any of them skips the release run. The maintainer decides: `gh api -X PATCH repos/Jaydee94/remedy -f squash_merge_commit_title=PR_TITLE -f squash_merge_commit_message=BLANK`, optionally with `-F allow_merge_commit=false -F allow_rebase_merge=false`. Make the three packages public after the first run. Never re-run an older run of `main`. |

## 9. Documents and files to change

`docs/design.md` (the release paragraph, if there is one: tags are made by the pipeline); `docs/specs/2026-10-06-kubernetes-deployment-design.md`
section 8 (a pointer to this document, tag and chart version rules) and section 11 (risk list pointer); `docs/runbook/homelab-deploy.md`
(sections 6, 9 and 10: install from `oci://ghcr.io/jaydee94/charts/remedy` with a git source for `cli-pin.yaml`, or from git at the tag;
the release process is a merge, not a tag; the visibility step for three packages); `docs/plans/k8s-followups.md` (K-5 task 6 is
replaced; the tag step and the first-release steps); `deploy/chart/README.md` (install from OCI); `README.md`;
`CLAUDE.md` ("Current state": the release is automated, conventional PR titles; commands: `make release-test`); `.github/workflows/`
(`release.yml`, `pr-title.yml`, `images.yml`, `ci.yml`); `scripts/check-release.sh` stays (used by `ci.yml` and the final check of `publish`); `renovate.json` gets rules for `release/package.json`
(the npm manager finds the file itself): the preset stays below 10, the semantic-release packages are one pull request that is
never merged by itself, and a version younger than 7 days is not proposed. `docs/runbook/release.md` is the maintainer's runbook.

## 10. Operating notes

- First release: create the tag `v0.0.0` on the first commit and push it (expected: no run, because GitHub reads the workflow files of the tagged commit and the first commit holds only `README.md`; if the old `images.yml` of `main`, which still triggers on `v*` tags, does start, it is expected to fail at its first step, `check-release.sh v0.0.0` against `Chart.yaml` 0.1.0, before any login or push: a harmless red run, ignore it; the merge removes the trigger), merge the pull request that adds
  the pipeline with the title `feat: ...`, watch the run, then make the three packages public.
- A wrong release: do not rewrite tags. Fix forward with a `fix:`; delete a GitHub release by hand only if it is harmful.
- Skipping a release on purpose: commits of type `chore`, `docs` or `ci` without a `!` release nothing; `[skip ci]` in the merge message skips
  the whole workflow (the images are then not rebuilt either).
