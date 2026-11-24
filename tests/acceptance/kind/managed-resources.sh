#!/usr/bin/env bash
# Managed resources on a real API server (#592): the chart grants the list of
# the catalog's managed kinds only once helm runs after their CRDs appear, the
# operator reads two Crossplane claims and their RDS Instances, the published
# orders consumer joins its database through the connection Secret name in its
# pod spec, a signed provisioning record puts one resource through the
# postgresql offering and the hand-applied claim reads outside the contract.
#
# No Crossplane runs: the CRDs, the XRD, the composites, the claims and the
# Instances (with the status a provider writes) are fixtures under
# fixtures/managed. Until the Crossplane adapter (#576) reports provisioning,
# this script signs the ResourceProvisioned record itself, with a key the trust
# config grants ResourceProvisioned for the postgresql provider repository only.
#
#   1. install: the operator with the Evidence Server, then the CRDs: the
#      managed-resources row reads partial with the helm upgrade hint and the
#      ClusterRole grants no managed kind.
#   2. upgrade: helm upgrade --reuse-values grants list, and only list, on
#      rds.aws.upbound.io instances and on the XRD's claim and composite
#      plurals; the detected ConfigMap holds both resources with their claims.
#   3. offering: the signed record for orders' postgresql dependency names the
#      orders-db claim; pacto fleet reads orders-db through the postgresql
#      offering, joined to orders by its connection Secret, legacy-db outside
#      the contract, and one of two resources through offerings.
set -euo pipefail
CLUSTER="${KIND_CLUSTER:-pacto-managed}"
NS=pacto-system
REL=pacto-operator
DETECTED="${REL}-detected"
CLUSTERROLE="${REL}-manager"
REG_HOST="pacto-registry.${NS}.svc.cluster.local:5000"
LOCAL_REG_PORT=18541
LOCAL_EV_PORT=18542
ACCOUNT=123456789012
REGION=eu-west-1
ORDERS_DB="external:aws/rds/${ACCOUNT}/${REGION}/orders-db"
LEGACY_DB="external:aws/rds/${ACCOUNT}/${REGION}/legacy-db"
HERE="$(cd "$(dirname "$0")" && pwd)"
FIX="$HERE/fixtures/managed"
# shellcheck source=tests/acceptance/kind/lib.sh
source "$HERE/lib.sh"
VER="$(release_version kubernetes)"
OP_REPO="localhost:5001/pacto-operator/pacto-controller"
OP_IMG="${OP_REPO}:${VER}"
DASH_IMG="localhost:5001/pacto-dashboard:$(release_version core)"
PAUSE_IMG="registry.k8s.io/pause:3.9@sha256:7031c1b283388d2c2e09b57badb803c05ebed362dc88d84b480cc47f72a21097"

for t in jq helm go; do command -v "$t" >/dev/null 2>&1 || fail "$t is required"; done

teardown() {
  helm uninstall "$REL" -n "$NS" --wait >/dev/null 2>&1 || true
  kubectl delete ns shop "$NS" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete -f "$FIX/crds.yaml" --ignore-not-found --wait=false >/dev/null 2>&1 || true
}
# shellcheck disable=SC2154  # rc is assigned by rc=$? inside the trap body
trap 'rc=$?; [ $rc -ne 0 ] && dump_diag "$NS"; pkill -f "kubectl.*port-forward" 2>/dev/null || true; keep_or_teardown "$NS" "$CLUSTER" teardown; exit $rc' EXIT

section "create the cluster and load the images"
cluster_start_bg
build_operator_images "$OP_IMG" "$DASH_IMG" "$VER"
pull_pinned "$PAUSE_IMG"
PACTO_BIN="$(build_pacto)"
CHART="$(package_chart "$PACTO_CHART")"
ensure_cluster
# The Evidence Server runs the dashboard image.
load_images "$OP_IMG" "$DASH_IMG" "${PAUSE_IMG%@*}"
kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
install_registry

section "the platform adapter's key: granted ResourceProvisioned for the postgresql provider only"
KEYDIR="$(mktemp -d)"
"$PACTO_BIN" evidence keygen --out "$KEYDIR" --key-id platform-adapter >/dev/null
cat > "$KEYDIR/trust.yaml" <<YAML
apiVersion: pacto.dev/evidence-trust/v1
keys:
  - keyId: platform-adapter
    producerId: platform-adapter
    publicKeyFile: platform-adapter.pub
    allowedObservationKinds: [ResourceProvisioned]
    allowedProviderRepos: [${REG_HOST}/demo/postgresql]
YAML
kubectl -n "$NS" create secret generic pacto-evidence-trust \
  --from-file=trust.yaml="$KEYDIR/trust.yaml" --from-file=platform-adapter.pub="$KEYDIR/platform-adapter.pub" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null

section "publish the postgresql provider and the orders consumer that depends on it"
WORK="$(mktemp -d)"
cp -R "$FIX/provider" "$FIX/consumer" "$WORK/"
sed -i.bak "s#__REG_HOST__#${REG_HOST}#" "$WORK/consumer/pacto.yaml" && rm -f "$WORK/consumer/pacto.yaml.bak"
REG_PF="$(pf "$LOCAL_REG_PORT" svc/pacto-registry 5000)"
# Pushed AS the in-cluster host, so the operator, the Evidence Server and the
# CLI below name one repository for each.
PROVIDER_DIGEST="$(push_bundle "$PACTO_BIN" "$LOCAL_REG_PORT" "$WORK/provider" postgresql:1.0.0 "$REG_HOST")"
CONSUMER_DIGEST="$(push_bundle "$PACTO_BIN" "$LOCAL_REG_PORT" "$WORK/consumer" orders:1.0.0 "$REG_HOST")"
[ -n "$PROVIDER_DIGEST" ] && [ -n "$CONSUMER_DIGEST" ] || fail "could not publish the provider and the consumer"
PROVIDER_REF="oci://${REG_HOST}/demo/postgresql@${PROVIDER_DIGEST}"
CONSUMER_REF="oci://${REG_HOST}/demo/orders@${CONSUMER_DIGEST}"
pass "published $PROVIDER_REF and $CONSUMER_REF"

section "SCENARIO 1 (install): the CRDs arrive after the chart, so their kinds are not granted"
SETS=(--set global.imageRegistry= --set image.repository="$OP_REPO" --set image.tag="$VER" --set image.pullPolicy=Never
      --set dashboard.enabled=false --set "insecureRegistries[0]=${REG_HOST}"
      --set evidence.enabled=true --set evidence.trust.existingSecret=pacto-evidence-trust
      --set evidence.trust.configKey=trust.yaml --set "evidence.registry.subjects[0]=${CONSUMER_REF}")
helm install "$REL" "$CHART" -n "$NS" "${SETS[@]}" --wait --timeout 240s >/dev/null
wait_managed_ready pacto-evidence >/dev/null || fail "the Evidence Server did not become Ready"
pass "the operator and its Evidence Server are Ready"

kubectl create namespace shop --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl apply -f "$FIX/consumer.yaml" >/dev/null
sed "s#__CONSUMER_REF__#${CONSUMER_REF}#" "$FIX/consumer-pacto.yaml" | kubectl apply -f - >/dev/null
has_contract() { [ -n "$(kubectl -n shop get pacto orders -o jsonpath='{.status.contract.resolvedRef}' 2>/dev/null)" ]; }
eventually 60 has_contract || fail "the orders Pacto never resolved $CONSUMER_REF"
pass "the orders Pacto resolved its published revision"

kubectl apply -f "$FIX/crds.yaml" >/dev/null
kubectl wait --for condition=established --timeout=60s -f "$FIX/crds.yaml" >/dev/null
kubectl apply -f "$FIX/xrd.yaml" -f "$FIX/composites.yaml" -f "$FIX/claim-offering.yaml" -f "$FIX/claim-outside.yaml" >/dev/null
uid() { kubectl get xpostgresqlinstances.platform.example.org "$1" -o jsonpath='{.metadata.uid}'; }
sed -e "s#__ORDERS_XR_UID__#$(uid orders-db-x1)#" -e "s#__LEGACY_XR_UID__#$(uid legacy-db-x1)#" "$FIX/instances.yaml" | kubectl apply -f - >/dev/null
at_provider() { # INSTANCE DB VERSION — what the provider writes once the database exists
  kubectl patch instances.rds.aws.upbound.io "$1" --subresource=status --type=merge \
    -p "{\"status\":{\"atProvider\":{\"arn\":\"arn:aws:rds:${REGION}:${ACCOUNT}:db:$2\",\"engine\":\"postgres\",\"engineVersion\":\"$3\"}}}" >/dev/null
}
at_provider orders-db-x1-rds orders-db 15.4
at_provider legacy-db-x1-rds legacy-db 14.9
pass "the XRD, two claims, their composites and their RDS Instances exist"

detected_json() { kubectl -n "$NS" get configmap "$DETECTED" -o jsonpath='{.data.detected\.json}' 2>/dev/null; }
row() { detected_json | jq -c '.entries[]? | select(.kind == "managed-resources")'; }
row_is() { row | jq -e "$@" >/dev/null 2>&1; }
# Detection runs at start and every five minutes: a restart reads now.
redetect() {
  kubectl -n "$NS" rollout restart deploy/pacto-operator >/dev/null
  wait_ready pacto-operator >/dev/null || fail "the restarted operator is not Ready"
}
redetect
# shellcheck disable=SC2016  # $h is a jq variable
eventually 120 row_is --arg h "helm upgrade <release> <chart> --reuse-values" \
  '.completeness == "partial" and .hint == $h and (.details | index("unread rds.aws.upbound.io/instances"))' \
  || fail "the managed-resources row does not read partial with the helm upgrade hint: $(row)"
pass "managed-resources: $(row | jq -r '"\(.completeness): \(.reason); hint: \(.hint)"')"
rules() { kubectl get clusterrole "$CLUSTERROLE" -o json | jq -c "[.rules[] | select(.apiGroups == [\"$1\"])]"; }
[ "$(rules rds.aws.upbound.io)" = "[]" ] && [ "$(rules platform.example.org)" = "[]" ] \
  || fail "the ClusterRole grants a managed kind before helm ran: $(rules rds.aws.upbound.io) $(rules platform.example.org)"
pass "the ClusterRole grants no managed kind until helm runs again"

section "SCENARIO 2 (upgrade): helm grants list on the catalog kinds and the XRD's plurals, nothing more"
helm upgrade "$REL" "$CHART" -n "$NS" --reuse-values --wait --timeout 240s >/dev/null
rds="$(rules rds.aws.upbound.io)"
jq -e 'length == 1 and .[0].verbs == ["list"] and (.[0].resources | index("instances"))' <<<"$rds" >/dev/null \
  || fail "rds.aws.upbound.io is not granted list on instances alone: $rds"
claims="$(rules platform.example.org)"
jq -e 'length == 1 and .[0].verbs == ["list"] and (.[0].resources | sort) == ["postgresqlinstances", "xpostgresqlinstances"]' <<<"$claims" >/dev/null \
  || fail "platform.example.org is not granted list on its claim and composite plurals alone: $claims"
kubectl get clusterrole "$CLUSTERROLE" -o json | jq -e '[.rules[] | (.apiGroups // []) + (.resources // []) + .verbs | .[] | select(. == "*")] == []' >/dev/null \
  || fail "the ClusterRole holds a wildcard"
pass "list on rds.aws.upbound.io $(jq -c '.[0].resources' <<<"$rds") and platform.example.org postgresqlinstances, xpostgresqlinstances; no wildcard"
redetect
both_read() {
  detected_json | jq -e '[.managedResources[]? | select(.claim != null and .claim != "")] | length == 2' >/dev/null 2>&1 &&
    row_is '.completeness == "complete"'
}
eventually 120 both_read || fail "the detected ConfigMap does not hold both resources with their claims: $(detected_json | jq -c '{managedResources, row: [.entries[]? | select(.kind == "managed-resources")]}')"
pass "detected: $(detected_json | jq -r '[.managedResources[] | "\(.identity) (\(.claim))"] | join(", ")')"

section "SCENARIO 3 (offering): a signed record puts orders-db through the postgresql offering"
EV_PF="$(pf "$LOCAL_EV_PORT" svc/pacto-evidence 8686)"
"$PACTO_BIN" evidence provisioned --contract "$CONSUMER_REF" --dependency postgresql --provider postgresql --phase Ready \
  --external-ref "PostgreSQLInstance shop/orders-db" --environment prod --collector platform-adapter > "$WORK/provisioned.json"
"$PACTO_BIN" evidence sign "$WORK/provisioned.json" --key "$KEYDIR/platform-adapter.key" --key-id platform-adapter \
  --producer platform-adapter --sequence 1 --id managed-orders-db-1 > "$WORK/provisioned.envelope.json"
out="$("$PACTO_BIN" evidence send "$WORK/provisioned.envelope.json" --url "http://127.0.0.1:${LOCAL_EV_PORT}" 2>&1)" \
  || fail "the Evidence Server refused the provisioning record: $out"
pass "the Evidence Server accepted the signed ResourceProvisioned record for orders' postgresql dependency"

# fleet ARGS... — pacto fleet over the cluster, its detected ConfigMap, the
# Evidence Server and both published revisions, as JSON. The forwarded
# registry port proxies the in-cluster host.
fleet() {
  HTTP_PROXY="http://127.0.0.1:${LOCAL_REG_PORT}" NO_PROXY="" no_proxy="" PACTO_INSECURE_REGISTRIES="$REG_HOST" \
    "$PACTO_BIN" fleet "$@" --local "" --k8s --detected "k8s:${NS}/${DETECTED}" \
    --evidence-url "http://127.0.0.1:${LOCAL_EV_PORT}" --oci "$PROVIDER_REF" --oci "$CONSUMER_REF" --output-format json 2>"$WORK/fleet.err"
}
ORDERS_JSON=""
orders_through_offering() {
  ORDERS_JSON="$(fleet get "$ORDERS_DB")" || return 1
  jq -e '.external.resources | length == 1 and .[0].contract == "offering" and .[0].claim == "PostgreSQLInstance shop/orders-db" and .[0].engine == "postgres 15.4"' \
    <<<"$ORDERS_JSON" >/dev/null
}
eventually 40 orders_through_offering \
  || fail "orders-db does not read through the postgresql offering: ${ORDERS_JSON:-$(cat "$WORK/fleet.err")}"
pass "orders-db: $(jq -r '.external.resources[0] | "\(.type) \(.name), claim \(.claim), \(.engine), \(.contract) offering \(.offering.label)"' <<<"$ORDERS_JSON")"
jq -e '[.external.sources[] | select(.kind == "pod-spec" and .match == "resource" and .provenance == "inferred" and (.service | test("(^|/)orders$")))] | length >= 1' \
  <<<"$ORDERS_JSON" >/dev/null || fail "orders is not joined to orders-db through its connection Secret: $(jq -c .external.sources <<<"$ORDERS_JSON")"
pass "orders joins orders-db through the connection Secret orders-db-conn (inferred, pod spec)"

LEGACY_JSON="$(fleet get "$LEGACY_DB")" || fail "pacto fleet get $LEGACY_DB failed: $(cat "$WORK/fleet.err")"
jq -e '.external.resources | length == 1 and .[0].contract == "outside" and .[0].claim == "PostgreSQLInstance shop/legacy-db" and (.[0] | has("offering") | not)' \
  <<<"$LEGACY_JSON" >/dev/null || fail "legacy-db does not read outside the contract: $(jq -c .external.resources <<<"$LEGACY_JSON")"
pass "legacy-db, the hand-applied claim, reads outside the contract"

OVERVIEW="$(fleet overview)" || fail "pacto fleet overview failed: $(cat "$WORK/fleet.err")"
jq -e '.match.resources.throughOfferings == 1 and .match.resources.discovered == 2' <<<"$OVERVIEW" >/dev/null \
  || fail "the match tally does not count 1 of 2 resources through offerings: $(jq -c .match.resources <<<"$OVERVIEW")"
pass "resources through offerings: 1 of 2"
kill "$EV_PF" "$REG_PF" 2>/dev/null || true

echo
echo "MANAGED RESOURCES E2E PASS"
