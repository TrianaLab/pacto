package dashboard

import (
	"strconv"
	"strings"
	"time"

	"github.com/trianalab/pacto/v2/pkg/contract"
	"github.com/trianalab/pacto/v2/pkg/sbom"
	"github.com/trianalab/pacto/v2/pkg/schemax"
)

// ContractStatus represents the contract compliance status of a service.
// It reflects whether the service's contract/bundle is valid and compliant,
// NOT the runtime health of the service itself.
type ContractStatus string

const (
	StatusCompliant    ContractStatus = "Compliant"
	StatusWarning      ContractStatus = "Warning"
	StatusNonCompliant ContractStatus = "NonCompliant"
	StatusUnknown      ContractStatus = "Unknown"
	StatusReference    ContractStatus = "Reference"
	StatusInvalid      ContractStatus = "Invalid"
	StatusNotEvaluated ContractStatus = "NotEvaluated"
)

// NormalizeContractStatus maps any non-standard status to one of the canonical
// contract statuses.
func NormalizeContractStatus(s ContractStatus) ContractStatus {
	switch s {
	case StatusCompliant, StatusWarning, StatusNonCompliant, StatusUnknown, StatusReference, StatusInvalid, StatusNotEvaluated:
		return s
	default:
		return StatusUnknown
	}
}

// ComplianceStatus represents the overall compliance assessment of a service.
type ComplianceStatus string

const (
	ComplianceOK        ComplianceStatus = "OK"
	ComplianceWarning   ComplianceStatus = "WARNING"
	ComplianceError     ComplianceStatus = "ERROR"
	ComplianceReference ComplianceStatus = "REFERENCE"
	ComplianceUnknown   ComplianceStatus = "UNKNOWN"
)

// EvaluationCoverage reports how many required assertions were evaluated.
type EvaluationCoverage struct {
	Evaluated int `json:"evaluated"` // required assertions with Outcome == Observed
	Required  int `json:"required"`  // total required assertions
}

// ComplianceInfo holds the computed compliance state for a service.
type ComplianceInfo struct {
	Status  ComplianceStatus  `json:"status"`
	Score   *int              `json:"score"`
	Summary *ComplianceCounts `json:"summary,omitempty"`
}

// ComplianceCounts summarizes validation check results.
type ComplianceCounts struct {
	Total    int `json:"total"`
	Passed   int `json:"passed"`
	Failed   int `json:"failed"`
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Unknown  int `json:"unknown"`
	// Secondary metrics per B-2 ruling (informational, distinguish failure from uncertainty).
	RuntimeEvaluated int `json:"runtimeEvaluated"` // Compliant + Warning + NonCompliant + Unknown
	Conclusive       int `json:"conclusive"`       // Compliant + Warning + NonCompliant
}

// ReadinessInfo is the derived readiness assessment surfaced in the dashboard.
type ReadinessInfo struct {
	Score         int     `json:"score"`
	MinScore      int     `json:"minScore"`
	TotalWeight   int     `json:"totalWeight"`
	EarnedWeight  int     `json:"earnedWeight"`
	PartialCredit float64 `json:"partialCredit"`
	Passing       bool    `json:"passing"`
	Expires       string  `json:"expires"`
	Expired       bool    `json:"expired"`
	DaysRemaining *int    `json:"daysRemaining,omitempty"`
	// Status counts use NO omitempty — 0 is a valid value.
	DoneCount     int                     `json:"doneCount"`
	PartialCount  int                     `json:"partialCount"`
	NotDoneCount  int                     `json:"notDoneCount"`
	DeferredCount int                     `json:"deferredCount"`
	Revisions     []ReadinessRevisionInfo `json:"revisions,omitempty"`
	Checks        []ReadinessCheckInfo    `json:"checks"`
}

// ReadinessCheckInfo is a single derived readiness check for the dashboard.
type ReadinessCheckInfo struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	Category     string `json:"category,omitempty"`
	Status       string `json:"status"` // done | partial | not-done | deferred
	Evidence     string `json:"evidence,omitempty"`
	Description  string `json:"description,omitempty"`
	Weight       int    `json:"weight"`
	EarnedWeight int    `json:"earnedWeight"`
	Excluded     bool   `json:"excluded"`
	// DocPath is set when Evidence resolves to an in-bundle document (an entry in
	// ServiceDetails.Docs), so the UI can render it inline. Empty for external evidence.
	DocPath string `json:"docPath,omitempty"`
}

// ReadinessRevisionInfo is a single readiness revision-history entry.
type ReadinessRevisionInfo struct {
	Date        string `json:"date"`
	Version     string `json:"version"`
	Author      string `json:"author"`
	Description string `json:"description"`
}

// DocInfo is one in-bundle Markdown document (docs/**/*.md) surfaced in the dashboard.
type DocInfo struct {
	Path      string `json:"path"`  // bundle-relative, e.g. docs/runbooks/payment-api.md
	Title     string `json:"title"` // first H1, else humanized filename
	Content   string `json:"content"`
	Truncated bool   `json:"truncated,omitempty"`
}

// ObservedRuntime holds runtime state observed by the operator from the cluster.
type ObservedRuntime struct {
	WorkloadKind                  string   `json:"workloadKind,omitempty"`
	DeploymentStrategy            string   `json:"deploymentStrategy,omitempty"`
	PodManagementPolicy           string   `json:"podManagementPolicy,omitempty"`
	TerminationGracePeriodSeconds *int     `json:"terminationGracePeriodSeconds,omitempty"`
	ContainerImages               []string `json:"containerImages,omitempty"`
	HasPVC                        *bool    `json:"hasPVC,omitempty"`
	HasEmptyDir                   *bool    `json:"hasEmptyDir,omitempty"`
	HealthProbeInitialDelay       *int     `json:"healthProbeInitialDelaySeconds,omitempty"`
}

// RuntimeDiffRow represents a single contract-vs-runtime comparison.
type RuntimeDiffRow struct {
	Field         string `json:"field"`
	ContractPath  string `json:"contractPath,omitempty"`
	DeclaredValue string `json:"declaredValue"`
	ObservedValue string `json:"observedValue"`
	Status        string `json:"status"` // match, mismatch, skipped, not_applicable
}

// Service is a summary entry for the service list view.
type Service struct {
	Name           string         `json:"name"`
	Version        string         `json:"version"`
	Owner          contract.Owner `json:"owner,omitempty"`
	ContractStatus ContractStatus `json:"contractStatus"`
	Source         string         `json:"source"`            // primary source: k8s, oci, local
	Sources        []string       `json:"sources,omitempty"` // all sources this service appears in
}

// ServiceDetails contains all information for the service detail view.
type ServiceDetails struct {
	Service

	Namespace string            `json:"namespace,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`

	// Contract references from operator.
	ResolvedRef     string `json:"resolvedRef,omitempty"`
	CurrentRevision string `json:"currentRevision,omitempty"`

	// Version tracking: derived from resolvedRef and version history.
	VersionPolicy   string `json:"versionPolicy,omitempty"`   // "tracking", "pinned-tag", "pinned-digest"
	LatestAvailable string `json:"latestAvailable,omitempty"` // highest semver from version history
	UpdateAvailable bool   `json:"updateAvailable,omitempty"` // true when latestAvailable > version

	Interfaces     []InterfaceInfo     `json:"interfaces,omitempty"`
	Configurations []ConfigurationInfo `json:"configurations,omitempty"`
	Dependencies   []DependencyInfo    `json:"dependencies,omitempty"`
	Workload       string              `json:"workload,omitempty"`
	State          *StateInfo          `json:"state,omitempty"`
	Capabilities   []CapabilityInfo    `json:"capabilities,omitempty"`
	Policies       []PolicyInfo        `json:"policies,omitempty"`

	// Tools are agent-invocable capabilities derived from openapi interfaces'
	// operations. Populated by bundle-backed sources (local/OCI/cache);
	// empty for k8s-only services (no bundle FS to parse).
	Tools []CapabilityTool `json:"tools,omitempty"`

	// Skills are optional domain-knowledge documents (skills/*.md) shipped in the
	// bundle. Populated by bundle-backed sources; empty for k8s-only services.
	Skills []SkillInfo `json:"skills,omitempty"`

	Validation *ValidationInfo `json:"validation,omitempty"`

	// Lock surfaces the committed pacto.lock pins. Nil when no lockfile is present
	// (or for sources that cannot read it from disk, e.g. OCI/cache/k8s).
	Lock *LockInfo `json:"lock,omitempty"`

	// Compliance is the computed compliance assessment.
	Compliance *ComplianceInfo `json:"compliance,omitempty"`

	// EvaluationCoverage reports how many required assertions were evaluated.
	// Metadata only, never changes compliance status.
	EvaluationCoverage *EvaluationCoverage `json:"evaluationCoverage,omitempty"`

	// Readiness is the derived operational readiness assessment. It is a separate
	// dimension from contract compliance and does not affect compliance status.
	Readiness *ReadinessInfo `json:"readiness,omitempty"`

	// Docs are the in-bundle Markdown documents (docs/**/*.md). Populated by
	// bundle-backed sources (local/OCI/cache); empty for k8s-only services.
	Docs []DocInfo `json:"docs,omitempty"`

	// SBOM is the parsed software bill of materials from the bundle's sbom/
	// directory (SPDX or CycloneDX). Nil when the bundle has no SBOM.
	SBOM *sbom.Document `json:"sbom,omitempty"`

	// ObservedRuntime holds runtime state observed by the operator.
	ObservedRuntime *ObservedRuntime `json:"observedRuntime,omitempty"`

	// RuntimeDiff is the semantic contract-vs-runtime comparison.
	RuntimeDiff []RuntimeDiffRow `json:"runtimeDiff,omitempty"`

	// Endpoints surfaced from runtime (k8s).
	Endpoints []EndpointStatus `json:"endpoints,omitempty"`

	// Conditions from operator CRD status.
	Conditions []Condition `json:"conditions,omitempty"`

	// Insights are computed diagnostic messages (critical, warning, info).
	Insights []Insight `json:"insights,omitempty"`

	// ChecksSummary from operator (passed/total checks).
	ChecksSummary *ChecksSummary `json:"checksSummary,omitempty"`

	// Kubernetes-specific fields, populated only by k8s source.
	Resources *ResourcesInfo `json:"resources,omitempty"`
	Ports     *PortsInfo     `json:"ports,omitempty"`

	LastUpdated      *time.Time `json:"lastUpdated,omitempty"`
	LastReconciledAt string     `json:"lastReconciledAt,omitempty"`

	// RuntimeEvaluated is true only when a Kubernetes runtime overlay was applied
	// (the operator actually observed this service). When false, the view is
	// "definition only" — runtime status/sections cannot be asserted.
	RuntimeEvaluated bool `json:"runtimeEvaluated,omitempty"`

	// SectionMeta describes, per section id, why a section is shown or absent and
	// which source supplied it — so the UI never silently hides data. Keys are the
	// Section* ids. Populated by the resolver after the merge, or by the server's
	// getService handler as a fallback on the non-resolved single-source path.
	SectionMeta map[string]SectionInfo `json:"sectionMeta,omitempty"`
}

// Section state values for SectionInfo.State.
const (
	SectionPresent       = "present"        // has data from an available source
	SectionEmpty         = "empty"          // applicable + source available, but nothing declared
	SectionNotApplicable = "not_applicable" // cannot apply to this contract (e.g. runtime on a reference)
	SectionUnavailable   = "unavailable"    // a source that would supply it was unreachable / not present
)

// Section ids for SectionMeta (sections embedded in a GetService response).
// Version history / dependents / cross-refs are fetched separately and carry
// their own state on the client.
const (
	SectionInterfaces      = "interfaces"
	SectionConfigurations  = "configurations"
	SectionPolicies        = "policies"
	SectionCapabilities    = "capabilities"
	SectionDependencies    = "dependencies"
	SectionReadiness       = "readiness"
	SectionDocs            = "docs"
	SectionSBOM            = "sbom"
	SectionRuntime         = "runtime"
	SectionValidation      = "validation"
	SectionObservedRuntime = "observedRuntime"
	SectionRuntimeDiff     = "runtimeDiff"
	SectionResources       = "resources"
	SectionPorts           = "ports"
	SectionEndpoints       = "endpoints"
	SectionConditions      = "conditions"
)

// SectionInfo records the availability + provenance of one dashboard section so
// the UI can explain absence (not-applicable vs unavailable vs empty) and label
// where present data came from.
type SectionInfo struct {
	State        string `json:"state"`                  // one of the Section* state consts
	Reason       string `json:"reason,omitempty"`       // human note for non-present states
	Source       string `json:"source,omitempty"`       // "k8s" | "oci" | "local" | "cache"
	OverriddenBy string `json:"overriddenBy,omitempty"` // source that overrode a contract value (e.g. "k8s")
}

// InterfaceEndpoint is a single API endpoint parsed from an OpenAPI spec.
type InterfaceEndpoint struct {
	Method  string `json:"method"`
	Path    string `json:"path"`
	Summary string `json:"summary,omitempty"`
}

// CapabilityTool is an agent-invocable operation derived from an OpenAPI
// operation in an http interface. Mutating has no omitempty: false is a
// meaningful, displayed value (read-only operations).
type CapabilityTool struct {
	Name     string `json:"name"`
	Method   string `json:"method"`
	Path     string `json:"path"`
	Summary  string `json:"summary,omitempty"`
	Mutating bool   `json:"mutating"`
}

// SkillInfo is a single bundled domain-knowledge document (skills/*.md).
type SkillInfo struct {
	Name    string `json:"name"`
	Content string `json:"content,omitempty"`
}

// InterfaceInfo describes a single service interface.
type InterfaceInfo struct {
	Name            string              `json:"name"`
	Type            string              `json:"type"` // openapi, asyncapi, grpc
	Visibility      string              `json:"visibility,omitempty"`
	HasContractFile bool                `json:"hasContractFile,omitempty"`
	ContractFile    string              `json:"contractFile,omitempty"`
	ContractContent string              `json:"contractContent,omitempty"`
	Endpoints       []InterfaceEndpoint `json:"endpoints,omitempty"`
}

// ConfigValue is a flattened key/value/type entry for display. It aliases
// schemax.Property so the dashboard and operator share one representation.
type ConfigValue = schemax.Property

// ConfigurationInfo describes a single configuration scope.
type ConfigurationInfo struct {
	Name       string        `json:"name,omitempty"`
	HasSchema  bool          `json:"hasSchema"`
	Schema     string        `json:"schema,omitempty"`
	Ref        string        `json:"ref,omitempty"`
	ValueKeys  []string      `json:"valueKeys,omitempty"`
	SecretKeys []string      `json:"secretKeys,omitempty"`
	Values     []ConfigValue `json:"values,omitempty"`
	// ValuesAreCurrent is set when a remote ref's values were resolved from the
	// referenced service's CURRENT version (because the ref is not version-pinned,
	// or the pinned version was unavailable) while displaying a historical version.
	// The UI labels such values "(current)" to flag the temporal mismatch.
	ValuesAreCurrent bool `json:"valuesAreCurrent,omitempty"`
	// Lock pins, populated from pacto.lock references (kind=config) when present.
	LockedDigest  string `json:"lockedDigest,omitempty"`
	LockedVersion string `json:"lockedVersion,omitempty"`
}

// DependencyInfo describes a declared dependency.
type DependencyInfo struct {
	Name          string `json:"name"`
	Ref           string `json:"ref"`
	Required      bool   `json:"required"`
	Compatibility string `json:"compatibility,omitempty"`
	// Lock pins, populated from pacto.lock when present (local source only).
	LockedDigest  string `json:"lockedDigest,omitempty"`
	LockedVersion string `json:"lockedVersion,omitempty"`
	// DriftStatus compares the locked digest against the dependency target's
	// runtime digest: "locked", "drift", "unlocked", "unknown". Empty when not computed.
	DriftStatus string `json:"driftStatus,omitempty"`
}

// LockInfo surfaces pacto.lock content for the service detail view. It is set
// only by sources that can read the committed lockfile from disk (local).
type LockInfo struct {
	Present      bool          `json:"present"`
	RootDigest   string        `json:"rootDigest,omitempty"`
	Dependencies []LockDepInfo `json:"dependencies,omitempty"`
	References   []LockRefInfo `json:"references,omitempty"`
}

// LockDepInfo is one resolved dependency entry from pacto.lock.
type LockDepInfo struct {
	Name        string `json:"name"`
	Source      string `json:"source,omitempty"`
	Ref         string `json:"ref,omitempty"`
	Path        string `json:"path,omitempty"`
	Constraint  string `json:"constraint,omitempty"`
	Version     string `json:"version,omitempty"`
	Digest      string `json:"digest,omitempty"`
	ContentHash string `json:"contentHash,omitempty"`
}

// LockRefInfo is one resolved config/policy reference from pacto.lock.
// Config/policy references carry no compatibility constraint (unlike
// dependencies), so there is no Constraint field.
type LockRefInfo struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Source      string `json:"source,omitempty"`
	Ref         string `json:"ref,omitempty"`
	Path        string `json:"path,omitempty"`
	Version     string `json:"version,omitempty"`
	Digest      string `json:"digest,omitempty"`
	ContentHash string `json:"contentHash,omitempty"`
}

// StateInfo describes the state semantics of the service.
type StateInfo struct {
	Type                  string `json:"type"`
	PersistenceScope      string `json:"persistenceScope,omitempty"`
	PersistenceDurability string `json:"persistenceDurability,omitempty"`
	DataCriticality       string `json:"dataCriticality,omitempty"`
}

// CapabilityInfo describes a service capability.
type CapabilityInfo struct {
	Type string `json:"type"`
	Ref  string `json:"ref,omitempty"`
}

// PolicyInfo describes an attached policy (JSON Schema constraint).
type PolicyInfo struct {
	Name        string        `json:"name"`
	HasSchema   bool          `json:"hasSchema"`
	Schema      string        `json:"schema,omitempty"`
	Ref         string        `json:"ref,omitempty"`
	Title       string        `json:"title,omitempty"`
	Description string        `json:"description,omitempty"`
	Content     string        `json:"content,omitempty"`
	Values      []ConfigValue `json:"values,omitempty"`
	// ValuesAreCurrent is set when a remote ref's values were resolved from the
	// referenced service's CURRENT version (unpinned ref, or pinned version
	// unavailable) while displaying a historical version. The UI labels such
	// values "(current)" to flag the temporal mismatch.
	ValuesAreCurrent bool `json:"valuesAreCurrent,omitempty"`
	// Lock pins, populated from pacto.lock references (kind=policy) when present.
	LockedDigest  string `json:"lockedDigest,omitempty"`
	LockedVersion string `json:"lockedVersion,omitempty"`
}

// ValidationInfo holds validation results.
type ValidationInfo struct {
	Valid    bool              `json:"valid"`
	Errors   []ValidationIssue `json:"errors,omitempty"`
	Warnings []ValidationIssue `json:"warnings,omitempty"`
}

// ValidationIssue represents a single validation error or warning.
type ValidationIssue struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

// ResourcesInfo holds Kubernetes resource existence checks.
type ResourcesInfo struct {
	ServiceExists  *bool `json:"serviceExists,omitempty"`
	WorkloadExists *bool `json:"workloadExists,omitempty"`
}

// PortsInfo holds port comparison results.
type PortsInfo struct {
	Expected   []int `json:"expected,omitempty"`
	Observed   []int `json:"observed,omitempty"`
	Missing    []int `json:"missing,omitempty"`
	Unexpected []int `json:"unexpected,omitempty"`
}

// Version represents a historical version of a service.
type Version struct {
	Version        string     `json:"version"`
	Ref            string     `json:"ref,omitempty"`
	ContractHash   string     `json:"contractHash,omitempty"`
	CreatedAt      *time.Time `json:"createdAt,omitempty"`
	Source         string     `json:"source,omitempty"`         // origin: "k8s", "oci", "local"
	Classification string     `json:"classification,omitempty"` // diff vs previous: "NON_BREAKING", "POTENTIAL_BREAKING", "BREAKING"
	IsCurrent      bool       `json:"isCurrent,omitempty"`      // true for the version currently deployed/active
}

// Ref identifies a specific version of a service for diffing.
type Ref struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	// Source is optional; defaults to the active data source.
	Source string `json:"source,omitempty"`
}

// DiffResult holds the output of comparing two service versions.
type DiffResult struct {
	From           Ref          `json:"from"`
	To             Ref          `json:"to"`
	Classification string       `json:"classification"` // NON_BREAKING, POTENTIAL_BREAKING, BREAKING
	Changes        []DiffChange `json:"changes"`
}

// DiffChange represents a single detected change.
type DiffChange struct {
	Path           string `json:"path"`
	Type           string `json:"type"` // added, removed, modified
	OldValue       any    `json:"oldValue,omitempty"`
	NewValue       any    `json:"newValue,omitempty"`
	Classification string `json:"classification"`
	Reason         string `json:"reason,omitempty"`
}

// AggregatedService groups data for the same service across multiple sources.
type AggregatedService struct {
	Name    string              `json:"name"`
	Sources []ServiceSourceData `json:"sources"`

	// Merged is the priority-merged view: k8s for runtime, oci for versions, local for in-progress.
	Merged *ServiceDetails `json:"merged"`
}

// ServiceSourceData holds service details from a single source.
type ServiceSourceData struct {
	SourceType string          `json:"sourceType"` // "k8s", "oci", "local"
	Service    *ServiceDetails `json:"service"`
}

// EndpointStatus describes the observed status of a service endpoint.
type EndpointStatus struct {
	Interface  string `json:"interface"`
	Type       string `json:"type,omitempty"` // "health", "metrics", or empty
	URL        string `json:"url,omitempty"`
	Healthy    *bool  `json:"healthy,omitempty"`
	StatusCode *int   `json:"statusCode,omitempty"`
	LatencyMs  *int64 `json:"latencyMs,omitempty"`
	Error      string `json:"error,omitempty"`
	Message    string `json:"message,omitempty"`
}

// SourceInfo describes a detected data source and its availability.
type SourceInfo struct {
	Type    string `json:"type"` // "k8s", "oci", "local"
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason,omitempty"` // why enabled/disabled
}

// DependencyGraph holds a resolved dependency tree for visualization.
type DependencyGraph struct {
	Root      *GraphNode `json:"root"`
	Cycles    [][]string `json:"cycles,omitempty"`
	Conflicts []string   `json:"conflicts,omitempty"`
}

// GraphNode represents a node in the dependency graph.
type GraphNode struct {
	Name         string      `json:"name"`
	Version      string      `json:"version"`
	Ref          string      `json:"ref,omitempty"`
	Dependencies []GraphEdge `json:"dependencies,omitempty"`
}

// GraphEdge represents an edge in the dependency graph.
type GraphEdge struct {
	Ref           string     `json:"ref"`
	Required      bool       `json:"required"`
	Compatibility string     `json:"compatibility,omitempty"`
	Error         string     `json:"error,omitempty"`
	Shared        bool       `json:"shared,omitempty"`
	Node          *GraphNode `json:"node,omitempty"`
	// Lock pins from pacto.lock, carried on the edge for the dependency tree view.
	LockedDigest  string `json:"lockedDigest,omitempty"`
	LockedVersion string `json:"lockedVersion,omitempty"`
	DriftStatus   string `json:"driftStatus,omitempty"`
}

// Condition represents a reconciliation condition (mirroring operator CRD status.conditions).
type Condition struct {
	Type              string `json:"type"`
	Status            string `json:"status"` // "True", "False", "Unknown"
	Reason            string `json:"reason,omitempty"`
	Message           string `json:"message,omitempty"`
	Severity          string `json:"severity,omitempty"` // "error", "warning"
	LastTransitionAgo string `json:"lastTransitionAgo,omitempty"`
}

// Insight represents a diagnostic finding (critical, warning, info).
type Insight struct {
	Severity    string `json:"severity"` // "critical", "warning", "info"
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

// ChecksSummary holds pass/fail check counts.
type ChecksSummary struct {
	Total  int `json:"total"`
	Passed int `json:"passed"`
	Failed int `json:"failed"`
}

// GenerateInsights derives diagnostic insights from the service details
// when no operator-provided insights exist. This is the single source of
// truth for insight generation — the UI consumes these directly.
func (d *ServiceDetails) GenerateInsights() {
	if len(d.Insights) > 0 {
		return
	}
	var ins []Insight

	switch d.ContractStatus {
	case StatusInvalid:
		ins = append(ins, Insight{Severity: "critical", Title: "Contract is malformed", Description: "The contract failed structural validation and cannot be evaluated."})
	case StatusNonCompliant:
		ins = append(ins, Insight{Severity: "critical", Title: "Contract is non-compliant", Description: "One or more critical validation checks have failed."})
	case StatusUnknown:
		ins = append(ins, Insight{Severity: "warning", Title: "Contract evaluation inconclusive", Description: "A required assertion could not be verified."})
	case StatusWarning:
		ins = append(ins, Insight{Severity: "warning", Title: "Contract has warnings", Description: "Some validation checks are failing."})
	}

	ins = append(ins, validationInsights(d.Validation)...)
	ins = append(ins, resourceInsights(d.Resources)...)
	ins = append(ins, portInsights(d.Ports)...)

	d.Insights = ins
}

func validationInsights(v *ValidationInfo) []Insight {
	if v == nil {
		return nil
	}
	var ins []Insight
	if n := len(v.Errors); n > 0 {
		ins = append(ins, Insight{Severity: "critical", Title: strconv.Itoa(n) + " validation error" + plural(n), Description: v.Errors[0].Message})
	}
	if n := len(v.Warnings); n > 0 {
		ins = append(ins, Insight{Severity: "warning", Title: strconv.Itoa(n) + " validation warning" + plural(n), Description: v.Warnings[0].Message})
	}
	return ins
}

func resourceInsights(r *ResourcesInfo) []Insight {
	if r == nil {
		return nil
	}
	var ins []Insight
	if r.ServiceExists != nil && !*r.ServiceExists {
		ins = append(ins, Insight{Severity: "critical", Title: "Service resource not found", Description: "The Kubernetes Service resource does not exist."})
	}
	if r.WorkloadExists != nil && !*r.WorkloadExists {
		ins = append(ins, Insight{Severity: "critical", Title: "Workload not found", Description: "The target workload does not exist."})
	}
	return ins
}

func portInsights(p *PortsInfo) []Insight {
	if p == nil {
		return nil
	}
	var ins []Insight
	if len(p.Missing) > 0 {
		ins = append(ins, Insight{Severity: "warning", Title: "Missing ports: " + joinInts(p.Missing), Description: "Ports declared in contract but not found on the service."})
	}
	if len(p.Unexpected) > 0 {
		ins = append(ins, Insight{Severity: "info", Title: "Unexpected ports: " + joinInts(p.Unexpected), Description: "Ports found on the service but not declared in contract."})
	}
	return ins
}

func plural(n int) string {
	if n != 1 {
		return "s"
	}
	return ""
}

func joinInts(vals []int) string {
	s := make([]string, len(vals))
	for i, v := range vals {
		s[i] = strconv.Itoa(v)
	}
	return strings.Join(s, ", ")
}

// ServiceListEntry is an enriched Service for the list view, including
// blast radius, dependency count, checks summary, compliance, and top insight.
type ServiceListEntry struct {
	Service
	Namespace        string           `json:"namespace,omitempty"`
	BlastRadius      int              `json:"blastRadius,omitempty"`
	DependencyCount  int              `json:"dependencyCount,omitempty"`
	ChecksPassed     int              `json:"checksPassed"`
	ChecksTotal      int              `json:"checksTotal"`
	ChecksFailed     int              `json:"checksFailed"`
	ComplianceStatus ComplianceStatus `json:"complianceStatus"`
	ComplianceScore  *int             `json:"complianceScore"`
	ComplianceErrors int              `json:"complianceErrors"`
	ComplianceWarns  int              `json:"complianceWarnings"`
	TopInsight       string           `json:"topInsight,omitempty"`
	UpdateAvailable  bool             `json:"updateAvailable,omitempty"`
	// EvaluationCoverage feeds the list's compact coverage badge (E of R required
	// assertions evaluated). Metadata only, never changes status. Nil when the
	// service was not runtime-evaluated.
	EvaluationCoverage *EvaluationCoverage `json:"evaluationCoverage,omitempty"`
	// Readiness is the derived operational readiness assessment, carried on the
	// list entry so the aggregated readiness overview can summarize, sort, and
	// filter every service from a single /api/services call (mirroring how the
	// owners view aggregates client-side). Nil when the service declares no
	// readiness block. It is a separate dimension from contract compliance.
	Readiness *ReadinessInfo `json:"readiness,omitempty"`
}
