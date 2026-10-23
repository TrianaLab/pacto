#!/usr/bin/env bash
# The Kubernetes delivery demo: "the payments story" (examples/story) played on
# a real cluster, where everything is real: the registry, Flux, the promoter,
# the recorder, Mira, the node agent, Dex and Keycloak sign-in, the dashboard
# and remote MCP.
#
#   up       create the cluster and install everything; payments 2.3.0,
#            checkout 1.8.0 (1.9.0 waiting in test) and orders 1.4.0 run.
#            fraud is a plain folder, not pactified yet.
#   story    play the story: pactify fraud, then B1 to B5, checking every beat
#            against examples/story; it stops with payments 3.0.0 held.
#   browser  B6, then B7 to B9 in Chromium with the cluster checked between
#            beats, then History; it runs once per cluster.
#   status   what runs where.
#   logs     diagnostics of the cluster.
#   down     delete the cluster and the containers beside it.
#   all      up, story, browser and down (CI's nightly run).
#
# Keys and kubeconfigs live in tmp/delivery-demo/ (ignored by git), so every
# subcommand finds them. Host ports 3300, 8585, 5556, 8443 and 5602 must be
# free, so the demo cannot run beside delivery.sh.
#
# What is generated rather than real: the payments workload is a stand-in
# (podinfo), its telemetry comes from a span generator in the cluster, and the
# call payments 2.4.2 makes to fraud comes from a sidecar in that version. The
# sign-in providers are the demo's own Dex and Keycloak.
set -euo pipefail
CMD="${1:-help}"
CLUSTER=pacto-delivery-demo
NS=pacto-system
DELIVERY_NS=pacto-delivery
REG_HOST="pacto-registry.${NS}.svc.cluster.local:5000"
LOCAL_REG_PORT=5602
FLUX_VERSION=v2.9.5
FLUX_INSTALL_SHA256=cc3dcd743af16215838b6937e1fce83745bf24c0dcc6c59737c59df15429caaf
FLUX_IMAGES=(ghcr.io/fluxcd/source-controller:v1.9.5@sha256:6f20d232d596a758c923d2861f23511718fc303b8a2e36a1434a7c736b9f4268
  ghcr.io/fluxcd/kustomize-controller:v1.9.5@sha256:a3a955eb2bc432c2eaa94d2d3714e3beae7fdf17586fd23aadf71ab597ac3339)
MIRA_IMG=ghcr.io/trianalab/mira:0.4.3@sha256:2fc95e0d4c8ffa38aa2de1a2389c98eb83e020ab7a1719ca27db90a62794fbcc
PODINFO_IMG=ghcr.io/stefanprodan/podinfo:6.7.1@sha256:862ca45e61b32392f7941a1bdfdbe5ff8b6899070135f1bdca1c287d0057fc94
PYTHON_IMG=python:3.13-alpine@sha256:bf12ecc0bea3f171d55145dd02c78b5eeb09f30ee2ac9cc2b4c9e4e8f5e30741
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=tests/acceptance/kind/lib.sh
source "$HERE/lib.sh"
# shellcheck source=tests/acceptance/kind/delivery-identity.sh
source "$HERE/delivery-identity.sh"
ROOT="$_PACTO_ROOT"
STORY="$ROOT/examples/story"
DEMO="$ROOT/tmp/delivery-demo"
KEYS="$DEMO/keys" WORK="$DEMO/work" BIN="$DEMO/bin"
PACTO="$BIN/pacto" OIDC_BIN="$BIN/oidclogin"
AGENT_KC="$DEMO/pay-agent.kubeconfig" ADMIN_KC="$DEMO/admin.kubeconfig"
# The stable directory replaces delivery-identity.sh's temporary one, and the
# demo's Dex (carol on team orders, pay-agent) replaces the delivery run's.
IDP_DIR="$DEMO/idp" IDP_FIXTURES="$HERE/fixtures/delivery-demo"
# shellcheck disable=SC2034  # read by lib.sh
KIND_POLL=2
curl() { command curl --connect-timeout 5 --max-time 30 "$@"; }

mkdirs() {
  mkdir -p "$KEYS" "$WORK" "$BIN" "$IDP_DIR"
  # The whole directory ignores itself: nothing in it belongs in git.
  echo '*' > "$DEMO/.gitignore"
}
admin() { [ -s "$ADMIN_KC" ] || { echo "no demo cluster: run 'make e2e-delivery-demo-up' first" >&2; exit 1; }; export KUBECONFIG="$ADMIN_KC"; }
# reg CMD... — a host command that reaches the in-cluster registry by its
# in-cluster name, through the port-forward (plain HTTP goes through HTTP_PROXY).
reg() { HTTP_PROXY="http://127.0.0.1:${LOCAL_REG_PORT}" PACTO_INSECURE_REGISTRIES="$REG_HOST" COSIGN_PASSWORD="" "$@"; }
registry_pf() { PF_PIDS+=" $(pf "$LOCAL_REG_PORT" svc/pacto-registry 5000)"; }
PF_PIDS=""
stop_pf() { local p; for p in $PF_PIDS; do kill "$p" 2>/dev/null || true; done; PF_PIDS=""; }

# --- up ----------------------------------------------------------------------

build_images() {
  local logs; logs="$(mktemp -d)"
  build_operator_images "$OP_IMG" "$DASH_IMG" "$VER" > "$logs/operator.log" 2>&1 &
  local op=$!
  docker build --load -f "$ROOT/integrations/kubernetes/Dockerfile" --build-arg VERSION="$VER" \
    --target delivery --build-arg CMD=cmd/delivery -t "$DL_IMG" "$ROOT" > "$logs/delivery.log" 2>&1 &
  local dl=$!
  wait "$op" || { cat "$logs/operator.log" >&2; fail "the dashboard or operator image did not build"; }
  wait "$dl" || { cat "$logs/delivery.log" >&2; fail "the pacto-delivery image did not build"; }
}

# kind_config — delivery-identity.sh's cluster (sign-in through Dex and
# Keycloak, ports on the host), plus a containerd that reads per-registry
# config, so the nodes can pull the fraud image pacto publish pushes.
kind_config() {
  local cfg; cfg="$(identity_prepare)"
  cat >> "$cfg" <<'YAML'
containerdConfigPatches:
- |-
  [plugins."io.containerd.grpc.v1.cri".registry]
    config_path = "/etc/containerd/certs.d"
YAML
  echo "$cfg"
}

# node_pulls_from_registry — the nodes do not resolve cluster names, so the
# in-cluster registry name is mapped to its ClusterIP for containerd.
node_pulls_from_registry() {
  local ip node dir="/etc/containerd/certs.d/$REG_HOST"
  ip="$(kubectl -n "$NS" get svc pacto-registry -o jsonpath='{.spec.clusterIP}')"
  for node in $(kind get nodes --name "$CLUSTER"); do
    docker exec "$node" mkdir -p "$dir"
    printf 'server = "http://%s:5000"\n[host."http://%s:5000"]\n  capabilities = ["pull", "resolve"]\n' "$ip" "$ip" \
      | docker exec -i "$node" tee "$dir/hosts.toml" >/dev/null
  done
}

idp_start() {
  cp "$IDP_FIXTURES/dex.yaml" "$IDP_FIXTURES/pacto-realm.json" "$IDP_DIR/"
  chmod 644 "$IDP_DIR/dex.yaml" "$IDP_DIR/pacto-realm.json"
  in_node pacto-e2e-dex -e DEX_SESSIONS_ENABLED=true -v "$IDP_DIR:/etc/dex:ro" "$DEX_IMG" dex serve /etc/dex/dex.yaml
  in_node pacto-e2e-keycloak -e KC_BOOTSTRAP_ADMIN_USERNAME=admin -e KC_BOOTSTRAP_ADMIN_PASSWORD=admin \
    -v "$IDP_DIR:/opt/keycloak/data/import:ro" -v "$IDP_DIR:/etc/x509:ro" "$KC_IMG" \
    start-dev --import-realm --http-enabled=false --https-port=8443 --http-management-port=9100 \
    --https-certificate-file=/etc/x509/tls.crt --https-certificate-key-file=/etc/x509/tls.key --hostname="https://127.0.0.1:8443"
}

# servers — the dashboard and the remote MCP server, in the node's network
# namespace like the providers, so one address works for every party.
servers() {
  local dashkc mcpkc dns
  # shellcheck disable=SC2034  # read by kubeconfig_for
  CA="$(kubectl config view --raw --minify -o jsonpath='{.clusters[0].cluster.certificate-authority-data}')"
  dashkc="$(kubeconfig_for "$(kubectl -n "$NS" create token pacto-dashboard --duration 24h)")"
  mcpkc="$(kubeconfig_for "")"
  dns="$(kubectl -n kube-system get svc kube-dns -o jsonpath='{.spec.clusterIP}')"
  printf 'search svc.cluster.local cluster.local\nnameserver %s\noptions ndots:5\n' "$dns" > "$IDP_DIR/resolv.conf"
  cp "$KEYS/recorder.pub" "$IDP_DIR/recorder.pub"; chmod 644 "$IDP_DIR/recorder.pub"
  in_node pacto-e2e-dashboard -e KUBECONFIG=/kube/config -e SSL_CERT_FILE=/ca/tls.crt -e HOME=/tmp -v "$IDP_DIR/resolv.conf:/etc/resolv.conf:ro" \
    -e PACTO_DASHBOARD_OIDC_CLIENT_SECRET="$DASH_SECRET" -e PACTO_INSECURE_REGISTRIES="$REG_HOST" -v "$dashkc:/kube/config:ro" \
    -v "$IDP_DIR/tls.crt:/ca/tls.crt:ro" -v "$IDP_DIR/recorder.pub:/trust/recorder.pub:ro" \
    "$DASH_IMG" dashboard --host 0.0.0.0 --port 3300 --oidc-issuer-url "$DEX_ISSUER" --oidc-client-id pacto-dashboard \
    --oidc-redirect-url "$DASH_URL/auth/callback" --oidc-extra-scopes audience:server:client_id:pacto-cli \
    --team-group-prefix team- --delivery-trust /trust/recorder.pub
  in_node pacto-e2e-mcp -e KUBECONFIG=/kube/config -e SSL_CERT_FILE=/ca/tls.crt -e HOME=/tmp -v "$IDP_DIR/resolv.conf:/etc/resolv.conf:ro" \
    -e PACTO_MCP_EXCHANGE_CLIENT_SECRET=mcp-secret -e PACTO_INSECURE_REGISTRIES="$REG_HOST" -v "$mcpkc:/kube/config:ro" \
    -v "$IDP_DIR/tls.crt:/ca/tls.crt:ro" -v "$IDP_DIR/recorder.pub:/trust/recorder.pub:ro" \
    "$DASH_IMG" mcp --delivery -t http --host 0.0.0.0 --port 8585 --auth-issuer "$KC_ISSUER" \
    --resource "$MCP_URL/mcp" --audience pacto-mcp --exchange-client-id pacto-mcp --delivery-trust /trust/recorder.pub
  dash_up() { curl -sf "$DASH_URL/health" >/dev/null; }
  eventually 40 dash_up || { docker logs pacto-e2e-dashboard 2>&1 | tail; fail "the dashboard did not start"; }
  mcp_up() { [ "$(curl -s -o /dev/null -w '%{http_code}' "$MCP_URL/mcp")" = 401 ]; }
  eventually 40 mcp_up || { docker logs pacto-e2e-mcp 2>&1 | tail; fail "the MCP server did not start"; }
}

# span_generator — payments spans for Mira, in the cluster so the demo
# survives the shell. Each environment's spans follow the mode the span-modes
# ConfigMap sets for the payments version running there ("<env>.<version>":
# ok, errors or silent; ok when unset).
span_generator() {
  kubectl -n "$NS" create configmap span-modes --from-literal=prod.3.0.0=silent --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  kubectl -n "$NS" create configmap span-generator --from-file=gen.py=/dev/stdin --dry-run=client -o yaml <<'PY' | kubectl apply -f - >/dev/null
import json, os, ssl, time, urllib.request
SA = "/var/run/secrets/kubernetes.io/serviceaccount"
API = "https://kubernetes.default.svc"
ctx = ssl.create_default_context(cafile=SA + "/ca.crt")
def get(path):
    token = open(SA + "/token").read()
    req = urllib.request.Request(API + path, headers={"Authorization": "Bearer " + token})
    return json.load(urllib.request.urlopen(req, context=ctx, timeout=5))
while True:
    time.sleep(2)
    try:
        modes = get("/api/v1/namespaces/pacto-system/configmaps/span-modes").get("data") or {}
    except Exception:
        continue
    for env in ("test", "prod"):
        try:
            labels = get(f"/apis/apps/v1/namespaces/{env}/deployments/payments")["spec"]["template"]["metadata"]["labels"]
        except Exception:
            continue
        mode = modes.get(env + "." + labels.get("app.kubernetes.io/version", ""), "ok")
        n, errors, now = (0 if mode == "silent" else 10), (5 if mode == "errors" else 0), time.time_ns()
        if n == 0:
            continue
        attrs = [{"key": "service.name", "value": {"stringValue": "payments"}},
                 {"key": "deployment.environment", "value": {"stringValue": env}}]
        spans = [{"traceId": os.urandom(16).hex(), "spanId": os.urandom(8).hex(), "name": "POST /payments", "kind": 2,
                  "startTimeUnixNano": str(now - i * 1000), "endTimeUnixNano": str(now - i * 1000 + 2_000_000),
                  "status": {"code": 2 if i < errors else 1}} for i in range(n)]
        body = json.dumps({"resourceSpans": [{"resource": {"attributes": attrs}, "scopeSpans": [{"spans": spans}]}]}).encode()
        try:
            urllib.request.urlopen(urllib.request.Request("http://mira:4318/v1/traces", data=body,
                                   headers={"content-type": "application/json"}), timeout=5)
        except Exception:
            pass
PY
  kubectl apply -f - >/dev/null <<YAML
apiVersion: v1
kind: ServiceAccount
metadata: {name: span-generator, namespace: $NS}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata: {name: demo-span-generator}
rules:
- {apiGroups: [apps], resources: [deployments], verbs: [get]}
- {apiGroups: [""], resources: [configmaps], verbs: [get]}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata: {name: demo-span-generator}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: demo-span-generator}
subjects: [{kind: ServiceAccount, name: span-generator, namespace: $NS}]
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: span-generator, namespace: $NS}
spec:
  replicas: 1
  selector: {matchLabels: {app: span-generator}}
  template:
    metadata: {labels: {app: span-generator}}
    spec:
      serviceAccountName: span-generator
      containers:
      - {name: gen, image: ${PYTHON_IMG%@*}, imagePullPolicy: Never, command: [python, -u, /gen/gen.py], volumeMounts: [{name: gen, mountPath: /gen}]}
      volumes: [{name: gen, configMap: {name: span-generator}}]
YAML
}
mode() { kubectl -n "$NS" patch configmap span-modes --type merge -p "{\"data\":{\"$1\":\"$2\"}}" >/dev/null; } # ENV.VERSION MODE

mira() {
  kubectl -n "$NS" apply -f - >/dev/null <<YAML
apiVersion: apps/v1
kind: Deployment
metadata: {name: mira}
spec:
  replicas: 1
  selector: {matchLabels: {app: mira}}
  template:
    metadata: {labels: {app: mira}}
    spec:
      containers:
      - name: mira
        image: ${MIRA_IMG%@*}
        imagePullPolicy: Never
        ports: [{containerPort: 4318}]
        readinessProbe: {httpGet: {path: /readyz, port: 4318}, periodSeconds: 2}
        volumeMounts: [{name: data, mountPath: /data}]
      volumes: [{name: data, emptyDir: {}}]
---
apiVersion: v1
kind: Service
metadata: {name: mira}
spec: {selector: {app: mira}, ports: [{name: http, port: 4318, targetPort: 4318}]}
YAML
}

# agent_kubeconfig — pay-agent, a ServiceAccount, by token: how the agent's
# own tools reach the cluster.
agent_kubeconfig() {
  local server ca tok
  server="$(kubectl config view --raw --minify -o jsonpath='{.clusters[0].cluster.server}')"
  ca="$(kubectl config view --raw --minify -o jsonpath='{.clusters[0].cluster.certificate-authority-data}')"
  tok="$(kubectl -n agents create token pay-agent --duration=24h)"
  cat > "$AGENT_KC" <<YAML
apiVersion: v1
kind: Config
clusters: [{name: demo, cluster: {server: "$server", certificate-authority-data: "$ca"}}]
users: [{name: pay-agent, user: {token: "$tok"}}]
contexts: [{name: pay-agent, context: {cluster: demo, user: pay-agent}}]
current-context: pay-agent
YAML
}

cmd_up() {
  command -v oras >/dev/null 2>&1 || fail "oras is required"
  command -v cosign >/dev/null 2>&1 || fail "cosign is required: it makes the publisher's key"
  command -v jq >/dev/null 2>&1 || fail "jq is required"
  command -v openssl >/dev/null 2>&1 || fail "openssl is required: it makes the recorder's key"
  command -v timeout >/dev/null 2>&1 || fail "timeout is required (GNU coreutils; on macOS: brew install coreutils)"
  if kind get clusters 2>/dev/null | grep -qx "$CLUSTER"; then fail "the demo cluster is already up; 'make e2e-delivery-demo-down' first"; fi
  rm -rf "$DEMO"; mkdirs
  VER="$(release_version kubernetes)"
  OP_REPO="localhost:5001/pacto-operator/pacto-controller" OP_IMG="localhost:5001/pacto-operator/pacto-controller:${VER}"
  DL_REPO="localhost:5001/pacto-delivery" DL_IMG="localhost:5001/pacto-delivery:${VER}"
  DASH_IMG="localhost:5001/pacto-dashboard:$(release_version core)"
  echo "DASH_IMG=$DASH_IMG" > "$DEMO/images.env"

  section "create the cluster and build the images while the rest is pulled"
  local cfg; cfg="$(kind_config)"
  cluster_start_bg "$cfg"
  infra "build the Pacto images" build_images & local build=$!
  local flux; flux="$(mktemp)"
  infra "download the Flux $FLUX_VERSION install manifest" \
    curl -fsSL --retry 2 --max-time 60 -o "$flux" "https://github.com/fluxcd/flux2/releases/download/${FLUX_VERSION}/install.yaml"
  [ "$(shasum -a 256 "$flux" | cut -d' ' -f1)" = "$FLUX_INSTALL_SHA256" ] || fail "the Flux install manifest changed upstream"
  local img
  for img in "$DEX_IMG" "$KC_IMG"; do infra "pull $img" docker pull -q "$img" >/dev/null; done
  for img in "${FLUX_IMAGES[@]}" "$MIRA_IMG" "$PODINFO_IMG" "$PYTHON_IMG"; do pull_pinned "$img"; done
  go build -o "$PACTO" "$ROOT/cmd/pacto" || fail "pacto did not build"
  ( cd "$ROOT" && go build -o "$OIDC_BIN" ./tests/acceptance/kind/oidclogin ) || fail "the login helper did not build"
  local op_chart dl_chart
  op_chart="$(package_chart "$PACTO_CHART")"
  dl_chart="$(package_chart "$ROOT/integrations/kubernetes/charts/pacto-delivery")"
  wait "$build" || exit 1

  timed "wait for the cluster" ensure_cluster "$cfg"
  cp "$KUBECONFIG" "$ADMIN_KC"; export KUBECONFIG="$ADMIN_KC"
  infra "load the images into the cluster" load_images "$OP_IMG" "$DL_IMG" "$DASH_IMG" "${FLUX_IMAGES[@]%@*}" \
    "${MIRA_IMG%@*}" "${PODINFO_IMG%@*}" "${PYTHON_IMG%@*}"
  local n
  for n in "$NS" "$DELIVERY_NS" test prod agents; do kubectl create namespace "$n" --dry-run=client -o yaml | kubectl apply -f - >/dev/null; done
  timed "Dex for people, Keycloak for agents" idp_start

  section "zot, the Pacto operator with its node agent, Flux, the delivery chart with the recorder, Mira"
  install_registry
  node_pulls_from_registry
  openssl genpkey -algorithm ed25519 -out "$KEYS/recorder.pem" 2>/dev/null || fail "openssl could not make the recorder's key"
  openssl pkey -in "$KEYS/recorder.pem" -pubout -out "$KEYS/recorder.pub"
  kubectl -n "$DELIVERY_NS" create secret generic recorder-key --from-file=key.pem="$KEYS/recorder.pem" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  (cd "$KEYS" && COSIGN_PASSWORD="" cosign generate-key-pair >/dev/null 2>&1) || fail "cosign could not make the publisher's key"
  for n in test prod; do
    kubectl -n "$n" create secret generic cosign-pub --from-file=cosign.pub="$KEYS/cosign.pub" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  done
  helm upgrade --install pacto-operator "$op_chart" -n "$NS" --set nodeAgent.enabled=true --set nodeAgent.publishInterval=5s \
    --set image.repository="$OP_REPO" --set image.tag="$VER" --set image.pullPolicy=Never \
    --set "insecureRegistries[0]=$REG_HOST" --wait --timeout 240s >/dev/null || fail "the pacto-operator chart did not roll out" &
  local op_helm=$!
  infra "install Flux" kubectl apply --server-side -f "$flux" >/dev/null
  kubectl -n flux-system scale deploy helm-controller --replicas=0 >/dev/null 2>&1 || true
  helm upgrade --install pacto-delivery "$dl_chart" -n "$DELIVERY_NS" \
    --set image.repository="$DL_REPO" --set image.tag="$VER" --set image.pullPolicy=Never \
    --set "insecureRegistries[0]=$REG_HOST" --set promoter.poll=2s --set identities.teamGroupPrefix=team- \
    --set-json "gates.providers=[{\"name\":\"mira\",\"type\":\"mira\",\"url\":\"http://mira.${NS}.svc.cluster.local:4318\"}]" \
    --set recorder.enabled=true --set recorder.streamsRepository="$REG_HOST/pacto-delivery-streams" --set recorder.poll=5s \
    --set recorder.signing.ed25519.secretName=recorder-key --wait --timeout 240s >/dev/null || fail "the pacto-delivery chart did not roll out" &
  local dl_helm=$!
  mira
  infra "roll out Flux's source-controller" kubectl -n flux-system rollout status deploy/source-controller --timeout=240s
  infra "roll out Flux's kustomize-controller" kubectl -n flux-system rollout status deploy/kustomize-controller --timeout=240s
  infra "roll out Mira" kubectl -n "$NS" rollout status deploy/mira --timeout=120s >/dev/null
  wait "$op_helm" || exit 1
  wait "$dl_helm" || exit 1
  span_generator
  eventually 60 kubectl -n pacto-node-agent get ds pacto-node-agent >/dev/null 2>&1 || fail "the operator never created the node agent"
  kubectl -n pacto-node-agent rollout status ds/pacto-node-agent --timeout=180s >/dev/null || fail "the node agent did not roll out"
  kubectl -n "$NS" rollout status deploy/span-generator --timeout=120s >/dev/null || fail "the span generator did not roll out"
  pass "installed: registry, operator and node agent, Flux, promoter and recorder, Mira, span generator"

  section "who may do what: pay-agent proposes, team members approve"
  kubectl -n agents create serviceaccount pay-agent --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  kubectl create clusterrolebinding demo-agents --clusterrole=pacto-delivery-agent \
    --serviceaccount=agents:pay-agent --group=pacto:agents --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  kubectl create clusterrolebinding demo-approvers --clusterrole=pacto-delivery-approver \
    --group=team-payments --group=team-orders --group=team-checkout --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  agent_kubeconfig
  timed "wait for Dex and Keycloak" identity_up
  timed "the dashboard and the remote MCP server" servers

  section "the story's starting point"
  registry_pf
  bootstrap
  stop_pf
  pass "the demo is up"
  next_steps up
}

# --- the story's services ----------------------------------------------------

beats() { [ -s "$DEMO/beats.json" ] || ( cd "$ROOT" && go run ./tests/acceptance/kind/storybeats ) > "$DEMO/beats.json"; cat "$DEMO/beats.json"; }
expect() { beats | jq -c ".\"$1\".Expect.$2"; } # BEAT FIELD — what examples/story says, as JSON
says() { beats | jq -r ".\"$1\".Says"; }
# drift BEAT WHAT GOT WANT — a beat the cluster does not play as the story says.
drift() { fail "DRIFT in beat $1: $2: the cluster says $3, examples/story says $4"; }
same() { [ "$3" = "$4" ] || drift "$1" "$2" "$3" "$4"; } # BEAT WHAT GOT WANT
beat() { echo; echo "== $1: $(says "$1")"; echo "   because: $(beats | jq -r ".\"$1\".Why")"; }

# checkout_version SERVICE VERSION — the story's files for that version in the
# service's git working copy, with only the registry host rewritten to the
# in-cluster one, plus the Pacto resource that checks the running version
# (what pacto init --delivery writes beside a contract).
checkout_version() {
  local svc="$1" v="$2" dir="$WORK/$1"
  mkdir -p "$dir"
  [ -d "$dir/.git" ] || git -C "$dir" init -q
  find "$dir" -mindepth 1 -maxdepth 1 ! -name .git -exec rm -rf {} +
  cp -R "$STORY/contracts/$svc/$v/." "$dir/"
  cp "$KEYS/cosign.key" "$dir/cosign.key"
  perl -pi -e "s#registry\\.example/story#${REG_HOST}/story#g" "$dir/pacto.yaml"
  # The http interface payments declares is served by its Service's port 8080:
  # Kubernetes knowledge that lives on the Pacto resource, not the contract.
  local bind=""
  [ "$svc" = payments ] && bind=", serviceName: payments, interfaceBindings: [{interface: http, servicePort: 8080}]"
  cat > "$dir/deploy/pacto-resource.yaml" <<YAML
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: {name: $svc}
spec:
  # Re-checked every 30s, so the claim check sees the node agent's baseline
  # inside the verify window (the default is 5 minutes).
  checkIntervalSeconds: 30
  contractRef: {oci: "oci://${REG_HOST}/story/$svc:$v"}
  target: {workloadRef: {name: $svc, kind: Deployment}$bind}
YAML
  # payments 2.4.2 is "logging only" but calls fraud: a sidecar stands in for
  # the code that makes the call.
  if [ "$svc/$v" = payments/2.4.2 ]; then
    perl -0pi -e 's#(            - containerPort: 8080\n)#$1        - name: logger\n          image: '"${PODINFO_IMG%@*}"'\n          command: [sh, -c, "while true; do wget -q -O /dev/null --post-data= http://fraud:8080/score; sleep 2; done"]\n#' \
      "$dir/deploy/payments.yaml"
  fi
  git -C "$dir" add -A
  git -C "$dir" -c user.email=demo@example.com -c user.name=demo commit -qm "$svc $v" --allow-empty
}

# publish SERVICE VERSION — pacto publish, as the agent's tools run it; prints
# the deploy artifact's digest.
publish() {
  local out
  checkout_version "$1" "$2"
  out="$(cd "$WORK/$1" && reg "$PACTO" publish 2>&1)" || { echo "$out" >&2; fail "pacto publish $1 $2 failed"; }
  grep -oE "^Deploy: .*@sha256:[0-9a-f]{64}" <<<"$out" | sed 's/.*@//' | grep . || { echo "$out" >&2; fail "pacto publish $1 $2 named no deploy artifact"; }
}

# environment SERVICE ENV CANDIDATES AUTO [GATES] — what applies SERVICE in
# ENV and the Stage that decides which version runs there, in the shape
# pacto init --delivery writes (the registry speaks plain HTTP, hence insecure),
# plus a health check on the Deployment: Flux calls a version applied only once
# its rollout is done, so the verify gates never judge the old pods (HANDOFF).
environment() {
  local svc="$1" env="$2" verify='verify: {provider: cosign, secretRef: {name: cosign-pub}}'
  local url="oci://${REG_HOST}/story/deploy/$svc"
  {
    if [ "$3" = candidates ]; then
      cat <<YAML
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata: {name: $svc-candidates, namespace: $env}
spec: {interval: 5s, insecure: true, url: "$url", ref: {semver: ">=0.0.0"}, $verify}
---
YAML
      CANDS="{ociRepositoryRef: {name: $svc-candidates}}"
    else
      CANDS="{upstreamStage: {name: $svc, namespace: test}}"
    fi
    cat <<YAML
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata: {name: $svc, namespace: $env, labels: {delivery.pacto.trianalab.io/apply-ref: $svc}}
spec: {interval: 30s, insecure: true, url: "$url", $verify}
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata: {name: $svc, namespace: $env}
spec:
  {interval: 10m, sourceRef: {kind: OCIRepository, name: $svc}, path: ./, prune: true, targetNamespace: $env, timeout: 3m,
   healthChecks: [{apiVersion: apps/v1, kind: Deployment, name: $svc, namespace: $env}]}
---
apiVersion: delivery.pacto.trianalab.io/v1alpha1
kind: Stage
metadata: {name: $svc, namespace: $env}
spec:
  service: {name: $svc}
  environment: $env
  applyRef: {name: $svc}
  candidates: $CANDS
  auto: $4
  gates:
    verify:
      window: 3m
${5:-}
YAML
  } | kubectl apply -f - >/dev/null
}
# payments_gates ENV [RECHECK] — the payments Stages verify with Mira (the
# error ratio of ENV's spans) and the node agent's claim check.
payments_gates() {
  cat <<YAML
      ${2:+recheck: $2}
      checks:
      - name: health
        provider: mira
        params: {minSpans: "20", maxErrorRatio: "0.2", soak: "15s", where: '[{"attr": "deployment.environment", "eq": "$1"}]'}
      - {name: claims, provider: pacto.runtime}
YAML
}

# --- reading the cluster -----------------------------------------------------
ops() { kubectl -n "$1" get operations -o json; }
op_to() { # NS STAGE DIGEST [TYPE] — the operations moving STAGE to DIGEST
  ops "$1" | jq -r --arg s "$2" --arg d "$3" --arg t "${4:-pacto.dev/promote}" \
    '.items[] | select(.spec.stage == $s and .spec.params.to == $d and .spec.type == $t) | .metadata.name'
}
op_exists() { [ -n "$(op_to "$@")" ]; }
wait_op() { eventually 120 op_exists "$@" || fail "no ${4:-promote} of $2 to $3 appeared in $1"; op_to "$@" | head -1; }
field() { kubectl -n "$1" get operation "$2" -o jsonpath="{.status.$3}"; }
op_json() { kubectl -n "$1" get operation "$2" -o json; }
concluded() { [ "$(field "$1" "$2" phase)" = Concluded ]; }
outcome() { echo "$(field "$1" "$2" outcome)/$(field "$1" "$2" verification)"; }
held() { [ "$(field "$1" "$2" phase)" = Held ]; }
wait_concluded() { # NS OP WANT
  eventually 150 concluded "$1" "$2" || fail "$1/$2 never concluded: $(field "$1" "$2" phase) $(field "$1" "$2" reason): $(field "$1" "$2" message)"
  [ "$(outcome "$1" "$2")" = "$3" ] || fail "$1/$2 concluded $(outcome "$1" "$2"), want $3 ($(field "$1" "$2" reason): $(field "$1" "$2" message))"
}
pointer() { kubectl -n "$1" get stage "$2" -o jsonpath='{.status.pointer}'; }
runs() { [ "$(pointer "$1" "$2")" = "$3" ]; }
failed_on() { kubectl -n "$1" get stage "$2" -o json | jq -e --arg d "$3" '.status.failedDigests // [] | index($d)' >/dev/null; }
# offered SERVICE DIGEST — test verified it and offers it to prod.
offered() { kubectl -n test get stage "$1" -o json | jq -e --arg d "$2" '.status.offered // [] | index($d)' >/dev/null; }
published_in() { kubectl -n "$1" get stage "$2" -o json | jq -e --arg d "$3" '.status.published // [] | index($d)' >/dev/null; }
digest_of() { cat "$DEMO/digests/$1-$2"; } # SERVICE VERSION
remember() { mkdir -p "$DEMO/digests"; echo "$3" > "$DEMO/digests/$1-$2"; }
propose() { KUBECONFIG="$AGENT_KC" "$PACTO" delivery propose "$@" >/dev/null; } # as pay-agent
# ship_test SERVICE VERSION — publish it and wait until test verified it.
ship_test() {
  local d op; d="$(publish "$1" "$2")"; remember "$1" "$2" "$d"
  op="$(wait_op test "$1" "$d")"; wait_concluded test "$op" Succeeded/Verified
}
# ship_prod SERVICE VERSION — pay-agent promotes a verified version in prod.
ship_prod() {
  local d; d="$(digest_of "$1" "$2")"
  eventually 60 offered "$1" "$d" || fail "test never offered $1 $2 to prod"
  propose -n prod --stage "$1" --to "$d" --name "$1-${2//./-}" --reason "the demo's starting point"
  wait_concluded prod "$1-${2//./-}" Succeeded/Verified
}

bootstrap() {
  environment payments test candidates true "$(payments_gates test)"
  environment payments prod upstream false "$(payments_gates prod 10s)"
  environment checkout test candidates true
  environment checkout prod upstream false
  # orders ships to prod only: nothing in test depends on it.
  environment orders prod candidates false
  ship_test payments 2.3.0; ship_prod payments 2.3.0
  pass "payments 2.3.0 runs in test and prod"
  ship_test checkout 1.8.0; ship_prod checkout 1.8.0
  local d; d="$(publish orders 1.4.0)"; remember orders 1.4.0 "$d"
  eventually 60 published_in prod orders "$d" || fail "prod never offered orders 1.4.0"
  propose -n prod --stage orders --to "$d" --name orders-1-4-0 --reason "the demo's starting point"
  wait_concluded prod orders-1-4-0 Succeeded/Verified
  pass "checkout 1.8.0 and orders 1.4.0 run in prod, both accepting payments ^2"
  ship_test checkout 1.9.0
  pass "checkout 1.9.0 (accepts payments ^2 || ^3) is verified in test; prod waits for someone to promote it"
}

# --- story -------------------------------------------------------------------

# diffs BEAT OLD NEW — pacto diff on the story's contracts gives the beat's
# classification and required bump.
diffs() {
  local got
  got="$("$PACTO" diff --output-format json "$STORY/contracts/payments/$2" "$STORY/contracts/payments/$3" 2>/dev/null || true)"
  same "$1" "classification" "$(jq -r .classification <<<"$got")" "$(expect "$1" Classification | jq -r .)"
  same "$1" "required bump" "$(jq -r .requiredBump <<<"$got")" "$(expect "$1" RequiredBump | jq -r .)"
}
predicted() { # BEAT NS OP — the promoter's plan says what the story says
  planned() { [ -n "$(field "$2" "$3" plan.predicted)" ]; }
  eventually 30 planned "$@" || fail "$2/$3 has no plan: $(field "$2" "$3" phase) $(field "$2" "$3" message)"
  same "$1" "predicted" "$(field "$2" "$3" plan.predicted)" "$(expect "$1" Predicted | jq -r .)"
}

story_pactify() {
  beat pactify
  local dir="$WORK/fraud"
  rm -rf "$dir"; cp -R "$STORY/plain/fraud" "$dir"
  perl -pi -e "s#registry\\.example/story#${REG_HOST}/story#g" "$dir/k8s/deployment.yaml"
  echo "  \$ pacto init --delivery --owner risk"
  (cd "$dir" && "$PACTO" init --delivery --owner risk) | sed 's/^/    /'
  [ -s "$dir/delivery/test.yaml" ] || fail "pacto init --delivery wrote no Stage for test"
  # Two patches on what pacto init wrote (HANDOFF: init could take hints for
  # both): the story signs offline (no transparency log), as its fraud
  # contract says, and the demo registry speaks plain HTTP, so the
  # OCIRepositories init wrote get insecure: true once applied.
  perl -pi -e 's#^(    key: cosign\.key)$#$1\n    transparencyLog: false#' "$dir/pacto.yaml"
  cp "$KEYS/cosign.key" "$dir/"
  git -C "$dir" init -q && git -C "$dir" add -A && git -C "$dir" -c user.email=demo@example.com -c user.name=demo commit -qm "pactify fraud"
  local out d op
  out="$(cd "$dir" && reg "$PACTO" publish 2>&1)" || { echo "$out" >&2; fail "pacto publish fraud failed"; }
  d="$(grep -oE "^Deploy: .*@sha256:[0-9a-f]{64}" <<<"$out" | sed 's/.*@//')"
  remember fraud 1.0.0 "$d"
  kubectl apply -f "$dir/delivery/test.yaml" -f "$dir/delivery/prod.yaml" >/dev/null
  for e in test prod; do
    kubectl -n "$e" patch ocirepository fraud --type merge -p '{"spec":{"insecure":true}}' >/dev/null
  done
  kubectl -n test patch ocirepository fraud-candidates --type merge -p '{"spec":{"insecure":true}}' >/dev/null
  op="$(wait_op test fraud "$d")"
  wait_concluded test "$op" Succeeded/Verified
  predicted pactify test "$op"
  eventually 60 kubectl -n test get pacto fraud >/dev/null 2>&1 || fail "pacto init --delivery wrote no Pacto resource for fraud in test"
  pass "fraud is pactified: its contract, Pacto resource and Stages come from pacto init (patched for offline signing and the plain-HTTP registry), and 1.0.0 runs in test with nobody involved"
}

story_b1() {
  beat B1
  diffs B1 2.3.0 2.4.0
  ship_test payments 2.4.0
  local d; d="$(digest_of payments 2.4.0)"
  eventually 60 offered payments "$d" || fail "test never offered payments 2.4.0 to prod"
  propose -n prod --stage payments --to "$d" --name payments-2-4-0 --reason "B1: adds an endpoint"
  predicted B1 prod payments-2-4-0
  same B1 "a person asked" "$(field prod payments-2-4-0 plan.floor.decision)" "$(expect B1 Decision | jq -r .)"
  wait_concluded prod payments-2-4-0 Succeeded/Verified
  pass "payments 2.4.0 shipped to prod with nobody involved"
}

story_b2() {
  beat B2
  mode prod.2.4.0 errors
  echo "  the span generator now sends prod errors for payments 2.4.0 (generated traffic)"
  local rb=payments-2-4-0-rollback
  eventually 90 kubectl -n prod get operation "$rb" >/dev/null 2>&1 || fail "the failed health check raised no rollback"
  local cause; cause="$(op_json prod "$rb" | jq -c .spec.cause.verdict)"
  same B2 "the check after release" "$(jq -r .result <<<"$cause")" "$(expect B2 Verify | jq -r .)"
  [ "$(jq -r .gate <<<"$cause")" = health ] || fail "the rollback cites $(jq -r .gate <<<"$cause"), not the health check: $cause"
  jq -e '.citation.query | length > 0' <<<"$cause" >/dev/null || fail "the rollback does not cite its Mira query: $cause"
  predicted B2 prod "$rb"
  wait_concluded prod "$rb" Succeeded/Verified
  runs prod payments "$(digest_of payments 2.3.0)" || fail "prod is not back on payments 2.3.0"
  pass "the health check failed, cited its query, and payments rolled back to 2.3.0 on its own"
}

story_b3() {
  beat B3
  diffs B3 2.4.0 2.4.2
  local d op; d="$(publish payments 2.4.2)"; remember payments 2.4.2 "$d"
  op="$(wait_op test payments "$d")"
  eventually 120 failed_on test payments "$d" || fail "payments 2.4.2 never failed in test: $(field test "$op" phase) $(field test "$op" message)"
  local cause; cause="$(op_json test "${op}-rollback" | jq -c .spec.cause.verdict)"
  same B3 "the claim check" "$(jq -r .result <<<"$cause")" "$(expect B3 Verify | jq -r .)"
  grep -q "DEPENDENCY_UNDECLARED dependency/fraud (ebpf" <<<"$(jq -r '.citation["payments.findings"]' <<<"$cause")" \
    || fail "the rollback does not cite the call to fraud the node agent saw: $cause"
  predicted B3 test "$op"
  wait_concluded test "${op}-rollback" Succeeded/Verified
  never_prod() { ! op_exists prod payments "$d"; }
  always 5 never_prod || fail "payments 2.4.2 reached prod"
  pass "the node agent saw payments 2.4.2 call fraud, which its contract never names: failed in test, never in prod"
}

story_b4() {
  beat B4
  local out
  checkout_version payments 2.4.3
  out="$(cd "$WORK/payments" && reg "$PACTO" publish 2>&1)" && fail "publishing payments 2.4.3 was not refused: $out"
  local rule; rule="$(grep -m1 "FAIL version:" <<<"$out" || true)"
  grep -q "$(expect B4 RequiredBump | jq -r .)" <<<"$rule" || drift B4 "the version rule" "${rule:-$out}" "a $(expect B4 RequiredBump | jq -r .) bump"
  same B4 "the version refused" "2.4.3" "$(expect B4 Refused | jq -r .)"
  echo "  pacto publish 2.4.3 is refused: ${rule#*FAIL }"
  diffs B4 2.4.2 3.0.0
  ship_test payments 3.0.0
  # A copy pushed with plain oras: the same layer and contract, never signed by
  # pacto publish.
  local r="127.0.0.1:${LOCAL_REG_PORT}/story/deploy/payments" tmp man copy
  tmp="$(mktemp -d)"
  man="$(oras manifest fetch --plain-http "$r:3.0.0")" || fail "oras could not read payments 3.0.0"
  oras blob fetch --plain-http "$r@$(jq -r '.layers[0].digest' <<<"$man")" --output "$tmp/layer" || fail "oras could not read its layer"
  copy="$(cd "$tmp" && oras push --plain-http --disable-path-validation --format go-template='{{.digest}}' \
    --annotation "pacto.dev/contract=$(jq -r '.annotations["pacto.dev/contract"]' <<<"$man")" \
    "$r:oras-copy" "layer:$(jq -r '.layers[0].mediaType' <<<"$man")")" || fail "oras could not push the copy"
  propose -n test --stage payments --to "$copy" --name payments-oras-copy --reason "B4: a copy pushed with plain oras"
  wait_concluded test payments-oras-copy Rejected/
  predicted B4 test payments-oras-copy
  same B4 "the refusing gate" "$(field test payments-oras-copy plan | jq -r '[.verdicts[] | select(.result == "Fail") | .gate][0]')" "$(expect B4 Refusal | jq -r .)"
  [ "$(field test payments-oras-copy reason)" = NotPublished ] \
    || drift B4 "the refusal" "$(field test payments-oras-copy reason)" "$(expect B4 Refusal | jq -r .) (NotPublished)"
  pass "2.4.3 refused (Required bump: major), 3.0.0 published signed and verified in test, the oras copy refused as not published"
}

story_b5() {
  beat B5
  diffs B5 2.3.0 3.0.0
  local d; d="$(digest_of payments 3.0.0)"
  eventually 60 offered payments "$d" || fail "test never offered payments 3.0.0 to prod"
  propose -n prod --stage payments --to "$d" --name payments-3-0-0 --reason "B5: removes a field"
  eventually 60 held prod payments-3-0-0 || fail "payments 3.0.0 was not held: $(field prod payments-3-0-0 phase) $(field prod payments-3-0-0 message)"
  predicted B5 prod payments-3-0-0
  local plan; plan="$(field prod payments-3-0-0 plan)"
  same B5 "who is asked" "$(jq -c '.floor.owners | sort' <<<"$plan")" "$(expect B5 Asked)"
  same B5 "the decision" "$(jq -r .floor.decision <<<"$plan")" "$(expect B5 Decision | jq -r .)"
  same B5 "what moves first" "$(jq -c '[.order.moveFirst[] | select(.candidate) | "\(.service) \(.candidate.version)"]' <<<"$plan")" "$(expect B5 MoveFirst)"
  echo "  question: $(jq -r '.question // empty' <<<"$plan")"
  pass "payments 3.0.0 is held for prod: only checkout and orders are asked, and checkout 1.9.0 can ship first"
}

# story_records — moment 5: every concluded step is a signed record anyone with
# the recorder's public key can check.
story_records() {
  echo; echo "== moment 5: every step is a signed record"
  records_verified() { reg "$PACTO" delivery record verify -n prod --stage payments --trust "$KEYS/recorder.pub" >/dev/null 2>&1; }
  eventually 60 records_verified || { reg "$PACTO" delivery record verify -n prod --stage payments --trust "$KEYS/recorder.pub" || true; fail "the payments prod records do not verify"; }
  reg "$PACTO" delivery history -n prod --stage payments --trust "$KEYS/recorder.pub" | sed 's/^/    /'
  pass "the payments prod history reads back from signed records, and they verify"
}

cmd_story() {
  admin
  [ -s "$DEMO/digests/payments-2.3.0" ] || fail "the demo's starting point is missing: run 'make e2e-delivery-demo-up'"
  [ ! -e "$DEMO/digests/payments-3.0.0" ] || fail "the story already ran on this cluster: 'make e2e-delivery-demo-down' and up again to replay it"
  registry_pf
  story_pactify
  story_b1
  story_b2
  story_b3
  story_b4
  story_b5
  story_records
  stop_pf
  echo; echo "STORY PASS: every beat matched examples/story"
  next_steps story
}

# --- browser -----------------------------------------------------------------

# b6 — the agent ships checkout 1.9 to prod (compatible, nobody involved), and
# the operation held in B5 now asks only orders: the same operation, not a new one.
b6() {
  beat B6
  local d; d="$(digest_of checkout 1.9.0)"
  if ! runs prod checkout "$d"; then
    propose -n prod --stage checkout --to "$d" --name checkout-1-9-0 --reason "B6: move first"
    wait_concluded prod checkout-1-9-0 Succeeded/Verified
  fi
  asks() { [ "$(field prod payments-3-0-0 plan.floor.owners)" = "$(expect B6 Asked | jq -c .)" ]; }
  eventually 60 asks || drift B6 "who is asked" "$(field prod payments-3-0-0 plan.floor.owners)" "$(expect B6 Asked)"
  held prod payments-3-0-0 || fail "payments 3.0.0 is no longer held: $(field prod payments-3-0-0 phase)"
  pass "checkout 1.9.0 shipped with nobody involved, and the same held operation now asks only orders"
}

# journeys BEAT — the dashboard journeys whose titles start with "BEAT: ", against
# the live demo; the team they expect to be asked comes from examples/story.
journeys() {
  ( cd "$ROOT/pkg/dashboard/frontend" && PW_BASE_URL="$DASH_URL/" PW_DELIVERY_DEMO=1 PW_ASKED="$(expect B7 Asked | jq -r 'join(" or ")')" \
    timeout 600 npx playwright test --config playwright.live.config.ts e2e-live/delivery-journeys.spec.ts --grep "$1: " ) \
    || fail "the dashboard journeys failed: $1"
}

# b8 — carol approved, 3.0.0 shipped, and prod sends no telemetry for it
# (span-modes prod.3.0.0=silent), so the check says Unknown and the operation
# waits for carol to accept the unknown.
b8() {
  beat B8
  left_held() { ! held prod payments-3-0-0; }
  eventually 60 left_held || fail "carol's approval did not move payments 3.0.0 on: $(field prod payments-3-0-0 phase) $(field prod payments-3-0-0 message)"
  [ "$(field prod payments-3-0-0 phase)" != Concluded ] || fail "payments 3.0.0 concluded $(outcome prod payments-3-0-0) instead of verifying"
  unknown_held() { held prod payments-3-0-0 && [ "$(field prod payments-3-0-0 applied)" = true ]; }
  eventually 150 unknown_held || fail "payments 3.0.0 never waited on its check: $(field prod payments-3-0-0 phase) $(field prod payments-3-0-0 message)"
  same B8 "the check after release" "$(op_json prod payments-3-0-0 | jq -r '[.status.verdicts[]? | select(.phase == "verify") | .result] | if index("Fail") then "Fail" elif index("Unknown") then "Unknown" else "Pass" end')" "$(expect B8 Verify | jq -r .)"
  journeys B8
  wait_concluded prod payments-3-0-0 Succeeded/AcceptedUnknown
  pass "payments 3.0.0 shipped, its check said Unknown, and it stays recorded as an accepted unknown, never a pass"
}

# b9 — carol rolls 3.0.0 back from the dashboard; the rollback proceeds and
# holds automation off, so nothing moves prod forward again on its own.
b9() {
  beat B9
  journeys B9
  local d op; d="$(digest_of payments 2.3.0)"
  rolled() { op_to prod payments "$d" pacto.dev/rollback | grep -vx payments-2-4-0-rollback | grep -q .; }
  eventually 60 rolled || fail "carol's rollback of payments 3.0.0 raised no operation"
  op="$(op_to prod payments "$d" pacto.dev/rollback | grep -vx payments-2-4-0-rollback | head -1)"
  predicted B9 prod "$op"
  wait_concluded prod "$op" Succeeded/Verified
  runs prod payments "$d" || fail "prod is not back on payments 2.3.0"
  [ "$(kubectl -n prod get stage payments -o jsonpath='{.status.autoHold}')" = true ] || fail "carol's rollback did not hold automation off"
  always 5 runs prod payments "$d" || fail "automation moved payments off 2.3.0 after carol's rollback"
  pass "carol rolled payments back to 2.3.0 and automation holds off"
}

cmd_browser() {
  admin
  [ -e "$DEMO/digests/payments-3.0.0" ] || fail "run the story first: make e2e-delivery-demo-story"
  # Carol's approval (B7) moves 3.0.0 on and B9 rolls prod back for good, so
  # once 3.0.0 has left its first hold the step cannot be played again.
  { held prod payments-3-0-0 && [ "$(field prod payments-3-0-0 applied)" != true ]; } \
    || fail "the browser step already ran on this cluster: 'make e2e-delivery-demo-down' and up again to replay it"
  b6
  ( cd "$ROOT/pkg/dashboard/frontend" \
    && { [ -d node_modules ] || npm ci --ignore-scripts >/dev/null 2>&1; } \
    && timeout 300 npx playwright install ${CI:+--with-deps} chromium >/dev/null 2>&1 ) \
    || fail "could not install the browser for the dashboard journeys"
  beat B7
  journeys B7
  b8
  b9
  echo; echo "== moment 5 in Chromium: History reads the steps from signed records"
  journeys History
  pass "B6 to B9 matched examples/story, and the dashboard journeys passed against the live demo"
}

# --- next steps --------------------------------------------------------------

next_steps() {
  # Paths from the repository root; the CLI reaches the registry in the
  # cluster through a port-forward, as the story does.
  local d=tmp/delivery-demo
  local k="KUBECONFIG=$d/admin.kubeconfig" a="KUBECONFIG=$d/pay-agent.kubeconfig" t="--trust $d/keys/recorder.pub"
  local r="HTTP_PROXY=http://127.0.0.1:$LOCAL_REG_PORT PACTO_INSECURE_REGISTRIES=$REG_HOST" p="$d/bin/pacto"
  cat <<EOF

== What to do next ==
From the repository root. The commands that read signed records need the
registry: $k kubectl -n $NS port-forward svc/pacto-registry $LOCAL_REG_PORT:5000 &
The guided path (five moments):
  1. pactify a service ............ $([ "$1" = up ] && echo "make e2e-delivery-demo-story" || echo "done: fraud")
  2. a safe change ships alone .... $([ "$1" = up ] && echo "(story B1)" || echo "done: payments 2.4.0")
  4. a bad release rolls back ..... $([ "$1" = up ] && echo "(story B2, B3)" || echo "done: 2.4.0 in prod, 2.4.2 in test")
  3. a breaking change asks only the affected teams .... $([ "$1" = up ] && echo "(story B5)" || echo "now: payments 3.0.0 is held")
  5. every step is a signed record:
       $k $r $p delivery history -n prod --stage payments $t
       $k $r $p delivery record verify --all $t
(The story plays beats in order, so moment 4 comes before moment 3.)

The dashboard: $DASH_URL  (sign in through Dex; every password is "password")
  carol@example.com  team orders, the one asked about payments 3.0.0
  alice@example.com  team payments, never asked
  pay-agent@example.com  an agent: it may look and propose, never approve

  make e2e-delivery-demo-status | make e2e-delivery-demo-logs | make e2e-delivery-demo-down
What is generated, not real: the payments workload (podinfo), its telemetry (a span generator) and the call 2.4.2 makes (a sidecar).
EOF
  # Explore more only after the story: B6 run before it ships checkout 1.9.0
  # early and the story's B5 no longer plays as written.
  [ "$1" = story ] || return 0
  cat <<EOF
Explore more:
  B6, as the agent: ship checkout 1.9 first, and the held change asks only orders
       $a $p delivery propose -n prod --stage checkout --to $(cat "$DEMO/digests/checkout-1.9.0" 2>/dev/null || echo '<checkout 1.9.0 digest>') --name checkout-1-9-0
       or play and check B6 to B9 (B7 to B9 in Chromium): make e2e-delivery-demo-browser
  B7, in the browser: pay-agent and alice cannot approve; carol signs in again and approves
  B8: prod sends too little telemetry for 3.0.0 (generated), so the check says Unknown and waits for carol
  B9: carol rolls 3.0.0 back from the dashboard; then
       $k $r $p delivery blame -n prod --stage payments $t
       $k $p delivery undo -n prod <operation>
  The agent over remote MCP ($MCP_URL/mcp): get a Keycloak token for agent-bot
       curl -sk https://127.0.0.1:8443/realms/pacto/protocol/openid-connect/token -u agent-bot:agent-secret -d grant_type=client_credentials
       and call the pacto_delivery_* tools with it as a bearer token (there is no approve tool)
  Check a record yourself with cosign, as docs/delivery-history.md shows (key: $d/keys/recorder.pub)
  $k kubectl get stages,operations,approvals -A
  Two agents racing, a check result never reused for a new version and the admission
  policies (no agent approvals, immutable intent) are proven by: make test-acceptance-kind-delivery
EOF
}

cmd_status() {
  admin
  kubectl get stages -A -o custom-columns='NAMESPACE:.metadata.namespace,STAGE:.metadata.name,POINTER:.status.pointer,FROZEN:.status.frozen'
  echo
  kubectl get operations -A -o custom-columns='NAMESPACE:.metadata.namespace,OPERATION:.metadata.name,TYPE:.spec.type,PHASE:.status.phase,OUTCOME:.status.outcome,REASON:.status.reason'
}

cmd_down() {
  docker rm -f "${IDP_CONTAINERS[@]}" >/dev/null 2>&1 || true
  down_cluster
  rm -rf "$DEMO"
}

case "$CMD" in
  up) trap 'rc=$?; stop_pf; [ $rc -eq 0 ] || { dump_diag "$NS"; dump_identity; }; exit $rc' EXIT; cmd_up ;;
  story) trap 'rc=$?; stop_pf; exit $rc' EXIT; cmd_story ;;
  browser) cmd_browser ;;
  status) cmd_status ;;
  logs) admin; dump_diag "$NS"; dump_identity; kubectl -n "$DELIVERY_NS" logs deploy/pacto-delivery --tail=100 2>&1 || true ;;
  down) cmd_down ;;
  all)
    rc=0; { bash "$0" up && bash "$0" story && bash "$0" browser; } || rc=$?
    # A failed run in CI keeps the cluster for scripts/ci/kind-diagnostics.sh.
    { [ -n "${CI:-}" ] && [ $rc -ne 0 ]; } || bash "$0" down
    exit $rc ;;
  *) echo "usage: $0 up|story|status|logs|browser|down|all"; exit 2 ;;
esac
