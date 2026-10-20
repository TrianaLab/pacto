#!/usr/bin/env bash
# Kind acceptance for Pacto delivery, phase 1 ("the core"): the promoter, the
# Pacto contract check and Flux, running end to end on a local cluster.
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
trap 'rc=$?; [ $rc -ne 0 ] && { dump_diag "$NS"; dump_delivery; }; pkill -f "kubectl.*port-forward" 2>/dev/null || true; exit $rc' EXIT

command -v oras >/dev/null 2>&1 || fail "oras is required: it publishes the manifest artifacts Flux pulls"
command -v jq >/dev/null 2>&1 || fail "jq is required to read operations"

VER="$(release_version kubernetes)"
OP_REPO="localhost:5001/pacto-operator/pacto-controller"
OP_IMG="${OP_REPO}:${VER}"
DL_REPO="localhost:5001/pacto-delivery"
DL_IMG="${DL_REPO}:${VER}"

echo "== build the operator and the pacto-delivery image (one Dockerfile, two entrypoints) =="
docker build --load -f "$_PACTO_ROOT/integrations/kubernetes/Dockerfile" --build-arg VERSION="$VER" -t "$OP_IMG" "$_PACTO_ROOT"
docker build --load -f "$_PACTO_ROOT/integrations/kubernetes/Dockerfile" --build-arg VERSION="$VER" \
  --build-arg CMD=cmd/delivery -t "$DL_IMG" "$_PACTO_ROOT"
PACTO="$(build_pacto)"
OP_CHART="$(package_chart "$PACTO_CHART")"
DL_CHART="$(package_chart "$_PACTO_ROOT/integrations/kubernetes/charts/pacto-delivery")"

ensure_cluster
load_images "$OP_IMG" "$DL_IMG"
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
as_person() { local u="$1"; shift; KUBECONFIG="$(person_kubeconfig "$u" humans)" "$PACTO" delivery "$@"; }

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
# artifact Flux pulls, annotated with its contract and tagged with its label so
# the registry keeps it. Prints the digest.
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
  echo "$digest"
}
# candidate LABEL — what CI does when a version is ready: move the candidate tag.
candidate() { oras tag --plain-http "127.0.0.1:${LOCAL_REG_PORT}/${APP_REPO}:$1" candidate >/dev/null; }

A="$(publish A "$C100")"; candidate A
echo "  A = $A (payments 1.0.0)"

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
reconciled() { [ -n "$(kubectl -n "$1" get pacto "$2" -o jsonpath='{.status.contract.serviceName}' 2>/dev/null)" ]; }
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
spec: {interval: 30s, insecure: true, url: "oci://${REG_HOST}/${APP_REPO}", ref: {digest: "$A"}}
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
spec: {interval: 10s, insecure: true, url: "oci://${REG_HOST}/${APP_REPO}", ref: {tag: candidate}}
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

# --- reading the promoter's state -------------------------------------------
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

echo "== item 5: an artifact naming no contract waits for a person instead of shipping =="
E="$(publish E "")"; candidate E
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
as_person alice approve -n test "$OPF" --decision approve --note "checkout migrates next sprint" >/dev/null
wait_concluded test "$OPF" Succeeded/Verified
[ "$(kubectl -n test get operation "$OPF" -o jsonpath='{.status.approvals[0].by}')" = alice ] || fail "the approval is not recorded"
pass "item 4: F held for team-checkout only, approved by alice, verified in test"
OPFP="$(wait_op prod "$F")"
eventually 30 held prod "$OPFP" || fail "F was not held in prod"
as_person carol approve -n prod "$OPFP" --decision approve >/dev/null
wait_concluded prod "$OPFP" Succeeded/Verified

echo "== item 7: a person's rollback in prod is not undone by automation =="
as_person alice propose -n prod --stage payments --type rollback --undoes "$OPFP" --name human-undo --reason "checkout errors" >/dev/null
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

kill "$REG_PF_PID" 2>/dev/null || true
echo "DELIVERY CORE PASS"
keep_or_teardown "$NS" "$CLUSTER" delete_cluster
