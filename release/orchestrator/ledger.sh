#!/usr/bin/env bash
# Durable, concurrency-safe release ledger — per-unit IMMUTABLE OCI artifacts.
#
# Layout (all tags under $PACTO_LEDGER_REPO):
#   <txn>              transaction METADATA {schema,transactionId,sourceSha,manifestSha}
#                      — written once by init, never mutated.
#   <txn>-<unit>       per-unit RESULT {coordinate,version,digest,status}
#                      — written once by that unit's `record`.
#   <txn>-<unit>.plan  per-unit PLAN {expectedDigest}
#                      — written once before the unit's push, so a crash after
#                        push/before record is recoverable (see publish-oci-unit.sh).
#
# Distinct writers touch distinct tags => no read-modify-write, no lost updates
# under parallel publication. Every tag is IMMUTABLE: a second write must be
# byte-identical (idempotent resume) else it fails closed (exit 3). The same
# script backs staging (a disposable local registry) and production; only
# $PACTO_LEDGER_REPO differs.
#
#   ledger.sh init    <txn> <sourceSha> <manifestSha>
#   ledger.sh verify  <txn> <sourceSha> <manifestSha>   # exit 0 if metadata matches, else non-zero
#   ledger.sh plan    <txn> <unit> <expectedDigest>
#   ledger.sh planned <txn> <unit>                       -> expected digest ("" if none)
#   ledger.sh record  <txn> <unit> <coordinate> <version> <digest> <status>
#   ledger.sh status  <txn> <unit>                       -> pending|complete|failed
#   ledger.sh digest  <txn> <unit>                       -> recorded digest ("" if none)
#   ledger.sh get     <txn>                              -> metadata JSON ("" if absent)
#   ledger.sh meta    <txn> <field>                      -> a metadata field value ("" if none)
set -euo pipefail
CMD="${1:?usage: ledger.sh <init|verify|plan|planned|record|status|digest|get|meta> ...}"; shift
REPO="${PACTO_LEDGER_REPO:?PACTO_LEDGER_REPO required}"
# Fail-closed: a production ledger repo requires explicit opt-in, same as the
# publishers, so staging tooling can never write a production ledger by accident.
case "$REPO" in
  *ghcr.io*|*trianalab*) [ "${PACTO_ALLOW_PROD:-}" = "1" ] || { echo "refusing prod ledger repo '$REPO' without PACTO_ALLOW_PROD=1" >&2; exit 1; } ;;
esac
# Fail-closed on a MISSING TOOL, not just a missing tag. The ledger is an OCI
# artifact read and written with oras|jq; when a job forgets to install one, every
# read returns the empty string, the empty string is indistinguishable from "this
# transaction recorded nothing", and a release half-ships. That is release run
# 32560058692. An absent tool is a job misconfiguration, so say which job and how.
for tool in oras jq; do
  command -v "$tool" >/dev/null 2>&1 || {
    echo "::error::ledger.sh $CMD: '$tool' is not installed, but the release ledger ($REPO) is an OCI artifact read and written with it. Install it in this job (ORAS: oras-project/setup-oras) — an uninstalled tool reads as an empty ledger, which is how a release half-ships." >&2
    exit 1
  }
done
orasFlags() { case "$REPO" in localhost*|127.0.0.1*) printf -- '--plain-http';; esac; }
# OCI tags allow [A-Za-z0-9_.-]; sanitize txn/unit-derived tags defensively.
tagsan() { printf '%s' "$1" | tr -c 'A-Za-z0-9_.-' '_'; }
ref() { printf '%s:%s' "$REPO" "$(tagsan "$1")"; }

# pull <tag> -> ledger.json content on stdout ("" if the tag is ABSENT). A fresh
# temp dir per call so concurrent/repeated pulls never collide on the filename.
#
# Only a genuine 404 yields "". Any other failure — auth, TLS, DNS, rate limit,
# a registry outage — is fatal, because "" is load-bearing everywhere upstream
# (it means "not yet recorded" and unlocks the write path), and reporting an
# UNREADABLE ledger as an EMPTY one is precisely how a resumed release both
# re-initializes a live transaction and republishes a completed unit. Callers
# assign the result to a variable first, so `set -e` propagates this exit.
pull() {
  local d out rc=0
  d="$(mktemp -d)"
  # shellcheck disable=SC2046  # $(orasFlags) is intentionally unquoted: empty => no arg.
  out="$(oras pull $(orasFlags) "$(ref "$1")" -o "$d" 2>&1)" || rc=$?
  if [ "$rc" -eq 0 ]; then
    cat "$d/ledger.json" 2>/dev/null || true
  elif ! printf '%s' "$out" | grep -qiE 'not found|NAME_UNKNOWN|MANIFEST_UNKNOWN|404'; then
    rm -rf "$d"
    echo "::error::ledger read failed for $(ref "$1") (exit $rc) — refusing to report an unreadable ledger as an empty one: $out" >&2
    exit 1
  fi
  rm -rf "$d"
}
# push_immutable <tag> <json>: write the doc to an immutable tag. If the tag
# already exists it must be byte-identical (idempotent) else fail closed (exit 3).
push_immutable() {
  local tag="$1" json="$2" existing
  existing="$(pull "$tag")"
  if [ -n "$existing" ]; then
    if [ "$(printf '%s' "$existing" | jq -cS .)" = "$(printf '%s' "$json" | jq -cS .)" ]; then
      return 0   # idempotent: identical content already recorded
    fi
    echo "::error::ledger tag '$tag' already exists with DIFFERENT content — refusing to overwrite (fail closed)" >&2
    return 3
  fi
  local d; d="$(mktemp -d)"; printf '%s' "$json" > "$d/ledger.json"
  # shellcheck disable=SC2046
  ( cd "$d" && oras push $(orasFlags) --artifact-type application/vnd.pacto.release.ledger.v1+json "$(ref "$tag")" ledger.json:application/json >/dev/null )
  rm -rf "$d"
}
# read one field from a per-unit or plan tag, with a default when the tag is absent.
field() { local v; v="$(pull "$1")"; if [ -n "$v" ]; then printf '%s' "$v" | jq -r --arg f "$2" --arg d "$3" '.[$f] // $d'; else printf '%s' "$3"; fi; }

case "$CMD" in
  init)
    json="$(jq -nc --arg t "$1" --arg s "$2" --arg m "$3" '{schema:"pacto-release-ledger/v1",kind:"transaction",transactionId:$t,sourceSha:$s,manifestSha:$m}')"
    push_immutable "$1" "$json"; echo "ledger init $1 (source=$2)" ;;
  verify)
    meta="$(pull "$1")"; [ -n "$meta" ] || { echo "::error::no ledger metadata for transaction $1" >&2; exit 1; }
    gt="$(printf '%s' "$meta" | jq -r '.transactionId // ""')"
    gs="$(printf '%s' "$meta" | jq -r '.sourceSha // ""')"
    gm="$(printf '%s' "$meta" | jq -r '.manifestSha // ""')"
    { [ "$gt" = "$1" ] && [ "$gs" = "$2" ] && [ "$gm" = "$3" ]; } || {
      echo "::error::ledger metadata mismatch for $1: stored (txn=$gt source=$gs manifest=$gm) != requested (txn=$1 source=$2 manifest=$3)" >&2; exit 1; }
    echo "ledger verify $1 OK (transactionId + sourceSha + manifestSha match)" ;;
  plan)
    json="$(jq -nc --arg t "$1" --arg u "$2" --arg d "$3" '{schema:"pacto-release-ledger/v1",kind:"plan",transactionId:$t,unit:$u,expectedDigest:$d}')"
    push_immutable "$1-$2.plan" "$json"; echo "ledger plan $1 $2 expect=$3" ;;
  planned) field "$1-$2.plan" expectedDigest "" ;;
  record)
    json="$(jq -nc --arg t "$1" --arg u "$2" --arg c "$3" --arg v "$4" --arg d "$5" --arg s "$6" '{schema:"pacto-release-ledger/v1",kind:"result",transactionId:$t,unit:$u,coordinate:$c,version:$v,digest:$d,status:$s}')"
    push_immutable "$1-$2" "$json"; echo "ledger record $1 $2=$6 ($5)" ;;
  status) field "$1-$2" status pending ;;
  digest) field "$1-$2" digest "" ;;
  get)    pull "$1" ;;
  meta)   field "$1" "$2" "" ;;
  *) echo "unknown command: $CMD" >&2; exit 2 ;;
esac
