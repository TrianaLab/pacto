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
# WHAT the fixture is, this script does not know. `tests/acceptance/scenario`
# declares it once and projects an execution plan; everything below reads that
# plan and acts on it. No service name, repository, tag, port, source id, subject
# or producer is written here — change one in the scenario and this script follows
# without being edited. The plan is DATA, read field by field and validated: it is
# never sourced and never evaluated.
#
# What stays here is what cannot exist before the run: the registry address kind
# assigned, the digests the registry chose, forwarded ports, temp directories,
# waiting, diagnostics and cleanup.
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
# shellcheck source=tests/acceptance/kind/lib.sh
source "$(dirname "$0")/lib.sh"

# plan_records KIND — every plan record of one kind, with the kind stripped, one
# per line, fields still TAB-separated.
#
# The plan is read, not executed: a record can only ever become arguments to a
# command, never a command. Callers split with IFS=$'\t' into named fields plus a
# trailing catch-all and reject anything of the wrong arity, so a record that
# grew or lost a field fails loudly instead of shifting every value left. Feed the
# loop with `< <(plan_records ...)`, never a pipe — a pipe would run the body in a
# subshell where `fail` exits nothing.
plan_records() {
  local kind="$1" line
  while IFS= read -r line; do
    case "$line" in
      "$kind"$'\t'*) printf '%s\n' "${line#*$'\t'}" ;;
    esac
  done < "$PLAN"
}

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

echo "== project the fixture: bundles, observation exports and the execution plan =="
# Everything the Product reasons about downstream is REAL published content, not a
# synthesized shortcut: the external-evidence subject, TWO revisions of one service
# that differ by exactly one deterministic semantic change, and a consumer revision
# that DECLARES the dependency between them. The refs the cluster later stores are
# the in-cluster ones; only the push hop goes through a forwarded port.
#
# The projector writes both the artifacts and the PLAN naming what to do with them.
# The gate below reads the same declaration, so nothing about the fixture is
# written down in two places that can quietly disagree.
PACTO_BIN="$(build_pacto)"
BDIR="$(mktemp -d)"
PLAN="$BDIR/plan.tsv"
CRS="$BDIR/pactos.yaml"
( cd "$ROOT" && go run ./tests/acceptance/scenario/project bundles \
    -dir "$BDIR" -domain "${REG_HOST}/demo" -plan "$PLAN" )

echo "== trust store: the declared producer's keypair -> a Secret evidence mounts =="
# WHO signs is the scenario's to declare, and the trust store binds the key to
# exactly that producer: sign as anyone else and ingestion rejects it.
SIGN_PRODUCER=""; SIGN_KEY_ID=""; SIGN_EXTRA=""
IFS=$'\t' read -r SIGN_PRODUCER SIGN_KEY_ID SIGN_EXTRA < <(plan_records signer) || true
[ -n "$SIGN_PRODUCER" ] && [ -n "$SIGN_KEY_ID" ] && [ -z "$SIGN_EXTRA" ] \
  || fail "the plan declares no usable signer"
KEYDIR="$(trust_keypair "$PACTO_BIN" "$SIGN_KEY_ID" "$SIGN_PRODUCER")"

echo "== publish the fixture's contract revisions to the in-cluster registry =="
REG_PF="$(pf "$LOCAL_REG_PORT" svc/pacto-registry 5000)"
DIGESTS=()
while IFS=$'\t' read -r key dir ref extra; do
  [ -n "$key" ] && [ -n "$dir" ] && [ -n "$ref" ] && [ -z "${extra:-}" ] \
    || fail "malformed push record in $PLAN"
  digest="$(push_bundle "$PACTO_BIN" "$LOCAL_REG_PORT" "$dir" "$ref")"
  [ -n "$digest" ] || fail "could not push/resolve a digest for $key"
  DIGESTS+=(-digest "${key}=${digest}")
  pass "published $ref"
done < <(plan_records push)
kill "$REG_PF" 2>/dev/null || true
[ "${#DIGESTS[@]}" -gt 0 ] || fail "the plan declares nothing to publish"

echo "== project what needs real digests: the CRs, the evidence payloads, the subjects =="
# The Evidence Server's subjects are these same digests: the registry holding
# those revisions IS the evidence store, so the chart values naming them are
# projected from the scenario rather than assembled here, exactly like the
# Compose surface's are.
EV_VALUES="$BDIR/helm-evidence.txt"
( cd "$ROOT" && go run ./tests/acceptance/scenario/project cluster \
    -dir "$BDIR" -domain "${REG_HOST}/demo" -namespace "$DEMO_NS" -crs "$CRS" \
    -helm-out "$EV_VALUES" "${DIGESTS[@]}" )

echo "== managed observation sources: the declared calls, exported offline =="
# The declarative form: named sources the OPERATOR mounts read-only into
# the dashboard it manages. Not the ad-hoc positional --traces path — the Product
# has to show each as a Data Source with this stable identity, and the export it
# carries is the one the scenario derived from the declared edge, so observed and
# declared cannot drift apart.
obs_i=0
while IFS=$'\t' read -r source_id configmap file_key export_path extra; do
  [ -n "$source_id" ] && [ -n "$configmap" ] && [ -n "$file_key" ] && [ -n "$export_path" ] && [ -z "${extra:-}" ] \
    || fail "malformed observation record in $PLAN"
  kubectl -n "$NS" create configmap "$configmap" --dry-run=client -o yaml \
    --from-file="${file_key}=${export_path}" | kubectl apply -f - >/dev/null
  obs_i=$((obs_i + 1))
done < <(plan_records observation)
[ "$obs_i" -gt 0 ] || fail "the plan declares no observation source"

# WHICH sources the dashboard is told about is the scenario's to say, so the chart
# values are PROJECTED rather than assembled here — the same declaration the
# Compose surface renders into its dashboard command. tests/acceptance/scenario/
# parity_test.go compares the two, so neither surface can quietly start naming a
# source the other does not. Joined here with the digest-dependent values the
# cluster projection already wrote: both are the scenario's, one just had to wait
# for the registry.
HELM_VALUES="$BDIR/helm-values.txt"
( cd "$ROOT" && go run ./tests/acceptance/scenario/project helm -out "$HELM_VALUES" )
projected_sets=()
while IFS= read -r value; do
  [ -n "$value" ] || continue
  projected_sets+=(--set "$value")
done < <(cat "$HELM_VALUES" "$EV_VALUES")
[ "${#projected_sets[@]}" -gt 0 ] || fail "the projection produced no chart values"

# The remaining --set values are this RUN's, not the fixture's: the image kind
# just loaded, the registry it just brought up, the components under test. They
# have one consumer and no counterpart in the scenario, so they stay here.
common_sets=(--set image.repository="$OP_REPO" --set image.tag="$VER" --set image.pullPolicy=Never
             --set dashboard.enabled=true --set evidence.enabled=true
             --set evidence.trust.existingSecret=pacto-evidence-trust
             --set "insecureRegistries[0]=${REG_HOST}"
             "${projected_sets[@]}")

echo "== install the operator with the dashboard + Evidence Server enabled =="
helm install pacto-operator "$CHART" -n "$NS" "${common_sets[@]}" --wait --timeout 240s
wait_managed_ready pacto-dashboard
wait_managed_ready pacto-evidence

echo "== declared services: the projected Pacto CRs, resolved FROM THE REGISTRY =="
# Every CR points at an immutable digest, so the operator publishes a real resolved
# contract identity in status and the dashboard reaches the same content back
# through the registry. WHICH revision each one pins is the scenario's Deployed
# flag, projected: a service can be published without being deployed anywhere,
# which is what makes a change analysis a real question about the fleet rather
# than a fixture.
#
# A workload whose contract declares a public interface needs a Service and a
# binding, or the operator has nothing to observe interface availability against
# and its only honest answer is Unknown. A declared port produces both; a zero
# port is the scenario saying this workload is deliberately unexposed.
kubectl create namespace "$DEMO_NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
workloads=()
while IFS=$'\t' read -r service deployment port extra; do
  [ -n "$service" ] && [ -n "$deployment" ] && [ -n "$port" ] && [ -z "${extra:-}" ] \
    || fail "malformed workload record in $PLAN"
  kubectl -n "$DEMO_NS" create deployment "$deployment" --image=registry.k8s.io/pause:3.9 >/dev/null 2>&1 || true
  kubectl -n "$DEMO_NS" rollout status "deployment/$deployment" --timeout=90s
  [ "$port" = 0 ] \
    || kubectl -n "$DEMO_NS" expose deployment "$deployment" --name="$deployment" --port="$port" >/dev/null 2>&1 || true
  workloads+=("$service")
done < <(plan_records workload)
[ "${#workloads[@]}" -gt 0 ] || fail "the plan declares no workload to reconcile"
kubectl apply -f "$CRS" >/dev/null
# Compliant is this run's readiness barrier, not a fact under test: the gate below
# re-derives every verdict from the live Product API. Waiting here only keeps the
# gate from starting before the operator has reconciled anything.
for service in "${workloads[@]}"; do
  wait_pacto_status "$DEMO_NS" "$service" Compliant \
    && pass "$service reconciled Compliant" || fail "$service did not reconcile"
done

echo "== ingest the declared signed EvidenceEnvelopes from a 'remote' environment =="
# The payloads were projected with the real published ContractRef; signing and
# sending them is all that is left. Sequence numbers are producer-scoped and
# strictly increasing, so they come from the plan rather than from a counter here.
EV_PF="$(pf "$LOCAL_EV_PORT" svc/pacto-evidence 8686)"
while IFS=$'\t' read -r subject payload sequence envelope_id extra; do
  [ -n "$subject" ] && [ -n "$payload" ] && [ -n "$sequence" ] && [ -n "$envelope_id" ] && [ -z "${extra:-}" ] \
    || fail "malformed evidence record in $PLAN"
  "$PACTO_BIN" evidence sign "$payload" --key "$KEYDIR/${SIGN_KEY_ID}.key" \
    --key-id "$SIGN_KEY_ID" --producer "$SIGN_PRODUCER" \
    --sequence "$sequence" --id "$envelope_id" > "$BDIR/${envelope_id}.envelope.json"
  "$PACTO_BIN" evidence send "$BDIR/${envelope_id}.envelope.json" --url "http://127.0.0.1:${LOCAL_EV_PORT}" >/dev/null 2>&1 \
    && pass "$subject evidence accepted" || fail "$subject evidence not accepted"
done < <(plan_records evidence)
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
