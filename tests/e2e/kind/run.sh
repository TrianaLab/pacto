#!/usr/bin/env bash
# Real kind acceptance — Pacto's formal verification against a live cluster
# (item 9). Builds the operator + dashboard images via the monorepo Dockerfiles
# (the exact production contexts), then: install a PREVIOUS chart fixture ->
# upgrade to the newly built chart (rolling the operator) -> assert the dashboard
# deployment path -> prove RBAC -> drive a real reconcile cycle Compliant ->
# Unknown (EVIDENCE_MISSING) -> Compliant -> assert evaluation coverage reaches
# the CR status -> uninstall and prove runtime resources are cleaned. Runs with
# the dashboard ENABLED (the chart default) and a short stabilization window so
# transitions are deterministic (the operator default window is 2m). Uses the
# packaged chart + real images the release simulation builds, not a dev deployment.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
CLUSTER="${KIND_CLUSTER:-pacto-mono}"
NS=pacto-system
# shellcheck source=tests/e2e/kind/lib.sh
source "$(dirname "$0")/lib.sh"
# On failure, dump cluster diagnostics before exiting; never mask the real code.
# This scenario reuses/leaves its kind cluster (see KEEP_E2E_CLUSTER in lib.sh).
# shellcheck disable=SC2154  # rc is assigned by rc=$? inside the trap body
trap 'rc=$?; [ $rc -ne 0 ] && dump_diag "$NS"; exit $rc' EXIT
node "$ROOT/release/scripts/build-release-plan.mjs" >/dev/null 2>&1
VER="$(python3 -c 'import json;print(json.load(open("'"$ROOT"'/release/release-plan.json"))["groups"]["kubernetes"]["version"])')"
CORE="$(python3 -c 'import json;print(json.load(open("'"$ROOT"'/release/release-plan.json"))["groups"]["core"]["version"])')"
OP_IMG="localhost:5001/pacto-operator/pacto-controller:${VER}"
OP_REPO="localhost:5001/pacto-operator/pacto-controller"
DASH_IMG="localhost:5001/pacto-dashboard:${CORE}"

echo "== build dashboard image (root Dockerfile) =="
docker build -f "$ROOT/Dockerfile" -t "$DASH_IMG" "$ROOT"
echo "== build operator image (production root context) coupled to the dashboard image =="
docker build -f "$ROOT/integrations/kubernetes/Dockerfile" \
  --build-arg VERSION="$VER" --build-arg DASHBOARD_IMAGE="$DASH_IMG" -t "$OP_IMG" "$ROOT"

echo "== package the new chart + a previous-release fixture =="
rm -rf /tmp/pacto-charts; mkdir -p /tmp/pacto-charts
helm package "$ROOT/integrations/kubernetes/charts/pacto-operator" -d /tmp/pacto-charts >/dev/null
NEW_CHART="$(ls /tmp/pacto-charts/pacto-operator-*.tgz | grep -v prev)"
# Faithful previous-release fixture: the same chart at an earlier version. The
# image is the same local build (a cross-major public-chart in-cluster upgrade is
# impractical since helm never upgrades crds/; that path is covered by the release
# dry-run + envtest). The upgrade still exercises a version bump + arg change +
# rolling restart.
helm package "$ROOT/integrations/kubernetes/charts/pacto-operator" --version 0.0.0-prev --app-version "$VER" -d /tmp/pacto-charts >/dev/null
PREV_CHART="/tmp/pacto-charts/pacto-operator-0.0.0-prev.tgz"

kind get clusters | grep -qx "$CLUSTER" || kind create cluster --name "$CLUSTER" --wait 90s
kind load docker-image "$DASH_IMG" "$OP_IMG" --name "$CLUSTER"
export KUBECONFIG="$(mktemp)"; kind get kubeconfig --name "$CLUSTER" > "$KUBECONFIG"

# Operator image loaded into kind (Never). The dashboard image the operator
# deploys is its coupled DASHBOARD_IMAGE (also kind-loaded); its tag is a real
# version so the pod's default pullPolicy is IfNotPresent -> uses the loaded image.
common_sets=(--set image.repository="$OP_REPO" --set image.tag="$VER" --set image.pullPolicy=Never
             --set dashboard.enabled=true)

echo "== install the PREVIOUS fixture =="
helm install pacto-operator "$PREV_CHART" -n "$NS" --create-namespace "${common_sets[@]}" --wait --timeout 180s
kubectl get crds | grep -q pactos.pacto.trianalab.io && echo "  CRDs installed"

echo "== UPGRADE to the newly built chart (short window, rolling restart) =="
helm upgrade pacto-operator "$NEW_CHART" -n "$NS" "${common_sets[@]}" \
  --set controller.stabilizationWindow=5s --wait --timeout 180s
kubectl -n "$NS" rollout status deployment/pacto-operator --timeout=120s
kubectl -n "$NS" get deploy pacto-operator -o jsonpath='{.spec.template.spec.containers[0].args}' | grep -q 'stabilization-window=5s' \
  && echo "  upgrade applied: --stabilization-window=5s present"

echo "== dashboard deployment path (enabled) =="
for _ in $(seq 1 40); do kubectl -n "$NS" get deploy pacto-dashboard >/dev/null 2>&1 && break; sleep 3; done
kubectl -n "$NS" rollout status deployment/pacto-dashboard --timeout=120s && echo "  dashboard deployment ready"

echo "== RBAC: the operator ServiceAccount may perform every read its collector needs =="
for verb_res in "get:deployments" "list:deployments" "get:statefulsets" "get:jobs" \
                "get:configmaps" "get:secrets" "get:services" "get:endpointslices.discovery.k8s.io"; do
  v="${verb_res%:*}"; r="${verb_res#*:}"
  kubectl auth can-i "$v" "$r" --as=system:serviceaccount:${NS}:pacto-operator -n demo | grep -qx yes \
    && echo "  can-i $v $r: yes" || { echo "  RBAC FAIL: operator cannot $v $r"; exit 1; }
done

kubectl create namespace demo --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n demo create deployment orders --image=registry.k8s.io/pause:3.9 >/dev/null 2>&1 || true
kubectl -n demo rollout status deployment/orders --timeout=90s
kubectl apply -f - >/dev/null <<YAML
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: {name: orders, namespace: demo}
spec:
  checkIntervalSeconds: 30
  contractRef:
    inline: |
      pactoVersion: '2.0'
      service: {name: orders, version: 1.0.0, owner: {team: audit, dri: d, contacts: [{type: email, value: a@e.com, purpose: escalation}]}}
      workload: service
      state: {type: stateless, persistence: {scope: local, durability: ephemeral}, dataCriticality: low}
  target: {workloadRef: {name: orders, kind: Deployment}}
YAML
wait_status() { for i in $(seq 1 60); do s=$(kubectl -n demo get pacto orders -o jsonpath='{.status.contractStatus}' 2>/dev/null||true); [ "$s" = "$1" ] && { echo "  status=$s OK"; return 0; }; sleep 3; done; echo "  FAIL: wanted $1 got '$s'"; kubectl -n demo get pacto orders -o yaml | tail -30; exit 1; }
echo "== A Compliant (workload observed + matches) =="; wait_status Compliant
echo "== B target deleted -> Unknown (EVIDENCE_MISSING, sustained past the 5s window) =="
kubectl -n demo delete deployment orders --wait >/dev/null
wait_status Unknown
kubectl -n demo get pacto orders -o jsonpath='{range .status.findings[*]}{.code}{" "}{end}' | grep -q EVIDENCE_MISSING \
  && echo "  finding EVIDENCE_MISSING present"
echo "== C target recreated -> Compliant (recovery) =="
kubectl -n demo create deployment orders --image=registry.k8s.io/pause:3.9 >/dev/null
kubectl -n demo rollout status deployment/orders --timeout=90s
wait_status Compliant

echo "== evaluation coverage reaches the CR status =="
cov="$(kubectl -n demo get pacto orders -o jsonpath='{.status.evaluationCoverage}' 2>/dev/null)"
[ -n "$cov" ] && echo "  evaluationCoverage: $cov" || { echo "  FAIL: no evaluationCoverage in status"; exit 1; }

echo "== live Kubernetes fleet source reflects the reconciled CR =="
# The operational graph's live k8s source reads Pacto CRs straight from the
# cluster this run reconciled, with no separate reporting step. KUBECONFIG is
# already exported above, so `pacto fleet --k8s` uses this kind cluster.
PACTO_BIN="$(mktemp)"
go build -o "$PACTO_BIN" "$ROOT/cmd/pacto"
FLEET_JSON="$("$PACTO_BIN" fleet search --k8s --namespace demo --output-format json 2>/tmp/fleet-k8s.err)" \
  || { echo "  FAIL: pacto fleet --k8s errored"; cat /tmp/fleet-k8s.err; exit 1; }
grep -q '"name": *"orders"' <<<"$FLEET_JSON" \
  && echo "  live k8s source shows orders in the graph" \
  || { echo "  FAIL: orders not in the k8s-backed fleet"; echo "$FLEET_JSON"; exit 1; }

echo "== uninstall cleans runtime resources =="
helm uninstall pacto-operator -n "$NS" --wait >/dev/null
sleep 3
for d in pacto-operator pacto-dashboard; do
  kubectl -n "$NS" get deploy "$d" -o name 2>/dev/null | grep -q . && { echo "  FAIL: $d survived uninstall"; exit 1; }
done
echo "  operator + dashboard runtime resources removed"

echo "KIND E2E PASS"
