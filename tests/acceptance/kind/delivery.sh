#!/usr/bin/env bash
# Kind acceptance for Pacto delivery: phase 1 ("the core"), the promoter, the
# Pacto contract check and Flux, running end to end on a local cluster; then
# phase 2 ("health"), phase 3 ("publishing"), phase 5 ("ordering"), phase 4
# ("dashboard and identity", in delivery-identity.sh) and phase 6 ("records"),
# described where each starts below. DELIVERY_SKIP_CORE=1,
# DELIVERY_SKIP_HEALTH=1, DELIVERY_SKIP_PUBLISH=1, DELIVERY_SKIP_ORDER=1,
# DELIVERY_SKIP_IDENTITY=1 and DELIVERY_SKIP_RECORDS=1 skip those scenarios (the
# setup still runs); phase 4 needs phase 1, and phase 6 needs phases 1 and 3.
#
# DELIVERY_PHASES=core,health,publish,ordering,identity,records (any subset)
# runs only the named phases plus the ones they need, by setting the skip switch
# of every other phase; CI runs one phase per shard. DELIVERY_DRY_RUN=1 prints
# which phases would run and stops before touching Docker or a cluster.
#
# The setup runs once; then the phases that do not depend on each other run at
# the same time in their own namespaces of the one cluster, each line prefixed
# with its phase (see run_phases). DELIVERY_SEQUENTIAL=1 runs them one at a
# time, for debugging. Every section and wait is timed, and a summary of where
# the time went prints at the end (KIND_TIMING=FILE keeps the raw lines).
# A laptop that sleeps mid-run (a closed lid sleeps even under `caffeinate -i`)
# drops every port-forward and lets verify windows pass with no telemetry, so a
# phase stalls on a Held operation: check `pmset -g log` before blaming the
# concurrency.
#
# DELIVERY_SAVE_IMAGES=FILE builds the three Pacto images, saves them to FILE
# and stops; DELIVERY_LOAD_IMAGES=FILE loads them from FILE instead of building.
# CI builds once and every shard loads the same archive.
#
# The recorder runs for the whole run, so every operation of every phase ends
# in a signed record in the registry; phase 6 checks them.
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
# Every section and every wait is timed; the summary prints when the run ends.
KIND_TIMING="${KIND_TIMING:-$(mktemp)}"
export KIND_TIMING
# Waits poll every second. Each still makes at least as many tries, and sleeps
# at least as long between them, as it did at 3s (see lib.sh's eventually).
# shellcheck disable=SC2034  # read by lib.sh's eventually
KIND_POLL=1
# The candidate OCIRepositories Flux scans for a moved tag or a new version.
# Every version a phase ships waits on this scan, so the run keeps it short.
CANDIDATE_INTERVAL=2s

PHASES="core health publish ordering identity records"
# skip_var PHASE — the name of the phase's skip switch.
skip_var() { echo "DELIVERY_SKIP_$(tr '[:lower:]' '[:upper:]' <<<"${1/ordering/order}")"; }
if [ -n "${DELIVERY_PHASES:-}" ]; then
  want=",${DELIVERY_PHASES},"
  for p in ${DELIVERY_PHASES//,/ }; do
    [[ " $PHASES " == *" $p "* ]] || fail "unknown phase '$p' in DELIVERY_PHASES (phases: $PHASES)"
  done
  # What each phase needs: identity ships core's F again; records reads core's
  # and publish's operations.
  [[ "$want" == *,identity,* || "$want" == *,records,* ]] && want+="core,"
  [[ "$want" == *,records,* ]] && want+="publish,"
  for p in $PHASES; do
    sw="$(skip_var "$p")"
    if [[ "$want" == *,"$p",* ]]; then unset "$sw"; else printf -v "$sw" 1; fi
  done
fi
# shellcheck disable=SC2034  # the skip switches are read through skip_var
[ -n "${DELIVERY_SKIP_CORE:-}" ] && DELIVERY_SKIP_IDENTITY=1 && DELIVERY_SKIP_RECORDS=1
# shellcheck disable=SC2034
[ -n "${DELIVERY_SKIP_PUBLISH:-}" ] && DELIVERY_SKIP_RECORDS=1
if [ -n "${DELIVERY_DRY_RUN:-}" ]; then
  for p in $PHASES; do
    sw="$(skip_var "$p")"
    [ -n "${!sw:-}" ] && echo "skip $p" || echo "run  $p"
  done
  [ -n "${DELIVERY_SEQUENTIAL:-}" ] && echo "one phase at a time" || echo "independent phases at once"
  exit 0
fi
# Every HTTP call here and in delivery-identity.sh is bounded: a server that
# never answers is a FAIL, not a run that hangs.
curl() { command curl --connect-timeout 5 --max-time 30 "$@"; }
# shellcheck source=tests/acceptance/kind/delivery-identity.sh
source "$(dirname "$0")/delivery-identity.sh"

dump_delivery() {
  echo "--- stages + operations + approvals ---"
  kubectl get stages,operations,approvals -A -o yaml || true
  echo "--- flux ---"
  kubectl get ocirepositories,kustomizations -A -o wide || true
  echo "--- logs deploy/pacto-delivery ---"
  kubectl -n "$DELIVERY_NS" logs deploy/pacto-delivery --tail=300 || true
  echo "--- logs deploy/pacto-delivery-recorder ---"
  kubectl -n "$DELIVERY_NS" logs deploy/pacto-delivery-recorder --tail=200 || true
  for d in source-controller kustomize-controller; do
    echo "--- logs deploy/$d (flux-system) ---"
    kubectl -n flux-system logs "deploy/$d" --tail=100 || true
  done
}
# --- running the phases --------------------------------------------------------
# Every phase runs in a subshell of its own. By default the phases that do not
# depend on each other run at the same time, each in its own namespaces of the
# one cluster; DELIVERY_SEQUENTIAL=1 runs them one at a time, in PHASES order,
# straight to the terminal, for debugging. The dependencies:
#   - identity runs after core: it ships core's F again on prod;
#   - records runs after core and publish, whose operations it reads, and its
#     audit of every operation and Stage of the run after every phase;
#   - health, publish and ordering start once core has its operator back
#     (item 8 stops the operator, which every phase's contract check needs).
# What a later phase reads from an earlier one is handed over with `share`.
STATE="$(mktemp -d)"
share() { local v; for v in "$@"; do printf '%s=%q\n' "$v" "${!v}"; done >> "$STATE/shared.env"; }
signal() { touch "$STATE/signal-$1"; }
signalled() { [ -e "$STATE/signal-$1" ]; }
await() { eventually 200 signalled "$1" || fail "waited ten minutes for $1"; }
phase_runs() { local sw; sw="$(skip_var "$1")"; [ -z "${!sw:-}" ]; }
# phase_deps PHASE — the phases that must have finished before PHASE starts.
phase_deps() {
  case "$1" in
    identity) echo core ;;
    records) echo core publish ;;
  esac
}
# phase_body PHASE — the phase itself, in its subshell. PHASE_PIDS are the
# background helpers it started (a port-forward, a span generator); they stop
# with it. Its exit code is left in STATE.
phase_body() {
  TIMING_PHASE="$1" PHASE_PIDS=""
  trap 'rc=$?; for p in $PHASE_PIDS; do kill "$p" 2>/dev/null || true; done; timing_end; echo "$rc" > "$STATE/$TIMING_PHASE.rc"' EXIT
  "phase_$1"
}
# kill_tree PID — PID and everything it started.
kill_tree() { local c; for c in $(pgrep -P "$1" 2>/dev/null); do kill_tree "$c"; done; kill "$1" 2>/dev/null || true; }
RUNNING=""
# stop PID — stop a background job of this shell and reap it quietly.
stop() { kill_tree "$1"; wait "$1" 2>/dev/null || true; }
stop_phases() { local p v; for p in $RUNNING; do for v in PID TAIL; do v="${v}_$p"; [ -z "${!v:-}" ] || stop "${!v}"; done; done; RUNNING=""; }
run_phases() {
  local p d ready rc pending="" pid tail
  for p in $PHASES; do phase_runs "$p" && pending+=" $p"; done
  if [ -n "${DELIVERY_SEQUENTIAL:-}" ]; then
    for p in $pending; do
      ( phase_body "$p" )
      # shellcheck disable=SC1091
      [ ! -s "$STATE/shared.env" ] || source "$STATE/shared.env"
    done
    return
  fi
  while [ -n "${pending// /}${RUNNING// /}" ]; do
    for p in $pending; do
      ready=1
      for d in $(phase_deps "$p"); do [[ " $pending $RUNNING " != *" $d "* ]] || ready=; done
      [ -n "$ready" ] || continue
      ( phase_body "$p" ) > "$STATE/$p.log" 2>&1 &
      printf -v "PID_$p" %s $!
      # A FAIL line keeps FAIL: first ("  FAIL: [health] ...") so CI's
      # scripts/ci/kind-run.sh, which strips a leading FAIL:, still reads it.
      ( tail -n +1 -f "$STATE/$p.log" | awk -v p="[$p] " '{ if (match($0, /^ *FAIL: */)) print substr($0, 1, RLENGTH) p substr($0, RLENGTH + 1); else print p $0; fflush() }' ) 2>/dev/null &
      printf -v "TAIL_$p" %s $!
      pending=" ${pending// $p/}" RUNNING+=" $p"
    done
    sleep 2
    for p in $RUNNING; do
      pid="PID_$p" tail="TAIL_$p"
      ! kill -0 "${!pid}" 2>/dev/null || continue
      wait "${!pid}" 2>/dev/null || true
      sleep 1; stop "${!tail}"
      RUNNING=" ${RUNNING// $p/}"
      rc="$(cat "$STATE/$p.rc" 2>/dev/null || echo 1)"
      if [ "$rc" != 0 ]; then
        # No FAIL: here: kind-run.sh reports the first FAIL: line, which must be
        # the phase's own check, not this pointer to a log CI never uploads.
        echo "  phase $p failed (exit $rc); its full log is $STATE/$p.log" >&2
        exit "$rc"
      fi
      # shellcheck disable=SC1091
      [ ! -s "$STATE/shared.env" ] || source "$STATE/shared.env"
    done
  done
}
# shellcheck disable=SC2154  # rc is assigned by rc=$? inside the trap body
trap 'rc=$?; stop_phases; timing_end; timing_summary "$KIND_TIMING"; echo "run took ${SECONDS}s"; [ -z "${_CLUSTER_BG_PID:-}" ] || wait "$_CLUSTER_BG_PID" 2>/dev/null || true; [ $rc -ne 0 ] && { dump_diag "$NS"; dump_delivery; dump_identity; }; pkill -f "kubectl.*port-forward" 2>/dev/null || true; identity_cleanup; exit $rc' EXIT

if [ -z "${DELIVERY_SAVE_IMAGES:-}" ]; then
  command -v oras >/dev/null 2>&1 || fail "oras is required: it publishes the manifest artifacts Flux pulls"
  command -v cosign >/dev/null 2>&1 || fail "cosign is required: every version a Stage promotes is signed"
  command -v jq >/dev/null 2>&1 || fail "jq is required to read operations"
  command -v openssl >/dev/null 2>&1 || fail "openssl is required: it makes the recorder's key and forges records phase 6 must refuse"
fi

VER="$(release_version kubernetes)"
OP_REPO="localhost:5001/pacto-operator/pacto-controller"
OP_IMG="${OP_REPO}:${VER}"
DL_REPO="localhost:5001/pacto-delivery"
DL_IMG="${DL_REPO}:${VER}"

# The operator is coupled to the runtime image (the root Dockerfile): phase 2
# runs its node agent, which is `pacto agent` in that image.
DASH_IMG="localhost:5001/pacto-dashboard:$(release_version core)"

# Third-party images the run waits on, pinned and preloaded, so no rollout the
# run waits on pulls from the internet. Flux's install manifest is pinned by its
# checksum; only the two controllers the run waits on are preloaded from it. Its
# other controllers (helm, notification, image-reflector, image-automation,
# source-watcher) are still pulled by the nodes mid-run, and nothing waits on
# them. zot is pinned by digest too (lib.sh install_registry).
MIRA_IMG=ghcr.io/trianalab/mira:0.4.3@sha256:2fc95e0d4c8ffa38aa2de1a2389c98eb83e020ab7a1719ca27db90a62794fbcc
AGNHOST=registry.k8s.io/e2e-test-images/agnhost:2.53@sha256:99c6b4bb4a1e1df3f0b3752168c89358794d02258ebebc26bf21c29399011a85
PAUSE_IMG=registry.k8s.io/pause:3.9@sha256:7031c1b283388d2c2e09b57badb803c05ebed362dc88d84b480cc47f72a21097
FLUX_INSTALL_SHA256=cc3dcd743af16215838b6937e1fce83745bf24c0dcc6c59737c59df15429caaf
FLUX_IMAGES=(ghcr.io/fluxcd/source-controller:v1.9.5@sha256:6f20d232d596a758c923d2861f23511718fc303b8a2e36a1434a7c736b9f4268
  ghcr.io/fluxcd/kustomize-controller:v1.9.5@sha256:a3a955eb2bc432c2eaa94d2d3714e3beae7fdf17586fd23aadf71ab597ac3339)

# build_images — the three Pacto images at once, each build's output in its own
# log, printed when it fails.
build_images() {
  local logs; logs="$(mktemp -d)"
  build_operator_images "$OP_IMG" "$DASH_IMG" "$VER" > "$logs/operator.log" 2>&1 &
  local op=$!
  docker build --load -f "$_PACTO_ROOT/integrations/kubernetes/Dockerfile" --build-arg VERSION="$VER" \
    --target delivery --build-arg CMD=cmd/delivery -t "$DL_IMG" "$_PACTO_ROOT" > "$logs/delivery.log" 2>&1 &
  local dl=$!
  wait "$op" || { cat "$logs/operator.log" >&2; fail "the dashboard or operator image did not build"; }
  wait "$dl" || { cat "$logs/delivery.log" >&2; fail "the pacto-delivery image did not build"; }
}
if [ -n "${DELIVERY_SAVE_IMAGES:-}" ]; then
  timed "build the operator, dashboard and pacto-delivery images" build_images
  docker save -o "$DELIVERY_SAVE_IMAGES" "$OP_IMG" "$DL_IMG" "$DASH_IMG"
  echo "saved $OP_IMG $DL_IMG $DASH_IMG to $DELIVERY_SAVE_IMAGES"
  exit 0
fi

# The cluster trusts the run's identity providers (phase 4): its apiserver is
# created with their structured authentication and an audit log. It is created
# in the background, and the images build in the background, while the pulls
# below run.
section "create the cluster and build the images while the rest is pulled"
KIND_CONFIG="$(identity_prepare)"
cluster_start_bg "$KIND_CONFIG"
if [ -n "${DELIVERY_LOAD_IMAGES:-}" ]; then
  docker load -i "$DELIVERY_LOAD_IMAGES" >/dev/null
  for img in "$OP_IMG" "$DL_IMG" "$DASH_IMG"; do
    docker image inspect "$img" >/dev/null 2>&1 || fail "$DELIVERY_LOAD_IMAGES has no $img: it was built from another tree"
  done
else
  build_images &
  BUILD_PID=$!
fi
FLUX_INSTALL="$(mktemp)"
infra "download the Flux $FLUX_VERSION install manifest" \
  curl -fsSL --retry 2 -o "$FLUX_INSTALL" "https://github.com/fluxcd/flux2/releases/download/${FLUX_VERSION}/install.yaml"
[ "$(shasum -a 256 "$FLUX_INSTALL" | cut -d' ' -f1)" = "$FLUX_INSTALL_SHA256" ] \
  || fail "the Flux $FLUX_VERSION install manifest changed upstream: its checksum is not the pinned one"
for img in "${FLUX_IMAGES[@]}" "$PAUSE_IMG"; do pull_pinned "$img"; done
FLUX_IMAGES=("${FLUX_IMAGES[@]%@*}")
EXTRA_IMAGES=()
phase_runs health && for img in "$MIRA_IMG" "$AGNHOST"; do pull_pinned "$img"; EXTRA_IMAGES+=("${img%@*}"); done
MIRA_IMG="${MIRA_IMG%@*}" AGNHOST="${AGNHOST%@*}" PAUSE_IMG="${PAUSE_IMG%@*}"
phase_runs identity && for img in "$DEX_IMG" "$KC_IMG"; do infra "pull $img" docker pull -q "$img" >/dev/null; done
PACTO="$(build_pacto)"
OP_CHART="$(package_chart "$PACTO_CHART")"
DL_CHART="$(package_chart "$_PACTO_ROOT/integrations/kubernetes/charts/pacto-delivery")"
# Phase 4 proves the promoter ignores an approval on a login older than its
# login freshness (15 minutes by default), so one login has to get that old.
# Where the chart lets the run set the freshness, it is 3 minutes here: the
# same check, without a quarter of an hour of waiting.
# shellcheck disable=SC2034  # read by delivery-identity.sh
AUTH_FRESHNESS=900 DL_FRESH=()
if helm show values "$DL_CHART" | grep -q '^  authFreshness:'; then
  # shellcheck disable=SC2034
  AUTH_FRESHNESS=180 DL_FRESH=(--set promoter.authFreshness=3m)
fi
if [ -n "${BUILD_PID:-}" ]; then
  section "wait for the image builds"
  wait "$BUILD_PID" || exit 1
fi

timed "wait for the cluster" ensure_cluster "$KIND_CONFIG"
section "load the images into the cluster"
infra "load the images into the cluster" load_images "$OP_IMG" "$DL_IMG" "$DASH_IMG" "${FLUX_IMAGES[@]}" "$PAUSE_IMG" ${EXTRA_IMAGES[@]+"${EXTRA_IMAGES[@]}"}
for n in "$NS" "$DELIVERY_NS" test prod agents; do
  kubectl create namespace "$n" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
done
# DELIVERY_SKIP_IDENTITY=1 skips phase 4. It needs phase 1 (it ships F again).
# Its providers start now and are waited on once the rest is installed.
! phase_runs identity || timed "identity providers: Dex for people, Keycloak for agents over HTTP" identity_start

section "zot, the Pacto operator (observing only), Flux and the delivery chart"
install_registry
REG_PF_PID="$(pf "$LOCAL_REG_PORT" svc/pacto-registry 5000)"
# The operator-managed dashboard gets its sign-in from the chart (phase 4
# checks the operator passes it on); its ServiceAccount is the read-only one
# phase 4's own dashboard runs with.
kubectl -n "$NS" create secret generic dashboard-oidc --from-literal=client-secret="$DASH_SECRET" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null
KEYDIR="$(mktemp -d)"
# The recorder's signing key: an Ed25519 key in a Secret only the recorder mounts.
RECORDER_KEY="$KEYDIR/recorder.pem"
RECORDER_PUB="$KEYDIR/recorder.pub"
openssl genpkey -algorithm ed25519 -out "$RECORDER_KEY" 2>/dev/null || fail "openssl could not generate the recorder's key"
openssl pkey -in "$RECORDER_KEY" -pubout -out "$RECORDER_PUB"
kubectl -n "$DELIVERY_NS" create secret generic recorder-key --from-file=key.pem="$RECORDER_KEY" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
STREAMS_REPO="pacto-delivery-streams"

# Phase 2 needs the operator's node agent and the promoter's Mira provider.
# Both are installed with the charts, not by phase 2 itself: an upgrade
# restarts the operator and the promoter under the phases running beside it.
OP_HEALTH=() DL_HEALTH=()
if phase_runs health; then
  OP_HEALTH=(--set nodeAgent.enabled=true --set nodeAgent.publishInterval=5s)
  DL_HEALTH=(--set-json "gates.providers=[{\"name\":\"mira\",\"type\":\"mira\",\"url\":\"http://mira.${NS}.svc.cluster.local:4318\"}]")
fi
# The Pacto chart installs and rollouts below are the product under test, not
# infrastructure: they run once and a failure is a FAIL, never a retry. The two
# charts install at the same time, the delivery chart once Flux's kinds exist.
helm upgrade --install pacto-operator "$OP_CHART" -n "$NS" ${OP_HEALTH[@]+"${OP_HEALTH[@]}"} \
  --set image.repository="$OP_REPO" --set image.tag="$VER" --set image.pullPolicy=Never \
  --set dashboard.enabled=true --set dashboard.auth.oidc.issuerURL="$DEX_ISSUER" \
  --set dashboard.auth.oidc.clientID=pacto-dashboard --set dashboard.auth.oidc.redirectURL="$DASH_URL/auth/callback" \
  --set dashboard.auth.oidc.clientSecretRef.name=dashboard-oidc \
  --set "insecureRegistries[0]=$REG_HOST" --wait --timeout 240s >/dev/null || fail "the pacto-operator chart did not roll out" &
OP_HELM=$!
infra "install Flux" kubectl apply --server-side -f "$FLUX_INSTALL" >/dev/null
kubectl -n flux-system scale deploy helm-controller --replicas=0 >/dev/null 2>&1 || true
helm upgrade --install pacto-delivery "$DL_CHART" -n "$DELIVERY_NS" ${DL_HEALTH[@]+"${DL_HEALTH[@]}"} ${DL_FRESH[@]+"${DL_FRESH[@]}"} \
  --set image.repository="$DL_REPO" --set image.tag="$VER" --set image.pullPolicy=Never \
  --set "insecureRegistries[0]=$REG_HOST" --set promoter.poll=2s \
  --set recorder.enabled=true --set recorder.streamsRepository="$REG_HOST/$STREAMS_REPO" --set recorder.poll=5s \
  --set recorder.signing.ed25519.secretName=recorder-key --wait --timeout 240s >/dev/null || fail "the pacto-delivery chart did not roll out" &
DL_HELM=$!
infra "roll out Flux's source-controller" kubectl -n flux-system rollout status deploy/source-controller --timeout=240s
infra "roll out Flux's kustomize-controller" kubectl -n flux-system rollout status deploy/kustomize-controller --timeout=240s

if phase_runs health; then
  section "Mira, the OTLP store phase 2's health gate reads"
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
  infra "roll out Mira" kubectl -n "$NS" rollout status deploy/mira --timeout=120s >/dev/null
fi

section "wait for the Pacto operator and the delivery chart"
wait "$OP_HELM" || exit 1
wait "$DL_HELM" || exit 1
if kubectl auth can-i patch ocirepositories.source.toolkit.fluxcd.io -A \
  --as "system:serviceaccount:${NS}:pacto-operator" | grep -q yes; then
  fail "the observing operator can edit the pointer; delivery must add no permission to it"
fi
pass "delivery installed; the operator's service account still cannot change what runs"

if phase_runs identity; then
  timed "wait for Dex and Keycloak" identity_up
  identity_login_stale
fi

section "the signing key: a cosign key pair, its public half in a Secret Flux verifies with"
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

section "identities: two agents, two people, break-glass"
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

section "contracts"
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

# publish LABEL CONTRACT_REF [SERVICE] — one version of SERVICE (payments by
# default): a Deployment and its Pacto, as the artifact Flux pulls from
# apps/SERVICE, annotated with its contract, signed and tagged with its label so
# the registry keeps it. Prints the digest.
publish() {
  local label="$1" ref="$2" tag="$1" svc="${3:-payments}" tree tgz digest
  local repo="apps/$svc"
  tree="$(mktemp -d)"; tgz="$(mktemp -t "$svc.XXXXXX")"
  cat > "$tree/kustomization.yaml" <<'YAML'
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources: [deployment.yaml, pacto.yaml]
YAML
  cat > "$tree/deployment.yaml" <<YAML
apiVersion: apps/v1
kind: Deployment
metadata: {name: $svc}
spec:
  replicas: 1
  selector: {matchLabels: {app: $svc}}
  template:
    metadata: {labels: {app: $svc}, annotations: {version: "$label"}}
    spec:
      containers: [{name: app, image: registry.k8s.io/pause:3.9}]
YAML
  cat > "$tree/pacto.yaml" <<YAML
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: {name: $svc}
spec:
  checkIntervalSeconds: 30
  contractRef: {oci: "${ref:-$C100}"}
  target: {workloadRef: {name: $svc, kind: Deployment}}
YAML
  tar -czf "$tgz" -C "$tree" .
  local -a ann=()
  [ -n "$ref" ] && ann=(--annotation "pacto.dev/contract=$ref")
  digest="$(oras push --plain-http --disable-path-validation ${ann[@]+"${ann[@]}"} --format go-template='{{.digest}}' \
    "127.0.0.1:${LOCAL_REG_PORT}/${repo}:${tag}" "${tgz}:${FLUX_LAYER_TYPE}")"
  sign_digest "$repo" "$digest"
  echo "$digest"
}
# candidate LABEL [SERVICE] — what CI does when a version is ready: move the candidate tag.
candidate() { oras tag --plain-http "127.0.0.1:${LOCAL_REG_PORT}/apps/${2:-payments}:$1" candidate >/dev/null; }

A="$(publish A "$C100")"; candidate A
echo "  A = $A (payments 1.0.0)"

# apply_env NS [SERVICE DIGEST] — SERVICE's apply OCIRepository (pinned to
# DIGEST, A by default) and the Kustomization that applies it in NS.
apply_env() {
  local svc="${2:-payments}" digest="${3:-$A}"
  kubectl -n "$1" apply -f - >/dev/null <<YAML
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata:
  name: $svc
  labels: {delivery.pacto.trianalab.io/apply-ref: $svc}
spec: {interval: 30s, insecure: true, url: "oci://${REG_HOST}/apps/$svc", ref: {digest: "$digest"}, $VERIFY}
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata: {name: $svc}
spec:
  interval: 1m
  sourceRef: {kind: OCIRepository, name: $svc}
  path: ./
  prune: true
  timeout: 2m
  targetNamespace: $1
  # The Deployment only. Waiting on every object would make Flux wait for the
  # Pacto to observe its new generation, which hides the stale verdict item 8
  # is about: the promoter, not Flux, must refuse a result for an old version.
  healthChecks: [{apiVersion: apps/v1, kind: Deployment, name: $svc, namespace: $1}]
YAML
}

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

# Phase 1, the core. DELIVERY_SKIP_CORE=1 skips it; DELIVERY_PHASES=health runs the setup and phase 2 only.
phase_core() {
section "the consumers: checkout accepts ^1.0.0, ledger accepts >=1.0.0, in both environments"
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

section "Flux sources, Kustomizations and Stages"
apply_env test
apply_env prod
for env in test prod; do eventually 60 reconciled "$env" payments || fail "$env/payments was never reconciled"; done
kubectl -n test apply -f - >/dev/null <<YAML
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata: {name: payments-candidates}
spec: {interval: $CANDIDATE_INTERVAL, insecure: true, url: "oci://${REG_HOST}/${APP_REPO}", ref: {tag: candidate}, $VERIFY}
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
  gates: {impact: {declaredConsumersOnly: true}, verify: {window: 3m}}
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
  gates: {impact: {declaredConsumersOnly: true}, verify: {window: 3m}}
YAML

section "bootstrap: A reaches test, then prod, by itself"
OP="$(wait_op test "$A")"; wait_concluded test "$OP" Succeeded/Verified
OP="$(wait_op prod "$A")"; wait_concluded prod "$OP" Succeeded/Verified
pass "A verified in test and in prod"

section "items 1, 3 and 8: B (compatible) ships to production with no person, and a stale check never counts"
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
# A person, who may cancel anyone's operation before it moves (an agent may
# not cancel the promoter's own auto promote at all).
OUT="$(KUBECONFIG="$(person_kubeconfig alice humans)" kubectl -n test delete operation "$OPB" --wait=false 2>&1 || true)"
if grep -q "can no longer be cancelled" <<<"$OUT"; then
  pass "cancel is refused once the pointer moved"
else
  fail "deleting an applied operation was not refused: $OUT"
fi
kubectl -n "$NS" scale deploy pacto-operator --replicas=1 >/dev/null
wait_concluded test "$OPB" Succeeded/Verified
signal operator-back
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

section "item 2: C (broken) fails in test, is rolled back once, never reaches prod and is never retried"
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
# 18s: nine candidate scans and nine promoter polls (2s each).
always 6 never_retried || fail "C was retried in test or reached prod"
pass "item 2: C rolled back by one bound rollback, absent from prod, not retried"

section "item 5: a published artifact whose contract cannot be read waits for a person instead of shipping"
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
# E is the promoter's, so an agent may not cancel it: an agent that could
# would also cancel a rollback the promoter raised before it applies.
OUT="$(as_agent "$AGENT_A" cancel -n test "$OPE" 2>&1 || true)"
if grep -q "an agent may cancel only an operation it proposed itself" <<<"$OUT"; then
  pass "an agent cannot cancel an operation the promoter raised"
else
  fail "an agent cancelled the promoter's operation: $OUT"
fi
held test "$OPE" || fail "E is gone after the refused cancel: $(field test "$OPE" phase)"
as_person alice humans cancel -n test "$OPE" >/dev/null
# A cancelled operation that never moved anything concludes Cancelled and is
# let go once the recorder has recorded it.
cancelled() { ! kubectl -n test get operation "$OPE" >/dev/null 2>&1 || [ "$(outcome test "$OPE")" = Cancelled/ ]; }
eventually 20 cancelled || fail "E was not cancelled: $(field test "$OPE" phase) $(field test "$OPE" outcome)"
always 5 has_pointer test "$B" || fail "cancelling E moved the pointer"
pass "item 5: E held for accept-unknown, refused to the agent and cancelled by a person; nothing moved"

section "item 6: two agents propose at once; exactly one wins"
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

section "item 4: F (payments 2.0.0) breaks checkout; it waits, and only checkout's team is asked"
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
# A decision that is not approve, reject or accept-unknown is refused by the
# schema, never stored to be silently ignored.
OUT="$(kubectl -n test create -f - 2>&1 <<YAML || true
apiVersion: delivery.pacto.trianalab.io/v1alpha1
kind: Approval
metadata: {name: typo}
spec: {operation: $OPF, operationUID: x, intentDigest: x, decision: aprove, by: carol}
YAML
)"
grep -q 'Unsupported value: "aprove"' <<<"$OUT" || fail "an Approval with a misspelt decision was stored: $OUT"
pass "an Approval's decision must be approve, reject or accept-unknown"
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
# Every Approval of F is owned by F, so it is garbage collected with it, and
# none can be edited once given.
OPF_UID="$(kubectl -n test get operation "$OPF" -o jsonpath='{.metadata.uid}')"
unowned() { kubectl -n test get approvals -o json | jq --arg u "$OPF_UID" '[.items[] | select(.spec.operationUID == $u) | select(any(.metadata.ownerReferences[]?; .uid == $u) | not)] | length'; }
owned() { [ "$(unowned)" = 0 ]; }
eventually 20 owned || fail "$(unowned) Approvals of $OPF are not owned by it"
APF="$(kubectl -n test get approvals -o json | jq -r --arg u "$OPF_UID" '[.items[] | select(.spec.operationUID == $u)][0].metadata.name')"
OUT="$(kubectl -n test patch approval "$APF" --type merge -p '{"spec":{"decision":"reject"}}' 2>&1 || true)"
grep -q "an approval is immutable" <<<"$OUT" || fail "an Approval's decision was edited after it was given: $OUT"
pass "F's Approvals are owned by F and cannot be edited"
pass "item 4: F held for team-checkout only, answered only by a team-checkout member, verified in test"
OPFP="$(wait_op prod "$F")"
eventually 30 held prod "$OPFP" || fail "F was not held in prod"
as_person carol team-checkout approve -n prod "$OPFP" --decision approve >/dev/null
wait_concluded prod "$OPFP" Succeeded/Verified

section "item 7: a person's rollback in prod is not undone by automation"
as_person alice humans propose -n prod --stage payments --type rollback --undoes "$OPFP" --name human-undo --reason "checkout errors" >/dev/null
wait_concluded prod human-undo Succeeded/Verified
has_pointer prod "$WINNER" || fail "prod is not back on $WINNER"
[ "$(kubectl -n prod get stage payments -o jsonpath='{.status.autoHold}')" = true ] || fail "no auto-hold after a person's rollback"
held_back() { has_pointer prod "$WINNER" && [ "$(op_to prod "$F" | wc -l | tr -d ' ')" = 1 ]; }
always 6 held_back || fail "automation promoted F again after a person rolled it back"
pass "item 7: prod stays on the version alice chose; auto-promotion is on hold"

section "admission: the intent is immutable, and only break-glass edits the pointer"
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
# The promoter's RBAC reaches every OCIRepository; admission holds it to the
# pointer field of a labelled one.
OUT="$(kubectl --as "system:serviceaccount:${DELIVERY_NS}:pacto-delivery" -n prod patch ocirepository payments --type merge \
  -p '{"spec":{"interval":"7m"}}' 2>&1 || true)"
if grep -q "the promoter may change only spec.ref.digest" <<<"$OUT"; then
  pass "the promoter cannot change anything but the pointer"
else
  fail "the promoter changed an OCIRepository's interval: $OUT"
fi
# Only the pointer decides what runs once the applier is labelled too: the
# Stage says so, and nobody but break-glass changes what the Kustomization
# applies, the OCIRepository's url or either label.
mediated_is() {
  [ "$(kubectl -n "$1" get stage "$2" -o jsonpath='{.status.conditions[?(@.type=="Mediated")].status}/{.status.conditions[?(@.type=="Mediated")].reason}')" = "$3" ]
}
eventually 20 mediated_is prod payments False/ApplierUnguarded \
  || fail "an unlabelled Kustomization is not reported: $(kubectl -n prod get stage payments -o jsonpath='{.status.conditions}')"
kubectl -n prod label kustomization payments delivery.pacto.trianalab.io/apply-ref=payments >/dev/null
eventually 20 mediated_is prod payments True/PointerOnly \
  || fail "a labelled Kustomization is not reported as mediated: $(kubectl -n prod get stage payments -o jsonpath='{.status.conditions}')"
OUT="$(kubectl -n prod patch kustomization payments --type merge -p '{"spec":{"images":[{"name":"payments","newTag":"evil"}]}}' 2>&1 || true)"
grep -q "only break-glass changes them" <<<"$OUT" || fail "a labelled Kustomization's images were changed: $OUT"
OUT="$(kubectl -n prod patch ocirepository payments --type merge -p '{"spec":{"url":"oci://elsewhere.example/payments"}}' 2>&1 || true)"
grep -q "spec.url and spec.layerSelector decide" <<<"$OUT" || fail "a labelled OCIRepository's url was changed: $OUT"
OUT="$(kubectl -n prod label ocirepository payments delivery.pacto.trianalab.io/apply-ref- 2>&1 || true)"
grep -q "only break-glass removes it" <<<"$OUT" || fail "the apply-ref label was removed without break-glass: $OUT"
OUT="$(kubectl -n prod label kustomization payments delivery.pacto.trianalab.io/apply-ref- 2>&1 || true)"
grep -q "only break-glass removes it" <<<"$OUT" || fail "a Kustomization's apply-ref label was removed without break-glass: $OUT"
pass "the prod Stage is Mediated, and only break-glass changes its applier, url or labels"
# The recorder's RBAC reaches every Stage status; admission allows it its
# record fields and RecordStoreReady condition alone.
# The recorder may update (not patch) status; a write that races the promoter
# conflicts before admission, so try again.
forge_ready() {
  OUT="$(kubectl -n prod get stage payments -o json |
    jq '.status.conditions = [{"type":"Ready","status":"False","reason":"Forged","message":"forged"}]' |
    kubectl --as "system:serviceaccount:${DELIVERY_NS}:pacto-delivery-recorder" replace --subresource=status -f - 2>&1 || true)"
  grep -q "the Pacto delivery recorder writes records, never what runs" <<<"$OUT"
}
if eventually 20 forge_ready; then
  pass "the recorder cannot write a Stage condition other than its own"
else
  fail "the recorder rewrote a Stage's Ready condition: $OUT"
fi
KUBECONFIG="$(person_kubeconfig ops pacto:break-glass)" kubectl -n prod patch ocirepository payments --type merge \
  -p "{\"spec\":{\"ref\":{\"digest\":\"$B\"}}}" >/dev/null
OPW2="$(wait_op prod "$B" pacto.dev/override)"
wait_concluded prod "$OPW2" Witnessed/Unverified
[ "$(kubectl -n prod get stage payments -o jsonpath='{.status.frozen}')" = true ] || fail "a witnessed edit must freeze the Stage"
has_pointer prod "$B" || fail "the witnessed edit was not adopted"
[ "$ORIG" = "$WINNER" ] || fail "the pointer before the edit was $ORIG"
pass "break-glass edit witnessed, adopted and frozen"

section "no promote in the run produced a witnessed override"
WITNESSED="$(kubectl get operations -A -o json | jq '[.items[] | select(.spec.witnessed == true)] | length')"
PROMOTES="$(kubectl get operations -A -o json | jq '[.items[] | select(.spec.type == "pacto.dev/promote")] | length')"
[ "$WITNESSED" = 1 ] || fail "$WITNESSED witnessed overrides, want exactly the one break-glass edit"
pass "$PROMOTES promotes, 0 witnessed overrides of their own"
section "the promoter counts what it concluded and says so in Events"
PROMOTER_POD="$(kubectl -n "$DELIVERY_NS" get pods -l app.kubernetes.io/component=promoter -o jsonpath='{.items[0].metadata.name}')"
METRICS="$(kubectl get --raw "/api/v1/namespaces/${DELIVERY_NS}/pods/${PROMOTER_POD}:8080/proxy/metrics")" || fail "the promoter serves no metrics on port 8080"
grep -q '^pacto_delivery_operations_total{.*type="pacto.dev/promote"' <<<"$METRICS" || fail "no concluded promote is counted: $(grep pacto_delivery <<<"$METRICS")"
grep -q '^pacto_delivery_stage_frozen{namespace="prod",stage="payments"} 1' <<<"$METRICS" || fail "the frozen prod Stage is not in the frozen gauge"
kubectl -n prod get events --field-selector involvedObject.kind=Stage,reason=Frozen -o name | grep -q . || fail "the prod Stage froze without an Event"
kubectl -n test get events --field-selector involvedObject.kind=Operation,reason=Held -o name | grep -q . || fail "no held operation emitted an Event"
pass "concluded operations are counted, the frozen Stage is in its gauge and holds and freezes emit Events"
share B C F OPC OPF C120

echo "DELIVERY CORE PASS"
}

# Phase 2, health. DELIVERY_SKIP_HEALTH=1 skips it; DELIVERY_PHASES=publish runs the setup and phase 3 only.
phase_health() {
# --- phase 2: health ----------------------------------------------------------
# Mira (OTLP in, the query API the health gate reads) and the operator's node
# agent (kernel evidence the claim gate reads), both installed with the setup,
# serve one Stage, health/payments, which verifies with the Pacto contract
# check, a Mira error-ratio check and the pacto.runtime claim check, and
# re-checks every 5s once a version shipped. A clean version passes after a 10s
# soak; one that never has enough spans is held for a person when the 1m window
# ends. A generator on the host sends payments spans to Mira in the mode the
# running version is set to (ok, errors or silent), which stands in for the
# telemetry of a real workload. It proves:
#   - a version whose error ratio exceeds the limit is rolled back, and the
#     rollback cites the Mira query with absolute bounds;
#   - too little telemetry holds the version as Unknown, never Pass and never a
#     rollback, until a person decides;
#   - a regression after a version was verified triggers exactly one rollback;
#   - a version the node agent sees calling a service its contract never
#     declares fails the claim gate, though Pacto's own status is only Warning.
section "phase 2: Mira, the node agent and a Stage with health and claim checks"
! phase_runs core || await operator-back
H=health
MIRA_PORT=5603
H_REPO=apps/payments-health
AGENT_NS=pacto-node-agent
kubectl create namespace "$H" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n "$H" create secret generic cosign-pub --from-file=cosign.pub="$KEYDIR/cosign.pub" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
MIRA_PF_PID="$(pf "$MIRA_PORT" svc/mira 4318)"
PHASE_PIDS+=" $MIRA_PF_PID"
eventually 40 kubectl -n "$AGENT_NS" get ds pacto-node-agent >/dev/null 2>&1 || fail "the operator never created the node agent"
kubectl -n "$AGENT_NS" rollout status ds/pacto-node-agent --timeout=180s >/dev/null || fail "the node agent did not roll out"
agent_running() {
  [ "$(kubectl -n "$AGENT_NS" get noderuntimeobservations.pacto.trianalab.io -o jsonpath='{.items[0].status.collection.state}' 2>/dev/null)" = Running ]
}
eventually 40 agent_running || fail "the node agent never reported Running collection: $(kubectl -n "$AGENT_NS" get noderuntimeobservations.pacto.trianalab.io -o yaml)"
pass "Mira and the node agent run; the promoter knows the mira provider"

section "the generator: payments spans in the mode the running version is set to"
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
PHASE_PIDS+=" $GEN_PID"
mode() { echo "$2" > "$GEN/$1"; } # LABEL MODE

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
S="$(publish_health s)"; mode s errors

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
spec: {interval: $CANDIDATE_INTERVAL, insecure: true, url: "oci://${REG_HOST}/${H_REPO}", ref: {tag: candidate}, $VERIFY}
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
    impact: {declaredConsumersOnly: true}
    verify:
      window: 1m
      recheck: 5s
      checks:
      - name: health
        provider: mira
        params: {minSpans: "20", maxErrorRatio: "0.2", soak: "10s"}
      - name: claims
        provider: pacto.runtime
YAML
section "the fraud service: Pacto-managed, and in no payments contract"
# Created once its namespace is placed (a Stage applies into it), so while
# the operator has not read its contract it never counts as a consumer of the
# payments that other phases ship at the same time.
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

section "H1 ships: healthy spans, the contract check and the claim check all pass"
OP1="$(ship h1 "$H1")"
wait_concluded "$H" "$OP1" Succeeded/Verified
for g in pacto.contract health claims; do
  [ "$(verdict "$OP1" "$g" .result)" = '"Pass"' ] || fail "$g did not pass for H1: $(verdict "$OP1" "$g" .)"
done
[ "$(verdict "$OP1" claims '.citation["payments.kernelCoverage"]')" = '"Complete"' ] || fail "the claim gate passed without complete kernel coverage"
pass "H1 verified by the contract, Mira and the node agent"

section "H2: its error ratio exceeds the limit, so it is rolled back, citing the Mira query"
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

section "Q: too little telemetry holds as Unknown, never Pass and never a rollback"
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

section "H5 ships, then regresses after it was verified: exactly one rollback"
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

section "R: the node agent sees it call fraud, which its contract never declares: the claim gate fails"
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

section "explain and history read it back"
OUT="$(KUBECONFIG="$AGENT_A" "$PACTO" delivery explain -n "$H" "$RB2")"
grep -q "re-run: POST query" <<<"$OUT" || fail "explain does not say how to re-run the Mira citation: $OUT"
HIST="$(KUBECONFIG="$AGENT_A" "$PACTO" delivery history -n "$H" --stage payments --output-format json)"
jq -e '.completeness == "partial" and ([.entries[] | select(.type == "pacto.dev/rollback")] | length) == 4' <<<"$HIST" >/dev/null \
  || fail "history does not list the four rollbacks: $HIST"
pass "explain cites how to re-run, history lists every rollback"

section "S: an agent takes the name of S's rollback first; S fails and the Stage freezes instead of keeping S"
# agent-a creates promote-s-rollback before proposing promote-s, the name the
# promoter gives S's one rollback. It is not the promoter's rollback, so it
# never stands in for one.
KUBECONFIG="$AGENT_A" kubectl -n "$H" create -f - >/dev/null <<YAML
apiVersion: delivery.pacto.trianalab.io/v1alpha1
kind: Operation
metadata: {name: promote-s-rollback, finalizers: [delivery.pacto.trianalab.io/record]}
spec: {type: pacto.dev/promote, stage: payments, facet: pointer, params: {from: "$H1", to: "$H1"}, actor: {user: "system:serviceaccount:agents:agent-a"}, reason: squat}
YAML
eventually 60 concluded "$H" promote-s-rollback || fail "the squatting operation never concluded: $(field "$H" promote-s-rollback phase)"
OPS="$(ship s "$S")"
wait_concluded "$H" "$OPS" Failed/
squat_frozen() {
  kubectl -n "$H" get stage payments -o json |
    jq -e '.status.frozen == true and (.status.frozenReason | contains("promote-s-rollback is taken by a pacto.dev/promote system:serviceaccount:agents:agent-a"))' >/dev/null
}
eventually 30 squat_frozen || fail "a squatted rollback name did not freeze the Stage: $(kubectl -n "$H" get stage payments -o jsonpath='{.status.frozen} {.status.frozenReason}')"
[ "$(rollbacks_of "$OPS")" = 0 ] || fail "a rollback of S was raised under another name"
pass "S failed with its rollback's name taken; the Stage froze naming the squatter"

kill "$GEN_PID" "$MIRA_PF_PID" 2>/dev/null || true
echo "DELIVERY HEALTH PASS"
}

# DELIVERY_SKIP_PUBLISH=1 skips phase 3 (and phase 6, which reads it).
phase_publish() {
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
section "phase 3: pacto publish, and a Stage that promotes only what it published"
! phase_runs core || await operator-back
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
# The recorder attaches operation records as the Stages promote; publish must
# write nothing, so its referrers are counted without them.
referrers() {
  oras discover --plain-http --format json "127.0.0.1:${LOCAL_REG_PORT}/$1" 2>/dev/null \
    | jq '[.. | objects | select(has("digest") and ((.annotations // {})["dev.sigstore.bundle.predicateType"] != "pacto.dev/operation/v1"))] | length'
}

kubectl -n "$P" apply -f - >/dev/null <<YAML
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata: {name: billing-candidates}
spec: {interval: $CANDIDATE_INTERVAL, insecure: true, url: "oci://${REG_HOST}/apps/billing", ref: {semver: ">=0.0.0"}, $VERIFY}
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
  gates: {impact: {declaredConsumersOnly: true}, verify: {window: 3m}}
YAML

section "--plan lists what is missing and writes nothing"
OUT="$(pub --plan)" || fail "the plan of a publishable version failed: $OUT"
{ grep -q "manifests  manifests  absent" <<<"$OUT" && grep -q "bundle     bundle     absent" <<<"$OUT" \
  && grep -q "2 to publish; nothing was changed" <<<"$OUT"; } || fail "the plan does not list the two missing artifacts: $OUT"
[ -z "$(tags apps/billing)$(tags demo/billing)" ] || fail "--plan wrote to the registry: $(tags apps/billing) $(tags demo/billing)"
pass "--plan lists the manifests and the bundle as absent and writes nothing"

section "publish: signed, the deploy artifact naming its contract"
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

section "the same publish again writes nothing"
BEFORE="$(tags apps/billing)|$(tags demo/billing)|$(referrers "apps/billing@$D1")"
OUT="$(pub)" || fail "the second publish failed: $OUT"
grep -q "Nothing to publish" <<<"$OUT" || fail "the second publish published something: $OUT"
AFTER="$(tags apps/billing)|$(tags demo/billing)|$(referrers "apps/billing@$D1")"
[ "$BEFORE" = "$AFTER" ] || fail "the second publish changed the registry: $BEFORE -> $AFTER"
pass "a second publish wrote no tag, artifact or signature ($AFTER)"

section "a breaking change published as a minor is refused"
billing_contract 1.1.0 job
if OUT="$(pub --plan 2>&1)"; then fail "the plan of a breaking minor passed: $OUT"; fi
grep -q "needs a major bump over 1.0.0: publish 2.0.0 or higher" <<<"$OUT" || fail "the plan does not say a major is needed: $OUT"
if OUT="$(pub 2>&1)"; then fail "a breaking change was published as a minor: $OUT"; fi
# The only write is the signed refusal, on its per-service anchor tag in the
# contract repository, where the dashboard reads it.
grep -q "Refusal: the refusal is kept, signed, in ${REG_HOST}/demo/billing" <<<"$OUT" || fail "the refusal was not kept: $OUT"
ANCHOR="$(tags demo/billing | tr ' ' '\n' | grep '^refusals-')" || fail "no refusal anchor in demo/billing: $(tags demo/billing)"
REFUSALS="$(oras discover --plain-http --format json "127.0.0.1:${LOCAL_REG_PORT}/demo/billing:$ANCHOR" 2>/dev/null \
  | jq '[.. | objects | select((.annotations // {})["dev.sigstore.bundle.predicateType"] == "pacto.dev/publish-refusal/v1")] | length')"
[ "$REFUSALS" = 1 ] || fail "the refusal anchor carries $REFUSALS refusals, want 1"
[ "$(tags apps/billing)|$(tags demo/billing | sed "s/$ANCHOR //")|$(referrers "apps/billing@$D1")" = "$AFTER" ] \
  || fail "the refused publish wrote a version to the registry: $(tags apps/billing) $(tags demo/billing)"
billing_contract 1.0.0 service
pass "BREAKING since 1.0.0 is refused as 1.1.0; only its signed refusal was written"

section "delivery promotes the published version with no extra step"
OPP="$(wait_op "$P" "$D1")"
wait_concluded "$P" "$OPP" Succeeded/Verified
[ "$(kubectl -n "$P" get stage billing -o jsonpath='{.status.pointer}')" = "$D1" ] || fail "the Stage does not run the published version"
[ "$(kubectl -n "$P" get deploy billing -o jsonpath='{.spec.template.metadata.annotations.version}')" = 1.0.0 ] \
  || fail "the cluster does not run billing 1.0.0"
published_in "$P" "$D1" billing || fail "the Stage does not remember $D1 as published"
[ "$(field "$P" "$OPP" actorClass)" = controller ] || fail "the promote was not raised by the promoter"
pass "the promoter promoted $D1 by itself and the contract check verified it"

section "publish --propose waits for Flux, then proposes on a Stage that does not promote by itself"
M=publish-manual
kubectl create namespace "$M" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n "$M" create secret generic cosign-pub --from-file=cosign.pub="$KEYDIR/cosign.pub" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n "$M" apply -f - >/dev/null <<YAML
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata: {name: billing-candidates}
spec: {interval: $CANDIDATE_INTERVAL, insecure: true, url: "oci://${REG_HOST}/apps/billing", ref: {semver: ">=0.0.0"}, $VERIFY}
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
  gates: {impact: {declaredConsumersOnly: true}, verify: {window: 3m}}
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

section "an artifact pushed another way is never a candidate"
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

section "a Stage whose candidate sets no spec.verify is not Ready"
# ready_is NS STAGE STATUS/REASON — the Stage's Ready condition.
ready_is() {
  [ "$(kubectl -n "$1" get stage "$2" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}/{.status.conditions[?(@.type=="Ready")].reason}')" = "$3" ]
}
eventually 20 ready_is "$P" billing True/Ready || fail "the publishing Stage is not Ready: $(kubectl -n "$P" get stage billing -o jsonpath='{.status.conditions}')"
# Without the apply-ref label admission guards nothing and the promoter may not
# write the pointer, so the Stage says so instead of promoting into a refusal.
# Only break-glass may remove the label (admission), so it is the one to.
KUBECONFIG="$(person_kubeconfig ops pacto:break-glass)" kubectl -n "$P" label ocirepository billing delivery.pacto.trianalab.io/apply-ref- >/dev/null
eventually 20 ready_is "$P" billing False/ApplyRefUnlabelled \
  || fail "an unlabelled apply OCIRepository is not reported: $(kubectl -n "$P" get stage billing -o jsonpath='{.status.conditions}')"
kubectl -n "$P" label ocirepository billing delivery.pacto.trianalab.io/apply-ref=billing >/dev/null
eventually 20 ready_is "$P" billing True/Ready || fail "the Stage is not Ready again once relabelled"
pass "a Stage whose apply OCIRepository lost its apply-ref label is Ready=False with ApplyRefUnlabelled until it is relabelled"
# A binding that only warns guards nothing the promoter can count on: the
# Stage is not Ready until it denies again.
kubectl patch validatingadmissionpolicybinding pacto-delivery-pointer --type merge -p '{"spec":{"validationActions":["Warn"]}}' >/dev/null
eventually 20 ready_is "$P" billing False/Unguarded \
  || { kubectl patch validatingadmissionpolicybinding pacto-delivery-pointer --type merge -p '{"spec":{"validationActions":["Deny"]}}' >/dev/null
       fail "a pointer binding set to Warn is not reported: $(kubectl -n "$P" get stage billing -o jsonpath='{.status.conditions}')"; }
kubectl patch validatingadmissionpolicybinding pacto-delivery-pointer --type merge -p '{"spec":{"validationActions":["Deny"]}}' >/dev/null
eventually 20 ready_is "$P" billing True/Ready || fail "the Stage is not Ready again once the binding denies"
pass "a Stage is Ready=False with Unguarded while the pointer binding only warns"
kubectl -n "$M" apply -f - >/dev/null <<YAML
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata: {name: billing-anyone}
spec: {interval: $CANDIDATE_INTERVAL, insecure: true, url: "oci://${REG_HOST}/apps/billing", ref: {semver: ">=0.0.0"}}
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
  gates: {impact: {declaredConsumersOnly: true}}
YAML
eventually 20 ready_is "$M" billing-anyone False/CandidateUnverified \
  || fail "a Stage whose candidate sets no spec.verify is not reported: $(kubectl -n "$M" get stage billing-anyone -o jsonpath='{.status.conditions}')"
[ -z "$(kubectl -n "$M" get stage billing-anyone -o jsonpath='{.status.published}')" ] || fail "an unverified candidate was recorded as published"
pass "a Stage whose candidate OCIRepository sets no spec.verify is Ready=False with CandidateUnverified and records nothing"
share P M FOREIGN
echo "DELIVERY PUBLISH PASS"
}

# DELIVERY_SKIP_ORDER=1 skips phase 5.
phase_ordering() {
# --- phase 5: ordering ---------------------------------------------------------
# pricing ships to live straight from its candidate tag. Its consumer, orders,
# ships through staging (fed by a candidate tag) and live (fed by staging's
# verified versions). orders 1.8.0 accepts pricing ^1.0.0; orders 1.9.0 accepts
# >=1.0.0 <3.0.0. It proves the vision's ordering step against the cluster:
#   - a breaking pricing 2.0.0 is held in live, and its plan names orders 1.8.0
#     as what must move first, with its Stage and orders 1.9.0, the version
#     staging verified, as the one that can go first;
#   - once orders 1.9.0 is promoted in live, pricing 2.0.0 ships with no person;
#   - with a Pacto in live that Pacto cannot read and no consumer incompatible,
#     the graph is partial and the plan holds as Unknown. (A partial graph
#     next to an incompatible consumer, where the order says the list may be
#     short, is proven by TestOrderOnAFirstVersionAndAPartialGraph.)
# Compatibility is judged from the declared ranges only; nothing here claims
# Pacto knows which fields or endpoints orders uses.
section "phase 5: a provider whose consumer must move first"
! phase_runs core || await operator-back
for n in staging live; do
  kubectl create namespace "$n" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  kubectl -n "$n" create secret generic cosign-pub --from-file=cosign.pub="$KEYDIR/cosign.pub" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
done
PR100="$(contract pricing 1.0.0 service)"
PR200="$(contract pricing 2.0.0 service)"  # orders 1.8.0 accepts ^1.0.0 only
PR210="$(contract pricing 2.1.0 service)"
# Not required: staging runs no pricing, and a required dependency the operator
# cannot observe there would keep orders' contract check Unknown. Impact judges
# the declared range either way.
orders_contract() { # VERSION RANGE
  contract orders "$1" service "dependencies: [{name: pricing, ref: \"oci://${REG_HOST}/demo/pricing\", required: false, compatibility: \"$2\"}]"
}
OR18="$(orders_contract 1.8.0 "^1.0.0")"
OR19="$(orders_contract 1.9.0 ">=1.0.0 <3.0.0")"
P1="$(publish P1 "$PR100" pricing)"; candidate P1 pricing
O18="$(publish O18 "$OR18" orders)"; candidate O18 orders
# stage_ptr NS STAGE — the digest a Stage runs.
stage_ptr() { kubectl -n "$1" get stage "$2" -o jsonpath='{.status.pointer}'; }
stage_runs() { [ "$(stage_ptr "$1" "$2")" = "$3" ]; }
# stage_yaml NAME ENV CANDIDATES [AUTO]
stage_yaml() {
  cat <<YAML
apiVersion: delivery.pacto.trianalab.io/v1alpha1
kind: Stage
metadata: {name: $1}
spec:
  service: {name: $1}
  environment: $2
  applyRef: {name: $1}
  candidates: $3
  auto: ${4:-true}
  gates: {impact: {declaredConsumersOnly: true}, verify: {window: 3m}}
YAML
}
candidates_yaml() { # SERVICE
  cat <<YAML
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata: {name: $1-candidates}
spec: {interval: $CANDIDATE_INTERVAL, insecure: true, url: "oci://${REG_HOST}/apps/$1", ref: {tag: candidate}, $VERIFY}
YAML
}

section "bootstrap: orders 1.8.0 verified in staging and live, then pricing 1.0.0 in live"
# The Stages come first, promoting nothing until orders' Pactos are read: a
# Pacto in a namespace a Stage applies into counts only for that Stage's
# environment, so while the operator has not read one yet it never holds what
# the other phases ship at the same time.
{ candidates_yaml orders; echo ---; stage_yaml orders staging "{ociRepositoryRef: {name: orders-candidates}}" false; } | kubectl -n staging apply -f - >/dev/null
stage_yaml orders live "{upstreamStage: {name: orders, namespace: staging}}" false | kubectl -n live apply -f - >/dev/null
apply_env staging orders "$O18"
apply_env live orders "$O18"
for n in staging live; do eventually 60 reconciled "$n" orders || fail "$n/orders was never reconciled"; done
for n in staging live; do kubectl -n "$n" patch stage orders --type merge -p '{"spec":{"auto":true}}' >/dev/null; done
OP="$(wait_op staging "$O18")"; wait_concluded staging "$OP" Succeeded/Verified
OP="$(wait_op live "$O18")"; wait_concluded live "$OP" Succeeded/Verified
apply_env live pricing "$P1"
eventually 60 reconciled live pricing || fail "live/pricing was never reconciled"
{ candidates_yaml pricing; echo ---; stage_yaml pricing live "{ociRepositoryRef: {name: pricing-candidates}}"; } | kubectl -n live apply -f - >/dev/null
OP="$(wait_op live "$P1")"; wait_concluded live "$OP" Succeeded/Verified
# From here agents and people move live, nothing else.
for s in orders pricing; do kubectl -n live patch stage "$s" --type merge -p '{"spec":{"auto":false}}' >/dev/null; done
pass "orders 1.8.0 runs in staging and live, pricing 1.0.0 in live, all verified"

section "orders 1.9.0 (accepts pricing >=1.0.0 <3.0.0) passes staging, and live does not take it by itself"
O19="$(publish O19 "$OR19" orders)"; candidate O19 orders
OP="$(wait_op staging "$O19")"; wait_concluded staging "$OP" Succeeded/Verified
offered() { kubectl -n staging get stage orders -o json | jq -e --arg d "$O19" '.status.offered // [] | index($d)' >/dev/null; }
eventually 30 offered || fail "staging never offered orders 1.9.0 downstream"
always 5 stage_runs live orders "$O18" || fail "live took orders 1.9.0 with auto off"

section "pricing 2.0.0 is held in live, and the plan says orders must move first"
P2="$(publish P2 "$PR200" pricing)"; candidate P2 pricing
eventually 60 published_in live "$P2" pricing || fail "pricing 2.0.0 was never verified as published in live"
as_agent "$AGENT_A" propose -n live --stage pricing --to "$P2" --name pricing-first --reason "phase 5" >/dev/null
eventually 30 held live pricing-first || fail "pricing 2.0.0 was not held: $(field live pricing-first phase) $(field live pricing-first message)"
PLAN="$(kubectl -n live get operation pricing-first -o jsonpath='{.status.plan}')"
echo "  order: $(jq -c .order <<<"$PLAN")"
jq -e --arg o19 "$O19" '.floor.consumers == ["orders"] and (.order.unknown // [] | length == 0) and (.order.moveFirst | length == 1) and
  (.order.moveFirst[0] | .service == "orders" and .version == "1.8.0" and .accepts == "^1.0.0"
    and .stage == {name: "orders", namespace: "live"}
    and .candidate.digest == $o19 and .candidate.version == "1.9.0" and .candidate.accepts == ">=1.0.0 <3.0.0"
    and .candidate.verifiedOn == {name: "orders", namespace: "staging"}
    and (.message | contains("Ship orders first")))' <<<"$PLAN" >/dev/null \
  || fail "the plan does not name orders 1.8.0, its Stage and orders 1.9.0 from staging: $PLAN"
field live pricing-first question | grep -q "Or ship orders first" || fail "the question does not offer shipping orders first: $(field live pricing-first question)"
OUT="$(as_agent "$AGENT_A" explain -n live pricing-first 2>&1)" || fail "explain failed: $OUT"
grep -q "move first: orders 1.8.0 runs in live and accepts pricing ^1.0.0, not 2.0.0" <<<"$OUT" \
  || fail "explain does not say orders must move first: $OUT"
always 5 stage_runs live pricing "$P1" || fail "pricing 2.0.0 moved without a person"
pass "pricing 2.0.0 waits; the plan names orders 1.8.0, Stage live/orders and orders 1.9.0 verified on staging/orders"

section "orders 1.9.0 moves first, then pricing 2.0.0 ships with no person"
# Pacto only says what moves first: the agent moves orders, then plans again.
as_agent "$AGENT_A" cancel -n live pricing-first >/dev/null
as_agent "$AGENT_A" propose -n live --stage orders --to "$O19" --name orders-first --reason "phase 5: pricing 2.0.0 needs it" >/dev/null
wait_concluded live orders-first Succeeded/Verified
as_agent "$AGENT_A" propose -n live --stage pricing --to "$P2" --name pricing-after --reason "phase 5" >/dev/null
wait_concluded live pricing-after Succeeded/Verified
kubectl -n live get operation pricing-after -o json | jq -e '.status.plan.floor.human != true and .status.plan.order == null and (.status.approvals // [] | length == 0)' >/dev/null \
  || fail "pricing 2.0.0 needed a person after orders moved: $(kubectl -n live get operation pricing-after -o jsonpath='{.status.plan}')"
stage_runs live pricing "$P2" || fail "live does not run pricing 2.0.0"
pass "with orders 1.9.0 in live, pricing 2.0.0 shipped with nobody involved"

section "a partial graph holds as Unknown"
# shipping's contract is not in the registry, so its Pacto never carries one:
# Pacto cannot rule it out as a consumer of pricing.
kubectl -n live apply -f - >/dev/null <<YAML
apiVersion: pacto.trianalab.io/v1alpha1
kind: Pacto
metadata: {name: shipping}
spec:
  checkIntervalSeconds: 30
  contractRef: {oci: "oci://${REG_HOST}/demo/shipping@sha256:$(printf '0%.0s' $(seq 64))"}
  target: {workloadRef: {name: shipping, kind: Deployment}}
YAML
P3="$(publish P3 "$PR210" pricing)"; candidate P3 pricing
eventually 60 published_in live "$P3" pricing || fail "pricing 2.1.0 was never verified as published in live"
as_agent "$AGENT_A" propose -n live --stage pricing --to "$P3" --name pricing-partial --reason "phase 5" >/dev/null
eventually 30 held live pricing-partial || fail "pricing 2.1.0 was not held: $(field live pricing-partial phase) $(field live pricing-partial message)"
PLAN="$(kubectl -n live get operation pricing-partial -o jsonpath='{.status.plan}')"
jq -e '.order == null and .floor.decision == "accept-unknown" and
  (.verdicts[] | select(.gate == "pacto.impact") | .result == "Unknown" and (.message | contains("partial")))' <<<"$PLAN" >/dev/null \
  || fail "a partial graph did not hold as Unknown with no order: $PLAN"
always 5 stage_runs live pricing "$P2" || fail "pricing 2.1.0 moved while the graph was partial"
as_agent "$AGENT_A" cancel -n live pricing-partial >/dev/null
kubectl -n live delete pacto shipping --wait=false >/dev/null
pass "with shipping unreadable the plan holds as Unknown (accept-unknown)"
echo "DELIVERY ORDER PASS"
}

phase_identity() {
  identity_scenario
  echo "DELIVERY IDENTITY PASS"
}

# Phase 6 needs phases 1 and 3 (it reads their operations). Its first part
# runs beside the other phases; its audit of every operation and every Stage of
# the run runs once they all finished.
# shellcheck source=tests/acceptance/kind/delivery-records.sh
source "$(dirname "$0")/delivery-records.sh"
phase_records() {
  section "phase 6: the recorder catches up, and a forged record stops the stream"
  records_store
}
phase_audit() {
  records_audit
  echo "DELIVERY RECORDS PASS"
}

timing_end
run_phases
if phase_runs records; then ( phase_body audit ); fi
TIMING_PHASE=teardown
kill "$REG_PF_PID" 2>/dev/null || true
echo "DELIVERY PASS"
keep_or_teardown "$NS" "$CLUSTER" delete_cluster
