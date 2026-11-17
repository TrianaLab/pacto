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
policy when the cluster serves Flux HelmReleases; the agent, approver and
recorder roles gain HelmRelease `get` there too, which includes inline Helm
values.

- The promoter reads, and never writes, the labels, annotations, field
  managers and owner references of each workload a Stage's Pactos report, and
  records the manager in the Stage's new `status.applier` (observed, with
  evidence, `asOf` and `completeness`) and the `Applier` print column. A
  manager that contradicts `spec.applyRef` reads `Ready` `False`
  `ApplierConflict`; an unsupported or ambiguous manager, a workload without
  its applier's marks or an unreadable one reads `ApplierUnknown`, with the
  reason a Kustomization could not be read. So does a Pacto the operator has
  not reconciled, or one that evaluated another workload than the Stage runs.
  Detection reads a Kustomization outside the Stage namespace only when it
  applies from the Stage's OCIRepository, and gives one fixed answer for any
  other the workload's labels name. `status.applier.staleAt` is twice the
  promoter's resync after `asOf`, so a longer `--resync` never reads stale.
- The new `pacto.applier` promote gate fails on a conflict and is Unknown when
  Pacto cannot tell; both refuse the promote, and nobody can accept them.
- `spec.applyRef.kind` accepts `HelmRelease`. The promoter writes the chart
  digest to the OCIRepository the HelmRelease's `spec.chartRef` names, in the
  Stage's namespace, and counts a move applied once the HelmRelease is Ready at
  that digest. `spec.chart` reads `ApplierNotChartRef`.
- `pacto delivery status`, `pacto delivery plan`, MCP `pacto_delivery_status`
  and the dashboard Delivery views print one applier line per service, with the
  reason when the Stage does not adopt it. `pacto delivery plan` and the
  candidate list read versions from an Argo CD Application's or a
  HelmRelease's repository, as the promoter does.
- An Operation the promoter cannot read the applier for says
  `reading the applier`, and a deadline says `the applier did not confirm`,
  instead of naming an OCIRepository or Flux.
- An `app.kubernetes.io/instance` label of `<namespace>_<app>` names the
  Application in that namespace, as the tracking id does.
- The delivery binary gains `--flux` (default `true`) and `--flux-helm`
  (default `false`). Go API: the new `pkg/workloadmanager` package (`Detect`,
  `Describe`), `delivery.AdoptApplier`, `delivery.ApplierStatus` and
  `delivery.GateApplier`.
