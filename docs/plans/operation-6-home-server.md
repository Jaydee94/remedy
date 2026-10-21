# Operation on the home server (cycle 6) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remedy runs on the maintainer's k3s home server, reaches its Alertmanager and the NAS, and the drill of umbrella section 10 runs end to end and is recorded.

**Architecture:** Tasks 1 to 7 are code and documents in this repository: a measured filter for `docker ps -a`, two typed egress values in the chart, and the runbooks. They end in one `feat:` pull request whose merge makes the release the install uses. Tasks 8 to 13 are operations: facts from the server, pull requests in the `home-server` repository, the NAS onboarding, the drill and the clean-up. Every one of them changes a real machine or needs the maintainer's "go", so each is a checklist with a stop at its gate, not code.

**Tech Stack:** Go (stdlib), Helm templates and the Go render tests in `deploy/`, POSIX shell, the SSH testbed `dev/ssh/` (a container with real `sshd` and `sudo`), a real `docker` for the measurement.

**Spec:** [`docs/specs/2026-10-21-operation-6-design.md`](../specs/2026-10-21-operation-6-design.md) (PR #156). Parents: the umbrella, hosts-2, alertmanager-4, skills-5, kubernetes-deployment (listed in the spec).

## Global Constraints

- Everything in the repo is English: docs, code, comments, commit messages, UI copy. Conversation with the maintainer may be German.
- Pull request titles are conventional commits. Task 7's is `feat: ...` (the squash title decides the release); the spec and plan PRs are `docs:`.
- Commit messages end with `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`; PR bodies end with `🤖 Generated with [Claude Code](https://claude.com/claude-code)`.
- Go: stdlib first, no new dependency. Test first for logic. `make check` must pass before the pull request (it needs `helm`; `make web-install` first in a fresh worktree).
- Shell in tests and scripts runs on macOS and Linux: GNU form first, no BSD-only flags (`stat -f`, `sed -i ''`, `date -j`).
- A package is not to import another across the existing direction: `sshhost` does **not** import `hostexec` (a test in `hostexec_test` pins the two copies of a rule, as for `unitRE`).
- Nothing is run on the server or the NAS without the maintainer's "go" in this session, including a read-only `kubectl` over SSH. The agent's own checks stay on this machine.
- No auto-merge. The maintainer merges every pull request, in every repository. No force-push.
- Never spell a chart or image version in a test: read `Chart.yaml` (the release commits change `appVersion`).
- Values copied from the spec: container pattern `remedy-*`; account `remedy`; host name `ugreen-nas`; NAS address `192.168.178.118/32`, port `22`; drill container `remedy-drill`; alert labels `instance="ugreen-nas"`, `name="remedy-drill"`, severity `critical`, `for: 1m`; `diagnose_max_per_day` 0, then 5; `alertmanager_auto_max_age` 10 minutes for the drill; namespace `remedy`; host name `remedy.homeserver`; `cluster.write.enabled: false`.

## Review Focus

Failure modes the spec implies that no task would test unless named here. Each has its test in the task that owns the code.

1. **A pattern with a dot** (`my.app-*`): the docker filter is a regular expression, so `.` is looser than the pattern says. Accepted in the spec's call; Task 2 pins the executed filter for it so a change is a decision.
2. **An exact-name pattern** (`remedy-drill`, no star) must list `remedy-drill` and not `remedy-drill2`: the filter ends in `$`. Task 2 (string) and Task 3 (fake docker).
3. **An approved `host_change` with the words `docker ps -a`** classifies as a read form. It must run filtered too, not as the plain command. Task 4.
4. **A pattern the filter cannot express** must never fall back to the unfiltered command: fail closed, with a sentence for the agent. Task 2 and Task 4.
5. **An IPv6 `/128` entry, an IPv4 `/31` or `/0`, a port of 0 or 70000** in `networkPolicy.hosts`. The first is accepted, the others refused. Task 6.

---

### Task 1: Measure docker's name filter and sudo's reading of the line

The spec's section 5 rests on two facts that were not measured: that `docker ps -a --filter name=^remedy-` lists exactly the containers of that prefix with a real docker, and that `sudo` matches a sudoers line written with `\=` against that command. This task measures both, and stops the plan if either is wrong.

**Files:**
- Create: `scripts/spike/docker-ps-filter.sh` (a throwaway measurement; it removes what it makes)
- Create: `docs/research/docker-ps-filter.md` (the record)

**Interfaces:**
- Produces: the filter strings and the sudoers line shape that Tasks 2 and 3 implement, as the record's "Decision" section. If the record differs from the spec's section 5, Task 2 does not start: correct the spec first and tell the maintainer.

- [ ] **Step 1: Write the script.** It starts five containers from a small local image with a `sleep` command, named `remedy-a`, `remedy-drill`, `remedy-drill2`, `xremedy-c` and `paperless-db`; the script refuses to run if a container of one of these names exists already (it must never touch someone's containers). It prints, for each of these filters, the names `docker ps -a` lists: `name=^remedy-`, `name=^remedy-drill$`, `name=^/remedy-`, `name=remedy-`. It removes the containers on exit (trap).
- [ ] **Step 2: Run it** with the local Docker (the kind testbed uses the same daemon). Run: `sh scripts/spike/docker-ps-filter.sh`. Expected: `^remedy-` lists `remedy-a`, `remedy-drill`, `remedy-drill2` and neither `xremedy-c` nor `paperless-db`; `^remedy-drill$` lists only `remedy-drill`.
- [ ] **Step 3: Measure the sudoers side** in the SSH testbed's image (real `sudo`, `visudo`): build a throwaway sudoers file with the line `remedy ALL=(root) NOPASSWD: /usr/bin/docker ps -a --filter name\=^remedy-`, check it with `visudo -cf`, then as the account `remedy` run `sudo -n -l /usr/bin/docker ps -a --filter name=^remedy-` (expected: allowed), and `sudo -n -l /usr/bin/docker ps -a` and `... --filter name=^other-` (expected: refused). Repeat with a line for the exact form `name\=^remedy-drill$`. Also try the line written without the backslash and record what `visudo -cf` says.
- [ ] **Step 4: Write `docs/research/docker-ps-filter.md`** with the docker version, the commands, the observed lists, the `visudo` and `sudo -l` output, and a "Decision" section: the final filter strings for a prefix pattern and an exact name, and the final sudoers spelling.
- [ ] **Step 5: Gate.** Compare the Decision with the spec's section 5. If they agree, continue. If not, edit section 5 of the spec (a commit on the spec branch) and stop for the maintainer's look.
- [ ] **Step 6: Commit** the script and the record: `docs: measure docker's name filter and sudo's reading of the line`.

### Task 2: The executed argv of `docker ps -a`

**Files:**
- Modify: `internal/hostexec/classify.go` (or a new `internal/hostexec/psfilter.go` of its own, if `classify.go` gets crowded), `internal/hostexec/result.go`
- Test: `internal/hostexec/psfilter_test.go`

**Interfaces:**
- Consumes: `Verdict`, `Rules`, `patternRE` and `readForms` from `internal/hostexec/classify.go`.
- Produces:
  - `func PsFilter(pattern string) (string, bool)`: for a pattern that passes `patternRE`, `name=^remedy-` for `remedy-*` (prefix) and `name=^remedy-drill$` for `remedy-drill` (exact); `false` for an empty or invalid pattern. The strings are the Decision of Task 1.
  - `func Expand(v Verdict, argv []string, r Rules) ([]string, bool)`: returns `argv` unchanged for every verdict whose `Form` is not `docker ps -a`; for that form returns `["docker","ps","-a","--filter", <PsFilter>]`, and `false` when the filter cannot be built.
  - `Result.Note string`, and `FormatResult` writes a line `note: <Note>` after the exit code when it is not empty.

- [ ] **Step 1: Write the failing tests**, table-driven, with these exact assertions: `PsFilter("remedy-*") == ("name=^remedy-", true)`; `PsFilter("remedy-drill") == ("name=^remedy-drill$", true)`; `PsFilter("my.app-*") == ("name=^my.app-", true)` (review focus 1, the dot is not escaped, as the spec says); `PsFilter("") `, `PsFilter("*")`, `PsFilter("a b")`, `PsFilter("remedy-*x")` are `("", false)`. `Expand` of the Read verdict for `docker ps -a` with the pattern `remedy-*` is `["docker","ps","-a","--filter","name=^remedy-"]`; with a pattern `PsFilter` refuses it returns `(nil, false)` (review focus 4); `Expand` of `docker logs --tail 50 remedy-a` and of `df -h` returns the argv as given. A `FormatResult` test: a `Result{Note: "x"}` output contains the line `note: x` between the exit code and `stdout:`; with no note the output is byte-for-byte what it was (the existing `result_test.go` cases still pass).
- [ ] **Step 2: Run them to see them fail.** Run: `go test ./internal/hostexec -run 'PsFilter|Expand|FormatResultNote' -v`. Expected: FAIL, undefined `PsFilter`.
- [ ] **Step 3: Implement** `PsFilter`, `Expand` and the `Note` field as specified above. `Expand` compares `v.Form` with the literal of the table's form, taken from one constant that `readForms` also uses, so the two cannot drift.
- [ ] **Step 4: Run the package tests.** Run: `go test ./internal/hostexec -race`. Expected: PASS.
- [ ] **Step 5: Commit.** `feat: the executed argv of docker ps -a carries the host's container filter`

### Task 3: The snippet, the testbed and the fake docker

**Files:**
- Modify: `internal/sshhost/snippet.go`, `dev/ssh/sudoers-remedy`, `dev/ssh/fakes/docker`, `dev/ssh/README.md`
- Test: `internal/sshhost/snippet_test.go`, `internal/hostexec/classify_test.go` (the pin), `internal/sshhost/live_test.go` (tag `sshlive`)

**Interfaces:**
- Consumes: the Decision of Task 1 (the sudoers spelling `name\=^remedy-`).
- Produces: `Snippet` writes `<account> ALL=(root) NOPASSWD: /usr/bin/docker ps -a --filter name\=<regexp>` instead of `... docker ps -a`, where `<regexp>` is the prefix `^remedy-` for a pattern with a star and `^NAME$` for an exact name; an unexported `psFilterWord(pattern string) string` in `snippet.go` computes it.

- [ ] **Step 1: Write the failing tests.** In `snippet_test.go`, `grantLine`'s docker alternative for `ps` becomes `/usr/bin/docker ps -a --filter name\\=\^` plus the pattern's literal prefix (the regexp of the test is the only shape the generator may produce); `TestSnippetForTheNAS` expects the exact line `remedy ALL=(root) NOPASSWD: /usr/bin/docker ps -a --filter name\=^remedy-\n` and, for the pattern `remedy-drill`, `... name\=^remedy-drill$\n`. In `classify_test.go` (external package, it already imports `sshhost`) add the pin: for the patterns `remedy-*`, `remedy-drill`, `my.app-*` the sudoers line `Snippet` writes equals `/usr/bin/docker ps -a --filter ` plus `PsFilter(pattern)` with each `=` written as `\=`. The existing check that the snippet's comment block warns about `*` still holds.
- [ ] **Step 2: Run them to see them fail.** Run: `go test ./internal/sshhost ./internal/hostexec -run 'Snippet|PsFilterPin' -v`. Expected: FAIL.
- [ ] **Step 3: Implement** `psFilterWord` and the new line in `Snippet`. Then update the testbed: `dev/ssh/sudoers-remedy` gets the same line for `remedy-*`; the fake `dev/ssh/fakes/docker` answers `ps -a --filter name=<re>` by printing the header and only the names that match the anchored regular expression (use `grep -E` on the name column), keeps `other` in its unfiltered list so a test can see the filter work, and answers a plain `ps -a` with a line `fake docker: unfiltered ps` on stderr and exit code 1 (the real sudo would have refused it before). `dev/ssh/README.md` says so.
- [ ] **Step 4: Update the live test** (`internal/sshhost/live_test.go`, tag `sshlive`): the existing `docker ps -a` case runs the filtered command through `Exec` and expects `remedy-drill` and not `other`; a second case runs the unfiltered command and expects `sudo` to refuse it (non-zero exit, no `remedy-drill` in stdout).
- [ ] **Step 5: Run.** Run: `go test ./internal/sshhost ./internal/hostexec -race`, then `dev/ssh/up.sh && . ~/remedy-ssh/env.sh && go test -tags sshlive ./internal/sshhost -run Live -v`. Expected: PASS (the testbed rebuilds its image from the new sudoers file; `visudo -cf` in the Dockerfile checks the syntax).
- [ ] **Step 6: Commit.** `feat: the onboarding snippet and the testbed filter docker ps -a`

### Task 4: `host_read` and an approved `host_change` run the filtered command

**Files:**
- Modify: `internal/gatekeeper/tools_host.go` (the `read()` and `change()` `Run` functions, the tool descriptions)
- Test: `internal/gatekeeper/tools_host_test.go`, `internal/app/learner_chain_test.go` and `internal/app/skills_chain_test.go` (their fakes expect the argv `docker ps -a`)

**Interfaces:**
- Consumes: `hostexec.Expand`, `Result.Note` (Task 2). The fake `HostAccess` of the tests records `execs[i].argv`.
- Produces: the argv that reaches `HostAccess.Exec` for the ps form is the expanded one; the activity and the stored call arguments still carry the agent's words (`["docker","ps","-a"]`); the result text carries `note: only containers matching the host's pattern remedy-* are listed`.

- [ ] **Step 1: Write the failing tests** in `tools_host_test.go`: (a) the existing `TestHostReadRunsAFormOfTheTableWithoutAnApproval` now expects `execs[0].argv == ["docker","ps","-a","--filter","name=^remedy-"]` with `sudo` true and the answer containing `note: only containers matching` and the pattern; (b) `TestHostReadPsOfAnExactPatternIsAnchored`: a host with the pattern `remedy-drill` gets `name=^remedy-drill$`; (c) `TestAnApprovedChangeWithThePsWordsRunsFiltered` (review focus 3): `host_change` with `docker ps -a`, approved through the existing `approveChange` helper, reaches `Exec` with the expanded argv; (d) `TestPsOnAHostWithNoPatternRunsNothing` (review focus 4): `docker ps -a` on a host with no container pattern answers the existing refusal and `execs` stays empty (the `!ok` branch of `Expand` cannot be reached through the real classifier, which refuses the same patterns first; it is covered by Task 2 and is defence in depth); (e) the stored call arguments and the activity text for `host_read` show the agent's words, not the filter.
- [ ] **Step 2: Run to see them fail.** Run: `go test ./internal/gatekeeper -run 'HostRead|ApprovedChangeWithThePs|PsOn' -v`. Expected: FAIL (the argv is unfiltered).
- [ ] **Step 3: Implement.** In `read()`'s `Run`, after the verdict is `Read`: `run, ok := hostexec.Expand(verdict, v.Argv, info.rules())`; `!ok` returns an `ArgumentError` ("this host's container pattern cannot be used as a filter; nothing was run"); pass `run` to `h.exec`; set `res.Note` when `run` differs from `v.Argv`. In `change()`'s `Run`, do the same with `hostexec.Classify(v.Argv, info.rules())` before `h.exec`; the sudo flag stays `true`. `Description` of `host_read` says that `docker ps -a` lists only the host's containers.
- [ ] **Step 4: Update the chain tests** in `internal/app` whose fake expects the plain argv, and run: `go test ./internal/gatekeeper ./internal/app -race`. Expected: PASS.
- [ ] **Step 5: Commit.** `feat: host_read and approved host_change list only the host's containers`

### Task 5: Chart values `networkPolicy.hosts` and `networkPolicy.alertmanager`

**Files:**
- Modify: `deploy/chart/values.yaml`, `deploy/chart/templates/networkpolicy.yaml` (the `remedy-server` policy only), `deploy/chart/templates/validate.yaml`, `deploy/chart/ci/lint-values.yaml`, `deploy/chart/README.md`
- Test: `deploy/networkpolicy_hosts_test.go` (new; uses the helpers of `deploy/helpers_test.go` and `deploy/chart_test.go`: `baseValues`, `set`, `mustRender`, `mustFail`, `policy`, `egressRules`, `ipBlocks`, `rulePorts`)

**Interfaces:**
- Produces: `networkPolicy.hosts` (list of `{cidr, port}`, default `[]`) and `networkPolicy.alertmanager` (`{namespace, podSelector, port}`, default `{}`, `port` default 9093). Each `hosts` entry renders as **one egress rule** `to: [{ipBlock: {cidr}}]`, `ports: [{protocol: TCP, port}]`, after the API-server rule and before the internet rule. The Alertmanager rule renders as one rule `to: [{namespaceSelector: {matchLabels: {kubernetes.io/metadata.name: <namespace>}}, podSelector: {matchLabels: <podSelector>}}]`, `ports: [{protocol: TCP, port}]`. Neither appears in `remedy-runner` or `remedy-token-refresher`.

- [ ] **Step 1: Write the failing tests** in `deploy/networkpolicy_hosts_test.go`: `TestHostAndAlertmanagerEgressAreAbsentByDefault` (the server policy has 2 egress rules with the cluster off and 3 with it on, exactly as the pinned tests say); `TestAHostEntryOpensOneSSHRuleOnTheServerPolicyOnly` (cidr `192.168.178.118/32`, port 22: one extra rule whose only peer is that `ipBlock` without an `except`, ports `["TCP/22"]`; the runner and refresher policies have no such address); `TestSeveralHostEntriesMakeSeveralRules`; `TestHostEntriesAreSingleAddressesOnly` (review focus 5, through `mustFail` with the substring `networkPolicy.hosts`): `192.168.178.0/24`, `192.168.178.118/31`, `0.0.0.0/0`, `192.168.178.118` (no length), `::/0`, a port 0, a port 70000, a missing port are refused, and `fd00::1/128` with port 22 renders; `TestAHostEntryMayNotBeTheAPIServer` (the same address as `networkPolicy.apiServer.cidrs`, with `cluster.enabled`); `TestTheAlertmanagerRuleNamesNamespaceAndPodLabels` (the peer has both selectors, the port defaults to `TCP/9093` and can be set); `TestTheAlertmanagerRuleNeedsANamespaceAndALabel` (`mustFail` on a missing namespace, an empty `podSelector`, a namespace with an upper-case letter, a port of 0); `TestPoliciesOffIgnoreBothValues` (`networkPolicy.enabled=false` renders no NetworkPolicy and no error).
- [ ] **Step 2: Run to see them fail.** Run: `go test ./deploy -run 'HostAndAlertmanager|HostEntr|AHostEntry|TheAlertmanagerRule|PoliciesOffIgnore' -v`. Expected: FAIL (needs `helm` on PATH; without it they skip, which is not a pass: install it).
- [ ] **Step 3: Implement.** The values with the comments of the spec's section 4; the template rules in the `remedy-server` policy only; the refusals in `validate.yaml` in the style of the existing `fail (printf "networkPolicy.hosts: ...")` messages (each message names the value and the rule). IPv4 entries must match `^(\d{1,3}\.){3}\d{1,3}/32$`, IPv6 entries `^[0-9a-fA-F:]*:[0-9a-fA-F:]*/128$`; ports are integers from 1 to 65535. Add one example of each value to `ci/lint-values.yaml` so `helm lint` and `kubeconform` cover the rules, and a paragraph to the chart's README under the NetworkPolicies row.
- [ ] **Step 4: Run.** Run: `make chart-check`. Expected: PASS, and `kubeconform` (when installed) accepts the render with the new values.
- [ ] **Step 5: Commit.** `feat: chart values for SSH and Alertmanager egress of the control plane`

### Task 6: Documents of this repository

**Files:**
- Create: `docs/runbook/home-server-drill.md`
- Modify: `docs/runbook/homelab-deploy.md`, `docs/runbook/alertmanager.md` (the two paragraphs that say the chart has no value for extra egress), `docs/specs/2026-10-09-hosts-and-skills-design.md` (status, the sentence of section 3, the sudoers lines of section 4, section 13's cycle 6 point), `docs/specs/2026-10-11-hosts-2-tools-design.md` (the table row for `docker ps -a`), `docs/design.md` (roadmap), `CLAUDE.md` ("Current state": cycle 6 specified, the filter and the chart values implemented, the install and the drill not made)

- [ ] **Step 1: The runbook `home-server-drill.md`** holds the steps of the spec's section 6 as commands the maintainer runs (Tasks 8 to 13 of this plan are its source), with the abort steps of section 7. Every command that changes something is marked "changes the NAS" or "changes the cluster".
- [ ] **Step 2: Rewrite `homelab-deploy.md`** for the wrapper chart under the home-server ApplicationSet (namespace `remedy`, one source), keep the three-source form as the alternative for a cluster without such an ApplicationSet, and keep its "not run" status line until Task 11 has run.
- [ ] **Step 3: Update the other documents** as listed. The umbrella's sentence that the Paperless containers are not visible gets a pointer to the spec's section 5.
- [ ] **Step 4: Check the links** with `grep -rn '](' docs/runbook/home-server-drill.md` and open each relative target exists; run `make check`. Expected: PASS.
- [ ] **Step 5: Commit.** `docs: runbook for the home-server drill and the wrapper-chart install`

### Task 7: The pull request and the release

- [ ] **Step 1: Run `make check`, and `go test -race ./internal/hostexec ./internal/sshhost ./internal/gatekeeper ./internal/app`.** Expected: PASS. (The full `go test -race ./internal/server` takes about 25 minutes and is not part of this task.)
- [ ] **Step 2: Open the pull request** `feat: filtered docker ps -a and egress values for hosts and Alertmanager (cycle 6a)`; the body lists the Task 1 measurement, what was accepted (the dot in a pattern), and what is not done (everything from Task 8 on).
- [ ] **Step 3: Gate: the maintainer reviews and merges.** After the merge, check that the release run published the images, the chart and the tag (`docs/runbook/release.md`), and write the released version into the notes of Task 9.

### Task 8: Facts from the server (gate: the maintainer says "go")

These are read-only commands over SSH on the server (`ssh -i ~/.ssh/id_ed25519 jaydee@192.168.178.127 'sudo kubectl ...'`). Write the answers into the skeleton `docs/research/home-server-drill.md` (created here, committed with the record in Task 13).

- [ ] **Step 1:** the API server: `kubectl get endpoints kubernetes -o jsonpath='{.subsets[0].addresses[0].ip}:{.subsets[0].ports[0].port}'`.
- [ ] **Step 2:** the Alertmanager: `kubectl -n monitoring get svc,pods -l app.kubernetes.io/name=vmalertmanager --show-labels` (find the name and labels as they are; if the label differs, use the one the output shows), and the port of the service.
- [ ] **Step 3:** the node: `kubectl get nodes -o jsonpath='{.items[0].status.nodeInfo.architecture}'`.
- [ ] **Step 4:** the packages: `gh api users/Jaydee94/packages/container/remedy-server --jq .visibility` for `remedy-server`, `remedy-runner` and `charts%2Fremedy`; any that is private is made public by the maintainer.
- [ ] **Step 5:** the real series: query the VictoriaMetrics instance for `container_last_seen{instance="ugreen-nas"}` and note the labels (is there a `name` label; the value for a Paperless container). Query is read-only (`curl` on the vmsingle service from the server).
- [ ] **Step 6:** DNS: does `remedy.homeserver` resolve (Pi-hole wildcard) or does Pi-hole need an entry; note what `docs/09-dns-architecture.md` of the home-server repository says.

### Task 9: home-server pull request 1: Remedy (gate: the maintainer merges)

Prepared by the author as a branch in `~/git/home-server` (outward-facing: the pull request is opened only on the maintainer's word). That repository's rules apply: pull requests only, green CI (`yamllint`, `helm lint`, `actionlint`), no plaintext secret.

**Files (in the home-server repository):**
- Create: `argocd/apps/remedy/Chart.yaml` (name `remedy`, a dependency on `oci://ghcr.io/jaydee94/charts` chart `remedy` at the version Task 7 released, no `~` or `^`), `argocd/apps/remedy/values.yaml` (everything under `remedy:`), `argocd/apps/remedy/templates/namespace.yaml`, `argocd/apps/remedy/templates/sealedsecret-remedy.yaml`, `docs/24-remedy.md`
- Modify: `argocd/apps/monitoring/templates/networkpolicy.yaml` (add the namespace `remedy` to the list of allowed sources), `docs/gotchas.md` (one line each for what the first sync teaches)

- [ ] **Step 1: The values.** Under `remedy:`: `existingSecret.name: remedy-secrets`; `image` untouched; `runner.model` pinned; `runner.cli` copied from `deploy/cli-pin.yaml` at the release tag, with the platform of Task 8 step 3; `ingress.enabled: true`, `className: traefik`, `host: remedy.homeserver`; `cluster.enabled: true`, `cluster.write.enabled: false`; `networkPolicy.apiServer.cidrs` and `port` from Task 8 step 1; `networkPolicy.hosts: [{cidr: 192.168.178.118/32, port: 22}]`; `networkPolicy.alertmanager` from Task 8 step 2; `networkPolicy.public.from` the `kube-system` namespace (Traefik).
- [ ] **Step 2: Render it locally** with `helm dependency build argocd/apps/remedy && helm template remedy argocd/apps/remedy --namespace remedy` and `kubeconform -strict` when installed. Expected: renders, no `fail`; the server policy shows the SSH and Alertmanager rules; no write token objects (the write side is off).
- [ ] **Step 3: The `SealedSecret`.** The maintainer generates the three values (the runbook's section 4 commands), seals them on the server, and commits only the sealed file. The master key is also stored in the maintainer's password manager.
- [ ] **Step 4: The `Namespace`** with `pod-security.kubernetes.io/enforce: restricted`; the open question (does Argo CD accept it next to `CreateNamespace`) is answered by the first sync; if not, drop the template and label by hand.
- [ ] **Step 5: Open the pull request**, the maintainer merges, Argo CD syncs. Watch: the namespace, `remedy-server` and `remedy-runner-0` reaching Ready (the probes under kube-router are unproven; the runbook's section 12 has the workarounds), no `PostSync` hook (write is off).
- [ ] **Step 6: Record** what the first sync did, including every surprise, in the research skeleton.

### Task 10: The first look (gate: Task 9 is synced)

- [ ] **Step 1:** the login: `kubectl -n remedy exec -it remedy-runner-0 -c runner -- /opt/claude/claude auth login` (the runbook's section 7; the form with `auth login` is the one the follow-ups list proposes and is itself unverified here: if it does not print the URL, use `/opt/claude/claude` and `/login`).
- [ ] **Step 2:** open the UI through `kubectl -n remedy port-forward svc/remedy-server 8080:8080` (an encrypted path; never type a secret over the plain LAN address), sign in, check Setup: runner connected, logged in, cluster read side on.
- [ ] **Step 3:** set `diagnose_max_per_day` to 0. Enter the Alertmanager URL from Task 8 step 2. Wait one poll interval, open the incidents list: record which alerts appeared and that none was diagnosed.
- [ ] **Step 4:** record the control plane's log lines for the Alertmanager source and the cluster read side.

### Task 11: The NAS (gate: the maintainer does every command on the NAS)

- [ ] **Step 1:** in the UI add the host: name `ugreen-nas`, address `192.168.178.118`, port 22, account `remedy`, container pattern `remedy-*`, no units, rule `instance="ugreen-nas"`. Copy the generated snippet (it now has the filtered `docker ps -a` line).
- [ ] **Step 2:** on the NAS: create the account `remedy` (not root, not in `docker`); append the public key of a key made for this purpose; write the sudoers file with `visudo -f /etc/sudoers.d/remedy`, after `command -v docker` has shown the path the lines name (change the lines if UGOS puts docker elsewhere); `chmod 0440`.
- [ ] **Step 3:** in the UI enter the private key (through the port-forward), delete the key file from disk afterwards, run the connection test, compare the shown fingerprint with `ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub` on the NAS, pin it. Expected report: account is not root, not in `docker`, `sudo -n` limited to the lines.
- [ ] **Step 4:** start the drill container: `docker run -d --name remedy-drill --label app=remedy-drill --restart=no busybox sh -c 'while true; do echo "remedy-drill alive $(date)"; sleep 10; done'`. Check that cAdvisor shows it with `name="remedy-drill"` (Task 8 step 5's query).
- [ ] **Step 5:** from the Ask page ask a question that selects the NAS host chip and reads `docker ps -a`; confirm in the run's output that only `remedy-*` containers are listed (the first live check of Task 4 against the real NAS).

### Task 12: The drill rule and the drill (gate: the maintainer merges and stops the container)

- [ ] **Step 1: home-server pull request 2:** `argocd/apps/monitoring/templates/vmrule-remedy-drill.yaml`, a `VMRule` named `remedy-drill` in `monitoring`, one rule `RemedyDrillContainerDown`, expression `absent(container_last_seen{instance="ugreen-nas",name="remedy-drill"})` (adjusted to what Task 8 step 5 measured, including what `docker stop` does to the series), `for: 1m`, labels `severity: critical`, `instance: ugreen-nas`, `name: remedy-drill`, annotations `summary` and `description`. Validate the expression in VictoriaMetrics before the pull request is opened; the alert must not fire while the container runs.
- [ ] **Step 2:** after the merge and a vmalert reload, set `diagnose_max_per_day` to 5 and `alertmanager_auto_max_age` to 10m in Setup.
- [ ] **Step 3: The drill.** The maintainer runs `docker stop remedy-drill` on the NAS and notes the time. Record, with timestamps from the UI and the database: the alert in Alertmanager; the incident (host mapped to `ugreen-nas`); the automatic run; its `host_read` calls (`docker ps -a`, `docker logs`); the approval card with the exact command `docker start remedy-drill` in Needs you; the approval and the container running; the alert resolving; the incident closing.
- [ ] **Step 4: The checks of the record.** From the run's prompt, tool results and answer (in the database and the log): no Paperless container name, no credential, no `192.168.178.118`. From the database: `sqlite3 remedy.db` over `host_credentials` shows only the sealed column and the hint.
- [ ] **Step 5 (optional): the second pass.** Stop the container again; deny the approval; once more, let it expire (`auto_approval_timeout`, 15 minutes). Record both.

### Task 13: The learning run, the clean-up and the record

- [ ] **Step 1:** on the NAS host's page press "Get to know this host"; review the proposals with their evidence; confirm some, reject some. Record how useful the profile is, given that `host_read` shows only `remedy-*` containers.
- [ ] **Step 2: Clean up.** `docker rm -f remedy-drill` on the NAS; home-server pull request 3 deletes the `VMRule`; the maintainer decides which settings stay and whether the NAS host stays in the inventory. Set `diagnose_max_per_day` back to what the maintainer wants.
- [ ] **Step 3: The record.** Finish `docs/research/home-server-drill.md` (sections: what was installed, the first sync, the first look, the NAS, the drill with timings, the second pass, the learning run, findings, what was not covered). Update `homelab-deploy.md`'s status line with what is now proven (the OCI dependency under Argo CD, the probes under kube-router, the hook not exercised) and what is not; update `CLAUDE.md`, `docs/design.md` and the umbrella's status for cycle 6.
- [ ] **Step 4: Commit and open** `docs: the record of the home-server drill (cycle 6)`; the maintainer merges.

---

## Self-review

- **Spec coverage:** sections 2 and 6 are Tasks 8 to 13; section 3's table is the file lists of Tasks 2 to 6 and 9; section 4 is Task 5; section 5 is Tasks 1 to 4 (the unverified part is Task 1, the existing-hosts paragraph is in Task 6's runbook and the snippet of Task 3); section 7 is Task 6 step 1; section 8's tests are in Tasks 2 to 5, the measurement in Task 1; section 9's criteria are Tasks 10 to 13; section 11 is Task 6 and Task 13; section 12's open points are Task 8 (service name, series, DNS, the packages, architecture) and Task 11 step 2 (the docker path); two are left open on purpose: whether Argo CD builds the OCI dependency (answered by Task 9 step 5) and whether Renovate should watch the wrapper's chart version (the maintainer's call after the first sync).
- **Types:** `PsFilter` and `Expand` are defined in Task 2 and used with those names in Tasks 3 and 4; `Result.Note` is defined in Task 2 and set in Task 4; `psFilterWord` is private to `sshhost` and pinned against `PsFilter` by Task 3's test.
- **Review focus:** items 1 and 2 have their strings in Task 2, item 3 in Task 4 test (c), item 4 in Task 2 and Task 4 test (d), item 5 in Task 5.
- **Known weak step:** the `!ok` branch of `Expand` in the gatekeeper is unreachable through the real classifier; Task 2's unit test covers it, and no seam is added only for it.
- **Proportion:** the spec is about 150 lines of dense text; the plan is longer because Tasks 8 to 13 are checklists of operations on real machines, not code.
