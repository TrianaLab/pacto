---
"@pacto/core": patch
---

Reframe the README, the pacto.run homepage and the pages that define what Pacto
is around a positive category noun: **operational contract system**.

The definitional slot on both front doors was held by an analogy
("Pacto is to service operations what OpenAPI is to HTTP APIs") and, on the
homepage, by an eyebrow reading "Open contract standard". The first installed
"file format" as the category and discarded the engine; the second promised
governance and a second implementation that do not exist. Both are replaced by
a definition that states what a contract records, how it is published and what
it is compared against.

The category noun says what Pacto is; one sentence beside it now says what that
is for. Pacto gives software a machine-readable operational interface — a
versioned description of what a service is, what it exposes, what it depends on
and what it promises — so platforms, CI systems, controllers, automation and
agents consume the same interface instead of each reconstructing operational
knowledge from deployment files, documentation and runtime state.

Structural changes:

- `README.md` leads with the category, then the problem (operational facts with
  nowhere to live, and a dependency edge that carries no version range), then
  the mechanism, then who reads a contract, then why software operating software
  raises the price of not having one. "What Pacto is NOT" drops from four
  bullets to three and moves behind all of it.
- `docs/index.md` moves "What Pacto is not" below "The problem" — non-goals
  disambiguate a model the reader already holds and cannot build one. The
  heading and its anchor are unchanged, so `#what-pacto-is-not` still resolves.
- The hero's only jump link now points at `#what-is-pacto` rather than at the
  list of exclusions.
- The IDP contrast is cut back to a single non-goal bullet. What replaces it as
  the differentiator is version shape: a catalog entry records that an edge
  exists, a Pacto dependency records the range it accepts and pins the closure
  by digest.

Corrections found while verifying the copy against the implementation:

- `Diff · Graph · Enforce · Verify` becomes `Diff · Graph · Validate · Verify`.
  There is no `pacto enforce`; policy is Layer 3 inside `validate`, and
  `MANIFEST.md` disowned "enforcement" eight lines below the slogan.
- `docs/contract-reference/sections.md` claimed a `configurations[].ref` is
  resolved from the referenced bundle at the fixed path
  `configuration/schema.json`. No code reads that path. The reference is
  validated as well-formed, recorded as a reference edge and pinned in
  `pacto.lock`; the recursive-resolution claim is scoped to the lockfile, which
  is the surface that actually walks the closure.
- `docs/index.md` said `pacto generate` produces deployment artifacts. It
  invokes a `pacto-plugin-<name>` binary you supply; Pacto ships no generators.
- "Blast-radius analysis" as an MCP capability becomes impact analysis, the
  feature that exists.
- The Kubernetes overview stated "never modifies" and then retracted it. It now
  leads with the actual grant — `get`, `list`, `watch` on watched workloads —
  and keeps the managed-component escalation as the second half of the same
  paragraph rather than as a retraction.
