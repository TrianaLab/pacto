---
"@pacto/core": patch
---

Publish the demo bundles to monorepo-owned OCI coordinates.

The demo bundles previously targeted `ghcr.io/trianalab/pacto-demo/*`, packages
owned by the old `pacto-demo` repository that the monorepo cannot write. They now
publish to `ghcr.io/trianalab/pacto/*`, created and owned by this repo. This also
re-cuts the dashboard contract bundle (its publisher now installs the pacto CLI +
plugins) and folds in the cel-go 0.29.0 bump. The core fixed group advances one
patch (core, cli, dashboard-image, demo-bundles, dashboard-contract-bundle).
