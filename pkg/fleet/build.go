package fleet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"sort"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/trianalab/pacto/v3/pkg/capability"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/finding"
	"github.com/trianalab/pacto/v3/pkg/lock"
	"github.com/trianalab/pacto/v3/pkg/openapi"
	"github.com/trianalab/pacto/v3/pkg/readiness"
	"github.com/trianalab/pacto/v3/pkg/skills"
	"github.com/trianalab/pacto/v3/pkg/validation"
)

// Bounds keep the projected snapshot from growing unboundedly on pathological
// bundles (a huge OpenAPI spec, a docs tree with thousands of files).
const (
	maxToolsPerRevision = 500
	maxDocRefs          = 200
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
	// AllowPartial returns a partial snapshot when a source fails. When false, a
	// single source failure is fatal. Defaults to true (fail open with explicit
	// limitations rather than losing every other source's records).
	AllowPartial bool
	// DisallowPartial forces fail-closed behavior. It exists because the zero
	// value of AllowPartial is the desired default (true); set this to require a
	// complete snapshot.
	DisallowPartial bool
	// Concurrency bounds simultaneous source Collect calls. Defaults to
	// min(8, len(sources)).
	Concurrency int
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

	snap := &FleetSnapshot{
		SchemaVersion:         SchemaVersion,
		Services:              map[ServiceKey]*ServiceRecord{},
		Revisions:             map[RevisionKey]*ContractRevision{},
		Targets:               map[TargetKey]*TargetRecord{},
		GeneratedAt:           generatedAt,
		reverseDeps:           map[string][]string{},
		forwardDeps:           map[string][]string{},
		forwardDepsByRevision: map[RevisionKey][]string{},
	}

	// Zero configured sources is NOT the same as "every source was available and
	// returned nothing". Make the distinction explicit.
	if len(sources) == 0 {
		snap.Limitations = append(snap.Limitations, Limitation{
			Code: LimitationNoSourcesConfigured, Message: "no sources were configured for this snapshot",
		})
	}

	seenSourceIDs := map[string]bool{}
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
		ingestCollection(snap, src, r.col, generatedAt, opts.FreshnessWindow)
	}

	linkTargets(snap)
	aggregateServices(snap)
	buildRelationships(snap)
	snap.Completeness = classifyCompleteness(snap)
	snap.Limitations = append(snap.Limitations, degradedLimitations(snap)...)
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
func ingestCollection(snap *FleetSnapshot, src Source, col *Collection, now time.Time, window time.Duration) {
	if col == nil {
		col = &Collection{}
	}
	revCount, targetCount := 0, 0
	for _, raw := range col.Revisions {
		rev := revisionFrom(raw, src.ID(), now)
		if rev == nil {
			continue
		}
		if existing := snap.Revisions[rev.Key]; existing != nil {
			// Same immutable revision from another source: merge complementary
			// projections and provenance rather than dropping either.
			snap.Limitations = append(snap.Limitations, mergeRevision(existing, rev)...)
		} else {
			snap.Revisions[rev.Key] = rev
		}
		revCount++
	}
	for _, raw := range col.Targets {
		tgt := targetFrom(raw, src.ID(), now, window)
		if existing := snap.Targets[tgt.Key]; existing != nil {
			// Same target contributed by another source (e.g. platform inventory
			// + operator evaluation): merge by field ownership and freshness.
			snap.Limitations = append(snap.Limitations, mergeTarget(existing, tgt)...)
		} else {
			snap.Targets[tgt.Key] = tgt
		}
		targetCount++
	}
	// A source that reported record-level problems is partial: keep its usable
	// records AND surface the problems.
	snap.Limitations = append(snap.Limitations, col.Limitations...)
	snap.Sources = append(snap.Sources, sourceStateFor(src, col, now, revCount, targetCount))
}

// mergeRevision folds a later contribution of the same immutable revision into
// the existing record: it unions provenance and fills empty complementary
// projections (lock, validation, tools, skills, docs, readiness). It never lets
// source order pick a winner. A digest disagreement (only possible for a
// version-fallback key) is reported as a content conflict.
func mergeRevision(existing, add *ContractRevision) []Limitation {
	existing.Sources = appendUnique(existing.Sources, add.Source)
	if existing.Lock == nil {
		existing.Lock = add.Lock
	}
	if existing.Readiness == nil {
		existing.Readiness = add.Readiness
	}
	if len(existing.Validation) == 0 && len(add.Validation) > 0 {
		existing.Validation = add.Validation
		existing.Valid = add.Valid
		existing.validated = existing.validated || add.validated
	}
	if len(existing.Tools) == 0 {
		existing.Tools = add.Tools
	}
	if len(existing.Skills) == 0 {
		existing.Skills = add.Skills
	}
	if len(existing.Docs) == 0 {
		existing.Docs = add.Docs
	}
	if existing.ResolvedRef == "" {
		existing.ResolvedRef = add.ResolvedRef
	}
	if existing.Digest != "" && add.Digest != "" && existing.Digest != add.Digest {
		return []Limitation{{
			Code: LimitationRevisionConflict, Source: add.Source,
			Message: "sources disagree on the content of revision " + string(existing.Key),
		}}
	}
	return nil
}

// mergeTarget folds a later contribution of the same target into the existing
// record. The fresher observation (by evidence time) owns evaluation fields;
// labels union with a conflict report on disagreement; a contradictory resolved
// reference is reported. Provenance is always retained.
func mergeTarget(existing, add *TargetRecord) []Limitation {
	existing.Sources = appendUnique(existing.Sources, add.Source)
	var lims []Limitation
	if ref, conflict := mergeRef(existing.ResolvedRef, add.ResolvedRef); conflict {
		lims = append(lims, Limitation{Code: LimitationTargetRefConflict, Source: add.Source,
			Message: "sources disagree on the resolved reference of target " + string(existing.Key)})
	} else {
		existing.ResolvedRef = ref
	}
	lims = append(lims, mergeTargetLabels(existing, add)...)
	if targetFresher(add, existing) {
		applyEvaluation(existing, add)
	}
	existing.Stale = existing.Stale && add.Stale
	existing.Limitations = append(existing.Limitations, add.Limitations...)
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
		if cur, ok := existing.Labels[k]; ok && cur != v {
			lims = append(lims, Limitation{Code: LimitationTargetFieldConflict, Source: add.Source,
				Message: "sources disagree on label " + k + " of target " + string(existing.Key)})
			continue
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

// applyEvaluation copies the evaluation-owned fields from the fresher target.
func applyEvaluation(dst, src *TargetRecord) {
	dst.Compliance = src.Compliance
	dst.Findings = src.Findings
	dst.Coverage = src.Coverage
	dst.Readiness = src.Readiness
	dst.ObservedRuntime = src.ObservedRuntime
	dst.EvidenceAt = src.EvidenceAt
	dst.ReconciledAt = src.ReconciledAt
	dst.Digest = src.Digest
}

// sourceStateFor derives (or honors a source-supplied) state for a collection.
func sourceStateFor(src Source, col *Collection, now time.Time, revCount, targetCount int) SourceState {
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
		return st
	}
	t := now
	status := SourceAvailable
	if len(col.Limitations) > 0 {
		status = SourcePartial
	}
	return SourceState{
		ID: src.ID(), Kind: src.Kind(), Status: status,
		LastSuccessfulSync: &t, ObservedAt: &t,
		RevisionCount: revCount, TargetCount: targetCount,
	}
}

// unavailableState builds a sanitized unavailable state for a failed source.
func unavailableState(src Source, err error) SourceState {
	return SourceState{
		ID: src.ID(), Kind: src.Kind(), Status: SourceUnavailable,
		Error: sanitizeError(err),
	}
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
// for a record with no parseable contract.
func revisionFrom(raw RawRevision, source string, now time.Time) *ContractRevision {
	if raw.Bundle == nil || raw.Bundle.Contract == nil {
		return nil
	}
	b := raw.Bundle
	c := b.Contract
	rev := &ContractRevision{
		Key:          NewRevisionKey(c.Service.Name, raw.Digest, raw.ResolvedRef, c.Service.Version),
		Service:      c.Service.Name,
		Domain:       raw.Domain,
		ServiceKey:   NewServiceKeyDomain(raw.Domain, c.Service.Name),
		PactoVersion: c.PactoVersion,
		Version:      c.Service.Version,
		RequestedRef: raw.RequestedRef,
		ResolvedRef:  raw.ResolvedRef,
		Digest:       raw.Digest,
		// The declared contract is deep-copied so a source mutating its own
		// bundle after Build can never mutate the snapshot.
		Contract:  cloneContract(c),
		Source:    source,
		Sources:   []string{source},
		FetchedAt: copyTime(raw.FetchedAt),
		bundle:    b,
	}
	// Owner references the cloned contract so it never aliases source memory.
	rev.Owner = rev.Contract.Service.Owner
	if b.RawYAML != nil {
		res := validation.Validate(c, b.RawYAML, b.FS)
		rev.Valid = res.IsValid()
		rev.Validation = res.Findings()
		rev.validated = true
	}
	rev.Readiness = readiness.Evaluate(c.Readiness, now)
	rev.Tools = toolsFrom(c, b.FS)
	if b.FS != nil {
		// skills.List calls fs.ReadDir, which panics on a nil FS interface; a
		// runtime-only or rawless bundle can legitimately carry no FS.
		if names, _ := skills.List(b.FS); len(names) > 0 {
			rev.Skills = names
		}
	}
	rev.Docs = docsFrom(b.FS)
	rev.Lock = cloneLock(lockFrom(raw))
	return rev
}

// toolsFrom derives bounded tool summaries from a contract's OpenAPI interfaces,
// mirroring the MCP/dashboard naming (interface prefix when >1 openapi iface).
func toolsFrom(c *contract.Contract, fsys fs.FS) []ToolSummary {
	if fsys == nil {
		return nil
	}
	openapiIfaces := 0
	for _, iface := range c.Interfaces {
		if iface.Type == contract.InterfaceTypeOpenAPI && iface.Ref != "" {
			openapiIfaces++
		}
	}
	var out []ToolSummary
	for _, iface := range c.Interfaces {
		if iface.Type != contract.InterfaceTypeOpenAPI || iface.Ref == "" {
			continue
		}
		doc, err := openapi.ReadDoc(fsys, iface.Ref)
		if err != nil {
			continue
		}
		prefix := ""
		if openapiIfaces > 1 {
			prefix = iface.Name + "_"
		}
		for _, tool := range capability.BuildTools(doc, true) {
			if len(out) >= maxToolsPerRevision {
				return out
			}
			summary := tool.Summary
			if summary == "" {
				summary = tool.Description
			}
			out = append(out, ToolSummary{
				Name: prefix + tool.Name, Method: tool.Method, Path: tool.Path,
				Summary: summary, Mutating: tool.Mutating,
			})
		}
	}
	return out
}

// docsFrom collects bounded doc references (path + humanized title) without
// reading any body — bodies are fetched lazily via the bundle when needed.
func docsFrom(fsys fs.FS) []DocRef {
	if fsys == nil {
		return nil
	}
	var out []DocRef
	_ = fs.WalkDir(fsys, "docs", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // docs/ absent or an unreadable subtree: skip silently
		}
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		if len(out) >= maxDocRefs {
			return fs.SkipAll
		}
		out = append(out, DocRef{Path: p, Title: humanizeTitle(p)})
		return nil
	})
	return out
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
	if raw.Bundle == nil || raw.Bundle.FS == nil {
		return nil
	}
	data, err := fs.ReadFile(raw.Bundle.FS, lock.FileName)
	if err != nil {
		return nil
	}
	l, err := lock.Parse(data)
	if err != nil {
		return nil
	}
	return l
}

// targetFrom projects a raw target into a TargetRecord, classifying evidence
// freshness and preserving source-supplied limitations plus computed ones.
func targetFrom(raw RawTarget, source string, now time.Time, window time.Duration) *TargetRecord {
	compliance := raw.Compliance
	if compliance == "" {
		compliance = StatusUnknown
	}
	// Every mutable field is deep-copied so a source mutating its own records
	// after Build cannot mutate the snapshot.
	t := &TargetRecord{
		Key:             NewTargetKey(raw.Scope, raw.Kind, raw.Name),
		Scope:           raw.Scope,
		Kind:            raw.Kind,
		Name:            raw.Name,
		Labels:          cloneStringMap(raw.Labels),
		Service:         raw.Service,
		Domain:          raw.Domain,
		ServiceKey:      NewServiceKeyDomain(raw.Domain, raw.Service),
		RequestedRef:    raw.RequestedRef,
		ResolvedRef:     raw.ResolvedRef,
		Digest:          raw.Digest,
		Compliance:      compliance,
		Findings:        append([]finding.Finding(nil), raw.Findings...),
		Coverage:        cloneCoverage(raw.Coverage),
		Readiness:       cloneReadiness(raw.Readiness),
		ObservedRuntime: cloneAnyMap(raw.ObservedRuntime),
		EvidenceAt:      copyTime(raw.EvidenceAt),
		ReconciledAt:    copyTime(raw.ReconciledAt),
		Source:          source,
		Sources:         []string{source},
		Limitations:     append([]Limitation(nil), raw.Limitations...),
	}
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
	return t
}

// linkTargets associates each target with a revision by digest, then resolved
// ref, then service+version.
func linkTargets(snap *FleetSnapshot) {
	for _, t := range snap.Targets {
		t.ContractRevision = matchRevision(snap, t)
	}
}

func matchRevision(snap *FleetSnapshot, t *TargetRecord) RevisionKey {
	// Prefer immutable digest, then resolved ref, then service+version.
	for _, rev := range snap.Revisions {
		if rev.ServiceKey != t.ServiceKey {
			continue
		}
		if t.Digest != "" && rev.Digest == t.Digest {
			return rev.Key
		}
	}
	for _, rev := range snap.Revisions {
		if rev.ServiceKey == t.ServiceKey && t.ResolvedRef != "" && rev.ResolvedRef == t.ResolvedRef {
			return rev.Key
		}
	}
	// Fallback: same service and the target's resolved ref pins the revision's
	// version (e.g. "…/orders-service:2.0.0" links the 2.0.0 revision). This
	// links an OCI-referenced target to a local revision of the same version.
	for _, rev := range snap.Revisions {
		if rev.ServiceKey == t.ServiceKey && rev.Version != "" &&
			(strings.HasSuffix(t.ResolvedRef, ":"+rev.Version) || strings.HasSuffix(t.ResolvedRef, "@"+rev.Version)) {
			return rev.Key
		}
	}
	return ""
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
	for _, s := range snap.Services {
		s.Status = serviceStatus(snap, s)
		snap.Limitations = append(snap.Limitations, deriveOwner(snap, s)...)
	}
}

// deriveOwner sets a deterministic owner SUMMARY for a service (the owner
// declared by its lowest-keyed revision that declares one) and reports an
// OWNER_CONFLICT limitation when revisions disagree. Per-revision ownership is
// always retained on the revisions themselves; the service field is only a
// documented summary, not the authority.
func deriveOwner(snap *FleetSnapshot, s *ServiceRecord) []Limitation {
	keys := append([]RevisionKey(nil), s.Revisions...)
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	var distinct []contract.Owner
	for _, rk := range keys {
		o := snap.Revisions[rk].Owner
		if o.IsEmpty() {
			continue
		}
		if s.Owner.IsEmpty() {
			s.Owner = o
		}
		if !ownerSeen(distinct, o) {
			distinct = append(distinct, o)
		}
	}
	if len(distinct) > 1 {
		return []Limitation{{
			Code: LimitationOwnerConflict, Source: "fleet",
			Message: "revisions of " + s.Name + " declare different owners; the service owner is a summary (see per-revision ownership)",
		}}
	}
	return nil
}

func ownerSeen(seen []contract.Owner, o contract.Owner) bool {
	for _, e := range seen {
		if e.Equal(o) {
			return true
		}
	}
	return false
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
	if st := targetAggregateStatus(snap, s); st != "" {
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
		switch snap.Targets[tk].Compliance {
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
func buildRelationships(snap *FleetSnapshot) {
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
		snap.Relationships = append(snap.Relationships, revisionDependencyEdges(snap, rk, rev)...)
		snap.Relationships = append(snap.Relationships, revisionReferenceEdges(snap, rk, rev)...)
	}
}

// revisionDependencyEdges builds one dependency edge per declared dependency of a
// revision, updating the revision-accurate and aggregated indexes for resolved
// edges.
func revisionDependencyEdges(snap *FleetSnapshot, rk RevisionKey, rev *ContractRevision) []Relationship {
	var out []Relationship
	for _, dep := range rev.Contract.Dependencies {
		toSvc, ok := resolveDepService(snap, rev.Domain, dep)
		rel := Relationship{
			FromService: rev.Service, FromRevision: rk, To: dep.Name,
			Type: RelationshipDependency, Provenance: ProvenanceDeclared,
			Required: dep.Required, Compatibility: dep.Compatibility,
			RequestedRef: dep.Ref, Resolved: ok, ToService: toSvc,
		}
		if rev.Lock != nil {
			if e, found := rev.Lock.Dependency(dep.Name); found {
				rel.LockedDigest = e.Digest
				rel.LockedVersion = e.Version
			}
		}
		if !ok {
			rel.Reason = "no service in the fleet resolves this dependency ref"
		} else {
			snap.forwardDepsByRevision[rk] = appendUnique(snap.forwardDepsByRevision[rk], toSvc)
			snap.forwardDeps[rev.Service] = appendUnique(snap.forwardDeps[rev.Service], toSvc)
			snap.reverseDeps[toSvc] = appendUnique(snap.reverseDeps[toSvc], rev.Service)
			rel.ResolvedRevision = resolveDepRevision(snap, toSvc, rel.LockedDigest)
		}
		out = append(out, rel)
	}
	return out
}

// revisionReferenceEdges builds config- and policy-reference edges (kept
// distinct) for a revision.
func revisionReferenceEdges(snap *FleetSnapshot, rk RevisionKey, rev *ContractRevision) []Relationship {
	var out []Relationship
	for _, ref := range rev.Contract.ReferenceRefs() {
		toSvc, ok := resolveRefService(snap, rev.Domain, ref)
		typ := RelationshipConfigRef
		if ref.Kind == contract.ReferenceKindPolicy {
			typ = RelationshipPolicyRef
		}
		rel := Relationship{
			FromService: rev.Service, FromRevision: rk, To: ref.Name,
			Type: typ, Provenance: ProvenanceDeclared, RequestedRef: ref.Ref,
			Resolved: ok, ToService: toSvc,
		}
		if !ok {
			rel.Reason = "no service in the fleet resolves this reference"
		}
		out = append(out, rel)
	}
	return out
}

// resolveDepRevision pins the exact revision of the resolved service that a
// dependency points at, when a lock digest identifies it; otherwise "".
func resolveDepRevision(snap *FleetSnapshot, toSvc, lockedDigest string) RevisionKey {
	if lockedDigest == "" {
		return ""
	}
	for _, rev := range snap.Revisions {
		if rev.Service == toSvc && rev.Digest == lockedDigest {
			return rev.Key
		}
	}
	return ""
}

// resolveDepService resolves a dependency to a logical service, preferring the
// depending revision's own domain (a bare dependency ref carries no domain, so it
// resolves within the same domain), tolerating a "-pacto" bundle-name suffix.
func resolveDepService(snap *FleetSnapshot, fromDomain string, dep contract.Dependency) (string, bool) {
	if s := snap.Services[NewServiceKeyDomain(fromDomain, dep.Name)]; s != nil {
		return s.Name, true
	}
	stripped := strings.TrimSuffix(dep.Name, "-pacto")
	if stripped != dep.Name {
		if s := snap.Services[NewServiceKeyDomain(fromDomain, stripped)]; s != nil {
			return s.Name, true
		}
	}
	return "", false
}

// resolveRefService resolves a config/policy reference to a service in the
// referencing revision's domain.
func resolveRefService(snap *FleetSnapshot, fromDomain string, ref contract.ReferenceRef) (string, bool) {
	if s := snap.Services[NewServiceKeyDomain(fromDomain, ref.Name)]; s != nil {
		return s.Name, true
	}
	return "", false
}

// classifyCompleteness derives snapshot completeness from source health.
func classifyCompleteness(snap *FleetSnapshot) Completeness {
	for _, st := range snap.Sources {
		if st.Status != SourceAvailable {
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
	sort.Slice(snap.Relationships, func(i, j int) bool {
		a, b := snap.Relationships[i], snap.Relationships[j]
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
		return a.RequestedRef < b.RequestedRef
	})
	sort.Slice(snap.Sources, func(i, j int) bool { return snap.Sources[i].ID < snap.Sources[j].ID })
	sort.Slice(snap.Limitations, func(i, j int) bool {
		a, b := snap.Limitations[i], snap.Limitations[j]
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.Source < b.Source
	})
	for _, s := range snap.Services {
		sort.Slice(s.Revisions, func(i, j int) bool { return s.Revisions[i] < s.Revisions[j] })
		sort.Slice(s.Targets, func(i, j int) bool { return s.Targets[i] < s.Targets[j] })
		sort.Strings(s.Sources)
	}
	for k := range snap.reverseDeps {
		sort.Strings(snap.reverseDeps[k])
	}
	for k := range snap.forwardDeps {
		sort.Strings(snap.forwardDeps[k])
	}
	for k := range snap.forwardDepsByRevision {
		sort.Strings(snap.forwardDepsByRevision[k])
	}
}

// appendUnique appends v to s if not already present.
func appendUnique(s []string, v string) []string {
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
