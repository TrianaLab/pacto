package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/oci"
)

// dummyStore satisfies oci.BundleStore for CLI tests.
type dummyStore struct{}

func (dummyStore) Push(context.Context, string, *contract.Bundle) (string, error) { return "", nil }
func (dummyStore) Pull(context.Context, string) (*contract.Bundle, error)         { return nil, nil }
func (dummyStore) Resolve(context.Context, string) (string, error)                { return "", nil }
func (dummyStore) ListTags(context.Context, string) ([]string, error)             { return nil, nil }

var _ oci.BundleStore = dummyStore{}

// dummyStoreWithCacheDir satisfies oci.BundleStore and implements CacheDir().
type dummyStoreWithCacheDir struct {
	dummyStore
	cacheDir string
}

func (d dummyStoreWithCacheDir) CacheDir() string { return d.cacheDir }

// isolateHost points every ambient lookup the dashboard makes — kubeconfig,
// home, cache — at an empty directory, so a developer's real cluster or bundle
// cache can never turn a "nothing detected" test green by accident.
func isolateHost(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("KUBECONFIG", filepath.Join(dir, "nonexistent"))
	t.Setenv("PACTO_DASHBOARD_REPO", "")
	t.Setenv("PACTO_EVIDENCE_SOURCE_URL", "")
	return dir
}

// writeBundle writes a minimal contract so a directory reads as a local source.
func writeBundle(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "pactoVersion: \"2.0\"\nservice:\n  name: " + name + "\n  version: 1.0.0\n"
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// runDashboard executes the command with a pre-cancelled context, so the server
// returns immediately, and hands back what the user would have seen on stderr.
func runDashboard(t *testing.T, svc *app.Service, v *viper.Viper, args ...string) (string, error) {
	t.Helper()
	cmd := newDashboardCommand(svc, v, "test")
	cmd.SetArgs(args)
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	cmd.SetOut(&bytes.Buffer{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd.SetContext(ctx)

	err := cmd.Execute()
	return errBuf.String(), err
}

func TestNewDashboardCommand_NoSources(t *testing.T) {
	isolateHost(t)

	stderr, err := runDashboard(t, app.NewService(nil, nil), viper.New(), "/nonexistent/empty/dir")
	if err == nil {
		t.Fatal("expected an error when no data sources are detected")
	}
	if !strings.Contains(err.Error(), "no data sources detected") {
		t.Errorf("error = %v, want it to say no data sources were detected", err)
	}
	if !strings.Contains(stderr, "no data sources detected") {
		t.Errorf("expected the reason on stderr, got:\n%s", stderr)
	}
}

// TestNewDashboardCommand_RejectsMalformedTraceSource proves an unusable
// observation configuration stops the command before anything is detected or
// served, rather than starting a dashboard that silently lacks a Data Source.
func TestNewDashboardCommand_RejectsMalformedTraceSource(t *testing.T) {
	isolateHost(t)

	_, err := runDashboard(t, app.NewService(nil, nil), viper.New(),
		t.TempDir(), "--port", "0", "--trace-source", "/no/name.json")
	if err == nil {
		t.Fatal("expected the malformed --trace-source to fail startup")
	}
	if !strings.Contains(err.Error(), `invalid --trace-source "/no/name.json"`) {
		t.Errorf("error = %v, want it to name the malformed value", err)
	}
}

func TestNewDashboardCommand_WithLocalSource(t *testing.T) {
	isolateHost(t)
	dir := t.TempDir()
	writeBundle(t, dir, "test-svc")

	svc := app.NewService(dummyStoreWithCacheDir{cacheDir: filepath.Join(dir, "cache")}, nil)
	stderr, _ := runDashboard(t, svc, viper.New(), dir, "--port", "0")

	if !strings.Contains(stderr, "Sources: local") {
		t.Errorf("expected the banner to report the local source, got:\n%s", stderr)
	}
}

// TestNewDashboardCommand_WithOCIAndCache proves an explicit repository and a
// non-empty bundle cache both reach the fleet options, and that the banner
// names them.
func TestNewDashboardCommand_WithOCIAndCache(t *testing.T) {
	dir := isolateHost(t)
	t.Setenv("PACTO_DASHBOARD_REPO", "ghcr.io/org/svc-a")

	cacheDir := filepath.Join(dir, "cache")
	if err := os.MkdirAll(filepath.Join(cacheDir, "_v2"), 0o755); err != nil {
		t.Fatal(err)
	}

	svc := app.NewService(dummyStoreWithCacheDir{cacheDir: cacheDir}, nil)
	stderr, _ := runDashboard(t, svc, viper.New(), dir, "--port", "0")

	if !strings.Contains(stderr, "Sources: oci, cache") {
		t.Errorf("expected the banner to report oci and cache, got:\n%s", stderr)
	}
}

// TestNewDashboardCommand_NoCacheExcludesTheCache pins --no-cache: entries on
// disk exist, but a cold start promises not to read them, so the cache is never
// published as a source.
func TestNewDashboardCommand_NoCacheExcludesTheCache(t *testing.T) {
	dir := isolateHost(t)
	cacheDir := filepath.Join(dir, "cache")
	if err := os.MkdirAll(filepath.Join(cacheDir, "_v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeBundle(t, dir, "nocache-svc")

	v := viper.New()
	v.Set("no-cache", true)
	v.Set("cache-dir", cacheDir)
	svc := app.NewService(dummyStore{}, nil)
	stderr, _ := runDashboard(t, svc, v, dir, "--port", "0")

	if strings.Contains(stderr, "cache") {
		t.Errorf("--no-cache must not publish a cache source, got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "Sources: local") {
		t.Errorf("expected the local source to survive --no-cache, got:\n%s", stderr)
	}
}

func TestNewDashboardCommand_DefaultDir(t *testing.T) {
	dir := isolateHost(t)
	writeBundle(t, dir, "default-dir-svc")

	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	stderr, _ := runDashboard(t, app.NewService(nil, nil), viper.New(), "--port", "0")
	if !strings.Contains(stderr, "Sources: local") {
		t.Errorf("expected the working directory to be detected, got:\n%s", stderr)
	}
}

func TestNewDashboardCommand_OCIPositionalArgsOverrideEnv(t *testing.T) {
	dir := isolateHost(t)
	t.Setenv("PACTO_DASHBOARD_REPO", "ghcr.io/org/from-env")

	svc := app.NewService(dummyStore{}, nil)
	stderr, _ := runDashboard(t, svc, viper.New(), dir, "oci://ghcr.io/org/from-arg", "--port", "0")

	if !strings.Contains(stderr, "Sources: oci") {
		t.Errorf("expected an oci source, got:\n%s", stderr)
	}
}

func TestNewDashboardCommand_HostFlag(t *testing.T) {
	dir := isolateHost(t)
	writeBundle(t, dir, "host-test")

	svc := app.NewService(dummyStore{}, nil)
	stderr, _ := runDashboard(t, svc, viper.New(), dir, "--port", "0", "--host", "0.0.0.0")

	// When host is 0.0.0.0, display should show 127.0.0.1 for user-friendliness.
	if !strings.Contains(stderr, "127.0.0.1") {
		t.Errorf("expected display address 127.0.0.1 when host is 0.0.0.0, got:\n%s", stderr)
	}
}

func TestNewDashboardCommand_DefaultFlags(t *testing.T) {
	cmd := newDashboardCommand(app.NewService(nil, nil), viper.New(), "test")

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
}

func TestNewDashboardCommand_InvalidArgs(t *testing.T) {
	isolateHost(t)
	if _, err := runDashboard(t, app.NewService(nil, nil), viper.New(), "./a", "./b"); err == nil {
		t.Error("expected an error for multiple local paths")
	}
}

func TestParseDashboardArgs_Empty(t *testing.T) {
	t.Setenv("PACTO_DASHBOARD_REPO", "")
	dir, repos, err := parseDashboardArgs(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != "." {
		t.Errorf("expected default dir \".\", got %q", dir)
	}
	if len(repos) != 0 {
		t.Errorf("expected no repos, got %v", repos)
	}
}

func TestParseDashboardArgs_LocalOnly(t *testing.T) {
	t.Setenv("PACTO_DASHBOARD_REPO", "")
	dir, repos, err := parseDashboardArgs([]string{"./services"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != "./services" {
		t.Errorf("expected dir ./services, got %q", dir)
	}
	if len(repos) != 0 {
		t.Errorf("expected no repos, got %v", repos)
	}
}

func TestParseDashboardArgs_Mixed(t *testing.T) {
	dir, repos, err := parseDashboardArgs([]string{
		"./services",
		"oci://ghcr.io/org/svc-a",
		"oci://ghcr.io/org/svc-b",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != "./services" {
		t.Errorf("expected dir ./services, got %q", dir)
	}
	if len(repos) != 2 {
		t.Errorf("expected 2 repos, got %v", repos)
	}
}

func TestParseDashboardArgs_MultipleLocalPaths(t *testing.T) {
	_, _, err := parseDashboardArgs([]string{"./a", "./b"})
	if err == nil {
		t.Fatal("expected an error for multiple local paths")
	}
	if !strings.Contains(err.Error(), "only one local path") {
		t.Errorf("expected 'only one local path' error, got: %v", err)
	}
}

func TestParseDashboardArgs_EnvVarFallback(t *testing.T) {
	t.Setenv("PACTO_DASHBOARD_REPO", "ghcr.io/org/svc-a,ghcr.io/org/svc-b")
	dir, repos, err := parseDashboardArgs(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != "." {
		t.Errorf("expected default dir \".\", got %q", dir)
	}
	if len(repos) != 2 || repos[0] != "ghcr.io/org/svc-a" || repos[1] != "ghcr.io/org/svc-b" {
		t.Errorf("expected 2 repos from env, got %v", repos)
	}
}

func TestParseDashboardArgs_OCIArgsOverrideEnvVar(t *testing.T) {
	t.Setenv("PACTO_DASHBOARD_REPO", "ghcr.io/org/from-env")
	_, repos, err := parseDashboardArgs([]string{"oci://ghcr.io/org/from-arg"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repos) != 1 || repos[0] != "ghcr.io/org/from-arg" {
		t.Errorf("expected the OCI arg to override the env var, got %v", repos)
	}
}

func TestParseDashboardArgs_EmptyOCIRef(t *testing.T) {
	_, _, err := parseDashboardArgs([]string{"oci://"})
	if err == nil {
		t.Fatal("expected an error for an empty OCI reference")
	}
	if !strings.Contains(err.Error(), "empty OCI reference") {
		t.Errorf("expected 'empty OCI reference' error, got: %v", err)
	}
}

// TestLocalRootHasBundle pins the guard that keeps the fleet's recursive walk
// off a directory that holds no contracts at all: dir defaults to the working
// directory, which may be $HOME.
func TestLocalRootHasBundle(t *testing.T) {
	root := t.TempDir()
	writeBundle(t, root, "root-svc")

	nested := t.TempDir()
	writeBundle(t, filepath.Join(nested, "services", "orders"), "orders")

	// Only hidden, vendored and non-directory entries: nothing to walk.
	noisy := t.TempDir()
	writeBundle(t, filepath.Join(noisy, ".hidden"), "hidden")
	writeBundle(t, filepath.Join(noisy, "node_modules"), "vendored")
	writeBundle(t, filepath.Join(noisy, "vendor"), "vendored")
	if err := os.WriteFile(filepath.Join(noisy, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A subdirectory with no contract of its own.
	bare := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bare, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		dir  string
		want bool
	}{
		{"unnamed directory", "", false},
		{"contract in the root", root, true},
		{"contract one level down", filepath.Join(nested, "services"), true},
		{"only hidden and vendored contracts", noisy, false},
		{"no contract anywhere", bare, false},
		{"unreadable directory", filepath.Join(root, "nonexistent"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := localRootHasBundle(tc.dir); got != tc.want {
				t.Errorf("localRootHasBundle(%q) = %v, want %v", tc.dir, got, tc.want)
			}
		})
	}
}

// TestCacheHasEntries covers the three answers: an explicit cache with content,
// an explicit cache that is absent, and the default location derived from the
// home directory — including a host with no home to derive it from.
func TestCacheHasEntries(t *testing.T) {
	filled := t.TempDir()
	if err := os.MkdirAll(filepath.Join(filled, "_v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := cacheHasEntries(filled); !got {
		t.Error("a cache directory with entries must count as a source")
	}
	if got := cacheHasEntries(filepath.Join(filled, "nonexistent")); got {
		t.Error("an absent cache directory must not count as a source")
	}

	t.Run("default location", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		if got := cacheHasEntries(""); got {
			t.Error("an empty default cache must not count as a source")
		}
		if err := os.MkdirAll(oci.CacheDirFor(home), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(oci.CacheDirFor(home), "_v2"), 0o755); err != nil {
			t.Fatal(err)
		}
		if got := cacheHasEntries(""); !got {
			t.Error("the default cache with entries must count as a source")
		}
	})

	t.Run("no home directory", func(t *testing.T) {
		t.Setenv("HOME", "")
		if got := cacheHasEntries(""); got {
			t.Error("a host with no resolvable home has no default cache")
		}
	})
}
