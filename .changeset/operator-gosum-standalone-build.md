---
"@pacto/k8s-module": patch
---

Carry the checksums for the pinned engine core in the operator's `go.sum`.

The published `v5.4.0` module requires `github.com/trianalab/pacto/v3 v3.3.0`
while its `go.sum` only covers `v3.2.7`, so building the operator module on
its own — outside the workspace, which is how a consumer or a fresh clone
builds it — fails with `missing go.sum entry` for every `pacto/v3` package.
The fix landed on main in #369; this release ships it, and moves the
published pin to `v3.3.1`.
