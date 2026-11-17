package backstage

import (
	"cmp"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// index walks the snapshot's relationships once, grouping them by the service
// they leave, and computes each service's in-use revisions.
func (e *exporter) index(services []*fleet.ServiceRecord) {
	e.edges = map[fleet.ServiceKey][]*fleet.Relationship{}
	for i := range e.snap.Relationships {
		rel := &e.snap.Relationships[i]
		e.edges[rel.FromService] = append(e.edges[rel.FromService], rel)
	}
	e.inUse = make(map[fleet.ServiceKey]map[fleet.RevisionKey]bool, len(services))
	for _, s := range services {
		e.inUse[s.Key] = e.inUseRevisions(s)
	}
}

// inUseRevisions is the revisions s's visible targets run, else its current
// revision: the revisions every other fleet answer reads declared edges from,
// so a dependency only an older, unused revision declared is not exported.
func (e *exporter) inUseRevisions(s *fleet.ServiceRecord) map[fleet.RevisionKey]bool {
	out := map[fleet.RevisionKey]bool{}
	for _, tk := range s.Targets {
		if rk := e.snap.Targets[tk].ContractRevision; e.snap.Revisions[rk] != nil {
			out[rk] = true
		}
	}
	if len(out) == 0 {
		if r := e.q.ProviderCurrentRevision(s.Key); r != nil {
			out[r.Key] = true
		}
	}
	return out
}

// exported reports whether rel, an edge from s, is one the export speaks
// about: visible and, when declared, declared by an in-use revision.
func (e *exporter) exported(s fleet.ServiceKey, rel *fleet.Relationship) bool {
	return !rel.Hidden && (rel.Provenance != fleet.ProvenanceDeclared || e.inUse[s][rel.FromRevision])
}

// ref is the entity ref rel points at, or "" when it points at nothing the
// export holds or is no runtime dependency (a policy reference, or a declared
// edge that does not resolve).
func (e *exporter) ref(rel *fleet.Relationship) string {
	switch {
	case rel.Type == fleet.RelationshipPolicyRef:
		return ""
	case rel.ToExternal != "":
		if n, ok := e.resources[rel.ToExternal]; ok {
			return "resource:default/" + n
		}
	case rel.Provenance != fleet.ProvenanceDeclared || fleet.ConsumesEdge(*rel):
		if n, ok := e.components[rel.ToService]; ok {
			return componentRef(n)
		}
	}
	return ""
}

func componentRef(name string) string { return "component:default/" + name }

// evidence is how one ref is known: declared, observed or both, and the last
// time an observation saw it.
type evidence struct {
	declared, observed bool
	lastSeen           *time.Time
}

// add folds rel's provenance into ev.
func (ev *evidence) add(rel *fleet.Relationship) {
	if rel.Provenance == fleet.ProvenanceDeclared {
		ev.declared = true
		return
	}
	ev.observed = true
	if rel.LastSeen != nil && (ev.lastSeen == nil || rel.LastSeen.After(*ev.lastSeen)) {
		ev.lastSeen = rel.LastSeen
	}
}

// String is the provenance of ev, with @<last seen> in RFC 3339 UTC on an
// observed one: the trace window, never the snapshot's time.
func (ev *evidence) String() string {
	p := fleet.ProvenanceDeclared
	switch {
	case ev.declared && ev.observed:
		p = fleet.ProvenanceDeclaredObserved
	case ev.observed:
		p = fleet.ProvenanceObserved
	}
	if ev.lastSeen != nil {
		p += "@" + ev.lastSeen.UTC().Format(time.RFC3339)
	}
	return p
}

// relationInfo is what one service's edges export.
type relationInfo struct {
	dependsOn, consumes []string
	provenance          string
	inferred            string
	unresolved          int
}

// annotate writes ri's annotations onto ann.
func (ri relationInfo) annotate(ann map[string]string) {
	if ri.provenance != "" {
		ann[AnnotationRelationProvenance] = ri.provenance
	}
	if ri.inferred != "" {
		ann[AnnotationInferredDependsOn] = ri.inferred
	}
	if ri.unresolved > 0 {
		ann[AnnotationUnresolvedDependency] = strconv.Itoa(ri.unresolved)
	}
}

// relations reads each edge from s once. spec.dependsOn holds every ref a
// declared or observed edge names; a ref only an inferred edge names goes to
// inferred-depends-on and never into spec. Unresolved counts the distinct
// (type, name) pairs of in-use declared edges that resolve to nothing and are
// not external.
func (e *exporter) relations(s *fleet.ServiceRecord) relationInfo {
	known := map[string]*evidence{}
	inferred := map[string]bool{}
	consumes := map[string]bool{}
	unresolved := map[[2]string]bool{}
	for _, rel := range e.edges[s.Key] {
		if !e.exported(s.Key, rel) {
			continue
		}
		if rel.Provenance == fleet.ProvenanceDeclared && !rel.Resolved && !rel.External {
			unresolved[[2]string{rel.Type, rel.To}] = true
		}
		for _, id := range e.consumedAPIs(rel) {
			consumes[apiRef(e.apis[id])] = true
		}
		ref := e.ref(rel)
		switch {
		case ref == "":
		case rel.Provenance == fleet.ProvenanceInferred:
			inferred[ref] = true
		default:
			known[ref] = cmp.Or(known[ref], &evidence{})
			known[ref].add(rel)
		}
	}
	ri := relationInfo{dependsOn: slices.Sorted(maps.Keys(known)), consumes: slices.Sorted(maps.Keys(consumes)), unresolved: len(unresolved)}
	entries := make([]string, len(ri.dependsOn))
	for i, ref := range ri.dependsOn {
		entries[i] = ref + "=" + known[ref].String()
	}
	ri.provenance = strings.Join(entries, ",")
	var only []string
	for ref := range inferred {
		if known[ref] == nil {
			only = append(only, ref)
		}
	}
	slices.Sort(only)
	ri.inferred = strings.Join(only, ",")
	return ri
}
