#!/usr/bin/env bash
# Kind acceptance for Pacto delivery: phase 1 ("the core"), the promoter, the
# Pacto contract check and Flux, running end to end on a local cluster; then
# phase 2 ("health") and phase 3 ("publishing"), described where each starts
# below. DELIVERY_SKIP_CORE=1 and DELIVERY_SKIP_HEALTH=1 skip those scenarios
# (the setup still runs).
#
# A Stage promotes only versions `pacto publish` published: signed (Flux
# verifies the signature through spec.verify on the candidate OCIRepository)
# and naming their contract. Every phase 1 and 2 version is therefore signed
# with the run's cosign key as it is pushed, the way CI would publish it.
#
# One service, payments, ships through two Stages: test (fed by a candidate tag
# CI moves) and prod (fed by test's verified versions). A consumer, checkout,
# accepts payments ^1.0.0; a second consumer, ledger, accepts >=1.0.0. Every
# version is an OCI artifact of manifests (a Deployment and its Pacto) whose
# pacto.dev/contract annotation names the digest-pinned contract it was built
# from. The scenario proves the eight claims of the vision's first-step list,
# each against the cluster rather than a status field the promoter wrote about
# itself:
#
#   1. A good version passes testing and reaches production on its own.
#   2. A broken version is rolled back, never reaches production and is never retried.
#   3. A change that affects no other service ships with no person involved.
#   4. A change that breaks a service waits, and only that service's team is asked.
#   5. If Pacto cannot see enough, the change waits instead of shipping.
#   6. When two agents act at once, exactly one wins.
#   7. A person's rollback is not undone by automation.
#   8. A check result for an old version is never used for a new one.
#
# plus the admission policies the chart installs (immutable intent, no agent
# approvals, the pointer edited only by the promoter or break-glass, cancel
# refused after the pointer moved) and a witnessed override for a break-glass
# edit. No promote in the run may produce a witnessed override.
#
# Agents run as their own ServiceAccounts with tokens; people are impersonated
# from the admin credentials with userInfo.extra["pacto.dev/auth-time"] set to
# the moment of login, which is what structured authentication would map from
# an OIDC auth_time claim. The CLI used for propose, cancel and approve is the
# real `pacto delivery`. It runs on the host, where the in-cluster registry name
# does not resolve, so its printed plan is not asserted here: the promoter's
# status.plan is, and plan() is one pure function shared by both.
set -euo pipefail
CLUSTER="${KIND_CLUSTER:-pacto-delivery}"
NS=pacto-system
DELIVERY_NS=pacto-delivery
REG_HOST="pacto-registry.${NS}.svc.cluster.local:5000"
LOCAL_REG_PORT=5602
# Pinned with the GitOps shard: spec.ref.digest and lastAttemptedRevision are both there.
FLUX_VERSION=v2.9.5
FLUX_LAYER_TYPE=application/vnd.cncf.flux.content.v1.tar+gzip
APP_REPO="apps/payments"
# shellcheck source=tests/acceptance/kind/lib.sh
source "$(dirname "$0")/lib.sh"

dump_delivery() {
  echo "--- stages + operations + approvals ---"
  kubectl get stages,operations,approvals -A -o yaml || true
  echo "--- flux ---"
  kubectl get ocirepositories,kustomizations -A -o wide || true
  echo "--- logs deploy/pacto-delivery ---"
  kubectl -n "$DELIVERY_NS" logs deploy/pacto-delivery --tail=300 || true
  for d in source-controller kustomize-controller; do
    echo "--- logs deploy/$d (flux-system) ---"
    kubectl -n flux-system logs "deploy/$d" --tail=100 || true
  done
}
# shellcheck disable=SC2154  # rc is assigned by rc=$? inside the trap body
trap 'rc=$?; [ $rc -ne 0 ] && { dump_diag "$NS"; dump_delivery; }; [ -z "${GEN_PID:-}" ] || kill "$GEN_PID" 2>/dev/null || true; pkill -f "kubectl.*port-forward" 2>/dev/null || true; exit $rc' EXIT

command -v oras >/dev/null 2>&1 || fail "oras is required: it publishes the manifest artifacts Flux pulls"
command -v cosign >/dev/null 2>&1 || fail "cosign is required: every version a Stage promotes is signed"
command -v jq >/dev/null 2>&1 || fail "jq is required to read operations"

VER="$(release_version kubernetes)"
OP_REPO="localhost:5001/pacto-operator/pacto-controller"
OP_IMG="${OP_REPO}:${VER}"
DL_REPO="localhost:5001/pacto-delivery"
DL_IMG="${DL_REPO}:${VER}"

# The operator is coupled to the runtime image (the root Dockerfile): phase 2
# runs its node agent, which is `pacto agent` in that image.
DASH_IMG="localhost:5001/pacto-dashboard:$(release_version core)"
build_operator_images "$OP_IMG" "$DASH_IMG" "$VER"
echo "== build the pacto-delivery image (the operator Dockerfile, its second entrypoint) =="
docker build --load -f "$_PACTO_ROOT/integrations/kubernetes/Dockerfile" --build-arg VERSION="$VER" \
  --build-arg CMD=cmd/delivery -t "$DL_IMG" "$_PACTO_ROOT"
PACTO="$(build_pacto)"
OP_CHART="$(package_chart "$PACTO_CHART")"
DL_CHART="$(package_chart "$_PACTO_ROOT/integrations/kubernetes/charts/pacto-delivery")"

ensure_cluster
load_images "$OP_IMG" "$DL_IMG" "$DASH_IMG"
for n in "$NS" "$DELIVERY_NS" test prod agents; do
  kubectl create namespace "$n" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
done

echo "== zot, the Pacto operator (observing only) and Flux =="
install_registry
REG_PF_PID="$(pf "$LOCAL_REG_PORT" svc/pacto-registry 5000)"
helm install pacto-operator "$OP_CHART" -n "$NS" \
  --set image.repository="$OP_REPO" --set image.tag="$VER" --set image.pullPolicy=Never \
  --set dashboard.enabled=false --set "insecureRegistries[0]=$REG_HOST" --wait --timeout 240s
kubectl apply --server-side -f "https://github.com/fluxcd/flux2/releases/download/${FLUX_VERSION}/install.yaml" >/dev/null
kubectl -n flux-system scale deploy helm-controller --replicas=0 >/dev/null 2>&1 || true
kubectl -n flux-system rollout status deploy/source-controller --timeout=240s
kubectl -n flux-system rollout status deploy/kustomize-controller --timeout=240s

echo "== the signing key: a cosign key pair, its public half in a Secret Flux verifies with =="
KEYDIR="$(mktemp -d)"
(cd "$KEYDIR" && COSIGN_PASSWORD="" cosign generate-key-pair >/dev/null 2>&1) || fail "cosign could not generate a key pair"
for n in test prod; do
  kubectl -n "$n" create secret generic cosign-pub --from-file=cosign.pub="$KEYDIR/cosign.pub" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
done
# sign_digest REPO DIGEST — sign an artifact in the run's registry, as a
# publisher would. The signature lives beside the digest, so it is found under
# the in-cluster name too.
sign_digest() {
  COSIGN_PASSWORD="" cosign sign --yes --key "$KEYDIR/cosign.key" --tlog-upload=false --use-signing-config=false \
    --allow-http-registry "127.0.0.1:${LOCAL_REG_PORT}/$1@$2" >/dev/null 2>&1 || fail "signing $1@$2 failed"
}
VERIFY='verify: {provider: cosign, secretRef: {name: cosign-pub}}'

echo "== the opt-in delivery chart =="
helm install pacto-delivery "$DL_CHART" -n "$DELIVERY_NS" \
  --set image.repository="$DL_REPO" --set image.tag="$VER" --set image.pullPolicy=Never \
  --set "insecureRegistries[0]=$REG_HOST" --set promoter.poll=2s --wait --timeout 240s
if kubectl auth can-i patch ocirepositories.source.toolkit.fluxcd.io -A \
  --as "system:serviceaccount:${NS}:pacto-operator" | grep -q yes; then
  fail "the observing operator can edit the pointer; delivery must add no permission to it"
fi
pass "delivery installed; the operator's service account still cannot change what runs"

echo "== identities: two agents, two people, break-glass =="
for a in agent-a agent-b; do kubectl -n agents create serviceaccount "$a" --dry-run=client -o yaml | kubectl apply -f - >/dev/null; done
kubectl create clusterrolebinding delivery-agents --clusterrole=pacto-delivery-agent \
  --serviceaccount=agents:agent-a --serviceaccount=agents:agent-b --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl create clusterrolebinding delivery-approvers --clusterrole=pacto-delivery-approver \
  --user=alice --user=carol --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl create clusterrole break-glass-pointer --verb=get,list,patch,update \
  --resource=ocirepositories.source.toolkit.fluxcd.io --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl create clusterrolebinding break-glass-pointer --clusterrole=break-glass-pointer \
  --group=pacto:break-glass --dry-run=client -o yaml | kubectl apply -f - >/dev/null
# mallory has the same RBAC as break-glass but is not in the group: only the
# pointer admission policy stands between her and spec.ref.digest.
kubectl create clusterrolebinding pointer-editor --clusterrole=break-glass-pointer \
  --user=mallory --dry-run=client -o yaml | kubectl apply -f - >/dev/null

SERVER="$(kubectl config view --raw --minify -o jsonpath='{.clusters[0].cluster.server}')"
CA="$(kubectl config view --raw --minify -o jsonpath='{.clusters[0].cluster.certificate-authority-data}')"
CERT="$(kubectl config view --raw --minify -o jsonpath='{.users[0].user.client-certificate-data}')"
KEY="$(kubectl config view --raw --minify -o jsonpath='{.users[0].user.client-key-data}')"

# agent_kubeconfig SA — a kubeconfig that is that ServiceAccount, by token.
agent_kubeconfig() {
  local f tok; f="$(mktemp)"; tok="$(kubectl -n agents create token "$1" --duration=3h)"
  cat > "$f" <<YAML
apiVersion: v1
kind: Config
clusters: [{name: kind, cluster: {server: "$SERVER", certificate-authority-data: "$CA"}}]
users: [{name: "$1", user: {token: "$tok"}}]
contexts: [{name: c, context: {cluster: kind, user: "$1"}}]
current-context: c
YAML
  echo "$f"
}
# person_kubeconfig USER GROUP — impersonates USER with a login time of NOW.
person_kubeconfig() {
  local f; f="$(mktemp)"
  cat > "$f" <<YAML
apiVersion: v1
kind: Config
clusters: [{name: kind, cluster: {server: "$SERVER", certificate-authority-data: "$CA"}}]
users:
- name: "$1"
  user:
    client-certificate-data: "$CERT"
    client-key-data: "$KEY"
    as: "$1"
    as-groups: ["$2"]
    as-user-extra: {"pacto.dev/auth-time": ["$(date -u +%Y-%m-%dT%H:%M:%SZ)"]}
contexts: [{name: c, context: {cluster: kind, user: "$1"}}]
current-context: c
YAML
  echo "$f"
}
AGENT_A="$(agent_kubeconfig agent-a)"
AGENT_B="$(agent_kubeconfig agent-b)"
as_agent() { local kc="$1"; shift; KUBECONFIG="$kc" "$PACTO" delivery "$@"; }
# as_person USER TEAM ... — only members of the team a held question names may answer it.
as_person() { local u="$1" g="$2"; shift 2; KUBECONFIG="$(person_kubeconfig "$u" "$g")" "$PACTO" delivery "$@"; }

echo "== contracts =="
contract_dir() { # NAME VERSION WORKLOAD [DEPENDENCY_LINES]
  local d; d="$(mktemp -d)"
  cat > "$d/pacto.yaml" <<YAML
pactoVersion: '2.0'
service:
  name: $1
  version: $2
  owner: {team: team-$1, dri: d, contacts: [{type: email, value: a@e.com, purpose: escalation}]}
workload: $3
state: {type: stateless, persistence: {scope: local, durability: ephemeral}, dataCriticality: low}
${4:-}
YAML
  echo "$d"
}
# contract NAME VERSION WORKLOAD [DEPENDENCY_LINES] -> in-cluster digest-pinned ref.
# Every contract lives in one registry organisation: the graph resolves a
# dependency by name within the consumer's own domain, so the consumers must be
# published beside payments to be counted as its consumers at all.
contract() {
  local digest; digest="$(push_bundle "$PACTO" "$LOCAL_REG_PORT" "$(contract_dir "$1" "$2" "$3" "${4:-}")" "$1:$2")"
  [ -n "$digest" ] || fail "pushing $1 $2 resolved no digest"
  echo "oci://${REG_HOST}/demo/$1@${digest}"
}
C100="$(contract payments 1.0.0 service)"
C110="$(contract payments 1.1.0 service)"
C120="$(contract payments 1.2.0 job)"      # contradicts the Deployment that ships: NonCompliant
C130="$(contract payments 1.3.0 service)"
C140="$(contract payments 1.4.0 service)"
C200="$(contract payments 2.0.0 service)"  # checkout accepts ^1.0.0 only
consumer_contract() { # NAME RANGE
  contract "$1" 1.0.0 service "dependencies: [{name: payments, ref: \"oci://${REG_HOST}/demo/payments\", required: true, compatibility: \"$2\"}]"
}
CHECKOUT="$(consumer_contract checkout "^1.0.0")"
LEDGER="$(consumer_contract ledger ">=1.0.0")"

# publish LABEL CONTRACT_REF — one version: a Deployment and its Pacto, as the
# artifact Flux pulls, annotated with its contract, signed and tagged with its
# label so the registry keeps it. Prints the digest.
publish() {
  local label="$1" ref="$2" tag="$1" tree tgz digest
  tree="$(mktemp -d)"; tgz="$(mktemp -t payments.XXXXXX)"
  cat > "$tree/kustomization.yaml" <<'YAML'
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources: [deployment.yaml, pacto.yaml]
YAML
  cat > "$tree/deployment.yaml" <<YAML
apiVersion: apps/v1
kind: Deployment
metadata: {name: payments}
spec:
  replicas: 1
  selector: {matchLabels: {app: payments}}
  template:
    metadata: {labels: {app: payments}, annotations: {version: "$label"}}
    spec:
      containers: [{name: app, image: registry.k8s.io/pause:3.9}]
YAML
  cat > "$tree/pacto.yaml" <<YAML
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: {name: payments}
spec:
  checkIntervalSeconds: 30
  contractRef: {oci: "${ref:-$C100}"}
  target: {workloadRef: {name: payments, kind: Deployment}}
YAML
  tar -czf "$tgz" -C "$tree" .
  local -a ann=()
  [ -n "$ref" ] && ann=(--annotation "pacto.dev/contract=$ref")
  digest="$(oras push --plain-http --disable-path-validation ${ann[@]+"${ann[@]}"} --format go-template='{{.digest}}' \
    "127.0.0.1:${LOCAL_REG_PORT}/${APP_REPO}:${tag}" "${tgz}:${FLUX_LAYER_TYPE}")"
  sign_digest "$APP_REPO" "$digest"
  echo "$digest"
}
# candidate LABEL — what CI does when a version is ready: move the candidate tag.
candidate() { oras tag --plain-http "127.0.0.1:${LOCAL_REG_PORT}/${APP_REPO}:$1" candidate >/dev/null; }

A="$(publish A "$C100")"; candidate A
echo "  A = $A (payments 1.0.0)"

# --- reading the cluster's state ---------------------------------------------
reconciled() { [ -n "$(kubectl -n "$1" get pacto "$2" -o jsonpath='{.status.contract.serviceName}' 2>/dev/null)" ]; }
pointer() { kubectl -n "$1" get stage payments -o jsonpath='{.status.pointer}'; }
has_pointer() { [ "$(pointer "$1")" = "$2" ]; }
ops() { kubectl -n "$1" get operations -o json; }
# op_to NS DIGEST [TYPE] — the names of operations moving NS to DIGEST.
op_to() { ops "$1" | jq -r --arg d "$2" --arg t "${3:-pacto.dev/promote}" \
  '.items[] | select(.spec.params.to == $d and .spec.type == $t) | .metadata.name'; }
field() { kubectl -n "$1" get operation "$2" -o jsonpath="{.status.$3}"; }
concluded() { [ "$(field "$1" "$2" phase)" = Concluded ]; }
outcome() { echo "$(field "$1" "$2" outcome)/$(field "$1" "$2" verification)"; }
op_exists() { [ -n "$(op_to "$@")" ]; }
wait_op() { # NS DIGEST [TYPE] — prints the one operation that appears
  eventually 60 op_exists "$@" || fail "no ${3:-promote} to $2 appeared in $1"
  op_to "$@" | head -1
}
wait_concluded() { # NS OP WANT_OUTCOME
  eventually 80 concluded "$1" "$2" || fail "$1/$2 never concluded: phase=$(field "$1" "$2" phase) reason=$(field "$1" "$2" reason) message=$(field "$1" "$2" message)"
  [ "$(outcome "$1" "$2")" = "$3" ] || fail "$1/$2 concluded $(outcome "$1" "$2"), want $3 ($(field "$1" "$2" reason): $(field "$1" "$2" message))"
}
held() { [ "$(field "$1" "$2" phase)" = Held ]; }
# published_in NS DIGEST — the Stage remembers DIGEST as published: Flux
# verified its signature on the candidate OCIRepository.
published_in() { kubectl -n "$1" get stage "${3:-payments}" -o json | jq -e --arg d "$2" '.status.published // [] | index($d)' >/dev/null; }

# DELIVERY_SKIP_CORE=1 runs the setup and phase 2 only, for iterating on it.
if [ -z "${DELIVERY_SKIP_CORE:-}" ]; then
echo "== the consumers: checkout accepts ^1.0.0, ledger accepts >=1.0.0, in both environments =="
consumer() { # NS NAME CONTRACT_REF
  kubectl -n "$1" apply -f - >/dev/null <<YAML
apiVersion: apps/v1
kind: Deployment
metadata: {name: $2}
spec:
  replicas: 1
  selector: {matchLabels: {app: $2}}
  template:
    metadata: {labels: {app: $2}}
    spec: {containers: [{name: app, image: registry.k8s.io/pause:3.9}]}
---
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: {name: $2}
spec:
  checkIntervalSeconds: 30
  contractRef: {oci: "$3"}
  target: {workloadRef: {name: $2, kind: Deployment}}
YAML
}
for env in test prod; do consumer "$env" checkout "$CHECKOUT"; consumer "$env" ledger "$LEDGER"; done

# Every Pacto must be reconciled before the first plan: a Pacto with no contract
# in its status is a consumer Pacto cannot rule out, and the plan would wait.
for env in test prod; do
  for p in checkout ledger; do eventually 60 reconciled "$env" "$p" || fail "$env/$p was never reconciled"; done
done

echo "== Flux sources, Kustomizations and Stages =="
apply_env() { # ENV
  kubectl -n "$1" apply -f - >/dev/null <<YAML
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata:
  name: payments
  labels: {delivery.pacto.trianalab.io/apply-ref: payments}
spec: {interval: 30s, insecure: true, url: "oci://${REG_HOST}/${APP_REPO}", ref: {digest: "$A"}, $VERIFY}
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata: {name: payments}
spec:
  interval: 1m
  sourceRef: {kind: OCIRepository, name: payments}
  path: ./
  prune: true
  timeout: 2m
  targetNamespace: $1
  # The Deployment only. Waiting on every object would make Flux wait for the
  # Pacto to observe its new generation, which hides the stale verdict item 8
  # is about: the promoter, not Flux, must refuse a result for an old version.
  healthChecks: [{apiVersion: apps/v1, kind: Deployment, name: payments, namespace: $1}]
YAML
}
apply_env test
apply_env prod
for env in test prod; do eventually 60 reconciled "$env" payments || fail "$env/payments was never reconciled"; done
kubectl -n test apply -f - >/dev/null <<YAML
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata: {name: payments-candidates}
spec: {interval: 10s, insecure: true, url: "oci://${REG_HOST}/${APP_REPO}", ref: {tag: candidate}, $VERIFY}
---
apiVersion: delivery.pacto.trianalab.io/v1alpha1
kind: Stage
metadata: {name: payments}
spec:
  service: {name: payments}
  environment: test
  applyRef: {name: payments}
  candidates: {ociRepositoryRef: {name: payments-candidates}}
  auto: true
  gates: {verify: {window: 3m}}
YAML
kubectl -n prod apply -f - >/dev/null <<YAML
apiVersion: delivery.pacto.trianalab.io/v1alpha1
kind: Stage
metadata: {name: payments}
spec:
  service: {name: payments}
  environment: prod
  applyRef: {name: payments}
  candidates: {upstreamStage: {name: payments, namespace: test}}
  auto: true
  gates: {verify: {window: 3m}}
YAML

echo "== bootstrap: A reaches test, then prod, by itself =="
OP="$(wait_op test "$A")"; wait_concluded test "$OP" Succeeded/Verified
OP="$(wait_op prod "$A")"; wait_concluded prod "$OP" Succeeded/Verified
pass "A verified in test and in prod"

echo "== items 1, 3 and 8: B (compatible) ships to production with no person, and a stale check never counts =="
# The operator is stopped before B ships, so the Pacto status still reports A's
# contract as Compliant when Flux applies B. That result must not verify B.
kubectl -n "$NS" scale deploy pacto-operator --replicas=0 >/dev/null
kubectl -n "$NS" rollout status deploy/pacto-operator --timeout=60s >/dev/null
B="$(publish B "$C110")"; candidate B
OPB="$(wait_op test "$B")"
verifying() { [ "$(field test "$OPB" phase)" = Verifying ]; }
eventually 60 verifying || fail "B never reached Verifying: $(field test "$OPB" phase) $(field test "$OPB" message)"
stale_ignored() {
  [ "$(field test "$OPB" phase)" = Verifying ] && field test "$OPB" message | grep -q "last evaluated contract"
}
always 7 stale_ignored || fail "a Compliant result for A's contract was used for B: $(field test "$OPB" phase) $(field test "$OPB" message)"
pass "item 8: with A's Compliant verdict still in the Pacto status, B stays unverified (Unknown)"
# Each refusal is captured before it is grepped: under pipefail, `kubectl ... |
# grep -q` reports kubectl's own non-zero exit even when grep matched.
OUT="$(KUBECONFIG="$AGENT_A" kubectl -n test delete operation "$OPB" --wait=false 2>&1 || true)"
if grep -q "can no longer be cancelled" <<<"$OUT"; then
  pass "cancel is refused once the pointer moved"
else
  fail "deleting an applied operation was not refused: $OUT"
fi
kubectl -n "$NS" scale deploy pacto-operator --replicas=1 >/dev/null
wait_concluded test "$OPB" Succeeded/Verified
OPBP="$(wait_op prod "$B")"; wait_concluded prod "$OPBP" Succeeded/Verified
has_pointer prod "$B" || fail "prod does not run B"
[ "$(kubectl -n prod get deploy payments -o jsonpath='{.spec.template.metadata.annotations.version}')" = B ] \
  || fail "prod's Deployment is not the B manifest"
for pair in "test:$OPB" "prod:$OPBP"; do
  env="${pair%%:*}" op="${pair#*:}"
  [ "$(field "$env" "$op" actorClass)" = controller ] || fail "$env/$op was not raised by the promoter"
  [ -z "$(field "$env" "$op" approvals)" ] || fail "$env/$op involved a person"
  kubectl -n "$env" get operation "$op" -o json | jq -e '.status.plan.floor.human != true' >/dev/null || fail "$env/$op asked for a person"
done
pass "items 1 and 3: B passed test and reached production with nobody involved"

echo "== item 2: C (broken) fails in test, is rolled back once, never reaches prod and is never retried =="
C="$(publish C "$C120")"; candidate C
OPC="$(wait_op test "$C")"
wait_concluded test "$OPC" Failed/
[ "$(field test "$OPC" reason)" = VerifyFailed ] || fail "C failed for the wrong reason: $(field test "$OPC" reason)"
RB="$(wait_op test "$B" pacto.dev/rollback)"
wait_concluded test "$RB" Succeeded/Verified
has_pointer test "$B" || fail "test is not back on B"
kubectl -n test get stage payments -o json | jq -e --arg c "$C" '.status.failedDigests | index($c)' >/dev/null || fail "C is not remembered as failed"
[ "$(op_to test "$B" pacto.dev/rollback | wc -l | tr -d ' ')" = 1 ] || fail "more than one automatic rollback for one cause"
never_retried() { [ "$(op_to test "$C" | wc -l | tr -d ' ')" = 1 ] && [ -z "$(op_to prod "$C")" ] && has_pointer prod "$B"; }
always 10 never_retried || fail "C was retried in test or reached prod"
pass "item 2: C rolled back by one bound rollback, absent from prod, not retried"

echo "== item 5: a published artifact whose contract cannot be read waits for a person instead of shipping =="
# E is signed and names a digest-pinned contract that is not in the registry.
E="$(publish E "oci://${REG_HOST}/demo/payments@sha256:$(printf '0%.0s' $(seq 64))")"; candidate E
OPE="$(wait_op test "$E")"
eventually 30 held test "$OPE" || fail "E was not held: $(field test "$OPE" phase) $(field test "$OPE" reason)"
field test "$OPE" question | grep -q "cannot see enough" || fail "E's question does not say Pacto cannot see enough: $(field test "$OPE" question)"
kubectl -n test get operation "$OPE" -o json | jq -e '.status.plan.floor.decision == "accept-unknown"' >/dev/null || fail "E's floor is not accept-unknown"
stays_on_b() { has_pointer test "$B" && held test "$OPE"; }
always 5 stays_on_b || fail "E moved while Pacto could not judge it"
# Move the candidate tag back to B before cancelling: a tag pointing at a digest
# test already verified is not a new version, so nothing raises E again once its
# operation is gone.
candidate B
candidate_is_b() { [ "$(kubectl -n test get ocirepository payments-candidates -o jsonpath='{.status.artifact.revision}')" = "candidate@$B" ]; }
eventually 30 candidate_is_b || fail "the candidate OCIRepository never saw the tag move back to B"
as_agent "$AGENT_A" cancel -n test "$OPE" >/dev/null
# A cancelled operation that never moved anything concludes Cancelled and, with
# no recorder in this phase, is let go at once.
cancelled() { ! kubectl -n test get operation "$OPE" >/dev/null 2>&1 || [ "$(outcome test "$OPE")" = Cancelled/ ]; }
eventually 20 cancelled || fail "E was not cancelled: $(field test "$OPE" phase) $(field test "$OPE" outcome)"
always 5 has_pointer test "$B" || fail "cancelling E moved the pointer"
pass "item 5: E held for accept-unknown and cancelled by the agent; nothing moved"

echo "== item 6: two agents propose at once; exactly one wins =="
G="$(publish G "$C130")"
H="$(publish H "$C140")"
# Both must be published before an agent may propose them: Flux verifies each
# on the candidate OCIRepository. Auto-promotion is paused meanwhile, so the
# race is between the two agents and nothing else.
kubectl -n test patch stage payments --type merge -p '{"spec":{"auto":false}}' >/dev/null
for v in G H; do
  candidate "$v"
  eventually 60 published_in test "${!v}" || fail "$v was never verified as published: $(kubectl -n test get ocirepository payments-candidates -o jsonpath='{.status.conditions}')"
done
as_agent "$AGENT_A" propose -n test --stage payments --to "$G" --name race-g --reason "agent a" >/dev/null &
PA=$!
as_agent "$AGENT_B" propose -n test --stage payments --to "$H" --name race-h --reason "agent b" >/dev/null &
PB=$!
wait "$PA"; wait "$PB"
eventually 60 concluded test race-g || fail "race-g never concluded"
eventually 60 concluded test race-h || fail "race-h never concluded"
WON=0
for o in race-g race-h; do
  case "$(outcome test "$o")" in
    Succeeded/Verified) WON=$((WON + 1)) ;;
    Superseded/ | Rejected/) ;;
    *) fail "$o ended $(outcome test "$o") ($(field test "$o" reason))" ;;
  esac
done
[ "$WON" = 1 ] || fail "$WON of two racing promotes won"
WINNER="$G"; [ "$(outcome test race-h)" = Succeeded/Verified ] && WINNER="$H"
has_pointer test "$WINNER" || fail "test does not run the winner"
pass "item 6: race-g $(outcome test race-g), race-h $(outcome test race-h); one winner"
# The candidate tag goes to the winner (already verified, so nothing new) and
# auto-promotion resumes.
WLABEL=G; [ "$WINNER" = "$H" ] && WLABEL=H
candidate "$WLABEL"
candidate_is_winner() { [ "$(kubectl -n test get ocirepository payments-candidates -o jsonpath='{.status.artifact.revision}')" = "candidate@$WINNER" ]; }
eventually 30 candidate_is_winner || fail "the candidate OCIRepository never saw the tag move to the winner"
kubectl -n test patch stage payments --type merge -p '{"spec":{"auto":true}}' >/dev/null
OPW="$(wait_op prod "$WINNER")"; wait_concluded prod "$OPW" Succeeded/Verified

echo "== item 4: F (payments 2.0.0) breaks checkout; it waits, and only checkout's team is asked =="
F="$(publish F "$C200")"; candidate F
OPF="$(wait_op test "$F")"
eventually 30 held test "$OPF" || fail "F was not held: $(field test "$OPF" phase) $(field test "$OPF" message)"
Q="$(field test "$OPF" question)"
echo "  question: $Q"
grep -q "team-checkout" <<<"$Q" || fail "the question does not name checkout's team"
if grep -q "team-ledger" <<<"$Q" || grep -q "team-payments" <<<"$Q"; then fail "the question asks a team whose service does not break"; fi
kubectl -n test get operation "$OPF" -o json | jq -e '.status.plan.floor.owners == ["team-checkout"]' >/dev/null \
  || fail "the floor is not routed to team-checkout alone"
waits_for_f() { has_pointer test "$WINNER" && held test "$OPF"; }
always 5 waits_for_f || fail "F moved without a person"
# No agent can approve: RBAC refuses the create, and the CLI refuses first.
OUT="$(as_agent "$AGENT_A" approve -n test "$OPF" 2>&1 || true)"
if grep -q "agents may not approve" <<<"$OUT"; then pass "the agent cannot approve"; else fail "an agent approved: $OUT"; fi
if KUBECONFIG="$AGENT_A" kubectl -n test create -f - 2>/dev/null <<YAML
apiVersion: delivery.pacto.trianalab.io/v1alpha1
kind: Approval
metadata: {name: forged}
spec: {operation: $OPF, operationUID: x, intentDigest: x, decision: approve, by: alice}
YAML
then fail "an agent created an Approval"; fi
# A person without a login time is refused by admission.
if kubectl --as carol --as-group humans -n test create -f - 2>/dev/null <<YAML
apiVersion: delivery.pacto.trianalab.io/v1alpha1
kind: Approval
metadata: {name: no-login}
spec: {operation: $OPF, operationUID: x, intentDigest: x, decision: approve, by: carol}
YAML
then fail "an approval without a fresh login was admitted"; fi
# Only checkout's team may answer: alice is ignored while in team-payments and counts once in team-checkout.
as_person alice team-payments approve -n test "$OPF" --decision approve --note "not my team" >/dev/null
always 3 waits_for_f || fail "an approval from outside team-checkout moved F"
as_person alice team-checkout approve -n test "$OPF" --decision approve --note "checkout migrates next sprint" >/dev/null
wait_concluded test "$OPF" Succeeded/Verified
[ "$(kubectl -n test get operation "$OPF" -o jsonpath='{.status.approvals[*].by}')" = alice ] || fail "the approval is not recorded, or the one from outside the team was"
pass "item 4: F held for team-checkout only, answered only by a team-checkout member, verified in test"
OPFP="$(wait_op prod "$F")"
eventually 30 held prod "$OPFP" || fail "F was not held in prod"
as_person carol team-checkout approve -n prod "$OPFP" --decision approve >/dev/null
wait_concluded prod "$OPFP" Succeeded/Verified

echo "== item 7: a person's rollback in prod is not undone by automation =="
as_person alice humans propose -n prod --stage payments --type rollback --undoes "$OPFP" --name human-undo --reason "checkout errors" >/dev/null
wait_concluded prod human-undo Succeeded/Verified
has_pointer prod "$WINNER" || fail "prod is not back on $WINNER"
[ "$(kubectl -n prod get stage payments -o jsonpath='{.status.autoHold}')" = true ] || fail "no auto-hold after a person's rollback"
held_back() { has_pointer prod "$WINNER" && [ "$(op_to prod "$F" | wc -l | tr -d ' ')" = 1 ]; }
always 10 held_back || fail "automation promoted F again after a person rolled it back"
pass "item 7: prod stays on the version alice chose; auto-promotion is on hold"

echo "== admission: the intent is immutable, and only break-glass edits the pointer =="
OUT="$(kubectl -n prod patch operation human-undo --type merge -p '{"spec":{"reason":"edited"}}' 2>&1 || true)"
if grep -q "immutable" <<<"$OUT"; then
  pass "an operation's spec cannot be edited"
else
  fail "an operation's spec was edited: $OUT"
fi
OUT="$(KUBECONFIG="$AGENT_A" kubectl -n prod create -f - 2>&1 <<YAML || true
apiVersion: delivery.pacto.trianalab.io/v1alpha1
kind: Operation
metadata: {name: forged, finalizers: [delivery.pacto.trianalab.io/record]}
spec: {type: pacto.dev/promote, stage: payments, facet: pointer, params: {from: "$WINNER", to: "$F"}, actor: {user: alice}}
YAML
)"
if grep -q "spec.actor.user must be the user" <<<"$OUT"; then
  pass "an agent cannot propose in a person's name"
else
  fail "an operation with a forged actor was admitted: $OUT"
fi
ORIG="$(kubectl -n prod get ocirepository payments -o jsonpath='{.spec.ref.digest}')"
OUT="$(kubectl --as mallory -n prod patch ocirepository payments --type merge \
  -p "{\"spec\":{\"ref\":{\"digest\":\"$B\"}}}" 2>&1 || true)"
if grep -q "the Pacto delivery pointer" <<<"$OUT"; then
  pass "a user with RBAC but no break-glass cannot edit the pointer"
else
  fail "the pointer admission policy let a non-break-glass edit through: $OUT"
fi
KUBECONFIG="$(person_kubeconfig ops pacto:break-glass)" kubectl -n prod patch ocirepository payments --type merge \
  -p "{\"spec\":{\"ref\":{\"digest\":\"$B\"}}}" >/dev/null
OPW2="$(wait_op prod "$B" pacto.dev/override)"
wait_concluded prod "$OPW2" Witnessed/Unverified
[ "$(kubectl -n prod get stage payments -o jsonpath='{.status.frozen}')" = true ] || fail "a witnessed edit must freeze the Stage"
has_pointer prod "$B" || fail "the witnessed edit was not adopted"
[ "$ORIG" = "$WINNER" ] || fail "the pointer before the edit was $ORIG"
pass "break-glass edit witnessed, adopted and frozen"

echo "== no promote in the run produced a witnessed override =="
WITNESSED="$(kubectl get operations -A -o json | jq '[.items[] | select(.spec.witnessed == true)] | length')"
PROMOTES="$(kubectl get operations -A -o json | jq '[.items[] | select(.spec.type == "pacto.dev/promote")] | length')"
[ "$WITNESSED" = 1 ] || fail "$WITNESSED witnessed overrides, want exactly the one break-glass edit"
pass "$PROMOTES promotes, 0 witnessed overrides of their own"

echo "DELIVERY CORE PASS"
fi

# DELIVERY_SKIP_HEALTH=1 runs the setup and phase 3 only, for iterating on it.
if [ -z "${DELIVERY_SKIP_HEALTH:-}" ]; then
# --- phase 2: health ----------------------------------------------------------
# Mira (OTLP in, the query API the health gate reads) and the operator's node
# agent (kernel evidence the claim gate reads) join the cluster. One Stage,
# health/payments, verifies with the Pacto contract check, a Mira error-ratio
# check and the pacto.runtime claim check, and re-checks every 10s once a version
# shipped. A generator on the host sends payments spans to Mira in the mode the
# running version is set to (ok, errors or silent), which stands in for the
# telemetry of a real workload. It proves:
#   - a version whose error ratio exceeds the limit is rolled back, and the
#     rollback cites the Mira query with absolute bounds;
#   - too little telemetry holds the version as Unknown, never Pass and never a
#     rollback, until a person decides;
#   - a regression after a version was verified triggers exactly one rollback;
#   - a version the node agent sees calling a service its contract never
#     declares fails the claim gate, though Pacto's own status is only Warning.
echo "== phase 2: Mira, the node agent and a Stage with health and claim checks =="
H=health
MIRA_IMG=ghcr.io/trianalab/mira:0.4.3
AGNHOST=registry.k8s.io/e2e-test-images/agnhost:2.53
MIRA_PORT=5603
H_REPO=apps/payments-health
# The operator restarts with the node agent first: the new manager takes over
# the leader lease from the old one, which can take a few minutes on kind.
AGENT_NS=pacto-node-agent
helm upgrade pacto-operator "$OP_CHART" -n "$NS" --reuse-values \
  --set nodeAgent.enabled=true --set nodeAgent.publishInterval=5s --wait --timeout 240s >/dev/null
for img in "$MIRA_IMG" "$AGNHOST"; do docker image inspect "$img" >/dev/null 2>&1 || docker pull -q "$img" >/dev/null; done
load_images "$MIRA_IMG" "$AGNHOST"
kubectl create namespace "$H" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n "$H" create secret generic cosign-pub --from-file=cosign.pub="$KEYDIR/cosign.pub" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
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
        image: $MIRA_IMG
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
kubectl -n "$NS" rollout status deploy/mira --timeout=120s >/dev/null
MIRA_PF_PID="$(pf "$MIRA_PORT" svc/mira 4318)"

helm upgrade pacto-delivery "$DL_CHART" -n "$DELIVERY_NS" --reuse-values \
  --set-json "gates.providers=[{\"name\":\"mira\",\"type\":\"mira\",\"url\":\"http://mira.${NS}.svc.cluster.local:4318\"}]" \
  --wait --timeout 240s >/dev/null
kubectl -n "$DELIVERY_NS" rollout status deploy/pacto-delivery --timeout=120s >/dev/null
eventually 40 kubectl -n "$AGENT_NS" get ds pacto-node-agent >/dev/null 2>&1 || fail "the operator never created the node agent"
kubectl -n "$AGENT_NS" rollout status ds/pacto-node-agent --timeout=180s >/dev/null
agent_running() {
  [ "$(kubectl -n "$AGENT_NS" get noderuntimeobservations.pacto.trianalab.io -o jsonpath='{.items[0].status.collection.state}' 2>/dev/null)" = Running ]
}
eventually 40 agent_running || fail "the node agent never reported Running collection: $(kubectl -n "$AGENT_NS" get noderuntimeobservations.pacto.trianalab.io -o yaml)"
pass "Mira and the node agent run; the promoter knows the mira provider"

echo "== the generator: payments spans in the mode the running version is set to =="
GEN="$(mktemp -d)"
gen_loop() {
  local ver mode
  while sleep 2; do
    ver="$(kubectl -n "$H" get deploy payments -o jsonpath='{.spec.template.metadata.annotations.version}' 2>/dev/null)" || continue
    mode="$(cat "$GEN/$ver" 2>/dev/null || echo ok)"
    python3 - "$mode" <<'PY' | curl -s -o /dev/null -X POST "http://127.0.0.1:${MIRA_PORT}/v1/traces" \
      -H 'content-type: application/json' --data-binary @- || true
import json, os, sys, time
mode, now = sys.argv[1], time.time_ns()
errors = {"ok": 0, "errors": 5, "silent": 0}[mode]
n = 0 if mode == "silent" else 10
spans = [{"traceId": os.urandom(16).hex(), "spanId": os.urandom(8).hex(), "name": "POST /pay", "kind": 2,
          "startTimeUnixNano": str(now - i * 1000), "endTimeUnixNano": str(now - i * 1000 + 2_000_000),
          "status": {"code": 2 if i < errors else 1}} for i in range(n)]
print(json.dumps({"resourceSpans": [{"resource": {"attributes": [{"key": "service.name", "value": {"stringValue": "payments"}}]},
                                     "scopeSpans": [{"spans": spans}]}]}))
PY
  done
}
gen_loop &
GEN_PID=$!
mode() { echo "$2" > "$GEN/$1"; } # LABEL MODE

echo "== the fraud service: Pacto-managed, and in no payments contract =="
FRAUD="$(contract fraud 1.0.0 service)"
kubectl -n "$H" apply -f - >/dev/null <<YAML
apiVersion: apps/v1
kind: Deployment
metadata: {name: fraud}
spec:
  replicas: 1
  selector: {matchLabels: {app: fraud}}
  template:
    metadata: {labels: {app: fraud}}
    spec:
      containers:
      - {name: server, image: $AGNHOST, imagePullPolicy: Never, args: [netexec, --http-port=8080], ports: [{containerPort: 8080}],
         readinessProbe: {httpGet: {path: /healthz, port: 8080}, periodSeconds: 2}}
---
apiVersion: v1
kind: Service
metadata: {name: fraud}
spec: {selector: {app: fraud}, ports: [{name: http, port: 80, targetPort: 8080}]}
---
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: {name: fraud}
spec:
  checkIntervalSeconds: 30
  contractRef: {oci: "$FRAUD"}
  target: {serviceName: fraud}
YAML
kubectl -n "$H" rollout status deploy/fraud --timeout=120s >/dev/null
eventually 60 reconciled "$H" fraud || fail "the fraud Pacto was never reconciled"

# publish_health LABEL [CALLS] — a payments version for the health Stage: an
# agnhost Deployment and its Pacto on payments 1.0.0 (no dependencies). With
# CALLS, a second container connects to fraud every 2s.
publish_health() {
  local label="$1" tree tgz extra=""
  tree="$(mktemp -d)"; tgz="$(mktemp -t payments-health.XXXXXX)"
  [ -n "${2:-}" ] && extra="
      - {name: caller, image: $AGNHOST, imagePullPolicy: Never, command: [sh, -c, 'while true; do nc -z -w 2 fraud 80; sleep 2; done']}"
  cat > "$tree/kustomization.yaml" <<'YAML'
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources: [deployment.yaml, pacto.yaml]
YAML
  cat > "$tree/deployment.yaml" <<YAML
apiVersion: apps/v1
kind: Deployment
metadata: {name: payments}
spec:
  replicas: 1
  selector: {matchLabels: {app: payments}}
  template:
    metadata: {labels: {app: payments}, annotations: {version: "$label"}}
    spec:
      terminationGracePeriodSeconds: 1
      containers:
      - {name: app, image: $AGNHOST, imagePullPolicy: Never, args: [pause]}$extra
YAML
  cat > "$tree/pacto.yaml" <<YAML
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: {name: payments}
spec:
  checkIntervalSeconds: 30
  contractRef: {oci: "$C100"}
  target: {workloadRef: {name: payments, kind: Deployment}}
YAML
  tar -czf "$tgz" -C "$tree" .
  local digest
  digest="$(oras push --plain-http --disable-path-validation --annotation "pacto.dev/contract=$C100" --format go-template='{{.digest}}' \
    "127.0.0.1:${LOCAL_REG_PORT}/${H_REPO}:${label}" "${tgz}:${FLUX_LAYER_TYPE}")"
  sign_digest "$H_REPO" "$digest"
  echo "$digest"
}
H1="$(publish_health h1)"
H2="$(publish_health h2)"; mode h2 errors
Q="$(publish_health q)"; mode q silent
H5="$(publish_health h5)"
R="$(publish_health r calls)"

kubectl -n "$H" apply -f - >/dev/null <<YAML
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata:
  name: payments
  labels: {delivery.pacto.trianalab.io/apply-ref: payments}
spec: {interval: 30s, insecure: true, url: "oci://${REG_HOST}/${H_REPO}", ref: {digest: "$H1"}, $VERIFY}
---
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata: {name: payments-candidates}
spec: {interval: 5s, insecure: true, url: "oci://${REG_HOST}/${H_REPO}", ref: {tag: candidate}, $VERIFY}
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata: {name: payments}
spec:
  interval: 1m
  sourceRef: {kind: OCIRepository, name: payments}
  path: ./
  prune: true
  timeout: 2m
  targetNamespace: $H
  healthChecks: [{apiVersion: apps/v1, kind: Deployment, name: payments, namespace: $H}]
---
apiVersion: delivery.pacto.trianalab.io/v1alpha1
kind: Stage
metadata: {name: payments}
spec:
  service: {name: payments}
  environment: health
  applyRef: {name: payments}
  candidates: {ociRepositoryRef: {name: payments-candidates}}
  gates:
    verify:
      window: 2m
      recheck: 10s
      checks:
      - name: health
        provider: mira
        params: {minSpans: "20", maxErrorRatio: "0.2", soak: "20s"}
      - name: claims
        provider: pacto.runtime
YAML
eventually 60 reconciled "$H" payments || fail "health/payments was never reconciled"

# ship LABEL DIGEST — CI moves the candidate tag to the version, Flux verifies
# its signature, and agent-a proposes it; prints the operation name.
ship() {
  oras tag --plain-http "127.0.0.1:${LOCAL_REG_PORT}/${H_REPO}:$1" candidate >/dev/null
  eventually 60 published_in "$H" "$2" || fail "$1 was never verified as published"
  as_agent "$AGENT_A" propose -n "$H" --stage payments --to "$2" --name "promote-$1" --reason "phase 2: $1" >/dev/null
  echo "promote-$1"
}
verdict() { # OP GATE FIELD — one field of one verdict, as JSON
  kubectl -n "$H" get operation "$1" -o json | jq -c --arg g "$2" ".status.verdicts[] | select(.gate == \$g) | $3"
}
rollbacks_of() { # OP — how many rollbacks undo OP
  local uid; uid="$(kubectl -n "$H" get operation "$1" -o jsonpath='{.metadata.uid}')"
  ops "$H" | jq --arg u "$uid" '[.items[] | select(.spec.type == "pacto.dev/rollback" and .spec.params.undoes == $u)] | length'
}

echo "== H1 ships: healthy spans, the contract check and the claim check all pass =="
OP1="$(ship h1 "$H1")"
wait_concluded "$H" "$OP1" Succeeded/Verified
for g in pacto.contract health claims; do
  [ "$(verdict "$OP1" "$g" .result)" = '"Pass"' ] || fail "$g did not pass for H1: $(verdict "$OP1" "$g" .)"
done
[ "$(verdict "$OP1" claims '.citation["payments.kernelCoverage"]')" = '"Complete"' ] || fail "the claim gate passed without complete kernel coverage"
pass "H1 verified by the contract, Mira and the node agent"

echo "== H2: its error ratio exceeds the limit, so it is rolled back, citing the Mira query =="
OP2="$(ship h2 "$H2")"
wait_concluded "$H" "$OP2" Failed/
{ [ "$(field "$H" "$OP2" reason)" = VerifyFailed ] && grep -q "^health: " <<<"$(field "$H" "$OP2" message)"; } \
  || fail "H2 failed for the wrong reason: $(field "$H" "$OP2" reason): $(field "$H" "$OP2" message)"
RB2="${OP2}-rollback"
Q2="$(kubectl -n "$H" get operation "$RB2" -o json | jq -r '.spec.cause.verdict.citation.query')"
jq -e --arg from "$(kubectl -n "$H" get operation "$OP2" -o jsonpath='{.status.times.applied}')" \
  '.signal == "traces" and (.from | type) == "number" and (.to | type) == "number" and .to > .from
   and (.where | index({"attr": "service.name", "eq": "payments"}))' <<<"$Q2" >/dev/null \
  || fail "the rollback does not cite the Mira query with absolute bounds: $Q2"
# The citation re-runs: the same documents against Mira read the same window.
# Spans Mira had not flushed yet when the gate read can only add to it, so the
# re-run finds at least what was cited and the ratio is still over the limit.
CITE="$(kubectl -n "$H" get operation "$RB2" -o json | jq -c '.spec.cause.verdict.citation')"
SPANS="$(jq -r .spans <<<"$CITE")"
count() { curl -s -X POST "http://127.0.0.1:${MIRA_PORT}/api/v1/query" -d "$1" | jq '.rows | length'; }
RERUN="$(count "$Q2")"
RERUN_ERRORS="$(count "$(jq -r .errorQuery <<<"$CITE")")"
{ [ "$RERUN" -ge "$SPANS" ] && [ $((RERUN_ERRORS * 5)) -gt "$RERUN" ]; } \
  || fail "re-running the cited queries counted $RERUN_ERRORS of $RERUN spans failed; the verdict counted $SPANS spans"
wait_concluded "$H" "$RB2" Succeeded/Verified
has_pointer "$H" "$H1" || fail "health is not back on H1"
pass "H2 rolled back to H1; the rollback cites the Mira query, and re-running it finds $RERUN_ERRORS of $RERUN spans failed"

echo "== Q: too little telemetry holds as Unknown, never Pass and never a rollback =="
OPQ="$(ship q "$Q")"
verifying_unknown() {
  [ "$(field "$H" "$OPQ" phase)" = Verifying ] && [ "$(verdict "$OPQ" health .result)" = '"Unknown"' ]
}
eventually 60 verifying_unknown || fail "Q did not wait as Unknown: $(field "$H" "$OPQ" phase) $(field "$H" "$OPQ" message)"
verdict "$OPQ" health .message | grep -q "spans in the window; at least 20" || fail "Q's Unknown does not say there were too few spans"
eventually 60 held "$H" "$OPQ" || fail "Q was not held for a person at the window's end: $(field "$H" "$OPQ" phase)"
[ "$(field "$H" "$OPQ" reason)" = VerifyUnknown ] || fail "Q held for the wrong reason: $(field "$H" "$OPQ" reason)"
{ [ "$(rollbacks_of "$OPQ")" = 0 ] && has_pointer "$H" "$Q"; } || fail "Unknown rolled Q back"
as_person alice team-payments approve -n "$H" "$OPQ" --decision reject --note "no telemetry, no trust" >/dev/null
wait_concluded "$H" "$OPQ" Failed/
wait_concluded "$H" "${OPQ}-rollback" Succeeded/Verified
pass "Q held as Unknown with no rollback until alice rejected it"

echo "== H5 ships, then regresses after it was verified: exactly one rollback =="
OP5="$(ship h5 "$H5")"
wait_concluded "$H" "$OP5" Succeeded/Verified
mode h5 errors
recheck_failed() { kubectl -n "$H" get stage payments -o json | jq -e --arg d "$H5" '.status.failedDigests | index($d)' >/dev/null; }
eventually 40 recheck_failed || fail "the regression after release was never caught: $(kubectl -n "$H" get stage payments -o jsonpath='{.status.lastCheck}')"
RB5="${OP5}-rollback"
kubectl -n "$H" get operation "$RB5" -o json | jq -e '.spec.reason | test("RecheckFailed: health")' >/dev/null \
  || fail "the rollback does not say a re-check failed"
wait_concluded "$H" "$RB5" Succeeded/Verified
one_rollback() { [ "$(rollbacks_of "$OP5")" = 1 ] && has_pointer "$H" "$H1"; }
always 7 one_rollback || fail "a regression after release raised $(rollbacks_of "$OP5") rollbacks"
pass "H5 regressed after release and was rolled back exactly once"

echo "== R: the node agent sees it call fraud, which its contract never declares: the claim gate fails =="
# The claim gate fails R while it verifies, or on the first re-check if the
# node agent saw the first call only after the soak; either way the version is
# marked failed and its one rollback cites the claim verdict. The gates run in
# order (contract, health, claims) and the first Fail is the cause, so a cause
# of claims also says Pacto's own check passed: an undeclared call is only a
# warning there.
OPR="$(ship r "$R")"
r_failed() { kubectl -n "$H" get stage payments -o json | jq -e --arg d "$R" '.status.failedDigests | index($d)' >/dev/null; }
eventually 80 r_failed || fail "R was never failed: $(field "$H" "$OPR" phase) $(field "$H" "$OPR" message)"
CAUSE="$(kubectl -n "$H" get operation "${OPR}-rollback" -o json | jq -c '.spec.cause.verdict')"
{ [ "$(jq -r .gate <<<"$CAUSE")" = claims ] && grep -q "DEPENDENCY_UNDECLARED dependency/fraud (ebpf" <<<"$(jq -r '.citation["payments.findings"]' <<<"$CAUSE")"; } \
  || fail "R's rollback does not cite the claim verdict and its kernel evidence: $CAUSE"
wait_concluded "$H" "${OPR}-rollback" Succeeded/Verified
pass "R failed the claim gate on kernel evidence and was rolled back"

echo "== explain and history read it back =="
OUT="$(KUBECONFIG="$AGENT_A" "$PACTO" delivery explain -n "$H" "$RB2")"
grep -q "re-run: POST query" <<<"$OUT" || fail "explain does not say how to re-run the Mira citation: $OUT"
HIST="$(KUBECONFIG="$AGENT_A" "$PACTO" delivery history -n "$H" --stage payments --output-format json)"
jq -e '.completeness == "partial" and ([.entries[] | select(.type == "pacto.dev/rollback")] | length) == 4' <<<"$HIST" >/dev/null \
  || fail "history does not list the four rollbacks: $HIST"
pass "explain cites how to re-run, history lists every rollback"

kill "$GEN_PID" "$MIRA_PF_PID" 2>/dev/null || true
echo "DELIVERY HEALTH PASS"
fi

# --- phase 3: publishing -------------------------------------------------------
# billing is published by the real `pacto publish` from a contract that declares
# what it publishes: its bundle and one manifests artifact, the deploy artifact a
# Flux Kustomization applies, built by a command and signed with the run's key.
# It proves the vision's step-3 list against the registry and the cluster:
#   - --plan lists exactly what is missing and writes nothing;
#   - publishing writes the version signed, the deploy artifact naming its
#     digest-pinned contract, and the same publish again writes nothing;
#   - a breaking change published as a minor is refused and writes nothing;
#   - delivery promotes the published version with no extra step;
#   - publish --propose waits for Flux to verify the new version, then
#     proposes it on a Stage that does not promote by itself;
#   - an artifact pushed another way, unsigned though it names a valid
#     contract, is never a candidate and a promote of it is refused.
# `pacto publish` runs on the host, where the in-cluster registry name does not
# resolve. HTTP_PROXY points plain-HTTP registry traffic at the port-forward, so
# the contract's refs are the in-cluster names Flux and the promoter read.
echo "== phase 3: pacto publish, and a Stage that promotes only what it published =="
P=publish
kubectl create namespace "$P" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n "$P" create secret generic cosign-pub --from-file=cosign.pub="$KEYDIR/cosign.pub" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
PUB="$(mktemp -d)"
cp "$KEYDIR/cosign.key" "$PUB/cosign.key"
cat > "$PUB/render.sh" <<'SH'
#!/bin/sh
# The manifests build: a Deployment and its Pacto, for the version publish passes.
set -e
cat > "$PACTO_OUT/kustomization.yaml" <<YAML
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources: [deployment.yaml, pacto.yaml]
YAML
cat > "$PACTO_OUT/deployment.yaml" <<YAML
apiVersion: apps/v1
kind: Deployment
metadata: {name: billing}
spec:
  replicas: 1
  selector: {matchLabels: {app: billing}}
  template:
    metadata: {labels: {app: billing}, annotations: {version: "$PACTO_VERSION"}}
    spec: {containers: [{name: app, image: registry.k8s.io/pause:3.9}]}
YAML
cat > "$PACTO_OUT/pacto.yaml" <<YAML
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: {name: billing}
spec:
  checkIntervalSeconds: 30
  contractRef: {oci: "oci://$BILLING_BUNDLE:$PACTO_VERSION"}
  target: {workloadRef: {name: billing, kind: Deployment}}
YAML
SH
chmod +x "$PUB/render.sh"
billing_contract() { # VERSION WORKLOAD
  cat > "$PUB/pacto.yaml" <<YAML
pactoVersion: "2.1"
service:
  name: billing
  version: $1
  owner: {team: team-billing, dri: d, contacts: [{type: email, value: a@e.com, purpose: escalation}]}
workload: $2
state: {type: stateless, persistence: {scope: local, durability: ephemeral}, dataCriticality: low}
publish:
  bundle: oci://${REG_HOST}/demo/billing
  deploy: manifests
  artifacts:
    - name: manifests
      type: manifests
      ref: oci://${REG_HOST}/apps/billing
      build: {command: [./render.sh]}
  sign: {key: cosign.key, transparencyLog: false}
YAML
}
billing_contract 1.0.0 service
printf 'cosign.key\nrender.sh\n' > "$PUB/.pactoignore"
git -C "$PUB" init -q && git -C "$PUB" add -A && git -C "$PUB" -c user.email=ci@e.com -c user.name=ci commit -qm billing
pub() {
  (cd "$PUB" && HTTP_PROXY="http://127.0.0.1:${LOCAL_REG_PORT}" PACTO_INSECURE_REGISTRIES="$REG_HOST" COSIGN_PASSWORD="" \
    BILLING_BUNDLE="${REG_HOST}/demo/billing" "$PACTO" publish "$@")
}
tags() { oras repo tags --plain-http "127.0.0.1:${LOCAL_REG_PORT}/$1" 2>/dev/null | sort | tr '\n' ' ' || true; }
referrers() { oras discover --plain-http --format json "127.0.0.1:${LOCAL_REG_PORT}/$1" 2>/dev/null | jq '[.. | .digest? // empty] | length'; }

kubectl -n "$P" apply -f - >/dev/null <<YAML
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata: {name: billing-candidates}
spec: {interval: 5s, insecure: true, url: "oci://${REG_HOST}/apps/billing", ref: {semver: ">=0.0.0"}, $VERIFY}
---
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata:
  name: billing
  labels: {delivery.pacto.trianalab.io/apply-ref: billing}
spec: {interval: 30s, insecure: true, url: "oci://${REG_HOST}/apps/billing", ref: {}, $VERIFY}
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata: {name: billing}
spec:
  interval: 1m
  sourceRef: {kind: OCIRepository, name: billing}
  path: ./
  prune: true
  timeout: 2m
  targetNamespace: $P
  healthChecks: [{apiVersion: apps/v1, kind: Deployment, name: billing, namespace: $P}]
---
apiVersion: delivery.pacto.trianalab.io/v1alpha1
kind: Stage
metadata: {name: billing}
spec:
  service: {name: billing}
  environment: publish
  applyRef: {name: billing}
  candidates: {ociRepositoryRef: {name: billing-candidates}}
  auto: true
  gates: {verify: {window: 3m}}
YAML

echo "== --plan lists what is missing and writes nothing =="
OUT="$(pub --plan)" || fail "the plan of a publishable version failed: $OUT"
{ grep -q "manifests  manifests  absent" <<<"$OUT" && grep -q "bundle     bundle     absent" <<<"$OUT" \
  && grep -q "2 to publish; nothing was changed" <<<"$OUT"; } || fail "the plan does not list the two missing artifacts: $OUT"
[ -z "$(tags apps/billing)$(tags demo/billing)" ] || fail "--plan wrote to the registry: $(tags apps/billing) $(tags demo/billing)"
pass "--plan lists the manifests and the bundle as absent and writes nothing"

echo "== publish: signed, the deploy artifact naming its contract =="
OUT="$(pub)" || fail "publish failed: $OUT"
D1="$(grep -oE "^Deploy: ${REG_HOST}/apps/billing@sha256:[0-9a-f]{64}" <<<"$OUT" | sed 's/.*@//')"
[ -n "$D1" ] || fail "publish named no deploy artifact: $OUT"
CONTRACT="$(oras manifest fetch --plain-http "127.0.0.1:${LOCAL_REG_PORT}/apps/billing@$D1" | jq -r '.annotations["pacto.dev/contract"]')"
[[ "$CONTRACT" == "oci://${REG_HOST}/demo/billing@sha256:"* ]] || fail "the deploy artifact names no digest-pinned contract: $CONTRACT"
for ref in "apps/billing@$D1" "demo/billing@${CONTRACT##*@}"; do
  cosign verify --key "$KEYDIR/cosign.pub" --insecure-ignore-tlog --allow-http-registry "127.0.0.1:${LOCAL_REG_PORT}/$ref" >/dev/null 2>&1 \
    || fail "$ref is not signed with the publisher's key"
done
pass "publish wrote billing 1.0.0: $D1, signed, naming ${CONTRACT##*/}"

echo "== the same publish again writes nothing =="
BEFORE="$(tags apps/billing)|$(tags demo/billing)|$(referrers "apps/billing@$D1")"
OUT="$(pub)" || fail "the second publish failed: $OUT"
grep -q "Nothing to publish" <<<"$OUT" || fail "the second publish published something: $OUT"
AFTER="$(tags apps/billing)|$(tags demo/billing)|$(referrers "apps/billing@$D1")"
[ "$BEFORE" = "$AFTER" ] || fail "the second publish changed the registry: $BEFORE -> $AFTER"
pass "a second publish wrote no tag, artifact or signature ($AFTER)"

echo "== a breaking change published as a minor is refused =="
billing_contract 1.1.0 job
if OUT="$(pub --plan 2>&1)"; then fail "the plan of a breaking minor passed: $OUT"; fi
grep -q "needs a major bump over 1.0.0: publish 2.0.0 or higher" <<<"$OUT" || fail "the plan does not say a major is needed: $OUT"
if OUT="$(pub 2>&1)"; then fail "a breaking change was published as a minor: $OUT"; fi
[ "$(tags apps/billing)|$(tags demo/billing)|$(referrers "apps/billing@$D1")" = "$AFTER" ] \
  || fail "the refused publish wrote to the registry: $(tags apps/billing) $(tags demo/billing)"
billing_contract 1.0.0 service
pass "BREAKING since 1.0.0 is refused as 1.1.0, and nothing was written"

echo "== delivery promotes the published version with no extra step =="
OPP="$(wait_op "$P" "$D1")"
wait_concluded "$P" "$OPP" Succeeded/Verified
[ "$(kubectl -n "$P" get stage billing -o jsonpath='{.status.pointer}')" = "$D1" ] || fail "the Stage does not run the published version"
[ "$(kubectl -n "$P" get deploy billing -o jsonpath='{.spec.template.metadata.annotations.version}')" = 1.0.0 ] \
  || fail "the cluster does not run billing 1.0.0"
published_in "$P" "$D1" billing || fail "the Stage does not remember $D1 as published"
[ "$(field "$P" "$OPP" actorClass)" = controller ] || fail "the promote was not raised by the promoter"
pass "the promoter promoted $D1 by itself and the contract check verified it"

echo "== publish --propose waits for Flux, then proposes on a Stage that does not promote by itself =="
M=publish-manual
kubectl create namespace "$M" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n "$M" create secret generic cosign-pub --from-file=cosign.pub="$KEYDIR/cosign.pub" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n "$M" apply -f - >/dev/null <<YAML
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata: {name: billing-candidates}
spec: {interval: 5s, insecure: true, url: "oci://${REG_HOST}/apps/billing", ref: {semver: ">=0.0.0"}, $VERIFY}
---
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata:
  name: billing
  labels: {delivery.pacto.trianalab.io/apply-ref: billing}
spec: {interval: 30s, insecure: true, url: "oci://${REG_HOST}/apps/billing", ref: {}, $VERIFY}
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata: {name: billing}
spec:
  interval: 1m
  sourceRef: {kind: OCIRepository, name: billing}
  path: ./
  prune: true
  timeout: 2m
  targetNamespace: $M
  healthChecks: [{apiVersion: apps/v1, kind: Deployment, name: billing, namespace: $M}]
---
apiVersion: delivery.pacto.trianalab.io/v1alpha1
kind: Stage
metadata: {name: billing}
spec:
  service: {name: billing}
  environment: publish-manual
  applyRef: {name: billing}
  candidates: {ociRepositoryRef: {name: billing-candidates}}
  gates: {verify: {window: 3m}}
YAML
billing_contract 1.0.1 service
git -C "$PUB" -c user.email=ci@e.com -c user.name=ci commit -qam "billing 1.0.1"
OUT="$(KUBECONFIG="$AGENT_A" pub --propose "$M/billing" 2>&1)" || fail "publish --propose failed: $OUT"
D2="$(grep -oE "^Deploy: ${REG_HOST}/apps/billing@sha256:[0-9a-f]{64}" <<<"$OUT" | sed 's/.*@//')"
OPM="$(grep -oE "on $M/billing as Operation [a-z0-9.-]+" <<<"$OUT" | awk '{print $NF}')"
{ [ -n "$D2" ] && [ -n "$OPM" ]; } || fail "publish --propose named no deploy artifact or Operation: $OUT"
published_in "$M" "$D2" billing || fail "--propose did not wait for the Stage to record $D2 as published"
wait_concluded "$M" "$OPM" Succeeded/Verified
[ "$(kubectl -n "$M" get deploy billing -o jsonpath='{.spec.template.metadata.annotations.version}')" = 1.0.1 ] \
  || fail "the manual Stage does not run billing 1.0.1"
pass "publish --propose waited for Flux to verify $D2, and the promote it raised shipped billing 1.0.1"
OPP2="$(wait_op "$P" "$D2")"
wait_concluded "$P" "$OPP2" Succeeded/Verified

echo "== an artifact pushed another way is never a candidate =="
FOREIGN_TREE="$(mktemp -d)"; FOREIGN_TGZ="$(mktemp -t foreign.XXXXXX)"
(PACTO_OUT="$FOREIGN_TREE" PACTO_VERSION=9.9.9 BILLING_BUNDLE="${REG_HOST}/demo/billing" "$PUB/render.sh")
tar -czf "$FOREIGN_TGZ" -C "$FOREIGN_TREE" .
FOREIGN="$(oras push --plain-http --disable-path-validation --annotation "pacto.dev/contract=$CONTRACT" --format go-template='{{.digest}}' \
  "127.0.0.1:${LOCAL_REG_PORT}/apps/billing:9.9.9" "${FOREIGN_TGZ}:${FLUX_LAYER_TYPE}")"
unverified() {
  kubectl -n "$P" get ocirepository billing-candidates -o json \
    | jq -e '.status.conditions // [] | map(select(.type == "SourceVerified" and .status == "False")) | length > 0' >/dev/null
}
eventually 40 unverified || fail "Flux never refused the unsigned 9.9.9: $(kubectl -n "$P" get ocirepository billing-candidates -o jsonpath='{.status.conditions}')"
never_candidate() {
  [ -z "$(op_to "$P" "$FOREIGN")" ] && ! published_in "$P" "$FOREIGN" billing \
    && [ "$(kubectl -n "$P" get stage billing -o jsonpath='{.status.pointer}')" = "$D2" ]
}
always 7 never_candidate || fail "the unsigned artifact became a candidate"
as_agent "$AGENT_A" propose -n "$P" --stage billing --to "$FOREIGN" --name foreign --reason "pushed by hand" >/dev/null
wait_concluded "$P" foreign Rejected/
[ "$(field "$P" foreign reason)" = NotPublished ] || fail "the foreign promote was refused for the wrong reason: $(field "$P" foreign reason): $(field "$P" foreign message)"
pass "the unsigned 9.9.9 ($FOREIGN) is never offered, and a promote of it is refused as NotPublished"

echo "== a Stage whose candidate sets no spec.verify is not Ready =="
# ready_is NS STAGE STATUS/REASON — the Stage's Ready condition.
ready_is() {
  [ "$(kubectl -n "$1" get stage "$2" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}/{.status.conditions[?(@.type=="Ready")].reason}')" = "$3" ]
}
eventually 20 ready_is "$P" billing True/Ready || fail "the publishing Stage is not Ready: $(kubectl -n "$P" get stage billing -o jsonpath='{.status.conditions}')"
kubectl -n "$M" apply -f - >/dev/null <<YAML
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata: {name: billing-anyone}
spec: {interval: 5s, insecure: true, url: "oci://${REG_HOST}/apps/billing", ref: {semver: ">=0.0.0"}}
---
apiVersion: delivery.pacto.trianalab.io/v1alpha1
kind: Stage
metadata: {name: billing-anyone}
spec:
  service: {name: billing}
  environment: publish-anyone
  applyRef: {name: billing-anyone-apply}
  candidates: {ociRepositoryRef: {name: billing-anyone}}
  auto: true
YAML
eventually 20 ready_is "$M" billing-anyone False/CandidateUnverified \
  || fail "a Stage whose candidate sets no spec.verify is not reported: $(kubectl -n "$M" get stage billing-anyone -o jsonpath='{.status.conditions}')"
[ -z "$(kubectl -n "$M" get stage billing-anyone -o jsonpath='{.status.published}')" ] || fail "an unverified candidate was recorded as published"
pass "a Stage whose candidate OCIRepository sets no spec.verify is Ready=False with CandidateUnverified and records nothing"
echo "DELIVERY PUBLISH PASS"

kill "$REG_PF_PID" 2>/dev/null || true
echo "DELIVERY PASS"
keep_or_teardown "$NS" "$CLUSTER" delete_cluster
