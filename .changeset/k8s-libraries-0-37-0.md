---
"@pacto/core": patch
"@pacto/k8s-module": patch
---

Move both Go modules onto the Kubernetes 0.37.0 library line and patch the
runtime image's OpenSSL.

`k8s.io/api`, `k8s.io/apimachinery` and `k8s.io/client-go` are now v0.37.0 in
`go.mod` and `integrations/kubernetes/go.mod`, together with the transitive
`k8s.io/kube-openapi`, `k8s.io/utils`, `k8s.io/streaming`,
`sigs.k8s.io/structured-merge-diff/v6` and `go-openapi/swag` moves the line
pulls in. The three library modules only work in lockstep, so bumping them
one at a time — as the individual Dependabot pull requests did — leaves
`k8s.io/api` behind and fails to compile.

The runtime stage of the CLI/dashboard image now runs `apk upgrade` before
installing its packages, so the image picks up the fixed `libssl3`/`libcrypto3`
3.5.8-r0 instead of the 3.5.7-r0 baked into the `alpine:3.22` tag (CVE-2026-14456,
HIGH).

No API or behaviour change.
