#!/usr/bin/env bash
# Shared harness for the kind acceptance scenarios (reconcile.sh, evidence.sh,
# dashboard-modes.sh, observation.sh, operational-graph.sh, upgrade-v4-v5.sh).
# Sourced, never executed directly.
#
# ONE implementation per stable shared concern: reporting, eventual conditions,
# cluster lifecycle, image loading, chart packaging, port-forwarding, fixture
# installation, failure diagnostics, cleanup. Everything a single scenario needs
# stays in that scenario's script, next to the claim it serves — this file is a
# toolbox, not a framework, and it never decides what a scenario proves.
#
# Functions that operate "in the current run" read the caller's $NS and $CLUSTER
# globals (every scenario sets both before using them); everything else is an
# explicit argument.
#
#   pass MSG / fail MSG            report one claim; fail exits 1
#   eventually ROUNDS CMD...       run CMD until it succeeds, 3s apart
#   ensure_cluster                 create $CLUSTER if absent, point KUBECONFIG at it
#   use_existing_cluster HINT      require an already-running $CLUSTER
#   load_images IMG...             import images into $CLUSTER
#   delete_cluster / down_cluster  quiet teardown / the chatty `down` subcommand
#   release_version GROUP          the version the release plan assigns
#   build_operator_images ...      the two production image builds, coupled
#   package_chart CHART_DIR ...    helm package -> prints the .tgz
#   build_pacto                    go build cmd/pacto -> prints the binary
#   wait_ready DEPLOY              a chart-created Deployment is rolled out
#   wait_managed_ready DEPLOY      an OPERATOR-created Deployment exists, then is
#   pacto_status NS NAME           the CR's contractStatus (empty while unset)
#   wait_pacto_status NS NAME WANT poll for it, dumping the CR when it never lands
#   install_registry               an in-cluster OCI registry, ready to push to
#   trust_keypair BIN [KEY] [PROD] producer keypair + the Secret evidence mounts
#   push_bundle BIN PORT DIR REF   publish a bundle -> prints the manifest digest
#   dump_diag NS                   best-effort diagnostics for a failed run
#   pf LOCAL_PORT TARGET REMOTE    a port-forward that is already answering
#   keep_or_teardown NS CLUSTER FN tear down unless KEEP_E2E_CLUSTER is set
#   helm_teardown NS               uninstall the release, drop the namespace

_PACTO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# The chart under test, always from the working tree.
# shellcheck disable=SC2034  # read by the scenarios that source this file
PACTO_CHART="$_PACTO_ROOT/integrations/kubernetes/charts/pacto-operator"

# --- reporting --------------------------------------------------------------

pass() { echo "  PASS: $*"; }
# The failure goes to STDERR: call sites routinely silence a helper's chatty
# stdout (`wait_managed_ready x >/dev/null || fail ...`), and a reason written to
# stdout would be silenced with it — leaving a run that exits 1 saying nothing.
fail() { echo "  FAIL: $*" >&2; exit 1; }

# eventually ROUNDS CMD... — run CMD until it succeeds, sleeping 3s between
# rounds; returns 1 if it never does. Nothing in a real cluster is true the
# instant the previous command returns: the operator has to reconcile, the
# dashboard rebuilds its snapshot on an interval, a rollout takes as long as it
# takes. Every scenario grew its own `for _ in $(seq 1 N)` around that; they
# differ only in the condition and in how long they are willing to wait, so
# those are the two arguments.
eventually() {
  local rounds="$1"; shift
  local i
  for ((i = 0; i < rounds; i++)); do
    "$@" && return 0
    sleep 3
  done
  return 1
}

# --- cluster lifecycle ------------------------------------------------------

# Every scenario runs against its own KUBECONFIG copy rather than the caller's
# current context: a kind acceptance run must never depend on — or disturb —
# whatever cluster the developer happens to be pointed at.
_kubeconfig_for_cluster() {
  KUBECONFIG="$(mktemp)"; export KUBECONFIG
  kind get kubeconfig --name "$CLUSTER" > "$KUBECONFIG"
}

ensure_cluster() {
  kind get clusters | grep -qx "$CLUSTER" || kind create cluster --name "$CLUSTER" --wait 90s
  _kubeconfig_for_cluster
}

use_existing_cluster() { # HINT — the command that would have created it
  kind get clusters | grep -qx "$CLUSTER" || { echo "no kind cluster '$CLUSTER' — run '${1:-the up subcommand}' first"; exit 1; }
  _kubeconfig_for_cluster
}

# load_images IMG... — import images into $CLUSTER. The ONE image-loading
# boundary: every scenario goes through it, on CI's classic Docker image store
# and on a Docker Desktop workstation alike.
#
# It is not `kind load docker-image`, which streams `docker save` into `ctr
# images import --all-platforms` inside the node and therefore breaks under the
# containerd image store, where a pulled tag keeps its multi-platform INDEX
# identity locally while only this host's platform is materialized: the node is
# asked for manifests that were never fetched and answers "content digest
# sha256:...: not found". Loading images one at a time (which this used to do,
# for the same class of symptom on shared base layers) does not help — the
# missing content is another PLATFORM, not another image.
#
# The decision logic lives in Go, in tests/acceptance/kind/kindload: which
# platform the NODE runs, whether this docker CLI can narrow an export to it,
# whether the resulting archive is self-contained before the node ever sees it,
# and — after loading — whether the reference resolves on every node to the
# config digest that was exported. That last check is what makes a scenario's
# `imagePullPolicy: Never` a guarantee rather than a hope.
load_images() {
  ( cd "$_PACTO_ROOT" && go run ./tests/acceptance/kind/kindload -cluster "$CLUSTER" "$@" )
}

delete_cluster() { kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true; }

down_cluster() { # the `down` subcommand: delete it if it is there, say which happened
  if kind get clusters | grep -qx "$CLUSTER"; then
    kind delete cluster --name "$CLUSTER"; echo "cluster '$CLUSTER' deleted"
  else
    echo "no cluster '$CLUSTER'"
  fi
}

# --- images and charts ------------------------------------------------------

_PACTO_PLAN_BUILT=

# release_version GROUP (core|kubernetes) — the version the release plan assigns.
# The plan is rebuilt once per run, then read with node, which the plan builder
# already requires. Reading it with a second interpreter only added a dependency.
release_version() {
  if [ -z "$_PACTO_PLAN_BUILT" ]; then
    node "$_PACTO_ROOT/release/scripts/build-release-plan.mjs" >/dev/null 2>&1
    _PACTO_PLAN_BUILT=1
  fi
  node -e 'process.stdout.write(require(process.argv[1]).groups[process.argv[2]].version)' \
    "$_PACTO_ROOT/release/release-plan.json" "$1"
}

# build_operator_images OP_IMG DASH_IMG VERSION — the two production builds, in
# the order that couples them: the operator deploys the dashboard image baked in
# at build time, so the dashboard must exist first and be named in the operator's
# DASHBOARD_IMAGE build-arg.
#
# --load forces the result into the local docker image store even when the
# default buildx builder uses the docker-container driver (otherwise the image
# lives only in buildkit's cache and `kind load docker-image` fails with
# "content digest not found").
build_operator_images() {
  echo "== build the dashboard image (root Dockerfile) =="
  docker build --load -f "$_PACTO_ROOT/Dockerfile" -t "$2" "$_PACTO_ROOT"
  echo "== build the operator image (production root context) coupled to that dashboard =="
  docker build --load -f "$_PACTO_ROOT/integrations/kubernetes/Dockerfile" \
    --build-arg VERSION="$3" --build-arg DASHBOARD_IMAGE="$2" -t "$1" "$_PACTO_ROOT"
}

# package_chart CHART_DIR [helm package args...] — prints the packaged .tgz path.
# Each call packages into its OWN empty directory, so the path is unambiguous
# without pattern-matching a shared /tmp directory that a previous run, or the
# scenario's own second package call, also wrote into.
package_chart() {
  local src="$1" dir; shift
  dir="$(mktemp -d)"
  helm package "$src" "$@" -d "$dir" >/dev/null
  ls "$dir"/*.tgz
}

build_pacto() { # prints the path to a freshly built pacto binary
  local bin; bin="$(mktemp)"
  go build -o "$bin" "$_PACTO_ROOT/cmd/pacto" >&2
  echo "$bin"
}

# --- workloads --------------------------------------------------------------

wait_ready() { # DEPLOY [TIMEOUT] — a Deployment the chart creates
  kubectl -n "$NS" rollout status "deployment/$1" --timeout="${2:-180s}"
}

deploy_exists() { kubectl -n "$NS" get deploy "$1" >/dev/null 2>&1; }

# wait_managed_ready DEPLOY [TIMEOUT] — a Deployment the OPERATOR creates.
# `helm --wait` cannot cover these: they do not exist when helm returns, so
# waiting on the rollout has to wait for the object first.
wait_managed_ready() {
  eventually 40 deploy_exists "$1" || fail "the operator never created deployment/$1"
  wait_ready "$1" "${2:-150s}"
}

pacto_status() { # NS NAME — the CR's contractStatus, empty while it has none
  kubectl -n "$1" get pacto "$2" -o jsonpath='{.status.contractStatus}' 2>/dev/null || true
}

# wait_pacto_status NS NAME WANT — poll for a reconciled verdict. On timeout it
# prints what it last saw and the tail of the CR: a status that never arrives is
# otherwise only diagnosable by reading operator logs backwards.
wait_pacto_status() {
  local last=""
  for _ in $(seq 1 60); do
    last="$(pacto_status "$1" "$2")"
    [ "$last" = "$3" ] && return 0
    sleep 3
  done
  echo "  wanted $2 contractStatus=$3, last saw '$last'" >&2
  kubectl -n "$1" get pacto "$2" -o yaml | tail -30 >&2 || true
  return 1
}

# --- fixtures ---------------------------------------------------------------

# PACTO_REGISTRY_IMAGE is the in-cluster registry every scenario runs against.
#
# It is zot, not `registry:2`, because the OCI 1.1 Referrers API is not optional
# here: accepted evidence IS a referrer of a contract revision, and Pacto refuses
# oras-go's legacy referrers-tag fallback, so a registry without the native
# endpoint makes the Evidence Server permanently not-ready. CNCF distribution
# implements no referrers endpoint at all — not in 2.x and not in 3.x — and the
# ORAS CLI hides that by falling back to the tag scheme client-side, which is
# exactly the silence this harness must not run on. zot-minimal is the smallest
# conformant option: same port, same /var/lib/registry, no CVE-database
# downloads to make a demo depend on the network.
#
# Pinned to a released tag so a scenario's `imagePullPolicy: Never` names a
# version rather than whatever `latest` moved to this morning.
# shellcheck disable=SC2034  # read by the scenarios that source this file
PACTO_REGISTRY_IMAGE="ghcr.io/project-zot/zot-minimal:v2.1.20"

# install_registry — an in-cluster OCI registry in $NS, ready to be pushed to.
# Plain HTTP, so callers must also teach whatever resolves from it to treat the
# service host as insecure. imagePullPolicy: Never, because the image is loaded
# into the node rather than pulled from the internet by every kind node in CI.
#
# The pull stays unconditional and needs no protecting: the registry image is a
# multi-platform tag, and the host's copy of it is a multi-platform index whose
# other platforms are not present — the very artifact that used to break the
# load. load_images narrows it at load time, so re-pulling cannot restore a
# broken state and there is no pre-flattened local image for a later pull to
# overwrite.
install_registry() {
  docker pull "$PACTO_REGISTRY_IMAGE" >/dev/null
  load_images "$PACTO_REGISTRY_IMAGE"
  kubectl -n "$NS" apply -f - >/dev/null <<YAML
apiVersion: apps/v1
kind: Deployment
metadata: { name: pacto-registry, labels: { app: pacto-registry } }
spec:
  replicas: 1
  selector: { matchLabels: { app: pacto-registry } }
  template:
    metadata: { labels: { app: pacto-registry } }
    spec:
      containers:
      - { name: registry, image: $PACTO_REGISTRY_IMAGE, imagePullPolicy: Never, ports: [ { containerPort: 5000 } ] }
---
apiVersion: v1
kind: Service
metadata: { name: pacto-registry }
spec:
  selector: { app: pacto-registry }
  ports: [ { port: 5000, targetPort: 5000 } ]
YAML
  wait_ready pacto-registry 120s
}

# trust_keypair PACTO_BIN [KEY_ID] [PRODUCER] — a producer keypair plus the
# Secret the Evidence Server mounts as its trust store. Prints the key directory;
# the private half stays there for the caller to sign with.
#
# The public key's FILENAME is the trust binding: `<producer>__<keyId>.pub`
# authorizes exactly that producer, and a bare `<keyId>.pub` authorizes a producer
# named after the key id. So the filename is read back off disk rather than
# reconstructed here — reconstructing it is how a scenario ends up installing one
# binding and signing under another, which the server rejects at ingestion with a
# producer mismatch that says nothing about the cause.
trust_keypair() {
  local bin="$1" keyid="${2:-demo}" producer="${3:-}"
  local keydir; keydir="$(mktemp -d)"
  local -a args=(evidence keygen --out "$keydir" --key-id "$keyid")
  [ -n "$producer" ] && args+=(--producer "$producer")
  "$bin" "${args[@]}" >/dev/null
  local pub; pub="$(ls "$keydir"/*.pub)"
  kubectl -n "$NS" create secret generic pacto-evidence-trust --from-file="$(basename "$pub")=$pub" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  echo "$keydir"
}

# push_bundle PACTO_BIN LOCAL_PORT DIR REPO:TAG — publish a bundle through a
# forwarded registry port; prints the resolved manifest digest.
#
# The push output is CAPTURED rather than piped straight into grep. Piped, a
# failed push produced no match, `grep` exited 1, and under `set -o pipefail`
# that killed the whole script at that line with every word of the actual error
# already consumed by the pipe. Held in a variable it can be printed — to
# stderr, because stdout here is the digest the caller captures.
push_bundle() {
  local out digest
  out="$(PACTO_INSECURE_REGISTRIES="127.0.0.1:$2" "$1" push "oci://127.0.0.1:$2/demo/$4" -p "$3" 2>&1)" || true
  digest="$(printf '%s' "$out" | grep -oE 'sha256:[0-9a-f]{64}' | head -1 || true)"
  [ -n "$digest" ] || { echo "  push of $4 resolved no digest; output was:" >&2; printf '%s\n' "$out" >&2; }
  printf '%s' "$digest"
}

# --- diagnostics and teardown -----------------------------------------------

# The pacto components a script may deploy; logs are dumped for whichever exist.
_PACTO_DEPLOYS=(pacto-operator pacto-dashboard pacto-evidence)

# dump_diag NS — best-effort cluster diagnostics for a failed run. Every command
# is guarded with `|| true` so the dump NEVER changes the caller's exit code — it
# runs inside an EXIT trap that must preserve the real failure code.
dump_diag() {
  local ns="${1:-}" d
  echo "== DIAGNOSTICS (namespace: ${ns:-<none>}) =="
  echo "--- pods (all namespaces) ---"
  kubectl get pods -A || true
  echo "--- events ($ns, by time) ---"
  kubectl get events -n "$ns" --sort-by=.lastTimestamp || true
  echo "--- deployments ($ns) ---"
  kubectl describe deploy -n "$ns" || true
  # The Pacto CRs carry the verdict AND the findings that produced it. Without
  # them a status that never reaches Compliant is only diagnosable by reading
  # operator logs backwards, which is how this dump earned its place.
  echo "--- pacto CRs (all namespaces) ---"
  kubectl get pactos -A -o yaml || true
  for d in "${_PACTO_DEPLOYS[@]}"; do
    echo "--- logs deploy/$d ($ns) ---"
    kubectl logs -n "$ns" "deploy/$d" --all-containers --tail=200 || true
    echo "--- logs deploy/$d ($ns, previous) ---"
    kubectl logs -n "$ns" "deploy/$d" --all-containers --previous --tail=200 || true
  done
  echo "--- pvc ($ns) ---"
  kubectl get pvc -n "$ns" -o wide || true
  # Evidence-specific diagnostics (best-effort; only when the component exists).
  # The store is the contract registry, so the diagnosable question is whether the
  # server can READ it: readiness names the subject that would not resolve, and
  # the targets DTO carries the health block (ready / partial / unavailable, with
  # the failed-subject and unreadable-artifact counts) that distinguishes "nothing
  # was reported" from "the registry could not be read". Neither leaks a secret.
  if kubectl -n "$ns" get deploy pacto-evidence >/dev/null 2>&1; then
    echo "--- evidence readiness/health ($ns) ---"
    kubectl -n "$ns" exec deploy/pacto-evidence -- sh -c 'wget -qO- http://127.0.0.1:8686/api/evidence/v1/ready; echo; wget -qO- http://127.0.0.1:8686/api/evidence/v1/health; echo' 2>/dev/null || true
    echo "--- evidence registry read state ($ns) ---"
    kubectl -n "$ns" exec deploy/pacto-evidence -- sh -c 'wget -qO- http://127.0.0.1:8686/api/evidence/v1/targets' 2>/dev/null | head -30 || true
  fi
}

# pf LOCAL_PORT TARGET REMOTE_PORT — prints the pid on stdout, waits on readiness.
#
# Three scripts each grew their own copy of this, two of them ending in a flat
# `sleep 2`. That sleep WAS the flake: on a loaded runner the tunnel is not
# listening yet when the next command connects, and callers that pipe their
# output (`pacto push ... | grep -oE 'sha256:...'`) swallow the connection error
# entirely — so the script died under `set -e` with no message at all, at a line
# that looked innocent. A readiness wait is the fix; a longer sleep is not.
#
# ANY HTTP response means ready: the request reached the pod and bytes came back.
# A 404 from a registry root proves the tunnel exactly as well as a 200 from the
# dashboard does, which is why this is a bare `curl`, not `curl -f`.
#
# The forward is RESPAWNED rather than waited on, because `kubectl port-forward`
# to a Service with no ready endpoint does not wait — it exits immediately. Every
# caller that reconnects after a restart hits exactly that: the pod is
# ContainerCreating, kubectl dies at once, and a single spawn turns a
# sixty-second readiness wait into a one-second failure. Respawning keeps the
# window the window.
#
# The failure goes to STDERR on purpose — stdout is the pid, and every call site
# captures it in `$(...)`, so a message written to stdout would be swallowed too.
#
# The port is claimed BEFORE the first spawn, because "something answers on this
# port" and "my port-forward works" are not the same statement and this function
# used to conflate them. A process that already holds lport stops kubectl from
# binding; kubectl exits at once, the curl below is answered by the squatter, and
# pf reports success. Every later assertion then interrogates a stranger — a
# positive one fails against a product that is fine, and a negative one
# (`! grep -q ...`) passes against a product that is not. Observed for real: a
# `python3 -m http.server` on 8080 turned a healthy Evidence Server run red.
# Refuse the port instead of testing someone else's server.
pf() {
  local lport="$1" target="$2" rport="$3" pid=""
  if curl -sS -o /dev/null --max-time 2 "http://127.0.0.1:${lport}/" 2>/dev/null; then
    echo "  FAIL: 127.0.0.1:${lport} already answers before the port-forward to ${target} started — something else holds that port" >&2
    return 1
  fi
  for _ in $(seq 1 60); do
    if [ -z "$pid" ] || ! kill -0 "$pid" 2>/dev/null; then
      kubectl -n "$NS" port-forward "$target" "${lport}:${rport}" >/dev/null 2>&1 &
      pid=$!
    fi
    if curl -sS -o /dev/null --max-time 3 "http://127.0.0.1:${lport}/" 2>/dev/null; then
      echo "$pid"; return 0
    fi
    sleep 1
  done
  kill "$pid" 2>/dev/null || true
  echo "  FAIL: port-forward to $target never answered on 127.0.0.1:${lport}" >&2
  return 1
}

# keep_or_teardown NS CLUSTER [TEARDOWN_FN] — tear the run down UNLESS
# KEEP_E2E_CLUSTER is set/non-empty, in which case the deployed state is left in
# place for interactive inspection:
#     KEEP_E2E_CLUSTER=1 bash tests/acceptance/kind/<script>.sh
# Default (unset/empty) tears down, so CI never leaks state. TEARDOWN_FN is the
# caller's own cleanup function; if omitted a generic helm-uninstall +
# delete-namespace is used. NS/CLUSTER are arguments (they differ per script:
# pacto-system, pacto-modes, pacto-upgrade) — nothing is hardcoded here.
keep_or_teardown() {
  local ns="${1:-}" cluster="${2:-}" fn="${3:-}"
  if [ -n "${KEEP_E2E_CLUSTER:-}" ]; then
    echo "== KEEP_E2E_CLUSTER set — leaving namespace '$ns' in kind cluster '$cluster' for inspection (KUBECONFIG=${KUBECONFIG:-unset}) =="
    return 0
  fi
  if [ -n "$fn" ]; then
    "$fn"
  else
    helm_teardown "$ns"
  fi
}

helm_teardown() { # NS — the generic cleanup: uninstall the release, drop the namespace
  helm uninstall pacto-operator -n "$1" --wait >/dev/null 2>&1 || true
  kubectl delete ns "$1" --wait=false >/dev/null 2>&1 || true
}
