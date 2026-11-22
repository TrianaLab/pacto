#!/usr/bin/env bash
# Installation profiles on a real API server: hardened, hardened with idp and
# idp (#542).
#
# helm-unittest pins what each profile renders; only a cluster can answer
# `kubectl auth can-i`, run the operator on the namespaced grants hardened
# leaves it, read the cluster id through the kube-system Role, keep the
# delivery namespace Helm would otherwise delete and serve the dashboard's
# capability answer. Every Pacto here checks every 30s (the minimum).
#
# Every release is named pacto-operator by fullnameOverride so the diagnostics
# find their logs. The core CRDs are kept on uninstall, so each new release
# first adopts them (adopt_crds). The member+hardened leg belongs to 514-S3.
#   1. hardened_refuses: profile=hardened with no controller.watchNamespaces
#      fails with the UX text and leaves nothing of the release.
#   2. hardened: release pacto-hd in a namespace created first, watching hd-a,
#      with the in-cluster registry: no ClusterRole or binding, no admission
#      object, no managed dashboard, no pacto-viewer, no Namespace created, no
#      cluster-wide Secret read; the operator reads namespaces/kube-system only,
#      reconciles a Pacto in hd-a and advertises profile [hardened] with the
#      cluster id and a target snapshot push.
#   3. hardened_refuses_delivery: delivery.enabled=true fails and the release
#      revision stays.
#   4. hardened_idp: profile {hardened,idp} turns probing and metrics
#      observation on, still renders no ClusterRole or promoter, and the
#      detected profile reads [hardened, idp].
#   5. idp_to_hardened_keeps_delivery_ns: release pacto-kd on idp, then
#      {hardened,idp}: the delivery namespace and a trust ConfigMap in it
#      survive and NOTES say so.
#   6. hardened_gone: uninstalling pacto-hd takes the kube-system Role with it
#      and leaves nothing of the release.
#   7. idp: release pacto-idp cluster-wide: the promoter and the managed
#      dashboard run, and a pacto-viewer signed in reads installation profile
#      [idp], delivery on and no MCP endpoint from GET /api/capabilities.
set -euo pipefail
# shellcheck source=tests/acceptance/kind/lib.sh
source "$(dirname "$0")/lib.sh"
# shellcheck source=tests/acceptance/kind/delivery-lib.sh
source "$(dirname "$0")/delivery-lib.sh"

CLUSTER="${KIND_CLUSTER:-pacto-mono}"
HD=pacto-hd   # hardened release and its namespace (also the registry's)
KD=pacto-kd   # idp, then hardened with idp: the kept delivery namespace
IDP=pacto-idp # idp, cluster-wide
NS="$HD"      # read by install_registry, dump_diag, wait_managed_ready and pf
DNS=pacto-delivery
CHART="$PACTO_CHART"
SA=pacto-operator
DETECTED=pacto-operator-detected
REG_HOST="pacto-registry.${HD}.svc:5000"
LOCAL_DASH_PORT=18521
LOCAL_REG_PORT=18522
HARDENED_WATCH="profile hardened needs controller.watchNamespaces"
HARDENED_DELIVERY="profile hardened renders no ClusterRole and Pacto delivery needs cluster-wide roles: set delivery.enabled=false or leave profile hardened"
KEPT_NS="Pacto delivery is off. Namespace $DNS was kept with its trust config"
# The tags CI's delivery-images archive carries (delivery.sh), so the shard
# loads that archive instead of building.
VER="$(release_version kubernetes)"
OP_REPO="localhost:5001/pacto-operator/pacto-controller"
OP_IMG="${OP_REPO}:${VER}"
DL_REPO="localhost:5001/pacto-delivery"
DL_IMG="${DL_REPO}:${VER}"
DASH_IMG="localhost:5001/pacto-dashboard:$(release_version core)"

for t in jq helm; do command -v "$t" >/dev/null 2>&1 || fail "$t is required"; done

teardown() {
  local r
  for r in "$IDP" "$KD" "$HD"; do helm uninstall "$r" -n "$r" --wait >/dev/null 2>&1 || true; done
  kubectl delete clusterrole,clusterrolebinding pacto-dashboard --ignore-not-found >/dev/null 2>&1 || true
  kubectl delete ns "$HD" "$KD" "$IDP" "$DNS" hd-a kd-a --ignore-not-found --wait=false >/dev/null 2>&1 || true
}
# shellcheck disable=SC2154  # rc is assigned by rc=$? inside the trap body
trap 'rc=$?; [ $rc -ne 0 ] && dump_diag "$NS"; keep_or_teardown "$NS" "$CLUSTER" teardown; exit $rc' EXIT

section "create the cluster and load the images"
cluster_start_bg
load_built_images "$OP_IMG" "$DL_IMG" "$DASH_IMG" || build_images
ensure_cluster
infra "load the images into the cluster" load_images "$OP_IMG" "$DL_IMG" "$DASH_IMG"
teardown
for ns in "$HD" "$KD" "$IDP" "$DNS" hd-a kd-a; do
  eventually 40 sh -c "! kubectl get ns $ns >/dev/null 2>&1" || fail "namespace $ns from an earlier run never went away"
done
kubectl create ns "$HD" >/dev/null
kubectl create ns hd-a >/dev/null
kubectl create ns kd-a >/dev/null

SETS=(--set global.imageRegistry= --set image.repository="$OP_REPO" --set image.tag="$VER" --set image.pullPolicy=Never
      --set fullnameOverride=pacto-operator --set controller.stabilizationWindow=5s
      --set delivery.image.repository="$DL_REPO" --set delivery.image.tag="$VER" --set delivery.image.pullPolicy=Never
      --set "insecureRegistries[0]=$REG_HOST" --set "delivery.insecureRegistries[0]=$REG_HOST")

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
detected_profile() { # JSON_ARRAY — the operator advertises exactly that profile
  detected_json | jq -e --argjson want "$1" '.installation.profile == $want' >/dev/null 2>&1
}
op_args() { kubectl -n "$NS" get deploy pacto-operator -o json | jq -c '.spec.template.spec.containers[0].args'; }
revision() { helm status "$1" -n "$1" -o json | jq -r .version; }
release_leftovers() { # RELEASE — every object labelled with the release, in any namespace
  kubectl get all,role,rolebinding,clusterrole,clusterrolebinding,serviceaccount,configmap -A \
    -l "app.kubernetes.io/instance=$1" -o name 2>/dev/null || true
}
no_cluster_rbac() { # RELEASE — no ClusterRole or binding of the release, none named pacto*
  local left
  left="$(kubectl get clusterrole,clusterrolebinding -l "app.kubernetes.io/instance=$1" -o name)"
  [ -z "$left" ] || fail "release $1 rendered cluster-scoped RBAC: $left"
  [ "$(count_pacto_clusterroles)" = "0" ] || fail "ClusterRoles or bindings named pacto* exist: $(pacto_clusterroles)"
}
apply_pacto() { # NS NAME — an inline contract, nothing to pull
  kubectl apply -f - >/dev/null <<YAML
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: {name: $2, namespace: $1}
spec:
  checkIntervalSeconds: 30
  contractRef:
    inline: |
      pactoVersion: '2.0'
      service: {name: $2, version: 1.0.0, owner: {team: audit, dri: d, contacts: [{type: email, value: a@e.com, purpose: escalation}]}}
      workload: service
      state: {type: stateless, persistence: {scope: local, durability: ephemeral}, dataCriticality: low}
  target: {workloadRef: {name: $2, kind: Deployment}}
YAML
}
has_status() { [ -n "$(pacto_status "$1" "$2")" ]; }

section "SCENARIO 1 (hardened_refuses): hardened without a namespace list fails"
if out="$(helm install pacto-hr "$CHART" -n "$HD" "${SETS[@]}" --set profile=hardened 2>&1)"; then
  fail "helm install with profile=hardened and no controller.watchNamespaces succeeded"
fi
case "$out" in *"$HARDENED_WATCH"*) ;; *) echo "$out" >&2; fail "the refusal does not say: $HARDENED_WATCH" ;; esac
[ -z "$(helm list -n "$HD" -q --all)" ] || fail "helm kept a record of the refused release: $(helm list -n "$HD" --all)"
[ -z "$(release_leftovers pacto-hr)" ] || fail "the refused release left objects: $(release_leftovers pacto-hr)"
pass "helm install refuses: $HARDENED_WATCH, and nothing of pacto-hr exists"

section "SCENARIO 2 (hardened): a hardened install renders no cluster-scoped privilege"
install_registry
adopt_crds "$HD"
helm install "$HD" "$CHART" -n "$HD" "${SETS[@]}" --set profile=hardened \
  --set 'controller.watchNamespaces={hd-a}' --set registry="$REG_HOST/acme" --wait --timeout 180s >/dev/null
no_cluster_rbac "$HD"
pass "no ClusterRole or ClusterRoleBinding of $HD and none named pacto*"
adm="$(kubectl get validatingadmissionpolicies,validatingadmissionpolicybindings,validatingwebhookconfigurations,mutatingwebhookconfigurations \
  -l "app.kubernetes.io/instance=$HD" -o name)"
[ -z "$adm" ] || fail "release $HD rendered admission objects: $adm"
pass "no admission policy, binding or webhook configuration of $HD"
always 20 gone -n "$HD" deploy pacto-dashboard || fail "the hardened operator created deploy/pacto-dashboard"
gone -n "$HD" sa pacto-viewer || fail "the hardened install created sa/pacto-viewer"
for ns in pacto-node-agent "$DNS"; do gone ns "$ns" || fail "the hardened install created namespace $ns"; done
pass "no managed dashboard after 60s, no pacto-viewer and no namespace created by the chart"
OP_AS="system:serviceaccount:$HD:$SA"
[ "$(can_i list secrets "$OP_AS" --all-namespaces)" = "no" ] || fail "the hardened operator may list Secrets in every namespace"
[ "$(can_i create clusterroles "$OP_AS")" = "no" ] || fail "the hardened operator may create ClusterRoles"
[ "$(can_i get namespaces/kube-system "$OP_AS" -n kube-system)" = "yes" ] || fail "the hardened operator may not read namespaces/kube-system (the cluster id)"
[ "$(can_i get namespaces/default "$OP_AS" -n default)" = "no" ] || fail "the hardened operator may read namespaces/default"
pass "the operator reads namespaces/kube-system only and no Secret cluster-wide"
apply_pacto hd-a hd-watched
eventually 20 has_status hd-a hd-watched || fail "the Pacto in watched hd-a never got a status"
pass "the Pacto in hd-a reads $(pacto_status hd-a hd-watched)"
eventually 30 detected_profile '["hardened"]' || fail "the detected ConfigMap does not advertise profile [hardened]: $(detected_json | jq -c .installation)"
[ -n "$(detected_json | jq -r '.cluster // empty')" ] || fail "the detected ConfigMap names no cluster id"
# Detection refreshes every five minutes and its first round runs before the
# first push, so the push is read from the registry, not from lastPush.
CLUSTER_ID="$(detected_json | jq -r .cluster)"
REG_PF="$(pf "$LOCAL_REG_PORT" svc/pacto-registry 5000)"
pushed() { curl -fsS --max-time 5 "http://127.0.0.1:${LOCAL_REG_PORT}/v2/acme/pacto-targets/tags/list" | jq -e --arg t "cluster-$CLUSTER_ID" '.tags | index($t)' >/dev/null 2>&1; }
eventually 60 pushed || { kill "$REG_PF" 2>/dev/null || true; fail "the operator never pushed $REG_HOST/acme/pacto-targets:cluster-$CLUSTER_ID"; }
kill "$REG_PF" 2>/dev/null || true
pass "the operator advertises profile [hardened], cluster $CLUSTER_ID and pushed $REG_HOST/acme/pacto-targets:cluster-$CLUSTER_ID"

section "SCENARIO 3 (hardened_refuses_delivery): delivery under hardened fails"
before="$(revision "$HD")"
if out="$(helm upgrade "$HD" "$CHART" -n "$HD" --reuse-values --set delivery.enabled=true 2>&1)"; then
  fail "helm upgrade with delivery.enabled=true under profile hardened succeeded"
fi
case "$out" in *"$HARDENED_DELIVERY"*) ;; *) echo "$out" >&2; fail "the refusal does not say: $HARDENED_DELIVERY" ;; esac
[ "$(revision "$HD")" = "$before" ] || fail "the refused upgrade moved $HD from revision $before to $(revision "$HD")"
pass "helm upgrade refuses delivery under hardened and $HD stays at revision $before"

section "SCENARIO 4 (hardened_idp): hardened with idp turns observation on and stays namespaced"
helm upgrade "$HD" "$CHART" -n "$HD" --reuse-values --set 'profile={hardened,idp}' --wait --timeout 180s >/dev/null
argv="$(op_args)"
jq -e 'index("--enable-probing=on") and index("--enable-metrics-observation=on") and index("--profile=hardened,idp")' <<<"$argv" >/dev/null \
  || fail "the operator does not run with probing and metrics observation on and --profile=hardened,idp: $argv"
no_cluster_rbac "$HD"
[ -z "$(kubectl get deploy -A -l app.kubernetes.io/component=delivery-promoter -o name)" ] || fail "hardened with idp installed a delivery promoter"
pass "probing and metrics observation on, no ClusterRole and no promoter"
eventually 30 detected_profile '["hardened","idp"]' || fail "the detected profile never read [hardened, idp]: $(detected_json | jq -c .installation.profile)"
pass "the operator advertises profile [hardened, idp]"
kubectl -n hd-a delete pacto hd-watched --wait >/dev/null

section "SCENARIO 5 (idp_to_hardened_keeps_delivery_ns): dropping delivery keeps its namespace"
adopt_crds "$KD"
helm install "$KD" "$CHART" -n "$KD" --create-namespace "${SETS[@]}" --set profile=idp \
  --set 'controller.watchNamespaces={kd-a}' --wait --timeout 240s >/dev/null
kubectl -n "$DNS" create configmap e2e-trust --from-literal=trust.yaml='keys: []' >/dev/null
out="$(helm upgrade "$KD" "$CHART" -n "$KD" --reuse-values --set 'profile={hardened,idp}' --wait --timeout 180s)"
case "$out" in *"$KEPT_NS"*) ;; *) echo "$out" >&2; fail "the upgrade NOTES do not say: $KEPT_NS" ;; esac
kubectl get ns "$DNS" >/dev/null 2>&1 || fail "the upgrade to hardened deleted namespace $DNS"
kubectl -n "$DNS" get configmap e2e-trust >/dev/null 2>&1 || fail "the upgrade to hardened deleted the trust ConfigMap in $DNS"
pass "namespace $DNS and its trust ConfigMap survive the upgrade and NOTES say so"
helm uninstall "$KD" -n "$KD" --wait >/dev/null
assert_release_gone "$KD"
kubectl delete ns "$DNS" "$KD" --wait --timeout=120s >/dev/null

section "SCENARIO 6 (hardened_gone): uninstalling the hardened release leaves nothing"
CI_ROLE="pacto-operator-$HD-cluster-identity"
helm uninstall "$HD" -n "$HD" --wait >/dev/null
eventually 20 gone -n kube-system role "$CI_ROLE" || fail "role/$CI_ROLE in kube-system outlived $HD"
eventually 20 gone -n kube-system rolebinding "$CI_ROLE" || fail "rolebinding/$CI_ROLE in kube-system outlived $HD"
assert_release_gone "$HD"
eventually 20 gone -n "$HD" configmap "$DETECTED" || fail "configmap/$DETECTED outlived $HD by 60s"
pass "the kube-system Role and binding and the detected ConfigMap went with $HD"

section "SCENARIO 7 (idp): idp runs delivery and the dashboard says so"
NS="$IDP"
adopt_crds "$IDP"
helm install "$IDP" "$CHART" -n "$IDP" --create-namespace "${SETS[@]}" --set profile=idp \
  --set registry="$REG_HOST/acme" --wait --timeout 240s >/dev/null
kubectl -n "$DNS" rollout status deploy/pacto-operator-delivery --timeout=180s >/dev/null || fail "the promoter Deployment is not ready"
wait_managed_ready pacto-dashboard >/dev/null || fail "dashboard Deployment not Ready"
pass "the promoter and the managed dashboard are ready"
eventually 30 detected_profile '["idp"]' || fail "the detected profile never read [idp]: $(detected_json | jq -c .installation.profile)"
DASH_PF="$(pf "$LOCAL_DASH_PORT" svc/pacto-dashboard 3000)"
SESSION="$(dash_token_login "$LOCAL_DASH_PORT" "$(kubectl -n "$IDP" create token pacto-viewer --audience pacto-dashboard --duration 10m)" link)"
[[ -n "$SESSION" && "$SESSION" != *" "* ]] || { kill "$DASH_PF" 2>/dev/null || true; fail "the pacto-viewer token did not sign in: $SESSION"; }
installation() { curl -fsS -H "Cookie: __Host-pacto-session=$SESSION" "http://127.0.0.1:${LOCAL_DASH_PORT}/api/capabilities" | jq -c .installation; }
idp_answer() {
  installation | jq -e '.profile.value == ["idp"] and .delivery.enabled == true and .mcp.served == false' >/dev/null
}
eventually 40 idp_answer || {
  installation >&2 || true
  kill "$DASH_PF" 2>/dev/null || true
  fail "GET /api/capabilities does not read installation profile [idp], delivery on and no MCP endpoint"
}
kill "$DASH_PF" 2>/dev/null || true
pass "GET /api/capabilities reads installation profile [idp], delivery enabled and mcp served false"
helm uninstall "$IDP" -n "$IDP" --wait >/dev/null
helm_teardown "$IDP"
kubectl delete ns "$DNS" --wait=false >/dev/null 2>&1 || true

echo
echo "PROFILES E2E PASS"
