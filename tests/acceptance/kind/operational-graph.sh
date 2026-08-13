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
# `tests/acceptance/kind/productready` (Go) proves the fixture is ready and emits the
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
# The observation source's identity, as the operator configures it and as the
# Product must publish it. It names the export the scenario projector writes.
OBS_SOURCE=orders-traces
# shellcheck source=tests/acceptance/kind/lib.sh
source "$(dirname "$0")/lib.sh"

CMD="${1:-run}"
case "$CMD" in
  status)
    use_existing_cluster "make e2e-operational-graph-up"
    echo "== pods =="; kubectl -n "$NS" get pods
    echo "== services =="; kubectl -n "$NS" get svc
    echo "== pvc =="; kubectl -n "$NS" get pvc
    echo "== reconciled Pacto CRs ($DEMO_NS) =="; kubectl -n "$DEMO_NS" get pacto -o wide 2>/dev/null || true
    for d in pacto-operator pacto-dashboard pacto-evidence pacto-registry; do
      kubectl -n "$NS" rollout status "deployment/$d" --timeout=5s >/dev/null 2>&1 && echo "  $d: Ready" || echo "  $d: not ready"
    done
    exit 0 ;;
  logs) use_existing_cluster "make e2e-operational-graph-up"; dump_diag "$NS"; exit 0 ;;
  down) down_cluster; exit 0 ;;
  run|up) : ;;
  browser) RUN_BROWSER=1 ;; # full bring-up + a Playwright run against the live dashboard
  *) echo "unknown subcommand: $CMD (use up|status|logs|down|browser)"; exit 2 ;;
esac

# shellcheck disable=SC2154  # rc is assigned by rc=$? inside the trap body
trap 'rc=$?; [ $rc -ne 0 ] && dump_diag "$NS"; pkill -f "kubectl.*port-forward" 2>/dev/null || true; exit $rc' EXIT

VER="$(release_version kubernetes)"
CORE="$(release_version core)"
OP_REPO="localhost:5001/pacto-operator/pacto-controller"
OP_IMG="${OP_REPO}:${VER}"
DASH_IMG="localhost:5001/pacto-dashboard:${CORE}"

build_operator_images "$OP_IMG" "$DASH_IMG" "$VER"

echo "== package the operator chart =="
CHART="$(package_chart "$PACTO_CHART")"

ensure_cluster
load_images "$DASH_IMG" "$OP_IMG"
kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

echo "== an in-cluster OCI registry makes contract revisions resolvable =="
install_registry

echo "== trust store: a producer keypair -> a Secret the Evidence Server mounts =="
PACTO_BIN="$(build_pacto)"
KEYDIR="$(trust_keypair "$PACTO_BIN")"

echo "== publish the fixture's contract revisions to the in-cluster registry =="
# Everything the Product reasons about downstream is REAL published content, not a
# synthesized shortcut: the external-evidence subject (payments), TWO checkout
# revisions that differ by exactly one deterministic semantic change, and an orders
# revision that DECLARES the checkout dependency. The refs the cluster later stores
# are the in-cluster ones; only this push hop goes through a forwarded port.
#
# WHAT is published is declared once, in tests/acceptance/scenario, and rendered
# here — bundles and the observation export together. The gate below reads the
# same value, so nothing about the fixture is written down in two places that can
# quietly disagree.
BDIR="$(mktemp -d)"
( cd "$ROOT" && go run ./tests/acceptance/scenario/project -dir "$BDIR" -domain "${REG_HOST}/demo" )

REG_PF="$(pf "$LOCAL_REG_PORT" svc/pacto-registry 5000)"
push() { push_bundle "$PACTO_BIN" "$LOCAL_REG_PORT" "$@"; }
DIGEST="$(push "$BDIR/payments" payments:1.0.0)"
CHECKOUT_A="$(push "$BDIR/checkout-a" checkout:1.0.0)"
CHECKOUT_B="$(push "$BDIR/checkout-b" checkout:1.1.0)"
ORDERS_DIGEST="$(push "$BDIR/orders" orders:1.0.0)"
kill "$REG_PF" 2>/dev/null || true
for d in "$DIGEST" "$CHECKOUT_A" "$CHECKOUT_B" "$ORDERS_DIGEST"; do
  [ -n "$d" ] || fail "could not push/resolve every contract digest"
done
pass "published payments, checkout 1.0.0, checkout 1.1.0 and orders"
CONTRACT_REF="oci://${REG_HOST}/demo/payments@${DIGEST}"

echo "== a managed observation source: the orders -> checkout call, exported offline =="
# The declarative Phase-7 form: a named source the OPERATOR mounts read-only into
# the dashboard it manages. Not the ad-hoc positional --traces path — the Product
# has to show this as a Data Source with this stable identity, and the export it
# carries is the one the scenario derived from the declared edge, so observed and
# declared cannot drift apart.
kubectl -n "$NS" create configmap "pacto-${OBS_SOURCE}" --dry-run=client -o yaml \
  --from-file="traces.json=$BDIR/${OBS_SOURCE}.json" \
  | kubectl apply -f - >/dev/null

common_sets=(--set image.repository="$OP_REPO" --set image.tag="$VER" --set image.pullPolicy=Never
             --set dashboard.enabled=true --set evidence.enabled=true
             --set evidence.trust.existingSecret=pacto-evidence-trust
             --set "insecureRegistries[0]=${REG_HOST}"
             --set "dashboard.observation.sources[0].name=${OBS_SOURCE}"
             --set 'dashboard.observation.sources[0].file=traces.json'
             --set "dashboard.observation.sources[0].configMap=pacto-${OBS_SOURCE}")

echo "== install the operator with the dashboard + Evidence Server enabled =="
helm install pacto-operator "$CHART" -n "$NS" "${common_sets[@]}" --wait --timeout 240s
wait_managed_ready pacto-dashboard
wait_managed_ready pacto-evidence

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
wait_pacto_status "$DEMO_NS" checkout Compliant && pass "checkout reconciled Compliant" || fail "checkout did not reconcile"
wait_pacto_status "$DEMO_NS" orders Compliant && pass "orders reconciled Compliant" || fail "orders did not reconcile"

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
#
# The POST-CACHE state is proved by the FACTS, not by counting snapshots. The
# pod's OCI cache starts empty and the first refresh's registry pulls are what
# fill it, so the pairing that used to publish one artifact as two revisions only
# exists once both sources contribute; the gate therefore requires the cache
# source to be usable AND every fixture revision to name both the registry and
# the cache in its provenance while staying ONE canonical, exact, retrievable
# revision. Counting refreshes could not establish that: a SnapshotID hashes the
# generation time, so distinct ids prove only that time passed.
#
# -snapshots 2 remains as a STABILITY requirement on top: the fixture must hold
# across a refresh, not merely at one lucky instant. (A pod restart would be the
# wrong lever: the operator mounts the cache as an emptyDir, so restarting ERASES
# the state under test.)
DASH_PF="$(pf 8080 svc/pacto-dashboard 3000)"
FIXTURE_JSON="$(mktemp)"
( cd "$ROOT" && go run ./tests/acceptance/kind/productready \
    -base "http://127.0.0.1:8080" -domain "${REG_HOST}/demo" \
    -snapshots 2 -out "$FIXTURE_JSON" ) \
  && pass "the live Product API proves the fixture, twice, across a refresh" \
  || fail "the live Product API never proved the fixture on two distinct snapshots"

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
keep_or_teardown "$NS" "$CLUSTER" delete_cluster
