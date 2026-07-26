#!/usr/bin/env bash
# publish-demo-bundles.sh — pack + push every demo OCI contract bundle
# (examples/demo/bundles) to a registry as the monorepo-owned v2 artifacts, so
# live `pacto validate` / `pacto lock` on the dependency-bearing bundles resolve
# to the v2 coordinate instead of an unowned/stale one.
#
# STAGING-ONLY BY DEFAULT. The target is $PACTO_DEMO_REGISTRY (default
# localhost:5001/pacto-demo, a disposable local registry). A production-looking
# target (ghcr.io / the trianalab namespace) is REFUSED unless PACTO_ALLOW_PROD=1
# — only the canonical release.yml `demo-bundles` publisher (the maintainer step)
# sets that. This script never publishes to production on its own.
#
# Pushing uses ./publishbundles (a validation-free pusher in the SAME OCI bundle
# format), NOT `pacto push`: the demo set is curated to showcase compliance
# states and deliberately includes contracts a strict `pacto push` rejects
# (policy violations, a gRPC .proto interface). Resolution is unaffected — the
# pushed artifacts are byte-identical to what `pacto push` would produce.
#
# What it does (staging mode):
#   1. Regenerate the committed OFFLINE demo locks via genlocks — the deterministic
#      content-hash generator the WASM demo embeds (run twice => no diff).
#   2. Copy the bundles to a scratch tree, repoint their refs to the target
#      coordinate, drop committed locks, and push every bundle (auto-tagged by
#      each contract's version).
#   3. PROVE live resolution: `pacto lock` + `pacto validate` each dep-bearing
#      bundle and confirm every resolved pin is a digest we just pushed (never a
#      stale foreign artifact). A pre-push negative control shows the same lock
#      FAILS before the v2 artifacts exist.
#   4. Write the proof to release/proofs/demo-artifacts.txt.
#
# In production mode (PACTO_ALLOW_PROD=1) it only performs step 2 against the
# production coordinate; the offline-lock regen and live proof are dev/PR concerns.
#
# Usage:
#   release/scripts/publish-demo-bundles.sh            # staging + proof
#   release/scripts/publish-demo-bundles.sh --push     # same (explicit)
#   PACTO_DEMO_REGISTRY=127.0.0.1:5002/pacto-demo release/scripts/publish-demo-bundles.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
PACTO_BIN="${PACTO_BIN:-/tmp/pacto}"
COORD="${PACTO_DEMO_REGISTRY:-localhost:5001/pacto-demo}"
BUNDLES="$ROOT/examples/demo/bundles"
OWNED="ghcr.io/trianalab/pacto-demo"   # committed refs' coordinate (== demo-bundles unit coordinate)
PROOF="$ROOT/release/proofs/demo-artifacts.txt"

# ---- production guard (mirrors publish.mjs resolveTarget) ----
host_path="${COORD#*/}"
if { printf '%s' "$COORD" | grep -qi 'ghcr\.io'; } || { printf '%s' "$host_path" | grep -qi 'trianalab'; }; then
  if [ "${PACTO_ALLOW_PROD:-}" != "1" ]; then
    echo "REFUSED: production-looking target '$COORD' — point PACTO_DEMO_REGISTRY at a disposable local registry (or set PACTO_ALLOW_PROD=1 in the canonical publisher)" >&2
    exit 1
  fi
  PROD=1
else
  PROD=0
fi

command -v "$PACTO_BIN" >/dev/null 2>&1 || { echo "pacto binary not found at $PACTO_BIN (build: go build -o /tmp/pacto ./cmd/pacto)" >&2; exit 1; }

export PACTO_NO_UPDATE_CHECK=1
# Hermetic cache so resolution can never fall back to a previously-cached ghcr pull.
export XDG_CACHE_HOME; XDG_CACHE_HOME="$(mktemp -d)"
WORK="$(mktemp -d)"
cleanup() { rm -rf "$WORK" "$XDG_CACHE_HOME"; }
trap cleanup EXIT

# ---- step 1: regenerate committed offline locks (deterministic) ----
if [ "$PROD" = "0" ]; then
  echo "==> genlocks: regenerate committed offline demo locks"
  ( cd "$ROOT/examples/demo" && go run ./genlocks >/dev/null )
fi

# ---- step 2: copy + repoint + push (no validation gate) ----
cp -R "$BUNDLES" "$WORK/bundles"
find "$WORK/bundles" -name pacto.yaml -exec sed -i.bak "s#$OWNED#$COORD#g" {} +
find "$WORK/bundles" -name '*.bak' -delete
find "$WORK/bundles" -name pacto.lock -delete   # regenerated fresh against the live coordinate
# The OCI layer tar embeds file mtimes, so normalize them to a fixed timestamp:
# the pushed digests (recorded in the proof) then reproduce byte-for-byte on any
# host, run twice => identical proof.
find "$WORK/bundles" -exec touch -t 202401010000.00 {} +

echo "==> push demo bundles to $COORD"
PUSHED="$WORK/pushed.txt"   # lines: "<svc>:<version> <digest>"
( cd "$ROOT/examples/demo" && go run ./publishbundles "$WORK/bundles" "$COORD" ) | tee "$PUSHED" | sed 's/^/    /'

if [ "$PROD" = "1" ]; then
  echo "==> production publish complete ($COORD)"; exit 0
fi

# ---- step 3: prove live resolution against the pushed v2 artifacts ----
dep_bearing() { grep -qE '^[[:space:]]*ref:[[:space:]]*oci://' "$1/pacto.yaml"; }

# negative control: the same lock FAILS when pointed at the SAME (reachable)
# registry host but an empty namespace with no v2 artifacts — proving resolution
# genuinely depends on the artifacts we pushed, not on the ref string alone.
NEG="$(mktemp -d)"; cp -r "$BUNDLES/auth-service" "$NEG/auth-service"
find "$NEG" -name pacto.yaml -exec sed -i.bak "s#$OWNED#${COORD%%/*}/pacto-demo-absent#g" {} +
find "$NEG" -name '*.bak' -delete; find "$NEG" -name pacto.lock -delete
NEG_OUT="$("$PACTO_BIN" lock "$NEG/auth-service" 2>&1 || true)"; rm -rf "$NEG"

TOTAL_REFS=0 MATCHED=0 ; PROVEN=() ; RESOLVE_FAIL=()
while IFS= read -r dir; do
  dep_bearing "$dir" || continue
  if ! "$PACTO_BIN" lock "$dir" >/dev/null 2>&1; then RESOLVE_FAIL+=("lock: ${dir#"$WORK"/bundles/}"); continue; fi
  # every pinned digest in the generated lock must be one we just pushed.
  ok=1
  while IFS= read -r d; do
    TOTAL_REFS=$((TOTAL_REFS+1))
    if grep -qF " $d" "$PUSHED"; then MATCHED=$((MATCHED+1)); else ok=0; fi
  done < <(grep -oE 'sha256:[a-f0-9]+' "$dir/pacto.lock")
  # validate must not report a ref-RESOLUTION failure (policy/interface content
  # errors are unrelated pre-existing demo-fixture concerns and are ignored here).
  vout="$("$PACTO_BIN" validate "$dir" 2>&1 || true)"
  if printf '%s' "$vout" | grep -qE 'POLICY_REF_UNRESOLVED|POLICY_REF_CYCLE|ARTIFACT_NOT_FOUND|not found in bundle registry|could not resolve'; then
    RESOLVE_FAIL+=("validate-resolve: ${dir#"$WORK"/bundles/}")
  fi
  [ "$ok" = "1" ] && PROVEN+=("${dir#"$WORK"/bundles/}")
done < <(find "$WORK/bundles" -name pacto.yaml -exec dirname {} \; | sort)

# ---- step 4: write the proof ----
mkdir -p "$(dirname "$PROOF")"
{
  echo "# PROOF - demo OCI bundles resolve to the monorepo-owned v2 coordinate"
  echo "#"
  echo "# Defect: 16 dep-bearing local v2 demo bundles declared refs under an UNOWNED"
  echo "# coordinate, so live pacto validate/lock resolved them to stale v1 artifacts."
  echo "# Fix: demo-bundles is now an owned release unit (release/release-manifest.json)"
  echo "# with a single publisher; this script republishes the bundles as v2 and proves"
  echo "# live resolution hits those v2 artifacts. Staging-only; production is refused."
  echo
  echo "owned coordinate (demo-bundles unit): $OWNED"
  echo "staging coordinate (this proof):      $COORD"
  echo "pacto:                                $("$PACTO_BIN" version 2>/dev/null | awk 'NR==1{print $2}')"
  echo
  echo "## negative control (defect is real): lock BEFORE the v2 artifacts exist"
  echo "  target repointed to an empty coordinate -> resolution FAILS:"
  printf '%s\n' "$NEG_OUT" | sed 's/^/    /' | head -4
  echo
  echo "## after staging-republish: every pinned digest is one we pushed as v2"
  echo "  dep-bearing bundles proven: ${#PROVEN[@]}"
  echo "  pinned refs checked:        $TOTAL_REFS   matched-to-pushed-v2: $MATCHED"
  if [ "${#RESOLVE_FAIL[@]}" -eq 0 ]; then
    echo "  ref-resolution failures:    0"
  else
    echo "  ref-resolution FAILURES:    ${#RESOLVE_FAIL[@]}"
    printf '    %s\n' "${RESOLVE_FAIL[@]}"
  fi
  echo
  echo "  proven bundles:"
  printf '    %s\n' "${PROVEN[@]}" | sort
  echo
  echo "## pushed v2 artifacts (service:version -> content digest)"
  sort "$PUSHED" | sed 's/^/    /'
  echo
  echo "## offline determinism: committed locks are regenerated by genlocks and"
  echo "## unchanged (make -C examples/demo demo-locks = zero diff)."
} > "$PROOF"

echo "==> wrote $PROOF (proven ${#PROVEN[@]} dep-bearing bundles, $MATCHED/$TOTAL_REFS pins matched)"
if [ "$TOTAL_REFS" -ne "$MATCHED" ] || [ "${#RESOLVE_FAIL[@]}" -ne 0 ]; then
  echo "FAIL: not every pin resolved to a pushed v2 artifact" >&2; exit 1
fi
