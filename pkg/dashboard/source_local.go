package dashboard

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/trianalab/pacto/pkg/contract"
)

const contractFile = "pacto.yaml"

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
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, fmt.Errorf("reading directory %s: %w", s.root, err)
	}

	var services []Service
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		bundle, err := loadLocalBundle(filepath.Join(s.root, entry.Name()))
		if err != nil {
			continue // skip directories without valid contracts
		}
		svc := ServiceFromContract(bundle.Contract, "local")
		svc.Phase = phaseFromBundle(bundle)
		services = append(services, svc)
	}

	// Also check root itself for a pacto.yaml
	if bundle, err := loadLocalBundle(s.root); err == nil {
		svc := ServiceFromContract(bundle.Contract, "local")
		svc.Phase = phaseFromBundle(bundle)
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
	return []Version{
		{Version: bundle.Contract.Service.Version},
	}, nil
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
	// Check root itself
	if bundle, err := loadLocalBundle(s.root); err == nil {
		if bundle.Contract.Service.Name == name {
			return bundle, nil
		}
	}

	// Check subdirectories
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, fmt.Errorf("reading directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		bundle, err := loadLocalBundle(filepath.Join(s.root, entry.Name()))
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
