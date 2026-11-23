package main

import (
	_ "embed"
	"time"

	"github.com/trianalab/pacto/v4/pkg/backstage"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// demoBackstageCatalog is one entities/by-query page of the organization's
// Backstage catalog: legacy-billing's owner, system and dependsOn, a
// payments-service owner that contradicts the declared one and a component
// Pacto exported, which is never read back.
//
//go:embed backstage-catalog.json
var demoBackstageCatalog []byte

// demoBackstageSource reads the catalog page as the dashboard's Backstage
// source does: an available advisory source of inferred service facts.
func demoBackstageSource(now time.Time) (fleet.Source, error) {
	facts, _, _, err := backstage.ReadCatalogPage(demoBackstageCatalog, now)
	if err != nil {
		return nil, err
	}
	return fleet.NewMemorySource(backstage.SourceKind, backstage.SourceKind, &fleet.Collection{
		Advisory: true, ServiceFacts: facts, State: &fleet.SourceState{Status: fleet.SourceAvailable, ObservedAt: &now},
	}), nil
}
