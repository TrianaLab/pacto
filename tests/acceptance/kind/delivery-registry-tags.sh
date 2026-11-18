#!/usr/bin/env bash
# Phase 9 ("registrytags") of the delivery kind acceptance, sourced by
# delivery.sh, whose state and helpers it reads. It runs last, after every
# other phase and the records audit, because it moves the run's one recorder
# from zot to CNCF distribution, a registry that serves no OCI Referrers API
# (GHCR does not either): the Stages recorded on zot are not written again.
# It proves against the cluster and the registry:
#   - a Stage recorded as native never narrows: admission refuses even the
#     recorder writing write-once-tags over it;
#   - a new Stage on distribution records on write-once tags: RecordStoreReady
#     is WriteOnceTags and status.stream.registryMode is write-once-tags;
#   - a person's Approval made with kubectl gets its signed record, the
#     operation proceeds, and the streams repository holds the operation,
#     intent and approval records as stream-<sha256>-<seq>-<hex12>, -intent-
#     and -approval- tags, and no sha256- referrers-tag index;
#   - `pacto delivery record verify <tag ref>` with no kubeconfig verifies all
#     three and says the approval counted;
#   - `pacto publish` with a key signs onto pacto-sig-<subject hex>-<hex12>
#     tags, and a second publish finds those signatures and writes nothing.
# The CLI runs on the host: HTTP_PROXY sends its plain-HTTP registry traffic
# to the port-forward of distribution, so the in-cluster names resolve.

TAGS_REG_PORT=5604

phase_registrytags() {
  section "phase 9: records, approvals and signatures on a registry without the Referrers API"
  local G=registry-tags T=team-receipts tags_host streams native out pid tmp
  tags_host="pacto-registry-tags.${NS}.svc.cluster.local:5000"
  streams="${tags_host}/${STREAMS_REPO}"
  tmp="$(mktemp -d)"

  section "a Stage recorded as native never narrows, the recorder included"
  native="$(kubectl get stages -A -o json | jq -r 'first(.items[] | select(.status.stream.registryMode == "native") | "\(.metadata.namespace)/\(.metadata.name)") // ""')"
  [ -n "$native" ] || fail "no Stage of the run is recorded as native on zot"
  narrow() {
    out="$(kubectl -n "${native%/*}" get stage "${native#*/}" -o json | jq '.status.stream.registryMode = "write-once-tags"' |
      kubectl --as "system:serviceaccount:${DNS}:pacto-operator-delivery-recorder" replace --subresource=status -f - 2>&1 || true)"
    grep -q "may not narrow to write-once-tags" <<<"$out"
  }
  eventually 20 narrow || fail "the recorder narrowed $native from native to write-once-tags: $out"
  [ "$(kubectl -n "${native%/*}" get stage "${native#*/}" -o jsonpath='{.status.stream.registryMode}')" = native ] \
    || fail "$native is no longer native"
  pass "$native stays native: the -records policy refused the recorder writing write-once-tags"

  section "the recorder moves to distribution"
  install_noreferrers_registry >/dev/null
  pid="$(pf "$TAGS_REG_PORT" svc/pacto-registry-tags 5000)" || fail "could not port-forward distribution"
  PHASE_PIDS+=" $pid"
  helm upgrade pacto-operator "$OP_CHART" -n "$NS" --reuse-values \
    --set-json "delivery.insecureRegistries=[\"$REG_HOST\",\"$tags_host\"]" \
    --set delivery.recorder.streamsRepository="$streams" \
    --set delivery.recorder.approvalsRepository="$streams-approvals" \
    --set-json 'delivery.identities.signers=[{"issuer":"https://idp.kind.invalid","subjects":"[^@]+@acme\\.example"}]' \
    --wait --timeout 240s >/dev/null || fail "the pacto-operator chart did not roll out the recorder on distribution"
  kubectl -n "$DNS" rollout status deploy/pacto-operator-delivery-recorder --timeout=120s >/dev/null \
    || fail "the recorder did not roll out on distribution"
  pass "the recorder writes to $streams"

  section "a new Stage records on write-once tags"
  kubectl create namespace "$G" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  people_may_update "$G"
  kubectl -n "$G" create secret generic cosign-pub --from-file=cosign.pub="$KEYDIR/cosign.pub" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  local R1
  R1="$(publish R1 "$(contract receipts 1.0.0 service)" receipts)"; candidate R1 receipts
  kubectl -n "$G" apply -f - >/dev/null <<YAML
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata: {name: receipts-candidates}
spec: {interval: $CANDIDATE_INTERVAL, insecure: true, url: "oci://${REG_HOST}/apps/receipts", ref: {tag: candidate}, $VERIFY}
---
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata:
  name: receipts
  labels: {delivery.pacto.trianalab.io/apply-ref: receipts}
spec: {interval: 30s, insecure: true, url: "oci://${REG_HOST}/apps/receipts", ref: {}, $VERIFY}
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata: {name: receipts}
spec:
  interval: 1m
  sourceRef: {kind: OCIRepository, name: receipts}
  path: ./
  prune: true
  timeout: 2m
  targetNamespace: $G
  healthChecks: [{apiVersion: apps/v1, kind: Deployment, name: receipts, namespace: $G}]
---
apiVersion: delivery.pacto.trianalab.io/v1alpha1
kind: Stage
metadata: {name: receipts}
spec:
  service: {name: receipts}
  environment: registry-tags
  applyRef: {name: receipts}
  candidates: {ociRepositoryRef: {name: receipts-candidates}}
  startLevel: advise
  gates: {impact: {declaredConsumersOnly: true}, verify: {window: 20s}}
YAML
  stage_field() { kubectl -n "$G" get stage receipts -o jsonpath="{.status.$1}"; }
  eventually 60 recording "$G" receipts || fail "the recorder never reported on $G/receipts: $(stage_field conditions)"
  [ "$(stage_field 'conditions[?(@.type=="RecordStoreReady")].reason')" = WriteOnceTags ] \
    || fail "RecordStoreReady on distribution is not WriteOnceTags: $(stage_field conditions)"
  [ "$(stage_field stream.registryMode)" = write-once-tags ] || fail "status.stream.registryMode is '$(stage_field stream.registryMode)'"
  [[ "$(stage_field stream.approvals)" == "$streams-approvals@sha256:"* ]] \
    || fail "the approvals anchor is not in $streams-approvals: $(stage_field stream.approvals)"
  pass "$G/receipts is recorded as write-once tags, its approvals anchored in $streams-approvals"

  section "a person's Approval gets its signed record and counts"
  eventually 60 published_in "$G" receipts "$R1" || fail "R1 was never verified as published"
  first "$G" receipts "$T" "$R1"
  local op appr aref iref seq
  op="$(move_level "$G" receipts "$T" auto-nonbreaking)"
  appr="$(kubectl -n "$G" get approvals -o json | jq -c --arg o "$op" 'first(.items[] | select(.spec.operation == $o and .spec.by == "carol")) // {}')"
  aref="$(jq -r '.status.record.ref // ""' <<<"$appr")"
  [[ "$aref" == "$streams@sha256:"* ]] || fail "carol's Approval of $op has no record in $streams: $appr"
  intent_of() { iref="$(field "$G" "$op" intentRecord.ref)"; [ -n "$iref" ]; }
  eventually 30 intent_of || fail "$op has no intent record"
  [[ "$iref" == "$streams@sha256:"* ]] || fail "the intent record of $op is not in $streams: $iref"
  recorded() { [ -n "$(field "$G" "$op" record.digest)" ]; }
  eventually 60 recorded || fail "$op concluded and was never recorded"
  seq="$(field "$G" "$op" record.seq)"
  pass "carol's Approval of $op carries its record, and $op proceeded and was recorded at seq $seq"

  section "the records are write-once tags, and no sha256- index"
  rtags() { oras repo tags --plain-http "127.0.0.1:${TAGS_REG_PORT}/$1" 2>/dev/null | sort; }
  hex12() { local d="${1##*@sha256:}"; echo "${d:0:12}"; }
  local anchor have optag itag atag
  anchor="stream-$(printf %s "$(stage_field stream.id)" | shasum -a 256 | cut -c1-64)"
  have="$(rtags "$STREAMS_REPO")"
  optag="$(grep -E "^${anchor}-${seq}-[0-9a-f]{12}$" <<<"$have")" || fail "no operation record tag ${anchor}-${seq}-<hex12>: $have"
  itag="${anchor}-intent-$(hex12 "$iref")"
  atag="${anchor}-approval-$(hex12 "$aref")"
  grep -qx "$itag" <<<"$have" || fail "no intent record tag $itag: $have"
  grep -qx "$atag" <<<"$have" || fail "no approval record tag $atag: $have"
  ! grep -q '^sha256-' <<<"$have" || fail "the streams repository has a sha256- referrers-tag index: $(grep '^sha256-' <<<"$have")"
  pass "$STREAMS_REPO holds $optag, $itag and $atag, and no sha256- tag"

  section "record verify reads every record by its tag with no kubeconfig"
  noconf() {
    HTTP_PROXY="http://127.0.0.1:${TAGS_REG_PORT}" PACTO_INSECURE_REGISTRIES="$tags_host" KUBECONFIG=/dev/null \
      "$PACTO" delivery --trust "$RECORDER_PUB" record verify "$streams:$1" --output-format json 2>"$tmp/err"
  }
  local t
  for t in "$optag" "$itag" "$atag"; do
    out="$(noconf "$t")" || fail "record verify $streams:$t failed with no kubeconfig: $(cat "$tmp/err") $out"
    jq -e '.verified' <<<"$out" >/dev/null || fail "$streams:$t does not verify: $out"
  done
  jq -e '.predicate == "delivery.pacto.trianalab.io/approval/v1" and .counted == "counted"' <<<"$out" >/dev/null \
    || fail "record verify does not say carol's approval counted: $out"
  pass "record verify, with KUBECONFIG=/dev/null, verified the operation, intent and approval records and says the approval counted"

  section "pacto publish signs onto pacto-sig tags, and a second publish finds them"
  local pubd dep before
  pubd="$(mktemp -d)"
  cp "$KEYDIR/cosign.key" "$pubd/cosign.key"
  cat > "$pubd/render.sh" <<'SH'
#!/bin/sh
set -e
printf 'apiVersion: v1\nkind: ConfigMap\nmetadata: {name: notes}\ndata: {version: "%s"}\n' "$PACTO_VERSION" > "$PACTO_OUT/configmap.yaml"
SH
  chmod +x "$pubd/render.sh"
  cat > "$pubd/pacto.yaml" <<YAML
pactoVersion: "2.1"
service:
  name: notes
  version: 1.0.0
  owner: {team: team-notes, dri: d, contacts: [{type: email, value: a@e.com, purpose: escalation}]}
workload: service
state: {type: stateless, persistence: {scope: local, durability: ephemeral}, dataCriticality: low}
publish:
  bundle: oci://${tags_host}/demo/notes
  deploy: manifests
  artifacts:
    - name: manifests
      type: manifests
      ref: oci://${tags_host}/apps/notes
      build: {command: [./render.sh]}
  sign: {key: cosign.key, transparencyLog: false}
YAML
  printf 'cosign.key\nrender.sh\n' > "$pubd/.pactoignore"
  git -C "$pubd" init -q && git -C "$pubd" add -A && git -C "$pubd" -c user.email=ci@e.com -c user.name=ci commit -qm notes
  tpub() {
    (cd "$pubd" && HTTP_PROXY="http://127.0.0.1:${TAGS_REG_PORT}" PACTO_INSECURE_REGISTRIES="$tags_host" COSIGN_PASSWORD="" "$PACTO" publish)
  }
  out="$(tpub 2>&1)" || fail "publish to distribution failed: $out"
  dep="$(grep -oE "^Deploy: ${tags_host}/apps/notes@sha256:[0-9a-f]{64}" <<<"$out" | sed 's/.*@sha256://')"
  [ -n "$dep" ] || fail "publish named no deploy artifact: $out"
  rtags apps/notes | grep -qE "^pacto-sig-${dep}-[0-9a-f]{12}$" || fail "no pacto-sig tag for the deploy artifact: $(rtags apps/notes | tr '\n' ' ')"
  rtags demo/notes | grep -qE '^pacto-sig-[0-9a-f]{64}-[0-9a-f]{12}$' || fail "no pacto-sig tag for the bundle: $(rtags demo/notes | tr '\n' ' ')"
  before="$(rtags apps/notes)|$(rtags demo/notes)"
  out="$(tpub 2>&1)" || fail "the second publish failed: $out"
  grep -q "Nothing to publish" <<<"$out" || fail "the second publish did not find its signatures on the pacto-sig tags: $out"
  [ "$before" = "$(rtags apps/notes)|$(rtags demo/notes)" ] || fail "the second publish changed the tags of distribution"
  pass "publish signed notes 1.0.0 onto pacto-sig tags; a second publish verified them and wrote nothing"
  echo "DELIVERY REGISTRY TAGS PASS"
}
