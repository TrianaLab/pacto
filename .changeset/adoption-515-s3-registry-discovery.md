---
"@pacto/core": minor
"@pacto/k8s-module": patch
"@pacto/operator-chart": patch
---

Pacto finds golden paths and offerings in the registry they are published to.
`pacto fleet`, `pacto mcp`, `pacto tui`, `pacto export` and `pacto stats` take
`--discover <registry>[/<path>]` (repeatable), and `pacto dashboard --registry`
and the operator-managed dashboard (the chart's `registry`) discover under the
installation registry. Pacto lists `<registry>/pacto-index` first, then
`_catalog` under the path where the registry serves it, and keeps only
templates and offerings whose name matches their repository. On a registry
with no `_catalog`, such as GHCR, the answer is partial with
`REGISTRY_LISTING_INDEX_ONLY`, so an unlisted path reads unknown, never absent.
`pacto impact` and `pacto ci` ignore `--discover`.

Bundles pushed from this release on carry an artifactType and a kind
annotation, so new content gets a different manifest digest than an older
Pacto would give it. Tags and digests already published never move:
`pacto push`, `pacto publish`, the inferred-contract repair and the demo
publisher treat the older digest of the same content as identical.

The chart value `dashboard.repositories` and the operator flag
`--dashboard-repositories` are deprecated and still read: set `registry`.
