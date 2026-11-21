#!/usr/bin/env bash
# shellcheck disable=SC2329 # poll helpers are invoked by name, which shellcheck cannot see
# Kind acceptance for the Argo CD sync gate: a Pacto Config Management Plugin
# sidecar on argocd-repo-server judges every render with
# `pacto impact --running` before Argo CD applies anything.
#
# The plugin files published in integrations/kubernetes/docs/gitops.md are the
# thing under test, so this scenario applies them verbatim from
# tests/acceptance/kind/fixtures/gitops/ and overrides exactly three things on
# the repo-server: the init container image (the locally built Pacto image),
# PACTO_TARGET_SNAPSHOT (this cluster's pinned tag) plus
# PACTO_INSECURE_REGISTRIES (the in-cluster registry speaks plain HTTP), and,
# for the last case, PACTO_ALLOW_UNVERIFIED.
#
# Delivery is absent: the operator runs with only the top-level `registry`
# value, and publishes the signed target snapshot the plugin reads, signed with
# the installation key it advertises in its detected ConfigMap.
#
# It proves against a real Argo CD:
#   - clear: a NON_BREAKING change renders and syncs, and the documented health
#     customization still judges the Pacto it applied;
#   - blocked: a BREAKING change a running consumer rejects fails the render
#     with `pacto impact: BLOCKED`, the sidecar log names that consumer and
#     nothing is applied;
#   - mismatch: a rendered contractRef that disagrees with pacto.yaml fails;
#   - unverified: a change Pacto cannot clear fails by default, an Application
#     cannot grant itself the opt-in, and the installation's per-application
#     mode lets it through;
#   - the repo-server gains no write access to Applications, the PreSync
#     fallback Job is a valid object, and a Helm chart renders through the
#     plugin with the same capabilities as Argo CD's native Helm.
set -euo pipefail
CLUSTER="${KIND_CLUSTER:-pacto-gitops-argo-sync}"
NS=pacto-system
APP_NS=demo
ARGO_NS=argocd
REG_HOST="pacto-registry.${NS}.svc.cluster.local:5000"
LOCAL_REG_PORT=5603
# The same Argo CD release and images as gitops-argocd.sh and delivery-argocd.sh;
# the plugin sidecar runs this image too (TestArgoPluginSidecarTagMatchesKindPin).
ARGOCD_VERSION=v3.5.2
ARGO_IMAGES=(quay.io/argoproj/argocd:v3.5.2@sha256:e2aadfae709d904e87f46ba4aa49601d827b3022db22cd4d03aae816a2e7097b
  public.ecr.aws/docker/library/redis:8.2.3-alpine@sha256:08ad0b1d280850169a790dba1393ff7a90aef951fc19632cf4d3ce4f78e679ba)
PAUSE_IMG=registry.k8s.io/pause:3.9@sha256:7031c1b283388d2c2e09b57badb803c05ebed362dc88d84b480cc47f72a21097
ARGO_LAYER_TYPE=application/vnd.oci.image.layer.v1.tar+gzip
HERE="$(cd "$(dirname "$0")" && pwd)"
FIXTURES="$HERE/fixtures/gitops"
HEALTH_KEY='resource.customizations.health.pacto.trianalab.io_Pacto'
# shellcheck source=tests/acceptance/kind/lib.sh
source "$HERE/lib.sh"

dump_argo() {
  echo "--- argocd applications ---"
  kubectl -n "$ARGO_NS" get applications -o yaml || true
  echo "--- $APP_NS objects ---"
  kubectl -n "$APP_NS" get all,pactos -o wide || true
  echo "--- repo-server pod ---"
  kubectl -n "$ARGO_NS" describe pod -l app.kubernetes.io/name=argocd-repo-server || true
  echo "--- logs argocd-repo-server, pacto sidecar included ---"
  kubectl -n "$ARGO_NS" logs -l app.kubernetes.io/name=argocd-repo-server --tail=300 --all-containers --prefix || true
  echo "--- logs argocd-application-controller ---"
  kubectl -n "$ARGO_NS" logs -l app.kubernetes.io/name=argocd-application-controller --tail=200 || true
}
# shellcheck disable=SC2154  # rc is assigned by rc=$? inside the trap body
trap 'rc=$?; [ $rc -ne 0 ] && { dump_diag "$NS"; dump_argo; }; pkill -f "kubectl.*port-forward" 2>/dev/null || true; exit $rc' EXIT

for t in oras jq helm; do command -v "$t" >/dev/null 2>&1 || fail "$t is required"; done

VER="$(release_version kubernetes)"
OP_REPO="localhost:5001/pacto-operator/pacto-controller"
OP_IMG="${OP_REPO}:${VER}"
DASH_IMG="localhost:5001/pacto-dashboard:$(release_version core)"

section "create the cluster and build the images"
cluster_start_bg
build_operator_images "$OP_IMG" "$DASH_IMG" "$VER"
pull_pinned "$PAUSE_IMG"
# pull_argo REPO:TAG@DIGEST — pull_pinned, except that public.ecr.aws, which
# throttles anonymous pulls from shared runners, falls back to the Docker Hub
# official image its docker/library images mirror (same digest).
pull_argo() {
  local ref="${1%@*}" digest="${1#*@}" hub
  case "$ref" in
    public.ecr.aws/docker/library/*)
      hub="docker.io/library/${ref#public.ecr.aws/docker/library/}"
      if docker pull -q "${ref%:*}@$digest" >/dev/null 2>&1; then
        docker tag "${ref%:*}@$digest" "$ref"
      else
        infra "pull $hub" docker pull -q "${hub%:*}@$digest" >/dev/null
        docker tag "${hub%:*}@$digest" "$ref"
      fi
      ;;
    *) pull_pinned "$1" ;;
  esac
}
for img in "${ARGO_IMAGES[@]}"; do pull_argo "$img"; done
PACTO="$(build_pacto)"
OP_CHART="$(package_chart "$PACTO_CHART")"
ensure_cluster
# The dashboard image is loaded for its pacto binary: the plugin's init
# container copies it into the sidecar.
infra "load the images into the cluster" load_images "$OP_IMG" "$DASH_IMG" "${PAUSE_IMG%@*}" "${ARGO_IMAGES[@]%@*}"
for n in "$NS" "$APP_NS" "$ARGO_NS"; do
  kubectl create namespace "$n" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
done

section "the operator with only the registry value: delivery absent"
install_registry
REG_PF_PID="$(pf "$LOCAL_REG_PORT" svc/pacto-registry 5000)"
helm install pacto-operator "$OP_CHART" -n "$NS" \
  --set global.imageRegistry= --set "image.repository=$OP_REPO" --set "image.tag=$VER" --set "image.pullPolicy=Never" \
  --set dashboard.enabled=false --set registry="$REG_HOST" --set "insecureRegistries[0]=$REG_HOST" \
  --wait --timeout 240s >/dev/null
! kubectl get crd stages.delivery.pacto.trianalab.io >/dev/null 2>&1 || fail "the Stage CRD is installed with delivery off"
[ -z "$(kubectl get deploy -A -o name | grep -E 'promoter|recorder|delivery' || true)" ] \
  || fail "a delivery Deployment runs with delivery off: $(kubectl get deploy -A -o name)"
CLUSTER_UID="$(kubectl get ns kube-system -o jsonpath='{.metadata.uid}')"
SNAPSHOT_TAG="127.0.0.1:${LOCAL_REG_PORT}/pacto-targets:cluster-${CLUSTER_UID}"
pass "no Stage CRD, no promoter or recorder; cluster uid $CLUSTER_UID"

section "publish payments 1.0.0, 1.1.0 and 2.0.0 and the ledger consumer"
# contract_dir NAME VERSION WORKLOAD [DEPENDENCY_LINES] — a contract folder.
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
# push NAME VERSION WORKLOAD [DEPENDENCY_LINES] — the bundle under demo/NAME:VERSION;
# prints its in-cluster digest-pinned ref.
push() {
  local digest
  digest="$(push_bundle "$PACTO" "$LOCAL_REG_PORT" "$(contract_dir "$@")" "$1:$2")"
  [ -n "$digest" ] || fail "pushing $1 $2 resolved no digest"
  echo "oci://${REG_HOST}/demo/$1@${digest}"
}
P100="$(push payments 1.0.0 service)"
push payments 1.1.0 service >/dev/null  # NON_BREAKING: only the version changes
push payments 2.0.0 job >/dev/null      # BREAKING: the workload changes
ledger_dep() { echo "dependencies: [{name: payments, ref: \"oci://${REG_HOST}/demo/payments\", required: true, compatibility: \"$1\"}]"; }
L100="$(push ledger 1.0.0 service "$(ledger_dep "^1.0.0")")"

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
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: {name: $1, namespace: $APP_NS}
spec:
  checkIntervalSeconds: 30
  contractRef: {oci: "$2"}
  target: {serviceName: $1, workloadRef: {name: $1, kind: Deployment}}
YAML
}
# Digest-pinned, so the snapshot links each target to its revision exactly: a
# tag link is "not proof" and never clears.
{ for s in payments ledger; do service "$s"; echo ---; done; workload payments; echo ---; pacto_cr payments "$P100"; echo ---; workload ledger; echo ---; pacto_cr ledger "$L100"; } \
  | kubectl apply -f - >/dev/null
wait_pacto_status "$APP_NS" payments Compliant || fail "payments 1.0.0 never reached Compliant"
wait_pacto_status "$APP_NS" ledger Compliant || fail "ledger never reached Compliant"

snapshot_digest() { oras resolve --plain-http "$SNAPSHOT_TAG" 2>/dev/null || true; }
# republish — restart the operator, whose publisher pushes at start, and wait
# for a new cluster-<uid> manifest (its asOf changed).
republish() {
  local before; before="$(snapshot_digest)"
  kubectl -n "$NS" rollout restart deploy/pacto-operator >/dev/null
  kubectl -n "$NS" rollout status deploy/pacto-operator --timeout=180s >/dev/null
  newer() { local d; d="$(snapshot_digest)"; [ -n "$d" ] && [ "$d" != "$before" ]; }
  eventually 60 newer || fail "the operator never republished pacto-targets:cluster-$CLUSTER_UID"
}
republish
pass "the operator published pacto-targets:cluster-$CLUSTER_UID with payments 1.0.0 and ledger (^1.0.0) running"

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
kubectl -n "$ARGO_NS" create configmap pacto-trust --from-file=trust.yaml="$TRUST" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

section "Argo CD $ARGOCD_VERSION (core install), its ConfigMaps written first"
ARGO_MANIFEST="$(mktemp)"
infra "download Argo CD $ARGOCD_VERSION" curl -fsSL --max-time 60 -o "$ARGO_MANIFEST" \
  "https://raw.githubusercontent.com/argoproj/argo-cd/${ARGOCD_VERSION}/manifests/core-install.yaml"
sed -i.bak 's/imagePullPolicy: Always/imagePullPolicy: IfNotPresent/' "$ARGO_MANIFEST"
[ "$(awk '$1 == "image:" { print $2 }' "$ARGO_MANIFEST" | sort -u)" = "$(printf '%s\n' "${ARGO_IMAGES[@]%@*}" | sort -u)" ] \
  || fail "Argo CD $ARGOCD_VERSION's install runs other images than ARGO_IMAGES pins"
# Created before the install, so the controller starts with them and needs no
# restart: the manifest's argocd-cm and argocd-cmd-params-cm carry no data, and
# a server-side apply under another field manager keeps these keys. The health
# customization is the documented file; persisting resource health puts Argo's
# per-resource verdict on .status.resources[] on a core install.
kubectl apply --server-side --field-manager=pacto-gitops -f "$FIXTURES/argocd-cm-pacto-health.yaml" >/dev/null
kubectl -n "$ARGO_NS" create configmap argocd-cmd-params-cm --from-literal=controller.resource.health.persist=true >/dev/null
kubectl apply -n "$ARGO_NS" --server-side -f "$ARGO_MANIFEST" >/dev/null
[ -n "$(kubectl -n "$ARGO_NS" get cm argocd-cm -o jsonpath="{.data.${HEALTH_KEY//./\\.}}")" ] \
  || fail "argocd-cm lost $HEALTH_KEY to the core install"
[ "$(kubectl -n "$ARGO_NS" get cm argocd-cmd-params-cm -o jsonpath='{.data.controller\.resource\.health\.persist}')" = true ] \
  || fail "argocd-cmd-params-cm lost controller.resource.health.persist to the core install"
for cm in argocd-cm argocd-cmd-params-cm; do
  kubectl -n "$ARGO_NS" get cm "$cm" -o jsonpath='{.metadata.labels.app\.kubernetes\.io/part-of}' | grep -q argocd \
    || fail "$cm lost Argo CD's labels"
done
pass "both keys read back after the install, and Argo CD's labels are on both ConfigMaps"
argo_crds() { kubectl get applications.argoproj.io -A >/dev/null 2>&1 && kubectl get appprojects.argoproj.io -A >/dev/null 2>&1; }
eventually 40 argo_crds || fail "Argo CD's kinds never reached discovery"
kubectl -n "$ARGO_NS" scale deploy argocd-applicationset-controller --replicas=0 >/dev/null 2>&1 || true
# The core install has no API server, which is what creates the default
# AppProject; without it every Application sits at InvalidSpecError.
kubectl apply -f - >/dev/null <<YAML
apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata: {name: default, namespace: $ARGO_NS}
spec:
  sourceRepos: ['*']
  destinations: [{server: '*', namespace: '*'}]
  clusterResourceWhitelist: [{group: '*', kind: '*'}]
YAML
# oci_repo NAME — the plain-HTTP OCI repository Secret for demo/NAME.
oci_repo() {
  kubectl apply -f - >/dev/null <<YAML
apiVersion: v1
kind: Secret
metadata: {name: $1, namespace: $ARGO_NS, labels: {argocd.argoproj.io/secret-type: repository}}
stringData: {name: $1, type: oci, url: "oci://${REG_HOST}/demo/$1", insecureOCIForceHttp: "true"}
YAML
}
oci_repo payments-manifests
oci_repo capcheck-manifests
TOKEN_BEFORE="$(kubectl -n "$ARGO_NS" get deploy argocd-repo-server -o jsonpath='{.spec.template.spec.automountServiceAccountToken}')"

section "install the plugin: the documented ConfigMap and repo-server patch, verbatim"
kubectl apply -f "$FIXTURES/argocd-cmp-pacto.yaml" >/dev/null
kubectl -n "$ARGO_NS" patch deployment argocd-repo-server --patch-file "$FIXTURES/argocd-repo-server-pacto-patch.yaml" >/dev/null
kubectl -n "$ARGO_NS" set image deployment/argocd-repo-server pacto-install="$DASH_IMG" >/dev/null
kubectl -n "$ARGO_NS" set env deployment/argocd-repo-server -c pacto \
  PACTO_TARGET_SNAPSHOT="oci://${REG_HOST}/pacto-targets:cluster-${CLUSTER_UID}" PACTO_INSECURE_REGISTRIES="$REG_HOST" >/dev/null
infra "roll out Argo CD's redis" kubectl -n "$ARGO_NS" rollout status deploy/argocd-redis --timeout=300s
infra "roll out Argo CD's repo server" kubectl -n "$ARGO_NS" rollout status deploy/argocd-repo-server --timeout=300s
infra "roll out Argo CD's controller" kubectl -n "$ARGO_NS" rollout status statefulset/argocd-application-controller --timeout=300s

section "the repo-server gains no privilege"
CAN="$(kubectl auth can-i update applications.argoproj.io -n "$ARGO_NS" --as "system:serviceaccount:$ARGO_NS:argocd-repo-server" 2>/dev/null || true)"
[ "$CAN" = no ] || fail "argocd-repo-server can update Applications: $CAN"
[ "$(kubectl -n "$ARGO_NS" get deploy argocd-repo-server -o jsonpath='{.spec.template.spec.automountServiceAccountToken}')" = "$TOKEN_BEFORE" ] \
  || fail "the patch changed automountServiceAccountToken (was '$TOKEN_BEFORE')"
pass "can-i update applications: no; automountServiceAccountToken unchanged ('$TOKEN_BEFORE')"

# publish_manifests TAG CONTRACT_VERSION CONTRACT_REF — the payments folder the
# Application pulls: a Kustomization of the Deployment and its Pacto (contractRef
# CONTRACT_REF) beside the contract pacto.yaml of CONTRACT_VERSION, which is what
# the plugin's discovery matches.
publish_manifests() {
  local tree tgz workload=service
  [ "${2%%.*}" = 2 ] && workload=job
  tree="$(contract_dir payments "$2" "$workload")"; tgz="$(mktemp -t payments-manifests.XXXXXX)"
  printf 'apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources: [deployment.yaml, pacto-cr.yaml]\n' > "$tree/kustomization.yaml"
  workload payments > "$tree/deployment.yaml"
  pacto_cr payments "$3" > "$tree/pacto-cr.yaml"
  tar -czf "$tgz" -C "$tree" .
  oras push --plain-http --disable-path-validation \
    "127.0.0.1:${LOCAL_REG_PORT}/demo/payments-manifests:$1" "${tgz}:${ARGO_LAYER_TYPE}" >/dev/null
}
ref_at() { echo "oci://${REG_HOST}/demo/payments:$1"; }
publish_manifests clear 1.1.0 "$(ref_at 1.1.0)"
publish_manifests breaking 2.0.0 "$(ref_at 2.0.0)"
publish_manifests mismatch 2.0.0 "$(ref_at 1.1.0)"

app_get() { kubectl -n "$ARGO_NS" get application "$1" -o jsonpath="$2" 2>/dev/null || true; }
app_sync() { app_get payments '{.status.sync.status}'; }
app_error() { app_get payments '{.status.conditions[?(@.type=="ComparisonError")].message}'; }
cluster_ref() { kubectl -n "$APP_NS" get pacto payments -o jsonpath='{.spec.contractRef.oci}' 2>/dev/null || true; }
refresh() { kubectl -n "$ARGO_NS" annotate application "$1" argocd.argoproj.io/refresh=hard --overwrite >/dev/null; }
retarget() {
  kubectl -n "$ARGO_NS" patch application payments --type merge -p "{\"spec\":{\"source\":{\"targetRevision\":\"$1\"}}}" >/dev/null
  refresh payments
}
error_has() { grep -qF "$1" <<<"$(app_error)"; }

section "clear: payments 1.1.0 renders and syncs"
# No helm, kustomize or directory section: Argo CD discovers the plugin.
kubectl apply -f - >/dev/null <<YAML
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata: {name: payments, namespace: $ARGO_NS}
spec:
  project: default
  source: {repoURL: "oci://${REG_HOST}/demo/payments-manifests", targetRevision: clear, path: .}
  destination: {server: https://kubernetes.default.svc, namespace: $APP_NS}
  syncPolicy: {automated: {}}
YAML
synced_at() { [ "$(app_sync)" = Synced ] && [ "$(cluster_ref)" = "$(ref_at "$1")" ]; }
eventually 60 synced_at 1.1.0 || fail "the clear change never synced: sync=$(app_sync) ref=$(cluster_ref) error=$(app_error)"
pass "Synced, and the cluster's payments Pacto names 1.1.0"
healthy() { [ "$(app_get payments '{.status.resources[?(@.kind=="Pacto")].health.status}')" = Healthy ]; }
eventually 40 healthy || fail "the health customization does not judge the synced Pacto Healthy: $(app_get payments '{.status.resources[?(@.kind=="Pacto")].health}')"
pass "the documented health customization still judges the Pacto the plugin rendered: Healthy"
republish

section "blocked: payments 2.0.0 breaks ledger, which runs and accepts ^1.0.0"
retarget breaking
# The render error is redacted (PACTO_SHOW_REASON=false); the sidecar log
# carries the reason that names ledger.
sidecar_log() { kubectl -n "$ARGO_NS" logs deployment/argocd-repo-server -c pacto --since=10m 2>/dev/null || true; }
blocked() { error_has "pacto impact: BLOCKED" && ! error_has ledger && grep -q ledger <<<"$(sidecar_log)"; }
eventually 60 blocked || fail "the breaking render was not blocked with ledger in the sidecar log only: $(app_error)"
held() { [ "$(app_sync)" != Synced ] && [ "$(cluster_ref)" = "$(ref_at 1.1.0)" ]; }
always 10 held || fail "the blocked change reached the cluster: sync=$(app_sync) ref=$(cluster_ref)"
pass "BLOCKED, ledger named in the sidecar log only; the cluster still runs payments 1.1.0: $(app_error)"

section "mismatch: the rendered contractRef disagrees with pacto.yaml"
retarget mismatch
eventually 60 error_has "ERROR rendered contractRef" || fail "a contractRef that disagrees with pacto.yaml was not refused: $(app_error)"
pass "the render failed: $(app_error)"

section "unverified: ledger accepts >=1.0.0, so the break cannot be cleared or blocked"
L101="$(push ledger 1.0.1 service "$(ledger_dep ">=1.0.0")")"
pacto_cr ledger "$L101" | kubectl apply -f - >/dev/null
ledger_moved() { [ "$(kubectl -n "$APP_NS" get pacto ledger -o jsonpath='{.status.observedGeneration}')" = "$(kubectl -n "$APP_NS" get pacto ledger -o jsonpath='{.metadata.generation}')" ]; }
eventually 40 ledger_moved || fail "the operator never evaluated ledger 1.0.1"
wait_pacto_status "$APP_NS" ledger Compliant || fail "ledger 1.0.1 never reached Compliant"
republish
retarget breaking
eventually 60 error_has "pacto impact: UNVERIFIED" || fail "the unverifiable change did not fail UNVERIFIED: $(app_error)"
pass "UNVERIFIED by default: $(app_error)"
kubectl -n "$ARGO_NS" patch application payments --type merge \
  -p '{"spec":{"source":{"plugin":{"env":[{"name":"PACTO_ALLOW_UNVERIFIED","value":"true"}]}}}}' >/dev/null
refresh payments
still_unverified() { error_has "pacto impact: UNVERIFIED" && [ "$(cluster_ref)" = "$(ref_at 1.1.0)" ]; }
always 10 still_unverified || fail "the Application granted itself the opt-in: ref=$(cluster_ref) error=$(app_error)"
pass "the Application's own PACTO_ALLOW_UNVERIFIED is ignored while the installation says false"
kubectl -n "$ARGO_NS" set env deployment/argocd-repo-server -c pacto PACTO_ALLOW_UNVERIFIED=per-application >/dev/null
kubectl -n "$ARGO_NS" rollout status deploy/argocd-repo-server --timeout=300s >/dev/null
refresh payments
eventually 60 synced_at 2.0.0 || fail "per-application did not let the Application's opt-in through: sync=$(app_sync) ref=$(cluster_ref) error=$(app_error)"
pass "with the installation at per-application, the Application's opt-in renders 2.0.0"

section "the PreSync fallback Job is a valid object"
kubectl apply --dry-run=server -n "$APP_NS" -f "$FIXTURES/argocd-presync-pacto-impact.yaml" >/dev/null \
  || fail "the API server refuses argocd-presync-pacto-impact.yaml"
pass "argocd-presync-pacto-impact.yaml passes a server-side dry run"

section "helm parity: the plugin renders what Argo CD's native Helm renders"
# A PodDisruptionBudget only when the cluster serves policy/v1, so a render
# without Argo CD's KUBE_API_VERSIONS drops it.
CHART="$(contract_dir capcheck 1.0.0 service)"
mkdir -p "$CHART/templates"
printf 'apiVersion: v2\nname: capcheck\nversion: 0.1.0\n' > "$CHART/Chart.yaml"
cat > "$CHART/templates/objects.yaml" <<'YAML'
apiVersion: v1
kind: ConfigMap
metadata: {name: {{ .Release.Name }}}
data: {release: {{ .Release.Name }}}
{{- if .Capabilities.APIVersions.Has "policy/v1" }}
---
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata: {name: {{ .Release.Name }}}
spec: {maxUnavailable: 1, selector: {matchLabels: {app: {{ .Release.Name }}}}}
{{- end }}
YAML
CHART_TGZ="$(mktemp -t capcheck.XXXXXX)"
tar -czf "$CHART_TGZ" -C "$CHART" .
oras push --plain-http --disable-path-validation \
  "127.0.0.1:${LOCAL_REG_PORT}/demo/capcheck-manifests:v1" "${CHART_TGZ}:${ARGO_LAYER_TYPE}" >/dev/null
# capcheck is in no fleet domain, so its answer is unverified; the installation
# is at per-application now and this Application opts in. The control
# Application names Helm, which skips plugin discovery. Neither syncs: the
# comparison alone lists the objects each render produced.
kubectl apply -f - >/dev/null <<YAML
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata: {name: capcheck, namespace: $ARGO_NS}
spec:
  project: default
  source:
    repoURL: "oci://${REG_HOST}/demo/capcheck-manifests"
    targetRevision: v1
    path: .
    plugin: {env: [{name: PACTO_ALLOW_UNVERIFIED, value: "true"}]}
  destination: {server: https://kubernetes.default.svc, namespace: $APP_NS}
---
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata: {name: capcheck-native, namespace: $ARGO_NS}
spec:
  project: default
  source:
    repoURL: "oci://${REG_HOST}/demo/capcheck-manifests"
    targetRevision: v1
    path: .
    helm: {releaseName: capcheck}
  destination: {server: https://kubernetes.default.svc, namespace: $APP_NS}
YAML
kinds() { app_get "$1" '{range .status.resources[*]}{.kind}{"\n"}{end}' | sort; }
rendered() { kinds capcheck | grep -qx PodDisruptionBudget && kinds capcheck-native | grep -qx PodDisruptionBudget; }
eventually 60 rendered || fail "a render dropped the policy/v1 object: plugin=[$(kinds capcheck | tr '\n' ' ')] native=[$(kinds capcheck-native | tr '\n' ' ')] error=$(app_get capcheck '{.status.conditions}')"
[ "$(kinds capcheck)" = "$(kinds capcheck-native)" ] \
  || fail "the plugin rendered [$(kinds capcheck | tr '\n' ' ')], native Helm [$(kinds capcheck-native | tr '\n' ' ')]"
pass "the plugin and native Helm render the same objects: $(kinds capcheck | tr '\n' ' ')"

kill "$REG_PF_PID" 2>/dev/null || true
echo "GITOPS ARGO CD SYNC GATE PASS"
keep_or_teardown "$NS" "$CLUSTER" delete_cluster
