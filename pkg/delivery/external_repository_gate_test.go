package delivery

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// withRepositoryExternalEdges is snap with an inferred edge from every one of
// its services to an external system its newest revision's inferred record
// names, and the record of that system.
func withRepositoryExternalEdges(snap *fleet.FleetSnapshot) *fleet.FleetSnapshot {
	out := *snap
	out.Relationships = append([]fleet.Relationship(nil), snap.Relationships...)
	rec := &fleet.ExternalRecord{Key: "stripe", Label: "Stripe", Class: "vendor-api"}
	for k, svc := range snap.Services {
		var rk fleet.RevisionKey
		if len(svc.Revisions) > 0 {
			rk = svc.Revisions[len(svc.Revisions)-1]
		}
		out.Relationships = append(out.Relationships, fleet.Relationship{FromService: k, ToExternal: "stripe", Type: fleet.RelationshipDependency,
			Provenance: fleet.ProvenanceInferred, Confidence: "possible", InferredSources: []fleet.InferredSource{
				{Source: SnapshotSourceID, Kind: "sdk", Where: "requirements.txt:1", Confidence: "possible", Revision: rk},
				{Source: SnapshotSourceID, Kind: "terraform", Where: "infra/main.tf:4 aws_sqs_queue.orders", Confidence: "possible", Revision: rk}}})
		rec.Consumers = append(rec.Consumers, k)
		rec.Sources = append(rec.Sources, fleet.ExternalSource{Kind: fleet.ExternalSourceRepository, Service: k, Revision: rk, Provenance: fleet.ProvenanceInferred, Match: "sdk"})
	}
	out.Externals = map[fleet.ExternalKey]*fleet.ExternalRecord{"stripe": rec}
	return &out
}

// TestPlanRepositoryExternalChangesNoVerdict: a Stage's gates give the same
// verdicts, floor and question with and without the externals a service's
// repository names, and no passing verdict cites one.
func TestPlanRepositoryExternalChangesNoVerdict(t *testing.T) {
	old, major := bundle("payments", "1.0.0"), bundle("payments", "2.0.0")
	for _, c := range []struct {
		name     string
		from, to Revision
		pactos   []PactoView
	}{
		{"compatible", rev(dA, c1, old), rev(dB, c2, bundle("payments", "1.1.0")), []PactoView{checkoutPacto("^1.0.0")}},
		{"breaking", rev(dA, c1, old), rev(dB, c2, major), []PactoView{checkoutPacto("^1.0.0")}},
	} {
		snap, err := Snapshot(context.Background(), c.pactos, nil, []Revision{c.from, c.to}, now, Members{})
		if err != nil {
			t.Fatal(err)
		}
		plan := func(s *fleet.FleetSnapshot) Plan {
			return autonomousPlan(PlanInput{Spec: promote(dA, dB), Stage: stageSpec(),
				Status: StageStatus{Pointer: dA, Verified: []string{dA}, Published: []string{dB}}, From: c.from, To: c.to, Snapshot: s, Class: ClassAgent})
		}
		without, with := plan(snap), plan(withRepositoryExternalEdges(snap))
		if !reflect.DeepEqual(without.Verdicts, with.Verdicts) || !reflect.DeepEqual(without.Floor, with.Floor) || without.Question != with.Question ||
			without.Predicted != with.Predicted || without.ImpactDigest != with.ImpactDigest {
			t.Errorf("%s: without %+v\nwith %+v", c.name, without, with)
		}
		for _, v := range with.Verdicts {
			if v.Result == Pass && strings.Contains(strings.ToLower(fmt.Sprint(v.Message, v.Citation)), "stripe") {
				t.Errorf("%s: a passing verdict cites the external: %+v", c.name, v)
			}
		}
	}
}
