# Environments

`environments` lists the environments a service runs in and the configuration
values each one uses. `pacto validate` checks those values against the schema of
every [configuration](configuration-and-policy.md#configurations) they name, on
every change. Pacto applies nothing and deploys nothing. The other sections are
in [Contract sections](sections.md) and
[Configuration and policy](configuration-and-policy.md).

## `environments`

Optional. Needs `pactoVersion: "2.3"`: an older contract with it fails
validation with `NEEDS_PACTO_2_3`. 2.3 needs Pacto core 4.4.0 or later and
operator 6.4.0 or later; see [Versions](../installation.md#versions).

```yaml
pactoVersion: "2.3"
service:
  name: web
  version: 1.4.0
configurations:
  - name: app
    schema: configuration/schema.json
    required: true
    values:
      LOG_LEVEL: info
  - name: chart
    schema: chart/values.schema.json
    required: true
environments:
  - name: staging
    values: environments/staging.yaml
  - name: production
    values: environments/production.yaml
```

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `name` | string | Yes | Lowercase alphanumeric and hyphens, at most 63 characters. Unique in the contract (`DUPLICATE_ENVIRONMENT_NAME` otherwise) |
| `values` | string | Yes | Path to a YAML or JSON file in the bundle (`FILE_NOT_FOUND` when missing). The path stays inside the bundle |

The values file maps each configuration name to the values that environment
uses for it:

```yaml
# environments/production.yaml
app:
  LOG_LEVEL: warn
chart:
  replicaCount: 5
  ingress:
    host: shop.example.com
```

The file is part of the bundle, so `pacto pack` and `pacto push` ship it with
the contract. A [`.pactoignore`](../pactoignore.md) entry that hides it makes
validation report `FILE_NOT_FOUND`.

### How values are checked

For each environment, and for each configuration its file names, Pacto merges
the environment's values over the configuration's default `values` and checks
the result against the configuration's `schema`. Objects merge key by key, and
the environment's value wins. A `required: true` configuration that the file
leaves out must pass its schema with its defaults alone. A configuration that
is optional and left out is not checked.

| Condition | Code |
|---|---|
| The file names a configuration the contract does not declare | `ENVIRONMENT_UNKNOWN_CONFIGURATION` |
| The merged values fail the configuration's schema, or the file is not a mapping of configuration names | `ENVIRONMENT_VALUES_INVALID` |
| A required configuration has no values in the file and its defaults fail its schema | `ENVIRONMENT_VALUES_MISSING` |
| The configuration names its schema by `ref`, so the bundle holds no schema to check against | `ENVIRONMENT_VALUES_UNVERIFIED` (warning) |

Each error names the environment, the configuration and the JSON path, and
points at the line in the values file. The checks run in the `environments`
stage of [`pacto validate`](validation.md#environments-stage).

Values hold references to secrets, such as a Secret or ExternalSecret name.
Keep secret material out of the values files, as you keep it out of the
contract; see [Secret references](configuration-and-policy.md#secret-references).

### Catch a chart change that breaks an environment

A Helm chart's `values.schema.json` is a JSON Schema, so you can reference it as
a configuration and keep one values file per environment. Version 1.4.0 of the
`web` service ships this chart schema:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "properties": {
    "replicaCount": {"type": "integer", "minimum": 1},
    "ingress": {
      "type": "object",
      "properties": {"host": {"type": "string"}}
    }
  }
}
```

The contract references it and lists two environments:

```yaml
pactoVersion: "2.3"
service:
  name: web
  version: 1.4.0
  owner: {team: storefront}
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

Staging sets an ingress host. Production sets only a class name:

```yaml
# environments/staging.yaml
chart:
  replicaCount: 1
  ingress:
    host: staging.example.com
```

```yaml
# environments/production.yaml
chart:
  replicaCount: 5
  ingress:
    className: nginx
```

Version 1.5.0 updates the chart, and its schema makes `ingress.host` required.
`pacto validate` reports the environment it breaks before you deploy it:

```console
$ pacto validate web-1.5.0
web-1.5.0 is invalid
PASS load
SKIP lock         no pacto.lock
PASS structural
PASS cross-field
PASS policy
FAIL environments
  web-1.5.0/environments/production.yaml:3:3: ERROR [ENVIRONMENT_VALUES_INVALID] environments[1].values: environment production: configuration chart at /chart/ingress: missing property 'host'
SKIP guarantees   no dependency declares requires
SKIP readiness    not requested (--readiness)
SKIP closure      not requested (--closure)
Next: fix environments[1].values at web-1.5.0/environments/production.yaml:3:3 [ENVIRONMENT_VALUES_INVALID], then run pacto validate web-1.5.0
validation failed with 1 error(s)
```

The command exits 1. Add the host to the production values file and run
`pacto validate` again. `pacto diff` then lists the schema change and the new
value:

```console
$ pacto diff web-1.4.0 web-1.5.0
Classification: BREAKING
Required bump: major
Changes (4):
  [NON_BREAKING] service.version (modified): service.version modified [1.4.0 -> 1.5.0]
  [POTENTIAL_BREAKING] schema.properties.ingress.properties.className (added): schema.properties.ingress.properties.className added [+ map[type:string]]
  [BREAKING] schema.properties.ingress.required (added): schema.properties.ingress.required added [+ [host]]
  [NON_BREAKING] environments[production].values.chart.ingress.host (added): environments[production].values.chart.ingress.host added [+ shop.example.com]
breaking changes detected
```

### What a change needs

[`pacto diff`](diff.md) compares environments by name:

| Change | Classification |
|---|---|
| An environment added | Non-breaking: it only adds values to check |
| An environment removed | Potentially breaking: the environment may still run the service |
| A value in an environment's file added, removed or changed | Non-breaking: the values are this service's own inputs, and no consumer reads them |
