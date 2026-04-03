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
	Metadata       map[string]interface{} `yaml:"metadata,omitempty" json:"metadata,omitempty"`
}

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
