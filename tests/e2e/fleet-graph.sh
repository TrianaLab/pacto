#!/usr/bin/env bash
# Hermetic operational-graph acceptance — no cluster required. Builds the pacto
# binary and drives the whole fleet story end to end against local fixtures:
# assemble a graph from local contracts, sign and ingest external evidence and
# see it as a target, observe runtime dependencies from OTLP traces, reconcile
# declared vs observed, and analyze a breaking change's real blast radius with
# observed corroboration. Every step asserts on real CLI output so a regression
# in any subsystem fails the run. The live-Kubernetes source is exercised
# separately by the kind acceptance (tests/e2e/kind).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
WORK="$(mktemp -d)"
BIN="$WORK/pacto"
trap 'rm -rf "$WORK"; [ -n "${SERVE_PID:-}" ] && kill "$SERVE_PID" 2>/dev/null || true' EXIT

pass() { echo "  PASS: $1"; }
fail() { echo "  FAIL: $1"; exit 1; }
assert_contains() {
  if grep -Fq "$2" <<<"$1"; then
    pass "$3"
  else
    echo "--- output ---"; echo "$1"; fail "$3"
  fi
}

echo "== build pacto =="
go build -o "$BIN" "$ROOT/cmd/pacto"

echo "== fixtures =="
mkdir -p "$WORK/ws/web" "$WORK/ws/payments" "$WORK/pay-old" "$WORK/pay-new"
cat > "$WORK/ws/web/pacto.yaml" <<'EOF'
pactoVersion: "2.0"
service:
  name: web
  version: "1.0.0"
  owner:
    team: frontend
dependencies:
  - name: payments
    ref: oci://x/payments
    required: true
    compatibility: "^1.0.0"
EOF
cat > "$WORK/ws/payments/pacto.yaml" <<'EOF'
pactoVersion: "2.0"
service:
  name: payments
  version: "1.0.0"
  owner:
    team: payments
EOF
cat > "$WORK/traces.json" <<'EOF'
{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"web"}}]},
 "scopeSpans":[{"spans":[
   {"kind":3,"attributes":[{"key":"peer.service","value":{"stringValue":"payments"}}]},
   {"kind":3,"attributes":[{"key":"peer.service","value":{"stringValue":"audit-log"}}]}
 ]}]}]}
EOF
# payments old (declares an interface) -> new (interface removed) = BREAKING.
cat > "$WORK/pay-old/pacto.yaml" <<'EOF'
pactoVersion: "2.0"
service: {name: payments, version: "1.0.0"}
interfaces:
  - name: pay-api
    type: grpc
    ref: pay.proto
EOF
cat > "$WORK/pay-new/pacto.yaml" <<'EOF'
pactoVersion: "2.0"
service: {name: payments, version: "2.0.0"}
EOF
# web is deployed (an active target), so a breaking change to payments blocks.
cat > "$WORK/targets.yaml" <<'EOF'
schemaVersion: pacto.dev/fleet-targets/v1
targets:
  - scope: prod
    kind: kubernetes
    name: web
    service: web
    compliance: Compliant
EOF

echo "== 1. assemble the graph from local contracts =="
OUT="$("$BIN" fleet search --local "$WORK/ws")"
assert_contains "$OUT" "web" "web is in the graph"
assert_contains "$OUT" "payments" "payments is in the graph"

echo "== 2. sign, ingest and surface external evidence as a target =="
"$BIN" evidence keygen --out "$WORK/keys" --key-id demo >/dev/null
cat > "$WORK/ev.json" <<EOF
{"Subject":{"kind":"service","name":"payments"},"ContractRef":"$WORK/ws/payments","Source":"remote","ObservedAt":"2026-07-29T11:00:00Z","Observations":[{"kind":"WorkloadObserved","subject":{"kind":"service","name":"payments"},"outcome":"Unsupported","provenance":{"collector":"remote","detectedAt":"2026-07-29T11:00:00Z"}}]}
EOF
"$BIN" evidence sign --key "$WORK/keys/demo.key" --key-id demo --producer prod-eu --ttl 0 "$WORK/ev.json" > "$WORK/env.json"
VOUT="$("$BIN" evidence verify --trust "$WORK/keys" "$WORK/env.json")"
assert_contains "$VOUT" "is valid" "signed envelope verifies"

PORT=18787
"$BIN" evidence serve --port "$PORT" --trust "$WORK/keys" --store-dir "$WORK/store" --producer prod-eu >/dev/null 2>&1 &
SERVE_PID=$!
for _ in $(seq 1 50); do
  curl -fsS "http://localhost:$PORT/api/evidence/v1/health" >/dev/null 2>&1 && break
  sleep 0.2
done
SEND="$("$BIN" evidence send --url "http://localhost:$PORT/api/evidence/v1/envelopes" "$WORK/env.json")"
assert_contains "$SEND" "accepted" "ingestion accepts the envelope"
FOUT="$("$BIN" fleet search --local "$WORK/ws" --evidence-store "$WORK/store" --scope prod-eu)"
assert_contains "$FOUT" "payments" "ingested evidence appears as a fleet target"
kill "$SERVE_PID" 2>/dev/null || true; SERVE_PID=""

echo "== 3. observe runtime dependencies from traces =="
OOUT="$("$BIN" otel observe "$WORK/traces.json")"
assert_contains "$OOUT" "web -> payments" "declared edge observed"
assert_contains "$OOUT" "web -> audit-log" "undeclared edge observed"

echo "== 4. reconcile declared vs observed =="
ROUT="$("$BIN" fleet reconcile --local "$WORK/ws" --traces "$WORK/traces.json")"
assert_contains "$ROUT" "[matched] web -> payments" "declared+observed dependency matched"
assert_contains "$ROUT" "[observed-not-declared] web -> audit-log" "shadow dependency surfaced"

echo "== 5. impact of a breaking change, corroborated by observed traffic =="
if IOUT="$("$BIN" impact "$WORK/pay-old" "$WORK/pay-new" --local "$WORK/ws" --target-state "$WORK/targets.yaml" --traces "$WORK/traces.json")"; then
  fail "impact should exit non-zero: breaking change hits an incompatible consumer"
else
  pass "impact exits non-zero on a breaking change to an active consumer"
fi
assert_contains "$IOUT" "Classification: BREAKING" "change classified BREAKING"
assert_contains "$IOUT" "confidence=corroborated" "consumer corroborated by declared + observed evidence"
assert_contains "$IOUT" "compat=incompatible" "consumer is incompatible with the new version"

echo "== operational-graph acceptance PASSED =="
