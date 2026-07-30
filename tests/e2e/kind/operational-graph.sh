#!/usr/bin/env bash
# Bring up the FULL Pacto operational-graph vertical in a local kind cluster so the
# whole product can be tested end to end in a browser: the operator, the dashboard,
# the Evidence Server and an in-cluster OCI registry — with real declared services
# (Pacto CRs the operator reconciles), a declared dependency edge, reconciled
# runtime targets, and a signed EvidenceEnvelope ingested from a "remote"
# environment as an external target. Everything a fully-configured install shows.
#
# Subcommands (driven by the Makefile aliases):
#   (default) / up   build + provision + assert, then keep or tear down
#   status           show component health for the running cluster
#   logs             dump component logs for the running cluster
#   down             tear the cluster/namespace down
#
# `make e2e-operational-graph` runs it and tears down unless KEEP_E2E_CLUSTER=1;
# `make e2e-operational-graph-up` sets KEEP so it stays up for manual testing.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
CLUSTER="${KIND_CLUSTER:-pacto-og}"
NS=pacto-system
DEMO_NS=demo
REG_HOST="pacto-registry.${NS}.svc.cluster.local:5000"
LOCAL_REG_PORT=5601
LOCAL_EV_PORT=8687
# shellcheck source=tests/e2e/kind/lib.sh
source "$(dirname "$0")/lib.sh"

use_existing_cluster() {
  kind get clusters | grep -qx "$CLUSTER" || { echo "no kind cluster '$CLUSTER' — run 'make e2e-operational-graph-up' first"; exit 1; }
  KUBECONFIG="$(mktemp)"; export KUBECONFIG; kind get kubeconfig --name "$CLUSTER" > "$KUBECONFIG"
}

CMD="${1:-run}"
case "$CMD" in
  status)
    use_existing_cluster
    echo "== pods =="; kubectl -n "$NS" get pods
    echo "== services =="; kubectl -n "$NS" get svc
    echo "== pvc =="; kubectl -n "$NS" get pvc
    echo "== reconciled Pacto CRs ($DEMO_NS) =="; kubectl -n "$DEMO_NS" get pacto -o wide 2>/dev/null || true
    for d in pacto-operator pacto-dashboard pacto-evidence pacto-registry; do
      kubectl -n "$NS" rollout status "deployment/$d" --timeout=5s >/dev/null 2>&1 && echo "  $d: Ready" || echo "  $d: not ready"
    done
    exit 0 ;;
  logs) use_existing_cluster; dump_diag "$NS"; exit 0 ;;
  down)
    if kind get clusters | grep -qx "$CLUSTER"; then kind delete cluster --name "$CLUSTER"; echo "cluster '$CLUSTER' deleted"; else echo "no cluster '$CLUSTER'"; fi
    exit 0 ;;
  run|up) : ;;
  browser) RUN_BROWSER=1 ;; # full bring-up + a Playwright run against the live dashboard
  *) echo "unknown subcommand: $CMD (use up|status|logs|down|browser)"; exit 2 ;;
esac

# shellcheck disable=SC2154  # rc is assigned by rc=$? inside the trap body
trap 'rc=$?; [ $rc -ne 0 ] && dump_diag "$NS"; pkill -f "kubectl.*port-forward" 2>/dev/null || true; exit $rc' EXIT

pass() { echo "  PASS: $1"; }
fail() { echo "  FAIL: $1"; exit 1; }
og_teardown() { kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true; }
pf() {
  local lport="$1" target="$2" rport="$3"
  kubectl -n "$NS" port-forward "$target" "${lport}:${rport}" >/dev/null 2>&1 &
  local pid=$!; sleep 2; echo "$pid"
}
wait_ready() { kubectl -n "$NS" rollout status "deployment/$1" --timeout="${2:-180s}"; }

node "$ROOT/release/scripts/build-release-plan.mjs" >/dev/null 2>&1
VER="$(python3 -c 'import json;print(json.load(open("'"$ROOT"'/release/release-plan.json"))["groups"]["kubernetes"]["version"])')"
CORE="$(python3 -c 'import json;print(json.load(open("'"$ROOT"'/release/release-plan.json"))["groups"]["core"]["version"])')"
OP_REPO="localhost:5001/pacto-operator/pacto-controller"
OP_IMG="${OP_REPO}:${VER}"
DASH_IMG="localhost:5001/pacto-dashboard:${CORE}"

echo "== build the operator + dashboard images =="
# --load forces the result into the local docker image store even when the default
# buildx builder uses the docker-container driver (otherwise the image lives only in
# buildkit's cache and `kind load docker-image` fails with "content digest not found").
docker build --load -f "$ROOT/Dockerfile" -t "$DASH_IMG" "$ROOT"
docker build --load -f "$ROOT/integrations/kubernetes/Dockerfile" \
  --build-arg VERSION="$VER" --build-arg DASHBOARD_IMAGE="$DASH_IMG" -t "$OP_IMG" "$ROOT"
docker pull registry:2 >/dev/null

echo "== package the operator chart =="
rm -rf /tmp/pacto-og-charts; mkdir -p /tmp/pacto-og-charts
helm package "$ROOT/integrations/kubernetes/charts/pacto-operator" -d /tmp/pacto-og-charts >/dev/null
CHART="$(ls /tmp/pacto-og-charts/pacto-operator-*.tgz)"

kind get clusters | grep -qx "$CLUSTER" || kind create cluster --name "$CLUSTER" --wait 90s
# Load images ONE AT A TIME: a combined `kind load docker-image A B C` streams a
# single ctr import that can fail with "content digest ... not found" when the
# images share base layers, so import each independently for robustness.
for img in "$DASH_IMG" "$OP_IMG" registry:2; do
  kind load docker-image "$img" --name "$CLUSTER"
done
KUBECONFIG="$(mktemp)"; export KUBECONFIG; kind get kubeconfig --name "$CLUSTER" > "$KUBECONFIG"
kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

echo "== an in-cluster OCI registry makes contract revisions resolvable =="
kubectl -n "$NS" apply -f - >/dev/null <<'YAML'
apiVersion: apps/v1
kind: Deployment
metadata: { name: pacto-registry, labels: { app: pacto-registry } }
spec:
  replicas: 1
  selector: { matchLabels: { app: pacto-registry } }
  template:
    metadata: { labels: { app: pacto-registry } }
    spec:
      containers:
      - { name: registry, image: registry:2, imagePullPolicy: Never, ports: [ { containerPort: 5000 } ] }
---
apiVersion: v1
kind: Service
metadata: { name: pacto-registry }
spec:
  selector: { app: pacto-registry }
  ports: [ { port: 5000, targetPort: 5000 } ]
YAML
wait_ready pacto-registry 120s

echo "== trust store: a producer keypair -> a Secret the Evidence Server mounts =="
PACTO_BIN="$(mktemp)"; go build -o "$PACTO_BIN" "$ROOT/cmd/pacto"
KEYDIR="$(mktemp -d)"; "$PACTO_BIN" evidence keygen --out "$KEYDIR" --key-id demo >/dev/null
kubectl -n "$NS" create secret generic pacto-evidence-trust --from-file=demo.pub="$KEYDIR/demo.pub" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null

echo "== push a contract revision to the in-cluster registry (over the forwarded port) =="
BDIR="$(mktemp -d)"
cat > "$BDIR/pacto.yaml" <<'YAML'
pactoVersion: "2.0"
service: { name: payments, version: "1.0.0" }
interfaces: [ { name: api, type: openapi, ref: openapi.yaml, visibility: public } ]
workload: service
state: { type: stateless, persistence: { scope: local, durability: ephemeral }, dataCriticality: low }
YAML
printf 'openapi: "3.0.0"\ninfo: { title: payments, version: "1.0.0" }\npaths: {}\n' > "$BDIR/openapi.yaml"
REG_PF="$(pf "$LOCAL_REG_PORT" svc/pacto-registry 5000)"
DIGEST="$(PACTO_INSECURE_REGISTRIES="127.0.0.1:${LOCAL_REG_PORT}" \
  "$PACTO_BIN" push "oci://127.0.0.1:${LOCAL_REG_PORT}/demo/payments:1.0.0" -p "$BDIR" 2>&1 | grep -oE 'sha256:[0-9a-f]{64}' | head -1)"
kill "$REG_PF" 2>/dev/null || true
[ -n "$DIGEST" ] && pass "pushed payments revision $DIGEST" || fail "could not push/resolve the contract digest"
CONTRACT_REF="oci://${REG_HOST}/demo/payments@${DIGEST}"

common_sets=(--set image.repository="$OP_REPO" --set image.tag="$VER" --set image.pullPolicy=Never
             --set dashboard.enabled=true --set evidence.enabled=true
             --set evidence.trust.existingSecret=pacto-evidence-trust)

echo "== install the operator with the dashboard + Evidence Server enabled =="
helm install pacto-operator "$CHART" -n "$NS" "${common_sets[@]}" --wait --timeout 240s
for _ in $(seq 1 40); do kubectl -n "$NS" rollout status deployment/pacto-evidence --timeout=10s >/dev/null 2>&1 && break; sleep 3; done
wait_ready pacto-dashboard
kubectl -n "$NS" set env deployment/pacto-evidence "PACTO_INSECURE_REGISTRIES=${REG_HOST}" >/dev/null
wait_ready pacto-evidence

echo "== declared services: two Pacto CRs the operator reconciles (checkout <- orders) =="
kubectl create namespace "$DEMO_NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
for svc in checkout orders; do
  kubectl -n "$DEMO_NS" create deployment "$svc" --image=registry.k8s.io/pause:3.9 >/dev/null 2>&1 || true
done
kubectl -n "$DEMO_NS" rollout status deployment/checkout --timeout=90s
kubectl -n "$DEMO_NS" rollout status deployment/orders --timeout=90s
kubectl apply -f - >/dev/null <<YAML
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: { name: checkout, namespace: ${DEMO_NS} }
spec:
  checkIntervalSeconds: 30
  contractRef:
    inline: |
      pactoVersion: '2.0'
      service: {name: checkout, version: 1.0.0, owner: {team: commerce, dri: d, contacts: [{type: email, value: a@e.com, purpose: escalation}]}}
      workload: service
      state: {type: stateless, persistence: {scope: local, durability: ephemeral}, dataCriticality: low}
  target: {workloadRef: {name: checkout, kind: Deployment}}
---
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: { name: orders, namespace: ${DEMO_NS} }
spec:
  checkIntervalSeconds: 30
  contractRef:
    inline: |
      pactoVersion: '2.0'
      service: {name: orders, version: 1.0.0, owner: {team: commerce, dri: d, contacts: [{type: email, value: a@e.com, purpose: escalation}]}}
      workload: service
      state: {type: stateless, persistence: {scope: local, durability: ephemeral}, dataCriticality: low}
      dependencies: [{name: checkout, ref: 'oci://${REG_HOST}/demo/checkout', required: false, compatibility: '^1.0.0'}]
  target: {workloadRef: {name: orders, kind: Deployment}}
YAML
wait_status() { for _ in $(seq 1 60); do [ "$(kubectl -n "$DEMO_NS" get pacto "$1" -o jsonpath='{.status.contractStatus}' 2>/dev/null||true)" = "$2" ] && return 0; sleep 3; done; return 1; }
wait_status checkout Compliant && pass "checkout reconciled Compliant" || fail "checkout did not reconcile"
wait_status orders Compliant && pass "orders reconciled Compliant" || fail "orders did not reconcile"

echo "== ingest a signed EvidenceEnvelope from a 'remote' environment (payments) =="
cat > /tmp/og-ev.json <<JSON
{
  "Subject": { "kind": "service", "name": "payments" },
  "ContractRef": "${CONTRACT_REF}",
  "Source": "remote-eu",
  "ObservedAt": "2026-07-29T12:00:00Z",
  "Observations": [
    { "kind": "WorkloadObserved", "subject": { "kind": "service", "name": "payments" },
      "outcome": "Observed", "value": { "type": "service" },
      "provenance": { "collector": "remote-eu", "detectedAt": "2026-07-29T12:00:00Z" } }
  ]
}
JSON
"$PACTO_BIN" evidence sign /tmp/og-ev.json --key "$KEYDIR/demo.key" --key-id demo --producer demo --sequence 1 --id og-1 > /tmp/og-env.json
EV_PF="$(pf "$LOCAL_EV_PORT" svc/pacto-evidence 8686)"
"$PACTO_BIN" evidence send /tmp/og-env.json --url "http://127.0.0.1:${LOCAL_EV_PORT}" >/dev/null 2>&1 \
  && pass "payments evidence accepted" || fail "payments evidence not accepted"
kill "$EV_PF" 2>/dev/null || true

echo "== the dashboard's operational graph shows the declared services AND the external evidence target =="
DASH_PF="$(pf 8080 svc/pacto-dashboard 3000)"; sleep 2
SNAP="$(curl -fsS http://127.0.0.1:8080/api/fleet/snapshot)"
echo "$SNAP" | grep -q checkout && echo "$SNAP" | grep -q orders && pass "declared services present in the graph" || fail "declared services missing"
echo "$SNAP" | grep -q payments && pass "external evidence target present in the graph" || fail "evidence target missing"
kill "$DASH_PF" 2>/dev/null || true

# Browser acceptance (§I): drive the LIVE dashboard in Chromium via Playwright,
# proving the real frontend bundle + real HTTP API + real operator data render
# together — not just that the JSON API answers. Opt-in (the `browser` subcommand)
# so the default vertical run stays dependency-light.
if [ -n "${RUN_BROWSER:-}" ]; then
  echo "== browser acceptance against the LIVE dashboard (Playwright/Chromium) =="
  BR_PF="$(pf 8080 svc/pacto-dashboard 3000)"; sleep 2
  ( cd "$ROOT/pkg/dashboard/frontend" \
    && npm ci --ignore-scripts >/dev/null 2>&1 \
    && npx playwright install --with-deps chromium >/dev/null 2>&1 \
    && PW_BASE_URL="http://127.0.0.1:8080/" npx playwright test --config playwright.live.config.ts ) \
    && pass "live dashboard browser acceptance" || fail "live dashboard browser acceptance failed"
  kill "$BR_PF" 2>/dev/null || true
fi

echo
echo "== the full operational-graph vertical is UP =="
if [ -n "${KEEP_E2E_CLUSTER:-}" ]; then
  cat <<EOF

  Everything is running in kind cluster '$CLUSTER' (namespace $NS). To use it:

    export KUBECONFIG=\$(mktemp) && kind get kubeconfig --name $CLUSTER > \$KUBECONFIG

    # Dashboard (Operational Graph + Impact):
    kubectl -n $NS port-forward svc/pacto-dashboard 8080:3000
    open http://localhost:8080/#/fleet

    # Evidence Server (source API):
    kubectl -n $NS port-forward svc/pacto-evidence 8686:8686
    curl -s localhost:8686/api/evidence/v1/targets | jq

  Inspect / tear down:
    make e2e-operational-graph-status
    make e2e-operational-graph-logs
    make e2e-operational-graph-down
EOF
fi
keep_or_teardown "$NS" "$CLUSTER" "og_teardown"
