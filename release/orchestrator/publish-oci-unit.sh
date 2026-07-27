#!/usr/bin/env bash
# Shared, crash-safe OCI publish adapter. THE single place both production
# (release.yml) and staging (dry-run.sh) publish an OCI artifact + record it in
# the durable per-unit ledger — only PACTO_LEDGER_REPO + coordinates + credentials
# differ. Two-phase so a crash between push and record is recoverable:
#
#   1. If the unit is already recorded complete -> verify the remote digest still
#      matches (resume): identical -> skip, else conflict -> fail closed.
#   2. Otherwise record a PLAN (the precomputed expected digest, when the caller
#      supplies one) BEFORE touching the registry.
#   3. verify the remote:
#        absent    -> run the push command, read the digest, (assert it equals the
#                     precomputed expected digest when given), record complete.
#        identical -> the remote already equals the expected digest — a crashed
#                     push or an idempotent re-run; record complete (if not yet) + skip.
#        adopt     -> the remote carries no recorded digest but its OCI provenance
#                     (revision + version labels) proves it is THIS transaction's
#                     artifact (image crash window); record its digest + skip.
#        conflict  -> remote differs / unattributable -> fail closed, never overwrite.
#
# Identity inputs (how recovery adopts a remote artifact — pick per artifact type):
#   PACTO_EXPECT_DIGEST    precomputed content digest (content-addressed artifacts:
#                          contract bundle, demo bundle, chart). Enables the PLAN
#                          record + digest-exact adoption.
#   PACTO_EXPECT_REVISION  org.opencontainers.image.revision the remote must carry
#   PACTO_EXPECT_VERSION   org.opencontainers.image.version  the remote must carry
#                          (OCI images built by buildx, where the digest is only
#                          known post-push -> provenance-label adoption instead).
#
#   PACTO_LEDGER_REPO=... PACTO_RELEASE_TXN=... [PACTO_EXPECT_*=...] \
#     publish-oci-unit.sh <unit> <ref> <version> -- <push command...>
set -euo pipefail
UNIT="${1:?unit}"; REF="${2:?ref}"; VER="${3:?version}"; shift 3
[ "${1:-}" = "--" ] && shift
: "${PACTO_LEDGER_REPO:?PACTO_LEDGER_REPO required}"
: "${PACTO_RELEASE_TXN:?PACTO_RELEASE_TXN required}"
TXN="$PACTO_RELEASE_TXN"
DIR="$(cd "$(dirname "$0")" && pwd)"
led() { "$DIR/ledger.sh" "$@"; }
digest() { crane digest "$1" 2>/dev/null || crane digest --insecure "$1" 2>/dev/null; }
EXPECT_D="${PACTO_EXPECT_DIGEST:-}"
EXPECT_R="${PACTO_EXPECT_REVISION:-}"
EXPECT_V="${PACTO_EXPECT_VERSION:-}"

recorded="$(led digest "$TXN" "$UNIT")"
if [ -n "$recorded" ]; then
  # A completed unit — resume: the remote must still match what we recorded.
  state="$("$DIR/verify-oci.sh" "$REF" "$recorded")"   # exits non-zero on conflict
  echo "$UNIT: already recorded ($recorded) -> $state (skip)"
  exit 0
fi

# Not yet complete. Record the plan (expected identity) before any registry write.
[ -n "$EXPECT_D" ] && led plan "$TXN" "$UNIT" "$EXPECT_D" >/dev/null

# verify-oci exits non-zero (fails us) on conflict.
state="$("$DIR/verify-oci.sh" "$REF" "$EXPECT_D" "$EXPECT_R" "$EXPECT_V")"
case "$state" in
  absent)
    "$@"                                       # run the caller's push command
    d="$(digest "$REF")"
    if [ -n "$EXPECT_D" ] && [ "$d" != "$EXPECT_D" ]; then
      echo "::error::$UNIT: pushed digest $d != precomputed expected $EXPECT_D" >&2; exit 1
    fi
    led record "$TXN" "$UNIT" "$REF" "$VER" "$d" complete >/dev/null
    echo "$UNIT: published + recorded ($d)" ;;
  identical)
    d="${EXPECT_D:-$(digest "$REF")}"
    led record "$TXN" "$UNIT" "$REF" "$VER" "$d" complete >/dev/null   # idempotent; heals a crashed record
    echo "$UNIT: identical -> recorded + skip ($d)" ;;
  adopt)
    d="$(digest "$REF")"
    led record "$TXN" "$UNIT" "$REF" "$VER" "$d" complete >/dev/null
    echo "$UNIT: adopt (provenance matched this transaction) -> recorded ($d)" ;;
  *)
    echo "::error::$UNIT: unexpected verify state '$state'" >&2; exit 1 ;;
esac
