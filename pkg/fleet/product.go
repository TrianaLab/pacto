package fleet

import (
	"fmt"
	"sort"
	"time"

	"github.com/trianalab/pacto/v3/pkg/finding"
)

// ProductSchemaVersion is the wire version of the product-oriented dashboard API
// model (overview, entities, neighborhood, entity detail, attention). It is
// distinct from [SchemaVersion] (the raw snapshot model) so a product consumer
// detects product-model changes independently of the low-level export.
const ProductSchemaVersion = "pacto.dev/fleet-product/v1"

// Product-answer bounds. Every product answer is bounded so a UI or agent can
// never be handed an unbounded response (requirement 2).
const (
	overviewAttentionLimit = 10
	overviewEvidenceLimit  = 10
	// RecentEvidenceWindow is how far back from a snapshot's as-of time evidence
	// counts as "recent". It is measured against the snapshot's own generated-at
	// time (never a wall clock), so overview counts are pure and reproducible.
	RecentEvidenceWindow  = 24 * time.Hour
	DefaultEntityLimit    = 100
	MaxEntityLimit        = 500
	DefaultAttentionLimit = 200
	MaxAttentionLimit     = 500
)

// EntityKind is the product-facing kind of a navigable entity.
type EntityKind string

const (
	KindService  EntityKind = "service"
	KindRevision EntityKind = "revision"
	KindTarget   EntityKind = "target"
	KindOwner    EntityKind = "owner"
	KindSource   EntityKind = "source"
)

// validEntityKind reports whether k is a known entity kind.
func validEntityKind(k EntityKind) bool {
	switch k {
	case KindService, KindRevision, KindTarget, KindOwner, KindSource:
		return true
	default:
		return false
	}
}

// EntityRef is a stable, route-neutral reference to a fleet entity. It carries
// the canonical key AND the human label AND enough classification (kind, domain,
// scope, parent service) for a consumer to render and identify an entity, but NO
// dashboard route or href: navigation is a transport concern, so the dashboard
// product transport adds a canonical href from the canonical key (ADR-2). Human
// labels are primary; the raw Key is secondary copyable metadata.
type EntityRef struct {
	Kind      EntityKind `json:"kind" enum:"service,revision,target,owner,source"`
	Key       string     `json:"key"`
	Label     string     `json:"label"`
	Secondary string     `json:"secondary,omitempty"`
	// Status is polymorphic by kind (a compliance status for service/revision/
	// target, a source-health value for source), so it carries no single enum.
	Status        string `json:"status,omitempty"`
	Explanation   string `json:"explanation,omitempty"`
	Domain        string `json:"domain,omitempty"`
	Scope         string `json:"scope,omitempty"`
	ParentService string `json:"parentService,omitempty"`
	// Version is the declared contract version a REVISION reference carries, set only for
	// revision refs. It is surfaced explicitly (not folded into the display label) so a
	// consumer can match a requested version to a canonical RevisionKey without parsing a
	// label -- e.g. the legacy version-bookmark migration (reopen section 8).
	Version string `json:"version,omitempty"`
}

// ProductMeta is the completeness envelope on every product answer: the product
// schema version plus the snapshot's identity, as-of time, completeness, source
// health and limitations. A consumer never renders "all clear" without consulting
// Completeness and Sources (requirement 10).
type ProductMeta struct {
	SchemaVersion string        `json:"schemaVersion"`
	SnapshotID    string        `json:"snapshotId"`
	AsOf          time.Time     `json:"asOf"`
	Completeness  Completeness  `json:"completeness"`
	Sources       []SourceState `json:"sources,omitempty"`
	// SourcesTruncated reports that the snapshot has more sources than the meta
	// carries (the meta keeps the least-healthy up to MaxMetaSources).
	SourcesTruncated bool         `json:"sourcesTruncated,omitempty"`
	Limitations      []Limitation `json:"limitations,omitempty"`
	// LimitationsTruncated reports that the snapshot has more limitations than the
	// meta carries (capped at MaxMetaLimitations).
	LimitationsTruncated bool `json:"limitationsTruncated,omitempty"`
}

func (q *Query) productMeta() ProductMeta {
	// Deep copies, then hard caps: a consumer may mutate the returned meta without
	// reaching back into the snapshot or a later answer, and the meta never grows
	// unbounded with the fleet's source or limitation count.
	srcs, srcTrunc := boundSources(cloneSources(q.snap.Sources))
	lims, limTrunc := boundLimitations(cloneLimitations(q.snap.Limitations))
	return ProductMeta{
		SchemaVersion:        ProductSchemaVersion,
		SnapshotID:           q.snap.SnapshotID,
		AsOf:                 q.snap.GeneratedAt,
		Completeness:         q.snap.Completeness,
		Sources:              srcs,
		SourcesTruncated:     srcTrunc,
		Limitations:          lims,
		LimitationsTruncated: limTrunc,
	}
}

// ProductMeta returns the product-answer completeness envelope for this snapshot,
// for a caller (e.g. the POST-impact handler) that composes its own product DTO.
func (q *Query) ProductMeta() ProductMeta { return q.productMeta() }

// serviceEntityRef builds a route-neutral reference to a logical service.
func serviceEntityRef(s *ServiceRecord) EntityRef {
	return EntityRef{
		Kind: KindService, Key: string(s.Key), Label: s.Name, Secondary: s.Domain,
		Status: s.Status, Domain: s.Domain,
	}
}

// revisionEntityRef builds a route-neutral reference to a contract revision. The
// label prefers the declared version; the digest is the copyable secondary. An
// invalid revision carries the Invalid status so the UI never presents it as
// healthy.
func revisionEntityRef(r *ContractRevision) EntityRef {
	label := r.Service
	if r.Version != "" {
		label = r.Service + " " + r.Version
	}
	status := ""
	if r.validated && !r.Valid {
		status = StatusInvalid
	}
	return EntityRef{
		Kind: KindRevision, Key: string(r.Key), Label: label, Secondary: r.Digest,
		Status: status, Domain: r.Domain, ParentService: string(r.ServiceKey), Version: r.Version,
	}
}

// targetEntityRef builds a route-neutral reference to an operational target.
func targetEntityRef(t *TargetRecord) EntityRef {
	return EntityRef{
		Kind: KindTarget, Key: string(t.Key), Label: t.DisplayName(), Secondary: t.Scope,
		Status: t.Compliance, Domain: t.Domain, Scope: t.Scope,
		ParentService: string(t.ServiceKey),
	}
}

// ownerEntityRef builds a route-neutral reference to an owner.
func ownerEntityRef(owner string) EntityRef {
	return EntityRef{Kind: KindOwner, Key: owner, Label: owner}
}

// sourceEntityRef builds a route-neutral reference to a source.
func sourceEntityRef(st SourceState) EntityRef {
	return EntityRef{
		Kind: KindSource, Key: st.ID, Label: st.ID, Secondary: st.Kind,
		Status: string(st.Status),
	}
}

// OverviewSummary is the product landing-page count model. Every count has a
// clickable entry point in [Overview.EntryPoints] (requirement 4): a count is
// never a passive number.
type OverviewSummary struct {
	Services                 int `json:"services"`
	ServicesNeedingAttention int `json:"servicesNeedingAttention"`
	// Revisions and Targets are the population denominators. Without them a
	// consumer can only show "3 non-compliant" and never "3 of 40": a distribution
	// needs its whole, and deriving the whole client-side from a truncated list
	// would be a fleet-wide claim made from a preview.
	Revisions                 int `json:"revisions"`
	Targets                   int `json:"targets"`
	InvalidRevisions          int `json:"invalidRevisions"`
	ExactTargetLinks          int `json:"exactTargetLinks"`
	InferredTargetLinks       int `json:"inferredTargetLinks"`
	AmbiguousTargetLinks      int `json:"ambiguousTargetLinks"`
	UnresolvedTargetLinks     int `json:"unresolvedTargetLinks"`
	CompliantTargets          int `json:"compliantTargets"`
	NonCompliantTargets       int `json:"nonCompliantTargets"`
	UnknownTargets            int `json:"unknownTargets"`
	InvalidTargets            int `json:"invalidTargets"`
	OtherComplianceTargets    int `json:"otherComplianceTargets"`
	StaleTargets              int `json:"staleTargets"`
	UnresolvedRelationships   int `json:"unresolvedRelationships"`
	ObservedOnlyRelationships int `json:"observedOnlyRelationships"`
	DegradedSources           int `json:"degradedSources"`
	StaleSources              int `json:"staleSources"`
	UnavailableSources        int `json:"unavailableSources"`
	RecentEvidence            int `json:"recentEvidence"`
}

// EntryPointView is the route-neutral destination class of an entry point. The
// dashboard transport maps (View, Category) to a canonical href; the fleet layer
// never emits a route string (ADR-2).
type EntryPointView string

const (
	// EntryPointAttention: the attention list, optionally filtered by Category.
	EntryPointAttention EntryPointView = "attention"
	// EntryPointServices: the services list.
	EntryPointServices EntryPointView = "services"
	// EntryPointOverview: the overview page.
	EntryPointOverview EntryPointView = "overview"
)

// EntryPoint is a suggested navigational starting point described route-neutrally
// by a destination View plus an optional Category filter. The transport turns
// that descriptor into a canonical href.
type EntryPoint struct {
	Label       string         `json:"label"`
	Description string         `json:"description,omitempty"`
	View        EntryPointView `json:"view"`
	Category    string         `json:"category,omitempty"`
	Count       int            `json:"count,omitempty"`
	// Severity grades the category the same way the attention items inside it are
	// graded. Without it every entry point rendered in one tone, so the overview
	// presented a confirmed drift and a missing readiness gate as equally urgent and
	// then the attention list directly below it badged them Error and Info. The
	// backend already decides this per item; saying it here keeps ONE decision.
	Severity string `json:"severity,omitempty" enum:"error,warning,info"`
}

// EvidenceItem links a recently-evidenced target to the moment it was evidenced.
type EvidenceItem struct {
	Target EntityRef  `json:"target"`
	At     *time.Time `json:"at,omitempty"`
}

// Overview is the product-oriented operational summary (requirement 2.1 and
// requirement 4). It answers "what needs attention", "which sources are
// incomplete", "what evidence arrived recently" and "where to go next". Source
// health is carried once, bounded, in Meta.Sources (with Meta.SourcesTruncated);
// there is no separate unbounded source list, so no product answer ever copies
// every source without a hard bound. Attention and RecentEvidence are explicit
// bounded previews carrying the TRUE total, count and truncation, so a consumer can
// tell "10 of 10" from "10 of 500" (requirement, item 12); neither is a raw bounded
// array that silently discards its total.
type Overview struct {
	Meta           ProductMeta      `json:"meta"`
	Summary        OverviewSummary  `json:"summary"`
	Attention      AttentionPreview `json:"attention"`
	RecentEvidence EvidencePreview  `json:"recentEvidence"`
	EntryPoints    []EntryPoint     `json:"entryPoints"`
}

// Overview computes the product landing summary in a single pass over the
// immutable snapshot.
func (q *Query) Overview() *Overview {
	ov := &Overview{Meta: q.productMeta(), EntryPoints: []EntryPoint{}}
	sum := &ov.Summary
	sum.Services = len(q.snap.Services)

	var recent []EvidenceItem
	sum.Revisions = len(q.snap.Revisions)
	for _, r := range q.snap.Revisions {
		if r.validated && !r.Valid {
			sum.InvalidRevisions++
		}
	}
	sum.Targets = len(q.snap.Targets)
	var link LinkTally
	var comp ComplianceTally
	for _, t := range q.snap.Targets {
		link.add(t)
		comp.add(t.Compliance)
		if t.Stale {
			sum.StaleTargets++
		}
		if ei, ok := q.recentEvidence(t); ok {
			sum.RecentEvidence++
			recent = append(recent, ei)
		}
	}
	// Copied out of the shared tallies so the fleet distribution, a service
	// distribution and a per-target badge all classify by ONE rule.
	sum.ExactTargetLinks, sum.InferredTargetLinks = link.Exact, link.Inferred
	sum.AmbiguousTargetLinks, sum.UnresolvedTargetLinks = link.Ambiguous, link.Unresolved
	sum.CompliantTargets, sum.NonCompliantTargets = comp.Compliant, comp.NonCompliant
	sum.UnknownTargets, sum.InvalidTargets = comp.Unknown, comp.Invalid
	sum.OtherComplianceTargets = comp.Other
	q.tallyRelationships(sum)
	q.tallySources(sum)

	sum.ServicesNeedingAttention = q.servicesNeedingAttention()
	// A constant, valid filter never errors; ignore it deliberately. The attention
	// preview carries the TRUE matched total (al.Total), not the 10-item page size.
	al, _ := q.Attention(AttentionFilter{Limit: overviewAttentionLimit})
	ov.Attention = attentionPreviewFromList(al)
	// The recent-evidence preview carries the TRUE recent count (== sum.RecentEvidence),
	// not merely the first overviewEvidenceLimit sliced off it.
	sortEvidenceDesc(recent)
	it, total, trunc := boundSlice(recent, overviewEvidenceLimit)
	ov.RecentEvidence = EvidencePreview{Total: total, Count: len(it), Truncated: trunc, Items: it}
	ov.EntryPoints = entryPoints(sum)
	return ov
}

// tallyRelationships counts unresolved declared dependencies and observed-only
// (undeclared runtime) relationships, using the same declared/observed split the
// engine keeps: an observed edge is "observed-only" iff no declared edge exists
// for the same (from, to) service pair.
func (q *Query) tallyRelationships(sum *OverviewSummary) {
	declared := map[[2]ServiceKey]bool{}
	for i := range q.snap.Relationships {
		rel := q.snap.Relationships[i]
		if rel.Type == RelationshipDependency && rel.Provenance == ProvenanceDeclared {
			if !rel.Resolved {
				sum.UnresolvedRelationships++
			}
			if rel.ToService != "" {
				declared[[2]ServiceKey{rel.FromService, rel.ToService}] = true
			}
		}
	}
	for i := range q.snap.Relationships {
		rel := q.snap.Relationships[i]
		if rel.Provenance == ProvenanceObserved && rel.ToService != "" &&
			!declared[[2]ServiceKey{rel.FromService, rel.ToService}] {
			sum.ObservedOnlyRelationships++
		}
	}
}

// tallySources counts degraded, stale and unavailable sources.
func (q *Query) tallySources(sum *OverviewSummary) {
	for _, st := range q.snap.Sources {
		switch st.Status {
		case SourcePartial:
			sum.DegradedSources++
		case SourceStale:
			sum.StaleSources++
		case SourceUnavailable:
			sum.UnavailableSources++
		}
	}
}

// recentEvidence returns the target's evidence item when its evidence is within
// the recent window of the snapshot's as-of time.
func (q *Query) recentEvidence(t *TargetRecord) (EvidenceItem, bool) {
	if t.EvidenceAt == nil {
		return EvidenceItem{}, false
	}
	if t.EvidenceAt.Before(q.snap.GeneratedAt.Add(-RecentEvidenceWindow)) {
		return EvidenceItem{}, false
	}
	return EvidenceItem{Target: targetEntityRef(t), At: copyTime(t.EvidenceAt)}, true
}

// servicesNeedingAttention counts distinct real services that have any attention
// signal: a target that is non-compliant, unknown or stale; an invalid revision;
// or an unresolved declared dependency.
func (q *Query) servicesNeedingAttention() int {
	need := map[ServiceKey]bool{}
	for _, t := range q.snap.Targets {
		if t.Compliance == StatusNonCompliant || t.Compliance == StatusUnknown || t.Stale {
			need[t.ServiceKey] = true
		}
	}
	for _, r := range q.snap.Revisions {
		if r.validated && !r.Valid {
			need[r.ServiceKey] = true
		}
	}
	for i := range q.snap.Relationships {
		rel := q.snap.Relationships[i]
		if rel.Type == RelationshipDependency && rel.Provenance == ProvenanceDeclared && !rel.Resolved {
			need[rel.FromService] = true
		}
	}
	n := 0
	for k := range need {
		if q.snap.Services[k] != nil {
			n++
		}
	}
	return n
}

// entryPoints builds the suggested navigational entry points, one per non-zero
// summary category, each with a canonical route.
func entryPoints(sum *OverviewSummary) []EntryPoint {
	candidates := []EntryPoint{
		{Label: "Services needing attention", Count: sum.ServicesNeedingAttention, View: EntryPointAttention, Severity: severityWarning},
		// "Not compliant", not "Non-compliant": the Overview shows this tile directly
		// above the triage chips and status badges that name the very same state, and a
		// hyphenated spelling beside the two-word one reads as two different states
		// rather than one. Leading noun matches the sibling target labels below.
		// Each Severity below is the SAME grade the attention items in that category
		// carry (see attentionItems), so a tile and the rows it opens agree.
		{Label: "Operational targets not compliant", Count: sum.NonCompliantTargets, View: EntryPointAttention, Category: categoryNonCompliant, Severity: severityError},
		{Label: "Operational targets with stale evidence", Count: sum.StaleTargets, View: EntryPointAttention, Category: categoryStale, Severity: severityWarning},
		{Label: "Operational targets with unknown compliance", Count: sum.UnknownTargets, View: EntryPointAttention, Category: categoryUnknown, Severity: severityWarning},
		{Label: "Invalid revisions", Count: sum.InvalidRevisions, View: EntryPointAttention, Category: categoryInvalid, Severity: severityError},
		{Label: "Unresolved declared dependencies", Count: sum.UnresolvedRelationships, View: EntryPointAttention, Category: categoryUnresolved, Severity: severityWarning},
		{Label: "Undeclared runtime dependencies observed", Count: sum.ObservedOnlyRelationships, View: EntryPointServices, Severity: severityInfo},
		{Label: "Incomplete sources", Count: sum.DegradedSources + sum.StaleSources + sum.UnavailableSources, View: EntryPointOverview, Severity: severityWarning},
	}
	out := []EntryPoint{}
	for _, ep := range candidates {
		if ep.Count > 0 {
			out = append(out, ep)
		}
	}
	return out
}

// AttentionItem is one attention row (requirement 2.5). Every item carries the
// route-neutral reference to the exact affected entity and recommends the next
// step; the transport adds the entity's canonical href.
type AttentionItem struct {
	Entity  EntityRef `json:"entity"`
	Service string    `json:"service,omitempty"`
	Label   string    `json:"label"`
	// Severity is the narrower attention domain (it never carries "unknown", unlike
	// finding severity).
	Severity string `json:"severity" enum:"error,warning,info"`
	Code     string `json:"code"`
	Category string `json:"category" enum:"non-compliant,unknown,stale,invalid,readiness,unresolved"`
	Summary  string `json:"summary"`
	Reason   string `json:"reason"`
	Source   string `json:"source,omitempty"`
	NextStep string `json:"nextStep,omitempty"`
}

// Attention categories (stable, used for filtering and for overview entry points).
const (
	categoryNonCompliant = "non-compliant"
	categoryUnknown      = "unknown"
	categoryStale        = "stale"
	categoryInvalid      = "invalid"
	categoryReadiness    = "readiness"
	categoryUnresolved   = "unresolved"
)

// severity levels.
const (
	severityError   = "error"
	severityWarning = "warning"
	severityInfo    = "info"
)

var severityOrder = map[string]int{severityError: 0, severityWarning: 1, severityInfo: 2}

// AttentionFilter constrains an attention query. The zero value returns the
// first page (bounded by the default limit) of every item. Offset walks pages.
type AttentionFilter struct {
	Category  string
	Kind      string
	Key       string
	Service   string
	Owner     string
	Source    string
	Severity  string
	Status    string
	StaleOnly bool
	Limit     int
	Offset    int
}

// AttentionList is a bounded, deterministically ordered, offset-pageable
// attention answer. Limit and Offset are the effective (defaulted and capped)
// page bounds; Truncated reports that more items matched than this page carries,
// and NextOffset is the offset of the next page (nil on the last page). Total is
// the full matched count across all pages, so every page reconstructs the
// complete sorted answer exactly once.
type AttentionList struct {
	Meta       ProductMeta     `json:"meta"`
	Total      int             `json:"total"`
	Count      int             `json:"count"`
	Limit      int             `json:"limit"`
	Offset     int             `json:"offset"`
	Truncated  bool            `json:"truncated"`
	NextOffset *int            `json:"nextOffset,omitempty"`
	Items      []AttentionItem `json:"items"`
	// Severities and Categories tally EVERY item the filter matched, not Items.
	// They are what a triage chart must be drawn from: Items is one page.
	Severities SeverityTally            `json:"severities"`
	Categories []AttentionCategoryCount `json:"categories"`
}

// validateAttentionFilter rejects a malformed attention filter (a negative limit
// or offset, or an unknown kind/status) rather than silently defaulting it, so a
// bad input is a typed error and never a misleading empty or full result.
func validateAttentionFilter(f AttentionFilter) error {
	if f.Limit < 0 {
		return &InvalidQueryError{Field: "limit", Value: fmt.Sprint(f.Limit), Reason: "must be >= 0"}
	}
	if f.Offset < 0 {
		return &InvalidQueryError{Field: "offset", Value: fmt.Sprint(f.Offset), Reason: "must be >= 0"}
	}
	if f.Kind != "" && !validEntityKind(EntityKind(f.Kind)) {
		return &InvalidQueryError{Field: "kind", Value: f.Kind, Reason: "not a known entity kind"}
	}
	if f.Status != "" && !ValidStatus(f.Status) {
		return &InvalidQueryError{Field: "status", Value: f.Status, Reason: "not a canonical status"}
	}
	return nil
}

// Attention reports attention items across every category, filtered, sorted and
// offset-paged. Items are computed directly from the snapshot (not via the
// low-level Status list) so each carries a clean entity reference. A malformed
// filter is a typed [InvalidQueryError]; the limit is defaulted at zero and
// hard-capped at MaxAttentionLimit, and paging is deterministic so walking every
// page reconstructs the complete sorted answer exactly once.
func (q *Query) Attention(f AttentionFilter) (*AttentionList, error) {
	if err := validateAttentionFilter(f); err != nil {
		return nil, err
	}
	items := q.collectAttention()
	filtered := items[:0:0]
	for _, it := range items {
		if q.attentionMatches(it, f) {
			filtered = append(filtered, it)
		}
	}
	sortAttention(filtered)
	total := len(filtered)
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultAttentionLimit
	}
	if limit > MaxAttentionLimit {
		limit = MaxAttentionLimit
	}
	start := f.Offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	page := append([]AttentionItem{}, filtered[start:end]...)
	truncated := end < total
	var next *int
	if truncated {
		n := end
		next = &n
	}
	return &AttentionList{
		Meta: q.productMeta(), Total: total, Count: len(page),
		Limit: limit, Offset: start, Truncated: truncated, NextOffset: next, Items: page,
		Severities: attentionSeverities(filtered), Categories: attentionCategories(filtered),
	}, nil
}

// attentionSeverities and attentionCategories tally the COMPLETE filtered attention
// population, not the page. A triage chart drawn from one page of ten items would
// present the first page as the shape of the backlog; these two cover everything the
// filter matched, so Total and the buckets always agree.
func attentionSeverities(items []AttentionItem) SeverityTally {
	var t SeverityTally
	for i := range items {
		t.add(finding.Severity(items[i].Severity))
	}
	return t
}

// AttentionCategoryCount is one triage category and how many matched items are in it.
// Categories are emitted in the canonical enum order with their zeros, so a category
// that empties out keeps its place instead of the chart reshuffling under the user.
type AttentionCategoryCount struct {
	Category string `json:"category"`
	Count    int    `json:"count"`
}

// AttentionCategories is the canonical display order of the triage categories,
// worst first. It mirrors the AttentionItem.Category enum.
var AttentionCategories = []string{"non-compliant", "invalid", "unknown", "stale", "unresolved", "readiness"}

func attentionCategories(items []AttentionItem) []AttentionCategoryCount {
	counts := map[string]int{}
	for i := range items {
		counts[items[i].Category]++
	}
	out := make([]AttentionCategoryCount, 0, len(AttentionCategories))
	for _, c := range AttentionCategories {
		out = append(out, AttentionCategoryCount{Category: c, Count: counts[c]})
		delete(counts, c)
	}
	extra := make([]string, 0, len(counts))
	for c := range counts {
		extra = append(extra, c)
	}
	sort.Strings(extra)
	for _, c := range extra {
		out = append(out, AttentionCategoryCount{Category: c, Count: counts[c]})
	}
	return out
}

// collectAttention gathers every attention item from the snapshot.
func (q *Query) collectAttention() []AttentionItem {
	var items []AttentionItem
	for _, t := range q.snap.Targets {
		items = append(items, q.targetAttention(t)...)
	}
	for _, r := range q.snap.Revisions {
		items = append(items, q.revisionAttention(r)...)
	}
	items = append(items, q.unresolvedAttention()...)
	return items
}

func (q *Query) targetAttention(t *TargetRecord) []AttentionItem {
	ref := targetEntityRef(t)
	var out []AttentionItem
	switch t.Compliance {
	case StatusNonCompliant:
		out = append(out, AttentionItem{
			Entity: ref, Service: string(t.ServiceKey), Label: t.DisplayName(),
			Severity: severityError, Code: "NON_COMPLIANT", Category: categoryNonCompliant,
			Summary: "Operational target has confirmed drift", Reason: "the target has confirmed compliance drift against its contract",
			Source: t.Source, NextStep: "Open the findings for this operational target",
		})
	case StatusUnknown:
		out = append(out, AttentionItem{
			Entity: ref, Service: string(t.ServiceKey), Label: t.DisplayName(),
			Severity: severityWarning, Code: "UNKNOWN", Category: categoryUnknown,
			Summary: "Compliance of this operational target is unknown", Reason: "compliance is unknown because evidence is insufficient",
			Source: t.Source, NextStep: "Check the evidence source for this operational target",
		})
	}
	if t.Stale {
		out = append(out, AttentionItem{
			Entity: ref, Service: string(t.ServiceKey), Label: t.DisplayName(),
			Severity: severityWarning, Code: "STALE_EVIDENCE", Category: categoryStale,
			Summary: "Evidence for this operational target is stale", Reason: "the most recent evidence is older than the freshness window",
			Source: t.Source, NextStep: "Refresh evidence for this operational target",
		})
	}
	return out
}

func (q *Query) revisionAttention(r *ContractRevision) []AttentionItem {
	ref := revisionEntityRef(r)
	var out []AttentionItem
	if r.validated && !r.Valid {
		out = append(out, AttentionItem{
			Entity: ref, Service: string(r.ServiceKey), Label: ref.Label,
			Severity: severityError, Code: "INVALID_CONTRACT", Category: categoryInvalid,
			Summary: "Revision contract is invalid", Reason: "the contract is structurally invalid",
			Source: r.Source, NextStep: "Open the revision and fix the contract",
		})
	}
	if r.Readiness == nil {
		out = append(out, AttentionItem{
			Entity: ref, Service: string(r.ServiceKey), Label: ref.Label,
			Severity: severityInfo, Code: "MISSING_READINESS", Category: categoryReadiness,
			Summary: "Revision has no readiness assessment", Reason: "the revision declares no readiness assessment",
			Source: r.Source, NextStep: "Add a readiness assessment",
		})
	}
	return out
}

// unresolvedAttention reports each unresolved declared dependency against the
// service that declares it — a navigable service entity, never a dead pseudo-edge.
func (q *Query) unresolvedAttention() []AttentionItem {
	var out []AttentionItem
	for i := range q.snap.Relationships {
		rel := q.snap.Relationships[i]
		if rel.Type != RelationshipDependency || rel.Provenance != ProvenanceDeclared || rel.Resolved {
			continue
		}
		// A declared dependency's FromService always exists — it is the service
		// whose revision declares the dependency.
		svc := q.snap.Services[rel.FromService]
		ref := serviceEntityRef(svc)
		out = append(out, AttentionItem{
			Entity: ref, Service: string(rel.FromService), Label: svc.Name + " depends on " + rel.To,
			Severity: severityWarning, Code: "UNRESOLVED_DEPENDENCY", Category: categoryUnresolved,
			Summary:  "Declared dependency is unresolved",
			Reason:   "the declared dependency " + rel.To + " is not resolved in the fleet",
			NextStep: "Publish or resolve the dependency",
		})
	}
	return out
}

// attentionMatches applies the filter's guards to one item. An empty filter field
// matches anything (via optEq), so only set fields constrain the result.
func (q *Query) attentionMatches(it AttentionItem, f AttentionFilter) bool {
	fields := [][2]string{
		{f.Category, it.Category},
		{f.Kind, string(it.Entity.Kind)},
		{f.Key, it.Entity.Key},
		{f.Service, it.Service},
		{f.Source, it.Source},
		{f.Severity, it.Severity},
		{f.Status, it.Entity.Status},
	}
	for _, p := range fields {
		if !optEq(p[0], p[1]) {
			return false
		}
	}
	if f.StaleOnly && it.Code != "STALE_EVIDENCE" {
		return false
	}
	return f.Owner == "" || q.serviceOwnedBy(it.Service, f.Owner)
}

// optEq reports whether an optional filter value (empty means unset) matches have.
func optEq(want, have string) bool { return want == "" || want == have }

// serviceOwnedBy reports whether the service identified by key is owned by owner.
func (q *Query) serviceOwnedBy(key, owner string) bool {
	s := q.snap.Services[ServiceKey(key)]
	return s != nil && s.Owner.MatchesFilter(owner)
}

// sortAttention orders items by severity, then category, then label, so the most
// severe items lead and the order is deterministic.
func sortAttention(items []AttentionItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if si, sj := severityOrder[items[i].Severity], severityOrder[items[j].Severity]; si != sj {
			return si < sj
		}
		if items[i].Category != items[j].Category {
			return items[i].Category < items[j].Category
		}
		return items[i].Label < items[j].Label
	})
}

// hasLimitation reports whether any limitation carries the given code.
func hasLimitation(ls []Limitation, code string) bool {
	for _, l := range ls {
		if l.Code == code {
			return true
		}
	}
	return false
}

// sortEvidenceDesc orders evidence items newest-first, breaking ties by target
// key so the order is deterministic.
func sortEvidenceDesc(items []EvidenceItem) {
	sort.SliceStable(items, func(i, j int) bool {
		ti, tj := items[i].At, items[j].At
		if ti != nil && tj != nil && !ti.Equal(*tj) {
			return ti.After(*tj)
		}
		return items[i].Target.Key < items[j].Target.Key
	})
}
