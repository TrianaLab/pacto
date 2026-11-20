package fleet

import (
	"context"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// restrictGoldenID is the snapshot id of twoClusterFleet restricted by
// goldenVisible and goldenWorkloads, recorded on the code before Restrict read
// clusters: a predicate that ignores the cluster gives the same view.
const restrictGoldenID = "sha256:597079527dab55cb5332831fbddb01f125d13e715245a0260b89aefe8c2a61aa"

func goldenVisible(ns string) bool        { return ns != "shop" && ns != "" }
func goldenWorkloads(ns, _ string) bool   { return ns == "prod" }
func euOnly(cluster, _ string) bool       { return cluster == clusterA }
func everyWorkloadIn(_, _, _ string) bool { return true }
func clusterOrScope(c string) func(string, string) bool {
	return func(cluster, ns string) bool { return cluster == c || (cluster == "" && ns == "") }
}

// twoClusterFleet is eu (payments in prod, shop in shop, a k8s caller in
// batch, an inferred cart in prod; it watches batch, prod and shop and could
// not read audit) and us (orders and payments in prod, an inferred worker in
// prod; it watches prod and could not read vault).
func twoClusterFleet(t *testing.T) *Query {
	t.Helper()
	k := SourceKindKubernetes
	eu, us := clusterOf(clusterA, "eu-prod", ClusterNameFromContext), clusterOf(clusterB, "us-prod", ClusterNameFromContext)
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow},
		NewMemorySource("eu", k, &Collection{
			Cluster:  eu,
			Targets:  []RawTarget{workload("prod", "payments"), workload("shop", "shop")},
			Observed: []ObservedEdge{{From: "k8s:batch/Deployment/legacy", To: "payments", Count: 1}},
			State:    &SourceState{Status: SourcePartial, Namespaces: []string{"batch", "prod", "shop"}, NamespacesTotal: 3},
			Limitations: []Limitation{
				NamespaceScopeLimitation("eu", []string{"batch", "prod", "shop"}, 0),
				NamespaceUnreadableLimitation("eu", "audit", "forbidden"),
			},
		}),
		workloadsSource("eu-workloads", eu, rawInferred("prod", "Deployment", "cart")),
		NewMemorySource("us", k, &Collection{
			Cluster: us,
			Targets: []RawTarget{workload("prod", "orders"), workload("prod", "payments")},
			State:   &SourceState{Status: SourcePartial, Namespaces: []string{"prod"}, NamespacesTotal: 1},
			Limitations: []Limitation{
				NamespaceScopeLimitation("us", []string{"prod"}, 0),
				NamespaceUnreadableLimitation("us", "vault", "forbidden"),
			},
		}),
		workloadsSource("us-workloads", us, rawInferred("prod", "Deployment", "worker")),
	)
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

// hasInferred reports whether s holds an inferred service of the workload name.
func hasInferred(s *FleetSnapshot, name string) bool {
	for _, svc := range s.Services {
		if svc.Inferred() && svc.Workload.Name == name {
			return true
		}
	}
	return false
}

func TestNamespacesArePairs(t *testing.T) {
	q := twoClusterFleet(t)
	want := []ClusterNamespace{{clusterB, "prod"}, {clusterA, "batch"}, {clusterA, "prod"}, {clusterA, "shop"}}
	if got := q.Namespaces(); !slices.Equal(got, want) {
		t.Errorf("Namespaces = %v, want %v", got, want)
	}
	wantScopes := []WorkloadScope{{"prod", "Deployment", clusterB}, {"prod", "Deployment", clusterA}}
	if got := q.InferredWorkloadScopes(); !slices.Equal(got, wantScopes) {
		t.Errorf("InferredWorkloadScopes = %v, want %v", got, wantScopes)
	}
	// A source's watched namespaces belong to its cluster even with no target there.
	scoped := NewQuery(buildClusterSnap(t,
		NewMemorySource("eu", SourceKindKubernetes, &Collection{Cluster: clusterOf(clusterA, "eu-prod", ClusterNameFromContext),
			Targets: []RawTarget{workload("prod", "payments")}, State: &SourceState{Namespaces: []string{"ledger"}}}),
		NewMemorySource("plain", SourceKindKubernetes, &Collection{State: &SourceState{Namespaces: []string{"ledger"}}})))
	if got := scoped.Namespaces(); !slices.Contains(got, ClusterNamespace{clusterA, "ledger"}) || !slices.Contains(got, ClusterNamespace{"", "ledger"}) {
		t.Errorf("scoped namespaces = %v", got)
	}
}

func TestRestrictSameNamespaceTwoClusters(t *testing.T) {
	q := twoClusterFleet(t)
	s := q.Restrict(euOnly, everyWorkloadIn).SnapshotForSerialization()
	if s.Services[NewServiceKey("orders")] != nil || hasInferred(s, "worker") {
		t.Error("a service only in us is hidden from a reader of eu")
	}
	pay := s.Services[NewServiceKey("payments")]
	if pay == nil || len(pay.Targets) != 1 || s.Targets[pay.Targets[0]].Cluster != clusterA {
		t.Fatalf("payments keeps its eu target only: %+v", pay)
	}
	if !s.targetsHidden[pay.Key] {
		t.Error("payments' hidden us target is counted")
	}
	for _, tr := range s.Targets {
		if tr.Cluster != clusterA {
			t.Errorf("a us target reached the view: %+v", tr)
		}
	}
	if len(s.Clusters) != 1 || s.Clusters[0].ID != clusterA {
		t.Errorf("clusters = %+v", s.Clusters)
	}
	if !hasInferred(s, "cart") {
		t.Error("eu's inferred cart stays")
	}
}

func TestRestrictCallerByCluster(t *testing.T) {
	q := NewQuery(buildClusterSnap(t, callerClusterSources("traces")...))
	visible := func(cluster, ns string) bool { return ns != "batch" || cluster == clusterA }
	s := q.Restrict(visible, nil).SnapshotForSerialization()
	if got := callerNames(s); !slices.Equal(got, []string{"k8s:" + clusterA + "/batch/Deployment/legacy"}) {
		t.Errorf("callers = %v, want eu's only: us's and the clusterless one are hidden", got)
	}
	if svc := s.Services[NewServiceKey("payments")]; svc == nil || svc.HiddenDependents != 3 {
		t.Errorf("the hidden callers are counted: %+v", svc)
	}
}

func TestRestrictScopeLimitationPerCluster(t *testing.T) {
	q := twoClusterFleet(t)
	// Every us target is hidden, so the view drops us from its clusters; us's
	// watched namespaces still read as us's, never as clusterless.
	notUS := func(cluster, _ string) bool { return cluster != clusterB }
	s := q.Restrict(notUS, everyWorkloadIn).SnapshotForSerialization()
	if len(s.Clusters) != 1 || s.Clusters[0].ID != clusterA {
		t.Fatalf("clusters = %+v", s.Clusters)
	}
	for _, st := range s.Sources {
		switch st.ID {
		case "eu":
			if !slices.Equal(st.Namespaces, []string{"batch", "prod", "shop"}) {
				t.Errorf("eu namespaces = %v", st.Namespaces)
			}
		case "us":
			if len(st.Namespaces) != 0 || st.NamespacesTotal != 1 {
				t.Errorf("us namespaces = %v of %d, want none named", st.Namespaces, st.NamespacesTotal)
			}
		}
	}
	unreadable := scopeMessages(s.Limitations, LimitationK8sNamespaceUnreadable)
	if len(unreadable) != 1 || unreadable[0] != NamespaceUnreadableLimitation("eu", "audit", "forbidden").Message {
		t.Errorf("unreadable = %q, want eu's audit only", unreadable)
	}
}

func TestRestrictInferredByClusterAndKind(t *testing.T) {
	q := twoClusterFleet(t)
	every := func(string, string) bool { return true }
	for name, tc := range map[string]struct {
		visible   func(string, string) bool
		workloads func(string, string, string) bool
	}{
		"workload read in eu only":  {every, func(c, _, _ string) bool { return c == clusterA }},
		"namespace read in eu only": {euOnly, everyWorkloadIn},
	} {
		s := q.Restrict(tc.visible, tc.workloads).SnapshotForSerialization()
		if !hasInferred(s, "cart") || hasInferred(s, "worker") {
			t.Errorf("%s: cart stays and worker goes", name)
		}
	}
}

func TestRestrictExternalPerClusterConsumer(t *testing.T) {
	k := SourceKindKubernetes
	rev := func(name string) RawRevision {
		return RawRevision{Bundle: &contract.Bundle{Contract: extContract(name, "1.0.0", extDep("stripe")), FS: fstest.MapFS{}}, Digest: "sha256:" + name}
	}
	target := func(name string) RawTarget {
		return RawTarget{Scope: "prod", Kind: k, Name: name, Service: name, Digest: "sha256:" + name, Compliance: StatusCompliant}
	}
	q := NewQuery(buildClusterSnap(t,
		NewMemorySource("oci", "oci", &Collection{Revisions: []RawRevision{rev("web"), rev("pay")}}),
		NewMemorySource("eu", k, &Collection{Cluster: clusterOf(clusterA, "eu-prod", ClusterNameFromContext), Targets: []RawTarget{target("web")}}),
		NewMemorySource("us", k, &Collection{Cluster: clusterOf(clusterB, "us-prod", ClusterNameFromContext), Targets: []RawTarget{target("pay")}}),
	))
	v, err := q.Restrict(clusterOrScope(clusterA), nil).GetExternal("stripe")
	if err != nil {
		t.Fatal(err)
	}
	if e := v.External; len(e.Consumers) != 1 || e.Consumers[0] != NewServiceKey("web") || e.HiddenConsumers != 1 {
		t.Errorf("stripe = %+v, want web shown and us's pay counted", e)
	}
}

func TestRestrictClusterScopeProviderRow(t *testing.T) {
	row := func(cluster string) Provider {
		return Provider{ID: "argo-" + cluster, Source: "detect", Name: "Argo CD", Evidence: "Argo CD found", Status: SourceAvailable,
			Provenance: ProvenanceInferred, Scope: ScopeCluster, Cluster: cluster, ClusterID: cluster, Hint: "pacto ..."}
	}
	clusters := []ClusterRecord{{Cluster: Cluster{ID: clusterA}}, {Cluster: Cluster{ID: clusterB}}}
	got := restrictProviders([]Provider{row(clusterA), row(clusterB)}, nil, nil, euOnly, nil, clusters, nil)
	if got[0].Evidence != "Argo CD found" || got[0].Hint == "" {
		t.Errorf("a cluster-wide row of a readable cluster stays whole: %+v", got[0])
	}
	if got[1].Evidence != "Argo CD detected cluster-wide; you may read only some namespaces" || got[1].Hint != "" {
		t.Errorf("a cluster-wide row of an unreadable cluster is redacted: %+v", got[1])
	}
}

func TestRestrictClusterTaggedExternalTargetHidden(t *testing.T) {
	k := SourceKindKubernetes
	ext := RawTarget{Scope: "ci", Kind: "external", Name: "payments-ci", Service: "payments", Compliance: StatusCompliant, Cluster: clusterOf(clusterB, "us-prod", ClusterNameFromContext)}
	evidence := NewMemorySource("ev", SourceKindEvidenceHTTP, &Collection{Targets: []RawTarget{ext}})
	q := NewQuery(buildClusterSnap(t,
		NewMemorySource("eu", k, &Collection{Cluster: clusterOf(clusterA, "eu-prod", ClusterNameFromContext), Targets: []RawTarget{workload("prod", "payments")}}),
		NewMemorySource("us", k, &Collection{Cluster: clusterOf(clusterB, "us-prod", ClusterNameFromContext), Targets: []RawTarget{workload("prod", "orders")}}),
		evidence))
	hasExt := func(s *FleetSnapshot) bool {
		for _, tr := range s.Targets {
			if tr.Kind == "external" {
				return true
			}
		}
		return false
	}
	if hasExt(q.Restrict(euOnly, nil).SnapshotForSerialization()) {
		t.Error("an external target of a cluster the reader may read nothing in is hidden")
	}
	euAndUSProd := func(c, ns string) bool { return c == clusterA || (c == clusterB && ns == "prod") }
	if !hasExt(q.Restrict(euAndUSProd, nil).SnapshotForSerialization()) {
		t.Error("an external target of a cluster the reader may read a namespace of stays")
	}
	ext.Cluster = clusterOf(clusterA, "eu-prod", ClusterNameFromContext)
	one := NewQuery(buildClusterSnap(t,
		NewMemorySource("eu", k, &Collection{Cluster: clusterOf(clusterA, "eu-prod", ClusterNameFromContext), Targets: []RawTarget{workload("prod", "payments")}}),
		NewMemorySource("ev", SourceKindEvidenceHTTP, &Collection{Targets: []RawTarget{ext}})))
	if !hasExt(one.Restrict(anyCluster(func(ns string) bool { return ns == "prod" }), nil).SnapshotForSerialization()) {
		t.Error("one cluster with a namespace grant keeps its external targets")
	}
}

func TestRestrictIgnoringClusterIsUnchanged(t *testing.T) {
	r := twoClusterFleet(t).Restrict(anyCluster(goldenVisible), anyClusterKind(goldenWorkloads))
	if got := r.SnapshotID(); got != restrictGoldenID {
		t.Errorf("snapshot id = %s, want the golden %s recorded before Restrict read clusters", got, restrictGoldenID)
	}
}

// declTwoClusterFleet is declFleet's signed newsletter, its declaration read
// from news in cluster, beside eu and us sources.
func declTwoClusterFleet(t *testing.T, cluster string) *Query {
	t.Helper()
	o := signedOpts(t)
	rec, err := contract.ParseInferredRecord(o.record)
	if err != nil {
		t.Fatal(err)
	}
	rec.Declaration.Cluster = cluster
	news := &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: "newsletter", Version: "1.0.0", Owner: contract.Owner{Team: "t"}}}
	fsys := fstest.MapFS{"deploy/app.yaml": {Data: []byte(recordManifest)}, contract.InferredRecordFile: {Data: rec.Marshal()}}
	k := SourceKindKubernetes
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, DeclarationSigners: o.policy},
		NewMemorySource("oci", "oci", &Collection{Revisions: []RawRevision{{Bundle: &contract.Bundle{Contract: news, FS: fsys}, Digest: "sha256:news", Provenance: o.prov}}}),
		NewMemorySource("eu", k, &Collection{Cluster: clusterOf(clusterA, "eu-prod", ClusterNameFromContext), Targets: []RawTarget{workload("news", "eu-app")}}),
		NewMemorySource("us", k, &Collection{Cluster: clusterOf(clusterB, "us-prod", ClusterNameFromContext), Targets: []RawTarget{workload("news", "us-app")}}))
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

func TestRestrictDeclarationInferredFromPerCluster(t *testing.T) {
	q := declTwoClusterFleet(t, clusterB)
	if !slices.Contains(q.Namespaces(), ClusterNamespace{clusterB, "news"}) {
		t.Fatalf("the declaration's namespace is us's: %v", q.Namespaces())
	}
	wantInferredFromHidden(t, q.Restrict(clusterOrScope(clusterA), everyWorkloadIn))
	if d := newsRevision(t, q.Restrict(clusterOrScope(clusterB), everyWorkloadIn)); d.Declaration.InferredFromHidden || d.Declaration.InferredFrom == "" {
		t.Errorf("a reader of news in us keeps the declaration's source: %+v", d.Declaration)
	}
	// A cluster id no source reported picks no reviewer: the declaration reads clusterless.
	if got := declTwoClusterFleet(t, "forged").Namespaces(); !slices.Contains(got, ClusterNamespace{"", "news"}) {
		t.Errorf("an unknown declaration cluster reads as \"\": %v", got)
	}
}
