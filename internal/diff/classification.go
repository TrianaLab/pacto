package diff

// classificationKey maps a field path and change type to a classification.
type classificationKey struct {
	Path string
	Type ChangeType
}

// rules is the deterministic lookup table for change classification.
// Each entry maps (field path, change type) → classification.
var rules = map[classificationKey]Classification{
	// Service identity
	{"service.name", Modified}:    Breaking,
	{"service.version", Modified}: NonBreaking,
	{"service.owner", Modified}:   NonBreaking,
	{"service.owner", Added}:      NonBreaking,
	{"service.owner", Removed}:    NonBreaking,
	{"service.image", Modified}:   NonBreaking,
	{"service.image", Added}:      NonBreaking,
	{"service.image", Removed}:    NonBreaking,

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

	// Configuration
	{"configuration.schema", Modified}: PotentialBreaking,
	{"configuration.schema", Added}:    NonBreaking,
	{"configuration.schema", Removed}:  Breaking,
	{"configuration", Added}:           NonBreaking,
	{"configuration", Removed}:         Breaking,

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

	// Runtime — health
	{"runtime.health.interface", Modified}:           PotentialBreaking,
	{"runtime.health.path", Modified}:                PotentialBreaking,
	{"runtime.health.initialDelaySeconds", Modified}: NonBreaking,

	// Scaling
	{"scaling.min", Modified}: PotentialBreaking,
	{"scaling.max", Modified}: NonBreaking,
	{"scaling", Added}:        NonBreaking,
	{"scaling", Removed}:      PotentialBreaking,

	// Dependencies
	{"dependencies", Added}:                  NonBreaking,
	{"dependencies", Removed}:                Breaking,
	{"dependencies.compatibility", Modified}: PotentialBreaking,
	{"dependencies.required", Modified}:      PotentialBreaking,

	// OpenAPI paths
	{"openapi.paths", Added}:   NonBreaking,
	{"openapi.paths", Removed}: Breaking,

	// OpenAPI methods
	{"openapi.methods", Added}:   NonBreaking,
	{"openapi.methods", Removed}: Breaking,

	// OpenAPI request body
	{"openapi.request-body", Added}:    PotentialBreaking,
	{"openapi.request-body", Removed}:  PotentialBreaking,
	{"openapi.request-body", Modified}: PotentialBreaking,

	// OpenAPI responses
	{"openapi.responses", Added}:    NonBreaking,
	{"openapi.responses", Removed}:  Breaking,
	{"openapi.responses", Modified}: PotentialBreaking,

	// JSON Schema properties
	{"schema.properties", Added}:   NonBreaking,
	{"schema.properties", Removed}: Breaking,
}

// classify returns the classification for a given path and change type.
// Unknown paths default to PotentialBreaking.
func classify(path string, ct ChangeType) Classification {
	if c, ok := rules[classificationKey{path, ct}]; ok {
		return c
	}
	return PotentialBreaking
}
