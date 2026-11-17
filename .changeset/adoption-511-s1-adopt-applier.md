---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

Pacto detects the manager that owns each Stage's workloads and never promotes
through another one. Stages can apply through a Flux HelmRelease.

Upgrade effect first. `delivery.argocd.enabled` and `delivery.flux.enabled`
default to `auto`. An upgrade that takes the new chart's defaults on a cluster
that serves Argo CD Applications grants the promoter `patch` on Applications, limited by
admission to `spec.source.targetRevision` of labelled Applications; set
`delivery.argocd.enabled=false` to keep it off. Flux access renders as before,
plus HelmRelease read, `--flux-helm` and the `applier-helmrelease` admission
policy when the cluster serves Flux HelmReleases.

- The promoter reads, and never writes, the labels, annotations, field
  managers and owner references of each workload a Stage's Pactos report, and
  records the manager in the Stage's new `status.applier` (observed, with
  evidence, `asOf` and `completeness`) and the `Applier` print column. A
  manager that contradicts `spec.applyRef` reads `Ready` `False`
  `ApplierConflict`; an unsupported or ambiguous manager, a workload without
  its applier's marks or an unreadable one reads `ApplierUnknown`.
- The new `pacto.applier` promote gate fails on a conflict and is Unknown when
  Pacto cannot tell; both refuse the promote, and nobody can accept them.
- `spec.applyRef.kind` accepts `HelmRelease`. The promoter writes the chart
  digest to the OCIRepository the HelmRelease's `spec.chartRef` names, in the
  Stage's namespace, and counts a move applied once the HelmRelease is Ready at
  that digest. `spec.chart` reads `ApplierNotChartRef`.
- Contracts on pactoVersion 2.5 may set `delivery.applier` to `auto`, `argocd`
  or `flux`; an older contract with it fails with `NEEDS_PACTO_2_5`.
  `pacto_edit` sets it and `pacto_schema` serves it.
- `pacto delivery status`, `pacto delivery plan`, MCP `pacto_delivery_status`
  and the dashboard Delivery views print one applier line per service, with the
  reason when the Stage does not adopt it.
- The delivery binary gains `--flux` (default `true`) and `--flux-helm`
  (default `false`). Go API: the new `pkg/workloadmanager` package (`Detect`,
  `Describe`), `delivery.AdoptApplier`, `delivery.ApplierStatus` and
  `delivery.GateApplier`.
