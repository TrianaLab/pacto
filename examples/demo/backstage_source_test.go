package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/backstage"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// demoBilling is the demo's inferred legacy-billing service.
var demoBilling = fleet.NewServiceKey(fleet.K8sCaller(prodClusterID, "shop", "Deployment", "legacy-billing"))

// TestDemoBackstageInferredOwner: the catalog's owner and system for
// legacy-billing land beside its empty declared fields as inferred values
// naming the source, the entity and the read time, read from an available
// advisory source.
func TestDemoBackstageInferredOwner(t *testing.T) {
	snap := demoFleet(t)
	s := snap.Services[demoBilling]
	if s == nil {
		t.Fatal("no legacy-billing service")
	}
	if !s.Owner.IsEmpty() || s.BusinessDomain != "" || s.OwnerProvenance != fleet.OwnerProvenanceInferred {
		t.Errorf("declared owner %q domain %q provenance %q", s.Owner.KeyString(), s.BusinessDomain, s.OwnerProvenance)
	}
	want := func(name string, f *fleet.InferredFact, v string) {
		if f == nil || f.Value != v || f.Source != backstage.SourceKind || f.Where != "component:default/legacy-billing" || f.AsOf == nil || !f.AsOf.Equal(demoClock) {
			t.Errorf("%s = %+v, want %q from the catalog", name, f, v)
		}
	}
	want("inferred owner", s.InferredOwner, "team:billing-ops")
	want("inferred domain", s.InferredBusinessDomain, "billing")
	i := slices.IndexFunc(snap.Sources, func(st fleet.SourceState) bool { return st.ID == backstage.SourceKind })
	if i < 0 || snap.Sources[i].Status != fleet.SourceAvailable || !snap.Sources[i].Advisory || snap.Sources[i].Kind != backstage.SourceKind {
		t.Fatalf("sources = %+v", snap.Sources)
	}
}

// TestDemoBackstageDeclaredOwnerUnchanged: a catalog owner that contradicts
// payments-service's declared owner changes nothing it decides.
func TestDemoBackstageDeclaredOwnerUnchanged(t *testing.T) {
	s := demoFleet(t).Services["payments-service"]
	if s.Owner.KeyString() != "team:payments-team" || s.OwnerProvenance != fleet.OwnerProvenanceDeclared || s.BusinessDomain != "payments" {
		t.Errorf("payments-service owner %q (%s) domain %q", s.Owner.KeyString(), s.OwnerProvenance, s.BusinessDomain)
	}
}

// TestDemoBackstageInferredDomainOnAContract: analytics-pipeline's contract
// declares an owner and no domain, so the catalog's system is an inferred
// domain on its contract service page and its catalog owner changes nothing.
func TestDemoBackstageInferredDomainOnAContract(t *testing.T) {
	s := demoFleet(t).Services["analytics-pipeline"]
	if s == nil || s.BusinessDomain != "" || s.OwnerProvenance != fleet.OwnerProvenanceDeclared {
		t.Fatalf("analytics-pipeline = %+v", s)
	}
	if f := s.InferredBusinessDomain; f == nil || f.Value != "analytics" || f.Where != "component:default/analytics-pipeline" {
		t.Errorf("inferred domain = %+v", f)
	}
}

// TestDemoBackstageExportSkipped: a component Pacto exported is never read
// back, so orders-service carries no inferred value.
func TestDemoBackstageExportSkipped(t *testing.T) {
	s := demoFleet(t).Services["orders-service"]
	if s.InferredOwner != nil || s.InferredBusinessDomain != nil {
		t.Errorf("orders-service read its own export back: %+v %+v", s.InferredOwner, s.InferredBusinessDomain)
	}
}

// TestDemoBackstageEdge: legacy-billing's catalog dependsOn on invoices is
// one inferred edge, never a declared one; the resource ref is left out.
func TestDemoBackstageEdge(t *testing.T) {
	snap := demoFleet(t)
	var facts []fleet.Relationship
	for _, r := range snap.InferredRelationships {
		if fleet.HasServiceFact(r) {
			facts = append(facts, r)
		}
	}
	if len(facts) != 1 || facts[0].FromService != demoBilling || facts[0].ToService != "invoices" || facts[0].Provenance != fleet.ProvenanceInferred {
		t.Fatalf("service fact edges = %+v", facts)
	}
	if slices.ContainsFunc(snap.Relationships, func(r fleet.Relationship) bool { return r.FromService == demoBilling && r.ToExternal == "" }) {
		t.Error("the catalog edge reached the declared relationships")
	}
}

// demoGoldenPaths is how many golden paths the demo declares: http-service.
const demoGoldenPaths = 1

// TestDemoInferredTemplates: the catalog's springboot-service template is an
// inferred golden path with a scaffolder link on the configured base; its
// http-service template is folded into the declared http-service golden
// path, which keeps it as its link and is listed once.
func TestDemoInferredTemplates(t *testing.T) {
	res, err := fleet.NewQuery(demoFleet(t)).Entities(fleet.EntityFilter{Template: fleet.TemplateOnly})
	if err != nil {
		t.Fatal(err)
	}
	list := res.InferredTemplates
	if list == nil || list.Total != 1 || len(list.Items) != 1 {
		t.Fatalf("inferred templates = %+v", list)
	}
	sb := list.Items[0]
	if sb.Name != "springboot-service" || sb.Provenance != fleet.ProvenanceInferred || !sb.Acceptable ||
		sb.URL != demoBackstageBase+"/create/templates/default/springboot-service" || sb.Entity != "template:default/springboot-service" {
		t.Errorf("springboot-service = %+v", sb)
	}
	if len(res.Entities) != demoGoldenPaths {
		t.Fatalf("golden paths = %+v, want %d", res.Entities, demoGoldenPaths)
	}
	if d := list.Declared; len(d) != 1 || d[0].Name != "http-service" || d[0].DeclaredAs != res.Entities[0].Key {
		t.Errorf("declared = %+v, golden path %q", d, res.Entities[0].Key)
	}
}

// TestDemoInferredTemplatesPartial: the demo's registry mirror and edge
// cluster are not read whole, so a listed template may already be declared:
// the list says so.
func TestDemoInferredTemplatesPartial(t *testing.T) {
	list := fleet.NewQuery(demoFleet(t)).InferredTemplates("")
	if list.Completeness != fleet.CompletenessPartial || !strings.Contains(fmt.Sprint(list.Limitations), "may already be declared") {
		t.Errorf("completeness %s limitations %+v", list.Completeness, list.Limitations)
	}
}
