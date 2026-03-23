package dashboard

import "time"

// Phase represents the overall health status of a service.
type Phase string

const (
	PhaseHealthy  Phase = "Healthy"
	PhaseDegraded Phase = "Degraded"
	PhaseInvalid  Phase = "Invalid"
	PhaseUnknown  Phase = "Unknown"
)

// NormalizePhase maps any non-standard phase (e.g. "Reference" from the K8s
// operator) to one of the four canonical dashboard phases.
func NormalizePhase(p Phase) Phase {
	switch p {
	case PhaseHealthy, PhaseDegraded, PhaseInvalid, PhaseUnknown:
		return p
	default:
		return PhaseUnknown
	}
}

// Service is a summary entry for the service list view.
type Service struct {
	Name    string   `json:"name"`
	Version string   `json:"version"`
	Owner   string   `json:"owner,omitempty"`
	Phase   Phase    `json:"phase"`
	Source  string   `json:"source"`            // primary source: k8s, oci, local
	Sources []string `json:"sources,omitempty"` // all sources this service appears in
}

// ServiceDetails contains all information for the service detail view.
type ServiceDetails struct {
	Service

	ImageRef string            `json:"imageRef,omitempty"`
	ChartRef string            `json:"chartRef,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`

	Interfaces    []InterfaceInfo    `json:"interfaces,omitempty"`
	Configuration *ConfigurationInfo `json:"configuration,omitempty"`
	Dependencies  []DependencyInfo   `json:"dependencies,omitempty"`
	Runtime       *RuntimeInfo       `json:"runtime,omitempty"`
	Scaling       *ScalingInfo       `json:"scaling,omitempty"`
	Policy        *PolicyInfo        `json:"policy,omitempty"`

	Validation *ValidationInfo `json:"validation,omitempty"`

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

// ConfigValue is a flattened key/value/type entry for display.
type ConfigValue struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Type  string `json:"type"`
}

// ConfigurationInfo describes the configuration section.
type ConfigurationInfo struct {
	HasSchema  bool          `json:"hasSchema"`
	Schema     string        `json:"schema,omitempty"`
	Ref        string        `json:"ref,omitempty"`
	ValueKeys  []string      `json:"valueKeys,omitempty"`
	SecretKeys []string      `json:"secretKeys,omitempty"`
	Values     []ConfigValue `json:"values,omitempty"`
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
	HasSchema bool          `json:"hasSchema"`
	Schema    string        `json:"schema,omitempty"`
	Ref       string        `json:"ref,omitempty"`
	Content   string        `json:"content,omitempty"`
	Values    []ConfigValue `json:"values,omitempty"`
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
	Version      string     `json:"version"`
	Ref          string     `json:"ref,omitempty"`
	ContractHash string     `json:"contractHash,omitempty"`
	CreatedAt    *time.Time `json:"createdAt,omitempty"`
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

// ServiceListEntry is an enriched Service for the list view, including
// blast radius, dependency count, checks summary, and top insight.
type ServiceListEntry struct {
	Service
	BlastRadius     int    `json:"blastRadius,omitempty"`
	DependencyCount int    `json:"dependencyCount,omitempty"`
	ChecksPassed    int    `json:"checksPassed"`
	ChecksTotal     int    `json:"checksTotal"`
	ChecksFailed    int    `json:"checksFailed"`
	TopInsight      string `json:"topInsight,omitempty"`
}
