package fleet

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
)

// observedExternalFleet builds a snapshot of the named services (a "domain/name"
// entry sets the domain) with the given observed edges, classified by cat.
func observedExternalFleet(t *testing.T, cat *externalcatalog.Catalog, services []string, observed ...ObservedEdge) *FleetSnapshot {
	t.Helper()
	var revs []RawRevision
	for _, s := range services {
		domain, name, ok := strings.Cut(s, "/")
		if !ok {
			domain, name = "", s
		}
		revs = append(revs, RawRevision{Domain: domain, Bundle: bundleFor(t, name), Digest: "sha256:" + s})
	}
	return buildObserved(t, cat, revs, observed)
}

func buildObserved(t *testing.T, cat *externalcatalog.Catalog, revs []RawRevision, observed []ObservedEdge) *FleetSnapshot {
	t.Helper()
	src := NewMemorySource("otel", "observation", &Collection{Revisions: revs, Observed: observed})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, ExternalCatalog: cat}, src)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func unresolvedFor(snap *FleetSnapshot, name string) int {
	n := 0
	for _, l := range snap.Limitations {
		if l.Code == LimitationObservedIdentityUnresolved && strings.Contains(l.Message, "observed service "+name+" ") {
			n++
		}
	}
	return n
}

func externalRels(snap *FleetSnapshot) []Relationship {
	var out []Relationship
	for _, r := range snap.Relationships {
		if r.ToExternal != "" && r.Provenance == ProvenanceObserved {
			out = append(out, r)
		}
	}
	return out
}

func TestFoldObservedCalleeToExternal(t *testing.T) {
	t0, t1 := fixedNow().Add(-time.Hour), fixedNow()
	snap := observedExternalFleet(t, nil, []string{"checkout"},
		ObservedEdge{From: "checkout", To: "api.stripe.com", Count: 2, FirstSeen: t0, LastSeen: t0},
		ObservedEdge{From: "checkout", To: "api.stripe.com:443", Count: 3, FirstSeen: t1, LastSeen: t1})
	rels := externalRels(snap)
	if len(rels) != 1 {
		t.Fatalf("observed external edges = %+v", rels)
	}
	want := Relationship{FromService: "checkout", ToExternal: "stripe", Type: RelationshipDependency, Provenance: ProvenanceObserved,
		ObservedCount: 5, Source: "otel", ObservedSources: []ObservedSourceStat{{Source: "otel", Count: 5, FirstSeen: &t0, LastSeen: &t1}},
		FirstSeen: &t0, LastSeen: &t1}
	if !reflect.DeepEqual(rels[0], want) {
		t.Fatalf("edge = %+v", rels[0])
	}
	if n := unresolvedFor(snap, "api.stripe.com"); n != 0 {
		t.Errorf("limitations = %+v", snap.Limitations)
	}
	rec := snap.Externals["stripe"]
	wantSrc := []ExternalSource{{Kind: ExternalSourceObserved, Service: "checkout", Provenance: ProvenanceObserved, Match: externalcatalog.KindHost}}
	if rec == nil || rec.Class != externalcatalog.ClassVendorAPI || rec.ConsumersTotal != 1 || !reflect.DeepEqual(rec.Sources, wantSrc) {
		t.Fatalf("record = %+v", rec)
	}
	if res, err := NewQuery(snap).Explain("checkout"); err != nil || len(res.Reasons) != 0 {
		t.Errorf("explain = %+v, %v", res, err)
	}
	if len(snap.observedForward["checkout"]) != 0 || len(snap.observedReverse) != 0 {
		t.Errorf("external edge entered the observed service indexes: %v %v", snap.observedForward, snap.observedReverse)
	}
}

func TestFoldUnmatchedHostnameUnclassified(t *testing.T) {
	snap := observedExternalFleet(t, nil, []string{"web"},
		ObservedEdge{From: "web", To: "erp.corp-legacy.co.uk", Count: 1},
		ObservedEdge{From: "web", To: "db1.corp-legacy.example", Count: 1})
	rels := externalRels(snap)
	if len(rels) != 1 || rels[0].ToExternal != "host/corp-legacy.co.uk" {
		t.Fatalf("edges = %+v", rels)
	}
	if rec := snap.Externals["host/corp-legacy.co.uk"]; rec == nil || rec.Class != externalcatalog.ClassUnclassified {
		t.Errorf("record = %+v", rec)
	}
	if unresolvedFor(snap, "erp.corp-legacy.co.uk") != 0 || unresolvedFor(snap, "db1.corp-legacy.example") != 1 {
		t.Errorf("limitations = %+v", snap.Limitations)
	}
}

func TestFoldNamespaceShortFormKeepsLimitation(t *testing.T) {
	snap := observedExternalFleet(t, nil, []string{"web"},
		ObservedEdge{From: "web", To: "orders.shop", Count: 1},
		ObservedEdge{From: "web", To: "payments.default", Count: 1})
	if rels := externalRels(snap); len(rels) != 0 {
		t.Fatalf("edges = %+v", rels)
	}
	if unresolvedFor(snap, "orders.shop") != 1 || unresolvedFor(snap, "payments.default") != 1 {
		t.Errorf("limitations = %+v", snap.Limitations)
	}
}

func TestFoldOrgSuffixPeerService(t *testing.T) {
	org, err := externalcatalog.ParseFile([]byte("apiVersion: catalog.pacto.trianalab.io/v1\nentries:\n  - identity: corp/billing\n    class: legacy\n    hosts: [\"*.corp.example\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	def, err := externalcatalog.ParseFile(externalcatalog.DefaultBytes())
	if err != nil {
		t.Fatal(err)
	}
	cat := externalcatalog.Merge(def, externalcatalog.DefaultRef(), org, &externalcatalog.Ref{Origin: externalcatalog.OriginOrganization, Digest: "sha256:org"})
	snap := observedExternalFleet(t, cat, []string{"web"}, ObservedEdge{From: "web", To: "billing.corp.example", Count: 1})
	if rels := externalRels(snap); len(rels) != 1 || rels[0].ToExternal != "corp/billing" {
		t.Fatalf("edges = %+v", rels)
	}
	if rec := snap.Externals["corp/billing"]; rec == nil || rec.Class != externalcatalog.ClassLegacy {
		t.Errorf("record = %+v", rec)
	}
}

func TestFoldDotlessUnknownKeepsLimitation(t *testing.T) {
	snap := observedExternalFleet(t, nil, []string{"web"}, ObservedEdge{From: "web", To: "inventory", Count: 1})
	if rels := externalRels(snap); len(rels) != 0 {
		t.Fatalf("edges = %+v", rels)
	}
	if unresolvedFor(snap, "inventory") != 1 {
		t.Errorf("limitations = %+v", snap.Limitations)
	}
}

func TestFoldAmbiguousKeepsLimitation(t *testing.T) {
	// "api.stripe.com" names two fleet services: ambiguity wins over the catalog.
	snap := observedExternalFleet(t, nil, []string{"web", "eu/api.stripe.com", "us/api.stripe.com"},
		ObservedEdge{From: "web", To: "api.stripe.com", Count: 1})
	if rels := externalRels(snap); len(rels) != 0 {
		t.Fatalf("edges = %+v", rels)
	}
	if unresolvedFor(snap, "api.stripe.com") != 1 {
		t.Errorf("limitations = %+v", snap.Limitations)
	}
}

func TestFoldServiceNameBeatsCatalog(t *testing.T) {
	cat := externalcatalog.Merge(mustCatalogFile(t, "  - identity: aws/rds\n    class: managed-cloud\n    otel: { dbSystem: [postgresql] }\n"),
		externalcatalog.Ref{Origin: externalcatalog.OriginDefault, Digest: "sha256:t"}, nil, nil)
	snap := observedExternalFleet(t, cat, []string{"web", "postgresql"}, ObservedEdge{From: "web", To: "postgresql", Count: 1})
	if rels := externalRels(snap); len(rels) != 0 {
		t.Fatalf("edges = %+v", rels)
	}
	if relObserved(snap.Relationships, "web", "postgresql") == nil {
		t.Fatalf("no service edge: %+v", snap.Relationships)
	}
	// Without the service, the OTel system value names the external.
	snap = observedExternalFleet(t, cat, []string{"web"}, ObservedEdge{From: "web", To: "postgresql", Count: 1})
	if rels := externalRels(snap); len(rels) != 1 || rels[0].ToExternal != "aws/rds" || snap.Externals["aws/rds"].Sources[0].Match != externalcatalog.KindSystem {
		t.Fatalf("edges = %+v", rels)
	}
}

func TestFoldExternalCalleeUnresolvedCaller(t *testing.T) {
	snap := observedExternalFleet(t, nil, []string{"web"}, ObservedEdge{From: "ghost", To: "api.stripe.com", Count: 1})
	if rels := externalRels(snap); len(rels) != 0 || snap.Externals["stripe"] != nil {
		t.Fatalf("edges = %+v, externals = %+v", rels, snap.Externals)
	}
	if unresolvedFor(snap, "ghost") != 1 || unresolvedFor(snap, "api.stripe.com") != 0 {
		t.Errorf("limitations = %+v", snap.Limitations)
	}
}

func TestReconcileDeclaredExternalMatched(t *testing.T) {
	c := &contract.Contract{PactoVersion: "2.1", Service: contract.Service{Name: "checkout", Version: "1.0.0", Owner: contract.Owner{Team: "t"}},
		Dependencies: []contract.Dependency{
			{Name: "stripe", External: true, Version: "2024-06-20", Required: true},
			{Name: "twilio", External: true, Version: "2010-04-01"},
		}}
	snap := buildObserved(t, nil, []RawRevision{{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Digest: "sha256:c"}},
		[]ObservedEdge{{From: "checkout", To: "api.stripe.com", Count: 1}})
	got := map[string]string{}
	for _, r := range snap.Relationships {
		if r.Provenance == ProvenanceDeclared {
			got[r.To] = r.Reconciliation
		}
	}
	if got["stripe"] != ReconciliationMatched || got["twilio"] != "" {
		t.Errorf("reconciliation = %v", got)
	}
	rec := snap.Externals["stripe"]
	if rec.ConsumersTotal != 1 || len(rec.Sources) != 2 || rec.Sources[0].Kind != ExternalSourceContract || rec.Sources[1].Kind != ExternalSourceObserved {
		t.Errorf("record = %+v", rec)
	}
}

func TestObservedExternalNilCatalogUsesDefault(t *testing.T) {
	var s FleetSnapshot
	if id, ok := s.ObservedExternal("api.stripe.com"); !ok || id != "stripe" {
		t.Errorf("ObservedExternal = %q, %v", id, ok)
	}
	if _, ok := s.ObservedExternal("http://["); ok {
		t.Error("an unparseable name matched")
	}
}

func mustCatalogFile(t *testing.T, entries string) *externalcatalog.File {
	t.Helper()
	f, err := externalcatalog.ParseFile([]byte("apiVersion: catalog.pacto.trianalab.io/v1\nentries:\n" + entries))
	if err != nil {
		t.Fatal(err)
	}
	return f
}
