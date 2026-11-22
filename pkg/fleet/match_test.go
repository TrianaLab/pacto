package fleet

import (
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// matchRev is the revision of name declaring deps.
func matchRev(name string, deps ...string) RawRevision {
	var d []contract.Dependency
	for _, dep := range deps {
		d = append(d, contract.Dependency{Name: dep, Ref: "oci://x/" + dep, Required: true})
	}
	c := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: name, Version: "1.0.0"}, Dependencies: d}
	return RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Digest: "sha256:" + name}
}

// matchSnap is a with dependencies on b and c, b, c (in namespace secret), d
// and e (running nowhere), the inferred cart workload, and observed calls
// a -> b, d -> b and cart -> a, plus a -> c when observeAC is set.
func matchSnap(t *testing.T, observeAC bool) *FleetSnapshot {
	t.Helper()
	k := SourceKindKubernetes
	tgt := func(ns, name string) RawTarget {
		return RawTarget{Scope: ns, Kind: k, Name: name, Service: name, Digest: "sha256:" + name, Compliance: StatusCompliant}
	}
	edges := []ObservedEdge{call("a", "b"), call("d", "b"), call(cartName, "a")}
	if observeAC {
		edges = append(edges, call("a", "c"))
	}
	return buildClusterSnap(t,
		NewMemorySource("k8s", k, &Collection{
			Revisions: []RawRevision{matchRev("a", "b", "c"), matchRev("b"), matchRev("c"), matchRev("d"), matchRev("e")},
			Targets:   []RawTarget{tgt("shop", "a"), tgt("shop", "b"), tgt("secret", "c"), tgt("shop", "d")},
		}),
		workloadsSource("k8s-workloads", nil, inferredCart()),
		kernel(edges...))
}

func counts(m, eo, oo, ins int) MatchCounts {
	return MatchCounts{Matched: m, ExpectedOnly: eo, ObservedOnly: oo, Insufficient: ins}
}

func TestMatchServiceStates(t *testing.T) {
	snap := matchSnap(t, false)
	q := NewQuery(snap)
	for name, want := range map[string]string{"a": MatchMatched, "e": MatchExpectedOnly, cartName: MatchObservedOnly} {
		if got := q.serviceMatch(snap.Services[NewServiceKey(name)]); got != want {
			t.Errorf("%s: %s, want %s", name, got, want)
		}
	}
	// No source reports targets: Pacto cannot tell whether e runs.
	reg := NewQuery(buildClusterSnap(t, NewMemorySource("reg", "oci", &Collection{Revisions: []RawRevision{matchRev("e")}})))
	if got := reg.serviceMatch(reg.snap.Services[NewServiceKey("e")]); got != MatchInsufficient {
		t.Errorf("placement unknown: %s", got)
	}
	// A golden path is no part of the tally.
	snap.Services[NewServiceKey("e")].Template = true
	if got := NewQuery(snap).Match().Services; got != counts(4, 0, 1, 0) {
		t.Errorf("services with a golden path %+v", got)
	}
}

func TestMatchDependencyStates(t *testing.T) {
	claim := func(r string) DeclaredClaimsPreview {
		return DeclaredClaimsPreview{Items: []DeclaredClaim{{Reconciliation: r}}}
	}
	sources := ObservationSourcesPreview{Items: []ObservedSourceStat{{Source: "kernel"}}}
	for _, tc := range []struct {
		name string
		e    NeighborhoodEdge
		want string
	}{
		{"matched", NeighborhoodEdge{Expected: true, Observed: true}, MatchMatched},
		{"expected, not observed", NeighborhoodEdge{Expected: true, DeclaredClaims: claim(ReconciliationDeclaredNotObserved)}, MatchExpectedOnly},
		{"no observation", NeighborhoodEdge{Expected: true, DeclaredClaims: claim(ReconciliationInsufficient)}, MatchInsufficient},
		{"observed, not expected", NeighborhoodEdge{Observed: true}, MatchObservedOnly},
		{"runtime inferred", NeighborhoodEdge{Inferred: true, ObservationSources: sources}, MatchObservedOnly},
		{"configuration inferred", NeighborhoodEdge{Inferred: true}, ""},
		{"configuration reference", NeighborhoodEdge{Expected: true, Relation: RelationConfigurationReference}, ""},
	} {
		if got := dependencyMatch(tc.e); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
	snap := matchSnap(t, false)
	want := NewQuery(snap).Match().Dependencies
	snap.Relationships = append(snap.Relationships, Relationship{FromService: NewServiceKey("a"), ToService: NewServiceKey("e"), Type: RelationshipConfigRef, Provenance: ProvenanceDeclared})
	if got := NewQuery(snap).Match().Dependencies; got != want || want != counts(1, 1, 2, 0) {
		t.Errorf("dependencies %+v, without the reference %+v", got, want)
	}
}

func TestMatchNoObservationNoPercent(t *testing.T) {
	q := NewQuery(buildClusterSnap(t, NewMemorySource("reg", "oci", &Collection{Revisions: []RawRevision{matchRev("a", "b"), matchRev("b")}})))
	m := q.Match()
	if m.Percent != nil || m.Coverage == nil || *m.Coverage != 0 || m.Services != counts(0, 0, 0, 2) || m.Dependencies != counts(0, 0, 0, 1) {
		t.Errorf("tally %+v", m)
	}
	if e := (&Query{snap: &FleetSnapshot{}}).Match(); e.Percent != nil || e.Coverage != nil {
		t.Errorf("empty tally %+v", e)
	}
}

func TestMatchPercentAndCoverage(t *testing.T) {
	m := NewQuery(matchSnap(t, false)).Match()
	// 4 services and a -> b matched; e and a -> c expected only; cart, d -> b
	// and cart -> a observed only.
	if m.Services != counts(4, 1, 1, 0) || m.Dependencies != counts(1, 1, 2, 0) || *m.Percent != 50 || *m.Coverage != 100 {
		t.Errorf("tally %+v percent %d coverage %d", m, *m.Percent, *m.Coverage)
	}
	if p := percentOf(2, 3); *p != 66 {
		t.Errorf("percent %d", *p)
	}
}

func TestMatchPartialSourceIsPartial(t *testing.T) {
	snap := matchSnap(t, false)
	if c := NewQuery(snap).Match().Completeness; c != snap.Completeness {
		t.Fatalf("completeness %s, snapshot %s", c, snap.Completeness)
	}
	snap.Completeness = CompletenessComplete
	if c := NewQuery(snap).Match().Completeness; c != CompletenessComplete {
		t.Errorf("complete snapshot: %s", c)
	}
	for _, c := range []Completeness{CompletenessPartial, CompletenessEmpty} {
		snap.Completeness = c
		if got := NewQuery(snap).Match().Completeness; got != CompletenessPartial {
			t.Errorf("%s snapshot: %s", c, got)
		}
	}
}

func TestMatchRestricted(t *testing.T) {
	r := NewQuery(matchSnap(t, false)).Restrict(func(_, ns string) bool { return ns != "secret" }, nil)
	m := r.Match()
	// c, cart and their edges are hidden; a -> c is a Hidden relationship.
	if m.Services != counts(3, 1, 0, 0) || m.Dependencies != counts(1, 0, 1, 0) || *m.Percent != 66 {
		t.Errorf("tally %+v", m)
	}
}

func TestMatchInferredIsObservedOnly(t *testing.T) {
	q := NewQuery(matchSnap(t, false))
	cart := NewServiceKey(cartName)
	if m := q.matchOf(func(yield func(ServiceKey) bool) { yield(cart) }); m.Services != counts(0, 0, 1, 0) || m.Dependencies != counts(0, 0, 1, 0) {
		t.Errorf("cart tally %+v", m)
	}
}

func TestMatchHiddenDependencyNotJudged(t *testing.T) {
	visible := func(_, ns string) bool { return ns != "secret" }
	with := NewQuery(matchSnap(t, true)).Restrict(visible, nil).Match()
	without := NewQuery(matchSnap(t, false)).Restrict(visible, nil).Match()
	if !reflect.DeepEqual(with, without) {
		t.Errorf("observing the hidden call changed the tally: %+v vs %+v", with, without)
	}
}

func TestMatchStaleOrQuarantinedTargetIsInsufficient(t *testing.T) {
	for _, mark := range []func(*TargetRecord){func(t *TargetRecord) { t.Stale = true }, func(t *TargetRecord) { t.Quarantined = true }} {
		snap := matchSnap(t, false)
		a := snap.Services[NewServiceKey("a")]
		for _, k := range a.Targets {
			mark(snap.Targets[k])
		}
		if got := NewQuery(snap).serviceMatch(a); got != MatchInsufficient {
			t.Errorf("state %s", got)
		}
	}
}
