package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestDemoConsumableOfferings: the demo lists postgresql 17.0.0, signed under
// its publisher rule, and objectstore 1.0.0, accepted by dana's declaration, as
// the offerings a caller may consume; every other provider is unchecked, so the
// answer is partial.
func TestDemoConsumableOfferings(t *testing.T) {
	res, err := fleet.NewQuery(demoFleet(t)).Entities(fleet.EntityFilter{Kinds: []fleet.EntityKind{fleet.KindOffering}, Consumable: true})
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]*fleet.ConsumableOffering{}
	for _, e := range res.Entities {
		rows[e.Key] = e.Consumable
	}
	if len(rows) != 2 || rows["postgresql"] == nil || rows["objectstore"] == nil {
		t.Fatalf("consumable rows = %v, want postgresql and objectstore", rows)
	}
	if p := rows["objectstore"].Providers; len(p) != 1 || p[0].Acceptance != "declaration" || p[0].Publisher != "dana@acme.example" {
		t.Fatalf("objectstore providers = %+v", p)
	}
	if p := rows["postgresql"].Providers; len(p) != 1 {
		t.Fatalf("postgresql providers = %+v", p)
	}
	checkDemoPostgresql(t, rows["postgresql"].Providers[0])
	if res.Meta.Completeness != fleet.CompletenessPartial {
		t.Fatalf("completeness = %s, want partial", res.Meta.Completeness)
	}
	var unknown string
	for _, l := range res.Meta.Limitations {
		if l.Code == fleet.LimitationOfferingAcceptanceUnknown {
			unknown = l.Message
		}
	}
	if !strings.Contains(unknown, "7 providers") {
		t.Fatalf("OFFERING_ACCEPTANCE_UNKNOWN = %q, want 7 providers", unknown)
	}
}

// checkDemoPostgresql: postgresql 17.0.0 is accepted by its publisher, its four
// untiered fields read contract, it has no class and its snippet needs nothing.
func checkDemoPostgresql(t *testing.T, p fleet.ConsumableProvider) {
	t.Helper()
	if p.Version != "17.0.0" || p.Acceptance != "publisher" || len(p.Classes) != 0 {
		t.Fatalf("postgresql provider = %+v", p)
	}
	tiers := map[string]string{}
	for _, par := range p.Parameters {
		tiers[par.Path] = par.Tier
	}
	want := map[string]string{"size": "contract", "storageGi": "contract", "highAvailability": "contract", "backups.retentionDays": "contract"}
	if fmt.Sprint(tiers) != fmt.Sprint(want) {
		t.Errorf("parameter tiers = %v, want %v", tiers, want)
	}
	cfgs := p.Snippet.Entries.Configurations
	if len(cfgs) != 1 || fmt.Sprint(cfgs[0].Values) != "map[size:small storageGi:10]" || len(cfgs[0].MissingValues) != 0 {
		t.Fatalf("snippet configurations = %+v", cfgs)
	}
}
