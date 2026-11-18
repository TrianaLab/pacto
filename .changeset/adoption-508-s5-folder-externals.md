---
"@pacto/core": minor
"@pacto/external-catalog": minor
---

pacto init finds the vendor APIs and managed cloud services a folder names
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
