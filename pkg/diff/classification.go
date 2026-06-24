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

	// Service identity
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
	{"service.image", Modified}:          NonBreaking,
	{"service.image", Added}:             NonBreaking,
	{"service.image", Removed}:           NonBreaking,
	{"service.chart", Modified}:          NonBreaking,
	{"service.chart", Added}:             NonBreaking,
	{"service.chart", Removed}:           NonBreaking,

	// Readiness — operational maturity, never consumer-facing.
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
	{"readiness.checks", Added}:           NonBreaking,
	{"readiness.checks", Removed}:         NonBreaking,
	{"readiness.checks", Modified}:        NonBreaking,

	// Interfaces
	{"interfaces", Added}:   NonBreaking,
	{"interfaces", Removed}: Breaking,

	// Interface fields (when an existing interface is modified)
	{"interfaces.type", Modified}:       Breaking,
	{"interfaces.port", Modified}:       Breaking,
	{"interfaces.port", Added}:          PotentialBreaking,
	{"interfaces.port", Removed}:        Breaking,
	{"interfaces.visibility", Modified}: PotentialBreaking,
	{"interfaces.contract", Modified}:   PotentialBreaking,

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

	// Runtime — workload
	{"runtime.workload", Modified}: Breaking,

	// Runtime — state
	{"runtime.state.type", Modified}:                   Breaking,
	{"runtime.state.persistence.scope", Modified}:      Breaking,
	{"runtime.state.persistence.durability", Modified}: Breaking,
	{"runtime.state.dataCriticality", Modified}:        PotentialBreaking,

	// Runtime — lifecycle
	{"runtime.lifecycle.upgradeStrategy", Modified}:         PotentialBreaking,
	{"runtime.lifecycle.upgradeStrategy", Added}:            NonBreaking,
	{"runtime.lifecycle.upgradeStrategy", Removed}:          PotentialBreaking,
	{"runtime.lifecycle.gracefulShutdownSeconds", Modified}: NonBreaking,
	{"runtime.lifecycle.gracefulShutdownSeconds", Added}:    NonBreaking,
	{"runtime.lifecycle.gracefulShutdownSeconds", Removed}:  NonBreaking,

	// Runtime — health (adding a health declaration is a new capability;
	// removing one loses a runtime check, so potentially breaking).
	{"runtime.health.interface", Modified}:           PotentialBreaking,
	{"runtime.health.interface", Added}:              NonBreaking,
	{"runtime.health.interface", Removed}:            PotentialBreaking,
	{"runtime.health.path", Modified}:                PotentialBreaking,
	{"runtime.health.path", Added}:                   NonBreaking,
	{"runtime.health.path", Removed}:                 PotentialBreaking,
	{"runtime.health.initialDelaySeconds", Modified}: NonBreaking,
	{"runtime.health.initialDelaySeconds", Added}:    NonBreaking,
	{"runtime.health.initialDelaySeconds", Removed}:  NonBreaking,

	// Runtime — metrics
	{"runtime.metrics.interface", Modified}: PotentialBreaking,
	{"runtime.metrics.interface", Added}:    NonBreaking,
	{"runtime.metrics.interface", Removed}:  PotentialBreaking,
	{"runtime.metrics.path", Modified}:      PotentialBreaking,
	{"runtime.metrics.path", Added}:         NonBreaking,
	{"runtime.metrics.path", Removed}:       PotentialBreaking,

	// Scaling
	{"scaling.replicas", Modified}: PotentialBreaking,
	{"scaling.min", Modified}:      PotentialBreaking,
	{"scaling.max", Modified}:      NonBreaking,
	{"scaling", Added}:             NonBreaking,
	{"scaling", Removed}:           PotentialBreaking,

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
