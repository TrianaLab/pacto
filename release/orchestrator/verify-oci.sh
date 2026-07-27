#!/usr/bin/env bash
# Digest-aware immutability check for an OCI ref.
# Prints exactly one of: absent | identical | conflict.
#
#   absent    -> the ref does not exist; the caller publishes then records.
#   identical -> the ref exists AND its digest matches the ledger-recorded digest
#                for this transaction; the caller skips (safe resume).
#   conflict  -> the ref exists with a digest that does NOT match the ledger
#                (a different transaction, hand-push, or a would-be overwrite).
#                Exits non-zero so the caller fails closed — never an overwrite.
#
#   verify-oci.sh <ref> [<expected-digest-from-ledger>]
#
# With no expected digest (a fresh publish where nothing was recorded yet) an
# existing ref is a conflict: we never overwrite an occupied immutable tag.
set -euo pipefail
REF="${1:?oci ref required}"
EXPECT="${2:-}"

digest() {
  if command -v crane >/dev/null 2>&1; then crane digest "$1" 2>/dev/null
  elif command -v oras  >/dev/null 2>&1; then oras manifest fetch --descriptor "$1" 2>/dev/null | jq -r .digest
  else docker manifest inspect "$1" >/dev/null 2>&1 && docker buildx imagetools inspect "$1" --format '{{.Manifest.Digest}}' 2>/dev/null; fi
}

remote="$(digest "$REF" || true)"
if [ -z "$remote" ]; then echo absent; exit 0; fi
if [ -n "$EXPECT" ] && [ "$remote" = "$EXPECT" ]; then echo identical; exit 0; fi
echo conflict
echo "::error::${REF} already exists at ${remote}${EXPECT:+ but the ledger recorded ${EXPECT}} — refusing to overwrite an immutable version" >&2
exit 3
