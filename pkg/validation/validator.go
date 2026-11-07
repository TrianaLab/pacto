// Package validation checks a contract across three layers — structural (JSON
// Schema), cross-field consistency, and policy enforcement — then resolves the
// configurations that name their schema by ref in the configuration-refs stage
// and checks each environment's values in the environments stage, and reports
// errors and warnings. It supports local-only policy resolution as well as recursive,
// ref-based resolution through a pluggable BundleResolver.
package validation

import (
	"context"
	"io/fs"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// Validate runs all three validation layers in order on the given contract,
// then the environments stage. If structural validation fails, subsequent
// layers are skipped.
// The rawYAML parameter is the original YAML bytes for JSON Schema validation.
// The bundleFS parameter provides access to bundle files for cross-field validation.
func Validate(c *contract.Contract, rawYAML []byte, bundleFS fs.FS) ValidationResult {
	return ValidateDocument(c, contract.DecodeDocument(rawYAML), bundleFS)
}

// ValidateDocument is [Validate] over a document a loader already decoded
// ([contract.ParseDocument]), so the YAML is not read twice.
func ValidateDocument(c *contract.Contract, doc *contract.Document, bundleFS fs.FS) ValidationResult {
	return Merged(ValidateDocumentStages(c, doc, bundleFS))
}

// ValidateDocumentStages is [ValidateDocument] reported layer by layer: one
// [Stage] each for structural, cross-field, policy, configuration-refs and
// environments. It resolves no configuration ref and warns
// CONFIG_REF_NOT_ENFORCED for each one with values to check.
func ValidateDocumentStages(c *contract.Contract, doc *contract.Document, bundleFS fs.FS) []Stage {
	return runLayers(c, doc, bundleFS, func() ([]ResolvedPolicy, ValidationResult) {
		return ResolvePoliciesFromBundle(c, bundleFS)
	}, func() (ConfigRefs, ValidationResult) {
		return configRefsNotEnforced(c, bundleFS)
	}, time.Now())
}

// ValidateDocumentStagesWith is [ValidateDocumentStages] with configuration
// refs resolved through opts: policies stay local-only, and a configuration ref
// resolves through opts.Resolver in the mode opts says.
func ValidateDocumentStagesWith(ctx context.Context, c *contract.Contract, doc *contract.Document, bundleFS fs.FS, opts ConfigRefOptions) []Stage {
	return runLayers(c, doc, bundleFS, func() ([]ResolvedPolicy, ValidationResult) {
		return ResolvePoliciesFromBundle(c, bundleFS)
	}, func() (ConfigRefs, ValidationResult) {
		return ResolveConfigRefs(ctx, c, bundleFS, opts)
	}, opts.Now)
}

// ValidateWithResolver runs all three validation layers, the
// configuration-refs stage and the environments stage, using the provided
// BundleResolver for recursive ref-based policy resolution and to resolve
// configuration refs. If resolver is nil, any ref-based policies produce a hard
// POLICY_REF_UNRESOLVED error and a configuration ref something depends on a
// CONFIG_REF_UNRESOLVED error (fail closed).
func ValidateWithResolver(ctx context.Context, c *contract.Contract, rawYAML []byte, bundleFS fs.FS, resolver BundleResolver) ValidationResult {
	return Merged(ValidateStagesWithResolver(ctx, c, rawYAML, bundleFS, resolver))
}

// ValidateStagesWithResolver is [ValidateWithResolver] reported layer by
// layer: one [Stage] each for structural, cross-field, policy,
// configuration-refs and environments.
func ValidateStagesWithResolver(ctx context.Context, c *contract.Contract, rawYAML []byte, bundleFS fs.FS, resolver BundleResolver) []Stage {
	return ValidateStagesWithResolverAt(ctx, c, rawYAML, bundleFS, resolver, time.Now())
}

// ValidateStagesWithResolverAt is [ValidateStagesWithResolver] with the time
// that dates a referenced provider's lifecycle warnings.
func ValidateStagesWithResolverAt(ctx context.Context, c *contract.Contract, rawYAML []byte, bundleFS fs.FS, resolver BundleResolver, now time.Time) []Stage {
	return runLayers(c, contract.DecodeDocument(rawYAML), bundleFS, func() ([]ResolvedPolicy, ValidationResult) {
		return ResolvePoliciesWithResolver(ctx, c, bundleFS, resolver)
	}, func() (ConfigRefs, ValidationResult) {
		return ResolveConfigRefs(ctx, c, bundleFS, ConfigRefOptions{Resolver: resolver, Strict: true, Now: now})
	}, now)
}

// Stage statuses.
const (
	StagePass = "pass"
	StageFail = "fail"
	StageSkip = "skip"
)

// Stage is one step of a validation run and what it found. Reason says why a
// skipped stage did not run.
type Stage struct {
	Name     string                       `json:"name"`
	Status   string                       `json:"status"`
	Reason   string                       `json:"reason,omitempty"`
	Errors   []contract.ValidationError   `json:"errors,omitempty"`
	Warnings []contract.ValidationWarning `json:"warnings,omitempty"`
}

// RanStage is a stage that ran: failed when r holds an error, else passed.
func RanStage(name string, r ValidationResult) Stage {
	st := Stage{Name: name, Status: StagePass, Errors: r.Errors, Warnings: r.Warnings}
	if !r.IsValid() {
		st.Status = StageFail
	}
	return st
}

// SkippedStages is a skipped stage for each name, all for one reason.
func SkippedStages(reason string, names ...string) []Stage {
	out := make([]Stage, len(names))
	for i, n := range names {
		out[i] = Stage{Name: n, Status: StageSkip, Reason: reason}
	}
	return out
}

// Merged is every stage's errors and warnings, in stage order.
func Merged(stages []Stage) ValidationResult {
	var r ValidationResult
	for _, st := range stages {
		r.Merge(ValidationResult{Errors: st.Errors, Warnings: st.Warnings})
	}
	return r
}

// runLayers runs the three-layer validation pipeline, then the
// configuration-refs and environments stages, short-circuiting on structural,
// cross-field and policy failures. A failed configuration-refs stage skips
// nothing: environments checks what the refs that did resolve allow. Policy
// resolution is delegated to resolvePolicies and configuration-ref resolution
// to resolveRefs, the steps that differ between the local-only Validate and the
// resolver-based ValidateWithResolver; now dates lifecycle warnings.
func runLayers(c *contract.Contract, doc *contract.Document, bundleFS fs.FS, resolvePolicies func() ([]ResolvedPolicy, ValidationResult), resolveRefs func() (ConfigRefs, ValidationResult), now time.Time) []Stage {
	// Layer 1: Structural validation via JSON Schema.
	structural := RanStage("structural", validateStructuralDoc(doc))
	if structural.Status == StageFail {
		return append([]Stage{structural}, SkippedStages("structural failed", "cross-field", "policy", "configuration-refs", "environments")...)
	}

	// Layer 2: Cross-field validation.
	crossField := RanStage("cross-field", ValidateCrossField(c, bundleFS))
	if crossField.Status == StageFail {
		return append([]Stage{structural, crossField}, SkippedStages("cross-field failed", "policy", "configuration-refs", "environments")...)
	}

	// Layer 3: Policy resolution + enforcement.
	policies, policyResult := resolvePolicies()
	if policyResult.IsValid() {
		policyResult.Merge(enforcePolicies(doc, policies))
	}
	policy := RanStage("policy", policyResult)
	if policy.Status == StageFail {
		return append([]Stage{structural, crossField, policy}, SkippedStages("policy failed", "configuration-refs", "environments")...)
	}

	// The later stages read the files cross-field checked, so they run only
	// when every layer before them passed.
	refs, refResult := resolveRefs()
	return []Stage{structural, crossField, policy,
		ConfigRefStage(c, refs, refResult, ConfigRefOptions{Now: now}),
		EnvironmentStageWith(c, bundleFS, refs)}
}

// ValidateStructuralRaw performs Layer 1 (JSON Schema) validation on raw YAML bytes.
func ValidateStructuralRaw(rawYAML []byte) ValidationResult {
	return validateStructuralDoc(contract.DecodeDocument(rawYAML))
}

func validateStructuralDoc(doc *contract.Document) ValidationResult {
	data, err := doc.JSON()
	if err != nil {
		var result ValidationResult
		result.AddError("", "YAML_PARSE_ERROR", err.Error())
		return result
	}
	return ValidateStructural(data)
}
