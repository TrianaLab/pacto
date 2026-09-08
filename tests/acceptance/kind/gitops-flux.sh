#!/usr/bin/env bash
# Kind acceptance for the GitOps promotion gate: a violated Pacto contract stops
# a Flux promotion, and satisfying it lets the promotion through.
#
# The claim is not "the CR says NonCompliant" — reconcile.sh already proves that.
# It is that the verdict CHANGES WHAT SHIPS. Two Flux Kustomizations model the
# promotion: `orders-staging` carries a healthCheckExprs entry reading the Pacto
# verdict, and `orders-production` dependsOn staging. While the contract is
# violated, production's own manifest must not exist in the cluster; when the
# contract is satisfied, it must appear. The ConfigMap is the assertion — a
# condition can be argued about, an absent object cannot.
#
# The Kustomizations are applied VERBATIM from
# tests/acceptance/kind/fixtures/gitops/flux-kustomization.yaml, which is the same
# file published in integrations/kubernetes/docs/gitops.md. The documented snippet
# and the tested one cannot drift because they are one file.
#
# The operator runs at its DEFAULT stabilization window (two minutes), unlike
# every other scenario here. That is deliberate: the violation is a
# WORKLOAD_MISMATCH, the docs say a mismatch is NonCompliant on the first
# reconcile with no window to wait out, and a run that shortened the window would
# not be able to say so.
set -euo pipefail
CLUSTER="${KIND_CLUSTER:-pacto-gitops}"
NS=pacto-system
APP_NS=demo
REG_HOST="pacto-registry.${NS}.svc.cluster.local:5000"
LOCAL_REG_PORT=5601
# Pinned: healthCheckExprs needs kustomize-controller v1.5.0 or newer (flux2
# v2.5.0), and the gate under test is that field.
FLUX_VERSION=v2.9.5
# Flux's own artifact layer type. source-controller takes layers[0] whatever its
# media type, so this is documentation rather than a selector — but an artifact
# labelled as something else is one a real Flux user would never publish.
FLUX_LAYER_TYPE=application/vnd.cncf.flux.content.v1.tar+gzip
MANIFEST_REPO="demo/orders-manifests"
# shellcheck source=tests/acceptance/kind/lib.sh
source "$(dirname "$0")/lib.sh"

# Flux lives outside $NS, so dump_diag alone cannot explain a stuck gate: the
# reason a Kustomization is not Ready is in its conditions and in the two
# controllers' logs.
dump_flux() {
  echo "--- flux sources + kustomizations ---"
  kubectl -n flux-system get ocirepositories,kustomizations -o wide || true
  kubectl -n flux-system describe kustomizations || true
  echo "--- $APP_NS objects the promotion should or should not have created ---"
  kubectl -n "$APP_NS" get all,configmaps,pactos || true
  for d in source-controller kustomize-controller; do
    echo "--- logs deploy/$d (flux-system) ---"
    kubectl -n flux-system logs "deploy/$d" --tail=200 || true
  done
}
# shellcheck disable=SC2154  # rc is assigned by rc=$? inside the trap body
trap 'rc=$?; [ $rc -ne 0 ] && { dump_diag "$NS"; dump_flux; }; pkill -f "kubectl.*port-forward" 2>/dev/null || true; exit $rc' EXIT

command -v oras >/dev/null 2>&1 \
  || fail "oras is required: it publishes the manifest artifact Flux's OCIRepository pulls"

VER="$(release_version kubernetes)"
CORE="$(release_version core)"
OP_IMG="localhost:5001/pacto-operator/pacto-controller:${VER}"
OP_REPO="localhost:5001/pacto-operator/pacto-controller"
DASH_IMG="localhost:5001/pacto-dashboard:${CORE}"

build_operator_images "$OP_IMG" "$DASH_IMG" "$VER"

echo "== package the chart =="
CHART="$(package_chart "$PACTO_CHART")"

ensure_cluster
# The dashboard is off for this scenario, so only the operator image is loaded.
load_images "$OP_IMG"
kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
# $APP_NS is created OUTSIDE the Flux inventory on purpose: a namespace inside it
# would be pruned along with everything in it the moment a Kustomization failed,
# which would delete the very Pacto CR whose verdict is under test.
kubectl create namespace "$APP_NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

echo "== the operator, at its DEFAULT stabilization window =="
helm install pacto-operator "$CHART" -n "$NS" \
  --set image.repository="$OP_REPO" --set image.tag="$VER" --set image.pullPolicy=Never \
  --set dashboard.enabled=false --wait --timeout 240s
if kubectl -n "$NS" get deploy pacto-operator -o jsonpath='{.spec.template.spec.containers[0].args}' \
  | grep -q 'stabilization-window'; then
  fail "the chart set an explicit stabilization window; this scenario must run at the default"
fi
pass "operator installed with no --stabilization-window override"

echo "== an in-cluster OCI registry to publish the promotion's manifests to =="
install_registry
REG_PF_PID="$(pf "$LOCAL_REG_PORT" svc/pacto-registry 5000)"

echo "== Flux $FLUX_VERSION (source + kustomize controllers) =="
# --server-side: Flux's CRDs are far past the size a last-applied-configuration
# annotation can hold, and a client-side apply of them fails on exactly that.
kubectl apply --server-side -f "https://github.com/fluxcd/flux2/releases/download/${FLUX_VERSION}/install.yaml" >/dev/null
# Nothing here reconciles a HelmRelease or sends an alert; scaling the other two
# controllers to zero saves their image pulls and keeps the diagnostics readable.
kubectl -n flux-system scale deploy helm-controller notification-controller --replicas=0 >/dev/null 2>&1 || true
kubectl -n flux-system rollout status deploy/source-controller --timeout=240s
kubectl -n flux-system rollout status deploy/kustomize-controller --timeout=240s

# publish WORKLOAD TAG — the promotion's manifests, as the OCI artifact Flux
# pulls. WORKLOAD is what the contract DECLARES; the workload that actually ships
# is always a Deployment, so `job` is a contract that contradicts the cluster.
publish() {
  local workload="$1" tag="$2" tree tgz
  tree="$(mktemp -d)"; tgz="$(mktemp -t orders-manifests.XXXXXX)"
  mkdir -p "$tree/staging" "$tree/production"

  cat > "$tree/staging/kustomization.yaml" <<'YAML'
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - deployment.yaml
  - pacto.yaml
YAML
  cat > "$tree/staging/deployment.yaml" <<YAML
apiVersion: apps/v1
kind: Deployment
metadata: { name: orders, namespace: $APP_NS }
spec:
  replicas: 1
  selector: { matchLabels: { app: orders } }
  template:
    metadata: { labels: { app: orders } }
    spec:
      containers:
        - { name: app, image: registry.k8s.io/pause:3.9 }
YAML
  # workloadRef carries BOTH name and kind, which is what makes the mismatch
  # assertable: without an explicit kind the operator reports EVIDENCE_INSUFFICIENT
  # (Unknown) rather than WORKLOAD_MISMATCH, and the gate would never close.
  cat > "$tree/staging/pacto.yaml" <<YAML
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: { name: orders, namespace: $APP_NS }
spec:
  checkIntervalSeconds: 15
  contractRef:
    inline: |
      pactoVersion: '2.0'
      service: {name: orders, version: 1.0.0, owner: {team: audit, dri: d, contacts: [{type: email, value: a@e.com, purpose: escalation}]}}
      workload: $workload
      state: {type: stateless, persistence: {scope: local, durability: ephemeral}, dataCriticality: low}
  target: {workloadRef: {name: orders, kind: Deployment}}
YAML

  cat > "$tree/production/kustomization.yaml" <<'YAML'
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - configmap.yaml
YAML
  # The promotion's payload. It is a ConfigMap and not a workload because what is
  # being tested is whether production RAN AT ALL, and the cheapest object that
  # answers that is the honest one.
  cat > "$tree/production/configmap.yaml" <<YAML
apiVersion: v1
kind: ConfigMap
metadata: { name: orders-release, namespace: $APP_NS }
data: { promoted: "$tag" }
YAML

  tar -czf "$tgz" -C "$tree" staging production
  # --disable-path-validation: $tgz is absolute and ORAS refuses an absolute file
  # argument unless told the path is deliberate. It is; nothing here is user input.
  oras push --plain-http --disable-path-validation \
    "127.0.0.1:${LOCAL_REG_PORT}/${MANIFEST_REPO}:${tag}" "${tgz}:${FLUX_LAYER_TYPE}" >/dev/null
  echo "  published $tag (contract declares workload: $workload)"
}

echo "== publish v1: a contract that CONTRADICTS the workload that ships =="
publish job v1

kubectl apply -f - >/dev/null <<YAML
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata: { name: orders, namespace: flux-system }
spec:
  interval: 20s
  insecure: true
  url: oci://${REG_HOST}/${MANIFEST_REPO}
  ref: { tag: v1 }
YAML
oci_ready() {
  [ "$(kubectl -n flux-system get ocirepository orders -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}')" = "True" ]
}
eventually 30 oci_ready || fail "Flux never pulled the manifest artifact from the in-cluster registry"
pass "OCIRepository resolved v1"

echo "== apply the DOCUMENTED Kustomizations, verbatim =="
kubectl apply -f "$(dirname "$0")/fixtures/gitops/flux-kustomization.yaml" >/dev/null

# The gate is a CRD field, and an unknown CRD field is PRUNED without a word.
# Read it back: a fixture that no longer matches the installed kustomize-controller
# would otherwise "pass" every assertion below by never gating anything at all.
GATE_EXPR="$(kubectl -n flux-system get kustomization orders-staging \
  -o jsonpath='{.spec.healthCheckExprs[0].failed}')"
[ -n "$GATE_EXPR" ] \
  || fail "kustomize-controller pruned spec.healthCheckExprs — the documented gate does not exist on Flux $FLUX_VERSION"
pass "the gate survived admission: failed = $GATE_EXPR"

ready_status() { kubectl -n flux-system get kustomization "$1" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'; }
ready_reason() { kubectl -n flux-system get kustomization "$1" -o jsonpath='{.status.conditions[?(@.type=="Ready")].reason}'; }
ready_message() { kubectl -n flux-system get kustomization "$1" -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}'; }
promoted() { kubectl -n "$APP_NS" get configmap orders-release >/dev/null 2>&1; }

echo "== A the verdict arrives with no window to wait out =="
wait_pacto_status "$APP_NS" orders NonCompliant || fail "the violated contract never reached NonCompliant"
kubectl -n "$APP_NS" get pacto orders -o jsonpath='{range .status.findings[*]}{.code}{" "}{end}' \
  | grep -q WORKLOAD_MISMATCH || fail "NonCompliant, but not for the reason this scenario set up"
pass "WORKLOAD_MISMATCH at the DEFAULT two-minute window: a mismatch does not stabilize"

echo "== B staging fails, and fails BECAUSE of the contract =="
# The workload itself is healthy. If staging failed for any other reason the gate
# would look like it worked while proving nothing, so the Deployment is asserted
# first and the failure message has to name the Pacto.
kubectl -n "$APP_NS" rollout status deployment/orders --timeout=120s
staging_failed() { [ "$(ready_reason orders-staging)" = "HealthCheckFailed" ]; }
eventually 40 staging_failed || fail "staging never reported a failed health check: reason=$(ready_reason orders-staging)"
MSG="$(ready_message orders-staging)"
if ! grep -q "Pacto" <<<"$MSG" || ! grep -q "orders" <<<"$MSG"; then
  fail "staging failed, but not on the Pacto: $MSG"
fi
pass "staging Ready=False, HealthCheckFailed: $MSG"

echo "== C production does not run =="
gate_closed() {
  [ "$(ready_status orders-production)" != "True" ] \
    || { echo "  production went Ready while the contract was violated"; return 1; }
  ! promoted \
    || { echo "  the production manifest was applied while the contract was violated"; return 1; }
}
production_blocked() { [ "$(ready_reason orders-production)" = "DependencyNotReady" ]; }
eventually 20 production_blocked \
  || fail "production is blocked, but not by the gate: reason=$(ready_reason orders-production)"
# Held for 90s, three times production's 30s dependency requeue: a single check
# cannot distinguish "never promoted" from "promoted and rolled back".
always 30 gate_closed || fail "the promotion gate opened while the contract was violated"
pass "production held at DependencyNotReady for 90s and applied nothing"

echo "== D fix the contract and publish v2 =="
publish service v2
kubectl -n flux-system patch ocirepository orders --type merge -p '{"spec":{"ref":{"tag":"v2"}}}' >/dev/null

wait_pacto_status "$APP_NS" orders Compliant || fail "the corrected contract never reached Compliant"
staging_ready() { [ "$(ready_status orders-staging)" = "True" ]; }
production_ready() { [ "$(ready_status orders-production)" = "True" ]; }
eventually 60 staging_ready || fail "staging never recovered: reason=$(ready_reason orders-staging)"
eventually 60 production_ready || fail "production never promoted: reason=$(ready_reason orders-production)"
promoted || fail "production reports Ready but its manifest is not in the cluster"
[ "$(kubectl -n "$APP_NS" get configmap orders-release -o jsonpath='{.data.promoted}')" = "v2" ] \
  || fail "the promoted manifest is not the revision that satisfied the contract"
pass "the gate opened: production promoted v2"

kill "$REG_PF_PID" 2>/dev/null || true
echo "GITOPS FLUX PROMOTION GATE PASS"
keep_or_teardown "$NS" "$CLUSTER" delete_cluster
