#!/usr/bin/env bash
# Local acceptance for `pacto run`: a command wrapped by pacto and checked
# against its contract while it runs, in both modes, on a real kernel.
#
# Everything runs inside containers built from the production runtime image
# (root Dockerfile) plus the agnhost test binary, so the same script proves the
# same thing from a Linux CI runner and from Docker Desktop on macOS:
#
#   - privileged (--privileged, its own cgroup namespace): pacto creates the
#     session cgroup pacto-run-<pid>, starts the command inside it and observes
#     it with eBPF. A served interface is Compliant (exit 0 under --fail-on
#     noncompliant), a command that never listens is INTERFACE_ABSENT (exit 1),
#     a refused dependency is DEPENDENCY_UNREACHABLE and a reached one
#     Compliant, all cited to collector pacto-run+ebpf. SIGTERM reaches the
#     command and the final round comes from the view taken before it exited;
#   - the command's stdout is its own: rounds go to stderr, or with --report
#     to a file (one JSON object per line, whole rounds only);
#   - unprivileged (--user 1000, no capabilities): pacto says it falls back to
#     polling /proc. A served interface is still Compliant and a deaf one still
#     INTERFACE_ABSENT (the listener view of the tree is complete), cited to
#     collector pacto-run; a refused dependency is NOT confirmed unreachable
#     (this mode never sees a failed connection), and a connection held open is
#     seen as reachable.
#
# Assertions over the JSON rounds are a typed decode in
# tests/acceptance/local/runtimecheck. Every scenario runs; the summary names
# each PASS or FAIL. Everything this script creates carries RUN_ID and is
# removed on exit.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
AGNHOST="registry.k8s.io/e2e-test-images/agnhost:2.53"
RUN_ID="$(od -An -N3 -tx1 /dev/urandom | tr -d ' \n')"
RUNTIME_IMG="pacto-runtime-acceptance:$RUN_ID"
IMG="pacto-run-acceptance:$RUN_ID"
FIXTURES="$ROOT/tests/acceptance/local/runtime-fixtures"
WORK="$(mktemp -d)"

SCENARIOS=()
FAILED=()
CURRENT=""
START=$SECONDS
CONTAINERS=()

scenario() {
  CURRENT="$1"
  SCENARIOS+=("$1")
  echo "== scenario $1: $2 =="
}

miss() {
  echo "  FAIL: $*" >&2
  FAILED+=("${CURRENT:-setup}")
}

summary() {
  local id verdict
  echo "== runtime-run summary ($((SECONDS - START))s) =="
  for id in "${SCENARIOS[@]}"; do
    verdict=PASS
    [[ " ${FAILED[*]:-} " == *" $id "* ]] && verdict=FAIL
    echo "  $verdict  $id"
  done
}

cleanup() {
  local rc=$?
  [ ${#SCENARIOS[@]} -gt 0 ] && summary
  for c in "${CONTAINERS[@]:-}"; do [ -n "$c" ] && docker rm -f "$c" >/dev/null 2>&1; done
  docker rmi "$IMG" "$RUNTIME_IMG" >/dev/null 2>&1 || true
  rm -rf "$WORK"
  [ $rc -eq 0 ] && [ ${#FAILED[@]} -gt 0 ] && rc=1
  exit $rc
}
trap cleanup EXIT

echo "== build the runtime image and the test image =="
docker build --load -q -f "$ROOT/Dockerfile" -t "$RUNTIME_IMG" "$ROOT" >/dev/null
"$ROOT/release/scripts/retry.sh" docker pull -q "$AGNHOST" >/dev/null
mkdir -p "$WORK/ctx"
printf 'FROM %s AS agnhost\nFROM %s\nCOPY --from=agnhost /agnhost /usr/local/bin/agnhost\n' "$AGNHOST" "$RUNTIME_IMG" > "$WORK/ctx/Dockerfile"
docker build --load -q -t "$IMG" "$WORK/ctx" >/dev/null
CHECK="$WORK/runtimecheck"
( cd "$ROOT" && go build -o "$CHECK" ./tests/acceptance/local/runtimecheck )

# The two ways the run container starts. Privileged gets its own cgroup
# namespace, so the session cgroup lands in the container's own subtree.
PRIVILEGED=(--privileged --cgroupns=private --user 0)
UNPRIVILEGED=(--user 1000 --cap-drop ALL --security-opt no-new-privileges)

# --report writes here; world-writable so the unprivileged user can too.
REPORTS="$WORK/reports"
mkdir -p "$REPORTS" && chmod 777 "$REPORTS"

OUT="" ERR="" STDOUT="" RC=0
# pacto_run privileged|unprivileged PACTO-RUN-ARGS... — run to completion,
# capturing the command's stdout, the rounds and notices (stderr) and the exit
# code. The rounds are checked from stderr, where the run writes them.
pacto_run() {
  local mode=("${UNPRIVILEGED[@]}")
  [ "$1" = privileged ] && mode=("${PRIVILEGED[@]}")
  shift
  RC=0
  docker run --rm "${mode[@]}" -v "$FIXTURES:/fixtures:ro" -v "$REPORTS:/reports" "$IMG" run --output-format json \
    --interval 1s --stabilization-window 3s "$@" >"$WORK/out" 2>"$WORK/err" || RC=$?
  STDOUT="$(cat "$WORK/out")" ERR="$(cat "$WORK/err")" OUT="$ERR"
}

# report FILE — check the rounds of a --report file instead of stderr.
report() { OUT="$(cat "$REPORTS/$1")"; }

stdout_is() { # WANT — the command's own stdout, nothing of pacto's
  if [ "$STDOUT" = "$1" ]; then echo "  PASS: stdout is the command's only"; else miss "stdout $(printf %q "$STDOUT"), want $(printf %q "$1")"; fi
}

# holds CHECKER-FLAGS... — the run's rounds satisfy the flags.
holds() {
  local got
  if got="$("$CHECK" "$@" <<<"$OUT" 2>&1)"; then echo "  PASS: $*"; else miss "$got"; fi
}

exits() { # WANT
  if [ "$RC" = "$1" ]; then echo "  PASS: exit $1"; else miss "exit $RC, want $1"$'\n'"$ERR"; fi
}

says() { # SUBSTRING — the run's notices
  if [[ "$ERR" == *"$1"* ]]; then echo "  PASS: says \"$1\""; else miss "stderr lacks \"$1\":"$'\n'"$ERR"; fi
}

SERVE='agnhost netexec --http-port=8080 >/dev/null 2>&1 & echo served; sleep 6'
# A dependency on 8081 served inside the command itself, dialled every half second.
DIAL_OK='agnhost netexec --http-port=8081 >/dev/null 2>&1 & sleep 1; for i in 1 2 3 4 5 6 7 8 9 10; do agnhost connect 127.0.0.1:8081 --timeout=1s >/dev/null 2>&1; sleep 0.5; done'
DIAL_REFUSED='for i in 1 2 3 4 5 6 7 8 9 10 11 12; do agnhost connect 127.0.0.1:9 --timeout=1s >/dev/null 2>&1; sleep 0.5; done'
# The unprivileged mode only sees a connection open at a poll: hold one.
HOLD_OK='agnhost netexec --http-port=8081 >/dev/null 2>&1 & sleep 1; sleep 6 | nc 127.0.0.1 8081 >/dev/null'

scenario run-ebpf-served "privileged: a served interface is Compliant and the run exits 0"
pacto_run privileged --contract /fixtures/served --bind api=8080 --fail-on noncompliant -- sh -c "$SERVE"
exits 0
says "with eBPF (collector pacto-run+ebpf)"
stdout_is served
holds -mode ebpf -status Compliant -covered

scenario run-report "--report: the rounds go to the file, whole, and nowhere else"
pacto_run privileged --contract /fixtures/served --bind api=8080 --fail-on noncompliant --report /reports/served.jsonl -- sh -c "$SERVE"
exits 0
stdout_is served
if "$CHECK" -mode ebpf <<<"$ERR" >/dev/null 2>&1; then miss "stderr carries rounds despite --report"; else echo "  PASS: no round on stderr"; fi
report served.jsonl
holds -mode ebpf -status Compliant -covered

scenario run-ebpf-deaf "privileged: a command that never listens is INTERFACE_ABSENT and fails the run"
pacto_run privileged --contract /fixtures/served --bind api=8080 --fail-on noncompliant -- sleep 8
exits 1
holds -mode ebpf -status NonCompliant -expect INTERFACE_ABSENT:interface/api -collector INTERFACE_ABSENT=pacto-run+ebpf

scenario run-ebpf-dependency-refused "privileged: a refused dependency is DEPENDENCY_UNREACHABLE"
pacto_run privileged --contract /fixtures/client --dep backend=127.0.0.1:9 -- sh -c "$DIAL_REFUSED"
exits 0
holds -mode ebpf -status NonCompliant -expect DEPENDENCY_UNREACHABLE:dependency/backend -collector DEPENDENCY_UNREACHABLE=pacto-run+ebpf

scenario run-ebpf-dependency-reachable "privileged: a reached dependency is Compliant"
pacto_run privileged --contract /fixtures/client --dep backend=127.0.0.1:8081 --fail-on unknown -- sh -c "$DIAL_OK"
exits 0
holds -mode ebpf -status Compliant -covered

scenario run-ebpf-sigterm "privileged: SIGTERM reaches the command and the final round predates its exit"
NAME="pacto-run-sig-$RUN_ID"
docker run -d --name "$NAME" "${PRIVILEGED[@]}" -v "$FIXTURES:/fixtures:ro" "$IMG" run --output-format json \
  --interval 1s --stabilization-window 3s --contract /fixtures/served --bind api=8080 --fail-on noncompliant -- \
  sh -c 'trap "exit 0" TERM; agnhost netexec --http-port=8080 >/dev/null 2>&1 & while :; do sleep 0.2; done' >/dev/null
CONTAINERS+=("$NAME")
sleep 6
docker stop -t 20 "$NAME" >/dev/null
RC="$(docker inspect -f '{{.State.ExitCode}}' "$NAME")"
STDOUT="$(docker logs "$NAME" 2>"$WORK/err")" ERR="$(cat "$WORK/err")" OUT="$ERR"
exits 0
holds -mode ebpf -status Compliant -covered

scenario run-unprivileged-served "unprivileged: says so, and a served interface is still Compliant"
pacto_run unprivileged --contract /fixtures/served --bind api=8080 --fail-on noncompliant -- sh -c "$SERVE"
exits 0
says "unprivileged mode"
says "never unreachable"
stdout_is served
holds -mode unprivileged -status Compliant -covered

scenario run-unprivileged-deaf "unprivileged: the complete listener view still confirms INTERFACE_ABSENT"
pacto_run unprivileged --contract /fixtures/served --bind api=8080 --fail-on noncompliant -- sleep 8
exits 1
holds -mode unprivileged -status NonCompliant -expect INTERFACE_ABSENT:interface/api -collector INTERFACE_ABSENT=pacto-run

scenario run-unprivileged-dependency-refused "unprivileged: a refused dependency is never confirmed unreachable"
pacto_run unprivileged --contract /fixtures/client --dep backend=127.0.0.1:9 --fail-on noncompliant -- sh -c "$DIAL_REFUSED"
exits 0
holds -mode unprivileged -status Unknown -forbid DEPENDENCY_UNREACHABLE -expect EVIDENCE_INSUFFICIENT:dependency/backend

scenario run-unprivileged-dependency-reachable "unprivileged: a connection open at a poll is reachable"
pacto_run unprivileged --contract /fixtures/client --dep backend=127.0.0.1:8081 --fail-on unknown -- sh -c "$HOLD_OK"
exits 0
holds -mode unprivileged -status Compliant -covered
CURRENT=""
