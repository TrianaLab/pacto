package fleet

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// contractFleet is orders 1.3.0, consuming postgresql's db (postgresql
// provides the offering named offering, none when empty), running in
// environment production on targetCluster, the managed resources res and
// the provisioning records recs.
type contractFleet struct {
	offering      string
	targetCluster *Cluster
	res           []RawManagedResource
	recs          []RawProvisioning
	rows          []Provider
}

func (f contractFleet) snap(t *testing.T) *FleetSnapshot {
	t.Helper()
	pg := svc24("postgresql", "16.1.0", f.offering)
	pg.Configurations = []contract.Configuration{{Name: "db", Schema: "s.json"}}
	orders := ordersIn("shop")
	orders.Digest = testDigest(acme, "orders", "1.3.0")
	sources := []Source{
		NewMemorySource("oci", "oci", &Collection{Revisions: []RawRevision{ociRev(acme, pg, nil), consumer("orders", "1.2.0", "postgresql"), consumer("orders", "1.3.0", "postgresql")}}),
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Cluster: f.targetCluster, Targets: []RawTarget{orders}}),
		managedSource("managed", f.res, nil),
		NewMemorySource("evidence-0", "evidence-http", &Collection{Provisioning: f.recs}),
	}
	for _, row := range f.rows {
		sources = append(sources, NewMemorySource("detection-"+row.Cluster, SourceKindDetection,
			&Collection{Advisory: true, Providers: []Provider{row}, State: &SourceState{Status: SourceAvailable}}))
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, sources...)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// throughOfferingFleet is the fleet with postgresql providing postgresql, orders
// running on prod-a and orders-db on prod-a, with recs.
func throughOfferingFleet(recs ...RawProvisioning) contractFleet {
	return contractFleet{offering: "postgresql", targetCluster: clusterOf("uid-a", "prod-a", ClusterNameFromContext), res: []RawManagedResource{ordersDB("uid-a")}, recs: recs}
}

// provisioned is a fresh, signed Ready report from postgresql naming ref.
func provisioned(ref string, mods ...func(*RawProvisioning)) RawProvisioning {
	return report(append([]func(*RawProvisioning){func(r *RawProvisioning) { r.Provider, r.ExternalRef = "postgresql", ref }}, mods...)...)
}

// contractOf is the served contract state of each resource of id.
func contractOf(t *testing.T, q *Query, id string) []ManagedResource {
	t.Helper()
	v, err := q.GetExternal(id)
	if err != nil {
		t.Fatal(err)
	}
	return v.External.Resources
}

func wantContracts(t *testing.T, rs []ManagedResource, want ...string) {
	t.Helper()
	if len(rs) != len(want) {
		t.Fatalf("resources = %+v, want %v", rs, want)
	}
	for i, r := range rs {
		if r.Contract != want[i] {
			t.Errorf("resource %d (%s) contract = %q, want %q", i, r.Name, r.Contract, want[i])
		}
		if (r.Contract == ContractOffering) != (r.Offering != nil && r.Evidence != "") {
			t.Errorf("resource %d offering %+v evidence %q", i, r.Offering, r.Evidence)
		}
	}
}

func TestManagedOfferingWithVerifiedRecord(t *testing.T) {
	q := NewQuery(throughOfferingFleet(provisioned("Instance orders-db")).snap(t))
	rs := contractOf(t, q, ordersDBID)
	wantContracts(t, rs, ContractOffering)
	if rs[0].Offering.Kind != KindOffering || rs[0].Offering.Key != "postgresql" || rs[0].Evidence != "reported by adapter at 2026-07-29T11:00:00Z" {
		t.Errorf("resource = %+v", rs[0])
	}
}

func TestManagedOutsideWithoutEvidence(t *testing.T) {
	wantContracts(t, contractOf(t, NewQuery(throughOfferingFleet().snap(t)), ordersDBID), ContractOutside)
}

func TestManagedOutsideWhenRecordUnverified(t *testing.T) {
	f := throughOfferingFleet(provisioned("Instance orders-db", func(r *RawProvisioning) { r.Verified = false }))
	wantContracts(t, contractOf(t, NewQuery(f.snap(t)), ordersDBID), ContractOutside)
}

func TestManagedOutsideWhenRecordStale(t *testing.T) {
	old := fixedNow().Add(-48 * time.Hour)
	f := throughOfferingFleet(provisioned("Instance orders-db", func(r *RawProvisioning) { r.ObservedAt, r.AcceptedAt = old, &old }))
	wantContracts(t, contractOf(t, NewQuery(f.snap(t)), ordersDBID), ContractOutside)
}

func TestManagedOutsideWhenRecordNotObserved(t *testing.T) {
	f := throughOfferingFleet(provisioned("Instance orders-db", func(r *RawProvisioning) {
		r.Outcome, r.Phase, r.Provider, r.ExternalRef, r.Environment = "Insufficient", "", "", "", ""
	}))
	wantContracts(t, contractOf(t, NewQuery(f.snap(t)), ordersDBID), ContractOutside)
}

func TestManagedOutsideWhenRecordAboutOlderRevision(t *testing.T) {
	f := throughOfferingFleet(provisioned("Instance orders-db", func(r *RawProvisioning) { r.Digest = testDigest(acme, "orders", "1.2.0") }))
	snap := f.snap(t)
	// orders 1.2.0 is known, so the record stays, but the target runs 1.3.0.
	q := NewQuery(snap)
	wantContracts(t, contractOf(t, q, ordersDBID), ContractOutside)
}

func TestManagedOfferingNeedsOfferingProvider(t *testing.T) {
	f := throughOfferingFleet(provisioned("Instance orders-db"))
	f.offering = ""
	wantContracts(t, contractOf(t, NewQuery(f.snap(t)), ordersDBID), ContractOutside)
}

func TestManagedOutsideWhenRecordProviderNotOffering(t *testing.T) {
	f := throughOfferingFleet(provisioned("Instance orders-db", func(r *RawProvisioning) { r.Provider = "aws" }))
	wantContracts(t, contractOf(t, NewQuery(f.snap(t)), ordersDBID), ContractOutside)
}

func TestManagedOfferingMatchesIdentityRef(t *testing.T) {
	// An identity names the resource wherever it was read: no cluster gate.
	f := throughOfferingFleet(provisioned(ordersDBID, func(r *RawProvisioning) { r.Environment = "staging" }))
	wantContracts(t, contractOf(t, NewQuery(f.snap(t)), ordersDBID), ContractOffering)
}

func TestManagedPartialIdentityNeverMatchesRecord(t *testing.T) {
	a, b := ordersDB("uid-a"), ordersDB("uid-a")
	a.Identity, a.Missing, a.Name = "aws/rds", []string{"account", "region", "id"}, "a"
	b.Identity, b.Missing, b.Name = "aws/rds", []string{"account"}, "b"
	f := throughOfferingFleet(provisioned("aws/rds"))
	f.res = []RawManagedResource{a, b}
	wantContracts(t, contractOf(t, NewQuery(f.snap(t)), "aws/rds"), ContractOutside, ContractOutside)
	// A complete identity with a segment reported missing never matches either.
	c := ordersDB("uid-a")
	c.Missing = []string{"region"}
	f = throughOfferingFleet(provisioned(ordersDBID))
	f.res = []RawManagedResource{c}
	wantContracts(t, contractOf(t, NewQuery(f.snap(t)), ordersDBID), ContractOutside)
}

func TestManagedOfferingMatchesClaimRef(t *testing.T) {
	r := ordersDB("uid-a")
	r.Claim = "PostgreSQLInstance shop/orders-db"
	f := throughOfferingFleet(provisioned(r.Claim))
	f.res = []RawManagedResource{r}
	wantContracts(t, contractOf(t, NewQuery(f.snap(t)), ordersDBID), ContractOffering)
}

func TestManagedObjectRefNeedsClusterObject(t *testing.T) {
	// A Terraform address is no cluster object, even on a cluster.
	r := ordersDB("uid-a")
	r.Source, r.Type = "terraform", "rds.aws.upbound.io/Instance"
	f := throughOfferingFleet(provisioned("Instance orders-db"))
	f.res = []RawManagedResource{r}
	wantContracts(t, contractOf(t, NewQuery(f.snap(t)), ordersDBID), ContractOutside)
	// A resource with no cluster has no target to resolve the environment to.
	f.res = []RawManagedResource{ordersDB("")}
	f.res[0].Claim = "PostgreSQLInstance shop/orders-db"
	f.recs = []RawProvisioning{provisioned(f.res[0].Claim)}
	wantContracts(t, contractOf(t, NewQuery(f.snap(t)), ordersDBID), ContractOutside)
}

func TestManagedRecordOtherClusterStaysOutside(t *testing.T) {
	f := throughOfferingFleet(provisioned("Instance orders-db"))
	f.targetCluster = &Cluster{ID: "uid-s", Name: "staging"}
	wantContracts(t, contractOf(t, NewQuery(f.snap(t)), ordersDBID), ContractOutside)
	// Same cluster, another environment.
	f = throughOfferingFleet(provisioned("Instance orders-db", func(r *RawProvisioning) { r.Environment = "staging" }))
	wantContracts(t, contractOf(t, NewQuery(f.snap(t)), ordersDBID), ContractOutside)
}

func TestManagedClaimKeepsOutside(t *testing.T) {
	snap := throughOfferingFleet().snap(t)
	snap.Externals[ordersDBID].Claim = &ExternalClaim{Service: "team-x/orders-db", By: "alice"}
	wantContracts(t, contractOf(t, NewQuery(snap), ordersDBID), ContractOutside)
}

func TestManagedObjectRefShapes(t *testing.T) {
	if got := ManagedObjectRef("Instance", "", "orders-db"); got != "Instance orders-db" {
		t.Errorf("cluster-scoped = %q", got)
	}
	if got := ManagedObjectRef("DBInstance", "shop", "orders-db"); got != "DBInstance shop/orders-db" {
		t.Errorf("namespaced = %q", got)
	}
}

// twoClusterResources is orders-db on prod-a, through postgresql, and
// invoices-db on prod-b, outside contract.
func twoClusterResources(t *testing.T) *FleetSnapshot {
	t.Helper()
	b := ordersDB("uid-b")
	b.Identity, b.Name = "aws/rds/123456789012/eu-west-1/invoices-db", "invoices-db"
	f := throughOfferingFleet(provisioned("Instance orders-db"))
	f.res = append(f.res, b)
	return f.snap(t)
}

func TestResourceCoverageAbsentWithoutResources(t *testing.T) {
	f := throughOfferingFleet()
	f.res = nil
	m, err := NewQuery(f.snap(t)).Map(MapQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if m.Match.Resources != nil {
		t.Errorf("resources = %+v", m.Match.Resources)
	}
}

func TestResourceCoverageCountsViewerRecords(t *testing.T) {
	snap := twoClusterResources(t)
	m, err := NewQuery(snap).Map(MapQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if c := m.Match.Resources; c == nil || c.ThroughOfferings != 1 || c.Discovered != 2 || c.Percent == nil || *c.Percent != 50 {
		t.Errorf("fleet = %+v", c)
	}
	// A viewer of prod-a reads orders-db only.
	rm, err := NewQuery(snap).Restrict(func(c, _ string) bool { return c == "uid-a" }, nil).Map(MapQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if c := rm.Match.Resources; c == nil || c.ThroughOfferings != 1 || c.Discovered != 1 || *c.Percent != 100 {
		t.Errorf("restricted = %+v", c)
	}
}

func TestMapMarksOutsideContract(t *testing.T) {
	q := NewQuery(twoClusterResources(t))
	m, err := q.Map(MapQuery{})
	if err != nil {
		t.Fatal(err)
	}
	var ext *MapGroup
	for i := range m.Groups {
		if m.Groups[i].Kind == MapKindExternal {
			ext = &m.Groups[i]
		}
	}
	if ext == nil || ext.OutsideContract != 1 {
		t.Fatalf("external group = %+v", ext)
	}
	nodes, err := q.MapNodes(MapNodesQuery{Group: ext.Key})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, n := range nodes.Nodes {
		got[n.Key] = n.OutsideContract
	}
	if got[ordersDBID] != 0 || got["aws/rds/123456789012/eu-west-1/invoices-db"] != 1 {
		t.Errorf("nodes = %v", got)
	}
}

func TestEntitiesOutsideContractFilter(t *testing.T) {
	q := NewQuery(twoClusterResources(t))
	res, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindExternal}, OutsideContract: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entities) != 1 || res.Entities[0].Key != "aws/rds/123456789012/eu-west-1/invoices-db" {
		t.Errorf("entities = %+v", res.Entities)
	}
	all, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindExternal}})
	if err != nil || len(all.Entities) != 2 {
		t.Fatalf("all = %+v, %v", all, err)
	}
	if _, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindService}, OutsideContract: true}); err == nil {
		t.Error("outsideContract on services accepted")
	}
	mixed, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindService, KindExternal}, OutsideContract: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range mixed.Entities {
		if e.Kind != KindExternal {
			t.Errorf("non-external %+v", e)
		}
	}
}

// managedRow is a detected managed-resources row of cluster name.
func managedRow(id, name string, status SourceStatus, hint string, details ...string) Provider {
	at := fixedNow()
	return Provider{ID: ProviderManagedResources, Name: "Managed resources", Evidence: "managed resources (from detection, cluster " + name + ")",
		Status: status, Provenance: ProvenanceInferred, AsOf: &at, Cluster: name, ClusterID: id, Scope: ScopeCluster,
		UsedFor: []string{"managed-resources"}, Hint: hint, Details: details}
}

const helmHint = "helm upgrade pacto oci://ghcr.io/trianalab/charts/pacto-operator --reuse-values"

func TestManagedGroupUnreadAttention(t *testing.T) {
	f := throughOfferingFleet()
	f.rows = []Provider{
		managedRow("uid-a", "prod-a", SourcePartial, helmHint,
			"unread rds.services.k8s.aws/dbinstances", "unread rds.services.k8s.aws/dbclusters", "unread sql.cnrm.cloud.google.com/sqlinstances", "listed 3 kinds"),
		// An available row names nothing unread.
		managedRow("uid-b", "prod-b", SourceAvailable, "", "unread x.example.com/things"),
	}
	q := NewQuery(f.snap(t))
	var items []AttentionItem
	for _, it := range q.attention() {
		if it.Code == "MANAGED_GROUP_UNREAD" {
			items = append(items, it)
		}
	}
	if len(items) != 2 {
		t.Fatalf("items = %+v", items)
	}
	it := items[0]
	if it.Label != "CRD rds.services.k8s.aws detected after install: run helm upgrade to observe it" || it.Category != categoryKnowledge ||
		it.Severity != severityInfo || it.NextStep != helmHint || it.Entity.Kind != KindSource || !strings.Contains(it.Reason, "in cluster prod-a") {
		t.Errorf("item = %+v", it)
	}
	if items[1].Label != "CRD sql.cnrm.cloud.google.com detected after install: run helm upgrade to observe it" {
		t.Errorf("second = %+v", items[1])
	}
}

func TestManagedGroupUnreadNoCluster(t *testing.T) {
	it := managedGroupItem(Provider{Source: "detection", Hint: helmHint}, "rds.services.k8s.aws")
	if strings.Contains(it.Reason, "in cluster") || it.Entity.Key != "detection" {
		t.Errorf("item = %+v", it)
	}
}

func TestManagedGroupUnreadCountsInKnowledge(t *testing.T) {
	f := throughOfferingFleet()
	before := NewQuery(f.snap(t)).Overview().Summary.KnowledgeItems
	f.rows = []Provider{managedRow("uid-a", "prod-a", SourcePartial, helmHint, "unread rds.services.k8s.aws/dbinstances")}
	if after := NewQuery(f.snap(t)).Overview().Summary.KnowledgeItems; after != before+1 {
		t.Errorf("knowledge items %d -> %d", before, after)
	}
}
