---
"@pacto/core": patch
"@pacto/k8s-module": minor
---

The Pacto status records the guarantees of the contract, so the fleet the
promoter builds from Pacto status evaluates them as a fleet built from
published bundles does.

- `status.interfaces[]` adds `guarantees`, `lifecycle` and
  `compatibilityEpoch`, under the contract's own field names.
- `status.dependencies[]` adds `requires`, one entry per interface of the
  provider with the guarantees the dependency relies on.
- The promoter's fleet rebuilds each Pacto's interfaces from its status. A
  dependency whose provider runs in the cluster reports its guarantees as met
  or unmet, and as unknown when no Pacto of the provider runs there. The
  impact analysis lists a guarantee the candidate does not meet under each consumer
  whose status records the requirement.

The fields are optional and the CRD version stays `v1alpha1`. Pactos last
reconciled by an older operator carry none of them until the operator
reconciles them again.
