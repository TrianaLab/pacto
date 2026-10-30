---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

Add `pactoVersion: "2.3"` and `environments`, so `pacto validate` checks the
configuration values each environment uses against the schema of every
configuration they name. Every 2.2 contract is valid under 2.3. A 2.3 contract
needs Pacto core 4.4.0 or later and operator 6.4.0 or later. A `"2.0"`, `"2.1"`
or `"2.2"` contract that declares `environments` reports `NEEDS_PACTO_2_3`.

```yaml
pactoVersion: "2.3"
configurations:
  - name: chart
    schema: chart/values.schema.json
    required: true
environments:
  - name: staging
    values: environments/staging.yaml
  - name: production
    values: environments/production.yaml
```

A values file maps each configuration name to that environment's values, for
example `chart: {replicaCount: 5, ingress: {host: shop.example.com}}`. It ships
in the bundle like any other referenced file. Values hold references to
secrets, never secret material.

- `pacto validate` reports a duplicate environment name
  (`DUPLICATE_ENVIRONMENT_NAME`) and a missing values file (`FILE_NOT_FOUND`).
  A new `environments` stage, after `policy`, merges each environment's values
  over the configuration's default `values` and checks the result against its
  schema (`ENVIRONMENT_VALUES_INVALID`). It reports values for a configuration
  the contract does not declare (`ENVIRONMENT_UNKNOWN_CONFIGURATION`) and a
  required configuration whose defaults alone fail its schema when an
  environment omits it (`ENVIRONMENT_VALUES_MISSING`). Each error names the
  environment, the configuration and the JSON path, and carries the values file
  with its line and column. A configuration whose schema is behind a `ref` is
  `ENVIRONMENT_VALUES_UNVERIFIED`, a warning, and the stage is skipped with a
  reason naming it. Without `environments` the stage is skipped.
- `pacto diff` reports an environment added as non-breaking, one removed as
  potentially breaking and every changed value in its file as non-breaking.
- A validation error carries `file` when it points into a file other than
  `pacto.yaml`. The text output of `pacto validate` widens its stage column by
  one character.
