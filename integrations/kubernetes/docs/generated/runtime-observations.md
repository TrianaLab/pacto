<!--
  GENERATED FILE -- DO NOT EDIT.
  Produced by release/scripts/gen_integration_docs.py from internal/observer/runtime.go, pkg/evidence, pkg/finding, pkg/runtimefacts, api/v1alpha1.
  Regenerate with `make docs-generate`; drift is a CI failure (`make docs-check`).
-->

# Runtime observations

What the operator looks at in your cluster, and every status and finding it can report. It gathers evidence (typed observations of what is running) from two sources, and the Pacto engine turns that evidence into findings and a contract status. The Kubernetes collector reads Kubernetes resources and always runs. The optional node agent watches TCP sockets in the Linux kernel with eBPF and publishes one `NodeRuntimeObservation` per node; the operator merges it into some dimensions when kernel evidence is on (`--enable-kernel-evidence`). See [Runtime conformance](runtime-conformance.md). This page is generated from the operator's observer and the shared engine packages.

## Observation dimensions

A dimension is one thing the operator checks, such as interfaces or dependencies. Each is observed only when the contract declares the matching section. Spec sections refer to the operator specification. A dash in the kernel column means the dimension comes from the Kubernetes API alone.

| Dimension | Spec section | Gated by | Kernel evidence |
| --- | --- | --- | --- |
| Interfaces | 7.3 / B1 + B4 | `--interface-name-match-discovery` (optional positive-availability assist); `--enable-probing` for the protocol fact | listeners on the ready backing pods |
| Dependencies | 7.6 | always on | outbound connections from the target pods |
| Health | 7.4 | `--enable-probing` (Tier A active probe; passive Tier B otherwise) | - |
| Metrics | 7.5 | `--enable-metrics-observation` | - |
| Configurations | 7.7 | always on | - |
| Workload | 7.1 + AR7 | always on | - |
| Persistence | 7.2 / B3 | always on | - |

The kernel source also reports one thing no dimension asks for: a successful connection to another Pacto-managed Service that no declared dependency resolves to, which the engine reports as `DEPENDENCY_UNDECLARED`. It reports none while a declared dependency resolves to no Pacto target by ref or name (a local or other non-OCI ref, or an OCI ref no Pacto carries), because that dependency could be the peer the pod reached.

## Collectors

Every observation names the collector that produced it in its provenance, and every finding copies that name into `evidenceRefs[].collector`.

| Collector | Meaning |
| --- | --- |
| `k8s-observer` | Kubernetes API only. |
| `k8s-observer+ebpf` | Kubernetes API result merged with node agent evidence that changed or qualified it. |
| `ebpf` | Node agent evidence only (an undeclared peer). |

## Observation outcomes

Every observation carries an outcome. Only `Observed` can lead to a violation; every other outcome becomes an `Unknown` finding instead.

| Outcome | Meaning |
| --- | --- |
| `Observed` | Collected successfully; a value is present. |
| `Unsupported` | The collector cannot observe this dimension (e.g. external Service, non-HTTP binding). |
| `Failed` | Collection was attempted and errored. |
| `Stale` | Last known data is too old to trust. |
| `Insufficient` | Partial/ambiguous, not conclusive (includes within-stabilization-window negatives). |

## Assertion kinds

An assertion is one claim of the contract the operator can check. These are the kinds of evidence it emits, one per assertion:

- `CapabilityObserved`
- `ConfigurationPresent`
- `DependencyReachable`
- `InterfaceObserved`
- `PersistenceObserved`
- `WorkloadObserved`

## Facts

Details an observation can carry beyond "it is there", such as the protocol a port answers with. An empty fact means not observed: the engine lists the check in `evaluationCoverage.unverified` and never treats it as a contradiction. Values are generated from `pkg/evidence/facts.go`.

| Fact | Value | Meaning |
| --- | --- | --- |
| `Protocol` | `http1` | the port answered with an HTTP/1.x status line |
| `Protocol` | `http2` | the port answered with an HTTP/2 SETTINGS frame |
| `Protocol` | `tls` | encrypted: the protocol inside cannot be read, so it proves nothing |
| `Protocol` | `other` | the port answered with bytes that are neither HTTP nor TLS |
| `Exposure` | `external` | reachable from outside the platform's private network |
| `Exposure` | `internal` | reachable from inside the platform's private network only |

## Contract status

`status.contractStatus` says whether the service matches its contract. It does not report pod health. Values are generated from the `Pacto` API.

| Status | Meaning |
| --- | --- |
| `Compliant` | No error, unknown or warning findings. |
| `Warning` | Only warning-severity findings. |
| `NonCompliant` | At least one confirmed violation (error-severity finding). |
| `Reference` | Reference-only contract (no runtime target); parsed and validated, never observed. |
| `Unknown` | A required assertion could not be evaluated, or the contract could not be obtained transiently. |
| `Invalid` | Structural validation failed, or a malformed artifact could not be parsed (fail-closed). |
| `NotEvaluated` | Reserved enum value the operator does not currently emit; used by the engine dashboard for offline sources never runtime-evaluated. |

Precedence when summarizing findings (see `summarizeFindings` in `internal/controller/status.go`): `Invalid` (structural) outranks all; then any error -> `NonCompliant`; else any unknown -> `Unknown`; else any warning -> `Warning`; else `Compliant`. Reference-only contracts short-circuit to `Reference`.

## Findings

A finding is one conclusion from the engine. Findings fall in two families. Confirmed violations (`RuntimeDrift`, `error`) need evidence that clearly contradicts the contract. Uncertainty findings (`Inconclusive`, `unknown`) mean the evidence could neither confirm nor refute it. A code listed under two severities is an error when the claim it reports on is `required` and a warning when it is optional, because an optional claim cannot make a contract non-compliant. Generated from `pkg/finding/codes.go`.

### Severity `error`

| Code | Category |
| --- | --- |
| `CAPABILITY_ABSENT` | `RuntimeDrift` |
| `CAPABILITY_INTERFACE_UNKNOWN` | `InvalidCapability` |
| `CAPABILITY_PATH_INVALID` | `InvalidCapability` |
| `CAPABILITY_REF_INVALID` | `InvalidCapability` |
| `CONFIGURATION_ABSENT` | `RuntimeDrift` |
| `CONFIGURATION_MISMATCH` | `RuntimeDrift` |
| `CONFIG_VALUES_VALIDATION_FAILED` | `ConfigurationViolation` |
| `DEPENDENCY_UNREACHABLE` | `RuntimeDrift` |
| `DEPENDENCY_VERSION_MISMATCH` | `RuntimeDrift` |
| `DUPLICATE_CAPABILITY` | `DuplicateName` |
| `DUPLICATE_CONFIGURATION_NAME` | `DuplicateName` |
| `DUPLICATE_DEPENDENCY_NAME` | `DuplicateName` |
| `DUPLICATE_ENVIRONMENT_NAME` | `DuplicateName` |
| `DUPLICATE_INTERFACE_NAME` | `DuplicateName` |
| `DUPLICATE_POLICY_NAME` | `DuplicateName` |
| `DUPLICATE_READINESS_ID` | `DuplicateName` |
| `EMPTY_COMPATIBILITY` | `InvalidDependency` |
| `EMPTY_READINESS_DESCRIPTION` | `MissingEvidence` |
| `EMPTY_READINESS_EVIDENCE` | `MissingEvidence` |
| `ENVIRONMENT_UNKNOWN_CONFIGURATION` | `InvalidReference` |
| `ENVIRONMENT_VALUES_INVALID` | `ConfigurationViolation` |
| `ENVIRONMENT_VALUES_MISSING` | `MissingConfiguration` |
| `FILE_NOT_FOUND` | `InvalidFile` |
| `INTERFACE_ABSENT` | `RuntimeDrift` |
| `INTERFACE_PROTOCOL_MISMATCH` | `RuntimeDrift` |
| `INTERFACE_VISIBILITY_MISMATCH` | `RuntimeDrift` |
| `INVALID_COMPATIBILITY` | `InvalidDependency` |
| `INVALID_CONFIG_JSON` | `InvalidFile` |
| `INVALID_CONFIG_REF` | `InvalidReference` |
| `INVALID_CONFIG_SCHEMA` | `SchemaViolation` |
| `INVALID_INTERFACE_SPEC` | `InvalidFile` |
| `INVALID_OCI_REF` | `InvalidReference` |
| `INVALID_POLICY_JSON` | `InvalidFile` |
| `INVALID_POLICY_REF` | `InvalidReference` |
| `INVALID_POLICY_SCHEMA` | `SchemaViolation` |
| `INVALID_READINESS_EXPIRES` | `InvalidReadiness` |
| `INVALID_READINESS_REVISION` | `InvalidReadiness` |
| `INVALID_SEMVER` | `InvalidVersion` |
| `NEEDS_PACTO_2_1` | `InvalidVersion` |
| `NEEDS_PACTO_2_2` | `InvalidVersion` |
| `NEEDS_PACTO_2_3` | `InvalidVersion` |
| `PERSISTENCE_MISMATCH` | `RuntimeDrift` |
| `POLICY_ENFORCEMENT_ERROR` | `PolicyViolation` |
| `POLICY_REF_CYCLE` | `ReferenceCycle` |
| `POLICY_REF_UNRESOLVED` | `UnresolvedReference` |
| `POLICY_VIOLATION` | `PolicyViolation` |
| `SCHEMA_ERROR` | `SchemaViolation` |
| `SCHEMA_VIOLATION` | `SchemaViolation` |
| `STATELESS_PERSISTENT_CONFLICT` | `StateMismatch` |
| `UNSUPPORTED_PACTO_VERSION` | `InvalidVersion` |
| `VALUES_WITHOUT_SCHEMA` | `MissingConfiguration` |
| `WORKLOAD_MISMATCH` | `RuntimeDrift` |
| `YAML_PARSE_ERROR` | `SchemaViolation` |

### Severity `warning`

| Code | Category |
| --- | --- |
| `CONFIGURATION_ABSENT` | `RuntimeDrift` |
| `CONFIGURATION_MISMATCH` | `RuntimeDrift` |
| `DEPENDENCY_UNDECLARED` | `RuntimeDrift` |
| `DEPENDENCY_UNREACHABLE` | `RuntimeDrift` |
| `DEPENDENCY_VERSION_MISMATCH` | `RuntimeDrift` |
| `ENVIRONMENT_VALUES_UNVERIFIED` | `UnresolvedReference` |
| `POLICY_REF_NOT_ENFORCED` | `UnresolvedReference` |
| `TAG_NOT_DIGEST` | `InvalidReference` |

### Severity `unknown`

| Code | Category |
| --- | --- |
| `COLLECTION_FAILED` | `Inconclusive` |
| `EVIDENCE_INSUFFICIENT` | `Inconclusive` |
| `EVIDENCE_MISSING` | `Inconclusive` |
| `EVIDENCE_STALE` | `Inconclusive` |
| `OBSERVATION_UNSUPPORTED` | `Inconclusive` |
