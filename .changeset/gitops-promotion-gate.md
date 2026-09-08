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
