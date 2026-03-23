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
	"testing/fstest"

	"github.com/Masterminds/semver/v3"

	"github.com/trianalab/pacto/pkg/contract"
)

// CacheSource implements DataSource by reading bundles from the on-disk OCI
// cache at ~/.cache/pacto/oci/. It discovers pre-existing cached bundles
// without requiring network access or explicit --repo flags.
//
// The cache layout is: <cacheDir>/<repo>/<tag>/bundle.tar.gz
// e.g. ~/.cache/pacto/oci/ghcr.io/org/service/1.0.0/bundle.tar.gz
type CacheSource struct {
	cacheDir string // e.g. ~/.cache/pacto/oci

	// Populated at scan time.
	services map[string]*cachedService // keyed by service name
}

type cachedService struct {
	name     string
	versions []cachedVersion
}

type cachedVersion struct {
	tag    string
	repo   string // full repo path relative to cacheDir
	path   string // absolute path to bundle.tar.gz
	bundle *contract.Bundle
}

// NewCacheSource scans the OCI cache directory for existing bundles.
// If the directory doesn't exist or contains no bundles, it returns a source
// that reports zero services.
func NewCacheSource(cacheDir string) *CacheSource {
	s := &CacheSource{
		cacheDir: cacheDir,
		services: make(map[string]*cachedService),
	}
	s.scan()
	return s
}

// scan walks the cache directory and indexes all discovered bundles.
func (s *CacheSource) scan() {
	if _, err := os.Stat(s.cacheDir); os.IsNotExist(err) {
		return
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

		svc, ok := s.services[name]
		if !ok {
			svc = &cachedService{name: name}
			s.services[name] = svc
		}

		svc.versions = append(svc.versions, cachedVersion{
			tag:    tag,
			repo:   repo,
			path:   path,
			bundle: bundle,
		})

		return nil
	})
}

// ServiceCount returns the number of discovered services.
func (s *CacheSource) ServiceCount() int {
	return len(s.services)
}

// VersionCount returns the total number of cached bundle versions.
func (s *CacheSource) VersionCount() int {
	total := 0
	for _, svc := range s.services {
		total += len(svc.versions)
	}
	return total
}

func (s *CacheSource) ListServices(_ context.Context) ([]Service, error) {
	var services []Service
	for _, svc := range s.services {
		latest := svc.latestVersion()
		if latest == nil {
			continue
		}
		service := ServiceFromContract(latest.bundle.Contract, "cache")
		service.Phase = phaseFromBundle(latest.bundle)
		services = append(services, service)
	}

	sort.Slice(services, func(i, j int) bool {
		return services[i].Name < services[j].Name
	})
	return services, nil
}

func (s *CacheSource) GetService(_ context.Context, name string) (*ServiceDetails, error) {
	svc, ok := s.services[name]
	if !ok {
		return nil, fmt.Errorf("service %q not found in OCI cache", name)
	}
	latest := svc.latestVersion()
	if latest == nil {
		return nil, fmt.Errorf("no versions found for %q in OCI cache", name)
	}
	return ServiceDetailsFromBundle(latest.bundle, "cache"), nil
}

func (s *CacheSource) GetVersions(_ context.Context, name string) ([]Version, error) {
	svc, ok := s.services[name]
	if !ok {
		return nil, fmt.Errorf("service %q not found in OCI cache", name)
	}

	var versions []Version
	for _, v := range svc.sortedVersions() {
		ver := Version{
			Version: v.tag,
			Ref:     v.repo + ":" + v.tag,
		}
		// Compute contract hash from raw YAML
		if v.bundle != nil && len(v.bundle.RawYAML) > 0 {
			h := sha256.Sum256(v.bundle.RawYAML)
			ver.ContractHash = hex.EncodeToString(h[:])
		}
		// Use bundle.tar.gz file modification time as created date
		if info, err := os.Stat(v.path); err == nil {
			t := info.ModTime()
			ver.CreatedAt = &t
		}
		versions = append(versions, ver)
	}
	return versions, nil
}

func (s *CacheSource) GetDiff(_ context.Context, a, b Ref) (*DiffResult, error) {
	svcA, ok := s.services[a.Name]
	if !ok {
		return nil, fmt.Errorf("service %q not found in OCI cache", a.Name)
	}
	bundleA := svcA.findVersion(a.Version)
	if bundleA == nil {
		return nil, fmt.Errorf("version %q of %q not found in OCI cache", a.Version, a.Name)
	}

	svcB, ok := s.services[b.Name]
	if !ok {
		return nil, fmt.Errorf("service %q not found in OCI cache", b.Name)
	}
	bundleB := svcB.findVersion(b.Version)
	if bundleB == nil {
		return nil, fmt.Errorf("version %q of %q not found in OCI cache", b.Version, b.Name)
	}

	return ComputeDiff(a, b, bundleA.bundle, bundleB.bundle), nil
}

func (svc *cachedService) latestVersion() *cachedVersion {
	sorted := svc.sortedVersions()
	if len(sorted) == 0 {
		return nil
	}
	return &sorted[0]
}

func (svc *cachedService) sortedVersions() []cachedVersion {
	sorted := make([]cachedVersion, len(svc.versions))
	copy(sorted, svc.versions)
	sort.Slice(sorted, func(i, j int) bool {
		return semverDescending(sorted[i].tag, sorted[j].tag)
	})
	return sorted
}

// semverDescending returns true if tag a should sort before tag b (latest first).
// Valid semver tags are compared properly; non-semver tags fall back to reverse lexicographic.
// Valid semver always sorts before non-semver.
func semverDescending(a, b string) bool {
	va, ea := semver.NewVersion(a)
	vb, eb := semver.NewVersion(b)
	if ea == nil && eb == nil {
		return vb.LessThan(va) // descending: latest first
	}
	if ea != nil && eb != nil {
		return a > b // fallback: reverse lexicographic
	}
	return ea == nil // valid semver sorts before non-semver
}

// latestTag returns the latest tag from a list using semver-aware sorting.
// Returns empty string if tags is empty.
func latestTag(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	sorted := make([]string, len(tags))
	copy(sorted, tags)
	sort.Slice(sorted, func(i, j int) bool {
		return semverDescending(sorted[i], sorted[j])
	})
	return sorted[0]
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
)

// extractTar reads a tar stream and returns an in-memory FS.
func extractTar(r io.Reader) (fs.FS, error) {
	memFS := fstest.MapFS{}
	tr := tar.NewReader(r)
	var totalSize int64

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading tar entry: %w", err)
		}

		name := filepath.ToSlash(strings.TrimPrefix(header.Name, "./"))
		if name == "" || name == "." {
			continue
		}
		if strings.Contains(name, "..") {
			return nil, fmt.Errorf("invalid path in tar: %s", header.Name)
		}

		if header.Typeflag == tar.TypeDir {
			memFS[name] = &fstest.MapFile{Mode: fs.ModeDir | 0755}
			continue
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
