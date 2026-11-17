package fleet

import (
	"reflect"
	"slices"
	"testing"
	"time"
)

// hoursAgo is a time n hours before the fixed clock, as a pointer.
func hoursAgo(n int) *time.Time { return new(fixedNow().Add(-time.Duration(n) * time.Hour)) }

// cartTarget is the Pacto target of service in shop that runs as the cart
// Deployment, created at created.
func cartTarget(service string, created *time.Time) RawTarget {
	t := workload("shop", service)
	t.Workload = &WorkloadRef{Kind: "Deployment", Name: "cart"}
	t.CreatedAt = created
	return t
}

// cartWorkload is the cart Deployment of shop, created at created.
func cartWorkload(created *time.Time) RawInferredService {
	w := inferredCart()
	w.CreatedAt = created
	return w
}

// replaceSnap is one cluster with the given Pacto targets, the cart workload
// created at created and a kernel that saw edges.
func replaceSnap(t *testing.T, created *time.Time, targets []RawTarget, edges ...ObservedEdge) *FleetSnapshot {
	t.Helper()
	cl := clusterOf(clusterA, "eu", ClusterNameFromContext)
	return buildClusterSnap(t,
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Cluster: cl, Targets: targets}),
		workloadsSource("k8s-workloads", cl, cartWorkload(created)),
		kernel(edges...))
}

func TestReplaceRecordsPactoTarget(t *testing.T) {
	snap := replaceSnap(t, hoursAgo(72), []RawTarget{cartTarget("cart-svc", hoursAgo(1))})
	if snap.Services[NewServiceKey(cartName)] != nil {
		t.Fatal("the targeted workload is still an inferred service")
	}
	if st := sourceState(t, snap, "k8s-workloads"); st.InferredCount != 0 {
		t.Errorf("inferred count %d", st.InferredCount)
	}
	if n := NewQuery(snap).Overview().Summary.InferredServices; n != 0 {
		t.Errorf("overview inferred services %d", n)
	}
	want := []Replacement{{
		From: NewServiceKey(cartName), To: NewServiceKey("cart-svc"), Link: "pacto-target",
		Target: NewTargetKey("shop", "kubernetes", "cart-svc"), At: hoursAgo(1),
		workload: workloadKey{namespace: "shop", kind: "Deployment", name: "cart"},
	}}
	if !reflect.DeepEqual(snap.Replacements, want) {
		t.Errorf("replacements %+v\nwant %+v", snap.Replacements, want)
	}
}

func TestReplacedWorkloadCallsResolveToDeclared(t *testing.T) {
	snap := replaceSnap(t, hoursAgo(72), []RawTarget{cartTarget("cart-svc", hoursAgo(1)), workload("shop", "orders")},
		call(cartName, "orders"), call("orders", cartName))
	cart, orders := NewServiceKey("cart-svc"), NewServiceKey("orders")
	if k, res := snap.ObservedNameResolver()(cartName); k != cart || res != ObservedResolved {
		t.Errorf("resolved %s %v", k, res)
	}
	for _, e := range [][2]ServiceKey{{cart, orders}, {orders, cart}} {
		if !slices.ContainsFunc(snap.Relationships, func(r Relationship) bool {
			return r.Provenance == ProvenanceObserved && r.FromService == e[0] && r.ToService == e[1]
		}) {
			t.Errorf("no observed %s -> %s in %+v", e[0], e[1], snap.Relationships)
		}
	}
	if len(snap.UncontractedCallers) != 0 || len(snap.InferredRelationships) != 0 {
		t.Errorf("callers %+v inferred %+v", snap.UncontractedCallers, snap.InferredRelationships)
	}
}

func TestReplaceAmbiguousIsLimitation(t *testing.T) {
	snap := replaceSnap(t, hoursAgo(72), []RawTarget{cartTarget("cart-a", hoursAgo(1)), cartTarget("cart-b", hoursAgo(1))})
	if snap.Services[NewServiceKey(cartName)] == nil || len(snap.Replacements) != 0 {
		t.Fatalf("services %v replacements %+v", snap.Services, snap.Replacements)
	}
	lims := limitationsWith(snap, LimitationInferredReplacementAmbiguous)
	if len(lims) != 1 || !limitationMentions(snap, LimitationInferredReplacementAmbiguous, "(cart-a, cart-b)") ||
		!limitationMentions(snap, LimitationInferredReplacementAmbiguous, cartName) {
		t.Errorf("limitations %+v", lims)
	}
	if k, res := snap.ObservedNameResolver()(cartName); res != ObservedInferred || k != NewServiceKey(cartName) {
		t.Errorf("resolved %s %v", k, res)
	}
}

func TestReplaceUnknownClusterAmbiguous(t *testing.T) {
	a, b := clusterOf(clusterA, "eu", ClusterNameFromContext), clusterOf(clusterB, "us", ClusterNameFromContext)
	for _, tc := range []struct {
		name string
		srcs []Source
	}{
		{"two matches", []Source{workloadsSource("wa", a, cartWorkload(hoursAgo(72))), workloadsSource("wb", b, cartWorkload(hoursAgo(72)))}},
		{"one match", []Source{workloadsSource("wa", a, cartWorkload(hoursAgo(72))), NewMemorySource("kb", SourceKindKubernetes, &Collection{Cluster: b})}},
		{"no match", []Source{workloadsSource("wa", a, rawInferred("shop", "Deployment", "other")), NewMemorySource("kb", SourceKindKubernetes, &Collection{Cluster: b})}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pactos := NewMemorySource("k8s", SourceKindKubernetes, &Collection{Targets: []RawTarget{cartTarget("cart-svc", hoursAgo(1))}})
			snap := buildClusterSnap(t, append(tc.srcs, pactos)...)
			name := K8sCaller(clusterA, "shop", "Deployment", "cart")
			if len(snap.Replacements) != 0 {
				t.Fatalf("replacements %+v", snap.Replacements)
			}
			lims := limitationsWith(snap, LimitationInferredReplacementAmbiguous)
			if tc.name == "no match" {
				if len(lims) != 0 {
					t.Errorf("a target that names no inferred workload is ambiguous: %+v", lims)
				}
				return
			}
			if snap.Services[NewServiceKey(name)] == nil || len(lims) != 1 || !limitationMentions(snap, LimitationInferredReplacementAmbiguous, name) {
				t.Errorf("services %v limitations %+v", snap.Services, lims)
			}
		})
	}
}

func TestReplaceNeedsOlderWorkload(t *testing.T) {
	for _, pacto := range []*time.Time{hoursAgo(73), hoursAgo(72)} {
		snap := replaceSnap(t, hoursAgo(72), []RawTarget{cartTarget("cart-svc", pacto)})
		if snap.Services[NewServiceKey(cartName)] != nil || len(snap.Replacements) != 0 {
			t.Errorf("pacto at %v: services %v replacements %+v", pacto, snap.Services, snap.Replacements)
		}
		if k, res := snap.ObservedNameResolver()(cartName); k != NewServiceKey("cart-svc") || res != ObservedResolved {
			t.Errorf("resolved %s %v", k, res)
		}
		for _, it := range attentionOf(t, NewQuery(snap), AttentionFilter{Category: categoryKnowledge}).Items {
			if it.Code == attentionInferredReplaced {
				t.Errorf("pacto at %v: attention %+v", pacto, it)
			}
		}
	}
}

func TestReplaceResolvesTargetWithNoWorkloadSource(t *testing.T) {
	cl := clusterOf(clusterA, "eu", ClusterNameFromContext)
	snap := buildClusterSnap(t, NewMemorySource("k8s", SourceKindKubernetes, &Collection{Cluster: cl, Targets: []RawTarget{
		cartTarget("cart-svc", hoursAgo(1)), cartTarget("pay-a", nil), cartTarget("pay-b", nil),
	}}))
	if len(snap.Replacements) != 0 || len(limitationsWith(snap, LimitationInferredReplacementAmbiguous)) != 0 {
		t.Fatalf("replacements %+v limitations %+v", snap.Replacements, snap.Limitations)
	}
	if k, res := snap.ObservedNameResolver()(cartName); res != ObservedUnknown {
		t.Errorf("a workload three services target resolved to %s %v", k, res)
	}
	one := buildClusterSnap(t, NewMemorySource("k8s", SourceKindKubernetes, &Collection{Cluster: cl, Targets: []RawTarget{cartTarget("cart-svc", hoursAgo(1))}}))
	if k, res := one.ObservedNameResolver()(cartName); k != NewServiceKey("cart-svc") || res != ObservedResolved {
		t.Errorf("resolved %s %v", k, res)
	}
}

func TestReplaceUnknownClusterSkipsReplacedWorkload(t *testing.T) {
	a, b := clusterOf(clusterA, "eu", ClusterNameFromContext), clusterOf(clusterB, "us", ClusterNameFromContext)
	snap := buildClusterSnap(t,
		workloadsSource("wa", a, cartWorkload(hoursAgo(72))),
		NewMemorySource("ka", SourceKindKubernetes, &Collection{Cluster: a, Targets: []RawTarget{cartTarget("cart-svc", hoursAgo(1))}}),
		NewMemorySource("kb", SourceKindKubernetes, &Collection{Cluster: b}),
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Targets: []RawTarget{cartTarget("cart-other", hoursAgo(1))}}))
	if len(snap.Replacements) != 1 {
		t.Fatalf("replacements %+v", snap.Replacements)
	}
	if lims := limitationsWith(snap, LimitationInferredReplacementAmbiguous); len(lims) != 0 {
		t.Errorf("an already replaced workload is named as inferred: %+v", lims)
	}
}

func TestReplaceUnknownTimesRecordsNothing(t *testing.T) {
	for _, tc := range [][2]*time.Time{{nil, hoursAgo(1)}, {hoursAgo(72), nil}} {
		snap := replaceSnap(t, tc[0], []RawTarget{cartTarget("cart-svc", tc[1])})
		if snap.Services[NewServiceKey(cartName)] != nil || len(snap.Replacements) != 0 {
			t.Errorf("times %v: services %v replacements %+v", tc, snap.Services, snap.Replacements)
		}
		if _, res := snap.ObservedNameResolver()(cartName); res != ObservedResolved {
			t.Errorf("times %v: resolution %v", tc, res)
		}
	}
}

func TestRestrictReplacementNeedsBothSides(t *testing.T) {
	q := NewQuery(replaceSnap(t, hoursAgo(72), []RawTarget{cartTarget("cart-svc", hoursAgo(1)), workload("web", "front")}))
	all := func(string) bool { return true }
	allKinds := func(string, string) bool { return true }
	if r := q.Restrict(func(ns string) bool { return ns != "web" }, allKinds); len(r.snap.Replacements) != 1 {
		t.Errorf("both sides visible: %+v", r.snap.Replacements)
	}
	noDeployments := func(_, kind string) bool { return kind != "Deployment" }
	if r := q.Restrict(all, noDeployments); len(r.snap.Replacements) != 0 {
		t.Errorf("hidden workload kind: %+v", r.snap.Replacements)
	}
	r := q.Restrict(func(ns string) bool { return ns != "shop" }, allKinds)
	if len(r.snap.Replacements) != 0 {
		t.Errorf("hidden namespace: %+v", r.snap.Replacements)
	}
	if _, res := r.snap.ObservedNameResolver()(cartName); res != ObservedUnknown {
		t.Errorf("a replaced name resolves to a hidden service: %v", res)
	}
}

func TestReplaceIsDeterministic(t *testing.T) {
	cl := clusterOf(clusterA, "eu", ClusterNameFromContext)
	ledgerTarget := workload("shop", "ledger-svc")
	ledgerTarget.Workload, ledgerTarget.CreatedAt = &WorkloadRef{Kind: "StatefulSet", Name: "ledger"}, hoursAgo(2)
	ledger := rawInferred("shop", "StatefulSet", "ledger")
	ledger.CreatedAt = hoursAgo(90)
	srcs := []Source{
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Cluster: cl, Targets: []RawTarget{cartTarget("cart-svc", hoursAgo(1)), ledgerTarget}}),
		workloadsSource("w0", nil, ledger),
		workloadsSource("w1", nil, cartWorkload(hoursAgo(72))),
		workloadsSource("w2", nil, cartWorkload(nil)),
		workloadsSource("w3", nil, cartWorkload(hoursAgo(80))),
	}
	// Cluster names break ties by source order, so only the sources that
	// report no cluster move.
	a := buildClusterSnap(t, srcs...)
	b := buildClusterSnap(t, srcs[0], srcs[4], srcs[3], srcs[2], srcs[1])
	if !reflect.DeepEqual(a.Replacements, b.Replacements) || a.SnapshotID != b.SnapshotID || len(a.Replacements) != 2 || !a.Replacements[0].At.Equal(*hoursAgo(1)) {
		t.Errorf("%+v %s\n%+v %s", a.Replacements, a.SnapshotID, b.Replacements, b.SnapshotID)
	}
}
