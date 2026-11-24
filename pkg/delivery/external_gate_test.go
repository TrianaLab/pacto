package delivery

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// withExternalEdges is snap with inferred and observed edges from every one
// of its services to an external system, and the record that names it.
func withExternalEdges(snap *fleet.FleetSnapshot) *fleet.FleetSnapshot {
	out := *snap
	out.Relationships = append([]fleet.Relationship(nil), snap.Relationships...)
	rec := &fleet.ExternalRecord{Key: "stripe", Label: "Stripe", Class: "vendor-api"}
	for k := range snap.Services {
		out.Relationships = append(out.Relationships,
			fleet.Relationship{FromService: k, ToExternal: "stripe", Type: fleet.RelationshipDependency, Provenance: fleet.ProvenanceInferred,
				Confidence: "likely", InferredSources: []fleet.InferredSource{{Source: SnapshotSourceID, Kind: "env", Where: "deployment/x env STRIPE", Host: "api.stripe.com", Confidence: "likely"}}},
			fleet.Relationship{FromService: k, ToExternal: "stripe", Type: fleet.RelationshipDependency, Provenance: fleet.ProvenanceObserved, ObservedCount: 3, Source: "otel"})
		rec.Consumers = append(rec.Consumers, k)
		rec.Sources = append(rec.Sources, fleet.ExternalSource{Kind: fleet.ExternalSourcePodSpec, Service: k, Provenance: fleet.ProvenanceInferred, Match: "host"})
	}
	out.Externals = map[fleet.ExternalKey]*fleet.ExternalRecord{"stripe": rec}
	return &out
}

// TestPlanInferredExternalEdgeChangesNoVerdict: a Stage's gates (impact,
// environment, owner and the rest) give the same verdicts, floor and
// question with and without inferred and observed edges to an external
// system, and no passing verdict cites one.
func TestPlanInferredExternalEdgeChangesNoVerdict(t *testing.T) {
	old, major := bundle("payments", "1.0.0"), bundle("payments", "2.0.0")
	prod := map[string]string{"prod.yaml": "app:\n  replicas: 3\n"}
	for _, c := range []struct {
		name     string
		from, to Revision
		pactos   []PactoView
	}{
		{"compatible", rev(dA, c1, old), rev(dB, c2, bundle("payments", "1.1.0")), []PactoView{checkoutPacto("^1.0.0")}},
		{"breaking", rev(dA, c1, old), rev(dB, c2, major), []PactoView{checkoutPacto("^1.0.0")}},
		{"environment", rev(dA, c1, old), rev(dB, c2, envBundle(map[string]string{"prod": "prod.yaml"}, prod, true)), nil},
	} {
		snap, err := Snapshot(context.Background(), c.pactos, nil, []Revision{c.from, c.to}, now, Members{})
		if err != nil {
			t.Fatal(err)
		}
		plan := func(s *fleet.FleetSnapshot) Plan {
			return autonomousPlan(PlanInput{Spec: promote(dA, dB), Stage: stageSpec(),
				Status: StageStatus{Pointer: dA, Verified: []string{dA}, Published: []string{dB}}, From: c.from, To: c.to, Snapshot: s, Class: ClassAgent})
		}
		without, with := plan(snap), plan(withExternalEdges(snap))
		if !reflect.DeepEqual(without.Verdicts, with.Verdicts) || !reflect.DeepEqual(without.Floor, with.Floor) || without.Question != with.Question ||
			without.Predicted != with.Predicted || without.ImpactDigest != with.ImpactDigest {
			t.Errorf("%s: without %+v\nwith %+v", c.name, without, with)
		}
		for _, v := range with.Verdicts {
			if v.Result != Pass {
				continue
			}
			if strings.Contains(strings.ToLower(v.Message), "stripe") {
				t.Errorf("%s: a passing verdict cites the external: %+v", c.name, v)
			}
			for _, val := range v.Citation {
				if strings.Contains(strings.ToLower(val), "stripe") {
					t.Errorf("%s: a passing verdict cites the external: %+v", c.name, v)
				}
			}
		}
	}
}
