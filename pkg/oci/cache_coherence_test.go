package oci_test

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/oci"
)

// A cache entry is TWO facts about one artifact: the bundle.tar.gz a walker keys
// on, and the ref.json beside it saying what that bundle IS. The PATH cannot say
// — every ':' is spelled '/', so a registry port reads like a path segment and a
// digest like a tag — so a reader that finds a bundle with no usable sidecar
// GUESSES an identity, and the same published artifact enters the fleet a second
// time under a derived content identity.
//
// These tests hold the pair to one rule: an entry a reader can SEE describes
// ITSELF. Absent is fine (a cache miss); disagreeing is not.

// markedBundle is a minimal valid bundle whose service name is marker, so a
// reader can tell WHICH artifact an entry actually holds after a round trip
// through the disk cache.
func markedBundle(marker string) *contract.Bundle {
	y := []byte(fmt.Sprintf("pactoVersion: \"2.0\"\nservice:\n  name: %q\n  version: \"1.0.0\"\n", marker))
	return &contract.Bundle{
		Contract: &contract.Contract{
			PactoVersion: "2.0",
			Service:      contract.Service{Name: marker, Version: "1.0.0"},
		},
		RawYAML: y,
		FS:      fstest.MapFS{"pacto.yaml": &fstest.MapFile{Data: y, Mode: fs.FileMode(0o644)}},
	}
}

// fixedStore is a registry holding exactly one artifact: one manifest digest,
// one content, however it is asked.
type fixedStore struct {
	digest string
	bundle *contract.Bundle
}

func (s *fixedStore) Push(context.Context, string, *contract.Bundle) (string, error) {
	return "", nil
}
func (s *fixedStore) Resolve(context.Context, string) (string, error)    { return s.digest, nil }
func (s *fixedStore) ListTags(context.Context, string) ([]string, error) { return nil, nil }
func (s *fixedStore) Pull(context.Context, string) (*contract.Bundle, error) {
	return s.bundle, nil
}

// useTempCacheHome points the disk cache at a fresh directory for one test.
func useTempCacheHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	old := oci.SetUserHomeDirFn(func() (string, error) { return home, nil })
	t.Cleanup(func() { oci.SetUserHomeDirFn(old) })
}

// heldBundle reads an entry back through the real disk-cache read path, over a
// store that would report zero pulls if the registry were consulted.
func heldBundle(t *testing.T, ref string) *contract.Bundle {
	t.Helper()
	cold := oci.NewCachedStore(&countingStore{})
	b, ok := cold.PullCached(context.Background(), ref)
	if !ok {
		t.Fatalf("the cache entry for %s cannot be read back from disk", ref)
	}
	return b
}

func TestCachedStore_PullNeverPublishesABundleWithoutItsIdentity(t *testing.T) {
	useTempCacheHome(t)
	const ref = "localhost:5000/demo/checkout:1.0.0"

	store := oci.NewCachedStore(&fixedStore{digest: "sha256:aaa", bundle: markedBundle("svc-first")})
	dir := cachedDir(store, ref)

	// The adversary: the sidecar CANNOT be written, because its path is already a
	// directory. Writing the pair in place fails here and goes on to publish
	// bundle.tar.gz anyway — a bundle a walker can see and cannot identify, which
	// is exactly the approximate, duplicated CacheSource revision at issue.
	if err := os.MkdirAll(filepath.Join(dir, oci.CachedRefFile), 0o755); err != nil {
		t.Fatal(err)
	}

	// Caching is best effort, so the pull itself must still succeed.
	if _, err := store.Pull(context.Background(), ref); err != nil {
		t.Fatalf("Pull() error: %v", err)
	}

	rec, ok := oci.ReadCachedRef(dir)
	if _, err := os.Stat(filepath.Join(dir, oci.CachedBundleFile)); err == nil && !ok {
		t.Fatal("a visible bundle.tar.gz with no usable ref.json: the reader can only guess what it is")
	}
	// Stronger than the invariant: a sidecar path blocked by a stale directory is
	// a condition the commit can clear, so the entry must actually land.
	if !ok {
		t.Fatal("the entry was not committed at all")
	}
	if rec.Ref != ref || rec.Digest != "sha256:aaa" {
		t.Fatalf("sidecar = %+v, want the requested ref and the digest of what was pulled", rec)
	}
	if got := heldBundle(t, ref).Contract.Service.Name; got != "svc-first" {
		t.Fatalf("the entry holds %q, want the bundle its sidecar describes", got)
	}
}

func TestCachedStore_PullNeverPairsANewIdentityWithOldBytes(t *testing.T) {
	useTempCacheHome(t)
	const ref = "localhost:5000/demo/checkout:1.0.0"
	ctx := context.Background()

	// A coherent entry to begin with: content "svc-first" under digest sha256:aaa.
	first := oci.NewCachedStore(&fixedStore{digest: "sha256:aaa", bundle: markedBundle("svc-first")})
	if _, err := first.Pull(ctx, ref); err != nil {
		t.Fatalf("first Pull() error: %v", err)
	}
	dir := cachedDir(first, ref)

	// The adversary: the BYTES can no longer be replaced, but the identity beside
	// them still can. An in-place overwrite therefore updates ref.json and leaves
	// bundle.tar.gz — the entry then swears the old artifact is the new digest, and
	// the fleet publishes content under an identity it does not have.
	bundlePath := filepath.Join(dir, oci.CachedBundleFile)
	if err := os.Chmod(bundlePath, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(bundlePath, 0o644) })

	// A different published artifact arrives under the same reference. DisableCache
	// makes this store cold, so it really pulls rather than serving what is there.
	second := oci.NewCachedStore(&fixedStore{digest: "sha256:bbb", bundle: markedBundle("svc-second")})
	second.DisableCache()
	if _, err := second.Pull(ctx, ref); err != nil {
		t.Fatalf("second Pull() error: %v", err)
	}

	rec, ok := oci.ReadCachedRef(dir)
	if !ok {
		t.Fatal("the entry lost its identity")
	}
	// EITHER round may have won — a failed commit legitimately leaves the earlier
	// entry standing. What may never happen is one round's identity over the
	// other's bytes.
	want := map[string]string{"sha256:aaa": "svc-first", "sha256:bbb": "svc-second"}[rec.Digest]
	if want == "" {
		t.Fatalf("sidecar digest %q belongs to neither published artifact", rec.Digest)
	}
	if got := heldBundle(t, ref).Contract.Service.Name; got != want {
		t.Fatalf("the entry claims digest %s but holds %q, not %q", rec.Digest, got, want)
	}
}

// movingTagStore is a registry that re-publishes the tag between observations of
// it: every Resolve reports a new manifest digest. Pulling a DIGEST serves
// exactly that artifact, as a content-addressed registry does; pulling the TAG
// serves whatever is current, and moves it again.
type movingTagStore struct {
	mu sync.Mutex
	n  int
}

func movingDigest(n int) string { return fmt.Sprintf("sha256:%064d", n) }

func (m *movingTagStore) Push(context.Context, string, *contract.Bundle) (string, error) {
	return "", nil
}
func (m *movingTagStore) ListTags(context.Context, string) ([]string, error) { return nil, nil }

func (m *movingTagStore) Resolve(context.Context, string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.n++
	return movingDigest(m.n), nil
}

func (m *movingTagStore) Pull(_ context.Context, ref string) (*contract.Bundle, error) {
	if i := strings.Index(ref, "@"); i >= 0 {
		return markedBundle(ref[i+1:]), nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.n++
	return markedBundle(movingDigest(m.n)), nil
}

// A version tag is not immutable. Pulling it and then asking what it points at
// are two observations of a moving target, and the answer to the second names
// bytes the first never downloaded: the cache then records bundle A as digest B,
// and every offline reader of that entry inherits the lie.
func TestCachedStore_PullPinnedBindsTheDigestToTheBytesItFetched(t *testing.T) {
	useTempCacheHome(t)
	const ref = "localhost:5000/demo/checkout:1.0.0"

	store := oci.NewCachedStore(&movingTagStore{})
	bundle, digest, err := store.PullPinned(context.Background(), ref)
	if err != nil {
		t.Fatalf("PullPinned() error: %v", err)
	}
	if digest == "" {
		t.Fatal("PullPinned reported no digest for a registry that answers every resolve")
	}
	if got := bundle.Contract.Service.Name; got != digest {
		t.Fatalf("PullPinned returned the artifact published as %q under digest %s", got, digest)
	}

	// The sidecar is what the OFFLINE reader will believe, so it must carry the
	// same binding — and still name the reference the caller asked for.
	rec, ok := oci.ReadCachedRef(cachedDir(store, ref))
	if !ok {
		t.Fatal("no sidecar beside the cached bundle")
	}
	if rec.Digest != digest {
		t.Errorf("sidecar digest = %q, want %q: the digest of the bytes on disk", rec.Digest, digest)
	}
	if rec.Ref != ref {
		t.Errorf("sidecar ref = %q, want the originally requested %q kept as provenance", rec.Ref, ref)
	}
	if got := heldBundle(t, ref).Contract.Service.Name; got != digest {
		t.Errorf("the cached bytes are %q, but the entry claims digest %s", got, digest)
	}
}

// ResolvePinned is the seam the fleet's OCI source uses to learn a revision's
// immutable identity. It must carry the pull's own binding through — and it must
// not invent one offline, where the recorded identity is all there is and dialling
// a registry is exactly what the mode forbids.
func TestResolverPinned_CarriesTheBindingTheStoreMade(t *testing.T) {
	useTempCacheHome(t)
	const ref = "localhost:5000/demo/checkout:1.0.0"
	ctx := context.Background()

	warm := oci.NewCachedStore(&movingTagStore{})
	bundle, digest, err := oci.NewResolver(warm).ResolvePinned(ctx, "oci://"+ref, oci.RemoteAllowed)
	if err != nil {
		t.Fatalf("ResolvePinned() error: %v", err)
	}
	if got := bundle.Contract.Service.Name; got != digest {
		t.Fatalf("resolved the artifact published as %q under digest %s", got, digest)
	}

	cold := oci.NewCachedStore(&countingStore{})
	local, digest, err := oci.NewResolver(cold).ResolvePinned(ctx, ref, oci.LocalOnly)
	if err != nil {
		t.Fatalf("LocalOnly ResolvePinned() error: %v", err)
	}
	if digest != "" {
		t.Errorf("LocalOnly reported digest %q; offline identity comes from the sidecar the caller reads, not from the registry", digest)
	}
	if local.Contract.Service.Name != bundle.Contract.Service.Name {
		t.Errorf("offline served %q, want the cached %q", local.Contract.Service.Name, bundle.Contract.Service.Name)
	}
}

// A cache hit must report the identity RECORDED at pull time. Re-deriving it
// would mean asking a registry the offline reader cannot reach — and asking a
// tag that has since moved.
func TestCachedStore_PullPinnedCacheHitReportsTheRecordedDigest(t *testing.T) {
	useTempCacheHome(t)
	const ref = "localhost:5000/demo/checkout:1.0.0"
	ctx := context.Background()

	inner := &movingTagStore{}
	warm := oci.NewCachedStore(inner)
	_, digest, err := warm.PullPinned(ctx, ref)
	if err != nil {
		t.Fatalf("PullPinned() error: %v", err)
	}

	for _, tc := range []struct {
		name  string
		store *oci.CachedStore
	}{
		{"memory hit", warm},
		{"disk hit", oci.NewCachedStore(inner)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bundle, got, err := tc.store.PullPinned(ctx, ref)
			if err != nil {
				t.Fatalf("PullPinned() error: %v", err)
			}
			if got != digest {
				t.Errorf("digest = %q, want the recorded %q", got, digest)
			}
			if name := bundle.Contract.Service.Name; name != got {
				t.Errorf("served %q under digest %q", name, got)
			}
		})
	}
}
