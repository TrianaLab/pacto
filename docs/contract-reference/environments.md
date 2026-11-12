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
    file: environments/staging.yaml
  - name: production
    file: environments/production.yaml
```

| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `name` | string | Yes | Lowercase alphanumeric and hyphens, at most 63 characters. Unique in the contract (`DUPLICATE_ENVIRONMENT_NAME` otherwise) |
| `file` | string | Yes | Path to a YAML or JSON file, from the bundle root, with no leading `/` or `./` and no `..` (`FILE_NOT_FOUND` otherwise, or when the file is missing) |
| `lifetime` | string | No | `temporary` or `persistent`. Needs `pactoVersion: "2.4"`. Absent reads "not declared", never persistent |
| `expires` | string | No | An RFC 3339 time, such as `2026-11-20T00:00:00Z`, and only with `lifetime: temporary` (`INVALID_ENVIRONMENT_EXPIRES`, `ENVIRONMENT_EXPIRES_NOT_TEMPORARY` otherwise). Needs `pactoVersion: "2.4"` |
| `owner` | object | No | Who owns this environment when it is not the service owner, in the shape of [`service.owner`](sections.md#service) (`team`, `dri`, `contacts`). Needs `pactoVersion: "2.4"` |

A contract older than 2.4 that sets `lifetime`, `expires` or `owner` fails
with `NEEDS_PACTO_2_4` at that field.

The file maps each configuration name to the values that environment uses for
it:

```yaml
# environments/production.yaml
app:
  LOG_LEVEL: warn
chart:
  replicaCount: 5
  ingress:
    host: shop.example.com
```

The `-f`/`--values` and `--set` flags change the contract itself; an
environment's values live in its file.

The file is part of the bundle, so `pacto pack` and `pacto push` ship it with
the contract. A [`.pactoignore`](../pactoignore.md) entry that hides it makes
validation report `FILE_NOT_FOUND`.

### Temporary environments

A preview or pull-request environment lives hours or days. Declare it
`temporary`, give it an `expires` time and, when another team runs it, an
`owner`:

```yaml
pactoVersion: "2.4"
environments:
  - name: production
    file: environments/production.yaml
    lifetime: persistent
  - name: pr-482
    file: environments/pr-482.yaml
    lifetime: temporary
    expires: "2026-11-20T00:00:00Z"
    owner: {team: storefront-qa}
```

Once `expires` is at or before the clock of the run, `pacto validate` and
`pacto_check` add an `ENVIRONMENT_EXPIRED` warning to the `environments` stage:

```console
WARN  [ENVIRONMENT_EXPIRED] environments[1].expires: environment pr-482 expired at 2026-11-20T00:00:00Z; nothing was deleted: remove it with pacto_edit remove_environments or move expires
```

The verdict stays the same. Pacto deletes nothing for an expired environment:
no contract entry, file, Stage or resource. The fleet and the dashboard's
service page show it as expired (inferred), as of the time of their answer. An
environment with no `owner` shows the service owner, labelled inferred.

### Create, list, compare and retire

| Task | Command |
|---|---|
| Create a service with environments | `pacto init --delivery --env test,pr-482` declares each environment with an empty values file and writes a Stage for each |
| Add environments to a contract | `pacto_edit` with `add_environments`, such as `[{"name":"pr-482","lifetime":"temporary","expires":"2026-11-20T00:00:00Z"}]` |
| Retire an environment | `pacto_edit` with `remove_environments`, such as `["pr-482"]`. A file no other environment names is deleted. Nothing in a cluster changes |
| List a service's environments | `pacto fleet entity service <name>`, `pacto_fleet_entity` or the [service page](../dashboard-service-page.md#environments) |
| Compare two environments | `pacto explain <bundle> --environments production,pr-482` or `pacto_check` with `environments` |
| See the Stages that deliver it | `pacto delivery status --service <name>` |

`pacto explain --environments` lists only the values the environments disagree
on:

```console
Differences between production, pr-482 (1):
  chart.replicaCount
    production  6
    pr-482      1
```

When a Stage is created or deleted, the delivery promoter emits a Kubernetes
Event (`EnvironmentCreated`, `EnvironmentDeleted`) and the recorder posts a
CDEvent (`environment.created`, `environment.deleted`); see
[delivery setup](../integrations/kubernetes/delivery-setup.md) and
[delivery records](../integrations/kubernetes/delivery-records.md). Each names
one Stage; other Stages for the same environment are unaffected.

### How values are checked

For each environment, and for each configuration its file names, Pacto merges
the environment's values over the configuration's default `values` and checks
the result against the configuration's `schema`. Objects merge key by key, and
the environment's value wins. A `null` removes its key from the merged values,
at any depth, so an environment can drop a default:

```yaml
# environments/staging.yaml
chart:
  ingress:
    className: null  # staging uses the cluster's default ingress class
```

A configuration named with no values, such as `app:`, keeps its defaults. A
`required: true` configuration that the file leaves out must pass its schema
with its defaults alone. A configuration that is optional and left out is not
checked.

| Condition | Code |
|---|---|
| The file names a configuration the contract does not declare | `ENVIRONMENT_UNKNOWN_CONFIGURATION` |
| The merged values fail the configuration's schema, or the file does not parse, is not a mapping of configuration names or has a key that is not a string | `ENVIRONMENT_VALUES_INVALID` |
| A required configuration has no values in the file and its defaults fail its schema | `ENVIRONMENT_VALUES_MISSING` |
| The configuration names its schema by `ref` and the referenced contract cannot be resolved, or there are no bundle files to read. A resolved `ref` has its values checked against the referenced contract's configuration schema like a local one | `ENVIRONMENT_VALUES_UNVERIFIED` (warning) |

Each error names the environment, the configuration and the JSON Pointer of
the value, and points at its line in the environment's file. A key that holds a
dot or a slash, such as `prometheus.io/scrape`, is one step of the pointer, with
`/` written as `~1` and `~` as `~0`. The checks run in the `environments`
stage of [`pacto validate`](validation.md#environments-stage).

Values hold references to secrets, such as a Secret or ExternalSecret name.
Keep secret material out of the environment files, as you keep it out of the
contract; see [Secret references](configuration-and-policy.md#secret-references).

### Catch a chart change that breaks an environment

A Helm chart's `values.schema.json` is a JSON Schema, so you can reference it as
a configuration and keep one file per environment. Version 1.4.0 of the
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
    file: environments/staging.yaml
  - name: production
    file: environments/production.yaml
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
SKIP lock               no pacto.lock
PASS structural
PASS cross-field
PASS policy
SKIP configuration-refs no configuration references
FAIL environments
  web-1.5.0/environments/production.yaml:3:3: ERROR [ENVIRONMENT_VALUES_INVALID] environments[1].file: environment production: configuration chart at /chart/ingress: missing property 'host'
SKIP guarantees         no dependency declares requires
SKIP readiness          not requested (--readiness)
SKIP closure            not requested (--closure)
Next: fix environments[1].file at web-1.5.0/environments/production.yaml:3:3 [ENVIRONMENT_VALUES_INVALID], then run pacto validate web-1.5.0
validation failed with 1 error(s)
```

The command exits 1. Add the host to the production file and run
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
  [NON_BREAKING] environments[production].chart.ingress.host (added): environments[production].chart.ingress.host added [+ shop.example.com]
breaking changes detected
```

### What a change needs

[`pacto diff`](diff.md) compares environments by name:

| Change | Classification |
|---|---|
| An environment added | Non-breaking: it only adds values to check |
| An environment removed | Potentially breaking: the environment may still run the service |
| An environment's `file`, `lifetime`, `expires` or `owner` added, removed or changed, at `environments[<name>].<field>` | Non-breaking: they describe this service's own environment, and no consumer reads them |
| A value in an environment's file added, removed or changed, at `environments[<name>].<configuration>.<path>` | Non-breaking: the values are this service's own inputs, and only the service reads them |

### Environments and Stages

When [delivery](../integrations/kubernetes/delivery.md) promotes a version, it
matches the Stage's `spec.environment` against the environment names the
version's contract declares. The `pacto.environment` check runs the same
`environments` stage as `pacto validate`, on the version's own bundle:

| The version's contract | The promote |
|---|---|
| Declares no `environments` | Goes on. The check says it does not apply |
| Declares environments, but none named like the Stage's `spec.environment` | Waits for the service's team to approve it |
| Declares that environment, and its values fail with an error from the table above | Waits for the service's team to approve it, with each error located in the file |
| Declares that environment, and its values are `ENVIRONMENT_VALUES_UNVERIFIED` | Waits for a person to [accept the unknown](../glossary.md#accept-the-unknown) |
| Declares that environment, and its values pass | Goes on |

Name each environment after the `spec.environment` of the Stages that deliver
the service there, such as `staging` and `production`. `pacto init --delivery --env` and
`pacto_create` with `delivery` declare each environment in the contract, with
an empty values file, beside the Stage they write for it, so the names match
from the start.
