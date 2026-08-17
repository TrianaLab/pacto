#!/usr/bin/env bash
# Clone-free acceptance for the distributed Compose demo.
#
# The demo's whole claim is that somebody who has never seen this repository can
# pull one pinned artifact and get a real Pacto fleet. So this proves it the way
# they would experience it: the artifact is pushed to a registry, pulled BY
# DIGEST into an empty directory outside the checkout, and started there. Nothing
# under $ROOT is on the execution path — the artifact's only host mount is `.`,
# the run directory itself.
#
# What runs from the checkout is the JUDGE, not the subject: the Product gate and
# the browser suite are built here and then talk to the running demo over HTTP,
# exactly as an outside observer would. The `browser` subcommand adds the live
# Playwright journeys to the same running demo — the same suite the Kind vertical
# drives, which is the point: one set of journeys, two deployments.
#
# The claims each stage proves are the ones the artifact's own README makes:
# digest identity, no embedded credentials, observed readiness, restart
# persistence, offline-after-pull, port overrides, independence of two pinned
# versions, and complete cleanup.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
IMAGE="${PACTO_DEMO_IMAGE:-pacto-demo:acceptance}"
REGISTRY_IMAGE="${PACTO_DEMO_REGISTRY_IMAGE:-registry:2}"
ART_PORT="${PACTO_DEMO_ARTIFACT_REGISTRY_PORT:-15071}"
ART_REPO="127.0.0.1:${ART_PORT}/pacto/demo"

# Two run directories, because the upgrade story is two pinned versions side by
# side. The second takes ports of its own so both can be up at once — which is
# what makes "independent" a thing this can observe rather than assert.
V1_PORTS=(PACTO_DEMO_DASHBOARD_PORT=18080 PACTO_DEMO_EVIDENCE_PORT=18686 PACTO_DEMO_REGISTRY_PORT=15051)
V2_PORTS=(PACTO_DEMO_DASHBOARD_PORT=18081 PACTO_DEMO_EVIDENCE_PORT=18687 PACTO_DEMO_REGISTRY_PORT=15052)
V1_BASE="http://127.0.0.1:18080"
V2_BASE="http://127.0.0.1:18081"

case "${1:-run}" in
run) ;;
browser) RUN_BROWSER=1 ;;
*)
	echo "unknown subcommand: $1 (use run|browser)" >&2
	exit 2
	;;
esac

WORK="$(mktemp -d)"
RUN1="$(mktemp -d)"
RUN2="$(mktemp -d)"
# `mkdir pacto-demo` is what the documented journey says, and under a normal umask
# that is 0755. `mktemp -d` is 0700, which no user typing the documented command
# would get -- and the demo's containers run as a non-root user, so on Linux 0700
# means they cannot even traverse into the directory they mount. Docker Desktop
# virtualizes bind-mount ownership and hides that difference completely, which is
# precisely how a green local run became a red CI one. Prove the documented mode.
chmod 755 "$RUN1" "$RUN2"

pass() { echo "  PASS: $1"; }
fail() { echo "  FAIL: $1" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || fail "$1 is required and not on PATH"; }

# up_or_dump DIR VAR=VAL... — start one run directory and WAIT on its own health
# checks, printing what the containers said if it never gets there. `up -d` keeps
# service output off the terminal, so without this a failure is one line ("service
# seed didn't complete successfully: exit 2") and no way at all to learn why --
# which is exactly how much a CI log gave the first time this went red.
up_or_dump() {
	dir="$1"
	shift
	# shellcheck disable=SC2086 # UP_FLAGS is a deliberate word list, empty by default
	(cd "$dir" && env "$@" docker compose up -d --wait ${UP_FLAGS:-} >/dev/null) && return 0
	echo "  --- the demo did not come up. What its containers said: ---" >&2
	(cd "$dir" && env "$@" docker compose ps -a && env "$@" docker compose logs --no-color) >&2 || true
	fail "the demo in $dir never reached a healthy state"
}

# down_quiet DIR — tear a run directory's project down without caring whether it
# was ever up. Used by the trap, so it must never be the thing that fails.
down_quiet() {
	[ -f "$1/compose.yaml" ] || return 0
	(cd "$1" && docker compose down -v --remove-orphans >/dev/null 2>&1) || true
}

cleanup() {
	down_quiet "$RUN1"
	down_quiet "$RUN2"
	# wait, so the shell reaps the registry here instead of announcing "Terminated"
	# on its own after the last PASS — a script that ends in a signal notice reads
	# like a failure to everybody who runs it.
	if [ -n "${REG_PID:-}" ]; then
		kill "$REG_PID" 2>/dev/null || true
		wait "$REG_PID" 2>/dev/null || true
	fi
	rm -rf "$WORK" "$RUN1" "$RUN2"
	true
}
trap cleanup EXIT

need docker
need oras
need go

echo "== 0. the run directories are outside the checkout =="
for d in "$RUN1" "$RUN2"; do
	case "$d" in
	"$ROOT" | "$ROOT"/*) fail "$d is inside the checkout, so this would prove nothing" ;;
	esac
	[ -z "$(ls -A "$d")" ] || fail "$d is not empty"
done
pass "two empty run directories outside $ROOT"

echo "== 1. build the pacto image the demo pins =="
# The REAL production image, from the production Dockerfile. A purpose-built test
# image would prove the demo works against something nobody ships.
docker build -q -t "$IMAGE" "$ROOT" >/dev/null
pass "built $IMAGE"

echo "== 2. build the observers =="
# From the checkout, before anything is running: the Product gate that will
# interrogate the live demo, and the in-memory registry that will distribute the
# artifact. Both talk over the network only.
go build -o "$WORK/productready" "$ROOT/tests/acceptance/kind/productready"
go build -o "$WORK/localregistry" "$ROOT/tests/acceptance/local/localregistry"
"$WORK/localregistry" --port "$ART_PORT" >/dev/null 2>&1 &
REG_PID=$!
for _ in $(seq 1 50); do curl -fsS "http://127.0.0.1:${ART_PORT}/v2/" >/dev/null 2>&1 && break; sleep 0.2; done
curl -fsS "http://127.0.0.1:${ART_PORT}/v2/" >/dev/null || fail "the artifact registry did not come up"
pass "product gate built, artifact registry serving"

# project_and_push VERSION DIR — render the artifact for one version and publish
# it, echoing the digest the registry assigned.
project_and_push() {
	( cd "$ROOT" && go run ./tests/acceptance/scenario/project demo \
		-dir "$2" -pacto-image "$IMAGE" -registry-image "$REGISTRY_IMAGE" \
		-artifact-repo "$ART_REPO" -version "$1" >/dev/null )
	( cd "$2" && oras push --plain-http --format go-template='{{.digest}}' "$ART_REPO:$1" . )
}

echo "== 3. publish two pinned versions of the artifact =="
D1="$(project_and_push 0.0.1 "$WORK/build-1")"
D2="$(project_and_push 0.0.2 "$WORK/build-2")"
[ -n "$D1" ] && [ -n "$D2" ] || fail "a push resolved no digest"
[ "$D1" != "$D2" ] || fail "two different versions produced one digest, so a version cannot be pinned"
pass "0.0.1 is $D1"
pass "0.0.2 is $D2"

echo "== 4. materialize 0.0.1 by DIGEST into an empty directory =="
# By digest, not by tag: the tag is the convenience, the digest is the identity.
oras pull --plain-http -o "$RUN1" "$ART_REPO@$D1" >/dev/null
diff -r "$WORK/build-1" "$RUN1" >/dev/null || fail "what came out of the registry is not what went in"
pass "the pulled tree is byte-identical to the projected one"

# Everything the demo needs, and nothing else. `ls` rather than a list written
# here: a file added to the artifact should show up as a fixture nobody declared,
# not be silently accepted because this only checked the ones it knew about.
for want in compose.yaml .env plan.tsv seed.sh README.md; do
	[ -f "$RUN1/$want" ] || fail "the artifact has no $want"
done
pass "the run directory is the whole input: $(find "$RUN1" -type f | wc -l | tr -d ' ') files"

echo "== 5. the artifact carries no credential =="
# A private key, a token or a password baked into an immutable artifact is one
# every user of it shares. The demo signs as an identity minted at run time into a
# volume, so there is nothing of the sort to find.
if grep -rIl -E 'PRIVATE KEY|BEGIN OPENSSH|password|passwd|secret_key|-----BEGIN' "$RUN1" >/dev/null 2>&1; then
	grep -rIn -E 'PRIVATE KEY|BEGIN OPENSSH|password|passwd|secret_key|-----BEGIN' "$RUN1" >&2
	fail "the artifact embeds credential material"
fi
if find "$RUN1" -type f \( -name '*.key' -o -name '*.pem' -o -name '*.pub' -o -name '*.p12' \) | grep -q .; then
	fail "the artifact ships key files"
fi
pass "no key, token or password in the pulled artifact"

echo "== 6. start it, and wait on OBSERVED readiness =="
# --wait returns when every service reports itself healthy. No sleep anywhere in
# this file: the demo's own health checks are the clock.
up_or_dump "$RUN1" "${V1_PORTS[@]}"
pass "the stack is up and healthy"

# Asked of the RUNNING containers rather than of the file, because the file is
# what was intended and this is what happened. Every bind mount must be the run
# directory; one naming the checkout would be the execution path reading fixtures
# from a repository the user does not have.
ids="$(cd "$RUN1" && env "${V1_PORTS[@]}" docker compose ps -aq)"
[ -n "$ids" ] || fail "the demo started no containers"
# shellcheck disable=SC2086 # the ids are one docker-generated hex token per line
binds="$(docker inspect --format '{{range .Mounts}}{{if eq .Type "bind"}}{{.Source}}{{"\n"}}{{end}}{{end}}' $ids | sort -u)"
[ -n "$binds" ] || fail "the demo bind-mounts nothing, so it cannot be reading the artifact"
while IFS= read -r src; do
	[ -n "$src" ] || continue
	case "$src" in
	"$ROOT" | "$ROOT"/*) fail "the running demo bind-mounts the checkout at $src" ;;
	*"$(basename "$RUN1")"*) ;;
	*) fail "the running demo bind-mounts $src, which is not its run directory" ;;
	esac
done <<<"$binds"
pass "the only host path the demo mounts is its own run directory"

echo "== 7. the live demo proves the canonical fixture =="
"$WORK/productready" -base "$V1_BASE" -domain registry:5000/demo -surface compose \
	-out "$WORK/fixture.json"

# The same live journeys the Kind vertical runs, against the pulled demo instead of
# a cluster. The suite reads the fixture the gate just discovered, so it addresses
# THIS deployment's entities by name — and skips, with a reason, the one journey
# that needs the operational target Compose declares it does not provide.
if [ -n "${RUN_BROWSER:-}" ]; then
	echo "== live Product journeys against the pulled demo (Playwright/Chromium) =="
	( cd "$ROOT/pkg/dashboard/frontend" &&
		npm ci --ignore-scripts >/dev/null 2>&1 &&
		npx playwright install --with-deps chromium >/dev/null 2>&1 &&
		PW_BASE_URL="$V1_BASE/" PW_FIXTURE="$(cat "$WORK/fixture.json")" \
			npx playwright test --config playwright.live.config.ts ) ||
		fail "the live product journeys failed against the Compose demo"
	pass "the documented browser journeys work on the pulled demo"
fi

echo "== 8. restart persistence =="
# `down` keeps the volumes, so the second `up` re-runs the seed against a registry
# that already has the bundles and an Evidence Server that already has the
# envelope. The README says that works; this is the claim, tested.
( cd "$RUN1" && env "${V1_PORTS[@]}" docker compose down >/dev/null 2>&1 )
up_or_dump "$RUN1" "${V1_PORTS[@]}"
seedlog="$(cd "$RUN1" && env "${V1_PORTS[@]}" docker compose logs seed --no-log-prefix 2>&1)"
grep -q "already in the registry" <<<"$seedlog" || { echo "$seedlog" >&2; fail "a restarted demo re-published instead of finding its content"; }
grep -q "already ingested" <<<"$seedlog" || { echo "$seedlog" >&2; fail "a restarted demo did not recognize its own envelope as a replay"; }
"$WORK/productready" -base "$V1_BASE" -domain registry:5000/demo -surface compose
pass "the fleet survived a stop and start"

echo "== 9. offline after the pull =="
# Fresh volumes, no image pulls: everything the demo needs is on the host and in
# the run directory. The registry it publishes into is the one it starts.
( cd "$RUN1" && env "${V1_PORTS[@]}" docker compose down -v >/dev/null 2>&1 )
UP_FLAGS="--pull never"
up_or_dump "$RUN1" "${V1_PORTS[@]}"
UP_FLAGS=""
"$WORK/productready" -base "$V1_BASE" -domain registry:5000/demo -surface compose
pass "a cold start with no registry access reaches the same fleet"

echo "== 10. upgrade to another pinned version, alongside the first =="
oras pull --plain-http -o "$RUN2" "$ART_REPO@$D2" >/dev/null
up_or_dump "$RUN2" "${V2_PORTS[@]}"
"$WORK/productready" -base "$V2_BASE" -domain registry:5000/demo -surface compose
# Ports moved because the environment said so, and the older version is still
# serving on its own — two run directories, two projects, two sets of volumes.
curl -fsS "$V1_BASE/health" >/dev/null || fail "starting 0.0.2 disturbed the running 0.0.1"
pass "both pinned versions run at once, on the ports each was given"

echo "== 11. cleanup leaves nothing behind =="
( cd "$RUN2" && env "${V2_PORTS[@]}" docker compose down -v >/dev/null 2>&1 )
( cd "$RUN1" && env "${V1_PORTS[@]}" docker compose down -v >/dev/null 2>&1 )
left="$(docker volume ls --format '{{.Name}}' | grep -E "pacto-demo-(state|registry)$" || true)"
[ -z "$left" ] || { echo "$left" >&2; fail "down -v left volumes behind"; }
if curl -fsS "$V1_BASE/health" >/dev/null 2>&1 || curl -fsS "$V2_BASE/health" >/dev/null 2>&1; then
	fail "a demo is still serving after down -v"
fi
pass "no containers, no volumes, nothing but the pulled images"

echo "== clone-free Compose demo acceptance PASSED =="
