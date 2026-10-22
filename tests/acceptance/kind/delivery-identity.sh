#!/usr/bin/env bash
# Phase 4 of the delivery kind acceptance ("dashboard and identity"), sourced by
# delivery.sh. Sourced, never executed directly.
#
# The cluster trusts two identity providers through structured
# authentication: Dex for people (the dashboard signs them in) and Keycloak for
# agents reaching Pacto's MCP server over HTTP (it supports RFC 8693 token
# exchange). Both map auth_time to userInfo.extra["pacto.dev/auth-time"], but
# only for tokens issued to the interactive clients (azp pacto-dashboard or
# pacto-cli), so client-credentials and exchanged tokens never carry it.
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
#   - a decision needs a fresh login; the promoter ignores an approval whose
#     login is older than 15 minutes; a token without the interactive azp gets
#     no auth-time and admission refuses its approval
#   - only the named team may approve; the promoter ignores anyone else
#   - an agent reaches Pacto over HTTP as itself (token exchange) and cannot
#     approve through the dashboard, the MCP server or the apiserver
#   - the dashboard's ServiceAccount can do nothing in delivery

IDP_DIR="$(mktemp -d)"
DEX_IMG="ghcr.io/dexidp/dex@sha256:0ae7ade1bdf1c83f7f7ccb1935912ac4c116e3e8ecc7e7503b30bab77e7c896c"
KC_IMG="quay.io/keycloak/keycloak@sha256:9409c59bdfb65dbffa20b11e6f18b8abb9281d480c7ca402f51ed3d5977e6007"
DEX_ISSUER="https://127.0.0.1:5556/dex"
KC_ISSUER="https://127.0.0.1:8443/realms/pacto"
DASH_URL="http://127.0.0.1:3300"
MCP_URL="http://127.0.0.1:8585"
DASH_SECRET="dashboard-secret"
IDP_FIXTURES="$(dirname "${BASH_SOURCE[0]}")/fixtures/identity"
IDP_CONTAINERS=(pacto-e2e-dex pacto-e2e-keycloak pacto-e2e-dashboard pacto-e2e-mcp)

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
    - key: pacto.dev/auth-time
      valueExpression: "has(claims.auth_time) && has(claims.azp) && claims.azp in ['pacto-dashboard', 'pacto-cli'] ? string(int(claims.auth_time)) : ''"
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

# identity_up starts Dex and Keycloak and waits until the apiserver trusts
# both: a token from each authenticates.
identity_up() {
  cp "$IDP_FIXTURES/dex.yaml" "$IDP_FIXTURES/pacto-realm.json" "$IDP_DIR/"
  chmod 644 "$IDP_DIR/dex.yaml" "$IDP_DIR/pacto-realm.json"
  in_node pacto-e2e-dex -e DEX_SESSIONS_ENABLED=true -v "$IDP_DIR:/etc/dex:ro" "$DEX_IMG" dex serve /etc/dex/dex.yaml
  in_node pacto-e2e-keycloak -e KC_BOOTSTRAP_ADMIN_USERNAME=admin -e KC_BOOTSTRAP_ADMIN_PASSWORD=admin \
    -v "$IDP_DIR:/opt/keycloak/data/import:ro" -v "$IDP_DIR:/etc/x509:ro" "$KC_IMG" \
    start-dev --import-realm --http-enabled=false --https-port=8443 --http-management-port=9100 \
    --https-certificate-file=/etc/x509/tls.crt --https-certificate-key-file=/etc/x509/tls.key --hostname="https://127.0.0.1:8443"
  OIDC_BIN="$(mktemp -d)/oidclogin"
  ( cd "$_PACTO_ROOT" && go build -o "$OIDC_BIN" ./tests/acceptance/kind/oidclogin ) || fail "building the login helper failed"
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

# identity_login_stale signs carol in at the start of the run. Phase 4 runs
# long after, and her login is then older than the promoter's 15 minutes.
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

identity_scenario() {
  section "phase 4: the dashboard and identity"
  # --- structured authentication ---
  local alice; alice="$(dex_token alice)"
  local who; who="$(whoami_json "$alice")"
  jq -e --arg iss "$DEX_ISSUER" '.status.userInfo.username | startswith($iss + "#")' <<<"$who" >/dev/null \
    || fail "a Dex user is not issuer#sub: $who"
  jq -e '.status.userInfo.groups | index("team-payments")' <<<"$who" >/dev/null || fail "alice's groups are not mapped: $who"
  jq -e '.status.userInfo.extra["pacto.dev/auth-time"][0] | test("^[0-9]+$")' <<<"$who" >/dev/null \
    || fail "an interactive login has no auth-time: $who"
  pass "a dashboard login authenticates as issuer#sub with its groups and its auth-time"
  local noazp; noazp="$(dex_token alice "openid profile groups")"
  who="$(whoami_json "$noazp")"
  jq -e '.status.userInfo.username != null and (.status.userInfo.extra["pacto.dev/auth-time"] == null)' <<<"$who" >/dev/null \
    || fail "a token without the interactive azp got an auth-time: $who"
  local agent; agent="$(kc_exchange "$(kc_agent_token)")"
  who="$(whoami_json "$agent")"
  jq -e '(.status.userInfo.groups | index("pacto:agents")) and (.status.userInfo.extra["pacto.dev/auth-time"] == null)' <<<"$who" >/dev/null \
    || fail "an exchanged agent token is not an agent without auth-time: $who"
  local AGENT_USER; AGENT_USER="$(jq -r .status.userInfo.username <<<"$who")"
  pass "a token without azp, and an exchanged agent token, authenticate with no auth-time"

  # --- identities and rights ---
  kubectl create clusterrolebinding delivery-oidc-approvers --clusterrole=pacto-delivery-approver \
    --group=team-checkout --group=team-payments --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  kubectl create clusterrolebinding delivery-oidc-agents --clusterrole=pacto-delivery-agent \
    --group=pacto:agents --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  local DSA="system:serviceaccount:${NS}:pacto-dashboard"
  # Only a plain "no" passes: a kubectl error prints nothing on stdout. The
  # auth-time extra is a subresource of userextras, as RBAC grants it.
  local v ans
  for v in "create approvals.delivery.pacto.trianalab.io -A" "create operations.delivery.pacto.trianalab.io -A" \
    "list stages.delivery.pacto.trianalab.io -A" "impersonate users" "impersonate userextras --subresource=pacto.dev/auth-time"; do
    # shellcheck disable=SC2086
    ans="$(kubectl auth can-i $v --as "$DSA" 2>/dev/null || true)"
    [[ "$ans" == no* ]] || fail "the dashboard's ServiceAccount may $v, or kubectl could not say: '$ans'"
  done
  pass "the dashboard's ServiceAccount can do nothing in delivery and cannot impersonate"
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
    --oidc-redirect-url "$DASH_URL/auth/callback" --oidc-extra-scopes audience:server:client_id:pacto-cli
  in_node pacto-e2e-mcp -e KUBECONFIG=/kube/config -e SSL_CERT_FILE=/ca/tls.crt -e HOME=/tmp -v "$IDP_DIR/resolv.conf:/etc/resolv.conf:ro" \
    -e PACTO_MCP_EXCHANGE_CLIENT_SECRET=mcp-secret -v "$mcpkc:/kube/config:ro" -v "$IDP_DIR/tls.crt:/ca/tls.crt:ro" \
    "$DASH_IMG" mcp --delivery -t http --host 0.0.0.0 --port 8585 --auth-issuer "$KC_ISSUER" \
    --resource "$MCP_URL/mcp" --audience pacto-mcp --exchange-client-id pacto-mcp
  dash_up() { curl -sf "$DASH_URL/health" >/dev/null; }
  eventually 40 dash_up || { docker logs pacto-e2e-dashboard 2>&1 | tail; fail "the dashboard did not start"; }
  mcp_up() { [ "$(curl -s -o /dev/null -w '%{http_code}' "$MCP_URL/mcp")" = 401 ]; }
  eventually 40 mcp_up || { docker logs pacto-e2e-mcp 2>&1 | tail; fail "the MCP server did not start"; }

  local caps; caps="$(curl -s "$DASH_URL/api/capabilities")"
  jq -e '.delivery == true and .auth.mode == "shared" and .auth.login == true and .auth.signedIn == false' <<<"$caps" >/dev/null \
    || fail "capabilities: $caps"
  local out; out="$(dash "" GET /api/delivery/inbox)"
  [ "${out%% *}" = 401 ] && grep -q LoginRequired <<<"$out" || fail "delivery without sign-in: $out"
  pass "delivery is offered where it is installed, and only to people who signed in"

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
  out="$(dash "$carol" POST /api/delivery/operations '{"namespace":"prod","stage":"payments","type":"pacto.dev/unfreeze","name":"idp-unfreeze","reason":"phase 4"}' "$cs")"
  [ "${out%% *}" = 201 ] || fail "carol's unfreeze: $out"
  eventually 40 concluded prod idp-unfreeze || fail "carol's unfreeze never concluded: $(field prod idp-unfreeze message)"
  [[ "$(outcome prod idp-unfreeze)" == Succeeded/* ]] || fail "carol's unfreeze concluded $(outcome prod idp-unfreeze)"
  [ "$(kubectl -n prod get stage payments -o jsonpath='{.status.frozen}')" != true ] || fail "prod is still frozen"
  [ "$(kubectl -n prod get operation idp-unfreeze -o jsonpath='{.spec.actor.user}')" = "$CAROL" ] || fail "the unfreeze is not carol's"
  pass "propose, cancel and unfreeze from the dashboard run as carol, and the audit log shows her"

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
  out="$(dash "$carol" POST /api/delivery/operations/prod/idp-f/decision '{"decision":"approve"}' "$cs")"
  [ "${out%% *}" = 401 ] && grep -q FreshLoginRequired <<<"$out" || fail "a decision without a fresh login: $out"
  out="$(dash "$carol" POST /api/delivery/operations/prod/idp-f/decision '{"decision":"approve"}')"
  [ "${out%% *}" = 403 ] || fail "a decision without the CSRF header: $out"
  out="$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "Cookie: __Host-pacto-session=$carol" -H "X-Pacto-CSRF: $cs" \
    -H 'Content-Type: application/json' -d '{"decision":"approve"}' "$DASH_URL/api/delivery/operations/prod/idp-f/decision")"
  [ "$out" = 403 ] || fail "a decision without an Origin: $out"
  pass "a decision needs a fresh login, the CSRF header and the dashboard's own origin"

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
  authTime: "$(jq -r '.status.userInfo.extra["pacto.dev/auth-time"][0] // ""' <<<"$w")"
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

  # A stale login: carol's first login, now over 15 minutes old.
  local wait=$((STALE_AT + 16 * 60 - $(date +%s)))
  if [ "$wait" -gt 0 ]; then
    echo "  waiting ${wait}s for carol's first login to pass 15 minutes"
    local slept=$SECONDS; sleep "$wait"; _timed_wait sleep "$slept" "carol's first login goes stale"
  fi
  approval_as "$STALE_TOKEN" idp-stale >/dev/null || fail "carol's stale Approval was refused by admission (its auth-time matches)"
  always 5 still_held || fail "the promoter took an approval whose login is over 15 minutes old"
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
  out="$(dash "$fresh" POST /api/delivery/operations "{\"namespace\":\"prod\",\"stage\":\"payments\",\"type\":\"pacto.dev/rollback\",\"undoes\":\"idp-f\",\"name\":\"idp-rollback\",\"reason\":\"phase 4\"}" "$(csrf_of "$fresh")")"
  [ "${out%% *}" = 201 ] || fail "carol's rollback: $out"
  [ "$(kubectl -n prod get operation idp-rollback -o jsonpath='{.spec.actor.user} {.spec.params.undoes}')" = "$CAROL $uid" ] \
    || fail "the rollback is not carol's undoing of F: $(kubectl -n prod get operation idp-rollback -o jsonpath='{.spec}')"
  wait_concluded prod idp-rollback Succeeded/Verified
  has_pointer prod "$from" || fail "prod is not back on the version before F"
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
  has_pointer prod "$from" || fail "a rejected F moved prod"
  pass "carol rolled F back and rejected it again from the dashboard, each as herself"

}
