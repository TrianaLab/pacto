# How Pacto relates to other tools

Pacto composes the interface tools it sits between, such as OpenAPI and config
schemas, and complements the deploy tools, such as Helm and Terraform. Its
core makes no deployment decisions. The opt-in delivery component decides
which version runs where and leaves applying it to Flux or Argo CD. Pacto sits on top of the IDP an
organization runs, public such as Backstage or Port, or its own private
portal, and adds ranges, impact and conformance to it.

This page grades Pacto and several nearby projects on the same criteria.
Reviewed on 2026-11-16. Maintainers review this page at every minor release.
These projects move fast, so check the source page before you rely on a cell.

## Criteria

Each column asks one question:

- **Versioned artifact**: the tool publishes the service description as an
  immutable, versioned artifact that others pull, such as an OCI image.
  Versioned snapshots that stay inside the tool are Partial.
- **Semantic diff**: the tool classifies a change by its compatibility impact.
  A line-based text diff is Partial.
- **Dependency graph**: the tool records which services depend on which.
  Dependencies between platform packages, or inside one application, are
  Partial.
- **Transitive policy**: the platform's rules follow every policy reference,
  fail closed and are evaluated across the dependency closure. Following the
  references without evaluating the closure is Partial.
- **Runtime verify**: the tool checks running workloads against an
  independently declared contract. Reconciling toward the tool's own desired
  state is Partial.
- **Deploys?**: the tool applies workloads itself.

A cell holds ✅ for first-class, Partial for adjacent or limited and a dash (—)
when the source page covers the area and describes no such feature. A cell holds
"not verified" when the source page alone does not settle it.

## The table

| | Versioned artifact | Semantic diff | Dependency graph | Transitive policy | Runtime verify | Deploys? | Source |
|---|---|---|---|---|---|---|---|
| **Score** | not verified | not verified | Partial | not verified | not verified | No | [Score specification](https://docs.score.dev/docs/score-specification/score-spec-reference/) |
| **Crossplane Configuration** | ✅ | not verified | Partial | not verified | Partial | Yes | [Configurations](https://docs.crossplane.io/latest/packages/configurations/) |
| **KubeVela** | Partial | Partial | not verified | not verified | not verified | Yes | [Version control](https://kubevela.io/docs/end-user/version-control/) |
| **Kratix** | Partial | not verified | Partial | not verified | not verified | Yes | [Promise reference](https://docs.kratix.io/main/reference/promises/intro) |
| **Kargo** | Partial | not verified | not verified | not verified | not verified | No | [Core concepts](https://docs.kargo.io/user-guide/core-concepts/) |
| **Argo CD** | not verified | Partial | not verified | not verified | Partial | Yes | [Argo CD overview](https://argo-cd.readthedocs.io/en/stable/) |
| **Flux** | not verified | not verified | Partial | not verified | Partial | Yes | [Kustomization](https://fluxcd.io/flux/components/kustomize/kustomizations/) |
| **Backstage** | not verified | not verified | ✅ | not verified | not verified | not verified | [Descriptor format](https://backstage.io/docs/features/software-catalog/descriptor-format) |
| **Port** | not verified | not verified | not verified | not verified | not verified | not verified | [Relate blueprints](https://docs.port.io/context-lake/data-model/setup-blueprint/relate-blueprints/) |
| **Pact** | not verified | Partial | ✅ | not verified | Partial | No | [Pact Broker introduction](https://docs.pact.io/pact_broker) |
| **oasdiff** | not verified | ✅ | not verified | not verified | not verified | No | [oasdiff README](https://github.com/oasdiff/oasdiff) |
| **buf** | not verified | ✅ | Partial | not verified | not verified | No | [buf README](https://github.com/bufbuild/buf) |
| **Pacto** | ✅ | ✅ | ✅ | ✅ (opt-in) | ✅ | No (delivery promotes, alpha) | This site |

What each source page shows:

- **Score**: a Score file describes one workload. Its resources are evaluated
  as an acyclic graph at deploy time, and a Score implementation turns the file
  into platform configuration.
- **Crossplane**: a Configuration is a versioned OCI package that lists the
  packages it depends on with version constraints. Crossplane installs and
  reconciles its contents and reports them `Healthy`.
- **KubeVela**: an ApplicationRevision keeps a snapshot of each published
  version, `vela live-diff` prints a line diff between the edited application
  and its current revision and KubeVela rolls back to a past revision.
- **Kratix**: Kratix keeps each Promise version as a Promise Revision, a
  Promise lists the Promises it requires and Kratix schedules resources to
  matching Destinations.
- **Kargo**: Freight is a Kargo resource that references specific revisions of
  images, charts and commits. Stages form a promotion pipeline, and a GitOps
  agent such as Argo CD does the deploying.
- **Argo CD**: Argo CD compares the live state of each application with the
  target state in Git, reports the differences and marks a drifted application
  `OutOfSync`. It syncs the manifests to the target clusters and analyses the
  health of their resources.
- **Flux**: a Kustomization applies the manifests from a source artifact and
  corrects drift on every interval. Its `dependsOn` list waits for other
  Kustomizations to be ready, and its health checks report the rollout status.
- **Backstage**: a Component descriptor lists the components and resources it
  depends on and the APIs it provides and consumes. The catalog turns these
  fields into two-way relations such as `dependsOn` and `dependencyOf`.
- **Port**: a relation connects two blueprints and the entities built from
  them. Dependencies between services are one of its common uses, and several
  sources can add targets to the same relation.
- **Pact**: consumer tests write the expected requests and responses to a
  pact file, and the Pact Broker shares it and draws the network of services.
  The provider build replays the pact against the provider, and `can-i-deploy`
  reads the results before a deployment proceeds.
- **oasdiff**: oasdiff compares two OpenAPI specs and lists the changes that
  break existing clients, from the command line or in CI. Its rules classify
  each change as breaking or non-breaking.
- **buf**: `buf breaking` compares a Protobuf schema with a past version and
  flags source, JSON or wire-format incompatibilities. A `buf.yaml` declares the
  schema modules it depends on, and `buf.lock` pins them.

## Where each tool wins

Each tool below wins at the job its source page describes. Pacto adds the check
named after it.

- **Score** wins at describing one workload in a file that each Score
  implementation turns into platform configuration,
  [Score specification](https://docs.score.dev/docs/score-specification/score-spec-reference/).
  When you run the workload under `pacto run` or `pacto agent` on Linux, or
  under the operator with its node agent turned on (`nodeAgent.enabled`), Pacto
  checks that it serves the ports its contract declares and, with eBPF, that it
  reaches the declared dependencies. What it cannot see it reports as unknown
  ([runtime conformance](runtime-conformance.md)).
- **Crossplane** wins at shipping Compositions and the packages they depend on
  as one portable OCI package,
  [Configurations](https://docs.crossplane.io/latest/packages/configurations/).
  Given two versions of a contract, Pacto classifies each change as breaking,
  potentially breaking or non-breaking ([diff](contract-reference/diff.md)).
- **KubeVela** wins at keeping a revision of each published application and
  rolling back to a past one,
  [Version control](https://kubevela.io/docs/end-user/version-control/). Given
  the old contract, the new one and the consumer contracts it can see, Pacto
  lists the consumers the new version would break ([impact](impact.md)).
- **Kratix** wins at packaging a platform service as a Promise with its API and
  the Promises it requires,
  [Promise reference](https://docs.kratix.io/main/reference/promises/intro).
  From the sources you connect, such as your registry or a cluster, Pacto shows
  which services depend on a service and which revision runs where
  ([operational graph](operational-graph.md)).
- **Kargo** wins at promoting Freight through a pipeline of Stages and leaves
  the deploying to a GitOps agent,
  [Core concepts](https://docs.kargo.io/user-guide/core-concepts/). In a pull
  request, Pacto lists which consumers would reject the new version, from the
  target snapshot your clusters publish or the contracts you name with `--root`
  ([who breaks](ci.md#on-every-pull-request-who-breaks)).
- **Argo CD** wins at syncing Git state to target clusters and showing where the
  live state differs, [Argo CD overview](https://argo-cd.readthedocs.io/en/stable/).
  With the health check from [GitOps gates](integrations/kubernetes/gitops.md)
  in `argocd-cm`, an Application goes Degraded when its Pacto is `NonCompliant`
  or `Invalid` and stays Progressing while the verdict is `Unknown`.
- **Flux** wins at applying manifests from a source artifact and correcting
  drift on every interval,
  [Kustomization](https://fluxcd.io/flux/components/kustomize/kustomizations/).
  With the health check expression from
  [GitOps gates](integrations/kubernetes/gitops.md), a Kustomization that holds
  a `NonCompliant` or `Invalid` Pacto turns unready and the Kustomizations that
  depend on it wait.
- **Backstage** wins at a catalog where each component lists what it depends
  on and the APIs it provides and consumes,
  [Descriptor format](https://backstage.io/docs/features/software-catalog/descriptor-format).
  A Pacto contract also records which versions of each dependency it accepts,
  so `pacto impact` names the consumers a new version breaks among the
  contracts it is given ([impact](impact.md)). See
  [Sit on top of any IDP](any-idp.md).
- **Port** wins at a data model where relations connect blueprints and their
  entities and several sources add targets to the same relation,
  [Relate blueprints](https://docs.port.io/context-lake/data-model/setup-blueprint/relate-blueprints/).
  From the sources you connect, Pacto keeps relationships declared in contracts
  apart from those observed in traffic and says how complete each answer is
  ([operational graph](operational-graph.md)). See
  [Sit on top of any IDP](any-idp.md).
- **Pact** wins at testing the requests each consumer expects against the
  provider before it deploys,
  [Pact Broker introduction](https://docs.pact.io/pact_broker). From the
  consumer contracts you name with `--root` or a fleet source, Pacto lists which
  would reject the new version and reports a breaking change it cannot fully
  check as unverified ([impact](impact.md)).
- **oasdiff** wins at listing the changes between two OpenAPI specs that break
  existing clients, from the command line or in CI,
  [oasdiff README](https://github.com/oasdiff/oasdiff). From the consumers
  your clusters' target snapshots name, or the contracts you name with `--root`,
  Pacto lists those whose accepted range rejects the new version
  ([who breaks](ci.md#on-every-pull-request-who-breaks)).
- **buf** wins at checking a Protobuf schema against a past version for source,
  JSON or wire-format breaks, [buf README](https://github.com/bufbuild/buf).
  Pacto classifies a change to the whole contract, including its
  configuration and dependencies, as breaking, potentially breaking or
  non-breaking ([diff](contract-reference/diff.md)).

## Pacto's own row

We graded our own row on the same criteria:

- **Transitive policy** is ✅ and opt-in. Pacto follows policy references from
  one policy contract to the next and fails closed when one cannot be resolved.
  By default it checks the policies against the root contract only.
  `pacto validate --closure` also checks every contract in the dependency
  closure against them, and fails when a dependency cannot be resolved. The
  flag is on `pacto validate` only. See
  [Dependency closure](contract-reference/validation.md#dependency-closure).
- **Semantic diff** classifies each change as non-breaking, potentially
  breaking or breaking ([diff](contract-reference/diff.md)).
- **Runtime verify** compares running workloads with the contract
  ([runtime conformance](runtime-conformance.md)).
- **Deploys?** Pacto's core deploys nothing. Delivery, which is alpha, moves
  the version digest on a Flux or Argo CD source, and Flux or Argo CD applies
  it ([delivery](integrations/kubernetes/delivery.md)).

Radius was in an earlier version of this table. Its row returns when a source
page settles its cells.
