package impact

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// legacyWorkloads is a workload source that sees k8s:pay/Deployment/legacy.
func legacyWorkloads() fleet.Source {
	return fleet.NewMemorySource("k8s-workloads", "kubernetes-workloads", &fleet.Collection{
		InferredServices: []fleet.RawInferredService{{Namespace: "pay", Kind: "Deployment", Name: "legacy"}},
	})
}

func TestImpactUncontractedConsumerUnchangedWithInferredCaller(t *testing.T) {
	auth := svcContract("auth-service", "1.0.0", contract.Owner{Team: "platform"})
	analyze := func(inferred bool) *Result {
		srcs := []fleet.Source{fleet.NewMemorySource("delivery", "kubernetes", &fleet.Collection{
			Revisions: []fleet.RawRevision{{Bundle: mkBundle(auth)}},
			Observed:  []fleet.ObservedEdge{{From: "k8s:pay/Deployment/legacy", To: "auth-service", Count: 1}},
		})}
		if inferred {
			srcs = append(srcs, legacyWorkloads())
		}
		snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, srcs...)
		if err != nil {
			t.Fatal(err)
		}
		return Analyze(context.Background(), auth, svcContract("auth-service", "2.0.0", contract.Owner{Team: "platform"}),
			fstest.MapFS{}, fstest.MapFS{}, snap, Options{IncludeObserved: true})
	}
	without, with := analyze(false), analyze(true)
	if !reflect.DeepEqual(without.Consumers, with.Consumers) || without.Gate != with.Gate || without.Completeness != with.Completeness {
		t.Errorf("consumers %+v gate %s %s\nwant %+v gate %s %s", with.Consumers, with.Gate, with.Completeness, without.Consumers, without.Gate, without.Completeness)
	}
	if len(with.Consumers) != 1 || !with.Consumers[0].Uncontracted || with.Consumers[0].CompatibilityVerdict != CompatibilityUnknown {
		t.Errorf("consumers %+v", with.Consumers)
	}
}

func TestImpactObservedInferredNeverCorroborates(t *testing.T) {
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return evidenceNow }},
		fleet.NewMemorySource("test", "memory", &fleet.Collection{Revisions: []fleet.RawRevision{
			{Bundle: mkBundle(svcContract("auth-service", "1.0.0", contract.Owner{Team: "platform"}))}, gwRev(authDep),
		}}), legacyWorkloads())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []ObservedEdge{{Consumer: "k8s:pay/Deployment/legacy", Provider: "auth-service"}, {Consumer: "api-gateway", Provider: "k8s:pay/Deployment/legacy"}} {
		res := analyzeAuth(snap, Options{IncludeObserved: true, ObservedEdges: []ObservedEdge{e}})
		if limitation(res, ObservedIdentityUnresolved) == nil || res.Completeness != fleet.CompletenessPartial || res.Gate == GateClear {
			t.Errorf("%+v: completeness %s gate %s limitations %+v", e, res.Completeness, res.Gate, res.Limitations)
		}
		if slices.ContainsFunc(res.Consumers, func(c AffectedConsumer) bool { return c.Service == "k8s:pay/Deployment/legacy" }) {
			t.Errorf("an inferred service is a consumer: %+v", res.Consumers)
		}
	}
}

func TestOfferingProvidersSkipInferred(t *testing.T) {
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, legacyWorkloads())
	if err != nil {
		t.Fatal(err)
	}
	if got := offeringProviders(snap, ""); len(got) != 0 {
		t.Errorf("an inferred service provides an offering: %v", got)
	}
}
