package dashboard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/oci"
	"github.com/trianalab/pacto/v3/pkg/semver"
)

// versionKeyFor is the version a recorded reference is displayed and looked up
// by. It is NOT the reference — that is kept exactly as recorded — only the key
// beside it.
//
// A digest-pinned reference has no tag; the digest is what pins it. Otherwise a
// tag is a ':' after the last '/', so a registry port is never mistaken for one.
// A reference that names neither is indexed by the version its contract
// declares: the path cannot help, because the cache spells "no tag" as the
// escape sequence %00, and "%00" is not a version anyone can ask for.
func versionKeyFor(ref, contractVersion string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		return ref[i+1:]
	}
	slash := strings.LastIndex(ref, "/")
	if colon := strings.LastIndex(ref, ":"); colon > slash {
		return ref[colon+1:]
	}
	return contractVersion
}

// CacheSource implements DataSource by reading materialized OCI bundles from
// the on-disk cache at ~/.cache/pacto/oci/. It is NOT a public data source —
// it exists solely as an internal backing store for OCISource, providing
// contract hash, classification, and createdAt enrichment from previously
// pulled bundles. It must never appear as a named source in the API or UI.
//
// When no live OCI registry is configured, ActiveSources() maps CacheSource
// under the "oci" key so previously cached data is still available.
//
// An entry is a directory under cacheDir holding a bundle and the identity
// recorded beside it. Where that directory SITS is the cache's own business —
// current entries live under a reserved namespace segment with their reference
// escaped, older ones under the reference spelled as a path — so this walker
// looks for the bundle file anywhere beneath cacheDir and asks the entry itself
// what it is, rather than decoding the pathname (see [oci.ReadCacheEntry]).
type CacheSource struct {
	cacheDir string // e.g. ~/.cache/pacto/oci

	// mu guards services. Rescan swaps the map copy-on-write under the write
	// lock; readers take a snapshot under the read lock. The published map is
	// never mutated in place, so callers may iterate the snapshot lock-free.
	mu sync.RWMutex
	// Populated at scan time.
	services map[string]*cachedService // keyed by service name
}

type cachedService struct {
	name      string
	versions  []cachedVersion
	latest    *contract.Bundle // full bundle (with FS) for the latest version, kept resident
	latestTag string           // tag of the resident latest bundle
}

type cachedVersion struct {
	tag string // the display / lookup key
	ref string // the EXACT reference recorded for this entry, never rebuilt from tag
	dir string // the cache entry directory this version was read from
	rec oci.CachedRef
	// Lightweight, always resident (pacto.yaml is small):
	contract *contract.Contract // parsed contract
	rawYAML  []byte             // raw pacto.yaml, for ContractHash
	// The full bundle FS (openapi/sbom/schema — the bulk of the bytes) is NOT
	// retained per version; it is loaded lazily from the entry on demand. Only
	// the latest version's full bundle lives resident on cachedService.latest.
	// This bounds memory to O(services) instead of O(services × versions).
}

// loadBundle reads the full bundle (with FS) for this version from disk.
//
// The deferred read is a SECOND observation of a shared directory, taken some
// time after the index recorded what lives there — long enough for a re-pull to
// have committed a different artifact into the same entry. So the generation
// that answers must still be the generation that was indexed: this version's
// contract, hash and reference were published from the first read, and returning
// the second read's bytes beneath them would splice the two just as surely as
// reading the identity separately would.
func (v *cachedVersion) loadBundle() (*contract.Bundle, error) {
	bundle, rec, ok := oci.ReadCacheEntry(v.dir)
	if !ok {
		return nil, fmt.Errorf("cache entry for %q is not readable", v.ref)
	}
	if rec != v.rec {
		return nil, fmt.Errorf("cache entry for %q now holds a different artifact; rescan", v.ref)
	}
	return bundle, nil
}

// bundle returns the full bundle for tag: the resident latest when it matches,
// otherwise a lazy read from disk.
func (svc *cachedService) bundle(tag string) (*contract.Bundle, error) {
	if svc.latest != nil && tag == svc.latestTag {
		return svc.latest, nil
	}
	cv := svc.findVersion(tag)
	if cv == nil {
		return nil, fmt.Errorf("version %q not found", tag)
	}
	return cv.loadBundle()
}

// NewCacheSource scans the OCI cache directory for existing bundles.
// If the directory doesn't exist or contains no bundles, it returns a source
// that reports zero services.
func NewCacheSource(cacheDir string) *CacheSource {
	s := &CacheSource{cacheDir: cacheDir}
	s.services = s.buildIndex()
	return s
}

// buildIndex walks the cache directory and returns a fresh service index.
// It never touches s.services, so it is safe to call without holding the lock.
func (s *CacheSource) buildIndex() map[string]*cachedService {
	services := make(map[string]*cachedService)
	if _, err := os.Stat(s.cacheDir); os.IsNotExist(err) {
		return services
	}

	// One artifact, one entry. The same reference can be on disk twice — under
	// the legacy key and under the current one — because nothing deletes the old
	// copy: retiring an entry means removing a directory of a SHARED cache by
	// pathname, which takes whatever generation happens to be installed at the
	// moment of the removal rather than the one that was inspected. So the
	// duplicate is dropped here instead, keyed on the entry's complete recorded
	// identity: the reference AND the digest it was pulled at.
	seen := map[string]bool{}

	_ = filepath.Walk(s.cacheDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if info.IsDir() || info.Name() != oci.CachedBundleFile {
			return nil
		}

		// The walk DISCOVERS entries; it does not read them. Opening the archive
		// by pathname and then asking a second time what it was — even a moment
		// later, even in the same callback — is two observations of a SHARED
		// directory, and another Pacto process commits whole generations into it.
		// Between the two, this walker would publish generation A's contract,
		// hash and service name under generation B's reference and digest: an
		// indexed version describing no artifact that has ever existed. So the
		// directory goes to the one entry read that returns both facts from a
		// single installed generation, or neither.
		dir := filepath.Dir(path)
		bundle, rec, ok := oci.ReadCacheEntry(dir)
		if !ok {
			return nil // corrupt, or replaced faster than it can be read whole
		}

		// The recorded reference is the exact one this artifact was pulled under,
		// and the display key is derived from it separately. Only an entry written
		// before the sidecar existed falls back to the path — which is an ENCODING
		// of a reference, not the reference: it escapes a registry port and spells
		// "no tag" as %00, so it can publish "repo:%00", which no registry has.
		ref, tag := rec.Ref, versionKeyFor(rec.Ref, bundle.Contract.Service.Version)
		if ref == "" {
			// cacheDir/ghcr.io/org/name/1.0.0/bundle.tar.gz
			// -> rel = ghcr.io/org/name/1.0.0/bundle.tar.gz
			// filepath.Rel cannot fail because path is always under s.cacheDir (Walk root).
			rel, _ := filepath.Rel(s.cacheDir, path)
			parts := strings.Split(filepath.Dir(rel), string(filepath.Separator))
			if len(parts) < 2 {
				return nil // need at least registry/name/tag
			}
			tag = parts[len(parts)-1]
			ref = strings.Join(parts[:len(parts)-1], "/") + ":" + tag
		}

		identity := ref + "@" + rec.Digest
		if seen[identity] {
			return nil
		}
		seen[identity] = true

		name := bundle.Contract.Service.Name

		svc, ok := services[name]
		if !ok {
			svc = &cachedService{name: name}
			services[name] = svc
		}

		svc.versions = append(svc.versions, cachedVersion{
			tag:      tag,
			ref:      ref,
			dir:      dir,
			rec:      rec,
			contract: bundle.Contract,
			rawYAML:  bundle.RawYAML,
		})

		// Retain the full bundle (with FS) only for the newest version seen so
		// far, so ListServices/GetService stay fast without holding every version
		// resident. Historical versions keep just contract+rawYAML above and
		// lazy-load their FS from disk on demand. Reusing the bundle already
		// loaded here avoids a second read and the window where a service scans
		// yet vanishes from the list because a re-read failed. Resident FS peaks
		// at O(services), not O(services × versions) — that growth was the OOM.
		if svc.latest == nil || semver.LessDesc(tag, svc.latestTag) {
			svc.latest = bundle
			svc.latestTag = tag
		}

		return nil
	})

	return services
}

// Rescan re-walks the cache directory and updates the in-memory index.
// This must be called after new bundles are cached (e.g. after resolve or
// fetch-all-versions) so they become visible as first-class cached artifacts.
// The freshly built index is swapped in under the write lock so concurrent
// readers never observe a partially populated map.
func (s *CacheSource) Rescan() {
	idx := s.buildIndex()
	s.mu.Lock()
	s.services = idx
	s.mu.Unlock()
}

// snapshot returns the current service index under the read lock. The returned
// map is immutable (Rescan swaps a new map in rather than mutating), so callers
// may iterate it without holding the lock.
func (s *CacheSource) snapshot() map[string]*cachedService {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.services
}

// ServiceCount returns the number of discovered services.
func (s *CacheSource) ServiceCount() int {
	return len(s.snapshot())
}

// VersionCount returns the total number of cached bundle versions.
func (s *CacheSource) VersionCount() int {
	total := 0
	for _, svc := range s.snapshot() {
		total += len(svc.versions)
	}
	return total
}

// ListServices returns one entry per service found in the on-disk OCI cache,
// using each service's latest cached version, sorted by name.
func (s *CacheSource) ListServices(_ context.Context) ([]Service, error) {
	var services []Service
	for _, svc := range s.snapshot() {
		if svc.latest == nil {
			continue
		}
		service := ServiceFromContract(svc.latest.Contract, "oci")
		service.ContractStatus = contractStatusFromBundle(svc.latest)
		services = append(services, service)
	}

	sort.Slice(services, func(i, j int) bool {
		return services[i].Name < services[j].Name
	})
	return services, nil
}

// GetService returns details for the latest cached version of name from the
// on-disk OCI cache, or an error if it is not cached.
func (s *CacheSource) GetService(_ context.Context, name string) (*ServiceDetails, error) {
	svc, ok := s.snapshot()[name]
	if !ok {
		return nil, fmt.Errorf("service %q not found in OCI cache", name)
	}
	if svc.latest == nil {
		return nil, fmt.Errorf("no versions found for %q in OCI cache", name)
	}
	return ServiceDetailsFromBundle(svc.latest, "oci"), nil
}

// GetVersions returns all cached versions of name (latest first) with contract
// hash, classification, and the on-disk materialization time as createdAt.
func (s *CacheSource) GetVersions(ctx context.Context, name string) ([]Version, error) {
	svc, ok := s.snapshot()[name]
	if !ok {
		return nil, fmt.Errorf("service %q not found in OCI cache", name)
	}

	sorted := svc.sortedVersions() // descending: latest first
	// Build bundle pairs for classification. The latest is resident; historical
	// versions are lazy-loaded from disk (nil on read error → pair skipped).
	pairs := make([]BundlePair, len(sorted))
	for i, v := range sorted {
		b, _ := svc.bundle(v.tag)
		pairs[i] = BundlePair{Tag: v.tag, Bundle: b}
	}
	classifications := ClassifyVersions(ctx, pairs)
	var versions []Version
	for _, v := range sorted {
		ver := Version{
			Version: v.tag,
			Ref:     v.ref,
		}
		// Compute contract hash from raw YAML (always resident, small).
		if len(v.rawYAML) > 0 {
			h := sha256.Sum256(v.rawYAML)
			ver.ContractHash = hex.EncodeToString(h[:])
		}
		// Use bundle.tar.gz file modification time as createdAt.
		// Note: this is the local materialization time (when the bundle was
		// pulled/cached to disk), not the registry push time.
		if info, err := os.Stat(filepath.Join(v.dir, oci.CachedBundleFile)); err == nil {
			t := info.ModTime()
			ver.CreatedAt = &t
		}
		ver.Classification = classifications[v.tag]
		versions = append(versions, ver)
	}
	return versions, nil
}

// GetDiff compares two versions read from the on-disk OCI cache, erroring if
// either version is not cached.
func (s *CacheSource) GetDiff(ctx context.Context, a, b Ref) (*DiffResult, error) {
	idx := s.snapshot()
	svcA, ok := idx[a.Name]
	if !ok {
		return nil, fmt.Errorf("service %q not found in OCI cache", a.Name)
	}
	bundleA, err := svcA.bundle(a.Version)
	if err != nil {
		return nil, fmt.Errorf("version %q of %q not found in OCI cache", a.Version, a.Name)
	}

	svcB, ok := idx[b.Name]
	if !ok {
		return nil, fmt.Errorf("service %q not found in OCI cache", b.Name)
	}
	bundleB, err := svcB.bundle(b.Version)
	if err != nil {
		return nil, fmt.Errorf("version %q of %q not found in OCI cache", b.Version, b.Name)
	}

	return ComputeDiff(ctx, a, b, bundleA, bundleB), nil
}

// GetServiceVersion returns details for a specific cached version from the
// on-disk OCI cache, or an error if that version is not cached.
func (s *CacheSource) GetServiceVersion(_ context.Context, ref Ref) (*ServiceDetails, error) {
	svc, ok := s.snapshot()[ref.Name]
	if !ok {
		return nil, fmt.Errorf("service %q not found in OCI cache", ref.Name)
	}
	b, err := svc.bundle(ref.Version)
	if err != nil {
		return nil, fmt.Errorf("version %q of %q not found in OCI cache", ref.Version, ref.Name)
	}
	return ServiceDetailsFromBundle(b, "oci"), nil
}

func (svc *cachedService) sortedVersions() []cachedVersion {
	sorted := make([]cachedVersion, len(svc.versions))
	copy(sorted, svc.versions)
	sort.Slice(sorted, func(i, j int) bool {
		return semver.LessDesc(sorted[i].tag, sorted[j].tag)
	})
	return sorted
}

func (svc *cachedService) findVersion(tag string) *cachedVersion {
	for i, v := range svc.versions {
		if v.tag == tag {
			return &svc.versions[i]
		}
	}
	return nil
}

// The bundle archive is read by [oci.ReadCacheEntry], which is also where the
// tar-bomb, traversal and non-regular-entry limits live. This package used to
// carry its own copy of that reader; it no longer opens a cache archive by
// pathname at all, and a second implementation of the same limits is a second
// place for them to drift.
