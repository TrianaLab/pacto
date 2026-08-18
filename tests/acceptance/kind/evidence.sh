#!/usr/bin/env bash
# Kind acceptance for the operator-managed Evidence Server. It proves BOTH the
# operator reconciliation (a separate Evidence Deployment + internal Service and
# NOTHING durable in the cluster when evidence.enabled=true; readiness gated on
# the registry answering native Referrers discovery; the managed dashboard
# auto-wired) AND the REAL in-cluster evidence lifecycle: a contract revision is
# made resolvable from an in-cluster registry, a signed EvidenceEnvelope is POSTed
# to the in-cluster Evidence Service, accepted, and published as an OCI 1.1
# referrer of that exact revision; the same target then appears through the
# Evidence source API, the dashboard Fleet API and the CLI over the same
# registry; replay, restart with no local state whatsoever, interoperability with
# the ORAS CLI in both directions, an unrelated artifact type being ignored and a
# malformed Pacto artifact (partial reads, writes fail closed) are all exercised
# in the cluster — not delegated to a filesystem-only test.
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

# ORAS is the INTEROPERABILITY OBSERVER for this scenario, and only that: it never
# stands in for anything Pacto does. Its whole job is to prove the evidence store
# is the OCI registry itself — a third-party client can discover what Pacto wrote
# and Pacto reads what a third-party client wrote.
command -v oras >/dev/null 2>&1 || fail "oras is required: it is the independent observer that proves the evidence store is a plain OCI registry"
EV_TYPE=application/vnd.pacto.evidence.record.v1+json
# --distribution-spec v1.1-referrers-api is not decoration: without it the ORAS
# CLI silently falls back to the referrers-TAG scheme when the native endpoint is
# missing, and every leg below would "prove" interoperability against a
# client-side emulation Pacto refuses to use. Pinned, the observer reads the same
# endpoint the Evidence Server does or it fails.
ORAS_API=(--plain-http --distribution-spec v1.1-referrers-api)

build_operator_images "$OP_IMG" "$DASH_IMG" "$VER"

echo "== package the chart =="
CHART="$(package_chart "$PACTO_CHART")"

ensure_cluster
load_images "$DASH_IMG" "$OP_IMG"
kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

echo "== an in-cluster OCI registry (plain HTTP) IS the evidence store =="
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
[ -n "$DIGEST" ] && pass "pushed contract revision $DIGEST" || fail "could not push/resolve the contract digest"
# The subject as the Evidence Server is configured with it (the in-cluster DNS
# name) and as this harness reaches the same manifest (the forwarded port). One
# manifest, two routes to it.
CONTRACT_REF="oci://${REG_HOST}/demo/checkout@${DIGEST}"
LOCAL_SUBJECT="127.0.0.1:${LOCAL_REG_PORT}/demo/checkout@${DIGEST}"

common_sets=(--set image.repository="$OP_REPO" --set image.tag="$VER" --set image.pullPolicy=Never
             --set dashboard.enabled=true
             --set "insecureRegistries[0]=${REG_HOST}"
             --set evidence.enabled=true
             --set evidence.trust.existingSecret=pacto-evidence-trust
             --set "evidence.registry.subjects[0]=${CONTRACT_REF}")

echo "== install the operator with the Evidence Server enabled =="
helm install pacto-operator "$CHART" -n "$NS" "${common_sets[@]}" --wait --timeout 240s

echo "== the operator reconciles a managed Evidence Server (readiness gated on native Referrers discovery) =="
wait_managed_ready pacto-evidence \
  && pass "evidence Deployment is Ready (every configured subject resolved and enumerable)" \
  || fail "evidence Deployment did not become Ready"
kubectl -n "$NS" get svc pacto-evidence >/dev/null && pass "internal Evidence Service exists" || fail "internal Evidence Service missing"

echo "== the cluster holds NOTHING durable: no PVC, no data volume =="
# The store is the registry. A PVC here would be a second place evidence could
# live, and the two would drift the first time one of them was restored.
[ -z "$(kubectl -n "$NS" get pvc -o name 2>/dev/null)" ] \
  && pass "no PersistentVolumeClaim exists in the namespace" \
  || fail "an evidence PVC was provisioned: $(kubectl -n "$NS" get pvc -o name | tr '\n' ' ')"
[ "$(kubectl -n "$NS" get deploy pacto-evidence -o jsonpath='{.spec.template.spec.volumes[?(@.persistentVolumeClaim)].name}{.spec.template.spec.volumes[?(@.emptyDir)].name}')" = "" ] \
  && pass "the pod mounts no storage of any kind" || fail "the evidence pod still mounts storage"
replicas="$(kubectl -n "$NS" get deploy pacto-evidence -o jsonpath='{.spec.replicas}')"
[ "$replicas" = "1" ] && pass "single writer (replicas=1)" || fail "expected 1 replica, got $replicas"
# Recreate, not RollingUpdate: a rolling update briefly runs two writers, and the
# registry offers no compare-and-set that would stop both passing the same replay
# check.
strategy="$(kubectl -n "$NS" get deploy pacto-evidence -o jsonpath='{.spec.strategy.type}')"
[ "$strategy" = "Recreate" ] && pass "single writer enforced across rollouts (Recreate)" || fail "expected the Recreate strategy, got $strategy"

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
    oras discover "${ORAS_API[@]}" "$LOCAL_SUBJECT" 2>&1 | head -20 || true
    fail "$2"
  fi
}
send_rejected() { # envelope, label (expects a NON-2xx; prints the code on unexpected accept)
  local out
  if out="$(send "$1")"; then echo "  unexpectedly accepted: $out"; fail "$2"; else pass "$2"; fi
}
send_ok "$WORK/env1.json" "envelope accepted (202)"

echo "== the accepted record is an OCI referrer of the exact contract revision, discoverable by ORAS =="
oras_referrers() { oras discover "${ORAS_API[@]}" --format json "$LOCAL_SUBJECT" 2>/dev/null; }
# ORAS 1.2 keys the discovered list "manifests", ORAS 1.3 keys it "referrers".
# Accept both: which CLI build the runner installed is not what this leg tests.
oras_evidence_digests() { oras_referrers | jq -r --arg t "$EV_TYPE" '((.manifests // []) + (.referrers // []))[] | select(.artifactType==$t) | .reference'; }
[ "$(oras_evidence_digests | wc -l | tr -d ' ')" = "1" ] \
  && pass "an independent OCI client discovers exactly one Pacto evidence referrer" \
  || fail "ORAS did not discover the published record: $(oras_referrers)"

echo "== the Evidence source API exposes the accepted target =="
curl -fsS "http://127.0.0.1:${LOCAL_EV_PORT}/api/evidence/v1/targets" | grep -q '"subject":"checkout"' \
  && pass "Evidence source API reports the checkout target" || fail "Evidence source API missing the target"
curl -fsS "http://127.0.0.1:${LOCAL_EV_PORT}/api/evidence/v1/targets" | grep -q '"status":"ready"' \
  && pass "the read is authoritative (health ready)" || fail "health is not ready with a fully-readable registry"

echo "== the dashboard Fleet API reports the same target from the same registry =="
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

echo "== restart with NO local state: the history is rebuilt from the registry =="
# There is no volume to survive the restart. Everything the replay check knows is
# re-derived by enumerating the contract revision's referrers.
kubectl -n "$NS" delete pod -l app.kubernetes.io/component=evidence --wait=false >/dev/null 2>&1 || \
  kubectl -n "$NS" rollout restart deployment/pacto-evidence >/dev/null
kubectl -n "$NS" rollout status deployment/pacto-evidence --timeout=120s
kill "$EV_PF_PID" 2>/dev/null || true
EV_PF_PID="$(pf "$LOCAL_EV_PORT" svc/pacto-evidence 8686)"
send_rejected "$WORK/env1.json" "replay still rejected after restart (history rebuilt from the registry)"
send_rejected "$WORK/env2.json" "seq-2 replay still rejected after restart"
curl -fsS "http://127.0.0.1:${LOCAL_EV_PORT}/api/evidence/v1/targets" | grep -q '"subject":"checkout"' \
  && pass "the target survived a restart that kept nothing locally" || fail "the target did not survive the restart"

echo "== an unrelated artifact attached to the same revision is ignored =="
# Signatures, SBOMs and attestations share the subject. Reading one as evidence —
# or letting one degrade the read — would make every signed contract look broken.
printf 'not evidence' > "$WORK/other.txt"
oras attach "${ORAS_API[@]}" --artifact-type application/vnd.example.sbom.v1+json \
  "$LOCAL_SUBJECT" "$WORK/other.txt:application/octet-stream" >/dev/null 2>&1 \
  || fail "could not attach an unrelated artifact"
curl -fsS "http://127.0.0.1:${LOCAL_EV_PORT}/api/evidence/v1/targets" | grep -q '"status":"ready"' \
  && pass "an unrelated artifact type leaves the read authoritative" || fail "an unrelated artifact degraded the read"

echo "== interop: Pacto ingests an equivalent record published by the ORAS CLI =="
# The store is a plain OCI registry, so a third party that writes the documented
# media types and payload schema writes evidence Pacto reads. Derived from the
# record Pacto itself published, so the two cannot disagree about the shape.
PUBLISHED="$(oras_evidence_digests | head -1)"
oras manifest fetch --plain-http "$PUBLISHED" \
  | jq -r '.layers[0].digest' > "$WORK/layer.txt"
oras blob fetch --plain-http --output "$WORK/payload.json" "${LOCAL_SUBJECT%@*}@$(cat "$WORK/layer.txt")"
# evidence.EvidenceSet carries no struct tags, so its wire keys are the Go field
# names — Subject, ContractRef. Nothing else may be touched: the payload is
# decoded strictly, so an invented field would make this artifact malformed
# instead of ingestible, and the leg would pass for the wrong reason.
jq '.record.envelope.id = "oras-1"
    | .record.envelope.sequence = 99
    | .record.envelope.evidenceSet.Subject.name = "checkout-canary"' \
  "$WORK/payload.json" > "$WORK/oras-payload.json"
grep -q '"schemaVersion": *"pacto.dev/evidence-record/v1"' "$WORK/oras-payload.json" \
  || fail "the derived payload does not carry the documented schema version"
grep -q 'checkout-canary' "$WORK/oras-payload.json" \
  || fail "the derived payload did not take the canary subject: the EvidenceSet wire shape changed"
oras attach "${ORAS_API[@]}" --artifact-type "$EV_TYPE" \
  "$LOCAL_SUBJECT" "$WORK/oras-payload.json:$EV_TYPE" >/dev/null \
  || fail "ORAS could not publish an equivalent evidence artifact"
oras_has_canary() { curl -fsS "http://127.0.0.1:${LOCAL_EV_PORT}/api/evidence/v1/targets" 2>/dev/null | grep -q 'checkout-canary'; }
eventually 10 oras_has_canary \
  && pass "Pacto reads an evidence record the ORAS CLI published" \
  || fail "the ORAS-published record was not read"

echo "== a malformed Pacto artifact makes reads partial and writes fail closed =="
# Same artifactType, unreadable payload. This is the case where silence would be a
# lie: the honest answer is "this read is incomplete", never "there is nothing".
printf '{not-a-record}' > "$WORK/corrupt.json"
# Identify the malformed artifact by what this attach ADDED, not by where the
# registry happens to list it: referrer order is the registry's choice, and
# deleting the wrong one later would silently invert the final assertion.
oras_evidence_digests | sort > "$WORK/before.txt"
oras attach "${ORAS_API[@]}" --artifact-type "$EV_TYPE" \
  "$LOCAL_SUBJECT" "$WORK/corrupt.json:$EV_TYPE" >/dev/null \
  || fail "could not publish the malformed artifact"
oras_evidence_digests | sort > "$WORK/after.txt"
CORRUPT="$(comm -13 "$WORK/before.txt" "$WORK/after.txt")"
[ "$(printf '%s' "$CORRUPT" | grep -c .)" = "1" ] \
  || fail "expected the attach to add exactly one referrer, got: $CORRUPT"
read_partial() { curl -fsS "http://127.0.0.1:${LOCAL_EV_PORT}/api/evidence/v1/targets" 2>/dev/null | grep -q '"status":"partial"'; }
eventually 15 read_partial \
  && pass "the read reports partial with the unreadable artifact counted" \
  || fail "an unreadable artifact did not degrade the read"
curl -fsS "http://127.0.0.1:${LOCAL_EV_PORT}/api/evidence/v1/targets" | grep -q '"subject":"checkout"' \
  && pass "usable records are still served while the read is partial" || fail "a partial read dropped the usable records"
# And the write path refuses: a replay check run against a history it could not
# fully reconstruct is not a replay check.
sign_envelope 100 e2e-fail-closed "$WORK/env3.json"
send_rejected "$WORK/env3.json" "ingestion fails closed while the history cannot be fully read"

echo "== removing the malformed artifact restores an authoritative read =="
oras manifest delete --plain-http --force "$CORRUPT" >/dev/null
read_ready() { curl -fsS "http://127.0.0.1:${LOCAL_EV_PORT}/api/evidence/v1/targets" 2>/dev/null | grep -q '"status":"ready"'; }
eventually 15 read_ready && pass "the read is authoritative again" || fail "the read stayed partial after the artifact was removed"
send_ok "$WORK/env3.json" "ingestion resumes once the history is fully readable"
kill "$EV_PF_PID" "$DASH_PF_PID" 2>/dev/null || true

echo "== disabling the Evidence Server removes its whole footprint (the evidence is in the registry) =="
helm upgrade pacto-operator "$CHART" -n "$NS" "${common_sets[@]}" --set evidence.enabled=false --wait --timeout 180s
evidence_gone() { ! deploy_exists pacto-evidence; }
eventually 40 evidence_gone && pass "evidence Deployment removed on disable" || fail "evidence Deployment survived disable"
[ -z "$(kubectl -n "$NS" get pvc -o name 2>/dev/null)" ] \
  && pass "there was never a PVC to retain or leak" || fail "a PVC appeared across the disable cycle"
oras_evidence_still_there() { [ "$(oras_evidence_digests | wc -l | tr -d ' ')" -ge 1 ]; }
oras_evidence_still_there \
  && pass "the accepted evidence is untouched in the registry" || fail "disabling the component lost evidence"

echo "== when disabled, the dashboard no longer reports the Evidence source =="
kubectl -n "$NS" rollout status deployment/pacto-dashboard --timeout=120s
DASH_PF_PID="$(pf 8080 svc/pacto-dashboard 3000)"; sleep 2
kubectl -n "$NS" get deploy pacto-dashboard -o jsonpath='{.spec.template.spec.containers[0].env[*].name}' \
  | grep -q PACTO_EVIDENCE_SOURCE_URL \
  && fail "dashboard still wired to the (disabled) Evidence Server" \
  || pass "dashboard drops the Evidence source when the component is disabled"
kill "$DASH_PF_PID" 2>/dev/null || true

echo "== re-enabling rediscovers everything from the registry (nothing was stored in the cluster) =="
helm upgrade pacto-operator "$CHART" -n "$NS" "${common_sets[@]}" --wait --timeout 180s
wait_managed_ready pacto-evidence && pass "evidence Deployment recovered with no local state" || fail "did not recover after re-enable"
EV_PF_PID="$(pf "$LOCAL_EV_PORT" svc/pacto-evidence 8686)"
curl -fsS "http://127.0.0.1:${LOCAL_EV_PORT}/api/evidence/v1/targets" | grep -q '"subject":"checkout"' \
  && pass "previously-ingested evidence survived the disable/re-enable cycle" || fail "evidence did not survive disable/re-enable"
send_rejected "$WORK/env1.json" "replay protection was rebuilt from the registry after re-enable"
kill "$EV_PF_PID" "$REG_PF_PID" 2>/dev/null || true

echo "== full in-cluster Evidence Server lifecycle acceptance PASSED =="
keep_or_teardown "$NS" "$CLUSTER"
