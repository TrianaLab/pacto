---
"@pacto/core": patch
---

`pacto init` no longer reads a Helm chart's templates as Kubernetes manifests, so unrendered templates stay out of the `manifests` artifact and never become the contract's workload. Unknown names the chart once and says how to render it.
