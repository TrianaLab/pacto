#!/usr/bin/env bash
# Kind acceptance for the operator-managed Evidence Server. It proves the
# CLUSTER-specific behavior that unit tests and the cluster-free acceptance
# cannot: the operator reconciles a SEPARATE Evidence Server Deployment, an
# internal Service and a retained PVC when evidence.enabled=true in the existing
# pacto-operator chart (no second chart); readiness is gated on storage
# recovery; the managed dashboard is auto-wired to the Evidence Server; and the
# PVC — hence the persistent evidence — survives disabling the component. The
# ingestion, replay, restart-recovery and replay-after-restart behavior is
# proven end to end by the cluster-free acceptance (tests/e2e/fleet-graph.sh,
# run in the engine leg) against the same durable store this Deployment runs.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
CLUSTER="${KIND_CLUSTER:-pacto-evidence}"
NS=pacto-system
node "$ROOT/release/scripts/build-release-plan.mjs" >/dev/null 2>&1
VER="$(python3 -c 'import json;print(json.load(open("'"$ROOT"'/release/release-plan.json"))["groups"]["kubernetes"]["version"])')"
CORE="$(python3 -c 'import json;print(json.load(open("'"$ROOT"'/release/release-plan.json"))["groups"]["core"]["version"])')"
OP_IMG="localhost:5001/pacto-operator/pacto-controller:${VER}"
OP_REPO="localhost:5001/pacto-operator/pacto-controller"
DASH_IMG="localhost:5001/pacto-dashboard:${CORE}"

pass() { echo "  PASS: $1"; }
fail() { echo "  FAIL: $1"; exit 1; }

echo "== build dashboard + operator images =="
docker build -f "$ROOT/Dockerfile" -t "$DASH_IMG" "$ROOT"
docker build -f "$ROOT/integrations/kubernetes/Dockerfile" \
  --build-arg VERSION="$VER" --build-arg DASHBOARD_IMAGE="$DASH_IMG" -t "$OP_IMG" "$ROOT"

echo "== package the chart =="
rm -rf /tmp/pacto-evidence-charts; mkdir -p /tmp/pacto-evidence-charts
helm package "$ROOT/integrations/kubernetes/charts/pacto-operator" -d /tmp/pacto-evidence-charts >/dev/null
CHART="$(ls /tmp/pacto-evidence-charts/pacto-operator-*.tgz)"

kind get clusters | grep -qx "$CLUSTER" || kind create cluster --name "$CLUSTER" --wait 90s
kind load docker-image "$DASH_IMG" "$OP_IMG" --name "$CLUSTER"
export KUBECONFIG="$(mktemp)"; kind get kubeconfig --name "$CLUSTER" > "$KUBECONFIG"

echo "== trust store: a producer keypair -> a Secret the Evidence Server mounts =="
PACTO_BIN="$(mktemp)"
go build -o "$PACTO_BIN" "$ROOT/cmd/pacto"
KEYDIR="$(mktemp -d)"
"$PACTO_BIN" evidence keygen --out "$KEYDIR" --key-id demo >/dev/null
kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n "$NS" create secret generic pacto-evidence-trust --from-file=demo.pub="$KEYDIR/demo.pub" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null

common_sets=(--set image.repository="$OP_REPO" --set image.tag="$VER" --set image.pullPolicy=Never
             --set dashboard.enabled=true
             --set evidence.enabled=true
             --set evidence.trust.existingSecret=pacto-evidence-trust)

echo "== install the operator with the Evidence Server enabled =="
helm install pacto-operator "$CHART" -n "$NS" "${common_sets[@]}" --wait --timeout 240s

echo "== the operator reconciles a managed Evidence Server Deployment (readiness gated on recovery) =="
kubectl -n "$NS" rollout status deployment/pacto-evidence --timeout=180s \
  && pass "evidence Deployment is Ready (storage recovered)" \
  || fail "evidence Deployment did not become Ready"
kubectl -n "$NS" get svc pacto-evidence >/dev/null \
  && pass "internal Evidence Service exists" || fail "internal Evidence Service missing"
kubectl -n "$NS" get pvc pacto-evidence-data >/dev/null \
  && pass "evidence PVC provisioned" || fail "evidence PVC missing"

echo "== the Evidence container runs 'pacto evidence serve' with one replica =="
replicas="$(kubectl -n "$NS" get deploy pacto-evidence -o jsonpath='{.spec.replicas}')"
[ "$replicas" = "1" ] && pass "single writer (replicas=1)" || fail "expected 1 replica, got $replicas"
kubectl -n "$NS" get deploy pacto-evidence -o jsonpath='{.spec.template.spec.containers[0].args}' | grep -q 'serve' \
  && pass "container runs the evidence server" || fail "evidence serve args missing"

echo "== the managed dashboard is auto-wired to the Evidence Server =="
kubectl -n "$NS" get deploy pacto-dashboard -o jsonpath='{.spec.template.spec.containers[0].env[*].name}' \
  | grep -q PACTO_EVIDENCE_SOURCE_URL \
  && pass "dashboard has PACTO_EVIDENCE_SOURCE_URL" || fail "dashboard not wired to the Evidence Server"

echo "== disabling the Evidence Server removes runtime resources but RETAINS the PVC =="
helm upgrade pacto-operator "$CHART" -n "$NS" "${common_sets[@]}" --set evidence.enabled=false --wait --timeout 180s
sleep 5
kubectl -n "$NS" get deploy pacto-evidence -o name 2>/dev/null | grep -q . \
  && fail "evidence Deployment survived disable" || pass "evidence Deployment removed on disable"
kubectl -n "$NS" get pvc pacto-evidence-data >/dev/null 2>&1 \
  && pass "evidence PVC RETAINED after disable (persistent evidence preserved)" \
  || fail "evidence PVC was deleted on disable — persistent evidence lost"

echo "== re-enabling recovers against the retained PVC =="
helm upgrade pacto-operator "$CHART" -n "$NS" "${common_sets[@]}" --wait --timeout 180s
kubectl -n "$NS" rollout status deployment/pacto-evidence --timeout=180s \
  && pass "evidence Deployment recovered against the retained PVC" \
  || fail "evidence Deployment did not recover after re-enable"

echo "== evidence disabled must not break the operator or dashboard =="
helm upgrade pacto-operator "$CHART" -n "$NS" "${common_sets[@]}" --set evidence.enabled=false --wait --timeout 180s
kubectl -n "$NS" rollout status deployment/pacto-operator --timeout=120s \
  && pass "operator healthy with evidence disabled" || fail "operator unhealthy with evidence disabled"
kubectl -n "$NS" rollout status deployment/pacto-dashboard --timeout=120s \
  && pass "dashboard healthy with evidence disabled (source unconfigured, not unavailable)" \
  || fail "dashboard unhealthy with evidence disabled"

echo "== operator-managed Evidence Server acceptance PASSED =="
