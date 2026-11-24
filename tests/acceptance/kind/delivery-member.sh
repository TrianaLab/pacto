#!/usr/bin/env bash
# Deliver to a remote cluster through its member (#570) on two real kind
# clusters and an authenticated zot. The hub writes nothing into the member
# and needs no route to it: a person's signed request goes to the member's
# request stream in <registry>/pacto-targets, the member's operator raises a
# promote as an agent on that person's behalf, and the member's gates,
# approvals and Flux decide the rest.
#
# Unit tests pin the request and outcome records, the intake's refusals, the
# placement and the hub's plan on fakes; only real clusters run the intake
# against a real API server (its SubjectAccessReview, the Operation admission
# that names the operator as actor), the promoter's pacto.published gate on a
# real signature and an apply through Flux while the hub is down.
#
#   zot ($ZOT)          on the kind docker network, htpasswd auth: pusher may
#                       push, puller may only read
#   hub ($CLUSTER)      the operator chart with registry=<zot>/acme and
#                       delivery, its approver signers one per joinsign run
#   member ($MEMBER)    profile default with delivery, registry=<zot>/acme, the
#                       recorder with its public key, no hub.url, egress to the
#                       hub denied by NetworkPolicy, Flux installed first from
#                       the chart's vendored manifests (the adopted applier),
#                       its node named us-prod (so it takes requests for
#                       production only), a billing Stage in production on an
#                       OCIRepository labelled apply-ref that holds every
#                       promote for a person, and a billing Stage in staging
#
# joinsign (tests/acceptance/kind/joinsign) plays each person against a private
# Sigstore with no browser login: `request` signs a member request through the
# WriteOperationRequests `pacto delivery propose --confirm` calls, and
# `approve-intent` signs a decision through the MemberTrust, IntentAt and
# ApproveIntent `pacto delivery approve --intent --registry` calls. Every run
# has its own Sigstore, so the run writes its root and waits; the script lists
# its signer on the member (and on the hub, which verifies requests too) and
# only then lets it sign. keylesssign signs the published versions as acme's
# release workflow.
#
# Claims:
#   1. the member publishes its join block naming the recorder key, the billing
#      Stage stream and the environments it takes requests for; alice approves
#      it on the hub;
#   2. billing 1.5.0 and 1.7.0 are signed by the trusted release workflow and
#      1.6.0 by nobody;
#   3. alice may create Operations in billing on the member, as a user;
#   4. `pacto delivery plan --service billing --environment production` on the
#      hub places billing on us-prod, source unconfirmed, with no request yet;
#   5. alice's request for 1.5.0 raises billing-req-<hex> on the member, its
#      actor the member operator's ServiceAccount, on behalf of alice, from
#      the hub that approved the member (not claimed), held for a person; the
#      hub plan reads held, confirmed by alice, with the approve command;
#   6. with the hub's operator and dashboard scaled to zero, bob approves from
#      the intent record with no kubeconfig of the member: 1.5.0 ships through
#      Flux and billing runs it; with the hub back the plan reads applied
#      (AC4: delivery through the member with no hub);
#   7. alice's request for the forged 1.6.0 raises an Operation that
#      pacto.published rejects; the hub plan reads it refused (Rejected) and
#      `pacto delivery refusals --registry` lists a Rejected member row for
#      us-prod (the forged-Operation acceptance);
#   8. a request for staging is refused OutsideAllowlist; carol, listed as a
#      signer but granted nothing, is refused NotAllowedHere; a request from a
#      run whose issuer nobody lists gets no outcome and the member's
#      pacto_member_requests_unverified_total grows by one;
#   9. with the member's operator, promoter and recorder scaled to zero and zot
#      unreachable from the member, billing stays Ready on 1.5.0 (an
#      unreachable hub or registry never undoes an apply).
# Not run here: a rung that follows another member's Stage stream (Decision 11)
# needs a third cluster beyond this leg's time budget; the promoter's follow
# binding is covered by integrations/kubernetes/internal/delivery's
# TestFollowUpstream* harness tests.
# shellcheck disable=SC2016  # the jq filters name jq variables such as $u
set -euo pipefail
# shellcheck source=tests/acceptance/kind/lib.sh
source "$(dirname "$0")/lib.sh"
# shellcheck source=tests/acceptance/kind/delivery-lib.sh
source "$(dirname "$0")/delivery-lib.sh"

CLUSTER="${KIND_CLUSTER:-pacto-member-hub}"
MEMBER=pacto-member-us
NS=pacto-member # the release namespace in both clusters (read by dump_diag)
DNS=pacto-delivery
ZOT=pacto-member-zot
DETECTED=pacto-operator-detected
APP=billing # the service, its namespace on the member and its Stage
MEMBER_NAME=us-prod
OP_SA="system:serviceaccount:$NS:pacto-operator"
GITOPS_USERS='["system:serviceaccount:flux-system:kustomize-controller","kubernetes-admin"]'
PAUSE_IMG=registry.k8s.io/pause:3.9@sha256:7031c1b283388d2c2e09b57badb803c05ebed362dc88d84b480cc47f72a21097
FLUX_LAYER_TYPE=application/vnd.cncf.flux.content.v1.tar+gzip
SOURCE=https://github.com/acme/shop
CI_SUBJECT="$SOURCE/.github/workflows/release.yml@refs/heads/main"
PUSH_PW="push-$RANDOM$RANDOM"
PULL_PW="pull-$RANDOM$RANDOM"
VER="$(release_version kubernetes)"
OP_REPO="localhost:5001/pacto-operator/pacto-controller"
OP_IMG="${OP_REPO}:${VER}"
DL_REPO="localhost:5001/pacto-delivery"
DL_IMG="${DL_REPO}:${VER}"
DASH_IMG="localhost:5001/pacto-dashboard:$(release_version core)"
FLUX_DIR="$PACTO_CHART/files/flux"
image_of() { awk '$1 == "image:" { print $2 }' "$FLUX_DIR/deployment-$1.yaml"; }

for t in jq helm docker curl base64 oras openssl; do command -v "$t" >/dev/null 2>&1 || fail "$t is required"; done
command -v htpasswd >/dev/null 2>&1 || infra "install htpasswd" sudo apt-get install -y -qq apache2-utils

WORK="$(mktemp -d)"
teardown() {
  for ctx in "kind-$CLUSTER" "kind-$MEMBER"; do
    helm uninstall pacto-operator -n "$NS" --kube-context "$ctx" --wait >/dev/null 2>&1 || true
    kubectl --context "$ctx" delete ns "$NS" "$DNS" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  done
  docker rm -f "$ZOT" >/dev/null 2>&1 || true
  kubectl --context "kind-$CLUSTER" delete clusterrole,clusterrolebinding pacto-cluster-approver --ignore-not-found >/dev/null 2>&1 || true
  kind delete cluster --name "$MEMBER" >/dev/null 2>&1 || true
}
dump_member() {
  echo "--- member: stages, operations, approvals, flux objects ---"
  kubectl --context "kind-$MEMBER" get stages,operations,approvals,ocirepositories,kustomizations -A -o yaml 2>/dev/null || true
  for d in "$NS/pacto-operator" "$DNS/pacto-operator-delivery" "$DNS/pacto-operator-delivery-recorder"; do
    echo "--- member: logs deploy/${d#*/} ---"
    kubectl --context "kind-$MEMBER" -n "${d%/*}" logs "deploy/${d#*/}" --tail=300 2>/dev/null || true
  done
}
# shellcheck disable=SC2154  # rc is assigned by rc=$? inside the trap body
trap 'rc=$?; [ $rc -ne 0 ] && { dump_diag "$NS"; dump_member; }; keep_or_teardown "$NS" "$CLUSTER" teardown; exit $rc' EXIT

section "two kind clusters, the images, pacto and the two signers"
cluster_start_bg
load_built_images "$OP_IMG" "$DL_IMG" "$DASH_IMG" || build_images
pull_pinned "$PAUSE_IMG"
FLUX_IMAGES=()
for c in source-controller kustomize-controller; do FLUX_IMAGES+=("$(image_of "$c")"); done
for img in "${FLUX_IMAGES[@]}"; do infra "pull $img" docker pull -q "$img" >/dev/null; done
BIN="$(build_pacto)"
JOINSIGN="$WORK/joinsign"
SIGN_BIN="$WORK/keylesssign"
(cd "$_PACTO_ROOT" && CGO_ENABLED=0 go build -o "$JOINSIGN" ./tests/acceptance/kind/joinsign &&
  CGO_ENABLED=0 go build -o "$SIGN_BIN" ./tests/acceptance/kind/keylesssign) || fail "building the signers failed"
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
infra "load the images into the hub" load_images "$OP_IMG" "$DL_IMG" "$DASH_IMG"
CLUSTER="$MEMBER" infra "load the images into the member" load_images "$OP_IMG" "$DL_IMG" "${PAUSE_IMG%@*}" "${FLUX_IMAGES[@]}"
hk() { kubectl --context "$CTX_H" "$@"; }
mk() { kubectl --context "$CTX_M" "$@"; }
ip_of() { docker inspect -f '{{(index .NetworkSettings.Networks "kind").IPAddress}}' "$1"; }
pass "hub $CLUSTER and member $MEMBER run with the images"

section "zot with a push and a pull-only user on the kind network"
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
    "accessControl": {"repositories": {"**": {"policies": [
      {"users": ["pusher"], "actions": ["read", "create", "update"]},
      {"users": ["puller"], "actions": ["read"]}
    ], "defaultPolicy": []}}}
  },
  "log": {"level": "info"}
}
JSON
chmod -R a+rX "$ZDIR"
docker run -d --name "$ZOT" --network kind -v "$ZDIR:/etc/zot:ro" "${PACTO_REGISTRY_IMAGE%@*}" serve /etc/zot/config.json >/dev/null
REG="$(ip_of "$ZOT"):5000"
asks_auth() { [ "$(curl -s -o /dev/null -w '%{http_code}' "http://$REG/v2/")" = 401 ]; }
eventually 30 asks_auth || { docker logs "$ZOT" >&2; fail "zot at $REG does not ask for credentials"; }
docker_config() { # USER PASSWORD — prints a DOCKER_CONFIG directory holding them for $REG
  local d; d="$(mktemp -d)"
  printf '{"auths":{"%s":{"auth":"%s"}}}' "$REG" "$(printf '%s:%s' "$1" "$2" | base64 | tr -d '\n')" > "$d/config.json"
  echo "$d"
}
PUSH_CFG="$(docker_config pusher "$PUSH_PW")"
PULL_CFG="$(docker_config puller "$PULL_PW")"
export PACTO_INSECURE_REGISTRIES="$REG"
pass "zot answers at $REG and asks for credentials"

section "billing 1.5.0 and 1.7.0 signed by acme's release workflow, 1.6.0 by nobody"
# publish VERSION — the contract bundle and a Flux content artifact at
# acme/apps/billing holding the Deployment and its Pacto, labelled production;
# prints the artifact's digest.
publish() {
  local v="$1" d tree tgz out ref digest
  d="$(mktemp -d)"; tree="$(mktemp -d)"; tgz="$(mktemp -t billing.XXXXXX)"
  cat > "$d/pacto.yaml" <<YAML
pactoVersion: '2.0'
service:
  name: $APP
  version: $v
  owner: {team: team-$APP, dri: d, contacts: [{type: email, value: a@e.com, purpose: escalation}]}
workload: service
state: {type: stateless, persistence: {scope: local, durability: ephemeral}, dataCriticality: low}
YAML
  out="$(DOCKER_CONFIG="$PUSH_CFG" "$BIN" push "oci://$REG/acme/$APP:$v" -p "$d" 2>&1)" || { echo "$out" >&2; fail "pacto push $APP $v failed"; }
  ref="oci://$REG/acme/$APP@$(grep -oE 'sha256:[0-9a-f]{64}' <<<"$out" | head -1)"
  cat > "$tree/deployment.yaml" <<YAML
apiVersion: apps/v1
kind: Deployment
metadata: {name: $APP}
spec:
  replicas: 1
  selector: {matchLabels: {app: $APP}}
  template:
    metadata: {labels: {app: $APP}, annotations: {version: "$v"}}
    spec:
      containers: [{name: app, image: "${PAUSE_IMG%@*}"}]
YAML
  cat > "$tree/pacto.yaml" <<YAML
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata:
  name: $APP
  labels: {delivery.pacto.trianalab.io/environment: production}
spec:
  checkIntervalSeconds: 30
  contractRef: {oci: "$ref"}
  target: {workloadRef: {name: $APP, kind: Deployment}}
YAML
  tar -czf "$tgz" -C "$tree" .
  digest="$(oras push --plain-http --disable-path-validation -u pusher -p "$PUSH_PW" --format go-template='{{.digest}}' \
    --annotation "delivery.pacto.trianalab.io/contract=$ref" --annotation "org.opencontainers.image.source=$SOURCE" \
    "$REG/acme/apps/$APP:$v" "$tgz:$FLUX_LAYER_TYPE")"
  [ -n "$digest" ] || fail "pushing $APP $v resolved no digest"
  echo "$digest"
}
D150="$(publish 1.5.0)"
D160="$(publish 1.6.0)"
D170="$(publish 1.7.0)"
CI_ROOT="$WORK/ci-root.json"
DOCKER_CONFIG="$PUSH_CFG" "$SIGN_BIN" -root "$CI_ROOT" -registry "$REG" -subject "$CI_SUBJECT" -repository "$SOURCE" \
  "acme/apps/$APP@$D150" "acme/apps/$APP@$D170" || fail "signing 1.5.0 and 1.7.0 failed"
pass "billing 1.5.0 ($D150) and 1.7.0 carry a signature by $CI_SUBJECT; 1.6.0 ($D160) carries none"

section "the member: adopted Flux, then Pacto with delivery and no hub"
secret() { # CONTEXT NAMESPACE NAME USER PASSWORD
  kubectl --context "$1" -n "$2" create secret docker-registry "$3" --docker-server="$REG" --docker-username="$4" --docker-password="$5" \
    --dry-run=client -o yaml | kubectl --context "$1" apply -f - >/dev/null
}
for n in "$NS" "$DNS" "$APP"; do
  mk create ns "$n" --dry-run=client -o yaml | mk apply -f - >/dev/null
  secret "$CTX_M" "$n" zot-push pusher "$PUSH_PW"
done
mk label node "${MEMBER}-control-plane" "alpha.eksctl.io/cluster-name=$MEMBER_NAME" --overwrite >/dev/null
RECORDER_KEY="$WORK/recorder.pem"
RECORDER_PUB="$WORK/recorder.pub"
openssl genpkey -algorithm ed25519 -out "$RECORDER_KEY" 2>/dev/null || fail "openssl could not generate the recorder's key"
openssl pkey -in "$RECORDER_KEY" -pubout -out "$RECORDER_PUB"
mk -n "$DNS" create secret generic recorder-key --from-file=key.pem="$RECORDER_KEY" --dry-run=client -o yaml | mk apply -f - >/dev/null
# The member takes nothing from the hub but the registry: no pod in the
# release or delivery namespace may reach the hub's node.
HUB_IP="$(ip_of "${CLUSTER}-control-plane")"
for n in "$NS" "$DNS"; do
  mk apply -f - >/dev/null <<YAML
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: no-route-to-hub, namespace: $n}
spec:
  podSelector: {}
  policyTypes: [Egress]
  egress: [{to: [{namespaceSelector: {}}]}, {to: [{ipBlock: {cidr: 0.0.0.0/0, except: [$HUB_IP/32]}}]}]
YAML
done
# Flux before Pacto: delivery.flux.enabled is auto and reads the served kinds.
flux_files=("$FLUX_DIR/namespace-flux-system.yaml")
for f in "$FLUX_DIR"/*.yaml; do
  case "$f" in *namespace-flux-system.yaml | *helm-controller.yaml) ;; *) flux_files+=("$f") ;; esac
done
for f in "${flux_files[@]}"; do infra "apply $(basename "$f")" mk apply --server-side -f "$f" >/dev/null; done
infra "serve Flux's kinds" mk wait --for=condition=Established --timeout=60s crd/ocirepositories.source.toolkit.fluxcd.io \
  crd/kustomizations.kustomize.toolkit.fluxcd.io >/dev/null
SETS=(--set global.imageRegistry= --set image.repository="$OP_REPO" --set image.tag="$VER" --set image.pullPolicy=Never
      --set fullnameOverride=pacto-operator --set controller.stabilizationWindow=5s
      --set delivery.image.repository="$DL_REPO" --set delivery.image.tag="$VER" --set delivery.image.pullPolicy=Never
      --set registry="$REG/acme" --set registryPullSecret="$NS/zot-push"
      --set "insecureRegistries[0]=$REG" --set "delivery.insecureRegistries[0]=$REG" --set delivery.enabled=true)
helm install pacto-operator "$PACTO_CHART" -n "$NS" --kube-context "$CTX_M" "${SETS[@]}" --set dashboard.enabled=false \
  --set metrics.secure=false --set delivery.createNamespace=false --set-json "delivery.admissionPolicies.gitopsUsers=$GITOPS_USERS" \
  --set delivery.environments.enabled=false --set delivery.promoter.poll=2s \
  --set delivery.promoter.registryCredentials.secretName=zot-push \
  --set delivery.publishers.registry=ghcr.io/acme --set-file delivery.publishers.trustedRoot="$CI_ROOT" \
  --set delivery.recorder.enabled=true --set delivery.recorder.poll=5s --set delivery.recorder.registryCredentials.secretName=zot-push \
  --set delivery.recorder.signing.ed25519.secretName=recorder-key --set delivery.recorder.signing.allowOperatorReadableKey=true \
  --set-file delivery.recorder.signing.publicKey="$RECORDER_PUB" \
  --wait --timeout 300s >/dev/null || fail "helm install pacto-operator on the member did not roll out"
infra "roll out Flux's source-controller" mk -n flux-system rollout status deploy/source-controller --timeout=240s
infra "roll out Flux's kustomize-controller" mk -n flux-system rollout status deploy/kustomize-controller --timeout=240s
op_args="$(mk -n "$NS" get deploy pacto-operator -o jsonpath='{.spec.template.spec.containers[0].args}')"
for want in --member-requests --recorder-trust-file= --member-streams-repository=; do
  grep -qF -- "$want" <<<"$op_args" || fail "the member operator runs without $want: $op_args"
done
! grep -qF -- --hub-url <<<"$op_args" || fail "the member operator dials a hub: $op_args"
pass "the member runs delivery with member requests on, no hub.url and no route to the hub"

section "the hub: delivery, and the member's billing Stages on Flux"
hk create ns "$NS" --dry-run=client -o yaml | hk apply -f - >/dev/null
secret "$CTX_H" "$NS" zot-push pusher "$PUSH_PW"
secret "$CTX_H" "$NS" zot-pull puller "$PULL_PW"
helm install pacto-operator "$PACTO_CHART" -n "$NS" --kube-context "$CTX_H" "${SETS[@]}" --set 'dashboard.ociSecrets[0]=zot-pull' \
  --wait --timeout 240s >/dev/null || fail "helm install pacto-operator on the hub did not roll out"
wait_managed_ready pacto-dashboard >/dev/null || fail "the hub dashboard Deployment is not Ready"
H_UID="$(hk get ns kube-system -o jsonpath='{.metadata.uid}')"
mk -n "$APP" apply -f - >/dev/null <<YAML
apiVersion: v1
kind: ServiceAccount
metadata: {name: $APP-deployer}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata: {name: $APP-deployer}
rules:
- {apiGroups: [apps], resources: [deployments], verbs: ["*"]}
- {apiGroups: [pacto.trianalab.io], resources: [pactos], verbs: ["*"]}
- {apiGroups: [apps], resources: [replicasets], verbs: [get, list, watch]}
- {apiGroups: [""], resources: [pods], verbs: [get, list, watch]}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata: {name: $APP-deployer}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: $APP-deployer}
subjects: [{kind: ServiceAccount, name: $APP-deployer, namespace: $APP}]
---
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata:
  name: $APP
  labels: {delivery.pacto.trianalab.io/apply-ref: $APP}
spec:
  interval: 1m
  url: oci://$REG/acme/apps/$APP
  insecure: true
  secretRef: {name: zot-push}
  ref: {tag: unreleased}
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata: {name: $APP}
spec:
  interval: 10m
  sourceRef: {kind: OCIRepository, name: $APP}
  path: ./
  prune: true
  targetNamespace: $APP
  serviceAccountName: $APP-deployer
  wait: true
  timeout: 2m
---
apiVersion: delivery.pacto.trianalab.io/v1alpha1
kind: Stage
metadata: {name: $APP}
spec:
  service: {name: $APP}
  environment: production
  applyRef: {kind: OCIRepository, name: $APP}
  workload: {kind: Deployment, name: $APP}
  candidates: {registry: {repository: "$REG/acme/apps/$APP"}}
  startLevel: advise
  approval: {always: true}
  gates: {impact: {declaredConsumersOnly: true}, verify: {window: 20s}}
---
apiVersion: delivery.pacto.trianalab.io/v1alpha1
kind: Stage
metadata: {name: $APP-staging}
spec:
  service: {name: $APP}
  environment: staging
  applyRef: {kind: OCIRepository, name: $APP-staging}
  workload: {kind: Deployment, name: $APP-staging}
  candidates: {registry: {repository: "$REG/acme/apps/$APP"}}
  startLevel: advise
YAML
stream_of() { mk -n "$APP" get stage "$APP" -o jsonpath='{.status.stream.id}' 2>/dev/null; }
has_stream() { [ -n "$(stream_of)" ]; }
eventually 60 has_stream || fail "the recorder never anchored a stream for $APP/$APP: $(mk -n "$APP" get stage "$APP" -o jsonpath='{.status}')"
published() { mk -n "$APP" get stage "$APP" -o json | jq -e --arg d "$1" '.status.published // [] | index($d)' >/dev/null; }
eventually 90 published "$D150" || fail "billing 1.5.0 never reached status.published: $(mk -n "$APP" get stage "$APP" -o jsonpath='{.status.publisher}')"
pass "hub $H_UID runs delivery; the member's Stage $APP/$APP records stream $(stream_of) and knows 1.5.0 as published"

section "CLAIM 1: the member joins, naming its recorder key, its Stage stream and the requests it takes"
detected() { mk -n "$NS" get configmap "$DETECTED" -o jsonpath='{.data.detected\.json}' 2>/dev/null; }
last_push() { detected | jq -c '.installation.lastPush // {}'; }
pushed_since() { # LAST_PUSH — the member pushed again since
  local now; now="$(last_push)"
  [ "$now" != "$1" ] && jq -e '.ref // "" | contains("pacto-targets:cluster-")' <<<"$now" >/dev/null
}
republish() { # restart the member operator, which publishes at once
  local before; before="$(last_push)"
  mk -n "$NS" rollout restart deploy/pacto-operator >/dev/null
  mk -n "$NS" rollout status deploy/pacto-operator --timeout=180s >/dev/null || fail "the member operator did not roll out"
  eventually 60 pushed_since "$before" || fail "the member never pushed its snapshot again: $(last_push)"
}
list() { (cd "$WORK" && DOCKER_CONFIG="$PULL_CFG" "$BIN" cluster list --context "$CTX_H" --registry "$REG/acme" --output-format json); }
member_row() { list | jq -c --arg u "$M_UID" 'first(.clusters[] | select(.uid == $u)) // {}'; }
row_is() { member_row | jq -e "$1" >/dev/null 2>&1; } # JQ_FILTER
# republish_until JQ — the snapshot published at start can precede the
# intake's first read of the approver signers, so restart until the hub's
# row matches.
republish_until() {
  local i
  for i in 1 2 3; do
    republish
    eventually 20 row_is "$1" && return 0
    echo "  the hub's row of $M_UID does not match yet (round $i): $(member_row)" >&2
  done
  return 1
}
eventually 60 pushed_since '{}' || fail "the member never published its snapshot: $(detected | jq -c .installation)"
M_UID="$(detected | jq -r .cluster)"
K1="$(detected | jq -r .installation.snapshotKeyID)"
P1="$(detected | jq -r .installation.snapshotKey)"
[ -n "$M_UID" ] && [ -n "$K1" ] && [ -n "$P1" ] || fail "the member detection names no cluster or key: $(detected | jq -c '{cluster, installation}')"
[ "$H_UID" != "$M_UID" ] || fail "hub and member report one kube-system UID: $H_UID"

# Signers: each run's issuer and root, listed per cluster.
SIGNERS_H='[]'
SIGNERS_M='[]'
add_signer() { # hub|member ISSUER ROOT_FILE
  local entry; entry="$(jq -nc --arg i "$2" --rawfile r "$3" '{issuer: $i, usernamePrefix: "oidc:", subjects: "[^@]+@acme\\.test", trustedRoot: $r}')"
  if [ "$1" = hub ]; then
    SIGNERS_H="$(jq -c --argjson e "$entry" '. + [$e]' <<<"$SIGNERS_H")"
    helm upgrade pacto-operator "$PACTO_CHART" -n "$NS" --kube-context "$CTX_H" --reuse-values \
      --set-json "delivery.identities.signers=$SIGNERS_H" --wait --timeout 240s >/dev/null || fail "the hub did not take the signer of $2"
  else
    SIGNERS_M="$(jq -c --argjson e "$entry" '. + [$e]' <<<"$SIGNERS_M")"
    helm upgrade pacto-operator "$PACTO_CHART" -n "$NS" --kube-context "$CTX_M" --reuse-values \
      --set-json "delivery.identities.signers=$SIGNERS_M" --wait --timeout 300s >/dev/null || fail "the member did not take the signer of $2"
  fi
}
# person RUN WHERE SUBCOMMAND ARGS... — joinsign SUBCOMMAND as one person with
# a Sigstore of its own: once it wrote its root, the run's signer is listed on
# WHERE (member, both or none) and only then does it sign. Its output is in
# $WORK/joinsign-RUN.log.
person() {
  local run="$1" where="$2" cmd="$3"; shift 3
  local root="$WORK/root-$run.json" ready="$WORK/ready-$run" log="$WORK/joinsign-$run.log" iss="https://idp-$run.acme.test" pid
  DOCKER_CONFIG="$PUSH_CFG" "$JOINSIGN" "$cmd" -root "$root" -ready "$ready" -issuer "$iss" -registry "$REG/acme" -context "$CTX_H" "$@" \
    > "$log" 2>&1 &
  pid=$!
  eventually 20 test -s "$root" || { cat "$log" >&2; fail "joinsign $cmd ($run) wrote no trusted root"; }
  case "$where" in
    both) add_signer member "$iss" "$root"; add_signer hub "$iss" "$root" ;;
    member) add_signer member "$iss" "$root" ;;
  esac
  touch "$ready"
  wait "$pid" || { cat "$log" >&2; fail "joinsign $cmd ($run) failed"; }
  sed 's/^/  /' "$log"
}

# alice approves the member as `pacto cluster approve` does; her signer is
# listed on the hub (which counts the record) and on the member (which
# derives its own hub from it).
cluster_approver_role "$CTX_H"
hk create clusterrolebinding pacto-cluster-approver --clusterrole=pacto-cluster-approver --user=oidc:alice@acme.test \
  --dry-run=client -o yaml | hk apply -f - >/dev/null
JOIN_ROOT="$WORK/root-join.json"
JOIN_ISS=https://idp-join.acme.test
DOCKER_CONFIG="$PUSH_CFG" "$JOINSIGN" -root "$JOIN_ROOT" -registry "$REG/acme" -hub "$H_UID" -cluster "$M_UID" -issuer "$JOIN_ISS" \
  -approver oidc:alice@acme.test -email alice@acme.test -key "sha256:$K1" -public-key "$P1" || fail "joinsign could not approve $M_UID"
add_signer hub "$JOIN_ISS" "$JOIN_ROOT"
add_signer member "$JOIN_ISS" "$JOIN_ROOT"
republish_until '.state == "approved" and .name == "'"$MEMBER_NAME"'" and .recorderKey != null and
    .requests != null and (.requests.environments | index("production")) and
    any(.streams[]?; .service.name == "'"$APP"'" and .environment == "production")' \
  || fail "the hub does not read $M_UID approved as $MEMBER_NAME with a recorder key, the $APP stream and requests for production: $(member_row)"
pass "$MEMBER_NAME ($M_UID) is approved and its join block names $(member_row | jq -c '{recorderKey: (.recorderKey != null), streams: [.streams[].stream], requests}')"

section "CLAIM 3: alice may create Operations in $APP on the member; bob may approve there"
mk apply -f - >/dev/null <<YAML
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata: {name: $APP-requester, namespace: $APP}
rules: [{apiGroups: [delivery.pacto.trianalab.io], resources: [operations], verbs: [create]}]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata: {name: $APP-requester, namespace: $APP}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: $APP-requester}
subjects: [{apiGroup: rbac.authorization.k8s.io, kind: User, name: "oidc:alice@acme.test"}]
YAML
mk -n "$APP" create rolebinding bob-edit --clusterrole=edit --user=oidc:bob@acme.test --dry-run=client -o yaml | mk apply -f - >/dev/null
[ "$(mk auth can-i create operations.delivery.pacto.trianalab.io -n "$APP" --as=oidc:alice@acme.test)" = yes ] \
  || fail "oidc:alice@acme.test may not create Operations in $APP"
[ "$(mk auth can-i create operations.delivery.pacto.trianalab.io -n "$APP" --as=oidc:carol@acme.test)" = no ] \
  || fail "oidc:carol@acme.test may create Operations in $APP"
pass "oidc:alice@acme.test may create Operations in $APP; oidc:carol@acme.test may not"

section "CLAIM 4: the hub plans billing on $MEMBER_NAME, proposed from the environments it takes requests for"
plan() { # DIGEST [ENVIRONMENT] — the hub's member plan
  (cd "$WORK" && DOCKER_CONFIG="$PULL_CFG" "$BIN" delivery plan --service "$APP" --environment "${2:-production}" --to "$1" \
    --registry "$REG/acme" --output-format json)
}
state_of() { plan "$1" | jq -c --arg u "$M_UID" 'first(.members.members[]? | select(.uid == $u)) // {}'; } # DIGEST
state_is() { state_of "$1" | jq -e "$2" >/dev/null 2>&1; } # DIGEST JQ_FILTER
p="$(plan "$D150")"
jq -e --arg u "$M_UID" '.members.placement.source == "unconfirmed" and any(.members.placement.clusters[]; .uid == $u)' <<<"$p" >/dev/null \
  || fail "the plan does not place billing on $M_UID unconfirmed: $p"
state_is "$D150" '.state == "no request"' || fail "the plan reads a request before any: $(state_of "$D150")"
pass "the plan places billing on $MEMBER_NAME (unconfirmed) and reads no request; confirm with: $(jq -r .members.next <<<"$p")"

section "CLAIM 5: alice's request raises a held promote on the member, as its operator on her behalf"
alice() { echo -cluster "$M_UID" -requester oidc:alice@acme.test -email alice@acme.test -service "$APP" -environment production; }
# shellcheck disable=SC2046  # alice is several flag pairs
person req-150 both request $(alice) -digest "$D150" -reason "ship 1.5.0 to us-prod"
member_ops() { mk -n "$APP" get operations -l delivery.pacto.trianalab.io/member-request -o json; }
op_for() { member_ops | jq -r --arg d "$1" 'first(.items[] | select(.spec.params.to == $d) | .metadata.name) // empty'; } # DIGEST
raised() { [ -n "$(op_for "$1")" ]; }
eventually 40 raised "$D150" || fail "the member raised no promote for alice's request: $(member_ops | jq -c '[.items[].metadata.name]')"
OP="$(op_for "$D150")"
[[ "$OP" == "$APP-req-"* ]] || fail "the raised Operation is named $OP, want $APP-req-<hex>"
mop() { mk -n "$APP" get operation "$OP" -o json; }
mop | jq -e --arg a "$OP_SA" --arg h "from hub $H_UID (request" '.spec.actor.user == $a and .spec.onBehalfOf.user == "oidc:alice@acme.test"
  and (.spec.reason | contains($h)) and (.spec.reason | contains("(claimed)") | not)' >/dev/null \
  || fail "$OP is not the operator's promote on alice's behalf from hub $H_UID: $(mop | jq -c '{actor: .spec.actor, onBehalfOf: .spec.onBehalfOf, reason: .spec.reason}')"
mheld() { [ "$(mk -n "$APP" get operation "$1" -o jsonpath='{.status.phase}')" = Held ]; }
eventually 60 mheld "$OP" || fail "$OP was never held for a person: $(mop | jq -c .status)"
eventually 60 state_is "$D150" '.state == "held" and (.next | contains("--intent"))' || fail "the hub plan does not read held: $(state_of "$D150")"
plan "$D150" | jq -e '.members.placement.source == "advertised" and .members.placement.confirmedBy == "oidc:alice@acme.test"' >/dev/null \
  || fail "the plan's placement is not confirmed by alice: $(plan "$D150" | jq -c .members.placement)"
INTENT="$(state_of "$D150" | jq -r .next | grep -oE -- '--intent [^ ]+' | cut -d' ' -f2)"
[ -n "$INTENT" ] || fail "the held member names no intent record: $(state_of "$D150")"
pass "$OP: actor $OP_SA on behalf of oidc:alice@acme.test, held; the hub reads held, confirmed by alice, intent $INTENT"

section "CLAIM 6: with the hub down, bob approves from the intent and Flux ships 1.5.0"
hub_scale() { # REPLICAS
  hk -n "$NS" scale deploy/pacto-operator deploy/pacto-dashboard --replicas="$1" >/dev/null
  if [ "$1" = 0 ]; then
    hk -n "$NS" wait --for=delete pod -l app.kubernetes.io/instance=pacto-operator --timeout=120s >/dev/null 2>&1 || true
  else
    hk -n "$NS" rollout status deploy/pacto-operator --timeout=180s >/dev/null || fail "the hub operator did not come back"
    hk -n "$NS" rollout status deploy/pacto-dashboard --timeout=180s >/dev/null || fail "the hub dashboard did not come back"
  fi
}
hub_scale 0
person approve-150 member approve-intent -email bob@acme.test -intent "$INTENT"
concluded_as() { [ "$(mk -n "$APP" get operation "$1" -o jsonpath='{.status.outcome}')" = "$2" ]; } # OP OUTCOME
eventually 100 concluded_as "$OP" Succeeded || fail "$OP did not succeed after bob's approval: $(mop | jq -c .status)"
runs_at() { # VERSION — billing is rolled out at VERSION
  mk -n "$APP" rollout status "deploy/$APP" --timeout=5s >/dev/null 2>&1 &&
    [ "$(mk -n "$APP" get deploy "$APP" -o jsonpath='{.spec.template.metadata.annotations.version}')" = "$1" ]
}
eventually 60 runs_at 1.5.0 || fail "billing does not run 1.5.0 on the member: $(mk -n "$APP" get ocirepository,kustomization "$APP" -o yaml)"
hub_scale 1
eventually 60 state_is "$D150" '.state == "applied"' || fail "the hub plan does not read applied: $(state_of "$D150")"
pass "bob's decision shipped 1.5.0 through Flux with the hub down; the hub, back, reads applied"

section "CLAIM 7: a request for the forged 1.6.0 is rejected by pacto.published and listed as a member refusal"
# shellcheck disable=SC2046
person req-160 both request $(alice) -digest "$D160" -reason "ship the forged 1.6.0"
eventually 40 raised "$D160" || fail "the member raised no promote for the forged request"
OP="$(op_for "$D160")"
eventually 60 concluded_as "$OP" Rejected || fail "$OP was not rejected: $(mop | jq -c .status)"
mop | jq -e 'tostring | contains("pacto.published")' >/dev/null || fail "$OP was rejected for another reason: $(mop | jq -c .status)"
eventually 60 state_is "$D160" '.state == "refused" and (.reason | startswith("Rejected"))' \
  || fail "the hub plan does not read the forged request refused as Rejected: $(state_of "$D160")"
refusals() { (cd "$WORK" && DOCKER_CONFIG="$PULL_CFG" "$BIN" delivery refusals --registry "$REG/acme"); }
member_refusal() { # JQ_FILTER — a refusals row of the member matches it
  refusals | jq -e --arg n "$MEMBER_NAME" "any(.refusals[]; .check == \"member\" and .cluster == \$n and ($1))" >/dev/null 2>&1
}
eventually 40 member_refusal '.digest == "'"$D160"'" and .code == "Rejected"' \
  || fail "pacto delivery refusals lists no member row for the forged request: $(refusals | jq -c '.refusals')"
pass "the forged 1.6.0 shipped nothing: $OP Rejected by pacto.published, and the hub lists the member refusal"

section "CLAIM 8: outside the allowlist, without a grant and unsigned by a listed issuer: refused or never answered"
person req-staging both request -cluster "$M_UID" -requester oidc:alice@acme.test -email alice@acme.test -service "$APP" \
  -environment staging -digest "$D170"
eventually 40 member_refusal '.code == "OutsideAllowlist" and (.reason | contains("staging"))' \
  || fail "the staging request was not refused OutsideAllowlist: $(refusals | jq -c '.refusals')"
person req-carol both request -cluster "$M_UID" -requester oidc:carol@acme.test -email carol@acme.test -service "$APP" \
  -environment production -digest "$D170"
eventually 40 member_refusal '.code == "NotAllowedHere" and .actor == "oidc:carol@acme.test"' \
  || fail "carol's request was not refused NotAllowedHere: $(refusals | jq -c '.refusals')"
[ -z "$(op_for "$D170")" ] || fail "a refused request raised $(op_for "$D170")"
unverified() { # the member operator's count of requests that did not verify
  local sel pod
  sel="$(mk -n "$NS" get deploy pacto-operator -o json | jq -r '.spec.selector.matchLabels | to_entries | map("\(.key)=\(.value)") | join(",")')"
  pod="$(mk -n "$NS" get pods -l "$sel" --field-selector=status.phase=Running -o jsonpath='{.items[0].metadata.name}')"
  mk get --raw "/api/v1/namespaces/$NS/pods/$pod:8443/proxy/metrics" | awk '$1 == "pacto_member_requests_unverified_total" { print $2; f=1 } END { if (!f) print 0 }'
}
BEFORE="$(unverified)"
# shellcheck disable=SC2046
person req-unlisted none request $(alice) -digest "$D170"
counted() { [ "$(unverified)" = "$((BEFORE + 1))" ]; }
eventually 30 counted || fail "pacto_member_requests_unverified_total is $(unverified), want $((BEFORE + 1))"
always 5 counted || fail "pacto_member_requests_unverified_total moved past $((BEFORE + 1)): $(unverified)"
[ -z "$(op_for "$D170")" ] || fail "the unverifiable request raised $(op_for "$D170")"
pass "staging is OutsideAllowlist, carol is NotAllowedHere, and the request no listed issuer signed was counted and never answered"

section "CLAIM 9: with the member's Pacto stopped and zot unreachable, billing keeps 1.5.0"
mk -n "$NS" scale deploy/pacto-operator --replicas=0 >/dev/null
mk -n "$DNS" scale deploy/pacto-operator-delivery deploy/pacto-operator-delivery-recorder --replicas=0 >/dev/null
docker network disconnect kind "$ZOT" >/dev/null || fail "zot could not be cut off the kind network"
always 20 runs_at 1.5.0 || fail "billing left 1.5.0 once the member's Pacto stopped and zot went away"
pass "billing stays Ready on 1.5.0 with the member's operator, promoter and recorder at zero and zot unreachable"

echo
echo "DELIVERY-MEMBER E2E PASS"
