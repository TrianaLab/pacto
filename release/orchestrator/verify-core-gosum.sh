#!/usr/bin/env bash
# Pre-tag gate: the operator's committed go.sum must carry the hash of the core module
# AS IT WILL BE TAGGED, which is the tree at HEAD.
#
# apply-release-plan.mjs (section 8) mints that hash on the Version PR, from the tree it
# generated. Nothing re-mints it if the Version PR's tree changes afterwards, and on
# 2026-10-19 it did: a dependabot PR was auto-merged with GITHUB_TOKEN (a push that
# triggers no workflow, so the changesets job never regenerated the Version PR), main
# was then merged into the Version PR by hand, and v4.0.0 was tagged with a
# .github/workflows/security.yml the staged hash had never seen. The operator image
# build failed with a checksum MISMATCH after both tags had been pushed, which left
# integrations/kubernetes/v6.0.0 a ghost.
#
# So this recomputes the hash from HEAD and compares. CI runs it on the PR merge ref,
# which is the tree the squash merge will produce; release.yml's detect runs it on the
# exact source commit before anything is tagged or pushed.
#
# Only meaningful while the pinned version is unpublished, or published AT HEAD (a
# recovery run). A version already tagged at another commit is the published module,
# and TestOperatorGoSumMatchesThePublishedCoreModule compares it with sum.golang.org.
set -euo pipefail
ROOT="$(git rev-parse --show-toplevel)"
ORIGIN="${PACTO_TAG_REMOTE:-origin}"
GOMOD="$ROOT/integrations/kubernetes/go.mod"
GOSUM="$ROOT/integrations/kubernetes/go.sum"

read -r MODULE VERSION < <(awk '$1 ~ /^github\.com\/trianalab\/pacto\/v[0-9]+$/ { print $1, $2; exit }' "$GOMOD")
[ -n "${VERSION:-}" ] || { echo "::error::$GOMOD requires no github.com/trianalab/pacto/vN; this gate has nothing to check"; exit 1; }

committed="$(awk -v m="$MODULE" -v v="$VERSION" '$1 == m && $2 == v { print $3 }' "$GOSUM")"
if [ -z "$committed" ]; then
  # Absent between a module-path rename and its major release, by design; otherwise
  # TestOperatorGoSumCoversThePinnedCoreVersion owns the failure.
  echo "go.sum carries no $MODULE $VERSION h1: line; nothing to verify"
  exit 0
fi

HEAD_SHA="$(git rev-parse HEAD)"
tagged="$(git ls-remote --tags "$ORIGIN" "refs/tags/$VERSION" "refs/tags/$VERSION^{}" | awk '{ print $1 }')"
if [ -n "$tagged" ] && ! grep -qx "$HEAD_SHA" <<<"$tagged"; then
  echo "$MODULE $VERSION is already published at another commit; nothing to verify against HEAD"
  exit 0
fi

tmp="$(mktemp -d)"
trap 'chmod -R u+w "$tmp" 2>/dev/null; rm -rf "$tmp"' EXIT

# Serve HEAD's tree as $VERSION from a scratch repository, so the real repo's tags are
# never touched. A parentless commit also works from a shallow checkout. go builds the
# module zip from the tagged TREE alone, so the commit's metadata never reaches the hash.
staging="$(GIT_AUTHOR_NAME=pacto-release GIT_AUTHOR_EMAIL=release@pacto.invalid \
  GIT_COMMITTER_NAME=pacto-release GIT_COMMITTER_EMAIL=release@pacto.invalid \
  GIT_AUTHOR_DATE=1970-01-01T00:00:00Z GIT_COMMITTER_DATE=1970-01-01T00:00:00Z \
  git commit-tree "HEAD^{tree}" -m 'release staging')"
git init -q --bare "$tmp/src.git"
git push -q "$tmp/src.git" "$staging:refs/tags/$VERSION"
printf '[url "file://%s"]\n\tinsteadOf = https://github.com/trianalab/pacto\n' "$tmp/src.git" > "$tmp/gitconfig"

( cd "$tmp" && env GIT_CONFIG_GLOBAL="$tmp/gitconfig" GIT_CONFIG_SYSTEM=/dev/null \
  GOWORK=off GOFLAGS= GOPROXY=direct GOPRIVATE='github.com/trianalab/*' GONOSUMDB='github.com/trianalab/*' \
  GOMODCACHE="$tmp/modcache" bash "$ROOT"/release/scripts/retry.sh go mod download -json "$MODULE@$VERSION" ) > "$tmp/download.json"
computed="$(jq -r '.Sum // empty' "$tmp/download.json")"
[ -n "$computed" ] || { echo "::error::could not compute the checksum of $MODULE $VERSION from HEAD"; exit 1; }

if [ "$computed" != "$committed" ]; then
  echo "::error::integrations/kubernetes/go.sum records $committed for $MODULE $VERSION, but the tree at HEAD hashes to $computed."
  echo "Tagging this commit would ship an operator whose standalone build fails with a checksum mismatch."
  echo "The Version PR is stale: main moved under it (a merge of main into the branch, or a push that never re-ran the changesets job)."
  echo "Fix: set that go.sum line to $computed on the Version PR branch, or regenerate the Version PR from current main."
  exit 1
fi
echo "go.sum matches the tree at HEAD for $MODULE $VERSION ($computed)"
