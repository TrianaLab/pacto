#!/usr/bin/env bash
# Least-privilege acceptance: prove on a real API server that the default
# install reads no Secret cluster-wide and that a namespace-list install
# (controller.watchNamespaces) creates no ClusterRole, reconciles only the
# namespaces it watches and says its fleet is partial.
#
# Unit and helm-unittest cases pin the rendered RBAC; only a cluster can answer
# `kubectl auth can-i`, refuse a real Secret read, run the NOTES lookup on an
# upgrade and show which Pactos a scoped operator never touches. With the Secret
# watch gone a new grant is read on the next requeue, so every Pacto here checks
# every 30s (the minimum).
#
# Two releases, one after the other, both named pacto-operator by
# fullnameOverride so the diagnostics find their logs:
#   1. default install (release 1, dashboard on): the operator may not list
#      Secrets cluster-wide or read one in `default`, may read one in its own
#      namespace; a pull secret in `default` sets PullSecretNotGranted with the
#      cluster-wide hint (controller.secretGrants, never watchNamespaces).
#      The operator writes pacto-operator-detected, owned by its Deployment,
#      with kind as the cloud, and may list nodes.
#   2. upgrade with controller.secretGrants naming that Secret: the reason
#      leaves PullSecretNotGranted.
#   3. upgrade to controller.watchNamespaces={lp-a}: NOTES prints the delete
#      command for the old pacto-dashboard ClusterRole, the operator warns
#      (StaleClusterRBAC) or prunes it, after the documented delete no
#      ClusterRole named pacto* is left, and NOTES prints what Pacto detected.
#   4. uninstall release 1 and the documented cleanup: nothing of it is left,
#      the detected ConfigMap included (garbage collected with the Deployment).
#   5. fresh release 2 watching {lp-a,lp-b}: no ClusterRole or binding, a Pacto
#      in lp-a gets a status and one in lp-c never does, the dashboard reviews
#      locally and its overview is partial with the two namespaces named.
#      Detection still writes its ConfigMap, with cloud unknown because the
#      operator may not list nodes.
#   6. uninstall release 2: nothing of it is left, the detected ConfigMap included.
set -euo pipefail
# shellcheck source=tests/acceptance/kind/lib.sh
source "$(dirname "$0")/lib.sh"

CLUSTER="${KIND_CLUSTER:-pacto-mono}"
NS1=pacto-lp1
NS2=pacto-lp2
R1=pacto-lp1
R2=pacto-lp2
NS="$NS1" # read by dump_diag, wait_managed_ready and pf: the release under test
CHART="$PACTO_CHART"
TAG=e2e-least-privilege
OP_REPO="localhost:5001/pacto-operator/pacto-controller"
OP_IMG="${OP_REPO}:${TAG}"
DASH_IMG="localhost:5001/pacto-dashboard:${TAG}"
SA=pacto-operator
LOCAL_DASH_PORT=18511
STALE_DELETE="kubectl delete clusterrole,clusterrolebinding pacto-dashboard"

build_operator_images "$OP_IMG" "$DASH_IMG" "$TAG"

ensure_cluster
load_images "$DASH_IMG" "$OP_IMG"

teardown() {
  kubectl -n default delete pacto lp-pull --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl -n default delete secret missing-grant --ignore-not-found >/dev/null 2>&1 || true
  helm uninstall "$R1" -n "$NS1" --wait >/dev/null 2>&1 || true
  helm uninstall "$R2" -n "$NS2" --wait >/dev/null 2>&1 || true
  kubectl delete clusterrole,clusterrolebinding pacto-dashboard --ignore-not-found >/dev/null 2>&1 || true
  kubectl delete ns "$NS1" "$NS2" lp-a lp-b lp-c --ignore-not-found --wait=false >/dev/null 2>&1 || true
}
# shellcheck disable=SC2154  # rc is assigned by rc=$? inside the trap body
trap 'rc=$?; [ $rc -ne 0 ] && dump_diag "$NS"; keep_or_teardown "$NS" "$CLUSTER" teardown; exit $rc' EXIT
teardown
for ns in lp-a lp-b lp-c; do
  eventually 40 sh -c "! kubectl get ns $ns >/dev/null 2>&1" || fail "namespace $ns from an earlier run never went away"
  kubectl create ns "$ns" >/dev/null
done

SETS=(--set global.imageRegistry= --set image.repository="$OP_REPO" --set image.tag="$TAG" --set image.pullPolicy=Never
      --set fullnameOverride=pacto-operator --set controller.stabilizationWindow=5s)

can_i() { # VERB RESOURCE AS [kubectl args...] — prints yes or no
  local verb="$1" res="$2" as="$3"; shift 3
  kubectl auth can-i "$verb" "$res" --as="$as" "$@" 2>/dev/null || true
}

assert_operator_healthy() {
  kubectl -n "$NS" rollout status deployment/pacto-operator --timeout=150s >/dev/null || fail "operator Deployment not Available"
  local pods=()
  for _ in $(seq 1 30); do
    read -ra pods <<<"$(kubectl -n "$NS" get pods -l app.kubernetes.io/name=pacto-operator --field-selector=status.phase=Running -o jsonpath='{.items[*].metadata.name}')"
    [ "${#pods[@]}" -eq 1 ] && break
    sleep 2
  done
  [ "${#pods[@]}" -eq 1 ] || fail "expected exactly one Running operator pod, got ${#pods[@]}"
  local restarts
  restarts=$(kubectl -n "$NS" get pod "${pods[0]}" -o jsonpath='{.status.containerStatuses[0].restartCount}')
  [ "$restarts" = "0" ] || fail "operator pod restartCount=$restarts (want 0)"
  pass "operator Ready with 0 restarts"
}

condition() { # NS NAME FIELD — the ContractValid condition's reason or message
  kubectl -n "$1" get pacto "$2" -o jsonpath="{.status.conditions[?(@.type==\"ContractValid\")].$3}" 2>/dev/null || true
}
reason_is() { [ "$(condition "$1" "$2" reason)" = "$3" ]; }
reason_left() { local r; r="$(condition "$1" "$2" reason)"; [ -n "$r" ] && [ "$r" != "$3" ]; }
has_status() { [ -n "$(pacto_status "$1" "$2")" ]; }
no_status() { [ -z "$(pacto_status "$1" "$2")" ]; }
gone() { ! kubectl get "$@" >/dev/null 2>&1; }

apply_pacto() { # NS NAME [PULL_SECRET] — a pull secret makes it an OCI ref (fake: only the secret read matters)
  local ref
  if [ -n "${3:-}" ]; then
    ref="    oci: registry.invalid/least-privilege/$2:1.0.0
    pullSecretRef: $3"
  else
    ref="    inline: |
      pactoVersion: '2.0'
      service: {name: $2, version: 1.0.0, owner: {team: audit, dri: d, contacts: [{type: email, value: a@e.com, purpose: escalation}]}}
      workload: service
      state: {type: stateless, persistence: {scope: local, durability: ephemeral}, dataCriticality: low}"
  fi
  kubectl apply -f - >/dev/null <<YAML
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: {name: $2, namespace: $1}
spec:
  checkIntervalSeconds: 30
  contractRef:
$ref
  target: {workloadRef: {name: $2, kind: Deployment}}
YAML
}

DETECTED=pacto-operator-detected # fullnameOverride + "-detected", what the operator writes
SCOPED_NODES="with controller.watchNamespaces the operator may not list nodes"
detected_json() { kubectl -n "$NS" get configmap "$DETECTED" -o jsonpath='{.data.detected\.json}' 2>/dev/null; }
detected_written() { detected_json | jq -e '.entries | length > 0' >/dev/null 2>&1; }
cloud_entry() { detected_json | jq -c '.entries[] | select(.kind == "cloud")'; }

assert_detected() { # CLOUD_JQ — the ConfigMap exists, is the operator's and its cloud entry matches CLOUD_JQ
  eventually 30 detected_written || fail "configmap/$DETECTED never appeared in $NS with entries"
  local owner managed
  owner="$(kubectl -n "$NS" get configmap "$DETECTED" -o jsonpath='{.metadata.ownerReferences[*].kind}/{.metadata.ownerReferences[*].name}')"
  [ "$owner" = "Deployment/pacto-operator" ] || fail "configmap/$DETECTED is owned by '$owner', want Deployment/pacto-operator"
  managed="$(kubectl -n "$NS" get configmap "$DETECTED" -o jsonpath='{.metadata.labels.app\.kubernetes\.io/managed-by}')"
  [ "$managed" = "pacto-operator" ] || fail "configmap/$DETECTED is managed-by '$managed', want pacto-operator"
  detected_json | jq -e ".entries[] | select(.kind == \"cloud\") | $1" >/dev/null ||
    fail "the cloud entry does not match $1: $(cloud_entry)"
  pass "configmap/$DETECTED is the operator's and reads cloud $(cloud_entry)"
}

count_pacto_clusterroles() { kubectl get clusterrole,clusterrolebinding -o name | grep -c '/pacto' || true; }

assert_release_gone() { # RELEASE
  local left
  left="$(kubectl get role,rolebinding,clusterrole,clusterrolebinding -A -l "app.kubernetes.io/instance=$1" -o name)"
  [ -z "$left" ] || fail "release $1 left RBAC behind: $left"
  for kind in role rolebinding clusterrole clusterrolebinding; do
    left="$(kubectl get "$kind" -A --field-selector metadata.name=pacto-dashboard -o name)"
    [ -z "$left" ] || fail "a $kind named pacto-dashboard is left: $left"
  done
  [ "$(count_pacto_clusterroles)" = "0" ] || fail "ClusterRoles or bindings named pacto* are left: $(kubectl get clusterrole,clusterrolebinding -o name | grep '/pacto')"
  pass "release $1 left no Role, RoleBinding, ClusterRole or ClusterRoleBinding"
}

section "SCENARIO 1: the default install reads no Secret cluster-wide"
helm install "$R1" "$CHART" -n "$NS1" --create-namespace "${SETS[@]}" --wait --timeout 180s >/dev/null
assert_operator_healthy
OP_AS="system:serviceaccount:$NS1:$SA"
DASH_AS="system:serviceaccount:$NS1:pacto-dashboard"
[ "$(can_i list secrets "$OP_AS" --all-namespaces)" = "no" ] || fail "the operator may list Secrets in every namespace"
[ "$(can_i get secrets "$OP_AS" -n default)" = "no" ] || fail "the operator may read Secrets in default"
[ "$(can_i get secrets "$OP_AS" -n "$NS1")" = "yes" ] || fail "the operator may not read Secrets in its release namespace"
pass "the operator may read Secrets in $NS1 only"
assert_detected 'select(.value == "kind")'
[ "$(can_i list nodes "$OP_AS")" = "yes" ] || fail "the cluster-wide operator may not list nodes (detection reads providerID and kernels)"
pass "the operator may list nodes"
[ "$(can_i patch "configmaps/$DETECTED" "$OP_AS" -n "$NS1")" = "yes" ] || fail "the operator may not patch configmap/$DETECTED"
[ "$(can_i patch configmaps/other "$OP_AS" -n "$NS1")" = "no" ] || fail "the operator may patch other ConfigMaps in $NS1"
pass "the operator writes only configmap/$DETECTED in $NS1"
wait_managed_ready pacto-dashboard >/dev/null || fail "dashboard Deployment not Ready"
eventually 40 kubectl get clusterrole pacto-dashboard >/dev/null || fail "the cluster-wide dashboard never got its ClusterRole"
[ "$(can_i list secrets "$DASH_AS" --all-namespaces)" = "no" ] || fail "the dashboard may list Secrets in every namespace"
pass "the dashboard may not list Secrets in any namespace"
# Inferred services: the dashboard lists the four workload kinds, list only,
# and never reads pods or Secrets.
for res in deployments.apps statefulsets.apps daemonsets.apps cronjobs.batch; do
  [ "$(can_i list "$res" "$DASH_AS" --all-namespaces)" = "yes" ] || fail "the dashboard may not list $res in every namespace"
  for verb in get watch; do
    [ "$(can_i "$verb" "$res" "$DASH_AS" --all-namespaces)" = "no" ] || fail "the dashboard may $verb $res; inferred services need list only"
  done
done
for res in pods secrets; do
  [ "$(can_i get "$res" "$DASH_AS" --all-namespaces)" = "no" ] || fail "the dashboard may get $res"
done
pass "the dashboard may list workloads, never get or watch them, and reads no pods or Secrets"
apply_pacto default lp-pull missing-grant
eventually 20 reason_is default lp-pull PullSecretNotGranted || fail "lp-pull never reached PullSecretNotGranted (reason '$(condition default lp-pull reason)')"
[ "$(pacto_status default lp-pull)" = "Unknown" ] || fail "lp-pull contractStatus=$(pacto_status default lp-pull), want Unknown"
msg="$(condition default lp-pull message)"
case "$msg" in *controller.secretGrants*) ;; *) fail "the message names no controller.secretGrants: $msg" ;; esac
case "$msg" in *watchNamespaces*) fail "a cluster-wide install was told to set watchNamespaces: $msg" ;; esac
pass "ContractValid=Unknown PullSecretNotGranted: $msg"
assert_operator_healthy

section "SCENARIO 2: a named secret grant lets the operator read the pull secret"
kubectl -n default create secret generic missing-grant --from-literal=token=e2e >/dev/null
helm upgrade "$R1" "$CHART" -n "$NS1" "${SETS[@]}" --set 'controller.secretGrants={default/missing-grant}' --wait --timeout 180s >/dev/null
[ "$(can_i get secrets/missing-grant "$OP_AS" -n default)" = "yes" ] || fail "the grant does not let the operator read default/missing-grant"
[ "$(can_i get secrets/other "$OP_AS" -n default)" = "no" ] || fail "the grant lets the operator read other Secrets in default"
eventually 30 reason_left default lp-pull PullSecretNotGranted || fail "lp-pull still reads PullSecretNotGranted 90s after the grant"
pass "the reason left PullSecretNotGranted: $(condition default lp-pull reason)"
# Release 1 stops watching default next; nothing would reconcile this Pacto again.
kubectl -n default delete pacto lp-pull --wait >/dev/null
kubectl -n default delete secret missing-grant >/dev/null

section "SCENARIO 3: moving to a namespace list never leaves the old ClusterRole silently"
out="$(helm upgrade "$R1" "$CHART" -n "$NS1" "${SETS[@]}" --set 'controller.watchNamespaces={lp-a}' --wait --timeout 180s)"
case "$out" in *"$STALE_DELETE"*) pass "NOTES names the delete command" ;; *) echo "$out" >&2; fail "the upgrade NOTES do not name: $STALE_DELETE" ;; esac
assert_operator_healthy
stale_warned() {
  kubectl -n "$NS1" get events --field-selector reason=StaleClusterRBAC,type=Warning -o name 2>/dev/null | grep -q . ||
    gone clusterrole pacto-dashboard
}
eventually 40 stale_warned || fail "the operator neither warned StaleClusterRBAC nor pruned the pacto-dashboard ClusterRole"
pass "the operator warned StaleClusterRBAC or pruned the old ClusterRole"
$STALE_DELETE --ignore-not-found >/dev/null
[ "$(count_pacto_clusterroles)" = "0" ] || fail "after the documented delete these are left: $(kubectl get clusterrole,clusterrolebinding -o name | grep '/pacto')"
pass "no ClusterRole or binding named pacto* after the documented delete"
case "$out" in *"Pacto detected (inferred"*) pass "NOTES prints what Pacto detected" ;; *) echo "$out" >&2; fail "the upgrade NOTES do not print the detected block" ;; esac

section "SCENARIO 4: uninstalling release 1 leaves nothing behind"
helm uninstall "$R1" -n "$NS1" --wait >/dev/null
$STALE_DELETE --ignore-not-found >/dev/null
eventually 40 gone clusterrole pacto-dashboard || fail "clusterrole pacto-dashboard never went away"
assert_release_gone "$R1"
eventually 20 gone -n "$NS1" configmap "$DETECTED" || fail "configmap/$DETECTED outlived release $R1 by 60s"
pass "configmap/$DETECTED went with release $R1"

section "SCENARIO 5: a namespace-list install has no ClusterRole and reads only its namespaces"
NS="$NS2"
helm install "$R2" "$CHART" -n "$NS2" --create-namespace "${SETS[@]}" --set crds.install=false \
  --set 'controller.watchNamespaces={lp-a,lp-b}' --wait --timeout 180s >/dev/null
assert_operator_healthy
wait_managed_ready pacto-dashboard >/dev/null || fail "dashboard Deployment not Ready"
[ -z "$(kubectl get clusterrole,clusterrolebinding -l "app.kubernetes.io/instance=$R2" -o name)" ] || fail "release $R2 rendered a ClusterRole or binding"
gone clusterrole pacto-dashboard || fail "the scoped operator applied the pacto-dashboard ClusterRole"
[ "$(count_pacto_clusterroles)" = "0" ] || fail "ClusterRoles named pacto* exist: $(kubectl get clusterrole,clusterrolebinding -o name | grep '/pacto')"
pass "no ClusterRole or ClusterRoleBinding for $R2"
OP_AS="system:serviceaccount:$NS2:$SA"
[ "$(can_i list pactos.pacto.trianalab.io "$OP_AS" --all-namespaces)" = "no" ] || fail "the scoped operator may list Pactos in every namespace"
[ "$(can_i list secrets "$OP_AS" --all-namespaces)" = "no" ] || fail "the scoped operator may list Secrets in every namespace"
[ "$(can_i get secrets "$OP_AS" -n lp-a)" = "yes" ] || fail "the scoped operator may not read Secrets in lp-a"
[ "$(can_i get pactos.pacto.trianalab.io "$OP_AS" -n lp-c)" = "no" ] || fail "the scoped operator may read Pactos in lp-c"
pass "the scoped operator reads lp-a and lp-b only"
[ "$(can_i list nodes "$OP_AS")" = "no" ] || fail "the scoped operator may list nodes"
assert_detected "select(.completeness == \"unknown\" and .reason == \"$SCOPED_NODES\")"
apply_pacto lp-a lp-watched
apply_pacto lp-c lp-unwatched
eventually 20 has_status lp-a lp-watched || fail "the Pacto in watched lp-a never got a status"
pass "the Pacto in lp-a reads $(pacto_status lp-a lp-watched)"
always 20 no_status lp-c lp-unwatched || fail "the Pacto in unwatched lp-c got a status: $(pacto_status lp-c lp-unwatched)"
pass "the Pacto in lp-c has no status 60s later"
dash_env() { kubectl -n "$NS2" get deploy pacto-dashboard -o jsonpath="{.spec.template.spec.containers[0].env[?(@.name==\"$1\")].value}"; }
[ "$(dash_env PACTO_DASHBOARD_REVIEW)" = "local" ] || fail "the dashboard reviews '$(dash_env PACTO_DASHBOARD_REVIEW)', want local"
[ "$(dash_env PACTO_DASHBOARD_NAMESPACE)" = "lp-a,lp-b" ] || fail "the dashboard reads '$(dash_env PACTO_DASHBOARD_NAMESPACE)', want lp-a,lp-b"
pass "the dashboard reads lp-a,lp-b and reviews people locally"
DASH_PF="$(pf "$LOCAL_DASH_PORT" svc/pacto-dashboard 3000)"
overview_partial() {
  curl -fsS "http://127.0.0.1:${LOCAL_DASH_PORT}/api/fleet/overview" | jq -e '
    .meta.completeness == "partial" and
    ([.meta.sources[] | select(.kind == "kubernetes") | .namespaces] == [["lp-a", "lp-b"]])' >/dev/null
}
eventually 40 overview_partial || {
  curl -sS "http://127.0.0.1:${LOCAL_DASH_PORT}/api/fleet/overview" | jq '.meta' >&2 || true
  kill "$DASH_PF" 2>/dev/null || true
  fail "the dashboard overview is not partial with namespaces [lp-a, lp-b]"
}
kill "$DASH_PF" 2>/dev/null || true
pass "the dashboard overview is partial and names lp-a and lp-b"

section "SCENARIO 6: uninstalling release 2 leaves nothing behind"
kubectl -n lp-a delete pacto lp-watched --wait >/dev/null
kubectl -n lp-c delete pacto lp-unwatched --wait >/dev/null
helm uninstall "$R2" -n "$NS2" --wait >/dev/null
assert_release_gone "$R2"
eventually 20 gone -n "$NS2" configmap "$DETECTED" || fail "configmap/$DETECTED outlived release $R2 by 60s"
pass "configmap/$DETECTED went with release $R2"

echo
echo "LEAST-PRIVILEGE E2E PASS"
