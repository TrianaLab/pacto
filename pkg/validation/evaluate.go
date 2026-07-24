package validation

import (
	"fmt"

	"github.com/trianalab/pacto/v2/pkg/contract"
	"github.com/trianalab/pacto/v2/pkg/evidence"
	"github.com/trianalab/pacto/v2/pkg/finding"
)

// Evaluate reasons over Contract × Evidence → typed Findings. It emits a finding
// ONLY when evidence AFFIRMATIVELY contradicts the contract (conservative semantic
// equality to avoid false drift). If NO observation exists for a contract assertion,
// Evaluate does NOT flag drift — the collector may not observe that dimension.
func Evaluate(c contract.Contract, ev evidence.EvidenceSet) []finding.Finding {
	var findings []finding.Finding

	evRef := finding.EvidenceRef{
		Source:     ev.Source,
		ObservedAt: ev.ObservedAt.Format("2006-01-02T15:04:05Z07:00"),
	}

	// Capabilities: if the contract requires a capability and evidence shows Present=false → finding.
	for _, cap := range c.Capabilities {
		if obs := findCapabilityObservation(ev.Observations, cap.Type); obs != nil {
			capObs, err := obs.GetCapabilityObservation()
			if err == nil && !capObs.Present {
				findings = append(findings, finding.Finding{
					Code:         finding.CodeCapabilityNotObserved,
					Severity:     finding.DefaultSeverity(finding.CodeCapabilityNotObserved),
					Category:     finding.CategoryOf(finding.CodeCapabilityNotObserved),
					Subject:      finding.SubjectRef{Kind: "capability", Name: cap.Type},
					ContractPath: fmt.Sprintf("capabilities[type=%s]", cap.Type),
					Message:      fmt.Sprintf("capability %q not observed in runtime", cap.Type),
					EvidenceRefs: []finding.EvidenceRef{evRef},
				})
			}
		}
	}

	// Interfaces: if the contract declares an interface and evidence shows Present=false → finding.
	for _, iface := range c.Interfaces {
		if obs := findInterfaceObservation(ev.Observations, iface.Name); obs != nil {
			ifaceObs, err := obs.GetInterfaceObservation()
			if err == nil && !ifaceObs.Present {
				findings = append(findings, finding.Finding{
					Code:         finding.CodeInterfaceNotObserved,
					Severity:     finding.DefaultSeverity(finding.CodeInterfaceNotObserved),
					Category:     finding.CategoryOf(finding.CodeInterfaceNotObserved),
					Subject:      finding.SubjectRef{Kind: "interface", Name: iface.Name},
					ContractPath: fmt.Sprintf("interfaces[name=%s]", iface.Name),
					Message:      fmt.Sprintf("interface %q not observed in runtime", iface.Name),
					EvidenceRefs: []finding.EvidenceRef{evRef},
				})
			}
		}
	}

	// Dependencies: if evidence shows Reachable=false → finding.
	for _, dep := range c.Dependencies {
		if obs := findDependencyObservation(ev.Observations, dep.Name); obs != nil {
			depObs, err := obs.GetDependencyObservation()
			if err == nil && !depObs.Reachable {
				findings = append(findings, finding.Finding{
					Code:         finding.CodeDependencyUnreachable,
					Severity:     finding.DefaultSeverity(finding.CodeDependencyUnreachable),
					Category:     finding.CategoryOf(finding.CodeDependencyUnreachable),
					Subject:      finding.SubjectRef{Kind: "dependency", Name: dep.Name},
					ContractPath: fmt.Sprintf("dependencies[name=%s]", dep.Name),
					Message:      fmt.Sprintf("dependency %q is unreachable", dep.Name),
					EvidenceRefs: []finding.EvidenceRef{evRef},
				})
			}
		}
	}

	// Configuration: for each configuration, check if any key observation shows Present=false.
	for _, cfg := range c.Configurations {
		if obs := findConfigurationObservation(ev.Observations, cfg.Name); obs != nil {
			cfgObs, err := obs.GetConfigurationObservation()
			if err == nil && !cfgObs.Present {
				findings = append(findings, finding.Finding{
					Code:         finding.CodeConfigNotObserved,
					Severity:     finding.DefaultSeverity(finding.CodeConfigNotObserved),
					Category:     finding.CategoryOf(finding.CodeConfigNotObserved),
					Subject:      finding.SubjectRef{Kind: "configuration", Name: cfg.Name},
					ContractPath: fmt.Sprintf("configurations[name=%s]", cfg.Name),
					Message:      fmt.Sprintf("configuration %q not observed in runtime", cfg.Name),
					EvidenceRefs: []finding.EvidenceRef{evRef},
				})
			}
		}
	}

	// Workload: if evidence exists and workload type != contract workload → finding.
	if c.Workload != "" {
		if obs := findWorkloadObservation(ev.Observations); obs != nil {
			wObs, err := obs.GetWorkloadObservation()
			if err == nil && wObs.Type != c.Workload {
				findings = append(findings, finding.Finding{
					Code:         finding.CodeWorkloadMismatch,
					Severity:     finding.DefaultSeverity(finding.CodeWorkloadMismatch),
					Category:     finding.CategoryOf(finding.CodeWorkloadMismatch),
					Subject:      finding.SubjectRef{Kind: "service", Name: c.Service.Name},
					ContractPath: "workload",
					Message:      fmt.Sprintf("workload type is %q but observed as %q", c.Workload, wObs.Type),
					EvidenceRefs: []finding.EvidenceRef{evRef},
				})
			}
		}
	}

	// Persistence: if contract declares persistent durability but evidence shows Durable=false → finding.
	if c.State != nil && c.State.Persistence.Durability == contract.DurabilityPersistent {
		if obs := findPersistenceObservation(ev.Observations); obs != nil {
			pObs, err := obs.GetPersistenceObservation()
			if err == nil && !pObs.Durable {
				findings = append(findings, finding.Finding{
					Code:         finding.CodePersistenceMismatch,
					Severity:     finding.DefaultSeverity(finding.CodePersistenceMismatch),
					Category:     finding.CategoryOf(finding.CodePersistenceMismatch),
					Subject:      finding.SubjectRef{Kind: "service", Name: c.Service.Name},
					ContractPath: "state.persistence.durability",
					Message:      "contract declares persistent durability but evidence shows ephemeral storage",
					EvidenceRefs: []finding.EvidenceRef{evRef},
				})
			}
		}
	}

	return findings
}

func findCapabilityObservation(obs []evidence.Observation, capType string) *evidence.Observation {
	for i := range obs {
		if obs[i].Kind == evidence.CapabilityObserved {
			if capObs, err := obs[i].GetCapabilityObservation(); err == nil && capObs.Type == capType {
				return &obs[i]
			}
		}
	}
	return nil
}

func findInterfaceObservation(obs []evidence.Observation, name string) *evidence.Observation {
	for i := range obs {
		if obs[i].Kind == evidence.InterfaceObserved {
			if ifaceObs, err := obs[i].GetInterfaceObservation(); err == nil && ifaceObs.Name == name {
				return &obs[i]
			}
		}
	}
	return nil
}

func findDependencyObservation(obs []evidence.Observation, name string) *evidence.Observation {
	for i := range obs {
		if obs[i].Kind == evidence.DependencyReachable {
			if depObs, err := obs[i].GetDependencyObservation(); err == nil && depObs.Name == name {
				return &obs[i]
			}
		}
	}
	return nil
}

func findConfigurationObservation(obs []evidence.Observation, key string) *evidence.Observation {
	for i := range obs {
		if obs[i].Kind == evidence.ConfigurationPresent {
			if cfgObs, err := obs[i].GetConfigurationObservation(); err == nil && cfgObs.Key == key {
				return &obs[i]
			}
		}
	}
	return nil
}

func findWorkloadObservation(obs []evidence.Observation) *evidence.Observation {
	for i := range obs {
		if obs[i].Kind == evidence.WorkloadObserved {
			return &obs[i]
		}
	}
	return nil
}

func findPersistenceObservation(obs []evidence.Observation) *evidence.Observation {
	for i := range obs {
		if obs[i].Kind == evidence.PersistenceObserved {
			return &obs[i]
		}
	}
	return nil
}
