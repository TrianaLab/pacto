#!/usr/bin/env bash
# Local acceptance for the host agent on plain Docker: `pacto agent --sink local
# --docker` in a privileged container watches every container on the daemon's
# kernel with eBPF, reads the contracts and bindings each one declares in its
# labels (dev.pacto.contract, dev.pacto.bind.<interface>, dev.pacto.dep.<name>)
# and prints every container's compliance each round.
#
# What it proves on the local Docker daemon (Docker Desktop's VM or a CI
# runner's kernel), nothing mocked:
#   - a container serving its bound interface is Compliant with full coverage,
#     in its own network namespace and in the host's (where its listeners are
#     told apart by its processes' socket descriptors);
#   - a container that never listens on its bound port is INTERFACE_ABSENT,
#     cited to collector pacto-agent+ebpf, in both network modes;
#   - a container whose declared dependency refuses it is
#     DEPENDENCY_UNREACHABLE, and one that reaches it is Compliant;
#   - a container that reaches another labelled container its contract does
#     not declare reports DEPENDENCY_UNDECLARED named by that container's
#     contract service, while the one that declares it reports none.
#
# Assertions over the JSON rounds are a typed decode in
# tests/acceptance/local/runtimecheck. Every scenario runs; the summary names
# each PASS or FAIL. Everything this script creates carries RUN_ID and is
# removed on exit.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
AGNHOST="registry.k8s.io/e2e-test-images/agnhost:2.53"
RUN_ID="$(od -An -N3 -tx1 /dev/urandom | tr -d ' \n')"
IMG="pacto-runtime-acceptance:$RUN_ID"
FIXTURES="$ROOT/tests/acceptance/local/runtime-fixtures"
WORK="$(mktemp -d)"
# Host-network ports from the run id, so two runs on one daemon do not collide.
HOST_PORT=$((20000 + 0x$RUN_ID % 20000))
HOST_DEAF_PORT=$((HOST_PORT + 1))

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
  echo "== runtime-docker summary ($((SECONDS - START))s) =="
  for id in "${SCENARIOS[@]}"; do
    verdict=PASS
    [[ " ${FAILED[*]:-} " == *" $id "* ]] && verdict=FAIL
    echo "  $verdict  $id"
  done
}

cleanup() {
  local rc=$?
  [ ${#SCENARIOS[@]} -gt 0 ] && summary
  if [ ${#FAILED[@]} -gt 0 ] || [ $rc -ne 0 ]; then
    echo "== agent stderr (last 40 lines) =="
    docker logs "agent-$RUN_ID" 2>&1 >/dev/null | tail -40 || true
  fi
  for c in "${CONTAINERS[@]:-}"; do [ -n "$c" ] && docker rm -f "$c" >/dev/null 2>&1; done
  docker rmi "$IMG" >/dev/null 2>&1 || true
  rm -rf "$WORK"
  [ $rc -eq 0 ] && [ ${#FAILED[@]} -gt 0 ] && rc=1
  # bash 3.2 reports 0 in this trap after a set -u abort: a run that never reached the end failed.
  [ $rc -eq 0 ] && [ -z "${DONE:-}" ] && rc=1
  exit $rc
}
trap cleanup EXIT

echo "== build the runtime image =="
docker build --load -q -f "$ROOT/Dockerfile" -t "$IMG" "$ROOT" >/dev/null
"$ROOT/release/scripts/retry.sh" docker pull -q "$AGNHOST" >/dev/null
CHECK="$WORK/runtimecheck"
( cd "$ROOT" && go build -o "$CHECK" ./tests/acceptance/local/runtimecheck )

# workload NAME CONTRACT [docker run args...] -- COMMAND... — a labelled agnhost container.
workload() {
  local name="$1-$RUN_ID" contract="$2"; shift 2
  local args=()
  while [ "$1" != "--" ]; do args+=("$1"); shift; done
  shift
  docker run -d --name "$name" --label "dev.pacto.contract=/fixtures/$contract" ${args[@]+"${args[@]}"} \
    --entrypoint sh "$AGNHOST" -c "$*" >/dev/null
  CONTAINERS+=("$name")
}

echo "== start the workloads and the host agent =="
workload served served --label dev.pacto.bind.api=8080 -- /agnhost netexec --http-port=8080
workload deaf served --label dev.pacto.bind.api=8080 -- /agnhost pause
workload hostserved served --network host --label "dev.pacto.bind.api=$HOST_PORT" -- /agnhost netexec --http-port="$HOST_PORT"
workload hostdeaf served --network host --label "dev.pacto.bind.api=$HOST_DEAF_PORT" -- /agnhost pause
BACKEND="$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "served-$RUN_ID")"
workload client client --label "dev.pacto.dep.backend=$BACKEND:8080" -- \
  "while :; do /agnhost connect $BACKEND:8080 --timeout=1s >/dev/null 2>&1; sleep 1; done"
workload refused client --label "dev.pacto.dep.backend=$BACKEND:9" -- \
  "while :; do /agnhost connect $BACKEND:9 --timeout=1s >/dev/null 2>&1; sleep 1; done"
workload stray stray -- \
  "while :; do /agnhost connect $BACKEND:8080 --timeout=1s >/dev/null 2>&1; sleep 1; done"

docker run -d --name "agent-$RUN_ID" --privileged --pid=host --cgroupns=host --user 0 \
  -v /var/run/docker.sock:/var/run/docker.sock -v /sys/fs/cgroup:/host/sys/fs/cgroup:ro \
  -v "$FIXTURES:/fixtures:ro" "$IMG" agent --sink local --docker --cgroup-root /host/sys/fs/cgroup \
  --output-format json --publish-interval 3s --resync-interval 3s --stabilization-window 6s >/dev/null
CONTAINERS+=("agent-$RUN_ID")

LAST=""
_holds() { # CHECKER-FLAGS...
  LAST="$(docker logs "agent-$RUN_ID" 2>/dev/null | "$CHECK" "$@" 2>&1)"
}

# expect ROUNDS CHECKER-FLAGS... — the agent's rounds eventually satisfy the flags.
expect() {
  local rounds="$1" i; shift
  for ((i = 0; i < rounds; i++)); do
    if _holds "$@"; then echo "  PASS: $*"; return; fi
    sleep 3
  done
  miss "$LAST"
}

scenario docker-served "a container serving its bound interface is Compliant"
expect 20 -target "container/served-$RUN_ID" -status Compliant -covered

scenario docker-deaf "a container that never listens is INTERFACE_ABSENT"
expect 20 -target "container/deaf-$RUN_ID" -status NonCompliant -expect INTERFACE_ABSENT:interface/api \
  -collector INTERFACE_ABSENT=pacto-agent+ebpf

scenario docker-hostnet-served "a host-network container serving its port is Compliant"
expect 20 -target "container/hostserved-$RUN_ID" -status Compliant -covered

scenario docker-hostnet-deaf "a host-network container that never listens is INTERFACE_ABSENT"
expect 20 -target "container/hostdeaf-$RUN_ID" -status NonCompliant -expect INTERFACE_ABSENT:interface/api \
  -collector INTERFACE_ABSENT=pacto-agent+ebpf

scenario docker-dependency-reachable "a container reaching its declared dependency is Compliant"
expect 20 -target "container/client-$RUN_ID" -status Compliant -covered -forbid DEPENDENCY_UNDECLARED

scenario docker-dependency-refused "a container refused by its declared dependency is DEPENDENCY_UNREACHABLE"
expect 20 -target "container/refused-$RUN_ID" -status NonCompliant -expect DEPENDENCY_UNREACHABLE:dependency/backend \
  -collector DEPENDENCY_UNREACHABLE=pacto-agent+ebpf

scenario docker-dependency-undeclared "a container reaching a labelled container it does not declare is DEPENDENCY_UNDECLARED"
expect 20 -target "container/stray-$RUN_ID" -expect DEPENDENCY_UNDECLARED:dependency/served \
  -collector DEPENDENCY_UNDECLARED=pacto-agent+ebpf
CURRENT=""
DONE=1
