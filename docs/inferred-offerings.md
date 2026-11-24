# Inferred offerings

A platform that already describes its infrastructure, such as Crossplane with
its XRDs, should not have to describe it again in a contract. The operator
reads each XRD as an *inferred offering*: a signed record that lists the
offering, its classes and its parameters. A person reviews it and accepts it
as a declared [platform offering](platform-offerings.md). The principle is
infrastructure only through offerings; every change planned and checked
before it applies.

An inferred offering never counts as declared, never satisfies a range, never
clears a gate and is never offered to consumers. Only the declared offering a
person accepts does.

## What the operator publishes

With `--registry` set, a cluster-wide operator reads every
CompositeResourceDefinition and the Compositions that select it, once a
minute, and pushes one bundle per XRD, signed with the installation key, to
its own repository `<registry>/pacto-inferred/crossplane-<clusterID>/services`.
Two installations that share a registry write to different repositories. A
Crossplane installed after the operator is picked up on the next run. See
[Inferred offerings](integrations/kubernetes/profiles.md#inferred-offerings)
in the operator profiles for the flag, the chart value and the read-only rule.

| Offering field | Read from the XRD |
|---|---|
| `service.name`, `service.offering` | The claim kind (`spec.claimNames.kind`), else the kind (`spec.names.kind`), lowercased |
| `service.version` | `0.0.0-inferred.<generation>.<k>`, the XRD's generation; the person sets the real version on acceptance |
| `service.summary` | The first line of the schema description, else `Inferred from XRD <name>` |
| `provisioning` configuration | `openAPIV3Schema.properties.spec` of the served, referenceable version |
| `class` | An enum of the names of the Compositions that select the XRD, required |

The mapper keeps every bound and enum, drops the `x-kubernetes-*` keywords
and sets `additionalProperties: false` on every typed object. A field that
keeps unknown values keeps no type, so acceptance names it open. An XRD that
no Composition selects gets no `class` and a `NO_CLASS` finding.

## The format

Any tool may push an inferred offering: the operator is one producer. The
format is a [contribution](write-a-source.md) whose `pacto.yaml` also declares
`service.offering`, `service.summary` and `configurations`, each entry with
only `name`, `schema` and `required`. Every other field is refused, as for any
contribution.

| File | Holds |
|---|---|
| `pacto.yaml` | The service name and version, the offering, its summary and the `provisioning` configuration |
| `configuration/provisioning.schema.json` | The JSON Schema of the parameters a consumer sends |
| `source/object.yaml` | The object it was read from: `apiVersion`, `kind`, `metadata.name`, `metadata.generation` and its spec |
| `source/classes.yaml` | The objects each class names, such as the Compositions |
| `pacto.inferred.yaml` | The [inferred record](contract-reference/inferred-record.md), citing the `source/` files with their digests |

```yaml
pactoVersion: "2.5"
service:
  name: xbucket
  version: 0.0.0-inferred.2.0
  offering: xbucket
  summary: An object storage bucket.
configurations:
  - name: provisioning
    schema: configuration/provisioning.schema.json
    required: true
```

A schema field may carry `x-pacto-tier: contract` or
`x-pacto-tier: tunable`. A field with no tier is `contract`. The operator
writes no tier, so every field it maps reads `contract`; a `tunable` field is
the accepting person's choice in the declared offering.

Pacto reads the source object's kind and name from `source/object.yaml` as
the producer wrote them. [Contribute an offering](write-a-source.md#contribute-an-offering)
shows a producer script.

## List inferred offerings

`pacto fleet offerings` lists the declared offerings and counts the inferred
ones. `--inferred` lists those instead, each with its source object, its
producer, its classes, when it was made and the command that accepts it:

```text
$ pacto fleet offerings --inferred --detected k8s:pacto-system/pacto-operator-detected --discover ghcr.io/acme
OFFERING  PROVENANCE  FROM                                                        CLASSES       ASOF
xbucket   inferred    CompositeResourceDefinition xbuckets.storage.acme.io gen 2  large, small  2026-11-23T10:02:00Z
                      producer crossplane-6f1c
          accept: pacto declare offering:xbucket --from oci://ghcr.io/acme/pacto-inferred/crossplane-6f1c/services:xbucket_0.0.0-inferred.2.0 --detected k8s:pacto-system/pacto-operator-detected --registry <registry> --version <version> --owner <team>
asOf 2026-11-23T10:05:00Z  completeness complete
```

`pacto fleet entities --kind offering --declaration inferred` and
`pacto_fleet_entities` with `kind: offering` and `declaration: inferred` give
the same rows. On the dashboard, the **Inferred offerings** checkbox of the
Offerings list switches it to them, muted. A viewer without cluster-scope read
on the cluster an inferred offering came from does not see it.

Readers pull records only under a registry they already read, such as one
`--discover` or `--oci` names. They pull the operator's records only from the
repository its detection advertises, verified with the installation key the
same detection names, and another producer's records only when its grant's
key verifies them.

## Accept an inferred offering

`pacto declare offering:<name>` turns an inferred offering into a declared
one. Without `--yes` it prints the plan and writes nothing:

```sh
pacto declare offering:xbucket \
  --from oci://ghcr.io/acme/pacto-inferred/crossplane-6f1c/services:xbucket_0.0.0-inferred.2.0 \
  --detected k8s:pacto-system/pacto-operator-detected \
  --registry ghcr.io/acme/offerings --version 1.0.0 --owner team-storage
```

`--detected` names the operator detection whose installation key must verify
`--from`; content no key verified is refused with `OfferingSourceUnverified`.
`--version` is the version you publish, above the newest declared one.
`--owner` is required on the first acceptance. The offering is published to
`<registry>/<name>`, never under `pacto-inferred`, signed by your Sigstore
login, with a [declaration](contract-reference/inferred-record.md#declaration)
that names `inferredFrom: offering:<producer>/<service>@<digest>`. Run it
again with `--yes --intent <digest>` to publish. A later acceptance keeps the
summary, the lifecycle and the owner you set before.

An agent's `pacto_declare` with `service: offering:<name>` takes `version`,
`owner` and `from` and writes a `draft-<n>` a person accepts with
`--from-draft`. On the dashboard, the page of an inferred offering offers
**Accept this offering**, which plans and publishes the same way; on a shared
dashboard it needs `update` on the source object in its cluster.

## Close an open schema

Acceptance refuses parameters that are not a closed space with
`OfferingSchemaOpen` and names each open path: an object without
`additionalProperties: false`, a field with no type and a `class` with no enum.

```text
$ pacto declare offering:xbucket --from oci://ghcr.io/acme/pacto-inferred/crossplane-6f1c/services:xbucket_0.0.0-inferred.2.0 \
    --detected k8s:pacto-system/pacto-operator-detected --registry ghcr.io/acme/offerings --version 1.0.0 --owner team-storage
the offering's parameters are not a closed space, open at provisioning: schema.properties.backups, provisioning: schema.properties.tags: close each path in a folder (additionalProperties: false, a type, a class enum) and accept it with --dir
run: pacto declare offering:xbucket --from oci://ghcr.io/acme/pacto-inferred/crossplane-6f1c/services:xbucket_0.0.0-inferred.2.0 --detected k8s:pacto-system/pacto-operator-detected --registry ghcr.io/acme/offerings --version 1.0.0 --owner team-storage
```

Pull the bundle with `pacto pull <from> -o xbucket`, close each path in
`configuration/provisioning.schema.json` and run the same command with
`--dir xbucket`. The producer signature is still checked against `--from`.

## Drift

When the producer's newest record differs from the accepted one,
`pacto fleet offerings` adds a line under the declared offering, and the
offering page adds it to the provider:

```text
$ pacto fleet offerings
OFFERING    PROVENANCE  FROM                                      CLASSES  ASOF
postgresql  declared    ghcr.io/acme/offerings/postgresql 16.1.0  -        2026-11-23T10:00:00Z
            drift: source object changed since accepted (now generation 4)
asOf 2026-11-23T10:05:00Z  completeness complete  (+1 inferred: pacto fleet offerings --inferred)
```

Drift reads unknown, never in sync, when Pacto cannot tell: the producer
advertises no fresh, complete observation (only an operator's detection
advertises one), the source object is no longer observed or the viewer may
not see the producer. Accept the new record to clear it.

## Change an offering's parameters

When both revisions declare `service.offering`, `pacto diff` grades the
configuration schema as input a consumer sends: a removed field, a narrowed
bound or a removed class breaks, and a new optional field or a widened bound
is compatible. [Impact](impact.md) names the consumers whose current values
the new schema rejects.
