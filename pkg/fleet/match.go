package fleet

import (
	"iter"
	"maps"
)

// The match states of a service or a dependency: how what the contracts
// declare compares with what Pacto observes running.
const (
	// MatchMatched: declared and observed.
	MatchMatched = "matched"
	// MatchExpectedOnly: declared, and Pacto can see it does not run (a
	// service) or is not called (a dependency).
	MatchExpectedOnly = "expected-only"
	// MatchObservedOnly: observed, and no contract declares it.
	MatchObservedOnly = "observed-only"
	// MatchInsufficient: declared, and Pacto cannot tell whether it runs or
	// is called. Unknown never counts as matched.
	MatchInsufficient = "insufficient"
)

// MatchCounts counts services or dependencies by match state.
type MatchCounts struct {
	Matched      int `json:"matched" doc:"Declared and observed"`
	ExpectedOnly int `json:"expectedOnly" doc:"Declared, and Pacto can see that it does not run or is not called"`
	ObservedOnly int `json:"observedOnly" doc:"Observed, and no contract declares it"`
	Insufficient int `json:"insufficient" doc:"Declared, and Pacto cannot tell whether it runs or is called"`
}

func (c *MatchCounts) add(state string) {
	switch state {
	case MatchMatched:
		c.Matched++
	case MatchExpectedOnly:
		c.ExpectedOnly++
	case MatchObservedOnly:
		c.ObservedOnly++
	case MatchInsufficient:
		c.Insufficient++
	}
}

// MatchTally is how far what a set of services declares is from what runs:
// services and their dependencies by match state. Percent is matched over
// what Pacto can observe (matched, expected-only and observed-only), Coverage
// what Pacto can observe over everything counted; both are absent when their
// base is 0, so a fleet Pacto observes nothing of never reads 100%.
type MatchTally struct {
	Services     MatchCounts  `json:"services" doc:"Services that are no golden path, by match state; an inferred service is observed-only"`
	Dependencies MatchCounts  `json:"dependencies" doc:"Service-to-service dependencies, by match state; a configuration reference and an edge inferred from configuration alone have none"`
	Percent      *int         `json:"percent,omitempty" doc:"Matched services and dependencies over those Pacto can observe, rounded down; absent when Pacto observes none"`
	Coverage     *int         `json:"coverage,omitempty" doc:"Services and dependencies Pacto can observe over all counted, rounded down; absent when none is counted"`
	Completeness Completeness `json:"completeness" enum:"complete,partial" doc:"partial when a source the tally reads is partial or stale"`
}

// serviceMatch is the match state of one service that is no golden path. A
// stale or quarantined target is no proof it runs.
func (q *Query) serviceMatch(s *ServiceRecord) string {
	if s.Inferred() {
		return MatchObservedOnly
	}
	for _, tk := range s.Targets {
		if t := q.snap.Targets[tk]; t != nil && !t.Stale && !t.Quarantined {
			return MatchMatched
		}
	}
	if yes, _ := q.notDeployed(s); yes {
		return MatchExpectedOnly
	}
	return MatchInsufficient
}

// dependencyMatch is the match state of one merged service edge, "" when it
// has none: a configuration reference, or an edge inferred from
// configuration alone. A runtime edge with an inferred end is observed-only.
func dependencyMatch(e NeighborhoodEdge) string {
	switch edgeDifference(e) {
	case DifferenceMatched:
		return MatchMatched
	case DifferenceExpectedNotObserved:
		return MatchExpectedOnly
	case DifferenceObservedNotExpected:
		return MatchObservedOnly
	case DifferenceInsufficient:
		return MatchInsufficient
	}
	if runtimeInferred(e) {
		return MatchObservedOnly
	}
	return ""
}

// matchOf is the match tally of the services keys names that are no golden
// path, and of the dependencies they declare or are observed making. A
// Hidden relationship is never judged: its reconciliation was computed
// before restriction.
//
// ponytail: one pass over every relationship per call; index by caller if
// the map calls it once per group at scale.
func (q *Query) matchOf(keys iter.Seq[ServiceKey]) MatchTally {
	in := map[ServiceKey]bool{}
	var t MatchTally
	for k := range keys {
		if s := q.snap.Services[k]; s != nil && !s.Template {
			in[k] = true
			t.Services.add(q.serviceMatch(s))
		}
	}
	edges := map[[2]ServiceKey]*NeighborhoodEdge{}
	fold := func(rel Relationship) {
		to := q.snap.Services[rel.ToService]
		if rel.Type != RelationshipDependency || rel.Hidden || !in[rel.FromService] || to == nil || to.Template {
			return
		}
		k := [2]ServiceKey{rel.FromService, rel.ToService}
		if edges[k] == nil {
			edges[k] = q.newEdge(k[0], k[1], RelationDependency)
		}
		q.foldRelationshipIntoEdge(edges[k], rel)
	}
	for _, rel := range q.snap.Relationships {
		fold(rel)
	}
	for _, rel := range q.snap.InferredRelationships {
		fold(rel)
	}
	for _, e := range edges {
		t.Dependencies.add(dependencyMatch(*e))
	}
	s, d := t.Services, t.Dependencies
	observed := s.Matched + s.ExpectedOnly + s.ObservedOnly + d.Matched + d.ExpectedOnly + d.ObservedOnly
	t.Percent = percentOf(s.Matched+d.Matched, observed)
	t.Coverage = percentOf(observed, observed+s.Insufficient+d.Insufficient)
	t.Completeness = CompletenessPartial
	if q.snap.Completeness == CompletenessComplete {
		t.Completeness = CompletenessComplete
	}
	return t
}

// Match is the match tally of every service in the view.
func (q *Query) Match() MatchTally { return q.matchOf(maps.Keys(q.snap.Services)) }

// percentOf is n over of as a whole percentage rounded down, nil when of is 0.
func percentOf(n, of int) *int {
	if of == 0 {
		return nil
	}
	return new(100 * n / of)
}
