#!/usr/bin/env bash
# Real release simulation (release/DESIGN-release-safety.md item 8). Builds the
# real release artifacts and pushes them to a DISPOSABLE local registry via the
# SAME shared adapters production uses (build-cli.sh, verify-oci-absent.sh,
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

# 0. disposable local registry
if ! curl -sf "http://$REG/v2/" >/dev/null 2>&1; then
  docker rm -f pacto-dryrun-reg >/dev/null 2>&1 || true
  docker run -d --name pacto-dryrun-reg -p 5001:5000 registry:2 >/dev/null
  OWNED_REG=1
  for _ in $(seq 1 30); do curl -sf "http://$REG/v2/" >/dev/null 2>&1 && break; sleep 1; done
fi

# 1. plan + a disposable version that can never collide with a real release
node "$ROOT/release/scripts/build-release-plan.mjs" >/dev/null
CORE="$(python3 -c "import json;print(json.load(open('$ROOT/release/release-plan.json'))['groups']['core']['version'])")"
DV="0.0.0-dryrun"
SHA="$(git -C "$ROOT" rev-parse HEAD)"
OPIMG="$REG/pacto-operator/pacto-controller:$DV"

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

echo "== package + push the chart =="
helm package "$ROOT/integrations/kubernetes/charts/pacto-operator" --version "$DV" --app-version "$DV" -d "$WORK" >/dev/null
CHART="$(ls "$WORK"/pacto-operator-*.tgz)"
helm push "$CHART" "oci://$REG/pacto-operator/charts" --plain-http >/dev/null 2>&1 \
  || helm push "$CHART" "oci://$REG/pacto-operator/charts" >/dev/null
echo "   chart pushed"

echo "== IDEMPOTENCY: an unchanged artifact re-verifies to the same digest (skip) =="
[ "$(digest "$OPIMG")" = "$OPDIGEST" ] && echo "   digest unchanged -> would skip" || { echo "digest drifted"; exit 1; }

echo "== IMMUTABILITY: the guard refuses an already-occupied tag (fail-closed) =="
if bash "$ROOT/release/orchestrator/verify-oci-absent.sh" "$OPIMG" >/dev/null 2>&1; then
  echo "   FAIL: verify-oci-absent passed on an occupied tag"; exit 1
fi
echo "   verify-oci-absent correctly refused the occupied tag"

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
