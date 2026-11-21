#!/usr/bin/env bash
# Hermetic operational-graph acceptance — no cluster required. Builds the pacto
# binary and drives the whole fleet story end to end against local fixtures:
# assemble a graph from local contracts, sign and ingest external evidence and
# see it as a target, observe runtime dependencies from OTLP traces, reconcile
# declared vs observed, and analyze a breaking change's real blast radius with
# observed corroboration. Every step asserts on real CLI output so a regression
# in any subsystem fails the run. The live-Kubernetes source is exercised
# separately by the kind acceptance (tests/acceptance/kind).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
WORK="$(mktemp -d)"
BIN="$WORK/pacto"
trap 'rm -rf "$WORK"; [ -n "${SERVE_PID:-}" ] && kill "$SERVE_PID" 2>/dev/null; [ -n "${REG_PID:-}" ] && kill "$REG_PID" 2>/dev/null; true' EXIT

pass() { echo "  PASS: $1"; }
fail() { echo "  FAIL: $1"; exit 1; }
assert_contains() {
  if grep -Fq "$2" <<<"$1"; then
    pass "$3"
  else
    echo "--- output ---"; echo "$1"; fail "$3"
  fi
}
assert_absent() {
  if grep -Fq "$2" <<<"$1"; then
    echo "--- output ---"; echo "$1"; fail "$3"
  else
    pass "$3"
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
# Its evidence is fresh: impact reads target evidence older than 24h as unknown.
cat > "$WORK/targets.yaml" <<EOF
schemaVersion: pacto.dev/fleet-targets/v1
targets:
  - scope: prod
    kind: kubernetes
    name: web
    service: web
    compliance: Compliant
    evidenceAt: $(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF
# The same target, last reported two days ago.
STALE_AT="$(date -u -v-2d +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '2 days ago' +%Y-%m-%dT%H:%M:%SZ)"
sed "s/evidenceAt: .*/evidenceAt: $STALE_AT/" "$WORK/targets.yaml" > "$WORK/targets-stale.yaml"

echo "== 1. assemble the graph from local contracts =="
OUT="$("$BIN" fleet search --local "$WORK/ws")"
assert_contains "$OUT" "web" "web is in the graph"
assert_contains "$OUT" "payments" "payments is in the graph"

echo "== 1b. a directory the OS refuses is a gap, not an empty fleet =="
# A local scan meets directories it may not open -- a macOS home directory refuses
# a hundred TCC-guarded paths, a shared checkout has another user's build output.
# Aborting the walk there reported the whole source UNAVAILABLE and answered "0 of
# 0 services" while every readable bundle sat further down, which is what `pacto
# tui` did from a home directory. The refusal must degrade the answer, never empty
# it. Named to sort FIRST, so the walk hits it before either contract: that
# ordering is the whole bug, and a locked directory visited last would pass even
# with the abort restored.
mkdir -p "$WORK/ws/aa-locked"
chmod 000 "$WORK/ws/aa-locked"
if [ "$(id -u)" = "0" ]; then
  # root reads a 0000 directory regardless, so there is no refusal to observe and
  # the assertions below would be vacuous. Say so instead of passing silently.
  echo "  SKIP: running as root, a mode-000 directory is still readable"
  chmod 755 "$WORK/ws/aa-locked"
else
  POUT="$("$BIN" fleet search --local "$WORK/ws" 2>&1)"
  chmod 755 "$WORK/ws/aa-locked"
  assert_contains "$POUT" "2 of 2 service(s)" "both contracts are still found past the refused directory"
  assert_contains "$POUT" "[SOURCE_PARTIAL] could not read aa-locked" "the refusal is reported as a gap"
  assert_absent "$POUT" "SOURCE_UNAVAILABLE" "one refused directory does not make the source unavailable"
  # The limitation is the same text an agent reads over MCP, so it names the
  # directory relative to the scan root rather than echoing the caller's absolute
  # path back at them.
  assert_absent "$POUT" "$WORK/ws/aa-locked" "the gap names the directory relative to the scan root"
fi
rmdir "$WORK/ws/aa-locked"

echo "== 2. sign, ingest and surface external evidence as a target =="
# A remote producer must reference an IMMUTABLE oci digest, not a local path.
# Publish the payments contract to an ephemeral in-memory registry (Go-only, no
# Docker) and reference it by digest — the shape the ingestion policy requires and
# the identity the accepted target links to.
REG_PORT=15071
REG_HOST="127.0.0.1:${REG_PORT}"
go build -o "$WORK/localregistry" "$ROOT/tests/acceptance/local/localregistry"
"$WORK/localregistry" --port "$REG_PORT" >/dev/null 2>&1 &
REG_PID=$!
for _ in $(seq 1 50); do curl -fsS "http://${REG_HOST}/v2/" >/dev/null 2>&1 && break; sleep 0.2; done
PAY_DIGEST="$(PACTO_INSECURE_REGISTRIES="$REG_HOST" "$BIN" push "oci://${REG_HOST}/demo/payments:1.0.0" -p "$WORK/ws/payments" 2>&1 | grep -oE 'sha256:[0-9a-f]{64}' | head -1)"
if [ -n "$PAY_DIGEST" ]; then pass "published payments contract $PAY_DIGEST"; else fail "could not publish the payments contract"; fi
CONTRACT_REF="oci://${REG_HOST}/demo/payments@${PAY_DIGEST}"

# Bind the key to the producer it will sign as, so the trust store authorizes it
# (writes prod-eu__demo.pub).
"$BIN" evidence keygen --out "$WORK/keys" --producer prod-eu --key-id demo >/dev/null
cat > "$WORK/ev.json" <<EOF
{"Subject":{"kind":"service","name":"payments"},"ContractRef":"${CONTRACT_REF}","Source":"remote","ObservedAt":"2026-07-29T11:00:00Z","Observations":[{"kind":"WorkloadObserved","subject":{"kind":"service","name":"payments"},"outcome":"Unsupported","provenance":{"collector":"remote","detectedAt":"2026-07-29T11:00:00Z"}}]}
EOF
sign_env() { # id sequence outfile
  "$BIN" evidence sign --key "$WORK/keys/demo.key" --key-id demo --producer prod-eu \
    --id "$1" --sequence "$2" --ttl 0 "$WORK/ev.json" > "$3"
}
sign_env e1 1 "$WORK/env1.json"
VOUT="$("$BIN" evidence verify --trust "$WORK/keys" "$WORK/env1.json")"
assert_contains "$VOUT" "is valid" "signed envelope verifies"

PORT=18787
API="http://localhost:$PORT/api/evidence/v1"
start_server() {
  # The registry IS the evidence store: the server resolves the contract subject
  # and publishes accepted records as referrers of it, over the ephemeral
  # plain-HTTP registry, so mark it insecure. No local state, no data directory.
  PACTO_INSECURE_REGISTRIES="$REG_HOST" "$BIN" evidence serve --port "$PORT" --trust "$WORK/keys" --subject "$CONTRACT_REF" --producer prod-eu >/dev/null 2>&1 &
  SERVE_PID=$!
}
wait_ready() { # readiness is gated on resolving every subject and enumerating its referrers
  for _ in $(seq 1 50); do curl -fsS "$API/ready" >/dev/null 2>&1 && return 0; sleep 0.2; done
  fail "evidence server did not become ready (subject unresolvable)"
}
stop_server() { kill "$SERVE_PID" 2>/dev/null || true; wait "$SERVE_PID" 2>/dev/null || true; SERVE_PID=""; }

start_server; wait_ready
SEND="$("$BIN" evidence send --url "$API/envelopes" "$WORK/env1.json")"
assert_contains "$SEND" "accepted" "ingestion accepts the envelope"
FOUT="$("$BIN" fleet search --local "$WORK/ws" --evidence-url "http://localhost:$PORT" --scope prod-eu)"
assert_contains "$FOUT" "payments" "ingested evidence appears as a fleet target"

echo "== 2b. durable replay protection survives a restart =="
stop_server
# The replacement server keeps NO local state whatsoever: it rebuilds the accepted
# history by enumerating the contract revision's referrers in the registry.
start_server; wait_ready
if "$BIN" evidence send --url "$API/envelopes" "$WORK/env1.json" >/dev/null 2>&1; then
  fail "a replayed envelope was accepted after restart"
else
  pass "replayed envelope rejected after restart (history reconstructed from the registry)"
fi

echo "== 2c. a newer sequence is accepted, an older one rejected =="
sign_env e2 2 "$WORK/env2.json"
SEND2="$("$BIN" evidence send --url "$API/envelopes" "$WORK/env2.json")"
assert_contains "$SEND2" "accepted" "newer-sequence report accepted after restart"
sign_env e3 1 "$WORK/env3.json"
if "$BIN" evidence send --url "$API/envelopes" "$WORK/env3.json" >/dev/null 2>&1; then
  fail "an out-of-sequence report was accepted"
else
  pass "out-of-sequence report rejected"
fi
stop_server

echo "== 2d. impact finds consumers through the organization index =="
# Every version pushed to the registry writes one entry to <registry>/pacto-index,
# so impact finds the declared consumers of a provider with no --root list.
# pay-old declares pay.proto and push validates the bundle, so the copy carries it.
cp -R "$WORK/pay-old" "$WORK/idx-pay"
printf 'syntax = "proto3";\npackage pay;\n' > "$WORK/idx-pay/pay.proto"
mkdir -p "$WORK/idx-web"
sed "s|ref: oci://x/payments|ref: oci://${REG_HOST}/idx/payments|" "$WORK/ws/web/pacto.yaml" > "$WORK/idx-web/pacto.yaml"
PPUSH="$(PACTO_INSECURE_REGISTRIES="$REG_HOST" "$BIN" push "oci://${REG_HOST}/idx/payments:1.0.0" -p "$WORK/idx-pay" 2>&1)"
assert_contains "$PPUSH" "Index: indexed as ${REG_HOST}/idx/pacto-index:" "payments is indexed"
WPUSH="$(PACTO_INSECURE_REGISTRIES="$REG_HOST" "$BIN" push "oci://${REG_HOST}/idx/web:1.0.0" -p "$WORK/idx-web" 2>&1)"
assert_contains "$WPUSH" "Index: indexed as ${REG_HOST}/idx/pacto-index:" "web is indexed"
n="$(curl -fsS "http://${REG_HOST}/v2/idx/pacto-index/tags/list" | grep -o '"[0-9a-f]\{16\}-1.0.0"' | wc -l | tr -d ' ')"
if [ "$n" = 2 ]; then pass "the index holds one entry per pushed version"; else fail "the index holds $n entries, expected 2"; fi
# An empty --local keeps the workspace out: web is found through the index alone.
mkdir -p "$WORK/empty"
rc=0
IOUT="$(PACTO_INSECURE_REGISTRIES="$REG_HOST" "$BIN" impact "oci://${REG_HOST}/idx/payments:1.0.0" "$WORK/pay-new" --local "$WORK/empty" 2>&1)" || rc=$?
assert_contains "$IOUT" "Consumers: 1 from ${REG_HOST}/idx/pacto-index (listed by tag, 2 services listed, unchecked)" "the index read is reported unchecked"
assert_contains "$IOUT" "path: ${REG_HOST}%2Fidx/web > " "the index finds web"
assert_contains "$IOUT" "Classification: BREAKING" "the change is breaking"
if [ "$rc" = 2 ]; then pass "an unchecked index never clears the change"; else echo "--- output ---"; echo "$IOUT"; fail "impact exited $rc, expected 2"; fi
rc=0
NOUT="$(PACTO_INSECURE_REGISTRIES="$REG_HOST" "$BIN" impact "oci://${REG_HOST}/idx/payments:1.0.0" "$WORK/pay-new" --local "$WORK/empty" --index none 2>&1)" || rc=$?
assert_contains "$NOUT" "organization index not read (--index none)" "--index none reads no index"
assert_contains "$NOUT" "No affected consumers." "without the index web is not found"
[ "$rc" = 0 ] || [ "$rc" = 2 ] || { echo "--- output ---"; echo "$NOUT"; fail "impact --index none exited $rc"; }
pass "impact finds the consumers published to the organization index with no --root"

echo "== 2e. impact resolves a 2.5 consumer that names its provider only =="
# A 2.5 dependency may leave out ref: Pacto resolves it by name in the
# consumer's registry domain, so impact still finds the consumer.
mkdir -p "$WORK/byname-ledger" "$WORK/byname-reports" "$WORK/byname-new"
printf 'pactoVersion: "2.5"\nservice: {name: ledger, version: "1.0.0"}\ninterfaces:\n  - name: api\n    type: grpc\n    ref: ledger.proto\n' > "$WORK/byname-ledger/pacto.yaml"
printf 'syntax = "proto3";\npackage ledger;\n' > "$WORK/byname-ledger/ledger.proto"
printf 'pactoVersion: "2.5"\nservice: {name: reports, version: "1.0.0"}\ndependencies:\n  - name: ledger\n' > "$WORK/byname-reports/pacto.yaml"
printf 'pactoVersion: "2.5"\nservice: {name: ledger, version: "2.0.0"}\n' > "$WORK/byname-new/pacto.yaml"
PACTO_INSECURE_REGISTRIES="$REG_HOST" "$BIN" push "oci://${REG_HOST}/byname/ledger:1.0.0" -p "$WORK/byname-ledger" >/dev/null 2>&1 || fail "push byname/ledger"
PACTO_INSECURE_REGISTRIES="$REG_HOST" "$BIN" push "oci://${REG_HOST}/byname/reports:1.0.0" -p "$WORK/byname-reports" >/dev/null 2>&1 || fail "push byname/reports"
rc=0
BOUT="$(PACTO_INSECURE_REGISTRIES="$REG_HOST" "$BIN" impact "oci://${REG_HOST}/byname/ledger:1.0.0" "$WORK/byname-new" --local "$WORK/empty" 2>&1)" || rc=$?
assert_contains "$BOUT" "Classification: BREAKING" "dropping ledger's interface is breaking"
assert_contains "$BOUT" "path: ${REG_HOST}%2Fbyname/reports > " "impact finds reports, which names ledger only"
[ "$rc" = 1 ] || [ "$rc" = 2 ] || { echo "--- output ---"; echo "$BOUT"; fail "impact exited $rc, expected 1 or 2"; }
pass "impact resolves a dependency by name"

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

echo "== 5b. target evidence older than 24h makes the consumer's confidence unknown =="
SOUT="$("$BIN" impact "$WORK/pay-old" "$WORK/pay-new" --local "$WORK/ws" --target-state "$WORK/targets-stale.yaml" --traces "$WORK/traces.json" || true)"
assert_contains "$SOUT" "confidence=unknown" "stale target evidence lowers confidence to unknown"

echo "== 6. two clusters keep two targets; an old key names both =="
# Two target-state files describe two clusters that each run prod/payments.
for c in eu us; do
  cat > "$WORK/cluster-$c.yaml" <<EOF
schemaVersion: pacto.dev/fleet-targets/v1
cluster: {id: uid-$c, name: $c-prod}
targets:
  - {scope: prod, kind: kubernetes, name: payments, service: payments, compliance: Compliant}
EOF
done
TWO=(--target-state "$WORK/cluster-eu.yaml" --target-state "$WORK/cluster-us.yaml")
if MOUT="$("$BIN" fleet entity target prod/kubernetes/payments "${TWO[@]}" 2>&1)"; then
  echo "--- output ---"; echo "$MOUT"; fail "a 3-part key that names two clusters must not resolve"
else
  pass "a 3-part key that names two clusters exits non-zero"
fi
assert_contains "$MOUT" "eu-prod / prod / payments   uid-eu/prod/kubernetes/payments" "the eu candidate is listed"
assert_contains "$MOUT" "us-prod / prod / payments   uid-us/prod/kubernetes/payments" "the us candidate is listed"
COUT="$("$BIN" fleet entity target uid-eu/prod/kubernetes/payments "${TWO[@]}")"
assert_contains "$COUT" "Key: uid-eu/prod/kubernetes/payments" "the cluster-qualified key answers"
OOUT1="$("$BIN" fleet entity target prod/kubernetes/payments --target-state "$WORK/cluster-eu.yaml")"
assert_contains "$OOUT1" "Key: prod/kubernetes/payments" "one cluster keeps the 3-part key"

echo "== 6b. status names each cluster; the cluster filter keeps one =="
SOUT2="$("$BIN" fleet status --local "$WORK/empty" "${TWO[@]}")"
assert_contains "$SOUT2" "Clusters: eu-prod (uid-eu) complete, us-prod (uid-us) complete" "status prints the Clusters line with the UIDs --cluster takes"
EOUT="$("$BIN" fleet entities --kind target --cluster uid-eu --local "$WORK/empty" "${TWO[@]}")"
assert_contains "$EOUT" "1 of 1 entit(ies):" "the cluster filter lists one target"
assert_contains "$EOUT" "uid-eu/prod/kubernetes/payments" "the eu target keeps its 4-part key"
assert_absent "$EOUT" "uid-us/" "the us target is filtered out"

echo "== operational-graph acceptance PASSED =="
