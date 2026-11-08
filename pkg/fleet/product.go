package fleet

import (
	"cmp"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/finding"
)

// ProductSchemaVersion is the wire version of the product-oriented dashboard API
// model (overview, entities, neighborhood, entity detail, attention). It is
// distinct from [SchemaVersion] (the raw snapshot model) so a product consumer
// detects product-model changes independently of the low-level export.
const ProductSchemaVersion = "pacto.dev/fleet-product/v1"

// Product-answer bounds. Every product answer is bounded so a UI or agent can
// never be handed an unbounded response.
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
	// KindDomain is a business domain (service.domain), keyed by its name.
	KindDomain EntityKind = "domain"
	// KindOffering is a platform offering (service.offering), keyed by its slug.
	KindOffering EntityKind = "offering"
)

// ProductKinds are the kinds the entities listing answers by default:
// services, owners, business domains and platform offerings. Revisions, targets
// and sources are reached through an entity's detail.
func ProductKinds() []string {
	return []string{string(KindService), string(KindOwner), string(KindDomain), string(KindOffering)}
}

// EntityKinds are every entity kind, in the order an entity detail lists them.
func EntityKinds() []string {
	return []string{
		string(KindService), string(KindRevision), string(KindTarget), string(KindOwner),
		string(KindSource), string(KindDomain), string(KindOffering),
	}
}

// validEntityKind reports whether k is a known entity kind.
func validEntityKind(k EntityKind) bool {
	switch k {
	case KindService, KindRevision, KindTarget, KindOwner, KindSource, KindDomain, KindOffering:
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
	Kind      EntityKind `json:"kind" enum:"service,revision,target,owner,source,domain,offering"`
	Key       string     `json:"key"`
	Label     string     `json:"label"`
	Secondary string     `json:"secondary,omitempty"`
	// Status is polymorphic by kind (a compliance status for service/revision/
	// target, a source-health value for source), so it carries no single enum.
	Status      string `json:"status,omitempty"`
	Explanation string `json:"explanation,omitempty"`
	// Domain is the registry path, shown as Registry (see ServiceRecord.Domain).
	Domain string `json:"domain,omitempty"`
	// BusinessDomain is the business domain of the service a service or target
	// reference belongs to, the domain a REVISION reference itself declares
	// (Contract.Service.Domain, which can differ from its service's), and the name
	// of a domain reference. Empty means No domain, unless BusinessDomainUnknown
	// says Pacto cannot tell (service and target references only).
	BusinessDomain        string `json:"businessDomain,omitempty"`
	BusinessDomainUnknown bool   `json:"businessDomainUnknown,omitempty"`
	// Offering is the platform offering a SERVICE reference provides and the
	// name of an offering reference; Summary is a service reference's one-line
	// purpose.
	Offering string `json:"offering,omitempty"`
	Summary  string `json:"summary,omitempty"`
	// OfferingProvenance says where an offering reference's membership comes
	// from: declared when a contract revision of any provider declares it,
	// observed when only the sources observing targets report it. A service
	// reference that carries Offering carries its own.
	OfferingProvenance string `json:"offeringProvenance,omitempty" enum:"declared,observed"`
	// Environment is the environment a TARGET reference runs in (see
	// TargetRecord.Environment), empty when unknown.
	Environment   string `json:"environment,omitempty"`
	Scope         string `json:"scope,omitempty"`
	ParentService string `json:"parentService,omitempty"`
	// Version is the declared contract version a REVISION reference carries, set only for
	// revision refs. It is surfaced explicitly (not folded into the display label) so a
	// consumer can match a requested version to a canonical RevisionKey without parsing a
	// label -- e.g. the legacy version-bookmark migration.
	Version string `json:"version,omitempty"`
	// FetchedAt is when a source read a REVISION reference's contract, set only
	// for revision refs and only by sources that record it (local files and the
	// catalog).
	FetchedAt *time.Time `json:"fetchedAt,omitempty"`
	// Owner is who a SERVICE reference's revisions name, set only on list answers:
	// the owner's label when they agree on one, "N owners claim it" when they do
	// not, and empty when none declares an owner.
	Owner string `json:"owner,omitempty"`
	// Targets counts what a SERVICE reference runs on; Services counts the services
	// an OWNER reference's revisions claim. Both are set only on list answers.
	Targets  int `json:"targets,omitempty"`
	Services int `json:"services,omitempty"`
	// Template marks a SERVICE reference that is a golden path (see
	// ServiceRecord.Template). On list answers a golden path reference also
	// carries Created, the services in this view created from it, and Signature,
	// the signature status of its newest readable revision: checked against the
	// signer its own contract declares, never against a trust config, and
	// not-checked when no source that read it checks signatures.
	Template  bool   `json:"template,omitempty"`
	Created   int    `json:"created,omitempty"`
	Signature string `json:"signature,omitempty" enum:"verified,unverified,unsigned,unknown,not-checked"`
}

// SourceCounts is the fleet's source population tallied by health, over EVERY
// source the snapshot holds rather than the capped [ProductMeta.Sources] beside
// it. It exists because that slice is bounded and, once bounded, is deliberately
// biased towards the least-healthy sources: counting it would answer "how healthy
// is the worst of them" while looking like an answer to "how healthy are they".
//
// Total is the complete population, so a status the product does not recognize
// leaves Total above the sum of the four buckets rather than being folded into one
// of them. A consumer rendering these as a distribution states Total as the
// denominator and shows the shortfall as unclassified.
type SourceCounts struct {
	Total       int `json:"total"`
	Available   int `json:"available"`
	Partial     int `json:"partial"`
	Stale       int `json:"stale"`
	Unavailable int `json:"unavailable"`
}

// sourceCounts tallies the complete source population by health.
func sourceCounts(ss []SourceState) SourceCounts {
	c := SourceCounts{Total: len(ss)}
	for _, s := range ss {
		switch s.Status {
		case SourceAvailable:
			c.Available++
		case SourcePartial:
			c.Partial++
		case SourceStale:
			c.Stale++
		case SourceUnavailable:
			c.Unavailable++
		}
	}
	return c
}

// ProductMeta is the completeness envelope on every product answer: the product
// schema version plus the snapshot's identity, as-of time, completeness, source
// health and limitations. A consumer never renders "all clear" without consulting
// Completeness and Sources.
type ProductMeta struct {
	SchemaVersion string        `json:"schemaVersion"`
	SnapshotID    string        `json:"snapshotId"`
	AsOf          time.Time     `json:"asOf"`
	Completeness  Completeness  `json:"completeness"`
	Sources       []SourceState `json:"sources,omitempty"`
	// SourceCounts tallies the COMPLETE source population by health, so a consumer
	// can state fleet-wide source health without reading it off the bounded,
	// least-healthy-first Sources slice.
	SourceCounts SourceCounts `json:"sourceCounts"`
	// SourcesTruncated reports that the snapshot has more sources than the meta
	// carries (the meta keeps the least-healthy up to MaxMetaSources).
	SourcesTruncated bool         `json:"sourcesTruncated,omitempty"`
	Limitations      []Limitation `json:"limitations,omitempty"`
	// LimitationsTruncated reports that the snapshot has more limitations than the
	// meta carries (capped at MaxMetaLimitations).
	LimitationsTruncated bool `json:"limitationsTruncated,omitempty"`
}

func (q *Query) productMeta() ProductMeta { return q.productMetaWith(nil) }

// productMetaWith is productMeta with the limitations an answer derives at query
// time, such as provisioning conflicts, listed first.
func (q *Query) productMetaWith(derived []Limitation) ProductMeta {
	// Deep copies, then hard caps: a consumer may mutate the returned meta without
	// reaching back into the snapshot or a later answer, and the meta never grows
	// unbounded with the fleet's source or limitation count.
	srcs, srcTrunc := boundSources(cloneSources(q.snap.Sources))
	lims, limTrunc := BoundLimitations(append(derived, cloneLimitations(q.snap.Limitations)...))
	return ProductMeta{
		SchemaVersion:        ProductSchemaVersion,
		SnapshotID:           q.snap.SnapshotID,
		AsOf:                 q.snap.GeneratedAt,
		Completeness:         q.snap.Completeness,
		Sources:              srcs,
		SourceCounts:         sourceCounts(q.snap.Sources),
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
		Status: s.Status, Domain: s.Domain, BusinessDomain: s.BusinessDomain, BusinessDomainUnknown: s.BusinessDomainUnknown,
		Offering: s.Offering, Summary: s.Summary, OfferingProvenance: s.OfferingProvenance, Template: s.Template,
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
		Status: status, Domain: r.Domain, BusinessDomain: declaredBusinessDomain(r), ParentService: string(r.ServiceKey), Version: r.Version,
		FetchedAt: copyTime(r.FetchedAt),
	}
}

// declaredBusinessDomain is the business domain a revision itself declares, empty
// when its contract was not read or the value is not a domain slug.
func declaredBusinessDomain(r *ContractRevision) string {
	if r.Contract == nil || !validBusinessDomain(r.Contract.Service.Domain) {
		return ""
	}
	return r.Contract.Service.Domain
}

// targetEntityRef builds a route-neutral reference to an operational target.
func targetEntityRef(t *TargetRecord) EntityRef {
	return EntityRef{
		Kind: KindTarget, Key: string(t.Key), Label: t.DisplayName(), Secondary: t.Scope,
		Status: t.Compliance, Domain: t.Domain, BusinessDomain: t.BusinessDomain, BusinessDomainUnknown: t.BusinessDomainUnknown,
		Environment: t.Environment, Scope: t.Scope,
		ParentService: string(t.ServiceKey),
	}
}

// ownerEntityRef builds a route-neutral reference to an owner from its canonical
// key. The Key routes and the Label reads: two owners can share a Label, so
// Secondary names the namespace that tells them apart on screen.
func ownerEntityRef(key string) EntityRef {
	ref := EntityRef{Kind: KindOwner, Key: key, Label: key}
	if k, err := contract.ParseOwnerKey(key); err == nil {
		ref.Label, ref.Secondary = k.Value, k.KindLabel()
	}
	return ref
}

// domainEntityRef builds a route-neutral reference to a business domain.
func domainEntityRef(name string) EntityRef {
	return EntityRef{Kind: KindDomain, Key: name, Label: name, BusinessDomain: name}
}

// offeringEntityRef builds a route-neutral reference to a platform offering
// whose membership has the given provenance.
func offeringEntityRef(name, provenance string) EntityRef {
	return EntityRef{Kind: KindOffering, Key: name, Label: name, Offering: name, OfferingProvenance: provenance}
}

// offeringProvenance is ProvenanceDeclared when any provider of the offering
// name declares it and ProvenanceObserved otherwise.
func offeringProvenance(snap *FleetSnapshot, name string) string {
	for _, s := range snap.Services {
		if s.Offering == name && s.OfferingProvenance == ProvenanceDeclared {
			return ProvenanceDeclared
		}
	}
	return ProvenanceObserved
}

// sourceEntityRef builds a route-neutral reference to a source.
func sourceEntityRef(st SourceState) EntityRef {
	return EntityRef{
		Kind: KindSource, Key: st.ID, Label: st.ID, Secondary: st.Kind,
		Status: string(st.Status),
	}
}

// OverviewSummary is the product landing-page count model. Every count has a
// clickable entry point in [Overview.EntryPoints]: a count is
// never a passive number.
type OverviewSummary struct {
	Services                 int `json:"services"`
	ServicesNeedingAttention int `json:"servicesNeedingAttention"`
	// GoldenPaths counts the golden paths, which every other count leaves out:
	// a golden path is a starting point for services, never one of them.
	// InvalidGoldenPaths counts those with an invalid revision.
	GoldenPaths        int `json:"goldenPaths"`
	InvalidGoldenPaths int `json:"invalidGoldenPaths"`
	// Revisions and Targets are the population denominators. Without them a
	// consumer can only show "3 non-compliant" and never "3 of 40": a distribution
	// needs its whole, and deriving the whole client-side from a truncated list
	// would be a fleet-wide claim made from a preview.
	Revisions             int `json:"revisions"`
	Targets               int `json:"targets"`
	InvalidRevisions      int `json:"invalidRevisions"`
	ExactTargetLinks      int `json:"exactTargetLinks"`
	InferredTargetLinks   int `json:"inferredTargetLinks"`
	AmbiguousTargetLinks  int `json:"ambiguousTargetLinks"`
	UnresolvedTargetLinks int `json:"unresolvedTargetLinks"`
	// The target compliance distribution, one flat counter per bucket of the shared
	// [ComplianceTally]. All eight are carried, not a chosen five: the tally's whole
	// point is that its buckets are exhaustive, and a consumer drawing a proportional
	// visual from a lossy copy has to render the shortfall as an unnamed remainder —
	// which is how a Warning or Reference target, correctly named on the targets list,
	// became a grey "Unclassified" slice on the landing page.
	CompliantTargets          int `json:"compliantTargets"`
	NonCompliantTargets       int `json:"nonCompliantTargets"`
	UnknownTargets            int `json:"unknownTargets"`
	WarningTargets            int `json:"warningTargets"`
	InvalidTargets            int `json:"invalidTargets"`
	ReferenceTargets          int `json:"referenceTargets"`
	NotEvaluatedTargets       int `json:"notEvaluatedTargets"`
	OtherComplianceTargets    int `json:"otherComplianceTargets"`
	StaleTargets              int `json:"staleTargets"`
	UnresolvedRelationships   int `json:"unresolvedRelationships"`
	UnsatisfiedRanges         int `json:"unsatisfiedRanges"`
	ObservedOnlyRelationships int `json:"observedOnlyRelationships"`
	UncontractedCallers       int `json:"uncontractedCallers"`
	DegradedSources           int `json:"degradedSources"`
	StaleSources              int `json:"staleSources"`
	UnavailableSources        int `json:"unavailableSources"`
	RecentEvidence            int `json:"recentEvidence"`
	// Ownership partitions the COMPLETE service population and Readiness the
	// COMPLETE contract-revision population. Neither is an operational failure, but
	// each gap is something a person can fix, so both also appear in the attention
	// backlog at info: a service with no declared owner as `unowned`, a revision with
	// no readiness assessment as `readiness`. The tallies here answer the systemic
	// question (is ownership declared at all, is anyone assessing readiness), which
	// no per-entity page can. They are the only two overview facts whose denominator
	// is not the target population.
	Ownership OwnershipTally `json:"ownership"`
	Readiness ReadinessTally `json:"readiness"`
	// Evidence is the fleet's freshness envelope: the third orthogonal question a
	// service page already answers ("how recently did we look"), at fleet scale and
	// over the same complete target population. Evidence.Stale equals StaleTargets by
	// construction — the flat field predates this and the entry points still read it —
	// but a proportion needs the whole partition, and fresh / stale / never-observed is
	// only a partition once "never observed at all" is its own bucket.
	Evidence EvidenceWindow `json:"evidence"`
	// ByScope says where the targets are, by scope (the environment or cluster a
	// target runs in), the scope with the most non-compliant targets first. It is
	// bounded to overviewScopeLimit; Scopes is how many scopes there are in all.
	ByScope []ScopeTally `json:"byScope"`
	Scopes  int          `json:"scopes"`
	// ByDomain breaks every service down by business domain, the domain with the
	// most non-compliant targets first. It ends with the Unknown domain row
	// (DomainUnknown) whenever Pacto cannot tell a service's domain, then the No
	// domain row (Domain empty) whenever a service has none, even when the list is
	// bounded to overviewScopeLimit. Domains counts the named domains only.
	ByDomain []DomainTally `json:"byDomain"`
	Domains  int           `json:"domains"`
}

// DomainTally counts the services of one business domain, the targets they run
// on, how many of those targets are broken, unknown or stale, and how many of the
// services no source knows an owner for. An empty Domain is the No domain row, or
// the Unknown domain row when DomainUnknown is set.
type DomainTally struct {
	Domain        string `json:"domain"`
	DomainUnknown bool   `json:"domainUnknown,omitempty"`
	Services      int    `json:"services"`
	Targets       int    `json:"targets"`
	NonCompliant  int    `json:"nonCompliant"`
	Unknown       int    `json:"unknown"`
	Stale         int    `json:"stale"`
	Unowned       int    `json:"unowned"`
}

// tallyDomains fills ByDomain and Domains from every service that is not a
// golden path.
func (q *Query) tallyDomains(sum *OverviewSummary) {
	type row struct {
		domain  string
		unknown bool
	}
	by := map[row]*DomainTally{}
	for _, s := range q.snap.Services {
		if s.Template {
			continue
		}
		k := row{s.BusinessDomain, s.BusinessDomainUnknown}
		dt := by[k]
		if dt == nil {
			dt = &DomainTally{Domain: k.domain, DomainUnknown: k.unknown}
			by[k] = dt
		}
		dt.Services++
		if n, _ := q.ownershipState(s); n == 0 {
			dt.Unowned++
		}
		for _, tk := range s.Targets {
			t := q.snap.Targets[tk]
			dt.Targets++
			dt.NonCompliant += b2i(t.Compliance == StatusNonCompliant)
			dt.Unknown += b2i(t.Compliance == StatusUnknown)
			dt.Stale += b2i(t.Stale)
		}
	}
	// The unknown row, then the No domain row, close the list.
	var tail []DomainTally
	for _, k := range []row{{"", true}, {"", false}} {
		if dt := by[k]; dt != nil {
			tail = append(tail, *dt)
			delete(by, k)
		}
	}
	named := make([]DomainTally, 0, len(by))
	for _, dt := range by {
		named = append(named, *dt)
	}
	slices.SortFunc(named, func(a, b DomainTally) int {
		return cmp.Or(cmp.Compare(b.NonCompliant, a.NonCompliant), cmp.Compare(b.Unknown, a.Unknown),
			cmp.Compare(b.Unowned, a.Unowned), cmp.Compare(a.Domain, b.Domain))
	})
	sum.ByDomain, sum.Domains, _ = boundSlice(named, overviewScopeLimit-len(tail))
	sum.ByDomain = append(sum.ByDomain, tail...)
}

// b2i is 1 for true and 0 for false.
func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ScopeTally counts the targets of one scope and how many of them are broken,
// unknown or stale.
type ScopeTally struct {
	Scope        string `json:"scope"`
	Targets      int    `json:"targets"`
	NonCompliant int    `json:"nonCompliant"`
	Unknown      int    `json:"unknown"`
	Stale        int    `json:"stale"`
}

// overviewScopeLimit is how many scopes the overview breaks the targets down by.
const overviewScopeLimit = 8

// tallyScopes fills ByScope and Scopes from every target.
func (q *Query) tallyScopes(sum *OverviewSummary) {
	by := map[string]*ScopeTally{}
	for _, t := range q.snap.Targets {
		st := by[t.Scope]
		if st == nil {
			st = &ScopeTally{Scope: t.Scope}
			by[t.Scope] = st
		}
		st.Targets++
		switch t.Compliance {
		case StatusNonCompliant:
			st.NonCompliant++
		case StatusUnknown:
			st.Unknown++
		}
		if t.Stale {
			st.Stale++
		}
	}
	all := make([]ScopeTally, 0, len(by))
	for _, st := range by {
		all = append(all, *st)
	}
	slices.SortFunc(all, func(a, b ScopeTally) int {
		return cmp.Or(cmp.Compare(b.NonCompliant, a.NonCompliant), cmp.Compare(b.Unknown, a.Unknown), cmp.Compare(a.Scope, b.Scope))
	})
	sum.ByScope, sum.Scopes, _ = boundSlice(all, overviewScopeLimit)
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

// Overview is the product-oriented operational summary. It answers "what needs
// attention", "which sources are
// incomplete", "what evidence arrived recently" and "where to go next". Source
// health is carried once, bounded, in Meta.Sources (with Meta.SourcesTruncated);
// there is no separate unbounded source list, so no product answer ever copies
// every source without a hard bound. Attention and RecentEvidence are explicit
// bounded previews carrying the TRUE total, count and truncation, so a consumer can
// tell "10 of 10" from "10 of 500"; neither is a raw bounded
// array that silently discards its total.
type Overview struct {
	Meta           ProductMeta      `json:"meta"`
	Summary        OverviewSummary  `json:"summary"`
	Attention      AttentionPreview `json:"attention"`
	RecentEvidence EvidencePreview  `json:"recentEvidence"`
	EntryPoints    []EntryPoint     `json:"entryPoints"`
}

// Overview computes the product landing summary in a single pass over the
// immutable snapshot. The golden paths and their revisions are counted apart.
func (q *Query) Overview() *Overview {
	ov := &Overview{Meta: q.productMeta(), EntryPoints: []EntryPoint{}}
	sum := &ov.Summary
	for _, s := range q.snap.Services {
		if s.Template {
			sum.GoldenPaths++
			sum.InvalidGoldenPaths += b2i(s.Status == StatusInvalid)
			continue
		}
		sum.Services++
		q.addOwnership(&sum.Ownership, s)
	}

	var recent []EvidenceItem
	for _, r := range q.snap.Revisions {
		if q.goldenPath(r.ServiceKey) {
			continue
		}
		sum.Revisions++
		if r.validated && !r.Valid {
			sum.InvalidRevisions++
		}
		sum.Readiness.add(r.Readiness)
	}
	sum.Targets = len(q.snap.Targets)
	var link LinkTally
	var comp ComplianceTally
	for _, t := range q.snap.Targets {
		link.add(t)
		comp.add(t.Compliance)
		sum.Evidence.add(t)
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
	sum.UnknownTargets, sum.WarningTargets = comp.Unknown, comp.Warning
	sum.InvalidTargets, sum.ReferenceTargets = comp.Invalid, comp.Reference
	sum.NotEvaluatedTargets, sum.OtherComplianceTargets = comp.NotEvaluated, comp.Other
	q.tallyRelationships(sum)
	q.tallySources(sum)
	q.tallyScopes(sum)
	q.tallyDomains(sum)

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
// (undeclared runtime) relationships, by the one declared/observed split in
// [Query.observedOnly].
func (q *Query) tallyRelationships(sum *OverviewSummary) {
	for i := range q.snap.Relationships {
		rel := q.snap.Relationships[i]
		// Dependencies only: the tallies count unresolved and out-of-range dependencies.
		if rel.Type == RelationshipDependency && rel.Provenance == ProvenanceDeclared {
			if rel.missing() {
				sum.UnresolvedRelationships++
			}
			if rel.RangeStatus == RangeUnsatisfied {
				sum.UnsatisfiedRanges++
			}
		}
	}
	sum.ObservedOnlyRelationships = len(q.observedOnly(""))
	sum.UncontractedCallers = len(q.snap.UncontractedCallers)
}

// observedOnly returns the observed relationships from the service (every
// service when from is empty) for which no declared dependency exists for the
// same (from, to) service pair. It is the one rule the overview, the service
// summary and the undeclared attention items count by.
func (q *Query) observedOnly(from ServiceKey) []Relationship {
	mine := func(rel Relationship) bool { return rel.ToService != "" && (from == "" || rel.FromService == from) }
	declared := map[[2]ServiceKey]bool{}
	for _, rel := range q.snap.Relationships {
		// Dependencies only: an observed call pairs with a declared dependency, never a reference.
		if rel.Type == RelationshipDependency && rel.Provenance != ProvenanceObserved && mine(rel) {
			declared[[2]ServiceKey{rel.FromService, rel.ToService}] = true
		}
	}
	var out []Relationship
	for _, rel := range q.snap.Relationships {
		if rel.Provenance == ProvenanceObserved && mine(rel) && !declared[[2]ServiceKey{rel.FromService, rel.ToService}] {
			out = append(out, rel)
		}
	}
	return out
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
// or an unresolved declared dependency. A golden path is no service.
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
		// Dependencies only: unresolved and out-of-range attention judges dependencies.
		if rel.Type == RelationshipDependency && rel.Provenance == ProvenanceDeclared && (rel.missing() || rel.RangeStatus == RangeUnsatisfied) {
			need[rel.FromService] = true
		}
	}
	n := 0
	for k := range need {
		if q.populationService(k) {
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
		{Label: "Dependencies outside their accepted range", Count: sum.UnsatisfiedRanges, View: EntryPointAttention, Category: categoryOutOfRange, Severity: severityWarning},
		// One tile for both kinds of undeclared item, so it opens a list of exactly
		// Count rows; graded like its most severe item.
		{Label: "Undeclared runtime dependencies observed", Count: sum.ObservedOnlyRelationships + sum.UncontractedCallers, View: EntryPointAttention, Category: categoryUndeclared, Severity: undeclaredSeverity(sum)},
		{Label: "Services with no declared owner", Count: sum.Ownership.Unowned, View: EntryPointAttention, Category: categoryUnowned, Severity: severityInfo},
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

// undeclaredSeverity grades the undeclared tile like the items it opens: an
// observed-only call is a warning, an uncontracted caller is info.
func undeclaredSeverity(sum *OverviewSummary) string {
	if sum.ObservedOnlyRelationships > 0 {
		return severityWarning
	}
	return severityInfo
}

// AttentionItem is one attention row. Every item carries the
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
	Category string `json:"category" enum:"non-compliant,unknown,stale,invalid,readiness,unresolved,out-of-range,undeclared,golden-path,unowned"`
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
	categoryOutOfRange   = "out-of-range"
	categoryUndeclared   = "undeclared"
	categoryUnowned      = "unowned"
	categoryGoldenPath   = "golden-path"
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
	Category string
	Kind     string
	Key      string
	Service  string
	// Owner is free-text owner SEARCH and OwnerKey is exact canonical owner
	// IDENTITY, exactly as on [EntityFilter]: a backlog opened from an owner page
	// carries OwnerKey, so it holds that owner's estate and not every owner whose
	// name contains theirs. They compose.
	Owner    string
	OwnerKey string
	// BusinessDomain keeps the items of services in this business domain, of
	// services with none for NoBusinessDomain, or of services whose domain is
	// unknown for UnknownBusinessDomain.
	BusinessDomain string
	Source         string
	Severity       string
	Status         string
	StaleOnly      bool
	Limit          int
	Offset         int
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
	if err := validatePaging(f.Limit, f.Offset); err != nil {
		return err
	}
	return validateFields([]fieldCheck{
		{"kind", f.Kind, "not a known entity kind", func(k string) bool { return validEntityKind(EntityKind(k)) }},
		{"status", f.Status, "not a canonical status", ValidStatus},
		{"category", f.Category, "not an attention category", func(c string) bool { return slices.Contains(attentionCategoryOrder, c) }},
		{"severity", f.Severity, "not error, warning or info", func(s string) bool { return slices.Contains(attentionSeverityLevels, s) }},
	})
}

// attentionSeverityLevels are the severities an attention item carries.
var attentionSeverityLevels = []string{"error", "warning", "info"}

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
	// A bare service name resolves to its key the way the graph resolves it; a
	// name several domains share is an error, never a silent empty answer.
	if f.Service != "" {
		var amb *AmbiguousError
		if svc, err := q.resolveService(f.Service); err == nil {
			f.Service = string(svc.Key)
		} else if errors.As(err, &amb) {
			return nil, err
		}
	}
	items := q.collectAttention()
	filtered := items[:0:0]
	for _, it := range items {
		if q.attentionMatches(it, f) {
			filtered = append(filtered, it)
		}
	}
	sortAttention(filtered)
	pg := Paginate(filtered, f.Limit, f.Offset, DefaultAttentionLimit, MaxAttentionLimit)
	return &AttentionList{
		Meta: q.productMeta(), Total: len(filtered), Count: len(pg.Items),
		Limit: pg.Limit, Offset: pg.Offset, Truncated: pg.Truncated, NextOffset: pg.NextOffset, Items: pg.Items,
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

// attentionCategoryOrder is the canonical display order of the triage
// categories, worst first. It mirrors the AttentionItem.Category enum.
var attentionCategoryOrder = []string{"non-compliant", "invalid", "unknown", "stale", "unresolved", "out-of-range", "undeclared", "golden-path", "readiness", "unowned"}

// AttentionCategoriesList returns the canonical display order of the triage
// categories, worst first, as a fresh copy the caller may modify.
func AttentionCategoriesList() []string { return slices.Clone(attentionCategoryOrder) }

func attentionCategories(items []AttentionItem) []AttentionCategoryCount {
	counts := map[string]int{}
	for i := range items {
		counts[items[i].Category]++
	}
	out := make([]AttentionCategoryCount, 0, len(attentionCategoryOrder))
	for _, c := range attentionCategoryOrder {
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

// collectAttention gathers every attention item from the snapshot, none about a
// golden path: it runs nowhere, so nothing about it needs attention.
func (q *Query) collectAttention() []AttentionItem {
	var items []AttentionItem
	for _, t := range q.snap.Targets {
		items = append(items, q.targetAttention(t)...)
	}
	for _, r := range q.snap.Revisions {
		items = append(items, q.revisionAttention(r)...)
	}
	items = append(items, q.unresolvedAttention()...)
	items = append(items, q.unownedAttention()...)
	items = append(items, q.undeclaredAttention()...)
	items = append(items, q.deployedTemplateAttention()...)
	return slices.DeleteFunc(items, func(it AttentionItem) bool { return q.goldenPath(ServiceKey(it.Service)) })
}

// undeclaredAttention reports each observed-only runtime call against the
// calling service, and each uncontracted caller against the service it calls,
// so its total is the undeclared Overview tile's count.
func (q *Query) undeclaredAttention() []AttentionItem {
	var out []AttentionItem
	for _, rel := range q.observedOnly("") {
		from, to := q.snap.Services[rel.FromService].Name, q.snap.Services[rel.ToService].Name
		out = append(out, AttentionItem{
			Entity: q.serviceRef(rel.FromService), Service: string(rel.FromService), Label: from + " calls " + to,
			Severity: severityWarning, Code: string(finding.CodeDependencyUndeclared), Category: categoryUndeclared,
			Summary:  "Runtime call with no declared dependency",
			Reason:   "observed calls from " + from + " to " + to + ", and no contract revision of " + from + " declares " + to,
			NextStep: "Declare " + to + " in " + from + "'s contract, or find out why " + from + " calls it",
		})
	}
	for _, c := range q.snap.UncontractedCallers {
		to := q.snap.Services[c.ToService].Name
		it := AttentionItem{
			Entity: q.serviceRef(c.ToService), Service: string(c.ToService), Label: c.Caller + " calls " + to,
			Severity: severityInfo, Code: "UNCONTRACTED_CALLER", Category: categoryUndeclared,
			Summary:  "Caller with no contract",
			Reason:   c.Caller + " calls " + to + " and no contract describes " + c.Caller,
			NextStep: "Give " + c.Caller + " a contract, or find out why it calls " + to,
		}
		if c.Ambiguous {
			it.Summary = "Caller matches more than one service"
			it.Reason = c.Caller + " matches more than one service, so Pacto cannot tell which one calls " + to
			it.NextStep = "Make " + c.Caller + "'s name match one contracted service"
		}
		out = append(out, it)
	}
	return out
}

// unownedAttention reports each service no source knows an owner for (no revision
// declares one and no observing source reports one), by the same rule as the
// ownership tally, so its total is OverviewSummary.Ownership.Unowned.
func (q *Query) unownedAttention() []AttentionItem {
	var out []AttentionItem
	for _, s := range q.snap.Services {
		if n, _ := q.ownershipState(s); n != 0 {
			continue
		}
		it := AttentionItem{
			Entity: serviceEntityRef(s), Service: string(s.Key), Label: s.Name,
			Severity: severityInfo, Code: "UNOWNED_SERVICE", Category: categoryUnowned,
			Summary: "Service has no declared owner", Reason: "no contract revision of this service declares service.owner, and no source reports an owner for it",
			NextStep: "Declare service.owner in the contract",
		}
		// Unreadable is not ownerless: say what Pacto cannot tell, never that the
		// contract lacks an owner block it may well have.
		if q.ownershipUnknown(s) {
			it.Summary = "Owner unknown: no contract of this service could be read"
			it.Reason = "Pacto could not read any contract revision of this service, so it cannot tell whether an owner is declared"
			it.NextStep = "Make a contract revision of this service readable"
		}
		out = append(out, it)
	}
	return out
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

// unresolvedAttention reports each unresolved declared dependency, and each one
// whose provider is in the fleet at a version outside the declared range,
// against the service that declares it: a navigable service entity, never a
// dead pseudo-edge.
func (q *Query) unresolvedAttention() []AttentionItem {
	var out []AttentionItem
	for i := range q.snap.Relationships {
		rel := q.snap.Relationships[i]
		// Dependencies only: the unresolved and out-of-range attention judges dependencies.
		if rel.Type != RelationshipDependency || rel.Provenance != ProvenanceDeclared {
			continue
		}
		// A declared dependency's FromService always exists: it is the service
		// whose revision declares the dependency.
		svc := q.snap.Services[rel.FromService]
		item := AttentionItem{Entity: serviceEntityRef(svc), Service: string(rel.FromService), Label: svc.Name + " depends on " + rel.To, Severity: severityWarning}
		switch {
		case rel.missing():
			item.Code, item.Category = "UNRESOLVED_DEPENDENCY", categoryUnresolved
			item.Summary = "Declared dependency is unresolved"
			item.Reason = "the declared dependency " + rel.To + " is not resolved in the fleet"
			item.NextStep = "Publish or resolve the dependency"
		case rel.RangeStatus == RangeUnsatisfied:
			versions := providerVersions(q.snap, rel)
			sort.Strings(versions)
			item.Code, item.Category = LimitationRangeUnsatisfied, categoryOutOfRange
			item.Summary = "Dependency version is outside the accepted range"
			item.Reason = svc.Name + " accepts " + rel.To + " " + rel.Compatibility + ", but the fleet holds " + rel.To + " " + strings.Join(versions, ", ")
			item.NextStep = "Ship a version of " + rel.To + " inside the range, or widen the range in " + svc.Name + "'s contract"
		default:
			continue
		}
		out = append(out, item)
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
	return (f.Owner == "" || q.serviceOwnedBy(it.Service, f.Owner)) &&
		(f.OwnerKey == "" || q.serviceOwnedByKey(it.Service, f.OwnerKey)) &&
		(f.BusinessDomain == "" || q.serviceInBusinessDomain(it.Service, f.BusinessDomain))
}

// serviceInBusinessDomain reports whether the service is in the business domain,
// with NoBusinessDomain matching a service known to have none and
// UnknownBusinessDomain one whose domain Pacto cannot tell.
func (q *Query) serviceInBusinessDomain(key, want string) bool {
	s := q.snap.Services[ServiceKey(key)]
	switch {
	case s == nil:
		return false
	case want == NoBusinessDomain:
		return s.BusinessDomain == "" && !s.BusinessDomainUnknown
	case want == UnknownBusinessDomain:
		return s.BusinessDomainUnknown
	default:
		return s.BusinessDomain == want
	}
}

// optEq reports whether an optional filter value (empty means unset) matches have.
func optEq(want, have string) bool { return want == "" || want == have }

// serviceOwnedBy searches a service's owner declarations and serviceOwnedByKey
// identifies them, through the one rule in [Query.ownerClaims] — a team that
// co-owns a service must see that service's attention items, not be told they are
// somebody else's problem, and a team must not be handed the backlog of an owner
// whose name merely contains theirs.
func (q *Query) serviceOwnedBy(key, owner string) bool {
	return q.matchOwner(q.snap.Services[ServiceKey(key)], owner)
}

func (q *Query) serviceOwnedByKey(key, ownerKey string) bool {
	return q.matchOwnerKey(q.snap.Services[ServiceKey(key)], ownerKey)
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
