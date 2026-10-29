package fleet

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func TestCheckRange(t *testing.T) {
	for _, tt := range []struct{ constraint, version, want string }{
		{"", "1.0.0", RangeUnknown},
		{"^1.0.0", "", RangeUnknown},
		{"not a range", "1.0.0", RangeUnknown},
		{"^1.0.0", "not a version", RangeUnknown},
		{"^2.0.0", "2.3.0", RangeSatisfied},
		{"^2.0.0", "1.6.2", RangeUnsatisfied},
	} {
		if got := CheckRange(tt.constraint, tt.version); got != tt.want {
			t.Errorf("CheckRange(%q, %q) = %q, want %q", tt.constraint, tt.version, got, tt.want)
		}
	}
}

// rangeFleet is the evaluation's fleet in miniature: reporting asks ledger
// ^2.0.0 while the fleet holds ledger 1.6.2; orders asks payments with no range;
// audit asks fraud, which runs on a target and has no revision; billing asks
// tax, whose two revisions disagree about the range.
func rangeFleet(t *testing.T) *Query {
	t.Helper()
	svc := func(name, version string, deps ...contract.Dependency) RawRevision {
		c := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: name, Version: version, Owner: contract.Owner{Team: "t"}}, Dependencies: deps}
		return RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Digest: "sha256:" + name + version}
	}
	src := NewMemorySource("local", "local", &Collection{
		Revisions: []RawRevision{
			svc("ledger", "1.6.2"),
			svc("reporting", "1.0.0", contract.Dependency{Name: "ledger", Compatibility: "^2.0.0"}),
			svc("payments", "2.0.0"),
			svc("orders", "1.0.0", contract.Dependency{Name: "payments"}),
			svc("audit", "1.0.0", contract.Dependency{Name: "fraud", Compatibility: "^1.0.0"}),
			svc("tax", "1.0.0"), svc("tax", "2.0.0"),
			svc("billing", "1.0.0", contract.Dependency{Name: "tax", Compatibility: "^2.0.0"}),
			svc("shop", "1.0.0", contract.Dependency{Name: "payments", Compatibility: "^2.0.0"}),
		},
		Targets: []RawTarget{{Scope: "prod", Kind: "k8s", Name: "fraud", Service: "fraud", Compliance: StatusCompliant}},
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

func TestRelationshipRangeStatus(t *testing.T) {
	q := rangeFleet(t)
	got := map[string]string{}
	for _, r := range q.snap.Relationships {
		if r.Type == RelationshipDependency {
			got[string(r.FromService)+"->"+r.To] = r.RangeStatus
			if !r.Resolved {
				t.Errorf("%s -> %s should resolve", r.FromService, r.To)
			}
		}
	}
	want := map[string]string{
		"reporting->ledger": RangeUnsatisfied,
		"orders->payments":  "",
		"audit->fraud":      RangeUnknown,
		"billing->tax":      RangeUnknown,
		"shop->payments":    RangeSatisfied,
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s rangeStatus = %q, want %q", k, got[k], w)
		}
	}
}

// TestRangeUnsatisfiedNeedsAttention proves a resolved dependency outside its
// range reaches status, with no category selected and with its own category.
func TestRangeUnsatisfiedNeedsAttention(t *testing.T) {
	q := rangeFleet(t)
	for name, sq := range map[string]StatusQuery{"empty": {}, "own category": {UnsatisfiedRanges: true}} {
		var found *StatusItem
		res := q.Status(sq)
		for i := range res.Items {
			if res.Items[i].Code == LimitationRangeUnsatisfied {
				found = &res.Items[i]
			}
		}
		if found == nil {
			t.Fatalf("%s: no %s item in %+v", name, LimitationRangeUnsatisfied, res.Items)
		}
		if found.Name != "reporting" || !strings.Contains(found.Reason, "reporting accepts ledger ^2.0.0, but the fleet holds ledger 1.6.2") {
			t.Errorf("%s: item = %+v", name, *found)
		}
	}
	if res := q.Status(StatusQuery{UnresolvedDeps: true}); len(res.Items) != 0 {
		t.Errorf("another category must not report the range item: %+v", res.Items)
	}
}

// TestRangeUnsatisfiedExplainAndOverview: explain names the range, and the
// overview tallies it with its own entry point.
func TestRangeUnsatisfiedExplainAndOverview(t *testing.T) {
	q := rangeFleet(t)
	ex, err := q.Explain("reporting")
	if err != nil {
		t.Fatal(err)
	}
	if len(ex.Reasons) != 1 || ex.Reasons[0].Code != LimitationRangeUnsatisfied || ex.Reasons[0].Assertion.Name != "ledger" {
		t.Errorf("explain reasons = %+v", ex.Reasons)
	}

	ov := q.Overview()
	if ov.Summary.UnsatisfiedRanges != 1 || ov.Summary.ServicesNeedingAttention != 1 {
		t.Errorf("summary = %d unsatisfied, %d needing attention; want 1 and 1", ov.Summary.UnsatisfiedRanges, ov.Summary.ServicesNeedingAttention)
	}
	var tile bool
	for _, ep := range ov.EntryPoints {
		tile = tile || (ep.Category == categoryOutOfRange && ep.Count == 1)
	}
	if !tile {
		t.Errorf("no out-of-range entry point in %+v", ov.EntryPoints)
	}
}

// TestStatus_EmptyQueryMeansAll: no category selected reports every category,
// and an answer with nothing to report is an empty list, never null.
func TestStatus_EmptyQueryMeansAll(t *testing.T) {
	q := rangeFleet(t)
	if got, all := q.Status(StatusQuery{}), q.Status(StatusQuery{NeedsAttention: true}); len(got.Items) == 0 || len(got.Items) != len(all.Items) {
		t.Errorf("empty query reported %d items, the union %d", len(got.Items), len(all.Items))
	}
	empty, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("local", "local", &Collection{}))
	if err != nil {
		t.Fatal(err)
	}
	if res := NewQuery(empty).Status(StatusQuery{}); res.Items == nil {
		t.Error("items must be an empty list, not nil")
	}
}

// TestOwnerFromTargets: a service known only from targets (a fleet read from
// Kubernetes alone) takes its owner from what the observing source read from the
// running contract, and search and get carry it in one shape.
func TestOwnerFromTargets(t *testing.T) {
	orders := contract.Owner{Team: "orders", Contacts: []contract.OwnerContact{{Type: "email", Value: "o@x"}}}
	src := NewMemorySource("k8s", SourceKindKubernetes, &Collection{Targets: []RawTarget{
		{Scope: "prod", Kind: "k8s", Name: "orders", Service: "orders", Compliance: StatusCompliant, Owner: orders},
		{Scope: "test", Kind: "k8s", Name: "orders", Service: "orders", Compliance: StatusCompliant, Owner: orders},
		{Scope: "prod", Kind: "k8s", Name: "lonely", Service: "lonely", Compliance: StatusCompliant},
		{Scope: "prod", Kind: "k8s", Name: "split", Service: "split", Compliance: StatusCompliant, Owner: contract.Owner{Team: "a"}},
		{Scope: "test", Kind: "k8s", Name: "split", Service: "split", Compliance: StatusCompliant, Owner: contract.Owner{Team: "b"}},
	}})
	// A second source reports the same orders target without an owner: the
	// owner the first one read is kept.
	again := NewMemorySource("k8s-2", SourceKindKubernetes, &Collection{Targets: []RawTarget{
		{Scope: "prod", Kind: "k8s", Name: "orders", Service: "orders", Compliance: StatusCompliant},
	}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, again, src)
	if err != nil {
		t.Fatal(err)
	}
	q := NewQuery(snap)
	view, err := q.GetService("orders")
	if err != nil {
		t.Fatal(err)
	}
	if !view.Service.Owner.Equal(orders) {
		t.Errorf("get owner = %+v, want %+v", view.Service.Owner, orders)
	}
	hits, err := q.Search(SearchFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits.Services {
		switch h.Name {
		case "orders":
			if !h.Owner.Equal(orders) {
				t.Errorf("search owner = %+v, want %+v", h.Owner, orders)
			}
		case "lonely":
			if b, _ := json.Marshal(h); strings.Contains(string(b), `"owner"`) {
				t.Errorf("an undeclared owner is left out, never {}: %s", b)
			}
		}
	}
	if !hasLimitation(snap.Limitations, LimitationOwnerConflict) {
		t.Error("targets reporting two owners must raise OWNER_CONFLICT")
	}
}
