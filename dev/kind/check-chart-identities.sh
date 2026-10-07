#!/bin/sh
# Shows what the three accounts the chart creates may and may not do, with `kubectl auth can-i --as`. Needs a cluster the
# chart is installed in, and cluster-admin rights to impersonate. It changes nothing. Argo CD's Application CRD must exist
# (kubectl cannot resolve an unknown resource type and answers "no", which would make every Argo CD line meaningless).
# Usage: dev/kind/check-chart-identities.sh <kubectl context> <release namespace> <write namespace> [argocd namespace]
set -eu
CTX=${1:?usage: check-chart-identities.sh <context> <release namespace> <write namespace> [argocd namespace]}
NS=${2:?}
WNS=${3:?}
ARGO=${4:-argocd}
fail=0

kubectl --context "$CTX" get crd applications.argoproj.io > /dev/null 2>&1 \
  || { echo "the CRD applications.argoproj.io is missing: install Argo CD (or only its CRD) first" >&2; exit 2; }

# can <expected yes|no> <as> <what>... : runs `kubectl auth can-i` and compares.
can() {
  want=$1; as=$2; shift 2
  got=$(kubectl --context "$CTX" auth can-i "$@" --as="system:serviceaccount:$NS:$as" 2> /dev/null || true)
  if [ "$got" = "$want" ]; then
    printf 'ok    %-22s %-3s %s\n' "$as" "$got" "$*"
  else
    printf 'WRONG %-22s %-3s (wanted %s) %s\n' "$as" "$got" "$want" "$*"
    fail=1
  fi
}

echo "-- remedy-read: get and list what the read tools show, nothing else"
can yes remedy-read list pods --all-namespaces
can yes remedy-read get pods --subresource=log -n "$WNS"
can yes remedy-read list deployments.apps -n "$WNS"
can yes remedy-read list applications.argoproj.io -n "$ARGO"
can no  remedy-read get secrets -n "$WNS"
can no  remedy-read get secrets -n "$NS"
can no  remedy-read list configmaps -n "$WNS"
can no  remedy-read delete pods -n "$WNS"
can no  remedy-read patch deployments.apps -n "$WNS"
can no  remedy-read patch applications.argoproj.io -n "$ARGO"

echo "-- remedy-write: restart, delete a pod, patch an application, and read nothing"
can yes remedy-write patch deployments.apps -n "$WNS"
can yes remedy-write delete pods -n "$WNS"
can yes remedy-write patch applications.argoproj.io -n "$ARGO"
can no  remedy-write get pods -n "$WNS"
can no  remedy-write get secrets -n "$WNS"
can no  remedy-write delete pods -n kube-system
can no  remedy-write patch deployments.apps -n kube-system
can no  remedy-write patch deployments.apps -n "$NS"
can no  remedy-write delete pods -n "$NS"

echo "-- remedy-token-refresher: mint one account's token, patch one Secret, nothing else"
can yes remedy-token-refresher create serviceaccounts/remedy-write --subresource=token -n "$NS"
can yes remedy-token-refresher patch secrets/remedy-write-token -n "$NS"
can no  remedy-token-refresher create serviceaccounts/remedy-read --subresource=token -n "$NS"
can no  remedy-token-refresher patch secrets/remedy-secrets -n "$NS"
can no  remedy-token-refresher get secrets/remedy-write-token -n "$NS"
can no  remedy-token-refresher create secrets -n "$NS"
can no  remedy-token-refresher list secrets -n "$NS"

echo "-- remedy-server (no cluster) or remedy-runner: no rights at all"
for who in remedy-runner; do
  can no "$who" list pods --all-namespaces
  can no "$who" get secrets -n "$NS"
done

[ "$fail" -eq 0 ] && echo "all as expected" || { echo "some answers are not as expected" >&2; exit 1; }
