#!/usr/bin/env bash
# Multi-cluster reads acceptance: one `pacto dashboard` started on the runner
# with --context reads two real kind clusters, and a signed-in reader sees only
# what each cluster's own RBAC lets them read.
#
# Unit tests pin the restriction, the per-cluster reviewers and the dedupe on
# fakes; only real API servers can answer a SubjectAccessReview, a TokenReview
# and a kube-system UID. No operator runs here: the dashboard reads the Pacto
# CRs (applied with no status) straight from each cluster.
#
#   cluster A ($CLUSTER)      namespaces mc-a (the reader's Role) and mc-b
#   cluster B ($CLUSTER_B)    namespace mc-a (the same name, no grant)
#
#   contexts: kind-A, an alias context of kind-A, kind-B
#
# Claims:
#   1. the alias context reaches cluster A again, so A is read once: two
#      Kubernetes sources, one per cluster;
#   2. sign-in runs against the first context: a ServiceAccount token of
#      cluster A signs in;
#   3. the reader sees mc-a of cluster A only: a grant on mc-a in A never shows
#      mc-a in B, and mc-b in A stays hidden;
#   4. cluster B is not-reviewable (a ServiceAccount is reviewed only in the
#      sign-in cluster), named by a CLUSTER_HIDDEN limitation, and the answer is
#      partial and answers 200, never an error.
set -euo pipefail
# shellcheck source=tests/acceptance/kind/lib.sh
source "$(dirname "$0")/lib.sh"

CLUSTER="${KIND_CLUSTER:-pacto-mono}"
CLUSTER_B="${CLUSTER}-mc-b"
NS=mc-a # read by dump_diag
PORT=18541
CRD="$PACTO_CHART/files/crds/pacto.trianalab.io_pactos.yaml"

cluster_start_bg
BIN="$(build_pacto)"
ensure_cluster
KC_A="$KUBECONFIG"
kind get clusters | grep -qx "$CLUSTER_B" || infra "create kind cluster $CLUSTER_B" \
  kind create cluster --name "$CLUSTER_B" --wait 90s
KC_B="$(mktemp)"
kind get kubeconfig --name "$CLUSTER_B" > "$KC_B"

# One kubeconfig holding both clusters plus an alias context of cluster A.
KUBECONFIG="$(mktemp)"; export KUBECONFIG
KUBECONFIG="$KC_A:$KC_B" kubectl config view --flatten > "$KUBECONFIG"
CTX_A="kind-$CLUSTER"
CTX_B="kind-$CLUSTER_B"
kubectl config set-context mc-alias --cluster="$CTX_A" --user="$CTX_A" >/dev/null
kubectl config use-context "$CTX_A" >/dev/null

DASH_PID=
teardown() {
  [ -n "$DASH_PID" ] && kill "$DASH_PID" 2>/dev/null
  kubectl --context "$CTX_A" delete ns mc-a mc-b --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kind delete cluster --name "$CLUSTER_B" >/dev/null 2>&1 || true
}
# shellcheck disable=SC2154  # rc is assigned by rc=$? inside the trap body
trap 'rc=$?; [ $rc -ne 0 ] && dump_diag "$NS"; keep_or_teardown "$NS" "$CLUSTER" teardown; [ -n "$DASH_PID" ] && kill "$DASH_PID" 2>/dev/null; exit $rc' EXIT

apply_pacto() { # CONTEXT NS NAME
  kubectl --context "$1" apply -f - >/dev/null <<YAML
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: {name: $3, namespace: $2}
spec:
  contractRef:
    inline: |
      pactoVersion: '2.0'
      service: {name: $3, version: 1.0.0, owner: {team: payments}}
  target: {workloadRef: {name: $3, kind: Deployment}}
YAML
}

section "fixtures: the Pacto CRD and Pactos in both clusters, a reader in cluster A"
for ctx in "$CTX_A" "$CTX_B"; do
  kubectl --context "$ctx" apply --server-side -f "$CRD" >/dev/null
  kubectl --context "$ctx" wait --for condition=established --timeout=60s crd/pactos.pacto.trianalab.io >/dev/null
done
kubectl --context "$CTX_A" create ns mc-a --dry-run=client -o yaml | kubectl --context "$CTX_A" apply -f - >/dev/null
kubectl --context "$CTX_A" create ns mc-b --dry-run=client -o yaml | kubectl --context "$CTX_A" apply -f - >/dev/null
kubectl --context "$CTX_B" create ns mc-a --dry-run=client -o yaml | kubectl --context "$CTX_B" apply -f - >/dev/null
apply_pacto "$CTX_A" mc-a payments
apply_pacto "$CTX_A" mc-b ledger
apply_pacto "$CTX_B" mc-a payments
kubectl --context "$CTX_A" -n mc-a create serviceaccount reader --dry-run=client -o yaml | kubectl --context "$CTX_A" apply -f - >/dev/null
kubectl --context "$CTX_A" -n mc-a create role pacto-reader --verb=get,list,watch --resource=pactos.pacto.trianalab.io \
  --dry-run=client -o yaml | kubectl --context "$CTX_A" apply -f - >/dev/null
kubectl --context "$CTX_A" -n mc-a create rolebinding pacto-reader --role=pacto-reader --serviceaccount=mc-a:reader \
  --dry-run=client -o yaml | kubectl --context "$CTX_A" apply -f - >/dev/null
UID_A="$(kubectl --context "$CTX_A" get ns kube-system -o jsonpath='{.metadata.uid}')"
UID_B="$(kubectl --context "$CTX_B" get ns kube-system -o jsonpath='{.metadata.uid}')"
[ -n "$UID_A" ] && [ -n "$UID_B" ] && [ "$UID_A" != "$UID_B" ] || fail "the two clusters do not report two kube-system UIDs: '$UID_A' '$UID_B'"
pass "cluster A is $UID_A, cluster B is $UID_B"

section "the dashboard reads three contexts on two clusters"
if curl -sS -o /dev/null --max-time 2 "http://127.0.0.1:$PORT/" 2>/dev/null; then
  fail "127.0.0.1:$PORT already answers before the dashboard started"
fi
WORK="$(mktemp -d)"
LOG="$WORK/dashboard.log"
( cd "$WORK" && exec "$BIN" dashboard --no-cache --port "$PORT" --token-login \
    --context "$CTX_A" --context mc-alias --context "$CTX_B" ) >"$LOG" 2>&1 &
DASH_PID=$!
up() { curl -sS -o /dev/null --max-time 3 "http://127.0.0.1:$PORT/" 2>/dev/null; }
eventually 40 up || { cat "$LOG" >&2; fail "the dashboard never answered on 127.0.0.1:$PORT"; }
pass "the dashboard answers"

SESSION="$(dash_token_login "$PORT" "$(kubectl --context "$CTX_A" -n mc-a create token reader --audience pacto-dashboard --duration 10m)" link)"
[[ -n "$SESSION" && "$SESSION" != *" "* ]] || { cat "$LOG" >&2; fail "a ServiceAccount token of cluster A (the first context) did not sign in: $SESSION"; }
pass "a ServiceAccount token of cluster A signs in: sign-in runs against the first context"

targets() {
  curl -sS -o "$WORK/targets.json" -w '%{http_code}' -H "Cookie: __Host-pacto-session=$SESSION" \
    "http://127.0.0.1:$PORT/api/fleet/entities?kinds=target"
}
want_view() {
  [ "$(targets)" = 200 ] || return 1
  jq -e --arg a "$UID_A" --arg b "$UID_B" '
    ([.items[] | .key] == [$a + "/mc-a/kubernetes/payments"]) and
    .meta.completeness == "partial" and
    ([.meta.sources[] | select(.kind == "kubernetes")] | length) == 2 and
    ([.meta.clusters[] | select(.id == $b) | .access] == ["not-reviewable"]) and
    ([.meta.limitations[] | select(.code == "CLUSTER_HIDDEN" and .source == $b)] | length) == 1
  ' "$WORK/targets.json" >/dev/null
}
eventually 40 want_view || {
  echo "--- last answer (HTTP $(targets)) ---" >&2
  jq '{items: [.items[]?.key], meta: {completeness: .meta.completeness, clusters: .meta.clusters, sources: [.meta.sources[]? | {id, kind, status}], limitations: .meta.limitations}}' "$WORK/targets.json" >&2 || cat "$WORK/targets.json" >&2
  tail -n 50 "$LOG" >&2
  fail "the reader's view is not mc-a of cluster A alone with cluster B hidden as not-reviewable"
}
pass "the alias context is read once: one Kubernetes source per cluster"
pass "the reader sees mc-a of cluster A only; mc-a of cluster B and mc-b of cluster A stay hidden"
pass "cluster B is not-reviewable, named by CLUSTER_HIDDEN, and the answer is partial with HTTP 200"

echo
echo "MULTI-CLUSTER E2E PASS"
