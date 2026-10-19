---
"@pacto/k8s-module": patch
---

Fix the operator's `go.sum` checksum for `github.com/trianalab/pacto/v4` v4.0.0.
The operator v6.0.0 tag recorded a checksum for a core tree that was never
tagged, so a standalone build of `integrations/kubernetes` failed with a
checksum mismatch and the v6.0.0 operator image and chart never published.
