---
"@pacto/core": patch
---

`pacto init` skips the files `.pactoignore` lists, and it no longer takes a Backstage `catalog-info.yaml` or a `skaffold.yaml` for a Kubernetes manifest, so neither lands in the `manifests` artifact Flux applies.
