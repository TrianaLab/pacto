#!/usr/bin/env bash
# Kind acceptance for a service's first Stage on a cluster that runs Argo CD
# and no Flux. The Stage takes its candidates from the registry
# (candidates.registry): the promoter lists the service's version tags and
# verifies each digest's Sigstore signature itself, so nothing from Flux is
# installed or needed. Argo CD's Application is the applier.
#
# The publisher is a CI workflow: acme's release workflow on main, signing
# keylessly against a private Sigstore (tests/acceptance/kind/keylesssign) whose
# trusted root the chart is given. The installation names its registry as
# ghcr.io/acme, so the promoter derives the rule "acme's GitHub Actions on
# main" and keeps no signer list. The artifacts themselves live in the
# in-cluster registry: the rule judges who signed, never where the artifact
# sits.
#
# It proves against the cluster:
#   - no Flux CRD is served, before and after Pacto is installed;
#   - verify: a digest the derived rule's identity signed reaches
#     status.published; an unsigned one is judged unsigned and never does;
#   - promote: a promote moves the Application's OCI source to the accepted
#     digest and Argo CD syncs it;
#   - rollback: a rollback moves it back to the previous digest, and Argo CD
#     syncs that.
set -euo pipefail
CLUSTER="${KIND_CLUSTER:-pacto-delivery-argo}"
NS=pacto-system
DNS=pacto-delivery
APP_NS=payments
ARGO_NS=argocd
REG_HOST="pacto-registry.${NS}.svc.cluster.local:5000"
LOCAL_REG_PORT=5602
# The GitOps controller creates the labelled Application; here the harness does
# it as kind's admin.
GITOPS_USERS='["system:serviceaccount:argocd:argocd-application-controller","kubernetes-admin"]'
# The same Argo CD release and images as gitops-argocd.sh and delivery-demo.sh.
ARGOCD_VERSION=v3.5.2
ARGO_IMAGES=(quay.io/argoproj/argocd:v3.5.2@sha256:e2aadfae709d904e87f46ba4aa49601d827b3022db22cd4d03aae816a2e7097b
  public.ecr.aws/docker/library/redis:8.2.3-alpine@sha256:08ad0b1d280850169a790dba1393ff7a90aef951fc19632cf4d3ce4f78e679ba)
PAUSE_IMG=registry.k8s.io/pause:3.9@sha256:7031c1b283388d2c2e09b57badb803c05ebed362dc88d84b480cc47f72a21097
# Argo CD accepts this layer type by default.
ARGO_LAYER_TYPE=application/vnd.oci.image.layer.v1.tar+gzip
APP_REPO=apps/payments
SOURCE=https://github.com/acme/payments
CI_SUBJECT="$SOURCE/.github/workflows/release.yml@refs/heads/main"
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=tests/acceptance/kind/lib.sh
source "$HERE/lib.sh"
# shellcheck source=tests/acceptance/kind/delivery-lib.sh
source "$HERE/delivery-lib.sh"

dump_argo_delivery() {
  echo "--- stages + operations ---"
  kubectl -n "$APP_NS" get stages,operations -o yaml || true
  echo "--- application ---"
  kubectl -n "$APP_NS" get application payments -o yaml || true
  echo "--- logs deploy/pacto-operator-delivery ---"
  kubectl -n "$DNS" logs deploy/pacto-operator-delivery --tail=300 || true
  for c in argocd-repo-server argocd-application-controller; do
    echo "--- logs $c ($ARGO_NS) ---"
    kubectl -n "$ARGO_NS" logs -l "app.kubernetes.io/name=$c" --tail=200 --all-containers || true
  done
}
# shellcheck disable=SC2154  # rc is assigned by rc=$? inside the trap body
trap 'rc=$?; [ $rc -ne 0 ] && { dump_diag "$NS"; dump_argo_delivery; }; pkill -f "kubectl.*port-forward" 2>/dev/null || true; exit $rc' EXIT

for t in oras jq helm; do command -v "$t" >/dev/null 2>&1 || fail "$t is required"; done

VER="$(release_version kubernetes)"
OP_REPO="localhost:5001/pacto-operator/pacto-controller"
OP_IMG="${OP_REPO}:${VER}"
DL_REPO="localhost:5001/pacto-delivery"
DL_IMG="${DL_REPO}:${VER}"
DASH_IMG="localhost:5001/pacto-dashboard:$(release_version core)"

section "create the cluster and build the images"
cluster_start_bg
load_built_images "$OP_IMG" "$DL_IMG" "$DASH_IMG" || build_images
pull_pinned "$PAUSE_IMG"
# pull_argo REPO:TAG@DIGEST — pull_pinned, except that public.ecr.aws, which
# throttles anonymous pulls from shared runners, falls back to the Docker Hub
# official image its docker/library images mirror (same digest).
pull_argo() {
  local ref="${1%@*}" digest="${1#*@}" hub
  case "$ref" in
    public.ecr.aws/docker/library/*)
      hub="docker.io/library/${ref#public.ecr.aws/docker/library/}"
      if docker pull -q "${ref%:*}@$digest" >/dev/null 2>&1; then
        docker tag "${ref%:*}@$digest" "$ref"
      else
        infra "pull $hub" docker pull -q "${hub%:*}@$digest" >/dev/null
        docker tag "${hub%:*}@$digest" "$ref"
      fi
      ;;
    *) pull_pinned "$1" ;;
  esac
}
for img in "${ARGO_IMAGES[@]}"; do pull_argo "$img"; done
PACTO="$(build_pacto)"
SIGN_BIN="$(mktemp -d)/keylesssign"
(cd "$_PACTO_ROOT" && go build -o "$SIGN_BIN" ./tests/acceptance/kind/keylesssign) || fail "building the keyless signer failed"
OP_CHART="$(package_chart "$PACTO_CHART")"
ensure_cluster
infra "load the images into the cluster" load_images "$OP_IMG" "$DL_IMG" "${PAUSE_IMG%@*}" "${ARGO_IMAGES[@]%@*}"
for n in "$NS" "$DNS" "$APP_NS" "$ARGO_NS"; do
  kubectl create namespace "$n" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
done

no_flux() {
  local n
  n="$(kubectl get crd -o name | grep -c fluxcd.io || true)"
  [ "$n" = 0 ] || fail "the cluster serves $n Flux CRDs ($1): $(kubectl get crd -o name | grep fluxcd.io | tr '\n' ' ')"
}
no_flux "before anything is installed"
pass "kubectl get crd -o name | grep -c fluxcd.io prints 0: no Flux on this cluster"

section "the registry, the contracts and three versions of payments"
install_registry
REG_PF_PID="$(pf "$LOCAL_REG_PORT" svc/pacto-registry 5000)"
# contract VERSION — payments VERSION, pushed as a bundle; prints its
# in-cluster digest-pinned ref.
contract() {
  local d digest
  d="$(mktemp -d)"
  cat > "$d/pacto.yaml" <<YAML
pactoVersion: '2.0'
service:
  name: payments
  version: $1
  owner: {team: team-payments, dri: d, contacts: [{type: email, value: a@e.com, purpose: escalation}]}
workload: service
state: {type: stateless, persistence: {scope: local, durability: ephemeral}, dataCriticality: low}
YAML
  digest="$(push_bundle "$PACTO" "$LOCAL_REG_PORT" "$d" "payments:$1")"
  [ -n "$digest" ] || fail "pushing payments $1 resolved no digest"
  echo "oci://${REG_HOST}/demo/payments@${digest}"
}
# publish VERSION — payments VERSION as the artifact Argo CD pulls: a
# Deployment and its Pacto, annotated with its contract and its source
# repository, tagged VERSION. Prints the digest; signs nothing.
publish() {
  local v="$1" ref tree tgz
  ref="$(contract "$v")"
  tree="$(mktemp -d)"; tgz="$(mktemp -t payments.XXXXXX)"
  cat > "$tree/deployment.yaml" <<YAML
apiVersion: apps/v1
kind: Deployment
metadata: {name: payments}
spec:
  replicas: 1
  selector: {matchLabels: {app: payments}}
  template:
    metadata: {labels: {app: payments}, annotations: {version: "$v"}}
    spec:
      containers: [{name: app, image: "${PAUSE_IMG%@*}"}]
YAML
  cat > "$tree/pacto.yaml" <<YAML
apiVersion: pacto.trianalab.io/v1
kind: Pacto
metadata: {name: payments}
spec:
  checkIntervalSeconds: 30
  contractRef: {oci: "$ref"}
  target: {workloadRef: {name: payments, kind: Deployment}}
YAML
  tar -czf "$tgz" -C "$tree" .
  oras push --plain-http --disable-path-validation --format go-template='{{.digest}}' \
    --annotation "delivery.pacto.trianalab.io/contract=$ref" --annotation "org.opencontainers.image.source=$SOURCE" \
    "127.0.0.1:${LOCAL_REG_PORT}/${APP_REPO}:$v" "${tgz}:${ARGO_LAYER_TYPE}"
}
V1="$(publish 1.0.0)"
V2="$(publish 1.1.0)"
V3="$(publish 1.2.0)"
echo "  V1 = $V1 (1.0.0), V2 = $V2 (1.1.0), V3 = $V3 (1.2.0)"

section "acme's release workflow signs 1.0.0 and 1.1.0 keylessly; 1.2.0 stays unsigned"
KEYDIR="$(mktemp -d)"
"$SIGN_BIN" -root "$KEYDIR/trusted_root.json" -registry "127.0.0.1:${LOCAL_REG_PORT}" \
  -subject "$CI_SUBJECT" -repository "$SOURCE" "$APP_REPO@$V1" "$APP_REPO@$V2" || fail "signing 1.0.0 and 1.1.0 failed"
pass "1.0.0 and 1.1.0 carry a Sigstore signature by $CI_SUBJECT"

section "Argo CD $ARGOCD_VERSION (core install), before Pacto so the chart grants it"
ARGO_MANIFEST="$(mktemp)"
infra "download Argo CD $ARGOCD_VERSION" curl -fsSL --max-time 60 -o "$ARGO_MANIFEST" \
  "https://raw.githubusercontent.com/argoproj/argo-cd/${ARGOCD_VERSION}/manifests/core-install.yaml"
sed -i.bak 's/imagePullPolicy: Always/imagePullPolicy: IfNotPresent/' "$ARGO_MANIFEST"
[ "$(awk '$1 == "image:" { print $2 }' "$ARGO_MANIFEST" | sort -u)" = "$(printf '%s\n' "${ARGO_IMAGES[@]%@*}" | sort -u)" ] \
  || fail "Argo CD $ARGOCD_VERSION's install runs other images than ARGO_IMAGES pins"
kubectl apply -n "$ARGO_NS" --server-side -f "$ARGO_MANIFEST" >/dev/null
argo_crds() { kubectl get applications.argoproj.io -A >/dev/null 2>&1 && kubectl get appprojects.argoproj.io -A >/dev/null 2>&1; }
eventually 40 argo_crds || fail "Argo CD's kinds never reached discovery"
kubectl -n "$ARGO_NS" scale deploy argocd-applicationset-controller --replicas=0 >/dev/null 2>&1 || true
# Patched before the controller restarts, which is when it reads it:
# Applications in the Stage's namespace.
kubectl -n "$ARGO_NS" patch configmap argocd-cmd-params-cm --type merge -p "{\"data\":{\"application.namespaces\":\"$APP_NS\"}}" >/dev/null
kubectl -n "$ARGO_NS" rollout restart statefulset/argocd-application-controller deploy/argocd-repo-server >/dev/null
# The core install has no API server, which is what creates the default
# AppProject.
kubectl apply -f - >/dev/null <<YAML
apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata: {name: default, namespace: $ARGO_NS}
spec:
  sourceRepos: ['*']
  sourceNamespaces: [$APP_NS]
  destinations: [{server: '*', namespace: '*'}]
  clusterResourceWhitelist: [{group: '*', kind: '*'}]
---
apiVersion: v1
kind: Secret
metadata: {name: apps-payments, namespace: $ARGO_NS, labels: {argocd.argoproj.io/secret-type: repository}}
stringData: {name: payments, type: oci, url: "oci://${REG_HOST}/${APP_REPO}", insecureOCIForceHttp: "true"}
YAML
infra "roll out Argo CD's redis" kubectl -n "$ARGO_NS" rollout status deploy/argocd-redis --timeout=300s
infra "roll out Argo CD's repo server" kubectl -n "$ARGO_NS" rollout status deploy/argocd-repo-server --timeout=300s
infra "roll out Argo CD's controller" kubectl -n "$ARGO_NS" rollout status statefulset/argocd-application-controller --timeout=300s
no_flux "after Argo CD"

section "the Pacto operator with delivery on: the publisher registry and the private Sigstore root, no signer list"
helm upgrade --install pacto-operator "$OP_CHART" -n "$NS" \
  --set global.imageRegistry= --set image.repository="$OP_REPO" --set image.tag="$VER" --set image.pullPolicy=Never \
  --set dashboard.enabled=false --set "insecureRegistries[0]=$REG_HOST" \
  --set delivery.enabled=true --set delivery.createNamespace=false --set-json "delivery.admissionPolicies.gitopsUsers=$GITOPS_USERS" \
  --set delivery.image.repository="$DL_REPO" --set delivery.image.tag="$VER" --set delivery.image.pullPolicy=Never \
  --set "delivery.insecureRegistries[0]=$REG_HOST" --set delivery.promoter.poll=2s \
  --set delivery.publishers.registry=ghcr.io/acme --set-file delivery.publishers.trustedRoot="$KEYDIR/trusted_root.json" \
  --wait --timeout 240s >/dev/null || fail "the pacto-operator chart did not roll out"
dl_args="$(kubectl -n "$DNS" get deploy pacto-operator-delivery -o json | jq -c '[.spec.template.spec.containers[].args[]?]')"
jq -e 'index("--flux=false") and index("--argocd") and index("--publishers-registry=ghcr.io/acme")' <<<"$dl_args" >/dev/null \
  || fail "the promoter does not run without Flux, with Argo CD and with the publisher registry: $dl_args"
no_flux "after Pacto"
pass "the promoter runs with --flux=false and --argocd, judging publishers against the rule derived from ghcr.io/acme"

section "people: alice, of payments' team, may update payments"
kubectl -n "$APP_NS" create rolebinding alice-edit --clusterrole=edit --user=alice --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl create clusterrole delivery-planner --verb=list --resource=pactos.pacto.trianalab.io --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl create clusterrolebinding delivery-planner --clusterrole=delivery-planner --user=alice --dry-run=client -o yaml | kubectl apply -f - >/dev/null
may_propose() { [ "$(kubectl auth can-i create operations.delivery.pacto.trianalab.io -n "$APP_NS" --as=alice)" = yes ]; }
eventually 60 may_propose || fail "edit in $APP_NS does not let alice create an Operation: the chart's -edit role was not aggregated"
SERVER="$(kubectl config view --raw --minify -o jsonpath='{.clusters[0].cluster.server}')"
CA="$(kubectl config view --raw --minify -o jsonpath='{.clusters[0].cluster.certificate-authority-data}')"
CERT="$(kubectl config view --raw --minify -o jsonpath='{.users[0].user.client-certificate-data}')"
KEY="$(kubectl config view --raw --minify -o jsonpath='{.users[0].user.client-key-data}')"
ALICE="$(mktemp)"
cat > "$ALICE" <<YAML
apiVersion: v1
kind: Config
clusters: [{name: kind, cluster: {server: "$SERVER", certificate-authority-data: "$CA"}}]
users:
- name: alice
  user:
    client-certificate-data: "$CERT"
    client-key-data: "$KEY"
    as: alice
    as-groups: [team-payments]
    as-user-extra: {"delivery.pacto.trianalab.io/auth-time": ["$(date -u +%Y-%m-%dT%H:%M:%SZ)"]}
contexts: [{name: c, context: {cluster: kind, user: alice}}]
current-context: c
YAML
alice() { KUBECONFIG="$ALICE" "$PACTO" delivery "$@"; }

section "payments' first Stage: an Argo CD Application, candidates from the registry"
kubectl -n "$APP_NS" apply -f - >/dev/null <<YAML
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata: {name: payments, labels: {delivery.pacto.trianalab.io/apply-ref: payments}}
spec:
  project: default
  source: {repoURL: "oci://${REG_HOST}/${APP_REPO}", path: .}
  destination: {server: https://kubernetes.default.svc, namespace: $APP_NS}
  syncPolicy: {automated: {}}
---
apiVersion: delivery.pacto.trianalab.io/v1
kind: Stage
metadata: {name: payments}
spec:
  service: {name: payments}
  environment: argo
  applyRef: {kind: Application, name: payments}
  candidates: {registry: {}}
  startLevel: advise
  gates: {impact: {declaredConsumersOnly: true}, verify: {window: 20s}}
YAML

section "verify: the registry watch accepts what the rule covers and nothing unsigned"
eventually 90 published_in "$APP_NS" payments "$V1" || fail "1.0.0 never reached status.published: $(kubectl -n "$APP_NS" get stage payments -o jsonpath='{.status.publisher}')"
eventually 30 published_in "$APP_NS" payments "$V2" || fail "1.1.0 never reached status.published: $(kubectl -n "$APP_NS" get stage payments -o jsonpath='{.status.publisher}')"
verdict_of() { kubectl -n "$APP_NS" get stage payments -o json | jq -r --arg d "$1" '[.status.publisher.verdicts[]? | select(.digest == $d)][0] | "\(.verdict)/\(.source)"'; }
[ "$(verdict_of "$V1")" = accepted/rule ] || fail "1.0.0 reads $(verdict_of "$V1"), want accepted/rule"
judged_unsigned() { [ "$(verdict_of "$V3")" = unsigned/none ]; }
eventually 30 judged_unsigned || fail "1.2.0 reads $(verdict_of "$V3"), want unsigned/none"
not_published() { ! published_in "$APP_NS" payments "$V3"; }
always 10 not_published || fail "the unsigned 1.2.0 reached status.published"
pass "verify: 1.0.0 and 1.1.0 are published (accepted by the rule derived from ghcr.io/acme); 1.2.0 is unsigned and never published"

# argo_on DIGEST — the Application's source names DIGEST and Argo CD synced it.
argo_on() {
  kubectl -n "$APP_NS" get application payments -o json | jq -e --arg d "$1" \
    '.spec.source.targetRevision == $d and .status.sync.status == "Synced" and (.status.sync.revision // "" | endswith($d | ltrimstr("sha256:")))' >/dev/null
}
runs_version() { [ "$(kubectl -n "$APP_NS" get deploy payments -o jsonpath='{.spec.template.metadata.annotations.version}' 2>/dev/null)" = "$1" ]; }

section "promote: alice ships 1.0.0, then 1.1.0, through the Application"
alice propose -n "$APP_NS" --stage payments --to "$V1" --name payments-1-0-0 --reason "the first version" >/dev/null
wait_concluded "$APP_NS" payments-1-0-0 Succeeded/Verified
alice propose -n "$APP_NS" --stage payments --to "$V2" --name payments-1-1-0 --reason "ship 1.1.0" >/dev/null
wait_concluded "$APP_NS" payments-1-1-0 Succeeded/Verified
eventually 30 argo_on "$V2" || fail "Argo CD is not synced at 1.1.0: $(kubectl -n "$APP_NS" get application payments -o jsonpath='{.spec.source.targetRevision} {.status.sync}')"
runs_version 1.1.0 || fail "the cluster does not run payments 1.1.0"
runs "$APP_NS" payments "$V2" || fail "the Stage's pointer is $(pointer "$APP_NS" payments), want $V2"
pass "promote: the promoter wrote 1.1.0's digest on spec.source.targetRevision and Argo CD synced it"

section "rollback: alice undoes 1.1.0, and the Application goes back to 1.0.0"
alice propose -n "$APP_NS" --stage payments --type rollback --undoes payments-1-1-0 --name payments-undo-1-1-0 --reason "roll back 1.1.0" >/dev/null
wait_concluded "$APP_NS" payments-undo-1-1-0 Succeeded/Verified
eventually 30 argo_on "$V1" || fail "Argo CD is not synced back at 1.0.0: $(kubectl -n "$APP_NS" get application payments -o jsonpath='{.spec.source.targetRevision} {.status.sync}')"
runs_version 1.0.0 || fail "the cluster does not run payments 1.0.0 after the rollback"
runs "$APP_NS" payments "$V1" || fail "the Stage's pointer is $(pointer "$APP_NS" payments), want $V1"
pass "rollback: the Application's source is back on 1.0.0's digest and Argo CD synced it"

kill "$REG_PF_PID" 2>/dev/null || true
echo "DELIVERY ARGO CD FIRST STAGE PASS"
keep_or_teardown "$NS" "$CLUSTER" delete_cluster
