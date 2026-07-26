# ADR 0002 — Changesets intent + custom multi-target release

Status: Accepted
Date: 2026-07-26

Relates to: [ADR 0001](0001-integrations-monorepo.md) (the monorepo decision this
release layer implements) and [ADR 0003](0003-operator-repo-cutover.md) (the
post-release cutover this pipeline must prove before it runs).

## Context

ADR 0001 keeps the engine and the Kubernetes integration in one repository as
**separate, independently versioned, independently published** modules. The old
paired-repo blocker (the operator building only through a dev `replace ../pacto`)
is eliminated only if the release layer publishes in a proven order: bump and
publish the core first, then repin the integration `go.mod` to that published
core version with no `replace`, then publish the integration. Moving files does
not solve ordering — the tested version/tag/publish sequence does.

Pacto's release units are not npm packages. They are:

- Go modules (`github.com/trianalab/pacto/v2`, `.../integrations/kubernetes`)
- OCI images (dashboard, operator controller)
- a Helm chart (`pacto-operator`)
- OCI-distributed contract/demo bundles
- a versioned MkDocs (mike) docs site

No off-the-shelf release tool understands all of these targets together while
also giving a human-authored, PR-based semver intent flow with lockstep groups.

## Decision

Split the release into an **intent layer** (Changesets) and a **mechanism layer**
(custom multi-target version + publish scripts). Changesets decides *what version
each unit becomes*; custom scripts *apply those versions to real artifacts and
publish them in a safe order*.

### Intent layer — Changesets

- `@changesets/cli` (`.changeset/config.json`, `package.json` scripts). Root is
  `private`, access `restricted`: **nothing is ever npm-published.**
- One placeholder npm package per release unit under `release/units/*`
  (`@pacto/core`, `@pacto/cli`, `@pacto/dashboard-image`, `@pacto/demo-bundles`,
  `@pacto/dashboard-contract-bundle`, `@pacto/k8s-module`, `@pacto/operator-image`,
  `@pacto/operator-chart`, `@pacto/k8s-docs`). These exist only so Changesets can
  compute a version graph; they carry the unit's real coordinate under a `pacto`
  key, not npm dependencies.
- Two `fixed` groups enforce lockstep (from ADR 0001): {core, CLI, dashboard
  image, demo bundles, dashboard contract bundle} move together; {k8s module,
  operator image, operator chart, k8s docs} move together.
- Contributors drop `.changeset/*.md` files declaring the bump per unit; the
  changelog and version math are Changesets' job.

### Mechanism layer — custom multi-target scripts

`npm run release:version` = `changeset version` then two scripts:

1. `changeset version` bumps `release/units/*/package.json` and writes changelogs.
2. `release/scripts/build-release-plan.mjs` reads the units and emits
   `release/release-plan.json` — the single source of truth: per-unit version,
   coordinate, tag (including the nested-module path tag
   `integrations/kubernetes/vA.B.C`), the release-state `goModPin`
   (published core, no replace) with `assertNoReplace: true`, the
   `compatibility.pactoCore` range, and a deterministic `publishOrder` (core
   first, so the integration's pin resolves).
3. `release/scripts/apply-release-plan.mjs` idempotently mutates the working tree
   into release state — pins the integration `go.mod` to the published core and
   fail-closes if any `replace` survives (`assertNoReplace`), sets the chart
   `version`/`appVersion` + Artifact Hub image tag, the `integration.yaml`
   compatibility, and the chart README install pins — then writes
   `release/release-manifest.json`. Re-running produces a byte-identical tree.

`npm run release:publish` = `release/scripts/publish.mjs` publishes each unit to
its real target (Go module tag, OCI image, Helm chart, docs) in the plan's
`publishOrder`. It is idempotent, immutability-aware (never overwrites a
published version) and resumable.

### Release-state pin (no replace)

`go.work` makes source development atomic. Committed release state pins a real
published core version in `integrations/kubernetes/go.mod` with **no `replace`**;
`apply-release-plan` enforces this fail-closed. This is what makes every
published module resolvable outside the workspace.

### Verify-standalone gate

`release/scripts/verify-standalone.sh` proves standalone consumability before any
publish: it computes the NEXT core version from pending changesets (the version
that will actually contain `pkg/evidence` + `pkg/finding`), repins a throwaway
copy of the integration module to it, asserts zero `replace`, and runs
`GOWORK=off GOFLAGS=-mod=mod go build ./...` in a throwaway module cache with no
network and no publish. It fails if no core bump is pending (a release would
otherwise repin an unchanged core).

## Alternatives considered

- **semantic-release / release-please**: commit-message- or PR-driven, but npm/
  GitHub-release centric; no first-class Go-module + OCI + Helm + mike targets,
  and weaker lockstep-group ergonomics.
- **goreleaser only**: strong for Go binaries + images, but no semver-intent
  layer, no lockstep version graph across non-Go artifacts, no changelog flow.
- **Fully custom**: rebuilds the version graph, changelog and PR flow Changesets
  already gives for free.

Changesets is already installed, gives the intent layer, and cleanly hands its
computed versions to the custom mechanism layer.

## Consequences

- One reviewed source change, one computed version graph, one release plan file.
- Publication is deterministic, ordered, idempotent and immutability-safe.
- The standalone-verify gate proves the ADR 0001 blocker stays eliminated.
- The npm units never publish — they are a version-graph device, not artifacts.
- This pipeline must pass a staging release simulation before the
  `TrianaLab/pacto-operator` cutover (ADR 0003) runs.
