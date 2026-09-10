package fleetsrc

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
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
	"github.com/trianalab/pacto/v3/pkg/oci"
)

// fakeStore is an oci.BundleStore for the source tests.
type fakeStore struct {
	bundles map[string]*contract.Bundle
	pullErr map[string]error
	digest  map[string]string
	// tags maps a repository to its published tags, for the untagged-reference path.
	tags map[string][]string
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
func (f *fakeStore) ListTags(_ context.Context, repo string) ([]string, error) {
	return f.tags[repo], nil
}
func (f *fakeStore) Pull(_ context.Context, ref string) (*contract.Bundle, error) {
	// A registry is content-addressed: it serves an artifact by digest as well as
	// by the tag that names it. The pull path resolves a mutable tag ONCE and
	// fetches the digest that resolve named, so this fake has to answer the pinned
	// reference with the same bytes the tag would have served.
	if i := strings.Index(ref, "@"); i >= 0 {
		for tagged, d := range f.digest {
			if d == ref[i+1:] {
				ref = tagged
				break
			}
		}
	}
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

// TestOCISource_Collect_UntaggedRepositoryIsPinned covers the reference shape a
// LIVE cluster contributes: alongside each digest-pinned running ref, the
// discovery callback reports the bare REPOSITORY, which is the only way the newest
// published revision — the one nothing is running yet — enters the snapshot at all.
//
// That revision has to be exact retrievable content, or change analysis against it
// is refused. The digest lookup is a HEAD against the reference as written, and a
// bare repository parses as ":latest", so asking about the reference as given
// returned nothing and left the newest revision holding a mutable tag. The
// resolution is pinned to the concrete tag first, so the digest is asked about
// exactly the revision that was read.
func TestOCISource_Collect_UntaggedRepositoryIsPinned(t *testing.T) {
	dgst := validDigest("c")
	const repo = "reg.internal:5000/demo/checkout"
	store := &fakeStore{
		// Only the highest tag is pullable, so resolving anything else fails loudly.
		bundles: map[string]*contract.Bundle{repo + ":1.1.0": bundleFor("checkout")},
		digest:  map[string]string{repo + ":1.1.0": dgst},
		tags:    map[string][]string{repo: {"1.0.0", "1.1.0"}},
	}
	col, err := NewOCISource("oci", store, []string{"oci://" + repo}).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(col.Revisions) != 1 {
		t.Fatalf("revisions = %d (%+v), want 1", len(col.Revisions), col.Limitations)
	}
	rev := col.Revisions[0]
	// RequestedRef is still what was asked for: only the resolution moved.
	if rev.RequestedRef != "oci://"+repo {
		t.Errorf("RequestedRef = %q, want the repository as requested", rev.RequestedRef)
	}
	if rev.Digest != dgst || rev.ResolvedRef != "oci://"+repo+"@"+dgst {
		t.Errorf("ResolvedRef/Digest = %q/%q, want the canonical pinned form", rev.ResolvedRef, rev.Digest)
	}
	// The product consequence, not just the string: this revision is analyzable.
	if !fleet.ClassifyContentIdentity(rev.ResolvedRef, rev.Digest).Retrievable() {
		t.Error("a discovered repository's newest revision must be exact retrievable content")
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
// contacting a registry. The resolve path strips the oci://
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
	if s := NewCacheSource("", ""); s.ID() != "cache" || s.Kind() != "cache" {
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

	// The cache source is the OFFLINE path, and it holds nothing that could dial:
	// no store, no resolver. One digest round trip per cached bundle (there can be
	// thousands) would block the first snapshot, and with it every fleet endpoint,
	// on a slow or unreachable registry. Offline the mutable tag is the honest
	// identity unless the entry itself recorded a digest.
	s := NewCacheSource("cache", dir)
	col, err := s.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(col.Revisions) != 1 || col.Revisions[0].RequestedRef != "ghcr.io/org/svc:1.0.0" {
		t.Fatalf("revisions = %+v", col.Revisions)
	}
	if col.Revisions[0].ResolvedRef != "ghcr.io/org/svc:1.0.0" || col.Revisions[0].Digest != "" {
		t.Errorf("offline revision = ref %q digest %q, want the unpinned mutable tag",
			col.Revisions[0].ResolvedRef, col.Revisions[0].Digest)
	}
}

func TestCacheSource_Collect_ContextCancelled(t *testing.T) {
	dir := t.TempDir()
	mustCacheFile(t, dir, "ghcr.io/org/svc/1.0.0/bundle.tar.gz")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewCacheSource("cache", dir).Collect(ctx); err == nil {
		t.Fatal("expected context error")
	}
}

// A cancellation that arrives WHILE the walk is running must stop the walk.
//
// The expensive work is per entry: every bundle.tar.gz found is gunzipped and
// untarred. Checking ctx only after the walk returned meant a cancelled snapshot
// still read the whole shared cache first — thousands of entries on a developer
// machine — and then discarded all of it. The test cancels from inside the walk,
// so the reads that follow are the ones the fix has to not perform.
func TestCacheSource_Collect_CancelStopsTheWalk(t *testing.T) {
	dir := t.TempDir()
	for _, svc := range []string{"a", "b", "c", "d", "e"} {
		mustCacheFile(t, dir, "ghcr.io/org/"+svc+"/1.0.0/bundle.tar.gz")
	}

	ctx, cancel := context.WithCancel(context.Background())
	entries := 0
	orig := fsWalkDir
	fsWalkDir = func(root string, fn fs.WalkDirFunc) error {
		return orig(root, func(p string, d fs.DirEntry, walkErr error) error {
			if d != nil && d.Name() == oci.CachedBundleFile {
				entries++
				cancel()
			}
			return fn(p, d, walkErr)
		})
	}
	t.Cleanup(func() { fsWalkDir = orig })

	if _, err := NewCacheSource("cache", dir).Collect(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Collect err = %v, want context.Canceled", err)
	}
	if entries != 1 {
		t.Errorf("the walk reached %d cache entries after cancellation, want 1: ctx is checked outside the walk, so every remaining entry is still read and unpacked", entries)
	}
}

func TestCacheSource_Collect_NoCacheDir(t *testing.T) {
	s := NewCacheSource("cache", filepath.Join(t.TempDir(), "does-not-exist"))
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
	s := NewCacheSource("cache", filepath.Join(afile, "sub"))
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
	if _, err := NewCacheSource("cache", dir).Collect(context.Background()); err == nil {
		t.Fatal("expected walk error")
	}
}

// A cache directory is shared, and one entry it cannot open must not empty the
// whole offline baseline. Run pacto once under sudo and every later read of the
// root-owned entries is refused; aborting there answered "nothing has ever been
// pulled" for a machine with a full cache. Same policy as the local scan, and
// the same shared tracker enforces it.
func TestCacheSource_Collect_RefusedEntryIsAGapNotAnAbort(t *testing.T) {
	dir := t.TempDir()
	entry := "ghcr.io/org/svc/1.0.0"
	mustCacheFile(t, dir, entry+"/bundle.tar.gz")
	mustSidecar(t, filepath.Join(dir, filepath.FromSlash(entry)),
		`{"ref":"ghcr.io/org/svc:1.0.0","digest":"`+validDigest("d")+`"}`)

	// The seam rather than a chmod: a mode-000 directory is readable when the
	// suite runs as root, and a branch that skips there is a branch the coverage
	// gate never sees. WalkDir hands the callback a traversal error for the
	// directory it could not open and then carries on with the rest of the tree,
	// converting an fs.SkipDir answer into "carry on" — reproduced exactly.
	orig := fsWalkDir
	fsWalkDir = func(root string, fn fs.WalkDirFunc) error {
		refused := filepath.Join(root, "refused")
		if err := fn(refused, nil, errors.New("permission denied")); err != nil && !errors.Is(err, fs.SkipDir) {
			return err
		}
		return filepath.WalkDir(root, fn)
	}
	t.Cleanup(func() { fsWalkDir = orig })

	col, err := NewCacheSource("cache", dir).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(col.Revisions) != 1 {
		t.Fatalf("revisions = %d, want the one readable entry to survive the refusal", len(col.Revisions))
	}
	if len(col.Limitations) != 1 {
		t.Fatalf("limitations = %+v, want exactly one", col.Limitations)
	}
	lim := col.Limitations[0]
	if lim.Code != fleet.LimitationSourcePartial || lim.Source != "cache" {
		t.Errorf("limitation = %+v, want SOURCE_PARTIAL from cache", lim)
	}
	if !strings.Contains(lim.Message, "could not read refused") || strings.Contains(lim.Message, dir) {
		t.Errorf("message = %q, want the refused directory named relative to the cache root", lim.Message)
	}
}

func TestCacheSource_Collect_SidecarIsTheIdentity(t *testing.T) {
	dir := t.TempDir()
	dgst := validDigest("d")
	// A port-carrying registry is the case the PATH cannot represent: the cache
	// spells ':' as '/', so "localhost:5000/demo/checkout" and
	// "localhost/5000/demo/checkout" are the same directory. Reconstructing from
	// the path invents the domain "localhost/5000/demo" and knows no digest — a
	// SECOND service and a SECOND revision for one published artifact. The
	// sidecar the cache wrote at pull time says what it actually is.
	entry := "localhost/5000/demo/checkout/1.0.0"
	mustCacheFile(t, dir, entry+"/bundle.tar.gz")
	mustSidecar(t, filepath.Join(dir, filepath.FromSlash(entry)),
		`{"ref":"localhost:5000/demo/checkout:1.0.0","digest":"`+dgst+`"}`)

	col, err := NewCacheSource("cache", dir).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(col.Revisions) != 1 {
		t.Fatalf("revisions = %+v", col.Revisions)
	}
	rev := col.Revisions[0]
	if rev.RequestedRef != "localhost:5000/demo/checkout:1.0.0" {
		t.Errorf("RequestedRef = %q, want the exact pulled ref", rev.RequestedRef)
	}
	if rev.Domain != "localhost:5000/demo" {
		t.Errorf("Domain = %q, want localhost:5000/demo", rev.Domain)
	}
	// The recorded digest — read back WITH the bytes, not looked up afterwards —
	// is what makes the cached record key to the SAME canonical revision as the
	// registry record for the same artifact.
	if rev.Digest != dgst || rev.ResolvedRef != "oci://localhost:5000/demo/checkout@"+dgst {
		t.Errorf("revision = ref %q digest %q, want the digest-pinned canonical form", rev.ResolvedRef, rev.Digest)
	}
}

func TestCacheSource_Collect_SidecarWithoutDigestStaysApproximate(t *testing.T) {
	dir := t.TempDir()
	entry := "ghcr.io/org/svc/2.0.0"
	mustCacheFile(t, dir, entry+"/bundle.tar.gz")
	// The registry would not state a digest at pull time, so the sidecar records
	// the ref alone. That is still better than the path (no lossy ':'), but the
	// identity remains the mutable tag and says so.
	mustSidecar(t, filepath.Join(dir, filepath.FromSlash(entry)), `{"ref":"ghcr.io/org/svc:2.0.0"}`)

	col, err := NewCacheSource("cache", dir).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(col.Revisions) != 1 || col.Revisions[0].Digest != "" ||
		col.Revisions[0].ResolvedRef != "ghcr.io/org/svc:2.0.0" {
		t.Fatalf("revisions = %+v, want one unpinned mutable-tag revision", col.Revisions)
	}
}

// TestCacheSource_Collect_OneReferenceIsCollectedOnce covers the state an
// upgrade leaves: the same reference cached under BOTH the legacy key and the
// current one, because nothing retires the old entry — removing a directory of a
// shared cache by pathname takes whichever generation is installed at that
// instant, not the one that was inspected. Resolving the reference twice would
// be duplicate work at best and a duplicate revision at worst.
func TestCacheSource_Collect_OneReferenceIsCollectedOnce(t *testing.T) {
	dir := t.TempDir()
	const ref = "localhost:5000/demo/checkout:1.0.0"
	dgst := validDigest("d")
	for _, entry := range []string{
		"_v2/localhost%3A5000/demo/checkout/1.0.0", // what this version writes
		"localhost/5000/demo/checkout/1.0.0",       // what an earlier one left
	} {
		mustCacheFile(t, dir, entry+"/bundle.tar.gz")
		mustSidecar(t, filepath.Join(dir, filepath.FromSlash(entry)),
			`{"ref":"`+ref+`","digest":"`+dgst+`"}`)
	}

	col, err := NewCacheSource("cache", dir).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(col.Revisions) != 1 {
		t.Fatalf("revisions = %+v, want the one reference collected once", col.Revisions)
	}
}

func TestCacheSource_Collect_UnusableSidecarFallsBackToPath(t *testing.T) {
	dir := t.TempDir()
	entry := "ghcr.io/org/svc/1.0.0"
	mustCacheFile(t, dir, entry+"/bundle.tar.gz")
	mustSidecar(t, filepath.Join(dir, filepath.FromSlash(entry)), "{not json")

	col, err := NewCacheSource("cache", dir).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(col.Revisions) != 1 || col.Revisions[0].RequestedRef != "ghcr.io/org/svc:1.0.0" {
		t.Fatalf("revisions = %+v, want the path-reconstructed ref", col.Revisions)
	}
}

func TestCacheSource_Collect_OrderIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	// A directory walk visits in name order, so a sidecar that renames an entry
	// can reorder it. Snapshots are compared across refreshes; the order must
	// come from the refs, not from where they happened to sit on disk.
	mustCacheFile(t, dir, "ghcr.io/org/aaa/1.0.0/bundle.tar.gz")
	mustSidecar(t, filepath.Join(dir, filepath.FromSlash("ghcr.io/org/aaa/1.0.0")),
		`{"ref":"ghcr.io/org/zzz:1.0.0"}`)
	mustCacheFile(t, dir, "ghcr.io/org/bbb/1.0.0/bundle.tar.gz")

	col, err := NewCacheSource("cache", dir).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(col.Revisions) != 2 ||
		col.Revisions[0].RequestedRef != "ghcr.io/org/bbb:1.0.0" ||
		col.Revisions[1].RequestedRef != "ghcr.io/org/zzz:1.0.0" {
		t.Fatalf("revisions = %+v, want bbb then zzz", col.Revisions)
	}
}

// TestCacheSource_Collect_TwoGenerationsOfOneReferenceAreTwoRevisions is the
// state an upgrade plus a republished tag leaves on disk, and the reason an
// inventory of REFERENCES is not an inventory of the cache.
//
// A mutable tag was pulled once by an older Pacto, which wrote its entry under
// the legacy key, and pulled again after the upgrade, which wrote a DIFFERENT
// artifact under the current key. Two entries, one reference, two digests, and
// neither may be retired: removing a directory of a shared cache by pathname
// takes whichever generation is installed at that instant, not the one that was
// inspected.
//
// Collapsing them by reference publishes one of the two and silently loses the
// other, and — because the survivor would then be resolved BY that reference —
// resolves to whichever entry the store's lookup order happens to reach, twice.
// Both artifacts are on disk; both are baseline.
func TestCacheSource_Collect_TwoGenerationsOfOneReferenceAreTwoRevisions(t *testing.T) {
	dir := t.TempDir()
	const ref = "localhost:5000/demo/checkout:1.0.0"
	digestA, digestB := validDigest("a"), validDigest("b")
	// What each generation holds, whole.
	gens := map[string]string{digestA: "gen-a", digestB: "gen-b"}
	mustGeneration(t, filepath.Join(dir, filepath.FromSlash("localhost/5000/demo/checkout/1.0.0")),
		"gen-a", ref, digestA) // what an earlier version left
	mustGeneration(t, filepath.Join(dir, filepath.FromSlash("_v2/localhost%3A5000/demo/checkout/1.0.0")),
		"gen-b", ref, digestB) // what this version wrote when the tag moved

	col, err := NewCacheSource("cache", dir).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(col.Revisions) != 2 {
		t.Fatalf("revisions = %+v, want both cached generations of %s", col.Revisions, ref)
	}
	seen := map[string]bool{}
	for _, rev := range col.Revisions {
		holds, published := gens[rev.Digest]
		if !published {
			t.Fatalf("revision digest %q belongs to no published generation", rev.Digest)
		}
		if seen[rev.Digest] {
			t.Fatalf("digest %s emitted twice: one entry was read as two", rev.Digest)
		}
		seen[rev.Digest] = true
		if got := rev.Bundle.Contract.Service.Name; got != holds {
			t.Errorf("digest %s carries the bytes of %q, which is %q", rev.Digest, got, holds)
		}
		if want := "oci://localhost:5000/demo/checkout@" + rev.Digest; rev.ResolvedRef != want {
			t.Errorf("ResolvedRef = %q, want %q", rev.ResolvedRef, want)
		}
		if rev.RequestedRef != ref {
			t.Errorf("RequestedRef = %q, want %q", rev.RequestedRef, ref)
		}
	}
}

// TestCacheSource_Collect_AnUnreadableEntryIsAGapNotASilence covers the entry
// the walk can SEE and the read cannot resolve to one whole generation. A
// baseline missing a service it has on disk, reported as complete, is worse than
// one that says which entry it could not read.
func TestCacheSource_Collect_AnUnreadableEntryIsAGapNotASilence(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, filepath.FromSlash("ghcr.io/org/svc/1.0.0"))
	if err := os.MkdirAll(entry, 0o755); err != nil {
		t.Fatal(err)
	}
	// Bytes that are not an archive, under an identity that is perfectly good.
	if err := os.WriteFile(filepath.Join(entry, oci.CachedBundleFile), []byte("not gzip"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustSidecar(t, entry, `{"ref":"ghcr.io/org/svc:1.0.0","digest":"`+validDigest("a")+`"}`)

	col, err := NewCacheSource("cache", dir).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(col.Revisions) != 0 {
		t.Fatalf("revisions = %+v, want none: nothing here is readable content", col.Revisions)
	}
	if len(col.Limitations) != 1 || col.Limitations[0].Code != fleet.LimitationSourceRecordInvalid ||
		!strings.Contains(col.Limitations[0].Message, "ghcr.io/org/svc:1.0.0") {
		t.Fatalf("limitations = %+v, want one naming the entry it could not read", col.Limitations)
	}
}

// diskBundle survives a round trip through the REAL disk cache: its FS carries
// the pacto.yaml a reader parses back, and its service name says which
// generation the reader got.
func diskBundle(marker string) *contract.Bundle {
	y := []byte("pactoVersion: \"2.0\"\nservice:\n  name: " + marker + "\n  version: \"1.0.0\"\n")
	return &contract.Bundle{
		Contract: &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: marker, Version: "1.0.0"}},
		RawYAML:  y,
		FS:       fstest.MapFS{"pacto.yaml": &fstest.MapFile{Data: y, Mode: 0o644}},
	}
}

// oneArtifactRegistry serves a single artifact: one digest, one content.
type oneArtifactRegistry struct {
	digest string
	bundle *contract.Bundle
}

func (r *oneArtifactRegistry) Push(context.Context, string, *contract.Bundle) (string, error) {
	return "", nil
}
func (r *oneArtifactRegistry) Resolve(context.Context, string) (string, error) {
	return r.digest, nil
}
func (r *oneArtifactRegistry) ListTags(context.Context, string) ([]string, error) { return nil, nil }
func (r *oneArtifactRegistry) Pull(context.Context, string) (*contract.Bundle, error) {
	if r.bundle == nil {
		return nil, errors.New("this store must not be asked")
	}
	return r.bundle, nil
}

// TestCacheSource_Collect_IdentityComesFromTheEntryNotThePath is what the
// inventory itself owns of the coherence rule. Reading the bundle and asking a
// SECOND time what it is — from the path it was found under, or by resolving the
// reference again after the walk — publishes one generation's bytes under
// another's identity, because a shared cache changes underneath a walk. So every
// field of a revision comes from the single [oci.ReadCacheEntry] that served the
// bytes; that read's own atomicity is pkg/oci's, and is proven there.
//
// These two references are the pair the pre-injective cache key spelled to ONE
// entry directory, so they are what a path and a record can genuinely disagree
// about, and the legacy entries of that era are still readable:
//
//	localhost:5000/demo/checkout:1.0.0   what the entry RECORDS
//	localhost/5000/demo/checkout:1.0.0   what the path it sits at SPELLS
//
// A path spells a registry port with the same character it spells a repository
// separator with, so the two disagree on the domain as well as the reference.
func TestCacheSource_Collect_IdentityComesFromTheEntryNotThePath(t *testing.T) {
	dir := t.TempDir()
	const recorded = "localhost:5000/demo/checkout:1.0.0"
	digest := validDigest("a")
	// Hand-built: the current injective key would never put this record at this
	// path, and the legacy layout still holds entries that do.
	mustGeneration(t, filepath.Join(dir, filepath.FromSlash("localhost/5000/demo/checkout/1.0.0")),
		"gen-a", recorded, digest)

	col, err := NewCacheSource("cache", dir).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(col.Revisions) != 1 {
		t.Fatalf("revisions = %+v, want the one cached entry", col.Revisions)
	}
	rev := col.Revisions[0]
	if rev.Digest != digest {
		t.Fatalf("Digest = %q, want the recorded %q", rev.Digest, digest)
	}
	if got := rev.Bundle.Contract.Service.Name; got != "gen-a" {
		t.Errorf("bundle is %q, but digest %s holds %q", got, rev.Digest, "gen-a")
	}
	if rev.RequestedRef != recorded {
		t.Errorf("RequestedRef = %q, want %q — the record beside the bytes, not the path they sit at", rev.RequestedRef, recorded)
	}
	if want := "localhost:5000/demo"; rev.Domain != want {
		t.Errorf("Domain = %q, want %q — the domain of the reference that served these bytes", rev.Domain, want)
	}
	if want := pinRefToDigest(recorded, digest); rev.ResolvedRef != want {
		t.Errorf("ResolvedRef = %q, want %q", rev.ResolvedRef, want)
	}
}

// TestOCISource_Collect_ACachedAliasIsNotThisReferencesRevision is the whole
// production path, wired as it ships: an OCISource over a real CachedStore in
// RemoteAllowed mode, a real registry, and a real disk cache that another
// process has already written to.
//
// The entry that other process left is a COMPLETE, coherent cache entry for
// ANOTHER reference — it just happens to sit where the pre-injective key also
// looked for this one. Everything the revision publishes is therefore under
// test at once: serve those bytes and the snapshot carries B's contract, B's
// digest and B's canonical key under A's requested reference and A's domain, a
// revision no published artifact has. The registry has A; A is what must come
// back, whole.
func TestOCISource_Collect_ACachedAliasIsNotThisReferencesRevision(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	ctx := context.Background()
	const refA, refB = "localhost:5000/demo/checkout:1.0.0", "localhost/5000/demo/checkout:1.0.0"
	digestA, digestB := validDigest("a"), validDigest("b")

	// Another pull, in another process, over the same cache directory.
	writer := oci.NewCachedStore(&oneArtifactRegistry{digest: digestB, bundle: diskBundle("gen-b")})
	writer.DisableCache() // cold, so it really pulls and really commits
	if _, err := writer.Pull(ctx, refB); err != nil {
		t.Fatalf("installing the other reference's entry: %v", err)
	}

	store := oci.NewCachedStore(&oneArtifactRegistry{digest: digestA, bundle: diskBundle("gen-a")})
	col, err := NewOCISource("oci", store, []string{refA}).Collect(ctx)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(col.Limitations) != 0 {
		t.Fatalf("limitations = %+v, want none: the registry holds this reference", col.Limitations)
	}
	if len(col.Revisions) != 1 {
		t.Fatalf("revisions = %+v, want the one configured ref", col.Revisions)
	}
	rev := col.Revisions[0]
	if got := rev.Bundle.Contract.Service.Name; got != "gen-a" {
		t.Errorf("bundle is %q, want the content the registry holds under %s", got, refA)
	}
	if rev.Digest != digestA {
		t.Errorf("Digest = %q, want %q — the artifact this reference names", rev.Digest, digestA)
	}
	if rev.RequestedRef != refA {
		t.Errorf("RequestedRef = %q, want %q", rev.RequestedRef, refA)
	}
	if rev.Domain != "localhost:5000/demo" {
		t.Errorf("Domain = %q, want the domain of %s", rev.Domain, refA)
	}
	if want := "oci://localhost:5000/demo/checkout@" + digestA; rev.ResolvedRef != want {
		t.Errorf("ResolvedRef = %q, want %q", rev.ResolvedRef, want)
	}

	// And the other reference still has the offline baseline it installed. This
	// reader's registry refuses to be asked, so an answer at all is the cache's.
	reader := oci.NewCachedStore(&oneArtifactRegistry{digest: digestB})
	if _, digest, err := reader.PullPinned(ctx, refB); err != nil || digest != digestB {
		t.Errorf("the other reference's entry now answers %q (err=%v)", digest, err)
	}
}

func mustSidecar(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, oci.CachedRefFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// mustCacheFile writes one file of a cache entry. A bundle.tar.gz is a REAL
// archive: the inventory reads every entry it discovers, so a placeholder byte
// would make each of these fixtures an unreadable entry rather than a revision.
func mustCacheFile(t *testing.T, dir, rel string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) == oci.CachedBundleFile {
		mustBundleFile(t, p, "svc")
		return
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mustBundleFile writes a real bundle archive at path, holding a contract whose
// service name is marker — so a reader's answer says WHICH generation it read.
func mustBundleFile(t *testing.T, path, marker string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	y := []byte("pactoVersion: \"2.0\"\nservice:\n  name: " + marker + "\n  version: \"1.0.0\"\n")
	if err := tw.WriteHeader(&tar.Header{Name: "pacto.yaml", Size: int64(len(y)), Mode: 0o644}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(y); err != nil {
		t.Fatal(err)
	}
	for _, closeErr := range []error{tw.Close(), gw.Close(), f.Close()} {
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	}
}

// mustGeneration writes a WHOLE cache entry into dir — the bundle archive and
// the identity beside it — as one committed generation.
func mustGeneration(t *testing.T, dir, marker, ref, digest string) {
	t.Helper()
	mustBundleFile(t, filepath.Join(dir, oci.CachedBundleFile), marker)
	rec, err := json.Marshal(oci.CachedRef{Ref: ref, Digest: digest})
	if err != nil {
		t.Fatal(err)
	}
	mustSidecar(t, dir, string(rec))
}
