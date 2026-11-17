# Delivery

`delivery` says how the Pacto promoter delivers the versions
[`publish`](publish.md) produces. Only the promoter reads it. The other
sections are in [Contract sections](sections.md) and
[Publish](publish.md).

## `delivery`

Optional. Needs `pactoVersion: "2.5"`: an older contract with a `delivery`
section fails validation with `NEEDS_PACTO_2_5`.

```yaml
pactoVersion: "2.5"
service: {name: payments, version: 2.4.0}
delivery:
  applier: argocd
```

| Field | Meaning |
|---|---|
| `applier` | The manager that applies the service's workloads: `auto`, `argocd` or `flux`. An absent value means `auto`. Every change to it is non-breaking |

| Value | What the promoter does |
|---|---|
| `auto` | Adopts the manager that already owns each workload: an Argo CD Application, a Flux Kustomization or a Flux HelmRelease. Nothing else to set |
| `argocd` | Requires an Argo CD Application: the Stage's `spec.applyRef.kind` must be `Application` |
| `flux` | Requires Flux: the Stage's `spec.applyRef.kind` must be `OCIRepository` or `HelmRelease` |

The value is declared. What the promoter detects on the workloads is
observed, and it records both apart on every promote. When the version's
contract names an applier the Stage does not apply through, the
`pacto.applier` gate fails and the promote is refused, whoever asks. Point the
Stage's `spec.applyRef` at the applier the contract names, or change
`delivery.applier`.
[Which manager applies a Stage](../integrations/kubernetes/delivery-reach.md#which-manager-applies-a-stage)
says how the promoter detects the manager.
