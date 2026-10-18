#!/usr/bin/env bash
# Kind acceptance for eBPF runtime contract conformance: the operator-managed node
# agent (a DaemonSet attaching a BTF tracepoint on inet_sock_set_state) publishes
# what the kernel saw per pod as NodeRuntimeObservation objects, and the operator
# folds that into every Pacto's verdict.
#
# Every case is a `scenario "<id>"` line, because the field coverage inventory
# (pkg/validation/schema/field-coverage.yaml) names these ids as the runtime proof
# of the contract fields, and pkg/validation/fieldcoverage_test.go refuses an id
# this script does not contain.
#
# What it proves against a real kernel, one kind node, nothing mocked:
#   - a conforming workload is Compliant with full coverage, its listener and its
#     outbound connection attributed to ITS pod and to no other;
#   - contradictions only the kernel can see (endpoints ready, nothing on the port;
#     a dependency the pod only fails to reach; a peer nobody declared) become
#     findings that cite the ebpf collector;
#   - the Kubernetes-API facets (protocol, exposure, workload kind, persistence,
#     configuration, health, metrics) each produce their documented finding;
#   - pod restarts and rollouts re-attribute by pod UID; an agent that stops
#     publishing reads stale, an agent that cannot attach reads failed, and both
#     recover.
#
# Contracts are published to an in-cluster registry rather than inlined: a
# dependency resolves its provider through the sibling Pacto's
# status.contract.resolvedRef, which an inline contract never has, and a
# configuration schema has to live in a bundle.
#
# Assertions over JSON are a typed decode in tests/acceptance/kind/conformancecheck.
# Assertions do not exit on the first miss: every scenario runs, and the summary
# at the end names each id PASS or FAIL.
#
# Subcommands:
#   (default) / up   build + provision + assert, then keep or tear down
#   status           Pactos and node objects of the running cluster
#   logs             dump diagnostics for the running cluster
#   down             tear the cluster down
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
CLUSTER="${KIND_CLUSTER:-pacto-runtime}"
NS=pacto-system
APP=conf
REG_HOST="pacto-registry.${NS}.svc.cluster.local:5000"
LOCAL_REG_PORT=5597
AGNHOST="registry.k8s.io/e2e-test-images/agnhost:2.53"
NRO=noderuntimeobservations.pacto.trianalab.io
# shellcheck source=tests/acceptance/kind/lib.sh
source "$(dirname "$0")/lib.sh"

CMD="${1:-run}"
case "$CMD" in
  status)
    use_existing_cluster "make e2e-runtime-conformance-kind-up"
    kubectl -n "$APP" get pactos
    kubectl -n "$NS" get "$NRO" -o wide
    exit 0 ;;
  logs)
    use_existing_cluster "make e2e-runtime-conformance-kind-up"
    dump_diag "$NS"
    kubectl -n "$NS" logs ds/pacto-node-agent --tail=200 || true
    exit 0 ;;
  down) down_cluster; exit 0 ;;
  run|up) : ;;
  *) echo "unknown subcommand: $CMD (use up|status|logs|down)"; exit 2 ;;
esac

SCENARIOS=()
FAILED=()
CURRENT=""
START=$SECONDS

# scenario ID DESCRIPTION — opens a case; every assertion after it is charged to ID.
scenario() {
  CURRENT="$1"
  SCENARIOS+=("$1")
  echo "== scenario $1: $2 =="
}

summary() {
  local id verdict
  echo "== runtime-conformance summary ($((SECONDS - START))s) =="
  for id in "${SCENARIOS[@]}"; do
    verdict=PASS
    [[ " ${FAILED[*]:-} " == *" $id "* ]] && verdict=FAIL
    echo "  $verdict  $id"
  done
}

# shellcheck disable=SC2154  # rc is assigned by rc=$? inside the trap body
trap 'rc=$?; [ $rc -ne 0 ] && [ -n "$CURRENT" ] && FAILED+=("$CURRENT"); [ ${#SCENARIOS[@]} -gt 0 ] && summary; if [ $rc -ne 0 ]; then dump_diag "$NS"; kubectl -n "$NS" logs ds/pacto-node-agent --tail=100 || true; kubectl -n "$NS" get "$NRO" -o yaml | tail -80 || true; fi; pkill -f "kubectl.*port-forward" 2>/dev/null || true; exit $rc' EXIT

# miss MSG — a failed assertion: reported, charged to the open scenario, not fatal.
miss() {
  echo "  FAIL: $*" >&2
  FAILED+=("${CURRENT:-setup}")
}

CHECK="$(mktemp -d)/conformancecheck"
( cd "$ROOT" && go build -o "$CHECK" ./tests/acceptance/kind/conformancecheck )

LAST=""
_pacto_holds() { # NAME CHECKER-FLAGS...
  local name="$1"; shift
  LAST="$(kubectl -n "$APP" get pacto "$name" -o json 2>&1 | "$CHECK" status "$@" 2>&1)"
}
_nro_holds() {
  LAST="$(kubectl -n "$NS" get "$NRO" -o json 2>&1 | "$CHECK" nro "$@" 2>&1)"
}

# expect_pacto NAME ROUNDS CHECKER-FLAGS... — the Pacto's status eventually satisfies the flags.
expect_pacto() {
  local name="$1" rounds="$2"; shift 2
  if eventually "$rounds" _pacto_holds "$name" "$@"; then pass "$name $*"; else miss "$LAST"; fi
}

# expect_nro ROUNDS CHECKER-FLAGS... — the node objects eventually satisfy the flags.
expect_nro() {
  local rounds="$1"; shift
  if eventually "$rounds" _nro_holds "$@"; then pass "kernel $*"; else miss "kernel: $LAST"; fi
}

# pod_uid APP-LABEL — the UID of a live (not terminating) pod of the workload.
pod_uid() {
  kubectl -n "$APP" get pods -l "app=$1" \
    -o go-template='{{range .items}}{{if not .metadata.deletionTimestamp}}{{.metadata.uid}}{{"\n"}}{{end}}{{end}}' | head -1
}
cluster_ip() { kubectl -n "$APP" get svc "$1" -o jsonpath='{.spec.clusterIP}'; }
rollout() { kubectl -n "$APP" rollout status "$1" --timeout=180s >/dev/null; }

VER="$(release_version kubernetes)"
CORE="$(release_version core)"
OP_IMG="localhost:5001/pacto-operator/pacto-controller:${VER}"
OP_REPO="localhost:5001/pacto-operator/pacto-controller"
DASH_IMG="localhost:5001/pacto-dashboard:${CORE}"

build_operator_images "$OP_IMG" "$DASH_IMG" "$VER"
echo "== package the chart =="
CHART="$(package_chart "$PACTO_CHART")"

ensure_cluster
docker pull "$AGNHOST" >/dev/null
load_images "$OP_IMG" "$DASH_IMG" "$AGNHOST"

# The node agent needs hostPID and BPF capabilities, which only the privileged Pod
# Security level admits. The chart documents this and deliberately does not label.
kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl label namespace "$NS" pod-security.kubernetes.io/enforce=privileged --overwrite >/dev/null
kubectl create namespace "$APP" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

echo "== in-cluster registry: contracts resolve by OCI ref, so dependencies find their sibling =="
install_registry
PACTO_BIN="$(build_pacto)"
pf "$LOCAL_REG_PORT" svc/pacto-registry 5000 >/dev/null

# publish NAME — the contract on stdin becomes NAME:1.0.0 with the shared spec files.
publish() {
  local dir digest
  dir="$(mktemp -d)"
  cat > "$dir/pacto.yaml"
  printf 'openapi: "3.0.0"\ninfo: { title: %s, version: "1.0.0" }\npaths: {}\n' "$1" > "$dir/openapi.yaml"
  printf 'syntax = "proto3";\npackage demo;\nservice Demo {}\n' > "$dir/api.proto"
  cat > "$dir/schema.json" <<'JSON'
{"type": "object", "required": ["mode"], "properties": {"mode": {"type": "string", "enum": ["fast", "safe"]}}}
JSON
  digest="$(push_bundle "$PACTO_BIN" "$LOCAL_REG_PORT" "$dir" "$1:1.0.0")"
  [ -n "$digest" ] || fail "could not publish the $1 contract"
}

DEP="oci://${REG_HOST}/demo"
HDR='pactoVersion: "2.0"'
API='{name: api, type: openapi, ref: openapi.yaml, visibility: internal}'
STATELESS='{type: stateless, persistence: {scope: local, durability: ephemeral}, dataCriticality: low}'
PERSISTENT='{type: stateful, persistence: {scope: local, durability: persistent}, dataCriticality: high}'

publish backend <<YAML
$HDR
service: {name: backend, version: "1.0.0"}
interfaces: [$API]
workload: service
YAML
publish conforming <<YAML
$HDR
service: {name: conforming, version: "1.0.0"}
interfaces: [$API]
configurations: [{name: app, schema: schema.json, required: true}]
dependencies: [{name: backend, ref: "$DEP/backend", required: true, compatibility: "^1.0.0"}]
workload: service
state: $STATELESS
capabilities: [{type: health, binding: {type: http, interface: api, path: /healthz}}]
YAML
publish deaf <<YAML
$HDR
service: {name: deaf, version: "1.0.0"}
interfaces: [$API]
workload: service
YAML
publish version-client <<YAML
$HDR
service: {name: version-client, version: "1.0.0"}
dependencies:
  - {name: backend, ref: "$DEP/backend", required: true, compatibility: "^2.0.0"}
  - {name: conforming, ref: "$DEP/conforming", required: true, compatibility: "^1.0.0"}
YAML
publish undeclared-client <<YAML
$HDR
service: {name: undeclared-client, version: "1.0.0"}
dependencies: [{name: backend, ref: "$DEP/backend", required: true, compatibility: "^1.0.0"}]
YAML
publish refused-client <<YAML
$HDR
service: {name: refused-client, version: "1.0.0"}
dependencies: [{name: deaf, ref: "$DEP/deaf", required: true, compatibility: "^1.0.0"}]
YAML
publish optional-client <<YAML
$HDR
service: {name: optional-client, version: "1.0.0"}
dependencies: [{name: deaf, ref: "$DEP/deaf", required: false, compatibility: "^1.0.0"}]
YAML
publish wl-mismatch <<YAML
$HDR
service: {name: wl-mismatch, version: "1.0.0"}
workload: job
YAML
publish wl-job <<YAML
$HDR
service: {name: wl-job, version: "1.0.0"}
workload: job
YAML
publish wl-cron <<YAML
$HDR
service: {name: wl-cron, version: "1.0.0"}
workload: scheduled
YAML
for name in persist-ephemeral persist-shared persist-durable; do
  publish "$name" <<YAML
$HDR
service: {name: $name, version: "1.0.0"}
workload: service
state: $PERSISTENT
YAML
done
publish persist-shared-scope <<YAML
$HDR
service: {name: persist-shared-scope, version: "1.0.0"}
workload: service
state: {type: stateful, persistence: {scope: shared, durability: persistent}, dataCriticality: high}
YAML
publish cfg-required <<YAML
$HDR
service: {name: cfg-required, version: "1.0.0"}
configurations:
  - {name: absent, schema: schema.json, required: true}
  - {name: mismatch, schema: schema.json, required: true}
YAML
publish cfg-optional <<YAML
$HDR
service: {name: cfg-optional, version: "1.0.0"}
configurations: [{name: extra, schema: schema.json, required: false}]
YAML
publish protocol <<YAML
$HDR
service: {name: protocol, version: "1.0.0"}
interfaces: [{name: rpc, type: grpc, ref: api.proto, visibility: public}]
YAML
publish visibility <<YAML
$HDR
service: {name: visibility, version: "1.0.0"}
interfaces: [{name: api, type: openapi, ref: openapi.yaml}]
YAML
publish public-api <<YAML
$HDR
service: {name: public-api, version: "1.0.0"}
interfaces: [{name: api, type: openapi, ref: openapi.yaml, visibility: public}]
YAML
publish health-404 <<YAML
$HDR
service: {name: health-404, version: "1.0.0"}
interfaces: [{name: api, type: openapi, ref: openapi.yaml, visibility: public}]
capabilities: [{type: health, binding: {type: http, interface: api, path: /nope}}]
YAML
publish multi <<YAML
$HDR
service: {name: multi, version: "1.0.0"}
interfaces:
  - {name: rpc, type: grpc, ref: api.proto}
  - {name: api, type: openapi, ref: openapi.yaml, visibility: public}
capabilities: [{type: health, binding: {type: http, interface: api, path: /nope}}]
workload: job
YAML
publish metrics <<YAML
$HDR
service: {name: metrics, version: "1.0.0"}
interfaces: [$API]
capabilities: [{type: metrics, binding: {type: http, interface: api, path: /metrics}}]
YAML
publish unbound <<YAML
$HDR
service: {name: unbound, version: "1.0.0"}
interfaces: [$API]
YAML

echo "== install the operator with the node agent (publish every 5s, 20s stabilization window) =="
SETS=(--set image.repository="$OP_REPO" --set image.tag="$VER" --set image.pullPolicy=Never
      --set dashboard.enabled=false
      --set "insecureRegistries[0]=${REG_HOST}"
      --set nodeAgent.enabled=true --set nodeAgent.publishInterval=5s
      --set controller.enableProbing=true --set controller.enableMetricsObservation=true
      --set controller.stabilizationWindow=20s)
helm upgrade --install pacto-operator "$CHART" -n "$NS" "${SETS[@]}" --wait --timeout 240s
wait_ready pacto-operator
eventually 40 kubectl -n "$NS" get ds pacto-node-agent >/dev/null 2>&1 || fail "the operator never created the node agent DaemonSet"
kubectl -n "$NS" rollout status ds/pacto-node-agent --timeout=180s
expect_nro 40 -state Running

echo "== workloads (all agnhost, loaded into the node) =="
kubectl -n "$APP" apply -f - >/dev/null <<YAML
apiVersion: v1
kind: ConfigMap
metadata: {name: conforming-config}
data: {config.json: '{"mode": "fast"}'}
---
apiVersion: v1
kind: ConfigMap
metadata: {name: bad-config}
data: {config.json: '{"mode": 7}'}
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata: {name: shared-data}
spec: {accessModes: [ReadWriteOnce], resources: {requests: {storage: 16Mi}}}
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata: {name: durable-data}
spec: {accessModes: [ReadWriteOnce], resources: {requests: {storage: 16Mi}}}
YAML

# workload NAME REPLICAS CONTAINERS-YAML [VOLUMES-YAML] — a Deployment labelled app=NAME.
workload() {
  kubectl -n "$APP" apply -f - >/dev/null <<YAML
apiVersion: apps/v1
kind: Deployment
metadata: {name: $1}
spec:
  replicas: $2
  selector: {matchLabels: {app: $1}}
  template:
    metadata: {labels: {app: $1}}
    spec:
      terminationGracePeriodSeconds: 1
      containers: $3
      volumes: ${4:-[]}
YAML
}
# service NAME [TYPE] — port 80 to the pods' 8080.
service() {
  kubectl -n "$APP" apply -f - >/dev/null <<YAML
apiVersion: v1
kind: Service
metadata: {name: $1}
spec: {type: ${2:-ClusterIP}, selector: {app: $1}, ports: [{name: http, port: 80, targetPort: 8080}]}
YAML
}

NETEXEC="{name: server, image: $AGNHOST, imagePullPolicy: Never, args: [netexec, --http-port=8080], ports: [{containerPort: 8080}], readinessProbe: {httpGet: {path: /healthz, port: 8080}, periodSeconds: 2}}"
# Ready by exec, serving nothing: Kubernetes alone believes this pod serves 8080.
DEAF="{name: server, image: $AGNHOST, imagePullPolicy: Never, args: [pause], ports: [{containerPort: 8080}], readinessProbe: {exec: {command: ['true']}, periodSeconds: 2}}"
PAUSE="{name: main, image: $AGNHOST, imagePullPolicy: Never, args: [pause]}"
loop() { # NAME HOST... — a client container that connects to each HOST:80 every 2s
  local name="$1"; shift
  echo "{name: $name, image: $AGNHOST, imagePullPolicy: Never, command: [sh, -c, 'while true; do for h in $*; do nc -z -w 2 \$h 80; done; sleep 2; done']}"
}

workload backend 1 "[$NETEXEC]"; service backend
workload conforming 1 "[$NETEXEC, $(loop client backend)]"; service conforming
workload deaf 1 "[$DEAF]"; service deaf
workload caller 1 "[$(loop client backend conforming)]"
workload deaf-caller 1 "[$(loop client deaf)]"
workload probe-target 1 "[{name: server, image: $AGNHOST, imagePullPolicy: Never, args: [test-webserver, --port=8080], ports: [{containerPort: 8080}]}]"
service probe-target NodePort
# The same pods behind a LoadBalancer whose status carries a public address. Kind
# runs no load balancer controller, so nothing overwrites the patched status.
kubectl -n "$APP" apply -f - >/dev/null <<YAML
apiVersion: v1
kind: Service
metadata: {name: probe-lb}
spec: {type: LoadBalancer, selector: {app: probe-target}, ports: [{name: http, port: 80, targetPort: 8080}]}
YAML
kubectl -n "$APP" patch svc probe-lb --subresource=status --type=merge \
  -p '{"status":{"loadBalancer":{"ingress":[{"ip":"203.0.113.10"}]}}}' >/dev/null
workload metrics 1 "[{name: server, image: $AGNHOST, imagePullPolicy: Never, args: [porter], env: [{name: SERVE_PORT_8080, value: \"# TYPE up gauge\\nup 1\\n\"}], ports: [{containerPort: 8080}]}]"
service metrics
workload scratch 1 "[$PAUSE]" "[{name: data, emptyDir: {}}]"
workload shared 2 "[$PAUSE]" "[{name: data, persistentVolumeClaim: {claimName: shared-data}}]"
workload durable 1 "[$PAUSE]" "[{name: data, persistentVolumeClaim: {claimName: durable-data}}]"
kubectl -n "$APP" apply -f - >/dev/null <<YAML
apiVersion: batch/v1
kind: Job
metadata: {name: batch}
spec:
  backoffLimit: 0
  template:
    spec: {restartPolicy: Never, terminationGracePeriodSeconds: 1, containers: [$PAUSE]}
---
apiVersion: batch/v1
kind: CronJob
metadata: {name: cron}
spec:
  schedule: "0 0 1 1 *"
  jobTemplate:
    spec:
      template:
        spec: {restartPolicy: Never, containers: [$PAUSE]}
YAML
for d in backend conforming deaf caller deaf-caller probe-target metrics scratch shared durable; do
  rollout "deploy/$d" || fail "deployment $d never rolled out"
done

# pacto NAME TARGET [CONTRACT] — a Pacto on the published CONTRACT:1.0.0 (default
# NAME), re-checked every 30s.
pacto() {
  kubectl -n "$APP" apply -f - >/dev/null <<YAML
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: {name: $1}
spec:
  contractRef: {oci: "${REG_HOST}/demo/${3:-$1}:1.0.0"}
  checkIntervalSeconds: 30
  target: $2
YAML
}
HTTP80='[{interface: api, servicePort: 80}]'
pacto backend "{serviceName: backend, interfaceBindings: $HTTP80}"
pacto conforming "{serviceName: conforming, interfaceBindings: $HTTP80, configBindings: [{configuration: app, kind: ConfigMap, name: conforming-config, key: config.json, format: json}]}"
pacto deaf "{serviceName: deaf, interfaceBindings: $HTTP80}"
pacto version-client "{workloadRef: {name: caller}}"
pacto undeclared-client "{workloadRef: {name: caller}}"
pacto refused-client "{workloadRef: {name: deaf-caller}}"
pacto optional-client "{workloadRef: {name: deaf-caller}}"
pacto wl-mismatch "{workloadRef: {name: backend, kind: Deployment}}"
pacto wl-job "{workloadRef: {name: batch, kind: Job}}"
pacto wl-cron "{workloadRef: {name: cron, kind: CronJob}}"
pacto persist-ephemeral "{workloadRef: {name: scratch}}"
pacto persist-shared "{workloadRef: {name: shared}}"
pacto persist-durable "{workloadRef: {name: durable}}"
pacto persist-shared-scope "{workloadRef: {name: shared}}"
pacto cfg-required "{workloadRef: {name: backend}, configBindings: [{configuration: absent, kind: ConfigMap, name: missing-config, key: config.json, format: json}, {configuration: mismatch, kind: ConfigMap, name: bad-config, key: config.json, format: json}]}"
pacto cfg-optional "{workloadRef: {name: backend}, configBindings: [{configuration: extra, kind: ConfigMap, name: missing-config, key: config.json, format: json}]}"
pacto protocol "{serviceName: probe-target, interfaceBindings: [{interface: rpc, servicePort: 80}]}"
pacto visibility "{serviceName: probe-lb, interfaceBindings: $HTTP80}"
pacto visibility-nodeport "{serviceName: probe-target, interfaceBindings: $HTTP80}" visibility
pacto public-api "{serviceName: probe-lb, interfaceBindings: $HTTP80}"
pacto health-404 "{serviceName: probe-target, interfaceBindings: $HTTP80}"
pacto multi "{serviceName: probe-lb, workloadRef: {name: probe-target, kind: Deployment}, interfaceBindings: [{interface: rpc, servicePort: 80}, {interface: api, servicePort: 80}]}"
pacto metrics "{serviceName: metrics, interfaceBindings: $HTTP80}"
pacto unbound "{workloadRef: {name: backend}}"

# The violations every non-conforming workload below produces. None may ever land
# on the conforming Pacto, which shares the node, the kernel and the agent.
VIOLATIONS=(INTERFACE_ABSENT INTERFACE_PROTOCOL_MISMATCH INTERFACE_VISIBILITY_MISMATCH DEPENDENCY_UNREACHABLE
            DEPENDENCY_VERSION_MISMATCH DEPENDENCY_UNDECLARED WORKLOAD_MISMATCH PERSISTENCE_MISMATCH
            CONFIGURATION_ABSENT CONFIGURATION_MISMATCH CAPABILITY_ABSENT EVIDENCE_STALE COLLECTION_FAILED)
FORBID=()
for c in "${VIOLATIONS[@]}"; do FORBID+=(-forbid "$c"); done

# Rounds are 3s. The slowest first verdict is a kernel negative: the agent's next
# /proc baseline after the pod started (resync every 60s), then the 20s window,
# then the next reconcile (30s).
R=80

scenario "conforming-workload" "openapi + health + dependency + configuration + stateless service, all satisfied"
expect_pacto conforming "$R" -status Compliant -no-findings -covered
expect_pacto backend "$R" -status Compliant -no-findings -covered

scenario "health-capability" "/healthz answers 200 (satisfied); /nope answers 404 (CAPABILITY_ABSENT with probing)"
expect_pacto conforming 5 -forbid CAPABILITY_ABSENT -covered
expect_pacto health-404 "$R" -status NonCompliant -expect CAPABILITY_ABSENT:error:capability/health

scenario "listener-absent" "endpoints ready by exec probe, the kernel sees no listener on 8080"
expect_pacto deaf "$R" -status NonCompliant -expect INTERFACE_ABSENT:error:interface/api -collector INTERFACE_ABSENT=ebpf

scenario "protocol-mismatch" "a grpc interface answered by an HTTP/1 server"
expect_pacto protocol "$R" -status NonCompliant -expect INTERFACE_PROTOCOL_MISMATCH:error:interface/rpc -forbid INTERFACE_VISIBILITY_MISMATCH

scenario "visibility-mismatch" "an internal (omitted) interface behind a LoadBalancer with a public address; NodePort stays unverified; public satisfied"
expect_pacto visibility "$R" -status NonCompliant -expect INTERFACE_VISIBILITY_MISMATCH:error:interface/api -forbid INTERFACE_PROTOCOL_MISMATCH
# A NodePort proves nothing about reachability (the node network may be private): exposure unknown, never a mismatch.
expect_pacto visibility-nodeport "$R" -status Compliant -forbid INTERFACE_VISIBILITY_MISMATCH -unverified "interfaces[name=api].visibility"
# The positive: a public interface with a public address is judged and satisfied.
expect_pacto public-api "$R" -status Compliant -no-findings -covered -verified "interfaces[name=api].visibility"

scenario "dependency-reachable-kernel" "the kernel saw the conforming pod connect to its provider"
BACKEND_IP="$(cluster_ip backend)"
CONFORMING_UID="$(pod_uid conforming)"
expect_nro "$R" -uid "$CONFORMING_UID" -connected "$BACKEND_IP:80"
expect_pacto conforming 5 -forbid DEPENDENCY_UNREACHABLE -forbid DEPENDENCY_UNDECLARED
# A finding on an Observed dependency carries the reachability evidence: ebpf in the collector means the kernel decided it.
expect_pacto version-client "$R" -collector DEPENDENCY_VERSION_MISMATCH=ebpf

scenario "dependency-refused" "the provider is ready but refuses: the kernel sees only failed connects"
DEAF_IP="$(cluster_ip deaf)"
expect_nro "$R" -uid "$(pod_uid deaf-caller)" -refused "$DEAF_IP:80"
expect_pacto refused-client "$R" -status NonCompliant -expect DEPENDENCY_UNREACHABLE:error:dependency/deaf -collector DEPENDENCY_UNREACHABLE=ebpf

scenario "dependency-optional-warning" "the same refused provider, declared optional"
expect_pacto optional-client "$R" -status Warning -expect DEPENDENCY_UNREACHABLE:warning:dependency/deaf

scenario "dependency-version-mismatch" "provider contract 1.0.0, compatibility ^2.0.0"
expect_pacto version-client "$R" -status NonCompliant -expect DEPENDENCY_VERSION_MISMATCH:error:dependency/backend -forbid DEPENDENCY_UNDECLARED

scenario "dependency-undeclared" "the caller also reaches the Pacto-managed conforming service it never declared"
expect_pacto undeclared-client "$R" -status Warning -expect DEPENDENCY_UNDECLARED:warning:dependency/conforming -collector DEPENDENCY_UNDECLARED=ebpf

scenario "workload-mismatch" "contract says job, the explicit workload is a Deployment"
expect_pacto wl-mismatch "$R" -status NonCompliant -expect WORKLOAD_MISMATCH:error

scenario "workload-job" "contract says job, the workload is a running Job"
expect_pacto wl-job "$R" -status Compliant -forbid WORKLOAD_MISMATCH -forbid EVIDENCE_MISSING -covered

scenario "workload-scheduled" "contract says scheduled, the workload is a CronJob"
expect_pacto wl-cron "$R" -status Compliant -forbid WORKLOAD_MISMATCH -forbid EVIDENCE_MISSING -covered

scenario "persistence-durability" "persistent state declared, only an emptyDir mounted"
expect_pacto persist-ephemeral "$R" -status NonCompliant -expect PERSISTENCE_MISMATCH:error

scenario "persistence-scope" "local scope declared, one claim mounted by two replicas; a single-replica claim satisfies; shared scope on the shared claim satisfies"
expect_pacto persist-shared "$R" -status NonCompliant -expect PERSISTENCE_MISMATCH:error
expect_pacto persist-durable "$R" -status Compliant -forbid PERSISTENCE_MISMATCH -covered
expect_pacto persist-shared-scope "$R" -status Compliant -forbid PERSISTENCE_MISMATCH -covered -verified "state.persistence.scope"

scenario "configuration-absent" "the bound ConfigMap does not exist (after the window)"
expect_pacto cfg-required "$R" -status NonCompliant -expect CONFIGURATION_ABSENT:error:configuration/absent

scenario "configuration-mismatch" "the bound ConfigMap violates the configuration schema"
expect_pacto cfg-required "$R" -expect CONFIGURATION_MISMATCH:error:configuration/mismatch

scenario "configuration-optional" "an optional configuration whose ConfigMap is missing"
expect_pacto cfg-optional "$R" -status Warning -expect CONFIGURATION_ABSENT:warning:configuration/extra

scenario "metrics-capability" "Prometheus text exposition on the bound interface"
expect_pacto metrics "$R" -status Compliant -no-findings -covered

scenario "unbound-interface" "an interface without a Service binding cannot be observed"
expect_pacto unbound "$R" -status Unknown -expect OBSERVATION_UNSUPPORTED:unknown:interface/api

scenario "multiple-violations" "one workload: wrong protocol, wrong exposure, missing health endpoint, wrong workload kind"
expect_pacto multi "$R" -status NonCompliant -expect INTERFACE_PROTOCOL_MISMATCH:error:interface/rpc \
  -expect INTERFACE_VISIBILITY_MISMATCH:error:interface/rpc -expect CAPABILITY_ABSENT:error:capability/health \
  -expect WORKLOAD_MISMATCH:error

scenario "same-node-attribution" "violations on the shared node never leak onto the conforming Pacto"
expect_pacto conforming 5 -status Compliant "${FORBID[@]}"
expect_nro 5 -uid "$CONFORMING_UID" -listener 8080 -connected "$BACKEND_IP:80" -no-peer "$DEAF_IP:80"
expect_nro 5 -uid "$(pod_uid deaf)" -no-listener 8080
expect_nro 5 -uid "$(pod_uid deaf-caller)" -no-peer "$BACKEND_IP:80"

echo "== static validation: an invalid contract is Invalid, not a runtime verdict =="
kubectl -n "$APP" apply -f - >/dev/null <<'YAML'
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: {name: invalid}
spec:
  contractRef:
    inline: |
      pactoVersion: "2.0"
      service:
        name: invalid
        version: "1.0.0"
        owner: {team: demo, contacts: [{type: fax, value: "+1 555 0100"}]}
  target: {workloadRef: {name: backend}}
YAML
_invalid() { [ "$(pacto_status "$APP" invalid)" = Invalid ]; }
if eventually 40 _invalid; then pass "invalid contract reads Invalid"; else miss "invalid contract read '$(pacto_status "$APP" invalid)'"; fi

scenario "pod-restart" "the conforming pod is replaced; the kernel re-attributes by the new UID"
kubectl -n "$APP" delete pod -l app=conforming --wait=false >/dev/null
rollout deploy/conforming
_has_pod() { [ -n "$(pod_uid "$1")" ]; }
eventually 20 _has_pod conforming || miss "no conforming pod came back"
NEW_UID="$(pod_uid conforming)"
if [ "$NEW_UID" != "$CONFORMING_UID" ]; then pass "a new pod UID ($NEW_UID)"; else miss "the pod UID did not change"; fi
expect_nro "$R" -uid "$NEW_UID" -listener 8080 -connected "$BACKEND_IP:80"
expect_nro "$R" -absent-uid "$CONFORMING_UID"
expect_pacto conforming "$R" -status Compliant -no-findings -covered

scenario "rollout-replacement" "a rollout to a variant that serves nothing, then a rollback"
kubectl -n "$APP" patch deploy conforming --type=json -p "[
  {\"op\": \"replace\", \"path\": \"/spec/template/spec/containers/0/args\", \"value\": [\"pause\"]},
  {\"op\": \"replace\", \"path\": \"/spec/template/spec/containers/0/readinessProbe\", \"value\": {\"exec\": {\"command\": [\"true\"]}, \"periodSeconds\": 2}}]" >/dev/null
rollout deploy/conforming
expect_pacto conforming "$R" -status NonCompliant -expect INTERFACE_ABSENT:error:interface/api -collector INTERFACE_ABSENT=ebpf
kubectl -n "$APP" patch deploy conforming --type=json -p "[
  {\"op\": \"replace\", \"path\": \"/spec/template/spec/containers/0/args\", \"value\": [\"netexec\", \"--http-port=8080\"]},
  {\"op\": \"replace\", \"path\": \"/spec/template/spec/containers/0/readinessProbe\", \"value\": {\"httpGet\": {\"path\": \"/healthz\", \"port\": 8080}, \"periodSeconds\": 2}}]" >/dev/null
rollout deploy/conforming
expect_pacto conforming "$R" -status Compliant -no-findings -covered

scenario "agent-failed" "the agent loses CAP_BPF, cannot attach, and reports Failed"
# A strategic merge on the capabilities list: the operator re-applies the
# DaemonSet on its own interval, so the window to observe this is that interval.
CAPS="$(kubectl -n "$NS" get ds pacto-node-agent -o jsonpath='{.spec.template.spec.containers[0].securityContext.capabilities.add}')"
NOBPF="$(printf '%s' "$CAPS" | tr -d '[]"' | tr ',' '\n' | grep -vx BPF | sed 's/.*/"&"/' | paste -sd, -)"
kubectl -n "$NS" patch ds pacto-node-agent --type=json \
  -p "[{\"op\": \"replace\", \"path\": \"/spec/template/spec/containers/0/securityContext/capabilities/add\", \"value\": [${NOBPF}]}]" >/dev/null
kubectl -n "$NS" rollout status ds/pacto-node-agent --timeout=120s >/dev/null || true
expect_nro 40 -state Failed
expect_pacto conforming 40 -status Unknown -expect COLLECTION_FAILED:unknown:interface/api
kubectl -n "$NS" rollout undo ds/pacto-node-agent >/dev/null
kubectl -n "$NS" rollout status ds/pacto-node-agent --timeout=120s >/dev/null
expect_nro 40 -state Running
expect_pacto conforming "$R" -status Compliant -no-findings

scenario "agent-stopped-stale" "the agent is removed while kernel evidence stays on: listener evidence reads stale"
helm upgrade pacto-operator "$CHART" -n "$NS" "${SETS[@]}" --set nodeAgent.enabled=false \
  --set controller.kernelEvidence.enabled=true --wait --timeout 240s >/dev/null
expect_pacto conforming "$R" -status Unknown -expect EVIDENCE_STALE:unknown:interface/api
helm upgrade pacto-operator "$CHART" -n "$NS" "${SETS[@]}" --wait --timeout 240s >/dev/null
eventually 40 kubectl -n "$NS" get ds pacto-node-agent >/dev/null 2>&1 || miss "the node agent was not re-created"
kubectl -n "$NS" rollout status ds/pacto-node-agent --timeout=180s >/dev/null || miss "the node agent did not roll out again"
expect_nro 40 -state Running
expect_pacto conforming "$R" -status Compliant -no-findings

CURRENT=""
[ ${#FAILED[@]} -eq 0 ] || fail "${#FAILED[@]} assertion(s) failed in: $(printf '%s\n' "${FAILED[@]}" | sort -u | paste -sd' ' -)"
keep_or_teardown "$NS" "$CLUSTER" delete_cluster
