---
"@pacto/demo-bundles": patch
"@pacto/dashboard-contract-bundle": patch
---

Publish the demo bundles to monorepo-owned OCI coordinates.

The demo bundles previously targeted `ghcr.io/trianalab/pacto-demo/*`, packages
owned by the old `pacto-demo` repository that the monorepo cannot write. They now
publish to `ghcr.io/trianalab/pacto/*`, created and owned by this repo. This also
re-cuts the dashboard contract bundle (its publisher now installs the pacto CLI +
plugins). The core fixed group advances one patch.
