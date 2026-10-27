#!/usr/bin/env bash
# Phase 6 ("records") of the delivery kind acceptance, sourced by delivery.sh,
# whose state and helpers it reads. records_store runs once phases 1 and 3
# finished, beside the others; records_audit runs after every phase. The recorder ran
# for the whole run with an Ed25519 key the run generated, writing to zot,
# which serves the OCI Referrers API natively. It proves against the registry:
#   - every operation of the run, refusals included, has a signed record, and
#     no seq of any Stage is a gap or recorded twice;
#   - a record is attached to the version it moved to, the one it moved from,
#     that version's contract and the Stage's stream anchor;
#   - `pacto delivery record verify` passes on every Stage, and cosign, with no
#     Pacto involved, verifies a record against the recorder's public key;
#   - history and blame answer from the records, even for an operation that
#     is no longer in the cluster, and undo returns a plan and proposes nothing;
#   - a plan cites what the records say happened to the version elsewhere;
#   - a recorder that was down records what concluded meanwhile, once each;
#   - a record whose payload was changed, or one validly signed that breaks
#     the chain, is refused by record verify, stops the recorder and freezes the
#     Stage until the stream is repaired and a person unfreezes it;
#   - nobody but the recorder can move the stream head.
# The CLI runs on the host: HTTP_PROXY sends its plain-HTTP registry traffic to
# the port-forward, so the in-cluster names the recorder wrote resolve.

# records_init defines what both parts of phase 6 use.
records_init() {
  streams_local="127.0.0.1:${LOCAL_REG_PORT}/${STREAMS_REPO}"
  tmp="$(mktemp -d)"
  rec() {
    HTTP_PROXY="http://127.0.0.1:${LOCAL_REG_PORT}" PACTO_INSECURE_REGISTRIES="$REG_HOST" KUBECONFIG="$AGENT_A" \
      "$PACTO" delivery --trust "$RECORDER_PUB" "$@"
  }
  # anchor_of NS STAGE — the digest of the Stage's stream anchor.
  anchor_of() { local a; a="$(kubectl -n "$1" get stage "$2" -o jsonpath='{.status.stream.anchor}')"; echo "${a##*@}"; }
  # records_on REPO DIGEST UID — how many records of operation UID are attached to REPO@DIGEST.
  records_on() {
    oras discover --plain-http --format json "127.0.0.1:${LOCAL_REG_PORT}/$1@$2" \
      | jq --arg u "$3" '[.. | objects | select((.annotations // {})["dev.pacto.uid"] == $u)] | length'
  }
  uid_of() { kubectl -n "$1" get operation "$2" -o jsonpath='{.metadata.uid}'; }
}

# records_store is the part of phase 6 that only touches publish-manual/billing
# and the recorder, so it runs beside the other phases: a recorder that was down
# catches up, and a changed or chain-breaking record stops the stream and freezes
# the Stage until it is repaired.
records_store() {
  records_init
  local out seq
  # refuse NAME — a refusal on publish-manual/billing: FOREIGN is never
  # published, so the promote is Rejected at once.
  refuse() {
    as_agent "$AGENT_A" propose -n "$M" --stage billing --to "$FOREIGN" --name "$1" --reason "phase 6" >/dev/null
    eventually 30 concluded "$M" "$1" || fail "$1 never concluded"
  }
  recorded() { [ -n "$(field "$M" "$1" record.digest)" ]; }
  unrecorded() { ! recorded "$1"; }

  section "a recorder that was down records what concluded meanwhile, once each"
  kubectl -n "$DNS" scale deploy pacto-operator-delivery-recorder --replicas=0 >/dev/null
  kubectl -n "$DNS" wait --for=delete pod -l app.kubernetes.io/component=delivery-recorder --timeout=120s >/dev/null 2>&1 || true
  local r1=records-down-1 r2=records-down-2
  refuse "$r1"; refuse "$r2"
  kubectl -n "$M" delete operation "$r2" --wait=false >/dev/null
  waits() { ! recorded "$r1" && kubectl -n "$M" get operation "$r2" >/dev/null 2>&1; }
  always 3 waits || fail "an operation was recorded or let go while the recorder was down"
  kubectl -n "$DNS" scale deploy pacto-operator-delivery-recorder --replicas=1 >/dev/null
  kubectl -n "$DNS" rollout status deploy/pacto-operator-delivery-recorder --timeout=180s >/dev/null
  r2_gone() { ! kubectl -n "$M" get operation "$r2" >/dev/null 2>&1; }
  eventually 40 recorded "$r1" || fail "$r1 was not recorded after the recorder came back"
  eventually 40 r2_gone || fail "the cancelled $r2 was not recorded and let go"
  out="$(rec record verify -n "$M" --stage billing --output-format json 2>"$tmp/err")" || fail "record verify after the restart: $(cat "$tmp/err") $out"
  seq="$(kubectl -n "$M" get stage billing -o jsonpath='{.status.stream.seq}')"
  # One record per seq and no gap: nothing was written twice or skipped.
  jq -e --argjson seq "$seq" '.ok and (.records | length) == $seq and ([.records[] | select(.kind == "gap")] | length) == 0' <<<"$out" >/dev/null \
    || fail "after the restart the stream has gaps or duplicates: $out"
  pass "with the recorder down $r1 and $r2 waited; back up, each was recorded once, with no gap"

  # stopped_until_repaired WHAT REPAIR... — the recorder refuses
  # publish-manual/billing's stream and the promoter freezes the Stage: a new
  # refusal stays unrecorded until REPAIR mends the stream, then is recorded,
  # and a person unfreezes.
  conflict() { [ "$(kubectl -n "$M" get stage billing -o jsonpath='{.status.conditions[?(@.type=="RecordStoreReady")].reason}')" = StreamConflict ]; }
  sound() { [ "$(kubectl -n "$M" get stage billing -o jsonpath='{.status.conditions[?(@.type=="RecordStoreReady")].status}')" = True ]; }
  frozen() { [ "$(kubectl -n "$M" get stage billing -o jsonpath='{.status.frozen}')" = true ]; }
  local unfreezes=0
  stopped_until_repaired() { # WHAT REPAIR...
    local held="records-$1" what="$1"
    shift
    refuse "$held"
    eventually 40 conflict || fail "the recorder did not stop on the $what record: $(kubectl -n "$M" get stage billing -o jsonpath='{.status.conditions}')"
    eventually 20 frozen || fail "the Stage was not frozen on the $what record"
    kubectl -n "$M" get stage billing -o jsonpath='{.status.frozenReason}' | grep -q StreamConflict || fail "the freeze does not say StreamConflict"
    always 2 unrecorded "$held" || fail "$held was recorded on a stream in conflict"
    "$@" >/dev/null || fail "could not repair the stream after the $what record"
    eventually 40 sound || fail "the recorder did not resume once the stream was repaired after the $what record"
    eventually 40 recorded "$held" || fail "$held was not recorded once the stream was repaired"
    unfreezes=$((unfreezes + 1))
    local unfreeze="records-unfreeze-$unfreezes"
    as_person alice team-billing propose -n "$M" --stage billing --type unfreeze --name "$unfreeze" --reason "stream repaired" >/dev/null
    eventually 40 concluded "$M" "$unfreeze" || fail "$unfreeze never concluded: $(field "$M" "$unfreeze" message)"
    [[ "$(outcome "$M" "$unfreeze")" == Succeeded/* ]] || fail "$unfreeze concluded $(outcome "$M" "$unfreeze")"
    ! frozen || fail "the Stage stayed frozen after a person unfroze it"
    # The next forgery reads the head: the unfreeze must be in the stream first.
    eventually 40 recorded "$unfreeze" || fail "$unfreeze was not recorded"
    rec record verify -n "$M" --stage billing >/dev/null 2>&1 || fail "record verify fails after the stream was repaired after the $what record"
  }
  # attach_forged FILE — attach a bundle to the Stage's anchor the way the recorder does; prints its manifest.
  attach_forged() {
    (cd "$tmp" && oras attach --plain-http --artifact-type application/vnd.dev.sigstore.bundle.v0.3+json \
      --annotation dev.sigstore.bundle.predicateType=delivery.pacto.trianalab.io/operation/v1 --annotation dev.sigstore.bundle.content=dsse-envelope \
      --format go-template='{{.digest}}' "$streams_local@$(anchor_of "$M" billing)" "$1:application/vnd.dev.sigstore.bundle.v0.3+json")
  }
  # head_bundle — the newest record of publish-manual/billing, as a file in $tmp.
  head_bundle() {
    local m l
    m="$(rec record verify -n "$M" --stage billing --output-format json 2>/dev/null | jq -r '.records[-1].manifest')"
    l="$(oras manifest fetch --plain-http "$streams_local@$m" | jq -r '.layers[0].digest')"
    oras blob fetch --plain-http --output "$tmp/head.json" "$streams_local@$l" >/dev/null
  }

  section "a record whose payload was changed is ignored"
  # Anyone who can push to the streams repository can attach a record, so one
  # no trusted key signed is not the stream's history: the recorder names it
  # and keeps recording, and nothing freezes.
  head_bundle
  jq --arg p "$(jq -r .dsseEnvelope.payload "$tmp/head.json" | base64 -d | jq -cj '.predicate.time.finished = "2000-01-01T00:00:00Z"' | base64 | tr -d '\n')" \
    '.dsseEnvelope.payload = $p' "$tmp/head.json" > "$tmp/tampered.json"
  local forged
  forged="$(attach_forged tampered.json)"
  out="$(rec record verify -n "$M" --stage billing 2>&1 || true)"
  grep -q "unreadable: $forged: signature" <<<"$out" || fail "record verify did not refuse the changed record: $out"
  refuse records-ignored
  eventually 40 recorded records-ignored || fail "the recorder stopped on a record no trusted key signed"
  kubectl -n "$M" get stage billing -o jsonpath='{.status.conditions[?(@.type=="RecordStoreReady")].message}' | grep -q "$forged" \
    || fail "RecordStoreReady does not name the ignored record"
  ! frozen || fail "a record no trusted key signed froze the Stage"
  oras manifest delete --plain-http --force "$streams_local@$forged" >/dev/null
  pass "the changed record failed its signature and was ignored: the recorder named it, kept recording and nothing froze"

  section "a trusted record that goes missing stops the recorder"
  head_bundle
  local head
  head="$(rec record verify -n "$M" --stage billing --output-format json 2>/dev/null | jq -r '.records[-1].manifest')"
  oras manifest delete --plain-http --force "$streams_local@$head" >/dev/null
  # Attaching the same signed bundle again restores the record: its id is the
  # digest of its payload, whatever manifest carries it.
  stopped_until_repaired missing attach_forged head.json
  pass "the newest record went missing; the recorder stopped and the Stage froze until the record was restored and alice unfroze it"

  section "a validly signed record that breaks the chain is refused"
  head_bundle
  jq -r .dsseEnvelope.payload "$tmp/head.json" | base64 -d \
    | jq '.predicate.chain.seq += 1 | .predicate.chain.parent = "sha256:" + ("0" * 64) | .predicate.intent.uid = "forged"' > "$tmp/fork.st.json"
  forge_record "$tmp/fork.st.json" "$tmp/fork.json"
  forged="$(attach_forged fork.json)"
  out="$(rec record verify -n "$M" --stage billing 2>&1 || true)"
  grep -q "does not chain from" <<<"$out" || fail "record verify did not refuse the broken chain: $out"
  stopped_until_repaired fork oras manifest delete --plain-http --force "$streams_local@$forged"
  pass "the forged record broke the chain; the recorder stopped and the Stage froze until it was removed and alice unfroze it"
}

# records_audit is the part of phase 6 that reads every operation and every
# Stage of the run, so it runs once every other phase has finished.
records_audit() {
  records_init
  section "every operation of the run has a signed record"
  all_recorded() {
    kubectl get operations -A -o json | jq -e '[.items[] | select(.metadata.deletionTimestamp or
      (.status.phase == "Concluded" and (.status.seq // 0) > 0 and (.status.record == null or
      ((.metadata.finalizers // []) | index("delivery.pacto.trianalab.io/record")))))] | length == 0' >/dev/null
  }
  eventually 80 all_recorded || fail "operations still wait for a record: $(kubectl get operations -A -o json | jq -c '[.items[] |
    select(.status.phase == "Concluded" and .status.record == null) | "\(.metadata.namespace)/\(.metadata.name)"]')"
  local outcomes
  outcomes="$(kubectl get operations -A -o json | jq -r '[.items[] | select(.status.record) | .status.outcome] | unique | join(",")')"
  for o in Succeeded Failed Rejected Witnessed; do
    grep -q "$o" <<<"$outcomes" || fail "no recorded operation ended $o (recorded outcomes: $outcomes)"
  done
  # Cancelling deletes the operation; its record is what is left of it.
  rec history -n test --stage payments --output-format json | jq -e '[.entries[] | select(.outcome == "Cancelled" and .record != "")] | length >= 1' >/dev/null \
    || fail "test's cancelled operation has no record"
  pass "every concluded operation has a record and lost its finalizer; recorded outcomes: $outcomes, Cancelled"

  section "record verify passes on every Stage, with no gap and no seq recorded twice"
  local s ns st out seq n=0
  for s in $(kubectl get stages -A -o json | jq -r '.items[] | select(.status.stream.anchor) | "\(.metadata.namespace)/\(.metadata.name)"'); do
    ns="${s%%/*}" st="${s#*/}"
    out="$(rec record verify -n "$ns" --stage "$st" --output-format json 2>"$tmp/err")" || fail "record verify failed on $s: $(cat "$tmp/err") $out"
    seq="$(kubectl -n "$ns" get stage "$st" -o jsonpath='{.status.stream.seq}')"
    jq -e --argjson seq "${seq:-0}" '.ok and (.records | length) == $seq and ([.records[] | select(.kind == "gap")] | length) == 0 and
      ([.records[].seq] | unique | length) == (.records | length)' <<<"$out" >/dev/null \
      || fail "the records of $s do not match its $seq seqs: $out"
    n=$((n + 1))
  done
  [ "$n" -ge 4 ] || fail "only $n Stages have a stream anchor"
  pass "record verify passes on all $n Stages, one record per seq"

  section "the fleet audit: record verify --all and history --all read every Stage in one walk"
  # One walk over every Stage of the run gives the same answer as one Stage at
  # a time: every stream verifies, each with one record per seq.
  local want total
  want="$(kubectl get stages -A -o json | jq -c '[.items[] | {key: "\(.metadata.namespace)/\(.metadata.name)", value: (.status.stream.seq // 0)}] | from_entries')"
  total="$(jq length <<<"$want")"
  out="$(rec record verify --all --output-format json 2>"$tmp/err")" || fail "record verify --all failed: $(cat "$tmp/err") $out"
  jq -e --argjson want "$want" '.ok and .completeness == "complete" and (.unreadable // [] | length) == 0 and all(.streams[]; .ok)
    and ([.streams[] | {key: .stage, value: (.records | length)}] | from_entries) == $want' <<<"$out" >/dev/null \
    || fail "record verify --all does not verify every Stage with one record per seq ($want): $(jq -c '{ok, completeness, unreadable, streams: [.streams[] | {stage, ok, records: (.records | length)}]}' <<<"$out")"
  out="$(rec history --all --output-format json 2>"$tmp/err")" || fail "history --all failed: $(cat "$tmp/err") $out"
  jq -e --argjson total "$total" '.completeness == "complete" and (.unreadable // [] | length) == 0 and (.histories | length) == $total
    and all(.histories[]; .source == "records" and .completeness == "complete")' <<<"$out" >/dev/null \
    || fail "history --all does not read all $total Stages from their records: $(jq -c '{completeness, note, unreadable, histories: [.histories[] | {namespace, source, completeness}]}' <<<"$out")"
  pass "record verify --all verifies all $total Stages, one record per seq; history --all reads all $total from their records"

  section "a record is attached to the version, the one it replaced, the contract and the anchor"
  local opc_uid ctr
  opc_uid="$(uid_of test "$OPC")"
  ctr="${C120##*@}"
  [ "$(records_on apps/payments "$C" "$opc_uid")" = 1 ] || fail "C's failed promote is not recorded on C"
  [ "$(records_on apps/payments "$B" "$opc_uid")" = 1 ] || fail "C's failed promote is not recorded on B, the version it replaced"
  [ "$(records_on demo/payments "$ctr" "$opc_uid")" = 1 ] || fail "C's failed promote is not recorded on its contract"
  [ "$(records_on "$STREAMS_REPO" "$(anchor_of test payments)" "$opc_uid")" = 1 ] || fail "C's failed promote is not on the stream anchor"
  [ "$(records_on apps/billing "$FOREIGN" "$(uid_of "$P" foreign)")" = 1 ] || fail "the refused promote of the unsigned artifact is not recorded on it"
  pass "the failed promote of C is on C, B, its contract and the anchor; the refusal of 9.9.9 is on 9.9.9"

  section "cosign verifies a record with the recorder's public key, no Pacto involved"
  local ad manifest layer
  ad="$(anchor_of test payments)"
  manifest="$(oras discover --plain-http --format json "$streams_local@$ad" \
    | jq -r --arg u "$opc_uid" '[.. | objects | select((.annotations // {})["dev.pacto.uid"] == $u)][0].digest')"
  layer="$(oras manifest fetch --plain-http "$streams_local@$manifest" | jq -r '.layers[0].digest')"
  oras blob fetch --plain-http --output "$tmp/c.json" "$streams_local@$layer" >/dev/null
  cosign verify-blob-attestation --bundle "$tmp/c.json" --key "$RECORDER_PUB" --type delivery.pacto.trianalab.io/operation/v1 \
    --insecure-ignore-tlog --digest "${ad#sha256:}" --digestAlg sha256 >/dev/null 2>&1 || fail "cosign does not verify C's record"
  jq -r '.dsseEnvelope.payload' "$tmp/c.json" | base64 -d | jq -e --arg c "$C" \
    '.predicate.outcome.result == "Failed" and .predicate.intent.params.to == $c and .predicate.actor.class == "controller"' >/dev/null \
    || fail "C's record does not say the promoter's promote of C failed"
  pass "cosign verify-blob-attestation verifies C's record, which says the promote of C failed"

  section "history and blame answer from the records"
  local first h b
  first="$(kubectl -n prod get operations -o json | jq -r '[.items[] | select(.status.record)] | sort_by(.status.seq) | .[0].metadata.name')"
  # Admission keeps a moved operation from hand deletes; the namespace
  # controller may still remove it, which is how a cluster forgets one.
  kubectl -n prod delete operation "$first" --wait=true \
    --as system:serviceaccount:kube-system:namespace-controller >/dev/null || fail "the namespace controller could not remove $first"
  h="$(rec history -n prod --stage payments --output-format json)"
  jq -e --arg n "$first" '.source == "records" and .completeness == "complete" and
    ([.entries[] | select(.operation == $n and .record != "")] | length) == 1' <<<"$h" >/dev/null \
    || fail "history does not read $first from its record once the cluster forgot it: $(jq -c '{source, completeness, note, problems, unreadable}' <<<"$h")"
  b="$(rec blame -n test --stage payments --output-format json)"
  jq -e --arg f "$F" --arg op "$OPF" '.source == "records" and .completeness == "complete" and .digest == $f and .change.operation == $op and
    (.approvals | map(select(.by == "alice" and .note == "checkout migrates next sprint")) | length) == 1' <<<"$b" >/dev/null \
    || fail "blame does not name alice's approval of F in test: $b"
  pass "history reads the deleted $first from its record; blame names $OPF and alice's approval"

  section "undo returns a plan and proposes nothing; a plan cites the records of its version"
  local before after u p
  before="$(ops test | jq '.items | length')"
  u="$(rec undo -n test "$OPF" --output-format json)"
  jq -e --arg op "$OPF" '.kind == "rollback" and .plan.intentDigest != "" and (.command | contains("--undoes " + $op))' <<<"$u" >/dev/null \
    || fail "undo of $OPF is not a rollback plan: $u"
  after="$(ops test | jq '.items | length')"
  [ "$before" = "$after" ] || fail "undo proposed something: $before operations before, $after after"
  p="$(rec plan -n prod --stage payments --to "$C" --output-format json)"
  jq -e '.precedent.completeness == "complete" and
    (.precedent.entries | map(select(.outcome == "Failed" and (.stream | endswith("/test/payments")))) | length) >= 1' <<<"$p" >/dev/null \
    || fail "a plan of C in prod does not cite C's failure in test: $(jq -c .precedent <<<"$p")"
  pass "undo plans a rollback of $OPF and proposes nothing; planning C in prod cites its failure in test"

  section "only the recorder moves the stream head"
  out="$(kubectl -n test patch stage payments --subresource=status --type merge -p '{"status":{"stream":{"head":"sha256:forged"}}}' 2>&1 || true)"
  grep -q "written only by the Pacto delivery recorder" <<<"$out" || fail "an admin moved the stream head: $out"
  pass "admission refuses a stream head written by anyone but the recorder"
  # One change per request, so each pin is proven on its own.
  local kind name edit
  while IFS=$'\t' read -r kind name edit; do
    out="$(kubectl -n test get "$kind" "$name" -o json | jq "$edit" |
      kubectl --as "system:serviceaccount:$DNS:pacto-operator-delivery-recorder" replace --subresource=status -f - 2>&1 || true)"
    grep -q "the Pacto delivery recorder writes records, never what runs" <<<"$out" || fail "the recorder wrote $edit on $kind/$name: $out"
  done <<EOF
stage	payments	.status.locks = {"pointer": "forged"}
stage	payments	.status.stream.concluded += [{"seq": 999999, "uid": "forged"}]
operation	$OPF	.status.plan.floor.owners = ["team-mallory"]
operation	$OPF	.status.actorClass = (if .status.actorClass == "controller" then "human" else "controller" end)
EOF
  pass "admission refuses the recorder a write to a Stage's locks or conclusions, or to an operation's plan or actor class"

}

# forge_record STATEMENT OUT — sign a statement as the recorder does (DSSE in a
# sigstore bundle, Ed25519), with openssl and the recorder's own key: a record
# only the chain check can refuse.
forge_record() {
  local d pt="application/vnd.in-toto+json" hint
  d="$(mktemp -d)"
  jq -cj . "$1" > "$d/payload"
  printf 'DSSEv1 %d %s %d ' "${#pt}" "$pt" "$(wc -c < "$d/payload" | tr -d ' ')" > "$d/pae"
  cat "$d/payload" >> "$d/pae"
  openssl pkeyutl -sign -inkey "$RECORDER_KEY" -rawin -in "$d/pae" -out "$d/sig"
  hint="$(openssl pkey -in "$RECORDER_KEY" -pubout -outform DER | openssl dgst -sha256 -r | cut -d' ' -f1)"
  jq -n --arg p "$(base64 < "$d/payload" | tr -d '\n')" --arg s "$(base64 < "$d/sig" | tr -d '\n')" --arg h "$hint" \
    '{mediaType: "application/vnd.dev.sigstore.bundle.v0.3+json", verificationMaterial: {publicKey: {hint: $h}},
      dsseEnvelope: {payload: $p, payloadType: "application/vnd.in-toto+json", signatures: [{sig: $s}]}}' > "$2"
}
