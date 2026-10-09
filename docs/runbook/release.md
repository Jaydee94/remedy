# Runbook: the release of Remedy

For the maintainer. The design and its reasons are in [`docs/specs/2026-10-09-release-automation-design.md`](../specs/2026-10-09-release-automation-design.md);
where this runbook and the spec differ, the spec decides.

**Status.** Implemented, **not yet run**. The first merge to `main` after this pipeline is the first run. What is measured is
the configuration in a scratch git repository (`make release-test`: versions, notes, the bump files) and the chart push to a
local registry. Everything about GitHub itself (the bump commit push by `GITHUB_TOKEN`, the GHCR packages, the GitHub release) is
expected, not seen: section 3 lists the three things the first run proves.

## 1. What a merge to `main` does

`.github/workflows/release.yml` runs on every push to `main`, one run at a time (`concurrency: release`). Four jobs:

| Job | What it does |
|---|---|
| `plan` | Runs semantic-release in dry-run mode and says the next version `X.Y.Z`, or nothing. Refuses a version that is not `X.Y.Z` and any major other than 0. |
| `images` | Builds `remedy-server` and `remedy-runner` for `linux/amd64` and `linux/arm64` and pushes them. Runs on every push to `main` once `plan` has succeeded (it needs `plan`). |
| `chart` | Only with a version: lints and packages the chart with `version` and `appVersion` set to `X.Y.Z`, pushes it to the registry. |
| `publish` | Only with a version, after `images` and `chart`: semantic-release for real (it first checks that its version is the planned one, then bump commit, tag, GitHub release), then three checks. |

What is published where:

- Images `ghcr.io/jaydee94/remedy-server` and `ghcr.io/jaydee94/remedy-runner`: `sha-<short>` and `edge` on every push to `main`,
  and `X.Y.Z` when there is a release. No `latest`.
- The chart `oci://ghcr.io/jaydee94/charts/remedy` at version `X.Y.Z` (its `appVersion` is `X.Y.Z` too, so it pulls the images `X.Y.Z`).
- The git tag `vX.Y.Z`.
- The GitHub release `vX.Y.Z` with the changelog as its notes and the chart `remedy-X.Y.Z.tgz` attached.
- `CHANGELOG.md` and `deploy/chart/Chart.yaml` (`version`, `appVersion`) written back to `main` as
  `chore(release): X.Y.Z [skip ci]` by `github-actions[bot]`. The tag points at this bump commit; the images were built from its parent.

The artifacts are pushed before the tag and the release are made: a failure in between leaves unused `X.Y.Z` artifacts, never a
release without artifacts. A merge that releases nothing still pushes `sha-<short>` and `edge`.

## 2. The rules for commits

The version is computed from conventional commit messages. A pull request is squash-merged, so the **pull request title** is the
commit message that counts; the `pr-title` check (`scripts/check-pr-title.sh`) fails a title that is not `type(scope)?!?: subject`
with one of the types `feat`, `fix`, `perf`, `revert`, `docs`, `chore`, `ci`, `test`, `refactor`, `style`, `build`.

| Commit | Release while the major version is 0 |
|---|---|
| `feat` | minor (`0.1.0` to `0.2.0`) |
| `fix`, `perf` | patch |
| any type marked with `!` (`feat!:`, and also `docs!:`, `chore!:`, `refactor!:`) | minor, not major. With the squash setting of section 3 (blank body) a `BREAKING CHANGE:` footer cannot reach `main`; only the `!` in the title marks a breaking change. This follows from the setting; it was not tried. |
| `docs`, `chore`, `ci`, `test`, `refactor`, `style`, `build`, `revert` without a `!` | nothing |

**A revert releases nothing by itself.** The `revert` rule (patch) fires only on a commit whose body has the line
`This reverts commit <sha>.` that `git revert` writes (the scratch test `release/test/dry-run.sh` covers exactly that commit).
With the squash setting of section 3 the body is blank, so the line never reaches `main`. A revert titled `revert: x` also gives no
release (reviewer ran the real analyzer). GitHub's Revert button titles the pull request `Revert "feat: x"`, which the title check
refuses: retitle it. To ship a revert, title the pull request `fix: revert <what>`.

Traps:

- **`[skip ci]` skips the whole release run.** GitHub documents the keywords `[skip ci]`, `[ci skip]`, `[no ci]`, `[skip actions]`
  and `[actions skip]`, and the trailer `skip-checks: true`, in the commit message of a push (and the HEAD commit for pull
  requests): the workflow run is skipped, so no images and no release for that merge. In practice the squash commit is what is
  pushed to `main`. `scripts/check-pr-title.sh` refuses the five keywords in a title, case-insensitively. The bump commit has
  `[skip ci]` on purpose.
- **A major version is refused on purpose** (spec R3). To go to 1.0.0 the maintainer does all four of these, in one pull request:
  (a) the `breaking` rule in `release/release.config.js` becomes `release: 'major'` (as written it can never produce a major);
  (b) 1.0.0 then happens only when a breaking commit (a `feat!:` title) reaches `main` after that;
  (c) the guard `case "$version" in ''|0.*)` of the `plan` job in `.github/workflows/release.yml` is removed or relaxed on purpose, because
  as written it also blocks 1.1.0 and every later major;
  (d) `release/test/dry-run.sh` pins the 0.x behaviour (`feat!` gives 0.2.0, `refactor!` 0.5.0), so `make release-test` and the CI
  `release` job fail until the test is updated in the same pull request.
- **Renovate titles.** Observed: the open Renovate pull requests #132 (the CLI pin) and #133 (an action bump) are titled
  `chore(deps): ...`. Still expected, not observed: `config:recommended` gives `fix(deps)` to npm `dependencies` and Go `require` bumps.
  `renovate.json` sets `semanticCommitType: chore` for `release/package.json` (the release tooling is not the product), so those bumps
  release nothing. A Go module or web dependency bump is still expected to be a `fix(deps)` patch release with no product change,
  unless you retitle it `chore(deps):`. The CLI pin pull request is `chore(deps)`; retitling it `fix: pin the claude CLI X.Y.Z` to ship
  it is the maintainer's decision (section 5).
- **`pr-title` is not a required check** (`main` is unprotected), so a red title can still be merged. Look at the check before you merge.
- A wrong type releases the wrong bump (spec A3). Do not rewrite a tag; fix forward.

## 3. One-time setup

Do these before the first merge, in this order. Each is outward-facing: do them yourself.

1. **The start tag.** Without it the first release would be `1.0.0` (and `plan` refuses it). With it the first `feat` is `0.1.0`
   and its changelog holds the history of `feat` and `fix` commits.

   ```sh
   git fetch origin
   git tag v0.0.0 $(git rev-list --max-parents=0 origin/main) && git push origin v0.0.0
   ```

   (`origin/main` has one root commit at the time of writing; the command needs exactly one.)

   The tagged commit holds only `README.md` and no workflow (checked with `git ls-tree`), so GitHub is expected to start no run for
   this tag; if a run starts anyway (the old `images.yml`), it fails at its first step, which is harmless.

2. **The squash merge setting.** The repository currently has `squash_merge_commit_title=COMMIT_OR_PR_TITLE` and
   `squash_merge_commit_message=COMMIT_MESSAGES`. With these a single-commit pull request is squashed with the commit's subject, not
   the title the check validated, and every commit message lands in the body, so a `[skip ci]` in any of them skips the release run.
   Set the title to the pull request title and the body to blank:

   ```sh
   gh api -X PATCH repos/Jaydee94/remedy -f squash_merge_commit_title=PR_TITLE -f squash_merge_commit_message=BLANK
   ```

   Optionally refuse the other merge methods, which bypass the title (a merge commit or a rebase keeps the commits' own messages):

   ```sh
   gh api -X PATCH repos/Jaydee94/remedy -F allow_merge_commit=false -F allow_rebase_merge=false
   ```

3. **Merge the pipeline pull request** with a conventional title such as
   `feat: automated releases with semantic-release, images and the Helm chart as OCI artifacts`. It is the first run; it is expected
   to release `0.1.0`. The first changelog will list the `feat` and `fix` commits since the first commit, but not pull request #130:
   its title is not a conventional commit, so its squash commit is not one either. Merge nothing else until the run is green: an open
   Renovate pull request merged meanwhile would move `main` and turn the first run into failure row 5, and muddy the proof of the first run.
   The first bump commit is expected to hold only `CHANGELOG.md`: `Chart.yaml` already says `0.1.0`, so `set-chart-version.sh` rewrites
   it unchanged.

4. **Make the three packages public.** The two image packages `remedy-server` and `remedy-runner` already exist on GHCR: the old
   `images.yml` pushed `sha-*` and `edge` when #130 was merged (run 37928003258, success, 2026-10-09T12:07Z, as told by the
   maintainer's coordinator). Their visibility was not checked (the token used here lacks `read:packages`), and they can be made public
   now. Only `charts/remedy` is created by the first release, expected private like a new package; make it public after the first run. In GitHub:
   profile, Packages, then the package, Package settings, Change visibility, Public. Without this the cluster cannot pull them.

   ```sh
   docker logout ghcr.io 2> /dev/null || true
   helm registry logout ghcr.io 2> /dev/null || true
   docker pull ghcr.io/jaydee94/remedy-server:0.1.0
   helm show chart oci://ghcr.io/jaydee94/charts/remedy --version 0.1.0
   ```

   The versions in these checks are the expected first release.

The first run is the proof of three things that cannot be shown before it:

- that `GITHUB_TOKEN` may push the bump commit to the unprotected `main`;
- that the `charts/remedy` package is linked to the repository (expected from the manifest annotation
  `org.opencontainers.image.source` that the chart push carries, measured on a local registry);
- the final check of `publish` (the tag, `Chart.yaml` at the tag, the GitHub release with its asset), and with it the `github` plugin,
  which the scratch test never runs.

Then write `docs/research/release-first-run.md` from the real run.

## 4. Failure points and recovery

Never move or delete a tag. Re-run **failed jobs only while `main` has not moved** (a re-run recomputes the same version and
overwrites the same tags). Re-run only the **latest** run of `main`: an older run moves `edge` backwards.

| # | State | Recovery |
|---|---|---|
| 1 | `plan` fails | Nothing is pushed. Re-run if it is the latest run of `main`; otherwise the next push does it. |
| 2 | `plan` says `none` for a merge with a `feat` or `fix` | Look for "behind the remote one" in the log: `main` moved and the newer run releases. Otherwise look at the commit messages that reached `main`. |
| 3 | `images` or `chart` fails with a version | `X.Y.Z` artifacts may be pushed; no commit, tag or release. Run "Re-run failed jobs" while `main` has not moved. |
| 4 | `publish`: the chart package is missing | As 3. |
| 4a | `publish`: semantic-release fails before the bump commit (the push is refused, `EGITNOPERMISSION`; or the version it computes differs from the planned one, which `scripts/check-expected-version.sh` reports in `verifyRelease`, before any commit or tag) | As 3: unused `X.Y.Z` artifacts, no commit, tag or release. Fix the permission or the settings (or find why the commits changed between `plan` and `publish`) and re-run the failed jobs while `main` has not moved. |
| 5 | `publish`: `main` moved | semantic-release exits 0 as "behind", the check "The tag exists" fails with "no tag". Nothing to do: the run of the newer push releases. |
| 6 | The bump commit is on `main` but the tag push failed | `main` has `chore(release): X.Y.Z` and no tag, and no GitHub release (expected order, from the plugin sources, not run here: bump commit pushed in `prepare`, then tag created and pushed, the GitHub release last). Either let the next run plan the same version (a duplicate changelog section is fixed by a `docs:` pull request), or tag by hand, `git fetch origin && git tag vX.Y.Z <sha of the bump commit> && git push origin vX.Y.Z`, then follow with row 7 (create the release by hand) and run the three checks: the tag exists locally and on the remote, `git rev-parse -q --verify refs/tags/vX.Y.Z && git ls-remote --exit-code --tags origin refs/tags/vX.Y.Z`; the chart at the tag, `git show vX.Y.Z:deploy/chart/Chart.yaml \| grep -E '^(version\|appVersion):'`; the release, `gh release view vX.Y.Z --json isDraft,assets` (not a draft, asset `remedy-X.Y.Z.tgz`). |
| 7 | The tag is pushed but the GitHub release failed or is a draft | A re-run of `publish` does not recover. Create the release by hand, with the chart from the run's artifact `chart` (or `helm pull oci://ghcr.io/jaydee94/charts/remedy --version X.Y.Z`): `gh release create vX.Y.Z remedy-X.Y.Z.tgz --title vX.Y.Z --notes-file <the CHANGELOG section of X.Y.Z>`. Or finish the existing one: `gh release upload vX.Y.Z remedy-X.Y.Z.tgz --clobber && gh release edit vX.Y.Z --draft=false` |
| 8 | A final check fails although the tag exists | Tag and `Chart.yaml` disagree: fix forward with a `fix:` pull request. |
| 9 | Before the first merge | Section 3: the tag `v0.0.0` and the squash setting. |

## 5. Skipping a release on purpose

- Merge only `docs`, `chore`, `ci`, `test`, `refactor`, `style` or `build` pull requests without a `!`: they release nothing, and the merge still
  pushes `sha-<short>` and `edge`. This is the normal way.
- `[skip ci]` in a commit message of the merge skips the whole workflow, so the images are not rebuilt either. Use it only when
  nothing should run.
- A pin bump of the CLI (`deploy/cli-pin.yaml`, a Renovate pull request) is expected to be a `chore(deps)` commit and releases nothing;
  the cluster reads the pin at the release tag. Retitling the pull request `fix: pin the claude CLI X.Y.Z` to release it is the
  maintainer's decision.
  Renovate opens the pull request; `cli-pin.yml` fails it until `scripts/cli-checksums.sh <version>` has written the checksums on
  its branch.

## 6. Where things are

| What | Where |
|---|---|
| The workflow | `.github/workflows/release.yml` (`plan`, `images`, `chart`, `publish`) |
| The pull request check | `.github/workflows/pr-title.yml`, `scripts/check-pr-title.sh` |
| The pull request image build (nothing pushed) | `.github/workflows/images.yml` |
| semantic-release configuration, pinned plugins | `release/release.config.js`, `release/package.json`, `release/package-lock.json` |
| The tests of the configuration | `release/test/dry-run.sh`, `scripts/chart-oci_test.sh`; `make release-test` (needs node, git, helm, docker) |
| The chart version and package | `scripts/set-chart-version.sh`, `scripts/package-chart.sh`, `scripts/push-chart.sh` |
| Tag against chart | `scripts/check-release.sh` |
| The changelog | `CHANGELOG.md` (written by the release) and the GitHub release |
| Design | `docs/specs/2026-10-09-release-automation-design.md`, plan `docs/plans/release-1-automation.md` |
| Installing a release | [`homelab-deploy.md`](homelab-deploy.md), `deploy/chart/README.md` |
