#!/usr/bin/env bash
# The Kubernetes delivery demo: "the payments story" (examples/story) played on
# a real cluster, where the components are real (the workloads, their telemetry
# and the B10 sign-ins are generated): the registry, Flux, the promoter,
# the recorder, Mira, the node agent, Dex and Keycloak sign-in, the dashboard
# and remote MCP.
#
#   up       create the cluster and install everything; payments 2.3.0,
#            checkout 1.8.0 (1.9.0 waiting in test) and orders 1.4.0 run.
#            fraud is a plain folder, not pactified yet.
#   story    play the story: pactify fraud, then B1 to B5, checking every beat
#            against examples/story; it stops with payments 3.0.0 held.
#   browser  B6, then B7 to B9 in Chromium with the cluster checked between
#            beats, then History, then B10 (a tampered record freezes
#            payments in prod, a break-glass edit is a witnessed record),
#            then the agent beat (pay-agent ships behind its evals); it runs
#            once per cluster.
#   reach    phase 8: a second cluster, prod-us, follows prod through its
#            signed records, and Argo CD applies what its promoter promotes;
#            then prod-eu (first cluster) freezes on a failed re-check it
#            cannot roll back, and prod-us refuses the frozen version.
#   status   what runs where.
#   logs     diagnostics of the cluster.
#   down     delete the cluster and the containers beside it.
#   all      up, story, browser, reach and down (CI's nightly run).
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
    for env in ("test", "prod", "prod-eu"):
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
# eval_runner — a stand-in eval runner for pay-agent's eval suite (phase 7):
# it runs nothing and answers, for the pay-agent version running in test, the
# count the eval-results ConfigMap sets ("<version>": "passed/total"; every
# case passes when unset). It reuses the span generator's read-only account.
eval_runner() {
  kubectl -n "$NS" create configmap eval-results --from-literal=1.1.0=1/2 --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  kubectl -n "$NS" create configmap eval-runner --from-file=run.py=/dev/stdin --dry-run=client -o yaml <<'PY' | kubectl apply -f - >/dev/null
import json, ssl, urllib.request
from http.server import BaseHTTPRequestHandler, HTTPServer
SA = "/var/run/secrets/kubernetes.io/serviceaccount"
ctx = ssl.create_default_context(cafile=SA + "/ca.crt")
def get(path):
    req = urllib.request.Request("https://kubernetes.default.svc" + path, headers={"Authorization": "Bearer " + open(SA + "/token").read()})
    return json.load(urllib.request.urlopen(req, context=ctx, timeout=5))
class Runner(BaseHTTPRequestHandler):
    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["content-length"])))
        version = get("/apis/apps/v1/namespaces/test/deployments/pay-agent")["spec"]["template"]["metadata"]["labels"]["app.kubernetes.io/version"]
        counts = (get("/api/v1/namespaces/pacto-system/configmaps/eval-results").get("data") or {}).get(version, "2/2")
        passed, total = (int(n) for n in counts.split("/"))
        out = json.dumps({"subject": body["input"]["subjects"][0], "suite": "evals/suite.yaml", "run": "demo-" + version,
                          "passed": passed, "total": total, "message": "generated by the demo's stand-in runner"}).encode()
        self.send_response(200); self.send_header("content-type", "application/json"); self.end_headers(); self.wfile.write(out)
HTTPServer(("", 8080), Runner).serve_forever()
PY
  kubectl -n "$NS" apply -f - >/dev/null <<YAML
apiVersion: apps/v1
kind: Deployment
metadata: {name: eval-runner}
spec:
  replicas: 1
  selector: {matchLabels: {app: eval-runner}}
  template:
    metadata: {labels: {app: eval-runner}}
    spec:
      serviceAccountName: span-generator
      containers:
      - {name: run, image: ${PYTHON_IMG%@*}, imagePullPolicy: Never, command: [python, -u, /runner/run.py], volumeMounts: [{name: run, mountPath: /runner}]}
      volumes: [{name: run, configMap: {name: eval-runner}}]
---
apiVersion: v1
kind: Service
metadata: {name: eval-runner}
spec: {selector: {app: eval-runner}, ports: [{name: http, port: 8080, targetPort: 8080}]}
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
    --set-json "gates.providers=[{\"name\":\"mira\",\"type\":\"mira\",\"url\":\"http://mira.${NS}.svc.cluster.local:4318\"},{\"name\":\"evals\",\"type\":\"eval\",\"url\":\"http://eval-runner.${NS}.svc.cluster.local:8080\"}]" \
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
  eval_runner
  eventually 60 kubectl -n pacto-node-agent get ds pacto-node-agent >/dev/null 2>&1 || fail "the operator never created the node agent"
  kubectl -n pacto-node-agent rollout status ds/pacto-node-agent --timeout=180s >/dev/null || fail "the node agent did not roll out"
  kubectl -n "$NS" rollout status deploy/span-generator --timeout=120s >/dev/null || fail "the span generator did not roll out"
  kubectl -n "$NS" rollout status deploy/eval-runner --timeout=120s >/dev/null || fail "the eval runner did not roll out"
  pass "installed: registry, operator and node agent, Flux, promoter and recorder, Mira, span generator, eval runner"

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
  # The interface payments (http) or pay-agent (tools) declares is served by
  # its Service's port 8080:
  # Kubernetes knowledge that lives on the Pacto resource, not the contract.
  local bind=""
  [ "$svc" = payments ] && bind=", serviceName: payments, interfaceBindings: [{interface: http, servicePort: 8080}]"
  [ "$svc" = pay-agent ] && bind=", serviceName: pay-agent, interfaceBindings: [{interface: tools, servicePort: 8080}]"
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
    impact: {declaredConsumersOnly: true}
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

# diffs BEAT OLD NEW [SERVICE] — pacto diff on the story's contracts gives the
# beat's classification and required bump (SERVICE defaults to payments).
diffs() {
  local got c="$STORY/contracts/${4:-payments}"
  got="$("$PACTO" diff --output-format json "$c/$2" "$c/$3" 2>/dev/null || true)"
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
  # A throwaway key stays out of the public transparency log, and the demo's
  # registry speaks plain HTTP: both are init flags.
  echo "  \$ pacto init --delivery --owner risk --no-transparency-log --insecure-registry"
  (cd "$dir" && "$PACTO" init --delivery --owner risk --no-transparency-log --insecure-registry) | sed 's/^/    /'
  [ -s "$dir/delivery/test.yaml" ] || fail "pacto init --delivery wrote no Stage for test"
  cp "$KEYS/cosign.key" "$dir/"
  git -C "$dir" init -q && git -C "$dir" add -A && git -C "$dir" -c user.email=demo@example.com -c user.name=demo commit -qm "pactify fraud"
  local out d op
  out="$(cd "$dir" && reg "$PACTO" publish 2>&1)" || { echo "$out" >&2; fail "pacto publish fraud failed"; }
  d="$(grep -oE "^Deploy: .*@sha256:[0-9a-f]{64}" <<<"$out" | sed 's/.*@//')"
  remember fraud 1.0.0 "$d"
  kubectl apply -f "$dir/delivery/test.yaml" -f "$dir/delivery/prod.yaml" >/dev/null
  op="$(wait_op test fraud "$d")"
  wait_concluded test "$op" Succeeded/Verified
  predicted pactify test "$op"
  eventually 60 kubectl -n test get pacto fraud >/dev/null 2>&1 || fail "pacto init --delivery wrote no Pacto resource for fraud in test"
  local img; img="$(kubectl -n test get deploy fraud -o jsonpath='{.spec.template.spec.containers[0].image}')"
  [[ "$img" == "${REG_HOST}/story/images/fraud@sha256:"* ]] || fail "fraud runs $img in test, not the image pacto publish built, pinned by digest"
  pass "fraud is pactified: its contract, Pacto resource and Stages come from pacto init as it wrote them, and 1.0.0 runs in test with nobody involved"
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
  remember payments oras-copy "$copy"
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

# person USER GROUP — a kubeconfig that is USER, a person on GROUP, signed in
# just now: the admin credentials impersonating them, with the sign-in time
# structured authentication maps from Dex's auth_time.
person() {
  local f="$DEMO/$1.kubeconfig"
  kubectl config view --raw --minify -o json | jq --arg u "$1" --arg g "$2" --arg t "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    '.users[0].user += {as: $u, "as-groups": [$g], "as-user-extra": {"pacto.dev/auth-time": [$t]}}' > "$f"
  echo "$f"
}
rec() { reg "$PACTO" delivery "$@" --trust "$KEYS/recorder.pub"; }
condition() { kubectl -n prod get stage payments -o jsonpath="{.status.conditions[?(@.type==\"RecordStoreReady\")].$1}"; }
frozen() { [ "$(kubectl -n prod get stage payments -o jsonpath='{.status.frozen}')" = true ]; }

# b10 — someone changes a signed record of the payments prod stream: the
# recorder refuses to write after it and the promoter freezes the Stage. The
# agent cannot unfreeze it; once the forged record is gone alice does. Then
# ops edits the pointer by hand under break-glass, which the promoter adopts
# as a witnessed override record and freezes the Stage again.
b10() {
  beat B10
  local tmp streams="127.0.0.1:${LOCAL_REG_PORT}/pacto-delivery-streams" m l forged out
  tmp="$(mktemp -d)"
  m="$(rec record verify -n prod --stage payments --output-format json | jq -r '.records[-1].manifest')"
  l="$(oras manifest fetch --plain-http "$streams@$m" | jq -r '.layers[0].digest')"
  oras blob fetch --plain-http --output "$tmp/head.json" "$streams@$l" >/dev/null || fail "oras could not read the newest payments prod record"
  # The newest record, with the time it finished changed and its signature kept.
  jq --arg p "$(jq -r .dsseEnvelope.payload "$tmp/head.json" | base64 -d | jq -cj '.predicate.time.finished = "2000-01-01T00:00:00Z"' | base64 | tr -d '\n')" \
    '.dsseEnvelope.payload = $p' "$tmp/head.json" > "$tmp/tampered.json"
  forged="$(cd "$tmp" && oras attach --plain-http --artifact-type application/vnd.dev.sigstore.bundle.v0.3+json \
    --annotation dev.sigstore.bundle.predicateType=pacto.dev/operation/v1 --annotation dev.sigstore.bundle.content=dsse-envelope \
    --format go-template='{{.digest}}' "$streams@$(kubectl -n prod get stage payments -o jsonpath='{.status.stream.anchor}' | sed 's/.*@//')" \
    "tampered.json:application/vnd.dev.sigstore.bundle.v0.3+json")" || fail "oras could not attach the tampered record"
  echo "  a changed copy of the newest record is attached to the payments prod stream: $forged"
  out="$(rec record verify -n prod --stage payments 2>&1 || true)"
  grep -q "unreadable: $forged: signature" <<<"$out" || fail "record verify did not refuse the tampered record: $out"
  echo "  pacto delivery record verify: $(grep -m1 "$forged" <<<"$out" | sed 's/^ *//')"
  # The next record the recorder writes reads the stream first, and stops there.
  propose -n prod --stage payments --to "$(digest_of payments oras-copy)" --name payments-b10 --reason "B10: anything the recorder must write"
  conflict() { [ "$(condition reason)" = StreamConflict ]; }
  eventually 60 conflict || fail "the recorder did not stop on the tampered record: $(condition message)"
  eventually 30 frozen || fail "the payments prod Stage did not freeze on the tampered record"
  out="$(KUBECONFIG="$AGENT_KC" "$PACTO" delivery propose -n prod --stage payments --type unfreeze --name b10-agent-unfreeze --reason "B10" 2>&1 || true)"
  grep -q "only a person or break-glass may unfreeze" <<<"$out" || fail "pay-agent's unfreeze was not refused: $out"
  echo "  pay-agent tries to unfreeze it: refused, only a person or break-glass may unfreeze a Stage"
  oras manifest delete --plain-http --force "$streams@$forged" >/dev/null || fail "oras could not remove the tampered record"
  sound() { [ "$(condition status)" = True ]; }
  eventually 60 sound || fail "the recorder did not resume once the tampered record was gone: $(condition message)"
  frozen || fail "the Stage unfroze on its own"
  echo "  the tampered record is removed and the recorder resumes; payments prod stays frozen until a person says so"
  KUBECONFIG="$(person alice@example.com team-payments)" "$PACTO" delivery propose -n prod --stage payments --type unfreeze \
    --name b10-unfreeze --reason "B10: the tampered record is gone" >/dev/null || fail "alice could not propose the unfreeze"
  echo "  alice unfreezes it (alice and, below, ops act through admin impersonation with a sign-in time the script makes up; B7 signs in through Dex)"
  eventually 60 concluded prod b10-unfreeze || fail "alice's unfreeze never concluded: $(field prod b10-unfreeze message)"
  # The promoter plans no unfreeze; the story says what it concludes instead.
  same B10 "the unfreeze" "$(outcome prod b10-unfreeze)" "$(expect B10 Concluded | jq -r .)"
  ! frozen || fail "payments prod stayed frozen after alice unfroze it"
  pass "the tampered record froze payments in prod; pay-agent could not unfreeze it, alice did once the record was gone"

  echo "  ops pins payments 2.4.0 in prod by hand, under break-glass, outside Pacto"
  mode prod.2.4.0 ok
  kubectl create clusterrole demo-break-glass --verb=get,patch --resource=ocirepositories.source.toolkit.fluxcd.io \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  kubectl create clusterrolebinding demo-break-glass --clusterrole=demo-break-glass --group=pacto:break-glass \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  local d op; d="$(digest_of payments 2.4.0)"
  KUBECONFIG="$(person ops@example.com pacto:break-glass)" kubectl -n prod patch ocirepository payments --type merge \
    -p "{\"spec\":{\"ref\":{\"digest\":\"$d\"}}}" >/dev/null || fail "the break-glass edit was refused"
  op="$(wait_op prod payments "$d" pacto.dev/override)"
  wait_concluded prod "$op" Witnessed/Unverified
  eventually 30 frozen || fail "the witnessed override did not freeze payments prod"
  witnessed() { rec history -n prod --stage payments --output-format json | jq -e \
    '[.entries[] | select(.outcome == "Witnessed" and .record != "")] | length >= 1' >/dev/null; }
  eventually 60 witnessed || fail "the history shows no witnessed record of the break-glass edit"
  rec record verify -n prod --stage payments >/dev/null 2>&1 || fail "the payments prod records do not verify after B10"
  pass "the break-glass edit is a witnessed override record, payments prod is frozen again, and every record verifies"
}

# b_agent — phase 7: pay-agent is delivered as a service. Its test Stage
# checks each version with its eval suite (through the stand-in runner):
# the runner reports fixed counts (2 of 2 for 1.0.0, 1 of 2 for 1.1.0, the size of
# its suite)
# rather than running the suite's cases. 1.0.0 ships and test offers it to
# prod; 1.1.0 swaps its model, fails in test and is never offered to prod.
b_agent() {
  beat agent
  environment pay-agent test candidates true "$(cat <<YAML
      checks:
      - {name: evals, provider: evals, params: {minPassRate: "0.95"}}
YAML
)"
  environment pay-agent prod upstream false
  ship_test pay-agent 1.0.0
  offered pay-agent "$(digest_of pay-agent 1.0.0)" || fail "test does not offer pay-agent 1.0.0 to prod after it passed its evals"
  echo "  the stand-in eval runner reports 2 of 2 for pay-agent 1.0.0, so test offers it to prod"
  diffs agent 1.0.0 1.1.0 pay-agent
  local d op cause; d="$(publish pay-agent 1.1.0)"; remember pay-agent 1.1.0 "$d"
  op="$(wait_op test pay-agent "$d")"
  predicted agent test "$op"
  eventually 150 failed_on test pay-agent "$d" || fail "pay-agent 1.1.0 never failed in test: $(field test "$op" phase) $(field test "$op" message)"
  cause="$(op_json test "${op}-rollback" | jq -c .spec.cause.verdict)"
  same agent "the eval check" "$(jq -r .result <<<"$cause")" "$(expect agent Verify | jq -r .)"
  [ "$(jq -r '.gate + " " + .citation.passed + "/" + .citation.total' <<<"$cause")" = "evals 1/2" ] \
    || fail "the rollback does not cite the eval run: $cause"
  wait_concluded test "${op}-rollback" Succeeded/Verified
  # prod is a manual Stage, so the gate's effect is what test offers it.
  not_offered() { ! offered pay-agent "$d"; }
  always 5 not_offered || fail "test offers pay-agent 1.1.0 to prod though it failed its evals"
  pass "pay-agent shipped like a service: 1.0.0 passed its evals and is offered to prod, 1.1.0 got 1 of 2 from the stand-in runner, rolled back in test and is never offered"
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
  registry_pf
  b10
  b_agent
  stop_pf
  pass "B6 to B10 and the agent beat matched examples/story, and the dashboard journeys passed against the live demo"
}

# --- reach (phase 8) ---------------------------------------------------------

# A second cluster, prod-us, runs payments wherever prod (the first cluster)
# verified it. The controllers never talk to each other: prod-us's promoter
# reads prod's signed records from the registry (in the demo that registry runs
# in the first cluster and prod-us reaches it through a NodePort), and Argo CD,
# not Flux, applies what it promotes.
US_CLUSTER=pacto-delivery-demo-us
US_KC="$DEMO/us.kubeconfig"
ARGOCD_VERSION=v3.5.2
us() { KUBECONFIG="$US_KC" "$@"; }

# us_registry — prod-us reaches the first cluster's registry under the same
# name, so every reference in an artifact or a record resolves unchanged: a
# NodePort on the first cluster, and in prod-us a Service of that name whose
# endpoint is the first cluster's node.
us_registry() {
  local ip port
  kubectl -n "$NS" expose deploy pacto-registry --name pacto-registry-np --type NodePort --port 5000 \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  port="$(kubectl -n "$NS" get svc pacto-registry-np -o jsonpath='{.spec.ports[0].nodePort}')"
  ip="$(docker inspect -f '{{.NetworkSettings.Networks.kind.IPAddress}}' "$CLUSTER-control-plane")"
  us kubectl -n "$NS" apply -f - >/dev/null <<YAML
apiVersion: v1
kind: Service
metadata: {name: pacto-registry}
spec: {ports: [{port: 5000, targetPort: $port}]}
---
apiVersion: discovery.k8s.io/v1
kind: EndpointSlice
metadata: {name: pacto-registry-first-cluster, labels: {kubernetes.io/service-name: pacto-registry}}
addressType: IPv4
ports: [{port: $port}]
endpoints: [{addresses: ["$ip"]}]
YAML
}

# us_argocd — Argo CD's core install (no API server, no UI), told to read
# Applications in the prod namespace, to fetch the registry over plain HTTP
# and to accept the layer type pacto publish writes (Flux's).
us_argocd() {
  local m img; m="$(mktemp)"
  infra "download Argo CD $ARGOCD_VERSION" curl -fsSL --max-time 60 -o "$m" \
    "https://raw.githubusercontent.com/argoproj/argo-cd/${ARGOCD_VERSION}/manifests/core-install.yaml"
  sed -i.bak 's/imagePullPolicy: Always/imagePullPolicy: IfNotPresent/' "$m"
  for img in $(awk '$1 == "image:" { print $2 }' "$m" | sort -u); do
    infra "pull $img" docker pull -q "$img" >/dev/null
    CLUSTER="$US_CLUSTER" infra "load $img into prod-us" load_images "$img"
  done
  us kubectl create namespace argocd --dry-run=client -o yaml | us kubectl apply -f - >/dev/null
  us kubectl apply -n argocd --server-side -f "$m" >/dev/null
  crds() { us kubectl get applications.argoproj.io -A >/dev/null 2>&1 && us kubectl get appprojects.argoproj.io -A >/dev/null 2>&1; }
  eventually 40 crds || fail "Argo CD's kinds never reached discovery"
  us kubectl -n argocd scale deploy argocd-applicationset-controller --replicas=0 >/dev/null 2>&1 || true
  us kubectl -n argocd patch configmap argocd-cmd-params-cm --type merge -p '{"data":{"application.namespaces":"prod",
    "reposerver.oci.layer.media.types":"application/vnd.oci.image.layer.v1.tar,application/vnd.oci.image.layer.v1.tar+gzip,application/vnd.cncf.flux.content.v1.tar+gzip"}}' >/dev/null
  us kubectl -n argocd rollout restart statefulset/argocd-application-controller deploy/argocd-repo-server >/dev/null
  us kubectl apply -f - >/dev/null <<YAML
apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata: {name: default, namespace: argocd}
spec:
  sourceRepos: ['*']
  sourceNamespaces: [prod]
  destinations: [{server: '*', namespace: '*'}]
  clusterResourceWhitelist: [{group: '*', kind: '*'}]
---
apiVersion: v1
kind: Secret
metadata: {name: story-deploy-payments, namespace: argocd, labels: {argocd.argoproj.io/secret-type: repository}}
stringData: {name: payments, type: oci, url: "oci://${REG_HOST}/story/deploy/payments", insecureOCIForceHttp: "true"}
YAML
  infra "roll out Argo CD's repo server" us kubectl -n argocd rollout status deploy/argocd-repo-server --timeout=300s
  infra "roll out Argo CD's controller" us kubectl -n argocd rollout status statefulset/argocd-application-controller --timeout=300s
}

cmd_reach() {
  admin
  [ -s "$DEMO/digests/payments-2.3.0" ] || fail "the demo's starting point is missing: run 'make e2e-delivery-demo-up'"
  local ver stream op_chart dl_chart d trust
  ver="$(release_version kubernetes)"
  stream="$(kubectl -n prod get stage payments -o jsonpath='{.status.stream.id}')"
  [ -n "$stream" ] || fail "payments prod has no record stream yet"

  section "prod-us: a second cluster with Argo CD, the Pacto operator and the promoter"
  kind get clusters 2>/dev/null | grep -qx "$US_CLUSTER" \
    || infra "create prod-us" kind create cluster --name "$US_CLUSTER" --kubeconfig "$US_KC" --wait 90s >/dev/null
  CLUSTER="$US_CLUSTER" infra "load the images into prod-us" load_images "localhost:5001/pacto-operator/pacto-controller:$ver" \
    "localhost:5001/pacto-delivery:$ver" "${PODINFO_IMG%@*}"
  local n
  for n in "$NS" "$DELIVERY_NS" prod; do us kubectl create namespace "$n" --dry-run=client -o yaml | us kubectl apply -f - >/dev/null; done
  us_registry
  us_argocd
  op_chart="$(package_chart "$PACTO_CHART")"
  dl_chart="$(package_chart "$ROOT/integrations/kubernetes/charts/pacto-delivery")"
  # The first cluster's recorder key, trusted for that one stream only.
  trust="$DEMO/us-values.json"
  jq -n --arg k "$(cat "$KEYS/recorder.pub")" --arg s "$stream" --arg eu "${stream%%/*}/prod-eu/payments" \
    '{promoter: {upstreamTrust: ({keys: [{name: "prod-recorder", publicKey: $k, streams: [$s, $eu]}]} | tojson)}}' > "$trust"
  us helm upgrade --install pacto-operator "$op_chart" -n "$NS" --set image.repository=localhost:5001/pacto-operator/pacto-controller \
    --set image.tag="$ver" --set image.pullPolicy=Never --set "insecureRegistries[0]=$REG_HOST" --wait --timeout 240s >/dev/null \
    || fail "the pacto-operator chart did not roll out in prod-us"
  us helm upgrade --install pacto-delivery "$dl_chart" -n "$DELIVERY_NS" --set image.repository=localhost:5001/pacto-delivery \
    --set image.tag="$ver" --set image.pullPolicy=Never --set "insecureRegistries[0]=$REG_HOST" --set promoter.poll=2s \
    --set argocd.enabled=true -f "$trust" --wait --timeout 240s >/dev/null \
    || fail "the pacto-delivery chart did not roll out in prod-us"

  section "payments in prod-us follows prod through its signed records, applied by Argo CD"
  us kubectl apply -f - >/dev/null <<YAML
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata: {name: payments, namespace: prod, labels: {delivery.pacto.trianalab.io/apply-ref: "true"}}
spec:
  project: default
  source: {repoURL: "oci://${REG_HOST}/story/deploy/payments", path: .}
  destination: {server: https://kubernetes.default.svc, namespace: prod}
  syncPolicy: {automated: {}}
---
apiVersion: delivery.pacto.trianalab.io/v1alpha1
kind: Stage
metadata: {name: payments, namespace: prod}
spec:
  service: {name: payments}
  environment: prod-us
  applyRef: {kind: Application, name: payments}
  candidates: {upstreamRecords: {repository: "${REG_HOST}/pacto-delivery-streams", stream: "$stream"}}
  auto: true
  gates: {impact: {declaredConsumersOnly: true}}
YAML
  d="$(digest_of payments 2.3.0)"
  us_runs() { [ "$(us kubectl -n prod get stage payments -o jsonpath='{.status.pointer}')" = "$d" ]; }
  eventually 150 us_runs || fail "prod-us never took payments 2.3.0: $(us kubectl -n prod get stage payments -o jsonpath='{.status.conditions}')"
  synced() { us kubectl -n prod get application payments -o json | jq -e --arg d "$d" \
    '.status.sync.status == "Synced" and .status.health.status == "Healthy" and (.status.sync.revision | endswith($d | ltrimstr("sha256:")))' >/dev/null; }
  eventually 90 synced || fail "Argo CD never applied payments 2.3.0: $(us kubectl -n prod get application payments -o jsonpath='{.status.sync}')"
  [ "$(us kubectl -n prod get deploy payments -o jsonpath='{.spec.template.metadata.labels.app\.kubernetes\.io/version}')" = 2.3.0 ] \
    || fail "prod-us does not run payments 2.3.0"
  pass "prod-us runs payments 2.3.0, the version prod verified, and Argo CD applied it"
  [ -s "$DEMO/digests/payments-2.4.0" ] || fail "payments 2.4.0 never failed in prod: run 'make e2e-delivery-demo-story' first"
  local bad msg; bad="$(digest_of payments 2.4.0)"
  # reach reuses a running prod-us, and propose is a plain create: drop the last run's operation first.
  us kubectl -n prod delete operation payments-2-4-0 --ignore-not-found --wait --timeout=60s >/dev/null \
    || fail "could not remove the previous payments 2.4.0 operation in prod-us"
  us "$PACTO" delivery propose -n prod --stage payments --to "$bad" --name payments-2-4-0 --reason "2.4.0 failed in prod" >/dev/null \
    || fail "could not propose payments 2.4.0 in prod-us"
  refused() { [ "$(us kubectl -n prod get operation payments-2-4-0 -o jsonpath='{.status.outcome}')" = Rejected ]; }
  eventually 60 refused || fail "prod-us did not refuse payments 2.4.0: $(us kubectl -n prod get operation payments-2-4-0 -o jsonpath='{.status}')"
  msg="$(us kubectl -n prod get operation payments-2-4-0 -o jsonpath='{.status.reason}: {.status.message}')"
  grep -qF "failed after release on the upstream Stage $stream" <<<"$msg" \
    || fail "prod-us refused payments 2.4.0 for another reason than its failure in prod: $msg"
  pass "prod-us refuses payments 2.4.0 because it failed after release in prod: $msg"
  reach_freeze
  echo; echo "REACH PASS: prod-us followed prod through signed records, applied by Argo CD, and refused a version prod-eu froze on"
}

# reach_freeze — the failure a re-check cannot roll back reaches the other
# cluster. In the first cluster prod-eu runs payments 2.3.0, takes 2.4.0,
# which fails after release and rolls back to 2.3.0 on its own; then 2.3.0
# fails too. That rollback was the cause's one automatic rollback, so the
# Stage freezes, and the freeze is a signed record naming 2.3.0 and the failed
# check. A Stage in prod-us following prod-eu's records never takes 2.3.0; a
# person unfreezes prod-eu. Only the health check runs in prod-eu: it is the
# one the generated traffic fails.
reach_freeze() {
  local a x eu fz=eu-2-4-0-rollback-freeze
  a="$(digest_of payments 2.3.0)" x="$(digest_of payments 2.4.0)"
  eu="$(kubectl -n prod get stage payments -o jsonpath='{.status.stream.id}')"; eu="${eu%%/*}/prod-eu/payments"
  registry_pf
  # reach reuses the first cluster too: prod-eu plays once per cluster.
  if ! kubectl -n prod-eu get operation "$fz" >/dev/null 2>&1; then
    section "prod-eu: a re-check fails on the promoter's own rollback, so the Stage freezes"
    kubectl create namespace prod-eu --dry-run=client -o yaml | kubectl apply -f - >/dev/null
    kubectl -n prod-eu create secret generic cosign-pub --from-file=cosign.pub="$KEYS/cosign.pub" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
    environment payments prod-eu upstream false "$(payments_gates prod-eu 10s | grep -v pacto.runtime)"
    propose -n prod-eu --stage payments --to "$a" --name eu-2-3-0 --reason "prod-eu starts on 2.3.0"
    wait_concluded prod-eu eu-2-3-0 Succeeded/Verified
    propose -n prod-eu --stage payments --to "$x" --name eu-2-4-0 --reason "prod-eu takes 2.4.0"
    wait_concluded prod-eu eu-2-4-0 Succeeded/Verified
    mode prod-eu.2.4.0 errors
    eventually 90 kubectl -n prod-eu get operation eu-2-4-0-rollback >/dev/null 2>&1 || fail "the failed re-check of 2.4.0 raised no rollback in prod-eu"
    wait_concluded prod-eu eu-2-4-0-rollback Succeeded/Verified
    runs prod-eu payments "$a" || fail "prod-eu is not back on payments 2.3.0"
    echo "  2.4.0 failed after release in prod-eu and rolled back to 2.3.0 on its own (generated errors)"
    mode prod-eu.2.3.0 errors
    eventually 90 kubectl -n prod-eu get operation "$fz" >/dev/null 2>&1 || fail "the failed re-check of the rollback raised no freeze in prod-eu"
    wait_concluded prod-eu "$fz" Succeeded/Unverified
    pe_frozen() { [ "$(kubectl -n prod-eu get stage payments -o jsonpath='{.status.frozen}')" = true ]; }
    pe_frozen || fail "prod-eu is not frozen after its rollback failed"
    grep -qF "the promoter's own rollback, which is never undone automatically" <<<"$(kubectl -n prod-eu get stage payments -o jsonpath='{.status.frozenReason}')" \
      || fail "prod-eu froze for another reason: $(kubectl -n prod-eu get stage payments -o jsonpath='{.status.frozenReason}')"
    op_json prod-eu "$fz" | jq -e --arg a "$a" '.spec.type == "pacto.dev/freeze" and .spec.params.from == $a and .spec.cause.verdict.result == "Fail"' >/dev/null \
      || fail "the freeze does not name 2.3.0 and its failed check: $(op_json prod-eu "$fz" | jq -c .spec)"
    signed() { rec record verify -n prod-eu --stage payments --output-format json 2>/dev/null | jq -e --arg o "$fz" '[.records[] | select(.operation == $o)] | length == 1' >/dev/null; }
    eventually 60 signed || fail "the freeze is not a signed record of prod-eu: $(rec record verify -n prod-eu --stage payments 2>&1)"
    mode prod-eu.2.3.0 ok
    pass "2.3.0 failed its re-check after the rollback that was 2.4.0's one: prod-eu froze, and the freeze is a signed record naming 2.3.0 and the failed check"
  fi

  section "prod-us follows prod-eu's records and never takes the version prod-eu froze on"
  us kubectl apply -f - >/dev/null <<YAML
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata: {name: payments-eu, namespace: prod, labels: {delivery.pacto.trianalab.io/apply-ref: "true"}}
spec:
  project: default
  source: {repoURL: "oci://${REG_HOST}/story/deploy/payments", path: .}
  destination: {server: https://kubernetes.default.svc, namespace: prod-eu}
---
apiVersion: delivery.pacto.trianalab.io/v1alpha1
kind: Stage
metadata: {name: payments-eu, namespace: prod}
spec:
  service: {name: payments}
  environment: prod-us
  applyRef: {kind: Application, name: payments-eu}
  candidates: {upstreamRecords: {repository: "${REG_HOST}/pacto-delivery-streams", stream: "$eu"}}
  auto: true
  gates: {impact: {declaredConsumersOnly: true}}
YAML
  us_empty() { [ -z "$(us kubectl -n prod get stage payments-eu -o jsonpath='{.status.pointer}')" ]; }
  always 10 us_empty || fail "prod-us took a version prod-eu failed: $(us kubectl -n prod get stage payments-eu -o jsonpath='{.status.pointer}')"
  us kubectl -n prod delete operation payments-eu-2-3-0 --ignore-not-found --wait --timeout=60s >/dev/null \
    || fail "could not remove the previous payments-eu operation in prod-us"
  us "$PACTO" delivery propose -n prod --stage payments-eu --to "$a" --name payments-eu-2-3-0 --reason "2.3.0 froze prod-eu" >/dev/null \
    || fail "could not propose payments 2.3.0 to payments-eu in prod-us"
  eu_refused() { [ "$(us kubectl -n prod get operation payments-eu-2-3-0 -o jsonpath='{.status.outcome}')" = Rejected ]; }
  eventually 60 eu_refused || fail "prod-us did not refuse payments 2.3.0: $(us kubectl -n prod get operation payments-eu-2-3-0 -o jsonpath='{.status}')"
  local msg; msg="$(us kubectl -n prod get operation payments-eu-2-3-0 -o jsonpath='{.status.reason}: {.status.message}')"
  grep -qF "failed after release on the upstream Stage $eu" <<<"$msg" || fail "prod-us refused payments 2.3.0 for another reason than the freeze: $msg"
  pass "prod-us reads the freeze from prod-eu's records: it takes nothing and refuses payments 2.3.0: $msg"

  if [ "$(kubectl -n prod-eu get stage payments -o jsonpath='{.status.frozen}')" = true ]; then
    KUBECONFIG="$(person alice@example.com team-payments)" "$PACTO" delivery propose -n prod-eu --stage payments --type unfreeze \
      --name eu-unfreeze --reason "the failed check is understood" >/dev/null || fail "alice could not propose the unfreeze of prod-eu"
    wait_concluded prod-eu eu-unfreeze Succeeded/Unverified
    [ "$(kubectl -n prod-eu get stage payments -o jsonpath='{.status.frozen}')" != true ] || fail "prod-eu stayed frozen after alice unfroze it"
    unfreeze_signed() { rec record verify -n prod-eu --stage payments --output-format json 2>/dev/null | jq -e '[.records[] | select(.operation == "eu-unfreeze")] | length == 1' >/dev/null; }
    eventually 60 unfreeze_signed || fail "alice's unfreeze is not a signed record of prod-eu"
    pass "alice unfroze prod-eu, a signed record too"
  fi
  stop_pf
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
What is generated, not real: the payments workload (podinfo), its telemetry (a span generator), the call 2.4.2 makes (a sidecar) and pay-agent's eval runner.
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
  B9: carol rolls 3.0.0 back from the dashboard; B10 and the agent beat follow it in make e2e-delivery-demo-browser; then
       $k $r $p delivery blame -n prod --stage payments $t
       $k $p delivery undo -n prod <operation>
  The agent over remote MCP ($MCP_URL/mcp): get a Keycloak token for agent-bot
       curl -sk https://127.0.0.1:8443/realms/pacto/protocol/openid-connect/token -u agent-bot:agent-secret -d grant_type=client_credentials
       and call the pacto_delivery_* tools with it as a bearer token (there is no approve tool)
  Check a record yourself with cosign, as integrations/kubernetes/docs/delivery-records.md#verify-records shows (key: $d/keys/recorder.pub)
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
  CLUSTER="$US_CLUSTER" down_cluster
  down_cluster
  rm -rf "$DEMO"
}

case "$CMD" in
  up) trap 'rc=$?; stop_pf; [ $rc -eq 0 ] || { dump_diag "$NS"; dump_identity; }; exit $rc' EXIT; cmd_up ;;
  story) trap 'rc=$?; stop_pf; exit $rc' EXIT; cmd_story ;;
  browser) trap 'rc=$?; stop_pf; exit $rc' EXIT; cmd_browser ;;
  reach) trap 'rc=$?; stop_pf; exit $rc' EXIT; cmd_reach ;;
  status) cmd_status ;;
  logs) admin; dump_diag "$NS"; dump_identity; kubectl -n "$DELIVERY_NS" logs deploy/pacto-delivery --tail=100 2>&1 || true ;;
  down) cmd_down ;;
  all)
    rc=0; { bash "$0" up && bash "$0" story && bash "$0" browser && bash "$0" reach; } || rc=$?
    # A failed run in CI keeps the cluster for scripts/ci/kind-diagnostics.sh.
    { [ -n "${CI:-}" ] && [ $rc -ne 0 ]; } || bash "$0" down
    exit $rc ;;
  *) echo "usage: $0 up|story|status|logs|browser|reach|down|all"; exit 2 ;;
esac
