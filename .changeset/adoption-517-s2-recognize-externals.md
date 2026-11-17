---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

Pacto recognizes the vendors, managed services and legacy hosts that a
service's configuration, pod spec and traces already name, with no contract
edit. Every surface shows the host and port at most: a user name, password,
path and query are stripped, a value under a sensitive key is never parsed and
no Secret value is ever read.

- `pacto init` and `pacto_create` with `pactify` list the externals that the
  environment, ConfigMaps, Secret and key names and every chart's
  `values.yaml` name, and write the likely ones as commented entries under
  `dependencies` for a person to uncomment. The parsed contract does not change.
- The operator writes `status.externals`, `status.externalsCoverage` and
  `status.externalCatalog` at `PactoStatusFormat` 3. It reads the target
  workload's env names and values, Secret names, `secretKeyRef` key names and
  the ConfigMap keys the pod spec references, plus the `ExternalName` Services,
  selectorless Services and Istio `ServiceEntries` of its own namespace that
  the workload names. `status.externals` shows every reader of a Pacto the
  normalized hosts that the referenced ConfigMap keys hold.
- The operator role gains `get`, `list` and `watch` on
  `networking.istio.io` `serviceentries`, rendered by the chart only when the
  cluster serves that API. Without it the coverage reads `partial`, reason
  `serviceentry-reader-off`.
- The fleet joins trace callees that the external catalog recognizes to
  external systems as `observed` edges, and reads the operator's externals as
  `inferred` edges with `inferredSources`. Neither enters an index that impact
  or a gate reads. `NeighborhoodEdge.provenance` gains the value `inferred`
  and edges gain `inferred: true`. `rpc.system` is now the last OTel callee
  key.
- New limitations: `EXTERNALS_UNREPORTED` (an older, partial or stale
  operator status), `EXTERNAL_CATALOG_MISMATCH` and
  `EXTERNAL_USE_UNATTRIBUTED`. `OBSERVED_IDENTITY_UNRESOLVED` stays for names
  that match more than one service or that are neither a service nor an
  external system.
