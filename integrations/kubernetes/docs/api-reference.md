# API Reference

## Packages
- [pacto.trianalab.io/v1alpha1](#pactotrianalabiov1alpha1)


## pacto.trianalab.io/v1alpha1

Package v1alpha1 contains API Schema definitions for the pacto v1alpha1 API group.

### Resource Types
- [Pacto](#pacto)
- [PactoRevision](#pactorevision)



#### CheckSummary



CheckSummary provides precomputed check counts.



_Appears in:_
- [PactoStatus](#pactostatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `total` _integer_ | Total is the number of checks performed. |  |  |
| `passed` _integer_ | Passed is the number of checks that passed. |  |  |
| `failed` _integer_ | Failed is the number of checks that failed. |  |  |


#### ConfigurationInfo



ConfigurationInfo describes a single named configuration scope from the contract.



_Appears in:_
- [PactoStatus](#pactostatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the configuration scope name (required, unique within contract). |  |  |
| `hasSchema` _boolean_ | HasSchema indicates whether a JSON Schema file is bundled. |  |  |
| `ref` _string_ | Ref is the external OCI reference for the configuration schema, if used. |  | Optional: \{\} <br /> |
| `valueKeys` _string array_ | ValueKeys lists the declared configuration value keys. |  | Optional: \{\} <br /> |
| `secretKeys` _string array_ | SecretKeys lists configuration keys whose values reference secrets. |  | Optional: \{\} <br /> |
| `overriddenKeys` _string array_ | OverriddenKeys lists configuration keys whose values were overridden<br />by spec.overrides.configurations. Empty when no overrides apply. |  | Optional: \{\} <br /> |
| `properties` _[SchemaProperty](#schemaproperty) array_ | Properties lists the configuration's declared keys with their type and<br />default/value, extracted from the bundled schema (or literal values), so<br />consumers can render configuration content without re-reading the bundle. |  | Optional: \{\} <br /> |


#### ConfigurationOverride



ConfigurationOverride specifies value overrides for a single named configuration scope.



_Appears in:_
- [ContractOverrides](#contractoverrides)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name identifies the configuration scope to override (must match a configurations[] entry in the contract). |  | MinLength: 1 <br />Required: \{\} <br /> |
| `values` _object (keys:string, values:string)_ | Values contains the configuration key-value pairs to merge into the resolved contract.<br />These values take precedence over the values declared in the contract. |  | MinProperties: 1 <br />Required: \{\} <br /> |


#### ContractInfo



ContractInfo exposes parsed contract metadata.



_Appears in:_
- [PactoStatus](#pactostatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `serviceName` _string_ | ServiceName is the service name declared in the contract. |  |  |
| `version` _string_ | Version is the semver version from the contract. |  |  |
| `owner` _[OwnerInfo](#ownerinfo)_ | Owner contains the structured ownership metadata from the contract<br />(team, DRI, and contacts). |  | MinProperties: 1 <br />Optional: \{\} <br /> |
| `ownerDisplay` _string_ | OwnerDisplay is the canonical display string derived from owner metadata.<br />Precedence: team > DRI.<br />Useful for printer columns, dashboards, and backward-compatible consumers. |  | Optional: \{\} <br /> |
| `imageRef` _string_ | ImageRef is the container image reference from the contract. |  | Optional: \{\} <br /> |
| `resolvedRef` _string_ | ResolvedRef is the fully-resolved OCI reference (with tag/digest).<br />Empty for inline contracts. |  | Optional: \{\} <br /> |


#### ContractOverrides



ContractOverrides specifies partial overrides to apply on top of the resolved contract.
Overrides are applied after contract resolution (OCI or inline) but before validation
and reconciliation. The original contract artifact is never mutated.



_Appears in:_
- [PactoSpec](#pactospec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `configurations` _[ConfigurationOverride](#configurationoverride) array_ | Configurations lists per-scope configuration value overrides.<br />Each entry is matched by name to a configurations[] entry in the resolved contract. |  | Optional: \{\} <br /> |


#### ContractRef



ContractRef specifies where to find the Pacto contract.



_Appears in:_
- [PactoSpec](#pactospec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `oci` _string_ | OCI is the OCI registry reference for the contract bundle.<br />Three forms are supported:<br />  - Unversioned (ghcr.io/org/service-pacto): tracks the latest semver tag.<br />  - Tagged (ghcr.io/org/service-pacto:1.2.3): pinned to that exact tag.<br />  - Digest (ghcr.io/org/service-pacto@sha256:...): immutable, exact reference. |  | Optional: \{\} <br /> |
| `inline` _string_ | Inline allows specifying the contract YAML directly (for testing/dev). |  | Optional: \{\} <br /> |
| `pullSecretRef` _string_ | PullSecretRef is the name of a Secret in the same namespace containing<br />OCI registry credentials. Supported secret types:<br />  - Opaque with "token" key (bearer token)<br />  - Opaque with "username"+"password" keys (basic auth)<br />  - kubernetes.io/dockerconfigjson (standard Docker registry auth) |  | Optional: \{\} <br /> |


#### DependencyInfo



DependencyInfo describes a declared dependency.



_Appears in:_
- [PactoStatus](#pactostatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the dependency name (required, unique within contract). |  |  |
| `ref` _string_ | Ref is the dependency reference (OCI URI). |  |  |
| `required` _boolean_ | Required indicates whether this dependency is mandatory. |  |  |
| `compatibility` _string_ | Compatibility is the semver constraint for the dependency. |  | Optional: \{\} <br /> |


#### EndpointCheckResult



EndpointCheckResult describes the result of probing a single HTTP endpoint.



_Appears in:_
- [EndpointsStatus](#endpointsstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `url` _string_ | URL is the fully-constructed endpoint URL that was probed. |  |  |
| `reachable` _boolean_ | Reachable indicates whether the HTTP request completed (no connection error). |  |  |
| `statusCode` _integer_ | StatusCode is the HTTP status code returned. Zero if unreachable. |  | Optional: \{\} <br /> |
| `latencyMs` _integer_ | LatencyMs is the request round-trip time in milliseconds. |  | Optional: \{\} <br /> |
| `error` _string_ | Error is the error message if the probe failed. Empty on success. |  | Optional: \{\} <br /> |


#### EndpointsStatus



EndpointsStatus describes the runtime probe results for declared endpoints.



_Appears in:_
- [PactoStatus](#pactostatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `health` _[EndpointCheckResult](#endpointcheckresult)_ | Health describes the result of probing the declared health endpoint. |  | Optional: \{\} <br /> |
| `metrics` _[EndpointCheckResult](#endpointcheckresult)_ | Metrics describes the result of probing the declared metrics endpoint. |  | Optional: \{\} <br /> |


#### InterfaceInfo



InterfaceInfo describes a single interface declared in the contract.



_Appears in:_
- [PactoStatus](#pactostatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the interface name. |  |  |
| `type` _string_ | Type is the interface type: http, grpc, or event. |  | Enum: [http grpc event] <br /> |
| `port` _integer_ | Port is the declared port number. |  | Optional: \{\} <br /> |
| `visibility` _string_ | Visibility is the declared visibility: public or internal. |  | Optional: \{\} <br /> |
| `hasContractFile` _boolean_ | HasContractFile indicates whether a contract file (OpenAPI, protobuf, AsyncAPI)<br />is present in the bundle for this interface. |  |  |


#### ObservedRuntime



ObservedRuntime describes the actual runtime state observed from the cluster.
This complements RuntimeInfo (contract-declared) to enable contract-vs-runtime comparison.



_Appears in:_
- [PactoStatus](#pactostatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `workloadKind` _string_ | WorkloadKind is the actual Kubernetes resource kind (Deployment, StatefulSet, Job, CronJob). |  | Optional: \{\} <br /> |
| `deploymentStrategy` _string_ | DeploymentStrategy is the observed strategy (RollingUpdate, Recreate). Empty for non-Deployments. |  | Optional: \{\} <br /> |
| `podManagementPolicy` _string_ | PodManagementPolicy is the observed pod management policy (OrderedReady, Parallel). Empty for non-StatefulSets. |  | Optional: \{\} <br /> |
| `terminationGracePeriodSeconds` _integer_ | TerminationGracePeriodSeconds is the observed terminationGracePeriodSeconds from the pod spec. |  | Optional: \{\} <br /> |
| `containerImages` _string array_ | ContainerImages lists the container images from the pod spec. |  | Optional: \{\} <br /> |
| `hasPVC` _boolean_ | HasPVC indicates whether the workload uses PersistentVolumeClaims. |  |  |
| `hasEmptyDir` _boolean_ | HasEmptyDir indicates whether the workload uses emptyDir volumes. |  |  |
| `healthProbeInitialDelaySeconds` _integer_ | HealthProbeInitialDelaySeconds is the observed initialDelaySeconds from the first container's probe. |  | Optional: \{\} <br /> |


#### OwnerContact



OwnerContact is a provider-neutral contact point for service ownership.



_Appears in:_
- [OwnerInfo](#ownerinfo)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _string_ | Type is the contact channel type (e.g. email, chat, oncall). |  | Required: \{\} <br /> |
| `value` _string_ | Value is the contact address or identifier. |  | Required: \{\} <br /> |
| `purpose` _string_ | Purpose describes what this contact is used for (e.g. escalation, support, oncall). |  | Optional: \{\} <br /> |


#### OwnerInfo



OwnerInfo is the structured ownership metadata for a service.
At least one field must be set.

_Validation:_
- MinProperties: 1

_Appears in:_
- [ContractInfo](#contractinfo)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `team` _string_ | Team is the owning team name. |  | Optional: \{\} <br /> |
| `dri` _string_ | DRI is the directly responsible individual. |  | Optional: \{\} <br /> |
| `contacts` _[OwnerContact](#ownercontact) array_ | Contacts lists provider-neutral contact points. |  | Optional: \{\} <br /> |


#### Pacto



Pacto is the Schema for the pactos API.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `pacto.trianalab.io/v1alpha1` | | |
| `kind` _string_ | `Pacto` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  | Optional: \{\} <br /> |
| `spec` _[PactoSpec](#pactospec)_ | spec defines the desired state of Pacto. |  | Required: \{\} <br /> |
| `status` _[PactoStatus](#pactostatus)_ | status defines the observed state of Pacto. |  | Optional: \{\} <br /> |


#### PactoRevision



PactoRevision is the Schema for the pactorevisions API.
It represents an immutable snapshot of a resolved contract version.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `pacto.trianalab.io/v1alpha1` | | |
| `kind` _string_ | `PactoRevision` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  | Optional: \{\} <br /> |
| `spec` _[PactoRevisionSpec](#pactorevisionspec)_ | spec defines the desired state of PactoRevision. |  | Required: \{\} <br /> |
| `status` _[PactoRevisionStatus](#pactorevisionstatus)_ | status defines the observed state of PactoRevision. |  | Optional: \{\} <br /> |


#### PactoRevisionSpec



PactoRevisionSpec defines the desired state of PactoRevision.
A PactoRevision is an immutable snapshot of a resolved contract version.



_Appears in:_
- [PactoRevision](#pactorevision)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `version` _string_ | Version is the contract version (from contract.service.version). |  | MinLength: 1 <br /> |
| `source` _[RevisionSource](#revisionsource)_ | Source specifies where this revision's contract was loaded from. |  | Required: \{\} <br /> |
| `pactoRef` _string_ | PactoRef is the name of the parent Pacto resource that owns this revision. |  | MinLength: 1 <br /> |
| `serviceName` _string_ | ServiceName is the service name from the contract. |  | Optional: \{\} <br /> |


#### PactoRevisionStatus



PactoRevisionStatus defines the observed state of PactoRevision.



_Appears in:_
- [PactoRevision](#pactorevision)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `resolved` _boolean_ | Resolved indicates whether this revision has been successfully resolved and parsed. |  |  |
| `contractHash` _string_ | ContractHash is the SHA-256 hash of the raw contract YAML.<br />Used to detect content changes across versions. |  | Optional: \{\} <br /> |
| `createdAt` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#time-v1-meta)_ | CreatedAt is the timestamp when this revision was first resolved. |  | Optional: \{\} <br /> |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#condition-v1-meta) array_ | conditions represent the current state of the PactoRevision resource. |  | Optional: \{\} <br /> |


#### PactoSpec



PactoSpec defines the desired state of Pacto.



_Appears in:_
- [Pacto](#pacto)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `contractRef` _[ContractRef](#contractref)_ | ContractRef specifies where to find the Pacto contract. |  | Required: \{\} <br /> |
| `target` _[TargetRef](#targetref)_ | Target specifies which Kubernetes resources to observe.<br />When omitted, the Pacto acts as a reference-only contract (no runtime validation). |  | Optional: \{\} <br /> |
| `overrides` _[ContractOverrides](#contractoverrides)_ | Overrides specifies partial configuration overrides to apply on top of the resolved contract.<br />This enables environment-specific tuning without duplicating the entire contract inline.<br />Semantics mirror the Pacto CLI --set / -f override model. |  | Optional: \{\} <br /> |
| `checkIntervalSeconds` _integer_ | CheckIntervalSeconds controls how often the reconciler re-checks compliance.<br />Defaults to 300 (5 minutes). | 300 | Minimum: 30 <br />Optional: \{\} <br /> |


#### PactoStatus



PactoStatus defines the observed state of Pacto.
All contract data is exposed as structured fields so external consumers
can read the CR status directly without parsing contracts themselves.



_Appears in:_
- [Pacto](#pacto)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `contractStatus` _string_ | ContractStatus is the high-level contract compliance state.<br />This reflects contract validation/compliance and is NOT runtime health. |  | Enum: [Compliant Warning NonCompliant Reference Unknown] <br />Optional: \{\} <br /> |
| `resolutionPolicy` _string_ | ResolutionPolicy describes how the OCI reference was resolved.<br />Latest: unversioned ref, operator tracks the highest semver tag.<br />PinnedTag: ref includes an explicit tag, used as-is.<br />PinnedDigest: ref includes a digest, used as-is (immutable).<br />Empty for inline contracts. |  | Enum: [Latest PinnedTag PinnedDigest] <br />Optional: \{\} <br /> |
| `summary` _[CheckSummary](#checksummary)_ | Summary provides precomputed check counts. |  | Optional: \{\} <br /> |
| `contractVersion` _string_ | ContractVersion is the version from the parsed contract.<br />Kept for backward compatibility and simple access via JSONPath. |  | Optional: \{\} <br /> |
| `contract` _[ContractInfo](#contractinfo)_ | Contract exposes parsed contract metadata. |  | Optional: \{\} <br /> |
| `validation` _[ValidationResult](#validationresult)_ | Validation describes the structural validation outcome of the contract. |  | Optional: \{\} <br /> |
| `resources` _[ResourcesStatus](#resourcesstatus)_ | Resources describes the existence of target Kubernetes resources. |  | Optional: \{\} <br /> |
| `ports` _[PortStatus](#portstatus)_ | Ports describes the port comparison between contract and runtime. |  | Optional: \{\} <br /> |
| `endpoints` _[EndpointsStatus](#endpointsstatus)_ | Endpoints describes the runtime probe results for declared health and metrics endpoints.<br />Only populated when the service exists and the contract declares health/metrics. |  | Optional: \{\} <br /> |
| `interfaces` _[InterfaceInfo](#interfaceinfo) array_ | Interfaces lists the parsed interfaces from the contract. |  | Optional: \{\} <br /> |
| `configurations` _[ConfigurationInfo](#configurationinfo) array_ | Configurations lists the contract's named configuration scopes.<br />Each entry corresponds to one configurations[] entry in the contract. |  | Optional: \{\} <br /> |
| `dependencies` _[DependencyInfo](#dependencyinfo) array_ | Dependencies lists the declared dependencies from the contract. |  | Optional: \{\} <br /> |
| `policies` _[PolicyInfo](#policyinfo) array_ | Policies lists the contract's declared policy sources (metadata only).<br />Each entry describes a local schema or external ref; the operator does not<br />resolve or enforce ref-based policies at runtime. |  | Optional: \{\} <br /> |
| `runtime` _[RuntimeInfo](#runtimeinfo)_ | Runtime describes the contract's runtime section (declared). |  | Optional: \{\} <br /> |
| `observedRuntime` _[ObservedRuntime](#observedruntime)_ | ObservedRuntime describes the actual runtime state observed from the cluster.<br />Only populated when a target workload exists. |  | Optional: \{\} <br /> |
| `scaling` _[ScalingInfo](#scalinginfo)_ | Scaling describes the contract's scaling section. |  | Optional: \{\} <br /> |
| `readiness` _[ReadinessStatus](#readinessstatus)_ | Readiness is the derived operational readiness assessment of the contract.<br />It is computed from the contract's declared readiness checks and the current<br />time. It is a separate dimension from contract compliance and does NOT affect<br />ContractStatus. Absent when the contract declares no readiness. |  | Optional: \{\} <br /> |
| `metadata` _object (keys:string, values:string)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  | Optional: \{\} <br /> |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#condition-v1-meta) array_ | Conditions represent individual validation checks. |  | Optional: \{\} <br /> |
| `currentRevision` _string_ | CurrentRevision is the name of the active PactoRevision. |  | Optional: \{\} <br /> |
| `lastReconciledAt` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.32/#time-v1-meta)_ | LastReconciledAt is when the last reconciliation completed. |  | Optional: \{\} <br /> |
| `observedGeneration` _integer_ | ObservedGeneration is the most recent generation observed by the controller. |  | Optional: \{\} <br /> |


#### PolicyInfo



PolicyInfo describes a single named policy source from the contract.
Each policy provides either a local JSON Schema file or a reference to an
external contract whose bundle contains the policy schema.



_Appears in:_
- [PactoStatus](#pactostatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the policy name (required, unique within contract). |  |  |
| `hasSchema` _boolean_ | HasSchema indicates whether a policy schema file is bundled. |  |  |
| `schema` _string_ | Schema is the bundle-relative path to the policy schema file, if local. |  | Optional: \{\} <br /> |
| `ref` _string_ | Ref is the external OCI reference for the policy schema, if used. |  | Optional: \{\} <br /> |
| `title` _string_ | Title is the policy schema's declared title, if any. |  | Optional: \{\} <br /> |
| `description` _string_ | Description is the policy schema's declared description, if any. |  | Optional: \{\} <br /> |
| `properties` _[SchemaProperty](#schemaproperty) array_ | Properties lists the policy schema's keys with their type and default, so<br />consumers can render policy content without re-reading the bundle. |  | Optional: \{\} <br /> |


#### PortStatus



PortStatus describes the port comparison between contract and runtime.



_Appears in:_
- [PactoStatus](#pactostatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `expected` _integer array_ | Expected lists ports declared in the contract. |  |  |
| `observed` _integer array_ | Observed lists ports found on the Kubernetes Service. |  |  |
| `missing` _integer array_ | Missing lists contract ports not found on the Service. |  |  |
| `unexpected` _integer array_ | Unexpected lists Service ports not declared in the contract. |  |  |


#### ReadinessCheckStatus



ReadinessCheckStatus is the derived state of a single readiness check.



_Appears in:_
- [ReadinessStatus](#readinessstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `id` _string_ | ID is the readiness requirement identifier (e.g. dashboard, runbook). |  | Required: \{\} <br /> |
| `type` _string_ | Type classifies the evidence pointer (url, document, ticket, report, artifact, identifier, other). |  | Required: \{\} <br /> |
| `category` _string_ | Category is the software-domain category (security, documentation, observability, etc.). |  | Optional: \{\} <br /> |
| `status` _string_ | Status is the declared completion status. |  | Enum: [done partial not-done deferred] <br />Required: \{\} <br /> |
| `evidence` _string_ | Evidence is the declared pointer to the evidence. |  | Optional: \{\} <br /> |
| `description` _string_ | Description is the optional human-readable explanation. |  | Optional: \{\} <br /> |
| `weight` _integer_ | Weight is the declared contribution to the readiness score (0-100). |  | Required: \{\} <br /> |
| `earnedWeight` _integer_ | EarnedWeight is the weight this check contributed toward the score. |  | Optional: \{\} <br /> |
| `excluded` _boolean_ | Excluded reports whether the check is excluded from scoring (deferred). |  | Optional: \{\} <br /> |


#### ReadinessRevisionStatus



ReadinessRevisionStatus is one declared readiness revision-history entry.



_Appears in:_
- [ReadinessStatus](#readinessstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `date` _string_ | Date is the date the revision was assessed (YYYY-MM-DD). |  | Required: \{\} <br /> |
| `version` _string_ | Version is the service version assessed in this revision. |  | Required: \{\} <br /> |
| `author` _string_ | Author is who performed the assessment. |  | Required: \{\} <br /> |
| `description` _string_ | Description summarizes what changed or was observed. |  | Required: \{\} <br /> |


#### ReadinessStatus



ReadinessStatus is the derived operational readiness assessment of a contract.
It is computed from the contract's declared readiness checks and the current
time; the derived per-check status and score are never authored in the contract.



_Appears in:_
- [PactoStatus](#pactostatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `score` _integer_ | Score is the percentage of in-scope weight earned (0-100). |  | Optional: \{\} <br /> |
| `minScore` _integer_ | MinScore is the gate threshold (the declared readiness.minScore, or 100 when omitted). |  | Optional: \{\} <br /> |
| `passing` _boolean_ | Passing reports whether the gate is met (not expired and Score >= MinScore). |  | Optional: \{\} <br /> |
| `totalWeight` _integer_ | TotalWeight is the sum of in-scope (non-deferred) check weights. |  | Optional: \{\} <br /> |
| `earnedWeight` _integer_ | EarnedWeight is the weight earned toward the score. |  | Optional: \{\} <br /> |
| `expires` _string_ | Expires is the assessment-level expiry boundary (YYYY-MM-DD). |  | Optional: \{\} <br /> |
| `expired` _boolean_ | Expired reports whether the assessment has expired; when true every in-scope check earns 0. |  | Optional: \{\} <br /> |
| `daysRemaining` _integer_ | DaysRemaining is the number of whole days until expiry (nil when expired). |  | Optional: \{\} <br /> |
| `doneCount` _integer_ | DoneCount is the number of checks declared done. |  | Optional: \{\} <br /> |
| `partialCount` _integer_ | PartialCount is the number of checks declared partial. |  | Optional: \{\} <br /> |
| `notDoneCount` _integer_ | NotDoneCount is the number of checks declared not-done. |  | Optional: \{\} <br /> |
| `deferredCount` _integer_ | DeferredCount is the number of checks declared deferred (excluded from scoring). |  | Optional: \{\} <br /> |
| `revisions` _[ReadinessRevisionStatus](#readinessrevisionstatus) array_ | Revisions is the declared readiness revision history. |  | Optional: \{\} <br /> |
| `checks` _[ReadinessCheckStatus](#readinesscheckstatus) array_ | Checks is the derived per-check readiness status. |  | Optional: \{\} <br /> |


#### ResourceStatus



ResourceStatus describes the observed state of a Kubernetes resource.



_Appears in:_
- [ResourcesStatus](#resourcesstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name of the resource. |  |  |
| `kind` _string_ | Kind of the resource (only set for workloads). |  | Optional: \{\} <br /> |
| `exists` _boolean_ | Exists indicates whether the resource was found in the cluster. |  |  |


#### ResourcesStatus



ResourcesStatus groups the status of target resources.



_Appears in:_
- [PactoStatus](#pactostatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `service` _[ResourceStatus](#resourcestatus)_ | Service describes the target Service. |  | Optional: \{\} <br /> |
| `workload` _[ResourceStatus](#resourcestatus)_ | Workload describes the target workload (Deployment/StatefulSet/ReplicaSet). |  | Optional: \{\} <br /> |


#### RevisionSource



RevisionSource specifies where the contract for this revision was loaded from.



_Appears in:_
- [PactoRevisionSpec](#pactorevisionspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `oci` _string_ | OCI is the fully resolved OCI reference (including tag/digest).<br />Example: ghcr.io/org/service-pacto:1.2.0 |  | Optional: \{\} <br /> |
| `digest` _string_ | Digest is the OCI manifest digest (sha256:...) at the time of resolution.<br />Used to detect force-pushes (tag overwrites) on the registry. |  | Optional: \{\} <br /> |
| `inline` _boolean_ | Inline indicates the contract was provided inline (no external source). |  | Optional: \{\} <br /> |


#### RuntimeInfo



RuntimeInfo describes the contract's runtime section.



_Appears in:_
- [PactoStatus](#pactostatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `workload` _string_ | Workload is the workload type: service, job, or scheduled. |  | Optional: \{\} <br /> |
| `stateType` _string_ | StateType is the state semantics: stateless, stateful, or hybrid. |  | Optional: \{\} <br /> |
| `persistenceScope` _string_ | PersistenceScope is the persistence scope: local or shared. |  | Optional: \{\} <br /> |
| `persistenceDurability` _string_ | PersistenceDurability is the durability: ephemeral or persistent. |  | Optional: \{\} <br /> |
| `dataCriticality` _string_ | DataCriticality is the data criticality level: low, medium, or high. |  | Optional: \{\} <br /> |
| `upgradeStrategy` _string_ | UpgradeStrategy is the declared upgrade strategy: rolling, recreate, or ordered. |  | Optional: \{\} <br /> |
| `gracefulShutdownSeconds` _integer_ | GracefulShutdownSeconds is the declared graceful shutdown period. |  | Optional: \{\} <br /> |
| `healthInterface` _string_ | HealthInterface is the interface used for health checks. |  | Optional: \{\} <br /> |
| `healthPath` _string_ | HealthPath is the HTTP path for health checks. |  | Optional: \{\} <br /> |
| `metricsInterface` _string_ | MetricsInterface is the interface used for metrics. |  | Optional: \{\} <br /> |
| `metricsPath` _string_ | MetricsPath is the HTTP path for metrics. |  | Optional: \{\} <br /> |
| `healthInitialDelaySeconds` _integer_ | HealthInitialDelaySeconds is the declared initial delay before health checks start. |  | Optional: \{\} <br /> |


#### ScalingInfo



ScalingInfo describes the contract's scaling section.



_Appears in:_
- [PactoStatus](#pactostatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `replicas` _integer_ | Replicas is the exact replica count (mutually exclusive with Min/Max). |  | Optional: \{\} <br /> |
| `min` _integer_ | Min is the minimum replica count for autoscaling. |  | Optional: \{\} <br /> |
| `max` _integer_ | Max is the maximum replica count for autoscaling. |  | Optional: \{\} <br /> |


#### SchemaProperty



SchemaProperty is a flattened key from a configuration or policy schema
(or a configuration values map), suitable for display by consumers.



_Appears in:_
- [ConfigurationInfo](#configurationinfo)
- [PolicyInfo](#policyinfo)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `key` _string_ | Key is the property name (dot-notation for nested objects). |  |  |
| `value` _string_ | Value is the default (for schema properties) or the literal value. |  | Optional: \{\} <br /> |
| `type` _string_ | Type is the JSON Schema type of the property. |  | Optional: \{\} <br /> |


#### TargetRef



TargetRef specifies which Kubernetes resources to observe.



_Appears in:_
- [PactoSpec](#pactospec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `serviceName` _string_ | ServiceName is the name of the Kubernetes Service to observe. |  | Optional: \{\} <br /> |
| `workloadRef` _[WorkloadRef](#workloadref)_ | WorkloadRef identifies the workload (Deployment, StatefulSet, ReplicaSet, Job, or CronJob).<br />If omitted, defaults to name=serviceName, kind=Deployment. |  | Optional: \{\} <br /> |


#### ValidationIssue



ValidationIssue describes a single validation error or warning.



_Appears in:_
- [ValidationResult](#validationresult)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `code` _string_ | Code is a machine-readable error code. |  | Optional: \{\} <br /> |
| `path` _string_ | Path is the JSON path to the invalid field. |  | Optional: \{\} <br /> |
| `message` _string_ | Message is a human-readable description of the issue. |  |  |


#### ValidationResult



ValidationResult describes the structural validation outcome of the contract.



_Appears in:_
- [PactoStatus](#pactostatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `valid` _boolean_ | Valid indicates whether the contract passed structural validation. |  |  |
| `errors` _[ValidationIssue](#validationissue) array_ | Errors lists structural validation errors. |  | Optional: \{\} <br /> |
| `warnings` _[ValidationIssue](#validationissue) array_ | Warnings lists structural validation warnings. |  | Optional: \{\} <br /> |


#### WorkloadRef



WorkloadRef identifies a workload resource by name and kind.



_Appears in:_
- [TargetRef](#targetref)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name of the workload resource. |  | Required: \{\} <br /> |
| `kind` _string_ | Kind of the workload resource.<br />Job/CronJob targets require an explicit kind (contract runtime.workload job/scheduled). | Deployment | Enum: [Deployment StatefulSet ReplicaSet Job CronJob] <br />Optional: \{\} <br /> |


