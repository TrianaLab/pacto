package fleet

import (
	"sort"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/finding"
	"github.com/trianalab/pacto/v3/pkg/readiness"
)

// Backend-authoritative aggregates over a COMPLETE population.
//
// Every distribution a product surface draws must come from here, never from a
// bounded preview: a preview is capped at MaxDetailPreview, so counting its Items
// would silently turn "the first 200 targets" into "the fleet". These tallies are
// computed over the whole snapshot slice and their buckets are exhaustive, so a
// consumer can render a proportional visual without inventing a remainder.

// ComplianceTally is the COMPLETE compliance distribution over an evaluated
// population: every canonical status gets its own bucket, plus one explicit
// catch-all, so the buckets always sum to the population size. Without the
// catch-all a consumer drawing a distribution has to guess whether the missing
// slice is a legacy status or a bug.
//
// All seven canonical states are named, not just the four Compliance 2.0 ones.
// The tally used to fold Warning, Reference and NotEvaluated into Other, which was
// harmless while only TARGETS were tallied -- a target is always evaluated -- and
// wrong the moment a SERVICE population was, because a service with nothing running
// is NotEvaluated and that is the common case, not an anomaly. Rendering a third of
// a fleet as an unnamed grey "Other" teaches the taxonomy wrong.
type ComplianceTally struct {
	Compliant    int `json:"compliant"`
	NonCompliant int `json:"nonCompliant"`
	Unknown      int `json:"unknown"`
	Warning      int `json:"warning"`
	Invalid      int `json:"invalid"`
	Reference    int `json:"reference"`
	NotEvaluated int `json:"notEvaluated"`
	Other        int `json:"other"`
}

func (c *ComplianceTally) add(status string) {
	switch status {
	case StatusCompliant:
		c.Compliant++
	case StatusNonCompliant:
		c.NonCompliant++
	case StatusUnknown:
		c.Unknown++
	case StatusWarning:
		c.Warning++
	case StatusInvalid:
		c.Invalid++
	case StatusReference:
		c.Reference++
	case StatusNotEvaluated:
		c.NotEvaluated++
	default:
		c.Other++
	}
}

// Total is the population the tally covers.
func (c ComplianceTally) Total() int {
	return c.Compliant + c.NonCompliant + c.Unknown + c.Warning +
		c.Invalid + c.Reference + c.NotEvaluated + c.Other
}

// LinkTally is the COMPLETE revision-match certainty distribution over a target
// population. It answers "how confidently does the fleet know which revision each
// target is running" and is orthogonal to compliance and to content
// retrievability (see [RevisionIdentity]).
type LinkTally struct {
	Exact      int `json:"exact"`
	Inferred   int `json:"inferred"`
	Ambiguous  int `json:"ambiguous"`
	Unresolved int `json:"unresolved"`
}

// add buckets one target by the SAME rule targetLinkState uses, so a distribution
// and a per-target badge can never disagree.
func (l *LinkTally) add(t *TargetRecord) {
	switch targetLinkState(t) {
	case "exact":
		l.Exact++
	case "inferred":
		l.Inferred++
	case "ambiguous":
		l.Ambiguous++
	default:
		l.Unresolved++
	}
}

// Total is the population the tally covers.
func (l LinkTally) Total() int { return l.Exact + l.Inferred + l.Ambiguous + l.Unresolved }

// SeverityTally is the COMPLETE finding-severity distribution over a population
// of findings.
type SeverityTally struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Infos    int `json:"infos"`
	Unknown  int `json:"unknown"`
}

func (s *SeverityTally) add(sev finding.Severity) {
	switch sev {
	case finding.SeverityError:
		s.Errors++
	case finding.SeverityWarning:
		s.Warnings++
	case finding.SeverityInfo:
		s.Infos++
	default:
		s.Unknown++
	}
}

// Total is the population the tally covers.
func (s SeverityTally) Total() int { return s.Errors + s.Warnings + s.Infos + s.Unknown }

// EvidenceWindow is the freshness envelope of a target population: how many
// targets carry evidence at all, how many are stale, and the oldest and newest
// evidence timestamps. Targets that carry NO evidence are counted separately and
// never folded into "stale": absence of evidence is not evidence of staleness.
//
// Stale is a strict SUBSET of WithEvidence (staleness is only ever set from an
// evidence timestamp older than the freshness window, so a target with no evidence
// is never stale), which makes fresh / stale / no-evidence a partition of the
// population: (WithEvidence-Stale) + Stale + WithoutEvidence == the whole.
type EvidenceWindow struct {
	WithEvidence    int        `json:"withEvidence"`
	WithoutEvidence int        `json:"withoutEvidence"`
	Stale           int        `json:"stale"`
	Quarantined     int        `json:"quarantined"`
	Oldest          *time.Time `json:"oldest,omitempty"`
	Newest          *time.Time `json:"newest,omitempty"`
}

func (e *EvidenceWindow) add(t *TargetRecord) {
	if t.Stale {
		e.Stale++
	}
	if t.Quarantined {
		e.Quarantined++
	}
	if t.EvidenceAt == nil {
		e.WithoutEvidence++
		return
	}
	e.WithEvidence++
	if e.Oldest == nil || t.EvidenceAt.Before(*e.Oldest) {
		e.Oldest = copyTime(t.EvidenceAt)
	}
	if e.Newest == nil || t.EvidenceAt.After(*e.Newest) {
		e.Newest = copyTime(t.EvidenceAt)
	}
}

// OwnershipTally partitions a SERVICE population by how well ownership is
// DECLARED on it: Consistent + Conflicting + Unowned == the population.
//
// Ownership is authored per revision (`service.owner` in the contract), so a
// service's ownership is a property of its revisions agreeing rather than of one
// field somebody set. A service is Consistent when every revision that declares an
// owner declares the SAME one; a revision that declares none is silence, not a
// contradiction. "The same one" is [ownerClaimSet]'s identity — the canonical owner
// key, the one the product routes by — so a team declared with two different DRIs
// is one owner. Both this tally and the OWNER_CONFLICT limitation [deriveOwner]
// raises count through that set, so they can never disagree.
//
// Conflicting is never folded into Unowned. "Two teams claim this" and "nobody
// claims this" are different answers to "who do I page", and they need opposite
// fixes. Neither is folded into Consistent either: ServiceRecord.Owner is only a
// documented summary (the lowest-keyed revision's owner), so counting a conflicted
// service as owned would present that tie-break as agreement.
type OwnershipTally struct {
	Consistent  int `json:"consistent"`
	Conflicting int `json:"conflicting"`
	Unowned     int `json:"unowned"`
}

// Total is the population the tally covers.
func (o OwnershipTally) Total() int { return o.Consistent + o.Conflicting + o.Unowned }

// ReadinessTally partitions a CONTRACT REVISION population by its declared
// readiness assessment. The unit is always the revision: readiness is authored
// preparedness of one immutable contract, never a property of a service, of a
// running target or of the fleet.
//
// It is orthogonal to compliance, and deliberately so — a revision whose readiness
// passes can still be running on a target observed to violate its contract, and a
// revision nobody assessed can be running perfectly. Every bucket is a state
// [readiness.Evaluate] already computes, so there is no invented middle tier.
// NotDeclared is its own bucket because "nobody wrote an assessment" is not the
// same answer as "the assessment does not pass".
type ReadinessTally struct {
	Passing int `json:"passing"`
	// BelowThreshold: assessed, in date, and scoring under the contract's own
	// minScore.
	BelowThreshold int `json:"belowThreshold"`
	// Expired: assessed, but past its `expires` date, so it earns no weight at all
	// and cannot be read as current.
	Expired     int `json:"expired"`
	NotDeclared int `json:"notDeclared"`
}

// add buckets one revision's assessment. Passing already excludes expiry, so the
// three assessed buckets are mutually exclusive.
func (r *ReadinessTally) add(res *readiness.Result) {
	switch {
	case res == nil:
		r.NotDeclared++
	case res.Passing:
		r.Passing++
	case res.Expired:
		r.Expired++
	default:
		r.BelowThreshold++
	}
}

// Total is the population the tally covers.
func (r ReadinessTally) Total() int {
	return r.Passing + r.BelowThreshold + r.Expired + r.NotDeclared
}

// OwnerCount is one owner's share of a service population and the operational
// targets those services carry. It is a RANKING row, not a partition bucket — see
// [EntityAggregate.ByOwner] for what the ranking leaves out.
//
// Key and Label are separate because they answer different questions. Key is the
// canonical owner identity ([contract.OwnerKey]) and is what a drill-down link
// must carry; Label is the human name, and two rows can legitimately carry the
// same one — a team and a DRI both named `alice` are two owners. Kind names the
// namespace so a reader can tell those two rows apart.
type OwnerCount struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
	// Ambiguous reports that another canonical owner ANYWHERE in the matched
	// population shares this Label, so the namespace has to be shown for the row to
	// name one owner. It is computed over the complete population rather than the
	// rows that survived the ranking cut, because a reader cannot see which owners
	// were cut: with `team:alice` ranked first and `dri:alice` eleventh, deciding
	// from the visible rows alone would print an unqualified `alice` that means a
	// different owner depending on how many owners happened to fit.
	Ambiguous bool `json:"ambiguous,omitempty"`
	Services  int  `json:"services"`
	Targets   int  `json:"targets"`
}

// maxOwnerRanking bounds the owner ranking on an entity aggregate. A fleet may
// have any number of owners and this answer travels on every list response, so the
// ranking is capped and the tail is reported as a count rather than dropped.
const maxOwnerRanking = 10

// EntityAggregate is the backend-authoritative tally of the COMPLETE population an
// [EntityFilter] matched, computed before paging. It is what a list page must draw
// a distribution from: the page it renders is one bounded slice of this
// population, and counting THAT would present the first 25 rows as the fleet.
//
// Every tally names the population it partitions instead of sharing one
// denominator, because the matched population is heterogeneous by design: a query
// for services and revisions at once has a compliance answer about the services
// and a readiness answer about the revisions, and no honest single whole. The
// per-kind counts are reported, never derived by summing buckets, so a
// disagreement between a denominator and its buckets stays visible.
type EntityAggregate struct {
	// Matched repeats EntityList.Total so a consumer holding only the aggregate
	// still has the whole it divides.
	Matched   int `json:"matched"`
	Services  int `json:"services"`
	Revisions int `json:"revisions"`
	Targets   int `json:"targets"`
	Owners    int `json:"owners"`
	Sources   int `json:"sources"`
	// ServiceCompliance partitions Services and TargetCompliance partitions Targets.
	// They are kept apart rather than summed: a service status is a roll-up of its
	// targets, so adding the two would count the same operational reality twice.
	ServiceCompliance ComplianceTally `json:"serviceCompliance"`
	TargetCompliance  ComplianceTally `json:"targetCompliance"`
	// Ownership partitions Services; Readiness partitions Revisions.
	Ownership OwnershipTally `json:"ownership"`
	Readiness ReadinessTally `json:"readiness"`
	// ByOwner ranks the CONSISTENTLY OWNED matched services by canonical owner,
	// largest first, bounded to maxOwnerRanking. It is not a partition of Services
	// and does not pretend to be: Ownership.Conflicting and Ownership.Unowned are
	// excluded, because a service two owners claim, or none, has no one owner to rank
	// under.
	//
	// The three fields below account for every consistently owned service the ranking
	// does NOT show, so a consumer can state the omission exactly rather than imply
	// the rows are the whole:
	//
	//	Sum(ByOwner.Services) + BeyondRanking + UnidentifiedOwnership == Ownership.Consistent
	//
	// They are deliberately not one "other" bucket. A service left out because its
	// owner placed eleventh is a display bound and a reader can go and find it; a
	// service left out because its owner declared nothing but an email address is a
	// gap in the fleet's ownership data and no amount of scrolling reveals a page for
	// it. Mixing them would report a data gap as a pagination detail.
	ByOwner []OwnerCount `json:"byOwner,omitempty"`
	// BeyondRanking counts consistently owned services whose canonical owner exists
	// but fell past maxOwnerRanking.
	BeyondRanking int `json:"beyondRanking,omitempty"`
	// UnidentifiedOwnership counts consistently owned services whose single owner
	// declaration carries NO canonical identity — the contract permits an owner block
	// of contacts alone, and an email address or a chat channel is a way to reach
	// somebody, not a name to file the fleet under. Ownership is declared and there
	// is nothing to navigate to.
	UnidentifiedOwnership int `json:"unidentifiedOwnership,omitempty"`
	// RankedOwners is the number of canonical owners the ranking is drawn from: those
	// with at least one consistently owned service in the matched population. It is
	// >= len(ByOwner), and the difference is the ranking tail.
	RankedOwners int `json:"rankedOwners,omitempty"`
	// DistinctOwners is every canonical owner identity the matched services declare,
	// including owners that appear ONLY on services their revisions disagree about.
	// It is therefore >= RankedOwners and is the honest answer to "how many owners
	// are in this population" — an owner in a dispute is still an owner, and counting
	// only the rankable ones would erase exactly the owners a reader is looking for.
	DistinctOwners int `json:"distinctOwners,omitempty"`
}

// ownerClaimsOf collects the DISTINCT owners a service's revisions declare, under
// the canonical owner identity the product routes by (see [ownerClaimSet]). One
// walk answers three questions that must never disagree: how the service is
// partitioned, which owner it ranks under, and which owners are present at all.
func (q *Query) ownerClaimsOf(s *ServiceRecord) ownerClaimSet {
	var claims ownerClaimSet
	for _, rk := range s.Revisions {
		claims.add(q.snap.Revisions[rk].Owner)
	}
	return claims
}

// ownershipState classifies a service's declared ownership: how many DISTINCT
// owners its revisions declare, and the canonical owner KEY when they declare
// exactly one. The count is the classification (0 unowned, 1 consistent, more
// conflicting); the key is only for ranking and linking, and it is empty for an
// owner that carries contacts but neither a team nor a DRI. Such a service is
// consistently owned and simply has nothing to rank under — which is why the count
// and the key are returned separately rather than an empty key standing in for
// "no owner". [EntityAggregate.UnidentifiedOwnership] is where those services are
// accounted for.
func (q *Query) ownershipState(s *ServiceRecord) (distinctOwners int, ownerKey string) {
	claims := q.ownerClaimsOf(s)
	return claims.len(), claims.soleKey()
}

// addOwnership buckets one service into the ownership partition and returns the
// canonical key it is consistently owned by (empty when conflicted, unowned or
// unidentified), so a caller ranking owners classifies by the SAME rule the
// partition uses.
func (q *Query) addOwnership(o *OwnershipTally, s *ServiceRecord) string {
	n, key := q.ownershipState(s)
	switch n {
	case 0:
		o.Unowned++
	case 1:
		o.Consistent++
	default:
		o.Conflicting++
	}
	return key
}

// aggregate tallies the complete matched population. refs are the filtered
// references BEFORE paging, which is the whole point: this is the denominator the
// page is a slice of.
func (q *Query) aggregate(refs []EntityRef) EntityAggregate {
	agg := EntityAggregate{Matched: len(refs)}
	byOwner := map[string]*OwnerCount{}
	present := map[string]bool{}
	for _, r := range refs {
		switch r.Kind {
		case KindService:
			agg.Services++
			s := q.snap.Services[ServiceKey(r.Key)]
			agg.ServiceCompliance.add(s.Status)
			claims := q.ownerClaimsOf(s)
			// Every canonical owner the service names is present in the population,
			// including the two sides of a dispute — neither of which can rank.
			for _, k := range claims.keys {
				present[k] = true
			}
			switch n := claims.len(); {
			case n == 0:
				agg.Ownership.Unowned++
			case n > 1:
				agg.Ownership.Conflicting++
			default:
				agg.Ownership.Consistent++
				key := claims.soleKey()
				if key == "" {
					// Consistently owned by an owner with no canonical identity. Counted
					// here so the ranking's omission is stated rather than lost.
					agg.UnidentifiedOwnership++
					break
				}
				c := byOwner[key]
				if c == nil {
					c = newOwnerCount(key)
					byOwner[key] = c
				}
				c.Services++
				c.Targets += len(s.Targets)
			}
		case KindRevision:
			agg.Revisions++
			agg.Readiness.add(q.snap.Revisions[RevisionKey(r.Key)].Readiness)
		case KindTarget:
			agg.Targets++
			agg.TargetCompliance.add(r.Status)
		case KindOwner:
			agg.Owners++
		case KindSource:
			agg.Sources++
		}
	}
	agg.RankedOwners = len(byOwner)
	agg.DistinctOwners = len(present)
	markAmbiguousLabels(byOwner, present)
	agg.ByOwner, agg.BeyondRanking = rankOwners(byOwner)
	return agg
}

// markAmbiguousLabels flags every ranking row whose human label is shared by
// another canonical owner in the population — the whole population, not the ranked
// subset, so the flag cannot flip as rows cross the ranking cut. `present` is every
// canonical owner the matched services name, including owners that only appear in a
// dispute: `alice` is no less ambiguous because the other alice happens to own
// nothing consistently.
func markAmbiguousLabels(byOwner map[string]*OwnerCount, present map[string]bool) {
	labels := make(map[string]int, len(present))
	for key := range present {
		labels[ownerLabelOf(key)]++
	}
	for _, c := range byOwner {
		if labels[c.Label] > 1 {
			c.Ambiguous = true
		}
	}
}

// ownerLabelOf is the human label a canonical key presents as, matching
// [newOwnerCount] so the two never disagree about what is ambiguous with what.
func ownerLabelOf(key string) string {
	label := key
	if k, err := contract.ParseOwnerKey(key); err == nil {
		label = k.Value
	}
	return label
}

// newOwnerCount builds a ranking row from a canonical key, splitting the identity
// back into the label and namespace a reader needs to tell two same-named owners
// apart. A key that fails to parse cannot be produced by [contract.Owner.Key], so
// it falls back to showing itself rather than inventing a nicer name.
func newOwnerCount(key string) *OwnerCount {
	c := &OwnerCount{Key: key, Label: ownerLabelOf(key)}
	if k, err := contract.ParseOwnerKey(key); err == nil {
		c.Kind = string(k.Kind)
	}
	return c
}

// rankOwners orders the owner buckets largest first (ties by canonical key, so the
// order is deterministic even between two owners with the same label) and cuts the
// ranking at maxOwnerRanking, returning the services the cut dropped rather than
// losing them.
func rankOwners(m map[string]*OwnerCount) ([]OwnerCount, int) {
	out := make([]OwnerCount, 0, len(m))
	for _, c := range m {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Services != out[j].Services {
			return out[i].Services > out[j].Services
		}
		return out[i].Key < out[j].Key
	})
	if len(out) <= maxOwnerRanking {
		return out, 0
	}
	other := 0
	for _, c := range out[maxOwnerRanking:] {
		other += c.Services
	}
	return out[:maxOwnerRanking], other
}

// ServiceSummary is the complete, backend-authoritative aggregate a service page
// draws its operational picture from. Every counter covers ALL of the service's
// targets, revisions and edges — the sibling previews on ServiceDetailData are
// bounded and must never be counted instead.
type ServiceSummary struct {
	Targets    int             `json:"targets"`
	Compliance ComplianceTally `json:"compliance"`
	Links      LinkTally       `json:"links"`
	Evidence   EvidenceWindow  `json:"evidence"`
	Findings   SeverityTally   `json:"findings"`
	// Revisions is every known revision of the service; RevisionsInUse is how many
	// of them at least one target is linked to (exact or inferred). The gap between
	// them is the "how much of what we know is actually running" question the old
	// version list answered and the entity model dropped.
	Revisions        int `json:"revisions"`
	InvalidRevisions int `json:"invalidRevisions"`
	RevisionsInUse   int `json:"revisionsInUse"`
	// Declared/Observed/Reconciled describe this service's OUTGOING dependency
	// edges. DeclaredNotObserved and ObservedNotDeclared are the two drift
	// directions, kept apart because they mean opposite things: a declared edge
	// nobody exercises versus runtime traffic nobody declared.
	DeclaredDependencies int `json:"declaredDependencies"`
	UnresolvedDeclared   int `json:"unresolvedDeclared"`
	ReconciledMatched    int `json:"reconciledMatched"`
	DeclaredNotObserved  int `json:"declaredNotObserved"`
	ObservedNotDeclared  int `json:"observedNotDeclared"`
}

// OwnerSummary is the complete, backend-authoritative aggregate an owner page
// draws its posture from. Every counter covers ALL of the owner's services,
// revisions and targets — the sibling previews on OwnerDetailData are bounded and
// must never be counted instead.
//
// It deliberately carries the SAME dimensions as [ServiceSummary] rather than a
// bespoke owner score: an owner page and a service page then answer the same three
// orthogonal questions (does it obey the contract, do we know which revision runs,
// how recently did we look) in one visual language, instead of teaching a second
// vocabulary for the same facts. There is no composite "owner health" number for
// the same reason there is no service one.
//
// The populations are exactly the previews' populations: Targets are the targets of
// the services this owner owns, and Revisions are the revisions that declare this
// owner — which is not always the same set, because a revision may declare an owner
// other than its service's (that disagreement is the ownership conflict the service
// page reports).
type OwnerSummary struct {
	Services         int             `json:"services"`
	Revisions        int             `json:"revisions"`
	InvalidRevisions int             `json:"invalidRevisions"`
	Targets          int             `json:"targets"`
	Compliance       ComplianceTally `json:"compliance"`
	Links            LinkTally       `json:"links"`
	Evidence         EvidenceWindow  `json:"evidence"`
	Findings         SeverityTally   `json:"findings"`
}

// addTarget tallies one target into every target-scoped dimension at once, so a
// caller cannot update three of the four and ship a summary whose bars disagree.
func (o *OwnerSummary) addTarget(t *TargetRecord) {
	o.Compliance.add(t.Compliance)
	o.Links.add(t)
	o.Evidence.add(t)
	for _, f := range t.Findings {
		o.Findings.add(f.Severity)
	}
}

// serviceSummary aggregates the service's complete target, revision and edge
// populations. inUse is the set of revisions at least one target links to.
func (q *Query) serviceSummary(view *ServiceView) (ServiceSummary, map[RevisionKey]bool) {
	sum := ServiceSummary{Targets: len(view.Targets), Revisions: len(view.Revisions)}
	inUse := map[RevisionKey]bool{}
	for _, t := range view.Targets {
		sum.Compliance.add(t.Compliance)
		sum.Links.add(t)
		sum.Evidence.add(t)
		for _, f := range t.Findings {
			sum.Findings.add(f.Severity)
		}
		if t.ContractRevision != "" {
			inUse[t.ContractRevision] = true
		}
	}
	for _, r := range view.Revisions {
		// A ServiceView carries CLONES, and a clone is produced through JSON, so it
		// loses the unexported "was this revision actually validated" flag: read
		// validity from the authoritative snapshot record instead. Counting it off the
		// clone silently reported every service as having zero invalid revisions.
		if rec := q.snap.Revisions[r.Key]; rec != nil && revisionStatus(rec) == StatusInvalid {
			sum.InvalidRevisions++
		}
		if inUse[r.Key] {
			sum.RevisionsInUse++
		}
	}
	q.tallyServiceEdges(&sum, view.Service.Key)
	return sum, inUse
}

// tallyServiceEdges counts the service's outgoing declared dependencies and the
// two drift directions. An observed edge is "observed, not declared" iff no
// declared edge exists for the same (from, to) service pair — the same
// declared/observed split the engine keeps.
//
// Every counter is over DISTINCT dependencies, not over declaration records. A
// declared relationship is per-revision (Relationship.FromRevision), so counting
// records made a service with five revisions of three dependencies report thirteen
// declared dependencies beside its own "Expected dependencies 3 of 3" list — a number
// that grew with release history rather than with the dependency set.
//
// Collapsing is lossless for reconciliation, which reconcileDeclared computes purely
// from the (FromService, ToService) pair and so is identical for every revision that
// declares the same dependency. Resolution is per-record, so a dependency counts as
// unresolved if ANY declaring revision failed to resolve it: a resolution failure in
// one revision is not erased by a newer revision that pins it.
func (q *Query) tallyServiceEdges(sum *ServiceSummary, key ServiceKey) {
	// An unresolved dependency has no ToService, so the raw ref keys it instead —
	// otherwise every unresolvable dependency would collapse onto one empty key.
	depKey := func(rel Relationship) string {
		if rel.ToService != "" {
			return "svc:" + string(rel.ToService)
		}
		return "ref:" + rel.To
	}
	declared := map[ServiceKey]bool{}
	seen := map[string]bool{}
	unresolved := map[string]bool{}
	for i := range q.snap.Relationships {
		rel := q.snap.Relationships[i]
		if rel.FromService != key || rel.Type != RelationshipDependency {
			continue
		}
		if rel.Provenance == ProvenanceObserved {
			continue
		}
		declared[rel.ToService] = true
		dk := depKey(rel)
		if !rel.Resolved && !unresolved[dk] {
			unresolved[dk] = true
			sum.UnresolvedDeclared++
		}
		if seen[dk] {
			continue
		}
		seen[dk] = true
		sum.DeclaredDependencies++
		switch rel.Reconciliation {
		case ReconciliationMatched:
			sum.ReconciledMatched++
		case ReconciliationDeclaredNotObserved:
			sum.DeclaredNotObserved++
		}
	}
	for i := range q.snap.Relationships {
		rel := q.snap.Relationships[i]
		if rel.FromService != key || rel.Provenance != ProvenanceObserved {
			continue
		}
		if !declared[rel.ToService] {
			sum.ObservedNotDeclared++
		}
	}
}

// labelsPreview projects observed workload labels into the bounded fact preview
// shape. Labels come from a runtime source, so the emitted list, each key and each
// value are capped exactly like observed runtime facts.
func labelsPreview(m map[string]string) RuntimePreview {
	keys, all := keysBounded(m, MaxDetailPreview)
	items := make([]RuntimeFact, 0, len(keys))
	for _, k := range keys {
		items = append(items, RuntimeFact{Key: capLen(k, maxRuntimeKeyLen), Value: capLen(m[k], maxRuntimeValueLen)})
	}
	rp := RuntimePreview{Count: len(items), Scanned: len(items), Truncated: !all, Items: items}
	if all {
		total := len(items)
		rp.Total = &total
	}
	return rp
}

// chronologicalRevisionRefs returns the service's revisions NEWEST FIRST in
// canonical revision chronology (see siblingRevisions), not in RevisionKey order.
// Key order is content-digest order, which is semantically arbitrary; a version
// list sorted by digest is the regression that made revision history unreadable.
func chronologicalRevisionRefs(revs []*ContractRevision) []EntityRef {
	ordered := append([]*ContractRevision{}, revs...)
	sort.Slice(ordered, func(i, j int) bool { return lessRevisionChrono(ordered[j], ordered[i]) })
	return revisionRefs(ordered)
}
