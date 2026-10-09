# S3: what NetworkPolicies do on kind and on k3s

Spike S3 of `docs/plans/k8s-0-spikes.md`. It feeds plan K-2 (the chart's policy defaults, `networkPolicy.apiServer.cidrs`) and K-4 (whether the dummy enables the policy). The spec behind the question is `docs/specs/2026-10-06-kubernetes-deployment-design.md` (D11, section 5, section 9).

**Status:** the kind half is measured (2026-10-07). The k3s half is a maintainer step and **not measured yet**; see "k3s: to be filled in" below.

## What was measured

`scripts/spike/k8s/s3-netpol.sh <context>` creates the namespace `netpol-spike` with three pods (`web`: busybox httpd on 8081; `client` and `other`: curl) and applies seven policies one after the other, always on `app=client` except section 5:

1. no policy (baseline)
2. default-deny egress
3. the runner's shape: DNS, the `web` pod on 8081, and `0.0.0.0/0` except `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `169.254.0.0/16` on TCP 443
4. the control plane's shape: DNS plus `ipBlock <api endpoint address>/32` on the endpoint port (no service address, no 443 to the internet)
5. ingress on `web`: only `app=client` on 8081
6. as 3, but the public range (except the private ranges) is also open on the endpoint port (6443). Added after review: in 3 the API server is blocked by address and by port at once (the rule allows only 443, the endpoint is on 6443), so 3 alone cannot say which one blocks it. 6 removes the port as a cause.
7. control: `0.0.0.0/0` without an except list on 443 and 6443. The API must be reachable here, which shows the probe can succeed once address and port both match (so the blocks in 3 and 6 are real).

At the end the script deletes the namespace with `--wait` and checks with `kubectl get namespace` that it is gone (it prints a warning with the manual command if not).

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

Sections 6 and 7 and the end state come from a second run of the extended script on a fresh `remedy-spike` cluster (same node address, same sections 1 to 5 output as above):

```text
== 6. as 3, but also TCP 6443 to the public range (except private ranges)
  web pod (private, 8081)            200
  internet 443 (example.com)         200
  api service (kubernetes.default.svc) blocked
  api endpoint (172.18.0.2:6443)     blocked
  dns (getent via nslookup)          ok
== 7. control: TCP 443 and 6443 to 0.0.0.0/0 without an except list
  web pod (private, 8081)            blocked
  internet 443 (example.com)         200
  api service (kubernetes.default.svc) 200
  api endpoint (172.18.0.2:6443)     200
  dns (getent via nslookup)          ok
== done; the namespace netpol-spike is removed (kubectl get namespace: NotFound)
```

Clean-up of the kind run: after the script, `kubectl --context kind-remedy-spike get ns netpol-spike` answered `NotFound`; then `kind delete cluster --name remedy-spike` and `kind get clusters` printed `No kind clusters found.`

### Reading

- **kind enforces policies.** Section 1 is fully reachable, section 2 is blocked everywhere including DNS. kind's default CNI in this version therefore enforces `NetworkPolicy` (which CNI it is was not checked); the assumption that it might not was wrong for this version. The dummy can run with the policies on.
- **The runner's shape (3) does what D11 wants on kind:** DNS, the in-cluster `web` pod and the internet on 443 work; the API server is blocked by both its addresses. In 3 alone this is confounded: the rule allows only TCP 443 and the endpoint is on 6443 (the service address is translated to `172.18.0.2:6443` too), so the port alone would block both probes. **Section 6 separates the two:** with 6443 also allowed to the public range except the private ranges, the API is still blocked, so on kind the `except` list (the node `172.18.0.2` is inside the excepted `172.16.0.0/12`) does block the API server by address. **Section 7** (no except list, 6443 allowed) answers 200, so the probe works and the blocks in 3 and 6 are the policy, not a broken probe. Both causes are therefore real on kind: by port in 3, by address in 6.
- **Consequence for K-2.** What keeps the runner from the API server depends on where the API is: (a) API on 6443 (kind, k3s default): a runner rule that allows only 443 to the public range blocks it by port, whatever the node address; (b) API on 443 with a public address: the `except` list does not protect it, the runner could reach it (the chart notes should say so; this was not measured, it follows from section 7); (c) typical homelab node addresses (`192.168.0.0/16`, `10.0.0.0/8`) are in the excepted ranges, so the address blocks it anyway.
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

It creates the namespace `netpol-spike` with three tiny pods, applies and deletes seven policies in it, deletes the namespace with `--wait` and checks that it is gone (the last line says so, or prints a warning with the command to remove it by hand). The output is pasted here under this heading. It fills these Decision lines: `k3s enforces policies` (section 2 blocked everywhere = yes), `runner shape (3) works on` (the k3s half), `api egress by endpoint address` (the k3s half: section 4, does the `api service` line answer?), `apiServer.cidrs for k3s` (the `api server: ... endpoint <ip>:<port>` line at the top; k3s usually answers `<node ip>:6443`) and `ingress rule (5)` (the k3s half). Things to look at:
- k3s ships kube-router's policy controller, which has to be enabled (not `--disable-network-policy`).
- The port of the API endpoint (top line). In section 3 the API is blocked by port when it is not 443, so a blocked `api` line there says nothing about the address. Read section 6 (6443 or whatever the endpoint port is, allowed to the public range except the private ranges): if the API is still blocked there, the address is in an excepted range and the `except` list protects it; if it answers there, the node address is outside the excepted ranges and only the port keeps the runner away. Section 7 is the control and must answer for the API.
- Whether the node address falls inside one of the excepted ranges (192.168.0.0/16 and 10.0.0.0/8 are the usual homelab ranges).

## Decision

```text
kind enforces policies:           yes
k3s enforces policies:            not measured yet (maintainer step 3)
runner shape (3) works on:        kind (internet yes, web pod yes, api blocked; blocked by port in 3 and by address in 6, both measured); k3s: not measured yet (maintainer step 3)
api egress by endpoint address:   works on kind (the service address kubernetes.default.svc passes when only <endpoint ip>/32:<endpoint port> is allowed, 172.18.0.2/32:6443 here); k3s: not measured yet (maintainer step 3)
chart default networkPolicy:      enabled: true      dummy values: enabled: true (kind enforces; the dummy must fill apiServer.cidrs from endpoints/kubernetes at install time)
apiServer.cidrs for k3s:          not measured yet (maintainer step 3); kind showed 172.18.0.2/32 port 6443
ingress rule (5):                 enforced on kind; k3s: not measured yet (maintainer step 3)
```
