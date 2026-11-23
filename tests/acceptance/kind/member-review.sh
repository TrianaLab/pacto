#!/usr/bin/env bash
# Review people in a member cluster through its outbound channel (#568) on two
# real kind clusters, two identity providers, a TLS ingress and zot.
#
# Unit tests pin the protocol, the reviewer, the exchange and the refusal
# threshold on fakes; only real clusters run the member's dial through a
# TLS-terminating ingress, a SelfSubjectReview with a forwarded token and a
# SubjectAccessReview in the member, with no inbound route to it.
#
#   docker network kind    pacto-e2e-member-kc (Keycloak, issuer A, token
#                          exchange), pacto-e2e-dex-b (Dex, issuer B) and zot,
#                          one scenario CA for both providers and the ingress
#   hub ($CLUSTER)         trusts issuer A (audience pacto-dashboard) and dex-b;
#                          ingress-nginx at hub.pacto-member.test with a 60s
#                          read timeout in front of the operator-managed
#                          dashboard (sign-in with issuer A, the exchange
#                          client), delivery for the approver signers
#   member ($MEMBER)       trusts issuer A for audience pacto-member-<uid> only;
#                          the operator with hub.url and hub.caConfigMap, and
#                          a default-deny ingress NetworkPolicy
#
# joinsign (tests/acceptance/kind/joinsign) plays alice approving the member
# and approving a group mapping keylessly, against a private Sigstore per run.
#
# Claims:
#   1. nothing reaches the member operator from inside its cluster once the
#      NetworkPolicy is applied, and the release exposes no NodePort or
#      hostPort;
#   2. the member links to the hub (live) and alice, signed in with issuer A,
#      sees the member's payments and not its ops (shared issuer);
#   3. the token exchanged for the member authenticates on the member
#      apiserver and gets 401 from the hub apiserver;
#   4. bob and carol, signed in with dex-b, do not see the member (hidden,
#      partial) and CLUSTER_ISSUER_DIFFERS names it; after alice's group
#      mapping bob sees the member's ops and not payments (group mapping);
#   5. an idle link outlives the ingress's 60s read timeout; with the member
#      operator stopped the hub answers partial with the member hidden;
#   6. no token appears in the hub dashboard, ingress or member operator logs;
#   7. the member operator may not impersonate.
# shellcheck disable=SC2016  # the jq filters name jq variables such as $u
set -euo pipefail
# shellcheck source=tests/acceptance/kind/lib.sh
source "$(dirname "$0")/lib.sh"
# shellcheck source=tests/acceptance/kind/delivery-lib.sh
source "$(dirname "$0")/delivery-lib.sh"

CLUSTER="${KIND_CLUSTER:-pacto-member-hub}"
MEMBER=pacto-member-edge
NS=pacto-member # the release namespace in both clusters (read by dump_diag)
KC=pacto-e2e-member-kc
DEXB=pacto-e2e-dex-b
ZOT=pacto-member-zot
HUB_HOST=hub.pacto-member.test
KC_ISSUER="https://$KC:8443/realms/pacto"
DEXB_ISSUER="https://$DEXB:5557/dex"
DASH_SECRET=dashboard-secret
DETECTED=pacto-operator-detected
KC_IMG="quay.io/keycloak/keycloak@sha256:9409c59bdfb65dbffa20b11e6f18b8abb9281d480c7ca402f51ed3d5977e6007"
DEX_IMG="ghcr.io/dexidp/dex@sha256:0ae7ade1bdf1c83f7f7ccb1935912ac4c116e3e8ecc7e7503b30bab77e7c896c"
PROBE_IMG=python:3.13-alpine@sha256:bf12ecc0bea3f171d55145dd02c78b5eeb09f30ee2ac9cc2b4c9e4e8f5e30741
INGRESS_NGINX=controller-v1.12.1
PUSH_PW="push-$RANDOM$RANDOM"
PULL_PW="pull-$RANDOM$RANDOM"
VER="$(release_version kubernetes)"
OP_REPO="localhost:5001/pacto-operator/pacto-controller"
OP_IMG="${OP_REPO}:${VER}"
DL_REPO="localhost:5001/pacto-delivery"
DL_IMG="${DL_REPO}:${VER}"
DASH_IMG="localhost:5001/pacto-dashboard:$(release_version core)"
FIXTURES="$(dirname "$0")/fixtures/member"

for t in jq helm docker curl base64 openssl; do command -v "$t" >/dev/null 2>&1 || fail "$t is required"; done
command -v htpasswd >/dev/null 2>&1 || infra "install htpasswd" sudo apt-get install -y -qq apache2-utils

WORK="$(mktemp -d)"; chmod 755 "$WORK" # Keycloak runs as uid 1000 and reads its import from here
teardown() {
  helm uninstall pacto-operator -n "$NS" --kube-context "kind-$CLUSTER" --wait >/dev/null 2>&1 || true
  kubectl --context "kind-$CLUSTER" delete ns "$NS" pacto-delivery ingress-nginx --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl --context "kind-$CLUSTER" delete clusterrole,clusterrolebinding pacto-cluster-approver --ignore-not-found >/dev/null 2>&1 || true
  docker rm -f "$KC" "$DEXB" "$ZOT" >/dev/null 2>&1 || true
  kind delete cluster --name "$MEMBER" >/dev/null 2>&1 || true
}
# shellcheck disable=SC2154  # rc is assigned by rc=$? inside the trap body
trap 'rc=$?; [ $rc -ne 0 ] && { dump_diag "$NS"; docker logs --tail 60 "$KC" 2>&1 | sed "s/^/kc: /"; docker logs --tail 60 "$DEXB" 2>&1 | sed "s/^/dex-b: /"; }; keep_or_teardown "$NS" "$CLUSTER" teardown; exit $rc' EXIT

section "the scenario CA, the identity providers and zot on the kind network"
docker network inspect kind >/dev/null 2>&1 || infra "create the kind docker network" docker network create kind
openssl req -x509 -newkey rsa:2048 -nodes -days 2 -keyout "$WORK/ca.key" -out "$WORK/ca.crt" -subj /CN=pacto-member-e2e-ca >/dev/null 2>&1 \
  || fail "openssl could not make the scenario CA"
# leaf NAME — a certificate for NAME signed by the scenario CA, in $WORK/NAME.{crt,key}
leaf() {
  openssl req -newkey rsa:2048 -nodes -keyout "$WORK/$1.key" -out "$WORK/$1.csr" -subj "/CN=$1" >/dev/null 2>&1
  openssl x509 -req -in "$WORK/$1.csr" -CA "$WORK/ca.crt" -CAkey "$WORK/ca.key" -CAcreateserial -days 2 \
    -extfile <(printf 'subjectAltName=DNS:%s' "$1") -out "$WORK/$1.crt" >/dev/null 2>&1 || fail "openssl could not sign $1"
  chmod 644 "$WORK/$1.key"
}
for n in "$KC" "$DEXB" "$HUB_HOST"; do leaf "$n"; done
mkdir -p "$WORK/kc" "$WORK/dex"
cp "$FIXTURES/realm.json" "$WORK/$KC.crt" "$WORK/$KC.key" "$WORK/kc/"
cp "$FIXTURES/dex-b.yaml" "$WORK/dex/"
cp "$WORK/$DEXB.crt" "$WORK/dex/tls.crt"; cp "$WORK/$DEXB.key" "$WORK/dex/tls.key"
chmod -R a+rX "$WORK/kc" "$WORK/dex"
docker rm -f "$KC" "$DEXB" >/dev/null 2>&1 || true
docker run -d --name "$KC" --network kind -e KC_BOOTSTRAP_ADMIN_USERNAME=admin -e KC_BOOTSTRAP_ADMIN_PASSWORD=admin \
  -v "$WORK/kc:/opt/keycloak/data/import:ro" "$KC_IMG" start-dev --import-realm --http-port=8080 --https-port=8443 \
  --https-certificate-file="/opt/keycloak/data/import/$KC.crt" --https-certificate-key-file="/opt/keycloak/data/import/$KC.key" \
  --hostname="https://$KC:8443" >/dev/null
docker run -d --name "$DEXB" --network kind -v "$WORK/dex:/etc/dex:ro" "$DEX_IMG" dex serve /etc/dex/dex-b.yaml >/dev/null
ip_of() { docker inspect -f '{{(index .NetworkSettings.Networks "kind").IPAddress}}' "$1"; }
KC_IP="$(ip_of "$KC")"; DEXB_IP="$(ip_of "$DEXB")"
# kcurl ARGS... — curl from the runner to a provider by its name, trusting the scenario CA.
kcurl() { curl -sf --cacert "$WORK/ca.crt" --resolve "$KC:8443:$KC_IP" --resolve "$DEXB:5557:$DEXB_IP" "$@"; }
pull_pinned "$PACTO_REGISTRY_IMAGE"
docker rm -f "$ZOT" >/dev/null 2>&1 || true
mkdir -p "$WORK/zot"
{ htpasswd -Bbn pusher "$PUSH_PW"; htpasswd -Bbn puller "$PULL_PW"; } | grep . > "$WORK/zot/htpasswd"
cat > "$WORK/zot/config.json" <<'JSON'
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
chmod -R a+rX "$WORK/zot"
docker run -d --name "$ZOT" --network kind -v "$WORK/zot:/etc/zot:ro" "${PACTO_REGISTRY_IMAGE%@*}" serve /etc/zot/config.json >/dev/null
REG="$(ip_of "$ZOT"):5000"
PUSH_CFG="$(mktemp -d)"
printf '{"auths":{"%s":{"auth":"%s"}}}' "$REG" "$(printf 'pusher:%s' "$PUSH_PW" | base64 | tr -d '\n')" > "$PUSH_CFG/config.json"
export PACTO_INSECURE_REGISTRIES="$REG"
pass "Keycloak ($KC_IP), dex-b ($DEXB_IP) and zot ($REG) run on the kind network"

# authn FILE AUDIENCE_A — the AuthenticationConfiguration of a cluster that
# trusts issuer A for AUDIENCE_A and, with a third argument, dex-b for
# pacto-dashboard. The apiserver reloads the file when it changes.
authn() {
  local ca; ca="$(sed 's/^/      /' "$WORK/ca.crt")"
  {
    echo "apiVersion: apiserver.config.k8s.io/v1"
    echo "kind: AuthenticationConfiguration"
    echo "jwt:"
    local iss aud
    for iss in "$KC_ISSUER" ${3:+"$DEXB_ISSUER"}; do
      aud="$2"; [ "$iss" = "$DEXB_ISSUER" ] && aud=pacto-dashboard
      cat <<YAML
- issuer:
    url: $iss
    audiences: [$aud]
    certificateAuthority: |
$ca
  claimMappings:
    username: {expression: "'$iss#' + claims.sub"}
    groups: {claim: groups, prefix: ""}
YAML
    done
  } > "$1.tmp"
  mv "$1.tmp" "$1"
  chmod 644 "$1"
}
# kind_config DIR [INGRESS] — a kind config whose apiserver reads DIR/authn.yaml.
kind_config() {
  local labels=""
  [ -n "${2:-}" ] && labels='    nodeRegistration: {kubeletExtraArgs: {node-labels: "ingress-ready=true"}}'
  cat > "$1/kind.yaml" <<YAML
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  extraMounts:
  - {hostPath: $1, containerPath: /etc/kubernetes/pacto-identity, readOnly: true}
  kubeadmConfigPatches:
  - |
    kind: ClusterConfiguration
    apiServer:
      extraArgs:
        authentication-config: /etc/kubernetes/pacto-identity/authn.yaml
      extraVolumes:
      - {name: pacto-identity, hostPath: /etc/kubernetes/pacto-identity, mountPath: /etc/kubernetes/pacto-identity, readOnly: true, pathType: Directory}
  - |
    kind: InitConfiguration
$labels
YAML
  echo "$1/kind.yaml"
}

section "two kind clusters trusting their identity providers, the images and the pacto CLI"
mkdir -p "$WORK/hub-authn" "$WORK/member-authn"; chmod 755 "$WORK/hub-authn" "$WORK/member-authn"
authn "$WORK/hub-authn/authn.yaml" pacto-dashboard dex-b
# The member's UID is known only once it exists: it starts trusting a
# placeholder audience, rewritten below.
authn "$WORK/member-authn/authn.yaml" pacto-member-pending
cluster_start_bg "$(kind_config "$WORK/hub-authn" ingress)"
load_built_images "$OP_IMG" "$DL_IMG" "$DASH_IMG" || build_images
JOINSIGN="$WORK/joinsign"
( cd "$_PACTO_ROOT" && CGO_ENABLED=0 go build -o "$JOINSIGN" ./tests/acceptance/kind/joinsign )
ensure_cluster "$(kind_config "$WORK/hub-authn" ingress)"
KC_H="$KUBECONFIG"
kind get clusters | grep -qx "$MEMBER" || infra "create kind cluster $MEMBER" \
  kind create cluster --name "$MEMBER" --wait 90s --config "$(kind_config "$WORK/member-authn")"
KC_M="$(mktemp)"
kind get kubeconfig --name "$MEMBER" > "$KC_M"
KUBECONFIG="$(mktemp)"; export KUBECONFIG
KUBECONFIG="$KC_H:$KC_M" kubectl config view --flatten > "$KUBECONFIG"
CTX_H="kind-$CLUSTER"
CTX_M="kind-$MEMBER"
kubectl config use-context "$CTX_H" >/dev/null
infra "load the images into the hub" load_images "$OP_IMG" "$DL_IMG" "$DASH_IMG"
pull_pinned "$PROBE_IMG"
CLUSTER="$MEMBER" infra "load the images into the member" load_images "$OP_IMG" "${PROBE_IMG%@*}"
HUB_IP="$(ip_of "${CLUSTER}-control-plane")"
H_UID="$(kubectl --context "$CTX_H" get ns kube-system -o jsonpath='{.metadata.uid}')"
M_UID="$(kubectl --context "$CTX_M" get ns kube-system -o jsonpath='{.metadata.uid}')"
[ -n "$H_UID" ] && [ -n "$M_UID" ] && [ "$H_UID" != "$M_UID" ] || fail "the two clusters do not report two kube-system UIDs: '$H_UID' '$M_UID'"
pass "hub $H_UID ($HUB_IP) and member $M_UID run"

# coredns_hosts CONTEXT LINE... — pods of that cluster resolve each "IP NAME".
coredns_hosts() {
  local ctx="$1" cf; shift
  cf="$(kubectl --context "$ctx" -n kube-system get configmap coredns -o jsonpath='{.data.Corefile}')"
  cf="$(awk -v h="$(printf '%s;' "$@")" '/^ *ready/ && !done {
      print "    hosts {"; n = split(h, a, ";"); for (i = 1; i < n; i++) print "       " a[i]
      print "       fallthrough"; print "    }"; done = 1 } { print }' <<<"$cf")"
  kubectl --context "$ctx" -n kube-system create configmap coredns --from-literal=Corefile="$cf" --dry-run=client -o yaml |
    kubectl --context "$ctx" apply -f - >/dev/null
  kubectl --context "$ctx" -n kube-system rollout restart deploy/coredns >/dev/null
  kubectl --context "$ctx" -n kube-system rollout status deploy/coredns --timeout=120s >/dev/null || fail "CoreDNS of $ctx did not restart"
}
coredns_hosts "$CTX_H" "$KC_IP $KC" "$DEXB_IP $DEXB"
coredns_hosts "$CTX_M" "$HUB_IP $HUB_HOST"

section "issuer A exchanges tokens for this member and the member apiserver accepts only that audience"
up() { kcurl -o /dev/null "$1/.well-known/openid-configuration"; }
eventually 100 up "$KC_ISSUER" || fail "Keycloak did not start"
eventually 40 up "$DEXB_ISSUER" || fail "dex-b did not start"
kcadm() { docker exec -i "$KC" /opt/keycloak/bin/kcadm.sh "$@"; }
kcadm config credentials --server http://localhost:8080 --realm master --user admin --password admin >/dev/null
AUD="pacto-member-$M_UID"
kcadm create clients -r pacto -s clientId="$AUD" -s enabled=true -s publicClient=false -s standardFlowEnabled=false >/dev/null
kcadm create client-scopes -r pacto -f - >/dev/null <<JSON
{"name": "member-audience", "protocol": "openid-connect", "attributes": {"include.in.token.scope": "false"},
 "protocolMappers": [{"name": "member", "protocol": "openid-connect", "protocolMapper": "oidc-audience-mapper",
   "config": {"included.client.audience": "$AUD", "access.token.claim": "true", "id.token.claim": "false"}}]}
JSON
SCOPE_ID="$(kcadm get client-scopes -r pacto | jq -r '.[] | select(.name == "member-audience") | .id')"
DASH_ID="$(kcadm get clients -r pacto -q clientId=pacto-dashboard | jq -r '.[0].id')"
kcadm update "clients/$DASH_ID/default-client-scopes/$SCOPE_ID" -r pacto >/dev/null
authn "$WORK/member-authn/authn.yaml" "$AUD"

kc_token() { kcurl "$KC_ISSUER/protocol/openid-connect/token" "$@" | jq -r .access_token; }
alice_token() { kc_token -d grant_type=password -d client_id=pacto-login -d username=alice -d password=password -d scope=openid; }
# member_exchange TOKEN — the exchange the hub makes for the member.
member_exchange() {
  kc_token -u "pacto-dashboard:$DASH_SECRET" -d grant_type=urn:ietf:params:oauth:grant-type:token-exchange -d subject_token="$1" \
    -d subject_token_type=urn:ietf:params:oauth:token-type:access_token \
    -d requested_token_type=urn:ietf:params:oauth:token-type:access_token -d audience="$AUD"
}
dex_token() { kcurl "$DEXB_ISSUER/token" -u "pacto-dashboard:$DASH_SECRET" -d grant_type=password -d scope='openid profile groups' \
  -d username="$1@example.com" -d password=password | jq -r .id_token; }
# as_token CONTEXT TOKEN ARGS... — kubectl against CONTEXT's apiserver with TOKEN as the only credential.
as_token() {
  local ctx="$1" tok="$2" f; shift 2
  f="$(mktemp "$WORK/token-kubeconfig.XXXX")"
  cat > "$f" <<YAML
apiVersion: v1
kind: Config
clusters: [{name: c, cluster: {server: "$(kubectl config view --raw -o jsonpath="{.clusters[?(@.name=='$ctx')].cluster.server}")", certificate-authority-data: "$(kubectl config view --raw -o jsonpath="{.clusters[?(@.name=='$ctx')].cluster.certificate-authority-data}")"}}]
users: [{name: u, user: {token: "$tok"}}]
contexts: [{name: c, context: {cluster: c, user: u}}]
current-context: c
YAML
  KUBECONFIG="$f" kubectl "$@"
}
ALICE="$(alice_token)"
[ -n "$ALICE" ] && [ "$ALICE" != null ] || fail "alice could not take a token from issuer A"
FORWARD="$(member_exchange "$ALICE")"
[ -n "$FORWARD" ] && [ "$FORWARD" != null ] || fail "issuer A did not exchange alice's token for $AUD"
member_accepts() { as_token "$CTX_M" "$FORWARD" auth whoami -o json 2>/dev/null | jq -e --arg i "$KC_ISSUER#" '.status.userInfo.username | startswith($i)' >/dev/null; }
eventually 40 member_accepts || fail "the member apiserver does not accept the token exchanged for $AUD: $(as_token "$CTX_M" "$FORWARD" auth whoami 2>&1)"
out="$(as_token "$CTX_H" "$FORWARD" auth whoami 2>&1 || true)"
grep -qi unauthorized <<<"$out" || fail "the hub apiserver did not refuse the token exchanged for the member with 401: $out"
hub_accepts() { as_token "$CTX_H" "$1" auth whoami -o json >/dev/null 2>&1; }
eventually 40 hub_accepts "$ALICE" || fail "the hub apiserver does not accept alice's issuer-A token"
eventually 40 hub_accepts "$(dex_token bob)" || fail "the hub apiserver does not accept bob's dex-b token"
pass "the token exchanged for $AUD authenticates on the member apiserver and gets 401 from the hub apiserver"

section "ingress-nginx on the hub with the scenario certificate and a 60s read timeout"
infra "install ingress-nginx $INGRESS_NGINX" kubectl --context "$CTX_H" apply -f \
  "https://raw.githubusercontent.com/kubernetes/ingress-nginx/$INGRESS_NGINX/deploy/static/provider/kind/deploy.yaml" >/dev/null
kubectl --context "$CTX_H" -n ingress-nginx patch configmap ingress-nginx-controller --type merge \
  -p '{"data":{"proxy-read-timeout":"60","proxy-send-timeout":"60"}}' >/dev/null
infra "wait for ingress-nginx" kubectl --context "$CTX_H" -n ingress-nginx wait --for=condition=available deploy/ingress-nginx-controller --timeout=180s
pass "ingress-nginx answers on $HUB_IP:443 (proxy-read-timeout 60s)"

section "the hub: operator, delivery and the dashboard behind the ingress"
SETS=(--set global.imageRegistry= --set image.repository="$OP_REPO" --set image.tag="$VER" --set image.pullPolicy=Never
      --set fullnameOverride=pacto-operator --set controller.stabilizationWindow=5s
      --set delivery.image.repository="$DL_REPO" --set delivery.image.tag="$VER" --set delivery.image.pullPolicy=Never
      --set registry="$REG/acme" --set registryPullSecret="$NS/zot-push"
      --set "insecureRegistries[0]=$REG" --set "delivery.insecureRegistries[0]=$REG")
for ctx in "$CTX_H" "$CTX_M"; do
  kubectl --context "$ctx" create ns "$NS" --dry-run=client -o yaml | kubectl --context "$ctx" apply -f - >/dev/null
  kubectl --context "$ctx" -n "$NS" create secret docker-registry zot-push --docker-server="$REG" --docker-username=pusher \
    --docker-password="$PUSH_PW" --dry-run=client -o yaml | kubectl --context "$ctx" apply -f - >/dev/null
  kubectl --context "$ctx" -n "$NS" create configmap scenario-ca --from-file=ca.crt="$WORK/ca.crt" --dry-run=client -o yaml |
    kubectl --context "$ctx" apply -f - >/dev/null
done
kubectl --context "$CTX_H" -n "$NS" create secret docker-registry zot-pull --docker-server="$REG" --docker-username=puller \
  --docker-password="$PULL_PW" --dry-run=client -o yaml | kubectl --context "$CTX_H" apply -f - >/dev/null
kubectl --context "$CTX_H" -n "$NS" create secret generic hub-oidc --from-literal=client-secret="$DASH_SECRET" \
  --dry-run=client -o yaml | kubectl --context "$CTX_H" apply -f - >/dev/null
kubectl --context "$CTX_H" -n "$NS" create secret tls hub-tls --cert="$WORK/$HUB_HOST.crt" --key="$WORK/$HUB_HOST.key" \
  --dry-run=client -o yaml | kubectl --context "$CTX_H" apply -f - >/dev/null
HUB_SETS=("${SETS[@]}" --set delivery.enabled=true --set 'dashboard.ociSecrets[0]=zot-pull'
  --set dashboard.auth.oidc.issuerURL="$KC_ISSUER" --set dashboard.auth.oidc.clientID=pacto-dashboard
  --set dashboard.auth.oidc.clientSecretRef.name=hub-oidc --set dashboard.auth.oidc.redirectURL="https://$HUB_HOST/auth/callback"
  --set dashboard.auth.oidc.caBundle.configMap=scenario-ca)
helm install pacto-operator "$PACTO_CHART" -n "$NS" --kube-context "$CTX_H" "${HUB_SETS[@]}" --wait --timeout 240s >/dev/null
wait_managed_ready pacto-dashboard >/dev/null || fail "the hub dashboard Deployment is not Ready"
# The controller turns available before its admission webhook answers, so the
# first apply can be refused with "connection refused": retry it until it lands.
INGRESS_YAML="$(cat <<YAML
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: pacto-hub
  namespace: $NS
  annotations:
    nginx.ingress.kubernetes.io/proxy-read-timeout: "60"
    nginx.ingress.kubernetes.io/proxy-send-timeout: "60"
spec:
  ingressClassName: nginx
  tls: [{hosts: [$HUB_HOST], secretName: hub-tls}]
  rules:
  - host: $HUB_HOST
    http: {paths: [{path: /, pathType: Prefix, backend: {service: {name: pacto-dashboard, port: {number: 3000}}}}]}
YAML
)"
apply_ingress() { kubectl --context "$CTX_H" apply -f - >/dev/null <<<"$INGRESS_YAML"; }
eventually 20 apply_ingress || fail "the hub Ingress was not admitted"
HUB="https://$HUB_HOST"
# hcurl ARGS... — the runner reaches the hub through the ingress, as a browser would.
hcurl() { curl -s --cacert "$WORK/ca.crt" --resolve "$HUB_HOST:443:$HUB_IP" "$@"; }
hub_up() { [ "$(hcurl -o /dev/null -w '%{http_code}' "$HUB/")" != 000 ] && [ "$(hcurl -o /dev/null -w '%{http_code}' "$HUB/")" -lt 500 ]; }
eventually 60 hub_up || fail "the hub dashboard does not answer at $HUB through the ingress"
kubectl --context "$CTX_H" create clusterrole pacto-cluster-approver --verb=approve --resource=clusterjoins.pacto.trianalab.io \
  --dry-run=client -o yaml | kubectl --context "$CTX_H" apply -f - >/dev/null
kubectl --context "$CTX_H" create clusterrolebinding pacto-cluster-approver --clusterrole=pacto-cluster-approver \
  --user=oidc:alice@acme.test --dry-run=client -o yaml | kubectl --context "$CTX_H" apply -f - >/dev/null
pass "the hub dashboard answers at $HUB with TLS from the scenario CA"

section "the member: Pactos in payments and ops, the operator with hub.url"
for ns in payments ops; do
  kubectl --context "$CTX_M" create ns "$ns" --dry-run=client -o yaml | kubectl --context "$CTX_M" apply -f - >/dev/null
done
helm install pacto-operator "$PACTO_CHART" -n "$NS" --kube-context "$CTX_M" "${SETS[@]}" --set dashboard.enabled=false \
  --set hub.url="$HUB" --set hub.caConfigMap=scenario-ca --wait --timeout 180s >/dev/null
for ns in payments ops; do
  kubectl --context "$CTX_M" apply -f - >/dev/null <<YAML
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: {name: $ns-svc, namespace: $ns}
spec:
  checkIntervalSeconds: 30
  contractRef:
    inline: |
      pactoVersion: '2.0'
      service: {name: $ns-svc, version: 1.0.0, owner: {team: $ns, dri: d, contacts: [{type: email, value: a@e.com, purpose: escalation}]}}
      workload: service
      state: {type: stateless, persistence: {scope: local, durability: ephemeral}, dataCriticality: low}
  target: {workloadRef: {name: $ns-svc, kind: Deployment}}
YAML
done
# The member's readers: team-payments lists Pactos in payments only, the
# mapped group ops-mapped in ops only.
kubectl --context "$CTX_M" -n payments create role pacto-reader --verb=list --resource=pactos.pacto.trianalab.io --dry-run=client -o yaml | kubectl --context "$CTX_M" apply -f - >/dev/null
kubectl --context "$CTX_M" -n payments create rolebinding pacto-reader --role=pacto-reader --group=team-payments --dry-run=client -o yaml | kubectl --context "$CTX_M" apply -f - >/dev/null
kubectl --context "$CTX_M" -n ops create role pacto-reader --verb=list --resource=pactos.pacto.trianalab.io --dry-run=client -o yaml | kubectl --context "$CTX_M" apply -f - >/dev/null
kubectl --context "$CTX_M" -n ops create rolebinding pacto-reader --role=pacto-reader --group=ops-mapped --dry-run=client -o yaml | kubectl --context "$CTX_M" apply -f - >/dev/null
detected() { kubectl --context "$CTX_M" -n "$NS" get configmap "$DETECTED" -o jsonpath='{.data.detected\.json}' 2>/dev/null; }
published() { detected | jq -e --arg u "$M_UID" '.cluster == $u and (.installation.lastPush.ref // "" | contains("pacto-targets:cluster-"))' >/dev/null; }
kubectl --context "$CTX_M" -n "$NS" rollout restart deploy/pacto-operator >/dev/null
kubectl --context "$CTX_M" -n "$NS" rollout status deploy/pacto-operator --timeout=180s >/dev/null || fail "the member operator did not roll out"
eventually 60 published || fail "the member never published its snapshot: $(detected | jq -c .installation)"
K1="$(detected | jq -r .installation.snapshotKeyID)"
P1="$(detected | jq -r .installation.snapshotKey)"
pass "the member published cluster-$M_UID signed by sha256:$K1"

section "CLAIM 1: nothing reaches the member operator from inside its cluster"
kubectl --context "$CTX_M" create ns probe --dry-run=client -o yaml | kubectl --context "$CTX_M" apply -f - >/dev/null
kubectl --context "$CTX_M" -n probe run probe --image="${PROBE_IMG%@*}" --image-pull-policy=Never --restart=Never -- sleep 3600 >/dev/null
kubectl --context "$CTX_M" -n probe wait --for=condition=Ready pod/probe --timeout=120s >/dev/null || fail "the probe pod did not start"
reaches() {
  kubectl --context "$CTX_M" -n probe exec probe -- python3 -c \
    "import socket; socket.create_connection(('pacto-operator-metrics.$NS.svc', 8443), 3)" >/dev/null 2>&1
}
eventually 20 reaches || fail "the probe never reached the member operator's metrics Service before the NetworkPolicy"
# Every pod-to-pod connection is refused; only the node itself (the
# kubelet's probes) may connect, so the operator keeps running.
kubectl --context "$CTX_M" apply -f - >/dev/null <<YAML
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: default-deny-ingress, namespace: $NS}
spec:
  podSelector: {}
  policyTypes: [Ingress]
  ingress: [{from: [{ipBlock: {cidr: $(ip_of "${MEMBER}-control-plane")/32}}]}]
YAML
blocked() { ! reaches; }
eventually 20 blocked || fail "the probe still reaches the member operator after a default-deny ingress NetworkPolicy"
always 3 blocked || fail "the NetworkPolicy does not hold"
exposed="$(kubectl --context "$CTX_M" -n "$NS" get svc,pods -o json |
  jq -c '[.items[] | (.spec.ports[]?.nodePort // empty), (.spec.containers[]?.ports[]?.hostPort // empty)]')"
[ "$exposed" = '[]' ] || fail "the member release exposes node or host ports: $exposed"
pass "the probe reached the operator before the NetworkPolicy (deny every pod) and not after; the release exposes no NodePort or hostPort"

# sign STEP ARGS... — joinsign as alice with its own Sigstore, then add its
# signer (its issuer and root) to the hub's delivery.identities.signers.
SIGNERS='[]'
sign() {
  local step="$1"; shift
  local root="$WORK/root-$step.json" iss="https://idp-$step.acme.test"
  DOCKER_CONFIG="$PUSH_CFG" "$JOINSIGN" -root "$root" -registry "$REG/acme" -hub "$H_UID" -cluster "$M_UID" -issuer "$iss" \
    -approver oidc:alice@acme.test -email alice@acme.test "$@" || return 1
  SIGNERS="$(jq -c --arg i "$iss" --rawfile r "$root" '. + [{issuer: $i, usernamePrefix: "oidc:", subjects: "[^@]+@acme\\.test", trustedRoot: $r}]' <<<"$SIGNERS")"
  helm upgrade pacto-operator "$PACTO_CHART" -n "$NS" --kube-context "$CTX_H" --reuse-values \
    --set-json "delivery.identities.signers=$SIGNERS" --wait --timeout 240s >/dev/null || fail "the hub did not take the signer of $iss"
  wait_managed_ready pacto-dashboard >/dev/null || fail "the hub dashboard is not Ready after the signer of $iss"
}
sign approve -key "sha256:$K1" -public-key "$P1" || fail "joinsign could not approve $M_UID"

# login TOKEN — a hub session for TOKEN, signed in as the sign-in page does.
login() {
  hcurl -D - -o "$WORK/login-body" -X POST -H "Origin: $HUB" -H 'Content-Type: application/json' \
    -d "{\"token\":\"$1\"}" "$HUB/auth/token" | tr -d '\r' | sed -n 's/^[Ss]et-[Cc]ookie: __Host-pacto-session=\([^;]*\).*/\1/p'
}
# targets SESSION — the targets a session sees; prints the HTTP status, the body in $WORK/targets.json.
targets() { hcurl -o "$WORK/targets.json" -w '%{http_code}' -H "Cookie: __Host-pacto-session=$1" "$HUB/api/fleet/entities?kinds=target"; }
# view SESSION JQ — the session's targets answer is 200 and matches JQ ($u is the member UID).
view() { [ "$(targets "$1")" = 200 ] && jq -e --arg u "$M_UID" "$2" "$WORK/targets.json" >/dev/null; }
show() { jq -c --arg u "$M_UID" '{keys: [.entities[]?.key], completeness: .meta.completeness, member: [.meta.clusters[]? | select(.id == $u)],
  limitations: [.meta.limitations[]? | select(.code == "CLUSTER_HIDDEN")]}' "$WORK/targets.json" >&2 || cat "$WORK/targets.json" >&2; }
MEMBER_KEYS='[.entities[].key | select(startswith($u + "/"))]'
LIVE='any(.meta.clusters[]; .id == $u and .link.channel == "live")'
HIDDEN='(any(.meta.clusters[]; .id == $u and (.access // "") != "") and .meta.completeness == "partial" and
  any(.meta.limitations[]; .code == "CLUSTER_HIDDEN"))'

section "CLAIM 2: the member links out and alice is reviewed there under the shared issuer"
SESSION_A="$(login "$ALICE")"
[ -n "$SESSION_A" ] || fail "alice's issuer-A token did not sign in to the hub: $(cat "$WORK/login-body")"
ALICE_VIEW="$LIVE and any(.meta.clusters[]; .id == \$u and any(.link.issuers[]?; .model == \"shared-issuer\")) and
  ($MEMBER_KEYS | length > 0) and all(${MEMBER_KEYS}[]; startswith(\$u + \"/payments/\"))"
eventually 60 view "$SESSION_A" "$ALICE_VIEW" || { show; fail "alice does not see the live member's payments alone under the shared issuer"; }
pass "the member is live under shared-issuer; alice sees $(jq -c --arg u "$M_UID" "$MEMBER_KEYS" "$WORK/targets.json") and nothing of ops"

section "CLAIM 4: dex-b people are hidden until alice approves a group mapping"
BOB="$(dex_token bob)"; CAROL="$(dex_token carol)"
SESSION_B="$(login "$BOB")"; SESSION_C="$(login "$CAROL")"
[ -n "$SESSION_B" ] && [ -n "$SESSION_C" ] || fail "bob or carol could not sign in to the hub with dex-b: $(cat "$WORK/login-body")"
view "$SESSION_B" "$HIDDEN and ($MEMBER_KEYS | length == 0)" || { show; fail "bob's first answer shows the member or is not partial"; }
view "$SESSION_C" "$HIDDEN and ($MEMBER_KEYS | length == 0)" || { show; fail "carol's answer shows the member or is not partial"; }
attention() { hcurl -H "Cookie: __Host-pacto-session=$SESSION_A" "$HUB/api/fleet/attention?category=cluster"; }
differs() { attention | jq -e --arg u "$M_UID" '.items | any(.code == "CLUSTER_ISSUER_DIFFERS" and (.nextStep // "" | contains($u)))' >/dev/null; }
refuses() { view "$SESSION_B" "$HIDDEN" && view "$SESSION_C" "$HIDDEN" && differs; }
eventually 40 refuses || { attention >&2; fail "after bob and carol were refused, CLUSTER_ISSUER_DIFFERS does not name the member"; }
pass "bob and carol see the member hidden (partial) and CLUSTER_ISSUER_DIFFERS names it with the map-groups command"
sign map -map-issuer "$DEXB_ISSUER" -map ops-b=ops-mapped || fail "joinsign could not write alice's group mapping"
BOB_VIEW="any(.meta.clusters[]; .id == \$u and any(.link.issuers[]?; .issuer == \"$DEXB_ISSUER\" and .model == \"group-mapping\")) and
  ($MEMBER_KEYS | length > 0) and all(${MEMBER_KEYS}[]; startswith(\$u + \"/ops/\"))"
BOB="$(dex_token bob)"; SESSION_B="$(login "$BOB")"
eventually 60 view "$SESSION_B" "$BOB_VIEW" || { show; fail "bob does not see the member's ops alone under the group mapping"; }
pass "after alice's mapping bob sees $(jq -c --arg u "$M_UID" "$MEMBER_KEYS" "$WORK/targets.json") under group-mapping"

section "CLAIM 5: an idle link outlives the ingress timeout; a stopped member is hidden"
sleep 90
ALICE="$(alice_token)"; SESSION_A="$(login "$ALICE")"
view "$SESSION_A" "$LIVE" || { show; fail "the member link did not outlive 90s idle behind a 60s read timeout"; }
pass "the link is still live after 90s idle (ingress proxy-read-timeout 60s)"
kubectl --context "$CTX_M" -n "$NS" scale deploy/pacto-operator --replicas=0 >/dev/null
hidden_ok() { view "$SESSION_A" "$HIDDEN and ($MEMBER_KEYS | length == 0)"; }
eventually 5 hidden_ok || { show; fail "within 15s of the member stopping the hub does not answer partial with the member hidden"; }
pass "the hub answers 200, partial, with the member hidden once its operator stopped"

section "CLAIM 6: no token in the logs"
LOGS="$WORK/logs.txt"
{
  kubectl --context "$CTX_H" -n "$NS" logs deploy/pacto-dashboard --all-containers 2>&1 || true
  kubectl --context "$CTX_H" -n ingress-nginx logs deploy/ingress-nginx-controller 2>&1 || true
  kubectl --context "$CTX_M" -n "$NS" logs deploy/pacto-operator --all-containers --previous 2>&1 || true
  kubectl --context "$CTX_M" -n "$NS" logs -l app.kubernetes.io/instance=pacto-operator --all-containers --tail=-1 2>&1 || true
} > "$LOGS"
[ -s "$LOGS" ] || fail "no logs were read"
for tok in "$ALICE" "$FORWARD" "$BOB" "$CAROL"; do
  ! grep -qF "$tok" "$LOGS" || fail "a token appears in the hub dashboard, ingress or member operator logs"
  ! grep -qF "${tok##*.}" "$LOGS" || fail "a token signature appears in the logs"
done
pass "no session or exchanged token appears in the hub dashboard, ingress or member operator logs"

section "CLAIM 7: the member operator may not impersonate"
[ "$(kubectl --context "$CTX_M" auth can-i impersonate users --as="system:serviceaccount:$NS:pacto-operator" 2>/dev/null || true)" = no ] ||
  fail "the member operator's ServiceAccount may impersonate users"
pass "the member operator's ServiceAccount may not impersonate users"

echo
echo "MEMBER-REVIEW E2E PASS"
