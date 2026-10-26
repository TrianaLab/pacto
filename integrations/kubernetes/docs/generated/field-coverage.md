<!--
  GENERATED FILE -- DO NOT EDIT.
  Produced by release/scripts/gen_integration_docs.py from pkg/validation/schema/field-coverage.yaml.
  Regenerate with `make docs-generate`; drift is a CI failure (`make docs-check`).
-->

# Field coverage

For every field you can write in a contract, this page says what checks it: `pacto validate` alone, a runtime check against the running service or nothing. Use it to answer "if I declare this, will Pacto notice when reality disagrees?" Each field also names the tests that prove its rule.

[Runtime conformance](../../runtime-conformance.md) runs three ways, and they do not see the same things: the Kubernetes operator also reads the cluster API, while `pacto agent` and `pacto run` see only the host. Every runtime-checked field says, per way, whether its evidence is `verified`, `partial` or `unverified` and why.

The page is generated from `pkg/validation/schema/field-coverage.yaml`. `pkg/validation/fieldcoverage_test.go` fails when a schema path or enum value is missing from that file, when an entry names no schema path and when a referenced test does not exist. Paths use `[]` for array items.

## Runtime classes

| Class | Meaning | Fields |
| --- | --- | --- |
| `verified` | A runtime fact can both satisfy and contradict the declaration. | 4 |
| `partial` | A runtime fact exists, but some values or environments stay unverified. | 15 |
| `static-only` | Checked by `pacto validate` only; nothing at runtime reads it. | 71 |
| `not-applicable` | Identity, container or open object with nothing to verify. | 13 |

A runtime check never turns "could not look" into a violation. A check with no usable fact is listed in `status.evaluationCoverage.unverified` and leaves the contract status unchanged.

## Summary

| Field | Runtime | Kubernetes | `pacto agent` | `pacto run` | Outcomes |
| --- | --- | --- | --- | --- | --- |
| `pactoVersion` | `not-applicable` | - | - | - | PARSE_ERROR when unsupported |
| `service` | `not-applicable` | - | - | - | PARSE_ERROR or SCHEMA_VIOLATION when malformed |
| `service.name` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `service.version` | `static-only` | - | - | - | INVALID_SEMVER |
| `service.owner` | `static-only` | - | - | - | SCHEMA_VIOLATION when empty |
| `service.owner.team` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `service.owner.dri` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `service.owner.contacts` | `static-only` | - | - | - | PARSE_ERROR when not a list |
| `service.owner.contacts[]` | `static-only` | - | - | - | PARSE_ERROR when not an object |
| `service.owner.contacts[].type` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `service.owner.contacts[].value` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `service.owner.contacts[].purpose` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `interfaces` | `not-applicable` | - | - | - | PARSE_ERROR when not a list |
| `interfaces[]` | `verified` | `verified` | `verified` | `verified` | Compliant when present; INTERFACE_ABSENT (error) when absent; OBSERVATION_UNSUPPORTED when unbound; EVIDENCE_STALE or COLLECTION_FAILED when the node agent is stale or failed; DUPLICATE_INTERFACE_NAME statically |
| `interfaces[].name` | `not-applicable` | - | - | - | SCHEMA_VIOLATION, DUPLICATE_INTERFACE_NAME |
| `interfaces[].type` | `partial` | `partial` | `partial` | `partial` | Compliant; INTERFACE_PROTOCOL_MISMATCH (error); listed in evaluationCoverage.unverified otherwise; SCHEMA_VIOLATION statically |
| `interfaces[].ref` | `static-only` | - | - | - | FILE_NOT_FOUND, INVALID_INTERFACE_SPEC, INVALID_MCP_TOOLS |
| `interfaces[].visibility` | `partial` | `partial` | `unverified` | `unverified` | Compliant; INTERFACE_VISIBILITY_MISMATCH (error); listed in evaluationCoverage.unverified otherwise; SCHEMA_VIOLATION statically |
| `configurations` | `not-applicable` | - | - | - | PARSE_ERROR when not a list |
| `configurations[]` | `partial` | `partial` | `unverified` | `unverified` | Compliant; CONFIGURATION_ABSENT or CONFIGURATION_MISMATCH (error, warning when optional); OBSERVATION_UNSUPPORTED when a required one is unbound; DUPLICATE_CONFIGURATION_NAME statically |
| `configurations[].name` | `not-applicable` | - | - | - | SCHEMA_VIOLATION, DUPLICATE_CONFIGURATION_NAME |
| `configurations[].schema` | `verified` | `verified` | `unverified` | `unverified` | Compliant or CONFIGURATION_MISMATCH; FILE_NOT_FOUND, INVALID_CONFIG_JSON, INVALID_CONFIG_SCHEMA statically |
| `configurations[].ref` | `static-only` | - | - | - | INVALID_CONFIG_REF; at runtime a ref-only configuration stays Insufficient |
| `configurations[].values` | `static-only` | - | - | - | CONFIG_VALUES_VALIDATION_FAILED |
| `configurations[].required` | `partial` | `partial` | `unverified` | `unverified` | EVIDENCE_MISSING and friends only when required; CONFIGURATION_* as warning when optional; SCHEMA_VIOLATION when omitted |
| `policies` | `static-only` | - | - | - | PARSE_ERROR when not a list |
| `policies[]` | `static-only` | - | - | - | DUPLICATE_POLICY_NAME |
| `policies[].name` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `policies[].schema` | `static-only` | - | - | - | POLICY_VIOLATION, FILE_NOT_FOUND, INVALID_POLICY_JSON, INVALID_POLICY_SCHEMA |
| `policies[].ref` | `static-only` | - | - | - | INVALID_POLICY_REF, POLICY_REF_UNRESOLVED, POLICY_REF_CYCLE, POLICY_REF_NOT_ENFORCED (warning) |
| `policies[].target` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `dependencies` | `not-applicable` | - | - | - | PARSE_ERROR when not a list |
| `dependencies[]` | `verified` | `verified` | `verified` | `partial` | Compliant; DEPENDENCY_UNREACHABLE (error, warning when optional); OBSERVATION_UNSUPPORTED when the provider is not a Pacto target; DEPENDENCY_UNDECLARED (warning) for reachable peers no entry names |
| `dependencies[].name` | `not-applicable` | - | - | - | SCHEMA_VIOLATION, DUPLICATE_DEPENDENCY_NAME |
| `dependencies[].ref` | `partial` | `partial` | `unverified` | `unverified` | OBSERVATION_UNSUPPORTED when no sibling matches; INVALID_OCI_REF and TAG_NOT_DIGEST (warning) statically; DEPENDENCY_REF_REQUIRED when missing on a dependency that is not external |
| `dependencies[].required` | `partial` | `partial` | `partial` | `partial` | DEPENDENCY_UNREACHABLE or DEPENDENCY_VERSION_MISMATCH as warning when optional; SCHEMA_VIOLATION when omitted |
| `dependencies[].compatibility` | `partial` | `partial` | `unverified` | `unverified` | Compliant; DEPENDENCY_VERSION_MISMATCH (error, warning when optional); listed in evaluationCoverage.unverified otherwise; INVALID_COMPATIBILITY statically |
| `dependencies[].external` | `static-only` | - | - | - | DEPENDENCY_REF_REQUIRED; NEEDS_PACTO_2_1 under 2.0 |
| `dependencies[].version` | `static-only` | - | - | - | EXTERNAL_VERSION_REQUIRED, VERSION_WITH_REF, COMPATIBILITY_WITHOUT_REF |
| `state` | `not-applicable` | - | - | - | PARSE_ERROR when not an object |
| `state.type` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `state.persistence` | `not-applicable` | - | - | - | SCHEMA_VIOLATION when missing |
| `state.persistence.scope` | `partial` | `partial` | `unverified` | `unverified` | Compliant; PERSISTENCE_MISMATCH (error) at state.persistence.scope; listed in evaluationCoverage.unverified otherwise; SCHEMA_VIOLATION statically |
| `state.persistence.durability` | `partial` | `partial` | `unverified` | `unverified` | Compliant; PERSISTENCE_MISMATCH (error) at state.persistence.durability; Unknown codes when unobservable; SCHEMA_VIOLATION statically |
| `state.dataCriticality` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `workload` | `verified` | `verified` | `unverified` | `unverified` | Compliant; WORKLOAD_MISMATCH (error); Unknown codes when no workload target; SCHEMA_VIOLATION statically |
| `capabilities` | `not-applicable` | - | - | - | PARSE_ERROR when not a list |
| `capabilities[]` | `partial` | `partial` | `partial` | `partial` | Compliant; CAPABILITY_ABSENT (error); OBSERVATION_UNSUPPORTED when unbound; extensions listed under coverage.unverified; DUPLICATE_CAPABILITY statically |
| `capabilities[].type` | `partial` | `partial` | `partial` | `partial` | Compliant, CAPABILITY_ABSENT or Unknown codes; SCHEMA_VIOLATION statically |
| `capabilities[].ref` | `static-only` | - | - | - | CAPABILITY_REF_INVALID, SCHEMA_VIOLATION |
| `capabilities[].binding` | `partial` | `partial` | `partial` | `partial` | OBSERVATION_UNSUPPORTED when missing; SCHEMA_VIOLATION on an extension |
| `capabilities[].binding.type` | `partial` | `partial` | `partial` | `partial` | SCHEMA_VIOLATION for anything else |
| `capabilities[].binding.interface` | `partial` | `partial` | `partial` | `partial` | CAPABILITY_INTERFACE_UNKNOWN statically; OBSERVATION_UNSUPPORTED at runtime when the interface is unbound |
| `capabilities[].binding.path` | `partial` | `partial` | `partial` | `partial` | CAPABILITY_PATH_INVALID statically (SSRF guard); EVIDENCE_INSUFFICIENT on a tier B path mismatch |
| `readiness` | `static-only` | - | - | - | PARSE_ERROR when not an object |
| `readiness.minScore` | `static-only` | - | - | - | SCHEMA_VIOLATION; gate result in pacto validate --readiness and the operator ReadinessSatisfied condition |
| `readiness.expires` | `static-only` | - | - | - | INVALID_READINESS_EXPIRES; an expired assessment scores 0 |
| `readiness.partialCredit` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `readiness.history` | `static-only` | - | - | - | PARSE_ERROR when not a list |
| `readiness.history[]` | `static-only` | - | - | - | PARSE_ERROR when not an object |
| `readiness.history[].date` | `static-only` | - | - | - | INVALID_READINESS_REVISION |
| `readiness.history[].version` | `static-only` | - | - | - | INVALID_READINESS_REVISION |
| `readiness.history[].author` | `static-only` | - | - | - | INVALID_READINESS_REVISION |
| `readiness.history[].description` | `static-only` | - | - | - | INVALID_READINESS_REVISION |
| `readiness.claims` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `readiness.claims[]` | `static-only` | - | - | - | DUPLICATE_READINESS_ID |
| `readiness.claims[].id` | `static-only` | - | - | - | SCHEMA_VIOLATION, DUPLICATE_READINESS_ID |
| `readiness.claims[].type` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `readiness.claims[].category` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `readiness.claims[].status` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `readiness.claims[].evidence` | `static-only` | - | - | - | EMPTY_READINESS_EVIDENCE |
| `readiness.claims[].weight` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `readiness.claims[].description` | `static-only` | - | - | - | EMPTY_READINESS_DESCRIPTION |
| `publish` | `static-only` | - | - | - | PARSE_ERROR when not a map; SCHEMA_VIOLATION under 2.0 |
| `publish.bundle` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `publish.deploy` | `static-only` | - | - | - | PUBLISH_DEPLOY_NOT_MANIFESTS |
| `publish.artifacts` | `static-only` | - | - | - | PARSE_ERROR when not a list |
| `publish.artifacts[]` | `static-only` | - | - | - | DUPLICATE_PUBLISH_ARTIFACT |
| `publish.artifacts[].name` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `publish.artifacts[].type` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `publish.artifacts[].ref` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `publish.artifacts[].build` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `publish.artifacts[].build.dockerfile` | `static-only` | - | - | - | PUBLISH_DOCKERFILE_NOT_IMAGE |
| `publish.artifacts[].build.dockerfile.context` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `publish.artifacts[].build.dockerfile.file` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `publish.artifacts[].build.dockerfile.platforms` | `static-only` | - | - | - | PARSE_ERROR when not a list |
| `publish.artifacts[].build.dockerfile.platforms[]` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `publish.artifacts[].build.command` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `publish.artifacts[].build.command[]` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `publish.artifacts[].sbom` | `static-only` | - | - | - | PUBLISH_SBOM_NOT_IMAGE |
| `publish.sign` | `static-only` | - | - | - | SCHEMA_VIOLATION; pacto publish refuses when omitted |
| `publish.sign.keyless` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `publish.sign.identity` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `publish.sign.issuer` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `publish.sign.key` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `publish.sign.kms` | `static-only` | - | - | - | SCHEMA_VIOLATION |
| `publish.sign.transparencyLog` | `static-only` | - | - | - | PARSE_ERROR when not a boolean |
| `checks` | `static-only` | - | - | - | PARSE_ERROR when not a list; NEEDS_PACTO_2_1 under 2.0 |
| `checks[]` | `static-only` | - | - | - | SCHEMA_VIOLATION, DUPLICATE_CHECK_NAME |
| `checks[].name` | `static-only` | - | - | - | SCHEMA_VIOLATION, DUPLICATE_CHECK_NAME |
| `checks[].file` | `static-only` | - | - | - | FILE_NOT_FOUND |
| `metadata` | `not-applicable` | - | - | - | PARSE_ERROR when not a map |
| `extensions` | `not-applicable` | - | - | - | PARSE_ERROR when not a map |

## Fields

### `pactoVersion`

| Key | Value |
| --- | --- |
| Meaning | Contract format version; selects the JSON Schema the contract is validated against. |
| Runtime | `not-applicable` |
| Evidence | none: the field has no runtime counterpart |
| Rule | parse rejects anything but "2.0" and "2.1" before any other layer runs; the version selects the schema |
| Outcomes | PARSE_ERROR when unsupported |
| Limitations | 2.0 has its own schema without publish, checks, external dependencies or mcp; this inventory walks 2.1, the superset |
| Tests | unit: `pkg/contract/parse_test.go:TestParse_UnsupportedVersion`, `pkg/validation/structural_test.go:TestValidateStructuralRaw_UnsupportedVersion_V10`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/pactoVersion/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/pactoVersion/invalid` |

| Value | Runtime | Meaning | Rule | Outcomes | Limitations | Tests |
| --- | --- | --- | --- | --- | --- | --- |
| `2.1` | `static-only` | the newest Operational Contract format, which adds publish, agent and the mcp interface type; 2.0 contracts keep working unchanged | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/pactoVersion=2.1`, `tests/integration/field_coverage_test.go:TestFieldCoverage/pactoVersion=2.0` |

### `service`

| Key | Value |
| --- | --- |
| Meaning | Identity block of the service the contract describes. |
| Runtime | `not-applicable` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema: object with name and version |
| Outcomes | PARSE_ERROR or SCHEMA_VIOLATION when malformed |
| Limitations | container only; its children carry the semantics |
| Tests | unit: `pkg/contract/parse_test.go:TestParse_ValidFull_Service`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/service/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/service/invalid` |

### `service.name`

| Key | Value |
| --- | --- |
| Meaning | DNS-style service name; the identity of the contract everywhere. |
| Runtime | `static-only` |
| Evidence | used as the subject name of service-scoped evidence (workload, persistence), never checked against a runtime name |
| Rule | schema pattern |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | the operator binds a runtime target by the Pacto CR, not by this name |
| Tests | unit: `pkg/contract/parse_test.go:TestParse_MissingServiceName`, `pkg/validation/evaluate_test.go:TestEvaluate_ServiceScopedMatchByServiceName`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/service.name/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/service.name/invalid` |

### `service.version`

| Key | Value |
| --- | --- |
| Meaning | Semantic version of this contract revision. |
| Runtime | `static-only` |
| Evidence | consumers read it from the provider's Pacto status as DependencyObservation.Version for their compatibility check |
| Rule | cross-field: valid semver |
| Outcomes | INVALID_SEMVER |
| Limitations | the running image version is never compared with it; it is the provider-side input of dependencies[].compatibility |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidateServiceVersion_InvalidSemver`, `pkg/validation/structural_test.go:TestValidateStructuralRaw_VersionMustBeOnePathComponent`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/service.version/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/service.version/invalid`; e2e: `tests/acceptance/kind/runtime-conformance.sh:dependency-version-mismatch` |

### `service.owner`

| Key | Value |
| --- | --- |
| Meaning | Who owns the service. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema: at least one of team or dri |
| Outcomes | SCHEMA_VIOLATION when empty |
| Limitations | ownership is metadata for routing and dashboards |
| Tests | unit: `pkg/contract/owner_test.go:TestOwner_IsEmpty`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/service.owner/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/service.owner/invalid` |

### `service.owner.team`

| Key | Value |
| --- | --- |
| Meaning | Owning team identifier. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema pattern |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | not checked against any directory |
| Tests | unit: `pkg/contract/owner_test.go:TestOwnerKey_TeamAndDRINamespacesNeverCollide`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/service.owner.team/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/service.owner.team/invalid` |

### `service.owner.dri`

| Key | Value |
| --- | --- |
| Meaning | Directly responsible individual. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema pattern |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | not checked against any directory |
| Tests | unit: `pkg/contract/owner_test.go:TestOwner_DisplayString`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/service.owner.dri/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/service.owner.dri/invalid` |

### `service.owner.contacts`

| Key | Value |
| --- | --- |
| Meaning | Ways to reach the owner. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema: array of contact objects |
| Outcomes | PARSE_ERROR when not a list |
| Limitations | treated as a set; order is irrelevant |
| Tests | unit: `pkg/contract/owner_test.go:TestOwner_EqualComparesContactsAsASet`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/service.owner.contacts/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/service.owner.contacts/invalid` |

### `service.owner.contacts[]`

| Key | Value |
| --- | --- |
| Meaning | One contact entry. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema: object with type and value |
| Outcomes | PARSE_ERROR when not an object |
| Limitations | never contacted or verified |
| Tests | unit: `pkg/contract/owner_test.go:TestOwner_ContactSetOrdersByTypeThenValueThenPurpose`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/service.owner.contacts[]/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/service.owner.contacts[]/invalid` |

### `service.owner.contacts[].type`

| Key | Value |
| --- | --- |
| Meaning | Channel of the contact. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema enum |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | the value is not checked to match the channel (an email type may hold a URL) |
| Tests | unit: `pkg/contract/owner_test.go:TestOwner_YAMLRoundTrip`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/service.owner.contacts[].type/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/service.owner.contacts[].type/invalid` |

| Value | Runtime | Meaning | Rule | Outcomes | Limitations | Tests |
| --- | --- | --- | --- | --- | --- | --- |
| `email` | `static-only` | an email address | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/service.owner.contacts[].type=email` |
| `url` | `static-only` | a web page or form | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/service.owner.contacts[].type=url` |
| `group` | `static-only` | a mailing list or directory group | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/service.owner.contacts[].type=group` |
| `oncall` | `static-only` | a paging rotation | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/service.owner.contacts[].type=oncall` |
| `chat` | `static-only` | a chat channel | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/service.owner.contacts[].type=chat` |
| `other` | `static-only` | any other channel | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/service.owner.contacts[].type=other` |

### `service.owner.contacts[].value`

| Key | Value |
| --- | --- |
| Meaning | Address of the contact. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema: required, non-empty |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | never validated as reachable |
| Tests | unit: `pkg/contract/owner_test.go:TestOwner_YAMLRoundTrip`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/service.owner.contacts[].value/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/service.owner.contacts[].value/invalid` |

### `service.owner.contacts[].purpose`

| Key | Value |
| --- | --- |
| Meaning | Why to use this contact. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema enum |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | informational |
| Tests | unit: `pkg/contract/owner_test.go:TestOwner_YAMLRoundTrip`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/service.owner.contacts[].purpose/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/service.owner.contacts[].purpose/invalid` |

| Value | Runtime | Meaning | Rule | Outcomes | Limitations | Tests |
| --- | --- | --- | --- | --- | --- | --- |
| `ownership` | `static-only` | accountable owner | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/service.owner.contacts[].purpose=ownership` |
| `support` | `static-only` | day-to-day questions | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/service.owner.contacts[].purpose=support` |
| `escalation` | `static-only` | when support does not answer | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/service.owner.contacts[].purpose=escalation` |
| `oncall` | `static-only` | operational incidents | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/service.owner.contacts[].purpose=oncall` |
| `notifications` | `static-only` | announcements and change notices | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/service.owner.contacts[].purpose=notifications` |

### `interfaces`

| Key | Value |
| --- | --- |
| Meaning | The interfaces the service exposes. |
| Runtime | `not-applicable` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema: array |
| Outcomes | PARSE_ERROR when not a list |
| Limitations | container; each item is a runtime assertion |
| Tests | unit: `pkg/contract/parse_test.go:TestParse_ValidFull_Interfaces`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/interfaces/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/interfaces/invalid` |

### `interfaces[]`

| Key | Value |
| --- | --- |
| Meaning | One exposed interface. Its availability is always a required runtime assertion. |
| Runtime | `verified` |
| Evidence | InterfaceObserved.Present from ready EndpointSlice endpoints on the bound Service port, refined by the node agent's LISTEN sockets in the backing pods |
| Rule | Present=false beyond the stabilization window contradicts the declaration; endpoints ready but no pod listening on the target port (complete kernel coverage) is also Present=false |
| Outcomes | Compliant when present; INTERFACE_ABSENT (error) when absent; OBSERVATION_UNSUPPORTED when unbound; EVIDENCE_STALE or COLLECTION_FAILED when the node agent is stale or failed; DUPLICATE_INTERFACE_NAME statically |
| Limitations | needs a spec.target.interfaceBindings entry (or name-match discovery); hostNetwork pods are unattributable to the kernel and keep the Kubernetes result |
| Tests | unit: `pkg/validation/evaluate_test.go:TestEvaluate_ContradictionPerDimension`, `pkg/validation/evaluate_test.go:TestEvaluate_UnknownCodesAndMessages`, `pkg/validation/crossfield_test.go:TestValidateInterfaceNamesUnique_Duplicate`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/interfaces[]/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/interfaces[]/invalid`; e2e: `tests/acceptance/kind/runtime-conformance.sh:conforming-workload`, `tests/acceptance/kind/runtime-conformance.sh:listener-absent`, `tests/acceptance/kind/runtime-conformance.sh:unbound-interface`, `tests/acceptance/kind/runtime-conformance.sh:agent-stopped-stale`, `tests/acceptance/kind/runtime-conformance.sh:agent-failed`, `tests/acceptance/kind/runtime-conformance.sh:pod-restart`, `tests/acceptance/kind/runtime-conformance.sh:rollout-replacement`, `tests/acceptance/kind/runtime-conformance.sh:same-node-attribution` |

| Platform | Evidence | Why | Tests |
| --- | --- | --- | --- |
| Kubernetes (the operator with its node agent) | `verified` | ready EndpointSlice endpoints, refined by the node agent's listeners in the backing pods | the e2e scenarios above |
| `pacto agent` (a Docker host or a server) | `verified` | the kernel's listeners of the bound container, unit or process on the bound port | `tests/acceptance/local/runtime-docker.sh:docker-served`, `tests/acceptance/local/runtime-docker.sh:docker-deaf`, `tests/acceptance/local/runtime-docker.sh:docker-hostnet-served`, `tests/acceptance/local/runtime-docker.sh:docker-hostnet-deaf` |
| `pacto run` (one wrapped command) | `verified` | the command tree's listeners, in full in both eBPF and unprivileged mode | `tests/acceptance/local/runtime-run.sh:run-ebpf-served`, `tests/acceptance/local/runtime-run.sh:run-ebpf-deaf`, `tests/acceptance/local/runtime-run.sh:run-unprivileged-served`, `tests/acceptance/local/runtime-run.sh:run-unprivileged-deaf` |

### `interfaces[].name`

| Key | Value |
| --- | --- |
| Meaning | Interface identifier; the subject name of its evidence and the key of the CR binding. |
| Runtime | `not-applicable` |
| Evidence | identity only: it names the InterfaceObserved subject; the runtime check lives on interfaces[] |
| Rule | schema: required; cross-field: unique |
| Outcomes | SCHEMA_VIOLATION, DUPLICATE_INTERFACE_NAME |
| Limitations | a renamed interface without a renamed binding becomes unbound (Unsupported) |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidateInterfaceNamesUnique_Duplicate`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/interfaces[].name/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/interfaces[].name/invalid` |

### `interfaces[].type`

| Key | Value |
| --- | --- |
| Meaning | Interface specification family. |
| Runtime | `partial` |
| Evidence | InterfaceObservation.Protocol from the active protocol probe (h2c preface; classifies http1, http2, tls, other) |
| Rule | OpenAPI is satisfied by http1 or http2 and contradicted by other; gRPC is satisfied by http2 and contradicted by http1 or other; tls, an empty protocol and AsyncAPI leave the facet unverified |
| Outcomes | Compliant; INTERFACE_PROTOCOL_MISMATCH (error); listed in evaluationCoverage.unverified otherwise; SCHEMA_VIOLATION statically |
| Limitations | requires --enable-probing; TLS hides the protocol; AsyncAPI names no wire protocol; judged only when the interface is Observed and present |
| Tests | unit: `pkg/validation/evaluate_test.go:TestEvaluate_InterfaceTypeFacet`, `pkg/validation/evaluate_test.go:TestEvaluate_InterfaceFacetsNeedPresentObservation`, `pkg/validation/structural_test.go:TestStructural_V20InterfaceTypes`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/interfaces[].type/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/interfaces[].type/invalid`; e2e: `tests/acceptance/kind/runtime-conformance.sh:conforming-workload`, `tests/acceptance/kind/runtime-conformance.sh:protocol-mismatch` |

| Platform | Evidence | Why | Tests |
| --- | --- | --- | --- |
| Kubernetes (the operator with its node agent) | `partial` | protocol probe on the Service port with controller.enableProbing; tls and asyncapi stay unverified | the e2e scenarios above |
| `pacto agent` (a Docker host or a server) | `partial` | protocol probe on the bound port with --enable-probing; tls and asyncapi stay unverified | `internal/runtimewatch/evalsink/sink_test.go:TestEvaluate_Compliant` |
| `pacto run` (one wrapped command) | `partial` | protocol probe on --host at the bound port with --enable-probing; tls and asyncapi stay unverified | `internal/runtimewatch/evalsink/sink_test.go:TestEvaluate_Compliant` |

| Value | Runtime | Meaning | Rule | Outcomes | Limitations | Tests |
| --- | --- | --- | --- | --- | --- | --- |
| `openapi` | `verified` | HTTP API described by an OpenAPI document. | http1 or http2 satisfies; other contradicts | Compliant or INTERFACE_PROTOCOL_MISMATCH | tls or no probe answer is unverified | unit: `pkg/validation/evaluate_test.go:TestEvaluate_InterfaceTypeFacet`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/interfaces[].type=openapi`; e2e: `tests/acceptance/kind/runtime-conformance.sh:conforming-workload`, `tests/acceptance/kind/runtime-conformance.sh:protocol-mismatch` |
| `grpc` | `verified` | gRPC service; HTTP/2 on the wire. | http2 satisfies; http1 or other contradicts | Compliant or INTERFACE_PROTOCOL_MISMATCH | tls or no probe answer is unverified | unit: `pkg/validation/evaluate_test.go:TestEvaluate_InterfaceTypeFacet`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/interfaces[].type=grpc`; e2e: `tests/acceptance/kind/runtime-conformance.sh:protocol-mismatch` |
| `asyncapi` | `static-only` | Message-driven API described by an AsyncAPI document. | no wire protocol is implied, so nothing satisfies or contradicts it | always listed as unverified when the interface is present | brokers and protocols vary (Kafka, AMQP, MQTT, WebSocket) | unit: `pkg/validation/evaluate_test.go:TestEvaluate_InterfaceTypeFacet`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/interfaces[].type=asyncapi` |
| `mcp` | `static-only` | The tools a service exposes, as an MCP tools/list result (pactoVersion 2.1). | cross-field: the file lists tools, each named once; diff compares tool by tool | INVALID_MCP_TOOLS; always listed as unverified when the interface is present | MCP runs over stdio or HTTP, so no wire protocol is implied; descriptions are not compared | unit: `pkg/validation/crossfield_test.go:TestValidateMCPInterfaces`, `pkg/diff/mcp_test.go:TestDiffMCP`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/interfaces[].type=mcp`, `tests/integration/field_coverage_test.go:TestFieldCoverage/interfaces[].type=mcp/no-tools` |

### `interfaces[].ref`

| Key | Value |
| --- | --- |
| Meaning | Path of the interface specification inside the bundle. |
| Runtime | `static-only` |
| Evidence | none: request and response conformance against the spec is not observed (no payload capture) |
| Rule | cross-field: file exists; YAML or JSON parses (.proto is not parsed) |
| Outcomes | FILE_NOT_FOUND, INVALID_INTERFACE_SPEC, INVALID_MCP_TOOLS |
| Limitations | only the protocol family is checked at runtime, through interfaces[].type |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidateInterfaceFiles_FileNotFound`, `pkg/validation/crossfield_test.go:TestValidateInterfaceFileContent_InvalidYAML`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/interfaces[].ref/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/interfaces[].ref/invalid` |

### `interfaces[].visibility`

| Key | Value |
| --- | --- |
| Meaning | Intended audience of the interface; omitted means internal (the schema default, applied by the engine). |
| Runtime | `partial` |
| Evidence | InterfaceObservation.Exposure: external only on a public address (a public spec.externalIPs entry, a LoadBalancer without an internal-LB annotation whose status carries a public IP or a hostname not starting with internal-, or an Ingress routing to the port whose status carries one); internal only for a ClusterIP Service no Ingress or HTTPRoute routes to; unknown otherwise (NodePort, internal or address-less LoadBalancer or Ingress, any HTTPRoute, a read error) |
| Rule | internal is satisfied by internal and contradicted by external; public is satisfied by external; public with internal exposure and an unknown exposure are unverified |
| Outcomes | Compliant; INTERFACE_VISIBILITY_MISMATCH (error); listed in evaluationCoverage.unverified otherwise; SCHEMA_VIOLATION statically |
| Limitations | node and gateway addresses are not read, so NodePort and HTTPRoute exposure is unknown; exposure through a gateway the operator cannot see (cloud load balancer outside the cluster, service mesh ingress) is invisible; judged only when the interface is Observed and present |
| Tests | unit: `pkg/validation/evaluate_test.go:TestEvaluate_InterfaceVisibilityFacet`, `integrations/kubernetes/internal/observer/exposure_test.go:TestInterfaceExposure`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/interfaces[].visibility/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/interfaces[].visibility/invalid`; e2e: `tests/acceptance/kind/runtime-conformance.sh:conforming-workload`, `tests/acceptance/kind/runtime-conformance.sh:visibility-mismatch` |

| Platform | Evidence | Why | Tests |
| --- | --- | --- | --- |
| Kubernetes (the operator with its node agent) | `partial` | exposure from Service, Ingress and load balancer status; NodePort and HTTPRoute stay unknown | the e2e scenarios above |
| `pacto agent` (a Docker host or a server) | `unverified` | no source off Kubernetes: a host does not know who can reach a port, so the facet is listed in coverage.unverified | - |
| `pacto run` (one wrapped command) | `unverified` | no source off Kubernetes: a host does not know who can reach a port, so the facet is listed in coverage.unverified | - |

| Value | Runtime | Meaning | Rule | Outcomes | Limitations | Tests |
| --- | --- | --- | --- | --- | --- | --- |
| `internal` | `verified` | Only callers inside the cluster may reach it. | internal satisfies; external contradicts | Compliant or INTERFACE_VISIBILITY_MISMATCH | unknown exposure is unverified | unit: `pkg/validation/evaluate_test.go:TestEvaluate_InterfaceVisibilityFacet`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/interfaces[].visibility=internal`; e2e: `tests/acceptance/kind/runtime-conformance.sh:conforming-workload`, `tests/acceptance/kind/runtime-conformance.sh:visibility-mismatch` |
| `public` | `partial` | Meant to be reachable from outside the cluster. | external satisfies; internal and unknown are unverified, never a violation | Compliant or unverified | a public interface behind an external gateway looks internal | unit: `pkg/validation/evaluate_test.go:TestEvaluate_InterfaceVisibilityFacet`, `integrations/kubernetes/internal/observer/exposure_test.go:TestInterfaceFacts_ProtocolAndExposure`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/interfaces[].visibility=public`; e2e: `tests/acceptance/kind/runtime-conformance.sh:visibility-mismatch` |

### `configurations`

| Key | Value |
| --- | --- |
| Meaning | Configuration inputs the service consumes. |
| Runtime | `not-applicable` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema: array |
| Outcomes | PARSE_ERROR when not a list |
| Limitations | container; each item is a runtime assertion |
| Tests | unit: `pkg/contract/contract_test.go:TestContract_Configurations`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/configurations/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/configurations/invalid` |

### `configurations[]`

| Key | Value |
| --- | --- |
| Meaning | One configuration input. Required ones are runtime assertions. |
| Runtime | `partial` |
| Evidence | ConfigurationPresent from the bound ConfigMap or Secret key (spec.target.configBindings) |
| Rule | absent contradicts; present but failing the declared schema contradicts |
| Outcomes | Compliant; CONFIGURATION_ABSENT or CONFIGURATION_MISMATCH (error, warning when optional); OBSERVATION_UNSUPPORTED when a required one is unbound; DUPLICATE_CONFIGURATION_NAME statically |
| Limitations | needs a configBinding; environment variables set directly on the pod are not read |
| Tests | unit: `pkg/validation/evaluate_test.go:TestEvaluate_ContradictionPerDimension`, `pkg/validation/crossfield_test.go:TestValidateConfigurationNamesUnique_Duplicate`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/configurations[]/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/configurations[]/invalid`; e2e: `tests/acceptance/kind/runtime-conformance.sh:configuration-absent`, `tests/acceptance/kind/runtime-conformance.sh:configuration-mismatch`, `tests/acceptance/kind/runtime-conformance.sh:configuration-optional` |

| Platform | Evidence | Why | Tests |
| --- | --- | --- | --- |
| Kubernetes (the operator with its node agent) | `partial` | presence of the ConfigMap or Secret key bound in spec.target.configBindings | the e2e scenarios above |
| `pacto agent` (a Docker host or a server) | `unverified` | no source off Kubernetes; a required configuration reads EVIDENCE_MISSING (Unknown) | - |
| `pacto run` (one wrapped command) | `unverified` | no source off Kubernetes; a required configuration reads EVIDENCE_MISSING (Unknown) | - |

### `configurations[].name`

| Key | Value |
| --- | --- |
| Meaning | Configuration identifier; subject of its evidence and key of the CR binding. |
| Runtime | `not-applicable` |
| Evidence | identity only; the runtime check lives on configurations[] |
| Rule | schema: required; cross-field: unique |
| Outcomes | SCHEMA_VIOLATION, DUPLICATE_CONFIGURATION_NAME |
| Limitations | a rename without a rebind leaves it unbound |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidateConfigurationNamesUnique_Duplicate`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/configurations[].name/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/configurations[].name/invalid` |

### `configurations[].schema`

| Key | Value |
| --- | --- |
| Meaning | JSON Schema the configuration must satisfy. |
| Runtime | `verified` |
| Evidence | ConfigurationObservation.Conformant: the live bound content validated against this schema |
| Rule | content failing the schema contradicts |
| Outcomes | Compliant or CONFIGURATION_MISMATCH; FILE_NOT_FOUND, INVALID_CONFIG_JSON, INVALID_CONFIG_SCHEMA statically |
| Limitations | an inline contract has no bundle filesystem, so conformance is Insufficient |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidateConfigFiles_FileNotFound`, `pkg/validation/crossfield_test.go:TestValidateConfigSchemaContent_InvalidSchema`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/configurations[].schema/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/configurations[].schema/invalid`; e2e: `tests/acceptance/kind/runtime-conformance.sh:configuration-mismatch` |

| Platform | Evidence | Why | Tests |
| --- | --- | --- | --- |
| Kubernetes (the operator with its node agent) | `verified` | the bound content validated against the schema | the e2e scenarios above |
| `pacto agent` (a Docker host or a server) | `unverified` | no configuration content is read off Kubernetes | - |
| `pacto run` (one wrapped command) | `unverified` | no configuration content is read off Kubernetes | - |

### `configurations[].ref`

| Key | Value |
| --- | --- |
| Meaning | OCI reference to a configuration defined by another bundle. |
| Runtime | `static-only` |
| Evidence | none: the referenced bundle defines no schema path to validate live content against |
| Rule | cross-field: valid OCI reference |
| Outcomes | INVALID_CONFIG_REF; at runtime a ref-only configuration stays Insufficient |
| Limitations | documented in the design as not doing |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidateConfigRef_InvalidOCI`, `pkg/validation/crossfield_test.go:TestValidateConfigRef_ValidOCI`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/configurations[].ref/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/configurations[].ref/invalid` |

### `configurations[].values`

| Key | Value |
| --- | --- |
| Meaning | Declared configuration values, validated against the schema. |
| Runtime | `static-only` |
| Evidence | none: the runtime check reads live content, not these declared values |
| Rule | cross-field: values satisfy the schema |
| Outcomes | CONFIG_VALUES_VALIDATION_FAILED |
| Limitations | the operator may merge spec.overrides into them before validation |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidateConfigValues_WithSchema_Invalid`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/configurations[].values/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/configurations[].values/invalid` |

### `configurations[].required`

| Key | Value |
| --- | --- |
| Meaning | Whether the service cannot run without it. |
| Runtime | `partial` |
| Evidence | decides the severity axis of the configuration evidence |
| Rule | required: a gap is Unknown and a contradiction an error; optional: a gap is silent and a contradiction a warning |
| Outcomes | EVIDENCE_MISSING and friends only when required; CONFIGURATION_* as warning when optional; SCHEMA_VIOLATION when omitted |
| Limitations | not shown by pacto diff |
| Tests | unit: `pkg/validation/schema_binding_test.go:TestSchema_ConfigurationMissingRequired_Rejected`, `pkg/contract/contract_test.go:TestRequired_SerializationRoundTrip`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/configurations[].required/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/configurations[].required/invalid`; e2e: `tests/acceptance/kind/runtime-conformance.sh:configuration-optional` |

| Platform | Evidence | Why | Tests |
| --- | --- | --- | --- |
| Kubernetes (the operator with its node agent) | `partial` | sets the severity axis of the configuration evidence | the e2e scenarios above |
| `pacto agent` (a Docker host or a server) | `unverified` | no source off Kubernetes; required makes the missing configuration read EVIDENCE_MISSING (Unknown) instead of being skipped | - |
| `pacto run` (one wrapped command) | `unverified` | no source off Kubernetes; required makes the missing configuration read EVIDENCE_MISSING (Unknown) instead of being skipped | - |

### `policies`

| Key | Value |
| --- | --- |
| Meaning | Policies the contract itself must satisfy. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema: array |
| Outcomes | PARSE_ERROR when not a list |
| Limitations | policies constrain the contract document, not the running service |
| Tests | unit: `pkg/contract/parse_test.go:TestParse_ValidFull_Policies`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/policies/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/policies/invalid` |

### `policies[]`

| Key | Value |
| --- | --- |
| Meaning | One policy source. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | cross-field: unique name; schema: exactly one of schema or ref |
| Outcomes | DUPLICATE_POLICY_NAME |
| Limitations | never evaluated at runtime |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidatePolicyNamesUnique_Duplicate`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/policies[]/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/policies[]/invalid` |

### `policies[].name`

| Key | Value |
| --- | --- |
| Meaning | Policy identifier. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema: required; cross-field: unique |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | used to name the policy in findings |
| Tests | unit: `pkg/contract/contract_test.go:TestPolicy_NameField`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/policies[].name/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/policies[].name/invalid` |

### `policies[].schema`

| Key | Value |
| --- | --- |
| Meaning | JSON Schema in the bundle the contract must satisfy. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | policy layer: the raw contract is validated against it |
| Outcomes | POLICY_VIOLATION, FILE_NOT_FOUND, INVALID_POLICY_JSON, INVALID_POLICY_SCHEMA |
| Limitations | evaluated by pacto validate and by the operator before observation |
| Tests | unit: `pkg/validation/policy_test.go:TestEnforcePolicies_SinglePolicyViolated`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/policies[].schema/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/policies[].schema/invalid` |

### `policies[].ref`

| Key | Value |
| --- | --- |
| Meaning | Reference to a bundle whose policies apply. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | policy layer: resolved recursively with a resolver; without one it is reported, not enforced |
| Outcomes | INVALID_POLICY_REF, POLICY_REF_UNRESOLVED, POLICY_REF_CYCLE, POLICY_REF_NOT_ENFORCED (warning) |
| Limitations | the operator validates without a resolver, so a ref-based policy is only a warning there |
| Tests | unit: `pkg/validation/policy_test.go:TestResolvePoliciesWithResolver_Resolved`, `pkg/validation/crossfield_test.go:TestValidatePolicyFields_InvalidRef`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/policies[].ref/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/policies[].ref/invalid` |

### `policies[].target`

| Key | Value |
| --- | --- |
| Meaning | What the policy applies to; omitted means contract. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema enum |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | only one target exists and nothing reads the value |
| Tests | unit: `pkg/contract/contract_test.go:TestPolicy_Target`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/policies[].target/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/policies[].target/invalid` |

| Value | Runtime | Meaning | Rule | Outcomes | Limitations | Tests |
| --- | --- | --- | --- | --- | --- | --- |
| `contract` | `static-only` | the policy validates the contract document | schema enum; the only value | no runtime outcome; only the static result below | not observed at runtime | unit: `pkg/contract/contract_test.go:TestPolicy_Target`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/policies[].target=contract` |

### `dependencies`

| Key | Value |
| --- | --- |
| Meaning | Services this service calls. |
| Runtime | `not-applicable` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema: array |
| Outcomes | PARSE_ERROR when not a list |
| Limitations | the list is not claimed to be exhaustive; undeclared peers are drift warnings, not reported while any entry resolves to no Pacto target by ref or name (a local or non-OCI ref, or an unresolved OCI ref) |
| Tests | unit: `pkg/validation/evaluate_test.go:TestEvaluate_UndeclaredDependency`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/dependencies/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/dependencies/invalid`; e2e: `tests/acceptance/kind/runtime-conformance.sh:dependency-undeclared` |

### `dependencies[]`

| Key | Value |
| --- | --- |
| Meaning | One dependency; required ones are runtime assertions. |
| Runtime | `verified` |
| Evidence | DependencyReachable from the provider Pacto's Service endpoints and, with the node agent, the latest TCP connect success and failure timestamps from the target pods to the provider, per peer address |
| Rule | a peer is positive when its latest success is within the staleness bound and not older than its latest failure, negative when its latest failure is within the bound and newer than its latest success; timestamps from before the pod started are ignored; any positive peer makes the dependency reachable, otherwise any negative peer is a windowed contradiction; zero ready provider endpoints beyond the window contradict; a kernel positive never overrides a Kubernetes negative (Insufficient); kernel silence never contradicts |
| Outcomes | Compliant; DEPENDENCY_UNREACHABLE (error, warning when optional); OBSERVATION_UNSUPPORTED when the provider is not a Pacto target; DEPENDENCY_UNDECLARED (warning) for reachable peers no entry names |
| Limitations | the provider must be a sibling Pacto target in the cluster; external services are Unsupported |
| Tests | unit: `pkg/validation/evaluate_test.go:TestEvaluate_ContradictionPerDimension`, `pkg/validation/evaluate_test.go:TestEvaluate_UndeclaredDependency`, `pkg/validation/crossfield_test.go:TestValidateDependencyNamesUnique_Duplicate`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/dependencies[]/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/dependencies[]/invalid`; e2e: `tests/acceptance/kind/runtime-conformance.sh:dependency-reachable-kernel`, `tests/acceptance/kind/runtime-conformance.sh:dependency-refused`, `tests/acceptance/kind/runtime-conformance.sh:dependency-undeclared` |

| Platform | Evidence | Why | Tests |
| --- | --- | --- | --- |
| Kubernetes (the operator with its node agent) | `verified` | provider endpoints and, with the node agent, the latest connection success and failure | the e2e scenarios above |
| `pacto agent` (a Docker host or a server) | `verified` | the kernel's latest connection success and failure to the bound host:port, ignoring timestamps from before the workload started; a reachable checked workload no entry names is DEPENDENCY_UNDECLARED, by the same suppression rule | `internal/runtimewatch/evalsink/sink_test.go:TestEvaluate_UndeclaredPeers`, `internal/runtimewatch/evalsink/sink_test.go:TestEvaluate_BindingStart`, `tests/acceptance/local/runtime-docker.sh:docker-dependency-reachable`, `tests/acceptance/local/runtime-docker.sh:docker-dependency-refused`, `tests/acceptance/local/runtime-docker.sh:docker-dependency-undeclared` |
| `pacto run` (one wrapped command) | `partial` | eBPF mode sees successes and failures; unprivileged mode never sees a failure, so it never confirms unreachable | `tests/acceptance/local/runtime-run.sh:run-ebpf-dependency-reachable`, `tests/acceptance/local/runtime-run.sh:run-ebpf-dependency-refused`, `tests/acceptance/local/runtime-run.sh:run-unprivileged-dependency-reachable`, `tests/acceptance/local/runtime-run.sh:run-unprivileged-dependency-refused` |

### `dependencies[].name`

| Key | Value |
| --- | --- |
| Meaning | Dependency identifier; subject of its evidence. |
| Runtime | `not-applicable` |
| Evidence | identity only; the runtime check lives on dependencies[] |
| Rule | schema: required; cross-field: unique |
| Outcomes | SCHEMA_VIOLATION, DUPLICATE_DEPENDENCY_NAME |
| Limitations | the provider is found by ref, not by name |
| Tests | unit: `pkg/contract/contract_test.go:TestDependency_NameField`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/dependencies[].name/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/dependencies[].name/invalid` |

### `dependencies[].ref`

| Key | Value |
| --- | --- |
| Meaning | Reference to the provider's contract bundle. |
| Runtime | `partial` |
| Evidence | resolution key: matched against sibling Pacto status.contract.resolvedRef to find the provider Service |
| Rule | a ref that matches no sibling cannot be observed |
| Outcomes | OBSERVATION_UNSUPPORTED when no sibling matches; INVALID_OCI_REF and TAG_NOT_DIGEST (warning) statically; DEPENDENCY_REF_REQUIRED when missing on a dependency that is not external |
| Limitations | a ref without a scheme is a local path and gets no static check; only an external dependency may leave it out |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidateDependencyRefs_InvalidOCI`, `pkg/validation/crossfield_test.go:TestValidateDependencyRefs_TagNotDigestWarning`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/dependencies[].ref/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/dependencies[].ref/invalid`; e2e: `tests/acceptance/kind/runtime-conformance.sh:dependency-reachable-kernel` |

| Platform | Evidence | Why | Tests |
| --- | --- | --- | --- |
| Kubernetes (the operator with its node agent) | `partial` | resolution key matched against sibling Pacto resolvedRef to find the provider Service | the e2e scenarios above |
| `pacto agent` (a Docker host or a server) | `unverified` | the binding names the address; the ref is never resolved to a running provider | - |
| `pacto run` (one wrapped command) | `unverified` | --dep names the address; the ref is never resolved to a running provider | - |

### `dependencies[].required`

| Key | Value |
| --- | --- |
| Meaning | Whether the service cannot work without the provider. |
| Runtime | `partial` |
| Evidence | decides the severity axis of the dependency evidence |
| Rule | required: a gap is Unknown and a contradiction an error; optional: a gap is silent and a contradiction a warning |
| Outcomes | DEPENDENCY_UNREACHABLE or DEPENDENCY_VERSION_MISMATCH as warning when optional; SCHEMA_VIOLATION when omitted |
| Limitations | none beyond the parent |
| Tests | unit: `pkg/validation/evaluate_test.go:TestEvaluate_OptionalDependency`, `pkg/validation/schema_binding_test.go:TestSchema_DependencyMissingRequired_Rejected`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/dependencies[].required/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/dependencies[].required/invalid`; e2e: `tests/acceptance/kind/runtime-conformance.sh:dependency-optional-warning` |

| Platform | Evidence | Why | Tests |
| --- | --- | --- | --- |
| Kubernetes (the operator with its node agent) | `partial` | sets the severity axis of the dependency evidence | the e2e scenarios above |
| `pacto agent` (a Docker host or a server) | `partial` | sets the severity of a bound dependency's evidence; an unbound one is unsupported | `tests/acceptance/local/runtime-docker.sh:docker-dependency-refused` |
| `pacto run` (one wrapped command) | `partial` | sets the severity of a bound dependency's evidence; only eBPF mode confirms a failure | `tests/acceptance/local/runtime-run.sh:run-ebpf-dependency-refused` |

### `dependencies[].compatibility`

| Key | Value |
| --- | --- |
| Meaning | Semver range of provider versions this service accepts. |
| Runtime | `partial` |
| Evidence | DependencyObservation.Version: the provider Pacto's status.contract.version; when several targets match the ref, the only one inside the range is the target (none or several is Insufficient) |
| Rule | a version inside the range satisfies; outside contradicts; an unknown or non-semver version is unverified |
| Outcomes | Compliant; DEPENDENCY_VERSION_MISMATCH (error, warning when optional); listed in evaluationCoverage.unverified otherwise; INVALID_COMPATIBILITY statically |
| Limitations | the provider's contract version is trusted as its deployed version; judged only when the dependency is Observed |
| Tests | unit: `pkg/validation/evaluate_test.go:TestEvaluate_DependencyCompatibilityFacet`, `pkg/validation/crossfield_test.go:TestValidateDependencyRefs_InvalidCompatibility`, `integrations/kubernetes/internal/observer/regressions_test.go:TestDependency_VersionTieBreak`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/dependencies[].compatibility/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/dependencies[].compatibility/invalid`; e2e: `tests/acceptance/kind/runtime-conformance.sh:dependency-version-mismatch` |

| Platform | Evidence | Why | Tests |
| --- | --- | --- | --- |
| Kubernetes (the operator with its node agent) | `partial` | the provider Pacto's deployed contract version | the e2e scenarios above |
| `pacto agent` (a Docker host or a server) | `unverified` | no source off Kubernetes: no provider version is known, so the facet is listed in coverage.unverified | - |
| `pacto run` (one wrapped command) | `unverified` | no source off Kubernetes: no provider version is known, so the facet is listed in coverage.unverified | - |

### `dependencies[].external`

| Key | Value |
| --- | --- |
| Meaning | The dependency is not delivered by this Pacto system (it may still run in the same company). |
| Runtime | `static-only` |
| Evidence | none: it only changes expectations; Pacto does not expect to see it running and never moves it first |
| Rule | cross-field: a dependency without ref must be external with a version; pactoVersion 2.1 only |
| Outcomes | DEPENDENCY_REF_REQUIRED; NEEDS_PACTO_2_1 under 2.0 |
| Limitations | with a ref it is compared like any other dependency; reachability is still checked when bound |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidateDependencyForm`, `pkg/graph/resolver_test.go:TestResolve_ExternalDependency`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/dependencies[].external/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/dependencies[].external/invalid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/dependencies[].external/invalid-2.0` |

### `dependencies[].version`

| Key | Value |
| --- | --- |
| Meaning | Free-text version an external dependency with no ref runs against. |
| Runtime | `static-only` |
| Evidence | none: no contract to compare it with, so anything relying on it is Unknown |
| Rule | cross-field: required for an external dependency without ref, refused beside a ref; diff: any change is potentially breaking |
| Outcomes | EXTERNAL_VERSION_REQUIRED, VERSION_WITH_REF, COMPATIBILITY_WITHOUT_REF |
| Limitations | shown in the graph and in diffs only; compatibility can never be confirmed |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidateDependencyForm`, `pkg/diff/dependency_test.go:TestDiffDependencies_External`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/dependencies[].version/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/dependencies[].version/invalid` |

### `state`

| Key | Value |
| --- | --- |
| Meaning | How the service holds state. |
| Runtime | `not-applicable` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema: object with type, persistence and dataCriticality |
| Outcomes | PARSE_ERROR when not an object |
| Limitations | declaring it makes the operator produce a persistence observation |
| Tests | unit: `pkg/contract/parse_test.go:TestParse_ValidFull_State`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/state/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/state/invalid` |

### `state.type`

| Key | Value |
| --- | --- |
| Meaning | Statefulness of the service. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema enum; stateless forces ephemeral durability |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | no runtime signal distinguishes stateful from hybrid; durability and scope carry the checkable part |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidateStatePersistenceInvariants_StatelessPersistent`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/state.type/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/state.type/invalid` |

| Value | Runtime | Meaning | Rule | Outcomes | Limitations | Tests |
| --- | --- | --- | --- | --- | --- | --- |
| `stateless` | `static-only` | keeps no state between requests; durability must be ephemeral | schema enum (stateless also requires ephemeral durability) | no runtime outcome; only the static result below | not observed at runtime | unit: `pkg/validation/structural_test.go:TestStructural_V20RejectsBadEnumsAndRanges`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/state.type=stateless` |
| `stateful` | `static-only` | owns state that outlives a request | schema enum (stateless also requires ephemeral durability) | no runtime outcome; only the static result below | not observed at runtime | unit: `pkg/validation/structural_test.go:TestStructural_V20RejectsBadEnumsAndRanges`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/state.type=stateful` |
| `hybrid` | `static-only` | mixes stateless serving with some owned state | schema enum (stateless also requires ephemeral durability) | no runtime outcome; only the static result below | not observed at runtime | unit: `pkg/validation/structural_test.go:TestStructural_V20RejectsBadEnumsAndRanges`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/state.type=hybrid` |

### `state.persistence`

| Key | Value |
| --- | --- |
| Meaning | Where and how long state lives. |
| Runtime | `not-applicable` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema: required object with scope and durability |
| Outcomes | SCHEMA_VIOLATION when missing |
| Limitations | container |
| Tests | unit: `pkg/contract/parse_test.go:TestParse_ValidFull_State`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/state.persistence/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/state.persistence/invalid` |

### `state.persistence.scope`

| Key | Value |
| --- | --- |
| Meaning | Whether state is private to a replica or shared between replicas. |
| Runtime | `partial` |
| Evidence | PersistenceObservation.Shared from the pod template: one writable PVC claim is shared when more than one pod runs at once (Deployment, ReplicaSet or StatefulSet replicas, Job parallelism); StatefulSet volumeClaimTemplates give each replica its own claim; a claim opened read-only or mounted read-only by every container is ignored |
| Rule | local is satisfied by not shared and contradicted by shared; shared is satisfied by shared; shared with per-replica storage and an unknown value are unverified |
| Outcomes | Compliant; PERSISTENCE_MISMATCH (error) at state.persistence.scope; listed in evaluationCoverage.unverified otherwise; SCHEMA_VIOLATION statically |
| Limitations | a shared store outside the cluster (managed database) is invisible; checked for ephemeral durability too, without asserting durability |
| Tests | unit: `pkg/validation/evaluate_test.go:TestEvaluate_PersistenceScopeFacet`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/state.persistence.scope/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/state.persistence.scope/invalid`; e2e: `tests/acceptance/kind/runtime-conformance.sh:persistence-scope` |

| Platform | Evidence | Why | Tests |
| --- | --- | --- | --- |
| Kubernetes (the operator with its node agent) | `partial` | writable volume claims shared by more than one running pod | the e2e scenarios above |
| `pacto agent` (a Docker host or a server) | `unverified` | no source off Kubernetes: no volume claims, so the facet is listed in coverage.unverified | - |
| `pacto run` (one wrapped command) | `unverified` | no source off Kubernetes: no volume claims, so the facet is listed in coverage.unverified | - |

| Value | Runtime | Meaning | Rule | Outcomes | Limitations | Tests |
| --- | --- | --- | --- | --- | --- | --- |
| `local` | `verified` | Each replica owns its state. | false satisfies; true contradicts | Compliant or PERSISTENCE_MISMATCH | no PVC gives no fact (unverified) | unit: `pkg/validation/evaluate_test.go:TestEvaluate_PersistenceScopeFacet`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/state.persistence.scope=local`; e2e: `tests/acceptance/kind/runtime-conformance.sh:persistence-scope` |
| `shared` | `partial` | Replicas share one state store. | true satisfies; false is unverified, never a violation | Compliant or unverified | the shared store may live outside the cluster | unit: `pkg/validation/evaluate_test.go:TestEvaluate_PersistenceScopeFacet`, `integrations/kubernetes/internal/observer/exposure_test.go:TestSharedClaim`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/state.persistence.scope=shared`; e2e: `tests/acceptance/kind/runtime-conformance.sh:persistence-scope` |

### `state.persistence.durability`

| Key | Value |
| --- | --- |
| Meaning | Whether state must survive a restart. |
| Runtime | `partial` |
| Evidence | PersistenceObservation.Durable from the workload's volumes (a PVC or a non-ephemeral volume) |
| Rule | persistent with no durable binding contradicts; ephemeral is never asserted |
| Outcomes | Compliant; PERSISTENCE_MISMATCH (error) at state.persistence.durability; Unknown codes when unobservable; SCHEMA_VIOLATION statically |
| Limitations | requires a workload target; a volume claim proves storage exists, not that the application writes to it |
| Tests | unit: `pkg/validation/evaluate_test.go:TestEvaluate_ContradictionPerDimension`, `pkg/validation/evaluate_test.go:TestEvaluate_EphemeralStateNeverAssertsDurability`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/state.persistence.durability/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/state.persistence.durability/invalid`; e2e: `tests/acceptance/kind/runtime-conformance.sh:persistence-durability` |

| Platform | Evidence | Why | Tests |
| --- | --- | --- | --- |
| Kubernetes (the operator with its node agent) | `partial` | the workload's volumes | the e2e scenarios above |
| `pacto agent` (a Docker host or a server) | `unverified` | no source off Kubernetes; persistent durability reads EVIDENCE_MISSING (Unknown) | - |
| `pacto run` (one wrapped command) | `unverified` | no source off Kubernetes; persistent durability reads EVIDENCE_MISSING (Unknown) | - |

| Value | Runtime | Meaning | Rule | Outcomes | Limitations | Tests |
| --- | --- | --- | --- | --- | --- | --- |
| `persistent` | `verified` | State must survive restarts and rescheduling. | a durable volume binding satisfies; none contradicts | Compliant or PERSISTENCE_MISMATCH (required assertion) | durability semantics of the storage class are not checked | unit: `pkg/validation/evaluate_test.go:TestEvaluate_ContradictionPerDimension`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/state.persistence.durability=persistent`; e2e: `tests/acceptance/kind/runtime-conformance.sh:persistence-durability` |
| `ephemeral` | `static-only` | State may be lost on restart. | never asserted: keeping a volume is not a contradiction | no durability finding; scope is still judged | nothing to observe | unit: `pkg/validation/evaluate_test.go:TestEvaluate_EphemeralStateNeverAssertsDurability`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/state.persistence.durability=ephemeral` |

### `state.dataCriticality`

| Key | Value |
| --- | --- |
| Meaning | Business impact of losing the data. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema enum, required |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | no runtime signal for criticality |
| Tests | unit: `pkg/validation/structural_test.go:TestStructural_V20RejectsBadEnumsAndRanges`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/state.dataCriticality/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/state.dataCriticality/invalid` |

| Value | Runtime | Meaning | Rule | Outcomes | Limitations | Tests |
| --- | --- | --- | --- | --- | --- | --- |
| `low` | `static-only` | loss is an inconvenience | schema enum | no runtime outcome; only the static result below | not observed at runtime | unit: `pkg/validation/structural_test.go:TestStructural_V20RejectsBadEnumsAndRanges`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/state.dataCriticality=low` |
| `medium` | `static-only` | loss hurts but is recoverable | schema enum | no runtime outcome; only the static result below | not observed at runtime | unit: `pkg/validation/structural_test.go:TestStructural_V20RejectsBadEnumsAndRanges`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/state.dataCriticality=medium` |
| `high` | `static-only` | loss is a serious incident | schema enum | no runtime outcome; only the static result below | not observed at runtime | unit: `pkg/validation/structural_test.go:TestStructural_V20RejectsBadEnumsAndRanges`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/state.dataCriticality=high` |

### `workload`

| Key | Value |
| --- | --- |
| Meaning | Execution pattern of the workload. |
| Runtime | `verified` |
| Evidence | WorkloadObserved.Type mapped from the target workload kind (Deployment, StatefulSet, ReplicaSet to service; Job to job; CronJob to scheduled) |
| Rule | a different mapped type contradicts |
| Outcomes | Compliant; WORKLOAD_MISMATCH (error); Unknown codes when no workload target; SCHEMA_VIOLATION statically |
| Limitations | requires a workload reference on the Pacto CR |
| Tests | unit: `pkg/validation/evaluate_test.go:TestEvaluate_ContradictionPerDimension`, `pkg/contract/contract_test.go:TestWorkloadConstants`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/workload/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/workload/invalid`; e2e: `tests/acceptance/kind/runtime-conformance.sh:conforming-workload`, `tests/acceptance/kind/runtime-conformance.sh:workload-mismatch`, `tests/acceptance/kind/runtime-conformance.sh:workload-job`, `tests/acceptance/kind/runtime-conformance.sh:workload-scheduled` |

| Platform | Evidence | Why | Tests |
| --- | --- | --- | --- |
| Kubernetes (the operator with its node agent) | `verified` | the target workload kind | the e2e scenarios above |
| `pacto agent` (a Docker host or a server) | `unverified` | no source off Kubernetes; a declared workload reads EVIDENCE_MISSING (Unknown) | - |
| `pacto run` (one wrapped command) | `unverified` | no source off Kubernetes; a declared workload reads EVIDENCE_MISSING (Unknown) | - |

| Value | Runtime | Meaning | Rule | Outcomes | Limitations | Tests |
| --- | --- | --- | --- | --- | --- | --- |
| `service` | `verified` | Long-running server. | Deployment, StatefulSet or ReplicaSet satisfies | Compliant or WORKLOAD_MISMATCH | none | unit: `pkg/validation/evaluate_test.go:TestEvaluate_ContradictionPerDimension`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/workload=service`; e2e: `tests/acceptance/kind/runtime-conformance.sh:conforming-workload`, `tests/acceptance/kind/runtime-conformance.sh:workload-mismatch` |
| `job` | `verified` | Run-to-completion task. | Job satisfies | Compliant or WORKLOAD_MISMATCH | none | unit: `pkg/validation/evaluate_test.go:TestEvaluate_ContradictionPerDimension`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/workload=job`; e2e: `tests/acceptance/kind/runtime-conformance.sh:workload-job` |
| `scheduled` | `verified` | Task run on a schedule. | CronJob satisfies | Compliant or WORKLOAD_MISMATCH | none | unit: `pkg/validation/evaluate_test.go:TestEvaluate_ContradictionPerDimension`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/workload=scheduled`; e2e: `tests/acceptance/kind/runtime-conformance.sh:workload-scheduled` |

### `capabilities`

| Key | Value |
| --- | --- |
| Meaning | Operational capabilities the service offers. |
| Runtime | `not-applicable` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema: array |
| Outcomes | PARSE_ERROR when not a list |
| Limitations | container |
| Tests | unit: `pkg/contract/parse_test.go:TestParse_ValidFull_Capabilities`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/capabilities/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/capabilities/invalid` |

### `capabilities[]`

| Key | Value |
| --- | --- |
| Meaning | One capability; each is a required runtime assertion. |
| Runtime | `partial` |
| Evidence | CapabilityObserved from a health probe or a metrics scrape on the bound endpoint |
| Rule | an absent endpoint contradicts |
| Outcomes | Compliant; CAPABILITY_ABSENT (error); OBSERVATION_UNSUPPORTED when unbound; extensions listed under coverage.unverified; DUPLICATE_CAPABILITY statically |
| Limitations | extension capabilities have no evaluator |
| Tests | unit: `pkg/validation/evaluate_test.go:TestEvaluate_ContradictionPerDimension`, `pkg/validation/crossfield_test.go:TestValidateCapabilities_DuplicateStandard`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/capabilities[]/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/capabilities[]/invalid`; e2e: `tests/acceptance/kind/runtime-conformance.sh:health-capability`, `tests/acceptance/kind/runtime-conformance.sh:metrics-capability` |

| Platform | Evidence | Why | Tests |
| --- | --- | --- | --- |
| Kubernetes (the operator with its node agent) | `partial` | health probe or metrics scrape on the bound endpoint | the e2e scenarios above |
| `pacto agent` (a Docker host or a server) | `partial` | health probe or metrics scrape at the target's host and bound port; metrics has no reliable negative | `internal/runtimewatch/evalsink/sink_test.go:TestEvaluate_Compliant`, `internal/runtimewatch/evalsink/sink_test.go:TestEvaluate_NegativesAfterTheWindow` |
| `pacto run` (one wrapped command) | `partial` | health probe or metrics scrape on --host at the bound port; metrics has no reliable negative | `internal/runtimewatch/evalsink/sink_test.go:TestEvaluate_Compliant`, `internal/cli/run_internal_test.go:TestRunCommand_Unprivileged` |

### `capabilities[].type`

| Key | Value |
| --- | --- |
| Meaning | Kind of capability. |
| Runtime | `partial` |
| Evidence | CapabilityObservation.Present |
| Rule | health and metrics are observed; extension is never observed |
| Outcomes | Compliant, CAPABILITY_ABSENT or Unknown codes; SCHEMA_VIOLATION statically |
| Limitations | metrics needs --enable-metrics-observation |
| Tests | unit: `pkg/validation/structural_test.go:TestStructural_V20CapabilityTypes`, `pkg/contract/contract_test.go:TestCapability_StandardTypes`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/capabilities[].type/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/capabilities[].type/invalid`; e2e: `tests/acceptance/kind/runtime-conformance.sh:health-capability`, `tests/acceptance/kind/runtime-conformance.sh:metrics-capability` |

| Platform | Evidence | Why | Tests |
| --- | --- | --- | --- |
| Kubernetes (the operator with its node agent) | `partial` | health: absence confirmed by a 404; metrics: presence only | the e2e scenarios above |
| `pacto agent` (a Docker host or a server) | `partial` | health: absence confirmed by a 404; metrics: presence only | `internal/runtimewatch/evalsink/sink_test.go:TestEvaluate_NegativesAfterTheWindow`, `internal/runtimewatch/evalsink/sink_test.go:TestEvaluate_InconclusiveProbes` |
| `pacto run` (one wrapped command) | `partial` | health: absence confirmed by a 404; metrics: presence only | `internal/runtimewatch/evalsink/sink_test.go:TestEvaluate_NegativesAfterTheWindow` |

| Value | Runtime | Meaning | Rule | Outcomes | Limitations | Tests |
| --- | --- | --- | --- | --- | --- | --- |
| `health` | `verified` | A health endpoint. | tier A: an active probe of binding.path answers; tier B (probing off): an httpGet readiness probe on the bound port whose path equals binding.path, with ready endpoints | Compliant, CAPABILITY_ABSENT or EVIDENCE_INSUFFICIENT | tier B infers health from the readiness probe instead of calling it | unit: `pkg/validation/evaluate_test.go:TestEvaluate_ContradictionPerDimension`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/capabilities[].type=health`; e2e: `tests/acceptance/kind/runtime-conformance.sh:health-capability` |
| `metrics` | `partial` | A Prometheus metrics endpoint. | a scrape of the discovered endpoint (ServiceMonitor, PodMonitor, annotations, named port, then binding.path) returns metrics | Compliant, CAPABILITY_ABSENT or Unknown codes | only with --enable-metrics-observation; only the first metrics capability is observed | unit: `pkg/validation/evaluate_test.go:TestEvaluate_ContradictionPerDimension`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/capabilities[].type=metrics`; e2e: `tests/acceptance/kind/runtime-conformance.sh:metrics-capability` |
| `extension` | `static-only` | A namespaced, vendor-defined capability. | no evaluator is registered | listed under coverage.unverified as capabilities[ref=&lt;ref&gt;]; never a finding and never counted as required | Pacto cannot verify an extension, so a Compliant verdict says nothing about it; read coverage.unverified | unit: `pkg/validation/evaluate_test.go:TestEvaluate_ExtensionCapability`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/capabilities[].type=extension` |

### `capabilities[].ref`

| Key | Value |
| --- | --- |
| Meaning | Namespaced identifier of an extension capability. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema: required for extension and forbidden otherwise; cross-field: domain-qualified |
| Outcomes | CAPABILITY_REF_INVALID, SCHEMA_VIOLATION |
| Limitations | identity only |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidateCapabilities_Extension_InvalidRef`, `pkg/validation/evaluate_test.go:TestEvaluate_ExtensionIdentityByRef`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/capabilities[].ref/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/capabilities[].ref/invalid` |

### `capabilities[].binding`

| Key | Value |
| --- | --- |
| Meaning | Where the capability is served. |
| Runtime | `partial` |
| Evidence | decides whether the capability can be observed at all |
| Rule | health or metrics without a binding cannot be probed |
| Outcomes | OBSERVATION_UNSUPPORTED when missing; SCHEMA_VIOLATION on an extension |
| Limitations | none beyond the parent |
| Tests | unit: `pkg/validation/schema_binding_test.go:TestSchema_CapabilityBinding_UnboundHealthValid`, `pkg/validation/schema_binding_test.go:TestSchema_ExtensionWithBinding_Rejected`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/capabilities[].binding/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/capabilities[].binding/invalid`; e2e: `tests/acceptance/kind/runtime-conformance.sh:health-capability` |

| Platform | Evidence | Why | Tests |
| --- | --- | --- | --- |
| Kubernetes (the operator with its node agent) | `partial` | decides whether the capability can be probed at all | the e2e scenarios above |
| `pacto agent` (a Docker host or a server) | `partial` | an http binding on a bound interface is probed; anything else is unsupported | `internal/runtimewatch/evalsink/sink_test.go:TestEvaluate_UnprobeableCapabilities` |
| `pacto run` (one wrapped command) | `partial` | an http binding on a bound interface is probed; anything else is unsupported | `internal/runtimewatch/evalsink/sink_test.go:TestEvaluate_UnprobeableCapabilities` |

### `capabilities[].binding.type`

| Key | Value |
| --- | --- |
| Meaning | Transport of the capability endpoint. |
| Runtime | `partial` |
| Evidence | selects the probe; only http exists |
| Rule | http is probed |
| Outcomes | SCHEMA_VIOLATION for anything else |
| Limitations | no other transport can be observed |
| Tests | unit: `pkg/validation/schema_binding_test.go:TestSchema_CapabilityBinding_UnknownTransportRejected`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/capabilities[].binding.type/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/capabilities[].binding.type/invalid`; e2e: `tests/acceptance/kind/runtime-conformance.sh:health-capability` |

| Platform | Evidence | Why | Tests |
| --- | --- | --- | --- |
| Kubernetes (the operator with its node agent) | `partial` | selects the probe; only http exists | the e2e scenarios above |
| `pacto agent` (a Docker host or a server) | `partial` | selects the probe; only http exists | `internal/runtimewatch/evalsink/sink_test.go:TestEvaluate_UnprobeableCapabilities` |
| `pacto run` (one wrapped command) | `partial` | selects the probe; only http exists | `internal/runtimewatch/evalsink/sink_test.go:TestEvaluate_UnprobeableCapabilities` |

| Value | Runtime | Meaning | Rule | Outcomes | Limitations | Tests |
| --- | --- | --- | --- | --- | --- | --- |
| `http` | `partial` | Plain HTTP endpoint. | probed over HTTP | observed through capabilities[].type | the only transport | unit: `pkg/validation/schema_binding_test.go:TestSchema_CapabilityBinding_Valid`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/capabilities[].binding.type=http`; e2e: `tests/acceptance/kind/runtime-conformance.sh:health-capability` |

### `capabilities[].binding.interface`

| Key | Value |
| --- | --- |
| Meaning | Declared interface that owns the endpoint. |
| Runtime | `partial` |
| Evidence | mapped through the interface binding to the Service port that is probed |
| Rule | an unbound interface cannot be probed |
| Outcomes | CAPABILITY_INTERFACE_UNKNOWN statically; OBSERVATION_UNSUPPORTED at runtime when the interface is unbound |
| Limitations | none beyond the interface binding |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidateCapabilities_BindingInterfaceUnknown`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/capabilities[].binding.interface/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/capabilities[].binding.interface/invalid`; e2e: `tests/acceptance/kind/runtime-conformance.sh:health-capability` |

| Platform | Evidence | Why | Tests |
| --- | --- | --- | --- |
| Kubernetes (the operator with its node agent) | `partial` | mapped through the interface binding to the probed Service port | the e2e scenarios above |
| `pacto agent` (a Docker host or a server) | `partial` | mapped to the interface's bound port; an unbound interface leaves the capability unsupported | `internal/runtimewatch/evalsink/sink_test.go:TestEvaluate_UnprobeableCapabilities` |
| `pacto run` (one wrapped command) | `partial` | mapped to the interface's --bind port; an unbound interface leaves the capability unsupported | `internal/runtimewatch/evalsink/sink_test.go:TestEvaluate_UnprobeableCapabilities` |

### `capabilities[].binding.path`

| Key | Value |
| --- | --- |
| Meaning | Application path of the endpoint on its interface. |
| Runtime | `partial` |
| Evidence | tier A probes this path; tier B compares it with the readiness probe path; metrics falls back to it last |
| Rule | a tier B path that differs from the readiness probe path is insufficient, not a violation |
| Outcomes | CAPABILITY_PATH_INVALID statically (SSRF guard); EVIDENCE_INSUFFICIENT on a tier B path mismatch |
| Limitations | without probing, the path is only compared, never called |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidateCapabilities_BindingPathInvalid`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/capabilities[].binding.path/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/capabilities[].binding.path/invalid`; e2e: `tests/acceptance/kind/runtime-conformance.sh:health-capability`, `tests/acceptance/kind/runtime-conformance.sh:metrics-capability` |

| Platform | Evidence | Why | Tests |
| --- | --- | --- | --- |
| Kubernetes (the operator with its node agent) | `partial` | probed directly, or compared with the readiness probe path | the e2e scenarios above |
| `pacto agent` (a Docker host or a server) | `partial` | probed directly at the bound port; there is no readiness probe to fall back on | `internal/runtimewatch/evalsink/sink_test.go:TestEvaluate_Compliant` |
| `pacto run` (one wrapped command) | `partial` | probed directly at the bound port; there is no readiness probe to fall back on | `internal/runtimewatch/evalsink/sink_test.go:TestEvaluate_Compliant` |

### `readiness`

| Key | Value |
| --- | --- |
| Meaning | Production-readiness self-assessment. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema: object with expires and claims |
| Outcomes | PARSE_ERROR when not an object |
| Limitations | readiness claims are unverified attestations scored by pkg/readiness, never observed |
| Tests | unit: `pkg/contract/contract_test.go:TestContract_Readiness`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness/invalid` |

### `readiness.minScore`

| Key | Value |
| --- | --- |
| Meaning | Score the assessment must reach to pass the gate (default 100). |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema: 0 to 100; gate: score >= minScore |
| Outcomes | SCHEMA_VIOLATION; gate result in pacto validate --readiness and the operator ReadinessSatisfied condition |
| Limitations | readiness claims are unverified attestations scored by pkg/readiness, never observed |
| Tests | unit: `pkg/validation/structural_test.go:TestValidateStructuralRaw_V20_MinScoreOutOfRange`, `pkg/readiness/readiness_test.go:TestEvaluate_PassingGate`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.minScore/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.minScore/invalid` |

### `readiness.expires`

| Key | Value |
| --- | --- |
| Meaning | Date after which the assessment no longer counts. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | cross-field: canonical date (the schema format is not asserted) |
| Outcomes | INVALID_READINESS_EXPIRES; an expired assessment scores 0 |
| Limitations | readiness claims are unverified attestations scored by pkg/readiness, never observed |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidateReadiness_InvalidExpires`, `pkg/readiness/readiness_test.go:TestEvaluate_Expired`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.expires/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.expires/invalid` |

### `readiness.partialCredit`

| Key | Value |
| --- | --- |
| Meaning | Credit a partial claim earns (default 0.5). |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema: 0 to 1 |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | readiness claims are unverified attestations scored by pkg/readiness, never observed |
| Tests | unit: `pkg/readiness/readiness_test.go:TestEvaluate_CustomPartialCredit`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.partialCredit/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.partialCredit/invalid` |

### `readiness.history`

| Key | Value |
| --- | --- |
| Meaning | Revisions of the assessment. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema: array |
| Outcomes | PARSE_ERROR when not a list |
| Limitations | display only |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidateReadiness_AllRevisionFields`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.history/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.history/invalid` |

### `readiness.history[]`

| Key | Value |
| --- | --- |
| Meaning | One revision. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema: object with date, version, author and description |
| Outcomes | PARSE_ERROR when not an object |
| Limitations | display only |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidateReadiness_AllRevisionFields`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.history[]/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.history[]/invalid` |

### `readiness.history[].date`

| Key | Value |
| --- | --- |
| Meaning | Date of the revision. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema: required; cross-field: non-blank canonical date |
| Outcomes | INVALID_READINESS_REVISION |
| Limitations | display only |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidateReadiness_InvalidRevisionDate`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.history[].date/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.history[].date/invalid` |

### `readiness.history[].version`

| Key | Value |
| --- | --- |
| Meaning | Service version assessed. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema: required; cross-field: non-blank |
| Outcomes | INVALID_READINESS_REVISION |
| Limitations | display only |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidateReadiness_BlankRevisionFields`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.history[].version/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.history[].version/invalid` |

### `readiness.history[].author`

| Key | Value |
| --- | --- |
| Meaning | Who wrote the revision. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema: required; cross-field: non-blank |
| Outcomes | INVALID_READINESS_REVISION |
| Limitations | display only |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidateReadiness_BlankRevisionFields`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.history[].author/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.history[].author/invalid` |

### `readiness.history[].description`

| Key | Value |
| --- | --- |
| Meaning | What changed. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema: required; cross-field: non-blank |
| Outcomes | INVALID_READINESS_REVISION |
| Limitations | display only |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidateReadiness_BlankRevisionFields`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.history[].description/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.history[].description/invalid` |

### `readiness.claims`

| Key | Value |
| --- | --- |
| Meaning | Readiness claims. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema: required, at least one |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | readiness claims are unverified attestations scored by pkg/readiness, never observed |
| Tests | unit: `pkg/validation/structural_test.go:TestValidateStructuralRaw_V20_ReadinessEmptyClaims`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims/invalid` |

### `readiness.claims[]`

| Key | Value |
| --- | --- |
| Meaning | One claim. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | cross-field: unique id |
| Outcomes | DUPLICATE_READINESS_ID |
| Limitations | readiness claims are unverified attestations scored by pkg/readiness, never observed |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidateReadiness_DuplicateClaimID`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[]/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[]/invalid` |

### `readiness.claims[].id`

| Key | Value |
| --- | --- |
| Meaning | Claim identifier. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema pattern; cross-field: unique |
| Outcomes | SCHEMA_VIOLATION, DUPLICATE_READINESS_ID |
| Limitations | readiness claims are unverified attestations scored by pkg/readiness, never observed |
| Tests | unit: `pkg/validation/structural_test.go:TestValidateStructuralRaw_V20_ReadinessMissingID`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].id/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].id/invalid` |

### `readiness.claims[].type`

| Key | Value |
| --- | --- |
| Meaning | Kind of evidence the claim points at. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema enum |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | the evidence is not fetched or checked against its type |
| Tests | unit: `pkg/validation/structural_test.go:TestValidateStructuralRaw_V20_ReadinessInvalidType`, `pkg/contract/contract_test.go:TestEvidenceTypeConstants`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].type/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].type/invalid` |

| Value | Runtime | Meaning | Rule | Outcomes | Limitations | Tests |
| --- | --- | --- | --- | --- | --- | --- |
| `url` | `static-only` | a web page | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].type=url` |
| `document` | `static-only` | a document in the repository or a wiki | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].type=document` |
| `ticket` | `static-only` | an issue tracker entry | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].type=ticket` |
| `report` | `static-only` | a generated report | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].type=report` |
| `artifact` | `static-only` | a build or scan artifact | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].type=artifact` |
| `identifier` | `static-only` | an external identifier | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].type=identifier` |
| `other` | `static-only` | anything else | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].type=other` |

### `readiness.claims[].category`

| Key | Value |
| --- | --- |
| Meaning | Area of readiness the claim covers. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema enum |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | used for grouping in the dashboard only |
| Tests | unit: `pkg/contract/contract_test.go:TestReadinessCategoryConstants`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].category/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].category/invalid` |

| Value | Runtime | Meaning | Rule | Outcomes | Limitations | Tests |
| --- | --- | --- | --- | --- | --- | --- |
| `architecture` | `static-only` | readiness area: architecture | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].category=architecture` |
| `testing` | `static-only` | readiness area: testing | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].category=testing` |
| `code-quality` | `static-only` | readiness area: code-quality | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].category=code-quality` |
| `observability` | `static-only` | readiness area: observability | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].category=observability` |
| `security` | `static-only` | readiness area: security | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].category=security` |
| `documentation` | `static-only` | readiness area: documentation | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].category=documentation` |
| `infrastructure` | `static-only` | readiness area: infrastructure | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].category=infrastructure` |
| `ci-cd` | `static-only` | readiness area: ci-cd | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].category=ci-cd` |
| `deployment` | `static-only` | readiness area: deployment | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].category=deployment` |
| `resilience` | `static-only` | readiness area: resilience | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].category=resilience` |
| `backup-recovery` | `static-only` | readiness area: backup-recovery | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].category=backup-recovery` |
| `incident-response` | `static-only` | readiness area: incident-response | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].category=incident-response` |
| `compliance` | `static-only` | readiness area: compliance | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].category=compliance` |
| `other` | `static-only` | readiness area: other | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].category=other` |

### `readiness.claims[].status`

| Key | Value |
| --- | --- |
| Meaning | Self-reported state of the claim. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema enum; scored by pkg/readiness |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | readiness claims are unverified attestations scored by pkg/readiness, never observed |
| Tests | unit: `pkg/contract/contract_test.go:TestReadinessStatusConstants`, `pkg/readiness/readiness_test.go:TestEvaluate_ScoringWithStatuses`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].status/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].status/invalid` |

| Value | Runtime | Meaning | Rule | Outcomes | Limitations | Tests |
| --- | --- | --- | --- | --- | --- | --- |
| `done` | `static-only` | earns its full weight | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].status=done` |
| `partial` | `static-only` | earns weight times partialCredit | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].status=partial` |
| `not-done` | `static-only` | earns nothing | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].status=not-done` |
| `deferred` | `static-only` | excluded from the score | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].status=deferred` |

### `readiness.claims[].evidence`

| Key | Value |
| --- | --- |
| Meaning | Pointer to the evidence for the claim. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | cross-field: non-blank |
| Outcomes | EMPTY_READINESS_EVIDENCE |
| Limitations | never dereferenced |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidateReadiness_EmptyEvidence`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].evidence/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].evidence/invalid` |

### `readiness.claims[].weight`

| Key | Value |
| --- | --- |
| Meaning | Weight of the claim in the score. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema: non-negative integer |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | readiness claims are unverified attestations scored by pkg/readiness, never observed |
| Tests | unit: `pkg/validation/structural_test.go:TestValidateStructuralRaw_V20_BadReadinessWeight`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].weight/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].weight/invalid` |

### `readiness.claims[].description`

| Key | Value |
| --- | --- |
| Meaning | Human description of the claim. |
| Runtime | `static-only` |
| Evidence | none: the field has no runtime counterpart |
| Rule | cross-field: non-blank when present |
| Outcomes | EMPTY_READINESS_DESCRIPTION |
| Limitations | display only |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidateReadiness_BlankDescription`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].description/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/readiness.claims[].description/invalid` |

### `publish`

| Key | Value |
| --- | --- |
| Meaning | What the service publishes, where each artifact goes and how it is built. |
| Runtime | `static-only` |
| Evidence | none: pacto publish reads it; nothing at runtime does |
| Rule | schema: object with bundle; pactoVersion 2.1 only |
| Outcomes | PARSE_ERROR when not a map; SCHEMA_VIOLATION under 2.0 |
| Limitations | read by pacto publish only |
| Tests | unit: `pkg/validation/structural_test.go:TestValidateStructural_V21Publish`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/publish/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/publish/invalid` |

### `publish.bundle`

| Key | Value |
| --- | --- |
| Meaning | The repository the contract bundle is published to. |
| Runtime | `static-only` |
| Evidence | none: pacto publish reads it; nothing at runtime does |
| Rule | schema: oci://host/path, no tag or digest |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | the tag is always service.version |
| Tests | unit: `pkg/validation/structural_test.go:TestValidateStructural_V21Publish`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.bundle/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.bundle/invalid` |

### `publish.deploy`

| Key | Value |
| --- | --- |
| Meaning | The manifests artifact a Flux Kustomization applies; tagged last. |
| Runtime | `static-only` |
| Evidence | none: pacto publish reads it; nothing at runtime does |
| Rule | cross-field: names a manifests artifact |
| Outcomes | PUBLISH_DEPLOY_NOT_MANIFESTS |
| Limitations | charts are published, never deployed |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidatePublish`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.deploy/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.deploy/invalid` |

### `publish.artifacts`

| Key | Value |
| --- | --- |
| Meaning | The artifacts published beside the contract, built in order. |
| Runtime | `static-only` |
| Evidence | none: pacto publish reads it; nothing at runtime does |
| Rule | schema: array |
| Outcomes | PARSE_ERROR when not a list |
| Limitations | none |
| Tests | unit: `pkg/validation/structural_test.go:TestValidateStructural_V21Publish`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts/invalid` |

### `publish.artifacts[]`

| Key | Value |
| --- | --- |
| Meaning | One artifact. |
| Runtime | `static-only` |
| Evidence | none: pacto publish reads it; nothing at runtime does |
| Rule | cross-field: names unique |
| Outcomes | DUPLICATE_PUBLISH_ARTIFACT |
| Limitations | none |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidatePublish`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts[]/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts[]/invalid` |

### `publish.artifacts[].name`

| Key | Value |
| --- | --- |
| Meaning | The artifact name; later builds read its digest as PACTO_DIGEST_&lt;NAME&gt;. |
| Runtime | `static-only` |
| Evidence | none: pacto publish reads it; nothing at runtime does |
| Rule | schema: lower-case name |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | none |
| Tests | unit: `pkg/validation/structural_test.go:TestValidateStructural_V21Publish`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts[].name/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts[].name/invalid` |

### `publish.artifacts[].type`

| Key | Value |
| --- | --- |
| Meaning | What the artifact is. |
| Runtime | `static-only` |
| Evidence | none: pacto publish reads it; nothing at runtime does |
| Rule | schema enum |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | none |
| Tests | unit: `pkg/validation/structural_test.go:TestValidateStructural_V21Publish`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts[].type/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts[].type/invalid` |

| Value | Runtime | Meaning | Rule | Outcomes | Limitations | Tests |
| --- | --- | --- | --- | --- | --- | --- |
| `image` | `static-only` | a container image, pushed from an OCI image layout | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts[].type=image` |
| `chart` | `static-only` | a Helm chart, published but never deployed | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts[].type=chart` |
| `manifests` | `static-only` | a directory of Kubernetes manifests, packaged as a Flux artifact | - | - | - | integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts[].type=manifests` |

### `publish.artifacts[].ref`

| Key | Value |
| --- | --- |
| Meaning | The repository the artifact is published to. |
| Runtime | `static-only` |
| Evidence | none: pacto publish reads it; nothing at runtime does |
| Rule | schema: oci://host/path, no tag or digest |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | the tag is always service.version |
| Tests | unit: `pkg/validation/structural_test.go:TestValidateStructural_V21Publish`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts[].ref/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts[].ref/invalid` |

### `publish.artifacts[].build`

| Key | Value |
| --- | --- |
| Meaning | How the artifact is built: exactly one builder. |
| Runtime | `static-only` |
| Evidence | none: pacto publish reads it; nothing at runtime does |
| Rule | schema: exactly one of dockerfile or command |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | builders never push |
| Tests | unit: `pkg/validation/structural_test.go:TestValidateStructural_V21Publish`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts[].build/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts[].build/invalid` |

### `publish.artifacts[].build.dockerfile`

| Key | Value |
| --- | --- |
| Meaning | Build an image with docker buildx into an OCI image layout. |
| Runtime | `static-only` |
| Evidence | none: pacto publish reads it; nothing at runtime does |
| Rule | cross-field: images only |
| Outcomes | PUBLISH_DOCKERFILE_NOT_IMAGE |
| Limitations | needs docker buildx on the publishing host |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidatePublish`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts[].build.dockerfile/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts[].build.dockerfile/invalid` |

### `publish.artifacts[].build.dockerfile.context`

| Key | Value |
| --- | --- |
| Meaning | The build context. |
| Runtime | `static-only` |
| Evidence | none: pacto publish reads it; nothing at runtime does |
| Rule | schema: non-empty string |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | relative to the contract directory |
| Tests | unit: `pkg/validation/structural_test.go:TestValidateStructural_V21Publish`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts[].build.dockerfile.context/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts[].build.dockerfile.context/invalid` |

### `publish.artifacts[].build.dockerfile.file`

| Key | Value |
| --- | --- |
| Meaning | The Dockerfile. |
| Runtime | `static-only` |
| Evidence | none: pacto publish reads it; nothing at runtime does |
| Rule | schema: non-empty string |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | relative to the contract directory |
| Tests | unit: `pkg/validation/structural_test.go:TestValidateStructural_V21Publish`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts[].build.dockerfile.file/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts[].build.dockerfile.file/invalid` |

### `publish.artifacts[].build.dockerfile.platforms`

| Key | Value |
| --- | --- |
| Meaning | The platforms to build. |
| Runtime | `static-only` |
| Evidence | none: pacto publish reads it; nothing at runtime does |
| Rule | schema: array |
| Outcomes | PARSE_ERROR when not a list |
| Limitations | defaults to the builder platform |
| Tests | unit: `pkg/validation/structural_test.go:TestValidateStructural_V21Publish`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts[].build.dockerfile.platforms/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts[].build.dockerfile.platforms/invalid` |

### `publish.artifacts[].build.dockerfile.platforms[]`

| Key | Value |
| --- | --- |
| Meaning | One platform, such as linux/amd64. |
| Runtime | `static-only` |
| Evidence | none: pacto publish reads it; nothing at runtime does |
| Rule | schema: os/arch[/variant] |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | none |
| Tests | unit: `pkg/validation/structural_test.go:TestValidateStructural_V21Publish`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts[].build.dockerfile.platforms[]/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts[].build.dockerfile.platforms[]/invalid` |

### `publish.artifacts[].build.command`

| Key | Value |
| --- | --- |
| Meaning | A command that writes the artifact to $PACTO_OUT. |
| Runtime | `static-only` |
| Evidence | none: pacto publish reads it; nothing at runtime does |
| Rule | schema: non-empty array |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | runs in the contract directory; Pacto never edits its output |
| Tests | unit: `pkg/validation/structural_test.go:TestValidateStructural_V21Publish`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts[].build.command/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts[].build.command/invalid` |

### `publish.artifacts[].build.command[]`

| Key | Value |
| --- | --- |
| Meaning | One argument. |
| Runtime | `static-only` |
| Evidence | none: pacto publish reads it; nothing at runtime does |
| Rule | schema: non-empty string |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | not run through a shell |
| Tests | unit: `pkg/validation/structural_test.go:TestValidateStructural_V21Publish`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts[].build.command[]/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts[].build.command[]/invalid` |

### `publish.artifacts[].sbom`

| Key | Value |
| --- | --- |
| Meaning | The SPDX or CycloneDX JSON file the build writes; Pacto attaches it as a referrer and signs it. |
| Runtime | `static-only` |
| Evidence | none: pacto publish reads it; nothing at runtime does |
| Rule | schema: a .spdx.json or .cdx.json path; cross-field: images only |
| Outcomes | PUBLISH_SBOM_NOT_IMAGE |
| Limitations | Pacto never makes the SBOM; publish refuses a missing file or one that is not the format its name says |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidatePublish`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts[].sbom/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.artifacts[].sbom/invalid` |

### `publish.sign`

| Key | Value |
| --- | --- |
| Meaning | How each published digest is signed, with sigstore-go. |
| Runtime | `static-only` |
| Evidence | none: pacto publish reads it; nothing at runtime does |
| Rule | schema: exactly one of keyless, key and kms |
| Outcomes | SCHEMA_VIOLATION; pacto publish refuses when omitted |
| Limitations | the signature is a Sigstore bundle referrer that cosign verify and Flux check |
| Tests | unit: `pkg/validation/structural_test.go:TestValidateStructural_V21Publish`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.sign/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.sign/invalid` |

### `publish.sign.keyless`

| Key | Value |
| --- | --- |
| Meaning | Sign with the ambient OIDC identity. |
| Runtime | `static-only` |
| Evidence | none: pacto publish reads it; nothing at runtime does |
| Rule | schema: true only |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | needs identity and issuer, so Pacto can tell its own signature from anyone else's |
| Tests | unit: `pkg/validation/structural_test.go:TestValidateStructural_V21Publish`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.sign.keyless/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.sign.keyless/invalid` |

### `publish.sign.identity`

| Key | Value |
| --- | --- |
| Meaning | The certificate identity keyless signing produces. |
| Runtime | `static-only` |
| Evidence | none: pacto publish reads it; nothing at runtime does |
| Rule | schema: non-empty string, required with keyless |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | matched exactly when Pacto verifies; Flux needs its own matchOIDCIdentity |
| Tests | unit: `pkg/validation/structural_test.go:TestValidateStructural_V21Publish`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.sign.identity/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.sign.identity/invalid` |

### `publish.sign.issuer`

| Key | Value |
| --- | --- |
| Meaning | The OIDC issuer of the keyless identity. |
| Runtime | `static-only` |
| Evidence | none: pacto publish reads it; nothing at runtime does |
| Rule | schema: non-empty string, required with keyless |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | matched exactly when Pacto verifies |
| Tests | unit: `pkg/validation/structural_test.go:TestValidateStructural_V21Publish`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.sign.issuer/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.sign.issuer/invalid` |

### `publish.sign.key`

| Key | Value |
| --- | --- |
| Meaning | A PEM private key file, such as a cosign key. |
| Runtime | `static-only` |
| Evidence | none: pacto publish reads it; nothing at runtime does |
| Rule | schema: non-empty string |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | relative to the contract directory; the password comes from COSIGN_PASSWORD |
| Tests | unit: `pkg/validation/structural_test.go:TestValidateStructural_V21Publish`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.sign.key/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.sign.key/invalid` |

### `publish.sign.kms`

| Key | Value |
| --- | --- |
| Meaning | A KMS key URI. |
| Runtime | `static-only` |
| Evidence | none: pacto publish reads it; nothing at runtime does |
| Rule | schema: non-empty string |
| Outcomes | SCHEMA_VIOLATION |
| Limitations | awskms://, gcpkms://, azurekms:// or hashivault:// |
| Tests | unit: `pkg/validation/structural_test.go:TestValidateStructural_V21Publish`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.sign.kms/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.sign.kms/invalid` |

### `publish.sign.transparencyLog`

| Key | Value |
| --- | --- |
| Meaning | Upload signatures to the transparency log. |
| Runtime | `static-only` |
| Evidence | none: pacto publish reads it; nothing at runtime does |
| Rule | schema: boolean |
| Outcomes | PARSE_ERROR when not a boolean |
| Limitations | false is for private registries; keyless signing needs the log |
| Tests | unit: `pkg/validation/structural_test.go:TestValidateStructural_V21Publish`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.sign.transparencyLog/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/publish.sign.transparencyLog/invalid` |

### `checks`

| Key | Value |
| --- | --- |
| Meaning | Test or eval suites shipped with the version; a delivery gate hands one to a runner. |
| Runtime | `static-only` |
| Evidence | none: a suite gate in delivery asks a runner to run a suite, bound to its sha256 |
| Rule | schema: non-empty list; pactoVersion 2.1 only |
| Outcomes | PARSE_ERROR when not a list; NEEDS_PACTO_2_1 under 2.0 |
| Limitations | Pacto never reads a suite; its format is the runner's |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidateChecks`, `pkg/diff/checks_test.go:TestDiffChecks`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/checks/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/checks/invalid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/checks/invalid-2.0` |

### `checks[]`

| Key | Value |
| --- | --- |
| Meaning | One suite, by name and file. |
| Runtime | `static-only` |
| Evidence | none: a suite gate runs it by name |
| Rule | schema: name and file, nothing else; cross-field: names are unique; diff: added is non-breaking, removed or changed is potentially breaking |
| Outcomes | SCHEMA_VIOLATION, DUPLICATE_CHECK_NAME |
| Limitations | the diff cannot tell a stricter suite from a weaker one |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidateChecks`, `pkg/diff/checks_test.go:TestDiffChecksSuiteContent`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/checks[]/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/checks[]/invalid` |

### `checks[].name`

| Key | Value |
| --- | --- |
| Meaning | The suite's name; a delivery gate names it to run it. |
| Runtime | `static-only` |
| Evidence | none: identity only |
| Rule | schema: required non-empty string; cross-field: unique |
| Outcomes | SCHEMA_VIOLATION, DUPLICATE_CHECK_NAME |
| Limitations | none |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidateChecks`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/checks[].name/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/checks[].name/invalid` |

### `checks[].file`

| Key | Value |
| --- | --- |
| Meaning | The suite's file in the bundle; a runner reads it, Pacto only hashes it. |
| Runtime | `static-only` |
| Evidence | none: the suite gate sends its path and sha256 and accepts only a verdict about that digest |
| Rule | cross-field: file exists in the bundle |
| Outcomes | FILE_NOT_FOUND |
| Limitations | the file's format is the runner's |
| Tests | unit: `pkg/validation/crossfield_test.go:TestValidateChecks`, `pkg/delivery/eval_test.go:TestEvalSuiteBinding`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/checks[].file/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/checks[].file/invalid` |

### `metadata`

| Key | Value |
| --- | --- |
| Meaning | Free-form key/value annotations. |
| Runtime | `not-applicable` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema: object |
| Outcomes | PARSE_ERROR when not a map |
| Limitations | open object; displayed, never checked |
| Tests | unit: `pkg/contract/parse_test.go:TestParse_OptionalFields`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/metadata/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/metadata/invalid` |

### `extensions`

| Key | Value |
| --- | --- |
| Meaning | Free-form vendor extension data. |
| Runtime | `not-applicable` |
| Evidence | none: the field has no runtime counterpart |
| Rule | schema: object |
| Outcomes | PARSE_ERROR when not a map |
| Limitations | open object; no Pacto code reads it |
| Tests | unit: `pkg/contract/parse_test.go:TestParse_OptionalFields`; integration: `tests/integration/field_coverage_test.go:TestFieldCoverage/extensions/valid`, `tests/integration/field_coverage_test.go:TestFieldCoverage/extensions/invalid` |
