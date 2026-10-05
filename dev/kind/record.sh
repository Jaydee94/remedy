#!/bin/sh
# Records what the API server answers to the requests of the read tools, as the test data of internal/kube/kubetest.
# Needs the testbed (up.sh) and jq. Usage: dev/kind/record.sh [output directory of up.sh] [target directory]
# Names, labels and messages stay as they are; what only the cluster knows (uids, resource versions, managed fields,
# the cluster's own addresses) is made neutral so that a recording is the same whichever cluster it came from.
set -eu

OUT=${1:-$HOME/remedy-kind}
DIR=$(cd "$(dirname "$0")" && pwd)
TARGET=${2:-$DIR/../../internal/kube/kubetest/testdata}
. "$OUT/env.sh"
mkdir -p "$TARGET"

get() { # get <file> <path>: the Accept header is the one the client sends; the API server refuses text/plain for a log
  curl -sf --cacert "$REMEDY_K8S_CA_FILE" -H "Authorization: Bearer $(cat "$REMEDY_K8S_READ_TOKEN_FILE")" \
    -H "Accept: application/json" "$REMEDY_K8S_API$2" > "$TARGET/$1.raw" || { echo "failed: $2" >&2; exit 1; }
}

neutral='walk(if type == "object" then del(.uid, .resourceVersion, .selfLink, .managedFields, .generation, .ownerReferences[]?.uid, .systemUUID, .machineID, .bootID) else . end)'

# Lists of all namespaces keep only the namespaces of the testbed: the system namespaces are many kilobytes of noise.
ours='.items |= map(select(.metadata.namespace | IN("demo", "other")))'
# Daemonsets and statefulsets of the system namespaces stay: there are no others, and a test wants one of each kind.
system='.items |= map(select(.metadata.namespace | IN("demo", "other", "kube-system", "argocd")))'

json() { # json <file> <path> [jq filter applied after the neutralising]
  get "$1.json" "$2"
  jq -S "$neutral | ${3:-.}" "$TARGET/$1.json.raw" > "$TARGET/$1.json"
  rm -f "$TARGET/$1.json.raw"
  echo "recorded $1.json"
}

text() { # text <file> <path>
  get "$1" "$2"
  mv "$TARGET/$1.raw" "$TARGET/$1"
  echo "recorded $1"
}

json version /version
json deployments-all "/apis/apps/v1/deployments" "$ours"
json statefulsets-all "/apis/apps/v1/statefulsets" "$system"
json daemonsets-all "/apis/apps/v1/daemonsets" "$system"
json pods-all "/api/v1/pods" "$ours"
json nodes "/api/v1/nodes"
json events-demo "/api/v1/namespaces/demo/events"
json applications "/apis/argoproj.io/v1alpha1/namespaces/argocd/applications"

# The logs of two pods of the testbed. The one of crashy is the log of a container that has exited, and its previous log
# is not available: the API server answers 200 with a text that says so.
podof() { jq -r --arg w "$1" '.items[] | select(.metadata.namespace == "demo" and (.metadata.name | startswith($w + "-"))) | .metadata.name' "$TARGET/pods-all.json" | head -1; }
chatty=$(podof chatty)
crashy=$(podof crashy)
text pod-log-chatty "/api/v1/namespaces/demo/pods/$chatty/log?tailLines=6&container=chatty"
text pod-log-crashy "/api/v1/namespaces/demo/pods/$crashy/log?tailLines=20&container=crashy"
text pod-log-crashy-previous "/api/v1/namespaces/demo/pods/$crashy/log?tailLines=20&previous=true&container=crashy"
echo "pods used: chatty=$chatty crashy=$crashy"
