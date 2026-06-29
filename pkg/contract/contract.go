// Package contract defines the core data model for Pacto service contracts: the
// in-memory representation of a pacto.yaml and the types for service identity,
// interfaces, dependencies, configurations, policies, runtime semantics,
// scaling, and readiness. It also provides YAML parsing along with OCI-reference
// and semver-range helpers shared by the CLI, dashboard, and operator.
package contract

// Contract is the root aggregate — the parsed in-memory representation of a pacto.yaml.
type Contract struct {
	PactoVersion   string                 `yaml:"pactoVersion" json:"pactoVersion"`
	Service        ServiceIdentity        `yaml:"service" json:"service"`
	Interfaces     []Interface            `yaml:"interfaces,omitempty" json:"interfaces,omitempty"`
	Configurations []ConfigurationSource  `yaml:"configurations,omitempty" json:"configurations,omitempty"`
	Policies       []PolicySource         `yaml:"policies,omitempty" json:"policies,omitempty"`
	Dependencies   []Dependency           `yaml:"dependencies,omitempty" json:"dependencies,omitempty"`
	Runtime        *Runtime               `yaml:"runtime,omitempty" json:"runtime,omitempty"`
	Scaling        *Scaling               `yaml:"scaling,omitempty" json:"scaling,omitempty"`
	Readiness      *Readiness             `yaml:"readiness,omitempty" json:"readiness,omitempty"`
	Metadata       map[string]interface{} `yaml:"metadata,omitempty" json:"metadata,omitempty"`
}

// Readiness declares operational readiness evidence for the service.
// It is an optional, provider-neutral section introduced in pactoVersion 1.2.
// Each check declares its completion status (done/partial/not-done/deferred)
// and points at evidence (a dashboard, runbook, ticket, report, etc.) without
// Pacto verifying the target content. The whole assessment expires on a single
// date; once expired, every in-scope check earns zero weight.
type Readiness struct {
	// MinScore is the gate: the derived readiness score must be >= this value for
	// the contract to be considered ready. It is on the same 0..100 scale as the
	// score. Omitted means 100 (every weighted check must be done); set lower to
	// tolerate partial completion. Enforced by `pacto validate --readiness` and
	// the operator.
	MinScore *int `yaml:"minScore,omitempty" json:"minScore,omitempty"`
	// Expires is the single assessment-level freshness boundary (YYYY-MM-DD).
	// After this date every in-scope check earns zero weight and the gate fails.
	Expires string `yaml:"expires" json:"expires"`
	// PartialCredit is the fraction of a check's weight earned when its status is
	// "partial". Omitted means 0.5.
	PartialCredit *float64 `yaml:"partialCredit,omitempty" json:"partialCredit,omitempty"`
	// History records the revision log of the readiness assessment.
	History []ReadinessRevision `yaml:"history,omitempty" json:"history,omitempty"`
	Checks  []ReadinessCheck    `yaml:"checks,omitempty" json:"checks,omitempty"`
}

// ReadinessRevision is a single entry in the readiness revision history.
type ReadinessRevision struct {
	Date        string `yaml:"date" json:"date"`
	Version     string `yaml:"version" json:"version"`
	Author      string `yaml:"author" json:"author"`
	Description string `yaml:"description" json:"description"`
}

// ReadinessCheck is a single declared readiness requirement.
// ID identifies the organizational requirement (e.g. dashboard, runbook),
// Type classifies the evidence pointer, Category groups the check into a
// software domain, Status declares its completion state, Evidence is the
// pointer itself, and Weight contributes to the readiness score. The service
// owner is declared at the contract level, so readiness checks deliberately
// carry no per-check owner.
type ReadinessCheck struct {
	ID          string `yaml:"id" json:"id"`
	Type        string `yaml:"type" json:"type"`
	Category    string `yaml:"category,omitempty" json:"category,omitempty"`
	Status      string `yaml:"status" json:"status"`
	Evidence    string `yaml:"evidence" json:"evidence"`
	Weight      int    `yaml:"weight" json:"weight"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// Declared per-check completion status.
const (
	StatusDone     = "done"
	StatusPartial  = "partial"
	StatusNotDone  = "not-done"
	StatusDeferred = "deferred"
)

// Readiness check software-domain categories.
const (
	CategoryArchitecture     = "architecture"
	CategoryTesting          = "testing"
	CategoryCodeQuality      = "code-quality"
	CategoryObservability    = "observability"
	CategorySecurity         = "security"
	CategoryDocumentation    = "documentation"
	CategoryInfrastructure   = "infrastructure"
	CategoryCICD             = "ci-cd"
	CategoryDeployment       = "deployment"
	CategoryResilience       = "resilience"
	CategoryBackupRecovery   = "backup-recovery"
	CategoryIncidentResponse = "incident-response"
	CategoryCompliance       = "compliance"
	CategoryOther            = "other"
)

// Evidence type constants for ReadinessCheck.Type. These classify the kind of
// evidence pointer, not the organizational requirement (which is the ID).
// "other" exists for forward compatibility.
const (
	EvidenceTypeURL        = "url"
	EvidenceTypeDocument   = "document"
	EvidenceTypeTicket     = "ticket"
	EvidenceTypeReport     = "report"
	EvidenceTypeArtifact   = "artifact"
	EvidenceTypeIdentifier = "identifier"
	EvidenceTypeOther      = "other"
)

// ServiceIdentity holds service identification fields.
type ServiceIdentity struct {
	Name    string `yaml:"name" json:"name"`
	Version string `yaml:"version" json:"version"`
	Owner   Owner  `yaml:"owner,omitempty" json:"owner,omitempty"`
	Image   *Image `yaml:"image,omitempty" json:"image,omitempty"`
	Chart   *Chart `yaml:"chart,omitempty" json:"chart,omitempty"`
}

// Image describes the container image for the service.
type Image struct {
	Ref     string `yaml:"ref" json:"ref"`
	Private bool   `yaml:"private,omitempty" json:"private,omitempty"`
}

// Chart describes the Helm chart for the service.
type Chart struct {
	Ref     string `yaml:"ref" json:"ref"`
	Version string `yaml:"version" json:"version"`
}

// Interface describes a service interface declaration.
type Interface struct {
	Name       string `yaml:"name" json:"name"`
	Type       string `yaml:"type" json:"type"`
	Port       *int   `yaml:"port,omitempty" json:"port,omitempty"`
	Visibility string `yaml:"visibility,omitempty" json:"visibility,omitempty"`
	Contract   string `yaml:"contract,omitempty" json:"contract,omitempty"`
}

// InterfaceType constants.
const (
	InterfaceTypeHTTP  = "http"
	InterfaceTypeGRPC  = "grpc"
	InterfaceTypeEvent = "event"
)

// Visibility constants.
const (
	VisibilityPublic   = "public"
	VisibilityInternal = "internal"
)

// ConfigurationSource declares a named configuration scope.
// Each entry is an independent scope with no implicit merge semantics.
// Name is required and must be unique within the configurations array.
// Exactly one of Schema or Ref must be set. Values is only allowed with Schema.
type ConfigurationSource struct {
	Name   string                 `yaml:"name" json:"name"`
	Schema string                 `yaml:"schema,omitempty" json:"schema,omitempty"`
	Ref    string                 `yaml:"ref,omitempty" json:"ref,omitempty"`
	Values map[string]interface{} `yaml:"values,omitempty" json:"values,omitempty"`
}

// PolicySource declares a named policy constraint source.
// Each entry provides either a local JSON Schema file or a reference to an
// external contract. When resolving a ref, if the referenced contract declares
// its own policies[] entries, those schemas are used directly (supporting custom
// paths and multiple schemas). Otherwise, the fixed path policy/schema.json is
// used as a backward-compatible fallback.
// A policy schema validates the contract itself, enabling platform teams to
// enforce organizational standards. Schema and Ref are mutually exclusive.
// Name is required and must be unique within the policies array.
type PolicySource struct {
	Name   string `yaml:"name" json:"name"`
	Schema string `yaml:"schema,omitempty" json:"schema,omitempty"`
	Ref    string `yaml:"ref,omitempty" json:"ref,omitempty"`
}

// Dependency represents a named dependency on another service.
// Name is required and must be unique within the dependencies array.
type Dependency struct {
	Name          string `yaml:"name" json:"name"`
	Ref           string `yaml:"ref" json:"ref"`
	Required      bool   `yaml:"required,omitempty" json:"required,omitempty"`
	Compatibility string `yaml:"compatibility" json:"compatibility"`
}

// Reference kinds for ReferenceRef.Kind.
const (
	ReferenceKindPolicy = "policy"
	ReferenceKindConfig = "config"
)

// ReferenceRef is one declared config/policy reference: its kind ("policy" or
// "config"), declared name and the raw ref string from the declaring contract.
type ReferenceRef struct {
	Kind string
	Name string
	Ref  string
}

// ReferenceRefs returns the contract's declared config/policy references in a
// stable order (policies first, then configurations), skipping entries that
// declare an inline schema rather than a ref (empty Ref). Both the lockfile
// builder and the demo lock generator resolve exactly this set.
func (c *Contract) ReferenceRefs() []ReferenceRef {
	var out []ReferenceRef
	for _, p := range c.Policies {
		if p.Ref != "" {
			out = append(out, ReferenceRef{Kind: ReferenceKindPolicy, Name: p.Name, Ref: p.Ref})
		}
	}
	for _, cfg := range c.Configurations {
		if cfg.Ref != "" {
			out = append(out, ReferenceRef{Kind: ReferenceKindConfig, Name: cfg.Name, Ref: cfg.Ref})
		}
	}
	return out
}

// Runtime describes how the service behaves at runtime.
type Runtime struct {
	Workload  string     `yaml:"workload" json:"workload"`
	State     State      `yaml:"state" json:"state"`
	Lifecycle *Lifecycle `yaml:"lifecycle,omitempty" json:"lifecycle,omitempty"`
	Health    *Health    `yaml:"health,omitempty" json:"health,omitempty"`
	Metrics   *Metrics   `yaml:"metrics,omitempty" json:"metrics,omitempty"`
}

// WorkloadType constants.
const (
	WorkloadTypeService   = "service"
	WorkloadTypeJob       = "job"
	WorkloadTypeScheduled = "scheduled"
)

// State describes the state semantics of the service.
type State struct {
	Type            string      `yaml:"type" json:"type"`
	Persistence     Persistence `yaml:"persistence" json:"persistence"`
	DataCriticality string      `yaml:"dataCriticality" json:"dataCriticality"`
}

// StateType constants.
const (
	StateStateless = "stateless"
	StateStateful  = "stateful"
	StateHybrid    = "hybrid"
)

// DataCriticality constants.
const (
	DataCriticalityLow    = "low"
	DataCriticalityMedium = "medium"
	DataCriticalityHigh   = "high"
)

// Persistence represents the persistence requirements.
type Persistence struct {
	Scope      string `yaml:"scope" json:"scope"`
	Durability string `yaml:"durability" json:"durability"`
}

// Scope constants.
const (
	ScopeLocal  = "local"
	ScopeShared = "shared"
)

// Durability constants.
const (
	DurabilityEphemeral  = "ephemeral"
	DurabilityPersistent = "persistent"
)

// Lifecycle describes lifecycle behavior.
type Lifecycle struct {
	UpgradeStrategy         string `yaml:"upgradeStrategy,omitempty" json:"upgradeStrategy,omitempty"`
	GracefulShutdownSeconds *int   `yaml:"gracefulShutdownSeconds,omitempty" json:"gracefulShutdownSeconds,omitempty"`
}

// UpgradeStrategy constants.
const (
	UpgradeStrategyRolling  = "rolling"
	UpgradeStrategyRecreate = "recreate"
	UpgradeStrategyOrdered  = "ordered"
)

// Health describes the health check configuration.
type Health struct {
	Interface           string `yaml:"interface" json:"interface"`
	Path                string `yaml:"path,omitempty" json:"path,omitempty"`
	InitialDelaySeconds *int   `yaml:"initialDelaySeconds,omitempty" json:"initialDelaySeconds,omitempty"`
}

// Metrics describes the metrics endpoint configuration.
type Metrics struct {
	Interface string `yaml:"interface" json:"interface"`
	Path      string `yaml:"path,omitempty" json:"path,omitempty"`
}

// Scaling describes scaling parameters.
// Either Replicas (exact count) or Min/Max (range) is set.
type Scaling struct {
	Replicas *int `yaml:"replicas,omitempty" json:"replicas,omitempty"`
	Min      int  `yaml:"min,omitempty" json:"min,omitempty"`
	Max      int  `yaml:"max,omitempty" json:"max,omitempty"`
}
