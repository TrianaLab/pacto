# @pacto/operator-chart

## 7.0.0

### Minor Changes

- 21f959b: A cluster that already runs an OpenTelemetry Collector, Tempo or Jaeger now
  gives the operational graph observed calls between services with no chart
  value. The operator detects each backend, plus a Datadog Agent, from the pods
  and Services it already reads. The dashboard reads the calls of the last 15
  minutes from Jaeger's `/api/dependencies` and from the service graph that
  Tempo's metrics-generator or the Collector's `servicegraph` connector writes to
  Prometheus, all read-only. For a Collector with no Prometheus, the dashboard
  receives OTLP/HTTP JSON traces on port 4318 (`--otlp-receiver`, set by the
  operator), keeps no span, serves nothing else there and turns off while
  another source proves complete coverage. The chart adds a NetworkPolicy that
  admits 4318 from pods in the cluster only. Receiver calls never corroborate a
  declared dependency and never clear a gate. Each backend is one Detected
  providers row with its own status, the reason it is partial and the Collector
  configuration to fix it. Pacto reads no Datadog Secret and holds no Datadog
  client.
- afb8709: The Evidence Server accepts signed evidence for every immutable revision
  published under a registry prefix, set with `pacto evidence serve
  --subject-prefix` or the chart value `evidence.registry.subjectPrefixes`,
  including revisions published after install, with no Helm edit and no
  restart. Refs must still be `@sha256` digests, exact subjects keep working and
  the trust config still narrows each key. The server records each subject in
  the `<prefix>pacto-evidence` repository, so a restart loses nothing. Where the
  registry serves the OCI Referrers API evidence stays a referrer of the
  revision; on GHCR and CNCF distribution each record is a write-once tag in
  `<prefix>pacto-evidence`, so a `ResourceProvisioned` report reads `Ready`
  there too. The server never writes the shared `sha256-<digest>` referrers tag.
- afb8709: `pacto export backstage --index <registry>/pacto-index` writes Backstage
  entities for every service the organization index lists, offline: it pulls
  each newest entry's bundle by digest with the same provenance and owner checks
  as `--oci`, and reads the inferred contracts those bundles name under
  `<registry>/pacto-inferred`. Add `--target-snapshot` for the inferred services
  the clusters published. `pacto fleet`, `pacto tui`, `pacto mcp` and
  `pacto reconcile` read `--index` the same way, up to 2000 services; scripts
  that passed `--index` to them and want the old behaviour drop it or pass
  `--index none`. `pacto impact` and `pacto ci` keep reading only consumers from
  it, so the index alone never clears a gate.
  
  `pacto dashboard --index` (`PACTO_DASHBOARD_INDEX`) serves the same entities at
  `/api/backstage/entities`. The operator-managed dashboard reads
  `<registry>/pacto-index` when `registry` is set; opt out with
  `dashboard.index: none`. On a private registry it needs a pull credential in
  `dashboard.ociSecrets`, or the route answers 503. A missing index on a
  long-running dashboard reads empty until it has once listed entries, then
  partial.
  
  Backstage entities carry three new annotations: `pacto.trianalab.io/source`,
  `pacto.trianalab.io/workload` and `pacto.trianalab.io/inferred-contract`.
- afb8709: Pacto reads the owners, systems and component dependencies a Backstage catalog
  already holds. `--backstage-url` on `pacto fleet`, `pacto impact`, `pacto tui`,
  `pacto mcp --fleet`, `pacto export backstage` and `pacto dashboard`
  (`PACTO_DASHBOARD_BACKSTAGE_URL`) names the catalog, and the reader token comes
  only from `PACTO_BACKSTAGE_TOKEN`. Each value is inferred, with its source,
  entity and read time: it never fills `owner` or the business domain, never
  becomes an owner claim, never clears a gate and never changes an impact
  verdict. The `backstage-catalog` source is advisory, so an unreachable or
  capped catalog shows as its own state and never makes an answer partial or
  blocks the Backstage export. Entities Pacto exported are never read back.
  
  Fleet answers and `pacto_fleet_get` carry the new keys `inferredOwner` and
  `inferredBusinessDomain`, the dashboard detail carries `ownership.inferred`
  and `inferredBusinessDomain`, and `ownerProvenance` reads `inferred` when no
  other owner exists. A name that matches several services raises
  `SERVICE_FACT_AMBIGUOUS`. `pacto export backstage` writes the inferred owner
  and domain only to the `pacto.trianalab.io/inferred-owner` and
  `pacto.trianalab.io/inferred-business-domain` annotations, with their
  `-source` annotations, and keeps `spec.owner: unknown`.
  
  The chart adds `dashboard.backstage.url` and `dashboard.backstage.tokenSecret`
  (the operator flags `--dashboard-backstage-url`,
  `--dashboard-backstage-token-secret` and `--dashboard-backstage-token-key`).
  Every dashboard user sees the owners, systems and dependencies the reader
  token can read, so give it catalog read on the entities every dashboard user
  may see. Detection proposes an in-cluster Backstage on port 7007, and with no
  URL set the dashboard reads it without a token.

### Patch Changes

- afb8709: Pacto finds golden paths and offerings in the registry they are published to.
  `pacto fleet`, `pacto mcp`, `pacto tui`, `pacto export` and `pacto stats` take
  `--discover <registry>[/<path>]` (repeatable), and `pacto dashboard --registry`
  and the operator-managed dashboard (the chart's `registry`) discover under the
  installation registry. Pacto lists `<registry>/pacto-index` first, then
  `_catalog` under the path where the registry serves it, and keeps only
  templates and offerings whose name matches their repository. On a registry
  with no `_catalog`, such as GHCR, the answer is partial with
  `REGISTRY_LISTING_INDEX_ONLY`, so an unlisted path reads unknown, never absent.
  `pacto impact` and `pacto ci` ignore `--discover`.
  
  Bundles pushed from this release on carry an artifactType and a kind
  annotation, so new content gets a different manifest digest than an older
  Pacto would give it. Tags and digests already published never move:
  `pacto push`, `pacto publish`, the inferred-contract repair and the demo
  publisher treat the older digest of the same content as identical.
  
  The chart value `dashboard.repositories` and the operator flag
  `--dashboard-repositories` are deprecated and still read: set `registry`.
- afb8709: Your own tools can check the `extension` capabilities a contract declares. A trust-config key gains `allowedExtensionRefs` (`pacto.dev/evidence-trust/v1`), which grants a producer exact extension refs and makes the key extension-only with `allowedObservationKinds: [CapabilityObserved]`. The Evidence Server accepts a signed extension report, a `CapabilityObserved` set under `<contract repository>/<segment>/extension/<name>` about extensions the contract declares, evaluates it under the authenticated producer and lists it under `extensions` on the targets endpoint with the new schema `pacto.dev/evidence-source/v6`, written only when a report exists. The fleet shows each reported extension of a revision as covered, failing or unknown with its producer and `asOf`: reported absent makes the service NonCompliant with `CAPABILITY_ABSENT`, and a stale report or one about another revision reads Unknown. An extension with no report stays unverified and never counts against compliance. The revision page, `pacto fleet get` and the MCP fleet tools show the check. No contract field, `pactoVersion`, route, MCP tool, RBAC rule or CRD changes.

## 6.6.0

### Minor Changes

- ca50460: Detect what the cluster provides. On start and every 5 minutes the operator writes the registry most namespaces pull from, the cloud, a sign-in provider, an exposure controller with a `--set` line for the dashboard route, whether the Prometheus Operator is served and the node kernels with BTF likely, unlikely or unknown to one ConfigMap, `<release>-detected`, in its namespace. Nothing is turned on. The rows show as inferred under Data sources, in `helm upgrade` notes, in `pacto fleet status` and `pacto fleet providers` with `--detected`, in `pacto_fleet_status` with `pacto mcp --fleet --detected` and in `pacto dashboard` with `PACTO_DASHBOARD_DETECTED`. A missing or stale detection names itself and never makes an answer partial. The chart grants `nodes list` in the cluster-wide ClusterRole and lets the operator create that ConfigMap and get and patch only it. With `controller.watchNamespaces` the registry row is partial, cloud and nodes are unknown and no ClusterRole is added.

## 6.5.0

No changes in this release.

## 6.4.1

No changes in this release.

## 6.4.0

No changes in this release.

## 6.3.0

No changes in this release.

## 6.2.0

No changes in this release.

## 6.1.0

No changes in this release.

## 6.0.1

## 6.0.0

## 5.4.3

## 5.4.2

## 5.4.1

## 5.4.0

## 5.3.0

## 5.2.4

## 5.2.3

## 5.2.2

## 5.2.1

## 5.2.0

## 5.1.2

## 5.1.1

## 5.1.0

## 5.0.0

### Major Changes

- 045f11e: The Kubernetes integration moves into the monorepo — breaking for consumers.

  - The Go module path becomes `github.com/trianalab/pacto/integrations/kubernetes/v5`
    (was `github.com/trianalab/pacto-operator`).
  - It pins the published core module `github.com/trianalab/pacto/v3` at release
    time; the `go.work` workspace resolution is development-only.
  - The integration continues the operator `v4` line as `v5` (image + chart +
    module); public OCI/chart coordinates are preserved.
