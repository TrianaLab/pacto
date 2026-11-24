#!/usr/bin/env bash
# Helpers the profile scenarios share (profiles.sh, idp.sh). Sourced, never
# executed directly. They read the caller's globals at call time: NS, DETECTED,
# CLUSTER_ID, LOCAL_REG_PORT (the forwarded in-cluster registry, which holds
# acme/pacto-targets), SESSION and LOCAL_DASH_PORT (a signed-in dashboard).
#
#   adopt_crds RELEASE   hand the kept core CRDs to RELEASE
#   gone KUBECTL_ARGS... the object does not exist
#   detected_json        the detected ConfigMap's detected.json
#   op_args              the operator container's arguments, as a JSON array
#   pushed               the operator pushed acme/pacto-targets:cluster-<id>
#   installation         GET /api/capabilities .installation

# adopt_crds RELEASE — the CRDs are kept on uninstall (helm.sh/resource-policy:
# keep) and carry the release that created them; hand them to RELEASE so its
# install adopts them instead of refusing them.
adopt_crds() {
  local crds
  crds="$(kubectl get crd -o name | grep 'pacto\.trianalab\.io$' || true)"
  [ -z "$crds" ] && return 0
  # shellcheck disable=SC2086  # one argument per CRD
  kubectl annotate --overwrite $crds "meta.helm.sh/release-name=$1" "meta.helm.sh/release-namespace=$1" >/dev/null
}

gone() { ! kubectl get "$@" >/dev/null 2>&1; }
detected_json() { kubectl -n "$NS" get configmap "$DETECTED" -o jsonpath='{.data.detected\.json}' 2>/dev/null; }
op_args() { kubectl -n "$NS" get deploy pacto-operator -o json | jq -c '.spec.template.spec.containers[0].args'; }
pushed() { curl -fsS --max-time 5 "http://127.0.0.1:${LOCAL_REG_PORT}/v2/acme/pacto-targets/tags/list" | jq -e --arg t "cluster-$CLUSTER_ID" '.tags | index($t)' >/dev/null 2>&1; }
installation() { curl -fsS -H "Cookie: __Host-pacto-session=$SESSION" "http://127.0.0.1:${LOCAL_DASH_PORT}/api/capabilities" | jq -c .installation; }
