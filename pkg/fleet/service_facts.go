package fleet

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
)

// serviceFactKind is the [InferredSource] kind of an edge a source outside
// every contract reports (see [Collection.ServiceFacts]).
const serviceFactKind = "serviceFact"

// maxAmbiguousNames bounds the names one SERVICE_FACT_AMBIGUOUS lists.
const maxAmbiguousNames = 5

// sourcedFact pairs a raw service fact with the id of the source that
// reported it.
type sourcedFact struct {
	fact   RawServiceFact
	source string
}

// sourcedFacts is the service facts of one source's collection.
func sourcedFacts(source string, col *Collection) []sourcedFact {
	out := make([]sourcedFact, len(col.ServiceFacts))
	for i, f := range col.ServiceFacts {
		out[i] = sourcedFact{fact: f, source: source}
	}
	return out
}

// foldServiceFacts keeps what sources outside every contract report for the
// services they name: the owner and business domain in
// [ServiceRecord.InferredOwner] and [ServiceRecord.InferredBusinessDomain],
// never in Owner or BusinessDomain, and each depends-on as an inferred edge in
// InferredRelationships, never in Relationships. A fact Pacto exported is
// skipped; one that names no service is ignored; one that names more than one,
// or whose values disagree with another's on the same service, gives no value
// and is listed in one SERVICE_FACT_AMBIGUOUS per source.
func foldServiceFacts(snap *FleetSnapshot, facts []sourcedFact) {
	if len(facts) == 0 {
		return
	}
	byName := servicesByFactName(snap)
	amb := ambiguityTally{}
	per := map[ServiceKey][]sourcedFact{}
	for _, sf := range facts {
		if sf.fact.Exported {
			continue
		}
		if k, ok := resolveFact(snap, byName, amb, sf.source, sf.fact.Name, sf.fact.ServiceKey); ok {
			per[k] = append(per[k], sf)
		}
	}
	edges := newFactEdges(snap)
	for _, k := range slices.Sorted(maps.Keys(per)) {
		group := per[k]
		slices.SortFunc(group, func(a, b sourcedFact) int {
			return cmp.Or(cmp.Compare(a.source, b.source), cmp.Compare(a.fact.Entity, b.fact.Entity))
		})
		s := snap.Services[k]
		s.InferredOwner = agreedFact(amb, k, group, factOwner)
		s.InferredBusinessDomain = agreedFact(amb, k, group, factDomain)
		markInferredOwner(s)
		for _, sf := range group {
			for _, ref := range sf.fact.DependsOn {
				if to, ok := resolveFact(snap, byName, amb, sf.source, ref.Name, ref.ServiceKey); ok && to != k {
					edges.add(k, to, cmp.Or(ref.Name, ref.Entity), InferredSource{Source: sf.source, Kind: serviceFactKind, Where: cleanWhere(sf.fact.Entity),
						Confidence: externalcatalog.ConfidencePossible, ObservedAt: copyTime(sf.fact.ObservedAt)})
				}
			}
		}
	}
	snap.serviceFactAmbiguous = amb.groups()
	snap.Limitations = append(snap.Limitations, serviceFactAmbiguity(snap)...)
}

// markInferredOwner marks s's owner inferred when no declared, attested or
// declared-via-target owner exists and a source reports one.
func markInferredOwner(s *ServiceRecord) {
	if s.Owner.IsEmpty() && s.InferredOwner != nil {
		s.OwnerProvenance = OwnerProvenanceInferred
	}
}

// servicesByFactName indexes every service by the lowercased names a fact may
// match it by: its name and, for an inferred service, its workload's name.
func servicesByFactName(snap *FleetSnapshot) map[string][]ServiceKey {
	out := map[string][]ServiceKey{}
	for _, k := range slices.Sorted(maps.Keys(snap.Services)) {
		s := snap.Services[k]
		names := []string{strings.ToLower(s.Name)}
		if s.Workload != nil {
			names = append(names, strings.ToLower(s.Workload.Name))
		}
		for _, n := range slices.Compact(names) {
			out[n] = append(out[n], k)
		}
	}
	return out
}

// resolveFact is the one service a fact or reference names: exactly key when
// set, else the only service named name. A name that matches more than one
// is tallied as ambiguous for source.
func resolveFact(snap *FleetSnapshot, byName map[string][]ServiceKey, amb ambiguityTally, source, name string, key ServiceKey) (ServiceKey, bool) {
	if key != "" {
		return key, snap.Services[key] != nil
	}
	keys := byName[strings.ToLower(name)]
	if len(keys) > 1 {
		amb.add(source, keys)
	}
	if len(keys) != 1 {
		return "", false
	}
	return keys[0], true
}

// factOwner is a fact's owner as an owner key, "" when it names none or the
// key is no clean bounded text.
func factOwner(f RawServiceFact) string {
	if k := f.Owner.KeyString(); cleanText(k, MaxInferredPathRunes) {
		return k
	}
	return ""
}

// factDomain is a fact's business domain, "" when it is no valid slug.
func factDomain(f RawServiceFact) string {
	if validBusinessDomain(f.BusinessDomain) {
		return f.BusinessDomain
	}
	return ""
}

// agreedFact is the value every fact of service k that reports one agrees on,
// with the first such fact's source, where and time. Facts that disagree give
// nil and tally k as ambiguous for each source that reported a value.
func agreedFact(amb ambiguityTally, k ServiceKey, group []sourcedFact, value func(RawServiceFact) string) *InferredFact {
	var out *InferredFact
	var sources []string
	disagree := false
	for _, sf := range group {
		v := value(sf.fact)
		if v == "" {
			continue
		}
		sources = append(sources, sf.source)
		if out == nil {
			out = &InferredFact{Value: v, Source: sf.source, Where: cleanWhere(sf.fact.Entity), AsOf: copyTime(sf.fact.ObservedAt)}
		}
		disagree = disagree || v != out.Value
	}
	if !disagree {
		return out
	}
	for _, src := range sources {
		amb.add(src, []ServiceKey{k})
	}
	return nil
}

// cleanWhere is a source's reference when it is clean bounded text, else "".
func cleanWhere(v string) string {
	if cleanText(v, MaxInferredPathRunes) {
		return v
	}
	return ""
}

// factEdges adds the inferred edges of service facts to a snapshot.
type factEdges struct {
	snap *FleetSnapshot
	// covered holds every (from, to) a Relationships dependency already joins,
	// declared or named by an inferred record: a fact adds nothing to it.
	covered map[[2]ServiceKey]bool
	// inferred indexes each InferredRelationships dependency by (from, to).
	inferred map[[2]ServiceKey]int
}

func newFactEdges(snap *FleetSnapshot) *factEdges {
	e := &factEdges{snap: snap, covered: map[[2]ServiceKey]bool{}, inferred: map[[2]ServiceKey]int{}}
	for _, rel := range snap.Relationships {
		if rel.Type == RelationshipDependency && rel.ToService != "" {
			e.covered[[2]ServiceKey{rel.FromService, rel.ToService}] = true
		}
	}
	for i, rel := range snap.InferredRelationships {
		if rel.Type == RelationshipDependency && rel.ToService != "" {
			e.inferred[[2]ServiceKey{rel.FromService, rel.ToService}] = i
		}
	}
	return e
}

// add records that from depends on to, as src reports: nothing when a
// Relationships edge already joins them, src appended to an inferred edge
// that does, else a new inferred edge that extends the inferred adjacency.
func (e *factEdges) add(from, to ServiceKey, name string, src InferredSource) {
	pair := [2]ServiceKey{from, to}
	if e.covered[pair] {
		return
	}
	if i, ok := e.inferred[pair]; ok {
		rel := &e.snap.InferredRelationships[i]
		if !slices.ContainsFunc(rel.InferredSources, func(o InferredSource) bool {
			return o.Source == src.Source && o.Kind == src.Kind && o.Where == src.Where
		}) {
			rel.InferredSources = append(rel.InferredSources, src)
			sortInferredSources(rel.InferredSources)
		}
		return
	}
	e.inferred[pair] = len(e.snap.InferredRelationships)
	e.snap.InferredRelationships = append(e.snap.InferredRelationships, Relationship{FromService: from, To: name, ToService: to, Resolved: true,
		Type: RelationshipDependency, Provenance: ProvenanceInferred, Compatibility: contract.CompatibilityUnknown,
		InferredSources: []InferredSource{src}, Confidence: externalcatalog.ConfidencePossible})
	e.snap.inferredForward[from] = appendUnique(e.snap.inferredForward[from], to)
	e.snap.inferredReverse[to] = appendUnique(e.snap.inferredReverse[to], from)
}

// HasServiceFact reports whether rel, an inferred edge, carries a depends-on a
// source outside every contract reported (see [Collection.ServiceFacts]).
func HasServiceFact(rel Relationship) bool {
	return slices.ContainsFunc(rel.InferredSources, func(s InferredSource) bool { return s.Kind == serviceFactKind })
}

// serviceFactOnly reports whether every piece of an inferred edge's evidence
// is a service fact: no runtime call or configuration backs it.
func serviceFactOnly(rel Relationship) bool {
	return len(rel.InferredSources) > 0 && len(rel.ObservedSources) == 0 && rel.ObservedCount == 0 &&
		!slices.ContainsFunc(rel.InferredSources, func(s InferredSource) bool { return s.Kind != serviceFactKind })
}

// ambiguityTally collects, per source, the sets of services a fact could not
// pick from, each once.
type ambiguityTally map[string]map[string][]ServiceKey

func (a ambiguityTally) add(source string, keys []ServiceKey) {
	if a[source] == nil {
		a[source] = map[string][]ServiceKey{}
	}
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = string(k)
	}
	a[source][strings.Join(parts, "\n")] = keys
}

// groups is the tally in a stable order, nil when empty.
func (a ambiguityTally) groups() map[string][][]ServiceKey {
	if len(a) == 0 {
		return nil
	}
	out := map[string][][]ServiceKey{}
	for src, sets := range a {
		for _, id := range slices.Sorted(maps.Keys(sets)) {
			out[src] = append(out[src], sets[id])
		}
	}
	return out
}

// serviceFactAmbiguity is one SERVICE_FACT_AMBIGUOUS per source from the
// services snap holds: a set of several services counts only while at least
// two of them are shown, so a restricted view never counts or names a hidden
// one.
func serviceFactAmbiguity(snap *FleetSnapshot) []Limitation {
	var out []Limitation
	for _, src := range slices.Sorted(maps.Keys(snap.serviceFactAmbiguous)) {
		var names []string
		for _, keys := range snap.serviceFactAmbiguous[src] {
			shown := slices.DeleteFunc(slices.Clone(keys), func(k ServiceKey) bool { return snap.Services[k] == nil })
			if len(shown) == 0 || (len(keys) > 1 && len(shown) < 2) {
				continue
			}
			names = append(names, snap.Services[shown[0]].Name)
		}
		if len(names) == 0 {
			continue
		}
		slices.Sort(names)
		names = slices.Compact(names)
		out = append(out, Limitation{Code: LimitationServiceFactAmbiguous, Source: src, Message: fmt.Sprintf(
			"%d names match more than one service or disagree, so their values were left out: %s",
			len(names), strings.Join(names[:min(len(names), maxAmbiguousNames)], ", "))})
	}
	return out
}
