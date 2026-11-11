package fleet

import (
	"fmt"
	"slices"
	"testing"
)

func TestPlacementCounts(t *testing.T) {
	d, err := envTargetFleet(t).EntityDetail(KindService, "env-svc")
	if err != nil {
		t.Fatal(err)
	}
	p := d.Service.Placement
	type row struct {
		env                                    string
		targets, declared, notDeclared, unknwn int
		revs                                   int
	}
	var got []row
	for _, it := range p.Items {
		got = append(got, row{it.Environment, it.Targets, it.Declared, it.NotDeclared, it.Unknown, it.RevisionsTotal})
	}
	// prod: not declared; staging: two declared plus one with no linked revision;
	// unlabelled and conflicting labels share the empty group, listed last.
	want := []row{{"prod", 1, 0, 1, 0, 1}, {"staging", 3, 2, 0, 1, 1}, {"", 2, 0, 0, 2, 1}}
	if !slices.Equal(got, want) || p.Total != 3 || p.Count != 3 || p.Truncated {
		t.Errorf("placement = %+v (%+v)", got, p)
	}
	if len(p.Items[1].Revisions) != 1 || p.Items[1].Revisions[0].Kind != KindRevision {
		t.Errorf("revisions = %+v", p.Items[1].Revisions)
	}
}

// placementQuery builds a query over n revisions of svc, newest highest.
func placementQuery(n int) *Query {
	snap := &FleetSnapshot{Revisions: map[RevisionKey]*ContractRevision{}}
	for i := 1; i <= n; i++ {
		k := RevisionKey(fmt.Sprintf("svc@%d", i))
		snap.Revisions[k] = &ContractRevision{Key: k, Service: "svc", Version: fmt.Sprintf("v1.%d.0", i)}
	}
	return NewQuery(snap)
}

func TestPlacementOrderAndBounds(t *testing.T) {
	q := placementQuery(1)
	var targets []*TargetRecord
	for _, env := range []string{"env-10", "", "env-2", "Prod", "env-2"} {
		targets = append(targets, &TargetRecord{Environment: env, ContractRevision: "svc@1"})
	}
	p := q.placement(targets)
	var envs []string
	for _, it := range p.Items {
		envs = append(envs, it.Environment)
	}
	if !slices.Equal(envs, []string{"Prod", "env-2", "env-10", ""}) || p.Items[1].Targets != 2 || p.Items[1].RevisionsTotal != 1 {
		t.Errorf("order = %q (%+v)", envs, p.Items)
	}
	targets = targets[:0]
	for i := range MaxDetailPreview + 1 {
		targets = append(targets, &TargetRecord{Environment: fmt.Sprintf("e%d", i)})
	}
	p = q.placement(targets)
	if p.Total != MaxDetailPreview+1 || p.Count != MaxDetailPreview || !p.Truncated || len(p.Items[0].Revisions) != 0 || p.Items[0].Revisions == nil {
		t.Errorf("bounds: total %d count %d truncated %v", p.Total, p.Count, p.Truncated)
	}
	if empty := q.placement(nil); empty.Items == nil || empty.Total != 0 {
		t.Errorf("empty = %+v", empty)
	}
}

func TestPlacementRevisionsTruncated(t *testing.T) {
	q := placementQuery(11)
	targets := func(n int) []*TargetRecord {
		var out []*TargetRecord
		for i := 1; i <= n; i++ {
			// Two targets per revision: still one distinct revision.
			k := RevisionKey(fmt.Sprintf("svc@%d", i))
			out = append(out, &TargetRecord{Environment: "prod", ContractRevision: k}, &TargetRecord{Environment: "prod", ContractRevision: k})
		}
		return out
	}
	it := q.placement(targets(11)).Items[0]
	if len(it.Revisions) != MaxPlacementRevisions || it.RevisionsTotal != 11 || !it.RevisionsTruncated || it.Revisions[0].Key != "svc@11" || it.Revisions[7].Key != "svc@4" {
		t.Errorf("11 revisions: %d listed, total %d, truncated %v, first %+v", len(it.Revisions), it.RevisionsTotal, it.RevisionsTruncated, it.Revisions[0])
	}
	it = q.placement(targets(3)).Items[0]
	if len(it.Revisions) != 3 || it.RevisionsTotal != 3 || it.RevisionsTruncated || it.Targets != 6 {
		t.Errorf("3 revisions: %+v", it)
	}
}

func TestPlacementRestricted(t *testing.T) {
	snap := &FleetSnapshot{
		Services: map[ServiceKey]*ServiceRecord{"a": {Key: "a", Name: "a", Targets: []TargetKey{"ta", "tb"}}},
		Targets: map[TargetKey]*TargetRecord{
			"ta": {Key: "ta", Name: "a", Kind: SourceKindKubernetes, Scope: "shop", ServiceKey: "a", Environment: "prod"},
			"tb": {Key: "tb", Name: "b", Kind: SourceKindKubernetes, Scope: "secret", ServiceKey: "a", Environment: "prod"},
		},
	}
	count := func(q *Query) int {
		t.Helper()
		d, err := q.EntityDetail(KindService, "a")
		if err != nil {
			t.Fatal(err)
		}
		return d.Service.Placement.Items[0].Targets
	}
	if n := count(NewQuery(snap)); n != 2 {
		t.Errorf("visible: %d targets", n)
	}
	if n := count(NewQuery(snap).Restrict(func(ns string) bool { return ns != "secret" })); n != 1 {
		t.Errorf("restricted: %d targets, want the hidden one uncounted", n)
	}
}

func TestPlacementLine(t *testing.T) {
	cases := []struct {
		e    EnvironmentPlacement
		want string
	}{
		{EnvironmentPlacement{Environment: "prod", Targets: 2, Declared: 1, NotDeclared: 1, RevisionsTotal: 3, RevisionsTruncated: true,
			Revisions: []EntityRef{{Version: "1.2.0"}, {Label: "svc"}}},
			"prod: 2 targets, 1.2.0, svc and 1 more; 1 declared, 1 not declared, 0 unknown"},
		{EnvironmentPlacement{Targets: 1, Unknown: 1}, "(no environment label): 1 target, no known revision; 0 declared, 0 not declared, 1 unknown"},
	}
	for _, c := range cases {
		if got := c.e.Line(); got != c.want {
			t.Errorf("line = %q, want %q", got, c.want)
		}
	}
}

func TestPlacementHeading(t *testing.T) {
	if PlacementHeading(CompletenessComplete) != "Runs in (observed)" || PlacementHeading(CompletenessPartial) != "Runs in (observed, targets Pacto can see)" {
		t.Errorf("headings %q %q", PlacementHeading(CompletenessComplete), PlacementHeading(CompletenessPartial))
	}
}
