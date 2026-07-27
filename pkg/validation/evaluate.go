package validation

import (
	"fmt"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/evidence"
	"github.com/trianalab/pacto/v3/pkg/finding"
)

// Coverage reports how many of the contract's REQUIRED assertions were actually evaluated
// (Outcome=Observed). Explanatory metadata; it NEVER changes the aggregate compliance state (INV-2).
// Computed in the same pass as Evaluate.
type Coverage struct {
	Evaluated int `json:"evaluated"`
	Required  int `json:"required"`
}

// Evaluate reasons over Contract x Evidence -> typed Findings + Coverage. It emits a confirmed violation
// ONLY when a matching observation has Outcome=Observed AND its payload contradicts the contract; a required
// assertion with no usable observation yields a SeverityUnknown finding. The engine is pure and stateless:
// it reads the operator-stamped Outcome and applies no temporal or k8s logic. Spec section 4.
func Evaluate(c contract.Contract, ev evidence.EvidenceSet) ([]finding.Finding, Coverage) {
	var findings []finding.Finding
	var cov Coverage
	evRef := finding.EvidenceRef{
		Source:     ev.Source,
		ObservedAt: ev.ObservedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	svc := c.Service.Name

	// Interfaces: availability is ALWAYS required (AR3). Contradiction: !Present -> INTERFACE_ABSENT.
	for _, iface := range c.Interfaces {
		evalAssertion(&findings, &cov, ev, evRef, evidence.InterfaceObserved, "interface", iface.Name,
			fmt.Sprintf("interfaces[name=%s]", iface.Name), true,
			func(o evidence.Observation) (bool, finding.Code, string) {
				p, _ := o.GetInterfaceObservation()
				return !p.Present, finding.CodeInterfaceAbsent,
					fmt.Sprintf("interface %q is not available (zero ready endpoints beyond the stabilization window)", iface.Name)
			})
	}

	// Capabilities: health/metrics are required + observable. extension routes to the engine rule.
	for _, cap := range c.Capabilities {
		key := cap.AssertionKey() // "health"/"metrics" for standard, the namespaced ref for extension
		if cap.Type == contract.CapabilityExtension {
			cov.Required++
			findings = append(findings, unknownFinding(finding.CodeExtensionEvaluatorUnavailable,
				"capability", key, fmt.Sprintf("capabilities[ref=%s]", key),
				fmt.Sprintf("extension capability %q has no registered evaluator", key), evRef))
			continue
		}
		evalAssertion(&findings, &cov, ev, evRef, evidence.CapabilityObserved, "capability", key,
			fmt.Sprintf("capabilities[type=%s]", key), true,
			func(o evidence.Observation) (bool, finding.Code, string) {
				p, _ := o.GetCapabilityObservation()
				return !p.Present, finding.CodeCapabilityAbsent,
					fmt.Sprintf("capability %q is absent at runtime", key)
			})
	}

	// Dependencies: required per dep.Required. Contradiction: !Reachable -> DEPENDENCY_UNREACHABLE.
	for _, dep := range c.Dependencies {
		name := dep.Name
		evalAssertion(&findings, &cov, ev, evRef, evidence.DependencyReachable, "dependency", name,
			fmt.Sprintf("dependencies[name=%s]", name), dep.Required,
			func(o evidence.Observation) (bool, finding.Code, string) {
				p, _ := o.GetDependencyObservation()
				return !p.Reachable, finding.CodeDependencyUnreachable,
					fmt.Sprintf("dependency %q is unreachable", name)
			})
	}

	// Configurations: required per cfg.Required. Contradiction: !Present -> CONFIGURATION_ABSENT;
	// Present && !Conformant -> CONFIGURATION_MISMATCH.
	for _, cfg := range c.Configurations {
		name := cfg.Name
		evalAssertion(&findings, &cov, ev, evRef, evidence.ConfigurationPresent, "configuration", name,
			fmt.Sprintf("configurations[name=%s]", name), cfg.Required,
			func(o evidence.Observation) (bool, finding.Code, string) {
				p, _ := o.GetConfigurationObservation()
				if !p.Present {
					return true, finding.CodeConfigurationAbsent,
						fmt.Sprintf("required configuration %q is absent at runtime", name)
				}
				if !p.Conformant {
					return true, finding.CodeConfigurationMismatch,
						fmt.Sprintf("configuration %q exists but does not conform to the declared schema", name)
				}
				return false, "", ""
			})
	}

	// Workload: required iff declared. Service-scoped subject (matched by c.Service.Name).
	if c.Workload != "" {
		evalAssertion(&findings, &cov, ev, evRef, evidence.WorkloadObserved, "service", svc,
			"workload", true,
			func(o evidence.Observation) (bool, finding.Code, string) {
				p, _ := o.GetWorkloadObservation()
				return p.Type != c.Workload, finding.CodeWorkloadMismatch,
					fmt.Sprintf("workload type is %q but observed as %q", c.Workload, p.Type)
			})
	}

	// Persistence: required iff persistent durability declared. Service-scoped subject.
	if c.State != nil && c.State.Persistence.Durability == contract.DurabilityPersistent {
		evalAssertion(&findings, &cov, ev, evRef, evidence.PersistenceObserved, "service", svc,
			"state.persistence.durability", true,
			func(o evidence.Observation) (bool, finding.Code, string) {
				p, _ := o.GetPersistenceObservation()
				return !p.Durable, finding.CodePersistenceMismatch,
					"contract declares persistent durability but no persistent storage binding was observed"
			})
	}

	return findings, cov
}

// evalAssertion applies the uniform per-assertion shape (spec 4.2) for one declared assertion.
func evalAssertion(findings *[]finding.Finding, cov *Coverage, ev evidence.EvidenceSet,
	evRef finding.EvidenceRef, kind evidence.ObservationKind, subjectKind, name, contractPath string,
	required bool, contradicted func(evidence.Observation) (bool, finding.Code, string)) {

	obs := findObservation(ev.Observations, kind, name)
	if obs == nil || obs.Outcome != evidence.Observed {
		if required {
			cov.Required++
			code := unknownCode(obs)
			*findings = append(*findings, unknownFinding(code, subjectKind, name, contractPath,
				unknownMessage(code, subjectKind, name), evRef))
		}
		return
	}
	if required {
		cov.Required++
		cov.Evaluated++
	}
	bad, code, msg := contradicted(*obs)
	if bad {
		sev := finding.SeverityError
		if !required {
			sev = finding.SeverityWarning
		}
		*findings = append(*findings, finding.Finding{
			Code:         code,
			Severity:     sev,
			Category:     finding.CategoryRuntimeDrift,
			Subject:      finding.SubjectRef{Kind: subjectKind, Name: name},
			ContractPath: contractPath,
			Message:      msg,
			EvidenceRefs: []finding.EvidenceRef{evRef},
		})
	}
}

// findObservation matches uniformly on (Kind, Subject.Name) for EVERY dimension — no kind-alone path.
// validate() (INV-1c) guarantees Kind implies Subject.Kind, so the match is safe.
func findObservation(obs []evidence.Observation, kind evidence.ObservationKind, name string) *evidence.Observation {
	for i := range obs {
		if obs[i].Kind == kind && obs[i].Subject.Name == name {
			return &obs[i]
		}
	}
	return nil
}

// unknownCode maps a (possibly nil) observation to its family-2 code (spec 4.4).
func unknownCode(obs *evidence.Observation) finding.Code {
	if obs == nil {
		return finding.CodeEvidenceMissing
	}
	switch obs.Outcome {
	case evidence.Unsupported:
		return finding.CodeObservationUnsupported
	case evidence.Failed:
		return finding.CodeCollectionFailed
	case evidence.Stale:
		return finding.CodeEvidenceStale
	default: // evidence.Insufficient
		return finding.CodeEvidenceInsufficient
	}
}

func unknownMessage(code finding.Code, subjectKind, name string) string {
	switch code {
	case finding.CodeEvidenceMissing:
		return fmt.Sprintf("no observation was produced for required %s %q", subjectKind, name)
	case finding.CodeObservationUnsupported:
		return fmt.Sprintf("%s %q cannot be observed in this environment", subjectKind, name)
	case finding.CodeCollectionFailed:
		return fmt.Sprintf("collection failed for %s %q", subjectKind, name)
	case finding.CodeEvidenceStale:
		return fmt.Sprintf("evidence for %s %q is stale", subjectKind, name)
	default: // CodeEvidenceInsufficient
		return fmt.Sprintf("evidence for %s %q is insufficient to satisfy or contradict the contract", subjectKind, name)
	}
}

func unknownFinding(code finding.Code, subjectKind, name, contractPath, msg string, evRef finding.EvidenceRef) finding.Finding {
	return finding.Finding{
		Code:         code,
		Severity:     finding.SeverityUnknown,
		Category:     finding.CategoryInconclusive,
		Subject:      finding.SubjectRef{Kind: subjectKind, Name: name},
		ContractPath: contractPath,
		Message:      msg,
		EvidenceRefs: []finding.EvidenceRef{evRef},
	}
}
