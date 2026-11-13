package fleet

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"
)

// observedFleet: pg and pg2 provide postgresql and app declares pg. In shop:
// pg, pg2, app and obs; in hidden: spy and ghostapp, which also declares pg.
// obs calls pg (fresh), app calls pg2, spy and ghostapp call pg, and two
// callers with no contract call pg: one in shop and one with no namespace.
func observedFleet(t *testing.T, extra ...ObservedEdge) *Query {
	t.Helper()
	k := SourceKindKubernetes
	target := func(scope, svc string) RawTarget {
		return RawTarget{Scope: scope, Kind: k, Name: svc, Service: svc, Compliance: StatusCompliant}
	}
	seen := fixedNow().Add(-time.Hour)
	src := NewMemorySource("k8s", k, &Collection{
		Revisions: []RawRevision{
			asPGProvider(lcRev("pg", "16.0.0", nil)),
			asPGProvider(lcRev("pg2", "1.0.0", nil)),
			lcRev("app", "1.0.0", nil, pgDep("^16.0.0")),
			lcRev("ghostapp", "1.0.0", nil, pgDep("^16.0.0")),
			lcRev("obs", "1.0.0", nil),
			lcRev("spy", "1.0.0", nil),
		},
		Targets: []RawTarget{target("shop", "pg"), target("shop", "pg2"), target("shop", "app"), target("shop", "obs"), target("hidden", "spy"), target("hidden", "ghostapp")},
		Observed: append([]ObservedEdge{
			{From: "obs", To: "pg", Count: 1, FirstSeen: seen, LastSeen: seen},
			{From: "app", To: "pg2", Count: 1, FirstSeen: seen, LastSeen: seen},
			{From: "spy", To: "pg", Count: 1, FirstSeen: seen, LastSeen: seen},
			{From: "ghostapp", To: "pg", Count: 1, FirstSeen: seen, LastSeen: seen},
			{From: "k8s:shop/Deployment/legacy", To: "pg", Count: 1},
			{From: "cron-x", To: "pg", Count: 1},
		}, extra...),
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

func observedOf(t *testing.T, q *Query) *OfferingDetailData {
	t.Helper()
	d, _ := succDetail(t, q)
	return d
}

func observedNames(d *OfferingDetailData) []string {
	var out []string
	for _, c := range d.ObservedConsumers.Items {
		if c.Consumer != nil {
			out = append(out, c.Consumer.Key+">"+c.Provider.Key)
		} else {
			out = append(out, c.Caller+">"+c.Provider.Key)
		}
	}
	return out
}

func TestObservedConsumersExcludeDeclared(t *testing.T) {
	d := observedOf(t, observedFleet(t))
	for _, n := range observedNames(d) {
		if n == "app>pg2" || n == "ghostapp>pg" {
			t.Errorf("a declared consumer of a provider of the offering is listed: %v", observedNames(d))
		}
	}
}

func TestObservedConsumersListOnlyObserved(t *testing.T) {
	d := observedOf(t, observedFleet(t))
	want := []string{"obs>pg", "spy>pg", "cron-x>pg", "k8s:shop/Deployment/legacy>pg"}
	if got := observedNames(d); !slices.Equal(got, want) {
		t.Fatalf("observed consumers = %v, want %v", got, want)
	}
	c := d.ObservedConsumers.Items[0]
	if c.Provenance != ProvenanceObserved || c.Uncontracted || c.Stale || c.LastSeen == nil || !slices.Equal(c.Sources, []string{"k8s"}) || c.Provider.Kind != KindService {
		t.Errorf("observed consumer = %+v", c)
	}
	if d.ObservedConsumers.Total != 4 || d.ObservedConsumers.Count != 4 || d.ObservedConsumers.Truncated || d.HiddenObservedConsumers != 0 {
		t.Errorf("preview = %+v, hidden %d", d.ObservedConsumers, d.HiddenObservedConsumers)
	}
}

func TestObservedConsumersUncontracted(t *testing.T) {
	d := observedOf(t, observedFleet(t))
	c := d.ObservedConsumers.Items[3]
	if !c.Uncontracted || c.Consumer != nil || c.Caller != "k8s:shop/Deployment/legacy" || c.LastSeen != nil || c.Stale || c.Provenance != ProvenanceObserved || !slices.Equal(c.Sources, []string{"k8s"}) {
		t.Errorf("uncontracted row = %+v", c)
	}
}

func TestObservedConsumersStale(t *testing.T) {
	old := fixedNow().Add(-48 * time.Hour)
	src := NewMemorySource("traces", "observation", &Collection{Observed: []ObservedEdge{{From: "obs", To: "pg2", Count: 1, FirstSeen: old, LastSeen: old}, {From: "spy", To: "pg2", Count: 1}}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("k8s", SourceKindKubernetes, &Collection{Revisions: []RawRevision{asPGProvider(lcRev("pg2", "1.0.0", nil)), lcRev("obs", "1.0.0", nil), lcRev("spy", "1.0.0", nil)}, Observed: []ObservedEdge{{From: "spy", To: "pg2", Count: 1}}}), src)
	if err != nil {
		t.Fatal(err)
	}
	d := observedOf(t, NewQuery(snap))
	if len(d.ObservedConsumers.Items) != 2 {
		t.Fatalf("observed = %v", observedNames(d))
	}
	for _, c := range d.ObservedConsumers.Items {
		if !c.Stale {
			t.Errorf("an old or undated observation is stale: %+v", c)
		}
	}
	if d.ObservedConsumers.Items[0].LastSeen == nil || d.ObservedConsumers.Items[1].LastSeen != nil {
		t.Errorf("last seen = %v, %v", d.ObservedConsumers.Items[0].LastSeen, d.ObservedConsumers.Items[1].LastSeen)
	}
	if got := d.ObservedConsumers.Items[1].Sources; !slices.Equal(got, []string{"k8s", "traces"}) {
		t.Errorf("a call two sources saw names both: %v", got)
	}
}

func TestObservedConsumersHiddenCounted(t *testing.T) {
	q := observedFleet(t).Restrict(func(ns string) bool { return ns != "hidden" })
	d := observedOf(t, q)
	for _, n := range observedNames(d) {
		if n == "spy>pg" || n == "ghostapp>pg" {
			t.Errorf("a hidden caller is named: %v", observedNames(d))
		}
	}
	if d.HiddenConsumers != 1 || d.HiddenObservedConsumers != 1 {
		t.Errorf("hidden consumers %d, hidden observed %d; want ghostapp once as a consumer and spy once as a caller", d.HiddenConsumers, d.HiddenObservedConsumers)
	}
}

func TestObservedConsumersHiddenUncontractedCounted(t *testing.T) {
	q := observedFleet(t).Restrict(restrictedVisible)
	d := observedOf(t, q)
	for _, n := range observedNames(d) {
		if n == "cron-x>pg" {
			t.Errorf("a caller with no namespace is named under a cluster-scope-denied view: %v", observedNames(d))
		}
	}
	if d.HiddenObservedConsumers != 2 {
		t.Errorf("hidden observed = %d, want spy and cron-x", d.HiddenObservedConsumers)
	}
}

func TestObservedConsumersBounded(t *testing.T) {
	var extra []ObservedEdge
	for i := range MaxDetailPreview + 5 {
		extra = append(extra, ObservedEdge{From: fmt.Sprintf("k8s:shop/Deployment/c%03d", i), To: "pg", Count: 1})
	}
	d := observedOf(t, observedFleet(t, extra...))
	p := d.ObservedConsumers
	if p.Total != MaxDetailPreview+9 || p.Count != MaxDetailPreview || len(p.Items) != MaxDetailPreview || !p.Truncated {
		t.Errorf("preview total %d count %d items %d truncated %v", p.Total, p.Count, len(p.Items), p.Truncated)
	}
}

func TestOfferingDetailActions(t *testing.T) {
	d, err := observedFleet(t).EntityDetail(KindOffering, "postgresql")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(d.Actions, []string{"impact"}) {
		t.Errorf("actions = %v", d.Actions)
	}
}
