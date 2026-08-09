package fleetsrc

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/go-containerregistry/pkg/name"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// fakeStore is an oci.BundleStore for the source tests.
type fakeStore struct {
	bundles map[string]*contract.Bundle
	pullErr map[string]error
	digest  map[string]string
	// resolves counts digest resolutions, which are registry round trips.
	resolves int
}

func bundleFor(name string) *contract.Bundle {
	return &contract.Bundle{
		Contract: &contract.Contract{Service: contract.Service{Name: name, Version: "1.0.0"}},
		FS:       fstest.MapFS{},
	}
}

// validDigest returns a syntactically valid lower-case sha256 content digest,
// built programmatically so no 64-char body literal drifts.
func validDigest(fill string) string {
	return "sha256:" + strings.Repeat(fill, 64/len(fill)+1)[:64]
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
	f.resolves++
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
	dgstA := validDigest("a")
	store := &fakeStore{
		bundles: map[string]*contract.Bundle{
			"ghcr.io/x/a:1.0.0": bundleFor("a"),
			"ghcr.io/x/b:1.0.0": bundleFor("b"),
		},
		pullErr: map[string]error{"ghcr.io/x/missing:1.0.0": errors.New("not found")},
		digest:  map[string]string{"ghcr.io/x/a:1.0.0": dgstA}, // b has no digest
	}
	s := NewOCISource("oci", store, []string{"ghcr.io/x/a:1.0.0", "ghcr.io/x/b:1.0.0", "ghcr.io/x/missing:1.0.0"})
	col, err := s.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(col.Revisions) != 2 {
		t.Fatalf("revisions = %d, want 2", len(col.Revisions))
	}
	if col.Revisions[0].Digest != dgstA || col.Revisions[0].RequestedRef != "ghcr.io/x/a:1.0.0" {
		t.Errorf("revision a wrong: %+v", col.Revisions[0])
	}
	// A resolved digest pins ResolvedRef to the CANONICAL, immutable digest form
	// with the oci:// scheme (never a bare, scheme-less ref the resolver would treat
	// as a local path), regardless of the input ref's spelling.
	if col.Revisions[0].ResolvedRef != "oci://ghcr.io/x/a@"+dgstA {
		t.Errorf("revision a ResolvedRef = %q, want canonical oci:// digest form", col.Revisions[0].ResolvedRef)
	}
	// The pinned ResolvedRef must satisfy the strict immutable-identity invariant,
	// so an OCI-originated revision is always resolver-compatible exact content.
	if !fleet.IsDigestPinnedRef(col.Revisions[0].ResolvedRef) {
		t.Errorf("revision a ResolvedRef %q must be a canonical immutable OCI ref", col.Revisions[0].ResolvedRef)
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
	d := validDigest("a")
	// Whatever the input spelling, the output is the CANONICAL oci://<repo>@<digest>
	// form: the oci:// scheme is always emitted (a bare, scheme-less digest ref
	// would be resolved as a local filesystem path), any existing tag/digest is
	// stripped, and a registry port is never mistaken for a tag.
	cases := []struct{ ref, digest, want string }{
		{"ghcr.io/x/a:1.0.0", d, "oci://ghcr.io/x/a@" + d},
		{"oci://ghcr.io/acme/pay:1.0", d, "oci://ghcr.io/acme/pay@" + d},
		{"localhost:5000/acme/pay:1.0", d, "oci://localhost:5000/acme/pay@" + d},
		{"oci://ghcr.io/acme/pay@sha256:old", d, "oci://ghcr.io/acme/pay@" + d},
		{"payments", d, "oci://payments@" + d},
	}
	for _, c := range cases {
		got := pinRefToDigest(c.ref, c.digest)
		if got != c.want {
			t.Errorf("pinRefToDigest(%q,%q) = %q, want %q", c.ref, c.digest, got, c.want)
		}
		// The canonical result must be accepted by the strict immutable-identity parser.
		if !fleet.IsDigestPinnedRef(got) {
			t.Errorf("pinRefToDigest(%q,%q) = %q is not a canonical immutable OCI ref", c.ref, c.digest, got)
		}
	}
}

// TestPinRefToDigest_ResolverParseCompatible proves that a canonical ref emitted by
// OCISource is accepted by the ACTUAL OCI client's parse boundary without
// contacting a registry (requirement, item 10). The resolve path strips the oci://
// scheme (graph.ParseDependencyRef) and hands the "<repository>@<digest>" location
// to the go-containerregistry name parser (pkg/oci Client.parseRef ->
// name.ParseReference). If fleet.ParseCanonicalOCIRef accepts a ref the real name
// parser rejects, a "canonical" Product Impact would pass the exact-content guard
// and then fail when the provider resolves it; this test forbids that divergence.
func TestPinRefToDigest_ResolverParseCompatible(t *testing.T) {
	d := validDigest("a")
	for _, ref := range []string{
		"ghcr.io/x/a:1.0.0",
		"oci://ghcr.io/acme/pay:1.0",
		"localhost:5000/acme/pay:1.0",
		"payments",
	} {
		canonical := pinRefToDigest(ref, d)
		// The strip graph.ParseDependencyRef performs before the resolver sees it.
		location := strings.TrimPrefix(canonical, "oci://")
		if _, err := name.ParseReference(location); err != nil {
			t.Errorf("canonical ref %q (location %q) is rejected by the production name parser: %v", canonical, location, err)
		}
		// And the fleet strict parser agrees it is canonical immutable content.
		if !fleet.IsDigestPinnedRef(canonical) {
			t.Errorf("canonical ref %q rejected by fleet.IsDigestPinnedRef", canonical)
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

	store := &fakeStore{
		bundles: map[string]*contract.Bundle{"ghcr.io/org/svc:1.0.0": bundleFor("svc")},
		digest:  map[string]string{"ghcr.io/org/svc:1.0.0": validDigest("c")},
	}
	s := NewCacheSource("cache", dir, store)
	col, err := s.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(col.Revisions) != 1 || col.Revisions[0].RequestedRef != "ghcr.io/org/svc:1.0.0" {
		t.Fatalf("revisions = %+v", col.Revisions)
	}
	// The cache source is the OFFLINE path. It must not dial the registry, even to
	// pin a digest: one round trip per cached bundle (there can be thousands) blocks
	// the first snapshot, and with it every fleet endpoint, on a slow or unreachable
	// registry. Offline the mutable tag is the honest identity.
	if store.resolves != 0 {
		t.Errorf("cache source made %d registry digest lookups, want 0 (offline source)", store.resolves)
	}
	if col.Revisions[0].ResolvedRef != "ghcr.io/org/svc:1.0.0" || col.Revisions[0].Digest != "" {
		t.Errorf("offline revision = ref %q digest %q, want the unpinned mutable tag",
			col.Revisions[0].ResolvedRef, col.Revisions[0].Digest)
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
