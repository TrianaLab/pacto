# Impact on the other surfaces

The same [impact analysis](impact.md) the CLI runs, projected through the MCP
tool and the dashboard. Given the same snapshot all three return the identical
answer; what differs is where the snapshot may come from.

## MCP tool: `pacto_impact`

The same analysis is exposed to agents as the read-only `pacto_impact` MCP tool.
It belongs to the **fleet query** family — see
[MCP integration](mcp-integration.md#three-tool-families-and-their-boundaries) —
and shares that family's boundaries: it projects the operational graph, observes
nothing, changes nothing and authorizes nothing. An agent uses `pacto_impact` to
*understand* a proposed change's blast radius before recommending a review, never
to act on it. Every answer carries `asOf`, `completeness` and `limitations`, so an
agent can tell how much of the system the answer actually covers.

It is the one fleet tool that does not serve a frozen snapshot: it resolves its
two refs and rebuilds the graph on every call, so its `asOf` advances while the
`pacto_fleet_*` tools' stays at the value they were started with. When the two
disagree they are describing two moments, not two systems — see
[what a session freezes](mcp-agent-capabilities.md#what-a-session-freezes-and-what-it-does-not).

---

## Dashboard: Change analysis

In the dashboard this analysis is one half of the **Change analysis** workspace,
served by the `/api/fleet/impact` endpoint and returning the same result model the
CLI and MCP tool produce. The workspace answers both halves of a single question
on one screen: *what changed* between two revisions of a service, and *what that
change affects*.

Change analysis is contextual: it is entered from the service or revision you are
already looking at (the **Compare revisions** action), the revision selectors are
populated from that service's known revisions, and the analyzed pair is in the URL
so the answer itself is shareable. It analyzes the **currently published**
snapshot — the same one the Operational Graph shows — so the answer's `snapshotId`
matches the graph, never a divergent rebuild. Breaking and potentially-breaking
changes are shown separately, and each consumer carries its path to the changed
service, compatibility range and verdict, and confidence with an explanation.

Because observed evidence must have a real source, the dashboard's
**include-observed** control is enabled only when the host declares an observation
source (reported by `GET /api/capabilities`); otherwise it is disabled — the
dashboard never ships a control that would have no effect.
