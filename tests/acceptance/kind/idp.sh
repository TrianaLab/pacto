#!/usr/bin/env bash
# Profile idp on a real API server: each idp piece turns on only where the
# cluster supports it, says why in the detected rows, bootstraps Crossplane on
# a person's request and leaves cleanly for profile default (#573).
#
# helm-unittest pins what idp renders; only a cluster can answer whether the
# registry serves OCI Referrers, whether the operator then runs the Evidence
# Server, whether the vendored Crossplane chart comes up and what Crossplane
# leaves behind when the value goes off. The operator's evidence gate and its
# detection both run at start and then every five minutes, so the row waits
# below allow two rounds.
#
# One release, pacto-operator by fullnameOverride, in namespace pacto-idp with
# the in-cluster zot registry (native Referrers) beside it.
#   1. idp_detects: profile=idp, registry=<zot>/acme, no signer and no trust
#      Secret: the crossplane row reads none found with the bootstrap hint, the
#      evidence row reads off for the trust Secret, the recorder row reads off
#      with no signing method, and no pacto-evidence Deployment exists.
#   2. evidence_on: the trust Secret <fullname>-evidence-trust appears with one
#      public key and the operator restarts: pacto-evidence is Ready and the row
#      reads on.
#   3. crossplane_bootstrap: idp.crossplane.install=true installs Crossplane,
#      a fixture XRD is accepted, the row reads Crossplane v2.<n>.<n> and
#      GET /api/capabilities reads provisioning engine crossplane; a second
#      helm upgrade with the same values succeeds (the release owns Crossplane).
#   4. to_default: profile=default, delivery.enabled=true,
#      idp.crossplane.install=false: no pacto-evidence, no recorder, no
#      --evidence-server-auto or --idp-recorder, no Crossplane Deployment; the
#      Crossplane CRDs and the fixture XRD stay, the row reads APIs served,
#      controller not found and the engine is no longer crossplane; a Pacto
#      still reconciles, the target snapshot push continues and the promoter
#      stays Ready. Then what Crossplane left: its webhook configurations, an
#      XRD change against them, their deletion and the same change accepted.
#   5. reenable: helm upgrade --dry-run=server back to idp with
#      idp.crossplane.install=true succeeds on the CRD-only cluster, and the
#      row still reads no running Crossplane.
set -euo pipefail
# shellcheck source=tests/acceptance/kind/lib.sh
source "$(dirname "$0")/lib.sh"
# shellcheck source=tests/acceptance/kind/delivery-lib.sh
source "$(dirname "$0")/delivery-lib.sh"
# shellcheck source=tests/acceptance/kind/profiles-lib.sh
source "$(dirname "$0")/profiles-lib.sh"

CLUSTER="${KIND_CLUSTER:-pacto-mono}"
REL=pacto-idp
NS="$REL" # read by install_registry, dump_diag, wait_managed_ready, pf and profiles-lib.sh
DNS=pacto-delivery
CHART="$PACTO_CHART"
DETECTED=pacto-operator-detected
TRUST=pacto-operator-evidence-trust
REG_HOST="pacto-registry.${NS}.svc:5000"
LOCAL_DASH_PORT=18531
LOCAL_REG_PORT=18532
XRD=xbuckets.e2e.pacto.run
XRD_KIND=compositeresourcedefinitions.apiextensions.crossplane.io
HINT="helm upgrade <release> <chart> --reuse-values --set idp.crossplane.install=true"
VER="$(release_version kubernetes)"
OP_REPO="localhost:5001/pacto-operator/pacto-controller"
OP_IMG="${OP_REPO}:${VER}"
DL_REPO="localhost:5001/pacto-delivery"
DL_IMG="${DL_REPO}:${VER}"
DASH_IMG="localhost:5001/pacto-dashboard:$(release_version core)"

for t in jq helm go; do command -v "$t" >/dev/null 2>&1 || fail "$t is required"; done

# crossplane_webhooks — the webhook configurations that call the Crossplane
# webhook Service. Crossplane creates them at runtime, so Helm never owns them.
crossplane_webhooks() {
  kubectl get validatingwebhookconfigurations,mutatingwebhookconfigurations -o json |
    jq -r '.items[] | select(any(.webhooks[]?; .clientConfig.service.name? == "crossplane-webhooks")) | "\(.kind | ascii_downcase)/\(.metadata.name)"'
}

teardown() {
  helm uninstall "$REL" -n "$REL" --wait >/dev/null 2>&1 || true
  kubectl delete clusterrole,clusterrolebinding pacto-dashboard --ignore-not-found >/dev/null 2>&1 || true
  local left
  left="$(crossplane_webhooks 2>/dev/null || true)"
  # shellcheck disable=SC2086  # one argument per object
  [ -z "$left" ] || kubectl delete $left --ignore-not-found >/dev/null 2>&1 || true
  kubectl delete "$XRD_KIND" "$XRD" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  left="$(kubectl get crd -o name 2>/dev/null | grep -E '(crossplane\.io|e2e\.pacto\.run)$' || true)"
  # shellcheck disable=SC2086  # one argument per CRD
  [ -z "$left" ] || kubectl delete $left --ignore-not-found --wait=false >/dev/null 2>&1 || true
  left="$(kubectl get clusterrole,clusterrolebinding -o name 2>/dev/null | grep '/crossplane' || true)"
  # shellcheck disable=SC2086  # one argument per object
  [ -z "$left" ] || kubectl delete $left --ignore-not-found >/dev/null 2>&1 || true
  kubectl delete ns "$REL" "$DNS" idp-a --ignore-not-found --wait=false >/dev/null 2>&1 || true
}
# shellcheck disable=SC2154  # rc is assigned by rc=$? inside the trap body
trap 'rc=$?; [ $rc -ne 0 ] && dump_diag "$NS"; keep_or_teardown "$NS" "$CLUSTER" teardown; exit $rc' EXIT

section "create the cluster and load the images"
cluster_start_bg
load_built_images "$OP_IMG" "$DL_IMG" "$DASH_IMG" || build_images
pull_pinned "$PACTO_CROSSPLANE_IMAGE"
PACTO_BIN="$(build_pacto)"
ensure_cluster
infra "load the images into the cluster" load_images "$OP_IMG" "$DL_IMG" "$DASH_IMG" "${PACTO_CROSSPLANE_IMAGE%@*}"
teardown
for ns in "$REL" "$DNS"; do
  eventually 40 sh -c "! kubectl get ns $ns >/dev/null 2>&1" || fail "namespace $ns from an earlier run never went away"
done
eventually 40 sh -c "! kubectl api-versions | grep -q '^apiextensions.crossplane.io/'" || fail "the Crossplane APIs from an earlier run are still served"
kubectl create ns "$REL" >/dev/null
install_registry

SETS=(--set global.imageRegistry= --set image.repository="$OP_REPO" --set image.tag="$VER" --set image.pullPolicy=Never
      --set fullnameOverride=pacto-operator --set controller.stabilizationWindow=5s
      --set delivery.image.repository="$DL_REPO" --set delivery.image.tag="$VER" --set delivery.image.pullPolicy=Never
      --set "insecureRegistries[0]=$REG_HOST" --set "delivery.insecureRegistries[0]=$REG_HOST")

has_status() { [ -n "$(pacto_status "$1" "$2")" ]; }
row() { detected_json | jq -c --arg k "$1" '.entries[]? | select(.kind == $k)'; }
row_is() { # KIND [JQ_ARGS...] JQ_FILTER — the detected row of KIND holds JQ_FILTER
  local kind="$1"; shift
  row "$kind" | jq -e "$@" >/dev/null 2>&1
}
DASH_PF=
# login — sign a pacto-viewer in to the managed dashboard forwarded to
# LOCAL_DASH_PORT; sets SESSION and DASH_PF. The dashboard is re-applied when
# the evidence URL changes, so every read signs in again.
login() {
  DASH_PF="$(pf "$LOCAL_DASH_PORT" svc/pacto-dashboard 3000)" || return 1
  SESSION="$(dash_token_login "$LOCAL_DASH_PORT" "$(kubectl -n "$NS" create token pacto-viewer --audience pacto-dashboard --duration 10m)" link)"
  [[ -n "$SESSION" && "$SESSION" != *" "* ]]
}
engine_is() { # WANT_JQ — GET /api/capabilities .installation.provisioning.engine holds WANT_JQ
  local rc=0
  login && installation | jq -e ".provisioning.engine | $1" >/dev/null 2>&1 || rc=1
  kill "$DASH_PF" 2>/dev/null || true
  return $rc
}
show_engine() { login && installation | jq -c .provisioning >&2; kill "$DASH_PF" 2>/dev/null || true; }
fixture_xrd() { # [LABEL] — the fixture XRD, labelled e2e.pacto.run/change=LABEL when given
  kubectl apply -f - <<YAML
apiVersion: apiextensions.crossplane.io/v2
kind: CompositeResourceDefinition
metadata:
  name: $XRD
  labels: {e2e.pacto.run/change: "${1:-none}"}
spec:
  group: e2e.pacto.run
  names: {kind: XBucket, plural: xbuckets}
  scope: Namespaced
  versions:
  - name: v1alpha1
    served: true
    referenceable: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
              region: {type: string}
YAML
}
running_crossplane='.completeness == "complete" and (.value | startswith("Crossplane "))'

section "SCENARIO 1 (idp_detects): profile idp turns on only what the cluster supports"
adopt_crds "$REL"
helm install "$REL" "$CHART" -n "$REL" "${SETS[@]}" --set profile=idp \
  --set registry="$REG_HOST/acme" --wait --timeout 240s >/dev/null
argv="$(op_args)"
jq -e 'index("--evidence-server-auto") and index("--idp-recorder=no-signer") and index("--evidence-subject-prefix=oci://'"$REG_HOST"'/acme/")' <<<"$argv" >/dev/null \
  || fail "the idp operator does not run with --evidence-server-auto, --idp-recorder=no-signer and the registry subject prefix: $argv"
pass "the operator runs with --evidence-server-auto, --idp-recorder=no-signer and the subject prefix oci://$REG_HOST/acme/"
# shellcheck disable=SC2016  # $h is a jq variable
eventually 60 row_is crossplane --arg h "$HINT" '.value == "none found" and .completeness == "complete" and .hint == $h' \
  || fail "the crossplane row does not read none found with the bootstrap hint: $(row crossplane)"
pass "crossplane: $(row crossplane | jq -r '"\(.value) \(.evidence); hint: \(.hint)"')"
eventually 60 row_is recorder '.value == "off" and (.evidence | startswith("no signing method"))' \
  || fail "the recorder row does not read off with no signing method: $(row recorder)"
pass "recorder: $(row recorder | jq -r '"\(.value) \(.evidence)"')"
# The gate decides only once the first target snapshot push is done, on its
# next round, and detection writes the row on its own next round.
eventually 220 row_is evidence '.value == "off" and .completeness == "complete" and (.evidence | contains("trust Secret"))' \
  || fail "the evidence row never read off for the trust Secret: $(row evidence)"
pass "evidence: $(row evidence | jq -r '"\(.value) \(.evidence)"')"
gone -n "$NS" deploy pacto-evidence || fail "the operator runs pacto-evidence with no trust Secret"
pass "no pacto-evidence Deployment while the trust Secret is missing"

section "SCENARIO 2 (evidence_on): a trust Secret with a public key turns the Evidence Server on"
KEYDIR="$(mktemp -d)"
"$PACTO_BIN" evidence keygen --out "$KEYDIR" --key-id idp >/dev/null
kubectl -n "$NS" create secret generic "$TRUST" --from-file="$(ls "$KEYDIR"/*.pub)" >/dev/null
kubectl -n "$NS" rollout restart deploy/pacto-operator >/dev/null
wait_ready pacto-operator >/dev/null || fail "the restarted operator is not ready"
eventually 220 deploy_exists pacto-evidence || fail "the operator never created pacto-evidence once the trust Secret held a key"
wait_ready pacto-evidence 180s >/dev/null || fail "pacto-evidence is not Ready"
pass "pacto-evidence is Ready"
eventually 120 row_is evidence '.value == "on" and (.evidence | contains("serves OCI Referrers"))' \
  || fail "the evidence row never read on: $(row evidence)"
pass "evidence: $(row evidence | jq -r '"\(.value) \(.evidence)"')"

section "SCENARIO 3 (crossplane_bootstrap): idp.crossplane.install installs Crossplane"
helm upgrade "$REL" "$CHART" -n "$REL" --reuse-values --set idp.crossplane.install=true --wait --timeout 300s >/dev/null
wait_ready crossplane 180s >/dev/null || fail "the Crossplane Deployment is not Ready"
# The core serves its webhook once its init container created the CRDs and the
# webhook configurations, a little after the Deployment reads Ready.
eventually 60 fixture_xrd >/dev/null || fail "the fixture XRD $XRD was never accepted"
pass "Crossplane is Ready and accepted the fixture XRD $XRD"
eventually 120 row_is crossplane "$running_crossplane"' and (.value | test("^Crossplane v2\\.[0-9]+\\.[0-9]+$"))' \
  || fail "the crossplane row never read a running Crossplane v2: $(row crossplane)"
pass "crossplane: $(row crossplane | jq -r '"\(.value) \(.evidence)"')"
eventually 60 engine_is '. == "crossplane"' || { show_engine || true; fail "GET /api/capabilities does not read provisioning engine crossplane"; }
pass "GET /api/capabilities reads installation.provisioning.engine crossplane"
helm upgrade "$REL" "$CHART" -n "$REL" --reuse-values --wait --timeout 300s >/dev/null \
  || fail "a second helm upgrade of the release that installed Crossplane failed"
pass "a second helm upgrade with the same values succeeds: the release owns its Crossplane"

section "SCENARIO 4 (to_default): profile default keeps observation, CI reads and delivery"
CLUSTER_ID="$(detected_json | jq -r .cluster)"
REG_PF="$(pf "$LOCAL_REG_PORT" svc/pacto-registry 5000)"
pushed || { kill "$REG_PF" 2>/dev/null || true; fail "the operator never pushed $REG_HOST/acme/pacto-targets:cluster-$CLUSTER_ID"; }
snapshot_digest() {
  curl -fsS -o /dev/null -D - -H 'Accept: application/vnd.oci.image.manifest.v1+json' \
    "http://127.0.0.1:${LOCAL_REG_PORT}/v2/acme/pacto-targets/manifests/cluster-$CLUSTER_ID" |
    tr -d '\r' | sed -n 's/^[Dd]ocker-[Cc]ontent-[Dd]igest: //p'
}
before="$(snapshot_digest)"
helm upgrade "$REL" "$CHART" -n "$REL" --reuse-values --set profile=default --set delivery.enabled=true \
  --set idp.crossplane.install=false --wait --timeout 300s >/dev/null
argv="$(op_args)"
jq -e 'map(select(startswith("--evidence-server-auto") or startswith("--idp-recorder") or startswith("--enable-evidence-server"))) == []' <<<"$argv" >/dev/null \
  || fail "the default operator still runs an idp piece: $argv"
pass "no --evidence-server-auto, --idp-recorder or --enable-evidence-server argument"
eventually 40 gone -n "$NS" deploy pacto-evidence || fail "pacto-evidence outlived profile default"
eventually 40 gone -n "$NS" svc pacto-evidence || fail "the pacto-evidence Service outlived profile default"
[ -z "$(kubectl get deploy -A -l app.kubernetes.io/component=delivery-recorder -o name)" ] || fail "profile default runs a delivery recorder"
crossplane_deploys() { kubectl get deploy -A -l app.kubernetes.io/name=crossplane -o name; }
no_crossplane() { [ -z "$(crossplane_deploys)" ]; }
eventually 40 no_crossplane || fail "a Crossplane Deployment outlived idp.crossplane.install=false: $(crossplane_deploys)"
pass "no pacto-evidence, no recorder and no Crossplane Deployment"
kubectl get crd "$XRD_KIND" >/dev/null 2>&1 || fail "the Crossplane CRD $XRD_KIND went with the release"
kubectl get "$XRD_KIND" "$XRD" >/dev/null 2>&1 || fail "the fixture XRD $XRD went with the release"
pass "the Crossplane CRDs and the fixture XRD $XRD stay"
eventually 120 row_is crossplane '.value == "Crossplane APIs served, controller not found" and .completeness == "partial"' \
  || fail "the crossplane row does not read APIs served, controller not found (partial): $(row crossplane)"
pass "crossplane: $(row crossplane | jq -r '"\(.value) (\(.completeness)): \(.reason)"')"
eventually 60 engine_is '. != "crossplane"' || { show_engine || true; fail "GET /api/capabilities still reads provisioning engine crossplane"; }
pass "GET /api/capabilities no longer reads provisioning engine crossplane"
kubectl create ns idp-a >/dev/null 2>&1 || true
kubectl apply -f - >/dev/null <<YAML
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: {name: idp-after, namespace: idp-a}
spec:
  checkIntervalSeconds: 30
  contractRef:
    inline: |
      pactoVersion: '2.0'
      service: {name: idp-after, version: 1.0.0, owner: {team: audit, dri: d, contacts: [{type: email, value: a@e.com, purpose: escalation}]}}
      workload: service
      state: {type: stateless, persistence: {scope: local, durability: ephemeral}, dataCriticality: low}
  target: {workloadRef: {name: idp-after, kind: Deployment}}
YAML
eventually 20 has_status idp-a idp-after || fail "a Pacto applied under profile default never got a status"
pass "a Pacto applied under profile default reads $(pacto_status idp-a idp-after)"
pushed_again() { local d; d="$(snapshot_digest)"; [ -n "$d" ] && [ "$d" != "$before" ]; }
eventually 120 pushed_again || { kill "$REG_PF" 2>/dev/null || true; fail "the target snapshot cluster-$CLUSTER_ID was not pushed again after the upgrade"; }
kill "$REG_PF" 2>/dev/null || true
pass "the target snapshot push continues: cluster-$CLUSTER_ID moved from $before"
kubectl -n "$DNS" rollout status deploy/pacto-operator-delivery --timeout=180s >/dev/null || fail "the promoter is not Ready under profile default"
pass "the promoter stays Ready"
left="$(crossplane_webhooks)"
echo "  Crossplane left these webhook configurations: ${left:-none}"
if out="$(fixture_xrd changed 2>&1)"; then
  echo "  an XRD change with them in place was accepted"
else
  echo "  an XRD change with them in place was refused: $out"
fi
# shellcheck disable=SC2086  # one argument per object
[ -z "$left" ] || kubectl delete $left >/dev/null
eventually 10 fixture_xrd cleaned >/dev/null || fail "an XRD change is refused after the leftover webhook configurations were deleted: $(fixture_xrd cleaned 2>&1)"
pass "after deleting the leftover webhook configurations, an XRD change is accepted"

section "SCENARIO 5 (reenable): idp with the bootstrap renders again on the CRD-only cluster"
helm upgrade --dry-run=server "$REL" "$CHART" -n "$REL" --reuse-values --set profile=idp \
  --set idp.crossplane.install=true >/dev/null || fail "helm upgrade --dry-run=server back to idp with idp.crossplane.install=true failed"
pass "helm upgrade --dry-run=server to idp with idp.crossplane.install=true succeeds with the CRDs left behind"
row_is crossplane '.value == "Crossplane APIs served, controller not found" and .completeness == "partial"' \
  || fail "the CRDs alone read as a running Crossplane: $(row crossplane)"
pass "the CRDs alone still read APIs served, controller not found"
kubectl delete ns idp-a --wait=false >/dev/null 2>&1 || true

echo
echo "IDP E2E PASS"
