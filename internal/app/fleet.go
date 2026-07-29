package app

import (
	"context"
	"fmt"
	"time"

	"github.com/trianalab/pacto/v3/internal/fleetsrc"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// FleetOptions configures how a fleet snapshot is assembled from sources. Local
// bundle roots supply contract revisions; evidence files supply operational
// targets. Additional source kinds (OCI, live Kubernetes) attach here later
// without changing callers.
type FleetOptions struct {
	LocalRoots      []string
	EvidenceFiles   []string
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
	for i, path := range opts.EvidenceFiles {
		sources = append(sources, fleetsrc.NewEvidenceSource(sourceID("evidence", i, len(opts.EvidenceFiles)), path))
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
