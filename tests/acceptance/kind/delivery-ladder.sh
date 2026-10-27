#!/usr/bin/env bash
# Phase 7 ("ladder") of the delivery kind acceptance, sourced by delivery.sh,
# whose state and helpers it reads. One service, wallet, runs in its own
# namespace beside a Flux OCIRepository that GitOps owns, and climbs the
# autonomy ladder the way a team would. It proves against the cluster:
#   - a Stage starts on observe: GitOps moving the applyRef raises nothing and
#     freezes nothing, and Pacto writes what GitOps' source reports (never
#     spec.ref) and what a fully automatic Stage would have done with it;
#   - the Stage's own verify gates run on what GitOps applied, so Pacto earns
#     a proposal from evidence, not from a status field it wrote about itself;
#   - leaving observe is refused until the applyRef is labelled; then one
#     person proposes advise and another confirms it, and the Stage adopts what
#     runs as its pointer, in a record cosign verifies with no Pacto involved;
#   - on advise a person's verified promote earns auto-nonbreaking, which a
#     person confirms again;
#   - on autonomous, a version the promoter shipped that fails its verify is
#     rolled back and steps the Stage down one rung, through a signed autonomy
#     operation the promoter raised with no approval.
# GitOps is played by `oras tag`: moving the applied tag is what a merged
# commit to a Flux source would do.

phase_ladder() {
  section "phase 7: a Stage climbs the autonomy ladder from observe"
  ! phase_runs core || await operator-back
  local L=ladder T=team-wallet
  kubectl create namespace "$L" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  kubectl -n "$L" create secret generic cosign-pub --from-file=cosign.pub="$KEYDIR/cosign.pub" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  local W100 W110 W130 W200 W1 W2 W3 W4
  W100="$(contract wallet 1.0.0 service)"
  W110="$(contract wallet 1.1.0 service)"
  W130="$(contract wallet 1.3.0 service)"
  # A major: changing the workload is BREAKING, and nothing consumes wallet, so
  # autonomous ships it with no person. It contradicts the Deployment that ships: NonCompliant.
  W200="$(contract wallet 2.0.0 job)"
  # applied LABEL — GitOps applies the version: the applied tag moves to it.
  applied() { oras tag --plain-http "127.0.0.1:${LOCAL_REG_PORT}/apps/wallet:$1" applied >/dev/null; }
  stage() { kubectl -n "$L" get stage wallet -o json; }
  st() { stage | jq -r "$1"; }
  observed_is() { [ "$(st '.status.observedRevision.digest // ""')" = "$1" ]; }
  seen_at() { stage | jq -e --arg d "$1" '.status.shadow // [] | map(select(.candidate == $d and (.observedAt // "") != "")) | length == 1' >/dev/null; }
  verified() { stage | jq -e --arg d "$1" '.status.verified // [] | index($d)' >/dev/null; }
  proposes() { [ "$(st '.status.proposal.rung // ""')" = "$1" ]; }
  ready_reason() { [ "$(st '.status.conditions[] | select(.type == "Ready") | .reason')" = "$1" ]; }
  no_ops() { [ "$(ops "$L" | jq '.items | length')" = 0 ]; }
  no_promote_of() { [ -z "$(op_to "$L" "$1")" ]; }

  W1="$(publish W1 "$W100" wallet)"; candidate W1 wallet; applied W1
  kubectl -n "$L" apply -f - >/dev/null <<YAML
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata: {name: wallet-candidates}
spec: {interval: $CANDIDATE_INTERVAL, insecure: true, url: "oci://${REG_HOST}/apps/wallet", ref: {tag: candidate}, $VERIFY}
---
# GitOps owns this one: no apply-ref label, and the version is a tag git moves.
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata: {name: wallet}
spec: {interval: 5s, insecure: true, url: "oci://${REG_HOST}/apps/wallet", ref: {tag: applied}, $VERIFY}
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata: {name: wallet}
spec:
  interval: 1m
  sourceRef: {kind: OCIRepository, name: wallet}
  path: ./
  prune: true
  timeout: 2m
  targetNamespace: $L
  healthChecks: [{apiVersion: apps/v1, kind: Deployment, name: wallet, namespace: $L}]
---
# No startRung: a Stage starts on observe.
apiVersion: delivery.pacto.trianalab.io/v1alpha1
kind: Stage
metadata: {name: wallet}
spec:
  service: {name: wallet}
  environment: ladder
  applyRef: {name: wallet}
  candidates: {ociRepositoryRef: {name: wallet-candidates}}
  autonomy: {proposeAfter: 1}
  gates: {impact: {declaredConsumersOnly: true}, verify: {window: 20s}}
YAML

  section "observe: Pacto reads what GitOps applied and verifies it, and moves nothing"
  eventually 60 observed_is "$W1" || fail "the Stage never observed W1: $(st .status)"
  [ "$(st .status.rung)" = observe ] || fail "a Stage with no startRung is on $(st .status.rung)"
  eventually 20 ready_reason Observing || fail "an observing Stage over an unlabelled applyRef is not Ready/Observing: $(st .status.conditions)"
  eventually 30 seen_at "$W1" || fail "no shadow entry for W1 with observedAt: $(st .status.shadow)"
  eventually 60 verified "$W1" || fail "the shadow verify never verified W1: $(st .status)"
  st '.status.proposal.blockers[].kind' | grep -qx unlabelled-apply-ref || fail "the proposal does not ask for the label: $(st .status.proposal)"
  no_ops || fail "observing raised an operation: $(ops "$L" | jq -c '[.items[].metadata.name]')"
  pass "observe: W1 observed from status.artifact.revision, planned, verified by the Stage's own gates; nothing raised"

  section "GitOps bumps the version: no freeze, a shadow entry, a verify"
  W2="$(publish W2 "$W110" wallet)"; candidate W2 wallet
  eventually 60 published_in "$L" "$W2" wallet || fail "W2 was never verified as published"
  applied W2
  eventually 60 observed_is "$W2" || fail "the Stage never observed W2"
  eventually 30 seen_at "$W2" || fail "no shadow entry for W2 with observedAt: $(st .status.shadow)"
  stage | jq -e --arg d "$W2" '.status.shadow[] | select(.candidate == $d) | .predicted == "Proceeds"' >/dev/null \
    || fail "a fully automatic Stage would not have shipped W2: $(st .status.shadow)"
  eventually 60 verified "$W2" || fail "the shadow verify never verified W2"
  { no_ops && [ "$(st '.status.frozen // false')" = false ]; } || fail "a GitOps move froze the Stage or raised an operation: $(st .status)"
  pass "GitOps moved W1 to W2: Pacto would have shipped it, verified it and froze nothing"

  section "leaving observe needs the label; then a person proposes advise and another confirms"
  local up
  up=wallet-advise-unlabelled
  as_person alice "$T" propose -n "$L" --stage wallet --type autonomy --to advise --name "$up" --reason "phase 7" >/dev/null
  wait_concluded "$L" "$up" Rejected/
  field "$L" "$up" message | grep -q unlabelled-apply-ref || fail "the refusal does not name the label: $(field "$L" "$up" message)"
  # The person pins what runs and labels the applyRef; from here the pointer
  # field is out of git, and admission lets only the promoter write it.
  kubectl -n "$L" patch ocirepository wallet --type merge -p "{\"spec\":{\"ref\":{\"tag\":null,\"digest\":\"$W2\"}}}" >/dev/null
  kubectl -n "$L" label ocirepository wallet "delivery.pacto.trianalab.io/apply-ref=wallet" >/dev/null
  eventually 30 ready_reason ObservingGuarded || fail "a labelled applyRef on observe is not ObservingGuarded: $(st .status.conditions)"
  eventually 30 proposes advise || fail "Pacto never proposed advise: $(st .status.proposal)"
  stage | jq -e --arg d "$W2" '.status.proposal.evidence == [$d]' >/dev/null || fail "the proposal does not cite W2: $(st .status.proposal)"
  up=wallet-advise
  as_person alice "$T" propose -n "$L" --stage wallet --type autonomy --to advise --name "$up" --reason "phase 7: Pacto proposes advise" >/dev/null
  eventually 30 held "$L" "$up" || fail "a raise did not wait for a person: $(field "$L" "$up" phase) $(field "$L" "$up" message)"
  decide "$L" "$up" alice "$T" approve "my own raise"
  always 3 held "$L" "$up" || fail "alice confirmed her own raise"
  decide "$L" "$up" carol "$T" approve "the shadow verified what GitOps shipped"
  wait_concluded "$L" "$up" Succeeded/Unverified
  [ "$(st .status.rung)" = advise ] && [ "$(st .status.pointer)" = "$W2" ] || fail "advise did not adopt W2: $(st .status)"
  [ "$(field "$L" "$up" pointer.after)" = "$W2" ] || fail "the operation does not show the adopted pointer"
  ladder_record "$L" "$up" | jq -e --arg d "$W2" '.predicate.outcome.rung == {from: "observe", to: "advise"} and
    .predicate.outcome.pointer.after == $d and (.predicate.approvals | map(select(.by == "carol")) | length) == 1' >/dev/null \
    || fail "the record of $up does not show observe -> advise, the adopted W2 and carol"
  pass "unlabelled: refused; labelled: alice proposed advise, carol confirmed, the Stage adopted W2 and cosign verifies the record"

  section "a person's verified promote on advise earns auto-nonbreaking, which a person confirms"
  W3="$(publish W3 "$W130" wallet)"; candidate W3 wallet
  eventually 60 published_in "$L" "$W3" wallet || fail "W3 was never verified as published"
  always 3 no_promote_of "$W3" || fail "an advise Stage promoted W3 by itself"
  as_person alice "$T" propose -n "$L" --stage wallet --to "$W3" --name wallet-w3 --reason "phase 7" >/dev/null
  wait_concluded "$L" wallet-w3 Succeeded/Verified
  eventually 30 proposes auto-nonbreaking || fail "Pacto never proposed auto-nonbreaking: $(st .status.proposal)"
  up="$(move_rung "$L" wallet "$T" auto-nonbreaking)"
  kubectl -n "$L" get operation "$up" -o json | jq -e '.status.plan.floor.decision == "approve" and
    (.status.verdicts[] | select(.gate == "pacto.autonomy") | .result == "Pass")' >/dev/null \
    || fail "the raise to auto-nonbreaking was not one Pacto proposed: $(kubectl -n "$L" get operation "$up" -o jsonpath='{.status.plan}')"
  pass "W3 verified on advise; Pacto proposed auto-nonbreaking and carol confirmed it"

  section "on autonomous a failed automatic promote steps the Stage down one rung, signed and with no approval"
  # Pacto has not seen a breaking change cleared yet, so carol accepts the unknown.
  up="$(move_rung "$L" wallet "$T" autonomous)"
  [ "$(field "$L" "$up" plan.floor.decision)" = accept-unknown ] || fail "an unearned raise did not ask to accept the unknown"
  W4="$(publish W4 "$W200" wallet)"; candidate W4 wallet
  local OP4 SD
  OP4="$(wait_op "$L" "$W4")"
  wait_concluded "$L" "$OP4" Failed/
  [ "$(field "$L" "$OP4" actorClass)" = controller ] || fail "W4 was not promoted by the promoter"
  SD="$OP4-stepdown"
  wait_concluded "$L" "$SD" Succeeded/Unverified
  kubectl -n "$L" get operation "$SD" -o json | jq -e '.spec.params == {from: "autonomous", to: "auto-nonbreaking"} and
    .status.actorClass == "controller" and (.status.approvals // [] | length) == 0' >/dev/null \
    || fail "the step-down is not the promoter's one-rung lowering with no approval: $(kubectl -n "$L" get operation "$SD" -o json | jq -c '{spec: .spec.params, status: {actorClass: .status.actorClass, approvals: .status.approvals}}')"
  [ "$(st .status.rung)" = auto-nonbreaking ] || fail "the Stage is on $(st .status.rung) after the failure"
  wait_concluded "$L" "$OP4-rollback" Succeeded/Verified
  [ "$(st .status.pointer)" = "$W3" ] || fail "W4 was not rolled back to W3"
  ladder_record "$L" "$SD" | jq -e '.predicate.outcome.rung == {from: "autonomous", to: "auto-nonbreaking"} and
    .predicate.actor.class == "controller" and (.predicate.approvals // [] | length) == 0' >/dev/null \
    || fail "the record of $SD does not show the promoter's step-down"
  st '.status.proposal.blockers[].kind' | grep -qx needs-verified || fail "the step-down did not restart the count: $(st .status.proposal)"
  pass "W4 failed its verify: rolled back to W3, and the promoter stepped wallet down to auto-nonbreaking in a signed record nobody approved"
  echo "DELIVERY LADDER PASS"
}

# ladder_record NS OP — the signed record of OP, verified with cosign against
# the recorder's public key and printed as its in-toto statement.
ladder_record() {
  local streams="127.0.0.1:${LOCAL_REG_PORT}/${STREAMS_REPO}" uid anchor manifest layer f
  uid="$(kubectl -n "$1" get operation "$2" -o jsonpath='{.metadata.uid}')"
  has_record() { [ -n "$(field "$1" "$2" record.digest)" ]; }
  eventually 40 has_record "$1" "$2" || fail "$1/$2 was never recorded"
  anchor="$(kubectl -n "$1" get stage wallet -o jsonpath='{.status.stream.anchor}')"
  anchor="${anchor##*@}"
  manifest="$(oras discover --plain-http --format json "$streams@$anchor" \
    | jq -r --arg u "$uid" '[.. | objects | select((.annotations // {})["dev.pacto.uid"] == $u)][0].digest')"
  layer="$(oras manifest fetch --plain-http "$streams@$manifest" | jq -r '.layers[0].digest')"
  f="$(mktemp)"
  oras blob fetch --plain-http --output "$f" "$streams@$layer" >/dev/null
  cosign verify-blob-attestation --bundle "$f" --key "$RECORDER_PUB" --type delivery.pacto.trianalab.io/operation/v1 \
    --insecure-ignore-tlog --digest "${anchor#sha256:}" --digestAlg sha256 >/dev/null 2>&1 || fail "cosign does not verify the record of $1/$2"
  jq -r '.dsseEnvelope.payload' "$f" | base64 -d
}
