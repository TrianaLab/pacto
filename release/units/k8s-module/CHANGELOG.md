# @pacto/k8s-module

## 6.0.1

### Patch Changes

- fe07e42: Fix the operator's `go.sum` checksum for `github.com/trianalab/pacto/v4` v4.0.0.
  The operator v6.0.0 tag recorded a checksum for a core tree that was never
  tagged, so a standalone build of `integrations/kubernetes` failed with a
  checksum mismatch and the v6.0.0 operator image and chart never published.

## 6.0.0

### Major Changes

- 9b84eda: Fixes from a whole-repo architecture review of the operator. Two of them change what you see.
  
  **Behaviour changes**
  
  - Controller logs default to JSON (production mode). Pass `--zap-devel` to get the old console format back.
  - The `RuntimeObserved` condition is now `False` with reason `ObservationFailed` whenever any runtime read failed during collection, and the message lists what failed. Before, it could read `True` while parts of the collection had failed.
  
  **Fixes**
  
  - PactoRevision names and version labels are valid for every semver version, including build metadata (`1.0.0+build.1`), uppercase pre-releases and very long versions. Names that were already valid do not change.
  - ServiceMonitor and PodMonitor endpoints that name their port (the usual case in real clusters) are now resolved, so metrics discovery through them works.
  - An unset numeric `targetPort` now means the Service port everywhere, as the API server defaults it.
  - Configuration schemas are compiled once per contract revision instead of on every reconcile.
  
  **Breaking changes and migration (operator v6)**
  
  - The Go module path becomes `github.com/trianalab/pacto/integrations/kubernetes/v6` (was `.../v5`) and requires `github.com/trianalab/pacto/v4`. Go code importing `api/v1alpha1` updates its imports; `go get github.com/trianalab/pacto/integrations/kubernetes/v6@v6.0.0`.
  - Apply the v6 CRDs before `helm upgrade`, which never touches them: `kubectl apply --server-side --force-conflicts -f charts/pacto-operator/crds/`. v6 adds the `NodeRuntimeObservation` CRD and the operator exits at startup without it (`no matches for kind "NodeRuntimeObservation"`), whether or not the node agent is enabled.
  - Removed `--webhook-cert-path`, `--webhook-cert-name` and `--webhook-cert-key`. The operator registers no webhook and starts no webhook server; drop the flags from any manifest that still passes them, or the operator refuses to start on an unknown flag.
  - Removed `--dashboard-oci-secret` and the `dashboard.ociSecret` chart value, superseded by `--dashboard-oci-secrets` and `dashboard.ociSecrets`. Migrate with `dashboard.ociSecrets: [<name>]`; the chart schema rejects the old key.
  - Removed the condition reasons no reconciler emitted (`NotFound`, `AllPortsMatch`, `MissingPorts`, the seven endpoint probe reasons `OK`, `ConnectionFailed`, `InvalidStatusCode`, `EmptyResponse`, `NotDeclared`, `InterfaceHasNoPort`, `InterfaceNotFound`, and `Match`, `Mismatch`, `Missing`, `Skipped`) and the `SeverityError` and `SeverityWarning` constants from `api/v1alpha1`. Nothing in a cluster carries them; Go code importing them uses `finding.Severity` and the reasons in `troubleshooting.md`.
  
  **Samples**
  
  - The scenario samples under `config/samples/scenarios/` use `pactoVersion: "2.0"` contracts. The duplicate showcase samples were removed in favour of `config/samples/demo/`.
- 9b84eda: Add an opt-in eBPF node agent and the runtime checks it and the Kubernetes API
  make possible.
  
  `nodeAgent.enabled` deploys a DaemonSet (`pacto agent --sink kubernetes` from
  the pacto runtime image the operator already runs the Evidence Server from;
  `--node-agent-image` overrides it) that
  watches TCP socket state through one BTF tracepoint and
  publishes each node's listeners and connections as a `NodeRuntimeObservation`
  object, a new CRD. It reads no payloads, files or Secrets and opens no port. It
  needs Linux 5.8 or later with BTF, `hostPID`, the node's `/sys/fs/cgroup`
  mounted read-only, root and the `BPF`, `PERFMON` and `SYS_PTRACE`
  capabilities, so it runs in its own namespace (`nodeAgent.namespace`, default
  `pacto-node-agent`), which the chart creates with the privileged Pod Security
  level; the release namespace keeps whatever level you set. Each agent renews a
  `coordination.k8s.io` Lease named after its node every publish interval and
  rewrites its `NodeRuntimeObservation` only when the view changes (or every tenth
  interval); the operator reads the node's heartbeat from the Lease. Where the
  API serves `ValidatingAdmissionPolicy` (Kubernetes 1.30 or later), the chart
  binds each agent's writes to the node named in its token, so one agent cannot
  speak for another node. The operator merges the evidence into interface and dependency
  observations: a ready pod with nothing listening on a TCP port becomes
  `INTERFACE_ABSENT`, the latest connection success or failure within the
  staleness bound decides dependency reachability (never against a provider with
  no ready endpoint) and a stale or failed node reads `Unknown`, never a
  violation. The agent's Role is `get`, `create` and `patch` on its observations
  and `get`, `create`, `patch` and `update` on its Leases, the DaemonSet runs on
  Linux nodes only and the operator deletes the objects of a node that no longer
  exists.
  
  Findings carry `evidenceRefs[].collector` and `detectedAt`;
  `status.evaluationCoverage.unverified` lists facets with no usable fact.
  
  **Behaviour change:** the complementary checks run with or without the node
  agent, because none of them depends on it: the interface protocol (with
  `controller.enableProbing`), the interface exposure, the provider version for
  `dependencies[].compatibility` (which also picks the target when several
  providers match the ref) and the persistence scope (read-only claims ignored).
  Exposure is `external` only on positive evidence of a public address: a public
  `externalIPs` entry, or a `LoadBalancer` Service without an internal load
  balancer annotation or an Ingress whose status carries a public IP or
  hostname. A `NodePort` Service or an HTTPRoute leaves it unknown. An interface
  with no `visibility` is `internal`, so one behind such a public address now
  reports `INTERFACE_VISIBILITY_MISMATCH`. Declare `visibility: public` for it.
  
  Fixes: interface availability compared the Service port with the EndpointSlice
  port, which carries the target port, so a Service with `port` different from
  `targetPort` read absent; `oci://` dependencies never matched a sibling Pacto
  and read `Unsupported`; passive health ignored the declared path. The chart now
  exposes `controller.enableProbing` and `controller.enableMetricsObservation`,
  grants `list` and `watch` on ConfigMaps, which the cached reads needed, and
  adds read access to pods, Ingresses, HTTPRoutes and `NodeRuntimeObservation`
  objects. Cached pods are trimmed to the fields the operator reads.
  
  The kernel merge and the probes now come from core `pkg/runtimefacts` and
  `pkg/probe`, so the same verdicts hold on Kubernetes and on any host.
  
  The operator uses evidence types from this release's core, so ship it in the
  same release as `@pacto/core`: the core tag is created first and the operator
  `go.mod` must pin it.
  
  **Breaking changes and migration (operator v6)**
  
  - The node agent moved to a dedicated namespace. Set `nodeAgent.namespace` if
    `pacto-node-agent` does not suit you; it must not be the release namespace and
    must not already exist outside this release. Remove the
    `pod-security.kubernetes.io/enforce=privileged` label you may have put on the
    release namespace for the agent. The operator deletes the agent it ran in the
    release namespace (DaemonSet, RBAC and observations) at its first sync. A
    private `imagePullSecrets` entry must also exist in the agent namespace.
  - `--kernel-evidence-namespace` (and `controller.kernelEvidence.namespace`)
    now defaults to the agent namespace, not the operator's. New flag:
    `--node-agent-namespace`.
  - The managed components' lifecycle permissions moved out of the ClusterRole:
    a `Role` in the release namespace (dashboard and Evidence Server, plus the
    teardown of the old agent) and a `Role` in the agent namespace (the agent's
    DaemonSet, RBAC, observations and Leases). The ClusterRole keeps the reads,
    the dashboard's ClusterRole and binding, node reads, Lease reads and a by-name
    read and relabel of the agent namespace. The operator no longer creates
    namespaces at all, and no longer needs `namespaces` list or watch. An operator
    installed without the chart needs the new Roles (`config/rbac/role.yaml`
    generates them).
  - Objects the operator creates for the agent no longer carry an owner
    reference to the operator Deployment: it lives in another namespace, and
    Kubernetes garbage-collects a dependent whose owner is in another namespace.

## 5.4.3

### Patch Changes

- 79de1ba: This week's dependency bumps in one go: `bubbletea` 2.0.10 → 2.1.0 and
  `x/sync` 0.23.0 → 0.24.0 in the engine, OpenTelemetry 1.46.0 → 1.47.0 (and the
  Prometheus exporter 0.68.0 → 0.69.0) in the operator module. No source changes.
  
  Go goes 1.26.6 → 1.26.9 and `x/net` 0.59.0 → 0.60.0 in both modules, because
  govulncheck flagged reachable `net/http` and `x/net/http2` vulns
  (GO-2026-6607 to GO-2026-6617) and wouldn't let any of the bot's PRs through.
  Both shipped binaries were exposed, so it's worth the patch release.

## 5.4.2

### Patch Changes

- fa6d27a: Take the week's dependency bumps in one go: `modelcontextprotocol/go-sdk`
  1.7.0 → 1.8.0 in the engine, and `controller-runtime` 0.25.0 → 0.25.1,
  `ginkgo` 2.32.2 → 2.33.0 and `gomega` 1.43.0 → 1.43.1 in the operator module.
  No source changes — all four are drop-in.
  
  Dependabot also learns to group: Kubernetes libraries move as one train
  because client-go, apimachinery and controller-runtime pass each other's
  types, Charm moves as one because bubbletea, bubbles and lipgloss share the v2
  renderer, everything else minor-or-patch lands in a single weekly PR. Five
  open PRs for one afternoon of bumps is how a bot teaches you to stop reading
  it.

## 5.4.1

### Patch Changes

- 35c37f6: Carry the checksums for the pinned engine core in the operator's `go.sum`.
  
  The published `v5.4.0` module requires `github.com/trianalab/pacto/v3 v3.3.0`
  while its `go.sum` only covers `v3.2.7`, so building the operator module on
  its own — outside the workspace, which is how a consumer or a fresh clone
  builds it — fails with `missing go.sum entry` for every `pacto/v3` package.
  The fix landed on main in #369; this release ships it, and moves the
  published pin to `v3.3.1`.

## 5.4.0

### Minor Changes

- 771574b: Make the operator's status tell the truth about overrides, force-pushed tags and
  per-tag failures, and stop the registry fan-out riding on the reconcile loop.
  
  **`spec.overrides` are now validated.** An override patched the parsed contract
  struct only, so validation layers 1 and 3 — the JSON Schema check and policy
  enforcement — still ran against the original document. An override that violated
  a policy reported `ContractValid=True`. The patch now applies to the raw YAML as
  well, so every layer sees the contract the operator actually reconciles.
  
  **Force-push detection works on the cases it was missing.** Two of them. A tag
  force-pushed twice compared the registry digest against an arbitrary
  `PactoRevision` rather than the newest one, so `TagOverwritten` re-fired on every
  reconcile forever. And a registry tag of `v1.2.0` for a contract declaring
  `service.version: 1.2.0` missed the label lookup entirely, so the drift check
  never ran for any v-prefixed tag.
  
  **A tag that fails to load is now reported.** Every per-tag failure during
  mirroring was a `log.V(1)` and a `continue`: invisible at default verbosity, and
  one bad tag aborted nothing but told nobody. Failures are aggregated and surface
  as the same events the main path already uses — `ContractUnavailable` for a
  transient obtain failure, `ContractInvalid` for everything else.
  
  **`.status.contractStatus` and the dashboard now agree.** The operator derived
  the status ladder from findings with its own local copy of the rule. It calls
  `validation.DeriveStatus` instead, so a contract cannot read `Warning` in
  `kubectl` and `Compliant` in the dashboard.
  
  **A configuration's schema is read from the bundle.** The runtime config
  dimension treated `configuration.schema` as inline JSON when it is a
  bundle-relative path, so a real schema never compiled and the dimension reported
  insufficient evidence instead of a verdict.
  
  **Tag mirroring runs on its own schedule.** Enumerating a registry's tags and
  loading each one used to happen inline in `Reconcile`, which every watched
  workload write triggers — so a busy namespace turned into registry traffic
  proportional to unrelated churn. It is now a manager runnable on a five-minute
  tick, independent of reconciliation.
  
  **Pull Secrets are read straight from the API server.** A cached typed `Get` on
  a Secret builds an informer for every Secret in scope and parks its `.data` in
  the operator's memory. Secret reads go through the uncached reader and the Secret
  watch carries metadata only, so the operator holds no credential it is not using
  right now.
  
  The dashboard and Evidence Server components moved to the same runnable shape
  behind a shared `component` lifecycle, with the same five-minute tick and an
  ownership check before any delete.
  
  Deprecated, with no replacement needed: the condition reasons no reconciler
  emits — `NotFound`, `AllPortsMatch`, `MissingPorts`, every `ReasonEndpoint*`,
  every runtime-reconciliation reason and the three severity constants. The
  outcomes they used to name are reported as evidence and findings now. They stay
  through v5 for API compatibility and are removed at v6.

## 5.3.0

### Minor Changes

- 92a064f: Make the contract verdict usable as a GitOps promotion gate, and emit the event
  that says a contract recovered.
  
  The operator has always reached a verdict and written it to
  `status.contractStatus`. Neither Flux nor Argo CD reads that field. Flux decides
  health with kstatus, which recognises `Ready`, `Reconciling` and `Stalled` and
  discards every condition Pacto publishes, so a Kustomization holding a
  `NonCompliant` Pacto reports healthy. Argo picks health checks from a closed list
  of built-in kinds and returns nothing for the rest, and the roll-up ignores
  nothing — a violated contract is not unhealthy to Argo, it is invisible. Both
  tools have an extension point for this; neither ships one for Pacto.
  
  The new [GitOps promotion gates](integrations/kubernetes/gitops.md) page is those
  two snippets, plus the timing they depend on. The Flux one is a
  `spec.healthCheckExprs` entry with no `inProgress` expression, so an unrecognised
  verdict holds the deploy instead of going falsely green. The Argo one is a Lua
  health customization written for the sandbox Argo actually runs it in, which has
  the string library disabled.
  
  Neither snippet is an illustration. Both live under
  `tests/acceptance/kind/fixtures/gitops/`, the page includes those files rather
  than copies of them, and each has a kind acceptance shard that applies it to a
  real cluster running the tool it targets.
  
  `gitops-flux.sh` proves the Flux gate changes what ships: a contract that
  contradicts the workload must leave the dependent Kustomization's manifest out of
  the cluster entirely, and correcting the contract must let it through. It runs at
  the operator's default stabilization window on purpose, because the page claims a
  mismatch does not wait one out.
  
  `gitops-argocd.sh` runs in two passes, because the Argo snippet's failure mode is
  silence — a `data` key Argo does not recognise is ignored, and an ignored key
  looks exactly like having configured nothing. The first pass needs no cluster:
  the `argocd` CLI evaluates the customization in the same Lua sandbox the
  controller uses, which is what gives every contract status an assertion,
  including the states a running cluster passes through too quickly to catch — no
  status yet, a verdict behind the contract, a status added in some future release.
  The second pass serves an Application from an OCI source in kind and requires it
  to go `Degraded` naming the finding, then `Healthy` once the contract is
  corrected. The page's read-back recipe is that same CLI command, and the shard
  runs it against the live cluster rather than only publishing it.
  
  Building that second pass turned up something the page has to say out loud: the
  merge patch alone is not enough. Argo's application controller caches health
  customizations at startup and compares that cached verdict to decide whether a
  changed object is worth re-examining, so a controller that started without the
  customization treats every verdict the operator writes as no change and only
  catches up on the next periodic resync, minutes later. The page now pairs the
  patch with a controller restart, and says why the two read-back checks cannot
  detect the difference — both read the ConfigMap, not the controller.
  
  `ContractRecovered` is new. The three contract warnings — `ValidationFailed`,
  `ContractInvalid`, `ContractUnavailable` — are transition-gated, so a contract
  that goes back to `Compliant` used to fall silent with no event marking the
  recovery. It now has one, matching the pair that `ReadinessGateUnmet` and
  `ReadinessRecovered` already formed. `ValidationFailed` also fired only when
  `status.summary` happened to be set; it now fires on the transition itself.
  
  Two documentation corrections ride along, both load-bearing for the timing advice
  on the new page. The stabilization window applies to **absences only** —
  `INTERFACE_ABSENT`, `DEPENDENCY_UNREACHABLE`, `CAPABILITY_ABSENT` and
  `CONFIGURATION_ABSENT`. A **mismatch** — `WORKLOAD_MISMATCH`,
  `PERSISTENCE_MISMATCH`, `CONFIGURATION_MISMATCH` — is `NonCompliant` on the first
  reconcile that observes it. The events table said seven events and listed seven;
  there are eight.

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
