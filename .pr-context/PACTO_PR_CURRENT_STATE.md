# Pacto PR #291 — Current Implementation State

**Snapshot date:** 2026-08-10  
**Repository:** `TrianaLab/pacto`  
**PR:** `#291`  
**Branch:** `feat/operational-graph-fleet`

> Living PR-coordination document. It is intentionally committed on `feat/operational-graph-fleet` during development so ChatGPT and Claude share durable state. It is **not product/repository documentation** and MUST be deleted in Phase 14 before the PR is marked ready.

## 1. Current reviewed repository state

Latest independently reviewed HEAD:

`759845ca2236bfadbccf88b6156d45feee9cb0b9`

Current synchronized `main` / merge-base at that review:

`a56b69e375f1881d645d3b39f3366f23398e72cf`

PR state at review:

- open;
- draft;
- mergeable;
- no authorized history rewrite;
- no force-push authorization.

Latest Claude session reported these commits on top of `13810112`:

- `fae36914` — immutable revision document bytes + authoritative reference destination work
- `874946ec` — query-aware retained rows + history-entry scroll
- `2fad1684` — demo lock correction
- `5072ba64` — rebuilt dashboard bundle
- `759845ca` — ledger/invariant documentation

Note: that handoff said "4 commits" but listed 5. Treat handoff arithmetic as non-authoritative.

## 2. Current phase status

### Phase 1 — COMPLETE

No known reopen item.

### Phase 2 — COMPLETE

Accepted identity/Product API foundations remain intact.

### Phase 3 — NARROWLY REOPENED

Current blocker:

**authoritative reference occurrence identity**

The latest resolver correctly removed:

- OCI repository basename -> ServiceKey inference;
- `ReferenceRef.Name` -> ServiceKey inference.

However, independent review found a deeper ambiguity:

`pacto.lock` contains a transitive config/policy reference closure, while `Lock.Reference(kind, name)` returns the first matching kind/name without identifying which contract/revision occurrence declared it.

Counterexample:

```text
root app
  config foo -> child-a
  config settings -> bundle-y

child-a
  config settings -> bundle-x
```

The lock can contain two `config/settings` entries. Resolving root `settings` through `Lock.Reference("config","settings")` can select the child's digest.

The digest is authoritative for the wrong occurrence.

Required correction:

- identify a lock reference by the specific declaring occurrence/origin;
- do not use kind+name alone;
- do not use slice/sort order;
- audit local relative refs because identical `./config` strings under different parent directories may resolve to different resources;
- legacy lock behavior must degrade honestly if occurrence identity cannot be proven;
- preserve deterministic lock generation and cycle safety.

### Phase 4 — COMPLETE

Accepted.

Operational Graph includes real Cytoscape rendering and previously reviewed corrections for projection semantics, target/revision/service identity, declared/observed distinction, graph controls, Product graph route, visual browser gate, graph state persistence and semantic refresh without full layout reset.

Do not reopen absent a concrete counterexample.

### Phase 5 — NARROWLY REOPENED

Current user-observed/systemic presentation problems:

1. typography hierarchy is inconsistent;
2. font sizes sometimes make nested sections look like peers or outrank parents;
3. too much explanatory/detail text is simultaneously visible;
4. Product needs progressive disclosure without losing information;
5. overall UI must use one consistent presentation grammar.

Independent code review found concrete design-system defects:

- global tokens define `--text-xs`, `--text-sm`, `--text-base`, `--text-lg`, `--text-xl`;
- Product components use undeclared `--text-md`;
- `PreviewSection` tries to make h2/h3/h4/h5 share one visual size through `var(--text-md)`;
- because the token is undeclared, the declaration is invalid and browser falls back to tag-level heading rules;
- therefore the same visual component can render at different sizes solely because semantic heading level differs;
- `--radius-md` is also used but absent from the inspected global token set;
- `--c-accent-border` was also observed in Product CSS without being part of the inspected global palette;
- entity pages have a visually-hidden semantic h1 while the visible `EntityIdentity` label is approximately body-size, allowing section headings to visually dominate the actual page title.

Required correction:

- complete global CSS custom-property audit;
- zero undeclared global design tokens;
- semantic visual typography roles independent of HTML heading level;
- visible page title on every Product entity route;
- consistent section/subsection/body hierarchy;
- consistent spacing/nesting grammar;
- one shared disclosure/help system;
- progressive disclosure across rich pages;
- no information removal;
- no critical information hover-only;
- real-browser computed-style/cognitive walkthrough.

### Phase 6 — NARROWLY REOPENED

Must prove the Phase 3/5 corrections in real browser acceptance.

Specific required acceptance:

- reference-occurrence adversarial cases where browser/Product behavior is relevant;
- typography hierarchy from computed styles;
- same role same visual treatment even across h2/h3;
- page title > section title > body/meta;
- nested subsection visually below parent;
- progressive disclosure accessibility;
- no required information lost;
- all previous rich browser acceptance remains green.

### Phase 7 — NOT STARTED

Do not start until Phases 3/5/6 re-close.

Target:

**operator-managed observed/trace-source packaging/config**

Current observed trace ingestion is still conceptually an offline/ad-hoc trace-file capability in important paths. Phase 7 should make it operationally manageable without turning Pacto into an observability backend.

### Phase 8 — NOT STARTED

Live Kind product vertical breadth.

### Phase 9 — NOT STARTED

Real built MkDocs browser E2E.

### Phase 10 — NOT STARTED

Docker Desktop/containerd/local-registry Kind path.

### Phase 11 — NOT STARTED

MCP catalog core.

### Phase 12 — NOT STARTED

MCP discovery server/CLI/E2E/docs.

### Phase 13 — NOT STARTED

Normative invariants finalization.

### Phase 14 — NOT STARTED

Finalization, final ontology audit, repository hygiene, PR body, exact SHA, readiness.

## 3. Accepted Product architecture

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

## 4. Accepted information-parity work

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

The prior lazy read incorrectly used a live `os.DirFS`, allowing the same RevisionKey to return changed bytes after Build.

Latest accepted correction:

- Build records SHA-256 fingerprint for listed document content;
- lazy read verifies bytes against fingerprint;
- changed/deleted/unreadable body becomes explicit unavailable;
- 512 KiB bound remains;
- source conflicts do not silently pick arbitrary bytes.

Do not reopen absent a new counterexample.

## 5. Accepted UI/interaction corrections

### Search autocomplete

Services search has Product-backed suggestions.

Expected behavior:

- debounced;
- bounded;
- canonical identity;
- same-name domain disambiguation;
- keyboard/pointer;
- stale-response protection.

### Same-query SWR

Accepted model:

- first load / changed identity may load;
- same-query refresh retains last valid data;
- refresh failure retains stale data with honest notice;
- changed query must not present old rows as if they answered the new query.

Latest implementation added explicit query identities to list views and a shared stale refresh notice.

### Scroll restoration

Accepted model:

- scroll belongs to a history entry, not only URL;
- same URL in different history entries remains independent;
- push starts fresh;
- Back/Forward restores exact entry;
- hard reload restores current entry;
- canonical replace preserves entry;
- state bounded;
- user scroll input can cancel pending restoration.

### Graph spatial state

Accepted from prior review:

- same-query refresh preserves layout;
- reload restores graph spatial state;
- query identities isolate graph state;
- semantic data still refreshes;
- `Fit` and `Reset layout` are different.

## 6. Accepted visualization direction

The redesign initially became too list/table-heavy.

Accepted principle:

> lists/tables for exact inspection; visualizations for system comprehension.

Useful visual summaries were added/evolved across Product surfaces.

Do not restore V1 dashboards mechanically.

Any future chart must:

- answer a real question;
- use authoritative aggregate data;
- not infer global truth from a page;
- preserve exact values;
- expose uncertainty/completeness;
- remain accessible/mobile/light/dark.

## 7. Current high-priority blocker details

### Blocker A — transitive lock reference occurrence identity

The current reference resolution is better than earlier heuristics but still potentially selects the wrong authoritative lock entry.

Required next iteration:

1. reproduce direct-vs-transitive same kind/name collision;
2. fix lock occurrence identity;
3. audit local relative reference dedupe identity;
4. preserve deterministic lock generation;
5. decide lock schema/version compatibility deliberately;
6. test ReferencedBy;
7. no frontend fix for backend identity problems.

### Blocker B — design token integrity

Perform complete Product CSS variable audit.

Known suspicious/undefined tokens from independent review:

- `--text-md`
- `--radius-md`
- `--c-accent-border`

Do not assume this list is exhaustive.

Need architecture guard for undeclared global Product tokens.

### Blocker C — typography hierarchy

Need explicit visual roles:

- Page title
- Section title
- Subsection title
- Body
- Secondary body
- Label
- Meta/caption
- Metric
- Code

HTML heading level must not accidentally determine visual role.

Entity pages need a visible dominant page title.

### Blocker D — information density

Do not delete recovered information.

Introduce consistent progressive disclosure:

**Primary**
- understand now.

**Secondary**
- intentional inspection.

**Diagnostic**
- exact IDs, provenance, raw refs, limitations/debugging.

Short conceptual help may use accessible tooltip/popover.

Substantive detail should use disclosure/drawer/drill-down.

Critical warnings must remain visible and must not be hover-only.

## 8. Latest verification snapshot

Claude's latest handoff for `759845ca` reported:

- gofmt clean;
- go vet clean;
- golangci-lint 0 issues;
- gocyclo clean;
- race/coverage gates at 100.0%;
- examples pass;
- demo contracts 24/24;
- authored U+00A7 gate clean;
- CLI docs current;
- dashboard SDK drift clean;
- deterministic second generation;
- UI cold build reproducible;
- `svelte-check`: 0 errors, 15 warnings, described as pre-existing;
- Vitest: 1116 passed / 66 files;
- Playwright: 167 passed, 0 failed / 18 specs;
- 0 `test.fixme`;
- 1 `test.skip` described as a data guard in headings;
- exact-SHA CI reported green across required workflows;
- PR review threads reported 132 total / 6 unresolved;
- six unresolved threads reported as generated minified Mermaid vendor findings;
- zero unresolved authored-code threads.

Important process rule:

**Do not trust these counts blindly in a later chat. Re-verify exact final SHA, CI and review threads before accepting the next handoff.**

## 9. Historical constraint

Authored-content U+00A7 gate is expected green.

Blocking enforcement over old branch commit history / PR metadata remains constrained because historical branch commit messages already contain the forbidden character.

No permission exists to rewrite shared history.

Do not rebase/filter-history/force-push to solve that unless Eduardo explicitly authorizes it.

## 10. Next iteration objective

The immediate next Claude session should be narrow:

1. fix authoritative reference occurrence identity;
2. audit local relative reference closure identity;
3. fix global design-token drift;
4. introduce stable visual typography roles;
5. make page titles visually dominant;
6. apply consistent progressive disclosure;
7. reduce permanent explanatory text without losing information;
8. visually inspect all canonical Product routes;
9. add computed-style and disclosure browser acceptance;
10. re-close Phase 3/5/6 only when those counterexamples pass;
11. do not begin Phase 7.

Once those re-close, freeze broad Product UI work again and proceed to Phase 7.

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
