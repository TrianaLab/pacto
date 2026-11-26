# Golden paths reference

This page describes how [the operational graph](operational-graph.md) treats
[golden paths](golden-paths.md) and the services created from them. The
[operational graph reference](operational-graph-reference.md) covers sources
and queries.

## Classification

A golden path is a service the graph treats as a starting point for new
services. A service is a golden path when:

- the newest revision Pacto can read declares
  [`service.template`](contract-reference/sections.md#golden-path-template);
- the service has no operational target;
- every source that reports targets (Kubernetes, the Evidence Server and a
  target state file) is available and complete, so Pacto knows the service
  runs nowhere.

While such a source is unavailable or partial, a service without targets
stays an ordinary service, so an outage never drops a running service from
impact. A running service that declares `service.template` also stays a
service and raises `GOLDEN_PATH_DEPLOYED` (warning, category `golden-path`).
With no target source in the fleet, Pacto cannot tell a golden path from a
service that runs elsewhere.

A golden path's dependencies and references describe what services created
from it need. They are never consumption:

- impact, graph traversal, `dependents`, the neighborhood and offering
  consumers leave the path out;
- the path has no offering membership and never joins `OFFERING_CONFLICT`;
- the Overview service, owner, domain, readiness and attention summaries leave
  it out and count it in `goldenPaths` and `invalidGoldenPaths`; their links
  carry `template=exclude`, so a count matches its list;
- an owner or domain that holds only golden paths has no row in the owner or
  domain list, and its own page still answers with zero services;
- the path's detail lists the same edges as requirements, through the
  offerings they reach.

## Declared origins

A service's origin is the
[`service.origin`](contract-reference/sections.md#service-origin) of its newest
readable revision. Pacto resolves it to the golden path revision whose content
digest (`lock.HashFS` of the bundle, the digest `pacto.lock` records) or
registry digest matches. An origin that matches no golden path, or more than
one, keeps its `ref` and `digest` with a `reason` and links nowhere. Every
origin is labelled `declared`.

A service created from a directory records as `ref` the coordinate the path's
`publish.bundle` and version name, which Pacto does not check against the
registry. A path with no `publish.bundle` records a path relative to the new
contract, or only the folder name when that path would climb above the home
folder, so no host folder such as `/Users/<name>` is published.

## Discovery

With a registry to discover (`--discover`, `pacto dashboard --registry` or the
chart's `registry`), the fleet gains a source named `registry` that lists every
golden path and offering published under that `<host>[/<path>]`:

1. the newest confirmed entry of each service in
   [`<registry>/pacto-index`](publishing.md#the-organization-index-entry);
2. the newest version tag of each repository the registry's `_catalog` lists
   under the path. The walk skips `pacto-index`, `pacto-targets`,
   `pacto-evidence` and every repository under `pacto-inferred`, whose
   contracts have their own readers and trust rules.

Pacto reads each candidate's manifest to tell its
[bundle type](publishing.md#bundle-type), and reads the config labels of a
bundle pushed before bundles were typed. It pulls only templates and
offerings, by digest, with the same signature and owner checks as `--oci`. It
keeps a pulled revision only when its contract declares `service.template` or
`service.offering` and its `service.name` matches its repository's last path
segment, so a bundle pushed under another name never stands in for a real
path. A discovered offering that runs nowhere is a declared service with no
target. `pacto impact` and `pacto ci` ignore discovery. On a dashboard or MCP
server that discovers, a partial listing never makes an impact answer
partial, since templates and offerings consume no service.

Listings are kept for a minute. The source reads complete only when the index
was read or is absent and `_catalog` was served, with no bound hit. Otherwise it
is partial with one of these limitations, which name the host, the path and the
index repository, never another repository or a digest:

| Code | Meaning |
|------|---------|
| `REGISTRY_LISTING_INDEX_ONLY` | The registry serves no `_catalog`, or refused it to this credential; only what the index lists was found |
| `REGISTRY_LISTING_NONE` | No index and no `_catalog`: the golden paths and offerings under the path are unknown |
| `REGISTRY_LISTING_TRUNCATED` | A bound (2000 services, the page limit) or the read timeout stopped a listing |
| `REGISTRY_LISTING_UNAVAILABLE` | A listing failed; when both fail the source is unavailable |
| `REGISTRY_LISTING_ENTRY_UNREADABLE` | A count of listed bundles that could not be read or that do not match their repository; a restricted view gives no count |

## Filters

| Parameter | Values | Effect |
|-----------|--------|--------|
| `template` (entities) | `only`, `exclude` | `only` keeps golden paths and, with no kinds, lists services only; `exclude` leaves them and their revisions out. Absent lists every service and marks golden paths `template` |
| `createdFrom` (entities; MCP `created_from`) | a service key, `(none)` or `(unresolved)` | Keeps the services whose origin resolves to that golden path. `(none)` keeps the services that declare no origin and `(unresolved)` those whose origin resolves to no golden path in the view. A hidden, absent or non-path key answers an empty list |
| `sort` (entities) | `created` | Golden paths that created the most services first, then the most non-compliant of those, then by name |
| `template` (search) | `only`, `exclude` | The same filter on search; the default keeps golden paths, each hit marked `template` |

A golden path row in an entity list carries `created`, the number of services
created from it, golden paths derived from it left out like the Services list
leaves them out, `nonCompliant`, how many of those are non-compliant, and its
signature status: `not-checked` when no source that read the path checks
signatures, and the path's detail then has no `signature`.

## Services by golden path

The aggregate of an entity list over services carries `origins`, which
partitions the matched services that are no golden path by their declared
origin, over the whole population before paging:

| Field | Counts |
|-------|--------|
| `byPath` | Up to 10 golden paths, the most services created first, each with `services`, `nonCompliant` and `hidden` |
| `paths` | Golden paths that created at least one matched service |
| `beyond` | Services created from the golden paths past the ranking |
| `unresolved` | Services whose origin resolves to no golden path in the view (`createdFrom=(unresolved)`) |
| `none` | Services that declare no origin (`createdFrom=(none)`) |
| `hidden` | Services created from a golden path that the viewer cannot see, in no other count |

The `services` of `byPath`, `beyond`, `unresolved` and `none` sum to the
matched services, and their `nonCompliant` counts sum to the non-compliant
ones. `pacto fleet entities` prints the ranking under the list, and the
dashboard's Golden paths page draws it.

## Follow the creation

`pacto init --from` ends with the step list: signature, contract, validation,
each plugin, delivery with `--delivery`, publishing, the fleet, deployment and
provisioning. A step is
`done` only when Pacto established it. Publishing, the fleet and deployment
stay `unknown` at creation, since no source has observed the new service yet.

Once a fleet source reads the service, `pacto fleet entity service gift-cards`
and the dashboard show its declared origin, the golden path revision whose
content digest matches and the same steps as a checklist derived from what the
fleet observes. `pacto fleet entities --created-from http-service` and
`pacto_fleet_entities` with `created_from` list every service created from a
path.

The [creation checklist](#creation-checklist) lists how each step is derived.

## Creation checklist

A service with an origin carries `creation`, a checklist derived from what the
fleet holds. Each step is `done` only on its signal:

| Step | `done` when | Otherwise |
|------|-------------|-----------|
| `signature` | never | `unknown`: a verified signature names a signer a publisher rule, pin or policy accepted, and an `unverified` one a signer none of them covers, which is no failure |
| `contract` | a revision was read | |
| `validation` | the newest revision is valid with no `*_UNVERIFIED` or `*_NOT_ENFORCED` finding | `unknown` listing those codes; `failed` when invalid |
| `plugin` | never | `unknown`: Pacto does not observe what a plugin wrote |
| `published` | a registry served a revision | `unknown`: seen only in local files |
| `fleet` | always, naming the sources | |
| `deployed` | the fleet observes a target | `unknown` |
| `provisioning` | every provider reports `Ready` | `failed` on any `Failed`; `unknown` without reports; `not-applicable` when nothing provisions |

The `delivery` step appears only in the step list `pacto init --from
--delivery` and `pacto_create` with `delivery` return. It is `planned`, then
`done`, when the Pacto resource and every Stage were planned. Otherwise it is
`unknown` with the reason.

## Create service

`POST /api/fleet/services` plans a service created from a golden path, and
`POST /api/fleet/services/accept` signs the plan or saves it as a draft.
`pacto_create_service` takes the same inputs in snake case, plus
`on_behalf_of`, `dry_run` and `intent_digest`.

| Field | Meaning |
|---|---|
| `path` | The service key of a declared golden path in your view; never a reference |
| `name` | The new service's name |
| `owner` | The owner team; the person who signs belongs to it or is a default approver |
| `namespace` | Where the review of `create` on `services.pacto.trianalab.io` runs |
| `cluster` | The cluster of that namespace; empty for the only one. Over MCP, the cluster the caller signed in against |
| `repository` | Where `0.1.0` goes; defaults to `<registry>/<name>` |
| `revision` | The publish revision; defaults to `pacto-create:<signer>:<RFC3339 time>` |
| `offerings` | `[{offering, values, environments}]`: values set once, and per environment on tunable fields only |
| `fromDraft` | `{tag, digest}` of an agent's created draft to review and sign; dashboard only |

A repository sits under the installation registry and ends in the service
name. A revision is 1 to 128 printable characters with no whitespace, and a
`pacto-create:` revision names only the person creating the service; a commit
SHA is fine. The plan answers both, and the write sends them back.

Each refusal of an input carries `code`, `message` and `field`, plus
`closest`, `allowed`, `cli` and `mcp` when they apply: `cli` and `mcp` are the
`pacto init` command and the `pacto_create_service` fragment that set the fix. `field` is
`configurations[<name>].values.<key>` for a value set once,
`environments[<env>].<configuration>.<key>` for a value set per environment,
`environments[<env>]` or `offerings[<offering>]`. A refused plan has no
`intentDigest` and publishes nothing.

| Code | Meaning |
|---|---|
| `ServiceExists` | The name is taken in this registry, by a version or by someone else's draft |
| `GoldenPathNotDeclared` | No declared golden path pinned by digest has this key in your view |
| `RepositoryOutsideRegistry` | The repository is outside the registry or does not end in the service name |
| `OfferingNotConsumable` | Not an offering you may consume here, hidden or absent alike |
| `OfferingUnknown` | The offerings list is partial, so Pacto could not confirm the offering |
| `ParameterNotDeclared` | The offering's schema declares no such field; `closest` names a near key |
| `ParameterOutOfRange` | The schema refuses the value; `closest` is the nearest bound or allowed value |
| `ParameterSensitive` | A sensitive field takes a Secret reference, never a literal |
| `ClassNotOffered` | The offering has no such class; `allowed` lists its classes |
| `TierNotTunable` | A `contract` tier field, or one with no tier, is the same in every environment |
| `EnvironmentNotDeclared` | The golden path declares no such environment |

The first three refuse the whole plan and carry `code`, `message` and
`command`. A denied review answers `NotAllowed`, with HTTP 403 on the
dashboard, and a review that fails `ReviewFailed`.

## Golden paths in a restricted view

A namespace-scoped viewer sees a golden path that has no Kubernetes target,
like a declared offering provider, with only the revisions that declare
`service.template`. Nothing in the view resolves to the older revisions it
drops. Services created from it that the viewer cannot see are counted in
`hiddenCreated`, and its requirements on services the viewer cannot see in
`hiddenRequirements`, never named. A visible service whose golden
path is hidden keeps its declared `ref` and `digest` and links nowhere, with
the reason "the golden path it names is not in this view".
