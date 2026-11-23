package fleet

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"maps"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/opencontainers/go-digest"
	"golang.org/x/sync/errgroup"

	"github.com/trianalab/pacto/v4/pkg/capability"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/externalid"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/lock"
	"github.com/trianalab/pacto/v4/pkg/readiness"
	"github.com/trianalab/pacto/v4/pkg/sbom"
	"github.com/trianalab/pacto/v4/pkg/skills"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// Bounds keep the projected snapshot from growing unboundedly on pathological
// bundles (a huge OpenAPI spec, a docs tree with thousands of files).
const (
	maxToolsPerRevision = 500
	maxDocRefs          = 200
	maxSBOMLicenses     = 20
	defaultConcurrency  = 8
)

// BuildOptions tunes a [Build]. The zero value is valid and sensible.
type BuildOptions struct {
	// Now is the clock used for freshness classification. Defaults to time.Now.
	// Inject a fixed clock for deterministic tests.
	Now func() time.Time
	// FreshnessWindow marks a target's evidence stale when it is older than this.
	// Zero disables target-evidence staleness classification.
	FreshnessWindow time.Duration
	// DisallowPartial forces fail-closed behavior: a single source failure is
	// fatal. The zero value fails open with explicit limitations rather than
	// losing every other source's records.
	DisallowPartial bool
	// Concurrency bounds simultaneous source Collect calls. Defaults to
	// min(8, len(sources)).
	Concurrency int
	// DiscloseUncheckedFreshness, when FreshnessWindow is zero, adds the
	// LimitationFreshnessNotEvaluated snapshot limitation so every answer says
	// evidence age was not checked. The delivery planner leaves it false because
	// impact applies its own 24h rule.
	DiscloseUncheckedFreshness bool
	// ExternalCatalog classifies the external systems contracts name. Nil uses
	// the embedded default catalog.
	ExternalCatalog *externalcatalog.Catalog
	// ExternalCatalogLimitations are the limitations the caller met loading
	// ExternalCatalog (an organization catalog it could not read, for
	// example). Any of them makes the snapshot partial.
	ExternalCatalogLimitations []Limitation
	// DeclarationSigners is the reader's installation signer policy: a
	// declared revision's cluster facts are shown only when its verified
	// signer is a person it allows. Nil or unread shows none of them.
	DeclarationSigners *DeclarationSignerPolicy
}

// Build composes an immutable [FleetSnapshot] from the given sources. It calls
// each source concurrently (bounded), projects raw records into the three
// distinct identities, deduplicates revisions by immutable key, associates
// targets with revisions, builds the declared relationship graph and its reverse
// index, classifies freshness and completeness, and orders everything
// deterministically. Context cancellation is honored and is always fatal. A
// source that fails is recorded as unavailable (sanitized) and, unless
// DisallowPartial is set, does not poison the rest of the snapshot.
func Build(ctx context.Context, opts BuildOptions, sources ...Source) (*FleetSnapshot, error) {
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	generatedAt := now()

	results, err := collectAll(ctx, opts, sources)
	if err != nil {
		return nil, err
	}
	dropNameCollisions(results, sources)
	clusters := clusterSet(results, sources)

	snap := &FleetSnapshot{
		SchemaVersion:         SchemaVersion,
		Services:              map[ServiceKey]*ServiceRecord{},
		Revisions:             map[RevisionKey]*ContractRevision{},
		Targets:               map[TargetKey]*TargetRecord{},
		GeneratedAt:           generatedAt,
		reverseDeps:           map[ServiceKey][]ServiceKey{},
		forwardDeps:           map[ServiceKey][]ServiceKey{},
		forwardDepsByRevision: map[RevisionKey][]ServiceKey{},
		observedReverse:       map[ServiceKey][]ServiceKey{},
		observedForward:       map[ServiceKey][]ServiceKey{},
		inferredReverse:       map[ServiceKey][]ServiceKey{},
		inferredForward:       map[ServiceKey][]ServiceKey{},
		provisioningWindow:    opts.FreshnessWindow,
		Clusters:              clusters.records,
		clusterKeyed:          clusters.multi,
		declarationSigners:    opts.DeclarationSigners,
	}

	// Zero configured sources is NOT the same as "every source was available and
	// returned nothing". Make the distinction explicit.
	if len(sources) == 0 {
		snap.Limitations = append(snap.Limitations, Limitation{
			Code: LimitationNoSourcesConfigured, Message: "no sources were configured for this snapshot",
		})
	}

	seenSourceIDs := map[string]bool{}
	var observed []observedInput
	var uses []sourcedUse
	var facts []sourcedFact
	var inferred []inferredInput
	undeclaredOnly := map[string]bool{}
	// Sources are processed in declared order so composition is deterministic
	// regardless of goroutine completion order.
	for i := range results {
		r := &results[i]
		src := sources[i]
		if seenSourceIDs[src.ID()] {
			snap.Limitations = append(snap.Limitations, Limitation{
				Code: LimitationDuplicateSourceID, Source: src.ID(),
				Message: "more than one source declares id " + src.ID() + "; provenance may be ambiguous",
			})
		}
		seenSourceIDs[src.ID()] = true
		if r.err != nil {
			if opts.DisallowPartial {
				return nil, r.err
			}
			snap.Sources = append(snap.Sources, unavailableState(src, r.err))
			snap.Limitations = append(snap.Limitations, Limitation{
				Code: LimitationSourceUnavailable, Source: src.ID(),
				Message: "source " + src.ID() + " is unavailable; its records are missing from this snapshot",
			})
			continue
		}
		inferred = append(inferred, ingestCollection(snap, src, r.col, generatedAt, opts.FreshnessWindow, clusters, i)...)
		if r.col != nil {
			undeclaredOnly[src.ID()] = r.col.ObservedUndeclaredOnly
			observed = append(observed, observedEdgesOf(src.ID(), r.col, clusters)...)
			for _, u := range r.col.ExternalUses {
				uses = append(uses, sourcedUse{use: u, source: src.ID()})
			}
			facts = append(facts, sourcedFacts(src.ID(), r.col)...)
		}
	}

	clusterMergeLimitations(snap, clusters)
	markClusterConflicts(snap, clusters, sources)
	boundTargetLabels(snap)
	linkTargets(snap)
	inferred = replaceInferred(snap, inferred)
	inferred = replaceByInferredFrom(snap, inferred)
	inferred = replaceByObservedLink(snap, inferred)
	revalidateConfigRefs(snap)
	supersedeContributions(snap)
	aggregateServices(snap)
	addInferredServices(snap, inferred)
	resolveOrigins(snap)
	cat := opts.ExternalCatalog
	if cat == nil {
		cat = externalcatalog.Default()
	}
	snap.catalog = cat
	snap.ExternalCatalogs = externalCatalogRefs(cat.Refs())
	snap.Limitations = append(snap.Limitations, opts.ExternalCatalogLimitations...)
	buildRelationships(snap, cat)
	foldObservedRelationships(snap, observed)
	foldExternalUses(snap, append(uses, recordExternalUses(snap)...))
	foldServiceFacts(snap, facts)
	attachInferredProviders(snap, cat)
	linkExternalClaims(snap)
	finalizeExternals(snap)
	reconcileDeclared(snap, undeclaredOnly)
	matchProvisioning(snap)
	matchUsage(snap)
	checkDetectedClusters(snap)
	finalizeProviders(snap)
	snap.Completeness = classifyCompleteness(snap)
	snap.workloadOnlyPartial = snap.Completeness == CompletenessPartial && completenessExcept(snap, nonConsumerSourceKinds...) != CompletenessPartial
	snap.Limitations = append(snap.Limitations, degradedLimitations(snap)...)
	if opts.DiscloseUncheckedFreshness && opts.FreshnessWindow == 0 && len(snap.Targets) > 0 {
		snap.Limitations = append(snap.Limitations, Limitation{
			Code:    LimitationFreshnessNotEvaluated,
			Message: "evidence age was not checked (--freshness 0), so no target is marked stale; stale counts read 0 because they were not computed",
		})
	}
	sortSnapshot(snap)
	snap.SnapshotID = computeSnapshotID(snap)
	return snap, nil
}

// computeSnapshotID hashes the fully-built, ordered snapshot into a deterministic
// content identity. The SnapshotID field itself is excluded from the hash. Two
// snapshots built from the same inputs and clock produce the same id.
func computeSnapshotID(snap *FleetSnapshot) string {
	clone := *snap
	clone.SnapshotID = ""
	// The catalogs are build inputs whose signature status changes as checks
	// complete; what they classified is hashed through Externals.
	clone.ExternalCatalogs = nil
	// OwnerProvenance and OwnerSource are derived from the revisions and
	// targets already hashed, so it adds nothing to the identity; leaving it out keeps the
	// ids of snapshots built before it existed.
	clone.Services = make(map[ServiceKey]*ServiceRecord, len(snap.Services))
	for k, s := range snap.Services {
		cp := *s
		cp.OwnerProvenance, cp.OwnerSource = "", ""
		clone.Services[k] = &cp
	}
	data, err := json.Marshal(&clone)
	if err != nil {
		// The snapshot is composed of JSON-serializable domain types; a marshal
		// error is not reachable in practice, but fail safe with a marker rather
		// than a partial hash.
		return "sha256:unavailable"
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// dropNameCollisions removes, across every source, each revision whose
// service.name was only inferred and is claimed by another folder or
// reference, before service keys are formed, so a guessed name never joins
// another service and inherits its targets or consumers. Each source that loses
// a revision gets one SERVICE_NAME_COLLISION limitation per name, which makes
// it partial. A source's own Collection is never mutated: it may be reused.
// An inferred name its own baseline declares (a folder pacto ci planned from
// that published revision) joins that service, so it claims like a declared
// one: the baseline is present and declares the name, so no guess is made.
func dropNameCollisions(results []sourceResult, sources []Source) {
	claims, confirmed := competingClaims(results, sources)
	collided := map[string]string{} // name -> limitation message
	for _, c := range contract.NameCollisions(claims) {
		var who []string
		for _, cl := range c.Claims {
			who = append(who, cl.Dir+" ("+cl.Source+")")
		}
		collided[c.Name] = "service name " + c.Name + " is claimed by " + strings.Join(who, ", ") +
			"; the inferred claims are left out: declare service.name to keep them"
	}
	if len(collided) == 0 {
		return
	}
	for i := range results {
		if col := results[i].col; col != nil {
			results[i].col = withoutInferredClaims(col, sources[i].ID(), collided, confirmed)
		}
	}
}

// competingClaims is every source's name claims, one per bundle, and the
// inferred claimants (name, bundle) their own baseline confirms, which claim
// as declared ones.
func competingClaims(results []sourceResult, sources []Source) ([]contract.NameClaim, map[[2]string]bool) {
	var all []digestClaim
	declared := map[[2]string]bool{} // name, bundle of a declared claim
	for i, r := range results {
		for _, c := range nameClaims(sources[i].ID(), r.col) {
			all = append(all, c)
			if !c.Inferred && c.Bundle != "" {
				declared[[2]string{c.Name, c.Bundle}] = true
			}
		}
	}
	confirmed := map[[2]string]bool{} // name, bundle of an inferred claim its baseline declares
	for _, c := range all {
		if c.Inferred && c.Baseline != "" && c.Bundle != "" && declared[[2]string{c.Name, c.Baseline}] {
			confirmed[[2]string{c.Name, c.Bundle}] = true
		}
	}
	var claims []contract.NameClaim
	seen := map[[2]string]bool{} // name, bundle of an inferred claim already made
	for _, c := range all {
		key := [2]string{c.Name, c.Bundle}
		c.Inferred = c.Inferred && !confirmed[key]
		// One bundle read by two sources (overlapping roots, a catalog root
		// inside --local) is one claimant, not two competing ones.
		if c.Inferred && c.Bundle != "" {
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		claims = append(claims, c.NameClaim)
	}
	return claims, confirmed
}

// digestClaim is a name claim and the identity of the bundle making it.
type digestClaim struct {
	contract.NameClaim
	Bundle, Baseline string
}

// bundleIdentity is the folder a local revision was read from, made absolute,
// or the digest of a published one. Two folders with identical content are
// two bundles, so a local identity is never a digest.
func bundleIdentity(rev RawRevision) string {
	ref := cmp.Or(rev.ResolvedRef, rev.RequestedRef)
	if strings.Contains(ref, "://") && !strings.HasPrefix(ref, "file://") {
		return rev.Digest
	}
	dir := strings.TrimPrefix(ref, "file://")
	if dir == "" {
		return ""
	}
	abs, _ := filepath.Abs(dir) // "" only when the working directory is unreadable
	return cmp.Or(abs, dir)
}

// nameClaims is one claim per revision of col, plus one declared claim per
// target that names a service: a target-only name (target state, an inline
// Pacto) is a service an inferred name must not join and inherit targets from.
// ponytail: provisioning and usage records name a consumer revision by digest,
// so they are not claims; add them if one ever names a service without one.
func nameClaims(sourceID string, col *Collection) []digestClaim {
	var claims []digestClaim
	for _, rev := range collectionRevisions(col) {
		claim := digestClaim{contract.NameClaim{Dir: sourceID + " " + cmp.Or(rev.ResolvedRef, rev.RequestedRef), Name: rev.Bundle.Contract.Service.Name, Source: "declared"}, bundleIdentity(rev), rev.Baseline}
		if inf, ok := rev.Bundle.NameInferred(); ok {
			claim.Source, claim.Inferred = inf.Source, true
		}
		claims = append(claims, claim)
	}
	if col != nil {
		for _, t := range col.Targets {
			if t.Service != "" {
				claims = append(claims, digestClaim{NameClaim: contract.NameClaim{
					Dir: sourceID + " target " + t.Scope + "/" + t.Kind + "/" + t.Name, Name: t.Service, Source: "declared"}})
			}
		}
	}
	return claims
}

// collectionRevisions is col's revisions that carry a contract.
func collectionRevisions(col *Collection) []RawRevision {
	if col == nil {
		return nil
	}
	var out []RawRevision
	for _, rev := range col.Revisions {
		if rev.Bundle != nil && rev.Bundle.Contract != nil {
			out = append(out, rev)
		}
	}
	return out
}

// withoutInferredClaims is a copy of col without the inferred claimants of the
// collided names, plus one limitation per name it lost. A confirmed claimant
// (name, bundle) stays.
func withoutInferredClaims(col *Collection, sourceID string, collided map[string]string, confirmed map[[2]string]bool) *Collection {
	cp := *col
	lost := map[string]bool{}
	cp.Revisions = slices.DeleteFunc(slices.Clone(col.Revisions), func(rev RawRevision) bool {
		if rev.Bundle == nil || rev.Bundle.Contract == nil {
			return false
		}
		name := rev.Bundle.Contract.Service.Name
		_, inferred := rev.Bundle.NameInferred()
		inferred = inferred && !confirmed[[2]string{name, bundleIdentity(rev)}]
		_, hit := collided[name]
		lost[name] = lost[name] || hit && inferred
		return hit && inferred
	})
	cp.Limitations = slices.Clone(col.Limitations)
	for _, name := range slices.Sorted(maps.Keys(lost)) {
		if lost[name] {
			cp.Limitations = append(cp.Limitations, Limitation{Code: LimitationServiceNameCollision, Source: sourceID, Message: collided[name]})
		}
	}
	return &cp
}

// sourceResult is one source's collection outcome.
type sourceResult struct {
	col *Collection
	err error
}

// collectAll runs every source's Collect concurrently with bounded parallelism.
// Context cancellation is fatal; a source error is captured per-source and
// classified later.
func collectAll(ctx context.Context, opts BuildOptions, sources []Source) ([]sourceResult, error) {
	results := make([]sourceResult, len(sources))
	if len(sources) == 0 {
		return results, nil
	}
	limit := opts.Concurrency
	if limit <= 0 {
		limit = defaultConcurrency
	}
	if limit > len(sources) {
		limit = len(sources)
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(limit)
	for i := range sources {
		g.Go(func() error {
			if err := gctx.Err(); err != nil {
				return err
			}
			col, err := sources[i].Collect(gctx)
			results[i] = sourceResult{col: col, err: err}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// ingestCollection projects one successful source's records into the snapshot.
// clusters is the snapshot's cluster index and i the source's position in it.
// It returns the source's inferred services, bounded, for addInferredServices.
func ingestCollection(snap *FleetSnapshot, src Source, col *Collection, now time.Time, window time.Duration, clusters clusterIndex, i int) []inferredInput {
	if col == nil {
		col = &Collection{}
	}
	if src.Kind() == SourceKindMemberLinks {
		// A carrier of link facts: no records, no source state, never listed.
		applyClusterLinks(snap, col.ClusterLinks)
		return nil
	}
	revCount, targetCount := 0, 0
	// recordProblems collects the limitations that say THIS source failed to deliver
	// a record: one it dropped outright, or one it delivered invalid. That is what
	// downgrades the source below, and the dropped-revision half used to be excluded
	// — which inverted the severity, since a target KEPT after normalizing one bad
	// enum marked its source partial while a revision dropped outright left the
	// source reporting available and complete.
	//
	// A cross-source MERGE conflict is deliberately not in here. Two sources
	// disagreeing about one label of one target is symmetric by construction:
	// neither returned a partial result, and blaming whichever happened to be
	// declared second would attribute partiality by ingestion order. Those
	// limitations are still reported at snapshot level, where they belong to the
	// disagreement rather than to a source.
	recordProblems := slices.Clone(clusters.problems[i])
	refused := contributionRefusals{}
	for _, raw := range col.Revisions {
		rev, lims := revisionFrom(raw, src.ID(), now)
		if rev != nil && divertRevision(snap, rev, raw, src.ID(), refused) {
			// An inferred contract is never a service: it becomes its
			// external system's provider. A refused contribution is none
			// either. What reading it could not establish (its record, say)
			// is still said.
			snap.Limitations = append(snap.Limitations, lims...)
			revCount++
			continue
		}
		if rev != nil {
			rev.registryBacked = registryBacked(src.Kind(), rev)
			var ingest bool
			var declLims []Limitation
			rev.Record, declLims, ingest = acceptDeclaration(rev, rev.Record, snap.declarationSigners)
			if !ingest {
				// A draft is no record problem: the source read it whole.
				snap.Limitations = append(snap.Limitations, declLims...)
				continue
			}
			lims = append(lims, declLims...)
		}
		if rev == nil {
			// A nil revision may still carry a limitation stating why it was omitted
			// (e.g. no immutable digest and unhashable content); record it so the
			// omission is honest rather than silent.
			recordProblems = append(recordProblems, lims...)
			continue
		}
		if existing := snap.Revisions[rev.Key]; existing != nil {
			// Same immutable revision from another source: merge complementary
			// projections and provenance rather than dropping either.
			snap.Limitations = append(snap.Limitations, mergeRevision(existing, rev)...)
		} else {
			snap.Revisions[rev.Key] = rev
			// The identity-unresolved limitation is a property of the revision, so
			// record it only when the revision first enters the snapshot. It is not a
			// record problem: the revision was read whole and is queryable, it just
			// carries a derived content identity instead of a registry digest.
			snap.Limitations = append(snap.Limitations, lims...)
		}
		revCount++
	}
	snap.Limitations = append(snap.Limitations, refused.limitations(src.ID())...)
	for _, raw := range col.Targets {
		tgt, invalid := targetFrom(raw, src.ID(), now, window, clusters.forTarget(src, col, raw))
		recordProblems = append(recordProblems, invalid...)
		tgt.ReportedBy = reportedBy(src, tgt.Scope)
		if existing := snap.Targets[tgt.Key]; existing != nil {
			// Same target contributed by another source (e.g. platform inventory
			// + operator evaluation): merge by field ownership and freshness.
			snap.Limitations = append(snap.Limitations, mergeTarget(existing, tgt)...)
		} else {
			snap.Targets[tgt.Key] = tgt
		}
		targetCount++
	}
	recordProblems = append(recordProblems, ingestProvisioning(snap, src, col.Provisioning, now)...)
	recordProblems = append(recordProblems, ingestUsage(snap, src, col.Usage, now)...)
	recordProblems = append(recordProblems, ingestExtensions(snap, src, col, now)...)
	kept, capped := boundInferred(col, src.ID())
	recordProblems = append(recordProblems, capped...)
	// A source that reported record-level problems is partial: keep its usable
	// records AND surface the problems. Canonicalization limitations (invalid finite
	// values normalized at ingestion) are recorded on the target AND at the snapshot
	// level, and mark the source partial.
	snap.Limitations = append(snap.Limitations, col.Limitations...)
	snap.Limitations = append(snap.Limitations, col.Notes...)
	snap.Limitations = append(snap.Limitations, recordProblems...)
	st, stateLims := sourceStateFor(src, col, now, revCount, targetCount, len(recordProblems) > 0)
	st.Advisory, st.CautionOnly = col.Advisory, col.ObservedCautionOnly
	snap.Limitations = append(snap.Limitations, stateLims...)
	snap.Sources = append(snap.Sources, st)
	before := len(snap.Providers)
	ingestProviders(snap, src.ID(), col)
	refused.downgradeRows(snap.Providers[before:])
	cluster := ""
	if cl, ok := clusters.resolved[rawClusterID(col, RawTarget{})]; ok {
		cluster = cl.ID
	}
	out := make([]inferredInput, len(kept))
	for j, raw := range kept {
		out[j] = inferredInput{source: src.ID(), cluster: cluster, raw: raw}
	}
	return out
}

// applyClusterLinks puts each link on the cluster record with its id. A link
// for a cluster the snapshot does not know is dropped: nothing shows beside
// it.
func applyClusterLinks(snap *FleetSnapshot, links map[string]ClusterLink) {
	for i := range snap.Clusters {
		if l, ok := links[snap.Clusters[i].ID]; ok {
			snap.Clusters[i].Link = &l
		}
	}
}

// clusterIndex is what Build knows about the clusters of one snapshot, computed
// once over every collected source before any record is ingested.
type clusterIndex struct {
	records  []ClusterRecord
	resolved map[string]Cluster
	// problems are per-source record problems (indexed like sources): invalid
	// cluster ids, CLUSTER_UNKNOWN with several clusters, CLUSTER_CONFLICT.
	problems [][]Limitation
	// clusterless are the keys of the targets each source delivered with no
	// known cluster (always 3-part keys).
	clusterless [][]TargetKey
	// conflicts are the keys of the targets each source reported in a cluster
	// another evidence source names for the same record (CLUSTER_CONFLICT).
	conflicts [][]TargetKey
	multi     bool
}

// targetCluster is the cluster one raw target resolves to.
type targetCluster struct {
	cluster  Cluster
	relation string
	keyed    bool
	// unknown: the target has no known cluster while the snapshot knows one.
	unknown bool
	// invalid: the source named a cluster id [ValidClusterID] refuses.
	invalid bool
}

// observedEdgesOf is the observed edges of one source. In a snapshot with several
// clusters a k8s: caller or callee of a source with a known cluster gains that
// cluster; an end the source already qualified is kept.
func observedEdgesOf(id string, col *Collection, c clusterIndex) []observedInput {
	cl, known := c.resolved[rawClusterID(col, RawTarget{})]
	qualify := func(end string) string {
		if cluster, ns, kind, name, ok := ParseK8sCaller(end); ok && cluster == "" && known && c.multi {
			return K8sCaller(cl.ID, ns, kind, name)
		}
		return end
	}
	out := make([]observedInput, 0, len(col.Observed))
	for _, e := range col.Observed {
		e.From, e.To = qualify(e.From), qualify(e.To)
		out = append(out, observedInput{edge: e, source: id})
	}
	return out
}

// forTarget resolves the cluster of one raw target: its own cluster when set,
// else the collection's. An invalid id resolves to none.
func (c clusterIndex) forTarget(src Source, col *Collection, raw RawTarget) targetCluster {
	id := rawClusterID(col, raw)
	cl, ok := c.resolved[id]
	if !ok {
		return targetCluster{unknown: len(c.records) > 0, keyed: c.multi, invalid: id != "" && !ValidClusterID(id)}
	}
	relation := ClusterRelationReportedThrough
	if src.Kind() == SourceKindKubernetes {
		relation = ClusterRelationRunsIn
	}
	return targetCluster{cluster: cl, relation: relation, keyed: c.multi}
}

// rawClusterID is the cluster id a raw target claims, "" when none.
func rawClusterID(col *Collection, raw RawTarget) string {
	switch {
	case raw.Cluster != nil:
		return raw.Cluster.ID
	case col.Cluster != nil:
		return col.Cluster.ID
	}
	return ""
}

// validClusterIDPattern is the shape of a cluster id from an untrusted source.
// Its 253-byte bound is a length check in [ValidClusterID], not a {0,252}
// repeat: compiling that recurses deeper than a WebKit worker's call stack.
var validClusterIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidClusterID reports whether id may name a cluster: what [Build] keeps from
// an untrusted source. A kube-system UID fits.
func ValidClusterID(id string) bool { return len(id) <= 253 && validClusterIDPattern.MatchString(id) }

// clusterNameRank orders the declared name sources of reported names.
var clusterNameRank = map[string]int{
	ClusterNameFromApproval: 5, ClusterNameFromNodeLabel: 4, ClusterNameFromContext: 3, ClusterNameFromFile: 2, ClusterNameFromUID: 1,
}

// ValidClusterName reports whether [Build] keeps a reported cluster name: at
// most 63 bytes of printable runes with a known name source.
func ValidClusterName(name, nameSource string) bool {
	return validClusterName(name, nameSource, 63)
}

// maxLiveClusterNameLen bounds a name the process read itself: a kubeconfig
// context name such as an EKS ARN or a GKE gke_<project>_<zone>_<cluster>
// often runs past the 63 bytes a reported name may take.
const maxLiveClusterNameLen = 253

func validClusterName(name, nameSource string, maxLen int) bool {
	return name != "" && len(name) <= maxLen && utf8.ValidString(name) && clusterNameRank[nameSource] > 0 &&
		!strings.ContainsFunc(name, func(r rune) bool { return !unicode.IsPrint(r) })
}

// clusterNameClaim is one source's claim on the name of a cluster.
type clusterNameClaim struct {
	name, nameSource, reportedBy string
	live                         bool
}

// outranks says whether claim a beats b: a name the process read itself (a
// kubernetes-kind source) beats any reported one, then the declared name
// source decides. Equal claims keep the earlier source.
func (a clusterNameClaim) outranks(b clusterNameClaim) bool {
	if a.live != b.live {
		return a.live
	}
	return clusterNameRank[a.nameSource] > clusterNameRank[b.nameSource]
}

// clusterSet validates every cluster the sources report, ranks their names and
// decides once whether the snapshot holds more than one cluster.
func clusterSet(results []sourceResult, sources []Source) clusterIndex {
	idx := clusterIndex{resolved: map[string]Cluster{}, problems: make([][]Limitation, len(sources)), clusterless: make([][]TargetKey, len(sources)), conflicts: make([][]TargetKey, len(sources))}
	claims := map[string]*clusterNameClaim{}
	reporters := map[string][]string{}
	for i, r := range results {
		if r.err != nil || r.col == nil {
			continue
		}
		invalid := 0
		for _, c := range sourceClusters(r.col) {
			if !ValidClusterID(c.ID) {
				invalid++
				continue
			}
			reporters[c.ID] = appendUnique(reporters[c.ID], sources[i].ID())
			claimClusterName(claims, sources[i], c)
		}
		if invalid > 0 {
			idx.problems[i] = append(idx.problems[i], clusterInvalid(sources[i].ID(), strconv.Itoa(invalid)+" of them"))
		}
	}
	for id, srcs := range reporters {
		cl := Cluster{ID: id, Name: id[:min(8, len(id))], NameSource: ClusterNameFromUID}
		if c := claims[id]; c != nil {
			cl.Name, cl.NameSource, cl.ReportedBy = c.name, c.nameSource, c.reportedBy
		}
		idx.resolved[id] = cl
		idx.records = append(idx.records, ClusterRecord{Cluster: cl, Sources: srcs})
	}
	sort.Slice(idx.records, func(a, b int) bool { return idx.records[a].ID < idx.records[b].ID })
	disambiguateClusterNames(&idx)
	idx.multi = len(idx.records) > 1
	idx.collectClusterless(results, sources)
	idx.detectClusterConflicts(results, sources)
	return idx
}

// disambiguateClusterNames appends the uid prefix to a name more than one
// cluster resolved to, so two clusters never read alike in labels and search.
func disambiguateClusterNames(idx *clusterIndex) {
	uses := map[string]int{}
	for _, r := range idx.records {
		uses[r.Name]++
	}
	for i := range idx.records {
		if r := &idx.records[i]; uses[r.Name] > 1 {
			r.Name += " (" + r.ID[:min(8, len(r.ID))] + ")"
			idx.resolved[r.ID] = r.Cluster
		}
	}
}

// sourceClusters lists every cluster a collection names: its own and each
// target's.
func sourceClusters(col *Collection) []Cluster {
	var out []Cluster
	if col.Cluster != nil {
		out = append(out, *col.Cluster)
	}
	for _, t := range col.Targets {
		if t.Cluster != nil {
			out = append(out, *t.Cluster)
		}
	}
	return out
}

// claimClusterName records src's claim on the name of c when it is valid and
// outranks the claim already held.
func claimClusterName(claims map[string]*clusterNameClaim, src Source, c Cluster) {
	// Both Kubernetes sources read the cluster with this process's own
	// credentials, so neither name is merely reported.
	live, maxLen := src.Kind() == SourceKindKubernetes || src.Kind() == SourceKindKubernetesWorkloads, 63
	if live {
		maxLen = maxLiveClusterNameLen
	}
	if !validClusterName(c.Name, c.NameSource, maxLen) {
		return
	}
	claim := clusterNameClaim{name: c.Name, nameSource: c.NameSource, live: live}
	if !claim.live {
		claim.reportedBy = src.ID()
	}
	if held := claims[c.ID]; held == nil || claim.outranks(*held) {
		claims[c.ID] = &claim
	}
}

// collectClusterless records the targets each source delivered with no known
// cluster and, in a snapshot with several clusters, makes each such source
// partial with one CLUSTER_UNKNOWN. With no known cluster nothing is recorded.
func (c *clusterIndex) collectClusterless(results []sourceResult, sources []Source) {
	if len(c.records) == 0 {
		return
	}
	for i, r := range results {
		if r.err != nil || r.col == nil {
			continue
		}
		for _, t := range r.col.Targets {
			if _, ok := c.resolved[rawClusterID(r.col, t)]; !ok {
				c.clusterless[i] = append(c.clusterless[i], NewTargetKey(t.Scope, t.Kind, t.Name))
			}
		}
		if c.multi && len(c.clusterless[i]) > 0 {
			c.problems[i] = append(c.problems[i], clusterUnknownMulti(sources[i].ID(), len(c.clusterless[i])))
		}
	}
}

// evidenceRecord identifies one evidence record across Evidence Servers.
type evidenceRecord struct {
	producer, kind, subject string
	evidenceAt, acceptedAt  time.Time
}

// detectClusterConflicts reports one evidence record that two evidence sources
// list as accepted in two different clusters: the later source gets a
// CLUSTER_CONFLICT record problem and both targets stay.
func (c *clusterIndex) detectClusterConflicts(results []sourceResult, sources []Source) {
	seen := map[evidenceRecord]string{}
	for i, r := range results {
		if r.err != nil || r.col == nil || sources[i].Kind() != SourceKindEvidenceHTTP {
			continue
		}
		for _, t := range r.col.Targets {
			id := rawClusterID(r.col, t)
			if _, ok := c.resolved[id]; !ok || t.EvidenceAt == nil || t.ReconciledAt == nil {
				continue
			}
			rec := evidenceRecord{t.Scope, t.Kind, t.Name, t.EvidenceAt.UTC(), t.ReconciledAt.UTC()}
			if prev, ok := seen[rec]; !ok {
				seen[rec] = id
			} else if prev != id {
				// Two ids mean several clusters, so every target is keyed by cluster.
				c.conflicts[i] = append(c.conflicts[i], NewClusterTargetKey(id, t.Scope, t.Kind, t.Name))
			}
		}
		if n := len(c.conflicts[i]); n > 0 {
			c.problems[i] = append(c.problems[i], clusterConflict(sources[i].ID(), n))
		}
	}
}

// clusterConflict is the CLUSTER_CONFLICT of source id for n evidence records.
func clusterConflict(id string, n int) Limitation {
	return Limitation{Code: LimitationClusterConflict, Source: id,
		Message: "source " + id + " reports " + strconv.Itoa(n) + " evidence records that another evidence source reports as accepted in a different cluster; one subject has exactly one accepting Evidence Server"}
}

// clusterInvalidPhrase marks the SOURCE_RECORD_INVALID about invalid cluster
// ids, which Restrict rebuilds from the targets a view may see.
const clusterInvalidPhrase = " reported invalid cluster ids: "

// clusterInvalid is the SOURCE_RECORD_INVALID of source id for what (a count
// of ids, or of targets in a restricted view).
func clusterInvalid(id, what string) Limitation {
	return Limitation{Code: LimitationSourceRecordInvalid, Source: id,
		Message: "source " + id + clusterInvalidPhrase + what + "; those clusters were dropped and their targets carry no cluster"}
}

// markClusterConflicts records on each conflicting target the source that
// reported it, so Restrict can recount CLUSTER_CONFLICT from visible targets.
func markClusterConflicts(snap *FleetSnapshot, c clusterIndex, sources []Source) {
	for i, keys := range c.conflicts {
		for _, k := range keys {
			t := snap.Targets[k]
			t.clusterConflictFrom = appendUnique(t.clusterConflictFrom, sources[i].ID())
		}
	}
}

// clusterMergeLimitations states, in a snapshot with exactly one known cluster,
// what Build assumed about clusterless targets. A clusterless target that merged
// into a clustered one was treated as the same cluster, so its source becomes
// partial with a CLUSTER_UNKNOWN naming the keys. With no such merge, one
// informational CLUSTER_UNKNOWN is added and completeness is unchanged.
func clusterMergeLimitations(snap *FleetSnapshot, c clusterIndex) {
	if len(c.records) != 1 {
		return
	}
	total, merged := 0, false
	for i, keys := range c.clusterless {
		total += len(keys)
		var hit []string
		for _, k := range keys {
			if snap.Targets[k].Cluster != "" {
				hit = appendUnique(hit, string(k))
			}
		}
		if len(hit) > 0 {
			merged = true
			slices.Sort(hit)
			markSourcePartial(snap, i, clusterUnknownMerged(snap.Sources[i].ID, c.records[0].Name, hit))
		}
	}
	if !merged && total > 0 {
		snap.Limitations = append(snap.Limitations, clusterUnknownInfo(total))
	}
}

// clusterUnknownMulti is the CLUSTER_UNKNOWN of a source that delivered n
// clusterless targets to a snapshot with several clusters.
func clusterUnknownMulti(id string, n int) Limitation {
	return Limitation{Code: LimitationClusterUnknown, Source: id,
		Message: strconv.Itoa(n) + " targets from source " + id + " carry no cluster id, so they cannot be told apart from another cluster's; give the source a cluster id (for the live source: get on namespaces/kube-system)"}
}

// clusterUnknownMerged is the CLUSTER_UNKNOWN of a source whose clusterless
// targets (keys) merged into the targets of the one known cluster.
func clusterUnknownMerged(id, clusterName string, keys []string) Limitation {
	return Limitation{Code: LimitationClusterUnknown, Source: id,
		Message: "targets from source " + id + " carry no cluster id and were merged into the targets of cluster " + clusterName + ": " + boundedKeyList(keys)}
}

// clusterUnknownInfo is the informational CLUSTER_UNKNOWN of a snapshot with
// one known cluster whose total clusterless targets merged into none.
func clusterUnknownInfo(total int) Limitation {
	return Limitation{Code: LimitationClusterUnknown,
		Message: strconv.Itoa(total) + " targets carry no cluster id; with one known cluster their keys stay unqualified"}
}

// boundedKeyList joins at most 20 keys and counts the rest, so a limitation
// message never grows with the fleet.
func boundedKeyList(keys []string) string {
	const limit = 20
	if len(keys) <= limit {
		return strings.Join(keys, ", ")
	}
	return strings.Join(keys[:limit], ", ") + " and " + strconv.Itoa(len(keys)-limit) + " more"
}

// markSourcePartial adds lim and downgrades an available source to partial.
// It runs before completeness is classified.
func markSourcePartial(snap *FleetSnapshot, i int, lim Limitation) {
	snap.Limitations = append(snap.Limitations, lim)
	if snap.Sources[i].Status == SourceAvailable {
		snap.Sources[i].Status = SourcePartial
	}
}

// mergeRevision folds a later contribution of the same immutable revision into
// the existing record: it unions provenance and fills empty complementary
// projections (validation, tools, skills, readiness). It never lets source order
// pick a winner. The two projections backed by bytes the snapshot does not own —
// documents and the lock — are not fill-the-empty-side: they get the stricter
// treatment in mergeRevisionDocs and mergeRevisionLock.
func mergeRevision(existing, add *ContractRevision) []Limitation {
	existingValidated := existing.validated
	existing.Sources = appendUnique(existing.Sources, add.Source)
	if existing.Readiness == nil {
		existing.Readiness = add.Readiness
	}
	if len(existing.Validation) == 0 && len(add.Validation) > 0 {
		existing.Validation = add.Validation
		existing.Valid = add.Valid
		existing.validated = existing.validated || add.validated
	}
	// A contribution that validated fills in the stage verdicts one that could
	// not read the document lacks; two that both validated read the same bytes.
	if len(existing.Stages) == 0 && len(add.Stages) > 0 {
		existing.Stages, existing.Environments = add.Stages, add.Environments
	}
	// A contribution that read the document also read the values files its
	// environments name, so its comparison replaces one that could not.
	if existing.differences == nil && existing.differencesErr == "" || !existingValidated && add.validated {
		existing.differences, existing.differencesTotal, existing.differencesErr = add.differences, add.differencesTotal, add.differencesErr
	}
	if len(existing.Tools) == 0 {
		existing.Tools = add.Tools
	}
	// A contribution that read the bundle names the documents it read; one
	// without files read none, so it never stands for "unreadable".
	if len(existing.SpecsRead) == 0 {
		existing.SpecsRead = add.SpecsRead
	}
	if len(existing.Skills) == 0 {
		existing.Skills = add.Skills
	}
	mergeProvenance(existing, add)
	lims := mergeRevisionDocs(existing, add)
	lims = append(lims, mergeRevisionLock(existing, add)...)
	if existing.ResolvedRef == "" {
		existing.ResolvedRef = add.ResolvedRef
	}
	return lims
}

// mergeRevisionLock folds a second contributor's pacto.lock into the revision.
//
// A lock is not a complementary projection: linkReferences and resolveDepRevision
// read it to decide which bundle a declared dependency or reference actually
// resolves to, so filling the empty side is only safe while the two contributors
// AGREE. Two sources may legitimately pin one immutable digest and still ship
// different lock bytes: the digest is the revision's identity, so a locally
// regenerated pacto.lock beside a registry copy of the same revision reaches this
// merge as two locks on one key. Taking whichever arrived first would let source
// completion order change the resolved graph edges under one unchanged
// SnapshotID, which computeSnapshotID cannot detect because both orders hash the
// same snapshot fields.
//
// So a disagreement drops the lock entirely and says why: no pins is an honest
// "not established", whereas a coin-flip between two locks is a confident wrong
// answer. The conflict is sticky for the same reason the document conflict is —
// a third, agreeing contributor must not be able to talk us back into serving one
// side of a real disagreement.
func mergeRevisionLock(existing, add *ContractRevision) []Limitation {
	switch {
	case add.Lock == nil || existing.lockConflict != "":
		return nil
	case existing.Lock == nil:
		existing.Lock = add.Lock
		return nil
	case sameResolutions(existing.Lock, add.Lock):
		return nil
	}
	existing.Lock = nil
	existing.lockConflict = "two sources contributed this revision with disagreeing pacto.lock " +
		"resolutions, so the pins it recorded were discarded rather than picked by arrival order"
	return []Limitation{{
		Code: LimitationRevisionLockConflict, Source: add.Source,
		Message: "sources disagree on the pacto.lock of revision " + string(existing.Key) +
			"; its recorded resolutions are not used to resolve dependencies or references",
	}}
}

// sameResolutions reports whether two locks record the same resolutions.
//
// It compares what the lock RESOLVED — its entries, plus the root identity and
// schema version lockReference reads to decide which entries answer for this
// revision — and deliberately not Pacto, which records only the CLI version that
// produced the lock. Two contributors that regenerated byte-identical pins on
// consecutive Pacto releases have not disagreed about anything, and treating that
// as a conflict would discard every pin the revision has and degrade all of its
// declared dependencies and references to unresolved. Same discipline as
// sameDocSet, which compares path plus content digest and nothing else.
func sameResolutions(a, b *lock.Lock) bool {
	x, y := *a, *b
	x.Pacto, y.Pacto = lock.PactoInfo{}, lock.PactoInfo{}
	return reflect.DeepEqual(x, y)
}

// fixedPathPolicy reports whether a contract that declares no policy serves one
// at the fixed path policy/schema.json, the schema a policy reference to its
// bundle falls back to.
func fixedPathPolicy(c *contract.Contract, b *contract.Bundle) bool {
	if len(c.Policies) > 0 || b == nil || b.FS == nil {
		return false
	}
	_, err := fs.Stat(b.FS, validation.PolicySchemaPath)
	return err == nil
}

// mergeRevisionDocs folds a second contributor's documents into the revision.
//
// Documents are the one projection backed by bytes the snapshot does not own, so
// "fill the empty side" is not enough here: adopting a doc list also adopts a
// private bundle handle, and if two contributors disagree about the content
// behind a path then whichever one happened to arrive first would decide what
// Query.RevisionDocument returns. Source ordering must never be able to change
// the bytes served under one SnapshotID.
//
// Evidence is the bundle filesystem, not the doc list: a contributor with a
// filesystem and no docs/ directory positively says "this revision has no
// documents", while a runtime-only contributor (an operator status with no
// bundle at all) says nothing and cannot conflict with anything. Two contributors
// that both have evidence must agree on every path AND every content digest;
// otherwise the revision's documents become unreadable and say why. The conflict
// is sticky, so a third, agreeing contributor cannot talk us back into serving
// one side of a real disagreement.
func mergeRevisionDocs(existing, add *ContractRevision) []Limitation {
	hasFS := func(r *ContractRevision) bool { return r.bundle != nil && r.bundle.FS != nil }
	switch {
	case !hasFS(add):
		return nil
	case !hasFS(existing):
		existing.Docs, existing.bundle, existing.fixedPolicy = add.Docs, add.bundle, add.fixedPolicy
		return nil
	case existing.docConflict != "" || sameDocSet(existing.Docs, add.Docs):
		return nil
	}
	existing.docConflict = "two sources contributed this revision with different document content, so no document body can be attributed to it"
	return []Limitation{{
		Code: LimitationRevisionDocConflict, Source: add.Source,
		Message: "sources disagree on the documents of revision " + string(existing.Key) +
			"; its document bodies are not served",
	}}
}

// sameDocSet reports whether two contributors describe exactly the same
// documents: the same paths, in the same walk order, with the same content
// fingerprints. An unfingerprintable document (empty digest) equals only another
// unfingerprintable one at the same path — it is never quietly compatible with a
// readable body.
func sameDocSet(a, b []DocRef) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Path != b[i].Path || a[i].digest != b[i].digest {
			return false
		}
	}
	return true
}

// mergeTarget folds a later contribution of the same target into the existing
// record. The fresher observation (by evidence time) owns evaluation fields;
// labels union with a conflict report on disagreement; a contradictory resolved
// reference is reported. Provenance is always retained.
func mergeTarget(existing, add *TargetRecord) []Limitation {
	existing.Sources = appendUnique(existing.Sources, add.Source)
	var lims []Limitation
	// Identity-bearing fields must AGREE across sources for one target key. Fill an
	// empty side from the other; a genuine disagreement quarantines the target and
	// is reported — deterministically, regardless of contribution order.
	lims = append(lims, mergeTargetIdentity(existing, add)...)
	if ref, conflict := mergeRef(existing.ResolvedRef, add.ResolvedRef); conflict {
		existing.Quarantined = true
		lims = append(lims, Limitation{Code: LimitationTargetRefConflict, Source: add.Source,
			Message: "sources disagree on the resolved reference of target " + string(existing.Key)})
	} else {
		existing.ResolvedRef = ref
	}
	lims = append(lims, mergeTargetLabels(existing, add)...)
	mergeTargetCluster(existing, add)
	if existing.Owner.IsEmpty() {
		existing.Owner = add.Owner
	}
	// Two sources that read different domains from one target disagree; both are
	// kept, so deriveBusinessDomain reports the conflict instead of picking one.
	for _, d := range add.reportedDomains {
		existing.reportedDomains = appendUnique(existing.reportedDomains, d)
	}
	existing.domainKnown = existing.domainKnown || add.domainKnown
	for _, v := range add.reportedOfferings {
		existing.reportedOfferings = appendUnique(existing.reportedOfferings, v)
	}
	for _, v := range add.reportedSummaries {
		existing.reportedSummaries = appendUnique(existing.reportedSummaries, v)
	}
	if targetFresher(add, existing) {
		applyEvaluation(existing, add)
	}
	existing.Limitations = append(existing.Limitations, add.Limitations...)
	return lims
}

// mergeTargetCluster fills an empty cluster of existing from add, so the merged
// target's cluster never depends on source order. Two different ids never share
// a key (two clusters key every target by cluster), so there is no conflict to
// report. runs-in wins over reported-through.
func mergeTargetCluster(existing, add *TargetRecord) {
	for _, src := range add.clusterUnknownFrom {
		existing.clusterUnknownFrom = appendUnique(existing.clusterUnknownFrom, src)
	}
	for _, src := range add.clusterInvalidFrom {
		existing.clusterInvalidFrom = appendUnique(existing.clusterInvalidFrom, src)
	}
	if existing.Cluster == "" {
		existing.Cluster, existing.ClusterName, existing.ClusterNameSource = add.Cluster, add.ClusterName, add.ClusterNameSource
		existing.ClusterNameReportedBy, existing.ClusterRelation = add.ClusterNameReportedBy, add.ClusterRelation
	}
	if add.ClusterRelation == ClusterRelationRunsIn {
		existing.ClusterRelation = ClusterRelationRunsIn
	}
}

// mergeTargetIdentity reconciles the identity-bearing fields of two contributions
// of one target key: an empty side is filled from the other; a real disagreement
// quarantines the target (so it is never authoritative) and yields a structured
// conflict limitation. The outcome is independent of contribution order.
func mergeTargetIdentity(existing, add *TargetRecord) []Limitation {
	var lims []Limitation
	reconcile := func(field string, get func(*TargetRecord) string, set func(*TargetRecord, string)) {
		a, b := get(existing), get(add)
		switch {
		case a == "":
			set(existing, b)
		case b == "" || a == b:
			// agree (or nothing new to add)
		default:
			existing.Quarantined = true
			lims = append(lims, Limitation{Code: LimitationTargetFieldConflict, Source: add.Source,
				Message: "sources disagree on the " + field + " of target " + string(existing.Key)})
		}
	}
	// service + domain together determine ServiceKey, so checking them covers the
	// key without double-reporting the derived value.
	reconcile("service", func(t *TargetRecord) string { return t.Service }, func(t *TargetRecord, v string) { t.Service = v; t.ServiceKey = NewServiceKeyDomain(t.Domain, v) })
	reconcile("domain", func(t *TargetRecord) string { return t.Domain }, func(t *TargetRecord, v string) { t.Domain = v; t.ServiceKey = NewServiceKeyDomain(v, t.Service) })
	reconcile("contractRevision", func(t *TargetRecord) string { return string(t.ContractRevision) }, func(t *TargetRecord, v string) { t.ContractRevision = RevisionKey(v) })
	reconcile("digest", func(t *TargetRecord) string { return t.Digest }, func(t *TargetRecord, v string) { t.Digest = v })
	return lims
}

// mergeRef returns the agreed reference, or reports a conflict when two non-empty
// references disagree.
func mergeRef(a, b string) (string, bool) {
	switch {
	case a == "":
		return b, false
	case b == "":
		return a, false
	case a == b:
		return a, false
	default:
		return a, true
	}
}

func mergeTargetLabels(existing, add *TargetRecord) []Limitation {
	if len(add.Labels) == 0 {
		return nil
	}
	if existing.Labels == nil {
		existing.Labels = map[string]string{}
	}
	var lims []Limitation
	for k, v := range add.Labels {
		if k == TargetEnvironmentLabel && existing.environmentConflict {
			continue // stays dropped: no contributor can settle it
		}
		cur, ok := existing.Labels[k]
		if ok && cur != v {
			lims = append(lims, Limitation{Code: LimitationTargetFieldConflict, Source: add.Source,
				Message: "sources disagree on label " + k + " of target " + string(existing.Key)})
			if k == TargetEnvironmentLabel {
				// Dropped, not kept from the first source: a label search must not
				// match the environment the target page calls unknown.
				existing.Environment, existing.environmentConflict = "", true
				delete(existing.Labels, k)
			}
			continue
		}
		if !ok && k == TargetEnvironmentLabel {
			existing.Environment = v
		}
		existing.Labels[k] = v
	}
	return lims
}

// targetFresher reports whether a is a fresher observation than b (by evidence
// then reconciliation time); a target with evidence is fresher than one without.
func targetFresher(a, b *TargetRecord) bool {
	switch {
	case a.EvidenceAt != nil && b.EvidenceAt != nil:
		return a.EvidenceAt.After(*b.EvidenceAt)
	case a.EvidenceAt != nil:
		return true
	case b.EvidenceAt != nil:
		return false
	case a.ReconciledAt != nil && b.ReconciledAt != nil:
		return a.ReconciledAt.After(*b.ReconciledAt)
	default:
		return a.ReconciledAt != nil
	}
}

// applyEvaluation copies the evaluation-owned fields from the fresher target,
// including Source so the source of record follows the authoritative evaluation
// (otherwise the winning evaluation is mis-attributed to whichever source merged
// first). Digest is deliberately NOT copied here: it is an identity-bearing
// field, and carrying it across the freshness merge would let a fresher
// evaluation that lacks a digest wipe one another source established, making the
// serialized snapshot and its SnapshotID depend on source order. Digest is
// reconciled order-independently by mergeTargetIdentity (fill-empty or
// quarantine).
func applyEvaluation(dst, src *TargetRecord) {
	dst.Compliance = src.Compliance
	dst.Findings = src.Findings
	dst.Coverage = src.Coverage
	dst.Readiness = src.Readiness
	dst.ObservedRuntime = src.ObservedRuntime
	dst.EvidenceAt = src.EvidenceAt
	dst.ReconciledAt = src.ReconciledAt
	// Stale is a verdict ABOUT EvidenceAt, so it travels with it. It used to be ANDed
	// across every contributor in mergeTarget, but Stale is only ever set from a
	// non-nil EvidenceAt (targetFrom), so a contributor carrying no evidence time had
	// Stale==false by default and silently cleared another source's staleness while
	// that source's old EvidenceAt stayed on the record. Owning it here keeps the pair
	// consistent and order-independent: whichever contribution wins targetFresher owns
	// both halves.
	dst.Stale = src.Stale
	dst.Source = src.Source
}

// canonicalSourceStatus normalizes a source-declared status to the finite
// source-health vocabulary. A malformed status is normalized to a valid DEGRADED
// state (partial) rather than emitted out-of-schema or silently upgraded to
// available.
func canonicalSourceStatus(s SourceStatus) (canonical SourceStatus, invalid bool) {
	if validSourceHealth(string(s)) {
		return s, false
	}
	return SourcePartial, true
}

// sourceStateFor derives (or honors a source-supplied) state for a collection. It
// canonicalizes a source-declared status to the finite source-health vocabulary,
// and downgrades an otherwise-available source to partial when the collection had
// record-level problems, so a source that emitted invalid records is never
// presented as healthy. It returns any SOURCE_RECORD_INVALID limitations the
// normalization produced.
func sourceStateFor(src Source, col *Collection, now time.Time, revCount, targetCount int, recordProblems bool) (SourceState, []Limitation) {
	if col.State != nil {
		st := *col.State
		if st.ID == "" {
			st.ID = src.ID()
		}
		if st.Kind == "" {
			st.Kind = src.Kind()
		}
		st.RevisionCount = revCount
		st.TargetCount = targetCount
		status, invalid := canonicalSourceStatus(st.Status)
		// A source that emitted invalid records is not healthy; downgrade an
		// available/valid declared status to partial (never upgrade a worse one).
		// col.Limitations counts for the same reason recordProblems does, and it is
		// checked here as well as in the derived branch below: a source that declares
		// its own state is exactly the source most likely to have shipped
		// collection-level limitations alongside it, and taking the declared
		// "available" at face value while its own limitations say otherwise let a
		// self-describing source out-rank its own evidence.
		if (recordProblems || len(col.Limitations) > 0) && status == SourceAvailable {
			status = SourcePartial
		}
		st.Status = status
		// Fill the freshness timestamps the source left unset, from the pinnable
		// BuildOptions.Now clock. The read that produced this state happened now, so a
		// declared state with no timestamps must not reach the snapshot looking like a
		// source nothing has looked at -- that is what used to force a source to choose
		// between declaring "partial" and keeping its own freshness.
		//
		// Only what is UNSET is filled. A source that declares stale and dates its last
		// successful sync an hour ago is stating the one fact "stale" carries;
		// overwriting it with now would erase the staleness and contradict the status
		// in the same struct.
		//
		// ObservedAt is filled for ANY status: the read happened now whatever it found.
		// LastSuccessfulSync is filled only for the two statuses that may claim a sync.
		// Stale and unavailable each ASSERT that the last GOOD read was not now, so
		// stamping the build clock there would make the state contradict its own status
		// -- and would disagree with unavailableState, which stamps neither field for
		// the same status on the error path. The canonicalized-and-downgraded status is
		// what decides, never the status as supplied.
		t := now
		if st.LastSuccessfulSync == nil && (status == SourceAvailable || status == SourcePartial) {
			st.LastSuccessfulSync = &t
		}
		if st.ObservedAt == nil {
			st.ObservedAt = &t
		}
		var lims []Limitation
		if invalid {
			lims = append(lims, Limitation{
				Code: LimitationSourceRecordInvalid, Source: st.ID,
				Message: "source " + st.ID + " reported a non-canonical health status; it was normalized to " + string(SourcePartial),
			})
		}
		return st, lims
	}
	t := now
	status := SourceAvailable
	if len(col.Limitations) > 0 || recordProblems {
		status = SourcePartial
	}
	return SourceState{
		ID: src.ID(), Kind: src.Kind(), Status: status,
		LastSuccessfulSync: &t, ObservedAt: &t,
		RevisionCount: revCount, TargetCount: targetCount,
	}, nil
}

// unavailableState builds a sanitized unavailable state for a failed source.
func unavailableState(src Source, err error) SourceState {
	st := SourceState{
		ID: src.ID(), Kind: src.Kind(), Status: SourceUnavailable,
		Error: sanitizeError(err),
	}
	if cs, ok := src.(ClusterSource); ok && cs.Cluster() != nil {
		c := *cs.Cluster()
		st.Cluster = &c
	}
	return st
}

// sanitizeError maps an arbitrary error into a category code plus a fixed,
// generic message. It never echoes the raw error text, so credentials, tokens,
// URLs, or host names in the underlying error can never leak to a consumer.
func sanitizeError(err error) *SourceError {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "context canceled") || strings.Contains(msg, "context deadline") || strings.Contains(msg, "timeout"):
		return &SourceError{Code: "CANCELLED", Message: "the source request was cancelled or timed out"}
	case strings.Contains(msg, "unauthor") || strings.Contains(msg, "forbidden") ||
		strings.Contains(msg, "credential") || strings.Contains(msg, "denied") ||
		strings.Contains(msg, "401") || strings.Contains(msg, "403") || strings.Contains(msg, "auth"):
		return &SourceError{Code: "AUTH_FAILED", Message: "authentication with the source failed"}
	case strings.Contains(msg, "not found") || strings.Contains(msg, "404"):
		return &SourceError{Code: "NOT_FOUND", Message: "the source or artifact was not found"}
	default:
		return &SourceError{Code: "UNAVAILABLE", Message: "the source is unavailable"}
	}
}

// revisionFrom projects a raw revision into an immutable ContractRevision,
// deriving validation, readiness, tools, skills, doc refs, and lock. Returns nil
// (and no limitations) for a record with no parseable contract. When the source
// pinned no immutable digest it derives a content digest for the key and returns
// a REVISION_IDENTITY_UNRESOLVED limitation, so the revision is collision-safe yet
// honestly marked as lacking an immutable registry reference.
func revisionFrom(raw RawRevision, source string, now time.Time) (*ContractRevision, []Limitation) {
	if raw.Bundle == nil || raw.Bundle.Contract == nil {
		return nil, nil
	}
	b := raw.Bundle
	c := b.Contract
	serviceKey := NewServiceKeyDomain(raw.Domain, c.Service.Name)
	var lims []Limitation
	// When the source pinned an immutable digest that IS the content identity:
	// two sources agreeing on the same digest can never spuriously conflict on
	// content, even if one's local FS holds regenerated/cache artifacts. Only when
	// there is no immutable digest do we derive a full-bundle content identity.
	contentID := raw.Digest
	if raw.Digest == "" {
		cd, err := contentDigest(b)
		if err != nil {
			// The full logical content cannot be hashed and there is no immutable
			// digest. Omit the revision rather than assign a contract-only identity
			// that would be presented as collision-safe when it is not.
			return nil, []Limitation{{
				Code: LimitationRevisionUnresolved, Source: source,
				Message: "revision " + c.Service.Name + " has no immutable digest and its bundle content could not be hashed (" + err.Error() + "); it is omitted rather than given a non-collision-safe identity",
			}}
		}
		contentID = cd
		lims = append(lims, Limitation{
			Code: LimitationRevisionUnresolved, Source: source,
			Message: "revision " + c.Service.Name + " has no immutable digest; a content digest was derived, so its identity is not an immutable registry reference",
		})
	}
	rev := &ContractRevision{
		Key:          NewRevisionKey(serviceKey, contentID),
		Service:      c.Service.Name,
		Domain:       raw.Domain,
		ServiceKey:   serviceKey,
		PactoVersion: c.PactoVersion,
		Version:      c.Service.Version,
		RequestedRef: raw.RequestedRef,
		ResolvedRef:  raw.ResolvedRef,
		Digest:       raw.Digest,
		// The declared contract is deep-copied so a source mutating its own
		// bundle after Build can never mutate the snapshot.
		Contract:   cloneContract(c),
		Inferred:   slices.Clone(b.Inferred),
		Source:     source,
		Sources:    []string{source},
		FetchedAt:  copyTime(raw.FetchedAt),
		Provenance: cloneProvenance(raw.Provenance),
		bundle:     b,
		// The first contribution of a revision keeps its contract, so it keeps
		// whether that contract's guarantees could be read too.
		guaranteesUnseen: raw.GuaranteesUnseen,
		// The contract's free-form metadata map is author-controlled and can be
		// arbitrarily wide, so it is bounded HERE, once, at the source boundary.
		Metadata: runtimePreview(c.Metadata),
		// Each configuration scope carries an author-controlled Values map that is
		// bounded the same way and for the same reason. It is precomputed here rather
		// than in revisionDetail so one contract with many wide scopes cannot multiply
		// the cost of every revision-detail request; the query joins reference
		// resolutions onto a copy of this projection.
		configurations: configurationsPreview(c.Configurations),
		fixedPolicy:    fixedPathPolicy(c, b),
	}
	// Owner references the cloned contract so it never aliases source memory.
	rev.Owner = rev.Contract.Service.Owner
	var recLims []Limitation
	rev.Record, recLims = inferredRecord(b.FS, c, source)
	rev.recordUnreadable = len(recLims) > 0
	lims = append(lims, recLims...)
	// Validation needs the contract DOCUMENT, and RawYAML holds it only for a local
	// read -- every bundle fetched from a registry carries the same document in its
	// FS and nowhere else, so keying off RawYAML alone left every fetched revision
	// silently unvalidated. Bundle.Raw asks both places.
	//
	// A Raw error means the document genuinely cannot be read: the bundle carries a
	// parsed contract with no FS to read it back from (a runtime-only or
	// contract-only record), or its FS has no readable pacto.yaml. There is nothing
	// to validate, so the revision stays validated=false -- the honest "never
	// validated" state consumers already distinguish from "validated and invalid".
	// It is not a limitation: a source that ships a parsed contract rather than a
	// document is a normal source, not a degraded one.
	if yml, err := b.Raw(); err == nil {
		doc := raw.Document // decoded by the loader already, when it says so
		if doc == nil {
			doc = contract.DecodeDocument(yml)
		}
		rev.localStages = validation.ValidateDocumentStages(c, doc, b.FS)
		setStages(rev, rev.localStages)
		rev.validated = true
		setDifferences(rev, c, b.FS)
	} else {
		rev.Environments = EnvironmentResults(c.Environments, nil)
		// Two environments with no values read are compared or stated
		// unreadable, so an empty list never reads as "the same values".
		setDifferences(rev, c, b.FS)
	}
	rev.Readiness = readiness.Evaluate(c.Readiness, now)
	rev.Tools, rev.SpecsRead = toolsFrom(c, b.FS)
	if b.FS != nil {
		// skills.List calls fs.ReadDir, which panics on a nil FS interface; a
		// runtime-only or rawless bundle can legitimately carry no FS.
		if names, _ := skills.List(b.FS); len(names) > 0 {
			rev.Skills = names
		}
	}
	rev.Docs = docsFrom(b.FS)
	if s, err := sbomSummary(b.FS); err != nil {
		// The bundle ships an SBOM we could not read. Say so, because a missing
		// summary must never be presented as "this revision declares no packages".
		lims = append(lims, Limitation{
			Code: LimitationSBOMUnreadable, Source: source,
			Message: "revision " + c.Service.Name + " ships an SBOM that could not be parsed (" + err.Error() + "); its software inventory is unknown, not empty",
		})
	} else {
		rev.SBOM = s
	}
	rev.Lock = cloneLock(lockFrom(raw))
	return rev, lims
}

// inferredRecord reads the bundle's pacto.inferred.yaml for a contract at
// pactoVersion 2.5 or later (an older one never carries a record, so one there
// is ignored). A record that cannot be parsed, or whose cited files do not
// match the bundle, is dropped with a limitation: its inferred dependencies are
// unknown, never guessed and never read as absent. Parsing yields fresh values,
// so the snapshot never aliases the source's bytes.
func inferredRecord(fsys fs.FS, c *contract.Contract, source string) (*contract.InferredRecord, []Limitation) {
	if validation.InferredRecordNeedsRaise(c.PactoVersion) {
		return nil, nil
	}
	rec, err := contract.ReadInferredRecord(fsys)
	if err == nil && rec != nil {
		err = contract.VerifyInferredRecord(fsys, rec)
	}
	if err != nil {
		return nil, []Limitation{{Code: LimitationInferredRecordUnreadable, Source: source,
			Message: "revision " + c.Service.Name + " ships an inferred record Pacto could not read (" + err.Error() + "); its inferred dependencies are unknown, not absent"}}
	}
	return rec, nil
}

// sbomSummary projects a bundle's SBOM into the bounded [SBOMSummary]. It returns
// (nil, nil) when the bundle simply ships no SBOM, and an error when it ships one
// that cannot be parsed — the caller turns that into a limitation rather than into
// silence.
func sbomSummary(fsys fs.FS) (*SBOMSummary, error) {
	doc, err := sbom.ParseFromFS(fsys)
	if err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, nil
	}
	counts := map[string]int{}
	for _, p := range doc.Packages {
		lic := strings.TrimSpace(p.License)
		if lic == "" {
			// A package that declares no license is a real answer ("we do not know
			// what this is licensed under"), so it gets its own bucket instead of
			// vanishing from a distribution that claims to cover every package.
			lic = "unspecified"
		}
		counts[lic]++
	}
	s := &SBOMSummary{Format: doc.Format, Packages: len(doc.Packages)}
	for lic, n := range counts {
		s.Licenses = append(s.Licenses, LicenseCount{License: lic, Count: n})
	}
	sort.Slice(s.Licenses, func(i, j int) bool {
		if s.Licenses[i].Count != s.Licenses[j].Count {
			return s.Licenses[i].Count > s.Licenses[j].Count
		}
		return s.Licenses[i].License < s.Licenses[j].License
	})
	if len(s.Licenses) > maxSBOMLicenses {
		for _, b := range s.Licenses[maxSBOMLicenses:] {
			s.OtherLicensed += b.Count
		}
		s.Licenses = s.Licenses[:maxSBOMLicenses]
	}
	return s, nil
}

// contentDigest derives a deterministic, collision-safe content identity for the
// COMPLETE logical bundle — the parsed contract PLUS every referenced file
// (OpenAPI documents, JSON schemas, skills, docs) via a deterministic FS hash. Two
// bundles with an identical pacto.yaml but different referenced content therefore
// get different revision identities. encoding/json sorts map keys, so equal
// contracts hash identically; lock.HashFS is order-independent over the FS.
func contentDigest(b *contract.Bundle) (string, error) {
	h := sha256.New()
	data, _ := json.Marshal(b.Contract)
	h.Write(data)
	if b.FS != nil {
		// Fold in every bundle file so referenced content affects identity. A hash
		// error (unreadable FS) is returned, NOT silently degraded to a
		// contract-only digest: the caller must not present an incomplete identity
		// as collision-safe.
		fsHash, err := lock.HashFS(b.FS)
		if err != nil {
			return "", err
		}
		h.Write([]byte{0})
		h.Write([]byte(fsHash))
	} else if b.RawYAML != nil {
		h.Write([]byte{0})
		h.Write(b.RawYAML)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// toolsFrom derives bounded tool summaries from a contract's OpenAPI interfaces,
// mirroring the MCP/dashboard naming (interface prefix when >1 openapi iface).
// It also returns the names of the interfaces whose referenced document was
// actually read, so a consumer can tell "this interface declares no operations"
// apart from "this interface's document was never available" — an empty tool
// list is otherwise indistinguishable between the two.
func toolsFrom(c *contract.Contract, fsys fs.FS) (tools []ToolSummary, specsRead []string) {
	if fsys == nil {
		return nil, nil
	}
	var out []ToolSummary
	for _, it := range capability.ToolsForContract(c, fsys, true) {
		if it.Err != nil {
			continue
		}
		// Every read interface is recorded even past the tool cap: only the
		// tools are bounded, so a later document never reads as unread.
		specsRead = append(specsRead, it.Interface.Name)
		for _, tool := range it.Tools {
			if len(out) >= maxToolsPerRevision {
				break
			}
			summary := tool.Summary
			if summary == "" {
				summary = tool.Description
			}
			out = append(out, ToolSummary{
				Name: tool.Name, Interface: it.Interface.Name,
				Method: tool.Method, Path: tool.Path,
				Summary: summary, Mutating: tool.Mutating,
			})
		}
	}
	return out, specsRead
}

// docsFrom collects bounded doc references (path + humanized title + content
// fingerprint) without RETAINING any body — bodies are fetched lazily via the
// bundle when needed.
//
// Each body is streamed once, through a hash, and thrown away: the snapshot grows
// by a fingerprint per document, never by the prose in the fleet. That
// fingerprint is what makes a lazy read honest. A bundle filesystem can be a live
// os.DirFS over a working directory, so the bytes behind a path may change,
// disappear or be replaced after Build; without a recorded digest the tuple
// (SnapshotID, RevisionKey, path) would silently name different documents over
// time. See [Query.RevisionDocument], which refuses any body that does not match.
func docsFrom(fsys fs.FS) []DocRef {
	if fsys == nil {
		return nil
	}
	var out []DocRef
	contract.WalkDocs(fsys, func(p string) bool {
		if len(out) >= maxDocRefs {
			return false
		}
		out = append(out, DocRef{Path: p, Title: humanizeTitle(p), digest: docDigest(fsys, p)})
		return true
	})
	return out
}

// docDigest fingerprints one document body under the SAME bound the lazy read
// applies, and returns "" when there is nothing trustworthy to record: the file
// could not be opened or read, or it is larger than [MaxDocumentBytes] and so can
// never be served whole anyway. An empty digest is not a silent pass — a document
// that carries one is reported as unavailable rather than served unverified.
func docDigest(fsys fs.FS, path string) string {
	f, err := fsys.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close() //nolint:errcheck // read-only
	h := sha256.New()
	// One byte past the bound, exactly like the read, so "at the bound" and "over
	// it" are distinguished the same way in both places.
	n, err := io.Copy(h, io.LimitReader(f, MaxDocumentBytes+1))
	if err != nil || n > MaxDocumentBytes {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// humanizeTitle turns a doc path into a display title from its filename.
func humanizeTitle(p string) string {
	name := p
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[:i]
	}
	name = strings.ReplaceAll(name, "-", " ")
	return strings.ReplaceAll(name, "_", " ")
}

// lockFrom returns the raw lock if supplied, else parses pacto.lock from the FS.
func lockFrom(raw RawRevision) *lock.Lock {
	if raw.Lock != nil {
		return raw.Lock
	}
	if raw.Bundle == nil {
		return nil
	}
	l, _ := lock.ReadFS(raw.Bundle.FS) //nolint:errcheck // an unreadable lockfile is no lock
	return l
}

// validSeverity reports whether s is a canonical finding severity. A [Source] is
// an extension seam, so a raw finding may carry any string; only these four values
// may reach the finite ProductFinding.severity OpenAPI enum.
func validSeverity(s finding.Severity) bool {
	switch s {
	case finding.SeverityError, finding.SeverityWarning, finding.SeverityInfo, finding.SeverityUnknown:
		return true
	default:
		return false
	}
}

// canonicalCompliance normalizes a source-supplied compliance value to the
// canonical vocabulary. An empty value defaults to Unknown (not an error); a
// non-empty out-of-vocabulary value is normalized conservatively to Unknown and
// reported invalid so the runtime never emits a value the compliance OpenAPI enum
// forbids and a bad value is never silently reinterpreted as healthy.
func canonicalCompliance(s string) (canonical string, invalid bool) {
	switch {
	case s == "":
		return StatusUnknown, false
	case ValidStatus(s):
		return s, false
	default:
		return StatusUnknown, true
	}
}

// canonicalFindings copies raw findings and normalizes any non-canonical severity
// to Unknown, so an extension source cannot smuggle an out-of-schema severity into
// the finite ProductFinding.severity enum. The copy keeps each finding usable; only
// the affected severity is degraded. invalid counts how many were normalized.
func canonicalFindings(fs []finding.Finding) (out []finding.Finding, invalid int) {
	if len(fs) == 0 {
		return nil, 0
	}
	out = make([]finding.Finding, len(fs))
	for i, f := range fs {
		if !validSeverity(f.Severity) {
			f.Severity = finding.SeverityUnknown
			invalid++
		}
		out[i] = f
	}
	return out, invalid
}

// targetFrom projects a raw target into a TargetRecord, classifying evidence
// freshness and preserving source-supplied limitations plus computed ones. It
// canonicalizes the finite compliance and finding-severity values a Source (an
// extension seam) may set to any string, keeping the usable record and returning a
// SOURCE_RECORD_INVALID limitation for each normalized field.
// The returned limitations are also recorded on the target so it self-describes.
func targetFrom(raw RawTarget, source string, now time.Time, window time.Duration, tc targetCluster) (*TargetRecord, []Limitation) {
	compliance, complianceInvalid := canonicalCompliance(raw.Compliance)
	findings, invalidSeverities := canonicalFindings(raw.Findings)
	keyCluster := ""
	if tc.keyed {
		keyCluster = cmp.Or(tc.cluster.ID, UnknownClusterKeyPart)
	}
	// Every mutable field is deep-copied so a source mutating its own records
	// after Build cannot mutate the snapshot.
	t := &TargetRecord{
		Key:          NewClusterTargetKey(keyCluster, raw.Scope, raw.Kind, raw.Name),
		Scope:        raw.Scope,
		Kind:         raw.Kind,
		Name:         raw.Name,
		Labels:       cloneStringMap(raw.Labels),
		Service:      raw.Service,
		Domain:       raw.Domain,
		ServiceKey:   NewServiceKeyDomain(raw.Domain, raw.Service),
		RequestedRef: raw.RequestedRef,
		ResolvedRef:  raw.ResolvedRef,
		Digest:       raw.Digest,
		Compliance:   compliance,
		Owner:        cloneOwner(raw.Owner),
		Environment:  raw.Labels[TargetEnvironmentLabel],
		Findings:     findings,
		Coverage:     cloneCoverage(raw.Coverage),
		Readiness:    cloneReadiness(raw.Readiness),
		// Bound the untrusted, arbitrarily wide/deep source runtime map ONCE here (the
		// single unbounded-source pass); the raw map is never retained on the snapshot.
		ObservedRuntime: runtimePreview(raw.ObservedRuntime),
		EvidenceAt:      copyTime(raw.EvidenceAt),
		ReconciledAt:    copyTime(raw.ReconciledAt),
		Source:          source,
		Sources:         []string{source},
		Limitations:     append([]Limitation(nil), raw.Limitations...),
	}
	if tc.cluster.ID != "" {
		t.Cluster, t.ClusterName, t.ClusterNameSource = tc.cluster.ID, tc.cluster.Name, tc.cluster.NameSource
		t.ClusterNameReportedBy, t.ClusterRelation = tc.cluster.ReportedBy, tc.relation
	}
	if tc.unknown {
		t.clusterUnknownFrom = []string{source}
	}
	if tc.invalid {
		t.clusterInvalidFrom = []string{source}
	}
	if raw.Workload != nil {
		t.workload = &WorkloadRef{Kind: raw.Workload.Kind, Name: raw.Workload.Name}
	}
	t.createdAt = copyTime(raw.CreatedAt)
	if l := raw.Link; l != nil {
		t.Link = &ObservedLink{Signal: l.Signal, Value: l.Value, Since: copyTime(l.Since), OtherVersions: slices.Clone(l.OtherVersions)}
	}
	t.reportedDomains, t.domainKnown = reportedDomainsFrom(raw)
	t.reportedOfferings, t.reportedSummaries = reportedValue(raw.Offering, validBusinessDomain), reportedValue(raw.Summary, validSummary)
	var recordInvalid []Limitation
	if complianceInvalid {
		recordInvalid = append(recordInvalid, Limitation{
			Code: LimitationSourceRecordInvalid, Source: source,
			Message: "target " + string(t.Key) + " reported a non-canonical compliance value; the record was kept and its compliance normalized to " + StatusUnknown,
		})
	}
	if invalidSeverities > 0 {
		recordInvalid = append(recordInvalid, Limitation{
			Code: LimitationSourceRecordInvalid, Source: source,
			Message: "target " + string(t.Key) + " reported a non-canonical finding severity; the record was kept and each normalized to " + string(finding.SeverityUnknown),
		})
	}
	t.Limitations = append(t.Limitations, recordInvalid...)
	if window > 0 && raw.EvidenceAt != nil && now.Sub(*raw.EvidenceAt) > window {
		t.Stale = true
		t.Limitations = append(t.Limitations, Limitation{
			Code: LimitationSourceStale, Source: source,
			Message: "the most recent evidence is older than the configured freshness window",
		})
	}
	if raw.EvidenceAt == nil && compliance == StatusUnknown {
		t.Limitations = append(t.Limitations, Limitation{
			Code: LimitationEvidenceMissing, Source: source,
			Message: "no evidence has been observed for this target",
		})
	}
	return t, recordInvalid
}

// Revision-link classes (see TargetRecord.RevisionMatch). These record
// REVISION-MATCH CERTAINTY -- how confidently a target is matched to a fleet
// revision -- NOT whether that revision's content is resolver-retrievable (that is
// the separate content-retrievability dimension, ClassifyContentIdentity /
// RevisionIdentity.Retrievable). An exact match can point at content Pacto cannot
// fetch (a trusted digest with no canonical ref).
const (
	revisionMatchExact     = "exact"     // content-digest match — authoritative revision
	revisionMatchInferred  = "inferred"  // unique mutable tag/version correlation
	revisionMatchAmbiguous = "ambiguous" // several candidates — no link is made
	// revisionMatchInconsistent: the target's identity is internally inconsistent
	// (a recorded digest that contradicts its digest-pinned ResolvedRef) or malformed.
	// No link is made and a limitation is surfaced; it is never an exact link.
	revisionMatchInconsistent = "inconsistent"
)

// boundTargetLabels precomputes each target's bounded label projection. It is a
// pass of its own rather than part of targetFrom because mergeTargetLabels keeps
// unioning Labels while collections are still being ingested: bounding at
// construction would select the bounded key set from a half-merged map, so which
// labels a reader sees would depend on source order. Running once after ingestion
// also keeps the O(labels * bound) selection off the request path, which is the
// point of bounding at the source boundary at all.
func boundTargetLabels(snap *FleetSnapshot) {
	for _, t := range snap.Targets {
		t.labels = labelsPreview(t.Labels)
	}
}

// linkTargets associates each target with a revision and records how the link
// was made. An ambiguous mutable match links to nothing and surfaces a
// REVISION_LINK_AMBIGUOUS limitation, so a guess is never presented as fact.
func linkTargets(snap *FleetSnapshot) {
	// Grouped once for the whole pass. Every scan the matcher runs filters on
	// ServiceKey, so re-deriving the order per target made the build quadratic in
	// (targets x revisions) for no new information: the order depends only on
	// snap.Revisions, which this loop does not mutate.
	byService := revisionKeysByService(snap)
	for _, t := range snap.Targets {
		key, kind := matchRevisionIn(snap, byService[t.ServiceKey], t)
		t.ContractRevision = key
		switch kind {
		case revisionMatchExact, revisionMatchInferred:
			t.RevisionMatch = kind
		case revisionMatchAmbiguous:
			lim := Limitation{
				Code: LimitationRevisionAmbiguous, Source: t.Source,
				Message: "several revisions of " + string(t.ServiceKey) + " match target " + string(t.Key) + " by its mutable reference; no authoritative revision link was made",
			}
			// Record it on the target too, so the target is self-describing: a
			// consumer classifies the link "ambiguous" (not merely "unresolved")
			// from the target alone, without parsing snapshot-level messages.
			t.Limitations = append(t.Limitations, lim)
			snap.Limitations = append(snap.Limitations, lim)
		case revisionMatchInconsistent:
			// The target's identity is internally inconsistent (a recorded digest that
			// contradicts its digest-pinned ResolvedRef) or malformed. No link is made
			// and the record self-describes the problem, so a consumer never sees a
			// TargetDetail whose identity class and linkState claim different authority.
			lim := Limitation{
				Code: LimitationSourceRecordInvalid, Source: t.Source,
				Message: "target " + string(t.Key) + " has an internally inconsistent or malformed identity (a recorded digest that contradicts its resolved reference, or a malformed reference); no authoritative revision link was made",
			}
			t.Limitations = append(t.Limitations, lim)
			snap.Limitations = append(snap.Limitations, lim)
		}
	}
}

// matchRevisionIn computes a target's REVISION-MATCH CERTAINTY against a pre-grouped
// candidate set (the sorted revision keys of the target's own service, from
// [revisionKeysByService]): which contract revision the target corresponds to and how
// confidently. The grouping is a parameter because the link pass groups the whole
// fleet once and reuses it for every target.
//
// This is a DIFFERENT question from
// whether that content is resolver-retrievable ([ClassifyContentIdentity] /
// RevisionIdentity.Retrievable). It reuses the content-identity classifier only as an
// input -- to derive the canonical content digest and to reject a self-contradictory
// identity -- so the two dimensions never disagree DISHONESTLY, but they may still
// differ HONESTLY: an EXACT match to a revision Pacto cannot fetch (a trusted digest
// with no canonical ref) is a real, valid outcome.
//
// An EXACT match is a content-digest match by the effective content digest (from a
// digest-pinned ResolvedRef, or a recorded digest that does not contradict the ref).
// An internally inconsistent (recorded-digest-vs-pinned-ref mismatch) or malformed
// identity is never exact and never mutably correlated -- it yields no link and a
// limitation. A match by mutable resolved-ref or version suffix is INFERRED, and only
// when UNIQUE; two or more candidates are AMBIGUOUS and yield no link. Revisions are
// visited in sorted key order for a deterministic result.
func matchRevisionIn(snap *FleetSnapshot, keys []RevisionKey, t *TargetRecord) (RevisionKey, string) {
	id := ClassifyContentIdentity(t.ResolvedRef, t.Digest)
	// A contradictory (digest-mismatch) or malformed OCI identity is never exact, and
	// correlating a mutable link off a contradictory/unparseable ref would be
	// dishonest.
	if id.Class == IdentityDigestMismatch || id.Class == IdentityMalformed {
		return "", revisionMatchInconsistent
	}
	// Tier 1: EXACT content identity, matched by the effective content digest and the
	// SAME classifier invariant everywhere. The effective digest is the classifier's
	// canonical digest when the ref is digest-pinned (already cross-checked against any
	// recorded digest); otherwise a digest embedded in a non-oci ref, cross-checked
	// against the recorded digest; otherwise the recorded digest. A ref that embeds a
	// digest CONTRADICTING the recorded one is internally inconsistent -- this closes
	// the scheme-less bypass of the oci:// digest-mismatch guard (a "reg/svc@<other>"
	// ref, classed local, whose embedded digest the classifier never parses).
	effective, inconsistent := effectiveContentDigest(id, t)
	if inconsistent {
		return "", revisionMatchInconsistent
	}
	if effective != "" {
		if key, ok := exactRevisionByDigest(snap, keys, t.ServiceKey, effective); ok {
			return key, revisionMatchExact
		}
		if id.Retrievable() {
			// A digest-pinned ref names exact content the fleet does not carry; there
			// is no honest mutable fallback.
			return "", ""
		}
	}
	return mutableRevisionMatch(snap, keys, t)
}

// effectiveContentDigest computes the content digest a target's identity names, and
// whether that identity is internally inconsistent. For an exact (digest-pinned) ref
// it is the classifier's canonical digest. Otherwise it reconciles a digest embedded
// in the ref text (scheme-agnostic) with the recorded digest: a genuine disagreement
// is inconsistent (never an exact link); agreement (or either being absent) yields
// the known digest. This makes a scheme-less ref carrying a digest subject to the
// same mismatch guard the oci:// path enforces.
func effectiveContentDigest(id ContentIdentity, t *TargetRecord) (digest string, inconsistent bool) {
	if id.Retrievable() {
		return id.Digest.String(), false
	}
	// A non-oci ref with MORE than one '@' is a malformed identity, exactly as
	// classifyOCIRef rejects extra '@' separators for oci:// refs. Reject it rather
	// than take the last '@' segment or silently fall through to the recorded digest,
	// which would let "reg/svc@<contradicting>@<recorded>" or "reg/svc@<other>@latest"
	// slip past the digest-mismatch guard the oci:// path enforces.
	if strings.Count(t.ResolvedRef, "@") > 1 {
		return "", true
	}
	embedded := embeddedDigest(t.ResolvedRef)
	switch {
	case embedded != "" && t.Digest != "" && embedded != t.Digest:
		return "", true
	case embedded != "":
		return embedded, false
	default:
		return t.Digest, false
	}
}

// embeddedDigest returns a syntactically valid content digest embedded after the
// single '@' in a reference (regardless of scheme), or "" if there is none or it is
// not a valid digest. Callers reject a ref with more than one '@' before calling this,
// so the last-'@' split is unambiguous. Only a valid digest can identify content or
// contradict a recorded one.
func embeddedDigest(ref string) string {
	i := strings.LastIndex(ref, "@")
	if i < 0 {
		return ""
	}
	d := ref[i+1:]
	if _, err := digest.Parse(d); err != nil {
		return ""
	}
	return d
}

// exactRevisionByDigest finds a revision of svc whose content digest equals the
// target's effective content digest. It returns false when no revision carries it.
func exactRevisionByDigest(snap *FleetSnapshot, keys []RevisionKey, svc ServiceKey, digest string) (RevisionKey, bool) {
	for _, k := range keys {
		if rev := snap.Revisions[k]; rev.ServiceKey == svc && rev.Digest == digest {
			return rev.Key, true
		}
	}
	return "", false
}

// mutableRevisionMatch correlates a target to a revision by mutable reference: exact
// resolved-ref equality, then a version-suffix pin. A unique candidate is inferred;
// several are ambiguous (no link).
func mutableRevisionMatch(snap *FleetSnapshot, keys []RevisionKey, t *TargetRecord) (RevisionKey, string) {
	if t.ResolvedRef == "" {
		return "", ""
	}
	// Tier 2: exact resolved-ref equality (still a MUTABLE reference) — inferred.
	var byRef []RevisionKey
	for _, k := range keys {
		if rev := snap.Revisions[k]; rev.ServiceKey == t.ServiceKey && rev.ResolvedRef == t.ResolvedRef {
			byRef = append(byRef, rev.Key)
		}
	}
	if key, kind := classifyMutableMatch(byRef); kind != "" {
		return key, kind
	}
	// Tier 3: the resolved ref pins the revision's version (":2.0.0"/"@2.0.0").
	var byVersion []RevisionKey
	for _, k := range keys {
		if rev := snap.Revisions[k]; rev.ServiceKey == t.ServiceKey && rev.Version != "" &&
			(strings.HasSuffix(t.ResolvedRef, ":"+rev.Version) || strings.HasSuffix(t.ResolvedRef, "@"+rev.Version)) {
			byVersion = append(byVersion, rev.Key)
		}
	}
	key, kind := classifyMutableMatch(byVersion)
	return key, kind
}

// classifyMutableMatch turns a set of mutable-correlation candidates into a link
// class: none, a unique inferred link, or ambiguous.
func classifyMutableMatch(candidates []RevisionKey) (RevisionKey, string) {
	switch len(candidates) {
	case 0:
		return "", ""
	case 1:
		return candidates[0], revisionMatchInferred
	default:
		return "", revisionMatchAmbiguous
	}
}

// revisionKeysByService groups every revision key under its own service, sorted
// within each group so a match is deterministic. The matcher never looks at another
// service's revisions, so this is the whole candidate set for any target of that
// service -- and grouping is what keeps the cost of a fleet-wide link pass
// proportional to the revisions each target could actually match, rather than to the
// whole revision set once per target.
func revisionKeysByService(snap *FleetSnapshot) map[ServiceKey][]RevisionKey {
	byService := make(map[ServiceKey][]RevisionKey)
	for k, rev := range snap.Revisions {
		byService[rev.ServiceKey] = append(byService[rev.ServiceKey], k)
	}
	for _, keys := range byService {
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	}
	return byService
}

// aggregateServices groups revisions and targets into logical service records,
// then derives each service's deterministic owner summary and aggregate status.
func aggregateServices(snap *FleetSnapshot) {
	for key, rev := range snap.Revisions {
		s := ensureService(snap, rev.Domain, rev.Service)
		s.Revisions = append(s.Revisions, key)
		for _, src := range rev.Sources {
			s.Sources = appendUnique(s.Sources, src)
		}
	}
	for key, t := range snap.Targets {
		s := ensureService(snap, t.Domain, t.Service)
		s.Targets = append(s.Targets, key)
		for _, src := range t.Sources {
			s.Sources = appendUnique(s.Sources, src)
		}
		// Target/environment labels are NOT merged into the logical-service label
		// map: distinct targets have distinct (possibly conflicting) labels, and
		// collapsing them by map iteration order would fabricate a service label
		// and break target-correlated search. Labels live on the target.
	}
	matchExtensions(snap)
	deriveExtensionChecks(snap, true)
	placed := targetPlacementKnown(snap)
	for _, s := range snap.Services {
		s.Declaration = DeclarationDeclared
		if s.Contribution = serviceContribution(snap, s); s.Contribution != nil {
			s.Declaration = DeclarationInferred
		}
		s.Status = serviceStatus(snap, s)
		s.NameInferred = nameInferred(snap, s)
		snap.Limitations = append(snap.Limitations, deriveOwner(snap, s)...)
		snap.Limitations = append(snap.Limitations, deriveBusinessDomain(snap, s)...)
		deriveTemplate(snap, s, placed)
		snap.Limitations = append(snap.Limitations, deriveOffering(snap, s)...)
	}
}

// deriveBusinessDomain sets a service's business domain (see businessDomainOf),
// reports a DOMAIN_CONFLICT limitation when its targets' sources disagree on it,
// and copies it onto the service's targets so their references carry it.
func deriveBusinessDomain(snap *FleetSnapshot, s *ServiceRecord) []Limitation {
	var conflict []string
	s.BusinessDomain, s.BusinessDomainUnknown, conflict = businessDomainOf(snap, s)
	for _, tk := range s.Targets {
		snap.Targets[tk].BusinessDomain, snap.Targets[tk].BusinessDomainUnknown = s.BusinessDomain, s.BusinessDomainUnknown
	}
	return domainConflict(s.Name, conflict)
}

// domainConflict is the DOMAIN_CONFLICT limitation for a service whose sources
// report the conflicting domains, nil when there is no conflict.
func domainConflict(name string, conflict []string) []Limitation {
	if conflict == nil {
		return nil
	}
	return []Limitation{{
		Code: LimitationDomainConflict, Source: "fleet",
		Message: "no contract revision of " + name + " declares a business domain and the sources observing its operational targets report different ones (" +
			strings.Join(conflict, ", ") + "), so its business domain is unknown",
	}}
}

// businessDomainOf is a service's business domain: the service.domain of its
// newest revision ([LessRevisionChrono]) that is not invalid. An older revision
// that declares another domain is a move, not a conflict. When no such revision
// declares a domain, it is the one the observing sources read from the contracts
// the service's targets run, as deriveOwner does for owners; sources that report
// different ones make it unknown, with the domains returned as the conflict.
// Otherwise it is none when a revision or a source says the contract declares
// none, and unknown when nothing says either way. A value that is not a domain
// slug is never a domain.
func businessDomainOf(snap *FleetSnapshot, s *ServiceRecord) (domain string, unknown bool, conflict []string) {
	domain, unknown, conflict, _ = declaredServiceValue(snap, s, func(c contract.Service) string { return c.Domain }, validBusinessDomain,
		func(t *TargetRecord) []string { return t.reportedDomains }, func(t *TargetRecord) bool { return t.domainKnown })
	return domain, unknown, conflict
}

// declaredServiceValue is the rule businessDomainOf documents, for any service
// field: declared reads it from a contract, valid says a non-empty value can be
// one, and reported and known read what the sources observing a target say. A
// nil known means no source says a contract declares none. fromContract says a
// revision declares the value.
func declaredServiceValue(snap *FleetSnapshot, s *ServiceRecord, declared func(contract.Service) string, valid func(string) bool,
	reported func(*TargetRecord) []string, known func(*TargetRecord) bool) (value string, unknown bool, conflict []string, fromContract bool) {
	var newest *ContractRevision
	isDeclared := false
	for _, rk := range s.Revisions {
		r := snap.Revisions[rk]
		if !serviceValueEvidence(r, declared, valid) {
			continue
		}
		isDeclared = isDeclared || declared(r.Contract.Service) != ""
		if newest == nil || LessRevisionChrono(newest, r) {
			newest = r
		}
	}
	if isDeclared {
		return declared(newest.Contract.Service), false, nil, true
	}
	isKnown := newest != nil
	var values []string
	for _, tk := range s.Targets {
		t := snap.Targets[tk]
		isKnown = isKnown || (known != nil && known(t))
		for _, v := range reported(t) {
			values = appendUnique(values, v)
		}
	}
	slices.Sort(values)
	switch len(values) {
	case 0:
		return "", !isKnown, nil, false
	case 1:
		return values[0], false, nil, false
	default:
		return "", true, values, false
	}
}

// serviceValueEvidence reports whether a revision can say what value a service
// field has: its contract was read, it is not invalid and what it declares is
// valid or none.
func serviceValueEvidence(r *ContractRevision, declared func(contract.Service) string, valid func(string) bool) bool {
	if !readableRevision(r) {
		return false
	}
	v := declared(r.Contract.Service)
	return v == "" || valid(v)
}

// readableRevision reports whether a revision's contract was read and is not
// invalid.
func readableRevision(r *ContractRevision) bool {
	return r.Contract != nil && (!r.validated || r.Valid)
}

// deriveOffering sets a service's platform offering and summary by the rule
// businessDomainOf documents. Sources that report different offerings leave it
// with none and an OFFERING_CONFLICT limitation; different summaries leave it
// with none and no limitation.
func deriveOffering(snap *FleetSnapshot, s *ServiceRecord) []Limitation {
	var conflict []string
	s.Offering, s.OfferingProvenance, conflict = offeringOf(snap, s)
	s.Summary, s.SummaryUnknown = summaryOf(snap, s)
	return offeringConflict(s.Name, conflict)
}

// offeringConflict is the OFFERING_CONFLICT limitation for a service whose
// sources report the conflicting offerings, nil when there is no conflict.
func offeringConflict(name string, conflict []string) []Limitation {
	if conflict == nil {
		return nil
	}
	return []Limitation{{
		Code: LimitationOfferingConflict, Source: "fleet",
		Message: "no contract revision of " + name + " declares a platform offering and the sources observing its operational targets report different ones (" +
			strings.Join(conflict, ", ") + "), so it has none",
	}}
}

// offeringOf is a service's platform offering, where its membership comes from
// (ProvenanceDeclared when a revision declares it, ProvenanceObserved when only
// its targets report it, empty with no offering) and the offerings its sources
// disagree on; the offering rule is the business-domain slug rule. A golden
// path provides no offering, whatever it declares: it runs nowhere.
func offeringOf(snap *FleetSnapshot, s *ServiceRecord) (offering, provenance string, conflict []string) {
	if s.Template {
		return "", "", nil
	}
	v, _, conflict, declared := declaredServiceValue(snap, s, func(c contract.Service) string { return c.Offering }, validBusinessDomain,
		func(t *TargetRecord) []string { return t.reportedOfferings }, nil)
	switch {
	case v == "":
	case declared:
		provenance = ProvenanceDeclared
	default:
		provenance = ProvenanceObserved
	}
	return v, provenance, conflict
}

// summaryOf is a service's one-line summary and whether Pacto cannot tell it:
// empty and unknown when its sources disagree or nothing was read.
func summaryOf(snap *FleetSnapshot, s *ServiceRecord) (string, bool) {
	v, unknown, _, _ := declaredServiceValue(snap, s, func(c contract.Service) string { return c.Summary }, validSummary,
		func(t *TargetRecord) []string { return t.reportedSummaries }, nil)
	return v, unknown
}

// validSummary is service.summary's rule in the contract schema: one line of at
// most 200 characters with no control character, which a terminal would run.
func validSummary(v string) bool {
	return v != "" && utf8.RuneCountInString(v) <= 200 && !strings.ContainsFunc(v, unicode.IsControl)
}

// reportedValue is a value a source read from a target's contract, as a set,
// dropped when it is not valid.
func reportedValue(v string, valid func(string) bool) []string {
	if v == "" || !valid(v) {
		return nil
	}
	return []string{v}
}

// reportedDomainsFrom is the domain a source read from a target's contract, as a
// set, and whether the source read the domain at all. A value that is not a domain
// slug says nothing.
func reportedDomainsFrom(raw RawTarget) ([]string, bool) {
	switch {
	case raw.BusinessDomain == "":
		return nil, raw.BusinessDomainKnown
	case validBusinessDomain(raw.BusinessDomain):
		return []string{raw.BusinessDomain}, true
	default:
		return nil, false
	}
}

// businessDomainPattern is service.domain's slug rule in the contract schema.
var businessDomainPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// validBusinessDomain reports whether d can be a business domain, so a filter
// value such as NoBusinessDomain never collides with one.
func validBusinessDomain(d string) bool {
	return len(d) <= 63 && businessDomainPattern.MatchString(d)
}

// deriveOwner sets a deterministic owner SUMMARY for a service — the owner
// declared by its NEWEST revision that declares one, else attested by its
// newest attested revision (see [ownerTier]) — and reports an
// OWNER_CONFLICT limitation when its current claims disagree (see
// [serviceOwnerClaims]): an older version's owner is a handover, not a dispute. Per-revision ownership is
// always retained on the revisions themselves; the service field is only a
// documented summary, not the authority.
//
// The order is [LessRevisionChrono] reversed, NOT RevisionKey lexicography. A
// RevisionKey embeds a content digest, so key order is digest order: under it,
// editing any byte of any bundle could silently change who Pacto says to notify.
func deriveOwner(snap *FleetSnapshot, s *ServiceRecord) []Limitation {
	t, _ := ownerTier(snap, s)
	if newest := newestOwnerRevision(snap, s, t); newest != nil {
		s.Owner, _ = claimOwner(newest, t)
		s.OwnerProvenance = OwnerProvenanceDeclared
		if t == tierAttested {
			// An owner read from the repository at a commit a verified CI
			// identity signed: after the revisions, before the target fallback.
			_, s.OwnerSource, _, _ = AttestedOwner(newest)
			s.OwnerProvenance = OwnerProvenanceAttested
		}
	}
	// No revision declares or attests an owner (a fleet read only from
	// Kubernetes has no revisions at all): fall back to the owner the observing
	// sources read from the contract each target runs.
	if s.Owner.IsEmpty() {
		for _, tk := range slices.Sorted(slices.Values(s.Targets)) {
			if s.Owner.IsEmpty() {
				s.Owner = snap.Targets[tk].Owner
			}
		}
		if !s.Owner.IsEmpty() {
			s.OwnerProvenance = OwnerProvenanceDeclaredViaTarget
		}
	}
	markInferredOwner(s)
	return append(ownerConflict(snap, s), ownerBlockedLimitations(snap, s)...)
}

// registrySourceKinds are the source kinds whose revisions were published to
// a registry, so the owner rule's verdict on them applies.
var registrySourceKinds = []string{"oci", "cache", SourceKindRegistryListing}

// pullingSourceKinds read a revision from a registry when the resolver
// pinned it to a digest, and from a local folder otherwise (catalog roots,
// the org index's closure).
var pullingSourceKinds = []string{"catalog", "index"}

// registryBacked reports whether rev, read by a source of kind, was pulled
// from a registry.
func registryBacked(kind string, rev *ContractRevision) bool {
	return slices.Contains(registrySourceKinds, kind) || (slices.Contains(pullingSourceKinds, kind) && rev.ResolvedRef != "")
}

// ownerBlocked is why r's owner claim is not read, "" when it is: a registry
// revision the owner rule refused (OWNER_DECLARATION_REFUSED), or whose check
// could not decide, or a declaration no check stamped (budget spent, no
// checker, a disk-cache read with no provenance): OWNER_DECLARATION_UNVERIFIED.
// Only an explicit allowed verdict keeps a declaration's owner. A local
// folder is its author's own files and is never blocked.
func ownerBlocked(r *ContractRevision) string {
	if !r.registryBacked {
		return ""
	}
	var check *OwnerRightCheck
	if r.Provenance != nil {
		check = r.Provenance.OwnerRight
	}
	switch {
	case check == nil:
		if r.Record != nil && r.Record.Declaration != nil {
			return LimitationOwnerDeclarationUnverified
		}
	case check.Allowed:
	case check.Code == ownerPolicyUnverified || check.Code == ownerSignerUnverified:
		return LimitationOwnerDeclarationUnverified
	default:
		return LimitationOwnerDeclarationRefused
	}
	return ""
}

// The owner right codes that mean the rule could not decide (delivery's
// OwnerPolicyUnverified and OwnerSignerUnverified).
const (
	ownerPolicyUnverified = "OWNER_POLICY_UNVERIFIED"
	ownerSignerUnverified = "OWNER_SIGNER_UNVERIFIED"
)

// ownerBlockedLimitations counts, once per service and code, the revisions of
// s that claim an owner ownerBlocked leaves out.
func ownerBlockedLimitations(snap *FleetSnapshot, s *ServiceRecord) []Limitation {
	counts := map[string]int{}
	for _, rk := range s.Revisions {
		r := snap.Revisions[rk]
		if r == nil {
			continue
		}
		if _, _, _, attested := AttestedOwner(r); r.Owner.IsEmpty() && !attested {
			continue // nothing left out
		}
		if code := ownerBlocked(r); code != "" {
			counts[code]++
		}
	}
	var out []Limitation
	for _, l := range []Limitation{
		{Code: LimitationOwnerDeclarationRefused, Message: "set an owner their signer may not set"},
		{Code: LimitationOwnerDeclarationUnverified, Message: "set an owner the owner rule could not check"},
	} {
		if n := counts[l.Code]; n > 0 {
			l.Source, l.Message = "fleet", s.Name+": "+strconv.Itoa(n)+" registry revision(s) "+l.Message+", so that owner is not used"
			out = append(out, l)
		}
	}
	return out
}

// claimTier is which owner claim of a revision the readers of a service ask
// for: its pacto.yaml owner, or the owner attested for it (see
// [AttestedOwner]). tierNone reads no revision claim.
type claimTier int

const (
	tierNone claimTier = iota
	tierDeclared
	tierAttested
)

// claimOwner is the ONLY reader of a revision's owner claim: r's owner in
// tier t and whether r makes a claim there. Every owner reader of a service
// routes through it, in the tier [ownerTier] picks.
func claimOwner(r *ContractRevision, t claimTier) (contract.Owner, bool) {
	if ownerBlocked(r) != "" {
		return contract.Owner{}, false
	}
	switch t {
	case tierDeclared:
		return r.Owner, !r.Owner.IsEmpty()
	case tierAttested:
		o, _, _, ok := AttestedOwner(r)
		return o, ok
	}
	return contract.Owner{}, false
}

// ownerTier is the tier a service's owner claims are read in: declared when
// any of its revisions declares an owner, attested when none does and its
// attested revisions were all signed from one repository, none otherwise.
// repos is every repository that signed an attested revision when the tier is
// not declared: more than one is an OWNER_CONFLICT, never newest-wins, so the
// first repository whose CI signs a service is trusted until a second appears.
// ponytail: recomputed per reader, O(revisions) per call; cache on the
// snapshot when a service with thousands of revisions shows up in a profile.
func ownerTier(snap *FleetSnapshot, s *ServiceRecord) (t claimTier, repos []string) {
	for _, rk := range s.Revisions {
		r := snap.Revisions[rk]
		if r == nil {
			continue
		}
		if _, ok := claimOwner(r, tierDeclared); ok {
			return tierDeclared, nil
		}
		if _, _, repo, ok := AttestedOwner(r); ok && !slices.Contains(repos, repo) {
			repos = append(repos, repo)
		}
	}
	if len(repos) == 1 {
		return tierAttested, repos
	}
	return tierNone, repos
}

// revisionClaim is r's owner claim in the tier its service's claims are read in.
func revisionClaim(snap *FleetSnapshot, r *ContractRevision) contract.Owner {
	if o, ok := claimOwner(r, tierDeclared); ok {
		return o
	}
	if s := snap.Services[r.ServiceKey]; s != nil {
		if t, _ := ownerTier(snap, s); t == tierAttested {
			o, _ := claimOwner(r, t)
			return o
		}
	}
	return contract.Owner{}
}

// attestedRepositoriesDisagree reports whether the attested revisions of s
// were signed from more than one repository while none declares an owner.
func attestedRepositoriesDisagree(snap *FleetSnapshot, s *ServiceRecord) bool {
	t, repos := ownerTier(snap, s)
	return t == tierNone && len(repos) > 1
}

// ownerDisputed reports whether the current owner claims of s disagree, or
// its attested revisions were signed from different repositories.
func ownerDisputed(snap *FleetSnapshot, s *ServiceRecord) bool {
	if attestedRepositoriesDisagree(snap, s) {
		return true
	}
	claims := serviceOwnerClaims(snap, s)
	return claims.len() > 1
}

// ownerConflict is the OWNER_CONFLICT limitation for a service whose current
// owner claims disagree (see [serviceOwnerClaims]), nil when they agree. Its
// message is [conflictReason], so it names why.
func ownerConflict(snap *FleetSnapshot, s *ServiceRecord) []Limitation {
	if ownerDisputed(snap, s) {
		return []Limitation{{Code: LimitationOwnerConflict, Source: "fleet", Message: conflictReason(snap, s)}}
	}
	return nil
}

// conflictReason says why the current claims of a service disagree: two
// revisions of its newest owned version declare different owners, or its
// targets run revisions that do. A target link contributes when the revision it
// runs declares an owner the newest version does not; when any contributing
// link is a tag correlation the reason says so, since only an exact link may be
// presented as authoritative. Called on a restricted snapshot, it reads only the
// targets that view holds.
func conflictReason(snap *FleetSnapshot, s *ServiceRecord) string {
	if attestedRepositoriesDisagree(snap, s) {
		return "attested revisions of " + s.Name + " were signed from different repositories"
	}
	t, _ := ownerTier(snap, s)
	newest := newestOwnerRevision(snap, s, t)
	var base ownerClaimSet
	for _, r := range sameVersionRevisions(snap, s, newest, t) {
		o, _ := claimOwner(r, t)
		base.add(o)
	}
	if base.len() > 1 {
		return "two revisions of " + s.Name + " " + newest.Version + " declare different owners"
	}
	for _, tk := range s.Targets {
		tg := snap.Targets[tk]
		if tg.RevisionMatch != revisionMatchInferred {
			continue
		}
		if r := snap.Revisions[tg.ContractRevision]; r != nil {
			if o, ok := claimOwner(r, t); ok && !base.has(o) {
				return "targets of " + s.Name + " are linked by tag (inferred) to revisions that declare different owners"
			}
		}
	}
	return "targets of " + s.Name + " run revisions that declare different owners"
}

// serviceOwnerClaims is the DISTINCT owners a service holds NOW: the owners of
// [currentOwnerRevisions] or, when no revision declares one, the owners its
// observing sources read from the contracts its targets run. An older version's
// owner that nothing runs any more is history (a handover), not a claim.
// deriveOwner and every ownership answer of the query count through it, so an
// owner the operator reports is never "unowned" and a handover is never a
// conflict.
func serviceOwnerClaims(snap *FleetSnapshot, s *ServiceRecord) ownerClaimSet {
	var claims ownerClaimSet
	revs, t := currentOwnerRevisions(snap, s)
	for _, r := range revs {
		o, _ := claimOwner(r, t)
		claims.add(o)
	}
	if claims.len() == 0 {
		for _, tk := range s.Targets {
			claims.add(snap.Targets[tk].Owner)
		}
	}
	return claims
}

// currentOwnerRevisions is the revisions of a service whose owners are its
// current claims, in [LessRevisionChrono] order: the newest revision that
// declares an owner, every other revision of that version that declares one,
// and every revision a target of the service is linked to now (exact or
// inferred) that declares one.
func currentOwnerRevisions(snap *FleetSnapshot, s *ServiceRecord) ([]*ContractRevision, claimTier) {
	t, _ := ownerTier(snap, s)
	out := sameVersionRevisions(snap, s, newestOwnerRevision(snap, s, t), t)
	for _, tk := range s.Targets {
		if r := snap.Revisions[snap.Targets[tk].ContractRevision]; r != nil && r.ServiceKey == s.Key && !slices.Contains(out, r) {
			if _, ok := claimOwner(r, t); ok {
				out = append(out, r)
			}
		}
	}
	slices.SortFunc(out, func(a, b *ContractRevision) int {
		if LessRevisionChrono(a, b) {
			return -1
		}
		return 1
	})
	return out, t
}

// sameVersionRevisions is the revisions of s with newest's version that
// claim an owner in tier t; nil when newest is nil.
func sameVersionRevisions(snap *FleetSnapshot, s *ServiceRecord, newest *ContractRevision, t claimTier) []*ContractRevision {
	if newest == nil {
		return nil
	}
	var out []*ContractRevision
	for _, rk := range s.Revisions {
		if r := snap.Revisions[rk]; r != nil && r.Version == newest.Version {
			if _, ok := claimOwner(r, t); ok {
				out = append(out, r)
			}
		}
	}
	return out
}

// newestOwnerRevision is the newest revision (see [LessRevisionChrono]) of s
// that claims an owner in tier t, the one [deriveOwner] takes the service
// owner from; nil when none does.
func newestOwnerRevision(snap *FleetSnapshot, s *ServiceRecord, t claimTier) *ContractRevision {
	var newest *ContractRevision
	for _, rk := range s.Revisions {
		r := snap.Revisions[rk]
		if r == nil || (newest != nil && !LessRevisionChrono(newest, r)) {
			continue
		}
		if _, ok := claimOwner(r, t); ok {
			newest = r
		}
	}
	return newest
}

// ownerClaimSet accumulates the DISTINCT owners of a service's current claims
// (see [serviceOwnerClaims]), under the one identity the product routes by.
//
// That identity is the canonical owner KEY ([contract.Owner.KeyString]) — the
// namespace the name was declared in plus the name — and not the whole structured
// block. Two revisions declaring `{team: platform, dri: alice}` and `{team:
// platform, dri: bob}` are one owner here, because the product offers exactly one
// owner page for `team:platform` and both revisions are on it: calling that a
// conflict would report two teams arguing where one team has two people, and would
// keep the service out of the only ranking row it belongs to. The DRI still
// travels on each revision, where it was authored.
//
// Equally, the namespace is never dropped: a team named `alice` and a DRI named
// `alice` print the same label and are two owners, so a service whose revisions
// name each of them is a real dispute and must not be flattened into one claim by
// a display string they happen to share.
//
// An owner that declares only contacts has no key to collapse onto, so those are
// compared structurally instead — two different contact blocks are two claims,
// because there is nothing that says they are the same team. Neither contact list
// ORDER nor a repeated contact point is part of that comparison: the block is a set
// of contact points, so `[ops]` and `[ops, ops]` are one claim written two ways
// (see [contract.Owner.Equal]).
//
// [Query.ownershipState] and [deriveOwner] both count through this set, which is
// what makes the OWNER_CONFLICT limitation and the ownership tally incapable of
// disagreeing (see [OwnershipTally]).
type ownerClaimSet struct {
	keys    []string
	keyless []contract.Owner
}

// has reports whether o is already one of the claims, by the identity add uses.
func (c *ownerClaimSet) has(o contract.Owner) bool {
	if k := o.KeyString(); k != "" {
		return containsStr(c.keys, k)
	}
	return ownerSeen(c.keyless, o)
}

func (c *ownerClaimSet) add(o contract.Owner) {
	if o.IsEmpty() {
		return
	}
	if k := o.KeyString(); k != "" {
		if !containsStr(c.keys, k) {
			c.keys = append(c.keys, k)
		}
		return
	}
	if !ownerSeen(c.keyless, o) {
		c.keyless = append(c.keyless, o)
	}
}

// len is the number of distinct claims; soleKey is the one canonical key when the
// single claim has one, and empty when the sole claim is keyless.
func (c *ownerClaimSet) len() int { return len(c.keys) + len(c.keyless) }

func (c *ownerClaimSet) soleKey() string {
	if len(c.keys) == 1 && len(c.keyless) == 0 {
		return c.keys[0]
	}
	return ""
}

func ownerSeen(seen []contract.Owner, o contract.Owner) bool {
	for _, e := range seen {
		if e.Equal(o) {
			return true
		}
	}
	return false
}

// nameInferred is the inferred service.name entry of the revision of s with
// the smallest key when every revision of s inferred its name, else nil: one
// declared revision makes the name declared.
func nameInferred(snap *FleetSnapshot, s *ServiceRecord) *contract.Inferred {
	var found *contract.Inferred
	for _, rk := range slices.Sorted(slices.Values(s.Revisions)) {
		inf, ok := (&contract.Bundle{Inferred: snap.Revisions[rk].Inferred}).NameInferred()
		if !ok {
			return nil
		}
		if found == nil {
			found = &inf
		}
	}
	return found
}

func ensureService(snap *FleetSnapshot, domain, name string) *ServiceRecord {
	key := NewServiceKeyDomain(domain, name)
	s := snap.Services[key]
	if s == nil {
		s = &ServiceRecord{Key: key, Domain: domain, Name: name}
		snap.Services[key] = s
	}
	return s
}

// serviceStatus derives a conservative aggregate over a service's targets and
// revisions using the canonical severity order — Invalid > NonCompliant >
// Unknown > Warning > Compliant. Invalid is NEVER collapsed into NonCompliant: a
// structurally invalid contract is a distinct, worse state than a compliance
// violation. The aggregate is a triage summary, not "the health of the service";
// callers needing precision inspect per-target and per-revision state.
func serviceStatus(snap *FleetSnapshot, s *ServiceRecord) string {
	// An invalid contract revision is the worst state and dominates.
	for _, rk := range s.Revisions {
		if rev := snap.Revisions[rk]; rev.validated && !rev.Valid {
			return StatusInvalid
		}
	}
	// A contributed service is never a compliance subject: no target speaks
	// for it, and no contract was evaluated.
	if s.Contribution != nil {
		return StatusUnknown
	}
	// A trusted producer's report on an extension capability of a revision the
	// service runs (its current one when none runs) counts like a target.
	if st := worstStatus(targetAggregateStatus(snap, s), extensionStatus(snap, s)); st != "" {
		return st
	}
	if len(s.Revisions) > 0 {
		return StatusNotEvaluated
	}
	return StatusUnknown
}

// targetAggregateStatus reduces a service's targets to a single status by
// severity (Invalid > NonCompliant > Unknown > Warning > Compliant), returning
// "" when the service has no targets (or only non-canonical ones). Non-canonical
// compliance values are ignored rather than mis-ranked.
func targetAggregateStatus(snap *FleetSnapshot, s *ServiceRecord) string {
	var invalid, nonCompliant, unknown, warning, compliant bool
	for _, tk := range s.Targets {
		t := snap.Targets[tk]
		// A quarantined target has conflicting identity claims, so it is not
		// authoritative — it must not drive the service's aggregate status.
		if t.Quarantined {
			continue
		}
		switch t.Compliance {
		case StatusInvalid:
			invalid = true
		case StatusNonCompliant:
			nonCompliant = true
		case StatusUnknown:
			unknown = true
		case StatusWarning:
			warning = true
		case StatusCompliant:
			compliant = true
		}
	}
	switch {
	case invalid:
		return StatusInvalid
	case nonCompliant:
		return StatusNonCompliant
	case unknown:
		return StatusUnknown
	case warning:
		return StatusWarning
	case compliant:
		return StatusCompliant
	}
	return ""
}

// buildRelationships builds declared edges from EVERY revision (never a single
// "representative" or "latest" revision) and populates the revision-accurate and
// aggregated dependency indexes. Each edge records the exact FromRevision it
// originates from, so a target running an old revision never inherits a newer
// revision's dependencies.
func buildRelationships(snap *FleetSnapshot, cat *externalcatalog.Catalog) {
	revKeys := make([]RevisionKey, 0, len(snap.Revisions))
	for k := range snap.Revisions {
		revKeys = append(revKeys, k)
	}
	sort.Slice(revKeys, func(i, j int) bool { return revKeys[i] < revKeys[j] })

	for _, rk := range revKeys {
		rev := snap.Revisions[rk]
		if rev.Contract == nil {
			continue
		}
		edges := append(revisionDependencyEdges(snap, cat, rk, rev), revisionReferenceEdges(snap, rk, rev)...)
		inferred := revisionInferredEdges(snap, rk, rev)
		edges = append(edges, inferred...)
		if !snap.Services[rev.ServiceKey].Template {
			// A neighbourhood walks a record's inferred edges as it walks the
			// runtime ones; impact and gates read neither.
			for _, e := range inferred {
				snap.inferredForward[e.FromService] = appendUnique(snap.inferredForward[e.FromService], e.ToService)
				snap.inferredReverse[e.ToService] = appendUnique(snap.inferredReverse[e.ToService], e.FromService)
			}
		}
		// A golden path consumes nothing: every edge of every one of its
		// revisions stays out of Relationships, routed by the service.
		if snap.Services[rev.ServiceKey].Template {
			snap.templateEdges = append(snap.templateEdges, edges...)
		} else {
			snap.Relationships = append(snap.Relationships, edges...)
		}
	}
}

// observedInput pairs a raw observed edge with the id of the source that
// witnessed it, so folded observed relationships carry their provenance.
type observedInput struct {
	edge   ObservedEdge
	source string
	// pair is the targets both workload ends map to, nil when either end
	// maps to none or to more than one (see envPairJudge).
	pair *envPair
}

// callerKey is one caller with no contract of a service with one.
type callerKey struct {
	caller    string
	to        ServiceKey
	ambiguous bool
	entity    ServiceKey
}

// setUncontractedCallers lists the callers with no contract, sorted by the
// service they call.
func setUncontractedCallers(snap *FleetSnapshot, uncontracted map[callerKey]map[string]bool) {
	for k, sources := range uncontracted {
		snap.UncontractedCallers = append(snap.UncontractedCallers, UncontractedCaller{
			Caller: k.caller, ToService: k.to, Sources: slices.Sorted(maps.Keys(sources)), Ambiguous: k.ambiguous, Entity: k.entity,
		})
	}
	sort.Slice(snap.UncontractedCallers, func(i, j int) bool {
		a, b := snap.UncontractedCallers[i], snap.UncontractedCallers[j]
		if a.ToService != b.ToService {
			return a.ToService < b.ToService
		}
		return a.Caller < b.Caller
	})
}

// observedEdgeKey is one observed (caller, callee) pair: a service, or an
// external system when ext is set.
type observedEdgeKey struct {
	from, to ServiceKey
	ext      ExternalKey
}

// observedEdgeAgg is one observed edge's per-source aggregate: a multi-source
// edge keeps EVERY source's own count and window rather than crediting the
// first source with the total. match is how an external callee was named.
type observedEdgeAgg struct {
	total    int
	bySource map[string]*ObservedSourceStat
	sources  []string
	match    string
	pairs    []envPair
}

// add counts one raw observed edge from source into the aggregate.
func (a *observedEdgeAgg) add(oi observedInput) {
	a.total += oi.edge.Count
	st := a.bySource[oi.source]
	if st == nil {
		st = &ObservedSourceStat{Source: oi.source}
		a.bySource[oi.source] = st
		a.sources = append(a.sources, oi.source)
	}
	st.Count += oi.edge.Count
	mergeWindow(&st.FirstSeen, &st.LastSeen, oi.edge.FirstSeen, oi.edge.LastSeen)
	if oi.pair != nil {
		a.pairs = addEnvPair(a.pairs, *oi.pair)
	}
}

// edgeFold aggregates observed edges by key, in first-seen order.
type edgeFold struct {
	edges map[observedEdgeKey]*observedEdgeAgg
	order []observedEdgeKey
}

// count adds one raw observed edge to the aggregate of k.
func (f *edgeFold) count(k observedEdgeKey, oi observedInput, match string) {
	a := f.edges[k]
	if a == nil {
		if f.edges == nil {
			f.edges = map[observedEdgeKey]*observedEdgeAgg{}
		}
		a = &observedEdgeAgg{bySource: map[string]*ObservedSourceStat{}, match: match}
		f.edges[k] = a
		f.order = append(f.order, k)
	}
	a.add(oi)
}

// emit calls fn for every aggregated edge, sorted by key.
func (f *edgeFold) emit(snap *FleetSnapshot, fn func(*FleetSnapshot, observedEdgeKey, *observedEdgeAgg)) {
	slices.SortFunc(f.order, func(a, b observedEdgeKey) int {
		return cmp.Or(cmp.Compare(a.from, b.from), cmp.Compare(a.to, b.to), cmp.Compare(a.ext, b.ext))
	})
	for _, k := range f.order {
		fn(snap, k, f.edges[k])
	}
}

// foldObservedRelationships resolves runtime-observed edges to UNIQUE
// domain-qualified services and folds resolved edges into the snapshot as observed
// dependency relationships, summing counts for duplicate edges. An endpoint that
// matches zero or multiple services is never coerced to a domain — it becomes an
// explicit unresolved limitation so observed traffic can't be misattributed across
// domains (see [FleetSnapshot.ObservedNameResolver]). A callee that matches no
// service but names an external system (see [FleetSnapshot.ObservedExternal])
// is an observed edge to that system instead, kept out of every service index.
// Resolved service edges also extend the SEPARATE observed adjacency indexes,
// keeping the declared graph declared while making observed-only (shadow)
// callers reachable in the operational graph. An edge with an inferred end
// and no unresolved one is an inferred relationship (see
// emitInferredRelationship); an inferred caller of a declared service also
// stays an uncontracted caller.
func foldObservedRelationships(snap *FleetSnapshot, observed []observedInput) {
	if len(observed) == 0 {
		return
	}
	snap.observingSources = observingSourcesOf(observed)
	resolve := snap.ObservedNameResolver()
	seenLim := map[string]bool{}
	var edges, inferred edgeFold
	uncontracted := map[callerKey]map[string]bool{}
	judge := envPairJudge(snap)
	for _, oi := range observed {
		oi.pair = judge(oi.edge)
		fromKey, fromRes := resolve(oi.edge.From)
		toKey, toRes := resolve(oi.edge.To)
		if m, ok := snap.observedExternalMatch(oi.edge.To, toRes); ok {
			// An external callee is no unresolved service; an unresolved caller still is.
			addObservedLimitation(snap, seenLim, oi.edge.From, fromRes, oi.source)
			k := observedEdgeKey{from: fromKey, ext: ExternalKey(m.Identity)}
			switch fromRes {
			case ObservedResolved:
				edges.count(k, oi, m.Kind)
			case ObservedInferred:
				inferred.count(k, oi, m.Kind)
			}
			continue
		}
		if inferredEnds(fromRes, toRes) {
			if fromKey != toKey {
				inferred.count(observedEdgeKey{from: fromKey, to: toKey}, oi, "")
			}
			if toRes != ObservedResolved {
				continue
			}
		}
		addObservedLimitation(snap, seenLim, oi.edge.To, toRes, oi.source)
		if toRes == ObservedResolved && fromRes != ObservedResolved {
			// A caller with no contract of a service with one is kept, not dropped:
			// it is exactly the consumer a breaking change cannot rule out. An
			// inferred caller stays one, linked to its inferred service.
			k := callerKey{caller: oi.edge.From, to: toKey, ambiguous: fromRes == ObservedAmbiguous}
			if fromRes == ObservedInferred {
				// Recorded under its workload's full name, also when observed without its kind.
				k.caller, k.entity = snap.Services[fromKey].Name, fromKey
			}
			if uncontracted[k] == nil {
				uncontracted[k] = map[string]bool{}
			}
			uncontracted[k][oi.source] = true
			continue
		}
		addObservedLimitation(snap, seenLim, oi.edge.From, fromRes, oi.source)
		if fromRes == ObservedResolved && toRes == ObservedResolved {
			edges.count(observedEdgeKey{from: fromKey, to: toKey}, oi, "")
		}
	}
	setUncontractedCallers(snap, uncontracted)
	edges.emit(snap, emitObservedRelationship)
	inferred.emit(snap, emitInferredRelationship)
}

// emitObservedRelationship appends the observed relationship of one
// aggregated edge. A service edge extends the observed adjacency indexes; an
// edge to an external system records its caller on the system's record
// instead.
func emitObservedRelationship(snap *FleetSnapshot, k observedEdgeKey, a *observedEdgeAgg) {
	rel := aggregatedRelationship(k, a, ProvenanceObserved)
	snap.Relationships = append(snap.Relationships, rel)
	if k.ext != "" {
		recordObservedExternal(snap, k.from, k.ext, a.match, ProvenanceObserved)
		return
	}
	snap.observedForward[k.from] = appendUnique(snap.observedForward[k.from], k.to)
	snap.observedReverse[k.to] = appendUnique(snap.observedReverse[k.to], k.from)
}

// aggregatedRelationship is the dependency relationship of one aggregated
// runtime edge, with its per-source counts and window.
func aggregatedRelationship(k observedEdgeKey, a *observedEdgeAgg, provenance string) Relationship {
	sort.Strings(a.sources)
	stats := make([]ObservedSourceStat, 0, len(a.sources))
	var winFirst, winLast *time.Time
	for _, s := range a.sources {
		st := a.bySource[s]
		stats = append(stats, *st)
		mergeWindowP(&winFirst, &winLast, st.FirstSeen, st.LastSeen)
	}
	// Source names a single unambiguous contributor; multi-source edges leave it
	// empty and rely on the per-source breakdown.
	source := ""
	if len(a.sources) == 1 {
		source = a.sources[0]
	}
	return Relationship{
		FromService: k.from, ToService: k.to, ToExternal: k.ext, Type: RelationshipDependency,
		Provenance: provenance, Resolved: k.ext == "",
		ObservedCount: a.total, Source: source,
		ObservedSources: stats, FirstSeen: winFirst, LastSeen: winLast,
		envPairs: sortedEnvPairs(a.pairs),
	}
}

// reconcileDeclared classifies each declared dependency edge against the
// snapshot's observed edges: matched (an observed edge from any source
// corroborates it), declared-not-observed (full-coverage observation data exists
// but did not witness it), or insufficient (no full-coverage observation data —
// so it cannot be reconciled). This is the ONLY source of a relationship's
// reconciliation state; a declared edge is never "reconciled" merely because its
// provider is deployed. Runs after foldObservedRelationships so the observed
// edges are present. Sources in undeclaredOnly corroborate a declared edge they
// witnessed but are no observation data otherwise: they never report, or see
// only part of, the declared calls, so their silence says nothing about one.
// An edge only caution-only sources saw is neither: it is no observation, and
// its pair is recorded in snap.cautionPairs for the graph.
func reconcileDeclared(snap *FleetSnapshot, undeclaredOnly map[string]bool) {
	// The target is the service, or the external system the edge names.
	type edge struct {
		from, to ServiceKey
		ext      ExternalKey
	}
	observed := map[edge]bool{}
	haveObservation := false
	for _, rel := range snap.Relationships {
		// Runtime calls are observed dependencies; a reference never makes one.
		// An edge only caution-only sources saw never corroborates.
		if rel.Type == RelationshipDependency && rel.Provenance == ProvenanceObserved && snap.CautionOnly(rel) {
			if snap.cautionPairs == nil {
				snap.cautionPairs = map[[2]ServiceKey]bool{}
			}
			snap.cautionPairs[[2]ServiceKey{rel.FromService, rel.ToService}] = true
		} else if rel.Type == RelationshipDependency && rel.Provenance == ProvenanceObserved {
			observed[edge{rel.FromService, rel.ToService, rel.ToExternal}] = true
			haveObservation = haveObservation || slices.ContainsFunc(rel.ObservedSources, func(s ObservedSourceStat) bool { return !undeclaredOnly[s.Source] })
		}
	}
	for i := range snap.Relationships {
		rel := &snap.Relationships[i]
		// Dependencies only: reconciliation judges runtime calls, which a configuration reference never makes.
		if rel.Type != RelationshipDependency || rel.Provenance != ProvenanceDeclared {
			continue
		}
		switch {
		case observed[edge{rel.FromService, rel.ToService, rel.ToExternal}]:
			rel.Reconciliation = ReconciliationMatched
		case !haveObservation:
			rel.Reconciliation = ReconciliationInsufficient
		case rel.External:
			// Not expected to run where Pacto observes, so not seeing it is no finding.
		default:
			rel.Reconciliation = ReconciliationDeclaredNotObserved
		}
	}
}

// mergeWindow widens the (first,last) window pointed to by dst with a candidate
// non-zero [first,last] pair; nil dst pointers are allocated on first widening.
func mergeWindow(dstFirst, dstLast **time.Time, first, last time.Time) {
	if !first.IsZero() && (*dstFirst == nil || first.Before(**dstFirst)) {
		f := first
		*dstFirst = &f
	}
	if !last.IsZero() && (*dstLast == nil || last.After(**dstLast)) {
		l := last
		*dstLast = &l
	}
}

// mergeWindowP is mergeWindow over pointer inputs (per-source windows already
// stored as *time.Time).
func mergeWindowP(dstFirst, dstLast **time.Time, first, last *time.Time) {
	if first != nil {
		mergeWindow(dstFirst, dstLast, *first, time.Time{})
	}
	if last != nil {
		mergeWindow(dstFirst, dstLast, time.Time{}, *last)
	}
}

// addObservedLimitation records an unresolved observed identity once per
// (reason,name). It is a no-op when the name resolved. The message never echoes a
// registry credential or secret — only the observed name and why it is ambiguous.
func addObservedLimitation(snap *FleetSnapshot, seen map[string]bool, name string, res ObservedResolution, source string) {
	reason := ""
	switch res {
	case ObservedUnknown:
		reason = "unknown"
	case ObservedAmbiguous:
		reason = "ambiguous across domains"
		if _, _, kind, _, ok := ParseK8sCaller(name); ok && kind == "" {
			reason = "ambiguous: more than one workload of this name"
		}
	default:
		return
	}
	if seen[reason+":"+name] {
		return
	}
	seen[reason+":"+name] = true
	snap.Limitations = append(snap.Limitations, Limitation{
		Code: LimitationObservedIdentityUnresolved, Source: source,
		Message: "observed service " + name + " could not be mapped to a unique fleet service (" + reason + "); its runtime edge is not attributed to any domain-qualified service",
	})
}

// revisionDependencyEdges builds one dependency edge per declared dependency of a
// revision, updating the revision-accurate and aggregated indexes for resolved
// edges of a service that is not a golden path.
func revisionDependencyEdges(snap *FleetSnapshot, cat *externalcatalog.Catalog, rk RevisionKey, rev *ContractRevision) []Relationship {
	var out []Relationship
	index := !snap.Services[rev.ServiceKey].Template
	for i, dep := range rev.Contract.Dependencies {
		var toSvc ServiceKey
		ok := false
		inferredKey, inferredPath, inferred := inferredLocation(dep.Ref)
		// An external dependency with no contract has only a local name, which
		// may be any unrelated fleet service's: it never resolves. Nor does a
		// ref to an inferred contract, which is never a service.
		if !inferred && (dep.Ref != "" || !dep.External) {
			toSvc, ok = resolveDepService(snap, rev.Domain, dep)
		}
		rel := Relationship{
			FromService: rev.ServiceKey, FromRevision: rk, To: dep.Name,
			Type: RelationshipDependency, Provenance: ProvenanceDeclared,
			Required: dep.Required, Compatibility: dep.Compatibility,
			RequestedRef: dep.Ref, Resolved: ok, ToService: toSvc,
			External: dep.External, Version: dep.Version, NameFromRef: dep.NameFromRef,
		}
		if rev.Lock != nil {
			if e, found := rev.Lock.Dependency(dep.Name); found {
				rel.LockedDigest = e.Digest
				rel.LockedVersion = e.Version
			}
		}
		switch {
		case inferred:
			joinInferredRef(snap, cat, rk, rev, i, dep, inferredKey, inferredPath, &rel)
		case !ok && dep.External:
			rel.ToExternal, rel.Reason = joinExternal(snap, cat, rk, rev, i, dep)
		case !ok && dep.NameFromRef:
			rel.Reason = derivedNameReason(dep.Ref)
		case !ok:
			rel.Reason = "no service in the fleet resolves this dependency ref"
		default:
			if index {
				indexConsumption(snap, rk, rev.ServiceKey, toSvc)
			}
			rel.ResolvedRevision = resolveDepRevision(snap, toSvc, rel.LockedDigest)
			rel.RangeStatus, rel.RangeReason = dependencyRangeStatus(snap, rel)
			rel.DeprecatedInterfaces = providerDeprecations(snap, rel)
			rel.ProviderLifecycle = providerServiceLifecycle(snap, rel)
			// A contested lock reaches this edge exactly as a missing one does — nil —
			// so the pin is absent either way and only the reason distinguishes them.
			// mergeRevisionLock deterministically discards BOTH sides of a lock
			// disagreement: the winner is "neither", identically in either contribution
			// order, which is the property an arrival-order (or any label-based)
			// tiebreak would have broken by naming one of two real bundles at random.
			// Without the carried reason this renders as a resolved dependency that
			// simply has no lockfile, and the reader re-runs `pacto lock`, regenerates
			// identical pins and sees nothing change. Empty when the revision genuinely
			// carries no lock, and never set on an unresolved edge, whose headline
			// problem is that it names no service at all.
			rel.Reason = rev.lockConflict
		}
		rel.DependencyBehavior = dependencyBehavior(snap, rev.Service, dep, rel)
		out = append(out, rel)
	}
	return out
}

// inferredRecordKind is the [InferredSource] kind of an edge a revision's
// inferred record names.
const inferredRecordKind = "inferredRecord"

// revisionInferredEdges builds one inferred dependency edge per
// dependencies[<name>] field of the revision's inferred record that the
// revision does not declare and that resolves to another fleet service. The
// edge's compatibility is unknown and it enters no service index (see
// [ConsumesEdge]), so it never satisfies a range or clears a gate.
// ponytail: an unresolved name (often a database or cache host) makes no edge;
// add one when the graph should list inferred gaps beside declared ones.
func revisionInferredEdges(snap *FleetSnapshot, rk RevisionKey, rev *ContractRevision) []Relationship {
	if rev.Record == nil {
		return nil
	}
	declared := map[string]bool{}
	for _, d := range rev.Contract.Dependencies {
		declared[d.Name] = true
	}
	var out []Relationship
	for _, f := range rev.Record.Fields {
		name, ok := strings.CutPrefix(f.Field, "dependencies[")
		name, closed := strings.CutSuffix(name, "]")
		// Exactly dependencies[<name>]: a dependencies[<name>].compatibility
		// key (a proposed range) never names an edge.
		if !ok || !closed || name == "" || strings.ContainsAny(name, "[]") || declared[name] || f.External != nil { // an external projects through recordExternalUses
			continue
		}
		to, resolved := resolveDepService(snap, resolveDomain(rev), contract.Dependency{Name: name})
		if !resolved || to == rev.ServiceKey {
			continue
		}
		srcs := []InferredSource{{Source: rev.Source, Kind: inferredRecordKind, Where: f.Source, Confidence: inferredConfidence(f.Confidence)}}
		out = append(out, Relationship{FromService: rev.ServiceKey, FromRevision: rk, To: name, ToService: to, Resolved: true,
			Type: RelationshipDependency, Provenance: ProvenanceInferred, Compatibility: contract.CompatibilityUnknown,
			InferredSources: srcs, Confidence: strongestConfidence(srcs)})
	}
	return out
}

// externalReason is the reason of an external dependency edge that joins no
// external system.
const externalReason = "an external dependency: this Pacto system does not deliver it"

// joinExternal names the external system dependency idx of revision rk calls
// and records the declaration on its [ExternalRecord]. A dependency with a ref
// resolves through its contract, and one whose name cannot be normalized (or
// normalizes to the reserved other-egress) joins nothing: both keep
// externalReason. A golden path consumes nothing, so its edge names the
// identity but no record counts it.
func joinExternal(snap *FleetSnapshot, cat *externalcatalog.Catalog, rk RevisionKey, rev *ContractRevision, idx int, dep contract.Dependency) (ExternalKey, string) {
	if dep.Ref != "" {
		return "", externalReason
	}
	id, match, err := externalcatalog.DeclaredIdentity(cat, dep.Identity, dep.Name)
	if err != nil {
		return "", externalReason
	}
	key := ExternalKey(id)
	recordExternalDeclaration(snap, cat, rk, rev, idx, dep, key, match)
	return key, "joined external " + string(id) + " through " + match + "; this Pacto system does not deliver it"
}

// recordExternalDeclaration records on key's [ExternalRecord] that dependency
// idx of revision rk declares it, joined by match. A golden path consumes
// nothing, so no record counts it.
func recordExternalDeclaration(snap *FleetSnapshot, cat *externalcatalog.Catalog, rk RevisionKey, rev *ContractRevision, idx int, dep contract.Dependency, key ExternalKey, match string) {
	if snap.Services[rev.ServiceKey].Template {
		return
	}
	rec := externalRecord(snap, cat, key)
	rec.declarations[rev.ServiceKey]++
	src := ExternalSource{Kind: ExternalSourceContract, Service: rev.ServiceKey, Revision: rk, Dependency: dep.Name, Index: idx,
		Version: dep.Version, Required: dep.Required, Provenance: ProvenanceDeclared, Match: match}
	// One source per consumer and position, from the newest revision that
	// declares it, so every consumer keeps its evidence and the record stays
	// bounded by the declarations the snapshot already holds.
	for i, s := range rec.Sources {
		if s.Service == src.Service && s.Index == idx {
			if LessRevisionChrono(snap.Revisions[s.Revision], rev) {
				rec.Sources[i] = src
			}
			return
		}
	}
	rec.Sources = append(rec.Sources, src)
}

// externalRecord returns the record of key, creating it classified by the
// catalog entry of that identity, else unclassified. finalizeExternal labels it.
func externalRecord(snap *FleetSnapshot, cat *externalcatalog.Catalog, key ExternalKey) *ExternalRecord {
	if rec := snap.Externals[key]; rec != nil {
		return rec
	}
	if snap.Externals == nil {
		snap.Externals = map[ExternalKey]*ExternalRecord{}
	}
	rec := &ExternalRecord{Key: key, Class: externalcatalog.ClassUnclassified, declarations: map[ServiceKey]int{}}
	if e, ref, ok := cat.Entry(externalid.ID(key)); ok {
		rec.Class = e.Class
		// A managed resource classified by its provider/service entry keeps
		// its own name: every RDS instance labelled "Amazon RDS" says nothing.
		if e.Identity == externalid.ID(key) {
			rec.catalogLabel = e.Label
		}
		rec.Catalog = &ExternalCatalogMatch{Origin: ref.Origin, Digest: ref.Digest, Entry: string(e.Identity), OpenAPI: e.OpenAPI}
		rec.Aliases = slices.Clone(e.Aliases)
	}
	snap.Externals[key] = rec
	return rec
}

// finalizeExternals orders every external record's consumers and sources and
// fills its totals. It never bounds a record: answers do.
func finalizeExternals(snap *FleetSnapshot) {
	for _, rec := range snap.Externals {
		for i := range rec.Sources {
			rec.Sources[i].Superseded = supersededSource(snap, rec.Sources[i])
		}
		finalizeExternal(rec)
	}
}

// supersededSource reports whether the consumer has a revision newer than the
// one src comes from. src is from the newest revision declaring it at its
// position, so any newer revision no longer does.
func supersededSource(snap *FleetSnapshot, src ExternalSource) bool {
	rev := snap.Revisions[src.Revision]
	if src.Kind != ExternalSourceContract || rev == nil {
		return false // observed and inferred sources carry no revision
	}
	return slices.ContainsFunc(snap.Services[src.Service].Revisions, func(k RevisionKey) bool {
		r := snap.Revisions[k]
		return r != nil && LessRevisionChrono(rev, r)
	})
}

// joinStrength orders matches strongest first.
var joinStrength = []string{externalcatalog.MatchIdentity, externalcatalog.MatchCatalogAlias, externalcatalog.MatchHost, externalcatalog.MatchNormalizedName}

// finalizeExternal derives a record's consumers and totals from its
// declarations, orders its sources by consumer then position, and derives
// its label and join from those sources (decision 5: only visible evidence).
func finalizeExternal(rec *ExternalRecord) {
	rec.Consumers = slices.Sorted(maps.Keys(rec.declarations))
	rec.ConsumersTotal = len(rec.Consumers)
	rec.SourcesTotal = 0
	for _, n := range rec.declarations {
		rec.SourcesTotal += n
	}
	slices.SortFunc(rec.Sources, func(a, b ExternalSource) int {
		return cmp.Or(strings.Compare(string(a.Service), string(b.Service)), strings.Compare(a.Kind, b.Kind), a.Index-b.Index,
			strings.Compare(string(a.Target), string(b.Target)), strings.Compare(a.Match, b.Match))
	})
	rec.Label = rec.catalogLabel
	if i := slices.IndexFunc(rec.Sources, func(s ExternalSource) bool { return s.Dependency != "" }); rec.Label == "" && i >= 0 {
		rec.Label = rec.Sources[i].Dependency
	}
	if rec.Label == "" {
		rec.Label = string(rec.Key) // only observed or inferred sources: they name no dependency
	}
	rec.JoinedBy = ""
	for _, m := range joinStrength {
		// A ref to its inferred contract names the identity itself.
		if slices.ContainsFunc(rec.Sources, func(s ExternalSource) bool {
			return s.Match == m || m == externalcatalog.MatchIdentity && s.Match == matchInferredContract
		}) {
			rec.JoinedBy = m
			break
		}
	}
	rec.HiddenConsumers = len(rec.hidden)
}

// externalCatalogRefs projects the catalogs a build classified with.
func externalCatalogRefs(refs []externalcatalog.Ref) []ExternalCatalogRef {
	out := make([]ExternalCatalogRef, len(refs))
	for i, r := range refs {
		out[i] = ExternalCatalogRef(r)
	}
	return out
}

// revisionReferenceEdges builds config- and policy-reference edges (kept
// distinct) for a revision.
func revisionReferenceEdges(snap *FleetSnapshot, rk RevisionKey, rev *ContractRevision) []Relationship {
	var out []Relationship
	index := !snap.Services[rev.ServiceKey].Template
	for _, ref := range rev.Contract.ReferenceRefs() {
		typ := RelationshipConfigRef
		if ref.Kind == contract.ReferenceKindPolicy {
			typ = RelationshipPolicyRef
		}
		rel := Relationship{
			FromService: rev.ServiceKey, FromRevision: rk, To: ref.Name,
			Type: typ, Provenance: ProvenanceDeclared, RequestedRef: ref.Ref,
		}
		locked, lockNote := lockReference(rev, ref)
		if locked != nil {
			rel.LockedDigest, rel.LockedVersion = locked.Digest, locked.Version
		}
		target, reason := resolveRefRevision(snap, rev, ref, locked, lockNote)
		if target == nil {
			rel.Reason = reason
		} else {
			rel.Resolved, rel.ToService, rel.ResolvedRevision = true, target.ServiceKey, target.Key
			rel.ProviderLifecycle = providerServiceLifecycle(snap, rel)
			// A configuration reference consumes its provider like a dependency
			// does, so traversal, dependents and impact see it; a policy only
			// constrains this contract and has no runtime consumer to break.
			// A golden path consumes nothing, so it is never indexed.
			if typ == RelationshipConfigRef && index {
				indexConsumption(snap, rk, rev.ServiceKey, target.ServiceKey)
			}
		}
		out = append(out, rel)
	}
	return out
}

// indexConsumption records that revision rk of service from consumes the
// service to in the revision-accurate and aggregated dependency indexes.
func indexConsumption(snap *FleetSnapshot, rk RevisionKey, from, to ServiceKey) {
	snap.forwardDepsByRevision[rk] = appendUnique(snap.forwardDepsByRevision[rk], to)
	snap.forwardDeps[from] = appendUnique(snap.forwardDeps[from], to)
	snap.reverseDeps[to] = appendUnique(snap.reverseDeps[to], from)
}

// Range statuses of a declared dependency (see [Relationship.RangeStatus]).
const (
	RangeSatisfied   = "satisfied"
	RangeUnsatisfied = "unsatisfied"
	RangeUnknown     = "unknown"
)

// CheckRange reports whether version satisfies the compatibility range
// constraint. Either side empty or unreadable by semver is RangeUnknown: an
// unreadable range is uncertainty, never a pass or a fail.
func CheckRange(constraint, version string) string {
	if constraint == "" || version == "" {
		return RangeUnknown
	}
	rng, err := contract.ParseRange(constraint)
	if err != nil {
		return RangeUnknown
	}
	ok, err := rng.Check(version)
	switch {
	case err != nil:
		return RangeUnknown
	case ok:
		return RangeSatisfied
	}
	return RangeUnsatisfied
}

// dependencyRangeStatus checks a resolved dependency's range against the
// provider: the locked revision when the lock pins one, otherwise every revision
// of the provider the fleet holds. Revisions that disagree make it unknown,
// because the fleet cannot tell which one the consumer reaches. A revision only
// this installation's operator signed is never checked: when it is all the
// dependency reaches, the range is unknown with reason INFERRED_PROVIDER.
func dependencyRangeStatus(snap *FleetSnapshot, rel Relationship) (status, reason string) {
	if rel.Compatibility == "" {
		return "", ""
	}
	versions := providerVersions(snap, rel)
	if len(versions) == 0 && slices.ContainsFunc(providerRevisions(snap, rel), func(rk RevisionKey) bool { return operatorSigned(snap.Revisions[rk]) }) {
		return RangeUnknown, RangeReasonInferredProvider
	}
	status = RangeUnknown
	for i, v := range versions {
		st := CheckRange(rel.Compatibility, v)
		if i > 0 && st != status {
			return RangeUnknown, ""
		}
		status = st
	}
	return status, ""
}

// providerVersions lists the versions a resolved dependency can reach: the
// locked revision's when the lock pins one, otherwise every revision the
// provider has in the fleet, in revision order. A revision only this
// installation's operator signed is left out (see [operatorSigned]).
func providerVersions(snap *FleetSnapshot, rel Relationship) []string {
	revs := providerRevisions(snap, rel)
	out := make([]string, 0, len(revs))
	for _, rk := range revs {
		if r := snap.Revisions[rk]; !operatorSigned(r) {
			out = append(out, r.Version)
		}
	}
	return out
}

// providerRevisions are the revisions a resolved dependency can reach: the
// locked one when the lock pins one, otherwise every revision of the provider.
func providerRevisions(snap *FleetSnapshot, rel Relationship) []RevisionKey {
	if rel.ResolvedRevision != "" {
		return []RevisionKey{rel.ResolvedRevision}
	}
	return snap.Services[rel.ToService].Revisions
}

// resolveDepRevision pins the exact revision of the resolved service that a
// dependency points at, when a lock digest identifies it; otherwise "".
func resolveDepRevision(snap *FleetSnapshot, toSvc ServiceKey, lockedDigest string) RevisionKey {
	if lockedDigest == "" {
		return ""
	}
	for _, rev := range snap.Revisions {
		if rev.ServiceKey == toSvc && rev.Digest == lockedDigest {
			return rev.Key
		}
	}
	return ""
}

// resolveDepService resolves a dependency to a logical service, preferring the
// depending revision's own domain (a bare dependency ref carries no domain, so it
// resolves within the same domain), tolerating a "-pacto" bundle-name suffix.
//
// A name derived from the ref is a guess from a path, so it resolves only to a
// service of the ref's own domain with a revision read from that repository: an
// oci://ghcr.io/other-org/payments never reaches the fleet's own payments, and a
// local ref, which names no repository, never resolves.
func resolveDepService(snap *FleetSnapshot, fromDomain string, dep contract.Dependency) (ServiceKey, bool) {
	if dep.NameFromRef {
		repo := ociRepository(dep.Ref)
		key := lookupServiceInDomain(snap, contract.OCIDomain(dep.Ref), dep.Name)
		if repo == "" || key == "" || !publishedAt(snap, key, repo) {
			return "", false
		}
		return key, true
	}
	key := lookupServiceInDomain(snap, fromDomain, dep.Name)
	return key, key != ""
}

// derivedNameReason says why a dependency whose name was derived from ref did
// not resolve.
func derivedNameReason(ref string) string {
	if repo := ociRepository(ref); repo != "" {
		return "the name was derived from the ref and no service in the fleet is published at " + repo
	}
	return "the name was derived from a local ref, which names no published repository: declare the name"
}

// ociRepository is the repository an oci:// ref names, without scheme, tag or
// digest; "" for any other ref.
func ociRepository(ref string) string {
	if !strings.HasPrefix(ref, contract.OCIScheme) {
		return ""
	}
	repo, _, _ := contract.SplitOCIRef(contract.TrimOCIScheme(ref))
	return repo
}

// publishedAt reports whether some revision of svc was read from repo.
func publishedAt(snap *FleetSnapshot, svc ServiceKey, repo string) bool {
	for _, rk := range snap.Services[svc].Revisions {
		rev := snap.Revisions[rk]
		for _, ref := range []string{rev.RequestedRef, rev.ResolvedRef} {
			if r, _, _ := contract.SplitOCIRef(contract.TrimOCIScheme(ref)); r == repo {
				return true
			}
		}
	}
	return false
}

// lockReference returns the lock entry that records what THIS revision's own
// declared config/policy reference actually resolved to, or nil plus the reason
// no entry authoritatively answers for it.
//
// pacto.lock pins the TRANSITIVE reference closure of one root contract, so it
// routinely holds several entries sharing a (kind, name): a configuration scope
// called "settings" the root declared, and another called "settings" declared by
// a bundle the root reached through a different reference. Both digests are real
// and each is authoritative for a different reference. Choosing between them by
// label — by slice order, sorted order, or any other tiebreak — yields a digest
// that names the wrong bundle and renders as a confident canonical Product link
// the contract never declared. Only the declaring contract recorded in the entry
// settles it, so only the root's own entries answer here.
func lockReference(rev *ContractRevision, ref contract.ReferenceRef) (*lock.Reference, string) {
	l := rev.Lock
	if l == nil {
		// A discarded lock is not the same as no lock. Saying nothing here would tell
		// the operator that pacto.lock recorded no resolution, so they would re-run
		// `pacto lock`, regenerate identical pins and see nothing change.
		return nil, rev.lockConflict
	}
	// A lock describes exactly one root contract, and "declared by the root" is
	// only meaningful about that one. A lock that names a DIFFERENT contract was
	// attached to the wrong revision, and its root entries are someone else's. A
	// lock that names no contract at all is unstated rather than contradictory, so
	// it is read at face value.
	if l.Root.Name != "" && l.Root.Name != rev.Contract.Service.Name {
		return nil, lockBelongsElsewhere(l)
	}
	if l.Root.Version != "" && l.Root.Version != rev.Contract.Service.Version {
		return nil, lockBelongsElsewhere(l)
	}
	if l.LockVersion < lock.RootOccurrenceLockVersion {
		return nil, "this pacto.lock predates reference-occurrence identity (lockVersion " +
			strconv.Itoa(l.LockVersion) + "), so which of its recorded resolutions belongs to this " +
			"declared reference cannot be established; re-run `pacto lock` to record it"
	}
	e, found := l.RootReference(ref.Kind, ref.Name)
	if !found {
		return nil, ""
	}
	return e, ""
}

// lockBelongsElsewhere explains, for a reader who has to work out why the pins
// are missing, which contract the attached lock actually describes.
func lockBelongsElsewhere(l *lock.Lock) string {
	who := l.Root.Name
	if who == "" {
		who = "another contract"
	} else if l.Root.Version != "" {
		who += "@" + l.Root.Version
	}
	return "the pacto.lock carried by this revision records the closure of " + who +
		", not of this contract, so it says nothing about this reference"
}

// resolveRefRevision resolves a config/policy reference to the EXACT fleet
// revision it names, or explains why it cannot. It returns nil and a reason
// rather than a guess.
//
// A Product reference link is a claim about identity, so it is made only from
// evidence of identity. The only such evidence a snapshot holds is an immutable
// content identifier for the referenced bundle:
//
//   - the digest (or contentHash) pacto.lock recorded when it actually resolved,
//     pulled and hashed that bundle — the same authority resolveDepRevision uses
//     for dependencies; or
//   - a digest the author pinned in the ref itself (oci://repo@sha256:...), which
//     IS the content address of exactly one bundle.
//
// Matching that identifier against a revision the snapshot already holds yields
// the referenced bundle's REAL contract.service.name, because the revision was
// built from that contract.
//
// What is deliberately NOT evidence, and used to be:
//
//   - the leaf of the ref's repository path. An artifact repository is named by
//     whoever pushed it; `oci://ghcr.io/acme/shared-config-contract` may publish a
//     contract whose service.name is `platform-settings`, and an unrelated service
//     genuinely called `shared-config-contract` may exist in the same domain. The
//     basename therefore both misses the real destination and invents a false one.
//   - ReferenceRef.Name. For a configuration that is the SCOPE name and for a
//     policy the POLICY ENTRY name — author-chosen labels for a slot in the
//     referring contract, with no relationship to the referenced service. A policy
//     called "payments" pointing at a retired bundle must not become a link to the
//     payments service.
//
// The domain boundary is unchanged: the destination revision must live in the
// referring revision's own domain, so two same-named services in two domains can
// never resolve to each other, and an identifier matching several services in one
// domain is ambiguous rather than arbitrarily won. Dependency resolution is
// untouched.
// lockNote, when non-empty, explains why the revision's lock could not answer for
// this reference occurrence; it is preferred over the generic reason when no
// authoritative identity is available at all.
func resolveRefRevision(snap *FleetSnapshot, from *ContractRevision, ref contract.ReferenceRef, locked *lock.Reference, lockNote string) (*ContractRevision, string) {
	ids := authoritativeRefIdentities(ref, locked)
	if len(ids) == 0 {
		if lockNote != "" {
			return nil, lockNote
		}
		return nil, "this reference carries no resolved identity: it is not digest-pinned and pacto.lock records no unambiguous resolution for it, so the destination contract is unknown"
	}
	var match *ContractRevision
	for _, id := range ids {
		for _, rev := range snap.Revisions {
			if rev.Domain != from.Domain || rev.Digest != id {
				continue
			}
			if match != nil && match.Key != rev.Key {
				// Two different revisions in one domain claiming one content identity
				// cannot both be the destination, and neither is more right than the other.
				return nil, "the resolved identity of this reference matches more than one revision in this domain, so its destination is ambiguous"
			}
			match = rev
		}
	}
	if match == nil {
		return nil, "the bundle this reference resolves to is not present in this snapshot, so its destination contract is unknown"
	}
	return match, ""
}

// authoritativeRefIdentities lists the immutable content identifiers that
// genuinely identify a reference's destination, most authoritative first: what
// the lock recorded when it resolved the reference for real, then a digest the
// author pinned in the ref. It returns nothing for a mutable tag, a local path or
// an unlocked reference — cases where Pacto has not established what the ref
// points at.
func authoritativeRefIdentities(ref contract.ReferenceRef, locked *lock.Reference) []string {
	var ids []string
	add := func(id string) {
		if id != "" && !containsStr(ids, id) {
			ids = append(ids, id)
		}
	}
	if locked != nil {
		add(locked.Digest)
		add(locked.ContentHash)
	}
	if _, dgst, err := ParseCanonicalOCIRef(ref.Ref); err == nil {
		add(dgst.String())
	}
	return ids
}

// lookupServiceInDomain finds a service by name within ONE domain, tolerating the
// "-pacto" bundle-name suffix. It returns "" when nothing matches; it never falls
// back to another domain, because a same-named service elsewhere is a different
// service.
func lookupServiceInDomain(snap *FleetSnapshot, domain, name string) ServiceKey {
	if name == "" {
		return ""
	}
	// An inferred service is never a dependency provider: a dependency named
	// like a workload stays unresolved.
	if s := snap.Services[NewServiceKeyDomain(domain, name)]; s != nil && !s.Inferred() {
		return s.Key
	}
	if stripped := strings.TrimSuffix(name, "-pacto"); stripped != name {
		if s := snap.Services[NewServiceKeyDomain(domain, stripped)]; s != nil && !s.Inferred() {
			return s.Key
		}
	}
	return ""
}

// classifyCompleteness derives snapshot completeness from source health.
func classifyCompleteness(snap *FleetSnapshot) Completeness {
	return completenessExcept(snap)
}

// ConsumerCompleteness is the completeness of what can name a consumer, for
// impact and gate answers. A workload source reports only inferred services
// and a registry listing only templates and offerings, none of them ever a
// consumer, so their health never makes such an answer partial; their
// limitations still say what they could not read. A view partial for any
// other reason stays partial.
func (s *FleetSnapshot) ConsumerCompleteness() Completeness {
	if s.Completeness != CompletenessPartial || !s.workloadOnlyPartial {
		return s.Completeness
	}
	return completenessExcept(s, nonConsumerSourceKinds...)
}

// completenessExcept is classifyCompleteness ignoring the sources of the
// kinds in skip.
func completenessExcept(snap *FleetSnapshot, skip ...string) Completeness {
	for _, st := range snap.Sources {
		if st.degraded() && !slices.Contains(skip, st.Kind) {
			return CompletenessPartial
		}
	}
	for _, l := range snap.Limitations {
		switch l.Code {
		case LimitationExternalCatalogUnavailable, LimitationInferredRecordUnreadable,
			LimitationDeclarationSignerUnverified, LimitationDeclarationSignerNotAllowed, LimitationOwnerDeclarationUnverified:
			return CompletenessPartial
		}
	}
	if len(snap.Services) == 0 && len(snap.Targets) == 0 {
		return CompletenessEmpty
	}
	return CompletenessComplete
}

// degradedLimitations turns non-available source states into stale/partial
// snapshot limitations (unavailable limitations are added inline in Build).
func degradedLimitations(snap *FleetSnapshot) []Limitation {
	var out []Limitation
	for _, st := range snap.Sources {
		if st.Advisory {
			continue
		}
		switch st.Status {
		case SourceStale:
			out = append(out, Limitation{Code: LimitationSourceStale, Source: st.ID,
				Message: "source " + st.ID + " is stale; its records may be out of date"})
		case SourcePartial:
			out = append(out, Limitation{Code: LimitationSourcePartial, Source: st.ID,
				Message: "source " + st.ID + " returned a partial result"})
		}
	}
	return out
}

// sortSnapshot imposes deterministic ordering on every slice.
func sortSnapshot(snap *FleetSnapshot) {
	sortRelationships(snap.Relationships)
	sortRelationships(snap.InferredRelationships)
	slices.SortFunc(snap.Replacements, func(a, b Replacement) int { return cmp.Compare(a.From, b.From) })
	sortRelationships(snap.templateEdges)
	sort.Slice(snap.Sources, func(i, j int) bool { return snap.Sources[i].ID < snap.Sources[j].ID })
	sortProvisioning(snap.Provisioning)
	sortUsage(snap.Usage)
	sortSnapshotLimitations(snap)
	for _, s := range snap.Services {
		sort.Slice(s.Revisions, func(i, j int) bool { return s.Revisions[i] < s.Revisions[j] })
		sort.Slice(s.Targets, func(i, j int) bool { return s.Targets[i] < s.Targets[j] })
		sort.Strings(s.Sources)
	}
	// A target's contributing-source set is provenance, not an ordered list; sort
	// it so a merged target serializes identically regardless of source order.
	for _, t := range snap.Targets {
		sort.Strings(t.Sources)
	}
	// Same for a revision, and Source goes with it. A revision is immutable content
	// keyed by its digest, so two sources that contribute it have contributed the
	// same thing and neither is a more authoritative "source of record" than the
	// other. Both fields used to be arrival-ordered — Sources kept appendUnique
	// order and Source stayed first-contributor-wins — so Build(a,b) and Build(b,a)
	// serialized differently from identical inputs. computeSnapshotID hashes that
	// serialization, so the order-independence this snapshot's identity is supposed
	// to carry did not hold. Taking the lexicographically first contributor makes
	// the tie deterministic instead of leaving it to goroutine bookkeeping.
	for _, r := range snap.Revisions {
		sort.Strings(r.Sources)
		if len(r.Sources) > 0 {
			r.Source = r.Sources[0]
		}
	}
	for k := range snap.reverseDeps {
		sortKeys(snap.reverseDeps[k])
	}
	for k := range snap.forwardDeps {
		sortKeys(snap.forwardDeps[k])
	}
	for k := range snap.forwardDepsByRevision {
		sortKeys(snap.forwardDepsByRevision[k])
	}
}

// sortRelationships orders relationships by source service, revision, type,
// destination and requested ref.
func sortRelationships(rels []Relationship) {
	sort.Slice(rels, func(i, j int) bool {
		a, b := rels[i], rels[j]
		if a.FromService != b.FromService {
			return a.FromService < b.FromService
		}
		if a.FromRevision != b.FromRevision {
			return a.FromRevision < b.FromRevision
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if a.To != b.To {
			return a.To < b.To
		}
		// Observed and inferred edges name no To: their target and
		// provenance keep the order total.
		return cmp.Or(cmp.Compare(a.RequestedRef, b.RequestedRef), cmp.Compare(a.ToService, b.ToService),
			cmp.Compare(a.ToExternal, b.ToExternal), cmp.Compare(a.Provenance, b.Provenance)) < 0
	})
}

// sortSnapshotLimitations orders the snapshot's limitations by code, source
// and message.
func sortSnapshotLimitations(snap *FleetSnapshot) {
	sort.Slice(snap.Limitations, func(i, j int) bool {
		a, b := snap.Limitations[i], snap.Limitations[j]
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		// Message is the tertiary key so limitations with the same code+source but
		// different subjects order deterministically (permutation-invariant).
		return a.Message < b.Message
	})
}

// sortKeys sorts service keys lexicographically in place.
func sortKeys(ks []ServiceKey) {
	sort.Slice(ks, func(i, j int) bool { return ks[i] < ks[j] })
}

// appendUnique appends v to s if not already present and non-empty. It is generic
// over string-like identities (bare source ids and domain-qualified ServiceKeys).
func appendUnique[T ~string](s []T, v T) []T {
	if v == "" {
		return s
	}
	for _, e := range s {
		if e == v {
			return s
		}
	}
	return append(s, v)
}

// setDifferences records the configuration values rev's environments disagree
// on, bounded, or why they cannot be read. Fewer than two environments compare
// nothing.
func setDifferences(rev *ContractRevision, c *contract.Contract, fsys fs.FS) {
	if len(c.Environments) < 2 {
		return
	}
	diffs, err := validation.EnvironmentDifferences(c, fsys, nil)
	if err != nil {
		rev.differencesErr = err.Error()
		return
	}
	rev.differences, rev.differencesTotal, _ = boundSlice(diffs, MaxDetailPreview)
}
