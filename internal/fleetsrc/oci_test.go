package fleetsrc

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// fakeStore is an oci.BundleStore for the source tests.
type fakeStore struct {
	bundles map[string]*contract.Bundle
	pullErr map[string]error
	digest  map[string]string
}

func bundleFor(name string) *contract.Bundle {
	return &contract.Bundle{
		Contract: &contract.Contract{Service: contract.Service{Name: name, Version: "1.0.0"}},
		FS:       fstest.MapFS{},
	}
}

func (f *fakeStore) Push(context.Context, string, *contract.Bundle) (string, error) { return "", nil }
func (f *fakeStore) ListTags(context.Context, string) ([]string, error)             { return nil, nil }
func (f *fakeStore) Pull(_ context.Context, ref string) (*contract.Bundle, error) {
	if e := f.pullErr[ref]; e != nil {
		return nil, e
	}
	if b, ok := f.bundles[ref]; ok {
		return b, nil
	}
	return nil, errors.New("not found")
}
func (f *fakeStore) Resolve(_ context.Context, ref string) (string, error) {
	if d, ok := f.digest[ref]; ok {
		return d, nil
	}
	return "", errors.New("no digest")
}

func TestOCISource_IDKind(t *testing.T) {
	if s := NewOCISource("", nil, nil); s.ID() != "oci" || s.Kind() != "oci" {
		t.Errorf("defaults wrong: id=%q kind=%q", s.ID(), s.Kind())
	}
	if NewOCISource("prod", nil, nil).ID() != "prod" {
		t.Error("custom id not honored")
	}
}

func TestOCISource_Collect(t *testing.T) {
	store := &fakeStore{
		bundles: map[string]*contract.Bundle{
			"ghcr.io/x/a:1.0.0": bundleFor("a"),
			"ghcr.io/x/b:1.0.0": bundleFor("b"),
		},
		pullErr: map[string]error{"ghcr.io/x/missing:1.0.0": errors.New("not found")},
		digest:  map[string]string{"ghcr.io/x/a:1.0.0": "sha256:aaa"}, // b has no digest
	}
	s := NewOCISource("oci", store, []string{"ghcr.io/x/a:1.0.0", "ghcr.io/x/b:1.0.0", "ghcr.io/x/missing:1.0.0"})
	col, err := s.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(col.Revisions) != 2 {
		t.Fatalf("revisions = %d, want 2", len(col.Revisions))
	}
	if col.Revisions[0].Digest != "sha256:aaa" || col.Revisions[0].RequestedRef != "ghcr.io/x/a:1.0.0" {
		t.Errorf("revision a wrong: %+v", col.Revisions[0])
	}
	// A resolved digest pins ResolvedRef to the immutable digest form (not the tag).
	if col.Revisions[0].ResolvedRef != "ghcr.io/x/a@sha256:aaa" {
		t.Errorf("revision a ResolvedRef = %q, want digest-pinned", col.Revisions[0].ResolvedRef)
	}
	if col.Revisions[1].Digest != "" { // b's digest lookup failed -> empty, still a revision
		t.Errorf("revision b digest = %q, want empty", col.Revisions[1].Digest)
	}
	// With no resolvable digest, ResolvedRef stays the mutable tag (honest: impact
	// by canonical key must reject it rather than claim snapshot parity).
	if col.Revisions[1].ResolvedRef != "ghcr.io/x/b:1.0.0" {
		t.Errorf("revision b ResolvedRef = %q, want the mutable tag", col.Revisions[1].ResolvedRef)
	}
	if len(col.Limitations) != 1 || col.Limitations[0].Code != fleet.LimitationSourceRecordInvalid {
		t.Errorf("expected 1 record-invalid limitation, got %+v", col.Limitations)
	}
}

func TestPinRefToDigest(t *testing.T) {
	cases := []struct{ ref, digest, want string }{
		{"ghcr.io/x/a:1.0.0", "sha256:aaa", "ghcr.io/x/a@sha256:aaa"},
		{"oci://ghcr.io/acme/pay:1.0", "sha256:bbb", "oci://ghcr.io/acme/pay@sha256:bbb"},
		{"localhost:5000/acme/pay:1.0", "sha256:ccc", "localhost:5000/acme/pay@sha256:ccc"},
		{"oci://ghcr.io/acme/pay@sha256:old", "sha256:new", "oci://ghcr.io/acme/pay@sha256:new"},
		{"payments", "sha256:ddd", "payments@sha256:ddd"},
	}
	for _, c := range cases {
		if got := pinRefToDigest(c.ref, c.digest); got != c.want {
			t.Errorf("pinRefToDigest(%q,%q) = %q, want %q", c.ref, c.digest, got, c.want)
		}
	}
}

func TestOCISource_Collect_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := NewOCISource("oci", &fakeStore{}, []string{"ghcr.io/x/a:1.0.0"})
	if _, err := s.Collect(ctx); err == nil {
		t.Fatal("expected context error")
	}
}

func TestCacheSource_IDKind(t *testing.T) {
	if s := NewCacheSource("", "", nil); s.ID() != "cache" || s.Kind() != "cache" {
		t.Errorf("defaults wrong: id=%q kind=%q", s.ID(), s.Kind())
	}
}

func TestCacheSource_Collect(t *testing.T) {
	dir := t.TempDir()
	// Valid cached bundle: <dir>/ghcr.io/org/svc/1.0.0/bundle.tar.gz
	mustCacheFile(t, dir, "ghcr.io/org/svc/1.0.0/bundle.tar.gz")
	// A too-shallow bundle.tar.gz is ignored.
	mustCacheFile(t, dir, "bundle.tar.gz")
	// A non-bundle file is ignored.
	mustCacheFile(t, dir, "ghcr.io/org/svc/1.0.0/manifest.json")

	store := &fakeStore{bundles: map[string]*contract.Bundle{"ghcr.io/org/svc:1.0.0": bundleFor("svc")}}
	s := NewCacheSource("cache", dir, store)
	col, err := s.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(col.Revisions) != 1 || col.Revisions[0].RequestedRef != "ghcr.io/org/svc:1.0.0" {
		t.Fatalf("revisions = %+v", col.Revisions)
	}
}

func TestCacheSource_Collect_NoCacheDir(t *testing.T) {
	s := NewCacheSource("cache", filepath.Join(t.TempDir(), "does-not-exist"), &fakeStore{})
	col, err := s.Collect(context.Background())
	if err != nil {
		t.Fatalf("absent cache dir should not error: %v", err)
	}
	if len(col.Revisions) != 0 {
		t.Errorf("expected no revisions, got %d", len(col.Revisions))
	}
}

func TestCacheSource_Collect_StatError(t *testing.T) {
	// A path whose parent is a regular file makes os.Stat fail with a non
	// not-exist error (ENOTDIR).
	dir := t.TempDir()
	afile := filepath.Join(dir, "afile")
	if err := os.WriteFile(afile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewCacheSource("cache", filepath.Join(afile, "sub"), &fakeStore{})
	if _, err := s.Collect(context.Background()); err == nil {
		t.Fatal("expected stat error for a non-directory parent")
	}
}

func TestCacheSource_Collect_WalkError(t *testing.T) {
	orig := fsWalkDir
	// Simulate WalkDir invoking the callback with a traversal error, as it does
	// for an unreadable directory.
	fsWalkDir = func(root string, fn fs.WalkDirFunc) error {
		return fn(root, nil, errors.New("walk failed"))
	}
	t.Cleanup(func() { fsWalkDir = orig })
	dir := t.TempDir()
	if _, err := NewCacheSource("cache", dir, &fakeStore{}).Collect(context.Background()); err == nil {
		t.Fatal("expected walk error")
	}
}

func mustCacheFile(t *testing.T, dir, rel string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}
