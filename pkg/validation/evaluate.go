package validation

import (
	"fmt"
	"slices"
	"sort"
	"time"

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
	// Unverified lists, sorted, the contract paths of declared facets that have a runtime check but got no
	// usable fact this time (e.g. interfaces[name=api].type with probing off). Facets never count toward
	// Required, so a cluster without a protocol probe does not degrade to Unknown.
	Unverified []string `json:"unverified,omitempty"`
}

// Evaluate reasons over Contract x Evidence -> typed Findings + Coverage. It emits a confirmed violation
// ONLY when a matching observation has Outcome=Observed AND its payload contradicts the contract; a required
// assertion with no usable observation yields a SeverityUnknown finding. The engine is pure and stateless:
// it reads the operator-stamped Outcome and applies no temporal or k8s logic. Spec section 4.
func Evaluate(c contract.Contract, ev evidence.EvidenceSet) ([]finding.Finding, Coverage) {
	e := &evaluator{
		ev: ev,
		evRef: finding.EvidenceRef{
			Source:     ev.Source,
			ObservedAt: ev.ObservedAt.Format(time.RFC3339),
		},
		unverified: map[string]bool{},
	}
	e.interfaces(c.Interfaces)
	e.capabilities(c.Capabilities)
	e.dependencies(c.Dependencies)
	e.configurations(c.Configurations)
	e.workload(c)
	e.persistence(c)

	for path := range e.unverified {
		e.cov.Unverified = append(e.cov.Unverified, path)
	}
	sort.Strings(e.cov.Unverified)
	return e.findings, e.cov
}

// evaluator carries one Evaluate pass: the evidence it reads and the findings and coverage it builds.
type evaluator struct {
	ev         evidence.EvidenceSet
	evRef      finding.EvidenceRef
	findings   []finding.Finding
	cov        Coverage
	unverified map[string]bool
}

// Interfaces: availability is ALWAYS required (AR3). Contradiction: !Present -> INTERFACE_ABSENT. The type
// and visibility facets are only judged on an interface that is actually there.
func (e *evaluator) interfaces(ifaces []contract.Interface) {
	for _, iface := range ifaces {
		path := fmt.Sprintf("interfaces[name=%s]", iface.Name)
		obs := e.assert(evidence.InterfaceObserved, "interface", iface.Name, path, true,
			func(o evidence.Observation) (bool, finding.Code, string) {
				p, _ := o.GetInterfaceObservation()
				return !p.Present, finding.CodeInterfaceAbsent,
					fmt.Sprintf("interface %q is not being served", iface.Name)
			})
		if obs == nil {
			continue
		}
		p, _ := obs.GetInterfaceObservation()
		if !p.Present {
			continue
		}
		e.facet(protocolVerdict(iface.Type, p.Protocol), finding.CodeInterfaceProtocolMismatch, true,
			"interface", iface.Name, path+".type", obs,
			fmt.Sprintf("interface %q is declared %s but its port answers as %s", iface.Name, iface.Type, p.Protocol))
		visibility := iface.Visibility
		if visibility == "" {
			visibility = contract.VisibilityInternal // the schema default, never applied in Go
		}
		e.facet(visibilityVerdict(visibility, p.Exposure), finding.CodeInterfaceVisibilityMismatch, true,
			"interface", iface.Name, path+".visibility", obs,
			fmt.Sprintf("interface %q is declared %s but is exposed externally", iface.Name, visibility))
	}
}

// Capabilities: health/metrics are required + observable. An extension has no evaluator, so it is listed
// as unverified coverage rather than counted as a required assertion nothing could ever satisfy: that kept
// every contract declaring one at Unknown forever.
func (e *evaluator) capabilities(caps []contract.Capability) {
	for _, cap := range caps {
		key := cap.AssertionKey() // "health"/"metrics" for standard, the namespaced ref for extension
		if cap.Type == contract.CapabilityExtension {
			e.unverified[fmt.Sprintf("capabilities[ref=%s]", key)] = true
			continue
		}
		e.assert(evidence.CapabilityObserved, "capability", key, fmt.Sprintf("capabilities[type=%s]", key), true,
			func(o evidence.Observation) (bool, finding.Code, string) {
				p, _ := o.GetCapabilityObservation()
				return !p.Present, finding.CodeCapabilityAbsent,
					fmt.Sprintf("capability %q is absent at runtime", key)
			})
	}
}

// Dependencies: required per dep.Required. Contradiction: !Reachable -> DEPENDENCY_UNREACHABLE. The
// provider's version is judged against compatibility on the same axis. A reachable peer the producer marked
// Undeclared is drift, reported as a warning only.
func (e *evaluator) dependencies(deps []contract.Dependency) {
	declared := map[string]bool{}
	for _, dep := range deps {
		name := dep.Name
		declared[name] = true
		path := fmt.Sprintf("dependencies[name=%s]", name)
		obs := e.assert(evidence.DependencyReachable, "dependency", name, path, dep.Required,
			func(o evidence.Observation) (bool, finding.Code, string) {
				p, _ := o.GetDependencyObservation()
				return !p.Reachable, finding.CodeDependencyUnreachable,
					fmt.Sprintf("dependency %q is unreachable", name)
			})
		if obs == nil {
			continue
		}
		p, _ := obs.GetDependencyObservation()
		e.facet(versionVerdict(dep.Compatibility, p.Version), finding.CodeDependencyVersionMismatch, dep.Required,
			"dependency", name, path+".compatibility", obs,
			fmt.Sprintf("dependency %q runs version %s, outside the declared compatibility %s", name, p.Version, dep.Compatibility))
	}
	e.undeclared(declared)
}

// undeclared reports every reachable peer a producer resolved to a Pacto-managed service no declared
// dependency names. A name the contract does declare was already judged above, so it is skipped.
func (e *evaluator) undeclared(declared map[string]bool) {
	for i := range e.ev.Observations {
		o := &e.ev.Observations[i]
		if o.Kind != evidence.DependencyReachable || o.Outcome != evidence.Observed || declared[o.Subject.Name] {
			continue
		}
		if p, _ := o.GetDependencyObservation(); p.Undeclared && p.Reachable {
			e.violation(finding.CodeDependencyUndeclared, true, "dependency", o.Subject.Name, "dependencies", o,
				fmt.Sprintf("the service talks to %q, which no declared dependency names", o.Subject.Name))
		}
	}
}

// Configurations: required per cfg.Required. Contradiction: !Present -> CONFIGURATION_ABSENT;
// Present && !Conformant -> CONFIGURATION_MISMATCH.
func (e *evaluator) configurations(cfgs []contract.Configuration) {
	for _, cfg := range cfgs {
		name := cfg.Name
		e.assert(evidence.ConfigurationPresent, "configuration", name, fmt.Sprintf("configurations[name=%s]", name), cfg.Required,
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
}

// Workload: required iff declared. Service-scoped subject (matched by c.Service.Name).
func (e *evaluator) workload(c contract.Contract) {
	if c.Workload == "" {
		return
	}
	e.assert(evidence.WorkloadObserved, "service", c.Service.Name, "workload", true,
		func(o evidence.Observation) (bool, finding.Code, string) {
			p, _ := o.GetWorkloadObservation()
			return p.Type != c.Workload, finding.CodeWorkloadMismatch,
				fmt.Sprintf("workload type is %q but observed as %q", c.Workload, p.Type)
		})
}

// Persistence: durability is required iff persistent is declared. Scope is a facet of any declared state,
// judged whenever an observation is there; for ephemeral state the observation is optional and Durable is
// never asserted. Service-scoped subject.
func (e *evaluator) persistence(c contract.Contract) {
	if c.State == nil {
		return
	}
	svc := c.Service.Name
	var obs *evidence.Observation
	if c.State.Persistence.Durability == contract.DurabilityPersistent {
		obs = e.assert(evidence.PersistenceObserved, "service", svc, "state.persistence.durability", true,
			func(o evidence.Observation) (bool, finding.Code, string) {
				p, _ := o.GetPersistenceObservation()
				return !p.Durable, finding.CodePersistenceMismatch,
					"contract declares persistent durability but no persistent storage binding was observed"
			})
	} else if o := findObservation(e.ev.Observations, evidence.PersistenceObserved, svc); o != nil && o.Outcome == evidence.Observed {
		obs = o
	}
	if obs == nil {
		return
	}
	p, _ := obs.GetPersistenceObservation()
	e.facet(scopeVerdict(c.State.Persistence.Scope, p.Shared), finding.CodePersistenceMismatch, true,
		"service", svc, "state.persistence.scope", obs,
		"contract declares local persistence scope but one storage claim is mounted by several replicas")
}

// assert applies the uniform per-assertion shape (spec 4.2) for one declared assertion. It returns the
// matched observation when it is Observed, so facet checks read the same payload, and nil otherwise.
func (e *evaluator) assert(kind evidence.ObservationKind, subjectKind, name, contractPath string,
	required bool, contradicted func(evidence.Observation) (bool, finding.Code, string)) *evidence.Observation {

	obs := findObservation(e.ev.Observations, kind, name)
	if obs == nil || obs.Outcome != evidence.Observed {
		if required {
			e.cov.Required++
			code := unknownCode(obs)
			e.findings = append(e.findings, unknownFinding(code, subjectKind, name, contractPath,
				unknownMessage(code, subjectKind, name), e.ref(obs)))
		}
		return nil
	}
	if required {
		e.cov.Required++
		e.cov.Evaluated++
	}
	if bad, code, msg := contradicted(*obs); bad {
		e.violation(code, required, subjectKind, name, contractPath, obs, msg)
	}
	return obs
}

// verdict is the result of one facet check.
type verdict int

const (
	verdictSatisfied  verdict = iota
	verdictViolated           // the fact contradicts the declared facet
	verdictUnverified         // no usable fact: neither satisfies nor contradicts
)

// facet records a facet check: a violation becomes a finding, an unverified facet is listed in coverage.
func (e *evaluator) facet(v verdict, code finding.Code, required bool, subjectKind, name, contractPath string,
	obs *evidence.Observation, msg string) {
	switch v {
	case verdictViolated:
		e.violation(code, required, subjectKind, name, contractPath, obs, msg)
	case verdictUnverified:
		e.unverified[contractPath] = true
	}
}

func (e *evaluator) violation(code finding.Code, required bool, subjectKind, name, contractPath string,
	obs *evidence.Observation, msg string) {
	e.findings = append(e.findings, finding.Finding{
		Code:         code,
		Severity:     finding.SeverityFor(code, required),
		Category:     finding.CategoryFor(code, required),
		Subject:      finding.SubjectRef{Kind: subjectKind, Name: name},
		ContractPath: contractPath,
		Message:      msg,
		EvidenceRefs: []finding.EvidenceRef{e.ref(obs)},
	})
}

// ref stamps the set-level evidence ref with the provenance of the observation a finding was derived from.
// A finding with no matched observation (EVIDENCE_MISSING) keeps the set-level ref only.
func (e *evaluator) ref(obs *evidence.Observation) finding.EvidenceRef {
	r := e.evRef
	if obs == nil {
		return r
	}
	r.Collector = obs.Provenance.Collector
	if !obs.Provenance.DetectedAt.IsZero() {
		r.DetectedAt = obs.Provenance.DetectedAt.Format(time.RFC3339)
	}
	return r
}

// classify judges a fact against the values that satisfy and contradict a facet. Anything else, including
// the empty "not observed" value, leaves the facet unverified: an inability to observe is not a contradiction.
func classify[T comparable](fact T, ok, bad []T) verdict {
	switch {
	case slices.Contains(ok, fact):
		return verdictSatisfied
	case slices.Contains(bad, fact):
		return verdictViolated
	}
	return verdictUnverified
}

// protocolVerdict: openapi is HTTP of either version, gRPC is HTTP/2 only. TLS hides the protocol inside
// and asyncapi names no wire protocol, so neither can be judged.
func protocolVerdict(ifaceType string, p evidence.Protocol) verdict {
	switch ifaceType {
	case contract.InterfaceTypeOpenAPI:
		return classify(p, []evidence.Protocol{evidence.ProtocolHTTP1, evidence.ProtocolHTTP2}, []evidence.Protocol{evidence.ProtocolOther})
	case contract.InterfaceTypeGRPC:
		return classify(p, []evidence.Protocol{evidence.ProtocolHTTP2}, []evidence.Protocol{evidence.ProtocolHTTP1, evidence.ProtocolOther})
	}
	return verdictUnverified
}

// visibilityVerdict: an internal interface must not be exposed externally. A public interface reachable
// only internally is not a contradiction (it may sit behind a gateway the producer cannot see).
func visibilityVerdict(visibility string, x evidence.Exposure) verdict {
	switch visibility {
	case contract.VisibilityInternal:
		return classify(x, []evidence.Exposure{evidence.ExposureInternal}, []evidence.Exposure{evidence.ExposureExternal})
	case contract.VisibilityPublic:
		return classify(x, []evidence.Exposure{evidence.ExposureExternal}, nil)
	}
	return verdictUnverified
}

// versionVerdict judges the provider's deployed version against the declared range. A version or range
// that does not parse is no usable fact.
func versionVerdict(compatibility, version string) verdict {
	rng, err := contract.ParseRange(compatibility)
	if err != nil {
		return verdictUnverified
	}
	ok, err := rng.Check(version)
	switch {
	case err != nil:
		return verdictUnverified
	case ok:
		return verdictSatisfied
	}
	return verdictViolated
}

// scopeVerdict: local storage must not be shared between replicas. Shared scope with per-replica storage
// is not a contradiction, because the shared store may live outside the cluster.
func scopeVerdict(scope string, shared *bool) verdict {
	if shared == nil {
		return verdictUnverified
	}
	switch scope {
	case contract.ScopeLocal:
		return classify(*shared, []bool{false}, []bool{true})
	case contract.ScopeShared:
		return classify(*shared, []bool{true}, nil)
	}
	return verdictUnverified
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
