#!/usr/bin/env bash
# Phase 8 ("appliers") of the delivery kind acceptance, sourced by delivery.sh,
# whose state and helpers it reads. One service, invoices, ships as a Helm
# chart through a Flux HelmRelease whose spec.chartRef names an OCIRepository.
# Nobody tells Pacto who applies it. It proves against the cluster:
#   - the chart installs --flux-helm with delivery.flux.enabled left on auto,
#     because Flux was installed first;
#   - the promoter detects, without writing, that helm-controller manages the
#     workload and adopts the HelmRelease as the Stage's applier;
#   - a promote writes the chart digest on the chart OCIRepository, and ships
#     and verifies once the HelmRelease is Ready at that digest;
#   - a Stage whose applyRef is an OCIRepository while its workload carries the
#     HelmRelease labels reads Ready False ApplierConflict, and a promote on it
#     is Rejected;
#   - a HelmRelease whose chartRef names an OCIRepository in another namespace
#     reads Ready False ApplierUnreadable, and a promote on its Stage never
#     writes that OCIRepository.
# The core phase checks the Kustomization case on its own Stage.

HELM_CHART_LAYER=application/vnd.cncf.helm.chart.content.v1.tar+gzip
HELM_CHART_CONFIG=application/vnd.cncf.helm.config.v1+json

# appliers_chart VERSION CONTRACT_REF — the invoices chart at VERSION: a
# Deployment and its Pacto, pushed to charts/invoices as a Helm OCI artifact
# naming its contract and signed, the way a publisher would. Prints the digest.
appliers_chart() {
  local v="$1" ref="$2" d digest
  d="$(mktemp -d)"
  mkdir -p "$d/invoices/templates"
  printf 'apiVersion: v2\nname: invoices\nversion: %s\n' "$v" > "$d/invoices/Chart.yaml"
  cat > "$d/invoices/templates/deployment.yaml" <<YAML
apiVersion: apps/v1
kind: Deployment
metadata: {name: invoices}
spec:
  replicas: 1
  selector: {matchLabels: {app: invoices}}
  template:
    metadata: {labels: {app: invoices}, annotations: {version: "$v"}}
    spec:
      containers: [{name: app, image: registry.k8s.io/pause:3.9}]
YAML
  cat > "$d/invoices/templates/pacto.yaml" <<YAML
apiVersion: pacto.trianalab.io/v1
kind: Pacto
metadata: {name: invoices}
spec:
  checkIntervalSeconds: 30
  contractRef: {oci: "$ref"}
  target: {workloadRef: {name: invoices, kind: Deployment}}
YAML
  helm package "$d/invoices" -d "$d" >/dev/null || fail "helm could not package the invoices chart $v"
  printf '{"apiVersion":"v2","name":"invoices","version":"%s"}' "$v" > "$d/config.json"
  digest="$(cd "$d" && oras push --plain-http --annotation "delivery.pacto.trianalab.io/contract=$ref" \
    --config "config.json:$HELM_CHART_CONFIG" --format go-template='{{.digest}}' \
    "127.0.0.1:${LOCAL_REG_PORT}/charts/invoices:$v" "invoices-$v.tgz:$HELM_CHART_LAYER")"
  [ -n "$digest" ] || fail "pushing the invoices chart $v resolved no digest"
  sign_digest charts/invoices "$digest"
  echo "$digest"
}

# appliers_ns NS — a namespace alice and carol may update, trusting the run's key.
appliers_ns() {
  kubectl create namespace "$1" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  people_may_update "$1"
  kubectl -n "$1" create secret generic cosign-pub --from-file=cosign.pub="$KEYDIR/cosign.pub" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
}

# chart_source NAME REF [LABEL] — an OCIRepository of the invoices chart at
# REF, labelled as the applyRef of the Stage LABEL when one is given.
chart_source() {
  local labels=""
  [ -z "${3:-}" ] || labels="labels: {delivery.pacto.trianalab.io/apply-ref: $3}"
  cat <<YAML
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata:
  name: $1
  $labels
spec:
  interval: $CANDIDATE_INTERVAL
  insecure: true
  url: "oci://${REG_HOST}/charts/invoices"
  ref: $2
  layerSelector: {mediaType: "$HELM_CHART_LAYER", operation: copy}
  $VERIFY
YAML
}

# ready_reason NS STAGE — the reason of the Stage's Ready condition.
ready_reason() { kubectl -n "$1" get stage "$2" -o jsonpath='{.status.conditions[?(@.type=="Ready")].reason}'; }
ready_reason_is() { [ "$(ready_reason "$1" "$2")" = "$3" ]; }
chart_digest() { kubectl -n "$1" get ocirepository "$2" -o jsonpath='{.spec.ref.digest}'; }
chart_on() { [ "$(chart_digest "$1" "$2")" = "$3" ]; } # NS NAME DIGEST

phase_appliers() {
  section "phase 8: Pacto adopts the HelmRelease that applies a service, and never fights another applier"
  ! phase_runs core || await operator-back
  local A=appliers C=appliers-conflict F=appliers-foreign T=team-invoices
  local I100 I110 V1 V2 OPC OPF n
  kubectl -n "$DNS" get deploy pacto-operator-delivery -o json | jq -e '[.spec.template.spec.containers[].args[]?] | index("--flux-helm")' >/dev/null \
    || fail "the promoter runs without --flux-helm: delivery.flux.enabled is auto and Flux's HelmRelease API was served before the install"
  pass "delivery.flux.enabled left on auto turned on --flux-helm: Flux was installed first"
  for n in "$A" "$C" "$F"; do appliers_ns "$n"; done
  kubectl -n flux-system scale deploy helm-controller --replicas=1 >/dev/null
  infra "roll out Flux's helm-controller" kubectl -n flux-system rollout status deploy/helm-controller --timeout=240s
  I100="$(contract invoices 1.0.0 service)"
  I110="$(contract invoices 1.1.0 service)"
  V1="$(appliers_chart 0.1.0 "$I100")"
  echo "  V1 = $V1 (invoices chart 0.1.0, contract 1.0.0)"

  section "a HelmRelease applies invoices from its chart OCIRepository; the Stage names it and nothing else"
  # helm-controller installs each version as this account, never as itself.
  # It waits on nothing: the promoter's verify, not helm-controller, must
  # refuse a result for an old version (as the core phase's healthChecks).
  {
    chart_source invoices-chart "{digest: \"$V1\"}" invoices
    echo ---
    chart_source invoices-candidates '{semver: ">=0.0.0"}'
    cat <<YAML
---
apiVersion: v1
kind: ServiceAccount
metadata: {name: invoices-deployer}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata: {name: invoices-deployer}
rules:
- {apiGroups: [apps], resources: [deployments], verbs: ["*"]}
- {apiGroups: [pacto.trianalab.io], resources: [pactos], verbs: ["*"]}
- {apiGroups: [""], resources: [secrets], verbs: ["*"]}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata: {name: invoices-deployer}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: invoices-deployer}
subjects: [{kind: ServiceAccount, name: invoices-deployer}]
---
apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: invoices
  labels: {delivery.pacto.trianalab.io/apply-ref: invoices}
spec:
  interval: 1m
  timeout: 2m
  chartRef: {kind: OCIRepository, name: invoices-chart}
  serviceAccountName: invoices-deployer
  install: {disableWait: true}
  upgrade: {disableWait: true}
---
apiVersion: delivery.pacto.trianalab.io/v1
kind: Stage
metadata: {name: invoices}
spec:
  service: {name: invoices}
  environment: appliers
  applyRef: {kind: HelmRelease, name: invoices}
  candidates: {ociRepositoryRef: {name: invoices-candidates}}
  startLevel: advise
  gates: {impact: {declaredConsumersOnly: true}, verify: {window: 3m}}
YAML
  } | kubectl -n "$A" apply -f - >/dev/null
  eventually 120 reconciled "$A" invoices || fail "$A/invoices was never reconciled: $(kubectl -n "$A" get helmrelease invoices -o jsonpath='{.status.conditions}')"
  eventually 60 applier_is "$A" invoices "flux/Adopted/HelmRelease $A/invoices/helm-controller labels" \
    || fail "$A/invoices did not adopt its HelmRelease: $(kubectl -n "$A" get stage invoices -o jsonpath='{.status.applier}')"
  pass "the promoter detected helm-controller on invoices and adopted HelmRelease $A/invoices; no value named it"

  section "a promote writes the chart digest, and ships once the HelmRelease is Ready at it"
  eventually 60 published_in "$A" invoices "$V1" || fail "chart 0.1.0 was never verified as published in $A"
  first "$A" invoices "$T" "$V1"
  V2="$(appliers_chart 0.2.0 "$I110")"
  eventually 60 published_in "$A" invoices "$V2" || fail "chart 0.2.0 was never verified as published in $A"
  as_person alice "$T" propose -n "$A" --stage invoices --to "$V2" --name invoices-0-2-0 --reason "the kind run ships chart 0.2.0" >/dev/null
  wait_concluded "$A" invoices-0-2-0 Succeeded/Verified
  [ "$(chart_digest "$A" invoices-chart)" = "$V2" ] || fail "the chart OCIRepository is on $(chart_digest "$A" invoices-chart), want $V2"
  [ "$(kubectl -n "$A" get deploy invoices -o jsonpath='{.spec.template.metadata.annotations.version}')" = 0.2.0 ] \
    || fail "the cluster does not run invoices chart 0.2.0"
  pass "alice promoted chart 0.2.0: the promoter wrote its digest on invoices-chart, helm-controller released it and the contract check verified it"

  section "a Stage whose applyRef is not the workload's manager never promotes"
  # The workload carries helm-controller's labels; the Stage applies through
  # an OCIRepository with no Kustomization.
  {
    chart_source invoices "{digest: \"$V1\"}" invoices
    echo ---
    chart_source invoices-candidates '{tag: "0.2.0"}'
    cat <<YAML
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: invoices
  labels: {helm.toolkit.fluxcd.io/name: invoices, helm.toolkit.fluxcd.io/namespace: $C}
spec:
  replicas: 1
  selector: {matchLabels: {app: invoices}}
  template:
    metadata: {labels: {app: invoices}}
    spec: {containers: [{name: app, image: registry.k8s.io/pause:3.9}]}
---
apiVersion: pacto.trianalab.io/v1
kind: Pacto
metadata: {name: invoices}
spec:
  checkIntervalSeconds: 30
  contractRef: {oci: "$I100"}
  target: {workloadRef: {name: invoices, kind: Deployment}}
---
apiVersion: delivery.pacto.trianalab.io/v1
kind: Stage
metadata: {name: invoices}
spec:
  service: {name: invoices}
  environment: appliers-conflict
  applyRef: {name: invoices}
  candidates: {ociRepositoryRef: {name: invoices-candidates}}
  startLevel: advise
  gates: {impact: {declaredConsumersOnly: true}, verify: {window: 3m}}
YAML
  } | kubectl -n "$C" apply -f - >/dev/null
  eventually 60 reconciled "$C" invoices || fail "$C/invoices was never reconciled"
  eventually 60 ready_reason_is "$C" invoices ApplierConflict \
    || fail "$C/invoices is Ready $(ready_reason "$C" invoices), want ApplierConflict: $(kubectl -n "$C" get stage invoices -o jsonpath='{.status.applier}')"
  eventually 60 published_in "$C" invoices "$V2" || fail "chart 0.2.0 was never verified as published in $C"
  OPC=invoices-conflict
  as_person alice "$T" propose -n "$C" --stage invoices --to "$V2" --name "$OPC" --reason "the kind run proposes on a conflicting Stage" >/dev/null
  wait_concluded "$C" "$OPC" Rejected/
  field "$C" "$OPC" reason | grep -q '^ApplierConflict' || fail "$C/$OPC was rejected for another reason: $(field "$C" "$OPC" reason)"
  [ "$(chart_digest "$C" invoices)" = "$V1" ] || fail "the conflicting Stage's OCIRepository moved to $(chart_digest "$C" invoices)"
  pass "a Stage applying through an OCIRepository while HelmRelease $C/invoices manages the workload reads ApplierConflict, and its promote was Rejected"

  section "a HelmRelease whose chart is in another namespace is never written through"
  {
    chart_source invoices-candidates '{tag: "0.1.0"}'
    cat <<YAML
---
apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: invoices
  labels: {delivery.pacto.trianalab.io/apply-ref: invoices}
spec:
  interval: 1m
  suspend: true
  chartRef: {kind: OCIRepository, name: invoices-chart, namespace: $A}
  serviceAccountName: invoices-deployer
---
apiVersion: delivery.pacto.trianalab.io/v1
kind: Stage
metadata: {name: invoices}
spec:
  service: {name: invoices}
  environment: appliers-foreign
  applyRef: {kind: HelmRelease, name: invoices}
  candidates: {ociRepositoryRef: {name: invoices-candidates}}
  startLevel: advise
  gates: {impact: {declaredConsumersOnly: true}, verify: {window: 3m}}
YAML
  } | kubectl -n "$F" apply -f - >/dev/null
  eventually 60 ready_reason_is "$F" invoices ApplierUnreadable || fail "$F/invoices is Ready $(ready_reason "$F" invoices), want ApplierUnreadable"
  eventually 60 published_in "$F" invoices "$V1" || fail "chart 0.1.0 was never verified as published in $F"
  OPF=invoices-foreign
  as_person alice "$T" propose -n "$F" --stage invoices --to "$V1" --name "$OPF" --reason "the kind run proposes through a foreign chart" >/dev/null
  eventually 60 held_or_concluded "$F" "$OPF" || fail "$F/$OPF was never planned: $(field "$F" "$OPF" message)"
  [[ "$(outcome "$F" "$OPF")" != Succeeded/* ]] || fail "$F/$OPF succeeded through a chart in another namespace"
  always 10 chart_on "$A" invoices-chart "$V2" \
    || fail "a Stage in $F moved $A/invoices-chart to $(chart_digest "$A" invoices-chart)"
  pass "HelmRelease $F/invoices names a chart in $A: the Stage reads ApplierUnreadable and $A/invoices-chart stays on chart 0.2.0"
  echo "DELIVERY APPLIERS PASS"
}
