package fleet

import (
	"errors"
	"strings"
	"testing"
)

// replacedQuery is the cart workload, created three days ago, taken over by
// cart-svc through a Pacto created pactoHours ago.
func replacedQuery(t *testing.T, pactoHours int) *Query {
	t.Helper()
	return NewQuery(replaceSnap(t, hoursAgo(72), []RawTarget{cartTarget("cart-svc", hoursAgo(pactoHours))}))
}

func notFound(t *testing.T, what string, err error) {
	t.Helper()
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Errorf("%s: an old inferred key answers: %v", what, err)
	}
}

func TestGetServiceByReplacedKey(t *testing.T) {
	q := replacedQuery(t, 1)
	v, err := q.GetService(cartName)
	if err != nil {
		t.Fatal(err)
	}
	if v.Service.Key != NewServiceKey("cart-svc") || v.ReplacedFrom == nil || v.ReplacedFrom.From != NewServiceKey(cartName) {
		t.Fatalf("service %s replacedFrom %+v", v.Service.Key, v.ReplacedFrom)
	}
	if len(v.Replaces) != 1 || v.ReplacesTotal != 1 || v.Replaces[0].Target != NewTargetKey("shop", "kubernetes", "cart-svc") {
		t.Errorf("replaces %+v total %d", v.Replaces, v.ReplacesTotal)
	}
	direct, err := q.GetService("cart-svc")
	if err != nil || direct.ReplacedFrom != nil || len(direct.Replaces) != 1 {
		t.Errorf("by its own key: %+v %v", direct, err)
	}
}

func TestGraphRootByReplacedKey(t *testing.T) {
	g, err := replacedQuery(t, 1).Graph(GraphQuery{Service: cartName})
	if err != nil {
		t.Fatal(err)
	}
	if g.Root != NewServiceKey("cart-svc") || g.ReplacedFrom == nil || g.ReplacedFrom.To != g.Root {
		t.Errorf("root %s replacedFrom %+v", g.Root, g.ReplacedFrom)
	}
}

func TestEntityDetailReplacedFrom(t *testing.T) {
	d, err := replacedQuery(t, 1).EntityDetail(KindService, cartName)
	if err != nil {
		t.Fatal(err)
	}
	if d.Entity.Key != string(NewServiceKey("cart-svc")) || d.ReplacedFrom == nil || d.ReplacedFrom.At == nil {
		t.Errorf("entity %+v replacedFrom %+v", d.Entity, d.ReplacedFrom)
	}
	if len(d.Service.Replaces) != 1 || d.Service.ReplacesTotal != 1 {
		t.Errorf("replaces %+v total %d", d.Service.Replaces, d.Service.ReplacesTotal)
	}
}

func TestServiceDetailReplacesBounded(t *testing.T) {
	q := replacedQuery(t, 1)
	to := NewServiceKey("cart-svc")
	r := q.snap.Replacements[0]
	for i := range MaxDetailPreview + 2 {
		r.From = NewServiceKey(cartName + strings.Repeat("x", i+1))
		q.snap.Replacements = append(q.snap.Replacements, r)
	}
	shown, total := q.replacesOf(to)
	if len(shown) != MaxDetailPreview || total != MaxDetailPreview+3 {
		t.Errorf("shown %d total %d", len(shown), total)
	}
	d, err := q.EntityDetail(KindService, "cart-svc")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Service.Replaces) != MaxDetailPreview || d.Service.ReplacesTotal != MaxDetailPreview+3 {
		t.Errorf("detail %d of %d", len(d.Service.Replaces), d.Service.ReplacesTotal)
	}
}

func TestExplainByReplacedKeyNotFound(t *testing.T) {
	q := replacedQuery(t, 1)
	_, err := q.Explain(cartName)
	notFound(t, "explain", err)
	_, err = q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: cartName})
	notFound(t, "neighborhood", err)
	_, err = q.resolveService(cartName)
	notFound(t, "versions provider lookup", err)
	if al := attentionOf(t, q, AttentionFilter{Service: cartName}); al.Total != 0 {
		t.Errorf("attention for the old key answers for the declared service: %+v", al.Items)
	}
}

func TestAttentionInferredReplacedWithin24h(t *testing.T) {
	for _, tc := range []struct {
		hours int
		want  int
	}{{1, 1}, {25, 0}} {
		q := replacedQuery(t, tc.hours)
		var got []AttentionItem
		al := attentionOf(t, q, AttentionFilter{Category: categoryKnowledge})
		for _, it := range al.Items {
			if it.Code == attentionInferredReplaced {
				got = append(got, it)
			}
		}
		if len(got) != tc.want {
			t.Fatalf("%dh: items %+v", tc.hours, got)
		}
		if n := q.Overview().Summary.KnowledgeItems; n != al.Total {
			t.Errorf("%dh: tile %d list %d", tc.hours, n, al.Total)
		}
		if tc.want == 0 {
			continue
		}
		it := got[0]
		if it.Severity != severityInfo || it.Service != string(NewServiceKey("cart-svc")) ||
			it.Label != cartName+" was replaced by cart-svc" || !strings.Contains(it.Reason, "shop/kubernetes/cart-svc") {
			t.Errorf("item %+v", it)
		}
	}
}

func TestReplacedKeyHiddenIsNotFound(t *testing.T) {
	r := replacedQuery(t, 1).Restrict(func(ns string) bool { return ns != "shop" }, func(string, string) bool { return true })
	_, err := r.GetService(cartName)
	notFound(t, "get", err)
	_, err = r.Graph(GraphQuery{Service: cartName})
	notFound(t, "graph", err)
	_, err = r.EntityDetail(KindService, cartName)
	notFound(t, "detail", err)
}
