package dashboard

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func writeBundleTarGzFile(t *testing.T, path string, pactoYAML string) {
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

func TestCacheSource_ScansDirectory(t *testing.T) {
	root := t.TempDir()

	// Create two services with multiple versions.
	writeBundleTarGzFile(t,
		filepath.Join(root, "ghcr.io/org/api/1.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: api
  version: 1.0.0
`)
	writeBundleTarGzFile(t,
		filepath.Join(root, "ghcr.io/org/api/2.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: api
  version: 2.0.0
`)
	writeBundleTarGzFile(t,
		filepath.Join(root, "ghcr.io/org/worker/1.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: worker
  version: 1.0.0
`)

	src := NewCacheSource(root)

	if src.ServiceCount() != 2 {
		t.Fatalf("expected 2 services, got %d", src.ServiceCount())
	}
	if src.VersionCount() != 3 {
		t.Fatalf("expected 3 versions, got %d", src.VersionCount())
	}

	// List services.
	ctx := context.Background()
	services, err := src.ListServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(services))
	}
	if services[0].Name != "api" {
		t.Errorf("expected first service 'api', got %q", services[0].Name)
	}
	if services[1].Name != "worker" {
		t.Errorf("expected second service 'worker', got %q", services[1].Name)
	}

	// Get service details.
	details, err := src.GetService(ctx, "api")
	if err != nil {
		t.Fatal(err)
	}
	if details.Version != "2.0.0" {
		t.Errorf("expected latest version '2.0.0', got %q", details.Version)
	}

	// Get versions.
	versions, err := src.GetVersions(ctx, "api")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}
}

func TestCacheSource_EmptyDirectory(t *testing.T) {
	root := t.TempDir()

	src := NewCacheSource(root)
	if src.ServiceCount() != 0 {
		t.Fatalf("expected 0 services, got %d", src.ServiceCount())
	}

	ctx := context.Background()
	services, err := src.ListServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 0 {
		t.Fatalf("expected 0 services, got %d", len(services))
	}
}

func TestCacheSource_NonExistentDirectory(t *testing.T) {
	src := NewCacheSource("/nonexistent/path")
	if src.ServiceCount() != 0 {
		t.Fatalf("expected 0 services, got %d", src.ServiceCount())
	}
}

func TestCacheSource_ImplementsDataSource(t *testing.T) {
	root := t.TempDir()
	src := NewCacheSource(root)
	var _ DataSource = src
}

func TestSemverDescending_BothValid(t *testing.T) {
	// 2.0.0 should sort before 1.0.0 (descending)
	if !semverDescending("2.0.0", "1.0.0") {
		t.Error("expected 2.0.0 before 1.0.0 in descending order")
	}
	if semverDescending("1.0.0", "2.0.0") {
		t.Error("expected 1.0.0 NOT before 2.0.0 in descending order")
	}
}

func TestSemverDescending_Equal(t *testing.T) {
	if semverDescending("1.0.0", "1.0.0") {
		t.Error("expected equal versions not to be ordered")
	}
}

func TestSemverDescending_BothInvalid(t *testing.T) {
	// Fallback to reverse lexicographic
	if !semverDescending("beta", "alpha") {
		t.Error("expected 'beta' before 'alpha' in reverse lex order")
	}
	if semverDescending("alpha", "beta") {
		t.Error("expected 'alpha' NOT before 'beta' in reverse lex order")
	}
}

func TestSemverDescending_ValidBeforeInvalid(t *testing.T) {
	// Valid semver should sort before non-semver
	if !semverDescending("1.0.0", "latest") {
		t.Error("expected valid semver '1.0.0' before non-semver 'latest'")
	}
	if semverDescending("latest", "1.0.0") {
		t.Error("expected non-semver 'latest' NOT before valid semver '1.0.0'")
	}
}

func TestSemverDescending_PreRelease(t *testing.T) {
	// 1.0.0 should sort before 1.0.0-alpha (prerelease is lower)
	if !semverDescending("1.0.0", "1.0.0-alpha") {
		t.Error("expected 1.0.0 before 1.0.0-alpha")
	}
}

func TestLatestTag_Empty(t *testing.T) {
	result := latestTag(nil)
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestLatestTag_SingleTag(t *testing.T) {
	result := latestTag([]string{"1.0.0"})
	if result != "1.0.0" {
		t.Errorf("expected '1.0.0', got %q", result)
	}
}

func TestLatestTag_MultipleTags(t *testing.T) {
	result := latestTag([]string{"1.0.0", "3.0.0", "2.0.0"})
	if result != "3.0.0" {
		t.Errorf("expected '3.0.0', got %q", result)
	}
}

func TestLatestTag_MixedSemverAndNonSemver(t *testing.T) {
	result := latestTag([]string{"latest", "1.0.0", "2.0.0"})
	if result != "2.0.0" {
		t.Errorf("expected '2.0.0' (valid semver wins), got %q", result)
	}
}

func TestCacheSource_GetDiff(t *testing.T) {
	root := t.TempDir()

	writeBundleTarGzFile(t,
		filepath.Join(root, "ghcr.io/org/api/1.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: api
  version: 1.0.0
`)
	writeBundleTarGzFile(t,
		filepath.Join(root, "ghcr.io/org/api/2.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: api
  version: 2.0.0
`)

	src := NewCacheSource(root)

	ctx := context.Background()
	a := Ref{Name: "api", Version: "1.0.0"}
	b := Ref{Name: "api", Version: "2.0.0"}

	result, err := src.GetDiff(ctx, a, b)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected non-nil diff result")
	}
	if result.From.Version != "1.0.0" {
		t.Errorf("expected from version '1.0.0', got %q", result.From.Version)
	}
	if result.To.Version != "2.0.0" {
		t.Errorf("expected to version '2.0.0', got %q", result.To.Version)
	}
}

func TestCacheSource_GetDiff_ServiceNotFound(t *testing.T) {
	root := t.TempDir()
	src := NewCacheSource(root)

	ctx := context.Background()
	_, err := src.GetDiff(ctx, Ref{Name: "missing", Version: "1.0.0"}, Ref{Name: "missing", Version: "2.0.0"})
	if err == nil {
		t.Fatal("expected error for missing service")
	}
}

func TestCacheSource_GetDiff_VersionNotFound(t *testing.T) {
	root := t.TempDir()
	writeBundleTarGzFile(t,
		filepath.Join(root, "ghcr.io/org/api/1.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: api
  version: 1.0.0
`)

	src := NewCacheSource(root)

	ctx := context.Background()
	_, err := src.GetDiff(ctx, Ref{Name: "api", Version: "1.0.0"}, Ref{Name: "api", Version: "9.9.9"})
	if err == nil {
		t.Fatal("expected error for missing version")
	}
}

func TestCacheSource_GetService_NotFound(t *testing.T) {
	root := t.TempDir()
	src := NewCacheSource(root)

	_, err := src.GetService(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent service")
	}
}

func TestCacheSource_GetVersions_NotFound(t *testing.T) {
	root := t.TempDir()
	src := NewCacheSource(root)

	_, err := src.GetVersions(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent service")
	}
}

func TestCacheSource_VersionsSortedDescending(t *testing.T) {
	root := t.TempDir()

	writeBundleTarGzFile(t,
		filepath.Join(root, "ghcr.io/org/api/1.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: api
  version: 1.0.0
`)
	writeBundleTarGzFile(t,
		filepath.Join(root, "ghcr.io/org/api/3.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: api
  version: 3.0.0
`)
	writeBundleTarGzFile(t,
		filepath.Join(root, "ghcr.io/org/api/2.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: api
  version: 2.0.0
`)

	src := NewCacheSource(root)

	versions, err := src.GetVersions(context.Background(), "api")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}
	// Should be descending: 3.0.0, 2.0.0, 1.0.0
	if versions[0].Version != "3.0.0" {
		t.Errorf("expected first version '3.0.0', got %q", versions[0].Version)
	}
	if versions[1].Version != "2.0.0" {
		t.Errorf("expected second version '2.0.0', got %q", versions[1].Version)
	}
	if versions[2].Version != "1.0.0" {
		t.Errorf("expected third version '1.0.0', got %q", versions[2].Version)
	}
}

func TestCacheSource_NonTarGzFileSkipped(t *testing.T) {
	root := t.TempDir()

	// Write a valid bundle.
	writeBundleTarGzFile(t,
		filepath.Join(root, "ghcr.io/org/api/1.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: api
  version: 1.0.0
`)

	// Write a non-tar.gz file that should be skipped.
	if err := os.MkdirAll(filepath.Join(root, "ghcr.io/org/api/1.0.0"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ghcr.io/org/api/1.0.0/random.txt"), []byte("not a bundle"), 0644); err != nil {
		t.Fatal(err)
	}

	src := NewCacheSource(root)
	if src.ServiceCount() != 1 {
		t.Fatalf("expected 1 service, got %d", src.ServiceCount())
	}
}

func TestCacheSource_ScanSkipsShallowPaths(t *testing.T) {
	root := t.TempDir()

	// Create a bundle.tar.gz at a path with only one component (too shallow).
	// rel = "shallow/bundle.tar.gz" -> parts = ["shallow"] -> len < 2, skipped.
	writeBundleTarGzFile(t,
		filepath.Join(root, "shallow/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: shallow-svc
  version: 1.0.0
`)

	src := NewCacheSource(root)
	if src.ServiceCount() != 0 {
		t.Fatalf("expected 0 services (shallow path skipped), got %d", src.ServiceCount())
	}
}

func TestCacheSource_ScanSkipsCorruptBundles(t *testing.T) {
	root := t.TempDir()

	// Create a bad gzip file.
	badPath := filepath.Join(root, "ghcr.io/org/bad/1.0.0/bundle.tar.gz")
	if err := os.MkdirAll(filepath.Dir(badPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(badPath, []byte("not gzip data"), 0644); err != nil {
		t.Fatal(err)
	}

	src := NewCacheSource(root)
	if src.ServiceCount() != 0 {
		t.Fatalf("expected 0 services (corrupt bundle skipped), got %d", src.ServiceCount())
	}
}

func TestCacheSource_ListServices_SkipsServiceWithNoVersions(t *testing.T) {
	// Manually construct a CacheSource with a service that has no versions.
	// This tests the `if latest == nil { continue }` path in ListServices.
	src := &CacheSource{
		services: map[string]*cachedService{
			"empty": {name: "empty", versions: nil},
		},
	}

	ctx := context.Background()
	services, err := src.ListServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 0 {
		t.Fatalf("expected 0 services (no versions), got %d", len(services))
	}
}

func TestCacheSource_GetService_NoVersions(t *testing.T) {
	// Manually construct a CacheSource with a service that has no versions.
	src := &CacheSource{
		services: map[string]*cachedService{
			"empty": {name: "empty", versions: nil},
		},
	}

	_, err := src.GetService(context.Background(), "empty")
	if err == nil {
		t.Fatal("expected error for service with no versions")
	}
}

func TestCacheSource_GetDiff_FromVersionNotFound(t *testing.T) {
	root := t.TempDir()
	writeBundleTarGzFile(t,
		filepath.Join(root, "ghcr.io/org/api/1.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: api
  version: 1.0.0
`)

	src := NewCacheSource(root)

	ctx := context.Background()
	// "from" version doesn't exist.
	_, err := src.GetDiff(ctx, Ref{Name: "api", Version: "9.9.9"}, Ref{Name: "api", Version: "1.0.0"})
	if err == nil {
		t.Fatal("expected error for missing 'from' version")
	}
}

func TestCacheSource_GetDiff_ToServiceNotFound(t *testing.T) {
	root := t.TempDir()
	writeBundleTarGzFile(t,
		filepath.Join(root, "ghcr.io/org/api/1.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: api
  version: 1.0.0
`)

	src := NewCacheSource(root)

	ctx := context.Background()
	// "to" service doesn't exist.
	_, err := src.GetDiff(ctx, Ref{Name: "api", Version: "1.0.0"}, Ref{Name: "missing", Version: "1.0.0"})
	if err == nil {
		t.Fatal("expected error for missing 'to' service")
	}
}

func TestCacheSource_GetDiff_ToVersionNotFound(t *testing.T) {
	root := t.TempDir()
	writeBundleTarGzFile(t,
		filepath.Join(root, "ghcr.io/org/api/1.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: api
  version: 1.0.0
`)
	writeBundleTarGzFile(t,
		filepath.Join(root, "ghcr.io/org/worker/1.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: worker
  version: 1.0.0
`)

	src := NewCacheSource(root)

	ctx := context.Background()
	// "to" version doesn't exist.
	_, err := src.GetDiff(ctx, Ref{Name: "api", Version: "1.0.0"}, Ref{Name: "worker", Version: "9.9.9"})
	if err == nil {
		t.Fatal("expected error for missing 'to' version")
	}
}

func TestCacheSource_Rescan_PicksUpNewBundles(t *testing.T) {
	root := t.TempDir()

	// Start with one bundle.
	writeBundleTarGzFile(t,
		filepath.Join(root, "ghcr.io/org/api/1.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: api
  version: 1.0.0
`)

	src := NewCacheSource(root)
	if src.ServiceCount() != 1 {
		t.Fatalf("expected 1 service, got %d", src.ServiceCount())
	}
	if src.VersionCount() != 1 {
		t.Fatalf("expected 1 version, got %d", src.VersionCount())
	}

	// Add a new version on disk (simulating a CachedStore write).
	writeBundleTarGzFile(t,
		filepath.Join(root, "ghcr.io/org/api/2.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: api
  version: 2.0.0
`)
	// Also add a new service.
	writeBundleTarGzFile(t,
		filepath.Join(root, "ghcr.io/org/worker/1.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: worker
  version: 1.0.0
`)

	// Before rescan, counts are stale.
	if src.ServiceCount() != 1 {
		t.Fatalf("before rescan: expected 1 service, got %d", src.ServiceCount())
	}

	// Rescan picks up new bundles.
	src.Rescan()
	if src.ServiceCount() != 2 {
		t.Fatalf("after rescan: expected 2 services, got %d", src.ServiceCount())
	}
	if src.VersionCount() != 3 {
		t.Fatalf("after rescan: expected 3 versions, got %d", src.VersionCount())
	}

	// The new version should be visible.
	details, err := src.GetService(context.Background(), "api")
	if err != nil {
		t.Fatal(err)
	}
	if details.Version != "2.0.0" {
		t.Errorf("expected latest version '2.0.0' after rescan, got %q", details.Version)
	}
}

func TestCacheSource_Rescan_EmptyAfterClear(t *testing.T) {
	root := t.TempDir()

	writeBundleTarGzFile(t,
		filepath.Join(root, "ghcr.io/org/api/1.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: api
  version: 1.0.0
`)

	src := NewCacheSource(root)
	if src.ServiceCount() != 1 {
		t.Fatalf("expected 1 service, got %d", src.ServiceCount())
	}

	// Remove all bundles and rescan.
	if err := os.RemoveAll(filepath.Join(root, "ghcr.io")); err != nil {
		t.Fatal(err)
	}

	src.Rescan()
	if src.ServiceCount() != 0 {
		t.Fatalf("expected 0 services after clearing, got %d", src.ServiceCount())
	}
}

func TestFilterValidSemver(t *testing.T) {
	tests := []struct {
		name     string
		tags     []string
		expected []string
	}{
		{
			name:     "mixed valid and invalid",
			tags:     []string{"v1.0.0", "latest", "2.0.0", "abc", "1.5.0"},
			expected: []string{"2.0.0", "1.5.0", "v1.0.0"},
		},
		{
			name:     "all invalid",
			tags:     []string{"latest", "main", "abc"},
			expected: nil,
		},
		{
			name:     "all valid sorted descending",
			tags:     []string{"3.0.0", "1.0.0", "2.0.0"},
			expected: []string{"3.0.0", "2.0.0", "1.0.0"},
		},
		{
			name:     "empty",
			tags:     nil,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterValidSemver(tt.tags)
			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d tags, got %d: %v", len(tt.expected), len(result), result)
			}
			for i, v := range result {
				if v != tt.expected[i] {
					t.Errorf("index %d: expected %q, got %q", i, tt.expected[i], v)
				}
			}
		})
	}
}

func TestLatestTag_IgnoresNonSemver(t *testing.T) {
	// When ALL tags are non-semver, latestTag returns empty string.
	result := latestTag([]string{"latest", "main", "abc"})
	if result != "" {
		t.Errorf("expected empty string for all-non-semver tags, got %q", result)
	}

	// When mixed, returns highest semver.
	result = latestTag([]string{"latest", "1.0.0", "2.0.0", "main"})
	if result != "2.0.0" {
		t.Errorf("expected '2.0.0', got %q", result)
	}
}

func TestCacheSource_CurrentVersionIsHighestSemver(t *testing.T) {
	root := t.TempDir()

	// Write versions in non-sorted order, including a non-semver tag.
	for _, v := range []struct {
		tag, version string
	}{
		{"1.0.0", "1.0.0"},
		{"3.0.0", "3.0.0"},
		{"2.0.0", "2.0.0"},
	} {
		writeBundleTarGzFile(t,
			filepath.Join(root, "ghcr.io/org/svc", v.tag, "bundle.tar.gz"),
			fmt.Sprintf(`pactoVersion: "1.0"
service:
  name: my-svc
  version: %s
`, v.version))
	}

	src := NewCacheSource(root)
	details, err := src.GetService(context.Background(), "my-svc")
	if err != nil {
		t.Fatal(err)
	}
	if details.Version != "3.0.0" {
		t.Errorf("expected current version '3.0.0' (highest semver), got %q", details.Version)
	}

	// Versions should be sorted descending.
	versions, err := src.GetVersions(context.Background(), "my-svc")
	if err != nil {
		t.Fatal(err)
	}
	if versions[0].Version != "3.0.0" {
		t.Errorf("expected first version '3.0.0', got %q", versions[0].Version)
	}
}

func TestCacheSource_LatestVersion_EmptyVersions(t *testing.T) {
	svc := &cachedService{name: "empty", versions: nil}
	latest := svc.latestVersion()
	if latest != nil {
		t.Error("expected nil for empty versions")
	}
}

func TestLoadBundleTarGz_BadGzip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bundle.tar.gz")
	if err := os.WriteFile(path, []byte("not gzip"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := loadBundleTarGz(path)
	if err == nil {
		t.Fatal("expected error for bad gzip file")
	}
}

func TestExtractTar_DirectoryEntry(t *testing.T) {
	// Create a tar with a directory entry and a regular file.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	// Add directory entry.
	_ = tw.WriteHeader(&tar.Header{
		Name:     "subdir/",
		Typeflag: tar.TypeDir,
		Mode:     0755,
	})

	// Add a non-pacto.yaml file.
	data := []byte("some other file content")
	_ = tw.WriteHeader(&tar.Header{
		Name: "subdir/other.txt",
		Size: int64(len(data)),
		Mode: 0644,
	})
	_, _ = tw.Write(data)

	// Add pacto.yaml.
	yamlData := []byte(`pactoVersion: "1.0"`)
	_ = tw.WriteHeader(&tar.Header{
		Name: "pacto.yaml",
		Size: int64(len(yamlData)),
		Mode: 0644,
	})
	_, _ = tw.Write(yamlData)
	_ = tw.Close()

	fsys, err := extractTar(&buf)
	if err != nil {
		t.Fatal(err)
	}

	// Verify directory entry exists.
	_, err = fs.Stat(fsys, "subdir")
	if err != nil {
		t.Errorf("expected subdir directory entry, got error: %v", err)
	}

	// Verify non-pacto file exists.
	content, err := fs.ReadFile(fsys, "subdir/other.txt")
	if err != nil {
		t.Fatalf("expected to read other.txt: %v", err)
	}
	if string(content) != "some other file content" {
		t.Errorf("unexpected content: %q", string(content))
	}
}

func TestExtractTar_TraversalPath(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	data := []byte("evil")
	_ = tw.WriteHeader(&tar.Header{
		Name: "../../../etc/passwd",
		Size: int64(len(data)),
		Mode: 0644,
	})
	_, _ = tw.Write(data)
	_ = tw.Close()

	_, err := extractTar(&buf)
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestLoadBundleTarGz_MissingPactoYAML(t *testing.T) {
	// Create a valid gzip/tar file but without pacto.yaml inside.
	dir := t.TempDir()
	path := filepath.Join(dir, "bundle.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	data := []byte("some content")
	_ = tw.WriteHeader(&tar.Header{Name: "other.txt", Size: int64(len(data)), Mode: 0644})
	_, _ = tw.Write(data)
	_ = tw.Close()
	_ = gw.Close()
	_ = f.Close()

	_, err = loadBundleTarGz(path)
	if err == nil {
		t.Fatal("expected error for tar without pacto.yaml")
	}
}

func TestLoadBundleTarGz_CorruptTarInsideGzip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bundle.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	// Write garbage (not a valid tar) inside gzip.
	_, _ = gw.Write([]byte("this is not a tar stream"))
	_ = gw.Close()
	_ = f.Close()

	_, err = loadBundleTarGz(path)
	if err == nil {
		t.Fatal("expected error for corrupt tar inside gzip")
	}
}

func TestLoadBundleTarGz_NonExistentFile(t *testing.T) {
	_, err := loadBundleTarGz("/nonexistent/path/bundle.tar.gz")
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
}

func TestLoadBundleTarGz_InvalidContract(t *testing.T) {
	// Valid gzip/tar with pacto.yaml that fails contract.Parse.
	dir := t.TempDir()
	path := filepath.Join(dir, "bundle.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	data := []byte("not: valid: contract: [[[")
	_ = tw.WriteHeader(&tar.Header{Name: "pacto.yaml", Size: int64(len(data)), Mode: 0644})
	_, _ = tw.Write(data)
	_ = tw.Close()
	_ = gw.Close()
	_ = f.Close()

	_, err = loadBundleTarGz(path)
	if err == nil {
		t.Fatal("expected error for invalid contract YAML")
	}
}

func TestExtractTar_OversizedFile(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	// Create a file that exceeds maxBundleFileSize (10MB).
	// We set the tar header size large but only write enough to trigger the check.
	data := make([]byte, maxBundleFileSize+1)
	_ = tw.WriteHeader(&tar.Header{
		Name: "big.bin",
		Size: int64(len(data)),
		Mode: 0644,
	})
	_, _ = tw.Write(data)
	_ = tw.Close()

	_, err := extractTar(&buf)
	if err == nil {
		t.Fatal("expected error for oversized file")
	}
}

func TestExtractTar_TotalSizeExceeded(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	// Create multiple files that together exceed maxBundleTotalSize (50MB).
	// Write 6 files of ~9MB each = ~54MB total > 50MB limit.
	fileSize := 9 * 1024 * 1024
	for i := 0; i < 6; i++ {
		data := make([]byte, fileSize)
		_ = tw.WriteHeader(&tar.Header{
			Name: fmt.Sprintf("file%d.bin", i),
			Size: int64(len(data)),
			Mode: 0644,
		})
		_, _ = tw.Write(data)
	}
	_ = tw.Close()

	_, err := extractTar(&buf)
	if err == nil {
		t.Fatal("expected error for total size exceeded")
	}
}

func TestCacheSource_GetVersions_Classification(t *testing.T) {
	root := t.TempDir()

	// v1 and v2 are identical contracts (only version differs) → NON_BREAKING
	writeBundleTarGzFile(t,
		filepath.Join(root, "ghcr.io/org/api/1.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: api
  version: 1.0.0
`)
	writeBundleTarGzFile(t,
		filepath.Join(root, "ghcr.io/org/api/2.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: api
  version: 2.0.0
`)
	// v3 removes an interface → BREAKING
	writeBundleTarGzWithFiles(t,
		filepath.Join(root, "ghcr.io/org/api/2.5.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: api
  version: 2.5.0
interfaces:
  - name: http
    type: rest
    port: 8080
`, nil)
	writeBundleTarGzWithFiles(t,
		filepath.Join(root, "ghcr.io/org/api/3.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: api
  version: 3.0.0
`, nil)

	src := NewCacheSource(root)
	versions, err := src.GetVersions(context.Background(), "api")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 4 {
		t.Fatalf("expected 4 versions, got %d", len(versions))
	}

	// Sorted descending: 3.0.0, 2.5.0, 2.0.0, 1.0.0
	// 3.0.0 vs 2.5.0: interface removed → BREAKING
	if versions[0].Classification != "BREAKING" {
		t.Errorf("expected v3.0.0 classification 'BREAKING', got %q", versions[0].Classification)
	}
	// 2.5.0 vs 2.0.0: interface added → NON_BREAKING
	if versions[1].Classification != "NON_BREAKING" {
		t.Errorf("expected v2.5.0 classification 'NON_BREAKING', got %q", versions[1].Classification)
	}
	// 2.0.0 vs 1.0.0: only version changed → NON_BREAKING
	if versions[2].Classification != "NON_BREAKING" {
		t.Errorf("expected v2.0.0 classification 'NON_BREAKING', got %q", versions[2].Classification)
	}
	// 1.0.0 is the oldest → no classification
	if versions[3].Classification != "" {
		t.Errorf("expected v1.0.0 classification empty (oldest), got %q", versions[3].Classification)
	}
}

func writeBundleTarGzWithFiles(t *testing.T, path string, pactoYAML string, extraFiles map[string][]byte) {
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

	for name, content := range extraFiles {
		_ = tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(content)), Mode: 0644})
		_, _ = tw.Write(content)
	}

	_ = tw.Close()
	_ = gw.Close()
}

func TestExtractTar_SkipsDotEntry(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	// Entry with name "./" should be skipped.
	_ = tw.WriteHeader(&tar.Header{
		Name:     "./",
		Typeflag: tar.TypeDir,
		Mode:     0755,
	})

	data := []byte("content")
	_ = tw.WriteHeader(&tar.Header{
		Name: "file.txt",
		Size: int64(len(data)),
		Mode: 0644,
	})
	_, _ = tw.Write(data)
	_ = tw.Close()

	fsys, err := extractTar(&buf)
	if err != nil {
		t.Fatal(err)
	}
	content, err := fs.ReadFile(fsys, "file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "content" {
		t.Errorf("unexpected content: %q", string(content))
	}
}
