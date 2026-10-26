// Package contract defines the core data model for Pacto v2.0 service contracts:
// the in-memory representation of a pacto.yaml and the types for service identity,
// interfaces, dependencies, configurations, policies, state, capabilities, and
// readiness. It also provides YAML parsing along with OCI-reference and semver-range
// helpers shared by the CLI, dashboard, and operator.
package contract

import (
	"net/url"
	"strings"
)

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
	Publish        *Publish        `yaml:"publish,omitempty" json:"publish,omitempty"`
	Checks         []Check         `yaml:"checks,omitempty" json:"checks,omitempty"`
	Metadata       map[string]any  `yaml:"metadata,omitempty" json:"metadata,omitempty"`
	Extensions     map[string]any  `yaml:"extensions,omitempty" json:"extensions,omitempty"`
}

// Check is one test or eval suite the version ships (pactoVersion 2.1).
// Pacto never reads the file: it packs it in the bundle, records its sha256
// and hands file and digest to the runner a delivery gate names, taking only
// a verdict about that exact digest.
type Check struct {
	Name string `yaml:"name" json:"name"`
	// File is the suite's path in the bundle.
	File string `yaml:"file" json:"file"`
}

// Publish declares what the service publishes, where and how each artifact is
// built (pactoVersion 2.1). `pacto publish` reads it; every tag is the
// service version.
type Publish struct {
	// Bundle is the repository the contract bundle goes to (oci://, no tag).
	Bundle string `yaml:"bundle" json:"bundle"`
	// Deploy names the manifests artifact Flux applies. Pacto annotates it with
	// the contract and tags it last.
	Deploy    string            `yaml:"deploy,omitempty" json:"deploy,omitempty"`
	Artifacts []PublishArtifact `yaml:"artifacts,omitempty" json:"artifacts,omitempty"`
	Sign      *PublishSign      `yaml:"sign,omitempty" json:"sign,omitempty"`
}

// PublishArtifact is one artifact a service publishes.
type PublishArtifact struct {
	Name  string       `yaml:"name" json:"name"`
	Type  string       `yaml:"type" json:"type"`
	Ref   string       `yaml:"ref" json:"ref"`
	Build PublishBuild `yaml:"build" json:"build"`
	// SBOM is the SPDX (.spdx.json) or CycloneDX (.cdx.json) file the build
	// writes, relative to the contract directory. Pacto attaches it to the
	// image and signs it; it never makes one.
	SBOM string `yaml:"sbom,omitempty" json:"sbom,omitempty"`
}

// Publish artifact types.
const (
	ArtifactImage     = "image"
	ArtifactChart     = "chart"
	ArtifactManifests = "manifests"
)

// PublishBuild is how an artifact is built: exactly one of the two.
type PublishBuild struct {
	Dockerfile *DockerfileBuild `yaml:"dockerfile,omitempty" json:"dockerfile,omitempty"`
	// Command writes the artifact to $PACTO_OUT.
	Command []string `yaml:"command,omitempty" json:"command,omitempty"`
}

// DockerfileBuild builds an image with docker buildx.
type DockerfileBuild struct {
	Context   string   `yaml:"context" json:"context"`
	File      string   `yaml:"file,omitempty" json:"file,omitempty"`
	Platforms []string `yaml:"platforms,omitempty" json:"platforms,omitempty"`
}

// PublishSign is how every published digest is signed: exactly one of
// Keyless, Key and KMS. Keyless names the Identity and Issuer its certificate
// carries, so Pacto can tell its own signature from anyone else's.
type PublishSign struct {
	Keyless bool `yaml:"keyless,omitempty" json:"keyless,omitempty"`
	// Identity is the certificate identity keyless signing produces.
	Identity string `yaml:"identity,omitempty" json:"identity,omitempty"`
	// Issuer is the OIDC issuer of that identity.
	Issuer string `yaml:"issuer,omitempty" json:"issuer,omitempty"`
	Key    string `yaml:"key,omitempty" json:"key,omitempty"`
	KMS    string `yaml:"kms,omitempty" json:"kms,omitempty"`
	// TransparencyLog is nil for the default (upload).
	TransparencyLog *bool `yaml:"transparencyLog,omitempty" json:"transparencyLog,omitempty"`
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
	// InterfaceTypeMCP is the tools a service exposes over the Model Context
	// Protocol; its ref is a tools/list result ({"tools": [...]}).
	InterfaceTypeMCP = "mcp"
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
//
// External (pactoVersion 2.1) marks a dependency this Pacto system does not
// deliver: Pacto does not expect to see it running and never moves it. With a
// Ref it is compared by Compatibility like any other; without one it carries
// a free-text Version instead, which nothing can check, so anything relying
// on it is Unknown.
type Dependency struct {
	Name          string `yaml:"name" json:"name"`
	Ref           string `yaml:"ref,omitempty" json:"ref,omitempty"`
	Required      bool   `yaml:"required" json:"required"`
	Compatibility string `yaml:"compatibility,omitempty" json:"compatibility,omitempty"`
	External      bool   `yaml:"external,omitempty" json:"external,omitempty"`
	Version       string `yaml:"version,omitempty" json:"version,omitempty"`
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

// ValidCapabilityPath enforces INV-6 (SSRF-safe): an application path must start with a single "/" and
// carry no scheme, authority/host, userinfo, fragment or opaque component. The validation crossfield check
// built on it is AUTHORITATIVE for INV-6; the JSON-schema pattern is defense-in-depth. The collector additionally builds
// the probe URL with net/url using the host from the trusted binding, never from this path.
func ValidCapabilityPath(path string) bool {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return false
	}
	u, err := url.Parse(path)
	if err != nil {
		return false
	}
	// u.Path is decoded, so a leading "//" also catches encoded-authority forms
	// like /%2Fevil.example and /%2f%2fevil.example (INV-6 MUST-reject set).
	if strings.HasPrefix(u.Path, "//") {
		return false
	}
	return u.Scheme == "" && u.Host == "" && u.User == nil && u.Fragment == "" && u.Opaque == ""
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
