#!/usr/bin/env bash
# shellcheck disable=SC2329 # poll helpers are invoked by name, which shellcheck cannot see
# Kind acceptance for runtime conformance with no Pacto CR: a team publishes
# its contract with `pacto publish --image`, Argo CD deploys the workload, and
# the operator links the running workload to the published contract and
# evaluates it with the same finding codes a Pacto CR gets.
#
# Nothing Pacto-specific is deployed next to the workloads. The operator runs
# with only the top-level `registry` value (delivery absent), the manifests Argo
# applies carry no Pacto, and `kubectl get pactos -A` stays empty until the last
# step writes one by hand. The two link signals are both exercised:
#
#   payments  links by image digest: its pod runs an image whose digest the
#             signed images record of the published payments 2.3.0 names.
#   ledger    links by the pacto.trianalab.io/contract annotation its own
#             Deployment carries.
#
# Then, in order: payments is NonCompliant while ledger has no ready pod
# (DEPENDENCY_UNREACHABLE, and its interface reads OBSERVATION_UNSUPPORTED with
# no binding, never INTERFACE_ABSENT); a new Argo revision names the Service
# port after the interface and scales ledger up, and payments turns Compliant;
# the CLI fleet shows the link and hands the inferred workload over to
# payments; the signed target snapshot carries the linked target; a
# hand-written Pacto CR for ledger wins over its link; and a digest two
# services record links nothing.
#
# The images are single-platform builds pushed to the in-cluster registry,
# which the nodes pull from through containerd's hosts.toml, so the digest a
# pod reports is the digest pacto publish recorded.
set -euo pipefail
CLUSTER="${KIND_CLUSTER:-pacto-linked}"
NS=pacto-system
APP_NS=demo
ARGO_NS=argocd
REG_HOST="pacto-registry.${NS}.svc.cluster.local:5000"
LOCAL_REG_PORT=5611
MANIFEST_REPO="acme/shop-manifests"
PAUSE_IMG="registry.k8s.io/pause:3.9@sha256:7031c1b283388d2c2e09b57badb803c05ebed362dc88d84b480cc47f72a21097"
IMAGES_TYPE="application/vnd.pacto.images.v1+json"
LINKED_BY="pacto.trianalab.io/linked-by"
LINKED_VALUE="pacto.trianalab.io/linked-value"
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=tests/acceptance/kind/lib.sh
source "$HERE/lib.sh"
# shellcheck source=tests/acceptance/kind/argocd-lib.sh
source "$(dirname "$0")/argocd-lib.sh"

WORK="$(mktemp -d)"
LOCAL_REG="127.0.0.1:${LOCAL_REG_PORT}"

# linked — the linked.json the operator's linker wrote, "{}" while it has none.
linked() {
  local out
  out="$(kubectl -n "$NS" get cm pacto-operator-detected -o jsonpath='{.data.linked\.json}' 2>/dev/null || true)"
  [ -n "$out" ] || out='{}'
  echo "$out"
}
# view NAME — the linked target named NAME (<Kind>/<name>) in the demo namespace.
view() { linked | jq -c --arg n "$1" --arg ns "$APP_NS" '.targets // [] | map(select(.metadata.name == $n and .metadata.namespace == $ns)) | .[0] // empty'; }

dump_linked() {
  echo "--- linked.json ---"
  linked | jq . 2>/dev/null || linked
  echo "--- argocd applications ---"
  kubectl -n "$ARGO_NS" get applications -o wide 2>/dev/null || true
  kubectl -n "$ARGO_NS" describe application shop 2>/dev/null || true
  echo "--- $APP_NS objects ---"
  kubectl -n "$APP_NS" get all,pactos -o wide 2>/dev/null || true
  kubectl -n "$APP_NS" get pods -o jsonpath='{range .items[*]}{.metadata.name}{" "}{.status.containerStatuses[*].imageID}{"\n"}{end}' 2>/dev/null || true
}
# shellcheck disable=SC2154  # rc is assigned by rc=$? inside the trap body
trap 'rc=$?; [ $rc -ne 0 ] && { dump_diag "$NS"; dump_linked; }; pkill -f "kubectl.*port-forward.*${LOCAL_REG_PORT}" 2>/dev/null || true; exit $rc' EXIT

command -v oras > /dev/null 2>&1 \
  || fail "oras is required: it publishes the manifest artifact Argo CD pulls and reads the target snapshot"

VER="$(release_version kubernetes)"
CORE="$(release_version core)"
OP_IMG="localhost:5001/pacto-operator/pacto-controller:${VER}"
OP_REPO="localhost:5001/pacto-operator/pacto-controller"
DASH_IMG="localhost:5001/pacto-dashboard:${CORE}"

section "1 the cluster, the in-cluster registry and the operator with only the registry value"
# containerd reads per-registry config, so the nodes can pull the images this
# scenario pushes to the in-cluster registry.
KIND_CFG="$WORK/kind.yaml"
cat > "$KIND_CFG" << 'YAML'
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
containerdConfigPatches:
- |-
  [plugins."io.containerd.grpc.v1.cri".registry]
    config_path = "/etc/containerd/certs.d"
YAML
cluster_start_bg "$KIND_CFG"
build_operator_images "$OP_IMG" "$DASH_IMG" "$VER"
pull_pinned "$PAUSE_IMG"
PACTO_BIN="$(build_pacto)"
CHART="$(package_chart "$PACTO_CHART")"
ensure_cluster "$KIND_CFG"
load_images "$OP_IMG"
for n in "$NS" "$APP_NS"; do
  kubectl create namespace "$n" --dry-run=client -o yaml | kubectl apply -f - > /dev/null
done
install_registry
node_pulls_from_registry
REG_PF_PID="$(pf "$LOCAL_REG_PORT" svc/pacto-registry 5000)"
# A 5s stabilization window confirms a negative on the linker's next cycle;
# name-match discovery is the only way an interface with no binding is
# observed, and it can only add positives.
helm install pacto-operator "$CHART" -n "$NS" \
  --set global.imageRegistry= --set image.repository="$OP_REPO" --set image.tag="$VER" --set image.pullPolicy=Never \
  --set dashboard.enabled=false --set registry="$REG_HOST/acme" --set "insecureRegistries[0]=$REG_HOST" \
  --set controller.stabilizationWindow=5s --set controller.interfaceNameMatchDiscovery=true \
  --wait --timeout 240s > /dev/null
! kubectl get crd stages.delivery.pacto.trianalab.io > /dev/null 2>&1 || fail "the delivery Stage CRD is installed with delivery off"
[ -z "$(kubectl get deploy -A -o name | grep -E 'promoter|recorder' || true)" ] \
  || fail "a promoter or recorder runs with delivery off: $(kubectl get deploy -A -o name)"
CLUSTER_UID="$(kubectl get ns kube-system -o jsonpath='{.metadata.uid}')"
pass "the operator runs with only the registry value: no Stage CRD, no promoter, no recorder (cluster $CLUSTER_UID)"

section "2 build and push two images, then publish payments 2.3.0 and ledger 1.0.0 with --image"
# push_image SVC — a single-platform image distinct per service, pushed to
# acme/images/SVC through the forwarded port; prints its digest.
push_image() {
  local dir out digest
  dir="$(mktemp -d)"
  printf 'FROM %s\nLABEL io.trianalab.pacto.e2e.service=%s\n' "${PAUSE_IMG%@*}" "$1" > "$dir/Dockerfile"
  docker build -q --provenance=false -t "localhost:${LOCAL_REG_PORT}/acme/images/$1:e2e" "$dir" > /dev/null \
    || fail "could not build the $1 image"
  out="$(docker push "localhost:${LOCAL_REG_PORT}/acme/images/$1:e2e" 2>&1)" || { echo "$out" >&2; fail "could not push the $1 image"; }
  digest="$(grep -oE 'digest: sha256:[0-9a-f]{64}' <<< "$out" | head -1 | cut -d' ' -f2)"
  [ -n "$digest" ] || { echo "$out" >&2; fail "pushing the $1 image printed no digest"; }
  echo "$digest"
}
PAY_DIGEST="$(push_image payments)"
LEDGER_DIGEST="$(push_image ledger)"
PAY_IMAGE="$REG_HOST/acme/images/payments@$PAY_DIGEST"
LEDGER_IMAGE="$REG_HOST/acme/images/ledger@$LEDGER_DIGEST"

# The contracts live in one git repository: publish stamps the commit.
CONTRACTS="$WORK/contracts"
mkdir -p "$CONTRACTS"
git -C "$CONTRACTS" init -q
# contract NAME VERSION [EXTRA_YAML] — a 2.5 contract with no publish section.
contract() {
  mkdir -p "$CONTRACTS/$1"
  cat > "$CONTRACTS/$1/pacto.yaml" << YAML
pactoVersion: "2.5"
service:
  name: $1
  version: $2
  owner: {team: team-$1, dri: d, contacts: [{type: email, value: a@e.com, purpose: escalation}]}
workload: service
state: {type: stateless, persistence: {scope: local, durability: ephemeral}, dataCriticality: low}
${3:-}
YAML
  git -C "$CONTRACTS" add -A
  git -C "$CONTRACTS" -c user.email=e2e@example.com -c user.name=e2e commit -qm "$1 $2"
}
contract ledger 1.0.0
contract payments 2.3.0 "interfaces: [{name: api, type: openapi, ref: openapi.yaml, visibility: internal}]
dependencies: [{name: ledger, ref: \"oci://$REG_HOST/acme/ledger\", required: true, compatibility: \"^1.0.0\"}]"
printf 'openapi: "3.0.0"\ninfo: { title: payments, version: "2.3.0" }\npaths: {}\n' > "$CONTRACTS/payments/openapi.yaml"
git -C "$CONTRACTS" add -A
git -C "$CONTRACTS" -c user.email=e2e@example.com -c user.name=e2e commit -qm "payments api"
openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "$WORK/sign.pem" 2> /dev/null \
  || fail "openssl could not make the publisher's key"

# reg CMD... — CMD with the forwarded port as a plain-HTTP proxy, so it reaches
# the registry AS $REG_HOST, the host the cluster reads, and outside any CI
# context (a pull-request pipeline refuses to publish).
reg() {
  env -u GITHUB_ACTIONS -u GITHUB_BASE_REF -u GITHUB_STEP_SUMMARY -u GITHUB_REPOSITORY_OWNER -u ACTIONS_ID_TOKEN_REQUEST_URL \
    -u GITLAB_CI -u CI_MERGE_REQUEST_DIFF_BASE_SHA -u SIGSTORE_ID_TOKEN \
    HTTP_PROXY="http://${LOCAL_REG}" NO_PROXY="" no_proxy="" PACTO_INSECURE_REGISTRIES="$REG_HOST" "$@"
}
# publish NAME IMAGE — pacto publish --image IMAGE for the contract in NAME/,
# signed with the run's key (the images record needs its signature referrer).
publish() {
  local out
  out="$(cd "$CONTRACTS/$1" && reg "$PACTO_BIN" publish --image "$2" --registry "$REG_HOST/acme" \
    --sign-key "$WORK/sign.pem" --no-transparency-log 2>&1)" || { echo "$out" >&2; fail "pacto publish $1 failed"; }
}
# images_referrer NAME VERSION — the published bundle has an images record.
images_referrer() {
  local digest
  digest="$(oras resolve --plain-http "$LOCAL_REG/acme/$1:$2" 2> /dev/null)" || return 1
  oras discover --plain-http --format json "$LOCAL_REG/acme/$1@$digest" 2> /dev/null \
    | jq -e --arg t "$IMAGES_TYPE" '[.. | objects | .artifactType? // empty] | index($t) != null' > /dev/null
}
publish ledger "$LEDGER_IMAGE"
publish payments "$PAY_IMAGE"
TAGS="$(oras repo tags --plain-http "$LOCAL_REG/acme/pacto-index" 2> /dev/null || true)"
{ grep -qE -- '-2\.3\.0$' <<< "$TAGS" && grep -qE -- '-1\.0\.0$' <<< "$TAGS"; } \
  || fail "the organization index $REG_HOST/acme/pacto-index has no entries for payments 2.3.0 and ledger 1.0.0: $TAGS"
for s in "payments 2.3.0" "ledger 1.0.0"; do
  # shellcheck disable=SC2086  # NAME VERSION
  images_referrer $s || fail "the published $s bundle has no images record referrer"
done
pass "published payments 2.3.0 ($PAY_DIGEST) and ledger 1.0.0, indexed, each with a signed images record"

section "3 Argo CD deploys both workloads from an OCI manifest artifact, with no Pacto"
# manifests TAG PORT_NAME LEDGER_REPLICAS — the Application's manifests as the
# OCI artifact Argo pulls. payments runs by digest; ledger names its contract
# in its own annotation.
manifests() {
  local tree tgz port_name=""
  tree="$(mktemp -d)"
  tgz="$(mktemp -t shop-manifests.XXXXXX)"
  [ -z "$2" ] || port_name="name: $2, "
  cat > "$tree/shop.yaml" << YAML
apiVersion: apps/v1
kind: Deployment
metadata: { name: payments, namespace: $APP_NS }
spec:
  replicas: 1
  selector: { matchLabels: { app: payments } }
  template:
    metadata: { labels: { app: payments } }
    spec:
      containers: [{ name: app, image: "$PAY_IMAGE" }]
---
apiVersion: v1
kind: Service
metadata: { name: payments, namespace: $APP_NS }
spec: { selector: { app: payments }, ports: [{ ${port_name}port: 8080, targetPort: 8080 }] }
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ledger
  namespace: $APP_NS
  annotations: { pacto.trianalab.io/contract: "oci://$REG_HOST/acme/ledger:1.0.0" }
spec:
  replicas: $3
  selector: { matchLabels: { app: ledger } }
  template:
    metadata: { labels: { app: ledger } }
    spec:
      containers: [{ name: app, image: "$LEDGER_IMAGE" }]
---
apiVersion: v1
kind: Service
metadata: { name: ledger, namespace: $APP_NS }
spec: { selector: { app: ledger }, ports: [{ port: 8080, targetPort: 8080 }] }
YAML
  tar -czf "$tgz" -C "$tree" .
  # --disable-path-validation: $tgz is absolute, which ORAS refuses unless told.
  oras push --plain-http --disable-path-validation "$LOCAL_REG/$MANIFEST_REPO:$1" "${tgz}:${ARGO_LAYER_TYPE}" > /dev/null \
    || fail "could not publish the manifests $1"
}
manifests v1 "" 0
install_argocd_core
argo_app_oci shop "$MANIFEST_REPO" v1
app_sync() { kubectl -n "$ARGO_NS" get application shop -o jsonpath='{.status.sync.status}' 2> /dev/null || true; }
synced() { [ "$(app_sync)" = "Synced" ]; }
eventually 60 synced || fail "Argo CD never synced the OCI source: sync=$(app_sync)"
kubectl -n "$APP_NS" rollout status deployment/payments --timeout=180s > /dev/null \
  || fail "payments never rolled out: the node could not pull $PAY_IMAGE from the in-cluster registry"
POD_IMAGE_ID="$(kubectl -n "$APP_NS" get pod -l app=payments -o jsonpath='{.items[0].status.containerStatuses[0].imageID}')"
[[ "$POD_IMAGE_ID" == *"@$PAY_DIGEST" ]] \
  || fail "the payments pod reports imageID '$POD_IMAGE_ID', not the pushed digest $PAY_DIGEST: a pull problem, so no digest link can be expected"
pass "Argo CD synced v1; the payments pod runs $PAY_DIGEST"

section "4 the operator links both workloads and evaluates them, with no Pacto object"
[ -z "$(kubectl get pactos -A -o name)" ] || fail "a Pacto object exists: $(kubectl get pactos -A -o name)"
linked_by() { [ "$(view "$1" | jq -r --arg l "$LINKED_BY" '.metadata.labels[$l] // empty')" = "$2" ]; }
eventually 60 linked_by Deployment/payments image-digest \
  || fail "linked.json never held Deployment/payments linked by image-digest: $(view Deployment/payments)"
[ "$(view Deployment/payments | jq -r --arg l "$LINKED_VALUE" '.metadata.labels[$l]')" = "$PAY_DIGEST" ] \
  || fail "Deployment/payments is linked by a value other than the running digest $PAY_DIGEST: $(view Deployment/payments)"
eventually 20 linked_by Deployment/ledger annotation \
  || fail "linked.json never held Deployment/ledger linked by its annotation: $(view Deployment/ledger)"
status_of() { view "$1" | jq -r '.status.contractStatus // empty'; }
codes_of() { view "$1" | jq -r '.status.findings // [] | map(.code + ":" + .subject) | join(" ")'; }
pay_noncompliant() { [ "$(status_of Deployment/payments)" = NonCompliant ] && grep -q 'DEPENDENCY_UNREACHABLE' <<< "$(codes_of Deployment/payments)"; }
eventually 100 pay_noncompliant \
  || fail "payments never became NonCompliant with DEPENDENCY_UNREACHABLE while ledger has no pod: $(status_of Deployment/payments) $(codes_of Deployment/payments)"
grep -q 'OBSERVATION_UNSUPPORTED:interface/api' <<< "$(codes_of Deployment/payments)" \
  || fail "the api interface of payments does not read OBSERVATION_UNSUPPORTED with no binding: $(codes_of Deployment/payments)"
! grep -q 'INTERFACE_ABSENT' <<< "$(codes_of Deployment/payments)" \
  || fail "the linker produced INTERFACE_ABSENT, which only a declared binding may: $(codes_of Deployment/payments)"
[ -z "$(kubectl get pactos -A -o name)" ] || fail "linking created a Pacto object: $(kubectl get pactos -A -o name)"
pass "payments NonCompliant (DEPENDENCY_UNREACHABLE), its api interface OBSERVATION_UNSUPPORTED, and still no Pacto object"

section "5 a new Argo revision names the port after the interface and scales ledger up"
manifests v2 api 1
argo_app_oci shop "$MANIFEST_REPO" v2
pay_compliant() { [ "$(status_of Deployment/payments)" = Compliant ]; }
# Three linker cycles of a minute each.
eventually 80 pay_compliant \
  || fail "payments never became Compliant after the api port and a ready ledger: $(status_of Deployment/payments) $(codes_of Deployment/payments)"
pass "payments Compliant: name-match discovery observes api and ledger is reachable"

section "6 the CLI fleet shows the link and hands the inferred workload over"
# fleet NAME — pacto fleet get NAME as JSON over this cluster and the
# operator's detected ConfigMap; "{}" when it prints nothing. --local "" keeps
# the repository's own contracts out: its payments would win the bare name.
fleet() {
  local out
  out="$(reg "$PACTO_BIN" fleet get "$1" --local "" --k8s --detected "k8s:$NS/pacto-operator-detected" --output-format json 2> /dev/null || true)"
  [ -n "$out" ] || out='{}'
  echo "$out"
}
OUT="$(fleet payments)"
jq -e '[.targets[] | select(.link.signal == "image-digest" and .compliance == "Compliant")] | length == 1' <<< "$OUT" > /dev/null \
  || fail "pacto fleet get payments shows no single Compliant target linked by image-digest: $(jq -c '.targets' <<< "$OUT")"
# Argo CD created the workload minutes before the link: a fresh install, so
# the workload is handed over with no recorded replacement (minReplacementGap)
# and its k8s: name names no service of its own.
OUT="$(fleet "k8s:$APP_NS/Deployment/payments")"
jq -e '.service == null or .service.name == "payments"' <<< "$OUT" > /dev/null \
  || fail "the linked k8s:$APP_NS/Deployment/payments stays an inferred service: $(jq -c '.service' <<< "$OUT")"
pass "one Compliant payments target linked by image-digest; k8s:$APP_NS/Deployment/payments is no inferred service of its own"

section "7 the signed target snapshot carries the linked target"
TARGETS="$LOCAL_REG/acme/pacto-targets:cluster-$CLUSTER_UID"
snapshot_linked() {
  local digest
  digest="$(oras manifest fetch --plain-http "$TARGETS" 2> /dev/null | jq -r '.layers[0].digest')" || return 1
  oras blob fetch --plain-http --output - "$LOCAL_REG/acme/pacto-targets@$digest" 2> /dev/null \
    | jq -e --arg l "$LINKED_BY" '.dsseEnvelope.payload | @base64d | fromjson
        | [.predicate.pactos[] | select(.metadata.name == "Deployment/payments" and .metadata.labels[$l] == "image-digest")] | length == 1' > /dev/null
}
# The publisher pushes every five minutes.
eventually 140 snapshot_linked || fail "$TARGETS never held Deployment/payments with the $LINKED_BY label"
pass "$TARGETS holds Deployment/payments linked by image-digest"

section "8 a hand-written Pacto CR for ledger wins over its link"
kubectl apply -f - > /dev/null << YAML
apiVersion: pacto.trianalab.io/v1
kind: Pacto
metadata: { name: ledger, namespace: $APP_NS }
spec:
  checkIntervalSeconds: 30
  contractRef: { oci: "oci://$REG_HOST/acme/ledger:1.0.0" }
  target: { serviceName: ledger, workloadRef: { name: ledger, kind: Deployment } }
YAML
ledger_unlinked() { [ -z "$(view Deployment/ledger)" ]; }
eventually 50 ledger_unlinked || fail "linked.json still holds Deployment/ledger two cycles after its Pacto CR: $(view Deployment/ledger)"
OUT="$(fleet ledger)"
jq -e '(.targets | length) >= 1 and ([.targets[] | select(.link != null)] | length == 0)' <<< "$OUT" > /dev/null \
  || fail "the fleet target of ledger is not the CR's alone: $(jq -c '.targets' <<< "$OUT")"
pass "the Pacto CR wins: Deployment/ledger is no longer linked, and the fleet shows ledger with no observed link"

section "9 a digest two services record links nothing"
contract billing 1.0.0
publish billing "$PAY_IMAGE"
kubectl apply -f - > /dev/null << YAML
apiVersion: apps/v1
kind: Deployment
metadata: { name: shared, namespace: $APP_NS }
spec:
  replicas: 1
  selector: { matchLabels: { app: shared } }
  template:
    metadata: { labels: { app: shared } }
    spec:
      containers: [{ name: app, image: "$PAY_IMAGE" }]
YAML
ambiguous() {
  linked | jq -e '.limitations // [] | map(select(.code == "INFERRED_REPLACEMENT_AMBIGUOUS" and (.message | test("Deployment/shared"))
    and (.message | test("billing")) and (.message | test("payments")))) | length == 1' > /dev/null
}
eventually 60 ambiguous \
  || fail "linked.json never named Deployment/shared INFERRED_REPLACEMENT_AMBIGUOUS between billing and payments: $(linked | jq -c '.limitations')"
[ -z "$(view Deployment/shared)" ] || fail "Deployment/shared was linked although two services record its digest: $(view Deployment/shared)"
pass "Deployment/shared links to none: INFERRED_REPLACEMENT_AMBIGUOUS names billing and payments"

kill "$REG_PF_PID" 2> /dev/null || true
echo "LINKED WORKLOADS PASS"
keep_or_teardown "$NS" "$CLUSTER" delete_cluster
