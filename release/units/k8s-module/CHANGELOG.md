# @pacto/k8s-module

## 5.2.4

### Patch Changes

- 840a183: Move both Go modules onto the Kubernetes 0.37.0 library line and patch the
  runtime image's OpenSSL.
  
  `k8s.io/api`, `k8s.io/apimachinery` and `k8s.io/client-go` are now v0.37.0 in
  `go.mod` and `integrations/kubernetes/go.mod`, together with the transitive
  `k8s.io/kube-openapi`, `k8s.io/utils`, `k8s.io/streaming`,
  `sigs.k8s.io/structured-merge-diff/v6` and `go-openapi/swag` moves the line
  pulls in. The three library modules only work in lockstep, so bumping them
  one at a time — as the individual Dependabot pull requests did — leaves
  `k8s.io/api` behind and fails to compile.
  
  The runtime stage of the CLI/dashboard image now runs `apk upgrade` before
  installing its packages, so the image picks up the fixed `libssl3`/`libcrypto3`
  3.5.8-r0 instead of the 3.5.7-r0 baked into the `alpine:3.22` tag (CVE-2026-14456,
  HIGH).
  
  No API or behaviour change.

## 5.2.3

### Patch Changes

- b11de31: Move both Go modules onto the Kubernetes 0.36.4 library line.
  
  `k8s.io/client-go`, `k8s.io/api` and `k8s.io/apimachinery` are now v0.36.4 in
  `go.mod` and `integrations/kubernetes/go.mod`. The bumps landed on `main`
  without a changeset, so neither the core line nor the kubernetes line would
  have shipped them — this patch is what actually publishes a core module and an
  operator image built against 0.36.4.
  
  No API or behaviour change: the 0.36.4 patch releases only refresh the
  `golang.org/x` dependencies underneath.

## 5.2.2

### Patch Changes

- 2ea7dbe: Rebuild the operator on pacto core v3.2.2.
  
  The 3.2.2 Version PR bumped `integrations/kubernetes/go.mod` to
  `github.com/trianalab/pacto/v3 v3.2.2`, but a core-line release does not
  republish the kubernetes line, so the operator image and chart stayed on the
  code built against v3.2.1. This changeset moves the kubernetes fixed group to
  5.2.2 so the published operator actually carries:
  
  - one `FILE_NOT_FOUND` finding per missing interface spec file, and a directory
    at a spec path no longer passing as a file
  - date-like scalars kept verbatim through generic YAML round-trips, so an
    unquoted timestamp is validated as the text the contract author wrote

## 5.2.1

### Patch Changes

- c230de9: Make a demo-fixture edit unable to half-ship a release.
  
  Release run 32560058692 published four irreversible units and then died. Two
  independent defects had to line up for that, and both are closed here.
  
  The demo bundles publish to immutable tags. `payments-service` 2.1.0 was edited
  in place — a mermaid diagram added to a version already published — so the
  byte-exact gate correctly refused the tag, but it refused it mid-release,
  because nothing ran that gate before the release. The fixture is restored to its
  published bytes and the diagram ships as a new `payments-service` 2.1.1, and
  `publish-demo-bundles.sh --check` now runs the identical gate read-only at PR
  time as the `demo-bundle-immutability` CI leg.
  
  Separately, the `demo-compose` job lost its ORAS install when the unit moved to
  `docker compose publish`, on the reasoning that ORAS stayed where the ledger
  used it — while that job still read and wrote the ledger, which *is* the ORAS
  user. `ledger.sh` returned the empty string for a missing binary, the empty
  string means "nothing recorded", and the unit failed closed. `ledger.sh` now
  refuses to run without its tools and distinguishes a 404 from an unreadable
  registry; the two `if [ "$(ledger.sh …)" ]` call sites that discarded its exit
  status now assign first; and a new gate walks every job's shell through its make
  targets and scripts and fails when a job can reach a CLI it never installed.
  That gate found a second, quieter instance: the release dry run was rehearsing
  without `syft`, silently skipping the SBOM the real release produces.

## 5.2.0

### Minor Changes

- 8352060: Run the operational graph in a cluster: an operator-managed Evidence Server and observed-dependency input for the dashboard.
  
  - **Managed Evidence Server.** Set `evidence.enabled` and the operator reconciles
    a separate Evidence Server Deployment and an internal Service, with optional
    Ingress or Gateway API `HTTPRoute` exposure. It is single-writer, so its replica
    count is fixed at one, and it runs the same runtime image as the managed
    dashboard. `evidence.registry.subjects` names the exact immutable contract
    revisions evidence may be reported against; the registry holding them is the
    durable store, so the chart installs no volume, database or bucket of its own.
  - **Observed dependencies.** `dashboard.observation.sources` mounts offline
    OTLP/JSON trace exports read-only into the managed dashboard, so its
    operational graph can reconcile declared dependencies against observed ones.
    Each entry is one named data source with a stable identity, read through a root
    the process cannot follow a symlink out of, never written to and never scanned.
    This is offline input only: no OTLP receiver ships and no collector is deployed.
  - **`insecureRegistries`.** Reach named `host:port` registries over plain HTTP for
    a controlled in-cluster registry, scoped per host so every other registry stays
    HTTPS-only. The controller, the managed dashboard and the managed Evidence
    Server all inherit it.
  
  Also adds a `pacto-dev-gateway` chart that installs Envoy Gateway and a
  `GatewayClass` for local development, so the Gateway API path can be exercised on
  a laptop cluster.
  
  Backwards compatible: every new capability is off by default and no existing
  value, CRD field or status shape changes.

## 5.1.2

### Patch Changes

- bbc7b9c: Rebuild the operator and dashboard container images through the new native per-arch
  build pipeline: each architecture builds on its own runner (no QEMU emulation) and is
  merged into the multi-arch manifest. No functional change to the engine, operator, or
  dashboard — this release ships and validates the faster image pipeline.

## 5.1.1

### Patch Changes

- dd4dab1: Repo-wide audit remediation (engine, CLI, dashboard). Closes the OpenAPI
  breaking-change diff false-negatives — path-item-level parameters are now diffed,
  the request body is deep-diffed so a newly required property is BREAKING, and
  optional→required / added-required parameters are BREAKING — so a BREAKING-only
  release gate can no longer be bypassed. Further security and dashboard fixes land
  in the same PR.

  As the first release since v3.1.0, this also ships the previously-merged but
  unreleased dashboard **Ctrl+C shutdown fix** and the demo version-label fix.

## 5.1.0

### Minor Changes

- b58778a: Unify all published OCI artifacts under the monorepo `ghcr.io/trianalab/pacto/*` namespace.

  - operator image → `ghcr.io/trianalab/pacto/operator`
  - operator chart → `ghcr.io/trianalab/pacto/charts/pacto-operator`
  - dashboard image → `ghcr.io/trianalab/pacto/dashboard`
  - dashboard contract bundle → `ghcr.io/trianalab/pacto/dashboard-contract`
  - demo bundles already live under `ghcr.io/trianalab/pacto/*`

  All packages are now created and owned by this repository. The chart name
  `pacto-operator` and the Artifact Hub repository are preserved (re-point the AH
  repository URL to the new chart coordinate). The previous coordinates remain as
  historical — their already-published versions are unaffected. Go module paths
  (`/v3`, `/v5`) are unchanged.

## 5.0.0

### Major Changes

- 045f11e: The Kubernetes integration moves into the monorepo — breaking for consumers.

  - The Go module path becomes `github.com/trianalab/pacto/integrations/kubernetes/v5`
    (was `github.com/trianalab/pacto-operator`).
  - It pins the published core module `github.com/trianalab/pacto/v3` at release
    time; the `go.work` workspace resolution is development-only.
  - The integration continues the operator `v4` line as `v5` (image + chart +
    module); public OCI/chart coordinates are preserved.
