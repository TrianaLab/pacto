# The fleet map

The dashboard draws every service on one map, with its problems on it,
whether the fleet has 40 services or 10000. The Overview leads with the map
and the attention list beside it. The **Operational graph** page is the same
map on the whole page, with the attention list folded away. `pacto fleet map`
and the MCP tool `pacto_fleet_map` give the same answer as text and JSON.

![The Operational graph page for the demo: the toolbar groups by Domain, Owner team or Environment and shows Both, Expected or Observed; the map draws the commerce, fulfillment and payments domains with their error and warning counts, the external systems and the services with no domain](assets/screenshots/dashboard-fleet-map-light.png#only-light)
![The Operational graph page for the demo: the toolbar groups by Domain, Owner team or Environment and shows Both, Expected or Observed; the map draws the commerce, fulfillment and payments domains with their error and warning counts, the external systems and the services with no domain](assets/screenshots/dashboard-fleet-map-dark.png#only-dark)

## Zoom levels

The map zooms by how much room each service has, through four levels:

| Level | What it draws |
|-------|---------------|
| **Groups** | One circle per group with its service count, its errors and warnings and the calls between groups. The server computes these figures, so the browser downloads no service to draw them |
| **Services** | One dot per service. The browser fetches the services of the groups in view, a page at a time |
| **Names** | Each service's name and version, where the names fit without overlapping |
| **Details** | A card per service with its version, owner, interfaces, what Pacto knows of it and its worst attention item |

Services stay neutral; colour marks only what needs attention, and every state
also has a glyph, a line style or words in the legend. The ladder in the
corner names the level and jumps to another.

## Group by and show

**Group by** places services by business domain (the default), owner team or
environment. Past about 40 teams, each domain is a group and its teams sit
inside it. In environment grouping a service appears once in each environment
it runs in, with the version it runs there, ordered by the Stages you may read, and
temporary environments fold into one **Previews** group until you zoom in.
The **Environment** picker dims what does not run in that environment.

**Show** picks the lens: **Both** draws what the contracts declare and what
Pacto observes, **Expected** only what the contracts declare and **Observed**
only what Pacto saw running. Inferred services read as observed, so Expected
hides them. Switching the lens never moves a service. **Only problems**
keeps errors and warnings and greys the rest.

## Focus on one service

**Find a service**, a click and Enter, or an item in the attention list
focuses one service: its callers sit on the left, what it calls on the right,
at most nine per column and worst first. Each call is labelled with the
interface and the version range it accepts; a call with a problem adds why,
such as the operation a declared break removes. **One more hop** adds the
next column on each side, and **Back to whole fleet** or Escape returns to
the map you left.

![The map focused on orders-service: api-gateway calls it and frontend calls api-gateway on the left; on the right it calls notification-service, payments-service and postgresql, which call fraud-service, email-provider and stripe-api](assets/screenshots/dashboard-fleet-map-focus-light.png#only-light)
![The map focused on orders-service: api-gateway calls it and frontend calls api-gateway on the left; on the right it calls notification-service, payments-service and postgresql, which call fraud-service, email-provider and stripe-api](assets/screenshots/dashboard-fleet-map-focus-dark.png#only-dark)

## The attention list and links

The list beside the map is what needs attention, grouped by severity. An item
about a service focuses it on the map; any other item opens its page.
`#/fleet/attention` links, with or without filters, open the Overview with
the list filtered and a **Filtered** line that clears the filter. The
sidebar's attention count sits on **Overview**.

The map keeps its state in the address (grouping, lens, mode, environment,
focus and hops), so a link opens the same view and the browser's back button
restores the last one. `#/fleet/graph/service/<key>` opens the full map in
focus on that service.

## Keyboard

| Key | Action |
|-----|--------|
| `+` and `-` | Zoom in and out |
| Arrow keys | Pan |
| `0` | Fit everything |
| `/` | Find a service |
| Enter | Open the selected service |
| Escape | Leave focus, then clear the selection |

The canvas announces the level, the group in view and the focused service.

## The headline figures

Two figures sit above the map and on the Metrics page, each with its weekly
trend when [run records](measure-what-pacto-does.md#match-and-completeness-trends)
are on. **Declared ↔ observed match** is how much of what Pacto can observe
matches what the contracts declare, with the observed coverage beside it.
**Contract completeness** is how much each service's contract declares, with
the sections most often missing. With no observation source the match reads
"Pacto observes nothing yet", never 100%.
[Operational graph tallies](operational-graph-tallies.md#declared-and-observed-match)
defines both.

## Large fleets

Past 300 groups the smallest fold into one **Other** group that opens when you
zoom in, and past 2000 calls between groups the smallest are left out; the
answer says so in its limitations. The [demo](examples/dashboard-demo.md)
opens a generated fleet of 400, 3000 or 10000 services with `?fleet=N`.

## From a terminal or an agent

`pacto fleet map` prints the same figures and one row per group. Run on the
demo fixture, abridged:

```console
$ pacto fleet map --local examples/demo/bundles --target-state examples/demo/fleet-targets.yaml
Declared ↔ observed match: 6% of what Pacto can observe · 34% observed coverage · unknown never counts as matched
  services: 2 matched · 29 expected only · 0 observed only · 9 insufficient
  dependencies: 0 matched · 0 expected only · 0 observed only · 51 insufficient
Contract completeness: 47% · 0 of 40 judged services fully described · most missing: domain 35, readiness 33, summary 32
Grouped by domain: 5 groups · services 40 (0 inferred) · external systems 4
GROUP        KIND      SERVICES  INFERRED  EXTERNAL  MATCH  COMPLETE  ERRORS  WARNINGS  WORST
commerce     domain    3         0         0         0%     66%       4       7         non-compliant 1, lifecycle 3, health 1
payments     domain    2         0         0         -      59%       1       2         lifecycle 1, stale 2, readiness 4
```

`--group <key>` pages one group's services and `--focus <service>` lists its
callers and callees; `pacto_fleet_map` takes `group` and `focus` too. The CLI
and MCP read no Stages, so they list environments by name and say so.

## What's next

- [Operational graph](operational-graph.md): the read model the map draws
- [Operational graph tallies](operational-graph-tallies.md): match,
  coverage and contract completeness, field by field
- [Fleet views](fleet-tools.md): the same questions from `pacto fleet` and MCP
