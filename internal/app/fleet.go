package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/internal/ci"
	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/workloadmanager"
)

// errNoBundleStore marks an OCI or cache source configured without a store.
var errNoBundleStore = errors.New("no bundle store configured (registry credentials/cache unavailable)")

// bundleStoreCacheDir returns the store's on-disk cache directory when it
// exposes one, else "" (the cache source then finds nothing).
func bundleStoreCacheDir(store any) string {
	if cs, ok := store.(oci.CacheLocator); ok {
		return cs.CacheDir()
	}
	return ""
}

// FleetOptions configures how a fleet snapshot is assembled from sources. Local
// bundle roots supply contract revisions; target-state fixture files supply
// operational targets for cluster-free demos and tests; Evidence Server URLs
// supply accepted-evidence targets ingested from remote producers. Additional
// source kinds (OCI, live Kubernetes) attach here without changing callers.
type FleetOptions struct {
	LocalRoots       []string
	TargetStateFiles []string
	// EvidenceURLs are base URLs of Evidence Servers whose read-only Operational
	// Graph contribution is consumed over HTTP; each becomes a fleet source of
	// external targets. This is the ONLY way to read accepted evidence: the
	// records live in the contract registry, and only the Evidence Server holds
	// the credentials and the verification logic that make them meaningful.
	EvidenceURLs []string
	// MiraURLs are base URLs of Mira nodes whose /api/v1/map supplies observed
	// edges; operator configuration only, never taken from a request.
	MiraURLs []string
	// ObservationSources are offline OTLP/JSON trace files supplying
	// runtime-observed dependency edges; each becomes an observation source whose
	// edges Build folds into the snapshot as domain-qualified observed
	// relationships.
	ObservationSources []ObservationSourceSpec
	// CatalogRoots are contract roots whose whole dependency CLOSURE joins the
	// snapshot: each root is resolved and its declarations followed, so a bundle
	// that depends on a registry reference brings that reference in rather than
	// leaving a dangling edge. It is the same discovery `pacto mcp --root`
	// performs, through the same resolver, so the two cannot disagree.
	CatalogRoots []string
	// OCIRefs are registry references to include as published-baseline revisions.
	// Requires a configured BundleStore.
	OCIRefs []string
	// Provenance, when set, checks the signature and lists the referrers of
	// every revision the OCI source reads. One checker is shared across builds
	// so its memo outlives each refresh.
	Provenance *ProvenanceChecker
	// RegistryProbe, when set, probes each registry host the OCI source reads
	// and adds one Registry row per host to the detected providers.
	RegistryProbe fleetsrc.RegistryProbe
	// LongRunning says the process builds again later (the dashboard, pacto
	// mcp, the TUI), so a background check started now can report on a later
	// build. A one-shot command never starts the default catalog's check.
	LongRunning bool
	// ExternalCatalogRef is the organization external catalog merged over the
	// default, as oci://REPOSITORY@sha256:DIGEST; a tag is refused.
	ExternalCatalogRef string
	// IncludeCache adds every bundle in the local OCI disk cache as an offline
	// baseline revision. Requires a configured BundleStore.
	IncludeCache bool
	// IncludeK8s adds a live source that reads Pacto CRs from the current
	// Kubernetes cluster as targets. An unreachable cluster surfaces as an
	// unavailable-source limitation rather than a build failure.
	IncludeK8s bool
	// K8sNamespaces scopes the live Kubernetes source; empty means all namespaces.
	K8sNamespaces []string
	// Detected adds what the operator detected as inferred provider rows:
	// k8s:<namespace>/<name> of its ConfigMap, read with the kubeconfig, or a
	// file holding the same JSON. The source is advisory; --k8s never adds it.
	Detected string
	// Fingerprints is a file (YAML or JSON) of the installation's workload
	// managers and promoters; each promoter found is a promotion row of the
	// workload source. Empty adds none.
	Fingerprints    string
	FreshnessWindow time.Duration
	Concurrency     int
	// DisallowPartial makes a single source failure fatal instead of yielding a
	// partial snapshot with explicit limitations.
	DisallowPartial bool
	// Now overrides the build clock (freshness classification); defaults to
	// time.Now. Injected in tests for deterministic staleness.
	Now func() time.Time
	// extraSources join the configured ones. Impact uses it to add the new
	// revision when no configured source holds the changed service.
	extraSources []fleet.Source
}

// ConsumerSources names, one printable entry each, the sources set in o that
// can name a consumer of a changed service, redacted: the one place that
// says which source flags can. LocalRoots never counts (it is the repository
// itself), and whether a source actually answered is read from the snapshot.
func (o FleetOptions) ConsumerSources() []string {
	var out []string
	add := func(flag string, values ...string) {
		for _, v := range values {
			out = append(out, ci.Redact(strings.TrimSpace(flag+" "+v)))
		}
	}
	add("--root", o.CatalogRoots...)
	add("--oci", o.OCIRefs...)
	if o.IncludeCache {
		add("--cache", "")
	}
	if o.IncludeK8s {
		ns := "(all namespaces)"
		if len(o.K8sNamespaces) > 0 {
			ns = "(namespace " + strings.Join(o.K8sNamespaces, ",") + ")"
		}
		add("--k8s", ns)
	}
	add("--evidence-url", o.EvidenceURLs...)
	add("--mira-source", o.MiraURLs...)
	add("--target-state", o.TargetStateFiles...)
	for _, spec := range o.ObservationSources {
		add("--traces", spec.Path)
	}
	return out
}

// Fleet builds an immutable [fleet.FleetSnapshot] from the configured sources.
// The snapshot is a pure read model; query it with [fleet.NewQuery]. Source
// failures are surfaced as explicit limitations unless DisallowPartial is set.
func (s *Service) Fleet(ctx context.Context, opts FleetOptions) (*fleet.FleetSnapshot, error) {
	cat, catLims, err := s.externalCatalog(ctx, opts)
	if err != nil {
		return nil, err
	}
	var sources []fleet.Source
	for i, root := range opts.LocalRoots {
		local := fleetsrc.NewLocalSource(sourceID("local", i, len(opts.LocalRoots)), root)
		local.Catalog = cat
		sources = append(sources, local)
	}
	if len(opts.CatalogRoots) > 0 {
		sources = append(sources, &catalogSource{id: "catalog", svc: s, roots: opts.CatalogRoots})
	}
	for i, path := range opts.TargetStateFiles {
		sources = append(sources, fleetsrc.NewTargetStateFileSource(sourceID("target-state", i, len(opts.TargetStateFiles)), path))
	}
	// A malformed --detected ref is a usage error, refused before any read.
	remote, err := httpSources(opts)
	fp, fpErr := loadFingerprints(opts.Fingerprints)
	if err := errors.Join(err, CheckDetectedRef(opts.Detected), fpErr); err != nil {
		return nil, err
	}
	sources = append(sources, remote...)
	for _, spec := range opts.ObservationSources {
		sources = append(sources, fleetsrc.NewObservationSource(spec.ID, spec.Root, spec.Path))
	}
	if len(opts.OCIRefs) > 0 {
		if s.BundleStore != nil {
			src := fleetsrc.NewOCISource("oci", s.BundleStore, opts.OCIRefs)
			if opts.Provenance != nil {
				src.Provenance = opts.Provenance.Check
			}
			src.RegistryProbe = opts.RegistryProbe
			sources = append(sources, src)
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
		contextName := s.env().kubeContext()
		id := cmp.Or(contextName, "k8s")
		client, err := s.env().newK8s()
		if err != nil {
			// An unreachable cluster (no kubeconfig, no API server) surfaces as an
			// unavailable source, consistent with the other source kinds; both
			// the Pacto and the workload source read unavailable.
			sources = append(sources, fleet.NewFailingSource(id, fleet.SourceKindKubernetes, err),
				fleet.NewFailingSource(id+workloadsSuffix, fleet.SourceKindKubernetesWorkloads, err))
		} else {
			sources = append(sources, liveK8sSources(ctx, contextName, client, opts.K8sNamespaces, fp.Promoters)...)
		}
	}
	sources = append(sources, s.detectionSources(opts.Detected)...)
	sources = append(sources, opts.extraSources...)
	if err := checkSourceIDsAreUnique(sources); err != nil {
		return nil, err
	}
	return fleet.Build(ctx, fleet.BuildOptions{
		ExternalCatalog:            cat,
		ExternalCatalogLimitations: catLims,
		Now:                        opts.Now,
		FreshnessWindow:            opts.FreshnessWindow,
		Concurrency:                opts.Concurrency,
		DisallowPartial:            opts.DisallowPartial,
		// The product answers say when freshness was not checked; the delivery
		// planner's own build leaves this false.
		DiscloseUncheckedFreshness: true,
	}, sources...)
}

// CheckDetectedRef refuses a --detected k8s: reference that does not name
// one ConfigMap as k8s:<namespace>/<name>. Any other value is a file, checked
// when it is read.
func CheckDetectedRef(ref string) error {
	rest, ok := strings.CutPrefix(ref, "k8s:")
	if !ok {
		return nil
	}
	if ns, name, _ := strings.Cut(rest, "/"); ns == "" || name == "" || strings.Contains(name, "/") {
		return fmt.Errorf("--detected %q is not k8s:<namespace>/<name>", ref)
	}
	return nil
}

// detectionSources is the advisory detection source of ref, checked by
// [CheckDetectedRef], none when ref is empty: k8s:NS/NAME reads that
// ConfigMap, anything else is a file. A dynamic client that cannot be built
// is an unavailable source that says so.
func (s *Service) detectionSources(ref string) []fleet.Source {
	const id = "detection"
	rest, ok := strings.CutPrefix(ref, "k8s:")
	switch {
	case ref == "":
		return nil
	case !ok:
		return []fleet.Source{fleetsrc.NewDetectionFileSource(id, ref, s.env().now)}
	}
	ns, name, _ := strings.Cut(rest, "/")
	dyn, err := s.env().newDynamic()
	if err != nil {
		return []fleet.Source{fleetsrc.NewDetectionFailedSource(id, fmt.Errorf("%w: %w", fleetsrc.ErrDetectionNoCluster, err))}
	}
	return []fleet.Source{fleetsrc.NewDetectionK8sSource(id, dyn, ns, name, s.env().now)}
}

// workloadsSuffix names a cluster's workload source after its Pacto source.
const workloadsSuffix = "-workloads"

// liveK8sSources are a cluster's two live sources: its Pacto CRs and its
// workloads. Each is named by the cluster UID when the cluster identity can be
// read, so two contexts on one cluster are one source. When it cannot be read
// (no get on namespaces/kube-system) the sources keep the context name ("k8s"
// without one) and report no cluster.
func liveK8sSources(ctx context.Context, contextName string, client k8sclient.K8sClient, namespaces []string, promoters []workloadmanager.Fingerprint) []fleet.Source {
	id, cluster := cmp.Or(contextName, "k8s"), (*fleet.Cluster)(nil)
	if ident, err := client.ClusterIdentity(ctx, contextName); err == nil {
		id, cluster = ident.ID, &fleet.Cluster{ID: ident.ID, Name: ident.Name, NameSource: ident.NameSource}
	}
	return []fleet.Source{
		fleetsrc.NewK8sSource(id, cluster, client, namespaces),
		fleetsrc.NewWorkloadSource(id+workloadsSuffix, cluster, client, namespaces).WithPromoters(promoters),
	}
}

// loadFingerprints reads the --fingerprints file, none when path is empty.
// A file that is unreadable or invalid is a usage error, refused before any
// source is read.
func loadFingerprints(path string) (workloadmanager.Fingerprints, error) {
	if path == "" {
		return workloadmanager.Fingerprints{}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return workloadmanager.Fingerprints{}, fmt.Errorf("--fingerprints: %w", err)
	}
	defer func() { _ = f.Close() }()
	fp, err := workloadmanager.ParseFingerprints(f)
	if err != nil {
		return workloadmanager.Fingerprints{}, fmt.Errorf("--fingerprints %s: %w", path, err)
	}
	return fp, nil
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

// httpSources returns the Evidence Server sources, then the Mira sources. Every
// Mira URL is checked first: a bad one fails the whole build, like a duplicate
// source id.
func httpSources(opts FleetOptions) ([]fleet.Source, error) {
	var sources []fleet.Source
	for i, url := range opts.EvidenceURLs {
		sources = append(sources, fleetsrc.NewEvidenceHTTPSource(sourceID("evidence-http", i, len(opts.EvidenceURLs)), url))
	}
	for i, u := range opts.MiraURLs {
		if err := fleetsrc.CheckMiraURL(u); err != nil {
			return nil, err
		}
		sources = append(sources, fleetsrc.NewMiraSource(sourceID("mira", i, len(opts.MiraURLs)), u))
	}
	return sources, nil
}

// sourceID returns a stable provenance id, suffixing the index only when more
// than one source of a kind is configured so single-source ids stay clean.
func sourceID(kind string, i, total int) string {
	if total <= 1 {
		return kind
	}
	return fmt.Sprintf("%s-%d", kind, i+1)
}
