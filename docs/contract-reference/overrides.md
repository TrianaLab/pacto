# Contract overrides

Overrides change contract values at the command line without editing `pacto.yaml`, the same way Helm values work:

```bash
pacto validate . --set service.version=2.0.0 -f values-prod.yaml
```

Overrides are available on all commands that take a contract reference: `validate`, `explain`, `doc`, `generate`, `graph`, `pack`, `push` and `lock`. The two commands that take a pair of contracts (`diff`, `impact`) use the prefixed flags in [Diff overrides](#diff-overrides). See the [CLI reference](../cli-reference.md) for the complete command and flag listing.

## Override flags

| Flag | Short | Description |
|------|-------|-------------|
| `--values <file>` | `-f` | Merge a YAML values file into the contract |
| `--set <key=value>` | | Set an individual value using dot-notation |

!!! note
    On `pacto push`, `-f` is the shorthand for `--force`, so spell out `--values`.

## Precedence

Overrides follow the same precedence logic as Helm (lowest to highest):

1. Base `pacto.yaml`
2. Values files (`-f`), in the order they are specified
3. `--set` values

Later sources override earlier ones. Multiple `-f` flags apply in order, and the last file wins for any conflicting key.

## `--set` type inference

`--set key=value` infers the value's type from the string, trying in order:
integer (`int64`) → float (`float64`) → boolean → string. The first parse that
succeeds wins, and a value that matches none stays a string. For example:

- `--set readiness.minScore=80` → integer `80`; `--set ratio=1.5` → float `1.5`.
- `--set flag=true` → boolean `true` (but `--set flag=1` → integer `1`).
- A multi-dot semver like `--set service.version=1.0.0` has two dots, so it matches
  none of the numeric or boolean types and **stays a string**.
  A two-segment value like `1.0` parses as the float `1.0`. Set values that must
  stay strings in a `-f` values file with explicit YAML typing.

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
pacto validate my-service --set interfaces[0].visibility=internal
```

[Configurations as composable claims](../patterns/index.md#configurations-as-composable-claims) shows a worked per-environment layout: one values file per environment, the array-replacement caveat and this precedence rule applied end to end.

## Diff overrides

`diff` and `impact` each take two contract references, an old one and a new one. To override each side independently, use these prefixed flags:

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

# The same four flags work on impact
pacto impact old-service new-service --new-set service.version=2.0.0
```

## Schema validation

Every command validates the contract against the Pacto JSON Schema after it applies overrides, so every command rejects invalid enum values, unknown fields and type mismatches.

```bash
# Rejected: "invalid" is not a valid enum value for state.type
pacto explain my-service --set state.type=invalid

# Rejected: "unknownField" is not defined in the schema
pacto doc my-service --set service.unknownField=value
```
