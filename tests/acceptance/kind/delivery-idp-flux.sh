#!/usr/bin/env bash
# Kind acceptance for profile idp delivering through the upstream Flux the
# Pacto chart installs (#547), from an empty cluster. Flux arrives only through
# one value of the Pacto release, idp.flux.install, set with helm upgrade of
# the release named pacto; nothing else installs it.
#
# Every publisher signature is keyless against a private Sigstore
# (tests/acceptance/kind/keylesssign) whose trusted root the chart is given;
# the Sigstore lives only while it signs, so every version the run ships is
# published and signed before Pacto is installed. Each Stage reads its
# candidates from the registry (candidates.registry), which needs no Flux.
#
# It proves against the cluster:
#   1. profile idp installs with no GitOps engine, and NOTES offer the Flux
#      value;
#   2. the detected ConfigMap holds an applier entry "none found" whose hint is
#      detect.FluxInstallHint (go run ./tests/acceptance/kind/hintcheck), and
#      pacto fleet status prints it;
#   3. a Stage on an OCIRepository before Flux reads Ready False NoApplier and
#      its promote is Rejected with "no applier: install Flux or Argo CD";
#   4. helm upgrade of the Pacto release with idp.flux.install=true, at the
#      previous Flux release (files/flux/VERSION upgradeFrom), installs the
#      source, kustomize and helm controllers only, locked down, owned by the
#      release and kept; the Applier row leaves after one detection round;
#   5. plain manifests (orders), a Kustomize tree (checkout) and a Helm chart
#      (payments) each ship 1.0.0 through Flux behind a person's approval,
#      adopted and verified, with no Pacto restart;
#   6. helm upgrade of the release moves Flux to the pinned version while the
#      workloads keep their uid and generation, and payments 1.1.0 ships
#      through the upgraded Flux;
#   7. orders 2.1.0 carries a ClusterRoleBinding: its promote is Rejected by
#      pacto.manifests before the pointer moves, a signed apply refusal is
#      listed by pacto delivery refusals and the binding never exists; orders
#      2.2.0 carries a Crossplane claim and is refused with RawInfraInArtifact,
#      and no claim exists;
#   8. idp.flux.install=false keeps Flux and the workloads, and true again
#      adopts the kept objects unchanged;
#   9. helm uninstall pacto leaves Flux and the three workloads running.
set -euo pipefail
CLUSTER="${KIND_CLUSTER:-pacto-idp-flux}"
NS=pacto-system
DNS=pacto-delivery
DETECTED=pacto-operator-detected # fullnameOverride + "-detected"
REG_HOST="pacto-registry.${NS}.svc.cluster.local:5000"
LOCAL_REG_PORT=5606
GITOPS_USERS='["system:serviceaccount:flux-system:kustomize-controller","kubernetes-admin"]'
PAUSE_IMG=registry.k8s.io/pause:3.9@sha256:7031c1b283388d2c2e09b57badb803c05ebed362dc88d84b480cc47f72a21097
FLUX_LAYER_TYPE=application/vnd.cncf.flux.content.v1.tar+gzip
HELM_CHART_LAYER=application/vnd.cncf.helm.chart.content.v1.tar+gzip
HELM_CHART_CONFIG=application/vnd.cncf.helm.config.v1+json
SOURCE=https://github.com/acme/shop
CI_SUBJECT="$SOURCE/.github/workflows/release.yml@refs/heads/main"
NO_APPLIER="no applier: install Flux or Argo CD"
CONTROLLERS=(source-controller kustomize-controller helm-controller)
HERE="$(cd "$(dirname "$0")" && pwd)"
FIX="$HERE/fixtures/idp-flux"
# shellcheck source=tests/acceptance/kind/lib.sh
source "$HERE/lib.sh"
# shellcheck source=tests/acceptance/kind/delivery-lib.sh
source "$HERE/delivery-lib.sh"

dump_idp_flux() {
  echo "--- flux-system ---"
  kubectl -n flux-system get deploy,pods -o wide || true
  kubectl get ocirepositories,kustomizations,helmreleases -A -o yaml || true
  echo "--- stages + operations ---"
  kubectl get stages,operations -A -o yaml || true
  echo "--- logs deploy/pacto-operator-delivery ---"
  kubectl -n "$DNS" logs deploy/pacto-operator-delivery --tail=300 || true
  for c in "${CONTROLLERS[@]}"; do
    echo "--- logs $c ---"
    kubectl -n flux-system logs "deploy/$c" --tail=100 || true
  done
}
# shellcheck disable=SC2154  # rc is assigned by rc=$? inside the trap body
trap 'rc=$?; [ $rc -ne 0 ] && { dump_diag "$NS"; dump_idp_flux; }; pkill -f "kubectl.*port-forward" 2>/dev/null || true; keep_or_teardown "$NS" "$CLUSTER" delete_cluster; exit $rc' EXIT

for t in oras jq helm curl openssl; do command -v "$t" >/dev/null 2>&1 || fail "$t is required"; done

VER="$(release_version kubernetes)"
OP_REPO="localhost:5001/pacto-operator/pacto-controller"
OP_IMG="${OP_REPO}:${VER}"
DL_REPO="localhost:5001/pacto-delivery"
DL_IMG="${DL_REPO}:${VER}"
DASH_IMG="localhost:5001/pacto-dashboard:$(release_version core)"

# The packaged Flux: the version this branch vendors and the release the
# upgrade starts from, both read from files/flux/VERSION.
FLUX_DIR="$PACTO_CHART/files/flux"
FLUX_VERSION="$(awk '$1 == "version:" { print $2 }' "$FLUX_DIR/VERSION")"
FLUX_FROM="$(awk '$1 == "upgradeFrom:" { print $2 }' "$FLUX_DIR/VERSION")"
# Step 6 upgrades from FLUX_FROM to FLUX_VERSION: the same release would pass
# it as a no-op, so the run refuses it before anything starts.
[ -n "$FLUX_FROM" ] && [ "$FLUX_FROM" != "$FLUX_VERSION" ] \
  || fail "files/flux/VERSION upgradeFrom ($FLUX_FROM) must name an earlier release than version ($FLUX_VERSION)"
# image_of DIR CONTROLLER — the image the vendored Deployment runs.
image_of() { awk '$1 == "image:" { print $2 }' "$1/deployment-$2.yaml"; }
# flux_objects DIR — kind/name of every vendored object, from its file name.
flux_objects() { local f b; for f in "$1"/*.yaml; do b="$(basename "$f" .yaml)"; echo "${b%%-*}/${b#*-}"; done; }

section "create the cluster, build the images and vendor Flux $FLUX_FROM into a copy of the chart"
cluster_start_bg
load_built_images "$OP_IMG" "$DL_IMG" "$DASH_IMG" || build_images
pull_pinned "$PAUSE_IMG"
# The Pacto chart as a release on FLUX_FROM would ship it: this branch's chart
# with the earlier Flux vendored by the same script.
OLD_CHART="$(mktemp -d)/chart"
cp -R "$PACTO_CHART" "$OLD_CHART"
OLD_DIR="$OLD_CHART/files/flux"
infra "vendor Flux $FLUX_FROM" "$_PACTO_ROOT/integrations/kubernetes/hack/vendor-flux.sh" "$FLUX_FROM" "$FLUX_FROM" "$OLD_DIR" >/dev/null
FLUX_IMAGES=()
for c in "${CONTROLLERS[@]}"; do FLUX_IMAGES+=("$(image_of "$OLD_DIR" "$c")" "$(image_of "$FLUX_DIR" "$c")"); done
for img in "${FLUX_IMAGES[@]}"; do infra "pull $img" docker pull -q "$img" >/dev/null; done
PACTO="$(build_pacto)"
SIGN_BIN="$(mktemp -d)/keylesssign"
(cd "$_PACTO_ROOT" && go build -o "$SIGN_BIN" ./tests/acceptance/kind/keylesssign) || fail "building the keyless signer failed"
HINT="$(cd "$_PACTO_ROOT" && go run ./tests/acceptance/kind/hintcheck)" || fail "go run ./tests/acceptance/kind/hintcheck failed"
case "$HINT" in *"--set idp.flux.install=true"*) ;; *) fail "detect.FluxInstallHint does not name --set idp.flux.install=true: $HINT" ;; esac
ensure_cluster
infra "load the images into the cluster" load_images "$OP_IMG" "$DL_IMG" "$DASH_IMG" "${PAUSE_IMG%@*}" "${FLUX_IMAGES[@]}"
for n in "$NS" "$DNS" orders checkout payments; do
  kubectl create namespace "$n" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
done
no_engine() { ! kubectl get crd -o name | grep -q -e 'fluxcd\.io$' -e 'argoproj\.io$'; }
no_engine || fail "the empty cluster serves Flux or Argo CD CRDs: $(kubectl get crd -o name | grep -e fluxcd.io -e argoproj.io | tr '\n' ' ')"
pass "the cluster serves no Flux and no Argo CD CRD"
# A claim kind as a Crossplane XRD offers it: namespaced, in the claim
# category. It is served before Pacto starts, since the promoter caches API
# discovery for its life.
kubectl apply -f - >/dev/null <<'YAML'
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: postgresclaims.platform.example.org}
spec:
  group: platform.example.org
  scope: Namespaced
  names: {kind: PostgresClaim, plural: postgresclaims, singular: postgresclaim, categories: [crossplane, claim]}
  versions:
  - name: v1
    served: true
    storage: true
    schema: {openAPIV3Schema: {type: object, x-kubernetes-preserve-unknown-fields: true}}
YAML

section "the registry, the contracts and every version the run ships, signed by acme's release workflow"
install_registry
REG_PF_PID="$(pf "$LOCAL_REG_PORT" svc/pacto-registry 5000)"
# contract SERVICE VERSION — pushed as a bundle; prints its in-cluster ref.
contract() {
  local d digest
  d="$(mktemp -d)"
  cat > "$d/pacto.yaml" <<YAML
pactoVersion: '2.0'
service:
  name: $1
  version: $2
  owner: {team: team-$1, dri: d, contacts: [{type: email, value: a@e.com, purpose: escalation}]}
workload: service
state: {type: stateless, persistence: {scope: local, durability: ephemeral}, dataCriticality: low}
YAML
  digest="$(push_bundle "$PACTO" "$LOCAL_REG_PORT" "$d" "$1:$2")"
  [ -n "$digest" ] || fail "pushing $1 $2 resolved no digest"
  echo "oci://${REG_HOST}/demo/$1@${digest}"
}
# workload DIR SERVICE VERSION — the Deployment and its Pacto in DIR.
workload() {
  local ref; ref="$(contract "$2" "$3")"
  cat > "$1/deployment.yaml" <<YAML
apiVersion: apps/v1
kind: Deployment
metadata: {name: $2}
spec:
  replicas: 1
  selector: {matchLabels: {app: $2}}
  template:
    metadata: {labels: {app: $2}, annotations: {version: "$3"}}
    spec:
      containers: [{name: app, image: "${PAUSE_IMG%@*}"}]
YAML
  cat > "$1/pacto.yaml" <<YAML
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: {name: $2}
spec:
  checkIntervalSeconds: 30
  contractRef: {oci: "$ref"}
  target: {workloadRef: {name: $2, kind: Deployment}}
YAML
  echo "$ref"
}
# push REPO:TAG CONTRACT_REF FILE:TYPE [oras args...] — prints the digest.
push() {
  local at="$1" ref="$2" layer="$3" digest; shift 3
  digest="$(oras push --plain-http --disable-path-validation --format go-template='{{.digest}}' "$@" \
    --annotation "delivery.pacto.trianalab.io/contract=$ref" --annotation "org.opencontainers.image.source=$SOURCE" \
    "127.0.0.1:${LOCAL_REG_PORT}/$at" "$layer")"
  [ -n "$ref" ] || fail "$at has no contract"
  [ -n "$digest" ] || fail "pushing $at resolved no digest"
  echo "$digest"
}
# publish_flux SERVICE VERSION plain|kustomize [crb|claim] — a Flux content
# artifact at apps/SERVICE: plain manifests, or a kustomization that only lists
# them and sets the Stage's namespace; crb adds a ClusterRoleBinding, claim a
# Crossplane claim.
publish_flux() {
  local svc="$1" v="$2" tree tgz ref
  tree="$(mktemp -d)"; tgz="$(mktemp -t "$svc.XXXXXX")"
  ref="$(workload "$tree" "$svc" "$v")"
  if [ "$3" = kustomize ]; then
    printf 'apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nnamespace: %s\nresources: [deployment.yaml, pacto.yaml]\n' "$svc" > "$tree/kustomization.yaml"
  fi
  if [ "${4:-}" = crb ]; then
    cat > "$tree/clusterrolebinding.yaml" <<YAML
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata: {name: $svc-admin}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: cluster-admin}
subjects: [{kind: ServiceAccount, name: default, namespace: $svc}]
YAML
  fi
  if [ "${4:-}" = claim ]; then
    printf 'apiVersion: platform.example.org/v1\nkind: PostgresClaim\nmetadata: {name: %s-db}\nspec: {size: small}\n' "$svc" > "$tree/claim.yaml"
  fi
  tar -czf "$tgz" -C "$tree" .
  push "apps/$svc:$v" "$ref" "$tgz:$FLUX_LAYER_TYPE"
}
# publish_chart SERVICE VERSION — a Helm OCI chart at charts/SERVICE.
publish_chart() {
  local svc="$1" v="$2" d ref
  d="$(mktemp -d)"
  mkdir -p "$d/$svc/templates"
  printf 'apiVersion: v2\nname: %s\nversion: %s\n' "$svc" "$v" > "$d/$svc/Chart.yaml"
  ref="$(workload "$d/$svc/templates" "$svc" "$v")"
  helm package "$d/$svc" -d "$d" >/dev/null || fail "helm could not package the $svc chart $v"
  printf '{"apiVersion":"v2","name":"%s","version":"%s"}' "$svc" "$v" > "$d/config.json"
  (cd "$d" && push "charts/$svc:$v" "$ref" "$svc-$v.tgz:$HELM_CHART_LAYER" --config "config.json:$HELM_CHART_CONFIG")
}
O100="$(publish_flux orders 1.0.0 plain)"
O210="$(publish_flux orders 2.1.0 plain crb)"
O220="$(publish_flux orders 2.2.0 plain claim)"
C100="$(publish_flux checkout 1.0.0 kustomize)"
P100="$(publish_chart payments 1.0.0)"
P110="$(publish_chart payments 1.1.0)"
KEYDIR="$(mktemp -d)"
"$SIGN_BIN" -root "$KEYDIR/trusted_root.json" -registry "127.0.0.1:${LOCAL_REG_PORT}" -subject "$CI_SUBJECT" -repository "$SOURCE" \
  "apps/orders@$O100" "apps/orders@$O210" "apps/orders@$O220" "apps/checkout@$C100" "charts/payments@$P100" "charts/payments@$P110" \
  || fail "signing the run's versions failed"
pass "orders 1.0.0, 2.1.0 and 2.2.0, checkout 1.0.0 and payments 1.0.0 and 1.1.0 carry a Sigstore signature by $CI_SUBJECT"

section "STEP 1: helm install pacto with profile idp and no idp.flux.install"
RECORDER_KEY="$KEYDIR/recorder.pem"
RECORDER_PUB="$KEYDIR/recorder.pub"
openssl genpkey -algorithm ed25519 -out "$RECORDER_KEY" 2>/dev/null || fail "openssl could not generate the recorder's key"
openssl pkey -in "$RECORDER_KEY" -pubout -out "$RECORDER_PUB"
kubectl -n "$DNS" create secret generic recorder-key --from-file=key.pem="$RECORDER_KEY" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
out="$(helm install pacto "$PACTO_CHART" -n "$NS" --set profile=idp \
  --set global.imageRegistry= --set image.repository="$OP_REPO" --set image.tag="$VER" --set image.pullPolicy=Never \
  --set fullnameOverride=pacto-operator --set "insecureRegistries[0]=$REG_HOST" \
  --set delivery.createNamespace=false --set-json "delivery.admissionPolicies.gitopsUsers=$GITOPS_USERS" \
  --set delivery.image.repository="$DL_REPO" --set delivery.image.tag="$VER" --set delivery.image.pullPolicy=Never \
  --set "delivery.insecureRegistries[0]=$REG_HOST" --set delivery.promoter.poll=2s \
  --set delivery.publishers.registry=ghcr.io/acme --set-file delivery.publishers.trustedRoot="$KEYDIR/trusted_root.json" \
  --set delivery.recorder.enabled=true --set delivery.recorder.streamsRepository="$REG_HOST/pacto-delivery-streams" --set delivery.recorder.poll=5s \
  --set delivery.recorder.signing.ed25519.secretName=recorder-key --set delivery.recorder.signing.allowOperatorReadableKey=true \
  --wait --timeout 300s)" || fail "helm install pacto with profile idp did not roll out"
grep -qF "$HINT" <<<"$out" || { echo "$out" >&2; fail "the install NOTES do not offer: $HINT"; }
no_engine || fail "profile idp installed a GitOps engine without idp.flux.install"
pass "profile idp runs with no GitOps engine, and NOTES offer the Flux value of this release"

section "STEP 2: Detected providers offer the Flux value; the hint is the compiled-in constant"
applier_entry() {
  kubectl -n "$NS" get configmap "$DETECTED" -o jsonpath='{.data.detected\.json}' 2>/dev/null \
    | jq -c '[.entries[]? | select(.kind == "applier")][0] // empty' 2>/dev/null || true
}
offers_flux() { applier_entry | jq -e --arg h "$HINT" '.value == "none found" and .hint == $h' >/dev/null 2>&1; }
eventually 60 offers_flux || fail "the detected ConfigMap holds no applier entry 'none found' with the compiled hint: $(applier_entry)"
# fleet_applier — the Applier row pacto fleet status prints over this cluster,
# run in an empty directory so no local bundle joins it.
fleet_applier() {
  (cd "$(mktemp -d)" && "$PACTO" fleet status --k8s --detected "k8s:$NS/$DETECTED" --output-format json 2>/dev/null) \
    | jq -c '[.providers[]? | select(.id == "applier")][0] // empty' 2>/dev/null || true
}
[ "$(fleet_applier | jq -r '.hint // empty')" = "$HINT" ] || fail "pacto fleet status does not print the Applier row with the compiled hint: $(fleet_applier)"
pass "the applier entry reads none found, and pacto fleet status prints its hint: $HINT"

section "people: alice proposes and carol approves in every service's namespace"
kubectl create clusterrole delivery-planner --verb=list \
  --resource=pactos.pacto.trianalab.io,kustomizations.kustomize.toolkit.fluxcd.io,helmreleases.helm.toolkit.fluxcd.io \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl create clusterrolebinding delivery-planner --clusterrole=delivery-planner --user=alice --user=carol \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null
for n in orders checkout payments; do
  for u in alice carol; do
    kubectl -n "$n" create rolebinding "$u-edit" --clusterrole=edit --user="$u" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  done
done
may_approve() { [ "$(kubectl auth can-i create approvals.delivery.pacto.trianalab.io -n payments --as=carol)" = yes ]; }
eventually 60 may_approve || fail "edit does not let carol create an Approval: the chart's -edit role was not aggregated"
SERVER="$(kubectl config view --raw --minify -o jsonpath='{.clusters[0].cluster.server}')"
CA="$(kubectl config view --raw --minify -o jsonpath='{.clusters[0].cluster.certificate-authority-data}')"
CERT="$(kubectl config view --raw --minify -o jsonpath='{.users[0].user.client-certificate-data}')"
KEY="$(kubectl config view --raw --minify -o jsonpath='{.users[0].user.client-key-data}')"
# person USER GROUP AUTH_TIME — a kubeconfig impersonating USER in GROUP who
# logged in at AUTH_TIME.
person() {
  local f; f="$(mktemp)"
  cat > "$f" <<YAML
apiVersion: v1
kind: Config
clusters: [{name: kind, cluster: {server: "$SERVER", certificate-authority-data: "$CA"}}]
users:
- name: $1
  user:
    client-certificate-data: "$CERT"
    client-key-data: "$KEY"
    as: $1
    as-groups: [$2]
    as-user-extra: {"delivery.pacto.trianalab.io/auth-time": ["$3"]}
contexts: [{name: c, context: {cluster: kind, user: $1}}]
current-context: c
YAML
  echo "$f"
}
now() { date -u +%Y-%m-%dT%H:%M:%SZ; }
as_person() { local u="$1" g="$2"; shift 2; KUBECONFIG="$(person "$u" "$g" "$(now)")" "$PACTO" delivery "$@"; }
# approve USER TEAM NS OP — the Approval a person with a fresh login creates
# for the decision OP's plan asks, as they would send it.
approve() {
  local at kc uid digest groups decision
  at="$(now)"; kc="$(person "$1" "$2" "$at")"
  uid="$(kubectl -n "$3" get operation "$4" -o jsonpath='{.metadata.uid}')"
  digest="$(kubectl -n "$3" get operation "$4" -o jsonpath='{.spec.intentDigest}')"
  decision="$(field "$3" "$4" plan.floor.decision)"
  groups="$(KUBECONFIG="$kc" kubectl auth whoami -o json | jq -c '.status.userInfo.groups')"
  KUBECONFIG="$kc" kubectl -n "$3" create -f - >/dev/null <<YAML
apiVersion: delivery.pacto.trianalab.io/v1alpha1
kind: Approval
metadata:
  generateName: $4-
  ownerReferences: [{apiVersion: delivery.pacto.trianalab.io/v1alpha1, kind: Operation, name: "$4", uid: "$uid"}]
spec: {operation: "$4", operationUID: "$uid", intentDigest: "$digest", decision: $decision, note: "the kind run approves", by: "$1", authTime: "$at", groups: $groups}
YAML
}
# ship NS DIGEST NAME — alice proposes DIGEST on the Stage named after NS; the
# Stage holds every promote for a person, carol approves, and it ships verified.
ship() {
  as_person alice "team-$1" propose -n "$1" --stage "$1" --to "$2" --name "$3" --reason "the kind run ships it" >/dev/null
  eventually 60 held "$1" "$3" || fail "$1/$3 was never held for a person: $(field "$1" "$3" phase) $(field "$1" "$3" reason): $(field "$1" "$3" message)"
  approve carol "team-$1" "$1" "$3"
  wait_concluded "$1" "$3" Succeeded/Verified
}
# stage NS APPLY_KIND REPOSITORY — the Stage named after NS, gated on a person.
stage() {
  kubectl -n "$1" apply -f - >/dev/null <<YAML
apiVersion: delivery.pacto.trianalab.io/v1alpha1
kind: Stage
metadata: {name: $1}
spec:
  service: {name: $1}
  environment: idp
  applyRef: {kind: $2, name: $1}
  workload: {kind: Deployment, name: $1}
  candidates: {registry: {repository: "$REG_HOST/$3"}}
  startLevel: advise
  approval: {always: true}
  gates: {impact: {declaredConsumersOnly: true}, verify: {window: 20s}}
YAML
}
ready_reason() { kubectl -n "$1" get stage "$2" -o jsonpath='{.status.conditions[?(@.type=="Ready")].reason}'; }
ready_reason_is() { [ "$(ready_reason "$1" "$2")" = "$3" ]; }
why() { echo "$(field "$1" "$2" reason): $(field "$1" "$2" message)"; } # NS OP

section "STEP 3: a Stage on an OCIRepository before Flux reads NoApplier and never promotes"
stage orders OCIRepository apps/orders
eventually 60 ready_reason_is orders orders NoApplier || fail "orders/orders is Ready $(ready_reason orders orders), want NoApplier"
kubectl -n orders get stage orders -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}' | grep -qF "$NO_APPLIER" \
  || fail "the NoApplier condition does not say: $NO_APPLIER"
applier_says_none() { kubectl -n orders get stage orders -o jsonpath='{.status.applier.message}' | grep -qF "$NO_APPLIER"; }
eventually 60 applier_says_none || fail "orders/orders' applier does not say $NO_APPLIER: $(kubectl -n orders get stage orders -o jsonpath='{.status.applier}')"
eventually 90 published_in orders orders "$O100" || fail "orders 1.0.0 never reached status.published: $(kubectl -n orders get stage orders -o jsonpath='{.status.publisher}')"
as_person alice team-orders propose -n orders --stage orders --to "$O100" --name orders-before-flux --reason "no applier yet" >/dev/null
wait_concluded orders orders-before-flux Rejected/
why orders orders-before-flux | grep -qF "$NO_APPLIER" || fail "orders-before-flux was rejected for another reason: $(why orders orders-before-flux)"
pass "Stage orders reads Ready False NoApplier and its promote was Rejected: $NO_APPLIER"

section "STEP 4: idp.flux.install=true on the Pacto release installs Flux $FLUX_FROM"
promoter_pods() { kubectl -n "$DNS" get pods -l app.kubernetes.io/component=delivery-promoter -o jsonpath='{range .items[*]}{.metadata.uid}:{.status.containerStatuses[0].restartCount} {end}'; }
PROMOTER="$(promoter_pods)"
[ -n "$PROMOTER" ] || fail "no promoter pod runs in $DNS"
helm upgrade pacto "$OLD_CHART" -n "$NS" --reset-then-reuse-values --set idp.flux.install=true --wait --timeout 300s >/dev/null \
  || fail "helm upgrade pacto with idp.flux.install=true did not roll out"
# controllers_at DIR — the three controllers are Ready on DIR's images.
controllers_at() {
  local c
  for c in "${CONTROLLERS[@]}"; do
    kubectl -n flux-system rollout status "deploy/$c" --timeout=5s >/dev/null 2>&1 || return 1
    [ "$(kubectl -n flux-system get deploy "$c" -o jsonpath='{.spec.template.spec.containers[0].image}')" = "$(image_of "$1" "$c")" ] || return 1
  done
}
eventually 60 controllers_at "$OLD_DIR" || fail "the Flux controllers are not Ready on Flux $FLUX_FROM's images: $(kubectl -n flux-system get deploy -o wide)"
flux_deploys="$(kubectl get deploy -A -l app.kubernetes.io/part-of=flux -o jsonpath='{range .items[*]}{.metadata.namespace}/{.metadata.name} {end}')"
[ "$flux_deploys" = "flux-system/helm-controller flux-system/kustomize-controller flux-system/source-controller " ] \
  || fail "the cluster runs other Flux Deployments than the three controllers: $flux_deploys"
kubectl -n flux-system get deploy kustomize-controller -o json | jq -e '.spec.template.spec.containers[0].args | index("--no-remote-bases=true")' >/dev/null \
  || fail "kustomize-controller runs without --no-remote-bases=true"
# owned_and_kept DIR — every object DIR vendors exists, owned by the release
# and kept on uninstall.
owned_and_kept() {
  # shellcheck disable=SC2046  # one argument per object
  kubectl -n flux-system get $(flux_objects "$1") -o json | jq -e --arg rel pacto \
    '[.items[].metadata.annotations // {} | select(.["meta.helm.sh/release-name"] != $rel or .["helm.sh/resource-policy"] != "keep")] | length == 0' >/dev/null
}
owned_and_kept "$OLD_DIR" || fail "a Flux object is not owned by release pacto or not kept on uninstall"
pass "Flux $FLUX_FROM runs source, kustomize and helm controllers only, locked down, owned by pacto and kept"
no_applier_row() { [ -z "$(applier_entry)" ] && [ -z "$(fleet_applier)" ]; }
eventually 130 no_applier_row || fail "the Applier row is still detected after a detection round: $(applier_entry)"
pass "the Applier row left pacto fleet status after one detection round"

section "STEP 5: manifests, Kustomize and Helm each ship 1.0.0 through Flux"
kubectl apply -f "$FIX/manifests/" -f "$FIX/kustomize/" -f "$FIX/helm/" >/dev/null
stage checkout OCIRepository apps/checkout
stage payments HelmRelease charts/payments
# delivered NS VERSION — the workload is rolled out at VERSION.
delivered() {
  kubectl -n "$1" rollout status "deploy/$1" --timeout=5s >/dev/null 2>&1 &&
    [ "$(kubectl -n "$1" get deploy "$1" -o jsonpath='{.spec.template.metadata.annotations.version}')" = "$2" ]
}
adopted() { [ "$(kubectl -n "$1" get stage "$1" -o jsonpath='{.status.applier.manager}/{.status.applier.state}')" = flux/Adopted ]; }
# A Stage judges its applier on every resync (a minute): orders read NoApplier
# before Flux, so each waits until its applier passes.
applier_passes() { [[ "$(kubectl -n "$1" get stage "$1" -o jsonpath='{.status.applier.state}')" =~ ^(Unmanaged|Adopted)$ ]]; }
first_version() { # NS DIGEST
  eventually 60 applier_passes "$1" || fail "Stage $1's applier never passed: $(kubectl -n "$1" get stage "$1" -o jsonpath='{.status.applier}')"
  eventually 90 published_in "$1" "$1" "$2" || fail "$1 1.0.0 never reached status.published"
  ship "$1" "$2" "$1-1-0-0"
  delivered "$1" 1.0.0 || fail "the cluster does not run $1 1.0.0"
  runs "$1" "$1" "$2" || fail "Stage $1 points at $(pointer "$1" "$1"), want $2"
  eventually 60 adopted "$1" || fail "Stage $1 did not adopt Flux: $(kubectl -n "$1" get stage "$1" -o jsonpath='{.status.applier}')"
  pass "$1 1.0.0 shipped through Flux after carol's approval, adopted and verified"
}
first_version orders "$O100"
first_version checkout "$C100"
first_version payments "$P100"
[ "$(promoter_pods)" = "$PROMOTER" ] || fail "the promoter restarted between Flux's install and the deliveries: $PROMOTER, now $(promoter_pods)"
pass "the promoter delivered through a Flux installed after it started, with no restart"

section "STEP 6: helm upgrade of the Pacto release moves Flux to $FLUX_VERSION; the workloads stay"
workload_ids() { local n; for n in orders checkout payments; do kubectl -n "$n" get deploy "$n" -o jsonpath='{.metadata.uid}:{.metadata.generation} '; done; }
IDS="$(workload_ids)"
helm upgrade pacto "$PACTO_CHART" -n "$NS" --reuse-values --wait --timeout 300s >/dev/null || fail "helm upgrade pacto to this branch's chart did not roll out"
eventually 60 controllers_at "$FLUX_DIR" || fail "the Flux controllers are not Ready on Flux $FLUX_VERSION's images: $(kubectl -n flux-system get deploy -o wide)"
crds_at() {
  kubectl get crd -l app.kubernetes.io/part-of=flux -o json | jq -e --arg v "$FLUX_VERSION" --argjson n "$(find "$FLUX_DIR" -name 'customresourcedefinition-*.yaml' | wc -l)" \
    '(.items | length) == $n and all(.items[]; .metadata.labels["app.kubernetes.io/version"] == $v)' >/dev/null
}
crds_at || fail "a vendored Flux CRD does not carry app.kubernetes.io/version $FLUX_VERSION"
owned_and_kept "$FLUX_DIR" || fail "a Flux $FLUX_VERSION object is not owned by release pacto or not kept"
same_workloads() { [ "$(workload_ids)" = "$IDS" ] && delivered orders 1.0.0 && delivered checkout 1.0.0 && delivered payments 1.0.0; }
always 10 same_workloads || fail "a workload changed across the Flux upgrade: $IDS, now $(workload_ids)"
pass "Flux $FLUX_FROM moved to $FLUX_VERSION with the release; each workload kept its uid and generation at 1.0.0"
eventually 90 published_in payments payments "$P110" || fail "payments 1.1.0 never reached status.published"
ship payments "$P110" payments-1-1-0
delivered payments 1.1.0 || fail "the cluster does not run payments 1.1.0"
pass "payments 1.1.0 shipped through the upgraded Flux"

section "STEP 7: orders 2.1.0 carries a ClusterRoleBinding and is refused before the pointer moves"
eventually 90 published_in orders orders "$O210" || fail "orders 2.1.0 never reached status.published"
as_person alice team-orders propose -n orders --stage orders --to "$O210" --name orders-2-1-0 --reason "ship 2.1.0" >/dev/null
wait_concluded orders orders-2-1-0 Rejected/
for want in pacto.manifests "ClusterRoleBinding orders-admin is cluster-scoped"; do
  why orders orders-2-1-0 | grep -qF "$want" || fail "orders-2-1-0 was rejected without naming $want: $(why orders orders-2-1-0)"
done
[ "$(kubectl -n orders get ocirepository orders -o jsonpath='{.spec.ref.digest}')" = "$O100" ] \
  || fail "the refused promote moved the orders OCIRepository to $(kubectl -n orders get ocirepository orders -o jsonpath='{.spec.ref.digest}')"
# The CLI runs on the host: HTTP_PROXY sends its plain-HTTP registry traffic
# to the port-forward, so the stream names the recorder wrote resolve.
refusals() {
  HTTP_PROXY="http://127.0.0.1:${LOCAL_REG_PORT}" PACTO_INSECURE_REGISTRIES="$REG_HOST" \
    "$PACTO" delivery --trust "$RECORDER_PUB" refusals -n orders --output-format json 2>/dev/null || true
}
refused_listed() {
  refusals | jq -e '[.refusals[]? | select(.check == "manifests" and .stage == "orders" and .verified == true and (.record // "") != "")] | length == 1' >/dev/null 2>&1
}
eventually 60 refused_listed || fail "pacto delivery refusals does not list one verified manifests refusal with a record: $(refusals)"
no_binding() { ! kubectl get clusterrolebinding orders-admin >/dev/null 2>&1; }
always 5 no_binding || fail "clusterrolebinding orders-admin exists"
pass "pacto.manifests rejected orders 2.1.0, the pointer stayed on 1.0.0, the signed refusal is listed and orders-admin never existed"
eventually 90 published_in orders orders "$O220" || fail "orders 2.2.0 never reached status.published"
as_person alice team-orders propose -n orders --stage orders --to "$O220" --name orders-2-2-0 --reason "ship 2.2.0" >/dev/null
wait_concluded orders orders-2-2-0 Rejected/
for want in pacto.manifests "PostgresClaim orders-db is raw infrastructure: declare a dependency on an offering"; do
  why orders orders-2-2-0 | grep -qF "$want" || fail "orders-2-2-0 was rejected without naming $want: $(why orders orders-2-2-0)"
done
code="$(kubectl -n orders get operation orders-2-2-0 -o jsonpath='{.status.verdicts[?(@.gate=="pacto.manifests")].citation.code}')"
[ "$code" = RawInfraInArtifact ] || fail "the pacto.manifests verdict of orders-2-2-0 cites code '$code', not RawInfraInArtifact"
[ "$(kubectl -n orders get ocirepository orders -o jsonpath='{.spec.ref.digest}')" = "$O100" ] \
  || fail "the refused claim moved the orders OCIRepository"
claim_listed() {
  refusals | jq -e '[.refusals[]? | select(.check == "manifests" and .stage == "orders" and .verified == true)] | length == 2' >/dev/null 2>&1
}
eventually 60 claim_listed || fail "pacto delivery refusals does not list the claim refusal beside the binding's: $(refusals)"
no_claim() { [ -z "$(kubectl get postgresclaims -A -o name 2>/dev/null)" ]; }
always 5 no_claim || fail "a PostgresClaim exists: $(kubectl get postgresclaims -A -o name)"
pass "profile idp refused orders 2.2.0 and its Crossplane claim with RawInfraInArtifact; the signed refusal is listed and no claim exists"

section "STEP 8: idp.flux.install=false keeps Flux; true again adopts it unchanged"
flux_ids() { local c; for c in "${CONTROLLERS[@]}"; do kubectl -n flux-system get deploy "$c" -o jsonpath='{.metadata.uid}:{.metadata.generation} '; done; }
FLUX_IDS="$(flux_ids)"
# kept — Flux's Namespace, CRDs and controllers exist and run, and so do the
# workloads at the versions they shipped.
kept() {
  # shellcheck disable=SC2046  # one argument per object
  kubectl -n flux-system get $(flux_objects "$FLUX_DIR") >/dev/null 2>&1 && controllers_at "$FLUX_DIR" &&
    delivered orders 1.0.0 && delivered checkout 1.0.0 && delivered payments 1.1.0
}
helm upgrade pacto "$PACTO_CHART" -n "$NS" --reuse-values --set idp.flux.install=false --wait --timeout 300s >/dev/null \
  || fail "helm upgrade pacto with idp.flux.install=false failed"
always 10 kept || fail "turning idp.flux.install off removed or stopped Flux or a workload"
pass "with idp.flux.install=false Flux and the three workloads stay and run"
out="$(helm upgrade pacto "$PACTO_CHART" -n "$NS" --reuse-values --set idp.flux.install=true --wait --timeout 300s 2>&1)" \
  || { echo "$out" >&2; fail "helm upgrade pacto with idp.flux.install=true again did not adopt the kept Flux"; }
[ "$(flux_ids)" = "$FLUX_IDS" ] || fail "adopting the kept Flux changed its controllers: $FLUX_IDS, now $(flux_ids)"
owned_and_kept "$FLUX_DIR" || fail "the adopted Flux is not owned by release pacto"
pass "idp.flux.install=true again adopted the kept objects with no ownership error and the controllers unchanged"

section "STEP 9: helm uninstall pacto leaves Flux and the workloads"
helm uninstall pacto -n "$NS" --wait >/dev/null || fail "helm uninstall pacto failed"
[ -z "$(helm list -n "$NS" -q --all)" ] || fail "helm still lists a release in $NS: $(helm list -n "$NS" --all)"
always 10 kept || fail "uninstalling Pacto removed or stopped Flux or a workload"
pass "flux-system, the Flux CRDs, the three controllers and orders 1.0.0, checkout 1.0.0 and payments 1.1.0 outlive the Pacto release"

kill "$REG_PF_PID" 2>/dev/null || true
echo "DELIVERY IDP FLUX PASS"
