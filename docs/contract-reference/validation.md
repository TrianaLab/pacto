# Validation layers

`pacto validate` tells you whether a contract is well-formed before anyone
relies on it. Run it in the bundle directory:

```bash
pacto validate .
```

It runs three checks in order, and so does anything else that has to trust a
contract. `pack` and `push` both refuse before they
write or transmit a byte, and the operator revalidates every bundle it loads.
Each layer short-circuits: if it fails, the layers after it are skipped, so one
broken field can hide the rest.

## Layer 1: Structural (JSON Schema)

Validates against the embedded schema of the contract's version, `pacto-v2.0.schema.json`, `pacto-v2.1.schema.json`, `pacto-v2.2.schema.json` or `pacto-v2.3.schema.json`:

- Field types match
- Required fields are present (including the mandatory `required` on `configurations[]` and `dependencies[]`)
- Enum values are valid (for example interface `type` is `openapi`/`asyncapi`/`grpc`/`mcp`, `capabilities[].type` is `health`/`metrics`/`extension`, `policies[].target` is `contract`)
- Fields that must carry a value are non-empty (for example `interfaces[].ref` and `checks[].file`)
- Discriminated shapes hold: a `configurations[]` entry has exactly one of `schema` or `ref`, a `capabilities[]` `extension` requires a namespaced `ref` and `health` or `metrics` sets no `ref`
- State invariants hold (`stateless` requires `ephemeral` durability)

Structural failures report `SCHEMA_VIOLATION`. A `"2.0"` contract that uses
`publish`, `checks`, an external dependency or an `mcp` interface reports `NEEDS_PACTO_2_1` for each
instead: set `pactoVersion: "2.1"`, which keeps every 2.0 field. A `"2.0"` or
`"2.1"` contract that uses an interface `lifecycle`, `compatibilityEpoch` or
`guarantees` or a dependency `requires` reports `NEEDS_PACTO_2_2` for each: set
`pactoVersion: "2.2"`. A `"2.0"`, `"2.1"` or `"2.2"` contract that declares
`environments` reports `NEEDS_PACTO_2_3`: set `pactoVersion: "2.3"`.

A contract that fails to **load**, because of malformed YAML, an unknown field or a
`pactoVersion` other than `"2.0"`, `"2.1"`, `"2.2"` or `"2.3"`, fails before any layer runs and reports
`PARSE_ERROR`. Loading is the parse step that turns `pacto.yaml` into a
contract. Validation starts once it succeeds.

## Layer 2: Cross-field

Validates semantic references and consistency:

| Rule | Code |
|---|---|
| `service.version` [parses as semver](sections.md#service), leniently: `1`, `0.1` and `v0.1` all pass | `INVALID_SEMVER` |
| Interface names are unique | `DUPLICATE_INTERFACE_NAME` |
| `interfaces[].lifecycle` dates are strict `YYYY-MM-DD` and `sunset` is not before `deprecated` | `INVALID_INTERFACE_LIFECYCLE` |
| `interfaces[].lifecycle.successor` names another interface or is an absolute URL | `INTERFACE_SUCCESSOR_UNKNOWN` |
| Configuration names are unique | `DUPLICATE_CONFIGURATION_NAME` |
| Policy names are unique | `DUPLICATE_POLICY_NAME` |
| Dependency names are unique | `DUPLICATE_DEPENDENCY_NAME` |
| Interface spec file exists in the bundle | `FILE_NOT_FOUND` |
| Interface spec file parses, if the ref ends `.json`, `.yaml` or `.yml` (for any other extension, only existence is checked) | `INVALID_INTERFACE_SPEC` |
| An `mcp` interface file lists tools, each with a name used once | `INVALID_MCP_TOOLS` |
| Check names are unique | `DUPLICATE_CHECK_NAME` |
| `checks[].file` exists in the bundle | `FILE_NOT_FOUND` |
| `extension` capability `ref` is well-formed | `CAPABILITY_REF_INVALID` |
| Each standard capability type is declared once | `DUPLICATE_CAPABILITY` |
| `capabilities[].binding.interface` matches a declared interface | `CAPABILITY_INTERFACE_UNKNOWN` |
| `capabilities[].binding.path` is a safe application path | `CAPABILITY_PATH_INVALID` |
| OCI dependency refs (`oci://`) are valid OCI references | `INVALID_OCI_REF` |
| A dependency without `ref` is `external` | `DEPENDENCY_REF_REQUIRED` |
| An external dependency without `ref` has a `version` | `EXTERNAL_VERSION_REQUIRED` |
| `compatibility` appears only with `ref`, and `version` only without it | `COMPATIBILITY_WITHOUT_REF`, `VERSION_WITH_REF` |
| A dependency with `ref` has a `compatibility` range | `EMPTY_COMPATIBILITY` |
| Compatibility ranges are valid semver constraints | `INVALID_COMPATIBILITY` |
| OCI dependency pins a digest | `TAG_NOT_DIGEST` (warning) |
| `configurations[].ref` is a valid OCI reference | `INVALID_CONFIG_REF` |
| `configurations[].schema` file exists in the bundle | `FILE_NOT_FOUND` |
| `configurations[].schema` file is valid JSON | `INVALID_CONFIG_JSON` |
| `configurations[].schema` file is valid JSON Schema | `INVALID_CONFIG_SCHEMA` |
| `configurations[].values` match the schema | `CONFIG_VALUES_VALIDATION_FAILED` |
| `policies[].schema` file exists in the bundle | `FILE_NOT_FOUND` |
| `policies[].schema` file is valid JSON | `INVALID_POLICY_JSON` |
| `policies[].schema` file is valid JSON Schema | `INVALID_POLICY_SCHEMA` |
| `policies[].ref` is a valid OCI reference | `INVALID_POLICY_REF` |
| Environment names are unique | `DUPLICATE_ENVIRONMENT_NAME` |
| `environments[].values` file exists in the bundle | `FILE_NOT_FOUND` |
| `readiness.claims[].id` are unique within the contract | `DUPLICATE_READINESS_ID` |
| `readiness.claims[].evidence` has non-whitespace content | `EMPTY_READINESS_EVIDENCE` |
| `readiness.claims[].description` (when present) has non-whitespace content | `EMPTY_READINESS_DESCRIPTION` |
| `readiness.expires` is a strict `YYYY-MM-DD` date | `INVALID_READINESS_EXPIRES` |
| `readiness.history[].{date,version,author,description}` are valid/non-blank | `INVALID_READINESS_REVISION` |

This table lists the codes you can see. Layer 1 rejects the contract first and
reports `SCHEMA_VIOLATION` wherever the JSON Schema already constrains a field. That covers the interface and capability `type` enums, a non-empty
`interfaces[].ref` or `dependencies[].compatibility`, the `configurations[]`
`schema`/`ref` choice, `policies[].target` and the stateless/ephemeral
invariant. Layer 2 has
its own check for each of those, but a contract that breaks them never gets past
Layer 1. Build CI rules only on codes in this table.

## Environments stage

After the policy layer, the `environments` stage checks each environment's
values file against the schema of every configuration it names. It runs only
when every stage before it passed, and reports `SKIP no environments declared`
for a contract without `environments`. [Environments](environments.md#how-values-are-checked)
lists the rules and the codes it reports: `ENVIRONMENT_UNKNOWN_CONFIGURATION`,
`ENVIRONMENT_VALUES_INVALID`, `ENVIRONMENT_VALUES_MISSING` and the
`ENVIRONMENT_VALUES_UNVERIFIED` warning.

An error in a values file carries that file in `file`, with `line` and `column`
pointing into it. The stage passes only when Pacto checked every value set and
found no error. A configuration whose schema is behind a `ref` leaves its values
unchecked, so a stage with no error and an unchecked value set reports
`SKIP could not verify:` and names each environment and configuration.

## Layer 3: Policy checks

Resolves every declared policy and checks the contract against it. Policies apply with strict **AND semantics**: the contract must satisfy all of them. Policies have no precedence or override logic, so contradictory policies fail.

| Condition | Code |
|---|---|
| `policies[].ref` cannot be resolved (resolver unavailable, network error, bundle not found) | `POLICY_REF_UNRESOLVED` |
| Cycle detected in recursive policy resolution chain | `POLICY_REF_CYCLE` |
| Contract violates a policy constraint | `POLICY_VIOLATION` |
| `policies[].ref` was skipped because no resolver was configured | `POLICY_REF_NOT_ENFORCED` (warning) |

`POLICY_REF_UNRESOLVED` is a **hard error**. Validation fails closed when a referenced policy cannot be resolved.

**When ref-based policies are resolved.** Recursive resolution of
`policies[].ref` happens only when a resolver is configured. `pacto validate` and
`pacto push` both supply one, since they can reach OCI/file refs. A `ref` policy
that cannot be fetched then fails closed with `POLICY_REF_UNRESOLVED`, which is
how `pacto push` checks remote policies before publishing. `pacto pack` and the **operator** pass no resolver. They check only the
locally compiled `schema`-based policies and skip `ref` policies, so they never
report the unresolved error. Cycle detection is per chain: a `ref` is a
cycle only when it reappears in its own resolution chain (`A → B → A`); two
sibling policies pointing at the same `ref` resolve independently.

### Dependency closure

By default Layer 3 checks the root contract only. `pacto validate --closure`
also checks every contract in the dependency closure against the policies the
root resolves. Each dependency's own `policies[]` is left to its owner's
validation. The closure resolves the same way `pacto graph` does, with one fetch
per dependency. External dependencies carry no contract and are skipped.

| Condition | Code |
|---|---|
| A dependency violates a policy the root resolves | `CLOSURE_POLICY_VIOLATION` |
| A dependency cannot be resolved, sits in a dependency cycle or has an unreadable contract | `CLOSURE_DEPENDENCY_UNRESOLVED` |
| A direct dependency declares a [deprecated interface](sections.md#interface-lifecycle) | `DEPENDENCY_INTERFACE_DEPRECATED` (warning) |
| That interface has reached its sunset | `DEPENDENCY_INTERFACE_SUNSET` (warning) |

A policy finding names the dependency in its path, for example
`dependencies[ledger@1.0.0]`. A dependency that cannot be resolved is named in
the message. When a dependency declared it, the path names that dependency. An
unresolvable dependency fails the run, so the check fails closed.

A deprecated interface is a warning that names the interface, its sunset and its
successor, with the path of the dependency that resolved to it, for example
`dependencies[1]`. Both codes stay warnings so the verdict does not change with
the date the check runs. To fail a pipeline after the sunset, gate on
`DEPENDENCY_INTERFACE_SUNSET` in `--output-format json`. When `pacto.lock` is present, the lock is verified first and
a drifted closure fails with the lock error before the closure is checked.

---
