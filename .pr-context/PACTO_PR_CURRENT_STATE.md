# Pacto PR #291 — Current Implementation State

**Snapshot date:** 2026-08-12  
**Repository:** `TrianaLab/pacto`  
**PR:** `#291`  
**Branch:** `feat/operational-graph-fleet`

> Living PR-coordination document. It is intentionally committed on `feat/operational-graph-fleet` during development so ChatGPT and Claude share durable state. It is **not product/repository documentation** and MUST be deleted in Phase 14 before the PR is marked ready.

## 1. Current reviewed repository state

Latest independently reviewed HEAD:

`5f3d4ebb06c3b1559622c151556a4e537eeb28fe`

Current synchronized `main` / merge-base at that review:

`a56b69e375f1881d645d3b39f3366f23398e72cf`

PR state at review:

- open;
- draft;
- mergeable;
- no authorized history rewrite;
- no force-push authorization.

Commits reviewed on top of the previous reviewed HEAD `7da2ad46` — all four of
them:

- `780a207a` — persist the independently reviewed PR state at `7da2ad46`
- `e150a548` — the operator can mount an offline trace export as a named source
- `6250ebe3` — the kind scenario asserts the reconciliation half a live cluster
  can actually hold
- `5f3d4ebb` — record Phase 7 as a submitted candidate, not a closed phase

This section records the last INDEPENDENTLY REVIEWED state. It is not a Claude
self-assessment and must not be re-closed by the session that implements against
it.

### Handoff discipline — corrected

The handoff that accompanied `5f3d4ebb` enumerated only three commits after
`7da2ad46`; there were four. Omitting the coordination-state commit made the
appended range unverifiable from the handoff alone, which is the one thing the
enumeration exists for.

Every later handoff MUST enumerate EVERY commit between the reviewed starting
SHA and the final SHA of the iteration, including coordination-state and
generated-bundle commits. A commit that only touches `.pr-context/` is still a
commit an independent reviewer has to account for.

### Accepted at this review — do NOT reopen

The following are settled. Do not redesign them, and do not "improve" them as a
side effect of the narrow items in section 2. Reopening any of them requires a
NEW concrete counterexample.

**Ownership identity and discovery**

- `OwnerKey` and the exact-versus-fuzzy owner semantics;
- contacts-only ownership without a fabricated canonical identity;
- the shared `OwnershipFact` presentation across Service, Revision and
  Operational Target detail;
- Revision contact inspection;
- contact-order and exact-duplicate normalization (`Owner.ContactSet`);
- owner ranking namespace ambiguity;
- the ownership aggregate semantics.

**Distribution primitive**

- `DistributionBar` under-count behaviour;
- `DistributionBar` ordinary over-count behaviour;
- `DistributionBar` authoritative-zero behaviour.

**Product surfaces**

- the Operational Overview dashboard generally;
- Data Sources as a secondary Product surface (four primary tabs unchanged);
- the Data Sources Overview section and its `PageToc` integration;
- source chips as exact links;
- the Data Sources inventory;
- selected-source health versus Fleet knowledge as distinct concepts;
- the Source-detail Product hierarchy;
- raw source records versus contributed Product entities;
- `SourceContribution` complete-population semantics;
- `PageToc`;
- Services aggregates;
- Readiness;
- typography;
- progressive disclosure;
- Graph / Change-analysis geometry.

**Model**

- lock v3.

**Knowledge population**

- `ProductMeta.SourceCounts` is the authoritative complete population for
  source-health arithmetic; `ProductMeta.Sources` is a bounded named preview.
  The Product renders one answer for one snapshot, at every level (Data Sources
  tally, `KnowledgeBanner`, knowledge severity).

**Phases**

- Phase 1 through Phase 6 are closed.

## 2. Current phase status

### Phase 1 — COMPLETE

### Phase 2 — COMPLETE

### Phase 3 — COMPLETE

### Phase 4 — COMPLETE

### Phase 5 — COMPLETE

### Phase 6 — COMPLETE

### Phase 7 — NARROWLY REOPENED at `5f3d4ebb`

Target:

**operator-managed OFFLINE observation/trace-source configuration**

The offline pipeline (OTLP/JSON trace file, offline `pkg/otelobserver`, observed
edges, Fleet observation source, reconciliation) already exists and is accepted.
What is missing is a declarative, operator-managed way to package, configure and
mount those observation sources, with stable Data Source identity that does not
depend on list position. Phase 7 is that packaging — NOT an OTLP receiver, a
Collector, a trace database or any live ingestion.

The candidate submitted in `e150a548` + `6250ebe3` was reviewed and its DESIGN
was accepted. Three specific defects reopened the phase. Everything else about
Phase 7 stays closed and must not be redesigned without a new counterexample:
the offline analyzer boundary, the operator-managed observation-source concept,
PVC + ConfigMap as the only backings, read-only mounts, no `hostPath`,
deterministic sorting, reordering changing neither identity nor pod template,
complete removal, source failure as explicit Fleet/Product knowledge, source
health separate from observation freshness, the retained ad-hoc
`pacto dashboard --traces`, the focused Kind observation-packaging scenario
(which need NOT manufacture `relationship.reconciliation == "matched"`), and the
existing architecture gates.

#### Blocker A — a Data Source identity was only unique among observation sources

An observation source named `k8s` collides with the in-pod live Kubernetes
source, whose id falls back to `k8s` when the pod has no kubeconfig context.
`fleet.Build` emitted `DUPLICATE_SOURCE_ID` and continued, so the Product
published one Data Source key owned by two semantic sources and
`sourceDetail("k8s")` answered with whichever came first — one physical source
unaddressable.

Required invariant: **one Product Data Source key -> exactly one semantic Data
Source**, across the WHOLE configured Fleet namespace (live Kubernetes including
the `k8s` fallback, OCI, cache, local, evidence store, evidence HTTP,
target-state, ad-hoc positional observation ids, and every other enabled source
kind). Fail closed before publishing ambiguous Product identity; never silently
rename either source.

#### Blocker B — lexical path validation does not stop a symlink escape

A PVC containing
`/var/lib/pacto/observation/orders/traces.json -> /var/run/secrets/kubernetes.io/serviceaccount/token`
passed validation (`file: traces.json` is lexically innocent) and `os.ReadFile`
followed the symlink out of the mount.

Required invariant: **a user-controlled path or backing must never make Pacto
read outside its declared source root.** The mechanism must be a real rooted-open
semantic, not hand-written string canonicalization. The invariant is NOT "reject
every symlink": a projected Kubernetes ConfigMap volume is built out of internal
symlinks (`..data` -> `..<timestamp>`) and must keep working. The
framework-independent observation parser must not gain Kubernetes dependencies,
and the ad-hoc CLI must keep working.

#### Blocker C — the Helm values and the operator flag wire disagreed

`file: exports/trace,part.json` was legal under `values.schema.json`, but the
controller's comma-delimited `--dashboard-trace-source` value
(`name=orders,file=exports/trace,part.json,configMap=orders-traces`) made
`ParseObservationSource` read `part.json` as a malformed field.

Required invariant: **every value accepted by `values.schema.json` and Helm
rendering must be representable and parseable by the operator into the same
semantic `ObservationSource`.** Either make the wire injective/escaped, or
deliberately restrict the lexical space and reject the delimiter consistently in
Go validation, `values.schema.json`, the Helm tests and the docs — the simpler
durable contract, not the cleverest encoding. Separately, `existingClaim` and
`configMap` must be validated against the appropriate Kubernetes naming rule
before a Deployment is created, rather than accepting a value that can only fail
later at API admission.

#### Accepted scoped deviation — still intentional

The live Kind scenario asserts the observed edge under its declared identity and
that it names the same pair the operator reconciled as declared, but not the
snapshot's `reconciliation: "matched"` verdict, which needs a contract-REVISION
source the operator-managed dashboard does not have in that scenario (the live
Kubernetes source projects deployed targets, not revisions). That verdict over an
observation source stays proven hermetically in `internal/app` and by
`make demo-fleet`. The fully live declared+observed Product reconciliation is
Phase 8 work.

### Phase 8 — NOT STARTED

Upgrade the EXISTING live Kind vertical's browser leg from a deliberate smoke
check into representative live Product acceptance.

### Phase 9 — NOT STARTED

Real built MkDocs browser E2E.

### Phase 10 — NOT STARTED

Docker Desktop/containerd/local-registry Kind path.

### Phase 11 — NOT STARTED

MCP catalog core: bounded multi-root catalog semantics over arbitrary Pacto
contract roots. Distinct from the operational Fleet MCP tools this branch
already ships.

### Phase 12 — NOT STARTED

MCP catalog discovery server/CLI/E2E/docs.

### Phase 13 — NOT STARTED

Normative invariants finalization.

### Phase 14 — NOT STARTED

Finalization, final ontology audit, repository hygiene, PR body, exact SHA, readiness.

## 3. Branch-hygiene regression — CLOSED

The four local-agent tooling files (`.claude/CLAUDE.md`, `.codex/config.toml`,
`.mcp.json`, `AGENTS.md`) were untracked by the append-only commit `ef67f494`
and remain locally as untracked tooling. The incidental `go.work.sum` entries
were reverted by `fffd9a39`, and the 47MB tracked build output by `ccf2ae0e`.
No history was rewritten.

The rule stands for every later phase: local/agent tooling, caches, plans and
workstation paths must never become tracked files of this PR.

## 4. Accepted Product architecture

Do not reopen these decisions without a new concrete counterexample.

### Canonical entities

- Service
- Contract Revision
- Operational Target

They remain distinct.

### User-facing terminology

- `target` internal kind -> **Operational target**
- `source` user-facing -> **Data source**
- Collector remains a distinct narrower evidence-producer concept
- user-facing "Fleet" jargon removed where it assumes internal knowledge
- internal `pkg/fleet`, `/fleet/...` and CLI/internal identifiers may remain

### Primary Product navigation

- Overview
- Services
- Operational graph
- Change analysis

Secondary/contextual:

- Needs attention
- Owners
- Data sources
- Readiness through revision/attention context

### Readiness

Readiness is per Contract Revision.

Fleet-host standalone legacy Readiness is retired/canonicalized.

### Change analysis

Legacy Compare + Product Impact were consolidated.

One canonical Product workspace answers what changed and what it affects. Detailed semantic field diff is preserved.

### Product entity responsibility

**Service**
- logical identity + aggregate operational posture.

**Revision**
- contract inspector.

**Operational Target**
- runtime/evidence inspector.

**Data source**
- provenance inspector: what this source is, whether it is healthy and current,
  what it supplied and what is attributable to it.

## 5. Accepted information-parity work

Earlier Product migration accidentally lost significant V1 inspection capability.

Subsequent work restored the intended model.

### Revision inspector

Supported information is reachable for:

- interfaces;
- operations;
- configurations;
- policies;
- capabilities/tools;
- skills;
- dependencies;
- readiness;
- docs;
- SBOM/software inventory;
- validation;
- identity/provenance;
- declared owner contact metadata;
- revision history;
- Change analysis entry.

### Markdown

Product Revision can again read bundled Markdown.

Current accepted architecture:

- docs listed in Revision detail;
- body fetched lazily;
- canonical RevisionKey + path boundary;
- bounded resource read;
- Markdown rendered safely;
- Mermaid rendered;
- path traversal rejected;
- same-name different-domain isolation.

### Immutable document correction

- Build records SHA-256 fingerprint for listed document content;
- lazy read verifies bytes against fingerprint;
- changed/deleted/unreadable body becomes explicit unavailable;
- 512 KiB bound remains;
- source conflicts do not silently pick arbitrary bytes.

## 6. Accepted UI/interaction corrections

- Services search autocomplete: debounced, bounded, canonical identity,
  same-name domain disambiguation, keyboard/pointer, stale-response protection.
- Same-query stale-while-revalidate: first load / changed identity may load;
  same-query refresh retains last valid data; refresh failure retains stale data
  with an honest notice; a changed query never presents old rows as the answer.
- Scroll restoration: scroll belongs to a history entry, not only a URL; push
  starts fresh; Back/Forward restores the exact entry; hard reload restores the
  current entry; canonical replace preserves the entry; state is bounded; user
  scroll input can cancel pending restoration.
- Graph spatial state: same-query refresh preserves layout; reload restores
  spatial state; query identities isolate state; semantic data still refreshes;
  `Fit` and `Reset layout` are different.

## 7. Accepted visualization direction

Accepted principle:

> lists/tables for exact inspection; visualizations for system comprehension.

Any chart must answer a real question, use authoritative aggregate data, not
infer global truth from a page, preserve exact values, expose
uncertainty/completeness and remain accessible/mobile/light/dark.

## 8. Latest verification snapshot

Reviewed at exact HEAD `5f3d4ebb`.

Review threads at that SHA:

- 0 unresolved authored-product threads;
- the remaining unresolved `github-code-quality` threads are on GENERATED
  minified UI assets under `pkg/dashboard/ui/assets/`, not authored code.

Re-verify both populations at the exact final SHA of every later pass; the
generated asset path changes whenever the UI bundle is rebuilt.

Generated Mermaid/minified assets must not be hand-edited. After the FINAL
generated bundle of a pass is committed, review threads must be queried again
and reported as unresolved authored / unresolved generated / current versus
outdated, with the exact current generated path.

The Security workflow's own status is a different claim from CodeQL alert
attribution. Do not describe CodeQL alert provenance as independently
established without inspecting the underlying alert records and their base/head
evidence.

Counts in a handoff are not evidence.

Important process rule:

**Do not trust reported counts blindly in a later chat. Re-verify exact final SHA, CI and review threads before accepting the next handoff.**

## 9. Historical constraint

Authored-content U+00A7 gate is expected green.

Blocking enforcement over old branch commit history / PR metadata remains constrained because historical branch commit messages already contain the forbidden character.

No permission exists to rewrite shared history.

Do not rebase/filter-history/force-push to solve that unless Eduardo explicitly authorizes it.

## 10. Next iteration objective

Phase 7 remediation, and nothing else. The three blockers in section 2 are the
whole scope. The accepted Phase-7 design, and Phases 1 through 6, must not be
reopened or redesigned as a side effect.

The immediate next Claude session should:

1. make Data Source identity unique across the WHOLE configured Fleet source
   namespace, failing closed before a Product is published — the smallest
   coherent fix, not a redesign of `fleet.Build` duplicate-source semantics, and
   with one shared rule rather than a second reserved-name list in Helm and Go;
2. make an observation read rooted at its declared mount, using a real
   rooted-open semantic, without banning the internal symlinks a projected
   ConfigMap volume needs and without giving the offline parser Kubernetes
   dependencies;
3. make the chart's accepted lexical space and the operator's flag wire agree,
   proved by a test that actually parses the RENDERED argument rather than by
   separate tests that each re-encode the grammar, and validate backing names
   before a Deployment is created;
4. keep every accepted Phase-7 behaviour intact, including the scoped Kind
   reconciliation deviation;
5. not begin Phase 8.

## 11. Final-phase requirements already agreed

When the whole plan reaches the final phase, two deep audits are mandatory.

### Final ontology audit

Very thoroughly compare:

**intended ontology <-> code/types/behavior <-> docs/API/UI**

Must validate all first-class concepts, identities, relationships, epistemic states, absence semantics, provenance and aggregation semantics.

Do not declare merge-ready if ambiguity could produce a false operational claim.

### Final repository hygiene audit

Review complete `base...HEAD`.

Classify every added file.

Remove implementation-only artifacts such as internal plans, ChatGPT/Claude instructions, handoffs, temporary ledgers, review screenshots, traces/reports, one-off scripts, caches/logs, orphan fixtures, accidental generated output and local config/paths/secrets.

The three PR coordination documents are temporary branch state and MUST be deleted in Phase 14 before merge readiness.
