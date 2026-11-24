#!/usr/bin/env bash
# Find candidate clusters and install a member through Argo CD and Flux (#569)
# on two real kind clusters and a zot.
#
# Unit tests pin the inventory reads, the matching and the generated files on
# fakes; only real clusters run Argo CD and Flux on what `pacto cluster
# install` prints, unchanged, and show that removal orphans.
#
#   zot ($ZOT)          on the kind docker network, htpasswd auth: pusher may
#                       push, puller may read, anyone may read the charts and
#                       acme/pacto-targets (what Argo CD and Flux pull)
#   hub ($CLUSTER)      Argo CD core (its ApplicationSet controller running),
#                       the Flux controllers this chart vendors, and Pacto with
#                       clusterInventory on for argocd and flux-system, delivery
#                       for the approver signer and its dashboard
#   member ($MEMBER)    nothing but the push-credential Secret in pacto, the
#                       documented step; registered on the hub as an Argo CD
#                       cluster Secret and a Flux kubeconfig Secret, both
#                       labelled env=prod
#
# Claims:
#   1. `pacto cluster list` shows the Argo CD and Flux candidates as unmatched;
#      a dashboard reader who may not approve clusterjoins gets
#      hiddenCandidates and a 403 on the download, an approver the YAML;
#   2. the generated ApplicationSet's values are exactly registry and
#      insecureRegistries; applied unchanged (plus the documented
#      registryPullSecret overlay) it installs Pacto on the member, which
#      publishes and reads pending, and never on the hub, whose own cluster
#      Secret is labelled like the member's: the hub keeps one Pacto release;
#   3. a stated UID on a pending member reads same-uid and gives no inventory
#      environment; once approved the candidate reads matched and the member
#      lists prod in inventoryEnvironments, named by its label, and not in
#      environments;
#   4. deleting the ApplicationSet leaves the member operator running;
#   5. `--from flux --push` prints a bootstrap pinned to the pushed digest;
#      applied unchanged it installs Pacto on the member again, and re-pointing
#      the old members-flux tag at other content changes nothing.
# shellcheck disable=SC2016  # the jq filters name jq variables such as $u
set -euo pipefail
# shellcheck source=tests/acceptance/kind/lib.sh
source "$(dirname "$0")/lib.sh"
# shellcheck source=tests/acceptance/kind/delivery-lib.sh
source "$(dirname "$0")/delivery-lib.sh"
# shellcheck source=tests/acceptance/kind/argocd-lib.sh
source "$(dirname "$0")/argocd-lib.sh"

CLUSTER="${KIND_CLUSTER:-pacto-install-hub}"
MEMBER=pacto-install-member
NS=pacto-install # the hub's release namespace (read by dump_diag and pf)
ARGO_NS=argocd   # read by install_argocd_core
ZOT=pacto-install-zot
DETECTED=pacto-operator-detected
M_NS=pacto                         # where the generated install puts Pacto in a member
M_OP=pacto-pacto-operator          # its fullname: release pacto, chart pacto-operator
M_DETECTED="$M_OP-detected"
ARGO_SECRET=member                 # the member's Argo CD cluster Secret in argocd
FLUX_SECRET=member-kubeconfig      # the member's Flux kubeconfig Secret in flux-system
LOCAL_DASH_PORT=18571
PUSH_PW="push-$RANDOM$RANDOM"
PULL_PW="pull-$RANDOM$RANDOM"
VER="$(release_version kubernetes)"
OP_REPO="localhost:5001/pacto-operator/pacto-controller"
OP_IMG="${OP_REPO}:${VER}"
DL_REPO="localhost:5001/pacto-delivery"
DL_IMG="${DL_REPO}:${VER}"
DASH_IMG="localhost:5001/pacto-dashboard:$(release_version core)"
# The image the chart runs by default at chart version $VER: the generated
# install sets no image, so the member gets the operator image under it.
MEMBER_OP_IMG="ghcr.io/trianalab/pacto/operator:${VER}"
FLUX_DIR="$PACTO_CHART/files/flux"

for t in jq helm docker curl base64; do command -v "$t" >/dev/null 2>&1 || fail "$t is required"; done
command -v htpasswd >/dev/null 2>&1 || infra "install htpasswd" sudo apt-get install -y -qq apache2-utils

WORK="$(mktemp -d)"
DASH_PF=
teardown() {
  [ -n "$DASH_PF" ] && kill "$DASH_PF" 2>/dev/null
  docker rm -f "$ZOT" >/dev/null 2>&1 || true
  kind delete cluster --name "$MEMBER" >/dev/null 2>&1 || true
  delete_cluster
}
dump_install() {
  kubectl --context "kind-$CLUSTER" -n "$ARGO_NS" get applicationsets,applications -o yaml 2>/dev/null | tail -n 80 || true
  kubectl --context "kind-$CLUSTER" -n flux-system get ocirepositories,kustomizations,helmreleases -o yaml 2>/dev/null | tail -n 80 || true
  for c in argocd-applicationset-controller argocd-repo-server; do
    echo "--- logs $c ---"; kubectl --context "kind-$CLUSTER" -n "$ARGO_NS" logs "deploy/$c" --tail=60 2>/dev/null || true
  done
  for c in source-controller kustomize-controller helm-controller; do
    echo "--- logs $c ---"; kubectl --context "kind-$CLUSTER" -n flux-system logs "deploy/$c" --tail=60 2>/dev/null || true
  done
  kubectl --context "kind-$MEMBER" -n "$M_NS" get all 2>/dev/null || true
}
# shellcheck disable=SC2154  # rc is assigned by rc=$? inside the trap body
trap 'rc=$?; [ $rc -ne 0 ] && { dump_diag "$NS"; dump_install; }; keep_or_teardown "$NS" "$CLUSTER" teardown; { [ -z "$DASH_PF" ] || kill "$DASH_PF" 2>/dev/null || true; }; exit $rc' EXIT

section "two kind clusters, the images, the chart and the pacto CLI"
cluster_start_bg
load_built_images "$OP_IMG" "$DL_IMG" "$DASH_IMG" || build_images
docker tag "$OP_IMG" "$MEMBER_OP_IMG"
FLUX_IMAGES=()
for c in source-controller kustomize-controller helm-controller; do
  FLUX_IMAGES+=("$(awk '$1 == "image:" { print $2 }' "$FLUX_DIR/deployment-$c.yaml")")
done
for img in "${FLUX_IMAGES[@]}"; do infra "pull $img" docker pull -q "$img" >/dev/null; done
BIN="$(build_pacto)"
JOINSIGN="$WORK/joinsign"
( cd "$_PACTO_ROOT" && CGO_ENABLED=0 go build -o "$JOINSIGN" ./tests/acceptance/kind/joinsign )
# The chart at the version the release plan assigns, so the version the hub
# advertises (--chart-version) is the one pushed to zot and the image tag the
# member runs.
TGZ="$(package_chart "$PACTO_CHART" --version "$VER" --app-version "$VER")"
ensure_cluster
KC_H="$KUBECONFIG"
kind get clusters | grep -qx "$MEMBER" || infra "create kind cluster $MEMBER" kind create cluster --name "$MEMBER" --wait 90s
KC_M="$(mktemp)"
kind get kubeconfig --name "$MEMBER" > "$KC_M"
KUBECONFIG="$(mktemp)"; export KUBECONFIG
KUBECONFIG="$KC_H:$KC_M" kubectl config view --flatten > "$KUBECONFIG"
CTX_H="kind-$CLUSTER"
CTX_M="kind-$MEMBER"
kubectl config use-context "$CTX_H" >/dev/null
infra "load the images into the hub" load_images "$OP_IMG" "$DL_IMG" "$DASH_IMG" "${FLUX_IMAGES[@]}"
CLUSTER="$MEMBER" infra "load the operator and dashboard images into the member" load_images "$MEMBER_OP_IMG" "$DASH_IMG"
pass "hub $CLUSTER and member $MEMBER run with the images"

section "zot: authenticated push, anonymous read of the charts and pacto-targets"
pull_pinned "$PACTO_REGISTRY_IMAGE"
docker rm -f "$ZOT" >/dev/null 2>&1 || true
ZDIR="$WORK/zot"
mkdir -p "$ZDIR"
{ htpasswd -Bbn pusher "$PUSH_PW"; htpasswd -Bbn puller "$PULL_PW"; } | grep . > "$ZDIR/htpasswd"
cat > "$ZDIR/config.json" <<'JSON'
{
  "distSpecVersion": "1.1.0",
  "storage": {"rootDirectory": "/var/lib/registry"},
  "http": {
    "address": "0.0.0.0", "port": "5000", "realm": "zot",
    "auth": {"htpasswd": {"path": "/etc/zot/htpasswd"}},
    "accessControl": {"repositories": {
      "charts/**": {"policies": [{"users": ["pusher"], "actions": ["read", "create", "update"]}],
                    "anonymousPolicy": ["read"], "defaultPolicy": []},
      "acme/pacto-targets": {"policies": [{"users": ["pusher"], "actions": ["read", "create", "update"]},
                                          {"users": ["puller"], "actions": ["read"]}],
                             "anonymousPolicy": ["read"], "defaultPolicy": []},
      "**": {"policies": [{"users": ["pusher"], "actions": ["read", "create", "update"]},
                          {"users": ["puller"], "actions": ["read"]}], "defaultPolicy": []}
    }}
  },
  "log": {"level": "info"}
}
JSON
chmod -R a+rX "$ZDIR"
docker run -d --name "$ZOT" --network kind -v "$ZDIR:/etc/zot:ro" "${PACTO_REGISTRY_IMAGE%@*}" serve /etc/zot/config.json >/dev/null
REG="$(docker inspect -f '{{(index .NetworkSettings.Networks "kind").IPAddress}}' "$ZOT"):5000"
answers() { [ "$(curl -s -o /dev/null -w '%{http_code}' "http://$REG/v2/")" != 000 ]; }
eventually 30 answers || { docker logs "$ZOT" >&2; fail "zot at $REG does not answer"; }
docker_config() { # USER PASSWORD — prints a DOCKER_CONFIG directory holding them for $REG
  local d; d="$(mktemp -d)"
  printf '{"auths":{"%s":{"auth":"%s"}}}' "$REG" "$(printf '%s:%s' "$1" "$2" | base64 | tr -d '\n')" > "$d/config.json"
  echo "$d"
}
PUSH_CFG="$(docker_config pusher "$PUSH_PW")"
PULL_CFG="$(docker_config puller "$PULL_PW")"
export PACTO_INSECURE_REGISTRIES="$REG"
export HELM_REGISTRY_CONFIG="$WORK/helm-registry.json"
helm registry login "$REG" --plain-http -u pusher --password-stdin <<<"$PUSH_PW" >/dev/null
helm push "$TGZ" "oci://$REG/charts" --plain-http >/dev/null || fail "helm could not push the chart to $REG/charts"
CHART="oci://$REG/charts/pacto-operator"
[ "$(curl -s -o /dev/null -w '%{http_code}' "http://$REG/v2/charts/pacto-operator/tags/list")" = 200 ] ||
  fail "an anonymous reader cannot list $REG/charts/pacto-operator"
pass "zot at $REG holds $CHART:$VER, readable anonymously"

section "the hub: Argo CD with its ApplicationSet controller, and Flux"
install_argocd_core >/dev/null
kubectl -n "$ARGO_NS" scale deploy argocd-applicationset-controller --replicas=1 >/dev/null
kubectl -n "$ARGO_NS" rollout status deploy/argocd-applicationset-controller --timeout=300s >/dev/null ||
  fail "the Argo CD ApplicationSet controller never rolled out"
kubectl apply -f "$FLUX_DIR/namespace-flux-system.yaml" >/dev/null
kubectl apply --server-side -f "$FLUX_DIR" >/dev/null
for c in source-controller kustomize-controller helm-controller; do
  kubectl -n flux-system rollout status "deploy/$c" --timeout=300s >/dev/null || fail "Flux $c never rolled out"
done
pass "the hub runs Argo CD $ARGOCD_VERSION with its ApplicationSet controller and Flux"

section "the member registered in Argo CD and Flux, and its push credential"
KC_IN="$WORK/member-internal.kubeconfig"
kind get kubeconfig --internal --name "$MEMBER" > "$KC_IN"
kc_field() { KUBECONFIG="$KC_IN" kubectl config view --raw -o jsonpath="$1"; }
M_SERVER="$(kc_field '{.clusters[0].cluster.server}')"
ARGO_CONFIG="$(jq -cn --arg ca "$(kc_field '{.clusters[0].cluster.certificate-authority-data}')" \
  --arg cert "$(kc_field '{.users[0].user.client-certificate-data}')" --arg key "$(kc_field '{.users[0].user.client-key-data}')" \
  '{tlsClientConfig: {caData: $ca, certData: $cert, keyData: $key}}')"
kubectl apply -f - >/dev/null <<YAML
apiVersion: v1
kind: Secret
metadata:
  name: hub
  namespace: $ARGO_NS
  labels: {argocd.argoproj.io/secret-type: cluster, env: prod}
stringData: {name: in-cluster, server: https://kubernetes.default.svc}
---
apiVersion: v1
kind: Secret
metadata:
  name: $ARGO_SECRET
  namespace: $ARGO_NS
  labels: {argocd.argoproj.io/secret-type: cluster, env: prod}
stringData:
  name: $MEMBER
  server: $M_SERVER
  config: '$ARGO_CONFIG'
YAML
kubectl -n flux-system create secret generic "$FLUX_SECRET" --from-file=value="$KC_IN" --dry-run=client -o yaml |
  kubectl apply -f - >/dev/null
kubectl -n flux-system label secret "$FLUX_SECRET" env=prod --overwrite >/dev/null
# The Flux object that makes the kubeconfig a candidate: a remote-cluster
# Kustomization, suspended, since it only has to name the Secret.
kubectl apply -f - >/dev/null <<YAML
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata: {name: member-apps, namespace: flux-system}
spec:
  suspend: true
  interval: 1h
  path: ./
  prune: false
  sourceRef: {kind: OCIRepository, name: member-apps}
  kubeConfig: {secretRef: {name: $FLUX_SECRET}}
YAML
kubectl --context "$CTX_M" create ns "$M_NS" --dry-run=client -o yaml | kubectl --context "$CTX_M" apply -f - >/dev/null
kubectl --context "$CTX_M" -n "$M_NS" create secret docker-registry zot-push --docker-server="$REG" \
  --docker-username=pusher --docker-password="$PUSH_PW" --dry-run=client -o yaml | kubectl --context "$CTX_M" apply -f - >/dev/null
pass "$M_SERVER is the Argo CD cluster Secret $ARGO_NS/$ARGO_SECRET and the Flux kubeconfig flux-system/$FLUX_SECRET; the hub's own Secret is labelled env=prod too"

section "Pacto on the hub with the cluster inventory granted"
kubectl create ns "$NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n "$NS" create secret docker-registry zot-pull --docker-server="$REG" --docker-username=puller \
  --docker-password="$PULL_PW" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
helm install pacto-operator "$TGZ" -n "$NS" --kube-context "$CTX_H" \
  --set global.imageRegistry= --set image.repository="$OP_REPO" --set image.tag="$VER" --set image.pullPolicy=Never \
  --set fullnameOverride=pacto-operator --set controller.stabilizationWindow=5s \
  --set delivery.enabled=true --set delivery.image.repository="$DL_REPO" --set delivery.image.tag="$VER" --set delivery.image.pullPolicy=Never \
  --set registry="$REG/acme" --set "insecureRegistries[0]=$REG" --set "delivery.insecureRegistries[0]=$REG" \
  --set 'dashboard.ociSecrets[0]=zot-pull' \
  --set clusterInventory.enabled=true --set 'clusterInventory.secretNamespaces={argocd,flux-system}' \
  --wait --timeout 240s >/dev/null
wait_managed_ready pacto-dashboard >/dev/null || fail "the hub dashboard Deployment is not Ready"
H_UID="$(kubectl --context "$CTX_H" get ns kube-system -o jsonpath='{.metadata.uid}')"
pass "hub $H_UID runs Pacto $VER with clusterInventory on for argocd and flux-system"

pacto_hub() { (cd "$WORK" && "$BIN" "$@" --context "$CTX_H" --detected "k8s:$NS/$DETECTED"); }
list() { DOCKER_CONFIG="$PULL_CFG" pacto_hub cluster list --output-format json; }
candidate() { # SOURCE NAMESPACE NAME — that candidate in pacto cluster list
  list | jq -c --arg s "$1" --arg n "$2" --arg m "$3" 'first(.candidates[]? | select(.source == $s and .namespace == $n and .name == $m)) // {}'
}
candidate_is() { candidate "$1" "$2" "$3" | jq -e "$4" >/dev/null 2>&1; } # SOURCE NAMESPACE NAME JQ_FILTER
restart_hub() { # the Detector reads the inventory on start
  kubectl -n "$NS" rollout restart deploy/pacto-operator >/dev/null
  kubectl -n "$NS" rollout status deploy/pacto-operator --timeout=180s >/dev/null || fail "the hub operator did not roll out"
}

section "SCENARIO 1: the hub lists the member as an unmatched candidate"
eventually 60 candidate_is argocd "$ARGO_NS" "$ARGO_SECRET" '.match == "unmatched" and .labels.env == "prod"' || {
  list >&2 || true; fail "pacto cluster list shows no unmatched Argo CD candidate $ARGO_NS/$ARGO_SECRET labelled env=prod"
}
candidate_is flux flux-system "$FLUX_SECRET" '.match == "unmatched" and .labels.env == "prod"' || {
  list >&2 || true; fail "pacto cluster list shows no unmatched Flux candidate flux-system/$FLUX_SECRET labelled env=prod"
}
pass "pacto cluster list: Argo CD $ARGO_NS/$ARGO_SECRET and Flux flux-system/$FLUX_SECRET read unmatched ($(candidate argocd "$ARGO_NS" "$ARGO_SECRET" | jq -r .reason))"

cluster_approver_role "$CTX_H"
DASH_PF="$(pf "$LOCAL_DASH_PORT" svc/pacto-dashboard 3000)"
viewer_session() {
  dash_token_login "$LOCAL_DASH_PORT" "$(kubectl -n "$NS" create token pacto-viewer --audience pacto-dashboard --duration 20m)" link
}
SESSION="$(viewer_session)"
[[ -n "$SESSION" && "$SESSION" != *" "* ]] || fail "the pacto-viewer token did not sign in: $SESSION"
dash() { curl -sS -H "Cookie: __Host-pacto-session=$SESSION" "http://127.0.0.1:${LOCAL_DASH_PORT}$1"; }
dash_code() { curl -s -o "$WORK/download.yaml" -w '%{http_code}' -H "Cookie: __Host-pacto-session=$SESSION" "http://127.0.0.1:${LOCAL_DASH_PORT}$1"; }
hidden() { dash /api/fleet/clusters | jq -e '(.hiddenCandidates // 0) > 0 and ((.candidates // []) | length) == 0' >/dev/null; }
eventually 40 hidden || { dash /api/fleet/clusters >&2 || true; fail "the reader without approve on clusterjoins sees candidates or no hidden count"; }
code="$(dash_code '/api/fleet/clusters/install?from=argocd')"
[ "$code" = 403 ] || fail "the reader without approve on clusterjoins got $code on the download, not 403"
pass "a dashboard reader without approve on clusterjoins: $(dash /api/fleet/clusters | jq .hiddenCandidates) hidden candidates and 403 on the download"
kubectl create clusterrolebinding pacto-cluster-approver --clusterrole=pacto-cluster-approver \
  --user=oidc:alice@acme.test --serviceaccount="$NS:pacto-viewer" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
downloads() { [ "$(dash_code '/api/fleet/clusters/install?from=argocd')" = 200 ] && grep -q '^kind: ApplicationSet' "$WORK/download.yaml"; }
eventually 20 downloads || fail "the approver could not download the ApplicationSet"
grep -qE '^[[:space:]]+hub:' "$WORK/download.yaml" && fail "the dashboard download sets hub.url"
pass "an approver downloads the ApplicationSet, which sets no hub.url"
kill "$DASH_PF" 2>/dev/null || true
DASH_PF=

section "SCENARIO 2: the generated ApplicationSet installs Pacto on the member, never on the hub"
pacto_hub cluster install --from argocd --insecure-registry --chart "$CHART" > "$WORK/m.yaml" 2>"$WORK/m.err" ||
  { cat "$WORK/m.err" >&2; fail "pacto cluster install --from argocd failed"; }
as_json() { kubectl create --dry-run=client -o json -f "$1"; } # FILE — its objects as one JSON List
APPSET="$(as_json "$WORK/m.yaml" | jq -c 'first(.items[] | select(.kind == "ApplicationSet"))')"
keys="$(jq -c '.spec.template.spec.source.helm.valuesObject | keys' <<<"$APPSET")"
[ "$keys" = '["insecureRegistries","registry"]' ] || fail "the generated values hold $keys, not exactly registry and insecureRegistries"
jq -e --arg r "$REG/acme" --arg h "$REG" '.spec.template.spec.source.helm.valuesObject == {registry: $r, insecureRegistries: [$h]}' <<<"$APPSET" >/dev/null ||
  fail "the generated values are not registry=$REG/acme and insecureRegistries=[$REG]: $(jq -c '.spec.template.spec.source.helm.valuesObject' <<<"$APPSET")"
kubectl apply -f "$WORK/m.yaml" >/dev/null
# The documented push credential: registryPullSecret through the engine's own
# values, applied apart from the generated file.
kubectl -n "$ARGO_NS" patch applicationset pacto-members --type=json \
  -p "[{\"op\":\"add\",\"path\":\"/spec/template/spec/source/helm/valuesObject/registryPullSecret\",\"value\":\"$M_NS/zot-push\"}]" >/dev/null
member_ready() { [ "$(kubectl --context "$CTX_M" -n "$M_NS" get deploy "$M_OP" -o jsonpath='{.status.readyReplicas}' 2>/dev/null)" = 1 ]; }
eventually 120 member_ready || fail "the member never ran the operator the ApplicationSet installs"
pass "values exactly registry and insecureRegistries; the member runs $M_OP"

member_detected() { kubectl --context "$CTX_M" -n "$M_NS" get configmap "$M_DETECTED" -o jsonpath='{.data.detected\.json}' 2>/dev/null; }
has_key() { [ -n "$(member_detected | jq -r '.installation.snapshotKeyID // ""')" ]; }
eventually 60 has_key || fail "the member's detection advertises no snapshot key"
M_UID="$(member_detected | jq -r .cluster)"
K1="$(member_detected | jq -r .installation.snapshotKeyID)"
P1="$(member_detected | jq -r .installation.snapshotKey)"
member_row() { list | jq -c --arg u "$M_UID" 'first(.clusters[]? | select(.uid == $u)) // {}'; }
row_is() { member_row | jq -e "$1" >/dev/null 2>&1; } # JQ_FILTER
eventually 60 row_is '.state == "pending"' || { list >&2 || true; fail "pacto cluster list does not show $M_UID pending"; }
pass "the member $M_UID published its snapshot and reads pending"

releases() { helm list -A --kube-context "$CTX_H" -o json | jq '[.[] | select(.chart | startswith("pacto-operator"))] | length'; }
[ "$(releases)" = 1 ] || fail "the hub holds $(releases) Pacto releases"
apps="$(kubectl -n "$ARGO_NS" get applications -o json | jq -c '[.items[].spec.destination.server]')"
[ "$apps" = "[\"$M_SERVER\"]" ] || fail "the ApplicationSet generated Applications for $apps, not only $M_SERVER"
if kubectl get deploy -n "$M_NS" -o name 2>/dev/null | grep -q .; then fail "the hub runs a Pacto in namespace $M_NS"; fi
pass "the hub keeps one Pacto release: its own labelled cluster Secret got no Application"

section "SCENARIO 3: a stated UID reads same-uid while pending, matched once approved"
kubectl -n "$ARGO_NS" annotate secret "$ARGO_SECRET" "pacto.trianalab.io/cluster-uid=$M_UID" --overwrite >/dev/null
restart_hub
eventually 60 candidate_is argocd "$ARGO_NS" "$ARGO_SECRET" ".match == \"same-uid\" and .uid == \"$M_UID\"" || {
  candidate argocd "$ARGO_NS" "$ARGO_SECRET" >&2; fail "the candidate stating $M_UID does not read same-uid while the member is pending"
}
row_is '.state == "pending" and ((.inventoryEnvironments // []) | length) == 0' ||
  fail "the pending member has inventory environments: $(member_row)"
pass "the candidate reads same-uid and the pending member has no inventory environment"

# sign STEP ARGS... — joinsign as one person with its own Sigstore, then add
# its signer (its issuer and root) to the hub's delivery.identities.signers.
sign() {
  local step="$1"; shift
  local root="$WORK/root-$step.json" iss="https://idp-$step.acme.test" signers
  DOCKER_CONFIG="$PUSH_CFG" "$JOINSIGN" -root "$root" -registry "$REG/acme" -hub "$H_UID" -cluster "$M_UID" -issuer "$iss" "$@" || return 1
  signers="$(jq -cn --arg i "$iss" --rawfile r "$root" '[{issuer: $i, usernamePrefix: "oidc:", subjects: "[^@]+@acme\\.test", trustedRoot: $r}]')"
  helm upgrade pacto-operator "$TGZ" -n "$NS" --kube-context "$CTX_H" --reuse-values \
    --set-json "delivery.identities.signers=$signers" --wait --timeout 240s >/dev/null || fail "the hub did not take the signer of $iss"
}
sign approve -approver oidc:alice@acme.test -email alice@acme.test -key "sha256:$K1" -public-key "$P1" || fail "joinsign could not approve $M_UID"
eventually 40 row_is '.state == "approved"' || { member_row >&2; fail "$M_UID does not read approved"; }
eventually 20 candidate_is argocd "$ARGO_NS" "$ARGO_SECRET" ".match == \"matched\" and .member.uid == \"$M_UID\"" || {
  candidate argocd "$ARGO_NS" "$ARGO_SECRET" >&2; fail "the candidate stating $M_UID does not read matched to the approved member"
}
row_is 'any(.inventoryEnvironments[]?; .name == "prod" and (.source | contains("env=prod")))' ||
  fail "the approved member lists no inventory environment prod named by its label: $(member_row)"
row_is '([.environments[]?.name] | index("prod")) == null' || fail "prod reached the member's signed environments: $(member_row)"
pass "matched to $M_UID; inventoryEnvironments: $(member_row | jq -c '[.inventoryEnvironments[] | {name, source}]'), environments without prod"

section "SCENARIO 4: deleting the ApplicationSet leaves the member operator running"
kubectl -n "$ARGO_NS" delete applicationset pacto-members --wait=true >/dev/null
no_apps() { [ "$(kubectl -n "$ARGO_NS" get applications -o name | grep -c . || true)" = 0 ]; }
eventually 40 no_apps || fail "the Applications outlived their ApplicationSet"
always 10 member_ready || fail "the member operator went with the ApplicationSet"
pass "the ApplicationSet and its Application are gone; $M_OP still runs on the member"
# Remove the member's Pacto on purpose: Argo CD installed no Helm release, so
# the chart's objects are deleted as rendered.
helm template pacto "$TGZ" -n "$M_NS" --set registry="$REG/acme" |
  kubectl --context "$CTX_M" delete --ignore-not-found --wait=true -f - >/dev/null 2>&1 || true
kubectl --context "$CTX_M" delete ns "$M_NS" --wait=true --timeout=180s >/dev/null || fail "the member's $M_NS namespace never went"
pass "Pacto removed from the member explicitly"

section "SCENARIO 5: the pushed Flux install, pinned by digest"
DOCKER_CONFIG="$PUSH_CFG" pacto_hub cluster install --from flux --insecure-registry --chart "$CHART" --push > "$WORK/bootstrap.yaml" 2>"$WORK/push.err" ||
  { cat "$WORK/push.err" >&2; fail "pacto cluster install --from flux --push failed"; }
pushed_digest() { sed -n 's/^pushed [^@]*@\(sha256:[0-9a-f]*\);.*/\1/p' "$1"; } # STDERR_FILE
D1="$(pushed_digest "$WORK/push.err")"
[ -n "$D1" ] || fail "the push printed no digest: $(cat "$WORK/push.err")"
pinned="$(as_json "$WORK/bootstrap.yaml" | jq -r 'first(.items[] | select(.kind == "OCIRepository")) | .spec.ref.digest')"
[ "$pinned" = "$D1" ] || fail "the bootstrap pins $pinned, not the pushed $D1"
kubectl apply -f "$WORK/bootstrap.yaml" >/dev/null
eventually 120 member_ready || fail "the member never ran the operator the Flux HelmRelease installs"
[ "$(releases)" = 1 ] || fail "the hub holds $(releases) Pacto releases after the Flux install"
pass "the bootstrap pins $D1; the member runs $M_OP again and the hub keeps one Pacto release"

TAG="members-flux-${D1:7:12}"
applied() { kubectl -n flux-system get kustomization pacto-members -o jsonpath='{.status.lastAppliedRevision}'; }
has_applied() { [ -n "$(applied)" ]; }
eventually 40 has_applied || fail "the bootstrap Kustomization applied nothing"
R1="$(applied)"
# Other content under the old tag: the argocd install pushed, and its manifest
# put under $TAG, as any member holding push on pacto-targets could.
DOCKER_CONFIG="$PUSH_CFG" pacto_hub cluster install --from argocd --insecure-registry --chart "$CHART" --push >/dev/null 2>"$WORK/push2.err" ||
  { cat "$WORK/push2.err" >&2; fail "pacto cluster install --from argocd --push failed"; }
D2="$(pushed_digest "$WORK/push2.err")"
MT=application/vnd.oci.image.manifest.v1+json
curl -fsS -u "pusher:$PUSH_PW" -H "Accept: $MT" "http://$REG/v2/acme/pacto-targets/manifests/$D2" > "$WORK/other.json"
curl -fsS -u "pusher:$PUSH_PW" -X PUT -H "Content-Type: $MT" --data-binary "@$WORK/other.json" \
  "http://$REG/v2/acme/pacto-targets/manifests/$TAG" >/dev/null || fail "zot refused re-pointing $TAG"
now="$(curl -fsS -I -u "pusher:$PUSH_PW" -H "Accept: $MT" "http://$REG/v2/acme/pacto-targets/manifests/$TAG" | tr -d '\r' | sed -n 's/^[Dd]ocker-[Cc]ontent-[Dd]igest: //p')"
[ "$now" = "$D2" ] || fail "$TAG resolves to $now, not the other content $D2"
for k in ocirepository kustomization; do
  kubectl -n flux-system annotate "$k" pacto-members "reconcile.fluxcd.io/requestedAt=$(date +%s)" --overwrite >/dev/null
done
unchanged() {
  [ "$(kubectl -n flux-system get ocirepository pacto-members -o jsonpath='{.spec.ref.digest}')" = "$D1" ] &&
    [ "$(applied)" = "$R1" ] && member_ready
}
always 10 unchanged || fail "re-pointing $TAG moved the hub's bootstrap ($(applied), was $R1) or the member"
pass "$TAG now names $D2; the bootstrap still applies $R1 and the member is unchanged"

echo
echo "CLUSTER-INSTALL E2E PASS"
