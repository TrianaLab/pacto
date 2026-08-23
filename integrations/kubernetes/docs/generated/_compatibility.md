<!--
  GENERATED FILE -- DO NOT EDIT.
  Produced by release/scripts/gen_integration_docs.py from integration.yaml (compatibility) + release/release-manifest.json.
  Regenerate with `make docs-generate`; drift is a CI failure (`make docs-check`).
-->

## Version compatibility

The Kubernetes integration is versioned independently from Pacto core. The table below is generated from `integration.yaml` and `release/release-manifest.json`.

| Integration artifact | Version | Supported Pacto core |
| --- | --- | --- |
| Operator image | `5.2.1` | `>=3.0.0` |
| Operator chart | `5.2.1` | `>=3.0.0` |
| Go module | `5.2.1` | `>=3.0.0` |
| Integration docs | `5.2.1` | `>=3.0.0` |

This documentation set corresponds to Pacto core `3.2.2`. The integration's own version (currently operator/chart `5.2.1`, docs `5.2.1`) advances on its own release cadence.

### Version selector

The site version selector (top of the page) tracks Pacto core releases. Because the Kubernetes integration ships on its own cadence, a Kubernetes-only release does NOT add a new core version entry to the selector: it republishes the current core version in place with regenerated integration docs, and this compatibility table shows the integration version those docs describe. Pick a core version from the selector to read the integration docs that shipped with it.
