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
		reverseDeps:           map[ServiceKey][]ServiceKey{},
		forwardDeps:           map[ServiceKey][]ServiceKey{},
		forwardDepsByRevision: map[RevisionKey][]ServiceKey{},
		observedReverse:       map[ServiceKey][]ServiceKey{},
		observedForward:       map[ServiceKey][]ServiceKey{},
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
		if r.col != nil {
			for _, e := range r.col.Observed {
				observed = append(observed, observedInput{edge: e, source: src.ID()})
			}
		}
	}

	linkTargets(snap)
	aggregateServices(snap)
	buildRelationships(snap)
	foldObservedRelationships(snap, observed)
	reconcileDeclared(snap)
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
		rev, lims := revisionFrom(raw, src.ID(), now)
		if rev == nil {
			// A nil revision may still carry a limitation stating why it was omitted
			// (e.g. no immutable digest and unhashable content); record it so the
			// omission is honest rather than silent.
			snap.Limitations = append(snap.Limitations, lims...)
			continue
		}
		if existing := snap.Revisions[rev.Key]; existing != nil {
			// Same immutable revision from another source: merge complementary
			// projections and provenance rather than dropping either.
			snap.Limitations = append(snap.Limitations, mergeRevision(existing, rev)...)
		} else {
			snap.Revisions[rev.Key] = rev
			// The identity-unresolved limitation is a property of the revision, so
			// record it only when the revision first enters the snapshot.
			snap.Limitations = append(snap.Limitations, lims...)
		}
		revCount++
	}
	var recordInvalid []Limitation
	for _, raw := range col.Targets {
		tgt, invalid := targetFrom(raw, src.ID(), now, window)
		recordInvalid = append(recordInvalid, invalid...)
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
	// records AND surface the problems. Canonicalization limitations (invalid finite
	// values normalized at ingestion) are recorded on the target AND at the snapshot
	// level, and mark the source partial.
	snap.Limitations = append(snap.Limitations, col.Limitations...)
	snap.Limitations = append(snap.Limitations, recordInvalid...)
	st, stateLims := sourceStateFor(src, col, now, revCount, targetCount, len(recordInvalid) > 0)
	snap.Limitations = append(snap.Limitations, stateLims...)
	snap.Sources = append(snap.Sources, st)
}

// mergeRevision folds a later contribution of the same immutable revision into
// the existing record: it unions provenance and fills empty complementary
// projections (lock, validation, tools, skills, docs, readiness). It never lets
// source order pick a winner. Because keys are content-addressed, a same-key
// collision whose derived content digests disagree means two sources pinned the
// same identity to different contract bodies — reported as a content conflict.
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
	if existing.content != add.content {
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
	if targetFresher(add, existing) {
		applyEvaluation(existing, add)
	}
	existing.Stale = existing.Stale && add.Stale
	existing.Limitations = append(existing.Limitations, add.Limitations...)
	return lims
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
	dst.Source = src.Source
}

// canonicalSourceStatus normalizes a source-declared status to the finite
// source-health vocabulary. A malformed status is normalized to a valid DEGRADED
// state (partial) rather than emitted out-of-schema or silently upgraded to
// available (requirement, item 5).
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
		if recordProblems && status == SourceAvailable {
			status = SourcePartial
		}
		st.Status = status
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
	content := raw.Digest
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
		content = cd
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
		Contract:  cloneContract(c),
		Source:    source,
		Sources:   []string{source},
		FetchedAt: copyTime(raw.FetchedAt),
		bundle:    b,
		content:   content,
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
	return rev, lims
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

// validSeverity reports whether s is a canonical finding severity. A [Source] is
// an extension seam, so a raw finding may carry any string; only these four values
// may reach the finite ProductFinding.severity OpenAPI enum (requirement, item 5).
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
// SOURCE_RECORD_INVALID limitation for each normalized field (requirement, item 5).
// The returned limitations are also recorded on the target so it self-describes.
func targetFrom(raw RawTarget, source string, now time.Time, window time.Duration) (*TargetRecord, []Limitation) {
	compliance, complianceInvalid := canonicalCompliance(raw.Compliance)
	findings, invalidSeverities := canonicalFindings(raw.Findings)
	// Every mutable field is deep-copied so a source mutating its own records
	// after Build cannot mutate the snapshot.
	t := &TargetRecord{
		Key:          NewTargetKey(raw.Scope, raw.Kind, raw.Name),
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

// Revision-link classes (see TargetRecord.RevisionMatch).
const (
	revisionMatchExact     = "exact"     // immutable digest — authoritative
	revisionMatchInferred  = "inferred"  // unique mutable tag/version correlation
	revisionMatchAmbiguous = "ambiguous" // several candidates — no link is made
	// revisionMatchInconsistent: the target's identity is internally inconsistent
	// (a recorded digest that contradicts its digest-pinned ResolvedRef) or malformed.
	// No link is made and a limitation is surfaced; it is never an exact link.
	revisionMatchInconsistent = "inconsistent"
)

// linkTargets associates each target with a revision and records how the link
// was made. An ambiguous mutable match links to nothing and surfaces a
// REVISION_LINK_AMBIGUOUS limitation, so a guess is never presented as fact.
func linkTargets(snap *FleetSnapshot) {
	for _, t := range snap.Targets {
		key, kind := matchRevision(snap, t)
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

// matchRevision links a target to a contract revision and classifies the link
// using the SINGLE exact-content-identity invariant ([ClassifyExactIdentity]) that
// RevisionDetail, TargetDetail and Product Impact also use, so the exact tier and
// the target's identity class can never contradict one another (requirement,
// item 6). An EXACT link is an immutable-content match by the canonical digest the
// classifier derives (from a digest-pinned ResolvedRef, or from a recorded digest
// that does not contradict a ref pinning none). An internally inconsistent
// (recorded-digest-vs-pinned-ref mismatch) or malformed identity is never exact and
// never mutably correlated — it yields no link and a limitation. A match by mutable
// resolved-ref or version suffix is INFERRED, and only when UNIQUE; two or more
// candidates are AMBIGUOUS and yield no link. Revisions are visited in sorted key
// order for a deterministic result.
func matchRevision(snap *FleetSnapshot, t *TargetRecord) (RevisionKey, string) {
	keys := sortedRevisionKeys(snap)
	id := ClassifyExactIdentity(t.ResolvedRef, t.Digest)
	// A contradictory (digest-mismatch) or malformed identity is never exact, and
	// correlating a mutable link off a contradictory/unparseable ref would be
	// dishonest.
	if id.Class == IdentityDigestMismatch || id.Class == IdentityMalformed {
		return "", revisionMatchInconsistent
	}
	// Tier 1: exact content identity, matched by the classifier's canonical digest
	// (never a contradictory recorded digest independently).
	if key, ok := exactRevisionByDigest(snap, keys, t, id); ok {
		return key, revisionMatchExact
	}
	if id.Exact() {
		// A digest-pinned ResolvedRef names exact content; if no revision in the fleet
		// carries it, there is no honest mutable fallback.
		return "", ""
	}
	return mutableRevisionMatch(snap, keys, t)
}

// exactRevisionByDigest finds a revision of the target's service whose content
// digest equals the target's effective content digest: the classifier's canonical
// digest when the ResolvedRef is digest-pinned (already cross-checked against any
// recorded digest), otherwise the recorded digest (which cannot contradict a ref
// that pins nothing). It returns false when there is no effective digest or match.
func exactRevisionByDigest(snap *FleetSnapshot, keys []RevisionKey, t *TargetRecord, id ExactIdentity) (RevisionKey, bool) {
	effective := t.Digest
	if id.Exact() {
		effective = id.Digest.String()
	}
	if effective == "" {
		return "", false
	}
	for _, k := range keys {
		if rev := snap.Revisions[k]; rev.ServiceKey == t.ServiceKey && rev.Digest == effective {
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

func sortedRevisionKeys(snap *FleetSnapshot) []RevisionKey {
	keys := make([]RevisionKey, 0, len(snap.Revisions))
	for k := range snap.Revisions {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
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

// observedInput pairs a raw observed edge with the id of the source that
// witnessed it, so folded observed relationships carry their provenance.
type observedInput struct {
	edge   ObservedEdge
	source string
}

// foldObservedRelationships resolves runtime-observed edges to UNIQUE
// domain-qualified services and folds resolved edges into the snapshot as observed
// dependency relationships, summing counts for duplicate edges. An endpoint that
// matches zero or multiple services is never coerced to a domain — it becomes an
// explicit unresolved limitation so observed traffic can't be misattributed across
// domains (see [FleetSnapshot.ObservedNameResolver]). Resolved edges also extend
// the SEPARATE observed adjacency indexes, keeping the declared graph declared
// while making observed-only (shadow) callers reachable in the operational graph.
func foldObservedRelationships(snap *FleetSnapshot, observed []observedInput) {
	if len(observed) == 0 {
		return
	}
	resolve := snap.ObservedNameResolver()
	seenLim := map[string]bool{}
	type edgeKey struct{ from, to ServiceKey }
	// Per-edge, per-source aggregate: a multi-source edge keeps EVERY source's own
	// count and window rather than crediting the first source with the total.
	type edgeAgg struct {
		total    int
		bySource map[string]*ObservedSourceStat
		sources  []string
	}
	edges := map[edgeKey]*edgeAgg{}
	var order []edgeKey
	for _, oi := range observed {
		fromKey, fromRes := resolve(oi.edge.From)
		toKey, toRes := resolve(oi.edge.To)
		addObservedLimitation(snap, seenLim, oi.edge.From, fromRes, oi.source)
		addObservedLimitation(snap, seenLim, oi.edge.To, toRes, oi.source)
		if fromRes != ObservedResolved || toRes != ObservedResolved {
			continue
		}
		k := edgeKey{fromKey, toKey}
		a := edges[k]
		if a == nil {
			a = &edgeAgg{bySource: map[string]*ObservedSourceStat{}}
			edges[k] = a
			order = append(order, k)
		}
		a.total += oi.edge.Count
		st := a.bySource[oi.source]
		if st == nil {
			st = &ObservedSourceStat{Source: oi.source}
			a.bySource[oi.source] = st
			a.sources = append(a.sources, oi.source)
		}
		st.Count += oi.edge.Count
		mergeWindow(&st.FirstSeen, &st.LastSeen, oi.edge.FirstSeen, oi.edge.LastSeen)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].from != order[j].from {
			return order[i].from < order[j].from
		}
		return order[i].to < order[j].to
	})
	for _, k := range order {
		a := edges[k]
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
		snap.Relationships = append(snap.Relationships, Relationship{
			FromService: k.from, ToService: k.to, Type: RelationshipDependency,
			Provenance: ProvenanceObserved, Resolved: true,
			ObservedCount: a.total, Source: source,
			ObservedSources: stats, FirstSeen: winFirst, LastSeen: winLast,
		})
		snap.observedForward[k.from] = appendUnique(snap.observedForward[k.from], k.to)
		snap.observedReverse[k.to] = appendUnique(snap.observedReverse[k.to], k.from)
	}
}

// reconcileDeclared classifies each declared dependency edge against the
// snapshot's observed edges: matched (an observed edge corroborates it),
// declared-not-observed (observation data exists but did not witness it), or
// insufficient (no observation data at all — so it cannot be reconciled). This is
// the ONLY source of a relationship's reconciliation state; a declared edge is
// never "reconciled" merely because its provider is deployed. Runs after
// foldObservedRelationships so the observed edges are present.
func reconcileDeclared(snap *FleetSnapshot) {
	type edge struct{ from, to ServiceKey }
	observed := map[edge]bool{}
	for _, rel := range snap.Relationships {
		if rel.Type == RelationshipDependency && rel.Provenance == ProvenanceObserved {
			observed[edge{rel.FromService, rel.ToService}] = true
		}
	}
	haveObservation := len(observed) > 0
	for i := range snap.Relationships {
		rel := &snap.Relationships[i]
		if rel.Type != RelationshipDependency || rel.Provenance != ProvenanceDeclared {
			continue
		}
		switch {
		case !haveObservation:
			rel.Reconciliation = ReconciliationInsufficient
		case observed[edge{rel.FromService, rel.ToService}]:
			rel.Reconciliation = ReconciliationMatched
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
// edges.
func revisionDependencyEdges(snap *FleetSnapshot, rk RevisionKey, rev *ContractRevision) []Relationship {
	var out []Relationship
	for _, dep := range rev.Contract.Dependencies {
		toSvc, ok := resolveDepService(snap, rev.Domain, dep)
		rel := Relationship{
			FromService: rev.ServiceKey, FromRevision: rk, To: dep.Name,
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
			snap.forwardDeps[rev.ServiceKey] = appendUnique(snap.forwardDeps[rev.ServiceKey], toSvc)
			snap.reverseDeps[toSvc] = appendUnique(snap.reverseDeps[toSvc], rev.ServiceKey)
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
			FromService: rev.ServiceKey, FromRevision: rk, To: ref.Name,
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
func resolveDepService(snap *FleetSnapshot, fromDomain string, dep contract.Dependency) (ServiceKey, bool) {
	if s := snap.Services[NewServiceKeyDomain(fromDomain, dep.Name)]; s != nil {
		return s.Key, true
	}
	stripped := strings.TrimSuffix(dep.Name, "-pacto")
	if stripped != dep.Name {
		if s := snap.Services[NewServiceKeyDomain(fromDomain, stripped)]; s != nil {
			return s.Key, true
		}
	}
	return "", false
}

// resolveRefService resolves a config/policy reference to a service in the
// referencing revision's domain.
func resolveRefService(snap *FleetSnapshot, fromDomain string, ref contract.ReferenceRef) (ServiceKey, bool) {
	if s := snap.Services[NewServiceKeyDomain(fromDomain, ref.Name)]; s != nil {
		return s.Key, true
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
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		// Message is the tertiary key so limitations with the same code+source but
		// different subjects order deterministically (permutation-invariant).
		return a.Message < b.Message
	})
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
