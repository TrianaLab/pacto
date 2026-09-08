#!/usr/bin/env bash
# Kind acceptance for the Argo CD half of the GitOps promotion gate: a violated
# Pacto contract turns its Argo Application red, and satisfying it turns it green.
#
# The health customization published in integrations/kubernetes/docs/gitops.md is
# the thing under test, and it fails SILENTLY. A `data` key Argo does not
# recognise is ignored, a Lua slip leaves the object unjudged, and both look
# exactly like having configured nothing: the Application stays green and nobody
# is told a check is missing. So this scenario runs the documented file itself,
# never a copy, from
# tests/acceptance/kind/fixtures/gitops/argocd-cm-pacto-health.yaml.
#
# It runs in two phases, cheap first.
#
#   Phase 1 needs no cluster at all. `argocd admin settings resource-overrides
#   health` evaluates the customization against a Pacto manifest using the same
#   Lua sandbox the server runs, so every contract verdict — including the ones a
#   live cluster cannot cheaply manufacture, like a stale observedGeneration —
#   gets an assertion. It fails in seconds when the mapping is wrong.
#
#   Phase 2 is the claim phase 1 cannot make: that a real Argo CD, reading that
#   ConfigMap out of its own namespace, reports the Application Degraded. It runs
#   the operator, the manifests and the verdict end to end.
set -euo pipefail
CLUSTER="${KIND_CLUSTER:-pacto-gitops-argo}"
NS=pacto-system
APP_NS=demo
ARGO_NS=argocd
REG_HOST="pacto-registry.${NS}.svc.cluster.local:5000"
LOCAL_REG_PORT=5602
# ONE pin for both halves. The CLI in phase 1 and the server in phase 2 must be
# the same release, or phase 1 is evidence about a Lua sandbox nobody deployed.
# Pinned to a version with OCI application sources (v3.1.0 and later), which is
# what lets this scenario reuse the in-cluster registry instead of standing up a
# git server for Argo to clone from.
ARGOCD_VERSION=v3.5.2
# What ORAS labels the manifest layer. Argo allows this media type by default
# (cmd/argocd-repo-server: application/vnd.oci.image.layer.v1.tar+gzip) and
# refuses an artifact carrying anything but exactly one content layer.
ARGO_LAYER_TYPE=application/vnd.oci.image.layer.v1.tar+gzip
MANIFEST_REPO="demo/orders-manifests"
HERE="$(cd "$(dirname "$0")" && pwd)"
FIXTURE="$HERE/fixtures/gitops/argocd-cm-pacto-health.yaml"
HEALTH_KEY='resource.customizations.health.pacto.trianalab.io_Pacto'
# shellcheck source=tests/acceptance/kind/lib.sh
source "$HERE/lib.sh"

echo "== phase 1: the documented customization, judged offline =="

# The CLI is cached by version rather than taken from PATH: a developer's argocd
# is whatever they last installed, and the whole point of the pin is that this
# check speaks for the server phase 2 deploys.
ARGOCD_BIN=""
install_argocd_cli() {
  local os arch
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  case "$(uname -m)" in
    x86_64 | amd64) arch=amd64 ;;
    arm64 | aarch64) arch=arm64 ;;
    *) fail "no argocd release asset for $(uname -m)" ;;
  esac
  ARGOCD_BIN="${TMPDIR:-/tmp}/pacto-argocd-${ARGOCD_VERSION}-${os}-${arch}"
  [ -x "$ARGOCD_BIN" ] && return 0
  # Downloaded to .part and renamed, so an interrupted download can never be
  # reused as a working binary by the next run.
  curl -sSfL -o "${ARGOCD_BIN}.part" \
    "https://github.com/argoproj/argo-cd/releases/download/${ARGOCD_VERSION}/argocd-${os}-${arch}" \
    || fail "could not download the argocd CLI ${ARGOCD_VERSION} for ${os}/${arch}"
  chmod +x "${ARGOCD_BIN}.part"
  mv "${ARGOCD_BIN}.part" "$ARGOCD_BIN"
}
install_argocd_cli

# verdict CONTRACT_STATUS [GENERATION] [OBSERVED_GENERATION] — a Pacto manifest
# carrying one verdict. The spec is minimal on purpose: the script under test
# reads status and metadata.generation and nothing else.
verdict() {
  cat <<YAML
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: { name: orders, namespace: $APP_NS, generation: ${2:-1} }
spec: { contractRef: { inline: "pactoVersion: '2.0'" } }
status: { contractStatus: $1, observedGeneration: ${3:-1} }
YAML
}

# health_case NAME WANT_STATUS WANT_MESSAGE_SUBSTRING < MANIFEST
health_case() {
  local name="$1" want_status="$2" want_msg="$3" manifest out status message
  manifest="$(mktemp)"
  cat > "$manifest"
  out="$("$ARGOCD_BIN" admin settings resource-overrides health "$manifest" \
    --argocd-cm-path "$FIXTURE" 2>&1)" \
    || fail "$name: argocd could not evaluate the customization: $out"
  # This is the silent failure the whole phase exists for, and argocd reports it
  # on stdout while exiting 0 — verified against ${ARGOCD_VERSION}. Read the
  # output; the exit status cannot tell configured from unconfigured.
  if grep -q 'Health script is not configured' <<< "$out"; then
    fail "$name: Argo found no health check for pacto.trianalab.io/Pacto — $(basename "$FIXTURE") does not carry a key Argo recognises"
  fi
  status="$(sed -n 's/^STATUS: //p' <<< "$out")"
  message="$(sed -n 's/^MESSAGE: //p' <<< "$out")"
  [ "$status" = "$want_status" ] \
    || fail "$name: expected STATUS $want_status, got '$status' (message: $message)"
  grep -qF "$want_msg" <<< "$message" \
    || fail "$name: expected the message to contain '$want_msg', got '$message'"
  pass "$name -> $status: $message"
}

# Every contractStatus in the CRD enum, plus the two states a live cluster passes
# through too fast to assert. None of them expects Argo's `Unknown`: it ranks
# worse than Degraded in the Application roll-up and does not fire the
# on-degraded trigger, and the page promises nothing lands there.
health_case "Compliant"      Healthy     "contract satisfied"                   < <(verdict Compliant)
health_case "Reference"      Healthy     "contract satisfied"                   < <(verdict Reference)
health_case "Warning"        Healthy     "contract satisfied with warnings"     < <(verdict Warning)
health_case "NonCompliant"   Degraded    "contract violated"                    < <(verdict NonCompliant)
health_case "Invalid"        Degraded    "invalid"                              < <(verdict Invalid)
health_case "Unknown"        Progressing "verdict pending: Unknown"             < <(verdict Unknown)
health_case "NotEvaluated"   Progressing "verdict pending: NotEvaluated"        < <(verdict NotEvaluated)
# The fail-closed property: a verdict added in some later release must hold the
# promotion rather than pass it.
health_case "a verdict added later" Progressing "verdict pending: Provisional"  < <(verdict Provisional)
health_case "verdict behind the contract" Progressing "not yet evaluated at this generation" < <(verdict Compliant 2 1)

# A Pacto the operator has not reached yet. Written out rather than produced by
# `verdict`, because the absent field IS the case.
health_case "no status yet" Progressing "waiting for the first contract evaluation" <<YAML
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: { name: orders, namespace: $APP_NS, generation: 1 }
spec: { contractRef: { inline: "pactoVersion: '2.0'" } }
YAML

# The findings loop is what puts the reason in the Argo UI instead of a bare red
# dot, and it is the one branch that reads a nested list under the sandbox's
# restrictions.
health_case "the reason reaches the UI" Degraded "WORKLOAD_MISMATCH" <<YAML
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: { name: orders, namespace: $APP_NS, generation: 1 }
spec: { contractRef: { inline: "pactoVersion: '2.0'" } }
status:
  contractStatus: NonCompliant
  observedGeneration: 1
  findings:
    - { severity: warning, code: CONFIGURATION_ABSENT, message: not the headline }
    - { severity: error, code: WORKLOAD_MISMATCH, message: declared job, observed Deployment }
YAML

echo "== phase 2: the same file, inside a real Argo CD =="

# Argo lives outside $NS, so dump_diag alone cannot explain a red — or wrongly
# green — Application: the reason is in the Application's own conditions, in the
# per-resource health Argo computed, and in the repo-server's log when the OCI
# source is what failed.
dump_argo() {
  echo "--- argocd applications ---"
  kubectl -n "$ARGO_NS" get applications -o wide || true
  kubectl -n "$ARGO_NS" describe application orders || true
  echo "--- resource health as Argo computed it ---"
  kubectl -n "$ARGO_NS" get application orders \
    -o jsonpath='{range .status.resources[*]}{.kind}{"/"}{.name}{" health="}{.health.status}{" msg="}{.health.message}{"\n"}{end}' || true
  echo "--- the customization argocd-cm actually holds ---"
  kubectl -n "$ARGO_NS" get cm argocd-cm -o jsonpath="{.data.${HEALTH_KEY//./\\.}}" || true
  echo
  echo "--- $APP_NS objects ---"
  kubectl -n "$APP_NS" get all,pactos || true
  for c in argocd-repo-server argocd-application-controller; do
    echo "--- logs $c ($ARGO_NS) ---"
    kubectl -n "$ARGO_NS" logs -l "app.kubernetes.io/name=$c" --tail=200 --all-containers || true
  done
}
# shellcheck disable=SC2154  # rc is assigned by rc=$? inside the trap body
trap 'rc=$?; [ $rc -ne 0 ] && { dump_diag "$NS"; dump_argo; }; pkill -f "kubectl.*port-forward" 2>/dev/null || true; exit $rc' EXIT

command -v oras > /dev/null 2>&1 \
  || fail "oras is required: it publishes the manifest artifact Argo's OCI source pulls"

VER="$(release_version kubernetes)"
CORE="$(release_version core)"
OP_IMG="localhost:5001/pacto-operator/pacto-controller:${VER}"
OP_REPO="localhost:5001/pacto-operator/pacto-controller"
DASH_IMG="localhost:5001/pacto-dashboard:${CORE}"

build_operator_images "$OP_IMG" "$DASH_IMG" "$VER"

echo "== package the chart =="
CHART="$(package_chart "$PACTO_CHART")"

ensure_cluster
# The dashboard is off for this scenario, so only the operator image is loaded.
load_images "$OP_IMG"
for ns in "$NS" "$APP_NS" "$ARGO_NS"; do
  kubectl create namespace "$ns" --dry-run=client -o yaml | kubectl apply -f - > /dev/null
done
# $APP_NS is created OUTSIDE Argo's inventory on purpose: with automated pruning
# on, a namespace inside it would take the Pacto CR under test down with it.

echo "== the operator =="
helm install pacto-operator "$CHART" -n "$NS" \
  --set image.repository="$OP_REPO" --set image.tag="$VER" --set image.pullPolicy=Never \
  --set dashboard.enabled=false --wait --timeout 240s

echo "== an in-cluster OCI registry to publish the Application's manifests to =="
install_registry
REG_PF_PID="$(pf "$LOCAL_REG_PORT" svc/pacto-registry 5000)"

# publish WORKLOAD TAG — the Application's manifests, as the OCI artifact Argo
# pulls. WORKLOAD is what the contract DECLARES; the workload that actually ships
# is always a Deployment, so `job` is a contract that contradicts the cluster.
publish() {
  local workload="$1" tag="$2" tree tgz
  tree="$(mktemp -d)"
  tgz="$(mktemp -t orders-manifests.XXXXXX)"

  cat > "$tree/deployment.yaml" << YAML
apiVersion: apps/v1
kind: Deployment
metadata: { name: orders, namespace: $APP_NS }
spec:
  replicas: 1
  selector: { matchLabels: { app: orders } }
  template:
    metadata: { labels: { app: orders } }
    spec:
      containers:
        - { name: app, image: registry.k8s.io/pause:3.9 }
YAML
  # workloadRef carries BOTH name and kind, which is what makes the mismatch
  # assertable: without an explicit kind the operator reports EVIDENCE_INSUFFICIENT
  # (Unknown) rather than WORKLOAD_MISMATCH, and the Application would go amber
  # for a reason this scenario did not set up.
  cat > "$tree/pacto.yaml" << YAML
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: { name: orders, namespace: $APP_NS }
spec:
  checkIntervalSeconds: 30
  contractRef:
    inline: |
      pactoVersion: '2.0'
      service: {name: orders, version: 1.0.0, owner: {team: audit, dri: d, contacts: [{type: email, value: a@e.com, purpose: escalation}]}}
      workload: $workload
      state: {type: stateless, persistence: {scope: local, durability: ephemeral}, dataCriticality: low}
  target: {workloadRef: {name: orders, kind: Deployment}}
YAML

  tar -czf "$tgz" -C "$tree" .
  # --disable-path-validation: $tgz is absolute and ORAS refuses an absolute file
  # argument unless told the path is deliberate. It is; nothing here is user input.
  oras push --plain-http --disable-path-validation \
    "127.0.0.1:${LOCAL_REG_PORT}/${MANIFEST_REPO}:${tag}" "${tgz}:${ARGO_LAYER_TYPE}" > /dev/null
  echo "  published $tag (contract declares workload: $workload)"
}

echo "== publish v1: a contract that CONTRADICTS the workload that ships =="
publish job v1

echo "== Argo CD $ARGOCD_VERSION (core install: no API server, no UI) =="
# The manifests come from the repository at the tag, not from the GitHub release:
# Argo attaches CLI binaries to a release and nothing else, so there is no
# core-install.yaml asset to download.
#
# --server-side: Argo's Application CRD is far past the size a
# last-applied-configuration annotation can hold, and a client-side apply of it
# fails on exactly that.
kubectl apply -n "$ARGO_NS" --server-side \
  -f "https://raw.githubusercontent.com/argoproj/argo-cd/${ARGOCD_VERSION}/manifests/core-install.yaml" > /dev/null
# The apply returns as soon as the CRD objects are written, which is before the
# API server serves argoproj.io in discovery. Anything applied in that window
# fails with `no matches for kind`, which reads like a manifest problem. Asking
# for the resources is the check, rather than `kubectl wait --for=established`:
# that errors outright on a CRD whose status has not been written yet, which is
# the very window being waited out.
argo_crds_served() {
  kubectl get appprojects.argoproj.io -A > /dev/null 2>&1 \
    && kubectl get applications.argoproj.io -A > /dev/null 2>&1
}
eventually 40 argo_crds_served || fail "argoproj.io never reached discovery after the core install"
# Nothing here reconciles an ApplicationSet, and unlike Flux's notification
# controller nothing posts to it, so scaling it away costs no diagnostics.
kubectl -n "$ARGO_NS" scale deploy argocd-applicationset-controller --replicas=0 > /dev/null 2>&1 || true

# The `default` AppProject is created by the API server on startup, and the core
# install has no API server. Without it every Application sits at
# InvalidSpecError with no sync attempted, which looks nothing like an OCI or a
# health-check problem and would send a reader debugging the wrong half.
kubectl apply -f - > /dev/null << YAML
apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata: { name: default, namespace: $ARGO_NS }
spec:
  sourceRepos: ['*']
  destinations: [{ server: '*', namespace: '*' }]
  clusterResourceWhitelist: [{ group: '*', kind: '*' }]
YAML

# Argo computes per-resource health either way; this only decides where it is
# written. By default the result stays in the controller's cache, which the UI
# and `argocd app get` read through the API server — and there is no API server
# here. Persisting it puts the same verdict on .status.resources[] where kubectl
# can read it, so this scenario asserts on Argo's judgement rather than on its
# own re-derivation of it. The controller reads the flag once at startup.
kubectl -n "$ARGO_NS" patch configmap argocd-cmd-params-cm --type merge \
  -p '{"data":{"controller.resource.health.persist":"true"}}' > /dev/null
kubectl -n "$ARGO_NS" rollout restart statefulset/argocd-application-controller > /dev/null

kubectl -n "$ARGO_NS" rollout status deploy/argocd-redis --timeout=300s
kubectl -n "$ARGO_NS" rollout status deploy/argocd-repo-server --timeout=300s
kubectl -n "$ARGO_NS" rollout status statefulset/argocd-application-controller --timeout=300s

echo "== apply the DOCUMENTED customization, verbatim, the way the page says to =="
kubectl -n "$ARGO_NS" patch configmap argocd-cm --type merge --patch-file "$FIXTURE" > /dev/null

# Read it back before anything depends on it. argocd-cm is a plain ConfigMap:
# every key is accepted, and one Argo does not recognise is dropped on the floor
# at read time with no event, no log line and a green Application. This is the
# same check the page tells a reader to run.
LIVE_SCRIPT="$(kubectl -n "$ARGO_NS" get cm argocd-cm -o jsonpath="{.data.${HEALTH_KEY//./\\.}}")"
[ -n "$LIVE_SCRIPT" ] \
  || fail "argocd-cm has no $HEALTH_KEY after the merge patch — Argo would judge nothing"
# Argo's own configuration has to survive the patch, or the page is telling
# readers to break their installation.
kubectl -n "$ARGO_NS" get cm argocd-cm -o jsonpath='{.metadata.labels.app\.kubernetes\.io/part-of}' \
  | grep -q argocd || fail "the merge patch replaced argocd-cm instead of adding to it"
pass "the customization is live in argocd-cm and Argo's own keys survived"

echo "== point an Application at the in-cluster registry =="
# type: oci and insecureOCIForceHttp are the whole reason this Secret exists: the
# registry speaks plain HTTP, and without the Secret Argo would try TLS against
# it and fail in the repo-server rather than anywhere a reader would look.
kubectl apply -f - > /dev/null << YAML
apiVersion: v1
kind: Secret
metadata:
  name: orders-manifests
  namespace: $ARGO_NS
  labels: { argocd.argoproj.io/secret-type: repository }
stringData:
  name: orders-manifests
  type: oci
  url: oci://${REG_HOST}/${MANIFEST_REPO}
  insecureOCIForceHttp: "true"
---
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata: { name: orders, namespace: $ARGO_NS }
spec:
  project: default
  source:
    repoURL: oci://${REG_HOST}/${MANIFEST_REPO}
    targetRevision: v1
    path: .
  destination: { server: https://kubernetes.default.svc, namespace: $APP_NS }
  syncPolicy:
    automated: { prune: true }
YAML

app_sync() { kubectl -n "$ARGO_NS" get application orders -o jsonpath='{.status.sync.status}' 2>/dev/null || true; }
app_health() { kubectl -n "$ARGO_NS" get application orders -o jsonpath='{.status.health.status}' 2>/dev/null || true; }
pacto_health() { kubectl -n "$ARGO_NS" get application orders -o jsonpath='{.status.resources[?(@.kind=="Pacto")].health.status}' 2>/dev/null || true; }
pacto_msg() { kubectl -n "$ARGO_NS" get application orders -o jsonpath='{.status.resources[?(@.kind=="Pacto")].health.message}' 2>/dev/null || true; }

echo "== A Argo pulls the artifact and applies it =="
# Asserted on its own so an OCI problem — plain HTTP refused, the wrong layer
# media type, a path that resolves to nothing — reads as an OCI problem rather
# than as a gate that failed to close.
synced() { [ "$(app_sync)" = "Synced" ]; }
eventually 60 synced || fail "Argo never synced the OCI source: sync=$(app_sync)"
kubectl -n "$APP_NS" get pacto orders > /dev/null 2>&1 \
  || fail "Argo reports Synced but the Pacto from the artifact is not in the cluster"
# Everything after this reads Argo's per-resource verdict off the Application.
# Argo records here where it put that verdict: `appTree` means the cache, and
# inline is the zero value, so persisting leaves the field absent. Every later
# health read would come back empty under `appTree`, which is indistinguishable
# from a customization that never took.
if [ "$(kubectl -n "$ARGO_NS" get application orders -o jsonpath='{.status.resourceHealthSource}')" = "appTree" ]; then
  fail "Argo is keeping per-resource health in its cache, not on the Application: controller.resource.health.persist did not reach the controller"
fi
pass "Argo pulled v1 over plain HTTP and applied it"

echo "== B the verdict arrives =="
wait_pacto_status "$APP_NS" orders NonCompliant || fail "the violated contract never reached NonCompliant"
kubectl -n "$APP_NS" get pacto orders -o jsonpath='{range .status.findings[*]}{.code}{" "}{end}' \
  | grep -q WORKLOAD_MISMATCH || fail "NonCompliant, but not for the reason this scenario set up"
pass "WORKLOAD_MISMATCH: the contract contradicts the workload that shipped"

echo "== C Argo turns the Application red, and names the reason =="
# The workload itself is healthy. If the Application went Degraded for any other
# reason the customization would look like it worked while proving nothing, so
# the Deployment is asserted first and the Pacto's own health has to carry the
# finding code.
kubectl -n "$APP_NS" rollout status deployment/orders --timeout=180s
pacto_degraded() { [ "$(pacto_health)" = "Degraded" ]; }
eventually 40 pacto_degraded \
  || fail "Argo never judged the Pacto Degraded: health='$(pacto_health)' (empty means the customization did not take)"
grep -q WORKLOAD_MISMATCH <<< "$(pacto_msg)" \
  || fail "Argo judged the Pacto Degraded without the reason: $(pacto_msg)"
app_red() {
  [ "$(app_health)" = "Degraded" ] \
    || { echo "  the Application read $(app_health) while the contract was violated"; return 1; }
}
# Held for 60s: a single check cannot distinguish "stayed red" from "went red
# once and recovered on the next reconcile", and only the first is the claim.
always 20 app_red || fail "the Application did not hold Degraded while the contract was violated"
pass "Application Degraded, Pacto health: $(pacto_msg)"

echo "== D fix the contract and publish v2 =="
publish service v2
kubectl -n "$ARGO_NS" patch application orders --type merge \
  -p '{"spec":{"source":{"targetRevision":"v2"}}}' > /dev/null

wait_pacto_status "$APP_NS" orders Compliant || fail "the corrected contract never reached Compliant"
app_green() { [ "$(app_health)" = "Healthy" ] && [ "$(pacto_health)" = "Healthy" ]; }
eventually 60 app_green \
  || fail "the Application never recovered: app=$(app_health) pacto=$(pacto_health)"
# The page tells a reader to look for this exact message, and an empty one is how
# a customization that never took presents itself.
[ "$(pacto_msg)" = "contract satisfied" ] \
  || fail "the Application is Healthy, but not because the customization said so: '$(pacto_msg)'"
pass "the Application went green: $(pacto_msg)"

echo "== E the page's own read-back recipe, run against this cluster =="
# Dump argocd-cm and the Pacto and ask the CLI what Argo makes of them — exactly
# what the page tells a reader to do when a gate looks configured and is not. A
# troubleshooting recipe nobody runs is one that quietly stops working, and this
# one is the only way to tell a live customization from an ignored key.
CM_DUMP="$(mktemp)"
CR_DUMP="$(mktemp)"
kubectl -n "$ARGO_NS" get cm argocd-cm -o yaml > "$CM_DUMP"
kubectl -n "$APP_NS" get pacto orders -o yaml > "$CR_DUMP"
RECIPE="$("$ARGOCD_BIN" admin settings resource-overrides health "$CR_DUMP" --argocd-cm-path "$CM_DUMP" 2>&1)"
grep -q '^STATUS: Healthy' <<< "$RECIPE" \
  || fail "the documented read-back recipe does not report the status the page says: $RECIPE"
grep -q '^MESSAGE: contract satisfied' <<< "$RECIPE" \
  || fail "the documented read-back recipe does not report the message the page says: $RECIPE"
pass "the recipe agrees with the cluster"

kill "$REG_PF_PID" 2>/dev/null || true
echo "GITOPS ARGO CD PROMOTION GATE PASS"
keep_or_teardown "$NS" "$CLUSTER" delete_cluster
