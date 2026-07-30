package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/trianalab/pacto/v3/internal/fleetsrc"
	"github.com/trianalab/pacto/v3/internal/k8sclient"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// errNoBundleStore marks an OCI or cache source configured without a store.
var errNoBundleStore = errors.New("no bundle store configured (registry credentials/cache unavailable)")

// bundleStoreCacheDir returns the store's on-disk cache directory when it
// exposes one, else "" (the cache source then finds nothing).
func bundleStoreCacheDir(store any) string {
	if cs, ok := store.(interface{ CacheDir() string }); ok {
		return cs.CacheDir()
	}
	return ""
}

// Kubernetes access seams, overridable in tests to avoid real cluster access.
var (
	newK8sClient       = k8sclient.NewGoClient
	currentKubeContext = k8sclient.CurrentKubeContext
)

// FleetOptions configures how a fleet snapshot is assembled from sources. Local
// bundle roots supply contract revisions; target-state fixture files supply
// operational targets for cluster-free demos and tests; evidence-store
// directories supply accepted-evidence targets ingested from remote producers.
// Additional source kinds (OCI, live Kubernetes) attach here without changing
// callers.
type FleetOptions struct {
	LocalRoots       []string
	TargetStateFiles []string
	// EvidenceStores are directories of accepted-evidence records (as written by
	// the ingestion host); each becomes a fleet source of external targets.
	EvidenceStores []string
	// EvidenceURLs are base URLs of Evidence Servers whose read-only Operational
	// Graph contribution is consumed over HTTP; each becomes a fleet source of
	// external targets without touching the server's durable bucket.
	EvidenceURLs []string
	// TraceFiles are OTLP/JSON trace files supplying runtime-observed dependency
	// edges; each becomes an observation source whose edges Build folds into the
	// snapshot as domain-qualified observed relationships.
	TraceFiles []string
	// OCIRefs are registry references to include as published-baseline revisions.
	// Requires a configured BundleStore.
	OCIRefs []string
	// IncludeCache adds every bundle in the local OCI disk cache as an offline
	// baseline revision. Requires a configured BundleStore.
	IncludeCache bool
	// IncludeK8s adds a live source that reads Pacto CRs from the current
	// Kubernetes cluster as targets. An unreachable cluster surfaces as an
	// unavailable-source limitation rather than a build failure.
	IncludeK8s bool
	// K8sNamespace scopes the live Kubernetes source; empty means all namespaces.
	K8sNamespace    string
	FreshnessWindow time.Duration
	Concurrency     int
	// DisallowPartial makes a single source failure fatal instead of yielding a
	// partial snapshot with explicit limitations.
	DisallowPartial bool
	// Now overrides the build clock (freshness classification); defaults to
	// time.Now. Injected in tests for deterministic staleness.
	Now func() time.Time
}

// Fleet builds an immutable [fleet.FleetSnapshot] from the configured sources.
// The snapshot is a pure read model; query it with [fleet.NewQuery]. Source
// failures are surfaced as explicit limitations unless DisallowPartial is set.
func (s *Service) Fleet(ctx context.Context, opts FleetOptions) (*fleet.FleetSnapshot, error) {
	var sources []fleet.Source
	for i, root := range opts.LocalRoots {
		sources = append(sources, fleetsrc.NewLocalSource(sourceID("local", i, len(opts.LocalRoots)), root))
	}
	for i, path := range opts.TargetStateFiles {
		sources = append(sources, fleetsrc.NewTargetStateFileSource(sourceID("target-state", i, len(opts.TargetStateFiles)), path))
	}
	for i, dir := range opts.EvidenceStores {
		id := sourceID("evidence-store", i, len(opts.EvidenceStores))
		// The durable source opens and recovers the bucket lazily in Collect, so an
		// unopenable/unrecoverable store surfaces as an unavailable-source
		// limitation (via a Collect error) rather than aborting the snapshot build.
		sources = append(sources, newDurableEvidenceSource(id, dir))
	}
	for i, url := range opts.EvidenceURLs {
		id := sourceID("evidence-http", i, len(opts.EvidenceURLs))
		sources = append(sources, fleetsrc.NewEvidenceHTTPSource(id, url))
	}
	for i, path := range opts.TraceFiles {
		id := sourceID("observation", i, len(opts.TraceFiles))
		sources = append(sources, fleetsrc.NewObservationSource(id, path))
	}
	if len(opts.OCIRefs) > 0 {
		if s.BundleStore != nil {
			sources = append(sources, fleetsrc.NewOCISource("oci", s.BundleStore, opts.OCIRefs))
		} else {
			sources = append(sources, fleet.NewFailingSource("oci", "oci", errNoBundleStore))
		}
	}
	if opts.IncludeCache {
		if s.BundleStore != nil {
			sources = append(sources, fleetsrc.NewCacheSource("cache", bundleStoreCacheDir(s.BundleStore), s.BundleStore))
		} else {
			sources = append(sources, fleet.NewFailingSource("cache", "cache", errNoBundleStore))
		}
	}
	if opts.IncludeK8s {
		id := currentKubeContext()
		if id == "" {
			id = "k8s"
		}
		client, err := newK8sClient()
		if err != nil {
			// An unreachable cluster (no kubeconfig, no API server) surfaces as an
			// unavailable source, consistent with the other source kinds.
			sources = append(sources, fleet.NewFailingSource(id, "kubernetes", err))
		} else {
			sources = append(sources, fleetsrc.NewK8sSource(id, client, opts.K8sNamespace))
		}
	}
	return fleet.Build(ctx, fleet.BuildOptions{
		Now:             opts.Now,
		FreshnessWindow: opts.FreshnessWindow,
		Concurrency:     opts.Concurrency,
		DisallowPartial: opts.DisallowPartial,
	}, sources...)
}

// sourceID returns a stable provenance id, suffixing the index only when more
// than one source of a kind is configured so single-source ids stay clean.
func sourceID(kind string, i, total int) string {
	if total <= 1 {
		return kind
	}
	return fmt.Sprintf("%s-%d", kind, i+1)
}
