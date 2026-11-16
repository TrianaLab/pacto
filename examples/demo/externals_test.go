package main

import (
	"maps"
	"slices"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestDemoExternalsJoinThroughCatalog: the demo organization's catalog joins
// support-assistant and pay-agent, which both declare "model", on one
// llm-provider node, and carrier-gateway's parcel-carrier on its own entry;
// both through a catalog alias from the organization catalog, which the
// snapshot reports as embedded and unsigned.
func TestDemoExternalsJoinThroughCatalog(t *testing.T) {
	snap := demoFleet(t)
	for id, want := range map[fleet.ExternalKey][]string{
		"llm-provider":   {"pay-agent", "support-assistant"},
		"parcel-carrier": {"carrier-gateway"},
	} {
		rec := snap.Externals[id]
		if rec == nil {
			t.Fatalf("no external %s; have %v", id, slices.Collect(maps.Keys(snap.Externals)))
		}
		var names []string
		for _, c := range rec.Consumers {
			names = append(names, snap.Services[c].Name)
		}
		slices.Sort(names)
		if !slices.Equal(names, want) {
			t.Errorf("%s consumers = %v, want %v", id, names, want)
		}
		if rec.Catalog == nil || rec.Catalog.Origin != externalcatalog.OriginOrganization || rec.Catalog.Digest != externalcatalog.Digest(demoExternalCatalog) {
			t.Errorf("%s catalog = %+v, want the organization catalog", id, rec.Catalog)
		}
		for _, s := range rec.Sources {
			if s.Match != externalcatalog.MatchCatalogAlias {
				t.Errorf("%s source %s match = %q, want %q", id, s.Service, s.Match, externalcatalog.MatchCatalogAlias)
			}
		}
	}
	assertOrganizationCatalogRef(t, snap)
}

// assertOrganizationCatalogRef: the snapshot reports the organization catalog
// as embedded and unsigned, with a reason.
func assertOrganizationCatalogRef(t *testing.T, snap *fleet.FleetSnapshot) {
	t.Helper()
	var org *fleet.ExternalCatalogRef
	for i := range snap.ExternalCatalogs {
		if snap.ExternalCatalogs[i].Origin == externalcatalog.OriginOrganization {
			org = &snap.ExternalCatalogs[i]
		}
	}
	if org == nil || org.Copy != externalcatalog.CopyEmbedded || org.Signature != externalcatalog.SignatureUnknown || org.Reason == "" {
		t.Errorf("organization catalog ref = %+v, want embedded, signature unknown, with a reason", org)
	}
}
