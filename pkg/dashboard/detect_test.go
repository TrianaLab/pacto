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

func TestDetectResult_ActiveSources_Multiple(t *testing.T) {
	root := t.TempDir()
	cache := NewCacheSource(root)
	r := &DetectResult{
		Local: NewLocalSource("."),
		Cache: cache,
	}
	sources := r.ActiveSources()
	if len(sources) != 2 {
		t.Fatalf("expected 2 active sources, got %d", len(sources))
	}
	if _, ok := sources["local"]; !ok {
		t.Error("expected 'local' in active sources")
	}
	if _, ok := sources["cache"]; !ok {
		t.Error("expected 'cache' in active sources")
	}
}

func TestDetectResult_ActiveSources_AllTypes(t *testing.T) {
	root := t.TempDir()
	cache := NewCacheSource(root)
	r := &DetectResult{
		Local: NewLocalSource("."),
		Cache: cache,
		K8s:   NewK8sSource("default", "pactos"),
		// OCI is nil — would need real store
	}
	sources := r.ActiveSources()
	if len(sources) != 3 {
		t.Fatalf("expected 3 active sources, got %d", len(sources))
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
	if len(result.Sources) != 1 {
		t.Fatalf("expected 1 source info, got %d", len(result.Sources))
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
	if len(result.Sources) != 1 {
		t.Fatalf("expected 1 source info, got %d", len(result.Sources))
	}
	if !result.Sources[0].Enabled {
		t.Error("expected cache source to be enabled")
	}
	if result.Diagnostics.Cache.ServiceCount != 1 {
		t.Errorf("expected 1 service, got %d", result.Diagnostics.Cache.ServiceCount)
	}
}

func TestDetectSources_WithLocalAndNoCache(t *testing.T) {
	root := t.TempDir()
	writeLocalPactoYAML(t, root, "svc", "1.0.0")

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

	// Check that NoCache branch produces a source info with the right reason.
	var cacheInfo *SourceInfo
	for i := range result.Sources {
		if result.Sources[i].Type == "cache" {
			cacheInfo = &result.Sources[i]
			break
		}
	}
	if cacheInfo == nil {
		t.Fatal("expected cache source info even when disabled")
	}
	if cacheInfo.Enabled {
		t.Error("expected cache to be disabled")
	}
	if cacheInfo.Reason != "disabled by --no-cache flag" {
		t.Errorf("expected 'disabled by --no-cache flag', got %q", cacheInfo.Reason)
	}
}

func TestDetectSources_WithCacheEnabled(t *testing.T) {
	root := t.TempDir()

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

func TestDetectK8s_KubectlNotFound(t *testing.T) {
	// Remove kubectl from PATH entirely.
	t.Setenv("PATH", t.TempDir())

	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectK8s(context.Background(), "")

	if result.K8s != nil {
		t.Error("expected nil K8s source when kubectl not found")
	}
	if len(result.Sources) != 1 {
		t.Fatalf("expected 1 source info, got %d", len(result.Sources))
	}
	if result.Sources[0].Enabled {
		t.Error("expected source to be disabled")
	}
	if !strings.Contains(result.Sources[0].Reason, "kubectl not found") {
		t.Errorf("expected reason about kubectl not found, got %q", result.Sources[0].Reason)
	}
}

func TestDetectK8s_ClusterNotReachable(t *testing.T) {
	// Fake kubectl that succeeds for LookPath but fails for cluster-info.
	setupFakeKubectlForDetect(t, map[string]fakeKubectlResponse{
		"cluster-info": {exitCode: 1},
	})

	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectK8s(context.Background(), "")

	if result.K8s != nil {
		t.Error("expected nil K8s source when cluster unreachable")
	}
	if !result.Diagnostics.K8s.KubectlFound {
		t.Error("expected KubectlFound=true")
	}
	if result.Diagnostics.K8s.ClusterReachable {
		t.Error("expected ClusterReachable=false")
	}
	if result.Diagnostics.K8s.Error == "" {
		t.Error("expected error in diagnostics")
	}
}

func TestDetectK8s_FullSuccess(t *testing.T) {
	apiResourcesOutput := "pactos   pc   pacto.trianalab.io/v1alpha1   true   Pacto"
	countOutput := "pacto.trianalab.io/v1alpha1/svc-a\npacto.trianalab.io/v1alpha1/svc-b"

	setupFakeKubectlForDetect(t, map[string]fakeKubectlResponse{
		"cluster-info":  {output: "Kubernetes control plane is running"},
		"api-resources": {output: apiResourcesOutput},
		"get":           {output: countOutput},
	})

	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectK8s(context.Background(), "default")

	if result.K8s == nil {
		t.Fatal("expected K8s source to be detected")
	}
	if !result.Diagnostics.K8s.KubectlFound {
		t.Error("expected KubectlFound=true")
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
	// cluster-info succeeds, api-resources returns nothing, get still works
	setupFakeKubectlForDetect(t, map[string]fakeKubectlResponse{
		"cluster-info":  {output: "OK"},
		"api-resources": {output: ""},
		"get":           {output: ""},
	})

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
	setupFakeKubectlForDetect(t, map[string]fakeKubectlResponse{
		"cluster-info":  {output: "OK"},
		"api-resources": {output: ""},
		"get":           {output: ""},
	})

	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectK8s(context.Background(), "") // empty namespace = all namespaces

	if !result.Diagnostics.K8s.AllNamespaces {
		t.Error("expected AllNamespaces=true for empty namespace")
	}
}

func TestDetectK8s_KubeconfigEnv(t *testing.T) {
	t.Setenv("KUBECONFIG", "/custom/kubeconfig")
	// No kubectl in PATH
	t.Setenv("PATH", t.TempDir())

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

	// No kubectl in PATH
	t.Setenv("PATH", t.TempDir())

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
	if len(result.Sources) != 1 {
		t.Fatalf("expected 1 source info, got %d", len(result.Sources))
	}
	if result.Sources[0].Enabled {
		t.Error("expected source to be disabled")
	}
	if result.Diagnostics.Cache.Error == "" {
		t.Error("expected error in diagnostics")
	}
}

// fakeKubectlResponse defines the output and exit code for a kubectl subcommand.
type fakeKubectlResponse struct {
	output   string
	exitCode int
}

// setupFakeKubectlForDetect creates a kubectl script that dispatches based on the
// first argument (e.g., "cluster-info", "api-resources", "get").
func setupFakeKubectlForDetect(t *testing.T, responses map[string]fakeKubectlResponse) {
	t.Helper()
	dir := t.TempDir()

	var cases strings.Builder
	for cmd, resp := range responses {
		if resp.exitCode != 0 {
			fmt.Fprintf(&cases, "  %s) exit %d ;;\n", cmd, resp.exitCode)
		} else if resp.output == "" {
			fmt.Fprintf(&cases, "  %s) exit 0 ;;\n", cmd)
		} else {
			fmt.Fprintf(&cases, "  %s) cat <<'ENDOFOUTPUT'\n%s\nENDOFOUTPUT\n;;\n", cmd, resp.output)
		}
	}

	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
%s  *) exit 0 ;;
esac
`, cases.String())

	scriptPath := filepath.Join(dir, "kubectl")
	_ = os.WriteFile(scriptPath, []byte(script), 0o755)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
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
