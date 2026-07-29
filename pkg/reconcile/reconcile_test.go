package reconcile

import (
	"reflect"
	"testing"
)

func TestReconcile_AllThreeStatesSortedAndSummarized(t *testing.T) {
	declared := []Declared{
		{Service: "web", Dependency: "payments", Required: true},
		{Service: "web", Dependency: "legacy", Required: false}, // declared, never observed
		{Service: "api", Dependency: "db", Required: true},
	}
	observed := []Observed{
		{Service: "web", Dependency: "payments", Count: 5},
		{Service: "web", Dependency: "payments", Count: 2}, // dup sums -> 7
		{Service: "api", Dependency: "db", Count: 3},
		{Service: "api", Dependency: "cache", Count: 1}, // observed, not declared
	}
	got := Reconcile(declared, observed)
	want := Report{
		Entries: []Entry{
			{Service: "api", Dependency: "cache", Status: StatusObservedNotDeclared, Count: 1},
			{Service: "api", Dependency: "db", Status: StatusMatched, Required: true, Count: 3},
			{Service: "web", Dependency: "legacy", Status: StatusDeclaredNotObserved, Required: false},
			{Service: "web", Dependency: "payments", Status: StatusMatched, Required: true, Count: 7},
		},
		Summary: Summary{Matched: 2, DeclaredNotObserved: 1, ObservedNotDeclared: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reconcile mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestReconcile_Empty(t *testing.T) {
	got := Reconcile(nil, nil)
	if got.Entries != nil {
		t.Errorf("expected nil entries, got %+v", got.Entries)
	}
	if (got.Summary != Summary{}) {
		t.Errorf("expected zero summary, got %+v", got.Summary)
	}
}

func TestReconcile_SortByDependencyWithinService(t *testing.T) {
	got := Reconcile(nil, []Observed{
		{Service: "s", Dependency: "z", Count: 1},
		{Service: "s", Dependency: "a", Count: 1},
	})
	if len(got.Entries) != 2 || got.Entries[0].Dependency != "a" || got.Entries[1].Dependency != "z" {
		t.Errorf("dependency sort wrong: %+v", got.Entries)
	}
}
