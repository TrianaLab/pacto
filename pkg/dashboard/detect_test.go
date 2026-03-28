package dashboard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitNonEmpty_Empty(t *testing.T) {
	result := splitNonEmpty("")
	if len(result) != 0 {
		t.Errorf("expected 0 lines, got %d", len(result))
	}
}

func TestSplitNonEmpty_SingleLine(t *testing.T) {
	result := splitNonEmpty("hello")
	if len(result) != 1 {
		t.Fatalf("expected 1 line, got %d", len(result))
	}
	if result[0] != "hello" {
		t.Errorf("expected 'hello', got %q", result[0])
	}
}

func TestSplitNonEmpty_MultipleLinesWithBlanks(t *testing.T) {
	input := "line1\n\n  line2  \n\n\nline3\n"
	result := splitNonEmpty(input)
	if len(result) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(result), result)
	}
	if result[0] != "line1" {
		t.Errorf("expected 'line1', got %q", result[0])
	}
	if result[1] != "line2" {
		t.Errorf("expected 'line2', got %q", result[1])
	}
	if result[2] != "line3" {
		t.Errorf("expected 'line3', got %q", result[2])
	}
}

func TestSplitNonEmpty_WhitespaceOnly(t *testing.T) {
	result := splitNonEmpty("  \n  \n  ")
	if len(result) != 0 {
		t.Errorf("expected 0 lines, got %d", len(result))
	}
}

func TestDetectResult_ActiveSources_Empty(t *testing.T) {
	r := &DetectResult{}
	sources := r.ActiveSources()
	if len(sources) != 0 {
		t.Errorf("expected 0 active sources, got %d", len(sources))
	}
}

func TestDetectResult_ActiveSources_LocalOnly(t *testing.T) {
	r := &DetectResult{
		Local: NewLocalSource("."),
	}
	sources := r.ActiveSources()
	if len(sources) != 1 {
		t.Fatalf("expected 1 active source, got %d", len(sources))
	}
	if _, ok := sources["local"]; !ok {
		t.Error("expected 'local' in active sources")
	}
}

func TestDetectResult_ActiveSources_LocalAndCache(t *testing.T) {
	root := t.TempDir()
	cache := NewCacheSource(root)
	r := &DetectResult{
		Local: NewLocalSource("."),
		Cache: cache,
	}
	sources := r.ActiveSources()
	// Cache without OCI is exposed as "oci" (offline access to previously pulled bundles).
	if len(sources) != 2 {
		t.Fatalf("expected 2 active sources, got %d", len(sources))
	}
	if _, ok := sources["local"]; !ok {
		t.Error("expected 'local' in active sources")
	}
	if _, ok := sources["oci"]; !ok {
		t.Error("expected 'oci' in active sources (cache exposed as oci)")
	}
}

func TestDetectResult_ActiveSources_AllTypes(t *testing.T) {
	root := t.TempDir()
	cache := NewCacheSource(root)
	client := &mockK8sClient{}
	r := &DetectResult{
		Local: NewLocalSource("."),
		Cache: cache,
		K8s:   NewK8sSource(client, "default", "pactos"),
		// OCI is nil — cache is exposed as "oci"
	}
	sources := r.ActiveSources()
	// local + k8s + oci (from cache)
	if len(sources) != 3 {
		t.Fatalf("expected 3 active sources, got %d", len(sources))
	}
	if _, ok := sources["oci"]; !ok {
		t.Error("expected 'oci' in active sources (cache exposed as oci)")
	}
}

func TestDetectLocal_RootPactoYAML(t *testing.T) {
	root := t.TempDir()
	writeLocalPactoYAML(t, root, "root-svc", "1.0.0")

	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectLocal(root)

	if result.Local == nil {
		t.Fatal("expected local source to be detected")
	}
	if len(result.Sources) != 1 {
		t.Fatalf("expected 1 source info, got %d", len(result.Sources))
	}
	if !result.Sources[0].Enabled {
		t.Error("expected source to be enabled")
	}
	if !result.Diagnostics.Local.PactoYamlFound {
		t.Error("expected PactoYamlFound=true")
	}
}

func TestDetectLocal_SubdirPactoYAML(t *testing.T) {
	root := t.TempDir()
	writeLocalPactoYAML(t, filepath.Join(root, "api"), "api", "1.0.0")

	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectLocal(root)

	if result.Local == nil {
		t.Fatal("expected local source to be detected from subdir")
	}
	if !result.Sources[0].Enabled {
		t.Error("expected source to be enabled")
	}
}

func TestDetectLocal_NoPactoYAML(t *testing.T) {
	root := t.TempDir()

	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectLocal(root)

	if result.Local != nil {
		t.Error("expected no local source detected")
	}
	if len(result.Sources) != 1 {
		t.Fatalf("expected 1 source info, got %d", len(result.Sources))
	}
	if result.Sources[0].Enabled {
		t.Error("expected source to be disabled")
	}
}

func TestDetectLocal_EmptyDir(t *testing.T) {
	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectLocal("")

	// When dir is empty it defaults to "." — shouldn't crash
	if len(result.Sources) != 1 {
		t.Fatalf("expected 1 source info, got %d", len(result.Sources))
	}
}

func TestDetectOCI_NilStore(t *testing.T) {
	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectOCI(nil, nil)

	if result.OCI != nil {
		t.Error("expected nil OCI source without store")
	}
	if len(result.Sources) != 1 {
		t.Fatalf("expected 1 source info, got %d", len(result.Sources))
	}
	if result.Sources[0].Enabled {
		t.Error("expected OCI disabled without store")
	}
}

func TestDetectOCI_StoreNoRepos(t *testing.T) {
	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectOCI(newMockBundleStore(), nil)

	if result.OCI != nil {
		t.Error("expected nil OCI source without repos")
	}
	if !result.Diagnostics.OCI.StoreConfigured {
		t.Error("expected StoreConfigured=true")
	}
	if result.Sources[0].Enabled {
		t.Error("expected OCI disabled without repos")
	}
}

func TestDetectOCI_StoreWithRepos(t *testing.T) {
	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectOCI(newMockBundleStore(), []string{"ghcr.io/org/svc"})

	if result.OCI == nil {
		t.Fatal("expected OCI source to be detected")
	}
	if !result.Sources[0].Enabled {
		t.Error("expected OCI enabled")
	}
	if len(result.Diagnostics.OCI.Repos) != 1 {
		t.Errorf("expected 1 repo, got %d", len(result.Diagnostics.OCI.Repos))
	}
}

func TestDetectOCI_StripsOCIPrefix(t *testing.T) {
	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectOCI(newMockBundleStore(), []string{"oci://ghcr.io/org/svc", "ghcr.io/org/other"})

	if result.OCI == nil {
		t.Fatal("expected OCI source to be detected")
	}
	// Verify the oci:// prefix was stripped.
	if result.Diagnostics.OCI.Repos[0] != "ghcr.io/org/svc" {
		t.Errorf("expected stripped repo, got %q", result.Diagnostics.OCI.Repos[0])
	}
	// Verify repos without prefix are unchanged.
	if result.Diagnostics.OCI.Repos[1] != "ghcr.io/org/other" {
		t.Errorf("expected unchanged repo, got %q", result.Diagnostics.OCI.Repos[1])
	}
}

func TestDetectCache_EmptyCacheDir(t *testing.T) {
	root := t.TempDir()

	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectCache(root)

	// Empty dir = no bundles
	if result.Cache != nil {
		t.Error("expected nil cache source for empty dir")
	}
	// Cache no longer adds to Sources (internal implementation detail).
	if len(result.Sources) != 0 {
		t.Fatalf("expected 0 source info, got %d", len(result.Sources))
	}
}

func TestDetectCache_NonExistentDir(t *testing.T) {
	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectCache("/nonexistent/path/to/cache")

	if result.Cache != nil {
		t.Error("expected nil cache source for nonexistent dir")
	}
}

func TestDetectCache_WithBundles(t *testing.T) {
	root := t.TempDir()
	writeBundleTarGzFile(t,
		filepath.Join(root, "ghcr.io/org/api/1.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: api
  version: 1.0.0
`)

	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectCache(root)

	if result.Cache == nil {
		t.Fatal("expected cache source to be detected")
	}
	if result.Diagnostics.Cache.ServiceCount != 1 {
		t.Errorf("expected 1 service, got %d", result.Diagnostics.Cache.ServiceCount)
	}
}

func TestDetectSources_WithLocalAndNoCache(t *testing.T) {
	root := t.TempDir()
	writeLocalPactoYAML(t, root, "svc", "1.0.0")

	// Override newK8sClientFunc to prevent real cluster access.
	origClient := newK8sClientFunc
	newK8sClientFunc = func() (K8sClient, error) {
		return nil, fmt.Errorf("no kubeconfig")
	}
	t.Cleanup(func() { newK8sClientFunc = origClient })

	result := DetectSources(context.Background(), DetectOptions{
		Dir:     root,
		Store:   nil,
		NoCache: true,
	})

	if result.Local == nil {
		t.Fatal("expected local source to be detected")
	}
	if result.OCI != nil {
		t.Error("expected nil OCI source without store")
	}
	if result.Cache != nil {
		t.Error("expected nil cache source with NoCache=true")
	}

	// Cache is internal — no SourceInfo entry when disabled.
	for i := range result.Sources {
		if result.Sources[i].Type == "cache" {
			t.Error("cache should not appear in Sources list")
		}
	}
}

func TestDetectSources_WithCacheEnabled(t *testing.T) {
	root := t.TempDir()

	// Override newK8sClientFunc to prevent real cluster access.
	origClient := newK8sClientFunc
	newK8sClientFunc = func() (K8sClient, error) {
		return nil, fmt.Errorf("no kubeconfig")
	}
	t.Cleanup(func() { newK8sClientFunc = origClient })

	result := DetectSources(context.Background(), DetectOptions{
		Dir:      root,
		Store:    nil,
		NoCache:  false,
		CacheDir: "/nonexistent/cache/dir",
	})

	// Cache should not be detected (nonexistent dir).
	if result.Cache != nil {
		t.Error("expected nil cache source for nonexistent dir")
	}
}

func TestDetectResult_AllSources_CacheNeverExposedWithOCI(t *testing.T) {
	cacheDir := t.TempDir()
	writeBundleTarGzFile(t,
		filepath.Join(cacheDir, "ghcr.io/org/svc/1.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: svc
  version: 1.0.0
`)
	r := &DetectResult{
		OCI:   NewOCISource(newMockBundleStore(), []string{"ghcr.io/org/svc"}),
		Cache: NewCacheSource(cacheDir),
	}
	all := r.AllSources()
	if _, ok := all["oci"]; !ok {
		t.Error("expected 'oci' in AllSources")
	}
	if _, ok := all["cache"]; ok {
		t.Error("cache must NOT appear as a separate public source — it is internal to OCI")
	}
}

func TestDetectResult_AllSources_CacheOnlyExposedAsOCI(t *testing.T) {
	cacheDir := t.TempDir()
	writeBundleTarGzFile(t,
		filepath.Join(cacheDir, "ghcr.io/org/svc/1.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: svc
  version: 1.0.0
`)
	r := &DetectResult{
		Cache: NewCacheSource(cacheDir),
	}
	all := r.AllSources()
	if _, ok := all["oci"]; !ok {
		t.Error("expected cache exposed as 'oci' when no live OCI")
	}
	if _, ok := all["cache"]; ok {
		t.Error("cache should not appear separately when no live OCI")
	}
}

func TestDetectResult_ActiveSources_WithOCI(t *testing.T) {
	r := &DetectResult{
		OCI: NewOCISource(newMockBundleStore(), []string{"ghcr.io/org/svc"}),
	}
	sources := r.ActiveSources()
	if len(sources) != 1 {
		t.Fatalf("expected 1 active source, got %d", len(sources))
	}
	if _, ok := sources["oci"]; !ok {
		t.Error("expected 'oci' in active sources")
	}
}

func TestDetectLocal_UnreadableDir(t *testing.T) {
	root := t.TempDir()

	// Make root unreadable so ReadDir fails (no pacto.yaml at root level).
	if err := os.Chmod(root, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectLocal(root)

	if result.Local != nil {
		t.Error("expected nil local source for unreadable dir")
	}
	if len(result.Sources) != 1 {
		t.Fatalf("expected 1 source info, got %d", len(result.Sources))
	}
	if result.Sources[0].Enabled {
		t.Error("expected source to be disabled")
	}
	if result.Diagnostics.Local.Error == "" {
		t.Error("expected error in diagnostics")
	}
}

func TestDetectCache_DefaultDir(t *testing.T) {
	// Set XDG_CACHE_HOME to a temp dir so we control where it looks.
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", root)

	// Create the expected cache dir with a bundle.
	cacheDir := filepath.Join(root, "pacto", "oci")
	writeBundleTarGzFile(t,
		filepath.Join(cacheDir, "ghcr.io/org/api/1.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: api
  version: 1.0.0
`)

	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectCache("") // empty = use default

	if result.Cache == nil {
		t.Fatal("expected cache source to be detected from default dir")
	}
	if result.Diagnostics.Cache.CacheDir != cacheDir {
		t.Errorf("expected cache dir %q, got %q", cacheDir, result.Diagnostics.Cache.CacheDir)
	}
}

// ---------------------------------------------------------------------------
// K8s detection tests using mock K8sClient
// ---------------------------------------------------------------------------

// setupMockK8sClient overrides the newK8sClientFunc for the duration of the test.
func setupMockK8sClient(t *testing.T, client K8sClient) {
	t.Helper()
	orig := newK8sClientFunc
	newK8sClientFunc = func() (K8sClient, error) { return client, nil }
	t.Cleanup(func() { newK8sClientFunc = orig })
}

// setupMockK8sClientError makes client creation fail.
func setupMockK8sClientError(t *testing.T, err error) {
	t.Helper()
	orig := newK8sClientFunc
	newK8sClientFunc = func() (K8sClient, error) { return nil, err }
	t.Cleanup(func() { newK8sClientFunc = orig })
}

func TestDetectK8s_ClientNotAvailable(t *testing.T) {
	setupMockK8sClientError(t, fmt.Errorf("no kubeconfig found"))

	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectK8s(context.Background(), "")

	if result.K8s != nil {
		t.Error("expected nil K8s source when client not available")
	}
	if len(result.Sources) != 1 {
		t.Fatalf("expected 1 source info, got %d", len(result.Sources))
	}
	if result.Sources[0].Enabled {
		t.Error("expected source to be disabled")
	}
	if !strings.Contains(result.Sources[0].Reason, "Kubernetes client not available") {
		t.Errorf("expected reason about client not available, got %q", result.Sources[0].Reason)
	}
}

func TestDetectK8s_ClusterNotReachable(t *testing.T) {
	client := &mockK8sClient{probeErr: fmt.Errorf("connection refused")}
	setupMockK8sClient(t, client)

	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectK8s(context.Background(), "")

	if result.K8s != nil {
		t.Error("expected nil K8s source when cluster unreachable")
	}
	if !result.Diagnostics.K8s.ClientConfigured {
		t.Error("expected ClientConfigured=true")
	}
	if result.Diagnostics.K8s.ClusterReachable {
		t.Error("expected ClusterReachable=false")
	}
	if result.Diagnostics.K8s.Error == "" {
		t.Error("expected error in diagnostics")
	}
}

func TestDetectK8s_FullSuccess(t *testing.T) {
	client := &mockK8sClient{
		crdDiscovery: &CRDDiscovery{
			Found:        true,
			Group:        "pacto.trianalab.io",
			Versions:     []string{"v1alpha1"},
			Version:      "v1alpha1",
			ResourceName: "pactos",
		},
		countResult: 2,
	}
	setupMockK8sClient(t, client)

	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectK8s(context.Background(), "default")

	if result.K8s == nil {
		t.Fatal("expected K8s source to be detected")
	}
	if !result.Diagnostics.K8s.ClientConfigured {
		t.Error("expected ClientConfigured=true")
	}
	if !result.Diagnostics.K8s.ClusterReachable {
		t.Error("expected ClusterReachable=true")
	}
	if !result.Diagnostics.K8s.CRDExists {
		t.Error("expected CRDExists=true")
	}
	if result.Diagnostics.K8s.ResourceName != "pactos" {
		t.Errorf("expected resource name 'pactos', got %q", result.Diagnostics.K8s.ResourceName)
	}
	if result.Diagnostics.K8s.ChosenVersion != "v1alpha1" {
		t.Errorf("expected chosen version 'v1alpha1', got %q", result.Diagnostics.K8s.ChosenVersion)
	}
	if result.Diagnostics.K8s.ResourceCount != 2 {
		t.Errorf("expected resource count 2, got %d", result.Diagnostics.K8s.ResourceCount)
	}
}

func TestDetectK8s_NoCRD(t *testing.T) {
	client := &mockK8sClient{
		crdDiscovery: &CRDDiscovery{Found: false, Group: "pacto.trianalab.io"},
	}
	setupMockK8sClient(t, client)

	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectK8s(context.Background(), "")

	if result.K8s == nil {
		t.Fatal("expected K8s source even without CRD")
	}
	if result.Diagnostics.K8s.CRDExists {
		t.Error("expected CRDExists=false")
	}
	if !strings.Contains(result.Sources[0].Reason, "CRD not detected") {
		t.Errorf("expected reason about CRD not detected, got %q", result.Sources[0].Reason)
	}
}

func TestDetectK8s_AllNamespaces(t *testing.T) {
	client := &mockK8sClient{
		crdDiscovery: &CRDDiscovery{Found: false, Group: "pacto.trianalab.io"},
	}
	setupMockK8sClient(t, client)

	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectK8s(context.Background(), "") // empty namespace = all namespaces

	if !result.Diagnostics.K8s.AllNamespaces {
		t.Error("expected AllNamespaces=true for empty namespace")
	}
}

func TestDetectK8s_KubeconfigEnv(t *testing.T) {
	t.Setenv("KUBECONFIG", "/custom/kubeconfig")
	// Make client creation fail so we just test kubeconfig detection.
	setupMockK8sClientError(t, fmt.Errorf("invalid kubeconfig"))

	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectK8s(context.Background(), "")

	if result.Diagnostics.K8s.KubeconfigPath != "/custom/kubeconfig" {
		t.Errorf("expected kubeconfig path '/custom/kubeconfig', got %q", result.Diagnostics.K8s.KubeconfigPath)
	}
}

func TestDetectK8s_DefaultKubeconfig(t *testing.T) {
	t.Setenv("KUBECONFIG", "")
	// Create a fake ~/.kube/config
	home := t.TempDir()
	orig := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = orig })

	kubeDir := filepath.Join(home, ".kube")
	_ = os.MkdirAll(kubeDir, 0o755)
	_ = os.WriteFile(filepath.Join(kubeDir, "config"), []byte("test"), 0o644)

	// Make client creation fail so we just test kubeconfig detection.
	setupMockK8sClientError(t, fmt.Errorf("invalid kubeconfig"))

	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectK8s(context.Background(), "")

	expected := filepath.Join(home, ".kube", "config")
	if result.Diagnostics.K8s.KubeconfigPath != expected {
		t.Errorf("expected kubeconfig path %q, got %q", expected, result.Diagnostics.K8s.KubeconfigPath)
	}
}

func TestDetectCache_HomeDirError(t *testing.T) {
	orig := userHomeDir
	userHomeDir = func() (string, error) {
		return "", fmt.Errorf("no home directory")
	}
	t.Cleanup(func() { userHomeDir = orig })

	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectCache("")

	if result.Cache != nil {
		t.Error("expected nil cache when home dir fails")
	}
	if result.Diagnostics.Cache.Error == "" {
		t.Error("expected error in diagnostics")
	}
}

func TestDetectK8s_DiscoverCRDError(t *testing.T) {
	client := &mockK8sClient{
		crdErr: fmt.Errorf("discovery failed"),
	}
	setupMockK8sClient(t, client)

	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectK8s(context.Background(), "")

	// Even with CRD discovery error, K8s source should be created (cluster is reachable).
	if result.K8s == nil {
		t.Fatal("expected K8s source even with CRD discovery error")
	}
	if result.Diagnostics.K8s.Error == "" {
		t.Error("expected error in diagnostics from CRD discovery failure")
	}
}

func TestDetectCache_DefaultDirNoXDG(t *testing.T) {
	// Ensure XDG is not set, so it uses ~/.cache/pacto/oci.
	t.Setenv("XDG_CACHE_HOME", "")

	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectCache("")

	// The default dir likely doesn't exist in test env -- that's fine.
	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".cache", "pacto", "oci")
	if result.Diagnostics.Cache.CacheDir != expected {
		t.Errorf("expected cache dir %q, got %q", expected, result.Diagnostics.Cache.CacheDir)
	}
}

// ---------------------------------------------------------------------------
// EnrichFromK8s tests
// ---------------------------------------------------------------------------

func TestEnrichFromK8s_NoK8sSource(t *testing.T) {
	r := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	r.EnrichFromK8s(context.Background(), newMockBundleStore(), "")
	if r.OCI != nil {
		t.Error("expected nil OCI without K8s source")
	}
}

func TestEnrichFromK8s_AlreadyHasOCI(t *testing.T) {
	r := &DetectResult{
		Diagnostics: &SourceDiagnostics{},
		K8s:         NewK8sSource(&mockK8sClient{}, "default", "pactos"),
		OCI:         NewOCISource(newMockBundleStore(), []string{"ghcr.io/org/svc"}),
	}
	r.EnrichFromK8s(context.Background(), newMockBundleStore(), "")
	// OCI should still be the original one, not overwritten.
	if len(r.OCI.repos) != 1 || r.OCI.repos[0] != "ghcr.io/org/svc" {
		t.Error("OCI repos should not change when OCI source already exists")
	}
}

func TestEnrichFromK8s_NilStore(t *testing.T) {
	r := &DetectResult{
		Diagnostics: &SourceDiagnostics{},
		K8s:         NewK8sSource(&mockK8sClient{}, "default", "pactos"),
	}
	r.EnrichFromK8s(context.Background(), nil, "")
	if r.OCI != nil {
		t.Error("expected nil OCI without store")
	}
}

func TestEnrichFromK8s_DiscoverRepos(t *testing.T) {
	// K8s source with services that have imageRefs.
	k8sData := `{"items": [
		{"metadata": {"name": "order-svc", "namespace": "default"},
		 "status": {"contractStatus": "Compliant", "contract": {"serviceName": "order-service", "version": "1.0.0", "imageRef": "ghcr.io/org/order-pacto:1.0.0"}}},
		{"metadata": {"name": "pay-svc", "namespace": "default"},
		 "status": {"contractStatus": "Compliant", "contract": {"serviceName": "payment-service", "version": "2.0.0", "imageRef": "ghcr.io/org/payment-pacto:2.0.0"}}},
		{"metadata": {"name": "no-image", "namespace": "default"},
		 "status": {"contractStatus": "Compliant", "contract": {"serviceName": "no-image-svc", "version": "1.0.0"}}}
	]}`

	client := &mockK8sClient{listJSON: []byte(k8sData)}
	r := &DetectResult{
		Diagnostics: &SourceDiagnostics{},
		K8s:         NewK8sSource(client, "", "pactos"),
	}

	store := newMockBundleStore()
	cacheDir := t.TempDir()
	r.EnrichFromK8s(context.Background(), store, cacheDir)

	if r.OCI == nil {
		t.Fatal("expected OCI source to be created from K8s imageRefs")
	}
	// Should have discovered 2 repos (the service without imageRef is skipped).
	if len(r.OCI.repos) != 2 {
		t.Errorf("expected 2 repos, got %d: %v", len(r.OCI.repos), r.OCI.repos)
	}
	// Cache source should have been created.
	if r.Cache == nil {
		t.Error("expected CacheSource to be created")
	}
}

func TestEnrichFromK8s_DeduplicatesRepos(t *testing.T) {
	k8sData := `{"items": [
		{"metadata": {"name": "svc-a", "namespace": "default"},
		 "status": {"contract": {"serviceName": "svc-a", "imageRef": "ghcr.io/org/svc-pacto:1.0.0"}}},
		{"metadata": {"name": "svc-b", "namespace": "default"},
		 "status": {"contract": {"serviceName": "svc-b", "imageRef": "ghcr.io/org/svc-pacto:2.0.0"}}}
	]}`

	client := &mockK8sClient{listJSON: []byte(k8sData)}
	r := &DetectResult{
		Diagnostics: &SourceDiagnostics{},
		K8s:         NewK8sSource(client, "", "pactos"),
	}

	r.EnrichFromK8s(context.Background(), newMockBundleStore(), t.TempDir())

	if r.OCI == nil {
		t.Fatal("expected OCI source")
	}
	// Same repo, different tags — should be deduplicated.
	if len(r.OCI.repos) != 1 {
		t.Errorf("expected 1 deduplicated repo, got %d: %v", len(r.OCI.repos), r.OCI.repos)
	}
}

func TestEnrichFromK8s_K8sListError(t *testing.T) {
	client := &mockK8sClient{listErr: fmt.Errorf("k8s unavailable")}
	r := &DetectResult{
		Diagnostics: &SourceDiagnostics{},
		K8s:         NewK8sSource(client, "", "pactos"),
	}

	r.EnrichFromK8s(context.Background(), newMockBundleStore(), "")
	if r.OCI != nil {
		t.Error("expected nil OCI when K8s list fails")
	}
	if r.K8s != nil {
		t.Error("expected K8s to be nil after permanent list error")
	}
}

func TestEnrichFromK8s_NoImageRefs(t *testing.T) {
	k8sData := `{"items": [
		{"metadata": {"name": "svc", "namespace": "default"},
		 "status": {"contractStatus": "Compliant", "contract": {"serviceName": "svc", "version": "1.0.0"}}}
	]}`

	client := &mockK8sClient{listJSON: []byte(k8sData)}
	r := &DetectResult{
		Diagnostics: &SourceDiagnostics{},
		K8s:         NewK8sSource(client, "", "pactos"),
	}

	r.EnrichFromK8s(context.Background(), newMockBundleStore(), "")
	if r.OCI != nil {
		t.Error("expected nil OCI when no services have imageRefs")
	}
}

func TestEnrichFromK8s_CacheSourceAlreadyExists(t *testing.T) {
	k8sData := `{"items": [
		{"metadata": {"name": "svc", "namespace": "default"},
		 "status": {"contract": {"serviceName": "svc", "imageRef": "ghcr.io/org/svc:1.0.0"}}}
	]}`

	client := &mockK8sClient{listJSON: []byte(k8sData)}
	existingCache := NewCacheSource(t.TempDir())
	r := &DetectResult{
		Diagnostics: &SourceDiagnostics{},
		K8s:         NewK8sSource(client, "", "pactos"),
		Cache:       existingCache,
	}

	r.EnrichFromK8s(context.Background(), newMockBundleStore(), "")
	// Cache source should remain the existing one, not overwritten.
	if r.Cache != existingCache {
		t.Error("expected existing CacheSource to be preserved")
	}
}

func TestEnsureCacheSource_DefaultDir(t *testing.T) {
	home := t.TempDir()
	orig := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = orig })
	t.Setenv("XDG_CACHE_HOME", "")

	r := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	r.ensureCacheSource("")

	if r.Cache == nil {
		t.Fatal("expected CacheSource to be created")
	}
	expected := filepath.Join(home, ".cache", "pacto", "oci")
	if r.Diagnostics.Cache.CacheDir != expected {
		t.Errorf("expected cache dir %q, got %q", expected, r.Diagnostics.Cache.CacheDir)
	}
}

func TestEnsureCacheSource_XDGDir(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", xdg)

	r := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	r.ensureCacheSource("")

	expected := filepath.Join(xdg, "pacto", "oci")
	if r.Diagnostics.Cache.CacheDir != expected {
		t.Errorf("expected cache dir %q, got %q", expected, r.Diagnostics.Cache.CacheDir)
	}
}

func TestEnsureCacheSource_ExplicitDir(t *testing.T) {
	dir := t.TempDir()
	r := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	r.ensureCacheSource(dir)

	if r.Cache == nil {
		t.Fatal("expected CacheSource to be created")
	}
	if r.Diagnostics.Cache.CacheDir != dir {
		t.Errorf("expected cache dir %q, got %q", dir, r.Diagnostics.Cache.CacheDir)
	}
}

func TestEnsureCacheSource_HomeDirError(t *testing.T) {
	orig := userHomeDir
	userHomeDir = func() (string, error) { return "", fmt.Errorf("no home") }
	t.Cleanup(func() { userHomeDir = orig })
	t.Setenv("XDG_CACHE_HOME", "")

	r := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	r.ensureCacheSource("")

	if r.Cache != nil {
		t.Error("expected nil CacheSource when home dir fails")
	}
}

func TestRedetectK8s_NoClient(t *testing.T) {
	old := newK8sClientFunc
	newK8sClientFunc = func() (K8sClient, error) {
		return nil, fmt.Errorf("no cluster")
	}
	t.Cleanup(func() { newK8sClientFunc = old })

	r := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	RedetectK8s(context.Background(), r, "")
	if r.K8s != nil {
		t.Error("expected nil K8s when client creation fails")
	}
}

func TestRedetectK8s_WithClient(t *testing.T) {
	old := newK8sClientFunc
	newK8sClientFunc = func() (K8sClient, error) {
		return &mockK8sClient{
			crdDiscovery: &CRDDiscovery{Found: true, Version: "v1alpha1", ResourceName: "pactos"},
			listJSON:     []byte(`{"items":[]}`),
		}, nil
	}
	t.Cleanup(func() { newK8sClientFunc = old })

	r := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	RedetectK8s(context.Background(), r, "")
	if r.K8s == nil {
		t.Error("expected K8s source when client succeeds")
	}
}

func TestCurrentKubeContext(t *testing.T) {
	old := currentKubeContextFunc
	currentKubeContextFunc = func() string { return "test-context" }
	t.Cleanup(func() { currentKubeContextFunc = old })

	got := CurrentKubeContext()
	if got != "test-context" {
		t.Errorf("CurrentKubeContext() = %q, want %q", got, "test-context")
	}
}
