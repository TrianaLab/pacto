#!/usr/bin/env bash
# Join a cluster through the registry (#567) on two real kind clusters and an
# authenticated zot.
#
# Unit tests pin the join states, the record checks and the reviews on fakes;
# only real clusters run the operator's publisher with its installation key,
# answer a SubjectAccessReview for approve on clusterjoins.pacto.trianalab.io
# and serve the hub dashboard through a pull-only registry credential.
#
#   zot ($ZOT)             on the kind docker network, htpasswd auth: user
#                          pusher may push, user puller may only read
#   member ($MEMBER)       the operator chart with registry=<zot>/acme and
#                          pusher in registryPullSecret, a Pact labelled
#                          environment production
#   hub ($CLUSTER)         the same, plus delivery with one approver signer per
#                          joinsign run, and puller in dashboard.ociSecrets
#
# joinsign (tests/acceptance/kind/joinsign) plays alice signing keylessly
# against a private Sigstore, through the same WriteClusterJoin `pacto cluster
# approve` calls. Each run has its own Sigstore, so each run adds an approver
# signer (its issuer and trustedRoot) to the hub before the hub reads it.
#
# Claims:
#   1. the member publishes cluster-<uid> and the hub lists it pending with its
#      name, name source, environments and the key id the member's detected
#      ConfigMap advertises (snapshotKeyID), in `pacto cluster list` and in the
#      hub dashboard's /api/fleet/clusters through the pull-only Secret;
#   2. a pending member is no source: `pacto fleet` lists none of its targets,
#      and the hub's `pacto impact` of a breaking payments answers as before the
#      member published a consumer of it, with no unread cluster counted (no
#      person approved a member yet, so the hub is no hub);
#   3. a snapshot signed by another key than the one checked gets nothing
#      signed (ClusterKeyMismatch);
#   4. alice, granted approve on clusterjoins as a user, approves it: the list
#      reads approved by oidc:alice@acme.test and `pacto fleet` lists the
#      member's targets under 4-part keys, and the hub's `pacto impact` is
#      blocked by checkout, which runs only in the member and names it with
#      the member's display name;
#   5. a record by bob, who has no grant, is not counted;
#   6. a new installation key reads pending with "the key changed";
#   7. alice's removal reads removed and the member's targets are gone: the
#      hub's `pacto impact` neither reads nor counts the member.
# shellcheck disable=SC2016  # the jq filters name jq variables such as $k
set -euo pipefail
# shellcheck source=tests/acceptance/kind/lib.sh
source "$(dirname "$0")/lib.sh"
# shellcheck source=tests/acceptance/kind/delivery-lib.sh
source "$(dirname "$0")/delivery-lib.sh"

CLUSTER="${KIND_CLUSTER:-pacto-join-hub}"
MEMBER=pacto-join-member
NS=pacto-join # the release namespace in both clusters (read by dump_diag and pf)
ZOT=pacto-join-zot
DETECTED=pacto-operator-detected
KEY_SECRET=pacto-operator-installation-key
APP_NS=eu-payments
LOCAL_DASH_PORT=18561
PUSH_PW="push-$RANDOM$RANDOM"
PULL_PW="pull-$RANDOM$RANDOM"
VER="$(release_version kubernetes)"
OP_REPO="localhost:5001/pacto-operator/pacto-controller"
OP_IMG="${OP_REPO}:${VER}"
DL_REPO="localhost:5001/pacto-delivery"
DL_IMG="${DL_REPO}:${VER}"
DASH_IMG="localhost:5001/pacto-dashboard:$(release_version core)"

for t in jq helm docker curl base64; do command -v "$t" >/dev/null 2>&1 || fail "$t is required"; done
command -v htpasswd >/dev/null 2>&1 || infra "install htpasswd" sudo apt-get install -y -qq apache2-utils

WORK="$(mktemp -d)"
DASH_PF=
teardown() {
  [ -n "$DASH_PF" ] && kill "$DASH_PF" 2>/dev/null
  helm uninstall pacto-operator -n "$NS" --kube-context "kind-$CLUSTER" --wait >/dev/null 2>&1 || true
  kubectl --context "kind-$CLUSTER" delete ns "$NS" pacto-delivery --ignore-not-found --wait=false >/dev/null 2>&1 || true
  docker rm -f "$ZOT" >/dev/null 2>&1 || true
  kubectl --context "kind-$CLUSTER" delete clusterrole,clusterrolebinding pacto-cluster-approver --ignore-not-found >/dev/null 2>&1 || true
  kind delete cluster --name "$MEMBER" >/dev/null 2>&1 || true
}
# shellcheck disable=SC2154  # rc is assigned by rc=$? inside the trap body
trap 'rc=$?; [ $rc -ne 0 ] && dump_diag "$NS"; keep_or_teardown "$NS" "$CLUSTER" teardown; { [ -z "$DASH_PF" ] || kill "$DASH_PF" 2>/dev/null || true; }; exit $rc' EXIT

section "two kind clusters, the images and the pacto CLI"
cluster_start_bg
load_built_images "$OP_IMG" "$DL_IMG" "$DASH_IMG" || build_images
BIN="$(build_pacto)"
JOINSIGN="$WORK/joinsign"
( cd "$_PACTO_ROOT" && CGO_ENABLED=0 go build -o "$JOINSIGN" ./tests/acceptance/kind/joinsign )
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
CLUSTER="$MEMBER" infra "load the operator image into the member" load_images "$OP_IMG"
pass "hub $CLUSTER and member $MEMBER run with the operator images"

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
REG="$(docker inspect -f '{{(index .NetworkSettings.Networks "kind").IPAddress}}' "$ZOT"):5000"
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

SETS=(--set global.imageRegistry= --set image.repository="$OP_REPO" --set image.tag="$VER" --set image.pullPolicy=Never
      --set fullnameOverride=pacto-operator --set controller.stabilizationWindow=5s
      --set delivery.image.repository="$DL_REPO" --set delivery.image.tag="$VER" --set delivery.image.pullPolicy=Never
      --set registry="$REG/acme" --set registryPullSecret="$NS/zot-push"
      --set "insecureRegistries[0]=$REG" --set "delivery.insecureRegistries[0]=$REG")
secret() { # CONTEXT NAME USER PASSWORD
  kubectl --context "$1" -n "$NS" create secret docker-registry "$2" --docker-server="$REG" --docker-username="$3" --docker-password="$4" \
    --dry-run=client -o yaml | kubectl --context "$1" apply -f - >/dev/null
}
for ctx in "$CTX_H" "$CTX_M"; do
  kubectl --context "$ctx" create ns "$NS" --dry-run=client -o yaml | kubectl --context "$ctx" apply -f - >/dev/null
  secret "$ctx" zot-push pusher "$PUSH_PW"
done
secret "$CTX_H" zot-pull puller "$PULL_PW"

detected() { kubectl --context "$CTX_M" -n "$NS" get configmap "$DETECTED" -o jsonpath='{.data.detected\.json}' 2>/dev/null; }
last_push() { detected | jq -c '.installation.lastPush // {}'; }
key_id() { detected | jq -r '.installation.snapshotKeyID // ""'; }
pushed_since() { # LAST_PUSH — the member pushed again since
  local now; now="$(last_push)"
  [ "$now" != "$1" ] && jq -e '.ref // "" | contains("pacto-targets:cluster-")' <<<"$now" >/dev/null
}
member_op() { kubectl --context "$CTX_M" -n "$NS" "$@"; }
stopped() { local r; r="$(member_op get deploy pacto-operator -o jsonpath='{.status.replicas}')"; [ -z "$r" ] || [ "$r" = 0 ]; }
scale_member() { # REPLICAS
  member_op scale deploy/pacto-operator --replicas="$1" >/dev/null
  if [ "$1" = 0 ]; then
    eventually 40 stopped || fail "the member operator never stopped"
  else
    member_op rollout status deploy/pacto-operator --timeout=180s >/dev/null || fail "the member operator never came back"
  fi
}
# republish — restart the member operator, which publishes at once.
republish() {
  local before; before="$(last_push)"
  member_op rollout restart deploy/pacto-operator >/dev/null
  member_op rollout status deploy/pacto-operator --timeout=180s >/dev/null || fail "the member operator did not roll out"
  eventually 60 pushed_since "$before" || fail "the member never pushed its snapshot again: $(last_push)"
}

section "SCENARIO 1: the member publishes and the hub lists it pending"
helm install pacto-operator "$PACTO_CHART" -n "$NS" --kube-context "$CTX_M" "${SETS[@]}" --set dashboard.enabled=false \
  --wait --timeout 180s >/dev/null
kubectl --context "$CTX_M" create ns "$APP_NS" --dry-run=client -o yaml | kubectl --context "$CTX_M" apply -f - >/dev/null
kubectl --context "$CTX_M" apply -f - >/dev/null <<YAML
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata:
  name: ledger
  namespace: $APP_NS
  labels: {delivery.pacto.trianalab.io/environment: production}
spec:
  checkIntervalSeconds: 30
  contractRef:
    inline: |
      pactoVersion: '2.0'
      service: {name: ledger, version: 1.0.0, owner: {team: payments, dri: d, contacts: [{type: email, value: a@e.com, purpose: escalation}]}}
      workload: service
      state: {type: stateless, persistence: {scope: local, durability: ephemeral}, dataCriticality: low}
  target: {workloadRef: {name: ledger, kind: Deployment}}
YAML
republish
M_UID="$(detected | jq -r .cluster)"
K1="$(key_id)"
P1="$(detected | jq -r .installation.snapshotKey)"
[ -n "$M_UID" ] && [ -n "$K1" ] && [ -n "$P1" ] || fail "the member detection names no cluster or key: $(detected | jq -c '{cluster, installation}')"
pass "member $M_UID published $(last_push | jq -r .ref) signed by sha256:$K1"

helm install pacto-operator "$PACTO_CHART" -n "$NS" --kube-context "$CTX_H" "${SETS[@]}" --set delivery.enabled=true \
  --set 'dashboard.ociSecrets[0]=zot-pull' --wait --timeout 240s >/dev/null
wait_managed_ready pacto-dashboard >/dev/null || fail "the hub dashboard Deployment is not Ready"
H_UID="$(kubectl --context "$CTX_H" get ns kube-system -o jsonpath='{.metadata.uid}')"
[ "$H_UID" != "$M_UID" ] || fail "hub and member report one kube-system UID: $H_UID"
pass "hub $H_UID runs delivery and its dashboard"

list() { (cd "$WORK" && DOCKER_CONFIG="$PULL_CFG" "$BIN" cluster list --context "$CTX_H" --registry "$REG/acme" --output-format json); }
member_row() { list | jq -c --arg u "$M_UID" 'first(.clusters[] | select(.uid == $u)) // {}'; }
row_is() { # JQ_FILTER — the member's row in pacto cluster list matches it
  member_row | jq -e --arg k "$K1" "$1" >/dev/null 2>&1
}
eventually 40 row_is '.state == "pending" and .keyID == $k and .name != "" and .nameSource != "" and
    ([.environments[]?.name] | index("production"))' || {
  list >&2 || true
  fail "pacto cluster list does not show $M_UID pending with its name, environments and key sha256:$K1"
}
pass "pacto cluster list: $M_UID pending as $(member_row | jq -r '"\(.name) (\(.nameSource))"'), key sha256:$K1 equal to the member's snapshotKeyID"

cluster_approver_role "$CTX_H"
kubectl --context "$CTX_H" create clusterrolebinding pacto-cluster-approver --clusterrole=pacto-cluster-approver \
  --user=oidc:alice@acme.test --serviceaccount="$NS:pacto-viewer" --dry-run=client -o yaml | kubectl --context "$CTX_H" apply -f - >/dev/null
DASH_PF="$(pf "$LOCAL_DASH_PORT" svc/pacto-dashboard 3000)"
SESSION="$(dash_token_login "$LOCAL_DASH_PORT" "$(kubectl --context "$CTX_H" -n "$NS" create token pacto-viewer --audience pacto-dashboard --duration 20m)" link)"
[[ -n "$SESSION" && "$SESSION" != *" "* ]] || fail "the pacto-viewer token did not sign in: $SESSION"
dash_row() {
  curl -fsS -H "Cookie: __Host-pacto-session=$SESSION" "http://127.0.0.1:${LOCAL_DASH_PORT}/api/fleet/clusters" |
    jq -e --arg u "$M_UID" --arg k "$K1" 'any(.clusters[]; .uid == $u and .state == "pending" and .keyID == $k)' >/dev/null
}
eventually 40 dash_row || {
  curl -sS -H "Cookie: __Host-pacto-session=$SESSION" "http://127.0.0.1:${LOCAL_DASH_PORT}/api/fleet/clusters" >&2 || true
  fail "the hub dashboard does not list $M_UID pending through the pull-only Secret"
}
kill "$DASH_PF" 2>/dev/null || true
DASH_PF=
pass "the hub dashboard lists $M_UID pending through dashboard.ociSecrets (puller)"

section "SCENARIO 2: a pending member is no source"
contract_dir() { # NAME VERSION WORKLOAD [DEPENDENCY_LINES] — prints a folder holding the contract
  local d; d="$(mktemp -d "$WORK/contract.XXXXXX")"
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
# payments 1.0.0 -> 2.0.0 also changes the workload, so it is BREAKING; checkout
# accepts ^1.0.0 only. Both stay local folders: the hub reads their consumers
# from its own Pactos and its members' snapshots, never from an index.
PAY_OLD="$(contract_dir payments 1.0.0 service)"
PAY_NEW="$(contract_dir payments 2.0.0 job)"
hub_impact() { # prints the hub's `pacto impact` of payments 1.0.0 -> 2.0.0 as JSON; the gate sets the exit code
  (cd "$WORK" && DOCKER_CONFIG="$PULL_CFG" "$BIN" impact "$PAY_OLD" "$PAY_NEW" --local "" --k8s --detected "k8s:$NS/$DETECTED" \
    --output-format json) || true
}
impact_is() { # JQ_FILTER — the hub's impact answer matches it ($u is the member's uid, $n its display name)
  hub_impact | jq -e --arg u "$M_UID" --arg n "${M_NAME:-}" "$1" >/dev/null 2>&1
}
member_targets() { # prints how many targets of the member the hub's fleet lists
  (cd "$WORK" && DOCKER_CONFIG="$PULL_CFG" "$BIN" fleet entities --kind target --k8s --detected "k8s:$NS/$DETECTED" --output-format json) |
    jq --arg u "$M_UID/" '[.entities[]? | select(.key | startswith($u))] | length'
}
n="$(member_targets)"
[ "$n" = 0 ] || fail "pacto fleet lists $n targets of the pending member"
pass "pacto fleet lists no target of the pending member"

impact_is 'has("gate")' || { hub_impact >&2; fail "the hub's pacto impact gave no answer"; }
G0="$(hub_impact | jq -r .gate)"
member_op apply -f - >/dev/null <<YAML
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata:
  name: checkout
  namespace: $APP_NS
  labels: {delivery.pacto.trianalab.io/environment: production}
spec:
  checkIntervalSeconds: 30
  contractRef:
    inline: |
      pactoVersion: '2.0'
      service: {name: checkout, version: 1.0.0, owner: {team: shop, dri: d, contacts: [{type: email, value: a@e.com, purpose: escalation}]}}
      workload: service
      state: {type: stateless, persistence: {scope: local, durability: ephemeral}, dataCriticality: low}
      dependencies: [{name: payments, ref: "oci://$REG/acme/payments", required: true, compatibility: "^1.0.0"}]
  target: {workloadRef: {name: checkout, kind: Deployment}}
YAML
checkout_reconciled() { member_op get pacto checkout -o jsonpath='{.status.dependencies[0].name}' 2>/dev/null | grep -qx payments; }
eventually 40 checkout_reconciled || fail "the member never reconciled checkout's dependency on payments"
republish
# The hub reads nothing the pending member published and, with no member ever
# approved, counts no unread cluster: the answer is the one before checkout.
impact_is '(has("unreadClusters") | not) and (any(.clusters[]?; .id == $u) | not)' || {
  hub_impact >&2; fail "the hub's pacto impact reads or counts the pending member"
}
[ "$(hub_impact | jq -r .gate)" = "$G0" ] || { hub_impact >&2; fail "a pending member's consumer moved the hub's impact gate from $G0"; }
pass "the hub's pacto impact of payments 2.0.0 stays $G0 with checkout pending in the member, and counts no unread cluster"

# sign STEP ARGS... — joinsign as one person with its own Sigstore, then add
# its signer (its issuer and root) to the hub's delivery.identities.signers.
SIGNERS='[]'
sign() {
  local step="$1"; shift
  local root="$WORK/root-$step.json" iss="https://idp-$step.acme.test"
  DOCKER_CONFIG="$PUSH_CFG" "$JOINSIGN" -root "$root" -registry "$REG/acme" -hub "$H_UID" -cluster "$M_UID" -issuer "$iss" "$@" || return 1
  SIGNERS="$(jq -c --arg i "$iss" --rawfile r "$root" '. + [{issuer: $i, usernamePrefix: "oidc:", subjects: "[^@]+@acme\\.test", trustedRoot: $r}]' <<<"$SIGNERS")"
  helm upgrade pacto-operator "$PACTO_CHART" -n "$NS" --kube-context "$CTX_H" --reuse-values \
    --set-json "delivery.identities.signers=$SIGNERS" --wait --timeout 240s >/dev/null || fail "the hub did not take the signer of $iss"
}
alice() { echo -approver oidc:alice@acme.test -email alice@acme.test; }

section "SCENARIO 3: a snapshot signed by another key gets nothing signed"
SAVED="$WORK/key-secret.json"
member_op get secret "$KEY_SECRET" -o json |
  jq 'del(.metadata.resourceVersion, .metadata.uid, .metadata.creationTimestamp, .metadata.managedFields, .metadata.ownerReferences)' > "$SAVED"
before="$(last_push)"
scale_member 0
member_op delete secret "$KEY_SECRET" >/dev/null
scale_member 1
eventually 60 pushed_since "$before" || fail "the member never published under a new key"
[ "$(key_id)" != "$K1" ] || fail "the member kept key sha256:$K1 after its key Secret went"
scale_member 0
# shellcheck disable=SC2046  # alice is two flag pairs
if out="$(sign swap $(alice) -key "sha256:$K1" -public-key "$P1" 2>&1)"; then
  fail "joinsign signed for sha256:$K1 while the snapshot names sha256:$(key_id): $out"
fi
grep -q ClusterKeyMismatch <<<"$out" || fail "the refusal does not say ClusterKeyMismatch: $out"
join_tags() { curl -fsS -u "puller:$PULL_PW" "http://$REG/v2/acme/pacto-targets/tags/list" | jq -r '[.tags[]? | select(startswith("stream-"))] | length'; }
[ "$(join_tags)" = 0 ] || fail "the refused approval left a join stream in $REG/acme/pacto-targets"
member_op delete secret "$KEY_SECRET" >/dev/null
member_op apply -f "$SAVED" >/dev/null
before="$(last_push)"
scale_member 1
eventually 60 pushed_since "$before" || fail "the member never published under its restored key"
[ "$(key_id)" = "$K1" ] || fail "the restored member publishes sha256:$(key_id), not sha256:$K1"
pass "a swapped snapshot is refused with ClusterKeyMismatch and no join stream exists; the member publishes sha256:$K1 again"

section "SCENARIO 4: alice approves and the member's targets appear"
# shellcheck disable=SC2046
sign approve $(alice) -key "sha256:$K1" -public-key "$P1" || fail "joinsign could not approve $M_UID"
eventually 40 row_is '.state == "approved" and .approver == "oidc:alice@acme.test"' || {
  member_row >&2; fail "$M_UID does not read approved by oidc:alice@acme.test"
}
pass "pacto cluster list: $M_UID approved by oidc:alice@acme.test"
has_targets() { [ "$(member_targets)" -gt 0 ]; }
eventually 110 has_targets || fail "pacto fleet lists no target of the approved member"
pass "pacto fleet lists $(member_targets) target(s) of $M_UID under 4-part keys"
M_NAME="$(member_row | jq -r .name)"
# checkout runs only in the member, so its refusal of payments 2.0.0 blocks
# the hub's release, attributed to the member by uid and named by display name.
eventually 40 impact_is '.gate == "blocked" and
    any(.consumers[]; .service == "checkout" and .clusters[0] == $u) and
    any(.clusters[]; .id == $u and .name == $n)' || {
  hub_impact >&2; fail "the hub's pacto impact is not blocked by checkout in $M_NAME ($M_UID)"
}
pass "the hub's pacto impact of payments 2.0.0 is blocked by checkout, which runs in member $M_NAME"

section "SCENARIO 5: a record by bob, who may not approve, is not counted"
sign bob -approver oidc:bob@acme.test -email bob@acme.test -key "sha256:$K1" -public-key "$P1" || fail "joinsign could not write bob's record"
always 5 row_is '.state == "approved" and .approver == "oidc:alice@acme.test"' || fail "bob's record decided: $(member_row)"
pass "bob's newer record is not counted: $M_UID stays approved by alice"

section "SCENARIO 6: a new installation key reads pending"
member_op delete secret "$KEY_SECRET" >/dev/null
republish
K3="$(key_id)"
P3="$(detected | jq -r .installation.snapshotKey)"
[ "$K3" != "$K1" ] || fail "the member kept key sha256:$K1"
eventually 40 row_is '.state == "pending" and (.reason | contains("the key changed"))' || {
  member_row >&2; fail "$M_UID with a new key does not read pending with the key changed"
}
pass "$M_UID reads pending: $(member_row | jq -r .reason)"

section "SCENARIO 7: alice removes the member and its targets go"
# shellcheck disable=SC2046
sign remove $(alice) -remove -key "sha256:$K3" -public-key "$P3" || fail "joinsign could not remove $M_UID"
eventually 40 row_is '.state == "removed"' || { member_row >&2; fail "$M_UID does not read removed"; }
[ "$(member_targets)" = 0 ] || fail "pacto fleet still lists targets of the removed member"
pass "$M_UID reads removed and pacto fleet lists none of its targets"
impact_is '(any(.clusters[]?; .id == $u) | not) and (has("unreadClusters") | not)' || {
  hub_impact >&2; fail "the hub's pacto impact still reads or counts the removed member"
}
pass "the hub's pacto impact neither reads nor counts the removed member"

echo
echo "CLUSTER-JOIN E2E PASS"
