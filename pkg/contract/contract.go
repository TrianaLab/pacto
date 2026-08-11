// Package contract defines the core data model for Pacto v2.0 service contracts:
// the in-memory representation of a pacto.yaml and the types for service identity,
// interfaces, dependencies, configurations, policies, state, capabilities, and
// readiness. It also provides YAML parsing along with OCI-reference and semver-range
// helpers shared by the CLI, dashboard, and operator.
package contract

// Contract is the root aggregate — the parsed in-memory representation of a pacto.yaml.
type Contract struct {
	PactoVersion   string          `yaml:"pactoVersion" json:"pactoVersion"`
	Service        Service         `yaml:"service" json:"service"`
	Interfaces     []Interface     `yaml:"interfaces,omitempty" json:"interfaces,omitempty"`
	Configurations []Configuration `yaml:"configurations,omitempty" json:"configurations,omitempty"`
	Dependencies   []Dependency    `yaml:"dependencies,omitempty" json:"dependencies,omitempty"`
	State          *State          `yaml:"state,omitempty" json:"state,omitempty"`
	Workload       string          `yaml:"workload,omitempty" json:"workload,omitempty"`
	Capabilities   []Capability    `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
	Policies       []Policy        `yaml:"policies,omitempty" json:"policies,omitempty"`
	Readiness      *Readiness      `yaml:"readiness,omitempty" json:"readiness,omitempty"`
	Metadata       map[string]any  `yaml:"metadata,omitempty" json:"metadata,omitempty"`
	Extensions     map[string]any  `yaml:"extensions,omitempty" json:"extensions,omitempty"`
}

// Readiness declares operational readiness evidence for the service.
// It is an optional, provider-neutral section. Each claim declares its completion
// status (done/partial/not-done/deferred) and points at evidence (a dashboard,
// runbook, ticket, report, etc.) without Pacto verifying the target content. The
// whole assessment expires on a single date; once expired, every in-scope claim
// earns zero weight.
type Readiness struct {
	// MinScore is the gate: the derived readiness score must be >= this value for
	// the contract to be considered ready. It is on the same 0..100 scale as the
	// score. Omitted means 100 (every weighted claim must be done); set lower to
	// tolerate partial completion. Enforced by `pacto validate --readiness` and
	// the operator.
	MinScore *int `yaml:"minScore,omitempty" json:"minScore,omitempty"`
	// Expires is the single assessment-level freshness boundary (YYYY-MM-DD).
	// After this date every in-scope claim earns zero weight and the gate fails.
	Expires string `yaml:"expires" json:"expires"`
	// PartialCredit is the fraction of a claim's weight earned when its status is
	// "partial". Omitted means 0.5.
	PartialCredit *float64 `yaml:"partialCredit,omitempty" json:"partialCredit,omitempty"`
	// History records the revision log of the readiness assessment.
	History []ReadinessRevision `yaml:"history,omitempty" json:"history,omitempty"`
	Claims  []ReadinessClaim    `yaml:"claims,omitempty" json:"claims,omitempty"`
}

// ReadinessRevision is a single entry in the readiness revision history.
type ReadinessRevision struct {
	Date        string `yaml:"date" json:"date"`
	Version     string `yaml:"version" json:"version"`
	Author      string `yaml:"author" json:"author"`
	Description string `yaml:"description" json:"description"`
}

// ReadinessClaim is a single declared readiness requirement.
// ID identifies the organizational requirement (e.g. dashboard, runbook),
// Type classifies the evidence pointer, Category groups the claim into a
// software domain, Status declares its completion state, Evidence is the
// pointer itself, and Weight contributes to the readiness score. The service
// owner is declared at the contract level, so readiness claims deliberately
// carry no per-claim owner. A claim is a declared, unverified attestation.
type ReadinessClaim struct {
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

// Service holds service identification fields.
type Service struct {
	Name    string `yaml:"name" json:"name"`
	Version string `yaml:"version" json:"version"`
	Owner   Owner  `yaml:"owner,omitempty" json:"owner,omitempty"`
}

// Interface describes a service interface declaration.
type Interface struct {
	Name       string `yaml:"name" json:"name"`
	Type       string `yaml:"type" json:"type"`
	Ref        string `yaml:"ref" json:"ref"`
	Visibility string `yaml:"visibility,omitempty" json:"visibility,omitempty"`
}

// InterfaceType constants.
const (
	InterfaceTypeOpenAPI  = "openapi"
	InterfaceTypeAsyncAPI = "asyncapi"
	InterfaceTypeGRPC     = "grpc"
)

// Visibility constants.
const (
	VisibilityPublic   = "public"
	VisibilityInternal = "internal"
)

// Configuration declares a named configuration scope.
// Each entry is an independent scope with no implicit merge semantics.
// Name is required and must be unique within the configurations array.
// Exactly one of Schema or Ref must be set. Values is only allowed with Schema.
type Configuration struct {
	Name     string         `yaml:"name" json:"name"`
	Schema   string         `yaml:"schema,omitempty" json:"schema,omitempty"`
	Ref      string         `yaml:"ref,omitempty" json:"ref,omitempty"`
	Values   map[string]any `yaml:"values,omitempty" json:"values,omitempty"`
	Required bool           `yaml:"required" json:"required"` // MANDATORY in schema; true = confirmed absence is a violation
}

// Policy declares a named policy constraint source.
// Each entry provides either a local JSON Schema file or a reference to an
// external contract. When resolving a ref, if the referenced contract declares
// its own policies[] entries, those schemas are used directly (supporting custom
// paths and multiple schemas). Otherwise, the fixed path policy/schema.json is
// used as a backward-compatible fallback.
// A policy schema validates the contract itself, enabling platform teams to
// enforce organizational standards. Schema and Ref are mutually exclusive.
// Name is required and must be unique within the policies array.
type Policy struct {
	Name   string `yaml:"name" json:"name"`
	Schema string `yaml:"schema,omitempty" json:"schema,omitempty"`
	Ref    string `yaml:"ref,omitempty" json:"ref,omitempty"`
	Target string `yaml:"target,omitempty" json:"target,omitempty"`
}

// Dependency represents a named dependency on another service.
// Name is required and must be unique within the dependencies array.
type Dependency struct {
	Name          string `yaml:"name" json:"name"`
	Ref           string `yaml:"ref" json:"ref"`
	Required      bool   `yaml:"required" json:"required"`
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
	for i := range c.Policies {
		if c.Policies[i].Ref != "" {
			out = append(out, ReferenceRef{Kind: ReferenceKindPolicy, Name: c.Policies[i].Name, Ref: c.Policies[i].Ref})
		}
	}
	for i := range c.Configurations {
		if c.Configurations[i].Ref != "" {
			out = append(out, ReferenceRef{Kind: ReferenceKindConfig, Name: c.Configurations[i].Name, Ref: c.Configurations[i].Ref})
		}
	}
	return out
}

// Duplicate is one configuration or policy name a contract declares more than
// once: the kind it was declared under, the name, the index of the repeat and
// the index of the declaration it repeats.
type Duplicate struct {
	Kind  string
	Name  string
	Index int
	First int
}

// DuplicateDeclarations returns every configuration or policy name this contract
// declares more than once, policies first and then configurations, in
// declaration order. Uniqueness is per kind: a configuration and a policy may
// share a name, because they are looked up under different kinds.
//
// This is the single statement of the rule, and it is stated over the DECLARED
// entries rather than over ReferenceRefs: a name declared once with an inline
// schema and once with a ref is still declared twice, and the ref half of that
// pair is the only half a reference walk can see.
//
// Two callers read it. Canonical validation reports the duplicates as
// DUPLICATE_POLICY_NAME / DUPLICATE_CONFIGURATION_NAME; `pacto lock` refuses to
// build a closure over such a contract, because a lock entry is identified by
// (declaring contract, kind, name) and two declarations of one name are
// indistinguishable in it.
func (c *Contract) DuplicateDeclarations() []Duplicate {
	var out []Duplicate
	seen := make(map[string]int, len(c.Policies))
	for i := range c.Policies {
		if prev, dup := seen[c.Policies[i].Name]; dup {
			out = append(out, Duplicate{Kind: ReferenceKindPolicy, Name: c.Policies[i].Name, Index: i, First: prev})
		}
		seen[c.Policies[i].Name] = i
	}
	clear(seen)
	for i := range c.Configurations {
		if prev, dup := seen[c.Configurations[i].Name]; dup {
			out = append(out, Duplicate{Kind: ReferenceKindConfig, Name: c.Configurations[i].Name, Index: i, First: prev})
		}
		seen[c.Configurations[i].Name] = i
	}
	return out
}

// Workload type constants.
const (
	WorkloadService   = "service"
	WorkloadJob       = "job"
	WorkloadScheduled = "scheduled"
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

// Capability describes a service capability. Standard types (health, metrics) may declare a binding to a
// declared interface; extension requires a namespaced ref (no binding).
type Capability struct {
	Type    string             `yaml:"type" json:"type"`
	Ref     string             `yaml:"ref,omitempty" json:"ref,omitempty"`         // extension only
	Binding *CapabilityBinding `yaml:"binding,omitempty" json:"binding,omitempty"` // standard types only
}

// CapabilityBinding binds a standard capability endpoint (health/metrics) to a declared interface for
// probing. binding.type is a CLOSED set — only "http" is supported (an unknown transport fails structural
// validation). binding.interface names the declared interface that provides the ADDRESS/PORT anchor for the
// probe — the platform-neutral counterpart of the Kubernetes CR's spec.target.interfaceBindings[]; it does
// NOT claim the capability path is part of that interface's OpenAPI/AsyncAPI/gRPC specification. binding.path
// is relative to the resolved endpoint.
type CapabilityBinding struct {
	Type      string `yaml:"type" json:"type"`                     // "http" (the only supported transport)
	Interface string `yaml:"interface" json:"interface"`           // declared interface providing the address/port anchor
	Path      string `yaml:"path,omitempty" json:"path,omitempty"` // relative to the resolved endpoint; must start with "/" (INV-6)
}

// CapabilityBindingType constants.
const (
	CapabilityBindingHTTP = "http"
)

// CapabilityType constants.
const (
	CapabilityHealth    = "health"
	CapabilityMetrics   = "metrics"
	CapabilityExtension = "extension"
)

// AssertionKey returns the canonical assertion identity for a capability: the namespaced ref for an
// extension, otherwise the standard type. This is the single identity rule used everywhere — Evidence
// Subject.Name, finding subjects, coverage counting, uniqueness validation and (in S6) collector /
// evaluator registration — so two distinct extensions never collapse to the same "extension" identity.
func (cap Capability) AssertionKey() string {
	if cap.Type == CapabilityExtension {
		return cap.Ref
	}
	return cap.Type
}

// PolicyTarget constants.
const (
	PolicyTargetContract = "contract"
)
