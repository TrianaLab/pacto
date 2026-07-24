/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// ContractRef specifies where to find the Pacto contract.
type ContractRef struct {
	// OCI is the OCI registry reference for the contract bundle.
	// Three forms are supported:
	//   - Unversioned (ghcr.io/org/service-pacto): tracks the latest semver tag.
	//   - Tagged (ghcr.io/org/service-pacto:1.2.3): pinned to that exact tag.
	//   - Digest (ghcr.io/org/service-pacto@sha256:...): immutable, exact reference.
	// +optional
	OCI string `json:"oci,omitempty"`

	// Inline allows specifying the contract YAML directly (for testing/dev).
	// +optional
	Inline string `json:"inline,omitempty"`

	// PullSecretRef is the name of a Secret in the same namespace containing
	// OCI registry credentials. Supported secret types:
	//   - Opaque with "token" key (bearer token)
	//   - Opaque with "username"+"password" keys (basic auth)
	//   - kubernetes.io/dockerconfigjson (standard Docker registry auth)
	// +optional
	PullSecretRef string `json:"pullSecretRef,omitempty"`
}

// WorkloadRef identifies a workload resource by name and kind.
type WorkloadRef struct {
	// Name of the workload resource.
	// +required
	Name string `json:"name"`

	// Kind of the workload resource. Left unspecified (empty) unless the author sets it explicitly;
	// WORKLOAD_MISMATCH only fires when BOTH name AND kind were explicit (AR7). No default is applied here
	// so the collector can distinguish "author asserted kind X" from "kind was defaulted for the GET".
	// +kubebuilder:validation:Enum=Deployment;StatefulSet;ReplicaSet;Job;CronJob
	// +optional
	Kind string `json:"kind,omitempty"`
}

// TargetRef specifies which Kubernetes resources to observe.
type TargetRef struct {
	// ServiceName is the name of the Kubernetes Service to observe.
	// +optional
	ServiceName string `json:"serviceName,omitempty"`

	// WorkloadRef identifies the workload (Deployment, StatefulSet, ReplicaSet, Job, or CronJob).
	// If omitted, defaults to name=serviceName, kind=Deployment.
	// +optional
	WorkloadRef *WorkloadRef `json:"workloadRef,omitempty"`

	// InterfaceBindings maps contract interfaces to the Service port that serves each (B4). Kubernetes
	// deployment knowledge — lives on the CR, NOT the platform-agnostic contract.
	// +optional
	InterfaceBindings []InterfaceBinding `json:"interfaceBindings,omitempty"`

	// ConfigBindings maps contract configurations to the concrete ConfigMap/Secret backing each (B7).
	// +optional
	ConfigBindings []ConfigBinding `json:"configBindings,omitempty"`
}

// InterfaceBinding maps a contract interface to the Service port that serves it (B4).
type InterfaceBinding struct {
	// Interface is the contract interfaces[].name this binding resolves.
	// +required
	Interface string `json:"interface"`
	// ServicePort is the Service port name or number that serves the interface.
	// +required
	ServicePort intstr.IntOrString `json:"servicePort"`
}

// ConfigBinding maps a contract configuration to the concrete Kubernetes object backing it (B7).
type ConfigBinding struct {
	// Configuration is the contract configurations[].name this binding backs.
	// +required
	Configuration string `json:"configuration"`
	// Kind of the backing object.
	// +kubebuilder:validation:Enum=ConfigMap;Secret
	// +required
	Kind string `json:"kind"`
	// Name of the backing ConfigMap/Secret in the Pacto's namespace.
	// +required
	Name string `json:"name"`
	// Key names the single ConfigMap/Secret key to decode and validate. Omit for existence-only.
	// +optional
	Key string `json:"key,omitempty"`
	// Format of the value at Key. Required with Key for ConfigMap conformance.
	// +kubebuilder:validation:Enum=yaml;json
	// +optional
	Format string `json:"format,omitempty"`
}

// ConfigurationOverride specifies value overrides for a single named configuration scope.
type ConfigurationOverride struct {
	// Name identifies the configuration scope to override (must match a configurations[] entry in the contract).
	// +required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Values contains the configuration key-value pairs to merge into the resolved contract.
	// These values take precedence over the values declared in the contract.
	// +required
	// +kubebuilder:validation:MinProperties=1
	Values map[string]string `json:"values"`
}

// ContractOverrides specifies partial overrides to apply on top of the resolved contract.
// Overrides are applied after contract resolution (OCI or inline) but before validation
// and reconciliation. The original contract artifact is never mutated.
type ContractOverrides struct {
	// Configurations lists per-scope configuration value overrides.
	// Each entry is matched by name to a configurations[] entry in the resolved contract.
	// +optional
	Configurations []ConfigurationOverride `json:"configurations,omitempty"`
}

// PactoSpec defines the desired state of Pacto.
type PactoSpec struct {
	// ContractRef specifies where to find the Pacto contract.
	// +required
	ContractRef ContractRef `json:"contractRef"`

	// Target specifies which Kubernetes resources to observe.
	// When omitted, the Pacto acts as a reference-only contract (no runtime validation).
	// +optional
	Target TargetRef `json:"target,omitempty"`

	// Overrides specifies partial configuration overrides to apply on top of the resolved contract.
	// This enables environment-specific tuning without duplicating the entire contract inline.
	// Semantics mirror the Pacto CLI --set / -f override model.
	// +optional
	Overrides *ContractOverrides `json:"overrides,omitempty"`

	// CheckIntervalSeconds controls how often the reconciler re-checks compliance.
	// Defaults to 300 (5 minutes).
	// +optional
	// +kubebuilder:default=300
	// +kubebuilder:validation:Minimum=30
	CheckIntervalSeconds int32 `json:"checkIntervalSeconds,omitempty"`
}

// --- Status types (designed as a stable, structured API for external consumers) ---

// ResourceStatus describes the observed state of a Kubernetes resource.
type ResourceStatus struct {
	// Name of the resource.
	Name string `json:"name"`

	// Kind of the resource (only set for workloads).
	// +optional
	Kind string `json:"kind,omitempty"`

	// Exists indicates whether the resource was found in the cluster.
	Exists bool `json:"exists"`
}

// ReadinessStatus is the derived operational readiness assessment of a contract.
// It is computed from the contract's declared readiness checks and the current
// time; the derived per-check status and score are never authored in the contract.
type ReadinessStatus struct {
	// Score is the percentage of in-scope weight earned (0-100).
	// +optional
	Score int32 `json:"score,omitempty"`

	// MinScore is the gate threshold (the declared readiness.minScore, or 100 when omitted).
	// +optional
	MinScore int32 `json:"minScore,omitempty"`

	// Passing reports whether the gate is met (not expired and Score >= MinScore).
	// +optional
	Passing bool `json:"passing,omitempty"`

	// TotalWeight is the sum of in-scope (non-deferred) check weights.
	// +optional
	TotalWeight int32 `json:"totalWeight,omitempty"`

	// EarnedWeight is the weight earned toward the score.
	// +optional
	EarnedWeight int32 `json:"earnedWeight,omitempty"`

	// Expires is the assessment-level expiry boundary (YYYY-MM-DD).
	// +optional
	Expires string `json:"expires,omitempty"`

	// Expired reports whether the assessment has expired; when true every in-scope check earns 0.
	// +optional
	Expired bool `json:"expired,omitempty"`

	// DaysRemaining is the number of whole days until expiry (nil when expired).
	// +optional
	DaysRemaining *int32 `json:"daysRemaining,omitempty"`

	// DoneCount is the number of checks declared done.
	// +optional
	DoneCount int32 `json:"doneCount,omitempty"`

	// PartialCount is the number of checks declared partial.
	// +optional
	PartialCount int32 `json:"partialCount,omitempty"`

	// NotDoneCount is the number of checks declared not-done.
	// +optional
	NotDoneCount int32 `json:"notDoneCount,omitempty"`

	// DeferredCount is the number of checks declared deferred (excluded from scoring).
	// +optional
	DeferredCount int32 `json:"deferredCount,omitempty"`

	// Revisions is the declared readiness revision history.
	// +optional
	Revisions []ReadinessRevisionStatus `json:"revisions,omitempty"`

	// Claims is the derived per-claim readiness status.
	// +optional
	Claims []ClaimStatus `json:"claims,omitempty"`
}

// ReadinessRevisionStatus is one declared readiness revision-history entry.
type ReadinessRevisionStatus struct {
	// Date is the date the revision was assessed (YYYY-MM-DD).
	// +required
	Date string `json:"date"`

	// Version is the service version assessed in this revision.
	// +required
	Version string `json:"version"`

	// Author is who performed the assessment.
	// +required
	Author string `json:"author"`

	// Description summarizes what changed or was observed.
	// +required
	Description string `json:"description"`
}

// ClaimStatus is the derived state of a single readiness claim.
type ClaimStatus struct {
	// ID is the readiness requirement identifier (e.g. dashboard, runbook).
	// +required
	ID string `json:"id"`

	// Type classifies the evidence pointer (url, document, ticket, report, artifact, identifier, other).
	// +required
	Type string `json:"type"`

	// Category is the software-domain category (security, documentation, observability, etc.).
	// +optional
	Category string `json:"category,omitempty"`

	// Status is the declared completion status.
	// +kubebuilder:validation:Enum=done;partial;not-done;deferred
	// +required
	Status string `json:"status"`

	// Evidence is the declared pointer to the evidence.
	// +optional
	Evidence string `json:"evidence,omitempty"`

	// Description is the optional human-readable explanation.
	// +optional
	Description string `json:"description,omitempty"`

	// Weight is the declared contribution to the readiness score (0-100).
	// +required
	Weight int32 `json:"weight"`

	// EarnedWeight is the weight this check contributed toward the score.
	// +optional
	EarnedWeight int32 `json:"earnedWeight"`

	// Excluded reports whether the check is excluded from scoring (deferred).
	// +optional
	Excluded bool `json:"excluded,omitempty"`
}

// ResourcesStatus groups the status of target resources.
type ResourcesStatus struct {
	// Service describes the target Service.
	// +optional
	Service *ResourceStatus `json:"service,omitempty"`

	// Workload describes the target workload (Deployment/StatefulSet/ReplicaSet).
	// +optional
	Workload *ResourceStatus `json:"workload,omitempty"`
}

// OwnerContact is a provider-neutral contact point for service ownership.
type OwnerContact struct {
	// Type is the contact channel type (e.g. email, chat, oncall).
	// +required
	Type string `json:"type"`

	// Value is the contact address or identifier.
	// +required
	Value string `json:"value"`

	// Purpose describes what this contact is used for (e.g. escalation, support, oncall).
	// +optional
	Purpose string `json:"purpose,omitempty"`
}

// OwnerInfo is the structured ownership metadata for a service.
// At least one field must be set.
// +kubebuilder:validation:MinProperties=1
type OwnerInfo struct {
	// Team is the owning team name.
	// +optional
	Team string `json:"team,omitempty"`

	// DRI is the directly responsible individual.
	// +optional
	DRI string `json:"dri,omitempty"`

	// Contacts lists provider-neutral contact points.
	// +optional
	Contacts []OwnerContact `json:"contacts,omitempty"`
}

// ContractInfo exposes parsed contract metadata.
type ContractInfo struct {
	// ServiceName is the service name declared in the contract.
	ServiceName string `json:"serviceName"`

	// Version is the semver version from the contract.
	Version string `json:"version"`

	// Owner contains the structured ownership metadata from the contract
	// (team, DRI, and contacts).
	// +optional
	Owner *OwnerInfo `json:"owner,omitempty"`

	// OwnerDisplay is the canonical display string derived from owner metadata.
	// Precedence: team > DRI.
	// Useful for printer columns, dashboards, and backward-compatible consumers.
	// +optional
	OwnerDisplay string `json:"ownerDisplay,omitempty"`

	// ResolvedRef is the fully-resolved OCI reference (with tag/digest).
	// Empty for inline contracts.
	// +optional
	ResolvedRef string `json:"resolvedRef,omitempty"`
}

// InterfaceInfo describes a single interface declared in the contract.
type InterfaceInfo struct {
	// Name is the interface name.
	Name string `json:"name"`

	// Type is the interface type: openapi, asyncapi, or grpc.
	// +kubebuilder:validation:Enum=openapi;asyncapi;grpc
	Type string `json:"type"`

	// Ref is the bundle-relative path to the interface specification file.
	// +optional
	Ref string `json:"ref,omitempty"`

	// Visibility is the declared visibility: public or internal.
	// +optional
	Visibility string `json:"visibility,omitempty"`
}

// ConfigurationInfo describes a single named configuration scope from the contract.
type ConfigurationInfo struct {
	// Name is the configuration scope name (required, unique within contract).
	Name string `json:"name"`

	// HasSchema indicates whether a JSON Schema file is bundled.
	HasSchema bool `json:"hasSchema"`

	// Ref is the external OCI reference for the configuration schema, if used.
	// +optional
	Ref string `json:"ref,omitempty"`

	// ValueKeys lists the declared configuration value keys.
	// +optional
	ValueKeys []string `json:"valueKeys,omitempty"`

	// SecretKeys lists configuration keys whose values reference secrets.
	// +optional
	SecretKeys []string `json:"secretKeys,omitempty"`

	// OverriddenKeys lists configuration keys whose values were overridden
	// by spec.overrides.configurations. Empty when no overrides apply.
	// +optional
	OverriddenKeys []string `json:"overriddenKeys,omitempty"`

	// Properties lists the configuration's declared keys with their type and
	// default/value, extracted from the bundled schema (or literal values), so
	// consumers can render configuration content without re-reading the bundle.
	// +optional
	Properties []SchemaProperty `json:"properties,omitempty"`
}

// SchemaProperty is a flattened key from a configuration or policy schema
// (or a configuration values map), suitable for display by consumers.
type SchemaProperty struct {
	// Key is the property name (dot-notation for nested objects).
	Key string `json:"key"`

	// Value is the default (for schema properties) or the literal value.
	// +optional
	Value string `json:"value,omitempty"`

	// Type is the JSON Schema type of the property.
	// +optional
	Type string `json:"type,omitempty"`
}

// DependencyInfo describes a declared dependency.
type DependencyInfo struct {
	// Name is the dependency name (required, unique within contract).
	Name string `json:"name"`

	// Ref is the dependency reference (OCI URI).
	Ref string `json:"ref"`

	// Required indicates whether this dependency is mandatory.
	Required bool `json:"required"`

	// Compatibility is the semver constraint for the dependency.
	// +optional
	Compatibility string `json:"compatibility,omitempty"`
}

// PolicyInfo describes a single named policy source from the contract.
// Each policy provides either a local JSON Schema file or a reference to an
// external contract whose bundle contains the policy schema.
type PolicyInfo struct {
	// Name is the policy name (required, unique within contract).
	Name string `json:"name"`

	// HasSchema indicates whether a policy schema file is bundled.
	HasSchema bool `json:"hasSchema"`

	// Schema is the bundle-relative path to the policy schema file, if local.
	// +optional
	Schema string `json:"schema,omitempty"`

	// Ref is the external OCI reference for the policy schema, if used.
	// +optional
	Ref string `json:"ref,omitempty"`

	// Title is the policy schema's declared title, if any.
	// +optional
	Title string `json:"title,omitempty"`

	// Description is the policy schema's declared description, if any.
	// +optional
	Description string `json:"description,omitempty"`

	// Properties lists the policy schema's keys with their type and default, so
	// consumers can render policy content without re-reading the bundle.
	// +optional
	Properties []SchemaProperty `json:"properties,omitempty"`
}

// ObservedRuntime describes the actual runtime state observed from the cluster (lean evidence view).
type ObservedRuntime struct {
	// WorkloadKind is the actual Kubernetes resource kind (Deployment, StatefulSet, Job, CronJob).
	// +optional
	WorkloadKind string `json:"workloadKind,omitempty"`

	// DeploymentStrategy is the observed strategy (RollingUpdate, Recreate). Empty for non-Deployments.
	// +optional
	DeploymentStrategy string `json:"deploymentStrategy,omitempty"`

	// PodManagementPolicy is the observed pod management policy (OrderedReady, Parallel). Empty for non-StatefulSets.
	// +optional
	PodManagementPolicy string `json:"podManagementPolicy,omitempty"`

	// TerminationGracePeriodSeconds is the observed terminationGracePeriodSeconds from the pod spec.
	// +optional
	TerminationGracePeriodSeconds *int64 `json:"terminationGracePeriodSeconds,omitempty"`

	// ContainerImages lists the container images from the pod spec.
	// +optional
	ContainerImages []string `json:"containerImages,omitempty"`

	// HasPVC indicates whether the workload uses PersistentVolumeClaims.
	HasPVC bool `json:"hasPVC"`

	// HasEmptyDir indicates whether the workload uses emptyDir volumes.
	HasEmptyDir bool `json:"hasEmptyDir"`

	// HealthProbeInitialDelaySeconds is the observed initialDelaySeconds from the first container's probe.
	// +optional
	HealthProbeInitialDelaySeconds *int32 `json:"healthProbeInitialDelaySeconds,omitempty"`
}

// ValidationIssue describes a single validation error or warning.
type ValidationIssue struct {
	// Code is a machine-readable error code.
	// +optional
	Code string `json:"code,omitempty"`

	// Path is the JSON path to the invalid field.
	// +optional
	Path string `json:"path,omitempty"`

	// Message is a human-readable description of the issue.
	Message string `json:"message"`
}

// ValidationResult describes the structural validation outcome of the contract.
type ValidationResult struct {
	// Valid indicates whether the contract passed structural validation.
	Valid bool `json:"valid"`

	// Errors lists structural validation errors.
	// +optional
	Errors []ValidationIssue `json:"errors,omitempty"`

	// Warnings lists structural validation warnings.
	// +optional
	Warnings []ValidationIssue `json:"warnings,omitempty"`
}

// EvidenceRefStatus links a finding to the evidence that supports it.
type EvidenceRefStatus struct {
	// Source is the evidence source (e.g. "k8s").
	Source string `json:"source"`

	// ObservedAt is the ISO8601 timestamp when the evidence was collected.
	ObservedAt string `json:"observedAt"`
}

// FindingStatus is a typed conclusion emitted by the Pacto engine.
type FindingStatus struct {
	// Code is the stable finding identifier (e.g. WORKLOAD_MISMATCH).
	Code string `json:"code"`

	// Severity is error, warning, info, or unknown.
	// +kubebuilder:validation:Enum=error;warning;info;unknown
	Severity string `json:"severity"`

	// Category groups related codes (e.g. RuntimeDrift, PolicyViolation).
	Category string `json:"category"`

	// Subject identifies what the finding is about.
	// +optional
	Subject string `json:"subject,omitempty"`

	// ContractPath is the YAML path to the relevant contract field.
	// +optional
	ContractPath string `json:"contractPath,omitempty"`

	// Message is a human-readable description.
	Message string `json:"message"`

	// EvidenceRefs links the finding to supporting evidence.
	// +optional
	EvidenceRefs []EvidenceRefStatus `json:"evidenceRefs,omitempty"`
}

// CapabilityInfo describes a declared capability.
type CapabilityInfo struct {
	// Type is the capability type (e.g. "database", "cache").
	Type string `json:"type"`

	// Ref is the capability reference (OCI URI or local path).
	// +optional
	Ref string `json:"ref,omitempty"`
}

// Summary provides severity-based finding counts.
type Summary struct {
	// ErrorCount is the number of error-severity findings.
	ErrorCount int32 `json:"errorCount"`

	// WarningCount is the number of warning-severity findings.
	WarningCount int32 `json:"warningCount"`

	// InfoCount is the number of info-severity findings.
	InfoCount int32 `json:"infoCount"`

	// UnknownCount is the number of unknown-severity findings (a required assertion could not be evaluated).
	UnknownCount int32 `json:"unknownCount"`
}

// EvaluationCoverage reports how many REQUIRED assertions were actually evaluated (Outcome=Observed).
// Explanatory metadata only; it NEVER changes ContractStatus.
type EvaluationCoverage struct {
	// Evaluated is the number of required assertions with a conclusive (Observed) observation.
	Evaluated int32 `json:"evaluated"`
	// Required is the total number of required assertions the contract declares.
	Required int32 `json:"required"`
}

// ObservationWindow tracks the first sustained NEGATIVE observation for one assertion, so a transient
// negative reads Unknown (stabilizing) and only a sustained one converts to a confirmed violation (B5).
type ObservationWindow struct {
	// Kind is the assertion dimension: interface | dependency | configuration | capability.
	// +required
	Kind string `json:"kind"`
	// Subject is the assertion identity (interface/dependency/configuration name, or capability key).
	// +required
	Subject string `json:"subject"`
	// FirstObservedNegativeAt is when the current negative streak began.
	// +required
	FirstObservedNegativeAt metav1.Time `json:"firstObservedNegativeAt"`
}

// PactoStatus defines the observed state of Pacto.
// All contract data is exposed as structured fields so external consumers
// can read the CR status directly without parsing contracts themselves.
type PactoStatus struct {
	// ContractStatus is the high-level contract compliance state.
	// This reflects contract validation/compliance and is NOT runtime health.
	// +kubebuilder:validation:Enum=Compliant;Warning;NonCompliant;Reference;Unknown;Invalid;NotEvaluated
	// +optional
	ContractStatus string `json:"contractStatus,omitempty"`

	// EvaluationCoverage reports how many required assertions were evaluated (metadata; never affects
	// ContractStatus).
	// +optional
	EvaluationCoverage *EvaluationCoverage `json:"evaluationCoverage,omitempty"`

	// ObservationWindows tracks per-assertion negative-observation streaks for time-based stabilization
	// (B5). Operator-owned temporal state; the pure engine never sees it.
	// +optional
	ObservationWindows []ObservationWindow `json:"observationWindows,omitempty"`

	// ResolutionPolicy describes how the OCI reference was resolved.
	// Latest: unversioned ref, operator tracks the highest semver tag.
	// PinnedTag: ref includes an explicit tag, used as-is.
	// PinnedDigest: ref includes a digest, used as-is (immutable).
	// Empty for inline contracts.
	// +kubebuilder:validation:Enum=Latest;PinnedTag;PinnedDigest
	// +optional
	ResolutionPolicy string `json:"resolutionPolicy,omitempty"`

	// Summary provides severity-based finding counts.
	// +optional
	Summary *Summary `json:"summary,omitempty"`

	// ContractVersion is the version from the parsed contract.
	// Kept for backward compatibility and simple access via JSONPath.
	// +optional
	ContractVersion string `json:"contractVersion,omitempty"`

	// Contract exposes parsed contract metadata.
	// +optional
	Contract *ContractInfo `json:"contract,omitempty"`

	// Validation describes the structural validation outcome of the contract.
	// +optional
	Validation *ValidationResult `json:"validation,omitempty"`

	// Findings is the list of typed conclusions from the Pacto engine.
	// Includes both contract-only findings (structural/semantic) and
	// evidence-based findings (runtime drift).
	// +optional
	Findings []FindingStatus `json:"findings,omitempty"`

	// Resources describes the existence of target Kubernetes resources.
	// +optional
	Resources *ResourcesStatus `json:"resources,omitempty"`

	// Interfaces lists the parsed interfaces from the contract.
	// +optional
	Interfaces []InterfaceInfo `json:"interfaces,omitempty"`

	// Capabilities lists the declared capabilities from the contract.
	// +optional
	Capabilities []CapabilityInfo `json:"capabilities,omitempty"`

	// Configurations lists the contract's named configuration scopes.
	// Each entry corresponds to one configurations[] entry in the contract.
	// +optional
	Configurations []ConfigurationInfo `json:"configurations,omitempty"`

	// Dependencies lists the declared dependencies from the contract.
	// +optional
	Dependencies []DependencyInfo `json:"dependencies,omitempty"`

	// Policies lists the contract's declared policy sources (metadata only).
	// Each entry describes a local schema or external ref; the operator does not
	// resolve or enforce ref-based policies at runtime.
	// +optional
	Policies []PolicyInfo `json:"policies,omitempty"`

	// ObservedRuntime describes the actual runtime state observed from the cluster.
	// Only populated when a target workload exists. This is a lean evidence view.
	// +optional
	ObservedRuntime *ObservedRuntime `json:"observedRuntime,omitempty"`

	// Readiness is the derived operational readiness assessment of the contract.
	// It is computed from the contract's declared readiness claims and the current
	// time. It is a separate dimension from contract compliance and does NOT affect
	// ContractStatus. Absent when the contract declares no readiness.
	// +optional
	Readiness *ReadinessStatus `json:"readiness,omitempty"`

	// Metadata contains arbitrary key-value pairs from the contract's metadata section.
	// +optional
	Metadata map[string]string `json:"metadata,omitempty"`

	// Conditions represent aggregated state (ContractValid, RuntimeObserved, etc.).
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// CurrentRevision is the name of the active PactoRevision.
	// +optional
	CurrentRevision string `json:"currentRevision,omitempty"`

	// LastReconciledAt is when the last reconciliation completed.
	// +optional
	LastReconciledAt *metav1.Time `json:"lastReconciledAt,omitempty"`

	// ObservedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.contractStatus`
// +kubebuilder:printcolumn:name="Service",type=string,JSONPath=`.spec.target.serviceName`
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.status.contractVersion`
// +kubebuilder:printcolumn:name="Errors",type=integer,JSONPath=`.status.summary.errorCount`
// +kubebuilder:printcolumn:name="Warnings",type=integer,JSONPath=`.status.summary.warningCount`
// +kubebuilder:printcolumn:name="Last Reconciled",type=date,JSONPath=`.status.lastReconciledAt`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Pacto is the Schema for the pactos API.
type Pacto struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Pacto.
	// +required
	Spec PactoSpec `json:"spec"`

	// status defines the observed state of Pacto.
	// +optional
	Status PactoStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PactoList contains a list of Pacto.
type PactoList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Pacto `json:"items"`
}

// IsReference returns true when the Pacto has no runtime target (reference-only contract).
func (p *Pacto) IsReference() bool {
	return p.Spec.Target.ServiceName == "" && p.Spec.Target.WorkloadRef == nil
}

// ResolvedWorkload returns the effective workload name and kind,
// applying defaults when WorkloadRef is not explicitly set.
func (p *Pacto) ResolvedWorkload() (name, kind string) {
	if p.Spec.Target.WorkloadRef != nil {
		kind = p.Spec.Target.WorkloadRef.Kind
		if kind == "" {
			kind = "Deployment"
		}
		return p.Spec.Target.WorkloadRef.Name, kind
	}
	if p.Spec.Target.ServiceName != "" {
		return p.Spec.Target.ServiceName, "Deployment"
	}
	return "", ""
}

func init() {
	SchemeBuilder.Register(&Pacto{}, &PactoList{})
}
