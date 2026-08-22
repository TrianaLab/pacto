#!/usr/bin/env bash
# Dashboard-modes acceptance: prove the operator honors
# dashboard.enabled in every lifecycle transition and NEVER crashloops when the
# dashboard is disabled. The historical bug: the startup dashboard cleanup GETs
# dashboard-owned cluster resources (ClusterRoleBinding/ClusterRole/ServiceAccount)
# through the CACHED client, which starts a cluster-scoped informer whose
# list/watch the chart only grants when the dashboard is ENABLED -> the informer
# cannot sync (forbidden) -> "failed to wait for caches to sync" -> the manager
# exits 1 -> crashloop. The fix reads those via the uncached API reader and keeps
# a narrow always-on teardown RBAC, so a disabled operator starts cleanly and an
# upgraded (enabled -> disabled) one can still tear the dashboard down.
#
# Builds the operator + dashboard images the same way every other scenario does
# (root Dockerfile for the dashboard, coupled into the operator via the
# DASHBOARD_IMAGE build-arg), installs the chart straight from the working tree
# rather than a packaged copy, and drives four scenarios:
#   1. fresh install dashboard.enabled=false -> operator Ready, ZERO restarts, no
#      dashboard resources.
#   2. upgrade true -> false -> dashboard resources removed, operator stays Ready.
#   3. upgrade false -> true -> dashboard resources created + Deployment Ready.
#   4. uninstall (both modes) -> no operator/dashboard runtime resources.
set -euo pipefail
# shellcheck source=tests/acceptance/kind/lib.sh
source "$(dirname "$0")/lib.sh"

CLUSTER="${KIND_CLUSTER:-pacto-mono}"
NS=pacto-modes
CHART="$PACTO_CHART"
TAG=e2e-modes
OP_REPO="localhost:5001/pacto-operator/pacto-controller"
OP_IMG="${OP_REPO}:${TAG}"
DASH_IMG="localhost:5001/pacto-dashboard:${TAG}"

exists() { kubectl "$@" -o name 2>/dev/null | grep -q .; }

build_operator_images "$OP_IMG" "$DASH_IMG" "$TAG"

ensure_cluster
load_images "$DASH_IMG" "$OP_IMG"

# On exit: dump diagnostics FIRST if we failed, then tear down — unless
# KEEP_E2E_CLUSTER is set, which leaves the state for inspection (see lib.sh).
# shellcheck disable=SC2154  # rc is assigned by rc=$? inside the trap body
trap 'rc=$?; [ $rc -ne 0 ] && dump_diag "$NS"; keep_or_teardown "$NS" "$CLUSTER"; exit $rc' EXIT
# Best-effort teardown up front too, so a rerun starts from a clean namespace.
helm_teardown "$NS"

# Image loaded into kind: operator via image.pullPolicy=Never; the dashboard image
# the operator deploys is its coupled DASHBOARD_IMAGE (also loaded, non-latest tag
# -> default IfNotPresent uses the loaded image). Short window keeps things fast.
SETS=(--set image.repository="$OP_REPO" --set image.tag="$TAG" --set image.pullPolicy=Never
      --set controller.stabilizationWindow=5s)

# Assert the operator Deployment is Available with a single Running pod at ZERO
# restarts (a crashloop shows up as restartCount > 0 or an unready rollout).
# After an upgrade the old revision lingers briefly (Succeeded on SIGTERM), so we
# wait for exactly one Running pod before reading its restart count.
assert_operator_healthy() {
  kubectl -n "$NS" rollout status deployment/pacto-operator --timeout=150s >/dev/null || fail "operator Deployment not Available (crashloop?)"
  local pods=() restarts phase
  for _ in $(seq 1 30); do
    read -ra pods <<<"$(kubectl -n "$NS" get pods -l app.kubernetes.io/name=pacto-operator --field-selector=status.phase=Running -o jsonpath='{.items[*].metadata.name}')"
    [ "${#pods[@]}" -eq 1 ] && break
    sleep 2
  done
  [ "${#pods[@]}" -eq 1 ] || fail "expected exactly one Running operator pod, got ${#pods[@]}: ${pods[*]-none}"
  restarts=$(kubectl -n "$NS" get pod "${pods[0]}" -o jsonpath='{.status.containerStatuses[0].restartCount}')
  phase=$(kubectl -n "$NS" get pod "${pods[0]}" -o jsonpath='{.status.phase}')
  [ "$phase" = "Running" ] || fail "operator pod phase=$phase (want Running)"
  [ "$restarts" = "0" ] || fail "operator pod restartCount=$restarts (want 0 - crashloop regression)"
  echo "  operator healthy: pod=${pods[0]} phase=Running restarts=0"
}

assert_no_dashboard() {
  ! exists -n "$NS" get deploy pacto-dashboard || fail "pacto-dashboard Deployment exists (want none)"
  ! exists -n "$NS" get svc pacto-dashboard || fail "pacto-dashboard Service exists (want none)"
  ! exists get clusterrolebinding pacto-dashboard || fail "pacto-dashboard ClusterRoleBinding exists (want none)"
  echo "  no dashboard resources present"
}

wait_gone() { # kind name [ns-args...]
  local kind="$1" name="$2"; shift 2
  for _ in $(seq 1 40); do
    exists "$@" get "$kind" "$name" || { echo "  removed: $kind/$name"; return 0; }
    sleep 3
  done
  fail "$kind/$name not removed"
}

wait_dashboard_ready() {
  wait_managed_ready pacto-dashboard 150s >/dev/null || fail "dashboard Deployment not Ready"
  exists -n "$NS" get svc pacto-dashboard || fail "pacto-dashboard Service missing"
  exists get clusterrolebinding pacto-dashboard || fail "pacto-dashboard ClusterRoleBinding missing"
  echo "  dashboard resources created + Deployment Ready"
}

echo
echo "== SCENARIO 1: fresh install with dashboard.enabled=false =="
helm install pacto-operator "$CHART" -n "$NS" --create-namespace "${SETS[@]}" \
  --set dashboard.enabled=false --wait --timeout 180s
assert_operator_healthy
assert_no_dashboard

echo
echo "== SCENARIO 4a: uninstall (disabled mode) leaves no runtime resources =="
helm uninstall pacto-operator -n "$NS" --wait >/dev/null
wait_gone deployment pacto-operator -n "$NS"
! exists -n "$NS" get deploy pacto-dashboard || fail "pacto-dashboard Deployment survived uninstall"
echo "  operator runtime resources removed"

echo
echo "== install with dashboard.enabled=true (baseline for the upgrade path) =="
helm install pacto-operator "$CHART" -n "$NS" --create-namespace "${SETS[@]}" \
  --set dashboard.enabled=true --wait --timeout 180s
assert_operator_healthy
wait_dashboard_ready

echo
echo "== SCENARIO 2: upgrade dashboard true -> false removes the dashboard, operator stays Ready =="
helm upgrade pacto-operator "$CHART" -n "$NS" "${SETS[@]}" \
  --set dashboard.enabled=false --wait --timeout 180s
assert_operator_healthy
wait_gone deployment pacto-dashboard -n "$NS"
wait_gone serviceaccount pacto-dashboard -n "$NS"
wait_gone clusterrolebinding pacto-dashboard
wait_gone clusterrole pacto-dashboard
assert_no_dashboard

echo
echo "== SCENARIO 3: upgrade dashboard false -> true recreates the dashboard =="
helm upgrade pacto-operator "$CHART" -n "$NS" "${SETS[@]}" \
  --set dashboard.enabled=true --wait --timeout 180s
assert_operator_healthy
wait_dashboard_ready

echo
echo "== SCENARIO 4b: uninstall (enabled mode) leaves no runtime resources =="
helm uninstall pacto-operator -n "$NS" --wait >/dev/null
wait_gone deployment pacto-operator -n "$NS"
wait_gone deployment pacto-dashboard -n "$NS"
echo "  operator + dashboard runtime resources removed"

echo
echo "DASHBOARD-MODES E2E PASS"
