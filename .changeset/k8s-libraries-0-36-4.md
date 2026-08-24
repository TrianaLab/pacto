---
"@pacto/core": patch
"@pacto/k8s-module": patch
---

Move both Go modules onto the Kubernetes 0.36.4 library line.

`k8s.io/client-go`, `k8s.io/api` and `k8s.io/apimachinery` are now v0.36.4 in
`go.mod` and `integrations/kubernetes/go.mod`. The bumps landed on `main`
without a changeset, so neither the core line nor the kubernetes line would
have shipped them — this patch is what actually publishes a core module and an
operator image built against 0.36.4.

No API or behaviour change: the 0.36.4 patch releases only refresh the
`golang.org/x` dependencies underneath.
