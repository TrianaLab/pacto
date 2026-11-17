package backstage

import (
	"slices"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// Entity provenance values.
const (
	provenanceDeclared = "declared"
	provenanceInferred = "inferred"
)

// factualOwner lists the owner provenances spec.owner may come from. An
// inferred owner never fills it.
var factualOwner = []string{fleet.OwnerProvenanceDeclared, fleet.OwnerProvenanceDeclaredViaTarget, fleet.OwnerProvenanceAttested}

// component is the Component entity named name for service s.
func component(q *fleet.Query, s *fleet.ServiceRecord, name string, base baseAnnotations, systemNames map[string]string) Entity {
	provenance := provenanceDeclared
	if !q.HasContractSource(s.Key) {
		provenance = provenanceInferred
	}
	e := entity(kindComp, base.annotations(provenance))
	if provenance == provenanceInferred {
		e.Metadata.Tags = []string{TagInferred}
	}
	ann := e.Metadata.Annotations
	ann[AnnotationServiceKey] = string(s.Key)
	e.Metadata.Name = name
	if name != s.Name {
		e.Metadata.Title = s.Name
	}
	e.Metadata.Description = s.Summary
	e.Spec.Type = "service"
	e.Spec.Lifecycle = lifecycle(q, s)
	e.Spec.Owner = owner(q, s, ann)
	e.Spec.System = system(s, ann, systemNames)
	if s.NameInferred != nil {
		ann[AnnotationNameProvenance] = provenanceInferred
		ann[AnnotationNameSource] = s.NameInferred.Source
	}
	return e
}

// lifecycle is `deprecated` when the service's current revision declares
// service.lifecycle.deprecated, else `unknown`: a running target never makes a
// service `production`.
func lifecycle(q *fleet.Query, s *fleet.ServiceRecord) string {
	r := q.ProviderCurrentRevision(s.Key)
	if r != nil && r.Contract != nil && r.Contract.Service.Lifecycle != nil && r.Contract.Service.Lifecycle.Deprecated != "" {
		return "deprecated"
	}
	return unknown
}

// owner is spec.owner for s, recording on ann where it comes from: the owner
// ref only for a declared, declared-via-target or attested owner with exactly
// one current claim and a canonical key that is a valid Backstage name, else
// `unknown`.
func owner(q *fleet.Query, s *fleet.ServiceRecord, ann map[string]string) string {
	if s.OwnerProvenance != "" {
		ann[AnnotationOwnerProvenance] = s.OwnerProvenance
	}
	if !slices.Contains(factualOwner, s.OwnerProvenance) {
		return unknown
	}
	switch n := q.OwnerClaimCount(s.Key); {
	case n > 1:
		ann[AnnotationOwnerConflict] = "true"
		return unknown
	case n == 0:
		return unknown
	}
	ref, ok := ownerRef(s.Owner)
	if !ok {
		if key := s.Owner.KeyString(); key != "" {
			ann[AnnotationOwner] = key
			ann[AnnotationOwnerUnmapped] = "true"
		}
		return unknown
	}
	return ref
}

// system is spec.system for s: the System of its declared business domain, or
// nothing. An unknown domain is said on ann, never guessed.
func system(s *fleet.ServiceRecord, ann map[string]string, systemNames map[string]string) string {
	if s.BusinessDomainUnknown {
		ann[AnnotationBusinessDomain] = unknown
		return ""
	}
	return systemNames[s.BusinessDomain]
}

// systems names one System per business domain a service declares and
// returns the names by domain with the System entities.
func systems(services []*fleet.ServiceRecord, base baseAnnotations) (map[string]string, []Entity) {
	var domains []string
	for _, s := range services {
		if s.BusinessDomain != "" && !s.BusinessDomainUnknown && !slices.Contains(domains, s.BusinessDomain) {
			domains = append(domains, s.BusinessDomain)
		}
	}
	reqs := make([]nameRequest, len(domains))
	for i, d := range domains {
		reqs[i] = nameRequest{base: d, identity: d}
	}
	byDomain := map[string]string{}
	out := make([]Entity, 0, len(domains))
	for i, name := range assignNames(reqs, sha256Hex) {
		byDomain[domains[i]] = name
		e := entity(kindSystem, base.annotations(provenanceDeclared))
		e.Metadata.Name = name
		e.Spec.Owner = unknown
		out = append(out, e)
	}
	return byDomain, out
}
