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
    file: environments/staging.yaml
  - name: production
    file: environments/production.yaml
```

An environment's file maps each configuration name to that environment's
values, for example `chart: {replicaCount: 5, ingress: {host: shop.example.com}}`.
It ships in the bundle like any other referenced file. Values hold references
to secrets, such as a Secret name; keep secret material out of the file.

- `pacto validate` reports a duplicate environment name
  (`DUPLICATE_ENVIRONMENT_NAME`), and a file that is missing or whose path
  leaves the bundle (`FILE_NOT_FOUND`). A new `environments` stage, after
  `policy`, merges each environment's values over the configuration's default
  `values` and checks the result against its schema
  (`ENVIRONMENT_VALUES_INVALID`). A `null` in an environment's file removes
  that key from the merged values. The stage reports values for a
  configuration the contract does not declare
  (`ENVIRONMENT_UNKNOWN_CONFIGURATION`) and a required configuration whose
  defaults alone fail its schema when an environment omits it
  (`ENVIRONMENT_VALUES_MISSING`). Each error names the environment, the
  configuration and the JSON Pointer of the value, and carries the
  environment's file. A schema violation and an unknown configuration also
  carry the line and column of the value, and a file that does not parse
  carries the line the parser names. A configuration whose schema is behind a
  `ref`, or a run with no bundle files, reports `ENVIRONMENT_VALUES_UNVERIFIED`,
  a warning, and the stage is skipped with a reason naming it. Without
  `environments` the stage is skipped.
- `pack`, `push`, `publish`, the operator, the dashboard and `pacto_check` run
  the environments stage too, so a version whose values break an environment
  is refused there as well.
- `pacto diff` reports an environment added as non-breaking, one removed as
  potentially breaking and every changed value in its file as non-breaking, at
  `environments[<name>].<configuration>.<path>`.
- `pacto explain` and the MCP contract summary list the environments.
- A validation error carries `file` when it points into a file other than
  `pacto.yaml`, in `pacto validate` and in `pacto_check`. The text output of
  `pacto validate` widens its stage column by one character.
