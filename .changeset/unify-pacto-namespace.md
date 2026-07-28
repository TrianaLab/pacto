---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

Unify all published OCI artifacts under the monorepo `ghcr.io/trianalab/pacto/*` namespace.

- operator image → `ghcr.io/trianalab/pacto/operator`
- operator chart → `ghcr.io/trianalab/pacto/charts/pacto-operator`
- dashboard image → `ghcr.io/trianalab/pacto/dashboard`
- dashboard contract bundle → `ghcr.io/trianalab/pacto/dashboard-contract`
- demo bundles already live under `ghcr.io/trianalab/pacto/*`

All packages are now created and owned by this repository. The chart name
`pacto-operator` and the Artifact Hub repository are preserved (re-point the AH
repository URL to the new chart coordinate). The previous coordinates remain as
historical — their already-published versions are unaffected. Go module paths
(`/v3`, `/v5`) are unchanged.
