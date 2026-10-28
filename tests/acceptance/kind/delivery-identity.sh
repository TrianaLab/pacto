#!/usr/bin/env bash
# Phase 4 of the delivery kind acceptance ("dashboard and identity"), sourced by
# delivery.sh. Sourced, never executed directly.
#
# The cluster trusts two identity providers through structured
# authentication: Dex for people (the dashboard signs them in) and Keycloak for
# agents reaching Pacto's MCP server over HTTP (it supports RFC 8693 token
# exchange). Both map auth_time to userInfo.extra["delivery.pacto.trianalab.io/auth-time"], but
# only for tokens issued to the clients that make a person log in again (azp
# pacto-dashboard or pacto-approve), so client-credentials and exchanged
# tokens, and a kubeconfig login (pacto-cli), never carry it.
#
# One address has to work for three parties: the apiserver (inside the
# control-plane node), the dashboard and MCP servers, and this script's test
# client on the host. So the providers, the dashboard and the MCP server all
# run as containers in the control-plane node's network namespace, and kind
# maps their ports to the host's loopback: https://127.0.0.1:5556/dex is the
# same Dex everywhere.
#
# Claims proved (the vision's step-4 list, against the cluster):
#   - delivery appears only where installed; a shared dashboard without
#     sign-in shows no delivery data and offers no actions
#   - a person sees what waits on their team and decides from the dashboard
#     under their own identity (the audit log shows their username)
#   - with a login freshness set (delivery.sh sets one; it is off by default),
#     the promoter ignores an approval whose login is older than it, and the
#     dashboard refuses a decision on such a login until a fresh one; a token
#     without the interactive azp gets no auth-time and admission refuses its
#     approval; a kubeconfig login has none either, so an agent in the
#     person's shell cannot approve with it
#   - `pacto delivery approve` logs the person in again with its own client and
#     records the approval as them
#   - only the named team may approve; the promoter ignores anyone else
#   - an agent reaches Pacto over HTTP as itself (token exchange) and cannot
#     approve through the dashboard, the MCP server or the apiserver
#   - the dashboard's ServiceAccount only lists and watches Stages and Operations
#   - signed in, a person sees only the services of the namespaces Kubernetes
#     RBAC lets them read; a visitor who has not signed in sees none

# mktemp -d makes the directory 0700. Keycloak runs as uid 1000, not the runner's
# uid, so it could not open the import directory and refused to start on CI.
IDP_DIR="$(mktemp -d)"; chmod 755 "$IDP_DIR"
DEX_IMG="ghcr.io/dexidp/dex@sha256:0ae7ade1bdf1c83f7f7ccb1935912ac4c116e3e8ecc7e7503b30bab77e7c896c"
KC_IMG="quay.io/keycloak/keycloak@sha256:9409c59bdfb65dbffa20b11e6f18b8abb9281d480c7ca402f51ed3d5977e6007"
DEX_ISSUER="https://127.0.0.1:5556/dex"
KC_ISSUER="https://127.0.0.1:8443/realms/pacto"
DASH_URL="http://127.0.0.1:3300"
MCP_URL="http://127.0.0.1:8585"
DASH_SECRET="dashboard-secret"
IDP_FIXTURES="$(dirname "${BASH_SOURCE[0]}")/fixtures/identity"
IDP_CONTAINERS=(pacto-e2e-dex pacto-e2e-keycloak pacto-e2e-dashboard pacto-e2e-mcp pacto-e2e-approve)

# A failed run in CI keeps the containers: scripts/ci/kind-diagnostics.sh runs
# after the scenario exits and saves their full logs (rc is the EXIT trap's).
identity_cleanup() {
  if [ -n "${CI:-}" ] && [ "${rc:-0}" -ne 0 ]; then return 0; fi
  docker rm -f "${IDP_CONTAINERS[@]}" >/dev/null 2>&1 || true
}

dump_identity() {
  local c
  for c in "${IDP_CONTAINERS[@]}"; do
    echo "--- logs $c ---"
    docker logs --tail 60 "$c" 2>&1 || true
  done
}

# identity_prepare writes the TLS certificate the providers serve, the
# apiserver's authentication and audit configuration and the kind cluster
# config that mounts them, then prints the kind config path.
identity_prepare() {
  openssl req -x509 -newkey rsa:2048 -nodes -days 2 -keyout "$IDP_DIR/tls.key" -out "$IDP_DIR/tls.crt" \
    -subj /CN=127.0.0.1 -addext "subjectAltName=IP:127.0.0.1,DNS:localhost" >/dev/null 2>&1 \
    || fail "openssl could not make the providers' certificate"
  chmod 644 "$IDP_DIR/tls.key"
  local ca; ca="$(sed 's/^/      /' "$IDP_DIR/tls.crt")"
  {
    echo "apiVersion: apiserver.config.k8s.io/v1"
    echo "kind: AuthenticationConfiguration"
    echo "jwt:"
    for iss in "$DEX_ISSUER" "$KC_ISSUER"; do
      local auds="[pacto-dashboard, pacto-cli]"
      [ "$iss" = "$KC_ISSUER" ] && auds="[pacto-cli]"
      # auth_time is a JSON number and extra values are strings. Only tokens
      # issued to the interactive clients count; a claim-mapping error would
      # fail the whole token, hence the has() guards.
      cat <<YAML
- issuer:
    url: $iss
    audiences: $auds
    audienceMatchPolicy: MatchAny
    certificateAuthority: |
$ca
  claimMappings:
    username: {expression: "'$iss#' + claims.sub"}
    groups: {claim: groups, prefix: ""}
    extra:
    - key: delivery.pacto.trianalab.io/auth-time
      valueExpression: "has(claims.auth_time) && has(claims.azp) && claims.azp in ['pacto-dashboard', 'pacto-approve'] ? string(int(claims.auth_time)) : ''"
YAML
    done
  } > "$IDP_DIR/authn.yaml"
  cp "$IDP_FIXTURES/audit-policy.yaml" "$IDP_DIR/audit-policy.yaml"
  cat > "$IDP_DIR/kind.yaml" <<YAML
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  extraPortMappings:
  - {containerPort: 5556, hostPort: 5556, listenAddress: 127.0.0.1}
  - {containerPort: 8443, hostPort: 8443, listenAddress: 127.0.0.1}
  - {containerPort: 3300, hostPort: 3300, listenAddress: 127.0.0.1}
  - {containerPort: 8585, hostPort: 8585, listenAddress: 127.0.0.1}
  extraMounts:
  - {hostPath: $IDP_DIR, containerPath: /etc/kubernetes/pacto-identity, readOnly: true}
  kubeadmConfigPatches:
  - |
    kind: ClusterConfiguration
    apiServer:
      extraArgs:
        authentication-config: /etc/kubernetes/pacto-identity/authn.yaml
        audit-policy-file: /etc/kubernetes/pacto-identity/audit-policy.yaml
        audit-log-path: /var/log/kubernetes/audit/audit.log
      extraVolumes:
      - {name: pacto-identity, hostPath: /etc/kubernetes/pacto-identity, mountPath: /etc/kubernetes/pacto-identity, readOnly: true, pathType: Directory}
      - {name: audit-log, hostPath: /var/log/kubernetes/audit, mountPath: /var/log/kubernetes/audit, pathType: DirectoryOrCreate}
YAML
  echo "$IDP_DIR/kind.yaml"
}

# in_node NAME IMAGE ARGS... — a container in the control-plane's network
# namespace, the one address every party shares.
in_node() {
  local name="$1"; shift
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker run -d --name "$name" --network "container:${CLUSTER}-control-plane" "$@" >/dev/null
}

# identity_start starts Dex and Keycloak; identity_up waits until the
# apiserver trusts both: a token from each authenticates. Keycloak takes a
# while to start, so the run installs the rest of the cluster in between.
identity_start() {
  cp "$IDP_FIXTURES/dex.yaml" "$IDP_FIXTURES/pacto-realm.json" "$IDP_DIR/"
  chmod 644 "$IDP_DIR/dex.yaml" "$IDP_DIR/pacto-realm.json"
  in_node pacto-e2e-dex -e DEX_SESSIONS_ENABLED=true -v "$IDP_DIR:/etc/dex:ro" "$DEX_IMG" dex serve /etc/dex/dex.yaml
  in_node pacto-e2e-keycloak -e KC_BOOTSTRAP_ADMIN_USERNAME=admin -e KC_BOOTSTRAP_ADMIN_PASSWORD=admin \
    -v "$IDP_DIR:/opt/keycloak/data/import:ro" -v "$IDP_DIR:/etc/x509:ro" "$KC_IMG" \
    start-dev --import-realm --http-enabled=false --https-port=8443 --http-management-port=9100 \
    --https-certificate-file=/etc/x509/tls.crt --https-certificate-key-file=/etc/x509/tls.key --hostname="https://127.0.0.1:8443"
  OIDC_BIN="$(mktemp -d)/oidclogin"
  ( cd "$_PACTO_ROOT" && go build -o "$OIDC_BIN" ./tests/acceptance/kind/oidclogin ) || fail "building the login helper failed"
  # The same helper for the node: `pacto delivery approve` listens on the
  # node's loopback, which only a process in its network namespace reaches.
  OIDC_NODE_BIN="$IDP_DIR/oidclogin-node"
  ( cd "$_PACTO_ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$(docker version -f '{{.Server.Arch}}')" \
      go build -o "$OIDC_NODE_BIN" ./tests/acceptance/kind/oidclogin ) || fail "building the node's login helper failed"
}
identity_up() {
  up() { curl -sf --cacert "$IDP_DIR/tls.crt" "$1/.well-known/openid-configuration" >/dev/null; }
  eventually 60 up "$DEX_ISSUER" || { docker logs pacto-e2e-dex 2>&1 | tail -20; fail "Dex did not start"; }
  eventually 100 up "$KC_ISSUER" || { docker logs pacto-e2e-keycloak 2>&1 | tail -20; fail "Keycloak did not start"; }
  local kc dex
  kc="$(kc_exchange "$(kc_agent_token)")"
  dex="$(dex_token alice)"
  trusted() { # TOKEN ISSUER — the apiserver authenticates TOKEN as ISSUER#sub
    whoami_json "$1" 2>/dev/null | jq -e --arg iss "$2#" '.status.userInfo.username | startswith($iss)' >/dev/null
  }
  # The apiserver reads a provider's keys in the background after it starts.
  eventually 40 trusted "$kc" "$KC_ISSUER" || fail "the apiserver does not trust Keycloak: $(whoami_json "$kc" 2>&1)"
  eventually 40 trusted "$dex" "$DEX_ISSUER" || fail "the apiserver does not trust Dex: $(whoami_json "$dex" 2>&1)"
  pass "Dex and Keycloak are up and the apiserver trusts both"
}

# dex_token USER [SCOPE] — an ID token for the dashboard client. The default
# scope names the CLI client too, so the token has two audiences and Dex sets
# azp; without it Dex sets no azp.
dex_token() {
  oidc token -issuer "$DEX_ISSUER" -client pacto-dashboard -secret "$DASH_SECRET" -redirect "$DASH_URL/auth/callback" \
    -scope "${2:-openid profile groups audience:server:client_id:pacto-cli}" -user "$1@example.com" -password password -jar "$IDP_DIR/jar-$1-token"
}

kc_token() { # -d ... — a Keycloak token-endpoint answer's access token
  curl -sf --cacert "$IDP_DIR/tls.crt" "$KC_ISSUER/protocol/openid-connect/token" "$@" | jq -r .access_token
}
kc_agent_token() { kc_token -u agent-bot:agent-secret -d grant_type=client_credentials; }
kc_person_token() { kc_token -d grant_type=password -d client_id=mcp-client -d username=dave -d password=password -d scope=openid; }
# kc_exchange TOKEN — what the MCP server does with a caller's token.
kc_exchange() {
  kc_token -u pacto-mcp:mcp-secret -d grant_type=urn:ietf:params:oauth:grant-type:token-exchange -d subject_token="$1" \
    -d subject_token_type=urn:ietf:params:oauth:token-type:access_token \
    -d requested_token_type=urn:ietf:params:oauth:token-type:access_token -d audience=pacto-cli
}

# oidc ARGS... — the login helper, trusting the providers' certificate.
oidc() { "$OIDC_BIN" "$@" -ca "$IDP_DIR/tls.crt"; }

# as_token TOKEN ARGS... — kubectl with TOKEN as the only credential (the
# admin kubeconfig's client certificate would win over --token).
as_token() {
  local tok="$1" f; shift
  f="$(mktemp "$IDP_DIR/token-kubeconfig.XXXX")"
  cat > "$f" <<YAML
apiVersion: v1
kind: Config
clusters: [{name: kind, cluster: {server: "$(kubectl config view --raw --minify -o jsonpath='{.clusters[0].cluster.server}')", certificate-authority-data: "$(kubectl config view --raw --minify -o jsonpath='{.clusters[0].cluster.certificate-authority-data}')"}}]
users: [{name: u, user: {token: "$tok"}}]
contexts: [{name: c, context: {cluster: kind, user: u}}]
current-context: c
YAML
  KUBECONFIG="$f" kubectl "$@"
}

# Callers may not list CRDs, which kubectl's client-side validation needs, so
# the apiserver validates instead (--validate=false).
whoami_json() { as_token "$1" create --validate=false -o json -f - <<'YAML'
apiVersion: authentication.k8s.io/v1
kind: SelfSubjectReview
YAML
}

# identity_login_stale signs carol in at the start of the run. When phase 4
# needs it, her login is older than the promoter's login freshness.
identity_login_stale() {
  STALE_AT="$(date +%s)"
  STALE_TOKEN="$(dex_token carol)" || fail "carol could not log in through Dex"
  pass "carol logged in at the start of the run (her login goes stale before phase 4)"
}

# kubeconfig_for TOKEN — the cluster as the node sees it, with TOKEN or with no
# credentials at all (the dashboard and MCP servers add callers' tokens).
kubeconfig_for() {
  local f; f="$(mktemp "$IDP_DIR/kubeconfig.XXXX")"
  local user="{}"; [ -n "${1:-}" ] && user="{token: \"$1\"}"
  cat > "$f" <<YAML
apiVersion: v1
kind: Config
clusters: [{name: kind, cluster: {server: "https://127.0.0.1:6443", certificate-authority-data: "$CA"}}]
users: [{name: u, user: $user}]
contexts: [{name: c, context: {cluster: kind, user: u}}]
current-context: c
YAML
  chmod 644 "$f"
  echo "$f"
}

# mcp TOKEN METHOD PARAMS — one JSON-RPC call to the remote MCP server; prints
# the JSON answer (the server is stateless: no initialize needed).
mcp() {
  local auth=(); [ -n "$1" ] && auth=(-H "Authorization: Bearer $1")
  curl -s "${auth[@]}" -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
    -H 'Mcp-Protocol-Version: 2025-06-18' \
    -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"$2\",\"params\":$3}" "$MCP_URL/mcp" | sed -n 's/^data: //p;/^{/p'
}

# dash SESSION METHOD PATH [BODY] [CSRF] — a dashboard API call as the browser
# would make it: the session cookie, the dashboard's own Origin and the CSRF
# header. Prints the status line's code, then the body.
dash() {
  local args=(-s -o "$IDP_DIR/body" -w '%{http_code}' -X "$2" -H "Origin: $DASH_URL" -H 'Content-Type: application/json')
  [ -n "$1" ] && args+=(-H "Cookie: __Host-pacto-session=$1")
  [ -n "${5:-}" ] && args+=(-H "X-Pacto-CSRF: $5")
  [ -n "${4:-}" ] && args+=(-d "$4")
  local code; code="$(curl "${args[@]}" "$DASH_URL$3")"
  echo "$code $(cat "$IDP_DIR/body")"
}
csrf_of() { dash "$1" GET /api/session | cut -d' ' -f2- | jq -r .csrf; }
login() { oidc dashboard -dashboard "$DASH_URL" -user "$1@example.com" -password password -jar "$IDP_DIR/jar-$1" "${@:2}"; }

audited() { # VERB RESOURCE USER — the apiserver's audit log saw USER do it
  docker exec "${CLUSTER}-control-plane" cat /var/log/kubernetes/audit/audit.log |
    jq -e --arg v "$1" --arg r "$2" --arg u "$3" 'select(.verb == $v and .objectRef.resource == $r and .user.username == $u and .responseStatus.code < 300)' >/dev/null
}

# identity_rbac_views: viewing the dashboard reuses Kubernetes RBAC. erin may
# list Pactos in rbac-open only; alice's approver role reads them everywhere.
# Each Pacto names a readable contract of its own, depending on nothing: a
# Pacto with no contract would count in every environment's graph and hold the
# plans of the phases running beside this one.
identity_rbac_views() {
  local ns ref
  for ns in rbac-open rbac-closed; do
    ref="$(contract "$ns-svc" 1.0.0 service)"
    kubectl create namespace "$ns" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
    kubectl apply -n "$ns" -f - >/dev/null <<YAML
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: {name: $ns-svc}
spec:
  checkIntervalSeconds: 30
  contractRef: {oci: "$ref"}
  target: {workloadRef: {name: $ns-svc, kind: Deployment}}
YAML
  done
  kubectl -n rbac-open create role pacto-viewer --verb=get,list --resource=pactos.pacto.trianalab.io \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  kubectl -n rbac-open create rolebinding pacto-viewer --role=pacto-viewer --group=team-viewers \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  local erin alice out
  erin="$(login erin)" || fail "erin could not sign in to the dashboard"
  alice="$(login alice)" || fail "alice could not sign in to the dashboard"
  names() { dash "$1" GET "/api/fleet/services?text=rbac-" | cut -d' ' -f2- | jq -c '[.services[].name] | sort'; }
  # The dashboard rebuilds its snapshot on an interval.
  both() { [ "$(names "$alice")" = '["rbac-closed-svc","rbac-open-svc"]' ]; }
  eventually 40 both || fail "alice, who reads Pactos everywhere, does not see both services: $(names "$alice")"
  [ "$(names "$erin")" = '["rbac-open-svc"]' ] || fail "erin sees services outside the namespace she may read: $(names "$erin")"
  out="$(dash "$erin" GET /api/fleet/snapshot)"
  [ "${out%% *}" = 200 ] && ! grep -q "rbac-closed" <<<"$out" || fail "erin's snapshot names what she may not read: ${out:0:300}"
  dash "$erin" GET /api/capabilities | cut -d' ' -f2- | jq -e '.auth.hidden == true' >/dev/null \
    || fail "erin is not told that some services are hidden"
  dash "$erin" GET /api/session | cut -d' ' -f2- | jq -e --arg iss "$DEX_ISSUER" \
    '(.user | startswith($iss + "#")) and (.groups | index("team-viewers"))' >/dev/null \
    || fail "erin's session does not name her user and groups: $(dash "$erin" GET /api/session)"
  # Signed out, every fleet view is refused, not narrowed.
  local path
  for path in "/api/fleet/services?text=rbac-" /api/fleet/snapshot /api/fleet/overview /api/capabilities /api/sources /metrics; do
    out="$(dash "" GET "$path")"
    [ "${out%% *}" = 401 ] || fail "a visitor who has not signed in got $path: ${out:0:300}"
  done
  pass "signed in, erin sees only rbac-open's services and alice sees both; a visitor gets no fleet view at all"

  # A pasted ServiceAccount token signs in like kubectl: its RBAC decides.
  kubectl -n rbac-open create serviceaccount rbac-viewer --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  kubectl -n rbac-open create rolebinding rbac-viewer --role=pacto-viewer --serviceaccount=rbac-open:rbac-viewer \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  token_login() { # TOKEN — the session cookie a token sign-in sets, or the HTTP status
    curl -s -D - -o /dev/null -X POST -H "Origin: $DASH_URL" -H 'Content-Type: application/json' \
      -d "{\"token\":\"$1\"}" "$DASH_URL/auth/token" | tr -d '\r' |
      sed -n -e 's/^HTTP\/[0-9.]* \(4[0-9][0-9]\).*/\1/p' -e 's/^[Ss]et-[Cc]ookie: __Host-pacto-session=\([^;]*\).*/\1/p'
  }
  out="$(token_login not-a-token)"
  [ "$out" = 401 ] || fail "a token the cluster refuses signed in: $out"
  local sa; sa="$(token_login "$(kubectl -n rbac-open create token rbac-viewer)")"
  [ -n "$sa" ] && [ "${#sa}" -gt 3 ] || fail "a ServiceAccount token did not sign in: $sa"
  dash "$sa" GET /api/session | cut -d' ' -f2- |
    jq -e '.method == "token" and .user == "system:serviceaccount:rbac-open:rbac-viewer"' >/dev/null \
    || fail "the token session does not name the ServiceAccount: $(dash "$sa" GET /api/session)"
  [ "$(names "$sa")" = '["rbac-open-svc"]' ] || fail "the token session sees services outside rbac-open: $(names "$sa")"
  out="$(dash "$sa" POST /api/delivery/operations/prod/any/decision '{"decision":"approve"}' "$(csrf_of "$sa")")"
  [ "${out%% *}" = 403 ] && grep -q TokenCannotDecide <<<"$out" || fail "a token session decided: $out"
  pass "a pasted ServiceAccount token signs in, sees only what its RBAC reads and never decides"
}

# servers_up — wait for the dashboard and the MCP server in_node started; the
# delivery demo starts the same two and waits the same way.
dash_up() { curl -sf "$DASH_URL/health" >/dev/null; }
mcp_up() { [ "$(curl -s -o /dev/null -w '%{http_code}' "$MCP_URL/mcp")" = 401 ]; }
servers_up() {
  eventually 40 dash_up || { docker logs pacto-e2e-dashboard 2>&1 | tail; fail "the dashboard did not start"; }
  eventually 40 mcp_up || { docker logs pacto-e2e-mcp 2>&1 | tail; fail "the MCP server did not start"; }
}

identity_scenario() {
  section "phase 4: the dashboard and identity"
  # --- structured authentication ---
  local alice; alice="$(dex_token alice)"
  local who; who="$(whoami_json "$alice")"
  jq -e --arg iss "$DEX_ISSUER" '.status.userInfo.username | startswith($iss + "#")' <<<"$who" >/dev/null \
    || fail "a Dex user is not issuer#sub: $who"
  jq -e '.status.userInfo.groups | index("team-payments")' <<<"$who" >/dev/null || fail "alice's groups are not mapped: $who"
  jq -e '.status.userInfo.extra["delivery.pacto.trianalab.io/auth-time"][0] | test("^[0-9]+$")' <<<"$who" >/dev/null \
    || fail "an interactive login has no auth-time: $who"
  pass "a dashboard login authenticates as issuer#sub with its groups and its auth-time"
  local noazp; noazp="$(dex_token alice "openid profile groups")"
  who="$(whoami_json "$noazp")"
  jq -e '.status.userInfo.username != null and (.status.userInfo.extra["delivery.pacto.trianalab.io/auth-time"] == null)' <<<"$who" >/dev/null \
    || fail "a token without the interactive azp got an auth-time: $who"
  local agent; agent="$(kc_exchange "$(kc_agent_token)")"
  who="$(whoami_json "$agent")"
  jq -e '(.status.userInfo.groups | index("pacto:agents")) and (.status.userInfo.extra["delivery.pacto.trianalab.io/auth-time"] == null)' <<<"$who" >/dev/null \
    || fail "an exchanged agent token is not an agent without auth-time: $who"
  local AGENT_USER; AGENT_USER="$(jq -r .status.userInfo.username <<<"$who")"
  pass "a token without azp, and an exchanged agent token, authenticate with no auth-time"
  # A kubeconfig login: the CLI client's own token, with azp pacto-cli. Any
  # process in the person's shell can reuse it, so it never carries the login
  # time an approval needs.
  local kubelogin
  kubelogin="$(oidc token -issuer "$DEX_ISSUER" -client pacto-cli -redirect http://127.0.0.1:8000 \
    -scope "openid profile groups audience:server:client_id:pacto-dashboard" -user alice@example.com -password password)" \
    || fail "alice could not log in as a kubeconfig would"
  who="$(whoami_json "$kubelogin")"
  jq -e '.status.userInfo.username != null and (.status.userInfo.extra["delivery.pacto.trianalab.io/auth-time"] == null)' <<<"$who" >/dev/null \
    || fail "a kubeconfig login got an auth-time, so anything in the shell could approve with it: $who"
  pass "a kubeconfig login authenticates with no auth-time"
  # Every client reads the one identity policy the chart's delivery section publishes,
  # with the person's own token: nothing is configured twice. This run installs
  # the chart with no team group prefix, since its contracts name the groups
  # themselves (owner team-checkout is group team-checkout).
  local policy
  policy="$(as_token "$kubelogin" -n "$DNS" get configmap pacto-delivery-identities -o jsonpath='{.data.policy\.json}')" \
    || fail "a signed-in person cannot read the published identity policy"
  jq -e '(.teamGroupPrefix // "") == "" and .requireOwnerTeam == true and (.agentGroups | index("pacto:agents"))' <<<"$policy" >/dev/null \
    || fail "the published identity policy is not the chart's: $policy"
  pass "a signed-in person reads the identity policy the promoter runs with"

  # --- identities and rights ---
  kubectl create clusterrolebinding delivery-oidc-approvers --clusterrole=pacto-operator-delivery-approver \
    --group=team-checkout --group=team-payments --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  kubectl create clusterrolebinding delivery-oidc-agents --clusterrole=pacto-operator-delivery-agent \
    --group=pacto:agents --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  local DSA="system:serviceaccount:${NS}:pacto-dashboard"
  # Only a plain "no" passes: a kubectl error prints nothing on stdout. The
  # auth-time extra is a subresource of userextras, as RBAC grants it.
  local v ans
  for v in "create approvals.delivery.pacto.trianalab.io -A" "create operations.delivery.pacto.trianalab.io -A" \
    "delete operations.delivery.pacto.trianalab.io -A" "update stages.delivery.pacto.trianalab.io -A" \
    "update stages.delivery.pacto.trianalab.io -A --subresource=status" "list approvals.delivery.pacto.trianalab.io -A" \
    "impersonate users" "impersonate userextras --subresource=delivery.pacto.trianalab.io/auth-time"; do
    # shellcheck disable=SC2086
    ans="$(kubectl auth can-i $v --as "$DSA" 2>/dev/null || true)"
    [[ "$ans" == no* ]] || fail "the dashboard's ServiceAccount may $v, or kubectl could not say: '$ans'"
  done
  # Its shared cache lists and watches Stages and Operations, nothing more.
  for v in "list stages.delivery.pacto.trianalab.io -A" "watch stages.delivery.pacto.trianalab.io -A" \
    "list operations.delivery.pacto.trianalab.io -A" "watch operations.delivery.pacto.trianalab.io -A"; do
    # shellcheck disable=SC2086
    ans="$(kubectl auth can-i $v --as "$DSA" 2>/dev/null || true)"
    [[ "$ans" == yes ]] || fail "the dashboard's ServiceAccount may not $v for its shared cache: '$ans'"
  done
  pass "the dashboard's ServiceAccount only reads Stages and Operations in delivery and cannot impersonate"
  kubectl -n "$NS" get deploy pacto-dashboard -o json | jq -e '.spec.template.spec.containers[0].env as $e |
      ($e | map(select(.name == "PACTO_DASHBOARD_OIDC_ISSUER_URL"))[0].value == "'"$DEX_ISSUER"'") and
      ($e | map(select(.name == "PACTO_DASHBOARD_OIDC_CLIENT_SECRET"))[0].valueFrom.secretKeyRef.name == "dashboard-oidc")' >/dev/null \
    || fail "the operator did not pass the chart's sign-in settings to the dashboard"
  pass "the operator-managed dashboard gets its sign-in from the chart, the secret by reference"

  # --- the servers, in the node's network namespace ---
  local dashkc mcpkc
  dashkc="$(kubeconfig_for "$(kubectl -n "$NS" create token pacto-dashboard --duration 2h)")"
  mcpkc="$(kubeconfig_for "")"
  # The node's own resolver does not know cluster names, and every lookup of
  # the Stages' registry would wait out its 5s timeout, retried per pull.
  local dns; dns="$(kubectl -n kube-system get svc kube-dns -o jsonpath='{.spec.clusterIP}')"
  printf 'search svc.cluster.local cluster.local\nnameserver %s\noptions ndots:5\n' "$dns" > "$IDP_DIR/resolv.conf"
  in_node pacto-e2e-dashboard -e KUBECONFIG=/kube/config -e SSL_CERT_FILE=/ca/tls.crt -e HOME=/tmp -v "$IDP_DIR/resolv.conf:/etc/resolv.conf:ro" \
    -e PACTO_DASHBOARD_OIDC_CLIENT_SECRET="$DASH_SECRET" -v "$dashkc:/kube/config:ro" -v "$IDP_DIR/tls.crt:/ca/tls.crt:ro" \
    "$DASH_IMG" dashboard --host 0.0.0.0 --port 3300 --oidc-issuer-url "$DEX_ISSUER" --oidc-client-id pacto-dashboard \
    --oidc-redirect-url "$DASH_URL/auth/callback" --oidc-extra-scopes audience:server:client_id:pacto-cli --token-login
  in_node pacto-e2e-mcp -e KUBECONFIG=/kube/config -e SSL_CERT_FILE=/ca/tls.crt -e HOME=/tmp -v "$IDP_DIR/resolv.conf:/etc/resolv.conf:ro" \
    -e PACTO_MCP_EXCHANGE_CLIENT_SECRET=mcp-secret -v "$mcpkc:/kube/config:ro" -v "$IDP_DIR/tls.crt:/ca/tls.crt:ro" \
    "$DASH_IMG" mcp --delivery -t http --host 0.0.0.0 --port 8585 --auth-issuer "$KC_ISSUER" \
    --resource "$MCP_URL/mcp" --audience pacto-mcp --exchange-client-id pacto-mcp
  servers_up

  # Signed out, the page, its sign-in check and the probes answer; the API does not.
  local out; out="$(dash "" GET /api/session)"
  jq -e '.mode == "shared" and .login == true and .signedIn == false' <<<"${out#* }" >/dev/null || fail "session signed out: $out"
  [ "$(curl -s -o /dev/null -w '%{http_code}' "$DASH_URL/")" = 200 ] || fail "the sign-in page does not load signed out"
  [ "$(curl -s -o /dev/null -w '%{http_code}' "$DASH_URL/health")" = 200 ] || fail "/health is not open to probes"
  out="$(dash "" GET /api/delivery/inbox)"
  [ "${out%% *}" = 401 ] || fail "delivery without sign-in: $out"
  local signed; signed="$(login alice)" || fail "alice could not sign in to the dashboard"
  out="$(dash "$signed" GET /api/capabilities)"
  jq -e '.delivery == true and .auth.mode == "shared" and .auth.login == true and .auth.signedIn == true' <<<"${out#* }" >/dev/null \
    || fail "capabilities: $out"
  pass "the dashboard answers nothing before sign-in, and offers delivery to people who signed in"
  identity_rbac_views

  # --- the dashboard: people act as themselves ---
  # Automation stays out of the way: prod is frozen from phase 1, and nothing
  # may move it while phase 4 runs.
  kubectl -n prod patch stage payments --type merge -p '{"spec":{"auto":false}}' >/dev/null
  local carol; carol="$(login carol)" || fail "carol could not sign in to the dashboard"
  out="$(dash "$carol" GET /api/session)"
  jq -e --arg iss "$DEX_ISSUER" '.caller.user | startswith($iss + "#")' <<<"${out#* }" >/dev/null || fail "session: $out"
  local CAROL; CAROL="$(jq -r .caller.user <<<"${out#* }")"
  local cs; cs="$(csrf_of "$carol")"
  out="$(dash "$carol" POST /api/delivery/operations "{\"namespace\":\"prod\",\"stage\":\"payments\",\"to\":\"$F\",\"name\":\"idp-carol\",\"reason\":\"phase 4\"}" "$cs")"
  [ "${out%% *}" = 201 ] || fail "carol's proposal: $out"
  [ "$(kubectl -n prod get operation idp-carol -o jsonpath='{.spec.actor.user}')" = "$CAROL" ] || fail "the proposal is not carol's"
  audited create operations "$CAROL" || fail "the audit log does not show carol proposing"
  eventually 30 held prod idp-carol || fail "carol's F was not held: $(field prod idp-carol phase)"
  out="$(dash "$carol" DELETE /api/delivery/operations/prod/idp-carol "" "$cs")"
  [ "${out%% *}" = 200 ] || fail "carol's cancel: $out"
  audited delete operations "$CAROL" || fail "the audit log does not show carol cancelling"
  out="$(dash "$carol" POST /api/delivery/operations '{"namespace":"prod","stage":"payments","type":"delivery.pacto.trianalab.io/unfreeze","name":"idp-unfreeze","reason":"phase 4"}' "$cs")"
  [ "${out%% *}" = 201 ] || fail "carol's unfreeze: $out"
  eventually 40 concluded prod idp-unfreeze || fail "carol's unfreeze never concluded: $(field prod idp-unfreeze message)"
  # carol is on team-checkout: only payments' own team, or break-glass, unfreezes payments.
  [ "$(outcome prod idp-unfreeze)" = Rejected/ ] || fail "carol's unfreeze concluded $(outcome prod idp-unfreeze)"
  field prod idp-unfreeze reason | grep -q NotYourTeam || field prod idp-unfreeze message | grep -q "NotYourTeam\|not in team-payments" \
    || fail "carol's unfreeze was refused for the wrong reason: $(field prod idp-unfreeze reason) $(field prod idp-unfreeze message)"
  [ "$(kubectl -n prod get operation idp-unfreeze -o jsonpath='{.spec.actor.user}')" = "$CAROL" ] || fail "the unfreeze is not carol's"
  local alice_u; alice_u="$(login alice)" || fail "alice could not sign in to the dashboard"
  out="$(dash "$alice_u" GET /api/session)"
  local ALICE; ALICE="$(jq -r .caller.user <<<"${out#* }")"
  out="$(dash "$alice_u" POST /api/delivery/operations '{"namespace":"prod","stage":"payments","type":"delivery.pacto.trianalab.io/unfreeze","name":"idp-unfreeze-alice","reason":"phase 4"}' "$(csrf_of "$alice_u")")"
  [ "${out%% *}" = 201 ] || fail "alice's unfreeze: $out"
  eventually 40 concluded prod idp-unfreeze-alice || fail "alice's unfreeze never concluded: $(field prod idp-unfreeze-alice message)"
  [[ "$(outcome prod idp-unfreeze-alice)" == Succeeded/* ]] || fail "alice's unfreeze concluded $(outcome prod idp-unfreeze-alice)"
  [ "$(kubectl -n prod get stage payments -o jsonpath='{.status.frozen}')" != true ] || fail "prod is still frozen"
  [ "$(kubectl -n prod get operation idp-unfreeze-alice -o jsonpath='{.spec.actor.user}')" = "$ALICE" ] || fail "the unfreeze is not alice's"
  pass "propose and cancel from the dashboard run as carol; her unfreeze of payments is refused, alice of payments' team unfreezes it, and the audit log shows them"

  # --- remote MCP: agents as themselves, people read-only, nobody approves ---
  out="$(curl -s -D - -o /dev/null -X POST "$MCP_URL/mcp")"
  grep -qi 'www-authenticate: Bearer resource_metadata="http://127.0.0.1:8585/.well-known/oauth-protected-resource/mcp"' <<<"$out" \
    || fail "an unauthenticated MCP call is not told where to authenticate: $out"
  curl -s "$MCP_URL/.well-known/oauth-protected-resource/mcp" | jq -e --arg i "$KC_ISSUER" '.authorization_servers == [$i]' >/dev/null \
    || fail "no protected-resource metadata"
  local agent_tools; agent_tools="$(mcp "$(kc_agent_token)" tools/list '{}')"
  jq -e '[.result.tools[].name] | (index("pacto_delivery_operation_propose") != null) and (map(select(test("approve"))) | length == 0)' <<<"$agent_tools" >/dev/null \
    || fail "agent tools over HTTP: $agent_tools"
  local person_tools; person_tools="$(mcp "$(kc_person_token)" tools/list '{}')"
  jq -e '[.result.tools[].name] | (index("pacto_delivery_get") != null) and (index("pacto_delivery_operation_propose") == null)' <<<"$person_tools" >/dev/null \
    || fail "a person's tools over HTTP: $person_tools"
  pass "over HTTP an agent gets propose and cancel, a person the read tools, nobody an approve tool"

  # The agent proposes F (payments 2.0.0, which breaks checkout) on prod.
  local from; from="$(kubectl -n prod get stage payments -o jsonpath='{.status.pointer}')"
  out="$(mcp "$(kc_agent_token)" tools/call "{\"name\":\"pacto_delivery_operation_propose\",\"arguments\":{\"namespace\":\"prod\",\"stage\":\"payments\",\"to\":\"$F\",\"from\":\"$from\",\"name\":\"idp-f\",\"reason\":\"phase 4\"}}")"
  kubectl -n prod get operation idp-f >/dev/null 2>&1 || fail "the agent could not propose over HTTP: $out"
  [ "$(kubectl -n prod get operation idp-f -o jsonpath='{.spec.actor.user}')" = "$AGENT_USER" ] \
    || fail "the operation's actor is not the agent's own identity"
  audited create operations "$AGENT_USER" || fail "the audit log does not show the agent creating the operation"
  eventually 30 held prod idp-f || fail "F was not held in prod: $(field prod idp-f phase) $(field prod idp-f message)"
  kubectl -n prod get operation idp-f -o json | jq -e '.status.actorClass == "agent" and .status.plan.floor.owners == ["team-checkout"]' >/dev/null \
    || fail "F is not held for team-checkout as an agent's operation"
  pass "an agent proposed over HTTP as itself; F waits for team-checkout"

  # --- decisions ---
  out="$(dash "$carol" GET /api/delivery/inbox)"
  jq -e '.held | map(select(.name == "idp-f"))[0] | (.question | contains("team-checkout")) and (.consumers | index("checkout"))
    and (.order.moveFirst | map(.service) == ["checkout"])' \
    <<<"${out#* }" >/dev/null || fail "carol's inbox does not show F with its question, the services it affects and what moves first: $out"
  out="$(dash "$carol" GET /api/delivery/operations/prod/idp-f)"
  jq -e '.actions | index("approve")' <<<"${out#* }" >/dev/null || fail "carol is not offered the decision: $out"
  pass "carol (team-checkout) sees F in her inbox with the question and the decision"
  out="$(dash "$carol" POST /api/delivery/operations/prod/idp-f/decision '{"decision":"approve"}')"
  [ "${out%% *}" = 403 ] || fail "a decision without the CSRF header: $out"
  out="$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "Cookie: __Host-pacto-session=$carol" -H "X-Pacto-CSRF: $cs" \
    -H 'Content-Type: application/json' -d '{"decision":"approve"}' "$DASH_URL/api/delivery/operations/prod/idp-f/decision")"
  [ "$out" = 403 ] || fail "a decision without an Origin: $out"
  pass "a decision needs the CSRF header and the dashboard's own origin"

  # Only the named team: alice (team-payments) is refused by the dashboard and
  # ignored by the promoter.
  local alice_s; alice_s="$(login alice -fresh)" || fail "alice could not sign in"
  out="$(dash "$alice_s" POST /api/delivery/operations/prod/idp-f/decision '{"decision":"approve"}' "$(csrf_of "$alice_s")")"
  [ "${out%% *}" = 403 ] && grep -q NotYourTeam <<<"$out" || fail "alice decided for team-checkout: $out"
  local uid digest
  uid="$(kubectl -n prod get operation idp-f -o jsonpath='{.metadata.uid}')"
  digest="$(kubectl -n prod get operation idp-f -o jsonpath='{.spec.intentDigest}')"
  approval_as() { # TOKEN NAME [GROUPS] — an Approval created straight at the apiserver with
    # TOKEN; GROUPS (a JSON list, or "omit") replaces the caller's own groups.
    local w g; w="$(whoami_json "$1")"
    g="groups: ${3:-$(jq -c .status.userInfo.groups <<<"$w")}"
    [ "${3:-}" = omit ] && g=""
    as_token "$1" -n prod create --validate=false -f - <<YAML
apiVersion: delivery.pacto.trianalab.io/v1alpha1
kind: Approval
metadata: {name: $2}
spec:
  operation: idp-f
  operationUID: $uid
  intentDigest: $digest
  decision: approve
  by: $(jq -r .status.userInfo.username <<<"$w")
  $g
  authTime: "$(jq -r '.status.userInfo.extra["delivery.pacto.trianalab.io/auth-time"][0] // ""' <<<"$w")"
YAML
  }
  approval_as "$(dex_token alice)" idp-alice >/dev/null || fail "alice's own Approval was refused by admission"
  still_held() { held prod idp-f; }
  always 5 still_held || fail "the promoter took an approval from outside team-checkout"
  pass "only team-checkout may approve: the dashboard refuses alice and the promoter ignores her Approval"
  # The promoter trusts an Approval's groups, so admission must refuse any
  # that are not exactly the approver's: alice naming team-checkout, or
  # leaving her groups out.
  local forged refused
  for forged in '["team-checkout"]' omit; do
    refused="$(approval_as "$(dex_token alice)" idp-forged "$forged" 2>&1 || true)"
    grep -q "spec.groups must be the groups of the user creating the approval" <<<"$refused" \
      || fail "admission let alice give an Approval groups that are not hers ($forged): $refused"
  done
  ! kubectl -n prod get approval idp-forged >/dev/null 2>&1 || fail "an Approval with forged groups exists"
  pass "admission refuses an Approval whose groups are not exactly the approver's, forged or left out"

  # A token without the interactive azp carries no auth-time: admission refuses.
  refused="$(approval_as "$noazp" idp-noazp 2>&1 || true)"
  grep -q "approving needs a fresh interactive login" <<<"$refused" || fail "an approval without auth-time was not refused for it: $refused"
  # An agent cannot approve: not with its exchanged token at the apiserver,
  # where its role grants no create...
  refused="$(approval_as "$agent" idp-agent 2>&1 || true)"
  grep -qE 'cannot create resource "approvals"|agents and service accounts may not approve' <<<"$refused" \
    || fail "an agent's Approval was not refused as an agent's: $refused"
  # ...nor with approver rights: robot is in team-checkout, so only the
  # admission rule on agent groups stops it...
  refused="$(approval_as "$(dex_token robot)" idp-robot 2>&1 || true)"
  grep -q "agents and service accounts may not approve" <<<"$refused" || fail "admission let an agent with approver rights approve: $refused"
  [ -z "$(kubectl -n prod get approval idp-noazp idp-agent idp-robot --ignore-not-found -o name)" ] || fail "a refused Approval exists"
  # ...nor through the dashboard, signed in as a person in an agent group.
  local robot; robot="$(login robot -fresh)" || fail "robot could not sign in"
  out="$(dash "$robot" POST /api/delivery/operations/prod/idp-f/decision '{"decision":"approve"}' "$(csrf_of "$robot")")"
  [ "${out%% *}" = 403 ] && grep -q AgentsMayNotApprove <<<"$out" || fail "an agent decided through the dashboard: $out"
  pass "no auth-time, no approval; an agent cannot approve at the apiserver, through MCP or through the dashboard"

  # A stale login: carol's first login, now older than the promoter's login
  # freshness (AUTH_FRESHNESS seconds, set by delivery.sh), with a minute to spare.
  local wait=$((STALE_AT + AUTH_FRESHNESS + 60 - $(date +%s)))
  if [ "$wait" -gt 0 ]; then
    echo "  waiting ${wait}s for carol's first login to pass the promoter's $((AUTH_FRESHNESS / 60)) minutes"
    local slept=$SECONDS; sleep "$wait"; _timed_wait sleep "$slept" "carol's first login goes stale"
  fi
  approval_as "$STALE_TOKEN" idp-stale >/dev/null || fail "carol's stale Approval was refused by admission (its auth-time matches)"
  always 5 still_held || fail "the promoter took an approval whose login is older than its login freshness"
  # Single sign-on gives carol a new session but keeps that old login: still no decision.
  local sso; sso="$(login carol -session "$carol")"
  out="$(dash "$sso" POST /api/delivery/operations/prod/idp-f/decision '{"decision":"approve"}' "$(csrf_of "$sso")")"
  [ "${out%% *}" = 401 ] && grep -q FreshLoginRequired <<<"$out" || fail "a session from an old login decided: $out"
  pass "an approval on a stale login is ignored, and a single-sign-on session cannot decide"

  # A fresh login rotates the session and decides; the apiserver sees carol.
  local fresh; fresh="$(login carol -fresh -session "$sso")" || fail "carol's fresh login failed"
  [ "$fresh" != "$sso" ] || fail "a fresh login did not rotate the session id"
  [ "$(dash "$sso" GET /api/session | cut -d' ' -f2- | jq -r .signedIn)" = false ] || fail "the rotated session still works"
  out="$(dash "$fresh" POST /api/delivery/operations/prod/idp-f/decision '{"decision":"approve","note":"checkout is ready"}' "$(csrf_of "$fresh")")"
  [ "${out%% *}" = 201 ] || fail "carol's fresh decision: $out"
  local approval; approval="$(jq -r .approval <<<"${out#* }")"
  [ "$(kubectl -n prod get approval "$approval" -o jsonpath='{.spec.by}')" = "$CAROL" ] || fail "the Approval is not carol's"
  audited create approvals "$CAROL" || fail "the audit log does not show carol creating the Approval"
  wait_concluded prod idp-f Succeeded/Verified
  [ "$(kubectl -n prod get operation idp-f -o jsonpath='{.status.approvals[*].name}')" = "$approval" ] \
    || fail "the promoter consumed an approval other than carol's fresh one"
  pass "carol approved from the dashboard after a fresh login; the audit log shows her, F shipped"

  # --- roll back and reject from the dashboard, as carol ---
  out="$(dash "$fresh" POST /api/delivery/operations "{\"namespace\":\"prod\",\"stage\":\"payments\",\"type\":\"delivery.pacto.trianalab.io/rollback\",\"undoes\":\"idp-f\",\"name\":\"idp-undo\",\"reason\":\"phase 4\"}" "$(csrf_of "$fresh")")"
  [ "${out%% *}" = 201 ] || fail "carol's rollback: $out"
  [ "$(kubectl -n prod get operation idp-undo -o jsonpath='{.spec.actor.user} {.spec.params.undoes}')" = "$CAROL $uid" ] \
    || fail "the rollback is not carol's undoing of F: $(kubectl -n prod get operation idp-undo -o jsonpath='{.spec}')"
  wait_concluded prod idp-undo Succeeded/Verified
  runs prod payments "$from" || fail "prod is not back on the version before F"
  out="$(mcp "$(kc_agent_token)" tools/call "{\"name\":\"pacto_delivery_operation_propose\",\"arguments\":{\"namespace\":\"prod\",\"stage\":\"payments\",\"to\":\"$F\",\"from\":\"$from\",\"name\":\"idp-f2\",\"reason\":\"phase 4 again\"}}")"
  eventually 30 held prod idp-f2 || fail "F was not held again: $out $(field prod idp-f2 phase) $(field prod idp-f2 message)"
  local again; again="$(login carol -fresh -session "$fresh")" || fail "carol's second fresh login failed"
  out="$(dash "$again" POST /api/delivery/operations/prod/idp-f2/decision '{"decision":"reject","note":"not this week"}' "$(csrf_of "$again")")"
  [ "${out%% *}" = 201 ] || fail "carol's reject: $out"
  approval="$(jq -r .approval <<<"${out#* }")"
  kubectl -n prod get approval "$approval" -o json | jq -e --arg u "$CAROL" '.spec.by == $u and .spec.decision == "reject"' >/dev/null \
    || fail "the rejection is not carol's"
  wait_concluded prod idp-f2 Rejected/
  # The promoter splits "RejectedByHuman: <by>: <note>" into the reason code and the message.
  { [ "$(field prod idp-f2 reason)" = RejectedByHuman ] && [[ "$(field prod idp-f2 message)" == "$CAROL: "* ]]; } \
    || fail "F2 was not rejected by carol: $(field prod idp-f2 reason): $(field prod idp-f2 message)"
  runs prod payments "$from" || fail "a rejected F moved prod"
  pass "carol rolled F back and rejected it again from the dashboard, each as herself"

  # --- approve from the CLI, as carol, with the command's own fresh login ---
  out="$(mcp "$(kc_agent_token)" tools/call "{\"name\":\"pacto_delivery_operation_propose\",\"arguments\":{\"namespace\":\"prod\",\"stage\":\"payments\",\"to\":\"$F\",\"from\":\"$from\",\"name\":\"idp-f3\",\"reason\":\"phase 4, from the CLI\"}}")"
  eventually 30 held prod idp-f3 || fail "F was not held for the CLI: $out $(field prod idp-f3 phase) $(field prod idp-f3 message)"
  # The command runs in the node with a kubeconfig that holds no credentials
  # at all: the token it sends is the one the fresh login gives it.
  in_node pacto-e2e-approve -e KUBECONFIG=/kube/config -e SSL_CERT_FILE=/ca/tls.crt -e HOME=/tmp \
    -v "$IDP_DIR/resolv.conf:/etc/resolv.conf:ro" -v "$mcpkc:/kube/config:ro" -v "$IDP_DIR/tls.crt:/ca/tls.crt:ro" \
    "$DASH_IMG" delivery approve -n prod idp-f3 --note "from the terminal" --oidc-issuer-url "$DEX_ISSUER" \
    --oidc-extra-scopes audience:server:client_id:pacto-cli --oidc-redirect-port 8001
  local login_url=""
  asked() { login_url="$(docker logs pacto-e2e-approve 2>&1 | grep -o 'https://127.0.0.1:5556/dex/auth[^ ]*' | head -1)"; [ -n "$login_url" ]; }
  eventually 30 asked || { docker logs pacto-e2e-approve 2>&1 | tail; fail "pacto delivery approve did not ask for a login"; }
  grep -q "prompt=login" <<<"$login_url" && grep -q "max_age=0" <<<"$login_url" || fail "the CLI's login is not forced: $login_url"
  docker run --rm --network "container:${CLUSTER}-control-plane" -v "$OIDC_NODE_BIN:/oidclogin:ro" -v "$IDP_DIR/tls.crt:/ca/tls.crt:ro" \
    --entrypoint /oidclogin "$DASH_IMG" open -url "$login_url" -user carol@example.com -password password -ca /ca/tls.crt >/dev/null \
    || fail "carol could not log in for the CLI"
  [ "$(docker wait pacto-e2e-approve)" = 0 ] || { docker logs pacto-e2e-approve 2>&1 | tail; fail "pacto delivery approve failed"; }
  approval="$(docker logs pacto-e2e-approve 2>/dev/null | sed -n 's/^recorded approve on prod\/idp-f3 as Approval //p')"
  kubectl -n prod get approval "$approval" -o json | jq -e --arg u "$CAROL" '.spec.by == $u and .spec.note == "from the terminal"' >/dev/null \
    || fail "the CLI's Approval is not carol's: $(docker logs pacto-e2e-approve 2>&1 | tail -3)"
  wait_concluded prod idp-f3 Succeeded/Verified
  pass "carol approved from the CLI after the command's own fresh login; F shipped"

}
