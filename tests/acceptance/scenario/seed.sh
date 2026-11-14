#!/bin/sh
# The Pacto demo's one imperative step: publish what the plan says to publish and
# ingest the evidence it says to ingest.
#
# Everything this acts on is DATA — the same tab-delimited execution plan the
# Kubernetes acceptance harness reads, projected once from the canonical scenario
# in tests/acceptance/scenario. No service name, repository, tag, subject,
# sequence number or producer is written here, so the demo cannot describe a
# different fixture from the one CI proves. The plan is read field by field and
# every record's arity is checked; it is never sourced and never evaluated.
#
# It is idempotent on purpose. `docker compose down` keeps the volumes, so a
# second `up` re-runs this against a registry that already has the bundles and an
# Evidence Server that already has the envelopes: a re-push is a no-op and a
# replayed envelope is a 409, which is a success here rather than a failure.
#
# POSIX sh: the image's shell is busybox ash, so no arrays, no process
# substitution and no `local`.
set -eu

: "${PACTO_DEMO_PLAN:?the plan to execute}"
: "${PACTO_DEMO_PUBLISH_TO:?the OCI domain to publish to}"
: "${PACTO_DEMO_EVIDENCE_URL:?the Evidence Server to ingest through}"
: "${PACTO_DEMO_KEYS:?the directory the signing key was minted into}"

TAB=$(printf '\t')
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

fail() { echo "seed: $*" >&2; exit 1; }

# plan_kind KIND — every record of one kind, with the kind stripped, into a file.
# A file rather than a pipe: a `while read` on the far side of a pipe runs in a
# subshell, where `fail` would exit nothing.
plan_kind() {
	while IFS= read -r line; do
		case "$line" in
		"$1$TAB"*) printf '%s\n' "${line#*"$TAB"}" ;;
		esac
	done <"$PACTO_DEMO_PLAN" >"$WORK/$1"
}

plan_kind push
plan_kind signer
plan_kind evidence

echo "== publish the fixture's contract revisions =="
# The registry is up before this container starts, but "up" and "serving" are not
# the same instant, so the first push retries. Everything after it does not: a
# second failure is a real one.
: >"$WORK/digests"
pushed=0
while IFS="$TAB" read -r key dir ref extra; do
	[ -n "$key" ] && [ -n "$dir" ] && [ -n "$ref" ] && [ -z "${extra:-}" ] ||
		fail "malformed push record in $PACTO_DEMO_PLAN"
	attempt=1
	while :; do
		out=$(pacto push "oci://$PACTO_DEMO_PUBLISH_TO/$ref" -p "$dir" 2>&1) && break
		[ "$pushed" -eq 0 ] && [ "$attempt" -lt 30 ] || {
			printf '%s\n' "$out" >&2
			fail "could not publish $ref"
		}
		attempt=$((attempt + 1))
		sleep 1
	done
	# A push that finds the same bundle under its tag is a successful no-op that
	# says "Already published" — the whole point of a second `up`. Either way it
	# prints the digest, so the cross-check below runs on every start.
	digest=$(printf '%s' "$out" | grep -oE 'sha256:[0-9a-f]{64}' | head -1 || true)
	[ -n "$digest" ] || fail "pushing $ref printed no digest: $out"
	printf '%s\n' "$digest" >>"$WORK/digests"
	pushed=$((pushed + 1))
	case "$out" in
	*"Already published"*) echo "  $ref is already in the registry" ;;
	*) echo "  published $ref $digest" ;;
	esac
done <"$WORK/push"
[ "$pushed" -gt 0 ] || fail "the plan declares nothing to publish"

echo "== wait for the Evidence Server to be ready =="
# It cannot have been ready before now. Its subjects are the contract revisions
# just published, and it answers 503 until every one of them resolves in the
# registry and answers native Referrers discovery — so this container is what
# makes it ready, which is why Compose only waits for it to have STARTED.
attempt=1
until wget -q --spider "$PACTO_DEMO_EVIDENCE_URL/api/evidence/v1/ready" 2>/dev/null; do
	[ "$attempt" -lt 60 ] || fail "the Evidence Server never became ready at $PACTO_DEMO_EVIDENCE_URL"
	attempt=$((attempt + 1))
	sleep 1
done

echo "== ingest the signed evidence from the 'remote' environment =="
# WHO signs is the plan's to say, and the trust store the Evidence Server built at
# startup binds the key to exactly that producer: sign as anyone else and
# ingestion rejects it.
producer=""
IFS="$TAB" read -r producer key_id extra <"$WORK/signer" || true
[ -n "$producer" ] && [ -n "$key_id" ] && [ -z "${extra:-}" ] ||
	fail "the plan declares no usable signer"
key="$PACTO_DEMO_KEYS/$key_id.key"
[ -f "$key" ] || fail "the Evidence Server minted no key at $key"

while IFS="$TAB" read -r subject payload sequence envelope_id extra; do
	[ -n "$subject" ] && [ -n "$payload" ] && [ -n "$sequence" ] && [ -n "$envelope_id" ] && [ -z "${extra:-}" ] ||
		fail "malformed evidence record in $PACTO_DEMO_PLAN"
	# The payload was pinned to a digest computed from these exact bundle bytes
	# BEFORE any registry existed. Packing is content-deterministic, so it must be
	# one of the digests the registry now holds — and if the two ever diverge, this
	# says so instead of leaving the demo pointing at content nobody pushed.
	want=$(grep -oE 'sha256:[0-9a-f]{64}' "$payload" | head -1 || true)
	[ -n "$want" ] || fail "$payload pins no digest"
	grep -qxF "$want" "$WORK/digests" ||
		fail "$payload points at $want, which the registry does not hold"

	pacto evidence sign "$payload" --key "$key" --key-id "$key_id" --producer "$producer" \
		--sequence "$sequence" --id "$envelope_id" >"$WORK/$envelope_id.envelope.json"
	if out=$(pacto evidence send "$WORK/$envelope_id.envelope.json" --url "$PACTO_DEMO_EVIDENCE_URL" 2>&1); then
		echo "  ingested evidence about $subject"
	else
		case "$out" in
		*409*) echo "  evidence about $subject was already ingested" ;;
		*)
			printf '%s\n' "$out" >&2
			fail "evidence about $subject was not accepted"
			;;
		esac
	fi
done <"$WORK/evidence"

if [ -n "${PACTO_DEMO_STATS:-}" ]; then
	echo "== record the demo's run records =="
	: "${PACTO_DEMO_STATS_TIMES:?the timestamps of the run records}"
	: "${PACTO_DEMO_STATS_BASE_EPOCH:?the time the run records are stamped at}"
	: "${PACTO_DEMO_STATS_REPO:?the repository to publish run records to}"
	# Run records are unsigned statistics, published once: the registry itself
	# says whether they already are. A repository with no records answers 0; any
	# other failure is a real one.
	pacto stats --registry "$PACTO_DEMO_STATS_REPO" --since 90d --output-format json >"$WORK/stats-read" 2>&1 || {
		cat "$WORK/stats-read" >&2
		fail "could not read the run records in $PACTO_DEMO_STATS_REPO"
	}
	read_count=$(grep -oE '"recordsRead": *[0-9]+' "$WORK/stats-read" | grep -oE '[0-9]+$' || true)
	[ -n "$read_count" ] || fail "pacto stats answered without a record count: $(cat "$WORK/stats-read")"
	if [ "$read_count" -gt 0 ]; then
		echo "  run records were already seeded ($read_count in $PACTO_DEMO_STATS_REPO)"
	else
		# The records were stamped at a fixed time when the artifact was built;
		# move every timestamp by the same shift to seed time, so the window
		# that ends now always holds the same records. Two
		# passes through placeholders, so a shifted time that equals another
		# record's original is never shifted twice.
		offset=$(($(date -u +%s) - PACTO_DEMO_STATS_BASE_EPOCH))
		: >"$WORK/stats.sed"
		: >"$WORK/stats-new.sed"
		n=0
		while IFS="$TAB" read -r epoch ts extra; do
			[ -n "$epoch" ] && [ -n "$ts" ] && [ -z "${extra:-}" ] || fail "malformed line in $PACTO_DEMO_STATS_TIMES"
			n=$((n + 1))
			new=$(date -u -d "@$((epoch + offset))" +%Y-%m-%dT%H:%M:%SZ) || fail "date cannot format @$((epoch + offset))"
			printf 's/"%s"/"@%s@"/g\n' "$ts" "$n" >>"$WORK/stats.sed"
			printf 's/"@%s@"/"%s"/g\n' "$n" "$new" >>"$WORK/stats-new.sed"
		done <"$PACTO_DEMO_STATS_TIMES"
		cat "$WORK/stats-new.sed" >>"$WORK/stats.sed"
		sed -f "$WORK/stats.sed" "$PACTO_DEMO_STATS" >"$WORK/stats.jsonl"
		mkdir "$WORK/stats"
		n=0
		while IFS= read -r line; do
			n=$((n + 1))
			printf '%s\n' "$line" >"$WORK/stats/$(printf '%04d' "$n").json"
		done <"$WORK/stats.jsonl"
		[ "$n" -gt 0 ] || fail "$PACTO_DEMO_STATS holds no run record"
		out=$(pacto stats publish "$WORK"/stats/*.json --registry "$PACTO_DEMO_STATS_REPO" 2>&1) || {
			printf '%s\n' "$out" >&2
			fail "could not publish the run records to $PACTO_DEMO_STATS_REPO"
		}
		echo "  published $n run records (unsigned statistics) to $PACTO_DEMO_STATS_REPO"
	fi
fi

echo "== the demo's fleet is published =="
