# Ask questions about your organization

Pacto joins your contracts, the clusters they run in and the traffic it
observes into one [operational graph](operational-graph.md). An AI agent asks
that graph questions through the read-only tools of `pacto mcp --fleet`, a
person through `pacto fleet` or the dashboard. All three answer from the same
queries, so an agent and a person asking the same question get the same answer.

| Question | MCP tool | CLI command | Dashboard view |
|---|---|---|---|
| Which services does my team own? | `pacto_fleet_entities` with `kind=owner` for the owner keys, then `pacto_fleet_search` with `owner` or `pacto_fleet_entities` with `owner_key` | `pacto fleet entities --kind owner`, `pacto fleet search --owner` | Owners |
| Which services depend on payments? | `pacto_fleet_graph` with `direction=dependents`; each node carries `provenance` | `pacto fleet graph payments-service --direction dependents` | Operational graph |
| What is unhealthy right now? | `pacto_fleet_attention` with `severity=error` | `pacto fleet attention --severity error` | Needs attention |
| Which services have undocumented dependencies? | `pacto_fleet_attention` with `category=undeclared` | `pacto fleet attention --category undeclared` | The Overview tile "Undeclared runtime dependencies observed" |
| What changed in the last 24 hours? | Not yet ([#459](https://github.com/TrianaLab/pacto/issues/459)) | Not yet | Not yet |
| Which infrastructure capabilities are available? | `pacto_fleet_entities` with `kind=offering`, `pacto_fleet_entity` with `kind=offering` | `pacto fleet entities --kind offering` | Offerings |
| Which services does this deployment affect? | `pacto_impact` with `event=regressed`, `service` and `revision` (an artifact digest cites delivery's recorded failures), or old and new refs for a contract change | `pacto impact --regressed <service>@<digest>`, `pacto impact <old> <new>` | Change analysis |
| What breaks if payments goes down? | `pacto_impact` with `event=unavailable` and `service` | `pacto impact --unavailable payments-service` | Change analysis, What if this service… |
| What uses this resource before I remove it? | `pacto_impact` with `event=removed` and `service` | `pacto impact --removed postgresql` | Change analysis, What if this service… |
| Which services does this policy change reach? | `pacto_impact` with the policy provider's old and new refs | `pacto impact <old> <new>` | Change analysis |
| Where are the biggest operational risks? | `pacto_fleet_attention` (most severe first), `pacto_fleet_entities` with `sort=health` | `pacto fleet attention`, `pacto fleet entities --sort health` | Needs attention |

## Read the answer

Every fleet answer carries `meta`. `asOf` is the time it describes and
`snapshotId` proves two answers came from the same view. `sources` says what
each source contributed and `limitations` what Pacto could not see. `pacto_impact`
carries the same fields at the top level. Cite them when you repeat an answer.
When `completeness` is `partial`, a missing result means unknown. An event
answer grades each consumer's `effect` (`breaks`, `may-degrade` or `unknown`),
and evidence older than 24 hours before `asOf` lowers a consumer's confidence
to `unknown`.

Each fact says how Pacto knows it:

- **Declared**: a contract says so.
- **Observed**: Pacto saw it at runtime, in a trace or through the Kubernetes
  operator.
- **Inferred**: Pacto derived it. The word has three uses: a transitive
  consumer in `pacto_impact`, a target linked to a revision by tag or version
  match and a relationship provenance reserved for future use.

An edge carries `provenance` (`declared`, `observed` or `declared+observed`),
and a graph node says how the edge that first reached it was known. A runtime
call no contract declares is listed under the `undeclared` attention category
as `DEPENDENCY_UNDECLARED`, and a caller with no contract as
`UNCONTRACTED_CALLER`.

## Not yet

One question needs data Pacto does not hold today: **changes in the last 24
hours** need a history of snapshots
([#459](https://github.com/TrianaLab/pacto/issues/459)).

For runtime health and incidents, [Investigate a problem with
Mira](investigate-a-problem.md) pairs these answers with Mira's signals.

## Open the dashboard from an answer

Start the server with the URL of a running Pacto dashboard:

```bash
pacto mcp --fleet --k8s --dashboard-url https://pacto.example.com
```

Search, graph, status, entities, entity and attention answers then carry an
`href` to each entity's page on that dashboard, so
an agent can hand you a link to the service, owner or attention list it is
talking about. Without the flag, answers carry no links.
