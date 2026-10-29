# How Pacto relates to other tools

Pacto composes the interface tools it sits between, such as OpenAPI and config
schemas, and complements the deploy tools, such as Helm and Terraform. Its
core makes no deployment decisions. The opt-in delivery component decides
which version runs where and leaves applying it to Flux or Argo CD.

This page grades Pacto and several nearby projects on the same criteria.
Reviewed on 2026-10-29. These projects move fast, so check the source page
before you rely on a cell.

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
| **Kargo** | Partial | not verified | — | not verified | not verified | No | [Core concepts](https://docs.kargo.io/user-guide/core-concepts/) |
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

Radius, Backstage and Port were in an earlier version of this table. Their
rows are out until a source page settles most of their cells.
