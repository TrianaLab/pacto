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

# --- A: a FEATURE PR carries an unconsumed changeset -> no ready transaction. ---
# The committed transaction (before any version command) must publish nothing.
eq "$(jq -r '.ready' release/release-transaction.json)" "false" "feature-PR committed txn ready"
node release/orchestrator/detect.mjs > "$WORK/decide.out" 2>/dev/null || true
grep -qx "release=false" "$WORK/decide.out" || fail "feature-PR detect must be release=false"
echo "  A: feature PR (unconsumed changeset) publishes nothing"

# --- B: run the REAL version command with a pending major core changeset. ---
# Consume ONLY a controlled changeset: drop the repo's pending entries so the
# bump is deterministic (config.json + README.md stay).
find .changeset -name '*.md' ! -name 'README.md' -delete
cat > .changeset/test-major.md <<'MD'
---
"@pacto/core": major
---

Test: exercise the real release:version transaction path.
MD
npm run release:version >/dev/null 2>&1 || fail "npm run release:version errored"

# changeset consumed
[ -f .changeset/test-major.md ] && fail "changeset was not consumed"
# versions bumped (core major: prev major +1 . 0 . 0)
new_core="$(jq -r '.units.core.version' release/release-manifest.json)"
want_core="$(( ${prev_core%%.*} + 1 )).0.0"
eq "$new_core" "$want_core" "core version bump"
# k8s unchanged (core-only changeset)
eq "$(jq -r '.units["k8s-module"].version' release/release-manifest.json)" "$prev_k8s" "k8s version unchanged"

# transaction is ready with exactly the core fixed group
eq "$(jq -r '.ready' release/release-transaction.json)" "true" "transaction ready"
got_units="$(jq -rc '.changedUnits | sort' release/release-transaction.json)"
eq "$got_units" '["cli","core","dashboard-contract-bundle","dashboard-image","demo-bundles"]' "changedUnits"
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
(
  cd "$C2"
  find .changeset -name '*.md' ! -name 'README.md' -delete
  cat > .changeset/test-major.md <<'MD'
---
"@pacto/core": major
---

Test: exercise the real release:version transaction path.
MD
  npm run release:version >/dev/null 2>&1
)
diff -q "$WORK/txn1.json" "$C2/release/release-transaction.json" >/dev/null \
  || fail "transaction not deterministic across independent runs"
echo "  C: same changesets -> byte-identical transaction across independent runs"

echo "RELEASE-VERSION-TEST OK"
