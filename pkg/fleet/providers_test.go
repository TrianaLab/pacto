package fleet

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func buildProviders(t *testing.T, sources ...Source) *FleetSnapshot {
	t.Helper()
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, sources...)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func crdRow() Provider {
	return Provider{
		ID: "pacto-crd", Name: "Pacto CRD", Evidence: "served (pacto.trianalab.io/v1alpha1)",
		UsedFor: []string{"declared-targets"}, Status: SourceAvailable,
		Objects: []ProviderObject{{Kind: "CustomResourceDefinition", Name: "pactos.pacto.trianalab.io"}},
	}
}

func workloadsRow(counts ...ProviderCount) Provider {
	return Provider{ID: "kubernetes-workloads", Name: "Kubernetes workloads", UsedFor: []string{"inferred-services"}, Status: SourceAvailable, Counts: counts}
}

func noProviderLimitations(p Provider) bool { return len(p.Limitations) == 0 }

func TestBuildKeepsProviderRows(t *testing.T) {
	dup := crdRow()
	dup.Name = "second"
	snap := buildProviders(t,
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{
			Cluster:   clusterOf(clusterA, "eu-prod", ClusterNameFromContext),
			Providers: []Provider{workloadsRow(ProviderCount{"Deployment", "shop", 2}), crdRow(), dup},
		}),
		NewMemorySource("anon", SourceKindKubernetes, &Collection{Cluster: &Cluster{ID: clusterB}, Providers: []Provider{crdRow()}}),
		NewMemorySource("files", SourceKindTargetState, &Collection{Providers: []Provider{crdRow()}}),
	)
	var got []string
	for _, p := range snap.Providers {
		got = append(got, p.Source+"/"+p.ID+"/"+p.Cluster+"/"+p.Name)
	}
	want := []string{
		"anon/pacto-crd/" + clusterB + "/Pacto CRD",
		"files/pacto-crd//Pacto CRD",
		"k8s/kubernetes-workloads/eu-prod/Kubernetes workloads",
		"k8s/pacto-crd/eu-prod/Pacto CRD",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("rows = %q, want %q", got, want)
	}
	if snap.Providers[2].Evidence != "2 Deployments" {
		t.Errorf("evidence = %q", snap.Providers[2].Evidence)
	}
}

func TestBuildDoesNotMutateProviderRows(t *testing.T) {
	objs := make([]ProviderObject, 25)
	for i := range objs {
		objs[i] = ProviderObject{Kind: "Service", Namespace: "z", Name: "o" + strconv.Itoa(24-i)}
	}
	row := workloadsRow(ProviderCount{"Deployment", "shop", 1})
	row.Objects = objs
	row.UnreadableKinds = []string{"CronJob"}
	col := &Collection{
		State:     &SourceState{Status: SourcePartial},
		Providers: []Provider{row, {ID: "x", Status: "bogus"}},
	}
	before := deepCopyCollection(t, col)
	src := NewMemorySource("k8s", SourceKindKubernetes, col)
	a, b := buildProviders(t, src), buildProviders(t, src)
	if after := deepCopyCollection(t, col); !reflect.DeepEqual(before, after) || !reflect.DeepEqual(col.Providers[0].Objects, objs) {
		t.Fatal("Build wrote the source's Collection")
	}
	if !reflect.DeepEqual(a.Providers, b.Providers) || a.SnapshotID != b.SnapshotID {
		t.Fatal("two builds from one source disagree")
	}
}

func deepCopyCollection(t *testing.T, col *Collection) []Provider {
	t.Helper()
	out := make([]Provider, len(col.Providers))
	for i, p := range col.Providers {
		p.UsedFor, p.Objects, p.Limitations = slices.Clone(p.UsedFor), slices.Clone(p.Objects), slices.Clone(p.Limitations)
		p.Counts, p.UnreadableKinds = slices.Clone(p.Counts), slices.Clone(p.UnreadableKinds)
		out[i] = p
	}
	return out
}

func TestProviderStatusInheritsSource(t *testing.T) {
	row := crdRow()
	row.Status = ""
	snap := buildProviders(t, NewMemorySource("k8s", SourceKindKubernetes, &Collection{State: &SourceState{Status: SourceStale}, Providers: []Provider{row}}))
	if p := snap.Providers[0]; p.Status != SourceStale || noProviderLimitations(p) {
		t.Errorf("row = %+v, want the source's stale with a reason", p)
	}
	ok := buildProviders(t, NewMemorySource("k8s", SourceKindKubernetes, &Collection{Providers: []Provider{row}}))
	if p := ok.Providers[0]; p.Status != SourceAvailable || !noProviderLimitations(p) {
		t.Errorf("row = %+v, want available with no limitation", p)
	}
}

func TestProviderStatusNeverBetterThanSource(t *testing.T) {
	snap := buildProviders(t, NewMemorySource("k8s", SourceKindKubernetes, &Collection{State: &SourceState{Status: SourcePartial}, Providers: []Provider{crdRow()}}))
	if p := snap.Providers[0]; p.Status != SourcePartial {
		t.Errorf("row status = %s, want partial", p.Status)
	}
	// A row worse than its source keeps its own status.
	stale := crdRow()
	stale.Status = SourceStale
	stale.Limitations = []Limitation{{Code: "OLD", Message: "old"}}
	snap = buildProviders(t, NewMemorySource("k8s", SourceKindKubernetes, &Collection{State: &SourceState{Status: SourcePartial}, Providers: []Provider{stale}}))
	if p := snap.Providers[0]; p.Status != SourceStale || len(p.Limitations) != 1 {
		t.Errorf("row = %+v, want its own stale and only its own reason", p)
	}
}

func TestProviderStatusFollowsLateSourceDowngrade(t *testing.T) {
	snap := buildProviders(t,
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Cluster: clusterOf(clusterA, "eu-prod", ClusterNameFromContext), Targets: []RawTarget{workload("payments", "payments")}}),
		NewMemorySource("file", SourceKindTargetState, &Collection{Targets: []RawTarget{workload("payments", "payments")}, Providers: []Provider{crdRow()}}),
	)
	if got := clusterSourceStatus(t, snap, "file"); got != SourcePartial {
		t.Fatalf("source = %s, want the late partial", got)
	}
	if p := snap.Providers[0]; p.Status != SourcePartial || p.Limitations[0].Code != LimitationProviderSourceDegraded {
		t.Errorf("row = %+v, want partial with a reason", p)
	}
}

func TestProviderInheritedStatusExplained(t *testing.T) {
	bare := crdRow()
	bare.ID, bare.Status = "bare", SourcePartial
	snap := buildProviders(t,
		NewMemorySource("down", SourceKindKubernetes, &Collection{State: &SourceState{Status: SourceStale}, Providers: []Provider{crdRow()}}),
		NewMemorySource("up", SourceKindKubernetes, &Collection{Providers: []Provider{bare}}),
	)
	for _, p := range snap.Providers {
		if p.Status != SourceAvailable && len(p.Limitations) == 0 {
			t.Errorf("row %s/%s is %s with no reason", p.Source, p.ID, p.Status)
		}
	}
	if l := snap.Providers[0].Limitations[0]; l.Code != LimitationProviderSourceDegraded || l.Source != "down" || !strings.Contains(l.Message, "down") || !strings.Contains(l.Message, "stale") {
		t.Errorf("lowered row limitation = %+v", l)
	}
	if l := snap.Providers[1].Limitations; len(l) != 1 || l[0].Code != LimitationProviderSourceDegraded || l[0].Source != "up" {
		t.Errorf("producer-set partial row limitations = %+v", l)
	}
}

func TestProvidersDroppedForDeclaredUnavailableSource(t *testing.T) {
	snap := buildProviders(t, NewMemorySource("k8s", SourceKindKubernetes, &Collection{State: &SourceState{Status: SourceUnavailable}, Providers: []Provider{crdRow()}}))
	if len(snap.Providers) != 0 {
		t.Errorf("rows = %+v, want none", snap.Providers)
	}
}

func TestProviderInvalidStatusCanonicalized(t *testing.T) {
	row := crdRow()
	row.Status = "bogus"
	snap := buildProviders(t, NewMemorySource("k8s", SourceKindKubernetes, &Collection{Providers: []Provider{row}}))
	if p := snap.Providers[0]; p.Status != SourcePartial || noProviderLimitations(p) {
		t.Errorf("row = %+v, want partial with a reason", p)
	}
}

func TestProviderRowsDoNotChangeCompleteness(t *testing.T) {
	mk := func(rows []Provider) *FleetSnapshot {
		return buildProviders(t, NewMemorySource("k8s", SourceKindKubernetes, &Collection{
			Targets:   []RawTarget{workload("shop", "web")},
			Providers: rows,
		}))
	}
	bad := crdRow()
	bad.Status = SourceStale
	without, with := mk(nil), mk([]Provider{bad, workloadsRow()})
	if with.Completeness != without.Completeness || !reflect.DeepEqual(with.Limitations, without.Limitations) || !reflect.DeepEqual(with.Sources, without.Sources) {
		t.Errorf("rows changed the snapshot: %s %+v %+v", with.Completeness, with.Limitations, with.Sources)
	}
}

func TestProviderObjectsCapped(t *testing.T) {
	row := crdRow()
	row.Objects = nil
	for i := range 25 {
		row.Objects = append(row.Objects, ProviderObject{Kind: "Service", Namespace: "n", Name: "o" + strconv.Itoa(124-i)})
	}
	snap := buildProviders(t, NewMemorySource("k8s", SourceKindKubernetes, &Collection{Providers: []Provider{row}}))
	p := snap.Providers[0]
	if len(p.Objects) != MaxProviderObjects || p.ObjectsTotal != 25 || p.Objects[0].Name != "o100" || p.Objects[19].Name != "o119" {
		t.Errorf("objects = %d (total %d) first %+v", len(p.Objects), p.ObjectsTotal, p.Objects[0])
	}
}

func TestWorkloadEvidence(t *testing.T) {
	for _, tc := range []struct {
		name       string
		counts     map[string]int
		unreadable []string
		want       string
	}{
		{"order", map[string]int{"CronJob": 1, "Job": 2, "Deployment": 412, "Pod": 3, "StatefulSet": 0, "DaemonSet": 2}, nil, "412 Deployments, 2 DaemonSets, 1 CronJob, 2 Jobs, 3 Pods"},
		{"unreadable", map[string]int{"Deployment": 1, "StatefulSet": 4}, []string{"CronJob", "StatefulSet"}, "1 Deployment, StatefulSets unreadable, CronJobs unreadable"},
		{"empty", map[string]int{"Deployment": 0, "CronJob": 0}, nil, "no workloads found"},
		{"nothing", nil, nil, "no workloads found"},
		{"one", map[string]int{"StatefulSet": 7}, nil, "7 StatefulSets"},
		{"all unreadable", nil, []string{"Deployment"}, "Deployments unreadable"},
	} {
		if got := WorkloadEvidence(tc.counts, tc.unreadable); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestProviderEvidenceRenderedFromCounts(t *testing.T) {
	row := workloadsRow()
	row.Counts = []ProviderCount{}
	row.Evidence = "a producer wording"
	snap := buildProviders(t, NewMemorySource("k8s", SourceKindKubernetes, &Collection{Providers: []Provider{row}}))
	if got := snap.Providers[0].Evidence; got != "no workloads found" {
		t.Errorf("evidence = %q", got)
	}
	data, _ := json.Marshal(snap.Providers[0])
	if strings.Contains(string(data), "ounts") || strings.Contains(string(data), "nreadable") {
		t.Errorf("counts serialised: %s", data)
	}
}

func TestSnapshotIDCoversProviders(t *testing.T) {
	a := buildProviders(t, NewMemorySource("k8s", SourceKindKubernetes, &Collection{}))
	b := buildProviders(t, NewMemorySource("k8s", SourceKindKubernetes, &Collection{Providers: []Provider{crdRow()}}))
	if a.SnapshotID == b.SnapshotID {
		t.Error("a provider row must change the snapshot id")
	}
}

// providerFleet is a cluster with targets in team-a and team-b, a workloads
// row counting both, the CRD row and a row with an object only in monitoring.
func providerFleet(t *testing.T) *Query {
	t.Helper()
	mon := Provider{ID: "prometheus", Name: "Prometheus", Evidence: "served (monitoring.coreos.com/v1)", UsedFor: []string{"observed-calls"}, Status: SourcePartial,
		Objects:     []ProviderObject{{Kind: "Prometheus", Namespace: "monitoring", Name: "prometheus-k8s"}, {Kind: "Prometheus", Namespace: "team-a", Name: "local"}},
		Limitations: []Limitation{{Code: "SCRAPE", Message: "prometheus-k8s in monitoring is not scraped"}, {Code: "NS", Message: "nothing about secrets"}},
	}
	snap := buildProviders(t, NewMemorySource("k8s", SourceKindKubernetes, &Collection{
		Targets: []RawTarget{workload("team-a", "web"), workload("team-b", "api")},
		Providers: []Provider{
			crdRow(),
			workloadsRow(ProviderCount{"Deployment", "team-a", 3}, ProviderCount{"Deployment", "team-b", 5}, ProviderCount{"CronJob", "team-b", 1}),
			mon,
		},
	}))
	return NewQuery(snap)
}

func providerByID(t *testing.T, rows []Provider, id string) Provider {
	t.Helper()
	for _, p := range rows {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("no row %s in %+v", id, rows)
	return Provider{}
}

func TestRestrictProvidersKeepsRows(t *testing.T) {
	q := providerFleet(t)
	r := q.Restrict(func(ns string) bool { return ns == "team-a" }, nil).SnapshotForSerialization()
	if len(r.Providers) != 3 {
		t.Fatalf("rows = %+v, want all three", r.Providers)
	}
	if q.Restrict(func(string) bool { return true }, nil) != q {
		t.Error("a cluster-wide reader gets the same query")
	}
}

func TestRestrictProvidersHidesNamespacedObjects(t *testing.T) {
	q := providerFleet(t)
	orig := q.SnapshotForSerialization()
	r := q.Restrict(func(ns string) bool { return ns != "monitoring" && ns != "" }, nil).SnapshotForSerialization()
	mon := providerByID(t, r.Providers, "prometheus")
	if len(mon.Objects) != 1 || mon.Objects[0].Namespace != "team-a" || mon.ObjectsTotal != 2 {
		t.Errorf("objects = %+v total %d", mon.Objects, mon.ObjectsTotal)
	}
	crd := providerByID(t, r.Providers, "pacto-crd")
	if len(crd.Objects) != 0 || crd.Evidence != "served (pacto.trianalab.io/v1alpha1)" || crd.ObjectsTotal != 1 {
		t.Errorf("restricted crd row = %+v", crd)
	}
	if len(providerByID(t, orig.Providers, "prometheus").Objects) != 2 {
		t.Error("Restrict wrote the base snapshot")
	}
	// A reader of every namespace, cluster scope included, keeps the CRD object.
	all := q.Restrict(func(ns string) bool { return ns != "monitoring" }, nil).SnapshotForSerialization()
	if len(providerByID(t, all.Providers, "pacto-crd").Objects) != 1 {
		t.Error("a cluster-scope reader must see the CRD object")
	}
}

func TestRestrictProvidersObjectNamespaceOnlyOnProvider(t *testing.T) {
	q := providerFleet(t)
	if got := q.Namespaces(); !slices.Contains(got, "monitoring") {
		t.Fatalf("Namespaces = %q, want monitoring reviewed", got)
	}
	r := q.Restrict(func(ns string) bool { return ns != "monitoring" }, nil)
	if r == q {
		t.Fatal("a refused namespace on a provider row must restrict")
	}
	for _, o := range providerByID(t, r.SnapshotForSerialization().Providers, "prometheus").Objects {
		if o.Namespace == "monitoring" {
			t.Error("the monitoring object stayed")
		}
	}
	// A restricted reader of every namespace still gets a view without the
	// cluster-scoped object.
	if q.Restrict(func(ns string) bool { return ns != "" }, nil) == q {
		t.Error("a cluster-scoped object must restrict a reader without cluster scope")
	}
}

func TestRestrictProvidersRecountsEvidence(t *testing.T) {
	q := providerFleet(t)
	if got := providerByID(t, q.SnapshotForSerialization().Providers, "kubernetes-workloads").Evidence; got != "8 Deployments, 1 CronJob" {
		t.Fatalf("unrestricted evidence = %q", got)
	}
	r := q.Restrict(func(ns string) bool { return ns != "team-b" }, nil).SnapshotForSerialization()
	if got := providerByID(t, r.Providers, "kubernetes-workloads").Evidence; got != "3 Deployments (+6 in namespaces hidden from you)" {
		t.Errorf("restricted evidence = %q", got)
	}
	crd := providerByID(t, r.Providers, "pacto-crd")
	if strings.ContainsAny(crd.Evidence, "0123456789") && crd.Evidence != "served (pacto.trianalab.io/v1alpha1)" {
		t.Errorf("crd evidence carries a count: %q", crd.Evidence)
	}
	if r.Sources[0].TargetCount != 1 {
		t.Errorf("recounted targets = %d, want the visible one", r.Sources[0].TargetCount)
	}
}

func TestRestrictProvidersDropsHiddenLimitations(t *testing.T) {
	rows := []Provider{{
		ID: "p", Status: SourcePartial,
		Limitations: []Limitation{
			NamespaceScopeLimitation("k8s", []string{"a"}, 0),
			NamespaceUnreadableLimitation("k8s", "secret", "forbidden"),
			NamespaceUnreadableLimitation("k8s", "a", "forbidden"),
			{Code: "X", Message: "service hidden-svc misbehaves"},
			{Code: LimitationProviderSourceDegraded, Source: "k8s", Message: "source k8s is partial; the counts cover only what it read"},
		},
	}}
	got := restrictProviders(rows, []string{"hidden-svc", ""}, func(ns string) bool { return ns == "a" })
	var codes []string
	for _, l := range got[0].Limitations {
		codes = append(codes, l.Code)
	}
	if !slices.Equal(codes, []string{LimitationK8sNamespaceUnreadable, LimitationProviderSourceDegraded}) || !strings.Contains(got[0].Limitations[0].Message, " a:") {
		t.Errorf("limitations = %+v", got[0].Limitations)
	}
	if len(rows[0].Limitations) != 5 {
		t.Error("restrictProviders wrote its input")
	}
	if restrictProviders(nil, nil, func(string) bool { return true }) != nil {
		t.Error("no rows stay no rows")
	}
}

func TestRestrictProvidersKeepsAReason(t *testing.T) {
	rows := []Provider{
		{ID: "p", Source: "k8s", Status: SourcePartial, Limitations: []Limitation{NamespaceUnreadableLimitation("k8s", "secret", "forbidden")}},
		{ID: "ok", Source: "k8s", Status: SourceAvailable, Limitations: []Limitation{NamespaceUnreadableLimitation("k8s", "secret", "forbidden")}},
	}
	got := restrictProviders(rows, nil, func(ns string) bool { return ns == "a" })
	if len(got[0].Limitations) != 1 || got[0].Limitations[0].Code != LimitationProviderSourceDegraded ||
		got[0].Limitations[0].Message != "source k8s reported this row as partial; the reason is hidden by your permissions" {
		t.Errorf("partial row limitations = %+v, want one degraded reason", got[0].Limitations)
	}
	if len(got[1].Limitations) != 0 {
		t.Errorf("an available row needs no reason: %+v", got[1].Limitations)
	}
}

func TestRestrictProvidersMasksEvidenceNamingHidden(t *testing.T) {
	q := providerFleet(t)
	r := q.Restrict(func(ns string) bool { return ns != "monitoring" }, nil).SnapshotForSerialization()
	mon := providerByID(t, r.Providers, "prometheus")
	for _, l := range mon.Limitations {
		if strings.Contains(l.Message, "monitoring") {
			t.Errorf("limitation names a hidden namespace: %+v", l)
		}
	}
	if mon.Evidence != "hidden by your permissions" {
		t.Errorf("evidence = %q, want masked: it names monitoring", mon.Evidence)
	}
}

func TestStatusCarriesProviders(t *testing.T) {
	q := providerFleet(t)
	res := q.Status(StatusQuery{})
	if len(res.Providers) != 3 || res.ProvidersTruncated {
		t.Fatalf("status providers = %+v", res.Providers)
	}
	if res.Providers[0].ID != "prometheus" {
		t.Errorf("least healthy first: %q", res.Providers[0].ID)
	}
}

func TestStatusProvidersBounded(t *testing.T) {
	rows := make([]Provider, MaxProviders+1)
	for i := range rows {
		rows[i] = Provider{ID: "p" + strconv.Itoa(1000+i), Status: SourceAvailable}
	}
	rows[MaxProviders].Status = SourceStale
	rows[MaxProviders].Limitations = []Limitation{{Code: "S", Message: "s"}}
	q := NewQuery(buildProviders(t, NewMemorySource("k8s", SourceKindKubernetes, &Collection{Providers: rows})))
	res := q.Status(StatusQuery{})
	if len(res.Providers) != MaxProviders || !res.ProvidersTruncated || res.Providers[0].Status != SourceStale || res.Providers[1].ID != "p1000" {
		t.Errorf("bounded = %d truncated=%v first=%+v", len(res.Providers), res.ProvidersTruncated, res.Providers[0])
	}
	if q.SnapshotForSerialization().Providers[0].ID != "p1000" {
		t.Error("boundProviders sorted the snapshot's rows")
	}
}

func TestQueryProviders(t *testing.T) {
	q := providerFleet(t)
	res := q.Providers()
	if res.Meta.SchemaVersion != ProductSchemaVersion || res.Total != 3 || res.Truncated || len(res.Providers) != 3 || res.Providers[0].ID != "prometheus" {
		t.Errorf("providers = %+v", res)
	}
	empty := NewQuery(buildProviders(t, NewMemorySource("local", "local", &Collection{}))).Providers()
	if empty.Providers == nil || len(empty.Providers) != 0 || empty.Total != 0 {
		t.Errorf("empty providers = %+v", empty)
	}
}
