---
"@pacto/k8s-module": minor
---

The `pacto-operator` chart renders every CRD as a template from
`files/crds/`, which `make sync-crds` fills from `config/crd/bases`. `helm
upgrade` now updates the `Pacto`, `PactoRevision` and `NodeRuntimeObservation`
CRDs and adds new ones, and `helm uninstall` keeps every CRD
(`helm.sh/resource-policy: keep`). The delivery CRDs still follow
`delivery.enabled`. Set `crds.install=false` to manage the CRDs yourself.

**Upgrading:** CRDs an older chart installed from its `crds/` directory are not
part of the release, so `helm upgrade` stops with `invalid ownership metadata`.
Label and annotate them for your release once, as
[Upgrade the operator](https://pacto.run/latest/integrations/kubernetes/upgrade/#hand-older-crds-to-helm)
shows, then upgrade.
