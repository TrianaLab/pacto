package diff

import "regexp"

var indexRe = regexp.MustCompile(`\[[^\]]*\]`)

// classificationKey maps a field path and change type to a classification.
type classificationKey struct {
	Path string
	Type ChangeType
}

// rules is the deterministic lookup table for change classification.
// Each entry maps (field path, change type) → classification.
var rules = map[classificationKey]Classification{
	// Schema version
	{"pactoVersion", Modified}: NonBreaking,
	{"pactoVersion", Added}:    NonBreaking,
	{"pactoVersion", Removed}:  NonBreaking,

	// Service identity (no image/chart in v2)
	{"service.name", Modified}:           Breaking,
	{"service.version", Modified}:        NonBreaking,
	{"service.owner.team", Modified}:     NonBreaking,
	{"service.owner.team", Added}:        NonBreaking,
	{"service.owner.team", Removed}:      NonBreaking,
	{"service.owner.dri", Modified}:      NonBreaking,
	{"service.owner.dri", Added}:         NonBreaking,
	{"service.owner.dri", Removed}:       NonBreaking,
	{"service.owner.contacts", Modified}: NonBreaking,
	{"service.owner.contacts", Added}:    NonBreaking,
	{"service.owner.contacts", Removed}:  NonBreaking,

	// Workload (top-level in v2)
	{"workload", Modified}: Breaking,
	{"workload", Added}:    NonBreaking,
	{"workload", Removed}:  NonBreaking,

	// State (top-level in v2)
	{"state.type", Modified}:                   Breaking,
	{"state.persistence.scope", Modified}:      Breaking,
	{"state.persistence.durability", Modified}: Breaking,
	{"state.dataCriticality", Modified}:        PotentialBreaking,
	{"state.dataCriticality", Added}:           NonBreaking,
	{"state.dataCriticality", Removed}:         NonBreaking,

	// Capabilities (v2: health/metrics/extension)
	{"capabilities", Added}:   NonBreaking,
	{"capabilities", Removed}: PotentialBreaking,

	// Readiness — operational maturity, never consumer-facing (claims not checks)
	{"readiness", Added}:                  NonBreaking,
	{"readiness", Removed}:                NonBreaking,
	{"readiness.minScore", Modified}:      NonBreaking,
	{"readiness.minScore", Added}:         NonBreaking,
	{"readiness.minScore", Removed}:       NonBreaking,
	{"readiness.expires", Modified}:       NonBreaking,
	{"readiness.expires", Added}:          NonBreaking,
	{"readiness.expires", Removed}:        NonBreaking,
	{"readiness.partialCredit", Modified}: NonBreaking,
	{"readiness.partialCredit", Added}:    NonBreaking,
	{"readiness.partialCredit", Removed}:  NonBreaking,
	{"readiness.claims", Added}:           NonBreaking,
	{"readiness.claims", Removed}:         NonBreaking,
	{"readiness.claims", Modified}:        NonBreaking,

	// Interfaces (no port in v2; ref instead of contract)
	{"interfaces", Added}:               NonBreaking,
	{"interfaces", Removed}:             Breaking,
	{"interfaces.type", Modified}:       Breaking,
	{"interfaces.ref", Modified}:        PotentialBreaking,
	{"interfaces.visibility", Modified}: PotentialBreaking,

	// Configurations (name-indexed)
	{"configurations", Added}:           NonBreaking,
	{"configurations", Removed}:         Breaking,
	{"configurations.schema", Modified}: PotentialBreaking,
	{"configurations.schema", Added}:    NonBreaking,
	{"configurations.schema", Removed}:  Breaking,
	{"configurations.ref", Modified}:    PotentialBreaking,
	{"configurations.ref", Added}:       NonBreaking,
	{"configurations.ref", Removed}:     Breaking,

	// Policies (name-indexed)
	{"policies", Added}:           NonBreaking,
	{"policies", Removed}:         PotentialBreaking,
	{"policies.schema", Modified}: PotentialBreaking,
	{"policies.ref", Modified}:    PotentialBreaking,
	{"policies.ref", Added}:       NonBreaking,
	{"policies.ref", Removed}:     PotentialBreaking,

	// Dependencies (name-indexed)
	{"dependencies", Added}:                  NonBreaking,
	{"dependencies", Removed}:                Breaking,
	{"dependencies.ref", Modified}:           PotentialBreaking,
	{"dependencies.compatibility", Modified}: PotentialBreaking,
	{"dependencies.required", Modified}:      PotentialBreaking,

	// OpenAPI paths
	{"openapi.paths", Added}:   NonBreaking,
	{"openapi.paths", Removed}: Breaking,

	// OpenAPI methods
	{"openapi.methods", Added}:   NonBreaking,
	{"openapi.methods", Removed}: Breaking,

	// OpenAPI parameters
	{"openapi.parameters", Added}:    PotentialBreaking,
	{"openapi.parameters", Removed}:  Breaking,
	{"openapi.parameters", Modified}: PotentialBreaking,

	// OpenAPI request body
	{"openapi.request-body", Added}:    PotentialBreaking,
	{"openapi.request-body", Removed}:  PotentialBreaking,
	{"openapi.request-body", Modified}: PotentialBreaking,

	// OpenAPI responses
	{"openapi.responses", Added}:    NonBreaking,
	{"openapi.responses", Removed}:  Breaking,
	{"openapi.responses", Modified}: PotentialBreaking,
}

// classify returns the classification for a given path and change type.
// Bracketed keys like "configs[0].schema" or "service.owner.contacts[slack:#c]"
// are normalised to "configs.schema" / "service.owner.contacts" before lookup.
// Unknown paths default to PotentialBreaking.
func classify(path string, ct ChangeType) Classification {
	if c, ok := rules[classificationKey{path, ct}]; ok {
		return c
	}
	// Strip array indices so "configs[0].schema" matches "configs.schema".
	norm := indexRe.ReplaceAllString(path, "")
	if c, ok := rules[classificationKey{norm, ct}]; ok {
		return c
	}
	return PotentialBreaking
}
