#!/usr/bin/env bash
# Real release simulation. Builds the
# real release artifacts and pushes them to a DISPOSABLE local registry via the
# SAME shared adapters production uses (build-cli.sh, verify-oci.sh,
# publish-go-tag.sh), then proves digest idempotency, fail-closed immutability and
# resume — with real digests, no production coordinate or credential. This is the
# code path release.yml runs; only the coordinates + credentials differ.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
REG="${PACTO_STAGING_REGISTRY:-localhost:5001}"

# Fail-closed: never a production-looking target.
case "$REG" in
  *ghcr.io*|*trianalab*) echo "refusing production-looking registry '$REG'"; exit 1;;
esac
[ "${PACTO_ALLOW_PROD:-}" = "1" ] && { echo "PACTO_ALLOW_PROD must NOT be set for a dry run"; exit 1; }

WORK="$(mktemp -d)"; DIST="$WORK/dist"; mkdir -p "$DIST"
OWNED_REG=0
cleanup() { [ "$OWNED_REG" = 1 ] && docker rm -f pacto-dryrun-reg >/dev/null 2>&1 || true; rm -rf "$WORK"; }
trap cleanup EXIT

# 0. disposable local registry — ALWAYS fresh. A stale registry from a prior run
# would carry old tags/ledgers and corrupt the absent/identical/conflict proofs,
# so tear down anything on the port + start clean (localhost only).
case "$REG" in
  localhost:5001|127.0.0.1:5001)
    docker rm -f pacto-dryrun-reg >/dev/null 2>&1 || true
    for c in $(docker ps -q --filter "publish=5001"); do docker rm -f "$c" >/dev/null 2>&1 || true; done
    docker run -d --name pacto-dryrun-reg -p 5001:5000 registry:2 >/dev/null
    OWNED_REG=1
    for _ in $(seq 1 30); do curl -sf "http://$REG/v2/" >/dev/null 2>&1 && break; sleep 1; done ;;
  *)
    curl -sf "http://$REG/v2/" >/dev/null 2>&1 || { echo "registry $REG not reachable"; exit 1; } ;;
esac

# 1. plan + a disposable version that can never collide with a real release
node "$ROOT/release/scripts/build-release-plan.mjs" >/dev/null
CORE="$(python3 -c "import json;print(json.load(open('$ROOT/release/release-plan.json'))['groups']['core']['version'])")"
DV="0.0.0-dryrun"
SHA="$(git -C "$ROOT" rev-parse HEAD)"
OPIMG="$REG/pacto-operator/pacto-controller:$DV"

echo "== TRANSACTION SELECTION: drive the REAL orchestrator (detect.mjs) per scenario =="
# Prove the decision layer that feeds the release DAG: core-only, k8s-only,
# coordinated and recovery each decide the correct unit set. This is the same
# detect.mjs release.yml runs; the release.yml job `if:` selection over these
# units is separately gated by tests/release/dag_test.go.
CORE_UNITS="core,cli,dashboard-image,dashboard-contract-bundle,demo-bundles,demo-compose"
K8S_UNITS="k8s-module,operator-image,operator-chart,k8s-docs"
mk_txn() { # <id> <groups-csv> <units-csv> <ready 0|1> -> transaction json
  python3 - "$@" <<'PY'
import json,sys
tid,groups,units,ready=sys.argv[1],sys.argv[2].split(','),sys.argv[3].split(','),sys.argv[4]=='1'
print(json.dumps({"schema":"pacto-release-transaction/v1","ready":ready,"transactionId":tid,
  "sourceSha":"deadbeef","manifestSha":"x","changedGroups":groups,"changedUnits":units,
  "newVersions":{},"previousVersions":{},"expectedTags":[],"expectedCoordinates":[],
  "dependencyOrder":units,"units":{u:{"status":"pending"} for u in units}}))
PY
}
run_detect() { # <event> <txn-json> [ENV=val ...] -> detect.mjs stdout (GITHUB_OUTPUT lines)
  local ev="$1" txn="$2"; shift 2
  local d; d="$(mktemp -d)"; mkdir -p "$d/release"
  printf '%s' "$txn" > "$d/release/release-transaction.json"
  cp "$ROOT/release/release-manifest.json" "$d/release/release-manifest.json"
  ( cd "$d" && env GITHUB_EVENT_NAME="$ev" GITHUB_SHA=deadbeef "$@" node "$ROOT/release/orchestrator/detect.mjs" ) 2>/dev/null
  local rc=$?; rm -rf "$d"; return $rc
}
assert_units() { # <label> <actual detect stdout> <expected release> <expected units-csv>
  local label="$1" out="$2" wantrel="$3" wantunits="$4"
  local rel units
  rel="$(printf '%s\n' "$out" | sed -n 's/^release=//p')"
  units="$(printf '%s\n' "$out" | sed -n 's/^units=//p')"
  [ "$rel" = "$wantrel" ] || { echo "   FAIL $label: release=$rel want $wantrel"; exit 1; }
  # order-independent unit-set compare
  local a b; a="$(printf '%s' "$units" | tr ',' '\n' | sort | paste -sd, -)"; b="$(printf '%s' "$wantunits" | tr ',' '\n' | sort | paste -sd, -)"
  [ "$a" = "$b" ] || { echo "   FAIL $label: units=$a want $b"; exit 1; }
  echo "   OK $label: release=$rel units=[$units]"
}
assert_units "core-only"    "$(run_detect push "$(mk_txn t-core core "$CORE_UNITS" 1)")"                    true  "$CORE_UNITS"
assert_units "k8s-only"     "$(run_detect push "$(mk_txn t-k8s  k8s  "$K8S_UNITS"  1)")"                    true  "$K8S_UNITS"
assert_units "coordinated"  "$(run_detect push "$(mk_txn t-both 'core,k8s' "$CORE_UNITS,$K8S_UNITS" 1)")"   true  "$CORE_UNITS,$K8S_UNITS"
assert_units "not-ready"    "$(run_detect push "$(mk_txn t-nr   core "$CORE_UNITS" 0)")"                    false ""
# recovery is workflow_dispatch only, and a mismatched transactionId is REFUSED
# (exit non-zero) — recovery must never become a second release trigger.
if run_detect workflow_dispatch "$(mk_txn t-real core "$CORE_UNITS" 1)" INPUT_TRANSACTION_ID=t-wrong INPUT_SOURCE_SHA=deadbeef >/dev/null 2>&1; then
  echo "   FAIL recovery: a mismatched transactionId was accepted"; exit 1
fi
echo "   OK recovery-refused: mismatched transactionId rejected (no second release trigger)"

echo "== build + push the operator image (production root context) =="
docker build -f "$ROOT/integrations/kubernetes/Dockerfile" --build-arg VERSION="$DV" -t "$OPIMG" "$ROOT"
docker push "$OPIMG" >/dev/null
digest() { crane digest --insecure "$1" 2>/dev/null || crane digest "$1"; }
OPDIGEST="$(digest "$OPIMG")"
echo "   operator image digest: $OPDIGEST"

echo "== build the CLI binaries + checksums + SBOM (reproducible) =="
PACTO_DIST_DIR="$DIST" bash "$ROOT/release/orchestrator/build-cli.sh" "v$CORE" "$SHA"
test -s "$DIST/checksums.txt" || { echo "no checksums produced"; exit 1; }
# Reproducibility: a second build from the same commit is byte-identical.
DIST2="$WORK/dist2"; mkdir -p "$DIST2"
PACTO_DIST_DIR="$DIST2" bash "$ROOT/release/orchestrator/build-cli.sh" "v$CORE" "$SHA" >/dev/null
diff -q "$DIST/checksums.txt" "$DIST2/checksums.txt" >/dev/null \
  && echo "   reproducible: identical checksums on rebuild" || { echo "CLI build not reproducible"; exit 1; }

echo "== package + push the chart (with source-revision provenance, as production does) =="
CHART_SRC="$WORK/chart-src"
cp -r "$ROOT/integrations/kubernetes/charts/pacto-operator" "$CHART_SRC"
SHA="$SHA" yq -i '.annotations."org.opencontainers.image.revision" = strenv(SHA)' "$CHART_SRC/Chart.yaml"
helm package "$CHART_SRC" --version "$DV" --app-version "$DV" -d "$WORK" >/dev/null
CHART="$(ls "$WORK"/pacto-operator-*.tgz)"
helm push "$CHART" "oci://$REG/pacto-operator/charts" --plain-http >/dev/null 2>&1 \
  || helm push "$CHART" "oci://$REG/pacto-operator/charts" >/dev/null
echo "   chart pushed"

echo "== operator-chart crash recovery: verify-oci adopts a pushed-but-unrecorded chart =="
# Simulates a push-before-record crash: no recorded digest, but the chart's manifest
# provenance (org.opencontainers.image.revision/version) proves it is this transaction's.
CHART_REF="$REG/pacto-operator/charts/pacto-operator:$DV"
cstate="$(bash "$ROOT/release/orchestrator/verify-oci.sh" "$CHART_REF" "" "$SHA" "$DV")"
[ "$cstate" = adopt ] || { echo "expected chart 'adopt' by provenance, got '$cstate'"; exit 1; }
echo "   adoptable (revision+version provenance matched)"
if bash "$ROOT/release/orchestrator/verify-oci.sh" "$CHART_REF" "" "wrong-sha" "$DV" >/dev/null 2>&1; then
  echo "expected conflict on a foreign revision"; exit 1
fi
echo "   foreign revision refused (fail-closed)"

echo "== DURABLE LEDGER + absent/identical/conflict + partial-failure/resume (shared adapter) =="
export PACTO_LEDGER_REPO="$REG/pacto-release-ledger"
led() { bash "$ROOT/release/orchestrator/ledger.sh" "$@"; }
vfy() { bash "$ROOT/release/orchestrator/verify-oci.sh" "$@"; }
TXN="dryrun-$(git -C "$ROOT" rev-parse --short HEAD)"
export PACTO_RELEASE_TXN="$TXN"
MSHA="$(node -e 'const c=require("crypto"),fs=require("fs");const m=JSON.parse(fs.readFileSync("'"$ROOT"'/release/release-manifest.json"));const nv=Object.fromEntries(Object.entries(m.units).map(([u,v])=>[u,v.version]));const st=v=>Array.isArray(v)?v.map(st):(v&&typeof v=="object"?Object.fromEntries(Object.keys(v).sort().map(k=>[k,st(v[k])])):v);process.stdout.write(c.createHash("sha256").update(JSON.stringify(st(nv))).digest("hex"))')"
led init "$TXN" "$SHA" "$MSHA" >/dev/null

# Publish EACH unit through the EXACT shared adapter release.yml uses
# (publish-oci-unit.sh): verify against the ledger, then absent->push+record,
# identical->skip (safe resume), conflict->fail closed. Only PACTO_LEDGER_REPO +
# the coordinates differ from production. One OCI unit per release LINE so the
# partial-failure/resume spans BOTH categories; every other OCI unit
# (dashboard-contract-bundle, demo-bundles, operator-chart) publishes through
# this identical adapter path — exercised here once per line, not per artifact.
adapter() { bash "$ROOT/release/orchestrator/publish-oci-unit.sh" "$@"; }
# Fresh coordinates the adapter itself publishes: the build step already pushed
# $OPIMG, so reusing it would read back as an existing tag (a conflict). Retag the
# real operator image under one fresh coordinate per release LINE.
K8S_UNIT_REF="$REG/pacto-operator/pacto-controller-ledger:$DV"   # k8s line
CORE_UNIT_REF="$REG/pacto-dashboard:$DV"                         # core line
docker tag "$OPIMG" "$K8S_UNIT_REF"
docker tag "$OPIMG" "$CORE_UNIT_REF"

echo "   RUN 1: publish the k8s-line unit, then CRASH before the core-line unit"
adapter operator-image "$K8S_UNIT_REF" "$DV" -- docker push "$K8S_UNIT_REF" >/dev/null
[ "$(led status "$TXN" operator-image)" = complete ] || { echo "operator-image not recorded"; exit 1; }
# The run crashed here: dashboard-image was never published nor recorded (pending).

echo "   RUN 2 (resume same transaction $TXN), across both release lines:"
adapter operator-image "$K8S_UNIT_REF" "$DV" -- docker push "$K8S_UNIT_REF" >/dev/null   # complete -> identical -> skip
adapter dashboard-image "$CORE_UNIT_REF" "$DV" -- docker push "$CORE_UNIT_REF" >/dev/null # absent -> publish + record
for u in operator-image dashboard-image; do
  [ "$(led status "$TXN" "$u")" = complete ] || { echo "resume did not complete $u"; exit 1; }
done
echo "   resume: completed k8s-line unit skipped, incomplete core-line unit published + recorded"

echo "== CONFLICT: an existing tag with different bytes than the ledger fails closed =="
echo 'FROM busybox' | docker build -q -t "$CORE_UNIT_REF" - >/dev/null && docker push "$CORE_UNIT_REF" >/dev/null
if adapter dashboard-image "$CORE_UNIT_REF" "$DV" -- true >/dev/null 2>&1; then
  echo "   FAIL: the adapter accepted a conflicting digest"; exit 1
fi
echo "   adapter correctly reported conflict + failed closed (no overwrite)"

echo "== ITEM 5: ledger metadata verify (match + fail-closed mismatch) =="
led verify "$TXN" "$SHA" "$MSHA" >/dev/null && echo "   verify MATCH ok"
if led verify "$TXN" "different-source-sha" "$MSHA" >/dev/null 2>&1; then
  echo "   FAIL: metadata mismatch was accepted"; exit 1
fi
echo "   verify MISMATCH refused (fail closed)"

echo "== ITEM 2: parallel per-unit records lose nothing; idempotent; conflict fails =="
CTX="dryrun-conc-$(git -C "$ROOT" rev-parse --short HEAD)"; led init "$CTX" "$SHA" "$MSHA" >/dev/null
CUNITS="core cli dashboard-image dashboard-contract-bundle demo-bundles demo-compose k8s-module operator-image operator-chart k8s-docs"
i=0
for u in $CUNITS; do i=$((i+1)); led record "$CTX" "$u" "coord/$u" "1.0.0" "sha256:$(printf '%064x' $((i*7)))" complete >/dev/null 2>&1 & done
wait
n=0; for u in $CUNITS; do [ "$(led status "$CTX" "$u")" = complete ] && n=$((n+1)) || echo "   LOST $u"; done
u=$(printf '%s\n' $CUNITS | wc -l | tr -d ' ')
[ "$n" -eq "$u" ] || { echo "   FAIL: parallel records lost $((u-n)) unit(s)"; exit 1; }
echo "   $n/$u parallel records survived (no lost updates), attributable to $CTX"
led record "$CTX" core "coord/core" 1.0.0 "sha256:$(printf '%064x' 7)" complete >/dev/null 2>&1 \
  && echo "   duplicate-identical record idempotent" || { echo "   FAIL idempotent"; exit 1; }
if led record "$CTX" core "coord/core" 1.0.0 "sha256:$(printf '%064x' 999)" complete >/dev/null 2>&1; then
  echo "   FAIL: a conflicting digest for an existing unit was accepted"; exit 1
fi
echo "   conflicting digest for an existing unit fails closed"

echo "== ITEM 3: crash after push / before record -> resume ADOPTS via provenance =="
CRTXN="dryrun-crash-$(git -C "$ROOT" rev-parse --short HEAD)"; led init "$CRTXN" "$SHA" "$MSHA" >/dev/null
CRIMG="$REG/pacto-operator/pacto-controller-crash:$DV"
printf 'FROM busybox\nLABEL org.opencontainers.image.revision=%s\nLABEL org.opencontainers.image.version=%s\n' "$SHA" "$DV" \
  | DOCKER_BUILDKIT=0 docker build -q -t "$CRIMG" - >/dev/null
docker push "$CRIMG" >/dev/null   # simulate: artifact pushed, runner died before the ledger record
[ -z "$(led digest "$CRTXN" operator-image)" ] || { echo "   FAIL: expected empty ledger for the crashed unit"; exit 1; }
PACTO_RELEASE_TXN="$CRTXN" PACTO_EXPECT_REVISION="$SHA" PACTO_EXPECT_VERSION="$DV" \
  adapter operator-image "$CRIMG" "$DV" -- false >/dev/null
[ -n "$(led digest "$CRTXN" operator-image)" ] || { echo "   FAIL: crash window not recovered"; exit 1; }
echo "   crash window recovered: remote adopted via provenance + recorded (no re-push)"

echo "== PARTIAL FAILURE + RESUME: go tags in an isolated clone =="
CLONE="$WORK/clone"; git clone -q "$ROOT" "$CLONE"
# Disposable bare origin so tag pushes never touch the real repo.
git init --bare -q "$WORK/tags-origin.git"
git -C "$CLONE" remote set-url origin "$WORK/tags-origin.git"
CSHA="$(git -C "$CLONE" rev-parse HEAD)"
( cd "$CLONE" && PACTO_TAG_REMOTE=origin bash "$ROOT/release/orchestrator/publish-go-tag.sh" "v$DV" "$CSHA" >/dev/null )
# resume the same transaction: the tag is already at the sha -> idempotent no-op.
( cd "$CLONE" && PACTO_TAG_REMOTE=origin bash "$ROOT/release/orchestrator/publish-go-tag.sh" "v$DV" "$CSHA" ) \
  | grep -q "nothing to do" && echo "   resume: tag idempotent" || { echo "resume not idempotent"; exit 1; }
# a moved tag (different sha) is refused.
OTHER="$(git -C "$CLONE" rev-parse HEAD~1)"
if ( cd "$CLONE" && PACTO_TAG_REMOTE=origin bash "$ROOT/release/orchestrator/publish-go-tag.sh" "v$DV" "$OTHER" ) >/dev/null 2>&1; then
  echo "   FAIL: an immutable tag was moved"; exit 1
fi
echo "   immutable tag move refused"

echo "== simulated GitHub Release payload (finalized last, from real assets) =="
python3 - "$DIST/checksums.txt" "$CORE" "$SHA" "$WORK/release.json" <<'PY'
import json, sys
checks, core, sha, out = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
assets = [l.split()[1] for l in open(checks) if l.strip()]
json.dump({"tag": f"v{core}", "targetSha": sha, "assets": assets, "dryRun": True}, open(out, "w"), indent=2)
print(f"   release payload: v{core} with {len(assets)} verified assets")
PY

echo "== standalone consumability (published core resolves, GOWORK=off, no replace) =="
bash "$ROOT/release/scripts/verify-standalone.sh" | tail -1

echo "== external-consumer proof for the /v5 Kubernetes module (go get @v5, GOWORK=off) =="
bash "$ROOT/release/orchestrator/verify-k8s-standalone.sh" | tail -1

echo "RELEASE-DRY-RUN OK: real artifacts to $REG, digest idempotency + immutability + resume proven, no production coordinate"
