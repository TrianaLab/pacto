package impact

import (
	"context"
	"reflect"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestServiceFactsNeverChangeImpact: inferred owners, domains and a fact edge
// from a service that is no consumer leave every impact answer as it was.
func TestServiceFactsNeverChangeImpact(t *testing.T) {
	build := func(facts bool) *fleet.FleetSnapshot {
		revs := []fleet.RawRevision{{Bundle: mkBundle(svcContract("auth-service", "1.0.0", contract.Owner{Team: "platform"}))}, gwRev(authDep),
			{Bundle: mkBundle(svcContract("frontend", "1.0.0", contract.Owner{}))}}
		srcs := []fleet.Source{fleet.NewMemorySource("test", "memory", &fleet.Collection{Revisions: revs, Targets: []fleet.RawTarget{gwTarget(&evidenceNow)}})}
		if facts {
			srcs = append(srcs, fleet.NewMemorySource("catalog-a", "idp-catalog", &fleet.Collection{Advisory: true, ServiceFacts: []fleet.RawServiceFact{
				{Name: "auth-service", Owner: contract.Owner{Team: "other"}, BusinessDomain: "identity"},
				{Name: "api-gateway", Owner: contract.Owner{Team: "edge"}, BusinessDomain: "edge"},
				{Name: "frontend", Owner: contract.Owner{Team: "web"}, DependsOn: []fleet.RawFactRef{{Name: "auth-service"}}},
			}}))
		}
		snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return evidenceNow }}, srcs...)
		if err != nil {
			t.Fatal(err)
		}
		return snap
	}
	without, with := build(false), build(true)
	if len(with.InferredRelationships) != 1 {
		t.Fatalf("the fixture holds no fact edge: %+v", with.InferredRelationships)
	}
	nonBreaking := func(snap *fleet.FleetSnapshot) *Result {
		return Analyze(context.Background(), svcContract("auth-service", "1.0.0", contract.Owner{Team: "platform"}),
			svcContract("auth-service", "1.1.0", contract.Owner{Team: "platform"}), fstest.MapFS{}, fstest.MapFS{}, snap, Options{IncludeObserved: true})
	}
	breaking := func(snap *fleet.FleetSnapshot) *Result { return analyzeAuth(snap, Options{IncludeObserved: true}) }
	for name, analyze := range map[string]func(*fleet.FleetSnapshot) *Result{"breaking": breaking, "non-breaking": nonBreaking} {
		a, b := analyze(without), analyze(with)
		if a.Gate != b.Gate || a.GateReason != b.GateReason || a.Classification != b.Classification || !reflect.DeepEqual(a.Consumers, b.Consumers) ||
			!reflect.DeepEqual(a.Owners, b.Owners) || a.Completeness != b.Completeness || !reflect.DeepEqual(a.Limitations, b.Limitations) {
			t.Errorf("%s: facts changed the answer:\n%+v\nwant\n%+v", name, b, a)
		}
		if name == "breaking" && len(a.Consumers) == 0 {
			t.Errorf("the breaking change reaches no consumer: %+v", a)
		}
	}
}
