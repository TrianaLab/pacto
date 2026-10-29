---
"@pacto/core": patch
---

`pacto init` no longer reads a Helm chart's templates as Kubernetes manifests, so unrendered templates stay out of the `manifests` artifact and never become the contract's workload. A chart's `crds/` folder is skipped too, whether the chart sits in a subfolder or at the service root. Unknown names every chart found and says how to render it.
