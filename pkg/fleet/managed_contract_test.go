package fleet

import (
	"context"
	"slices"
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
	// The Overview's match tile carries the same figure.
	if c := NewQuery(snap).Overview().Summary.Match.Resources; c == nil || c.ThroughOfferings != 1 || c.Discovered != 2 {
		t.Errorf("overview = %+v", c)
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
	if it.Label != "rds.services.k8s.aws: Pacto may not list it" || it.Category != categoryKnowledge ||
		it.Severity != severityInfo || it.NextStep != helmHint || it.Entity.Kind != KindSource || !strings.Contains(it.Reason, "in cluster prod-a") {
		t.Errorf("item = %+v", it)
	}
	if items[1].Label != "sql.cnrm.cloud.google.com: Pacto may not list it" || !strings.Contains(items[1].Reason, "helm template") {
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

// Two cluster-scoped objects of different API groups share the reference
// "Cluster orders" on one cluster: the reference is ambiguous, so the record
// naming it puts neither through the offering.
func TestManagedAmbiguousObjectRefStaysOutside(t *testing.T) {
	rds, cache := ordersDB("uid-a"), ordersDB("uid-a")
	rds.Type, rds.Name = "rds.aws.upbound.io/Cluster", "orders"
	cache.Type, cache.Name, cache.Identity, cache.Entry = "elasticache.aws.upbound.io/Cluster", "orders", "aws/elasticache/123456789012/eu-west-1/orders", "aws/elasticache"
	f := throughOfferingFleet(provisioned("Cluster orders"))
	f.res = []RawManagedResource{rds, cache}
	q := NewQuery(f.snap(t))
	wantContracts(t, contractOf(t, q, ordersDBID), ContractOutside)
	wantContracts(t, contractOf(t, q, "aws/elasticache/123456789012/eu-west-1/orders"), ContractOutside)
	// One object alone is unambiguous.
	f.res = []RawManagedResource{rds}
	wantContracts(t, contractOf(t, NewQuery(f.snap(t)), ordersDBID), ContractOffering)
}

// A cluster object and an inventory item of one complete identity are one
// database: the object's proof puts the inventory read through it too.
func TestManagedSameIdentityReadsShareContract(t *testing.T) {
	inv := ordersDB("")
	inv.Source, inv.Type, inv.ConnectionSecrets = "aws-resource-explorer", "rds:db", nil
	partial := ordersDB("")
	partial.Source, partial.Type, partial.Name, partial.Missing, partial.ConnectionSecrets = "terraform", "aws_db_instance", "aws_db_instance.orders", []string{"region"}, nil
	f := throughOfferingFleet(provisioned("Instance orders-db"))
	f.res = append(f.res, inv, partial)
	q := NewQuery(f.snap(t))
	rs := contractOf(t, q, ordersDBID)
	got := map[string]string{}
	for _, r := range rs {
		got[r.Source] = r.Contract
	}
	// A read with a missing segment is no proof it is the same resource.
	if got["crossplane"] != ContractOffering || got["aws-resource-explorer"] != ContractOffering || got["terraform"] != ContractOutside {
		t.Fatalf("contracts = %v", got)
	}
}

// A viewer who may read the cluster-scoped object but not the consumer
// whose record proves it sees it through an offering they cannot see, never
// outside.
func TestManagedRestrictedHiddenProofStaysOffering(t *testing.T) {
	q := NewQuery(throughOfferingFleet(provisioned("Instance orders-db")).snap(t))
	rq := q.Restrict(func(c, ns string) bool { return c == "uid-a" && ns == "" }, nil)
	rs := contractOf(t, rq, ordersDBID)
	if len(rs) != 1 || rs[0].Contract != ContractOffering || rs[0].Offering != nil || rs[0].Evidence != "" {
		t.Fatalf("resources = %+v", rs)
	}
	if rq.outsideContract(rq.snap.Externals[ordersDBID]) != 0 {
		t.Error("counted outside")
	}
	// A cluster-access view over it keeps the carried state.
	wq := rq.WithClusterAccess(q, map[string]string{"uid-b": ClusterNoAccess})
	if rs := contractOf(t, wq, ordersDBID); len(rs) != 1 || rs[0].Contract != ContractOffering {
		t.Fatalf("cluster access = %+v", rs)
	}
}

// A managed-resources row that did not read everything makes the resource
// tally and every external answer partial; an off row (with a hint) does
// not.
func TestManagedIncompleteRowLowersCompleteness(t *testing.T) {
	f := throughOfferingFleet(provisioned("Instance orders-db"))
	complete := NewQuery(f.snap(t))
	m, _ := complete.Map(MapQuery{})
	v, _ := complete.GetExternal(ordersDBID)
	if m.Match.Completeness != CompletenessComplete || v.Meta.Completeness != CompletenessComplete {
		t.Fatalf("baseline %s %s", m.Match.Completeness, v.Meta.Completeness)
	}
	f.rows = []Provider{managedRow("uid-a", "prod-a", SourceUnavailable, "helm upgrade --set controller.cloudInventory=aws")}
	off := NewQuery(f.snap(t))
	if v, _ := off.GetExternal(ordersDBID); v.Meta.Completeness != CompletenessComplete {
		t.Fatalf("off row lowered %s", v.Meta.Completeness)
	}
	f.rows = []Provider{managedRow("uid-a", "prod-a", SourcePartial, helmHint, "unread rds.services.k8s.aws/dbinstances")}
	q := NewQuery(f.snap(t))
	m, _ = q.Map(MapQuery{})
	v, _ = q.GetExternal(ordersDBID)
	if m.Match.Completeness != CompletenessPartial || v.Meta.Completeness != CompletenessPartial ||
		!slices.ContainsFunc(v.Meta.Limitations, func(l Limitation) bool {
			return l.Code == LimitationSourcePartial && strings.Contains(l.Message, "Managed resources is partial")
		}) {
		t.Fatalf("map %s external %+v", m.Match.Completeness, v.Meta)
	}
	if o := q.Overview().Summary.Match; o.Completeness != CompletenessPartial {
		t.Errorf("overview %s", o.Completeness)
	}
	d, err := q.EntityDetail(KindExternal, ordersDBID)
	if err != nil || d.Meta.Completeness != CompletenessPartial {
		t.Errorf("entity %+v %v", d, err)
	}
}

// A namespaced managed resource's namespace is one a reader's permissions
// decide.
func TestNamespacesNameManagedResourceNamespace(t *testing.T) {
	r := ordersDB("uid-a")
	r.Source, r.Type, r.Namespace = "ack", "rds.services.k8s.aws/DBInstance", "payments-db"
	f := throughOfferingFleet()
	f.res = []RawManagedResource{r}
	if !slices.Contains(NewQuery(f.snap(t)).Namespaces(), ClusterNamespace{"uid-a", "payments-db"}) {
		t.Fatal("payments-db not reviewed")
	}
}

// A capped managed list counts the whole cluster: a reader of only some of
// its namespaces gets neither the row's nor the snapshot's count.
func TestRestrictDropsManagedCappedForNamespaceReader(t *testing.T) {
	capped := Limitation{Code: LimitationManagedResourcesCapped, Source: "detection-prod-a", Message: "120 of 620 managed resources past the bound of 500 were not kept"}
	row := managedRow("uid-a", "prod-a", SourcePartial, "")
	row.Source, row.Limitations = "detection-prod-a", []Limitation{capped}
	f := throughOfferingFleet()
	snap := f.snap(t)
	snap.Limitations = append(snap.Limitations, capped)
	snap.Providers = append(snap.Providers, row)
	has := func(q *Query) (bool, bool) {
		s := q.SnapshotForSerialization()
		onRow := slices.ContainsFunc(s.Providers, func(p Provider) bool {
			return slices.ContainsFunc(p.Limitations, func(l Limitation) bool { return l.Code == LimitationManagedResourcesCapped })
		})
		return onRow, slices.ContainsFunc(s.Limitations, func(l Limitation) bool { return l.Code == LimitationManagedResourcesCapped })
	}
	q := NewQuery(snap)
	if r, s := has(q.Restrict(func(c, ns string) bool { return c == "uid-a" && ns == "shop" }, nil)); r || s {
		t.Errorf("namespace reader keeps the count: row %v snapshot %v", r, s)
	}
	// A cluster-wide reader of the row's cluster keeps the row's count; the
	// snapshot's names no cluster, so only a reader of every one keeps it.
	if r, s := has(q.Restrict(func(c, _ string) bool { return c == "uid-a" }, nil)); !r || s {
		t.Errorf("cluster reader: row %v snapshot %v", r, s)
	}
}
