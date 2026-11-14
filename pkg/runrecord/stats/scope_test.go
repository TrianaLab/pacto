package stats

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
)

var fleetNow = time.Date(2026, 11, 14, 11, 0, 0, 0, time.UTC)

// statsFleet: shop/orders (team checkout, commerce) and catalog (no owner,
// commerce) and ghost (no contract, domain unknown) run in shop;
// payments/orders (team money, finance) and billing (DRI alice, no domain)
// run in vault. The bare name orders is ambiguous.
func statsFleet(t *testing.T) *fleet.Query {
	t.Helper()
	rev := func(name, domain string, owner contract.Owner, business string) fleet.RawRevision {
		c := &contract.Contract{PactoVersion: "2.4", Service: contract.Service{Name: name, Version: "1.0.0", Owner: owner, Domain: business}}
		return fleet.RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Digest: "sha256:" + domain + name, Domain: domain}
	}
	target := func(ns, name, domain string) fleet.RawTarget {
		return fleet.RawTarget{Scope: ns, Kind: fleet.SourceKindKubernetes, Name: name, Service: name, Domain: domain, Compliance: fleet.StatusCompliant}
	}
	src := fleet.NewMemorySource("k8s", "kubernetes", &fleet.Collection{
		Revisions: []fleet.RawRevision{
			rev("orders", "shop", contract.Owner{Team: "checkout"}, "commerce"),
			rev("orders", "payments", contract.Owner{Team: "money"}, "finance"),
			rev("billing", "", contract.Owner{DRI: "alice"}, ""),
			rev("catalog", "", contract.Owner{}, "commerce"),
		},
		Targets: []fleet.RawTarget{
			target("shop", "orders", "shop"), target("vault", "orders", "payments"), target("vault", "billing", ""),
			target("shop", "catalog", ""), target("shop", "ghost", ""),
		},
	})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return fleetNow }}, src)
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

func shopOnly(ns string) bool { return ns == "shop" }

func restrictedScope(t *testing.T) Scope {
	full := statsFleet(t)
	return Scope{Restricted: true, Place: FleetPlacement(full, full.Restrict(shopOnly)), OperationVisible: shopOnly}
}

func unrestrictedScope(t *testing.T) Scope {
	return Scope{Place: FleetPlacement(statsFleet(t), nil)}
}

// of moves r to service, keeping a CI record's id consistent with it.
func of(service string, r runrecord.Record) runrecord.Record {
	r.Service = service
	if r.Kind != runrecord.KindOperation {
		r.ID = runrecord.ID(r.Kind, service, r.OldRevision, r.NewRevision)
	}
	return r
}

func diffOf(service string) runrecord.Record {
	return of(service, rec(runrecord.KindDiff, "sha256:new", hoursAgo(2)))
}

func aggBy(s RecordSet, by string, sc Scope) *Stats {
	return Aggregate(s, Query{Window: aggWindow, By: by}, sc, aggNow)
}

func labels(gs []Group) string {
	var out []string
	for _, g := range gs {
		out = append(out, g.Label)
	}
	return strings.Join(out, ",")
}

func hasLimitation(st *Stats, code string) bool {
	for _, l := range st.Limitations {
		if l.Code == code {
			return true
		}
	}
	return false
}

func TestGroupByServiceOwnerDomainRepositoryEnvironment(t *testing.T) {
	orders := of("shop/orders", rec(runrecord.KindImpact, "sha256:new", hoursAgo(2)))
	orders.Repository = "github.com/acme/shop"
	billing := diffOf("billing")
	billing.Environment = "staging"
	catalog := diffOf("catalog")
	catalog.Repository = "github.com/acme/shop"
	s := set(orders, billing, catalog, diffOf("ghost"), of("catalog", promote(delivery.Succeeded, delivery.Verified, hoursAgo(1))), diffOf("nobody"))
	for by, want := range map[string]string{
		"service":     "billing,catalog,ghost,nobody,shop/orders",
		"owner":       "alice,checkout,unknown,unowned",
		"domain":      "commerce,none,unknown",
		"repository":  "github.com/acme/shop,unknown",
		"environment": "production,staging,none",
	} {
		st := aggBy(s, by, unrestrictedScope(t))
		if got := labels(st.Groups); got != want {
			t.Errorf("by %s = %s, want %s", by, got, want)
		}
		if st.Runs != 6 || st.Hidden.Present {
			t.Errorf("by %s: runs %d hidden %v", by, st.Runs, st.Hidden.Present)
		}
	}
	if st := aggBy(s, "", unrestrictedScope(t)); st.Groups != nil {
		t.Fatalf("no by = %+v", st.Groups)
	}
}

func TestGroupByOwnerIsInferred(t *testing.T) {
	full := statsFleet(t)
	snap := &Attribution{AsOf: fleetNow, SnapshotID: full.SnapshotID()}
	sc := Scope{Place: FleetPlacement(full, nil), Snapshot: snap}
	s := set(diffOf("shop/orders"))
	for _, by := range []string{"owner", "domain"} {
		a := aggBy(s, by, sc).Attribution
		if a == nil || a.Provenance != "inferred" || !a.AsOf.Equal(fleetNow) || a.SnapshotID == "" || a.SnapshotID != full.SnapshotID() {
			t.Fatalf("by %s attribution = %+v", by, a)
		}
	}
	if snap.Provenance != "" {
		t.Fatal("Aggregate wrote through the caller's attribution")
	}
	if a := aggBy(s, "service", sc).Attribution; a != nil {
		t.Fatalf("by service attribution = %+v", a)
	}
	g := aggBy(s, "owner", sc).Groups
	if len(g) != 1 || g[0].Key != "team:checkout" || g[0].Label != "checkout" {
		t.Fatalf("owner group = %+v", g)
	}
}

func TestGroupByDomainUnknownDomain(t *testing.T) {
	g := aggBy(set(diffOf("ghost"), diffOf("billing")), "domain", unrestrictedScope(t)).Groups
	if len(g) != 2 || g[0].Label != "none" || g[1].Label != "unknown" || g[0].Key == "none" || g[1].Key == "unknown" {
		t.Fatalf("groups = %+v", g)
	}
}

func TestGroupByRepositoryMissingIsUnknown(t *testing.T) {
	r := diffOf("shop/orders")
	r.Repository = "unknown"
	g := aggBy(set(r, diffOf("catalog")), "repository", Scope{}).Groups
	// A repository literally named unknown stays apart from the missing ones.
	if len(g) != 2 || g[0].Key != "unknown" || g[1].Label != "unknown" || g[1].Key == "unknown" {
		t.Fatalf("groups = %+v", g)
	}
}

func TestUnrestrictedUnknownServiceGroupsAsUnknown(t *testing.T) {
	for _, sc := range []Scope{unrestrictedScope(t), {}} {
		st := aggBy(set(diffOf("nobody"), diffOf("orders")), "owner", sc)
		if labels(st.Groups) != "unknown" || st.Runs != 2 || st.Hidden.Present || st.Completeness != fleet.CompletenessComplete {
			t.Fatalf("answer = %+v", st)
		}
	}
}

func TestFleetPlacementAmbiguousIsUnplaced(t *testing.T) {
	place := FleetPlacement(statsFleet(t), nil)
	if _, ok := place("orders"); ok {
		t.Fatal("ambiguous orders placed")
	}
	if p, ok := place("shop/orders"); !ok || p != (Placement{OwnerKey: "team:checkout", OwnerLabel: "checkout", Domain: "commerce"}) {
		t.Fatalf("shop/orders = %+v %v", p, ok)
	}
	if p, ok := place("ghost"); !ok || !p.DomainUnknown || p.OwnerKey != "" {
		t.Fatalf("ghost = %+v %v", p, ok)
	}
}

func TestFleetPlacementResolvesEachNameOnce(t *testing.T) {
	calls := map[string]int{}
	place := memoize(func(name string) (Placement, bool) {
		calls[name]++
		return Placement{Domain: name}, name == "orders"
	})
	var recs []runrecord.Record
	for i := range 50 {
		recs = append(recs, of("orders", rec(runrecord.KindDiff, "sha256:"+strings.Repeat("a", i+1), hoursAgo(2))))
	}
	recs = append(recs, diffOf("nobody"), diffOf("nobody"))
	st := aggBy(set(recs...), "domain", Scope{Restricted: true, Place: place})
	if calls["orders"] != 1 || calls["nobody"] != 1 || st.Runs != 50 {
		t.Fatalf("calls = %v, runs %d", calls, st.Runs)
	}
}

func TestRestrictedUnplaceableServiceIsHidden(t *testing.T) {
	s := set(diffOf("nobody"), diffOf("billing"), diffOf("shop/orders"))
	if st := aggBy(s, "service", restrictedScope(t)); st.Runs != 1 || labels(st.Groups) != "shop/orders" || !st.Hidden.Present {
		t.Fatalf("answer = %+v", st)
	}
	if st := aggBy(s, "service", Scope{Restricted: true}); st.Runs != 0 || !st.Hidden.Present {
		t.Fatalf("no placement = %+v", st)
	}
}

func TestRestrictedSameNameInHiddenDomainIsHidden(t *testing.T) {
	st := aggBy(set(diffOf("orders"), breaking(diffOf("orders"))), "service", restrictedScope(t))
	if st.Runs != 0 || st.Groups != nil || !st.Hidden.Present {
		t.Fatalf("answer = %+v", st)
	}
	st = aggBy(set(diffOf("orders"), diffOf("shop/orders")), "owner", restrictedScope(t))
	if st.Runs != 1 || len(st.Groups) != 1 || st.Groups[0].Label != "checkout" {
		t.Fatalf("answer = %+v", st)
	}
}

func TestRestrictedHiddenNeverNamed(t *testing.T) {
	hidden := []string{"billing", "payments/orders"}
	op := of("payments/orders", verifiedTo(digest('d'), hoursAgo(1)))
	op.Stage.Namespace = "vault"
	s := set(diffOf("billing"), breaking(diffOf("payments/orders")), op, diffOf("shop/orders"))
	s.Skipped = map[string]Skipped{runrecord.AnchorTag("billing"): {Unreadable: 2}}
	s.Truncated, s.TruncatedAnchor = "more than 3 run records", runrecord.AnchorTag("billing")
	for _, by := range []string{"service", "owner", "domain", "repository", "environment"} {
		st := aggBy(s, by, restrictedScope(t))
		data, _ := json.Marshal(st)
		for _, bad := range []string{"billing", "payments", "vault", "alice", "money", "finance", runrecord.AnchorTag(hidden[0]), runrecord.AnchorTag(hidden[1])} {
			if strings.Contains(string(data), bad) {
				t.Fatalf("by %s names %q: %s", by, bad, data)
			}
		}
		if !hasLimitation(st, LimitationOutsideView) || !hasLimitation(st, LimitationTruncated) {
			t.Fatalf("limitations = %+v", st.Limitations)
		}
	}
}

func TestTruncatedNamesOnlyAPlacedService(t *testing.T) {
	s := set(diffOf("shop/orders"))
	s.Truncated, s.TruncatedAnchor = "more than 3 run records", runrecord.AnchorTag("shop/orders")
	for _, sc := range []Scope{restrictedScope(t), {}} {
		st := aggBy(s, "", sc)
		if !strings.Contains(st.Limitations[0].Message, "shop/orders") || st.Truncated != s.Truncated {
			t.Fatalf("limitations = %+v", st.Limitations)
		}
	}
	s.TruncatedAnchor = runrecord.AnchorTag("nobody")
	if st := aggBy(s, "", Scope{}); st.Limitations[0].Message != "the read was cut at more than 3 run records; records past the bound are left out of every number" {
		t.Fatalf("limitations = %+v", st.Limitations)
	}
}

func TestRestrictedHiddenHasNoCounts(t *testing.T) {
	s := set(diffOf("billing"), diffOf("billing"), diffOf("shop/orders"))
	s.Skipped = map[string]Skipped{runrecord.AnchorTag("billing"): {Malformed: 4}, runrecord.AnchorTag("shop/orders"): {Oversized: 1}}
	st := aggBy(s, "", restrictedScope(t))
	data, _ := json.Marshal(st.Hidden)
	if string(data) != `{"present":true}` || st.RecordsRead != 1 || st.Runs != 1 || st.Skipped != (Skipped{Oversized: 1}) {
		t.Fatalf("hidden %s read %d runs %d skipped %+v", data, st.RecordsRead, st.Runs, st.Skipped)
	}
	// A placed service named by the filter keeps its skipped records with no
	// record read.
	only := RecordSet{Skipped: map[string]Skipped{runrecord.AnchorTag("shop/orders"): {Oversized: 1}}}
	st = Aggregate(only, Query{Window: aggWindow, Service: "shop/orders"}, restrictedScope(t), aggNow)
	if st.Skipped != (Skipped{Oversized: 1}) || st.Hidden.Present {
		t.Fatalf("filtered = %+v", st)
	}
	st = Aggregate(only, Query{Window: aggWindow, Service: "billing"}, restrictedScope(t), aggNow)
	if st.Skipped != (Skipped{}) || !st.Hidden.Present {
		t.Fatalf("hidden filter = %+v", st)
	}
	// An unrestricted reader counts every anchor.
	if st := aggBy(s, "", Scope{}); st.RecordsRead != 3 || st.Skipped != (Skipped{Malformed: 4, Oversized: 1}) {
		t.Fatalf("unrestricted = %+v", st)
	}
}

func TestRestrictedOperationNeedsOperationAccess(t *testing.T) {
	s := set(of("shop/orders", verifiedTo(digest('d'), hoursAgo(1))), diffOf("shop/orders"))
	sc := restrictedScope(t)
	if st := aggBy(s, "", sc); st.Totals.Environments == nil || st.Hidden.Present {
		t.Fatalf("allowed = %+v", st)
	}
	for _, visible := range []func(string) bool{func(string) bool { return false }, nil} {
		sc.OperationVisible = visible
		st := aggBy(s, "", sc)
		if st.Totals.Environments != nil || !st.Hidden.Present || st.Runs != 1 {
			t.Fatalf("refused = %+v", st)
		}
	}
	noStage := of("shop/orders", verifiedTo(digest('d'), hoursAgo(1)))
	noStage.Stage = nil
	sc.OperationVisible = func(ns string) bool { return ns != "" }
	if st := aggBy(set(noStage), "", sc); st.Runs != 0 || !st.Hidden.Present {
		t.Fatalf("no stage = %+v", st)
	}
}

func TestRestrictedNoDeliveryDropsOperations(t *testing.T) {
	const msg = "operation records need a sign-in this server cannot exchange"
	sc := restrictedScope(t)
	sc.NoDelivery = msg
	st := aggBy(set(of("shop/orders", verifiedTo(digest('d'), hoursAgo(1))), of("billing", verifiedTo(digest('d'), hoursAgo(1))), diffOf("shop/orders")), "environment", sc)
	if st.Totals.Environments != nil || st.Runs != 1 || st.Hidden.Present || st.Completeness != fleet.CompletenessPartial {
		t.Fatalf("answer = %+v", st)
	}
	if l := st.Limitations; len(l) != 1 || l[0].Code != LimitationDeliveryUnavailable || l[0].Message != msg {
		t.Fatalf("limitations = %+v", l)
	}
}

func TestRestrictedHiddenIsPartial(t *testing.T) {
	st := aggBy(set(diffOf("billing")), "", restrictedScope(t))
	if st.Completeness != fleet.CompletenessPartial || !hasLimitation(st, LimitationOutsideView) || !hasLimitation(st, LimitationNoRecords) {
		t.Fatalf("answer = %+v", st)
	}
	for _, l := range st.Limitations {
		if l.Code == LimitationOutsideView && strings.ContainsAny(l.Message, "0123456789") {
			t.Fatalf("outside limitation counts: %q", l.Message)
		}
	}
}

func TestRestrictedHiddenLeftOutOfEveryMetric(t *testing.T) {
	hidden := breaking(gated(of("billing", rec(runrecord.KindImpact, "sha256:new", hoursAgo(2))), runrecord.VerdictBlocked, 3, 2))
	st := aggBy(set(hidden, diffOf("shop/orders")), "owner", restrictedScope(t))
	if st.DistinctChanges != 1 || !reflect.DeepEqual(*st.Totals, Metrics{}) || len(st.Groups) != 1 || !reflect.DeepEqual(st.Groups[0].Metrics, Metrics{}) {
		t.Fatalf("answer = %+v", st)
	}
}

func TestLeadTimeNeverJoinsHiddenOrOtherServicePublish(t *testing.T) {
	promo := of("shop/orders", verifiedTo(digest('d'), hoursAgo(1)))
	hidden := published("billing", digest('d'), "", hoursAgo(5))
	other := published("catalog", digest('d'), "", hoursAgo(4))
	for _, sc := range []Scope{restrictedScope(t), {}} {
		lt := aggBy(set(hidden, other, promo), "", sc).Totals.Environments[0].LeadTime
		if lt.Joined != 0 || lt.Unjoined != 1 || !lt.Unknown {
			t.Fatalf("lead time = %+v", lt)
		}
	}
}

func TestAttributionPartialSnapshotMakesPartial(t *testing.T) {
	sc := unrestrictedScope(t)
	sc.Snapshot = &Attribution{AsOf: fleetNow, SnapshotID: "snap", Partial: true}
	s := set(diffOf("shop/orders"))
	st := aggBy(s, "domain", sc)
	if st.Completeness != fleet.CompletenessPartial || !hasLimitation(st, LimitationAttributionPartial) || !st.Attribution.Partial {
		t.Fatalf("answer = %+v", st)
	}
	if st := aggBy(s, "service", sc); st.Completeness != fleet.CompletenessComplete || len(st.Limitations) != 0 {
		t.Fatalf("by service = %+v", st)
	}
}

func TestGroupsWithOneLabelStayApart(t *testing.T) {
	place := func(service string) (Placement, bool) {
		return Placement{OwnerKey: map[string]string{"a": "team:x", "b": "dri:x"}[service], OwnerLabel: "x"}, true
	}
	g := aggBy(set(diffOf("a"), diffOf("b")), "owner", Scope{Place: place})
	if len(g.Groups) != 2 || g.Groups[0].Key != "dri:x" || g.Groups[1].Key != "team:x" {
		t.Fatalf("groups = %+v", g.Groups)
	}
}
