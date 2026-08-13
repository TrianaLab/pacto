package oci

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

// Two references that spell to ONE legacy cache directory. The port form is the
// one every registry writes; the path form is how the old key spelled it. The
// new key gives each its own entry, but the collision does not go away — it is
// still there in every cache written before the upgrade, which is why a read
// must judge what it finds at the legacy path and a write must never go there.
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
// counterexample at the read boundary. A read still consults the legacy path,
// where the two spellings DO collide, so what it finds there can be a real,
// coherent cache entry that is simply SOME OTHER artifact's — and says so.
// Serving it would hand back its bytes and its digest under the reference this
// call named.
//
// Both legs are exercised, because they used to be able to disagree: the cold
// read fills memory under the LOOKUP key, so a guard that lived only on the disk
// path would let the second read publish the mixture the first one refused.
func TestPullCached_AnEntryNamingAnotherReferenceIsAMissColdAndWarm(t *testing.T) {
	privateCache(t)
	ctx := context.Background()
	seedLegacyEntry(t, refPathed, "sha256:bbb")

	reader := NewCachedStore(&generationStore{digest: "sha256:aaa"})
	legacy := reader.legacyEntryDir(refPathed)
	if got := reader.legacyEntryDir(refPorted); got != legacy {
		t.Fatalf("this test needs the two references to alias: %s vs %s", got, legacy)
	}

	if bundle, rec, ok := reader.PullCachedPinned(ctx, refPorted); ok {
		t.Fatalf("cold read served %q as %+v; the entry states %q", bundle.Contract.Service.Name, rec, refPathed)
	}

	// The disk is gone, so only the memory the cold read filled can answer now.
	if err := os.RemoveAll(legacy); err != nil {
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
	seedLegacyEntry(t, refPathed, "sha256:bbb")

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
	seedLegacyEntry(t, refPathed, "sha256:bbb")

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

// seedLegacyEntry installs the entry an EARLIER BUILD left for ref: the
// pre-injective path, with the complete pair beside each other — the bundle and
// the sidecar naming what it is. This is the state an upgrade actually starts
// from, and it is not reproducible by the current store, which no longer writes
// there.
func seedLegacyEntry(t *testing.T, ref, digest string) {
	t.Helper()
	store := NewCachedStore(&unreachableStore{})
	legacy := store.legacyEntryDir(ref)
	rec := CachedRef{Ref: ref, Digest: digest}
	if err := store.writeCacheEntry(legacy, rec, generationBundle(generations[digest])); err != nil {
		t.Fatalf("seeding the legacy entry for %s: %v", ref, err)
	}
}

// assertColdOfflineBaselines fails unless each reference is still recoverable,
// whole, by a reader that starts cold and cannot reach a registry — the process
// after the upgrade, on a plane.
func assertColdOfflineBaselines(t *testing.T, ctx context.Context, want ...CachedRef) {
	t.Helper()
	for _, w := range want {
		reader := NewCachedStore(&unreachableStore{})
		bundle, rec, ok := reader.PullCachedPinned(ctx, w.Ref)
		if !ok {
			t.Errorf("%s is no longer cached: its offline baseline was taken", w.Ref)
			continue
		}
		if rec != w {
			t.Errorf("%s: record = %+v, want its own %+v", w.Ref, rec, w)
		}
		assertCoherent(t, bundle, rec.Digest)
	}
}

// TestCachedStore_AnUpgradePullDoesNotDestroyALegacyBaseline is the alias
// counterexample at the MIGRATION boundary, the one an injective-among-new-keys
// map still loses. legacyEntryDir(refPorted) and entryDir(refPathed) are the
// same directory, so an upgraded store pulling refPathed writes straight over
// the baseline refPorted left behind — and nothing in the pull can tell, because
// the destruction is a commit at a path this reference legitimately owns.
//
// Both orders run: which reference was cached first must not decide which one
// survives.
func TestCachedStore_AnUpgradePullDoesNotDestroyALegacyBaseline(t *testing.T) {
	for _, tc := range []struct {
		name   string
		legacy CachedRef
		pulled CachedRef
	}{
		{"legacy A, then B is pulled",
			CachedRef{Ref: refPorted, Digest: "sha256:aaa"},
			CachedRef{Ref: refPathed, Digest: "sha256:bbb"}},
		{"legacy B, then A is pulled",
			CachedRef{Ref: refPathed, Digest: "sha256:bbb"},
			CachedRef{Ref: refPorted, Digest: "sha256:aaa"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			privateCache(t)
			ctx := context.Background()
			seedLegacyEntry(t, tc.legacy.Ref, tc.legacy.Digest)

			puller := NewCachedStore(&generationStore{
				digest: tc.pulled.Digest,
				bundle: generationBundle(generations[tc.pulled.Digest]),
			})
			bundle, digest, err := puller.PullPinned(ctx, tc.pulled.Ref)
			if err != nil {
				t.Fatalf("PullPinned(%s): %v", tc.pulled.Ref, err)
			}
			if digest != tc.pulled.Digest {
				t.Errorf("digest = %q, want the artifact the registry holds", digest)
			}
			assertCoherent(t, bundle, digest)

			assertColdOfflineBaselines(t, ctx, tc.legacy, tc.pulled)
		})
	}
}

// validRefs spans the shapes a reference comes in — tagged, untagged,
// digest-pinned, ported, nested — plus the pair whose spellings alias under the
// legacy key.
var validRefs = []string{
	"ghcr.io/org/svc:1.0.0",
	"ghcr.io/org/svc",
	"ghcr.io/org/svc@sha256:abc",
	"ghcr.io/org/svc/nested:1.0.0",
	"ghcr.io/org/svc:1.0.0-rc.1",
	refPorted,
	refPathed,
	"localhost:5000/demo/checkout",
	"registry.example.com:443/a/b/c:v2",
	"registry.example.com/443/a/b/c:v2",
}

// TestEntryDir_TheNewNamespaceIsDisjointFromEveryLegacyKey proves the property
// the migration needs, which is strictly stronger than injectivity among new
// keys: a new entry never lands where ANY reference's legacy entry lives. An
// injective-but-overlapping map still overwrites a stranger's baseline exactly
// once per upgrade, and the loss is invisible until the next offline run.
func TestEntryDir_TheNewNamespaceIsDisjointFromEveryLegacyKey(t *testing.T) {
	privateCache(t)
	store := NewCachedStore(&unreachableStore{})
	for _, a := range validRefs {
		for _, b := range validRefs {
			// Disjointness holds for a == b too: a reference's own legacy entry is
			// read, never written over.
			if got := store.entryDir(a); got == store.legacyEntryDir(b) {
				t.Errorf("entryDir(%q) is %q, the legacy entry of %q", a, got, b)
			}
			if a != b && store.entryDir(a) == store.entryDir(b) {
				t.Errorf("entryDir(%q) == entryDir(%q) == %q", a, b, store.entryDir(a))
			}
		}
	}
}
