package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

var egressAsOf = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

// kernelEgress is the row fleetsrc.KernelEgressRow builds for services.
func kernelEgress(status fleet.SourceStatus, services ...fleet.ServiceKey) fleet.Provider {
	return fleet.Provider{ID: fleet.ProviderKernelEgress, Name: "Kernel egress", Evidence: "node agent on 3/4 nodes",
		UsedFor: []string{fleet.UseExternalEgress}, Status: status, Scope: fleet.ScopeCluster, Services: services,
		Limitations: []fleet.Limitation{{Code: fleet.LimitationExternalsUnreported, Source: "k8s", Message: "the node agent cannot rule out an external call"}}}
}

// egressQuery: shop-svc runs in shop, secret-svc in secret; the Kubernetes
// source reports rows beside the istio-egress (served) and cilium-fqdn (not
// served) detection rows.
func egressQuery(t *testing.T, rows ...fleet.Provider) *fleet.Query {
	t.Helper()
	res := detect.Result{AsOf: egressAsOf, Entries: []detect.Entry{detect.IstioEgress(true, nil), detect.CiliumFQDN(false, nil)}}
	rows = append(rows, detect.ProviderRows(res, "operator", egressAsOf)...)
	k := fleet.SourceKindKubernetes
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return egressAsOf }}, fleet.NewMemorySource("k8s", k, &fleet.Collection{
		Targets: []fleet.RawTarget{
			{Scope: "shop", Kind: k, Name: "shop-svc", Service: "shop-svc"},
			{Scope: "secret", Kind: k, Name: "secret-svc", Service: "secret-svc"},
		},
		Providers: rows,
	}))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

func getSourceRows(t *testing.T, o Options) []SourceInfo {
	t.Helper()
	out, err := NewServer(o).getSources(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return out.Body.Sources
}

func egressRows(rows []SourceInfo) []SourceInfo {
	return slices.DeleteFunc(slices.Clone(rows), func(s SourceInfo) bool { return !s.Egress })
}

func TestSourcesIncludeEgressRows(t *testing.T) {
	q := egressQuery(t, kernelEgress(fleet.SourcePartial, "shop-svc"))
	base := serveOptions(t, Options{Fleet: fleetOf(q)})
	var body struct {
		Sources []SourceInfo `json:"sources"`
	}
	getJSON(t, base+"/api/sources", http.StatusOK, &body)
	want := []SourceInfo{
		{Type: "kernel-egress", Enabled: true, Reason: "node agent on 3/4 nodes", Egress: true, Coverage: "partial", Source: "k8s"},
		{Type: "istio-egress", Enabled: true, Reason: "ServiceEntry CRD present (from API discovery)", Egress: true, Coverage: "complete", Source: "k8s"},
		{Type: "cilium-fqdn", Enabled: false, Reason: "no CiliumNetworkPolicy CRD (from API discovery)", Egress: true, Coverage: "unavailable", Source: "k8s"},
	}
	got := egressRows(body.Sources)
	slices.SortFunc(got, func(a, b SourceInfo) int {
		return slices.IndexFunc(want, func(w SourceInfo) bool { return w.Type == a.Type }) - slices.IndexFunc(want, func(w SourceInfo) bool { return w.Type == b.Type })
	})
	if !slices.Equal(got, want) {
		t.Fatalf("egress rows = %+v\nwant %+v", got, want)
	}
	if body.Sources[0].Type != "kubernetes" || body.Sources[0].Egress {
		t.Errorf("data sources come first and are no egress row: %+v", body.Sources)
	}
}

func TestSourcesEgressRowsFollowViewerRestriction(t *testing.T) {
	hidden := getSourceRows(t, Options{Fleet: fleetOf(egressQuery(t, kernelEgress(fleet.SourcePartial, "secret-svc"))), View: restrictTo("shop")})
	if slices.ContainsFunc(hidden, func(s SourceInfo) bool { return s.Type == fleet.ProviderKernelEgress }) {
		t.Fatalf("a viewer who sees no contributing service sees the row: %+v", hidden)
	}
	some := getSourceRows(t, Options{Fleet: fleetOf(egressQuery(t, kernelEgress(fleet.SourcePartial, "shop-svc", "secret-svc"))), View: restrictTo("shop")})
	i := slices.IndexFunc(some, func(s SourceInfo) bool { return s.Type == fleet.ProviderKernelEgress })
	if i < 0 || some[i].Reason != "Kernel egress detected cluster-wide; you may read only some namespaces" {
		t.Fatalf("a viewer without cluster scope must see the row redacted: %+v", some)
	}
	for _, s := range egressRows(some) {
		if !strings.HasSuffix(s.Reason, " detected cluster-wide; you may read only some namespaces") {
			t.Errorf("a cluster-wide egress row reaches a viewer without cluster scope unredacted: %+v", s)
		}
	}
}

func TestSourcesEgressStaleRow(t *testing.T) {
	crd := fleet.Provider{ID: fleet.ProviderPactoCRD, Name: "Pacto CRD", Evidence: "served", UsedFor: []string{fleet.UseDeclaredTargets}}
	got := providerSources(egressQuery(t, kernelEgress(fleet.SourceStale, "shop-svc"), crd))
	if len(got) != 3 {
		t.Fatalf("a row that reads no egress is listed: %+v", got)
	}
	i := slices.IndexFunc(got, func(s SourceInfo) bool { return s.Type == fleet.ProviderKernelEgress })
	if i < 0 || got[i].Enabled || got[i].Coverage != "unavailable" {
		t.Fatalf("stale row = %+v", got)
	}
	if providerSources(nil) != nil {
		t.Error("a nil query has egress rows")
	}
}

func TestSourcesViewsOnce(t *testing.T) {
	views := 0
	q := egressQuery(t, kernelEgress(fleet.SourcePartial, "shop-svc"))
	rows := getSourceRows(t, Options{Fleet: fleetOf(q), View: func(_ context.Context, q *fleet.Query) (*fleet.Query, error) {
		views++
		return q, nil
	}})
	if views != 1 || len(egressRows(rows)) != 3 {
		t.Fatalf("views = %d, rows = %+v", views, rows)
	}
}

func TestMetricsSourceCountExcludesEgress(t *testing.T) {
	q := egressQuery(t, kernelEgress(fleet.SourcePartial, "shop-svc"))
	out, err := NewServer(Options{Fleet: fleetOf(q)}).metrics(context.Background(), nil)
	if err != nil || out.Body.SourceCount != 1 {
		t.Fatalf("source count = %d, %v", out.Body.SourceCount, err)
	}
}

func TestSourceInfoJSONKeepsFields(t *testing.T) {
	b, _ := json.Marshal(SourceInfo{Type: "oci", Enabled: true, Reason: "1 revisions, 0 targets"})
	if string(b) != `{"type":"oci","enabled":true,"reason":"1 revisions, 0 targets"}` {
		t.Errorf("data source JSON = %s", b)
	}
	b, _ = json.Marshal(SourceInfo{Type: "kernel-egress", Egress: true, Coverage: "unavailable"})
	if string(b) != `{"type":"kernel-egress","enabled":false,"egress":true,"coverage":"unavailable"}` {
		t.Errorf("egress JSON = %s", b)
	}
}

// Two clusters each report a kernel-egress row: the rows name their source
// and cluster, so the partial one never hides behind the complete one.
func TestSourcesEgressRowsNameTheirCluster(t *testing.T) {
	k := fleet.SourceKindKubernetes
	src := func(id, cluster string, status fleet.SourceStatus) fleet.Source {
		return fleet.NewMemorySource(id, k, &fleet.Collection{Cluster: &fleet.Cluster{ID: "uid-" + cluster, Name: cluster}, Providers: []fleet.Provider{kernelEgress(status)}})
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return egressAsOf }},
		src("k8s-prod", "prod", fleet.SourceAvailable), src("k8s-edge", "edge", fleet.SourcePartial))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range providerSources(fleet.NewQuery(snap)) {
		got[s.Source+"/"+s.Cluster] = s.Coverage
	}
	want := map[string]string{"k8s-prod/prod": "complete", "k8s-edge/edge": "partial"}
	if len(got) != 2 || got["k8s-prod/prod"] != want["k8s-prod/prod"] || got["k8s-edge/edge"] != want["k8s-edge/edge"] {
		t.Fatalf("egress rows by source/cluster = %v, want %v", got, want)
	}
}

// TestSourcesProjectManagedRows: a detected managed-resources row is
// projected with resources set, its coverage and the evidence that names the
// missing read access; a resources row is no egress row and no data source.
func TestSourcesProjectManagedRows(t *testing.T) {
	res := detect.Result{AsOf: egressAsOf, Entries: []detect.Entry{{Kind: fleet.ProviderManagedResources, Value: "3 kinds read",
		Evidence: "unread rds.services.k8s.aws/dbinstances", Completeness: detect.Partial, Reason: "run helm upgrade"}}}
	q := egressQuery(t, detect.ProviderRows(res, "operator", egressAsOf)...)
	rows := getSourceRows(t, Options{Fleet: fleetOf(q)})
	i := slices.IndexFunc(rows, func(s SourceInfo) bool { return s.Type == fleet.ProviderManagedResources })
	want := SourceInfo{Type: fleet.ProviderManagedResources, Enabled: true, Reason: "3 kinds read unread rds.services.k8s.aws/dbinstances (from operator)",
		Resources: true, Coverage: "partial", Source: "k8s"}
	if i < 0 || rows[i] != want {
		t.Fatalf("rows = %+v\nwant %+v", rows, want)
	}
	if len(egressRows(rows)) != 2 {
		t.Errorf("a resources row is counted as egress: %+v", egressRows(rows))
	}
	out, err := NewServer(Options{Fleet: fleetOf(q)}).metrics(context.Background(), nil)
	if err != nil || out.Body.SourceCount != 1 {
		t.Errorf("source count = %d, %v", out.Body.SourceCount, err)
	}
}
