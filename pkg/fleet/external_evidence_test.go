package fleet

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestExternalDetailListsInferredAndObservedSources(t *testing.T) {
	snap := inferredFleet(t, []inferredSvc{{name: "checkout", ns: []string{"shop"}}}, stripeUses(),
		[]ObservedEdge{{From: "checkout", To: "api.stripe.com", Count: 4}}, nil)
	q := NewQuery(snap)
	d, err := q.EntityDetail(KindExternal, "stripe")
	if err != nil {
		t.Fatal(err)
	}
	ev := d.External.ExternalEvidence
	if len(ev.InferredSources) != 2 || ev.InferredSourcesTotal != 2 || len(ev.ObservedSources) != 1 || ev.ObservedSourcesTotal != 1 {
		t.Fatalf("evidence = %+v", ev)
	}
	if env := ev.InferredSources[0]; env != wantEnvSource(env) || env.ObservedAt == nil || env.Target == "" {
		t.Errorf("env source = %+v", env)
	}
	if o := ev.ObservedSources[0]; o.Service != "checkout" || o.Source != "k8s" || o.Count != 4 {
		t.Errorf("observed source = %+v", o)
	}
	v, _ := q.GetExternal("stripe")
	if len(v.InferredSources) != 2 || len(v.ObservedSources) != 1 {
		t.Errorf("get = %+v", v.ExternalEvidence)
	}
	checkInferredReasons(t, q)
}

// wantEnvSource is checkout's env evidence for stripe, read at got's time.
func wantEnvSource(got ExternalInferredSource) ExternalInferredSource {
	u := stripeUses()[1]
	return ExternalInferredSource{Service: "checkout", InferredSource: InferredSource{Source: "k8s", Target: got.Target, Kind: u.Kind, Where: u.Where,
		Host: u.Host, Confidence: u.Confidence, Entry: u.Entry, Catalog: u.Catalog, ObservedAt: got.ObservedAt}}
}

// checkInferredReasons: explain gives one reason per inferred source, with
// its kind, where, confidence, host and entry.
func checkInferredReasons(t *testing.T, q *Query) {
	t.Helper()
	ex, _ := q.Explain("external:stripe")
	var inferred []string
	for _, r := range ex.Reasons {
		if r.Code == ReasonExternalInferred {
			inferred = append(inferred, r.Message)
		}
	}
	want := "inferred from env deployment/checkout env STRIPE_API_BASE of checkout (likely, host api.stripe.com, entry stripe"
	if len(inferred) != 2 || !slices.ContainsFunc(inferred, func(m string) bool { return strings.HasPrefix(m, want) }) {
		t.Errorf("inferred reasons = %q", inferred)
	}
}

func TestExternalEvidenceBounded(t *testing.T) {
	var uses []RawExternalUse
	for i := range MaxExternalSources + 7 {
		u := stripeUses()[1]
		u.Where = fmt.Sprintf("deployment/checkout env STRIPE_%03d", i)
		uses = append(uses, u)
	}
	d, err := NewQuery(inferredFleet(t, []inferredSvc{{name: "checkout", ns: []string{"shop"}}}, uses, nil, nil)).EntityDetail(KindExternal, "stripe")
	if err != nil {
		t.Fatal(err)
	}
	if ev := d.External.ExternalEvidence; len(ev.InferredSources) != MaxExternalSources || ev.InferredSourcesTotal != MaxExternalSources+7 || ev.ObservedSources != nil {
		t.Errorf("bounded evidence = %d of %d, observed %v", len(ev.InferredSources), ev.InferredSourcesTotal, ev.ObservedSources)
	}
}

func TestExternalEvidenceRestrictedDropsHiddenCaller(t *testing.T) {
	svcs := []inferredSvc{{name: "checkout", ns: []string{"shop"}}, {name: "billing", ns: []string{"secret"}}}
	uses := append(stripeUses(), RawExternalUse{Service: "billing", Namespace: "secret", Identity: "stripe", Confidence: "likely", Kind: "env",
		Where: "deployment/billing env STRIPE_URL", Host: "api.stripe.com", Entry: "stripe"})
	snap := inferredFleet(t, svcs, uses, []ObservedEdge{{From: "billing", To: "api.stripe.com", Count: 1}}, nil)
	q := NewQuery(snap).Restrict(func(ns string) bool { return ns == "shop" }, nil)
	d, err := q.EntityDetail(KindExternal, "stripe")
	if err != nil {
		t.Fatal(err)
	}
	ev := d.External.ExternalEvidence
	if d.External.External.HiddenConsumers != 1 || ev.InferredSourcesTotal != 2 || len(ev.ObservedSources) != 0 {
		t.Errorf("restricted = hidden %d evidence %+v", d.External.External.HiddenConsumers, ev)
	}
	for _, s := range ev.InferredSources {
		if s.Service != "checkout" {
			t.Errorf("hidden caller's evidence leaked: %+v", s)
		}
	}
}
