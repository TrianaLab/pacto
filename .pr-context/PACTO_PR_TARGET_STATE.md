# Pacto PR #291 — Target State

**Purpose:** Stable description of the state we want PR #291 to reach before it is marked ready for review/merge.

**Repository:** `TrianaLab/pacto`  
**PR:** `#291`  
**Branch:** `feat/operational-graph-fleet`

> Temporary PR-coordination document. It is intentionally committed on `feat/operational-graph-fleet` while the PR is being developed so both ChatGPT and Claude share durable state. It is **not product/repository documentation** and MUST be deleted in Phase 14 before the PR is marked ready.

## 1. Product thesis

Pacto should model an operational system as a versioned, navigable and verifiable graph of declared intent, resolved identities, runtime observations and evidence.

The final product must let humans, automation and agents answer:

- what services exist;
- which immutable contract revisions exist;
- where those services/revisions are observed operationally;
- what each revision declares;
- what runtime evidence says;
- where declared and observed reality agree or differ;
- how services/revisions/targets relate;
- what changed between two revisions;
- what that change may affect;
- how complete, stale or uncertain Pacto's knowledge is.

Pacto remains a read-only declarative/discovery/evaluation system at this boundary.

Pacto is not a deployment engine, authorization system, observability backend, generic runtime controller, IDP or automatic remediation engine.

External systems may deploy, authorize, instrument or remediate. Pacto declares, resolves, observes through supplied evidence, evaluates, graphs, diffs and explains.

## 2. Canonical ontology

The final implementation and documentation must describe exactly one coherent ontology.

### Service

A logical software/service identity.

A Service is not a contract revision, runtime instance, Kubernetes Deployment or operational health result.

Identity is domain-qualified through `ServiceKey`.

### Contract Revision

An immutable version/content identity of a service contract.

A Contract Revision belongs to one Service, has canonical immutable identity through `RevisionKey`, contains/addresses authored contract material, may be retrievable or non-retrievable independently from identity matching, and owns revision-level readiness semantics.

A Contract Revision is not the running workload, the Service itself or runtime compliance.

### Operational Target

A concrete operational subject for which Pacto has runtime/operational state or evidence.

Internal model names may remain `Target`, `TargetKey`, `EntityKind=target`.

User-facing term: **Operational target**.

An Operational Target is not automatically a Kubernetes Deployment and does not imply that Pacto deployed it.

### Evidence / EvidenceSet

Observed or reported facts from an operational environment.

Evidence has provenance and epistemic limits. It does not automatically imply a finding or policy conclusion.

### Finding

A specific evaluation result/problem derived from evaluating facts against declared intent or other rules.

Evidence != Finding.

### Readiness

A property/assessment of a **Contract Revision** describing declared/assessed preparedness.

Readiness != Compliance.  
Readiness != Kubernetes readiness.  
Readiness != Service health.

### Compliance

An evaluation of runtime/evidence facts against the relevant contract intent.

The final state model and denominator must be documented once and implemented consistently.

Important distinctions include Compliant, Warning, NonCompliant, Unknown, Invalid, NotEvaluated and Reference.

### Data Source

The broad Fleet ingestion/provenance concept.

Examples may include local bundles, OCI, cache, Kubernetes/runtime sources, ingested evidence and observation inputs.

User-facing term: **Data source**.

### Collector

An observer/adapter that produces Evidence/EvidenceSet.

A Collector may contribute through a Data Source path, but Source != Collector. Not every Data Source is a Collector.

### Declared relationship

A relationship coming from authored/resolved contract intent.

### Observed relationship

A relationship supported by runtime observation/evidence.

Declared != Observed.

### Reconciliation

An explicit backend interpretation of declared vs observed relationship knowledge.

Absence of observation must never be interpreted as proof that runtime traffic does not exist.

### Reference resolution

Configuration, policy and dependency references may become canonical Product relationships only when destination identity is supported by authoritative resolution evidence.

Never infer canonical identity from display names, OCI repository basename, config/policy entry name or arbitrary same-domain service match.

Unknown/unresolved is preferable to a plausible false link.

### Change analysis

One canonical workflow combining:

1. **what changed** — semantic field-level contract diff;
2. **what it may affect** — operational blast radius.

User-facing workspace: **Change analysis**.

Primary CTA may say **Compare revisions**.

## 3. Core epistemic invariants

Release-level invariants:

- Service != Contract Revision != Operational Target.
- Declared != Observed.
- Evidence != Finding.
- Readiness != Compliance.
- Data Source != Collector.
- Requested ref != resolved identity.
- Exact target-to-revision match != content retrievability.
- Unknown != empty.
- Unknown != NotEvaluated.
- Partial != empty.
- Partial != complete.
- Stale != unavailable.
- Ambiguous != unresolved.
- Relationship existence != relationship observation.
- Declared-not-observed != confirmed absence.
- Observed-only != invalid by definition.
- Absence of evidence != evidence of absence.
- Product discovery != authorization.
- Product discovery != execution.
- Contract intent != runtime truth.
- Frontend presentation may simplify presentation, never meaning.
- Canonical identity must not be reconstructed heuristically in the frontend.
- Product queries are bounded and honest about truncation/completeness.
- Mutable requested refs must resolve to immutable identities before being treated as exact content identity.

## 4. Identity model

### Service identity

Domain-qualified end to end. Same service names in different domains never cross-contaminate.

### Revision identity

Immutable. Content identity and requested/resolved reference semantics explicit. The same `RevisionKey` must never silently identify different content.

Lazy resources such as Markdown documents must obey the same immutable revision semantics.

### Target identity

Concrete scope/kind/name operational identity. Never flattened into Service or Revision identity.

### Reference occurrence identity

A lock/reference entry must be attributable to the **specific declaration occurrence that produced it**.

`kind + name` alone is insufficient when the lock contains a transitive reference closure.

Local relative references must include enough origin/base context so identical relative strings from different directories cannot collapse incorrectly.

### Evidence identity

Producer, sequence, subject/target and digest semantics remain explicit and deterministic.

## 5. Final Product information architecture

### Primary navigation

- Overview
- Services
- Operational graph
- Change analysis

### Secondary/contextual destinations

- Needs attention
- Owners
- Data sources
- Readiness through revision/attention context

There must be no visible legacy Fleet-host Readiness or Compare island.

Legacy routes may canonicalize to Product routes for compatibility.

### Overview

Answers: **What is happening and what deserves attention?**

It should combine concise metrics, useful visual summaries and drill-downs without becoming a wall of charts.

### Service

Answers: **What is this logical service and what is its operational posture?**

Prioritize owner/domain, target posture, revision adoption/identity, important drift/attention, dependencies/dependents, evidence/finding summaries and useful aggregate visualization.

Service must not become a contract dump.

### Contract Revision

Canonical **Contract Inspector**.

All supported contract information must remain reachable, including where applicable:

- interfaces and operations;
- configuration;
- policies;
- capabilities/tools;
- skills;
- dependencies;
- readiness;
- documentation;
- SBOM/software inventory;
- validation;
- identity/provenance;
- revision history;
- entry to Change analysis.

The page must be scannable through progressive disclosure instead of showing every detail simultaneously.

### Operational Target

Canonical runtime/evidence inspector.

Immediately communicate service, target identity, linked revision and match certainty, content retrievability where relevant, compliance, evidence freshness, important findings, supported observed runtime facts and observed relationships/drift.

### Operational Graph

A real Cytoscape topology.

Must preserve Service / Revision / Operational Target perspectives, declared / observed / difference/reconciliation semantics, bounded neighborhood queries, truthful disabled states, canonical identity, selection/detail and graph spatial state.

Graph state requirements:

- same-query refresh preserves existing layout/viewport;
- changed topology preserves existing nodes as far as practical;
- browser reload restores same-query spatial state;
- graph-query state does not leak across unrelated graphs;
- `Fit` != `Reset layout`;
- semantic data may refresh without resetting positions.

### Change analysis

One coherent flow:

- choose two canonical revisions;
- detailed semantic diff;
- classify changes;
- operational blast radius;
- affected consumers;
- owners;
- targets;
- confidence/limitations;
- graph transition when useful.

## 6. Presentation system target

The final UI must preserve rich information while remaining easy to scan.

### Visual roles

The design system should define stable visual roles such as Page title, Section title, Subsection title, Body, Secondary body, Label, Meta/caption, Metric and Code.

Semantic HTML heading level and visual role are separate concepts.

A section must not become visually larger/smaller merely because accessibility required `h2` vs `h3`.

### Progressive disclosure

Information should be layered:

**Primary:** necessary to understand the page now.

**Secondary / inspection:** details opened intentionally.

**Diagnostic:** exact identity, provenance, raw refs, limitations and debug information.

No information may be removed merely to make the UI look cleaner.

No critical state may be hidden only behind hover.

Use disclosures for substantive detail, accessible tooltip/popover for short definitions, and drawer/drill-down for complex inspection.

### Consistency

Same conceptual role => same typography, spacing and interaction grammar throughout the Product.

Zero undeclared global design tokens.

Nesting should be understandable from typography, whitespace and grouping before the user reads the words.

## 7. Visualization target

Lists/tables are for precise inspection. Charts/visual summaries are for pattern recognition.

The Product should retain useful visual intelligence without recreating V1 dashboards mechanically.

Visualizations must answer a real question faster than reading rows, use backend-authoritative aggregates, not pretend a paginated preview is the full population, show exact values/text equivalent, work light/dark and mobile, not rely only on color, and have meaningful drill-down where interactive.

Candidate areas include target compliance posture, revision adoption, revision-link certainty, evidence freshness, findings/severity, source health, relationship differences and change impact.

## 8. Product API target

Product UI must consume canonical bounded Product APIs.

Requirements:

- no raw `FleetSnapshot` consumption in Product UI;
- no legacy name-based API fallback;
- no handwritten wire DTO mirrors where generated SDK is authoritative;
- canonical identities in routes/DTOs;
- bounded responses;
- explicit truncation/completeness;
- backend owns semantic facts;
- frontend must not guess reconciliation, identity, source completeness or operational truth.

Large content such as Markdown/SBOM/OpenAPI material should use bounded/lazy single-resource access rather than inflating normal Entity Detail.

## 9. Browser/product acceptance target

The built WASM product must be exercised in real Chromium.

Acceptance should cover novice journeys, rich Service/Revision/Target inspection, Markdown and Mermaid, reference navigation, search/autocomplete, same-name multi-domain identity, partial/stale/unavailable knowledge, ambiguous/unresolved identities, changed-query vs same-query SWR, scroll preservation and Back/Forward history, graph renderer, graph spatial persistence, graph semantic refresh, Change analysis, charts, keyboard, responsive 320/375px, light/dark, axe with contrast, rich/adversarial datasets, boundedness and reasonable rendering behavior.

Required existing capability must not be hidden behind `test.fixme`.

## 10. Remaining whole-program phases

Canonical sequence:

### Phase 1 — foundations
Expected complete before later work.

### Phase 2 — Product API / identity foundation
Expected complete before later work.

### Phase 3 — Product entity/detail migration and information parity
Must be complete with authoritative reference occurrence identity.

### Phase 4 — Operational Graph
Must be complete with real topology and accepted semantics.

### Phase 5 — responsive/accessibility/product presentation
Includes final design-system consistency, hierarchy and progressive disclosure.

### Phase 6 — WASM/browser/product acceptance
Includes rich/adversarial/stress acceptance of the finished Product UI.

### Phase 7 — operator-managed observed/trace-source packaging/config
Make observed/trace input an operationally managed capability rather than only an ad-hoc local file path, without turning Pacto into an observability backend.

### Phase 8 — live Kind product vertical
Prove the complete real Product journey against live operator/Evidence Server/runtime data.

### Phase 9 — real MkDocs browser E2E
Test the actual built MkDocs site in a browser, including relevant diagrams, not only docs rendered inside the dashboard.

### Phase 10 — Docker Desktop/local-registry Kind
Close the local Kind path where Docker Desktop/containerd image-store behavior differs from CI/classic `kind load`.

### Phase 11 — MCP catalog core
Implement bounded multi-root catalog semantics over arbitrary Pacto contract roots.

### Phase 12 — MCP discovery server/CLI/E2E/docs
Expose the catalog through a small read-only discovery interface.

### Phase 13 — normative invariants
Turn important model distinctions into durable executable/documented invariants.

### Phase 14 — finalization / merge readiness
Includes docs, final screenshots where useful, exact-SHA CI, review-thread closure, PR body rewrite, generated artifact verification, final ontology audit, final repository hygiene audit, final clean diff review, and marking ready only after all gates pass.

## 11. MCP target

MCP remains deferred until its phases.

No Pacto config file or IDP-specific model should be introduced merely for MCP.

Intended usage:

```bash
pacto mcp \
  --root oci://ghcr.io/acme/platform/root@sha256:... \
  --root ./experimental-platform
```

Core thesis:

> Any set of Pacto contract roots plus their dependency closure becomes a bounded, discoverable, machine-readable catalog.

Required semantics:

- resolve/index root closure;
- root/direct/transitive provenance;
- preserve all paths;
- canonical identity;
- requested/resolved refs;
- exact immutable digest;
- roots/direct rank above transitives;
- canonical dedupe without losing paths;
- conflicts visible;
- unresolved dependencies visible;
- completeness may be partial;
- cycle-safe;
- hard bounds;
- mutable tags resolve once at startup;
- session catalog immutable;
- catalog metadata such as catalogId/generatedAt/completeness/limitations;
- discovery != auth;
- discovery != execution.

Explicitly excluded: proxy/execution, authorization, IDP adapters, registry crawling, vector search, marketplace, new persistent config model and dynamic activation.

## 12. Final ontology audit — blocking

Before merge readiness, perform a repository-wide ontology audit.

It must triangulate:

**intended model <-> implementation/types/behavior <-> public documentation**

If two agree and the third differs, there is a bug.

At minimum audit Service, Contract Revision, Operational Target, Evidence, Finding, Readiness, Compliance, Data Source, Collector, declared/observed/reconciled relationships, completeness/freshness, target-revision match certainty, retrievability, reference resolution, identity and change impact.

Audit code names, type system, DTOs, frontend labels/derivations, CLI, operator integration, examples, diagrams and docs.

Important adversarial scenarios must have unambiguous answers, including same-name services across domains, exact identity but non-retrievable content, inferred/ambiguous/unresolved target revision, no evidence, stale evidence, partial source, declared-not-observed, observed-not-declared, unresolved caller, config/policy repo name != service.name, transitive same-name reference occurrences, local same-relative-ref from different directories, all-Unknown evaluation, readiness passing while runtime non-compliant, multiple revisions active simultaneously and mutable OCI tag resolved to immutable digest.

If an ambiguity can cause a false operational claim, the PR is not merge-ready.

A concise durable Concepts/Ontology page may be added to public docs if it has long-term contributor/user value. Do not commit a transient audit scratchpad.

## 13. Final repository hygiene audit — blocking

Before merge readiness, review the complete `base...HEAD` diff.

Do not rely only on `git status`.

Every added file must have a durable post-merge purpose and be classified as production source, permanent test, intentional fixture, intentionally tracked generated source, public/durable docs, permanent developer tooling or packaging/release asset.

Remove anything existing only because the PR was implemented/reviewed, including implementation plans, agent instructions, handoff notes, temporary ledgers, scratch architecture docs, screenshots used only for review, Playwright traces/videos/reports, logs, coverage/profiling output, temporary JSON/YAML, one-off scripts, caches, local environment files, editor/OS metadata, temporary keys/certs, local registry/Kind artifacts, orphan fixtures, backups and accidental local paths/secrets.

Audit generated files: repository intentionally tracks them, normal generator reproduces them, drift gate expects them.

Audit changed docs: durable user/contributor value => keep; transient implementation coordination => remove or convert into durable docs.

The final repository should make sense to a contributor who knows nothing about how the PR was built.

**The three PR coordination files are intentionally temporary branch artifacts. They MUST be deleted in Phase 14 before merge readiness and must not exist in the final `main` diff.**

## 14. Release discipline

Until the whole program is complete:

- PR remains draft.
- No history rewrite.
- No shared-history rebase.
- No amend of published commits.
- No force-push unless Eduardo explicitly authorizes it.
- No broad scope reduction because a difficult requirement is inconvenient.
- Do not claim a phase complete when an accepted counterexample remains.
- Exact final SHA must be verified in GitHub Actions.
- Review threads must be inspected at the exact final SHA.
- Generated/minified assets must not be hand-edited.
- Authored-content U+00A7 gate must remain green.
- Historical commit-message/PR-metadata U+00A7 enforcement remains blocked unless explicit history-rewrite authorization is later granted.

## 15. Definition of final success

PR #291 reaches the target state when:

1. Ontology is coherent and identical in behavior, types, API, UI and docs.
2. Product identity is canonical and non-heuristic.
3. Product UI is rich but scannable and visually consistent.
4. No supported V1 capability was accidentally lost unless deliberately removed by the Pacto v2 model.
5. Operational Graph is truthful, usable and spatially stable.
6. Product APIs are bounded and epistemically honest.
7. Rich/adversarial browser acceptance is green.
8. Runtime/operator/live-Kind phases are complete.
9. MCP catalog/discovery phases are complete within their intended boundary.
10. Normative invariants are durable.
11. Public docs accurately describe the final product.
12. Repository hygiene audit finds no accidental implementation artifacts.
13. Full ontology audit finds no dangerous ambiguity or contradiction.
14. Exact final-SHA CI is green.
15. The PR body accurately describes what actually ships.
16. Only then is the PR moved from draft to ready.
