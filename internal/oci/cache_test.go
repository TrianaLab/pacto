package oci_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/trianalab/pacto/internal/oci"
	"github.com/trianalab/pacto/pkg/contract"
)

type countingStore struct {
	pullCount      atomic.Int32
	listTagsCount  atomic.Int32
	pullErr        error
	listTagsErr    error
	listTagsResult []string
	bundle         *contract.Bundle
}

func (s *countingStore) Push(context.Context, string, *contract.Bundle) (string, error) {
	return "", nil
}

func (s *countingStore) Resolve(context.Context, string) (string, error) {
	return "sha256:abc123", nil
}

func (s *countingStore) Pull(_ context.Context, _ string) (*contract.Bundle, error) {
	s.pullCount.Add(1)
	if s.pullErr != nil {
		return nil, s.pullErr
	}
	return s.bundle, nil
}

func (s *countingStore) ListTags(_ context.Context, _ string) ([]string, error) {
	s.listTagsCount.Add(1)
	if s.listTagsErr != nil {
		return nil, s.listTagsErr
	}
	return s.listTagsResult, nil
}

func newCachedStoreWithTempDir(t *testing.T) (*oci.CachedStore, *countingStore) {
	t.Helper()
	cacheDir := t.TempDir()
	old := oci.SetUserHomeDirFn(func() (string, error) { return cacheDir, nil })
	t.Cleanup(func() { oci.SetUserHomeDirFn(old) })
	inner := &countingStore{bundle: newTestBundle()}
	return oci.NewCachedStore(inner), inner
}

func TestCachedStore_Pull_CachesOnDisk(t *testing.T) {
	store, inner := newCachedStoreWithTempDir(t)
	ctx := context.Background()
	ref := "ghcr.io/test/repo:1.0.0"

	b1, err := store.Pull(ctx, ref)
	if err != nil {
		t.Fatalf("first Pull() error: %v", err)
	}
	if b1.Contract.Service.Name != "test-svc" {
		t.Errorf("got name %q, want test-svc", b1.Contract.Service.Name)
	}
	if inner.pullCount.Load() != 1 {
		t.Fatalf("expected 1 inner pull, got %d", inner.pullCount.Load())
	}

	b2, err := store.Pull(ctx, ref)
	if err != nil {
		t.Fatalf("second Pull() error: %v", err)
	}
	if b2.Contract.Service.Name != "test-svc" {
		t.Errorf("got name %q, want test-svc", b2.Contract.Service.Name)
	}
	if inner.pullCount.Load() != 1 {
		t.Fatalf("expected 1 inner pull after cache hit, got %d", inner.pullCount.Load())
	}
}

func TestCachedStore_Pull_InMemoryCacheServesRepeatedCalls(t *testing.T) {
	store, inner := newCachedStoreWithTempDir(t)
	ctx := context.Background()
	ref := "ghcr.io/test/repo:1.0.0"

	// First pull: hits inner.
	if _, err := store.Pull(ctx, ref); err != nil {
		t.Fatalf("Pull() error: %v", err)
	}

	// Second and third: served from in-memory cache (no inner, no disk I/O).
	for i := range 2 {
		b, err := store.Pull(ctx, ref)
		if err != nil {
			t.Fatalf("Pull() #%d error: %v", i+2, err)
		}
		if b.Contract.Service.Name != "test-svc" {
			t.Errorf("Pull() #%d: got name %q, want test-svc", i+2, b.Contract.Service.Name)
		}
	}

	if inner.pullCount.Load() != 1 {
		t.Errorf("expected 1 inner pull (in-memory cache), got %d", inner.pullCount.Load())
	}
}

func TestCachedStore_Pull_DiskCacheServesNewInstance(t *testing.T) {
	cacheDir := t.TempDir()
	old := oci.SetUserHomeDirFn(func() (string, error) { return cacheDir, nil })
	t.Cleanup(func() { oci.SetUserHomeDirFn(old) })

	ref := "ghcr.io/test/repo:1.0.0"
	ctx := context.Background()

	// First store: populate disk cache.
	inner1 := &countingStore{bundle: newTestBundle()}
	store1 := oci.NewCachedStore(inner1)
	if _, err := store1.Pull(ctx, ref); err != nil {
		t.Fatalf("store1 Pull() error: %v", err)
	}
	if inner1.pullCount.Load() != 1 {
		t.Fatalf("expected 1 inner pull on store1, got %d", inner1.pullCount.Load())
	}

	// Second store (simulates new process): fresh in-memory cache, same disk.
	inner2 := &countingStore{bundle: newTestBundle()}
	store2 := oci.NewCachedStore(inner2)
	b, err := store2.Pull(ctx, ref)
	if err != nil {
		t.Fatalf("store2 Pull() error: %v", err)
	}
	if b.Contract.Service.Name != "test-svc" {
		t.Errorf("got name %q, want test-svc", b.Contract.Service.Name)
	}
	if inner2.pullCount.Load() != 0 {
		t.Errorf("expected 0 inner pulls on store2 (disk hit), got %d", inner2.pullCount.Load())
	}
}

func TestCachedStore_Pull_DifferentRefsMissCache(t *testing.T) {
	store, inner := newCachedStoreWithTempDir(t)
	ctx := context.Background()

	if _, err := store.Pull(ctx, "ghcr.io/test/a:1.0.0"); err != nil {
		t.Fatalf("Pull(a) error: %v", err)
	}
	if _, err := store.Pull(ctx, "ghcr.io/test/b:1.0.0"); err != nil {
		t.Fatalf("Pull(b) error: %v", err)
	}

	if inner.pullCount.Load() != 2 {
		t.Errorf("expected 2 inner pulls for different refs, got %d", inner.pullCount.Load())
	}
}

func TestCachedStore_Pull_InnerError(t *testing.T) {
	store, inner := newCachedStoreWithTempDir(t)
	inner.pullErr = errors.New("registry unreachable")

	_, err := store.Pull(context.Background(), "ghcr.io/test/repo:1.0.0")
	if err == nil {
		t.Fatal("expected error from inner Pull")
	}
	if !strings.Contains(err.Error(), "registry unreachable") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCachedStore_Pull_CorruptGzipFallsBack(t *testing.T) {
	cacheDir := t.TempDir()
	old := oci.SetUserHomeDirFn(func() (string, error) { return cacheDir, nil })
	t.Cleanup(func() { oci.SetUserHomeDirFn(old) })

	ref := "ghcr.io/test/corrupt:1.0.0"
	ctx := context.Background()

	// Store 1: populate disk cache.
	inner1 := &countingStore{bundle: newTestBundle()}
	store1 := oci.NewCachedStore(inner1)
	if _, err := store1.Pull(ctx, ref); err != nil {
		t.Fatalf("initial Pull() error: %v", err)
	}

	// Corrupt the cached file with invalid gzip data.
	cachePath := filepath.Join(cacheDir, ".cache", "pacto", "oci",
		strings.ReplaceAll(ref, ":", "/"), "bundle.tar.gz")
	if err := os.WriteFile(cachePath, []byte("not gzip"), 0644); err != nil {
		t.Fatalf("failed to corrupt cache: %v", err)
	}

	// Store 2 (fresh in-memory cache): should fall back to inner Pull.
	inner2 := &countingStore{bundle: newTestBundle()}
	store2 := oci.NewCachedStore(inner2)
	b, err := store2.Pull(ctx, ref)
	if err != nil {
		t.Fatalf("Pull() after corrupt cache error: %v", err)
	}
	if b.Contract.Service.Name != "test-svc" {
		t.Errorf("got name %q, want test-svc", b.Contract.Service.Name)
	}
	if inner2.pullCount.Load() != 1 {
		t.Errorf("expected 1 inner pull on store2, got %d", inner2.pullCount.Load())
	}
}

func TestCachedStore_Pull_SaveErrorIgnored(t *testing.T) {
	cacheDir := t.TempDir()
	old := oci.SetUserHomeDirFn(func() (string, error) { return cacheDir, nil })
	defer oci.SetUserHomeDirFn(old)

	inner := &countingStore{bundle: newTestBundle()}
	store := oci.NewCachedStore(inner)

	// Make cache dir read-only so save fails.
	ociDir := filepath.Join(cacheDir, ".cache", "pacto", "oci")
	if err := os.MkdirAll(ociDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ociDir, 0444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ociDir, 0755) })

	// Pull should succeed even though save fails.
	b, err := store.Pull(context.Background(), "ghcr.io/test/repo:1.0.0")
	if err != nil {
		t.Fatalf("Pull() error: %v", err)
	}
	if b.Contract.Service.Name != "test-svc" {
		t.Errorf("got name %q, want test-svc", b.Contract.Service.Name)
	}
}

func TestCachedStore_Push_DelegatesToInner(t *testing.T) {
	store := oci.NewCachedStore(&countingStore{bundle: newTestBundle()})

	digest, err := store.Push(context.Background(), "ghcr.io/test/repo:1.0.0", newTestBundle())
	if err != nil {
		t.Fatalf("Push() error: %v", err)
	}
	if digest != "" {
		t.Errorf("expected empty digest from mock, got %q", digest)
	}
}

func TestCachedStore_Resolve_DelegatesToInner(t *testing.T) {
	store := oci.NewCachedStore(&countingStore{bundle: newTestBundle()})

	digest, err := store.Resolve(context.Background(), "ghcr.io/test/repo:1.0.0")
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if digest != "sha256:abc123" {
		t.Errorf("expected sha256:abc123, got %q", digest)
	}
}

func TestCachedStore_DisableCache(t *testing.T) {
	store, inner := newCachedStoreWithTempDir(t)
	ctx := context.Background()
	ref := "ghcr.io/test/repo:1.0.0"

	// Populate cache.
	if _, err := store.Pull(ctx, ref); err != nil {
		t.Fatalf("Pull() error: %v", err)
	}

	store.DisableCache()

	// After disabling, should always hit inner.
	if _, err := store.Pull(ctx, ref); err != nil {
		t.Fatalf("Pull() error: %v", err)
	}
	if inner.pullCount.Load() != 2 {
		t.Errorf("expected 2 inner pulls after disable, got %d", inner.pullCount.Load())
	}
}

func TestCachedStore_DisabledWhenHomeDirFails(t *testing.T) {
	old := oci.SetUserHomeDirFn(func() (string, error) {
		return "", errors.New("no home")
	})
	defer oci.SetUserHomeDirFn(old)

	inner := &countingStore{bundle: newTestBundle()}
	store := oci.NewCachedStore(inner)
	ctx := context.Background()

	if _, err := store.Pull(ctx, "ghcr.io/test/repo:1.0.0"); err != nil {
		t.Fatalf("Pull() error: %v", err)
	}
	// Second pull should hit in-memory cache even without disk caching.
	if _, err := store.Pull(ctx, "ghcr.io/test/repo:1.0.0"); err != nil {
		t.Fatalf("Pull() error: %v", err)
	}
	if inner.pullCount.Load() != 1 {
		t.Errorf("expected 1 inner pull (in-memory cache), got %d", inner.pullCount.Load())
	}
}

func TestCachedStore_Pull_CorruptTarFallsBack(t *testing.T) {
	cacheDir := t.TempDir()
	old := oci.SetUserHomeDirFn(func() (string, error) { return cacheDir, nil })
	t.Cleanup(func() { oci.SetUserHomeDirFn(old) })

	ref := "ghcr.io/test/badtar:1.0.0"
	ctx := context.Background()

	// Store 1: populate disk cache.
	inner1 := &countingStore{bundle: newTestBundle()}
	store1 := oci.NewCachedStore(inner1)
	if _, err := store1.Pull(ctx, ref); err != nil {
		t.Fatalf("initial Pull() error: %v", err)
	}

	// Overwrite with valid gzip but invalid tar content.
	cachePath := filepath.Join(cacheDir, ".cache", "pacto", "oci",
		strings.ReplaceAll(ref, ":", "/"), "bundle.tar.gz")
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	_, _ = gw.Write([]byte("not a tar"))
	_ = gw.Close()
	if err := os.WriteFile(cachePath, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}

	// Store 2 (fresh in-memory cache): should fall back to inner Pull.
	inner2 := &countingStore{bundle: newTestBundle()}
	store2 := oci.NewCachedStore(inner2)
	b, err := store2.Pull(ctx, ref)
	if err != nil {
		t.Fatalf("Pull() after corrupt tar error: %v", err)
	}
	if b.Contract.Service.Name != "test-svc" {
		t.Errorf("got name %q, want test-svc", b.Contract.Service.Name)
	}
	if inner2.pullCount.Load() != 1 {
		t.Errorf("expected 1 inner pull on store2, got %d", inner2.pullCount.Load())
	}
}

func TestCachedStore_Pull_MissingPactoYamlFallsBack(t *testing.T) {
	cacheDir := t.TempDir()
	old := oci.SetUserHomeDirFn(func() (string, error) { return cacheDir, nil })
	t.Cleanup(func() { oci.SetUserHomeDirFn(old) })

	ref := "ghcr.io/test/nopacto:1.0.0"
	ctx := context.Background()

	// Store 1: populate disk cache.
	inner1 := &countingStore{bundle: newTestBundle()}
	store1 := oci.NewCachedStore(inner1)
	if _, err := store1.Pull(ctx, ref); err != nil {
		t.Fatalf("initial Pull() error: %v", err)
	}

	// Overwrite cache with valid gzip+tar but no pacto.yaml.
	cachePath := filepath.Join(cacheDir, ".cache", "pacto", "oci",
		strings.ReplaceAll(ref, ":", "/"), "bundle.tar.gz")
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	data := []byte("hello")
	_ = tw.WriteHeader(&tar.Header{Name: "other.txt", Size: int64(len(data)), Mode: 0644})
	_, _ = tw.Write(data)
	_ = tw.Close()
	_ = gw.Close()
	if err := os.WriteFile(cachePath, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}

	// Store 2 (fresh in-memory cache): should fall back to inner Pull.
	inner2 := &countingStore{bundle: newTestBundle()}
	store2 := oci.NewCachedStore(inner2)
	b, err := store2.Pull(ctx, ref)
	if err != nil {
		t.Fatalf("Pull() after missing pacto.yaml error: %v", err)
	}
	if b.Contract.Service.Name != "test-svc" {
		t.Errorf("got name %q, want test-svc", b.Contract.Service.Name)
	}
	if inner2.pullCount.Load() != 1 {
		t.Errorf("expected 1 inner pull on store2, got %d", inner2.pullCount.Load())
	}
}

func TestCachedStore_Pull_InvalidPactoYamlFallsBack(t *testing.T) {
	cacheDir := t.TempDir()
	old := oci.SetUserHomeDirFn(func() (string, error) { return cacheDir, nil })
	t.Cleanup(func() { oci.SetUserHomeDirFn(old) })

	ref := "ghcr.io/test/badyaml:1.0.0"
	ctx := context.Background()

	// Store 1: populate disk cache.
	inner1 := &countingStore{bundle: newTestBundle()}
	store1 := oci.NewCachedStore(inner1)
	if _, err := store1.Pull(ctx, ref); err != nil {
		t.Fatalf("initial Pull() error: %v", err)
	}

	// Overwrite with valid gzip+tar containing invalid pacto.yaml.
	cachePath := filepath.Join(cacheDir, ".cache", "pacto", "oci",
		strings.ReplaceAll(ref, ":", "/"), "bundle.tar.gz")
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	data := []byte("[[[invalid yaml")
	_ = tw.WriteHeader(&tar.Header{Name: "pacto.yaml", Size: int64(len(data)), Mode: 0644})
	_, _ = tw.Write(data)
	_ = tw.Close()
	_ = gw.Close()
	if err := os.WriteFile(cachePath, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}

	// Store 2 (fresh in-memory cache): should fall back to inner Pull.
	inner2 := &countingStore{bundle: newTestBundle()}
	store2 := oci.NewCachedStore(inner2)
	b, err := store2.Pull(ctx, ref)
	if err != nil {
		t.Fatalf("Pull() after invalid yaml error: %v", err)
	}
	if b.Contract.Service.Name != "test-svc" {
		t.Errorf("got name %q, want test-svc", b.Contract.Service.Name)
	}
	if inner2.pullCount.Load() != 1 {
		t.Errorf("expected 1 inner pull on store2, got %d", inner2.pullCount.Load())
	}
}

func TestCachedStore_Pull_ReadOnlyCacheDirIgnored(t *testing.T) {
	cacheDir := t.TempDir()
	old := oci.SetUserHomeDirFn(func() (string, error) { return cacheDir, nil })
	defer oci.SetUserHomeDirFn(old)

	inner := &countingStore{bundle: newTestBundle()}
	store := oci.NewCachedStore(inner)

	// Create cache dir structure and make the ref-specific dir read-only.
	refDir := filepath.Join(cacheDir, ".cache", "pacto", "oci", "ghcr.io", "test", "readonly", "1.0.0")
	if err := os.MkdirAll(refDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(refDir, 0444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(refDir, 0755) })

	// Pull should succeed even though file creation fails.
	b, err := store.Pull(context.Background(), "ghcr.io/test/readonly:1.0.0")
	if err != nil {
		t.Fatalf("Pull() error: %v", err)
	}
	if b.Contract.Service.Name != "test-svc" {
		t.Errorf("got name %q, want test-svc", b.Contract.Service.Name)
	}
}

func TestCachedStore_ListTags_CachesInMemory(t *testing.T) {
	inner := &countingStore{
		bundle:         newTestBundle(),
		listTagsResult: []string{"1.0.0", "1.1.0", "2.0.0"},
	}
	store := oci.NewCachedStore(inner)
	ctx := context.Background()
	repo := "ghcr.io/test/repo"

	tags1, err := store.ListTags(ctx, repo)
	if err != nil {
		t.Fatalf("first ListTags() error: %v", err)
	}
	if len(tags1) != 3 {
		t.Fatalf("expected 3 tags, got %d", len(tags1))
	}

	tags2, err := store.ListTags(ctx, repo)
	if err != nil {
		t.Fatalf("second ListTags() error: %v", err)
	}
	if len(tags2) != 3 {
		t.Fatalf("expected 3 tags, got %d", len(tags2))
	}

	if inner.listTagsCount.Load() != 1 {
		t.Errorf("expected 1 inner ListTags call, got %d", inner.listTagsCount.Load())
	}
}

func TestCachedStore_ListTags_DifferentReposMissCache(t *testing.T) {
	inner := &countingStore{
		bundle:         newTestBundle(),
		listTagsResult: []string{"1.0.0"},
	}
	store := oci.NewCachedStore(inner)
	ctx := context.Background()

	if _, err := store.ListTags(ctx, "ghcr.io/test/a"); err != nil {
		t.Fatalf("ListTags(a) error: %v", err)
	}
	if _, err := store.ListTags(ctx, "ghcr.io/test/b"); err != nil {
		t.Fatalf("ListTags(b) error: %v", err)
	}

	if inner.listTagsCount.Load() != 2 {
		t.Errorf("expected 2 inner ListTags calls for different repos, got %d", inner.listTagsCount.Load())
	}
}

func TestCachedStore_ListTags_ErrorNotCached(t *testing.T) {
	inner := &countingStore{
		bundle:      newTestBundle(),
		listTagsErr: errors.New("registry error"),
	}
	store := oci.NewCachedStore(inner)
	ctx := context.Background()
	repo := "ghcr.io/test/repo"

	if _, err := store.ListTags(ctx, repo); err == nil {
		t.Fatal("expected error from ListTags")
	}

	// Clear error so second call succeeds.
	inner.listTagsErr = nil
	inner.listTagsResult = []string{"1.0.0"}

	tags, err := store.ListTags(ctx, repo)
	if err != nil {
		t.Fatalf("second ListTags() error: %v", err)
	}
	if len(tags) != 1 {
		t.Errorf("expected 1 tag, got %d", len(tags))
	}
	if inner.listTagsCount.Load() != 2 {
		t.Errorf("expected 2 inner calls (error not cached), got %d", inner.listTagsCount.Load())
	}
}

func TestCachedStore_XDGCacheHome(t *testing.T) {
	customCache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", customCache)

	inner := &countingStore{bundle: newTestBundle()}
	store := oci.NewCachedStore(inner)
	ctx := context.Background()
	ref := "ghcr.io/test/xdg:1.0.0"

	if _, err := store.Pull(ctx, ref); err != nil {
		t.Fatalf("Pull() error: %v", err)
	}

	cachePath := filepath.Join(customCache, "pacto", "oci",
		strings.ReplaceAll(ref, ":", "/"), "bundle.tar.gz")
	if _, err := os.Stat(cachePath); err != nil {
		t.Errorf("expected cache file at %s: %v", cachePath, err)
	}

	// Second pull should hit cache.
	if _, err := store.Pull(ctx, ref); err != nil {
		t.Fatalf("Pull() error: %v", err)
	}
	if inner.pullCount.Load() != 1 {
		t.Errorf("expected 1 inner pull with XDG cache, got %d", inner.pullCount.Load())
	}
}

func TestCachedStore_Pull_AllFilesSurviveDiskCache(t *testing.T) {
	cacheDir := t.TempDir()
	old := oci.SetUserHomeDirFn(func() (string, error) { return cacheDir, nil })
	t.Cleanup(func() { oci.SetUserHomeDirFn(old) })

	ref := "ghcr.io/test/multifile:1.0.0"
	ctx := context.Background()

	// Store 1: populate disk cache with multi-file bundle.
	inner1 := &countingStore{bundle: newTestBundle()}
	store1 := oci.NewCachedStore(inner1)
	if _, err := store1.Pull(ctx, ref); err != nil {
		t.Fatalf("store1 Pull() error: %v", err)
	}

	// Store 2 (simulates new process): fresh in-memory cache, reads from disk.
	inner2 := &countingStore{bundle: newTestBundle()}
	store2 := oci.NewCachedStore(inner2)
	b, err := store2.Pull(ctx, ref)
	if err != nil {
		t.Fatalf("store2 Pull() error: %v", err)
	}
	if inner2.pullCount.Load() != 0 {
		t.Errorf("expected 0 inner pulls on store2 (disk hit), got %d", inner2.pullCount.Load())
	}

	// Verify RawYAML is populated from cache.
	if b.RawYAML == nil {
		t.Fatal("expected RawYAML to be populated from disk cache")
	}

	// Verify ALL files survived the disk cache round-trip.
	wantFiles := map[string]string{
		"pacto.yaml":      "test-svc",
		"openapi.yaml":    "openapi:",
		"docs/README.md":  "# Test Service",
		"docs/runbook.md": "# Runbook",
	}
	for path, wantSubstr := range wantFiles {
		data, err := fs.ReadFile(b.FS, path)
		if err != nil {
			t.Errorf("ReadFile(%s) error: %v — file not preserved in disk cache", path, err)
			continue
		}
		if !strings.Contains(string(data), wantSubstr) {
			t.Errorf("%s content = %q, want it to contain %q", path, string(data), wantSubstr)
		}
	}
}
