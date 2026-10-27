---
"@pacto/k8s-module": minor
---

The `pacto-operator` chart has a `global` block shared by every workload it
runs (the controller, the delivery promoter and the recorder):
`global.imageRegistry` (default `ghcr.io`), `global.imagePullSecrets`,
`global.commonLabels`, `global.podSecurityContext` and
`global.securityContext`. A component's own `podSecurityContext` or
`securityContext` replaces the global one, and its `imagePullSecrets` follow
the global ones. The delivery pods take `delivery.podAnnotations` and
`delivery.podLabels`.

**Changed values:** `image.repository` and `delivery.image.repository` no
longer carry the registry (`trianalab/pacto/operator`,
`trianalab/pacto/delivery`). Set `image.registry` or
`delivery.image.registry` for one image, `global.imageRegistry` for all, or
`global.imageRegistry=""` to give each repository as a whole reference. The
top-level `podSecurityContext` and `securityContext`, and their `delivery.`
counterparts, default to empty and fall back to the global ones.
