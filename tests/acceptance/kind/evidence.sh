#!/usr/bin/env bash
# Kind acceptance for the operator-managed Evidence Server. It proves BOTH the
# operator reconciliation (a separate Evidence Deployment + internal Service +
# retained PVC when evidence.enabled=true; readiness gated on recovery; the
# managed dashboard auto-wired) AND the REAL in-cluster evidence lifecycle:
# a contract revision is made resolvable from an in-cluster registry, a signed
# EvidenceEnvelope is POSTed to the in-cluster Evidence Service, accepted, and
# committed durably to the PVC; the same target then appears through the Evidence
# source API, the dashboard Fleet API and the CLI over the same store; replay and
# restart-recovery, a newer sequence, manifest-projection reconstruction (proven
# physically on disk) and a semantically-corrupt record (degraded state) are all
# exercised in the cluster — not delegated to a filesystem-only test.
set -euo pipefail
CLUSTER="${KIND_CLUSTER:-pacto-evidence}"
NS=pacto-system
REG_HOST="pacto-registry.${NS}.svc.cluster.local:5000"
LOCAL_REG_PORT=5599
LOCAL_EV_PORT=8686
# shellcheck source=tests/acceptance/kind/lib.sh
source "$(dirname "$0")/lib.sh"
# shellcheck disable=SC2154  # rc is assigned by rc=$? inside the trap body
trap 'rc=$?; [ $rc -ne 0 ] && dump_diag "$NS"; pkill -f "kubectl.*port-forward" 2>/dev/null || true; exit $rc' EXIT
VER="$(release_version kubernetes)"
CORE="$(release_version core)"
OP_IMG="localhost:5001/pacto-operator/pacto-controller:${VER}"
OP_REPO="localhost:5001/pacto-operator/pacto-controller"
DASH_IMG="localhost:5001/pacto-dashboard:${CORE}"

build_operator_images "$OP_IMG" "$DASH_IMG" "$VER"

echo "== package the chart =="
CHART="$(package_chart "$PACTO_CHART")"

ensure_cluster
load_images "$DASH_IMG" "$OP_IMG"
kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

echo "== an in-cluster OCI registry (plain HTTP) makes a contract revision resolvable =="
install_registry

echo "== trust store: a producer keypair -> a Secret the Evidence Server mounts =="
PACTO_BIN="$(build_pacto)"
KEYDIR="$(trust_keypair "$PACTO_BIN")"

echo "== push a contract bundle to the in-cluster registry (over the forwarded port) =="
BDIR="$(mktemp -d)"
cat > "$BDIR/pacto.yaml" <<'YAML'
pactoVersion: "2.0"
service:
  name: checkout
  version: "1.0.0"
interfaces:
  - name: api
    type: openapi
    ref: openapi.yaml
    visibility: public
workload: service
state:
  type: stateless
  persistence: { scope: local, durability: ephemeral }
  dataCriticality: low
YAML
printf 'openapi: "3.0.0"\ninfo: { title: checkout, version: "1.0.0" }\npaths: {}\n' > "$BDIR/openapi.yaml"
REG_PF_PID="$(pf "$LOCAL_REG_PORT" svc/pacto-registry 5000)"
DIGEST="$(push_bundle "$PACTO_BIN" "$LOCAL_REG_PORT" "$BDIR" checkout:1.0.0)"
kill "$REG_PF_PID" 2>/dev/null || true
[ -n "$DIGEST" ] && pass "pushed contract revision $DIGEST" || fail "could not push/resolve the contract digest"
CONTRACT_REF="oci://${REG_HOST}/demo/checkout@${DIGEST}"

common_sets=(--set image.repository="$OP_REPO" --set image.tag="$VER" --set image.pullPolicy=Never
             --set dashboard.enabled=true
             --set evidence.enabled=true
             --set evidence.trust.existingSecret=pacto-evidence-trust)

echo "== install the operator with the Evidence Server enabled =="
helm install pacto-operator "$CHART" -n "$NS" "${common_sets[@]}" --wait --timeout 240s

echo "== the operator reconciles a managed Evidence Server (readiness gated on recovery) =="
wait_managed_ready pacto-evidence \
  && pass "evidence Deployment is Ready (storage recovered)" \
  || fail "evidence Deployment did not become Ready"
kubectl -n "$NS" get svc pacto-evidence >/dev/null && pass "internal Evidence Service exists" || fail "internal Evidence Service missing"
kubectl -n "$NS" get pvc pacto-evidence-data >/dev/null && pass "evidence PVC provisioned" || fail "evidence PVC missing"
replicas="$(kubectl -n "$NS" get deploy pacto-evidence -o jsonpath='{.spec.replicas}')"
[ "$replicas" = "1" ] && pass "single writer (replicas=1)" || fail "expected 1 replica, got $replicas"

echo "== teach the Evidence Server to resolve the in-cluster (plain-HTTP) registry =="
# kubectl-set env is a distinct field manager; the operator's server-side apply
# does not own the env field, so this persists across reconciles.
kubectl -n "$NS" set env deployment/pacto-evidence "PACTO_INSECURE_REGISTRIES=${REG_HOST}" >/dev/null
kubectl -n "$NS" rollout status deployment/pacto-evidence --timeout=120s

# Helper: sign an EvidenceSet for `checkout` at the given sequence + envelope id.
sign_envelope() {
  local seq="$1" id="$2" out="$3"
  cat > "${out}.evidence.json" <<JSON
{
  "Subject": { "kind": "service", "name": "checkout" },
  "ContractRef": "${CONTRACT_REF}",
  "Source": "e2e",
  "ObservedAt": "2026-07-29T12:00:00Z",
  "Observations": [
    {
      "kind": "WorkloadObserved",
      "subject": { "kind": "service", "name": "checkout" },
      "outcome": "Observed",
      "value": { "type": "service" },
      "provenance": { "collector": "e2e", "detectedAt": "2026-07-29T12:00:00Z" }
    }
  ]
}
JSON
  "$PACTO_BIN" evidence sign "${out}.evidence.json" --key "$KEYDIR/demo.key" --key-id demo \
    --producer demo --sequence "$seq" --id "$id" > "$out"
}

echo "== sign + POST a signed envelope to the in-cluster Evidence Service =="
WORK="$(mktemp -d)"
sign_envelope 1 e2e-1 "$WORK/env1.json"
EV_PF_PID="$(pf "$LOCAL_EV_PORT" svc/pacto-evidence 8686)"
sleep 1
# send captures the ingestion host's response so a failure surfaces the HTTP
# status, stable error code and sanitized message (never silently discarded).
send() { "$PACTO_BIN" evidence send "$1" --url "http://127.0.0.1:${LOCAL_EV_PORT}" 2>&1; }
send_ok() { # envelope, label
  local out
  if out="$(send "$1")"; then pass "$2"; else
    echo "  ingestion response: $out"
    kubectl -n "$NS" exec deploy/pacto-evidence -- pacto evidence inspect --bucket-url file:///var/lib/pacto/evidence 2>&1 | head -20 || true
    fail "$2"
  fi
}
send_rejected() { # envelope, label (expects a NON-2xx; prints the code on unexpected accept)
  local out
  if out="$(send "$1")"; then echo "  unexpectedly accepted: $out"; fail "$2"; else pass "$2"; fi
}
send_ok "$WORK/env1.json" "envelope accepted (202)"

echo "== the accepted record is committed durably to the PVC =="
kubectl -n "$NS" exec deploy/pacto-evidence -- sh -c 'ls -R /var/lib/pacto/evidence 2>/dev/null | grep -q envelopes' \
  && pass "durable immutable record exists on the PVC" || fail "no durable record on the PVC"

echo "== the Evidence source API exposes the accepted target =="
curl -fsS "http://127.0.0.1:${LOCAL_EV_PORT}/api/evidence/v1/targets" | grep -q '"subject":"checkout"' \
  && pass "Evidence source API reports the checkout target" || fail "Evidence source API missing the target"

echo "== the dashboard Fleet API reports the same target from the same store =="
DASH_PF_PID="$(pf 8080 svc/pacto-dashboard 3000)"
# The dashboard serves a periodically-refreshed snapshot (fleetRefreshInterval,
# 30s), so an envelope ingested after its first build appears on the next refresh.
# Poll rather than race the first build.
dash_has_checkout() { curl -fsS "http://127.0.0.1:8080/api/fleet/snapshot" 2>/dev/null | grep -q 'checkout'; }
eventually 24 dash_has_checkout \
  && pass "dashboard Fleet API reports the checkout target" || fail "dashboard Fleet API missing the target"

echo "== the CLI reports the same target over the same Evidence source =="
# Retry: the CLI builds a fresh snapshot each run, but a long-lived kubectl
# port-forward can drop transiently. On failure the actual output is printed for
# diagnosis rather than swallowed.
cli_out=""
cli_has_checkout() {
  cli_out="$("$PACTO_BIN" fleet search --evidence-url "http://127.0.0.1:${LOCAL_EV_PORT}" 2>&1 || true)"
  printf '%s' "$cli_out" | grep -q checkout
}
eventually 10 cli_has_checkout || { echo "  CLI output was: $cli_out"; fail "CLI missing the target"; }
pass "CLI fleet search reports the checkout target"

echo "== replay: re-sending the same envelope is rejected (409) =="
send_rejected "$WORK/env1.json" "replay rejected"

echo "== a newer sequence is accepted =="
sign_envelope 2 e2e-2 "$WORK/env2.json"
send_ok "$WORK/env2.json" "newer sequence accepted"

echo "== restart-recovery: replay protection survives a pod restart =="
kubectl -n "$NS" delete pod -l app.kubernetes.io/component=evidence --wait=false >/dev/null 2>&1 || \
  kubectl -n "$NS" delete pod -l app=pacto-evidence --wait=false >/dev/null 2>&1 || \
  kubectl -n "$NS" rollout restart deployment/pacto-evidence >/dev/null
kubectl -n "$NS" rollout status deployment/pacto-evidence --timeout=120s
kill "$EV_PF_PID" 2>/dev/null || true
EV_PF_PID="$(pf "$LOCAL_EV_PORT" svc/pacto-evidence 8686)"
send_rejected "$WORK/env1.json" "replay still rejected after restart (rebuilt from immutable records)"
send_rejected "$WORK/env2.json" "seq-2 replay still rejected after restart"

echo "== the manifest projection is rebuildable: delete it, restart, target survives AND the manifest is physically rewritten =="
# find (not a fixed-depth glob) so this works regardless of the prefix depth
# (DefaultEvidencePrefix is two levels: pacto-evidence/v1). The only materialized
# projection is manifest.json; the per-target latest is served from the in-memory
# index recovery rebuilds from the immutable log, never a persisted per-target file.
kubectl -n "$NS" exec deploy/pacto-evidence -- sh -c 'find /var/lib/pacto/evidence -type d -name materialized -exec rm -rf {} + 2>/dev/null; true'
kubectl -n "$NS" rollout restart deployment/pacto-evidence >/dev/null
kubectl -n "$NS" rollout status deployment/pacto-evidence --timeout=120s
kill "$EV_PF_PID" 2>/dev/null || true
EV_PF_PID="$(pf "$LOCAL_EV_PORT" svc/pacto-evidence 8686)"
curl -fsS "http://127.0.0.1:${LOCAL_EV_PORT}/api/evidence/v1/targets" | grep -q '"subject":"checkout"' \
  && pass "target reconstructed from the immutable log after projection loss" || fail "projection reconstruction failed"
# Physically prove the manifest object was RESTORED on disk (not only that /targets
# answers from memory): the single writer rewrites it via recovery + RepairProjections.
kubectl -n "$NS" exec deploy/pacto-evidence -- sh -c 'set -e; for _ in $(seq 1 30); do find /var/lib/pacto/evidence -type f -name manifest.json | grep -q . && exit 0; sleep 2; done; exit 1' \
  && pass "manifest projection physically rewritten on disk after loss" || fail "manifest projection was not physically restored"

echo "== a semantically-corrupt immutable record surfaces a degraded store =="
# find the envelopes dir regardless of prefix depth; fail loudly if absent rather
# than falling back to a read-only CWD write.
kubectl -n "$NS" exec deploy/pacto-evidence -- sh -c 'set -e; d=$(find /var/lib/pacto/evidence -type d -name envelopes | head -1); [ -n "$d" ]; printf "{not-a-record}" > "$d/zzzz-corrupt.json"'
kubectl -n "$NS" rollout restart deployment/pacto-evidence >/dev/null
kubectl -n "$NS" rollout status deployment/pacto-evidence --timeout=120s
# Poll, like every other post-restart assertion here: `rollout status` returns when the
# new pod is Ready, and readiness is gated on recovery having STARTED, so a single shot
# can read the store a moment before the scan reaches the corrupt object -- and `exec
# deploy/...` can still pick the terminating pod. The claim is that the store surfaces
# the corruption, not that it does so within one round trip.
store_degraded() {
  kubectl -n "$NS" exec deploy/pacto-evidence -- pacto evidence inspect --bucket-url file:///var/lib/pacto/evidence 2>/dev/null \
    | grep -qiE 'degraded|corrupt'
}
eventually 30 store_degraded \
  && pass "store reports degraded with the corrupt record surfaced (usable records retained)" \
  || fail "store did not surface the corrupt record as degraded"
kill "$EV_PF_PID" "$DASH_PF_PID" 2>/dev/null || true

echo "== disabling the Evidence Server RETAINS the PVC (persistent evidence preserved) =="
helm upgrade pacto-operator "$CHART" -n "$NS" "${common_sets[@]}" --set evidence.enabled=false --wait --timeout 180s
evidence_gone() { ! deploy_exists pacto-evidence; }
eventually 40 evidence_gone && pass "evidence Deployment removed on disable" || fail "evidence Deployment survived disable"
kubectl -n "$NS" get pvc pacto-evidence-data >/dev/null 2>&1 \
  && pass "evidence PVC RETAINED after disable" || fail "evidence PVC was deleted on disable"

echo "== when disabled, the dashboard no longer reports the Evidence source =="
kubectl -n "$NS" rollout status deployment/pacto-dashboard --timeout=120s
DASH_PF_PID="$(pf 8080 svc/pacto-dashboard 3000)"; sleep 2
kubectl -n "$NS" get deploy pacto-dashboard -o jsonpath='{.spec.template.spec.containers[0].env[*].name}' \
  | grep -q PACTO_EVIDENCE_SOURCE_URL \
  && fail "dashboard still wired to the (disabled) Evidence Server" \
  || pass "dashboard drops the Evidence source when the component is disabled"
kill "$DASH_PF_PID" 2>/dev/null || true

echo "== re-enabling recovers the RETAINED evidence (data survives the disable cycle) =="
helm upgrade pacto-operator "$CHART" -n "$NS" "${common_sets[@]}" --wait --timeout 180s
wait_managed_ready pacto-evidence && pass "evidence Deployment recovered against the retained PVC" || fail "did not recover after re-enable"
kubectl -n "$NS" set env deployment/pacto-evidence "PACTO_INSECURE_REGISTRIES=${REG_HOST}" >/dev/null
kubectl -n "$NS" rollout status deployment/pacto-evidence --timeout=120s
EV_PF_PID="$(pf "$LOCAL_EV_PORT" svc/pacto-evidence 8686)"
curl -fsS "http://127.0.0.1:${LOCAL_EV_PORT}/api/evidence/v1/targets" | grep -q '"subject":"checkout"' \
  && pass "previously-ingested evidence survived the disable/re-enable cycle" || fail "evidence did not survive disable/re-enable"
kill "$EV_PF_PID" 2>/dev/null || true

echo "== full in-cluster Evidence Server lifecycle acceptance PASSED =="
keep_or_teardown "$NS" "$CLUSTER"
