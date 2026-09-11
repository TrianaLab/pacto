package dashboard

import (
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/contractview"
	depgraph "github.com/trianalab/pacto/v3/pkg/graph"
	"github.com/trianalab/pacto/v3/pkg/lock"
)

// The contract-view half of this package — the DTOs that describe ONE service's
// contract, and the pure functions that build them from a bundle — now lives in
// [github.com/trianalab/pacto/v3/pkg/contractview], a leaf with no HTTP, no Huma
// and no fleet in it. That is what lets `pacto doc` render an offline page
// without linking a server.
//
// These are aliases, not copies: pkg/dashboard is a released v3 package, its DTO
// names appear in the published OpenAPI document and in the generated TypeScript
// SDK, and a v3 importer must keep compiling. Alias identity also keeps the two
// spellings interchangeable at every call site, so nothing has to convert.
//
// New code should import pkg/contractview directly.

// ── DTOs ─────────────────────────────────────────────────────────────

type ValidationCatalogEntry = contractview.ValidationCatalogEntry
type DependentInfo = contractview.DependentInfo
type CrossReference = contractview.CrossReference
type CrossReferences = contractview.CrossReferences
type GraphNodeData = contractview.GraphNodeData
type GraphEdgeData = contractview.GraphEdgeData
type GlobalGraph = contractview.GlobalGraph
type ContractStatus = contractview.ContractStatus
type ComplianceStatus = contractview.ComplianceStatus
type EvaluationCoverage = contractview.EvaluationCoverage
type ComplianceInfo = contractview.ComplianceInfo
type ComplianceCounts = contractview.ComplianceCounts
type ReadinessInfo = contractview.ReadinessInfo
type ReadinessCheckInfo = contractview.ReadinessCheckInfo
type ReadinessRevisionInfo = contractview.ReadinessRevisionInfo
type DocInfo = contractview.DocInfo
type ObservedRuntime = contractview.ObservedRuntime
type RuntimeDiffRow = contractview.RuntimeDiffRow
type Service = contractview.Service
type ServiceDetails = contractview.ServiceDetails
type InterfaceEndpoint = contractview.InterfaceEndpoint
type CapabilityTool = contractview.CapabilityTool
type SkillInfo = contractview.SkillInfo
type InterfaceInfo = contractview.InterfaceInfo
type ConfigValue = contractview.ConfigValue
type ConfigurationInfo = contractview.ConfigurationInfo
type DependencyInfo = contractview.DependencyInfo
type LockInfo = contractview.LockInfo
type LockDepInfo = contractview.LockDepInfo
type LockRefInfo = contractview.LockRefInfo
type StateInfo = contractview.StateInfo
type CapabilityInfo = contractview.CapabilityInfo
type PolicyInfo = contractview.PolicyInfo
type ValidationInfo = contractview.ValidationInfo
type ValidationIssue = contractview.ValidationIssue
type ResourcesInfo = contractview.ResourcesInfo
type PortsInfo = contractview.PortsInfo
type Version = contractview.Version
type Ref = contractview.Ref
type DiffResult = contractview.DiffResult
type DiffChange = contractview.DiffChange
type AggregatedService = contractview.AggregatedService
type ServiceSourceData = contractview.ServiceSourceData
type EndpointStatus = contractview.EndpointStatus
type DependencyGraph = contractview.DependencyGraph
type GraphNode = contractview.GraphNode
type GraphEdge = contractview.GraphEdge
type Condition = contractview.Condition
type Insight = contractview.Insight
type ChecksSummary = contractview.ChecksSummary
type ServiceListEntry = contractview.ServiceListEntry

// ── Vocabularies ─────────────────────────────────────────────────────

const (
	StatusCompliant     = contractview.StatusCompliant
	StatusWarning       = contractview.StatusWarning
	StatusNonCompliant  = contractview.StatusNonCompliant
	StatusUnknown       = contractview.StatusUnknown
	StatusReference     = contractview.StatusReference
	StatusInvalid       = contractview.StatusInvalid
	StatusNotEvaluated  = contractview.StatusNotEvaluated
	ComplianceOK        = contractview.ComplianceOK
	ComplianceWarning   = contractview.ComplianceWarning
	ComplianceError     = contractview.ComplianceError
	ComplianceReference = contractview.ComplianceReference
	ComplianceUnknown   = contractview.ComplianceUnknown
)

// ── Per-section availability (inert) ─────────────────────────────────
//
// SectionInfo and the Section* vocabularies describe [ServiceDetails.SectionMeta],
// a map that reported, per section of a service page, whether the data was
// present, empty, not applicable or from an unreachable source.
//
// Nothing populates it. Its two writers were the dashboard's multi-source
// resolver and the getService handler — both needed to know which source
// answered, and both went when the fleet took that job over. The offline export
// is now the only producer of a ServiceDetails and never wrote it. Source
// availability is reported per source by /api/sources, and the dashboard's own
// service page decides per section whether it has content to show.
//
// They stay because they are released v3 API and appear in the published OpenAPI
// document; the field is `omitempty`, so it is simply absent on the wire.

// Deprecated: describes [ServiceDetails.SectionMeta], which nothing populates. Removed at v4.
type SectionInfo = contractview.SectionInfo

// Deprecated: values of [SectionInfo].State, which nothing populates. Removed at v4.
const (
	SectionPresent       = contractview.SectionPresent
	SectionEmpty         = contractview.SectionEmpty
	SectionNotApplicable = contractview.SectionNotApplicable
	SectionUnavailable   = contractview.SectionUnavailable
)

// Deprecated: keys of [ServiceDetails.SectionMeta], which nothing populates. Removed at v4.
const (
	SectionInterfaces      = contractview.SectionInterfaces
	SectionConfigurations  = contractview.SectionConfigurations
	SectionPolicies        = contractview.SectionPolicies
	SectionCapabilities    = contractview.SectionCapabilities
	SectionDependencies    = contractview.SectionDependencies
	SectionReadiness       = contractview.SectionReadiness
	SectionDocs            = contractview.SectionDocs
	SectionSBOM            = contractview.SectionSBOM
	SectionRuntime         = contractview.SectionRuntime
	SectionValidation      = contractview.SectionValidation
	SectionObservedRuntime = contractview.SectionObservedRuntime
	SectionRuntimeDiff     = contractview.SectionRuntimeDiff
	SectionResources       = contractview.SectionResources
	SectionPorts           = contractview.SectionPorts
	SectionEndpoints       = contractview.SectionEndpoints
	SectionConditions      = contractview.SectionConditions
)

// ── Constructors ─────────────────────────────────────────────────────
//
// Go has no function alias, and these seven were plain funcs in released v3.
// Re-exporting them as `var F = contractview.F` would keep call sites compiling
// while quietly changing each one from a function into a reassignable package
// variable — an API change in a published major, and seven swappable seams in
// production code. They stay functions, and forward.

// ServiceFromContract builds a Service summary from a parsed contract.
//
// Deprecated: use [contractview.ServiceFromContract]. Removed at v4.
func ServiceFromContract(c *contract.Contract, source string) Service {
	return contractview.ServiceFromContract(c, source)
}

// ServiceDetailsFromBundle builds full ServiceDetails from a contract bundle.
//
// Deprecated: use [contractview.ServiceDetailsFromBundle]. Removed at v4.
func ServiceDetailsFromBundle(bundle *contract.Bundle, source string) *ServiceDetails {
	return contractview.ServiceDetailsFromBundle(bundle, source)
}

// GlobalGraphFromResult builds the flat D3 GlobalGraph from a resolved dependency graph.
//
// Deprecated: use [contractview.GlobalGraphFromResult]. Removed at v4.
func GlobalGraphFromResult(gr *depgraph.Result, root *ServiceDetails) *GlobalGraph {
	return contractview.GlobalGraphFromResult(gr, root)
}

// NormalizeContractStatus maps any non-standard status to a canonical one.
//
// Deprecated: use [contractview.NormalizeContractStatus]. Removed at v4.
func NormalizeContractStatus(s ContractStatus) ContractStatus {
	return contractview.NormalizeContractStatus(s)
}

// ComputeCompliance computes the compliance status and score from contract status and conditions.
//
// Deprecated: use [contractview.ComputeCompliance]. Removed at v4.
func ComputeCompliance(cs ContractStatus, conditions []Condition) *ComplianceInfo {
	return contractview.ComputeCompliance(cs, conditions)
}

// LookupValidation returns the catalog entry for a condition type.
//
// Deprecated: use [contractview.LookupValidation]. Removed at v4.
func LookupValidation(conditionType string) ValidationCatalogEntry {
	return contractview.LookupValidation(conditionType)
}

// ApplyLock maps a parsed lock onto a ServiceDetails.
//
// Deprecated: use [contractview.ApplyLock]. Removed at v4.
func ApplyLock(svc *ServiceDetails, l *lock.Lock) {
	contractview.ApplyLock(svc, l)
}
