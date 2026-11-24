package fleet

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
)

const ordersDBID = "aws/rds/123456789012/eu-west-1/orders-db"

// ordersDB is the RDS instance orders-db read as a Crossplane object on
// cluster, writing its connection to shop/orders-db-conn.
func ordersDB(cluster string) RawManagedResource {
	at := fixedNow().Add(-time.Minute)
	return RawManagedResource{Source: "crossplane", Identity: ordersDBID, Entry: "aws/rds", Catalog: externalcatalog.DefaultDigest(),
		Type: "rds.aws.upbound.io/Instance", Cluster: cluster, ClusterName: "name-" + cluster, Name: "orders-db",
		ConnectionSecrets: []string{"shop/orders-db-conn"}, Engine: "postgres 16", ObservedAt: &at}
}

// managedK8s is a Kubernetes source of id on cluster (nil: none) running
// each service in ns as a Deployment of the same name.
func managedK8s(id string, cluster *Cluster, ns string, svcs ...string) Source {
	col := &Collection{Cluster: cluster}
	for _, s := range svcs {
		digest := "sha256:" + s
		col.Revisions = append(col.Revisions, RawRevision{Bundle: bundleOf(extContract(s, "1.0.0")), Digest: digest})
		col.Targets = append(col.Targets, RawTarget{Scope: ns, Kind: SourceKindKubernetes, Name: s, Service: s, Digest: digest,
			Compliance: StatusCompliant, Workload: &WorkloadRef{Kind: "Deployment", Name: s}})
	}
	return NewMemorySource(id, SourceKindKubernetes, col)
}

// managedSource is a detection source of id reporting res and refs.
func managedSource(id string, res []RawManagedResource, refs []RawSecretRef) Source {
	return NewMemorySource(id, SourceKindDetection, &Collection{ManagedResources: res, SecretRefs: refs})
}

// ordersRef is a Deployment's reference to shop/orders-db-conn on cluster.
func ordersRef(cluster, workload string) RawSecretRef {
	return RawSecretRef{Cluster: cluster, Namespace: "shop", Kind: "Deployment", Name: workload, Secret: "orders-db-conn"}
}

func buildManaged(t *testing.T, sources ...Source) *FleetSnapshot {
	t.Helper()
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, sources...)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func mClusterA() *Cluster { return &Cluster{ID: "uid-a", Name: "prod-a"} }
func mClusterB() *Cluster { return &Cluster{ID: "uid-b", Name: "prod-b"} }

func TestManagedResourceCreatesExternal(t *testing.T) {
	snap := buildManaged(t, managedSource("managed", []RawManagedResource{ordersDB("")}, nil))
	rec := snap.Externals[ordersDBID]
	if rec == nil || rec.Class != externalcatalog.ClassManagedCloud || rec.ConsumersTotal != 0 || rec.Label != "orders-db" || len(rec.Resources) != 1 || rec.ResourcesTotal != 1 {
		t.Fatalf("record = %+v", rec)
	}
	r := rec.Resources[0]
	if r.Provenance != ProvenanceInferred || r.Type != "rds.aws.upbound.io/Instance" || r.Name != "orders-db" || r.Engine != "postgres 16" || r.ObservedAt == nil {
		t.Fatalf("resource = %+v", r)
	}
	v, err := NewQuery(snap).GetExternal("external:" + ordersDBID)
	if err != nil || len(v.External.Resources) != 1 {
		t.Fatalf("get = %+v, %v", v, err)
	}
}

func TestConnectionSecretJoinsConsumer(t *testing.T) {
	snap := buildManaged(t, managedK8s("k8s", nil, "shop", "orders"),
		managedSource("managed", []RawManagedResource{ordersDB("")}, nil),
		managedSource("workloads", nil, []RawSecretRef{ordersRef("", "orders")}))
	rels := relsTo(snap, ordersDBID)
	if len(rels) != 1 || rels[0].FromService != "orders" || rels[0].Provenance != ProvenanceInferred || rels[0].Confidence != externalcatalog.ConfidenceLikely {
		t.Fatalf("edges = %+v", rels)
	}
	tk := snap.Services["orders"].Targets[0]
	src := rels[0].InferredSources
	if len(src) != 1 || src[0].Kind != "connectionSecret" || src[0].Source != "workloads" || src[0].Target != tk ||
		src[0].Where != "deployment/orders secret/orders-db-conn" || src[0].Entry != "aws/rds" {
		t.Fatalf("sources = %+v", src)
	}
	rec := snap.Externals[ordersDBID]
	want := ExternalSource{Kind: ExternalSourcePodSpec, Service: "orders", Target: tk, Provenance: ProvenanceInferred, Match: externalcatalog.KindResource}
	if rec.ConsumersTotal != 1 || len(rec.Sources) != 1 || rec.Sources[0] != want {
		t.Fatalf("record = %+v", rec)
	}
}

func TestConnectionSecretJoinsInferredService(t *testing.T) {
	work := NewMemorySource("workloads", SourceKindKubernetesWorkloads, &Collection{
		InferredServices: []RawInferredService{{Namespace: "shop", Kind: "Deployment", Name: "legacy"}},
		SecretRefs:       []RawSecretRef{ordersRef("", "legacy")},
	})
	snap := buildManaged(t, managedSource("managed", []RawManagedResource{ordersDB("")}, nil), work)
	rels := relsTo(snap, ordersDBID)
	if len(rels) != 1 || snap.Services[rels[0].FromService] == nil || !snap.Services[rels[0].FromService].Inferred() {
		t.Fatalf("edges = %+v", rels)
	}
}

func TestConnectionSecretJoinsTargetOnRefCluster(t *testing.T) {
	b := ordersDB("uid-b")
	b.Identity = "aws/rds/123456789012/eu-west-1/invoices-db"
	snap := buildManaged(t, managedK8s("k8s-a", mClusterA(), "shop", "orders"), managedK8s("k8s-b", mClusterB(), "shop", "invoices"),
		managedSource("managed", []RawManagedResource{ordersDB("uid-a"), b}, nil),
		managedSource("refs", nil, []RawSecretRef{ordersRef("uid-a", "orders"), ordersRef("uid-b", "invoices")}))
	a := relsTo(snap, ordersDBID)
	inv := relsTo(snap, ExternalKey(b.Identity))
	if len(a) != 1 || a[0].FromService != "orders" || len(inv) != 1 || inv[0].FromService != "invoices" {
		t.Fatalf("orders-db %+v, invoices-db %+v", a, inv)
	}
	if tk := a[0].InferredSources[0].Target; snap.Targets[tk].Cluster != "uid-a" {
		t.Errorf("orders target %s on %s", tk, snap.Targets[tk].Cluster)
	}
}

func TestConnectionSecretOtherNamespaceNoJoin(t *testing.T) {
	ref := ordersRef("", "orders")
	ref.Namespace = "billing"
	snap := buildManaged(t, managedK8s("k8s", nil, "billing", "orders"),
		managedSource("managed", []RawManagedResource{ordersDB("")}, []RawSecretRef{ref}))
	if rels := relsTo(snap, ordersDBID); len(rels) != 0 {
		t.Fatalf("edges = %+v", rels)
	}
}

func TestConnectionSecretRefsDeduplicated(t *testing.T) {
	ref := ordersRef("", "orders")
	snap := buildManaged(t, managedK8s("k8s", nil, "shop", "orders"),
		managedSource("managed", []RawManagedResource{ordersDB("")}, []RawSecretRef{ref, ref}),
		managedSource("workloads", nil, []RawSecretRef{ref}))
	rels := relsTo(snap, ordersDBID)
	if len(rels) != 1 || len(rels[0].InferredSources) != 1 || rels[0].InferredSources[0].Source != "managed" {
		t.Fatalf("edges = %+v", rels)
	}
}

func TestConnectionSecretMatchIsResource(t *testing.T) {
	if m := inferredMatch(RawExternalUse{Kind: kindConnectionSecret, Host: "orders-db.example.com"}); m != externalcatalog.KindResource {
		t.Fatalf("match = %s", m)
	}
	snap := buildManaged(t, managedK8s("k8s", nil, "shop", "orders"),
		managedSource("managed", []RawManagedResource{ordersDB("")}, []RawSecretRef{ordersRef("", "orders")}))
	for _, s := range snap.Externals[ordersDBID].Sources {
		if s.Match != externalcatalog.KindResource {
			t.Errorf("source = %+v", s)
		}
	}
}

func TestConnectionSecretUseIsInferredOnly(t *testing.T) {
	snap := buildManaged(t, managedK8s("k8s", nil, "shop", "orders"),
		managedSource("managed", []RawManagedResource{ordersDB("")}, []RawSecretRef{ordersRef("", "orders")}))
	checkIndexesEmpty(t, snap)
	for _, r := range relsTo(snap, ordersDBID) {
		if ConsumesEdge(r) || r.RangeStatus != "" || r.Resolved || r.ToService != "" {
			t.Errorf("edge = %+v", r)
		}
	}
	if len(snap.inferredForward["orders"]) != 0 || len(snap.inferredReverse["orders"]) != 0 {
		t.Errorf("inferred indexes hold %v %v", snap.inferredForward, snap.inferredReverse)
	}
}

func TestClaimSecretJoinsClaimNamespace(t *testing.T) {
	r := ordersDB("")
	r.ConnectionSecrets = []string{"crossplane-system/orders-db-xr", "shop/orders-db-conn", "malformed"}
	r.Claim = "PostgreSQLInstance shop/orders-db"
	snap := buildManaged(t, managedK8s("k8s", nil, "shop", "orders"),
		managedSource("managed", []RawManagedResource{r}, []RawSecretRef{ordersRef("", "orders")}))
	rels := relsTo(snap, ordersDBID)
	if len(rels) != 1 || rels[0].FromService != "orders" {
		t.Fatalf("edges = %+v", rels)
	}
	if got := snap.Externals[ordersDBID].Resources[0].Claim; got != r.Claim {
		t.Errorf("claim = %q", got)
	}
}

func TestManagedResourceInvalidIdentityCounted(t *testing.T) {
	bad, reserved := ordersDB(""), ordersDB("")
	bad.Identity, reserved.Identity = "Not A Secret Identity", string(OtherEgress)
	snap := buildManaged(t, managedSource("managed", []RawManagedResource{bad, reserved}, nil))
	if len(snap.Externals) != 0 {
		t.Fatalf("externals = %+v", snap.Externals)
	}
	i := slices.IndexFunc(snap.Limitations, func(l Limitation) bool { return l.Code == LimitationManagedResourceInvalid })
	if i < 0 || snap.Limitations[i].Source != "managed" || !strings.HasPrefix(snap.Limitations[i].Message, "2 managed resources") ||
		strings.Contains(snap.Limitations[i].Message, "Secret") {
		t.Fatalf("limitations = %+v", snap.Limitations)
	}
}

func TestManagedResourceUnmatchedWorkloadCounted(t *testing.T) {
	snap := buildManaged(t, managedK8s("k8s", nil, "shop", "orders"),
		managedSource("managed", []RawManagedResource{ordersDB("")}, nil),
		managedSource("workloads", nil, []RawSecretRef{ordersRef("", "ghost")}))
	if rels := relsTo(snap, ordersDBID); len(rels) != 0 {
		t.Fatalf("edges = %+v", rels)
	}
	if rec := snap.Externals[ordersDBID]; rec == nil || len(rec.Resources) != 1 {
		t.Fatalf("record = %+v", rec)
	}
	i := slices.IndexFunc(snap.Limitations, func(l Limitation) bool { return l.Code == LimitationExternalUseUnattributed })
	if i < 0 || snap.Limitations[i].Source != "workloads" || strings.Contains(snap.Limitations[i].Message, "ghost") {
		t.Fatalf("limitations = %+v", snap.Limitations)
	}
}

// onlyCluster is what a caller who may read only cluster sees.
func onlyCluster(cluster string) func(string, string) bool {
	return func(c, _ string) bool { return c == cluster }
}

func TestRestrictHidesClusterScopedResource(t *testing.T) {
	b := ordersDB("uid-b")
	b.Identity = "aws/rds/123456789012/eu-west-1/invoices-db"
	snap := buildManaged(t, managedK8s("k8s-a", mClusterA(), "shop", "orders"),
		managedSource("managed", []RawManagedResource{ordersDB("uid-a"), b}, nil))
	q := NewQuery(snap).Restrict(onlyCluster("uid-a"), nil)
	if q.snap.Externals[ordersDBID] == nil || q.snap.Externals[ExternalKey(b.Identity)] != nil {
		t.Fatalf("externals = %+v", q.snap.Externals)
	}
	if _, err := q.GetExternal(b.Identity); err == nil {
		t.Errorf("cluster B's resource reachable")
	}
}

func TestRestrictHidesStateFromOtherClusterViewer(t *testing.T) {
	state := RawManagedResource{Source: "terraform", Identity: ordersDBID, Type: "aws_db_instance", Name: "aws_db_instance.orders",
		Cluster: "uid-b", Location: "s3://acme-tf/prod.tfstate"}
	local := state
	local.Identity, local.Cluster, local.Location = "aws/rds/123456789012/eu-west-1/local-db", "", "prod.tfstate"
	snap := buildManaged(t, managedK8s("k8s-a", mClusterA(), "shop", "orders"), managedSource("managed", []RawManagedResource{state, local}, nil))
	q := NewQuery(snap).Restrict(onlyCluster("uid-a"), nil)
	if len(q.snap.Externals) != 0 {
		t.Fatalf("externals = %+v", q.snap.Externals)
	}
	all := NewQuery(snap).Restrict(func(c, ns string) bool { return c == "" || c == "uid-a" }, nil)
	if all.snap.Externals[ExternalKey(local.Identity)] == nil || all.snap.Externals[ordersDBID] != nil {
		t.Fatalf("cluster-scope externals = %+v", all.snap.Externals)
	}
}

func TestRestrictCountsHiddenResources(t *testing.T) {
	snap := buildManaged(t, managedK8s("k8s-a", mClusterA(), "shop", "orders"),
		managedSource("managed", []RawManagedResource{ordersDB("uid-a"), ordersDB("uid-b")}, nil))
	if rec := snap.Externals[ordersDBID]; rec.ResourcesTotal != 2 || rec.Resources[0].Cluster != "uid-a" {
		t.Fatalf("record = %+v", rec)
	}
	v, err := NewQuery(snap).Restrict(onlyCluster("uid-a"), nil).GetExternal(ordersDBID)
	if err != nil || len(v.External.Resources) != 1 || v.External.ResourcesTotal != 1 || v.External.HiddenResources != 1 ||
		v.External.Resources[0].Cluster != "uid-a" {
		t.Fatalf("view = %+v, %v", v, err)
	}
}

func TestRestrictDropsResourceOnlyExternal(t *testing.T) {
	snap := buildManaged(t, managedK8s("k8s-a", mClusterA(), "shop", "orders"), managedK8s("k8s-b", mClusterB(), "shop", "invoices"),
		managedSource("managed", []RawManagedResource{ordersDB("uid-b")}, []RawSecretRef{ordersRef("uid-b", "invoices")}))
	q := NewQuery(snap).Restrict(onlyCluster("uid-a"), nil)
	if len(q.snap.Externals) != 0 || q.snap.hiddenExternals != 1 {
		t.Fatalf("externals = %+v, hidden %d", q.snap.Externals, q.snap.hiddenExternals)
	}
	// A visible consumer keeps the record and only its resource is hidden.
	q = NewQuery(snap).Restrict(func(c, ns string) bool { return ns == "shop" }, nil)
	if rec := q.snap.Externals[ordersDBID]; rec == nil || len(rec.Resources) != 0 || rec.HiddenResources != 1 {
		t.Fatalf("record = %+v", rec)
	}
}

func TestBoundExternalResources(t *testing.T) {
	var res []RawManagedResource
	for i := range MaxExternalResources + 5 {
		r := ordersDB("")
		r.Name = fmt.Sprintf("orders-db-%02d", i)
		res = append(res, r)
	}
	v, err := NewQuery(buildManaged(t, managedSource("managed", res, nil))).GetExternal(ordersDBID)
	if err != nil || len(v.External.Resources) != MaxExternalResources || v.External.ResourcesTotal != MaxExternalResources+5 ||
		v.External.Resources[0].Name != "orders-db-00" {
		t.Fatalf("view = %d of %d, %v", len(v.External.Resources), v.External.ResourcesTotal, err)
	}
}

func TestFoldCountsResources(t *testing.T) {
	var res []RawManagedResource
	for i := range MaxExternalNodes + 5 {
		r := ordersDB("")
		r.Identity = fmt.Sprintf("aws/rds/123456789012/eu-west-1/db-%03d", i)
		res = append(res, r)
	}
	q := NewQuery(buildManaged(t, managedSource("managed", res, nil)))
	other, err := q.GetExternal(string(OtherEgress))
	if err != nil || other.External.Folded != 6 || other.External.ResourcesTotal != 6 {
		t.Fatalf("other egress = %+v, %v", other, err)
	}
}
