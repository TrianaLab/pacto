package contract

// Contract is the root aggregate — the parsed in-memory representation of a pacto.yaml.
type Contract struct {
	PactoVersion  string                 `yaml:"pactoVersion" json:"pactoVersion"`
	Service       ServiceIdentity        `yaml:"service" json:"service"`
	Interfaces    []Interface            `yaml:"interfaces,omitempty" json:"interfaces,omitempty"`
	Configuration *Configuration         `yaml:"configuration,omitempty" json:"configuration,omitempty"`
	Policies      []PolicySource         `yaml:"policies,omitempty" json:"policies,omitempty"`
	Dependencies  []Dependency           `yaml:"dependencies,omitempty" json:"dependencies,omitempty"`
	Runtime       *Runtime               `yaml:"runtime,omitempty" json:"runtime,omitempty"`
	Scaling       *Scaling               `yaml:"scaling,omitempty" json:"scaling,omitempty"`
	Metadata      map[string]interface{} `yaml:"metadata,omitempty" json:"metadata,omitempty"`
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

// Configuration holds the configuration section of a contract.
// It supports two forms:
//   - Legacy: top-level Schema/Ref/Values fields (single configuration source)
//   - New: a Configs array of named configuration sources (multiple independent scopes)
//
// When both forms are present, Configs takes precedence. Internally, use
// EffectiveConfigs() to normalize both forms into a uniform slice.
type Configuration struct {
	Schema  string                 `yaml:"schema,omitempty" json:"schema,omitempty"`
	Ref     string                 `yaml:"ref,omitempty" json:"ref,omitempty"`
	Values  map[string]interface{} `yaml:"values,omitempty" json:"values,omitempty"`
	Configs []NamedConfigSource    `yaml:"configs,omitempty" json:"configs,omitempty"`
}

// NamedConfigSource declares a named configuration source within the
// configuration.configs[] array. Name is required and each entry is an
// independent named scope with no implicit merge semantics.
type NamedConfigSource struct {
	Name   string                 `yaml:"name" json:"name"`
	Schema string                 `yaml:"schema,omitempty" json:"schema,omitempty"`
	Ref    string                 `yaml:"ref,omitempty" json:"ref,omitempty"`
	Values map[string]interface{} `yaml:"values,omitempty" json:"values,omitempty"`
}

// EffectiveConfigSource is the normalized internal representation of a single
// configuration source, regardless of whether it came from the legacy form
// or the new configuration.configs[] form.
type EffectiveConfigSource struct {
	Name   string
	Schema string
	Ref    string
	Values map[string]interface{}
}

// EffectiveConfigs normalizes both configuration forms into a uniform slice.
// If Configs is non-empty, each named entry is returned directly.
// Otherwise, if legacy fields (Schema or Ref) are set, a single entry is returned.
// Returns nil if the Configuration is nil or has no effective sources.
func (c *Configuration) EffectiveConfigs() []EffectiveConfigSource {
	if c == nil {
		return nil
	}
	if len(c.Configs) > 0 {
		result := make([]EffectiveConfigSource, len(c.Configs))
		for i, cfg := range c.Configs {
			result[i] = EffectiveConfigSource(cfg)
		}
		return result
	}
	if c.Schema != "" || c.Ref != "" || len(c.Values) > 0 {
		return []EffectiveConfigSource{{
			Schema: c.Schema,
			Ref:    c.Ref,
			Values: c.Values,
		}}
	}
	return nil
}

// PolicySource declares a policy constraint source.
// Each entry provides either a local JSON Schema file or a reference to an
// external contract. When resolving a ref, if the referenced contract declares
// its own policies[] entries, those schemas are used directly (supporting custom
// paths and multiple schemas). Otherwise, the fixed path policy/schema.json is
// used as a backward-compatible fallback.
// A policy schema validates the contract itself, enabling platform teams to
// enforce organizational standards. Schema and Ref are mutually exclusive.
type PolicySource struct {
	Schema string `yaml:"schema,omitempty" json:"schema,omitempty"`
	Ref    string `yaml:"ref,omitempty" json:"ref,omitempty"`
}

// Dependency represents a dependency on another service.
type Dependency struct {
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
