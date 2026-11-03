---
"@pacto/core": patch
"@pacto/k8s-module": patch
---

Dependency bumps from Dependabot in one go: `bubbletea` 2.1.0 → 2.1.1, `huma`
2.39.1 → 2.40.0, `prometheus/common` 0.72.1 → 0.73.0 and `sigstore` 1.10.9 →
1.11.1 in the engine, and `k8s.io/api`, `apimachinery` and `client-go` 0.37.1 →
0.37.2 in both modules. No source changes.

`sigstore` 1.11 needs Go 1.27, so Go goes 1.26.10 → 1.27.3 everywhere it's
pinned (both modules, `go.work`, both Dockerfiles), and golangci-lint goes
v2.13.0 → v2.14.0 because v2.13 can't load Go 1.27 code.

Heads-up: Go 1.27's gzip writes different bytes, so `pacto push` now produces different OCI bundle digests for the same content than older binaries did. The published demo bundles get re-published once via `release/demo-bundle-migration.txt`.
