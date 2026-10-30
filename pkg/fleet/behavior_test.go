package fleet

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// behaviorFleet: events 1.0.0 delivers at-least-once on its "orders" interface,
// which it deprecates; events 2.0.0 delivers at-most-once and deprecates
// nothing. Each consumer requires at-least-once delivery from "orders":
// - early accepts ^1 only (met), late accepts ^2 only (unmet),
// - any accepts both revisions (they disagree: unknown),
// - future accepts ^9 (no revision in range: unknown),
// - lost depends on ghost, which no fleet service resolves (unknown),
// - plain requires nothing.
func behaviorFleet(t *testing.T) *Query {
	t.Helper()
	orders := func(delivery string, l *contract.InterfaceLifecycle) contract.Interface {
		return contract.Interface{Name: "orders", Type: "asyncapi", Lifecycle: l, Guarantees: &contract.Guarantees{Delivery: delivery}}
	}
	need := []contract.Requirement{{Interface: "orders", Guarantees: contract.Guarantees{Delivery: "at-least-once"}}}
	svc := func(name, version string, ifaces []contract.Interface, deps ...contract.Dependency) RawRevision {
		c := &contract.Contract{PactoVersion: "2.2", Service: contract.Service{Name: name, Version: version, Owner: contract.Owner{Team: "t"}}, Interfaces: ifaces, Dependencies: deps}
		return RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Digest: "sha256:" + name + version}
	}
	dep := func(provider, rng string, req []contract.Requirement) contract.Dependency {
		return contract.Dependency{Name: provider, Compatibility: rng, Required: true, Requires: req}
	}
	src := NewMemorySource("local", "local", &Collection{
		Revisions: []RawRevision{
			svc("events", "1.0.0", []contract.Interface{orders("at-least-once", &contract.InterfaceLifecycle{Deprecated: "2026-01-01", Sunset: "2026-06-01", Successor: "orders-v2"}), {Name: "orders-v2", Type: "asyncapi"}}),
			svc("events", "2.0.0", []contract.Interface{orders("at-most-once", nil)}),
			svc("early", "1.0.0", nil, dep("events", "^1.0.0", need)),
			svc("late", "1.0.0", nil, dep("events", "^2.0.0", need)),
			svc("any", "1.0.0", nil, dep("events", "", need)),
			svc("future", "1.0.0", nil, dep("events", "^9.0.0", need)),
			svc("lost", "1.0.0", nil, dep("ghost", "", need)),
			svc("plain", "1.0.0", nil, dep("events", "^1.0.0", nil)),
		},
		Targets: []RawTarget{{Scope: "secret", Kind: SourceKindKubernetes, Name: "events", Service: "events", Compliance: StatusCompliant}},
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

func behaviorOf(t *testing.T, q *Query, from string) Relationship {
	t.Helper()
	for _, r := range q.snap.Relationships {
		if r.Type == RelationshipDependency && string(r.FromService) == from {
			return r
		}
	}
	t.Fatalf("no dependency from %s", from)
	return Relationship{}
}

func TestDependencyBehavior(t *testing.T) {
	q := behaviorFleet(t)
	for _, tt := range []struct {
		from, status string
		unmet        bool
		reason       bool
	}{
		{"early", GuaranteeMet, false, false},
		{"late", GuaranteeUnmet, true, false},
		{"any", GuaranteeUnknown, false, true},
		{"future", GuaranteeUnknown, false, true},
		{"lost", GuaranteeUnknown, false, true},
		{"plain", "", false, false},
	} {
		r := behaviorOf(t, q, tt.from)
		if r.GuaranteeStatus != tt.status || (len(r.Unmet) > 0) != tt.unmet || (r.GuaranteeReason != "") != tt.reason {
			t.Errorf("%s: status %q unmet %v reason %q", tt.from, r.GuaranteeStatus, r.Unmet, r.GuaranteeReason)
		}
	}
	late := behaviorOf(t, q, "late")
	if m := late.Unmet[0]; m.Consumer != "late" || m.Provider != "events" || m.Guarantee != "delivery" || m.Offered != "at-most-once" {
		t.Errorf("late mismatch = %+v", m)
	}
	if len(late.Requires) != 1 || late.Requires[0].Interface != "orders" {
		t.Errorf("late requires = %+v", late.Requires)
	}
}

func TestProviderDeprecations(t *testing.T) {
	q := behaviorFleet(t)
	early := behaviorOf(t, q, "early").DeprecatedInterfaces
	if len(early) != 1 || early[0] != (InterfaceDeprecation{Interface: "orders", Deprecated: "2026-01-01", Sunset: "2026-06-01", Successor: "orders-v2"}) {
		t.Errorf("early deprecations = %+v", early)
	}
	// The newest revision any reaches (2.0.0) deprecates nothing; future reaches none.
	for _, from := range []string{"any", "late", "future", "lost"} {
		if d := behaviorOf(t, q, from).DeprecatedInterfaces; len(d) != 0 {
			t.Errorf("%s deprecations = %+v", from, d)
		}
	}
}

func TestReachableRevisionsLocked(t *testing.T) {
	q := behaviorFleet(t)
	var pinned RevisionKey
	for k, r := range q.snap.Revisions {
		if r.Service == "events" && r.Version == "1.0.0" {
			pinned = k
		}
	}
	revs := reachableRevisions(q.snap, Relationship{ToService: q.snap.Revisions[pinned].ServiceKey, ResolvedRevision: pinned, Compatibility: "^2.0.0"})
	if len(revs) != 1 || revs[0].Key != pinned {
		t.Errorf("a pinned dependency reaches only its pin, got %v", revs)
	}
}

func TestEdgeBehaviorFold(t *testing.T) {
	q := behaviorFleet(t)
	d, err := q.EntityDetail(KindService, "events")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]NeighborhoodEdge{}
	for _, e := range d.Service.Relationships.Items {
		got[e.From.Label] = e
	}
	if got["late"].GuaranteeStatus != GuaranteeUnmet || got["any"].GuaranteeStatus != GuaranteeUnknown || got["early"].GuaranteeStatus != GuaranteeMet || got["plain"].GuaranteeStatus != "" {
		t.Errorf("edge statuses: late %q any %q early %q plain %q", got["late"].GuaranteeStatus, got["any"].GuaranteeStatus, got["early"].GuaranteeStatus, got["plain"].GuaranteeStatus)
	}
	if dep := got["early"].DeprecatedInterfaces; len(dep) != 1 || dep[0].Interface != "orders" {
		t.Errorf("early edge deprecations = %+v", dep)
	}
	if c := got["late"].DeclaredClaims.Items[0]; c.GuaranteeStatus != GuaranteeUnmet || len(c.Unmet) != 1 {
		t.Errorf("late claim = %+v", c)
	}
}

func TestFoldBehavior(t *testing.T) {
	// The worst status wins and deprecations union by interface, whatever the order.
	e := &NeighborhoodEdge{}
	b := InterfaceDeprecation{Interface: "b", Deprecated: "2026-01-01"}
	a := InterfaceDeprecation{Interface: "a", Deprecated: "2026-01-01"}
	foldBehavior(e, Relationship{DependencyBehavior: DependencyBehavior{GuaranteeStatus: GuaranteeUnmet}, DeprecatedInterfaces: []InterfaceDeprecation{b}})
	foldBehavior(e, Relationship{DependencyBehavior: DependencyBehavior{GuaranteeStatus: GuaranteeMet}, DeprecatedInterfaces: []InterfaceDeprecation{a, b}})
	if e.GuaranteeStatus != GuaranteeUnmet || len(e.DeprecatedInterfaces) != 2 || e.DeprecatedInterfaces[0].Interface != "a" {
		t.Errorf("folded edge = %+v", e)
	}

	// An expected-free view carries no declared knowledge.
	projectEdgeForViews(e, []KnowledgeView{ViewObserved})
	if e.GuaranteeStatus != "" || e.DeprecatedInterfaces != nil {
		t.Errorf("observed-only edge kept declared behavior: %+v", e)
	}
}

func TestRevisionDependencyBehavior(t *testing.T) {
	q := behaviorFleet(t)
	var late RevisionKey
	for k, r := range q.snap.Revisions {
		if r.Service == "late" {
			late = k
		}
	}
	d, err := q.EntityDetail(KindRevision, string(late))
	if err != nil {
		t.Fatal(err)
	}
	e := d.Revision.Dependencies.Items[0]
	if e.GuaranteeStatus != GuaranteeUnmet || e.DeclaredClaims.Items[0].GuaranteeStatus != GuaranteeUnmet {
		t.Errorf("revision dependency edge = %+v", e)
	}
	// The claim is a copy: changing it leaves the snapshot alone.
	e.DeclaredClaims.Items[0].Unmet[0].Guarantee = "changed"
	if behaviorOf(t, q, "late").Unmet[0].Guarantee != "delivery" {
		t.Error("a detail claim aliases the snapshot")
	}
}

func TestRestrictHidesProviderBehavior(t *testing.T) {
	r := behaviorFleet(t).Restrict(func(ns string) bool { return ns != "secret" })
	for _, from := range []string{"late", "plain"} {
		rel := behaviorOf(t, r, from)
		if !rel.Hidden || rel.Unmet != nil || rel.DeprecatedInterfaces != nil {
			t.Errorf("%s keeps the hidden provider's contract: %+v", from, rel)
		}
	}
	if rel := behaviorOf(t, r, "late"); rel.GuaranteeStatus != GuaranteeUnknown || rel.GuaranteeReason != hiddenMessage {
		t.Errorf("late = %q %q", rel.GuaranteeStatus, rel.GuaranteeReason)
	}
	if rel := behaviorOf(t, r, "plain"); rel.GuaranteeStatus != "" {
		t.Errorf("plain requires nothing, got %q", rel.GuaranteeStatus)
	}
}

func TestInterfaceSummaryBehavior(t *testing.T) {
	l := &contract.InterfaceLifecycle{Deprecated: "2026-01-01"}
	g := &contract.Guarantees{Idempotent: true}
	p := interfacesPreview([]contract.Interface{{Name: "a", Lifecycle: l, Guarantees: g, CompatibilityEpoch: 2}, {Name: "b"}}, nil, nil)
	a := p.Items[0]
	if a.Lifecycle == l || *a.Lifecycle != *l || a.Guarantees == g || !a.Guarantees.Idempotent || a.CompatibilityEpoch != 2 {
		t.Errorf("interface a = %+v", a)
	}
	if b := p.Items[1]; b.Lifecycle != nil || b.Guarantees != nil {
		t.Errorf("interface b = %+v", b)
	}
}
