package backstage

import (
	"cmp"
	"maps"
	"slices"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// resourceEntities names and builds one Resource per external system the
// snapshot holds. Its provenance is the union of its sources'.
func (e *exporter) resourceEntities() []Entity {
	keys := slices.Sorted(maps.Keys(e.snap.Externals))
	reqs := make([]nameRequest, len(keys))
	for i, k := range keys {
		reqs[i] = nameRequest{base: cmp.Or(e.snap.Externals[k].Label, string(k)), identity: string(k)}
	}
	e.resources = make(map[fleet.ExternalKey]string, len(keys))
	out := make([]Entity, len(keys))
	for i, name := range assignNames(reqs, sha256Hex) {
		x := e.snap.Externals[keys[i]]
		e.resources[x.Key] = name
		p := externalProvenance(x.Sources)
		r := entity(kindRes, e.base.annotations(p))
		if p == provenanceInferred {
			r.Metadata.Tags = []string{TagInferred}
		}
		r.Metadata.Annotations[AnnotationExternalKey] = string(x.Key)
		r.Metadata.Name = name
		r.Spec.Type = x.Class
		r.Spec.Owner = unknown
		out[i] = r
	}
	return out
}

// externalProvenance is declared, observed or declared+observed from the
// sources that say so, inferred only when none does.
func externalProvenance(sources []fleet.ExternalSource) string {
	has := func(p string) bool {
		return slices.ContainsFunc(sources, func(s fleet.ExternalSource) bool { return s.Provenance == p })
	}
	d, o := has(fleet.ProvenanceDeclared), has(fleet.ProvenanceObserved)
	switch {
	case d && o:
		return fleet.ProvenanceDeclaredObserved
	case d:
		return fleet.ProvenanceDeclared
	case o:
		return fleet.ProvenanceObserved
	}
	return provenanceInferred
}
