package backstage

import (
	"slices"
	"strconv"

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

// component is the Component entity of service s followed by the API
// entities it provides.
func (e *exporter) component(s *fleet.ServiceRecord) []Entity {
	q := e.q
	// Declared when a contract was read, observed when only a running target
	// shows the service exists, inferred when neither does.
	provenance, tags := provenanceDeclared, []string(nil)
	switch {
	case q.HasContractSource(s.Key):
	case len(s.Targets) > 0:
		provenance, tags = fleet.ProvenanceObserved, []string{TagObserved}
	default:
		provenance, tags = provenanceInferred, []string{TagInferred}
	}
	c := entity(kindComp, e.base.annotations(provenance))
	c.Metadata.Tags = tags
	ann := c.Metadata.Annotations
	ann[AnnotationServiceKey] = string(s.Key)
	name := e.components[s.Key]
	c.Metadata.Name = name
	if name != s.Name {
		c.Metadata.Title = s.Name
	}
	c.Metadata.Description = s.Summary
	c.Spec.Type = "service"
	c.Spec.Lifecycle = lifecycle(q, s)
	c.Spec.Owner = owner(q, s, ann)
	c.Spec.System = system(s, ann, e.systems)
	if provenance == provenanceInferred {
		inferredSource(s, ann)
	}
	if s.NameInferred != nil {
		ann[AnnotationNameProvenance] = provenanceInferred
		ann[AnnotationNameSource] = s.NameInferred.Source
	}
	rels := e.relations(s)
	rels.annotate(ann)
	c.Spec.DependsOn, c.Spec.ConsumesAPIs = rels.dependsOn, rels.consumes
	hidden := s.HiddenDependencies + s.HiddenDependents
	if hidden > 0 {
		ann[AnnotationHiddenRelations] = strconv.Itoa(hidden)
	}
	if hidden > 0 || rels.unresolved > 0 || ann[AnnotationOwnerConflict] != "" {
		ann[AnnotationCompleteness] = string(fleet.CompletenessPartial)
	}
	apis := e.apiEntities(s, c)
	for _, a := range apis {
		c.Spec.ProvidesAPIs = append(c.Spec.ProvidesAPIs, apiRef(a.Metadata.Name))
	}
	slices.Sort(c.Spec.ProvidesAPIs)
	return append([]Entity{c}, apis...)
}

// inferredSource records on ann the workload Pacto learned inferred service
// s from, when it read one.
func inferredSource(s *fleet.ServiceRecord, ann map[string]string) {
	w := s.Workload
	if w == nil {
		return
	}
	where := w.Namespace + "/" + w.Name
	if w.Cluster != "" {
		where = w.Cluster + "/" + where
	}
	ann[AnnotationSource] = "workload"
	ann[AnnotationWorkload] = w.Kind + " " + where
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
