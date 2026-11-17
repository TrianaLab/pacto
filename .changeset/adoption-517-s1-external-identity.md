---
"@pacto/core": minor
"@pacto/k8s-module": minor
"@pacto/external-catalog": minor
---

Every vendor API, managed cloud service and legacy host that a contract names
is one graph node of the new kind `external`, keyed by a canonical identity such
as `stripe`, `aws/rds/123456789012/eu-west-1/orders-db` or
`host/corp-legacy.example`. A ref-less `external: true` dependency joins it
through its `identity`, else a catalog alias, else a hostname's registrable
domain, else its normalized name, so consumers write nothing new. Its
relationship carries `toExternal` with `resolved: false`; an external system
never gets a range status and never enters impact, dependents or delivery.

Each external record lists its sources with their own provenance and match, its
consumers and the catalog entry and digest that classified it. A snapshot lists
them in `externals` and the catalogs used in `externalCatalogs`, which every
answer's meta repeats. One answer names at most 200 external systems and folds
the rest into `other-egress`, partial with `EXTERNAL_CAP`. A restricted view
counts hidden consumers and hidden external systems and never names them.

`pacto fleet get`, `graph` and `explain` and the matching MCP tools take
`external:<identity>`; `pacto fleet entities --kind external --class <class>`
lists them and a text search adds matching `externals`. The TUI, the dashboard
Overview, a new External systems list, an external entity page with its
provenance and a distinct graph node show them. The contract graph labels a
broken reference "Unresolved ref" and an external system "External system".

Pacto embeds a default external catalog. `--external-catalog
oci://REPOSITORY@sha256:DIGEST` on `pacto fleet`, `impact`, `mcp`, `stats`,
`tui` and `dashboard` (or `PACTO_DASHBOARD_EXTERNAL_CATALOG`) merges an
organization catalog pinned by digest; a tag is refused and a catalog that
cannot be read makes the answer partial with `EXTERNAL_CATALOG_UNAVAILABLE`.
The operator chart value `dashboard.externalCatalog` sets it on the managed
dashboard through the new operator flag `--dashboard-external-catalog`.

pactoVersion 2.5 adds the optional `dependencies[].identity`, validated with
`INVALID_EXTERNAL_IDENTITY` and `IDENTITY_WITHOUT_EXTERNAL`.

Each core release publishes the default catalog to
`ghcr.io/trianalab/pacto/external-catalog:<core version>` and signs it with
Cosign; its digest equals the embedded copy's.
