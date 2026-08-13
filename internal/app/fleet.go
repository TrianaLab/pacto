package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
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
	// ObservationSources are offline OTLP/JSON trace files supplying
	// runtime-observed dependency edges; each becomes an observation source whose
	// edges Build folds into the snapshot as domain-qualified observed
	// relationships.
	ObservationSources []ObservationSourceSpec
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
	for _, spec := range opts.ObservationSources {
		sources = append(sources, fleetsrc.NewObservationSource(spec.ID, spec.Root, spec.Path))
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
			sources = append(sources, fleetsrc.NewCacheSource("cache", bundleStoreCacheDir(s.BundleStore)))
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
	if err := checkSourceIDsAreUnique(sources); err != nil {
		return nil, err
	}
	return fleet.Build(ctx, fleet.BuildOptions{
		Now:             opts.Now,
		FreshnessWindow: opts.FreshnessWindow,
		Concurrency:     opts.Concurrency,
		DisallowPartial: opts.DisallowPartial,
	}, sources...)
}

// checkSourceIDsAreUnique refuses to build a snapshot whose sources do not have
// one id each.
//
// A source id is not decoration: it is the Data Source key the Product publishes,
// and everything a source contributed is attributed to it under that key. Two
// sources sharing one key means one of them is unaddressable — a detail lookup
// answers with whichever the snapshot happens to hold first — while their
// attribution and their limitations pile up under a single identity that belongs
// to neither. There is no honest repair: renaming one silently makes the
// configuration mean something the operator did not write, and serving both makes
// the Product's answer depend on assembly order.
//
// The check lives here, over the FINAL assembled set, because that is the only
// place the whole namespace is known. Ids come from four different places — a
// declared observation name, a positional suffix, a fixed kind name, and the
// ambient kubeconfig context (which in a pod with no context falls back to
// "k8s", exactly where a source declared "k8s" would land on top of it). A rule
// written anywhere earlier would be a guess about the other sources.
//
// [fleet.Build] keeps reporting a duplicate as a DUPLICATE_SOURCE_ID limitation
// for callers that assemble their own sources; the difference is that a Pacto
// configuration is something we can refuse to run, so it never reaches a Product.
func checkSourceIDsAreUnique(sources []fleet.Source) error {
	kinds := make(map[string][]string, len(sources))
	var collided []string
	for _, src := range sources {
		id := src.ID()
		if len(kinds[id]) == 1 {
			collided = append(collided, id)
		}
		kinds[id] = append(kinds[id], src.Kind())
	}
	if len(collided) == 0 {
		return nil
	}
	// Sorted, so the same misconfiguration reports the same error however its
	// entries were ordered.
	slices.Sort(collided)
	details := make([]string, 0, len(collided))
	for _, id := range collided {
		details = append(details, fmt.Sprintf("%q is claimed by %s", id, strings.Join(kinds[id], " and ")))
	}
	return fmt.Errorf(
		"configured data sources do not have one identity each: %s; rename a source so every data source has its own name",
		strings.Join(details, "; "),
	)
}

// ObservationSourceSpec is one offline OTLP/JSON trace file contributed as an
// observation source under an EXPLICIT provenance id.
//
// The id, not the file path and not the position in a list, is the identity
// consumers see (the fleet [fleet.SourceState] id, and through it the Product
// Data Source). Keeping the two apart is what makes a declarative configuration
// safe: reordering entries never renames a source, and two sources whose files
// happen to share a basename never merge. The path is configuration — where the
// bytes happen to live right now.
type ObservationSourceSpec struct {
	ID   string
	Path string
	// Root is the directory this source is allowed to read inside, with Path
	// resolved relative to it. Set for a source whose storage Pacto does not own —
	// the operator-managed dashboard's read-only mount — so nothing the volume
	// contains can point the read at the container's own filesystem. Empty for the
	// ad-hoc command line, where Path is read as given.
	Root string
}

// TraceFileSources adapts path-only trace inputs (the ad-hoc `--traces`
// convenience) to observation sources with the positional ids `observation`,
// `observation-1`, ... Positional ids are honest for a one-off command line and
// nowhere else: anything declarative — notably the operator-managed dashboard —
// must carry explicit ids, because reordering its entries must not rewrite
// Data Source identity.
func TraceFileSources(paths []string) []ObservationSourceSpec {
	if len(paths) == 0 {
		return nil
	}
	specs := make([]ObservationSourceSpec, 0, len(paths))
	for i, p := range paths {
		specs = append(specs, ObservationSourceSpec{ID: sourceID("observation", i, len(paths)), Path: p})
	}
	return specs
}

// sourceID returns a stable provenance id, suffixing the index only when more
// than one source of a kind is configured so single-source ids stay clean.
func sourceID(kind string, i, total int) string {
	if total <= 1 {
		return kind
	}
	return fmt.Sprintf("%s-%d", kind, i+1)
}
