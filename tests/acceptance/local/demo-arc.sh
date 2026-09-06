#!/usr/bin/env bash
# Demo-arc acceptance — no Docker, no cluster, no network. Drives the beats the
# guided tour publishes (docs/examples/demo-tour.md) against the committed demo
# fixture and asserts on the real bytes a reader will see, so the tour cannot
# quietly stop being true.
#
# Assertions are on output SUBSTRINGS, not exit codes: a typo'd `pacto fleet`
# subcommand prints help and exits 0, so an exit-code check proves nothing about
# a renamed subcommand.
#
# beat_args returns an ARGUMENT LIST, not one argument, so every call site is
# deliberately word-split. No path in beats.sh contains a space, and it says so.
# shellcheck disable=SC2046
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
cd "$ROOT"
# The beat commands themselves live in examples/demo/beats.sh, shared with the
# transcript generator so the tour publishes the same invocation this asserts on.
# shellcheck source=../../../examples/demo/beats.sh
. "$ROOT/examples/demo/beats.sh"
WORK="$(mktemp -d)"
BIN="$WORK/pacto"
DASH=""  # dashboard PID once beat 12 starts one
cleanup() {
  if [ -n "$DASH" ]; then kill "$DASH" 2>/dev/null || true; fi
  rm -rf "$WORK"
}
trap cleanup EXIT

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

echo "== beat 1: the fleet exists =="
OUT="$("$BIN" $(beat_args 1))" || fail "beat 1: fleet search failed"
assert_contains "$OUT" "16 of 16 service(s):" "every demo service is in the snapshot"
assert_contains "$OUT" "payments-service" "the service the arc changes is present"

echo "== beat 2: four compliance states at once =="
OUT="$("$BIN" $(beat_args 2))" || fail "beat 2: fleet search failed"
assert_contains "$OUT" "auth-service                 Unknown"      "no evidence reads as Unknown, not as a pass"
assert_contains "$OUT" "fraud-service                Compliant"    "evidenced and conformant reads as Compliant"
assert_contains "$OUT" "orders-service               NonCompliant" "a confirmed violation reads as NonCompliant"
assert_contains "$OUT" "api-gateway                  NotEvaluated" "a service with no target is not evaluated"

echo "== beat 3: Unknown is an absence of evidence, not a violation =="
OUT="$("$BIN" $(beat_args 3))" || fail "beat 3: fleet explain failed"
assert_contains "$OUT" "target production-eu/kubernetes-workload/identity%2Fauth-service: Unknown" "the named target is Unknown"
assert_contains "$OUT" "[EVIDENCE_MISSING]" "the reason is named, not implied"

# ...and the same lesson by a second mechanism: api-gateway/v1.2.0's readiness
# assessment expired, so five claims all marked done earn nothing. Fail closed.
OUT="$("$BIN" $(beat_args 3-readiness))" || fail "beat 3: explain failed"
assert_contains "$OUT" "Expires: 2025-01-01 (EXPIRED)" "an expired readiness assessment is marked expired"
assert_contains "$OUT" "Earned Weight: 0" "an expired assessment earns nothing, fail closed"
assert_contains "$OUT" "Status: 5 done" "every claim is done and it still earns nothing"

echo "== beat 4: NonCompliant at full coverage =="
OUT="$("$BIN" $(beat_args 4))" || fail "beat 4: fleet get failed"
assert_contains "$OUT" "Compliance: NonCompliant"                 "confirmed violation"
assert_contains "$OUT" "Coverage: 5/5 evaluated"                  "every check was actually evaluated"
assert_contains "$OUT" "STATELESS_PERSISTENT_CONFLICT"            "the specific contradiction is named"

echo "== beat 5: a breaking change, refused =="
# pacto diff exits 1 on BREAKING, so capture without tripping set -e.
if OUT="$("$BIN" $(beat_args 5) 2>&1)"; then
  fail "diff should exit non-zero on a breaking change"
else
  pass "diff exits non-zero"
fi
assert_contains "$OUT" "Classification: BREAKING" "the change is classified BREAKING"
assert_contains "$OUT" "Changes (30):"            "every change is counted"
assert_contains "$OUT" "dependencies.required (modified)" "a dependency becoming required is caught"
assert_contains "$OUT" "capabilities (removed)"   "a removed capability is caught"
assert_contains "$OUT" "SBOM changes (1):"        "SBOM changes are detected"

echo "== beat 6: the same field, one release earlier, is only potentially breaking =="
OUT="$("$BIN" $(beat_args 6))" || fail "beat 6: diff failed"
assert_contains "$OUT" "Classification: POTENTIAL_BREAKING" "an additive change is not a break"
assert_contains "$OUT" "Changes (5):"                       "every change is counted"

echo "== beat 7: blast radius, declared evidence only =="
OUT="$("$BIN" $(beat_args 7))" || fail "beat 7: impact failed"
assert_contains "$OUT" "Classification: BREAKING"      "the change is breaking"
assert_contains "$OUT" "Affected consumers (4):"       "four consumers are affected"
assert_contains "$OUT" "confidence=contractual"        "declared consumers are graded contractual"
assert_contains "$OUT" "confidence=inferred"           "transitive consumers are graded inferred"
assert_contains "$OUT" "compat=incompatible"           "a direct consumer is incompatible with the new version"

echo "== beat 8: the same change, with runtime evidence =="
OUT="$("$BIN" $(beat_args 8))" || fail "beat 8: impact failed"
assert_contains "$OUT" "Affected consumers (5):" "observed traffic surfaces a consumer nobody declared"
assert_contains "$OUT" "audit-log"               "the shadow consumer is named"
assert_contains "$OUT" "confidence=observed"     "an observed-only consumer is graded observed"
assert_contains "$OUT" "confidence=corroborated" "a declared consumer seen in traffic is upgraded to corroborated"

echo "== beat 9: the same change again, now against live targets =="
if OUT="$("$BIN" $(beat_args 9) 2>&1)"; then
  fail "impact should exit non-zero: a breaking change reaches an active consumer"
else
  pass "impact exits non-zero when a breaking change reaches an active consumer"
fi
assert_contains "$OUT" "Active targets" "the active targets are named"

echo "== beat 10: declared versus observed =="
OUT="$("$BIN" $(beat_args 10))" || fail "beat 10: fleet reconcile failed"
assert_contains "$OUT" "[matched] orders-service -> payments-service"              "a declared edge seen in traffic is matched"
assert_contains "$OUT" "[observed-not-declared] audit-log -> payments-service"     "an undeclared edge seen in traffic is surfaced"

echo "== beat 11: an agent gets read tools until someone says otherwise =="
# `pacto mcp` exits 1 when stdin reaches EOF, so both captures end in `|| true`:
# the exit code says nothing about the write gate, only the stderr line does.
# Do not "fix" these into exit-code checks.
REQ='{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
RO_ERR="$(echo "$REQ" | "$BIN" $(beat_args 11) 2>&1 >/dev/null || true)"
assert_contains "$RO_ERR" 'skipped 5 mutating operation(s) in interface "http"' \
  "mutating operations are withheld by default, and the CLI says so"
RW_ERR="$(echo "$REQ" | "$BIN" $(beat_args 11-writes) 2>&1 >/dev/null || true)"
if grep -Fq "skipped" <<<"$RW_ERR"; then
  echo "$RW_ERR"; fail "--allow-writes should stop withholding the mutating operations"
else
  pass "--allow-writes exposes them and drops the warning"
fi

echo "== beat 12: a human and an agent read the same fleet =="
# The dashboard is itself a Pacto bundle with a real OpenAPI contract, so the
# agent's tools come from the same server the human is looking at.
DB="examples/demo/pacto-dashboard"
PORT=8899
# Refuse to run against a stranger. Our dashboard prints "running at" and probes
# its sources before it binds, so a squatter on this port answers /health long
# before our own process dies on the failed bind — the liveness check below
# cannot win that race, and every assertion would pass against the wrong server.
if curl -fsS --max-time 2 "http://127.0.0.1:$PORT/health" >/dev/null 2>&1; then
  fail "beat 12: something is already serving on port $PORT — beat 12 needs it free"
fi
"$BIN" dashboard examples/demo/bundles --port "$PORT" >"$WORK/dashboard.log" 2>&1 &
DASH=$!
READY=""
# 30s, not 10s: the dashboard probes for a cluster and for OCI repositories
# before it binds, and on a machine that has both, 10s is not always enough.
for _ in $(seq 1 120); do
  # Liveness before readiness: if the port is already taken our server exits on a
  # failed bind and something else answers /health, so polling alone would assert
  # against a stranger's process and pass.
  kill -0 "$DASH" 2>/dev/null || { cat "$WORK/dashboard.log"; fail "beat 12: the dashboard exited before serving (is port $PORT taken?)"; }
  curl -fsS --max-time 2 "http://127.0.0.1:$PORT/health" >/dev/null 2>&1 && { READY=1; break; }
  sleep 0.25
done
if [ -z "$READY" ]; then
  cat "$WORK/dashboard.log"
  fail "beat 12: the dashboard never answered /health on port $PORT (is the port taken?)"
fi
pass "the dashboard the human reads is serving"

# The MCP server answers nothing before the initialize handshake, and stdin must
# stay open past the call or the server sees EOF and closes before flushing —
# hence the trailing sleep inside the brace group, and the `|| true` on the EOF
# exit as in beat 11.
MCP_OUT="$({ printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"demo-arc","version":"1"}}}'
             printf '%s\n' '{"jsonrpc":"2.0","method":"notifications/initialized"}'
             printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"health","arguments":{}}}'
             sleep 2; } | "$BIN" mcp "$DB" --base-url "http://127.0.0.1:$PORT" 2>/dev/null || true)"
# Single-quoted: the tool result is JSON inside JSON, so the bytes on the wire
# carry the escaping. The Date header and the version string are not assertable.
assert_contains "$MCP_OUT" '\"StatusCode\": 200'         "the agent's tool call reaches the same live server"
assert_contains "$MCP_OUT" '\\\"status\\\":\\\"ok\\\"'   "and gets that server's real health body back"

kill "$DASH" 2>/dev/null || true
wait "$DASH" 2>/dev/null || true
DASH=""

echo "== demo-arc acceptance PASSED =="
