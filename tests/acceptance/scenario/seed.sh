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
fresh=0
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
	# A push that finds its tag already taken declines to overwrite it and prints no
	# digest — the whole point of a second `up`, and not something to fail on. The
	# digest cross-check below is what a first run gets and a resumed one does not.
	digest=$(printf '%s' "$out" | grep -oE 'sha256:[0-9a-f]{64}' | head -1 || true)
	pushed=$((pushed + 1))
	if [ -n "$digest" ]; then
		printf '%s\n' "$digest" >>"$WORK/digests"
		fresh=$((fresh + 1))
		echo "  published $ref $digest"
	else
		echo "  $ref is already in the registry"
	fi
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
	# one of the digests just published — and if the two ever diverge, this says so
	# instead of leaving the demo pointing at content nobody pushed.
	#
	# Only when THIS run published all of them. A resumed run pushed nothing, so it
	# has nothing to compare against and would report a divergence that is really
	# just the registry remembering. The comparison is not the only guard: ingestion
	# resolves the envelope's digest-pinned ref against the registry on every run,
	# resumed ones included, and a payload pointing at absent content fails there.
	want=$(grep -oE 'sha256:[0-9a-f]{64}' "$payload" | head -1 || true)
	[ -n "$want" ] || fail "$payload pins no digest"
	if [ "$fresh" -eq "$pushed" ]; then
		grep -qxF "$want" "$WORK/digests" ||
			fail "$payload points at $want, which this run did not publish"
	fi

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

echo "== the demo's fleet is published =="
