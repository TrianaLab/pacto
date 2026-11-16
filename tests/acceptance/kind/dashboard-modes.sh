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
#   1b. install dashboard.enabled=true -> the dashboard asks people to sign in,
#      and the link NOTES prints signs in as pacto-viewer, which reads the fleet.
#   2. upgrade true -> false -> dashboard resources removed, operator stays Ready.
#   3. upgrade false -> true -> dashboard resources created + Deployment Ready.
#   4. uninstall (both modes) -> no operator/dashboard runtime resources.
set -euo pipefail
# shellcheck source=tests/acceptance/kind/lib.sh
source "$(dirname "$0")/lib.sh"

CLUSTER="${KIND_CLUSTER:-pacto-mono}"
NS=pacto-modes
CHART="$PACTO_CHART"
# The release tags every scenario builds, so CI loads them from the shared
# archive (lib.sh build_operator_images) instead of building them again.
TAG="$(release_version kubernetes)"
OP_REPO="localhost:5001/pacto-operator/pacto-controller"
OP_IMG="${OP_REPO}:${TAG}"
DASH_IMG="localhost:5001/pacto-dashboard:$(release_version core)"

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
SETS=(--set global.imageRegistry= --set image.repository="$OP_REPO" --set image.tag="$TAG" --set image.pullPolicy=Never
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
echo "== SCENARIO 1b: sign in with the NOTES link =="
# Follow NOTES literally: its create-token command and its port-forward, then
# the POST /auth/token the link's page makes (dash_token_login). The release is
# pacto-operator, so the chart's fullname is pacto-operator too.
notes="$(helm get notes pacto-operator -n "$NS")"
mint="$(grep -oE "kubectl -n $NS create token pacto-viewer --audience pacto-dashboard --duration 10m" <<<"$notes" | head -n 1)"
[ -n "$mint" ] || { echo "$notes" >&2; fail "NOTES print no create-token command for pacto-viewer with the pacto-dashboard audience"; }
grep -q "#token=\$($mint)" <<<"$notes" || fail "NOTES do not put the token in the link's fragment"
kubectl -n "$NS" get deploy pacto-dashboard -o json | jq -e '.spec.template.spec.containers[0].env as $e |
    ($e | map(select(.name == "PACTO_DASHBOARD_TOKEN_LOGIN"))[0].value == "true") and
    ($e | map(select(.name == "PACTO_DASHBOARD_ALLOW_ANONYMOUS")) == [])' >/dev/null \
  || fail "a bare install does not sign people in: $(kubectl -n "$NS" get deploy pacto-dashboard -o jsonpath='{.spec.template.spec.containers[0].env}')"
DASH_PF="$(pf 3000 svc/pacto-dashboard 3000)" || fail "could not port-forward the dashboard"
dash_get() { # COOKIE PATH — the HTTP status of a GET, with the session cookie when given
  local args=(-s -o /dev/null -w '%{http_code}')
  [ -n "$1" ] && args+=(-H "Cookie: __Host-pacto-session=$1")
  curl "${args[@]}" "http://127.0.0.1:3000$2"
}
[ "$(dash_get "" /api/fleet/services)" = 401 ] || fail "a visitor who has not signed in reads the fleet"
read -ra mint_cmd <<<"$mint" # the NOTES command, word for word
link="$("${mint_cmd[@]}")"
session="$(dash_token_login 3000 "$link" link)"
[[ -n "$session" && "$session" != *" "* ]] || fail "the NOTES link did not sign in: $session"
curl -s -H "Cookie: __Host-pacto-session=$session" http://127.0.0.1:3000/api/session |
  jq -e --arg u "system:serviceaccount:$NS:pacto-viewer" '.signedIn == true and .method == "token" and .user == $u' >/dev/null \
  || fail "the link session is not pacto-viewer's: $(curl -s -H "Cookie: __Host-pacto-session=$session" http://127.0.0.1:3000/api/session)"
[ "$(dash_get "$session" /api/fleet/services)" = 200 ] || fail "the link session cannot read the fleet"
out="$(dash_token_login 3000 "$(kubectl -n "$NS" create token pacto-viewer --duration 10m)")"
if [ "${out%% *}" != 401 ] || ! grep -q -- '--audience pacto-dashboard' <<<"$out"; then
  fail "a pacto-viewer token minted for another audience signed in: $out"
fi
kill "$DASH_PF" 2>/dev/null || true
pass "the dashboard asks people to sign in; the NOTES link signs in as pacto-viewer and reads the fleet; a token for another audience does not"

echo
echo "== SCENARIO 2: upgrade dashboard true -> false removes the dashboard, operator stays Ready =="
helm upgrade pacto-operator "$CHART" -n "$NS" "${SETS[@]}" \
  --set dashboard.enabled=false --wait --timeout 180s
assert_operator_healthy
wait_gone deployment pacto-dashboard -n "$NS"
wait_gone serviceaccount pacto-dashboard -n "$NS"
wait_gone clusterrolebinding pacto-dashboard
wait_gone clusterrole pacto-dashboard
wait_gone serviceaccount pacto-viewer -n "$NS"
wait_gone clusterrolebinding pacto-operator-dashboard-tokenreview
wait_gone clusterrolebinding pacto-operator-viewer
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
