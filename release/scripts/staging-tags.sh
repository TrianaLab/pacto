#!/usr/bin/env bash
# Local git tags staged on HEAD for the standalone-consumer checks — and put back
# exactly as they were found. Source this; it defines two functions and runs nothing.
#
# Those checks resolve a Go module out of THIS checkout by pointing go at a tag, so
# the tag has to name HEAD: the tree the release is about to publish, not the tree
# the last release published. Between releases the names they stage —
# `integrations/kubernetes/v5.4.1`, `v3.3.1` — are REAL published tags, and any
# maintainer who has run `git fetch --tags` holds them locally, pointing at their
# actual release commits. Staging therefore has to force-move a tag that already
# exists, and both callers used to clean up by deleting it: a check that reads
# nothing and publishes nothing was quietly destroying release history in the
# maintainer's clone. (Never in CI, which clones fresh and throws the clone away —
# which is why it went unnoticed.)
#
# So remember where each tag pointed and restore that ref on the way out, deleting
# only the tags we created ourselves. `update-ref` restores the ref's exact object,
# so an annotated tag comes back annotated. Callers must trap restore_staged_tags
# on EXIT, not call it on the happy path — a check that fails mid-way is exactly
# when the tags most need putting back.

# _staged accumulates "<sha|-> <tag>" lines, oldest first. A plain string rather
# than an array: this is sourced by scripts that may run under bash 3.2, where
# expanding an empty array under `set -u` is an error.
_staged_repo=""
_staged=""

# stage_tag <repo> <tag> — force <tag> onto HEAD, remembering where it pointed.
stage_tag() {
  local sha
  _staged_repo="$1"
  sha="$(git -C "$1" rev-parse -q --verify "refs/tags/$2" 2>/dev/null || true)"
  _staged="${_staged}${sha:--} $2
"
  git -C "$1" tag -f "$2" HEAD >/dev/null 2>&1
}

# restore_staged_tags — restore every staged tag to the object it named, or delete
# the ones that did not exist before. Idempotent, and safe to trap unconditionally.
restore_staged_tags() {
  [ -n "$_staged" ] || return 0
  local sha tag
  while read -r sha tag; do
    [ -n "$tag" ] || continue
    if [ "$sha" = "-" ]; then
      git -C "$_staged_repo" update-ref -d "refs/tags/$tag" >/dev/null 2>&1 || true
    else
      git -C "$_staged_repo" update-ref "refs/tags/$tag" "$sha" >/dev/null 2>&1 || true
    fi
  done <<EOF
$_staged
EOF
  _staged=""
}
