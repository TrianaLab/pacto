package fleet

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// undeclaredFleet: a declares b and calls b and c at runtime; d calls b with no
// declaration; an uncontracted Kubernetes caller and an ambiguous "pay" (two
// domains) call b. a, b and d run in shop, c in secret.
func undeclaredFleet(t *testing.T) *Query {
	t.Helper()
	mk := func(name, domain string, deps ...string) RawRevision {
		var d []contract.Dependency
		for _, dep := range deps {
			d = append(d, contract.Dependency{Name: dep, Ref: "oci://x/" + dep, Required: true})
		}
		c := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: name, Version: "1.0.0", Owner: contract.Owner{Team: "t"}}, Dependencies: d, Readiness: readyContract()}
		return RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Domain: domain, Digest: "sha256:" + domain + name}
	}
	k := SourceKindKubernetes
	src := NewMemorySource("k8s", "kubernetes", &Collection{
		Revisions: []RawRevision{mk("a", "", "b"), mk("b", ""), mk("c", ""), mk("d", ""), mk("pay", "eu"), mk("pay", "us")},
		Targets: []RawTarget{
			{Scope: "shop", Kind: k, Name: "a", Service: "a", Compliance: StatusCompliant},
			{Scope: "shop", Kind: k, Name: "b", Service: "b", Compliance: StatusCompliant},
			{Scope: "secret", Kind: k, Name: "c", Service: "c", Compliance: StatusCompliant},
			{Scope: "shop", Kind: k, Name: "d", Service: "d", Compliance: StatusCompliant},
		},
		Observed: []ObservedEdge{
			{From: "a", To: "b", Count: 1},
			{From: "a", To: "c", Count: 1},
			{From: "d", To: "b", Count: 1},
			{From: "k8s:secret/Deployment/x", To: "b", Count: 1},
			{From: "pay", To: "b", Count: 1},
		},
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

func attentionOf(t *testing.T, q *Query, f AttentionFilter) *AttentionList {
	t.Helper()
	f.Limit = 1000
	al, err := q.Attention(f)
	if err != nil {
		t.Fatal(err)
	}
	return al
}

func TestStatusCoversEveryAttentionCategory(t *testing.T) {
	flag := map[string]func(*StatusQuery){
		categoryNonCompliant: func(s *StatusQuery) { s.NonCompliant = true },
		categoryInvalid:      func(s *StatusQuery) { s.Invalid = true },
		categoryUnknown:      func(s *StatusQuery) { s.Unknown = true },
		categoryStale:        func(s *StatusQuery) { s.StaleEvidence = true },
		categoryUnresolved:   func(s *StatusQuery) { s.UnresolvedDeps = true },
		categoryOutOfRange:   func(s *StatusQuery) { s.UnsatisfiedRanges = true },
		categoryUndeclared:   func(s *StatusQuery) { s.Undeclared = true },
		categoryReadiness:    func(s *StatusQuery) { s.MissingReadiness = true },
		categoryUnowned:      func(s *StatusQuery) { s.Unowned = true },
	}
	fleets := []*Query{productFleet(t), rangeFleet(t), undeclaredFleet(t), domainFleet(t)}
	for _, c := range AttentionCategoriesList() {
		set, ok := flag[c]
		if !ok {
			t.Fatalf("category %q has no status flag", c)
		}
		found := false
		for _, q := range fleets {
			al := attentionOf(t, q, AttentionFilter{Category: c})
			if al.Total == 0 {
				continue
			}
			found = true
			codes := map[string]bool{}
			for _, it := range al.Items {
				codes[it.Code] = true
			}
			all := q.Status(StatusQuery{Limit: 1000})
			if !slices.ContainsFunc(all.Items, func(it StatusItem) bool { return codes[it.Code] }) {
				t.Errorf("category %q: an empty status query omits it", c)
			}
			var sq StatusQuery
			sq.Limit = 1000
			set(&sq)
			only := q.Status(sq)
			if len(only.Items) != al.Total {
				t.Errorf("category %q: status returned %d items, attention %d", c, len(only.Items), al.Total)
			}
			for _, it := range only.Items {
				if !codes[it.Code] {
					t.Errorf("category %q: status flag returned %s", c, it.Code)
				}
			}
		}
		if !found {
			t.Errorf("no fixture produces category %q", c)
		}
	}
	if !slices.ContainsFunc(domainFleet(t).Status(StatusQuery{}).Items, func(it StatusItem) bool { return it.Code == "UNOWNED_SERVICE" }) {
		t.Error("UNOWNED_SERVICE missing from an empty status query")
	}
}

func TestStatusReportsTruncation(t *testing.T) {
	var revs []RawRevision
	for i := range 250 {
		name := fmt.Sprintf("svc-%03d", i)
		c := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: name, Version: "1.0.0", Owner: contract.Owner{Team: "t"}}}
		revs = append(revs, RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Digest: "sha256:" + name})
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("local", "local", &Collection{Revisions: revs}))
	if err != nil {
		t.Fatal(err)
	}
	q := NewQuery(snap)
	res := q.Status(StatusQuery{})
	if len(res.Items) != DefaultStatusLimit || res.Total != 250 || !res.Truncated {
		t.Errorf("default limit: items=%d total=%d truncated=%v", len(res.Items), res.Total, res.Truncated)
	}
	res = q.Status(StatusQuery{Limit: 300})
	if len(res.Items) != 250 || res.Total != 250 || res.Truncated {
		t.Errorf("limit 300: items=%d total=%d truncated=%v", len(res.Items), res.Total, res.Truncated)
	}
}

func TestUndeclaredAttentionMatchesOverviewTile(t *testing.T) {
	q := undeclaredFleet(t)
	al := attentionOf(t, q, AttentionFilter{Category: categoryUndeclared})
	sum := q.Overview().Summary
	if al.Total != 4 || sum.ObservedOnlyRelationships+sum.UncontractedCallers != 4 {
		t.Fatalf("total=%d observedOnly=%d callers=%d", al.Total, sum.ObservedOnlyRelationships, sum.UncontractedCallers)
	}
	var tile *EntryPoint
	for _, ep := range entryPoints(&sum) {
		if ep.Category == categoryUndeclared {
			tile = &ep
		}
	}
	if tile == nil || tile.Count != al.Total {
		t.Fatalf("tile = %+v, want count %d", tile, al.Total)
	}
	byLabel := map[string]AttentionItem{}
	for _, it := range al.Items {
		byLabel[it.Label] = it
	}
	want := []AttentionItem{
		{Entity: q.serviceRef("a"), Service: "a", Label: "a calls c", Severity: severityWarning, Code: "DEPENDENCY_UNDECLARED", Category: categoryUndeclared,
			Summary: "Runtime call with no declared dependency", Reason: "observed calls from a to c, and no contract revision of a declares c",
			NextStep: "Declare c in a's contract, or find out why a calls it"},
		{Entity: q.serviceRef("d"), Service: "d", Label: "d calls b", Severity: severityWarning, Code: "DEPENDENCY_UNDECLARED", Category: categoryUndeclared,
			Summary: "Runtime call with no declared dependency", Reason: "observed calls from d to b, and no contract revision of d declares b",
			NextStep: "Declare b in d's contract, or find out why d calls it"},
		{Entity: q.serviceRef("b"), Service: "b", Label: "k8s:secret/Deployment/x calls b", Severity: severityInfo, Code: "UNCONTRACTED_CALLER", Category: categoryUndeclared,
			Summary: "Caller with no contract", Reason: "k8s:secret/Deployment/x calls b and no contract describes k8s:secret/Deployment/x",
			NextStep: "Give k8s:secret/Deployment/x a contract, or find out why it calls b"},
		{Entity: q.serviceRef("b"), Service: "b", Label: "pay calls b", Severity: severityInfo, Code: "UNCONTRACTED_CALLER", Category: categoryUndeclared,
			Summary: "Caller matches more than one service", Reason: "pay matches more than one service, so Pacto cannot tell which one calls b",
			NextStep: "Make pay's name match one contracted service"},
	}
	for _, w := range want {
		if got := byLabel[w.Label]; fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", w) {
			t.Errorf("item %q:\n got %+v\nwant %+v", w.Label, got, w)
		}
	}
	var s ServiceSummary
	for _, key := range []ServiceKey{"a", "d"} {
		s = ServiceSummary{}
		q.tallyServiceEdges(&s, key)
		if s.ObservedNotDeclared != 1 {
			t.Errorf("%s observedNotDeclared = %d, want 1", key, s.ObservedNotDeclared)
		}
	}
}

func TestObservedOnlyCountsUnchanged(t *testing.T) {
	q := undeclaredFleet(t)
	want := map[ServiceKey]int{"a": 1, "b": 0, "c": 0, "d": 1}
	for k, n := range want {
		if got := len(q.observedOnly(k)); got != n {
			t.Errorf("observedOnly(%s) = %d, want %d", k, got, n)
		}
	}
	if got := len(q.observedOnly("")); got != 2 {
		t.Errorf("observedOnly(all) = %d, want 2", got)
	}
	// restrictFleet: web declares and calls api; hiding secret hides api, so
	// web's declared edge becomes hidden and its observed call goes with it.
	r := restrictFleet(t)
	if got := len(r.observedOnly("")); got != 1 { // api -> reg
		t.Errorf("restrictFleet observedOnly(all) = %d, want 1", got)
	}
	rv := r.Restrict(func(ns string) bool { return ns != "secret" })
	for _, k := range []ServiceKey{"web", "db", "reg", "lone"} {
		var s ServiceSummary
		rv.tallyServiceEdges(&s, k)
		if got := len(rv.observedOnly(k)); got != 0 || s.ObservedNotDeclared != 0 {
			t.Errorf("restricted %s: observedOnly=%d observedNotDeclared=%d", k, got, s.ObservedNotDeclared)
		}
	}
}
