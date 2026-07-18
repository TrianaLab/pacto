# Validation layers

Pacto validates contracts through four successive layers. Each layer short-circuits — if it fails, subsequent layers are skipped.

## Layer 1: Structural (JSON Schema)

Validates against the embedded JSON Schema:
- Field types match
- Required fields are present
- Enum values are valid
- Conditional requirements are met (`http` needs `port`, etc.)
- State invariants are enforced (`stateless` needs `ephemeral`)

## Layer 2: Cross-field

Validates semantic references and consistency:

| Rule | Code |
|---|---|
| `service.version` is valid semver | `INVALID_SEMVER` |
| Interface names are unique | `DUPLICATE_INTERFACE_NAME` |
| Configuration names are unique | `DUPLICATE_CONFIGURATION_NAME` |
| Policy names are unique | `DUPLICATE_POLICY_NAME` |
| Dependency names are unique | `DUPLICATE_DEPENDENCY_NAME` |
| `http`/`grpc` interfaces have `port` | `PORT_REQUIRED` |
| `event` interfaces with `port` set | `PORT_IGNORED` (warning) |
| `grpc`/`event` interfaces have `contract` | `CONTRACT_REQUIRED` |
| `health.interface` matches a declared interface | `HEALTH_INTERFACE_NOT_FOUND` |
| Health interface is not `event` type | `HEALTH_INTERFACE_INVALID` |
| `health.path` required for `http` health interface | `HEALTH_PATH_REQUIRED` |
| `health.path` on `grpc` health interface is ignored | `HEALTH_PATH_IGNORED` (warning) |
| `metrics.interface` matches a declared interface | `METRICS_INTERFACE_NOT_FOUND` |
| Metrics interface is not `event` type | `METRICS_INTERFACE_INVALID` |
| `metrics.path` required for `http` metrics interface | `METRICS_PATH_REQUIRED` |
| `metrics.path` on `grpc` metrics interface is ignored | `METRICS_PATH_IGNORED` (warning) |
| Referenced files exist in the bundle | `FILE_NOT_FOUND` |
| OCI dependency refs (`oci://`) are valid OCI references | `INVALID_OCI_REF` |
| Compatibility ranges are valid semver constraints | `INVALID_COMPATIBILITY` |
| Compatibility range is empty | `EMPTY_COMPATIBILITY` |
| OCI dependency uses tag instead of digest | `TAG_NOT_DIGEST` (warning) |
| `image.ref` is a valid OCI reference | `INVALID_IMAGE_REF` |
| `chart.ref` (OCI) is a valid OCI reference | `INVALID_CHART_REF` |
| `chart.version` is valid semver | `INVALID_CHART_VERSION` |
| `configurations[].ref` is not a valid OCI reference | `INVALID_CONFIG_REF` |
| `configurations[].values` without a schema | `VALUES_WITHOUT_SCHEMA` |
| `readiness.checks[].id` are unique within the contract | `DUPLICATE_READINESS_ID` |
| `readiness.checks[].evidence` is not blank/whitespace | `EMPTY_READINESS_EVIDENCE` |
| `readiness.checks[].description` (when present) is not blank | `EMPTY_READINESS_DESCRIPTION` |
| `readiness.expires` is a strict `YYYY-MM-DD` date | `INVALID_READINESS_EXPIRES` |
| `readiness.history[].{date,version,author,description}` are valid/non-blank | `INVALID_READINESS_REVISION` |
| `configurations[].schema` file is not valid JSON | `INVALID_CONFIG_JSON` |
| `configurations[].schema` file is not valid JSON Schema | `INVALID_CONFIG_SCHEMA` |
| `configurations[].values` don't match the schema | `CONFIG_VALUES_VALIDATION_FAILED` |
| `policies[].schema` file does not exist in the bundle | `FILE_NOT_FOUND` |
| `policies[].schema` file is not valid JSON | `INVALID_POLICY_JSON` |
| `policies[].schema` file is not valid JSON Schema | `INVALID_POLICY_SCHEMA` |
| `policies[].ref` is not a valid OCI reference | `INVALID_POLICY_REF` |
| `interfaces[].contract` (YAML) file is not valid YAML | `INVALID_CONTRACT_FILE` |
| `scaling.min` <= `scaling.max` | `SCALING_MIN_EXCEEDS_MAX` |
| Job workloads cannot have scaling | `JOB_SCALING_NOT_ALLOWED` |
| Stateless + persistent is invalid | `STATELESS_PERSISTENT_CONFLICT` |

## Layer 3: Semantic

Validates cross-concern consistency:

| Rule | Code | Type |
|---|---|---|
| `ordered` upgrade strategy with `stateless` state | `UPGRADE_STRATEGY_STATE_MISMATCH` | Warning |

## Layer 4: Policy enforcement

Resolves and enforces all declared policies against the contract. Policies are applied with strict **AND semantics** — the contract must satisfy every resolved policy. Contradictory policies naturally fail; no precedence or override logic is applied.

| Condition | Code |
|---|---|
| `policies[].ref` cannot be resolved (resolver unavailable, network error, bundle not found) | `POLICY_REF_UNRESOLVED` |
| Cycle detected in recursive policy resolution chain | `POLICY_REF_CYCLE` |
| Contract violates a policy constraint | `POLICY_VIOLATION` |
| Contract YAML cannot be parsed for policy validation | `POLICY_ENFORCEMENT_ERROR` |

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

