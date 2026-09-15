# Evidence and boundaries

The rest of the [Concepts](concepts.md) index: the distinctions between what a
contract declares, what a collector observes and what the engine judges, and the
boundaries between the surfaces that answer.

## Declaration, observation and judgement

### Declared is not observed

A contract states intent. A collector or a tracer reports what is actually
there. Every relationship in the graph carries a provenance discriminator so the
two never merge, and they live in separate adjacency indexes so neither can leak
into the other's answers.
→ [Relationships: declared, observed, inferred](operational-graph.md#relationships-declared-observed-inferred)
· [Declaration versus observation](model.md#declaration-versus-observation)

### Declared-but-not-observed is not confirmed absence

A declared dependency we did not see in traffic may be absent, or may simply be
idle, or unobservable by the sources we have. Pacto reports
`declared-not-observed` only when it had enough observation to have seen it, and
`insufficient` otherwise.
→ [Observed dependencies and reconciliation](operational-graph.md#observed-dependencies-and-reconciliation)

### Observed-only is not invalid

A dependency in traffic that no contract declares is a finding about the
contract, not a defect in the observation. It is surfaced as a difference, not
discarded.

### Absence of evidence is not evidence of absence

The evidence model has no way to assert that something is not there: an
observation is recorded as observed, unsupported, failed, stale or insufficient.
"We could not look" therefore cannot be stored as "we looked and it was
missing".

### Evidence is not a finding

Evidence is what a collector saw. A finding is a verdict reached by comparing a
contract against evidence, and the only thing that turns one into the other is
the engine's `Evaluate` function. A finding cites the evidence behind it by
source and timestamp, never by carrying the observation itself, so there is
exactly one place a verdict can come from.
→ [The engine](model.md#the-engine)
· [Declaration vs observation](collectors.md#declaration-vs-observation)

### Readiness is not compliance

Readiness is a team's own scored self-assessment of a contract revision, with a
threshold and an expiry. Compliance is evidence-derived: what the running
instance is actually doing. A revision that passes every readiness check can run
on a target that is non-compliant, and both statements are true at once.
→ [`readiness`](contract-reference/dependencies-and-state.md#readiness)

### Contract intent is not runtime truth

The whole point of the control loop is that the two can disagree. Pacto's job is
to say so precisely, not to reconcile them by assumption.
→ [The operational control loop](model.md#the-operational-control-loop)

---

## Boundaries

### A data source is not a collector

A collector observes an environment and produces evidence. A data source is
where the graph reads records *from* — a local directory, a registry, a cluster,
a cache. Their health is also different: a data source being reachable says
nothing about whether the evidence it carries is fresh.
→ [The roles](collectors.md#the-roles)
· [Source health is not evidence freshness](observation-sources.md#source-health-is-not-evidence-freshness)

### Data source health is not fleet knowledge completeness

One healthy source in a fleet of ten tells you that source answered. It tells
you nothing about the other nine, and the snapshot's completeness is the
fleet-wide claim.
→ [Sources](fleet-sources.md#sources)

### The contract catalog is not the operational graph

The catalog answers what a set of contract roots and their closure *declare*,
from a frozen discovery session that holds no runtime observation and outlives
nothing. The operational graph answers what is actually *running*. A complete
catalog closure and a complete fleet snapshot are complete about different
worlds.
→ [Contract catalog discovery](mcp-catalog-discovery.md)

### Discovery is not authorization, and neither is execution

Being able to find and read a contract grants no permission to change anything
and performs no action. Pacto's read surfaces stay read surfaces.
→ [What it is not](mcp-catalog-discovery.md#what-it-is-not)
· [Why Pacto does not act or authorize](operational-graph.md#why-pacto-does-not-act-or-authorize)
· [It recommends review, it does not act](impact.md#it-recommends-review-it-does-not-act)

### A contract status is not a knowledge state

The Kubernetes operator writes `Unknown` on a `Pacto` resource to mean *this
contract was evaluated and a required assertion could not be decided* — a
verdict about one service, reached with full knowledge that it could not be
reached. The `unknown` above is about the answer itself: no completeness arrived
at all. Same word, opposite subject. The operator's ladder (`Compliant`,
`Warning`, `NonCompliant`, `Reference`, `Unknown`, `Invalid`) is a per-contract
verdict set and never a `meta.completeness`.
→ [What the operator reports](integrations/kubernetes/overview.md#what-it-reports)
· [Status is `Unknown`](integrations/kubernetes/troubleshooting.md#status-is-unknown)

### A rendering may drop detail; it always carries the meaning it was given

The dashboard may show fewer rows, shorter labels and collapsed sections. What
it may not do is decide what something means: canonical identity, completeness
and every verdict arrive from the backend already decided, and the browser never
reconstructs them by heuristic.
