# The chart's cluster identities on a real cluster (plan K-3, task 4)

Date: 2026-10-07. Kubernetes server v1.37.0 (kubectl client v1.37.1), kind v0.33.0 (one control-plane node, Docker Desktop on arm64),
Helm v4.3.0. The images are `remedy-server:dev` and `remedy-runner:dev` from `make images`, loaded with `kind load docker-image`.
The cluster `remedy-k3` was created for this run and deleted afterwards (see Cleanup). No token and no password appears below: lengths and
12-character SHA-256 prefixes only.

## Setup

```sh
kind create cluster --name remedy-k3
for n in remedy-system demo argocd; do kubectl --context kind-remedy-k3 create namespace $n; done
kubectl --context kind-remedy-k3 -n remedy-system create secret generic remedy-secrets ...   # random values, not recorded
kubectl --context kind-remedy-k3 get endpoints kubernetes -o jsonpath='{.subsets[0].addresses[0].ip}:{.subsets[0].ports[0].port}'
# 172.18.0.2:6443
```

Values (kept outside the repository): `runner.enabled: false`, `existingSecret.name: remedy-secrets`,
`cluster: {enabled: true, write: {enabled: true, namespaces: [demo]}}`, `networkPolicy: {enabled: true, apiServer: {cidrs: [172.18.0.2/32]}}`.
The NetworkPolicies were on and enforced.

The `argocd` namespace holds no Argo CD. `kubectl auth can-i` resolves the resource type through discovery: for an unknown type
it prints a warning and answers `no`, so the first run of the script was wrong on every Argo CD line. The proof therefore applied
a stub `CustomResourceDefinition` for `applications.argoproj.io` (group, plural, `x-kubernetes-preserve-unknown-fields`; nothing
else). The script now refuses to run without that CRD.

## Install

```sh
helm --kube-context kind-remedy-k3 install remedy deploy/chart -n remedy-system -f k3-values.yaml --wait --timeout 5m
```

`STATUS: deployed`, `REVISION: 1`, 11 seconds. `--wait` returned after the post-install hook Job had completed: the Secret
held a token by the time Helm returned. The hook Job is deleted on success (`hook-succeeded`), so `kubectl get jobs` shows only the
CronJob afterwards.

Token length in `remedy-write-token` (`wc -c` of the Base64 `data.token`): 1280.

Server log:

```
level=WARN msg="REMEDY_K8S_WRITE_TOKEN_FILE holds no token yet: every cluster action fails until it does (the token refresher fills it)"
level=INFO msg="control plane listening" addr=:8080 db=/data/remedy.db pollInterval=1m0s
level=INFO msg="internal listener" addr=:8081
level=INFO msg="the cluster answers" version=v1.37.0 actions=true
```

The "holds no token yet" warning DID show at the first start: Helm starts the hook after the Deployment is ready, so the pod
always starts first. That is the designed behaviour. The pod was not restarted (restart count 0, same pod through two upgrades): a
debug container sharing the process namespace saw `/var/run/remedy/write/token` with 960 bytes (the raw JWT), so the mounted
Secret volume filled while the server ran. The server's `Writer` reads the file at every request, so no restart is needed.

## What each identity may do

`dev/kind/check-chart-identities.sh kind-remedy-k3 remedy-system demo argocd`, exit status 0:

```
-- remedy-read: get and list what the read tools show, nothing else
ok    remedy-read            yes list pods --all-namespaces
ok    remedy-read            yes get pods --subresource=log -n demo
ok    remedy-read            yes list deployments.apps -n demo
ok    remedy-read            yes list applications.argoproj.io -n argocd
ok    remedy-read            no  get secrets -n demo
ok    remedy-read            no  get secrets -n remedy-system
ok    remedy-read            no  list configmaps -n demo
ok    remedy-read            no  delete pods -n demo
ok    remedy-read            no  patch deployments.apps -n demo
ok    remedy-read            no  patch applications.argoproj.io -n argocd
-- remedy-write: restart, delete a pod, patch an application, and read nothing
ok    remedy-write           yes patch deployments.apps -n demo
ok    remedy-write           yes delete pods -n demo
ok    remedy-write           yes patch applications.argoproj.io -n argocd
ok    remedy-write           no  get pods -n demo
ok    remedy-write           no  get secrets -n demo
ok    remedy-write           no  delete pods -n kube-system
ok    remedy-write           no  patch deployments.apps -n kube-system
ok    remedy-write           no  patch deployments.apps -n remedy-system
ok    remedy-write           no  delete pods -n remedy-system
-- remedy-token-refresher: mint one account's token, patch one Secret, nothing else
ok    remedy-token-refresher yes create serviceaccounts/remedy-write --subresource=token -n remedy-system
ok    remedy-token-refresher yes patch secrets/remedy-write-token -n remedy-system
ok    remedy-token-refresher no  create serviceaccounts/remedy-read --subresource=token -n remedy-system
ok    remedy-token-refresher no  patch secrets/remedy-secrets -n remedy-system
ok    remedy-token-refresher no  get secrets/remedy-write-token -n remedy-system
ok    remedy-token-refresher no  create secrets -n remedy-system
ok    remedy-token-refresher no  list secrets -n remedy-system
-- no rights granted to the name remedy-runner (the runner is not installed in this proof, so this only shows that no role binds that name)
ok    remedy-runner          no  list pods --all-namespaces
ok    remedy-runner          no  get secrets -n remedy-system
all as expected
```

(The runner account does not exist in this install because the runner is off; `can-i` evaluates RBAC for any name, so these two
lines would pass for any unbound name: they show only that no role binds `remedy-runner`.) The case that matters most, the refresher minting a token for another account:

```
kubectl --context kind-remedy-k3 -n remedy-system auth can-i create serviceaccounts/default --subresource=token --as=system:serviceaccount:remedy-system:remedy-token-refresher
no
```

## A manual refresh, and an upgrade without hooks

```
before: 4616fc7727a5                      (the token the hook minted at install)
kubectl create job manual-refresh --from=cronjob/remedy-token-refresh   -> condition met
after the manual job: e5aae623049b        (a new token)
helm upgrade ... --no-hooks --wait        REVISION 2, deployed
after an upgrade without hooks: e5aae623049b   (unchanged)
job log: msg="stored a new token" account=remedy-write secret=remedy-write-token key=token expires=2026-10-07T13:08:11Z
```

The job ran at 11:08:11 UTC and the expiry is two hours ahead; the log carries no token text. A further upgrade with hooks
(REVISION 3) changed the hash to `d2a053adda2d` before `helm upgrade --wait` returned: the post-upgrade hook ran and `--wait`
waited for it.

## Claims left open by the review of task 3

- **The refresher under `readOnlyRootFilesystem`:** the manual job's container runs with `allowPrivilegeEscalation: false`,
  `capabilities.drop: [ALL]` and `readOnlyRootFilesystem: true`, and the job completed (3 seconds). The refresher needs no writable path.
- **The server's projected token is the one in use:** the pod has `automountServiceAccountToken: false` and runs as
  `remedy-read`; its only credential is the projected volume (`serviceAccountToken`, 3600 s, plus `kube-root-ca.crt` as `ca.crt`).
  The log line `the cluster answers version=v1.37.0 actions=true` therefore came from that token and that CA file.
- **The refresher's NetworkPolicy selects its pods:** the manual job's pod carries `app.kubernetes.io/component=token-refresh`,
  `app.kubernetes.io/instance=remedy`, `app.kubernetes.io/name=remedy`, which is the policy's `PodSelector`; egress is DNS
  (kube-dns, port 53) and `172.18.0.2/32` port 6443 only. A throwaway busybox pod in `remedy-system` with those labels could open `172.18.0.2:6443`
  (`nc`: open) and could not fetch `http://1.1.1.1` (`wget`: timed out, blocked). The same image in the same namespace without the labels fetched
  it (reached), so the block comes from the policy.

## Helm 4 (v4.3.0) against what the plan assumed from Helm 3

- Install and upgrade use server-side apply by default (`--server-side` default true), and `--wait` defaults to the `hookOnly` strategy,
  while `--wait` given alone is `watcher`. Both returned only after the hook Job completed (install and upgrade), and
  `helm status` showed `deployed`.
- The Secret `remedy-write-token` has two field managers, `helm` (Apply) and the refresher (Update). The chart's manifest has no
  `data` key, so Helm's server-side apply does not own it and an upgrade leaves it alone: proved with `--no-hooks`.
- `--no-hooks` is accepted by `helm upgrade` and skipped the hook (token unchanged).
- Hook annotations `post-install,post-upgrade`, `hook-weight`, `before-hook-creation,hook-succeeded` behaved as in Helm 3.
- Not exercised: a failing hook (`helm install` failing visibly) and `--wait-for-jobs`.

## Two mistakes in the plan's text, fixed here

- `kubectl auth can-i` has no `--resource-name` flag (`error: unknown flag`). The name goes into the type: `serviceaccounts/remedy-write
  --subresource=token`, `secrets/remedy-write-token`. The script and the step 6 command use that form.
- Without Argo CD's CRD the Argo CD answers are `no` for the wrong reason (see Setup).

## Result

This proves, on a real cluster with enforced NetworkPolicies and Helm 4: the sampled `can-i` answers of the script match the chart's RBAC
(the script is a finite sample; that the roles grant nothing more follows from reading their rules, which name `resourceNames`
for every refresher rule, and the unit tests that compare each role to its exact expected set). Of the accounts the refresher could be
asked to mint a token for, `default` and `remedy-read` were probed and refused. The hook fills `remedy-write-token` at install and
upgrade; the CronJob's job refreshes it with a new two-hour token; an upgrade without hooks keeps the token; the control plane starts
with an empty write token, warns, and picks the token up without a restart; the control plane reaches the API server with its projected
read token and the cluster CA; the refresher runs with a read-only root file system.

It does not prove: an action failing closed live (the unit test of the `Writer` pins that), a cluster action approved and executed with the
mounted write token (no agent run here), Argo CD's handling of the Secret and of the hook as `PostSync` (plan K-5), the runner
(plan K-4), or the behaviour when the token expires with a stopped refresher.

## Cleanup

```
$ kind delete cluster --name remedy-k3
Deleting cluster "remedy-k3" ...
Deleted nodes: ["remedy-k3-control-plane"]
$ kind get clusters
No kind clusters found.
```

The values file with the throwaway settings was removed from the scratchpad; the context `kind-forgedeck` of another project was never touched.
