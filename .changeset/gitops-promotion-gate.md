---
"@pacto/k8s-module": minor
---

Make the contract verdict usable as a GitOps promotion gate, and emit the event
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

The Flux snippet is not an illustration. It lives at
`tests/acceptance/kind/fixtures/gitops/flux-kustomization.yaml`, the page includes
that file rather than a copy of it, and a new kind acceptance shard applies the
same file to a real cluster running Flux: a contract that contradicts the workload
that ships must leave the dependent Kustomization's manifest out of the cluster
entirely, and correcting the contract must let it through. The shard runs at the
operator's default stabilization window on purpose, because the page claims a
mismatch does not wait one out.

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
