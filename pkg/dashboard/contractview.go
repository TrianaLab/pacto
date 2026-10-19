package dashboard

import "github.com/trianalab/pacto/v4/pkg/contractview"

// The contract-view half of this package — the DTOs that describe ONE service's
// contract, and the pure functions that build them from a bundle — now lives in
// [github.com/trianalab/pacto/v4/pkg/contractview], a leaf with no HTTP, no Huma
// and no fleet in it. That is what lets `pacto doc` render an offline page
// without linking a server.
//
// These are aliases, not copies: the DTO names appear in the published OpenAPI
// document and in the generated TypeScript SDK. Alias identity also keeps the two
// spellings interchangeable at every call site, so nothing has to convert.
//
// New code should import pkg/contractview directly.

// ── DTOs ─────────────────────────────────────────────────────────────

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
type Version = contractview.Version
type Ref = contractview.Ref
type DiffResult = contractview.DiffResult
type DiffChange = contractview.DiffChange
type AggregatedService = contractview.AggregatedService
type ServiceSourceData = contractview.ServiceSourceData
type DependencyGraph = contractview.DependencyGraph
type GraphNode = contractview.GraphNode
type GraphEdge = contractview.GraphEdge
type Insight = contractview.Insight
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
