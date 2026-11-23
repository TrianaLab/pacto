# Provider reports

A provider adapter tells Pacto about the resources a consumer's contract asks
for: their phase with the `ResourceProvisioned` observation kind and their
usage and cost with `ResourceUsage`. Both arrive as signed
[EvidenceSets](evidence.md), and each needs a key granted that kind by name.
Every provider is optional: with no report a resource reads Unknown and its
usage reads not reported.

## Provisioning reports

A provider adapter reports the state of a resource with the
`ResourceProvisioned` observation kind. The report is about the consumer's
contract revision, and its observation subject is the consumer's
`configuration` or `dependency` that asked for the resource. Decoding rejects
any key beyond these four:

| Key | Meaning |
|-----|---------|
| `provider` | A lowercase slug of at most 63 bytes naming the provider. Pacto keeps no list of providers. |
| `externalRef` | Optional. An opaque handle of the resource at the provider, at most 253 printable bytes. Viewers of the consumer see it; a URL with credentials is rejected. |
| `phase` | `Requested`, `Provisioning`, `Ready`, `Failed` or `Deleting`. |
| `environment` | Optional. The consumer environment the resource serves, a lowercase slug. |

Any other outcome carries no payload; the observation's
`subject.environment` names its environment. `pacto evidence
provisioned` writes the EvidenceSet; sign and send it like any other:

```bash
pacto evidence provisioned \
  --contract oci://registry.example.com/acme/orders@sha256:<digest> \
  --configuration database --environment production \
  --provider my-platform --phase Ready \
  --external-ref databases/orders-production > report.json
```

The subject defaults to `<repository>/<environment or default>/<kind>/<name>`,
one per resource: the server keeps the latest report per producer and subject.

The server accepts a provisioning report only when:

- its key lists `ResourceProvisioned` in
  [`allowedObservationKinds`](evidence.md#trust-store-and-trust-config);
- the EvidenceSet carries only `ResourceProvisioned` observations;
- its subject starts with the consumer's repository path, so the key's
  `allowedSubjects` globs scope which consumers it reports for;
- each observation names a configuration reference or a dependency the
  consumer declares, in an environment it declares;
- when its key sets `allowedProviderRepos`, that configuration reference or
  dependency is an `oci://` ref to a provider repository under one of them;
- it is dated no more than five minutes after the server accepted it.

The server evaluates no compliance findings for a provisioning report and
lists it under `provisioning` on the targets endpoint, never as a target. The
[operational graph](platform-offerings.md#provisioning-status) joins it onto
the offering's uses. A v2 target-state file carries the same reports unsigned,
for tests and demos.

## Usage reports

A provider adapter reports how much of a resource a consumer uses, and what it
costs, with the `ResourceUsage` observation kind. Its subject is the
consumer's `configuration` or `dependency`, as in a provisioning report.
Decoding rejects any key beyond these five:

| Key | Meaning |
|-----|---------|
| `provider` | A lowercase slug of at most 63 bytes naming the provider. |
| `externalRef` | Optional. The resource's handle at the provider, with the provisioning report's rules. |
| `environment` | Optional. The consumer environment the resource serves, a lowercase slug. |
| `metrics` | Up to 16 of `{name, value, unit}`: a lowercase slug, a number from 0 to 10^18 and an optional unit of at most 16 printable bytes. |
| `cost` | Optional. `{amount, currency, period}`: a decimal string such as `212.40`, an ISO 4217 code and an ISO 8601 period of one unit (`D`, `W`, `M` or `Y`) from `P1D` to `P1Y`, so `P30D` or `P1M` but never `P1M15D`. |

A report carries at least one metric or a cost. `pacto evidence usage` writes
the EvidenceSet:

```bash
pacto evidence usage \
  --contract oci://registry.example.com/acme/orders@sha256:<digest> \
  --configuration database --environment production --provider my-platform \
  --metric storage-used=31:Gi --cost 212.40:USD:P30D > usage.json
```

The subject defaults to `<repository>/<environment or default>/usage/<kind>/<name>`.
The server keeps the latest report per producer and subject, so only usage
subjects hold the `usage` segment and a usage report never replaces a
provisioning report. Otherwise the provisioning rules above apply, with
`ResourceUsage` in place of `ResourceProvisioned`.

The targets endpoint lists usage under `usage` with schema
`pacto.dev/evidence-source/v4` (v5 when the response also names a
cluster), written only when it holds usage, so a v3 reader keeps working. The operational graph shows a figure as reported only
when one signed, fresh report about the newest consumer revision that uses
the resource, named as `revision`, is contradicted by no other producer. An
unsigned, stale or foreign report reads unknown, with its figures apart as
reported; disagreeing reports read unknown, name the producers under
`conflict` and give no figures. Pacto never sums or converts a cost, and
usage never changes a service's compliance. A v2 target-state file carries
usage reports unsigned, so they always read unknown.

## Extension capability reports

A producer that checks an `extension` capability the contract declares
reports with `CapabilityObserved` under a key granted that ref by
`allowedExtensionRefs`. Its verdict feeds compliance on a revision the service
runs, or on its current revision when none runs: reported absent makes the
service NonCompliant, and a stale report reads Unknown. A provisioning or
usage report never changes compliance. See
[Extension capabilities](evidence.md#extension-capabilities).
