package dashboard

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/trianalab/pacto/pkg/contract"
)

const contractFile = "pacto.yaml"

// maxLocalScanDepth bounds how deep local discovery walks for pacto.yaml so a
// large repo root (e.g. bundles/<svc>/<version>/pacto.yaml) is found without
// scanning the entire tree.
const maxLocalScanDepth = 5

// localBundleDirs returns directories at or under root that contain a pacto.yaml,
// walking up to maxLocalScanDepth levels and skipping hidden/vendor dirs. Order
// is deterministic (root first, then sorted subdirectories, depth-first).
func localBundleDirs(root string) []string {
	var dirs []string
	collectBundleDirs(root, 0, &dirs)
	return dirs
}

// skipScanDir reports whether a directory name must be excluded from local
// bundle discovery. Hidden directories (e.g. .git, .Trash, .cache) and dependency
// vendoring directories are never project bundle roots, and descending into them
// from a large root (such as $HOME) makes discovery walk an enormous tree. Both
// the recursive walk (collectBundleDirs) and source detection (detectLocal) use
// this so a pacto.yaml under such a directory neither activates nor is scanned.
func skipScanDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor"
}

func collectBundleDirs(dir string, depth int, out *[]string) {
	if depth > maxLocalScanDepth {
		return
	}
	if _, err := os.Stat(filepath.Join(dir, contractFile)); err == nil {
		*out = append(*out, dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if skipScanDir(e.Name()) {
			continue
		}
		collectBundleDirs(filepath.Join(dir, e.Name()), depth+1, out)
	}
}

// LocalSource implements DataSource by reading from the local filesystem.
// It scans a root directory for subdirectories containing pacto.yaml files.
type LocalSource struct {
	root string
}

// NewLocalSource creates a data source backed by local filesystem directories.
// root is the directory to scan for service subdirectories.
func NewLocalSource(root string) *LocalSource {
	return &LocalSource{root: root}
}

func (s *LocalSource) ListServices(_ context.Context) ([]Service, error) {
	if _, err := os.ReadDir(s.root); err != nil {
		return nil, fmt.Errorf("reading directory %s: %w", s.root, err)
	}

	seen := make(map[string]bool)
	var services []Service
	for _, dir := range localBundleDirs(s.root) {
		bundle, err := loadLocalBundle(dir)
		if err != nil {
			continue // skip directories without valid contracts
		}
		name := bundle.Contract.Service.Name
		if seen[name] {
			continue // first match wins (e.g. multiple versioned dirs of one service)
		}
		seen[name] = true
		svc := ServiceFromContract(bundle.Contract, "local")
		svc.ContractStatus = contractStatusFromBundle(bundle)
		services = append(services, svc)
	}

	sort.Slice(services, func(i, j int) bool {
		return services[i].Name < services[j].Name
	})

	return services, nil
}

func (s *LocalSource) GetService(_ context.Context, name string) (*ServiceDetails, error) {
	bundle, err := s.findBundle(name)
	if err != nil {
		return nil, err
	}
	return ServiceDetailsFromBundle(bundle, "local"), nil
}

func (s *LocalSource) GetVersions(_ context.Context, name string) ([]Version, error) {
	bundle, err := s.findBundle(name)
	if err != nil {
		return nil, err
	}
	// Local source only knows about the current version on disk.
	v := Version{Version: bundle.Contract.Service.Version}
	if len(bundle.RawYAML) > 0 {
		h := sha256.Sum256(bundle.RawYAML)
		v.ContractHash = hex.EncodeToString(h[:])
	}
	return []Version{v}, nil
}

func (s *LocalSource) GetDiff(_ context.Context, a, b Ref) (*DiffResult, error) {
	bundleA, err := s.findBundle(a.Name)
	if err != nil {
		return nil, fmt.Errorf("loading %q: %w", a.Name, err)
	}
	bundleB, err := s.findBundle(b.Name)
	if err != nil {
		return nil, fmt.Errorf("loading %q: %w", b.Name, err)
	}
	return ComputeDiff(a, b, bundleA, bundleB), nil
}

func (s *LocalSource) findBundle(name string) (*contract.Bundle, error) {
	for _, dir := range localBundleDirs(s.root) {
		bundle, err := loadLocalBundle(dir)
		if err != nil {
			continue
		}
		if bundle.Contract.Service.Name == name {
			return bundle, nil
		}
	}
	return nil, fmt.Errorf("service %q not found in %s", name, s.root)
}

func loadLocalBundle(dir string) (*contract.Bundle, error) {
	filePath := filepath.Join(dir, contractFile)
	rawYAML, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	c, err := contract.Parse(bytes.NewReader(rawYAML))
	if err != nil {
		return nil, err
	}
	return &contract.Bundle{
		Contract: c,
		RawYAML:  rawYAML,
		FS:       os.DirFS(dir),
	}, nil
}
