package fleet

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// detectedSource is an advisory detection source with rows and status.
func detectedSource(status SourceStatus, rows ...Provider) Source {
	return NewMemorySource("detection", SourceKindDetection, &Collection{Advisory: true, Providers: rows, State: &SourceState{Status: status}})
}

func detectedRow(id string, scope string, objects ...ProviderObject) Provider {
	at := fixedNow()
	return Provider{ID: id, Name: strings.ToUpper(id[:1]) + id[1:], Evidence: id + " (from detection)", UsedFor: []string{"proposal"}, Status: SourceAvailable,
		Provenance: ProvenanceInferred, AsOf: &at, Hint: "--set " + id, Details: []string{id + " detail"}, Scope: scope, Objects: objects}
}

// detectedFleet: Kubernetes targets in team-a and team-b, plus a cloud row
// read cluster-wide, a registry row voted from team-a and team-b, and an
// exposure row on a Gateway in infra.
func detectedFleet(t *testing.T) *Query {
	t.Helper()
	return NewQuery(buildProviders(t,
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Targets: []RawTarget{workload("team-a", "web"), workload("team-b", "api")}}),
		detectedSource(SourceAvailable,
			detectedRow("cloud", ScopeCluster),
			detectedRow("registry", "", ProviderObject{Kind: "Namespace", Namespace: "team-a", Name: "team-a"}, ProviderObject{Kind: "Namespace", Namespace: "team-b", Name: "team-b"}),
			detectedRow("exposure", "", ProviderObject{Kind: "Gateway", Namespace: "infra", Name: "public"}),
		)))
}

func readerOf(nss ...string) func(string) bool {
	return func(ns string) bool { return slices.Contains(nss, ns) }
}

func redacted(p Provider) bool {
	return p.Hint == "" && p.Details == nil && !strings.Contains(p.Evidence, "(from detection)")
}

func TestProviderDetectedFieldsAdditive(t *testing.T) {
	b, err := json.Marshal(struct {
		P Provider
		S SourceState
	}{crdRow(), SourceState{ID: "k8s", Status: SourceAvailable}})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"provenance", "asOf", "hint", "details", "scope", "advisory", "ClusterID"} {
		if strings.Contains(string(b), `"`+key+`"`) {
			t.Errorf("%s serialised on a row without it: %s", key, b)
		}
	}
}

// assertAdvisoryIgnored: the advisory detection source changes neither the
// completeness nor any degraded tally, and adds no snapshot limitation.
func assertAdvisoryIgnored(t *testing.T, status SourceStatus) {
	t.Helper()
	k8s := NewMemorySource("k8s", SourceKindKubernetes, &Collection{Targets: []RawTarget{workload("team-a", "web")}})
	without := buildProviders(t, k8s)
	with := buildProviders(t, k8s, detectedSource(status, detectedRow("cloud", ScopeCluster)))
	if with.Completeness != CompletenessComplete || with.Completeness != without.Completeness || with.ConsumerCompleteness() != CompletenessComplete {
		t.Errorf("completeness %s, want %s", with.Completeness, without.Completeness)
	}
	if !reflect.DeepEqual(with.Limitations, without.Limitations) {
		t.Errorf("limitations %+v, want %+v", with.Limitations, without.Limitations)
	}
	i := slices.IndexFunc(with.Sources, func(s SourceState) bool { return s.ID == "detection" })
	if i < 0 || with.Sources[i].Status != status || !with.Sources[i].Advisory {
		t.Fatalf("detection source = %+v", with.Sources)
	}
	q := NewQuery(with)
	if c := q.Providers().Meta.SourceCounts; c.Partial+c.Stale+c.Unavailable != 0 || c.Total != 1 {
		t.Errorf("source counts %+v count the advisory source", c)
	}
	if s := q.Overview().Summary; s.DegradedSources+s.StaleSources+s.UnavailableSources != 0 {
		t.Errorf("overview tallies %d %d %d", s.DegradedSources, s.StaleSources, s.UnavailableSources)
	}
	// Service history stays complete and a single-match legacy key resolves.
	if q.sourcesIncomplete() || q.revisionsPartial(&ServiceRecord{}) {
		t.Error("the advisory source makes history partial or a legacy key ambiguous")
	}
}

func TestDetectionUnavailableLeavesSnapshotComplete(t *testing.T) {
	assertAdvisoryIgnored(t, SourceUnavailable)
}

func TestDetectionStaleLeavesSnapshotComplete(t *testing.T) {
	assertAdvisoryIgnored(t, SourceStale)
	assertAdvisoryIgnored(t, SourcePartial)
}

// TestAvailableAdvisorySourceIsCounted: an available advisory source is a
// data source like any other.
func TestAvailableAdvisorySourceIsCounted(t *testing.T) {
	snap := buildProviders(t, detectedSource(SourceAvailable, detectedRow("cloud", ScopeCluster)))
	if c := NewQuery(snap).Providers().Meta.SourceCounts; c.Total != 1 || c.Available != 1 {
		t.Errorf("counts = %+v", c)
	}
}

func TestDetectionClusterMismatchIsNamed(t *testing.T) {
	other, same := detectedRow("cloud", ScopeCluster), detectedRow("nodes", ScopeCluster)
	other.ClusterID, other.Cluster = "uid-2", "uid-2"
	same.ClusterID, same.Cluster, same.Evidence = "uid-1", "uid-1", "kernel 6.1 (from nodes, cluster uid-1)"
	snap := buildProviders(t,
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Cluster: clusterOf("uid-1", "prod", ClusterNameFromContext), Targets: []RawTarget{workload("team-a", "web")}}),
		detectedSource(SourceAvailable, other, same))
	cloud := providerByID(t, snap.Providers, "cloud")
	if !slices.ContainsFunc(cloud.Limitations, func(l Limitation) bool {
		return l.Code == LimitationDetectionClusterMismatch && strings.Contains(l.Message, "uid-2")
	}) || cloud.Status != SourceStale {
		t.Errorf("cloud row = %+v", cloud)
	}
	if nodes := providerByID(t, snap.Providers, "nodes"); nodes.Cluster != "prod" || nodes.Evidence != "kernel 6.1 (from nodes, cluster prod)" {
		t.Errorf("a matching row is named by its cluster's name: %+v", nodes)
	}
	if clusterSourceStatus(t, snap, "detection") != SourceStale || snap.Completeness != CompletenessComplete {
		t.Errorf("detection source %s, completeness %s", clusterSourceStatus(t, snap, "detection"), snap.Completeness)
	}
	// With no cluster in the snapshot there is nothing to compare: no mismatch.
	alone := buildProviders(t, detectedSource(SourceAvailable, other))
	if p := providerByID(t, alone.Providers, "cloud"); len(p.Limitations) != 0 || p.Cluster != "uid-2" {
		t.Errorf("clusterless snapshot row = %+v", p)
	}
}

func TestQueryNamespacesIncludesDetected(t *testing.T) {
	if got := detectedFleet(t).Namespaces(); !slices.Contains(got, "infra") {
		t.Errorf("Namespaces = %q, want the Gateway namespace reviewed", got)
	}
}

func TestRestrictRedactsDetectedNamespaces(t *testing.T) {
	r := detectedFleet(t).Restrict(readerOf("team-a", "infra"), nil).SnapshotForSerialization()
	reg := providerByID(t, r.Providers, "registry")
	if !redacted(reg) || reg.Evidence != "Registry found in a namespace you cannot read" || len(reg.Objects) != 1 {
		t.Errorf("registry row = %+v", reg)
	}
}

func TestRestrictKeepsVisibleDetectedNamespaces(t *testing.T) {
	r := detectedFleet(t).Restrict(readerOf("team-a", "team-b", "infra"), nil).SnapshotForSerialization()
	for _, id := range []string{"registry", "exposure"} {
		if p := providerByID(t, r.Providers, id); redacted(p) || p.Hint == "" || len(p.Details) != 1 {
			t.Errorf("%s row redacted for a reader of its namespaces: %+v", id, p)
		}
	}
}

func TestRestrictRedactsDetectedNamespaceNotInSnapshot(t *testing.T) {
	r := detectedFleet(t).Restrict(readerOf("team-a", "team-b"), nil).SnapshotForSerialization()
	if p := providerByID(t, r.Providers, "exposure"); !redacted(p) || len(p.Objects) != 0 || strings.Contains(p.Hint+p.Evidence, "infra") {
		t.Errorf("exposure row = %+v", p)
	}
}

func TestRestrictRedactsClusterScopedDetectedRows(t *testing.T) {
	q := detectedFleet(t)
	r := q.Restrict(func(ns string) bool { return ns != "" }, nil)
	if r == q {
		t.Fatal("a cluster-scoped detected row must restrict a reader without cluster scope")
	}
	cloud := providerByID(t, r.SnapshotForSerialization().Providers, "cloud")
	if !redacted(cloud) || cloud.Evidence != "Cloud detected cluster-wide; you may read only some namespaces" {
		t.Errorf("cloud row = %+v", cloud)
	}
	if q.Restrict(func(string) bool { return true }, anyKind) != q {
		t.Error("a cluster-wide reader gets the same query")
	}
}

func TestRestrictDetectedNoEarlyReturn(t *testing.T) {
	q := NewQuery(buildProviders(t,
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Targets: []RawTarget{workload("team-a", "web")}}),
		detectedSource(SourceAvailable, detectedRow("exposure", "", ProviderObject{Kind: "Gateway", Namespace: "infra", Name: "public"}))))
	r := q.Restrict(readerOf("team-a", ""), nil)
	if r == q {
		t.Fatal("a hidden Gateway namespace must restrict")
	}
	if p := providerByID(t, r.SnapshotForSerialization().Providers, "exposure"); !redacted(p) {
		t.Errorf("exposure row = %+v", p)
	}
}

// TestRestrictRedactsCappedDetectedRow: a row capped to MaxProviderObjects
// may hold a namespace nobody reviewed, so a restricted reader sees it
// redacted even when every object left is visible.
func TestRestrictRedactsCappedDetectedRow(t *testing.T) {
	var objs []ProviderObject
	for i := range MaxProviderObjects + 1 {
		ns := "team-" + string(rune('a'+i))
		objs = append(objs, ProviderObject{Kind: "Namespace", Namespace: ns, Name: ns})
	}
	q := NewQuery(buildProviders(t, detectedSource(SourceAvailable, detectedRow("registry", "", objs...))))
	r := q.Restrict(func(ns string) bool { return ns != "" }, nil)
	if r == q || !redacted(providerByID(t, r.SnapshotForSerialization().Providers, "registry")) {
		t.Error("a capped detected row must be redacted for a restricted reader")
	}
}

// TestProvidersListReportedBeforeInferred: a reported row comes before every
// inferred one, even a healthier one, so a guess never pushes a fact behind
// Show all; each group stays least healthy first.
func TestProvidersListReportedBeforeInferred(t *testing.T) {
	stale := detectedRow("nodes", ScopeCluster)
	stale.Status = SourceUnavailable
	rows, _ := boundProviders([]Provider{
		detectedRow("cloud", ScopeCluster), stale,
		{ID: "crd", Status: SourceAvailable}, {ID: "workloads", Status: SourcePartial},
	})
	var got []string
	for _, p := range rows {
		got = append(got, p.ID)
	}
	if want := []string{"workloads", "crd", "nodes", "cloud"}; !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}
