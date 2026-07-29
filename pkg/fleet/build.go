package fleet

import (
	"context"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"golang.org/x/sync/errgroup"

	"github.com/trianalab/pacto/v3/pkg/capability"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/graph"
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
		Services:    map[ServiceKey]*ServiceRecord{},
		Revisions:   map[RevisionKey]*ContractRevision{},
		Targets:     map[TargetKey]*TargetRecord{},
		GeneratedAt: generatedAt,
		reverseDeps: map[string][]string{},
		forwardDeps: map[string][]string{},
	}

	// Sources are processed in declared order so revision dedup ("first key
	// wins") is deterministic regardless of goroutine completion order.
	for i := range results {
		r := &results[i]
		src := sources[i]
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
	return snap, nil
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
		if _, exists := snap.Revisions[rev.Key]; exists {
			continue // first source wins for a given immutable revision
		}
		snap.Revisions[rev.Key] = rev
		revCount++
	}
	for _, raw := range col.Targets {
		tgt := targetFrom(raw, src.ID(), now, window)
		if _, exists := snap.Targets[tgt.Key]; exists {
			continue
		}
		snap.Targets[tgt.Key] = tgt
		targetCount++
	}
	snap.Sources = append(snap.Sources, sourceStateFor(src, col, now, revCount, targetCount))
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
	return SourceState{
		ID: src.ID(), Kind: src.Kind(), Status: SourceAvailable,
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
		ServiceKey:   NewServiceKey(c.Service.Name),
		PactoVersion: c.PactoVersion,
		Version:      c.Service.Version,
		RequestedRef: raw.RequestedRef,
		ResolvedRef:  raw.ResolvedRef,
		Digest:       raw.Digest,
		Owner:        c.Service.Owner,
		Contract:     c,
		Source:       source,
		FetchedAt:    raw.FetchedAt,
		bundle:       b,
	}
	if b.RawYAML != nil {
		res := validation.Validate(c, b.RawYAML, b.FS)
		rev.Valid = res.IsValid()
		rev.Validation = res.Findings()
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
	rev.Lock = lockFrom(raw)
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
	t := &TargetRecord{
		Key:             NewTargetKey(raw.Scope, raw.Kind, raw.Name),
		Scope:           raw.Scope,
		Kind:            raw.Kind,
		Name:            raw.Name,
		Labels:          raw.Labels,
		Service:         raw.Service,
		ServiceKey:      NewServiceKey(raw.Service),
		RequestedRef:    raw.RequestedRef,
		ResolvedRef:     raw.ResolvedRef,
		Digest:          raw.Digest,
		Compliance:      compliance,
		Findings:        raw.Findings,
		Coverage:        raw.Coverage,
		Readiness:       raw.Readiness,
		ObservedRuntime: raw.ObservedRuntime,
		EvidenceAt:      raw.EvidenceAt,
		ReconciledAt:    raw.ReconciledAt,
		Source:          source,
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
		if rev.Service != t.Service {
			continue
		}
		if t.Digest != "" && rev.Digest == t.Digest {
			return rev.Key
		}
	}
	for _, rev := range snap.Revisions {
		if rev.Service == t.Service && t.ResolvedRef != "" && rev.ResolvedRef == t.ResolvedRef {
			return rev.Key
		}
	}
	// Fallback: same service and the target's resolved ref pins the revision's
	// version (e.g. "…/orders-service:2.0.0" links the 2.0.0 revision). This
	// links an OCI-referenced target to a local revision of the same version.
	for _, rev := range snap.Revisions {
		if rev.Service == t.Service && rev.Version != "" &&
			(strings.HasSuffix(t.ResolvedRef, ":"+rev.Version) || strings.HasSuffix(t.ResolvedRef, "@"+rev.Version)) {
			return rev.Key
		}
	}
	return ""
}

// aggregateServices groups revisions and targets into logical service records.
func aggregateServices(snap *FleetSnapshot) {
	for key, rev := range snap.Revisions {
		s := ensureService(snap, rev.Service)
		s.Revisions = append(s.Revisions, key)
		s.Sources = appendUnique(s.Sources, rev.Source)
		if s.Owner.Team == "" && s.Owner.DRI == "" && (rev.Owner.Team != "" || rev.Owner.DRI != "") {
			s.Owner = rev.Owner
		}
	}
	for key, t := range snap.Targets {
		s := ensureService(snap, t.Service)
		s.Targets = append(s.Targets, key)
		s.Sources = appendUnique(s.Sources, t.Source)
		for k, v := range t.Labels {
			if s.Labels == nil {
				s.Labels = map[string]string{}
			}
			s.Labels[k] = v
		}
	}
	for _, s := range snap.Services {
		s.Status = serviceStatus(snap, s)
	}
}

func ensureService(snap *FleetSnapshot, name string) *ServiceRecord {
	key := NewServiceKey(name)
	s := snap.Services[key]
	if s == nil {
		s = &ServiceRecord{Key: key, Name: name}
		snap.Services[key] = s
	}
	return s
}

// serviceStatus derives a conservative aggregate status. A confirmed
// non-compliant target dominates; an unknown target is surfaced as unknown;
// only all-compliant observed targets yield Compliant. With no targets it falls
// back to the revisions' validity (Invalid if any is invalid, else
// NotEvaluated) and never claims compliance from declaration alone.
func serviceStatus(snap *FleetSnapshot, s *ServiceRecord) string {
	if len(s.Targets) > 0 {
		anyUnknown, allCompliant := false, true
		for _, tk := range s.Targets {
			switch snap.Targets[tk].Compliance {
			case StatusNonCompliant, StatusInvalid:
				return StatusNonCompliant
			case StatusCompliant:
			default:
				anyUnknown = true
				allCompliant = false
			}
		}
		if anyUnknown {
			return StatusUnknown
		}
		if allCompliant {
			return StatusCompliant
		}
	}
	for _, rk := range s.Revisions {
		if snap.Revisions[rk].bundle != nil && snap.Revisions[rk].bundle.RawYAML != nil && !snap.Revisions[rk].Valid {
			return StatusInvalid
		}
	}
	if len(s.Revisions) > 0 {
		return StatusNotEvaluated
	}
	return StatusUnknown
}

// buildRelationships builds declared edges from a representative revision per
// service and populates the forward/reverse dependency indexes.
func buildRelationships(snap *FleetSnapshot) {
	names := make([]string, 0, len(snap.Services))
	for _, s := range snap.Services {
		names = append(names, s.Name)
	}
	sort.Strings(names)

	for _, name := range names {
		rev := representativeRevision(snap, snap.Services[NewServiceKey(name)])
		if rev == nil || rev.Contract == nil {
			continue
		}
		for _, dep := range rev.Contract.Dependencies {
			resolved, ok := resolveDepService(snap, dep)
			rel := Relationship{
				From: name, To: dep.Name, Type: graph.EdgeDependency,
				Provenance: ProvenanceDeclared, Required: dep.Required,
				Compatibility: dep.Compatibility, RequestedRef: dep.Ref,
				Resolved: ok, ResolvedService: resolved,
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
				snap.forwardDeps[name] = appendUnique(snap.forwardDeps[name], resolved)
				snap.reverseDeps[resolved] = appendUnique(snap.reverseDeps[resolved], name)
			}
			snap.Relationships = append(snap.Relationships, rel)
		}
		for _, ref := range rev.Contract.ReferenceRefs() {
			resolved, ok := resolveRefService(snap, ref)
			snap.Relationships = append(snap.Relationships, Relationship{
				From: name, To: ref.Name, Type: graph.EdgeReference,
				Provenance: ProvenanceDeclared, RequestedRef: ref.Ref,
				Resolved: ok, ResolvedService: resolved,
			})
		}
	}
}

// representativeRevision picks the highest-version revision of a service to
// source its declared edges from (deterministic; ties break on key).
func representativeRevision(snap *FleetSnapshot, s *ServiceRecord) *ContractRevision {
	var best *ContractRevision
	for _, rk := range s.Revisions {
		rev := snap.Revisions[rk]
		if best == nil || revisionNewer(rev, best) {
			best = rev
		}
	}
	return best
}

// revisionNewer reports whether a is a newer revision than b: greater semver
// version, else (for unparseable versions) the greater key for determinism.
func revisionNewer(a, b *ContractRevision) bool {
	av, aerr := semver.NewVersion(a.Version)
	bv, berr := semver.NewVersion(b.Version)
	switch {
	case aerr == nil && berr == nil:
		if av.Equal(bv) {
			return a.Key > b.Key
		}
		return av.GreaterThan(bv)
	case aerr == nil:
		return true
	case berr == nil:
		return false
	default:
		return a.Key > b.Key
	}
}

// resolveDepService resolves a dependency to a logical service in the fleet by
// name, tolerating a "-pacto" bundle-name suffix.
func resolveDepService(snap *FleetSnapshot, dep contract.Dependency) (string, bool) {
	if _, ok := snap.Services[NewServiceKey(dep.Name)]; ok {
		return dep.Name, true
	}
	stripped := strings.TrimSuffix(dep.Name, "-pacto")
	if stripped != dep.Name {
		if _, ok := snap.Services[NewServiceKey(stripped)]; ok {
			return stripped, true
		}
	}
	return "", false
}

// resolveRefService resolves a config/policy reference to a service by name.
func resolveRefService(snap *FleetSnapshot, ref contract.ReferenceRef) (string, bool) {
	if _, ok := snap.Services[NewServiceKey(ref.Name)]; ok {
		return ref.Name, true
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
		if a.From != b.From {
			return a.From < b.From
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
