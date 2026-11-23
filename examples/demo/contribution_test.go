package main

import (
	"testing"

	"github.com/trianalab/pacto/v4/examples/story"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// demoLedgerSync is the key of the service acme-idp contributes.
var demoLedgerSync = fleet.NewServiceKeyDomain(story.Domain+"/pacto-inferred/acme-idp", "ledger-sync")

// TestDemoContributionIsInferred: acme-idp's contributed ledger-sync is an
// inferred service with its producer, never declared, and its record's one
// dependency is an inferred edge to the story's declared fraud.
func TestDemoContributionIsInferred(t *testing.T) {
	snap := demoFleet(t)
	s := snap.Services[demoLedgerSync]
	if s == nil || !s.Inferred() || s.Contribution == nil || s.Contribution.Producer != demoProducer ||
		s.Contribution.Repository != demoContributionRepository || s.Contribution.AsOf == nil || s.Contribution.Stale {
		t.Fatalf("ledger-sync = %+v", s)
	}
	testLedgerSyncEdge(t, snap)
}

// testLedgerSyncEdge: ledger-sync's one edge is inferred, of unknown
// compatibility, to the story's declared fraud.
func testLedgerSyncEdge(t *testing.T, snap *fleet.FleetSnapshot) {
	t.Helper()
	fraud := fleet.NewServiceKeyDomain(story.Domain, "fraud")
	var edges []fleet.Relationship
	for _, r := range snap.Relationships {
		if r.FromService == demoLedgerSync {
			edges = append(edges, r)
		}
	}
	if len(edges) != 1 || edges[0].ToService != fraud || edges[0].Provenance != fleet.ProvenanceInferred ||
		edges[0].Compatibility != contract.CompatibilityUnknown {
		t.Errorf("ledger-sync edges = %+v, want one inferred edge to %s", edges, fraud)
	}
	if f := snap.Services[fraud]; f == nil || f.Inferred() {
		t.Errorf("fraud = %+v, want a declared service", f)
	}
}

// TestDemoProducerRow: the Evidence Server source lists acme-idp's one row,
// built by fleet.ProducerRow, available and dated.
func TestDemoProducerRow(t *testing.T) {
	snap := demoFleet(t)
	var rows []fleet.Provider
	for _, p := range snap.Providers {
		if p.Source == demoEvidenceSource {
			rows = append(rows, p)
		}
	}
	want, _ := fleet.ProducerRow(demoProducerFacts(), demoNow(), demoFreshness)
	if len(rows) != 1 || rows[0].ID != fleet.ProducerRowPrefix+demoProducer || rows[0].Status != fleet.SourceAvailable ||
		rows[0].AsOf == nil || rows[0].Evidence != want.Evidence {
		t.Fatalf("evidence rows = %+v, want acme-idp's", rows)
	}
}

// TestDemoReportedTarget: acme-idp reports one external target through the
// Evidence Server, so the target is reported by it.
func TestDemoReportedTarget(t *testing.T) {
	snap := demoFleet(t)
	var got []*fleet.TargetRecord
	for _, tg := range snap.Targets {
		if tg.Scope == demoProducer {
			got = append(got, tg)
		}
	}
	if len(got) != 1 || got[0].Kind != "external" || got[0].ReportedBy != demoProducer ||
		got[0].ServiceKey != fleet.NewServiceKeyDomain(story.Domain, "fraud") {
		t.Fatalf("acme-idp targets = %+v", got)
	}
}
