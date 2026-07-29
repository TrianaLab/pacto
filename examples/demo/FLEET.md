# Operational graph demo (fleet)

This walkthrough shows how an agent — or a human — can understand an operational
system through Pacto alone, without reverse-engineering repositories, Helm charts,
Kubernetes resources or human documentation.

Everything below runs offline against the bundles in `examples/demo/bundles`
plus the ingested evidence in `examples/demo/fleet-evidence.yaml`. No cluster,
registry or running process is required. The evidence file models what a platform
would ingest from real environments (compliant, non-compliant, unknown and stale
targets) so every operational state is visible without fabricating a runtime fact.

## Setup

From the repository root:

```bash
BUNDLES=examples/demo/bundles
EVIDENCE=examples/demo/fleet-evidence.yaml
alias pfleet="go run ./cmd/pacto fleet --local $BUNDLES --evidence $EVIDENCE --freshness 24h"
```

## CLI walkthrough

```bash
# What is in the fleet? (15 services, with owners, revision and target counts)
pfleet search

# Who owns what, filtered
pfleet search --owner payments

# Which services depend on auth-service, transitively?
pfleet graph auth-service --direction dependents --transitive
#   api-gateway -> frontend -> pacto-demo

# What needs attention right now?
pfleet status
#   NON_COMPLIANT  orders-service target (confirmed drift)
#   UNKNOWN        auth-service target (insufficient evidence)
#   STALE_EVIDENCE fraud-service target (evidence older than 24h)

# Inspect a target's compliance and the finding behind it
pfleet get --target commerce/orders-service

# Why is this target Unknown? (deterministic, structured reasons)
pfleet explain production-eu/kubernetes-workload/identity/auth-service
#   [EVIDENCE_MISSING] no evidence has been observed for this target

# Machine-readable, with as-of time and completeness on every answer
pfleet search --output-format json | head
```

Add `--output-format json` to any command for a structured response whose `meta`
carries `asOf`, `completeness` and any `limitations`. Point `--evidence` at a
missing file to see partial completeness: the local revisions are still returned
and the missing source is reported as `unavailable`, never as empty.

## Agent walkthrough (MCP)

Start a read-only fleet MCP server over the same sources:

```bash
go run ./cmd/pacto mcp --fleet \
  --local examples/demo/bundles \
  --evidence examples/demo/fleet-evidence.yaml \
  --freshness 24h
```

It exposes five read-only tools — `pacto_fleet_search`, `pacto_fleet_get`,
`pacto_fleet_graph`, `pacto_fleet_status`, `pacto_fleet_explain` — alongside the
authoring tools. Point any MCP client (Claude Desktop, an IDE, a custom agent) at
it. These tools OBSERVE the operational system; they never modify contracts,
deploy, invoke live services or grant authorization.

### Scenario A — dependency impact and ownership

> Which services depend on auth-service? Who owns them? Which deployed targets are
> currently Unknown or NonCompliant, and what evidence is missing? Which operations
> and skills are available for the affected services?

An agent answers this entirely from structured Pacto data:

1. `pacto_fleet_graph { service: "auth-service", direction: "dependents", transitive: true }`
   -> `api-gateway`, `frontend`, `pacto-demo`.
2. `pacto_fleet_get { service: "api-gateway" }` -> owner, revisions, tools, skills.
3. `pacto_fleet_status { needs_attention: true }` -> the NonCompliant, Unknown and
   stale targets, each with its code.
4. `pacto_fleet_explain { subject: "production-eu/kubernetes-workload/identity/auth-service" }`
   -> `EVIDENCE_MISSING` — the auth-service target could not be observed, so its
   compliance is Unknown (uncertainty), not a confirmed violation.

### Scenario B — production readiness by team

> Show production services owned by the payments team that are not operationally
> ready and explain why.

1. `pacto_fleet_search { owner: "payments", not_ready: true }`.
2. `pacto_fleet_explain { subject: "<service>" }` for each hit.

Every answer includes `asOf` and `completeness`. A `partial` or stale answer is
incomplete knowledge: a missing result does not prove absence when source coverage
is incomplete. Pacto supplies the operational meaning; the agent decides what to
do with it, and an external policy or authorization system decides what it is
allowed to do.

## Dashboard

`pacto dashboard examples/demo/bundles` also serves the same operational graph at
`/api/fleet/snapshot`, `/api/fleet/services`, `/api/fleet/services/{name}/graph`
and `/api/fleet/status`, backed by the identical `pkg/fleet` read model.
