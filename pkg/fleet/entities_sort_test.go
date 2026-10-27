package fleet

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func TestNaturalLess(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"target-2", "target-10", true},
		{"target-10", "target-2", false},
		{"Target-1", "target-1", false},
		{"a", "b", true},
		{"ab", "a", false},
		{"a", "ab", true},
		{"x007", "x7", false},
		{"x7", "x007", false},
		{"x07a", "x7b", true},
		{"x12", "x13", true},
		{"1", "a", true},
	} {
		if got := naturalLess(c.a, c.b); got != c.want {
			t.Errorf("naturalLess(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestHealthRank(t *testing.T) {
	if healthRank(StatusInvalid) >= healthRank(StatusCompliant) {
		t.Error("Invalid must rank before Compliant")
	}
	if healthRank(string(SourceUnavailable)) >= healthRank(string(SourceAvailable)) {
		t.Error("an unavailable source must rank before an available one")
	}
	if healthRank("nonsense") <= healthRank(StatusNotEvaluated) {
		t.Error("an unknown status sorts last")
	}
}

func TestEntitiesSort(t *testing.T) {
	q := NewQuery(sortFixture())
	names := func(sort string) []string {
		l, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindSource}, Sort: sort})
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, e := range l.Entities {
			out = append(out, e.Label)
		}
		return out
	}
	if got := names(SortName); !equal(got, []string{"src-2", "src-10", "src-11"}) {
		t.Errorf("name order = %v", got)
	}
	if got := names(SortHealth); !equal(got, []string{"src-11", "src-2", "src-10"}) {
		t.Errorf("health order = %v", got)
	}
	_, err := q.Entities(EntityFilter{Sort: "size"})
	var iq *InvalidQueryError
	if !errors.As(err, &iq) || iq.Field != "sort" {
		t.Errorf("an unknown sort must be rejected, got %v", err)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sortFixture() *FleetSnapshot {
	return &FleetSnapshot{Sources: []SourceState{
		{ID: "src-10", Kind: "k8s", Status: SourceAvailable},
		{ID: "src-11", Kind: "k8s", Status: SourceUnavailable},
		{ID: "src-2", Kind: "k8s", Status: SourceAvailable},
	}}
}

// A list row says who owns a service and how many services an owner has, and
// the owners list can put the busiest owner first.
func TestEntitiesCarryOwnerAndCounts(t *testing.T) {
	rev := func(name, team string) RawRevision {
		return RawRevision{Bundle: &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.0",
			Service: contract.Service{Name: name, Version: "1.0.0", Owner: contract.Owner{Team: team}}}}, Digest: "sha256:" + name}
	}
	src := NewMemorySource("local", "local", &Collection{Revisions: []RawRevision{rev("a", "team-y"), rev("b", "team-x"), rev("c", "team-x"), rev("d", "team-w")}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	q := NewQuery(snap)
	owners, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindOwner}, Sort: SortServices})
	if err != nil {
		t.Fatal(err)
	}
	// Owners with as many services fall back to name order.
	if len(owners.Entities) != 3 || owners.Entities[0].Label != "team-x" || owners.Entities[0].Services != 2 ||
		owners.Entities[1].Label != "team-w" || owners.Entities[2].Label != "team-y" || owners.Entities[2].Services != 1 {
		t.Fatalf("owners = %+v", owners.Entities)
	}
	svcs, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindService}, Sort: SortName})
	if err != nil {
		t.Fatal(err)
	}
	if svcs.Entities[0].Owner != "team-y" || svcs.Entities[1].Owner != "team-x" {
		t.Fatalf("services = %+v", svcs.Entities)
	}
}

// Revisions of one service list newest version first, not in digest order;
// other services and other kinds keep key order.
func TestSortEntityRefs_RevisionsNewestVersionFirst(t *testing.T) {
	refs := []EntityRef{
		{Kind: KindRevision, Key: "a@1", ParentService: "a", Version: "1.10.0"},
		{Kind: KindRevision, Key: "b@0", ParentService: "b", Version: "9.0.0"},
		{Kind: KindRevision, Key: "a@2", ParentService: "a", Version: "1.9.0"},
		{Kind: KindRevision, Key: "a@0", ParentService: "a", Version: "2.0.0"},
		{Kind: KindRevision, Key: "a@3", ParentService: "a", Version: "2.0.0"},
		{Kind: KindService, Key: "a"},
	}
	sortEntityRefs(refs)
	var got []string
	for _, r := range refs {
		got = append(got, r.Key)
	}
	want := "a@0 a@3 a@1 a@2 b@0 a"
	if s := strings.Join(got, " "); s != want {
		t.Errorf("order = %s, want %s", s, want)
	}
}
