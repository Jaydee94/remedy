# Kubernetes deployment: what stays open after the branch

A plain backlog, not a plan. Each line says what is open and where it comes from. Nothing here is claimed to be done.

## 1. Owned by the maintainer

- Verify `DISABLE_UPDATES`: delete `~/remedy-kind/claude/.local`, run `make dummy-login` once and `/exit`, and confirm that
  `.local/share/claude` does not return. (Spec note on `DISABLE_UPDATES`; `docs/research/k8s-dummy-real-run.md`.)
- K-6 task 6 items 2, 3 and 6: `make dummy-logout`, then log in again; the change from logged in to not logged in on the Setup
  page; `make dummy-smoke` after it. (`docs/research/k8s-runner-status-real-run.md`, "Not run, on purpose".)
- The first release, by the release automation (replaces K-5 task 6; `docs/runbook/release.md`, `docs/plans/release-1-automation.md`
  task 6): create the tag `v0.0.0` on the first commit; set the repository's squash merge to the pull request title
  (`gh api -X PATCH repos/Jaydee94/remedy -f squash_merge_commit_title=PR_TITLE -f squash_merge_commit_message=BLANK`); merge the
  pipeline pull request with a `feat:` title; make the three packages (`remedy-server`, `remedy-runner`, `charts/remedy`) public; run the
  released images in the dummy once; write `docs/research/release-first-run.md`. The first run is expected to prove three things:
  the bump commit push by `GITHUB_TOKEN`, the chart package linked to the repository, and the final check of `publish`.
- The Renovate pull request for the CLI pin: Renovate has opened one on the branch `renovate/claude-code-cli-2.x` (a CLI bump).
  `cli-pin.yml` fails it until `scripts/cli-checksums.sh <version>` has written the checksums; run it on that branch. A pin bump is a
  `chore` commit that releases nothing: to ship it, title the pull request `fix: pin the claude CLI X.Y.Z`.
- The OCI chart install under Argo CD (`chart:` from `ghcr.io/jaydee94/charts` with a `ref` source for `deploy/cli-pin.yaml`) is not
  proven; the git source at the tag is the alternative. (`docs/runbook/homelab-deploy.md`, section 6.)
- The S3 run on k3s: `scripts/spike/k8s/s3-netpol.sh <homelab-context>`. It should also answer whether kube-router lets the
  node's traffic (the kubelet's probes on 8080 and 8082) through the default policies. (`docs/research/k8s-s3-networkpolicy.md`;
  `docs/runbook/homelab-deploy.md` section 6.)
- The confinement spike of spec section 11, R10: with the pinned CLI and the runner's exact flags, can the agent read a canary file
  outside its workspace (never the credential itself)? (`docs/specs/2026-10-06-kubernetes-deployment-design.md`, R10.)
- Success criterion 4 of the spec: the runbook run end to end under Argo CD on k3s, including Argo CD's handling of the
  `PostSync` hook and of the Secret without data. (`docs/runbook/homelab-deploy.md`, "Status of this runbook".)

## 2. Design follow-ups

- K-7, the login from the Remedy UI: a design decision first. (Spec section 10.1.)
- `networkPolicy.runner.probeFrom`: not built; only if a CNI blocks the kubelet's probes on port 8082. (Runbook section 6.)
- The same question for the control plane: `networkPolicy.public.from` does not include the node, so the server's own probes on
  8080 depend on the CNI. Decide whether the default needs a node peer. (Runbook section 6; unverified on k3s.)
- Redact `sk-ant-` patterns from stored run output as defence in depth. (Spec R10: `internal/redact` covers the prompt and the
  gatekeeper's call text only.)
- The interactive login command (`kubectl exec -it remedy-runner-0 -c runner -- /opt/claude/claude`) inherits
  `REMEDY_RUNNER_TOKEN` and starts an unrestricted session. Consider `/opt/claude/claude auth login` instead (S5 showed it prints
  the URL and reads the code; not verified in this flow), and add `-n remedy-system` to `loginCommand` in
  `web/src/runnerstatus.ts` (and its test). (`docs/research/k8s-s5-login-relay.md`.)

## 3. Parked minors

- Config: port-range validation for `REMEDY_INTERNAL_ADDR` and `REMEDY_RUNNER_STATUS_ADDR`.
- Server: the internal listener has no shutdown drain.
- `cmd/remedy-tokenrefresh`: an `http://` API address is accepted; the rune back-off is not exercised by `message_test`; a 404 is
  detected by a substring.
- `internal/cliinstall`: no command-level test for exit 1; the redirect cap of 5 is untested.
- Chart: an empty platform list renders; the init container's arguments are unquoted; the ingress test has no nil check;
  `public.from: [{}]` needs a comment in `values.yaml`; the refuser NetworkPolicy test is not tied to the pod labels; the CronJob
  and hook constants are not asserted; the refresher and hook pods have no `resources`, which matters under a LimitRange or a
  ResourceQuota.
- `scripts/cli-checksums.sh`: the read-back compares three fields; compare the files with `cmp`.
- CI: GitHub Actions are pinned by major tag, not by commit SHA; `azure/setup-helm` does not pin the Helm version, and the chart
  was never rendered with Helm 3 (only Helm 4.3.0 locally).
- Dummy scripts: `sed` with `|`, `&` or `\` in `REMEDY_KIND_DIR`; a relative `REMEDY_KIND_DIR`; a trailing 3 s sleep in
  `dummy-up`; no unit test of `runner_state`; the cookie jar is not removed on SIGINT; `apply_secret` replaces an existing
  `EXIT` trap; `dummy-logout` on a symlinked `claude` prints "removed" without deleting.
- Login check: its timeout kills the CLI but not the CLI's descendants.
- Runner: no test that a 401 from the control plane means "not connected".
- Release: merging to `main` triggers the first push of the `sha-*` and `edge` images (and, with a `feat` or `fix`, the release) to GHCR;
  the packages are expected to be private until they are made public.
