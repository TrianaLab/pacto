package backstage

import (
	"cmp"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// callerGroup is one observed caller with no contract and the services it
// calls. ambiguous is set when its name matched more than one contracted
// service, so Pacto does not know whether it has a contract.
type callerGroup struct {
	caller    string
	to        []fleet.ServiceKey
	ambiguous bool
}

// base is the name a stub is named after: the caller's last path segment.
func (c callerGroup) base() string {
	return c.caller[strings.LastIndex(c.caller, "/")+1:]
}

// callerGroups groups the snapshot's uncontracted callers by caller, sorted.
func callerGroups(snap *fleet.FleetSnapshot) []callerGroup {
	var out []callerGroup
	for _, u := range snap.UncontractedCallers {
		i := slices.IndexFunc(out, func(c callerGroup) bool { return c.caller == u.Caller })
		if i < 0 {
			out = append(out, callerGroup{caller: u.Caller})
			i = len(out) - 1
		}
		if !slices.Contains(out[i].to, u.ToService) {
			out[i].to = append(out[i].to, u.ToService)
		}
		out[i].ambiguous = out[i].ambiguous || u.Ambiguous
	}
	slices.SortFunc(out, func(a, b callerGroup) int { return cmp.Compare(a.caller, b.caller) })
	return out
}

// stub is the Component of caller c, named name: observed, owned by nobody
// Pacto knows, depending on the services it calls.
func (e *exporter) stub(c callerGroup, name string) Entity {
	s := entity(kindComp, e.base.annotations(fleet.ProvenanceObserved))
	s.Metadata.Name = name
	s.Metadata.Tags = []string{TagObserved}
	ann := s.Metadata.Annotations
	ann[AnnotationCaller] = c.caller
	if c.ambiguous {
		ann[AnnotationCallerAmbiguous] = "true"
		ann[AnnotationCompleteness] = string(fleet.CompletenessPartial)
	}
	s.Spec.Type, s.Spec.Lifecycle, s.Spec.Owner = "service", unknown, unknown
	var entries []string
	for _, to := range c.to {
		if n, ok := e.components[to]; ok {
			s.Spec.DependsOn = append(s.Spec.DependsOn, componentRef(n))
		}
	}
	slices.Sort(s.Spec.DependsOn)
	for _, ref := range s.Spec.DependsOn {
		entries = append(entries, ref+"="+fleet.ProvenanceObserved)
	}
	if len(entries) > 0 {
		ann[AnnotationRelationProvenance] = strings.Join(entries, ",")
	}
	return s
}
