package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const cartName = "k8s:shop/Deployment/cart"

// inferredCart is the cart Deployment of shop as a workload source reads it.
func inferredCart() RawInferredService {
	at := fixedNow().Add(-time.Minute)
	c := "spec.template.spec.containers[name=cart]"
	return RawInferredService{
		Namespace: "shop", Kind: "Deployment", Name: "cart",
		Image:      &InferredValue{Value: "ghcr.io/acme/cart:1.4.2", From: c + ".image"},
		Ports:      []InferredPort{{Port: 8080, Name: "http", Protocol: "TCP", From: c + ".ports[0]"}},
		Probes:     []InferredProbe{{Kind: "readiness", Path: "/healthz", Port: "8080", From: c + ".readinessProbe.httpGet"}},
		ObservedAt: &at,
	}
}

func rawInferred(ns, kind, name string) RawInferredService {
	return RawInferredService{Namespace: ns, Kind: kind, Name: name}
}

func workloadsSource(id string, cluster *Cluster, in ...RawInferredService) Source {
	return NewMemorySource(id, SourceKindKubernetesWorkloads, &Collection{Cluster: cluster, InferredServices: in})
}

func sourceState(t *testing.T, snap *FleetSnapshot, id string) SourceState {
	t.Helper()
	for _, st := range snap.Sources {
		if st.ID == id {
			return st
		}
	}
	t.Fatalf("no source %q", id)
	return SourceState{}
}

func limitationMentions(snap *FleetSnapshot, code, text string) bool {
	return slices.ContainsFunc(limitationsWith(snap, code), func(l Limitation) bool { return strings.Contains(l.Message, text) })
}

func unverifiedRecord(snap *FleetSnapshot, name string) bool {
	s := snap.Services[NewServiceKey(name)]
	return s != nil && s.Workload.DeclarationUnverified
}

func checkCartWorkload(t *testing.T, w *InferredWorkload) {
	t.Helper()
	c := inferredCart()
	// Whether the fixture verified the declaration is each test's to check.
	want := &InferredWorkload{Source: "k8s-workloads", Namespace: "shop", Kind: "Deployment", Name: "cart", Image: c.Image, Ports: c.Ports, Probes: c.Probes, ObservedAt: c.ObservedAt, DeclarationUnverified: w.DeclarationUnverified}
	if !reflect.DeepEqual(w, want) {
		t.Errorf("workload %+v, want %+v", w, want)
	}
}

func TestInferredServiceRecord(t *testing.T) {
	snap := buildClusterSnap(t,
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Targets: []RawTarget{workload("payments", "payments")}}),
		workloadsSource("k8s-workloads", nil, inferredCart()))
	s := snap.Services[NewServiceKey(cartName)]
	if s == nil {
		t.Fatalf("no inferred service in %v", snap.Services)
	}
	if s.Declaration != DeclarationInferred || !s.Inferred() || s.Name != cartName {
		t.Errorf("declaration %q name %q", s.Declaration, s.Name)
	}
	if !s.BusinessDomainUnknown || !s.SummaryUnknown || s.Status != "" || len(s.Targets) != 0 || len(s.Revisions) != 0 {
		t.Errorf("inferred record claims something: %+v", s)
	}
	if !slices.Equal(s.Sources, []string{"k8s-workloads"}) {
		t.Errorf("sources %v", s.Sources)
	}
	checkCartWorkload(t, s.Workload)
	if p := snap.Services[NewServiceKey("payments")]; p.Declaration != DeclarationDeclared || p.Inferred() {
		t.Errorf("declared service reads %q", p.Declaration)
	}
	if (&ServiceRecord{}).Inferred() {
		t.Error("a record with no declaration reads as inferred")
	}
}

func TestInferredServiceCarriesNoTargets(t *testing.T) {
	k8s := func() Source {
		return NewMemorySource("k8s", SourceKindKubernetes, &Collection{Targets: []RawTarget{workload("payments", "payments")}})
	}
	without := buildClusterSnap(t, k8s())
	with := buildClusterSnap(t, k8s(), workloadsSource("k8s-workloads", nil, inferredCart()))
	a, _ := json.Marshal(without.Targets)
	b, _ := json.Marshal(with.Targets)
	if string(a) != string(b) {
		t.Errorf("targets changed:\n%s\n%s", a, b)
	}
	ov := func(s *FleetSnapshot) [6]int {
		o := NewQuery(s).Overview().Summary
		return [6]int{o.Targets, o.ExactTargetLinks, o.InferredTargetLinks, o.AmbiguousTargetLinks, o.UnresolvedTargetLinks, o.CompliantTargets}
	}
	if ov(without) != ov(with) {
		t.Errorf("target tallies %v != %v", ov(without), ov(with))
	}
	if st := sourceState(t, with, "k8s-workloads"); st.TargetCount != 0 || st.RevisionCount != 0 {
		t.Errorf("workload source counts %+v", st)
	}
}

func TestInferredNameSingleCluster(t *testing.T) {
	snap := buildClusterSnap(t, workloadsSource("k8s-workloads", clusterOf(clusterA, "eu-prod", ClusterNameFromContext), inferredCart()))
	s := snap.Services[NewServiceKey(cartName)]
	if s == nil || s.Workload.Cluster != clusterA {
		t.Fatalf("want %s in cluster %s, got %v", cartName, clusterA, snap.Services)
	}
}

func TestInferredNameMultiCluster(t *testing.T) {
	snap := buildClusterSnap(t,
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Cluster: clusterOf(clusterA, "eu", ClusterNameFromContext), Targets: []RawTarget{workload("payments", "payments")}}),
		workloadsSource("k8s-workloads", clusterOf(clusterB, "us", ClusterNameFromContext), inferredCart()))
	name := K8sCaller(clusterB, "shop", "Deployment", "cart")
	if s := snap.Services[NewServiceKey(name)]; s == nil || s.Name != name || s.Workload.Cluster != clusterB {
		t.Fatalf("want %s, got %v", name, snap.Services)
	}
}

// declaringSnap builds a cluster-A Pacto source whose target in shop runs as
// ref, beside a workload source reporting in.
func declaringSnap(t *testing.T, ref WorkloadRef, wlCluster string, in ...RawInferredService) *FleetSnapshot {
	t.Helper()
	tgt := workload("shop", "ledger")
	tgt.Workload = &ref
	return buildClusterSnap(t,
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Cluster: clusterOf(clusterA, "eu", ClusterNameFromContext), Targets: []RawTarget{tgt}}),
		workloadsSource("k8s-workloads", clusterOf(wlCluster, wlCluster[:4], ClusterNameFromContext), in...))
}

func TestDeclaredTargetWorkloadNotInferred(t *testing.T) {
	snap := declaringSnap(t, WorkloadRef{Kind: "StatefulSet", Name: "ledger"}, clusterA,
		rawInferred("shop", "StatefulSet", "ledger"), rawInferred("web", "StatefulSet", "ledger"), rawInferred("shop", "Deployment", "ledger"))
	if snap.Services[NewServiceKey("k8s:shop/StatefulSet/ledger")] != nil {
		t.Error("a targeted workload was inferred")
	}
	for _, n := range []string{"k8s:web/StatefulSet/ledger", "k8s:shop/Deployment/ledger"} {
		if snap.Services[NewServiceKey(n)] == nil {
			t.Errorf("%s is missing", n)
		}
	}
	other := declaringSnap(t, WorkloadRef{Kind: "StatefulSet", Name: "ledger"}, clusterB, rawInferred("shop", "StatefulSet", "ledger"))
	if n := K8sCaller(clusterB, "shop", "StatefulSet", "ledger"); other.Services[NewServiceKey(n)] == nil {
		t.Errorf("the same workload in another cluster is missing: %v", other.Services)
	}
}

func TestDeclaredTargetDefaultKindNotInferred(t *testing.T) {
	snap := declaringSnap(t, WorkloadRef{Kind: "Deployment", Name: "ledger"}, clusterA, rawInferred("shop", "Deployment", "ledger"))
	for k, s := range snap.Services {
		if s.Inferred() {
			t.Errorf("inferred %s", k)
		}
	}
}

func TestInferredServicesBoundedPerSource(t *testing.T) {
	in := make([]RawInferredService, MaxInferredServices+1)
	for i := range in {
		in[i] = rawInferred("shop", "Deployment", fmt.Sprintf("svc-%05d", MaxInferredServices-i))
	}
	snap := buildClusterSnap(t, workloadsSource("k8s-workloads", nil, in...))
	n := 0
	for _, s := range snap.Services {
		if s.Inferred() {
			n++
		}
	}
	if n != MaxInferredServices || snap.Services[NewServiceKey("k8s:shop/Deployment/svc-00000")] == nil {
		t.Errorf("kept %d", n)
	}
	if snap.Services[NewServiceKey(fmt.Sprintf("k8s:shop/Deployment/svc-%05d", MaxInferredServices))] != nil {
		t.Error("the last workload in order was kept")
	}
	st := sourceState(t, snap, "k8s-workloads")
	if st.Status != SourcePartial || st.InferredCount != MaxInferredServices {
		t.Errorf("state %+v", st)
	}
	if lims := limitationsWith(snap, LimitationInferredServicesCapped); len(lims) != 1 || lims[0].Source != "k8s-workloads" || !strings.Contains(lims[0].Message, "1 workloads") {
		t.Errorf("limitations %+v", lims)
	}
}

func TestInferredRecordDropsControlAndOversized(t *testing.T) {
	in := inferredCart()
	in.Image = &InferredValue{Value: "ghcr.io/acme/cart\x1b]0;pwn\x07", From: "spec.template.spec.containers[name=cart].image"}
	in.Probes = []InferredProbe{{Kind: "liveness", Path: "/" + strings.Repeat("a", 10240), From: "spec.template.spec.containers[name=cart].livenessProbe.httpGet"}, in.Probes[0]}
	in.Ports = append(in.Ports, InferredPort{Port: 9090, Name: "x\x07", From: "spec.template.spec.containers[name=cart].ports[1]"})
	long := inferredCart()
	long.Name, long.Image.Value = "big", strings.Repeat("i", MaxInferredImageRunes+1)
	bad := rawInferred("shop/x", "Deployment", "evil")
	snap := buildClusterSnap(t, workloadsSource("k8s-workloads", nil, in, long, bad))
	s := snap.Services[NewServiceKey(cartName)]
	if s == nil || s.Workload.Image != nil || len(s.Workload.Probes) != 1 || s.Workload.Probes[0].Kind != "readiness" || len(s.Workload.Ports) != 1 {
		t.Fatalf("workload %+v", s.Workload)
	}
	if snap.Services[NewServiceKey("k8s:shop/Deployment/big")].Workload.Image != nil {
		t.Error("oversized image kept")
	}
	for k := range snap.Services {
		if strings.Contains(string(k), "evil") {
			t.Errorf("an invalid identity was kept: %s", k)
		}
	}
	if !slices.Equal(s.Workload.Dropped, []string{"image", "ports[1]", "probes[0]"}) {
		t.Errorf("dropped %v", s.Workload.Dropped)
	}
	if got := snap.Services[NewServiceKey("k8s:shop/Deployment/big")].Workload.Dropped; !slices.Equal(got, []string{"image"}) {
		t.Errorf("big dropped %v", got)
	}
	// One count per source, never one limitation per record.
	for _, want := range []string{"workloads from source k8s-workloads (2)", "a workload with an invalid identity"} {
		if !limitationMentions(snap, LimitationSourceRecordInvalid, want) {
			t.Errorf("no limitation names %q: %+v", want, limitationsWith(snap, LimitationSourceRecordInvalid))
		}
	}
	data, _ := json.Marshal(snap)
	for _, leak := range []string{"pwn", `\u001b`, "\x1b", "aaaaaaaaaa", "iiiiiiiiii", "evil"} {
		if strings.Contains(string(data), leak) {
			t.Errorf("snapshot holds %q", leak)
		}
	}
}

func TestInferredRecordStripsProbeQuery(t *testing.T) {
	in := inferredCart()
	in.Probes = []InferredProbe{{Kind: "readiness", Path: "/healthz?token=s3cr3t", From: "a"}, {Kind: "liveness", Path: "/live#frag", From: "b"}}
	snap := buildClusterSnap(t, workloadsSource("k8s-workloads", nil, in))
	p := snap.Services[NewServiceKey(cartName)].Workload.Probes
	if len(p) != 2 || p[0].Path != "/healthz" || p[1].Path != "/live" {
		t.Errorf("probes %+v", p)
	}
	if data, _ := json.Marshal(snap); strings.Contains(string(data), "s3cr3t") {
		t.Error("probe query reached the snapshot")
	}
}

func TestInferredUnderUnreadablePactosIsUnverified(t *testing.T) {
	front := rawInferred("web", "Deployment", "front")
	far := rawInferred("ops", "Deployment", "tool")
	scoped := NewMemorySource("k8s", SourceKindKubernetes, &Collection{
		State:       &SourceState{Status: SourcePartial, Namespaces: []string{"web"}, NamespacesTotal: 1},
		Limitations: []Limitation{NamespaceUnreadableLimitation("k8s", "shop", "forbidden")},
	})
	snap := buildClusterSnap(t, scoped, workloadsSource("k8s-workloads", nil, inferredCart(), front, far))
	for _, n := range []string{cartName, "k8s:ops/Deployment/tool"} {
		if !unverifiedRecord(snap, n) {
			t.Errorf("%s is not unverified", n)
		}
	}
	if unverifiedRecord(snap, "k8s:web/Deployment/front") {
		t.Error("a namespace the source read is unverified")
	}
	if !limitationMentions(snap, LimitationDeclarationUnverified, "(services: 2, namespaces: 2)") || len(limitationsWith(snap, LimitationDeclarationUnverified)) != 1 {
		t.Errorf("want one count per source: %+v", limitationsWith(snap, LimitationDeclarationUnverified))
	}
	down := NewMemorySource("k8s", SourceKindKubernetes, nil)
	down.err = errors.New("unreachable")
	snap = buildClusterSnap(t, down, workloadsSource("k8s-workloads", nil, front))
	if !unverifiedRecord(snap, "k8s:web/Deployment/front") {
		t.Error("an unavailable Pacto source verified a declaration")
	}
	whole := NewMemorySource("k8s", SourceKindKubernetes, &Collection{})
	snap = buildClusterSnap(t, whole, workloadsSource("k8s-workloads", nil, front))
	if len(limitationsWith(snap, LimitationDeclarationUnverified)) != 0 {
		t.Error("a cluster-wide available source left a declaration unverified")
	}
}

func TestInferredUnverifiedMultiClusterMatchesCluster(t *testing.T) {
	snap := buildClusterSnap(t,
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Cluster: clusterOf(clusterA, "eu", ClusterNameFromContext)}),
		workloadsSource("wa", clusterOf(clusterA, "eu", ClusterNameFromContext), inferredCart()),
		workloadsSource("wb", clusterOf(clusterB, "us", ClusterNameFromContext), inferredCart()))
	if unverifiedRecord(snap, K8sCaller(clusterA, "shop", "Deployment", "cart")) {
		t.Error("cluster A was read")
	}
	if !unverifiedRecord(snap, K8sCaller(clusterB, "shop", "Deployment", "cart")) {
		t.Error("cluster B was never read")
	}
}

func TestInferredWithCRDAbsentIsVerified(t *testing.T) {
	absent := NewMemorySource("k8s", SourceKindKubernetes, &Collection{Limitations: []Limitation{{Code: LimitationPactoCRDNotInstalled, Source: "k8s", Message: "no CRD"}}})
	snap := buildClusterSnap(t, absent, workloadsSource("k8s-workloads", nil, inferredCart()))
	if snap.Services[NewServiceKey(cartName)] == nil || len(limitationsWith(snap, LimitationDeclarationUnverified)) != 0 {
		t.Errorf("limitations %+v", snap.Limitations)
	}
}

func TestInferredDuplicateAcrossSourcesMerges(t *testing.T) {
	newer := inferredCart()
	at := fixedNow()
	newer.ObservedAt = &at
	older := inferredCart()
	older.ObservedAt = nil
	older.Image = &InferredValue{Value: "bad\x1b", From: "image"}
	snap := buildClusterSnap(t, workloadsSource("a", nil, inferredCart()), workloadsSource("b", nil, newer), workloadsSource("c", nil, older))
	s := snap.Services[NewServiceKey(cartName)]
	if !slices.Equal(s.Sources, []string{"a", "b", "c"}) || !s.Workload.ObservedAt.Equal(at) {
		t.Errorf("merged %+v %v", s.Sources, s.Workload.ObservedAt)
	}
	if !slices.Equal(s.Workload.Dropped, []string{"image"}) {
		t.Errorf("dropped %v", s.Workload.Dropped)
	}
}

func TestInferredNameCollisionSkipped(t *testing.T) {
	tgt := workload("shop", "x")
	tgt.Service = cartName
	snap := buildClusterSnap(t, NewMemorySource("k8s", SourceKindKubernetes, &Collection{Targets: []RawTarget{tgt}}),
		workloadsSource("k8s-workloads", nil, inferredCart()))
	if s := snap.Services[NewServiceKey(cartName)]; s.Inferred() || s.Workload != nil {
		t.Errorf("collision overwrote a declared service: %+v", s)
	}
	if !limitationMentions(snap, LimitationSourceRecordInvalid, "k8s-workloads") {
		t.Errorf("no collision limitation: %+v", snap.Limitations)
	}
}

func TestInferredCountOnSourceState(t *testing.T) {
	snap := buildClusterSnap(t, workloadsSource("k8s-workloads", nil, inferredCart(), rawInferred("web", "CronJob", "sweep")))
	if st := sourceState(t, snap, "k8s-workloads"); st.InferredCount != 2 || st.Status != SourceAvailable || st.Kind != SourceKindKubernetesWorkloads {
		t.Errorf("state %+v", st)
	}
}

func TestOnlyInferredSnapshotIsComplete(t *testing.T) {
	snap := buildClusterSnap(t, workloadsSource("k8s-workloads", nil, inferredCart()))
	if snap.Completeness != CompletenessComplete {
		t.Errorf("completeness %s", snap.Completeness)
	}
}

func TestInferredServiceNeverSatisfiesDependency(t *testing.T) {
	c := extContract("checkout", "1.0.0", contract.Dependency{Name: cartName, Compatibility: "^1.0.0", Required: true})
	col := &Collection{Revisions: []RawRevision{{Bundle: bundleOf(c), Digest: "sha256:checkout"}}}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow},
		NewMemorySource("local", "local", col), workloadsSource("k8s-workloads", nil, inferredCart()))
	if err != nil {
		t.Fatal(err)
	}
	key := NewServiceKey(cartName)
	if !snap.Services[key].Inferred() {
		t.Fatal("no inferred cart")
	}
	if len(snap.reverseDeps[key]) != 0 || len(snap.forwardDeps[NewServiceKey("checkout")]) != 0 {
		t.Errorf("the dependency resolved: %v %v", snap.reverseDeps, snap.forwardDeps)
	}
	for _, r := range snap.Relationships {
		if r.ToService == key {
			t.Errorf("relationship to the inferred service: %+v", r)
		}
	}
}
