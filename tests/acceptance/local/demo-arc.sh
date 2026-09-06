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

# Beats 5 and 6 (the two `pacto diff` beats) are absent on purpose: today they
# exit 1 on LOCK_DIGEST_MISMATCH with empty stdout, so there is no
# classification line to assert on. The task that deletes the demo lockfiles
# adds them here, between beat 4 and beat 7.

echo "== beat 7: blast radius, declared evidence only =="
OUT="$("$BIN" impact "$B/payments-service/v1.2.0" "$B/payments-service/v2.0.0" --local "$B")" || fail "beat 7: impact failed"
assert_contains "$OUT" "Classification: BREAKING"      "the change is breaking"
assert_contains "$OUT" "Affected consumers (4):"       "four consumers are affected"
assert_contains "$OUT" "confidence=contractual"        "declared consumers are graded contractual"
assert_contains "$OUT" "confidence=inferred"           "transitive consumers are graded inferred"
assert_contains "$OUT" "compat=incompatible"           "a direct consumer is incompatible with the new version"

echo "== demo-arc acceptance PASSED =="
