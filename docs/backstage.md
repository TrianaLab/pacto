# Export services to Backstage

`pacto export backstage` writes the services Pacto knows as one multi-document
YAML file of Backstage catalog entities. You commit that one file and point
Backstage at it, so no repository needs its own `catalog-info.yaml`. Every
entity says how Pacto knows it, when and how completely.

## Run the export

The command reads the same sources as `pacto fleet`: `--oci`, `--root`, `--k8s`,
`--target-state` and the rest. It scans a local directory only when you pass
`--local`, so drafts and fixtures in the working directory never reach the
catalog:

```bash
pacto export backstage --oci ghcr.io/acme/payments --oci ghcr.io/acme/ledger \
  -o catalog/pacto-entities.yaml
```

Run it as a scheduled sync job. When a source cannot be read, or the fleet is
empty, the command writes nothing, leaves an existing `-o` file as it was and
exits with code 2. A source outage would otherwise drop the services it holds
and Backstage would orphan their entities. Pass `--allow-partial` to write the
file anyway; each entity then says `partial`.

## Who sees the file

Everyone who reads the file sees what the exporter's credentials could read.
Run the job with a reader-scoped kubeconfig or `--namespace`, and list the
`--oci` sources explicitly. With `--k8s` the command prints to standard error
which namespaces it read.

## Point Backstage at the file

Commit the file to a repository Backstage can read and add a location to
`app-config.yaml`:

```yaml
catalog:
  locations:
    - type: url
      target: https://github.com/acme/catalog/blob/main/catalog/pacto-entities.yaml
```

A Backstage instance that runs next to a checkout can use `type: file` with
the path instead.

## What each entity is

| Pacto | Backstage entity |
|---|---|
| A service | `Component` of type `service` |
| An interface of the service's current revision | `API` named `<component>-<interface>`, listed in `providesApis` |
| An external system | `Resource` with its class as `type` |
| A business domain | `System`, set as the Component's `spec.system` |
| An observed caller with no contract | stub `Component` tagged `pacto-observed` |

Golden paths run nowhere and consume nothing, so they are left out.

## Names and owners

An entity's name is the service name with every character Backstage rejects
replaced by `-`, cut to fit 63 characters. When two services share a name, every
one of them gets `-` and 8 hex digits of a hash of its full service key, so a
name never moves to another service when a second registry joins. The full key
is in `pacto.trianalab.io/service-key`. A stub Component always carries its
suffix, so it never takes a service's name. Every entity lives in namespace
`default`.

A team owner becomes `group:default/<team>` and a DRI `user:default/<dri>`.
Pacto never rewrites an owner: when the name is not a valid Backstage name,
`spec.owner` is `unknown`, `pacto.trianalab.io/owner` holds the raw value and
`pacto.trianalab.io/owner-unmapped` is `"true"`. Two owners claiming one service
also give `unknown`, with `pacto.trianalab.io/owner-conflict`. An inferred owner
never fills `spec.owner`. An API copies its Component's owner and owner
annotations.

## Lifecycle, system and relations

`spec.lifecycle` is `deprecated` when the running revision deprecates the
service, else `unknown`; a running target never becomes `production`.
`spec.system` is the declared business domain. An unknown domain sets
`pacto.trianalab.io/business-domain: unknown` and no system.

`spec.dependsOn` lists the services and external systems the running revisions
declare or that traces observed. `consumesApis` lists the interfaces a
dependency requires that the resolved provider revision and the provider's
running revision both declare, so every ref names an API in the file. A dependency
only an older, unused revision declares is left out. Each ref's provenance is in
`pacto.trianalab.io/relation-provenance` as `<ref>=declared`,
`<ref>=observed@<time>` or `<ref>=declared+observed@<time>`, where the time is
when a trace last saw the call. An edge Pacto only inferred never enters `spec`;
it is listed in `pacto.trianalab.io/inferred-depends-on`.

## Inferred and observed entities

Backstage's catalog table cannot filter on annotations, so entities Pacto did
not read from a contract also carry a tag: `pacto-inferred` on every inferred
entity, and `pacto-observed` on every stub and on a service Pacto only sees
running, with no contract it could read. Filter them out of the catalog table
with the tag picker. A stub whose name matched more than one contracted service
adds `pacto.trianalab.io/caller-ambiguous: "true"` and is `partial`.

## Annotations

| Annotation | Meaning |
|---|---|
| `pacto.trianalab.io/provenance` | How Pacto knows the entity: `declared`, `observed`, `declared+observed` or `inferred` |
| `pacto.trianalab.io/as-of` | When the snapshot was built |
| `pacto.trianalab.io/completeness` | `complete` or `partial` |
| `pacto.trianalab.io/service-key` | The full service key |
| `pacto.trianalab.io/owner-provenance` | Where the owner comes from: `declared`, `declared-via-target`, `attested` or `inferred` |
| `pacto.trianalab.io/owner` | The raw owner when it is not a valid Backstage name |
| `pacto.trianalab.io/owner-unmapped` | `"true"` when the owner could not become a ref |
| `pacto.trianalab.io/owner-conflict` | `"true"` when two owners claim the service |
| `pacto.trianalab.io/business-domain` | `unknown` when the domain is unknown |
| `pacto.trianalab.io/name-provenance` | `inferred` when the service name was inferred |
| `pacto.trianalab.io/name-source` | Where an inferred name came from |
| `pacto.trianalab.io/relation-provenance` | Provenance of each `dependsOn` ref |
| `pacto.trianalab.io/inferred-depends-on` | Edges Pacto only inferred |
| `pacto.trianalab.io/hidden-relations` | How many relations your view hides |
| `pacto.trianalab.io/unresolved-dependencies` | How many declared dependencies, configuration or policy references did not resolve |
| `pacto.trianalab.io/external-key` | The external system's key |
| `pacto.trianalab.io/caller` | The observed caller's identity |
| `pacto.trianalab.io/caller-ambiguous` | `"true"` when the caller matched more than one service |

A Component is `partial` when the snapshot is partial, your view hides some of
its relations, a declared dependency did not resolve or owners conflict. A
hidden service or edge is only ever counted, never named.

## An exported Component

This Component comes from the [demo](examples/demo-tour.md):

```yaml
kind: Component
metadata:
  name: fulfillment-service
  namespace: default
  annotations:
    pacto.trianalab.io/as-of: "2026-11-16T12:00:00Z"
    pacto.trianalab.io/completeness: complete
    pacto.trianalab.io/owner-provenance: declared
    pacto.trianalab.io/provenance: declared
    pacto.trianalab.io/relation-provenance: component:default/event-bus=declared,component:default/inventory-service=declared,component:default/shipping-rates=declared
    pacto.trianalab.io/service-key: fulfillment-service
spec:
  type: service
  lifecycle: unknown
  owner: group:default/logistics-team
  system: fulfillment
  dependsOn:
    - component:default/event-bus
    - component:default/inventory-service
    - component:default/shipping-rates
  providesApis:
    - api:default/fulfillment-service-shipment-events
    - api:default/fulfillment-service-shipments
  consumesApis:
    - api:default/inventory-service-stock-events
```

## Agents read the same data

Agents read the same services through `pacto_fleet_search` and
`pacto_fleet_get`; the provenance names (declared, observed, declared+observed,
inferred, and for owners declared-via-target and attested) are identical.
