#!/usr/bin/env bash
# Prove the OPERATOR-MANAGED observation packaging against a real cluster: a
# declarative Helm value naming an offline OTLP/JSON trace export becomes a
# read-only mount, reaches the dashboard's configuration under the name the
# values declared, and shows up as a Data Source whose observed edge reconciles
# against a declared one.
#
# Deliberately narrow: this is the packaging vertical, asserted at the API level,
# with no browser leg. The broad live Product journey lives in
# tests/e2e/kind/operational-graph.sh.
#
# What it proves, end to end:
#   1. an externally managed PVC holding a trace export is mounted read-only,
#   2. a ConfigMap-backed source is mounted read-only alongside it,
#   3. the dashboard is configured with both, under their declared names,
#   4. the healthy source's observed edge reaches the fleet attributed to that
#      name, naming the same pair the operator reconciled as declared,
#   5. a malformed source is explicit unavailable knowledge, not a silent gap,
#   6. changing sources rolls the dashboard and leaves no orphaned wiring, and a
#      source that goes unreadable leaves the dashboard alive with its other
#      sources still answering.
#
# Subcommands (driven by the Makefile aliases):
#   (default) / up   build + provision + assert, then keep or tear down
#   status           show the observation wiring for the running cluster
#   logs             dump component logs for the running cluster
#   down             tear the cluster down
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
CLUSTER="${KIND_CLUSTER:-pacto-obs}"
NS=pacto-system
DEMO_NS=demo
LOCAL_DASH_PORT=8081
MOUNT_ROOT=/var/lib/pacto/observation
# shellcheck source=tests/e2e/kind/lib.sh
source "$(dirname "$0")/lib.sh"

use_existing_cluster() {
  kind get clusters | grep -qx "$CLUSTER" || { echo "no kind cluster '$CLUSTER' — run 'make e2e-observation-kind-up' first"; exit 1; }
  KUBECONFIG="$(mktemp)"; export KUBECONFIG; kind get kubeconfig --name "$CLUSTER" > "$KUBECONFIG"
}

CMD="${1:-run}"
case "$CMD" in
  status)
    use_existing_cluster
    echo "== pods =="; kubectl -n "$NS" get pods
    echo "== pvc =="; kubectl -n "$NS" get pvc
    echo "== dashboard observation volumes =="
    kubectl -n "$NS" get deployment pacto-dashboard -o jsonpath='{range .spec.template.spec.volumes[*]}{.name}{"\n"}{end}' 2>/dev/null || true
    echo "== dashboard observation mounts =="
    kubectl -n "$NS" get deployment pacto-dashboard -o jsonpath='{range .spec.template.spec.containers[0].volumeMounts[*]}{.name}{" "}{.mountPath}{" readOnly="}{.readOnly}{"\n"}{end}' 2>/dev/null || true
    exit 0 ;;
  logs) use_existing_cluster; dump_diag "$NS"; exit 0 ;;
  down)
    if kind get clusters | grep -qx "$CLUSTER"; then kind delete cluster --name "$CLUSTER"; echo "cluster '$CLUSTER' deleted"; else echo "no cluster '$CLUSTER'"; fi
    exit 0 ;;
  run|up) : ;;
  *) echo "unknown subcommand: $CMD (use up|status|logs|down)"; exit 2 ;;
esac

# shellcheck disable=SC2154  # rc is assigned by rc=$? inside the trap body
trap 'rc=$?; [ $rc -ne 0 ] && dump_diag "$NS"; pkill -f "kubectl.*port-forward" 2>/dev/null || true; exit $rc' EXIT

pass() { echo "  PASS: $1"; }
fail() { echo "  FAIL: $1"; exit 1; }
obs_teardown() { kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true; }
pf() {
  local lport="$1" target="$2" rport="$3"
  kubectl -n "$NS" port-forward "$target" "${lport}:${rport}" >/dev/null 2>&1 &
  local pid=$!; sleep 2; echo "$pid"
}
wait_ready() { kubectl -n "$NS" rollout status "deployment/$1" --timeout="${2:-180s}"; }
# The kind runners have python3 but not necessarily jq, so assertions over JSON
# are python expressions reading stdin (same choice as the other kind scripts).
jqp() { python3 -c "$1"; }

node "$ROOT/release/scripts/build-release-plan.mjs" >/dev/null 2>&1
VER="$(python3 -c 'import json;print(json.load(open("'"$ROOT"'/release/release-plan.json"))["groups"]["kubernetes"]["version"])')"
CORE="$(python3 -c 'import json;print(json.load(open("'"$ROOT"'/release/release-plan.json"))["groups"]["core"]["version"])')"
OP_REPO="localhost:5001/pacto-operator/pacto-controller"
OP_IMG="${OP_REPO}:${VER}"
DASH_IMG="localhost:5001/pacto-dashboard:${CORE}"

echo "== build the operator + dashboard images =="
docker build --load -f "$ROOT/Dockerfile" -t "$DASH_IMG" "$ROOT"
docker build --load -f "$ROOT/integrations/kubernetes/Dockerfile" \
  --build-arg VERSION="$VER" --build-arg DASHBOARD_IMAGE="$DASH_IMG" -t "$OP_IMG" "$ROOT"

echo "== package the operator chart =="
rm -rf /tmp/pacto-obs-charts; mkdir -p /tmp/pacto-obs-charts
helm package "$ROOT/integrations/kubernetes/charts/pacto-operator" -d /tmp/pacto-obs-charts >/dev/null
CHART="$(ls /tmp/pacto-obs-charts/pacto-operator-*.tgz)"

kind get clusters | grep -qx "$CLUSTER" || kind create cluster --name "$CLUSTER" --wait 90s
for img in "$DASH_IMG" "$OP_IMG"; do
  kind load docker-image "$img" --name "$CLUSTER"
done
KUBECONFIG="$(mktemp)"; export KUBECONFIG; kind get kubeconfig --name "$CLUSTER" > "$KUBECONFIG"
kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

echo "== an externally managed PVC holds the trace export (Pacto never writes it) =="
# The writer Job stands in for whatever really produces the export: it is the
# PVC's first consumer (kind's local-path provisioner binds on first use), writes
# one OTLP/JSON file and exits. Pacto only ever reads it, read-only. Reusing the
# already-loaded dashboard image keeps this hermetic — no extra registry pull.
kubectl -n "$NS" apply -f - >/dev/null <<YAML
apiVersion: v1
kind: PersistentVolumeClaim
metadata: { name: orders-trace-export }
spec:
  accessModes: [ ReadWriteOnce ]
  resources: { requests: { storage: 64Mi } }
---
apiVersion: batch/v1
kind: Job
metadata: { name: trace-writer }
spec:
  backoffLimit: 2
  template:
    spec:
      restartPolicy: Never
      securityContext: { runAsUser: 0 }
      containers:
      - name: writer
        image: ${DASH_IMG}
        imagePullPolicy: Never
        command: ["sh", "-c"]
        args:
        - |
          cat > /data/traces.json <<'JSON'
          {"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"orders"}}]},
           "scopeSpans":[{"spans":[{"kind":3,"attributes":[{"key":"peer.service","value":{"stringValue":"checkout"}}]}]}]}]}
          JSON
          chmod 0644 /data/traces.json
        volumeMounts: [ { name: export, mountPath: /data } ]
      volumes:
      - name: export
        persistentVolumeClaim: { claimName: orders-trace-export }
YAML
kubectl -n "$NS" wait --for=condition=complete job/trace-writer --timeout=180s >/dev/null \
  && pass "trace export written to the externally managed PVC" || fail "the trace writer did not complete"

echo "== a second source, ConfigMap-backed, carrying a MALFORMED export =="
kubectl -n "$NS" create configmap broken-trace-export --from-literal=traces.json='{not json' \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null

echo "== install the operator with two declared observation sources =="
helm install pacto-operator "$CHART" -n "$NS" \
  --set image.repository="$OP_REPO" --set image.tag="$VER" --set image.pullPolicy=Never \
  --set dashboard.enabled=true \
  --set 'dashboard.observation.sources[0].name=orders-traces' \
  --set 'dashboard.observation.sources[0].file=traces.json' \
  --set 'dashboard.observation.sources[0].existingClaim=orders-trace-export' \
  --set 'dashboard.observation.sources[1].name=broken-traces' \
  --set 'dashboard.observation.sources[1].file=traces.json' \
  --set 'dashboard.observation.sources[1].configMap=broken-trace-export' \
  --wait --timeout 240s
wait_ready pacto-dashboard

echo "== the REAL Deployment carries the read-only mounts and the configuration =="
kubectl -n "$NS" get deployment pacto-dashboard -o json | jqp '
import json,sys
spec=json.load(sys.stdin)["spec"]["template"]["spec"]
vols={v["name"]:v for v in spec["volumes"]}
c=spec["containers"][0]
mounts={m["name"]:m for m in c["volumeMounts"]}
env={e["name"]:e.get("value","") for e in c["env"]}
root="'"$MOUNT_ROOT"'"
errs=[]
pvc=vols.get("obs-orders-traces",{}).get("persistentVolumeClaim")
if not pvc or pvc.get("claimName")!="orders-trace-export":
    errs.append("obs-orders-traces is not backed by the declared PVC")
elif not pvc.get("readOnly"):
    errs.append("the PVC volume is not readOnly")
cm=vols.get("obs-broken-traces",{}).get("configMap")
if not cm or cm.get("name")!="broken-trace-export":
    errs.append("obs-broken-traces is not backed by the declared ConfigMap")
for n in ("obs-orders-traces","obs-broken-traces"):
    m=mounts.get(n)
    if not m: errs.append("missing mount "+n)
    elif not m.get("readOnly"): errs.append(n+" is mounted writable")
    elif m.get("mountPath")!=root+"/"+n[4:]: errs.append(n+" mounted at "+str(m.get("mountPath")))
want="broken-traces=%s/broken-traces/traces.json orders-traces=%s/orders-traces/traces.json"%(root,root)
if env.get("PACTO_DASHBOARD_TRACE_SOURCES")!=want:
    errs.append("PACTO_DASHBOARD_TRACE_SOURCES="+repr(env.get("PACTO_DASHBOARD_TRACE_SOURCES")))
print("\n".join(errs))
sys.exit(1 if errs else 0)
' && pass "both sources mounted read-only under their declared names, and configured" \
  || fail "the deployment wiring is wrong (see above)"

echo "== declared services: orders declares a dependency on checkout =="
kubectl create namespace "$DEMO_NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
for svc in checkout orders; do
  kubectl -n "$DEMO_NS" create deployment "$svc" --image=registry.k8s.io/pause:3.9 >/dev/null 2>&1 || true
done
kubectl -n "$DEMO_NS" rollout status deployment/checkout --timeout=90s
kubectl -n "$DEMO_NS" rollout status deployment/orders --timeout=90s
# The dependency ref is matched against sibling Pacto CRs' resolvedRef, never
# fetched, so no registry is needed for the declared edge to exist.
kubectl apply -f - >/dev/null <<YAML
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: { name: checkout, namespace: ${DEMO_NS} }
spec:
  checkIntervalSeconds: 30
  contractRef:
    inline: |
      pactoVersion: '2.0'
      service: {name: checkout, version: 1.0.0, owner: {team: commerce, dri: d, contacts: [{type: email, value: a@e.com, purpose: escalation}]}}
      workload: service
      state: {type: stateless, persistence: {scope: local, durability: ephemeral}, dataCriticality: low}
  target: {workloadRef: {name: checkout, kind: Deployment}}
---
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: { name: orders, namespace: ${DEMO_NS} }
spec:
  checkIntervalSeconds: 30
  contractRef:
    inline: |
      pactoVersion: '2.0'
      service: {name: orders, version: 1.0.0, owner: {team: commerce, dri: d, contacts: [{type: email, value: a@e.com, purpose: escalation}]}}
      workload: service
      state: {type: stateless, persistence: {scope: local, durability: ephemeral}, dataCriticality: low}
      dependencies: [{name: checkout, ref: 'oci://example.com/demo/checkout', required: false, compatibility: '^1.0.0'}]
  target: {workloadRef: {name: orders, kind: Deployment}}
YAML
wait_status() { for _ in $(seq 1 60); do [ "$(kubectl -n "$DEMO_NS" get pacto "$1" -o jsonpath='{.status.contractStatus}' 2>/dev/null||true)" = "$2" ] && return 0; sleep 3; done; return 1; }
wait_status checkout Compliant && pass "checkout reconciled" || fail "checkout did not reconcile"
wait_status orders Compliant && pass "orders reconciled" || fail "orders did not reconcile"

echo "== the Product API sees both Data Sources under their declared names =="
DASH_PF="$(pf "$LOCAL_DASH_PORT" svc/pacto-dashboard 3000)"; sleep 2
BASE="http://127.0.0.1:${LOCAL_DASH_PORT}"
# The dashboard rebuilds its snapshot periodically, so poll the assertion itself
# rather than racing the first build; the last attempt's errors are what we print.
OK=""
for _ in $(seq 1 30); do
  SNAP="$(curl -fsS "$BASE/api/fleet/snapshot" 2>/dev/null || true)"
  [ -n "$SNAP" ] && echo "$SNAP" | jqp '
import json,sys
s=json.load(sys.stdin)
src={x["id"]:x for x in s.get("sources",[])}
errs=[]
if src.get("orders-traces",{}).get("status")!="available":
    errs.append("orders-traces status="+repr(src.get("orders-traces",{}).get("status")))
if src.get("broken-traces",{}).get("status")!="unavailable":
    errs.append("broken-traces status="+repr(src.get("broken-traces",{}).get("status")))
if not any(l.get("code")=="SOURCE_UNAVAILABLE" and l.get("source")=="broken-traces" for l in s.get("limitations",[])):
    errs.append("no SOURCE_UNAVAILABLE limitation naming broken-traces")
obs=[r for r in s.get("relationships",[]) if r.get("provenance")=="observed"
     and r.get("fromService","").endswith("orders") and r.get("toService","").endswith("checkout")]
if not obs:
    errs.append("no observed orders->checkout edge reached the fleet")
elif not any(st.get("source")=="orders-traces" for st in obs[0].get("observedSources",[])):
    errs.append("the observed edge is not attributed to orders-traces: "+json.dumps(obs[0].get("observedSources")))
if not any(v.get("name")=="checkout" for v in s.get("services",{}).values()):
    errs.append("the reconciled services are not in the snapshot yet")
print("\n".join(errs))
sys.exit(1 if errs else 0)
' >/tmp/pacto-obs-assert.txt 2>&1 && { OK=1; break; }
  sleep 3
done
[ -n "$OK" ] && pass "the mounted export's observed edge is attributed to orders-traces; the malformed source is explicitly unavailable" \
  || { cat /tmp/pacto-obs-assert.txt; fail "the fleet did not report the configured sources correctly (see above)"; }

# The observed half above and the declared half the operator reconciled name the
# same pair, which is what makes the mounted export reconcilable evidence rather
# than a disconnected graph. The snapshot's own reconciliation verdict
# (relationship.reconciliation == "matched") additionally needs a contract
# REVISION source: the live Kubernetes source projects deployed targets, never
# revisions, so an operator-managed dashboard gets its declared edges from OCI —
# out of this scenario's scope, which is the observation packaging. That verdict
# over an observation source is proven hermetically by
# TestFleet_ObservedEdgeNamesItsConfiguredSource and by `make demo-fleet`.
[ "$(kubectl -n "$DEMO_NS" get pacto orders -o jsonpath='{.status.dependencies[0].name}')" = "checkout" ] \
  && pass "the operator reconciled the same orders->checkout pair as declared" \
  || fail "the declared dependency the observed edge should reconcile against is missing"

curl -fsS "$BASE/api/fleet/entities/source?key=orders-traces" >/dev/null \
  && pass "orders-traces is a first-class Product Data Source" || fail "orders-traces is not addressable as a Product entity"
curl -fsS "$BASE/api/fleet/entities/source?key=broken-traces" >/dev/null \
  && pass "broken-traces stays addressable while unavailable" || fail "a failed source disappeared from the Product API"
kill "$DASH_PF" 2>/dev/null || true

echo "== force a configured source to fail: drop one source, repoint the other =="
BEFORE="$(kubectl -n "$NS" get deployment pacto-dashboard -o jsonpath='{.metadata.generation}')"
helm upgrade pacto-operator "$CHART" -n "$NS" \
  --set image.repository="$OP_REPO" --set image.tag="$VER" --set image.pullPolicy=Never \
  --set dashboard.enabled=true \
  --set 'dashboard.observation.sources[0].name=orders-traces' \
  --set 'dashboard.observation.sources[0].file=absent.json' \
  --set 'dashboard.observation.sources[0].existingClaim=orders-trace-export' \
  --wait --timeout 240s
AFTER="$BEFORE"
for _ in $(seq 1 40); do
  AFTER="$(kubectl -n "$NS" get deployment pacto-dashboard -o jsonpath='{.metadata.generation}')"
  [ "$AFTER" != "$BEFORE" ] && break
  sleep 3
done
[ "$AFTER" != "$BEFORE" ] && pass "changing the sources rolled the dashboard" || fail "the configuration change never reached the pod template"
wait_ready pacto-dashboard

kubectl -n "$NS" get deployment pacto-dashboard -o json | jqp '
import json,sys
spec=json.load(sys.stdin)["spec"]["template"]["spec"]
names={v["name"] for v in spec["volumes"]} | {m["name"] for m in spec["containers"][0]["volumeMounts"]}
env={e["name"]:e.get("value","") for e in spec["containers"][0]["env"]}
errs=[]
if "obs-broken-traces" in names: errs.append("the removed source left orphaned volume/mount wiring behind")
if "broken-traces" in env.get("PACTO_DASHBOARD_TRACE_SOURCES",""): errs.append("the removed source is still configured")
print("\n".join(errs))
sys.exit(1 if errs else 0)
' && pass "removing a source removed its mount and its configuration" || fail "removal left wiring behind (see above)"

DASH_PF="$(pf "$LOCAL_DASH_PORT" svc/pacto-dashboard 3000)"; sleep 2
curl -fsS "$BASE/health" >/dev/null && pass "the dashboard is alive with a failing source" || fail "a failing source took the dashboard down"
OK=""
for _ in $(seq 1 30); do
  SNAP="$(curl -fsS "$BASE/api/fleet/snapshot" 2>/dev/null || true)"
  if echo "$SNAP" | jqp '
import json,sys
s=json.load(sys.stdin)
src={x["id"]:x for x in s.get("sources",[])}
if src.get("orders-traces",{}).get("status")!="unavailable": sys.exit(1)
# The other sources must be unaffected: the live Kubernetes source still answers
# and the services it reconciled are still there.
if not any(x["kind"]=="kubernetes" and x["status"]=="available" for x in s.get("sources",[])): sys.exit(1)
if not any(v.get("name")=="checkout" for v in s.get("services",{}).values()): sys.exit(1)
' 2>/dev/null; then OK=1; break; fi
  sleep 3
done
[ -n "$OK" ] && pass "the failed source is explicit unavailable knowledge; healthy sources still answer" \
  || fail "a missing trace file did not surface as an unavailable Data Source with the rest of the fleet intact"
kill "$DASH_PF" 2>/dev/null || true

echo
echo "== operator-managed observation packaging verified =="
if [ -n "${KEEP_E2E_CLUSTER:-}" ]; then
  cat <<EOF

  Running in kind cluster '$CLUSTER' (namespace $NS). To use it:

    export KUBECONFIG=\$(mktemp) && kind get kubeconfig --name $CLUSTER > \$KUBECONFIG
    kubectl -n $NS port-forward svc/pacto-dashboard 8080:3000
    curl -s localhost:8080/api/fleet/snapshot | python3 -m json.tool | less

  Inspect / tear down:
    make e2e-observation-kind-status
    make e2e-observation-kind-logs
    make e2e-observation-kind-down
EOF
fi
keep_or_teardown "$NS" "$CLUSTER" "obs_teardown"
