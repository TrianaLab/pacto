package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/trianalab/pacto/internal/app"
	"github.com/trianalab/pacto/internal/oci"
	"github.com/trianalab/pacto/pkg/contract"
	"github.com/trianalab/pacto/pkg/dashboard"
)

// dummyStore satisfies oci.BundleStore for CLI tests.
type dummyStore struct{}

func (dummyStore) Push(context.Context, string, *contract.Bundle) (string, error) { return "", nil }
func (dummyStore) Pull(context.Context, string) (*contract.Bundle, error)         { return nil, nil }
func (dummyStore) Resolve(context.Context, string) (string, error)                { return "", nil }
func (dummyStore) ListTags(context.Context, string) ([]string, error)             { return nil, nil }

var _ oci.BundleStore = dummyStore{}

func TestCacheTTL(t *testing.T) {
	tests := []struct {
		sourceType string
		expected   time.Duration
	}{
		{"k8s", 10 * time.Second},
		{"oci", 5 * time.Minute},
		{"local", 2 * time.Second},
		{"unknown", 30 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.sourceType, func(t *testing.T) {
			got := cacheTTL(tt.sourceType)
			if got != tt.expected {
				t.Errorf("cacheTTL(%q) = %v, want %v", tt.sourceType, got, tt.expected)
			}
		})
	}
}

func TestNewDashboardCommand_NoSources(t *testing.T) {
	// Isolate from host kubeconfig / cache so no real sources are found.
	emptyDir := t.TempDir()
	t.Setenv("PATH", emptyDir)
	t.Setenv("HOME", emptyDir)
	t.Setenv("XDG_CACHE_HOME", emptyDir)
	t.Setenv("KUBECONFIG", filepath.Join(emptyDir, "nonexistent"))

	svc := app.NewService(nil, nil)
	v := viper.New()
	cmd := newDashboardCommand(svc, v, "test")
	cmd.SetArgs([]string{"/nonexistent/empty/dir"})
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)

	// Use a cancelled context as safety net to prevent server from blocking.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd.SetContext(ctx)

	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when no data sources are detected")
	}
}

func TestNewDashboardCommand_WithLocalSource(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(`pactoVersion: "1.0"
service:
  name: test-svc
  version: 1.0.0
`), 0644); err != nil {
		t.Fatal(err)
	}

	// Prevent real K8s client creation.
	t.Setenv("KUBECONFIG", filepath.Join(dir, "nonexistent"))

	svc := app.NewService(dummyStore{}, nil)
	v := viper.New()
	cmd := newDashboardCommand(svc, v, "test")
	cmd.SetArgs([]string{dir, "--port", "0", "--diagnostics"})

	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)

	// Use a pre-cancelled context so the server stops immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd.SetContext(ctx)

	// The server exits immediately due to cancelled context.
	_ = cmd.Execute()

	stderr := errBuf.String()
	if !strings.Contains(stderr, "local") {
		t.Errorf("expected stderr to mention 'local' source, got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "enabled") {
		t.Errorf("expected stderr to mention 'enabled', got:\n%s", stderr)
	}
}

func TestNewDashboardCommand_WithOCISource(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(`pactoVersion: "1.0"
service:
  name: oci-wiring-svc
  version: 1.0.0
`), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", filepath.Join(dir, "nonexistent"))
	t.Setenv("PACTO_DASHBOARD_REPO", "ghcr.io/org/svc-a")

	// Create a cache dir with a real bundle to exercise SetCache wiring.
	bundlePath := filepath.Join(dir, ".cache", "pacto", "oci", "ghcr.io", "org", "cached", "1.0.0", "bundle.tar.gz")
	writeTestBundleTarGz(t, bundlePath, `pactoVersion: "1.0"
service:
  name: cached-svc
  version: 1.0.0
`)
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, ".cache"))

	svc := app.NewService(dummyStore{}, nil)
	v := viper.New()
	cmd := newDashboardCommand(svc, v, "test")
	cmd.SetArgs([]string{dir, "--port", "0"})

	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	cmd.SetOut(&bytes.Buffer{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd.SetContext(ctx)

	_ = cmd.Execute()

	stderr := errBuf.String()
	if !strings.Contains(stderr, "oci: enabled") {
		t.Errorf("expected stderr to mention 'oci: enabled', got:\n%s", stderr)
	}
}

func TestNewDashboardCommand_DefaultDir(t *testing.T) {
	// When no dir arg is provided, it defaults to ".".
	// Create a temp dir with pacto.yaml and chdir into it.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(`pactoVersion: "1.0"
service:
  name: default-dir-svc
  version: 1.0.0
`), 0644); err != nil {
		t.Fatal(err)
	}

	// Prevent real K8s client creation.
	t.Setenv("KUBECONFIG", filepath.Join(dir, "nonexistent"))

	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	svc := app.NewService(nil, nil)
	v := viper.New()
	cmd := newDashboardCommand(svc, v, "test")
	cmd.SetArgs([]string{"--port", "0"}) // no dir arg

	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	cmd.SetOut(&bytes.Buffer{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd.SetContext(ctx)

	_ = cmd.Execute()

	stderr := errBuf.String()
	if !strings.Contains(stderr, "local") {
		t.Errorf("expected stderr to mention 'local', got:\n%s", stderr)
	}
}

func TestNewDashboardCommand_NoSourcesDetails(t *testing.T) {
	// Use an empty temp dir with no pacto.yaml, no kubeconfig, no OCI, no cache.
	emptyDir := t.TempDir()

	svc := app.NewService(nil, nil)
	v := viper.New()
	cmd := newDashboardCommand(svc, v, "test")
	cmd.SetArgs([]string{emptyDir})

	// Ensure no K8s client can be created.
	t.Setenv("PATH", emptyDir)
	t.Setenv("HOME", emptyDir)
	t.Setenv("XDG_CACHE_HOME", emptyDir)
	t.Setenv("KUBECONFIG", filepath.Join(emptyDir, "nonexistent"))

	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	cmd.SetOut(&bytes.Buffer{})

	// Use a cancelled context as safety net to prevent server from blocking.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd.SetContext(ctx)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	stderr := errBuf.String()
	if !strings.Contains(stderr, "No data sources detected") {
		t.Errorf("expected 'No data sources detected' message, got:\n%s", stderr)
	}
}

func TestNewDashboardCommand_RepoEnvVar(t *testing.T) {
	emptyDir := t.TempDir()
	t.Setenv("PACTO_DASHBOARD_REPO", "ghcr.io/org/svc-a,ghcr.io/org/svc-b")
	t.Setenv("HOME", emptyDir)
	t.Setenv("XDG_CACHE_HOME", emptyDir)
	t.Setenv("KUBECONFIG", filepath.Join(emptyDir, "nonexistent"))

	svc := app.NewService(dummyStore{}, nil)
	v := viper.New()
	cmd := newDashboardCommand(svc, v, "test")
	cmd.SetArgs([]string{emptyDir, "--port", "0"})

	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	cmd.SetOut(&bytes.Buffer{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd.SetContext(ctx)

	_ = cmd.Execute()

	stderr := errBuf.String()
	if !strings.Contains(stderr, "oci") {
		t.Errorf("expected stderr to mention 'oci' source when PACTO_DASHBOARD_REPO is set, got:\n%s", stderr)
	}
}

func TestNewDashboardCommand_RepoFlagOverridesEnv(t *testing.T) {
	emptyDir := t.TempDir()
	t.Setenv("PACTO_DASHBOARD_REPO", "ghcr.io/org/from-env")
	t.Setenv("HOME", emptyDir)
	t.Setenv("XDG_CACHE_HOME", emptyDir)
	t.Setenv("KUBECONFIG", filepath.Join(emptyDir, "nonexistent"))

	svc := app.NewService(dummyStore{}, nil)
	v := viper.New()
	cmd := newDashboardCommand(svc, v, "test")
	cmd.SetArgs([]string{emptyDir, "--port", "0", "--repo", "ghcr.io/org/from-flag"})

	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	cmd.SetOut(&bytes.Buffer{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd.SetContext(ctx)

	_ = cmd.Execute()

	stderr := errBuf.String()
	// The flag value should be used, not the env var — both enable OCI.
	if !strings.Contains(stderr, "oci") {
		t.Errorf("expected stderr to mention 'oci' source, got:\n%s", stderr)
	}
}

func TestNewDashboardCommand_HostFlag(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(`pactoVersion: "1.0"
service:
  name: host-test
  version: 1.0.0
`), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", filepath.Join(dir, "nonexistent"))

	svc := app.NewService(dummyStore{}, nil)
	v := viper.New()
	cmd := newDashboardCommand(svc, v, "test")
	cmd.SetArgs([]string{dir, "--port", "0", "--host", "0.0.0.0"})

	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	cmd.SetOut(&bytes.Buffer{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd.SetContext(ctx)

	_ = cmd.Execute()

	stderr := errBuf.String()
	// When host is 0.0.0.0, display should show 127.0.0.1 for user-friendliness.
	if !strings.Contains(stderr, "127.0.0.1") {
		t.Errorf("expected display address 127.0.0.1 when host is 0.0.0.0, got:\n%s", stderr)
	}
}

func TestNewDashboardCommand_DefaultFlags(t *testing.T) {
	svc := app.NewService(nil, nil)
	v := viper.New()
	cmd := newDashboardCommand(svc, v, "test")

	// Verify default flag values
	host, _ := cmd.Flags().GetString("host")
	if host != "127.0.0.1" {
		t.Errorf("expected default host 127.0.0.1, got %q", host)
	}
	port, _ := cmd.Flags().GetInt("port")
	if port != 3000 {
		t.Errorf("expected default port 3000, got %d", port)
	}
	ns, _ := cmd.Flags().GetString("namespace")
	if ns != "" {
		t.Errorf("expected default namespace empty, got %q", ns)
	}
	diag, _ := cmd.Flags().GetBool("diagnostics")
	if diag {
		t.Error("expected diagnostics default false")
	}
}

func TestDeduplicateSourceInfo(t *testing.T) {
	info := []dashboard.SourceInfo{
		{Type: "oci", Enabled: false, Reason: "no repos"},
		{Type: "local", Enabled: true, Reason: "found"},
		{Type: "oci", Enabled: true, Reason: "discovered"},
	}
	result := deduplicateSourceInfo(info)
	if len(result) != 2 {
		t.Fatalf("expected 2 deduplicated entries, got %d", len(result))
	}
	// OCI should have the last (updated) entry.
	for _, si := range result {
		if si.Type == "oci" && !si.Enabled {
			t.Error("expected oci to be enabled (last wins)")
		}
	}
}

func TestDeduplicateSourceInfo_NoDuplicates(t *testing.T) {
	info := []dashboard.SourceInfo{
		{Type: "local", Enabled: true, Reason: "found"},
		{Type: "k8s", Enabled: true, Reason: "cluster reachable"},
	}
	result := deduplicateSourceInfo(info)
	if len(result) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result))
	}
}

func TestRetryOCIEnrichment_WithExplicitRepos(t *testing.T) {
	var buf bytes.Buffer
	result := retryOCIEnrichment(
		context.Background(), &buf,
		&dashboard.DetectResult{Diagnostics: &dashboard.SourceDiagnostics{}},
		dummyStore{}, "", []string{"ghcr.io/org/svc"},
	)
	if result {
		t.Error("expected false when explicit repos are provided")
	}
}

func TestRetryOCIEnrichment_NilStore(t *testing.T) {
	var buf bytes.Buffer
	result := retryOCIEnrichment(
		context.Background(), &buf,
		&dashboard.DetectResult{Diagnostics: &dashboard.SourceDiagnostics{}},
		nil, "", nil,
	)
	if result {
		t.Error("expected false when store is nil")
	}
}

func TestRetryOCIEnrichment_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	var buf bytes.Buffer
	result := retryOCIEnrichment(
		ctx, &buf,
		&dashboard.DetectResult{
			Diagnostics: &dashboard.SourceDiagnostics{},
		},
		dummyStore{}, t.TempDir(), nil,
	)
	// With cancelled context and no K8s source, enrichment won't find OCI.
	// Should return true (needs lazy enrichment).
	if !result {
		t.Error("expected true (needs lazy enrichment) with cancelled context")
	}
}

func TestRetryOCIEnrichment_ExhaustsRetries(t *testing.T) {
	// Override retry interval to avoid 5s waits.
	old := enrichRetryInterval
	enrichRetryInterval = time.Millisecond
	t.Cleanup(func() { enrichRetryInterval = old })

	var buf bytes.Buffer
	result := retryOCIEnrichment(
		context.Background(), &buf,
		&dashboard.DetectResult{
			Diagnostics: &dashboard.SourceDiagnostics{},
		},
		dummyStore{}, t.TempDir(), nil,
	)
	if !result {
		t.Error("expected true (needs lazy enrichment) after exhausting retries")
	}
	output := buf.String()
	if !strings.Contains(output, "waiting for K8s") {
		t.Errorf("expected retry progress messages, got:\n%s", output)
	}
	if !strings.Contains(output, "will retry lazily") {
		t.Errorf("expected lazy retry message, got:\n%s", output)
	}
}

func TestWireOCIEnrichment_NoK8s(t *testing.T) {
	detectResult := &dashboard.DetectResult{
		Diagnostics: &dashboard.SourceDiagnostics{},
	}
	resolved := dashboard.BuildResolvedSource(map[string]dashboard.DataSource{})
	srv := dashboard.NewServer(nil, nil)
	memCache := dashboard.NewMemoryCache()

	fn := wireOCIEnrichment(detectResult, resolved, srv, memCache, dummyStore{}, t.TempDir())

	// Without K8s source, enrichment should fail.
	if fn(context.Background()) {
		t.Error("expected false when no K8s source is available")
	}
}

// cliMockK8sClient implements dashboard.K8sClient for CLI tests.
type cliMockK8sClient struct {
	listJSON []byte
}

func (m *cliMockK8sClient) Probe(context.Context) error { return nil }
func (m *cliMockK8sClient) DiscoverCRD(context.Context) (*dashboard.CRDDiscovery, error) {
	return &dashboard.CRDDiscovery{Found: false}, nil
}
func (m *cliMockK8sClient) ListJSON(_ context.Context, _, _ string) ([]byte, error) {
	return m.listJSON, nil
}
func (m *cliMockK8sClient) GetJSON(_ context.Context, _, _, name string) ([]byte, error) {
	return nil, nil
}
func (m *cliMockK8sClient) CountResources(context.Context, string, string) (int, error) {
	return 0, nil
}

// enrichStore implements oci.BundleStore returning predefined bundles.
type enrichStore struct {
	tags   []string
	bundle *contract.Bundle
}

func (s *enrichStore) Push(context.Context, string, *contract.Bundle) (string, error) { return "", nil }
func (s *enrichStore) Resolve(context.Context, string) (string, error)                { return "", nil }
func (s *enrichStore) Pull(_ context.Context, _ string) (*contract.Bundle, error) {
	if s.bundle != nil {
		return s.bundle, nil
	}
	return nil, nil
}
func (s *enrichStore) ListTags(_ context.Context, _ string) ([]string, error) {
	return s.tags, nil
}

func TestRetryOCIEnrichment_SucceedsOnFirstTry(t *testing.T) {
	old := enrichRetryInterval
	enrichRetryInterval = time.Millisecond
	t.Cleanup(func() { enrichRetryInterval = old })

	k8sData := `{"items": [
		{"metadata": {"name": "svc", "namespace": "default"},
		 "status": {"contract": {"serviceName": "svc", "imageRef": "ghcr.io/org/svc:1.0.0"}}}
	]}`
	k8sClient := &cliMockK8sClient{listJSON: []byte(k8sData)}
	store := &enrichStore{
		tags: []string{"1.0.0"},
		bundle: &contract.Bundle{
			Contract: &contract.Contract{
				PactoVersion: "1.0",
				Service:      contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
			},
		},
	}

	detectResult := &dashboard.DetectResult{
		Diagnostics: &dashboard.SourceDiagnostics{},
		K8s:         dashboard.NewK8sSource(k8sClient, "", "pactos"),
	}

	var buf bytes.Buffer
	result := retryOCIEnrichment(
		context.Background(), &buf,
		detectResult, store, t.TempDir(), nil,
	)
	if result {
		t.Error("expected false (OCI found on first try)")
	}
	if detectResult.OCI == nil {
		t.Error("expected OCI source to be created")
	}
}

// writeTestBundleTarGz creates a minimal bundle.tar.gz at the given path.
func writeTestBundleTarGz(t *testing.T, path string, pactoYAML string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	data := []byte(pactoYAML)
	_ = tw.WriteHeader(&tar.Header{Name: "pacto.yaml", Size: int64(len(data)), Mode: 0644})
	_, _ = tw.Write(data)
	_ = tw.Close()
	_ = gw.Close()
}

func TestWireOCIEnrichment_Success(t *testing.T) {
	k8sData := `{"items": [
		{"metadata": {"name": "svc", "namespace": "default"},
		 "status": {"contract": {"serviceName": "svc", "imageRef": "ghcr.io/org/svc:1.0.0"}}}
	]}`
	k8sClient := &cliMockK8sClient{listJSON: []byte(k8sData)}
	store := &enrichStore{
		tags: []string{"1.0.0"},
		bundle: &contract.Bundle{
			Contract: &contract.Contract{
				PactoVersion: "1.0",
				Service:      contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
			},
		},
	}

	detectResult := &dashboard.DetectResult{
		Diagnostics: &dashboard.SourceDiagnostics{},
		K8s:         dashboard.NewK8sSource(k8sClient, "", "pactos"),
	}
	resolved := dashboard.BuildResolvedSource(map[string]dashboard.DataSource{})
	srv := dashboard.NewServer(nil, nil)
	memCache := dashboard.NewMemoryCache()

	fn := wireOCIEnrichment(detectResult, resolved, srv, memCache, store, t.TempDir())

	if !fn(context.Background()) {
		t.Error("expected true when OCI enrichment succeeds")
	}
	if !resolved.HasSource("oci") {
		t.Error("expected oci source to be added to resolved")
	}
}
