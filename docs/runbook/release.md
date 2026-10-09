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
| `images` | Builds `remedy-server` and `remedy-runner` for `linux/amd64` and `linux/arm64` and pushes them. Runs on every push to `main`. |
| `chart` | Only with a version: lints and packages the chart with `version` and `appVersion` set to `X.Y.Z`, pushes it to the registry. |
| `publish` | Only with a version, after `images` and `chart`: semantic-release for real (bump commit, tag, GitHub release), then three checks. |

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
| a breaking change (`feat!:` or a `BREAKING CHANGE:` footer) | minor, not major |
| a revert made by git (`Revert "feat: ..."`) | patch |
| `docs`, `chore`, `ci`, `test`, `refactor`, `style`, `build` | nothing |

Traps:

- **`[skip ci]` skips the whole release run.** GitHub skips the run of a push when a commit message of the push contains
  `[skip ci]`, `[ci skip]`, `[no ci]` or `[skip actions]`: no images, no release for that merge. Keep these words out of titles
  and, while the squash body carries commit messages, out of every commit. (The bump commit has `[skip ci]` on purpose.)
- **A major version is refused on purpose** (spec R3). The `plan` job fails when the planned major is not 0, and the rules in
  `release/release.config.js` make a breaking change a minor. To go to 1.0.0 the maintainer changes the guard in
  `.github/workflows/release.yml` (the `case "$version"` step of `plan`) and the breaking rule in the configuration, deliberately.
- With the repository setting of section 3 the squash body is empty, so a breaking change can only be marked with `!` in the title.
  This follows from the setting; it was not tried.
- A wrong type releases the wrong bump (spec A3). Do not rewrite a tag; fix forward.

## 3. One-time setup

Do these before the first merge, in this order. Each is outward-facing: do them yourself.

1. **The start tag.** Without it the first release would be `1.0.0` (and `plan` refuses it). With it the first `feat` is `0.1.0`
   and its changelog holds the history of `feat` and `fix` commits. Nothing runs on a tag.

   ```sh
   git tag v0.0.0 $(git rev-list --max-parents=0 origin/main) && git push origin v0.0.0
   ```

   (`origin/main` has one root commit at the time of writing; the command needs exactly one.)

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
   `feat: automated releases with semantic-release, images and the Helm chart as OCI artifacts`. It is the first run.

4. **Make the three packages public** after the first run. They are expected to be created private by the first push. In GitHub:
   profile, Packages, then `remedy-server`, `remedy-runner` and `charts/remedy`, Package settings, Change visibility, Public.
   Without this the cluster cannot pull them.

   ```sh
   docker logout ghcr.io 2> /dev/null || true
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
| 5 | `publish`: `main` moved | semantic-release exits 0 as "behind", the check "The tag exists" fails with "no tag". Nothing to do: the run of the newer push releases. |
| 6 | The bump commit is on `main` but the tag push failed | `main` has `chore(release): X.Y.Z` and no tag. Tag the bump commit by hand and run the checks again, or let the next run plan the same version (a duplicate changelog section is fixed by a `docs:` pull request): `git fetch origin && git tag vX.Y.Z <sha of the bump commit> && git push origin vX.Y.Z` |
| 7 | The tag is pushed but the GitHub release failed or is a draft | A re-run of `publish` does not recover. Create the release by hand, with the chart from the run's artifact `chart` (or `helm pull oci://ghcr.io/jaydee94/charts/remedy --version X.Y.Z`): `gh release create vX.Y.Z remedy-X.Y.Z.tgz --title vX.Y.Z --notes-file <the CHANGELOG section of X.Y.Z>`. Or finish the existing one: `gh release upload vX.Y.Z remedy-X.Y.Z.tgz --clobber && gh release edit vX.Y.Z --draft=false` |
| 8 | A final check fails although the tag exists | Tag and `Chart.yaml` disagree: fix forward with a `fix:` pull request. |
| 9 | Before the first merge | Section 3: the tag `v0.0.0` and the squash setting. |

## 5. Skipping a release on purpose

- Merge only `docs`, `chore`, `ci`, `test`, `refactor`, `style` or `build` pull requests: they release nothing, and the merge still
  pushes `sha-<short>` and `edge`. This is the normal way.
- `[skip ci]` in a commit message of the merge skips the whole workflow, so the images are not rebuilt either. Use it only when
  nothing should run.
- A pin bump of the CLI (`deploy/cli-pin.yaml`, a Renovate pull request) is a `chore` or `ci` commit and releases nothing; the
  cluster reads the pin at the release tag. Give that pull request the title `fix: pin the claude CLI X.Y.Z` to release it.
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
