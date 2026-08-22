#!/usr/bin/env bash
# Digest-aware immutability + crash-recovery check for an OCI ref.
# Prints exactly one of: absent | identical | adopt | conflict.
#
#   absent    -> the ref does not exist; the caller publishes then records.
#   identical -> the ref exists AND its digest matches the ledger-recorded (or
#                locally precomputed expected) digest; the caller skips (safe resume).
#   adopt     -> the ref exists with NO recorded digest yet (the push-before-record
#                crash window), BUT its identity proves it is THIS transaction's
#                artifact: either its OCI revision + version labels match the
#                expected source SHA + version, or it is a native Compose
#                application whose one compose-file layer digests to the expected
#                content. The caller records the remote digest and skips
#                re-pushing. Never fires without one of those matches.
#   conflict  -> the ref exists with a digest/provenance that does NOT match.
#                Exits non-zero (3) so the caller fails closed — never an overwrite.
#
#   verify-oci.sh <ref> [<expected-digest>] [<expect-revision> <expect-version>] \
#                       [<expect-content-digest>]
#
# <expected-digest>  the ledger-recorded digest (resume) OR a locally precomputed
#                    content digest (content-addressed artifacts). Empty on a first
#                    publish before anything is recorded.
# <expect-revision>  org.opencontainers.image.revision the artifact must carry to be
#                    adoptable in the crash window (the transaction source SHA).
# <expect-version>   org.opencontainers.image.version it must carry (the release version).
# <expect-content-digest>
#                    the digest of the artifact's ONE compose-file layer, for the
#                    publisher that writes neither a stable manifest digest nor
#                    provenance annotations. `docker compose publish` is the case: it
#                    stamps org.opencontainers.image.created into the manifest (so the
#                    manifest digest moves between two publishes of identical bytes)
#                    and emits no revision/version. Its single
#                    application/vnd.docker.compose.file+yaml layer, however, is the
#                    verbatim compose file, so sha256(the projected file) IS the
#                    artifact's content identity — computable before the push.
#                    Matched together with the rest of the native Compose identity,
#                    never on its own: bytes are not a type, and an artifact holding
#                    the same bytes under a foreign artifact/layer media type is one
#                    `docker compose -f oci://…` cannot run.
#
# With no expected digest AND no provenance/content match, an existing ref is a
# conflict: we never overwrite or blindly adopt an occupied immutable tag.
set -euo pipefail
REF="${1:?oci ref required}"
EXPECT="${2:-}"
EXPECT_REV="${3:-}"
EXPECT_VER="${4:-}"
EXPECT_CONTENT="${5:-}"

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
# label <ref> <key> -> the provenance value for key ("" if unavailable/no crane).
# Reads a docker-style config label first (images), then falls back to the OCI
# manifest annotations (helm charts carry org.opencontainers.image.* there, not in a
# config Labels block).
label() {
  command -v crane >/dev/null 2>&1 || { printf ''; return; }
  local v=""
  # shellcheck disable=SC2046
  v="$(run crane config $(craneflags "$1") "$1" 2>/dev/null | jq -r --arg k "$2" '(.config.Labels[$k]) // (.config.labels[$k]) // ""' 2>/dev/null || printf '')"
  if [ -n "$v" ]; then printf '%s' "$v"; return; fi
  # shellcheck disable=SC2046
  run crane manifest $(craneflags "$1") "$1" 2>/dev/null | jq -r --arg k "$2" '(.annotations[$k]) // ""' 2>/dev/null || printf ''
}
# The native Compose application's OCI identity, exactly as `docker compose publish`
# writes it. THE definition — publish-oci-unit.sh asserts through this script rather
# than keeping a second copy, because a second copy is how the post-push assertion
# and the adoption rule drift apart.
COMPOSE_ARTIFACT_TYPE='application/vnd.docker.compose.project'
COMPOSE_LAYER_TYPE='application/vnd.docker.compose.file+yaml'

# content <ref> -> the digest of the artifact's ONE compose-file layer, or "" unless
# the WHOLE native Compose identity holds: the manifest's artifactType, exactly one
# layer, and that layer's media type. A layer count and a digest are not an identity
# — any artifact can carry those bytes as one octet-stream layer under any artifact
# type, and it would have the same layer digest while being something Compose cannot
# run. Refusing to answer for anything else is what keeps this from adopting it.
content() {
  command -v crane >/dev/null 2>&1 || { printf ''; return; }
  # shellcheck disable=SC2046
  run crane manifest $(craneflags "$1") "$1" 2>/dev/null \
    | jq -r --arg a "$COMPOSE_ARTIFACT_TYPE" --arg l "$COMPOSE_LAYER_TYPE" \
        'if .artifactType == $a and (.layers | length) == 1 and .layers[0].mediaType == $l
         then .layers[0].digest else "" end' 2>/dev/null || printf ''
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
# Same crash window, for a publisher whose manifest digest is not reproducible and
# whose annotations carry no provenance: the native Compose application it published
# is the identity — the type, the one layer, its media type and its bytes together.
if [ -z "$EXPECT" ] && [ -n "$EXPECT_CONTENT" ]; then
  if [ "$(content "$REF")" = "$EXPECT_CONTENT" ]; then echo adopt; exit 0; fi
fi
echo conflict
echo "::error::${REF} already exists at ${remote}${EXPECT:+ but expected ${EXPECT}}${EXPECT_REV:+ (revision/version provenance mismatch vs ${EXPECT_REV}/${EXPECT_VER})}${EXPECT_CONTENT:+ (not a ${COMPOSE_ARTIFACT_TYPE} artifact with one ${COMPOSE_LAYER_TYPE} layer at ${EXPECT_CONTENT})} — refusing to overwrite an immutable version" >&2
exit 3
