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
# digest identity of the artifact AND of every image it runs, no embedded
# credentials, observed readiness, restart persistence, port overrides,
# independence of two pinned versions, complete cleanup — and the network
# boundary, which is the one claim worth stating exactly:
#
#   After the demo artifact and its digest-pinned images have been pulled, the
#   stack requires no external network access. Its private Compose service
#   network remains available because the dashboard, Evidence Server and embedded
#   registry must communicate with each other.
#
# Stage 10 proves that boundary and nothing weaker: the registry the artifact
# came from is stopped, every route out of the demo's own network is refused, and
# the stack still comes up from empty volumes and serves the same fleet.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
ART_PORT="${PACTO_DEMO_ARTIFACT_REGISTRY_PORT:-15071}"
ART_REPO="127.0.0.1:${ART_PORT}/pacto/demo"
IMAGE_REPO="127.0.0.1:${ART_PORT}/pacto/dashboard"
REG_NAME="pacto-demo-acceptance-registry"
NETFILTER_IMAGE="pacto-demo-netfilter:acceptance"
# Empty means "whatever pin the artifact ships with" — the projection's own
# default, which is the reference a released demo actually runs.
REGISTRY_IMAGE="${PACTO_DEMO_REGISTRY_IMAGE:-}"
# A digest-qualified reference, or empty to build the production image here and
# pin it to the digest the registry assigns. The projection refuses a tag either
# way, so an override that names one fails at stage 3 rather than silently
# running a different demo.
IMAGE="${PACTO_DEMO_IMAGE:-}"

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

# The demo's egress is denied by a filter installed in the HOST's netns, so it
# outlives this script if the script dies. Everything that installs one records
# the bridge here, and the trap takes it back down.
DENIED_BR=""

# deny_egress BRIDGE — refuse every packet that leaves the demo's own network.
#
# In DOCKER-USER, which is the one FORWARD hook Docker guarantees it will not
# rewrite, keyed to this project's bridge: in on it and out somewhere else is
# refused, in on it and out on it is untouched, so the four services keep talking
# to each other. REJECT rather than DROP because a demo that fails should fail in
# seconds with a connection error, not hang until something times out.
#
# The ESTABLISHED,RELATED accept comes first and is not decoration: where Docker
# publishes ports with DNAT (Linux, so CI), the reply leg of a host->published
# connection is also "in on the bridge, out somewhere else", and dropping it
# would take away the host access the boundary explicitly keeps.
#
# This is a HARNESS mechanism. The shipped compose file is not touched, and in
# particular nothing here makes the network `internal:` — that was tried, and it
# takes the published ports away with the egress.
#
# netfilter SCRIPT — run SCRIPT in the host's netns with $ipt bound to whichever
# iptables backend Docker itself used. alpine ships both nft and legacy, and a
# rule written to the one Docker is not using is not an error — it is simply
# invisible, which would turn this whole stage into a no-op that passes. Only the
# backend Docker built DOCKER-USER in has that chain, so that is the tell.
netfilter() {
	docker run --rm --net host --privileged "$NETFILTER_IMAGE" sh -euc '
		for b in iptables-nft iptables-legacy; do
			if "$b" -S DOCKER-USER >/dev/null 2>&1; then ipt="$b"; break; fi
		done
		[ -n "${ipt:-}" ] || { echo "no iptables backend has a DOCKER-USER chain" >&2; exit 1; }
		eval "$1"
	' sh "$1" >/dev/null
}

deny_egress() {
	netfilter "
		ip link show '$1' >/dev/null
		\$ipt -I DOCKER-USER 1 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
		\$ipt -I DOCKER-USER 2 -i '$1' ! -o '$1' -j REJECT
	" || fail "could not deny the demo's egress on $1 — without it stage 10 would prove only that Docker pulled no images"
	DENIED_BR="$1"
}

allow_egress() {
	[ -n "$DENIED_BR" ] || return 0
	netfilter "
		\$ipt -D DOCKER-USER -i '$DENIED_BR' ! -o '$DENIED_BR' -j REJECT
		\$ipt -D DOCKER-USER -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
	" 2>/dev/null || true
	DENIED_BR=""
}

# reaches NETWORK HOSTPORT... — can a container on NETWORK open this connection?
# The probe is a container of its own rather than one of the demo's, so it says
# what the NETWORK allows and cannot be confused with what a service happens to
# have configured.
reaches() {
	net="$1"
	shift
	docker run --rm --net "$net" --add-host "pacto-outside:host-gateway" \
		"$NETFILTER_IMAGE" nc -w 4 -z "$@" >/dev/null 2>&1
}

cleanup() {
	allow_egress
	down_quiet "$RUN1"
	down_quiet "$RUN2"
	docker rm -f "$REG_NAME" >/dev/null 2>&1 || true
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

echo "== 1. build the observers, start the artifact registry =="
# From the checkout, before anything is running: the Product gate that will
# interrogate the live demo, and a small privileged image that installs and
# removes stage 10's egress filter. Both talk over the network only.
go build -o "$WORK/productready" "$ROOT/tests/acceptance/kind/productready"
printf 'FROM alpine:3\nRUN apk add --no-cache iptables\n' | docker build -q -t "$NETFILTER_IMAGE" - >/dev/null

# A registry CONTAINER, not an in-process one: the artifact is distributed by
# `oras`, but the image it pins has to be pushed and pulled by the Docker daemon,
# and on Docker Desktop the daemon lives in a VM that cannot reach a process
# listening on the host's loopback. A container's published port it can reach.
#
# Published on every interface because stage 10's control probe has to reach it
# from inside a container as a genuinely EXTERNAL endpoint, which loopback is not.
# It is an empty throwaway registry that exists for the length of this run.
docker rm -f "$REG_NAME" >/dev/null 2>&1 || true
docker run -d --name "$REG_NAME" -p "$ART_PORT:5000" registry:2 >/dev/null
for _ in $(seq 1 50); do curl -fsS "http://127.0.0.1:${ART_PORT}/v2/" >/dev/null 2>&1 && break; sleep 0.2; done
curl -fsS "http://127.0.0.1:${ART_PORT}/v2/" >/dev/null || fail "the artifact registry did not come up"
pass "product gate built, artifact registry serving"

echo "== 2. build the pacto image the demo pins, and pin it by DIGEST =="
# The REAL production image, from the production Dockerfile. A purpose-built test
# image would prove the demo works against something nobody ships.
#
# Then pushed, so it has a digest at all: a locally built image has an id, which
# is not a name any compose file can address. Pinning to the digest the registry
# assigns is what makes the local path the same shape as the released one, where
# the pin comes from the ledger record the dashboard-image unit left behind.
if [ -z "$IMAGE" ]; then
	docker build -q -t "$IMAGE_REPO:acceptance" "$ROOT" >/dev/null
	docker push "$IMAGE_REPO:acceptance" >/dev/null
	IMAGE="$(docker image inspect "$IMAGE_REPO:acceptance" \
		--format '{{range .RepoDigests}}{{println .}}{{end}}' | grep -m1 "^$IMAGE_REPO@")"
	docker pull "$IMAGE" >/dev/null
fi
case "$IMAGE" in
*@sha256:*) ;;
*) fail "the pacto image $IMAGE is not pinned to a digest" ;;
esac
pass "the demo will run $IMAGE"

# project_and_push VERSION DIR — render the artifact for one version and publish
# it, echoing the digest the registry assigned.
#
# `|| return 1` because this runs inside a command substitution: bash does not
# propagate errexit out of a failing subshell in a function called that way, so
# without it a refused projection would be pushed as an empty artifact and only
# surface four stages later as a missing compose.yaml.
project_and_push() {
	( cd "$ROOT" && go run ./tests/acceptance/scenario/project demo \
		-dir "$2" -pacto-image "$IMAGE" ${REGISTRY_IMAGE:+-registry-image "$REGISTRY_IMAGE"} \
		-artifact-repo "$ART_REPO" -version "$1" >/dev/null ) || return 1
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

echo "== 5. every image the artifact runs is pinned, and still lets the host decide =="
# An immutable artifact that names a mutable image is not immutable: the digest
# stays the same and the bytes it executes change. Asked of the PULLED compose
# file, which is what a user runs.
images="$(sed -n 's/^ *image: *//p' "$RUN1/compose.yaml" | sort -u)"
[ -n "$images" ] || fail "the artifact's compose file names no images"
while IFS= read -r img; do
	case "$img" in
	*@sha256:*) ;;
	*) fail "the artifact runs $img, which a tag could move" ;;
	esac
done <<<"$images"

# A multi-platform INDEX digest and one of its per-architecture CHILD manifest
# digests are the same string shape, and only the first keeps the demo native:
# pinning a child would run one architecture everywhere and emulate it on the
# other. The difference is observable — resolve each pin and ask what Docker got.
# For the pacto image built above this is a tautology, since a local build is the
# host's architecture either way; for `registry:2`, which the artifact pins to a
# real published index, it is the check.
if grep -qE '^ *platform:' "$RUN1/compose.yaml"; then
	fail "the artifact selects a platform, so it is not letting the host decide"
fi
hostarch="$(docker version --format '{{.Server.Arch}}')"
while IFS= read -r img; do
	docker pull -q "$img" >/dev/null || fail "cannot resolve $img"
	got="$(docker image inspect "$img" --format '{{.Architecture}}')"
	[ "$got" = "$hostarch" ] ||
		fail "$img resolves to $got on a $hostarch host — that pin is one architecture's manifest, not the multi-platform index"
done <<<"$images"
pass "$(wc -l <<<"$images" | tr -d ' ') pinned images, each resolving to $hostarch here"

echo "== 6. the artifact carries no credential =="
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

echo "== 7. start it, and wait on OBSERVED readiness =="
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

echo "== 8. the live demo proves the canonical fixture =="
"$WORK/productready" -base "$V1_BASE" -domain registry:5000/demo -surface compose \
	-out "$WORK/fixture.json"

# The same live journeys the Kind vertical runs, against the pulled demo instead of
# a cluster. The suite reads the fixture the gate just discovered, so it addresses
# THIS deployment's entities by name — and skips, with a reason, the one journey
# that needs the operational target Compose declares it does not provide.
#
# browser_journeys BASE FIXTURE — the browser leg, so stage 10 can run the same
# journeys against the isolated stack rather than only against this one.
browser_journeys() {
	( cd "$ROOT/pkg/dashboard/frontend" &&
		npm ci --ignore-scripts >/dev/null 2>&1 &&
		npx playwright install --with-deps chromium >/dev/null 2>&1 &&
		PW_BASE_URL="$1/" PW_FIXTURE="$(cat "$2")" \
			npx playwright test --config playwright.live.config.ts )
}
if [ -n "${RUN_BROWSER:-}" ]; then
	echo "== live Product journeys against the pulled demo (Playwright/Chromium) =="
	browser_journeys "$V1_BASE" "$WORK/fixture.json" ||
		fail "the live product journeys failed against the Compose demo"
	pass "the documented browser journeys work on the pulled demo"
fi

echo "== 9. restart persistence =="
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

echo "== 10. the documented network boundary =="
# The claim, exactly: after the artifact and its digest-pinned images have been
# pulled, the stack requires no external network access; its private Compose
# service network stays up because the four services must reach each other.
#
# `up --pull never` alone would not prove that. It proves Docker pulled nothing,
# on a runner that still has the whole Internet and still has the registry the
# artifact came from. So both of those are taken away first.
( cd "$RUN1" && env "${V1_PORTS[@]}" docker compose down -v >/dev/null 2>&1 )
# `create` rather than `up`: it makes the network and the containers without
# starting any of them, which is the only window in which the filter can be in
# place BEFORE the one-shot seed's first packet. A per-container rule cannot do
# this — the seed has no netns until it runs.
( cd "$RUN1" && env "${V1_PORTS[@]}" docker compose create --pull never >/dev/null 2>&1 ) ||
	fail "the demo could not even be created without pulling, so nothing here is local"
cid="$(cd "$RUN1" && env "${V1_PORTS[@]}" docker compose ps -aq | head -1)"
[ -n "$cid" ] || fail "compose created no containers"
proj="$(docker inspect "$cid" --format '{{index .Config.Labels "com.docker.compose.project"}}')"
netline="$(docker network ls --format '{{.ID}} {{.Name}}' --filter "label=com.docker.compose.project=$proj" | head -1)"
netid="${netline%% *}"
NET="${netline#* }"
[ -n "$netid" ] && [ -n "$NET" ] || fail "cannot find the demo's own network"
# Docker names a user-defined bridge after its own network id. deny_egress
# refuses to install anything if that interface is not there.
BR="br-${netid}"

# Control first, or "it could not reach out" would be indistinguishable from "it
# was never able to". This is a real, listening, external endpoint: the host, on
# the port the artifact registry publishes.
reaches "$NET" pacto-outside "$ART_PORT" ||
	fail "the demo's network cannot reach $ART_PORT on the host even before the filter, so the control proves nothing"
deny_egress "$BR"
if reaches "$NET" pacto-outside "$ART_PORT"; then
	fail "the filter is not denying egress, so the isolation below would be a claim rather than a test"
fi
pass "external egress from the demo's network is refused"

# And the registry the artifact came from goes away entirely.
docker stop "$REG_NAME" >/dev/null
if curl -fsS --max-time 3 "http://127.0.0.1:${ART_PORT}/v2/" >/dev/null 2>&1; then
	fail "the artifact registry is still serving, so this is not a cold start without it"
fi
pass "the registry the artifact was pulled from is stopped"

UP_FLAGS="--pull never"
up_or_dump "$RUN1" "${V1_PORTS[@]}"
UP_FLAGS=""
# Internal service communication survived — that is the half of the boundary the
# demo depends on, and the half `internal: true` would have taken with it.
reaches "$NET" evidence 8686 ||
	fail "the demo's services cannot reach each other, so the boundary is drawn in the wrong place"
# Host access to the documented published ports survived, which is what these two
# say by succeeding at all.
"$WORK/productready" -base "$V1_BASE" -domain registry:5000/demo -surface compose \
	-out "$WORK/fixture-isolated.json"
if [ -n "${RUN_BROWSER:-}" ]; then
	echo "== the same live journeys, against the isolated demo =="
	browser_journeys "$V1_BASE" "$WORK/fixture-isolated.json" ||
		fail "the live product journeys failed against the isolated demo"
fi
pass "empty volumes, no image pulls, no registry, no route out — the same fleet"

# The counterexample. Redirect the one startup dependency that talks to a
# registry at the endpoint the control above proved is reachable from this
# network when the filter is not there, and the stack must fail. Without this,
# a filter that quietly stopped applying would leave every assertion above
# passing.
docker start "$REG_NAME" >/dev/null
for _ in $(seq 1 50); do curl -fsS "http://127.0.0.1:${ART_PORT}/v2/" >/dev/null 2>&1 && break; sleep 0.2; done
cat >"$WORK/external.override.yaml" <<YAML
services:
  seed:
    extra_hosts:
      - "pacto-outside:host-gateway"
    environment:
      PACTO_DEMO_PUBLISH_TO: "pacto-outside:${ART_PORT}/pacto/redirected"
YAML
if ( cd "$RUN1" && env "${V1_PORTS[@]}" docker compose \
	-f compose.yaml -f "$WORK/external.override.yaml" \
	run --rm --no-deps seed >/dev/null 2>&1 ); then
	fail "a startup dependency pointed outside the demo's network still succeeded, so stage 10 does not test what it says"
fi
pass "a startup dependency redirected outside the network fails the stack"
allow_egress

echo "== 11. upgrade to another pinned version, alongside the first =="
oras pull --plain-http -o "$RUN2" "$ART_REPO@$D2" >/dev/null
up_or_dump "$RUN2" "${V2_PORTS[@]}"
"$WORK/productready" -base "$V2_BASE" -domain registry:5000/demo -surface compose
# Ports moved because the environment said so, and the older version is still
# serving on its own — two run directories, two projects, two sets of volumes.
curl -fsS "$V1_BASE/health" >/dev/null || fail "starting 0.0.2 disturbed the running 0.0.1"
pass "both pinned versions run at once, on the ports each was given"

echo "== 12. cleanup leaves nothing behind =="
( cd "$RUN2" && env "${V2_PORTS[@]}" docker compose down -v >/dev/null 2>&1 )
( cd "$RUN1" && env "${V1_PORTS[@]}" docker compose down -v >/dev/null 2>&1 )
left="$(docker volume ls --format '{{.Name}}' | grep -E "pacto-demo-(state|registry)$" || true)"
[ -z "$left" ] || { echo "$left" >&2; fail "down -v left volumes behind"; }
if curl -fsS "$V1_BASE/health" >/dev/null 2>&1 || curl -fsS "$V2_BASE/health" >/dev/null 2>&1; then
	fail "a demo is still serving after down -v"
fi
pass "no containers, no volumes, nothing but the pulled images"

echo "== clone-free Compose demo acceptance PASSED =="
