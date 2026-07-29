---
"@pacto/core": patch
"@pacto/k8s-module": patch
---

Rebuild the operator and dashboard container images through the new native per-arch
build pipeline: each architecture builds on its own runner (no QEMU emulation) and is
merged into the multi-arch manifest. No functional change to the engine, operator, or
dashboard — this release ships and validates the faster image pipeline.
