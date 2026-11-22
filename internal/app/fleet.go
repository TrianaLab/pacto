package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/internal/ci"
	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/detect"
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
	// K8sContexts are kubeconfig contexts read like IncludeK8s reads the
	// current one, each cluster once (also in a pod).
	K8sContexts []string
	// K8sNamespaces scopes the live Kubernetes sources; empty means all namespaces.
	K8sNamespaces []string
	// LiveEdges, set only by the dashboard, turns the trace backends the
	// operator detected into live edge sources sharing it; nil keeps them
	// detected rows (a laptop cannot reach their in-cluster URLs).
	LiveEdges *fleetsrc.LiveEdges
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
	// TargetSnapshot is one oci:// ref of a pacto-targets repository, whose
	// every cluster snapshot is read, or one or more pinned cluster-<uid> tags
	// or digests; empty reads none.
	TargetSnapshot []string
	// TargetSnapshotTrustFile is the delivery trust config the snapshots are
	// verified with; empty reads $PACTO_DELIVERY_TRUST.
	TargetSnapshotTrustFile string
	// Now overrides the build clock (freshness classification); defaults to
	// time.Now. Injected in tests for deterministic staleness.
	Now func() time.Time
	// Index names the organization index impact reads: "" derives
	// <registry>/pacto-index from the provider reference, IndexNone reads
	// none, anything else is the repository. Only impact reads it.
	Index string
	// extraSources join the configured ones. Impact uses it to add the new
	// revision when no configured source holds the changed service.
	extraSources []fleet.Source
	// indexSrc is the organization index impact reads, nil for none.
	indexSrc *indexSource
}

// hasTargetInput reports whether o names a source of targets: a cluster, a
// target-state file, an Evidence Server or a target snapshot.
func (o FleetOptions) hasTargetInput() bool {
	return o.IncludeK8s || len(o.K8sContexts) > 0 || len(o.TargetStateFiles) > 0 || len(o.EvidenceURLs) > 0 || len(o.TargetSnapshot) > 0
}

// addedSources are the sources impact adds: extraSources, then the
// organization index when it reads one.
func (o FleetOptions) addedSources() []fleet.Source {
	if o.indexSrc == nil {
		return o.extraSources
	}
	return append(slices.Clone(o.extraSources), o.indexSrc)
}

// ConsumerSources names, one printable entry each, the sources set in o that
// can name a consumer of a changed service, redacted: the one place that
// says which source flags can. LocalRoots never counts (it is the repository
// itself), and whether a source actually answered is read from the snapshot.
// Index never counts either: an index alone is never a consumer source, since
// without a target source it is CONSUMER_INDEX_UNCHECKED (509-S7 Decision 16).
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
	ns := "(all namespaces)"
	if len(o.K8sNamespaces) > 0 {
		ns = "(namespace " + strings.Join(o.K8sNamespaces, ",") + ")"
	}
	if o.IncludeK8s {
		add("--k8s", ns)
	}
	for _, c := range o.K8sContexts {
		add("--context", c+" "+ns)
	}
	add("--evidence-url", o.EvidenceURLs...)
	add("--mira-source", o.MiraURLs...)
	add("--target-state", o.TargetStateFiles...)
	add("--target-snapshot", o.TargetSnapshot...)
	if o.Detected != "" {
		// Only the workloads the operator linked with no Pacto: alone it
		// never covers the cluster (see consumerEvidence).
		add("--detected", o.Detected+" (linked workloads only)")
	}
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
	snaps, snapErr := s.targetSnapshotSources(ctx, opts)
	if err := errors.Join(err, CheckDetectedRef(opts.Detected), fpErr, snapErr); err != nil {
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
				src.OwnerRights = opts.Provenance.ownerRights(s.fleetOwnerPolicy(opts.TargetSnapshotTrustFile))
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
	if opts.IncludeK8s || len(opts.K8sContexts) > 0 {
		sources = append(sources, s.k8sSources(ctx, opts, fp.Promoters)...)
	}
	sources = append(sources, s.detectionSources(ctx, opts, slices.Concat(sources, snaps, opts.addedSources()))...)
	sources = append(sources, s.linkedSources(opts.Detected)...)
	sources = append(sources, snaps...)
	sources = append(sources, opts.addedSources()...)
	if err := checkSourceIDsAreUnique(sources); err != nil {
		return nil, err
	}
	return fleet.Build(ctx, fleet.BuildOptions{
		DeclarationSigners:         s.declarationSigners(ctx, opts),
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

// declarationSigners is the installation signer policy a declared
// revision's signer is judged with. Only the registry source checks
// provenance, so only it can show a declaration's cluster facts: without it
// none is read.
func (s *Service) declarationSigners(ctx context.Context, opts FleetOptions) *fleet.DeclarationSignerPolicy {
	if opts.Provenance == nil || len(opts.OCIRefs) == 0 || s.BundleStore == nil {
		return nil
	}
	return opts.Provenance.DeclarationSigners(ctx)
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

// detectionReader is the advisory detection source of ref, checked by
// [CheckDetectedRef], nil when ref is empty: k8s:NS/NAME reads that
// ConfigMap, anything else is a file. A dynamic client that cannot be built
// is an unavailable source that says so. It is the one reader of a
// detection: the fleet and pacto ci's installation advertisement both use it.
func (s *Service) detectionReader(ref string) *fleetsrc.DetectionSource {
	const id = "detection"
	rest, ok := strings.CutPrefix(ref, "k8s:")
	switch {
	case ref == "":
		return nil
	case !ok:
		return fleetsrc.NewDetectionFileSource(id, ref, s.env().now)
	}
	ns, name, _ := strings.Cut(rest, "/")
	dyn, err := s.env().newDynamic()
	if err != nil {
		return fleetsrc.NewDetectionFailedSource(id, fmt.Errorf("%w: %w", fleetsrc.ErrDetectionNoCluster, err))
	}
	return fleetsrc.NewDetectionK8sSource(id, dyn, ns, name, s.env().now)
}

// linkedSources are the sources of the workloads the operator linked to a
// published contract with no Pacto, read from the detection at ref (checked
// by [CheckDetectedRef]), none when ref is empty: k8s:NS/NAME reads the
// linked key of that ConfigMap; a detection file has the linked file beside
// it when one is there. A dynamic client that cannot be built is an
// unavailable source that says so.
func (s *Service) linkedSources(ref string) []fleet.Source {
	const linkedID = "linked"
	rest, ok := strings.CutPrefix(ref, "k8s:")
	switch {
	case ref == "":
		return nil
	case !ok:
		if linked := filepath.Join(filepath.Dir(ref), delivery.LinkedConfigMapKey); fileThere(linked) {
			return []fleet.Source{fleetsrc.NewLinkedFileSource(linkedID, linked, s.env().now)}
		}
		return nil
	}
	ns, name, _ := strings.Cut(rest, "/")
	dyn, err := s.env().newDynamic()
	if err != nil {
		return []fleet.Source{fleetsrc.NewLinkedFailedSource(linkedID, fmt.Errorf("%w: %w", fleetsrc.ErrDetectionNoCluster, err))}
	}
	return []fleet.Source{fleetsrc.NewLinkedK8sSource(linkedID, dyn, ns, name, s.env().now)}
}

// fileThere reports whether path names something Stat can read.
func fileThere(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// liveEdgeIDs are, per detected kind a live edge source takes, the id of that
// source. A Collector entry is taken only by the OTLP receiver, when it runs;
// the service graph only reads it.
var liveEdgeIDs = map[string]string{"jaeger": "detected-jaeger", "tempo": serviceGraphID, "otel-collector": receiverID}

// InstallationConfig reads what the operator was configured with from the
// detection at ref (as [Service.detectionReader] reads it) on each call, for
// the installation capability: nil when ref is empty.
// ponytail: one read per call (one ConfigMap GET for k8s:); cache it with
// the published snapshot if a profile shows it hot.
func (s *Service) InstallationConfig(ref string) func(context.Context) (*fleet.InstallationDeclared, fleet.SourceState) {
	src := s.detectionReader(ref)
	if src == nil {
		return nil
	}
	// A file ref is a path on this host: remote callers read a fixed label.
	label := "a detection file"
	if strings.HasPrefix(ref, "k8s:") {
		label = ref
	}
	return func(ctx context.Context) (*fleet.InstallationDeclared, fleet.SourceState) {
		res, st := src.Read(ctx)
		return res.Declared(label), st
	}
}

// serviceGraphID and receiverID are the ids of the service-graph and OTLP
// receiver edge sources.
const (
	serviceGraphID = "detected-service-graph"
	receiverID     = "otlp-receiver"
)

// detectionSources are the detection sources of opts.Detected and the OTLP
// receiver's source. Only the dashboard (opts.LiveEdges set) reads live
// edges: it reads the detection once, gives each entry a live edge source
// takes to that source and the rest to the detection source, so each backend
// is one row. Elsewhere, and when the detection cannot be read, the detection
// source reads as it always did. The receiver's source is built whenever the
// receiver runs, with the Collector entry when one was read. assembled are
// the sources gathered so far.
func (s *Service) detectionSources(ctx context.Context, opts FleetOptions, assembled []fleet.Source) []fleet.Source {
	// ponytail: a configured source holding an id (a --trace-source
	// detected-jaeger=...) wins; the backend stays a detected row.
	free := func(id string) bool {
		return !slices.ContainsFunc(assembled, func(a fleet.Source) bool { return a.ID() == id })
	}
	receiver := opts.LiveEdges != nil && opts.LiveEdges.Receiver != nil && free(receiverID)
	var bare []fleet.Source
	if receiver {
		bare = []fleet.Source{fleetsrc.NewReceiverSource(receiverID, nil, nil, opts.LiveEdges)}
	}
	src := s.detectionReader(opts.Detected)
	switch {
	case src == nil:
		return bare
	case opts.LiveEdges == nil:
		return []fleet.Source{src}
	}
	res, st := src.Read(ctx)
	if st.Status == fleet.SourceUnavailable {
		return append([]fleet.Source{src}, bare...)
	}
	var kinds []string
	for kind, id := range liveEdgeIDs {
		if free(id) && (id != receiverID || receiver) {
			kinds = append(kinds, kind)
		}
	}
	rest, taken := detect.SplitEdgeKinds(res, kinds)
	out := append([]fleet.Source{fleetsrc.NewDetectionResultSource(src.ID(), rest, s.env().now)}, s.liveEdgeSources(res, taken, free(serviceGraphID), opts)...)
	if !slices.ContainsFunc(taken, func(e detect.Entry) bool { return e.Kind == "otel-collector" }) {
		out = append(out, bare...)
	}
	return out
}

// liveEdgeSources are the live edge sources of the taken entries of res. A
// taken Tempo is read through the service graph; with none and graph free, a
// Collector entry with a Prometheus Endpoint is read through it too, without
// being taken. A taken Collector is the OTLP receiver's row.
func (s *Service) liveEdgeSources(res detect.Result, taken []detect.Entry, graph bool, opts FleetOptions) []fleet.Source {
	var out []fleet.Source
	for _, e := range taken {
		switch e.Kind {
		case "jaeger":
			out = append(out, fleetsrc.NewJaegerSource(liveEdgeIDs[e.Kind], res, e, opts.LiveEdges, s.env().now))
		case "tempo":
			out = append(out, fleetsrc.NewServiceGraphSource(serviceGraphID, res, e, opts.LiveEdges, s.env().now))
			graph = false
		case "otel-collector":
			out = append(out, fleetsrc.NewReceiverSource(receiverID, &res, &e, opts.LiveEdges))
		}
	}
	if i := slices.IndexFunc(res.Entries, func(e detect.Entry) bool { return e.Kind == "otel-collector" && e.Endpoint != "" }); graph && i >= 0 {
		out = append(out, fleetsrc.NewServiceGraphSource(serviceGraphID, res, res.Entries[i], opts.LiveEdges, s.env().now))
	}
	return out
}

// workloadsSuffix and meshSuffix name a cluster's workload and mesh sources
// after its Pacto source.
const (
	workloadsSuffix = "-workloads"
	meshSuffix      = "-mesh"
)

// liveK8sSources are a cluster's three live sources: its Pacto CRs, its
// workloads and the calls a service mesh records, the Pacto source named id
// and the others after it. cluster is nil when the cluster identity could not
// be read. meshMemo keeps the mesh source's last read across builds.
func liveK8sSources(id string, cluster *fleet.Cluster, client k8sclient.K8sClient, namespaces []string, promoters []workloadmanager.Fingerprint, meshMemo *fleetsrc.MeshMemo) []fleet.Source {
	return []fleet.Source{
		fleetsrc.NewK8sSource(id, cluster, client, namespaces),
		fleetsrc.NewWorkloadSource(id+workloadsSuffix, cluster, client, namespaces).WithPromoters(promoters),
		fleetsrc.NewMeshSource(id+meshSuffix, cluster, client, namespaces).WithMemo(meshMemo),
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
