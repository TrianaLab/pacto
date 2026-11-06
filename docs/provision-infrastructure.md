# Provision infrastructure through contracts

A platform team publishes a contract for each kind of infrastructure it offers,
such as a PostgreSQL database. A developer who needs one references that
contract from the service's own contract and writes the values the provider
lets them control. The consumer contract is the request. Pacto validates it
against the provider's published schema, and your platform's tools provision
the resource. Pacto then shows what those tools report back.

This guide follows the PostgreSQL offering of the [demo](examples/demo-tour.md)
from both sides.

## Publish an infrastructure contract

An infrastructure contract describes what consumers may ask for:

```yaml
pactoVersion: "2.6"
service:
  name: postgresql
  version: 16.1.0
  offering: postgresql
  summary: Primary relational database for transactional data
  owner:
    team: infra-data
  lifecycle:
    deprecated: "2026-11-01"
    sunset: "2027-06-30"
    successor: oci://ghcr.io/trianalab/pacto/postgresql:17.0.0
workload: service
configurations:
  - name: provisioning
    schema: configuration/provisioning.schema.json
    required: true
policies:
  - name: database-standards
    schema: policy/schema.json
```

- `configurations[]` holds the provisioning schema: the fields a consumer
  controls, their types, ranges and defaults. In the demo a consumer chooses
  `size` and `storageGi` and may turn on `highAvailability` and `backups`.
- `policies[]` holds the platform rules every consumer contract must meet.
- `service.lifecycle` announces a deprecation, a sunset date and a successor.
  Consumers are warned with `DEPENDENCY_SERVICE_DEPRECATED` and
  `DEPENDENCY_SERVICE_SUNSET`.
- `service.offering` and `service.summary` list the contract on the dashboard's
  Offerings page, in `pacto fleet search --offering postgresql` and in
  `pacto_fleet_search`.

Publish each version with `pacto push` or `pacto publish`. Each published
version is the platform interface consumers pin.

## Consume it

Developers find the offering on the Offerings page or with
`pacto fleet versions postgresql --range '^16.0.0'` and `pacto_fleet_versions`.
Each version shows its lifecycle and whether it satisfies the range. The list
comes from the contract revisions the fleet holds, and the registry may publish
others. The Offerings page also gives a snippet to copy, built from the version
it shows as current.

A consumer uses both forms:

```yaml
pactoVersion: "2.6"
dependencies:
  - name: postgresql
    ref: oci://ghcr.io/trianalab/pacto/postgresql
    required: true
    compatibility: ^16.0.0
configurations:
  - name: database
    ref: oci://ghcr.io/trianalab/pacto/postgresql:16.1.0
    configuration: provisioning
    required: true
    values:
      size: medium
      storageGi: 50
environments:
  - name: production
    file: environments/production.yaml
```

The dependency carries the version range and puts the consumer in impact
answers. The configuration reference carries the request. `configuration`
selects one of the provider's configurations and may be omitted when the
provider declares exactly one. Inline `values` are the defaults, and each
environment file merges its own values on top:

```yaml
# environments/production.yaml
database:
  highAvailability: true
  backups:
    retentionDays: 14
```

`pacto validate` fetches the referenced contract and checks the inline values
and every environment's merged values against the selected schema. When the
reference cannot be fetched, validation fails with `CONFIG_REF_UNRESOLVED`, so
values are never accepted unchecked. `pacto lock` pins the reference to a
digest in `pacto.lock`, which the fleet needs to resolve the reference.

## Request provisioning

Publish the consumer contract. That published revision is the request: it
names the provider version, the selected configuration and the values for each
environment. An adapter in your platform reads it and provisions the resource.
Pacto itself provisions nothing and keeps no provider-specific code.

## Write an adapter

An adapter is any program that turns a published consumer contract into a
resource: a CI job, a controller or a script. It:

1. Maps the offering slug, `postgresql`, to your implementation. The mapping
   lives in the adapter; the contract carries no provider hints.
2. Reads the consumer's values for each environment from
   `pacto explain <consumer> --output-format json`. Each entry of
   `configurations` carries `environmentValues`: the inline `values` with that
   environment's file merged on top. Objects merge key by key, and a `null`
   removes its key.
3. Provisions the resource with your platform's tools.
4. Reports the result:

```bash
pacto evidence provisioned \
  --contract oci://ghcr.io/acme/orders@sha256:<digest> \
  --configuration database --environment production \
  --provider my-platform --phase Ready \
  --external-ref databases/orders-production > report.json
pacto evidence sign report.json --key adapter.key --key-id adapter-2026 \
  --producer platform-adapter --sequence 42 > envelope.json
pacto evidence send envelope.json \
  --url https://evidence.example.com/api/evidence/v1/envelopes
```

`--phase` is one of `Requested`, `Provisioning`, `Ready`, `Failed` or
`Deleting`. Use `--dependency` instead of `--configuration` when the resource
answers a dependency. The subject defaults to the full repository path, the
environment (or `default`) and the name, such as
`ghcr.io/acme/orders/production/database`. Keep one subject per resource so
each resource keeps its own latest report.

Give the adapter its own key. A key may report `ResourceProvisioned` only when
its trust entry lists it, so a team's runtime key cannot mark its own
database Ready:

```yaml
apiVersion: pacto.dev/evidence-trust/v1
keys:
  - keyId: adapter-2026
    producerId: platform-adapter
    publicKeyFile: platform-adapter__adapter-2026.pub
    allowedObservationKinds: [ResourceProvisioned]
    allowedContractRepos: [ghcr.io/acme]
```

The Evidence Server stores each report on the consumer revision it is about,
so that revision must be one of its `--subject` values. Its targets endpoint
lists consumer contracts, producers and external references: keep it behind
the same network and auth boundary as the dashboard. See
[Evidence](evidence.md) for the trust store and the Evidence Server.

On Kubernetes, put that trust config in the operator's trust Secret next to
the public keys and set `evidence.trust.configKey` to its key; see
[Provisioning reports](integrations/kubernetes/evidence-server.md#provisioning-reports).
Without it, the operator's Evidence Server trusts each public key for runtime
evidence only and rejects `ResourceProvisioned` reports.

[Plugins](plugins.md) are another seam: `pacto generate` hands a contract to
a program that writes files, such as the manifests your provisioning tool
applies.

## Follow provisioning status

The dashboard's Offerings page, `pacto fleet entity offering postgresql` and
`pacto_fleet_entity` show each consumer's provisioning status per environment,
with its producer, source and report time. Unknown means Pacto cannot say;
it is neither failure nor success. The status is Unknown when:

- no provider has reported for that use;
- the newest report is older than the freshness window, 24 hours by default;
- the provider reported an outcome other than Observed;
- the newest report is for an older revision of the consumer;
- two producers disagree, which also adds a `PROVISIONING_CONFLICT`
  limitation.

Reports from a target-state file show as unsigned. A viewer restricted to some
namespaces sees a provider with no Kubernetes target, its owner and versions,
and none of its targets. Consumers the viewer may not read appear only as a
count, with their provisioning status hidden, and a conflict reflects only the
reports the viewer can see.

## Use the resource

Each status also shows the provider's external reference for that environment,
such as `databases/orders-production`, and links the resource to the consuming
service. The adapter delivers the connection details as a ConfigMap or Secret.
On Kubernetes, the consumer's Pacto CR binds that object to the configuration
with `spec.target.configBindings`:

```yaml
spec:
  target:
    configBindings:
      - configuration: database
        kind: Secret
        name: orders-database
```

The operator then checks that the object exists. It never reads Secret values.
See [Configuration and policy](contract-reference/configuration-and-policy.md#what-the-kubernetes-collector-validates).

## Update or retire

To change a request, edit its values and publish the consumer contract again;
`pacto_edit` sets environment values with `set_environment_values`. To retire
it, remove the configuration reference with `remove_configuration_refs` and
publish.

When the platform team ships a new provider version, `pacto impact` and
`pacto_impact` list every consumer by dependency and by configuration
reference. Impact checks a reference consumer's values against the new schema,
so a breaking change never clears on its behalf. Pacto shows the declared
lifecycle and the provider's latest report.
