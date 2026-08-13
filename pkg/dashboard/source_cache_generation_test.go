package dashboard

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v3/internal/cachehook"
	"github.com/trianalab/pacto/v3/pkg/oci"
)

// The on-disk OCI cache is SHARED. A `pacto pull`, a resolve from another
// dashboard, or the operator can commit a whole new generation into an entry
// directory while this index is walking it — and a cache entry is TWO facts,
// bundle and identity. Read separately, the walker publishes one generation's
// contract, hash and service name under the next generation's reference and
// digest: an indexed version describing an artifact that has never existed.
//
// These tests drive that interleaving deterministically. The competing writer
// commits at exactly the instant the window is open, and every record the index
// publishes must be wholly one generation.

func genDigest(fill string) string { return "sha256:" + strings.Repeat(fill, 64) }

// writeGeneration writes a whole cache entry into dir: the bundle archive and
// the identity recorded beside it, as one committed generation.
func writeGeneration(t *testing.T, dir, service, version, ref, digest string) {
	t.Helper()
	writeBundleTarGzFile(t, filepath.Join(dir, oci.CachedBundleFile),
		"pactoVersion: \"2.0\"\nservice:\n  name: "+service+"\n  version: "+version+"\n")
	rec, err := json.Marshal(oci.CachedRef{Ref: ref, Digest: digest})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, oci.CachedRefFile), rec, 0o600); err != nil {
		t.Fatal(err)
	}
}

// atBarrier runs fn inside the next n cache-entry reads, between the bundle and
// the identity beside it — the window a competing writer commits in.
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

// installOver commits the generation staged at src over the entry at dst, the
// way the cache itself does: the old generation is UNLINKED — a reader holding
// it keeps reading it, and sees its files absent rather than rewritten — and the
// new one takes the name. src is staged outside the walked tree, so the walk
// never sees a half-built entry.
func installOver(t *testing.T, dst, src string) {
	t.Helper()
	if err := os.RemoveAll(dst); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(src, dst); err != nil {
		t.Fatal(err)
	}
}

// TestCacheSource_BuildIndex_ABundleAndItsIdentityAreOneGeneration is the
// walker's counterexample. Generation A is installed and discovered; generation
// B — a different service, at a different version, under a different reference
// and digest — is committed atomically before the identity beside A's bytes is
// read.
//
// Every fact the index publishes for that entry comes from one generation or the
// entry is not indexed at all. A record carrying A's contract under B's
// reference or digest is the defect; so is B's contract under A's.
func TestCacheSource_BuildIndex_ABundleAndItsIdentityAreOneGeneration(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "ghcr.io", "org", "checkout", "1.0.0")
	digestA, digestB := genDigest("a"), genDigest("b")
	// What each published generation IS, whole.
	gens := map[string]struct{ service, version, ref string }{
		digestA: {"checkout-a", "1.0.0", "ghcr.io/org/checkout:1.0.0"},
		digestB: {"checkout-b", "2.0.0", "ghcr.io/other/checkout:2.0.0"},
	}
	writeGeneration(t, entry, gens[digestA].service, gens[digestA].version, gens[digestA].ref, digestA)

	staged := filepath.Join(t.TempDir(), "next")
	writeGeneration(t, staged, gens[digestB].service, gens[digestB].version, gens[digestB].ref, digestB)

	fired := 0
	atBarrier(t, 1, func() {
		fired++
		installOver(t, entry, staged)
	})

	src := NewCacheSource(root)
	if fired != 1 {
		t.Fatal("the competing writer never ran: the index read the bundle and its identity as two separate observations, " +
			"so nothing here says what happens when a generation is committed between them")
	}

	// Either generation is an acceptable answer, and so is indexing nothing at
	// all. A MIXTURE is not.
	if src.ServiceCount() == 0 {
		return // a coherent miss; the next Rescan repairs it
	}
	svc := src.snapshot()[gens[digestB].service]
	if svc == nil {
		svc = src.snapshot()[gens[digestA].service]
	}
	if svc == nil {
		t.Fatalf("indexed %d service(s), none of which is either published generation: %v", src.ServiceCount(), src.snapshot())
	}
	if len(svc.versions) != 1 {
		t.Fatalf("versions = %+v, want the one entry", svc.versions)
	}
	v := svc.versions[0]
	gen, published := gens[v.rec.Digest]
	if !published {
		t.Fatalf("indexed digest %q belongs to no published generation", v.rec.Digest)
	}
	if svc.name != gen.service {
		t.Errorf("service %q is indexed under digest %s, which holds %q", svc.name, v.rec.Digest, gen.service)
	}
	if v.ref != gen.ref || v.rec.Ref != gen.ref {
		t.Errorf("reference = %q/%q, want %q — the reference of the generation that served these bytes", v.ref, v.rec.Ref, gen.ref)
	}
	if v.tag != gen.version || v.contract.Service.Version != gen.version {
		t.Errorf("version = %q/%q, want %q", v.tag, v.contract.Service.Version, gen.version)
	}
	if svc.latest == nil || svc.latest.Contract.Service.Name != gen.service {
		t.Errorf("the resident bundle is %+v, want %q's", svc.latest, gen.service)
	}
}

// TestCacheSource_LazyLoad_RefusesTheGenerationItDidNotIndex holds the deferred
// read to the same rule. Only the latest version's bundle stays resident; every
// other version is read from disk when it is asked for, which can be long after
// the index recorded what lives there — long enough for a re-pull to have
// committed a different artifact into the same entry.
//
// The version's contract, hash and reference were published from the FIRST read.
// Serving the second read's bytes beneath them is the same splice, just delayed.
func TestCacheSource_LazyLoad_RefusesTheGenerationItDidNotIndex(t *testing.T) {
	root := t.TempDir()
	older := filepath.Join(root, "ghcr.io", "org", "api", "1.0.0")
	writeGeneration(t, older, "api", "1.0.0", "ghcr.io/org/api:1.0.0", genDigest("a"))
	// A newer version keeps 1.0.0 off the resident slot, so reading it is lazy.
	writeGeneration(t, filepath.Join(root, "ghcr.io", "org", "api", "2.0.0"),
		"api", "2.0.0", "ghcr.io/org/api:2.0.0", genDigest("b"))

	src := NewCacheSource(root)
	ctx := context.Background()
	if _, err := src.GetServiceVersion(ctx, Ref{Name: "api", Version: "1.0.0"}); err != nil {
		t.Fatalf("the indexed generation must still be readable: %v", err)
	}

	// The tag is republished and re-pulled: same reference, different artifact.
	staged := filepath.Join(t.TempDir(), "next")
	writeGeneration(t, staged, "api", "1.0.0", "ghcr.io/org/api:1.0.0", genDigest("c"))
	installOver(t, older, staged)

	if _, err := src.GetServiceVersion(ctx, Ref{Name: "api", Version: "1.0.0"}); err == nil {
		t.Error("the entry now holds a different artifact; serving it under the indexed version's identity is the splice")
	}

	// And an entry that is simply gone is a miss, not a panic or a stale answer.
	if err := os.RemoveAll(older); err != nil {
		t.Fatal(err)
	}
	if _, err := src.GetServiceVersion(ctx, Ref{Name: "api", Version: "1.0.0"}); err == nil {
		t.Error("a removed entry must not resolve")
	}
	// The resident latest is unaffected: it was never read from disk again.
	if _, err := src.GetServiceVersion(ctx, Ref{Name: "api", Version: "2.0.0"}); err != nil {
		t.Errorf("the resident latest version must still resolve: %v", err)
	}
}
