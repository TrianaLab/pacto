package impact

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const legacy = "k8s:pay/Deployment/legacy"

// inferredEdgeSnaps is auth-service, api-gateway depending on it and an
// uncontracted legacy caller of auth-service, without and with the legacy
// workload and the inferred edges it ends (legacy to and from auth-service,
// api-gateway to legacy).
func inferredEdgeSnaps(t *testing.T) (without, with *fleet.FleetSnapshot) {
	t.Helper()
	build := func(observed []fleet.ObservedEdge, other ...fleet.Source) *fleet.FleetSnapshot {
		revs := []fleet.RawRevision{{Bundle: mkBundle(svcContract("auth-service", "1.0.0", contract.Owner{Team: "platform"}))}, gwRev(authDep)}
		srcs := append([]fleet.Source{fleet.NewMemorySource("test", "memory", &fleet.Collection{Revisions: revs, Observed: observed})}, other...)
		snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return evidenceNow }}, srcs...)
		if err != nil {
			t.Fatal(err)
		}
		return snap
	}
	caller := fleet.ObservedEdge{From: legacy, To: "auth-service", Count: 1}
	without = build([]fleet.ObservedEdge{caller})
	with = build([]fleet.ObservedEdge{caller, {From: "auth-service", To: legacy, Count: 1}, {From: "api-gateway", To: legacy, Count: 1}}, legacyWorkloads())
	if len(with.InferredRelationships) != 3 {
		t.Fatalf("inferred relationships %+v", with.InferredRelationships)
	}
	return without, with
}

// sameAnswer fails when b's consumers, gate or completeness differ from a's.
func sameAnswer(t *testing.T, a, b *Result) {
	t.Helper()
	if !reflect.DeepEqual(a.Consumers, b.Consumers) || a.Gate != b.Gate || a.Completeness != b.Completeness {
		t.Errorf("consumers %+v gate %s %s\nwant %+v gate %s %s", b.Consumers, b.Gate, b.Completeness, a.Consumers, a.Gate, a.Completeness)
	}
}

func TestImpactIgnoresInferredRelationships(t *testing.T) {
	without, with := inferredEdgeSnaps(t)
	a, b := analyzeAuth(without, Options{IncludeObserved: true}), analyzeAuth(with, Options{IncludeObserved: true})
	sameAnswer(t, a, b)
	if !slices.ContainsFunc(b.Consumers, func(c AffectedConsumer) bool { return c.Uncontracted }) {
		t.Errorf("the uncontracted legacy caller is no consumer: %+v", b.Consumers)
	}
}

func TestEventIgnoresInferredRelationships(t *testing.T) {
	without, with := inferredEdgeSnaps(t)
	auth := string(fleet.NewServiceKey("auth-service"))
	a, b := event(without, EventUnavailable, auth, Options{IncludeObserved: true}), event(with, EventUnavailable, auth, Options{IncludeObserved: true})
	sameAnswer(t, a, b)
}

func TestImpactTracesIgnoreInferredCaller(t *testing.T) {
	_, snap := inferredEdgeSnaps(t)
	base := analyzeAuth(snap, Options{IncludeObserved: true})
	res := analyzeAuth(snap, Options{IncludeObserved: true, ObservedEdges: []ObservedEdge{{Consumer: legacy, Provider: "auth-service"}}})
	if !reflect.DeepEqual(base.Consumers, res.Consumers) {
		t.Errorf("a trace from the inferred legacy changed the consumers: %+v\nwant %+v", res.Consumers, base.Consumers)
	}
	if slices.ContainsFunc(res.Consumers, func(c AffectedConsumer) bool {
		return !c.Uncontracted && (c.Service == legacy || c.Service == string(fleet.NewServiceKey(legacy)))
	}) {
		t.Errorf("an inferred service is a contracted consumer: %+v", res.Consumers)
	}
	if res.Gate == GateClear {
		t.Errorf("a trace no contract explains cleared the gate: %s", res.GateReason)
	}
}

func TestImpactTracesFromReplacedWorkload(t *testing.T) {
	revs := []fleet.RawRevision{{Bundle: mkBundle(svcContract("auth-service", "1.0.0", contract.Owner{Team: "platform"}))}, gwRev(authDep)}
	gw := fleet.RawTarget{Scope: "pay", Kind: "kubernetes", Name: "api-gateway", Service: "api-gateway", Workload: &fleet.WorkloadRef{Kind: "Deployment", Name: "legacy"}}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return evidenceNow }},
		fleet.NewMemorySource("test", "memory", &fleet.Collection{Revisions: revs, Targets: []fleet.RawTarget{gw}}), legacyWorkloads())
	if err != nil {
		t.Fatal(err)
	}
	res := analyzeAuth(snap, Options{IncludeObserved: true, ObservedEdges: []ObservedEdge{{Consumer: legacy, Provider: "auth-service"}}})
	if l := limitation(res, ObservedIdentityUnresolved); l != nil {
		t.Errorf("the replaced workload is unresolved: %s", l.Message)
	}
	if slices.ContainsFunc(res.Consumers, func(c AffectedConsumer) bool { return c.Service == legacy || c.Uncontracted }) {
		t.Errorf("the replaced workload is an uncontracted consumer: %+v", res.Consumers)
	}
	if gwc := consumerNamed(t, res, "api-gateway"); gwc.Service == "" {
		t.Errorf("consumers %+v", res.Consumers)
	}
}
