# Validation layers

`pacto validate` runs three successive layers over a contract, and so does
anything else that has to trust one: `pack` and `push` both refuse before they
write or transmit a byte, and the operator revalidates every bundle it loads.
Each layer short-circuits — if it fails, the layers after it are skipped, which
is why one broken field can hide the rest.

## Layer 1: Structural (JSON Schema)

Validates against the embedded `pacto-v2.0.schema.json`:

- Field types match
- Required fields are present (including the mandatory `required` on `configurations[]` and `dependencies[]`)
- Enum values are valid (e.g. interface `type` is `openapi`/`asyncapi`/`grpc`, `capabilities[].type` is `health`/`metrics`/`extension`, `policies[].target` is `contract`)
- Fields that must carry a value are non-empty (e.g. `interfaces[].ref`, `dependencies[].compatibility`)
- Discriminated shapes hold — a `configurations[]` entry has exactly one of `schema`/`ref`; a `capabilities[]` `extension` requires a namespaced `ref` while `health`/`metrics` must not set `ref`
- State invariants are enforced (`stateless` requires `ephemeral` durability)

Structural failures report `SCHEMA_VIOLATION`.

A contract that cannot be **loaded** at all — malformed YAML, an unknown field, or a
`pactoVersion` other than `"2.0"` — fails before any layer runs and reports
`PARSE_ERROR` instead. Loading is the parse step that turns `pacto.yaml` into a
contract; validation only starts once it succeeds.

## Layer 2: Cross-field

Validates semantic references and consistency:

| Rule | Code |
|---|---|
| `service.version` [parses as semver](sections.md#service) — leniently: `1`, `0.1` and `v0.1` all pass | `INVALID_SEMVER` |
| Interface names are unique | `DUPLICATE_INTERFACE_NAME` |
| Configuration names are unique | `DUPLICATE_CONFIGURATION_NAME` |
| Policy names are unique | `DUPLICATE_POLICY_NAME` |
| Dependency names are unique | `DUPLICATE_DEPENDENCY_NAME` |
| Interface spec file exists in the bundle | `FILE_NOT_FOUND` |
| Interface spec file parses as JSON/YAML | `INVALID_INTERFACE_SPEC` |
| `extension` capability `ref` is well-formed | `CAPABILITY_REF_INVALID` |
| Standard capability type is not declared twice | `DUPLICATE_CAPABILITY` |
| `capabilities[].binding.interface` matches a declared interface | `CAPABILITY_INTERFACE_UNKNOWN` |
| `capabilities[].binding.path` is a safe application path | `CAPABILITY_PATH_INVALID` |
| OCI dependency refs (`oci://`) are valid OCI references | `INVALID_OCI_REF` |
| Compatibility ranges are valid semver constraints | `INVALID_COMPATIBILITY` |
| OCI dependency uses tag instead of digest | `TAG_NOT_DIGEST` (warning) |
| `configurations[].ref` is not a valid OCI reference | `INVALID_CONFIG_REF` |
| `configurations[].schema` file does not exist in the bundle | `FILE_NOT_FOUND` |
| `configurations[].schema` file is not valid JSON | `INVALID_CONFIG_JSON` |
| `configurations[].schema` file is not valid JSON Schema | `INVALID_CONFIG_SCHEMA` |
| `configurations[].values` don't match the schema | `CONFIG_VALUES_VALIDATION_FAILED` |
| `policies[].schema` file does not exist in the bundle | `FILE_NOT_FOUND` |
| `policies[].schema` file is not valid JSON | `INVALID_POLICY_JSON` |
| `policies[].schema` file is not valid JSON Schema | `INVALID_POLICY_SCHEMA` |
| `policies[].ref` is not a valid OCI reference | `INVALID_POLICY_REF` |
| `readiness.claims[].id` are unique within the contract | `DUPLICATE_READINESS_ID` |
| `readiness.claims[].evidence` is not blank/whitespace | `EMPTY_READINESS_EVIDENCE` |
| `readiness.claims[].description` (when present) is not blank | `EMPTY_READINESS_DESCRIPTION` |
| `readiness.expires` is a strict `YYYY-MM-DD` date | `INVALID_READINESS_EXPIRES` |
| `readiness.history[].{date,version,author,description}` are valid/non-blank | `INVALID_READINESS_REVISION` |

This table lists only what you can actually see. Where the JSON Schema already
constrains a field — the interface and capability `type` enums, a non-empty
`interfaces[].ref` or `dependencies[].compatibility`, the `configurations[]`
`schema`/`ref` choice, `policies[].target`, the stateless/ephemeral invariant —
Layer 1 rejects the contract first and reports `SCHEMA_VIOLATION`. Layer 2 has
its own check for each of those, but no contract survives Layer 1 to reach it,
so do not build a CI rule on a code that is not in this table.

## Layer 3: Policy enforcement

Resolves and enforces all declared policies against the contract. Policies are applied with strict **AND semantics** — the contract must satisfy every resolved policy. Contradictory policies naturally fail; no precedence or override logic is applied.

| Condition | Code |
|---|---|
| `policies[].ref` cannot be resolved (resolver unavailable, network error, bundle not found) | `POLICY_REF_UNRESOLVED` |
| Cycle detected in recursive policy resolution chain | `POLICY_REF_CYCLE` |
| Contract violates a policy constraint | `POLICY_VIOLATION` |

`POLICY_REF_UNRESOLVED` is a **hard error** — validation fails closed when a referenced policy cannot be resolved. This ensures that missing or unreachable policies are never silently skipped.

**When ref-based policies are resolved.** Recursive resolution of
`policies[].ref` happens only when a resolver is configured. `pacto validate` and
`pacto push` both supply one (they can reach OCI/file refs), so a `ref` policy
that cannot be fetched fails closed with `POLICY_REF_UNRESOLVED` — this is how
`pacto push` enforces remote policies before publishing. Local-only paths that
pass no resolver — `pacto pack` and the **operator** — enforce only the
locally-compiled `schema`-based policies and skip `ref` policies by design (they
never reach the unresolved error). Cycle detection is per chain: a `ref` is a
cycle only when it reappears in its own resolution chain (`A → B → A`); two
sibling policies pointing at the same `ref` resolve independently.

---

