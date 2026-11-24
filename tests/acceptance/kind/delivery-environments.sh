#!/usr/bin/env bash
# Kind acceptance for the installation ladder (#548): a developer publishes
# with pacto ci --publish and writes no environments[], no Stage and no Flux
# object, and the promoter of a profile idp install, with the Flux the chart
# ships (idp.flux.install), creates and retires the Stages itself.
#
# Every publisher signature is keyless against a private Sigstore
# (tests/acceptance/kind/keylesssign) whose trusted root the chart is given;
# the Sigstore lives only while it signs, so every version the run ships is
# published and signed before Pacto is installed, and the temporary
# environment's expiry is set from that moment. The person who confirms the
# installation ladder signs as `pacto delivery ladder confirm` does, through
# tests/acceptance/kind/laddersign and its own private Sigstore, whose issuer
# and root the chart then lists among the approver signers; the command
# itself needs a browser login.
#
# It proves against the cluster:
#   1. payments 1.5.0, published with no environments[], gets the managed
#      Stages payments-staging (from the registry, autonomous with every
#      promote held for a person) and payments-production (from
#      payments-staging) in the namespaces named after the rungs, each with
#      suspended Flux objects whose pointer has no spec.ref, and no
#      payments-deployer ServiceAccount;
#   2. ledger, whose workload Helm installed, gets no Stage and no Flux
#      object, and its row says Helm owns it;
#   3. fraud 1.0.1, a bundle crafted to say it was inferred from orders'
#      Deployment, stays unplaced: the namespace belongs to orders
#      (team-orders);
#   4. a hand-written Stage is never touched;
#   5. carol approves the first promote of payments-staging: the placement is
#      confirmed as hers, the deployer account exists, the Flux objects are
#      unsuspended at 1.5.0, the pointer pins the CI identity and reaches the
#      plain-HTTP registry as the promoter does, and the workload runs;
#   6. alice confirms the proposed preview rung: payments-preview takes its
#      versions from the registry;
#   7. checkout's temporary environment pr-412, declared in a 2.6 contract
#      with no file, retires retireAfter (1m) after it expires: its Stage and
#      Flux objects are gone, its workload stays and one EnvironmentDeleted is
#      emitted;
#   8. a managed Stage deleted while the promoter is scaled to 0 gives exactly
#      one EnvironmentDeleted once it is back.
set -euo pipefail
CLUSTER="${KIND_CLUSTER:-pacto-environments}"
NS=pacto-system
DNS=pacto-delivery
REG_HOST="pacto-registry.${NS}.svc.cluster.local:5000"
LOCAL_REG_PORT=5607
GITOPS_USERS='["system:serviceaccount:flux-system:kustomize-controller","kubernetes-admin"]'
PAUSE_IMG=registry.k8s.io/pause:3.9@sha256:7031c1b283388d2c2e09b57badb803c05ebed362dc88d84b480cc47f72a21097
SOURCE=https://github.com/acme/shop
CI_SUBJECT="$SOURCE/.github/workflows/release.yml@refs/heads/main"
CONTROLLERS=(source-controller kustomize-controller helm-controller)
# The temporary environment pr-412 expires this long after the run publishes
# it: long enough for the promoter to create its Stage first.
EXPIRES_IN=12m
LADDER_ISSUER=https://idp-ladder.acme.test
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=tests/acceptance/kind/lib.sh
source "$HERE/lib.sh"
# shellcheck source=tests/acceptance/kind/delivery-lib.sh
source "$HERE/delivery-lib.sh"

dump_environments() {
  echo "--- environments + placements ---"
  kubectl -n "$DNS" get configmap pacto-delivery-environments pacto-delivery-placements -o yaml || true
  echo "--- stages, operations and flux objects ---"
  kubectl get stages,operations,ocirepositories,kustomizations,helmreleases -A -o yaml || true
  echo "--- events ---"
  kubectl get events -A --field-selector reason=EnvironmentDeleted -o wide || true
  echo "--- logs deploy/pacto-operator-delivery ---"
  kubectl -n "$DNS" logs deploy/pacto-operator-delivery --tail=300 || true
}
# shellcheck disable=SC2154  # rc is assigned by rc=$? inside the trap body
trap 'rc=$?; [ $rc -ne 0 ] && { dump_diag "$NS"; dump_environments; }; pkill -f "kubectl.*port-forward" 2>/dev/null || true; keep_or_teardown "$NS" "$CLUSTER" delete_cluster; exit $rc' EXIT

for t in oras jq helm curl openssl git; do command -v "$t" >/dev/null 2>&1 || fail "$t is required"; done

VER="$(release_version kubernetes)"
OP_REPO="localhost:5001/pacto-operator/pacto-controller"
OP_IMG="${OP_REPO}:${VER}"
DL_REPO="localhost:5001/pacto-delivery"
DL_IMG="${DL_REPO}:${VER}"
DASH_IMG="localhost:5001/pacto-dashboard:$(release_version core)"
FLUX_DIR="$PACTO_CHART/files/flux"
image_of() { awk '$1 == "image:" { print $2 }' "$FLUX_DIR/deployment-$1.yaml"; }
WORK="$(mktemp -d)"

section "create the cluster and build the images, pacto and the two signers"
cluster_start_bg
load_built_images "$OP_IMG" "$DL_IMG" "$DASH_IMG" || build_images
pull_pinned "$PAUSE_IMG"
FLUX_IMAGES=()
for c in "${CONTROLLERS[@]}"; do FLUX_IMAGES+=("$(image_of "$c")"); done
for img in "${FLUX_IMAGES[@]}"; do infra "pull $img" docker pull -q "$img" >/dev/null; done
PACTO="$(build_pacto)"
SIGN_BIN="$WORK/keylesssign"
LADDER_BIN="$WORK/laddersign"
(cd "$_PACTO_ROOT" && go build -o "$SIGN_BIN" ./tests/acceptance/kind/keylesssign && go build -o "$LADDER_BIN" ./tests/acceptance/kind/laddersign) \
  || fail "building the keyless signers failed"
ensure_cluster
infra "load the images into the cluster" load_images "$OP_IMG" "$DL_IMG" "$DASH_IMG" "${PAUSE_IMG%@*}" "${FLUX_IMAGES[@]}"
# The rung namespaces exist (delivery.environments.createNamespaces stays off),
# and orders-preview and ledger-preview make Pacto propose a preview rung.
for n in "$NS" "$DNS" staging production preview pr-412 orders-staging ledger-staging orders-preview ledger-preview; do
  kubectl create namespace "$n" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
done
CLUSTER_UID="$(kubectl get ns kube-system -o jsonpath='{.metadata.uid}')"

section "publish payments, ledger, fraud and checkout with pacto ci --publish; push orders"
install_registry
REG_PF_PID="$(pf "$LOCAL_REG_PORT" svc/pacto-registry 5000)"
# reg CMD... — CMD with the forwarded port as a plain-HTTP proxy, so it reaches
# the registry AS $REG_HOST, the host the cluster reads, and outside any CI
# context (a pull-request pipeline refuses to publish). The artifacts then name
# no source repository: the runner's would contradict the one $SOURCE's
# signing certificate attests, and the promoter refuses that version.
reg() {
  env -u GITHUB_ACTIONS -u GITHUB_BASE_REF -u GITHUB_STEP_SUMMARY -u GITHUB_REPOSITORY_OWNER -u ACTIONS_ID_TOKEN_REQUEST_URL \
    -u GITHUB_SERVER_URL -u GITHUB_REPOSITORY -u GITLAB_CI -u CI_MERGE_REQUEST_DIFF_BASE_SHA -u CI_PROJECT_URL -u SIGSTORE_ID_TOKEN \
    HTTP_PROXY="http://127.0.0.1:${LOCAL_REG_PORT}" NO_PROXY="" no_proxy="" PACTO_INSECURE_REGISTRIES="$REG_HOST" COSIGN_PASSWORD="" "$@"
}
REPO="$WORK/shop"
mkdir -p "$REPO"
git -C "$REPO" init -q
openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "$WORK/sign.pem" 2>/dev/null || fail "openssl could not make the publish key"
# service NAME VERSION [EXTRA_YAML] — a 2.6 contract in NAME/ that publishes
# a manifests artifact of the Deployment NAME and its Pacto as its deploy
# artifact: the Pacto says whose workload it is.
service() {
  local d="$REPO/$1"
  mkdir -p "$d"
  cp "$WORK/sign.pem" "$d/sign.pem"
  printf 'sign.pem\nrender.sh\n' > "$d/.pactoignore"
  cat > "$d/render.sh" <<SH
#!/bin/sh
set -e
cat > "\$PACTO_OUT/deployment.yaml" <<YAML
apiVersion: apps/v1
kind: Deployment
metadata: {name: $1}
spec:
  replicas: 1
  selector: {matchLabels: {app: $1}}
  template:
    metadata: {labels: {app: $1}, annotations: {version: "\$PACTO_VERSION"}}
    spec:
      containers: [{name: app, image: "${PAUSE_IMG%@*}"}]
YAML
cat > "\$PACTO_OUT/pacto.yaml" <<YAML
apiVersion: pacto.trianalab.io/v1
kind: Pacto
metadata: {name: $1}
spec:
  checkIntervalSeconds: 30
  contractRef: {oci: "oci://${REG_HOST}/acme/$1:\$PACTO_VERSION"}
  target: {workloadRef: {name: $1, kind: Deployment}}
YAML
SH
  chmod +x "$d/render.sh"
  cat > "$d/pacto.yaml" <<YAML
pactoVersion: "2.6"
service:
  name: $1
  version: $2
  owner: {team: team-$1, dri: d, contacts: [{type: email, value: a@e.com, purpose: escalation}]}
workload: service
state: {type: stateless, persistence: {scope: local, durability: ephemeral}, dataCriticality: low}
${3:-}
publish:
  bundle: oci://${REG_HOST}/acme/$1
  deploy: manifests
  artifacts:
    - name: manifests
      type: manifests
      ref: oci://${REG_HOST}/acme/apps/$1
      build: {command: [./render.sh]}
  sign: {key: sign.pem, transparencyLog: false}
YAML
}
EXPIRES="$(date -u -d "+${EXPIRES_IN%m} minutes" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -v+"${EXPIRES_IN%m}"M +%Y-%m-%dT%H:%M:%SZ)"
service payments 1.5.0
service ledger 1.0.0 "environments: [{name: staging}]"
service fraud 1.0.0 "environments: [{name: staging}]"
service checkout 1.0.0 "environments: [{name: pr-412, lifetime: temporary, expires: \"$EXPIRES\"}]"
git -C "$REPO" add -A
git -C "$REPO" -c user.email=ci@e.com -c user.name=ci commit -qm "four services"
out="$(cd "$REPO" && reg "$PACTO" ci --publish --registry "$REG_HOST/acme" 2>&1)" || { echo "$out" >&2; fail "pacto ci --publish failed"; }
# digest REPO:TAG — the manifest digest the forwarded registry resolves.
digest() { oras resolve --plain-http "127.0.0.1:${LOCAL_REG_PORT}/acme/$1" 2>/dev/null || true; }
# BUNDLE_<service> and DEPLOY_<service> hold each published digest.
for s in payments:1.5.0 ledger:1.0.0 fraud:1.0.0 checkout:1.0.0; do
  printf -v "BUNDLE_${s%%:*}" '%s' "$(digest "$s")"
  printf -v "DEPLOY_${s%%:*}" '%s' "$(digest "apps/$s")"
  b="BUNDLE_${s%%:*}" d="DEPLOY_${s%%:*}"
  [ -n "${!b}" ] && [ -n "${!d}" ] || { echo "$out" >&2; fail "pacto ci --publish left no bundle or deploy artifact for $s"; }
done
TAGS="$(oras repo tags --plain-http "127.0.0.1:${LOCAL_REG_PORT}/acme/pacto-index" 2>/dev/null | tr '\n' ' ' || true)"
# Entries are tagged <service key hash>-<version>: three at 1.0.0, one at 1.5.0.
[ "$(wc -w <<<"$TAGS" | tr -d ' ')" = 4 ] && [ "$(tr ' ' '\n' <<<"$TAGS" | grep -c -- '-1\.5\.0$')" = 1 ] \
  || fail "the organization index $REG_HOST/acme/pacto-index does not hold the four services: $TAGS"
# fraud 1.0.1 is a bundle someone crafted by hand, its record naming orders'
# Deployment in orders-staging as what the contract was inferred from: the
# newest signed version, so the one the promoter places.
FD="$WORK/fraud-1.0.1"
mkdir -p "$FD"
sed 's/^  version: 1.0.0$/  version: 1.0.1/' "$REPO/fraud/pacto.yaml" > "$FD/pacto.yaml"
cat > "$FD/pacto.inferred.yaml" <<'YAML'
format: pacto.inferred/v1
inputs: {pacto: "", discovery: "5", sources: []}
fields: []
declaration:
  state: declared
  asker: {subject: alice@acme.test, issuer: https://idp.acme.test, class: human}
  inferredFrom: k8s:orders-staging/Deployment/orders
YAML
tar -czf "$FD/layer.tgz" -C "$FD" pacto.yaml pacto.inferred.yaml
jq -nc --arg d "sha256:$(gzip -dc "$FD/layer.tgz" | openssl dgst -sha256 | awk '{print $NF}')" '{architecture: "", os: "",
  rootfs: {type: "layers", diff_ids: [$d]}, config: {Labels: {"io.pacto.name": "fraud", "io.pacto.pactoVersion": "2.6", "io.pacto.version": "1.0.1"}}}' > "$FD/config.json"
FRAUD="$(cd "$FD" && oras push --plain-http --format go-template='{{.digest}}' --config "config.json:application/vnd.docker.container.image.v1+json" \
  "127.0.0.1:${LOCAL_REG_PORT}/acme/fraud:1.0.1" "layer.tgz:application/vnd.pacto.bundle.layer.v1.tar+gzip")"
[ -n "$FRAUD" ] || fail "pushing the crafted fraud 1.0.1 resolved no digest"
refs=("acme/fraud@$FRAUD")
for s in payments ledger checkout; do b="BUNDLE_$s" d="DEPLOY_$s"; refs+=("acme/$s@${!b}" "acme/apps/$s@${!d}"); done
KEYDIR="$WORK/keys"
mkdir -p "$KEYDIR"
"$SIGN_BIN" -root "$KEYDIR/trusted_root.json" -registry "127.0.0.1:${LOCAL_REG_PORT}" -subject "$CI_SUBJECT" -repository "$SOURCE" "${refs[@]}" \
  || fail "signing the run's versions failed"
pass "pacto ci --publish published and indexed payments 1.5.0, ledger, fraud and checkout (pr-412 expires $EXPIRES); they and a crafted fraud 1.0.1 are signed by $CI_SUBJECT"
# orders is a workload of team-orders that Pacto does not deliver.
od="$WORK/orders"
mkdir -p "$od"
cat > "$od/pacto.yaml" <<'YAML'
pactoVersion: "2.6"
service:
  name: orders
  version: 1.0.0
  owner: {team: team-orders, dri: d, contacts: [{type: email, value: a@e.com, purpose: escalation}]}
workload: service
state: {type: stateless, persistence: {scope: local, durability: ephemeral}, dataCriticality: low}
YAML
ORDERS="$(push_bundle "$PACTO" "$LOCAL_REG_PORT" "$od" "orders:1.0.0")"
[ -n "$ORDERS" ] || fail "pushing orders resolved no digest"

section "helm install pacto with profile idp and its Flux, the installation ladder off"
# The ladder starts once the workloads below and their Pactos exist, as it
# would on a cluster that already runs them: its first pass would otherwise
# place ledger and fraud by name before any Pacto says where they run.
RECORDER_KEY="$KEYDIR/recorder.pem"
openssl genpkey -algorithm ed25519 -out "$RECORDER_KEY" 2>/dev/null || fail "openssl could not generate the recorder's key"
kubectl -n "$DNS" create secret generic recorder-key --from-file=key.pem="$RECORDER_KEY" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
helm install pacto "$PACTO_CHART" -n "$NS" --set profile=idp --set idp.flux.install=true \
  --set global.imageRegistry= --set image.repository="$OP_REPO" --set image.tag="$VER" --set image.pullPolicy=Never \
  --set fullnameOverride=pacto-operator --set registry="$REG_HOST/acme" --set "insecureRegistries[0]=$REG_HOST" \
  --set delivery.createNamespace=false --set-json "delivery.admissionPolicies.gitopsUsers=$GITOPS_USERS" \
  --set delivery.image.repository="$DL_REPO" --set delivery.image.tag="$VER" --set delivery.image.pullPolicy=Never \
  --set "delivery.insecureRegistries[0]=$REG_HOST" --set delivery.promoter.poll=2s --set delivery.promoter.resync=15s \
  --set delivery.environments.enabled=false --set delivery.environments.retireAfter=1m \
  --set delivery.publishers.registry=ghcr.io/acme --set-file delivery.publishers.trustedRoot="$KEYDIR/trusted_root.json" \
  --set delivery.recorder.enabled=true --set delivery.recorder.streamsRepository="$REG_HOST/pacto-delivery-streams" --set delivery.recorder.poll=5s \
  --set delivery.recorder.signing.ed25519.secretName=recorder-key --set delivery.recorder.signing.allowOperatorReadableKey=true \
  --wait --timeout 300s >/dev/null || fail "helm install pacto with profile idp did not roll out"
pass "pacto runs with profile idp and its Flux, the installation ladder off"

section "workloads: orders and its Pacto, ledger by Helm, checkout's in pr-412, a hand-written Stage"
# deployment NS NAME — a Deployment kubectl creates, which no applier manages.
deployment() {
  kubectl -n "$1" create deployment "$2" --image="${PAUSE_IMG%@*}" --dry-run=client -o yaml | kubectl -n "$1" create -f - >/dev/null
}
# pacto_cr NS NAME REF — a Pacto of NAME targeting the Deployment NAME.
pacto_cr() {
  kubectl -n "$1" apply -f - >/dev/null <<YAML
apiVersion: pacto.trianalab.io/v1
kind: Pacto
metadata: {name: $2}
spec:
  checkIntervalSeconds: 30
  contractRef: {oci: "$3"}
  target: {workloadRef: {name: $2, kind: Deployment}}
YAML
}
deployment orders-staging orders
pacto_cr orders-staging orders "oci://${REG_HOST}/demo/orders@${ORDERS}"
deployment pr-412 checkout
# shellcheck disable=SC2154  # printf -v assigned it
pacto_cr pr-412 checkout "oci://${REG_HOST}/acme/checkout@${BUNDLE_checkout}"
chart="$WORK/ledger-chart"
mkdir -p "$chart/templates"
printf 'apiVersion: v2\nname: ledger\nversion: 1.0.0\n' > "$chart/Chart.yaml"
kubectl create deployment ledger --image="${PAUSE_IMG%@*}" --dry-run=client -o yaml > "$chart/templates/deployment.yaml"
helm install ledger "$chart" -n ledger-staging --wait --timeout 120s >/dev/null || fail "helm install ledger failed"
# shellcheck disable=SC2154  # printf -v assigned it
pacto_cr ledger-staging ledger "oci://${REG_HOST}/acme/ledger@${BUNDLE_ledger}"
read_contract() { [ -n "$(kubectl -n "$1" get pacto "$2" -o jsonpath='{.status.contract.owner.team}' 2>/dev/null)" ]; }
for p in orders-staging/orders pr-412/checkout ledger-staging/ledger; do
  eventually 60 read_contract "${p%/*}" "${p#*/}" || fail "the Pacto $p never read its contract: $(kubectl -n "${p%/*}" get pacto "${p#*/}" -o jsonpath='{.status}')"
done
kubectl -n orders-staging apply -f - >/dev/null <<YAML
apiVersion: delivery.pacto.trianalab.io/v1
kind: Stage
metadata: {name: orders}
spec:
  service: {name: orders}
  environment: staging
  applyRef: {kind: OCIRepository, name: orders}
  workload: {kind: Deployment, name: orders}
  candidates: {registry: {repository: "$REG_HOST/demo/orders"}}
  startLevel: advise
YAML
# handwritten — what a person wrote on the orders Stage, which Pacto never changes.
handwritten() { kubectl -n orders-staging get stage orders -o json | jq -c '{g: .metadata.generation, l: .metadata.labels, a: .metadata.annotations, s: .spec}'; }
HANDWRITTEN="$(handwritten)"
pass "orders runs in orders-staging, ledger by Helm in ledger-staging and checkout in pr-412, each with its Pacto"

section "turn the installation ladder on"
helm upgrade pacto "$PACTO_CHART" -n "$NS" --reuse-values --set delivery.environments.enabled=true --wait --timeout 300s >/dev/null \
  || fail "helm upgrade pacto with the installation ladder on did not roll out"
promoter_args="$(kubectl -n "$DNS" get deploy pacto-operator-delivery -o jsonpath='{.spec.template.spec.containers[0].args}')"
for want in --environments=true --environment-ladder=staging,production --environment-create-appliers --environment-retire-after=1m; do
  grep -qF -- "$want" <<<"$promoter_args" || fail "the promoter runs without $want: $promoter_args"
done
pass "the promoter runs the installation ladder staging -> production and creates Flux objects under profile idp"

# placements — the rows the promoter writes, which only it and admins read.
placements() { kubectl -n "$DNS" get configmap pacto-delivery-placements -o jsonpath='{.data.rows\.json}' 2>/dev/null || echo '[]'; }
row() { placements | jq -c --arg s "$1" --arg e "$2" '[.[] | select(.service == $s and .environment == $e)][0] // empty'; }
view() { kubectl -n "$DNS" get configmap pacto-delivery-environments -o jsonpath='{.data.view\.json}' 2>/dev/null; }
stage_json() { kubectl -n "$1" get stage "$2" -o json 2>/dev/null; }
has_stage() { stage_json "$1" "$2" >/dev/null; }
no_stage() { ! has_stage "$1" "$2"; }
managed_of() { kubectl get stages -A -l "delivery.pacto.trianalab.io/service=$1" -o name 2>/dev/null; }
flux_of() { # NS NAME — the Flux objects Pacto names NAME in NS
  kubectl -n "$1" get ocirepository,kustomization,helmrelease -o name 2>/dev/null | grep "/$2\$" || true
}

section "STEP 1: payments gets two managed Stages, each on suspended Flux objects, and no deployer yet"
eventually 80 has_stage staging payments-staging || fail "no Stage staging/payments-staging appeared: $(row payments staging) $(view)"
eventually 40 has_stage production payments-production || fail "no Stage production/payments-production appeared: $(row payments production)"
stage_json staging payments-staging | jq -e '.metadata.labels["delivery.pacto.trianalab.io/managed-by"] == "pacto"
  and (.spec.candidates.registry != null) and (.spec.candidates.upstreamStage == null)
  and .spec.approval.always == true and .spec.startLevel == "autonomous"
  and .metadata.annotations["delivery.pacto.trianalab.io/placement-provenance"] == "default"' >/dev/null \
  || fail "payments-staging is not a managed Stage from the registry holding every promote: $(stage_json staging payments-staging | jq -c '{m: .metadata, s: .spec}')"
stage_json production payments-production | jq -e '.spec.candidates.upstreamStage.name == "payments-staging" and .spec.candidates.registry == null' >/dev/null \
  || fail "payments-production does not take its versions from payments-staging: $(stage_json production payments-production | jq -c .spec.candidates)"
for ns in staging production; do
  name="payments-$ns"
  kubectl -n "$ns" get ocirepository "$name" -o json | jq -e '.spec.suspend == true and (.spec | has("ref") | not) and .spec.insecure == true' >/dev/null \
    || fail "the OCIRepository $ns/$name is not suspended with no ref over plain HTTP: $(kubectl -n "$ns" get ocirepository "$name" -o json | jq -c .spec)"
  kubectl -n "$ns" get kustomization "$name" -o json | jq -e --arg sa payments-deployer '.spec.suspend == true and .spec.serviceAccountName == $sa' >/dev/null \
    || fail "the Kustomization $ns/$name is not suspended as payments-deployer"
  ! kubectl -n "$ns" get serviceaccount payments-deployer >/dev/null 2>&1 || fail "payments-deployer exists in $ns before any approval"
done
pass "payments-staging (registry, every promote held) and payments-production (from payments-staging) sit on suspended Flux objects with no ref, and no deployer exists"

section "STEP 2: ledger, which Helm installed, gets no Stage and no Flux object"
helm_owned() { row ledger staging | jq -e '.placement.provenance == "unknown" and (.placement.evidence | contains("Helm")) and (.stage // "") == ""' >/dev/null 2>&1; }
eventually 40 helm_owned || fail "ledger's staging row does not say Helm owns it: $(row ledger staging)"
[ -z "$(managed_of ledger)" ] || fail "Pacto created a Stage for ledger: $(managed_of ledger)"
[ -z "$(kubectl get ocirepository,kustomization,helmrelease -A -o name | grep ledger || true)" ] || fail "Pacto created Flux objects for ledger"
pass "ledger reads $(row ledger staging | jq -r .placement.evidence), with no Stage and no Flux object"

section "STEP 3: fraud, which says it was inferred from orders' Deployment, stays unplaced"
refused() { row fraud staging | jq -e '.placement.refused == "namespace orders-staging belongs to orders (team-orders)"' >/dev/null 2>&1; }
eventually 40 refused || fail "fraud's staging row is not refused for orders' namespace: $(row fraud staging)"
[ -z "$(managed_of fraud)" ] || fail "Pacto created a Stage for fraud: $(managed_of fraud)"
unplaced() { view | jq -e '.unplacedCount >= 2' >/dev/null 2>&1; }
eventually 20 unplaced || fail "the installation ladder view counts fewer than two unplaced environments: $(view | jq -c '{unplacedCount, completeness}')"
pass "fraud stays unplaced: $(row fraud staging | jq -r .placement.refused)"

section "STEP 4: the hand-written orders Stage is untouched"
untouched() { [ "$(handwritten)" = "$HANDWRITTEN" ]; }
always 10 untouched || fail "the hand-written Stage changed: $HANDWRITTEN, now $(handwritten)"
pass "the hand-written orders Stage kept its generation, labels, annotations and spec"

section "STEP 5: carol approves the first promote of payments-staging; the placement is confirmed"
for u in alice carol; do
  kubectl -n staging create rolebinding "$u-edit" --clusterrole=edit --user="$u" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
done
SERVER="$(kubectl config view --raw --minify -o jsonpath='{.clusters[0].cluster.server}')"
CA="$(kubectl config view --raw --minify -o jsonpath='{.clusters[0].cluster.certificate-authority-data}')"
CERT="$(kubectl config view --raw --minify -o jsonpath='{.users[0].user.client-certificate-data}')"
KEY="$(kubectl config view --raw --minify -o jsonpath='{.users[0].user.client-key-data}')"
# person USER GROUP AUTH_TIME — a kubeconfig impersonating USER in GROUP who
# logged in at AUTH_TIME.
person() {
  local f; f="$(mktemp)"
  cat > "$f" <<YAML
apiVersion: v1
kind: Config
clusters: [{name: kind, cluster: {server: "$SERVER", certificate-authority-data: "$CA"}}]
users:
- name: $1
  user:
    client-certificate-data: "$CERT"
    client-key-data: "$KEY"
    as: $1
    as-groups: [$2]
    as-user-extra: {"delivery.pacto.trianalab.io/auth-time": ["$3"]}
contexts: [{name: c, context: {cluster: kind, user: $1}}]
current-context: c
YAML
  echo "$f"
}
now() { date -u +%Y-%m-%dT%H:%M:%SZ; }
# approve USER GROUP NS OP — the Approval a person with a fresh login creates
# for the decision OP's plan asks.
approve() {
  local at kc uid intent groups decision
  at="$(now)"; kc="$(person "$1" "$2" "$at")"
  uid="$(kubectl -n "$3" get operation "$4" -o jsonpath='{.metadata.uid}')"
  intent="$(kubectl -n "$3" get operation "$4" -o jsonpath='{.spec.intentDigest}')"
  decision="$(field "$3" "$4" plan.floor.decision)"
  groups="$(KUBECONFIG="$kc" kubectl auth whoami -o json | jq -c '.status.userInfo.groups')"
  KUBECONFIG="$kc" kubectl -n "$3" create -f - >/dev/null <<YAML
apiVersion: delivery.pacto.trianalab.io/v1
kind: Approval
metadata:
  generateName: $4-
  ownerReferences: [{apiVersion: delivery.pacto.trianalab.io/v1, kind: Operation, name: "$4", uid: "$uid"}]
spec: {operation: "$4", operationUID: "$uid", intentDigest: "$intent", decision: $decision, note: "the kind run approves", by: "$1", authTime: "$at", groups: $groups}
YAML
}
# shellcheck disable=SC2154  # printf -v assigned it
P150="$DEPLOY_payments"
eventually 90 published_in staging payments-staging "$P150" || fail "payments 1.5.0 never reached status.published of payments-staging"
OP="$(wait_op staging payments-staging "$P150")"
eventually 60 held staging "$OP" || fail "staging/$OP was never held for a person: $(field staging "$OP" phase) $(field staging "$OP" reason)"
kubectl -n staging get operation "$OP" -o jsonpath='{.spec.reason}' | grep -qF "deploy payments to namespace staging" \
  || fail "the held promote does not ask where payments runs: $(kubectl -n staging get operation "$OP" -o jsonpath='{.spec.reason}')"
approve carol team-payments staging "$OP"
confirmed() { [ "$(kubectl -n staging get stage payments-staging -o jsonpath='{.metadata.annotations.delivery\.pacto\.trianalab\.io/placement-confirmed-by}')" = carol ]; }
eventually 60 confirmed || fail "payments-staging's placement was not confirmed by carol: $(stage_json staging payments-staging | jq -c .metadata.annotations)"
unsuspended() {
  kubectl -n staging get ocirepository payments-staging -o json | jq -e --arg d "$P150" '.spec.suspend == false and .spec.ref.digest == $d' >/dev/null &&
    kubectl -n staging get kustomization payments-staging -o json | jq -e '.spec.suspend == false' >/dev/null
}
eventually 30 unsuspended || fail "the Flux objects of payments-staging were not unsuspended at $P150"
# The pointer pins the CI identity, but Flux cannot check it here: with a
# custom trusted root source-controller wants a Rekor log in it, and the
# private Sigstore keylesssign runs has only a timestamp authority. The run
# drops spec.verify after reading it, so Flux applies what the promote pinned;
# the promoter checked the same signature against this root before it placed
# payments.
kubectl -n staging get ocirepository payments-staging -o json | jq -e --arg s "$CI_SUBJECT" \
  '.spec.verify.provider == "cosign" and (.spec.verify.matchOIDCIdentity[0].subject | gsub("\\\\"; "")) == "^\($s)$"' >/dev/null \
  || fail "the pointer of payments-staging does not pin $CI_SUBJECT: $(kubectl -n staging get ocirepository payments-staging -o jsonpath='{.spec.verify}')"
kubectl -n staging patch ocirepository payments-staging --type=json -p '[{"op": "remove", "path": "/spec/verify"}]' >/dev/null \
  || fail "the run could not drop spec.verify from the pointer of payments-staging"
kubectl -n staging get serviceaccount payments-deployer >/dev/null 2>&1 || fail "no payments-deployer ServiceAccount after the approval"
kubectl -n staging get rolebinding payments-deployer -o jsonpath='{.roleRef.name}' | grep -qx pacto-operator-delivery-deployer \
  || fail "payments-deployer is not bound to pacto-operator-delivery-deployer"
runs_payments() {
  kubectl -n staging rollout status deploy/payments --timeout=5s >/dev/null 2>&1 &&
    [ "$(kubectl -n staging get deploy payments -o jsonpath='{.spec.template.metadata.annotations.version}')" = 1.5.0 ]
}
eventually 60 runs_payments || fail "payments 1.5.0 does not run in staging: $(kubectl -n staging get ocirepository,kustomization payments-staging -o yaml)"
pass "carol's approval confirmed the placement, created payments-deployer, unsuspended the Flux objects at 1.5.0 and payments runs in staging"

section "STEP 6: alice confirms the proposed preview rung; payments-preview takes the registry"
proposes_preview() { view | jq -e '[.proposal[]? | select(.name == "preview")] | length == 1' >/dev/null 2>&1; }
eventually 40 proposes_preview || fail "Pacto does not propose preview: $(view | jq -c .proposal)"
# No API serves environmentladders, so `kubectl create clusterrole` refuses it:
# the ClusterRole is written as YAML, as cluster_approver_role does.
kubectl apply -f - >/dev/null <<'YAML'
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata: {name: pacto-ladder-approver}
rules: [{apiGroups: [delivery.pacto.trianalab.io], resources: [environmentladders], verbs: [approve]}]
YAML
kubectl create clusterrolebinding pacto-ladder-approver --clusterrole=pacto-ladder-approver --user=oidc:alice@acme.test \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null
view > "$WORK/view.json"
out="$(reg "$LADDER_BIN" -root "$WORK/ladder-root.json" -view "$WORK/view.json" -cluster "$CLUSTER_UID" -email alice@acme.test -issuer "$LADDER_ISSUER" 2>&1)" \
  || { echo "$out" >&2; fail "laddersign could not confirm the installation ladder"; }
SIGNERS="$(jq -nc --arg i "$LADDER_ISSUER" --rawfile r "$WORK/ladder-root.json" '[{issuer: $i, usernamePrefix: "oidc:", subjects: "[^@]+@acme\\.test", trustedRoot: $r}]')"
helm upgrade pacto "$PACTO_CHART" -n "$NS" --reuse-values --set-json "delivery.identities.signers=$SIGNERS" --wait --timeout 300s >/dev/null \
  || fail "the promoter did not take alice's approver signer"
preview_confirmed() { view | jq -e '([.confirmed[]? | select(.name == "preview")] | length == 1) and .confirmedBy == "oidc:alice@acme.test"' >/dev/null 2>&1; }
eventually 60 preview_confirmed || fail "the installation ladder does not hold preview confirmed by oidc:alice@acme.test: $(view | jq -c '{confirmed, confirmedBy, notes}')"
eventually 60 has_stage preview payments-preview || fail "no Stage preview/payments-preview appeared: $(row payments preview)"
stage_json preview payments-preview | jq -e '.spec.candidates.registry != null and .spec.candidates.upstreamStage == null' >/dev/null \
  || fail "payments-preview does not take its versions from the registry: $(stage_json preview payments-preview | jq -c .spec.candidates)"
pass "alice confirmed preview ($out); payments-preview is a leaf on the registry"

section "STEP 7: checkout's temporary environment pr-412 retires after it expires; the workload stays"
eventually 40 has_stage pr-412 checkout-pr-412 || fail "no Stage pr-412/checkout-pr-412 appeared before it expired: $(row checkout pr-412)"
[ -n "$(flux_of pr-412 checkout-pr-412)" ] || fail "checkout-pr-412 has no Flux objects"
expired() { [ "$(date -u +%s)" -ge "$(date -u -d "$EXPIRES" +%s 2>/dev/null || date -u -j -f %Y-%m-%dT%H:%M:%SZ "$EXPIRES" +%s)" ]; }
until expired; do sleep 10; done
retired() { no_stage pr-412 checkout-pr-412 && [ -z "$(flux_of pr-412 checkout-pr-412)" ]; }
eventually 60 retired || fail "checkout-pr-412 did not retire a minute after $EXPIRES: $(stage_json pr-412 checkout-pr-412 | jq -c .metadata.annotations) $(flux_of pr-412 checkout-pr-412)"
kubectl -n pr-412 get deploy checkout >/dev/null 2>&1 || fail "retiring pr-412 deleted the checkout Deployment"
kubectl get ns pr-412 >/dev/null 2>&1 || fail "retiring pr-412 deleted the namespace"
# deleted NS/STAGE — how many EnvironmentDeleted Events name that Stage.
deleted() {
  kubectl get events -A --field-selector reason=EnvironmentDeleted -o json |
    jq --arg m "Stage $1 for " '[.items[] | select(.message | startswith($m)) | (.count // 1)] | add // 0'
}
retire_event() { [ "$(deleted pr-412/checkout-pr-412)" -ge 1 ]; }
eventually 20 retire_event || fail "retiring checkout-pr-412 emitted no EnvironmentDeleted"
[ "$(deleted pr-412/checkout-pr-412)" = 1 ] || fail "retiring checkout-pr-412 emitted $(deleted pr-412/checkout-pr-412) EnvironmentDeleted Events, want 1"
pass "checkout-pr-412 and its Flux objects retired after $EXPIRES plus 1m; the checkout Deployment and pr-412 stay; one EnvironmentDeleted"

section "STEP 8: a managed Stage deleted while the promoter is down gives one EnvironmentDeleted"
kubectl -n "$DNS" scale deploy pacto-operator-delivery --replicas=0 >/dev/null
kubectl -n "$DNS" wait --for=delete pod -l app.kubernetes.io/component=delivery-promoter --timeout=120s >/dev/null 2>&1 || true
kubectl -n production delete stage payments-production --wait --timeout=60s >/dev/null || fail "kubectl could not delete payments-production"
kubectl -n "$DNS" scale deploy pacto-operator-delivery --replicas=1 >/dev/null
kubectl -n "$DNS" rollout status deploy/pacto-operator-delivery --timeout=180s >/dev/null || fail "the promoter did not come back"
missed() { [ "$(deleted production/payments-production)" -ge 1 ]; }
eventually 40 missed || fail "the delete made while the promoter was down emitted no EnvironmentDeleted"
once() { [ "$(deleted production/payments-production)" = 1 ]; }
always 10 once || fail "the missed delete emitted $(deleted production/payments-production) EnvironmentDeleted Events, want 1"
eventually 40 has_stage production payments-production || fail "the promoter did not create payments-production again"
pass "the delete made while the promoter was down emitted exactly one EnvironmentDeleted, and payments-production is back"

kill "$REG_PF_PID" 2>/dev/null || true
echo "DELIVERY ENVIRONMENTS PASS"
