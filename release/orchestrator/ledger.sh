#!/usr/bin/env bash
# Durable, digest-aware release ledger. Stored as an
# OCI artifact keyed by the transactionId, so a resumed release run reconstructs
# per-unit truth (published digest + status) from a durable store — not a stale
# local file. The same script backs staging (a disposable local registry) and
# production; only $PACTO_LEDGER_REPO differs.
#
#   ledger.sh init   <txnId> <sourceSha> <manifestSha>
#   ledger.sh record <txnId> <unit> <coordinate> <version> <digest> <status>
#   ledger.sh get    <txnId>                      -> ledger JSON (empty if absent)
#   ledger.sh status <txnId> <unit>               -> pending|complete|failed
#   ledger.sh digest <txnId> <unit>               -> recorded digest ("" if none)
set -euo pipefail
CMD="${1:?usage: ledger.sh <init|record|get|status|digest> ...}"; shift
REPO="${PACTO_LEDGER_REPO:?PACTO_LEDGER_REPO required}"
# Fail-closed: a production ledger repo requires explicit opt-in, same as the
# publishers, so staging tooling can never write a production ledger by accident.
case "$REPO" in
  *ghcr.io*|*trianalab*) [ "${PACTO_ALLOW_PROD:-}" = "1" ] || { echo "refusing prod ledger repo '$REPO' without PACTO_ALLOW_PROD=1" >&2; exit 1; } ;;
esac
orasFlags() { case "$REPO" in localhost*|127.0.0.1*) printf -- '--plain-http';; esac; }
ref() { printf '%s:%s' "$REPO" "$1"; }
WORK="$(mktemp -d)"; trap 'rm -rf "$WORK"' EXIT

pull() { oras pull $(orasFlags) "$(ref "$1")" -o "$WORK" >/dev/null 2>&1 && cat "$WORK/ledger.json" 2>/dev/null || printf ''; }
push() { ( cd "$WORK" && oras push $(orasFlags) --artifact-type application/vnd.pacto.release.ledger.v1+json "$(ref "$1")" ledger.json:application/json >/dev/null ); }

case "$CMD" in
  init)
    jq -n --arg t "$1" --arg s "$2" --arg m "$3" \
      '{schema:"pacto-release-ledger/v1",transactionId:$t,sourceSha:$s,manifestSha:$m,units:{}}' > "$WORK/ledger.json"
    push "$1"; echo "ledger init $1 (source=$2)" ;;
  record)
    cur="$(pull "$1")"; [ -n "$cur" ] || { echo "no ledger for transaction $1 (init first)" >&2; exit 1; }
    printf '%s' "$cur" | jq --arg u "$2" --arg c "$3" --arg v "$4" --arg d "$5" --arg s "$6" \
      '.units[$u]={coordinate:$c,version:$v,digest:$d,status:$s}' > "$WORK/ledger.json"
    push "$1"; echo "ledger record $1 $2=$6 ($5)" ;;
  get)    pull "$1" ;;
  status) pull "$1" | jq -r --arg u "$2" '.units[$u].status // "pending"' ;;
  digest) pull "$1" | jq -r --arg u "$2" '.units[$u].digest // ""' ;;
  *) echo "unknown command: $CMD" >&2; exit 2 ;;
esac
