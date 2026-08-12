#!/usr/bin/env bash
# Bring up the FULL Pacto operational-graph vertical in a local kind cluster: the
# operator, the dashboard, the Evidence Server and an in-cluster OCI registry —
# with real published contract revisions, real declared services (Pacto CRs the
# operator reconciles from those revisions), a declared dependency edge, an
# operator-managed observation source carrying the matching observed edge,
# reconciled runtime targets, and a signed EvidenceEnvelope ingested from a
# "remote" environment as an external target.
#
# This script is THIN ORCHESTRATION only: it builds images, publishes bundles,
# installs the chart, applies CRs and forwards ports. Every semantic claim about
# the result is made elsewhere, against the real Product API —
# `tests/e2e/kind/productready` (Go) proves the fixture is ready and emits the
# discovered keys, and the `browser` subcommand then runs the live Product
# journeys in `pkg/dashboard/frontend/e2e-live/` against the same dashboard.
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

echo "== publish the fixture's contract revisions to the in-cluster registry =="
# Everything the Product reasons about downstream is REAL published content, not a
# synthesized shortcut: the external-evidence subject (payments), TWO checkout
# revisions that differ by exactly one deterministic semantic change, and an orders
# revision that DECLARES the checkout dependency. The refs the cluster later stores
# are the in-cluster ones; only this push hop goes through a forwarded port.
BDIR="$(mktemp -d)"; mkdir -p "$BDIR"/{payments,checkout-a,checkout-b,orders}

cat > "$BDIR/payments/pacto.yaml" <<'YAML'
pactoVersion: "2.0"
service: { name: payments, version: "1.0.0" }
interfaces: [ { name: api, type: openapi, ref: openapi.yaml, visibility: public } ]
workload: service
state: { type: stateless, persistence: { scope: local, durability: ephemeral }, dataCriticality: low }
YAML
printf 'openapi: "3.0.0"\ninfo: { title: payments, version: "1.0.0" }\npaths: {}\n' > "$BDIR/payments/openapi.yaml"

cat > "$BDIR/checkout-a/pacto.yaml" <<'YAML'
pactoVersion: "2.0"
service: { name: checkout, version: "1.0.0", owner: { team: commerce, dri: d, contacts: [ { type: email, value: a@e.com, purpose: escalation } ] } }
interfaces: [ { name: api, type: openapi, ref: openapi.yaml, visibility: public } ]
workload: service
state: { type: stateless, persistence: { scope: local, durability: ephemeral }, dataCriticality: low }
YAML
cat > "$BDIR/checkout-a/openapi.yaml" <<'YAML'
openapi: "3.0.0"
info: { title: checkout, version: "1.0.0" }
paths:
  /checkout: { post: { responses: { "200": { description: ok } } } }
  /cart: { get: { responses: { "200": { description: ok } } } }
YAML

# Revision B is revision A with ONE change: the /cart path is gone. Both files are
# written out in full rather than derived from A, so the change under analysis is
# readable here instead of hidden in a sed. The real diff engine classifies a
# removed OpenAPI path as Breaking; nothing else about the service moves.
cat > "$BDIR/checkout-b/pacto.yaml" <<'YAML'
pactoVersion: "2.0"
service: { name: checkout, version: "1.1.0", owner: { team: commerce, dri: d, contacts: [ { type: email, value: a@e.com, purpose: escalation } ] } }
interfaces: [ { name: api, type: openapi, ref: openapi.yaml, visibility: public } ]
workload: service
state: { type: stateless, persistence: { scope: local, durability: ephemeral }, dataCriticality: low }
YAML
cat > "$BDIR/checkout-b/openapi.yaml" <<'YAML'
openapi: "3.0.0"
info: { title: checkout, version: "1.1.0" }
paths:
  /checkout: { post: { responses: { "200": { description: ok } } } }
YAML

cat > "$BDIR/orders/pacto.yaml" <<YAML
pactoVersion: "2.0"
service: { name: orders, version: "1.0.0", owner: { team: commerce, dri: d, contacts: [ { type: email, value: a@e.com, purpose: escalation } ] } }
workload: service
state: { type: stateless, persistence: { scope: local, durability: ephemeral }, dataCriticality: low }
dependencies: [ { name: checkout, ref: 'oci://${REG_HOST}/demo/checkout', required: false, compatibility: '^1.0.0' } ]
YAML

REG_PF="$(pf "$LOCAL_REG_PORT" svc/pacto-registry 5000)"
push_bundle() { # <dir> <repo:tag> -> prints the resolved manifest digest
  PACTO_INSECURE_REGISTRIES="127.0.0.1:${LOCAL_REG_PORT}" \
    "$PACTO_BIN" push "oci://127.0.0.1:${LOCAL_REG_PORT}/demo/$2" -p "$1" 2>&1 | grep -oE 'sha256:[0-9a-f]{64}' | head -1
}
DIGEST="$(push_bundle "$BDIR/payments" payments:1.0.0)"
CHECKOUT_A="$(push_bundle "$BDIR/checkout-a" checkout:1.0.0)"
CHECKOUT_B="$(push_bundle "$BDIR/checkout-b" checkout:1.1.0)"
ORDERS_DIGEST="$(push_bundle "$BDIR/orders" orders:1.0.0)"
kill "$REG_PF" 2>/dev/null || true
for d in "$DIGEST" "$CHECKOUT_A" "$CHECKOUT_B" "$ORDERS_DIGEST"; do
  [ -n "$d" ] || fail "could not push/resolve every contract digest"
done
pass "published payments, checkout 1.0.0, checkout 1.1.0 and orders"
CONTRACT_REF="oci://${REG_HOST}/demo/payments@${DIGEST}"

echo "== a managed observation source: the orders -> checkout call, exported offline =="
# The declarative Phase-7 form: a named source the OPERATOR mounts read-only into
# the dashboard it manages. Not the ad-hoc positional --traces path — the Product
# has to show this as a Data Source with the stable identity 'orders-traces'.
kubectl -n "$NS" create configmap pacto-orders-traces --dry-run=client -o yaml \
  --from-literal=traces.json='{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"orders"}}]},"scopeSpans":[{"spans":[{"kind":3,"attributes":[{"key":"peer.service","value":{"stringValue":"checkout"}}]}]}]}]}' \
  | kubectl apply -f - >/dev/null

common_sets=(--set image.repository="$OP_REPO" --set image.tag="$VER" --set image.pullPolicy=Never
             --set dashboard.enabled=true --set evidence.enabled=true
             --set evidence.trust.existingSecret=pacto-evidence-trust
             --set "insecureRegistries[0]=${REG_HOST}"
             --set 'dashboard.observation.sources[0].name=orders-traces'
             --set 'dashboard.observation.sources[0].file=traces.json'
             --set 'dashboard.observation.sources[0].configMap=pacto-orders-traces')

echo "== install the operator with the dashboard + Evidence Server enabled =="
helm install pacto-operator "$CHART" -n "$NS" "${common_sets[@]}" --wait --timeout 240s
for _ in $(seq 1 40); do kubectl -n "$NS" rollout status deployment/pacto-evidence --timeout=10s >/dev/null 2>&1 && break; sleep 3; done
wait_ready pacto-dashboard
wait_ready pacto-evidence

echo "== declared services: two Pacto CRs the operator resolves FROM THE REGISTRY =="
# Both CRs point at an immutable digest, so the operator publishes a real resolved
# contract identity in status and the dashboard reaches the same content back
# through the registry. The running checkout is pinned to revision A while B is
# published but deployed nowhere — which is what makes the A -> B change analysis
# a real question about the fleet rather than a fixture.
kubectl create namespace "$DEMO_NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
for svc in checkout orders; do
  kubectl -n "$DEMO_NS" create deployment "$svc" --image=registry.k8s.io/pause:3.9 >/dev/null 2>&1 || true
done
kubectl -n "$DEMO_NS" rollout status deployment/checkout --timeout=90s
kubectl -n "$DEMO_NS" rollout status deployment/orders --timeout=90s
# checkout's contract declares a public interface, and interface availability is
# ALWAYS a required assertion. Without a Service and a binding the operator has
# nothing to observe it against, so the only honest answer it can give is Unknown.
# A real install binds the interface to the port that serves it; the fixture does
# the same, so checkout's Compliant verdict below is observed, not assumed.
kubectl -n "$DEMO_NS" expose deployment checkout --name=checkout --port=8080 >/dev/null 2>&1 || true
kubectl apply -f - >/dev/null <<YAML
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: { name: checkout, namespace: ${DEMO_NS} }
spec:
  checkIntervalSeconds: 30
  contractRef:
    oci: ${REG_HOST}/demo/checkout@${CHECKOUT_A}
  target:
    serviceName: checkout
    workloadRef: {name: checkout, kind: Deployment}
    interfaceBindings: [ {interface: api, servicePort: 8080} ]
---
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: { name: orders, namespace: ${DEMO_NS} }
spec:
  checkIntervalSeconds: 30
  contractRef:
    oci: ${REG_HOST}/demo/orders@${ORDERS_DIGEST}
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

echo "== the live Product API proves the fixture is ready =="
# The dashboard serves a periodically-refreshed snapshot, so the vertical is not
# ready the moment the last kubectl returns. Waiting is delegated to a Go program
# that re-checks EVERY fact each round against the real Product endpoints and
# reports all outstanding ones together: source usability, service identity,
# revision retrievability, target linkage, the declared and observed edges and
# their reconciliation, and the external evidence target. It also emits the keys
# it DISCOVERED, so nothing downstream has to construct one.
DASH_PF="$(pf 8080 svc/pacto-dashboard 3000)"; sleep 2
FIXTURE_JSON="$(mktemp)"
( cd "$ROOT" && go run ./tests/e2e/kind/productready \
    -base "http://127.0.0.1:8080" -domain "${REG_HOST}/demo" \
    -checkout-a 1.0.0 -checkout-b 1.1.0 -out "$FIXTURE_JSON" ) \
  && pass "the live Product API proves the fixture" || fail "the live Product API never proved the fixture"

# Browser acceptance: drive the LIVE dashboard in Chromium via Playwright, proving
# the real frontend bundle + real HTTP API + real operator data render together —
# not just that the JSON API answers. This is the ONLY live-browser layer; the
# offline WASM suite in `e2e/` covers the same frontend against a seeded in-browser
# backend and deliberately does not duplicate these journeys. Opt-in (the `browser`
# subcommand) so the default vertical run stays dependency-light.
if [ -n "${RUN_BROWSER:-}" ]; then
  echo "== live Product journeys against the LIVE dashboard (Playwright/Chromium) =="
  ( cd "$ROOT/pkg/dashboard/frontend" \
    && npm ci --ignore-scripts >/dev/null 2>&1 \
    && npx playwright install --with-deps chromium >/dev/null 2>&1 \
    && PW_BASE_URL="http://127.0.0.1:8080/" PW_FIXTURE="$(cat "$FIXTURE_JSON")" \
       npx playwright test --config playwright.live.config.ts ) \
    && pass "live dashboard product journeys" || fail "live dashboard product journeys failed"
fi
kill "$DASH_PF" 2>/dev/null || true

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
