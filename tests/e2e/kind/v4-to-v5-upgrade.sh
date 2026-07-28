#!/usr/bin/env bash
# Real cross-major operator upgrade — v4 -> v5 chart + CRD migration
#. Unlike run.sh (which upgrades a 0.0.0-prev fixture that
# is just the NEW chart repackaged with an older number — same CRDs, no real
# schema change), this test installs the ACTUAL previous-major operator: the v4.7.0
# chart (byte-faithful fixture, see tests/e2e/kind/fixtures/pacto-operator-v4/) with
# its v4 CRDs, driven by the real published v4 controller image 4.7.0. It then
# performs the DOCUMENTED CRD migration mechanism and upgrades to the v5 chart built
# from the working tree, proving an existing v4-stored Pacto survives the schema
# change and reconciles under v5.
#
# Flow:
#   1. clean slate -> install the v4 fixture chart (helm auto-installs its v4 CRDs
#      from crds/) with the real v4 image 4.7.0.
#   2. create a representative v4-shaped Pacto CR + its target workload; let the v4
#      operator reconcile; capture status.contractStatus + the CR UID.
#   3. server-side apply the NEW v5 CRDs (config/crd/bases/*.yaml) — the documented
#      mechanism, because `helm upgrade` never touches a chart's crds/. Prove the
#      apply succeeds and the CRD schema actually changed (v4 Passed/Failed printer
#      columns -> v5 Errors/Warnings).
#   4. helm upgrade to the v5 chart built from the working tree. Prove the SAME CR
#      (unchanged UID) is still stored + readable under the new CRD (no decode /
#      conversion error, i.e. NOT silently dropped) and reconciles to a v5 status
#      that carries a v5-only field (status.evaluationCoverage).
#   5. clean uninstall.
# Prints V4-TO-V5-UPGRADE PASS.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
CLUSTER="${KIND_CLUSTER:-pacto-mono}"
NS=pacto-upgrade
DEMO_NS=upgrade-demo
FIXTURE="$ROOT/tests/e2e/kind/fixtures/pacto-operator-v4"
V4_REPO="ghcr.io/trianalab/pacto-operator/pacto-controller"
V4_TAG="4.7.0"
# Pin the historical v4 image by digest so a mutable/republished :4.7.0 tag cannot
# silently change what this cross-major migration test installs. Verified in
# SOURCE.md; the guard below fails closed if the live tag no longer resolves here.
V4_DIGEST="sha256:a2e8e27dd8b080e797436ab376cef3f95467c7f91c9408bacc09aad8ff769e7d"
V5_REPO="pacto/operator"
V5_TAG="5.0.0-e2e"
V5_IMG="${V5_REPO}:${V5_TAG}"
CRD_PACTOS=pactos.pacto.trianalab.io
CRD_REVS=pactorevisions.pacto.trianalab.io

fail() { echo "  FAIL: $*"; exit 1; }

echo "== build the v5 operator image from the working tree (root Dockerfile) =="
# The dashboard is incidental to this test but must be ENABLED: the real v4.7.0
# operator sets up a cached ClusterRoleBinding watch whose RBAC the chart grants
# only when dashboard.enabled=true, so with it disabled the v4 manager's caches
# never sync and the Pacto reconciler never runs. Enabling it is v4's default,
# fully supported config. The v5 image's baked-in dashboard image is a
# non-pullable placeholder (the dashboard pod is harmless ImagePullBackOff — this
# test asserts the operator + CR, never the dashboard), which keeps the build fast.
docker build --provenance=false -f "$ROOT/integrations/kubernetes/Dockerfile" \
  --build-arg VERSION="$V5_TAG" --build-arg DASHBOARD_IMAGE="pacto-dashboard:disabled" \
  -t "$V5_IMG" "$ROOT"

echo "== package the v4 fixture chart (as 4.7.0) + the v5 chart (as 5.0.0) =="
rm -rf /tmp/pacto-upgrade-charts; mkdir -p /tmp/pacto-upgrade-charts
# The v4.7.0 release was cut from a 0.1.0 source with a package-time version
# override; reproduce that exactly.
helm package "$FIXTURE" --version 4.7.0 --app-version 4.7.0 -d /tmp/pacto-upgrade-charts >/dev/null
V4_CHART="/tmp/pacto-upgrade-charts/pacto-operator-4.7.0.tgz"
helm package "$ROOT/integrations/kubernetes/charts/pacto-operator" --version 5.0.0 --app-version "$V5_TAG" -d /tmp/pacto-upgrade-charts >/dev/null
V5_CHART="/tmp/pacto-upgrade-charts/pacto-operator-5.0.0.tgz"

kind get clusters | grep -qx "$CLUSTER" || kind create cluster --name "$CLUSTER" --wait 90s
# Only the locally built v5 image is kind-loaded. The real v4 image is multi-arch;
# the kind node pulls it directly from ghcr at install time (pullPolicy=IfNotPresent,
# public image) — a host-side `docker pull --platform` + `kind load` fails because
# kind imports all platforms and the other arch's blobs are not present locally.
echo "== load the freshly built v5 image into kind =="
kind load docker-image "$V5_IMG" --name "$CLUSTER"
export KUBECONFIG="$(mktemp)"; kind get kubeconfig --name "$CLUSTER" > "$KUBECONFIG"

# Clean slate so helm's first install genuinely installs the v4 CRDs (helm skips
# crds/ that already exist). Also runs on EXIT so a rerun / failure leaves nothing.
# Cluster-scoped resources are deleted by name too, so a leftover from an aborted
# run does not block helm install with an ownership conflict. Pacto CRs carry no
# finalizers, so the namespace + CRD deletes do not hang.
cleanup() {
  helm uninstall pacto-operator -n "$NS" --wait >/dev/null 2>&1 || true
  kubectl delete ns "$NS" "$DEMO_NS" --wait=false >/dev/null 2>&1 || true
  kubectl delete crd "$CRD_PACTOS" "$CRD_REVS" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete clusterrole,clusterrolebinding pacto-operator-manager pacto-dashboard \
    --ignore-not-found --wait=false >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup
# Wait for the CRDs to actually be gone before installing.
for _ in $(seq 1 30); do kubectl get crd "$CRD_PACTOS" >/dev/null 2>&1 || break; sleep 2; done

SETS=(--set image.pullPolicy=IfNotPresent --set dashboard.enabled=true)

echo
echo "== STEP 0: pin the v4 image by digest (fail closed if the tag was republished) =="
# Resolve the LIVE index digest of the historical tag; crane if present, else the
# docker fallbacks (same pattern as release/orchestrator/verify-oci.sh).
resolve_index_digest() {
  if command -v crane >/dev/null 2>&1; then crane digest "$1" 2>/dev/null && return; fi
  docker buildx imagetools inspect "$1" --format '{{json .Manifest}}' 2>/dev/null | grep -oE 'sha256:[a-f0-9]{64}' | head -1 && return
  docker manifest inspect -v "$1" 2>/dev/null | grep -oE 'sha256:[a-f0-9]{64}' | head -1
}
LIVE_DIGEST="$(resolve_index_digest "${V4_REPO}:${V4_TAG}")"
[ -n "$LIVE_DIGEST" ] || fail "could not resolve the live digest of ${V4_REPO}:${V4_TAG} (crane/docker registry unreachable)"
[ "$LIVE_DIGEST" = "$V4_DIGEST" ] || fail "v4 image tag drifted: ${V4_REPO}:${V4_TAG} now resolves to $LIVE_DIGEST, expected $V4_DIGEST (see SOURCE.md)"
echo "  v4 image pinned: ${V4_REPO}:${V4_TAG} == $V4_DIGEST"

echo
echo "== STEP 1: install the REAL v4 chart (4.7.0) + its v4 CRDs =="
helm install pacto-operator "$V4_CHART" -n "$NS" --create-namespace \
  --set image.repository="$V4_REPO" --set image.tag="$V4_TAG" "${SETS[@]}" --wait --timeout 180s
kubectl -n "$NS" rollout status deployment/pacto-operator --timeout=120s >/dev/null
kubectl get crd "$CRD_PACTOS" >/dev/null 2>&1 || fail "v4 install did not create the Pacto CRD"
# Prove the installed CRD is the v4 schema (its printer columns are v4-shaped).
V4COLS=$(kubectl get crd "$CRD_PACTOS" -o jsonpath='{.spec.versions[0].additionalPrinterColumns[*].name}')
echo "  installed CRD printer columns (v4): $V4COLS"
echo "$V4COLS" | grep -qw Passed || fail "installed CRD is not the v4 schema (no 'Passed' column): $V4COLS"
RUNNING_IMG=$(kubectl -n "$NS" get deploy pacto-operator -o jsonpath='{.spec.template.spec.containers[0].image}')
echo "  operator image: $RUNNING_IMG"
echo "$RUNNING_IMG" | grep -q ':4.7.0' || fail "operator not running the v4 image 4.7.0: $RUNNING_IMG"

echo
echo "== STEP 2: create a representative v4-shaped Pacto CR + let v4 reconcile =="
# The v4 operator embeds an older engine core, so it may report this contract as
# invalid (e.g. an unknown newer field) and land on NonCompliant — that is fine and
# faithful: the point is the v4 operator RECONCILES the CR and writes a status
# (surfaced, not dropped). The v5 operator resolves it to Compliant after upgrade.
kubectl create namespace "$DEMO_NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n "$DEMO_NS" create deployment orders --image=registry.k8s.io/pause:3.9 >/dev/null 2>&1 || true
kubectl -n "$DEMO_NS" rollout status deployment/orders --timeout=90s >/dev/null
kubectl apply -f - >/dev/null <<YAML
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: {name: orders, namespace: ${DEMO_NS}}
spec:
  checkIntervalSeconds: 30
  contractRef:
    inline: |
      pactoVersion: '2.0'
      service: {name: orders, version: 1.0.0, owner: {team: audit, dri: d, contacts: [{type: email, value: a@e.com, purpose: escalation}]}}
      workload: service
      state: {type: stateless, persistence: {scope: local, durability: ephemeral}, dataCriticality: low}
  target: {workloadRef: {name: orders, kind: Deployment}}
YAML
wait_nonempty_status() {
  local s
  for _ in $(seq 1 60); do
    s=$(kubectl -n "$DEMO_NS" get pacto orders -o jsonpath='{.status.contractStatus}' 2>/dev/null || true)
    [ -n "$s" ] && { echo "$s"; return 0; }
    sleep 3
  done
  return 1
}
V4_STATUS=$(wait_nonempty_status) || { kubectl -n "$DEMO_NS" get pacto orders -o yaml | tail -30; fail "v4 operator never wrote status.contractStatus"; }
V4_UID=$(kubectl -n "$DEMO_NS" get pacto orders -o jsonpath='{.metadata.uid}')
echo "  v4 reconciled: contractStatus=$V4_STATUS uid=$V4_UID"

echo
echo "== STEP 3: CRD migration — server-side apply the NEW v5 CRDs BEFORE the upgrade =="
# helm upgrade never touches a chart's crds/, so the new schema must be applied out
# of band. Server-side apply is required: these CRDs exceed the client-side
# last-applied-configuration annotation limit. --force-conflicts takes ownership of
# the fields helm set when it installed the v4 CRDs.
kubectl apply --server-side --force-conflicts -f "$ROOT/integrations/kubernetes/config/crd/bases/pacto.trianalab.io_pactos.yaml"
kubectl apply --server-side --force-conflicts -f "$ROOT/integrations/kubernetes/config/crd/bases/pacto.trianalab.io_pactorevisions.yaml"
V5COLS=$(kubectl get crd "$CRD_PACTOS" -o jsonpath='{.spec.versions[0].additionalPrinterColumns[*].name}')
echo "  migrated CRD printer columns (v5): $V5COLS"
echo "$V5COLS" | grep -qw Errors || fail "CRD did not migrate to the v5 schema (no 'Errors' column): $V5COLS"
# The stored v4 CR must remain readable under the new CRD (no decode/conversion
# error, same UID) — proof it was NOT silently dropped by the schema change.
POST_UID=$(kubectl -n "$DEMO_NS" get pacto orders -o jsonpath='{.metadata.uid}' 2>/dev/null) \
  || fail "existing Pacto CR is no longer readable after the CRD migration"
[ "$POST_UID" = "$V4_UID" ] || fail "CR UID changed across the CRD migration ($V4_UID -> $POST_UID)"
echo "  existing CR still stored + readable under the v5 CRD (uid unchanged)"

echo
echo "== STEP 4: helm upgrade to the v5 chart (built from the working tree) =="
helm upgrade pacto-operator "$V5_CHART" -n "$NS" \
  --set image.repository="$V5_REPO" --set image.tag="$V5_TAG" --set image.pullPolicy=Never \
  --set dashboard.enabled=true --set controller.stabilizationWindow=5s --wait --timeout 180s
kubectl -n "$NS" rollout status deployment/pacto-operator --timeout=120s >/dev/null
UP_IMG=$(kubectl -n "$NS" get deploy pacto-operator -o jsonpath='{.spec.template.spec.containers[0].image}')
echo "  operator rolled to: $UP_IMG"
echo "$UP_IMG" | grep -q "${V5_REPO}:${V5_TAG}" || fail "operator did not roll to the v5 image: $UP_IMG"
kubectl -n "$NS" get deploy pacto-operator -o jsonpath='{.spec.template.spec.containers[0].args}' \
  | grep -q 'stabilization-window=5s' || fail "v5 controller args missing --stabilization-window=5s"

echo "== the v5 operator reconciles the pre-existing CR to a v5 status =="
wait_v5_status() {
  local s
  for _ in $(seq 1 60); do
    s=$(kubectl -n "$DEMO_NS" get pacto orders -o jsonpath='{.status.contractStatus}' 2>/dev/null || true)
    [ "$s" = "Compliant" ] && { echo "  contractStatus=$s (v5)"; return 0; }
    sleep 3
  done
  echo "  FAIL: v5 operator did not reconcile the CR to Compliant (got '$s')"
  kubectl -n "$DEMO_NS" get pacto orders -o yaml | tail -40
  exit 1
}
wait_v5_status
FINAL_UID=$(kubectl -n "$DEMO_NS" get pacto orders -o jsonpath='{.metadata.uid}')
[ "$FINAL_UID" = "$V4_UID" ] || fail "CR was recreated across the upgrade ($V4_UID -> $FINAL_UID)"
COV=$(kubectl -n "$DEMO_NS" get pacto orders -o jsonpath='{.status.evaluationCoverage}' 2>/dev/null || true)
[ -n "$COV" ] || fail "v5-only status field evaluationCoverage missing — CR not re-reconciled under v5"
echo "  same CR (uid=$FINAL_UID) now carries the v5-only status.evaluationCoverage: $COV"
echo "  migration summary: v4 contractStatus='$V4_STATUS' -> v5 contractStatus='Compliant'"

echo
echo "== STEP 5: clean uninstall =="
helm uninstall pacto-operator -n "$NS" --wait >/dev/null
sleep 3
kubectl -n "$NS" get deploy pacto-operator -o name 2>/dev/null | grep -q . \
  && fail "operator Deployment survived uninstall" || echo "  operator runtime resources removed"

echo
echo "V4-TO-V5-UPGRADE PASS"
