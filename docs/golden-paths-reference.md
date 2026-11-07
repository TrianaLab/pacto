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
- every Kubernetes source is available and complete, so Pacto knows the
  service runs nowhere.

While a Kubernetes source is unavailable or partial, a service without targets
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

## Filters

| Parameter | Values | Effect |
|-----------|--------|--------|
| `template` (entities) | `only`, `exclude` | `only` keeps golden paths and, with no kinds, lists services only; `exclude` leaves them out. Absent lists every service and marks golden paths `template` |
| `createdFrom` (entities; MCP `created_from`) | a service key | Keeps the services whose origin resolves to that golden path. A hidden, absent or non-path key answers an empty list |
| `template` (search) | `only`, `exclude` | The same filter on search; the default keeps golden paths, each hit marked `template` |

A golden path row in an entity list carries `created`, the number of services
created from it, and its signature status.

## Creation checklist

A service with an origin carries `creation`, a checklist derived from what the
fleet holds. Each step is `done` only on its signal:

| Step | `done` when | Otherwise |
|------|-------------|-----------|
| `signature` | never | `unknown`: a verified signature names the signer the path's own contract declares, which no trust config checked; `failed` when the signature failed verification |
| `contract` | a revision was read | |
| `validation` | the newest revision is valid with no `*_UNVERIFIED` or `*_NOT_ENFORCED` finding | `unknown` listing those codes; `failed` when invalid |
| `plugin` | never | `unknown`: Pacto does not observe what a plugin wrote |
| `published` | a registry served a revision | `unknown`: seen only in local files |
| `fleet` | always, naming the sources | |
| `deployed` | the fleet observes a target | `unknown` |
| `provisioning` | every provider reports `Ready` | `failed` on any `Failed`; `unknown` without reports; `not-applicable` when nothing provisions |

## Golden paths in a restricted view

A namespace-scoped viewer sees a golden path that has no Kubernetes target,
like a declared offering provider, with only the revisions that declare
`service.template`. Services created from it that the viewer cannot see are
counted in `hiddenCreated` and never named. A visible service whose golden
path is hidden keeps its declared `ref` and `digest` and links nowhere, with
the reason "the golden path it names is not in this view".
