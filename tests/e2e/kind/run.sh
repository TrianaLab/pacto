#!/usr/bin/env bash
# Real kind acceptance: build the operator image via the monorepo go.work Dockerfile,
# package the chart at release-state version, install into kind, prove a real
# reconcile-to-status transition (Compliant -> Unknown -> Compliant) using an inline
# contract, then chart upgrade + clean uninstall. Uses the packaged chart + real image
# (the same artifacts the release dry-run builds), not a dev deployment.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
CLUSTER="${KIND_CLUSTER:-pacto-mono}"
VER="$(node "$ROOT/release/scripts/build-release-plan.mjs" >/dev/null 2>&1; python3 -c 'import json;print(json.load(open("'"$ROOT"'/release/release-plan.json"))["groups"]["kubernetes"]["version"])')"
IMG="localhost:5001/pacto-operator/pacto-controller:${VER}"
echo "== build operator image (go.work Dockerfile) =="
docker build -f "$ROOT/integrations/kubernetes/Dockerfile" --build-arg VERSION="$VER" -t "$IMG" "$ROOT"
echo "== package chart =="; rm -rf /tmp/pacto-charts; helm package "$ROOT/integrations/kubernetes/charts/pacto-operator" -d /tmp/pacto-charts >/dev/null
CHART="$(ls /tmp/pacto-charts/pacto-operator-*.tgz)"
kind get clusters | grep -qx "$CLUSTER" || kind create cluster --name "$CLUSTER" --wait 60s
kind load docker-image "$IMG" --name "$CLUSTER"
export KUBECONFIG="$(mktemp)"; kind get kubeconfig --name "$CLUSTER" > "$KUBECONFIG"
echo "== install =="
helm install pacto-operator "$CHART" -n pacto-system --create-namespace \
  --set image.repository=localhost:5001/pacto-operator/pacto-controller --set image.tag="$VER" \
  --set image.pullPolicy=Never --set dashboard.enabled=false --wait --timeout 120s
kubectl get crds | grep -q pactos.pacto.trianalab.io
kubectl create namespace demo --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n demo create deployment orders --image=registry.k8s.io/pause:3.9 2>/dev/null || true
kubectl -n demo rollout status deployment/orders --timeout=60s
kubectl apply -f - >/dev/null <<YAML
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: {name: orders, namespace: demo}
spec:
  contractRef:
    inline: |
      pactoVersion: '2.0'
      service: {name: orders, version: 1.0.0, owner: {team: audit, dri: d, contacts: [{type: email, value: a@e.com, purpose: escalation}]}}
      workload: service
      state: {type: stateless, persistence: {scope: local, durability: ephemeral}, dataCriticality: low}
  target: {workloadRef: {name: orders, kind: Deployment}}
YAML
wait_status() { for i in $(seq 1 40); do s=$(kubectl -n demo get pacto orders -o jsonpath='{.status.contractStatus}' 2>/dev/null||true); [ "$s" = "$1" ] && { echo "  status=$s OK"; return 0; }; sleep 3; done; echo "  FAIL: wanted $1 got $s"; exit 1; }
echo "== A Compliant =="; wait_status Compliant
echo "== B break -> Unknown =="; kubectl -n demo delete deployment orders >/dev/null; kubectl -n demo annotate pacto orders x="$(date +%s)" --overwrite >/dev/null; wait_status Unknown
echo "== C recover -> Compliant =="; kubectl -n demo create deployment orders --image=registry.k8s.io/pause:3.9 >/dev/null; kubectl -n demo rollout status deployment/orders --timeout=60s; kubectl -n demo annotate pacto orders x="$(date +%s)r" --overwrite >/dev/null; wait_status Compliant
echo "== upgrade + uninstall =="; helm upgrade pacto-operator "$CHART" -n pacto-system --reuse-values --wait --timeout 90s >/dev/null; helm uninstall pacto-operator -n pacto-system --wait >/dev/null
echo "KIND E2E PASS"
