package dashboard

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/semver"
)

// CacheSource implements DataSource by reading materialized OCI bundles from
// the on-disk cache at ~/.cache/pacto/oci/. It is NOT a public data source —
// it exists solely as an internal backing store for OCISource, providing
// contract hash, classification, and createdAt enrichment from previously
// pulled bundles. It must never appear as a named source in the API or UI.
//
// When no live OCI registry is configured, ActiveSources() maps CacheSource
// under the "oci" key so previously cached data is still available.
//
// The cache layout is: <cacheDir>/<repo>/<tag>/bundle.tar.gz
// e.g. ~/.cache/pacto/oci/ghcr.io/org/service/1.0.0/bundle.tar.gz
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
	tag  string
	repo string // full repo path relative to cacheDir
	path string // absolute path to bundle.tar.gz
	// Lightweight, always resident (pacto.yaml is small):
	contract *contract.Contract // parsed contract
	rawYAML  []byte             // raw pacto.yaml, for ContractHash
	// The full bundle FS (openapi/sbom/schema — the bulk of the bytes) is NOT
	// retained per version; it is loaded lazily from path on demand. Only the
	// latest version's full bundle lives resident on cachedService.latest. This
	// bounds memory to O(services) instead of O(services × versions).
}

// loadBundle reads the full bundle (with FS) for this version from disk.
func (v *cachedVersion) loadBundle() (*contract.Bundle, error) {
	return loadBundleTarGz(v.path)
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

	_ = filepath.Walk(s.cacheDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if info.IsDir() || info.Name() != "bundle.tar.gz" {
			return nil
		}

		// Extract repo and tag from path structure:
		// cacheDir/ghcr.io/org/name/1.0.0/bundle.tar.gz
		// -> rel = ghcr.io/org/name/1.0.0/bundle.tar.gz
		// filepath.Rel cannot fail because path is always under s.cacheDir (Walk root).
		rel, _ := filepath.Rel(s.cacheDir, path)

		parts := strings.Split(filepath.Dir(rel), string(filepath.Separator))
		if len(parts) < 2 {
			return nil // need at least registry/name/tag
		}

		tag := parts[len(parts)-1]
		repo := strings.Join(parts[:len(parts)-1], "/")

		bundle, err := loadBundleTarGz(path)
		if err != nil {
			return nil // skip corrupt bundles
		}

		name := bundle.Contract.Service.Name

		svc, ok := services[name]
		if !ok {
			svc = &cachedService{name: name}
			services[name] = svc
		}

		svc.versions = append(svc.versions, cachedVersion{
			tag:      tag,
			repo:     repo,
			path:     path,
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
func (s *CacheSource) GetVersions(_ context.Context, name string) ([]Version, error) {
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
	classifications := ClassifyVersions(pairs)
	var versions []Version
	for _, v := range sorted {
		ver := Version{
			Version: v.tag,
			Ref:     v.repo + ":" + v.tag,
		}
		// Compute contract hash from raw YAML (always resident, small).
		if len(v.rawYAML) > 0 {
			h := sha256.Sum256(v.rawYAML)
			ver.ContractHash = hex.EncodeToString(h[:])
		}
		// Use bundle.tar.gz file modification time as createdAt.
		// Note: this is the local materialization time (when the bundle was
		// pulled/cached to disk), not the registry push time.
		if info, err := os.Stat(v.path); err == nil {
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
func (s *CacheSource) GetDiff(_ context.Context, a, b Ref) (*DiffResult, error) {
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

	return ComputeDiff(a, b, bundleA, bundleB), nil
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

// loadBundleTarGz reads a bundle.tar.gz file and parses the contract within.
func loadBundleTarGz(path string) (*contract.Bundle, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer func() { _ = gr.Close() }()

	fsys, err := extractTar(gr)
	if err != nil {
		return nil, err
	}

	rawYAML, err := fs.ReadFile(fsys, "pacto.yaml")
	if err != nil {
		return nil, err
	}

	c, err := contract.Parse(bytes.NewReader(rawYAML))
	if err != nil {
		return nil, err
	}

	return &contract.Bundle{Contract: c, RawYAML: rawYAML, FS: fsys}, nil
}

const (
	maxBundleFileSize  = 10 << 20 // 10 MB per file
	maxBundleTotalSize = 50 << 20 // 50 MB total
	maxBundleEntries   = 10000    // max number of tar entries
)

// dotDotComponent reports whether any path component is "..", i.e. a real
// traversal (as opposed to a name that merely contains "..").
func dotDotComponent(name string) bool {
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

// extractTar reads a tar stream and returns an in-memory FS.
func extractTar(r io.Reader) (fs.FS, error) {
	memFS := fstest.MapFS{}
	tr := tar.NewReader(r)
	var totalSize int64
	var entries int

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading tar entry: %w", err)
		}

		entries++
		if entries > maxBundleEntries {
			return nil, fmt.Errorf("tar has too many entries (>%d)", maxBundleEntries)
		}

		name := filepath.ToSlash(strings.TrimPrefix(header.Name, "./"))
		if name == "" || name == "." {
			continue
		}
		if dotDotComponent(name) {
			return nil, fmt.Errorf("invalid path in tar: %s", header.Name)
		}

		if header.Typeflag == tar.TypeDir {
			memFS[name] = &fstest.MapFile{Mode: fs.ModeDir | 0755}
			continue
		}
		if header.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("unsupported tar entry type %q for %s", string(header.Typeflag), name)
		}

		data, err := io.ReadAll(io.LimitReader(tr, maxBundleFileSize+1))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", name, err)
		}
		if int64(len(data)) > maxBundleFileSize {
			return nil, fmt.Errorf("file %s exceeds maximum size", name)
		}

		totalSize += int64(len(data))
		if totalSize > maxBundleTotalSize {
			return nil, fmt.Errorf("extracted bundle exceeds maximum total size")
		}

		memFS[name] = &fstest.MapFile{Data: data, Mode: 0644}
	}

	return memFS, nil
}
