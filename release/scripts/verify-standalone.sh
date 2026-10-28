#!/usr/bin/env bash
# Reproducibly prove the RELEASE state is standalone-consumable: after core is
# published at the NEXT version (computed from pending changesets — the version that
# CONTAINS pkg/evidence+pkg/finding), the operator module builds with GOWORK=off and
# NO replace. Non-invasive: local staging tag from the current tree + a process-scoped
# git config (never the user's global) + a throwaway module cache. No network, no publish.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
. "$ROOT/release/scripts/staging-tags.sh"
# NEXT core version = current @pacto/core version bumped by the strongest pending
# changeset naming @pacto/core OR any package in its `fixed` group: changesets bumps
# a fixed group as one, so an "@pacto/cli": minor changeset moves core too.
NEXT="$(node -e '
const fs=require("fs"),path=require("path"),root=process.argv[1];
const cur=JSON.parse(fs.readFileSync(path.join(root,"release/units/pacto-core/package.json"))).version.split(".").map(Number);
const cfg=JSON.parse(fs.readFileSync(path.join(root,".changeset/config.json")));
const group=new Set((cfg.fixed||[]).find(g=>g.includes("@pacto/core"))||["@pacto/core"]);
const rank={none:0,patch:1,minor:2,major:3};
let bump="none";
for(const f of fs.readdirSync(path.join(root,".changeset")).filter(f=>f.endsWith(".md")&&f!=="README.md")){
  const front=(fs.readFileSync(path.join(root,".changeset",f),"utf8").match(/^---\n([\s\S]*?)\n---/)||[,""])[1];
  for(const m of front.matchAll(/^\s*["\x27]?([^"\x27:]+)["\x27]?\s*:\s*(major|minor|patch)\s*$/gm)){
    if(group.has(m[1])&&rank[m[2]]>rank[bump])bump=m[2];
  }
}
let [a,b,c]=cur;
if(bump==="major"){a++;b=0;c=0}else if(bump==="minor"){b++;c=0}else if(bump==="patch"){c++}
process.stdout.write(`${a}.${b}.${c}`);
' "$ROOT")"
# --next prints the computed version and stops (the regression test drives this).
[ "${1:-}" = "--next" ] && { echo "$NEXT"; exit 0; }
# The core module path the operator go.mod requires, from the release plan
# (derived from the unit coordinate), so a /vN rename needs no edit here.
CORE_MODULE="$(node -e 'console.log(JSON.parse(require("fs").readFileSync(process.argv[1])).groups.kubernetes.goModPin.module)' "$ROOT/release/release-plan.json")"
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
# Everything below can fail — a build error, a proxy timeout, a ^C — and the only
# tag deletion used to be the last line of the script, so any of those leaked the
# staging tag into the maintainer's clone. Trap the restore before staging.
trap restore_staged_tags EXIT
stage_tag "$ROOT" "v${NEXT}"                            # reproducible local staging tag
TMPGIT="$(mktemp)"; printf '[url "file://%s"]\n\tinsteadOf = https://github.com/trianalab/pacto\n' "$ROOT" > "$TMPGIT"
WORK="$(mktemp -d)"; mkdir -p "$WORK/op"; tar -C "$ROOT/integrations/kubernetes" --exclude=bin --exclude=.github --exclude=node_modules -cf - . | tar -C "$WORK/op" -xf -
CORE_MODULE="$CORE_MODULE" NEXT="$NEXT" perl -i -pe 's{\Q$ENV{CORE_MODULE}\E v[0-9][0-9.]*}{$ENV{CORE_MODULE} v$ENV{NEXT}}' "$WORK/op/go.mod"
[ "$(grep -c '^replace' "$WORK/op/go.mod")" = "0" ] || { echo "ERROR: replace directive present in release state"; exit 1; }
cd "$WORK/op"
export GIT_CONFIG_GLOBAL="$TMPGIT" GIT_CONFIG_SYSTEM=/dev/null GOWORK=off GOFLAGS=-mod=mod \
       GOPROXY=https://proxy.golang.org,direct GOPRIVATE='github.com/trianalab/*' GONOSUMDB='github.com/trianalab/*' GOMODCACHE="$(mktemp -d)"
echo "go mod download (external, GOWORK=off)..."; bash "$ROOT"/release/scripts/retry.sh go mod download "$CORE_MODULE"
echo "go build ./... (standalone operator, no go.work, no replace)..."
go build ./... && go build -o /dev/null ./cmd
echo "STANDALONE-VERIFY OK: operator module builds against published core v${NEXT}, GOWORK=off, replace=0"
