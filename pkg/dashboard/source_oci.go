package dashboard

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/logging"
	"github.com/trianalab/pacto/v3/pkg/oci"
	"github.com/trianalab/pacto/v3/pkg/semver"
)

// ociRediscoverInterval controls how often background discovery re-runs
// after the initial cycle completes. Tests may override this.
var ociRediscoverInterval = 60 * time.Second

// OCISource implements DataSource by pulling bundles from an OCI registry.
// It discovers the full dependency tree progressively in the background,
// returning whatever has been discovered so far on each ListServices call.
type OCISource struct {
	store oci.BundleStore
	repos []string // OCI repository references to scan

	mu          sync.RWMutex
	repoMap     map[string]string  // service name -> repo
	failedRepos map[string]string  // repo -> failure reason (auth_failed, no_semver_tags, not_found, pull_failed)
	services    []Service          // discovered so far
	started     bool               // background discovery launched
	done        chan struct{}      // closed when the first discovery cycle completes
	stopped     chan struct{}      // closed when the background loop has fully exited
	cancel      context.CancelFunc // cancels the background loop; set when discovery starts

	onDiscover func() // called when a new service is discovered (cache invalidation)

	// repoProvider is an optional callback invoked during each background
	// discovery cycle to obtain additional repos (e.g. from k8s resolvedRefs).
	// This enables late-arriving CRDs to contribute repos even after the
	// initial startup enrichment.
	repoProvider func(ctx context.Context) []string

	// cache is an optional internal CacheSource used to enrich version data
	// (hash, createdAt, classification) from materialized bundles on disk.
	// This is an internal implementation detail — cache is never exposed as
	// a separate public source.
	cache *CacheSource
}

// NewOCISource creates a data source backed by OCI registries.
// repos is a list of OCI repository references (e.g., "ghcr.io/org/service").
func NewOCISource(store oci.BundleStore, repos []string) *OCISource {
	return &OCISource{store: store, repos: repos, repoMap: make(map[string]string), failedRepos: make(map[string]string), done: make(chan struct{}), stopped: make(chan struct{})}
}

// SetOnDiscover sets a callback invoked each time a new service is discovered
// in the background. Typically used to invalidate caches so the new data
// surfaces immediately on the next API call.
func (s *OCISource) SetOnDiscover(fn func()) {
	s.onDiscover = fn
}

// SetRepoProvider sets a callback invoked during each background discovery
// cycle to obtain additional OCI repos. This allows k8s-discovered repos
// to feed into OCI scanning even after the initial startup enrichment.
func (s *OCISource) SetRepoProvider(fn func(ctx context.Context) []string) {
	s.repoProvider = fn
}

// SetCache wires an internal CacheSource so that GetVersions can enrich
// bare tag listings with hash, createdAt, and classification data from
// materialized bundles on disk. The cache is never exposed as a public source.
func (s *OCISource) SetCache(cs *CacheSource) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache = cs
}

// RescanCache triggers a rescan of the internal CacheSource, picking up
// any newly materialized bundles on disk.
func (s *OCISource) RescanCache() {
	s.mu.RLock()
	cs := s.cache
	s.mu.RUnlock()
	if cs != nil {
		cs.Rescan()
	}
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

// ListServices returns the services discovered so far from the OCI registry,
// kicking off background discovery on first call and returning immediately
// (initially empty) while it proceeds; results are sorted by name.
func (s *OCISource) ListServices(ctx context.Context) ([]Service, error) {
	s.mu.Lock()
	if !s.started {
		s.started = true
		// Kick off discovery entirely in the background so the first
		// ListServices call returns immediately (empty list). The UI
		// shows "Discovering services…" via Discovering() until the
		// initial scan completes. shallowScan + backgroundLoop run with a
		// context detached from the triggering HTTP request, but owned by
		// the source so Close() can stop the loop on server shutdown.
		bgCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		s.cancel = cancel
		s.mu.Unlock()
		go func() {
			s.shallowScan(bgCtx)
			s.backgroundLoop(bgCtx)
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

// shallowScan pulls only the configured repos (no recursion, no version prefetch).
// This is fast — one ListTags + one Pull per repo.
func (s *OCISource) shallowScan(ctx context.Context) {
	for _, repo := range s.repos {
		s.discoverRepo(ctx, repo) // no-ops instantly once ctx is cancelled
	}
}

// backgroundLoop runs discoverAndPrefetch in a loop. The first cycle closes
// s.done (ending the "discovering" state for the UI). Subsequent cycles
// re-run periodically to pick up new services, dependencies, and versions.
func (s *OCISource) backgroundLoop(ctx context.Context) {
	defer close(s.stopped) // signal the loop has fully exited (used by Close)
	s.discoverAndPrefetch(ctx)
	close(s.done) // signal initial discovery complete

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(ociRediscoverInterval):
			s.discoverAndPrefetch(ctx)
		}
	}
}

// Close stops the background discovery loop and waits for it to exit. It is
// safe to call multiple times and safe to call when discovery never started
// (e.g. ListServices was never invoked). Wiring this into the server shutdown
// path ensures the detached discovery goroutine does not outlive the server.
func (s *OCISource) Close() {
	s.mu.Lock()
	cancel := s.cancel
	started := s.started
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if started {
		// Returns promptly: discoverRepo/depReposForService/prefetchVersions all
		// no-op the moment the context is cancelled, so the loop exits at once.
		<-s.stopped
	}
}

// discoverAndPrefetch recursively discovers OCI dependencies from all known
// services and prefetches their version history.
// On shutdown the context is cancelled; discoverRepo, depReposForService and
// prefetchVersions each no-op immediately once that happens, so this drains and
// returns promptly rather than running every remaining registry call.
func (s *OCISource) discoverAndPrefetch(ctx context.Context) {
	visited := make(map[string]bool)

	// Mark already-discovered repos as visited (skip re-pulling).
	s.mu.RLock()
	for _, repo := range s.repoMap {
		visited[repo] = true
	}
	// Snapshot current services to iterate their deps.
	services := make([]Service, len(s.services))
	copy(services, s.services)
	s.mu.RUnlock()

	// Collect dependency repos from initial shallow scan.
	var queue []string
	for _, svc := range services {
		queue = append(queue, s.depReposForService(ctx, svc.Name)...)
	}

	// Also add repos from the external provider (e.g. k8s resolvedRefs).
	// This picks up CRDs that appeared after the initial startup scan.
	if s.repoProvider != nil {
		queue = append(queue, s.repoProvider(ctx)...)
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
			logging.LoggerFromContext(ctx).Info("OCI dependency not resolved (will appear as external)", "repo", repo)
			continue
		}
		// Collect deps from the newly discovered service.
		queue = append(queue, s.depReposForService(ctx, name)...)
	}

	s.prefetchVersions(ctx)

	// Rescan the internal cache so GetVersions can enrich with hash,
	// createdAt, and classification from the newly materialized bundles.
	s.RescanCache()
	if s.onDiscover != nil {
		s.onDiscover()
	}
}

// prefetchVersions pulls every semver tag of every discovered repo into the cache.
// Bails as soon as the context is cancelled so shutdown isn't held up.
func (s *OCISource) prefetchVersions(ctx context.Context) {
	s.mu.RLock()
	repos := make(map[string]string, len(s.repoMap))
	for name, repo := range s.repoMap {
		repos[name] = repo
	}
	s.mu.RUnlock()

	for _, repo := range repos {
		if ctx.Err() != nil {
			return
		}
		tags, err := s.store.ListTags(ctx, repo)
		if err != nil {
			continue
		}
		for _, tag := range semver.Filter(tags) {
			ref := repo + ":" + tag
			if _, err := s.store.Pull(ctx, ref); err != nil {
				logging.LoggerFromContext(ctx).Debug("OCI prefetch version failed", "ref", ref, "error", err)
			}
		}
	}

	logging.LoggerFromContext(ctx).Debug("OCI background discovery complete", "services", len(repos))
}

// discoverRepo pulls the latest bundle from a repo and registers it.
// Returns the service name if successful, empty string otherwise.
// On failure, records the reason in failedRepos for graph diagnostics.
func (s *OCISource) discoverRepo(ctx context.Context, repo string) string {
	if ctx.Err() != nil {
		return ""
	}
	tags, err := s.store.ListTags(ctx, repo)
	if err != nil {
		logOCIError(ctx, "OCI ListTags failed", "repo", repo, err)
		s.recordFailure(repo, classifyOCIError(err))
		return ""
	}
	if len(tags) == 0 {
		logging.LoggerFromContext(ctx).Warn("OCI repo has no tags", "repo", repo)
		s.recordFailure(repo, "no_semver_tags")
		return ""
	}

	latest := semver.Latest(tags)
	if latest == "" {
		logging.LoggerFromContext(ctx).Warn("OCI repo has no semver tags", "repo", repo)
		s.recordFailure(repo, "no_semver_tags")
		return ""
	}
	ref := repo + ":" + latest

	bundle, err := s.store.Pull(ctx, ref)
	if err != nil {
		logOCIError(ctx, "OCI Pull failed", "ref", ref, err)
		s.recordFailure(repo, classifyOCIError(err))
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
	svc.ContractStatus = contractStatusFromBundle(bundle)
	s.services = append(s.services, svc)
	cb := s.onDiscover
	s.mu.Unlock()

	if cb != nil {
		cb()
	}
	logging.LoggerFromContext(ctx).Info("OCI service discovered", "name", name, "repo", repo)

	return name
}

// depReposForService returns the OCI repo bases for a service's dependencies
// and referenced contracts (configuration, policy).
func (s *OCISource) depReposForService(ctx context.Context, name string) []string {
	if ctx.Err() != nil {
		return nil
	}
	bundle, err := s.findLatestBundle(ctx, name)
	if err != nil {
		return nil
	}
	var refs []string
	for _, dep := range bundle.Contract.Dependencies {
		refs = append(refs, dep.Ref)
	}
	for _, cfg := range bundle.Contract.Configurations {
		refs = append(refs, cfg.Ref)
	}
	for _, pol := range bundle.Contract.Policies {
		refs = append(refs, pol.Ref)
	}
	return collectOCIRepos(refs)
}

// collectOCIRepos extracts and normalises OCI repository bases from a list
// of refs, filtering out non-OCI references and stripping explicit tags.
func collectOCIRepos(refs []string) []string {
	var repos []string
	for _, ref := range refs {
		repo := extractOCIRepo(ref)
		if repo == "" {
			continue
		}
		if oci.HasExplicitTag(repo) {
			repo = stripTag(repo)
		}
		repos = append(repos, repo)
	}
	return repos
}

// GetService returns details for name by pulling its latest-tagged bundle from
// the OCI registry, or an error if the service repo cannot be resolved.
func (s *OCISource) GetService(ctx context.Context, name string) (*ServiceDetails, error) {
	bundle, err := s.findLatestBundle(ctx, name)
	if err != nil {
		return nil, err
	}
	return ServiceDetailsFromBundle(bundle, "oci"), nil
}

// GetVersions returns the semver tags for name from the OCI registry (latest
// first), enriched with hash, createdAt, and classification from the internal
// disk cache when available.
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
	semverTags := semver.Filter(tags)

	var versions []Version
	for _, tag := range semverTags {
		versions = append(versions, Version{
			Version: tag,
			Ref:     repo + ":" + tag,
		})
	}

	// Internally enrich from materialized bundles (cache) when available.
	// This fills in hash, createdAt, and classification without exposing
	// cache as a separate public source.
	s.mu.RLock()
	cs := s.cache
	s.mu.RUnlock()
	if cs != nil {
		cacheVersions, err := cs.GetVersions(ctx, name)
		if err == nil {
			cacheByTag := make(map[string]*Version, len(cacheVersions))
			for i := range cacheVersions {
				cacheByTag[cacheVersions[i].Version] = &cacheVersions[i]
			}
			for i := range versions {
				if cv, ok := cacheByTag[versions[i].Version]; ok {
					enrichVersion(&versions[i], cv)
				}
			}
		}
	}

	return versions, nil
}

// GetDiff compares two versions by pulling each from the OCI registry, erroring
// if either ref cannot be pulled.
func (s *OCISource) GetDiff(ctx context.Context, a, b Ref) (*DiffResult, error) {
	bundleA, err := s.pullRef(ctx, a)
	if err != nil {
		return nil, fmt.Errorf("pulling %v: %w", a, err)
	}
	bundleB, err := s.pullRef(ctx, b)
	if err != nil {
		return nil, fmt.Errorf("pulling %v: %w", b, err)
	}
	return ComputeDiff(ctx, a, b, bundleA, bundleB), nil
}

// GetServiceVersion returns details for a specific ref by pulling that exact
// version's bundle from the OCI registry.
func (s *OCISource) GetServiceVersion(ctx context.Context, ref Ref) (*ServiceDetails, error) {
	bundle, err := s.pullRef(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("pulling %v: %w", ref, err)
	}
	return ServiceDetailsFromBundle(bundle, "oci"), nil
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

	ref := repo + ":" + semver.Latest(tags)
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
		bundle, err := s.store.Pull(ctx, repo+":"+semver.Latest(tags))
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

// recordFailure stores why a repo could not be discovered.
func (s *OCISource) recordFailure(repo, reason string) {
	s.mu.Lock()
	s.failedRepos[repo] = reason
	s.mu.Unlock()
}

// classifyOCIError maps an OCI error to a resolution failure reason.
func classifyOCIError(err error) string {
	var authErr *oci.AuthenticationError
	if errors.As(err, &authErr) {
		return "auth_failed"
	}
	var notFound *oci.ArtifactNotFoundError
	if errors.As(err, &notFound) {
		return "not_found"
	}
	var unreachable *oci.RegistryUnreachableError
	if errors.As(err, &unreachable) {
		return "pull_failed"
	}
	return "pull_failed"
}

// UnresolvedReason returns why a dependency ref could not be resolved during
// OCI background discovery. Returns "" if the ref is not an OCI ref, was
// successfully resolved, or discovery is still in progress for this repo.
func (s *OCISource) UnresolvedReason(depRef string) string {
	repo := extractOCIRepo(depRef)
	if repo == "" {
		return ""
	}
	if oci.HasExplicitTag(repo) {
		repo = stripTag(repo)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// If still discovering and repo isn't failed yet, report as discovering.
	if reason, ok := s.failedRepos[repo]; ok {
		return reason
	}

	select {
	case <-s.done:
		return "" // discovery complete, no recorded failure — unknown
	default:
		if s.started {
			return "discovering"
		}
		return ""
	}
}

// logOCIError logs an OCI operation error at the appropriate level.
// Auth errors are logged at ERROR (actionable), others at WARN.
func logOCIError(ctx context.Context, msg, key, val string, err error) {
	lg := logging.LoggerFromContext(ctx)
	var authErr *oci.AuthenticationError
	if errors.As(err, &authErr) {
		lg.Error(msg, key, val, "error", err)
	} else {
		lg.Warn(msg, key, val, "error", err)
	}
}

func stripTag(ref string) string {
	// Strip digest first (@sha256:...).
	if idx := strings.LastIndex(ref, "@"); idx > 0 {
		ref = ref[:idx]
	}
	// Strip tag (":version" after the last slash).
	lastSlash := strings.LastIndex(ref, "/")
	lastColon := strings.LastIndex(ref, ":")
	if lastColon > lastSlash {
		return ref[:lastColon]
	}
	return ref
}

// RepoProviderFromSource returns a repoProvider callback that discovers OCI
// repos by querying a DataSource for resolvedRef / imageRef fields. Use this
// to wire k8s-discovered repos into OCI background scanning.
func RepoProviderFromSource(src DataSource) func(ctx context.Context) []string {
	return func(ctx context.Context) []string {
		repos, _ := discoverOCIReposFromSource(ctx, src)
		return repos
	}
}

// ContractRefProviderFromSource returns a callback reporting the contract
// references a runtime source currently attributes to its services: each exact
// resolvedRef the operator resolved, AND the repository that ref names.
//
// Both are needed, and they answer different questions. The exact ref names the
// revision a target is RUNNING — immutable when the operator pinned a digest, and
// the only reference that can make a target's revision link exact rather than
// inferred. The repository resolves to the newest PUBLISHED revision, which is
// what makes "a newer revision exists" and a change analysis against it
// answerable at all. Reporting only repositories, as OCI background discovery
// does, drops the running revision from the graph whenever it is not also the
// latest tag — precisely the case an operator pins a digest to create.
//
// A source failure yields no references rather than an error: the caller
// assembles snapshot sources, where a cluster that cannot be read right now is
// the absence of contributed references, not a reason to abandon the snapshot.
func ContractRefProviderFromSource(src DataSource) func(ctx context.Context) []string {
	return func(ctx context.Context) []string {
		resolved, err := resolvedRefsFromSource(ctx, src)
		if err != nil {
			return nil
		}
		refs := make([]string, 0, len(resolved)*2)
		seen := make(map[string]bool, len(resolved)*2)
		for _, ref := range resolved {
			for _, candidate := range []string{ref, stripTag(ref)} {
				if candidate != "" && !seen[candidate] {
					seen[candidate] = true
					refs = append(refs, candidate)
				}
			}
		}
		return refs
	}
}

// discoverOCIReposFromSource queries a DataSource for services and extracts
// unique OCI repository references from their resolvedRef / imageRef fields.
func discoverOCIReposFromSource(ctx context.Context, src DataSource) ([]string, error) {
	resolved, err := resolvedRefsFromSource(ctx, src)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var repos []string
	for _, ref := range resolved {
		repo := stripTag(ref)
		if repo != "" && !seen[repo] {
			seen[repo] = true
			repos = append(repos, repo)
		}
	}
	return repos, nil
}

// resolvedRefsFromSource lists the non-empty resolvedRef of every service a
// source reports, in source order. A service the source cannot detail is
// skipped: one unreadable service must not hide the rest.
func resolvedRefsFromSource(ctx context.Context, src DataSource) ([]string, error) {
	services, err := src.ListServices(ctx)
	if err != nil {
		logging.LoggerFromContext(ctx).Warn("OCI repo discovery: failed to list services", "error", err)
		return nil, err
	}
	var refs []string
	for _, svc := range services {
		d, err := src.GetService(ctx, svc.Name)
		if err != nil || d == nil || d.ResolvedRef == "" {
			continue
		}
		refs = append(refs, d.ResolvedRef)
	}
	return refs, nil
}
