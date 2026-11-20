# Inferred contracts for external systems

This page describes the inferred contracts Pacto publishes for the
[external systems](external-catalog-reference.md) your services call, such as
Stripe or a legacy host. An inferred contract records what Pacto sees of a
system nobody in your organization declares. A consumer can point a dependency
at it, and Pacto never checks a version range against it.

## What the operator publishes

Once the chart's `registry` value is set, for example `ghcr.io/acme`, the
operator publishes one inferred contract for every external system with at
least one consumer, to `<registry>/pacto-inferred/<identity>`:

```sh
oras repo ls ghcr.io/acme | grep pacto-inferred
```

```text
acme/pacto-inferred/stripe
acme/pacto-inferred/host/corp-legacy.example
```

The operator runs the publisher every 5 minutes and needs no other value: no
flag, no extra chart key and no Git access. Without `registry` nothing is
published. The operator log reports each run, and the metrics
`pacto_inferred_contracts_published_total` and
`pacto_inferred_contracts_publish_errors_total` count pushes and errors.

## The bundle

Each revision holds two files. `pacto.yaml` names the service, a version and
the external system it describes in
[`service.external`](contract-reference/sections.md#service):

```yaml
pactoVersion: "2.5"
service:
  name: stripe
  version: 0.0.3
  external: stripe
```

`pacto.inferred.yaml` holds the `surface`: the hosts, ports and client majors
the fleet sees, each with the sources that saw it, the digest of the catalog
that classified it and when it was first and last seen:

```yaml
format: pacto.inferred/v1
inputs:
  pacto: 4.6.0
  discovery: "1"
  sources: []
  catalog: ["default sha256:9c1f..."]
surface:
  identity: stripe
  class: vendor-api
  asOf: "2026-11-20T09:15:00Z"
  completeness: {state: complete}
  catalog: {origin: default, digest: "sha256:9c1f...", entry: stripe}
  hosts:
    - value: api.stripe.com:443
      sources: [prod-workloads]
      catalog: "sha256:9c1f..."
      firstSeen: "2026-11-02T08:00:00Z"
      lastSeen: "2026-11-20T09:10:00Z"
  ports:
    - {value: "443", sources: [prod-workloads], catalog: "sha256:9c1f..."}
  clients:
    - {value: stripe-go/76, sources: [org-repositories]}
```

The bundle never names a consumer. Who calls the system stays in the fleet,
where [restricted views](operational-graph-reference.md#query-semantics) apply.
The version is a count of surface changes and never a vendor version.

## When a revision is published

A new `0.0.<n>` tag is pushed only when the surface changes: a host, port or
client added or removed. A new caller adds a source and publishes nothing, and
so do changed sources, times, `asOf` and `completeness`. Tags are never
overwritten.

`asOf` says when the surface was computed and `completeness` whether the fleet
snapshot was complete. A partial snapshot never removes a value: the operator
keeps every value the last revision it signed holds and only adds new ones.
Removing a value needs a complete snapshot.

At most 20 contracts are pushed per run; the rest wait for the next run.

## The signer

The operator signs every revision with the installation key, the key it
creates once in the Secret `pacto-operator-installation-key`, and annotates
the manifest with `run.pacto.contract.provenance: inferred` for registry UIs,
`oras` and other readers. Pacto itself reads the location and the signature,
never the annotation.

An operator signature never makes a publisher. Readers name the operator's
signer from the `snapshotKey` the `pacto-operator-detected` ConfigMap
advertises (`--detected` on the CLI). A revision only the operator signed shows
its signature as `unverified` with source `operator`, even when its
`publish.sign` names the operator. A declared revision that an accepted
publisher also signed keeps that publisher's verdict. A revision under
`pacto-inferred` reads as inferred from its location alone, signed or not.

Pull access to `pacto-inferred` is a separate audience that the registry's
ACL governs: restricted views cover Pacto's own answers, never direct registry
pulls.

## A range against an inferred contract

A dependency whose ref names `<registry>/pacto-inferred/<identity>` joins that
external system. When it declares a `compatibility` range, the range reads
`rangeStatus: unknown` with `rangeReason: INFERRED_PROVIDER`, even when the
version would satisfy it:

```sh
pacto fleet get external:stripe --output-format json \
  | jq '.external.provider.rangeStatus,.external.provider.rangeReason'
```

```text
"unknown"
"INFERRED_PROVIDER"
```

Every gate reads it as unverified. [Impact](impact.md) gives each consumer the
verdict `unknown` and the gate `unverified`, and delivery's move-first checks
and the Stage environment gate never pass it. A range against a revision only
the operator signed, wherever it is published, reads the same way. An inferred
contract never clears a gate.

## Declare instead

Each external system with such a range raises one attention item,
`INFERRED_PROVIDER`, in the knowledge category: "2 services check a range
against stripe's inferred contract". Its next action is `declare`, through the
`pacto declare` command or the `pacto_declare` MCP tool. Declare a contract for
the system and point the ranges at its repository: the reason then disappears.

The External page on the dashboard shows the inferred contract in a collapsed
block after Consumers, with its surface and the signer.

## Publish without an operator

An installation with no operator, such as the
[Compose demo](examples/compose-demo.md) or a Docker host, publishes with
[`pacto fleet publish-inferred`](cli-reference.md#pacto-fleet-publish-inferred):

```sh
pacto fleet publish-inferred --registry ghcr.io/acme --key installation.key \
  --oci oci://ghcr.io/acme/checkout
```

`--key` takes a PEM private key or a `pacto evidence keygen` seed. Never run it
against a registry an operator already writes. No MCP tool publishes inferred
contracts: agents never sign as the installation.

## Evidence

A provisioning or usage key scoped with `allowedProviderRepos` never matches a
`pacto-inferred` repository, and the Evidence Server refuses provider evidence
about one; see [Evidence](evidence.md). Declare a contract for the external
system first.
