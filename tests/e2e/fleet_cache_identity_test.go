//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
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
