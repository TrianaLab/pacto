package fleetsrc

import (
	"context"
	"slices"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// meshBuild builds a snapshot from the workload and mesh sources over one
// client, beside the declared payments a Pacto target runs as prod/payments.
func meshBuild(t *testing.T, namespaces []string) *fleet.FleetSnapshot {
	t.Helper()
	f := meshClient(true,
		[]string{svcJSON("istio-system", "istiod", "", "app=istiod"), svcJSON("istio-system", "prom-main", "", "app.kubernetes.io/name=prometheus"),
			svcJSON("shop", "cart", ""), svcJSON("legacy", "old", "")},
		[]string{nsJSON("istio-system"), nsJSON("shop", "istio-injection=enabled"), nsJSON("legacy")})
	// The two sources read concurrently, so each gets its own fake.
	workloads := &workloadFake{pages: map[string][2]string{
		"deployments||":  {workloadJSON("shop", "cart", "cart:1") + "," + workloadJSON("prod", "payments", "pay:1")},
		"statefulsets||": {workloadJSON("shop", "ledger", "ledger:1")},
	}}
	p := newProm(t, answer(vector(series("source", "cart", "ledger", "7"),
		series("source", "cart", "payments", "4", "destination_workload_namespace=prod"))))
	cl := &fleet.Cluster{ID: "c1", Name: "eu"}
	mesh := meshSource(f, namespaces, map[string]*promStub{"istio-system/prom-main": p}, nil)
	mesh.cluster = cl
	tgt := fleet.RawTarget{Scope: "prod", Kind: "kubernetes", Name: "payments", Service: "payments", Compliance: fleet.StatusCompliant,
		Workload: &fleet.WorkloadRef{Kind: "Deployment", Name: "payments"}}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{},
		fleet.NewMemorySource("k8s", fleet.SourceKindKubernetes, &fleet.Collection{Cluster: cl, Targets: []fleet.RawTarget{tgt}}),
		NewWorkloadSource("c1-workloads", cl, workloads, nil), mesh)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestMeshEdgeJoinsInferredService(t *testing.T) {
	snap := meshBuild(t, nil)
	cart, ledger := fleet.NewServiceKey("k8s:shop/Deployment/cart"), fleet.NewServiceKey("k8s:shop/StatefulSet/ledger")
	if !slices.ContainsFunc(snap.InferredRelationships, func(r fleet.Relationship) bool { return r.FromService == cart && r.ToService == ledger }) {
		t.Errorf("inferred relationships %+v", snap.InferredRelationships)
	}
	if len(snap.Relationships) != 0 {
		t.Errorf("a mesh edge with an inferred end is declared or observed: %+v", snap.Relationships)
	}
	want := fleet.UncontractedCaller{Caller: "k8s:shop/Deployment/cart", ToService: fleet.NewServiceKey("payments"), Sources: []string{"c1-mesh"}, Entity: cart}
	if len(snap.UncontractedCallers) != 1 || !callerIs(snap.UncontractedCallers[0], want) {
		t.Errorf("callers %+v", snap.UncontractedCallers)
	}
}

func callerIs(a, b fleet.UncontractedCaller) bool {
	return a.Caller == b.Caller && a.ToService == b.ToService && a.Entity == b.Entity && slices.Equal(a.Sources, b.Sources)
}

func TestMeshRowRestricted(t *testing.T) {
	q := fleet.NewQuery(meshBuild(t, nil))
	row := meshProvider(t, q)
	if row.Status != fleet.SourcePartial || len(row.Objects) != 3 {
		t.Fatalf("row %+v", row)
	}
	r := meshProvider(t, q.Restrict(func(_, ns string) bool { return ns == "shop" }, func(_, ns, _ string) bool { return ns == "shop" }))
	if len(r.Objects) != 0 || r.ObjectsTotal != 3 || r.Evidence != row.Evidence {
		t.Errorf("restricted row %+v", r)
	}
	if !slices.ContainsFunc(r.Limitations, func(l fleet.Limitation) bool { return l.Message == "1 namespace without sidecars" }) {
		t.Errorf("restricted limitations %+v", r.Limitations)
	}
}

// meshProvider is the Istio row of q.
func meshProvider(t *testing.T, q *fleet.Query) fleet.Provider {
	t.Helper()
	for _, p := range q.Providers().Providers {
		if p.ID == fleet.ProviderIstio {
			return p
		}
	}
	t.Fatal("no Istio row")
	return fleet.Provider{}
}
