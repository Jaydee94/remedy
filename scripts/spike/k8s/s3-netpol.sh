#!/bin/sh
# THROWAWAY (spike S3). Measures what NetworkPolicies do in the cluster of a kubectl context. It creates the
# namespace netpol-spike, three pods and some policies in it, and removes the namespace at the end. Nothing else.
# Usage: scripts/spike/k8s/s3-netpol.sh <kubectl context>
set -eu
CTX=${1:?usage: s3-netpol.sh <kubectl context>}
NS=netpol-spike
k() { kubectl --context "$CTX" -n "$NS" "$@"; }
trap 'kubectl --context "$CTX" delete namespace "$NS" --wait=false > /dev/null 2>&1 || true' EXIT

echo "context: $CTX"
kubectl --context "$CTX" get nodes -o wide | head -3
kubectl --context "$CTX" create namespace "$NS"

# The API server as pods see it: the service address and the endpoint behind it (what a rule has to name).
SVC_IP=$(kubectl --context "$CTX" -n default get svc kubernetes -o jsonpath='{.spec.clusterIP}')
EP_IP=$(kubectl --context "$CTX" -n default get endpoints kubernetes -o jsonpath='{.subsets[0].addresses[0].ip}')
EP_PORT=$(kubectl --context "$CTX" -n default get endpoints kubernetes -o jsonpath='{.subsets[0].ports[0].port}')
echo "api server: service $SVC_IP:443, endpoint $EP_IP:$EP_PORT"

cat <<EOF | k apply -f -
apiVersion: v1
kind: Pod
metadata: {name: web, labels: {app: web}}
spec:
  containers:
    - name: web
      image: busybox:1.37
      command: [sh, -c, "mkdir -p /www && echo ok > /www/index.html && httpd -f -p 8081 -h /www"]
---
apiVersion: v1
kind: Pod
metadata: {name: client, labels: {app: client}}
spec:
  containers:
    - name: client
      image: curlimages/curl:8.11.1
      command: [sleep, "3600"]
---
apiVersion: v1
kind: Pod
metadata: {name: other, labels: {app: other}}
spec:
  containers:
    - name: other
      image: curlimages/curl:8.11.1
      command: [sleep, "3600"]
EOF
k wait --for=condition=Ready pod/web pod/client pod/other --timeout=180s
WEB_IP=$(k get pod web -o jsonpath='{.status.podIP}')

# probe <what> <url>: prints the HTTP status the client got, or "blocked" when curl could not connect in 4 s.
probe() {
  code=$(k exec client -- curl -sk -m 4 -o /dev/null -w '%{http_code}' "$2" 2> /dev/null || true)
  case "$code" in 000|"") code=blocked ;; esac
  printf '  %-34s %s\n' "$1" "$code"
}
probe_all() {
  probe "web pod (private, 8081)" "http://$WEB_IP:8081/"
  probe "internet 443 (example.com)" "https://example.com/"
  probe "api service (kubernetes.default.svc)" "https://kubernetes.default.svc/version"
  probe "api endpoint ($EP_IP:$EP_PORT)" "https://$EP_IP:$EP_PORT/version"
  printf '  %-34s ' "dns (getent via nslookup)"; k exec client -- nslookup -timeout=3 example.com > /dev/null 2>&1 && echo ok || echo blocked
}

echo "== 1. no policy (the baseline: everything should be reachable; 401 or 403 from the api means reachable)"
probe_all

echo "== 2. default-deny egress for app=client (if these are still reachable, the CNI does not enforce policies)"
cat <<EOF | k apply -f -
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: deny-egress}
spec:
  podSelector: {matchLabels: {app: client}}
  policyTypes: [Egress]
EOF
sleep 5
probe_all

echo "== 3. the runner's shape: dns, the web pod, and 443 to the internet except private ranges"
k delete networkpolicy deny-egress
cat <<EOF | k apply -f -
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: runner-shape}
spec:
  podSelector: {matchLabels: {app: client}}
  policyTypes: [Egress]
  egress:
    - to:
        - namespaceSelector: {}
          podSelector: {matchLabels: {k8s-app: kube-dns}}
      ports: [{protocol: UDP, port: 53}, {protocol: TCP, port: 53}]
    - to:
        - podSelector: {matchLabels: {app: web}}
      ports: [{protocol: TCP, port: 8081}]
    - to:
        - ipBlock:
            cidr: 0.0.0.0/0
            except: [10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16]
      ports: [{protocol: TCP, port: 443}]
EOF
sleep 5
probe_all

echo "== 4. the server's shape: as 3, plus the api endpoint by address (does a rule on the endpoint let the service address through?)"
k delete networkpolicy runner-shape
cat <<EOF | k apply -f -
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: server-shape}
spec:
  podSelector: {matchLabels: {app: client}}
  policyTypes: [Egress]
  egress:
    - to:
        - namespaceSelector: {}
          podSelector: {matchLabels: {k8s-app: kube-dns}}
      ports: [{protocol: UDP, port: 53}, {protocol: TCP, port: 53}]
    - to: [{ipBlock: {cidr: $EP_IP/32}}]
      ports: [{protocol: TCP, port: $EP_PORT}]
EOF
sleep 5
probe_all

echo "== 5. ingress: only app=client may reach the web pod on 8081"
k delete networkpolicy server-shape
cat <<EOF | k apply -f -
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: web-ingress}
spec:
  podSelector: {matchLabels: {app: web}}
  policyTypes: [Ingress]
  ingress:
    - from: [{podSelector: {matchLabels: {app: client}}}]
      ports: [{protocol: TCP, port: 8081}]
EOF
sleep 5
printf '  %-34s ' "client -> web"; k exec client -- curl -s -m 4 -o /dev/null -w '%{http_code}\n' "http://$WEB_IP:8081/" || echo blocked
printf '  %-34s ' "other -> web (must be blocked)"; k exec other -- curl -s -m 4 -o /dev/null -w '%{http_code}\n' "http://$WEB_IP:8081/" 2> /dev/null || echo blocked
echo "== done; the namespace $NS is removed"
