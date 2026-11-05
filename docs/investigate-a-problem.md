# Investigate a problem with Mira

When a service misbehaves, an engineer or an agent starts from that service and
asks what changed, what it depends on, who depends on it and who owns them.
Pacto answers from contracts, delivery records and the operational graph.
[Mira](https://github.com/trianalab/mira), an OpenTelemetry trace store,
answers from the traces your services send: when the problem started and which
signals show it. This guide maps each question to the tool that answers it and
says what to cite.

## Set up

Run three MCP servers side by side and give your agent all three:

```bash
# Fleet questions, with Mira's service map as observed calls
pacto mcp --fleet --k8s --mira-source http://mira:4318 \
  --dashboard-url https://pacto.example.com

# Delivery questions, over the cluster your kubeconfig points at
pacto mcp --delivery

# Mira's own tools, served by Mira at POST /mcp
#   http://mira:4318/mcp
```

The servers stay separate because each has its own authorization model. Fleet
answers can be restricted to what a caller may read, delivery reads run as the
caller against the cluster, and Mira has no authentication. Pacto reads only
Mira's service map, and only from the URL the operator passes with
`--mira-source`; no tool argument can point it anywhere else.

For the dashboard, set `PACTO_DASHBOARD_MIRA_SOURCES` (space-separated URLs) to
add Mira's service map to the fleet, and `--mira-url` to link a service page to
Mira.

## Questions and tools

| Question | Tool | What to cite |
|----------|------|--------------|
| When did the problem start? | Mira `list_alerts`, `query_metric`, `correlate`. The `frame.from` of a `correlate` answer is the start of the window you asked for; read the onset from alert times and metric points. | Alert times and metric points |
| Which signals indicate degradation? | Mira `query_metric`, `list_alerts`, `get_trace` | Series and exemplar trace ids |
| Which deployments or changes happened around then? | `pacto_delivery_history` with `service`, `since` and `until`; `pacto_delivery_explain` for verdict citations; `pacto_impact` with `include_observed=true` on a suspected change | Record ids, `applied`, `cause`, `completeness` and affected consumers |
| Which dependencies are involved? | `pacto_fleet_graph`, `pacto_fleet_neighborhood` with `views=differences`; Mira `service_map` | Edge `provenance`, `reconciliation` and `observedSources` |
| Which other services are affected? | `pacto_fleet_neighborhood` with `direction=dependents` and `depth=2` (views default to `expected,observed`); `pacto_fleet_graph` with `direction=dependents` and `transitive=true`; `pacto_fleet_get` for `uncontractedCallers`, the callers with no contract that the neighborhood does not list | Nodes, `uncontractedCallers` and `hiddenDependents`. A restricted viewer sees only a count of hidden services and callers. |
| Which teams own the affected components? | `pacto_fleet_neighborhood` owners, `pacto_fleet_entity` with `kind=owner` | Owner keys |
| What is the likely cause? | The agent or person concludes; Mira `render_rca` writes it up with citations | Every citation above |
| What evidence supports the conclusion? | Every answer above | `meta.asOf`, `snapshotId`, `sources`, `limitations`, delivery record ids and Mira queries with absolute times |

From the CLI, `pacto fleet neighborhood <service>` and
`pacto delivery history --all --service <service> --since 2h` answer the same
questions.

## Read the evidence

Unknown is never healthy. Treat these as limits on what an answer proves:

- A `partial` answer may be missing a change or an edge. A failed Mira read
  marks its source unavailable, and a truncated, empty or unattributable map
  marks it partial.
- Mira's map is sampled, covers only instrumented services and only the last
  15 minutes, so an edge it did not see may still exist. A declared dependency
  that Mira or any other source saw is `matched`; one Mira did not see stays
  `insufficient` unless a `--traces` export also observes calls.
- A Mira node behind `mira proxy` cannot serve the map. Mira keeps traces for
  7 days by default.
- Mira names carry no namespace. An observed edge between two services you can
  see counts every namespace's traffic under those service names, including
  namespaces you may not read.
- A restricted Pacto view hides every caller that has no namespace, whichever
  source saw it, and counts it in `hiddenDependents`.
- The dashboard shows "Open in Mira" only to viewers with nothing hidden,
  because Mira has no authentication. In Open mode the dashboard serves the
  whole fleet to everyone.

## Revisit and share

- With `--dashboard-url`, fleet answers carry an `href` to each entity's page,
  and `pacto_impact` consumers link to their service pages.
- A dashboard delivery history URL keeps its window, for example
  `#/delivery/history?service=payments&since=2026-11-05T09:00:00Z&until=2026-11-05T11:00:00Z`.
- `pacto fleet snapshot --output-format json > snapshot.json` keeps the whole
  graph as you saw it.
- Mira `render_rca` writes the incident report with its citations.
