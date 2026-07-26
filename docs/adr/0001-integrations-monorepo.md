# ADR 0001 — Pacto first-party integrations monorepo

Status: Accepted (supersedes the paired-repo release model)
Date: 2026-07-26

## Context

Pacto shipped as two repositories: `TrianaLab/pacto` (contract model, pure engine,
CLI, dashboard) and `TrianaLab/pacto-operator` (Kubernetes collector + operator).
The paired release audit ended with **DO NOT MERGE as an atomic pair** because the
operator depends on unreleased engine packages (`pkg/evidence`, `pkg/finding`,
absent from the latest engine tag `v2.7.0`) and builds only through a dev-only
`replace ../pacto`. The required release order was:

    merge engine -> tag/publish engine -> repin operator go.mod -> remove replace
    -> rerun operator CI -> merge and release operator

That ordering is a structural blocker, not documentation friction.

## Decision

Adopt a single repository, `TrianaLab/pacto`, as the canonical home of Pacto and
its officially maintained integrations, while keeping **independently versioned,
independently published, standalone-consumable** components.

- **Separate Go modules**, not one module:
  - root module `github.com/trianalab/pacto/v3` — platform-neutral core + CLI + dashboard.
  - nested module `github.com/trianalab/pacto/integrations/kubernetes` — collector + operator host.
- **Root `go.work`** ties the modules for local development, CI and release *builds*
  only. The integration `go.mod` carries a normal `require` on the core with
  **no `replace`**; `go.work` resolves it to the local source.
  - Cross-major transition: when the core moves to a new major whose first
    version is not yet published (e.g. `v3.0.0` on `github.com/trianalab/pacto/v3`),
    `go.work` cannot build the module graph from an unpublished `require`. A
    dev-only *versioned* workspace replace (`replace github.com/trianalab/pacto/v3
    v3.0.0 => .` in `go.work`, never in any module's `go.mod`) resolves it. It is
    absent under `GOWORK=off`, so the published integration `go.mod` still has no
    replace, and the release publishes the core `v3.0.0` first so the require
    resolves for real (proven by the standalone smoke test below).
- The imported operator was history-preserved (subtree import at operator SHA
  `199de04`; every operator commit remains an ancestor and traceable to
  `TrianaLab/pacto-operator`).

### Why this eliminates the blocker (not hides it)

`go.work` makes *source development atomic* (one commit builds everything). The
release layer ([ADR 0002](0002-changesets-release.md) / Changesets) computes a new core version, tags+publishes
the core module first, then bumps the integration `go.mod` to that version and
publishes the integration — so every published module resolves outside the
workspace. An external-consumer smoke test builds the integration module from a
temporary module with `GOFLAGS=-mod=mod` and **no `go.work`** to prove
standalone consumability. Moving files alone does not solve ordering; the tested
version/tag/publish sequence does.

## Architecture boundaries (enforced)

Dependency direction, enforced by `tests/architecture/boundary_test.go` and run
in CI via `make ci-gates` (a dedicated `ci-gates` job plus the `ci` aggregate):

    k8s operator -> k8s collector -> Pacto evidence/evaluation APIs -> Pacto core

The platform-neutral engine packages the integration consumes — `pkg/contract`,
`pkg/evidence`, `pkg/finding`, `pkg/graph`, `pkg/oci`, `pkg/readiness`,
`pkg/schemax`, `pkg/semver`, `pkg/validation` — must never import `k8s.io/*`,
`sigs.k8s.io/*` or any `integrations/*` package (transitively). The gate is the
integration's full `v2/pkg/...` import closure minus `pkg/dashboard`, which
intentionally embeds a `client-go` runtime source. It fails the build on any
violation, so external/third-party collectors can consume the core without
pulling Kubernetes.

The Kubernetes **collector** — the `internal/observer` package inside the
integration module (Kubernetes -> Evidence translation) — is a separable package,
not a separate top-level directory or module. It is decoupled from the operator
**host** (`internal/controller`: reconciliation, CRDs, status, temporal windows,
deployment) and is testable without starting a controller manager. It implements
the platform-neutral `pkg/collector.Collector` interface in the core.

## Release units and tag policy

| Release unit | Kind | Tag / coordinate |
|---|---|---|
| Pacto core module | Go module | `vX.Y.Z` (root) |
| Pacto CLI | binaries + checksums | GitHub Release `vX.Y.Z` |
| Pacto dashboard | OCI image | `ghcr.io/trianalab/pacto-dashboard:X.Y.Z` |
| K8s integration module | Go module | `integrations/kubernetes/vA.B.C` |
| Operator image | OCI image | `ghcr.io/trianalab/pacto-operator/pacto-controller:A.B.C` |
| Operator chart | Helm/OCI | `pacto-operator` chart `version=A.B.C`, `appVersion=A.B.C` |
| K8s integration docs | MkDocs (mike) | integration version A.B.C |
| Demo OCI bundles | OCI | existing demo coordinates |

**Fixed groups:** {core, CLI, dashboard image} version together; {k8s module,
operator image, chart, k8s docs} version together. Future first-party
integrations are versioned independently unless a real dependency forces grouping.
Go multi-module tag policy: the core uses `vX.Y.Z`; the nested integration module
uses the path-prefixed tag `integrations/kubernetes/vA.B.C` (Go's nested-module
tag convention). Public OCI/chart coordinates are preserved from the operator repo
(see the artifact pipeline ledger).

## Version continuity (from inventory)

Historical public versions preserved: core Go module `v2.7.0` (latest), operator image + chart `v4.7.0` (latest). The k8s integration release group continues the operator `v4.x` line (image + chart + integration module), NOT reset to 0.x. Core continues `v2.x`. Bare-`vX.Y.Z` tag collision (engine v2 vs operator v4) is resolved by path-prefixed nested-module tags.

## Consequences

- One coherent source tree, one reviewed change, one CI result, one release plan.
- Independent public versions preserved (historical continuity — the operator is
  NOT reset to 0.1.0 because its source moved).
- The old `TrianaLab/pacto-operator` repo is archived only *after* a staging
  release simulation proves the new pipeline (see the cutover checklist,
  [ADR 0003](0003-operator-repo-cutover.md)).
