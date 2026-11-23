package main

import (
	_ "embed"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/backstage"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// demoBackstageCatalog is one entities/by-query page of the organization's
// Backstage catalog: legacy-billing's owner, system and dependsOn, a
// payments-service owner that contradicts the declared one, a component
// Pacto exported, which is never read back, and two software templates:
// springboot-service, an inferred golden path, and http-service, which the
// declared http-service golden path holds the name of.
//
//go:embed backstage-catalog.json
var demoBackstageCatalog []byte

// demoBackstageBase is where the demo's configured Backstage is opened
// (RFC 2606).
const demoBackstageBase = "https://backstage.acme.example"

// demoBackstageSource reads the catalog page as a configured Backstage source
// does: an available advisory source of inferred service facts and of
// software templates a person may accept.
func demoBackstageSource(now time.Time) (fleet.Source, error) {
	read, err := backstage.ReadCatalogPage(demoBackstageCatalog, now)
	if err != nil {
		return nil, err
	}
	for i, t := range read.Templates {
		ns, name, _ := strings.Cut(strings.TrimPrefix(t.Entity, "template:"), "/")
		read.Templates[i].From, read.Templates[i].Acceptable = "Backstage (backstage.acme.example)", true
		read.Templates[i].URL = backstage.ScaffolderURL(demoBackstageBase, ns, name)
	}
	return fleet.NewMemorySource(backstage.SourceKind, backstage.SourceKind, &fleet.Collection{
		Advisory: true, ServiceFacts: read.Facts, InferredTemplates: read.Templates, ListsTemplates: true,
		State: &fleet.SourceState{Status: fleet.SourceAvailable, ObservedAt: &now},
	}), nil
}
