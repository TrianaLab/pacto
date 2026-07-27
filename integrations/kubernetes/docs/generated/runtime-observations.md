<!--
  GENERATED FILE -- DO NOT EDIT.
  Produced by release/scripts/gen_integration_docs.py from internal/observer/runtime.go, pkg/evidence, pkg/finding, api/v1alpha1.
  Regenerate with `make docs-generate`; drift is a CI failure (`make docs-check`).
-->

# Runtime observations

The operator's collector reads Kubernetes resources and produces typed Evidence, which the Pacto engine evaluates into Findings and a contract status. This page is generated from the collector and the shared engine reasoning packages.

## Observation dimensions

Each dimension is observed only when the contract declares the corresponding section. Spec sections refer to the operator specification.

| Dimension | Spec section | Gated by |
| --- | --- | --- |
| Interfaces | 7.3 / B1 + B4 | `--interface-name-match-discovery` (optional positive-availability assist) |
| Dependencies | 7.6 | always on |
| Health | 7.4 | `--enable-probing` (Tier A active probe; passive Tier B otherwise) |
| Metrics | 7.5 | `--enable-metrics-observation` |
| Configurations | 7.7 | always on |
| Workload | 7.1 + AR7 | always on |
| Persistence | 7.2 / B3 | always on |

## Observation outcomes

Every observation carries an outcome. Non-`Observed` outcomes never fabricate a violation -- they resolve to an uncertainty (`Unknown`) finding.

| Outcome | Meaning |
| --- | --- |
| `Observed` | Collected successfully; a value is present. |
| `Unsupported` | The collector cannot observe this dimension (e.g. external Service, non-HTTP binding). |
| `Failed` | Collection was attempted and errored. |
| `Stale` | Last known data is too old to trust. |
| `Insufficient` | Partial/ambiguous, not conclusive (includes within-stabilization-window negatives). |

## Assertion kinds

The evidence kinds the collector emits, one per observed assertion:

- `CapabilityObserved`
- `ConfigurationPresent`
- `DependencyReachable`
- `InterfaceObserved`
- `PersistenceObserved`
- `WorkloadObserved`

## Contract status

The high-level `status.contractStatus` compliance state (contract fidelity, NOT runtime health). Enum values are generated from the `Pacto` API.

| Status | Meaning |
| --- | --- |
| `Compliant` | No error, unknown or warning findings. |
| `Warning` | Only warning-severity findings. |
| `NonCompliant` | At least one confirmed violation (error-severity finding). |
| `Reference` | Reference-only contract (no runtime target); parsed and validated, never observed. |
| `Unknown` | A required assertion could not be evaluated, or the contract could not be obtained transiently. |
| `Invalid` | Structural validation failed, or a malformed artifact could not be parsed (fail-closed). |
| `NotEvaluated` | Reserved enum value the operator does not currently emit; used by the engine dashboard for offline sources never runtime-evaluated. |

Precedence when summarizing findings (see `summarizeFindings` in `internal/controller/pacto_controller.go`): `Invalid` (structural) outranks all; then any error -> `NonCompliant`; else any unknown -> `Unknown`; else any warning -> `Warning`; else `Compliant`. Reference-only contracts short-circuit to `Reference`.

## Findings

Typed conclusions from the engine, grouped by severity family. Family 1 (confirmed violations, `RuntimeDrift`/`error`) requires conclusive contradicting evidence; family 2 (`Inconclusive`/`unknown`) captures evidence that could not confirm or refute. Generated from `pkg/finding/codes.go`.

### Severity `error`

| Code | Category |
| --- | --- |
| `CAPABILITY_ABSENT` | `RuntimeDrift` |
| `CAPABILITY_INTERFACE_UNKNOWN` | `InvalidCapability` |
| `CAPABILITY_PATH_INVALID` | `InvalidCapability` |
| `CAPABILITY_REF_INVALID` | `InvalidCapability` |
| `CAPABILITY_REF_REQUIRED` | `InvalidCapability` |
| `CONFIGURATION_ABSENT` | `RuntimeDrift` |
| `CONFIGURATION_MISMATCH` | `RuntimeDrift` |
| `CONFIG_VALUES_VALIDATION_FAILED` | `ConfigurationViolation` |
| `DEPENDENCY_UNREACHABLE` | `RuntimeDrift` |
| `DUPLICATE_CAPABILITY` | `DuplicateName` |
| `DUPLICATE_CONFIGURATION_NAME` | `DuplicateName` |
| `DUPLICATE_DEPENDENCY_NAME` | `DuplicateName` |
| `DUPLICATE_INTERFACE_NAME` | `DuplicateName` |
| `DUPLICATE_POLICY_NAME` | `DuplicateName` |
| `DUPLICATE_READINESS_ID` | `DuplicateName` |
| `EMPTY_COMPATIBILITY` | `InvalidDependency` |
| `EMPTY_READINESS_DESCRIPTION` | `MissingEvidence` |
| `EMPTY_READINESS_EVIDENCE` | `MissingEvidence` |
| `FILE_NOT_FOUND` | `InvalidFile` |
| `INTERFACE_ABSENT` | `RuntimeDrift` |
| `INTERFACE_REF_REQUIRED` | `InterfaceMismatch` |
| `INVALID_CAPABILITY_TYPE` | `InvalidCapability` |
| `INVALID_COMPATIBILITY` | `InvalidDependency` |
| `INVALID_CONFIG_JSON` | `InvalidFile` |
| `INVALID_CONFIG_REF` | `InvalidReference` |
| `INVALID_CONFIG_SCHEMA` | `SchemaViolation` |
| `INVALID_INTERFACE_SPEC` | `InvalidFile` |
| `INVALID_INTERFACE_TYPE` | `InterfaceMismatch` |
| `INVALID_OCI_REF` | `InvalidReference` |
| `INVALID_POLICY_JSON` | `InvalidFile` |
| `INVALID_POLICY_REF` | `InvalidReference` |
| `INVALID_POLICY_SCHEMA` | `SchemaViolation` |
| `INVALID_READINESS_EXPIRES` | `InvalidReadiness` |
| `INVALID_READINESS_REVISION` | `InvalidReadiness` |
| `INVALID_SEMVER` | `InvalidVersion` |
| `PERSISTENCE_MISMATCH` | `RuntimeDrift` |
| `POLICY_ENFORCEMENT_ERROR` | `PolicyViolation` |
| `POLICY_REF_CYCLE` | `ReferenceCycle` |
| `POLICY_REF_UNRESOLVED` | `UnresolvedReference` |
| `POLICY_VIOLATION` | `PolicyViolation` |
| `SCHEMA_ERROR` | `SchemaViolation` |
| `SCHEMA_VIOLATION` | `SchemaViolation` |
| `STATELESS_PERSISTENT_CONFLICT` | `StateMismatch` |
| `UNSUPPORTED_PACTO_VERSION` | `InvalidVersion` |
| `UNSUPPORTED_POLICY_TARGET` | `PolicyViolation` |
| `VALUES_WITHOUT_SCHEMA` | `MissingConfiguration` |
| `WORKLOAD_MISMATCH` | `RuntimeDrift` |
| `YAML_PARSE_ERROR` | `SchemaViolation` |

### Severity `warning`

| Code | Category |
| --- | --- |
| `POLICY_REF_NOT_ENFORCED` | `UnresolvedReference` |
| `TAG_NOT_DIGEST` | `InvalidReference` |

### Severity `unknown`

| Code | Category |
| --- | --- |
| `COLLECTION_FAILED` | `Inconclusive` |
| `EVIDENCE_INSUFFICIENT` | `Inconclusive` |
| `EVIDENCE_MISSING` | `Inconclusive` |
| `EVIDENCE_STALE` | `Inconclusive` |
| `EXTENSION_EVALUATOR_UNAVAILABLE` | `Inconclusive` |
| `OBSERVATION_UNSUPPORTED` | `Inconclusive` |
