package dashboard

import (
	"strconv"
	"strings"
	"time"

	"github.com/trianalab/pacto/pkg/contract"
	"github.com/trianalab/pacto/pkg/schemax"
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
)

// NormalizeContractStatus maps any non-standard status to one of the five
// canonical contract statuses.
func NormalizeContractStatus(s ContractStatus) ContractStatus {
	switch s {
	case StatusCompliant, StatusWarning, StatusNonCompliant, StatusUnknown, StatusReference:
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
)

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
}

// ReadinessInfo is the derived readiness assessment surfaced in the dashboard.
type ReadinessInfo struct {
	Score         int                  `json:"score"`
	MinScore      int                  `json:"minScore"`
	Passing       bool                 `json:"passing"`
	TotalWeight   int                  `json:"totalWeight"`
	CurrentWeight int                  `json:"currentWeight"`
	CurrentCount  int                  `json:"currentCount"`
	ExpiredCount  int                  `json:"expiredCount"`
	InvalidCount  int                  `json:"invalidCount,omitempty"`
	Checks        []ReadinessCheckInfo `json:"checks"`
}

// ReadinessCheckInfo is a single derived readiness check for the dashboard.
type ReadinessCheckInfo struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	Status        string `json:"status"` // Current | Expired | Invalid
	Evidence      string `json:"evidence,omitempty"`
	Weight        int    `json:"weight"`
	Expires       string `json:"expires"`
	Description   string `json:"description,omitempty"`
	DaysRemaining *int   `json:"daysRemaining,omitempty"`
	// DocPath is set when Evidence resolves to an in-bundle document (an entry in
	// ServiceDetails.Docs), so the UI can render it inline. Empty for external evidence.
	DocPath string `json:"docPath,omitempty"`
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
	ImageRef  string            `json:"imageRef,omitempty"`
	ChartRef  string            `json:"chartRef,omitempty"`
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
	Runtime        *RuntimeInfo        `json:"runtime,omitempty"`
	Scaling        *ScalingInfo        `json:"scaling,omitempty"`
	Policies       []PolicyInfo        `json:"policies,omitempty"`

	Validation *ValidationInfo `json:"validation,omitempty"`

	// Compliance is the computed compliance assessment.
	Compliance *ComplianceInfo `json:"compliance,omitempty"`

	// Readiness is the derived operational readiness assessment. It is a separate
	// dimension from contract compliance and does not affect compliance status.
	Readiness *ReadinessInfo `json:"readiness,omitempty"`

	// Docs are the in-bundle Markdown documents (docs/**/*.md). Populated by
	// bundle-backed sources (local/OCI/cache); empty for k8s-only services.
	Docs []DocInfo `json:"docs,omitempty"`

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
	SectionDependencies    = "dependencies"
	SectionReadiness       = "readiness"
	SectionDocs            = "docs"
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

// InterfaceInfo describes a single service interface.
type InterfaceInfo struct {
	Name            string              `json:"name"`
	Type            string              `json:"type"` // http, grpc, event
	Port            *int                `json:"port,omitempty"`
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
}

// DependencyInfo describes a declared dependency.
type DependencyInfo struct {
	Name          string `json:"name"`
	Ref           string `json:"ref"`
	Required      bool   `json:"required"`
	Compatibility string `json:"compatibility,omitempty"`
}

// RuntimeInfo describes runtime behavior.
type RuntimeInfo struct {
	Workload                string `json:"workload"` // service, job, scheduled
	StateType               string `json:"stateType,omitempty"`
	PersistenceScope        string `json:"persistenceScope,omitempty"`
	PersistenceDurability   string `json:"persistenceDurability,omitempty"`
	DataCriticality         string `json:"dataCriticality,omitempty"`
	UpgradeStrategy         string `json:"upgradeStrategy,omitempty"`
	GracefulShutdownSeconds *int   `json:"gracefulShutdownSeconds,omitempty"`
	HealthInterface         string `json:"healthInterface,omitempty"`
	HealthPath              string `json:"healthPath,omitempty"`
	MetricsInterface        string `json:"metricsInterface,omitempty"`
	MetricsPath             string `json:"metricsPath,omitempty"`
}

// ScalingInfo describes scaling parameters.
type ScalingInfo struct {
	Replicas *int `json:"replicas,omitempty"`
	Min      *int `json:"min,omitempty"`
	Max      *int `json:"max,omitempty"`
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
	case StatusNonCompliant:
		ins = append(ins, Insight{Severity: "critical", Title: "Contract is non-compliant", Description: "One or more critical validation checks have failed."})
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
	// Readiness is the derived operational readiness assessment, carried on the
	// list entry so the aggregated readiness overview can summarize, sort, and
	// filter every service from a single /api/services call (mirroring how the
	// owners view aggregates client-side). Nil when the service declares no
	// readiness block. It is a separate dimension from contract compliance.
	Readiness *ReadinessInfo `json:"readiness,omitempty"`
}
