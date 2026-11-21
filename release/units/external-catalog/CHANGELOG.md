# @pacto/external-catalog

## 4.6.1

No changes in this release.

## 4.6.0

### Minor Changes

- ca50460: pacto init finds the vendor APIs and managed cloud services a folder names
  (configuration, SDK manifests, Terraform and Crossplane) and records them as
  inferred external dependencies in pacto.inferred.yaml, each with every file and
  line that names it.
  
  - Sources: the workload environment, Helm values files, Compose `environment`,
    Kustomize `configMapGenerator`, `go.mod`, `package.json` with
    `package-lock.json`, `requirements*.txt`, `poetry.lock`, `pom.xml`,
    `gradle.lockfile`, Terraform `*.tf` resources and Crossplane objects. Only the
    host and port, file, line and key name are kept; Compose `env_file`,
    Terraform state, secret attributes and package registry URLs are never read.
  - SDK, Terraform and unconfirmed Crossplane matches are `possible`. The SDK
    major is recorded as the client version only when a lockfile or an exact
    range gives it. A managed resource gets its five-segment identity only when
    the folder shows its account and region, and otherwise names what it misses.
  - `pacto init` prints one `External dependencies: N (...), inferred` line,
    counts every input it did not read and takes `--external-catalog`
    (`PACTO_EXTERNAL_CATALOG`, also read by `pacto pack`, `pacto push` and
    `pacto explain`). The record lists each catalog digest under
    `inputs.catalog`. MCP `pacto_create` classifies with the server's catalog.
  - The fleet, `pacto fleet get external:<id>`, the MCP fleet tools and the
    dashboard show these externals as inferred edges of the new source kind
    `repository`, with the revision that named them. They never enter impact or
    a gate.
  - The external catalog format gains ecosystem-prefixed `sdkModules` (`npm:`,
    `pypi:`, `maven:`), and `terraform` and `crossplane` rows on `managed-cloud`
    entries; a sensitive Terraform id attribute is refused. The default catalog
    gains cited rows, so its digest changes.
- e250ce8: Every vendor API, managed cloud service and legacy host that a contract names
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
