package app

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/kubecluster"
)

func writeLocalBundle(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "pactoVersion: \"2.0\"\nservice:\n  name: " + name + "\n  version: \"1.0.0\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestService_Fleet(t *testing.T) {
	root := t.TempDir()
	writeLocalBundle(t, filepath.Join(root, "svc-a"), "svc-a")

	targetState := filepath.Join(t.TempDir(), "targets.yaml")
	body := "schemaVersion: pacto.dev/fleet-targets/v1\ntargets:\n  - scope: prod\n    kind: k8s\n    name: svc-a\n    service: svc-a\n    compliance: Compliant\n"
	if err := os.WriteFile(targetState, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	fixed := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	svc := NewService(nil, nil)
	snap, err := svc.Fleet(context.Background(), FleetOptions{
		LocalRoots:       []string{root},
		TargetStateFiles: []string{targetState},
		Concurrency:      2,
		Now:              func() time.Time { return fixed },
	})
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	if !snap.GeneratedAt.Equal(fixed) {
		t.Errorf("GeneratedAt = %v, want injected clock %v", snap.GeneratedAt, fixed)
	}
	if snap.Service("svc-a") == nil {
		t.Error("expected service svc-a in snapshot")
	}
	if len(snap.Targets) != 1 {
		t.Errorf("got %d targets, want 1", len(snap.Targets))
	}
	// Single source of each kind → clean, unsuffixed ids.
	ids := map[string]bool{}
	for _, s := range snap.Sources {
		ids[s.ID] = true
	}
	if !ids["local"] || !ids["target-state"] {
		t.Errorf("source ids = %v, want local + target-state", ids)
	}
}

func TestFleetDisclosesUncheckedFreshness(t *testing.T) {
	root := t.TempDir()
	writeLocalBundle(t, filepath.Join(root, "svc-a"), "svc-a")
	targetState := filepath.Join(t.TempDir(), "targets.yaml")
	body := "schemaVersion: pacto.dev/fleet-targets/v1\ntargets:\n  - scope: prod\n    kind: k8s\n    name: svc-a\n    service: svc-a\n    compliance: Compliant\n"
	if err := os.WriteFile(targetState, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	has := func(window time.Duration) bool {
		snap, err := NewService(nil, nil).Fleet(context.Background(), FleetOptions{
			LocalRoots: []string{root}, TargetStateFiles: []string{targetState}, FreshnessWindow: window,
		})
		if err != nil {
			t.Fatalf("Fleet: %v", err)
		}
		return slices.ContainsFunc(snap.Limitations, func(l fleet.Limitation) bool {
			return l.Code == fleet.LimitationFreshnessNotEvaluated
		})
	}
	if !has(0) {
		t.Error("the product build path must disclose a zero freshness window")
	}
	if has(fleet.RecentEvidenceWindow) {
		t.Error("a set window checked freshness, so there is nothing to disclose")
	}
}

func TestSourceID(t *testing.T) {
	cases := []struct {
		kind     string
		i, total int
		want     string
	}{
		{"local", 0, 1, "local"},
		{"local", 0, 2, "local-1"},
		{"local", 1, 2, "local-2"},
		{"target-state", 0, 1, "target-state"},
		{"target-state", 1, 3, "target-state-2"},
	}
	for _, tc := range cases {
		if got := sourceID(tc.kind, tc.i, tc.total); got != tc.want {
			t.Errorf("sourceID(%q,%d,%d) = %q, want %q", tc.kind, tc.i, tc.total, got, tc.want)
		}
	}
}

func TestService_Fleet_MultipleSourcesGetSuffixedIDs(t *testing.T) {
	r1 := t.TempDir()
	writeLocalBundle(t, filepath.Join(r1, "svc-a"), "svc-a")
	r2 := t.TempDir()
	writeLocalBundle(t, filepath.Join(r2, "svc-b"), "svc-b")

	svc := NewService(nil, nil)
	snap, err := svc.Fleet(context.Background(), FleetOptions{LocalRoots: []string{r1, r2}})
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	ids := map[string]bool{}
	for _, s := range snap.Sources {
		ids[s.ID] = true
	}
	if !ids["local-1"] || !ids["local-2"] {
		t.Errorf("source ids = %v, want local-1 + local-2", ids)
	}
}

func TestService_Fleet_EvidenceURLs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"schemaVersion":"pacto.dev/evidence-source/v2","health":{"status":"ready","subjects":1},"targets":[{"subject":"svc-a","producer":"prod-eu","compliance":"Compliant","coverage":{"evaluated":3,"required":5},"evidenceAt":"2026-07-29T11:00:00Z","acceptedAt":"2026-07-29T11:00:00Z"}]}`))
	}))
	defer srv.Close()

	svc := NewService(nil, nil)
	snap, err := svc.Fleet(context.Background(), FleetOptions{EvidenceURLs: []string{srv.URL}})
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	found := false
	for _, tgt := range snap.Targets {
		if tgt.Scope == "prod-eu" && tgt.Name == "svc-a" && tgt.Kind == "external" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an external target prod-eu/svc-a, got %+v", snap.Targets)
	}
	ids := map[string]bool{}
	for _, s := range snap.Sources {
		ids[s.ID] = true
	}
	if !ids["evidence-http"] {
		t.Errorf("source ids = %v, want evidence-http", ids)
	}
}

func TestService_Fleet_EvidenceURL_Unavailable(t *testing.T) {
	// An Evidence Server nobody could reach is a failing source (a limitation), not
	// a build abort and not an environment with no targets.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	svc := NewService(nil, nil)
	snap, err := svc.Fleet(context.Background(), FleetOptions{EvidenceURLs: []string{url}})
	if err != nil {
		t.Fatalf("Fleet returned a hard error, want partial snapshot: %v", err)
	}
	found := false
	for _, l := range snap.Limitations {
		if l.Code == fleet.LimitationSourceUnavailable && l.Source == "evidence-http" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a SOURCE_UNAVAILABLE limitation for evidence-http, got %+v", snap.Limitations)
	}
}

// fakeFleetStore is an oci.BundleStore for the OCI/cache wiring tests. It does
// not expose CacheDir (so bundleStoreCacheDir falls back to "").
type fakeFleetStore struct {
	bundles map[string]*contract.Bundle
}

func (f fakeFleetStore) Push(context.Context, string, *contract.Bundle) (string, error) {
	return "", nil
}
func (f fakeFleetStore) ListTags(context.Context, string) ([]string, error) { return nil, nil }
func (f fakeFleetStore) Resolve(context.Context, string) (string, error)    { return "sha256:x", nil }
func (f fakeFleetStore) Pull(_ context.Context, ref string) (*contract.Bundle, error) {
	if b, ok := f.bundles[ref]; ok {
		return b, nil
	}
	// A registry is content-addressed: the artifact a tag names is served just as
	// well by its digest, which is how the OCI source now fetches it. The fixtures
	// are keyed by tag, so a digest-pinned ref resolves to its repository's bundle.
	if i := strings.Index(ref, "@"); i >= 0 {
		for tagged, b := range f.bundles {
			if strings.HasPrefix(tagged, ref[:i]+":") {
				return b, nil
			}
		}
	}
	return nil, errors.New("not found")
}

// fakeFleetCacheStore adds a real CacheDir so the cache source can walk it.
type fakeFleetCacheStore struct {
	fakeFleetStore
	dir string
}

func (f fakeFleetCacheStore) CacheDir() string { return f.dir }

func bundleForFleet(name string) *contract.Bundle {
	return &contract.Bundle{Contract: &contract.Contract{Service: contract.Service{Name: name, Version: "1.0.0"}}}
}

func TestService_Fleet_OCIRefs(t *testing.T) {
	store := fakeFleetStore{bundles: map[string]*contract.Bundle{"ghcr.io/x/a:1.0.0": bundleForFleet("a")}}
	svc := NewService(store, nil)
	snap, err := svc.Fleet(context.Background(), FleetOptions{OCIRefs: []string{"ghcr.io/x/a:1.0.0"}})
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	// The OCI ref's registry+org becomes the service domain, so it is keyed under
	// ghcr.io/x, not the default domain.
	if snap.Services[fleet.NewServiceKeyDomain("ghcr.io/x", "a")] == nil {
		t.Errorf("expected service a in domain ghcr.io/x from OCI source, got %+v", snap.Services)
	}
}

func TestService_Fleet_OCIRefs_NoStore(t *testing.T) {
	svc := NewService(nil, nil)
	snap, err := svc.Fleet(context.Background(), FleetOptions{OCIRefs: []string{"ghcr.io/x/a:1.0.0"}})
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	if !hasUnavailable(snap, "oci") {
		t.Errorf("expected SOURCE_UNAVAILABLE for oci, got %+v", snap.Limitations)
	}
}

func TestService_Fleet_Cache(t *testing.T) {
	dir := t.TempDir()
	writeCacheBundle(t, dir, "ghcr.io/org/svc/1.0.0/bundle.tar.gz")
	store := fakeFleetCacheStore{
		fakeFleetStore: fakeFleetStore{bundles: map[string]*contract.Bundle{"ghcr.io/org/svc:1.0.0": bundleForFleet("svc")}},
		dir:            dir,
	}
	svc := NewService(store, nil)
	snap, err := svc.Fleet(context.Background(), FleetOptions{IncludeCache: true})
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	if snap.Services[fleet.NewServiceKeyDomain("ghcr.io/org", "svc")] == nil {
		t.Errorf("expected service svc in domain ghcr.io/org from cache source, got %+v", snap.Services)
	}
}

func TestService_Fleet_Cache_NoStore(t *testing.T) {
	svc := NewService(nil, nil)
	snap, err := svc.Fleet(context.Background(), FleetOptions{IncludeCache: true})
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	if !hasUnavailable(snap, "cache") {
		t.Errorf("expected SOURCE_UNAVAILABLE for cache, got %+v", snap.Limitations)
	}
}

func TestService_Fleet_Cache_StoreWithoutCacheDir(t *testing.T) {
	// A store lacking CacheDir() resolves to "" (bundleStoreCacheDir fallback);
	// the cache source then finds nothing but does not fail.
	svc := NewService(fakeFleetStore{}, nil)
	snap, err := svc.Fleet(context.Background(), FleetOptions{IncludeCache: true})
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	if len(snap.Services) != 0 {
		t.Errorf("expected no services, got %+v", snap.Services)
	}
}

func hasUnavailable(snap *fleet.FleetSnapshot, source string) bool {
	for _, l := range snap.Limitations {
		if l.Code == fleet.LimitationSourceUnavailable && l.Source == source {
			return true
		}
	}
	return false
}

// writeCacheBundle writes a real cached bundle archive. The cache inventory
// reads every entry it discovers — a placeholder byte would make this fixture an
// unreadable entry rather than the baseline revision the test is about.
func writeCacheBundle(t *testing.T, dir, rel string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	y := []byte("pactoVersion: \"2.0\"\nservice:\n  name: svc\n  version: \"1.0.0\"\n")
	if err := tw.WriteHeader(&tar.Header{Name: "pacto.yaml", Size: int64(len(y)), Mode: 0o644}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(y); err != nil {
		t.Fatal(err)
	}
	for _, closeErr := range []error{tw.Close(), gw.Close(), f.Close()} {
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	}
}

// fakeFleetK8sClient is a k8sclient.K8sClient for the IncludeK8s wiring tests.
type fakeFleetK8sClient struct {
	disc     *k8sclient.CRDDiscovery
	listData []byte
	// identity is the cluster identity; an empty id reads as Forbidden, the
	// fallback the context-id tests rely on.
	identity   kubecluster.Identity
	gotContext string
	// workloads is the list JSON of each workload resource; missing: none.
	workloads map[string]string
	versions  map[string]string
	// down, when set, fails every read of the cluster but its identity.
	down error
}

// PreferredVersions serves the groups of versions; none by default.
func (f *fakeFleetK8sClient) PreferredVersions(context.Context, ...string) (map[string]string, error) {
	return f.versions, nil
}

func (f *fakeFleetK8sClient) DiscoverCRD(context.Context) (*k8sclient.CRDDiscovery, error) {
	return f.disc, f.down
}

// ListPage answers from workloads, else reads as a cluster with no workloads.
func (f *fakeFleetK8sClient) ListPage(_ context.Context, gvr schema.GroupVersionResource, _, _ string) ([]byte, string, error) {
	if f.down != nil {
		return nil, "", f.down
	}
	if l, ok := f.workloads[gvr.Resource]; ok {
		return []byte(l), "", nil
	}
	return []byte(`{"items":[]}`), "", nil
}

func (f *fakeFleetK8sClient) ListJSON(context.Context, k8sclient.CRDDiscovery, string) ([]byte, error) {
	return f.listData, nil
}
func (f *fakeFleetK8sClient) ClusterIdentity(_ context.Context, contextName string) (kubecluster.Identity, error) {
	f.gotContext = contextName
	if f.identity.ID == "" {
		return kubecluster.Identity{}, errors.New("reading the cluster id: forbidden")
	}
	return f.identity, nil
}

func TestService_Fleet_IncludeK8s(t *testing.T) {
	svc := NewService(nil, nil)
	svc.sys.newK8s = func(string) (k8sclient.K8sClient, error) {
		return &fakeFleetK8sClient{
			disc:     &k8sclient.CRDDiscovery{Found: true, ResourceName: "pactos"},
			listData: []byte(`{"items":[{"metadata":{"name":"svc-a","namespace":"prod"},"status":{"contractStatus":"Compliant","contract":{"serviceName":"svc-a"}}}]}`),
		}, nil
	}
	svc.sys.kubeContext = func() string { return "my-ctx" }

	snap, err := svc.Fleet(context.Background(), FleetOptions{LocalRoots: nil, IncludeK8s: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	q := fleet.NewQuery(snap)
	res, err := q.Search(fleet.SearchFilter{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Services) != 1 || res.Services[0].Name != "svc-a" {
		t.Fatalf("expected svc-a from the k8s source, got %+v", res.Services)
	}
}

func TestService_Fleet_IncludeK8s_ClientError(t *testing.T) {
	svc := NewService(nil, nil)
	svc.sys.newK8s = func(string) (k8sclient.K8sClient, error) { return nil, errors.New("no cluster") }
	svc.sys.kubeContext = func() string { return "" } // exercise the "k8s" id fallback

	snap, err := svc.Fleet(context.Background(), FleetOptions{IncludeK8s: true})
	if err != nil {
		t.Fatalf("Fleet returned a hard error, want partial: %v", err)
	}
	found := false
	for _, l := range snap.Limitations {
		if l.Code == fleet.LimitationSourceUnavailable && l.Source == "k8s" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected SOURCE_UNAVAILABLE for k8s, got %+v", snap.Limitations)
	}
}

// TestTraceFileSources covers the ad-hoc path-only convenience: no paths means no
// sources, and the positional ids stay exactly what the CLI has always produced.
// Identity here is a function of position by construction — which is precisely why
// declarative configuration carries explicit ids instead.
func TestTraceFileSources(t *testing.T) {
	if got := TraceFileSources(nil); got != nil {
		t.Errorf("TraceFileSources(nil) = %+v, want nil", got)
	}
	if got := TraceFileSources([]string{"a.json"}); len(got) != 1 || got[0].ID != "observation" {
		t.Errorf("single source = %+v", got)
	}
	got := TraceFileSources([]string{"a.json", "b.json"})
	want := []ObservationSourceSpec{
		{ID: "observation-1", Path: "a.json"},
		{ID: "observation-2", Path: "b.json"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("two sources = %+v, want %+v", got, want)
	}
}

func TestService_Fleet_DisallowPartial(t *testing.T) {
	svc := NewService(nil, nil)
	_, err := svc.Fleet(context.Background(), FleetOptions{
		TargetStateFiles: []string{filepath.Join(t.TempDir(), "missing.yaml")},
		DisallowPartial:  true,
	})
	if err == nil {
		t.Fatal("expected error with DisallowPartial and a missing target-state file")
	}
}

func TestFleetMiraSources(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"map":{"nodes":[{"key":"1","name":"payments"},{"key":"2","name":"orders"}],"edges":[{"from":"2","to":"1","calls":3}]},"stats":{"rows_scanned":6}}`))
	}))
	defer srv.Close()
	root := t.TempDir()
	writeLocalBundle(t, filepath.Join(root, "orders"), "orders")
	writeLocalBundle(t, filepath.Join(root, "payments"), "payments")

	svc := NewService(nil, nil)
	snap, err := svc.Fleet(context.Background(), FleetOptions{LocalRoots: []string{root}, MiraURLs: []string{srv.URL}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(snap.Sources, func(s fleet.SourceState) bool { return s.ID == "mira" && s.Kind == fleet.SourceKindMira }) {
		t.Fatalf("sources = %+v, want mira/mira", snap.Sources)
	}
	if !slices.ContainsFunc(snap.Relationships, func(r fleet.Relationship) bool {
		return r.Provenance == fleet.ProvenanceObserved && string(r.FromService) == "orders" && string(r.ToService) == "payments" &&
			len(r.ObservedSources) > 0 && r.ObservedSources[0].Source == "mira"
	}) {
		t.Fatalf("relationships = %+v, want observed orders->payments from mira", snap.Relationships)
	}

	snap, err = svc.Fleet(context.Background(), FleetOptions{MiraURLs: []string{srv.URL, srv.URL}})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"mira-1", "mira-2"} {
		if !slices.ContainsFunc(snap.Sources, func(s fleet.SourceState) bool { return s.ID == id }) {
			t.Fatalf("sources = %+v, want %s", snap.Sources, id)
		}
	}
}

func TestFleetRejectsBadMiraURL(t *testing.T) {
	snap, err := NewService(nil, nil).Fleet(context.Background(), FleetOptions{MiraURLs: []string{"https://user:pw@mira.example"}})
	if err == nil || snap != nil || strings.Contains(err.Error(), "pw") {
		t.Fatalf("snap=%v err=%v, want no snapshot and an error without the password", snap, err)
	}
}

func TestLiveK8sRegistersWorkloadSource(t *testing.T) {
	svc := k8sService("eu-prod-ctx")
	svc.sys.newK8s = func(string) (k8sclient.K8sClient, error) {
		return &fakeFleetK8sClient{
			disc:     &k8sclient.CRDDiscovery{Found: true, ResourceName: "pactos"},
			listData: []byte(`{"items":[]}`),
			identity: kubecluster.Identity{ID: clusterA, Name: "eu-prod", NameSource: kubecluster.NameFromContext},
		}, nil
	}
	snap, err := svc.Fleet(context.Background(), FleetOptions{IncludeK8s: true})
	if err != nil {
		t.Fatal(err)
	}
	cr, wl := sourceState(snap, clusterA), sourceState(snap, clusterA+"-workloads")
	if cr == nil || cr.Kind != fleet.SourceKindKubernetes || wl == nil || wl.Kind != fleet.SourceKindKubernetesWorkloads {
		t.Fatalf("sources = %+v; want the Pacto and the workload source", snap.Sources)
	}
	if wl.Status != fleet.SourceAvailable || wl.InferredCount != 0 {
		t.Errorf("workload source = %+v; want available with no inferred service", wl)
	}
	if len(snap.Clusters) != 1 || !slices.Equal(snap.Clusters[0].Sources, []string{clusterA, clusterA + "-workloads", clusterA + "-mesh"}) || snap.Clusters[0].ReportedBy != "" {
		t.Errorf("clusters = %+v; want one cluster every live source read", snap.Clusters)
	}
}

func TestLiveK8sSourcesIncludeMesh(t *testing.T) {
	srcs := liveK8sSources("uid-1", nil, &fakeFleetK8sClient{}, nil, nil)
	var ids []string
	for _, s := range srcs {
		ids = append(ids, s.ID())
	}
	if !slices.Equal(ids, []string{"uid-1", "uid-1" + workloadsSuffix, "uid-1" + meshSuffix}) || srcs[2].Kind() != fleet.SourceKindMesh {
		t.Errorf("live sources %v; want the Pacto, workload and mesh sources", ids)
	}
}

func TestUnreachableClusterMarksBothSourcesUnavailable(t *testing.T) {
	svc := NewService(nil, nil)
	svc.sys.newK8s = func(string) (k8sclient.K8sClient, error) { return nil, errors.New("no cluster") }
	svc.sys.kubeContext = func() string { return "" }
	snap, err := svc.Fleet(context.Background(), FleetOptions{IncludeK8s: true})
	if err != nil {
		t.Fatal(err)
	}
	for id, kind := range map[string]string{"k8s": fleet.SourceKindKubernetes, "k8s-workloads": fleet.SourceKindKubernetesWorkloads} {
		if st := sourceState(snap, id); st == nil || st.Kind != kind || st.Status != fleet.SourceUnavailable {
			t.Errorf("source %s = %+v, want unavailable %s", id, st, kind)
		}
	}
}

func TestWorkloadOfDeclaredTargetNotInferred(t *testing.T) {
	svc := k8sService("")
	svc.sys.newK8s = func(string) (k8sclient.K8sClient, error) {
		return &fakeFleetK8sClient{
			disc:     &k8sclient.CRDDiscovery{Found: true, ResourceName: "pactos"},
			listData: []byte(`{"items":[{"metadata":{"name":"cart","namespace":"shop"},"spec":{"target":{"serviceName":"cart"}},"status":{"contractStatus":"Compliant","contract":{"serviceName":"cart"}}}]}`),
			workloads: map[string]string{"deployments": `{"items":[
				{"metadata":{"name":"cart","namespace":"shop"},"spec":{"template":{"spec":{"containers":[{"name":"cart","image":"cart:1"}]}}}},
				{"metadata":{"name":"nightly","namespace":"shop"},"spec":{"template":{"spec":{"containers":[{"name":"nightly","image":"nightly:1"}]}}}}]}`},
		}, nil
	}
	snap, err := svc.Fleet(context.Background(), FleetOptions{IncludeK8s: true})
	if err != nil {
		t.Fatal(err)
	}
	var inferred []string
	for _, s := range snap.Services {
		if s.Inferred() {
			inferred = append(inferred, s.Name)
		}
	}
	if !slices.Equal(inferred, []string{"k8s:shop/Deployment/nightly"}) {
		t.Errorf("inferred = %v, want only the workload no Pacto targets", inferred)
	}
}

func TestFleetK8sProviderRows(t *testing.T) {
	svc := NewService(nil, nil)
	svc.sys.newK8s = func(string) (k8sclient.K8sClient, error) {
		return &fakeFleetK8sClient{
			disc:      &k8sclient.CRDDiscovery{Found: true, Version: "v1alpha1", ResourceName: "pactos"},
			listData:  []byte(`{"items":[]}`),
			identity:  kubecluster.Identity{ID: "uid-1", Name: "eu-prod", NameSource: kubecluster.NameFromContext},
			workloads: map[string]string{"deployments": `{"items":[{"metadata":{"name":"cart","namespace":"shop"}}]}`},
		}, nil
	}
	svc.sys.kubeContext = func() string { return "eu-prod" }
	snap, err := svc.Fleet(context.Background(), FleetOptions{IncludeK8s: true})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{fleet.ProviderPactoCRD: "uid-1", fleet.ProviderKubernetesWorkloads: "uid-1" + workloadsSuffix}
	if len(snap.Providers) != len(want) {
		t.Fatalf("providers = %+v", snap.Providers)
	}
	for _, p := range snap.Providers {
		if want[p.ID] != p.Source || p.Cluster != "eu-prod" {
			t.Errorf("row %s from %q in cluster %q; want source %q in eu-prod", p.ID, p.Source, p.Cluster, want[p.ID])
		}
	}
}

func TestFleetK8sGroupingRows(t *testing.T) {
	svc := NewService(nil, nil)
	svc.sys.newK8s = func(string) (k8sclient.K8sClient, error) {
		return &fakeFleetK8sClient{
			disc:     &k8sclient.CRDDiscovery{Found: true, Version: "v1alpha1", ResourceName: "pactos"},
			listData: []byte(`{"items":[]}`),
			identity: kubecluster.Identity{ID: "uid-1", Name: "eu-prod", NameSource: kubecluster.NameFromContext},
			versions: map[string]string{"argoproj.io": "v1alpha1", "helm.toolkit.fluxcd.io": "v2"},
			workloads: map[string]string{"deployments": `{"items":[{"metadata":{"name":"cart","namespace":"shop",` +
				`"annotations":{"meta.helm.sh/release-name":"cart","meta.helm.sh/release-namespace":"shop"}}}]}`},
		}, nil
	}
	svc.sys.kubeContext = func() string { return "eu-prod" }
	snap, err := svc.Fleet(context.Background(), FleetOptions{IncludeK8s: true})
	if err != nil {
		t.Fatal(err)
	}
	workloads := "uid-1" + workloadsSuffix
	want := map[string]string{fleet.ProviderPactoCRD: "uid-1", fleet.ProviderKubernetesWorkloads: workloads,
		fleet.ProviderHelm: workloads, fleet.ProviderArgoCD: workloads, fleet.ProviderFlux: workloads}
	if len(snap.Providers) != len(want) {
		t.Fatalf("providers = %+v", snap.Providers)
	}
	for _, p := range snap.Providers {
		if want[p.ID] != p.Source || p.Cluster != "eu-prod" {
			t.Errorf("row %s from %q in cluster %q; want source %q in eu-prod", p.ID, p.Source, p.Cluster, want[p.ID])
		}
	}
}

func TestConsumerSourcesOrder(t *testing.T) {
	all := FleetOptions{
		LocalRoots:         []string{"."},
		TargetStateFiles:   []string{"t.yaml"},
		EvidenceURLs:       []string{"https://u:p@e.example/?token=x"},
		MiraURLs:           []string{"https://m.example"},
		ObservationSources: TraceFileSources([]string{"traces.json"}),
		CatalogRoots:       []string{"oci://r.example/acme/web:1.0.0"},
		OCIRefs:            []string{"r.example/acme/api:1.0.0"},
		IncludeCache:       true,
		IncludeK8s:         true,
		K8sNamespaces:      []string{"a", "b"},
	}
	want := []string{
		"--root oci://r.example/acme/web:1.0.0", "--oci r.example/acme/api:1.0.0", "--cache", "--k8s (namespace a,b)",
		"--evidence-url https://e.example/ (redacted)", "--mira-source https://m.example", "--target-state t.yaml", "--traces traces.json",
	}
	if got := all.ConsumerSources(); !slices.Equal(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if got := (FleetOptions{IncludeK8s: true}).ConsumerSources(); !slices.Equal(got, []string{"--k8s (all namespaces)"}) {
		t.Fatalf("k8s = %q", got)
	}
	if got := (FleetOptions{LocalRoots: []string{"."}}).ConsumerSources(); len(got) != 0 {
		t.Fatalf("local only = %q", got)
	}
}
