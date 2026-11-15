package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/evidence"
)

// usageReport is a fresh, signed usage report for orders 1.3.0's db in
// production.
func usageReport(mods ...func(*RawUsage)) RawUsage {
	at := fixedNow().Add(-time.Hour)
	r := RawUsage{
		Service: "orders", Domain: acme, Digest: testDigest(acme, "orders", "1.3.0"), Kind: "configuration", Name: "db",
		Environment: "production", Provider: "aws", ExternalRef: "orders-db",
		Metrics: []evidence.UsageMetric{{Name: "storage-used", Value: 31, Unit: "Gi"}},
		Cost:    &evidence.UsageCost{Amount: "212.40", Currency: "USD", Period: "P30D"},
		Outcome: "Observed", Producer: "adapter", ProducerKeyID: "k1", ObservedAt: at, AcceptedAt: &at, Verified: true,
	}
	for _, m := range mods {
		m(&r)
	}
	return r
}

func usageSnap(t *testing.T, window time.Duration, base *Collection, reports ...[]RawUsage) *FleetSnapshot {
	t.Helper()
	sources := []Source{NewMemorySource("oci", "oci", base)}
	for i, recs := range reports {
		sources = append(sources, NewMemorySource(fmt.Sprintf("evidence-%d", i), "evidence-http", &Collection{Usage: recs}))
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, FreshnessWindow: window}, sources...)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// consumption is the db configuration use's consumption of orders.
func consumption(t *testing.T, q *Query) ([]ResourceConsumption, []Limitation) {
	t.Helper()
	u, lims := use(t, q, "orders", "postgresql", RelationshipConfigRef)
	return u.Consumption, lims
}

func one(t *testing.T, cs []ResourceConsumption) ResourceConsumption {
	t.Helper()
	if len(cs) != 1 {
		t.Fatalf("consumption = %+v", cs)
	}
	return cs[0]
}

func TestUsageReportedWhenSignedFresh(t *testing.T) {
	cs, _ := consumption(t, NewQuery(usageSnap(t, 0, ordersFleet(), []RawUsage{usageReport()})))
	c := one(t, cs)
	want := &UsageFigures{Metrics: usageReport().Metrics, Cost: usageReport().Cost}
	if c.State != consumptionReported || c.Environment != "production" || !c.Verified || c.Reported != nil || c.Provenance != ProvenanceObserved ||
		c.Provider != "aws" || c.ExternalRef != "orders-db" || c.Producer != "adapter" || c.ProducerKeyID != "k1" || c.Source != "evidence-0" ||
		c.AsOf == nil || c.Reason != "" || fmt.Sprint(c.Figures.Metrics, *c.Figures.Cost) != fmt.Sprint(want.Metrics, *want.Cost) {
		t.Errorf("consumption = %+v", c)
	}
}

func TestUsageUnsignedIsUnknown(t *testing.T) {
	cs, _ := consumption(t, NewQuery(usageSnap(t, 0, ordersFleet(), []RawUsage{usageReport(func(r *RawUsage) { r.Verified, r.AcceptedAt = false, nil })})))
	c := one(t, cs)
	if c.State != consumptionUnknown || c.Figures != nil || c.Reported == nil || c.Reported.Cost.Amount != "212.40" || c.Verified || !strings.Contains(c.Reason, "not signed") {
		t.Errorf("consumption = %+v", c)
	}
}

func TestUsageStaleIsUnknown(t *testing.T) {
	old := fixedNow().Add(-3 * time.Hour)
	snap := usageSnap(t, 2*time.Hour, ordersFleet(), []RawUsage{usageReport(func(r *RawUsage) { r.ObservedAt, r.AcceptedAt = old, &old })})
	c := one(t, must(consumption(t, NewQuery(snap))))
	if c.State != consumptionUnknown || c.Figures != nil || c.Reported == nil || !c.Stale || !strings.Contains(c.Reason, "older than") {
		t.Errorf("consumption = %+v", c)
	}
	if !snap.Usage[0].Stale || !snap.Usage[0].Verified || snap.Usage[0].Source != "evidence-0" {
		t.Errorf("raw record = %+v", snap.Usage[0])
	}
	// The window defaults to a day.
	day := fixedNow().Add(-25 * time.Hour)
	c = one(t, must(consumption(t, NewQuery(usageSnap(t, 0, ordersFleet(), []RawUsage{usageReport(func(r *RawUsage) { r.ObservedAt, r.AcceptedAt = day, &day })})))))
	if !c.Stale {
		t.Errorf("default window consumption = %+v", c)
	}
}

func must(cs []ResourceConsumption, _ []Limitation) []ResourceConsumption { return cs }

func TestUsageConflictIsUnknown(t *testing.T) {
	other := usageReport(func(r *RawUsage) {
		r.Producer, r.Metrics = "other", []evidence.UsageMetric{{Name: "storage-used", Value: 40, Unit: "Gi"}}
	})
	cs, lims := consumption(t, NewQuery(usageSnap(t, 0, ordersFleet(), []RawUsage{usageReport(), other})))
	c := one(t, cs)
	if c.State != consumptionUnknown || c.Figures != nil || c.Reported != nil || !slices.Equal(c.Conflict, []string{"adapter", "other"}) {
		t.Errorf("consumption = %+v", c)
	}
	if countLimitations(lims, LimitationUsageConflict) != 1 {
		t.Errorf("limitations = %+v", lims)
	}
	// Agreeing producers, metrics listed in another order, raise no conflict.
	two := []evidence.UsageMetric{{Name: "a", Value: 1}, {Name: "b", Value: 2}}
	agree := []RawUsage{
		usageReport(func(r *RawUsage) { r.Metrics = two }),
		usageReport(func(r *RawUsage) { r.Producer, r.Metrics = "other", []evidence.UsageMetric{two[1], two[0]} }),
	}
	if c := one(t, must(consumption(t, NewQuery(usageSnap(t, 0, ordersFleet(), agree))))); c.State != consumptionReported || c.Conflict != nil {
		t.Errorf("agreeing consumption = %+v", c)
	}
}

func TestUsageForeignRevisionIsUnknown(t *testing.T) {
	cs, _ := consumption(t, NewQuery(usageSnap(t, 0, ordersFleet(), []RawUsage{usageReport(func(r *RawUsage) { r.Digest = testDigest(acme, "orders", "1.2.0") })})))
	c := one(t, cs)
	if c.State != consumptionUnknown || c.Figures != nil || c.Reported == nil || c.Reason != "last usage report is for 1.2.0; 1.3.0 has no report yet" {
		t.Errorf("consumption = %+v", c)
	}
}

// An environment the current revision declares with no usage keeps
// not-reported beside the one a provider reported; a non-Observed outcome
// reads unknown with no figures.
func TestUsageNotReportedSlot(t *testing.T) {
	base := ordersFleet()
	cur := consumer("orders", "1.3.0", "postgresql")
	cur.Bundle.Contract.Environments = []contract.Environment{{Name: "production", File: "p.yaml"}, {Name: "staging", File: "s.yaml"}, {Name: "dev", File: "d.yaml"}}
	base.Revisions[2] = cur
	insufficient := usageReport(func(r *RawUsage) {
		r.Environment, r.Outcome, r.Provider, r.ExternalRef, r.Metrics, r.Cost = "dev", "Insufficient", "", "", nil, nil
	})
	cs, _ := consumption(t, NewQuery(usageSnap(t, 0, base, []RawUsage{usageReport(), insufficient})))
	var got []string
	for _, c := range cs {
		got = append(got, c.Environment+" "+c.State+" "+c.Reason+" "+fmt.Sprint(c.Figures != nil, c.Reported != nil))
	}
	want := []string{
		"dev unknown the provider reported insufficient false false",
		"production reported  true false",
		"staging not-reported " + consumptionNotReportedReason + " false false",
	}
	if !slices.Equal(got, want) {
		t.Errorf("consumption:\n got %q\nwant %q", got, want)
	}
}

// Two entries on one provider keep their own consumption, named by entry.
func TestUsageEntriesKeptApart(t *testing.T) {
	c := svc24("orders", "1.3.0", "")
	c.Configurations = []contract.Configuration{{Name: "primary", Ref: pgRef("postgresql")}, {Name: "replica", Ref: pgRef("postgresql")}}
	base := &Collection{Revisions: []RawRevision{provider("postgresql"), ociRev(acme, c, nil)}}
	cs, _ := consumption(t, NewQuery(usageSnap(t, 0, base, []RawUsage{usageReport(func(r *RawUsage) { r.Name = "replica" })})))
	var got []string
	for _, c := range cs {
		got = append(got, c.Environment+"/"+c.Entry+" "+c.State)
	}
	if want := []string{"/primary not-reported", "production/replica reported"}; !slices.Equal(got, want) {
		t.Errorf("consumption = %q, want %q", got, want)
	}
}

func TestUsageUnmatchedLimitation(t *testing.T) {
	stray := usageReport(func(r *RawUsage) { r.Name = "cache" })
	snap := usageSnap(t, 0, ordersFleet(), []RawUsage{usageReport(), stray})
	if len(snap.Usage) != 1 || countLimitations(snap.Limitations, LimitationUsageUnmatched) != 1 {
		t.Errorf("usage = %+v, limitations %+v", snap.Usage, snap.Limitations)
	}
}

// Invalid records are dropped with one counted limitation per source.
func TestUsageInvalidRecordsDropped(t *testing.T) {
	future := fixedNow().Add(time.Hour)
	bad := []RawUsage{
		usageReport(func(r *RawUsage) { r.Service = "" }),
		usageReport(func(r *RawUsage) { r.Kind = "secret" }),
		usageReport(func(r *RawUsage) { r.ObservedAt, r.AcceptedAt = future, nil }),
		usageReport(func(r *RawUsage) { r.Metrics, r.Cost = nil, nil }),
		usageReport(func(r *RawUsage) { r.Outcome = "Insufficient" }), // payload on a non-Observed outcome
		usageReport(func(r *RawUsage) {
			r.Outcome, r.Metrics, r.Cost, r.Provider, r.ExternalRef, r.Environment = "Insufficient", nil, nil, "", "", "Bad Env"
		}),
	}
	snap := usageSnap(t, 0, ordersFleet(), append(bad, usageReport()))
	if len(snap.Usage) != 1 {
		t.Errorf("usage = %+v", snap.Usage)
	}
	for _, l := range snap.Limitations {
		if l.Code == LimitationSourceRecordInvalid && l.Message == "6 usage records were invalid and dropped" {
			return
		}
	}
	t.Errorf("limitations = %+v", snap.Limitations)
}

func TestUsageTruncated(t *testing.T) {
	base := ordersFleet()
	base.Revisions = append(base.Revisions, consumer("billing", "1.0.0", "postgresql"))
	base.Targets = []RawTarget{
		{Scope: "open", Kind: SourceKindKubernetes, Name: "orders", Service: "orders", Domain: acme, Digest: testDigest(acme, "orders", "1.3.0")},
		{Scope: "secret", Kind: SourceKindKubernetes, Name: "billing", Service: "billing", Domain: acme, Digest: testDigest(acme, "billing", "1.0.0")},
	}
	recs := make([]RawUsage, 0, MaxUsageRecords+2)
	for i := range MaxUsageRecords {
		at := fixedNow().Add(-time.Duration(i+1) * time.Millisecond)
		recs = append(recs, usageReport(func(r *RawUsage) {
			r.Service, r.Digest, r.ObservedAt, r.AcceptedAt = "billing", testDigest(acme, "billing", "1.0.0"), at, &at
		}))
	}
	for i := range 2 {
		at := fixedNow().Add(-time.Hour - time.Duration(i)*time.Millisecond)
		recs = append(recs, usageReport(func(r *RawUsage) { r.ObservedAt, r.AcceptedAt = at, &at }))
	}
	q := NewQuery(usageSnap(t, 0, base, recs))
	msgs := func(q *Query) []string {
		var out []string
		for _, l := range q.snap.Limitations {
			if l.Code == LimitationUsageTruncated {
				out = append(out, l.Message)
			}
		}
		return out
	}
	if got := msgs(q); len(q.snap.Usage) != MaxUsageRecords || len(got) != 1 || !strings.HasPrefix(got[0], "10002 usage reports exceed") {
		t.Errorf("unrestricted = %q (%d kept)", got, len(q.snap.Usage))
	}
	r := q.Restrict(func(ns string) bool { return ns != "secret" })
	if got := msgs(r); len(got) != 1 || !strings.HasPrefix(got[0], "2 usage reports about the services you can see") {
		t.Errorf("restricted = %q", got)
	}
}

func TestUsageRestrictedToVisibleConsumers(t *testing.T) {
	billing := func(r *RawUsage) {
		r.Service, r.Digest, r.ExternalRef, r.Producer = "billing", testDigest(acme, "billing", "1.0.0"), "billing-db-secret-ref", "billing-adapter"
	}
	base := ordersFleet()
	base.Revisions = append(base.Revisions, consumer("billing", "1.0.0", "postgresql"))
	base.Targets = []RawTarget{
		{Scope: "open", Kind: SourceKindKubernetes, Name: "orders", Service: "orders", Domain: acme, Digest: testDigest(acme, "orders", "1.3.0")},
		{Scope: "secret", Kind: SourceKindKubernetes, Name: "billing", Service: "billing", Domain: acme, Digest: testDigest(acme, "billing", "1.0.0")},
	}
	snap := usageSnap(t, 0, base, []RawUsage{usageReport(), usageReport(billing)})
	// An orphaned usage report alone makes Restrict rebuild the view.
	snap.Usage = append(snap.Usage, UsageRecord{ServiceKey: NewServiceKeyDomain(acme, "ghost"), Service: "ghost", Producer: "ghost-adapter"})
	if keepsEveryName(snap, false) {
		t.Error("an orphaned usage report reads as visible")
	}
	r := NewQuery(snap).Restrict(func(ns string) bool { return ns != "secret" })
	if c := one(t, must(consumption(t, r))); c.State != consumptionReported {
		t.Errorf("orders consumption = %+v", c)
	}
	data, err := json.Marshal(r.SnapshotForSerialization())
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"billing-db-secret-ref", "billing-adapter", "ghost", testDigest(acme, "billing", "1.0.0")} {
		if strings.Contains(string(data), leak) {
			t.Errorf("restricted snapshot leaks %q", leak)
		}
	}
	if got := r.SnapshotForSerialization().Usage; len(got) != 1 || got[0].Service != "orders" {
		t.Errorf("usage = %+v", got)
	}
}

// Usage is lifecycle data about a resource: it never moves the consumer's
// status or the overview counts.
func TestUsageNeverChangesStatus(t *testing.T) {
	base := func() *Collection {
		c := ordersFleet()
		c.Targets = []RawTarget{{Scope: "shop", Kind: SourceKindKubernetes, Name: "orders", Service: "orders", Domain: acme,
			Digest: testDigest(acme, "orders", "1.3.0"), Compliance: StatusCompliant}}
		return c
	}
	state := func(q *Query) string {
		s := q.Overview().Summary
		att, err := q.Attention(AttentionFilter{})
		if err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("%v %d %d %d %d", q.snap.Services[NewServiceKeyDomain(acme, "orders")].Status, s.CompliantTargets, s.NonCompliantTargets, s.UnknownTargets, len(att.Items))
	}
	without := state(NewQuery(usageSnap(t, 0, base())))
	conflict := usageReport(func(r *RawUsage) {
		r.Producer, r.Cost = "other", &evidence.UsageCost{Amount: "1", Currency: "EUR", Period: "P1D"}
	})
	if got := state(NewQuery(usageSnap(t, 0, base(), []RawUsage{usageReport(), conflict}))); got != without {
		t.Errorf("usage changed the status: %q, want %q", got, without)
	}
}

// sortUsage orders records that differ only in their figures by the figures.
func TestSortUsageByFigures(t *testing.T) {
	at := fixedNow()
	a := UsageRecord{ObservedAt: at, Metrics: []evidence.UsageMetric{{Name: "b", Value: 2}}}
	b := UsageRecord{ObservedAt: at, Metrics: []evidence.UsageMetric{{Name: "a", Value: 1}}}
	recs := []UsageRecord{a, b}
	sortUsage(recs)
	if recs[0].Metrics[0].Name != "a" {
		t.Errorf("order = %+v", recs)
	}
}
