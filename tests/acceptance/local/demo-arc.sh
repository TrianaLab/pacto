#!/usr/bin/env bash
# Demo-arc acceptance — no Docker, no cluster, no network. Drives the beats the
# guided tour publishes (docs/examples/demo-tour.md) against the committed demo
# fixture and asserts on the real bytes a reader will see, so the tour cannot
# quietly stop being true.
#
# Assertions are on output SUBSTRINGS, not exit codes: a typo'd `pacto fleet`
# subcommand prints help and exits 0, so an exit-code check proves nothing about
# a renamed subcommand.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
WORK="$(mktemp -d)"
BIN="$WORK/pacto"
trap 'rm -rf "$WORK"' EXIT

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

B="$ROOT/examples/demo/bundles"
T="$ROOT/examples/demo/fleet-targets.yaml"
TR="$ROOT/examples/demo/traces.json"

echo "== beat 1: the fleet exists =="
OUT="$("$BIN" fleet search --local "$B")" || fail "beat 1: fleet search failed"
assert_contains "$OUT" "16 of 16 service(s):" "every demo service is in the snapshot"
assert_contains "$OUT" "payments-service" "the service the arc changes is present"

echo "== beat 2: four compliance states at once =="
OUT="$("$BIN" fleet search --local "$B" --target-state "$T")" || fail "beat 2: fleet search failed"
assert_contains "$OUT" "auth-service                 Unknown"      "no evidence reads as Unknown, not as a pass"
assert_contains "$OUT" "fraud-service                Compliant"    "evidenced and conformant reads as Compliant"
assert_contains "$OUT" "orders-service               NonCompliant" "a confirmed violation reads as NonCompliant"
assert_contains "$OUT" "api-gateway                  NotEvaluated" "a service with no target is not evaluated"

echo "== beat 3: Unknown is an absence of evidence, not a violation =="
OUT="$("$BIN" fleet explain identity/auth-service --local "$B" --target-state "$T" --freshness 24h)" || fail "beat 3: fleet explain failed"
assert_contains "$OUT" "target production-eu/kubernetes-workload/identity%2Fauth-service: Unknown" "the named target is Unknown"
assert_contains "$OUT" "[EVIDENCE_MISSING]" "the reason is named, not implied"

echo "== beat 4: NonCompliant at full coverage =="
OUT="$("$BIN" fleet get --target commerce/orders-service --local "$B" --target-state "$T")" || fail "beat 4: fleet get failed"
assert_contains "$OUT" "Compliance: NonCompliant"                 "confirmed violation"
assert_contains "$OUT" "Coverage: 5/5 evaluated"                  "every check was actually evaluated"
assert_contains "$OUT" "STATELESS_PERSISTENT_CONFLICT"            "the specific contradiction is named"

echo "== beat 5: a breaking change, refused =="
# pacto diff exits 1 on BREAKING, so capture without tripping set -e.
if OUT="$("$BIN" diff "$B/payments-service/v1.2.0" "$B/payments-service/v2.0.0" 2>&1)"; then
  fail "diff should exit non-zero on a breaking change"
else
  pass "diff exits non-zero"
fi
assert_contains "$OUT" "Classification: BREAKING" "the change is classified BREAKING"
assert_contains "$OUT" "Changes (28):"            "every change is counted"
assert_contains "$OUT" "dependencies.required (modified)" "a dependency becoming required is caught"

echo "== beat 6: the same field, one release earlier, is only potentially breaking =="
OUT="$("$BIN" diff "$B/payments-service/v1.0.0" "$B/payments-service/v1.1.0")" || fail "beat 6: diff failed"
assert_contains "$OUT" "Classification: POTENTIAL_BREAKING" "an additive change is not a break"
assert_contains "$OUT" "Changes (5):"                       "every change is counted"

echo "== beat 7: blast radius, declared evidence only =="
OUT="$("$BIN" impact "$B/payments-service/v1.2.0" "$B/payments-service/v2.0.0" --local "$B")" || fail "beat 7: impact failed"
assert_contains "$OUT" "Classification: BREAKING"      "the change is breaking"
assert_contains "$OUT" "Affected consumers (4):"       "four consumers are affected"
assert_contains "$OUT" "confidence=contractual"        "declared consumers are graded contractual"
assert_contains "$OUT" "confidence=inferred"           "transitive consumers are graded inferred"
assert_contains "$OUT" "compat=incompatible"           "a direct consumer is incompatible with the new version"

echo "== beat 8: the same change, with runtime evidence =="
OUT="$("$BIN" impact "$B/payments-service/v1.2.0" "$B/payments-service/v2.0.0" --local "$B" --traces "$TR")" || fail "beat 8: impact failed"
assert_contains "$OUT" "Affected consumers (5):" "observed traffic surfaces a consumer nobody declared"
assert_contains "$OUT" "audit-log"               "the shadow consumer is named"
assert_contains "$OUT" "confidence=observed"     "an observed-only consumer is graded observed"
assert_contains "$OUT" "confidence=corroborated" "a declared consumer seen in traffic is upgraded to corroborated"

echo "== beat 9: the same change again, now against live targets =="
if OUT="$("$BIN" impact "$B/payments-service/v1.2.0" "$B/payments-service/v2.0.0" --local "$B" --traces "$TR" --target-state "$T" 2>&1)"; then
  fail "impact should exit non-zero: a breaking change reaches an active consumer"
else
  pass "impact exits non-zero when a breaking change reaches an active consumer"
fi
assert_contains "$OUT" "Active targets" "the active targets are named"

echo "== beat 10: declared versus observed =="
OUT="$("$BIN" fleet reconcile --local "$B" --traces "$TR")" || fail "beat 10: fleet reconcile failed"
assert_contains "$OUT" "[matched] orders-service -> payments-service"              "a declared edge seen in traffic is matched"
assert_contains "$OUT" "[observed-not-declared] audit-log -> payments-service"     "an undeclared edge seen in traffic is surfaced"

echo "== demo-arc acceptance PASSED =="
