#!/usr/bin/env bash
# Prove the OPERATOR-MANAGED observation packaging against a real cluster: a
# declarative Helm value naming an offline OTLP/JSON trace export becomes a
# read-only mount, reaches the dashboard's configuration under the name the
# values declared, and shows up as a Data Source whose observed edge reconciles
# against a declared one.
#
# Deliberately narrow: this is the packaging vertical, asserted at the API level,
# with no browser leg. The broad live Product journey lives in
# tests/acceptance/kind/operational-graph.sh.
#
# What it proves, end to end:
#   1. an externally managed PVC holding a trace export is mounted read-only,
#   2. a ConfigMap-backed source is mounted read-only alongside it,
#   3. the dashboard is configured with both, under their declared names,
#   4. the healthy source's observed edge reaches the fleet attributed to that
#      name, naming the same pair the operator reconciled as declared,
#   5. a malformed source is explicit unavailable knowledge, not a silent gap,
#   6. a source whose export storage contains a symlink pointing OUT of its mount
#      reads nothing — in a real kubelet-managed container, where the thing a
#      symlink can reach is the service-account token,
#   7. a ConfigMap-backed source with valid content is available, so the internal
#      symlink chain a projected volume is built from still resolves,
#   8. changing sources rolls the dashboard and leaves no orphaned wiring, and a
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
# shellcheck source=tests/acceptance/kind/lib.sh
source "$(dirname "$0")/lib.sh"

CMD="${1:-run}"
case "$CMD" in
  status)
    use_existing_cluster "make e2e-observation-kind-up"
    echo "== pods =="; kubectl -n "$NS" get pods
    echo "== pvc =="; kubectl -n "$NS" get pvc
    echo "== dashboard observation volumes =="
    kubectl -n "$NS" get deployment pacto-dashboard -o jsonpath='{range .spec.template.spec.volumes[*]}{.name}{"\n"}{end}' 2>/dev/null || true
    echo "== dashboard observation mounts =="
    kubectl -n "$NS" get deployment pacto-dashboard -o jsonpath='{range .spec.template.spec.containers[0].volumeMounts[*]}{.name}{" "}{.mountPath}{" readOnly="}{.readOnly}{"\n"}{end}' 2>/dev/null || true
    exit 0 ;;
  logs) use_existing_cluster "make e2e-observation-kind-up"; dump_diag "$NS"; exit 0 ;;
  down) down_cluster; exit 0 ;;
  run|up) : ;;
  *) echo "unknown subcommand: $CMD (use up|status|logs|down)"; exit 2 ;;
esac

# shellcheck disable=SC2154  # rc is assigned by rc=$? inside the trap body
trap 'rc=$?; [ $rc -ne 0 ] && dump_diag "$NS"; pkill -f "kubectl.*port-forward" 2>/dev/null || true; exit $rc' EXIT

# Everything semantic this scenario asserts over JSON — the Deployment wiring and
# the Product facts — is a typed decode in tests/acceptance/kind/obscheck, which
# has its own mutation-check suite. The shell only brings the cluster up and says
# which facts must hold.
obscheck() { ( cd "$ROOT" && go run ./tests/acceptance/kind/obscheck "$@" ); }

VER="$(release_version kubernetes)"
CORE="$(release_version core)"
OP_REPO="localhost:5001/pacto-operator/pacto-controller"
OP_IMG="${OP_REPO}:${VER}"
DASH_IMG="localhost:5001/pacto-dashboard:${CORE}"

build_operator_images "$OP_IMG" "$DASH_IMG" "$VER"

echo "== package the operator chart =="
CHART="$(package_chart "$PACTO_CHART")"

ensure_cluster
load_images "$DASH_IMG" "$OP_IMG"
kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

echo "== an externally managed PVC holds the trace export (Pacto never writes it) =="
# The writer Job stands in for whatever really produces the exports: it is both
# PVCs' first consumer (kind's local-path provisioner binds on first use), writes
# what each holds and exits. Pacto only ever reads them, read-only. Reusing the
# already-loaded dashboard image keeps this hermetic — no extra registry pull.
kubectl -n "$NS" apply -f - >/dev/null <<YAML
apiVersion: v1
kind: PersistentVolumeClaim
metadata: { name: orders-trace-export }
spec:
  accessModes: [ ReadWriteOnce ]
  resources: { requests: { storage: 64Mi } }
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata: { name: escaping-trace-export }
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
          # Whoever writes an export can also write a symlink into it. This one
          # leaves the mount it will be read through, which is the same
          # resolution step that reaches
          # /var/run/secrets/kubernetes.io/serviceaccount/token — but it points
          # at a file that IS a valid export, so an unrooted read would succeed
          # and be visible in the snapshot instead of failing for its own
          # reasons. Pacto must read nothing.
          #
          # It gets its own claim: two volumes in one pod referencing the same
          # ReadWriteOnce PVC is a kubelet coin flip, not a property of Pacto.
          ln -sf ../orders-traces/traces.json /escaping/escape.json
        volumeMounts:
        - { name: export, mountPath: /data }
        - { name: escaping, mountPath: /escaping }
      volumes:
      - name: export
        persistentVolumeClaim: { claimName: orders-trace-export }
      - name: escaping
        persistentVolumeClaim: { claimName: escaping-trace-export }
YAML
kubectl -n "$NS" wait --for=condition=complete job/trace-writer --timeout=180s >/dev/null \
  && pass "trace export written to the externally managed PVC" || fail "the trace writer did not complete"

echo "== two ConfigMap-backed sources: one MALFORMED, one valid =="
kubectl -n "$NS" create configmap broken-trace-export --from-literal=traces.json='{not json' \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null
# A projected ConfigMap volume is a directory of symlinks into a versioned
# "..data" directory, itself a symlink. Reading through a root must not break
# that, so this source carries VALID content and has to come back available.
kubectl -n "$NS" create configmap fixture-trace-export --from-literal=traces.json='{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"checkout"}}]},"scopeSpans":[{"spans":[{"kind":3,"attributes":[{"key":"peer.service","value":{"stringValue":"orders"}}]}]}]}]}' \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null

echo "== install the operator with four declared observation sources =="
helm install pacto-operator "$CHART" -n "$NS" \
  --set image.repository="$OP_REPO" --set image.tag="$VER" --set image.pullPolicy=Never \
  --set dashboard.enabled=true \
  --set 'dashboard.observation.sources[0].name=orders-traces' \
  --set 'dashboard.observation.sources[0].file=traces.json' \
  --set 'dashboard.observation.sources[0].existingClaim=orders-trace-export' \
  --set 'dashboard.observation.sources[1].name=broken-traces' \
  --set 'dashboard.observation.sources[1].file=traces.json' \
  --set 'dashboard.observation.sources[1].configMap=broken-trace-export' \
  --set 'dashboard.observation.sources[2].name=fixture-traces' \
  --set 'dashboard.observation.sources[2].file=traces.json' \
  --set 'dashboard.observation.sources[2].configMap=fixture-trace-export' \
  --set 'dashboard.observation.sources[3].name=escaping-traces' \
  --set 'dashboard.observation.sources[3].file=escape.json' \
  --set 'dashboard.observation.sources[3].existingClaim=escaping-trace-export' \
  --wait --timeout 240s
wait_ready pacto-dashboard

echo "== the REAL Deployment carries the read-only mounts and the configuration =="
kubectl -n "$NS" get deployment pacto-dashboard -o json \
  | obscheck wiring -mount-root "$MOUNT_ROOT" \
      -source 'orders-traces:pvc:orders-trace-export:traces.json' \
      -source 'broken-traces:configMap:broken-trace-export:traces.json' \
      -source 'fixture-traces:configMap:fixture-trace-export:traces.json' \
      -source 'escaping-traces:pvc:escaping-trace-export:escape.json' \
  && pass "every source mounted read-only under its declared name, and configured" \
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
wait_pacto_status "$DEMO_NS" checkout Compliant && pass "checkout reconciled" || fail "checkout did not reconcile"
wait_pacto_status "$DEMO_NS" orders Compliant && pass "orders reconciled" || fail "orders did not reconcile"

echo "== the Product API sees both Data Sources under their declared names =="
DASH_PF="$(pf "$LOCAL_DASH_PORT" svc/pacto-dashboard 3000)"
BASE="http://127.0.0.1:${LOCAL_DASH_PORT}"
# -silent escaping-traces is the whole point of that source: it read nothing, so
# it witnessed nothing. Had the symlink out of its mount been followed it would
# have read the very export the observed edge came from and would be counted
# alongside orders-traces. A projected ConfigMap volume, by contrast, resolves
# its OWN internal symlinks, so a rooted read must not break fixture-traces.
obscheck snapshot -base "$BASE" \
  -available orders-traces -available fixture-traces \
  -unavailable broken-traces -unavailable escaping-traces \
  -observed orders:checkout -attributed orders-traces -silent escaping-traces \
  -service checkout \
  && pass "the mounted export's observed edge is attributed to orders-traces; the projected ConfigMap source is available; the malformed and the escaping sources are explicitly unavailable and contributed nothing" \
  || fail "the fleet did not report the configured sources correctly (see above)"

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
curl -fsS "$BASE/api/fleet/entities/source?key=escaping-traces" >/dev/null \
  && pass "escaping-traces stays addressable while unavailable" || fail "the refused source disappeared from the Product API"
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
rolled() { [ "$(kubectl -n "$NS" get deployment pacto-dashboard -o jsonpath='{.metadata.generation}')" != "$BEFORE" ]; }
eventually 40 rolled && pass "changing the sources rolled the dashboard" || fail "the configuration change never reached the pod template"
wait_ready pacto-dashboard

kubectl -n "$NS" get deployment pacto-dashboard -o json \
  | obscheck wiring -mount-root "$MOUNT_ROOT" \
      -source 'orders-traces:pvc:orders-trace-export:absent.json' \
      -absent broken-traces -absent fixture-traces -absent escaping-traces \
  && pass "removing a source removed its mount and its configuration" \
  || fail "removal left wiring behind (see above)"

DASH_PF="$(pf "$LOCAL_DASH_PORT" svc/pacto-dashboard 3000)"
curl -fsS "$BASE/health" >/dev/null && pass "the dashboard is alive with a failing source" || fail "a failing source took the dashboard down"
# The other sources must be unaffected: the live Kubernetes source still answers
# and the services it reconciled are still there.
obscheck snapshot -base "$BASE" \
  -unavailable orders-traces -kind-available kubernetes -service checkout \
  && pass "the failed source is explicit unavailable knowledge; healthy sources still answer" \
  || fail "a missing trace file did not surface as an unavailable Data Source with the rest of the fleet intact"
kill "$DASH_PF" 2>/dev/null || true

echo
echo "== operator-managed observation packaging verified =="
if [ -n "${KEEP_E2E_CLUSTER:-}" ]; then
  cat <<EOF

  Running in kind cluster '$CLUSTER' (namespace $NS). To use it:

    export KUBECONFIG=\$(mktemp) && kind get kubeconfig --name $CLUSTER > \$KUBECONFIG
    kubectl -n $NS port-forward svc/pacto-dashboard 8080:3000
    curl -s localhost:8080/api/fleet/snapshot | less

  Inspect / tear down:
    make e2e-observation-kind-status
    make e2e-observation-kind-logs
    make e2e-observation-kind-down
EOF
fi
keep_or_teardown "$NS" "$CLUSTER" delete_cluster
