package oci

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCachePath_TraversalBlocked(t *testing.T) {
	cacheDir := t.TempDir()
	store := &CachedStore{cacheDir: cacheDir}

	ref := "ghcr.io/../../../etc/passwd"
	got := store.cachePath(ref)

	rel, err := filepath.Rel(cacheDir, got)
	if err != nil {
		t.Fatalf("filepath.Rel error: %v", err)
	}
	if strings.HasPrefix(rel, "..") {
		t.Errorf("cachePath escaped cache directory: %s (rel=%s)", got, rel)
	}
}

func TestCachePath_NormalRef(t *testing.T) {
	cacheDir := t.TempDir()
	store := &CachedStore{cacheDir: cacheDir}

	ref := "ghcr.io/acme/svc:1.0.0"
	got := store.cachePath(ref)

	rel, err := filepath.Rel(cacheDir, got)
	if err != nil {
		t.Fatalf("filepath.Rel error: %v", err)
	}
	if strings.HasPrefix(rel, "..") {
		t.Errorf("normal ref should stay inside cache: %s (rel=%s)", got, rel)
	}
	if !strings.HasSuffix(got, "bundle.tar.gz") {
		t.Errorf("expected bundle.tar.gz suffix, got %s", got)
	}
}
