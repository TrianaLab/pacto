#!/usr/bin/env bash
# shellcheck disable=SC2329 # poll helpers are invoked by name, which shellcheck cannot see
# Kind acceptance for the Kargo promotion gate: one shared ClusterAnalysisTemplate
# runs `pacto impact --running` on the Freight's contract bundle after each
# promotion, and the Freight verifies only when no consumer that runs breaks.
#
# The template and the reader role published in
# integrations/kubernetes/docs/gitops-kargo.md are the thing under test, so this
# scenario applies them verbatim from tests/acceptance/kind/fixtures/gitops/ and
# changes exactly two lines of the template, each with a printed sed: the image
# (the locally built Pacto image) and PACTO_INSECURE_REGISTRIES (the in-cluster
# registry speaks plain HTTP).
#
# It runs in two phases, cheap first.
#
#   Phase 1 needs no cluster. The template's exit-policy script runs under `sh`
#   against a stub pacto for every exit, then inside the locally built image so
#   busybox runs it too, and `pacto impact` gives the scenario's three verdicts
#   over local folders: clear 0, blocked 1, unverified 2.
#   KARGO_OFFLINE_ONLY=1 stops here.
#
#   Phase 2 is a real Kargo with Argo Rollouts. Delivery is absent: the operator
#   runs with only the top-level `registry` value and publishes the signed
#   target snapshot the template reads by default (`targets: snapshot`), and the
#   Job's ServiceAccount has no RBAC at all, so consumers are known only through
#   that snapshot. The cases use disjoint provider and consumer pairs:
#     A clear       payments 1.1.0 verifies against the running 1.0.0;
#     B blocked     payments 2.0.0 fails: ledger runs and requires ^1;
#     C unverified  inventory 2.0.0 fails in test and verifies in test-lenient
#                   (allow-unverified: "true"): reports requires ^1 and runs
#                   nowhere known;
#     D targets k8s the reader role bound to the project repeats case B from a
#                   live read of this cluster.
set -euo pipefail
CLUSTER="${KIND_CLUSTER:-pacto-gitops-kargo}"
NS=pacto-system
APP_NS=demo
KARGO_NS=kargo
ROLLOUTS_NS=argo-rollouts
# REG_HOST is the registry Service's ClusterIP:5000, set once the registry runs:
# Kargo reads image tags over plain HTTP only from an RFC 1918 or loopback host,
# and asks a DNS name over HTTPS, which the in-cluster registry does not speak.
REG_HOST=
LOCAL_REG_PORT=5604
# Kargo v1.12.6 (2026-11-16) and Argo Rollouts v1.10.2 (2026-11-04), the latest
# stable releases when this scenario was written. Each download and image is
# pinned to what was read that day (sha256sum of the chart archive and the
# install manifest, `crane digest` of each image), so a moved tag cannot change
# what a run tests.
KARGO_VERSION=1.12.6
KARGO_CHART_SHA256=bd674ebf77f57f413323ebc861a4d3672d6eefd8ddaa05fac4e2ab687086bba0
KARGO_IMAGE="ghcr.io/akuity/kargo:v${KARGO_VERSION}@sha256:2212826176eb1326d75fa8ff31071c441e767233ac7355ef961566affaee873d"
ARGO_ROLLOUTS_VERSION=v1.10.2
ROLLOUTS_MANIFEST_SHA256=b8280994791eaee7eb91c8b84450efcc6e0292fca5aafc18c1b0ebc5d18dbefe
ROLLOUTS_IMAGE="quay.io/argoproj/argo-rollouts:${ARGO_ROLLOUTS_VERSION}@sha256:2a395642ea411ae7b09274a560c3934b16134dfe36f5ffaaa6054c8a21a2eecf"
PAUSE_IMG=registry.k8s.io/pause:3.9@sha256:7031c1b283388d2c2e09b57badb803c05ebed362dc88d84b480cc47f72a21097
HERE="$(cd "$(dirname "$0")" && pwd)"
FIXTURES="$HERE/fixtures/gitops"
TEMPLATE="$FIXTURES/kargo-cluster-analysis-template.yaml"
# shellcheck source=tests/acceptance/kind/lib.sh
source "$HERE/lib.sh"

dump_kargo() {
  echo "--- kargo stages, freight and promotions ---"
  kubectl get stages.kargo.akuity.io,freights.kargo.akuity.io,promotions.kargo.akuity.io -A -o yaml || true
  echo "--- analysisruns and jobs ---"
  kubectl get analysisruns.argoproj.io -A -o yaml || true
  kubectl get jobs,pods -A -l analysisrun.argoproj.io/uid -o wide || true
  kubectl logs -A -l analysisrun.argoproj.io/uid --tail=200 --prefix || true
  echo "--- logs kargo ---"
  kubectl -n "$KARGO_NS" logs -l app.kubernetes.io/name=kargo --all-containers --tail=300 --prefix || true
  echo "--- logs argo-rollouts ---"
  kubectl -n "$ROLLOUTS_NS" logs deploy/argo-rollouts --tail=300 || true
}
# shellcheck disable=SC2154  # rc is assigned by rc=$? inside the trap body
trap 'rc=$?; [ $rc -ne 0 ] && { dump_diag "$NS"; dump_kargo; }; pkill -f "kubectl.*port-forward" 2>/dev/null || true; exit $rc' EXIT

VER="$(release_version kubernetes)"
OP_REPO="localhost:5001/pacto-operator/pacto-controller"
OP_IMG="${OP_REPO}:${VER}"
DASH_IMG="localhost:5001/pacto-dashboard:$(release_version core)"

# contract_dir NAME VERSION WORKLOAD [DEPENDENCY_LINES] — a contract folder.
# A change of workload is BREAKING; a change of version alone is not.
contract_dir() {
  local d; d="$(mktemp -d)"
  cat > "$d/pacto.yaml" <<YAML
pactoVersion: '2.0'
service:
  name: $1
  version: $2
  owner: {team: team-$1, dri: d, contacts: [{type: email, value: a@e.com, purpose: escalation}]}
workload: $3
state: {type: stateless, persistence: {scope: local, durability: ephemeral}, dataCriticality: low}
${4:-}
YAML
  echo "$d"
}
# requires NAME RANGE REGISTRY — a required dependency on REGISTRY/demo/NAME.
requires() { echo "dependencies: [{name: $1, ref: \"oci://$3/demo/$1\", required: true, compatibility: \"$2\"}]"; }

# ---------------------------------------------------------------------------
section "phase 1: the template's exit policy under sh"
# The script block of the template's only container, as Kargo's Job runs it.
SCRIPT="$(awk '
  found && NF { match($0, /^ */); if (RLENGTH < indent) exit }
  found { print substr($0, indent + 1); next }
  /^ *- \|$/ { found = 1; getline; match($0, /^ */); indent = RLENGTH; print substr($0, indent + 1) }
' "$TEMPLATE")"
[ -n "$SCRIPT" ] || fail "no script block in $TEMPLATE"
! grep -qF '{{' <<<"$SCRIPT" || fail "the template script interpolates an arg into its own text"
STUB="$(mktemp -d)"; NOBIN="$(mktemp -d)"
cat > "$STUB/pacto" <<'SH'
#!/bin/sh
for a; do printf '[%s]' "$a"; done; echo
exit "${STUB_RC:-0}"
SH
chmod 755 "$STUB" "$NOBIN" "$STUB/pacto"
NEW_REF="oci://registry.invalid/demo/payments@sha256:$(printf '%064d' 1)"

# run_template RUNNER BIN_DIR STUB_RC TARGETS ALLOW_UNVERIFIED — the script with
# only BIN_DIR on PATH; sets OUT and RC. RUNNER is host (/bin/sh: dash on the CI
# runner) or image (busybox sh in the locally built image).
run_template() {
  RC=0
  if [ "$1" = host ]; then
    OUT="$(env -i PATH="$2" STUB_RC="$3" NEW="$NEW_REF" TARGETS="$4" ALLOW_UNVERIFIED="$5" /bin/sh -c "$SCRIPT" 2>&1)" || RC=$?
  else
    OUT="$(docker run --rm --entrypoint /bin/sh -v "$2:/bin-under-test:ro" -e PATH=/bin-under-test \
      -e STUB_RC="$3" -e NEW="$NEW_REF" -e TARGETS="$4" -e ALLOW_UNVERIFIED="$5" -e SCRIPT="$SCRIPT" \
      "$DASH_IMG" -c 'exec /bin/sh -c "$SCRIPT"' 2>&1)" || RC=$?
  fi
}
# expect_template RUNNER WANT_RC WANT_TEXT BIN_DIR STUB_RC TARGETS ALLOW_UNVERIFIED
expect_template() {
  local runner="$1" want="$2" text="$3" what; shift 3
  what="pacto exit $2"
  [ "$1" = "$NOBIN" ] && what="no pacto on PATH"
  run_template "$runner" "$@"
  if [ "$RC" != "$want" ] || ! grep -qF -- "$text" <<<"$OUT"; then
    fail "$runner: $what, targets=$3, allow-unverified=$4: want exit $want with '$text', got $RC: $OUT"
  fi
  pass "$runner: $what, targets=$3, allow-unverified=$4 -> exit $RC"
}
template_cases() {
  local r="$1" snap="[impact][--running][$NEW_REF][--local][]"
  expect_template "$r" 0 "$snap" "$STUB" 0 snapshot false
  expect_template "$r" 0 "${snap}[--k8s]" "$STUB" 0 k8s false
  expect_template "$r" 1 "impact exit 1" "$STUB" 1 snapshot false
  expect_template "$r" 1 "unverified fails unless allow-unverified is true" "$STUB" 2 snapshot false
  expect_template "$r" 0 "impact exit 2 passes" "$STUB" 2 snapshot true
  expect_template "$r" 1 "unverified fails unless allow-unverified is true" "$STUB" 2 snapshot yes
  expect_template "$r" 1 "impact exit 127" "$NOBIN" 0 snapshot true
  expect_template "$r" 1 "targets must be snapshot or k8s" "$STUB" 0 bogus true
  grep -qF '[impact]' <<<"$OUT" && fail "$r: a bad targets value still ran pacto: $OUT"
  return 0
}
template_cases host

if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
  section "phase 1: the same script under busybox sh, in the locally built image"
  load_built_images "$OP_IMG" "$DASH_IMG" || docker image inspect "$DASH_IMG" >/dev/null 2>&1 \
    || docker build --load -f "$_PACTO_ROOT/Dockerfile" -t "$DASH_IMG" "$_PACTO_ROOT"
  template_cases image
else
  echo "  docker is not available: the in-image run is skipped"
fi

section "phase 1: the three verdicts from pacto impact over local folders"
PACTO="$(build_pacto)"
NOW="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
OFFLINE_REG=registry.invalid
# verdict LABEL WANT_EXIT OLD NEW FLEET TARGETS — one `pacto impact` answer.
verdict() {
  local got=0 out
  out="$("$PACTO" impact "$3" "$4" --local "$5" --target-state "$6" 2>&1)" || got=$?
  [ "$got" = "$2" ] || fail "$1: pacto impact exited $got, want $2: $out"
  pass "$1: exit $got ($(grep 'Release gate:' <<<"$out"))"
}
target() { echo "  - {service: $1, name: demo/$1, scope: kind, resolvedRef: \"oci://$OFFLINE_REG/demo/$1:$2\", evidenceAt: \"$NOW\"}"; }
SHOP="$(mktemp -d)"; STOCK="$(mktemp -d)"
cp -R "$(contract_dir ledger 1.0.0 service "$(requires payments ^1.0.0 "$OFFLINE_REG")")" "$SHOP/ledger"
cp -R "$(contract_dir reports 1.0.0 service "$(requires inventory ^1.0.0 "$OFFLINE_REG")")" "$STOCK/reports"
SHOP_TARGETS="$(mktemp)"; STOCK_TARGETS="$(mktemp)"
printf 'schemaVersion: pacto.dev/fleet-targets/v1\ntargets:\n%s\n%s\n' "$(target payments 1.0.0)" "$(target ledger 1.0.0)" > "$SHOP_TARGETS"
printf 'schemaVersion: pacto.dev/fleet-targets/v1\ntargets:\n%s\n' "$(target inventory 1.0.0)" > "$STOCK_TARGETS"
PAY100="$(contract_dir payments 1.0.0 service)"
verdict "A clear: payments 1.0.0 -> 1.1.0" 0 "$PAY100" "$(contract_dir payments 1.1.0 service)" "$SHOP" "$SHOP_TARGETS"
verdict "B blocked: payments 1.0.0 -> 2.0.0, ledger runs" 1 "$PAY100" "$(contract_dir payments 2.0.0 job)" "$SHOP" "$SHOP_TARGETS"
verdict "C unverified: inventory 1.0.0 -> 2.0.0, reports runs nowhere known" 2 \
  "$(contract_dir inventory 1.0.0 service)" "$(contract_dir inventory 2.0.0 job)" "$STOCK" "$STOCK_TARGETS"

if [ -n "${KARGO_OFFLINE_ONLY:-}" ]; then
  echo "GITOPS KARGO OFFLINE PHASE PASS"
  exit 0
fi

# ---------------------------------------------------------------------------
for t in oras jq helm openssl; do command -v "$t" >/dev/null 2>&1 || fail "$t is required"; done
sha256() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d' ' -f1; }

section "phase 2: create the cluster and build the images"
cluster_start_bg
build_operator_images "$OP_IMG" "$DASH_IMG" "$VER"
for img in "$PAUSE_IMG" "$KARGO_IMAGE" "$ROLLOUTS_IMAGE"; do pull_pinned "$img"; done
KARGO_CHART_DIR="$(mktemp -d)"
fetch_kargo_chart() { helm pull oci://ghcr.io/akuity/kargo-charts/kargo --version "$KARGO_VERSION" -d "$KARGO_CHART_DIR" >/dev/null; }
infra "pull the Kargo chart $KARGO_VERSION" fetch_kargo_chart
KARGO_CHART="$KARGO_CHART_DIR/kargo-${KARGO_VERSION}.tgz"
[ "$(sha256 "$KARGO_CHART")" = "$KARGO_CHART_SHA256" ] || fail "the Kargo chart $KARGO_VERSION is not the archive this scenario pins"
ROLLOUTS_MANIFEST="$(mktemp)"
infra "download Argo Rollouts $ARGO_ROLLOUTS_VERSION" curl -fsSL --max-time 60 -o "$ROLLOUTS_MANIFEST" \
  "https://github.com/argoproj/argo-rollouts/releases/download/${ARGO_ROLLOUTS_VERSION}/install.yaml"
[ "$(sha256 "$ROLLOUTS_MANIFEST")" = "$ROLLOUTS_MANIFEST_SHA256" ] || fail "Argo Rollouts' install.yaml is not the manifest this scenario pins"
sed -i.bak 's/imagePullPolicy: Always/imagePullPolicy: IfNotPresent/' "$ROLLOUTS_MANIFEST"
OP_CHART="$(package_chart "$PACTO_CHART")"
ensure_cluster
# The dashboard image is the pacto CLI image the template's Job runs.
infra "load the images into the cluster" load_images "$OP_IMG" "$DASH_IMG" "${PAUSE_IMG%@*}" "${KARGO_IMAGE%@*}" "${ROLLOUTS_IMAGE%@*}"
for n in "$NS" "$APP_NS" "$KARGO_NS" "$ROLLOUTS_NS"; do
  kubectl create namespace "$n" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
done

section "the operator with only the registry value: delivery absent"
install_registry
REG_HOST="$(kubectl -n "$NS" get svc pacto-registry -o jsonpath='{.spec.clusterIP}'):5000"
REG_PF_PID="$(pf "$LOCAL_REG_PORT" svc/pacto-registry 5000)"
helm install pacto-operator "$OP_CHART" -n "$NS" \
  --set global.imageRegistry= --set "image.repository=$OP_REPO" --set "image.tag=$VER" --set "image.pullPolicy=Never" \
  --set dashboard.enabled=false --set registry="$REG_HOST" --set "insecureRegistries[0]=$REG_HOST" \
  --wait --timeout 240s >/dev/null
! helm get values pacto-operator -n "$NS" -o json | jq -e '.delivery.enabled == true' >/dev/null \
  || fail "the release sets delivery.enabled=true"
! kubectl get crd stages.delivery.pacto.trianalab.io >/dev/null 2>&1 || fail "the delivery Stage CRD is installed with delivery off"
[ -z "$(kubectl get deploy -A -o name | grep -E 'promoter|recorder|delivery' || true)" ] \
  || fail "a delivery Deployment runs with delivery off: $(kubectl get deploy -A -o name)"
CLUSTER_UID="$(kubectl get ns kube-system -o jsonpath='{.metadata.uid}')"
SNAPSHOT_TAG="127.0.0.1:${LOCAL_REG_PORT}/pacto-targets:cluster-${CLUSTER_UID}"
pass "no delivery value, no delivery Stage CRD, no promoter or recorder; cluster uid $CLUSTER_UID"

section "publish the bundles: payments and ledger for A, B and D; inventory and reports for C"
# push NAME VERSION WORKLOAD [DEPENDENCY_LINES] — the bundle under demo/NAME:VERSION;
# prints its in-cluster digest-pinned ref.
push() {
  local digest
  digest="$(push_bundle "$PACTO" "$LOCAL_REG_PORT" "$(contract_dir "$@")" "$1:$2" "$REG_HOST")"
  [ -n "$digest" ] || fail "pushing $1 $2 resolved no digest"
  echo "oci://${REG_HOST}/demo/$1@${digest}"
}
P100="$(push payments 1.0.0 service)"
push payments 1.1.0 service >/dev/null
L100="$(push ledger 1.0.0 service "$(requires payments ^1.0.0 "$REG_HOST")")"
I100="$(push inventory 1.0.0 service)"
push inventory 2.0.0 job >/dev/null
push reports 1.0.0 service "$(requires inventory ^1.0.0 "$REG_HOST")" >/dev/null
# payments 2.0.0 is pushed after case A, so the Warehouse's first Freight is 1.1.0.

# workload NAME — a Deployment running pause.
workload() {
  cat <<YAML
apiVersion: apps/v1
kind: Deployment
metadata: {name: $1, namespace: $APP_NS}
spec:
  replicas: 1
  selector: {matchLabels: {app: $1}}
  template:
    metadata: {labels: {app: $1}}
    spec:
      containers: [{name: app, image: "${PAUSE_IMG%@*}"}]
YAML
}
# service NAME — the Service in front of NAME, so a consumer of NAME resolves to
# ready endpoints: a required dependency on a provider with no Service is Unknown.
service() {
  cat <<YAML
apiVersion: v1
kind: Service
metadata: {name: $1, namespace: $APP_NS}
spec: {selector: {app: $1}, ports: [{port: 80}]}
YAML
}
# pacto_cr NAME REF — the Pacto that runs NAME's contract REF.
pacto_cr() {
  cat <<YAML
apiVersion: pacto.trianalab.io/v1
kind: Pacto
metadata: {name: $1, namespace: $APP_NS}
spec:
  checkIntervalSeconds: 30
  contractRef: {oci: "$2"}
  target: {serviceName: $1, workloadRef: {name: $1, kind: Deployment}}
YAML
}
# Digest-pinned, so the snapshot links each target to its revision exactly. The
# promotions below change nothing, so these stay the running revisions; reports
# runs nowhere.
{
  workload payments; echo ---; pacto_cr payments "$P100"; echo ---
  workload ledger; echo ---; pacto_cr ledger "$L100"; echo ---
  workload inventory; echo ---; pacto_cr inventory "$I100"; echo ---
  for s in payments ledger inventory; do service "$s"; echo ---; done
} | kubectl apply -f - >/dev/null
for s in payments ledger inventory; do
  wait_pacto_status "$APP_NS" "$s" Compliant || fail "$s never reached Compliant"
done

snapshot_digest() { oras resolve --plain-http "$SNAPSHOT_TAG" 2>/dev/null || true; }
# The operator's publisher pushes at start: restart it and wait for a new
# cluster-<uid> manifest, which now holds every Compliant target above.
before="$(snapshot_digest)"
kubectl -n "$NS" rollout restart deploy/pacto-operator >/dev/null
kubectl -n "$NS" rollout status deploy/pacto-operator --timeout=180s >/dev/null
republished() { local d; d="$(snapshot_digest)"; [ -n "$d" ] && [ "$d" != "$before" ]; }
eventually 60 republished || fail "the operator never republished pacto-targets:cluster-$CLUSTER_UID"
pass "the operator published pacto-targets:cluster-$CLUSTER_UID with payments 1.0.0, ledger and inventory 1.0.0 running"

section "the trust entry: the installation key, scoped to this cluster"
advertised() {
  SNAPSHOT_KEY="$(kubectl get cm -n "$NS" pacto-operator-detected -o jsonpath='{.data.detected\.json}' | jq -r '.installation.snapshotKey // empty')"
  [ -n "$SNAPSHOT_KEY" ]
}
eventually 30 advertised || fail "the detected ConfigMap advertises no installation.snapshotKey"
TRUST="$(mktemp)"
{
  echo "keys:"
  echo "  - name: cluster-${CLUSTER_UID}"
  echo "    publicKey: |"
  echo "      -----BEGIN PUBLIC KEY-----"
  fold -w 64 <<<"$SNAPSHOT_KEY" | sed 's/^/      /'
  echo "      -----END PUBLIC KEY-----"
  echo "    streams: [\"${CLUSTER_UID}/*/*\"]"
} >"$TRUST"

section "Argo Rollouts $ARGO_ROLLOUTS_VERSION and Kargo $KARGO_VERSION"
kubectl apply -n "$ROLLOUTS_NS" --server-side -f "$ROLLOUTS_MANIFEST" >/dev/null
infra "roll out Argo Rollouts" kubectl -n "$ROLLOUTS_NS" rollout status deploy/argo-rollouts --timeout=300s
# Kargo's webhook server serves TLS only. A self-signed certificate and its
# caBundle stand in for cert-manager, which the chart otherwise needs.
TLS="$(mktemp -d)"
openssl req -x509 -newkey rsa:2048 -nodes -days 2 -subj "/CN=kargo-webhooks-server.${KARGO_NS}.svc" \
  -addext "subjectAltName=DNS:kargo-webhooks-server.${KARGO_NS}.svc" \
  -keyout "$TLS/tls.key" -out "$TLS/tls.crt" 2>/dev/null
kubectl -n "$KARGO_NS" create secret tls kargo-webhooks-server-cert --cert="$TLS/tls.crt" --key="$TLS/tls.key" >/dev/null
# No API server (it needs an admin password and a signing key), no external
# webhooks, no Argo CD: the controller, the management controller and the
# webhook server are what verification needs.
helm install kargo "$KARGO_CHART" -n "$KARGO_NS" \
  --set api.enabled=false --set externalWebhooksServer.enabled=false --set garbageCollector.enabled=false \
  --set controller.argocd.integrationEnabled=false \
  --set webhooksServer.tls.selfSignedCert=false --set-file webhooksServer.tls.caBundle="$TLS/tls.crt" \
  --wait --timeout 300s >/dev/null
kargo_kinds() { kubectl get stages.kargo.akuity.io -A >/dev/null 2>&1 && kubectl get clusteranalysistemplates.argoproj.io >/dev/null 2>&1; }
eventually 40 kargo_kinds || fail "Kargo's and Rollouts' kinds never reached discovery"

section "apply the documented template, with two printed seds"
PATCHED="$(mktemp)"
echo "  sed: image -> $DASH_IMG; PACTO_INSECURE_REGISTRIES -> $REG_HOST"
sed -e "s|image: ghcr.io/trianalab/pacto/dashboard:[^ ]*|image: ${DASH_IMG}|" \
  -e "/name: PACTO_INSECURE_REGISTRIES/{n;s|value: \"\"|value: \"${REG_HOST}\"|;}" "$TEMPLATE" > "$PATCHED"
[ "$(diff "$TEMPLATE" "$PATCHED" | grep -c '^>')" = 2 ] || fail "the seds did not change exactly two lines: $(diff "$TEMPLATE" "$PATCHED")"
kubectl apply -f "$PATCHED" >/dev/null
kubectl get clusteranalysistemplates.argoproj.io pacto-impact >/dev/null || fail "pacto-impact is not served"
pass "ClusterAnalysisTemplate pacto-impact applied"

# project NAME — a Kargo Project, the pacto-impact ServiceAccount with no
# binding and the trust Secret in its namespace.
project() {
  kubectl apply -f - >/dev/null <<YAML
apiVersion: kargo.akuity.io/v1alpha1
kind: Project
metadata: {name: $1}
YAML
  ready() { [ "$(kubectl get projects.kargo.akuity.io "$1" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null)" = True ]; }
  eventually 40 ready "$1" || fail "Kargo Project $1 never became Ready: $(kubectl get projects.kargo.akuity.io "$1" -o yaml)"
  kubectl -n "$1" create serviceaccount pacto-impact >/dev/null
  kubectl -n "$1" create secret generic pacto-delivery-trust --from-file=trust.yaml="$TRUST" >/dev/null
}
# warehouse NS NAME — a subscription to demo/NAME, the default SemVer selection.
warehouse() {
  kubectl apply -f - >/dev/null <<YAML
apiVersion: kargo.akuity.io/v1alpha1
kind: Warehouse
metadata: {name: $2, namespace: $1}
spec:
  subscriptions:
    - image: {repoURL: "${REG_HOST}/demo/$2"}
YAML
}
# stage NS NAME WAREHOUSE [EXTRA_ARG_LINES] — the documented stanza, a no-op
# promotion and Freight straight from WAREHOUSE.
stage() {
  kubectl apply -f - >/dev/null <<YAML
apiVersion: kargo.akuity.io/v1alpha1
kind: Stage
metadata: {name: $2, namespace: $1}
spec:
  vars:
    - name: pactoBundle
      value: ${REG_HOST}/demo/$3
  requestedFreight:
    - origin: {kind: Warehouse, name: $3}
      sources: {direct: true}
  promotionTemplate:
    spec:
      steps:
        - {uses: compose-output, as: noop, config: {noop: "true"}}
  verification:
    analysisTemplates:
      - name: pacto-impact
        kind: ClusterAnalysisTemplate
    args:
      - name: new
        value: oci://\${{ vars.pactoBundle }}@\${{ imageFrom(vars.pactoBundle).Digest }}
${4:-}
YAML
}
freight_for() { # NS WAREHOUSE TAG — the Freight's name, empty until discovered
  kubectl -n "$1" get freights.kargo.akuity.io -o json 2>/dev/null \
    | jq -r --arg w "$2" --arg t "$3" '.items[] | select(.origin.name == $w and any(.images[]?; .tag == $t)) | .metadata.name' | head -1
}
discovered() { [ -n "$(freight_for "$@")" ]; }
promote() { # NS STAGE FREIGHT — Kargo's webhook names the Promotion and copies the Stage's steps
  kubectl create -f - >/dev/null <<YAML
apiVersion: kargo.akuity.io/v1alpha1
kind: Promotion
metadata: {generateName: $2-, namespace: $1}
spec: {stage: $2, freight: $3}
YAML
}
verification() { # NS STAGE FREIGHT — the newest verification phase of FREIGHT in STAGE
  kubectl -n "$1" get stages.kargo.akuity.io "$2" -o json \
    | jq -r --arg f "$3" '.status.freightHistory[0] // {} | select([.items[]?.name] | index($f)) | .verificationHistory[0].phase // empty'
}
verified() { case "$(verification "$1" "$2" "$3")" in Successful | Failed | Error) return 0 ;; esac; return 1; }
job_log() { # NS STAGE — the log of the Job of the Stage's newest AnalysisRun
  local run uid
  run="$(kubectl -n "$1" get stages.kargo.akuity.io "$2" -o jsonpath='{.status.freightHistory[0].verificationHistory[0].analysisRun.name}')"
  uid="$(kubectl -n "$1" get analysisruns.argoproj.io "$run" -o jsonpath='{.metadata.uid}')"
  kubectl -n "$1" logs -l "analysisrun.argoproj.io/uid=$uid" --tail=-1 2>&1 || true
}
# verify NS STAGE FREIGHT WANT_PHASE — promote FREIGHT to STAGE and wait for its
# verification; leaves the Job log in LOG.
verify() {
  promote "$1" "$2" "$3"
  eventually 100 verified "$1" "$2" "$3" \
    || fail "$2 never finished verifying $3: $(kubectl -n "$1" get stages.kargo.akuity.io "$2" -o jsonpath='{.status.conditions}')"
  local got; got="$(verification "$1" "$2" "$3")"
  LOG="$(job_log "$1" "$2")"
  [ "$got" = "$4" ] || fail "$1/$2: the verification of $3 is $got, want $4; Job log: $LOG"
}
has() { grep -qF -- "$1" <<<"$LOG" || fail "the Job log lacks '$1': $LOG"; }
can_i() { kubectl auth can-i "$@" --as="system:serviceaccount:shop:pacto-impact" 2>/dev/null || true; }

section "projects shop and stock: Warehouses on the bundle repositories, Stages with the stanza"
project shop
project stock
[ "$(can_i list pactos.pacto.trianalab.io)" = no ] || fail "shop's pacto-impact can list Pacto CRs before any binding"
warehouse shop payments
warehouse stock inventory
stage shop test payments
stage stock test inventory
stage stock test-lenient inventory '      - {name: allow-unverified, value: "true"}'
stage shop test-k8s payments '      - {name: targets, value: k8s}'
eventually 60 discovered shop payments 1.1.0 || fail "the payments Warehouse never discovered 1.1.0: $(kubectl -n shop get warehouses.kargo.akuity.io payments -o yaml)"
eventually 60 discovered stock inventory 2.0.0 || fail "the inventory Warehouse never discovered 2.0.0: $(kubectl -n stock get warehouses.kargo.akuity.io inventory -o yaml)"
pass "Freight payments 1.1.0 in shop and inventory 2.0.0 in stock"

section "A clear: payments 1.1.0 verifies against the running 1.0.0"
verify shop test "$(freight_for shop payments 1.1.0)" Successful
has "Running: 1 revision"
has "${P100##*@}"
has "Release gate: clear"
pass "Successful; the Running line names payments 1.0.0 by digest"

section "B blocked: payments 2.0.0 fails, ledger runs and requires ^1"
push payments 2.0.0 job >/dev/null
kubectl -n shop annotate warehouses.kargo.akuity.io payments "kargo.akuity.io/refresh=$(date +%s)" --overwrite >/dev/null
eventually 60 discovered shop payments 2.0.0 || fail "the payments Warehouse never discovered 2.0.0"
F200="$(freight_for shop payments 2.0.0)"
verify shop test "$F200" Failed
has "Release gate: blocked"
has ledger
has "${P100##*@}"
has "impact exit 1"
pass "Failed; the Job log names ledger and the 1.0.0 baseline"

section "C unverified: inventory 2.0.0 fails by default and verifies with allow-unverified"
I200="$(freight_for stock inventory 2.0.0)"
verify stock test "$I200" Failed
has "impact exit 2: unverified fails unless allow-unverified is true"
verify stock test-lenient "$I200" Successful
has "impact exit 2 passes: allow-unverified is true"
pass "Failed in test, Successful in test-lenient"

section "D targets k8s: the documented reader role, bound to shop"
kubectl apply -f "$FIXTURES/kargo-impact-rbac.yaml" >/dev/null
kubectl create clusterrolebinding pacto-impact-shop --clusterrole=pacto-impact-reader \
  --serviceaccount=shop:pacto-impact >/dev/null
for v in 'get namespaces/kube-system' 'list pactos.pacto.trianalab.io' 'get pactos.pacto.trianalab.io' 'list deployments.apps'; do
  # shellcheck disable=SC2086  # VERB RESOURCE, split on purpose
  [ "$(can_i $v)" = yes ] || fail "the reader cannot $v"
done
for v in 'get namespaces/default' 'get customresourcedefinitions' 'list nodes' 'get secrets' 'create pactos.pacto.trianalab.io'; do
  # shellcheck disable=SC2086  # VERB RESOURCE, split on purpose
  [ "$(can_i $v)" = no ] || fail "the reader can $v"
done
pass "the reader reads the cluster identity, Pacto CRs and workloads; no other namespace, CRD, node, Secret or write"
verify shop test-k8s "$F200" Failed
has "Release gate: blocked"
has ledger
# The live read names the running payments 1.0.0 by the digest its Pacto CR pins,
# though the registry now lists payments at 2.0.0.
has "${P100##*@}"
has "impact exit 1"
pass "Failed from a live read of cluster ${CLUSTER_UID:0:8}"

kill "$REG_PF_PID" 2>/dev/null || true
echo "GITOPS KARGO PROMOTION GATE PASS"
keep_or_teardown "$NS" "$CLUSTER" delete_cluster
