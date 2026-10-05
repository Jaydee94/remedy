#!/bin/sh
# Shows the RBAC limits of the two identities with curl, against the real API server of the testbed. Every line ends
# with the status that is expected. Usage: dev/kind/check-rbac.sh [output directory of up.sh]
set -eu
OUT=${1:-$HOME/remedy-kind}
. "$OUT/env.sh"
READ=$(cat "$REMEDY_K8S_READ_TOKEN_FILE")
WRITE=$(cat "$REMEDY_K8S_WRITE_TOKEN_FILE")
CHATTY=$(kubectl --context kind-remedy-dev -n demo get pods -l app=chatty -o name | cut -d/ -f2)

code() { # code <token> <method> <path> [json body]
  if [ $# -ge 4 ]; then
    curl -s -o /dev/null -w '%{http_code}' --cacert "$REMEDY_K8S_CA_FILE" -X "$2" -H "Authorization: Bearer $1" \
      -H 'Content-Type: application/merge-patch+json' -d "$4" "$REMEDY_K8S_API$3"
  else
    curl -s -o /dev/null -w '%{http_code}' --cacert "$REMEDY_K8S_CA_FILE" -X "$2" -H "Authorization: Bearer $1" "$REMEDY_K8S_API$3"
  fi
}
row() { printf '%-5s %-7s %-52s %s (expected %s)\n' "$1" "$2" "$3" "$4" "$5"; }

row read  GET    /api/v1/namespaces/demo/pods                   "$(code "$READ" GET /api/v1/namespaces/demo/pods)" 200
row read  GET    "pod log"                                      "$(code "$READ" GET "/api/v1/namespaces/demo/pods/$CHATTY/log?tailLines=1")" 200
row read  GET    /api/v1/nodes                                  "$(code "$READ" GET /api/v1/nodes)" 200
row read  GET    argocd/applications                            "$(code "$READ" GET /apis/argoproj.io/v1alpha1/namespaces/argocd/applications)" 200
row read  GET    /api/v1/namespaces/demo/secrets                "$(code "$READ" GET /api/v1/namespaces/demo/secrets)" 403
row read  GET    /api/v1/secrets                                "$(code "$READ" GET /api/v1/secrets)" 403
row read  GET    /api/v1/namespaces/demo/configmaps             "$(code "$READ" GET /api/v1/namespaces/demo/configmaps)" 403
row read  PATCH  deployments/web                                "$(code "$READ" PATCH /apis/apps/v1/namespaces/demo/deployments/web '{}')" 403
row read  DELETE pods/nope                                      "$(code "$READ" DELETE /api/v1/namespaces/demo/pods/nope)" 403
row write PATCH  demo/deployments/web                           "$(code "$WRITE" PATCH /apis/apps/v1/namespaces/demo/deployments/web '{}')" 200
row write PATCH  other/deployments/web                          "$(code "$WRITE" PATCH /apis/apps/v1/namespaces/other/deployments/web '{}')" 403
row write PATCH  kube-system/deployments/coredns                "$(code "$WRITE" PATCH /apis/apps/v1/namespaces/kube-system/deployments/coredns '{}')" 403
row write DELETE demo/pods/nope                                  "$(code "$WRITE" DELETE /api/v1/namespaces/demo/pods/nope)" 404
row write DELETE other/pods/nope                                "$(code "$WRITE" DELETE /api/v1/namespaces/other/pods/nope)" 403
row write PATCH  argocd/applications/guestbook                  "$(code "$WRITE" PATCH /apis/argoproj.io/v1alpha1/namespaces/argocd/applications/guestbook '{}')" 200
row write GET    /api/v1/namespaces/demo/pods                   "$(code "$WRITE" GET /api/v1/namespaces/demo/pods)" 403
row write GET    /api/v1/namespaces/demo/secrets                "$(code "$WRITE" GET /api/v1/namespaces/demo/secrets)" 403
