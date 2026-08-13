package oci

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

// Two references that USED to spell to one cache directory. The port form is the
// one every registry writes; the path form is how the old key spelled it, so an
// entry installed under refPathed lands exactly where a legacy entry for
// refPorted lives. Nothing here is hand-built: the alias is produced by the real
// store, which is the only way to prove the real store no longer honours it.
const (
	refPorted = "localhost:5000/demo/checkout:1.0.0"
	refPathed = "localhost/5000/demo/checkout:1.0.0"
)

// unreachableStore is a registry that is not there. Any call is a failure, so a
// test that expects the cache to answer proves it answered WITHOUT the network,
// and a test that expects a refusal proves the refusal is not a quiet fallback.
type unreachableStore struct{}

func (unreachableStore) Push(context.Context, string, *contract.Bundle) (string, error) {
	return "", errors.New("no registry")
}
func (unreachableStore) Resolve(context.Context, string) (string, error) {
	return "", errors.New("no registry")
}
func (unreachableStore) ListTags(context.Context, string) ([]string, error) {
	return nil, errors.New("no registry")
}
func (unreachableStore) Pull(context.Context, string) (*contract.Bundle, error) {
	return nil, errors.New("no registry")
}

// TestPullCached_AnEntryNamingAnotherReferenceIsAMissColdAndWarm is the alias
// counterexample at the read boundary. The entry on disk is a real, coherent
// cache entry — it is simply SOME OTHER artifact's, and it says so. Serving it
// would hand back its bytes and its digest under the reference this call named.
//
// Both legs are exercised, because they used to be able to disagree: the cold
// read fills memory under the LOOKUP key, so a guard that lived only on the disk
// path would let the second read publish the mixture the first one refused.
func TestPullCached_AnEntryNamingAnotherReferenceIsAMissColdAndWarm(t *testing.T) {
	privateCache(t)
	ctx := context.Background()
	installGeneration(t, ctx, refPathed, "sha256:bbb")

	reader := NewCachedStore(&generationStore{digest: "sha256:aaa"})
	if got, want := reader.legacyEntryDir(refPorted), reader.entryDir(refPathed); got != want {
		t.Fatalf("this test needs the two references to alias: %s vs %s", got, want)
	}

	if bundle, rec, ok := reader.PullCachedPinned(ctx, refPorted); ok {
		t.Fatalf("cold read served %q as %+v; the entry states %q", bundle.Contract.Service.Name, rec, refPathed)
	}

	// The disk is gone, so only the memory the cold read filled can answer now.
	if err := os.RemoveAll(reader.entryDir(refPathed)); err != nil {
		t.Fatal(err)
	}
	if _, ok := reader.pullCache[refPorted]; !ok {
		t.Fatal("the cold read left nothing in memory, so the second read would prove nothing about the warm path")
	}
	if bundle, rec, ok := reader.PullCachedPinned(ctx, refPorted); ok {
		t.Fatalf("warm read served %q as %+v; the entry states %q", bundle.Contract.Service.Name, rec, refPathed)
	}
}

// TestPullPinned_AnAliasEntryIsRefetchedNotServed is the same counterexample on
// the RemoteAllowed path, where a miss has somewhere to go: the registry holds
// the artifact actually asked for, and that is what must come back. The entry
// the lookup passed over belongs to another reference and must survive intact —
// it is that reference's offline baseline.
func TestPullPinned_AnAliasEntryIsRefetchedNotServed(t *testing.T) {
	privateCache(t)
	ctx := context.Background()
	installGeneration(t, ctx, refPathed, "sha256:bbb")

	store := NewCachedStore(&generationStore{digest: "sha256:aaa", bundle: generationBundle("gen-a")})
	bundle, digest, err := store.PullPinned(ctx, refPorted)
	if err != nil {
		t.Fatalf("PullPinned(%s): %v", refPorted, err)
	}
	if digest != "sha256:aaa" {
		t.Errorf("digest = %q, want the artifact the registry holds under this reference", digest)
	}
	assertCoherent(t, bundle, digest)

	other := NewCachedStore(&unreachableStore{})
	if _, rec, ok := other.PullCachedPinned(ctx, refPathed); !ok || rec.Digest != "sha256:bbb" {
		t.Errorf("the other reference's entry is now %+v (hit=%v); refetching one reference must not retire another's baseline", rec, ok)
	}
}

// TestPullPinned_AMatchingCacheHitAsksNoRegistry holds the other half: the guard
// rejects a DISAGREEING record, not a cache hit. An entry that names the
// reference asked for is still served from disk with no network at all, which is
// what makes the offline path offline.
func TestPullPinned_AMatchingCacheHitAsksNoRegistry(t *testing.T) {
	privateCache(t)
	ctx := context.Background()
	installGeneration(t, ctx, refPorted, "sha256:aaa")

	store := NewCachedStore(&unreachableStore{})
	bundle, digest, err := store.PullPinned(ctx, refPorted)
	if err != nil {
		t.Fatalf("a cached reference needed the registry: %v", err)
	}
	if digest != "sha256:aaa" {
		t.Errorf("digest = %q, want the one recorded at pull time", digest)
	}
	assertCoherent(t, bundle, digest)
}

// TestPullPinned_AnAliasEntryWithNoRegistryFailsRatherThanMix is the case with
// no good answer available: the only bytes on hand are another reference's and
// the registry cannot be reached. Failing is the honest result; the tempting
// alternative — "we have something close, take it" — is precisely the mixed
// revision.
func TestPullPinned_AnAliasEntryWithNoRegistryFailsRatherThanMix(t *testing.T) {
	privateCache(t)
	ctx := context.Background()
	installGeneration(t, ctx, refPathed, "sha256:bbb")

	store := NewCachedStore(&unreachableStore{})
	bundle, digest, err := store.PullPinned(ctx, refPorted)
	if err == nil {
		t.Fatalf("served %q under %s with the registry unreachable", bundle.Contract.Service.Name, refPorted)
	}
	if bundle != nil || digest != "" {
		t.Errorf("a failed pull returned bundle=%v digest=%q", bundle, digest)
	}
}

// TestCachedStore_TwoAliasingReferencesKeepSeparateBaselines is the persistence
// half of the same defect: with a non-injective key, pulling the second
// reference OVERWROTE the first one's entry, so the loser silently lost its
// offline baseline — invisible until a disconnected run, one process later.
// Each reader here is cold and offline, exactly that later process.
func TestCachedStore_TwoAliasingReferencesKeepSeparateBaselines(t *testing.T) {
	privateCache(t)
	ctx := context.Background()
	installGeneration(t, ctx, refPathed, "sha256:bbb")
	installGeneration(t, ctx, refPorted, "sha256:aaa")

	for _, tc := range []struct{ ref, digest string }{
		{refPathed, "sha256:bbb"},
		{refPorted, "sha256:aaa"},
	} {
		reader := NewCachedStore(&unreachableStore{})
		bundle, rec, ok := reader.PullCachedPinned(ctx, tc.ref)
		if !ok {
			t.Errorf("%s is not cached; pulling the other reference erased its baseline", tc.ref)
			continue
		}
		if (rec != CachedRef{Ref: tc.ref, Digest: tc.digest}) {
			t.Errorf("%s: record = %+v, want its own", tc.ref, rec)
		}
		assertCoherent(t, bundle, rec.Digest)
	}
}

// TestPullPinned_TheSupersededLegacyEntryIsRetired keeps the migration from
// doubling the cache. An entry this reference wrote under the old key is not
// read again once the new one exists, so leaving it behind is dead bytes at the
// exact path a future alias would want.
func TestPullPinned_TheSupersededLegacyEntryIsRetired(t *testing.T) {
	privateCache(t)
	ctx := context.Background()
	store := NewCachedStore(&generationStore{digest: "sha256:aaa", bundle: generationBundle("gen-a")})

	// The legacy entry is this reference's own and unreadable, so the pull is a
	// miss the registry answers — the one moment the old path can be retired.
	legacy := store.legacyEntryDir(refPorted)
	if err := store.writeCacheEntry(legacy, CachedRef{Ref: refPorted, Digest: "sha256:aaa"}, generationBundle("gen-a")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, CachedBundleFile), []byte("not gzip"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := store.PullPinned(ctx, refPorted); err != nil {
		t.Fatalf("PullPinned(%s): %v", refPorted, err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("the superseded entry is still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.entryDir(refPorted), CachedBundleFile)); err != nil {
		t.Errorf("the pull did not write the entry under the new key: %v", err)
	}
}

// A retirement is housekeeping. It happens after the pull has already succeeded
// and been committed, so a cache directory that refuses the removal must cost a
// warning, never the bundle the caller asked for.
func TestPullPinned_ARetirementThatCannotHappenIsNotAFailure(t *testing.T) {
	privateCache(t)
	ctx := context.Background()
	store := NewCachedStore(&generationStore{digest: "sha256:aaa", bundle: generationBundle("gen-a")})

	legacy := store.legacyEntryDir(refPorted)
	if err := store.writeCacheEntry(legacy, CachedRef{Ref: refPorted, Digest: "sha256:aaa"}, generationBundle("gen-a")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, CachedBundleFile), []byte("not gzip"), 0o600); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(legacy)
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

	bundle, digest, err := store.PullPinned(ctx, refPorted)
	if err != nil {
		t.Fatalf("an unretirable legacy entry failed the pull: %v", err)
	}
	assertCoherent(t, bundle, digest)
}
