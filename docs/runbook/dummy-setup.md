# Runbook: the dummy setup, Remedy from its Helm chart in a kind cluster with the real agent

The record of the first run is [`docs/research/k8s-dummy-real-run.md`](../research/k8s-dummy-real-run.md). The design is
section 7 of the [spec](../specs/2026-10-06-kubernetes-deployment-design.md).

## What it is

A throwaway kind cluster (`remedy-dev`) that runs Remedy installed from the Helm chart (`deploy/chart`) with the real `claude`
CLI. In it: the demo workloads (`demo/web`, `demo/crashy` and others), Argo CD, and the chart (control plane, runner, token
refresher, NetworkPolicies). Not in it: GitHub (nothing creates an incident), and a fake agent: every run uses your Claude
subscription, so it costs quota and its text is not deterministic.

## You need

`docker` (Docker Desktop for Mac is what was measured), `kind`, `kubectl`, `helm`, `jq`, `curl`, `openssl`, `make`, and a Claude
subscription you can log in with. Port `18080` on the host must be free (`8080` is left to `make dev-server`).

## The commands

| Command | What it does |
|---|---|
| `make dummy-up` | Cluster, demo workloads, Argo CD, both images, the chart; on a fresh install it restarts the control plane once, after the write token exists. Cold start about 1.5 to 2 minutes. Ends with a `Ready.` block: the URL, where the password is, and the runner's login state (`the runner is logged in`, `NOT logged in` or `has not said yet`). |
| `make dummy-login` | Interactive, once: starts `claude` in the runner pod. Type `/login`, open the URL, paste the code, `/exit`. |
| `make dummy-smoke` | Two real runs (a question about a crashing deployment in `demo`, an approved restart of `demo/web`). About 20 s (19 to 25 s measured), two short runs of quota. Stops before the first run when the runner is not connected or not logged in. |
| `make dummy-status` | `key: value` lines (below). |
| `make dummy-logs` | Follows the server, the runner and the refresher (does not end; stop it with Ctrl-C). |
| `make dummy-redeploy` | Rebuilds both images with a new tag, loads them, `helm upgrade`; about 40 s. Ends `redeployed with the tag dev-...`. |
| `make dummy-reset` | Empties the database; cluster, secrets and login stay. Ends `the database is empty again`. |
| `make dummy-down` | Deletes the cluster and what the scripts generated (`dummy.env`); the login stays. About 1 s. |
| `make dummy-logout` | Interactive: deletes the CLI login after a confirmation. |

## The first time

```sh
make dummy-up        # about 1.5 to 2 minutes
make dummy-login     # once: /login, open the URL, paste the code, /exit
make dummy-smoke     # two real runs on your subscription
```

Then open `http://127.0.0.1:18080` and sign in. The password is `REMEDY_ADMIN_PASSWORD` in `~/remedy-kind/dummy.env`
(`grep REMEDY_ADMIN_PASSWORD ~/remedy-kind/dummy.env`; it is not printed anywhere else).

## Where things are

- `~/remedy-kind/dummy.env` (mode `0600`): the URL, the generated admin password, runner token and master key. Removed by
  `dummy-down`, made again by `dummy-up`.
- `~/remedy-kind/claude`: the CLI login (the runner pod's `HOME`, mounted into the kind node and exposed by a hostPath volume).
  It is outside the repository and outside the cluster on purpose, so that `dummy-down` and `dummy-up` do not ask for a login
  again; `dummy-down` never removes it, only `dummy-logout` does, and no script ever opens or lists it
  (`scripts/check-login-dir-untouched.sh` enforces it). Do not copy it anywhere.
- The directory is mode `0700` and belongs to you. That is enough on Docker Desktop for Mac, where the pod writes as uid 65532
  and the file still belongs to the host user. This was measured on Docker Desktop for Mac only. On a Linux Docker host a bind
  mount keeps the real owner, so the directory would need a `chown` to 65532 or a world-writable mode; no script does either, and
  the dummy does not claim to run there.
- The cluster itself (`kind get clusters`: `remedy-dev`, context `kind-remedy-dev`) and the Helm release `remedy` in the
  namespace `remedy-system`. They do not survive `dummy-down`.

## For an agent

- Every target is non-interactive except `dummy-login` and `dummy-logout`. Do not run those two; ask the maintainer.
- `make dummy-up` ends with a `login:` line (`the runner is logged in`, `NOT logged in`, or `has not said yet`; it waits up to
  a minute for the runner's report).
- `make dummy-status` prints, in this order:

  ```
  cluster: remedy-dev
  release: deployed (revision N)
  server: 1/1 ready, image remedy-server:dev-<time>
  runner pod: Running, restarts 0
  write token: present
  token refresher: last scheduled success <time or never>
  database volume: Bound
  login directory: ~/remedy-kind/claude (present)
  url: http://127.0.0.1:18080 (200 on /healthz)
  runner: connected, login ok
  secrets: ~/remedy-kind/dummy.env
  ```

  Other answers: `cluster: none` (exit 1; no cluster, run `make dummy-up`), `release: not installed` (exit 1), and
  `url: unknown (no ~/remedy-kind/dummy.env)` instead of the last two lines when `dummy.env` is missing. `write token:`
  is `present` or `missing`. The `runner:` line is `connected, login ok`, `connected, login missing`, `connected, login unknown` (the runner has not
  reported yet, for example right after a restart: ask again in a few seconds), `not connected, login unknown`, or `unknown (the
  control plane did not answer)`. `write token: present` says the Secret holds a token, not that the server pod already sees it.
  That cannot be asked (the server image has no shell). After a fresh `dummy-up` the control plane was restarted once, after the
  token existed, so its pod is expected to have the file; a pod started before the token existed is expected to see it only
  after the kubelet's next Secret sync (see Troubleshooting).
- `~/remedy-kind/dummy.env` has `REMEDY_URL` and `REMEDY_ADMIN_PASSWORD`. Use the password only to sign in at that URL (as JSON on
  stdin, never in an argument); do not print it, and do not write it into a file of the repository.
- Exit codes of the smoke test. `dev/kind/smoke.sh` called directly: 0 all ok; 1 a failed assertion, or a missing login (it
  prints `smoke: FAILED: ...` with the reason). Through `make dummy-smoke` a failure is 2, because GNU make answers 2 whenever a
  recipe fails (it prints `Error 1` for the script's own code). Test for "not 0" or call the script directly.
- Quota: each pass of the smoke test is two short runs of the maintainer's subscription. Do not loop it.
- The kube context is `kind-remedy-dev`. Pass it explicitly (`kubectl --context kind-remedy-dev ...`); never act on another
  context.

## Trying it by hand

Sign in at `http://127.0.0.1:18080`, open Ask Remedy (`/runs`), switch on the chips "Use gatekeeper tools" and "Read the cluster", and ask for example
"Which workload in demo is unhealthy and why?". Any change (a restart, a pod deletion, an Argo CD sync) waits for your approval
under Needs you. The prompts and what to look for are in [`cluster-real-run.md`](cluster-real-run.md) (it describes the
testbed with a local server; here the server and runner are the pods of the chart).

## Troubleshooting

One line each. Those marked "seen" happened in the first real run; the others are what the scripts and the spec say and were not
provoked.

- Seen, mitigated: the first smoke test shortly after a cold `dummy-up` once failed with `smoke: FAILED: run 2 has no single
  approved and succeeded restart`; the server log said `an approved tool failed ... open /var/run/remedy/write/token: no such
  file or directory`. A second run about three minutes later passed. Observed: the failure shortly after the install, the pass
  minutes later. Expected kubelet behaviour (not measured): the write token is filled by a hook after the server pod started,
  the pod mounts the Secret optional, and a running pod shows a new Secret file only after the kubelet's next sync, which
  takes up to a minute or two. `dummy-up` now restarts the control plane once on a fresh install (a release that is not
  `deployed`), after it has checked that the Secret holds the token; it prints one line saying so. In one check (1 of 1) the
  smoke test ran immediately after such a `dummy-up` and took 25 s with no failure; that is a mitigation seen once, not a proof.
  An upgrade (`dummy-up` on a running cluster) does not restart. If the control plane is started by other means before the
  token exists, the same window is expected: wait a minute or two, or `kubectl --context kind-remedy-dev -n remedy-system rollout
  restart deployment/remedy-server`.
- Seen: right after a redeploy or a reset the `runner:` line can say `login unknown` for a few seconds; ask again. (The chart's
  NOTES used to say that every run ends with "Not logged in" also for a runner that was logged in; they now say it only for a
  runner that was never logged in.)
- `write token: missing` (not provoked): the post-install hook did not fill the Secret. `kubectl --context kind-remedy-dev -n
  remedy-system get jobs` and `logs job/remedy-token-refresh-hook`; `make dummy-redeploy` runs the hook again (it is a
  post-upgrade hook too).
- `runner: not connected, login unknown` (not provoked): the runner does not reach the control plane yet or at all. Read
  `kubectl --context kind-remedy-dev -n remedy-system get pods` (an init container still downloading the CLI, a probe failing) and
  `logs remedy-runner-0 -c runner`; the NetworkPolicies are on.
- `runner: connected, login missing` (not provoked since the login): the runner was never logged in, or the login directory was
  removed by `dummy-logout`. The maintainer runs `make dummy-login`.
- Seen: `make dummy-smoke; echo "exit=$?"` prints 2, not 1: see "For an agent".
- Seen: the smoke test without a login stops in about 4 s with `the runner is not logged in. Run: make dummy-login` and costs
  nothing.
- The CLI's updater, an open verification. Measured by the maintainer (`du -sk` and a file count): under
  `~/remedy-kind/claude/.local` there is one file of 249528 KiB (about 244 MiB), created at the interactive login; the headless
  smoke runs created nothing in the runs observed (directory times unchanged). `.local` is not the login (the CLI's configuration
  directory is `/state/claude` in the pod, so the subdirectory `claude` of the host directory). Inferred from the size: it is the auto-updater's staged copy of the CLI; its contents
  were never inspected. Expected from the vendor's documentation (`docs/research/k8s-s4-cli-install.md`, found, not tested): such a
  copy takes effect only through a launcher, which the runner does not use, and `DISABLE_UPDATES=1` is meant to block the update
  paths. Decided (2026-10-09): the chart sets `DISABLE_UPDATES=1` in the runner container's environment, which the `kubectl exec`
  login is expected to inherit; no `provider.FilterEnv` entry. NOT YET VERIFIED: the maintainer deletes
  `~/remedy-kind/claude/.local` themselves, runs `make dummy-login` once after the redeploy (the runner is logged in, so the CLI is
  expected to open directly), types `/exit`, and checks that the directory does not come back. Until that is done, do not rely
  on it. No script and no agent lists or deletes anything in the login directory.
- The install is stuck (`helm ... --wait` runs the full 10 minutes): `kubectl --context kind-remedy-dev -n remedy-system get pods`
  and `describe pod`; most often a pod waits for an image (`make dummy-redeploy` loads them again) or the runner's init container
  cannot download the CLI (next line).
- The CLI download fails (the runner pod stays in `Init`, `kubectl ... logs remedy-runner-0 -c install-cli`): the node needs
  internet access to `downloads.claude.ai`, and the SHA-256 in `deploy/cli-pin.yaml` must match the downloaded file. A mismatch is
  refused on purpose.
- A pod is rejected with a Pod Security message: the namespace enforces `restricted`. A pod that runs as root, has a writable
  root filesystem or lacks the `RuntimeDefault` seccomp profile is refused; fix the chart, do not relax the label.
- A run or the server cannot reach something: the NetworkPolicies are on and kind enforces them. The API server's address
  comes from `endpoints/kubernetes` at install time (`networkPolicy.apiServer.cidrs`); if the cluster was remade with another
  address, `make dummy-redeploy` fills it again.
- `make dummy-up` says the cluster was started with `dev/kind/up.sh` and its accounts collide: run `make dummy-down`, then
  `make dummy-up`. The two setups do not share a running cluster. `up.sh` is the older testbed without the chart.
- `make dummy-up` says the cluster has no mount for the login directory (it was made before the dummy setup existed): run
  `make dummy-down`, then `make dummy-up`. The login directory is kept.
- Port `18080` is busy: stop what uses it; the port is fixed in `dev/kind/kind.yaml` and `dummy.env`.
- Asked to log in again after `dummy-down` and `dummy-up`: that must not happen (in the first real run the login survived `dummy-down` and `dummy-up` twice).
  Check that `~/remedy-kind/claude` exists with mode `0700` and was not removed by `dummy-logout`.
