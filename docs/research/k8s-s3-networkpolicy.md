# S3: what NetworkPolicies do on kind and on k3s

Spike S3 of `docs/plans/k8s-0-spikes.md`. It feeds plan K-2 (the chart's policy defaults, `networkPolicy.apiServer.cidrs`) and K-4 (whether the dummy enables the policy). The spec behind the question is `docs/specs/2026-10-06-kubernetes-deployment-design.md` (D11, section 5, section 9).

**Status:** the kind half is measured (2026-10-07). The k3s half is a maintainer step and **not measured yet**; see "k3s: to be filled in" below.

## What was measured

`scripts/spike/k8s/s3-netpol.sh <context>` creates the namespace `netpol-spike` with three pods (`web`: busybox httpd on 8081; `client` and `other`: curl) and applies five policies one after the other, always on `app=client` except the last:

1. no policy (baseline)
2. default-deny egress
3. the runner's shape: DNS, the `web` pod on 8081, and `0.0.0.0/0` except `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `169.254.0.0/16` on TCP 443
4. the control plane's shape: DNS plus `ipBlock <api endpoint address>/32` on the endpoint port (no service address, no 443 to the internet)
5. ingress on `web`: only `app=client` on 8081

After each policy it probes from `client`: the `web` pod, `https://example.com/`, `https://kubernetes.default.svc/version` (the service address), `https://<endpoint ip>:<port>/version` (the endpoint address) and DNS (`nslookup`). A line is `blocked` when curl cannot connect within 4 s. An HTTP status from the API server (here 200, anonymous `/version`) means reachable.

## kind: result

Environment: `kind create cluster --name remedy-spike` with the default CNI, one node, Kubernetes v1.37.0, containerd 2.3.4, arm64 (Docker Desktop, linuxkit kernel 7.0.14). Raw output kept outside the repository. Both images (`busybox:1.37`, `curlimages/curl:8.11.1`) pulled on arm64 without changes to the script.

```text
context: kind-remedy-spike
remedy-spike-control-plane   Ready   control-plane   ...   INTERNAL-IP 172.18.0.2   v1.37.0
api server: service 10.96.0.1:443, endpoint 172.18.0.2:6443

== 1. no policy
  web pod (private, 8081)            200
  internet 443 (example.com)         200
  api service (kubernetes.default.svc) 200
  api endpoint (172.18.0.2:6443)     200
  dns (getent via nslookup)          ok
== 2. default-deny egress for app=client
  web pod (private, 8081)            blocked
  internet 443 (example.com)         blocked
  api service (kubernetes.default.svc) blocked
  api endpoint (172.18.0.2:6443)     blocked
  dns (getent via nslookup)          blocked
== 3. the runner's shape
  web pod (private, 8081)            200
  internet 443 (example.com)         200
  api service (kubernetes.default.svc) blocked
  api endpoint (172.18.0.2:6443)     blocked
  dns (getent via nslookup)          ok
== 4. the server's shape (endpoint by address)
  web pod (private, 8081)            blocked
  internet 443 (example.com)         blocked
  api service (kubernetes.default.svc) 200
  api endpoint (172.18.0.2:6443)     200
  dns (getent via nslookup)          ok
== 5. ingress: only app=client may reach the web pod on 8081
  client -> web                      200
  other -> web (must be blocked)     000
blocked
```

The last two lines of section 5 are one result: `curl -w` printed `000` and then the script's `|| echo blocked` fired, so `other -> web` was blocked (the script prints both; cosmetic, not fixed).

### Reading

- **kind enforces policies.** Section 1 is fully reachable, section 2 is blocked everywhere including DNS. kind's default CNI in this version therefore enforces `NetworkPolicy` (which CNI it is was not checked); the assumption that it might not was wrong for this version. The dummy can run with the policies on.
- **The runner's shape (3) does what D11 wants:** DNS, the in-cluster `web` pod and the internet on 443 work; the API server is blocked by both its addresses. Note why the API is blocked here: the node (and so the API endpoint) is `172.18.0.2`, inside the excepted `172.16.0.0/12`. The `except` list is what keeps the runner from the API server on kind; on a cluster whose node address is outside all four excepted ranges, the `except` list would not block it, because a public node address is allowed on 443 (K-2 should say so in the chart notes).
- **The `web` pod (cluster pod IP, `10.244.x.x`) is reachable in shape 3 through the `podSelector` rule, not through the `ipBlock`:** the pod range is inside the excepted `10.0.0.0/8`. So "the control plane's internal port" has to be allowed by a pod/namespace selector, not by an address.
- **Egress to the API by endpoint address works and lets the service address through (4):** with only `172.18.0.2/32:6443` allowed, both `kubernetes.default.svc` (`10.96.0.1:443`) and the endpoint answer 200. The service address is translated to the endpoint before the policy is evaluated, so the rule names the endpoint address and port (from `endpoints/kubernetes`), not the ClusterIP. A rule on the ClusterIP would not be needed (not tested). The rule must carry the endpoint port (6443 here), not 443.
- DNS through the `namespaceSelector: {}` + `k8s-app=kube-dns` rule works in 3 and 4.
- **Ingress (5) is enforced:** `client -> web` 200, `other -> web` blocked.
- Caveat for K-2/K-4: the endpoint address on kind is the Docker network address of the node container (`172.18.0.2` here) and can differ between clusters and machines. The dummy has to read it at install time (`kubectl get endpoints kubernetes`) rather than hard-code it.

## k3s: to be filled in

Not measured yet (maintainer step 3). The agent does not touch the homelab cluster. The maintainer runs once, naming the homelab context:

```sh
scripts/spike/k8s/s3-netpol.sh <homelab context> 2>&1 | tee ~/remedy-spike/s3-k3s.txt
```

It creates the namespace `netpol-spike` with three tiny pods, applies and deletes five policies in it, and deletes the namespace. The output is pasted here under this heading. It fills these Decision lines: `k3s enforces policies` (section 2 blocked everywhere = yes), `runner shape (3) works on` (the k3s half), `api egress by endpoint address` (the k3s half: section 4, does the `api service` line answer?), `apiServer.cidrs for k3s` (the `api server: ... endpoint <ip>:<port>` line at the top; k3s usually answers `<node ip>:6443`) and `ingress rule (5)` (the k3s half). Things to look at: k3s ships kube-router's policy controller, which has to be enabled (not `--disable-network-policy`); also check whether the node address falls inside one of the excepted ranges, which decides whether shape 3 blocks the API server there.

## Decision

```text
kind enforces policies:           yes
k3s enforces policies:            not measured yet (maintainer step 3)
runner shape (3) works on:        kind (internet yes, web pod yes, api blocked); k3s: not measured yet (maintainer step 3)
api egress by endpoint address:   works on kind (the service address kubernetes.default.svc passes when only <endpoint ip>/32:<endpoint port> is allowed, 172.18.0.2/32:6443 here); k3s: not measured yet (maintainer step 3)
chart default networkPolicy:      enabled: true      dummy values: enabled: true (kind enforces; the dummy must fill apiServer.cidrs from endpoints/kubernetes at install time)
apiServer.cidrs for k3s:          not measured yet (maintainer step 3); kind showed 172.18.0.2/32 port 6443
ingress rule (5):                 enforced on kind; k3s: not measured yet (maintainer step 3)
```
