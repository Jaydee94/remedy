# Cycle 7a: a stranger can install Remedy, implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Someone who has never seen the maintainer's lab can install Remedy from a release with plain `helm install`, on a stock cluster, without a value or a sentence that only the maintainer's setup has.

**Architecture:** Mostly copy, defaults and docs, no migration. The CLI pin moves into the chart's default values (one file less to pass), `networkPolicy.public.from` loses its k3s default, every lab value leaves the shipped files and a script keeps it that way, and a generic install runbook replaces the homelab runbook as the first thing a stranger reads.

**Tech Stack:** POSIX shell (scripts and their tests), Helm and Go render tests in `deploy/`, Markdown, a few lines of React copy.

**Spec:** `docs/specs/2026-10-21-self-hosted-for-others-design.md` (sections 7, 10 step 1, 12, 13 points 2 and 8). 7b (API-key mode) and 7c (webhook) are not part of this plan: no sentence written here may say that key mode or webhooks exist.

## Global Constraints

- Everything in the repo is English (docs, code, comments, UI copy, commit messages). Pull request titles are conventional commits; the implementation pull request is `feat!:` (two defaults change for existing installations), the plan's own pull request is `docs:`.
- The licence is MIT (`LICENSE`, #161); the three packages are public (measured anonymously on 2026-10-21). Do not touch either.
- "The maintainer" in agent-facing text (`internal/prompt/*.go`, `internal/gatekeeper/tools_*.go`) stays: a prompt edit needs a real run to re-verify (spec section 7, a call).
- The login relay and the login-directory guard are unchanged. Nothing here reads, copies or stores a CLI login.
- Historic documents keep their lab values and their `deploy/cli-pin.yaml` mentions: `docs/specs/`, `docs/research/`, `docs/plans/` (the spec lists the first two; `docs/plans/` is added here as a call, because the plans of earlier cycles are history too, 111 hits in one of them), `CHANGELOG.md`, `docs/runbook/home-server-drill.md` (cycle 6's, an example by design) and `scripts/spike/`.
- Tests: Go tests need PostgreSQL (`make test` starts one); chart tests need `helm` (`REMEDY_REQUIRE_HELM=1`); shell tests run with `make shell-test`. Shell in tests runs on macOS and Linux: no BSD-only flags.
- `make check` is the authority before every pull request.

## Review Focus

1. **An existing installation upgrades without `networkPolicy.public.from`.** The render must fail with a message that names the value and gives the three presets (k3s Traefik, ingress-nginx, a node CIDR), not a bare `fail`. The maintainer's home-server values must set it before the upgrade. (Task 4.)
2. **An operator still passes `-f deploy/cli-pin.yaml` of an older tag, or copies a pin into their own values.** Their values must win over the chart's default pin, and the file being gone from the repository must not break `helm install` of an older chart. (Task 3.)
3. **`runner.enabled=false` with the pin now defaulted.** Nothing of the CLI is rendered and nothing fails; the existing test `TestTheRunnerCanBeLeftOut` keeps passing. (Task 3.)
4. **The lab-values script on awkward input.** Binary tracked files (`docs/assets/logo.svg`, PNGs) are skipped, matching is case-insensitive, a file name with a space works, running outside a git repository is an error and not a pass, and an untracked file is not scanned. (Task 1.)
5. **`scripts/cli-checksums.sh` on the new location.** It refuses a file that does not have exactly one line of each shape. `deploy/chart/values.yaml` must have exactly one, and a test must fail when somebody adds a lookalike `version: "..."` line to it later. (Task 3.)

---

## File structure

| File | Responsibility |
|---|---|
| `scripts/check-no-lab-values.sh`, `scripts/lab-values.deny`, `scripts/check-no-lab-values_test.sh` | The deny-list check over tracked files, its patterns, its test. |
| `deploy/chart/values.yaml` | Gains the pinned CLI as the default of `runner.cli`; `networkPolicy.public.from` defaults to empty. |
| `deploy/chart/templates/networkpolicy.yaml` | The failure message for an empty `public.from`. |
| `deploy/cli-pin.yaml` | Deleted. |
| `scripts/cli-checksums.sh`, `.github/workflows/cli-pin.yml`, `renovate.json`, `dev/kind/lib.sh`, `dev/kind/dummy-values.yaml` | Point at the chart's `values.yaml` instead of `deploy/cli-pin.yaml`. |
| `deploy/chart_test.go`, `deploy/helpers_test.go` | New render tests; `baseValues` names `public.from`. |
| `SECURITY.md` (new), `README.md`, `deploy/chart/Chart.yaml`, `deploy/chart/README.md`, `docs/design.md`, `CLAUDE.md` | Positioning and pointers. |
| `docs/runbook/install.md` (new), `docs/runbook/homelab-deploy.md` | The generic install; the old runbook becomes the maintainer's example with a true status line. |
| `docs/research/install-walkthrough.md` (new) | The record of walking `install.md` on a throwaway kind cluster. |

---

### Task 1: The lab-values check

**Files:**
- Create: `scripts/check-no-lab-values.sh`, `scripts/lab-values.deny`, `scripts/check-no-lab-values_test.sh`

**Interfaces:**
- Produces: `sh scripts/check-no-lab-values.sh` run from anywhere inside a git work tree. Exit 0: clean. Exit 1: prints one `path:line: text` per hit on stdout and a closing line naming `scripts/lab-values.deny`. Exit 2: not a git work tree, or the deny file missing or empty. Task 2 wires it into `make shell-test`.
- `scripts/lab-values.deny` holds one extended-regex pattern per line; blank lines and lines starting with `#` are ignored. Initial patterns: `ugreen`, `paperless`, `jaydee94/homelab`, `/volume1`.

- [ ] **Step 1: Write the failing test** `scripts/check-no-lab-values_test.sh` in the style of `scripts/check-release_test.sh`: each case builds a scratch repository in `mktemp -d` (`git init`, files, `git add`), copies the script and a small deny file into it, runs the script from inside, and counts failures. Cases and assertions: (a) a tracked `web/x.tsx` containing `Ugreen-NAS` exits 1 and the output contains `web/x.tsx:1:`; (b) the same text in `docs/specs/a.md`, `docs/research/a.md`, `docs/plans/a.md`, `docs/runbook/home-server-drill.md`, `CHANGELOG.md` and `scripts/spike/a.sh` exits 0; (c) a tracked binary file (bytes `\0` plus the word `ugreen`) exits 0; (d) an untracked file with the word exits 0; (e) a tracked file named `a b.md` (outside the excluded paths) with the word exits 1 and the output names it; (f) a deny file with a `# comment` line and a blank line, and a pattern `a.c`, matches `abc` in a tracked file (exit 1); (g) running in a directory that is not a git work tree exits 2; (h) a missing deny file exits 2; (i) a clean repository exits 0 and prints nothing.
- [ ] **Step 2: Run it to see it fail.** Run: `sh scripts/check-no-lab-values_test.sh`. Expected: FAIL, the script does not exist.
- [ ] **Step 3: Implement** `scripts/check-no-lab-values.sh`. Approach: `git ls-files -z`, drop the excluded prefixes and files (the list of Global Constraints plus `scripts/lab-values.deny`, `scripts/check-no-lab-values.sh`, `scripts/check-no-lab-values_test.sh` and `.claude/`), then `grep -I -n -i -E -f <patterns>` over what is left with `xargs -0`; build the pattern file with `grep -v -E '^(#|$)'`. The deny file is read from the script's own directory, the files from the work tree's root.
- [ ] **Step 4: Run the test** (`sh scripts/check-no-lab-values_test.sh`, expected: PASS) and the script on this repository (`sh scripts/check-no-lab-values.sh`, expected: exit 1 with hits; this list is Task 2's work list, keep it).
- [ ] **Step 5: Commit.** `feat: a check that no lab value ships in the repository`

### Task 2: Take the lab values out

**Files:**
- Modify: `web/src/HostsPage.tsx:68`, `web/src/components/setup/ReposSection.tsx:61`, `web/src/components/hosts/HostForm.tsx:84`, `web/src/components/hosts/AddHostForm.tsx:38`, `README.md` (Alertmanager block, line 187 to 188), `internal/alertmatch/alertmatch.go:124`, `internal/hostexec/psfilter.go:26`, `internal/alertmanager/alertmanagertest/testdata/home-server.json`, every `*_test.go` and `*.test.ts` that Task 1's run lists, `Makefile` (`shell-test`)

**Interfaces:**
- Consumes: the hit list of Task 1 step 4.
- Produces: `sh scripts/check-no-lab-values.sh` exits 0 on the repository, and `make shell-test` runs it and its test.

- [ ] **Step 1: Copy, exactly.** `HostsPage.tsx`: "such as a NAS, a VM or a server" instead of "such as your NAS". `ReposSection.tsx`: `Use owner/name, for example acme/infra.` (and the one line in `web/src/setup.test.ts` that pins the old example). `HostForm.tsx` hint: `nas\\.local` becomes `myhost\\.local`. `AddHostForm.tsx` placeholder: `nas` becomes `myhost`. README: `instance="myhost"` and `instance=~"myhost(:[0-9]+)?"`. `alertmatch.go` error text: `for example "myhost"` (and its test). `psfilter.go` comment: a generic sentence, "another application's containers", instead of Paperless.
- [ ] **Step 2: Rename test data with equal lengths, mechanically**, so no test that pins a length, a cut or an order changes meaning: `ugreen` to `myhost` (also inside `ugreen-nas`, giving `myhost-nas`) and `paperless` to `nextcloud`, in each case variant that occurs (`Paperless` to `Nextcloud`, `PAPERLESS` to `NEXTCLOUD`). Apply to the fixture `home-server.json` (keep the file name: tests refer to it) and to the test files of the hit list. Do not rename anything in a file that is on the excluded list.
- [ ] **Step 3: Run the suites that changed:** `go test ./internal/... -count=1`, `cd web && npm test && npm run lint`. Expected: PASS. A failure means a test depended on the real word (a pattern such as `remedy-*` next to `paperless-*`): fix the test's data, never the rule it checks.
- [ ] **Step 4: Wire the check.** Add `sh scripts/check-no-lab-values_test.sh` and `sh scripts/check-no-lab-values.sh` to `shell-test` in the `Makefile`, after the two `check-login-dir-untouched` lines, and extend the target's description. Run: `make shell-test`. Expected: PASS.
- [ ] **Step 5: Commit.** `chore: no lab value in the shipped files, and a check that keeps it so`

### Task 3: The CLI pin ships with the chart

**Files:**
- Modify: `deploy/chart/values.yaml:111-118`, `scripts/cli-checksums.sh` (default of `pin`, header comment), `scripts/cli-checksums_test.sh` (only if it names the default path), `.github/workflows/cli-pin.yml` (`paths` and the comment), `renovate.json` (`managerFilePatterns`), `dev/kind/lib.sh:227`, `dev/kind/dummy-values.yaml:2`, `deploy/chart/ci/lint-values.yaml`, `deploy/chart/ci/postgres-values.yaml`, `deploy/chart/README.md:309-330`, `docs/runbook/release.md:167-170`, `docs/runbook/dummy-setup.md:155`
- Delete: `deploy/cli-pin.yaml`
- Test: `deploy/chart_test.go`

**Interfaces:**
- Produces: `runner.cli` in `deploy/chart/values.yaml` holds the real pin (version, urlTemplate, `archive: none`, both platforms with their SHA-256), moved verbatim from `deploy/cli-pin.yaml`. `scripts/cli-checksums.sh` defaults to `--pin deploy/chart/values.yaml`. A chart rendered with no `runner.cli` value installs that CLI.
- Ordering: Renovate's bump of the pin (#132, CLI 2.1.316) is merged, so `deploy/cli-pin.yaml` already holds 2.1.316 and its checksums: move exactly those values. A later Renovate pull request on `deploy/cli-pin.yaml` that is still open when this task starts is landed or closed first, otherwise it conflicts with the deletion; a closed one is opened again against the new path.

- [ ] **Step 1: Write the failing tests** in `deploy/chart_test.go`: `TestTheChartShipsAPinnedCLI` renders `baseValues()` with `runner.cli` removed (`delete` through a helper) and asserts the `install-cli` args of the runner StatefulSet carry a `--version=` that is not empty, an `https://` url template, and two `--platform=` arguments whose checksum is 64 lowercase hex characters and differs between the two. `TestAnOperatorsPinWinsOverTheDefault`: `baseValues()` (its `2.1.288`) renders `--version=2.1.288`. `TestThePinInValuesHasOneLineOfEachShape`: read `chart/values.yaml` as text and assert that exactly one line matches each of the four regular expressions copied from `scripts/cli-checksums.sh` (`re_version`, `re_url`, `re_amd64`, `re_arm64`).
- [ ] **Step 2: Run them to see them fail.** Run: `go test ./deploy -run 'ShipsAPinned|OperatorsPin|OneLineOfEachShape' -v`. Expected: FAIL (the default `version` is empty).
- [ ] **Step 3: Move the pin.** Put the values of `deploy/cli-pin.yaml` into `runner.cli` of `values.yaml` as defaults and keep the comment lines that explain them (rewritten without "homelab"); delete `deploy/cli-pin.yaml`. Keep the `required` calls in `runner-statefulset.yaml`: they still guard an operator who blanks a value.
- [ ] **Step 4: Point the consumers at the new place:** the script's `pin=` default and its header comment; the workflow's `paths` (`deploy/chart/values.yaml`); the Renovate manager's `managerFilePatterns` (`/^deploy\/chart\/values\.yaml$/`) while its `matchStrings` stays; `dev/kind/lib.sh` drops the `-f .../cli-pin.yaml` argument; the comment in `dummy-values.yaml`; `lint-values.yaml` loses its own `runner.cli` block so `helm lint` covers the default pin (check `postgres-values.yaml` the same way); the chart README's "Install from the OCI registry" example loses `-f deploy/cli-pin.yaml` and the paragraph under it says the pin is the chart's default and is overridden by `runner.cli.*`; `release.md` and `dummy-setup.md` name the new file.
- [ ] **Step 5: Prove the Renovate pattern still reads the file.** Run: `python3 -c 'import re,sys; t=open("deploy/chart/values.yaml").read(); m=re.search(r"cli:\n\s+version: \"([0-9][0-9.]*)\"", t); print(m.group(1) if m else sys.exit(1))'`. Expected: prints the pinned version. If a comment line between `cli:` and `version:` breaks it, move the comment above `cli:`.
- [ ] **Step 6: Run.** `go test ./deploy -count=1` with `REMEDY_REQUIRE_HELM=1`, `sh scripts/cli-checksums_test.sh`, `sh scripts/cli-checksums.sh --check` (needs network; expected: exit 0), `make chart-check`. Expected: all PASS.
- [ ] **Step 7: Commit.** `feat!: the chart ships the pinned CLI as its default`

### Task 4: `networkPolicy.public.from` has no default

**Files:**
- Modify: `deploy/chart/values.yaml:134-140`, `deploy/chart/templates/networkpolicy.yaml:2-3`, `deploy/helpers_test.go` (`baseValues`), `deploy/chart_test.go:395`, `deploy/chart/ci/lint-values.yaml`, `deploy/chart/ci/postgres-values.yaml`, `dev/kind/dummy-values.yaml`, `deploy/chart/README.md`
- Test: `deploy/chart_test.go`

**Interfaces:**
- Consumes: Task 3 (the chart renders without `runner.cli`).
- Produces: `networkPolicy.public.from: []` by default; the render fails when `networkPolicy.enabled` is true and the list is empty, with a message that names `networkPolicy.public.from` and the three presets.

- [ ] **Step 1: Write the failing tests.** `TestTheChartNeedsToKnowWhoMayReachTheUI`: `baseValues()` without `networkPolicy.public` fails (`mustFail`) with the substrings `networkPolicy.public.from`, `kube-system`, `ingress-nginx` and `ipBlock`. `TestTheChartRendersWithOnlyTheValuesOfAStockCluster` (spec section 7, point 4): values are `existingSecret`, `postgres.existingSecret`, `networkPolicy.public.from` (a namespaceSelector for `ingress-nginx`) and nothing else, no `runner.cli`, no `cluster`; it renders, the public rule names `ingress-nginx`, and the rendered text contains neither `kube-system` nor any deny-list word of `scripts/lab-values.deny`. `TestPoliciesOffNeedNoPublicPeers`: `networkPolicy.enabled=false` renders without `public.from`. Update `baseValues` to carry an `ingress-nginx` peer and the assertion at `chart_test.go:395` to expect that name.
- [ ] **Step 2: Run them to see them fail.** Run: `go test ./deploy -run 'NeedsToKnow|StockCluster|PoliciesOffNeedNoPublic' -v`. Expected: FAIL.
- [ ] **Step 3: Implement.** `values.yaml`: `public: {from: []}` with a comment that lists the three presets as YAML (k3s Traefik: namespace `kube-system`; ingress-nginx: namespace `ingress-nginx`; a node or proxy CIDR as an `ipBlock`) and says why an empty list is refused. `networkpolicy.yaml`: the `fail` text carries the same three presets in one sentence each. The policies-off path must not evaluate the check.
- [ ] **Step 4: Give the known installations the value they relied on.** `lint-values.yaml`, `postgres-values.yaml` and `dev/kind/dummy-values.yaml` name `kube-system` explicitly (kind keeps the behaviour it has today). Add an "Upgrading from 0.17" paragraph to `deploy/chart/README.md` and to `homelab-deploy.md` section 9: set `networkPolicy.public.from` before the upgrade, and the `-f deploy/cli-pin.yaml` source can go.
- [ ] **Step 5: Run.** `make chart-check`, then `make dummy-up` is **not** run here (it costs a cluster build); `dev/kind/lib.sh` is checked by `make shell-test`. Expected: PASS.
- [ ] **Step 6: Commit.** `feat!: the chart has no default for who may reach the UI`

### Task 5: Positioning and the security policy

**Files:**
- Create: `SECURITY.md`
- Modify: `README.md` (intro lines 7 to 10, a new "Who is this for" block after the status block, the sentence at line 171, links), `deploy/chart/Chart.yaml:3`, `deploy/chart/README.md` (intro), `docs/design.md` (section 1, the roadmap line of cycle 7), `CLAUDE.md` ("Current state": cycle 7a, the pin's new place, the new variable-less default, the lab-values script under Commands)

**Interfaces:**
- Consumes: Tasks 3 and 4 for the facts the README states.
- Gate (spec open point 1): the maintainer has to re-read the vendor's legal page before the README goes public. Ask before this task's commit whether they did. If they did not, write the "Who is this for" block without any statement about what the terms allow, and say in the pull request that it waits for that reading.

- [ ] **Step 1: `SECURITY.md`.** Supported versions (the latest release), how to report: GitHub's private vulnerability reporting for this repository (a Security tab link), no public issue for a vulnerability, what to include, what to expect (an answer in a reasonable time, stated as intent not as a promise). The setting has to be turned on by the maintainer: record that as a step of the pull request description, and do not invent an e-mail address. No `CONTRIBUTING.md` (open point 2: not wanted until the maintainer says so).
- [ ] **Step 2: README and chart copy,** with the spec's section 12 wording, limited to what exists after 7a: the first sentence becomes "Self-hosted AI operator for your infrastructure."; the agents sentence becomes "Agents run the unmodified Claude Code CLI on your own subscription login. Each installation belongs to one operator; Remedy is not a service and has no user accounts." (key mode and Antigravity are not mentioned: neither exists); the "Who is this for" block says individuals, one installation each, and why there are no teams (one subscription login must not serve several people); the sentence about `ANTHROPIC_API_KEY` stays as it is true today. `Chart.yaml` description: "Remedy, a self-hosted AI operator for your infrastructure. The control plane and the runner." The README's install paragraph links `docs/runbook/install.md` first and `homelab-deploy.md` as the maintainer's example. `docs/design.md` section 1 gets the goal sentence of the spec's section 12; its roadmap names cycle 7a.
- [ ] **Step 3: Check.** Run: `sh scripts/check-no-lab-values.sh` (expected: exit 0) and `grep -n 'homelab' README.md deploy/chart/Chart.yaml deploy/chart/README.md` (expected: only links to the maintainer's runbook).
- [ ] **Step 4: Commit.** `docs: position Remedy for other operators and add a security policy`

### Task 6: The generic install runbook and its walkthrough

**Files:**
- Create: `docs/runbook/install.md`, `docs/research/install-walkthrough.md`
- Modify: `docs/runbook/homelab-deploy.md` (status line, first paragraph, every `deploy/cli-pin.yaml` mention at lines 8, 19, 130, 151, 194, 234, 269, 274, 286, 366, 373, 385), `docs/runbook/home-server-drill.md:55` (one note that the pin is now the chart's default), `docs/runbook/alertmanager.md` (nothing, unless Task 2 touched its example)

**Interfaces:**
- Consumes: Tasks 3 to 5.
- Produces: `docs/runbook/install.md` with these sections, in this order: what you need (a cluster with a NetworkPolicy-enforcing CNI, `helm`, `kubectl`, outbound access to `downloads.claude.ai` and `ghcr.io`, a Claude subscription login of your own); the namespace; the two Secrets with `kubectl create secret` (`remedy-secrets` with `admin-password`, `runner-token`, `master-key`, and `remedy-postgres` with `password`, generated as in `homelab-deploy.md` section 4 but without `kubeseal`); a values file for a stock cluster (`existingSecret`, `postgres.existingSecret`, `networkPolicy.public.from` with the three presets, `ingress.*` or none, `cluster.enabled: false` as the smallest start); `helm install remedy oci://ghcr.io/jaydee94/charts/remedy --version X.Y.Z -n remedy -f values.yaml` (no pin file); reaching the UI without an Ingress (`kubectl -n remedy port-forward svc/remedy-server 8080:8080`); the one-time login (the Setup page's "Log in from here", with `kubectl exec -it` from `homelab-deploy.md` section 7 as the fallback); first steps (GitHub token, a repository, optionally a cluster and a host, with links to the existing runbooks); upgrading; what is accepted and said plainly (the UI has a password only and belongs on a LAN or a VPN; five failed logins per TCP peer lock out everyone behind a proxy; the cookie is not `Secure` without TLS; the master key is not recoverable); one operator per installation.

- [ ] **Step 1: Write `install.md`** from the contents above. Every command that is not shown in an existing runbook is run in Step 3 before it stays in the text.
- [ ] **Step 2: Update `homelab-deploy.md`.** The status line states what is true: the chart and the images are public, the OCI install is still unproven under Argo CD, and the maintainer's own install is the record of `docs/research/alert-recurrence-and-tools-real-run.md`. The first paragraph says this runbook is the maintainer's example and points to `install.md`. Each `cli-pin.yaml` mention is reworded to "the chart's default `runner.cli`" or removed (a source with `ref` for the pin is no longer needed in the three-source form; say so once, in section 6).
- [ ] **Step 3: Walk it.** On a throwaway kind cluster (`dev/kind/up.sh` builds one; if the cluster has no ingress controller, use the port-forward path and `networkPolicy.public.from` with the `kube-system` namespace), follow `install.md` from the top with the latest release's chart: Secrets, values, `helm install`, pods Ready, the UI through the port-forward, sign-in. Stop before the login (it costs subscription quota and needs the maintainer). Write `docs/research/install-walkthrough.md`: date, chart and CLI versions, each command that did not work as written and the correction, what was not walked (the login, a real Ingress). Note: the walkthrough installs a **published** chart, which still has the old defaults until the implementation pull request is released; pass `-f` with the pin and the `public.from` value of the new text only after the release, or walk the local `deploy/chart` for the new defaults and say which one was walked.
- [ ] **Step 4: Finish `install.md`'s status line** with exactly what the record shows, and link the record.
- [ ] **Step 5: Run** `sh scripts/check-no-lab-values.sh`, `make check`. Expected: PASS.
- [ ] **Step 6: Commit and open** `feat!: a generic install for other operators (cycle 7a)`; the description carries the upgrade notes of Tasks 3 and 4, the `SECURITY.md` setting the maintainer has to turn on (`gh api -X PUT repos/Jaydee94/remedy/private-vulnerability-reporting`), and the list of what the README does not say yet (key mode, webhooks).

---

## Self-review

- **Spec coverage (section 7):** `SECURITY.md` (Task 5); the homelab runbook's status line and a generic runbook (Task 6); `public.from` (Task 4); the CLI pin (Task 3); `Chart.yaml` and the README intro (Task 5); the README Alertmanager example, `HostsPage`, `ReposSection`, `HostForm`, `AddHostForm`, the fixture (Task 2); the prompt text stays (Global Constraints); `docs/specs/2026-10-09-*` stays (history); the login lock said plainly in the runbook (Task 6); the script and its wiring (Tasks 1 and 2); the render test on stock values (Task 4). Section 12 items that depend on 7b or 7c (key-mode sentences, the webhook in section 2.3, the risk rows, `api-key-mode.md`, `webhooks.md`, the research paragraph on strangers) are deliberately left out and named in Task 6's pull request description.
- **Open points 8 of section 13:** the pin ships as the chart's default values (Task 3); the deny-list is `scripts/lab-values.deny`, starting with four patterns, and the maintainer's domains are not in it because they are not known to the plan (the maintainer adds them to that one file); the no-Ingress path is the port-forward in Task 6.
- **Calls made here, open to change:** `docs/plans/` is excluded from the check; equal-length renames in tests; `feat!:` for the implementation pull request; GitHub's private vulnerability reporting instead of an address; no `CONTRIBUTING.md`.
- **Types and names:** `scripts/check-no-lab-values.sh` and `scripts/lab-values.deny` are named the same in Tasks 1, 2, 4, 5 and 6; the test names of Tasks 3 and 4 do not repeat.
