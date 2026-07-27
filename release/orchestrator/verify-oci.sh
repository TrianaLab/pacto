#!/usr/bin/env bash
# Digest-aware immutability + crash-recovery check for an OCI ref.
# Prints exactly one of: absent | identical | adopt | conflict.
#
#   absent    -> the ref does not exist; the caller publishes then records.
#   identical -> the ref exists AND its digest matches the ledger-recorded (or
#                locally precomputed expected) digest; the caller skips (safe resume).
#   adopt     -> the ref exists with NO recorded digest yet (the push-before-record
#                crash window), BUT its provenance proves it is THIS transaction's
#                artifact (its OCI revision + version labels match the expected
#                source SHA + version). The caller records the remote digest and
#                skips re-pushing. Never fires without a provenance match.
#   conflict  -> the ref exists with a digest/provenance that does NOT match.
#                Exits non-zero (3) so the caller fails closed — never an overwrite.
#
#   verify-oci.sh <ref> [<expected-digest>] [<expect-revision> <expect-version>]
#
# <expected-digest>  the ledger-recorded digest (resume) OR a locally precomputed
#                    content digest (content-addressed artifacts). Empty on a first
#                    publish before anything is recorded.
# <expect-revision>  org.opencontainers.image.revision the artifact must carry to be
#                    adoptable in the crash window (the transaction source SHA).
# <expect-version>   org.opencontainers.image.version it must carry (the release version).
#
# With no expected digest AND no provenance match, an existing ref is a conflict:
# we never overwrite or blindly adopt an occupied immutable tag.
set -euo pipefail
REF="${1:?oci ref required}"
EXPECT="${2:-}"
EXPECT_REV="${3:-}"
EXPECT_VER="${4:-}"

# crane over a plain-http localhost registry needs --insecure, else it hangs on a
# TLS handshake against the http port. A no-op flag for real (https) registries.
craneflags() { case "$1" in localhost*|127.0.0.1*) printf -- '--insecure';; esac; }
# run <cmd...>: bounded so a wedged registry can never hang the release.
run() { if command -v timeout >/dev/null 2>&1; then timeout 60 "$@"; else "$@"; fi; }
digest() {
  # shellcheck disable=SC2046  # $(craneflags) intentionally unquoted: empty => no arg.
  if command -v crane >/dev/null 2>&1; then run crane digest $(craneflags "$1") "$1" 2>/dev/null
  elif command -v oras  >/dev/null 2>&1; then run oras manifest fetch --descriptor "$1" 2>/dev/null | jq -r .digest
  else docker manifest inspect "$1" >/dev/null 2>&1 && docker buildx imagetools inspect "$1" --format '{{.Manifest.Digest}}' 2>/dev/null; fi
}
# label <ref> <key> -> the image config label value ("" if unavailable/no crane).
label() {
  command -v crane >/dev/null 2>&1 || { printf ''; return; }
  # shellcheck disable=SC2046
  run crane config $(craneflags "$1") "$1" 2>/dev/null | jq -r --arg k "$2" '(.config.Labels[$k]) // (.config.labels[$k]) // ""' 2>/dev/null || printf ''
}

remote="$(digest "$REF" || true)"
if [ -z "$remote" ]; then echo absent; exit 0; fi
if [ -n "$EXPECT" ] && [ "$remote" = "$EXPECT" ]; then echo identical; exit 0; fi
# Crash-window adoption: nothing recorded/precomputed matched, but the artifact's
# own provenance proves it is this transaction's — adopt instead of conflicting.
if [ -z "$EXPECT" ] && [ -n "$EXPECT_REV" ]; then
  rev="$(label "$REF" org.opencontainers.image.revision)"
  ver="$(label "$REF" org.opencontainers.image.version)"
  if [ -n "$rev" ] && [ "$rev" = "$EXPECT_REV" ] && [ "$ver" = "$EXPECT_VER" ]; then
    echo adopt; exit 0
  fi
fi
echo conflict
echo "::error::${REF} already exists at ${remote}${EXPECT:+ but expected ${EXPECT}}${EXPECT_REV:+ (revision/version provenance mismatch vs ${EXPECT_REV}/${EXPECT_VER})} — refusing to overwrite an immutable version" >&2
exit 3
