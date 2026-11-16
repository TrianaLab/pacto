# External catalog reference

This page describes how [the operational graph](operational-graph.md) names
the systems outside your Pacto system that contracts depend on: vendor APIs,
managed cloud services and legacy hosts. Each one is a single graph node of
kind `external`, so "who depends on Stripe?" has an answer without anyone
writing a contract for Stripe. The
[operational graph reference](operational-graph-reference.md#external-systems)
covers how queries return them.

![The External systems list: two vendor APIs, each with its class, consumer count and the catalog entry that joined it](assets/screenshots/dashboard-fleet-externals-light.png#only-light)
![The External systems list: two vendor APIs, each with its class, consumer count and the catalog entry that joined it](assets/screenshots/dashboard-fleet-externals-dark.png#only-dark)

An external system never clears a gate. Pacto delivers none, never checks a
version range against one and reports its status as `Unknown`.

## Identities

An identity is a path of lower-case segments. The same string is the graph key
and the CLI and MCP subject `external:<identity>`.

| Form | Example | Used for |
|---|---|---|
| `<vendor>` | `stripe` | A vendor API, one global segment |
| `<provider>/<service>/<account-or-project>/<region>/<id>` | `aws/rds/123456789012/eu-west-1/orders-db` | A managed cloud resource |
| `host/<registrable-domain>` | `host/corp-legacy.example` | A host no catalog entry classifies |

Rules:

- at most five segments and 253 bytes;
- each segment is 1 to 63 lower-case letters, digits, `.`, `_` or `-`, starts
  and ends with a letter or digit and never contains `..`;
- a `host` identity has exactly two segments;
- `other-egress` is reserved for the [cap](#the-cap) node.

An identity carries no cluster: a vendor is the same system whichever cluster
calls it.

## How a declared external joins

A dependency with `external: true` and no `ref` joins the first identity that
applies, in this order:

| Order | Match | Rule |
|---|---|---|
| 1 | `identity` | The dependency's [`identity`](contract-reference/dependencies-and-state.md#name-an-external-system) field (`pactoVersion: "2.5"`) |
| 2 | `catalog-alias` | A catalog alias equal to the dependency name, ignoring case; organization entries first |
| 3 | `host` | The registrable domain of a name that is a hostname with a public suffix: `api.corp-legacy.example` joins `host/corp-legacy.example` |
| 4 | `normalized-name` | The name lower-cased, with spaces, `_` and `/` turned into `-` and other characters dropped: `Legacy MQ` and `legacy-mq` join `legacy-mq` |

A dependency with a `ref` resolves through its provider contract and never
joins. A name that normalizes to nothing, or to `other-egress`, stays an
unresolved external dependency. Consumers write nothing new: two contracts that
declare `Stripe` and `stripe-api` join one `stripe` node through the default
catalog. Two unrelated systems that share a generic name, such as `model`, also
join by name; set `identity` or add an organization alias to tell them apart.
The external page says when a node is joined by name only.

## Catalog format

A catalog is one YAML file. Pacto decodes it strictly and rejects unknown keys.

```yaml
apiVersion: catalog.pacto.trianalab.io/v1
entries:
  - identity: stripe
    class: vendor-api
    label: Stripe
    aliases: [stripe, stripe-api]
    hosts: [api.stripe.com, "*.stripe.com"]
    sdkModules: [github.com/stripe/stripe-go]
    keyPatterns: ["STRIPE_*"]
  - identity: aws/rds
    class: managed-cloud
    hosts: ["*.rds.amazonaws.com"]
```

| Field | Required | Rule |
|---|---|---|
| `apiVersion` | Yes | Exactly `catalog.pacto.trianalab.io/v1` |
| `entries[].identity` | Yes | A valid [identity](#identities), unique in the file; a `managed-cloud` entry names exactly `<provider>/<service>` |
| `entries[].class` | Yes | `vendor-api`, `managed-cloud`, `legacy` or `unclassified` |
| `entries[].label` | No | The name the dashboard shows |
| `entries[].aliases` | No | Dependency names that join this entry; non-empty |
| `entries[].hosts` | No | Lower-case DNS names with an optional leading `*.` |
| `entries[].sdkModules` | No | SDK module paths |
| `entries[].keyPatterns` | No | Environment and secret key names: `A-Z`, `0-9` and `_`, with at most one trailing `*` |
| `entries[].otel` | No | `dbSystem`, `messagingSystem` and `rpcSystem` lists of OpenTelemetry system values |

An alias, host or key pattern claimed by two entries of one file is an error.
A file holds at most 2000 entries and 1 MiB. Pacto joins declared dependencies
by `aliases` today; the other matchers are parsed and validated so a catalog
written now keeps working when Pacto classifies hosts, SDK modules, key names
and OpenTelemetry callees.

## The default catalog

Every Pacto binary embeds a default catalog of well-known vendors (Stripe,
Twilio, SendGrid, Anthropic, OpenAI, GitHub, Slack, PagerDuty, Datadog, Auth0,
Algolia and Mailgun) and the AWS, Google Cloud and Azure managed services.
Each host and module path cites the vendor documentation it comes from.

Each core release publishes the same bytes as an OCI artifact at
`ghcr.io/trianalab/pacto/external-catalog:<core version>` and signs it with
Cosign keyless signing, as [Installation](installation.md#supply-chain-what-is-signed-and-what-is-not)
lists. The digest is the sha256 of a manifest that depends only on the catalog
bytes, so the embedded copy and the published artifact have one digest. Every
answer records that digest in `externalCatalogs`, whichever copy was used.

Pacto checks the default catalog's signature only when a configured source
already reads `ghcr.io`, so a private-registry or air-gapped installation never
calls it. The check runs in the background and never delays a build: the first
answer reports `signature: unknown` with a reason saying the check is running,
and a later refresh reports the result. When the signature verifies, `copy` reads
`registry`. Otherwise `copy` reads `embedded`: Pacto classifies with the copy
compiled into the binary and `reason` says why the signature was not checked.
A release run from a branch other than `main` signs with another identity, so
that version's default catalog reads `unverified` and the embedded copy still
classifies.

## Organization catalogs

An organization catalog adds entries and overrides default ones. An entry with
the identity of a default entry replaces it, and an alias, host or key pattern
the organization claims is taken from any default entry that also claims it.

Give it by digest:

| Surface | Setting |
|---|---|
| `pacto fleet`, `pacto impact`, `pacto mcp`, `pacto stats`, `pacto tui` | `--external-catalog oci://REPOSITORY@sha256:DIGEST` |
| `pacto dashboard` | `--external-catalog` or `PACTO_DASHBOARD_EXTERNAL_CATALOG` |
| The operator-managed dashboard | Chart value `dashboard.externalCatalog` |

A tag is refused, because a tag can move. The digest pin is what Pacto trusts
the catalog by: its signature is reported as `unknown` with the reason and
never decides whether it loads. A catalog Pacto cannot pull or parse is
skipped, the default catalog alone classifies and the answer is partial with
`EXTERNAL_CATALOG_UNAVAILABLE`.

Publish one with [ORAS](https://oras.land/) and pin the digest it prints:

```bash
oras push registry.example.com/platform/external-catalog:2026-11 \
  --artifact-type application/vnd.pacto.external-catalog.v1 \
  catalog.yaml:application/vnd.pacto.external-catalog.v1+yaml
```

Pacto reads any manifest with that `artifactType`, the OCI empty config
descriptor (ORAS writes it when no `--config` is given) and one layer of the
media type `application/vnd.pacto.external-catalog.v1+yaml`. From a Pacto
checkout, `go run ./cmd/genexternalcatalog check -f catalog.yaml` validates the
file merged over the default and prints the entry count. The digest it prints
belongs to Pacto's own minimal manifest; ORAS adds annotations, so pin the
digest the registry returns.

## The cap

One answer names at most 200 external systems. Beyond that, the least-consumed
fold into one `other-egress` node of class `other-egress`, whose `folded`
counts them; a relationship to a folded system keeps its real `toExternal` and
gains `foldedInto: other-egress`. The answer is partial with `EXTERNAL_CAP`.
The snapshot itself keeps every identity. A list answer shows at most 50
consumers of one external system, with `consumersTotal` and `sourcesTotal`
counting them all.

## Restricted views

A caller who cannot see a consumer never learns its name. An external system
lists only visible consumers and counts the rest in `hiddenConsumers`, and its
totals count visible data only. An external system that only hidden services
declare is left out and counted in the Overview's `hiddenExternals`. The cap
ranks on visible consumers, so two callers with the same view get the same
answer.

## Limitations

| Limitation | Raised when |
|---|---|
| `EXTERNAL_CATALOG_UNAVAILABLE` | The organization catalog could not be pulled or is not a valid catalog |
| `EXTERNAL_CAP` | An answer folds external systems into `other-egress` |

![The page of one external system: its consumers with each declared version, and the catalog entry and digest that classified it](assets/screenshots/dashboard-external-entity-light.png#only-light)
![The page of one external system: its consumers with each declared version, and the catalog entry and digest that classified it](assets/screenshots/dashboard-external-entity-dark.png#only-dark)
