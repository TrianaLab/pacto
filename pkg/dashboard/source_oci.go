package dashboard

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/trianalab/pacto/internal/oci"
	"github.com/trianalab/pacto/pkg/contract"
)

// OCISource implements DataSource by pulling bundles from an OCI registry.
// It discovers the full dependency tree progressively in the background,
// returning whatever has been discovered so far on each ListServices call.
type OCISource struct {
	store        oci.BundleStore
	repos        []string // OCI repository references to scan
	shallowCount int      // number of repos to scan synchronously (the rest go to background)

	mu       sync.RWMutex
	repoMap  map[string]string // service name -> repo
	services []Service         // discovered so far
	started  bool              // background discovery launched
	done     chan struct{}     // closed when background discovery completes

	onDiscover func() // called when a new service is discovered (cache invalidation)
}

// NewOCISource creates a data source backed by OCI registries.
// repos is a list of OCI repository references (e.g., "ghcr.io/org/service").
func NewOCISource(store oci.BundleStore, repos []string) *OCISource {
	return &OCISource{store: store, repos: repos, shallowCount: len(repos), repoMap: make(map[string]string), done: make(chan struct{})}
}

// AddRepos appends additional OCI repositories to scan. Duplicates are ignored.
// This must be called before background discovery starts.
func (s *OCISource) AddRepos(repos []string) {
	existing := make(map[string]bool, len(s.repos))
	for _, r := range s.repos {
		existing[r] = true
	}
	for _, r := range repos {
		if !existing[r] {
			s.repos = append(s.repos, r)
			existing[r] = true
		}
	}
}

// SetOnDiscover sets a callback invoked each time a new service is discovered
// in the background. Typically used to invalidate caches so the new data
// surfaces immediately on the next API call.
func (s *OCISource) SetOnDiscover(fn func()) {
	s.onDiscover = fn
}

// Discovering reports whether background dependency discovery is still running.
func (s *OCISource) Discovering() bool {
	select {
	case <-s.done:
		return false
	default:
		s.mu.RLock()
		started := s.started
		s.mu.RUnlock()
		return started
	}
}

func (s *OCISource) ListServices(ctx context.Context) ([]Service, error) {
	s.mu.Lock()
	if !s.started {
		s.started = true
		s.mu.Unlock()
		// Run synchronous shallow scan of configured repos (fast: 1 pull per repo).
		s.shallowScan(ctx)
		// Kick off background deep discovery of dependencies + version prefetch.
		go func() {
			s.backgroundDiscover(context.WithoutCancel(ctx))
			close(s.done)
		}()
	} else {
		s.mu.Unlock()
	}

	s.mu.RLock()
	out := make([]Service, len(s.services))
	copy(out, s.services)
	s.mu.RUnlock()

	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// shallowScan pulls only the originally configured repos (no recursion, no version prefetch).
// Auto-seeded repos (added via AddRepos) are deferred to backgroundDiscover.
func (s *OCISource) shallowScan(ctx context.Context) {
	for _, repo := range s.repos[:s.shallowCount] {
		s.discoverRepo(ctx, repo)
	}
}

// backgroundDiscover recursively discovers OCI dependencies from all known
// services and prefetches their version history. Runs in a background goroutine.
func (s *OCISource) backgroundDiscover(ctx context.Context) {
	visited := make(map[string]bool)

	// Mark already-discovered repos as visited.
	s.mu.RLock()
	for _, repo := range s.repoMap {
		visited[repo] = true
	}
	// Snapshot current services to iterate their deps.
	services := make([]Service, len(s.services))
	copy(services, s.services)
	s.mu.RUnlock()

	// Start with auto-seeded repos (added via AddRepos, skipped by shallowScan).
	queue := append([]string{}, s.repos[s.shallowCount:]...)

	// Collect dependency repos from initial shallow scan.
	for _, svc := range services {
		queue = append(queue, s.depReposForService(ctx, svc.Name)...)
	}

	// BFS: discover dependency repos, collecting new deps as we go.
	for len(queue) > 0 {
		repo := queue[0]
		queue = queue[1:]

		if visited[repo] {
			continue
		}
		visited[repo] = true

		name := s.discoverRepo(ctx, repo)
		if name == "" {
			continue
		}
		// Collect deps from the newly discovered service.
		queue = append(queue, s.depReposForService(ctx, name)...)
	}

	// Prefetch all versions for every discovered service (populates cache).
	s.mu.RLock()
	repos := make(map[string]string, len(s.repoMap))
	for name, repo := range s.repoMap {
		repos[name] = repo
	}
	s.mu.RUnlock()

	for _, repo := range repos {
		tags, err := s.store.ListTags(ctx, repo)
		if err != nil {
			continue
		}
		for _, tag := range filterValidSemver(tags) {
			ref := repo + ":" + tag
			if _, err := s.store.Pull(ctx, ref); err != nil {
				slog.Debug("OCI prefetch version failed", "ref", ref, "error", err)
			}
		}
	}

	slog.Debug("OCI background discovery complete", "services", len(repos))
}

// discoverRepo pulls the latest bundle from a repo and registers it.
// Returns the service name if successful, empty string otherwise.
func (s *OCISource) discoverRepo(ctx context.Context, repo string) string {
	tags, err := s.store.ListTags(ctx, repo)
	if err != nil {
		slog.Warn("OCI ListTags failed", "repo", repo, "error", err)
		return ""
	}
	if len(tags) == 0 {
		slog.Warn("OCI repo has no tags", "repo", repo)
		return ""
	}

	latest := latestTag(tags)
	ref := repo + ":" + latest

	bundle, err := s.store.Pull(ctx, ref)
	if err != nil {
		slog.Warn("OCI Pull failed", "ref", ref, "error", err)
		return ""
	}

	name := bundle.Contract.Service.Name

	s.mu.Lock()
	if _, exists := s.repoMap[name]; exists {
		s.mu.Unlock()
		return "" // already discovered via another path
	}
	s.repoMap[name] = repo

	svc := ServiceFromContract(bundle.Contract, "oci")
	svc.Phase = phaseFromBundle(bundle)
	s.services = append(s.services, svc)
	cb := s.onDiscover
	s.mu.Unlock()

	if cb != nil {
		cb()
	}
	slog.Info("OCI service discovered", "name", name, "repo", repo)

	return name
}

// depReposForService returns the OCI repo bases for a service's dependencies.
func (s *OCISource) depReposForService(ctx context.Context, name string) []string {
	bundle, err := s.findLatestBundle(ctx, name)
	if err != nil {
		return nil
	}
	var repos []string
	for _, dep := range bundle.Contract.Dependencies {
		depRepo := extractOCIRepo(dep.Ref)
		if depRepo == "" {
			continue
		}
		if oci.HasExplicitTag(depRepo) {
			depRepo = stripTag(depRepo)
		}
		repos = append(repos, depRepo)
	}
	return repos
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
	// Check cached mapping first (populated by discovery).
	s.mu.RLock()
	repo, ok := s.repoMap[name]
	s.mu.RUnlock()
	if ok {
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

// extractOCIRepo extracts the OCI repository from a dependency ref.
// Returns empty string if the ref is not an OCI reference.
func extractOCIRepo(ref string) string {
	if !strings.HasPrefix(ref, "oci://") {
		return ""
	}
	return strings.TrimPrefix(ref, "oci://")
}

// stripTag removes the tag portion from an OCI ref (e.g. "repo:tag" -> "repo").
func stripTag(ref string) string {
	if idx := strings.LastIndex(ref, "@"); idx > 0 {
		return ref[:idx]
	}
	lastSlash := strings.LastIndex(ref, "/")
	lastColon := strings.LastIndex(ref, ":")
	if lastColon > lastSlash {
		return ref[:lastColon]
	}
	return ref
}
