#!/usr/bin/env bash
# acme-portal: a private in-house IDP that sits on top of Pacto through the
# open seams only, with curl, the pacto CLI and coreutils. A template: the
# service, versions and database it reports are fixture facts; replace them
# with what your portal knows before you run it for real. The platform sets:
#   REGISTRY          where contracts live, such as registry.acme.internal/acme
#   EVIDENCE_URL      the Evidence Server's base URL
#   PACTO_URL         the dashboard's base URL
#   PACTO_TOKEN_FILE  a 0600 file holding "Authorization: Bearer <reader token>"
#   CHECKOUT_REF      the running checkout revision (oci://...@sha256:...)
#   KEYS              where acme-portal keeps its signing key, never a bundle
set -euo pipefail

KEY="$KEYS/acme-portal-2026.key"
if [ ! -f "$KEY" ]; then
  pacto evidence keygen --out "$KEYS" --producer acme-portal --key-id acme-portal-2026
  echo "acme-portal: hand $KEYS/acme-portal__acme-portal-2026.pub to the platform for its trust grant, then run again"
  exit 0
fi
# Ingest refuses a sequence no greater than the producer's last, across runs.
seq=$(( $(date +%s) * 1000 ))

# send signs one EvidenceSet and posts it to the Evidence Server.
send() {
  seq=$((seq + 1))
  pacto evidence sign "$1" --key "$KEY" --key-id acme-portal-2026 --producer acme-portal \
    --sequence "$seq" > envelope.json
  pacto evidence send envelope.json --url "$EVIDENCE_URL/api/evidence/v1/envelopes"
}

# contribute pushes one record the portal knows, signed, as acme-portal's
# inferred contribution. The bundle holds the record, cited by digest, and
# the two files that describe it: never a key or anything else of the run.
contribute() { # name version depends-on
  local dir digest
  dir=$(mktemp -d)
  echo "{\"$1\": {\"dependsOn\": [\"$3\"]}}" > "$dir/portal.json"
  digest=$( (sha256sum 2>/dev/null || shasum -a 256) < "$dir/portal.json" | cut -d' ' -f1)
  cat > "$dir/pacto.yaml" <<YAML
pactoVersion: "2.5"
service: {name: $1, version: $2}
YAML
  cat > "$dir/pacto.inferred.yaml" <<YAML
format: pacto.inferred/v1
inputs:
  sources: [{path: portal.json, digest: "sha256:$digest"}]
fields:
  - {field: "dependencies[$3]", value: $3, source: "portal.json:1", confidence: likely}
YAML
  pacto push "oci://$REGISTRY/pacto-inferred/acme-portal/services" -p "$dir" --sign-key "$KEY"
  rm -rf "$dir"
}

# 1. Contribution: a service the portal runs that has no contract yet.
contribute legacy-billing 0.1.0 payments

# 2. Target: a running checkout named checkout-prod, whoever deployed it, and
# the payments and postgresql versions it reaches, which conformance checks.
now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
subject='{"kind": "service", "name": "checkout-prod"}'
prov="{\"collector\": \"acme-portal\", \"detectedAt\": \"$now\"}"
dep() { # name version
  echo ", {\"kind\": \"DependencyReachable\", \"subject\": {\"kind\": \"dependency\", \"name\": \"$1\"},
  \"outcome\": \"Observed\", \"value\": {\"reachable\": true, \"version\": \"$2\"}, \"provenance\": $prov}"
}
cat > workload.json <<JSON
{"Subject": $subject, "ContractRef": "$CHECKOUT_REF", "Source": "acme-portal", "ObservedAt": "$now",
 "Observations": [{"kind": "WorkloadObserved", "subject": $subject, "outcome": "Observed",
  "value": {"type": "service"}, "provenance": $prov}$(dep payments 2.1.0)$(dep postgresql 16.1.0)]}
JSON
send workload.json

# 3. Export: read ranges and conformance from the served route.
for try in 1 2 3 4 5 6 7 8 9 10; do
  code=$(curl -sS -o entities.yaml -w '%{http_code}' -H @"$PACTO_TOKEN_FILE" "$PACTO_URL/api/backstage/entities")
  [ "$code" = 503 ] || break
  sleep 1
done
[ "$code" = 200 ] || { echo "acme-portal: the route answered $code (try $try)" >&2; exit 1; }

# component prints one Component of entities.yaml.
component() {
  awk -v n="  name: $1" '/^---$/ {if (f) printf "%s", b; b = ""; f = 0; next}
    {b = b $0 "\n"} $0 == n {f = 1} END {if (f) printf "%s", b}' entities.yaml
}
expect() { # name text
  component "$1" | grep -qF -- "$2" || { echo "acme-portal: $1 lacks $2" >&2; component "$1" >&2; exit 1; }
}
expect checkout '"component:default/payments":[{"range":"^2","rangeStatus":"satisfied"}]'
expect checkout 'pacto.trianalab.io/conformance:'
expect legacy-billing 'pacto.trianalab.io/contributed-by: acme-portal'
expect legacy-billing 'pacto.trianalab.io/provenance: inferred'

# 4. Provisioning: the portal made checkout's database and says so.
pacto evidence provisioned --contract "$CHECKOUT_REF" --dependency postgresql \
  --provider acme-portal --phase Ready --external-ref acme://postgresql/checkout > prov.json
send prov.json

echo "acme-portal: done"
