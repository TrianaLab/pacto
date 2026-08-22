//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/oci"
	"github.com/trianalab/pacto/v3/pkg/plugin"
)

// The dashboard's normal deployment runs the registry source and the disk-cache
// source SIDE BY SIDE: OCIRefs are discovered from the cluster each refresh, and
// IncludeCache keeps the pod useful when the registry is unreachable. That pairing
// is what these tests hold to account. Once a registry pull has populated the pod
// cache, the SAME published artifact is offered twice — once by the registry, once
// off disk — and the fleet must recognise it as ONE canonical Contract Revision.
// It previously did not: the cache path spells ':' as '/', so the cached record
// carried a guessed domain and no manifest digest, and a derived content digest
// became a second RevisionKey that the Product showed as an unresolved shadow of
// the real revision.
//
// These run against a real registry, a real CachedStore and a real
// app.Service.Fleet — the production wiring, not a stand-in.

// deadRegistry is a BundleStore that refuses every call and counts the attempts.
// It stands in for the disconnected pod: any use of it is a network call that a
// LocalOnly cache read promised not to make.
type deadRegistry struct{ calls atomic.Int32 }

func (d *deadRegistry) Push(context.Context, string, *contract.Bundle) (string, error) {
	d.calls.Add(1)
	return "", errors.New("registry unreachable")
}

func (d *deadRegistry) Pull(context.Context, string) (*contract.Bundle, error) {
	d.calls.Add(1)
	return nil, errors.New("registry unreachable")
}

func (d *deadRegistry) Resolve(context.Context, string) (string, error) {
	d.calls.Add(1)
	return "", errors.New("registry unreachable")
}

func (d *deadRegistry) ListTags(context.Context, string) ([]string, error) {
	d.calls.Add(1)
	return nil, errors.New("registry unreachable")
}

// cacheIdentityContract is a minimal valid contract; extra is appended to the
// metadata so two pushes of the same service and version differ in CONTENT, and
// therefore in manifest digest, which is the case that must stay two revisions.
func cacheIdentityContract(name, version, extra string) string {
	return fmt.Sprintf(`pactoVersion: "2.0"
service:
  name: %s
  version: "%s"
  owner:
    team: platform
metadata:
  marker: "%s"
`, name, version, extra)
}

// pushCacheIdentityBundle writes a bundle and pushes it, returning the ref.
func pushCacheIdentityBundle(t *testing.T, reg *testRegistry, repo, tag, name, version, extra string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name+"-"+tag)
	writeBundleDir(t, dir, cacheIdentityContract(name, version, extra), nil)
	ref := "oci://" + reg.host + "/" + repo + ":" + tag
	if out, err := runCommand(t, reg, "push", ref, "-p", dir); err != nil {
		t.Fatalf("push %s: %v\n%s", ref, err, out)
	}
	return ref
}

// revisionsOf returns the snapshot's revisions for one service name, sorted by key.
func revisionsOf(snap *fleet.FleetSnapshot, service string) []*fleet.ContractRevision {
	var out []*fleet.ContractRevision
	for _, rev := range snap.Revisions {
		if rev.Service == service {
			out = append(out, rev)
		}
	}
	sort.Slice(out, func(i, j int) bool { return string(out[i].Key) < string(out[j].Key) })
	return out
}

func hasSource(rev *fleet.ContractRevision, id string) bool {
	for _, s := range rev.Sources {
		if s == id {
			return true
		}
	}
	return false
}

// unresolvedRevisions reports the REVISION_IDENTITY_UNRESOLVED limitations, which
// are exactly the shadow records this fix exists to prevent.
func unresolvedRevisions(snap *fleet.FleetSnapshot) []string {
	var msgs []string
	for _, l := range snap.Limitations {
		if l.Code == fleet.LimitationRevisionUnresolved {
			msgs = append(msgs, l.Source+": "+l.Message)
		}
	}
	return msgs
}

func TestFleetCacheAndOCIYieldOneCanonicalRevision(t *testing.T) {
	// t.Setenv forbids t.Parallel: the cache home is process-wide state.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	reg := newTestRegistry(t)

	checkoutRef := pushCacheIdentityBundle(t, reg, "demo/checkout", "1.0.0", "checkout", "1.0.0", "a")
	ordersRef := pushCacheIdentityBundle(t, reg, "demo/orders", "1.0.0", "orders", "1.0.0", "a")

	// Production wiring: the registry client behind the disk cache, both sources on.
	store := oci.NewCachedStore(reg.client)
	svc := app.NewService(store, &plugin.SubprocessRunner{})
	opts := app.FleetOptions{
		OCIRefs:      []string{checkoutRef, ordersRef},
		IncludeCache: true,
	}

	// Round one: the cache starts EMPTY and the registry pull fills it. This is the
	// refresh that used to plant the shadow — the cache source walks a directory the
	// registry source is writing into, and whatever it finds must already know what
	// it is.
	first, err := svc.Fleet(context.Background(), opts)
	if err != nil {
		t.Fatalf("cold build: %v", err)
	}
	if msgs := unresolvedRevisions(first); len(msgs) != 0 {
		t.Errorf("cold build produced unresolved shadow revisions: %v", msgs)
	}

	// Round two: the cache is now populated, so both sources contribute the same
	// two published artifacts. This is the steady state of a running dashboard.
	second, err := svc.Fleet(context.Background(), opts)
	if err != nil {
		t.Fatalf("warm build: %v", err)
	}
	if msgs := unresolvedRevisions(second); len(msgs) != 0 {
		t.Fatalf("warm build produced unresolved shadow revisions: %v", msgs)
	}
	for _, service := range []string{"checkout", "orders"} {
		revs := revisionsOf(second, service)
		if len(revs) != 1 {
			for _, r := range revs {
				t.Logf("  %s: key=%s domain=%s digest=%q sources=%v", service, r.Key, r.Domain, r.Digest, r.Sources)
			}
			t.Fatalf("%s has %d revisions after the cache was populated, want exactly 1 canonical revision", service, len(revs))
		}
		rev := revs[0]
		if !hasSource(rev, "oci") || !hasSource(rev, "cache") {
			t.Errorf("%s revision sources = %v, want both the registry and the cache to have contributed the same revision", service, rev.Sources)
		}
		// Canonical means immutable and retrievable: the registry's manifest digest,
		// pinned into a resolver-parseable reference.
		if !strings.HasPrefix(rev.Digest, "sha256:") {
			t.Errorf("%s digest = %q, want the registry manifest digest", service, rev.Digest)
		}
		if !strings.HasPrefix(rev.ResolvedRef, "oci://") || !strings.Contains(rev.ResolvedRef, "@"+rev.Digest) {
			t.Errorf("%s resolvedRef = %q, want an oci:// reference pinned to %s", service, rev.ResolvedRef, rev.Digest)
		}
		// One domain, spelled as the registry is actually addressed. The path-derived
		// spelling ("<host>/<port>/demo") is the second-identity bug in its other form.
		if rev.Domain != reg.host+"/demo" {
			t.Errorf("%s domain = %q, want %q", service, rev.Domain, reg.host+"/demo")
		}
	}
}

func TestFleetDistinctDigestsSameVersionStayDistinct(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	reg := newTestRegistry(t)

	// Same service, same DECLARED version, two different immutable artifacts. The
	// version is an author's label; the digest is what was actually published.
	// Collapsing these would hide a re-published tag, which is the failure the
	// operational graph exists to surface.
	a := pushCacheIdentityBundle(t, reg, "demo/payments", "1.0.0", "payments", "1.0.0", "first")
	b := pushCacheIdentityBundle(t, reg, "demo/payments", "1.0.0-rebuild", "payments", "1.0.0", "second")

	store := oci.NewCachedStore(reg.client)
	svc := app.NewService(store, &plugin.SubprocessRunner{})
	opts := app.FleetOptions{OCIRefs: []string{a, b}, IncludeCache: true}

	if _, err := svc.Fleet(context.Background(), opts); err != nil {
		t.Fatalf("cold build: %v", err)
	}
	snap, err := svc.Fleet(context.Background(), opts)
	if err != nil {
		t.Fatalf("warm build: %v", err)
	}

	revs := revisionsOf(snap, "payments")
	if len(revs) != 2 {
		for _, r := range revs {
			t.Logf("  payments: key=%s digest=%q sources=%v", r.Key, r.Digest, r.Sources)
		}
		t.Fatalf("payments has %d revisions, want 2 (two genuinely different published artifacts)", len(revs))
	}
	if revs[0].Digest == revs[1].Digest {
		t.Errorf("both revisions carry digest %q; the fixture did not produce two distinct artifacts", revs[0].Digest)
	}
	for _, r := range revs {
		if r.Version != "1.0.0" {
			t.Errorf("revision %s version = %q, want the declared 1.0.0", r.Key, r.Version)
		}
	}
}

// movingTagRegistry is the real registry with an adversary attached: the first
// time an artifact is downloaded, the tag is RE-PUBLISHED before the download
// returns. Nothing here is simulated — the re-push is a real push to a real
// registry — only its timing is made deterministic, so the window that a
// re-publishing CI job hits by luck is hit on every run.
type movingTagRegistry struct {
	oci.BundleStore
	once   sync.Once
	repush func()
}

func (m *movingTagRegistry) Pull(ctx context.Context, ref string) (*contract.Bundle, error) {
	b, err := m.BundleStore.Pull(ctx, ref)
	m.once.Do(m.repush)
	return b, err
}

func cacheIdentityMarker(c *contract.Contract) string {
	s, _ := c.Metadata["marker"].(string)
	return s
}

// A version tag is NOT immutable. If the digest a revision carries is observed
// separately from the bytes it describes, a re-push between the two observations
// makes the fleet publish artifact A under artifact B's immutable identity — and
// every offline reader of the resulting cache entry inherits it. The digest must
// name what was actually downloaded.
func TestFleetMovingTagBindsDigestToTheBytesPulled(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	reg := newTestRegistry(t)
	ref := pushCacheIdentityBundle(t, reg, "demo/checkout", "1.0.0", "checkout", "1.0.0", "first")
	tag := reg.host + "/demo/checkout:1.0.0"
	published, err := reg.client.Resolve(context.Background(), tag)
	if err != nil {
		t.Fatalf("resolving the published tag: %v", err)
	}

	adversary := &movingTagRegistry{BundleStore: reg.client, repush: func() {
		// --force because this IS the case the flag exists for: an occupied tag
		// being made to point somewhere else.
		dir := filepath.Join(t.TempDir(), "repush")
		writeBundleDir(t, dir, cacheIdentityContract("checkout", "1.0.0", "second"), nil)
		if out, err := runCommand(t, reg, "push", ref, "-p", dir, "--force"); err != nil {
			t.Errorf("re-publishing the tag: %v\n%s", err, out)
		}
	}}
	store := oci.NewCachedStore(adversary)
	svc := app.NewService(store, &plugin.SubprocessRunner{})

	snap, err := svc.Fleet(context.Background(), app.FleetOptions{OCIRefs: []string{ref}, IncludeCache: true})
	if err != nil {
		t.Fatalf("build over a moving tag: %v", err)
	}
	revs := revisionsOf(snap, "checkout")
	if len(revs) != 1 {
		t.Fatalf("checkout has %d revisions, want 1", len(revs))
	}
	rev := revs[0]

	// The adversary must actually have fired, or the window never opened and the
	// test would pass without exercising anything. Measured against what the tag
	// pointed at BEFORE the build, never against the value under test.
	moved, err := reg.client.Resolve(context.Background(), tag)
	if err != nil {
		t.Fatalf("resolving the re-published tag: %v", err)
	}
	if moved == published {
		t.Fatalf("the tag still points at %s; the re-push never happened", moved)
	}

	// The claim under test: dereferencing the recorded digest yields the content
	// the revision is actually made of. The registry is the arbiter, not the tag.
	pinned, err := reg.client.Pull(context.Background(), oci.PinRefToDigest(tag, rev.Digest))
	if err != nil {
		t.Fatalf("pulling the recorded digest %s: %v", rev.Digest, err)
	}
	if got, want := cacheIdentityMarker(rev.Contract), cacheIdentityMarker(pinned.Contract); got != want {
		t.Errorf("revision holds marker %q but its digest %s names the artifact marked %q", got, rev.Digest, want)
	}
	if !strings.Contains(rev.ResolvedRef, "@"+rev.Digest) {
		t.Errorf("resolvedRef = %q, want it pinned to %s", rev.ResolvedRef, rev.Digest)
	}
	if rev.RequestedRef != ref {
		t.Errorf("requestedRef = %q, want the originally requested %q kept as provenance", rev.RequestedRef, ref)
	}

	// The disk cache inherits the same binding, so the offline reader is not told
	// the lie either.
	dead := &deadRegistry{}
	offline := app.NewService(oci.NewCachedStore(dead), &plugin.SubprocessRunner{})
	offSnap, err := offline.Fleet(context.Background(), app.FleetOptions{IncludeCache: true})
	if err != nil {
		t.Fatalf("offline build: %v", err)
	}
	offRevs := revisionsOf(offSnap, "checkout")
	if len(offRevs) != 1 {
		t.Fatalf("offline snapshot has %d checkout revisions, want 1", len(offRevs))
	}
	if offRevs[0].Digest != rev.Digest {
		t.Errorf("cached revision digest = %q, want the recorded %q", offRevs[0].Digest, rev.Digest)
	}
	if got := cacheIdentityMarker(offRevs[0].Contract); got != cacheIdentityMarker(rev.Contract) {
		t.Errorf("cached bytes are marked %q under digest %s, which names %q", got, offRevs[0].Digest, cacheIdentityMarker(rev.Contract))
	}
}

// entryDirOf returns the single cache entry directory under root, failing if
// there is not exactly one. The layout is the store's business; the test only
// needs to find what it wrote.
func entryDirOf(t *testing.T, root string) string {
	t.Helper()
	var found []string
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == oci.CachedBundleFile {
			found = append(found, filepath.Dir(path))
		}
		return err
	}); err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if len(found) != 1 {
		t.Fatalf("found %d cache entries under %s, want exactly 1: %v", len(found), root, found)
	}
	return found[0]
}

// copyEntry copies a whole cache entry — the bundle and the identity recorded
// beside it — to dst, leaving the original in place.
func copyEntry(t *testing.T, dst, src string) {
	t.Helper()
	if err := os.MkdirAll(dst, 0o750); err != nil {
		t.Fatal(err)
	}
	names, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(src, n.Name())) //nolint:gosec
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, n.Name()), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// legacyDirFor is the pathname an older Pacto stored an entry under, before the
// cache key was injective: every ':' spelled as a path separator.
func legacyDirFor(cacheDir, ref string) string {
	return filepath.Join(cacheDir, filepath.FromSlash(strings.ReplaceAll(ref, ":", "/")))
}

// A mutable tag republished between two pulls leaves the cache holding TWO
// generations of ONE reference: the artifact the older Pacto pulled, under the
// legacy pathname, and the artifact this version pulled, under the current one.
// Both are real. Neither may be destructively retired — removing an entry means
// deleting a directory of a SHARED cache by pathname, which takes whatever
// generation happens to be installed at that instant rather than the one that
// was inspected.
//
// So the offline fleet must enumerate GENERATIONS, not pathnames and not
// reference spellings. An inventory keyed on the reference alone reports one of
// the two artifacts and silently loses the other, which is exactly the history
// an operational graph exists to show.
func TestFleetOfflineSeesEveryCachedGenerationOfOneReference(t *testing.T) {
	homeA, shared := t.TempDir(), t.TempDir()
	reg := newTestRegistry(t)
	ref := pushCacheIdentityBundle(t, reg, "demo/checkout", "1.0.0", "checkout", "1.0.0", "first")
	tagged := reg.host + "/demo/checkout:1.0.0"
	ctx := context.Background()

	// Generation A, pulled and written by a real CachedStore into its own cache.
	t.Setenv("XDG_CACHE_HOME", homeA)
	storeA := oci.NewCachedStore(reg.client)
	if _, err := app.NewService(storeA, &plugin.SubprocessRunner{}).
		Fleet(ctx, app.FleetOptions{OCIRefs: []string{ref}}); err != nil {
		t.Fatalf("pulling generation A: %v", err)
	}
	digestA, err := reg.client.Resolve(ctx, tagged)
	if err != nil {
		t.Fatalf("resolving generation A: %v", err)
	}

	// The tag is republished: same reference, different artifact.
	repush := filepath.Join(t.TempDir(), "second")
	writeBundleDir(t, repush, cacheIdentityContract("checkout", "1.0.0", "second"), nil)
	if out, err := runCommand(t, reg, "push", ref, "-p", repush, "--force"); err != nil {
		t.Fatalf("republishing the tag: %v\n%s", err, out)
	}

	// Generation B, pulled by a real CachedStore into the cache under test.
	t.Setenv("XDG_CACHE_HOME", shared)
	storeB := oci.NewCachedStore(reg.client)
	if _, err := app.NewService(storeB, &plugin.SubprocessRunner{}).
		Fleet(ctx, app.FleetOptions{OCIRefs: []string{ref}}); err != nil {
		t.Fatalf("pulling generation B: %v", err)
	}
	digestB, err := reg.client.Resolve(ctx, tagged)
	if err != nil {
		t.Fatalf("resolving generation B: %v", err)
	}
	if digestA == digestB {
		t.Fatalf("both pulls resolved to %s; the fixture did not republish the tag", digestA)
	}

	// A now takes the place the older Pacto left it in, beside B. Nothing about
	// the entry is rewritten: it is byte-for-byte what the store wrote.
	copyEntry(t, legacyDirFor(storeB.CacheDir(), tagged), entryDirOf(t, storeA.CacheDir()))

	// Restart, cold, disconnected: a brand-new store over the cache directory and
	// a registry that answers nothing.
	dead := &deadRegistry{}
	snap, err := app.NewService(oci.NewCachedStore(dead), &plugin.SubprocessRunner{}).
		Fleet(ctx, app.FleetOptions{IncludeCache: true})
	if err != nil {
		t.Fatalf("offline build: %v", err)
	}
	if n := dead.calls.Load(); n != 0 {
		t.Errorf("offline build made %d registry calls, want 0", n)
	}
	if msgs := unresolvedRevisions(snap); len(msgs) != 0 {
		t.Errorf("offline build produced unresolved revisions: %v", msgs)
	}

	revs := revisionsOf(snap, "checkout")
	if len(revs) != 2 {
		for _, r := range revs {
			t.Logf("  checkout: key=%s digest=%q marker=%q", r.Key, r.Digest, cacheIdentityMarker(r.Contract))
		}
		t.Fatalf("checkout has %d revisions, want both cached generations of %s", len(revs), tagged)
	}
	// Each generation is itself: the digest names the bytes filed under it, and
	// the canonical reference is pinned to that digest.
	want := map[string]string{digestA: "first", digestB: "second"}
	for _, r := range revs {
		marker, published := want[r.Digest]
		if !published {
			t.Fatalf("revision digest %q belongs to neither published generation", r.Digest)
		}
		delete(want, r.Digest)
		if got := cacheIdentityMarker(r.Contract); got != marker {
			t.Errorf("digest %s carries the bytes marked %q, which is %q", r.Digest, got, marker)
		}
		if r.ResolvedRef != "oci://"+oci.PinRefToDigest(tagged, r.Digest) {
			t.Errorf("resolvedRef = %q, want it pinned to %s", r.ResolvedRef, r.Digest)
		}
		if r.RequestedRef != tagged {
			t.Errorf("requestedRef = %q, want the reference both generations were pulled under, %q", r.RequestedRef, tagged)
		}
	}
	if len(want) != 0 {
		t.Errorf("no revision was emitted for %v", want)
	}
}

// The other half of the same rule: one artifact filed under both pathnames is
// still ONE artifact. Suppression is keyed on the complete recorded identity —
// reference AND digest — so the legacy copy of a generation collapses into it
// rather than doubling the fleet.
func TestFleetOfflineCollapsesOneArtifactCachedTwice(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	reg := newTestRegistry(t)
	ref := pushCacheIdentityBundle(t, reg, "demo/checkout", "1.0.0", "checkout", "1.0.0", "only")
	tagged := reg.host + "/demo/checkout:1.0.0"
	ctx := context.Background()

	store := oci.NewCachedStore(reg.client)
	if _, err := app.NewService(store, &plugin.SubprocessRunner{}).
		Fleet(ctx, app.FleetOptions{OCIRefs: []string{ref}}); err != nil {
		t.Fatalf("warming the cache: %v", err)
	}
	copyEntry(t, legacyDirFor(store.CacheDir(), tagged), entryDirOf(t, store.CacheDir()))

	dead := &deadRegistry{}
	snap, err := app.NewService(oci.NewCachedStore(dead), &plugin.SubprocessRunner{}).
		Fleet(ctx, app.FleetOptions{IncludeCache: true})
	if err != nil {
		t.Fatalf("offline build: %v", err)
	}
	if n := dead.calls.Load(); n != 0 {
		t.Errorf("offline build made %d registry calls, want 0", n)
	}
	revs := revisionsOf(snap, "checkout")
	if len(revs) != 1 {
		for _, r := range revs {
			t.Logf("  checkout: key=%s digest=%q", r.Key, r.Digest)
		}
		t.Fatalf("checkout has %d revisions, want 1 — the same artifact filed twice is one artifact", len(revs))
	}
}

func TestFleetCacheOnlyIsAvailableAndNetworkFree(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	reg := newTestRegistry(t)
	ref := pushCacheIdentityBundle(t, reg, "demo/checkout", "1.0.0", "checkout", "1.0.0", "a")

	// Populate the cache exactly as a connected refresh does.
	warm := app.NewService(oci.NewCachedStore(reg.client), &plugin.SubprocessRunner{})
	if _, err := warm.Fleet(context.Background(), app.FleetOptions{OCIRefs: []string{ref}}); err != nil {
		t.Fatalf("warming the cache: %v", err)
	}

	// Now the disconnected pod: same cache directory, a registry that answers
	// nothing. The dashboard must still render, from disk, without dialling out —
	// one round trip per cached bundle is what used to leave every fleet endpoint
	// blocked on the first snapshot.
	dead := &deadRegistry{}
	offline := app.NewService(oci.NewCachedStore(dead), &plugin.SubprocessRunner{})
	snap, err := offline.Fleet(context.Background(), app.FleetOptions{IncludeCache: true})
	if err != nil {
		t.Fatalf("offline build: %v", err)
	}
	if n := dead.calls.Load(); n != 0 {
		t.Errorf("offline build made %d registry calls, want 0", n)
	}

	revs := revisionsOf(snap, "checkout")
	if len(revs) != 1 {
		t.Fatalf("offline snapshot has %d checkout revisions, want 1", len(revs))
	}
	// Offline is not degraded: the identity was RECORDED at pull time, so the
	// disconnected reader agrees with the registry about what this artifact is.
	if !strings.HasPrefix(revs[0].Digest, "sha256:") {
		t.Errorf("offline revision digest = %q, want the recorded manifest digest", revs[0].Digest)
	}
	if msgs := unresolvedRevisions(snap); len(msgs) != 0 {
		t.Errorf("offline build produced unresolved revisions: %v", msgs)
	}
}
