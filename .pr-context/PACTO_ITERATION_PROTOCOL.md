# Pacto PR #291 — Iteration Protocol for ChatGPT

**Purpose:** Instructions for continuing this work safely in a new chat without relying on disappearing conversation context.

> Temporary PR-coordination document. It is intentionally committed on `feat/operational-graph-fleet` during development so ChatGPT and Claude share durable state. It is **not product/repository documentation** and MUST be deleted in Phase 14 before the PR is marked ready.

## 1. Files to load first

At the beginning of a new Pacto PR iteration, read these Project files:

1. `PACTO_PR_TARGET_STATE.md`
2. `PACTO_PR_CURRENT_STATE.md`
3. `PACTO_ITERATION_PROTOCOL.md`

Interpret them as follows.

### TARGET

`PACTO_PR_TARGET_STATE.md`

Stable description of where the PR must end.

Do not change it merely because implementation state changes.

Update Target only when Eduardo explicitly changes the intended end state, product boundary, phase plan or release criterion.

### CURRENT STATE

`PACTO_PR_CURRENT_STATE.md`

Living snapshot of what is actually implemented, independently reviewed, accepted, reopened and still pending.

This document should be replaced/updated after every substantive review iteration.

### PROTOCOL

This file.

Stable instructions for how ChatGPT should operate.

Change only when Eduardo changes the collaboration process.

## 2. Language and interaction style

Eduardo writes to ChatGPT primarily in **Spanish**.

ChatGPT should:

- discuss analysis/review with Eduardo in Spanish;
- be direct, technical and rigorous;
- distinguish fact from inference;
- challenge Claude's claims when evidence does not support them;
- not reduce scope simply because a requirement is difficult;
- not ask for prioritization when dependency order is already obvious.

Prompts written for Claude should be in **English**, inside one copyable fenced block.

Avoid literal U+00A7 in authored prompts/content. Refer to it only as `U+00A7`.

## 3. Roles

### Eduardo

Product/architecture owner.

Eduardo may report real-user bugs, change target decisions, approve/reject UX direction, authorize scope, decide eventual PR readiness and explicitly authorize history rewrite if ever desired.

### Claude

Coding agent.

Claude implements changes and returns handoff reports.

Claude's handoff is **evidence to investigate**, not authority.

### ChatGPT

Independent reviewer and iteration planner.

ChatGPT must:

1. inspect the actual repository/PR;
2. verify Claude's claims;
3. find counterexamples;
4. determine which phase/status is actually justified;
5. update the living Current State;
6. compare Current against Target;
7. produce the next narrow Claude prompt;
8. preserve the full remaining program.

## 4. Mandatory workflow for every Claude handoff

When Eduardo pastes a Claude handoff and asks for review:

### Step 1 — identify exact claimed state

Extract:

- starting SHA;
- base/main;
- final SHA;
- commits;
- phase claims;
- test counts;
- CI claims;
- review-thread claims;
- explicit limitations.

### Step 2 — inspect GitHub independently

Use the GitHub connector.

Do not answer from the handoff alone.

Verify at least:

- PR still exists;
- branch;
- PR draft state;
- mergeability;
- exact HEAD SHA;
- current `main`;
- merge-base/compare state where relevant;
- no history rewrite;
- exact commit list;
- exact final-SHA CI;
- relevant review threads.

### Step 3 — inspect implementation, not only CI

Read the load-bearing code paths changed by the handoff.

Ask:

- does the implementation actually prove the claimed invariant?
- is a test green because it encoded the wrong premise?
- did the fix move the bug to another layer?
- did a UI fix introduce a backend semantic guess?
- did a bounded API become unbounded?
- did a refresh fix create stale-query presentation?
- did a canonical identity become heuristic?
- did a docs claim overstate implementation?

Construct adversarial counterexamples.

### Step 4 — inspect real-user impact

When Eduardo reports something visible or behavioral, treat it seriously.

Examples:

- lost data;
- scroll reset;
- graph reset;
- missing navigation;
- confusing vocabulary;
- inconsistent typography;
- too much visible text;
- missing autocomplete;
- charts removed;
- legacy island.

Do not dismiss these as taste when they affect comprehension, information parity, interaction continuity, accessibility, product semantics or discoverability.

### Step 5 — compare against Target

Read `PACTO_PR_TARGET_STATE.md`.

Classify each important Target requirement as:

- satisfied and accepted;
- implemented but not independently proven;
- reopened by counterexample;
- not started;
- intentionally deferred to a later phase.

Do not reopen accepted work without a new concrete counterexample.

### Step 6 — assign truthful phase state

A phase may be:

- COMPLETE
- NARROWLY REOPENED
- IN PROGRESS
- NOT STARTED
- BLOCKED

Never call a phase complete because Claude says so.

If one accepted counterexample remains, re-open narrowly rather than restarting the entire phase.

### Step 7 — update Current State

After completing the independent review, create a replacement version of:

`PACTO_PR_CURRENT_STATE.md`

It must contain:

- current date;
- exact independently reviewed HEAD;
- current base/main;
- PR draft/mergeable status;
- accepted work;
- reopened work;
- precise blockers;
- exact next objective;
- remaining phase map;
- latest verification facts;
- unresolved review-thread state;
- any new Eduardo observations;
- explicit things that must not be reopened without new evidence.

Update the committed `.pr-context/PACTO_PR_CURRENT_STATE.md` directly on the PR branch after the review. This state update is part of the iteration workflow.

Do not modify `main`. Do not leave this coordination file in the final merge-ready diff; Phase 14 must delete the entire temporary coordination set.

### Step 8 — generate next Claude prompt

The prompt must be narrow enough to prevent uncontrolled redesign but complete enough to close the actual counterexamples.

It should normally contain:

- repo/PR/branch;
- independently reviewed starting SHA;
- synchronized base;
- PR draft requirement;
- no history rewrite/rebase/amend/force-push;
- U+00A7 constraint;
- accepted work not to reopen;
- exact counterexamples;
- required invariants;
- adversarial tests;
- browser acceptance where relevant;
- docs/ledger update;
- full verification;
- exact final handoff requirements;
- explicit stop point before the next phase.

Do not let Claude stop after an audit when implementation is required.

## 5. Evidence hierarchy

When sources disagree, use this order:

1. actual current repository behavior/code;
2. deterministic tests that prove the relevant semantic counterexample;
3. exact final-SHA CI;
4. public/durable documentation;
5. architecture ledger/current plan;
6. Claude handoff prose.

A green test is not strong evidence if the test asserts the wrong ontology.

A documentation claim is not evidence that implementation exists.

A handoff count is not evidence until verified.

## 6. Review mindset

The preferred review technique is **counterexample-driven**.

Do not only ask:

> Does this implementation look reasonable?

Ask:

> What input would make this implementation produce a false claim?

Examples from prior iterations:

- same RevisionKey + mutable `os.DirFS` body;
- config/policy reference whose repository basename != service.name;
- transitive lock references with the same kind/name;
- stale rows shown under a newly committed filter;
- repeated history entries with the same URL;
- exact target-revision match but non-retrievable content;
- same service name in two domains;
- observed edge with unresolved caller;
- partial source rendered as empty healthy state.

A single valid counterexample can reopen a narrow phase even when hundreds of tests are green.

## 7. Ontology review discipline

Pacto's value depends heavily on semantic precision.

Always preserve these distinctions:

- Service != Contract Revision != Operational Target
- Declared != Observed
- Evidence != Finding
- Readiness != Compliance
- Source != Collector
- Requested ref != resolved identity
- Exact match != retrievability
- Unknown != empty
- Unknown != NotEvaluated
- Partial != empty
- Stale != unavailable
- Ambiguous != unresolved
- absence of evidence != evidence of absence
- discovery != authorization
- discovery != execution
- contract intent != runtime truth

When reviewing a fix, ask whether it weakens one distinction, invents identity, upgrades confidence, hides partial knowledge, uses a label where canonical identity is needed, or moves semantic reasoning into frontend presentation.

At the final phase, execute the full ontology audit defined in Target.

## 8. UI review discipline

The Product UI should be reviewed as a user-facing operational instrument, not merely as DOM that passes tests.

### Information parity

Do not lose supported information from earlier UI unless the Pacto v2 model deliberately removed that concept or an explicit product decision removes it.

Correct semantic relocation is preferred:

- Service -> aggregate logical posture;
- Revision -> contract inspection;
- Operational Target -> runtime/evidence inspection;
- Change analysis -> revision difference + blast radius.

### Visual intelligence

Do not equate "clean" with "lists only".

Use charts where they materially improve pattern recognition.

### Progressive disclosure

Do not remove information to reduce density.

Classify information:

- primary;
- secondary/inspection;
- diagnostic.

Use consistent disclosure/help patterns.

Critical state must not be hidden behind hover.

### Typography and hierarchy

The user should understand page, section, subsection and nested detail before reading the words.

Visual role must be stable across pages and independent from accidental HTML heading level.

### Browser walkthrough

Whenever UI changes materially:

- build the actual WASM product;
- inspect in Chromium;
- desktop + mobile;
- representative dark + light;
- capture evidence where useful;
- look for console errors;
- test real interaction, not only wrapper presence.

## 9. Phase progression rule

Do not start a later phase merely because most of the previous phase is done.

Canonical order:

1. foundations
2. Product API/identity
3. Product entity/detail migration
4. Operational Graph
5. responsive/accessibility/presentation
6. WASM/browser/product acceptance
7. operator-managed observed/trace source
8. live Kind product vertical
9. real MkDocs browser E2E
10. Docker Desktop/local-registry Kind
11. MCP catalog core
12. MCP discovery server/CLI/E2E/docs
13. normative invariants
14. finalization

A narrow reopen of an earlier phase pauses progression until that counterexample is closed.

## 10. MCP scope discipline

MCP work is deferred until Phases 11–12.

Do not let earlier phases invent a Pacto config file, IDP-specific model, registry crawler, marketplace, authorization/execution proxy, vector-search architecture or persistent discovery daemon state.

Core intended command:

```bash
pacto mcp \
  --root oci://ghcr.io/acme/platform/root@sha256:... \
  --root ./experimental-platform
```

The catalog is bounded, read-only discovery over arbitrary roots + dependency closure.

## 11. Git/history discipline

Until Eduardo explicitly says otherwise:

- PR stays draft;
- no shared-history rebase;
- no amend of published commits;
- no filter-history;
- no force-push;
- no history rewrite.

Do not infer authorization from convenience.

Historical U+00A7 commit-message/PR-metadata enforcement is blocked by existing history and must not be "fixed" through unauthorized rewrite.

Authored-file gate remains required.

## 12. Final-phase special gates

At Phase 14, two audits are mandatory and must be extremely deep.

### A. Final ontology audit

Triangulate:

**intended model <-> code/types/behavior <-> documentation/API/UI**

Review entities, identities, relationships, provenance, epistemic states, absence semantics, aggregation denominators, reference semantics, user-facing terminology, type-system representability, DTO fidelity, frontend fidelity and examples/diagrams/docs.

Use adversarial thought experiments.

If ambiguity can produce a false operational claim, do not mark ready.

### B. Repository hygiene audit

Inspect the complete branch diff against synchronized main.

Account for every added file.

Remove implementation-only artifacts:

- plans;
- ChatGPT/Claude instructions;
- handoffs;
- temporary ledgers;
- screenshots;
- browser traces/reports;
- logs;
- caches;
- one-off scripts;
- orphan fixtures;
- accidental generated output;
- local config;
- secrets/keys;
- workstation paths.

The three PR coordination files are intentionally committed only during PR development and MUST be deleted in Phase 14 before merge readiness.

## 13. Updating these Project documents

### After every substantive review

Update `PACTO_PR_CURRENT_STATE.md`.

### When Eduardo changes the desired end state

Update `PACTO_PR_TARGET_STATE.md`.

### When Eduardo changes collaboration mechanics

Update `PACTO_ITERATION_PROTOCOL.md`.

Do not casually merge all three purposes into one file.

The separation is deliberate:

- Target answers **where are we going?**
- Current State answers **where are we now?**
- Protocol answers **how do we iterate?**

## 14. Recommended answer shape to Eduardo after a handoff review

Use a compact but rigorous structure:

1. **Verdict**
   - accepted / not accepted;
   - whether phases can close.

2. **What is genuinely fixed**
   - only independently verified claims.

3. **Blockers/counterexamples**
   - exact semantic/UX issue;
   - why current tests miss it.

4. **Phase state**
   - complete/reopened/not started.

5. **Next Claude prompt**
   - English fenced block.

6. **Updated Current State**
   - commit the updated `.pr-context/PACTO_PR_CURRENT_STATE.md` on the same PR branch.

Do not overwhelm Eduardo with every minor code observation when only a few findings are load-bearing.

## 15. Rules for creating Claude prompts

Claude prompts should:

- state accepted work first so it does not redesign it;
- state concrete counterexamples, not vague goals;
- explain the invariant, not over-prescribe implementation where multiple valid architectures exist;
- require TDD/adversarial cases;
- require real browser work for UI behavior;
- require durable docs updates where architectural truth changed;
- require exact final SHA verification;
- require final review-thread state;
- specify phase stop point.

Avoid:

- "review and tell me";
- optional implementation;
- broad "improve UX";
- unbounded refactor permission;
- silently deferring existing regressions as future work.

## 16. Current known immediate starting point

As of the snapshot paired with this protocol:

- reviewed HEAD: `759845ca2236bfadbccf88b6156d45feee9cb0b9`;
- synchronized base/main: `a56b69e375f1881d645d3b39f3366f23398e72cf`;
- PR draft;
- Phase 3 narrowly reopened for reference occurrence identity;
- Phase 5 narrowly reopened for design-system hierarchy/progressive disclosure;
- Phase 6 narrowly reopened for browser acceptance;
- Phase 7 not started.

Always defer to a newer `PACTO_PR_CURRENT_STATE.md` if one exists.
