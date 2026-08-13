package oci

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/internal/cachehook"
	"github.com/trianalab/pacto/v3/pkg/contract"
)

// A cache entry is TWO files, and the disk cache is SHARED: another Pacto
// process, or another CachedStore in this one, can commit a whole new generation
// between a reader's two opens. A mutex proves nothing across either boundary.
// These tests drive that interleaving deterministically — the barrier fires
// exactly where the competing writer would — and hold every successful read to
// one generation: A/A or B/B, never A/B.

// generationBundle is a minimal valid bundle whose service name is marker, so a
// reader can tell WHICH generation it actually read after a disk round trip.
func generationBundle(marker string) *contract.Bundle {
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

// generationStore is a registry holding one artifact: one digest, one content.
type generationStore struct {
	digest string
	bundle *contract.Bundle
}

func (s *generationStore) Push(context.Context, string, *contract.Bundle) (string, error) {
	return "", nil
}
func (s *generationStore) Resolve(context.Context, string) (string, error)    { return s.digest, nil }
func (s *generationStore) ListTags(context.Context, string) ([]string, error) { return nil, nil }
func (s *generationStore) Pull(context.Context, string) (*contract.Bundle, error) {
	if s.bundle == nil {
		return nil, errors.New("this store must not be asked")
	}
	return s.bundle, nil
}

// generations is what each published digest actually holds, so a reader's answer
// can be checked for coherence rather than for a particular winner.
var generations = map[string]string{"sha256:aaa": "gen-a", "sha256:bbb": "gen-b", "sha256:ccc": "gen-c"}

// privateCache points the disk cache at a directory of this test's own.
func privateCache(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
}

// installGeneration commits one whole cache entry for ref, as a separate store
// (a separate process, as far as the reader is concerned) would.
func installGeneration(t *testing.T, ctx context.Context, ref, digest string) {
	t.Helper()
	writer := NewCachedStore(&generationStore{digest: digest, bundle: generationBundle(generations[digest])})
	writer.DisableCache() // cold, so it really pulls and really commits
	if _, err := writer.Pull(ctx, ref); err != nil {
		t.Fatalf("installing generation %s: %v", digest, err)
	}
}

// atBarrier runs fn between the reader's bundle read and its identity read —
// the window a competing writer commits in — for the next n reads.
func atBarrier(t *testing.T, n int, fn func()) {
	t.Helper()
	old := cachehook.AfterBundleRead
	t.Cleanup(func() { cachehook.AfterBundleRead = old })
	cachehook.AfterBundleRead = func() {
		if n == 0 {
			return
		}
		n--
		fn()
	}
}

// assertCoherent fails unless the bundle and the digest describe the same
// published artifact.
func assertCoherent(t *testing.T, bundle *contract.Bundle, digest string) {
	t.Helper()
	want, published := generations[digest]
	if !published {
		t.Fatalf("digest %q belongs to no published generation", digest)
	}
	if got := bundle.Contract.Service.Name; got != want {
		t.Fatalf("the reader returned the bytes of %q under digest %s, which holds %q", got, digest, want)
	}
}

func TestPullCached_ServesOneGenerationsBytesUnderThatGenerationsIdentity(t *testing.T) {
	privateCache(t)
	ctx := context.Background()
	const ref = "localhost:5000/demo/checkout:1.0.0"

	installGeneration(t, ctx, ref, "sha256:aaa")
	// The writer commits generation B once the reader holds A's bundle and is
	// about to ask what it is.
	atBarrier(t, 1, func() { installGeneration(t, ctx, ref, "sha256:bbb") })

	reader := NewCachedStore(&generationStore{digest: "sha256:aaa"})
	bundle, rec, ok := reader.pullCached(ctx, ref)
	if !ok {
		t.Fatal("the entry is installed throughout: a reader must see one of the two generations")
	}
	assertCoherent(t, bundle, rec.Digest)
	if rec.Digest != "sha256:bbb" {
		t.Errorf("digest = %q; the generation displaced mid-read is gone, so its successor is what is readable", rec.Digest)
	}
}

func TestPullCached_AWriterThatKeepsWinningIsAMissNotAMixture(t *testing.T) {
	privateCache(t)
	ctx := context.Background()
	const ref = "localhost:5000/demo/checkout:1.0.0"

	installGeneration(t, ctx, ref, "sha256:aaa")
	digests := []string{"sha256:bbb", "sha256:ccc", "sha256:aaa"}
	i := 0
	atBarrier(t, len(digests), func() {
		installGeneration(t, ctx, ref, digests[i])
		i++
	})

	reader := NewCachedStore(&generationStore{digest: "sha256:aaa"})
	bundle, rec, ok := reader.pullCached(ctx, ref)
	if ok {
		// Serving is allowed only if it is coherent; giving up is the other
		// acceptable answer, and the next pull repairs it.
		assertCoherent(t, bundle, rec.Digest)
	}
	if i != len(digests) {
		t.Errorf("the reader retried %d times, want %d attempts against a writer that keeps winning", i, len(digests))
	}
}

func TestReadCacheEntry_ASidecarlessEntryIsCompatibleNotSwapped(t *testing.T) {
	privateCache(t)
	ctx := context.Background()
	const ref = "localhost:5000/demo/checkout:1.0.0"

	installGeneration(t, ctx, ref, "sha256:aaa")
	// An entry written before sidecars existed: bytes, no identity. It is still
	// readable, and reports no digest rather than being retried away as a
	// generation someone displaced.
	store := NewCachedStore(&generationStore{digest: "sha256:aaa"})
	dir := store.entryDir(ref)
	if err := os.Remove(filepath.Join(dir, CachedRefFile)); err != nil {
		t.Fatal(err)
	}
	bundle, rec, ok := ReadCacheEntry(dir)
	if !ok {
		t.Fatal("a pre-sidecar cache entry must still be readable")
	}
	if rec.Digest != "" || rec.Ref != "" {
		t.Errorf("identity = %+v, want none recorded", rec)
	}
	if got := bundle.Contract.Service.Name; got != "gen-a" {
		t.Errorf("read %q, want the cached bytes", got)
	}
}

// TestPullCachedPinned_AWarmReadKeepsTheRecordedRef holds the in-memory cache to
// the same rule as the disk read: what it hands back is what the generation that
// supplied the bytes SAID, not a digest with the record thrown away. A warm read
// that reduced the record to a digest would lose the only statement of what
// these bytes are — silently, on the second request.
func TestPullCachedPinned_AWarmReadKeepsTheRecordedRef(t *testing.T) {
	privateCache(t)
	ctx := context.Background()
	const ref = "localhost:5000/demo/checkout:1.0.0"
	installGeneration(t, ctx, ref, "sha256:bbb")

	reader := NewCachedStore(&generationStore{digest: "sha256:aaa"}) // a registry that would disagree
	for _, read := range []string{"cold (disk)", "warm (memory)"} {
		bundle, rec, ok := reader.PullCachedPinned(ctx, ref)
		if !ok {
			t.Fatalf("%s: the entry is installed, want a hit", read)
		}
		assertCoherent(t, bundle, rec.Digest)
		if (rec != CachedRef{Ref: ref, Digest: "sha256:bbb"}) {
			t.Errorf("%s read: record = %+v, want the whole record the entry holds", read, rec)
		}
	}
}
