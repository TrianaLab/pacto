package dashboard

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/trianalab/pacto/internal/oci"
	"github.com/trianalab/pacto/pkg/contract"
)

// OCISource implements DataSource by pulling bundles from an OCI registry.
type OCISource struct {
	store   oci.BundleStore
	repos   []string          // OCI repository references to scan
	repoMap map[string]string // service name -> repo (populated on first ListServices)
}

// NewOCISource creates a data source backed by OCI registries.
// repos is a list of OCI repository references (e.g., "ghcr.io/org/service").
func NewOCISource(store oci.BundleStore, repos []string) *OCISource {
	return &OCISource{store: store, repos: repos}
}

func (s *OCISource) ListServices(ctx context.Context) ([]Service, error) {
	var services []Service
	repoMap := make(map[string]string)

	for _, repo := range s.repos {
		tags, err := s.store.ListTags(ctx, repo)
		if err != nil {
			slog.Warn("OCI ListTags failed", "repo", repo, "error", err)
			continue
		}
		if len(tags) == 0 {
			slog.Warn("OCI repo has no tags", "repo", repo)
			continue
		}

		// Pull the latest tag to get service metadata.
		latest := latestTag(tags)
		ref := repo + ":" + latest

		bundle, err := s.store.Pull(ctx, ref)
		if err != nil {
			slog.Warn("OCI Pull failed", "ref", ref, "error", err)
			continue
		}

		name := bundle.Contract.Service.Name
		repoMap[name] = repo

		svc := ServiceFromContract(bundle.Contract, "oci")
		svc.Phase = phaseFromBundle(bundle)
		services = append(services, svc)
	}

	s.repoMap = repoMap

	sort.Slice(services, func(i, j int) bool {
		return services[i].Name < services[j].Name
	})

	return services, nil
}

func (s *OCISource) GetService(ctx context.Context, name string) (*ServiceDetails, error) {
	bundle, err := s.findLatestBundle(ctx, name)
	if err != nil {
		return nil, err
	}
	return ServiceDetailsFromBundle(bundle, "oci"), nil
}

func (s *OCISource) GetVersions(ctx context.Context, name string) ([]Version, error) {
	repo, err := s.findRepo(ctx, name)
	if err != nil {
		return nil, err
	}

	tags, err := s.store.ListTags(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("listing tags for %s: %w", repo, err)
	}

	// Filter to valid semver tags only, sorted descending (latest first).
	semverTags := filterValidSemver(tags)

	var versions []Version
	for _, tag := range semverTags {
		versions = append(versions, Version{
			Version: tag,
			Ref:     repo + ":" + tag,
		})
	}

	return versions, nil
}

func (s *OCISource) GetDiff(ctx context.Context, a, b Ref) (*DiffResult, error) {
	bundleA, err := s.pullRef(ctx, a)
	if err != nil {
		return nil, fmt.Errorf("pulling %v: %w", a, err)
	}
	bundleB, err := s.pullRef(ctx, b)
	if err != nil {
		return nil, fmt.Errorf("pulling %v: %w", b, err)
	}
	return ComputeDiff(a, b, bundleA, bundleB), nil
}

func (s *OCISource) pullRef(ctx context.Context, ref Ref) (*contract.Bundle, error) {
	repo, err := s.findRepo(ctx, ref.Name)
	if err != nil {
		return nil, err
	}
	ociRef := repo + ":" + ref.Version
	return s.store.Pull(ctx, ociRef)
}

func (s *OCISource) findLatestBundle(ctx context.Context, name string) (*contract.Bundle, error) {
	repo, err := s.findRepo(ctx, name)
	if err != nil {
		return nil, err
	}

	tags, err := s.store.ListTags(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("listing tags: %w", err)
	}
	if len(tags) == 0 {
		return nil, fmt.Errorf("no tags found for %s", repo)
	}

	ref := repo + ":" + latestTag(tags)
	return s.store.Pull(ctx, ref)
}

func (s *OCISource) findRepo(ctx context.Context, name string) (string, error) {
	// Check cached mapping first (populated by ListServices).
	if repo, ok := s.repoMap[name]; ok {
		return repo, nil
	}

	for _, repo := range s.repos {
		// Check if repo name ends with the service name.
		parts := strings.Split(repo, "/")
		if parts[len(parts)-1] == name {
			return repo, nil
		}

		// Otherwise, try pulling latest to match by contract name.
		tags, err := s.store.ListTags(ctx, repo)
		if err != nil || len(tags) == 0 {
			continue
		}
		bundle, err := s.store.Pull(ctx, repo+":"+latestTag(tags))
		if err != nil {
			continue
		}
		if bundle.Contract.Service.Name == name {
			return repo, nil
		}
	}
	return "", fmt.Errorf("service %q not found in configured OCI repositories", name)
}
