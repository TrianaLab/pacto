---
"@pacto/core": minor
---

Add the Pacto operational graph: what is declared, what is actually running and how the two differ.

Pacto could describe a contract. It could not describe a fleet. This release adds
the read model for that, and the surfaces on top of it.

`pkg/fleet` composes many contracts, contract revisions and operational targets
into an immutable, deterministic `FleetSnapshot` with a pure, network-free
`Query` over it. It keeps three identities distinct — the logical service, the
contract revision and the operational target — and it keeps them
domain-qualified, so two teams may own a `checkout` without becoming one node. It
makes incompleteness explicit: every snapshot and every answer carries an as-of
time, a completeness and structured limitations, so an unreachable source is
reported as an `unavailable` source that turns the answer's `completeness` into
`partial` — surfacing in the dashboard as `unavailable` knowledge, taken from the
worst source health — and never as an authoritative empty graph. `unknown` stays
a distinct state, for when there is no completeness envelope at all.

Around that read model:

- **Evidence reporting.** `pacto evidence serve` accepts signed evidence sets
  from environments Pacto cannot reach, verifies the producer signature, checks
  the report against the resolved contract revision and records the result. An
  environment that stops reporting goes stale rather than disappearing.
  `pkg/evidenceenvelope` is the signed wire format and `pkg/evidenceingest` the
  accept pipeline.
- **Evidence lives in the registry.** An accepted record is stored as an OCI 1.1
  referrer of the exact contract digest it is about, so the registry that already
  holds the contract is the only durable evidence system. No bucket, no database,
  no second persistence path.
- **Contract catalog.** `pkg/catalog` answers what a set of contract roots and
  their closure contain, bounded and free of any delivery mechanism. It reaches
  agents over MCP as exactly two fixed read-only resources, `pacto://catalog` and
  `pacto://catalog/closure`, plus one tool, `pacto_catalog_revision` — and no
  resource templates, because a revision identity is four structured fields and a
  URI template would force the ad hoc encoding that identity discipline exists to
  prevent. The session is frozen, so a catalog answer cannot change underneath a
  conversation.
- **Change impact.** `pkg/impact` and `pacto impact` answer who is affected by a
  change, computed over canonical identities and refusing a mutable reference.
- **Reconciliation and observation.** `pkg/reconcile` compares the declared graph
  with the observed one; `pkg/otelobserver` reads an OpenTelemetry span export to
  discover calls nobody declared.
- **CLI.** New `pacto fleet` (with `fleet reconcile`), `pacto evidence`,
  `pacto impact` and `pacto otel` command groups, all backed by the same read
  model.
- **Dashboard.** A product-shaped interface over the graph: services, revisions,
  targets, owners and sources as first-class pages with canonical links between
  them, an attention view that ranks what is actually wrong, and a graph view
  that stays readable at fleet size. The wire contract is generated from OpenAPI
  end to end, so the frontend cannot invent semantics the backend does not have.

Everything reports what it does not know. Evidence that is absent, stale, partial
or unreadable is reported as such and is never rendered as a passing result.

Backwards compatible: no existing flag, API or JSON shape changes.
