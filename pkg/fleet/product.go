package fleet

import (
	"sort"
	"time"
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

// EntityRef is a stable, navigable reference to a fleet entity. It carries the
// canonical key AND the human label AND the canonical dashboard route, so a
// consumer renders and links an entity without re-deriving identity or routes
// (requirement 2 and requirement 13). Human labels are primary; the raw Key is
// secondary copyable metadata.
type EntityRef struct {
	Kind          EntityKind `json:"kind"`
	Key           string     `json:"key"`
	Label         string     `json:"label"`
	Secondary     string     `json:"secondary,omitempty"`
	Status        string     `json:"status,omitempty"`
	Explanation   string     `json:"explanation,omitempty"`
	Domain        string     `json:"domain,omitempty"`
	Scope         string     `json:"scope,omitempty"`
	Route         string     `json:"route"`
	ParentService string     `json:"parentService,omitempty"`
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
	Limitations   []Limitation  `json:"limitations,omitempty"`
}

func (q *Query) productMeta() ProductMeta {
	return ProductMeta{
		SchemaVersion: ProductSchemaVersion,
		SnapshotID:    q.snap.SnapshotID,
		AsOf:          q.snap.GeneratedAt,
		Completeness:  q.snap.Completeness,
		// Deep copies, never snapshot aliases: a consumer may mutate the returned
		// meta without reaching back into the snapshot or a later answer.
		Sources:     cloneSources(q.snap.Sources),
		Limitations: cloneLimitations(q.snap.Limitations),
	}
}

// ProductMeta returns the product-answer completeness envelope for this snapshot,
// for a caller (e.g. the POST-impact handler) that composes its own product DTO.
func (q *Query) ProductMeta() ProductMeta { return q.productMeta() }

// serviceEntityRef builds a navigable reference to a logical service.
func serviceEntityRef(s *ServiceRecord) EntityRef {
	return EntityRef{
		Kind: KindService, Key: string(s.Key), Label: s.Name, Secondary: s.Domain,
		Status: s.Status, Domain: s.Domain, Route: RouteForService(s.Key),
	}
}

// revisionEntityRef builds a navigable reference to a contract revision. The
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
		Status: status, Domain: r.Domain, Route: RouteForRevision(r.Key), ParentService: string(r.ServiceKey),
	}
}

// targetEntityRef builds a navigable reference to an operational target.
func targetEntityRef(t *TargetRecord) EntityRef {
	return EntityRef{
		Kind: KindTarget, Key: string(t.Key), Label: t.DisplayName(), Secondary: t.Scope,
		Status: t.Compliance, Domain: t.Domain, Scope: t.Scope,
		Route: RouteForTarget(t.Key), ParentService: string(t.ServiceKey),
	}
}

// ownerEntityRef builds a navigable reference to an owner.
func ownerEntityRef(owner string) EntityRef {
	return EntityRef{Kind: KindOwner, Key: owner, Label: owner, Route: RouteForOwner(owner)}
}

// sourceEntityRef builds a navigable reference to a source.
func sourceEntityRef(st SourceState) EntityRef {
	return EntityRef{
		Kind: KindSource, Key: st.ID, Label: st.ID, Secondary: st.Kind,
		Status: string(st.Status), Route: RouteForSource(st.ID),
	}
}

// OverviewSummary is the product landing-page count model. Every count has a
// clickable entry point in [Overview.EntryPoints] (requirement 4): a count is
// never a passive number.
type OverviewSummary struct {
	Services                  int `json:"services"`
	ServicesNeedingAttention  int `json:"servicesNeedingAttention"`
	InvalidRevisions          int `json:"invalidRevisions"`
	ExactTargetLinks          int `json:"exactTargetLinks"`
	InferredTargetLinks       int `json:"inferredTargetLinks"`
	AmbiguousTargetLinks      int `json:"ambiguousTargetLinks"`
	UnresolvedTargetLinks     int `json:"unresolvedTargetLinks"`
	NonCompliantTargets       int `json:"nonCompliantTargets"`
	UnknownTargets            int `json:"unknownTargets"`
	StaleTargets              int `json:"staleTargets"`
	UnresolvedRelationships   int `json:"unresolvedRelationships"`
	ObservedOnlyRelationships int `json:"observedOnlyRelationships"`
	DegradedSources           int `json:"degradedSources"`
	StaleSources              int `json:"staleSources"`
	UnavailableSources        int `json:"unavailableSources"`
	RecentEvidence            int `json:"recentEvidence"`
}

// EntryPoint is a suggested navigational starting point. Route is a canonical
// dashboard route (a filtered list, an entity, or a focused graph).
type EntryPoint struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Route       string `json:"route"`
	Count       int    `json:"count,omitempty"`
}

// EvidenceItem links a recently-evidenced target to the moment it was evidenced.
type EvidenceItem struct {
	Target EntityRef  `json:"target"`
	At     *time.Time `json:"at,omitempty"`
}

// Overview is the product-oriented operational summary (requirement 2.1 and
// requirement 4). It answers "what needs attention", "which sources are
// incomplete", "what evidence arrived recently" and "where to go next", with a
// canonical route on every actionable item.
type Overview struct {
	Meta           ProductMeta     `json:"meta"`
	Summary        OverviewSummary `json:"summary"`
	Sources        []SourceState   `json:"sources"`
	Attention      []AttentionItem `json:"attention"`
	RecentEvidence []EvidenceItem  `json:"recentEvidence"`
	EntryPoints    []EntryPoint    `json:"entryPoints"`
}

// Overview computes the product landing summary in a single pass over the
// immutable snapshot.
func (q *Query) Overview() *Overview {
	ov := &Overview{
		Meta: q.productMeta(), Sources: cloneSources(q.snap.Sources),
		Attention: []AttentionItem{}, RecentEvidence: []EvidenceItem{}, EntryPoints: []EntryPoint{},
	}
	sum := &ov.Summary
	sum.Services = len(q.snap.Services)

	for _, r := range q.snap.Revisions {
		if r.validated && !r.Valid {
			sum.InvalidRevisions++
		}
	}
	for _, t := range q.snap.Targets {
		q.tallyTargetLink(sum, t)
		if ei, ok := q.recentEvidence(t); ok {
			sum.RecentEvidence++
			ov.RecentEvidence = append(ov.RecentEvidence, ei)
		}
	}
	q.tallyRelationships(sum)
	q.tallySources(sum)

	sum.ServicesNeedingAttention = q.servicesNeedingAttention()
	ov.Attention = q.Attention(AttentionFilter{Limit: overviewAttentionLimit}).Items
	sortEvidenceDesc(ov.RecentEvidence)
	if len(ov.RecentEvidence) > overviewEvidenceLimit {
		ov.RecentEvidence = ov.RecentEvidence[:overviewEvidenceLimit]
	}
	ov.EntryPoints = entryPoints(sum)
	return ov
}

// tallyTargetLink buckets a target by its revision-link class and compliance.
func (q *Query) tallyTargetLink(sum *OverviewSummary, t *TargetRecord) {
	switch t.RevisionMatch {
	case revisionMatchExact:
		sum.ExactTargetLinks++
	case revisionMatchInferred:
		sum.InferredTargetLinks++
	default:
		if hasLimitation(t.Limitations, LimitationRevisionAmbiguous) {
			sum.AmbiguousTargetLinks++
		} else {
			sum.UnresolvedTargetLinks++
		}
	}
	switch t.Compliance {
	case StatusNonCompliant:
		sum.NonCompliantTargets++
	case StatusUnknown:
		sum.UnknownTargets++
	}
	if t.Stale {
		sum.StaleTargets++
	}
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
		{Label: "Services needing attention", Count: sum.ServicesNeedingAttention, Route: RouteForAttention()},
		{Label: "Non-compliant deployments", Count: sum.NonCompliantTargets, Route: RouteForAttentionFilter(categoryNonCompliant)},
		{Label: "Deployments with stale evidence", Count: sum.StaleTargets, Route: RouteForAttentionFilter(categoryStale)},
		{Label: "Deployments with unknown compliance", Count: sum.UnknownTargets, Route: RouteForAttentionFilter(categoryUnknown)},
		{Label: "Invalid revisions", Count: sum.InvalidRevisions, Route: RouteForAttentionFilter(categoryInvalid)},
		{Label: "Unresolved declared dependencies", Count: sum.UnresolvedRelationships, Route: RouteForAttentionFilter(categoryUnresolved)},
		{Label: "Undeclared runtime dependencies observed", Count: sum.ObservedOnlyRelationships, Route: RouteForServices()},
		{Label: "Incomplete sources", Count: sum.DegradedSources + sum.StaleSources + sum.UnavailableSources, Route: RouteForOverview()},
	}
	out := []EntryPoint{}
	for _, ep := range candidates {
		if ep.Count > 0 {
			out = append(out, ep)
		}
	}
	return out
}

// AttentionItem is one navigable attention row (requirement 2.5). Every item
// links to the exact affected entity and recommends the next step; nothing is a
// non-clickable plain string.
type AttentionItem struct {
	Entity   EntityRef `json:"entity"`
	Service  string    `json:"service,omitempty"`
	Label    string    `json:"label"`
	Severity string    `json:"severity"`
	Code     string    `json:"code"`
	Category string    `json:"category"`
	Summary  string    `json:"summary"`
	Reason   string    `json:"reason"`
	Source   string    `json:"source,omitempty"`
	Route    string    `json:"route"`
	NextStep string    `json:"nextStep,omitempty"`
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

// AttentionFilter constrains an attention query. The zero value returns every
// item (bounded by the default limit).
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
}

// AttentionList is a bounded, deterministically ordered attention answer.
type AttentionList struct {
	Meta  ProductMeta     `json:"meta"`
	Total int             `json:"total"`
	Count int             `json:"count"`
	Items []AttentionItem `json:"items"`
}

// Attention reports navigable attention items across every category, filtered and
// bounded. Items are computed directly from the snapshot (not via the low-level
// Status list) so each carries a clean entity reference and canonical route.
func (q *Query) Attention(f AttentionFilter) *AttentionList {
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
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return &AttentionList{Meta: q.productMeta(), Total: total, Count: len(filtered), Items: filtered}
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
			Summary: "Deployment has confirmed drift", Reason: "the target has confirmed compliance drift against its contract",
			Source: t.Source, Route: ref.Route, NextStep: "Open the deployment findings",
		})
	case StatusUnknown:
		out = append(out, AttentionItem{
			Entity: ref, Service: string(t.ServiceKey), Label: t.DisplayName(),
			Severity: severityWarning, Code: "UNKNOWN", Category: categoryUnknown,
			Summary: "Deployment compliance is unknown", Reason: "compliance is unknown because evidence is insufficient",
			Source: t.Source, Route: ref.Route, NextStep: "Check the deployment evidence source",
		})
	}
	if t.Stale {
		out = append(out, AttentionItem{
			Entity: ref, Service: string(t.ServiceKey), Label: t.DisplayName(),
			Severity: severityWarning, Code: "STALE_EVIDENCE", Category: categoryStale,
			Summary: "Deployment evidence is stale", Reason: "the most recent evidence is older than the freshness window",
			Source: t.Source, Route: ref.Route, NextStep: "Refresh evidence for this deployment",
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
			Source: r.Source, Route: ref.Route, NextStep: "Open the revision and fix the contract",
		})
	}
	if r.Readiness == nil {
		out = append(out, AttentionItem{
			Entity: ref, Service: string(r.ServiceKey), Label: ref.Label,
			Severity: severityInfo, Code: "MISSING_READINESS", Category: categoryReadiness,
			Summary: "Revision has no readiness assessment", Reason: "the revision declares no readiness assessment",
			Source: r.Source, Route: ref.Route, NextStep: "Add a readiness assessment",
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
			Summary: "Declared dependency is unresolved",
			Reason:  "the declared dependency " + rel.To + " is not resolved in the fleet",
			Route:   ref.Route, NextStep: "Publish or resolve the dependency",
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
