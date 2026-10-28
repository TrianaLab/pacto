#!/usr/bin/env bash
# Helpers delivery.sh and delivery-demo.sh share: the image build and the reads
# of Operations and Stages. Every reader names its Stage explicitly, so a call
# copied between the scripts cannot silently check another Stage. Source it
# after lib.sh (eventually, fail, build_operator_images, _PACTO_ROOT).
#
# Waits are bounded by DELIVERY_OP_WAIT (an Operation appears) and
# DELIVERY_CONCLUDE_WAIT (it concludes), in eventually's attempts; each script
# sets its own.

# build_images — the three Pacto images at once, each build's output in its own
# log, printed when it fails.
build_images() {
  local logs; logs="$(mktemp -d)"
  build_operator_images "$OP_IMG" "$DASH_IMG" "$VER" > "$logs/operator.log" 2>&1 &
  local op=$!
  docker build --load -f "$_PACTO_ROOT/integrations/kubernetes/Dockerfile" --build-arg VERSION="$VER" \
    --target delivery --build-arg CMD=cmd/delivery -t "$DL_IMG" "$_PACTO_ROOT" > "$logs/delivery.log" 2>&1 &
  local dl=$!
  wait "$op" || { cat "$logs/operator.log" >&2; fail "the dashboard or operator image did not build"; }
  wait "$dl" || { cat "$logs/delivery.log" >&2; fail "the pacto-delivery image did not build"; }
}

ops() { kubectl -n "$1" get operations -o json; }
op_to() { # NS STAGE DIGEST [TYPE] — the operations moving STAGE to DIGEST
  ops "$1" | jq -r --arg s "$2" --arg d "$3" --arg t "${4:-delivery.pacto.trianalab.io/promote}" \
    '.items[] | select(.spec.stage == $s and .spec.params.to == $d and .spec.type == $t) | .metadata.name'
}
op_exists() { [ -n "$(op_to "$@")" ]; }
wait_op() { # NS STAGE DIGEST [TYPE] — prints the one operation that appears
  eventually "${DELIVERY_OP_WAIT:-60}" op_exists "$@" || fail "no ${4:-promote} of $2 to $3 appeared in $1"
  op_to "$@" | head -1
}
field() { kubectl -n "$1" get operation "$2" -o jsonpath="{.status.$3}"; }
concluded() { [ "$(field "$1" "$2" phase)" = Concluded ]; }
outcome() { echo "$(field "$1" "$2" outcome)/$(field "$1" "$2" verification)"; }
held() { [ "$(field "$1" "$2" phase)" = Held ]; }
wait_concluded() { # NS OP WANT_OUTCOME
  eventually "${DELIVERY_CONCLUDE_WAIT:-80}" concluded "$1" "$2" || fail "$1/$2 never concluded: $(field "$1" "$2" phase) $(field "$1" "$2" reason): $(field "$1" "$2" message)"
  [ "$(outcome "$1" "$2")" = "$3" ] || fail "$1/$2 concluded $(outcome "$1" "$2"), want $3 ($(field "$1" "$2" reason): $(field "$1" "$2" message))"
}
pointer() { kubectl -n "$1" get stage "$2" -o jsonpath='{.status.pointer}'; } # NS STAGE
runs() { [ "$(pointer "$1" "$2")" = "$3" ]; } # NS STAGE DIGEST
# published_in NS STAGE DIGEST — the Stage remembers DIGEST as published: Flux
# verified its signature on the candidate OCIRepository.
published_in() { kubectl -n "$1" get stage "$2" -o json | jq -e --arg d "$3" '.status.published // [] | index($d)' >/dev/null; }
