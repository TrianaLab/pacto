<!--
  GENERATED FILE -- DO NOT EDIT.
  Produced by release/scripts/gen_integration_docs.py from integration.yaml (compatibility) + release/release-manifest.json.
  Regenerate with `make docs-generate`; drift is a CI failure (`make docs-check`).
-->

## Version compatibility

The Kubernetes integration is versioned independently from Pacto core. The table below is generated from `integration.yaml` and `release/release-manifest.json`.

| Integration artifact | Version | Supported Pacto core |
| --- | --- | --- |
| Operator image | `6.4.0` | `>=4.0.0` |
| Operator chart | `6.4.0` | `>=4.0.0` |
| Delivery image | `6.4.0` | `>=4.0.0` |
| Go module | `6.4.0` | `>=4.0.0` |
| Integration docs | `6.4.0` | `>=4.0.0` |

Contracts with `pactoVersion: "2.1"` need Pacto core 4.1.0 or later and operator 6.1.0 or later. Contracts with `pactoVersion: "2.2"` need Pacto core 4.3.0 or later and operator 6.3.0 or later. Contracts with `pactoVersion: "2.3"` need Pacto core 4.4.0 or later and operator 6.4.0 or later.

This documentation set corresponds to Pacto core `4.4.0`. The integration's own version (currently operator/chart `6.4.0`, docs `6.4.0`) advances on its own release cadence.

### Version selector

The site version selector (top of the page) tracks Pacto core releases. Because the Kubernetes integration ships on its own cadence, a Kubernetes-only release does NOT add a new core version entry to the selector: it republishes the current core version in place with regenerated integration docs, and this compatibility table shows the integration version those docs describe. Pick a core version from the selector to read the integration docs that shipped with it.
