# Write a source

This page shows how any tool, such as an internal developer platform, a
service catalog or a deployment system, feeds Pacto the services, calls and
running workloads it knows about. The tool needs no Pacto release and no Go
code: it uses two signed formats Pacto already reads.

- **Records** carry inferred services and their calls. The tool pushes one
  bundle per service, holding a `pacto.yaml` with only the service name and
  version and an [inferred record](contract-reference/inferred-record.md).
- **Evidence** carries targets: a workload that runs a contract revision. The
  tool signs an `EvidenceSet` and sends it to the
  [Evidence Server](evidence.md), which serves it to the fleet as it serves
  every other report.

One key signs both. The tool is the *producer*, and its id names it in the
fleet, such as `my-idp`.

## Grant the producer

The platform adds the producer's public key to the Evidence Server's
[trust config](evidence.md#trust-store-and-trust-config). The
`allowedContractRepos` entry under `pacto-inferred` grants it the right to
contribute records; the other entries list the contract repositories it
reports targets about.

```yaml
apiVersion: pacto.dev/evidence-trust/v1
keys:
  - keyId: my-idp-2026
    producerId: my-idp
    publicKeyFile: my-idp__my-idp-2026.pub
    allowedContractRepos:
      - ghcr.io/acme/ledger                  # targets it reports
      - ghcr.io/acme/pacto-inferred/my-idp   # records it contributes
```

The grant names the producer's own path only: loading refuses an entry under
`pacto-inferred` that names another producer or a deeper path. A key with no
`allowedContractRepos` contributes no records.

## The producer

The script below is a whole producer. It reads `REGISTRY`, `EVIDENCE_URL` and
`CONTRACT_REF` from the environment and runs as is with `bash -euo pipefail`.

```bash
# The platform sets REGISTRY (such as ghcr.io/acme), EVIDENCE_URL (https from
# CI) and CONTRACT_REF, the contract revision the reported workload runs.
KEYS="$HOME/.pacto/keys" # never inside a bundle directory
KEY="$KEYS/my-idp-2026.key"
[ -f "$KEY" ] || pacto evidence keygen --out "$KEYS" --producer my-idp --key-id my-idp-2026

# Records: one bundle per service, read from your tool's export.
bundle=$(mktemp -d)
echo '{"checkout": ["ledger"]}' > "$bundle/export.json"
digest=$(shasum -a 256 "$bundle/export.json" | cut -d' ' -f1)
cat > "$bundle/pacto.yaml" <<EOF
pactoVersion: "2.5"
service: {name: checkout, version: 0.1.0}
EOF
cat > "$bundle/pacto.inferred.yaml" <<EOF
format: pacto.inferred/v1
inputs:
  sources: [{path: export.json, digest: "sha256:$digest"}]
fields:
  - {field: "dependencies[ledger]", value: ledger, source: "export.json:1", confidence: likely}
EOF
pacto push "oci://$REGISTRY/pacto-inferred/my-idp/services" -p "$bundle" --sign-key "$KEY"

# Targets: one signed EvidenceSet per running workload.
now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
subject='{"kind": "service", "name": "ledger-prod"}'
cat > evidence.json <<EOF
{"Subject": $subject, "ContractRef": "$CONTRACT_REF", "Source": "my-idp", "ObservedAt": "$now",
 "Observations": [{"kind": "WorkloadObserved", "subject": $subject, "outcome": "Observed",
  "value": {"type": "service"}, "provenance": {"collector": "my-idp", "detectedAt": "$now"}}]}
EOF
pacto evidence sign evidence.json --key "$KEY" --key-id my-idp-2026 --producer my-idp \
  --sequence "$(date +%s)" > envelope.json
pacto evidence send envelope.json --url "$EVIDENCE_URL/api/evidence/v1/envelopes"
```

The first run creates the key; hand `my-idp__my-idp-2026.pub` to the platform.

- Records live in one repository per producer,
  `<registry>/pacto-inferred/<producer>/services`, with one tag per service
  version, `<service>_<version>`. A service name holds no `_`, so the tag
  splits one way. The tag must match the bundle's `service.name` and
  `service.version`: Pacto refuses a bundle tagged otherwise.
- `pacto push --sign-key` signs the pushed digest and never writes an
  organization index entry for a contribution. Re-running it on an unchanged
  bundle signs it when no signature verifies, and renews its creation time
  once it is half the 24h freshness window old, so a producer that keeps
  pushing never reads stale. Changed content under the same version is
  refused: push a new version, or use `--force`.
- The push refuses a version that is not `MAJOR.MINOR.PATCH` semver, and a
  `pacto.yaml` that declares more than its name and version.
- The `pacto.yaml` of a record holds only `pactoVersion`, `service.name` and
  `service.version`. Everything else is a field of `pacto.inferred.yaml` with
  its `source` and `confidence`.
- A field's `source` cites a file the bundle ships and `inputs.sources`
  lists with its digest, such as `export.json` above. When a field cites a
  file the list leaves out, or a listed digest differs, Pacto reads nothing
  of the record and the service shows with no calls.

## Map your tool

Any tool maps to the same shape: a service dependencies export, a catalog's
`dependsOn` relations or a platform webhook. Read your tool's export, write
one `pacto.inferred.yaml` per service with a `dependencies[<name>]` field per
call, push and sign it. For each workload your tool runs, send one
`WorkloadObserved` about the contract revision it runs.

## What Pacto does with it

The dashboard, `pacto fleet` and `pacto mcp` read the producer through the
Evidence Server. They show one `producer/my-idp` row in
[Detected providers](operational-graph-providers.md#producers) with its own
status and `asOf`.

- A contributed service is inferred: its page says `contributed by my-idp`,
  and the entities list mutes it with every other
  [inferred service](operational-graph-inferred.md#contributed-services). Its
  calls are inferred edges with `compatibility: unknown`, so they never
  satisfy a range and never clear a gate.
- A contribution is never a compliance subject: the Evidence Server refuses
  evidence about one, so a contributed service never reads Compliant.
- A target the producer reported reads `Reported by: my-idp` with its evidence
  age. When the producer stops reporting, its targets and its row go stale.
- A record older than the freshness window reads stale. A registry may keep a
  tag for ever, so to retire a service push a newer version whose record holds
  no dependencies.
- When a contract declares the same service name in the producer's registry,
  the contract wins and the record is dropped.

Readers pull records only from a repository under a registry they already
read, such as the registry of their `--oci` refs, and only when a signature
verifies with a key the Evidence Server advertises for that producer.

To read ranges and conformance back into your tool as well, see
[Sit on top of any IDP](any-idp.md).
