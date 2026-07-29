package app

import (
	"context"
	"fmt"
	"time"

	"github.com/trianalab/pacto/v3/internal/fleetsrc"
	"github.com/trianalab/pacto/v3/internal/k8sclient"
	"github.com/trianalab/pacto/v3/pkg/evidenceingest"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

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
		store, err := evidenceingest.NewFileStore(dir)
		if err != nil {
			// Consistent with the other sources, a store we cannot open becomes a
			// failing source (surfaced as an unavailable-source limitation) rather
			// than aborting the whole snapshot build.
			sources = append(sources, fleet.NewFailingSource(id, "evidence-ingest", err))
			continue
		}
		sources = append(sources, evidenceingest.NewSource(id, store))
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
