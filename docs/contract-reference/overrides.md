# Contract overrides

Pacto supports a Helm-style override system that lets you modify contract values without editing `pacto.yaml` directly. Overrides are available on all commands that take a contract reference (`validate`, `explain`, `diff`, `doc`, `generate`, `graph`, `pack`, `push`, `lock`). See the [CLI reference](../cli-reference.md) for the complete command and flag listing.

## Override flags

| Flag | Short | Description |
|------|-------|-------------|
| `--values <file>` | `-f` | Merge a YAML values file into the contract |
| `--set <key=value>` | | Set an individual value using dot-notation |

!!! note
    The `-f` shorthand is not available on `pacto push` because `-f` is already used for `--force`.

## Precedence

Overrides follow the same precedence logic as Helm (lowest to highest):

1. Base `pacto.yaml`
2. Values files (`-f`), in the order they are specified
3. `--set` values

Later sources override earlier ones. Multiple `-f` flags are applied in order — the last file wins for any conflicting key.

## `--set` type inference

`--set key=value` infers the value's type from the string, trying in order:
integer (`int64`) → float (`float64`) → boolean → string. The first parse that
succeeds wins; anything that matches none stays a string. Consequences worth
knowing:

- `--set scaling.replicas=3` → integer `3`; `--set ratio=1.5` → float `1.5`.
- `--set flag=true` → boolean `true` (but `--set flag=1` → integer `1`, not boolean).
- A multi-dot semver like `--set service.version=1.0.0` has two dots, so it parses
  as none of the numeric/boolean types and **stays a string** (the desired result).
  A two-segment value like `1.0` parses as the float `1.0` — quote-sensitive values
  that must remain strings are better set via a `-f` values file with explicit YAML typing.

## Examples

```bash
# Override the service version
pacto validate my-service --set service.version=2.0.0

# Use a values file for environment-specific overrides
pacto validate my-service -f staging-values.yaml

# Combine both (--set takes precedence over -f)
pacto validate my-service -f staging-values.yaml --set service.version=3.0.0

# Set configuration values validated against the config schema
pacto validate my-service --set configurations[0].values.DB_HOST=localhost

# Set array elements using bracket notation
pacto validate my-service --set interfaces[0].port=9090
```

## Diff overrides

The `diff` command takes two contract references. To override each independently, use prefixed flags:

| Flag | Description |
|------|-------------|
| `--old-values <file>` | Values file for the old (first) contract |
| `--old-set <key=value>` | Set value for the old contract |
| `--new-values <file>` | Values file for the new (second) contract |
| `--new-set <key=value>` | Set value for the new contract |

```bash
# Override only the new contract
pacto diff oci://ghcr.io/acme/svc-pacto:1.0.0 my-service --new-set service.version=2.0.0

# Override both contracts
pacto diff old-service new-service \
  --old-values old-env.yaml \
  --new-values new-env.yaml \
  --new-set service.version=3.0.0
```

## Schema validation

All overrides are validated against the Pacto JSON Schema after they are applied. This means invalid enum values, unknown fields, and type mismatches are rejected by every command — not just `validate` and `push`.

```bash
# Rejected: "invalid" is not a valid enum value for runtime.state.type
pacto explain my-service --set runtime.state.type=invalid

# Rejected: "unknownField" is not defined in the schema
pacto doc my-service --set service.unknownField=value
```

## Environment-specific values files

A common pattern is to maintain per-environment values files that override configuration and scaling for each deployment target. The base `pacto.yaml` defines defaults, and each values file layers environment-specific settings on top.

```
my-service/
├── pacto.yaml
├── values/
│   ├── dev.yaml
│   ├── staging.yaml
│   └── production.yaml
└── configuration/
    └── schema.json
```

```yaml
# values/dev.yaml
configurations:
  - name: default
    values:
      DB_HOST: localhost
      DB_PORT: 5432
      DB_PASSWORD: dev-password
      LOG_LEVEL: debug
```

`staging.yaml` and `production.yaml` follow the same shape, differing only in host and secret reference. Apply one per command with `-f`:

```bash
pacto validate my-service -f values/dev.yaml
pacto push oci://ghcr.io/acme/my-service-pacto -p my-service --values values/production.yaml --set service.version=2.1.0
```

See [Developers — values files](../developers.md) for the full per-environment workflow.

## Configuration values validation

Overrides can set `configurations[].values` fields. These values are validated against the JSON Schema referenced by the corresponding `configurations[].schema`. If a value has the wrong type or is not defined in the schema, validation fails.

```yaml
# pacto.yaml
configurations:
  - name: default
    schema: configuration/schema.json
```

```bash
# This will fail if DB_PORT expects an integer but receives a string
pacto validate my-service --set configurations[0].values.DB_PORT=not-a-number
```

---

