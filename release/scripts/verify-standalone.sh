#!/usr/bin/env bash
# Reproducibly prove the RELEASE state is standalone-consumable: after core is
# published at the NEXT version (computed from pending changesets — the version that
# CONTAINS pkg/evidence+pkg/finding), the operator module builds with GOWORK=off and
# NO replace. Non-invasive: local staging tag from the current tree + a process-scoped
# git config (never the user's global) + a throwaway module cache. No network, no publish.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
# NEXT core version = current @pacto/core version bumped by the strongest pending changeset.
NEXT="$(node -e '
const fs=require("fs"),path=require("path");
const cur=JSON.parse(fs.readFileSync(path.join(process.argv[1],"release/units/pacto-core/package.json"))).version.split(".").map(Number);
let bump="none";
for(const f of fs.readdirSync(path.join(process.argv[1],".changeset")).filter(f=>f.endsWith(".md")&&f!=="README.md")){
  const t=fs.readFileSync(path.join(process.argv[1],".changeset",f),"utf8");
  if(/"@pacto\/core":\s*major/.test(t))bump="major";
  else if(/"@pacto\/core":\s*minor/.test(t)&&bump!=="major")bump="minor";
  else if(/"@pacto\/core":\s*patch/.test(t)&&bump==="none")bump="patch";
}
let [a,b,c]=cur;
if(bump==="major"){a++;b=0;c=0}else if(bump==="minor"){b++;c=0}else if(bump==="patch"){c++}
process.stdout.write(`${a}.${b}.${c}`);
' "$ROOT")"
echo "next published core version (from pending changesets): v${NEXT}"
# Between releases there is no pending core bump, so NEXT equals the CURRENT core —
# which is already published. This check substitutes a local staging tag for the
# core module, so it must only run for a NOT-yet-published NEXT; substituting a tag
# for the published current version collides with its real go.sum checksum. Nothing
# to prove in that case (the current core was verified standalone at its own
# release), so skip. "Don't repin an unchanged core" is a release-DECISION guard
# (detect.mjs), not a standalone-build check.
if [ "$NEXT" = "$(node -e 'console.log(require("'"$ROOT"'/release/units/pacto-core/package.json").version)')" ]; then
  echo "note: no pending core bump — current core already published + proven standalone; skipping"
  exit 0
fi
git -C "$ROOT" tag -f "v${NEXT}" HEAD >/dev/null 2>&1   # reproducible local staging tag (deleted at end)
TMPGIT="$(mktemp)"; printf '[url "file://%s"]\n\tinsteadOf = https://github.com/trianalab/pacto\n' "$ROOT" > "$TMPGIT"
WORK="$(mktemp -d)"; mkdir -p "$WORK/op"; tar -C "$ROOT/integrations/kubernetes" --exclude=bin --exclude=.github --exclude=node_modules -cf - . | tar -C "$WORK/op" -xf -
perl -i -pe "s{github.com/trianalab/pacto/v3 v[0-9][0-9.]*}{github.com/trianalab/pacto/v3 v${NEXT}}" "$WORK/op/go.mod"
[ "$(grep -c '^replace' "$WORK/op/go.mod")" = "0" ] || { echo "ERROR: replace directive present in release state"; exit 1; }
cd "$WORK/op"
export GIT_CONFIG_GLOBAL="$TMPGIT" GIT_CONFIG_SYSTEM=/dev/null GOWORK=off GOFLAGS=-mod=mod \
       GOPROXY=direct GOPRIVATE='github.com/trianalab/*' GONOSUMDB='github.com/trianalab/*' GOMODCACHE="$(mktemp -d)"
echo "go mod download (external, GOWORK=off)..."; go mod download github.com/trianalab/pacto/v3
echo "go build ./... (standalone operator, no go.work, no replace)..."
go build ./... && go build -o /dev/null ./cmd
echo "STANDALONE-VERIFY OK: operator module builds against published core v${NEXT}, GOWORK=off, replace=0"
git -C "$ROOT" tag -d "v${NEXT}" >/dev/null 2>&1 || true
