package fleetsrc

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// egressSeries is one Istio series from src in shop to an unknown
// destination whose service name is svc.
func egressSeries(src, svc, v string, extra ...string) string {
	return series("source", src, "unknown", v, append([]string{"destination_workload_namespace=unknown", "destination_service_name=" + svc}, extra...)...)
}

func TestIstioEgressSeriesBecomeHostEdges(t *testing.T) {
	p := newProm(t, answer(vector(egressSeries("cart", "api.stripe.com", "3"), egressSeries("cart", "API.Stripe.com", "2"),
		egressSeries("cart", "ledger.shop.svc.cluster.local", "4"))))
	col := collectMesh(t, meshSource(defaultMesh(true), nil, map[string]*promStub{"istio-system/prom-main": p}, nil))
	want := []fleet.ObservedEdge{{From: "k8s:shop//cart", To: "api.stripe.com", Count: 5, FirstSeen: meshNow.Add(-time.Hour), LastSeen: meshNow}}
	if !slices.Equal(col.Observed, want) {
		t.Errorf("observed %+v", col.Observed)
	}
	// An unknown destination named by an in-cluster Service name is still unattributed.
	if row := istioRow(t, col); !hasMessage(row, fleet.LimitationMeshCallsUnattributed, "1 call series had an end Pacto could not name") {
		t.Errorf("row %v", messages(row))
	}

	snap := meshEgressBuild(t, vector(series("source", "payments", "unknown", "4", "source_workload_namespace=prod",
		"destination_workload_namespace=unknown", "destination_service_name=api.stripe.com")))
	pay := fleet.NewServiceKey("payments")
	if !slices.ContainsFunc(snap.Relationships, func(r fleet.Relationship) bool {
		return r.FromService == pay && r.ToExternal == "stripe" && r.Provenance == fleet.ProvenanceObserved
	}) {
		t.Errorf("relationships %+v", snap.Relationships)
	}
}

func TestIstioEgressGatewayDestinationAttributedToSource(t *testing.T) {
	gw := func(ns string) string {
		return series("source", "cart", "istio-egressgateway", "6", "destination_workload_namespace="+ns, "destination_service_name=api.stripe.com")
	}
	p := newProm(t, answer(vector(gw("istio-system"), gw("shop"),
		series("source", "cart", "istio-egressgateway", "1", "destination_workload_namespace=istio-system",
			"destination_service_name=istio-egressgateway.istio-system.svc.cluster.local"))))
	col := collectMesh(t, meshSource(defaultMesh(true), nil, map[string]*promStub{"istio-system/prom-main": p}, nil))
	got := map[string]int{}
	for _, e := range col.Observed {
		got[e.From+" -> "+e.To] = e.Count
	}
	// Only a gateway in the gateway namespace speaks for the host; a copy in a
	// tenant's namespace and a call to the gateway's own Service stay in-cluster edges.
	want := map[string]int{"k8s:shop//cart -> api.stripe.com": 6, "k8s:shop//cart -> k8s:shop//istio-egressgateway": 6,
		"k8s:shop//cart -> k8s:istio-system//istio-egressgateway": 1}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("observed %v", got)
	}
}

func TestIstioPassthroughIsPartial(t *testing.T) {
	p := newProm(t, answer(vector(egressSeries("cart", "PassthroughCluster", "3"), egressSeries("cart", "BlackHoleCluster", "1"))))
	col := collectMesh(t, meshSource(defaultMesh(true), nil, map[string]*promStub{"istio-system/prom-main": p}, nil))
	row := istioRow(t, col)
	if len(col.Observed) != 0 || row.Status != fleet.SourcePartial ||
		!hasMessage(row, fleet.LimitationMeshCallsUnattributed, "1 call series went through the passthrough cluster, which names no host; add a ServiceEntry") ||
		!hasMessage(row, fleet.LimitationMeshCallsUnattributed, "1 call series had an end Pacto could not name") {
		t.Errorf("row %v observed %+v", messages(row), col.Observed)
	}
}

func TestIstioInClusterSeriesUnchanged(t *testing.T) {
	read := func(body string) []fleet.ObservedEdge {
		p := newProm(t, answer(body))
		return collectMesh(t, meshSource(defaultMesh(true), nil, map[string]*promStub{"istio-system/prom-main": p}, nil)).Observed
	}
	base := read(istioAnswer())
	// The same calls split by destination Service name, beside egress, fold to the same edges.
	split := read(vector(series("source", "cart", "ledger", "6.4", "destination_service_name=ledger.shop.svc.cluster.local"),
		series("source", "cart", "ledger", "4", "destination_service_name=ledger-v2.shop.svc.cluster.local"),
		series("destination", "cart", "ledger", "12.6"), series("source", "cart", "payments", "0.3"),
		series("source", "ledger", "cart", "0"), series("source", "cart", "cart", "5"), egressSeries("cart", "api.stripe.com", "9")))
	if len(base) != 2 || len(split) != 3 || !slices.Equal(withoutHosts(split), base) {
		t.Errorf("in-cluster edges changed: %+v vs %+v", withoutHosts(split), base)
	}
}

// withoutHosts is edges without those to a host.
func withoutHosts(edges []fleet.ObservedEdge) []fleet.ObservedEdge {
	return slices.DeleteFunc(slices.Clone(edges), func(e fleet.ObservedEdge) bool { return !strings.HasPrefix(e.To, "k8s:") })
}

func TestIstioEgressScopedQueryKeepsUnknownDestinations(t *testing.T) {
	p := newProm(t, answer(vector(egressSeries("cart", "api.stripe.com", "1"))))
	col := collectMesh(t, meshSource(defaultMesh(true), []string{"shop", "istio-system"}, map[string]*promStub{"istio-system/prom-main": p}, nil))
	q, _ := url.ParseQuery(p.reqs[0].URL.RawQuery)
	if !strings.Contains(q.Get("query"), `destination_workload_namespace=~"istio-system|shop|unknown"`) ||
		!strings.Contains(q.Get("query"), "destination_service_name") || len(col.Observed) != 1 {
		t.Errorf("query %q observed %+v", q.Get("query"), col.Observed)
	}
}

func TestLinkerdAuthorityEgress(t *testing.T) {
	p := newProm(t, answer(vector(
		lkSeries("3", "namespace=shop", "deployment=cart", "authority=api.stripe.com:443"),
		lkSeries("2", "namespace=shop", "deployment=cart", "authority=[2001:db8::1]:443"),
		lkSeries("1", "namespace=shop", "deployment=cart", "authority=ledger.shop.svc.cluster.local:8080"),
		lkSeries("4", "namespace=shop", "deployment=cart", "dst_namespace=shop", "dst_statefulset=ledger", "authority=ledger.shop.svc.cluster.local:8080"))))
	col := collectMesh(t, meshSource(linkerdMesh(nil), []string{"shop", "linkerd-viz"}, map[string]*promStub{"linkerd-viz/prometheus": p}, nil))
	want := []fleet.ObservedEdge{
		{From: "k8s:shop/Deployment/cart", To: "k8s:shop/StatefulSet/ledger", Count: 4, FirstSeen: meshNow.Add(-time.Hour), LastSeen: meshNow},
		{From: "k8s:shop/Deployment/cart", To: "api.stripe.com", Count: 3, FirstSeen: meshNow.Add(-time.Hour), LastSeen: meshNow},
	}
	if !slices.Equal(col.Observed, want) {
		t.Errorf("observed %+v", col.Observed)
	}
	q, _ := url.ParseQuery(p.reqs[0].URL.RawQuery)
	if !strings.Contains(q.Get("query"), `dst_namespace=~"|linkerd-viz|shop"`) || !strings.Contains(q.Get("query"), "authority") {
		t.Errorf("query %q", q.Get("query"))
	}
}

func TestHubbleWorldFlowWithNamesIsEgress(t *testing.T) {
	at := meshNow.Add(-59 * time.Minute)
	other := worldCall(at, ep("other", "Deployment/x", ""), "api.stripe.com")
	row, col, _ := readHubble(t, worldCall(at, cart(), "api.stripe.com", "stripe.com"), worldCall(at.Add(time.Minute), cart(), "API.stripe.com."),
		worldCallID(at, cart(), "api.github.com"), call(at, cart(), ledger()), other,
		flowFrame(at, verdictForwarded, directionEgress, true, cart(), pb(4, "reserved:world")))
	want := []fleet.ObservedEdge{
		{From: "k8s:shop/Deployment/cart", To: "api.stripe.com", Count: 2, FirstSeen: at, LastSeen: at.Add(time.Minute)},
		{From: "k8s:other/Deployment/x", To: "api.stripe.com", Count: 1, FirstSeen: at, LastSeen: at},
		{From: "k8s:shop/Deployment/cart", To: "api.github.com", Count: 1, FirstSeen: at, LastSeen: at},
		{From: "k8s:shop/Deployment/cart", To: "k8s:shop/StatefulSet/ledger", Count: 1, FirstSeen: at, LastSeen: at},
	}
	if !sameEdges(col.Observed, want) || row.Status != fleet.SourceAvailable {
		t.Errorf("observed %+v row %v", col.Observed, messages(row))
	}
	// A scoped read leaves a world flow of an unread namespace out.
	r := newRelay(t, other)
	scoped := hubbleRowOf(t, collectMesh(t, hubbleSource(hubbleMesh(true, defaultRelay), []string{"shop", "kube-system"}, r)))
	if !hasMessage(scoped, fleet.LimitationMeshCallsUnattributed, "1 flow reaches namespaces this installation does not read") {
		t.Errorf("scoped %v", messages(scoped))
	}
}

// sameEdges reports whether a and b hold the same edges in any order.
func sameEdges(a, b []fleet.ObservedEdge) bool {
	key := func(e fleet.ObservedEdge) string { return fmt.Sprint(e) }
	ka, kb := make([]string, len(a)), make([]string, len(b))
	for i := range a {
		ka[i] = key(a[i])
	}
	for i := range b {
		kb[i] = key(b[i])
	}
	slices.Sort(ka)
	slices.Sort(kb)
	return slices.Equal(ka, kb)
}

func TestHubbleWorldFlowWithoutNamesPartial(t *testing.T) {
	at := meshNow.Add(-10 * time.Minute)
	row, col, _ := readHubble(t, worldCall(at, cart()), worldCall(at, cart(), "localhost"), call(at, cart(), ledger()))
	if len(col.Observed) != 1 || row.Status != fleet.SourcePartial ||
		!hasMessage(row, fleet.LimitationMeshCallsUnattributed, "2 flows left the cluster with no name; enable DNS visibility for egress names") {
		t.Errorf("row %v observed %+v", messages(row), col.Observed)
	}
}

func TestMeshEgressSeriesCapped(t *testing.T) {
	items := make([]string, 0, maxEgressSeries+1)
	for i := range maxEgressSeries + 1 {
		items = append(items, egressSeries("cart", fmt.Sprintf("h%d.example.com", i), fmt.Sprint(i+1)))
	}
	p := newProm(t, answer(vector(items...)))
	col := collectMesh(t, meshSource(defaultMesh(true), nil, map[string]*promStub{"istio-system/prom-main": p}, nil))
	row := istioRow(t, col)
	if len(col.Observed) != maxEgressSeries || col.Observed[0].To != fmt.Sprintf("h%d.example.com", maxEgressSeries) ||
		!hasMessage(row, fleet.LimitationEgressSeriesCap, "only the 2000 busiest calls to external hosts were read") {
		t.Errorf("observed %d lims %v", len(col.Observed), messages(row))
	}

	at := meshNow.Add(-10 * time.Minute)
	frames := make([][]byte, 0, maxEgressSeries+1)
	for i := range maxEgressSeries + 1 {
		frames = append(frames, worldCall(at, cart(), fmt.Sprintf("h%d.example.com", i)))
	}
	hrow, hcol, _ := readHubble(t, frames...)
	if len(hcol.Observed) != maxEgressSeries || !hasMessage(hrow, fleet.LimitationEgressSeriesCap, "only the 2000 busiest calls to external hosts were read") {
		t.Errorf("hubble observed %d lims %v", len(hcol.Observed), messages(hrow))
	}
}

func TestMeshEgressRowsUseExternalEgress(t *testing.T) {
	p := newProm(t, answer(istioAnswer()))
	read := istioRow(t, collectMesh(t, meshSource(defaultMesh(true), nil, map[string]*promStub{"istio-system/prom-main": p}, nil)))
	if !slices.Equal(read.UsedFor, []string{fleet.UseObservedEdges, fleet.UseExternalEgress}) {
		t.Errorf("read row %v", read.UsedFor)
	}
	unread := istioRow(t, collectMesh(t, meshSource(defaultMesh(true), nil, nil, nil)))
	if !slices.Equal(unread.UsedFor, []string{fleet.UseObservedEdges}) {
		t.Errorf("unread row %v", unread.UsedFor)
	}
	hrow, _, _ := readHubble(t, call(meshNow.Add(-time.Minute), cart(), ledger()))
	if !slices.Equal(hrow.UsedFor, []string{fleet.UseObservedEdges, fleet.UseExternalEgress}) {
		t.Errorf("hubble row %v", hrow.UsedFor)
	}
}

func TestMeshEgressFromInferredServiceIsInferredEdge(t *testing.T) {
	snap := meshEgressBuild(t, vector(egressSeries("cart", "api.stripe.com", "4")))
	cart := fleet.NewServiceKey("k8s:shop/Deployment/cart")
	if !slices.ContainsFunc(snap.InferredRelationships, func(r fleet.Relationship) bool {
		return r.FromService == cart && r.ToExternal == "stripe" && r.Provenance == fleet.ProvenanceInferred
	}) {
		t.Errorf("inferred relationships %+v", snap.InferredRelationships)
	}
	if slices.ContainsFunc(snap.Relationships, func(r fleet.Relationship) bool { return r.ToExternal != "" }) {
		t.Errorf("an inferred caller's egress is observed: %+v", snap.Relationships)
	}
}

// meshEgressBuild is meshBuild with the Prometheus answering body.
func meshEgressBuild(t *testing.T, body string) *fleet.FleetSnapshot {
	t.Helper()
	f := meshClient(true,
		[]string{svcJSON("istio-system", "istiod", "", "app=istiod"), svcJSON("istio-system", "prom-main", "", "app.kubernetes.io/name=prometheus"),
			svcJSON("shop", "cart", "")},
		[]string{nsJSON("istio-system"), nsJSON("shop", "istio-injection=enabled")})
	workloads := &workloadFake{pages: map[string][2]string{
		"deployments||": {workloadJSON("shop", "cart", "cart:1") + "," + workloadJSON("prod", "payments", "pay:1")},
	}}
	cl := &fleet.Cluster{ID: "c1", Name: "eu"}
	mesh := meshSource(f, nil, map[string]*promStub{"istio-system/prom-main": newProm(t, answer(body))}, nil)
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

// worldCall is an egress request flow from src to the world whose
// destination names are names.
func worldCall(at time.Time, src []byte, names ...string) []byte {
	kv := []any{1, stamp(at), 2, uint64(verdictForwarded), 8, src, 9, pb(4, "reserved:world-ipv4"), 22, uint64(directionEgress), 26, pb(1, uint64(0))}
	for _, n := range names {
		kv = append(kv, 14, n)
	}
	return pb(1, pb(kv...))
}

// worldCallID is worldCall to an endpoint marked world by its identity only.
func worldCallID(at time.Time, src []byte, name string) []byte {
	return pb(1, pb(1, stamp(at), 2, uint64(verdictForwarded), 8, src, 9, pb(2, uint64(2)), 22, uint64(directionEgress), 26, pb(1, uint64(0)), 14, name))
}

// TestEgressHostKeepsOnlyHostsTheFleetFolds: an address, a private-suffix name or an in-cluster name is no
// External, so it never becomes an edge (the caller counts it as a call it could not name); a wildcard
// ServiceEntry name loses its "*.".
func TestEgressHostKeepsOnlyHostsTheFleetFolds(t *testing.T) {
	for in, want := range map[string]string{
		"api.stripe.com:443": "api.stripe.com", "API.Stripe.com.": "api.stripe.com", "*.files.stripe.com": "files.stripe.com",
		"10.1.2.3:8080": "", "203.0.113.7": "", "db.corp.internal": "", "ledger.shop.svc.cluster.local": "", "legacy": "",
	} {
		if got := egressHost(in); got != want {
			t.Errorf("egressHost(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestHubbleWorldFlowNamesAcrossDomainsIsUnnamed: Cilium lists every name that resolved to the address, so
// names under two registrable domains (a shared CDN address) name no vendor; one domain names its first host.
func TestHubbleWorldFlowNamesAcrossDomainsIsUnnamed(t *testing.T) {
	at := meshNow.Add(-10 * time.Minute)
	row, col, _ := readHubble(t, worldCall(at, cart(), "api.vendor-a.com", "assets.vendor-b.com"),
		worldCall(at, cart(), "api.stripe.com", "files.stripe.com"))
	want := []fleet.ObservedEdge{{From: "k8s:shop/Deployment/cart", To: "api.stripe.com", Count: 1, FirstSeen: at, LastSeen: at}}
	if !sameEdges(col.Observed, want) || row.Status != fleet.SourcePartial ||
		!hasMessage(row, fleet.LimitationMeshCallsUnattributed, "1 flow left the cluster with no name; enable DNS visibility for egress names") {
		t.Errorf("row %v observed %+v", messages(row), col.Observed)
	}
}
