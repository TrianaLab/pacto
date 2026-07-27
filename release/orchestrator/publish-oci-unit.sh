#!/usr/bin/env bash
# Shared OCI publish adapter (release-safety items 4,5,11). Verifies a unit
# against the durable ledger, then: identical -> skip (safe resume); conflict ->
# fail closed; absent -> run the push command, read back the digest, record
# complete in the ledger. release.yml (production) and dry-run.sh (staging) call
# THIS same adapter — only PACTO_LEDGER_REPO + coordinates + credentials differ.
#
#   PACTO_LEDGER_REPO=... PACTO_RELEASE_TXN=... \
#     publish-oci-unit.sh <unit> <ref> <version> -- <push command...>
set -euo pipefail
UNIT="${1:?unit}"; REF="${2:?ref}"; VER="${3:?version}"; shift 3
[ "${1:-}" = "--" ] && shift
: "${PACTO_LEDGER_REPO:?PACTO_LEDGER_REPO required}"
: "${PACTO_RELEASE_TXN:?PACTO_RELEASE_TXN required}"
DIR="$(cd "$(dirname "$0")" && pwd)"
digest() { crane digest "$1" 2>/dev/null || crane digest --insecure "$1" 2>/dev/null; }

want="$("$DIR/ledger.sh" digest "$PACTO_RELEASE_TXN" "$UNIT")"
state="$("$DIR/verify-oci.sh" "$REF" "$want")"   # exits non-zero (fails us) on conflict
case "$state" in
  identical)
    echo "$UNIT: identical -> skip (already published this transaction)" ;;
  absent)
    "$@"                                          # run the caller's push command
    d="$(digest "$REF")"
    "$DIR/ledger.sh" record "$PACTO_RELEASE_TXN" "$UNIT" "$REF" "$VER" "$d" complete >/dev/null
    echo "$UNIT: published + recorded ($d)" ;;
  *)
    echo "$UNIT: unexpected state '$state'"; exit 1 ;;
esac
