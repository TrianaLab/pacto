#!/usr/bin/env bash
# Integration test for the version command. Runs the REAL
# `npm run release:version` in a throwaway clone with actual pending changesets and
# proves it consumes them, bumps versions, and emits a ready:true transaction that
# detect.mjs acts on — and that a feature PR with unconsumed changesets still
# publishes nothing. Not a synthetic decideRelease fixture. Assumes node_modules
# is present in the repo root (the CI job runs npm ci first).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
WORK="$(mktemp -d)"; trap 'rm -rf "$WORK"' EXIT
CLONE="$WORK/clone"

fail() { echo "FAIL: $*" >&2; exit 1; }
eq() { [ "$1" = "$2" ] || fail "$3: got '$1' want '$2'"; }

git clone -q "$ROOT" "$CLONE"
# node_modules is gitignored; reuse the root's (the CI job installed it).
[ -d "$ROOT/node_modules" ] || fail "root node_modules missing (run npm ci first)"
ln -s "$ROOT/node_modules" "$CLONE/node_modules"
cd "$CLONE"

# Baseline previous versions (pre-bump), from the committed manifest.
prev_core="$(jq -r '.units.core.version' release/release-manifest.json)"
prev_k8s="$(jq -r '.units["k8s-module"].version' release/release-manifest.json)"

# --- A: detect must decide correctly for THIS commit's committed transaction. ---
# detect.mjs fires ONLY when a ready transaction is NEWLY introduced — its
# transactionId differs from HEAD^ (the changed-in-commit guard). So the correct
# decision depends on which commit the test runs on:
#   - feature merge / post-release commit: transaction unchanged vs HEAD^ -> release=false
#   - the version-PR-merge commit that introduces the ready transaction: release=true
#     (that is the intended release trigger, not a failure).
# Mirror that guard so this passes on EVERY commit — including the release commit
# itself, where a blanket release=false assertion wrongly failed and reddened main.
node release/orchestrator/detect.mjs > "$WORK/decide.out" 2>/dev/null || true
head_ready="$(jq -r '.ready // false' release/release-transaction.json 2>/dev/null || echo false)"
head_tid="$(jq -r '.transactionId // ""' release/release-transaction.json 2>/dev/null || echo '')"
prev_tid="$(git show HEAD^:release/release-transaction.json 2>/dev/null | jq -r '.transactionId // ""' 2>/dev/null || echo '')"
if [ "$head_ready" = "true" ] && [ -n "$head_tid" ] && [ "$head_tid" != "$prev_tid" ]; then
  grep -qx "release=true" "$WORK/decide.out" || fail "version-PR-merge (new ready transaction) detect must be release=true"
  echo "  A: version-PR-merge commit (ready transaction introduced) -> release=true"
else
  grep -qx "release=false" "$WORK/decide.out" || fail "feature-PR / post-release detect must be release=false"
  echo "  A: feature PR / post-release (unchanged committed transaction) publishes nothing"
fi

# stage_changeset <dir> <bump> — replace the repo's pending changesets with one
# controlled entry, so the bump is deterministic (config.json + README.md stay).
stage_changeset() {
  find "$1/.changeset" -name '*.md' ! -name 'README.md' -delete
  cat > "$1/.changeset/test-bump.md" <<MD
---
"@pacto/core": $2
---

Test: exercise the real release:version transaction path.
MD
}

# --- B: run the REAL version command with a pending minor core changeset. ---
# Minor, not major: a major bump is REFUSED until the module path is renamed to
# match (case D below), and every invariant B checks — consumption, the fixed
# group, the transaction, manifestSha — is bump-size independent.
stage_changeset . minor
npm run release:version >/dev/null 2>&1 || fail "npm run release:version errored"

# changeset consumed
[ -f .changeset/test-bump.md ] && fail "changeset was not consumed"
# versions bumped (core minor: prev major . prev minor +1 . 0)
new_core="$(jq -r '.units.core.version' release/release-manifest.json)"
want_core="${prev_core%%.*}.$(( $(echo "$prev_core" | cut -d. -f2) + 1 )).0"
eq "$new_core" "$want_core" "core version bump"
# k8s unchanged (core-only changeset)
eq "$(jq -r '.units["k8s-module"].version' release/release-manifest.json)" "$prev_k8s" "k8s version unchanged"

# transaction is ready with exactly the core fixed group
eq "$(jq -r '.ready' release/release-transaction.json)" "true" "transaction ready"
got_units="$(jq -rc '.changedUnits | sort' release/release-transaction.json)"
eq "$got_units" '["cli","core","dashboard-contract-bundle","dashboard-image","demo-bundles","demo-compose"]' "changedUnits"
eq "$(jq -r '.previousVersions.core' release/release-transaction.json)" "$prev_core" "previousVersions.core"
eq "$(jq -r '.newVersions.core' release/release-transaction.json)" "$new_core" "newVersions.core"
# manifestSha matches sha256 of the stable newVersions map
want_sha="$(node -e 'const c=require("crypto");const m=JSON.parse(require("fs").readFileSync("release/release-manifest.json"));const nv=Object.fromEntries(Object.entries(m.units).map(([u,v])=>[u,v.version]));const st=v=>Array.isArray(v)?v.map(st):(v&&typeof v=="object"?Object.fromEntries(Object.keys(v).sort().map(k=>[k,st(v[k])])):v);process.stdout.write(c.createHash("sha256").update(JSON.stringify(st(nv))).digest("hex"))')"
eq "$(jq -r '.manifestSha' release/release-transaction.json)" "$want_sha" "manifestSha"

# detect.mjs acts on it: a ready transaction newly introduced -> release=true
node release/orchestrator/detect.mjs > "$WORK/decide2.out" 2>/dev/null || true
grep -qx "release=true" "$WORK/decide2.out" || fail "version-PR detect must be release=true"
echo "  B: real release:version -> ready transaction, detect release=true"

# --- C: deterministic — the same changesets produce a byte-identical transaction
# in an independent run (no clock/random; item 1 "second invocation byte-identical").
cp release/release-transaction.json "$WORK/txn1.json"
C2="$WORK/clone2"; git clone -q "$ROOT" "$C2"; ln -s "$ROOT/node_modules" "$C2/node_modules"
stage_changeset "$C2" minor
( cd "$C2" && npm run release:version >/dev/null 2>&1 )
diff -q "$WORK/txn1.json" "$C2/release/release-transaction.json" >/dev/null \
  || fail "transaction not deterministic across independent runs"
echo "  C: same changesets -> byte-identical transaction across independent runs"

# --- D: a major bump is REFUSED while the module path still carries the old major. ---
# Go binds a module's version to its path (/vN carries only vN.y.z), and a major
# changeset moves the version without moving the path — the rename is a separate,
# deliberate commit. Emitting the plan anyway wrote `github.com/trianalab/pacto/v3
# v4.0.0` into the operator's go.mod, a require go refuses to parse, discovered only
# after the Version PR existed. release:version must fail loudly instead, naming the
# rename. Run in a third clone so B and C's consumed state is untouched.
C3="$WORK/clone3"; git clone -q "$ROOT" "$C3"; ln -s "$ROOT/node_modules" "$C3/node_modules"
stage_changeset "$C3" major
if ( cd "$C3" && npm run release:version ) > "$WORK/major.out" 2>&1; then
  fail "a major core bump was accepted while the module path still carries the old major"
fi
grep -q 'refuses to emit' "$WORK/major.out" \
  || fail "major bump failed for the wrong reason: $(tail -3 "$WORK/major.out")"
grep -q 'Rename it to github.com/trianalab/pacto/v4' "$WORK/major.out" \
  || fail "the refusal does not name the rename that unblocks it"
# The refusal has to land BEFORE the operator go.mod is touched: a require go cannot
# parse is worse than no bump at all.
git -C "$C3" diff --quiet -- integrations/kubernetes/go.mod \
  || fail "operator go.mod was modified by a refused major bump"
echo "  D: major core bump refused, module-path rename named, operator go.mod untouched"

echo "RELEASE-VERSION-TEST OK"
