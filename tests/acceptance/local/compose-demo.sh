#!/usr/bin/env bash
# Clone-free acceptance for the distributed Compose demo.
#
# The demo's whole claim is that somebody who has never seen this repository can
# run one pinned artifact and get a real Pacto fleet. Docker Compose owns that
# artifact type natively — `docker compose publish` writes it, `docker compose -f
# oci://…@sha256:…` runs it — so this proves the claim the way a stranger meets
# it: the application is published by Compose, addressed by digest, and executed
# straight out of the registry. There is no run directory, no extracted tree and
# no local compose file anywhere on the execution path; the projected files are
# deleted after publication and everything below that point runs without them.
#
# What runs from the checkout is the JUDGE, not the subject: the Product gate and
# the browser suite are built here and then talk to the running demo over HTTP,
# exactly as an outside observer would. The `browser` subcommand adds the live
# Playwright journeys to the same running demo — the same suite the Kind vertical
# drives, which is the point: one set of journeys, two deployments.
#
# The claims each stage proves are the ones the demo's documentation makes:
# digest identity of the artifact AND of every image it runs, no embedded
# credentials, observed readiness, restart persistence, port overrides, explicit
# project identity, independence of two pinned versions, complete cleanup — and
# the network boundary, which is the one claim worth stating exactly:
#
#   After the demo artifact and its digest-pinned images have been pulled, the
#   stack requires no external network access. Its private Compose service
#   network remains available because the dashboard, Evidence Server and embedded
#   registry must communicate with each other.
#
# The boundary stage proves that boundary and nothing weaker: the registry the
# artifact came from is stopped, BOTH routes out of the demo's own network are
# refused — the forwarded one and the host-local one, independently — and the
# stack still comes up from empty volumes and serves the same fleet.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"

# RUN_ID — this invocation's identity, and the reason nothing below is a fixed
# name any more. This harness installs real state in the HOST: containers, a veth
# pair, an address, an image tag. It used to claim those under fixed names by
# force — `docker rm -f`, `ip link del` — which is fine on a throwaway runner and
# destructive on the machine of anyone who happened to have one. Everything it
# creates now carries this id, is refused rather than reclaimed if something is
# already there, and is recorded only once the daemon or the kernel says it
# exists, so cleanup can only ever remove what this run made.
#
# Six hex digits: enough that two runs do not pick the same names, and short
# enough to leave the interface names inside Linux's 15-character limit.
RUN_ID="$(od -An -N3 -tx1 /dev/urandom | tr -d ' \n')"

ART_PORT="${PACTO_DEMO_ARTIFACT_REGISTRY_PORT:-15071}"
ART_HOST="127.0.0.1:${ART_PORT}"
ART_PATH="pacto/demo"
ART_REPO="${ART_HOST}/${ART_PATH}"
IMAGE_REPO="${ART_HOST}/pacto/dashboard"
REG_NAME="pacto-demo-acceptance-registry-$RUN_ID"
NETFILTER_IMAGE="pacto-demo-netfilter:$RUN_ID"
# Empty means "whatever pin the artifact ships with" — the projection's own
# default, which is the reference a released demo actually runs.
REGISTRY_IMAGE="${PACTO_DEMO_REGISTRY_IMAGE:-}"
# A digest-qualified reference, or empty to build the production image here and
# pin it to the digest the registry assigns. The projection refuses a tag either
# way, so an override that names one fails at projection rather than silently
# running a different demo.
IMAGE="${PACTO_DEMO_IMAGE:-}"

# The project names the documentation tells a user to type. There is no directory
# to derive one from — the application is executed out of a registry — so `-p` is
# the whole of project identity, and the upgrade story is two of them: two
# versions, two sets of containers, two sets of volumes, two sets of ports.
PROJ1="pacto-demo"
PROJ2="pacto-demo-next"
V1_PORTS=(PACTO_DEMO_DASHBOARD_PORT=18080 PACTO_DEMO_EVIDENCE_PORT=18686 PACTO_DEMO_REGISTRY_PORT=15051)
V2_PORTS=(PACTO_DEMO_DASHBOARD_PORT=18081 PACTO_DEMO_EVIDENCE_PORT=18687 PACTO_DEMO_REGISTRY_PORT=15052)
V1_BASE="http://127.0.0.1:18080"
V2_BASE="http://127.0.0.1:18081"

MODE="${1:-run}"
case "$MODE" in
run) ;;
browser) RUN_BROWSER=1 ;;
# selftest proves the harness's own host-safety contract; own-and-exit is the
# child it spawns to watch a real EXIT trap take a real invocation's resources
# back down. Neither runs the demo.
selftest) ;;
own-and-exit) ;;
*)
	echo "unknown subcommand: $1 (use run|browser|selftest)" >&2
	exit 2
	;;
esac

WORK="$(mktemp -d)"

pass() { echo "  PASS: $1"; }
fail() { echo "  FAIL: $1" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || fail "$1 is required and not on PATH"; }
sha256() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d' ' -f1; }

# dc — Compose, told that the throwaway artifact registry speaks plain HTTP.
#
# The registry this harness stands up has no certificate, and Compose's registry
# client is its own: it does not inherit the daemon's insecure-registry list, and
# `localhost` buys nothing (verified — it still offers HTTPS first). The flag is
# a harness concern only; a released artifact lives in a real registry and the
# documented commands carry no such flag.
dc() { docker compose --insecure-registry="$ART_HOST" "$@"; }

# wait_http URL — poll until it answers. The demo's own readiness, not a sleep:
# every wait in this file is bounded by something the stack publishes.
wait_http() {
	for _ in $(seq 1 300); do
		curl -fsS --max-time 2 "$1" >/dev/null 2>&1 && return 0
		sleep 0.2
	done
	return 1
}

# up_or_dump PROJECT CMD... — start a project and WAIT on its own health checks,
# printing what the containers said if it never gets there. `up -d` keeps service
# output off the terminal, so without this a failure is one line ("service seed
# didn't complete successfully: exit 2") and no way at all to learn why — which is
# exactly how much a CI log gave the first time this went red.
up_or_dump() {
	proj="$1"
	shift
	"$@" >/dev/null && return 0
	echo "  --- the demo did not come up. What its containers said: ---" >&2
	docker compose -p "$proj" ps -a >&2 || true
	docker compose -p "$proj" logs --no-color >&2 || true
	fail "the demo in project $proj never reached a healthy state"
}

# down_quiet PROJECT — tear a project down by NAME, without caring whether it was
# ever up and without a compose file: `down` is label-driven, which is the only
# reason cleanup can work after the registry the application came from is gone.
# Used by the trap, so it must never be the thing that fails.
down_quiet() { docker compose -p "$1" down -v --remove-orphans >/dev/null 2>&1 || true; }

# uncache DIGEST — remove Compose's local copy of a fetched application, so a
# later run's clean-path assertions cannot be satisfied by this run's leftovers.
uncache() {
	hex="${1#sha256:}"
	rm -rf "${XDG_CACHE_HOME:-$HOME/.cache}/docker-compose/$hex" \
		"$HOME/Library/Caches/docker-compose/$hex" 2>/dev/null || true
}

# --- the network boundary's machinery -------------------------------------
#
# Everything below installs state in the HOST's netns, so it outlives this script
# if the script dies. Each installer refuses to touch anything it did not create,
# records what it did create, and the trap takes exactly that back down.

# The host-local endpoint: a real registry listening in the host's own network
# namespace. A packet a container addresses to a host address is delivered
# LOCALLY — it is never forwarded — so this is the endpoint that exercises INPUT.
HL_PORT="${PACTO_DEMO_HOSTLOCAL_PORT:-15072}"
HL_NAME="pacto-demo-hostlocal-endpoint-$RUN_ID"

# The forwarded endpoint: the artifact registry again, reached over a point-to-
# point link instead of over its published port. A packet addressed to the far end
# of that link is not the host's, so it is ROUTED — in on the demo's bridge, out
# on the host end — which is FORWARD, and therefore DOCKER-USER.
#
# The link lives in 198.18.0.0/15, RFC 2544's reserved benchmarking space: nothing
# on the public Internet routes it, which is what makes it the right range to
# borrow an address from. That is a statement about the Internet and not about
# this machine — a lab, a VPN or a second copy of this harness can perfectly well
# have a route into it here — so the range is only where we look. Which /30 we
# take is decided at run time, from what this host is demonstrably not using.
VETH_HOST="pactoout-$RUN_ID"
VETH_PEER="pactoin-$RUN_ID"
FWD_LOCAL=""
FWD_ADDR=""
FWD_PORT=5000
DENIED_BR=""

# What this invocation created, recorded as it succeeds. Cleanup reads only these.
#
# Containers are recorded by immutable ID and never by name. A name is a lease:
# it ends with the container holding it, and whoever takes it next is a stranger
# a list of names would have cleanup delete. An id names one container for good.
OWNED_CONTAINERS=""
OWNED_VETH=""
OWNED_IMAGE=""
OWNED_PROJECTS=""
# The id of the container `run_owned` created most recently, for the call sites
# that go on to address their own container afterwards.
OWNED_ID=""

# Linux caps an interface name at IFNAMSIZ-1 = 15 characters and refuses a longer
# one outright, so this is a build-time property of the naming scheme, checked
# where the scheme is, not discovered as a wiring failure two minutes in.
for n in "$VETH_HOST" "$VETH_PEER"; do
	[ "${#n}" -le 15 ] || fail "interface name $n is ${#n} characters; Linux allows 15"
done

# netfilter SCRIPT — run SCRIPT in the host's netns with $ipt bound to whichever
# iptables backend Docker itself used. alpine ships both nft and legacy, and a
# rule written to the one Docker is not using is not an error — it is simply
# invisible, which would turn this whole stage into a no-op that passes. Only the
# backend Docker built DOCKER-USER in has that chain, so that is the tell.
#
# --pid host as well as --net host: wiring the forwarded route means moving one
# end of a veth pair into another container's network namespace, which is
# addressed by that container's pid.

# hostns SCRIPT [ARG...] — run SCRIPT in the host's netns, READ ONLY: it looks at
# links, routes and addresses and changes none of them, so unlike netfilter() it
# needs neither --privileged nor --pid host.
hostns() {
	local s="$1"
	shift
	docker run --rm --net host "$NETFILTER_IMAGE" sh -euc "$s" sh "$@"
}

# refuse_existing KIND NAME CHECK... — stop if NAME is already taken.
#
# Everything this harness installs carries $RUN_ID, so nothing should be in the
# way. "Should" is not "is", and the alternative to stopping is what this harness
# used to do: `docker rm -f` a fixed container name and `ip link del` a fixed
# interface name, which on a developer's machine destroys whatever was there and
# cannot put it back. Refusing costs a rerun. Reclaiming costs someone their lab.
refuse_existing() {
	local kind="$1" name="$2"
	shift 2
	if "$@" >/dev/null 2>&1; then
		fail "a $kind named $name already exists; this harness will not delete, replace or reuse one it did not create"
	fi
}

# run_owned NAME DOCKER-RUN-ARGS... — start a container this invocation owns, and
# leave its id in $OWNED_ID.
#
# Create and start are two commands rather than one `docker run -d`, because the
# daemon really does perform the first and refuse the second — a host port
# somebody else already published is enough — and what it leaves behind is a
# created container under this name that `docker run`'s non-zero exit says
# nothing about. The id is therefore recorded BETWEEN the two: a container that
# never started is still this invocation's to take away.
run_owned() {
	local name="$1"
	shift
	refuse_existing container "$name" docker container inspect "$name"
	OWNED_ID="$(docker create --name "$name" "$@")" || fail "could not create the container $name"
	OWNED_CONTAINERS="$OWNED_CONTAINERS $OWNED_ID"
	docker start "$OWNED_ID" >/dev/null || fail "created but could not start $name ($OWNED_ID)"
}

# claim_projects PROJECT... — the only thing that ever fills $OWNED_PROJECTS.
#
# `docker compose -p NAME down -v --remove-orphans` is destructive and purely
# label-driven: it needs no file, so it will happily take a stranger's project
# that happens to carry a name this harness documents. The authority to run it
# comes from having found the names holding nothing at all first — which is also
# the preflight the demo run needs anyway, since a leftover project would answer
# every health check below.
claim_projects() {
	local p
	for p in "$@"; do
		[ -z "$(docker compose -p "$p" ps -aq 2>/dev/null)" ] ||
			fail "project $p already has containers; run \`docker compose -p $p down -v\` first"
		[ -z "$(docker volume ls -q --filter "label=com.docker.compose.project=$p")" ] ||
			fail "project $p already has volumes; run \`docker compose -p $p down -v\` first"
	done
	OWNED_PROJECTS="$*"
}

# The small privileged image that installs and removes the boundary stage's two
# egress filters and the point-to-point link the forwarded one needs. Tagged per
# invocation like everything else, so it never takes a tag off someone's image;
# the layer cache makes rebuilding it free.
build_netfilter_image() {
	refuse_existing image "$NETFILTER_IMAGE" docker image inspect "$NETFILTER_IMAGE"
	printf 'FROM alpine:3\nRUN apk add --no-cache iptables iproute2 util-linux\n' |
		docker build -q -t "$NETFILTER_IMAGE" - >/dev/null
	OWNED_IMAGE="$NETFILTER_IMAGE"
}

netfilter() {
	docker run --rm --net host --pid host --privileged "$NETFILTER_IMAGE" sh -euc '
		for b in iptables-nft iptables-legacy; do
			if "$b" -S DOCKER-USER >/dev/null 2>&1; then ipt="$b"; break; fi
		done
		[ -n "${ipt:-}" ] || { echo "no iptables backend has a DOCKER-USER chain" >&2; exit 1; }
		eval "$1"
	' sh "$1"
}

# deny_forwarded_egress BRIDGE — refuse every packet the demo's network FORWARDS.
#
# DOCKER-USER is the one FORWARD hook Docker guarantees it will not rewrite. In on
# this project's bridge and out somewhere else is refused; in on it and out on it
# is untouched, so the four services keep talking. This arm cannot reach a
# host-local packet, and that is the point: it is one of two independent controls,
# not a general "deny everything".
#
# REJECT rather than DROP because a demo that fails should fail in seconds with a
# connection error, not hang until something times out.
#
# The ESTABLISHED,RELATED accept comes first and is not decoration: where Docker
# publishes ports with DNAT (Linux, so CI), the reply leg of a host->published
# connection arrives on the bridge headed for the host, and dropping it would take
# away the host access the boundary explicitly keeps.
deny_forwarded_egress() {
	netfilter "
		ip link show '$1' >/dev/null
		\$ipt -I DOCKER-USER 1 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
		\$ipt -I DOCKER-USER 2 -i '$1' ! -o '$1' -j REJECT
	" || fail "could not install the FORWARD/DOCKER-USER arm on $1 — the forwarded half of the boundary would be untested"
	DENIED_BR="$1"
}

# deny_hostlocal_egress BRIDGE — refuse every packet the demo addresses to the
# HOST ITSELF.
#
# A packet whose destination is a host address is delivered locally and never
# reaches FORWARD, so DOCKER-USER alone leaves the demo able to reach everything
# the machine publishes — which is most of what "outside" means to a container.
# Refused by ingress interface, so it covers every host address rather than the
# one the probe happened to use.
deny_hostlocal_egress() {
	netfilter "
		ip link show '$1' >/dev/null
		\$ipt -I INPUT 1 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
		\$ipt -I INPUT 2 -i '$1' -j REJECT
	" || fail "could not install the INPUT arm on $1 — the host-local half of the boundary would be untested"
	DENIED_BR="$1"
}

allow_egress() {
	[ -n "$DENIED_BR" ] || return 0
	# Each deletion tolerated on its own: a rule that is already gone must not
	# stop the shell before it removes the ones that are still there.
	netfilter "
		\$ipt -D INPUT -i '$DENIED_BR' -j REJECT || true
		\$ipt -D INPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT || true
		\$ipt -D DOCKER-USER -i '$DENIED_BR' ! -o '$DENIED_BR' -j REJECT || true
		\$ipt -D DOCKER-USER -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT || true
	" 2>/dev/null || true
	DENIED_BR=""
}

# pick_forwarded_net — take a /30 out of 198.18.0.0/15 that this machine is
# demonstrably not using, and set both ends of the link from it.
#
# "Reserved for benchmarking" is not "free here": a route into the range, a
# supernet route over it (a VPN), or a local address in it all mean somebody is
# using it, and installing our own on top would both disturb them and silently
# invalidate the forwarded proof — the probe would be answered by their route,
# not ours. Candidates are walked from an offset derived from $RUN_ID so two
# concurrent runs start in different places, and if none is free we refuse.
#
# The default route matches every prefix and is not a claim on this one, so it is
# the single exclusion; anything else `to match` reports really does cover us.
pick_forwarded_net() {
	local i m cands=()
	for i in $(seq 0 31); do
		m=$(( (0x$RUN_ID + i) % 16384 ))
		cands+=("198.18.$((m / 64)).$(((m % 64) * 4))")
	done
	local free
	# shellcheck disable=SC2016  # "$@" is the container shell's, not ours.
	free="$(hostns '
		for c in "$@"; do
			[ -z "$(ip -4 route show to match "$c/30" | grep -v "^default" || true)" ] || continue
			[ -z "$(ip -4 route show to root "$c/30")" ] || continue
			[ -z "$(ip -4 -o addr show to "$c/30")" ] || continue
			echo "$c"
			break
		done
	' "${cands[@]}")" || fail "could not read this machine's routes, so no link address can be shown to be free"
	[ -n "$free" ] || fail "none of 32 candidate /30s in 198.18.0.0/15 is free on this machine; the harness will not route over an address it did not create"
	FWD_LOCAL="${free%.*}.$((${free##*.} + 1))"
	FWD_ADDR="${free%.*}.$((${free##*.} + 2))"
}

# wire_forwarded_route — give the artifact registry a second address that is only
# reachable by ROUTING to it. One veth end stays in the host's netns with
# $FWD_LOCAL/30; the other is moved into the registry's netns as $FWD_ADDR/30.
# Docker's own MASQUERADE rule makes the return path symmetric, so nothing else is
# needed. Deleting the host end deletes the pair, which is also what happens by
# itself if the registry container goes away.
wire_forwarded_route() {
	regpid="$(docker inspect -f '{{.State.Pid}}' "$REG_NAME" 2>/dev/null || true)"
	[ -n "$regpid" ] && [ "$regpid" != "0" ] || fail "the artifact registry is not running, so there is nothing to route to"
	# Both names carry $RUN_ID, so an interface holding one is not ours. This is
	# here for the diagnostic and for nothing else: a name that is free when it is
	# read can be taken before the next line runs, so nothing below may depend on
	# the answer still being true.
	refuse_existing interface "$VETH_HOST" hostns "ip link show $VETH_HOST"
	refuse_existing interface "$VETH_PEER" hostns "ip link show $VETH_PEER"
	pick_forwarded_net
	# The create is on its own, and ownership begins exactly where it returns 0.
	# Until then these are candidate names: `ip link add` is atomic and fails
	# outright on a name somebody took in the meantime, and deleting that name on
	# the way out would delete THEIR interface. "It was free a moment ago" is the
	# race, not a claim.
	netfilter "ip link add $VETH_HOST type veth peer name $VETH_PEER" ||
		fail "could not create the point-to-point link $VETH_HOST/$VETH_PEER"
	OWNED_VETH="$VETH_HOST"
	# Past that line the pair is this invocation's, so a failure in the rest of
	# the wiring takes it back down — and it is the only thing that can.
	netfilter "
		ip addr add $FWD_LOCAL/30 dev $VETH_HOST
		ip link set $VETH_HOST up
		ip link set $VETH_PEER netns $regpid
		nsenter -t $regpid -n ip addr add $FWD_ADDR/30 dev $VETH_PEER
		nsenter -t $regpid -n ip link set $VETH_PEER up
	" || {
		unwire_forwarded_route
		fail "could not wire a forwarded route to the artifact registry"
	}
}

unwire_forwarded_route() {
	[ -n "$OWNED_VETH" ] || return 0
	netfilter "ip link del $OWNED_VETH 2>/dev/null || true" 2>/dev/null || true
	OWNED_VETH=""
}

# reaches NETWORK HOST PORT — can a container on NETWORK open this connection?
# The probe is a container of its own rather than one of the demo's, so it says
# what the NETWORK allows and cannot be confused with what a service happens to
# have configured.
reaches() {
	net="$1"
	shift
	docker run --rm --net "$net" "$NETFILTER_IMAGE" nc -w 4 -z "$@" >/dev/null 2>&1
}

# release_owned — give the host back exactly what this invocation took from it,
# by the names recorded as each one was created. Nothing here is a fixed name and
# nothing here is a wildcard, so there is no path by which it removes a container,
# an interface or an image that belongs to the machine. Runs on success and on
# failure, and must never itself be the thing that fails.
release_owned() {
	unwire_forwarded_route
	for c in $OWNED_CONTAINERS; do docker rm -f "$c" >/dev/null 2>&1 || true; done
	OWNED_CONTAINERS=""
	[ -z "$OWNED_IMAGE" ] || docker rmi -f "$OWNED_IMAGE" >/dev/null 2>&1 || true
	OWNED_IMAGE=""
}

cleanup() {
	allow_egress
	# Only the projects this invocation claimed, which is none at all in the
	# helper modes: they never reach the preflight, so they never acquire the
	# authority to tear a documented project name down.
	for p in $OWNED_PROJECTS; do down_quiet "$p"; done
	release_owned
	[ -n "${D1:-}" ] && uncache "$D1"
	[ -n "${D2:-}" ] && uncache "$D2"
	rm -rf "$WORK"
	true
}
trap cleanup EXIT

need docker
need go
need curl
need jq

# --- the harness's own host-safety contract --------------------------------
#
# This file is privileged: it creates containers, a veth pair, an address and an
# image tag in the HOST, and those outlive it if it dies. The contract is that it
# never deletes, replaces, reuses or hijacks anything it did not create in THIS
# invocation, and that it removes everything it did, on success and on failure
# alike. That is a property of the harness rather than of the demo, so it is
# proved here rather than by a demo run — against sentinels this selftest creates
# and therefore owns, so nothing on the machine is put at risk in the course of
# demonstrating that nothing is put at risk.
#
# It exists because the contract used to be broken in exactly the way it is now
# checked: `ip link del pactoout` and `docker rm -f <fixed name>` ran before every
# create, and on somebody's machine they took whatever was there.

# sentinel_link NAME [CIDR] — an interface standing in for the one somebody else's
# work already had. Created here, so removing it later is ours to do; and `ip link
# add` refuses a name that exists, so this cannot take one either.
#
# A veth pair rather than a dummy: veth is the link type Docker itself runs on, so
# it is loaded anywhere this script can run at all, while `dummy` is one more
# module a runner's kernel might not have. Deleting either end takes both.
sentinel_link() {
	netfilter "
		ip link add $1 type veth peer name pdpeer-$RUN_ID
		${2:+ip addr add $2 dev $1}
		ip link set $1 up
	" >/dev/null
}
sentinel_gone() { netfilter "ip link del $1 2>/dev/null || true" >/dev/null 2>&1 || true; }

# link_state NAME — ifindex and addresses. Enough that a delete-and-recreate,
# which is exactly what the old code did, cannot pass for "untouched".
link_state() { hostns "ip -o addr show dev $1" 2>/dev/null || printf 'GONE'; }

# filter_state — both netfilter hooks, verbatim. Compared across a refusal: a
# harness that had begun mutating before it refused shows up as a difference,
# whatever rules the machine already had of its own.
filter_state() { netfilter "\$ipt -S DOCKER-USER; \$ipt -S INPUT"; }

# refuses_to_touch LABEL CMD... — CMD must fail. Run in a subshell so its `fail`
# ends the attempt rather than this selftest, and so a function it redefines for
# the length of the attempt dies with it.
refuses_to_touch() {
	local label="$1"
	shift
	if ("$@") >/dev/null 2>&1; then
		fail "$label: the harness went ahead instead of refusing"
	fi
}

# survives_refusal LABEL LINK CMD... — CMD must fail, and when it has, LINK must
# be the interface it was and both netfilter chains the rules they were. Every
# way this file can give up on the forwarded route goes through here, because
# "it refused" and "it refused without touching anything" are different claims.
survives_refusal() {
	local label="$1" link="$2" before after filters
	shift 2
	before="$(link_state "$link")"
	filters="$(filter_state)"
	refuses_to_touch "$label" "$@"
	after="$(link_state "$link")"
	[ "$before" = "$after" ] || fail "$label: $link changed: '$before' -> '$after'"
	[ "$filters" = "$(filter_state)" ] ||
		fail "$label: a netfilter rule was installed or removed on the way out"
}

# raced_wire — the real wire_forwarded_route with its preflight already behind it.
# The preflight cannot close the window it opens: a name it reads as free can be
# taken before `ip link add` runs, and that is the case worth proving. Emptying
# the check enters the window on purpose, and only ever inside the subshell
# refuses_to_touch wraps around it.
raced_wire() {
	refuse_existing() { :; }
	wire_forwarded_route
}

# bad_address_wire — the real wire_forwarded_route given an address the kernel
# will not take, so what fails is a step AFTER `ip link add` succeeded. Same
# subshell rule as raced_wire.
bad_address_wire() {
	pick_forwarded_net() {
		FWD_LOCAL="198.18.300.1"
		FWD_ADDR="198.18.300.2"
	}
	wire_forwarded_route
}

# sentinel_project PROJECT — a project standing in for the demo somebody already
# has running under one of the documented names.
#
# Assembled out of plain `docker` rather than out of a compose file, and that is
# the point rather than a shortcut: `down -v --remove-orphans` reads labels and
# nothing else, so a container, a network and a named volume wearing this
# project's labels are exactly as exposed as ones Compose wrote — with no local
# compose file anywhere near the demo's own journey. Created and not started,
# because created is already the whole exposure.
sentinel_project() {
	docker volume create \
		--label com.docker.compose.project="$1" \
		--label com.docker.compose.volume=sentinel-data "${1}_sentinel-data" >/dev/null
	docker network create \
		--label com.docker.compose.project="$1" \
		--label com.docker.compose.network=default "${1}_default" >/dev/null
	docker create --name "$1-sentinel-1" \
		--label com.docker.compose.project="$1" \
		--label com.docker.compose.service=sentinel \
		--label com.docker.compose.container-number=1 \
		--label com.docker.compose.oneoff=False \
		--label com.docker.compose.config-hash=sentinel \
		--network "${1}_default" \
		-v "${1}_sentinel-data:/var/lib/registry" registry:2 >/dev/null
	# Which labels Compose reads is Compose's business and it can change them. So
	# ask it: a sentinel Compose cannot see is one `down -v` would not have taken
	# either, and a test that plants one proves nothing without saying so.
	[ -n "$(docker compose -p "$1" ps -aq)" ] ||
		fail "the planted project $1 is not one this Compose recognises, so nothing below would be under test"
}

# proj_state PROJECT — the identities of everything carrying its label. Ids and
# names rather than counts, so a project torn down and put back cannot pass for
# one nobody touched.
proj_state() {
	printf '%s|%s|%s' \
		"$(docker compose -p "$1" ps -aq | sort | tr '\n' ' ')" \
		"$(docker network ls -q --filter "label=com.docker.compose.project=$1" | sort | tr '\n' ' ')" \
		"$(docker volume ls -q --filter "label=com.docker.compose.project=$1" | sort | tr '\n' ' ')"
}

# own_and_exit — create this invocation's host resources, name them on stdout,
# then leave: cleanly, through `fail` when PACTO_SELFTEST_DIE is set, or through a
# container the daemon creates and then refuses to start when PACTO_SELFTEST_STUCK
# is. The parent watches the REAL EXIT trap take everything back down, on all
# three paths.
own_and_exit() {
	local reg
	build_netfilter_image
	run_owned "$REG_NAME" registry:2
	reg="$OWNED_ID"
	wire_forwarded_route
	echo "OWNED image=$NETFILTER_IMAGE container=$reg veth=$OWNED_VETH"
	# The reviewer's case was a host port somebody else had already published; an
	# entrypoint that is not in the image is the same daemon behaviour — created,
	# then not started — with nothing on the host to collide with. `run_owned` puts
	# the id in its failure, because the trap is about to be the only one who knows
	# there is a container at all.
	[ -z "${PACTO_SELFTEST_STUCK:-}" ] ||
		run_owned "pacto-demo-stuck-$RUN_ID" --entrypoint /pacto-no-such-entrypoint registry:2
	[ -z "${PACTO_SELFTEST_DIE:-}" ] || fail "induced failure, with everything this invocation created still installed"
}

# own_and_clean LABEL EXPECTED-EXIT [DIE] [STUCK] — run that child and prove its
# trap left nothing of its own behind and nothing of anyone else's missing.
own_and_clean() {
	local label="$1" want="$2" die="${3:-}" stuck="${4:-}" out rc=0 img cont veth id
	out="$(PACTO_SELFTEST_DIE="$die" PACTO_SELFTEST_STUCK="$stuck" bash "$0" own-and-exit 2>&1)" || rc=$?
	if [ "$rc" != "$want" ]; then
		echo "$out" >&2
		fail "$label: the child exited $rc, expected $want"
	fi
	img="$(printf '%s\n' "$out" | sed -n 's/^OWNED image=\([^ ]*\).*/\1/p')"
	cont="$(printf '%s\n' "$out" | sed -n 's/^OWNED .*container=\([^ ]*\).*/\1/p')"
	veth="$(printf '%s\n' "$out" | sed -n 's/^OWNED .*veth=\([^ ]*\)$/\1/p')"
	if [ -z "$img" ] || [ -z "$cont" ] || [ -z "$veth" ]; then
		echo "$out" >&2
		fail "$label: the child never reported what it created, so there is nothing to check"
	fi
	if docker container inspect "$cont" >/dev/null 2>&1; then fail "$label: the container $cont survived its own cleanup"; fi
	if hostns "ip link show $veth" >/dev/null 2>&1; then fail "$label: the interface $veth survived its own cleanup"; fi
	if docker image inspect "$img" >/dev/null 2>&1; then fail "$label: the image $img survived its own cleanup"; fi
	if [ -n "$stuck" ]; then
		# The id of the container the daemon created and would not start, read out
		# of the child's own failure. Finding it there is what proves one was
		# created at all, so its absence now is the trap's work and not a create
		# that never happened.
		id="$(printf '%s\n' "$out" | sed -n 's/.*created but could not start [^ ]* (\([0-9a-f]*\)).*/\1/p')"
		if [ -z "$id" ]; then
			echo "$out" >&2
			fail "$label: no container was created and left unstarted, so nothing here is under test"
		fi
		if docker container inspect "$id" >/dev/null 2>&1; then
			fail "$label: the container $id was created, never started, and outlived the EXIT trap"
		fi
		pass "$label: a container created but refused a start was still removed by the real EXIT trap ($id)"
		return 0
	fi
	pass "$label: it removed its own container, interface and image ($cont, $veth, $img)"
}

run_selftest() {
	local id p1 p2 decoy reg foreign s1 s2
	# The two documented project names, taken the way a demo run takes them and
	# then made to hold a real project. Claiming them first is not a loophole in
	# what follows: it is the rule itself — the authority to tear one of these
	# names down comes from having found it empty — and it is what keeps a selftest
	# that fails halfway from leaving its own scenery behind. What is under test is
	# the modes below, which claim nothing and must therefore take nothing.
	claim_projects "$PROJ1" "$PROJ2"
	sentinel_project "$PROJ1"
	sentinel_project "$PROJ2"
	s1="$(proj_state "$PROJ1")"
	s2="$(proj_state "$PROJ2")"

	build_netfilter_image
	run_owned "$REG_NAME" registry:2
	reg="$OWNED_ID"

	echo "== S1. a pre-existing interface with this run's candidate name survives =="
	sentinel_link "$VETH_HOST" 203.0.113.1/30
	survives_refusal "S1/S4 the veth host end" "$VETH_HOST" wire_forwarded_route
	sentinel_gone "$VETH_HOST"
	pass "S1/S4: $VETH_HOST was refused, not deleted, and no filter was touched"

	echo "== S2. a pre-existing endpoint container with this run's name survives =="
	docker create --name "$HL_NAME" registry:2 >/dev/null
	id="$(docker container inspect -f '{{.Id}}' "$HL_NAME")"
	refuses_to_touch "the host-local endpoint" run_owned "$HL_NAME" --net host registry:2
	[ "$(docker container inspect -f '{{.Id}}' "$HL_NAME")" = "$id" ] ||
		fail "the sentinel container $HL_NAME was replaced"
	[ "$(docker container inspect -f '{{.State.Running}}' "$HL_NAME")" = false ] ||
		fail "the sentinel container $HL_NAME was started"
	docker rm -f "$HL_NAME" >/dev/null
	pass "S2: $HL_NAME was refused, not force-removed"

	echo "== S3. an occupied /30 in the benchmarking range is not hijacked =="
	pick_forwarded_net
	p1="$FWD_LOCAL"
	sentinel_link "pdsen-$RUN_ID" "$p1/30"
	pick_forwarded_net
	p2="$FWD_LOCAL"
	[ "$p1" != "$p2" ] || fail "the harness chose $p2 again with that /30 already in use"
	[ "${p2%.*}" != "${p1%.*}" ] || [ "$((${p2##*.} / 4))" != "$((${p1##*.} / 4))" ] ||
		fail "the harness chose $p2, which is inside the occupied $p1/30"
	hostns "ip -o addr show to $p1/30" | grep -q "pdsen-$RUN_ID" ||
		fail "$p1/30 is no longer held by the sentinel that had it"
	sentinel_gone "pdsen-$RUN_ID"
	pass "S3: $p1/30 stayed with its owner; the link moved to $p2"

	echo "== S8. an interface that appears after the preflight is not deleted =="
	sentinel_link "$VETH_HOST" 203.0.113.1/30
	survives_refusal "S8 the raced veth create" "$VETH_HOST" raced_wire
	sentinel_gone "$VETH_HOST"
	pass "S8: the create lost the race for $VETH_HOST and the winner kept its interface"

	echo "== S9. a failure after the pair exists removes that pair, and only it =="
	sentinel_link "pdsen-$RUN_ID" 203.0.113.5/30
	survives_refusal "S9 the veth configuration" "pdsen-$RUN_ID" bad_address_wire
	[ "$(link_state "$VETH_HOST")" = "GONE" ] ||
		fail "S9: $VETH_HOST outlived the step that failed after it was created"
	[ "$(link_state "$VETH_PEER")" = "GONE" ] ||
		fail "S9: the far end $VETH_PEER outlived the step that failed after it was created"
	sentinel_gone "pdsen-$RUN_ID"
	pass "S9: the half-wired pair was taken back down and the interface beside it was not"

	# A container named the way this harness names its own, under a run id that is
	# not one. It must survive every child below: cleanup that ever went back to a
	# fixed name or a `pacto-demo-*` sweep would take it. Its own id goes on this
	# process's list, because a fixed name is the one thing here that a failed run
	# could leave behind to poison the next one, and this run did create it.
	decoy="pacto-demo-hostlocal-endpoint-000000"
	refuse_existing container "$decoy" docker container inspect "$decoy"
	OWNED_CONTAINERS="$OWNED_CONTAINERS $(docker create --name "$decoy" registry:2)"

	echo "== S5. a run that succeeds gives everything back =="
	own_and_clean "S5" 0

	echo "== S6. a run that fails after creating everything gives it back too =="
	own_and_clean "S6" 1 1

	echo "== S7. a container the daemon creates and will not start is not leaked =="
	own_and_clean "S7" 1 "" 1

	docker container inspect "$decoy" >/dev/null 2>&1 ||
		fail "a harness-shaped container this run did not create was removed by someone else's cleanup"
	pass "S5/S6/S7: a harness-shaped container none of those runs created was left alone"

	echo "== S10. a name given up by an owned container carries no authority =="
	# Stage 11 shuts the host-local endpoint down early, and the freed name is the
	# next caller's for the asking. Whoever takes it is a stranger that a cleanup
	# list of names would delete. This is the production cleanup path, called here,
	# not a copy of it.
	run_owned "$HL_NAME" registry:2
	docker rm -f "$HL_NAME" >/dev/null
	foreign="$(docker create --name "$HL_NAME" registry:2)"
	release_owned
	docker container inspect "$foreign" >/dev/null 2>&1 ||
		fail "S10: cleanup deleted $foreign, which had done nothing but take the freed name $HL_NAME"
	if docker container inspect "$reg" >/dev/null 2>&1; then
		fail "S10: cleanup left behind $reg, a container this run did create"
	fi
	docker rm -f "$foreign" >/dev/null
	pass "S10: cleanup removed the container it recorded and left the name's next holder alone"

	echo "== S11. the demo projects planted under the documented names are intact =="
	[ "$s1" = "$(proj_state "$PROJ1")" ] ||
		fail "S11: something tore down $PROJ1: '$s1' -> '$(proj_state "$PROJ1")'"
	[ "$s2" = "$(proj_state "$PROJ2")" ] ||
		fail "S11: something tore down $PROJ2: '$s2' -> '$(proj_state "$PROJ2")'"
	pass "S11: $PROJ1 and $PROJ2 kept every container, network and volume they had"

	echo "SELFTEST OK: the harness owns what it creates, refuses what it does not, and gives all of it back"
}

case "$MODE" in
selftest)
	run_selftest
	exit 0
	;;
own-and-exit)
	own_and_exit
	exit 0
	;;
esac

echo "== 0. nothing of this demo is running or cached here =="
# Both project names are free, so what comes up below came up now. A leftover
# project from an interrupted run would answer every health check in this file —
# and this is also the only place the trap's authority to run `down -v` on those
# two names comes from.
claim_projects "$PROJ1" "$PROJ2"
pass "the documented project names $PROJ1 and $PROJ2 are free"

echo "== 1. build the observers, start the artifact registry =="
# From the checkout, before anything is running: the Product gate that will
# interrogate the live demo, and a small privileged image that installs and
# removes the boundary stage's two egress filters and the point-to-point link the
# forwarded one needs.
go build -o "$WORK/productready" "$ROOT/tests/acceptance/kind/productready"
build_netfilter_image

# A registry CONTAINER, not an in-process one: the image the artifact pins has to
# be pushed and pulled by the Docker daemon, and on Docker Desktop the daemon
# lives in a VM that cannot reach a process listening on the host's loopback. A
# container's published port it can reach.
#
# Published on every interface because the boundary stage's controls have to reach
# it from inside a container as genuinely EXTERNAL endpoints, which loopback is
# not. It is an empty throwaway registry that exists for the length of this run.
run_owned "$REG_NAME" -p "$ART_PORT:5000" registry:2
wait_http "http://$ART_HOST/v2/" || fail "the artifact registry did not come up"
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

echo "== 3. project two versions of the application =="
# The projection's output is a PUBLICATION INPUT and never an execution input:
# these two files exist only long enough for `docker compose publish` to read
# them, and stage 6 deletes them.
project() { # VERSION OUT
	( cd "$ROOT" && go run ./tests/acceptance/scenario/project demo \
		-out "$2" -pacto-image "$IMAGE" ${REGISTRY_IMAGE:+-registry-image "$REGISTRY_IMAGE"} \
		-version "$1" >/dev/null )
}
project 0.0.1 "$WORK/v1.yaml"
project 0.0.2 "$WORK/v2.yaml"

# The Compose floor, asked of the artifact rather than restated here: the
# projection declares the oldest Compose that owns this artifact type, and the
# harness is a consumer of that declaration like any other. Checked before the
# first `publish`, so an old Compose says so instead of failing on a flag.
MIN_COMPOSE="$(sed -n 's/^ *minimum-compose-version: *//p' "$WORK/v1.yaml" | tr -d '"' | head -1)"
[ -n "$MIN_COMPOSE" ] || fail "the projected application declares no minimum Compose version"
HAVE_COMPOSE="$(docker compose version --short 2>/dev/null | sed 's/^v//')"
[ -n "$HAVE_COMPOSE" ] || fail "\`docker compose version --short\` said nothing; Docker Compose v2+ is required"
[ "$(printf '%s\n%s\n' "$MIN_COMPOSE" "$HAVE_COMPOSE" | sort -V | head -1)" = "$MIN_COMPOSE" ] ||
	fail "Docker Compose $HAVE_COMPOSE cannot run this demo: the application needs $MIN_COMPOSE or newer, which is the release that added \`docker compose publish\` and \`-f oci://…\`. Upgrade Docker Desktop, or install the plugin from https://github.com/docker/compose/releases"
pass "Docker Compose $HAVE_COMPOSE satisfies the artifact's declared floor of $MIN_COMPOSE"

echo "== 4. publish both with \`docker compose publish\`, and nothing else =="
# Compose owns this OCI artifact type. It writes the manifest, chooses the media
# types and uploads the one layer; nothing here adds a file, an annotation or a
# second layer afterwards, because an application that had to be repaired by a
# generic OCI tool after publication would not be the thing a user's Compose
# fetches.
publish() { # VERSION FILE -> the artifact digest the registry assigned
	docker compose -f "$2" publish --insecure-registry -y "$ART_REPO:$1" >/dev/null 2>&1 ||
		return 1
	curl -fsS -I -H 'Accept: application/vnd.oci.image.manifest.v1+json' \
		"http://$ART_HOST/v2/$ART_PATH/manifests/$1" |
		tr -d '\r' | sed -n 's/^[Dd]ocker-[Cc]ontent-[Dd]igest: *//p'
}
D1="$(publish 0.0.1 "$WORK/v1.yaml")" || fail "\`docker compose publish\` failed; see the output above"
D2="$(publish 0.0.2 "$WORK/v2.yaml")" || fail "\`docker compose publish\` failed; see the output above"
[ -n "$D1" ] && [ -n "$D2" ] || fail "a publication resolved no artifact digest"
[ "$D1" != "$D2" ] || fail "two different versions produced one digest, so a version cannot be pinned"
pass "0.0.1 is $D1"
pass "0.0.2 is $D2"

echo "== 5. the published application is the projected bytes, verbatim =="
manifest() { curl -fsS -H 'Accept: application/vnd.oci.image.manifest.v1+json' "http://$ART_HOST/v2/$ART_PATH/manifests/$1"; }
M1="$(manifest "$D1")"
[ "$(jq -r .artifactType <<<"$M1")" = "application/vnd.docker.compose.project" ] ||
	fail "the published artifact is not a Compose application: $(jq -r .artifactType <<<"$M1")"
# ONE layer, and it is the compose file. An artifact with a second layer is one
# somebody appended to, which is the shape this repository is not allowed to
# produce for this unit.
[ "$(jq -r '.layers | length' <<<"$M1")" = "1" ] ||
	fail "the published application has $(jq -r '.layers | length' <<<"$M1") layers; Compose publishes exactly one"
[ "$(jq -r '.layers[0].mediaType' <<<"$M1")" = "application/vnd.docker.compose.file+yaml" ] ||
	fail "the one layer is not a compose file: $(jq -r '.layers[0].mediaType' <<<"$M1")"
LAYER1="$(jq -r '.layers[0].digest' <<<"$M1")"
[ "$LAYER1" = "sha256:$(sha256 "$WORK/v1.yaml")" ] ||
	fail "the published layer $LAYER1 is not the projected file; something rewrote the application between projection and publication"
pass "the application's content digest IS sha256 of the projected compose file"

# And read back by DIGEST, which is the identity the documented journey uses. The
# tag is a convenience; nothing below ever names one.
dc -f "oci://$ART_REPO@$D1" config >"$WORK/published-1.yaml" 2>/dev/null ||
	fail "the published application cannot be read back by digest"

# An immutable artifact that names a mutable image is not immutable: the digest
# stays the same and the bytes it executes change.
images="$(sed -n 's/^ *image: *//p' "$WORK/published-1.yaml" | sort -u)"
[ -n "$images" ] || fail "the published application names no images"
while IFS= read -r img; do
	case "$img" in
	*@sha256:*) ;;
	*) fail "the application runs $img, which a tag could move" ;;
	esac
done <<<"$images"

# A multi-platform INDEX digest and one of its per-architecture CHILD manifest
# digests are the same string shape, and only the first keeps the demo native:
# pinning a child would run one architecture everywhere and emulate it on the
# other. The difference is observable — resolve each pin and ask what Docker got.
# For the pacto image built above this is a tautology, since a local build is the
# host's architecture either way; for `registry:2`, which the artifact pins to a
# real published index, it is the check.
if grep -qE '^ *platform:' "$WORK/published-1.yaml"; then
	fail "the application selects a platform, so it is not letting the host decide"
fi
hostarch="$(docker version --format '{{.Server.Arch}}')"
while IFS= read -r img; do
	docker pull -q "$img" >/dev/null || fail "cannot resolve $img"
	got="$(docker image inspect "$img" --format '{{.Architecture}}')"
	[ "$got" = "$hostarch" ] ||
		fail "$img resolves to $got on a $hostarch host — that pin is one architecture's manifest, not the multi-platform index"
done <<<"$images"
pass "$(wc -l <<<"$images" | tr -d ' ') pinned images, each resolving to $hostarch here"

# A private key, a token or a password baked into an immutable artifact is one
# every user of it shares. The demo signs as an identity minted at run time into a
# volume, so there is nothing of the sort to find.
if grep -qE 'PRIVATE KEY|BEGIN OPENSSH|password|passwd|secret_key|-----BEGIN' "$WORK/published-1.yaml"; then
	grep -nE 'PRIVATE KEY|BEGIN OPENSSH|password|passwd|secret_key|-----BEGIN' "$WORK/published-1.yaml" >&2
	fail "the published application embeds credential material"
fi
pass "no key, token or password in the published application"

echo "== 6. the projections are deleted; nothing below reads a local file =="
rm -f "$WORK/v1.yaml" "$WORK/v2.yaml" "$WORK/published-1.yaml"
pass "the only remaining copy of either version is the one in the registry"

echo "== 7. run 0.0.1 straight out of the registry, by digest, under \`-p $PROJ1\` =="
# The documented command, exactly. No preceding pull, no extracted directory, no
# compose file on disk: `-f oci://…@sha256:…` is the whole input, and `-p` is the
# whole of project identity. `--wait` returns when every service reports itself
# healthy — the demo's own health checks are the clock.
up_or_dump "$PROJ1" env "${V1_PORTS[@]}" \
	docker compose --insecure-registry="$ART_HOST" -f "oci://$ART_REPO@$D1" -p "$PROJ1" up -d --wait -y
pass "the stack is up and healthy"

echo "== 8. what is actually running reads nothing from this machine =="
# Asked of the RUNNING containers rather than of the file, because the file is
# what was intended and this is what happened.
ids="$(docker compose -p "$PROJ1" ps -aq)"
[ -n "$ids" ] || fail "the demo started no containers"
# shellcheck disable=SC2086 # the ids are one docker-generated hex token per line
binds="$(docker inspect --format '{{range .Mounts}}{{if eq .Type "bind"}}{{println .Source}}{{end}}{{end}}' $ids | grep -c . || true)"
[ "$binds" = "0" ] || {
	# shellcheck disable=SC2086
	docker inspect --format '{{range .Mounts}}{{if eq .Type "bind"}}{{println .Source}}{{end}}{{end}}' $ids >&2
	fail "the running demo bind-mounts $binds host paths; a published application has no directory beside it to mount"
}
pass "zero bind mounts: every fixture input arrived inline, as a Compose config"

# The one path Compose does record is where it cached the application it fetched.
# It must not be in this checkout — that would be the execution path reading a
# repository the user does not have.
# shellcheck disable=SC2086
cfgs="$(docker inspect --format '{{index .Config.Labels "com.docker.compose.project.config_files"}}' $ids | sort -u)"
while IFS= read -r c; do
	[ -n "$c" ] || continue
	case "$c" in
	"$ROOT" | "$ROOT"/*) fail "the running demo was configured from $c, which is inside the checkout" ;;
	esac
	case "$c" in
	*"${D1#sha256:}"*) ;;
	*) fail "the running demo was configured from $c, which is not the application digest $D1 it was asked for" ;;
	esac
done <<<"$cfgs"
pass "the running project's only configuration file is Compose's own cache of $D1"

echo "== 9. the live demo proves the canonical fixture =="
"$WORK/productready" -base "$V1_BASE" -domain registry:5000/demo -surface compose \
	-out "$WORK/fixture.json"

# The same live journeys the Kind vertical runs, against the published demo
# instead of a cluster. The suite reads the fixture the gate just discovered, so it
# addresses THIS deployment's entities by name — and skips, with a reason, the one
# journey that needs the operational target Compose declares it does not provide.
browser_journeys() { # BASE FIXTURE
	( cd "$ROOT/pkg/dashboard/frontend" &&
		npm ci --ignore-scripts >/dev/null 2>&1 &&
		npx playwright install --with-deps chromium >/dev/null 2>&1 &&
		PW_BASE_URL="$1/" PW_FIXTURE="$(cat "$2")" \
			npx playwright test --config playwright.live.config.ts )
}
if [ -n "${RUN_BROWSER:-}" ]; then
	echo "== live Product journeys against the published demo (Playwright/Chromium) =="
	browser_journeys "$V1_BASE" "$WORK/fixture.json" ||
		fail "the live product journeys failed against the Compose demo"
	pass "the documented browser journeys work on the published demo"
fi

echo "== 10. restart persistence =="
# `stop` and `start` by project name — no compose file, because after publication
# there is none. The volumes are untouched, so the second start re-runs the seed
# against a registry that already has the bundles and an Evidence Server that
# already has the envelope. The documentation says that works; this is the claim,
# tested.
docker compose -p "$PROJ1" stop >/dev/null
docker compose -p "$PROJ1" start >/dev/null
wait_http "$V1_BASE/health" || fail "the demo did not come back after a stop and start"
seedlog="$(docker compose -p "$PROJ1" logs seed --no-log-prefix 2>&1)"
grep -q "already in the registry" <<<"$seedlog" || { echo "$seedlog" >&2; fail "a restarted demo re-published instead of finding its content"; }
grep -q "already ingested" <<<"$seedlog" || { echo "$seedlog" >&2; fail "a restarted demo did not recognize its own envelope as a replay"; }
"$WORK/productready" -base "$V1_BASE" -domain registry:5000/demo -surface compose
pass "the fleet survived a stop and start"

echo "== 11. the documented network boundary =="
# The claim, exactly: after the artifact and its digest-pinned images have been
# pulled, the stack requires no external network access; its private Compose
# service network stays up because the four services must reach each other.
#
# Two operations are involved and this stage keeps them apart. FETCHING THE
# APPLICATION is a registry read — `-f oci://…` performs it on every invocation
# and Compose offers no offline mode for it. PULLING THE SERVICE IMAGES is a
# separate daemon operation, and `--pull never` refuses it. So the application is
# fetched ONCE, here, with `--pull never` proving no image moved; after that the
# registry is taken away and the project runs from its own labels.
docker compose -p "$PROJ1" down -v >/dev/null
up_or_dump "$PROJ1" env "${V1_PORTS[@]}" \
	docker compose --insecure-registry="$ART_HOST" -f "oci://$ART_REPO@$D1" -p "$PROJ1" up -d --wait --pull never -y
pass "the application was fetched once, and no image was pulled to run it"

# Stopped, and its runtime state emptied, so what comes back below is a cold
# start and not a resumption. `docker volume rm` refuses a volume a container
# still references, so the contents go rather than the volumes.
docker compose -p "$PROJ1" stop >/dev/null
docker run --rm --net none \
	-v "${PROJ1}_pacto-demo-state:/s" -v "${PROJ1}_pacto-demo-registry:/r" \
	"$NETFILTER_IMAGE" find /s /r -mindepth 1 -delete
pass "the demo is stopped and its volumes are empty"

netline="$(docker network ls --format '{{.ID}} {{.Name}}' --filter "label=com.docker.compose.project=$PROJ1" | head -1)"
netid="${netline%% *}"
NET="${netline#* }"
[ -n "$netid" ] && [ -n "$NET" ] || fail "cannot find the demo's own network"
# Docker names a user-defined bridge after its own network id, and addresses it
# with the network's gateway. deny_* refuse to install anything if the interface
# is not there.
BR="br-${netid}"
GW="$(docker network inspect "$NET" --format '{{(index .IPAM.Config 0).Gateway}}')"
[ -n "$GW" ] || fail "the demo's network has no gateway address, so there is no host-local endpoint to probe"

# TWO endpoints, deliberately reached by two different netfilter paths, because
# one control cannot prove both halves of the boundary.
#
#   host-local  a listener in the HOST's netns, addressed at the bridge's own
#               gateway. Delivered locally: INPUT, never FORWARD.
#   forwarded   the artifact registry, addressed over a point-to-point link.
#               Routed: FORWARD, and therefore DOCKER-USER, never INPUT.
run_owned "$HL_NAME" --net host -e REGISTRY_HTTP_ADDR="0.0.0.0:$HL_PORT" registry:2
HL_ID="$OWNED_ID"
for _ in $(seq 1 100); do
	docker run --rm --net host "$NETFILTER_IMAGE" nc -w 2 -z 127.0.0.1 "$HL_PORT" >/dev/null 2>&1 && break
	sleep 0.2
done
wire_forwarded_route

# Control first, or "it could not reach out" would be indistinguishable from "it
# was never able to". Both endpoints are real, listening and external.
reaches "$NET" "$GW" "$HL_PORT" ||
	fail "the host-local control endpoint at $GW:$HL_PORT is unreachable from the demo's network even before any filter, so that arm would prove nothing"
reaches "$NET" "$FWD_ADDR" "$FWD_PORT" ||
	fail "the forwarded control endpoint at $FWD_ADDR:$FWD_PORT is unreachable from the demo's network even before any filter, so that arm would prove nothing"
pass "both routes out of the demo's network are open, and each is a different netfilter path"

# The FORWARD arm alone. The forwarded endpoint must go, and the host-local one
# must NOT — which is what proves these are two independent controls and that the
# forwarded endpoint really is forwarded. If a filter in DOCKER-USER could close
# the host-local route, that route was never host-local.
deny_forwarded_egress "$BR"
if reaches "$NET" "$FWD_ADDR" "$FWD_PORT"; then
	fail "the DOCKER-USER arm did not refuse the forwarded route to $FWD_ADDR:$FWD_PORT"
fi
reaches "$NET" "$GW" "$HL_PORT" ||
	fail "the DOCKER-USER arm also closed the host-local route to $GW:$HL_PORT, so that endpoint is not host-local and the two arms are not independent"
pass "FORWARD/DOCKER-USER refused: the forwarded route is closed, the host-local one is still open"

# And now the INPUT arm, which is the only thing that can close the other one.
deny_hostlocal_egress "$BR"
if reaches "$NET" "$GW" "$HL_PORT"; then
	fail "the INPUT arm did not refuse the host-local route to $GW:$HL_PORT"
fi
pass "INPUT refused: the host-local route is closed too"

# The counterexamples. Redirect the one startup dependency that talks to a
# registry at each endpoint the controls above proved reachable when the filters
# were not there, and the stack must fail — once per route. Without these, a
# filter that quietly stopped applying would leave every assertion above passing.
redirected_seed_fails() { # LABEL HOSTPORT
	if docker compose --insecure-registry="$ART_HOST" -f "oci://$ART_REPO@$D1" -p "$PROJ1" \
		run --rm --no-deps \
		-e PACTO_DEMO_PUBLISH_TO="$2/pacto/redirected" \
		-e PACTO_INSECURE_REGISTRIES="registry:5000,$2" \
		seed >/dev/null 2>&1; then
		fail "a startup dependency pointed at the $1 endpoint still succeeded, so that arm does not test what it says"
	fi
}
redirected_seed_fails forwarded "$FWD_ADDR:$FWD_PORT"
redirected_seed_fails host-local "$GW:$HL_PORT"
pass "a startup dependency redirected over either route fails the stack"

# And the registry the artifact came from goes away entirely, along with the
# host-local endpoint and the link to it. Both registries are STOPPED rather than
# removed: what the boundary has to lose is the listener, and a container this
# invocation still holds is one whose name cannot be taken by anybody else in the
# meantime. Final cleanup removes them, by the ids it recorded.
unwire_forwarded_route
docker stop "$HL_ID" >/dev/null
docker stop "$REG_NAME" >/dev/null
if curl -fsS --max-time 3 "http://$ART_HOST/v2/" >/dev/null 2>&1; then
	fail "the artifact registry is still serving, so this is not a cold start without it"
fi
# Which is the distinction this stage owes: the APPLICATION can no longer be
# fetched at all. What is offline is the created project, not `-f oci://`.
if dc -f "oci://$ART_REPO@$D1" -p "$PROJ1" config >/dev/null 2>&1; then
	fail "the application was still readable with its registry stopped, so this stage cannot tell an application fetch from an image pull"
fi
pass "the registry is stopped: the application can no longer be fetched, and no image can be pulled"

# `start`, by project name. It honours the same dependency graph `up` does, which
# is the whole reason a project created online can be operated offline.
docker compose -p "$PROJ1" start >/dev/null ||
	fail "the demo could not be started with no registry and no route out"
wait_http "$V1_BASE/health" || {
	docker compose -p "$PROJ1" ps -a >&2 || true
	docker compose -p "$PROJ1" logs --no-color >&2 || true
	fail "the isolated demo never became healthy"
}
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

allow_egress
docker start "$REG_NAME" >/dev/null
wait_http "http://$ART_HOST/v2/" || fail "the artifact registry did not come back"

echo "== 12. upgrade: two digest-pinned versions at once, two project names =="
# The same registry, a different digest and a different `-p`. Nothing is shared:
# not the containers, not the volumes, not the ports — which is what makes
# "independent" a thing this can observe rather than assert.
up_or_dump "$PROJ2" env "${V2_PORTS[@]}" \
	docker compose --insecure-registry="$ART_HOST" -f "oci://$ART_REPO@$D2" -p "$PROJ2" up -d --wait -y
"$WORK/productready" -base "$V2_BASE" -domain registry:5000/demo -surface compose
curl -fsS "$V1_BASE/health" >/dev/null || fail "starting 0.0.2 disturbed the running 0.0.1"
for v in "${PROJ1}_pacto-demo-state" "${PROJ1}_pacto-demo-registry" "${PROJ2}_pacto-demo-state" "${PROJ2}_pacto-demo-registry"; do
	docker volume inspect "$v" >/dev/null 2>&1 || fail "$v does not exist, so the two versions are not keeping separate state"
done
pass "both pinned versions run at once, on the ports and under the project names each was given"

echo "== 13. cleanup leaves nothing behind =="
# By project name and with no compose file, because that is all a user who
# published nothing and cloned nothing has.
docker compose -p "$PROJ2" down -v >/dev/null
curl -fsS "$V1_BASE/health" >/dev/null || fail "cleaning up 0.0.2 took 0.0.1 down with it"
docker compose -p "$PROJ1" down -v >/dev/null
left="$(docker volume ls --format '{{.Name}}' | grep -E "^(${PROJ1}|${PROJ2})_pacto-demo-(state|registry)$" || true)"
[ -z "$left" ] || { echo "$left" >&2; fail "down -v left volumes behind"; }
if curl -fsS "$V1_BASE/health" >/dev/null 2>&1 || curl -fsS "$V2_BASE/health" >/dev/null 2>&1; then
	fail "a demo is still serving after down -v"
fi
pass "no containers, no volumes, nothing but the pulled images"

echo "== clone-free Compose demo acceptance PASSED =="
