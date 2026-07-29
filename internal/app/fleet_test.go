package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/trianalab/pacto/v3/internal/k8sclient"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/evidence"
	"github.com/trianalab/pacto/v3/pkg/evidenceenvelope"
	"github.com/trianalab/pacto/v3/pkg/evidencestore"
	"github.com/trianalab/pacto/v3/pkg/fleet"
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

func TestService_Fleet_EvidenceStores(t *testing.T) {
	fixed := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	storeDir := t.TempDir()
	// Seed a durable evidence store the same way the ingestion host would, so the
	// fleet durable source reads it back through Recover + ListLatest.
	store, err := openEvidenceStore(context.Background(), "file://"+storeDir, DefaultEvidencePrefix)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	ar := evidencestore.AcceptedRecord{
		Envelope: evidenceenvelope.Envelope{
			ID:       "e1",
			Producer: evidenceenvelope.Producer{ID: "prod-eu"},
			Sequence: 1,
			EvidenceSet: evidence.EvidenceSet{
				Subject:     evidence.SubjectRef{Kind: "service", Name: "svc-a"},
				ContractRef: "oci://ghcr.io/acme/svc:1.0.0",
				ObservedAt:  fixed,
			},
		},
		Compliance:  fleet.StatusCompliant,
		ContractRef: "oci://ghcr.io/acme/svc:1.0.0",
		TargetKey:   string(fleet.NewTargetKey("prod-eu", "external", "svc-a")),
		AcceptedAt:  fixed,
	}
	if err := store.Commit(context.Background(), ar); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	svc := NewService(nil, nil)
	snap, err := svc.Fleet(context.Background(), FleetOptions{
		EvidenceStores: []string{storeDir},
		Now:            func() time.Time { return fixed },
	})
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	if len(snap.Targets) != 1 {
		t.Fatalf("got %d targets, want 1", len(snap.Targets))
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
	if !ids["evidence-store"] {
		t.Errorf("source ids = %v, want evidence-store", ids)
	}
}

func TestService_Fleet_EvidenceStore_OpenError(t *testing.T) {
	// A regular file cannot host a store directory beneath it, so the durable
	// source's lazy open fails in Collect and the store becomes a failing source
	// (a limitation), not a build abort.
	dir := t.TempDir()
	file := filepath.Join(dir, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := NewService(nil, nil)
	snap, err := svc.Fleet(context.Background(), FleetOptions{
		EvidenceStores: []string{filepath.Join(file, "store")},
	})
	if err != nil {
		t.Fatalf("Fleet returned a hard error, want partial snapshot: %v", err)
	}
	found := false
	for _, l := range snap.Limitations {
		if l.Code == fleet.LimitationSourceUnavailable && l.Source == "evidence-store" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a SOURCE_UNAVAILABLE limitation for evidence-store, got %+v", snap.Limitations)
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
	if snap.Service("a") == nil {
		t.Errorf("expected service a from OCI source, got %+v", snap.Services)
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
	if snap.Service("svc") == nil {
		t.Errorf("expected service svc from cache source, got %+v", snap.Services)
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

func writeCacheBundle(t *testing.T, dir, rel string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeFleetK8sClient is a k8sclient.K8sClient for the IncludeK8s wiring tests.
type fakeFleetK8sClient struct {
	disc     *k8sclient.CRDDiscovery
	listData []byte
}

func (f *fakeFleetK8sClient) Probe(context.Context) error { return nil }
func (f *fakeFleetK8sClient) DiscoverCRD(context.Context) (*k8sclient.CRDDiscovery, error) {
	return f.disc, nil
}
func (f *fakeFleetK8sClient) ListJSON(context.Context, string, string) ([]byte, error) {
	return f.listData, nil
}
func (f *fakeFleetK8sClient) GetJSON(context.Context, string, string, string) ([]byte, error) {
	return nil, nil
}
func (f *fakeFleetK8sClient) CountResources(context.Context, string, string) (int, error) {
	return 0, nil
}

func TestService_Fleet_IncludeK8s(t *testing.T) {
	origClient, origCtx := newK8sClient, currentKubeContext
	t.Cleanup(func() { newK8sClient, currentKubeContext = origClient, origCtx })
	newK8sClient = func() (k8sclient.K8sClient, error) {
		return &fakeFleetK8sClient{
			disc:     &k8sclient.CRDDiscovery{Found: true, ResourceName: "pactos"},
			listData: []byte(`{"items":[{"metadata":{"name":"svc-a","namespace":"prod"},"status":{"contractStatus":"Compliant","contract":{"serviceName":"svc-a"}}}]}`),
		}, nil
	}
	currentKubeContext = func() string { return "my-ctx" }

	svc := NewService(nil, nil)
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
	origClient, origCtx := newK8sClient, currentKubeContext
	t.Cleanup(func() { newK8sClient, currentKubeContext = origClient, origCtx })
	newK8sClient = func() (k8sclient.K8sClient, error) { return nil, errors.New("no cluster") }
	currentKubeContext = func() string { return "" } // exercise the "k8s" id fallback

	svc := NewService(nil, nil)
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
