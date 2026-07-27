#!/usr/bin/env bash
# Immutable Go module tag publisher.
# Creates <tag> -> <sha> on origin. Fail-closed: an existing tag that points at a
# different commit is an immutable-version violation; an existing tag already at
# <sha> is an idempotent no-op (safe resume). Shared by release.yml + the dry-run
# (ORIGIN selects the target: origin for prod, a local clone for staging).
set -euo pipefail
TAG="${1:?tag required}"
SHA="${2:?sha required}"
ORIGIN="${PACTO_TAG_REMOTE:-origin}"

# Both the direct ref (lightweight tag) and the peeled ref (annotated tag).
# Portable across bash 3.2 (macOS) + 4+; mapfile is bash 4-only.
refs=""
while IFS= read -r line; do [ -n "$line" ] && refs="${refs}${line} "; done < <(
  git ls-remote --tags "$ORIGIN" "refs/tags/${TAG}" "refs/tags/${TAG}^{}" | awk '{print $1}')
if [ -n "$refs" ]; then
  for r in $refs; do
    if [ "$r" = "$SHA" ]; then echo "tag ${TAG} already at ${SHA} — nothing to do"; exit 0; fi
  done
  echo "::error::tag ${TAG} already exists at ${refs}, expected ${SHA} — refusing to move an immutable tag"
  exit 1
fi
git tag "${TAG}" "${SHA}"
git push "${ORIGIN}" "${TAG}"
echo "tagged ${TAG} -> ${SHA}"
