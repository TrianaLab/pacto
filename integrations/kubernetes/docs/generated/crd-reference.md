<!--
  GENERATED FILE -- DO NOT EDIT.
  Produced by release/scripts/gen_integration_docs.py from config/crd/bases/*.yaml.
  Regenerate with `make docs-generate`; drift is a CI failure (`make docs-check`).
-->

# CRD reference

Custom Resource Definitions installed by the operator. Field lists below are generated from the authoritative OpenAPI v3 schema in `config/crd/bases/`.


## PactoRevision

- **API group**: `pacto.trianalab.io`
- **Version**: `v1alpha1`
- **Kind**: `PactoRevision`
- **Plural**: `pactorevisions`
- **Scope**: Namespaced

### Additional printer columns

| Name | Type | JSONPath |
| --- | --- | --- |
| Version | string | `.spec.version` |
| Pacto | string | `.spec.pactoRef` |
| Resolved | boolean | `.status.resolved` |
| Age | date | `.metadata.creationTimestamp` |

### Fields

| Field | Type | Required | Default | Enum | Description |
| --- | --- | --- | --- | --- | --- |
| `spec` | `object` | yes |  |  | spec defines the desired state of PactoRevision. |
| `spec.pactoRef` | `string` | yes |  |  | PactoRef is the name of the parent Pacto resource that owns this revision. |
| `spec.serviceName` | `string` | no |  |  | ServiceName is the service name from the contract. |
| `spec.source` | `object` | yes |  |  | Source specifies where this revision's contract was loaded from. |
| `spec.source.digest` | `string` | no |  |  | Digest is the OCI manifest digest (sha256:...) at the time of resolution. Used to detect force-pushes (tag overwrites) on the registry. |
| `spec.source.inline` | `boolean` | no |  |  | Inline indicates the contract was provided inline (no external source). |
| `spec.source.oci` | `string` | no |  |  | OCI is the fully resolved OCI reference (including tag/digest). Example: ghcr.io/org/service-pacto:1.2.0 |
| `spec.version` | `string` | yes |  |  | Version is the contract version (from contract.service.version). |
| `status` | `object` | no |  |  | status defines the observed state of PactoRevision. |
| `status.conditions` | `[]object` | no |  |  | conditions represent the current state of the PactoRevision resource. |
| `status.conditions[].lastTransitionTime` | `string` | yes |  |  | lastTransitionTime is the last time the condition transitioned from one status to another. This should be when the underlying condition changed. If that is not known, then using the time when the API field changed is acceptable. |
| `status.conditions[].message` | `string` | yes |  |  | message is a human readable message indicating details about the transition. This may be an empty string. |
| `status.conditions[].observedGeneration` | `integer` | no |  |  | observedGeneration represents the .metadata.generation that the condition was set based upon. For instance, if .metadata.generation is currently 12, but the .status.conditions[x].observedGeneration is 9, the condition is out of date with respect to the current state of the instance. |
| `status.conditions[].reason` | `string` | yes |  |  | reason contains a programmatic identifier indicating the reason for the condition's last transition. Producers of specific condition types may define expected values and meanings for this field, and whether the values are considered a guaranteed API. The value should be a CamelCase string. This field may not be empty. |
| `status.conditions[].status` | `string` | yes |  | `True`, `False`, `Unknown` | status of the condition, one of True, False, Unknown. |
| `status.conditions[].type` | `string` | yes |  |  | type of condition in CamelCase or in foo.example.com/CamelCase. |
| `status.contractHash` | `string` | no |  |  | ContractHash is the SHA-256 hash of the raw contract YAML. Used to detect content changes across versions. |
| `status.createdAt` | `string` | no |  |  | CreatedAt is the timestamp when this revision was first resolved. |
| `status.resolved` | `boolean` | yes |  |  | Resolved indicates whether this revision has been successfully resolved and parsed. |


## Pacto

- **API group**: `pacto.trianalab.io`
- **Version**: `v1alpha1`
- **Kind**: `Pacto`
- **Plural**: `pactos`
- **Scope**: Namespaced

### Additional printer columns

| Name | Type | JSONPath |
| --- | --- | --- |
| Status | string | `.status.contractStatus` |
| Service | string | `.spec.target.serviceName` |
| Version | string | `.status.contractVersion` |
| Errors | integer | `.status.summary.errorCount` |
| Warnings | integer | `.status.summary.warningCount` |
| Last Reconciled | date | `.status.lastReconciledAt` |
| Age | date | `.metadata.creationTimestamp` |

### Fields

| Field | Type | Required | Default | Enum | Description |
| --- | --- | --- | --- | --- | --- |
| `spec` | `object` | yes |  |  | spec defines the desired state of Pacto. |
| `spec.checkIntervalSeconds` | `integer` | no | `300` |  | CheckIntervalSeconds controls how often the reconciler re-checks compliance. Defaults to 300 (5 minutes). |
| `spec.contractRef` | `object` | yes |  |  | ContractRef specifies where to find the Pacto contract. |
| `spec.contractRef.inline` | `string` | no |  |  | Inline allows specifying the contract YAML directly (for testing/dev). |
| `spec.contractRef.oci` | `string` | no |  |  | OCI is the OCI registry reference for the contract bundle. Three forms are supported: - Unversioned (ghcr.io/org/service-pacto): tracks the latest semver tag. - Tagged (ghcr.io/org/service-pacto:1.2.3): pinned to that exact tag. - Digest (ghcr.io/org/service-pacto@sha256:...): immutable, exact reference. |
| `spec.contractRef.pullSecretRef` | `string` | no |  |  | PullSecretRef is the name of a Secret in the same namespace containing OCI registry credentials. Supported secret types: - Opaque with "token" key (bearer token) - Opaque with "username"+"password" keys (basic auth) - kubernetes.io/dockerconfigjson (standard Docker registry auth) For Opaque secrets, add a "registry" key (e.g. "ghcr.io") to bind the credentials to that host; the operator then refuses to send them to any other registry. Recommended to prevent a contract from redirecting a referenced secret to an attacker-controlled registry. |
| `spec.overrides` | `object` | no |  |  | Overrides specifies partial configuration overrides to apply on top of the resolved contract. This enables environment-specific tuning without duplicating the entire contract inline. Semantics mirror the Pacto CLI --set / -f override model. |
| `spec.overrides.configurations` | `[]object` | no |  |  | Configurations lists per-scope configuration value overrides. Each entry is matched by name to a configurations[] entry in the resolved contract. |
| `spec.overrides.configurations[].name` | `string` | yes |  |  | Name identifies the configuration scope to override (must match a configurations[] entry in the contract). |
| `spec.overrides.configurations[].values` | `object` | yes |  |  | Values contains the configuration key-value pairs to merge into the resolved contract. These values take precedence over the values declared in the contract. |
| `spec.target` | `object` | no |  |  | Target specifies which Kubernetes resources to observe. When omitted, the Pacto acts as a reference-only contract (no runtime validation). |
| `spec.target.configBindings` | `[]object` | no |  |  | ConfigBindings maps contract configurations to the concrete ConfigMap/Secret backing each (B7). |
| `spec.target.configBindings[].configuration` | `string` | yes |  |  | Configuration is the contract configurations[].name this binding backs. |
| `spec.target.configBindings[].format` | `string` | no |  | `yaml`, `json` | Format of the value at Key. Required with Key for ConfigMap conformance. |
| `spec.target.configBindings[].key` | `string` | no |  |  | Key names the single ConfigMap/Secret key to decode and validate. Omit for existence-only. |
| `spec.target.configBindings[].kind` | `string` | yes |  | `ConfigMap`, `Secret` | Kind of the backing object. |
| `spec.target.configBindings[].name` | `string` | yes |  |  | Name of the backing ConfigMap/Secret in the Pacto's namespace. |
| `spec.target.interfaceBindings` | `[]object` | no |  |  | InterfaceBindings maps contract interfaces to the Service port that serves each (B4). Kubernetes deployment knowledge — lives on the CR, NOT the platform-agnostic contract. |
| `spec.target.interfaceBindings[].interface` | `string` | yes |  |  | Interface is the contract interfaces[].name this binding resolves. |
| `spec.target.interfaceBindings[].servicePort` | `object` | yes |  |  | ServicePort is the Service port name or number that serves the interface. |
| `spec.target.serviceName` | `string` | no |  |  | ServiceName is the name of the Kubernetes Service to observe. |
| `spec.target.workloadRef` | `object` | no |  |  | WorkloadRef identifies the workload (Deployment, StatefulSet, ReplicaSet, Job, or CronJob). If omitted, defaults to name=serviceName, kind=Deployment. |
| `spec.target.workloadRef.kind` | `string` | no |  | `Deployment`, `StatefulSet`, `ReplicaSet`, `Job`, `CronJob` | Kind of the workload resource. Left unspecified (empty) unless the author sets it explicitly; WORKLOAD_MISMATCH only fires when BOTH name AND kind were explicit (AR7). No default is applied here so the collector can distinguish "author asserted kind X" from "kind was defaulted for the GET". |
| `spec.target.workloadRef.name` | `string` | yes |  |  | Name of the workload resource. |
| `status` | `object` | no |  |  | status defines the observed state of Pacto. |
| `status.capabilities` | `[]object` | no |  |  | Capabilities lists the declared capabilities from the contract. |
| `status.capabilities[].ref` | `string` | no |  |  | Ref is the capability reference (OCI URI or local path). |
| `status.capabilities[].type` | `string` | yes |  |  | Type is the capability type (e.g. "database", "cache"). |
| `status.conditions` | `[]object` | no |  |  | Conditions represent aggregated state (ContractValid, RuntimeObserved, etc.). |
| `status.conditions[].lastTransitionTime` | `string` | yes |  |  | lastTransitionTime is the last time the condition transitioned from one status to another. This should be when the underlying condition changed. If that is not known, then using the time when the API field changed is acceptable. |
| `status.conditions[].message` | `string` | yes |  |  | message is a human readable message indicating details about the transition. This may be an empty string. |
| `status.conditions[].observedGeneration` | `integer` | no |  |  | observedGeneration represents the .metadata.generation that the condition was set based upon. For instance, if .metadata.generation is currently 12, but the .status.conditions[x].observedGeneration is 9, the condition is out of date with respect to the current state of the instance. |
| `status.conditions[].reason` | `string` | yes |  |  | reason contains a programmatic identifier indicating the reason for the condition's last transition. Producers of specific condition types may define expected values and meanings for this field, and whether the values are considered a guaranteed API. The value should be a CamelCase string. This field may not be empty. |
| `status.conditions[].status` | `string` | yes |  | `True`, `False`, `Unknown` | status of the condition, one of True, False, Unknown. |
| `status.conditions[].type` | `string` | yes |  |  | type of condition in CamelCase or in foo.example.com/CamelCase. |
| `status.configurations` | `[]object` | no |  |  | Configurations lists the contract's named configuration scopes. Each entry corresponds to one configurations[] entry in the contract. |
| `status.configurations[].hasSchema` | `boolean` | yes |  |  | HasSchema indicates whether a JSON Schema file is bundled. |
| `status.configurations[].name` | `string` | yes |  |  | Name is the configuration scope name (required, unique within contract). |
| `status.configurations[].overriddenKeys` | `[]string` | no |  |  | OverriddenKeys lists configuration keys whose values were overridden by spec.overrides.configurations. Empty when no overrides apply. |
| `status.configurations[].properties` | `[]object` | no |  |  | Properties lists the configuration's declared keys with their type and default/value, extracted from the bundled schema (or literal values), so consumers can render configuration content without re-reading the bundle. |
| `status.configurations[].properties[].key` | `string` | yes |  |  | Key is the property name (dot-notation for nested objects). |
| `status.configurations[].properties[].type` | `string` | no |  |  | Type is the JSON Schema type of the property. |
| `status.configurations[].properties[].value` | `string` | no |  |  | Value is the default (for schema properties) or the literal value. |
| `status.configurations[].ref` | `string` | no |  |  | Ref is the external OCI reference for the configuration schema, if used. |
| `status.configurations[].secretKeys` | `[]string` | no |  |  | SecretKeys lists configuration keys whose values reference secrets. |
| `status.configurations[].valueKeys` | `[]string` | no |  |  | ValueKeys lists the declared configuration value keys. |
| `status.contract` | `object` | no |  |  | Contract exposes parsed contract metadata. |
| `status.contract.owner` | `object` | no |  |  | Owner contains the structured ownership metadata from the contract (team, DRI, and contacts). |
| `status.contract.owner.contacts` | `[]object` | no |  |  | Contacts lists provider-neutral contact points. |
| `status.contract.owner.contacts[].purpose` | `string` | no |  |  | Purpose describes what this contact is used for (e.g. escalation, support, oncall). |
| `status.contract.owner.contacts[].type` | `string` | yes |  |  | Type is the contact channel type (e.g. email, chat, oncall). |
| `status.contract.owner.contacts[].value` | `string` | yes |  |  | Value is the contact address or identifier. |
| `status.contract.owner.dri` | `string` | no |  |  | DRI is the directly responsible individual. |
| `status.contract.owner.team` | `string` | no |  |  | Team is the owning team name. |
| `status.contract.ownerDisplay` | `string` | no |  |  | OwnerDisplay is the canonical display string derived from owner metadata. Precedence: team > DRI. Useful for printer columns, dashboards, and backward-compatible consumers. |
| `status.contract.resolvedRef` | `string` | no |  |  | ResolvedRef is the fully-resolved OCI reference (with tag/digest). Empty for inline contracts. |
| `status.contract.serviceName` | `string` | yes |  |  | ServiceName is the service name declared in the contract. |
| `status.contract.version` | `string` | yes |  |  | Version is the semver version from the contract. |
| `status.contractStatus` | `string` | no |  | `Compliant`, `Warning`, `NonCompliant`, `Reference`, `Unknown`, `Invalid`, `NotEvaluated` | ContractStatus is the high-level contract compliance state. This reflects contract validation/compliance and is NOT runtime health. |
| `status.contractVersion` | `string` | no |  |  | ContractVersion is the version from the parsed contract. Kept for backward compatibility and simple access via JSONPath. |
| `status.currentRevision` | `string` | no |  |  | CurrentRevision is the name of the active PactoRevision. |
| `status.dependencies` | `[]object` | no |  |  | Dependencies lists the declared dependencies from the contract. |
| `status.dependencies[].compatibility` | `string` | no |  |  | Compatibility is the semver constraint for the dependency. |
| `status.dependencies[].name` | `string` | yes |  |  | Name is the dependency name (required, unique within contract). |
| `status.dependencies[].ref` | `string` | yes |  |  | Ref is the dependency reference (OCI URI). |
| `status.dependencies[].required` | `boolean` | yes |  |  | Required indicates whether this dependency is mandatory. |
| `status.evaluationCoverage` | `object` | no |  |  | EvaluationCoverage reports how many required assertions were evaluated (metadata; never affects ContractStatus). |
| `status.evaluationCoverage.evaluated` | `integer` | yes |  |  | Evaluated is the number of required assertions with a conclusive (Observed) observation. |
| `status.evaluationCoverage.required` | `integer` | yes |  |  | Required is the total number of required assertions the contract declares. |
| `status.findings` | `[]object` | no |  |  | Findings is the list of typed conclusions from the Pacto engine. Includes both contract-only findings (structural/semantic) and evidence-based findings (runtime drift). |
| `status.findings[].category` | `string` | yes |  |  | Category groups related codes (e.g. RuntimeDrift, PolicyViolation). |
| `status.findings[].code` | `string` | yes |  |  | Code is the stable finding identifier (e.g. WORKLOAD_MISMATCH). |
| `status.findings[].contractPath` | `string` | no |  |  | ContractPath is the YAML path to the relevant contract field. |
| `status.findings[].evidenceRefs` | `[]object` | no |  |  | EvidenceRefs links the finding to supporting evidence. |
| `status.findings[].evidenceRefs[].observedAt` | `string` | yes |  |  | ObservedAt is the ISO8601 timestamp when the evidence was collected. |
| `status.findings[].evidenceRefs[].source` | `string` | yes |  |  | Source is the evidence source (e.g. "k8s"). |
| `status.findings[].message` | `string` | yes |  |  | Message is a human-readable description. |
| `status.findings[].severity` | `string` | yes |  | `error`, `warning`, `info`, `unknown` | Severity is error, warning, info, or unknown. |
| `status.findings[].subject` | `string` | no |  |  | Subject identifies what the finding is about. |
| `status.interfaces` | `[]object` | no |  |  | Interfaces lists the parsed interfaces from the contract. |
| `status.interfaces[].name` | `string` | yes |  |  | Name is the interface name. |
| `status.interfaces[].ref` | `string` | no |  |  | Ref is the bundle-relative path to the interface specification file. |
| `status.interfaces[].type` | `string` | yes |  | `openapi`, `asyncapi`, `grpc` | Type is the interface type: openapi, asyncapi, or grpc. |
| `status.interfaces[].visibility` | `string` | no |  |  | Visibility is the declared visibility: public or internal. |
| `status.lastReconciledAt` | `string` | no |  |  | LastReconciledAt is when the last reconciliation completed. |
| `status.metadata` | `object` | no |  |  | Metadata contains arbitrary key-value pairs from the contract's metadata section. |
| `status.observationWindows` | `[]object` | no |  |  | ObservationWindows tracks per-assertion negative-observation streaks for time-based stabilization (B5). Operator-owned temporal state; the pure engine never sees it. |
| `status.observationWindows[].firstObservedNegativeAt` | `string` | yes |  |  | FirstObservedNegativeAt is when the current negative streak began. |
| `status.observationWindows[].kind` | `string` | yes |  |  | Kind is the assertion dimension: interface \| dependency \| configuration \| capability. |
| `status.observationWindows[].subject` | `string` | yes |  |  | Subject is the assertion identity (interface/dependency/configuration name, or capability key). |
| `status.observedGeneration` | `integer` | no |  |  | ObservedGeneration is the most recent generation observed by the controller. |
| `status.observedRuntime` | `object` | no |  |  | ObservedRuntime describes the actual runtime state observed from the cluster. Only populated when a target workload exists. This is a lean evidence view. |
| `status.observedRuntime.containerImages` | `[]string` | no |  |  | ContainerImages lists the container images from the pod spec. |
| `status.observedRuntime.deploymentStrategy` | `string` | no |  |  | DeploymentStrategy is the observed strategy (RollingUpdate, Recreate). Empty for non-Deployments. |
| `status.observedRuntime.hasEmptyDir` | `boolean` | yes |  |  | HasEmptyDir indicates whether the workload uses emptyDir volumes. |
| `status.observedRuntime.hasPVC` | `boolean` | yes |  |  | HasPVC indicates whether the workload uses PersistentVolumeClaims. |
| `status.observedRuntime.healthProbeInitialDelaySeconds` | `integer` | no |  |  | HealthProbeInitialDelaySeconds is the observed initialDelaySeconds from the first container's probe. |
| `status.observedRuntime.podManagementPolicy` | `string` | no |  |  | PodManagementPolicy is the observed pod management policy (OrderedReady, Parallel). Empty for non-StatefulSets. |
| `status.observedRuntime.terminationGracePeriodSeconds` | `integer` | no |  |  | TerminationGracePeriodSeconds is the observed terminationGracePeriodSeconds from the pod spec. |
| `status.observedRuntime.workloadKind` | `string` | no |  |  | WorkloadKind is the actual Kubernetes resource kind (Deployment, StatefulSet, ReplicaSet, Job, CronJob). |
| `status.policies` | `[]object` | no |  |  | Policies lists the contract's declared policy sources (metadata only). Each entry describes a local schema or external ref; the operator does not resolve or enforce ref-based policies at runtime. |
| `status.policies[].description` | `string` | no |  |  | Description is the policy schema's declared description, if any. |
| `status.policies[].hasSchema` | `boolean` | yes |  |  | HasSchema indicates whether a policy schema file is bundled. |
| `status.policies[].name` | `string` | yes |  |  | Name is the policy name (required, unique within contract). |
| `status.policies[].properties` | `[]object` | no |  |  | Properties lists the policy schema's keys with their type and default, so consumers can render policy content without re-reading the bundle. |
| `status.policies[].properties[].key` | `string` | yes |  |  | Key is the property name (dot-notation for nested objects). |
| `status.policies[].properties[].type` | `string` | no |  |  | Type is the JSON Schema type of the property. |
| `status.policies[].properties[].value` | `string` | no |  |  | Value is the default (for schema properties) or the literal value. |
| `status.policies[].ref` | `string` | no |  |  | Ref is the external OCI reference for the policy schema, if used. |
| `status.policies[].schema` | `string` | no |  |  | Schema is the bundle-relative path to the policy schema file, if local. |
| `status.policies[].title` | `string` | no |  |  | Title is the policy schema's declared title, if any. |
| `status.readiness` | `object` | no |  |  | Readiness is the derived operational readiness assessment of the contract. It is computed from the contract's declared readiness claims and the current time. It is a separate dimension from contract compliance and does NOT affect ContractStatus. Absent when the contract declares no readiness. |
| `status.readiness.claims` | `[]object` | no |  |  | Claims is the derived per-claim readiness status. |
| `status.readiness.claims[].category` | `string` | no |  |  | Category is the software-domain category (security, documentation, observability, etc.). |
| `status.readiness.claims[].description` | `string` | no |  |  | Description is the optional human-readable explanation. |
| `status.readiness.claims[].earnedWeight` | `integer` | no |  |  | EarnedWeight is the weight this check contributed toward the score. |
| `status.readiness.claims[].evidence` | `string` | no |  |  | Evidence is the declared pointer to the evidence. |
| `status.readiness.claims[].excluded` | `boolean` | no |  |  | Excluded reports whether the check is excluded from scoring (deferred). |
| `status.readiness.claims[].id` | `string` | yes |  |  | ID is the readiness requirement identifier (e.g. dashboard, runbook). |
| `status.readiness.claims[].status` | `string` | yes |  | `done`, `partial`, `not-done`, `deferred` | Status is the declared completion status. |
| `status.readiness.claims[].type` | `string` | yes |  |  | Type classifies the evidence pointer (url, document, ticket, report, artifact, identifier, other). |
| `status.readiness.claims[].weight` | `integer` | yes |  |  | Weight is the declared contribution to the readiness score (0-100). |
| `status.readiness.daysRemaining` | `integer` | no |  |  | DaysRemaining is the number of whole days until expiry (nil when expired). |
| `status.readiness.deferredCount` | `integer` | no |  |  | DeferredCount is the number of checks declared deferred (excluded from scoring). |
| `status.readiness.doneCount` | `integer` | no |  |  | DoneCount is the number of checks declared done. |
| `status.readiness.earnedWeight` | `integer` | no |  |  | EarnedWeight is the weight earned toward the score. |
| `status.readiness.expired` | `boolean` | no |  |  | Expired reports whether the assessment has expired; when true every in-scope check earns 0. |
| `status.readiness.expires` | `string` | no |  |  | Expires is the assessment-level expiry boundary (YYYY-MM-DD). |
| `status.readiness.minScore` | `integer` | no |  |  | MinScore is the gate threshold (the declared readiness.minScore, or 100 when omitted). |
| `status.readiness.notDoneCount` | `integer` | no |  |  | NotDoneCount is the number of checks declared not-done. |
| `status.readiness.partialCount` | `integer` | no |  |  | PartialCount is the number of checks declared partial. |
| `status.readiness.passing` | `boolean` | no |  |  | Passing reports whether the gate is met (not expired and Score >= MinScore). |
| `status.readiness.revisions` | `[]object` | no |  |  | Revisions is the declared readiness revision history. |
| `status.readiness.revisions[].author` | `string` | yes |  |  | Author is who performed the assessment. |
| `status.readiness.revisions[].date` | `string` | yes |  |  | Date is the date the revision was assessed (YYYY-MM-DD). |
| `status.readiness.revisions[].description` | `string` | yes |  |  | Description summarizes what changed or was observed. |
| `status.readiness.revisions[].version` | `string` | yes |  |  | Version is the service version assessed in this revision. |
| `status.readiness.score` | `integer` | no |  |  | Score is the percentage of in-scope weight earned (0-100). |
| `status.readiness.totalWeight` | `integer` | no |  |  | TotalWeight is the sum of in-scope (non-deferred) check weights. |
| `status.resolutionPolicy` | `string` | no |  | `Latest`, `PinnedTag`, `PinnedDigest` | ResolutionPolicy describes how the OCI reference was resolved. Latest: unversioned ref, operator tracks the highest semver tag. PinnedTag: ref includes an explicit tag, used as-is. PinnedDigest: ref includes a digest, used as-is (immutable). Empty for inline contracts. |
| `status.resources` | `object` | no |  |  | Resources describes the existence of target Kubernetes resources. |
| `status.resources.service` | `object` | no |  |  | Service describes the target Service. |
| `status.resources.service.exists` | `boolean` | yes |  |  | Exists indicates whether the resource was found in the cluster. |
| `status.resources.service.kind` | `string` | no |  |  | Kind of the resource (only set for workloads). |
| `status.resources.service.name` | `string` | yes |  |  | Name of the resource. |
| `status.resources.workload` | `object` | no |  |  | Workload describes the target workload (Deployment/StatefulSet/ReplicaSet). |
| `status.resources.workload.exists` | `boolean` | yes |  |  | Exists indicates whether the resource was found in the cluster. |
| `status.resources.workload.kind` | `string` | no |  |  | Kind of the resource (only set for workloads). |
| `status.resources.workload.name` | `string` | yes |  |  | Name of the resource. |
| `status.summary` | `object` | no |  |  | Summary provides severity-based finding counts. |
| `status.summary.errorCount` | `integer` | yes |  |  | ErrorCount is the number of error-severity findings. |
| `status.summary.infoCount` | `integer` | yes |  |  | InfoCount is the number of info-severity findings. |
| `status.summary.unknownCount` | `integer` | yes |  |  | UnknownCount is the number of unknown-severity findings (a required assertion could not be evaluated). |
| `status.summary.warningCount` | `integer` | yes |  |  | WarningCount is the number of warning-severity findings. |
| `status.validation` | `object` | no |  |  | Validation describes the structural validation outcome of the contract. |
| `status.validation.errors` | `[]object` | no |  |  | Errors lists structural validation errors. |
| `status.validation.errors[].code` | `string` | no |  |  | Code is a machine-readable error code. |
| `status.validation.errors[].message` | `string` | yes |  |  | Message is a human-readable description of the issue. |
| `status.validation.errors[].path` | `string` | no |  |  | Path is the JSON path to the invalid field. |
| `status.validation.valid` | `boolean` | yes |  |  | Valid indicates whether the contract passed structural validation. |
| `status.validation.warnings` | `[]object` | no |  |  | Warnings lists structural validation warnings. |
| `status.validation.warnings[].code` | `string` | no |  |  | Code is a machine-readable error code. |
| `status.validation.warnings[].message` | `string` | yes |  |  | Message is a human-readable description of the issue. |
| `status.validation.warnings[].path` | `string` | no |  |  | Path is the JSON path to the invalid field. |
