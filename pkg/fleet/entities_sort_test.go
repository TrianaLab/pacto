package fleet

import (
	"errors"
	"testing"
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
	if !(healthRank(StatusInvalid) < healthRank(StatusCompliant)) {
		t.Error("Invalid must rank before Compliant")
	}
	if !(healthRank(string(SourceUnavailable)) < healthRank(string(SourceAvailable))) {
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
